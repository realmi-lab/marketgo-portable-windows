package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/aggregate"
	"market-share-go/internal/backfill"
	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
)

// CheckMinuteBackfillSchema is intentionally read-only. Unlike the legacy
// backfill commands, this command never bootstraps or migrates a database.
func (r *Repository) CheckMinuteBackfillSchema(ctx context.Context) error {
	checks := []string{
		`SELECT gap_id,start_second,end_second,status,affected_exchanges_json,affected_markets_json FROM observation_gap_events LIMIT 0`,
		`SELECT observed_at_ms,exchange_name,authoritative,quote_counts_json FROM market_registry_refreshes LIMIT 0`,
		`SELECT event_id,observed_at_ms,exchange_name,market,base,quote,scope,event_type FROM market_registry_events LIMIT 0`,
		`SELECT ` + wsMinuteColumns + ` FROM ws_trade_minutes LIMIT 0`,
		`SELECT ` + minuteSealColumns + ` FROM minute_observation_seals LIMIT 0`,
		`SELECT ` + officialObservationColumns + ` FROM official_minute_candle_observations LIMIT 0`,
		`SELECT exchange_name,market,minute_start,quote_volume_exact,notional_method FROM official_minute_candles LIMIT 0`,
		`SELECT exchange_name,market,minute_start,status,effective_krw_volume FROM minute_reconciliations LIMIT 0`,
		`SELECT ` + minuteCanonicalColumns + ` FROM volume_buckets LIMIT 0`,
	}
	for _, query := range checks {
		rows, err := r.pool.Query(ctx, query)
		if err != nil {
			return fmt.Errorf("minute evidence schema is unavailable; no migration was run: %w", err)
		}
		rows.Close()
	}
	return nil
}

