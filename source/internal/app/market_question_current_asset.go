package app

import (
	"fmt"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/tickertrend"
)

type marketQuestionAssetBasis struct {
	Source, EvidenceLayer, AsOfKind string
	AsOfMS, WindowSec               int64
	Official, Complete              bool
}

func useDeterministicCurrentAssetPlan(question string, history []marketanalysis.MarketQuestionConversationTurn, query marketanalysis.MarketQuestionQuery, err error) bool {
	return err == nil && len(history) == 0 && query.Intent == marketanalysis.MarketQuestionAssetCapture && query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.Asset != "" && query.MarketScope == "KRW" && (query.Exchange == "" || query.Exchange == "bithumb") &&
		containsAnyString(question, "거래금액", "거래대금", "점유율", "비중", "포착률") && !containsAnyString(question, "예측", "전망", "다음", "미래", "목표", "가격", "분모", "구분")
}

// Rank only known amounts in the requested scope. Equal amounts share a rank;
// missing coins are absent, never ranked below the known coins.
func currentMarketQuestionComparedAssetRanks(payload marketanalysis.Analysis, query marketanalysis.MarketQuestionQuery) map[string]int {
	type item struct {
		asset  string
		amount float64
	}
	items := []item{}
	for _, asset := range query.ComparedAssets {
		m := currentMarketQuestionAssetMetrics(payload, asset, currentMarketQuestionAssetBasis(payload).Complete)
		amount := m.CurrentDomesticVolumeKRW
		if query.Exchange == "bithumb" {
			amount = m.CurrentBithumbVolumeKRW
		}
		if amount != nil && finiteNonnegative(*amount) {
			items = append(items, item{asset, *amount})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].amount == items[j].amount {
			return items[i].asset < items[j].asset
		}
		return items[i].amount > items[j].amount
	})
	ranks := map[string]int{}
	rank := 0
	for i, item := range items {
		if i == 0 || item.amount != items[i-1].amount {
			rank = i + 1
		}
		ranks[item.asset] = rank
	}
	return ranks
}

// Consume the provenance attached to the selected rows, not a later ticker
// snapshot. Analysis may already have replaced its 24h rows with official
// amounts; that availability is not WebSocket observation completeness.
func currentMarketQuestionAssetBasis(payload marketanalysis.Analysis) marketQuestionAssetBasis {
	basis := marketQuestionAssetBasis{
		Source: "WEBSOCKET_ROLLING_WINDOW", EvidenceLayer: "WEBSOCKET_ROLLING_24H_ASSET_SNAPSHOT", AsOfKind: "WEBSOCKET_ROLLING_WINDOW_END",
		WindowSec: payload.WindowSec, AsOfMS: int64(payload.Context.CurrentIntervalEnd * 1000),
		Complete: payload.Context.SelectedWindowComplete && payload.Summary.SelectedWindowComplete,
	}
	if basis.AsOfMS <= 0 {
		basis.AsOfMS = int64(payload.GeneratedAt * 1000)
	}
	data := payload.Context.DataSource
	basis.Official = data["official_24h_ticker_used"] == true || data["volume_basis"] == officialDomestic24hBasis || data["bar_type"] == "OFFICIAL_ROLLING_24H_TICKER"
	if !basis.Official {
		return basis
	}
	basis.Source = officialDomestic24hBasis
	basis.EvidenceLayer = "OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER"
	basis.AsOfKind = basis.EvidenceLayer
	basis.AsOfMS = snapshotInt64(data["official_24h_ticker_as_of_ms"])
	basis.Complete = basis.Complete && basis.WindowSec == 86_400 &&
		data["official_24h_ticker_used"] == true && data["official_24h_ticker_status"] == "LIVE" &&
		snapshotInt64(data["official_24h_ticker_exchange_count"]) == 5 && basis.AsOfMS > 0 && basis.AsOfMS < 100_000_000_000_000
	return basis
}

