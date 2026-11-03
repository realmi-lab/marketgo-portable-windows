package tickertrend

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/movement"
)

const (
	minDailyEndpointCount = 2
	maxDailyEndpointCount = 8
)

var dailyEndpointKST = time.FixedZone("Asia/Seoul", 9*60*60)

type detailedMarketStore interface {
	OfficialTickerMinuteMarketRows(
		context.Context,
		[]int64,
	) (map[int64]map[movement.AssetKey]PersistedMarketRow, error)
}

// CompareDailyEndpoints loads 2-8 exact rolling-24h endpoints at the same KST
// clock hour. Adjacent selected dates and the first-to-last cumulative interval
// are compared independently of interior hourly path completeness.
func (t *Tracker) CompareDailyEndpoints(
	ctx context.Context,
	endpointsMS []int64,
) (DailyEndpointSeries, error) {
	result := newDailyEndpointSeries()
	intervalMS := DefaultInterval * 1_000
	if t == nil || t.store == nil {
		return result, fmt.Errorf("official aligned daily endpoint store is not configured")
	}
	if err := validateDailyEndpoints(endpointsMS, intervalMS); err != nil {
		return result, err
	}

	firstMS, lastMS := endpointsMS[0], endpointsMS[len(endpointsMS)-1]
	expected64 := (lastMS-firstMS)/intervalMS + 1
	if expected64 < 1 || expected64 > MaxHistoryPoints {
		return result, fmt.Errorf(
			"official aligned daily endpoint path must contain between 1 and %d hourly points",
			MaxHistoryPoints,
		)
	}
	expected := int(expected64)
	result.ExpectedHourlyPointCount = expected

	samples, err := t.store.OfficialTickerMinuteSamplesBetween(ctx, firstMS, lastMS, expected)
	if err != nil {
		return result, fmt.Errorf("load official aligned daily endpoint path: %w", err)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].BucketMS < samples[j].BucketMS })
	byBucket := make(map[int64]Sample, len(samples))
	for _, sample := range samples {
		if sample.BucketMS < firstMS || sample.BucketMS > lastMS || sample.BucketMS%intervalMS != 0 {
			continue
		}
		byBucket[sample.BucketMS] = sample
	}

	result.ObservedHourlyPointCount, result.MissingBucketMS, result.NonLiveBucketMS =
		dailyPathQuality(byBucket, firstMS, lastMS, intervalMS)
	result.PathComplete = result.ObservedHourlyPointCount == result.ExpectedHourlyPointCount &&
		len(result.MissingBucketMS) == 0 && len(result.NonLiveBucketMS) == 0

	if err := t.hydrateDailyEndpointMarkets(ctx, endpointsMS, byBucket); err != nil {
		return result, err
	}

	result.Points = make([]DailyEndpointPoint, 0, len(endpointsMS))
	exactCount := 0
	for _, endpointMS := range endpointsMS {
		sample, found := byBucket[endpointMS]
		point := dailyEndpointPoint(endpointMS, sample, found)
		if point.ExactEndpointAvailable {
			exactCount++
		}
		result.Points = append(result.Points, point)
	}

	result.Transitions = make([]DailyEndpointTransition, 0, len(endpointsMS)-1)
	for index := 1; index < len(endpointsMS); index++ {
		result.Transitions = append(result.Transitions, dailyEndpointTransition(
			endpointsMS[index-1], endpointsMS[index], byBucket, intervalMS,
		))
	}
	result.Cumulative = dailyEndpointTransition(firstMS, lastMS, byBucket, intervalMS)

	switch {
	case exactCount == len(endpointsMS) && result.PathComplete:
		result.Status, result.Reason = "LIVE", "EXACT_ENDPOINTS_AND_COMPLETE_HOURLY_PATH"
	case exactCount == len(endpointsMS):
		result.Status, result.Reason = "LIVE", "EXACT_ENDPOINTS_WITH_HOURLY_PATH_GAPS"
	case exactCount > 0:
		result.Status, result.Reason = "PARTIAL", "ONE_OR_MORE_EXACT_ENDPOINTS_UNAVAILABLE"
	default:
		result.Status, result.Reason = "UNAVAILABLE", "EXACT_ENDPOINTS_UNAVAILABLE"
	}
	return result, nil
}

