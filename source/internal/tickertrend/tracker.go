package tickertrend

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/movement"
)

const observationIntervalToleranceMS int64 = 15 * 1000
const maxContextWindowSamples = 90*24 + 1
const pathDisplayResolutionPP = 0.005
const marketRowsRelativeTolerance = 1e-9
const officialShareAbsoluteTolerancePP = 1e-8

type Store interface {
	SaveOfficialTickerMinuteSample(context.Context, Sample) error
	RecentOfficialTickerMinuteSamples(context.Context, int64, int) ([]Sample, error)
	OfficialTickerMinuteSamplesBetween(context.Context, int64, int64, int) ([]Sample, error)
	OfficialTickerMinuteMarkets(context.Context, []int64) (map[int64]map[movement.AssetKey]float64, error)
}

type Tracker struct {
	persistMu   sync.Mutex
	mu          sync.RWMutex
	store       Store
	intervalMS  int64
	trendLimit  int
	driverLimit int
	samples     []Sample
}

func NewTracker(store Store) *Tracker {
	return &Tracker{
		store: store, intervalMS: DefaultInterval * 1000,
		trendLimit: defaultTrendSize, driverLimit: defaultDriverSize,
	}
}

func (t *Tracker) Restore(ctx context.Context) error {
	if t == nil || t.store == nil {
		return nil
	}
	// Serialize the read/install with Observe so a concurrent new hour cannot
	// be overwritten by an older database snapshot. Failed reads retain history.
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	since := time.Now().Add(-49 * time.Hour).UnixMilli()
	samples, err := t.store.RecentOfficialTickerMinuteSamples(ctx, since, t.trendLimit)
	if err != nil {
		return fmt.Errorf("restore official aligned 1h candle samples: %w", err)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].BucketMS < samples[j].BucketMS })
	if len(samples) > t.trendLimit {
		samples = samples[len(samples)-t.trendLimit:]
	}
	t.mu.Lock()
	t.samples = cloneSamples(samples)
	t.mu.Unlock()
	return nil
}

// Observe stores one aligned rolling-24h candle snapshot per closed UTC hour.
func (t *Tracker) Observe(ctx context.Context, snapshot *movement.Snapshot) error {
	return t.ObserveWithBithumbCloses(ctx, snapshot, nil)
}

// ObserveWithBithumbCloses stores the rolling-24h snapshot together with exact
// closes from Bithumb's final aligned one-hour candle. Missing close values are
// retained as missing; this method never copies a nearby hour's price.
func (t *Tracker) ObserveWithBithumbCloses(
	ctx context.Context,
	snapshot *movement.Snapshot,
	closes map[string]float64,
) error {
	if t == nil || snapshot == nil || snapshot.AsOfMS <= 0 || snapshot.TotalVolumeKRW <= 0 {
		return fmt.Errorf("invalid official aligned 1h candle snapshot")
	}
	normalizedCloses, err := normalizedBithumbCloses(snapshot, closes)
	if err != nil {
		return err
	}
	bucketMS := snapshot.AsOfMS / t.intervalMS * t.intervalMS
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.mu.RLock()
	if len(t.samples) > 0 && bucketMS <= t.samples[len(t.samples)-1].BucketMS {
		t.mu.RUnlock()
		return nil
	}
	t.mu.RUnlock()
	sample := Sample{
		BucketMS: bucketMS, Snapshot: *cloneSnapshot(snapshot),
		AssetRowCount: len(snapshot.Assets), BithumbClosePricesKRW: normalizedCloses,
	}
	if err := t.store.SaveOfficialTickerMinuteSample(ctx, sample); err != nil {
		return fmt.Errorf("save official aligned 1h candle sample: %w", err)
	}

	// Keep the last committed snapshot readable while storage is in flight.
	// persistMu still serializes Observe and Restore; hydrators can only enrich
	// existing entries under mu, so append against the latest in-memory slice.
	t.mu.Lock()
	t.samples = append(t.samples, sample)
	if len(t.samples) > t.trendLimit {
		t.samples = append([]Sample(nil), t.samples[len(t.samples)-t.trendLimit:]...)
	}
	t.mu.Unlock()
	return nil
}

func normalizedBithumbCloses(snapshot *movement.Snapshot, closes map[string]float64) (map[string]float64, error) {
	result := make(map[string]float64, len(closes))
	for rawAsset, closePrice := range closes {
		asset := strings.ToUpper(strings.TrimSpace(rawAsset))
		if asset == "" || closePrice <= 0 || math.IsNaN(closePrice) || math.IsInf(closePrice, 0) {
			return nil, fmt.Errorf("invalid Bithumb aligned 1h close price for %q", rawAsset)
		}
		if snapshot != nil {
			if _, found := snapshot.Assets[movement.AssetKey{Exchange: "bithumb", Asset: asset}]; !found {
				return nil, fmt.Errorf("Bithumb aligned 1h close has no matching asset row for %s", asset)
			}
		}
		if previous, duplicate := result[asset]; duplicate && previous != closePrice {
			return nil, fmt.Errorf("conflicting Bithumb aligned 1h close prices for %s", asset)
		}
		result[asset] = closePrice
	}
	return result, nil
}