func currentMarketQuestionAssetEvidence(payload marketanalysis.Analysis, asset string) map[string]any {
	basis := currentMarketQuestionAssetBasis(payload)
	result := map[string]any{
		"source": basis.Source, "evidence_layer": basis.EvidenceLayer, "window_sec": basis.WindowSec,
		"as_of_ms": basis.AsOfMS, "asset_metrics": currentMarketQuestionAssetMetrics(payload, asset, basis.Complete),
		"trade_counts_available": !basis.Official && basis.Complete,
	}
	if basis.Official {
		result["snapshot_available"] = basis.Complete
		result["semantics"] = "FIVE_EXCHANGE_OFFICIAL_ROLLING_24H_AMOUNTS_NOT_WEBSOCKET_COVERAGE"
	} else {
		result["window_complete"] = basis.Complete
		result["coverage_sec"] = payload.Context.SelectedWindowCoverageSec
		result["required_sec"] = payload.Context.SelectedWindowRequiredSec
	}
	return result
}

func officialMarketQuestionAssetMetrics(comparison tickertrend.WindowComparison, asset string) *marketanalysis.MarketQuestionAssetInterval {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" {
		return nil
	}
	identity := marketanalysis.Identity(asset)
	result := &marketanalysis.MarketQuestionAssetInterval{
		Asset: asset, CanonicalAssetID: identity.CanonicalAssetID,
		ContractIdentityStatus: identity.ContractIdentityStatus, ContractIdentityVerified: identity.ContractIdentityVerified,
		ObservedExchanges: []string{},
	}
	if comparison.Status != "LIVE" || !comparison.EndpointComparison || comparison.Previous == nil || comparison.Current == nil {
		return result
	}
	for _, driver := range comparison.Drivers {
		if !strings.EqualFold(driver.Asset, asset) {
			continue
		}
		previousDomestic := driver.PreviousBithumbVolumeKRW + driver.PreviousCompetitorVolumeKRW
		currentDomestic := driver.CurrentBithumbVolumeKRW + driver.CurrentCompetitorVolumeKRW
		result.PreviousDomesticVolumeKRW = float64Pointer(previousDomestic)
		result.CurrentDomesticVolumeKRW = float64Pointer(currentDomestic)
		result.PreviousBithumbVolumeKRW = float64Pointer(driver.PreviousBithumbVolumeKRW)
		result.CurrentBithumbVolumeKRW = float64Pointer(driver.CurrentBithumbVolumeKRW)
		result.PreviousBithumbCapturePct = optionalMarketQuestionPercent(driver.PreviousBithumbVolumeKRW, previousDomestic)
		result.CurrentBithumbCapturePct = optionalMarketQuestionPercent(driver.CurrentBithumbVolumeKRW, currentDomestic)
		result.BithumbCaptureChangePP = optionalMarketQuestionDifference(result.CurrentBithumbCapturePct, result.PreviousBithumbCapturePct)
		result.PreviousShareWithinBithumbPct = optionalMarketQuestionPercent(driver.PreviousBithumbVolumeKRW, comparison.Previous.BithumbVolumeKRW)
		result.CurrentShareWithinBithumbPct = optionalMarketQuestionPercent(driver.CurrentBithumbVolumeKRW, comparison.Current.BithumbVolumeKRW)
		result.ShareWithinBithumbChangePP = optionalMarketQuestionDifference(result.CurrentShareWithinBithumbPct, result.PreviousShareWithinBithumbPct)
		result.PreviousTotalShareContributionPct = optionalMarketQuestionPercent(driver.PreviousBithumbVolumeKRW, comparison.Previous.TotalVolumeKRW)
		result.CurrentTotalShareContributionPct = optionalMarketQuestionPercent(driver.CurrentBithumbVolumeKRW, comparison.Current.TotalVolumeKRW)
		result.TotalShareContributionChangePP = optionalMarketQuestionDifference(result.CurrentTotalShareContributionPct, result.PreviousTotalShareContributionPct)
		result.ChangeAttributionPP = float64Pointer(driver.ContributionPP)
		return result
	}
	return result
}

