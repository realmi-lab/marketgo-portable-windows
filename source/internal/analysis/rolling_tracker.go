package analysis

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

const (
	movementSampleSec      = int64(10)
	movementHistorySec     = int64(3 * 3600)
	movementPersistenceGap = int64(120)
	movementPersistedLimit = 180
)

type movementSample struct {
	AsOfMS               int64              `json:"as_of_ms"`
	SharePct             *float64           `json:"share_pct"`
	Deltas               map[int64]*float64 `json:"deltas"`
	RecentFlowSharePct   *float64           `json:"recent_flow_share_pct"`
	ExpiringFlowSharePct *float64           `json:"expiring_flow_share_pct"`
	IncomingEffectPP     *float64           `json:"incoming_effect_pp"`
	ExpiringEffectPP     *float64           `json:"expiring_effect_pp"`
	QualityStatus        string             `json:"quality_status"`
}

type RollingTrackerState struct {
	Version  int              `json:"version"`
	Samples  []movementSample `json:"samples"`
	Movement *RollingMovement `json:"movement"`
}

type RollingMovementTracker struct {
	mu       sync.Mutex
	samples  []movementSample
	movement *RollingMovement
}

func NewRollingMovementTracker() *RollingMovementTracker { return &RollingMovementTracker{} }

func (t *RollingMovementTracker) Observe(monitor RollingMonitor) RollingMonitor {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.addAnomalies(monitor.Horizons)
	movementReady := monitor.Rolling24h.Status == "CONFIRMED" && exactHorizonCount(monitor.Horizons) >= 2
	if t.recordSample(monitor, movementReady) {
		candidate := candidateDirection(monitor.Horizons)
		t.updateMovement(candidate, monitor.Rolling24h.CurrentSharePct, monitor.AsOfMS, movementReady)
	}
	return t.decorate(monitor, movementReady)
}

// Snapshot enriches a freshly calculated monitor with the tracker state
// without recording another sample. The background sampler is the only writer;
// API and WebSocket readers therefore cannot change results based on how many
// browsers are connected.
func (t *RollingMovementTracker) Snapshot(monitor RollingMonitor) RollingMonitor {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.addAnomalies(monitor.Horizons)
	movementReady := monitor.Rolling24h.Status == "CONFIRMED" && exactHorizonCount(monitor.Horizons) >= 2
	return t.decorate(monitor, movementReady)
}

func (t *RollingMovementTracker) decorate(monitor RollingMonitor, movementReady bool) RollingMonitor {
	monitor.Movement = stableMovement(monitor.AsOfMS, monitor.Rolling24h.CurrentSharePct)
	if t.movement != nil {
		monitor.Movement = cloneRollingMovement(*t.movement)
	}
	monitor.Trend = t.trend()
	if monitor.DataQuality == nil {
		monitor.DataQuality = make(map[string]any)
	}
	monitor.DataQuality["movement_samples"] = len(t.samples)
	if movementReady {
		monitor.DataQuality["movement_status"] = "READY"
	} else {
		monitor.DataQuality["movement_status"] = "BLOCKED_BY_DATA_GAP"
	}
	if len(t.samples) >= 10 {
		monitor.DataQuality["baseline_status"] = "READY"
	} else {
		monitor.DataQuality["baseline_status"] = "WARMING"
	}
	return monitor
}

func (t *RollingMovementTracker) ExportState() RollingTrackerState {
	t.mu.Lock()
	defer t.mu.Unlock()
	start := max(0, len(t.samples)-movementPersistedLimit)
	state := RollingTrackerState{Version: 1, Samples: append([]movementSample(nil), t.samples[start:]...)}
	for index := range state.Samples {
		state.Samples[index] = cloneMovementSample(state.Samples[index])
	}
	if t.movement != nil {
		copy := cloneRollingMovement(*t.movement)
		state.Movement = &copy
	}
	return state
}