type minuteReadQuery interface {
	rowQuerier
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadMinuteBackfillGaps(ctx context.Context, q minuteReadQuery, from, to int64, ids []int64) ([]backfill.ObservationGap, error) {
	if ids == nil {
		ids = []int64{}
	}
	rows, err := q.Query(ctx, `SELECT gap_id,start_second,end_second,affected_exchanges_json,affected_markets_json
		FROM observation_gap_events WHERE end_second IS NOT NULL AND status NOT IN ('OPEN','OFFICIAL_RECONCILIATION')
		AND start_second < $2 AND end_second >= $1 AND (cardinality($3::bigint[])=0 OR gap_id=ANY($3))
		ORDER BY start_second,gap_id LIMIT 201`, from, to, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []backfill.ObservationGap{}
	for rows.Next() {
		var g backfill.ObservationGap
		var exchanges, markets string
		if err := rows.Scan(&g.ID, &g.Start, &g.End, &exchanges, &markets); err != nil {
			return nil, err
		}
		if err := parseMinuteGapScope(&g, exchanges, markets); err != nil {
			return nil, err
		}
		if err := g.Validate(); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) > 200 {
		return nil, errors.New("more than 200 gaps; narrow the fixed range")
	}
	return result, nil
}

func parseMinuteGapScope(gap *backfill.ObservationGap, exchanges, markets string) error {
	var names []string
	if json.Unmarshal([]byte(exchanges), &names) != nil || json.Unmarshal([]byte(markets), &gap.AffectedMarkets) != nil {
		return errors.New("stored gap scope is malformed")
	}
	for _, name := range names {
		exchange, err := domain.ParseExchange(name)
		if err != nil || !domain.ScopeMatchesExchange(domain.ScopeDomestic, exchange) {
			return errors.New("stored gap has an unsupported exchange")
		}
		gap.AffectedExchanges = append(gap.AffectedExchanges, exchange)
	}
	for _, market := range gap.AffectedMarkets {
		if strings.TrimSpace(market) == "" {
			return errors.New("stored gap market scope is empty")
		}
	}
	return nil
}

// Historical membership is reconstructed from lifecycle events, never from
// today's active registry. The complete set must match the latest successful
// historical registry count, and membership may not change inside this minute.
func historicalMinuteMarkets(ctx context.Context, q minuteReadQuery, exchange domain.Exchange, minute domain.UnixSecond) ([]domain.MarketPair, error) {
	var expected *int64
	err := q.QueryRow(ctx, `SELECT (quote_counts_json->>'KRW')::bigint FROM market_registry_refreshes
		WHERE exchange_name=$1 AND authoritative AND observed_at_ms <= $2 ORDER BY observed_at_ms DESC LIMIT 1`, exchange, int64(minute)*1000).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("historical registry baseline is missing")
	}
	if err != nil {
		return nil, err
	}
	if expected == nil || *expected < 1 || *expected > 5000 {
		return nil, errors.New("historical KRW registry count is invalid")
	}
	var changed bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM market_registry_events WHERE exchange_name=$1 AND quote='KRW'
		AND observed_at_ms > $2 AND observed_at_ms < $3)`, exchange, int64(minute)*1000, (int64(minute)+60)*1000).Scan(&changed); err != nil {
		return nil, err
	}
	if changed {
		return nil, errors.New("registry membership changed inside the selected minute")
	}
	rows, err := q.Query(ctx, `SELECT market,base,quote,scope FROM (
		SELECT DISTINCT ON (market) market,base,quote,scope,event_type FROM market_registry_events
		WHERE exchange_name=$1 AND observed_at_ms <= $2 ORDER BY market,observed_at_ms DESC,event_id DESC
	) latest WHERE event_type IN ('ADDED','DISCOVERED') AND quote='KRW' AND scope='domestic' ORDER BY market LIMIT 5001`, exchange, int64(minute)*1000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.MarketPair{}
	for rows.Next() {
		item := domain.MarketPair{Exchange: exchange}
		if err := rows.Scan(&item.Symbol, &item.Base, &item.Quote, &item.Scope); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(result)) != *expected {
		return nil, fmt.Errorf("historical registry count mismatch: reconstructed %d expected %d", len(result), *expected)
	}
	return result, nil
}

func (r *Repository) MinuteBackfillCandidates(ctx context.Context, selection backfill.MinuteSelection) ([]backfill.MinuteCandidate, bool, error) {
	if err := selection.Validate(time.Now()); err != nil {
		return nil, false, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(selection.GapIDs) > 0 {
		var valid int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM observation_gap_events WHERE gap_id=ANY($1::bigint[]) AND end_second IS NOT NULL AND status NOT IN ('OPEN','OFFICIAL_RECONCILIATION') AND start_second < $3 AND end_second >= $2`, selection.GapIDs, selection.From.Unix(), selection.To.Unix()).Scan(&valid); err != nil {
			return nil, false, err
		}
		if valid != len(selection.GapIDs) {
			return nil, false, errors.New("selected gap is open, missing, duplicated or outside the fixed range")
		}
	}
	gaps, err := loadMinuteBackfillGaps(ctx, tx, selection.From.Unix(), selection.To.Unix(), selection.GapIDs)
	if err != nil {
		return nil, false, err
	}
	type key struct {
		minute   domain.UnixSecond
		exchange domain.Exchange
	}
	set := map[key]bool{}
	for _, gap := range gaps {
		exchanges := gap.AffectedExchanges
		if len(exchanges) == 0 {
			exchanges = domain.DomesticExchanges[:]
		}
		// Clip selection only. Original gap bounds and IDs remain evidence for
		// per-minute seconds and the transactional apply-time recheck.
		first := max(gap.Start, domain.UnixSecond(selection.From.Unix()))
		last := min(gap.End, domain.UnixSecond(selection.To.Unix()-1))
		for minute := first / 60 * 60; minute <= last; minute += 60 {
			for _, exchange := range exchanges {
				if backfill.ExactMinuteExchange(exchange) {
					set[key{minute, exchange}] = true
				}
			}
		}
	}
	if len(set) > 512 {
		return nil, false, errors.New("more than 512 exchange-minutes; narrow the range")
	}
	keys := make([]key, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].minute != keys[j].minute {
			return keys[i].minute < keys[j].minute
		}
		return keys[i].exchange < keys[j].exchange
	})
	after, _ := selection.AfterKey()
	result := []backfill.MinuteCandidate{}
	for _, k := range keys {
		seal, err := getMinuteSeal(ctx, tx, k.exchange, k.minute)
		if err != nil {
			return nil, false, err
		}
		if backfill.PreserveObservedMinute(nil, seal) {
			continue
		}
		markets, err := historicalMinuteMarkets(ctx, tx, k.exchange, k.minute)
		if err != nil {
			return nil, false, fmt.Errorf("%s minute %d: %w", k.exchange, k.minute, err)
		}
		for _, market := range markets {
			candidate := backfill.MinuteCandidate{Exchange: k.exchange, Market: market.Symbol, Base: market.Base, MinuteStart: k.minute}
			if candidate.Key() <= after {
				continue
			}
			candidate.GapSeconds, candidate.GapIDs = backfill.GapSecondsForMinute(gaps, k.exchange, market.Symbol, k.minute)
			if candidate.GapSeconds == 0 {
				continue
			}
			ws, err := getWSMinute(ctx, tx, k.exchange, market.Symbol, k.minute)
			if err != nil {
				return nil, false, err
			}
			if backfill.PreserveObservedMinute(ws, seal) {
				continue
			}
			result = append(result, candidate)
			if len(result) > selection.Limit {
				return result[:selection.Limit], true, nil
			}
		}
	}
	return result, false, nil
}

