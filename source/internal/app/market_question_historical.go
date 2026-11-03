package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/store/postgres"
)

const historicalQuestionBaseSec = int64(10)
const historicalQuestionMaxRangeSec = int64(31 * 24 * 60 * 60)

type historicalQuestionTotal struct {
	VolumeKRW     float64  `json:"volume_krw"`
	TradeCount    int64    `json:"trade_count"`
	BuyVolumeKRW  float64  `json:"buy_volume_krw"`
	SellVolumeKRW float64  `json:"sell_volume_krw"`
	BuyPct        *float64 `json:"buy_pct"`
	Unresolved    int64    `json:"unresolved_count"`
}

type historicalQuestionExchange struct {
	Exchange string `json:"exchange"`
	historicalQuestionTotal
	SharePct *float64 `json:"share_pct"`
}

type historicalQuestionAsset struct {
	Asset             string   `json:"asset"`
	DomesticVolumeKRW float64  `json:"domestic_volume_krw"`
	BithumbVolumeKRW  float64  `json:"bithumb_volume_krw"`
	DomesticSharePct  *float64 `json:"domestic_share_pct"`
	BithumbCapturePct *float64 `json:"bithumb_capture_pct"`
	TradeCount        int64    `json:"trade_count"`
}

type historicalQuestionPathPoint struct {
	StartMS           int64           `json:"start_ms"`
	EndMS             int64           `json:"end_ms"`
	DomesticVolumeKRW float64         `json:"domestic_volume_krw"`
	BithumbVolumeKRW  float64         `json:"bithumb_volume_krw"`
	BithumbSharePct   *float64        `json:"bithumb_share_pct"`
	TargetVolumeKRW   float64         `json:"target_volume_krw"`
	TargetSharePct    *float64        `json:"target_share_pct"`
	TradeCount        int64           `json:"trade_count"`
	Coverage          coverage.Result `json:"coverage"`
}

type historicalQuestionTargetAsset struct {
	Asset             string   `json:"asset"`
	TargetVolumeKRW   float64  `json:"target_volume_krw"`
	DomesticVolumeKRW float64  `json:"domestic_volume_krw"`
	TargetCapturePct  *float64 `json:"target_capture_pct"`
}

type historicalQuestionSignalExtreme struct {
	AsOfMS           int64                                       `json:"as_of_ms"`
	PreviousSharePct *float64                                    `json:"previous_share_pct"`
	CurrentSharePct  *float64                                    `json:"current_share_pct"`
	ShareDeltaPP     *float64                                    `json:"share_delta_pp"`
	CurrentVolumeKRW float64                                     `json:"current_volume_krw"`
	AssetDrivers     []marketanalysis.RealtimeFlowDriver         `json:"asset_drivers"`
	ExchangeDrivers  []marketanalysis.RealtimeFlowExchangeDriver `json:"exchange_drivers"`
}

type historicalQuestionSignals struct {
	Status               string                           `json:"status"`
	StoredRows           int                              `json:"stored_rows"`
	QualityRows          int                              `json:"quality_rows"`
	QualityPct           *float64                         `json:"quality_pct"`
	WeightedMeanSharePct *float64                         `json:"weighted_mean_share_pct"`
	SimpleMeanSharePct   *float64                         `json:"simple_mean_share_pct"`
	MedianSharePct       *float64                         `json:"median_share_pct"`
	P10SharePct          *float64                         `json:"p10_share_pct"`
	P90SharePct          *float64                         `json:"p90_share_pct"`
	MinSharePct          *float64                         `json:"min_share_pct"`
	MaxSharePct          *float64                         `json:"max_share_pct"`
	RisingCount          int                              `json:"rising_count"`
	FallingCount         int                              `json:"falling_count"`
	FlatCount            int                              `json:"flat_count"`
	Statuses             map[string]int                   `json:"statuses"`
	AlertRisingCount     int                              `json:"alert_rising_count"`
	AlertFallingCount    int                              `json:"alert_falling_count"`
	LargestDrop          *historicalQuestionSignalExtreme `json:"largest_drop,omitempty"`
}

type historicalQuestionEvidence struct {
	Status             string                               `json:"status"`
	Reason             string                               `json:"reason,omitempty"`
	Source             string                               `json:"source"`
	Semantics          string                               `json:"semantics"`
	StartMS            int64                                `json:"start_ms"`
	EndMS              int64                                `json:"end_ms"`
	DurationSec        int64                                `json:"duration_sec"`
	RetrievalPlan      historicalQuestionRetrievalPlan      `json:"retrieval_plan"`
	AggregationVersion uint64                               `json:"aggregation_version"`
	Coverage           coverage.Result                      `json:"coverage"`
	Domestic           historicalQuestionTotal              `json:"domestic"`
	Bithumb            historicalQuestionTotal              `json:"bithumb"`
	BithumbSharePct    *float64                             `json:"bithumb_share_pct"`
	TargetExchange     string                               `json:"target_exchange"`
	Target             historicalQuestionTotal              `json:"target"`
	TargetSharePct     *float64                             `json:"target_share_pct"`
	TargetTopAssets    []historicalQuestionTargetAsset      `json:"target_top_assets"`
	BackfillEvidence   []postgres.OfficialCandle10mEvidence `json:"official_candle_10m_backfill"`
	Aligned1hEvidence  []postgres.OfficialAligned1hEvidence `json:"official_aligned_1h_evidence"`
	Exchanges          []historicalQuestionExchange         `json:"exchanges"`
	TopAssets          []historicalQuestionAsset            `json:"top_assets"`
	PathBucketSec      int64                                `json:"path_bucket_sec"`
	Path               []historicalQuestionPathPoint        `json:"path"`
	HighToLow          marketanalysis.Attribution           `json:"high_to_low_attribution"`
	HighPoint          *historicalQuestionPathPoint         `json:"high_point,omitempty"`
	LowPoint           *historicalQuestionPathPoint         `json:"low_point,omitempty"`
	FirstVsLastHour    marketanalysis.Attribution           `json:"first_vs_last_hour"`
	FirstHourStartMS   int64                                `json:"first_hour_start_ms"`
	FirstHourEndMS     int64                                `json:"first_hour_end_ms"`
	LastHourStartMS    int64                                `json:"last_hour_start_ms"`
	LastHourEndMS      int64                                `json:"last_hour_end_ms"`
	Signals10s         historicalQuestionSignals            `json:"signals_10s"`
}

type historicalQuestionRetrievalPlan struct {
	AvailablePathSec     []int64 `json:"available_path_sec"`
	SelectedPathSec      int64   `json:"selected_path_sec"`
	SelectionReason      string  `json:"selection_reason"`
	AnalysisDepth        string  `json:"analysis_depth"`
	IncludeAttribution   bool    `json:"include_attribution"`
	IncludeSignals10s    bool    `json:"include_signals_10s"`
	IncludeAssetTable    bool    `json:"include_asset_table"`
	IncludeExchangeTable bool    `json:"include_exchange_table"`
	IncludePath          bool    `json:"include_path"`
}

