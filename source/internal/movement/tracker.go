package movement

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

const recentLimit = 20

const (
	defaultRestoreMaxAge     = 2 * time.Minute
	defaultMaxObservationGap = 30 * time.Second
)

var ErrNonMonotonicObservation = errors.New("official movement observation is duplicate or out of order")

type Store interface {
	SaveMovementSample(context.Context, *Snapshot) error
	SaveMovementEvent(context.Context, *Event, *Snapshot, *Snapshot) error
	RecentMovementEventsBySource(context.Context, string, int) ([]Event, error)
	RecentMovementSamplesBySource(context.Context, string, int64, int) ([]SamplePoint, error)
	MovementEventSnapshot(context.Context, string, string) (*Snapshot, error)
}

type Config struct {
	WarningPP         float64
	ThresholdPP       float64
	ReleasePP         float64
	ConfirmSamples    int
	TrendLimit        int
	SampleInterval    time.Duration
	RestoreMaxAge     time.Duration
	MaxObservationGap time.Duration
	Source            string
}

func (c Config) defaults() Config {
	if c.WarningPP <= 0 {
		c.WarningPP = 0.5
	}
	if c.ThresholdPP <= 0 {
		c.ThresholdPP = 2
	}
	if c.ReleasePP <= 0 {
		c.ReleasePP = 0.5
	}
	if c.ConfirmSamples < 1 {
		c.ConfirmSamples = 2
	}
	if c.TrendLimit < 2 {
		c.TrendLimit = 180
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = 5 * time.Second
	}
	if c.RestoreMaxAge <= 0 {
		c.RestoreMaxAge = defaultRestoreMaxAge
	}
	if c.MaxObservationGap <= 0 {
		c.MaxObservationGap = defaultMaxObservationGap
	}
	if strings.TrimSpace(c.Source) == "" {
		c.Source = "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H"
	}
	return c
}

type Tracker struct {
	// writeMu orders every change to the tracker: Restore, observations and
	// notice relinks. A writer holds it for its whole run, including database
	// reads and commits, so nothing else can change the tracker between the
	// copy it works on and the result it publishes. mu guards the fields
	// themselves and is held only briefly, so readers keep answering with the
	// last published state while a commit is in flight.
	writeMu            sync.Mutex
	mu                 sync.RWMutex
	cfg                Config
	store              Store
	baseline           *Snapshot
	candidateDirection string
	candidateCount     int
	releaseCount       int
	lastObservedMS     int64
	active             *Event
	extreme            *Snapshot
	current            *Snapshot
	trend              []SamplePoint
	recent             []Event
}

func NewTracker(cfg Config, store Store) *Tracker { return &Tracker{cfg: cfg.defaults(), store: store} }

func (t *Tracker) Restore(ctx context.Context) error {
	return t.restore(ctx, time.Now())
}

