package app

import (
	"context"
	"fmt"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/store/postgres"
)

// Keep candle facts separate from WS coverage, trade counts and 10-second signals.
// The basket contains only markets with every candle in BOTH windows.
func (s *Service) addQuestionCandleFallback(ctx context.Context, q marketanalysis.MarketQuestionQuery, w *marketanalysis.MarketQuestionIntervalEvidence) {
	if s.postgresRepository == nil || q.MarketScope != "KRW" || q.Asset != "" || q.PeerExchange != "" || w.WindowSec <= 0 || w.WindowSec%1800 != 0 {
		return
	}
	end := (w.AsOfMS/1000 - 120) / 1800 * 1800
	rows, err := s.postgresRepository.QuestionCandleMarkets(ctx, end-2*w.WindowSec, end-w.WindowSec, end)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("question candle comparison unavailable", "error", err)
		}
		return
	}
	w.CandleComparison = buildQuestionCandleComparison(rows, end, w.WindowSec, q.Exchange)
	if w.CandleComparison != nil {
		w.AsOfMS = w.CandleComparison.EndMS
	}
}

func buildQuestionCandleComparison(rows []postgres.QuestionCandleMarket, end, window int64, target string) *marketanalysis.MarketQuestionCandleComparison {
	if target == "" {
		target = "bithumb"
	}
	if target != "bithumb" && target != "upbit" && target != "coinone" {
		return nil
	}
	c := &marketanalysis.MarketQuestionCandleComparison{Source: "OFFICIAL_REST_CANDLE_MATCHED_MARKETS", Status: "PARTIAL_MARKET_SCOPE", Target: target, StartMS: (end - 2*window) * 1000, MiddleMS: (end - window) * 1000, EndMS: end * 1000, MarketCounts: map[string]int{}}
	var previous, current, pt, ct float64
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Exchange != "upbit" && r.Exchange != "bithumb" && r.Exchange != "coinone" || !finiteNonnegative(r.Previous) || !finiteNonnegative(r.Current) {
			return nil
		}
		key := r.Exchange + ":" + r.Market
		if seen[key] {
			return nil
		}
		seen[key] = true
		c.MarketCounts[r.Exchange]++
		previous += r.Previous
		current += r.Current
		if r.Exchange == target {
			pt += r.Previous
			ct += r.Current
		}
	}
	if !finiteNonnegative(previous) || !finiteNonnegative(current) || !finiteNonnegative(pt) || !finiteNonnegative(ct) || c.MarketCounts["upbit"] == 0 || c.MarketCounts["bithumb"] == 0 || c.MarketCounts["coinone"] == 0 || previous <= 0 || current <= 0 {
		return nil
	}
	c.PreviousVolumeKRW = pt
	c.CurrentVolumeKRW = ct
	c.VolumeDeltaKRW = ct - pt
	c.PreviousSharePct = pt / previous * 100
	c.CurrentSharePct = ct / current * 100
	c.ShareDeltaPP = c.CurrentSharePct - c.PreviousSharePct
	c.PreviousDenominatorKRW = previous
	c.CurrentDenominatorKRW = current
	c.ExcludedExchanges = map[string]string{"korbit": "EXACT_KRW_CANDLE_VOLUME_UNAVAILABLE", "gopax": "EXACT_KRW_CANDLE_VOLUME_UNAVAILABLE"}
	return c
}

