package app

import (
	"regexp"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

type marketQuestionBoundNumber struct {
	value, unit, exchange, asset, metric, role string
}
type marketQuestionBindingAssetPattern struct {
	asset   string
	pattern *regexp.Regexp
}
type marketQuestionBindingHeading struct {
	level int
	text  string
}

var marketQuestionBindingSentence = regexp.MustCompile(`[.!?](?:\s|$)|\n`)
var marketQuestionBindingVenue = regexp.MustCompile(`(?i)업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax`)
var marketQuestionBindingDomestic = regexp.MustCompile(`국내\s*(?:전체\s*)?(?:공식\s*)?(?:관측\s*체결|거래대금|거래금액)`)
var marketQuestionBindingDenominator = regexp.MustCompile(`분모\s*(?:는|가|:)\s*([^.!?\n]{1,70})|((?:국내|업비트|빗썸|코인원|코빗|고팍스)[^,;.!?\n]{0,45}거래(?:대금|금액)(?:\s*[0-9.,]+(?:조원|억원|만원|원))?)\s*(?:대비|를\s*분모|을\s*분모)`)
var marketQuestionBindingDuration = regexp.MustCompile(`([0-9]+)\s*(시간|분|초|일)`)
var marketQuestionBindingClause = regexp.MustCompile(`[,;]|이며|이고|지만|하며`)
var marketQuestionBindingDirectNegation = regexp.MustCompile(`^\s*(?:은|는|이|가)?\s*(?:아니|아닙|아닌|아닐)`)
var marketQuestionBindingHeadingPattern = regexp.MustCompile(`^\s*(#{1,6})\s+(.+)$`)
var marketQuestionBindingTableSeparator = regexp.MustCompile(`^[\s|:-]+$`)
var marketQuestionBindingComparativeOperand = regexp.MustCompile(`^\s*(?:보다|(?:와|과)\s*비교|에\s*비해|에\s*비하여)`)
var marketQuestionBindingPostValueSubject = regexp.MustCompile(`^(?:%p|%|퍼센트포인트|퍼센트|조원|억원|만원|원|건|회)\s*(?:은|는|이|가|인)?\s*[(（]?\s*$`)
var marketQuestionBindingPostfixNegation = regexp.MustCompile(`^\s*(?:의\s*)?(?:(?:현재|공식|rolling)\s*)*(?:점유율|비중|거래대금|거래금액)\s*(?:은|는|이|가)?\s*(?:아니|아닙|아닌|아닐)`)
var marketQuestionBindingPostfixPredicate = regexp.MustCompile(`^\s*(?:의\s*)?(?:(?:현재|공식|rolling)\s*)*(?:점유율|비중|거래대금|거래금액)(?:\s*(?:입니다|이다|이며|이고|였|임)|\s*$)|^\s*[)）]`)
var marketQuestionBindingPostfixRelativeOwner = regexp.MustCompile(`^(?:%|퍼센트)\s*(?:를|을)\s*차지한\s*$`)
var marketQuestionBindingUnavailableRole = regexp.MustCompile(`^\s*(?:의\s*)?(?:(?:시점|기간|비교)\s*)?(?:자료|근거|값|수치|데이터)(?:는|가|이)?\s*(?:없|부족|제공되지|확인되지)`)
var marketQuestionBindingEndpointPairStart = regexp.MustCompile(`^(?:%p|%|퍼센트포인트|퍼센트|조원|억원|만원|원|건|회)\s*(?:→|에서)\s*(?:(?:시작|직전|이전|종료|현재|끝)\s*)?[+-]?[0-9]`)
var marketQuestionBindingSignedDeltaPredicate = regexp.MustCompile(`^(?:조원|억원|만원|원)\s*(?:(?:증가|감소|변했|늘었|줄었)|[,，][^!?\n]*(?:변했|증가했|감소했))`)

// This is an additional fail-closed check for explicit, display-form numeric
// claims. It is not a general Korean semantic parser. It binds typed projections
// only; LEGACY_DISPLAY values keep their existing guard until projected. It does
// not turn a different target's valid number into a globally reusable fact.
// Free-form qualitative inference, pronouns and arbitrary implicit relations
// still require answer evaluation; this check never certifies those as facts.
func marketQuestionAnswerFactBindingsAllowed(answer marketanalysis.MarketQuestionAnswer, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery) bool {
	text := strings.NewReplacer("**", "", "__", "", "`", "").Replace(marketQuestionAnswerText(answer))
	boundComparisons := marketQuestionBoundComparisonLines(text, facts)
	if !marketQuestionBindingVenueCountsAllowed(text, facts, query) {
		return false
	}
	index := []marketQuestionBoundNumber{}
	assets := map[string]bool{}
	for _, fact := range facts {
		if fact.Metric == "LEGACY_DISPLAY" || fact.Exchange == "" {
			continue
		}
		if fact.Asset != "" {
			assets[strings.ToUpper(fact.Asset)] = true
		}
		body := strings.TrimPrefix(fact.Text, fact.Period)
		pairedOrdinal := 0
		for _, at := range marketQuestionNumericToken.FindAllStringIndex(body, -1) {
			unit := marketQuestionNumericUnit(body[at[1]:])
			metric, exchange := fact.Metric, fact.Exchange
			if fact.Comparison != nil {
				exchange = "__atomic_comparison_only__"
			}
			if strings.HasPrefix(unit, "KRW") && (metric == "ASSET_ATTRIBUTION_PP" || metric == "EXCHANGE_ATTRIBUTION_PP") {
				metric = "VOLUME_DELTA_KRW"
				if fact.Metric == "ASSET_ATTRIBUTION_PP" && strings.LastIndex(body[:at[0]], "경쟁") > strings.LastIndex(body[:at[0]], "빗썸") {
					exchange = "competitor"
				}
			}
			if !marketQuestionBindingMetricUnit(metric, unit) {
				continue
			}
			value, ok := normalizeMarketQuestionNumericToken(body[at[0]:at[1]])
			if ok {
				role := ""
				// Current snapshots carry no paired-endpoint role in their fact
				// text. Their validated query still fixes these values to current,
				// rather than authorizing the same scalar as an earlier observation.
				if query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot {
					role = "current"
				}
				if strings.Contains(body, "→") && !strings.Contains(metric, "ATTRIBUTION") {
					if pairedOrdinal == 0 {
						role = "previous"
					} else {
						role = "current"
					}
					pairedOrdinal++
				}
				index = append(index, marketQuestionBoundNumber{value, unit, exchange, strings.ToUpper(fact.Asset), metric, role})
			}
		}
	}
	if len(index) == 0 {
		return true
	}
	assetPatterns := marketQuestionBindingAssetPatterns(assets)
	var tableHeaders []string
	var headings []marketQuestionBindingHeading
	var previousPercentage []marketQuestionBoundNumber
	discourse := newMarketQuestionBindingDiscourse()
	var previousScope marketQuestionBindingScope
	for _, sentence := range marketQuestionBindingSentence.Split(text, -1) {
		trimmed := strings.TrimSpace(sentence)
		if trimmed == "" {
			tableHeaders = nil
			previousScope = marketQuestionBindingScope{}
			continue
		}
		hasValue, hasPercentage := marketQuestionBindingNumericKinds(sentence)
		if heading := marketQuestionBindingHeadingPattern.FindStringSubmatch(sentence); len(heading) == 3 {
			previousScope = marketQuestionBindingScope{}
			level := len(heading[1])
			for len(headings) > 0 && headings[len(headings)-1].level >= level {
				headings = headings[:len(headings)-1]
			}
			headings = append(headings, marketQuestionBindingHeading{level: level, text: heading[2]})
			tableHeaders, previousPercentage = nil, nil
			// A title qualifies subsequent claims; a title alone is not a market
			// value. Parent sections remain in force under nested headings.
			if !hasValue {
				continue
			}
		}
		isTable := strings.HasPrefix(trimmed, "|")
		if isTable {
			previousScope = marketQuestionBindingScope{}
			if marketQuestionBindingTableSeparator.MatchString(sentence) {
				continue
			}
			if len(tableHeaders) == 0 && !hasValue {
				tableHeaders = strings.Split(sentence, "|")
				previousPercentage = nil
				continue
			}
		} else {
			tableHeaders = nil
		}
		if hasValue {
			for _, heading := range headings {
				if !marketQuestionBindingCurrentPeriodAllowed(heading.text+" 거래대금", query) {
					return false
				}
				if query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && marketQuestionBindingRole(heading.text, "") == "previous" {
					return false
				}
			}
		}
		if !marketQuestionBindingCurrentPeriodAllowed(sentence, query) {
			return false
		}
		if boundComparisons[trimmed] {
			// Parent period/role checks above still apply to an atomic quote.
			// Do not let a correctly rendered current comparison become yesterday's.
			previousPercentage = nil
			previousScope = marketQuestionBindingScope{}
			continue
		}
		// A directly adjacent explanatory sentence may state the denominator
		// after the percentage. Keep its matched fact identity, not just a number.
		if !hasPercentage && len(previousPercentage) > 0 && strings.Contains(sentence, "분모") {
			if denominator := marketQuestionBindingExplicitDenominatorWithFacts(sentence, facts, query); denominator != "" &&
				!marketQuestionBindingAdjacentDenominatorAllowed(sentence, denominator, previousPercentage) {
				return false
			}
		}
		var nextPercentage []marketQuestionBoundNumber
		locations := marketQuestionNumericToken.FindAllStringIndex(sentence, -1)
		groupBindings, groupsAllowed := marketQuestionBindingGroups(sentence)
		if !groupsAllowed {
			return false
		}
		var pendingDiscourse []marketQuestionBoundNumber
		coordinatedOwner, coordinatedEnd := "", -1
		for numberIndex, at := range locations {
			unit := marketQuestionNumericUnit(sentence[at[1]:])
			if unit != "PCT" && unit != "PP" && unit != "COUNT" && !strings.HasPrefix(unit, "KRW") {
				continue
			}
			value, _ := normalizeMarketQuestionNumericToken(sentence[at[0]:at[1]])
			prefix := sentence[:at[0]]
			exchange := marketQuestionBindingExchange(prefix, unit)
			grouped := groupBindings[at[0]]
			if grouped != "" {
				exchange = grouped
			}
			aggregate := marketQuestionBindingAmountReference(prefix, sentence[at[1]:], unit, facts, query)
			if reference, isReference := previousScope.resolveCandleDenominator(prefix, unit); isReference {
				if aggregate != "" && aggregate != reference {
					return false
				}
				aggregate = reference
			}
			localScopePrefix, _ := marketQuestionBindingDiscourseLocalContext(prefix, sentence[at[1]:])
			if reference, isReference := previousScope.resolve(localScopePrefix, unit); isReference {
				if aggregate != "" && aggregate != reference {
					return false
				}
				aggregate = reference
			}
			postfix := marketQuestionBindingPostfixExchange(sentence[at[1]:])
			// In a coordinated current claim, the prior percentage's denominator
			// words do not own an immediately following owner-elliptical amount.
			// Carry only a previously matched, explicit, unique venue subject;
			// a new owner/scope/period or intervening claim cannot use this path.
			if query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare && query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.Asset == "" &&
				!isTable && strings.HasPrefix(unit, "KRW") && coordinatedOwner != "" && numberIndex > 0 && locations[numberIndex-1][1] == coordinatedEnd &&
				grouped == "" && aggregate == "" && postfix == "" && marketQuestionBindingCoordinatedAmount.MatchString(sentence[coordinatedEnd:at[0]]) {
				exchange = coordinatedOwner
			}
			coordinatedOwner, coordinatedEnd = "", -1
			if marketQuestionBindingAggregateConflict(aggregate, postfix) || marketQuestionBindingGroupConflict(grouped, aggregate, postfix) {
				return false
			}
			if aggregate != "" {
				exchange = aggregate
			}
			// An explicit "value is venue's metric" predicate binds this value
			// more tightly than a different venue mentioned in an earlier clause.
			// The helper excludes a following comparison operand or denominator.
			if postfix != "" {
				exchange = postfix
			}
			asset := marketQuestionBindingAsset(prefix, assetPatterns)
			explicitAsset := asset
			metric := marketQuestionBindingMetric(prefix, unit)
			// A signed monetary amount denotes a change, even when the shared
			// change predicate follows another coordinated metric. It must match
			// a typed delta for this owner, never an endpoint with the same value.
			if strings.HasPrefix(unit, "KRW") && strings.ContainsAny(sentence[at[0]:at[0]+1], "+-") && marketQuestionBindingSignedDeltaPredicate.MatchString(sentence[at[1]:]) {
				metric = "VOLUME_DELTA_KRW"
			}
			if asset == "" && strings.HasPrefix(metric, "ASSET_") {
				asset = strings.ToUpper(query.Asset)
			}
			role := marketQuestionBindingRole(prefix, sentence[at[1]:])
			tableOwner, tableMetric := false, false
			if column := strings.Count(prefix, "|"); len(tableHeaders) > column {
				header := strings.TrimSpace(tableHeaders[column])
				if columnAsset := marketQuestionBindingAsset(header, assetPatterns); columnAsset != "" {
					if explicitAsset != "" && explicitAsset != columnAsset {
						return false
					}
					asset, tableOwner = columnAsset, true
				}
				cells := strings.Split(sentence, "|")
				columnExchange := marketQuestionBindingTableExchange(tableHeaders, cells, column, unit)
				// An attribution table has two venue axes: the row identifies
				// the contributor; the column may name the share being explained.
				// Bind the typed contribution to its row, never to the target.
				if unit == "PP" && strings.Contains(header, "기여") {
					if contributor, hasAxis := marketQuestionBindingTableContributor(tableHeaders, cells); hasAxis {
						if contributor == "" || (query.Exchange != "" && query.Exchange != "bithumb") || !marketQuestionBindingContributionHeader.MatchString(header) {
							return false
						}
						columnExchange, metric = contributor, "EXCHANGE_ATTRIBUTION_PP"
					}
				}
				if columnExchange != "" {
					if marketQuestionBindingGroupConflict(grouped, columnExchange) {
						return false
					}
					exchange = columnExchange
					tableOwner = true
				}
				if !marketQuestionBindingCurrentPeriodAllowed(header+" 거래대금", query) {
					return false
				}
				if columnRole := marketQuestionBindingRole(header, ""); columnRole != "" {
					role = columnRole
				}
				if strings.HasPrefix(unit, "KRW") && (strings.Contains(header, "변화") || strings.Contains(header, "증감")) {
					metric = "VOLUME_DELTA_KRW"
				}
				tableMetric = marketQuestionBindingDiscourseHasMetric(header, unit)
			}
			// Relative rates are a distinct legacy projection, not a share level.
			if metric == "LEGACY_RELATIVE" {
				nextPercentage = nil
				continue
			}
			denominator := ""
			scopeQuery := query
			if asset != "" {
				scopeQuery.Asset = asset
			}
			if unit == "PCT" || unit == "PP" {
				if isTable && query.Asset != "" {
					denominator = marketQuestionAssetTableDenominator(tableHeaders, strings.Split(sentence, "|"), facts, scopeQuery)
				}
				next := len(sentence)
				for _, later := range locations[numberIndex+1:] {
					laterUnit := marketQuestionNumericUnit(sentence[later[1]:])
					if laterUnit == "PCT" || laterUnit == "PP" {
						next = later[0]
						break
					}
				}
				following := sentence[at[1]:next]
				if isTable && query.Asset != "" {
					following, _, _ = strings.Cut(following, "|")
				}
				if explicit := marketQuestionBindingExplicitDenominatorWithFacts(following, facts, scopeQuery); explicit != "" {
					if denominator != "" && denominator != explicit {
						return false
					}
					denominator = explicit
				}
				if query.Asset != "" {
					precedingPrefix := prefix
					if isTable {
						precedingPrefix = prefix[strings.LastIndex(prefix, "|")+1:]
					}
					preceding := marketQuestionAssetPrecedingDenominator(precedingPrefix, facts, scopeQuery)
					if preceding != "" {
						if denominator != "" && denominator != preceding {
							return false
						}
						denominator = preceding
					}
				} else if denominator == "" {
					denominator = marketQuestionBindingPrecedingDenominator(prefix)
				}
			}
			localPrefix, localSuffix := marketQuestionBindingDiscourseLocalContext(prefix, sentence[at[1]:])
			explicitOwner := grouped != "" || aggregate != "" || postfix != "" || tableOwner || marketQuestionBindingDiscourseHasOwner(localPrefix+" "+localSuffix, assetPatterns)
			explicitMetric := tableMetric || marketQuestionBindingDiscourseHasMetric(localPrefix+" "+localSuffix, unit)
			resolvedFromDiscourse := false
			if !isTable && grouped == "" && aggregate == "" && postfix == "" && denominator == "" && role == "" && !explicitOwner && !explicitMetric && marketQuestionBindingBareRepeat.MatchString(localSuffix) {
				antecedent, found, conflict := discourse.resolve(value, unit)
				if conflict {
					return false
				}
				if found {
					exchange, asset, metric, role = antecedent.exchange, antecedent.asset, antecedent.metric, antecedent.role
					resolvedFromDiscourse = true
				}
			}
			if exchange == "" {
				exchange = query.Exchange
				if exchange == "" {
					exchange = "bithumb"
				}
			}
			covered, matched := false, false
			var matchedFacts []marketQuestionBoundNumber
			for _, fact := range index {
				if fact.value != value || fact.unit != unit {
					continue
				}
				covered = true
				if fact.exchange != exchange || fact.asset != asset {
					continue
				}
				if metric != "" && fact.metric != metric {
					if metric != "VOLUME_LEVEL_KRW" || (fact.metric != "DOMESTIC_VOLUME_KRW" && fact.metric != "EXCHANGE_VOLUME_KRW") {
						continue
					}
				}
				if role != "" && fact.role != "" && role != fact.role {
					continue
				}
				if denominator != "" && denominator != marketQuestionBindingFactDenominator(fact.metric) {
					continue
				}
				matched = true
				matchedFacts = append(matchedFacts, fact)
			}
			if covered && !matched {
				return false
			}
			if unit == "PCT" && covered && matched && explicitOwner && explicitMetric && grouped == "" && len(matchedFacts) == 1 {
				fact := matchedFacts[0]
				venues := marketQuestionBindingVenue.FindAllStringIndex(prefix, -1)
				if fact.metric == "EXCHANGE_SHARE_PCT" && fact.asset == "" && len(venues) == 1 &&
					marketQuestionBindingCoordinatedSubjectContext.MatchString(prefix[:venues[0][0]]) &&
					marketQuestionBindingCoordinatedShareSubject.MatchString(prefix[venues[0][1]:]) {
					coordinatedOwner, coordinatedEnd = fact.exchange, at[1]
				}
			}
			if covered && matched && explicitOwner && explicitMetric && !resolvedFromDiscourse {
				pendingDiscourse = append(pendingDiscourse, matchedFacts...)
			}
			if unit == "PCT" || unit == "PP" {
				nextPercentage = matchedFacts
			}
		}
		discourse.remember(pendingDiscourse)
		previousScope = marketQuestionBindingDeclaredScope(sentence, facts, query)
		previousPercentage = nextPercentage
	}
	return true
}

func marketQuestionBindingNumericKinds(text string) (market, percentage bool) {
	for _, at := range marketQuestionNumericToken.FindAllStringIndex(text, -1) {
		unit := marketQuestionNumericUnit(text[at[1]:])
		percentage = percentage || unit == "PCT" || unit == "PP"
		market = market || unit == "PCT" || unit == "PP" || unit == "COUNT" || strings.HasPrefix(unit, "KRW")
	}
	return market, percentage
}

func marketQuestionBindingPostfixExchange(suffix string) string {
	locations := marketQuestionBindingVenue.FindAllStringIndex(suffix, -1)
	if len(locations) == 0 {
		return ""
	}
	first := locations[0]
	// "57%를 차지한 거래소" directly attributes this percentage to the
	// following venue, even when that venue is a comparison operand. A value
	// merely used as a threshold ("35%보다 높은", "35%를 기준으로 비교한")
	// is not the following venue's own metric and must keep its prefix subject.
	if marketQuestionBindingPostfixRelativeOwner.MatchString(suffix[:first[0]]) &&
		!marketQuestionBindingDirectNegation.MatchString(suffix[first[1]:]) &&
		!marketQuestionBindingPostfixNegation.MatchString(suffix[first[1]:]) {
		return marketQuestionExchangeNames[strings.ToLower(suffix[first[0]:first[1]])]
	}
	if !marketQuestionBindingPostValueSubject.MatchString(suffix[:first[0]]) {
		return ""
	}
	for _, at := range locations {
		if marketQuestionBindingDirectNegation.MatchString(suffix[at[1]:]) || marketQuestionBindingPostfixNegation.MatchString(suffix[at[1]:]) {
			continue
		}
		if marketQuestionBindingPostfixPredicate.MatchString(suffix[at[1]:]) {
			return marketQuestionExchangeNames[strings.ToLower(suffix[at[0]:at[1]])]
		}
		// Do not search beyond a non-subject venue into another clause. For
		// example, "35% is below Upbit's share" does not belong to Upbit.
		return ""
	}
	return ""
}

func marketQuestionBindingTableExchange(headers, cells []string, column int, unit string) string {
	if exchange := marketQuestionBindingExchange(headers[column], unit); exchange != "" {
		return exchange
	}
	// A row's explicit venue field is authoritative even when placed after
	// numeric fields. Column-oriented tables instead declare the venue above.
	for index, header := range headers {
		if index < len(cells) && strings.Contains(header, "거래소") {
			if exchange := marketQuestionBindingExchange(cells[index], unit); exchange != "" {
				return exchange
			}
		}
	}
	return ""
}

// Consume the whole target label. An unrecognized written owner is not an
// omitted owner: only these target-free forms may inherit the Bithumb query.
// Unknown, foreign, mixed and negated owners must not borrow its contribution.
var marketQuestionBindingContributionHeader = regexp.MustCompile(`(?i)^(?:(?:빗썸|bithumb)(?:의)?\s*)?(?:점유율(?:에\s*대한)?\s*(?:산술\s*)?기여(?:도)?|산술\s*기여(?:도)?)$`)

var marketQuestionBindingCoordinatedAmount = regexp.MustCompile(`^(?:%|퍼센트)\s*(?:,|이며\s*,?|이고\s*,?)\s*거래(?:대금|금액)(?:은|는|이|가)\s*$`)

// The entire antecedent must be a supported source context, one venue and
// the share predicate below. Unknown text before the venue is not discarded,
// regardless of how it joins or qualifies a different written subject.
var marketQuestionBindingCoordinatedSubjectContext = regexp.MustCompile(`^\s*(?:(?:현재|지금|확인된|공식|같은|동일한)\s*)*(?:(?:자료|표본)(?:에서|의)\s*)?$`)
var marketQuestionBindingCoordinatedShareSubject = regexp.MustCompile(`^(?:의\s*)?(?:(?:국내|전체|공식)\s*)*(?:거래(?:대금|금액)\s*)?점유율(?:은|는|이|가)?\s*$`)

func marketQuestionBindingTableContributor(headers, cells []string) (string, bool) {
	contributor := ""
	found := false
	for column, header := range headers {
		switch strings.TrimSpace(header) {
		case "거래소", "거래소명", "기여 거래소":
		default:
			continue
		}
		if column >= len(cells) || found {
			return "", true
		}
		found = true
		contributor = marketQuestionExchangeNames[strings.ToLower(strings.TrimSpace(cells[column]))]
	}
	return contributor, found
}

func marketQuestionBindingAdjacentDenominatorAllowed(sentence, denominator string, previous []marketQuestionBoundNumber) bool {
	metric := marketQuestionBindingMetric(sentence, "PCT")
	applicable := false
	for _, claim := range previous {
		if metric != "" && metric != claim.metric {
			continue
		}
		applicable = true
		if denominator == marketQuestionBindingFactDenominator(claim.metric) {
			return true
		}
	}
	// A new explicitly named ratio is not an anaphoric explanation of the
	// previous ratio. Its own numbered claim uses the normal binding path.
	return !applicable
}

func marketQuestionBindingMetricUnit(metric, unit string) bool {
	switch {
	case strings.HasSuffix(metric, "_PCT"):
		return unit == "PCT"
	case strings.HasSuffix(metric, "_PP"):
		return unit == "PP"
	case strings.HasSuffix(metric, "_KRW"):
		return strings.HasPrefix(unit, "KRW")
	case metric == "TRADE_COUNT":
		return unit == "COUNT"
	}
	return false
}

func marketQuestionBindingExchange(prefix, unit string) string {
	if unit == "PCT" || unit == "PP" {
		// Names inside a denominator (including all-five exchange lists) are not
		// the numerator subject. Preserve the outside subject or explicit query.
		prefix = marketQuestionBindingDenominator.ReplaceAllString(prefix, " ")
	}
	locations := marketQuestionBindingVenue.FindAllStringIndex(prefix, -1)
	last, exchange := -1, ""
	for _, at := range locations {
		if marketQuestionBindingComparativeOperand.MatchString(prefix[at[1]:]) {
			continue
		}
		last = at[0]
		exchange = marketQuestionExchangeNames[strings.ToLower(prefix[at[0]:at[1]])]
	}
	if unit != "PCT" && unit != "PP" {
		domestic := marketQuestionBindingDomestic.FindAllStringIndex(prefix, -1)
		if len(domestic) > 0 && domestic[len(domestic)-1][0] > last && !marketQuestionBindingComparativeOperand.MatchString(prefix[domestic[len(domestic)-1][1]:]) {
			return "domestic"
		}
		if strings.LastIndex(prefix, "경쟁") > last {
			return "competitor"
		}
	} else if unit == "PP" && strings.Contains(prefix, "기여") && strings.Contains(prefix, "거래대금 변화") {
		// In an exchange decomposition the contributing venue remains the
		// subject even when the effect is described as Bithumb share change.
		before, _, _ := strings.Cut(prefix, "거래대금 변화")
		if mentions := marketQuestionBindingVenue.FindAllString(before, -1); len(mentions) > 0 {
			return marketQuestionExchangeNames[strings.ToLower(mentions[len(mentions)-1])]
		}
	}
	return exchange
}

func marketQuestionBindingAssetPatterns(assets map[string]bool) []marketQuestionBindingAssetPattern {
	keys := make([]string, 0, len(assets))
	for asset := range assets {
		keys = append(keys, asset)
	}
	sort.Strings(keys)
	patterns := make([]marketQuestionBindingAssetPattern, 0, len(keys))
	for _, asset := range keys {
		patterns = append(patterns, marketQuestionBindingAssetPattern{asset, regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(asset) + `\b`)})
	}
	return patterns
}

