package postgres

import (
	"context"
	"fmt"
)

// OfficialCandle10mEvidence describes persisted backfill rows without
// promoting base-only candles to KRW turnover evidence.
type OfficialCandle10mEvidence struct {
	Exchange       string `json:"exchange"`
	NotionalMethod string `json:"notional_method"`
	RowCount       int64  `json:"row_count"`
	ExactQuoteRows int64  `json:"exact_quote_rows"`
	FirstBucketMS  int64  `json:"first_bucket_ms"`
	LastBucketMS   int64  `json:"last_bucket_ms"`
}

// OfficialAligned1hEvidence describes stored exact rolling-24h endpoints and
// their per-exchange asset rows.
type OfficialAligned1hEvidence struct {
	Exchange      string `json:"exchange"`
	Source        string `json:"source"`
	EndpointCount int64  `json:"endpoint_count"`
	AssetRowCount int64  `json:"asset_row_count"`
	FirstBucketMS int64  `json:"first_bucket_ms"`
	LastBucketMS  int64  `json:"last_bucket_ms"`
}

func (r *Repository) OfficialAligned1hEvidenceInRange(
	ctx context.Context,
	startMS, endMS int64,
) ([]OfficialAligned1hEvidence, error) {
	if r == nil || r.pool == nil || startMS <= 0 || endMS < startMS {
		return []OfficialAligned1hEvidence{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT market.exchange_name, sample.source,
			count(DISTINCT sample.bucket_ms), count(*),
			min(sample.bucket_ms), max(sample.bucket_ms)
		FROM official_aligned_1h_samples AS sample
		JOIN official_aligned_1h_markets AS market USING(bucket_ms)
		WHERE sample.bucket_ms BETWEEN $1 AND $2
			AND sample.status = 'LIVE'
		GROUP BY market.exchange_name, sample.source
		ORDER BY market.exchange_name, sample.source`, startMS, endMS)
	if err != nil {
		return nil, fmt.Errorf("query official aligned-hour evidence: %w", err)
	}
	defer rows.Close()
	result := []OfficialAligned1hEvidence{}
	for rows.Next() {
		var item OfficialAligned1hEvidence
		if err := rows.Scan(
			&item.Exchange, &item.Source, &item.EndpointCount, &item.AssetRowCount,
			&item.FirstBucketMS, &item.LastBucketMS,
		); err != nil {
			return nil, fmt.Errorf("scan official aligned-hour evidence: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read official aligned-hour evidence: %w", err)
	}
	return result, nil
}

// OfficialCandle10mEvidenceInRange returns bounded inventory metadata for the
// requested period. It does not return raw candle rows or infer KRW turnover.
func (r *Repository) OfficialCandle10mEvidenceInRange(
	ctx context.Context,
	startMS, endMS int64,
) ([]OfficialCandle10mEvidence, error) {
	if r == nil || r.pool == nil || startMS <= 0 || endMS < startMS {
		return []OfficialCandle10mEvidence{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT exchange_name, notional_method, count(*),
			count(*) FILTER (WHERE quote_volume_krw IS NOT NULL),
			min(bucket_start) * 1000, max(bucket_start) * 1000
		FROM official_candle_10m_volumes
		WHERE bucket_start >= $1 AND bucket_start <= $2
		GROUP BY exchange_name, notional_method
		ORDER BY exchange_name, notional_method`, startMS/1_000, endMS/1_000)
	if err != nil {
		return nil, fmt.Errorf("query official 10-minute candle evidence: %w", err)
	}
	defer rows.Close()
	result := []OfficialCandle10mEvidence{}
	for rows.Next() {
		var item OfficialCandle10mEvidence
		if err := rows.Scan(
			&item.Exchange,
			&item.NotionalMethod,
			&item.RowCount,
			&item.ExactQuoteRows,
			&item.FirstBucketMS,
			&item.LastBucketMS,
		); err != nil {
			return nil, fmt.Errorf("scan official 10-minute candle evidence: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read official 10-minute candle evidence: %w", err)
	}
	return result, nil
}
