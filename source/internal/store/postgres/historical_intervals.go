package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

const maxHistoricalIntervalRequests = 9_000
const maxHistoricalFixedBucketRangeSec = int64(31 * 24 * 60 * 60)

// HistoricalIntervalRead is a bounded, PostgreSQL-backed snapshot for an
// explicit historical question. It deliberately bypasses the live
// aggregator's 48-hour memory horizon; volume_buckets remains the durable
// source of truth for older ranges.
type HistoricalIntervalRead struct {
	Intervals []aggregate.IntervalSnapshotResult
	Coverage  []coverage.Result
}

// HistoricalFixedBuckets groups one bounded range in a single table scan.
// It is used by long-range questions where joining every persisted second to
// hundreds of requested intervals would produce an avoidable nested plan.
func (r *Repository) HistoricalFixedBuckets(
	ctx context.Context,
	start, end, stepSec int64,
) (HistoricalIntervalRead, error) {
	if r == nil || r.pool == nil {
		return HistoricalIntervalRead{}, errors.New("historical PostgreSQL repository is unavailable")
	}
	if start <= 0 || end < start || end-start+1 > maxHistoricalFixedBucketRangeSec ||
		stepSec < 10 || stepSec > 86_400 {
		return HistoricalIntervalRead{}, errors.New("invalid historical fixed-bucket range")
	}
	ranges := fixedBucketRanges(start, end, stepSec)
	result := HistoricalIntervalRead{
		Intervals: make([]aggregate.IntervalSnapshotResult, len(ranges)),
		Coverage:  make([]coverage.Result, len(ranges)),
	}
	byStart := make(map[int64]int, len(ranges))
	for index, item := range ranges {
		byStart[item[0]] = index
		result.Intervals[index] = aggregate.IntervalSnapshotResult{
			Values: make(map[aggregate.VolumeKey]aggregate.Totals), OK: true,
		}
	}
	rows, err := r.pool.Query(ctx, `
		SELECT greatest($1::bigint, (bucket.second / $3::bigint) * $3::bigint) AS bucket_start,
			bucket.scope, bucket.exchange_name, bucket.market, bucket.base, bucket.quote,
			sum(bucket.native_volume), sum(bucket.krw_volume), sum(bucket.trade_count),
			sum(bucket.unresolved_count), sum(bucket.buy_krw_volume),
			sum(bucket.sell_krw_volume), sum(bucket.base_volume)
		FROM volume_buckets AS bucket
		WHERE bucket.second BETWEEN $1 AND $2
			AND bucket.source_kind NOT LIKE 'OFFICIAL_CANDLE%'
		GROUP BY bucket_start, bucket.scope, bucket.exchange_name,
			bucket.market, bucket.base, bucket.quote
		ORDER BY bucket_start`, start, end, stepSec)
	if err != nil {
		return HistoricalIntervalRead{}, fmt.Errorf("query historical fixed buckets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			bucketStart                 int64
			key                         aggregate.VolumeKey
			totals                      aggregate.Totals
			tradeCount, unresolvedCount int64
		)
		if err := rows.Scan(
			&bucketStart, &key.Scope, &key.Exchange, &key.Market, &key.Base, &key.Quote,
			&totals.NativeVolume, &totals.KRWVolume, &tradeCount, &unresolvedCount,
			&totals.BuyKRWVolume, &totals.SellKRWVolume, &totals.BaseVolume,
		); err != nil {
			return HistoricalIntervalRead{}, fmt.Errorf("scan historical fixed bucket: %w", err)
		}
		index, ok := byStart[bucketStart]
		if !ok || tradeCount < 0 || unresolvedCount < 0 {
			return HistoricalIntervalRead{}, errors.New("invalid historical fixed-bucket aggregate")
		}
		totals.TradeCount, totals.UnresolvedCount = uint64(tradeCount), uint64(unresolvedCount)
		if err := totals.Validate(); err != nil {
			return HistoricalIntervalRead{}, fmt.Errorf("validate historical fixed bucket: %w", err)
		}
		result.Intervals[index].Values[key] = totals
	}
	if err := rows.Err(); err != nil {
		return HistoricalIntervalRead{}, fmt.Errorf("read historical fixed buckets: %w", err)
	}
	minuteCoverage, err := r.historicalMinuteCoverage(ctx, start, end)
	if err != nil {
		return HistoricalIntervalRead{}, err
	}
	for index, item := range ranges {
		result.Coverage[index] = calculateHistoricalMinuteCoverage(
			minuteCoverage, item[0], item[1], int64(len(domain.DomesticExchanges)),
		)
	}
	return result, nil
}