func (t *Tracker) State() State {
	nowMS := time.Now().UnixMilli()
	state := State{
		Version: Version, GeneratedAtMS: nowMS, IntervalSec: DefaultInterval,
		Status: "WARMING", Reason: "TWO_CONSECUTIVE_ALIGNED_HOUR_SAMPLES_REQUIRED",
		Drivers: []movement.Driver{}, ExchangeDrivers: []movement.ExchangeDriver{}, Trend: []Point{},
		ExpirationEstimate: unavailableEstimate("WS_INTERVAL_NOT_EVALUATED"),
		DataQuality: map[string]any{
			"source":               "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H",
			"sampling":             "ONE_SNAPSHOT_PER_CLOSED_UTC_HOUR",
			"asset_history_source": "official_aligned_1h_markets",
			"retention_policy":     "NO_TIME_BASED_DELETION",
		},
	}
	if t == nil {
		state.Status, state.Reason = "UNAVAILABLE", "TRACKER_NOT_CONFIGURED"
		return state
	}
	t.mu.RLock()
	samples := cloneSamples(t.samples)
	driverLimit := t.driverLimit
	intervalMS := t.intervalMS
	t.mu.RUnlock()
	state.DataQuality["sample_count"] = len(samples)
	for _, sample := range samples {
		state.Trend = append(state.Trend, samplePoint(sample))
	}
	if len(samples) == 0 {
		return state
	}
	current := samples[len(samples)-1]
	currentPoint := samplePoint(current)
	state.Current = &currentPoint
	if len(samples) < 2 {
		return state
	}
	previous := samples[len(samples)-2]
	previousPoint := samplePoint(previous)
	state.Previous = &previousPoint
	if current.BucketMS-previous.BucketMS != intervalMS {
		state.Status, state.Reason = "GAP", "CONSECUTIVE_ALIGNED_HOUR_BUCKET_MISSING"
		return state
	}
	observedIntervalMS := current.Snapshot.AsOfMS - previous.Snapshot.AsOfMS
	state.DataQuality["observation_interval_sec"] = float64(observedIntervalMS) / 1000
	state.DataQuality["expected_observation_interval_sec"] = DefaultInterval
	state.DataQuality["observation_interval_tolerance_sec"] = observationIntervalToleranceMS / 1000
	if observedIntervalMS < intervalMS-observationIntervalToleranceMS ||
		observedIntervalMS > intervalMS+observationIntervalToleranceMS {
		state.Status, state.Reason = "GAP", "OFFICIAL_OBSERVATION_INTERVAL_OUT_OF_RANGE"
		return state
	}
	state.Status, state.Reason = "LIVE", "CONSECUTIVE_ALIGNED_HOUR_CANDLE_SNAPSHOTS"
	state.ShareDeltaPP = number(current.Snapshot.BithumbSharePct - previous.Snapshot.BithumbSharePct)
	state.DomesticVolumeDeltaKRW = number(current.Snapshot.TotalVolumeKRW - previous.Snapshot.TotalVolumeKRW)
	state.BithumbVolumeDeltaKRW = number(current.Snapshot.BithumbVolumeKRW - previous.Snapshot.BithumbVolumeKRW)
	assetReady := previous.AssetRowCount > 0 && current.AssetRowCount > 0 &&
		len(previous.Snapshot.Assets) > 0 && len(current.Snapshot.Assets) > 0
	state.DataQuality["asset_details_ready"] = assetReady
	state.DataQuality["previous_asset_row_count"] = previous.AssetRowCount
	state.DataQuality["current_asset_row_count"] = current.AssetRowCount
	state.ExchangeDrivers = limitExchangeDrivers(
		movement.AttributeExchangeSnapshots(&previous.Snapshot, &current.Snapshot),
		driverLimit,
	)
	if !assetReady {
		state.DataQuality["asset_details_reason"] = "TWO_ASSET_COMPLETE_ALIGNED_HOUR_SAMPLES_REQUIRED"
		return state
	}
	drivers, exchanges := movement.AttributeSnapshots(&previous.Snapshot, &current.Snapshot)
	state.Drivers = limitDrivers(drivers, driverLimit)
	state.ExchangeDrivers = limitExchangeDrivers(exchanges, driverLimit)
	return state
}

// CompareWindow compares two aligned closed-hour 24h candle snapshots separated
// by an exact server-defined window. It refuses to bridge missing hour buckets.
func (t *Tracker) CompareWindow(windowSec int64) WindowComparison {
	result := WindowComparison{
		WindowSec: windowSec, Status: "UNAVAILABLE", Reason: "WINDOW_NOT_AVAILABLE",
		Drivers: []movement.Driver{}, ExchangeDrivers: []movement.ExchangeDriver{},
		PathSummary:             WindowPathSummary{Status: "UNAVAILABLE", Reason: "WINDOW_NOT_AVAILABLE"},
		PriceRelationshipStatus: "UNAVAILABLE", PriceRelationshipReason: "INSUFFICIENT_CONTIGUOUS_PRICE_PAIRS",
		PriceRelationships: []WindowPriceRelationship{},
	}
	if t == nil || windowSec <= 0 {
		return result
	}
	t.mu.RLock()
	samples := cloneSamples(t.samples)
	intervalMS, driverLimit := t.intervalMS, t.driverLimit
	t.mu.RUnlock()
	windowMS := windowSec * 1_000
	if len(samples) == 0 || intervalMS <= 0 || windowMS%intervalMS != 0 {
		result.Reason = "EXACT_HOUR_WINDOW_REQUIRED"
		return result
	}
	expected := int(windowMS/intervalMS) + 1
	result.ExpectedSampleCount = expected
	result.PathSummary.ExpectedPointCount = expected
	currentIndex := len(samples) - 1
	current := samples[currentIndex]
	targetBucket := current.BucketMS - windowMS
	previousIndex := -1
	for index := currentIndex - 1; index >= 0; index-- {
		if samples[index].BucketMS == targetBucket {
			previousIndex = index
			break
		}
		if samples[index].BucketMS < targetBucket {
			break
		}
	}
	if previousIndex < 0 {
		result.Status, result.Reason = "WARMING", "WINDOW_START_SAMPLE_MISSING"
		result.ObservedSampleCount = min(len(samples), expected)
		result.PathSummary = WindowPathSummary{
			Status: "INCOMPLETE", Reason: "WINDOW_START_SAMPLE_MISSING",
			PointCount: result.ObservedSampleCount, ExpectedPointCount: expected,
		}
		return result
	}
	windowSamples := samples[previousIndex : currentIndex+1]
	result.ObservedSampleCount = len(windowSamples)
	result.MissingSampleCount = max(0, expected-len(windowSamples))
	pathComplete := len(windowSamples) == expected
	for index := 1; index < len(windowSamples); index++ {
		if windowSamples[index].BucketMS-windowSamples[index-1].BucketMS != intervalMS {
			pathComplete = false
		}
	}
	for _, sample := range windowSamples {
		if sample.Snapshot.Status != "LIVE" || !validPathShare(sample.Snapshot.BithumbSharePct) {
			pathComplete = false
		}
	}
	result.PathSummary = summarizeWindowPath(windowSamples, expected, intervalMS, pathComplete)
	result.PriceRelationships, result.PriceRelationshipStatus, result.PriceRelationshipReason =
		summarizeWindowPriceRelationships(windowSamples, intervalMS, 8, 10)
	previous := windowSamples[0]
	if previous.Snapshot.Status != "LIVE" || current.Snapshot.Status != "LIVE" {
		result.Status, result.Reason = "GAP", "WINDOW_ENDPOINT_NOT_LIVE"
		return result
	}
	previousPoint, currentPoint := samplePoint(previous), samplePoint(current)
	result.Previous, result.Current = &previousPoint, &currentPoint
	result.Status, result.Reason = "LIVE", "EXACT_ALIGNED_HOUR_ENDPOINTS"
	result.EndpointComparison, result.PathComplete = true, pathComplete
	if !pathComplete {
		result.Reason = "EXACT_ENDPOINTS_WITH_PATH_GAPS"
	}
	result.ShareDeltaPP = number(current.Snapshot.BithumbSharePct - previous.Snapshot.BithumbSharePct)
	result.DomesticVolumeDeltaKRW = number(current.Snapshot.TotalVolumeKRW - previous.Snapshot.TotalVolumeKRW)
	result.BithumbVolumeDeltaKRW = number(current.Snapshot.BithumbVolumeKRW - previous.Snapshot.BithumbVolumeKRW)
	result.ExchangeDrivers = limitExchangeDrivers(
		movement.AttributeExchangeSnapshots(&previous.Snapshot, &current.Snapshot), driverLimit,
	)
	if previous.AssetRowCount > 0 && current.AssetRowCount > 0 && len(previous.Snapshot.Assets) > 0 && len(current.Snapshot.Assets) > 0 {
		drivers, exchanges := movement.AttributeSnapshots(&previous.Snapshot, &current.Snapshot)
		result.Drivers = limitDrivers(drivers, driverLimit)
		result.ExchangeDrivers = limitExchangeDrivers(exchanges, driverLimit)
	}
	return result
}

