package app

import (
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

func useDeterministicOfficialEvidencePlan(question string, history []marketanalysis.MarketQuestionConversationTurn, q marketanalysis.MarketQuestionQuery, err error) bool {
	if err != nil || len(history) != 0 || q.MarketScope != "KRW" || q.Asset != "" || q.PeerExchange != "" || len(q.ComparedAssets) != 0 {
		return false
	}
	compact := strings.Join(strings.Fields(question), "")
	if q.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
		return strings.Contains(compact, "공식24시간거래대금") || strings.Contains(compact, "공식24시간거래금액")
	}
	window, ok := marketQuestionWSWindowSec(q)
	return ok && window > 0 && window%1800 == 0 && (strings.Contains(compact, "공식분봉") || strings.Contains(compact, "공식봉"))
}

// Current and relative analyses use verified realtime results as context.
// Calendar/historical requests retain their own observation-time contract.
func marketQuestionDefaultRealtimeContext(q marketanalysis.MarketQuestionQuery) marketanalysis.MarketQuestionQuery {
	if q.Intent != marketanalysis.MarketQuestionCurrentOfficialShare &&
		q.Intent != marketanalysis.MarketQuestionOfficialWindowChange &&
		q.Intent != marketanalysis.MarketQuestionWSIntervalCompare {
		return q
	}
	if !marketQuestionQueryRequestsExplanation(q, "REALTIME") {
		q.RequestedExplanation = append(append([]string(nil), q.RequestedExplanation...), "REALTIME")
	}
	return q
}
