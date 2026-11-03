package analysis

import (
	"fmt"
	"math"
)

const minListingGapMarketSharePct = 0.5

func buildFindings(result Analysis) []Finding {
	findings := make([]Finding, 0, 7)
	findings = appendShareDriver(findings, result.ChangeAttribution)
	findings = appendListingGap(findings, result.Assets, result.Summary.DomesticVolumeKRW)
	findings = appendExclusiveStrength(findings, result.Assets)
	findings = appendCaptureGap(findings, result.Assets, result.Summary.BithumbSharePct)
	findings = appendQuoteGap(findings, result.QuoteMarkets, result.Summary.BithumbSharePct)
	findings = appendGlobalGap(findings, result.Assets)
	return appendMomentum(findings, result.Momentum)
}

func appendShareDriver(findings []Finding, attribution Attribution) []Finding {
	if attribution.Status != "CONFIRMED" || len(attribution.Assets) == 0 || attribution.ShareChangePP == nil || math.Abs(*attribution.ShareChangePP) <= 1e-12 {
		return findings
	}
	direction := "SHARE_GAIN_DRIVER"
	driver := attribution.Assets[0]
	for _, candidate := range attribution.Assets[1:] {
		if *attribution.ShareChangePP < 0 && candidate.ContributionPP < driver.ContributionPP {
			driver = candidate
		}
		if *attribution.ShareChangePP > 0 && candidate.ContributionPP > driver.ContributionPP {
			driver = candidate
		}
	}
	word := "상승"
	if *attribution.ShareChangePP < 0 {
		direction, word = "SHARE_LOSS_DRIVER", "하락"
	}
	return append(findings, Finding{
		Kind: direction, Title: fmt.Sprintf("%s 점유율 %s 기여", driver.Asset, word),
		Detail: "현재 구간과 직전 동일 구간의 자산별 거래금액을 비교해 점유율 변화에 가장 크게 기여한 자산을 계산한 결과입니다.",
		Asset:  driver.Asset, ValueKRW: pointer(driver.DomesticVolumeChangeKRW), ValuePct: pointer(driver.ContributionPP),
		Basis: []string{
			"산식: 자산별 빗썸 거래금액 변화와 경쟁 거래소 거래금액 변화를 점유율 기여도로 분해",
			fmt.Sprintf("점유율 기여: %+.3f%%p", driver.ContributionPP),
			fmt.Sprintf("빗썸 거래금액: %s원 → %s원", comma(driver.PreviousBithumbVolumeKRW), comma(driver.CurrentBithumbVolumeKRW)),
			fmt.Sprintf("경쟁 거래금액: %s원 → %s원", comma(driver.PreviousCompetitorVolumeKRW), comma(driver.CurrentCompetitorVolumeKRW)),
		},
	})
}

func appendListingGap(findings []Finding, assets []AssetAnalysis, domesticTotal float64) []Finding {
	var target *AssetAnalysis
	for index := range assets {
		if assets[index].GapType == "UNLISTED" {
			target = &assets[index]
			break
		}
	}
	if target == nil {
		return findings
	}
	share := percent(target.CompetitorVolumeKRW, domesticTotal)
	if share == nil || *share < minListingGapMarketSharePct {
		return findings
	}
	return append(findings, Finding{
		Kind: "LISTING_GAP", Title: target.Asset + " 미상장 공백",
		Detail: fmt.Sprintf("빗썸 미상장 자산 중 경쟁 거래소 거래금액이 가장 큰 항목이며, 선택 구간 국내 전체 거래금액의 %.2f%%를 차지합니다. 상장 필요성이나 원인을 단정하지 않습니다.", *share),
		Asset:  target.Asset, ValueKRW: pointer(target.CompetitorVolumeKRW),
		Basis: []string{
			"조건: 빗썸 미상장 + 국내 경쟁 거래소 거래금액 관측",
			fmt.Sprintf("국내 경쟁 거래금액: %s원", comma(target.CompetitorVolumeKRW)),
			fmt.Sprintf("해당 자산 국내 거래금액: %s원", comma(pointerValue(target.DomesticVolumeKRW))),
			fmt.Sprintf("선택 구간 국내 전체 거래금액: %s원", comma(domesticTotal)),
			fmt.Sprintf("국내 전체 거래금액 내 비중: %.2f%%", *share),
			fmt.Sprintf("표시 기준: 국내 전체 거래금액의 %.1f%% 이상", minListingGapMarketSharePct),
		},
	})
}