func newDailyEndpointSeries() DailyEndpointSeries {
	return DailyEndpointSeries{
		ContractVersion: DailySeriesVersion,
		Status:          "UNAVAILABLE",
		Reason:          "DAILY_ENDPOINTS_NOT_LOADED",
		Points:          []DailyEndpointPoint{},
		Transitions:     []DailyEndpointTransition{},
		MissingBucketMS: []int64{},
		NonLiveBucketMS: []int64{},
		IncludedExchanges: []string{
			"upbit", "bithumb", "coinone",
		},
		ExcludedExchanges: map[string]string{
			"korbit": "OFFICIAL_CANDLE_QUOTE_VOLUME_UNAVAILABLE",
			"gopax":  "OFFICIAL_CANDLE_QUOTE_VOLUME_UNAVAILABLE",
		},
		Cumulative: unavailableDailyTransition(0, 0, "DAILY_ENDPOINTS_NOT_LOADED"),
	}
}

func validateDailyEndpoints(endpointsMS []int64, intervalMS int64) error {
	if len(endpointsMS) < minDailyEndpointCount || len(endpointsMS) > maxDailyEndpointCount {
		return fmt.Errorf("official aligned daily comparison requires between %d and %d endpoints", minDailyEndpointCount, maxDailyEndpointCount)
	}
	if intervalMS <= 0 {
		return fmt.Errorf("official aligned daily comparison interval is invalid")
	}
	firstLocal := time.UnixMilli(endpointsMS[0]).In(dailyEndpointKST)
	firstHour, firstMinute, firstSecond := firstLocal.Clock()
	for index, endpointMS := range endpointsMS {
		if endpointMS <= 0 || endpointMS%intervalMS != 0 {
			return fmt.Errorf("official aligned daily endpoint %d is not an exact positive UTC hour", index)
		}
		if index > 0 && endpointMS <= endpointsMS[index-1] {
			return fmt.Errorf("official aligned daily endpoints must be strictly increasing")
		}
		local := time.UnixMilli(endpointMS).In(dailyEndpointKST)
		hour, minute, second := local.Clock()
		if hour != firstHour || minute != firstMinute || second != firstSecond || local.Nanosecond() != firstLocal.Nanosecond() {
			return fmt.Errorf("official aligned daily endpoints must use the same KST clock time")
		}
	}
	return nil
}

func dailyPathQuality(
	byBucket map[int64]Sample,
	fromMS int64,
	toMS int64,
	intervalMS int64,
) (int, []int64, []int64) {
	observed := 0
	missing := make([]int64, 0)
	nonLive := make([]int64, 0)
	for bucketMS := fromMS; ; bucketMS += intervalMS {
		sample, found := byBucket[bucketMS]
		if !found {
			missing = append(missing, bucketMS)
		} else {
			observed++
			if !validDailyEndpointSample(sample, bucketMS) {
				nonLive = append(nonLive, bucketMS)
			}
		}
		if bucketMS == toMS {
			break
		}
	}
	return observed, missing, nonLive
}

func (t *Tracker) hydrateDailyEndpointMarkets(
	ctx context.Context,
	endpointsMS []int64,
	byBucket map[int64]Sample,
) error {
	// Range rows carry only endpoint totals in PostgreSQL. Clear any adapter-local
	// embedded copies as well so attribution and exact closes always come from the
	// dedicated persisted market-row read, never from an unverified duplicate.
	for _, bucketMS := range endpointsMS {
		sample, found := byBucket[bucketMS]
		if !found {
			continue
		}
		sample.Snapshot.Assets = make(map[movement.AssetKey]float64)
		for exchangeName, exchange := range sample.Snapshot.Exchanges {
			exchange.Assets = nil
			sample.Snapshot.Exchanges[exchangeName] = exchange
		}
		sample.BithumbClosePricesKRW = map[string]float64{}
		byBucket[bucketMS] = sample
	}
	if reader, ok := t.store.(detailedMarketStore); ok {
		rowsByBucket, err := reader.OfficialTickerMinuteMarketRows(ctx, endpointsMS)
		if err != nil {
			return fmt.Errorf("load official aligned daily endpoint market rows: %w", err)
		}
		for _, bucketMS := range endpointsMS {
			sample, found := byBucket[bucketMS]
			if !found || !completePersistedMarketRows(sample, rowsByBucket[bucketMS]) {
				continue
			}
			setSamplePersistedMarketRows(&sample, rowsByBucket[bucketMS])
			byBucket[bucketMS] = sample
		}
		return nil
	}

	rowsByBucket, err := t.store.OfficialTickerMinuteMarkets(ctx, endpointsMS)
	if err != nil {
		return fmt.Errorf("load official aligned daily endpoint markets: %w", err)
	}
	for _, bucketMS := range endpointsMS {
		sample, found := byBucket[bucketMS]
		if !found || !endpointMarketRowsComplete(sample, rowsByBucket[bucketMS]) {
			continue
		}
		setSampleMarketRows(&sample, rowsByBucket[bucketMS])
		byBucket[bucketMS] = sample
	}
	return nil
}

