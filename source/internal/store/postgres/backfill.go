package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/backfill"
)

func (r *Repository) ApplyBackfill(ctx context.Context, result backfill.FetchResult, force bool) (backfill.WriteResult, error) {
	if result.Source == "" {
		result.Source = backfill.RawTradeSource
	}
	if result.SourceGranularitySeconds == 0 {
		result.SourceGranularitySeconds = 1
	}
	if result.NotionalMethod == "" {
		result.NotionalMethod = "EXACT_TRADE"
	}
	if err := backfill.ValidateFetchResult(result); err != nil {
		return backfill.WriteResult{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return backfill.WriteResult{}, fmt.Errorf("begin backfill: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !force {
		complete, err := backfillRunComplete(ctx, tx, result)
		if err != nil {
			return backfill.WriteResult{}, err
		}
		if complete {
			decision, err := persistExactRecoveryStatus(ctx, tx, result.Gap.ID)
			if err != nil {
				return backfill.WriteResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return backfill.WriteResult{}, fmt.Errorf("commit existing backfill recovery: %w", err)
			}
			return backfillWriteResult(result, "ALREADY_APPLIED", true, 0, 0, decision), nil
		}
	}
	trades := make([]backfill.HistoricalTrade, 0)
	for _, history := range result.Histories {
		trades = append(trades, history...)
	}
	buckets, err := backfill.AggregateTrades(trades)
	if err != nil {
		return backfill.WriteResult{}, err
	}
	successful := result.SuccessfulMarkets()
	if len(successful) > 0 {
		_, err = tx.Exec(ctx, `DELETE FROM volume_buckets
            WHERE exchange_name=$1 AND quote='KRW' AND second BETWEEN $2 AND $3
              AND market=ANY($4)`, result.Exchange, result.Gap.Start, result.Gap.End, successful)
		if err != nil {
			return backfill.WriteResult{}, fmt.Errorf("delete replaced backfill buckets: %w", err)
		}
	}
	batch := &pgx.Batch{}
	for _, row := range buckets {
		batch.Queue(`INSERT INTO volume_buckets (
            second, scope, exchange_name, market, base, quote,
            native_volume, krw_volume, trade_count, unresolved_count,
            buy_krw_volume, sell_krw_volume, base_volume,
            source_granularity_sec, source_kind, notional_method
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			row.Second, row.Scope, row.Exchange, row.Market, row.Base, row.Quote, row.NativeVolume, row.KRWVolume, row.TradeCount, row.UnresolvedCount, row.BuyKRWVolume, row.SellKRWVolume, row.BaseVolume, row.SourceGranularitySeconds, row.SourceKind, row.NotionalMethod)
	}
	results := tx.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		return backfill.WriteResult{}, fmt.Errorf("insert backfill buckets: %w", err)
	}
	detail, err := json.Marshal(backfill.Detail(result))
	if err != nil {
		return backfill.WriteResult{}, fmt.Errorf("encode backfill detail: %w", err)
	}
	completedAt := float64(time.Now().UTC().UnixNano()) / 1e9
	_, err = tx.Exec(ctx, `INSERT INTO historical_backfill_runs (
            gap_id, exchange_name, quote, source, start_second, end_second,
            status, market_count, successful_market_count, fetched_trade_count,
            bucket_count, request_count, completed_at, detail_json
        ) VALUES ($1,$2,'KRW',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
        ON CONFLICT (gap_id,exchange_name,quote,source) DO UPDATE SET
            start_second=EXCLUDED.start_second,end_second=EXCLUDED.end_second,
            status=EXCLUDED.status,market_count=EXCLUDED.market_count,
            successful_market_count=EXCLUDED.successful_market_count,
            fetched_trade_count=EXCLUDED.fetched_trade_count,
            bucket_count=EXCLUDED.bucket_count,request_count=EXCLUDED.request_count,
            completed_at=EXCLUDED.completed_at,detail_json=EXCLUDED.detail_json`,
		result.Gap.ID, result.Exchange, result.Source, result.Gap.Start, result.Gap.End, result.Status(), len(result.Markets), len(successful), len(trades), len(buckets), result.RequestCount, completedAt, string(detail))
	if err != nil {
		return backfill.WriteResult{}, fmt.Errorf("save backfill run: %w", err)
	}
	decision, err := persistExactRecoveryStatus(ctx, tx, result.Gap.ID)
	if err != nil {
		return backfill.WriteResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return backfill.WriteResult{}, fmt.Errorf("commit backfill: %w", err)
	}
	return backfillWriteResult(result, result.Status(), false, int64(len(trades)), int64(len(buckets)), decision), nil
}

func persistExactRecoveryStatus(ctx context.Context, tx pgx.Tx, gapID int64) (backfill.RecoveryDecision, error) {
	_, decision, err := recoveryDecisionForGap(ctx, tx, gapID)
	if err != nil {
		return decision, err
	}
	if !decision.Eligible {
		return decision, nil
	}
	command, err := tx.Exec(ctx, `UPDATE observation_gap_events SET status=$2 WHERE gap_id=$1 AND end_second IS NOT NULL`, gapID, backfill.OfficialReconciliationStatus)
	if err != nil {
		return decision, fmt.Errorf("mark exact backfill recovery: %w", err)
	}
	if command.RowsAffected() != 1 {
		return decision, fmt.Errorf("mark exact backfill recovery %d affected %d rows", gapID, command.RowsAffected())
	}
	return decision, nil
}

func backfillWriteResult(result backfill.FetchResult, status string, skipped bool, fetchedTrades, buckets int64, decision backfill.RecoveryDecision) backfill.WriteResult {
	successful := result.SuccessfulMarkets()
	recovered := []int64{}
	if decision.Eligible {
		recovered = append(recovered, result.Gap.ID)
	}
	return backfill.WriteResult{
		GapID: result.Gap.ID, Exchange: result.Exchange, Status: status, Skipped: skipped,
		MarketCount: int64(len(result.Markets)), SuccessfulMarketCount: int64(len(successful)),
		FetchedTradeCount: fetchedTrades, BucketCount: buckets, RequestCount: result.RequestCount,
		RecoveryStatus: decision.Status, RecoveredGapIDs: recovered,
		// ApplyBackfill is an offline PostgreSQL mutation. The running service
		// deliberately keeps its immutable in-memory buckets and coverage until
		// the next hydration, so this result must never claim live use.
		HistoricalUsed: false, RestartRequired: decision.Eligible || len(successful) > 0,
	}
}

func backfillRunComplete(ctx context.Context, tx pgx.Tx, result backfill.FetchResult) (bool, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM historical_backfill_runs WHERE gap_id=$1 AND exchange_name=$2 AND quote='KRW' AND source=$3`, result.Gap.ID, result.Exchange, result.Source).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check completed backfill: %w", err)
	}
	return status == "APPLIED_CURRENT_REGISTRY", nil
}