func (s *Service) marketQuestionHistoricalEvidence(ctx context.Context, query marketanalysis.MarketQuestionQuery) (historicalQuestionEvidence, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.MarketQuestionHistoricalEvidence(ctx, query)
	}
	start, end := query.TargetStartMS/1_000, query.TargetEndMS/1_000
	targetExchange := strings.ToLower(strings.TrimSpace(query.Exchange))
	if targetExchange == "" {
		targetExchange = string(domain.ExchangeBithumb)
	}
	result := historicalQuestionEvidence{
		Status: "UNAVAILABLE", Reason: "HISTORICAL_RANGE_NOT_RETAINED", Source: "WEBSOCKET_1S_BUCKETS",
		Semantics: "EXACT_USER_KST_RANGE_INCLUSIVE_SECONDS", StartMS: query.TargetStartMS,
		EndMS: query.TargetEndMS, DurationSec: end - start + 1, Exchanges: []historicalQuestionExchange{},
		TargetExchange: targetExchange, TopAssets: []historicalQuestionAsset{},
		TargetTopAssets: []historicalQuestionTargetAsset{}, Path: []historicalQuestionPathPoint{},
		BackfillEvidence:  []postgres.OfficialCandle10mEvidence{},
		Aligned1hEvidence: []postgres.OfficialAligned1hEvidence{},
		Signals10s:        historicalQuestionSignals{Status: "UNAVAILABLE", Statuses: map[string]int{}},
	}
	if s.postgresRepository != nil {
		backfill, backfillErr := s.postgresRepository.OfficialCandle10mEvidenceInRange(ctx, query.TargetStartMS, query.TargetEndMS)
		if backfillErr != nil {
			return result, backfillErr
		}
		result.BackfillEvidence = backfill
		aligned, alignedErr := s.postgresRepository.OfficialAligned1hEvidenceInRange(ctx, query.TargetStartMS, query.TargetEndMS)
		if alignedErr != nil {
			return result, alignedErr
		}
		result.Aligned1hEvidence = aligned
	}
	if s.runtime == nil || end < start || end-start+1 > historicalQuestionMaxRangeSec {
		return result, nil
	}
	result.RetrievalPlan = historicalQuestionPlan(query, result.DurationSec)
	result.PathBucketSec = result.RetrievalPlan.SelectedPathSec
	ranges := [][2]int64{}
	if result.RetrievalPlan.IncludePath {
		ranges = historicalQuestionPathRanges(start, end, result.PathBucketSec)
	}
	useFixedPathRead := len(ranges) > 0 && result.DurationSec > marketQuestionMaxWSIntervalSec &&
		s.postgresRepository != nil && end < time.Now().Unix()-2
	intervals := make([]aggregate.IntervalRequest, 0, len(ranges)+3)
	coverages := make([]coverageInterval, 0, len(ranges)+3)
	add := func(from, to int64) {
		intervals = append(intervals, aggregate.IntervalRequest{Start: domain.UnixSecond(from - 1), End: domain.UnixSecond(to)})
		coverages = append(coverages, coverageInterval{start: from, end: to})
	}
	add(start, end)
	if !useFixedPathRead {
		for _, item := range ranges {
			add(item[0], item[1])
		}
	}
	firstHourEnd := min(end, start+3_599)
	lastHourStart := max(start, end-3_599)
	add(start, firstHourEnd)
	add(lastHourStart, end)
	read, err := s.marketQuestionHistoricalRead(ctx, intervals, coverages, end)
	if err != nil {
		return result, err
	}
	if len(read.intervals) != len(intervals) || len(read.coverage) != len(coverages) || !read.intervals[0].OK {
		return result, nil
	}
	result.AggregationVersion = read.version
	result.Coverage = read.coverage[0]
	fullRows := analysisRows(filterAnalysisValues(read.intervals[0].Values, query.MarketScope))
	result.Domestic, result.Bithumb, result.BithumbSharePct, result.Exchanges, result.TopAssets = historicalQuestionSummaries(fullRows)
	result.Target, result.TargetSharePct = historicalQuestionTargetSummary(result.Exchanges, targetExchange)
	if !result.RetrievalPlan.IncludeExchangeTable {
		result.Exchanges = []historicalQuestionExchange{}
	}
	if !result.RetrievalPlan.IncludeAssetTable {
		result.TopAssets = []historicalQuestionAsset{}
	}
	if result.Domestic.VolumeKRW <= 0 {
		result.Reason = "NO_OBSERVED_TRADES_IN_RANGE"
		return result, nil
	}
	result.Status, result.Reason = "PARTIAL", "OBSERVATION_COVERAGE_INCOMPLETE"
	if result.Coverage.WindowComplete && result.Coverage.CoverageSec >= result.DurationSec {
		result.Status, result.Reason = "CONFIRMED", ""
	}
	var pathIntervals []aggregate.IntervalSnapshotResult
	var pathCoverage []coverage.Result
	if useFixedPathRead {
		fixed, fixedErr := s.postgresRepository.HistoricalFixedBuckets(ctx, start, end, result.PathBucketSec)
		if fixedErr != nil {
			return result, fixedErr
		}
		if len(fixed.Intervals) != len(ranges) || len(fixed.Coverage) != len(ranges) {
			return result, errors.New("historical PostgreSQL fixed-bucket result is incomplete")
		}
		pathIntervals, pathCoverage = fixed.Intervals, fixed.Coverage
	} else {
		pathIntervals = read.intervals[1 : 1+len(ranges)]
		pathCoverage = read.coverage[1 : 1+len(ranges)]
	}
	pathRows := make([][]marketanalysis.Row, 0, len(ranges))
	for index, item := range ranges {
		rows := analysisRows(filterAnalysisValues(pathIntervals[index].Values, query.MarketScope))
		pathRows = append(pathRows, rows)
		domestic, bithumb, share, exchanges, _ := historicalQuestionSummaries(rows)
		target, targetShare := historicalQuestionTargetSummary(exchanges, targetExchange)
		result.Path = append(result.Path, historicalQuestionPathPoint{
			StartMS: item[0] * 1_000, EndMS: item[1] * 1_000,
			DomesticVolumeKRW: domestic.VolumeKRW, BithumbVolumeKRW: bithumb.VolumeKRW,
			BithumbSharePct: share, TargetVolumeKRW: target.VolumeKRW,
			TargetSharePct: targetShare, TradeCount: domestic.TradeCount, Coverage: pathCoverage[index],
		})
	}
	highIndex, lowIndex := historicalQuestionPathExtremes(result.Path)
	if result.RetrievalPlan.IncludeAttribution && highIndex >= 0 && lowIndex >= 0 {
		result.HighPoint, result.LowPoint = &result.Path[highIndex], &result.Path[lowIndex]
		complete := historicalQuestionPointComplete(result.Path[highIndex]) && historicalQuestionPointComplete(result.Path[lowIndex])
		result.HighToLow = historicalQuestionTargetAttribution(pathRows[lowIndex], pathRows[highIndex], targetExchange, complete)
		result.TargetTopAssets = historicalQuestionTargetAssets(pathRows[highIndex], targetExchange)
	}
	firstIndex, lastIndex := len(ranges)+1, len(ranges)+2
	if useFixedPathRead {
		firstIndex, lastIndex = 1, 2
	}
	firstRows := analysisRows(filterAnalysisValues(read.intervals[firstIndex].Values, query.MarketScope))
	lastRows := analysisRows(filterAnalysisValues(read.intervals[lastIndex].Values, query.MarketScope))
	result.FirstHourStartMS, result.FirstHourEndMS = start*1_000, firstHourEnd*1_000
	result.LastHourStartMS, result.LastHourEndMS = lastHourStart*1_000, end*1_000
	result.FirstVsLastHour = marketanalysis.Attribution{Status: "UNAVAILABLE", UnavailableReason: "RANGE_SHORTER_THAN_TWO_HOURS"}
	if result.DurationSec >= 2*3_600 {
		result.FirstVsLastHour = historicalQuestionTargetAttribution(lastRows, firstRows, targetExchange,
			read.coverage[firstIndex].WindowComplete && read.coverage[lastIndex].WindowComplete)
	}
	if result.RetrievalPlan.IncludeSignals10s && s.repository != nil {
		history, historyErr := s.repository.RealtimeFlowHistory(ctx, marketanalysis.RealtimeFlowHistoryFilter{
			FromMS: query.TargetStartMS, ToMS: query.TargetEndMS, Limit: 9_000,
		})
		if historyErr == nil {
			result.Signals10s = historicalQuestionSignalSummary(history)
		}
	}
	return result, nil
}

