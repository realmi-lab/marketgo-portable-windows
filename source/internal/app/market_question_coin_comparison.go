package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
)

// Restrict the additional breakdown to an explicit pair and a coin comparison.
// Negated venues and three-way requests remain with normal query admission.
func marketQuestionCoinPair(question string) (string, string, bool) {
	if !containsAnyString(marketQuestionWithoutExchangeNames(question), "코인", "종목") || !containsAnyString(question, "비교", "우위", "높은", "많은", "강한", "강해", "강세", "몰린", "몰려", "앞선") {
		return "", "", false
	}
	return marketQuestionExplicitPair(question)
}

func marketQuestionExplicitPair(question string) (string, string, bool) {
	upper := strings.ToUpper(question)
	positions := map[string]int{}
	subject := ""
	for alias, venue := range marketQuestionExchangeNames {
		a := strings.ToUpper(alias)
		present, negated := marketQuestionTargetAliasPresent(upper, a)
		if negated {
			return "", "", false
		}
		if !present {
			continue
		}
		p := strings.Index(upper, a)
		if old, ok := positions[venue]; !ok || p < old {
			positions[venue] = p
		}
		for _, suffix := range []string{"가", "이"} {
			if strings.Contains(upper, a+suffix) || strings.Contains(upper, a+" "+suffix) {
				if subject != "" && subject != venue {
					return "", "", false
				}
				subject = venue
			}
		}
	}
	if len(positions) != 2 {
		return "", "", false
	}
	venues := make([]string, 0, 2)
	for v := range positions {
		venues = append(venues, v)
	}
	sort.Slice(venues, func(i, j int) bool { return positions[venues[i]] < positions[venues[j]] })
	if subject == venues[1] {
		venues[0], venues[1] = venues[1], venues[0]
	}
	return venues[0], venues[1], true
}

// Current official facts already retain Bithumb and one target exchange on
// the same five-exchange denominator. Do not broaden this to other pairs.
func marketQuestionCurrentSharePair(question string) (string, bool) {
	content := marketQuestionWithoutExchangeNames(question)
	if !containsAnyString(question, "점유율", "비중") || !containsAnyString(question, "비교", "대비") || containsAnyString(content, "코인", "종목", "변화", "상승", "하락", "제외", "빼고", "말고", "아닌") {
		return "", false
	}
	a, b, ok := marketQuestionExplicitPair(question)
	if !ok {
		return "", false
	}
	if a == "bithumb" {
		return b, true
	}
	if b == "bithumb" {
		return a, true
	}
	return "", false
}

func marketQuestionWithoutExchangeNames(question string) string {
	content := strings.ToUpper(question)
	for alias := range marketQuestionExchangeNames {
		content = strings.ReplaceAll(content, strings.ToUpper(alias), "")
	}
	return content
}

func buildMarketQuestionCoinComparison(query marketanalysis.MarketQuestionQuery, plan marketQuestionIntervalPlan, rows []marketanalysis.Row, observed coverage.Result) *marketanalysis.MarketQuestionCoinComparison {
	if !marketQuestionLedgerVenue(query.Exchange) || !marketQuestionLedgerVenue(query.PeerExchange) || query.Exchange == query.PeerExchange {
		return nil
	}
	c := &marketanalysis.MarketQuestionCoinComparison{Status: "UNAVAILABLE", Target: query.Exchange, Peer: query.PeerExchange,
		StartMS: plan.currentStart * 1000, EndMS: (plan.currentEnd + 1) * 1000, Denominator: "DOMESTIC_SAME_ASSET_KRW", Rows: []marketanalysis.MarketQuestionCoinComparisonRow{}}
	if !observed.WindowComplete || observed.CoverageSec < plan.windowSec {
		return c
	}
	type total struct {
		amount, quantity float64
		quantityValid    bool
		seen             bool
	}
	assets := map[string]map[string]total{}
	seenVenues := map[string]bool{}
	domestic := 0.0
	for _, row := range rows {
		venue, asset := string(row.Exchange), strings.ToUpper(strings.TrimSpace(row.Base))
		if !marketQuestionLedgerVenue(venue) {
			continue
		}
		if !finiteNonnegative(row.VolumeKRW) || row.UnresolvedFXCount != 0 {
			return c
		}
		domestic += row.VolumeKRW
		if asset == "" {
			return c
		}
		seenVenues[venue] = true
		if assets[asset] == nil {
			assets[asset] = map[string]total{}
		}
		t := assets[asset][venue]
		validQuantity := finiteNonnegative(row.BaseQuantity) && !row.TradeDetailIncomplete && row.SupplementalVolumeKRW == 0 && row.OfficialReplacementVolumeKRW == 0 && (row.VolumeKRW == 0 || row.BaseQuantity > 0)
		if !t.seen {
			t.quantityValid = true
		}
		t.seen, t.quantityValid = true, t.quantityValid && validQuantity
		t.amount += row.VolumeKRW
		t.quantity += row.BaseQuantity
		assets[asset][venue] = t
	}
	if !finitePositive(domestic) || !seenVenues[query.Exchange] || !seenVenues[query.PeerExchange] {
		return c
	}
	var targetVolume, peerVolume float64
	for asset, venues := range assets {
		a, b := venues[query.Exchange], venues[query.PeerExchange]
		targetVolume += a.amount
		peerVolume += b.amount
		// A missing peer row is not evidence of listing or an observed zero.
		if !a.seen || !b.seen {
			continue
		}
		c.EligibleCount++
		if a.amount <= b.amount {
			continue
		}
		totalAmount := 0.0
		for _, v := range venues {
			totalAmount += v.amount
		}
		if !finitePositive(totalAmount) {
			continue
		}
		r := marketanalysis.MarketQuestionCoinComparisonRow{Asset: asset, TargetVolumeKRW: a.amount, PeerVolumeKRW: b.amount,
			TargetSharePct: a.amount / totalAmount * 100, PeerSharePct: b.amount / totalAmount * 100, ShareGapPP: (a.amount - b.amount) / totalAmount * 100}
		if a.quantityValid {
			r.TargetBaseQuantity = float64Pointer(a.quantity)
		}
		if b.quantityValid {
			r.PeerBaseQuantity = float64Pointer(b.quantity)
		}
		c.Rows = append(c.Rows, r)
	}
	c.Status = "CONFIRMED"
	c.TargetVolumeKRW, c.PeerVolumeKRW = float64Pointer(targetVolume), float64Pointer(peerVolume)
	c.TargetSharePct, c.PeerSharePct = float64Pointer(targetVolume/domestic*100), float64Pointer(peerVolume/domestic*100)
	sort.Slice(c.Rows, func(i, j int) bool {
		if c.Rows[i].TargetVolumeKRW == c.Rows[j].TargetVolumeKRW {
			return c.Rows[i].Asset < c.Rows[j].Asset
		}
		return c.Rows[i].TargetVolumeKRW > c.Rows[j].TargetVolumeKRW
	})
	c.LeadingCount = len(c.Rows)
	if len(c.Rows) > 20 {
		c.Rows = c.Rows[:20]
	}
	return c
}

