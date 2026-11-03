package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
)

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const wsMinuteColumns = `exchange_name, market, base, quote, minute_start,
    open_price, high_price, low_price, close_price, base_volume, quote_volume,
    krw_volume, trade_count, buy_krw_volume, sell_krw_volume,
    first_trade_ts_ms, last_trade_ts_ms, unresolved_trade_count,
    coverage_status, gap_seconds, source_kind, detail_status, created_at`

const officialObservationColumns = `exchange_name, market, base, quote, minute_start,
    observation_attempt, open_price, high_price, low_price, close_price,
    base_volume, quote_volume, quote_volume_exact, source_kind, notional_method,
    fetched_at, created_at`

func (r *Repository) SaveWSMinute(ctx context.Context, value reconcile.WSTradeMinute) (bool, error) {
	return saveWSMinute(ctx, r.pool, value)
}

func saveWSMinute(ctx context.Context, query ingestionQueryExecutor, value reconcile.WSTradeMinute) (bool, error) {
	if err := value.Normalize(); err != nil {
		return false, err
	}
	tag, err := query.Exec(ctx, `
        INSERT INTO ws_trade_minutes (`+wsMinuteColumns+`)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
        ON CONFLICT (exchange_name, market, minute_start) DO NOTHING`,
		value.Exchange, value.Market, value.Base, value.Quote, value.MinuteStart,
		value.Open, value.High, value.Low, value.Close, value.BaseVolume, value.QuoteVolume,
		value.KRWVolume, value.TradeCount, value.BuyKRWVolume, value.SellKRWVolume,
		value.FirstTradeTS, value.LastTradeTS, value.UnresolvedTradeCount,
		value.CoverageStatus, value.GapSeconds, value.SourceKind, value.DetailStatus, value.CreatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("save WebSocket minute: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	existing, err := getWSMinute(ctx, query, value.Exchange, value.Market, value.MinuteStart)
	if err != nil {
		return false, err
	}
	if existing != nil && sameWSMinute(*existing, value) {
		return false, nil
	}
	return false, &reconcile.ImmutableMinuteConflict{Message: fmt.Sprintf(
		"ws_trade_minutes already contains different evidence for %s %s %d",
		value.Exchange, value.Market, value.MinuteStart,
	)}
}

func (r *Repository) GetWSMinute(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.WSTradeMinute, error) {
	return getWSMinute(ctx, r.pool, exchange, market, minute)
}

func getWSMinute(ctx context.Context, query rowQuerier, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.WSTradeMinute, error) {
	if int64(minute)%reconcile.MinuteSeconds != 0 {
		return nil, errors.New("minute start must be aligned to a 60-second boundary")
	}
	row := reconcile.WSTradeMinute{}
	var first, last *int64
	err := query.QueryRow(ctx, `SELECT `+wsMinuteColumns+` FROM ws_trade_minutes
        WHERE exchange_name=$1 AND market=$2 AND minute_start=$3`, exchange, market, minute).Scan(
		&row.Exchange, &row.Market, &row.Base, &row.Quote, &row.MinuteStart,
		&row.Open, &row.High, &row.Low, &row.Close, &row.BaseVolume, &row.QuoteVolume,
		&row.KRWVolume, &row.TradeCount, &row.BuyKRWVolume, &row.SellKRWVolume,
		&first, &last, &row.UnresolvedTradeCount, &row.CoverageStatus, &row.GapSeconds,
		&row.SourceKind, &row.DetailStatus, &row.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get WebSocket minute: %w", err)
	}
	row.FirstTradeTS = unixMilli(first)
	row.LastTradeTS = unixMilli(last)
	return &row, nil
}

func (r *Repository) SaveOfficialCandle(ctx context.Context, value reconcile.OfficialMinuteCandle) (bool, error) {
	if err := value.Normalize(); err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin official candle transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
        INSERT INTO official_minute_candle_observations (`+officialObservationColumns+`)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
        ON CONFLICT (exchange_name, market, minute_start, observation_attempt) DO NOTHING`,
		value.Exchange, value.Market, value.Base, value.Quote, value.MinuteStart, value.ObservationAttempt,
		value.Open, value.High, value.Low, value.Close, value.BaseVolume, value.QuoteVolume,
		value.QuoteVolumeExact, value.SourceKind, value.NotionalMethod, value.FetchedAt, value.CreatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("save official candle observation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, getErr := getOfficialObservation(ctx, tx, value.Exchange, value.Market, value.MinuteStart, value.ObservationAttempt)
		if getErr != nil {
			return false, getErr
		}
		if existing != nil && sameOfficialCandle(*existing, value) {
			return false, nil
		}
		return false, &reconcile.ImmutableMinuteConflict{Message: fmt.Sprintf(
			"official candle observation already contains different evidence for %s %s %d attempt %d",
			value.Exchange, value.Market, value.MinuteStart, value.ObservationAttempt,
		)}
	}
	first, err := getFirstOfficialCandle(ctx, tx, value.Exchange, value.Market, value.MinuteStart)
	if err != nil {
		return false, err
	}
	if first == nil {
		_, err = tx.Exec(ctx, `
            INSERT INTO official_minute_candles (
                exchange_name, market, base, quote, minute_start,
                open_price, high_price, low_price, close_price, base_volume,
                quote_volume, quote_volume_exact, source_kind, notional_method,
                fetched_at, created_at
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			value.Exchange, value.Market, value.Base, value.Quote, value.MinuteStart,
			value.Open, value.High, value.Low, value.Close, value.BaseVolume,
			value.QuoteVolume, value.QuoteVolumeExact, value.SourceKind, value.NotionalMethod,
			value.FetchedAt, value.CreatedAt,
		)
		if err != nil {
			return false, fmt.Errorf("save first official candle: %w", err)
		}
	} else if first.Base != value.Base || first.Quote != value.Quote {
		return false, &reconcile.ImmutableMinuteConflict{Message: "official candle observation identity differs from the first evidence"}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit official candle: %w", err)
	}
	return true, nil
}

func (r *Repository) GetFirstOfficialCandle(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.OfficialMinuteCandle, error) {
	return getFirstOfficialCandle(ctx, r.pool, exchange, market, minute)
}

func getFirstOfficialCandle(ctx context.Context, query rowQuerier, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.OfficialMinuteCandle, error) {
	row := reconcile.OfficialMinuteCandle{}
	err := query.QueryRow(ctx, `SELECT exchange_name, market, base, quote, minute_start, 0,
        open_price, high_price, low_price, close_price, base_volume, quote_volume,
        quote_volume_exact, source_kind, notional_method, fetched_at, created_at
        FROM official_minute_candles WHERE exchange_name=$1 AND market=$2 AND minute_start=$3`,
		exchange, market, minute).Scan(officialScanTargets(&row)...)
	return officialScanResult(row, err)
}

func (r *Repository) GetOfficialObservation(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond, attempt int) (*reconcile.OfficialMinuteCandle, error) {
	return getOfficialObservation(ctx, r.pool, exchange, market, minute, attempt)
}

func getOfficialObservation(ctx context.Context, query rowQuerier, exchange domain.Exchange, market string, minute domain.UnixSecond, attempt int) (*reconcile.OfficialMinuteCandle, error) {
	row := reconcile.OfficialMinuteCandle{}
	err := query.QueryRow(ctx, `SELECT `+officialObservationColumns+`
        FROM official_minute_candle_observations
        WHERE exchange_name=$1 AND market=$2 AND minute_start=$3 AND observation_attempt=$4`,
		exchange, market, minute, attempt).Scan(officialScanTargets(&row)...)
	return officialScanResult(row, err)
}

func (r *Repository) GetLatestOfficialCandle(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.OfficialMinuteCandle, error) {
	row := reconcile.OfficialMinuteCandle{}
	err := r.pool.QueryRow(ctx, `SELECT `+officialObservationColumns+`
        FROM official_minute_candle_observations
        WHERE exchange_name=$1 AND market=$2 AND minute_start=$3
        ORDER BY observation_attempt DESC LIMIT 1`, exchange, market, minute).Scan(officialScanTargets(&row)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.GetFirstOfficialCandle(ctx, exchange, market, minute)
	}
	return officialScanResult(row, err)
}

func (r *Repository) OfficialCandleObservations(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond) ([]reconcile.OfficialMinuteCandle, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+officialObservationColumns+`
        FROM official_minute_candle_observations
        WHERE exchange_name=$1 AND market=$2 AND minute_start=$3
        ORDER BY observation_attempt`, exchange, market, minute)
	if err != nil {
		return nil, fmt.Errorf("query official candle observations: %w", err)
	}
	defer rows.Close()
	result := make([]reconcile.OfficialMinuteCandle, 0)
	for rows.Next() {
		row := reconcile.OfficialMinuteCandle{}
		if err := rows.Scan(officialScanTargets(&row)...); err != nil {
			return nil, fmt.Errorf("scan official candle observation: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func officialScanTargets(row *reconcile.OfficialMinuteCandle) []any {
	return []any{&row.Exchange, &row.Market, &row.Base, &row.Quote, &row.MinuteStart,
		&row.ObservationAttempt, &row.Open, &row.High, &row.Low, &row.Close,
		&row.BaseVolume, &row.QuoteVolume, &row.QuoteVolumeExact, &row.SourceKind,
		&row.NotionalMethod, &row.FetchedAt, &row.CreatedAt}
}

func officialScanResult(row reconcile.OfficialMinuteCandle, err error) (*reconcile.OfficialMinuteCandle, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get official candle: %w", err)
	}
	return &row, nil
}

func sameWSMinute(left, right reconcile.WSTradeMinute) bool {
	left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func sameOfficialCandle(left, right reconcile.OfficialMinuteCandle) bool {
	// Optional raw parser strings are not persisted on ordinary observations.
	// Proved precision evidence remains immutable inside NotionalMethod.
	left.DecimalEvidence, right.DecimalEvidence = nil, nil
	left.FetchedAt, right.FetchedAt = time.Time{}, time.Time{}
	left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(left, right)
}

func unixMilli(value *int64) *domain.UnixMilli {
	if value == nil {
		return nil
	}
	result := domain.UnixMilli(*value)
	return &result
}

func (r *Repository) ReconcileMinute(
	ctx context.Context,
	exchange domain.Exchange,
	market string,
	minute domain.UnixSecond,
	knownGapSeconds int64,
	checkedAt time.Time,
) (reconcile.Result, error) {
	if int64(minute)%reconcile.MinuteSeconds != 0 {
		return reconcile.Result{}, errors.New("minute start must be aligned to a 60-second boundary")
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("begin reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ws, err := getWSMinute(ctx, tx, exchange, market, minute)
	if err != nil {
		return reconcile.Result{}, err
	}
	officialCandle, err := getLatestOfficialCandle(ctx, tx, exchange, market, minute)
	if err != nil {
		return reconcile.Result{}, err
	}
	if ws == nil && officialCandle == nil {
		return reconcile.Result{}, errors.New("no WebSocket minute or official candle exists for reconciliation")
	}
	seal, err := getMinuteSeal(ctx, tx, exchange, minute)
	if err != nil {
		return reconcile.Result{}, err
	}
	observedZero := ws == nil && seal != nil && seal.Status != reconcile.SealMissing && knownGapSeconds == 0
	decision := reconcile.DecideWithObservedZero(ws, officialCandle, knownGapSeconds, observedZero)
	result := buildReconciliation(ws, officialCandle, decision, knownGapSeconds, checkedAt)
	if err := saveReconciliation(ctx, tx, result); err != nil {
		return reconcile.Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return reconcile.Result{}, fmt.Errorf("commit reconciliation: %w", err)
	}
	return result, nil
}

// FinalizeExchangeMinute rebuilds the exchange-level row once after a worker
// finishes a batch of market reconciliations, then returns the latest global
// view. Keeping this scan out of ReconcileMinute avoids rescanning 1..N rows
// after each of N markets while preserving the same persisted decisions.
func (r *Repository) FinalizeExchangeMinute(
	ctx context.Context,
	exchange domain.Exchange,
	minute domain.UnixSecond,
	expectedMarketCount int64,
	checkedAt time.Time,
) (reconcile.Summary, error) {
	if int64(minute)%reconcile.MinuteSeconds != 0 {
		return reconcile.Summary{}, errors.New("minute start must be aligned to a 60-second boundary")
	}
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return reconcile.Summary{}, fmt.Errorf("begin exchange minute finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := refreshExchangeMinute(ctx, tx, exchange, minute, expectedMarketCount, checkedAt); err != nil {
		return reconcile.Summary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return reconcile.Summary{}, fmt.Errorf("commit exchange minute finalization: %w", err)
	}
	return r.LatestReconciliationSummary(ctx)
}

func getLatestOfficialCandle(ctx context.Context, query rowQuerier, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.OfficialMinuteCandle, error) {
	row := reconcile.OfficialMinuteCandle{}
	err := query.QueryRow(ctx, `SELECT `+officialObservationColumns+`
        FROM official_minute_candle_observations
        WHERE exchange_name=$1 AND market=$2 AND minute_start=$3
        ORDER BY observation_attempt DESC LIMIT 1`, exchange, market, minute).Scan(officialScanTargets(&row)...)
	if !errors.Is(err, pgx.ErrNoRows) {
		return officialScanResult(row, err)
	}
	return getFirstOfficialCandle(ctx, query, exchange, market, minute)
}

func buildReconciliation(ws *reconcile.WSTradeMinute, candle *reconcile.OfficialMinuteCandle, decision reconcile.Decision, knownGap int64, checkedAt time.Time) reconcile.Result {
	gap := knownGap
	if gap < 0 {
		gap = 0
	}
	if gap > reconcile.MinuteSeconds {
		gap = reconcile.MinuteSeconds
	}
	var identityExchange domain.Exchange
	var identityMarket, base, quote string
	var minute domain.UnixSecond
	if ws != nil {
		identityExchange, identityMarket, base, quote, minute = ws.Exchange, ws.Market, ws.Base, ws.Quote, ws.MinuteStart
		if ws.GapSeconds > gap {
			gap = ws.GapSeconds
		}
	} else {
		identityExchange, identityMarket, base, quote, minute = candle.Exchange, candle.Market, candle.Base, candle.Quote, candle.MinuteStart
	}
	result := reconcile.Result{
		Exchange: identityExchange, Market: identityMarket, Base: base, Quote: quote, MinuteStart: minute,
		Status: decision.Status, Reason: decision.Reason, WSPresent: ws != nil, OfficialPresent: candle != nil,
		KnownGapSeconds: gap, BaseVolumeMatches: decision.BaseVolumeMatches,
		QuoteVolumeMatches: decision.QuoteVolumeMatches, OHLCMatches: decision.OHLCMatches,
		BaseVolumeDelta: decision.BaseVolumeDelta, QuoteVolumeDelta: decision.QuoteVolumeDelta,
		EffectiveBaseVolume: decision.EffectiveBaseVolume, EffectiveQuoteVolume: decision.EffectiveQuoteVolume,
		EffectiveKRWVolume: decision.EffectiveKRWVolume, EffectiveTradeCount: decision.EffectiveTradeCount,
		EffectiveBuyKRWVolume:  decision.EffectiveBuyKRWVolume,
		EffectiveSellKRWVolume: decision.EffectiveSellKRWVolume,
		EffectiveSource:        decision.EffectiveSource, TradeDetailStatus: decision.TradeDetailStatus,
		CheckedAt: checkedAt,
	}
	if ws != nil {
		result.WSBaseVolume = float64Pointer(ws.BaseVolume)
		result.WSQuoteVolume = float64Pointer(ws.QuoteVolume)
		result.WSTradeCount = int64PointerLocal(ws.TradeCount)
	}
	if candle != nil {
		result.OfficialBaseVolume = float64Pointer(candle.BaseVolume)
		result.OfficialQuoteVolume = cloneFloat64(candle.QuoteVolume)
	}
	return result
}

func saveReconciliation(ctx context.Context, tx pgx.Tx, row reconcile.Result) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO minute_reconciliations (
            exchange_name, market, base, quote, minute_start, status, reason,
            ws_present, official_present, known_gap_seconds, base_volume_matches,
            quote_volume_matches, ohlc_matches, ws_base_volume, ws_quote_volume,
            ws_trade_count, official_base_volume, official_quote_volume,
            base_volume_delta, quote_volume_delta, effective_base_volume,
            effective_quote_volume, effective_krw_volume, effective_trade_count,
            effective_buy_krw_volume, effective_sell_krw_volume,
            effective_source, trade_detail_status, checked_at
        ) VALUES (
            $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
            $16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29
        ) ON CONFLICT (exchange_name, market, minute_start) DO UPDATE SET
            base=EXCLUDED.base, quote=EXCLUDED.quote, status=EXCLUDED.status,
            reason=EXCLUDED.reason, ws_present=EXCLUDED.ws_present,
            official_present=EXCLUDED.official_present,
            known_gap_seconds=EXCLUDED.known_gap_seconds,
            base_volume_matches=EXCLUDED.base_volume_matches,
            quote_volume_matches=EXCLUDED.quote_volume_matches,
            ohlc_matches=EXCLUDED.ohlc_matches,
            ws_base_volume=EXCLUDED.ws_base_volume,
            ws_quote_volume=EXCLUDED.ws_quote_volume,
            ws_trade_count=EXCLUDED.ws_trade_count,
            official_base_volume=EXCLUDED.official_base_volume,
            official_quote_volume=EXCLUDED.official_quote_volume,
            base_volume_delta=EXCLUDED.base_volume_delta,
            quote_volume_delta=EXCLUDED.quote_volume_delta,
            effective_base_volume=EXCLUDED.effective_base_volume,
            effective_quote_volume=EXCLUDED.effective_quote_volume,
            effective_krw_volume=EXCLUDED.effective_krw_volume,
            effective_trade_count=EXCLUDED.effective_trade_count,
            effective_buy_krw_volume=EXCLUDED.effective_buy_krw_volume,
            effective_sell_krw_volume=EXCLUDED.effective_sell_krw_volume,
            effective_source=EXCLUDED.effective_source,
            trade_detail_status=EXCLUDED.trade_detail_status,
            checked_at=EXCLUDED.checked_at
        WHERE minute_reconciliations.status <> 'REPLACED_OFFICIAL_MINUTE'
           OR EXCLUDED.status = 'REPLACED_OFFICIAL_MINUTE'`,
		row.Exchange, row.Market, row.Base, row.Quote, row.MinuteStart, row.Status, row.Reason,
		row.WSPresent, row.OfficialPresent, row.KnownGapSeconds, row.BaseVolumeMatches,
		row.QuoteVolumeMatches, row.OHLCMatches, row.WSBaseVolume, row.WSQuoteVolume,
		row.WSTradeCount, row.OfficialBaseVolume, row.OfficialQuoteVolume,
		row.BaseVolumeDelta, row.QuoteVolumeDelta, row.EffectiveBaseVolume,
		row.EffectiveQuoteVolume, row.EffectiveKRWVolume, row.EffectiveTradeCount,
		row.EffectiveBuyKRWVolume, row.EffectiveSellKRWVolume, row.EffectiveSource,
		row.TradeDetailStatus, row.CheckedAt,
	)
	if err != nil {
		return fmt.Errorf("save minute reconciliation: %w", err)
	}
	return nil
}

func refreshExchangeMinute(ctx context.Context, tx pgx.Tx, exchange domain.Exchange, minute domain.UnixSecond, expectedMarketCount int64, updatedAt time.Time) (reconcile.ExchangeSummary, error) {
	rows, err := tx.Query(ctx, `
        SELECT status, ws_present, official_present, ws_quote_volume,
               official_quote_volume, effective_quote_volume, trade_detail_status
        FROM minute_reconciliations
        WHERE exchange_name=$1 AND minute_start=$2`, exchange, minute)
	if err != nil {
		return reconcile.ExchangeSummary{}, fmt.Errorf("query exchange reconciliation rows: %w", err)
	}
	defer rows.Close()
	summary := reconcile.ExchangeSummary{Exchange: exchange, MinuteStart: minute, UpdatedAt: updatedAt}
	counts := make(map[string]int64)
	var officialTotal, effectiveTotal float64
	var officialSeen, effectiveSeen bool
	for rows.Next() {
		var status, detail string
		var wsPresent, officialPresent bool
		var wsQuote, officialQuote, effectiveQuote *float64
		if err := rows.Scan(&status, &wsPresent, &officialPresent, &wsQuote, &officialQuote, &effectiveQuote, &detail); err != nil {
			return reconcile.ExchangeSummary{}, fmt.Errorf("scan exchange reconciliation row: %w", err)
		}
		summary.MarketCount++
		if wsPresent {
			summary.WSMarketCount++
		}
		if officialPresent {
			summary.OfficialMarketCount++
		}
		if detail == reconcile.DetailUnknown {
			summary.UnknownDetailCount++
		}
		counts[status]++
		if wsQuote != nil {
			summary.WSQuoteVolume += *wsQuote
		}
		if officialQuote != nil {
			officialSeen = true
			officialTotal += *officialQuote
		}
		if effectiveQuote != nil {
			effectiveSeen = true
			effectiveTotal += *effectiveQuote
		}
	}
	if err := rows.Err(); err != nil {
		return reconcile.ExchangeSummary{}, err
	}
	if summary.MarketCount == 0 {
		return summary, errors.New("no reconciled markets exist for exchange minute")
	}
	summary.Status = reconcile.StatusForCompleteness(counts, summary.MarketCount, expectedMarketCount)
	summary.VerifiedCount = counts[reconcile.StatusVerifiedWS]
	summary.RecoveredCount = counts[reconcile.StatusRecoveredCandle] + counts[reconcile.StatusReplacedCandle]
	summary.MismatchCount = counts[reconcile.StatusMismatch]
	summary.WSMissingCount = counts[reconcile.StatusWSMissing]
	summary.BaseOnlyCount = counts[reconcile.StatusCandleBaseOnly]
	summary.CandleUnavailableCount = counts[reconcile.StatusCandleUnavailable]
	if officialSeen {
		summary.OfficialQuoteVolume = float64Pointer(officialTotal)
	}
	if effectiveSeen {
		summary.EffectiveQuoteVolume = float64Pointer(effectiveTotal)
	}
	if err := saveExchangeSummary(ctx, tx, summary); err != nil {
		return reconcile.ExchangeSummary{}, err
	}
	return summary, nil
}

func saveExchangeSummary(ctx context.Context, tx pgx.Tx, row reconcile.ExchangeSummary) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO exchange_minute_reconciliations (
            exchange_name, minute_start, status, market_count, ws_market_count,
            official_market_count, verified_count, recovered_count, mismatch_count,
            ws_missing_count, base_only_count, candle_unavailable_count,
            unknown_detail_count, ws_quote_volume, official_quote_volume,
            effective_quote_volume, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
        ON CONFLICT (exchange_name, minute_start) DO UPDATE SET
            status=EXCLUDED.status, market_count=EXCLUDED.market_count,
            ws_market_count=EXCLUDED.ws_market_count,
            official_market_count=EXCLUDED.official_market_count,
            verified_count=EXCLUDED.verified_count,
            recovered_count=EXCLUDED.recovered_count,
            mismatch_count=EXCLUDED.mismatch_count,
            ws_missing_count=EXCLUDED.ws_missing_count,
            base_only_count=EXCLUDED.base_only_count,
            candle_unavailable_count=EXCLUDED.candle_unavailable_count,
            unknown_detail_count=EXCLUDED.unknown_detail_count,
            ws_quote_volume=EXCLUDED.ws_quote_volume,
            official_quote_volume=EXCLUDED.official_quote_volume,
            effective_quote_volume=EXCLUDED.effective_quote_volume,
            updated_at=EXCLUDED.updated_at`,
		row.Exchange, row.MinuteStart, row.Status, row.MarketCount, row.WSMarketCount,
		row.OfficialMarketCount, row.VerifiedCount, row.RecoveredCount, row.MismatchCount,
		row.WSMissingCount, row.BaseOnlyCount, row.CandleUnavailableCount,
		row.UnknownDetailCount, row.WSQuoteVolume, row.OfficialQuoteVolume,
		row.EffectiveQuoteVolume, row.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("save exchange minute summary: %w", err)
	}
	return nil
}

func (r *Repository) LatestReconciliationSummary(ctx context.Context) (reconcile.Summary, error) {
	result := reconcile.Summary{Status: reconcile.StatusCandleUnavailable, Exchanges: make(map[string]reconcile.ExchangeSummary), MinuteSeals: make(map[string]reconcile.MinuteSeal)}
	sealMinute, seals, err := r.LatestMinuteSeals(ctx)
	if err != nil {
		return result, err
	}
	result.SealMinuteStart, result.MinuteSeals = sealMinute, seals
	domestic := make([]string, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		domestic = append(domestic, string(exchange))
	}
	var latest *int64
	if err := r.pool.QueryRow(ctx, `
        SELECT MAX(minute_start)
        FROM exchange_minute_reconciliations
        WHERE exchange_name = ANY($1)`, domestic).Scan(&latest); err != nil {
		return result, fmt.Errorf("get latest reconciliation minute: %w", err)
	}
	if latest == nil {
		return result, nil
	}
	minute := domain.UnixSecond(*latest)
	result.MinuteStart = &minute
	rows, err := r.pool.Query(ctx, `
        SELECT exchange_name, minute_start, status, market_count, ws_market_count,
               official_market_count, verified_count, recovered_count, mismatch_count,
               ws_missing_count, base_only_count, candle_unavailable_count,
               unknown_detail_count, ws_quote_volume, official_quote_volume,
		       effective_quote_volume, updated_at
		FROM exchange_minute_reconciliations
		WHERE minute_start=$1 AND exchange_name = ANY($2)
		ORDER BY exchange_name`, minute, domestic)
	if err != nil {
		return result, fmt.Errorf("query latest reconciliation summary: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int64)
	var effective float64
	var effectiveSeen bool
	for rows.Next() {
		row := reconcile.ExchangeSummary{}
		if err := rows.Scan(
			&row.Exchange, &row.MinuteStart, &row.Status, &row.MarketCount, &row.WSMarketCount,
			&row.OfficialMarketCount, &row.VerifiedCount, &row.RecoveredCount, &row.MismatchCount,
			&row.WSMissingCount, &row.BaseOnlyCount, &row.CandleUnavailableCount,
			&row.UnknownDetailCount, &row.WSQuoteVolume, &row.OfficialQuoteVolume,
			&row.EffectiveQuoteVolume, &row.UpdatedAt,
		); err != nil {
			return result, fmt.Errorf("scan latest reconciliation summary: %w", err)
		}
		result.Exchanges[string(row.Exchange)] = row
		result.ExchangeCount++
		result.MarketCount += row.MarketCount
		result.VerifiedCount += row.VerifiedCount
		result.RecoveredCount += row.RecoveredCount
		result.MismatchCount += row.MismatchCount
		result.WSMissingCount += row.WSMissingCount
		result.BaseOnlyCount += row.BaseOnlyCount
		result.CandleUnavailableCount += row.CandleUnavailableCount
		result.UnknownDetailCount += row.UnknownDetailCount
		counts[row.Status]++
		if row.EffectiveQuoteVolume != nil {
			effectiveSeen = true
			effective += *row.EffectiveQuoteVolume
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.Status = reconcile.StatusForCompleteness(counts, result.ExchangeCount, int64(len(domain.DomesticExchanges)))
	if effectiveSeen {
		result.EffectiveQuoteVolume = float64Pointer(effective)
	}
	return result, nil
}

// RecoveredMinuteSupplements returns only exact KRW quote-volume candle
// evidence. The saved residual is informational: analysis rebases against its
// atomic current observation, including sub-won or previously zero residuals.
// Base-only candles (Korbit/GOPAX) and non-KRW quote markets are
// intentionally excluded: close*base is not an exact traded notional.
func (r *Repository) RecoveredMinuteSupplements(
	ctx context.Context,
	fromMinute, toMinute domain.UnixSecond,
) ([]reconcile.MinuteSupplement, error) {
	if int64(fromMinute)%reconcile.MinuteSeconds != 0 || int64(toMinute)%reconcile.MinuteSeconds != 0 {
		return nil, errors.New("minute supplement bounds must be aligned to 60-second boundaries")
	}
	if toMinute < fromMinute {
		return nil, errors.New("minute supplement end must not precede start")
	}
	rows, err := r.pool.Query(ctx, `
        SELECT r.exchange_name, r.market, r.base, r.quote, r.minute_start,
               known_gap_seconds, COALESCE(ws_quote_volume, 0),
               official_quote_volume,
               official_quote_volume - COALESCE(ws_quote_volume, 0),
               CASE WHEN r.status=$4 THEN COALESCE(o.source_kind,'MISSING_SOURCE_PRECISION_EVIDENCE') ELSE effective_source END,
               CASE WHEN r.status=$4 THEN COALESCE(o.notional_method,'MISSING_SOURCE_PRECISION_EVIDENCE') ELSE '' END,
               o.base, o.quote, o.base_volume, o.quote_volume, o.quote_volume_exact,
               o.open_price, o.high_price, o.low_price, o.close_price,
               r.status=$5, o.source_kind, o.notional_method
        FROM minute_reconciliations r
        LEFT JOIN LATERAL (
          SELECT base, quote, base_volume, quote_volume, quote_volume_exact,
                 open_price, high_price, low_price, close_price, source_kind, notional_method
          FROM official_minute_candle_observations o
          WHERE r.status IN ($4,$5) AND o.exchange_name=r.exchange_name AND o.market=r.market AND o.minute_start=r.minute_start
          ORDER BY o.observation_attempt DESC LIMIT 1
        ) o ON true
        WHERE r.minute_start BETWEEN $1 AND $2
          AND r.status IN ($3,$4,$5)
          AND UPPER(r.quote) = 'KRW'
          AND r.official_quote_volume IS NOT NULL
          AND r.effective_krw_volume IS NOT NULL
        ORDER BY r.minute_start, r.exchange_name, r.market`,
		fromMinute, toMinute, reconcile.StatusRecoveredCandle, reconcile.StatusSourcePrecisionDifference, reconcile.StatusReplacedCandle,
	)
	if err != nil {
		return nil, fmt.Errorf("query recovered minute supplements: %w", err)
	}
	defer rows.Close()
	result := make([]reconcile.MinuteSupplement, 0)
	for rows.Next() {
		row := reconcile.MinuteSupplement{}
		var base, quote *string
		var baseVolume *float64
		var quoteExact *bool
		var originalSource, originalMethod *string
		candle := reconcile.OfficialMinuteCandle{}
		if err := rows.Scan(
			&row.Exchange, &row.Market, &row.Base, &row.Quote, &row.MinuteStart,
			&row.KnownGapSeconds, &row.WSVolumeKRW, &row.OfficialVolumeKRW,
			&row.SupplementalVolumeKRW, &row.Source, &row.NotionalMethod,
			&base, &quote, &baseVolume, &candle.QuoteVolume, &quoteExact,
			&candle.Open, &candle.High, &candle.Low, &candle.Close,
			&row.ReplaceWholeMinute, &originalSource, &originalMethod,
		); err != nil {
			return nil, fmt.Errorf("scan recovered minute supplement: %w", err)
		}
		row.IntervalSec = reconcile.MinuteSeconds
		if row.ReplaceWholeMinute {
			// The durable selection is usable only with its immutable official
			// market-minute evidence, never the derived reconciliation alone.
			if base != nil {
				candle.Base = *base
			}
			if quote != nil {
				candle.Quote = *quote
			}
			if baseVolume != nil {
				candle.BaseVolume = *baseVolume
			}
			if quoteExact != nil {
				candle.QuoteVolumeExact = *quoteExact
			}
			if originalSource != nil {
				candle.SourceKind = *originalSource
			}
			if originalMethod != nil {
				candle.NotionalMethod = *originalMethod
			}
			candle.Exchange, candle.Market, candle.MinuteStart = row.Exchange, row.Market, row.MinuteStart
			_, precision := reconcile.CoinonePrecisionEvidence(candle)
			if base == nil || *base != row.Base || quote == nil || *quote != "KRW" || baseVolume == nil ||
				quoteExact == nil || !*quoteExact || candle.QuoteVolume == nil || *candle.QuoteVolume != row.OfficialVolumeKRW ||
				originalSource == nil || *originalSource != row.Source || originalMethod == nil || (*originalMethod != "EXACT_CANDLE_QUOTE_VOLUME" && !precision) || candle.Normalize() != nil {
				row.NotionalMethod = "INVALID_WHOLE_MINUTE_EVIDENCE"
			}
		}
		if row.NotionalMethod != "" {
			candle.Exchange, candle.Market, candle.MinuteStart = row.Exchange, row.Market, row.MinuteStart
			candle.SourceKind, candle.NotionalMethod = row.Source, row.NotionalMethod
			if base != nil {
				candle.Base = *base
			}
			if quote != nil {
				candle.Quote = *quote
			}
			if baseVolume != nil {
				candle.BaseVolume = *baseVolume
			}
			if quoteExact != nil {
				candle.QuoteVolumeExact = *quoteExact
			}
			bindPrecisionSupplementEvidence(&row, candle)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recovered minute supplements: %w", err)
	}
	return result, nil
}

// A restart must bind the immutable envelope to the latest observation's
// numeric columns as well as the reconciliation identity. Preserve an invalid
// row as a conflict marker rather than silently dropping it or using it as a
// normal positive candle supplement.
func bindPrecisionSupplementEvidence(row *reconcile.MinuteSupplement, candle reconcile.OfficialMinuteCandle) {
	_, valid := reconcile.CoinonePrecisionEvidence(candle)
	if !valid || candle.Exchange != row.Exchange || candle.Market != row.Market || candle.MinuteStart != row.MinuteStart || candle.Base != row.Base || candle.Quote != row.Quote || candle.SourceKind != row.Source || candle.NotionalMethod != row.NotionalMethod || candle.QuoteVolume == nil || *candle.QuoteVolume != row.OfficialVolumeKRW {
		row.NotionalMethod = "INVALID_SOURCE_PRECISION_EVIDENCE"
	}
}

// RecoveredTenMinuteSupplements groups exact recovered KRW minute residuals on
// aligned 10-minute boundaries. Only minutes whose transport gap was confirmed
// and recovered from an exact official quote-volume candle participate. This
// deliberately does not require observations for quiet/non-target minutes: the
// absence of those rows is not evidence that the recovered gap minute is
// unusable. Kept for repository compatibility; live analysis must not use this
// coarse block because it cannot prove any clipped minute's exact amount.
func (r *Repository) RecoveredTenMinuteSupplements(
	ctx context.Context,
	fromBucket, toBucket domain.UnixSecond,
) ([]reconcile.MinuteSupplement, error) {
	const intervalSec = int64(600)
	if int64(fromBucket)%intervalSec != 0 || int64(toBucket)%intervalSec != 0 {
		return nil, errors.New("ten-minute supplement bounds must be aligned to 600-second boundaries")
	}
	if toBucket < fromBucket {
		return nil, errors.New("ten-minute supplement end must not precede start")
	}
	rows, err := r.pool.Query(ctx, `
        SELECT exchange_name, market, MAX(base), MAX(quote),
               (minute_start / 600) * 600 AS bucket_start,
               SUM(known_gap_seconds), SUM(COALESCE(ws_quote_volume, 0)),
               SUM(official_quote_volume),
               SUM(official_quote_volume - COALESCE(ws_quote_volume, 0))
        FROM minute_reconciliations
        WHERE minute_start BETWEEN $1 AND $2 + 599
          AND status = $3
          AND UPPER(quote) = 'KRW'
          AND official_quote_volume IS NOT NULL
          AND effective_krw_volume IS NOT NULL
          AND official_quote_volume - COALESCE(ws_quote_volume, 0) >= 1
        GROUP BY exchange_name, market, (minute_start / 600) * 600
        ORDER BY bucket_start, exchange_name, market`,
		fromBucket, toBucket, reconcile.StatusRecoveredCandle,
	)
	if err != nil {
		return nil, fmt.Errorf("query recovered ten-minute supplements: %w", err)
	}
	defer rows.Close()
	result := make([]reconcile.MinuteSupplement, 0)
	for rows.Next() {
		row := reconcile.MinuteSupplement{IntervalSec: intervalSec, Source: "OFFICIAL_EXACT_1M_RESIDUALS_ALIGNED_10M"}
		if err := rows.Scan(
			&row.Exchange, &row.Market, &row.Base, &row.Quote, &row.MinuteStart,
			&row.KnownGapSeconds, &row.WSVolumeKRW, &row.OfficialVolumeKRW,
			&row.SupplementalVolumeKRW,
		); err != nil {
			return nil, fmt.Errorf("scan recovered ten-minute supplement: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recovered ten-minute supplements: %w", err)
	}
	return result, nil
}

func float64Pointer(value float64) *float64 { return &value }
func int64PointerLocal(value int64) *int64  { return &value }
func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