// Only the two official evidence tables and their derived reconciliation row
// are written, together in one transaction. No WS bucket, seal, gap or coverage
// status is modified. A later exact recovery always takes precedence.
func (r *Repository) ApplyMinuteEvidence(ctx context.Context, candidate backfill.MinuteCandidate, candle reconcile.OfficialMinuteCandle) (string, error) {
	return r.applyMinuteEvidence(ctx, candidate, candle, false)
}

// ApplyMinuteReplacement selects the verified whole official minute for amount
// analysis. Raw trades and observation coverage remain immutable.
func (r *Repository) ApplyMinuteReplacement(ctx context.Context, candidate backfill.MinuteCandidate, candle reconcile.OfficialMinuteCandle) (string, error) {
	return r.applyMinuteEvidence(ctx, candidate, candle, true)
}

func (r *Repository) applyMinuteEvidence(ctx context.Context, candidate backfill.MinuteCandidate, candle reconcile.OfficialMinuteCandle, replace bool) (string, error) {
	if err := validateMinuteEvidence(candidate, candle, time.Now()); err != nil {
		return "", err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='5s'`); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='500ms'`); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "minute-evidence:"+candidate.Key()); err != nil {
		return "", err
	}
	var exact bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM observation_gap_events WHERE gap_id=ANY($1::bigint[]) AND status='OFFICIAL_RECONCILIATION')`, candidate.GapIDs).Scan(&exact); err != nil {
		return "", err
	}
	if exact {
		return "PRESERVED_EXACT_RECOVERY", nil
	}
	gaps, err := loadMinuteBackfillGaps(ctx, tx, 0, time.Now().Add(-2*time.Minute).Truncate(time.Minute).Unix(), candidate.GapIDs)
	if err != nil {
		return "", err
	}
	seconds, ids := backfill.GapSecondsForMinute(gaps, candidate.Exchange, candidate.Market, candidate.MinuteStart)
	if seconds != candidate.GapSeconds || len(ids) != len(candidate.GapIDs) {
		return "", errors.New("stored gap evidence changed; rebuild the minute plan")
	}
	markets, err := historicalMinuteMarkets(ctx, tx, candidate.Exchange, candidate.MinuteStart)
	if err != nil {
		return "", err
	}
	member := false
	for _, market := range markets {
		if market.Symbol == candidate.Market && market.Base == candidate.Base {
			member = true
		}
	}
	if !member {
		return "", errors.New("candidate is not in the historical registry")
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM minute_observation_seals WHERE exchange_name=$1 AND minute_start=$2 FOR SHARE`, candidate.Exchange, candidate.MinuteStart); err != nil {
		return "", err
	}
	seal, err := getMinuteSeal(ctx, tx, candidate.Exchange, candidate.MinuteStart)
	if err != nil {
		return "", err
	}
	ws, err := getWSMinute(ctx, tx, candidate.Exchange, candidate.Market, candidate.MinuteStart)
	if err != nil {
		return "", err
	}
	if backfill.PreserveObservedMinute(ws, seal) {
		return "PRESERVED_COMPLETE_WS", nil
	}
	canonical, err := loadMinuteCanonicalEvidence(ctx, tx, candidate)
	if err != nil {
		return "", err
	}
	if replace {
		if candle.NotionalMethod != "EXACT_CANDLE_QUOTE_VOLUME" || canonical.Totals.Validate() != nil {
			return "", errors.New("whole-minute replacement requires exact official quote evidence and valid canonical totals")
		}
	} else {
		candle, err = admitMinuteCanonicalEvidence(ws, candle, canonical)
		if err != nil {
			return "", err
		}
	}
	previous, err := getOfficialObservation(ctx, tx, candidate.Exchange, candidate.Market, candidate.MinuteStart, backfill.MinuteBackfillAttempt)
	if err != nil {
		return "", err
	}
	if previous != nil {
		if sameOfficialCandle(*previous, candle) || replace && sameReplacementOfficialCandle(*previous, candle) {
			if replace {
				var status string
				err := tx.QueryRow(ctx, `SELECT status FROM minute_reconciliations WHERE exchange_name=$1 AND market=$2 AND minute_start=$3`, candidate.Exchange, candidate.Market, candidate.MinuteStart).Scan(&status)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return "", err
				}
				if status == reconcile.StatusReplacedCandle {
					return "ALREADY_APPLIED_REPLACEMENT", nil
				}
			} else {
				if _, ok := reconcile.WithCoinoneSingleTradePrecision(ws, candle); ok {
					return "ALREADY_APPLIED_SOURCE_PRECISION_DIFFERENCE", nil
				}
				return "ALREADY_APPLIED", nil
			}
		} else {
			return "", &reconcile.ImmutableMinuteConflict{Message: "operational minute evidence attempt 3 conflicts with stored evidence"}
		}
	}
	if err := candle.Normalize(); err != nil {
		return "", err
	}
	if previous == nil {
		_, err = tx.Exec(ctx, `INSERT INTO official_minute_candle_observations (`+officialObservationColumns+`)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, candle.Exchange, candle.Market, candle.Base, candle.Quote, candle.MinuteStart, candle.ObservationAttempt, candle.Open, candle.High, candle.Low, candle.Close, candle.BaseVolume, candle.QuoteVolume, candle.QuoteVolumeExact, candle.SourceKind, candle.NotionalMethod, candle.FetchedAt, candle.CreatedAt)
		if err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO official_minute_candles(exchange_name,market,base,quote,minute_start,open_price,high_price,low_price,close_price,base_volume,quote_volume,quote_volume_exact,source_kind,notional_method,fetched_at,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(exchange_name,market,minute_start) DO NOTHING`, candle.Exchange, candle.Market, candle.Base, candle.Quote, candle.MinuteStart, candle.Open, candle.High, candle.Low, candle.Close, candle.BaseVolume, candle.QuoteVolume, candle.QuoteVolumeExact, candle.SourceKind, candle.NotionalMethod, candle.FetchedAt, candle.CreatedAt)
	if err != nil {
		return "", err
	}
	decision := reconcile.Decide(ws, &candle, seconds)
	if replace && decision.Status == reconcile.StatusRecoveredCandle {
		decision.Status = reconcile.StatusReplacedCandle
		decision.Reason = "verified official whole minute replaces this market-minute in amount analysis; observed raw trades and exact coverage are retained"
	}
	if decision.Status != reconcile.StatusRecoveredCandle && decision.Status != reconcile.StatusSourcePrecisionDifference && decision.Status != reconcile.StatusReplacedCandle {
		return "", errors.New("minute evidence did not produce an exact candle reconciliation")
	}
	if err := saveReconciliation(ctx, tx, buildReconciliation(ws, &candle, decision, seconds, time.Now().UTC())); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if decision.Status == reconcile.StatusSourcePrecisionDifference {
		return "APPLIED_SOURCE_PRECISION_DIFFERENCE", nil
	}
	if replace {
		return "APPLIED_REPLACEMENT", nil
	}
	return "APPLIED", nil
}

