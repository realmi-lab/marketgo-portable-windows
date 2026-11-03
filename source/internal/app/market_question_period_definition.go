package app

import "strings"

// A shared definition predicate can own several coordinated noun phrases.
// Commas in that list do not create independent observed-value assertions.
// This classification affects period role only, never number authorization,
// exchange/denominator binding, or query validation.
func marketQuestionBindingCoordinatedDefinition(sentence string) bool {
	body := strings.TrimSpace(sentence)
	matched := false
	for _, predicate := range []string{"과는 정의가 다릅니다", "와는 정의가 다릅니다", "과는 정의가 다르다", "와는 정의가 다르다"} {
		if strings.HasSuffix(body, predicate) {
			body = strings.TrimSpace(strings.TrimSuffix(body, predicate))
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	// No prices, shares, amounts or trade counts can inherit this role.
	for _, at := range marketQuestionNumericToken.FindAllStringIndex(body, -1) {
		unit := marketQuestionNumericUnit(body[at[1]:])
		barCount := false
		if unit == "COUNT" && strings.HasPrefix(strings.TrimSpace(body[at[1]:]), "개") {
			for _, bar := range []string{"시간봉", "분봉", "일봉"} {
				if strings.HasSuffix(strings.TrimSpace(body[:at[0]]), bar) {
					barCount = true
				}
			}
		}
		if unit == "PCT" || unit == "PP" || (unit == "COUNT" && !barCount) || strings.HasPrefix(unit, "KRW") {
			return false
		}
	}
	clauses := marketQuestionBindingClause.Split(body, -1)
	if len(clauses) < 2 {
		return false
	}
	for i, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if i < len(clauses)-1 {
			// Explicit nominal coordination, not an affirmative predicate.
			if !strings.HasSuffix(clause, "이나") && !strings.HasSuffix(clause, "또는") {
				return false
			}
			clause = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(clause, "이나"), "또는"))
		}
		nominal := false
		for _, suffix := range []string{"거래대금", "거래금액", "합계", "값", "집계", "구간"} {
			if strings.HasSuffix(clause, suffix) {
				nominal = true
				break
			}
		}
		if !nominal {
			return false
		}
	}
	return true
}
