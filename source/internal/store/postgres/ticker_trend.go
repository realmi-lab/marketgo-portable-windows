package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/movement"
	"market-share-go/internal/tickertrend"
)

type officialTickerTables struct {
	samples string
	markets string
	label   string
}

var alignedHourTables = officialTickerTables{
	"official_aligned_1h_samples",
	"official_aligned_1h_markets",
	"official aligned 1h candle",
}

func (r *Repository) SaveOfficialTickerMinuteSample(ctx context.Context, sample tickertrend.Sample) error {
	return r.saveOfficialTickerSample(ctx, sample, alignedHourTables)
}

// EnrichOfficialAlignedBithumbCloses updates only exact Bithumb close prices
// on market rows that already belong to a persisted historical endpoint. It
// deliberately leaves the endpoint totals, registry-sized row set, volumes,
// status, and provenance untouched: a later registry snapshot must never
// rewrite an older market-share fact merely to populate the nullable price
// column added by migration 0022.
func (r *Repository) EnrichOfficialAlignedBithumbCloses(
	ctx context.Context,
	bucketMS int64,
	closes map[string]float64,
) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, fmt.Errorf("official aligned close repository is not configured")
	}
	if bucketMS <= 0 {
		return 0, fmt.Errorf("official aligned close bucket must be positive")
	}
	normalized, err := normalizedTickerTrendClosePrices(closes)
	if err != nil {
		return 0, fmt.Errorf("validate official aligned Bithumb closes: %w", err)
	}
	if len(normalized) == 0 {
		return 0, nil
	}
	assets := make([]string, 0, len(normalized))
	for asset := range normalized {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	prices := make([]float64, len(assets))
	for index, asset := range assets {
		prices[index] = normalized[asset]
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin official aligned close enrichment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `WITH incoming(asset, close_price_krw) AS (
        SELECT * FROM unnest($2::text[], $3::double precision[])
    )
    UPDATE official_aligned_1h_markets AS market
    SET close_price_krw=incoming.close_price_krw
    FROM incoming
    WHERE market.bucket_ms=$1
      AND lower(market.exchange_name)='bithumb'
      AND upper(market.asset)=incoming.asset`, bucketMS, assets, prices)
	if err != nil {
		return 0, fmt.Errorf("enrich official aligned Bithumb closes: %w", err)
	}
	updated := tag.RowsAffected()
	if updated != int64(len(assets)) {
		return 0, fmt.Errorf(
			"official aligned close enrichment matched %d of %d historical Bithumb rows",
			updated, len(assets),
		)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit official aligned close enrichment: %w", err)
	}
	return updated, nil
}

