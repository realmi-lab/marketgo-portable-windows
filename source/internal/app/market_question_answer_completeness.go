package app

import (
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

// This is a minimum question-fulfillment check, not a prose-length target or a
// proof that every explanatory subquestion was answered. The existing numeric,
// entity, binding and safety guards must also pass. Daily reports retain their
// dedicated structure/binding check. Caveat alone cannot stand in for an answer.
func marketQuestionAnswerCompletenessAllowed(answer marketanalysis.MarketQuestionAnswer, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery, question ...string) bool {
	if len(query.ComparedAssets) > 1 {
		text := answer.Answer + "\n" + strings.Join(answer.KeyPoints, "\n")
		claims := marketQuestionCompletenessClaims(text, facts, query)
		venue, metric := "domestic", "DOMESTIC_VOLUME_KRW"
		if query.Exchange == "bithumb" {
			venue, metric = "bithumb", "EXCHANGE_VOLUME_KRW"
		}
		for _, asset := range query.ComparedAssets {
			available, answered := false, false
			for _, fact := range facts {
				if fact.Asset != asset || fact.Exchange != venue || fact.Metric != metric {
					continue
				}
				values := marketQuestionCompletenessFactValues(fact)
				available = available || len(values) > 0
				answered = answered || marketQuestionCompletenessValuesAnswered(values, claims)
			}
			if available && !answered || !available && !marketQuestionUncertaintyClaim(strings.ToLower(text)) {
				return false
			}
			if len(question) > 0 && containsAnyString(question[0], "점유율", "비중", "포착률") {
				item := query
				item.Asset, item.ComparedAssets = asset, nil
				if !marketQuestionAnswerCompletenessAllowed(answer, facts, item, question...) {
					return false
				}
			}
		}
		return marketQuestionBindingVenueCountsAllowed(text, facts, query)
	}
	if query.PeerExchange != "" && query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
		peer := query
		peer.Exchange, peer.PeerExchange = query.PeerExchange, ""
		peer.Metric = marketanalysis.MarketQuestionMetricOverallShare
		if !marketQuestionAnswerCompletenessAllowed(answer, facts, peer, question...) {
			return false
		}
	}
	if marketQuestionDailyEndpointSeriesRequested(query) {
		return true
	}
	groups := marketQuestionCompletenessMetricGroups(query, question...)
	text := answer.Answer + "\n" + strings.Join(answer.KeyPoints, "\n")
	text = strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	if !marketQuestionBindingVenueCountsAllowed(text, facts, query) {
		return false
	}
	claims := marketQuestionCompletenessClaims(text, facts, query)
	exchange := strings.ToLower(query.Exchange)
	if exchange == "" {
		exchange = "bithumb"
	}
	asset := strings.ToUpper(query.Asset)
	// Legacy projections are still display text, not fully typed claims. They
	// can establish availability per requested metric, but do not gain a typed
	// binding guarantee merely by passing this minimum-fulfillment check.
	legacy := []string{}
	for _, fact := range facts {
		// Equal-window metrics are fully projected by ledger.interval. Legacy
		// prose can describe missing data (including hypothetical 0원), but
		// cannot establish an observed metric absent from those typed facts.
		if fact.Metric == "LEGACY_DISPLAY" && query.Comparison != marketanalysis.MarketQuestionCurrentVsPreviousInterval {
			legacy = append(legacy, fact.Text)
		}
	}
	// Legacy availability comes from server-owned text, not a model claim. It
	// may remain available without a typed projection (for example, unknown
	// ticker observation time). Apply the scope/count guard to the answer above,
	// not this fallback source, or an absent projection erases known metrics.
	legacyValues := marketQuestionCompletenessClaims(strings.Join(legacy, "\n"), facts, query)
	missing := false
	for _, group := range groups {
		available, answered := false, false
		for _, fact := range facts {
			if fact.Exchange != exchange || strings.ToUpper(fact.Asset) != asset || !marketQuestionCompletenessContains(group, fact.Metric) {
				continue
			}
			values := marketQuestionCompletenessFactValues(fact)
			if len(values) == 0 {
				continue
			}
			available = true
			answered = answered || marketQuestionCompletenessValuesAnswered(values, claims)
		}
		if !available {
			for _, value := range legacyValues {
				if value.exchange != exchange || value.asset != asset || !marketQuestionCompletenessPrimaryUnit(value, [][]string{group}) {
					continue
				}
				available = true
				answered = answered || marketQuestionCompletenessValuesAnswered([]marketQuestionBoundNumber{value}, claims)
			}
		}
		if available && !answered {
			return false
		}
		missing = missing || !available
	}
	// A missing requested metric cannot erase another available requested
	// metric, or be silently passed off as a complete combined answer.
	return !missing || marketQuestionUncertaintyClaim(strings.ToLower(text))
}

