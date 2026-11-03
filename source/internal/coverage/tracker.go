package coverage

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var gapPersistenceSequence atomic.Uint64

const HistoricalRecoverySourceOfficialRawTrade = "OFFICIAL_RAW_TRADE_REST_V1"

type Segment struct {
	Start int64 `json:"start_second"`
	End   int64 `json:"end_second"`
}

// HistoricalRecovery is exact official raw-trade coverage loaded from a
// completed backfill run. It is kept separate from WebSocket segments so the
// API can report how much of a window was observed live and how much was
// recovered after restart.
type HistoricalRecovery struct {
	GapID  int64  `json:"gap_id"`
	Start  int64  `json:"start_second"`
	End    int64  `json:"end_second"`
	Source string `json:"source"`
}

type GapContext struct {
	AffectedExchanges      []string `json:"affected_exchanges"`
	AffectedMarkets        []string `json:"affected_markets"`
	ObservedVolumeKRW      *float64 `json:"observed_volume_krw"`
	ObservedTotalVolumeKRW *float64 `json:"observed_total_volume_krw"`
	ObservedVolumeAtRisk   *float64 `json:"observed_volume_at_risk_pct"`
	WeightWindowSec        int64    `json:"weight_window_sec"`
	ReasonCodes            []string `json:"reason_codes"`
}

