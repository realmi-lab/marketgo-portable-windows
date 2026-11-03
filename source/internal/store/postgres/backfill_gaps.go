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

	"market-share-go/internal/backfill"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

func (r *Repository) LoadObservationGap(ctx context.Context, id int64) (backfill.ObservationGap, error) {
	var gap backfill.ObservationGap
	var exchangesJSON, marketsJSON string
	err := r.pool.QueryRow(ctx, `SELECT gap_id,start_second,end_second,affected_exchanges_json,affected_markets_json
		FROM observation_gap_events WHERE gap_id=$1 AND end_second IS NOT NULL AND status<>'OPEN'`, id).Scan(&gap.ID, &gap.Start, &gap.End, &exchangesJSON, &marketsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return gap, fmt.Errorf("closed observation gap %d was not found", id)
	}
	if err != nil {
		return gap, fmt.Errorf("load observation gap: %w", err)
	}
	gap.AffectedExchanges = parseBackfillExchanges(exchangesJSON)
	gap.AffectedMarkets = parseBackfillMarkets(marketsJSON)
	return gap, gap.Validate()
}

func (r *Repository) PendingObservationGaps(ctx context.Context, minAge time.Duration, retainedStart domain.UnixSecond, now time.Time, limit int) ([]backfill.ObservationGap, error) {
	if limit < 1 {
		limit = 1
	}
	cutoff := now.Add(-maxDuration(0, minAge)).Unix()
	rows, err := r.pool.Query(ctx, `SELECT gap_id,start_second,end_second,affected_exchanges_json,affected_markets_json
        FROM observation_gap_events
        WHERE end_second IS NOT NULL AND status<>'OPEN' AND status<>$1
          AND end_second<=$2 AND start_second>=$3
        ORDER BY end_second DESC,gap_id DESC`, backfill.OfficialReconciliationStatus, cutoff, retainedStart)
	if err != nil {
		return nil, fmt.Errorf("query pending observation gaps: %w", err)
	}
	defer rows.Close()
	candidates := make([]backfill.ObservationGap, 0)
	for rows.Next() {
		var gap backfill.ObservationGap
		var exchangesJSON, marketsJSON string
		if err := rows.Scan(&gap.ID, &gap.Start, &gap.End, &exchangesJSON, &marketsJSON); err != nil {
			return nil, err
		}
		gap.AffectedExchanges = parseBackfillExchanges(exchangesJSON)
		gap.AffectedMarkets = parseBackfillMarkets(marketsJSON)
		candidates = append(candidates, gap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]backfill.ObservationGap, 0, limit)
	for _, gap := range candidates {
		pending, err := r.gapNeedsBackfill(ctx, gap)
		if err != nil {
			return nil, err
		}
		if pending {
			result = append(result, gap)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (r *Repository) gapNeedsBackfill(ctx context.Context, gap backfill.ObservationGap) (bool, error) {
	exchanges := gap.AffectedExchanges
	if len(exchanges) == 0 {
		exchanges = append([]domain.Exchange(nil), domain.DomesticExchanges[:]...)
	}
	for _, exchange := range exchanges {
		source, err := backfill.SourceForExchange(exchange)
		if err != nil {
			continue
		}
		var status string
		err = r.pool.QueryRow(ctx, `SELECT status FROM historical_backfill_runs
            WHERE gap_id=$1 AND exchange_name=$2 AND quote='KRW' AND source=$3`, gap.ID, exchange, source).Scan(&status)
		pending, err := backfillRunPending(status, err)
		if err != nil || pending {
			return pending, err
		}
	}
	return false, nil
}

func backfillRunPending(status string, err error) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		// A database failure is not evidence that a recovery is pending. Surface
		// it so an operator does not receive a plausible but incomplete gap list.
		return false, err
	}
	return status != "APPLIED_CURRENT_REGISTRY", nil
}

func parseBackfillExchanges(raw string) []domain.Exchange {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	seen := map[domain.Exchange]struct{}{}
	for _, value := range values {
		exchange, err := domain.ParseExchange(value)
		if err != nil {
			continue
		}
		if _, err := backfill.SourceForExchange(exchange); err == nil {
			seen[exchange] = struct{}{}
		}
	}
	result := make([]domain.Exchange, 0, len(seen))
	for _, exchange := range domain.DomesticExchanges {
		if _, ok := seen[exchange]; ok {
			result = append(result, exchange)
		}
	}
	return result
}

func parseBackfillMarkets(raw string) []string {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := strings.TrimSpace(value); normalized != "" {
			seen[normalized] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type backfillRecoveryQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type persistedBackfillDetail struct {
	SourceGranularitySeconds int64    `json:"source_granularity_sec"`
	NotionalMethod           string   `json:"notional_method"`
	SuccessfulMarkets        []string `json:"successful_markets"`
}

func recoveryDecisionForGap(ctx context.Context, queryer backfillRecoveryQueryer, gapID int64) (backfill.ObservationGap, backfill.RecoveryDecision, error) {
	rows, err := queryer.Query(ctx, `
		SELECT g.gap_id,g.start_second,g.end_second,g.affected_exchanges_json,g.affected_markets_json,
		       r.exchange_name,r.source,r.status,r.start_second,r.end_second,r.detail_json
		FROM observation_gap_events g
		LEFT JOIN historical_backfill_runs r ON r.gap_id=g.gap_id AND r.quote='KRW'
		WHERE g.gap_id=$1 AND g.end_second IS NOT NULL
		ORDER BY r.exchange_name,r.source`, gapID)
	if err != nil {
		return backfill.ObservationGap{}, backfill.RecoveryDecision{}, fmt.Errorf("query backfill recovery evidence: %w", err)
	}
	defer rows.Close()
	var gap backfill.ObservationGap
	evidence := make([]backfill.RecoveryEvidence, 0)
	found := false
	for rows.Next() {
		var exchangesJSON, marketsJSON string
		var exchangeName, source, status, detailJSON *string
		var runStart, runEnd *domain.UnixSecond
		if err := rows.Scan(
			&gap.ID, &gap.Start, &gap.End, &exchangesJSON, &marketsJSON,
			&exchangeName, &source, &status, &runStart, &runEnd, &detailJSON,
		); err != nil {
			return gap, backfill.RecoveryDecision{}, err
		}
		found = true
		gap.AffectedExchanges = parseBackfillExchanges(exchangesJSON)
		gap.AffectedMarkets = parseBackfillMarkets(marketsJSON)
		if exchangeName == nil {
			continue
		}
		exchange, err := domain.ParseExchange(*exchangeName)
		if err != nil || source == nil || status == nil || runStart == nil || runEnd == nil || detailJSON == nil {
			continue
		}
		var detail persistedBackfillDetail
		if err := json.Unmarshal([]byte(*detailJSON), &detail); err != nil {
			return gap, backfill.RecoveryDecision{}, fmt.Errorf("decode backfill recovery detail for gap %d: %w", gapID, err)
		}
		evidence = append(evidence, backfill.RecoveryEvidence{
			Exchange: exchange, Source: *source, Status: *status, Start: *runStart, End: *runEnd,
			SourceGranularitySeconds: detail.SourceGranularitySeconds,
			NotionalMethod:           detail.NotionalMethod,
			SuccessfulMarkets:        detail.SuccessfulMarkets,
		})
	}
	if err := rows.Err(); err != nil {
		return gap, backfill.RecoveryDecision{}, err
	}
	if !found {
		return gap, backfill.RecoveryDecision{}, fmt.Errorf("observation gap %d was not found", gapID)
	}
	return gap, backfill.ExactRecoveryDecision(gap, evidence), nil
}

func loadHistoricalRecoveries(ctx context.Context, queryer backfillRecoveryQueryer, since domain.UnixSecond, state *coverage.State) error {
	rows, err := queryer.Query(ctx, `
		SELECT gap_id FROM observation_gap_events
		WHERE status=$1 AND end_second IS NOT NULL AND end_second>=$2
		ORDER BY gap_id`, backfill.OfficialReconciliationStatus, since)
	if err != nil {
		return fmt.Errorf("query exact backfill recoveries: %w", err)
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		gap, decision, err := recoveryDecisionForGap(ctx, queryer, id)
		if err != nil {
			return err
		}
		if !decision.Eligible {
			continue
		}
		state.HistoricalRecoveries = append(state.HistoricalRecoveries, coverage.HistoricalRecovery{
			GapID: gap.ID, Start: int64(gap.Start), End: int64(gap.End), Source: backfill.RawTradeSource,
		})
	}
	return nil
}
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