func appendExclusiveStrength(findings []Finding, assets []AssetAnalysis) []Finding {
	var target *AssetAnalysis
	for index := range assets {
		candidate := &assets[index]
		if candidate.ExclusiveOnBithumb && candidate.BithumbVolumeKRW > 0 && (target == nil || candidate.BithumbVolumeKRW > target.BithumbVolumeKRW) {
			target = candidate
		}
	}
	if target == nil {
		return findings
	}
	return append(findings, Finding{
		Kind: "EXCLUSIVE_LISTING_STRENGTH", Title: target.Asset + " 빗썸 단독상장 거래금액 상위",
		Detail: "현재 상장 목록에서 빗썸에만 있는 자산 중 선택 구간 거래금액이 가장 큰 항목입니다. 단독상장이 거래 증가의 원인이라는 뜻은 아닙니다.",
		Asset:  target.Asset, ValueKRW: pointer(target.BithumbVolumeKRW), ValuePct: target.BithumbCapturePct,
		Basis: []string{
			"조건: 국내 거래소 상장 집합에서 빗썸만 포함",
			fmt.Sprintf("빗썸 거래금액: %s원", comma(target.BithumbVolumeKRW)),
			fmt.Sprintf("해당 자산 국내 거래금액: %s원", comma(pointerValue(target.DomesticVolumeKRW))),
			fmt.Sprintf("빗썸 포착률: %.1f%%", pointerValue(target.BithumbCapturePct)),
		},
	})
}

func appendCaptureGap(findings []Finding, assets []AssetAnalysis, venueShare *float64) []Finding {
	var target *AssetAnalysis
	for index := range assets {
		candidate := &assets[index]
		if candidate.ListedOnBithumb && (candidate.GapType == "NO_RECENT_BITHUMB_VOLUME" || candidate.GapType == "BELOW_VENUE_BASELINE") {
			target = candidate
			break
		}
	}
	if target == nil {
		return findings
	}
	return append(findings, Finding{
		Kind: "CAPTURE_GAP", Title: target.Asset + " 포착률 부족",
		Detail: "빗썸에 상장된 자산이지만, 해당 자산 안에서 빗썸이 차지한 거래금액 비중이 국내 전체 점유율보다 낮습니다. 상장 자체가 원인이라는 뜻은 아닙니다.",
		Asset:  target.Asset, ValueKRW: pointer(target.CompetitorVolumeKRW), ValuePct: target.BithumbCapturePct,
		Basis: []string{
			fmt.Sprintf("해당 자산 빗썸 포착률: %.1f%%", pointerValue(target.BithumbCapturePct)),
			fmt.Sprintf("국내 전체 점유율 기준: %.1f%%", pointerValue(venueShare)),
			fmt.Sprintf("경쟁 거래금액: %s원", comma(target.CompetitorVolumeKRW)),
			fmt.Sprintf("빗썸 거래금액: %s원", comma(target.BithumbVolumeKRW)),
		},
	})
}

func appendQuoteGap(findings []Finding, quotes []QuoteAnalysis, venueShare *float64) []Finding {
	if venueShare == nil {
		return findings
	}
	gap, index := 0.0, -1
	for candidate := range quotes {
		row := quotes[candidate]
		if row.VolumeKRW <= 0 || row.BithumbCapturePct == nil {
			continue
		}
		value := max(0, *venueShare-*row.BithumbCapturePct) * row.VolumeKRW / 100
		if value > gap {
			gap, index = value, candidate
		}
	}
	if index < 0 || gap <= 0 {
		return findings
	}
	row := quotes[index]
	return append(findings, Finding{
		Kind: "QUOTE_GAP", Title: row.Quote + " 마켓 포착률",
		Detail: "기준통화별 거래금액을 합산했을 때 빗썸 포착률과 전체 점유율의 차이를 거래금액으로 가중해 계산한 값이 가장 큰 항목입니다.",
		Quote:  row.Quote, ValueKRW: pointer(row.VolumeKRW), ValuePct: row.BithumbCapturePct,
		Basis: []string{
			fmt.Sprintf("%s 마켓 거래금액: %s원", row.Quote, comma(row.VolumeKRW)),
			fmt.Sprintf("%s 빗썸 포착률: %.1f%%", row.Quote, pointerValue(row.BithumbCapturePct)),
			fmt.Sprintf("전체 빗썸 점유율 기준: %.1f%%", *venueShare),
			fmt.Sprintf("가중 공백 추정액: %s원", comma(gap)),
		},
	})
}