func marketQuestionBindingAsset(prefix string, patterns []marketQuestionBindingAssetPattern) string {
	last, selected := -1, ""
	for _, item := range patterns {
		asset := item.asset
		for _, at := range item.pattern.FindAllStringIndex(prefix, -1) {
			if at[0] > last {
				last, selected = at[0], asset
			}
		}
		for name, symbol := range marketQuestionKoreanAssetNames {
			if symbol == asset {
				if at := strings.LastIndex(prefix, name); at > last {
					last, selected = at, asset
				}
			}
		}
	}
	return selected
}

func marketQuestionBindingMetric(prefix, unit string) string {
	if unit != "PCT" && !strings.HasPrefix(unit, "KRW") {
		return ""
	}
	// Look only after the latest numerical claim so one metric does not relabel
	// another later field in the same compound explanation.
	if numbers := marketQuestionNumericToken.FindAllStringIndex(prefix, -1); len(numbers) > 0 {
		prefix = prefix[numbers[len(numbers)-1][1]:]
	}
	if strings.HasPrefix(unit, "KRW") {
		for _, label := range []string{"거래대금 변화", "거래금액 변화", "거래대금 증감", "거래금액 증감", "증가액", "감소액", "변화값은", "변화량은", "증감액은"} {
			if strings.Contains(prefix, label) {
				return "VOLUME_DELTA_KRW"
			}
		}
		if strings.Contains(prefix, "거래대금") || strings.Contains(prefix, "거래금액") {
			return "VOLUME_LEVEL_KRW"
		}
		return ""
	}
	switch {
	case strings.Contains(prefix, "상대 변화율") || strings.Contains(prefix, "증감률"):
		return "LEGACY_RELATIVE"
	case strings.Contains(prefix, "포착률"):
		return "ASSET_CAPTURE_PCT"
	case strings.Contains(prefix, "내부 비중"):
		return "ASSET_INTERNAL_PCT"
	case strings.Contains(prefix, "수준 기여"):
		return "ASSET_TOTAL_SHARE_PCT"
	}
	return ""
}

