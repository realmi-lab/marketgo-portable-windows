package app

import (
	"math"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/tickertrend"
)

func buildGroundedMarketQuestionResponse(
	payload marketanalysis.Analysis,
	officialWindow tickertrend.WindowComparison,
	wsInterval marketanalysis.MarketQuestionIntervalEvidence,
	query marketanalysis.MarketQuestionQuery,
	auxiliary *marketanalysis.MarketQuestionAuxiliaryFacts,
	answer marketanalysis.MarketQuestionAnswer,
) marketanalysis.GroundedMarketQuestionResponse {
	facts, quality, officialBucketMS, comparisonEndMS, asOfKind := marketQuestionV2Facts(payload, officialWindow, wsInterval, query)
	if query.PeerExchange == "" {
		facts.TargetExchange = marketQuestionTargetExchangeWithOfficialTicker(facts.TargetExchange, payload, query)
	}
	facts.CoinComparison = wsInterval.CoinComparison
	if len(query.ComparedAssets) > 1 {
		facts.ComparedAssetRanks = currentMarketQuestionComparedAssetRanks(payload, query)
	}
	for _, asset := range query.ComparedAssets {
		metrics := currentMarketQuestionAssetMetrics(payload, asset, currentMarketQuestionAssetBasis(payload).Complete)
		facts.ComparedAssetMetrics = append(facts.ComparedAssetMetrics, metrics)
		if metrics.CurrentDomesticVolumeKRW == nil {
			if quality.Status == "LIVE" {
				quality.Status = "PARTIAL"
			}
			quality.Warnings = append(quality.Warnings, asset+"의 비교 가능한 거래금액을 확인하지 못했습니다.")
		}
	}
	facts.Auxiliary = auxiliary
	quality.Warnings = append(quality.Warnings, marketQuestionAuxiliaryWarnings(auxiliary)...)
	return marketanalysis.GroundedMarketQuestionResponse{
		Contract: "GROUNDED_MARKET_QUESTION_V2", Query: query, Facts: facts, Quality: quality,
		Prose: marketanalysis.MarketQuestionProse{Answer: answer.Answer, KeyPoints: append([]string{}, answer.KeyPoints...), Caveat: answer.Caveat},
		Provider: marketanalysis.MarketQuestionProvider{
			Source: answer.Source, Model: answer.Model,
			FallbackUsed: strings.Contains(strings.ToUpper(answer.Source), "FALLBACK"), AsOfMS: answer.AsOfMS,
		},
		Provenance: marketanalysis.MarketQuestionProvenance{
			AnalysisVersion: payload.Context.AnalysisVersion, AggregationVersion: wsInterval.AggregationVersion,
			AnswerPolicyVersion:        marketQuestionPromptPolicyVersion,
			RetrievalStrategy:          marketQuestionRetrievalStrategy,
			PersistentStore:            "POSTGRESQL",
			ModelGeneratedSQL:          false,
			RawDatabaseRowsSentToModel: false,
			OfficialBucketMS:           officialBucketMS, ComparisonEndMS: comparisonEndMS,
			AsOfKind: asOfKind, DisplayTimezone: "Asia/Seoul",
		},
	}
}

func marketQuestionTargetExchangeWithOfficialTicker(
	target *marketanalysis.MarketQuestionTargetExchangeEvidence,
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
) *marketanalysis.MarketQuestionTargetExchangeEvidence {
	exchangeName := strings.ToLower(strings.TrimSpace(query.Exchange))
	if exchangeName == "" || exchangeName == string(domain.ExchangeBithumb) {
		return target
	}
	if target == nil {
		target = &marketanalysis.MarketQuestionTargetExchangeEvidence{Exchange: exchangeName}
	} else {
		cloned := *target
		target = &cloned
	}
	summary := payload.OfficialDomestic24h
	snapshot, exists := summary.Exchanges[domain.Exchange(exchangeName)]
	if !exists {
		target.OfficialTickerStatus = "UNAVAILABLE"
		return target
	}
	target.OfficialTickerStatus = snapshot.Status
	if snapshot.Status != "LIVE" || snapshot.VolumeKRW == nil {
		return target
	}
	target.Official24hVolumeKRW = copyQuestionFloat(snapshot.VolumeKRW)
	if summary.Status == "LIVE" && summary.TotalVolumeKRW != nil && *summary.TotalVolumeKRW > 0 {
		target.Official24hSharePct = float64Pointer(*snapshot.VolumeKRW / *summary.TotalVolumeKRW * 100)
	}
	return target
}