func (t *RollingMovementTracker) RestoreState(state RollingTrackerState, nowMS int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.samples, t.movement = nil, nil
	if state.Version != 1 {
		return false
	}
	cutoff := nowMS - movementHistorySec*1000
	for _, sample := range state.Samples {
		if sample.AsOfMS >= cutoff && sample.AsOfMS > 0 && sample.AsOfMS <= nowMS+movementSampleSec*1000 && finitePointerValue(sample.SharePct) {
			t.samples = append(t.samples, cloneMovementSample(sample))
		}
	}
	sort.Slice(t.samples, func(i, j int) bool { return t.samples[i].AsOfMS < t.samples[j].AsOfMS })
	if len(t.samples) == 0 || nowMS-t.samples[len(t.samples)-1].AsOfMS > movementPersistenceGap*1000 {
		t.samples = nil
		return false
	}
	if len(t.samples) > movementPersistedLimit {
		t.samples = t.samples[len(t.samples)-movementPersistedLimit:]
	}
	// A saved movement is optional even when its sample history is usable.
	// Updating a continuing movement requires its original start timestamp.
	if state.Movement != nil && state.Movement.ShareAtStart != nil &&
		state.Movement.StartedAtMS != nil && *state.Movement.StartedAtMS > 0 {
		copy := cloneRollingMovement(*state.Movement)
		t.movement = &copy
	}
	return true
}

func (t *RollingMovementTracker) addAnomalies(horizons []RollingHorizon) {
	// One private scratch slice serves both medians and every horizon. Sorting
	// it never changes the retained samples or their chronological order.
	var values []float64
	for index := range horizons {
		value := horizons[index].Rolling24hDeltaPP
		if value == nil {
			horizons[index].Anomaly = RollingAnomaly{Status: "UNAVAILABLE"}
			continue
		}
		if values == nil {
			values = make([]float64, 0, len(t.samples))
		}
		values = values[:0]
		for _, sample := range t.samples {
			if delta := sample.Deltas[horizons[index].DurationSec]; delta != nil {
				values = append(values, *delta)
			}
		}
		if len(values) == 0 {
			horizons[index].Anomaly = RollingAnomaly{Status: "BASELINE_WARMING"}
			continue
		}
		baseline := medianFloatInPlace(values)
		for i, item := range values {
			values[i] = math.Abs(item - baseline)
		}
		mad := medianFloatInPlace(values)
		scale := max(1e-6, 1.4826*mad)
		z := (*value - baseline) / scale
		status := "WITHIN_BASELINE"
		if len(values) >= 10 && math.Abs(*value-baseline) >= max(.02, 3*scale) {
			status = "UNUSUAL"
		}
		horizons[index].Anomaly = RollingAnomaly{Status: status, MedianPP: pointer(baseline), MADPP: pointer(mad), RobustZ: pointer(z)}
	}
}

func (t *RollingMovementTracker) recordSample(monitor RollingMonitor, ready bool) bool {
	// Reject the whole observation before allocating or changing movement.
	// Updating an older point's direction without retaining its sample would
	// roll the movement clock back while leaving the trend at a newer time.
	if len(t.samples) > 0 && monitor.AsOfMS < t.samples[len(t.samples)-1].AsOfMS {
		return false
	}
	deltas := make(map[int64]*float64, len(monitor.Horizons))
	var flow *RollingHorizon
	for index := range monitor.Horizons {
		row := &monitor.Horizons[index]
		if ready {
			deltas[row.DurationSec] = copyPointer(row.Rolling24hDeltaPP)
		} else {
			deltas[row.DurationSec] = nil
		}
		if row.DurationSec == 600 {
			flow = row
		}
	}
	sample := movementSample{AsOfMS: monitor.AsOfMS, Deltas: deltas, QualityStatus: monitor.Rolling24h.Status}
	if ready {
		sample.SharePct = copyPointer(monitor.Rolling24h.CurrentSharePct)
		if flow != nil {
			sample.RecentFlowSharePct = copyPointer(flow.RecentFlowSharePct)
			sample.ExpiringFlowSharePct = copyPointer(flow.ExpiringFlowSharePct)
			sample.IncomingEffectPP = copyPointer(flow.IncomingEffectPP)
			sample.ExpiringEffectPP = copyPointer(flow.ExpiringEffectPP)
		}
	}
	if len(t.samples) > 0 {
		last := t.samples[len(t.samples)-1]
		if movementSampleSlot(sample.AsOfMS) == movementSampleSlot(last.AsOfMS) {
			t.samples[len(t.samples)-1] = sample
		} else {
			t.samples = append(t.samples, sample)
		}
	} else {
		t.samples = append(t.samples, sample)
	}
	cutoff := monitor.AsOfMS - movementHistorySec*1000
	first := 0
	for first < len(t.samples) && t.samples[first].AsOfMS < cutoff {
		first++
	}
	t.samples = t.samples[first:]
	return true
}

