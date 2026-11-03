package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

const marketQuestionMaxWSIntervalSec = int64(86_400)

type marketQuestionIntervalPlan struct {
	windowSec     int64
	previousStart int64
	previousEnd   int64
	currentStart  int64
	currentEnd    int64
}

func marketQuestionWSWindowSec(query marketanalysis.MarketQuestionQuery) (int64, bool) {
	if query.Comparison != marketanalysis.MarketQuestionCurrentVsPreviousInterval || query.TimeValue <= 0 {
		return 0, false
	}
	unitSec := int64(60)
	switch query.TimeUnit {
	case marketanalysis.MarketQuestionTimeHour:
		unitSec = 3_600
	case marketanalysis.MarketQuestionTimeMinute:
	default:
		return 0, false
	}
	// Check before multiplying so even malformed internal queries cannot wrap
	// into a shorter valid window. Admission applies the same retention cap.
	if query.TimeValue > marketQuestionMaxWSIntervalSec/unitSec {
		return 0, false
	}
	return query.TimeValue * unitSec, true
}

func newMarketQuestionIntervalPlan(query marketanalysis.MarketQuestionQuery, requestedEnd int64) (marketQuestionIntervalPlan, error) {
	windowSec, ok := marketQuestionWSWindowSec(query)
	if !ok {
		return marketQuestionIntervalPlan{}, errors.New("websocket interval was not requested")
	}
	if windowSec > marketQuestionMaxWSIntervalSec {
		return marketQuestionIntervalPlan{}, errors.New("websocket interval exceeds the 24-hour interactive retention limit")
	}
	// A minute question ends at the most recent completely closed minute.
	currentEnd := (requestedEnd+1)/60*60 - 1
	currentStart := currentEnd - windowSec + 1
	return marketQuestionIntervalPlan{
		windowSec: windowSec, currentStart: currentStart, currentEnd: currentEnd,
		previousStart: currentStart - windowSec, previousEnd: currentStart - 1,
	}, nil
}

func unavailableMarketQuestionInterval(query marketanalysis.MarketQuestionQuery, asOfMS int64, reason string) marketanalysis.MarketQuestionIntervalEvidence {
	windowSec, _ := marketQuestionWSWindowSec(query)
	return marketanalysis.MarketQuestionIntervalEvidence{
		Status: "UNAVAILABLE", Reason: reason, Source: "WEBSOCKET_1S_BUCKETS",
		Semantics: "CURRENT_INTERVAL_VS_PREVIOUS_EQUAL_INTERVAL", MarketScope: query.MarketScope,
		WindowSec: windowSec, AsOfMS: asOfMS,
		Drivers: []marketanalysis.AttributionAsset{}, ExchangeDrivers: []marketanalysis.AttributionExchange{},
	}
}

func (s *Service) marketQuestionIntervalEvidence(
	ctx context.Context,
	query marketanalysis.MarketQuestionQuery,
	requestedEnd int64,
) (marketanalysis.MarketQuestionIntervalEvidence, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.MarketQuestionIntervalEvidence(ctx, query, requestedEnd)
	}
	plan, err := newMarketQuestionIntervalPlan(query, requestedEnd)
	if err != nil {
		return unavailableMarketQuestionInterval(query, (requestedEnd+1)*1_000, err.Error()), nil
	}
	if s.runtime == nil {
		return marketanalysis.MarketQuestionIntervalEvidence{}, ErrStopped
	}
	read, err := s.runtime.atomicRead(ctx, []aggregate.IntervalRequest{
		{Start: domain.UnixSecond(plan.previousStart - 1), End: domain.UnixSecond(plan.previousEnd)},
		{Start: domain.UnixSecond(plan.currentStart - 1), End: domain.UnixSecond(plan.currentEnd)},
	}, []coverageInterval{
		{start: plan.previousStart, end: plan.previousEnd},
		{start: plan.currentStart, end: plan.currentEnd},
	})
	if err != nil {
		return marketanalysis.MarketQuestionIntervalEvidence{}, err
	}
	if len(read.intervals) != 2 || len(read.coverage) != 2 || !read.intervals[0].OK || !read.intervals[1].OK {
		return marketanalysis.MarketQuestionIntervalEvidence{}, errors.New("invalid atomic websocket interval read")
	}
	previous := analysisRows(filterAnalysisValues(read.intervals[0].Values, query.MarketScope))
	current := analysisRows(filterAnalysisValues(read.intervals[1].Values, query.MarketScope))
	return buildMarketQuestionIntervalEvidence(query, plan, read.version, previous, current, read.coverage[0], read.coverage[1]), nil
}