func currentMarketQuestionAssetMetrics(payload marketanalysis.Analysis, asset string, exposeNumbers bool) *marketanalysis.MarketQuestionAssetInterval {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" {
		return nil
	}
	identity := marketanalysis.Identity(asset)
	result := &marketanalysis.MarketQuestionAssetInterval{
		Asset: asset, CanonicalAssetID: identity.CanonicalAssetID,
		ContractIdentityStatus: identity.ContractIdentityStatus, ContractIdentityVerified: identity.ContractIdentityVerified,
		ObservedExchanges: []string{},
	}
	basis := currentMarketQuestionAssetBasis(payload)
	if basis.Official && !basis.Complete {
		exposeNumbers = false
	}
	for _, value := range payload.Assets {
		if !strings.EqualFold(value.Asset, asset) {
			continue
		}
		result.CanonicalAssetID = value.AssetIdentity.CanonicalAssetID
		result.ContractIdentityStatus = value.AssetIdentity.ContractIdentityStatus
		result.ContractIdentityVerified = value.AssetIdentity.ContractIdentityVerified
		result.ObservedExchanges = append([]string(nil), value.DomesticExchanges...)
		if !exposeNumbers {
			return result
		}
		result.CurrentBithumbCapturePct = copyQuestionFloat(value.BithumbCapturePct)
		result.CurrentShareWithinBithumbPct = optionalMarketQuestionPercent(value.BithumbVolumeKRW, payload.Summary.BithumbVolumeKRW)
		result.CurrentTotalShareContributionPct = optionalMarketQuestionPercent(value.BithumbVolumeKRW, payload.Summary.DomesticVolumeKRW)
		result.CurrentDomesticVolumeKRW = copyQuestionFloat(value.DomesticVolumeKRW)
		result.CurrentBithumbVolumeKRW = float64Pointer(value.BithumbVolumeKRW)
		if !basis.Official {
			result.CurrentTradeCount = int64Pointer(value.DomesticTradeCount)
		}
		return result
	}
	return result
}