// A validated precision envelope preserves the same official OHLC/volume
// columns. Selecting those official columns does not rewrite that envelope
// or assert that the original WebSocket trade was restored.
func sameReplacementOfficialCandle(previous, fetched reconcile.OfficialMinuteCandle) bool {
	if _, valid := reconcile.CoinonePrecisionEvidence(previous); !valid {
		return false
	}
	previous.NotionalMethod = fetched.NotionalMethod
	return sameOfficialCandle(previous, fetched)
}

const minuteCanonicalColumns = `second,scope,exchange_name,market,base,quote,native_volume,krw_volume,trade_count,unresolved_count,buy_krw_volume,sell_krw_volume,base_volume,source_kind,notional_method,source_granularity_sec`

// PostgreSQL's default btrim removes only ASCII spaces. Match the Unicode
// whitespace removed by strings.TrimSpace in the atomic runtime identity.
const minuteIdentityWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

type minuteCanonicalEvidence struct {
	Totals      aggregate.Totals
	TradeSecond domain.UnixSecond
}

// Read every matching normalized identity in this same SERIALIZABLE snapshot.
// Do not use the potentially missing/stale ws_trade_minutes row as a substitute,
// and never add that derived row to the canonical totals. Individual rows are
// checked before summation so invalid aliases cannot cancel valid amounts.
func loadMinuteCanonicalEvidence(ctx context.Context, q minuteReadQuery, candidate backfill.MinuteCandidate) (minuteCanonicalEvidence, error) {
	var result minuteCanonicalEvidence
	rows, err := q.Query(ctx, `SELECT `+minuteCanonicalColumns+` FROM volume_buckets
		WHERE second >= $1 AND second < $2 AND lower(btrim(exchange_name,$5))=$3 AND upper(btrim(market,$5))=$4
		ORDER BY second,exchange_name,market LIMIT 4097`, candidate.MinuteStart, candidate.MinuteStart+60, candidate.Exchange, candidate.Market, minuteIdentityWhitespace)
	if err != nil {
		return result, fmt.Errorf("read canonical minute evidence: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 4096 {
			return result, errors.New("canonical minute has too many identity aliases")
		}
		var row aggregate.Bucket
		var source, method string
		var granularity int64
		if err := rows.Scan(&row.Second, &row.Key.Scope, &row.Key.Exchange, &row.Key.Market, &row.Key.Base, &row.Key.Quote,
			&row.Totals.NativeVolume, &row.Totals.KRWVolume, &row.Totals.TradeCount, &row.Totals.UnresolvedCount,
			&row.Totals.BuyKRWVolume, &row.Totals.SellKRWVolume, &row.Totals.BaseVolume, &source, &method, &granularity); err != nil {
			return result, fmt.Errorf("scan canonical minute evidence: %w", err)
		}
		if err := addMinuteCanonicalBucket(&result, candidate, row, source, method, granularity); err != nil {
			return result, err
		}
	}
	return result, rows.Err()
}