func buildMarketQuestionIntervalEvidence(
	query marketanalysis.MarketQuestionQuery,
	plan marketQuestionIntervalPlan,
	version uint64,
	previous, current []marketanalysis.Row,
	previousCoverage, currentCoverage coverage.Result,
) marketanalysis.MarketQuestionIntervalEvidence {
	result := marketanalysis.MarketQuestionIntervalEvidence{
		Status: "UNAVAILABLE", Reason: "COMPARISON_WINDOW_INCOMPLETE",
		Source: "WEBSOCKET_1S_BUCKETS", Semantics: "CURRENT_INTERVAL_VS_PREVIOUS_EQUAL_INTERVAL",
		MarketScope: query.MarketScope, WindowSec: plan.windowSec,
		AsOfMS: (plan.currentEnd + 1) * 1_000, AggregationVersion: version,
		Previous:         marketanalysis.MarketQuestionIntervalWindow{StartMS: plan.previousStart * 1_000, EndMS: (plan.previousEnd + 1) * 1_000},
		Current:          marketanalysis.MarketQuestionIntervalWindow{StartMS: plan.currentStart * 1_000, EndMS: (plan.currentEnd + 1) * 1_000},
		PreviousCoverage: marketQuestionCoverage(previousCoverage), CurrentCoverage: marketQuestionCoverage(currentCoverage),
		Drivers: []marketanalysis.AttributionAsset{}, ExchangeDrivers: []marketanalysis.AttributionExchange{},
	}
	if query.Exchange != "" {
		result.TargetExchange = &marketanalysis.MarketQuestionTargetExchangeEvidence{Exchange: query.Exchange}
	}
	result.CoinComparison = buildMarketQuestionCoinComparison(query, plan, current, currentCoverage)
	complete := previousCoverage.WindowComplete && currentCoverage.WindowComplete &&
		previousCoverage.CoverageSec >= plan.windowSec && currentCoverage.CoverageSec >= plan.windowSec
	attribution := marketanalysis.ChangeAttribution(current, previous, complete)
	if attribution.Status != "CONFIRMED" {
		if strings.TrimSpace(attribution.UnavailableReason) != "" {
			result.Reason = attribution.UnavailableReason
		}
		return result
	}

	previousTrades, previousBithumbTrades := marketQuestionTradeCounts(previous)
	currentTrades, currentBithumbTrades := marketQuestionTradeCounts(current)
	result.Status, result.Reason = "CONFIRMED", ""
	result.Previous.DomesticVolumeKRW = attribution.PreviousVolumeKRW
	result.Previous.BithumbVolumeKRW = attribution.PreviousBithumbVolumeKRW
	result.Previous.BithumbSharePct = attribution.PreviousSharePercent
	result.Previous.DomesticTradeCount, result.Previous.BithumbTradeCount = int64Pointer(previousTrades), int64Pointer(previousBithumbTrades)
	result.Current.DomesticVolumeKRW = float64Pointer(attribution.CurrentVolumeKRW)
	result.Current.BithumbVolumeKRW = float64Pointer(attribution.CurrentBithumbVolumeKRW)
	result.Current.BithumbSharePct = attribution.CurrentSharePercent
	result.Current.DomesticTradeCount, result.Current.BithumbTradeCount = int64Pointer(currentTrades), int64Pointer(currentBithumbTrades)
	result.ShareDeltaPP = attribution.ShareChangePP
	result.DomesticVolumeDeltaKRW = attribution.MarketVolumeChangeKRW
	result.BithumbVolumeDeltaKRW = attribution.BithumbVolumeChangeKRW
	result.PrimaryDriver = attribution.PrimaryDriver
	driverSum := 0.0
	for _, driver := range attribution.Assets {
		driverSum += driver.ContributionPP
	}
	result.DriverContributionSumPP = float64Pointer(driverSum)
	result.DriverResidualPP = float64Pointer(*attribution.ShareChangePP - driverSum)
	result.Drivers = append(result.Drivers, balancedMarketQuestionAttributionDrivers(attribution.Assets, 5)...)
	result.ExchangeDrivers = append(result.ExchangeDrivers, attribution.Exchanges[:min(5, len(attribution.Exchanges))]...)
	if query.Exchange != "" {
		result.TargetExchange = marketQuestionTargetExchangeEvidence(query.Exchange, previous, current)
	}
	if query.Asset != "" {
		result.Asset = marketQuestionAssetInterval(query.Asset, attribution, previous, current)
	}
	return result
}

func balancedMarketQuestionAttributionDrivers(
	values []marketanalysis.AttributionAsset,
	limitPerDirection int,
) []marketanalysis.AttributionAsset {
	positive := make([]marketanalysis.AttributionAsset, 0, len(values))
	negative := make([]marketanalysis.AttributionAsset, 0, len(values))
	for _, value := range values {
		switch {
		case value.ContributionPP > 0:
			positive = append(positive, value)
		case value.ContributionPP < 0:
			negative = append(negative, value)
		}
	}
	sort.SliceStable(positive, func(i, j int) bool {
		return positive[i].ContributionPP > positive[j].ContributionPP
	})
	sort.SliceStable(negative, func(i, j int) bool {
		return negative[i].ContributionPP < negative[j].ContributionPP
	})
	result := append([]marketanalysis.AttributionAsset(nil), positive[:min(len(positive), limitPerDirection)]...)
	result = append(result, negative[:min(len(negative), limitPerDirection)]...)
	sort.SliceStable(result, func(i, j int) bool {
		return math.Abs(result[i].ContributionPP) > math.Abs(result[j].ContributionPP)
	})
	return result
}