// CompareAlignedHour compares the selected persisted aligned-hour endpoint
// with the immediately preceding persisted hour. Unlike CompareWindow, the
// selected endpoint is supplied by the caller instead of being inferred from
// the newest in-memory sample. Asset attribution is hydrated from the
// persisted per-market rows and is intentionally not truncated so consumers
// can independently select the largest rising and falling contributors.
func (t *Tracker) CompareAlignedHour(ctx context.Context, toMS int64) (WindowComparison, error) {
	const expected = 2
	intervalMS := DefaultInterval * 1_000
	result := WindowComparison{
		WindowSec: DefaultInterval, Status: "UNAVAILABLE", Reason: "ALIGNED_HOUR_ENDPOINTS_NOT_AVAILABLE",
		Drivers: []movement.Driver{}, ExchangeDrivers: []movement.ExchangeDriver{},
		ExpectedSampleCount: expected, MissingSampleCount: expected,
		PathSummary: WindowPathSummary{
			Status: "INCOMPLETE", Reason: "ALIGNED_HOUR_ENDPOINTS_NOT_AVAILABLE",
			ExpectedPointCount: expected,
		},
	}
	if toMS <= 0 || toMS%intervalMS != 0 {
		return result, fmt.Errorf("official aligned contribution requires a positive exact-hour endpoint")
	}
	if t == nil || t.store == nil {
		return result, fmt.Errorf("official aligned contribution store is not configured")
	}

	fromMS := toMS - intervalMS
	samples, err := t.store.OfficialTickerMinuteSamplesBetween(ctx, fromMS, toMS, expected)
	if err != nil {
		return result, fmt.Errorf("load official aligned 1h contribution endpoints: %w", err)
	}
	var previous, current *Sample
	for index := range samples {
		switch samples[index].BucketMS {
		case fromMS:
			copy := samples[index]
			previous = &copy
		case toMS:
			copy := samples[index]
			current = &copy
		}
	}
	result.ObservedSampleCount = boolCount(previous != nil) + boolCount(current != nil)
	result.MissingSampleCount = expected - result.ObservedSampleCount
	result.PathSummary.PointCount = result.ObservedSampleCount
	if previous == nil || current == nil {
		result.Status = "WARMING"
		switch {
		case previous == nil && current == nil:
			result.Reason = "ALIGNED_HOUR_ENDPOINTS_MISSING"
		case previous == nil:
			result.Reason = "PREVIOUS_ALIGNED_HOUR_SAMPLE_MISSING"
		default:
			result.Reason = "SELECTED_ALIGNED_HOUR_SAMPLE_MISSING"
		}
		result.PathSummary.Reason = result.Reason
		return result, nil
	}

	// Range rows deliberately do not provide the attribution input. Clear any
	// adapter-local copies (SQLite keeps its compact sample as JSON), then use
	// only the dedicated persisted market-row contract for both endpoints.
	exact := []Sample{*previous, *current}
	for index := range exact {
		exact[index].Snapshot.Assets = make(map[movement.AssetKey]float64)
		for exchangeName, exchange := range exact[index].Snapshot.Exchanges {
			exchange.Assets = nil
			exact[index].Snapshot.Exchanges[exchangeName] = exchange
		}
	}
	if exact[0].AssetRowCount > 0 && exact[1].AssetRowCount > 0 {
		markets, loadErr := t.store.OfficialTickerMinuteMarkets(ctx, []int64{fromMS, toMS})
		if loadErr != nil {
			return result, fmt.Errorf("load official aligned 1h contribution markets: %w", loadErr)
		}
		if endpointMarketRowsComplete(exact[0], markets[fromMS]) &&
			endpointMarketRowsComplete(exact[1], markets[toMS]) {
			setSampleMarketRows(&exact[0], markets[fromMS])
			setSampleMarketRows(&exact[1], markets[toMS])
		}
	}

	transient := &Tracker{
		store: t.store, intervalMS: intervalMS, trendLimit: expected,
		// Zero means no truncation in limitDrivers/limitExchangeDrivers.
		driverLimit: 0, samples: exact,
	}
	return transient.CompareWindow(DefaultInterval), nil
}