// Explicit past ranges are read from PostgreSQL rather than depending on the
// live aggregator's bounded 48-hour memory horizon. Very recent ranges keep
// the runtime barrier so an accepted trade cannot be queried before its
// persistence flush completes.
func (s *Service) marketQuestionHistoricalRead(
	ctx context.Context,
	intervals []aggregate.IntervalRequest,
	coverages []coverageInterval,
	requestedEnd int64,
) (atomicReadResult, error) {
	if s.postgresRepository == nil || requestedEnd >= time.Now().Unix()-2 {
		return s.runtime.atomicRead(ctx, intervals, coverages)
	}
	persisted, err := s.postgresRepository.HistoricalIntervals(ctx, intervals)
	if err != nil {
		return atomicReadResult{}, err
	}
	if len(persisted.Coverage) != len(coverages) {
		return atomicReadResult{}, errors.New("historical PostgreSQL coverage result is incomplete")
	}
	return atomicReadResult{
		intervals: persisted.Intervals,
		coverage:  persisted.Coverage,
		caughtUp:  true,
	}, nil
}

func historicalQuestionPlan(query marketanalysis.MarketQuestionQuery, _ int64) historicalQuestionRetrievalPlan {
	available := []int64{10, 30, 60, 300, 600, 1_800, 3_600}
	requested := map[string]bool{}
	for _, value := range query.RequestedExplanation {
		requested[value] = true
	}
	plan := historicalQuestionRetrievalPlan{
		AvailablePathSec: available, AnalysisDepth: query.AnalysisDepth,
		SelectedPathSec:      query.RequestedPathSec,
		SelectionReason:      "HERMES_SELECTED_RETRIEVAL_PLAN",
		IncludeAttribution:   requested["DRIVERS"] || requested["EXCHANGES"],
		IncludeSignals10s:    requested["HISTORICAL_SIGNALS"],
		IncludeAssetTable:    requested["DRIVERS"],
		IncludeExchangeTable: requested["EXCHANGES"],
		IncludePath:          requested["PATH"],
	}
	if !plan.IncludePath {
		plan.SelectedPathSec = 0
	} else if plan.SelectedPathSec == 0 {
		// The deterministic parser is only an infrastructure fallback. When it
		// recognizes that a path was requested but no explicit display interval
		// was given, expose the stored 10-second base interval instead of leaking
		// the internal zero sentinel into the report.
		plan.SelectedPathSec = historicalQuestionBaseSec
		plan.SelectionReason = "FALLBACK_DEFAULTED_TO_STORED_BASE_INTERVAL"
	}
	return plan
}

func historicalQuestionTargetSummary(
	exchanges []historicalQuestionExchange,
	target string,
) (historicalQuestionTotal, *float64) {
	for _, item := range exchanges {
		if strings.EqualFold(item.Exchange, target) {
			return item.historicalQuestionTotal, item.SharePct
		}
	}
	return historicalQuestionTotal{}, nil
}

func historicalQuestionTargetAssets(rows []marketanalysis.Row, target string) []historicalQuestionTargetAsset {
	values := map[string]*historicalQuestionTargetAsset{}
	for _, row := range rows {
		asset := strings.ToUpper(strings.TrimSpace(row.Base))
		item := values[asset]
		if item == nil {
			item = &historicalQuestionTargetAsset{Asset: asset}
			values[asset] = item
		}
		item.DomesticVolumeKRW += row.VolumeKRW
		if strings.EqualFold(string(row.Exchange), target) {
			item.TargetVolumeKRW += row.VolumeKRW
		}
	}
	result := make([]historicalQuestionTargetAsset, 0, len(values))
	for _, item := range values {
		if item.TargetVolumeKRW <= 0 {
			continue
		}
		item.TargetCapturePct = historicalQuestionPercent(item.TargetVolumeKRW, item.DomesticVolumeKRW)
		result = append(result, *item)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].TargetVolumeKRW > result[j].TargetVolumeKRW })
	if len(result) > 15 {
		result = result[:15]
	}
	return result
}

func historicalQuestionTargetAttribution(
	currentRows, previousRows []marketanalysis.Row,
	target string,
	complete bool,
) marketanalysis.Attribution {
	if target == "" || target == string(domain.ExchangeBithumb) {
		return marketanalysis.ChangeAttribution(currentRows, previousRows, complete)
	}
	targetExchange := domain.Exchange(target)
	remap := func(rows []marketanalysis.Row) []marketanalysis.Row {
		result := append([]marketanalysis.Row(nil), rows...)
		for index := range result {
			switch result[index].Exchange {
			case targetExchange:
				result[index].Exchange = domain.ExchangeBithumb
			case domain.ExchangeBithumb:
				result[index].Exchange = targetExchange
			}
		}
		return result
	}
	return marketanalysis.ChangeAttribution(remap(currentRows), remap(previousRows), complete)
}

func buildHistoricalMarketQuestionEvidence(
	query marketanalysis.MarketQuestionQuery,
	e historicalQuestionEvidence,
	planEvidence map[string]any,
	fallback marketanalysis.MarketQuestionAnswer,
) map[string]any {
	return map[string]any{
		"contract": "GROUNDED_MARKET_QUESTION_V1",
		"as_of_ms": e.EndMS,
		"query":    query,
		"retrieval_policy": map[string]any{
			"strategy": marketQuestionRetrievalStrategy, "persistent_store": "POSTGRESQL",
			"model_generated_sql": false, "raw_database_rows_sent_to_model": false,
		},
		"retrieval_plan":   planEvidence,
		"historical_range": historicalQuestionModelProjection(e),
		"verified_display_briefing": map[string]any{
			"answer": fallback.Answer, "key_points": fallback.KeyPoints, "caveat": fallback.Caveat,
			"instruction": "Numeric values and units in this briefing are server-formatted and safe to quote verbatim.",
		},
	}
}

func historicalQuestionModelProjection(e historicalQuestionEvidence) map[string]any {
	highToLow := map[string]any{
		"status": e.HighToLow.Status, "unavailable_reason": e.HighToLow.UnavailableReason,
		"previous_share_pct": e.HighToLow.PreviousSharePercent, "current_share_pct": e.HighToLow.CurrentSharePercent,
		"share_change_pp":     e.HighToLow.ShareChangePP,
		"previous_volume_krw": e.HighToLow.PreviousVolumeKRW, "current_volume_krw": e.HighToLow.CurrentVolumeKRW,
		"previous_bithumb_volume_krw": e.HighToLow.PreviousBithumbVolumeKRW,
		"current_bithumb_volume_krw":  e.HighToLow.CurrentBithumbVolumeKRW,
		"bithumb_direct_effect_pp":    e.HighToLow.BithumbDirectEffectPP,
		"competitor_effect_pp":        e.HighToLow.CompetitorEffectPP,
		"primary_driver":              e.HighToLow.PrimaryDriver,
		"exchanges":                   e.HighToLow.Exchanges[:min(5, len(e.HighToLow.Exchanges))],
		"assets":                      e.HighToLow.Assets[:min(15, len(e.HighToLow.Assets))],
		"contribution_method":         e.HighToLow.ContributionMethod,
	}
	firstLast := map[string]any{
		"status": e.FirstVsLastHour.Status, "unavailable_reason": e.FirstVsLastHour.UnavailableReason,
		"previous_share_pct": e.FirstVsLastHour.PreviousSharePercent, "current_share_pct": e.FirstVsLastHour.CurrentSharePercent,
		"share_change_pp":              e.FirstVsLastHour.ShareChangePP,
		"market_volume_change_krw":     e.FirstVsLastHour.MarketVolumeChangeKRW,
		"bithumb_volume_change_krw":    e.FirstVsLastHour.BithumbVolumeChangeKRW,
		"competitor_volume_change_krw": e.FirstVsLastHour.CompetitorVolumeChangeKRW,
		"start_ms":                     e.FirstHourStartMS, "start_end_ms": e.FirstHourEndMS,
		"last_start_ms": e.LastHourStartMS, "end_ms": e.LastHourEndMS,
	}
	return map[string]any{
		"status": e.Status, "reason": e.Reason, "source": e.Source, "semantics": e.Semantics,
		"start_ms": e.StartMS, "end_ms": e.EndMS, "duration_sec": e.DurationSec,
		"aggregation_version": e.AggregationVersion, "coverage": e.Coverage,
		"retrieval_plan": e.RetrievalPlan,
		"domestic":       e.Domestic, "bithumb": e.Bithumb, "bithumb_share_pct": e.BithumbSharePct,
		"target_exchange": e.TargetExchange, "target": e.Target, "target_share_pct": e.TargetSharePct,
		"target_top_assets": e.TargetTopAssets, "official_candle_10m_backfill": e.BackfillEvidence,
		"official_aligned_1h_evidence": e.Aligned1hEvidence,
		"exchanges":                    e.Exchanges, "top_assets": e.TopAssets,
		"path_bucket_sec": e.PathBucketSec, "path": e.Path,
		"high_point": e.HighPoint, "low_point": e.LowPoint, "high_to_low_attribution": highToLow,
		"first_vs_last_hour": firstLast, "signals_10s": e.Signals10s,
	}
}