func marketQuestionCompletenessMetricGroups(query marketanalysis.MarketQuestionQuery, question ...string) [][]string {
	change := query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta || query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval
	asset := func(level, delta string) []string {
		if change {
			return []string{delta, level}
		}
		return []string{level}
	}
	switch query.Metric {
	case marketanalysis.MarketQuestionMetricAssetCapture:
		return [][]string{asset("ASSET_CAPTURE_PCT", "ASSET_CAPTURE_DELTA_PP")}
	case marketanalysis.MarketQuestionMetricAssetInternal:
		return [][]string{asset("ASSET_INTERNAL_PCT", "ASSET_INTERNAL_DELTA_PP")}
	case marketanalysis.MarketQuestionMetricContribution:
		return [][]string{asset("ASSET_TOTAL_SHARE_PCT", "ASSET_TOTAL_SHARE_DELTA_PP")}
	case marketanalysis.MarketQuestionMetricAssetCombined:
		return [][]string{
			asset("ASSET_CAPTURE_PCT", "ASSET_CAPTURE_DELTA_PP"),
			asset("ASSET_INTERNAL_PCT", "ASSET_INTERNAL_DELTA_PP"),
			asset("ASSET_TOTAL_SHARE_PCT", "ASSET_TOTAL_SHARE_DELTA_PP"),
		}
	default:
		share := []string{"EXCHANGE_SHARE_PCT"}
		volume := []string{"EXCHANGE_VOLUME_KRW"}
		if change {
			share = append(share, "SHARE_DELTA_PP")
			volume = append(volume, "VOLUME_DELTA_KRW")
		}
		if len(question) > 0 {
			text := strings.Join(strings.Fields(strings.ToLower(question[0])), "")
			requested := [][]string{}
			if marketQuestionCompletenessMetricMention(text, []string{"점유율", "비중", "비율", "marketshare"}, false) {
				requested = append(requested, share)
			}
			if marketQuestionCompletenessMetricMention(text, []string{"거래금액", "거래대금", "거래액", "turnover", "tradingvalue"}, true) {
				requested = append(requested, volume)
			}
			if len(requested) > 0 {
				return requested
			}
		}
		// The validated exchange metric combines share and turnover. If the
		// latest question does not explicitly distinguish them, either remains
		// a legitimate minimum answer; explicit requests above never use OR.
		return [][]string{append(share, volume...)}
	}
}

func marketQuestionCompletenessMetricMention(question string, aliases []string, amount bool) bool {
	for _, alias := range aliases {
		for offset := 0; offset < len(question); {
			index := strings.Index(question[offset:], alias)
			if index < 0 {
				break
			}
			offset += index + len(alias)
			suffix := question[offset:]
			if marketQuestionTargetIsCorrected(suffix) {
				continue
			}
			for _, particle := range []string{"은", "는", "이", "가", "을", "를", "의"} {
				if strings.HasPrefix(suffix, particle) {
					suffix = strings.TrimPrefix(suffix, particle)
					break
				}
			}
			if marketQuestionCompletenessHasPrefix(suffix, "제외", "빼고", "필요없", "아니고") {
				continue
			}
			// A turnover mention defining the share denominator is not a
			// separate request for an amount (unlike "점유율과 거래금액").
			if amount && marketQuestionCompletenessHasPrefix(suffix, "대비", "기준", "중", "에서", "분모", "비중", "비율", "점유율", "으로나눈", "으로나눠") {
				continue
			}
			return true
		}
	}
	return false
}