func (r *Repository) saveOfficialTickerSample(
	ctx context.Context,
	sample tickertrend.Sample,
	tables officialTickerTables,
) error {
	closePrices, err := normalizedTickerTrendClosePrices(sample.BithumbClosePricesKRW)
	if err != nil {
		return fmt.Errorf("validate %s Bithumb closes: %w", tables.label, err)
	}
	exchanges, err := json.Marshal(sample.Snapshot.Exchanges)
	if err != nil {
		return fmt.Errorf("encode %s exchanges: %w", tables.label, err)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s sample: %w", tables.label, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO `+tables.samples+` (
        bucket_ms, observed_at_ms, status, source, collection_span_ms,
        total_volume_krw, bithumb_volume_krw, bithumb_share_pct,
        exchanges_json, asset_row_count
    ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    ON CONFLICT(bucket_ms) DO UPDATE SET
        observed_at_ms=EXCLUDED.observed_at_ms,
        status=EXCLUDED.status,
        source=EXCLUDED.source,
        collection_span_ms=EXCLUDED.collection_span_ms,
        total_volume_krw=EXCLUDED.total_volume_krw,
        bithumb_volume_krw=EXCLUDED.bithumb_volume_krw,
        bithumb_share_pct=EXCLUDED.bithumb_share_pct,
        exchanges_json=EXCLUDED.exchanges_json,
        asset_row_count=EXCLUDED.asset_row_count`,
		sample.BucketMS, sample.Snapshot.AsOfMS, sample.Snapshot.Status, sample.Snapshot.Source,
		sample.Snapshot.CollectionSpanMS, sample.Snapshot.TotalVolumeKRW,
		sample.Snapshot.BithumbVolumeKRW, sample.Snapshot.BithumbSharePct,
		exchanges, sample.AssetRowCount,
	)
	if err != nil {
		return fmt.Errorf("upsert %s sample: %w", tables.label, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM `+tables.markets+` WHERE bucket_ms=$1`, sample.BucketMS); err != nil {
		return fmt.Errorf("clear %s markets: %w", tables.label, err)
	}
	rows := make([][]any, 0, len(sample.Snapshot.Assets))
	writtenCloses := make(map[string]struct{}, len(closePrices))
	for key, volume := range sample.Snapshot.Assets {
		var closePrice any
		if strings.EqualFold(key.Exchange, "bithumb") {
			asset := strings.ToUpper(strings.TrimSpace(key.Asset))
			if value, found := closePrices[asset]; found {
				closePrice = value
				writtenCloses[asset] = struct{}{}
			}
		}
		rows = append(rows, []any{sample.BucketMS, key.Exchange, key.Asset, volume, closePrice})
	}
	if len(writtenCloses) != len(closePrices) {
		return fmt.Errorf("%s Bithumb close has no matching market row", tables.label)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i][1].(string) == rows[j][1].(string) {
			return rows[i][2].(string) < rows[j][2].(string)
		}
		return rows[i][1].(string) < rows[j][1].(string)
	})
	if len(rows) > 0 {
		_, err = tx.CopyFrom(
			ctx,
			pgx.Identifier{tables.markets},
			[]string{"bucket_ms", "exchange_name", "asset", "volume_krw", "close_price_krw"},
			pgx.CopyFromRows(rows),
		)
		if err != nil {
			return fmt.Errorf("copy %s markets: %w", tables.label, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s sample: %w", tables.label, err)
	}
	return nil
}

func (r *Repository) RecentOfficialTickerMinuteSamples(ctx context.Context, sinceMS int64, limit int) ([]tickertrend.Sample, error) {
	return r.recentOfficialTickerSamples(ctx, sinceMS, limit, alignedHourTables)
}

func (r *Repository) OfficialTickerMinuteSamplesBetween(
	ctx context.Context,
	startMS int64,
	endMS int64,
	limit int,
) ([]tickertrend.Sample, error) {
	if limit < 1 || startMS <= 0 || endMS < startMS {
		return []tickertrend.Sample{}, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT
        bucket_ms, observed_at_ms, status, source, collection_span_ms,
        total_volume_krw, bithumb_volume_krw, bithumb_share_pct,
        exchanges_json, asset_row_count
    FROM official_aligned_1h_samples
    WHERE bucket_ms >= $1 AND bucket_ms <= $2
    ORDER BY bucket_ms
    LIMIT $3`, startMS, endMS, limit)
	if err != nil {
		return nil, fmt.Errorf("query official calendar-day samples: %w", err)
	}
	defer rows.Close()
	result := make([]tickertrend.Sample, 0, limit)
	for rows.Next() {
		var sample tickertrend.Sample
		var exchangesJSON []byte
		if err := rows.Scan(
			&sample.BucketMS, &sample.Snapshot.AsOfMS, &sample.Snapshot.Status,
			&sample.Snapshot.Source, &sample.Snapshot.CollectionSpanMS,
			&sample.Snapshot.TotalVolumeKRW, &sample.Snapshot.BithumbVolumeKRW,
			&sample.Snapshot.BithumbSharePct, &exchangesJSON, &sample.AssetRowCount,
		); err != nil {
			return nil, fmt.Errorf("scan official calendar-day sample: %w", err)
		}
		if err := json.Unmarshal(exchangesJSON, &sample.Snapshot.Exchanges); err != nil {
			return nil, fmt.Errorf("decode official calendar-day exchanges: %w", err)
		}
		sample.Snapshot.Assets = make(map[movement.AssetKey]float64)
		result = append(result, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate official calendar-day samples: %w", err)
	}
	return result, nil
}

func (r *Repository) OfficialTickerMinuteMarkets(
	ctx context.Context,
	buckets []int64,
) (map[int64]map[movement.AssetKey]float64, error) {
	marketRows, err := r.OfficialTickerMinuteMarketRows(ctx, buckets)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]map[movement.AssetKey]float64, len(buckets))
	for bucketMS, values := range marketRows {
		result[bucketMS] = make(map[movement.AssetKey]float64, len(values))
		for key, row := range values {
			result[bucketMS][key] = row.VolumeKRW
		}
	}
	return result, nil
}

// OfficialTickerMinuteMarketRows loads normalized market volume and the
// optional exact Bithumb close in one bounded query. The legacy volume-only
// method above remains available for existing attribution consumers.
func (r *Repository) OfficialTickerMinuteMarketRows(
	ctx context.Context,
	buckets []int64,
) (map[int64]map[movement.AssetKey]tickertrend.PersistedMarketRow, error) {
	result := make(map[int64]map[movement.AssetKey]tickertrend.PersistedMarketRow, len(buckets))
	if len(buckets) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT bucket_ms, exchange_name, asset, volume_krw, close_price_krw
		FROM official_aligned_1h_markets
		WHERE bucket_ms = ANY($1)
		ORDER BY bucket_ms, exchange_name, asset`, buckets)
	if err != nil {
		return nil, fmt.Errorf("query official aligned 1h endpoint market rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucketMS int64
		var exchange, asset string
		var volume float64
		var closePrice *float64
		if err := rows.Scan(&bucketMS, &exchange, &asset, &volume, &closePrice); err != nil {
			return nil, fmt.Errorf("scan official aligned 1h endpoint market row: %w", err)
		}
		if result[bucketMS] == nil {
			result[bucketMS] = make(map[movement.AssetKey]tickertrend.PersistedMarketRow)
		}
		result[bucketMS][movement.AssetKey{Exchange: exchange, Asset: asset}] = tickertrend.PersistedMarketRow{
			VolumeKRW: volume, ClosePriceKRW: copyTickerTrendFloat(closePrice),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate official aligned 1h endpoint market rows: %w", err)
	}
	return result, nil
}

func (r *Repository) recentOfficialTickerSamples(
	ctx context.Context,
	sinceMS int64,
	limit int,
	tables officialTickerTables,
) ([]tickertrend.Sample, error) {
	if limit < 1 {
		return []tickertrend.Sample{}, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT
        bucket_ms, observed_at_ms, status, source, collection_span_ms,
        total_volume_krw, bithumb_volume_krw, bithumb_share_pct,
        exchanges_json, asset_row_count
    FROM `+tables.samples+`
    WHERE observed_at_ms >= $1
    ORDER BY bucket_ms DESC
    LIMIT $2`, sinceMS, limit)
	if err != nil {
		return nil, fmt.Errorf("query %s samples: %w", tables.label, err)
	}
	defer rows.Close()
	result := make([]tickertrend.Sample, 0, limit)
	for rows.Next() {
		var sample tickertrend.Sample
		var exchangesJSON []byte
		if err := rows.Scan(
			&sample.BucketMS, &sample.Snapshot.AsOfMS, &sample.Snapshot.Status,
			&sample.Snapshot.Source, &sample.Snapshot.CollectionSpanMS,
			&sample.Snapshot.TotalVolumeKRW, &sample.Snapshot.BithumbVolumeKRW,
			&sample.Snapshot.BithumbSharePct, &exchangesJSON, &sample.AssetRowCount,
		); err != nil {
			return nil, fmt.Errorf("scan %s sample: %w", tables.label, err)
		}
		if err := json.Unmarshal(exchangesJSON, &sample.Snapshot.Exchanges); err != nil {
			return nil, fmt.Errorf("decode %s exchanges: %w", tables.label, err)
		}
		sample.Snapshot.Assets = make(map[movement.AssetKey]float64)
		result = append(result, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s samples: %w", tables.label, err)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	if err := r.loadRecentOfficialTickerMarkets(ctx, result, tables); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) loadRecentOfficialTickerMarkets(
	ctx context.Context,
	samples []tickertrend.Sample,
	tables officialTickerTables,
) error {
	buckets := make([]int64, 0, 2)
	for index := len(samples) - 1; index >= 0 && len(buckets) < 2; index-- {
		if samples[index].AssetRowCount > 0 {
			buckets = append(buckets, samples[index].BucketMS)
		}
	}
	if len(buckets) == 0 {
		return nil
	}
	byBucket := make(map[int64]*tickertrend.Sample, len(buckets))
	for index := range samples {
		for _, bucket := range buckets {
			if samples[index].BucketMS == bucket {
				byBucket[bucket] = &samples[index]
			}
		}
	}
	rows, err := r.pool.Query(ctx, `SELECT bucket_ms, exchange_name, asset, volume_krw, close_price_krw
	        FROM `+tables.markets+`
        WHERE bucket_ms = ANY($1)
        ORDER BY bucket_ms, exchange_name, asset`, buckets)
	if err != nil {
		return fmt.Errorf("query %s markets: %w", tables.label, err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucketMS int64
		var exchange, asset string
		var volume float64
		var closePrice *float64
		if err := rows.Scan(&bucketMS, &exchange, &asset, &volume, &closePrice); err != nil {
			return fmt.Errorf("scan %s market: %w", tables.label, err)
		}
		sample := byBucket[bucketMS]
		if sample == nil {
			continue
		}
		sample.Snapshot.Assets[movement.AssetKey{Exchange: exchange, Asset: asset}] = volume
		if strings.EqualFold(exchange, "bithumb") && closePrice != nil {
			if sample.BithumbClosePricesKRW == nil {
				sample.BithumbClosePricesKRW = make(map[string]float64)
			}
			sample.BithumbClosePricesKRW[asset] = *closePrice
		}
		exchangeRow := sample.Snapshot.Exchanges[exchange]
		if exchangeRow.Assets == nil {
			exchangeRow.Assets = make(map[string]float64)
		}
		exchangeRow.Assets[asset] = volume
		sample.Snapshot.Exchanges[exchange] = exchangeRow
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s markets: %w", tables.label, err)
	}
	return nil
}

func copyTickerTrendFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func normalizedTickerTrendClosePrices(values map[string]float64) (map[string]float64, error) {
	result := make(map[string]float64, len(values))
	for rawAsset, value := range values {
		asset := strings.ToUpper(strings.TrimSpace(rawAsset))
		if asset == "" || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid close price for %q", rawAsset)
		}
		if previous, duplicate := result[asset]; duplicate && previous != value {
			return nil, fmt.Errorf("conflicting close prices for %s", asset)
		}
		result[asset] = value
	}
	return result, nil
}
