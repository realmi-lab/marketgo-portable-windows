package app

import (
	"regexp"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

// A scope antecedent carries an evidence-checked entity/metric, not a scalar.
// It exists for one adjacent sentence only; unrelated prose, tables, headings
// and blank paragraphs clear it. Numeric equality never creates a scope.
type marketQuestionBindingScope struct {
	exchange          string
	metric            string
	candleDenominator bool
}

var marketQuestionBindingScopeReference = regexp.MustCompile(`^\s*그\s*합계\s*(?:는|가|:)?\s*$`)
var marketQuestionBindingCandleDenominatorReference = regexp.MustCompile(`^\s*(?:이|그)\s*분모\s*거래(?:대금|금액)(?:은|는|:)?\s*(?:(?:직전|최근)\s*)?(?:[0-9,.]+(?:조원|억원|만원|원)\s*(?:에서|→)\s*(?:최근|현재)\s*)?$`)

func marketQuestionBindingDeclaredScope(sentence string, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery) marketQuestionBindingScope {
	if query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval && query.Asset == "" &&
		strings.Contains(sentence, "동일 종목") && strings.Contains(sentence, "합계") &&
		marketQuestionBindingExplicitDenominatorWithFacts(sentence, facts, query) == "domestic" {
		return marketQuestionBindingScope{exchange: "domestic", metric: "DOMESTIC_VOLUME_KRW", candleDenominator: true}
	}
	if query.Intent != marketanalysis.MarketQuestionCurrentOfficialShare || query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot || query.Asset != "" || strings.Contains(sentence, "|") {
		return marketQuestionBindingScope{}
	}
	// The declaration must consume a complete amount-summing predicate. It may
	// name the result a sample, but cannot omit the metric or invent membership.
	text := strings.TrimSpace(sentence)
	if !strings.Contains(text, "합") || !strings.HasSuffix(text, "표본입니다") {
		return marketQuestionBindingScope{}
	}
	text = strings.TrimSuffix(text, "입니다")
	text = strings.TrimSpace(strings.TrimSuffix(text, "표본"))
	if marketQuestionBindingListedAmountScope(text, marketQuestionBindingDomesticScope(facts), facts) != "domestic" {
		return marketQuestionBindingScope{}
	}
	return marketQuestionBindingScope{exchange: "domestic", metric: "DOMESTIC_VOLUME_KRW"}
}

func (s marketQuestionBindingScope) resolveCandleDenominator(prefix, unit string) (string, bool) {
	if !strings.HasPrefix(unit, "KRW") || !marketQuestionBindingCandleDenominatorReference.MatchString(prefix) {
		return "", false
	}
	if !s.candleDenominator {
		return "unverified_aggregate", true
	}
	return s.exchange, true
}

func (s marketQuestionBindingScope) resolve(prefix, unit string) (string, bool) {
	if !strings.HasPrefix(unit, "KRW") || !marketQuestionBindingScopeReference.MatchString(prefix) {
		return "", false
	}
	if s.exchange == "" || s.metric != "DOMESTIC_VOLUME_KRW" {
		return "unverified_aggregate", true
	}
	return s.exchange, true
}