func completePersistedMarketRows(sample Sample, rows map[movement.AssetKey]PersistedMarketRow) bool {
	if sample.AssetRowCount <= 0 || len(rows) != sample.AssetRowCount {
		return false
	}
	volumes := make(map[movement.AssetKey]float64, len(rows))
	for key, row := range rows {
		if strings.TrimSpace(key.Exchange) == "" || strings.TrimSpace(key.Asset) == "" ||
			row.VolumeKRW < 0 || math.IsNaN(row.VolumeKRW) || math.IsInf(row.VolumeKRW, 0) {
			return false
		}
		if row.ClosePriceKRW != nil && (*row.ClosePriceKRW <= 0 || math.IsNaN(*row.ClosePriceKRW) || math.IsInf(*row.ClosePriceKRW, 0)) {
			return false
		}
		volumes[key] = row.VolumeKRW
	}
	return endpointMarketRowsComplete(sample, volumes)
}

func setSamplePersistedMarketRows(sample *Sample, rows map[movement.AssetKey]PersistedMarketRow) {
	if sample == nil {
		return
	}
	volumes := make(map[movement.AssetKey]float64, len(rows))
	closes := make(map[string]float64)
	for key, row := range rows {
		volumes[key] = row.VolumeKRW
		if strings.EqualFold(key.Exchange, "bithumb") && row.ClosePriceKRW != nil {
			closes[strings.ToUpper(strings.TrimSpace(key.Asset))] = *row.ClosePriceKRW
		}
	}
	setSampleMarketRows(sample, volumes)
	sample.BithumbClosePricesKRW = closes
}

func dailyEndpointPoint(endpointMS int64, sample Sample, found bool) DailyEndpointPoint {
	point := DailyEndpointPoint{
		BucketMS: endpointMS, Status: "UNAVAILABLE", Reason: "EXACT_ENDPOINT_MISSING",
		BithumbClosePricesKRW: map[string]float64{},
	}
	if !found {
		return point
	}
	point.ObservedAtMS = sample.Snapshot.AsOfMS
	point.AssetRowCount = sample.AssetRowCount
	if !validDailyEndpointSample(sample, endpointMS) {
		point.Reason = "EXACT_ENDPOINT_NOT_LIVE_OR_INVALID"
		return point
	}
	point.Status, point.Reason = "LIVE", "EXACT_ENDPOINT_AVAILABLE"
	point.ExactEndpointAvailable = true
	point.TotalVolumeKRW = number(sample.Snapshot.TotalVolumeKRW)
	point.BithumbVolumeKRW = number(sample.Snapshot.BithumbVolumeKRW)
	point.BithumbSharePct = number(sample.Snapshot.BithumbSharePct)
	point.AssetDetailsAvailable = sampleAssetRowsComplete(sample)
	point.BithumbClosePricesKRW = cloneClosePrices(sample.BithumbClosePricesKRW)
	return point
}

func validDailyEndpointSample(sample Sample, endpointMS int64) bool {
	return sample.BucketMS == endpointMS && sample.Snapshot.AsOfMS > 0 &&
		sample.Snapshot.Status == "LIVE" && officialSnapshotTotalsReconcile(&sample.Snapshot)
}

func sampleAssetRowsComplete(sample Sample) bool {
	return sample.AssetRowCount > 0 && len(sample.Snapshot.Assets) == sample.AssetRowCount &&
		marketRowsReconcileSnapshot(&sample.Snapshot, sample.Snapshot.Assets)
}