func marketQuestionBindingRole(prefix, suffix string) string {
	last, role := -1, ""
	for _, label := range []struct{ text, role string }{{"시작", "previous"}, {"직전", "previous"}, {"이전", "previous"}, {"어제", "previous"}, {"그제", "previous"}, {"과거", "previous"}, {"종료", "current"}, {"현재", "current"}, {"최근", "current"}, {"끝", "current"}} {
		if at := strings.LastIndex(prefix, label.text); at > last && at >= 0 && len(prefix)-at < 100 && !marketQuestionNumericToken.MatchString(prefix[at+len(label.text):]) {
			tail := prefix[at+len(label.text):]
			// A statement that earlier evidence is absent, or an explicitly
			// excluded/comparative time, does not date the following current value.
			// Only the qualifier before this number is examined; an unrelated
			// limitation appended after an asserted past value cannot excuse it.
			if marketQuestionBindingUnavailableRole.MatchString(tail) || marketQuestionBindingDirectNegation.MatchString(tail) || marketQuestionBindingComparativeOperand.MatchString(tail) {
				continue
			}
			last, role = at, label.role
		}
	}
	if role != "" {
		return role
	}
	// Plain paired display "A원 → B원" / "A원에서 B원" preserves order.
	if at := strings.LastIndex(prefix, "→"); at >= 0 && !marketQuestionNumericToken.MatchString(prefix[at+len("→"):]) {
		return "current"
	}
	if at := strings.LastIndex(prefix, "에서"); at >= 0 && marketQuestionNumericToken.MatchString(prefix[:at]) && !marketQuestionNumericToken.MatchString(prefix[at+len("에서"):]) {
		return "current"
	}
	// The locative "에서" in "금액은 빗썸에서 집계" or "국내 금액에서
	// 차지한 비중" is not an earlier endpoint. Require an actual adjacent pair.
	if marketQuestionBindingEndpointPairStart.MatchString(suffix) {
		return "previous"
	}
	return ""
}