func marketQuestionV2Facts(
	payload marketanalysis.Analysis,
	officialWindow tickertrend.WindowComparison,
	wsInterval marketanalysis.MarketQuestionIntervalEvidence,
	query marketanalysis.MarketQuestionQuery,
) (marketanalysis.MarketQuestionFacts, marketanalysis.MarketQuestionQuality, int64, int64, string) {
	if query.Intent == marketanalysis.MarketQuestionCalendarDayShare {
		facts := marketanalysis.MarketQuestionFacts{
			EvidenceLayer: "OFFICIAL_ALIGNED_KST_CALENDAR_DAY", WindowSec: 86_400,
			Current:         officialFactWindow(officialWindow.Current),
			PathSummary:     officialFactPathSummary(officialWindow.PathSummary),
			Drivers:         []marketanalysis.MarketQuestionFactDriver{},
			ExchangeDrivers: []marketanalysis.MarketQuestionFactExchangeDriver{},
		}
		quality := marketanalysis.MarketQuestionQuality{
			Status: "UNAVAILABLE", EndpointComparisonAvailable: false,
			PathComplete: officialWindow.PathComplete, Warnings: []string{},
		}
		bucketMS := int64(0)
		if officialWindow.Status == "LIVE" && officialWindow.Current != nil {
			quality.Status = "LIVE"
			bucketMS = officialWindow.Current.BucketMS
		} else {
			quality.Warnings = append(quality.Warnings,
				"요청 날짜의 다음 날 00:00 KST 정확한 마감 표본이 없어 점유율을 확정하지 않았습니다.",
			)
		}
		if officialWindow.Current != nil && !officialWindow.PathComplete {
			quality.Warnings = append(quality.Warnings,
				"마감값은 확보했지만 해당 KST 날짜의 시간별 경로가 불완전해 최저·최고 경로 수치는 제공하지 않습니다.",
			)
		}
		if strings.TrimSpace(query.Asset) != "" {
			quality.Warnings = append(quality.Warnings,
				"날짜 마감 자산 세부는 현재 확정 근거에 포함되지 않아 전체 점유율만 제공합니다.",
			)
		}
		return facts, quality, bucketMS, query.TargetEndMS, "OFFICIAL_ALIGNED_KST_CALENDAR_DAY_CLOSE"
	}
	if query.Intent == marketanalysis.MarketQuestionAssetCapture && query.Asset != "" {
		return currentMarketQuestionAssetFacts(payload, query)
	}
	if _, requested := marketQuestionWSWindowSec(query); requested {
		facts := marketanalysis.MarketQuestionFacts{
			EvidenceLayer: "WEBSOCKET_DIRECT_OBSERVATION", WindowSec: wsInterval.WindowSec,
			CandleComparison: wsInterval.CandleComparison,
			Previous:         &wsInterval.Previous, Current: &wsInterval.Current,
			ShareDeltaPP: wsInterval.ShareDeltaPP, DomesticVolumeDeltaKRW: wsInterval.DomesticVolumeDeltaKRW,
			BithumbVolumeDeltaKRW: wsInterval.BithumbVolumeDeltaKRW, AssetMetrics: wsInterval.Asset,
			Drivers: websocketFactDrivers(wsInterval.Drivers), ExchangeDrivers: websocketFactExchangeDrivers(wsInterval.ExchangeDrivers),
			DriverContributionSumPP: wsInterval.DriverContributionSumPP, DriverResidualPP: wsInterval.DriverResidualPP,
			TargetExchange: wsInterval.TargetExchange,
		}
		if wsInterval.TargetExchange != nil && strings.EqualFold(wsInterval.TargetExchange.Exchange, string(domain.ExchangeBithumb)) {
			// Bithumb is already the primary numerator. A duplicate target object
			// makes clients treat it as the separate non-Bithumb presentation and
			// hide its verified attribution rows.
			facts.TargetExchange = nil
		} else if wsInterval.TargetExchange != nil {
			// The legacy driver and numerator fields describe effects on Bithumb
			// share. Do not expose them as primary facts for another exchange.
			facts.ShareDeltaPP = wsInterval.TargetExchange.ShareDeltaPP
			facts.BithumbVolumeDeltaKRW = nil
			facts.Drivers = []marketanalysis.MarketQuestionFactDriver{}
			facts.ExchangeDrivers = []marketanalysis.MarketQuestionFactExchangeDriver{}
			facts.DriverContributionSumPP = nil
			facts.DriverResidualPP = nil
		}
		warnings := []string{"공식 정렬 시간봉이 아닌 WebSocket 직접 관측 구간입니다."}
		if wsInterval.Asset != nil && !wsInterval.Asset.ContractIdentityVerified {
			warnings = append(warnings, "자산은 "+marketQuestionAssetIdentityNote)
		}
		status := "UNAVAILABLE"
		if wsInterval.Status == "CONFIRMED" {
			status = "LIVE"
		} else if wsInterval.CandleComparison != nil {
			status = "PARTIAL"
			warnings = []string{"공식 봉의 동일 종목 묶음 비교를 제공합니다. 전체 시장 점유율과 WebSocket 관측 완전성을 뜻하지 않습니다."}
		} else {
			warnings = append(warnings, "현재·직전 구간 coverage가 완전하지 않아 수치를 확정하지 않았습니다.")
		}
		quality := marketanalysis.MarketQuestionQuality{
			Status: status, EndpointComparisonAvailable: wsInterval.Status == "CONFIRMED",
			PathComplete: wsInterval.Status == "CONFIRMED", CurrentCoverage: wsInterval.CurrentCoverage,
			PreviousCoverage: wsInterval.PreviousCoverage, Warnings: warnings,
		}
		if wsInterval.CandleComparison != nil {
			facts.EvidenceLayer = wsInterval.CandleComparison.Source
			return facts, quality, 0, wsInterval.CandleComparison.EndMS, "OFFICIAL_CANDLE_WINDOW_END"
		}
		return facts, quality, 0, wsInterval.AsOfMS, "LAST_CLOSED_MINUTE_BOUNDARY"
	}

	if _, requested := marketQuestionOfficialWindowSec(query); requested {
		facts := marketanalysis.MarketQuestionFacts{
			EvidenceLayer: "OFFICIAL_ALIGNED_24H_ENDPOINTS", WindowSec: officialWindow.WindowSec,
			Previous: officialFactWindow(officialWindow.Previous), Current: officialFactWindow(officialWindow.Current),
			ShareDeltaPP: officialWindow.ShareDeltaPP, DomesticVolumeDeltaKRW: officialWindow.DomesticVolumeDeltaKRW,
			BithumbVolumeDeltaKRW:   officialWindow.BithumbVolumeDeltaKRW,
			PathSummary:             officialFactPathSummary(officialWindow.PathSummary),
			PriceRelationshipStatus: officialWindow.PriceRelationshipStatus,
			PriceRelationshipReason: officialWindow.PriceRelationshipReason,
			PriceRelationships:      officialFactPriceRelationships(officialWindow.PriceRelationships),
			AssetMetrics:            officialMarketQuestionAssetMetrics(officialWindow, query.Asset),
			Drivers:                 officialFactDrivers(officialWindow.Drivers), ExchangeDrivers: officialFactExchangeDrivers(officialWindow.ExchangeDrivers),
		}
		facts.ShareRelativeChangePct = relativeQuestionChange(
			officialWindowPreviousShare(officialWindow), officialWindowCurrentShare(officialWindow),
		)
		facts.DomesticVolumeChangePct = relativeQuestionChange(
			officialWindowPreviousDomesticVolume(officialWindow), officialWindowCurrentDomesticVolume(officialWindow),
		)
		facts.BithumbVolumeChangePct = relativeQuestionChange(
			officialWindowPreviousBithumbVolume(officialWindow), officialWindowCurrentBithumbVolume(officialWindow),
		)
		facts.DriverContributionSumPP, facts.DriverResidualPP = marketQuestionOfficialDriverReconciliation(officialWindow)
		warnings := []string{}
		if !officialWindow.EndpointComparison {
			warnings = append(warnings, "비교 시작점과 끝점이 모두 확보되지 않았습니다.")
		}
		if officialWindow.EndpointComparison && !officialWindow.PathComplete {
			warnings = append(warnings, "시작점과 끝점은 비교 가능하지만 중간 1시간봉 경로에 공백이 있습니다.")
		}
		if query.Asset != "" && (facts.AssetMetrics == nil || facts.AssetMetrics.CurrentDomesticVolumeKRW == nil) {
			warnings = append(warnings, "요청 자산의 공식 endpoint 상세가 없어 코인별 수치를 확정하지 않았습니다.")
		}
		if facts.AssetMetrics != nil && !facts.AssetMetrics.ContractIdentityVerified {
			warnings = append(warnings, "자산은 "+marketQuestionAssetIdentityNote)
		}
		status := "UNAVAILABLE"
		if officialWindow.Status == "LIVE" && officialWindow.EndpointComparison {
			status = "LIVE"
		}
		quality := marketanalysis.MarketQuestionQuality{
			Status: status, EndpointComparisonAvailable: officialWindow.EndpointComparison,
			PathComplete: officialWindow.PathComplete, Warnings: warnings,
		}
		bucketMS := int64(0)
		if officialWindow.Current != nil {
			bucketMS = officialWindow.Current.BucketMS
		}
		return facts, quality, bucketMS, bucketMS, "OFFICIAL_ALIGNED_HOUR_ENDPOINT"
	}

	return currentOfficialFacts(payload, query)
}