func addMinuteCanonicalBucket(result *minuteCanonicalEvidence, candidate backfill.MinuteCandidate, row aggregate.Bucket, source, method string, granularity int64) error {
	k, totals := row.Key, row.Totals
	if row.Second < candidate.MinuteStart || row.Second >= candidate.MinuteStart+60 ||
		!strings.EqualFold(strings.TrimSpace(k.Exchange), string(candidate.Exchange)) || !strings.EqualFold(strings.TrimSpace(k.Market), candidate.Market) ||
		!strings.EqualFold(strings.TrimSpace(k.Base), candidate.Base) || !strings.EqualFold(strings.TrimSpace(k.Quote), "KRW") || !strings.EqualFold(strings.TrimSpace(k.Scope), "domestic") ||
		(source != reconcile.SourceWebSocket && source != backfill.RawTradeBucketSource) || method != "EXACT_TRADE" || granularity != 1 || totals.Validate() != nil ||
		totals.NativeVolume != totals.KRWVolume || totals.UnresolvedCount != 0 ||
		(totals.TradeCount == 0 && (totals.NativeVolume != 0 || totals.BaseVolume != 0 || totals.BuyKRWVolume != 0 || totals.SellKRWVolume != 0)) ||
		(totals.TradeCount > 0 && (totals.NativeVolume <= 0 || totals.BaseVolume <= 0)) {
		return errors.New("canonical minute contains invalid identity, source or trade totals")
	}
	if !result.Totals.CheckedAdd(totals) {
		return errors.New("canonical minute totals overflow")
	}
	if totals.TradeCount > 0 {
		result.TradeSecond = row.Second
	}
	return nil
}