func marketQuestionBindingExplicitDenominator(sentence string) string {
	for _, text := range marketQuestionBindingExplicitDenominatorCandidates(sentence) {
		if strings.Contains(text, "동일 코인") || strings.Contains(text, "같은 코인") {
			return "domestic_asset"
		}
		if strings.Contains(text, "국내") {
			return "domestic"
		}
		if venues := marketQuestionBindingVenue.FindAllString(text, -1); len(venues) > 0 {
			return marketQuestionExchangeNames[strings.ToLower(venues[len(venues)-1])]
		}
	}
	return ""
}

func marketQuestionBindingExplicitDenominatorCandidates(sentence string) []string {
	var candidates []string
	for _, at := range marketQuestionBindingDenominator.FindAllStringSubmatchIndex(sentence, -1) {
		text := ""
		if at[2] >= 0 {
			text = sentence[at[2]:at[3]]
		} else {
			text = sentence[at[4]:at[5]]
		}
		// Negation qualifies this denominator only when attached to its operand,
		// not an unrelated later caveat such as "투자 권유는 하지 않습니다".
		if at[4] >= 0 && marketQuestionBindingDirectNegation.MatchString(sentence[at[1]:]) {
			continue
		}
		if clauses := marketQuestionBindingClause.Split(text, -1); len(clauses) > 0 {
			text = clauses[0]
		}
		if strings.Contains(text, "아닙") || strings.Contains(text, "아니라") {
			continue
		}
		candidates = append(candidates, text)
	}
	return candidates
}