func officialFactPathSummary(path tickertrend.WindowPathSummary) *marketanalysis.MarketQuestionPathSummary {
	if path.Status == "" {
		return nil
	}
	return &marketanalysis.MarketQuestionPathSummary{
		Status: path.Status, Reason: path.Reason,
		PointCount: path.PointCount, ExpectedPointCount: path.ExpectedPointCount,
		StartMS: path.StartMS, EndMS: path.EndMS,
		MinSharePct: copyQuestionFloat(path.MinSharePct), MinShareAtMS: path.MinShareAtMS,
		MaxSharePct: copyQuestionFloat(path.MaxSharePct), MaxShareAtMS: path.MaxShareAtMS,
		LargestStepDeltaPP: copyQuestionFloat(path.LargestStepDeltaPP),
		LargestStepStartMS: path.LargestStepStartMS, LargestStepEndMS: path.LargestStepEndMS,
		LargestStepAbsSharePct: copyQuestionFloat(path.LargestStepAbsSharePct),
		RisingStepCount:        path.RisingStepCount, FallingStepCount: path.FallingStepCount,
		FlatStepCount: path.FlatStepCount, Direction: path.Direction,
		DominantDirectionSharePct: copyQuestionFloat(path.DominantDirectionSharePct),
		ObservedSegments:          officialFactPathSegments(path.ObservedSegments),
	}
}