func fixedBucketRanges(start, end, stepSec int64) [][2]int64 {
	result := make([][2]int64, 0, int((end-start)/stepSec)+2)
	for current := start; current <= end; {
		next := (current/stepSec + 1) * stepSec
		if next <= current {
			next = current + stepSec
		}
		to := min(end, next-1)
		result = append(result, [2]int64{current, to})
		current = to + 1
	}
	return result
}

type historicalMinuteCoverage struct {
	seconds       int64
	exchangeCount int64
}

// HistoricalIntervals aggregates several (start, end] ranges in one bounded
// SQL read and evaluates their persisted observation coverage. Request order,
// including duplicate ranges, is preserved.
func (r *Repository) HistoricalIntervals(
	ctx context.Context,
	requests []aggregate.IntervalRequest,
) (HistoricalIntervalRead, error) {
	if r == nil || r.pool == nil {
		return HistoricalIntervalRead{}, errors.New("historical PostgreSQL repository is unavailable")
	}
	if len(requests) == 0 || len(requests) > maxHistoricalIntervalRequests {
		return HistoricalIntervalRead{}, fmt.Errorf("historical interval request count must be between 1 and %d", maxHistoricalIntervalRequests)
	}
	starts := make([]int64, len(requests))
	ends := make([]int64, len(requests))
	minimumStart := int64(math.MaxInt64)
	result := HistoricalIntervalRead{
		Intervals: make([]aggregate.IntervalSnapshotResult, len(requests)),
		Coverage:  make([]coverage.Result, len(requests)),
	}
	for index, request := range requests {
		if request.End < request.Start || int64(request.Start) == math.MaxInt64 {
			return HistoricalIntervalRead{}, fmt.Errorf("invalid historical interval at index %d", index)
		}
		starts[index], ends[index] = int64(request.Start), int64(request.End)
		minimumStart = min(minimumStart, starts[index]+1)
		result.Intervals[index] = aggregate.IntervalSnapshotResult{
			Values: make(map[aggregate.VolumeKey]aggregate.Totals), OK: true,
		}
	}

	rows, err := r.pool.Query(ctx, `
		WITH requested AS (
			SELECT ordinality::bigint-1 AS request_index, start_second, end_second
			FROM unnest($1::bigint[], $2::bigint[]) WITH ORDINALITY
				AS value(start_second, end_second, ordinality)
		)
		SELECT requested.request_index,
			bucket.scope, bucket.exchange_name, bucket.market, bucket.base, bucket.quote,
			sum(bucket.native_volume), sum(bucket.krw_volume), sum(bucket.trade_count),
			sum(bucket.unresolved_count), sum(bucket.buy_krw_volume),
			sum(bucket.sell_krw_volume), sum(bucket.base_volume)
		FROM requested
		JOIN volume_buckets AS bucket
			ON bucket.second > requested.start_second
			AND bucket.second <= requested.end_second
		WHERE bucket.source_kind NOT LIKE 'OFFICIAL_CANDLE%'
		GROUP BY requested.request_index, bucket.scope, bucket.exchange_name,
			bucket.market, bucket.base, bucket.quote
		ORDER BY requested.request_index`, starts, ends)
	if err != nil {
		return HistoricalIntervalRead{}, fmt.Errorf("query historical interval aggregates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			index                       int64
			key                         aggregate.VolumeKey
			totals                      aggregate.Totals
			tradeCount, unresolvedCount int64
		)
		if err := rows.Scan(
			&index, &key.Scope, &key.Exchange, &key.Market, &key.Base, &key.Quote,
			&totals.NativeVolume, &totals.KRWVolume, &tradeCount, &unresolvedCount,
			&totals.BuyKRWVolume, &totals.SellKRWVolume, &totals.BaseVolume,
		); err != nil {
			return HistoricalIntervalRead{}, fmt.Errorf("scan historical interval aggregate: %w", err)
		}
		if index < 0 || index >= int64(len(result.Intervals)) || tradeCount < 0 || unresolvedCount < 0 {
			return HistoricalIntervalRead{}, errors.New("invalid historical interval aggregate")
		}
		totals.TradeCount, totals.UnresolvedCount = uint64(tradeCount), uint64(unresolvedCount)
		if err := totals.Validate(); err != nil {
			return HistoricalIntervalRead{}, fmt.Errorf("validate historical interval aggregate: %w", err)
		}
		result.Intervals[index].Values[key] = totals
	}
	if err := rows.Err(); err != nil {
		return HistoricalIntervalRead{}, fmt.Errorf("read historical interval aggregates: %w", err)
	}

	minuteCoverage, err := r.historicalMinuteCoverage(ctx, minimumStart, maximum(ends))
	if err != nil {
		return HistoricalIntervalRead{}, err
	}
	if len(minuteCoverage) > 0 {
		for index := range requests {
			result.Coverage[index] = calculateHistoricalMinuteCoverage(
				minuteCoverage, starts[index]+1, ends[index], int64(len(domain.DomesticExchanges)),
			)
		}
		return result, nil
	}

	// Older databases may predate minute seals. Keep the exact persisted
	// segment/recovery calculation as a compatibility fallback.
	state := State{}
	if err := r.loadCoverage(ctx, domain.UnixSecond(minimumStart), &state); err != nil {
		return HistoricalIntervalRead{}, fmt.Errorf("load historical interval coverage: %w", err)
	}
	for index := range requests {
		result.Coverage[index] = coverage.CalculateWithHistorical(
			state.Coverage.Segments, state.Coverage.HistoricalRecoveries,
			starts[index]+1, ends[index],
		)
	}
	return result, nil
}