func endpointMarketRowsComplete(sample Sample, rows map[movement.AssetKey]float64) bool {
	return sample.AssetRowCount > 0 && len(rows) == sample.AssetRowCount &&
		marketRowsReconcileSnapshot(&sample.Snapshot, rows)
}

func marketRowsReconcileSnapshot(snapshot *movement.Snapshot, rows map[movement.AssetKey]float64) bool {
	if !officialSnapshotTotalsReconcile(snapshot) || len(rows) == 0 {
		return false
	}
	total, bithumb := 0.0, 0.0
	for key, volume := range rows {
		if key.Exchange == "" || key.Exchange != strings.ToLower(strings.TrimSpace(key.Exchange)) ||
			key.Asset == "" || key.Asset != strings.ToUpper(strings.TrimSpace(key.Asset)) ||
			volume < 0 || math.IsNaN(volume) || math.IsInf(volume, 0) {
			return false
		}
		total += volume
		if key.Exchange == "bithumb" {
			bithumb += volume
		}
		if math.IsInf(total, 0) || math.IsInf(bithumb, 0) {
			return false
		}
	}
	return marketRowVolumesEqual(total, snapshot.TotalVolumeKRW) &&
		marketRowVolumesEqual(bithumb, snapshot.BithumbVolumeKRW)
}

func officialSnapshotTotalsReconcile(snapshot *movement.Snapshot) bool {
	if snapshot == nil || snapshot.TotalVolumeKRW <= 0 || math.IsNaN(snapshot.TotalVolumeKRW) ||
		math.IsInf(snapshot.TotalVolumeKRW, 0) || snapshot.BithumbVolumeKRW < 0 ||
		snapshot.BithumbVolumeKRW > snapshot.TotalVolumeKRW || math.IsNaN(snapshot.BithumbVolumeKRW) ||
		math.IsInf(snapshot.BithumbVolumeKRW, 0) || !validPathShare(snapshot.BithumbSharePct) {
		return false
	}
	wantShare := snapshot.BithumbVolumeKRW / snapshot.TotalVolumeKRW * 100
	return math.Abs(snapshot.BithumbSharePct-wantShare) <= officialShareAbsoluteTolerancePP
}

func marketRowVolumesEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= scale*marketRowsRelativeTolerance
}