func officialFactPathSegments(values []tickertrend.WindowPathSegment) []marketanalysis.MarketQuestionPathSegment {
	result := make([]marketanalysis.MarketQuestionPathSegment, 0, len(values))
	for _, value := range values {
		result = append(result, marketanalysis.MarketQuestionPathSegment{
			StartMS: value.StartMS, EndMS: value.EndMS, PointCount: value.PointCount,
			StartSharePct: value.StartSharePct, EndSharePct: value.EndSharePct,
			ShareDeltaPP: value.ShareDeltaPP,
			MinSharePct:  value.MinSharePct, MinShareAtMS: value.MinShareAtMS,
			MaxSharePct: value.MaxSharePct, MaxShareAtMS: value.MaxShareAtMS,
			LargestStepDeltaPP: value.LargestStepDeltaPP,
			LargestStepStartMS: value.LargestStepStartMS, LargestStepEndMS: value.LargestStepEndMS,
		})
	}
	return result
}

func officialFactPriceRelationships(values []tickertrend.WindowPriceRelationship) []marketanalysis.MarketQuestionPriceRelationship {
	result := make([]marketanalysis.MarketQuestionPriceRelationship, 0, len(values))
	for _, value := range values {
		result = append(result, marketanalysis.MarketQuestionPriceRelationship{
			Rank: value.Rank, Asset: value.Asset, PairCount: value.PairCount,
			Correlation: value.Correlation, Direction: value.Direction,
		})
	}
	return result
}