func (r *Repository) historicalMinuteCoverage(
	ctx context.Context,
	start, end int64,
) (map[int64]historicalMinuteCoverage, error) {
	if end < start {
		return map[int64]historicalMinuteCoverage{}, nil
	}
	exchanges := make([]string, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		exchanges = append(exchanges, string(exchange))
	}
	rows, err := r.pool.Query(ctx, `
		SELECT minute_start, min(coverage_seconds), count(DISTINCT exchange_name)
		FROM minute_observation_seals
		WHERE minute_start BETWEEN $1 AND $2
			AND exchange_name=ANY($3::text[])
		GROUP BY minute_start
		ORDER BY minute_start`, minuteFloor(start), minuteFloor(end), exchanges)
	if err != nil {
		return nil, fmt.Errorf("query historical minute coverage: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]historicalMinuteCoverage)
	for rows.Next() {
		var minute int64
		var value historicalMinuteCoverage
		if err := rows.Scan(&minute, &value.seconds, &value.exchangeCount); err != nil {
			return nil, fmt.Errorf("scan historical minute coverage: %w", err)
		}
		if minute <= 0 || value.seconds < 0 || value.seconds > 60 || value.exchangeCount < 0 {
			return nil, errors.New("invalid historical minute coverage")
		}
		result[minute] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read historical minute coverage: %w", err)
	}
	return result, nil
}

func calculateHistoricalMinuteCoverage(
	minutes map[int64]historicalMinuteCoverage,
	start, end, requiredExchanges int64,
) coverage.Result {
	if start <= 0 || end < start || requiredExchanges < 1 {
		return coverage.Result{Status: "UNAVAILABLE"}
	}
	required := end - start + 1
	covered := int64(0)
	for minute := minuteFloor(start); minute <= minuteFloor(end); minute += 60 {
		overlapStart, overlapEnd := max(start, minute), min(end, minute+59)
		overlap := overlapEnd - overlapStart + 1
		value, ok := minutes[minute]
		if !ok || value.exchangeCount < requiredExchanges {
			continue
		}
		if value.seconds >= 60 {
			covered += overlap
			continue
		}
		// A partial seal stores a count, not the exact covered second positions.
		// Count only the seconds guaranteed to overlap this clipped boundary.
		guaranteed := value.seconds - (60 - overlap)
		if guaranteed > 0 {
			covered += min(overlap, guaranteed)
		}
	}
	status := "PARTIAL"
	if covered == 0 {
		status = "UNAVAILABLE"
	} else if covered >= required {
		covered, status = required, "CONFIRMED"
	}
	return coverage.Result{
		Status: status, CoverageSec: covered, RequiredSec: required,
		GapSeconds: required - covered, WindowComplete: status == "CONFIRMED",
		WebSocketCoverage: covered,
	}
}

func minuteFloor(second int64) int64 { return second - second%60 }

func maximum(values []int64) int64 {
	result := int64(math.MinInt64)
	for _, value := range values {
		result = max(result, value)
	}
	return result
}
