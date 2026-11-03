package app

import (
	"errors"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
)

var errUnsupportedDailyEndpointScope = errors.New(
	"daily endpoint series supports only overall Bithumb share in the exact KRW three-exchange history",
)

// validateMarketQuestionDailyEndpointScope keeps the daily endpoint calculator
// inside the contract its persisted rows can actually prove. In particular,
// attribution rows for many assets do not turn the series into a single-asset
// metric, and the current five-exchange converted scope is a separate evidence
// layer.
func validateMarketQuestionDailyEndpointScope(query marketanalysis.MarketQuestionQuery) error {
	if !marketQuestionDailyEndpointSeriesRequested(query) {
		return nil
	}
	if strings.ToUpper(strings.TrimSpace(query.MarketScope)) != "KRW" ||
		strings.TrimSpace(query.Asset) != "" ||
		query.Metric != marketanalysis.MarketQuestionMetricOverallShare {
		return errUnsupportedDailyEndpointScope
	}
	exchange := strings.ToLower(strings.TrimSpace(query.Exchange))
	if exchange != "" && exchange != string(domain.ExchangeBithumb) {
		return errUnsupportedDailyEndpointScope
	}
	return nil
}

// validateMarketQuestionPlanExplicitSemantics verifies meaning that the user
// stated independently of the planner JSON. This catches a planner that keeps
// the dates but silently drops an exchange, asset alias, or converted scope.
func validateMarketQuestionPlanExplicitSemantics(
	question string,
	query marketanalysis.MarketQuestionQuery,
	assetAliases map[string]string,
	reference time.Time,
) error {
	if err := validateMarketQuestionPlanExplicitAuxiliary(question, query); err != nil {
		return err
	}
	if marketQuestionRelativeMonthPattern.MatchString(question) {
		expected, err := parseMarketQuestionAt(question, assetAliases, reference)
		if err != nil {
			return err
		}
		if query.Intent != expected.Intent || query.Comparison != expected.Comparison ||
			query.Exchange != expected.Exchange || query.Metric != expected.Metric ||
			query.TargetStartMS != expected.TargetStartMS || query.TargetEndMS != expected.TargetEndMS {
			return errors.New("planner did not preserve the requested 30-day historical range")
		}
	}
	if err := validateMarketQuestionPlanExplicitDates(question, query, reference); err != nil {
		return err
	}
	if err := validateMarketQuestionPlanExplicitTargetsAndWindow(question, query, assetAliases, reference); err != nil {
		return err
	}
	if !marketQuestionDailyEndpointSeriesRequested(query) {
		return nil
	}
	if err := validateMarketQuestionDailyEndpointScope(query); err != nil {
		return err
	}
	asset, err := parseMarketQuestionAsset(question, assetAliases)
	if err != nil {
		return err
	}
	if asset != "" {
		return errUnsupportedDailyEndpointScope
	}
	exchange, err := parseMarketQuestionExchange(question)
	if err != nil {
		return err
	}
	if exchange != "" && exchange != string(domain.ExchangeBithumb) {
		return errUnsupportedDailyEndpointScope
	}
	if marketQuestionExplicitAllDomesticConverted(question) {
		return errUnsupportedDailyEndpointScope
	}
	return nil
}

// validateMarketQuestionPlanExplicitAuxiliary prevents a broad analysis plan
// from quietly mixing short realtime flow, orderbook, or notice context into an
// official answer. These layers are useful only when the user actually asks
// for them; a bare "current share" or "change drivers" request is not enough.
func validateMarketQuestionPlanExplicitAuxiliary(
	question string,
	query marketanalysis.MarketQuestionQuery,
) error {
	compact := strings.ReplaceAll(strings.TrimSpace(question), " ", "")
	realtimeRequested := containsAnyString(
		compact,
		"지금도", "현재도", "실시간", "방금", "현재흐름", "최근흐름", "단기흐름",
	)
	if marketQuestionDailyEndpointSeriesRequested(query) {
		realtimeRequested = marketQuestionRequestsDailyRealtime(question)
	}
	expected := map[string]bool{
		"REALTIME":  realtimeRequested,
		"ORDERBOOK": containsAnyString(compact, "호가", "슬리피지"),
		"NOTICES":   marketQuestionRequestsNotices(question),
	}
	for _, explanation := range []string{"REALTIME", "ORDERBOOK", "NOTICES"} {
		selected := marketQuestionQueryRequestsExplanation(query, explanation)
		if selected == expected[explanation] {
			continue
		}
		if selected {
			return errors.New("planner added unrequested " + strings.ToLower(explanation) + " context")
		}
		return errors.New("planner omitted requested " + strings.ToLower(explanation) + " context")
	}
	return nil
}

func marketQuestionExplicitAllDomesticConverted(question string) bool {
	compact := strings.ToUpper(strings.Join(strings.Fields(question), ""))
	return strings.Contains(compact, "ALL_DOMESTIC_CONVERTED") ||
		strings.Contains(compact, "전체환산")
}
