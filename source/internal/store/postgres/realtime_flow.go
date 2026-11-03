package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"

	marketanalysis "market-share-go/internal/analysis"
)

const realtimeFlowSummaryLimit = 5

func (r *Repository) SaveRealtimeFlowState(ctx context.Context, state marketanalysis.RealtimeFlowState) error {
	_, err := r.SaveRealtimeFlowStateWithSummary(ctx, state)
	return err
}

// SaveRealtimeFlowStateWithSummary commits the immutable evidence and compact
// analytical projection atomically, then returns the exact projection that was
// stored. The application can append this immutable value to its in-memory
// 24-hour ring without querying PostgreSQL again.
func (r *Repository) SaveRealtimeFlowStateWithSummary(
	ctx context.Context,
	state marketanalysis.RealtimeFlowState,
) (marketanalysis.RealtimeFlowState, error) {
	state.Status = strings.ToUpper(strings.TrimSpace(state.Status))
	state.Direction = strings.ToUpper(strings.TrimSpace(state.Direction))
	if err := validateRealtimeFlowState(state); err != nil {
		return marketanalysis.RealtimeFlowState{}, err
	}
	detailed := state.Status == "WATCH" || state.Status == "ALERT"
	archived := archiveRealtimeFlowState(state, detailed)
	summary := realtimeFlowSummaryProjection(archived)
	payload, err := json.Marshal(archived)
	if err != nil {
		return marketanalysis.RealtimeFlowState{}, fmt.Errorf("encode realtime flow observation: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return marketanalysis.RealtimeFlowState{}, fmt.Errorf("begin realtime flow observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertRealtimeFlowObservation(ctx, tx, archived, detailed, payload); err != nil {
		return marketanalysis.RealtimeFlowState{}, err
	}
	if err := replaceRealtimeFlowTenSecondSummary(ctx, tx, summary); err != nil {
		return marketanalysis.RealtimeFlowState{}, err
	}
	if err := replaceRealtimeFlowDetails(ctx, tx, archived); err != nil {
		return marketanalysis.RealtimeFlowState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return marketanalysis.RealtimeFlowState{}, fmt.Errorf("commit realtime flow observation: %w", err)
	}
	return summary, nil
}

func validateRealtimeFlowState(state marketanalysis.RealtimeFlowState) error {
	if state.AsOfMS <= 0 {
		return errors.New("realtime flow timestamp must be positive")
	}
	if state.Version != marketanalysis.RealtimeFlowVersion {
		return fmt.Errorf("realtime flow contract version must be %s", marketanalysis.RealtimeFlowVersion)
	}
	switch state.Status {
	case "WARMING", "NORMAL", "WATCH", "ALERT", "DEGRADED":
	default:
		return errors.New("realtime flow status is invalid")
	}
	if state.Status != "WATCH" && state.Status != "ALERT" {
		return nil
	}
	if state.ShareDeltaPP == nil || !finiteRealtimeFlowNumber(*state.ShareDeltaPP) || *state.ShareDeltaPP == 0 {
		return fmt.Errorf("realtime %s state must have a finite non-zero share_delta_pp", state.Status)
	}
	if state.Direction != "UP" && state.Direction != "DOWN" {
		return fmt.Errorf("realtime %s direction must be UP or DOWN", state.Status)
	}
	if state.Direction == "UP" && *state.ShareDeltaPP < 0 || state.Direction == "DOWN" && *state.ShareDeltaPP > 0 {
		return fmt.Errorf("realtime %s direction conflicts with share_delta_pp", state.Status)
	}
	return nil
}

func finiteRealtimeFlowNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func archiveRealtimeFlowState(state marketanalysis.RealtimeFlowState, detailed bool) marketanalysis.RealtimeFlowState {
	result := state
	// The live state carries a rolling 60-point, one-second chart. Persisted
	// history already stores one state per 10 seconds, so archiving this nested
	// series would duplicate data without adding analytical evidence.
	result.RecentShare1S = nil
	result.Windows = append(
		make([]marketanalysis.RealtimeFlowWindow, 0, len(state.Windows)),
		state.Windows...,
	)
	result.ExchangeDrivers = append(
		make([]marketanalysis.RealtimeFlowExchangeDriver, 0, len(state.ExchangeDrivers)),
		state.ExchangeDrivers...,
	)
	assetLimit := len(state.CurrentAssets)
	if !detailed {
		assetLimit = min(assetLimit, realtimeFlowSummaryLimit)
	}
	result.Drivers = archiveRealtimeFlowDrivers(state.Drivers, detailed)
	result.CurrentAssets = append(
		make([]marketanalysis.RealtimeFlowCurrentAsset, 0, assetLimit),
		state.CurrentAssets[:assetLimit]...,
	)
	result.StoredDriverCount = len(result.Drivers)
	if result.TotalDriverCount < len(state.Drivers) {
		result.TotalDriverCount = len(state.Drivers)
	}
	result.ResidualContributionPP = nil
	if result.ShareDeltaPP != nil {
		residual := *result.ShareDeltaPP
		for _, driver := range result.Drivers {
			residual -= driver.ContributionPP
		}
		result.ResidualContributionPP = &residual
	}
	result.TenSecondAttribution = archiveRealtimeFlowAttribution(state.TenSecondAttribution, detailed)
	result.DataQuality = make(map[string]any, len(state.DataQuality))
	for key, value := range state.DataQuality {
		result.DataQuality[key] = value
	}
	return result
}

func archiveRealtimeFlowAttribution(
	source *marketanalysis.RealtimeFlowAttribution,
	detailed bool,
) *marketanalysis.RealtimeFlowAttribution {
	if source == nil {
		return nil
	}
	result := *source
	result.Drivers = archiveRealtimeFlowDrivers(source.Drivers, detailed)
	result.ExchangeDrivers = append(
		make([]marketanalysis.RealtimeFlowExchangeDriver, 0, len(source.ExchangeDrivers)),
		source.ExchangeDrivers...,
	)
	result.StoredDriverCount = len(result.Drivers)
	if result.TotalDriverCount < len(source.Drivers) {
		result.TotalDriverCount = len(source.Drivers)
	}
	result.ResidualContributionPP = nil
	if source.ResidualContributionPP != nil {
		included := 0.0
		for _, driver := range result.Drivers {
			included += driver.ContributionPP
		}
		totalDelta := *source.ResidualContributionPP
		for _, driver := range source.Drivers {
			totalDelta += driver.ContributionPP
		}
		residual := totalDelta - included
		result.ResidualContributionPP = &residual
	}
	return &result
}

func archiveRealtimeFlowDrivers(
	source []marketanalysis.RealtimeFlowDriver,
	detailed bool,
) []marketanalysis.RealtimeFlowDriver {
	driverLimit := len(source)
	if !detailed {
		driverLimit = min(driverLimit, realtimeFlowSummaryLimit)
	}
	result := make([]marketanalysis.RealtimeFlowDriver, driverLimit)
	for index := range result {
		result[index] = source[index]
		impactLimit := len(source[index].ExchangeImpacts)
		if !detailed {
			impactLimit = min(impactLimit, 1)
		}
		result[index].ExchangeImpacts = append(
			make([]marketanalysis.RealtimeFlowExchangeImpact, 0, impactLimit),
			source[index].ExchangeImpacts[:impactLimit]...,
		)
	}
	return result
}

// realtimeFlowSummaryProjection is the complete semantic input of the rolling
// period summarizer and nothing more. Keeping this projection explicit makes
// it possible to prove that the hot path preserves episode boundaries, signed
// contribution, residuals, and exchange attribution without retaining order
// books or unrelated live-state fields.
func realtimeFlowSummaryProjection(state marketanalysis.RealtimeFlowState) marketanalysis.RealtimeFlowState {
	result := marketanalysis.RealtimeFlowState{
		Version: state.Version,
		AsOfMS:  state.AsOfMS,
		Status:  state.Status,
		Windows: []marketanalysis.RealtimeFlowWindow{},
		DataQuality: map[string]any{
			"complete_frame": state.DataQuality["complete_frame"],
			"frame_status":   state.DataQuality["frame_status"],
		},
	}
	for _, window := range state.Windows {
		if window.DurationSec == marketanalysis.RealtimeFlowSummarySampleSec {
			result.Windows = append(result.Windows, window)
			break
		}
	}
	if state.TenSecondAttribution == nil ||
		state.TenSecondAttribution.WindowSec != marketanalysis.RealtimeFlowSummarySampleSec {
		return result
	}
	result.TenSecondAttribution = &marketanalysis.RealtimeFlowAttribution{
		WindowSec:              marketanalysis.RealtimeFlowSummarySampleSec,
		ResidualContributionPP: state.TenSecondAttribution.ResidualContributionPP,
		Drivers:                make([]marketanalysis.RealtimeFlowDriver, 0, len(state.TenSecondAttribution.Drivers)),
		ExchangeDrivers:        make([]marketanalysis.RealtimeFlowExchangeDriver, 0, len(state.TenSecondAttribution.ExchangeDrivers)),
	}
	for _, driver := range state.TenSecondAttribution.Drivers {
		result.TenSecondAttribution.Drivers = append(
			result.TenSecondAttribution.Drivers,
			marketanalysis.RealtimeFlowDriver{
				Rank: driver.Rank, Asset: driver.Asset,
				ContributionPP:            driver.ContributionPP,
				BithumbVolumeChangeKRW:    driver.BithumbVolumeChangeKRW,
				CompetitorVolumeChangeKRW: driver.CompetitorVolumeChangeKRW,
			},
		)
	}
	for _, driver := range state.TenSecondAttribution.ExchangeDrivers {
		result.TenSecondAttribution.ExchangeDrivers = append(
			result.TenSecondAttribution.ExchangeDrivers,
			marketanalysis.RealtimeFlowExchangeDriver{
				Rank: driver.Rank, Exchange: driver.Exchange,
				ContributionPP: driver.ContributionPP,
			},
		)
	}
	return result
}

func upsertRealtimeFlowObservation(ctx context.Context, tx pgx.Tx, state marketanalysis.RealtimeFlowState, detailed bool, payload []byte) error {
	detailLevel := "SUMMARY"
	if detailed {
		detailLevel = "SIGNAL"
	}
	_, err := tx.Exec(ctx, `INSERT INTO realtime_flow_observations (
        as_of_ms, contract_version, status, direction, reason,
        primary_window_sec, share_delta_pp, domestic_volume_1s_krw,
        bithumb_volume_1s_krw, bithumb_share_1s_pct, detail_level, payload_json
    ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
    ON CONFLICT(as_of_ms) DO UPDATE SET
        contract_version=EXCLUDED.contract_version, status=EXCLUDED.status,
        direction=EXCLUDED.direction, reason=EXCLUDED.reason,
        primary_window_sec=EXCLUDED.primary_window_sec,
        share_delta_pp=EXCLUDED.share_delta_pp,
        domestic_volume_1s_krw=EXCLUDED.domestic_volume_1s_krw,
        bithumb_volume_1s_krw=EXCLUDED.bithumb_volume_1s_krw,
        bithumb_share_1s_pct=EXCLUDED.bithumb_share_1s_pct,
        detail_level=EXCLUDED.detail_level, payload_json=EXCLUDED.payload_json`,
		state.AsOfMS, state.Version, state.Status, state.Direction, state.Reason,
		state.PrimaryWindowSec, state.ShareDeltaPP, state.DomesticVolume1SKRW,
		state.BithumbVolume1SKRW, state.BithumbShare1SPct, detailLevel, payload,
	)
	if err != nil {
		return fmt.Errorf("save realtime flow observation: %w", err)
	}
	return nil
}

func replaceRealtimeFlowTenSecondSummary(ctx context.Context, tx pgx.Tx, state marketanalysis.RealtimeFlowState) error {
	frameComplete, _ := state.DataQuality["complete_frame"].(bool)
	frameStatus, _ := state.DataQuality["frame_status"].(string)
	var (
		windowAvailable  bool
		windowStatus     string
		currentSharePct  *float64
		previousSharePct *float64
		shareDeltaPP     *float64
	)
	for index := range state.Windows {
		window := &state.Windows[index]
		if window.DurationSec != marketanalysis.RealtimeFlowSummarySampleSec {
			continue
		}
		windowAvailable = true
		windowStatus = window.Status
		currentSharePct = window.CurrentSharePct
		previousSharePct = window.PreviousSharePct
		shareDeltaPP = window.ShareDeltaPP
		break
	}
	attribution := state.TenSecondAttribution
	attributionAvailable := attribution != nil && attribution.WindowSec == marketanalysis.RealtimeFlowSummarySampleSec
	var residualContributionPP *float64
	if attributionAvailable {
		residualContributionPP = attribution.ResidualContributionPP
	}
	_, err := tx.Exec(ctx, `INSERT INTO realtime_flow_10s_samples (
        as_of_ms, contract_version, status, frame_complete, frame_status,
        window_available, window_status, current_share_pct, previous_share_pct,
        share_delta_pp, attribution_available, residual_contribution_pp
    ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
    ON CONFLICT(as_of_ms) DO UPDATE SET
        contract_version=EXCLUDED.contract_version, status=EXCLUDED.status,
        frame_complete=EXCLUDED.frame_complete, frame_status=EXCLUDED.frame_status,
        window_available=EXCLUDED.window_available, window_status=EXCLUDED.window_status,
        current_share_pct=EXCLUDED.current_share_pct,
        previous_share_pct=EXCLUDED.previous_share_pct,
        share_delta_pp=EXCLUDED.share_delta_pp,
        attribution_available=EXCLUDED.attribution_available,
        residual_contribution_pp=EXCLUDED.residual_contribution_pp`,
		state.AsOfMS, state.Version, state.Status, frameComplete, frameStatus,
		windowAvailable, windowStatus, currentSharePct, previousSharePct,
		shareDeltaPP, attributionAvailable, residualContributionPP,
	)
	if err != nil {
		return fmt.Errorf("save realtime ten-second summary sample: %w", err)
	}
	for _, table := range []string{
		"realtime_flow_10s_asset_impacts", "realtime_flow_10s_exchange_impacts",
	} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE as_of_ms=$1", state.AsOfMS); err != nil {
			return fmt.Errorf("replace %s: %w", table, err)
		}
	}
	if !attributionAvailable {
		return nil
	}
	if len(attribution.Drivers) > 0 {
		ranks := make([]int32, 0, len(attribution.Drivers))
		assets := make([]string, 0, len(attribution.Drivers))
		contributions := make([]float64, 0, len(attribution.Drivers))
		bithumbChanges := make([]float64, 0, len(attribution.Drivers))
		competitorChanges := make([]float64, 0, len(attribution.Drivers))
		for index, driver := range attribution.Drivers {
			if strings.TrimSpace(driver.Asset) == "" {
				continue
			}
			ranks = append(ranks, int32(index+1))
			assets = append(assets, driver.Asset)
			contributions = append(contributions, driver.ContributionPP)
			bithumbChanges = append(bithumbChanges, driver.BithumbVolumeChangeKRW)
			competitorChanges = append(competitorChanges, driver.CompetitorVolumeChangeKRW)
		}
		if len(ranks) > 0 {
			_, err = tx.Exec(ctx, `INSERT INTO realtime_flow_10s_asset_impacts (
                as_of_ms, rank_no, asset, contribution_pp,
                bithumb_volume_change_krw, competitor_volume_change_krw
            ) SELECT $1, incoming.rank_no, incoming.asset, incoming.contribution_pp,
                incoming.bithumb_change, incoming.competitor_change
            FROM unnest($2::INTEGER[], $3::TEXT[], $4::DOUBLE PRECISION[],
                $5::DOUBLE PRECISION[], $6::DOUBLE PRECISION[])
            AS incoming(rank_no, asset, contribution_pp, bithumb_change, competitor_change)`,
				state.AsOfMS, ranks, assets, contributions, bithumbChanges, competitorChanges,
			)
			if err != nil {
				return fmt.Errorf("save realtime ten-second asset impacts: %w", err)
			}
		}
	}
	if len(attribution.ExchangeDrivers) > 0 {
		ranks := make([]int32, 0, len(attribution.ExchangeDrivers))
		exchanges := make([]string, 0, len(attribution.ExchangeDrivers))
		contributions := make([]float64, 0, len(attribution.ExchangeDrivers))
		for index, driver := range attribution.ExchangeDrivers {
			if strings.TrimSpace(driver.Exchange) == "" {
				continue
			}
			ranks = append(ranks, int32(index+1))
			exchanges = append(exchanges, driver.Exchange)
			contributions = append(contributions, driver.ContributionPP)
		}
		if len(ranks) > 0 {
			_, err = tx.Exec(ctx, `INSERT INTO realtime_flow_10s_exchange_impacts (
                as_of_ms, rank_no, exchange_name, contribution_pp
            ) SELECT $1, incoming.rank_no, incoming.exchange_name, incoming.contribution_pp
            FROM unnest($2::INTEGER[], $3::TEXT[], $4::DOUBLE PRECISION[])
            AS incoming(rank_no, exchange_name, contribution_pp)`,
				state.AsOfMS, ranks, exchanges, contributions,
			)
			if err != nil {
				return fmt.Errorf("save realtime ten-second exchange impacts: %w", err)
			}
		}
	}
	return nil
}