func candidateDirection(horizons []RollingHorizon) string {
	down, up := 0, 0
	for index := range horizons {
		row := &horizons[index]
		if !isMovementHorizon(row.DurationSec) || row.Status != "CONFIRMED" || row.TotalEffectPP == nil {
			continue
		}
		replacementSupportsDown := row.ReplacementGapPP == nil || *row.ReplacementGapPP < 0
		replacementSupportsUp := row.ReplacementGapPP == nil || *row.ReplacementGapPP > 0
		if *row.TotalEffectPP < -.005 && replacementSupportsDown {
			down++
		}
		if *row.TotalEffectPP > .005 && replacementSupportsUp {
			up++
		}
	}
	if down >= 2 {
		return "DOWN"
	}
	if up >= 2 {
		return "UP"
	}
	return "STABLE"
}

func (t *RollingMovementTracker) updateMovement(candidate string, share *float64, nowMS int64, ready bool) {
	if t.movement != nil && t.movement.ShareAtStart == nil {
		t.movement = nil
	}
	if share == nil || !ready {
		return
	}
	if candidate != "UP" && candidate != "DOWN" {
		t.movement = nil
		return
	}
	if t.movement == nil || t.movement.Direction != candidate {
		t.startMovement(candidate, share, nowMS)
		return
	}
	start := *t.movement.ShareAtStart
	total := *share - start
	same := (t.movement.Direction == "DOWN" && total < 0) || (t.movement.Direction == "UP" && total > 0)
	previous := previousObservedShare(t.samples)
	short := 0.0
	if previous != nil {
		short = *share - *previous
	}
	state := "RECOVERING"
	if candidate == t.movement.Direction && same {
		if math.Abs(total) >= math.Abs(short) {
			state = "EXPANDING"
		} else {
			state = "EASING"
		}
	} else if same {
		if math.Abs(short) < .005 {
			state = "EASING"
		}
	} else if math.Abs(total) <= .02 {
		state = "STABLE"
	}
	t.movement.State, t.movement.CurrentStatus, t.movement.LastUpdatedMS = state, state, nowMS
	t.movement.DurationSec = max(int64(0), nowMS/1000-*t.movement.StartedAtMS/1000)
	t.movement.CurrentShare = copyPointer(share)
	t.movement.TotalChangePP = pointer(total)
	if t.movement.ExtremeShare == nil {
		t.movement.ExtremeShare = copyPointer(share)
	} else if t.movement.Direction == "DOWN" {
		value := min(*t.movement.ExtremeShare, *share)
		t.movement.ExtremeShare = &value
	} else {
		value := max(*t.movement.ExtremeShare, *share)
		t.movement.ExtremeShare = &value
	}
}

func (t *RollingMovementTracker) startMovement(direction string, share *float64, nowMS int64) {
	id := fmt.Sprintf("movement-%s-%d", lowerASCII(direction), nowMS)
	start := nowMS
	t.movement = &RollingMovement{MovementID: &id, Direction: direction, State: "STARTING", CurrentStatus: "STARTING", StartedAtMS: &start, LastUpdatedMS: nowMS, ShareAtStart: copyPointer(share), CurrentShare: copyPointer(share), ExtremeShare: copyPointer(share), TotalChangePP: pointer(0)}
}