func marketQuestionBindingPrecedingDenominator(prefix string) string {
	// A preceding "X 거래대금 대비" attaches directly to this number. A
	// trailing explanation of an earlier percentage must not leak into it.
	for _, at := range marketQuestionBindingDenominator.FindAllStringSubmatchIndex(prefix, -1) {
		if at[4] < 0 {
			continue
		}
		tail := strings.TrimSpace(prefix[at[1]:])
		if tail == "" || tail == "약" {
			return marketQuestionBindingExplicitDenominator(prefix[at[0]:at[1]])
		}
	}
	return ""
}

func marketQuestionBindingFactDenominator(metric string) string {
	switch metric {
	case "ASSET_CAPTURE_PCT", "ASSET_CAPTURE_DELTA_PP":
		return "domestic_asset"
	case "ASSET_INTERNAL_PCT", "ASSET_INTERNAL_DELTA_PP":
		return "bithumb"
	default:
		return "domestic"
	}
}

func marketQuestionBindingCurrentPeriodAllowed(sentence string, query marketanalysis.MarketQuestionQuery) bool {
	if query.Intent != marketanalysis.MarketQuestionCurrentOfficialShare || query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot {
		return true
	}
	if !strings.Contains(sentence, "거래대금") && !strings.Contains(sentence, "거래금액") && !strings.Contains(sentence, "점유율") {
		return true
	}
	if marketQuestionBindingCoordinatedDefinition(sentence) {
		return true
	}
	for _, clause := range marketQuestionBindingClause.Split(sentence, -1) {
		for _, duration := range marketQuestionBindingDuration.FindAllStringSubmatch(clause, -1) {
			if duration[1] == "24" && duration[2] == "시간" {
				continue
			}
			negative := false
			for _, marker := range []string{"아닙", "아니라", "다릅", "다르", "자료가 없", "자료는 없", "판단할 수 없", "확정할 수 없"} {
				if strings.Contains(clause, marker) {
					negative = true
					break
				}
			}
			if !negative {
				return false
			}
		}
	}
	return true
}