func marketQuestionTargetExchangeEvidence(
	exchange string,
	previous, current []marketanalysis.Row,
) *marketanalysis.MarketQuestionTargetExchangeEvidence {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if exchange == "" {
		return nil
	}
	previousVolume, previousTrades, previousDomestic := marketQuestionExchangeWindowTotals(exchange, previous)
	currentVolume, currentTrades, currentDomestic := marketQuestionExchangeWindowTotals(exchange, current)
	result := &marketanalysis.MarketQuestionTargetExchangeEvidence{
		Exchange:          exchange,
		PreviousVolumeKRW: float64Pointer(previousVolume), CurrentVolumeKRW: float64Pointer(currentVolume),
		VolumeDeltaKRW:     float64Pointer(currentVolume - previousVolume),
		PreviousTradeCount: int64Pointer(previousTrades), CurrentTradeCount: int64Pointer(currentTrades),
	}
	if previousDomestic > 0 {
		result.PreviousSharePct = float64Pointer(previousVolume / previousDomestic * 100)
	}
	if currentDomestic > 0 {
		result.CurrentSharePct = float64Pointer(currentVolume / currentDomestic * 100)
	}
	if result.PreviousSharePct != nil && result.CurrentSharePct != nil {
		result.ShareDeltaPP = float64Pointer(*result.CurrentSharePct - *result.PreviousSharePct)
	}
	return result
}

func marketQuestionExchangeWindowTotals(exchange string, rows []marketanalysis.Row) (float64, int64, float64) {
	var volume, domestic float64
	var trades int64
	for _, row := range rows {
		domestic += row.VolumeKRW
		if strings.EqualFold(string(row.Exchange), exchange) {
			volume += row.VolumeKRW
			trades += row.TradeCount
		}
	}
	return volume, trades, domestic
}

func marketQuestionCoverage(value coverage.Result) marketanalysis.MarketQuestionIntervalCoverage {
	return marketanalysis.MarketQuestionIntervalCoverage{
		Status: value.Status, CoverageSec: value.CoverageSec, RequiredSec: value.RequiredSec,
		GapSeconds: value.GapSeconds, WindowComplete: value.WindowComplete,
	}
}

func marketQuestionTradeCounts(rows []marketanalysis.Row) (int64, int64) {
	var total, bithumb int64
	for _, row := range rows {
		total += row.TradeCount
		if row.Exchange == domain.ExchangeBithumb {
			bithumb += row.TradeCount
		}
	}
	return total, bithumb
}

func marketQuestionAssetInterval(asset string, attribution marketanalysis.Attribution, previous, current []marketanalysis.Row) *marketanalysis.MarketQuestionAssetInterval {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	observedExchanges := marketQuestionAssetObservedExchanges(asset, previous, current)
	previousSide := marketQuestionAssetSideMetrics(asset, previous, "")
	currentSide := marketQuestionAssetSideMetrics(asset, current, "")
	previousBithumbSide := marketQuestionAssetSideMetrics(asset, previous, string(domain.ExchangeBithumb))
	currentBithumbSide := marketQuestionAssetSideMetrics(asset, current, string(domain.ExchangeBithumb))
	for _, value := range attribution.Assets {
		if !strings.EqualFold(value.Asset, asset) {
			continue
		}
		currentWithinBithumb := optionalMarketQuestionPercent(value.CurrentBithumbVolumeKRW, attribution.CurrentBithumbVolumeKRW)
		previousWithinBithumb := optionalMarketQuestionPercent(value.PreviousBithumbVolumeKRW, pointerValue(attribution.PreviousBithumbVolumeKRW))
		currentContribution := optionalMarketQuestionPercent(value.CurrentBithumbVolumeKRW, attribution.CurrentVolumeKRW)
		previousContribution := optionalMarketQuestionPercent(value.PreviousBithumbVolumeKRW, pointerValue(attribution.PreviousVolumeKRW))
		return &marketanalysis.MarketQuestionAssetInterval{
			Asset: asset, CanonicalAssetID: value.AssetIdentity.CanonicalAssetID,
			ContractIdentityStatus:    value.AssetIdentity.ContractIdentityStatus,
			ContractIdentityVerified:  value.AssetIdentity.ContractIdentityVerified,
			ObservedExchanges:         observedExchanges,
			CurrentBithumbCapturePct:  value.CurrentBithumbCapturePercent,
			PreviousBithumbCapturePct: value.PreviousBithumbCapturePercent, BithumbCaptureChangePP: value.BithumbCaptureChangePP,
			CurrentShareWithinBithumbPct: currentWithinBithumb, PreviousShareWithinBithumbPct: previousWithinBithumb,
			ShareWithinBithumbChangePP:       optionalMarketQuestionDifference(currentWithinBithumb, previousWithinBithumb),
			CurrentTotalShareContributionPct: currentContribution, PreviousTotalShareContributionPct: previousContribution,
			TotalShareContributionChangePP: optionalMarketQuestionDifference(currentContribution, previousContribution),
			ChangeAttributionPP:            float64Pointer(value.ContributionPP),
			CurrentDomesticVolumeKRW:       float64Pointer(value.CurrentDomesticVolumeKRW), PreviousDomesticVolumeKRW: float64Pointer(value.PreviousDomesticVolumeKRW),
			CurrentBithumbVolumeKRW: float64Pointer(value.CurrentBithumbVolumeKRW), PreviousBithumbVolumeKRW: float64Pointer(value.PreviousBithumbVolumeKRW),
			CurrentTradeCount: int64Pointer(value.CurrentTradeCount), PreviousTradeCount: int64Pointer(value.PreviousTradeCount),
			CurrentAggressorBuyPct: currentSide.buyPct, PreviousAggressorBuyPct: previousSide.buyPct,
			AggressorBuyChangePP:          optionalMarketQuestionDifference(currentSide.buyPct, previousSide.buyPct),
			CurrentDirectionalCoveragePct: currentSide.coveragePct, PreviousDirectionalCoveragePct: previousSide.coveragePct,
			CurrentBithumbAggressorBuyPct: currentBithumbSide.buyPct, PreviousBithumbAggressorBuyPct: previousBithumbSide.buyPct,
			BithumbAggressorBuyChangePP:          optionalMarketQuestionDifference(currentBithumbSide.buyPct, previousBithumbSide.buyPct),
			CurrentBithumbDirectionalCoveragePct: currentBithumbSide.coveragePct, PreviousBithumbDirectionalCoveragePct: previousBithumbSide.coveragePct,
		}
	}
	identity := marketanalysis.Identity(asset)
	return &marketanalysis.MarketQuestionAssetInterval{
		Asset: asset, CanonicalAssetID: identity.CanonicalAssetID,
		ContractIdentityStatus:   identity.ContractIdentityStatus,
		ContractIdentityVerified: identity.ContractIdentityVerified,
		ObservedExchanges:        observedExchanges,
		CurrentAggressorBuyPct:   currentSide.buyPct, PreviousAggressorBuyPct: previousSide.buyPct,
		AggressorBuyChangePP:          optionalMarketQuestionDifference(currentSide.buyPct, previousSide.buyPct),
		CurrentDirectionalCoveragePct: currentSide.coveragePct, PreviousDirectionalCoveragePct: previousSide.coveragePct,
		CurrentBithumbAggressorBuyPct: currentBithumbSide.buyPct, PreviousBithumbAggressorBuyPct: previousBithumbSide.buyPct,
		BithumbAggressorBuyChangePP:          optionalMarketQuestionDifference(currentBithumbSide.buyPct, previousBithumbSide.buyPct),
		CurrentBithumbDirectionalCoveragePct: currentBithumbSide.coveragePct, PreviousBithumbDirectionalCoveragePct: previousBithumbSide.coveragePct,
	}
}