func setSampleMarketRows(sample *Sample, rows map[movement.AssetKey]float64) {
	if sample == nil {
		return
	}
	sample.Snapshot.Assets = cloneAssetVolumes(rows)
	for key, volume := range rows {
		exchange := sample.Snapshot.Exchanges[key.Exchange]
		if exchange.Assets == nil {
			exchange.Assets = make(map[string]float64)
		}
		exchange.Assets[key.Asset] = volume
		sample.Snapshot.Exchanges[key.Exchange] = exchange
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func summarizeWindowPath(samples []Sample, expected int, intervalMS int64, complete bool) WindowPathSummary {
	summary := WindowPathSummary{
		Status: "INCOMPLETE", Reason: "PATH_GAPS_OR_NON_LIVE_OR_INVALID_SAMPLES",
		PointCount: len(samples), ExpectedPointCount: expected,
		ObservedSegments: observedWindowPathSegments(samples, intervalMS),
	}
	if !complete || len(samples) == 0 || len(samples) != expected {
		return summary
	}
	for _, sample := range samples {
		if sample.Snapshot.Status != "LIVE" || !validPathShare(sample.Snapshot.BithumbSharePct) {
			summary.Status, summary.Reason = "UNAVAILABLE", "INVALID_PATH_POINT"
			return summary
		}
	}

	summary.Status, summary.Reason = "COMPLETE", "EXACT_ALIGNED_LIVE_HOUR_PATH"
	summary.StartMS, summary.EndMS = samples[0].BucketMS, samples[len(samples)-1].BucketMS
	minShare, maxShare := samples[0].Snapshot.BithumbSharePct, samples[0].Snapshot.BithumbSharePct
	summary.MinShareAtMS, summary.MaxShareAtMS = samples[0].BucketMS, samples[0].BucketMS
	largestStep, largestAbs, totalAbs := 0.0, -1.0, 0.0
	for index := 1; index < len(samples); index++ {
		share := samples[index].Snapshot.BithumbSharePct
		if share < minShare {
			minShare, summary.MinShareAtMS = share, samples[index].BucketMS
		}
		if share > maxShare {
			maxShare, summary.MaxShareAtMS = share, samples[index].BucketMS
		}
		step := share - samples[index-1].Snapshot.BithumbSharePct
		stepAbs := math.Abs(step)
		if stepAbs >= pathDisplayResolutionPP {
			totalAbs += stepAbs
		}
		if stepAbs > largestAbs {
			largestStep, largestAbs = step, stepAbs
			summary.LargestStepStartMS = samples[index-1].BucketMS
			summary.LargestStepEndMS = samples[index].BucketMS
		}
		switch {
		case step >= pathDisplayResolutionPP:
			summary.RisingStepCount++
		case step <= -pathDisplayResolutionPP:
			summary.FallingStepCount++
		default:
			summary.FlatStepCount++
		}
	}
	summary.MinSharePct, summary.MaxSharePct = number(minShare), number(maxShare)
	if len(samples) > 1 {
		summary.LargestStepDeltaPP = number(largestStep)
		if totalAbs > 0 {
			summary.LargestStepAbsSharePct = number(largestAbs / totalAbs * 100)
		}
		stepCount := len(samples) - 1
		dominantCount := max(summary.RisingStepCount, max(summary.FallingStepCount, summary.FlatStepCount))
		summary.DominantDirectionSharePct = number(float64(dominantCount) / float64(stepCount) * 100)
	}
	switch {
	case summary.RisingStepCount > 0 && summary.FallingStepCount == 0:
		summary.Direction = "RISING"
	case summary.FallingStepCount > 0 && summary.RisingStepCount == 0:
		summary.Direction = "FALLING"
	case summary.RisingStepCount == 0 && summary.FallingStepCount == 0:
		summary.Direction = "FLAT"
	default:
		summary.Direction = "MIXED"
	}
	return summary
}

func observedWindowPathSegments(samples []Sample, intervalMS int64) []WindowPathSegment {
	if len(samples) == 0 || intervalMS <= 0 {
		return []WindowPathSegment{}
	}
	segments := make([]WindowPathSegment, 0, 4)
	start := -1
	flush := func(end int) {
		if start < 0 || end-start+1 < 2 {
			start = -1
			return
		}
		values := samples[start : end+1]
		segment := WindowPathSegment{
			StartMS: values[0].BucketMS, EndMS: values[len(values)-1].BucketMS,
			PointCount: len(values), StartSharePct: values[0].Snapshot.BithumbSharePct,
			EndSharePct: values[len(values)-1].Snapshot.BithumbSharePct,
			MinSharePct: values[0].Snapshot.BithumbSharePct, MinShareAtMS: values[0].BucketMS,
			MaxSharePct: values[0].Snapshot.BithumbSharePct, MaxShareAtMS: values[0].BucketMS,
		}
		segment.ShareDeltaPP = segment.EndSharePct - segment.StartSharePct
		largestAbs := -1.0
		for index := 1; index < len(values); index++ {
			share := values[index].Snapshot.BithumbSharePct
			if share < segment.MinSharePct {
				segment.MinSharePct, segment.MinShareAtMS = share, values[index].BucketMS
			}
			if share > segment.MaxSharePct {
				segment.MaxSharePct, segment.MaxShareAtMS = share, values[index].BucketMS
			}
			step := share - values[index-1].Snapshot.BithumbSharePct
			if math.Abs(step) > largestAbs {
				largestAbs = math.Abs(step)
				segment.LargestStepDeltaPP = step
				segment.LargestStepStartMS = values[index-1].BucketMS
				segment.LargestStepEndMS = values[index].BucketMS
			}
		}
		segments = append(segments, segment)
		start = -1
	}
	for index, sample := range samples {
		valid := sample.Snapshot.Status == "LIVE" && validPathShare(sample.Snapshot.BithumbSharePct)
		if !valid {
			flush(index - 1)
			continue
		}
		if start < 0 {
			start = index
			continue
		}
		if sample.BucketMS-samples[index-1].BucketMS != intervalMS {
			flush(index - 1)
			start = index
		}
	}
	flush(len(samples) - 1)
	return segments
}

type windowPricePair struct {
	priceReturnPct float64
	shareDeltaPP   float64
}

func summarizeWindowPriceRelationships(
	samples []Sample,
	intervalMS int64,
	minimumPairs int,
	limit int,
) ([]WindowPriceRelationship, string, string) {
	if len(samples) < 2 || intervalMS <= 0 || minimumPairs < 2 || limit <= 0 {
		return []WindowPriceRelationship{}, "UNAVAILABLE", "INSUFFICIENT_CONTIGUOUS_PRICE_PAIRS"
	}
	pairsByAsset := make(map[string][]windowPricePair)
	for index := 1; index < len(samples); index++ {
		previous, current := samples[index-1], samples[index]
		if current.BucketMS-previous.BucketMS != intervalMS ||
			previous.Snapshot.Status != "LIVE" || current.Snapshot.Status != "LIVE" ||
			!validPathShare(previous.Snapshot.BithumbSharePct) || !validPathShare(current.Snapshot.BithumbSharePct) {
			continue
		}
		shareDelta := current.Snapshot.BithumbSharePct - previous.Snapshot.BithumbSharePct
		for asset, previousClose := range previous.BithumbClosePricesKRW {
			currentClose, found := current.BithumbClosePricesKRW[asset]
			if !found || previousClose <= 0 || currentClose <= 0 {
				continue
			}
			priceReturn := (currentClose/previousClose - 1) * 100
			if math.IsNaN(priceReturn) || math.IsInf(priceReturn, 0) || math.IsNaN(shareDelta) || math.IsInf(shareDelta, 0) {
				continue
			}
			pairsByAsset[asset] = append(pairsByAsset[asset], windowPricePair{
				priceReturnPct: priceReturn, shareDeltaPP: shareDelta,
			})
		}
	}

	result := make([]WindowPriceRelationship, 0, len(pairsByAsset))
	for asset, pairs := range pairsByAsset {
		if len(pairs) < minimumPairs {
			continue
		}
		correlation, ok := windowPricePearson(pairs)
		if !ok {
			continue
		}
		direction := "SAME_DIRECTION"
		if correlation < 0 {
			direction = "OPPOSITE_DIRECTION"
		}
		result = append(result, WindowPriceRelationship{
			Asset: asset, PairCount: len(pairs), Correlation: correlation, Direction: direction,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		leftAbs, rightAbs := math.Abs(result[left].Correlation), math.Abs(result[right].Correlation)
		if leftAbs == rightAbs {
			return result[left].Asset < result[right].Asset
		}
		return leftAbs > rightAbs
	})
	if len(result) > limit {
		result = result[:limit]
	}
	for index := range result {
		result[index].Rank = index + 1
	}
	if len(result) == 0 {
		return []WindowPriceRelationship{}, "UNAVAILABLE", "INSUFFICIENT_OR_CONSTANT_CONTIGUOUS_PRICE_PAIRS"
	}
	return result, "EXPLORATORY", "PAIRED_CONTIGUOUS_HOURLY_PRICE_RETURN_AND_SHARE_CHANGE"
}

func windowPricePearson(pairs []windowPricePair) (float64, bool) {
	if len(pairs) < 2 {
		return 0, false
	}
	meanPrice, meanShare := 0.0, 0.0
	for _, pair := range pairs {
		meanPrice += pair.priceReturnPct
		meanShare += pair.shareDeltaPP
	}
	meanPrice /= float64(len(pairs))
	meanShare /= float64(len(pairs))
	covariance, priceVariance, shareVariance := 0.0, 0.0, 0.0
	for _, pair := range pairs {
		priceCentered := pair.priceReturnPct - meanPrice
		shareCentered := pair.shareDeltaPP - meanShare
		covariance += priceCentered * shareCentered
		priceVariance += priceCentered * priceCentered
		shareVariance += shareCentered * shareCentered
	}
	if priceVariance <= 0 || shareVariance <= 0 {
		return 0, false
	}
	correlation := covariance / math.Sqrt(priceVariance*shareVariance)
	if math.IsNaN(correlation) || math.IsInf(correlation, 0) {
		return 0, false
	}
	return max(-1.0, min(1.0, correlation)), true
}

func validPathShare(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

// CompareWindowContext hydrates only the two exact endpoint market snapshots
// when a restored lightweight trend does not already hold their asset rows.
// This keeps the long graph history small while still allowing an on-demand
// one-hour coin attribution from the persisted aligned-candle values.
func (t *Tracker) CompareWindowContext(ctx context.Context, windowSec int64) (WindowComparison, error) {
	result := t.CompareWindow(windowSec)
	t.mu.RLock()
	store := t.store
	intervalMS, driverLimit := t.intervalMS, t.driverLimit
	retained := cloneSamples(t.samples)
	t.mu.RUnlock()
	if (result.Status != "LIVE" || !result.EndpointComparison) && store != nil && len(retained) > 0 && intervalMS > 0 {
		if windowSec > 0 && windowSec <= math.MaxInt64/1_000 {
			windowMS := windowSec * 1_000
			if windowMS%intervalMS != 0 {
				return result, nil
			}
			expected := int(windowMS/intervalMS) + 1
			if expected > maxContextWindowSamples {
				result.Reason = "WINDOW_EXCEEDS_DATABASE_INTERACTIVE_LIMIT"
				return result, nil
			}
			currentBucket := retained[len(retained)-1].BucketMS
			persisted, err := store.RecentOfficialTickerMinuteSamples(ctx, currentBucket-windowMS, expected)
			if err != nil {
				return result, fmt.Errorf("load official aligned 1h comparison path: %w", err)
			}
			sort.Slice(persisted, func(i, j int) bool { return persisted[i].BucketMS < persisted[j].BucketMS })
			transient := &Tracker{store: store, intervalMS: intervalMS, trendLimit: len(persisted), driverLimit: driverLimit, samples: cloneSamples(persisted)}
			result = transient.CompareWindow(windowSec)
			if result.Status == "LIVE" && result.EndpointComparison && len(result.Drivers) == 0 {
				if err := transient.hydrateComparisonEndpoints(ctx, result); err != nil {
					return result, err
				}
				result = transient.CompareWindow(windowSec)
			}
			return result, nil
		}
	}
	if result.Status != "LIVE" || !result.EndpointComparison || len(result.Drivers) > 0 ||
		result.Previous == nil || result.Current == nil ||
		result.Previous.AssetRowCount == 0 || result.Current.AssetRowCount == 0 {
		return result, nil
	}

	if store == nil {
		return result, nil
	}
	if err := t.hydrateComparisonEndpoints(ctx, result); err != nil {
		return result, err
	}
	return t.CompareWindow(windowSec), nil
}

// CalendarDayClose returns the exact official snapshot formed from the 24
// aligned hourly candles in [startMS, endMS). It is anchored at the requested
// KST calendar-day boundary rather than an exchange ticker's daily boundary.
// The close remains usable when the intraday path is incomplete; path
// statistics are exposed only when all 25 boundary points exist.
func (t *Tracker) CalendarDayClose(ctx context.Context, startMS, endMS int64) (WindowComparison, error) {
	result := WindowComparison{
		WindowSec: 86_400, Status: "UNAVAILABLE", Reason: "CALENDAR_DAY_NOT_AVAILABLE",
		Drivers: []movement.Driver{}, ExchangeDrivers: []movement.ExchangeDriver{},
		ExpectedSampleCount: 25,
		PathSummary: WindowPathSummary{
			Status: "INCOMPLETE", Reason: "CALENDAR_DAY_PATH_MISSING",
			ExpectedPointCount: 25,
		},
	}
	if t == nil || t.store == nil || startMS <= 0 || endMS <= startMS || endMS-startMS != 86_400_000 {
		result.Reason = "INVALID_CALENDAR_DAY_RANGE"
		return result, nil
	}
	t.mu.RLock()
	intervalMS := t.intervalMS
	t.mu.RUnlock()
	if intervalMS <= 0 || startMS%intervalMS != 0 || endMS%intervalMS != 0 {
		result.Reason = "CALENDAR_DAY_BOUNDARY_NOT_ALIGNED"
		return result, nil
	}
	expected := int((endMS-startMS)/intervalMS) + 1
	if expected != 25 {
		result.Reason = "CALENDAR_DAY_REQUIRES_25_HOURLY_BOUNDARIES"
		return result, nil
	}
	samples, err := t.store.OfficialTickerMinuteSamplesBetween(ctx, startMS, endMS, expected)
	if err != nil {
		return result, fmt.Errorf("load official calendar-day path: %w", err)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].BucketMS < samples[j].BucketMS })
	result.ObservedSampleCount = len(samples)
	result.MissingSampleCount = max(0, expected-len(samples))
	result.PathSummary.PointCount = len(samples)
	pathComplete := len(samples) == expected
	if pathComplete {
		for index, sample := range samples {
			if sample.BucketMS != startMS+int64(index)*intervalMS ||
				sample.Snapshot.Status != "LIVE" || !validPathShare(sample.Snapshot.BithumbSharePct) {
				pathComplete = false
				break
			}
		}
	}
	result.PathComplete = pathComplete
	result.PathSummary = summarizeWindowPath(samples, expected, intervalMS, pathComplete)
	if !pathComplete {
		result.PathSummary.Reason = "CALENDAR_DAY_PATH_GAPS_OR_NON_LIVE_SAMPLES"
	}
	var closeSample *Sample
	for index := range samples {
		if samples[index].BucketMS == endMS {
			closeSample = &samples[index]
			break
		}
	}
	if closeSample == nil {
		result.Status, result.Reason = "WARMING", "CALENDAR_DAY_CLOSE_SAMPLE_MISSING"
		return result, nil
	}
	if closeSample.Snapshot.Status != "LIVE" || !validPathShare(closeSample.Snapshot.BithumbSharePct) {
		result.Status, result.Reason = "GAP", "CALENDAR_DAY_CLOSE_SAMPLE_NOT_LIVE"
		return result, nil
	}
	point := samplePoint(*closeSample)
	result.Current = &point
	result.Status, result.Reason = "LIVE", "EXACT_CALENDAR_DAY_CLOSE"
	return result, nil
}

// History loads an exact bounded range from PostgreSQL so callers can inspect
// backfilled endpoints that are older than the in-memory trend window.
func (t *Tracker) History(ctx context.Context, fromMS, toMS int64) (History, error) {
	result := History{
		ContractVersion: HistoryVersion,
		Status:          "UNAVAILABLE",
		Source:          "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H",
		IntervalSec:     DefaultInterval,
		FromMS:          fromMS,
		ToMS:            toMS,
		Items:           []Point{},
	}
	intervalMS := DefaultInterval * 1_000
	if t == nil || t.store == nil {
		return result, fmt.Errorf("official aligned history store is not configured")
	}
	if fromMS <= 0 || toMS < fromMS || fromMS%intervalMS != 0 || toMS%intervalMS != 0 {
		return result, fmt.Errorf("official aligned history requires ordered exact-hour endpoints")
	}
	expected64 := (toMS-fromMS)/intervalMS + 1
	if expected64 < 1 || expected64 > MaxHistoryPoints {
		return result, fmt.Errorf("official aligned history range must contain between 1 and %d points", MaxHistoryPoints)
	}
	expected := int(expected64)
	result.ExpectedPointCount = expected
	samples, err := t.store.OfficialTickerMinuteSamplesBetween(ctx, fromMS, toMS, expected)
	if err != nil {
		return result, fmt.Errorf("load official aligned history: %w", err)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].BucketMS < samples[j].BucketMS })
	result.Items = make([]Point, 0, len(samples))
	complete := len(samples) == expected
	for _, sample := range samples {
		point := samplePoint(sample)
		result.Items = append(result.Items, point)
		if point.Status != "LIVE" || point.BucketMS < fromMS || point.BucketMS > toMS || point.BucketMS%intervalMS != 0 {
			complete = false
		}
		if strings.TrimSpace(sample.Snapshot.Source) != "" {
			result.Source = sample.Snapshot.Source
		}
	}
	if complete {
		for index, point := range result.Items {
			if point.BucketMS != fromMS+int64(index)*intervalMS {
				complete = false
				break
			}
		}
	}
	result.ObservedPointCount = len(result.Items)
	result.MissingPointCount = max(0, expected-len(result.Items))
	switch {
	case complete:
		result.Status = "LIVE"
	case len(result.Items) > 0:
		result.Status = "PARTIAL"
	}
	return result, nil
}