func buildCurrentMarketQuestionAssetFallback(
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	if len(query.ComparedAssets) > 1 {
		answer := marketanalysis.MarketQuestionAnswer{Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS}
		for _, asset := range query.ComparedAssets {
			item := query
			item.Asset, item.ComparedAssets = asset, nil
			part := buildCurrentMarketQuestionAssetFallback(payload, item, asOfMS)
			answer.Answer += part.Answer + "\n\n"
			answer.KeyPoints = append(answer.KeyPoints, part.KeyPoints...)
			answer.Caveat, answer.AsOfMS = part.Caveat, part.AsOfMS
		}
		answer.Answer = strings.TrimSpace(answer.Answer)
		if len(answer.KeyPoints) > marketQuestionPointLimit {
			answer.KeyPoints = answer.KeyPoints[:marketQuestionPointLimit]
		}
		return answer
	}
	basis := currentMarketQuestionAssetBasis(payload)
	if basis.Official {
		return buildCurrentOfficialAssetFallback(payload, query, basis)
	}
	complete := payload.Context.SelectedWindowComplete && payload.Summary.SelectedWindowComplete
	metrics := currentMarketQuestionAssetMetrics(payload, query.Asset, complete)
	windowLabel := marketQuestionWSWindowLabel(payload.WindowSec)
	if !complete {
		return marketanalysis.MarketQuestionAnswer{
			Answer: fmt.Sprintf(
				"%s의 현재 WebSocket rolling %s 자산 비중을 확정할 만큼 coverage가 완전하지 않습니다. 현재 coverage는 %d/%d초이며 불완전한 구간의 수치를 보완하지 않았습니다.",
				metrics.Asset, windowLabel, payload.Context.SelectedWindowCoverageSec, payload.Context.SelectedWindowRequiredSec,
			),
			KeyPoints: []string{fmt.Sprintf("coverage %d/%d초", payload.Context.SelectedWindowCoverageSec, payload.Context.SelectedWindowRequiredSec)},
			Caveat:    "이 자산 snapshot은 공식 정렬 시간봉이 아니라 WebSocket 직접 관측 rolling 구간입니다. coverage가 완전해진 뒤 수치를 확정할 수 있습니다.",
			Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}
	if metrics.CurrentDomesticVolumeKRW == nil {
		return marketanalysis.MarketQuestionAnswer{
			Answer: fmt.Sprintf(
				"WebSocket 직접 관측 rolling %s coverage는 완전하지만 %s의 비교 가능한 국내 체결은 없었습니다. 0원으로 추정하지 않고 미관측으로 구분합니다.",
				windowLabel, metrics.Asset,
			),
			KeyPoints: []string{metrics.Asset + " 비교 가능한 체결 미관측"},
			Caveat:    "미관측은 거래가 0원이었다는 뜻이 아닙니다. registry와 수집 coverage를 함께 확인해야 합니다.",
			Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}
	details := []string{}
	points := []string{}
	if detail, point := marketQuestionAssetAmounts(metrics); detail != "" {
		details, points = append(details, detail), append(points, point)
	}
	if metrics.CurrentBithumbCapturePct != nil {
		details = append(details, fmt.Sprintf("국내 %s 거래 중 빗썸 포착률은 %.2f%%", metrics.Asset, *metrics.CurrentBithumbCapturePct))
		points = append(points, fmt.Sprintf("%s 빗썸 포착률 %.2f%%", metrics.Asset, *metrics.CurrentBithumbCapturePct))
	}
	if metrics.CurrentShareWithinBithumbPct != nil {
		details = append(details, fmt.Sprintf("빗썸 거래대금 안에서 %s 비중은 %.2f%%", metrics.Asset, *metrics.CurrentShareWithinBithumbPct))
		points = append(points, fmt.Sprintf("빗썸 내부 %s 비중 %.2f%%", metrics.Asset, *metrics.CurrentShareWithinBithumbPct))
	}
	if metrics.CurrentTotalShareContributionPct != nil {
		details = append(details, fmt.Sprintf("국내 전체 거래대금 중 빗썸의 %s 거래가 차지한 몫은 %.2f%%", metrics.Asset, *metrics.CurrentTotalShareContributionPct))
		points = append(points, fmt.Sprintf("%s 전체 점유율 몫 %.2f%%", metrics.Asset, *metrics.CurrentTotalShareContributionPct))
	}
	if len(details) == 0 {
		details = append(details, metrics.Asset+"의 비교 가능한 체결이 없어 비중을 계산할 수 없습니다")
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: fmt.Sprintf(
			"WebSocket 직접 관측 rolling %s 기준 %s입니다. coverage는 %d/%d초로 완전합니다. 이 비율들은 분모가 서로 달라 같은 비율로 해석하면 안 됩니다.",
			windowLabel, strings.Join(details, ", "), payload.Context.SelectedWindowCoverageSec, payload.Context.SelectedWindowRequiredSec,
		),
		KeyPoints: append(points, fmt.Sprintf("coverage %d/%d초", payload.Context.SelectedWindowCoverageSec, payload.Context.SelectedWindowRequiredSec)),
		Caveat:    "현재 자산 snapshot은 공식 24시간 정렬봉 점유율이 아니라 WebSocket rolling 직접 관측값입니다. 거래대금 비중은 관측 사실이며 가격 방향이나 원인을 뜻하지 않습니다.",
		Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func buildCurrentOfficialAssetFallback(payload marketanalysis.Analysis, query marketanalysis.MarketQuestionQuery, basis marketQuestionAssetBasis) marketanalysis.MarketQuestionAnswer {
	metrics := currentMarketQuestionAssetMetrics(payload, query.Asset, basis.Complete)
	answer := marketanalysis.MarketQuestionAnswer{
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: basis.AsOfMS,
		Caveat: "국내 5개 거래소의 공식 rolling 24시간 티커 금액을 사용했습니다. 공식 정렬 시간봉과 집계 방식이 다르며 WebSocket 체결 관측의 완전성을 증명하지 않습니다. 이 티커에는 체결 건수가 없어 제공하지 않습니다.",
	}
	if !basis.Complete {
		answer.Answer = metrics.Asset + "의 공식 rolling 24시간 자산 표본이 LIVE 조건을 충족하지 않아 비중을 확정할 수 없습니다."
		answer.KeyPoints = []string{"공식 rolling 24시간 자산 표본 확정 전"}
		return answer
	}
	if metrics.CurrentDomesticVolumeKRW == nil {
		answer.Answer = metrics.Asset + "의 공식 rolling 24시간 자산 금액이 선택한 표본에 없어 비중을 계산할 수 없습니다. 없는 항목을 0원으로 추정하지 않았습니다."
		answer.KeyPoints = []string{metrics.Asset + " 공식 자산 금액 미확보"}
		return answer
	}
	details, points := []string{}, []string{}
	if detail, point := marketQuestionAssetAmounts(metrics); detail != "" {
		details, points = append(details, detail), append(points, point)
	}
	for _, value := range []struct {
		label  string
		number *float64
	}{
		{metrics.Asset + " 빗썸 포착률", metrics.CurrentBithumbCapturePct},
		{"빗썸 내부 " + metrics.Asset + " 비중", metrics.CurrentShareWithinBithumbPct},
		{metrics.Asset + " 국내 전체 점유율 몫", metrics.CurrentTotalShareContributionPct},
	} {
		if value.number != nil {
			point := fmt.Sprintf("%s %.2f%%", value.label, *value.number)
			details, points = append(details, point), append(points, point)
		}
	}
	answer.Answer = fmt.Sprintf("KST %s 기준 국내 5개 거래소 공식 rolling 24시간 티커에서 %s입니다. 포착률의 분모는 국내 같은 코인 거래대금, 내부 비중의 분모는 빗썸 전체 거래대금, 전체 점유율 몫의 분모는 국내 전체 거래대금입니다.", marketQuestionKST(basis.AsOfMS), strings.Join(details, ", "))
	answer.KeyPoints = append(points, "공식 티커 기준 · 체결 건수 미제공")
	return answer
}

func enrichCurrentMarketQuestionAssetFacts(
	answer marketanalysis.MarketQuestionAnswer,
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
) marketanalysis.MarketQuestionAnswer {
	if len(query.ComparedAssets) > 1 {
		return answer
	}
	if query.Intent != marketanalysis.MarketQuestionAssetCapture || strings.TrimSpace(query.Asset) == "" {
		return answer
	}
	if basis := currentMarketQuestionAssetBasis(payload); basis.Official {
		mandatory := buildCurrentOfficialAssetFallback(payload, query, basis).KeyPoints
		for _, point := range answer.KeyPoints {
			if len(mandatory) >= marketQuestionPointLimit {
				break
			}
			if !marketQuestionContainsString(mandatory, point) {
				mandatory = append(mandatory, point)
			}
		}
		answer.KeyPoints, answer.AsOfMS = mandatory, basis.AsOfMS
		return answer
	}
	complete := payload.Context.SelectedWindowComplete && payload.Summary.SelectedWindowComplete
	metrics := currentMarketQuestionAssetMetrics(payload, query.Asset, complete)
	mandatory := []string{}
	if !complete {
		mandatory = append(mandatory, fmt.Sprintf(
			"WebSocket rolling coverage %d/%d초 · 불완전하여 자산 수치를 확정하지 않음",
			payload.Context.SelectedWindowCoverageSec, payload.Context.SelectedWindowRequiredSec,
		))
	} else if metrics.CurrentDomesticVolumeKRW == nil {
		mandatory = append(mandatory, metrics.Asset+" 비교 가능한 국내 체결 미관측 · 0원으로 추정하지 않음")
	} else {
		if metrics.CurrentBithumbCapturePct != nil {
			mandatory = append(mandatory, fmt.Sprintf("%s 빗썸 포착률 %.2f%%", metrics.Asset, *metrics.CurrentBithumbCapturePct))
		}
		if metrics.CurrentShareWithinBithumbPct != nil {
			mandatory = append(mandatory, fmt.Sprintf("빗썸 내부 %s 비중 %.2f%%", metrics.Asset, *metrics.CurrentShareWithinBithumbPct))
		}
		if metrics.CurrentTotalShareContributionPct != nil {
			mandatory = append(mandatory, fmt.Sprintf("%s 국내 전체 점유율 몫 %.2f%%", metrics.Asset, *metrics.CurrentTotalShareContributionPct))
		}
		mandatory = append(mandatory, fmt.Sprintf(
			"WebSocket rolling coverage %d/%d초 · 완전",
			payload.Context.SelectedWindowCoverageSec, payload.Context.SelectedWindowRequiredSec,
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

func copyQuestionFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return float64Pointer(*value)
}

// marketQuestionAssetAmounts states a coin's Bithumb and domestic amounts once
// for the briefing sentence and its key point. Missing amounts stay unstated.
func marketQuestionAssetAmounts(metrics *marketanalysis.MarketQuestionAssetInterval) (string, string) {
	if metrics == nil || metrics.CurrentBithumbVolumeKRW == nil || metrics.CurrentDomesticVolumeKRW == nil {
		return "", ""
	}
	bithumb := formatQuestionKRWAmount(*metrics.CurrentBithumbVolumeKRW)
	domestic := formatQuestionKRWAmount(*metrics.CurrentDomesticVolumeKRW)
	return fmt.Sprintf("빗썸 %s 거래대금은 %s, 국내 %s 거래대금은 %s", metrics.Asset, bithumb, metrics.Asset, domestic),
		fmt.Sprintf("빗썸 %s 거래대금 %s · 국내 %s 거래대금 %s", metrics.Asset, bithumb, metrics.Asset, domestic)
}