func relativeQuestionChange(previous, current *float64) *float64 {
	if previous == nil || current == nil || *previous <= 0 || math.IsNaN(*previous) || math.IsNaN(*current) || math.IsInf(*previous, 0) || math.IsInf(*current, 0) {
		return nil
	}
	return float64Pointer((*current / *previous - 1) * 100)
}

func officialWindowPreviousShare(window tickertrend.WindowComparison) *float64 {
	if window.Previous == nil {
		return nil
	}
	return float64Pointer(window.Previous.BithumbSharePct)
}

func officialWindowCurrentShare(window tickertrend.WindowComparison) *float64 {
	if window.Current == nil {
		return nil
	}
	return float64Pointer(window.Current.BithumbSharePct)
}

func officialWindowPreviousDomesticVolume(window tickertrend.WindowComparison) *float64 {
	if window.Previous == nil {
		return nil
	}
	return float64Pointer(window.Previous.TotalVolumeKRW)
}

func officialWindowCurrentDomesticVolume(window tickertrend.WindowComparison) *float64 {
	if window.Current == nil {
		return nil
	}
	return float64Pointer(window.Current.TotalVolumeKRW)
}

func officialWindowPreviousBithumbVolume(window tickertrend.WindowComparison) *float64 {
	if window.Previous == nil {
		return nil
	}
	return float64Pointer(window.Previous.BithumbVolumeKRW)
}

func officialWindowCurrentBithumbVolume(window tickertrend.WindowComparison) *float64 {
	if window.Current == nil {
		return nil
	}
	return float64Pointer(window.Current.BithumbVolumeKRW)
}

func currentMarketQuestionAssetFacts(
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
) (marketanalysis.MarketQuestionFacts, marketanalysis.MarketQuestionQuality, int64, int64, string) {
	basis := currentMarketQuestionAssetBasis(payload)
	if basis.Official {
		metrics := currentMarketQuestionAssetMetrics(payload, query.Asset, basis.Complete)
		facts := marketanalysis.MarketQuestionFacts{
			EvidenceLayer: basis.EvidenceLayer, WindowSec: basis.WindowSec,
			Current:      &marketanalysis.MarketQuestionIntervalWindow{StartMS: basis.AsOfMS, EndMS: basis.AsOfMS},
			AssetMetrics: metrics, Drivers: []marketanalysis.MarketQuestionFactDriver{}, ExchangeDrivers: []marketanalysis.MarketQuestionFactExchangeDriver{},
		}
		quality := marketanalysis.MarketQuestionQuality{
			Status: "UNAVAILABLE", CurrentCoverage: marketanalysis.MarketQuestionIntervalCoverage{Status: "NOT_APPLICABLE"},
			Warnings: []string{"국내 5개 거래소 공식 rolling 24시간 티커 금액입니다. WebSocket 체결 관측의 완전성과 체결 건수는 이 표본에서 확인할 수 없습니다."},
		}
		if basis.Complete {
			quality.Status = "LIVE"
			facts.Current.DomesticVolumeKRW = float64Pointer(payload.Summary.DomesticVolumeKRW)
			facts.Current.BithumbVolumeKRW = float64Pointer(payload.Summary.BithumbVolumeKRW)
			facts.Current.BithumbSharePct = copyQuestionFloat(payload.Summary.BithumbSharePct)
		} else {
			quality.Warnings = append(quality.Warnings, "공식 자산 표본이 LIVE 조건을 충족하지 않아 수치를 확정하지 않았습니다.")
		}
		if basis.Complete && metrics.CurrentDomesticVolumeKRW == nil {
			quality.Warnings = append(quality.Warnings, "요청 자산의 공식 금액이 선택한 표본에 없어 0원으로 추정하지 않았습니다.")
		}
		if !metrics.ContractIdentityVerified {
			quality.Warnings = append(quality.Warnings, "자산은 "+marketQuestionAssetIdentityNote)
		}
		return facts, quality, basis.AsOfMS, basis.AsOfMS, basis.AsOfKind
	}
	complete := payload.Context.SelectedWindowComplete && payload.Summary.SelectedWindowComplete
	metrics := currentMarketQuestionAssetMetrics(payload, query.Asset, complete)
	startMS := int64(payload.Context.CurrentIntervalStart * 1_000)
	endMS := int64(payload.Context.CurrentIntervalEnd * 1_000)
	facts := marketanalysis.MarketQuestionFacts{
		EvidenceLayer: "WEBSOCKET_ROLLING_24H_ASSET_SNAPSHOT", WindowSec: payload.WindowSec,
		Current:      &marketanalysis.MarketQuestionIntervalWindow{StartMS: startMS, EndMS: endMS},
		AssetMetrics: metrics, Drivers: []marketanalysis.MarketQuestionFactDriver{},
		ExchangeDrivers: []marketanalysis.MarketQuestionFactExchangeDriver{},
	}
	quality := marketanalysis.MarketQuestionQuality{
		Status: "UNAVAILABLE", EndpointComparisonAvailable: false, PathComplete: complete,
		CurrentCoverage: marketanalysis.MarketQuestionIntervalCoverage{
			Status: payload.Context.SelectedWindowStatus, CoverageSec: payload.Context.SelectedWindowCoverageSec,
			RequiredSec: payload.Context.SelectedWindowRequiredSec, WindowComplete: complete,
		},
		Warnings: []string{"공식 정렬 시간봉이 아닌 WebSocket rolling 자산 snapshot입니다."},
	}
	if complete {
		quality.Status = "LIVE"
		facts.Current.DomesticVolumeKRW = float64Pointer(payload.Summary.DomesticVolumeKRW)
		facts.Current.BithumbVolumeKRW = float64Pointer(payload.Summary.BithumbVolumeKRW)
		facts.Current.BithumbSharePct = copyQuestionFloat(payload.Summary.BithumbSharePct)
		facts.Current.DomesticTradeCount = int64Pointer(payload.Summary.DomesticTradeCount)
		facts.Current.BithumbTradeCount = int64Pointer(payload.Summary.BithumbTradeCount)
	} else {
		quality.Warnings = append(quality.Warnings, "rolling 구간 coverage가 완전하지 않아 자산 수치를 확정하지 않았습니다.")
	}
	if complete && metrics.CurrentDomesticVolumeKRW == nil {
		quality.Warnings = append(quality.Warnings, "요청 자산의 비교 가능한 국내 체결이 없어 0원으로 추정하지 않았습니다.")
	}
	if !metrics.ContractIdentityVerified {
		quality.Warnings = append(quality.Warnings, "자산은 "+marketQuestionAssetIdentityNote)
	}
	return facts, quality, 0, endMS, "WEBSOCKET_ROLLING_WINDOW_END"
}

