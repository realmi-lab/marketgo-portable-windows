package aggregate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"market-share-go/internal/domain"
)

const MaxLiveEventReceiveSkew = 30_000

// DefaultWindows are the rolling windows, in seconds, that every dashboard
// read, analysis and realtime tick supports. Retention is twice the longest
// window, so windows shorter than 24 hours do not change retained memory.
var DefaultWindows = []int64{10, 30, 60, 300, 600, 1800, 3600, 7200, 14400, 21600, 43200, 86400}

var windowNames = map[int64]string{
	10: "10s", 30: "30s", 60: "1m", 300: "5m", 600: "10m", 1800: "30m",
	3600: "1h", 7200: "2h", 14400: "4h", 21600: "6h", 43200: "12h", 86400: "24h",
}

// WindowName is the public label of a window ("10s", "1m", "24h"). A window
// outside DefaultWindows is labeled by its seconds.
func WindowName(window int64) string {
	if name, ok := windowNames[window]; ok {
		return name
	}
	return strconv.FormatInt(window, 10)
}

// SupportsWindow reports whether window is one of DefaultWindows.
func SupportsWindow(window int64) bool {
	return slices.Contains(DefaultWindows, window)
}

type Bucket struct {
	Second domain.UnixSecond
	Key    VolumeKey
	Totals Totals
}

// AcceptedTrade is the single canonical result of trade ingestion. Its
// identity, event-time bucket, and totals are the exact values already applied
// to rolling and retained-second aggregation. Downstream consumers must use
// this value instead of validating, deduplicating, or recalculating the raw
// trade independently.
//
// Every field is a value (there are no maps, slices, or pointers), so handing a
// copy to an observer cannot alias or mutate Aggregator state.
type AcceptedTrade struct {
	Key         VolumeKey
	EventSecond domain.UnixSecond
	TradeID     string
	Price       float64
	EventTS     domain.UnixMilli
	ReceivedTS  domain.UnixMilli
	Side        domain.TradeSide
	Totals      Totals
}

type IntervalRequest struct {
	Start domain.UnixSecond
	End   domain.UnixSecond
}

type IntervalSnapshotResult struct {
	Values map[VolumeKey]Totals
	OK     bool
}

type Metrics struct {
	Version       uint64
	Duplicates    uint64
	InvalidEvents uint64
	// IdentityConflicts counts events whose exchange/market pair was already
	// bound to different scope/base/quote metadata. Such events are rejected
	// before either rolling or persisted-second state is mutated.
	IdentityConflicts uint64
	UnresolvedFX      uint64
	IngestedTrades    uint64
	LateEvents        uint64
	FutureEvents      uint64
	// ReceiveTimeAdjustedEvents is retained for replay/API compatibility.
	// Strict event-time aggregation no longer rebuckets delayed trades.
	ReceiveTimeAdjustedEvents uint64
	MaxEventReceiveSkewMS     int64
	LastTradeTS               domain.UnixMilli
}

// Aggregator is deliberately not internally synchronized. One owner
// goroutine must call its mutation methods; readers consume copied snapshots.
type Aggregator struct {
	windows   []int64
	storage   []int64
	retention int64
	rolling   rollingEngine
	// Buckets retain only compact stable IDs. The five string headers in a
	// VolumeKey are interned once per market identity instead of being copied
	// into every active second map entry across the retention horizon.
	buckets map[domain.UnixSecond]map[volumeKeyID]Totals
	// coldMinutes owns exact rows older than the hot map window in compact
	// minute blocks. Exact second boundaries remain queryable.
	coldMinutes map[domain.UnixSecond]*compactMinute
	keyTable    []VolumeKey
	// bucketSeconds is a sorted, unique index of populated retained seconds.
	// Normal live traffic appends in O(1); a delayed event uses a binary-search
	// insertion and remains queryable anywhere inside retention. Interval reads
	// therefore never walk wall-clock seconds for which no bucket exists.
	bucketSeconds []domain.UnixSecond
	// minuteBlocks and hourBlocks are derived, lazily materialized range
	// indexes. They make the production 24h/48h interval reads proportional to
	// a few canonical blocks instead of to every retained wall-clock second.
	// The exact second buckets remain authoritative for persistence, late
	// events, boundary fragments, and deterministic cache rebuilds.
	minuteBlocks     map[domain.UnixSecond]*intervalBlock
	hourBlocks       map[domain.UnixSecond]*intervalBlock
	minuteStarts     []domain.UnixSecond
	hourStarts       []domain.UnixSecond
	minuteValueCache intervalValueCache
	hourValueCache   intervalValueCache
	dirty            map[domain.UnixSecond]map[volumeKeyID]struct{}
	subtractScratch  map[VolumeKey]Totals
	// intervalComputed is owner-local scratch used only while one batch of
	// interval requests is being evaluated. Reusing it avoids rebuilding the
	// duplicate-request index for every analysis refresh.
	intervalComputed map[IntervalRequest]int
	// Aggregate with stable numeric IDs, then expand each output key once.
	// This scratch map is never exposed to readers or the rolling engine.
	intervalTotals map[volumeKeyID]Totals
	lastCutoff     map[int64]domain.UnixSecond
	lastNow        domain.UnixSecond
	identities     map[volumeMarketIdentity]volumeKeyID
	dedupe         *Dedupe
	metrics        Metrics
}