func admitMinuteCanonicalEvidence(ws *reconcile.WSTradeMinute, candle reconcile.OfficialMinuteCandle, canonical minuteCanonicalEvidence) (reconcile.OfficialMinuteCandle, error) {
	if candle.QuoteVolume == nil || canonical.Totals.Validate() != nil || canonical.Totals.UnresolvedCount != 0 || canonical.Totals.NativeVolume != canonical.Totals.KRWVolume {
		return candle, errors.New("canonical minute evidence is invalid")
	}
	precision := candle.NotionalMethod != "EXACT_CANDLE_QUOTE_VOLUME"
	if precision || ws != nil && ws.QuoteVolume > *candle.QuoteVolume {
		proved, ok := reconcile.WithCoinoneSingleTradePrecision(ws, candle)
		if ok {
			e, bound := reconcile.CoinonePrecisionEvidence(proved)
			totals := canonical.Totals
			if !bound || totals.TradeCount != 1 || !e.ProvesSingleTrade(totals.NativeVolume, totals.KRWVolume, totals.BaseVolume, 1, 0) || canonical.TradeSecond != domain.UnixSecond(int64(*ws.FirstTradeTS)/1000) {
				return candle, errors.New("Coinone original WS precision proof disagrees with canonical minute")
			}
			return proved, nil
		}
		if precision || ws != nil && ws.QuoteVolume > *candle.QuoteVolume+1e-6 {
			return candle, errors.New("official minute total is below observed WS volume; evidence conflicts")
		}
	}
	if _, ok := reconcile.CanonicalMinuteResidual(*candle.QuoteVolume, canonical.Totals.KRWVolume); !ok {
		return candle, errors.New("official minute total is below canonical observed volume; evidence conflicts")
	}
	return candle, nil
}

func validateMinuteEvidence(candidate backfill.MinuteCandidate, candle reconcile.OfficialMinuteCandle, now time.Time) error {
	if !backfill.ExactMinuteExchange(candidate.Exchange) || candidate.MinuteStart%60 != 0 || candidate.MinuteStart <= 0 || candidate.Market == "" || candidate.Base == "" || candidate.GapSeconds < 1 || candidate.GapSeconds > 60 || len(candidate.GapIDs) == 0 {
		return errors.New("invalid minute evidence candidate")
	}
	seen := map[int64]bool{}
	for _, id := range candidate.GapIDs {
		if id <= 0 || seen[id] {
			return errors.New("minute evidence requires distinct positive gap IDs")
		}
		seen[id] = true
	}
	if time.Unix(int64(candidate.MinuteStart)+60, 0).After(now.Add(-2 * time.Minute).Truncate(time.Minute)) {
		return errors.New("minute evidence is not safely closed")
	}
	expectedSource := strings.ToUpper(string(candidate.Exchange)) + "_OFFICIAL_MINUTE_CANDLE_REST"
	_, precisionEvidence := reconcile.CoinonePrecisionEvidence(candle)
	methodValid := candle.NotionalMethod == "EXACT_CANDLE_QUOTE_VOLUME" || precisionEvidence
	if candle.Exchange != candidate.Exchange || candle.Market != candidate.Market || candle.Base != candidate.Base || candle.Quote != "KRW" || candle.MinuteStart != candidate.MinuteStart || candle.ObservationAttempt != backfill.MinuteBackfillAttempt || !candle.QuoteVolumeExact || candle.QuoteVolume == nil || !methodValid || candle.SourceKind != expectedSource {
		return errors.New("official evidence identity or exact quote provenance is invalid")
	}
	return candle.Normalize()
}