func currentOfficialFacts(
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
) (marketanalysis.MarketQuestionFacts, marketanalysis.MarketQuestionQuality, int64, int64, string) {
	if query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
		exchange := strings.ToLower(strings.TrimSpace(query.Exchange))
		if exchange == "" {
			exchange = string(domain.ExchangeBithumb)
		}
		facts, quality, bucket, end, kind := currentOfficialExchangeFacts(payload, exchange)
		if query.PeerExchange == "bithumb" {
			peer, peerQuality, _, _, _ := currentOfficialExchangeFacts(payload, query.PeerExchange)
			if facts.Current != nil && peer.TargetExchange != nil {
				facts.Current.BithumbVolumeKRW = copyQuestionFloat(peer.TargetExchange.CurrentVolumeKRW)
				facts.Current.BithumbSharePct = copyQuestionFloat(peer.TargetExchange.CurrentSharePct)
			}
			if peerQuality.Status != "LIVE" {
				quality.Status = "PARTIAL"
				quality.Warnings = append(quality.Warnings, peerQuality.Warnings...)
			}
		}
		return facts, quality, bucket, end, kind
	}
	monitor := payload.OfficialShareMonitor
	facts := marketanalysis.MarketQuestionFacts{
		EvidenceLayer: "OFFICIAL_ALIGNED_24H_CURRENT", WindowSec: 86_400,
		Drivers: []marketanalysis.MarketQuestionFactDriver{}, ExchangeDrivers: []marketanalysis.MarketQuestionFactExchangeDriver{},
	}
	quality := marketanalysis.MarketQuestionQuality{Status: "UNAVAILABLE", Warnings: []string{}}
	bucketMS := int64(0)
	if monitor.Current != nil {
		bucketMS = monitor.Current.AsOfMS
		facts.Current = &marketanalysis.MarketQuestionIntervalWindow{
			StartMS: monitor.Current.AsOfMS, EndMS: monitor.Current.AsOfMS,
			DomesticVolumeKRW: float64Pointer(monitor.Current.TotalVolumeKRW),
			BithumbVolumeKRW:  float64Pointer(monitor.Current.BithumbVolumeKRW),
			BithumbSharePct:   float64Pointer(monitor.Current.BithumbSharePct),
		}
		quality.Status = monitor.Current.Status
		if quality.Status == "" {
			quality.Status = "LIVE"
		}
	}
	event := monitor.Movement
	if event == nil && len(monitor.RecentEvents) > 0 {
		event = &monitor.RecentEvents[0]
	}
	if event != nil && query.Intent == marketanalysis.MarketQuestionMovementExplanation {
		facts.Previous = &marketanalysis.MarketQuestionIntervalWindow{StartMS: event.StartedAtMS, EndMS: event.StartedAtMS, BithumbSharePct: float64Pointer(event.BaselineSharePct)}
		facts.Current = &marketanalysis.MarketQuestionIntervalWindow{StartMS: event.UpdatedAtMS, EndMS: event.UpdatedAtMS, BithumbSharePct: float64Pointer(event.CurrentSharePct)}
		bucketMS = event.UpdatedAtMS
		facts.ShareDeltaPP = float64Pointer(event.ChangePP)
		facts.Drivers = officialFactDrivers(event.Drivers)
		facts.ExchangeDrivers = officialFactExchangeDrivers(event.ExchangeDrivers)
		sum := 0.0
		for _, driver := range event.Drivers {
			sum += driver.ContributionPP
		}
		facts.DriverContributionSumPP = float64Pointer(sum)
		facts.DriverResidualPP = float64Pointer(event.ChangePP - sum)
		quality.EndpointComparisonAvailable = true
		quality.PathComplete = true
	}
	if facts.Current == nil {
		quality.Warnings = append(quality.Warnings, "현재 공식 24시간 정렬 점유율이 없습니다.")
	}
	if query.Intent == marketanalysis.MarketQuestionDataQuality {
		if !payload.Context.SelectedWindowComplete || !payload.Summary.SelectedWindowComplete {
			quality.Warnings = append(quality.Warnings, "WebSocket rolling 구간 coverage가 완전하지 않습니다.")
		}
		if payload.Summary.DomesticUnresolvedFXCount > 0 {
			quality.Warnings = append(quality.Warnings, "국내 거래대금 원화 환산 미해결 항목이 있습니다.")
		}
	}
	return facts, quality, bucketMS, bucketMS, "OFFICIAL_ALIGNED_24H_SNAPSHOT"
}

