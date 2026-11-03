package app

import (
	"fmt"
	"math"
	"slices"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
)

// A comparison is owned by two server facts from the same immutable sample.
// It is not an interval change or an attribution to price/event causality.
type marketQuestionFactComparison struct {
	Against       string   `json:"against"`
	Denominator   string   `json:"denominator"`
	Operation     string   `json:"operation"`
	SourceFactIDs []string `json:"source_fact_ids"`
}

func (b *marketQuestionLedgerBuilder) currentTickerComparisons(payload marketanalysis.Analysis, query marketanalysis.MarketQuestionQuery, period string) {
	if !slices.Contains(query.RequestedExplanation, "EXCHANGES") {
		return
	}
	target := query.Exchange
	if target == "" {
		target = "bithumb"
	}
	ticker := payload.OfficialDomestic24h
	total := 0.0
	venues := []domain.Exchange{domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeCoinone, domain.ExchangeKorbit, domain.ExchangeGopax}
	for _, venue := range venues {
		row, ok := ticker.Exchanges[venue]
		if !ok || row.Exchange != venue || row.Status != "LIVE" || row.VolumeKRW == nil || !finiteNonnegative(*row.VolumeKRW) || !marketQuestionLedgerPct(row.SharePercent) {
			return
		}
		if math.Abs(*row.SharePercent - *row.VolumeKRW / *ticker.TotalVolumeKRW * 100) > 1e-6 {
			return
		}
		total += *row.VolumeKRW
	}
	if math.Abs(total-*ticker.TotalVolumeKRW) > math.Max(0.01, *ticker.TotalVolumeKRW*1e-9) {
		return
	}
	targetRow, ok := ticker.Exchanges[domain.Exchange(target)]
	if !ok {
		return
	}
	source := map[string]string{}
	for _, fact := range b.facts {
		source[fact.Exchange+"/"+fact.Metric] = fact.ID
	}
	for _, peer := range venues {
		if string(peer) == target {
			continue
		}
		peerRow := ticker.Exchanges[peer]
		for _, metric := range []string{"EXCHANGE_SHARE_PCT", "EXCHANGE_VOLUME_KRW"} {
			ids := []string{source[target+"/"+metric], source[string(peer)+"/"+metric]}
			if ids[0] == "" || ids[1] == "" {
				continue
			}
			delta := *targetRow.VolumeKRW - *peerRow.VolumeKRW
			label, unit, comparisonMetric := "거래대금", formatQuestionKRWAmount(math.Abs(delta)), "EXCHANGE_VOLUME_GAP_KRW"
			higher, lower := "많습니다", "적습니다"
			if delta != 0 && math.Abs(delta) < 1 {
				unit = "1원 미만"
			}
			if metric == "EXCHANGE_SHARE_PCT" {
				delta = (*targetRow.VolumeKRW - *peerRow.VolumeKRW) / *ticker.TotalVolumeKRW * 100
				label, unit, comparisonMetric = "점유율", fmt.Sprintf("%.2f%%p", math.Abs(delta)), "EXCHANGE_SHARE_GAP_PP"
				higher, lower = "높습니다", "낮습니다"
				if delta != 0 && math.Abs(delta) < 0.005 {
					unit = "0.01%p 미만"
				}
			}
			direction := higher
			if delta < 0 {
				direction = lower
			}
			statement := fmt.Sprintf("같은 공식 표본에서 %s %s은 %s보다 %s %s.", rollingExchangeLabel(target), label, rollingExchangeLabel(string(peer)), unit, direction)
			if delta == 0 {
				statement = fmt.Sprintf("같은 공식 표본에서 %s %s의 %s은 같습니다(차이 %s).", koreanWithAnd(rollingExchangeLabel(target)), rollingExchangeLabel(string(peer)), label, unit)
			}
			b.add(marketQuestionDisplayFact{Text: statement, Metric: comparisonMetric, Exchange: target, Period: period, EvidenceLayer: "CURRENT_OFFICIAL_TICKER", Quality: "LIVE", Comparison: &marketQuestionFactComparison{Against: string(peer), Denominator: "domestic", Operation: "TARGET_MINUS_PEER", SourceFactIDs: ids}})
		}
	}
}
