package app

import (
	"fmt"
	"math"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/tickertrend"
)

func marketQuestionUsesNonBithumbTarget(evidence marketanalysis.MarketQuestionIntervalEvidence) bool {
	return evidence.TargetExchange != nil && strings.TrimSpace(evidence.TargetExchange.Exchange) != "" &&
		!strings.EqualFold(strings.TrimSpace(evidence.TargetExchange.Exchange), string(domain.ExchangeBithumb))
}

// These display facts enlarge the answer model's usable evidence, not its
// arithmetic authority. The existing numeric guard still accepts only values
// formatted by the server briefing; no raw evidence number is allowlisted.
func officialMarketQuestionEndpointDetail(comparison tickertrend.WindowComparison) string {
	previous, current := comparison.Previous, comparison.Current
	if comparison.Status != "LIVE" || !comparison.EndpointComparison || previous == nil || current == nil {
		return ""
	}
	for _, point := range []*tickertrend.Point{previous, current} {
		if !finitePositive(point.TotalVolumeKRW) || !finiteNonnegative(point.BithumbVolumeKRW) || point.BithumbVolumeKRW > point.TotalVolumeKRW {
			return ""
		}
	}
	text := fmt.Sprintf("시작 시점 국내(업비트·빗썸·코인원) 거래대금은 %s, 빗썸은 %s이며 종료 시점은 각각 %s, %s입니다.",
		formatQuestionKRWAmount(previous.TotalVolumeKRW), formatQuestionKRWAmount(previous.BithumbVolumeKRW),
		formatQuestionKRWAmount(current.TotalVolumeKRW), formatQuestionKRWAmount(current.BithumbVolumeKRW))
	if previous.BucketMS > 0 && current.BucketMS > previous.BucketMS &&
		comparison.WindowSec > 0 && (current.BucketMS-previous.BucketMS)%1000 == 0 &&
		(current.BucketMS-previous.BucketMS)/1000 == comparison.WindowSec {
		text += fmt.Sprintf(" 비교 시점은 KST %s → %s입니다.", marketQuestionKST(previous.BucketMS), marketQuestionKST(current.BucketMS))
	}
	return text + " 각 금액은 해당 시점의 rolling 24시간 합계이며, 두 시점 사이에 새로 발생한 거래대금이 아닙니다."
}

func marketQuestionPairedAmount(label string, previous, current *float64) string {
	if previous == nil || current == nil || !finiteNonnegative(*previous) || !finiteNonnegative(*current) {
		return ""
	}
	return fmt.Sprintf("%s: 직전 %s → 최근 %s", label, formatQuestionKRWAmount(*previous), formatQuestionKRWAmount(*current))
}

func marketQuestionPairedCount(label string, previous, current *int64) string {
	if previous == nil || current == nil || *previous < 0 || *current < 0 {
		return ""
	}
	return fmt.Sprintf("%s: 직전 %d건 → 최근 %d건", label, *previous, *current)
}

func marketQuestionIntervalDetail(evidence marketanalysis.MarketQuestionIntervalEvidence) string {
	if evidence.Status != "CONFIRMED" {
		return ""
	}
	details := []string{}
	appendDetail := func(value string) {
		if value != "" {
			details = append(details, value)
		}
	}
	appendDetail(marketQuestionPairedAmount("국내 전체 거래대금", evidence.Previous.DomesticVolumeKRW, evidence.Current.DomesticVolumeKRW))
	appendDetail(marketQuestionPairedAmount("빗썸 거래대금", evidence.Previous.BithumbVolumeKRW, evidence.Current.BithumbVolumeKRW))
	appendDetail(marketQuestionPairedCount("국내 전체 관측 체결", evidence.Previous.DomesticTradeCount, evidence.Current.DomesticTradeCount))
	appendDetail(marketQuestionPairedCount("빗썸 관측 체결", evidence.Previous.BithumbTradeCount, evidence.Current.BithumbTradeCount))
	if asset := evidence.Asset; asset != nil && strings.TrimSpace(asset.Asset) != "" {
		appendDetail(marketQuestionPairedAmount(asset.Asset+" 국내 거래대금", asset.PreviousDomesticVolumeKRW, asset.CurrentDomesticVolumeKRW))
		appendDetail(marketQuestionPairedAmount(asset.Asset+" 빗썸 거래대금", asset.PreviousBithumbVolumeKRW, asset.CurrentBithumbVolumeKRW))
		appendDetail(marketQuestionPairedCount(asset.Asset+" 국내 관측 체결", asset.PreviousTradeCount, asset.CurrentTradeCount))
	}
	if len(details) == 0 {
		return ""
	}
	return "같은 길이의 직접 관측 구간별 실제 합계입니다.\n\n- " + strings.Join(details, "\n- ") +
		"\n\n체결 건수는 거래금액과 별도 지표이며, 거래에 참여한 사람 수를 뜻하지 않습니다."
}

func marketQuestionIntervalDriverDetail(evidence marketanalysis.MarketQuestionIntervalEvidence) string {
	if evidence.Status != "CONFIRMED" {
		return ""
	}
	positive, negative := []string{}, []string{}
	for _, driver := range balancedMarketQuestionAttributionDrivers(evidence.Drivers, 5) {
		if strings.TrimSpace(driver.Asset) == "" || !finiteMovementNumber(driver.ContributionPP) ||
			!finiteMovementNumber(driver.BithumbVolumeChangeKRW) || !finiteMovementNumber(driver.CompetitorVolumeChangeKRW) {
			continue
		}
		detail := fmt.Sprintf("%s %+.2f%%p · 빗썸 거래대금 변화 %s / 경쟁 거래소 변화 %s",
			driver.Asset, driver.ContributionPP, formatQuestionKRW(driver.BithumbVolumeChangeKRW), formatQuestionKRW(driver.CompetitorVolumeChangeKRW))
		if math.Abs(driver.ContributionPP) < 0.005 {
			detail += " (표시 정밀도 미만의 기여)"
		}
		if driver.ContributionPP > 0 {
			positive = append(positive, detail)
		} else if driver.ContributionPP < 0 {
			negative = append(negative, detail)
		}
	}
	sections := []string{}
	if len(positive) > 0 {
		sections = append(sections, "상승 기여 코인:\n- "+strings.Join(positive, "\n- "))
	}
	if len(negative) > 0 {
		sections = append(sections, "하락 기여 코인:\n- "+strings.Join(negative, "\n- "))
	}
	exchanges := []string{}
	for _, driver := range evidence.ExchangeDrivers[:min(5, len(evidence.ExchangeDrivers))] {
		if !finiteMovementNumber(driver.ContributionPP) || !finiteMovementNumber(driver.VolumeChangeKRW) {
			continue
		}
		exchanges = append(exchanges, fmt.Sprintf("%s: 거래대금 변화 %s · 빗썸 점유율 산술 기여 %+.2f%%p",
			rollingExchangeLabel(string(driver.Exchange)), formatQuestionKRW(driver.VolumeChangeKRW), driver.ContributionPP))
	}
	if len(exchanges) > 0 {
		sections = append(sections, "거래소별 기여:\n- "+strings.Join(exchanges, "\n- "))
	}
	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n") + "\n\n각 기여는 빗썸 점유율 변화의 산술 분해입니다. 거래소별 기여와 코인별 기여는 같은 변화를 서로 다르게 나눈 것이므로 다시 합산하지 않습니다. 공지나 가격이 원인임을 증명하지 않습니다."
}