// restore reads the stored state without holding mu, so readers are not held
// up by the database, and publishes it only when every read succeeded.
func (t *Tracker) restore(ctx context.Context, now time.Time) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	restoreWindow := max(30*time.Minute, t.cfg.RestoreMaxAge)
	samples, err := t.store.RecentMovementSamplesBySource(ctx, t.cfg.Source, now.Add(-restoreWindow).UnixMilli(), t.cfg.TrendLimit)
	if err != nil {
		return fmt.Errorf("restore movement samples: %w", err)
	}
	events, err := t.store.RecentMovementEventsBySource(ctx, t.cfg.Source, recentLimit)
	if err != nil {
		return fmt.Errorf("restore movement events: %w", err)
	}
	var baseline, extreme *Snapshot
	var active *Event
	var recent []Event
	lastObservedMS := int64(0)
	for _, sample := range samples {
		if sample.AsOfMS > lastObservedMS {
			lastObservedMS = sample.AsOfMS
		}
	}
	for i := range events {
		if ValidateEventDirection(&events[i]) != nil {
			continue
		}
		if events[i].State != "ACTIVE" {
			recent = append(recent, events[i])
			continue
		}
		candidate := events[i]
		age := now.UnixMilli() - candidate.UpdatedAtMS
		if active != nil || candidate.UpdatedAtMS <= 0 || age < 0 || age > t.cfg.RestoreMaxAge.Milliseconds() {
			continue
		}
		baseline, err = t.store.MovementEventSnapshot(ctx, candidate.EventID, "BASELINE")
		if err != nil {
			return err
		}
		extreme, err = t.store.MovementEventSnapshot(ctx, candidate.EventID, "CURRENT")
		if err != nil {
			return err
		}
		active = &candidate
		if candidate.UpdatedAtMS > lastObservedMS {
			lastObservedMS = candidate.UpdatedAtMS
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.baseline, t.active, t.extreme, t.current = baseline, active, extreme, nil
	t.candidateDirection, t.candidateCount, t.releaseCount = "", 0, 0
	t.trend, t.recent, t.lastObservedMS = samples, recent, lastObservedMS
	return nil
}

func (t *Tracker) Observe(ctx context.Context, snapshot *Snapshot) error {
	if snapshot == nil || snapshot.TotalVolumeKRW <= 0 {
		return fmt.Errorf("invalid official movement snapshot")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	if snapshot.AsOfMS <= 0 || snapshot.AsOfMS <= t.lastObservedMS {
		return fmt.Errorf("%w: as_of_ms=%d last_observed_ms=%d", ErrNonMonotonicObservation, snapshot.AsOfMS, t.lastObservedMS)
	}
	if t.lastObservedMS > 0 && snapshot.AsOfMS-t.lastObservedMS > t.cfg.MaxObservationGap.Milliseconds() {
		if err := t.closeContinuity(ctx, t.lastObservedMS, "ENDED_BY_OBSERVATION_GAP"); err != nil {
			return err
		}
	}
	if err := t.store.SaveMovementSample(ctx, snapshot); err != nil {
		return err
	}
	t.lastObservedMS = snapshot.AsOfMS
	t.current = cloneSnapshot(snapshot)
	t.trend = append(t.trend, SamplePoint{snapshot.AsOfMS, snapshot.BithumbSharePct, snapshot.Status})
	if len(t.trend) > t.cfg.TrendLimit {
		t.trend = append([]SamplePoint(nil), t.trend[len(t.trend)-t.cfg.TrendLimit:]...)
	}
	if t.baseline == nil {
		t.baseline = cloneSnapshot(snapshot)
		return nil
	}
	if t.active == nil {
		t.observeCandidate(snapshot)
	} else {
		t.updateActive(snapshot)
	}
	if t.active == nil {
		return nil
	}
	t.active.Drivers, t.active.ExchangeDrivers = attribute(t.baseline, t.extreme)
	if err := t.store.SaveMovementEvent(ctx, t.active, t.baseline, t.extreme); err != nil {
		return err
	}
	if t.active.State == "ENDED" {
		ended := *cloneEvent(t.active)
		t.recent = prepend(t.recent, ended, recentLimit)
		t.active, t.extreme = nil, nil
		t.resetBaseline(snapshot)
	}
	return nil
}

// ObserveGap closes the current continuity segment. A later LIVE snapshot must
// establish a new baseline and can never confirm a pre-gap candidate or extend
// a pre-gap active event.
func (t *Tracker) ObserveGap(ctx context.Context, asOfMS int64) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	if asOfMS <= 0 || asOfMS <= t.lastObservedMS {
		return fmt.Errorf("%w: as_of_ms=%d last_observed_ms=%d", ErrNonMonotonicObservation, asOfMS, t.lastObservedMS)
	}
	if err := t.closeContinuity(ctx, asOfMS, "ENDED_BY_DATA_GAP"); err != nil {
		return err
	}
	t.lastObservedMS = asOfMS
	return nil
}

func (t *Tracker) closeContinuity(ctx context.Context, endedAtMS int64, qualityStatus string) error {
	if t.active != nil {
		ended := cloneEvent(t.active)
		ended.State = "ENDED"
		ended.EndedAtMS = &endedAtMS
		ended.UpdatedAtMS = endedAtMS
		ended.QualityStatus = qualityStatus
		if err := t.store.SaveMovementEvent(ctx, ended, t.baseline, t.extreme); err != nil {
			return fmt.Errorf("close official movement at data gap: %w", err)
		}
		t.recent = prepend(t.recent, *ended, recentLimit)
	}
	t.baseline, t.active, t.extreme, t.current = nil, nil, nil, nil
	t.trend = nil
	t.resetCandidate()
	t.releaseCount = 0
	return nil
}

func (t *Tracker) observeCandidate(snapshot *Snapshot) {
	delta := snapshot.BithumbSharePct - t.baseline.BithumbSharePct
	if math.Abs(delta) < t.cfg.ThresholdPP {
		t.resetCandidate()
		return
	}
	direction := "UP"
	if delta < 0 {
		direction = "DOWN"
	}
	if direction != t.candidateDirection {
		t.candidateDirection, t.candidateCount = direction, 1
	} else {
		t.candidateCount++
	}
	if t.candidateCount < t.cfg.ConfirmSamples {
		return
	}
	t.active = newEvent(t.baseline, snapshot, direction, t.cfg.ThresholdPP)
	t.extreme = cloneSnapshot(snapshot)
	t.releaseCount = 0
}

func newEvent(baseline, current *Snapshot, direction string, threshold float64) *Event {
	now := current.AsOfMS
	delta := current.BithumbSharePct - baseline.BithumbSharePct
	return &Event{
		EventID:          fmt.Sprintf("official-aligned-24h-%s-%d", strings.ToLower(direction), now),
		Source:           current.Source,
		Direction:        direction,
		State:            "ACTIVE",
		StartedAtMS:      baseline.AsOfMS,
		ConfirmedAtMS:    &now,
		UpdatedAtMS:      now,
		BaselineSharePct: baseline.BithumbSharePct,
		CurrentSharePct:  current.BithumbSharePct,
		ExtremeSharePct:  current.BithumbSharePct,
		ChangePP:         delta,
		ExtremeChangePP:  delta,
		ThresholdPP:      threshold,
		QualityStatus:    current.Status,
		Drivers:          []Driver{},
		ExchangeDrivers:  []ExchangeDriver{},
		FlowEvidence:     []FlowEvidence{},
		Notices:          []NoticeEvidence{},
	}
}

func (t *Tracker) updateActive(snapshot *Snapshot) {
	e := t.active
	if e.State == "ENDED" {
		return
	}
	delta := snapshot.BithumbSharePct - e.BaselineSharePct
	if movementCrossedBaseline(e.Direction, delta) {
		t.releaseCount++
		if t.releaseCount < t.cfg.ConfirmSamples {
			e.QualityStatus = "RELEASE_PENDING_BASELINE_CROSS"
			return
		}
		ended := snapshot.AsOfMS
		e.State = "ENDED"
		e.EndedAtMS = &ended
		e.UpdatedAtMS = ended
		e.QualityStatus = "ENDED_BY_BASELINE_CROSS"
		return
	}
	e.UpdatedAtMS = snapshot.AsOfMS
	e.CurrentSharePct = snapshot.BithumbSharePct
	e.ChangePP = delta
	e.QualityStatus = snapshot.Status
	newExtreme := e.Direction == "UP" && snapshot.BithumbSharePct > e.ExtremeSharePct || e.Direction == "DOWN" && snapshot.BithumbSharePct < e.ExtremeSharePct
	if newExtreme {
		e.ExtremeSharePct = snapshot.BithumbSharePct
		e.ExtremeChangePP = e.ExtremeSharePct - e.BaselineSharePct
		t.extreme = cloneSnapshot(snapshot)
	}
	released := math.Abs(e.ChangePP) < t.cfg.ReleasePP
	if !released {
		t.releaseCount = 0
		return
	}
	t.releaseCount++
	if t.releaseCount >= t.cfg.ConfirmSamples {
		ended := snapshot.AsOfMS
		e.EndedAtMS = &ended
		e.State = "ENDED"
	}
}

func movementCrossedBaseline(direction string, delta float64) bool {
	return direction == "UP" && delta < 0 || direction == "DOWN" && delta > 0
}

func (t *Tracker) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	recentEvents := currentAndRecent(t.active, t.recent, 10)
	trend := append(make([]SamplePoint, 0, len(t.trend)), t.trend...)
	return State{
		Version:       "OFFICIAL_SHARE_MONITOR_V1",
		GeneratedAtMS: time.Now().UnixMilli(),
		Current:       cloneSnapshot(t.current),
		Movement:      cloneEvent(t.active),
		RecentEvents:  recentEvents,
		Trend:         trend,
		DataQuality: map[string]any{
			"source":                  t.cfg.Source,
			"sample_interval_sec":     durationSeconds(t.cfg.SampleInterval),
			"event_threshold_pp":      t.cfg.ThresholdPP,
			"release_confirm_samples": t.cfg.ConfirmSamples,
			"baseline_strategy":       "FIXED_CONTINUITY_ANCHOR",
			"restore_max_age_sec":     t.cfg.RestoreMaxAge.Seconds(),
			"max_observation_gap_sec": t.cfg.MaxObservationGap.Seconds(),
			"notice_poll_sec":         600,
		},
	}
}