func historicalQuestionPathRanges(start, end, stepSec int64) [][2]int64 {
	if stepSec < historicalQuestionBaseSec {
		stepSec = historicalQuestionBaseSec
	}
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

func historicalQuestionSummaries(rows []marketanalysis.Row) (historicalQuestionTotal, historicalQuestionTotal, *float64, []historicalQuestionExchange, []historicalQuestionAsset) {
	var domestic, bithumb historicalQuestionTotal
	exchanges := map[string]*historicalQuestionExchange{}
	assets := map[string]*historicalQuestionAsset{}
	for _, row := range rows {
		domestic.VolumeKRW += row.VolumeKRW
		domestic.TradeCount += row.TradeCount
		domestic.BuyVolumeKRW += row.BuyVolumeKRW
		domestic.SellVolumeKRW += row.SellVolumeKRW
		domestic.Unresolved += row.UnresolvedFXCount
		exchange := strings.ToLower(string(row.Exchange))
		item := exchanges[exchange]
		if item == nil {
			item = &historicalQuestionExchange{Exchange: exchange}
			exchanges[exchange] = item
		}
		item.VolumeKRW += row.VolumeKRW
		item.TradeCount += row.TradeCount
		item.BuyVolumeKRW += row.BuyVolumeKRW
		item.SellVolumeKRW += row.SellVolumeKRW
		item.Unresolved += row.UnresolvedFXCount
		asset := strings.ToUpper(row.Base)
		assetItem := assets[asset]
		if assetItem == nil {
			assetItem = &historicalQuestionAsset{Asset: asset}
			assets[asset] = assetItem
		}
		assetItem.DomesticVolumeKRW += row.VolumeKRW
		assetItem.TradeCount += row.TradeCount
		if row.Exchange == domain.ExchangeBithumb {
			bithumb.VolumeKRW += row.VolumeKRW
			bithumb.TradeCount += row.TradeCount
			bithumb.BuyVolumeKRW += row.BuyVolumeKRW
			bithumb.SellVolumeKRW += row.SellVolumeKRW
			bithumb.Unresolved += row.UnresolvedFXCount
			assetItem.BithumbVolumeKRW += row.VolumeKRW
		}
	}
	domestic.BuyPct = historicalQuestionPercent(domestic.BuyVolumeKRW, domestic.BuyVolumeKRW+domestic.SellVolumeKRW)
	bithumb.BuyPct = historicalQuestionPercent(bithumb.BuyVolumeKRW, bithumb.BuyVolumeKRW+bithumb.SellVolumeKRW)
	share := historicalQuestionPercent(bithumb.VolumeKRW, domestic.VolumeKRW)
	exchangeRows := make([]historicalQuestionExchange, 0, len(exchanges))
	for _, item := range exchanges {
		item.BuyPct = historicalQuestionPercent(item.BuyVolumeKRW, item.BuyVolumeKRW+item.SellVolumeKRW)
		item.SharePct = historicalQuestionPercent(item.VolumeKRW, domestic.VolumeKRW)
		exchangeRows = append(exchangeRows, *item)
	}
	sort.SliceStable(exchangeRows, func(i, j int) bool { return exchangeRows[i].VolumeKRW > exchangeRows[j].VolumeKRW })
	assetRows := make([]historicalQuestionAsset, 0, len(assets))
	for _, item := range assets {
		item.DomesticSharePct = historicalQuestionPercent(item.DomesticVolumeKRW, domestic.VolumeKRW)
		item.BithumbCapturePct = historicalQuestionPercent(item.BithumbVolumeKRW, item.DomesticVolumeKRW)
		assetRows = append(assetRows, *item)
	}
	sort.SliceStable(assetRows, func(i, j int) bool { return assetRows[i].DomesticVolumeKRW > assetRows[j].DomesticVolumeKRW })
	if len(assetRows) > 15 {
		assetRows = assetRows[:15]
	}
	return domestic, bithumb, share, exchangeRows, assetRows
}

func historicalQuestionPercent(numerator, denominator float64) *float64 {
	if denominator <= 0 || math.IsNaN(numerator) || math.IsInf(numerator, 0) {
		return nil
	}
	value := numerator / denominator * 100
	return &value
}

func historicalQuestionPathExtremes(points []historicalQuestionPathPoint) (int, int) {
	high, low := -1, -1
	for index := range points {
		if points[index].TargetSharePct == nil || !historicalQuestionPointComplete(points[index]) {
			continue
		}
		if high < 0 || *points[index].TargetSharePct > *points[high].TargetSharePct {
			high = index
		}
		if low < 0 || *points[index].TargetSharePct < *points[low].TargetSharePct {
			low = index
		}
	}
	return high, low
}

func historicalQuestionPointComplete(point historicalQuestionPathPoint) bool {
	required := (point.EndMS-point.StartMS)/1_000 + 1
	return point.Coverage.WindowComplete && point.Coverage.CoverageSec >= required
}

func historicalQuestionSignalSummary(items []marketanalysis.RealtimeFlowState) historicalQuestionSignals {
	result := historicalQuestionSignals{Status: "UNAVAILABLE", StoredRows: len(items), Statuses: map[string]int{}}
	shares := make([]float64, 0, len(items))
	var weightedNumerator, weightedDenominator, sum float64
	for _, item := range items {
		result.Statuses[strings.ToUpper(item.Status)]++
		window := historicalQuestionTenSecondWindow(item.Windows)
		completeFrame, _ := item.DataQuality["complete_frame"].(bool)
		if window == nil || window.Status != "COMPLETE" || window.CurrentSharePct == nil || item.TenSecondAttribution == nil || !completeFrame {
			continue
		}
		result.QualityRows++
		share := *window.CurrentSharePct
		shares = append(shares, share)
		sum += share
		if window.CurrentVolumeKRW > 0 {
			weightedNumerator += share * window.CurrentVolumeKRW
			weightedDenominator += window.CurrentVolumeKRW
		}
		if window.ShareDeltaPP != nil {
			switch {
			case *window.ShareDeltaPP > 0:
				result.RisingCount++
				if item.Status == "ALERT" {
					result.AlertRisingCount++
				}
			case *window.ShareDeltaPP < 0:
				result.FallingCount++
				if item.Status == "ALERT" {
					result.AlertFallingCount++
				}
			default:
				result.FlatCount++
			}
			if result.LargestDrop == nil || result.LargestDrop.ShareDeltaPP == nil || *window.ShareDeltaPP < *result.LargestDrop.ShareDeltaPP {
				result.LargestDrop = &historicalQuestionSignalExtreme{
					AsOfMS: item.AsOfMS, PreviousSharePct: window.PreviousSharePct, CurrentSharePct: window.CurrentSharePct,
					ShareDeltaPP: window.ShareDeltaPP, CurrentVolumeKRW: window.CurrentVolumeKRW,
					AssetDrivers:    append([]marketanalysis.RealtimeFlowDriver(nil), item.TenSecondAttribution.Drivers[:min(5, len(item.TenSecondAttribution.Drivers))]...),
					ExchangeDrivers: append([]marketanalysis.RealtimeFlowExchangeDriver(nil), item.TenSecondAttribution.ExchangeDrivers[:min(5, len(item.TenSecondAttribution.ExchangeDrivers))]...),
				}
			}
		}
	}
	result.QualityPct = historicalQuestionPercent(float64(result.QualityRows), float64(result.StoredRows))
	if len(shares) == 0 {
		return result
	}
	sort.Float64s(shares)
	result.Status = "CONFIRMED"
	result.SimpleMeanSharePct = historicalQuestionFloat(sum / float64(len(shares)))
	if weightedDenominator > 0 {
		result.WeightedMeanSharePct = historicalQuestionFloat(weightedNumerator / weightedDenominator)
	}
	result.MedianSharePct = historicalQuestionFloat(historicalQuestionQuantile(shares, 0.5))
	result.P10SharePct = historicalQuestionFloat(historicalQuestionQuantile(shares, 0.1))
	result.P90SharePct = historicalQuestionFloat(historicalQuestionQuantile(shares, 0.9))
	result.MinSharePct = historicalQuestionFloat(shares[0])
	result.MaxSharePct = historicalQuestionFloat(shares[len(shares)-1])
	return result
}

func historicalQuestionTenSecondWindow(windows []marketanalysis.RealtimeFlowWindow) *marketanalysis.RealtimeFlowWindow {
	for index := range windows {
		if windows[index].DurationSec == 10 {
			return &windows[index]
		}
	}
	return nil
}

func historicalQuestionFloat(value float64) *float64 { return &value }

func historicalQuestionQuantile(sorted []float64, percentile float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := percentile * float64(len(sorted)-1)
	left := int(math.Floor(position))
	right := int(math.Ceil(position))
	if left == right {
		return sorted[left]
	}
	weight := position - float64(left)
	return sorted[left]*(1-weight) + sorted[right]*weight
}

func historicalQuestionFallback(e historicalQuestionEvidence, asOfMS int64) marketanalysis.MarketQuestionAnswer {
	if e.TargetExchange != "" && e.TargetExchange != string(domain.ExchangeBithumb) {
		return historicalTargetExchangeFallback(e, asOfMS)
	}
	if e.Domestic.VolumeKRW <= 0 {
		return marketanalysis.MarketQuestionAnswer{
			Answer:    fmt.Sprintf("## 요약 결론\n\n요청한 **%s~%s KST** 구간은 현재 서버가 보유한 WebSocket 체결 자료에서 확인되지 않습니다.\n\n## 조회 범위와 자료 상태\n\n정확한 사용자 지정 구간으로 조회했지만 관측 거래대금이 없어 점유율을 계산하지 않았습니다.\n\n## 거래소·자산 분석\n\n근거 행이 없으므로 거래소별 합계와 자산별 기여를 추정하지 않습니다.\n\n## 10초 신호와 품질\n\n해당 구간에 저장된 유효 10초 신호도 확인되지 않았습니다.\n\n## 판단 한계와 다음 확인\n\n현재 PostgreSQL 보존 범위 안의 날짜와 시간을 지정해야 동일한 상세 분석이 가능합니다.", historicalQuestionTime(e.StartMS), historicalQuestionTime(e.EndMS)),
			KeyPoints: []string{"요청 구간의 점유율을 계산할 수 있는 관측 거래대금이 없습니다."}, Caveat: "보존되지 않은 구간의 값을 다른 날짜나 가까운 시각으로 대체하지 않았습니다.",
			Source: "SERVER_BRIEFING_FALLBACK", Model: "deterministic-go", AsOfMS: asOfMS,
		}
	}
	var b strings.Builder
	b.WriteString(historicalQuestionCoverageExplanation(e) + "\n\n")
	fmt.Fprintf(&b, "## 요약 결론\n\n%s~%s KST 실시간 체결 직접 관측 구간의 빗썸 점유율은 **%.2f%%**입니다. 국내 거래대금 **%s** 중 빗썸은 **%s**였고, 전체 **%s건** 중 빗썸 체결은 **%s건**입니다.\n\n", historicalQuestionTime(e.StartMS), historicalQuestionTime(e.EndMS), pointerValue(e.BithumbSharePct), historicalQuestionMoney(e.Domestic.VolumeKRW), historicalQuestionMoney(e.Bithumb.VolumeKRW), historicalQuestionInteger(e.Domestic.TradeCount), historicalQuestionInteger(e.Bithumb.TradeCount))
	if e.Domestic.BuyPct != nil && e.Bithumb.BuyPct != nil {
		fmt.Fprintf(&b, "빗썸 매수 거래대금 비중은 **%.2f%%**, 국내 전체 매수 거래대금 비중은 **%.2f%%**입니다.\n\n", pointerValue(e.Bithumb.BuyPct), pointerValue(e.Domestic.BuyPct))
	}
	if len(e.Exchanges) > 0 {
		b.WriteString("## 거래소별 합산 결과\n\n| 거래소 | 거래대금 | 국내 점유율 | 체결 수 |\n|---|---:|---:|---:|\n")
		for _, row := range e.Exchanges {
			fmt.Fprintf(&b, "| %s | %s | %s | %s건 |\n", historicalQuestionExchangeName(row.Exchange), historicalQuestionMoney(row.VolumeKRW), historicalQuestionDisplayPercent(row.SharePct), historicalQuestionInteger(row.TradeCount))
		}
	}
	if len(e.Path) > 0 {
		fmt.Fprintf(&b, "\n## 시간 경로\n\n사용 데이터는 거래소에서 직접 수집한 **WebSocket 체결 자료**입니다. 서버가 질문에 맞춰 **%s 단위**로 흐름을 묶었으며, 합계는 요청한 시간 범위에서 관측된 자료로 계산했습니다.\n\n| 시간대(KST) | 국내 거래대금 | 빗썸 거래대금 | 빗썸 점유율 | 자료 상태 |\n|---|---:|---:|---:|---|\n", historicalQuestionDuration(e.PathBucketSec))
		for _, row := range e.Path {
			quality := "PARTIAL"
			if historicalQuestionPointComplete(row) {
				quality = "COMPLETE"
			}
			fmt.Fprintf(&b, "| %s~%s | %s | %s | %s | %s |\n", historicalQuestionClock(row.StartMS), historicalQuestionClock(row.EndMS), historicalQuestionMoney(row.DomesticVolumeKRW), historicalQuestionMoney(row.BithumbVolumeKRW), historicalQuestionDisplayPercent(row.BithumbSharePct), quality)
		}
	}
	if e.HighPoint != nil && e.LowPoint != nil && e.HighToLow.Status == "CONFIRMED" {
		fmt.Fprintf(&b, "\n## 고점에서 저점까지의 구조\n\n완전한 %s 표시 구간 중 최고는 **%s~%s %.2f%%**, 최저는 **%s~%s %.2f%%**이며 변화는 **%.2f%%p**입니다. 빗썸 거래대금 효과는 **%.2f%%p**, 경쟁 거래소 효과는 **%.2f%%p**로 나눠 볼 수 있습니다.\n\n", historicalQuestionDuration(e.PathBucketSec), historicalQuestionClock(e.HighPoint.StartMS), historicalQuestionClock(e.HighPoint.EndMS), pointerValue(e.HighPoint.BithumbSharePct), historicalQuestionClock(e.LowPoint.StartMS), historicalQuestionClock(e.LowPoint.EndMS), pointerValue(e.LowPoint.BithumbSharePct), pointerValue(e.HighToLow.ShareChangePP), pointerValue(e.HighToLow.BithumbDirectEffectPP), pointerValue(e.HighToLow.CompetitorEffectPP))
		b.WriteString("| 거래소 | 거래대금 변화 | 점유율 변화 기여 |\n|---|---:|---:|\n")
		for _, row := range e.HighToLow.Exchanges {
			fmt.Fprintf(&b, "| %s | %s → %s | %+.2f%%p |\n", historicalQuestionExchangeName(string(row.Exchange)), historicalQuestionMoney(row.PreviousVolumeKRW), historicalQuestionMoney(row.CurrentVolumeKRW), row.ContributionPP)
		}
		b.WriteString("\n### 코인별 주요 기여\n\n| 코인 | 고점→저점 점유율 기여 | 빗썸 거래대금 변화 | 경쟁 거래소 거래대금 변화 |\n|---|---:|---:|---:|\n")
		for _, row := range e.HighToLow.Assets[:min(15, len(e.HighToLow.Assets))] {
			fmt.Fprintf(&b, "| %s | %+.2f%%p | %s | %s |\n", row.Asset, row.ContributionPP, historicalQuestionSignedMoney(row.BithumbVolumeChangeKRW), historicalQuestionSignedMoney(row.CompetitorVolumeChangeKRW))
		}
	}
	if len(e.TopAssets) > 0 {
		b.WriteString("\n## 전체 구간에서 거래가 많았던 코인\n\n| 코인 | 국내 거래대금 | 전체 비중 | 해당 코인의 빗썸 점유율 |\n|---|---:|---:|---:|\n")
		for _, row := range e.TopAssets {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row.Asset, historicalQuestionMoney(row.DomesticVolumeKRW), historicalQuestionDisplayPercent(row.DomesticSharePct), historicalQuestionDisplayPercent(row.BithumbCapturePct))
		}
	}
	if e.FirstVsLastHour.Status == "CONFIRMED" {
		fmt.Fprintf(&b, "\n## 시작 1시간과 마지막 1시간 비교\n\n시작 1시간 점유율은 **%.2f%%**, 마지막 1시간은 **%.2f%%**로 **%+.2f%%p** 변했습니다. 국내 거래대금 변화는 **%s**, 빗썸은 **%s**, 경쟁 거래소는 **%s**입니다.\n", pointerValue(e.FirstVsLastHour.PreviousSharePercent), pointerValue(e.FirstVsLastHour.CurrentSharePercent), pointerValue(e.FirstVsLastHour.ShareChangePP), historicalQuestionSignedMoney(pointerValue(e.FirstVsLastHour.MarketVolumeChangeKRW)), historicalQuestionSignedMoney(pointerValue(e.FirstVsLastHour.BithumbVolumeChangeKRW)), historicalQuestionSignedMoney(pointerValue(e.FirstVsLastHour.CompetitorVolumeChangeKRW)))
	}
	if e.RetrievalPlan.IncludeSignals10s {
		b.WriteString("\n## 10초 변화 신호\n\n")
		if e.Signals10s.Status == "CONFIRMED" {
			fmt.Fprintf(&b, "저장된 10초 기록은 **%s개**이며, 그중 비교 가능한 기록은 **%s개(%.2f%%)**입니다. 비교 가능한 기록의 거래대금 가중 평균은 **%.2f%%**, 단순 평균은 **%.2f%%**, 중앙값은 **%.2f%%**, 하위·상위 10%% 경계는 **%.2f%%·%.2f%%**입니다. 방향은 상승 **%s회**, 하락 **%s회**, 보합 **%s회**입니다.\n\n", historicalQuestionInteger(int64(e.Signals10s.StoredRows)), historicalQuestionInteger(int64(e.Signals10s.QualityRows)), pointerValue(e.Signals10s.QualityPct), pointerValue(e.Signals10s.WeightedMeanSharePct), pointerValue(e.Signals10s.SimpleMeanSharePct), pointerValue(e.Signals10s.MedianSharePct), pointerValue(e.Signals10s.P10SharePct), pointerValue(e.Signals10s.P90SharePct), historicalQuestionInteger(int64(e.Signals10s.RisingCount)), historicalQuestionInteger(int64(e.Signals10s.FallingCount)), historicalQuestionInteger(int64(e.Signals10s.FlatCount)))
			b.WriteString("| 상태 | 행 수 | 비중 |\n|---|---:|---:|\n")
			for _, status := range []string{"NORMAL", "WATCH", "ALERT", "DEGRADED", "WARMING"} {
				count := e.Signals10s.Statuses[status]
				fmt.Fprintf(&b, "| %s | %s개 | %.2f%% |\n", status, historicalQuestionInteger(int64(count)), float64(count)/float64(max(1, e.Signals10s.StoredRows))*100)
			}
		} else {
			b.WriteString("해당 구간에는 품질 조건을 만족한 저장 10초 신호가 없습니다.\n")
		}
	}
	b.WriteString("\n## 사용한 데이터\n\n거래소에서 직접 수집한 WebSocket 체결 자료와, 그 자료로 서버가 계산한 거래소별·코인별 거래대금 및 빗썸 점유율을 사용했습니다.")
	answer := b.String()
	return marketanalysis.MarketQuestionAnswer{
		Answer: answer,
		KeyPoints: []string{
			fmt.Sprintf("요청 구간 빗썸 점유율은 %.2f%%입니다.", pointerValue(e.BithumbSharePct)),
			fmt.Sprintf("국내 거래대금은 %s, 빗썸 거래대금은 %s입니다.", historicalQuestionMoney(e.Domestic.VolumeKRW), historicalQuestionMoney(e.Bithumb.VolumeKRW)),
			fmt.Sprintf("전체 체결은 %s건, 빗썸 체결은 %s건입니다.", historicalQuestionInteger(e.Domestic.TradeCount), historicalQuestionInteger(e.Bithumb.TradeCount)),
			"거래소에서 직접 수집한 WebSocket 체결 자료를 사용했습니다.",
		},
		Caveat: historicalQuestionCoverageExplanation(e),
		Source: "SERVER_BRIEFING_FALLBACK", Model: "deterministic-go", AsOfMS: asOfMS,
	}
}