func appendGlobalGap(findings []Finding, assets []AssetAnalysis) []Finding {
	var target *AssetAnalysis
	var gap *float64
	for index := range assets {
		candidate := &assets[index]
		if !candidate.AssetIdentity.ContractIdentityVerified || pointerValue(candidate.GlobalVolumeKRW) <= 0 {
			continue
		}
		value := candidate.GlobalMinusDomesticInterestPP
		if value != nil && (gap == nil || *value > *gap) {
			target, gap = candidate, value
		}
	}
	if target == nil || gap == nil || *gap <= 0 {
		return findings
	}
	detail := fmt.Sprintf("Binance Spot 거래금액 비중 %.1f%%가 국내 거래금액 비중 %.1f%%보다 %.1f%%p 높은 자산입니다.", pointerValue(target.GlobalVolumeSharePct), pointerValue(target.DomesticVolumeSharePct), *gap)
	domesticBasis := fmt.Sprintf("국내 거래금액 비중: %.1f%%", pointerValue(target.DomesticVolumeSharePct))
	return append(findings, Finding{
		Kind: "GLOBAL_INTEREST_GAP", Title: target.Asset + " 글로벌 관심 격차", Detail: detail,
		Asset: target.Asset, ValueKRW: target.GlobalVolumeKRW, ValuePct: gap,
		Basis: []string{
			fmt.Sprintf("Binance Spot 거래금액: %s원", comma(pointerValue(target.GlobalVolumeKRW))),
			fmt.Sprintf("Binance Spot 거래금액 비중: %.1f%%", pointerValue(target.GlobalVolumeSharePct)), domesticBasis,
			fmt.Sprintf("비중 차이: %+.1f%%p", *gap),
		},
	})
}

func appendMomentum(findings []Finding, momentum []Momentum) []Finding {
	var short, hour *Momentum
	for index := range momentum {
		if momentum[index].Window == "10s" {
			short = &momentum[index]
		}
		if momentum[index].Window == "1h" {
			hour = &momentum[index]
		}
	}
	if short == nil || hour == nil || short.Status != "CONFIRMED" || hour.Status != "CONFIRMED" || short.SharePct == nil || hour.SharePct == nil {
		return findings
	}
	change := *short.SharePct - *hour.SharePct
	detail := "최근 10초와 1시간 점유율이 같습니다."
	if change > 0 {
		detail = fmt.Sprintf("최근 10초 점유율이 1시간 점유율보다 %.3f%%p 높습니다.", math.Abs(change))
	} else if change < 0 {
		detail = fmt.Sprintf("최근 10초 점유율이 1시간 점유율보다 %.3f%%p 낮습니다.", math.Abs(change))
	}
	detail += " 이 차이의 원인은 별도 자료가 필요합니다."
	return append(findings, Finding{
		Kind: "MOMENTUM", Title: "단기 점유율 모멘텀",
		Detail:   detail,
		ValuePct: pointer(change), Basis: []string{
			fmt.Sprintf("최근 10초 점유율: %.2f%%", *short.SharePct),
			fmt.Sprintf("최근 1시간 점유율: %.2f%%", *hour.SharePct),
			fmt.Sprintf("차이 계산: %.2f%% - %.2f%% = %+.3f%%p", *short.SharePct, *hour.SharePct, change),
		},
	})
}

func comma(value float64) string {
	text := fmt.Sprintf("%.0f", value)
	start := 0
	if len(text) > 0 && text[0] == '-' {
		start = 1
	}
	for index := len(text) - 3; index > start; index -= 3 {
		text = text[:index] + "," + text[index:]
	}
	return text
}