type marketQuestionSideMetrics struct {
	buyPct      *float64
	coveragePct *float64
}

func marketQuestionAssetSideMetrics(asset string, rows []marketanalysis.Row, exchange string) marketQuestionSideMetrics {
	var total, buy, sell float64
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.Base), asset) ||
			exchange != "" && !strings.EqualFold(string(row.Exchange), exchange) {
			continue
		}
		total += row.VolumeKRW
		buy += row.BuyVolumeKRW
		sell += row.SellVolumeKRW
	}
	directional := buy + sell
	result := marketQuestionSideMetrics{buyPct: optionalMarketQuestionPercent(buy, directional)}
	if total > 0 && directional >= 0 && directional <= total+math.Max(1e-6, total*1e-9) {
		result.coveragePct = optionalMarketQuestionPercent(directional, total)
	}
	return result
}

func marketQuestionAssetObservedExchanges(asset string, rowSets ...[]marketanalysis.Row) []string {
	seen := map[string]bool{}
	for _, rows := range rowSets {
		for _, row := range rows {
			if strings.EqualFold(strings.TrimSpace(row.Base), asset) {
				seen[string(row.Exchange)] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for _, exchange := range domain.DomesticExchanges {
		if seen[string(exchange)] {
			result = append(result, string(exchange))
		}
	}
	return result
}

func optionalMarketQuestionPercent(numerator, denominator float64) *float64 {
	if denominator <= 0 || numerator < 0 || math.IsNaN(numerator) || math.IsNaN(denominator) || math.IsInf(numerator, 0) || math.IsInf(denominator, 0) {
		return nil
	}
	return float64Pointer(numerator / denominator * 100)
}

func optionalMarketQuestionDifference(current, previous *float64) *float64 {
	if current == nil || previous == nil {
		return nil
	}
	return float64Pointer(*current - *previous)
}

func pointerValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func float64Pointer(value float64) *float64 { return &value }
func int64Pointer(value int64) *int64       { return &value }

func buildWSIntervalFallback(evidence marketanalysis.MarketQuestionIntervalEvidence, asOfMS int64) marketanalysis.MarketQuestionAnswer {
	if evidence.CandleComparison != nil {
		return marketanalysis.MarketQuestionAnswer{Answer: questionCandleBriefing(evidence.CandleComparison), Caveat: "공식 봉의 동일 종목 묶음 기준 비교이며 전체 시장 점유율과 WebSocket 직접 관측의 완전성을 뜻하지 않습니다.", Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS, KeyPoints: []string{}}
	}
	if marketQuestionUsesNonBithumbTarget(evidence) {
		return buildTargetExchangeWSIntervalFallback(evidence, asOfMS)
	}
	windowLabel := marketQuestionWSWindowLabel(evidence.WindowSec)
	points := []string{}
	if evidence.Status != "CONFIRMED" || evidence.Current.BithumbSharePct == nil || evidence.Previous.BithumbSharePct == nil || evidence.ShareDeltaPP == nil {
		answer := fmt.Sprintf(
			"최근 %s 직전 %s의 WebSocket 직접 관측 구간을 비교할 만큼 자료가 완전하지 않습니다. 현재 구간 coverage는 %d/%d초, 직전 구간은 %d/%d초이며 gap은 각각 %d초, %d초입니다.",
			koreanWithAnd(windowLabel), windowLabel,
			evidence.CurrentCoverage.CoverageSec, evidence.WindowSec,
			evidence.PreviousCoverage.CoverageSec, evidence.WindowSec,
			evidence.CurrentCoverage.GapSeconds, evidence.PreviousCoverage.GapSeconds,
		)
		points = append(points,
			fmt.Sprintf("현재 %s coverage %d/%d초", windowLabel, evidence.CurrentCoverage.CoverageSec, evidence.WindowSec),
			fmt.Sprintf("직전 %s coverage %d/%d초", windowLabel, evidence.PreviousCoverage.CoverageSec, evidence.WindowSec),
		)
		if reason := marketQuestionReasonLabel(evidence.Reason); reason != "" {
			points = append(points, "자료 미확보 사유: "+reason)
		}
		return marketanalysis.MarketQuestionAnswer{
			Answer:    answer + " 불완전한 구간의 거래대금이나 점유율을 확정값처럼 보완하지 않았습니다.",
			KeyPoints: points, Caveat: "이 비교는 공식 정렬 시간봉이 아니라 WebSocket으로 직접 관측한 체결 구간입니다. 두 구간의 coverage가 모두 완전해진 뒤 수치 비교가 가능합니다.",
			Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}

	paragraphs := []string{fmt.Sprintf(
		"WebSocket 직접 관측 기준 최근 %s 빗썸 점유율은 직전 %s의 %.2f%%에서 %.2f%%로 바뀌었고 변화폭은 %+.2f%%p(%s)입니다.",
		windowLabel, windowLabel, *evidence.Previous.BithumbSharePct, *evidence.Current.BithumbSharePct,
		*evidence.ShareDeltaPP, marketQuestionShareDirectionLabel(*evidence.ShareDeltaPP),
	)}
	points = append(points, fmt.Sprintf("직전 %.2f%% → 최근 %.2f%% · %+.2f%%p", *evidence.Previous.BithumbSharePct, *evidence.Current.BithumbSharePct, *evidence.ShareDeltaPP))
	if detail := marketQuestionIntervalDetail(evidence); detail != "" {
		paragraphs = append(paragraphs, detail)
	}
	if evidence.DomesticVolumeDeltaKRW != nil && evidence.BithumbVolumeDeltaKRW != nil {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"두 구간 사이 국내 전체 거래대금은 %s, 빗썸 거래대금은 %s 변했습니다. 점유율 변화는 빗썸 거래대금과 경쟁 거래소 거래대금이 함께 변한 결과입니다.",
			formatQuestionKRW(*evidence.DomesticVolumeDeltaKRW), formatQuestionKRW(*evidence.BithumbVolumeDeltaKRW),
		))
		points = append(points,
			"국내 전체 거래대금 변화 "+formatQuestionKRW(*evidence.DomesticVolumeDeltaKRW),
			"빗썸 거래대금 변화 "+formatQuestionKRW(*evidence.BithumbVolumeDeltaKRW),
		)
	}
	if evidence.Asset != nil {
		asset := evidence.Asset
		if asset.CurrentBithumbCapturePct == nil && asset.PreviousBithumbCapturePct == nil {
			paragraphs = append(paragraphs, fmt.Sprintf("%s는 두 관측 구간에서 비교 가능한 국내 체결이 없어 코인별 비중을 계산할 수 없습니다.", asset.Asset))
			points = append(points, asset.Asset+" 비교 가능한 체결 없음")
		} else {
			assetDetails := []string{}
			if asset.PreviousBithumbCapturePct != nil && asset.CurrentBithumbCapturePct != nil && asset.BithumbCaptureChangePP != nil {
				assetDetails = append(assetDetails, fmt.Sprintf("국내 %s 거래 중 빗썸 포착률은 %.2f%%에서 %.2f%%로 %+.2f%%p", asset.Asset, *asset.PreviousBithumbCapturePct, *asset.CurrentBithumbCapturePct, *asset.BithumbCaptureChangePP))
				points = append(points, fmt.Sprintf("%s 빗썸 포착률 %.2f%% → %.2f%% · %+.2f%%p", asset.Asset, *asset.PreviousBithumbCapturePct, *asset.CurrentBithumbCapturePct, *asset.BithumbCaptureChangePP))
			}
			if asset.PreviousShareWithinBithumbPct != nil && asset.CurrentShareWithinBithumbPct != nil && asset.ShareWithinBithumbChangePP != nil {
				assetDetails = append(assetDetails, fmt.Sprintf("빗썸 안에서 %s 비중은 %.2f%%에서 %.2f%%로 %+.2f%%p", asset.Asset, *asset.PreviousShareWithinBithumbPct, *asset.CurrentShareWithinBithumbPct, *asset.ShareWithinBithumbChangePP))
			}
			if asset.PreviousTotalShareContributionPct != nil && asset.CurrentTotalShareContributionPct != nil && asset.TotalShareContributionChangePP != nil {
				assetDetails = append(assetDetails, fmt.Sprintf("국내 전체 거래대금 중 빗썸의 %s 거래가 차지한 몫은 %.2f%%에서 %.2f%%로 %+.2f%%p", asset.Asset, *asset.PreviousTotalShareContributionPct, *asset.CurrentTotalShareContributionPct, *asset.TotalShareContributionChangePP))
			}
			if asset.ChangeAttributionPP != nil {
				assetDetails = append(assetDetails, fmt.Sprintf("전체 빗썸 점유율 변화에 대한 정규화 산술 기여는 %+.2f%%p", *asset.ChangeAttributionPP))
				points = append(points, fmt.Sprintf("%s 전체 점유율 변화 산술 기여 %+.2f%%p", asset.Asset, *asset.ChangeAttributionPP))
			}
			if asset.PreviousAggressorBuyPct != nil && asset.CurrentAggressorBuyPct != nil && asset.AggressorBuyChangePP != nil {
				coverage := "방향 분류 coverage 미확보"
				if asset.PreviousDirectionalCoveragePct != nil && asset.CurrentDirectionalCoveragePct != nil {
					coverage = fmt.Sprintf("방향 분류 coverage %.1f%%→%.1f%%", *asset.PreviousDirectionalCoveragePct, *asset.CurrentDirectionalCoveragePct)
				}
				assetDetails = append(assetDetails, fmt.Sprintf(
					"국내 %s 공격적 매수 비중은 %.2f%%에서 %.2f%%로 %+.2f%%p(%s)",
					asset.Asset, *asset.PreviousAggressorBuyPct, *asset.CurrentAggressorBuyPct, *asset.AggressorBuyChangePP, coverage,
				))
				points = append(points, fmt.Sprintf("%s 공격적 매수 비중 %.2f%% → %.2f%% · %+.2f%%p", asset.Asset, *asset.PreviousAggressorBuyPct, *asset.CurrentAggressorBuyPct, *asset.AggressorBuyChangePP))
			}
			if asset.PreviousBithumbAggressorBuyPct != nil && asset.CurrentBithumbAggressorBuyPct != nil && asset.BithumbAggressorBuyChangePP != nil {
				assetDetails = append(assetDetails, fmt.Sprintf(
					"빗썸 %s 공격적 매수 비중은 %.2f%%에서 %.2f%%로 %+.2f%%p",
					asset.Asset, *asset.PreviousBithumbAggressorBuyPct, *asset.CurrentBithumbAggressorBuyPct, *asset.BithumbAggressorBuyChangePP,
				))
			}
			if len(assetDetails) > 0 {
				paragraphs = append(paragraphs, strings.Join(assetDetails, "; ")+"였습니다. 집계 대상 국내시장이 서로 다른 지표이므로 같은 비율로 해석하면 안 됩니다.")
			}
		}
	}

	if detail := marketQuestionIntervalDriverDetail(evidence); detail != "" {
		paragraphs = append(paragraphs, detail)
	}
	paragraphs = append(paragraphs, fmt.Sprintf(
		"비교 구간은 KST %s까지 닫힌 %s 단위이며 현재·직전 구간 모두 %d초 coverage가 확인됐습니다. 집계 버전은 %d입니다.",
		marketQuestionKST(evidence.AsOfMS), windowLabel, evidence.WindowSec, evidence.AggregationVersion,
	))
	points = append(points, fmt.Sprintf("현재·직전 coverage 각각 %d/%d초 · gap 0초", evidence.WindowSec, evidence.WindowSec))
	if len(points) > marketQuestionPointLimit {
		points = points[:marketQuestionPointLimit]
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(paragraphs, "\n\n"), KeyPoints: points,
		Caveat: "이 수치는 공식 24시간 정렬봉 점유율이 아니라 WebSocket으로 직접 관측한 현재·직전 동일 구간 비교입니다. 코인·거래소 기여는 거래대금 변화의 산술 분해이며 가격 움직임이나 공지가 원인임을 증명하지 않습니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func buildTargetExchangeWSIntervalFallback(evidence marketanalysis.MarketQuestionIntervalEvidence, asOfMS int64) marketanalysis.MarketQuestionAnswer {
	target := evidence.TargetExchange
	windowLabel := marketQuestionWSWindowLabel(evidence.WindowSec)
	label := rollingExchangeLabel(target.Exchange)
	if evidence.Status != "CONFIRMED" || target.PreviousVolumeKRW == nil || target.CurrentVolumeKRW == nil ||
		target.VolumeDeltaKRW == nil || target.PreviousSharePct == nil || target.CurrentSharePct == nil || target.ShareDeltaPP == nil {
		return marketanalysis.MarketQuestionAnswer{
			Answer: fmt.Sprintf(
				"%s의 최근 %s 구간과 직전 %s 구간을 WebSocket 직접 관측값으로 비교할 만큼 자료가 완전하지 않습니다. 현재 구간 coverage는 %d/%d초, 직전 구간은 %d/%d초입니다. 불완전한 구간에 임의 금액을 채우거나 빗썸 수치로 대체하지 않았습니다.",
				label, windowLabel, windowLabel, evidence.CurrentCoverage.CoverageSec, evidence.WindowSec,
				evidence.PreviousCoverage.CoverageSec, evidence.WindowSec,
			),
			KeyPoints: []string{
				fmt.Sprintf("%s 현재 %s coverage %d/%d초", label, windowLabel, evidence.CurrentCoverage.CoverageSec, evidence.WindowSec),
				fmt.Sprintf("%s 직전 %s coverage %d/%d초", label, windowLabel, evidence.PreviousCoverage.CoverageSec, evidence.WindowSec),
			},
			Caveat: "이 비교는 공식 rolling 24시간 티커가 아니라 DB에 누적된 WebSocket 체결의 현재·직전 동일 구간입니다. 두 구간이 모두 완전할 때만 거래대금과 점유율을 확정합니다.",
			Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}
	paragraphs := []string{
		fmt.Sprintf(
			"WebSocket 직접 관측 기준 %s의 최근 %s 거래대금은 %s이고 직전 %s의 %s보다 %s 변했습니다.",
			label, windowLabel, formatQuestionKRWAmount(*target.CurrentVolumeKRW), windowLabel,
			formatQuestionKRWAmount(*target.PreviousVolumeKRW), formatQuestionKRW(*target.VolumeDeltaKRW),
		),
		fmt.Sprintf(
			"같은 구간의 국내 전체 거래대금 중 %s 비중은 %.2f%%에서 %.2f%%로 %+.2f%%p(%s) 바뀌었습니다. 이 비중은 해당 구간 WebSocket 체결 거래대금 기준입니다.",
			label, *target.PreviousSharePct, *target.CurrentSharePct, *target.ShareDeltaPP,
			marketQuestionShareDirectionLabel(*target.ShareDeltaPP),
		),
	}
	if detail := marketQuestionPairedAmount("같은 구간 국내 전체 거래대금", evidence.Previous.DomesticVolumeKRW, evidence.Current.DomesticVolumeKRW); detail != "" {
		paragraphs = append(paragraphs, detail+"입니다. 각 금액은 해당 직접 관측 구간의 합계이며 공식 rolling 24시간 거래대금이 아닙니다.")
	}
	if target.PreviousTradeCount != nil && target.CurrentTradeCount != nil {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"관측 체결 건수는 직전 %s %d건, 최근 %s %d건입니다. 체결 건수와 거래대금은 서로 다른 지표이므로 함께 보되 같은 변화율로 해석하지 않습니다.",
			windowLabel, *target.PreviousTradeCount, windowLabel, *target.CurrentTradeCount,
		))
	}
	paragraphs = append(paragraphs, fmt.Sprintf(
		"비교 종료시각은 KST %s이며 현재·직전 구간 모두 %d초 coverage가 확인됐습니다.",
		marketQuestionKST(evidence.AsOfMS), evidence.WindowSec,
	))
	points := []string{
		fmt.Sprintf("%s 최근 %s 거래대금 %s", label, windowLabel, formatQuestionKRWAmount(*target.CurrentVolumeKRW)),
		fmt.Sprintf("%s 직전 %s 거래대금 %s", label, windowLabel, formatQuestionKRWAmount(*target.PreviousVolumeKRW)),
		fmt.Sprintf("%s 거래대금 변화 %s", label, formatQuestionKRW(*target.VolumeDeltaKRW)),
		fmt.Sprintf("%s 국내 비중 %.2f%% → %.2f%% · %+.2f%%p", label, *target.PreviousSharePct, *target.CurrentSharePct, *target.ShareDeltaPP),
		fmt.Sprintf("현재·직전 coverage 각각 %d/%d초 · gap %d초/%d초", evidence.WindowSec, evidence.WindowSec, evidence.CurrentCoverage.GapSeconds, evidence.PreviousCoverage.GapSeconds),
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(paragraphs, "\n\n"), KeyPoints: points,
		Caveat: "이 수치는 DB에 누적된 WebSocket 체결의 현재·직전 동일 구간 비교이며 공식 rolling 24시간 티커 변화가 아닙니다. 관측 거래대금 변화만 보여 주며 가격이나 공지가 원인임을 뜻하지 않습니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func marketQuestionShareDirectionLabel(deltaPP float64) string {
	switch {
	case deltaPP >= 0.005:
		return "상승"
	case deltaPP <= -0.005:
		return "하락"
	default:
		return "표시 정밀도상 변화 없음"
	}
}

func enrichWSIntervalFacts(answer marketanalysis.MarketQuestionAnswer, evidence marketanalysis.MarketQuestionIntervalEvidence) marketanalysis.MarketQuestionAnswer {
	if evidence.CandleComparison != nil {
		return answer
	}
	if marketQuestionUsesNonBithumbTarget(evidence) {
		return enrichTargetExchangeWSIntervalFacts(answer, evidence)
	}
	if evidence.Status != "CONFIRMED" || evidence.ShareDeltaPP == nil || evidence.Current.BithumbSharePct == nil || evidence.Previous.BithumbSharePct == nil {
		return answer
	}
	mandatory := []string{fmt.Sprintf(
		"WebSocket %s: 직전 %.2f%% → 최근 %.2f%% · %+.2f%%p",
		marketQuestionWSWindowLabel(evidence.WindowSec), *evidence.Previous.BithumbSharePct, *evidence.Current.BithumbSharePct, *evidence.ShareDeltaPP,
	), fmt.Sprintf(
		"현재·직전 coverage %d/%d초 · %d/%d초 · gap %d초/%d초",
		evidence.CurrentCoverage.CoverageSec, evidence.WindowSec,
		evidence.PreviousCoverage.CoverageSec, evidence.WindowSec,
		evidence.CurrentCoverage.GapSeconds, evidence.PreviousCoverage.GapSeconds,
	)}
	if evidence.Asset != nil && evidence.Asset.ChangeAttributionPP != nil {
		mandatory = append(mandatory, fmt.Sprintf("%s 전체 점유율 변화 산술 기여 %+.2f%%p", evidence.Asset.Asset, *evidence.Asset.ChangeAttributionPP))
	}
	if evidence.Asset != nil && evidence.Asset.PreviousBithumbCapturePct != nil && evidence.Asset.CurrentBithumbCapturePct != nil && evidence.Asset.BithumbCaptureChangePP != nil {
		mandatory = append(mandatory, fmt.Sprintf(
			"%s 빗썸 포착률 %.2f%% → %.2f%% · %+.2f%%p",
			evidence.Asset.Asset, *evidence.Asset.PreviousBithumbCapturePct,
			*evidence.Asset.CurrentBithumbCapturePct, *evidence.Asset.BithumbCaptureChangePP,
		))
	}
	if evidence.Asset != nil && evidence.Asset.PreviousShareWithinBithumbPct != nil && evidence.Asset.CurrentShareWithinBithumbPct != nil && evidence.Asset.ShareWithinBithumbChangePP != nil {
		mandatory = append(mandatory, fmt.Sprintf(
			"빗썸 내부 %s 비중 %.2f%% → %.2f%% · %+.2f%%p",
			evidence.Asset.Asset, *evidence.Asset.PreviousShareWithinBithumbPct,
			*evidence.Asset.CurrentShareWithinBithumbPct, *evidence.Asset.ShareWithinBithumbChangePP,
		))
	}
	if evidence.Asset != nil && evidence.Asset.PreviousTotalShareContributionPct != nil && evidence.Asset.CurrentTotalShareContributionPct != nil && evidence.Asset.TotalShareContributionChangePP != nil {
		mandatory = append(mandatory, fmt.Sprintf(
			"%s 국내 전체 점유율 몫 %.2f%% → %.2f%% · %+.2f%%p",
			evidence.Asset.Asset, *evidence.Asset.PreviousTotalShareContributionPct,
			*evidence.Asset.CurrentTotalShareContributionPct, *evidence.Asset.TotalShareContributionChangePP,
		))
	}
	if evidence.Asset != nil && evidence.Asset.CurrentAggressorBuyPct != nil && evidence.Asset.CurrentDirectionalCoveragePct != nil {
		mandatory = append(mandatory, fmt.Sprintf(
			"%s 최근 공격적 매수 비중 %.2f%% · 방향 분류 coverage %.1f%%",
			evidence.Asset.Asset, *evidence.Asset.CurrentAggressorBuyPct, *evidence.Asset.CurrentDirectionalCoveragePct,
		))
	}
	if len(evidence.Drivers) > 0 {
		driver := evidence.Drivers[0]
		if asset := strings.ToUpper(strings.TrimSpace(driver.Asset)); asset != "" {
			mandatory = append(mandatory, fmt.Sprintf(
				"주요 코인 driver %s %+.2f%%p · 빗썸 거래대금 변화 %s / 타 거래소 %s",
				asset, driver.ContributionPP, formatQuestionKRW(driver.BithumbVolumeChangeKRW), formatQuestionKRW(driver.CompetitorVolumeChangeKRW),
			))
		}
	}
	if len(evidence.ExchangeDrivers) > 0 {
		driver := evidence.ExchangeDrivers[0]
		mandatory = append(mandatory, fmt.Sprintf(
			"주요 거래소 기여 %s %+.2f%%p · 거래대금 변화 %s",
			rollingExchangeLabel(string(driver.Exchange)), driver.ContributionPP, formatQuestionKRW(driver.VolumeChangeKRW),
		))
	}
	merged := append([]string{}, mandatory...)
	for _, point := range answer.KeyPoints {
		if len(merged) >= marketQuestionPointLimit {
			break
		}
		if !marketQuestionContainsString(merged, point) {
			merged = append(merged, point)
		}
	}
	answer.KeyPoints = merged
	return answer
}

func enrichTargetExchangeWSIntervalFacts(answer marketanalysis.MarketQuestionAnswer, evidence marketanalysis.MarketQuestionIntervalEvidence) marketanalysis.MarketQuestionAnswer {
	target := evidence.TargetExchange
	if evidence.Status != "CONFIRMED" || target == nil || target.PreviousVolumeKRW == nil || target.CurrentVolumeKRW == nil ||
		target.VolumeDeltaKRW == nil || target.PreviousSharePct == nil || target.CurrentSharePct == nil || target.ShareDeltaPP == nil {
		return answer
	}
	label := rollingExchangeLabel(target.Exchange)
	mandatory := []string{
		fmt.Sprintf("%s 최근 %s 거래대금 %s · 직전 %s · 변화 %s", label, marketQuestionWSWindowLabel(evidence.WindowSec), formatQuestionKRWAmount(*target.CurrentVolumeKRW), formatQuestionKRWAmount(*target.PreviousVolumeKRW), formatQuestionKRW(*target.VolumeDeltaKRW)),
		fmt.Sprintf("%s 국내 비중 %.2f%% → %.2f%% · %+.2f%%p", label, *target.PreviousSharePct, *target.CurrentSharePct, *target.ShareDeltaPP),
		fmt.Sprintf("현재·직전 coverage %d/%d초 · %d/%d초", evidence.CurrentCoverage.CoverageSec, evidence.WindowSec, evidence.PreviousCoverage.CoverageSec, evidence.WindowSec),
	}
	merged := append([]string{}, mandatory...)
	for _, point := range answer.KeyPoints {
		if len(merged) >= marketQuestionPointLimit || marketQuestionContainsString(merged, point) {
			continue
		}
		merged = append(merged, point)
	}
	answer.KeyPoints = merged
	return answer
}

func marketQuestionContainsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func marketQuestionWSWindowLabel(windowSec int64) string {
	if windowSec%3_600 == 0 {
		return fmt.Sprintf("%d시간", windowSec/3_600)
	}
	if windowSec%60 == 0 {
		return fmt.Sprintf("%d분", windowSec/60)
	}
	return fmt.Sprintf("%d초", windowSec)
}

func marketQuestionKST(timestampMS int64) string {
	kst := time.FixedZone("KST", 9*60*60)
	return time.UnixMilli(timestampMS).In(kst).Format("2006-01-02 15:04")
}
