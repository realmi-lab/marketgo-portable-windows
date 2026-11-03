package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
)

const minuteSealColumns = `exchange_name, minute_start, status,
    coverage_seconds, gap_seconds, registry_market_count, observed_market_count,
    observed_zero_count, unknown_market_count, gap_market_count,
    scheduled_market_count, registry_fingerprint, sealed_at, updated_at`

// SaveMinuteSeal writes one compact row per exchange and minute. The upsert is
// intentional: a later coverage observation may conservatively downgrade a
// recent seal without creating per-market zero rows.
func (r *Repository) SaveMinuteSeal(ctx context.Context, value reconcile.MinuteSeal) error {
	return r.SaveMinuteSeals(ctx, []reconcile.MinuteSeal{value})
}

// SaveMinuteSeals writes all exchange seals for a close or audit in one
// PostgreSQL statement. Besides removing one network round trip per exchange,
// the statement is atomic: callers never observe only part of a minute batch.
func (r *Repository) SaveMinuteSeals(ctx context.Context, values []reconcile.MinuteSeal) error {
	return saveMinuteSeals(ctx, r.pool, values)
}

func saveMinuteSeals(ctx context.Context, query ingestionQueryExecutor, values []reconcile.MinuteSeal) error {
	if len(values) == 0 {
		return nil
	}
	exchanges := make([]string, len(values))
	minutes := make([]int64, len(values))
	statuses := make([]string, len(values))
	coverageSeconds := make([]int64, len(values))
	gapSeconds := make([]int64, len(values))
	registryCounts := make([]int64, len(values))
	observedCounts := make([]int64, len(values))
	observedZeroCounts := make([]int64, len(values))
	unknownCounts := make([]int64, len(values))
	gapMarketCounts := make([]int64, len(values))
	scheduledCounts := make([]int64, len(values))
	fingerprints := make([]string, len(values))
	sealedAt := make([]time.Time, len(values))
	updatedAt := make([]time.Time, len(values))
	for index := range values {
		// The input may be retained for an atomic checkpoint retry. Normalize
		// only this SQL value, never the caller's pending result slice.
		value := values[index]
		if err := value.Normalize(); err != nil {
			return err
		}
		exchanges[index] = string(value.Exchange)
		minutes[index] = int64(value.MinuteStart)
		statuses[index] = string(value.Status)
		coverageSeconds[index] = value.CoverageSeconds
		gapSeconds[index] = value.GapSeconds
		registryCounts[index] = value.RegistryMarketCount
		observedCounts[index] = value.ObservedMarketCount
		observedZeroCounts[index] = value.ObservedZeroCount
		unknownCounts[index] = value.UnknownMarketCount
		gapMarketCounts[index] = value.GapMarketCount
		scheduledCounts[index] = value.ScheduledMarketCount
		fingerprints[index] = value.RegistryFingerprint
		sealedAt[index] = value.SealedAt
		updatedAt[index] = value.UpdatedAt
	}
	_, err := query.Exec(ctx, `
        INSERT INTO minute_observation_seals (`+minuteSealColumns+`)
        SELECT * FROM unnest(
            $1::text[], $2::bigint[], $3::text[], $4::bigint[], $5::bigint[],
            $6::bigint[], $7::bigint[], $8::bigint[], $9::bigint[], $10::bigint[],
            $11::bigint[], $12::text[], $13::timestamptz[], $14::timestamptz[])
        ON CONFLICT (exchange_name, minute_start) DO UPDATE SET
            status=EXCLUDED.status,
            coverage_seconds=EXCLUDED.coverage_seconds,
            gap_seconds=EXCLUDED.gap_seconds,
            registry_market_count=EXCLUDED.registry_market_count,
            observed_market_count=EXCLUDED.observed_market_count,
            observed_zero_count=EXCLUDED.observed_zero_count,
            unknown_market_count=EXCLUDED.unknown_market_count,
            gap_market_count=EXCLUDED.gap_market_count,
            scheduled_market_count=EXCLUDED.scheduled_market_count,
            registry_fingerprint=EXCLUDED.registry_fingerprint,
            sealed_at=LEAST(minute_observation_seals.sealed_at, EXCLUDED.sealed_at),
            updated_at=EXCLUDED.updated_at`,
		exchanges, minutes, statuses, coverageSeconds, gapSeconds, registryCounts,
		observedCounts, observedZeroCounts, unknownCounts, gapMarketCounts,
		scheduledCounts, fingerprints, sealedAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("save minute observation seal batch: %w", err)
	}
	return nil
}

func (r *Repository) GetMinuteSeal(ctx context.Context, exchange domain.Exchange, minute domain.UnixSecond) (*reconcile.MinuteSeal, error) {
	return getMinuteSeal(ctx, r.pool, exchange, minute)
}

func getMinuteSeal(ctx context.Context, query rowQuerier, exchange domain.Exchange, minute domain.UnixSecond) (*reconcile.MinuteSeal, error) {
	if int64(minute)%reconcile.MinuteSeconds != 0 {
		return nil, errors.New("minute start must be aligned to a 60-second boundary")
	}
	row := reconcile.MinuteSeal{}
	err := query.QueryRow(ctx, `SELECT `+minuteSealColumns+`
        FROM minute_observation_seals
        WHERE exchange_name=$1 AND minute_start=$2`, exchange, minute).Scan(
		&row.Exchange, &row.MinuteStart, &row.Status,
		&row.CoverageSeconds, &row.GapSeconds, &row.RegistryMarketCount,
		&row.ObservedMarketCount, &row.ObservedZeroCount, &row.UnknownMarketCount,
		&row.GapMarketCount, &row.ScheduledMarketCount, &row.RegistryFingerprint,
		&row.SealedAt, &row.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get minute observation seal: %w", err)
	}
	return &row, nil
}

func (r *Repository) LatestMinuteSeals(ctx context.Context) (*domain.UnixSecond, map[string]reconcile.MinuteSeal, error) {
	result := make(map[string]reconcile.MinuteSeal, len(domain.DomesticExchanges))
	domestic := make([]string, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		domestic = append(domestic, string(exchange))
	}
	var latest *int64
	if err := r.pool.QueryRow(ctx, `SELECT MAX(minute_start)
        FROM minute_observation_seals WHERE exchange_name = ANY($1)`, domestic).Scan(&latest); err != nil {
		return nil, result, fmt.Errorf("get latest minute observation seal: %w", err)
	}
	if latest == nil {
		return nil, result, nil
	}
	minute := domain.UnixSecond(*latest)
	rows, err := r.pool.Query(ctx, `SELECT `+minuteSealColumns+`
        FROM minute_observation_seals
        WHERE minute_start=$1 AND exchange_name = ANY($2)
        ORDER BY exchange_name`, minute, domestic)
	if err != nil {
		return nil, result, fmt.Errorf("query latest minute observation seals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		row := reconcile.MinuteSeal{}
		if err := rows.Scan(
			&row.Exchange, &row.MinuteStart, &row.Status,
			&row.CoverageSeconds, &row.GapSeconds, &row.RegistryMarketCount,
			&row.ObservedMarketCount, &row.ObservedZeroCount, &row.UnknownMarketCount,
			&row.GapMarketCount, &row.ScheduledMarketCount, &row.RegistryFingerprint,
			&row.SealedAt, &row.UpdatedAt,
		); err != nil {
			return nil, result, fmt.Errorf("scan latest minute observation seal: %w", err)
		}
		result[string(row.Exchange)] = row
	}
	if err := rows.Err(); err != nil {
		return nil, result, err
	}
	return &minute, result, nil
}