func replaceRealtimeFlowDetails(ctx context.Context, tx pgx.Tx, state marketanalysis.RealtimeFlowState) error {
	// Preserve statement order and the enclosing transaction while avoiding a
	// network round trip for every asset and exchange in a detailed signal.
	batch := &pgx.Batch{}
	for _, table := range []string{
		"realtime_flow_windows", "realtime_flow_asset_impacts",
		"realtime_flow_exchange_impacts", "realtime_flow_market_impacts",
	} {
		batch.Queue("DELETE FROM "+table+" WHERE as_of_ms=$1", state.AsOfMS)
	}
	for _, window := range state.Windows {
		batch.Queue(`INSERT INTO realtime_flow_windows (
            as_of_ms, duration_sec, status, current_share_pct, previous_share_pct,
            share_delta_pp, current_volume_krw, previous_volume_krw, threshold_pp
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			state.AsOfMS, window.DurationSec, window.Status,
			window.CurrentSharePct, window.PreviousSharePct, window.ShareDeltaPP,
			window.CurrentVolumeKRW, window.PreviousVolumeKRW, window.ThresholdPP,
		)
	}
	for _, driver := range state.Drivers {
		book, err := nullableRealtimeFlowJSON(driver.Orderbook)
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO realtime_flow_asset_impacts (
            as_of_ms, asset, rank_no, contribution_pp, bithumb_volume_change_krw,
            competitor_volume_change_krw, previous_bithumb_volume_krw,
            current_bithumb_volume_krw, previous_competitor_volume_krw,
            current_competitor_volume_krw, bithumb_buy_pct, top_competitor,
            top_competitor_volume_krw, orderbook_json
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			state.AsOfMS, driver.Asset, driver.Rank, driver.ContributionPP,
			driver.BithumbVolumeChangeKRW, driver.CompetitorVolumeChangeKRW,
			driver.PreviousBithumbVolumeKRW, driver.CurrentBithumbVolumeKRW,
			driver.PreviousCompetitorVolumeKRW, driver.CurrentCompetitorVolumeKRW,
			driver.BithumbBuyPct, driver.TopCompetitor, driver.TopCompetitorVolumeKRW, book,
		)
		for rank, impact := range driver.ExchangeImpacts {
			book, err := nullableRealtimeFlowJSON(impact.Orderbook)
			if err != nil {
				return err
			}
			batch.Queue(`INSERT INTO realtime_flow_market_impacts (
                as_of_ms, asset, exchange_name, asset_rank_no, exchange_rank_no,
                contribution_pp, volume_change_krw, previous_volume_krw,
                current_volume_krw, buy_pct, orderbook_json
            ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
				state.AsOfMS, driver.Asset, impact.Exchange, driver.Rank, rank+1,
				impact.ContributionPP, impact.VolumeChangeKRW, impact.PreviousVolumeKRW,
				impact.CurrentVolumeKRW, impact.BuyPct, book,
			)
		}
	}
	for _, driver := range state.ExchangeDrivers {
		batch.Queue(`INSERT INTO realtime_flow_exchange_impacts (
            as_of_ms, exchange_name, rank_no, contribution_pp, volume_change_krw,
            previous_volume_krw, current_volume_krw, leading_asset,
            leading_asset_contribution_pp
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			state.AsOfMS, driver.Exchange, driver.Rank, driver.ContributionPP,
			driver.VolumeChangeKRW, driver.PreviousVolumeKRW, driver.CurrentVolumeKRW,
			driver.LeadingAsset, driver.LeadingAssetContributionPP,
		)
	}
	results := tx.SendBatch(ctx, batch)
	defer results.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("save realtime flow detail statement %d: %w", i, err)
		}
	}
	return results.Close()
}

func nullableRealtimeFlowJSON(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode realtime orderbook evidence: %w", err)
	}
	return payload, nil
}

func (r *Repository) RealtimeFlowHistory(ctx context.Context, filter marketanalysis.RealtimeFlowHistoryFilter) ([]marketanalysis.RealtimeFlowState, error) {
	if filter.Limit < 1 {
		filter.Limit = 100
	}
	// Public history requests are capped at 1,000 by the HTTP handler. The
	// service may request up to one full day of ten-second rows for the shared
	// 24-hour summary.
	filter.Limit = min(filter.Limit, 9_000)
	var query strings.Builder
	query.WriteString(`SELECT observation.payload_json
        FROM realtime_flow_observations observation
        WHERE observation.contract_version=$1`)
	args := []any{marketanalysis.RealtimeFlowVersion}
	add := func(clause string, value any) {
		args = append(args, value)
		query.WriteString(fmt.Sprintf(clause, len(args)))
	}
	if filter.FromMS > 0 {
		add(" AND observation.as_of_ms >= $%d", filter.FromMS)
	}
	if filter.ToMS > 0 {
		add(" AND observation.as_of_ms <= $%d", filter.ToMS)
	}
	if status := strings.ToUpper(strings.TrimSpace(filter.Status)); status != "" {
		add(" AND observation.status = $%d", status)
	}
	if asset := strings.ToUpper(strings.TrimSpace(filter.Asset)); asset != "" {
		add(` AND EXISTS (SELECT 1 FROM realtime_flow_asset_impacts asset
            WHERE asset.as_of_ms=observation.as_of_ms AND asset.asset=$%d)`, asset)
	}
	if exchange := strings.ToLower(strings.TrimSpace(filter.Exchange)); exchange != "" {
		add(` AND EXISTS (SELECT 1 FROM realtime_flow_market_impacts market
            WHERE market.as_of_ms=observation.as_of_ms AND market.exchange_name=$%d)`, exchange)
	}
	args = append(args, filter.Limit)
	query.WriteString(fmt.Sprintf(" ORDER BY observation.as_of_ms DESC LIMIT $%d", len(args)))
	rows, err := r.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query realtime flow history: %w", err)
	}
	defer rows.Close()
	result := make([]marketanalysis.RealtimeFlowState, 0, filter.Limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan realtime flow history: %w", err)
		}
		var state marketanalysis.RealtimeFlowState
		if err := json.Unmarshal(payload, &state); err != nil {
			return nil, fmt.Errorf("decode realtime flow history: %w", err)
		}
		result = append(result, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read realtime flow history: %w", err)
	}
	return result, nil
}

// RealtimeFlowSummaryHistory returns only the exact fields consumed by the
// rolling 24-hour summary. The full payload_json remains the evidence source
// for public detail/history reads and official movement checkpoints.
func (r *Repository) RealtimeFlowSummaryHistory(ctx context.Context, fromMS, toMS int64) ([]marketanalysis.RealtimeFlowState, error) {
	if toMS <= fromMS {
		return []marketanalysis.RealtimeFlowState{}, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT
        as_of_ms, contract_version, status, frame_complete, frame_status,
        window_available, window_status, current_share_pct, previous_share_pct,
        share_delta_pp, attribution_available, residual_contribution_pp
    FROM realtime_flow_10s_samples
    WHERE contract_version=$1 AND as_of_ms>$2 AND as_of_ms<=$3
        AND MOD(as_of_ms, $4)=0
    ORDER BY as_of_ms ASC`, marketanalysis.RealtimeFlowVersion, fromMS, toMS,
		marketanalysis.RealtimeFlowSummarySampleSec*1_000)
	if err != nil {
		return nil, fmt.Errorf("query realtime ten-second summary samples: %w", err)
	}
	defer rows.Close()
	result := make([]marketanalysis.RealtimeFlowState, 0, int((toMS-fromMS)/10_000)+1)
	indexes := make(map[int64]int)
	for rows.Next() {
		var (
			state                marketanalysis.RealtimeFlowState
			frameComplete        bool
			frameStatus          string
			windowAvailable      bool
			windowStatus         string
			currentSharePct      *float64
			previousSharePct     *float64
			shareDeltaPP         *float64
			attributionAvailable bool
			residualContribution *float64
		)
		if err := rows.Scan(
			&state.AsOfMS, &state.Version, &state.Status, &frameComplete, &frameStatus,
			&windowAvailable, &windowStatus, &currentSharePct, &previousSharePct,
			&shareDeltaPP, &attributionAvailable, &residualContribution,
		); err != nil {
			return nil, fmt.Errorf("scan realtime ten-second summary sample: %w", err)
		}
		state.DataQuality = map[string]any{
			"complete_frame": frameComplete,
			"frame_status":   frameStatus,
		}
		state.Windows = []marketanalysis.RealtimeFlowWindow{}
		if windowAvailable {
			state.Windows = append(state.Windows, marketanalysis.RealtimeFlowWindow{
				DurationSec: marketanalysis.RealtimeFlowSummarySampleSec,
				Status:      windowStatus, CurrentSharePct: currentSharePct,
				PreviousSharePct: previousSharePct, ShareDeltaPP: shareDeltaPP,
			})
		}
		if attributionAvailable {
			state.TenSecondAttribution = &marketanalysis.RealtimeFlowAttribution{
				WindowSec:              marketanalysis.RealtimeFlowSummarySampleSec,
				ResidualContributionPP: residualContribution,
				Drivers:                []marketanalysis.RealtimeFlowDriver{},
				ExchangeDrivers:        []marketanalysis.RealtimeFlowExchangeDriver{},
			}
		}
		indexes[state.AsOfMS] = len(result)
		result = append(result, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read realtime ten-second summary samples: %w", err)
	}
	if len(result) == 0 {
		return result, nil
	}
	if err := r.attachRealtimeFlowTenSecondAssetImpacts(ctx, fromMS, toMS, indexes, result); err != nil {
		return nil, err
	}
	if err := r.attachRealtimeFlowTenSecondExchangeImpacts(ctx, fromMS, toMS, indexes, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) attachRealtimeFlowTenSecondAssetImpacts(
	ctx context.Context,
	fromMS, toMS int64,
	indexes map[int64]int,
	states []marketanalysis.RealtimeFlowState,
) error {
	rows, err := r.pool.Query(ctx, `SELECT impact.as_of_ms, impact.rank_no, impact.asset,
        impact.contribution_pp, impact.bithumb_volume_change_krw,
        impact.competitor_volume_change_krw
    FROM realtime_flow_10s_asset_impacts impact
    JOIN realtime_flow_10s_samples sample ON sample.as_of_ms=impact.as_of_ms
    WHERE sample.contract_version=$1 AND impact.as_of_ms>$2 AND impact.as_of_ms<=$3
        AND MOD(impact.as_of_ms, $4)=0
    ORDER BY impact.as_of_ms ASC, impact.rank_no ASC`,
		marketanalysis.RealtimeFlowVersion, fromMS, toMS,
		marketanalysis.RealtimeFlowSummarySampleSec*1_000)
	if err != nil {
		return fmt.Errorf("query realtime ten-second asset impacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var asOfMS int64
		var driver marketanalysis.RealtimeFlowDriver
		if err := rows.Scan(
			&asOfMS, &driver.Rank, &driver.Asset, &driver.ContributionPP,
			&driver.BithumbVolumeChangeKRW, &driver.CompetitorVolumeChangeKRW,
		); err != nil {
			return fmt.Errorf("scan realtime ten-second asset impact: %w", err)
		}
		index, ok := indexes[asOfMS]
		if !ok || states[index].TenSecondAttribution == nil {
			continue
		}
		states[index].TenSecondAttribution.Drivers = append(
			states[index].TenSecondAttribution.Drivers, driver,
		)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read realtime ten-second asset impacts: %w", err)
	}
	return nil
}

func (r *Repository) attachRealtimeFlowTenSecondExchangeImpacts(
	ctx context.Context,
	fromMS, toMS int64,
	indexes map[int64]int,
	states []marketanalysis.RealtimeFlowState,
) error {
	rows, err := r.pool.Query(ctx, `SELECT impact.as_of_ms, impact.rank_no,
        impact.exchange_name, impact.contribution_pp
    FROM realtime_flow_10s_exchange_impacts impact
    JOIN realtime_flow_10s_samples sample ON sample.as_of_ms=impact.as_of_ms
    WHERE sample.contract_version=$1 AND impact.as_of_ms>$2 AND impact.as_of_ms<=$3
        AND MOD(impact.as_of_ms, $4)=0
    ORDER BY impact.as_of_ms ASC, impact.rank_no ASC`,
		marketanalysis.RealtimeFlowVersion, fromMS, toMS,
		marketanalysis.RealtimeFlowSummarySampleSec*1_000)
	if err != nil {
		return fmt.Errorf("query realtime ten-second exchange impacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var asOfMS int64
		var driver marketanalysis.RealtimeFlowExchangeDriver
		if err := rows.Scan(&asOfMS, &driver.Rank, &driver.Exchange, &driver.ContributionPP); err != nil {
			return fmt.Errorf("scan realtime ten-second exchange impact: %w", err)
		}
		index, ok := indexes[asOfMS]
		if !ok || states[index].TenSecondAttribution == nil {
			continue
		}
		states[index].TenSecondAttribution.ExchangeDrivers = append(
			states[index].TenSecondAttribution.ExchangeDrivers, driver,
		)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read realtime ten-second exchange impacts: %w", err)
	}
	return nil
}