func marketQuestionCompletenessHasPrefix(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func marketQuestionCompletenessContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func marketQuestionCompletenessPrimaryUnit(value marketQuestionBoundNumber, groups [][]string) bool {
	for _, group := range groups {
		for _, metric := range group {
			if marketQuestionBindingMetricUnit(metric, value.unit) && marketQuestionCompletenessMetricMatches(value.metric, metric) {
				return true
			}
		}
	}
	return false
}

func marketQuestionCompletenessMetricMatches(claim, fact string) bool {
	return claim == "" || claim == fact || (claim == "VOLUME_LEVEL_KRW" && (fact == "EXCHANGE_VOLUME_KRW" || fact == "DOMESTIC_VOLUME_KRW"))
}

func marketQuestionCompletenessFactValues(fact marketQuestionDisplayFact) []marketQuestionBoundNumber {
	body := strings.TrimPrefix(fact.Text, fact.Period)
	values := []marketQuestionBoundNumber{}
	for _, at := range marketQuestionNumericToken.FindAllStringIndex(body, -1) {
		unit := marketQuestionNumericUnit(body[at[1]:])
		if !marketQuestionBindingMetricUnit(fact.Metric, unit) {
			continue
		}
		value, ok := normalizeMarketQuestionNumericToken(body[at[0]:at[1]])
		if ok {
			values = append(values, marketQuestionBoundNumber{value: value, unit: unit, exchange: fact.Exchange, asset: strings.ToUpper(fact.Asset), metric: fact.Metric})
		}
	}
	if len(values) == 2 && strings.Contains(body, "→") {
		values[0].role, values[1].role = "previous", "current"
	}
	return values
}

func marketQuestionCompletenessValuesAnswered(values, claims []marketQuestionBoundNumber) bool {
	used := make([]bool, len(claims))
	for _, value := range values {
		matched := false
		for index, claim := range claims {
			if used[index] || claim.value != value.value || claim.unit != value.unit || claim.exchange != value.exchange || claim.asset != value.asset || !marketQuestionCompletenessMetricMatches(claim.metric, value.metric) {
				continue
			}
			if claim.role != "" && value.role != "" && claim.role != value.role {
				continue
			}
			used[index], matched = true, true
			break
		}
		if !matched {
			return false
		}
	}
	return len(values) > 0
}

// Reuse the binding parser's supported explicit subject/table conventions.
// Unsupported implicit relationships are not newly certified by this check.
func marketQuestionCompletenessClaims(text string, facts []marketQuestionDisplayFact, query marketanalysis.MarketQuestionQuery) []marketQuestionBoundNumber {
	assets := map[string]bool{}
	for _, fact := range facts {
		if fact.Asset != "" {
			assets[strings.ToUpper(fact.Asset)] = true
		}
	}
	if query.Asset != "" {
		assets[strings.ToUpper(query.Asset)] = true
	}
	assetPatterns := marketQuestionBindingAssetPatterns(assets)
	text = strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	var headers []string
	claims := []marketQuestionBoundNumber{}
	for _, sentence := range marketQuestionBindingSentence.Split(text, -1) {
		trimmed := strings.TrimSpace(sentence)
		if strings.HasPrefix(trimmed, "|") {
			if marketQuestionBindingTableSeparator.MatchString(sentence) {
				continue
			}
			if hasValue, _ := marketQuestionBindingNumericKinds(sentence); len(headers) == 0 && !hasValue {
				headers = strings.Split(sentence, "|")
				continue
			}
		} else {
			headers = nil
		}
		groupBindings, groupsAllowed := marketQuestionBindingGroups(sentence)
		if !groupsAllowed {
			continue
		}
		for _, at := range marketQuestionNumericToken.FindAllStringIndex(sentence, -1) {
			unit := marketQuestionNumericUnit(sentence[at[1]:])
			if unit != "PCT" && unit != "PP" && !strings.HasPrefix(unit, "KRW") {
				continue
			}
			value, ok := normalizeMarketQuestionNumericToken(sentence[at[0]:at[1]])
			if !ok {
				continue
			}
			prefix := sentence[:at[0]]
			claim := marketQuestionBoundNumber{value: value, unit: unit, exchange: marketQuestionBindingExchange(prefix, unit), asset: marketQuestionBindingAsset(prefix, assetPatterns), metric: marketQuestionBindingMetric(prefix, unit), role: marketQuestionBindingRole(prefix, sentence[at[1]:])}
			grouped := groupBindings[at[0]]
			if grouped != "" {
				claim.exchange = grouped
			}
			aggregate := marketQuestionBindingAmountReference(prefix, sentence[at[1]:], unit, facts, query)
			postfix := marketQuestionBindingPostfixExchange(sentence[at[1]:])
			if marketQuestionBindingAggregateConflict(aggregate, postfix) || marketQuestionBindingGroupConflict(grouped, aggregate, postfix) {
				continue
			}
			if aggregate != "" {
				claim.exchange = aggregate
			}
			if postfix != "" {
				claim.exchange = postfix
			}
			if column := strings.Count(prefix, "|"); len(headers) > column {
				if asset := marketQuestionBindingAsset(headers[column], assetPatterns); asset != "" {
					claim.asset = asset
				}
				if exchange := marketQuestionBindingTableExchange(headers, strings.Split(sentence, "|"), column, unit); exchange != "" {
					if marketQuestionBindingGroupConflict(grouped, exchange) {
						continue
					}
					claim.exchange = exchange
				}
				if role := marketQuestionBindingRole(headers[column], ""); role != "" {
					claim.role = role
				}
			}
			if claim.exchange == "" {
				claim.exchange = strings.ToLower(query.Exchange)
				if claim.exchange == "" {
					claim.exchange = "bithumb"
				}
			}
			if claim.asset == "" && strings.HasPrefix(claim.metric, "ASSET_") {
				claim.asset = strings.ToUpper(query.Asset)
			}
			claims = append(claims, claim)
		}
	}
	return claims
}
