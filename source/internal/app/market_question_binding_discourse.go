package app

import (
	"regexp"
	"strings"
)

type marketQuestionBindingDiscourseKey struct {
	value string
	unit  string
}

// A discourse antecedent comes only from a prior claim in this answer that
// already matched a typed fact with an explicit owner and metric. It is not
// populated from the server ledger, the question, history, or a future token.
type marketQuestionBindingDiscourse struct {
	verified map[marketQuestionBindingDiscourseKey]map[marketQuestionBoundNumber]bool
}

var marketQuestionBindingBareRepeat = regexp.MustCompile(`(?i)^\s*(?:%p|%포인트|%|퍼센트포인트|퍼센트|조원|억원|만원|원|krw|건|개|회)\s*(?:만으로|만|은|는|이|가|도)(?:\s|$)`)

func newMarketQuestionBindingDiscourse() *marketQuestionBindingDiscourse {
	return &marketQuestionBindingDiscourse{verified: map[marketQuestionBindingDiscourseKey]map[marketQuestionBoundNumber]bool{}}
}

func (d *marketQuestionBindingDiscourse) remember(claims []marketQuestionBoundNumber) {
	for _, claim := range claims {
		key := marketQuestionBindingDiscourseKey{value: claim.value, unit: claim.unit}
		if d.verified[key] == nil {
			d.verified[key] = map[marketQuestionBoundNumber]bool{}
		}
		d.verified[key][claim] = true
	}
}

// resolve returns conflict=true when prior answer claims gave this same value
// and unit more than one typed identity. Refusing the normal query-default path
// in that case prevents an ambiguous repeated scalar from silently becoming
// Bithumb merely because Bithumb is the question's default subject.
func (d *marketQuestionBindingDiscourse) resolve(value, unit string) (claim marketQuestionBoundNumber, found, conflict bool) {
	candidates := d.verified[marketQuestionBindingDiscourseKey{value: value, unit: unit}]
	if len(candidates) == 0 {
		return marketQuestionBoundNumber{}, false, false
	}
	if len(candidates) != 1 {
		return marketQuestionBoundNumber{}, false, true
	}
	for candidate := range candidates {
		return candidate, true, false
	}
	return marketQuestionBoundNumber{}, false, false
}

func marketQuestionBindingDiscourseLocalContext(prefix, suffix string) (string, string) {
	localPrefix := marketQuestionBindingLocalNumericPrefix(prefix)
	if clauses := marketQuestionBindingClause.FindAllStringIndex(localPrefix, -1); len(clauses) > 0 {
		localPrefix = localPrefix[clauses[len(clauses)-1][1]:]
	}
	end := len(suffix)
	if next := marketQuestionNumericToken.FindStringIndex(suffix); next != nil && next[0] < end {
		end = next[0]
	}
	if clause := marketQuestionBindingClause.FindStringIndex(suffix); clause != nil && clause[0] < end {
		end = clause[0]
	}
	return localPrefix, suffix[:end]
}

func marketQuestionBindingDiscourseHasOwner(text string, assetPatterns []marketQuestionBindingAssetPattern) bool {
	if marketQuestionBindingVenue.MatchString(text) || strings.Contains(text, "국내") || strings.Contains(text, "경쟁") || marketQuestionBindingAsset(text, assetPatterns) != "" {
		return true
	}
	lower := strings.ToLower(text)
	for name := range marketQuestionUnsupportedExchangeNames {
		if strings.Contains(lower, name) {
			return true
		}
	}
	for name := range marketQuestionKoreanAssetNames {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}

func marketQuestionBindingDiscourseHasMetric(text, unit string) bool {
	switch {
	case strings.HasPrefix(unit, "KRW"):
		for _, label := range []string{"거래대금", "거래금액", "증가액", "감소액"} {
			if strings.Contains(text, label) {
				return true
			}
		}
	case unit == "PCT" || unit == "PP":
		for _, label := range []string{"점유율", "비중", "포착률", "기여"} {
			if strings.Contains(text, label) {
				return true
			}
		}
	}
	return false
}