func questionCandleBriefing(c *marketanalysis.MarketQuestionCandleComparison) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("공식 봉의 동일 종목 묶음 기준 비교입니다. 직전 KST [%s, %s), 최근 KST [%s, %s). 업비트 %d개·빗썸 %d개·코인원 %d개 시장 중 두 구간의 모든 봉이 확보된 동일 종목만 포함합니다.\n\n%s 동일 종목 묶음 거래대금: 직전 %s → 최근 %s, 변화 %s. 동일 종목 묶음 기준 점유율: 직전 %.2f%% → 최근 %.2f%%, 변화 %+.2f%%p. 분모는 포함된 업비트·빗썸·코인원 종목의 거래대금 합계입니다. 동일 종목 묶음의 국내 거래대금: 직전 %s → 최근 %s.\n\n전체 거래소·전체 종목 점유율은 자료가 부족하여 확정할 수 없습니다. 코빗·고팍스는 정확한 원화 거래대금이 없으며, 봉이 빠진 종목도 제외했습니다. 이 결과를 전체 시장으로 일반화하거나 WebSocket 누락을 복원한 값으로 해석하지 않습니다. 공식 봉은 10초 지표와 합산하지 않으며, 요청한 최신 분 경계와 실제 봉 마감 경계는 다릅니다.", marketQuestionLedgerKST(c.StartMS), marketQuestionLedgerKST(c.MiddleMS), marketQuestionLedgerKST(c.MiddleMS), marketQuestionLedgerKST(c.EndMS), c.MarketCounts["upbit"], c.MarketCounts["bithumb"], c.MarketCounts["coinone"], rollingExchangeLabel(c.Target), formatQuestionKRW(c.PreviousVolumeKRW), formatQuestionKRW(c.CurrentVolumeKRW), formatQuestionKRW(c.VolumeDeltaKRW), c.PreviousSharePct, c.CurrentSharePct, c.ShareDeltaPP, formatQuestionKRW(c.PreviousDenominatorKRW), formatQuestionKRW(c.CurrentDenominatorKRW))
}

func questionCandleSafetyAllowed(text string, c *marketanalysis.MarketQuestionCandleComparison) bool {
	// Preserve an explicit limitation and the selected basket in every model answer.
	text = strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	if c == nil || !strings.Contains(text, "공식") || !strings.Contains(text, "동일 종목") {
		return false
	}
	for _, sentence := range marketQuestionBindingSentence.Split(text, -1) {
		hasValue, _ := marketQuestionBindingNumericKinds(sentence)
		if hasValue && strings.Contains(sentence, "전체") && !marketQuestionUncertaintyClaim(sentence) &&
			(!strings.Contains(sentence, "동일 종목") || strings.Contains(sentence, "전체 시장") || strings.Contains(sentence, "전체 거래소") || strings.Contains(sentence, "국내 전체")) {
			return false
		}
	}
	return true
}

func (b *marketQuestionLedgerBuilder) candle(c *marketanalysis.MarketQuestionCandleComparison) {
	if c == nil {
		return
	}
	period := fmt.Sprintf("공식 봉 동일 종목 묶음 기준 · 직전 KST [%s, %s), 최근 KST [%s, %s)", marketQuestionLedgerKST(c.StartMS), marketQuestionLedgerKST(c.MiddleMS), marketQuestionLedgerKST(c.MiddleMS), marketQuestionLedgerKST(c.EndMS))
	add := func(metric, exchange, text string) {
		b.add(marketQuestionDisplayFact{Text: period + ": " + text, Period: period, Metric: metric, Exchange: exchange, EvidenceLayer: c.Source, Quality: c.Status})
	}
	label := rollingExchangeLabel(c.Target)
	add("EXCHANGE_VOLUME_KRW", c.Target, fmt.Sprintf("%s 동일 종목 묶음 거래대금: 직전 %s → 최근 %s", label, formatQuestionKRWAmount(c.PreviousVolumeKRW), formatQuestionKRWAmount(c.CurrentVolumeKRW)))
	add("VOLUME_DELTA_KRW", c.Target, label+" 동일 종목 묶음 거래대금 변화 "+formatQuestionKRW(c.VolumeDeltaKRW))
	add("EXCHANGE_SHARE_PCT", c.Target, fmt.Sprintf("%s 동일 종목 묶음 점유율: 직전 %.2f%% → 최근 %.2f%%", label, c.PreviousSharePct, c.CurrentSharePct))
	add("SHARE_DELTA_PP", c.Target, fmt.Sprintf("%s 동일 종목 묶음 점유율 변화 %+.2f%%p", label, c.ShareDeltaPP))
	add("DOMESTIC_VOLUME_KRW", "domestic", fmt.Sprintf("포함된 업비트·빗썸·코인원 동일 종목 묶음의 분모 거래대금: 직전 %s → 최근 %s", formatQuestionKRWAmount(c.PreviousDenominatorKRW), formatQuestionKRWAmount(c.CurrentDenominatorKRW)))
}