func currentOfficialExchangeFacts(
	payload marketanalysis.Analysis,
	exchangeName string,
) (marketanalysis.MarketQuestionFacts, marketanalysis.MarketQuestionQuality, int64, int64, string) {
	exchangeName = strings.ToLower(strings.TrimSpace(exchangeName))
	summary := payload.OfficialDomestic24h
	snapshot, exists := summary.Exchanges[domain.Exchange(exchangeName)]
	facts := marketanalysis.MarketQuestionFacts{
		EvidenceLayer: "OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER", WindowSec: 86_400,
		Drivers: []marketanalysis.MarketQuestionFactDriver{}, ExchangeDrivers: []marketanalysis.MarketQuestionFactExchangeDriver{},
		TargetExchange: &marketanalysis.MarketQuestionTargetExchangeEvidence{Exchange: exchangeName},
	}
	quality := marketanalysis.MarketQuestionQuality{Status: "UNAVAILABLE", Warnings: []string{}}
	asOfMS := marketQuestionSnapshotAsOfMS(payload, marketanalysis.MarketQuestionQuery{Intent: marketanalysis.MarketQuestionCurrentOfficialShare})
	if exists && summary.Status == "LIVE" && summary.TotalVolumeKRW != nil && *summary.TotalVolumeKRW > 0 &&
		snapshot.Status == "LIVE" && snapshot.VolumeKRW != nil {
		share := *snapshot.VolumeKRW / *summary.TotalVolumeKRW * 100
		facts.Current = &marketanalysis.MarketQuestionIntervalWindow{
			StartMS: asOfMS, EndMS: asOfMS, DomesticVolumeKRW: copyQuestionFloat(summary.TotalVolumeKRW),
		}
		facts.TargetExchange.CurrentVolumeKRW = copyQuestionFloat(snapshot.VolumeKRW)
		facts.TargetExchange.CurrentSharePct = float64Pointer(share)
		quality.Status = "LIVE"
	} else if exists && snapshot.Status == "LIVE" && snapshot.VolumeKRW != nil && finiteNonnegative(*snapshot.VolumeKRW) {
		facts.TargetExchange.CurrentVolumeKRW = copyQuestionFloat(snapshot.VolumeKRW)
		quality.Status = "PARTIAL"
		quality.Warnings = append(quality.Warnings, "선택한 거래소의 공식 거래대금은 확인됐지만 국내 전체 분모와 점유율은 확정 전입니다.")
	} else {
		quality.Warnings = append(quality.Warnings, rollingExchangeLabel(exchangeName)+" 공식 rolling 24시간 티커가 LIVE 상태가 아닙니다.")
	}
	return facts, quality, asOfMS, asOfMS, "OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER"
}