func (t *Tracker) hydrateComparisonEndpoints(ctx context.Context, result WindowComparison) error {
	if t == nil || t.store == nil || result.Previous == nil || result.Current == nil {
		return nil
	}
	buckets := []int64{result.Previous.BucketMS, result.Current.BucketMS}
	markets, err := t.store.OfficialTickerMinuteMarkets(ctx, buckets)
	if err != nil {
		return fmt.Errorf("load official aligned 1h comparison markets: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for index := range t.samples {
		rows, ok := markets[t.samples[index].BucketMS]
		if !ok || len(rows) == 0 {
			continue
		}
		t.samples[index].Snapshot.Assets = cloneAssetVolumes(rows)
		for key, volume := range rows {
			exchange := t.samples[index].Snapshot.Exchanges[key.Exchange]
			if exchange.Assets == nil {
				exchange.Assets = make(map[string]float64)
			}
			exchange.Assets[key.Asset] = volume
			t.samples[index].Snapshot.Exchanges[key.Exchange] = exchange
		}
	}
	return nil
}

func cloneAssetVolumes(values map[movement.AssetKey]float64) map[movement.AssetKey]float64 {
	result := make(map[movement.AssetKey]float64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func samplePoint(sample Sample) Point {
	return Point{
		BucketMS: sample.BucketMS, ObservedAtMS: sample.Snapshot.AsOfMS,
		Status: sample.Snapshot.Status, TotalVolumeKRW: sample.Snapshot.TotalVolumeKRW,
		BithumbVolumeKRW: sample.Snapshot.BithumbVolumeKRW,
		BithumbSharePct:  sample.Snapshot.BithumbSharePct, AssetRowCount: sample.AssetRowCount,
	}
}

func cloneSamples(samples []Sample) []Sample {
	result := make([]Sample, len(samples))
	for i := range samples {
		result[i] = Sample{
			BucketMS: samples[i].BucketMS, Snapshot: *cloneSnapshot(&samples[i].Snapshot),
			AssetRowCount:         samples[i].AssetRowCount,
			BithumbClosePricesKRW: cloneClosePrices(samples[i].BithumbClosePricesKRW),
		}
	}
	return result
}

func cloneClosePrices(values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	for asset, value := range values {
		result[asset] = value
	}
	return result
}

func cloneSnapshot(snapshot *movement.Snapshot) *movement.Snapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Assets = make(map[movement.AssetKey]float64, len(snapshot.Assets))
	for key, value := range snapshot.Assets {
		copy.Assets[key] = value
	}
	copy.Exchanges = make(map[string]movement.ExchangeSnapshot, len(snapshot.Exchanges))
	for key, exchange := range snapshot.Exchanges {
		exchangeCopy := exchange
		exchangeCopy.Assets = make(map[string]float64, len(exchange.Assets))
		for asset, volume := range exchange.Assets {
			exchangeCopy.Assets[asset] = volume
		}
		copy.Exchanges[key] = exchangeCopy
	}
	return &copy
}

func limitDrivers(rows []movement.Driver, limit int) []movement.Driver {
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return append([]movement.Driver(nil), rows...)
}

func limitExchangeDrivers(rows []movement.ExchangeDriver, limit int) []movement.ExchangeDriver {
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return append([]movement.ExchangeDriver(nil), rows...)
}

func number(value float64) *float64 { return &value }

func unavailableEstimate(reason string) ExpirationEstimate {
	return ExpirationEstimate{
		Status: "UNAVAILABLE", Reason: reason,
		Source: "INFERRED_FROM_ALIGNED_1H_CANDLE_DELTA_AND_WS_INCOMING",
		Assets: []AssetExpirationEstimate{}, Exchanges: []ExchangeExpirationEstimate{},
	}
}

// WithIncomingEstimate applies rolling = previous + incoming - expiring to an
// interval aligned to the two official candle observation times. It never
// promotes the derived expiring amount to a directly observed value.
func WithIncomingEstimate(state State, markets []IncomingMarket, coverageStatus string, coverageSec int64, complete bool) State {
	state.ExpirationEstimate = unavailableEstimate("TWO_CONSECUTIVE_ALIGNED_HOUR_SAMPLES_REQUIRED")
	if state.Status != "LIVE" || state.Previous == nil || state.Current == nil {
		return state
	}
	startMS, endMS := state.Previous.ObservedAtMS, state.Current.ObservedAtMS
	requiredSec := endMS/1000 - startMS/1000
	estimate := unavailableEstimate("WS_INTERVAL_INCOMPLETE")
	estimate.IntervalStartMS, estimate.IntervalEndMS = startMS, endMS
	estimate.RequiredSec, estimate.CoverageSec = requiredSec, coverageSec
	estimate.CoverageStatus = coverageStatus
	if !complete || coverageStatus != "CONFIRMED" || requiredSec <= 0 || coverageSec < requiredSec {
		state.ExpirationEstimate = estimate
		return state
	}
	type split struct{ bithumb, competitor float64 }
	assets := make(map[string]split)
	exchanges := make(map[string]float64)
	for _, market := range markets {
		if market.VolumeKRW < 0 || math.IsNaN(market.VolumeKRW) || math.IsInf(market.VolumeKRW, 0) {
			continue
		}
		exchange := strings.ToLower(strings.TrimSpace(market.Exchange))
		asset := strings.ToUpper(strings.TrimSpace(market.Asset))
		row := assets[asset]
		if exchange == "bithumb" {
			row.bithumb += market.VolumeKRW
		} else {
			row.competitor += market.VolumeKRW
		}
		assets[asset] = row
		exchanges[exchange] += market.VolumeKRW
	}
	incomingB, incomingC := 0.0, 0.0
	for _, row := range assets {
		incomingB += row.bithumb
		incomingC += row.competitor
	}
	deltaB, deltaTotal := 0.0, 0.0
	if state.BithumbVolumeDeltaKRW != nil {
		deltaB = *state.BithumbVolumeDeltaKRW
	}
	if state.DomesticVolumeDeltaKRW != nil {
		deltaTotal = *state.DomesticVolumeDeltaKRW
	}
	expB, okB := inferredNonnegative(incomingB - deltaB)
	expC, okC := inferredNonnegative(incomingC - (deltaTotal - deltaB))
	estimate.Totals = EstimateTotals{IncomingBithumbVolumeKRW: incomingB, IncomingCompetitorVolumeKRW: incomingC}
	if okB && okC {
		estimate.Totals.EstimatedExpiringBithumbKRW = number(expB)
		estimate.Totals.EstimatedExpiringCompetitorKRW = number(expC)
		if expB+expC > 0 {
			estimate.Totals.EstimatedExpiringSharePct = number(expB / (expB + expC) * 100)
		}
	}
	for _, driver := range state.Drivers {
		incoming := assets[strings.ToUpper(driver.Asset)]
		bithumb, bithumbOK := inferredNonnegative(incoming.bithumb - driver.BithumbVolumeChangeKRW)
		competitor, competitorOK := inferredNonnegative(incoming.competitor - driver.CompetitorVolumeChangeKRW)
		row := AssetExpirationEstimate{
			Asset: driver.Asset, Status: "INFERRED",
			TickerBithumbNetChangeKRW:    driver.BithumbVolumeChangeKRW,
			TickerCompetitorNetChangeKRW: driver.CompetitorVolumeChangeKRW,
			IncomingBithumbVolumeKRW:     incoming.bithumb, IncomingCompetitorVolumeKRW: incoming.competitor,
		}
		if bithumbOK && competitorOK {
			row.EstimatedExpiringBithumbKRW = number(bithumb)
			row.EstimatedExpiringCompetitorKRW = number(competitor)
		} else {
			row.Status = "UNAVAILABLE_SOURCE_SKEW"
		}
		estimate.Assets = append(estimate.Assets, row)
	}
	for _, driver := range state.ExchangeDrivers {
		incoming := exchanges[strings.ToLower(driver.Exchange)]
		expiring, ok := inferredNonnegative(incoming - driver.VolumeChangeKRW)
		row := ExchangeExpirationEstimate{
			Exchange: driver.Exchange, Status: "INFERRED", TickerNetChangeKRW: driver.VolumeChangeKRW,
			IncomingVolumeKRW: incoming,
		}
		if ok {
			row.EstimatedExpiringVolumeKRW = number(expiring)
		} else {
			row.Status = "UNAVAILABLE_SOURCE_SKEW"
		}
		estimate.Exchanges = append(estimate.Exchanges, row)
	}
	if okB && okC {
		estimate.Status, estimate.Reason = "INFERRED", "OFFICIAL_CANDLE_NET_CHANGE_PLUS_ALIGNED_WS_INCOMING"
	} else {
		estimate.Status, estimate.Reason = "PARTIAL", "OFFICIAL_AND_WS_SOURCE_TIMING_SKEW"
	}
	state.ExpirationEstimate = estimate
	return state
}

func inferredNonnegative(value float64) (float64, bool) {
	if value >= 0 {
		return value, true
	}
	if value > -1 {
		return 0, true
	}
	return 0, false
}
