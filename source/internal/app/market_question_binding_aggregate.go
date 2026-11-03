package app

import (
	"regexp"
	"strconv"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

var marketQuestionBindingDomesticAssetList = regexp.MustCompile(`(?i)^\s*국내\s*(?:공식\s*)?(?:rolling\s*24시간\s*)?거래(?:대금|금액)(?:은|는)\s*(?:[A-Z][A-Z0-9]{1,11}\s+[0-9][0-9,.]*\s*(?:조원|억원|만원|원)\s*,\s*)*([A-Z][A-Z0-9]{1,11})\s*$`)
var marketQuestionBindingSampleTotal = regexp.MustCompile(`(?:(?:동일|같은)\s*(?:공식\s*)?시점(?:의|에서)?\s*)?표본\s*전체(?:\s*(?:공식\s*)?거래(?:대금|금액))?\s*$`)
var marketQuestionBindingAggregateSeparator = regexp.MustCompile(`^[\s·,]*(?:(?:와|과|및|그리고)[\s·,]*)?$`)
var marketQuestionBindingPossessiveVenue = regexp.MustCompile(`(?i)(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*$`)
var marketQuestionBindingDomesticAmountScope = regexp.MustCompile(`국내\s*(?:전체|[0-9]+\s*개\s*거래소|거래소\s*전체|(?:공식\s*)?거래(?:대금|금액))`)
var marketQuestionBindingAmountDenominatorRole = regexp.MustCompile(`분모\s*(?:인|는|가|:)?\s*$`)
var marketQuestionBindingAmountDefinition = regexp.MustCompile(`^(?:조원|억원|만원|원)\s*(?:(?:은|는)\s*|(?:이|가)\s+)(.+?)(?:입니다|이다|이며|이고|임)(?:\s|$|[,;])`)
var marketQuestionBindingUnresolvedVenues = regexp.MustCompile(`(?:나머지|다른|그\s*밖의|이들|해당)\s*거래소(?:들)?`)
var marketQuestionBindingExcludedVenues = regexp.MustCompile(`제외(?:한|하고|된)?|빼고|뺀`)
var marketQuestionBindingAggregateIntentTail = regexp.MustCompile(`(?:합친|합한|합산한|합계|총액|전체\s*거래(?:대금|금액))[^.!?]*$`)
var marketQuestionBindingUnknownListTail = regexp.MustCompile(`^\s*(?:[·,]|와\s|과\s|및\s)`)
var marketQuestionBindingLeadingListJoin = regexp.MustCompile(`(?:[·,]|와|과|및|그리고)\s*$`)
var marketQuestionBindingLeadingClauseEnd = regexp.MustCompile(`(?:아니라|이며|이고|지만|하며|반면|다만)\s*,\s*$`)
var marketQuestionBindingExcludedOperand = regexp.MustCompile(`^\s*(?:의\s*)?(?:거래(?:대금|금액)\s*)?(?:을|를)?\s*(?:빼고|뺀|제외(?:하고|한|된)?)\s*,?\s*$`)
var marketQuestionBindingVenueCount = regexp.MustCompile(`([0-9]+)\s*개\s*(?:의\s*)?(?:국내\s*)?거래소|거래소\s*([0-9]+)\s*개`)
var marketQuestionBindingDomesticCardinalityAmount = regexp.MustCompile(`(?i)^\s*(?:분모\s*(?:는|가|:)?\s*)?(?:(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*거래(?:대금|금액)\s*(?:이|가)?\s*아니라\s*)?([0-9]+)\s*개\s*(?:의\s*)?거래소(?:의)?\s*(?:공식\s*)?전체\s*거래(?:대금|금액)\s*$`)
var marketQuestionBindingListedDenominatorLeading = regexp.MustCompile(`^\s*(?:(?:(?:같은|동일한)\s*)?(?:공식\s*)?표본(?:에\s*포함된)?\s*)?$`)
var marketQuestionBindingLeadingAmountSubject = regexp.MustCompile(`(?i)(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*(?:공식\s*)?거래(?:대금|금액)(?:은|는|이|가)\s*$`)

// Parse only this financial noun phrase, consuming every token. Source
// attribution and an appositional name for the same domestic total do not
// change the already verified member set or amount metric. Unknown modifiers,
// negation, other currencies/periods/metrics are not discarded or accepted.
func marketQuestionBindingAggregateTailAllowed(text string) bool {
	allowed, _ := marketQuestionBindingAggregateTailProjection(text)
	return allowed
}

func marketQuestionBindingAggregateTailProjection(text string) (bool, bool) {
	tail := strings.TrimPrefix(strings.ToLower(strings.Join(strings.Fields(text), "")), "의")
	consume := func(tokens ...string) bool {
		for _, token := range tokens {
			if strings.HasPrefix(tail, token) {
				tail = strings.TrimPrefix(tail, token)
				return true
			}
		}
		return false
	}
	// A complete KRW-market scope predicate can precede the same set's
	// domestic turnover in this clause. Membership is still checked by the
	// caller; do not discard unknown qualifiers, exclusions or another market.
	if consume("원화마켓이며,", "원화마켓이고,", "krw마켓이며,", "krw마켓이고,") {
		if !consume("국내전체") {
			return false, false
		}
		if !consume("거래대금", "거래금액") {
			return false, false
		}
		consume("은", "는", "이", "가")
		return tail == "", false
	}
	hasTicker, hasOfficial, hasRollingWindow, hasKRW := false, false, false, false
	for {
		if consume("공식") {
			hasOfficial = true
			continue
		}
		if consume("rolling24시간") {
			hasRollingWindow = true
			continue
		}
		if consume("krw", "원화") {
			hasKRW = true
			continue
		}
		if consume("국내", "전체") {
			continue
		}
		if consume("티커") {
			hasTicker = true
			if consume("에서", "에") && !consume("잡힌", "확인된", "집계된", "기록된", "표시된", "제공된") {
				return false, false
			}
			continue
		}
		break
	}
	hasAmount := consume("거래대금", "거래금액")
	if !hasAmount && !hasTicker {
		return false, false
	}
	consume("을", "를")
	hasSum := consume("합친", "합한", "합산한", "합계", "총액")
	projected := !hasAmount && hasTicker && hasOfficial && hasRollingWindow && hasKRW && hasSum
	// In this amount-only projection, a fully qualified official rolling KRW
	// ticker aggregate identifies its turnover field even if the amount noun
	// is elided. Unqualified tickers or other fields do not select that metric.
	if hasTicker && hasOfficial && hasRollingWindow && hasKRW && hasSum {
		hasAmount = true
	}
	if hasSum {
		// A post-sum metric restates the amount, not a different calculation.
		consume("거래대금", "거래금액", "금액")
		consume("인")
		if consume("국내전체") {
			if consume("거래대금", "거래금액") {
				hasAmount = true
			}
		}
	}
	consume("은", "는", "이", "가")
	consume("입니다", "이다", "이며", "이고", "임")
	return hasAmount && tail == "", projected
}

func marketQuestionBindingDomesticScope(facts []marketQuestionDisplayFact) map[string]bool {
	scope := map[string]bool{}
	for _, fact := range facts {
		if fact.Metric != "DOMESTIC_VOLUME_KRW" || fact.Exchange != "domestic" || fact.Asset != "" {
			continue
		}
		for _, venue := range marketQuestionBindingVenue.FindAllString(fact.Text, -1) {
			scope[marketQuestionExchangeNames[strings.ToLower(venue)]] = true
		}
	}
	return scope
}

// Venue cardinality is a property of this verified set. A COUNT such as the
// number of hourly candles elsewhere in a legacy paragraph cannot authorize it.
func marketQuestionBindingVenueCountsAllowed(text string, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery) bool {
	if query.Intent != marketanalysis.MarketQuestionCurrentOfficialShare || query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot || query.Asset != "" {
		return true
	}
	scope := marketQuestionBindingDomesticScope(facts)
	for _, sentence := range marketQuestionBindingSentence.Split(text, -1) {
		for _, match := range marketQuestionBindingVenueCount.FindAllStringSubmatch(sentence, -1) {
			count, err := strconv.Atoi(match[1] + match[2])
			if err == nil && len(scope) >= 2 && count == len(scope) {
				continue
			}
			// Expected coverage is not observed coverage. Permit only an
			// explicitly unconfirmed count backed by the server's missing scope.
			allowed := false
			if marketQuestionUncertaintyClaim(strings.ToLower(sentence)) {
				for _, fact := range facts {
					if fact.Metric == "INCOMPLETE_EXPECTED_EXCHANGE_COUNT" && fact.EvidenceLayer == "CURRENT_OFFICIAL_TICKER" && fact.Quality == "PARTIAL" {
						for _, expected := range marketQuestionBindingVenueCount.FindAllStringSubmatch(fact.Text, -1) {
							n, e := strconv.Atoi(expected[1] + expected[2])
							allowed = allowed || e == nil && err == nil && n == count
						}
					}
				}
			}
			if allowed {
				continue
			}
			return false
		}
	}
	return true
}

// A direct aggregate claim cannot become a single venue's amount merely by
// appending an explicit possessive predicate to the same numeric value.
func marketQuestionBindingAggregateConflict(aggregate, postfix string) bool {
	return aggregate == "unverified_aggregate" || aggregate != "" && postfix != "" && aggregate != postfix
}

func marketQuestionBindingExplicitDenominatorWithFacts(sentence string, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery) string {
	if query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval && query.Asset == "" {
		for _, fact := range facts {
			if fact.Metric != "DOMESTIC_VOLUME_KRW" || fact.EvidenceLayer != "OFFICIAL_REST_CANDLE_MATCHED_MARKETS" || fact.Quality != "PARTIAL_MARKET_SCOPE" {
				continue
			}
			for _, candidate := range marketQuestionBindingExplicitDenominatorCandidates(sentence) {
				venues := map[string]bool{}
				for _, venue := range marketQuestionBindingVenue.FindAllString(candidate, -1) {
					venues[venue] = true
				}
				if len(venues) == 3 && venues["업비트"] && venues["빗썸"] && venues["코인원"] && strings.Contains(candidate, "종목") {
					return "domestic"
				}
			}
		}
	}
	if query.Asset != "" {
		resolved := ""
		for _, clause := range marketQuestionBindingClause.Split(sentence, -1) {
			for _, candidate := range marketQuestionBindingExplicitDenominatorCandidates(clause) {
				scope := marketQuestionTypedAssetDenominator(candidate, facts, query)
				if resolved != "" && resolved != scope {
					return "unverified_asset_scope"
				}
				resolved = scope
			}
		}
		if resolved != "" {
			return resolved
		}
	}
	for _, at := range marketQuestionNumericToken.FindAllStringIndex(sentence, -1) {
		unit := marketQuestionNumericUnit(sentence[at[1]:])
		if aggregate := marketQuestionBindingAmountReference(sentence[:at[0]], sentence[at[1]:], unit, facts, query); aggregate != "" {
			return aggregate
		}
	}
	if query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare && query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.Asset == "" {
		scope := marketQuestionBindingDomesticScope(facts)
		if len(scope) >= 2 {
			for _, candidate := range marketQuestionBindingExplicitDenominatorCandidates(sentence) {
				aggregate := marketQuestionBindingListedAmountScope(candidate, scope, facts)
				if aggregate == "" {
					continue
				}
				if aggregate == "domestic" {
					venues := marketQuestionBindingVenue.FindAllStringIndex(candidate, -1)
					if len(venues) == 0 || !marketQuestionBindingListedDenominatorLeading.MatchString(candidate[:venues[0][0]]) {
						return "unverified_aggregate"
					}
				}
				return aggregate
			}
		}
	}
	return marketQuestionBindingExplicitDenominator(sentence)
}

// Amount ownership and the ratio's numerator are separate. Inspect the local
// numeric clause and any direct following definition, never the numeric value
// itself. This is limited to the verified current official whole-market scope;
// asset capture/internal denominators retain their existing independent path.
func marketQuestionBindingAmountReference(prefix, suffix, unit string, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery) string {
	// A shared domestic-money predicate can own a comma-separated asset list.
	// Require its entire prefix and a typed amount for the final named asset.
	if strings.HasPrefix(unit, "KRW") {
		if match := marketQuestionBindingDomesticAssetList.FindStringSubmatch(prefix); match != nil {
			assetQuery := query
			assetQuery.Asset = strings.ToUpper(match[1])
			if marketQuestionHasDomesticAssetAmount(facts, assetQuery) {
				return "domestic"
			}
		}
	}
	if query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval && query.Asset == "" && strings.HasPrefix(unit, "KRW") {
		// A candle basket's denominator is a separately computed amount. The
		// absence of a venue noun must not assign it to the requested numerator.
		at := strings.LastIndex(prefix, "분모")
		if at >= 0 && strings.Contains(prefix[:at], "동일 종목") && !marketQuestionBindingVenue.MatchString(prefix[at:]) {
			for _, fact := range facts {
				if fact.Metric == "DOMESTIC_VOLUME_KRW" && fact.EvidenceLayer == "OFFICIAL_REST_CANDLE_MATCHED_MARKETS" && fact.Quality == "PARTIAL_MARKET_SCOPE" {
					return "domestic"
				}
			}
		}
	}
	if strings.HasPrefix(unit, "KRW") && marketQuestionHasDomesticAssetAmount(facts, query) {
		local := strings.TrimSpace(marketQuestionBindingLocalNumericPrefix(prefix))
		scope := marketQuestionTypedAssetDenominator(local, facts, query)
		if scope == "domestic_asset" {
			return "domestic"
		}
		if strings.HasPrefix(local, "국내") && scope == "unverified_asset_scope" {
			return "unverified_aggregate"
		}
	}
	if query.Intent != marketanalysis.MarketQuestionCurrentOfficialShare || query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot || query.Asset != "" {
		return ""
	}
	// Resolve each numeric clause before examining unresolved owners. A generic
	// definition in a completed clause cannot own a later explicit sample total.
	// Preserve a full list until the existing set validator has examined it.
	local := marketQuestionBindingLocalNumericPrefix(prefix)
	var scope map[string]bool
	if strings.HasPrefix(unit, "KRW") {
		scope = marketQuestionBindingDomesticScope(facts)
		if len(scope) >= 2 {
			if aggregate := marketQuestionBindingListedAmountScope(local, scope, facts); aggregate != "" {
				return aggregate
			}
		}
	}
	// Only a completed connective ends an unresolved owner's scope. A comma
	// may simply separate "the other exchanges, amount" and must not erase it.
	localOwnerBoundary := 0
	for _, clause := range marketQuestionBindingClause.FindAllStringIndex(prefix, -1) {
		if prefix[clause[0]:clause[1]] != "," && prefix[clause[0]:clause[1]] != ";" {
			localOwnerBoundary = clause[1]
		}
	}
	prefix = prefix[localOwnerBoundary:]
	if clauses := marketQuestionBindingClause.FindAllStringIndex(local, -1); len(clauses) > 0 {
		local = local[clauses[len(clauses)-1][1]:]
	}
	if exclusions := marketQuestionBindingExcludedVenues.FindAllStringIndex(prefix, -1); len(exclusions) > 0 {
		lastExclusion := exclusions[len(exclusions)-1][0]
		if venues := marketQuestionBindingVenue.FindAllStringIndex(prefix, -1); len(venues) == 0 || lastExclusion > venues[len(venues)-1][0] {
			// No subset total has been computed for this explicit exclusion.
			return "unverified_aggregate"
		}
	}
	if groups := marketQuestionBindingUnresolvedVenues.FindAllStringIndex(prefix, -1); len(groups) > 0 {
		lastGroup := groups[len(groups)-1][0]
		if venues := marketQuestionBindingVenue.FindAllStringIndex(prefix, -1); len(venues) == 0 || lastGroup > venues[len(venues)-1][0] {
			// A group is not the preceding venue or the query's default venue.
			return "unverified_aggregate"
		}
	}
	if !strings.HasPrefix(unit, "KRW") {
		return ""
	}
	if len(scope) < 2 {
		return ""
	}
	if definition := marketQuestionBindingAmountDefinition.FindStringSubmatch(suffix); len(definition) == 2 {
		// A following independent numeric assertion is not the definition of
		// this value. Do not reach across it into another claim's subject.
		if marketQuestionBindingLocalNumericPrefix(definition[1]) != definition[1] {
			return ""
		}
		aggregate := marketQuestionBindingListedAmountScope(definition[1], scope, facts)
		if aggregate == "" && marketQuestionBindingDomesticAmountOwner(definition[1]) {
			aggregate = "domestic"
		}
		if aggregate != "" {
			if venues := marketQuestionBindingVenue.FindAllStringIndex(local, -1); len(venues) > 0 && marketQuestionBindingAggregateTailAllowed(local[venues[len(venues)-1][1]:]) {
				return "unverified_aggregate"
			}
			return aggregate
		}
	}
	if marketQuestionBindingDomesticAmountOwner(local) || marketQuestionBindingVerifiedDomesticCardinalityAmountOwner(local, scope) || marketQuestionBindingAmountDenominatorRole.MatchString(local) {
		return "domestic"
	}
	if at := marketQuestionBindingSampleTotal.FindStringIndex(local); at != nil && !marketQuestionBindingPossessiveVenue.MatchString(local[:at[0]]) {
		return "domestic"
	}
	return ""
}

func marketQuestionBindingVerifiedDomesticCardinalityAmountOwner(text string, scope map[string]bool) bool {
	match := marketQuestionBindingDomesticCardinalityAmount.FindStringSubmatch(text)
	if len(match) != 2 {
		return false
	}
	count, err := strconv.Atoi(match[1])
	return err == nil && count == len(scope)
}

func marketQuestionBindingLocalNumericPrefix(prefix string) string {
	start := 0
	for _, at := range marketQuestionNumericToken.FindAllStringIndex(prefix, -1) {
		unit := marketQuestionNumericUnit(prefix[at[1]:])
		// A scope count (such as the number of domestic venues) belongs to
		// the amount's noun phrase; it must not cut that phrase in half.
		if unit == "PCT" || unit == "PP" || strings.HasPrefix(unit, "KRW") {
			start = at[1]
		}
	}
	return prefix[start:]
}

func marketQuestionBindingDomesticAmountOwner(text string) bool {
	locations := marketQuestionBindingDomesticAmountScope.FindAllStringIndex(text, -1)
	if len(locations) == 0 {
		return false
	}
	lastDomestic := locations[len(locations)-1]
	tail := strings.TrimSpace(text[lastDomestic[1]:])
	tail = strings.TrimPrefix(tail, "거래대금")
	tail = strings.TrimPrefix(tail, "거래금액")
	if marketQuestionBindingComparativeOperand.MatchString(tail) {
		return false
	}
	venues := marketQuestionBindingVenue.FindAllStringIndex(text, -1)
	return len(venues) == 0 || lastDomestic[0] > venues[len(venues)-1][0]
}

// A maximal contiguous venue list is a scope, not its last member. Its amount
// must describe the same set as the already computed domestic fact. No sums or
// subsets are synthesized here, whether the list occurs before or after a value.
func marketQuestionBindingListedAmountScope(list string, scope map[string]bool, facts []marketQuestionDisplayFact) string {
	venues := marketQuestionBindingVenue.FindAllStringIndex(list, -1)
	if len(venues) < 2 {
		if len(venues) == 1 {
			tail := list[venues[0][1]:]
			if marketQuestionBindingUnknownListTail.MatchString(tail) && marketQuestionBindingAggregateIntentTail.MatchString(tail) {
				return "unverified_aggregate"
			}
		}
		return ""
	}
	start := len(venues) - 1
	for start > 0 && marketQuestionBindingAggregateSeparator.MatchString(list[venues[start-1][1]:venues[start][0]]) {
		start--
	}
	venues = venues[start:]
	tail := list[venues[len(venues)-1][1]:]
	allowed, projected := marketQuestionBindingAggregateTailProjection(tail)
	if projected && !marketQuestionBindingCompleteTickerAmounts(facts, scope) {
		return "unverified_aggregate"
	}
	if !allowed {
		if len(venues) >= 2 && marketQuestionBindingAggregateIntentTail.MatchString(tail) {
			// A declared plural total that fails scope parsing stays unresolved;
			// an unknown member must not vanish into the last recognized venue.
			return "unverified_aggregate"
		}
		return ""
	}
	if len(venues) < 2 {
		return ""
	}
	// A recognized suffix of a list is not the whole list. Preserve an
	// unparsed leading member instead of silently dropping it. A comma after
	// an explicit clause connective is a clause boundary, not a list join.
	leading := list[:venues[0][0]]
	// An aggregate complement cannot overwrite its explicit singular subject:
	// "A's turnover is [the whole set's sum]" still assigns that sum to A.
	if marketQuestionBindingLeadingAmountSubject.MatchString(leading) {
		return "unverified_aggregate"
	}
	if marketQuestionBindingLeadingListJoin.MatchString(leading) && !marketQuestionBindingLeadingClauseEnd.MatchString(leading) {
		return "unverified_aggregate"
	}
	// An immediately attached exclusion of a venue (or its turnover) still
	// qualifies the following list even when that list repeats the venue.
	// Excluding an explanation is not excluding a member; nor does an earlier
	// independent clause propagate its exclusion into this amount's scope.
	if operands := marketQuestionBindingVenue.FindAllStringIndex(leading, -1); len(operands) > 0 && marketQuestionBindingExcludedOperand.MatchString(leading[operands[len(operands)-1][1]:]) {
		return "unverified_aggregate"
	}
	if len(venues) != len(scope) {
		return "unverified_aggregate"
	}
	seen := map[string]bool{}
	for i, at := range venues {
		venue := marketQuestionExchangeNames[strings.ToLower(list[at[0]:at[1]])]
		if !scope[venue] || seen[venue] || i > 0 && !marketQuestionBindingAggregateSeparator.MatchString(list[venues[i-1][1]:at[0]]) {
			return "unverified_aggregate"
		}
		seen[venue] = true
	}
	return "domestic"
}

// Ellipsis selects a field from verified data, not from wording alone. All
// five turnover projections must share the domestic total's source epoch.
func marketQuestionBindingCompleteTickerAmounts(facts []marketQuestionDisplayFact, scope map[string]bool) bool {
	if len(scope) != 5 {
		return false
	}
	period := ""
	for _, fact := range facts {
		if fact.Metric == "DOMESTIC_VOLUME_KRW" && fact.Exchange == "domestic" && fact.Asset == "" && fact.EvidenceLayer == "CURRENT_OFFICIAL_TICKER" && fact.Quality == "LIVE" {
			if period != "" && period != fact.Period {
				return false
			}
			period = fact.Period
		}
	}
	if period == "" {
		return false
	}
	seen := map[string]bool{}
	for _, fact := range facts {
		if fact.Metric != "EXCHANGE_VOLUME_KRW" || fact.Asset != "" || !scope[fact.Exchange] {
			continue
		}
		if fact.Period != period || fact.EvidenceLayer != "CURRENT_OFFICIAL_TICKER" || fact.Quality != "LIVE" || seen[fact.Exchange] {
			return false
		}
		seen[fact.Exchange] = true
	}
	return len(seen) == len(scope)
}