// ReplaceNoticeEvidence applies a repository relink without changing movement
// direction, shares, drivers, timestamps, or lifecycle state.
func (t *Tracker) ReplaceNoticeEvidence(replacements map[string][]NoticeEvidence) {
	if len(replacements) == 0 {
		return
	}
	// A relink that arrives while an observation commits waits for it, so the
	// observation cannot publish its earlier copy over the relinked evidence.
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active != nil {
		if notices, found := replacements[t.active.EventID]; found {
			t.active.Notices = cloneNoticeEvidence(notices)
		}
	}
	for index := range t.recent {
		if notices, found := replacements[t.recent[index].EventID]; found {
			t.recent[index].Notices = cloneNoticeEvidence(notices)
		}
	}
}

func durationSeconds(value time.Duration) int64 { return max(1, int64(value/time.Second)) }

func (t *Tracker) resetBaseline(snapshot *Snapshot) {
	t.baseline = cloneSnapshot(snapshot)
	t.resetCandidate()
	t.releaseCount = 0
}

func (t *Tracker) resetCandidate() { t.candidateDirection = ""; t.candidateCount = 0 }

func prepend(events []Event, event Event, limit int) []Event {
	result := []Event{event}
	for _, row := range events {
		if row.EventID != event.EventID && len(result) < limit {
			result = append(result, row)
		}
	}
	return result
}