func historicalTargetExchangeFallback(e historicalQuestionEvidence, asOfMS int64) marketanalysis.MarketQuestionAnswer {
	targetName := historicalQuestionExchangeName(e.TargetExchange)
	backfill := e.BackfillEvidence
	targetBackfill := historicalQuestionTargetBackfillEvidence(backfill, e.TargetExchange)
	var b strings.Builder
	fmt.Fprintf(&b, "## 요약 결론\n\n요청한 **%s~%s KST** 범위에서 %s의 WebSocket 직접 관측 자료를 기준으로 정리했습니다. %s\n\n", historicalQuestionTime(e.StartMS), historicalQuestionTime(e.EndMS), targetName, historicalQuestionCoverageExplanation(e))
	if e.Domestic.VolumeKRW > 0 && e.TargetSharePct != nil {
		fmt.Fprintf(&b, "실시간 체결 직접 관측 합계에서 국내 거래대금은 **%s**, %s 거래대금은 **%s**, 관측분 기준 비중은 **%.2f%%**입니다.\n\n", historicalQuestionMoney(e.Domestic.VolumeKRW), targetName, historicalQuestionMoney(e.Target.VolumeKRW), pointerValue(e.TargetSharePct))
	} else {
		fmt.Fprintf(&b, "요청 범위의 WebSocket 실체결만으로는 %s 점유율을 계산할 수 없었습니다. 다만 아래 백필 보유 여부는 별도로 표시합니다.\n\n", targetName)
	}
	if e.HighPoint != nil && e.HighPoint.TargetSharePct != nil {
		fmt.Fprintf(&b, "## 확인 가능한 최고 구간\n\n자료가 모두 있는 **%s** 구간 중 최고는 **%s~%s KST**이며, %s 점유율은 **%.2f%%**입니다. 이때 %s 거래대금은 **%s**, 국내 전체는 **%s**였습니다.\n\n", historicalQuestionDuration(e.PathBucketSec), historicalQuestionTime(e.HighPoint.StartMS), historicalQuestionTime(e.HighPoint.EndMS), targetName, pointerValue(e.HighPoint.TargetSharePct), targetName, historicalQuestionMoney(e.HighPoint.TargetVolumeKRW), historicalQuestionMoney(e.HighPoint.DomesticVolumeKRW))
		if len(e.TargetTopAssets) > 0 {
			b.WriteString("## 최고 구간의 주요 코인\n\n| 코인 | " + targetName + " 거래대금 | 국내 거래대금 | 해당 코인의 " + targetName + " 비중 |\n|---|---:|---:|---:|\n")
			for _, item := range e.TargetTopAssets[:min(10, len(e.TargetTopAssets))] {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", item.Asset, historicalQuestionMoney(item.TargetVolumeKRW), historicalQuestionMoney(item.DomesticVolumeKRW), historicalQuestionDisplayPercent(item.TargetCapturePct))
			}
			b.WriteString("\n")
		}
	} else if e.RetrievalPlan.IncludePath {
		b.WriteString("## 확인 가능한 최고 구간\n\n이번 답변 근거에서 비교 가능한 최고 구간을 확인하지 못했습니다. 전체 요청 구간의 자료 상태와 시간대별 최고점의 확인 여부는 구분합니다.\n\n")
	}
	b.WriteString("## 공식 정렬 1시간 저장 자료\n\n")
	if len(e.Aligned1hEvidence) == 0 {
		b.WriteString("이번 답변 근거에는 공식 정렬 1시간 저장 자료의 보유 현황이 포함되지 않았습니다. 이를 실제 저장 자료가 없다는 뜻으로 해석하지 않습니다.\n\n")
	} else {
		b.WriteString("| 거래소 | exact endpoint | 자산 행 | 기간(KST) |\n|---|---:|---:|---|\n")
		for _, item := range e.Aligned1hEvidence {
			fmt.Fprintf(&b, "| %s | %s | %s | %s~%s |\n", historicalQuestionExchangeName(item.Exchange), historicalQuestionInteger(item.EndpointCount), historicalQuestionInteger(item.AssetRowCount), historicalQuestionTime(item.FirstBucketMS), historicalQuestionTime(item.LastBucketMS))
		}
		if len(historicalQuestionTargetAlignedEvidence(e.Aligned1hEvidence, e.TargetExchange)) == 0 {
			fmt.Fprintf(&b, "\n이 자료에는 %s 자료가 포함되지 않습니다. 저장 자료가 있다는 사실은 표시하지만, 3거래소 분모를 %s의 국내 5거래소 점유율로 바꾸어 사용하지 않았습니다.\n", targetName, targetName)
		}
		b.WriteString("\n")
	}
	b.WriteString("## 공식 10분봉 백필 보유 자료\n\n")
	if len(backfill) == 0 {
		b.WriteString("이번 답변 근거에는 공식 10분봉 백필 보유 현황이 포함되지 않았습니다. 이를 실제 백필 행이 없다는 뜻으로 해석하지 않습니다.\n\n")
	} else {
		b.WriteString("| 거래소 | 방식 | 보유 행 | 정확한 KRW 거래금액 행 | 기간(KST) | 점유율 사용 |\n|---|---|---:|---:|---|---|\n")
		for _, item := range backfill {
			usable := "가능"
			if item.NotionalMethod != "EXACT_QUOTE_VOLUME" || item.ExactQuoteRows != item.RowCount {
				usable = "불가 — base 거래량 근거만 존재"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s~%s | %s |\n", historicalQuestionExchangeName(item.Exchange), item.NotionalMethod, historicalQuestionInteger(item.RowCount), historicalQuestionInteger(item.ExactQuoteRows), historicalQuestionTime(item.FirstBucketMS), historicalQuestionTime(item.LastBucketMS), usable)
		}
		if len(targetBackfill) == 0 {
			fmt.Fprintf(&b, "\n이 중 %s 백필 행은 없습니다. 다른 거래소의 백필을 %s 거래대금으로 대신하지 않았습니다.\n", targetName, targetName)
		}
		b.WriteString("\n백필 행은 존재 자체를 숨기지 않습니다. 다만 `BASE_VOLUME_ONLY` 행은 정확한 KRW 거래금액이 아니므로 점유율 분자에는 넣지 않았습니다.\n\n")
	}
	b.WriteString("## 사용한 데이터\n\n기본 근거는 거래소에서 직접 수집한 WebSocket 체결 자료입니다. 확인된 공식 정렬봉과 백필 보유 현황은 별도 자료이며, 직접 관측의 결측을 대신하거나 정확한 체결 관측으로 바꾸지 않았습니다.")
	points := []string{
		historicalQuestionCoverageExplanation(e),
	}
	if e.Domestic.VolumeKRW > 0 && e.TargetSharePct != nil {
		points = append(points, fmt.Sprintf("%s WebSocket 관측 거래대금 %s", targetName, historicalQuestionMoney(e.Target.VolumeKRW)))
	}
	if e.HighPoint != nil && e.HighPoint.TargetSharePct != nil {
		points = append(points, fmt.Sprintf("확인 가능한 최고 %s %.2f%%", historicalQuestionTime(e.HighPoint.StartMS), pointerValue(e.HighPoint.TargetSharePct)))
	}
	if len(targetBackfill) > 0 {
		var targetBackfillRows int64
		for _, item := range targetBackfill {
			targetBackfillRows += item.RowCount
		}
		points = append(points, fmt.Sprintf("%s 공식 10분봉 백필 %s행 보유", targetName, historicalQuestionInteger(targetBackfillRows)))
	} else if len(backfill) > 0 {
		points = append(points, fmt.Sprintf("다른 거래소 공식 10분봉 백필 %s개 집계 보유", historicalQuestionInteger(int64(len(backfill)))))
	}
	if len(e.Aligned1hEvidence) > 0 {
		points = append(points, fmt.Sprintf("공식 정렬 1시간 저장 자료 %s개 거래소 집계 보유", historicalQuestionInteger(int64(len(e.Aligned1hEvidence)))))
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: b.String(), KeyPoints: points,
		Caveat: historicalQuestionCoverageExplanation(e),
		Source: "SERVER_BRIEFING_FALLBACK", Model: "deterministic-go", AsOfMS: asOfMS,
	}
}

func historicalQuestionDisplayPercent(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return "확인 불가"
	}
	return fmt.Sprintf("%.2f%%", *value)
}