func officialFactWindow(point *tickertrend.Point) *marketanalysis.MarketQuestionIntervalWindow {
	if point == nil {
		return nil
	}
	return &marketanalysis.MarketQuestionIntervalWindow{
		StartMS: point.BucketMS, EndMS: point.BucketMS,
		DomesticVolumeKRW: float64Pointer(point.TotalVolumeKRW), BithumbVolumeKRW: float64Pointer(point.BithumbVolumeKRW),
		BithumbSharePct: float64Pointer(point.BithumbSharePct),
	}
}

func officialFactDrivers(values []movement.Driver) []marketanalysis.MarketQuestionFactDriver {
	result := make([]marketanalysis.MarketQuestionFactDriver, 0, min(5, len(values)))
	for _, value := range values[:min(5, len(values))] {
		result = append(result, marketanalysis.MarketQuestionFactDriver{
			Asset: value.Asset, ContributionPP: value.ContributionPP,
			BithumbVolumeChangeKRW: value.BithumbVolumeChangeKRW, CompetitorVolumeChangeKRW: value.CompetitorVolumeChangeKRW,
		})
	}
	return result
}

func officialFactExchangeDrivers(values []movement.ExchangeDriver) []marketanalysis.MarketQuestionFactExchangeDriver {
	result := make([]marketanalysis.MarketQuestionFactExchangeDriver, 0, min(3, len(values)))
	for _, value := range values[:min(3, len(values))] {
		result = append(result, marketanalysis.MarketQuestionFactExchangeDriver{Exchange: value.Exchange, ContributionPP: value.ContributionPP, VolumeChangeKRW: value.VolumeChangeKRW})
	}
	return result
}

func websocketFactDrivers(values []marketanalysis.AttributionAsset) []marketanalysis.MarketQuestionFactDriver {
	result := make([]marketanalysis.MarketQuestionFactDriver, 0, len(values))
	for _, value := range values {
		result = append(result, marketanalysis.MarketQuestionFactDriver{
			Asset: value.Asset, ContributionPP: value.ContributionPP,
			BithumbVolumeChangeKRW: value.BithumbVolumeChangeKRW, CompetitorVolumeChangeKRW: value.CompetitorVolumeChangeKRW,
		})
	}
	return result
}

func websocketFactExchangeDrivers(values []marketanalysis.AttributionExchange) []marketanalysis.MarketQuestionFactExchangeDriver {
	result := make([]marketanalysis.MarketQuestionFactExchangeDriver, 0, min(3, len(values)))
	for _, value := range values[:min(3, len(values))] {
		result = append(result, marketanalysis.MarketQuestionFactExchangeDriver{Exchange: string(value.Exchange), ContributionPP: value.ContributionPP, VolumeChangeKRW: value.VolumeChangeKRW})
	}
	return result
}

func marketQuestionOfficialDriverReconciliation(comparison tickertrend.WindowComparison) (*float64, *float64) {
	if comparison.ShareDeltaPP == nil || !comparison.EndpointComparison {
		return nil, nil
	}
	sum := 0.0
	for _, driver := range comparison.Drivers {
		sum += driver.ContributionPP
	}
	if math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil, nil
	}
	return float64Pointer(sum), float64Pointer(*comparison.ShareDeltaPP - sum)
}