// This server-owned numeric appendix survives provider failure/omission. The
// model still owns validated explanation; it cannot rewrite the computed cells.
func marketQuestionCoinComparisonReport(c *marketanalysis.MarketQuestionCoinComparison) string {
	if c == nil {
		return ""
	}
	a, b := rollingExchangeLabel(c.Target), rollingExchangeLabel(c.Peer)
	if c.Status != "CONFIRMED" || c.TargetVolumeKRW == nil || c.PeerVolumeKRW == nil || c.TargetSharePct == nil || c.PeerSharePct == nil {
		return "요청한 코인별 거래소 비교는 해당 구간의 직접 관측 자료가 불완전하여 확정할 수 없습니다. 미확인 거래량과 점유율은 0으로 채우지 않았습니다."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "### 같은 구간 거래소·코인 비교\n\nKST %s부터 %s 직전까지 직접 관측한 값입니다.\n\n| 거래소 | 거래대금 | 국내 전체 점유율 |\n|---|---:|---:|\n| %s | %s | %.2f%% |\n| %s | %s | %.2f%% |\n\n", marketQuestionKST(c.StartMS), marketQuestionKST(c.EndMS), a, formatQuestionKRWAmount(*c.TargetVolumeKRW), *c.TargetSharePct, b, formatQuestionKRWAmount(*c.PeerVolumeKRW), *c.PeerSharePct)
	fmt.Fprintf(&out, "양쪽 관측 행이 있는 %d개 코인 중 %s 거래대금이 더 큰 코인은 %d개이며, 아래는 %s 거래대금 내림차순 %d개입니다. 변화 기여 순위나 전체 상장 목록이 아닙니다.\n\n", c.EligibleCount, a, c.LeadingCount, a, len(c.Rows))
	if len(c.Rows) == 0 {
		return out.String()
	}
	fmt.Fprintf(&out, "코인별 점유율 분모는 국내 동일 코인의 전체 거래대금입니다. 거래량은 코인 수량이며 거래대금·체결 건수와 다릅니다.\n\n| 코인 | %s 거래대금 | %s 거래량 | %s 점유율 | %s 거래대금 | %s 거래량 | %s 점유율 | 점유율 격차 |\n|---|---:|---:|---:|---:|---:|---:|---:|\n", a, a, a, b, b, b)
	quantity := func(v *float64) string {
		if v == nil {
			return "미확인"
		}
		return strconv.FormatFloat(*v, 'f', -1, 64) + "개"
	}
	for _, r := range c.Rows {
		fmt.Fprintf(&out, "| %s | %s | %s | %.2f%% | %s | %s | %.2f%% | +%.2f%%p |\n", r.Asset, formatQuestionKRWAmount(r.TargetVolumeKRW), quantity(r.TargetBaseQuantity), r.TargetSharePct, formatQuestionKRWAmount(r.PeerVolumeKRW), quantity(r.PeerBaseQuantity), r.PeerSharePct, r.ShareGapPP)
	}
	out.WriteString("\n점유율 격차는 같은 구간의 두 거래소 차이입니다. 직전 구간 대비 증감이나 실제 자금 이동을 뜻하지 않습니다. 심볼 기준 비교이며 동일 심볼의 계약 동일성까지 보장하지 않습니다.")
	return out.String()
}