func historicalQuestionCoverageExplanation(e historicalQuestionEvidence) string {
	switch e.Status {
	case "CONFIRMED":
		return "요청 구간의 관측이 완전한 WebSocket 체결 자료로 계산했습니다. 공식 rolling 24시간 ticker와는 다른 구간입니다."
	case "PARTIAL":
		return "관측된 부분의 합계이며 요청 구간 전체의 확정값은 아닙니다. 확인 가능한 완전 구간은 따로 구분합니다."
	default:
		return "점유율을 계산할 수 있는 관측 근거가 없어 요청 구간의 점유율을 확정하지 않았습니다. 없는 값을 0으로 추정하지 않습니다."
	}
}

func historicalQuestionTargetAlignedEvidence(
	values []postgres.OfficialAligned1hEvidence,
	target string,
) []postgres.OfficialAligned1hEvidence {
	result := []postgres.OfficialAligned1hEvidence{}
	for _, item := range values {
		if strings.EqualFold(item.Exchange, target) {
			result = append(result, item)
		}
	}
	return result
}

func historicalQuestionTargetBackfillEvidence(
	values []postgres.OfficialCandle10mEvidence,
	target string,
) []postgres.OfficialCandle10mEvidence {
	result := []postgres.OfficialCandle10mEvidence{}
	for _, item := range values {
		if strings.EqualFold(item.Exchange, target) {
			result = append(result, item)
		}
	}
	return result
}