func dailyEndpointTransition(
	fromMS int64,
	toMS int64,
	byBucket map[int64]Sample,
	intervalMS int64,
) DailyEndpointTransition {
	result := unavailableDailyTransition(fromMS, toMS, "EXACT_ENDPOINTS_UNAVAILABLE")
	if toMS <= fromMS || intervalMS <= 0 || (toMS-fromMS)%intervalMS != 0 {
		result.Reason = "INVALID_DAILY_ENDPOINT_TRANSITION"
		return result
	}
	result.WindowSec = (toMS - fromMS) / 1_000
	result.ExpectedHourlyPointCount = int((toMS-fromMS)/intervalMS) + 1
	result.ObservedHourlyPointCount, result.MissingBucketMS, _ = dailyPathQuality(byBucket, fromMS, toMS, intervalMS)
	result.PathComplete = result.ObservedHourlyPointCount == result.ExpectedHourlyPointCount && len(result.MissingBucketMS) == 0
	if result.PathComplete {
		for bucketMS := fromMS; ; bucketMS += intervalMS {
			if !validDailyEndpointSample(byBucket[bucketMS], bucketMS) {
				result.PathComplete = false
				break
			}
			if bucketMS == toMS {
				break
			}
		}
	}

	previous, previousFound := byBucket[fromMS]
	current, currentFound := byBucket[toMS]
	if !previousFound || !currentFound || !validDailyEndpointSample(previous, fromMS) || !validDailyEndpointSample(current, toMS) {
		return result
	}

	result.Status = "LIVE"
	result.Reason = "EXACT_ENDPOINTS_AND_COMPLETE_HOURLY_PATH"
	if !result.PathComplete {
		result.Reason = "EXACT_ENDPOINTS_WITH_HOURLY_PATH_GAPS"
	}
	result.EndpointComparison = true
	result.ShareDeltaPP = number(current.Snapshot.BithumbSharePct - previous.Snapshot.BithumbSharePct)
	result.DomesticVolumeDeltaKRW = number(current.Snapshot.TotalVolumeKRW - previous.Snapshot.TotalVolumeKRW)
	result.BithumbVolumeDeltaKRW = number(current.Snapshot.BithumbVolumeKRW - previous.Snapshot.BithumbVolumeKRW)
	result.ExchangeDrivers = append(
		[]movement.ExchangeDriver(nil),
		movement.AttributeExchangeSnapshots(&previous.Snapshot, &current.Snapshot)...,
	)
	if !sampleAssetRowsComplete(previous) || !sampleAssetRowsComplete(current) {
		return result
	}
	drivers, exchanges := movement.AttributeSnapshots(&previous.Snapshot, &current.Snapshot)
	result.AssetAttributionAvailable = true
	result.Drivers = dailyEndpointDrivers(drivers, previous.BithumbClosePricesKRW, current.BithumbClosePricesKRW)
	result.ExchangeDrivers = append([]movement.ExchangeDriver(nil), exchanges...)
	return result
}

func unavailableDailyTransition(fromMS, toMS int64, reason string) DailyEndpointTransition {
	return DailyEndpointTransition{
		FromBucketMS: fromMS, ToBucketMS: toMS, Status: "UNAVAILABLE", Reason: reason,
		MissingBucketMS: []int64{}, Drivers: []DailyEndpointDriver{},
		ExchangeDrivers: []movement.ExchangeDriver{},
	}
}

func dailyEndpointDrivers(
	drivers []movement.Driver,
	previousCloses map[string]float64,
	currentCloses map[string]float64,
) []DailyEndpointDriver {
	result := make([]DailyEndpointDriver, 0, len(drivers))
	for _, driver := range drivers {
		row := DailyEndpointDriver{
			Rank: driver.Rank, Asset: driver.Asset, ContributionPP: driver.ContributionPP,
			BithumbVolumeChangeKRW:      driver.BithumbVolumeChangeKRW,
			CompetitorVolumeChangeKRW:   driver.CompetitorVolumeChangeKRW,
			PreviousBithumbVolumeKRW:    driver.PreviousBithumbVolumeKRW,
			CurrentBithumbVolumeKRW:     driver.CurrentBithumbVolumeKRW,
			PreviousCompetitorVolumeKRW: driver.PreviousCompetitorVolumeKRW,
			CurrentCompetitorVolumeKRW:  driver.CurrentCompetitorVolumeKRW,
		}
		asset := strings.ToUpper(strings.TrimSpace(driver.Asset))
		if value, found := previousCloses[asset]; found && value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			row.PreviousClosePriceKRW = number(value)
		}
		if value, found := currentCloses[asset]; found && value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			row.CurrentClosePriceKRW = number(value)
		}
		if row.PreviousClosePriceKRW != nil && row.CurrentClosePriceKRW != nil {
			change := *row.CurrentClosePriceKRW - *row.PreviousClosePriceKRW
			row.ClosePriceChangeKRW = number(change)
			row.ClosePriceChangePct = number(change / *row.PreviousClosePriceKRW * 100)
		}
		result = append(result, row)
	}
	return result
}