type Gap struct {
	Start     int64      `json:"start_second"`
	End       *int64     `json:"end_second"`
	Status    string     `json:"status"`
	Context   GapContext `json:"context"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	// PersistenceID is the PostgreSQL observation_gap_events primary key. A
	// loaded gap keeps this ID even if retroactive invalidation moves Start.
	PersistenceID int64 `json:"-"`
	// PersistenceToken identifies a gap created in this process until its first
	// successful database commit assigns PersistenceID. It is never serialized.
	PersistenceToken uint64 `json:"-"`
}

func (g Gap) Duration(now int64) int64 {
	end := now
	if g.End != nil {
		end = *g.End
	}
	if end < g.Start {
		return 0
	}
	length, ok := inclusiveLength(g.Start, end)
	if !ok {
		return math.MaxInt64
	}
	return length
}

type Result struct {
	Status             string  `json:"status"`
	CoverageSec        int64   `json:"coverage_sec"`
	RequiredSec        int64   `json:"required_sec"`
	GapSeconds         int64   `json:"gap_seconds"`
	WindowComplete     bool    `json:"window_complete"`
	WebSocketCoverage  int64   `json:"websocket_coverage_sec"`
	HistoricalCoverage int64   `json:"-"`
	HistoricalUsed     bool    `json:"-"`
	HistoricalStart    int64   `json:"-"`
	HistoricalEnd      int64   `json:"-"`
	RecoveredGapIDs    []int64 `json:"-"`
}

type State struct {
	CoverageStart int64
	LastObserved  int64
	LiveStart     int64
	// InvalidThrough is an owner checkpoint boundary, not a new observation.
	// Legacy relational coverage rows omit it; journal checkpoints retain it.
	InvalidThrough       int64
	Segments             []Segment
	HistoricalRecoveries []HistoricalRecovery
	Gaps                 []Gap
}

type Tracker struct {
	coverageStart        int64
	lastObserved         int64
	liveStart            int64
	segments             []Segment
	historicalRecoveries []HistoricalRecovery
	gaps                 []Gap
	activeGap            *int
	invalidThrough       int64
	committedClosedGaps  map[uint64]int64
}

func New(now int64, observationComplete bool) *Tracker {
	tracker := &Tracker{coverageStart: now, liveStart: now}
	if observationComplete {
		tracker.observe(now)
	} else {
		tracker.invalidate(now, GapContext{}, true)
	}
	return tracker
}

func Restore(state State, now int64, observationComplete bool) *Tracker {
	tracker := &Tracker{
		coverageStart:        state.CoverageStart,
		lastObserved:         state.LastObserved,
		liveStart:            now,
		segments:             normalizeSegments(state.Segments),
		historicalRecoveries: normalizeHistoricalRecoveries(state.HistoricalRecoveries),
		gaps:                 append([]Gap(nil), state.Gaps...),
	}
	hadOpenGap := false
	for index := range tracker.gaps {
		ensureGapPersistenceToken(&tracker.gaps[index])
		if tracker.gaps[index].Status == "OPEN" && tracker.gaps[index].End == nil {
			hadOpenGap = true
			end := maxInt64(tracker.gaps[index].Start, now-1)
			closedAt := time.Now()
			tracker.gaps[index].End = &end
			tracker.gaps[index].Status = "CLOSED_ON_RESTART"
			tracker.gaps[index].ClosedAt = &closedAt
		}
	}
	previous := latestSegmentEnd(tracker.segments)
	if !hadOpenGap && previous > 0 && now-previous > 1 {
		start := previous + 1
		end := now - 1
		closedAt := time.Now()
		tracker.gaps = append(tracker.gaps, newGap(Gap{
			Start: start, End: &end, Status: "CLOSED_ON_RESTART",
			Context: normalizeContext(GapContext{
				AffectedExchanges: []string{"bithumb", "coinone", "gopax", "korbit", "upbit"},
				ReasonCodes:       []string{"PROCESS_DOWNTIME"},
			}),
			CreatedAt: closedAt, ClosedAt: &closedAt,
		}))
	}
	if observationComplete {
		tracker.observe(now)
	} else {
		tracker.invalidate(now, GapContext{}, true)
	}
	return tracker
}

// Advance records transport readiness, not trade activity. A healthy delayed
// scheduler tick remains contiguous; an explicit invalidation opens a gap.
func (t *Tracker) Advance(second int64, observationComplete bool, context GapContext) {
	if observationComplete {
		t.observe(second)
		return
	}
	t.invalidate(second, context, true)
}

// InvalidateFrom retroactively removes coverage from the first second that may
// have been unobserved. Stale connections are detected later than the actual
// loss; recording only the detection second would falsely confirm that gap.
func (t *Tracker) InvalidateFrom(start, detected int64, context GapContext) {
	if start <= 0 || start > detected {
		start = detected
	}
	kept := make([]Segment, 0, len(t.segments))
	for _, segment := range t.segments {
		if segment.Start >= start {
			continue
		}
		if segment.End >= start {
			segment.End = start - 1
		}
		if segment.End >= segment.Start {
			kept = append(kept, segment)
		}
	}
	t.segments = kept
	t.coverageStart = start
	t.lastObserved = 0
	t.invalidThrough = maxInt64(t.invalidThrough, detected)
	normalized := normalizeContext(context)
	if t.activeGap == nil {
		index := len(t.gaps)
		t.gaps = append(t.gaps, newGap(Gap{Start: start, Status: "OPEN", Context: normalized, CreatedAt: time.Now()}))
		t.activeGap = &index
		return
	}
	gap := &t.gaps[*t.activeGap]
	if start < gap.Start {
		gap.Start = start
	}
	gap.Context = mergeContext(gap.Context, normalized)
}

func (t *Tracker) observe(second int64) {
	if t.liveStart == 0 {
		// Empty durable replay starts at its first observation, not the
		// calculator's wall clock. Later explicit losses still fence continuity.
		t.liveStart = second
	}
	hadActiveGap := t.activeGap != nil
	if t.activeGap != nil {
		index := *t.activeGap
		// An observation cannot both invalidate and confirm the same second.
		// Wait for the following second before closing a newly opened gap.
		if second <= t.invalidThrough {
			t.coverageStart = t.gaps[index].Start
			t.lastObserved = 0
			return
		}
		end := maxInt64(t.gaps[index].Start, second-1)
		closedAt := time.Now()
		t.gaps[index].End = &end
		t.gaps[index].Status = "RECOVERED"
		t.gaps[index].ClosedAt = &closedAt
		t.activeGap = nil
		t.invalidThrough = 0
	}
	previous := latestSegmentEnd(t.segments)
	sameLiveSession := previous >= t.liveStart && t.liveStart > 0
	continueHealthy := !hadActiveGap && sameLiveSession
	if t.lastObserved > 0 && (second-t.lastObserved <= 1 || continueHealthy) {
		// Keep the previous contiguous start.
	} else {
		t.coverageStart = second
	}
	t.lastObserved = second

	if len(t.segments) > 0 {
		last := &t.segments[len(t.segments)-1]
		if second-last.End <= 1 || continueHealthy {
			if second > last.End {
				last.End = second
			}
			return
		}
	}
	t.segments = append(t.segments, Segment{Start: second, End: second})
}

func (t *Tracker) invalidate(second int64, context GapContext, recordGap bool) {
	t.coverageStart = second
	t.lastObserved = 0
	t.invalidThrough = maxInt64(t.invalidThrough, second)
	if !recordGap {
		return
	}
	normalized := normalizeContext(context)
	if t.activeGap == nil {
		index := len(t.gaps)
		t.gaps = append(t.gaps, newGap(Gap{
			Start: second, Status: "OPEN", Context: normalized, CreatedAt: time.Now(),
		}))
		t.activeGap = &index
		return
	}
	t.gaps[*t.activeGap].Context = mergeContext(t.gaps[*t.activeGap].Context, normalized)
}

func (t *Tracker) Interval(start, end int64) Result {
	return CalculateWithHistorical(t.segments, t.historicalRecoveries, start, end)
}

func (t *Tracker) Window(seconds, now int64) Result {
	if seconds <= 0 || seconds > 1 && now < math.MinInt64+(seconds-1) {
		return Result{Status: "UNAVAILABLE"}
	}
	return t.Interval(now-seconds+1, now)
}

// Scalar owner reads need no copy of retained gap or recovery history.
func (t *Tracker) LastObservedSecond() int64   { return t.lastObserved }
func (t *Tracker) InvalidThroughSecond() int64 { return t.invalidThrough }

func (t *Tracker) State() State {
	return State{
		CoverageStart:        t.coverageStart,
		LastObserved:         t.lastObserved,
		LiveStart:            t.liveStart,
		InvalidThrough:       t.invalidThrough,
		Segments:             append([]Segment(nil), t.segments...),
		HistoricalRecoveries: append([]HistoricalRecovery(nil), t.historicalRecoveries...),
		Gaps:                 copyGaps(t.gaps),
	}
}

// PersistenceState retains absolute segment replacement, but closed gaps that
// this process has successfully committed are immutable and need no new upsert.
// The acknowledgement cache starts empty after restore; failed or uncertain
// commits therefore keep every pending gap in the next retry.
func (t *Tracker) PersistenceState() State {
	state := State{CoverageStart: t.coverageStart, LastObserved: t.lastObserved,
		LiveStart: t.liveStart, InvalidThrough: t.invalidThrough,
		Segments:             append([]Segment(nil), t.segments...),
		HistoricalRecoveries: append([]HistoricalRecovery(nil), t.historicalRecoveries...)}
	var pending []Gap
	for _, gap := range t.gaps {
		if gap.Status == "RECOVERED" && gap.End != nil && gap.PersistenceID > 0 &&
			t.committedClosedGaps[gap.PersistenceToken] == gap.PersistenceID {
			continue
		}
		pending = append(pending, gap)
	}
	state.Gaps = copyGaps(pending)
	return state
}

// ApplyPersistenceIdentities copies database IDs assigned to a State snapshot
// back into the live tracker. PersistenceToken is the only valid join key:
// slice indexes can change when retained history is pruned.
func (t *Tracker) ApplyPersistenceIdentities(state State) bool {
	ids := make(map[uint64]int64, len(state.Gaps))
	ambiguous := make(map[uint64]struct{})
	for _, gap := range state.Gaps {
		if gap.PersistenceToken == 0 || gap.PersistenceID <= 0 {
			continue
		}
		if previous, exists := ids[gap.PersistenceToken]; exists && previous != gap.PersistenceID {
			delete(ids, gap.PersistenceToken)
			ambiguous[gap.PersistenceToken] = struct{}{}
			continue
		}
		if _, conflict := ambiguous[gap.PersistenceToken]; !conflict {
			ids[gap.PersistenceToken] = gap.PersistenceID
		}
	}

	changed := false
	closed := make(map[uint64]Gap)
	for _, gap := range state.Gaps {
		if gap.Status == "RECOVERED" && gap.End != nil && ids[gap.PersistenceToken] == gap.PersistenceID && gap.PersistenceID > 0 {
			closed[gap.PersistenceToken] = gap
		}
	}
	for index := range t.gaps {
		gap := &t.gaps[index]
		if receipt, ok := closed[gap.PersistenceToken]; ok && gap.Status == "RECOVERED" && gap.End != nil &&
			gap.Start == receipt.Start && *gap.End == *receipt.End && (gap.PersistenceID == 0 || gap.PersistenceID == receipt.PersistenceID) {
			if t.committedClosedGaps == nil {
				t.committedClosedGaps = make(map[uint64]int64)
			}
			t.committedClosedGaps[gap.PersistenceToken] = receipt.PersistenceID
		}
		if gap.PersistenceID > 0 || gap.PersistenceToken == 0 {
			continue
		}
		if id := ids[gap.PersistenceToken]; id > 0 {
			gap.PersistenceID = id
			changed = true
		}
	}
	return changed
}

// PruneSegmentsBefore bounds the in-memory coverage history to the same
// inclusive deletion boundary as persisted aggregation buckets. Seconds at or
// before deleteThrough no longer have retained volume data, so a crossing
// segment starts at the first retained second (deleteThrough+1). Closed gaps
// wholly inside the deleted range are removed; open and spanning gaps remain
// because they can still affect retained observations.
func (t *Tracker) PruneSegmentsBefore(deleteThrough int64) bool {
	changed := false
	kept := make([]Segment, 0, len(t.segments))
	for _, segment := range t.segments {
		if segment.End <= deleteThrough {
			changed = true
			continue
		}
		if segment.Start <= deleteThrough {
			segment.Start = deleteThrough + 1
			changed = true
		}
		kept = append(kept, segment)
	}
	if len(kept) != len(t.segments) {
		t.segments = kept
	} else if changed {
		t.segments = kept
	}

	keptRecoveries := make([]HistoricalRecovery, 0, len(t.historicalRecoveries))
	for _, recovery := range t.historicalRecoveries {
		if recovery.End <= deleteThrough {
			changed = true
			continue
		}
		if recovery.Start <= deleteThrough {
			recovery.Start = deleteThrough + 1
			changed = true
		}
		keptRecoveries = append(keptRecoveries, recovery)
	}
	t.historicalRecoveries = keptRecoveries

	keptGaps := make([]Gap, 0, len(t.gaps))
	var activeGap *int
	for index, gap := range t.gaps {
		if gap.End != nil && *gap.End <= deleteThrough {
			delete(t.committedClosedGaps, gap.PersistenceToken)
			changed = true
			continue
		}
		keptIndex := len(keptGaps)
		keptGaps = append(keptGaps, gap)
		if t.activeGap != nil && *t.activeGap == index {
			activeGap = &keptIndex
		}
	}
	if len(keptGaps) != len(t.gaps) {
		t.gaps = keptGaps
		t.activeGap = activeGap
	}
	return changed
}

func newGap(gap Gap) Gap {
	ensureGapPersistenceToken(&gap)
	return gap
}

func ensureGapPersistenceToken(gap *Gap) {
	if gap.PersistenceID > 0 || gap.PersistenceToken != 0 {
		return
	}
	token := gapPersistenceSequence.Add(1)
	if token == 0 {
		token = gapPersistenceSequence.Add(1)
	}
	gap.PersistenceToken = token
}

func Calculate(segments []Segment, start, end int64) Result {
	if end < start {
		return Result{Status: "UNAVAILABLE"}
	}
	required, ok := inclusiveLength(start, end)
	if !ok {
		return Result{Status: "UNAVAILABLE"}
	}
	clipped := make([]Segment, 0, len(segments))
	for _, segment := range segments {
		if segment.End < start || segment.Start > end {
			continue
		}
		clipped = append(clipped, Segment{
			Start: maxInt64(start, segment.Start),
			End:   minInt64(end, segment.End),
		})
	}
	merged := normalizeSegments(clipped)
	covered := int64(0)
	for _, segment := range merged {
		length, ok := inclusiveLength(segment.Start, segment.End)
		if !ok || covered > math.MaxInt64-length {
			return Result{Status: "UNAVAILABLE"}
		}
		covered += length
	}
	status := "UNAVAILABLE"
	if covered >= required {
		status = "CONFIRMED"
	} else if covered > 0 {
		status = "PARTIAL"
	}
	return Result{
		Status: status, CoverageSec: minInt64(required, covered), RequiredSec: required,
		GapSeconds: maxInt64(0, required-covered), WindowComplete: status == "CONFIRMED",
		WebSocketCoverage: minInt64(required, covered),
	}
}

// CalculateWithHistorical combines exact official raw-trade recoveries with
// WebSocket coverage without relabelling recovered seconds as live. Recovery
// IDs are returned only when their interval contributes at least one second
// that was not already covered by WebSocket or an earlier recovery.
func CalculateWithHistorical(segments []Segment, recoveries []HistoricalRecovery, start, end int64) Result {
	websocket := Calculate(segments, start, end)
	if websocket.RequiredSec == 0 {
		return websocket
	}
	covered := normalizeSegments(clippedSegments(segments, start, end))
	rows := normalizeHistoricalRecoveries(recoveries)
	contributed := make([]Segment, 0, len(rows))
	recoveredIDs := make([]int64, 0, len(rows))
	recoveredIDSet := make(map[int64]struct{}, len(rows))
	historicalSeconds := int64(0)
	historicalStart, historicalEnd := int64(0), int64(0)
	for _, recovery := range rows {
		target := Segment{Start: maxInt64(start, recovery.Start), End: minInt64(end, recovery.End)}
		if target.End < target.Start {
			continue
		}
		parts := uncoveredSegments(target, covered)
		if len(parts) == 0 {
			continue
		}
		if _, exists := recoveredIDSet[recovery.GapID]; !exists {
			recoveredIDs = append(recoveredIDs, recovery.GapID)
			recoveredIDSet[recovery.GapID] = struct{}{}
		}
		for _, part := range parts {
			length, ok := inclusiveLength(part.Start, part.End)
			if !ok || historicalSeconds > math.MaxInt64-length {
				return Result{Status: "UNAVAILABLE"}
			}
			historicalSeconds += length
			if historicalStart == 0 || part.Start < historicalStart {
				historicalStart = part.Start
			}
			if part.End > historicalEnd {
				historicalEnd = part.End
			}
			contributed = append(contributed, part)
		}
		covered = normalizeSegments(append(covered, parts...))
	}
	combined := Calculate(append(append([]Segment(nil), segments...), contributed...), start, end)
	combined.WebSocketCoverage = websocket.CoverageSec
	combined.HistoricalCoverage = historicalSeconds
	combined.HistoricalUsed = historicalSeconds > 0
	combined.HistoricalStart = historicalStart
	combined.HistoricalEnd = historicalEnd
	combined.RecoveredGapIDs = recoveredIDs
	return combined
}

func clippedSegments(segments []Segment, start, end int64) []Segment {
	result := make([]Segment, 0, len(segments))
	for _, segment := range segments {
		if segment.End < start || segment.Start > end {
			continue
		}
		result = append(result, Segment{Start: maxInt64(start, segment.Start), End: minInt64(end, segment.End)})
	}
	return result
}

func uncoveredSegments(target Segment, covered []Segment) []Segment {
	result := make([]Segment, 0, 2)
	cursor := target.Start
	exhausted := false
	for _, segment := range normalizeSegments(covered) {
		if segment.End < cursor {
			continue
		}
		if segment.Start > target.End {
			break
		}
		if segment.Start > cursor {
			result = append(result, Segment{Start: cursor, End: minInt64(target.End, segment.Start-1)})
		}
		if segment.End >= target.End || segment.End == math.MaxInt64 {
			exhausted = true
			break
		}
		cursor = maxInt64(cursor, segment.End+1)
	}
	if !exhausted && cursor <= target.End {
		result = append(result, Segment{Start: cursor, End: target.End})
	}
	return result
}

func normalizeHistoricalRecoveries(values []HistoricalRecovery) []HistoricalRecovery {
	result := make([]HistoricalRecovery, 0, len(values))
	seen := make(map[HistoricalRecovery]struct{}, len(values))
	for _, value := range values {
		value.Source = strings.TrimSpace(value.Source)
		if value.GapID <= 0 || value.Start <= 0 || value.End < value.Start || value.Source != HistoricalRecoverySourceOfficialRawTrade {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].GapID != result[j].GapID {
			return result[i].GapID < result[j].GapID
		}
		if result[i].Start != result[j].Start {
			return result[i].Start < result[j].Start
		}
		return result[i].End < result[j].End
	})
	return result
}

func normalizeSegments(segments []Segment) []Segment {
	rows := append([]Segment(nil), segments...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Start == rows[j].Start {
			return rows[i].End < rows[j].End
		}
		return rows[i].Start < rows[j].Start
	})
	result := make([]Segment, 0, len(rows))
	for _, row := range rows {
		if row.End < row.Start {
			continue
		}
		if len(result) == 0 || separated(result[len(result)-1].End, row.Start) {
			result = append(result, row)
			continue
		}
		if row.End > result[len(result)-1].End {
			result[len(result)-1].End = row.End
		}
	}
	return result
}

func inclusiveLength(start, end int64) (int64, bool) {
	if end < start || start <= 0 && end > math.MaxInt64+start-1 {
		return 0, false
	}
	return end - start + 1, true
}

func separated(previousEnd, nextStart int64) bool {
	return previousEnd != math.MaxInt64 && nextStart > previousEnd+1
}

func normalizeContext(context GapContext) GapContext {
	context.AffectedExchanges = uniqueStrings(context.AffectedExchanges)
	context.AffectedMarkets = uniqueStrings(context.AffectedMarkets)
	context.ReasonCodes = canonicalReasonCodes(context.ReasonCodes)
	if context.WeightWindowSec < 1 {
		context.WeightWindowSec = 10
	}
	context.ObservedVolumeKRW = finitePointer(context.ObservedVolumeKRW)
	context.ObservedTotalVolumeKRW = finitePointer(context.ObservedTotalVolumeKRW)
	context.ObservedVolumeAtRisk = finitePointer(context.ObservedVolumeAtRisk)
	return context
}

func mergeContext(existing, update GapContext) GapContext {
	existing = normalizeContext(existing)
	update = normalizeContext(update)
	result := update
	result.AffectedExchanges = mergeAffectedScope(existing.AffectedExchanges, update.AffectedExchanges)
	result.AffectedMarkets = mergeAffectedScope(existing.AffectedMarkets, update.AffectedMarkets)
	result.ReasonCodes = canonicalReasonCodes(append(existing.ReasonCodes, update.ReasonCodes...))
	result.WeightWindowSec = maxInt64(existing.WeightWindowSec, update.WeightWindowSec)
	result.copyWeight(mergedWeight(existing, update, result))
	return result
}

// An empty affected scope means every exchange/market, not an empty set. A
// later global outage must not inherit and narrow an earlier scoped failure.
func mergeAffectedScope(existing, update []string) []string {
	if len(existing) == 0 || len(update) == 0 {
		return nil
	}
	return uniqueStrings(append(existing, update...))
}

const missingTradeEventsReasonPrefix = "MISSING_TRADE_EVENTS:"

// canonicalReasonCodes preserves the magnitude carried by repeated collector
// loss reports. Treating reason codes as a plain set would collapse two
// MISSING_TRADE_EVENTS:3 reports into one and understate the known loss. The
// total remains in the existing string schema and saturates on overflow.
func canonicalReasonCodes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var missingTotal uint64
	missingSeen := false
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, missingTradeEventsReasonPrefix) {
			magnitude := strings.TrimSpace(strings.TrimPrefix(value, missingTradeEventsReasonPrefix))
			parsed, err := strconv.ParseUint(magnitude, 10, 64)
			if err == nil {
				missingTotal = saturatingAddUint64(missingTotal, parsed)
				missingSeen = true
				continue
			}
			if numberError, ok := err.(*strconv.NumError); ok && numberError.Err == strconv.ErrRange {
				missingTotal = math.MaxUint64
				missingSeen = true
				continue
			}
		}
		// Malformed magnitude evidence is retained verbatim instead of silently
		// discarded. It does not participate in the numeric total.
		seen[value] = struct{}{}
	}
	if missingSeen {
		seen[missingTradeEventsReasonPrefix+strconv.FormatUint(missingTotal, 10)] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func saturatingAddUint64(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func finitePointer(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	copy := *value
	return &copy
}

func latestSegmentEnd(segments []Segment) int64 {
	var result int64
	for _, segment := range segments {
		if segment.End > result {
			result = segment.End
		}
	}
	return result
}

func copyGaps(gaps []Gap) []Gap {
	result := make([]Gap, len(gaps))
	for index, gap := range gaps {
		result[index] = gap
		result[index].Context = normalizeContext(gap.Context)
		if gap.End != nil {
			end := *gap.End
			result[index].End = &end
		}
		if gap.ClosedAt != nil {
			closed := *gap.ClosedAt
			result[index].ClosedAt = &closed
		}
	}
	return result
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