func enrichHistoricalQuestionResponse(
	response marketanalysis.GroundedMarketQuestionResponse,
	e historicalQuestionEvidence,
) marketanalysis.GroundedMarketQuestionResponse {
	current := &marketanalysis.MarketQuestionIntervalWindow{
		StartMS: e.StartMS, EndMS: e.EndMS,
		DomesticVolumeKRW:  historicalQuestionFloat(e.Domestic.VolumeKRW),
		DomesticTradeCount: int64Pointer(e.Domestic.TradeCount),
	}
	if e.TargetExchange == "" || e.TargetExchange == string(domain.ExchangeBithumb) {
		current.BithumbVolumeKRW = historicalQuestionFloat(e.Bithumb.VolumeKRW)
		current.BithumbSharePct = e.BithumbSharePct
		current.BithumbTradeCount = int64Pointer(e.Bithumb.TradeCount)
	}
	response.Facts = marketanalysis.MarketQuestionFacts{
		EvidenceLayer: "WEBSOCKET_HISTORICAL_RANGE", WindowSec: e.DurationSec,
		Current:         current,
		Drivers:         websocketFactDrivers(e.HighToLow.Assets[:min(5, len(e.HighToLow.Assets))]),
		ExchangeDrivers: websocketFactExchangeDrivers(e.HighToLow.Exchanges[:min(5, len(e.HighToLow.Exchanges))]),
	}
	if e.TargetExchange != "" && e.TargetExchange != string(domain.ExchangeBithumb) {
		response.Facts.TargetExchange = &marketanalysis.MarketQuestionTargetExchangeEvidence{
			Exchange:          e.TargetExchange,
			CurrentVolumeKRW:  historicalQuestionFloat(e.Target.VolumeKRW),
			CurrentSharePct:   e.TargetSharePct,
			CurrentTradeCount: int64Pointer(e.Target.TradeCount),
		}
		// The legacy driver fields are named for Bithumb. Target-exchange detail
		// remains in the grounded report rather than being mislabeled here.
		response.Facts.Drivers = []marketanalysis.MarketQuestionFactDriver{}
		response.Facts.ExchangeDrivers = []marketanalysis.MarketQuestionFactExchangeDriver{}
	}
	if e.HighToLow.Status == "CONFIRMED" {
		response.Facts.ShareDeltaPP = e.HighToLow.ShareChangePP
		response.Facts.DomesticVolumeDeltaKRW = e.HighToLow.MarketVolumeChangeKRW
		if e.TargetExchange == "" || e.TargetExchange == string(domain.ExchangeBithumb) {
			response.Facts.BithumbVolumeDeltaKRW = e.HighToLow.BithumbVolumeChangeKRW
		}
	}
	status := "PARTIAL"
	if e.Status == "CONFIRMED" {
		status = "LIVE"
	}
	if e.Status == "UNAVAILABLE" {
		status = "UNAVAILABLE"
	}
	response.Quality = marketanalysis.MarketQuestionQuality{
		Status: status, EndpointComparisonAvailable: e.HighToLow.Status == "CONFIRMED",
		PathComplete: e.Status == "CONFIRMED", CurrentCoverage: marketQuestionCoverage(e.Coverage),
		Warnings: []string{"공식 정렬 시간봉이 아닌 PostgreSQL WebSocket 직접 관측 구간입니다."},
	}
	if len(e.BackfillEvidence) > 0 {
		response.Quality.Warnings = append(response.Quality.Warnings, "공식 10분봉 백필 보유 자료는 보고서에 별도 표시하며, base-only 행은 점유율 계산에서 제외합니다.")
	}
	if len(e.Aligned1hEvidence) > 0 {
		response.Quality.Warnings = append(response.Quality.Warnings, "공식 정렬 1시간 저장 자료는 보고서에 별도 표시하며, 요청 거래소가 빠진 근거 계층을 국내 5거래소 점유율로 혼합하지 않습니다.")
	}
	if e.Status != "CONFIRMED" {
		response.Quality.Warnings = append(response.Quality.Warnings, "질문 구간 coverage에 공백이 있어 관측 합계와 완전 구간 비교를 구분해야 합니다.")
	}
	response.Provenance.AggregationVersion = e.AggregationVersion
	response.Provenance.OfficialBucketMS = 0
	response.Provenance.ComparisonEndMS = e.EndMS
	response.Provenance.AsOfKind = "EXACT_USER_KST_RANGE_END"
	return response
}