func currentAndRecent(active *Event, recent []Event, limit int) []Event {
	result := append(make([]Event, 0, len(recent)), recent...)
	if active != nil {
		result = prepend(result, *cloneEvent(active), limit)
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func cloneEvent(source *Event) *Event {
	if source == nil {
		return nil
	}
	result := *source
	result.Drivers = append([]Driver(nil), source.Drivers...)
	result.ExchangeDrivers = append([]ExchangeDriver(nil), source.ExchangeDrivers...)
	result.FlowEvidence = cloneFlowEvidence(source.FlowEvidence)
	result.Notices = cloneNoticeEvidence(source.Notices)
	return &result
}

func cloneFlowEvidence(source []FlowEvidence) []FlowEvidence {
	result := append([]FlowEvidence(nil), source...)
	for index := range result {
		result[index].Payload = append([]byte(nil), source[index].Payload...)
	}
	return result
}

func cloneNoticeEvidence(source []NoticeEvidence) []NoticeEvidence {
	result := append([]NoticeEvidence(nil), source...)
	for index := range result {
		result[index].MatchedAssets = append([]string(nil), source[index].MatchedAssets...)
	}
	return result
}

func cloneSnapshot(source *Snapshot) *Snapshot {
	if source == nil {
		return nil
	}
	result := *source
	result.Exchanges = make(map[string]ExchangeSnapshot, len(source.Exchanges))
	for key, row := range source.Exchanges {
		row.Assets = make(map[string]float64, len(source.Exchanges[key].Assets))
		for asset, volume := range source.Exchanges[key].Assets {
			row.Assets[asset] = volume
		}
		if row.ExchangeTimestampMS != nil {
			value := *row.ExchangeTimestampMS
			row.ExchangeTimestampMS = &value
		}
		result.Exchanges[key] = row
	}
	result.Assets = make(map[AssetKey]float64, len(source.Assets))
	for key, value := range source.Assets {
		result.Assets[key] = value
	}
	return &result
}