func (t *RollingMovementTracker) trend() []RollingTrend {
	start := max(0, len(t.samples)-180)
	result := make([]RollingTrend, 0, len(t.samples)-start)
	for _, sample := range t.samples[start:] {
		result = append(result, RollingTrend{AsOfMS: sample.AsOfMS, Rolling24hSharePct: copyPointer(sample.SharePct), RecentFlowSharePct: copyPointer(sample.RecentFlowSharePct), ExpiringFlowSharePct: copyPointer(sample.ExpiringFlowSharePct), IncomingEffectPP: copyPointer(sample.IncomingEffectPP), ExpiringEffectPP: copyPointer(sample.ExpiringEffectPP), QualityStatus: sample.QualityStatus})
	}
	return result
}

func exactHorizonCount(rows []RollingHorizon) int {
	count := 0
	for _, row := range rows {
		if isMovementHorizon(row.DurationSec) && row.Status == "CONFIRMED" && row.TotalEffectPP != nil {
			count++
		}
	}
	return count
}
func isMovementHorizon(duration int64) bool {
	return duration == 600 || duration == 1200 || duration == 1800
}
func movementSampleSlot(asOfMS int64) int64 {
	return asOfMS / (movementSampleSec * 1000)
}
func stableMovement(now int64, share *float64) RollingMovement {
	return RollingMovement{Direction: "STABLE", State: "STABLE", CurrentStatus: "STABLE", LastUpdatedMS: now, CurrentShare: copyPointer(share), ExtremeShare: copyPointer(share)}
}
func previousObservedShare(samples []movementSample) *float64 {
	seenLatest := false
	for index := len(samples) - 1; index >= 0; index-- {
		if share := samples[index].SharePct; share != nil {
			if seenLatest {
				return copyPointer(share)
			}
			seenLatest = true
		}
	}
	return nil
}
func medianFloat(values []float64) float64 {
	return medianFloatInPlace(append([]float64(nil), values...))
}
func medianFloatInPlace(rows []float64) float64 {
	sort.Float64s(rows)
	middle := len(rows) / 2
	if len(rows)%2 == 1 {
		return rows[middle]
	}
	return rows[middle-1] + (rows[middle]-rows[middle-1])/2
}

// State crossing the tracker mutex belongs to its recipient, including the
// optional values and sample delta maps nested inside the exported structs.
func cloneMovementSample(sample movementSample) movementSample {
	sample.SharePct = copyPointer(sample.SharePct)
	sample.RecentFlowSharePct = copyPointer(sample.RecentFlowSharePct)
	sample.ExpiringFlowSharePct = copyPointer(sample.ExpiringFlowSharePct)
	sample.IncomingEffectPP = copyPointer(sample.IncomingEffectPP)
	sample.ExpiringEffectPP = copyPointer(sample.ExpiringEffectPP)
	if sample.Deltas != nil {
		deltas := make(map[int64]*float64, len(sample.Deltas))
		for duration, value := range sample.Deltas {
			deltas[duration] = copyPointer(value)
		}
		sample.Deltas = deltas
	}
	return sample
}

func cloneRollingMovement(movement RollingMovement) RollingMovement {
	if movement.MovementID != nil {
		id := *movement.MovementID
		movement.MovementID = &id
	}
	if movement.StartedAtMS != nil {
		started := *movement.StartedAtMS
		movement.StartedAtMS = &started
	}
	movement.ShareAtStart = copyPointer(movement.ShareAtStart)
	movement.CurrentShare = copyPointer(movement.CurrentShare)
	movement.ExtremeShare = copyPointer(movement.ExtremeShare)
	movement.TotalChangePP = copyPointer(movement.TotalChangePP)
	return movement
}

func copyPointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func finitePointerValue(value *float64) bool {
	return value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0))
}
func lowerASCII(value string) string {
	if value == "UP" {
		return "up"
	}
	if value == "DOWN" {
		return "down"
	}
	return value
}