// volumeKeyID is stable for the lifetime of an Aggregator. IDs are never
// recycled, so restored and delayed events resolve to the same canonical key
// even after their earliest retained seconds have been pruned.
type volumeKeyID uint64

// volumeMarketIdentity matches the PostgreSQL volume_buckets conflict key after
// its second component: within a given second, exchange+market can persist as
// only one row. Keeping the same identity stable across retained seconds also
// prevents a delayed live event from being split into an in-memory VolumeKey
// that would overwrite different metadata when flushed.
type volumeMarketIdentity struct {
	Exchange string
	Market   string
}

func New(windows []int64) (*Aggregator, error) {
	if len(windows) == 0 {
		windows = DefaultWindows
	}
	unique := make(map[int64]struct{})
	for _, window := range windows {
		if window <= 0 || window > math.MaxInt64/2 {
			return nil, errors.New("rolling windows must be positive and safely doubleable")
		}
		unique[window] = struct{}{}
	}
	normalized := make([]int64, 0, len(unique))
	storageSet := make(map[int64]struct{}, len(unique)*2)
	for window := range unique {
		normalized = append(normalized, window)
		storageSet[window] = struct{}{}
		storageSet[window*2] = struct{}{}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	storage := make([]int64, 0, len(storageSet))
	for window := range storageSet {
		storage = append(storage, window)
	}
	sort.Slice(storage, func(i, j int) bool { return storage[i] < storage[j] })
	rolling, err := newRollingEngine(storage)
	if err != nil {
		return nil, err
	}
	return &Aggregator{
		windows: normalized, storage: storage, retention: storage[len(storage)-1],
		rolling: rolling, buckets: make(map[domain.UnixSecond]map[volumeKeyID]Totals),
		coldMinutes:      make(map[domain.UnixSecond]*compactMinute),
		minuteBlocks:     make(map[domain.UnixSecond]*intervalBlock),
		hourBlocks:       make(map[domain.UnixSecond]*intervalBlock),
		dirty:            make(map[domain.UnixSecond]map[volumeKeyID]struct{}),
		subtractScratch:  make(map[VolumeKey]Totals),
		intervalComputed: make(map[IntervalRequest]int),
		intervalTotals:   make(map[volumeKeyID]Totals),
		lastCutoff:       make(map[int64]domain.UnixSecond),
		identities:       make(map[volumeMarketIdentity]volumeKeyID),
		dedupe:           NewDedupe(maxExactIDsPerMarket),
	}, nil
}

func (a *Aggregator) Ingest(event domain.TradeEvent, krwRate *float64, now domain.UnixSecond) bool {
	_, accepted := a.IngestWithResult(event, krwRate, now)
	return accepted
}

// IngestWithResult atomically applies one valid, non-duplicate trade and returns
// the exact accepted values. A rejected trade returns the zero value and false.
func (a *Aggregator) IngestWithResult(event domain.TradeEvent, krwRate *float64, now domain.UnixSecond) (AcceptedTrade, bool) {
	if now <= 0 {
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	now = a.effectiveNow(now)
	if err := event.Validate(); err != nil {
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	key := VolumeKey{Scope: string(event.Scope), Exchange: string(event.Exchange), Market: event.Market, Base: event.Base, Quote: event.Quote}
	if !a.identityAvailable(key) {
		a.metrics.InvalidEvents++
		a.metrics.IdentityConflicts++
		return AcceptedTrade{}, false
	}
	if a.dedupe.SeenAt(event, now) {
		a.metrics.Duplicates++
		return AcceptedTrade{}, false
	}
	native := event.NativeQuoteNotional()
	if !finitePositive(native) {
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	resolved := krwRate != nil && finitePositive(*krwRate)
	if krwRate != nil && (!finite(*krwRate) || *krwRate < 0) {
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	krw := 0.0
	if resolved {
		krw = native * *krwRate
		if !finite(krw) {
			a.metrics.InvalidEvents++
			return AcceptedTrade{}, false
		}
	}
	eventSecond := domain.UnixSecond(int64(event.EventTS) / 1000)
	receivedSecond := domain.UnixSecond(int64(event.ReceivedTS) / 1000)
	skew := int64(event.ReceivedTS - event.EventTS)
	if absolute(skew) > a.metrics.MaxEventReceiveSkewMS {
		a.metrics.MaxEventReceiveSkewMS = absolute(skew)
	}
	if eventSecond > now+5 {
		a.metrics.FutureEvents++
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	if receivedSecond-eventSecond > domain.UnixSecond(MaxLiveEventReceiveSkew/1000) {
		a.metrics.LateEvents++
	}
	if eventSecond <= now-domain.UnixSecond(a.retention) {
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	matchCount := event.EffectiveMatchCount()
	value := Totals{NativeVolume: native, KRWVolume: krw, TradeCount: matchCount, BaseVolume: event.Quantity}
	if !resolved {
		value.UnresolvedCount = matchCount
	}
	switch event.Side {
	case domain.TradeSideBuy:
		value.BuyKRWVolume = krw
	case domain.TradeSideSell:
		value.SellKRWVolume = krw
	}
	keyID, keyInterned := a.identities[identityOf(key)]
	bucket := Totals{}
	if keyInterned {
		bucket, _ = a.retainedTotal(eventSecond, keyID)
	}
	if !bucket.CheckedAdd(value) {
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	if err := a.rolling.Ingest(int64(eventSecond), int64(now), key, value); err != nil {
		// Both rolling engines reject atomically. Classify an unrepresentable
		// cumulative total as invalid input instead of crashing the owner loop.
		a.metrics.InvalidEvents++
		return AcceptedTrade{}, false
	}
	if !a.dedupe.Claim(event, eventSecond, now) {
		panic("aggregate: dedupe changed between Seen and Claim")
	}
	if !keyInterned {
		keyID = a.rememberIdentity(key)
	}
	a.setRetainedTotal(eventSecond, keyID, bucket, eventSecond <= now-hotSecondRetentionSeconds)
	a.invalidateIntervalBlocks(eventSecond)
	dirty := a.dirty[eventSecond]
	if dirty == nil {
		dirty = make(map[volumeKeyID]struct{})
		a.dirty[eventSecond] = dirty
	}
	dirty[keyID] = struct{}{}
	a.metrics.Version++
	a.metrics.IngestedTrades++
	a.metrics.UnresolvedFX += value.UnresolvedCount
	if event.ReceivedTS > a.metrics.LastTradeTS {
		a.metrics.LastTradeTS = event.ReceivedTS
	}
	return AcceptedTrade{
		Key: key, EventSecond: eventSecond, TradeID: event.TradeID,
		Price: event.Price, EventTS: event.EventTS, ReceivedTS: event.ReceivedTS,
		Side: event.Side, Totals: value,
	}, true
}

func (a *Aggregator) Advance(current domain.UnixSecond) {
	if current <= 0 {
		return
	}
	current = a.effectiveNow(current)
	for _, window := range a.storage {
		cutoff := current - domain.UnixSecond(window)
		previous, initialized := a.lastCutoff[window]
		if !initialized {
			previous = cutoff
			if first, _, exists := a.bucketBounds(); exists && first <= cutoff {
				previous = first - 1
			}
		}
		for _, second := range a.populatedSeconds(previous+1, cutoff) {
			values := a.materializeSecond(second, a.subtractScratch)
			if err := a.rolling.SubtractBatch(window, values); err != nil {
				panic(err)
			}
		}
		a.lastCutoff[window] = cutoff
	}
	a.compactHotBucketsThrough(current - hotSecondRetentionSeconds)
	deleteBefore := current - domain.UnixSecond(a.retention) - 5
	a.deleteBucketsThrough(deleteBefore)
}

func (a *Aggregator) Snapshot(window int64) (map[VolumeKey]Totals, bool) {
	rows, ok, err := a.rolling.Snapshot(window)
	if err != nil {
		panic(err)
	}
	return rows, ok
}

// ComparisonSnapshot returns adjacent windows with identical lengths. The
// current interval is (now-window, now] and the previous interval is
// (now-2*window, now-window]. Building both from second buckets keeps the
// boundary semantics explicit and avoids subtracting floating-point totals.
func (a *Aggregator) ComparisonSnapshot(window int64, now domain.UnixSecond) (
	map[VolumeKey]Totals,
	map[VolumeKey]Totals,
	bool,
) {
	if !a.supportsWindow(window) {
		return nil, nil, false
	}
	currentStart := now - domain.UnixSecond(window)
	previousStart := now - domain.UnixSecond(2*window)
	results := a.IntervalSnapshots([]IntervalRequest{
		{Start: currentStart, End: now},
		{Start: previousStart, End: currentStart},
	})
	return results[0].Values, results[1].Values, true
}

// IntervalSnapshot sums the retained second buckets in (start, end]. Missing
// seconds are intentionally not zero-filled; coverage is reported separately.
func (a *Aggregator) IntervalSnapshot(start, end domain.UnixSecond) (map[VolumeKey]Totals, bool) {
	result := a.IntervalSnapshots([]IntervalRequest{{Start: start, End: end}})[0]
	return result.Values, result.OK
}

// IntervalSnapshots sums multiple retained (start, end] intervals through the
// sparse hierarchical range index. Exact second buckets are used at interval
// edges, minute blocks inside partial hours, and hour blocks for the interior.
// Blocks and rows are always visited chronologically. Results preserve request
// order, including duplicate intervals, and every valid request owns an
// independent values map.
func (a *Aggregator) IntervalSnapshots(requests []IntervalRequest) []IntervalSnapshotResult {
	result, _ := a.IntervalSnapshotsContext(context.Background(), requests)
	return result
}

// Cancellation is checked between indexed interval traversals; scratch maps
// remain owner-private and are cleared on the next read.
func (a *Aggregator) IntervalSnapshotsContext(ctx context.Context, requests []IntervalRequest) ([]IntervalSnapshotResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	results := make([]IntervalSnapshotResult, len(requests))
	seconds := a.indexedBucketSeconds()
	computed := a.intervalComputed
	clear(computed)
	for index, request := range requests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if request.End < request.Start {
			continue
		}
		if previous, exists := computed[request]; exists {
			results[index] = IntervalSnapshotResult{
				Values: cloneTotalsMap(results[previous].Values),
				OK:     true,
			}
			continue
		}
		clear(a.intervalTotals)
		first, last, exists := populatedRangeExclusive(seconds, request.Start, request.End)
		if exists {
			a.accumulateHourRange(first, last, a.intervalTotals)
		}
		values := a.materializeBucket(a.intervalTotals, make(map[VolumeKey]Totals, len(a.intervalTotals)))
		results[index] = IntervalSnapshotResult{Values: values, OK: true}
		computed[request] = index
	}
	return results, ctx.Err()
}

// IntervalBuckets returns retained second buckets in (start, end]. The rows
// are copied so analysis code never observes or mutates the owner's live maps.
func (a *Aggregator) IntervalBuckets(start, end domain.UnixSecond) ([]Bucket, bool) {
	return a.intervalBuckets(start, end, nil)
}

// IntervalBucketsForAsset applies the event-study asset/scope predicate while
// rows are still represented by compact key IDs. This avoids materializing an
// entire market universe for a calculation that immediately discards every
// other asset. Global rows remain available for the existing identity and
// quarantine checks; KRW scope filters only domestic non-KRW markets, exactly
// like analysis.EventEffect.
func (a *Aggregator) IntervalBucketsForAsset(start, end domain.UnixSecond, base, marketScope string) ([]Bucket, bool) {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil, false
	}
	krwOnly := strings.EqualFold(strings.TrimSpace(marketScope), "KRW")
	return a.intervalBuckets(start, end, func(key VolumeKey) bool {
		if !strings.EqualFold(key.Base, base) {
			return false
		}
		return !krwOnly || !strings.EqualFold(key.Scope, string(domain.ScopeDomestic)) || strings.EqualFold(key.Quote, "KRW")
	})
}

func (a *Aggregator) intervalBuckets(start, end domain.UnixSecond, include func(VolumeKey) bool) ([]Bucket, bool) {
	if end < start {
		return nil, false
	}
	result := make([]Bucket, 0)
	if end == start {
		return result, true
	}
	seconds := a.indexedBucketSeconds()
	first, last, exists := populatedRangeExclusive(seconds, start, end)
	if !exists {
		return result, true
	}
	// The identity table is stable during this owner read. Evaluate the asset
	// predicate once per market rather than once per retained second row.
	var included []bool
	if include != nil {
		included = make([]bool, len(a.keyTable))
		anyIncluded := false
		for id, key := range a.keyTable {
			included[id] = include(key)
			anyIncluded = anyIncluded || included[id]
		}
		if !anyIncluded {
			return result, true
		}
	}
	for _, second := range a.populatedSeconds(first, last) {
		a.forEachRetainedRow(second, func(keyID volumeKeyID, totals Totals) {
			if included != nil && !included[keyID] {
				return
			}
			key := a.volumeKey(keyID)
			result = append(result, Bucket{Second: second, Key: key, Totals: totals})
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Second != result[j].Second {
			return result[i].Second < result[j].Second
		}
		if result[i].Key.Exchange != result[j].Key.Exchange {
			return result[i].Key.Exchange < result[j].Key.Exchange
		}
		return result[i].Key.Market < result[j].Key.Market
	})
	return result, true
}

func (a *Aggregator) RetentionSec() int64 { return a.retention }

func (a *Aggregator) LatestBucketSecond() domain.UnixSecond {
	_, latest, exists := a.bucketBounds()
	if !exists {
		return 0
	}
	return latest
}

func (a *Aggregator) supportsWindow(window int64) bool {
	for _, supported := range a.windows {
		if supported == window {
			return true
		}
	}
	return false
}

func (a *Aggregator) DrainClosed(current domain.UnixSecond) ([]Bucket, []WatermarkRow, []RecentIDRow) {
	return a.DrainClosedInto(current, nil, nil, nil)
}

// DrainClosedInto writes persistence rows into caller-owned buffers. The
// returned slices are independent of live aggregation maps but may reuse the
// supplied backing arrays.
func (a *Aggregator) DrainClosedInto(
	current domain.UnixSecond,
	buckets []Bucket,
	watermarks []WatermarkRow,
	recent []RecentIDRow,
) ([]Bucket, []WatermarkRow, []RecentIDRow) {
	buckets = buckets[:0]
	for second, keys := range a.dirty {
		if second >= current {
			continue
		}
		for keyID := range keys {
			if totals, found := a.retainedTotal(second, keyID); found {
				buckets = append(buckets, Bucket{Second: second, Key: a.volumeKey(keyID), Totals: totals})
			}
		}
		delete(a.dirty, second)
	}
	watermarks, recent = a.dedupe.DrainBeforeInto(current, watermarks, recent)
	return buckets, watermarks, recent
}

func (a *Aggregator) Metrics() Metrics { return a.metrics }

func (a *Aggregator) EngineInfo() EngineInfo {
	return EngineInfo{Name: a.rolling.Name(), ABIVersion: a.rolling.ABIVersion(), Native: a.rolling.ABIVersion() > 0}
}

func (a *Aggregator) Close() { a.rolling.Close() }

func (a *Aggregator) RestoreBucket(row Bucket, current domain.UnixSecond) error {
	if current <= 0 {
		return errors.New("restored bucket current second must be positive")
	}
	if row.Second <= 0 || row.Key.Scope == "" || row.Key.Exchange == "" || row.Key.Market == "" || row.Key.Base == "" || row.Key.Quote == "" {
		return errors.New("restored bucket identity and second are required")
	}
	if row.Key.Scope != string(domain.ScopeDomestic) && row.Key.Scope != string(domain.ScopeGlobal) {
		return fmt.Errorf("restored bucket has invalid scope %q", row.Key.Scope)
	}
	if !domain.Exchange(row.Key.Exchange).Valid() {
		return fmt.Errorf("restored bucket has invalid exchange %q", row.Key.Exchange)
	}
	if !domain.ScopeMatchesExchange(domain.Scope(row.Key.Scope), domain.Exchange(row.Key.Exchange)) {
		return fmt.Errorf("restored bucket scope %q does not match exchange %q", row.Key.Scope, row.Key.Exchange)
	}
	if err := row.Totals.Validate(); err != nil {
		return err
	}
	if !a.identityAvailable(row.Key) {
		canonical := a.volumeKey(a.identities[identityOf(row.Key)])
		return fmt.Errorf(
			"restored bucket conflicts with canonical identity for %s:%s: scope/base/quote %s/%s/%s, want %s/%s/%s",
			row.Key.Exchange, row.Key.Market,
			row.Key.Scope, row.Key.Base, row.Key.Quote,
			canonical.Scope, canonical.Base, canonical.Quote,
		)
	}
	keyID, keyInterned := a.identities[identityOf(row.Key)]
	currentBucket := Totals{}
	if keyInterned {
		currentBucket, _ = a.retainedTotal(row.Second, keyID)
	}
	if !currentBucket.CheckedAdd(row.Totals) {
		return errors.New("restored bucket total overflow")
	}
	if err := a.rolling.Ingest(int64(row.Second), int64(current), row.Key, row.Totals); err != nil {
		return fmt.Errorf("restore rolling total: %w", err)
	}
	if !keyInterned {
		keyID = a.rememberIdentity(row.Key)
	}
	a.setRetainedTotal(row.Second, keyID, currentBucket, row.Second <= current-hotSecondRetentionSeconds)
	a.invalidateIntervalBlocks(row.Second)
	return nil
}

func (a *Aggregator) rememberIdentity(key VolumeKey) volumeKeyID {
	identity := identityOf(key)
	if existing, exists := a.identities[identity]; exists {
		return existing
	}
	id := volumeKeyID(len(a.keyTable))
	a.keyTable = append(a.keyTable, key)
	a.identities[identity] = id
	return id
}

func (a *Aggregator) volumeKey(id volumeKeyID) VolumeKey {
	if uint64(id) >= uint64(len(a.keyTable)) {
		panic("aggregate: bucket references an unknown interned volume key")
	}
	return a.keyTable[int(id)]
}

// materializeBucket bridges compact retained rows to the unchanged Go/Rust
// rolling SubtractBatch contract. The rolling engines consume the map during
// the call and do not retain it, so one owner-local scratch map is safe to
// reuse without allocation or aliasing.
func (a *Aggregator) materializeBucket(rows map[volumeKeyID]Totals, target map[VolumeKey]Totals) map[VolumeKey]Totals {
	clear(target)
	for id, totals := range rows {
		target[a.volumeKey(id)] = totals
	}
	return target
}

func (a *Aggregator) effectiveNow(candidate domain.UnixSecond) domain.UnixSecond {
	if candidate > a.lastNow {
		a.lastNow = candidate
	}
	return a.lastNow
}

func (a *Aggregator) recordBucketSecond(second domain.UnixSecond) {
	// Production updates storage and this index together. The O(1) endpoint
	// check keeps ordered checkpoint restoration linear; package-local fixtures
	// that replace storage clear or invalidate the index and are repaired here.
	seconds := a.indexedBucketSeconds()
	if len(seconds) == 0 || second > seconds[len(seconds)-1] {
		a.bucketSeconds = append(seconds, second)
		a.recordIntervalBlockSecond(second)
		return
	}
	position := sort.Search(len(seconds), func(index int) bool { return seconds[index] >= second })
	if position < len(seconds) && seconds[position] == second {
		return
	}
	seconds = append(seconds, 0)
	copy(seconds[position+1:], seconds[position:])
	seconds[position] = second
	a.bucketSeconds = seconds
	a.recordIntervalBlockSecond(second)
}

// indexedBucketSeconds lazily rebuilds the sparse index when package-local
// tests replace the bucket map directly. Production paths update it at the
// same time as the map, including delayed inserts and retention pruning.
func (a *Aggregator) indexedBucketSeconds() []domain.UnixSecond {
	// The owner updates bucketSeconds together with every production insert and
	// retention delete. Trust that O(1) invariant while both indexed endpoints
	// still exist instead of recounting every compact retained second on every
	// interval query. Package fixtures that replace storage directly clear this
	// index and therefore still take the repair path below.
	if len(a.bucketSeconds) > 0 &&
		a.hasRetainedSecond(a.bucketSeconds[0]) &&
		a.hasRetainedSecond(a.bucketSeconds[len(a.bucketSeconds)-1]) {
		return a.bucketSeconds
	}
	if len(a.buckets) == 0 && len(a.coldMinutes) == 0 {
		a.bucketSeconds = nil
		a.minuteValueCache.reset()
		a.hourValueCache.reset()
		a.minuteBlocks = make(map[domain.UnixSecond]*intervalBlock)
		a.hourBlocks = make(map[domain.UnixSecond]*intervalBlock)
		a.minuteStarts = nil
		a.hourStarts = nil
		return nil
	}
	a.rebuildSparseIndexes()
	return a.bucketSeconds
}

func (a *Aggregator) bucketBounds() (domain.UnixSecond, domain.UnixSecond, bool) {
	seconds := a.indexedBucketSeconds()
	if len(seconds) == 0 {
		return 0, 0, false
	}
	return seconds[0], seconds[len(seconds)-1], true
}

func (a *Aggregator) clampBucketRange(first, last domain.UnixSecond) (domain.UnixSecond, domain.UnixSecond, bool) {
	retainedFirst, retainedLast, exists := a.bucketBounds()
	if !exists || last < first || last < retainedFirst || first > retainedLast {
		return 0, 0, false
	}
	if first < retainedFirst {
		first = retainedFirst
	}
	if last > retainedLast {
		last = retainedLast
	}
	return first, last, true
}

func (a *Aggregator) deleteBucketsThrough(cutoff domain.UnixSecond) {
	seconds := a.indexedBucketSeconds()
	remove := sort.Search(len(seconds), func(index int) bool { return seconds[index] > cutoff })
	if remove == 0 {
		return
	}
	for _, second := range seconds[:remove] {
		a.forgetIntervalBlockSecond(second)
		a.deleteRetainedSecond(second)
		delete(a.dirty, second)
	}
	a.compactIntervalBlockStarts()
	if remove == len(seconds) {
		a.bucketSeconds = nil
		return
	}
	a.bucketSeconds = seconds[remove:]
}

// populatedSeconds returns a read-only slice of populated seconds in the
// inclusive range [first,last]. It aliases the owner-only sparse index and is
// valid until the next bucket insertion or retention prune.
func (a *Aggregator) populatedSeconds(first, last domain.UnixSecond) []domain.UnixSecond {
	seconds := a.indexedBucketSeconds()
	start, end := bucketSecondRange(seconds, first, last)
	return seconds[start:end]
}

func bucketSecondRange(seconds []domain.UnixSecond, first, last domain.UnixSecond) (int, int) {
	if len(seconds) == 0 || last < first {
		return 0, 0
	}
	start := sort.Search(len(seconds), func(index int) bool { return seconds[index] >= first })
	end := sort.Search(len(seconds), func(index int) bool { return seconds[index] > last })
	if start > end {
		return end, end
	}
	return start, end
}

func identityOf(key VolumeKey) volumeMarketIdentity {
	return volumeMarketIdentity{Exchange: key.Exchange, Market: key.Market}
}

func (a *Aggregator) identityAvailable(candidate VolumeKey) bool {
	id, exists := a.identities[identityOf(candidate)]
	return !exists || a.volumeKey(id) == candidate
}

func (a *Aggregator) RestoreWatermark(row WatermarkRow) error {
	if !row.Exchange.Valid() || strings.TrimSpace(row.Market) == "" || row.TradeID < 0 {
		return errors.New("restored watermark identity and nonnegative trade ID are required")
	}
	a.dedupe.RestoreWatermark(row.Exchange, row.Market, row.TradeID)
	return nil
}

func (a *Aggregator) RestoreRecentID(row RecentIDRow) error {
	if !row.Exchange.Valid() || strings.TrimSpace(row.Market) == "" || strings.TrimSpace(row.TradeID) == "" || row.EventSecond <= 0 {
		return errors.New("restored recent trade identity and positive event second are required")
	}
	a.dedupe.RestoreRecentID(row.Exchange, row.Market, row.TradeID, row.EventSecond)
	return nil
}

func finite(value float64) bool         { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePositive(value float64) bool { return finite(value) && value > 0 }
func absolute(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