func historicalQuestionTime(ms int64) string {
	return time.UnixMilli(ms).In(marketQuestionKSTLocation).Format("2006-01-02 15:04:05")
}
func historicalQuestionClock(ms int64) string {
	return time.UnixMilli(ms).In(marketQuestionKSTLocation).Format("15:04:05")
}
func historicalQuestionDuration(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%d초", seconds)
	}
	if seconds < 3_600 {
		return fmt.Sprintf("%d분", seconds/60)
	}
	return fmt.Sprintf("%d시간", seconds/3_600)
}
func historicalQuestionExchangeName(value string) string {
	switch value {
	case "upbit":
		return "업비트"
	case "bithumb":
		return "빗썸"
	case "coinone":
		return "코인원"
	case "korbit":
		return "코빗"
	case "gopax":
		return "고팍스"
	}
	return value
}
func historicalQuestionMoney(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "확인 불가"
	}
	switch {
	case math.Abs(value) >= 100_000_000:
		return historicalQuestionComma(fmt.Sprintf("%.2f", value/100_000_000)) + "억원"
	case math.Abs(value) >= 10_000:
		return historicalQuestionComma(fmt.Sprintf("%.2f", value/10_000)) + "만원"
	default:
		// A positive observed amount must not become the displayed zero merely
		// because the historical report used hundred-million-won units.
		return historicalQuestionComma(strconv.FormatFloat(value, 'f', -1, 64)) + "원"
	}
}
func historicalQuestionSignedMoney(value float64) string {
	amount := historicalQuestionMoney(value)
	if value > 0 && !math.IsInf(value, 0) {
		return "+" + amount
	}
	return amount
}
func historicalQuestionInteger(value int64) string {
	return historicalQuestionComma(fmt.Sprintf("%d", value))
}
func historicalQuestionComma(value string) string {
	parts := strings.SplitN(value, ".", 2)
	integer, sign := parts[0], ""
	if strings.HasPrefix(integer, "-") || strings.HasPrefix(integer, "+") {
		sign, integer = integer[:1], integer[1:]
	}
	for index := len(integer) - 3; index > 0; index -= 3 {
		integer = integer[:index] + "," + integer[index:]
	}
	if len(parts) == 2 {
		return sign + integer + "." + parts[1]
	}
	return sign + integer
}
