package hermesrunner

import (
	"regexp"
	"strings"
)

type noticeQuestionRelationScope struct{ assets, exchanges []string }

var noticeRelationMarkdownHeading = regexp.MustCompile(`^(#{1,6})[\t ]+(.*)$`)

func noticeQuestionRelationNamedScope(text string, sources []noticeQuestionEventRelationSource) noticeQuestionRelationScope {
	result := noticeQuestionRelationScope{}
	// Reuse the existing exchange aliases/anchor boundaries, including explicit
	// uncited venues: absence from the evidence does not erase a named subject.
	venues := []noticeQuestionClaimSource{{Exchange: "upbit"}, {Exchange: "bithumb"}, {Exchange: "coinone"}, {Exchange: "korbit"}, {Exchange: "gopax"}}
	result.exchanges = noticeReasonMentionedExchanges(text, venues)
	seen := map[string]bool{}
	for _, source := range sources {
		for _, asset := range source.Assets {
			if noticeQuestionHasAnchor(text, asset) && !seen[asset] {
				result.assets = append(result.assets, asset)
				seen[asset] = true
			}
		}
	}
	for _, asset := range noticeClaimTicker.FindAllString(text, -1) {
		if len(asset) > 1 && !seen[asset] && asset != "KST" && asset != "UTC" && asset != "GMT" && asset != "API" && asset != "JSON" {
			result.assets = append(result.assets, asset)
			seen[asset] = true
		}
	}
	return result
}

func noticeQuestionRelationScopeAt(prefix string, sources []noticeQuestionEventRelationSource) noticeQuestionRelationScope {
	type frame struct {
		level int
		scope noticeQuestionRelationScope
	}
	var stack []frame
	for _, raw := range strings.Split(prefix, "\n") {
		line := strings.TrimSpace(raw)
		if line == "|" {
			stack = nil
			continue
		} // Answer/key-points/caveat boundary.
		scope := noticeQuestionRelationNamedScope(line, sources)
		level := 0
		if match := noticeRelationMarkdownHeading.FindStringSubmatch(line); len(match) > 0 {
			level = len(match[1])
		} else if len(scope.assets)+len(scope.exchanges) > 0 && !strings.ContainsAny(line, ".!?。") && !noticeClaimAction.MatchString(line) {
			// Plain/bold section labels and exchange colon labels follow the same
			// actor/alias semantics as the existing source-scoped prose guards.
			// A plain/bold actor label narrows the enclosing Markdown
			// section; it is not a new top-level section. Preserve the
			// other axis across consecutive partial actor labels too.
			level = 7
			if len(stack) > 0 && stack[len(stack)-1].level == 7 {
				previous := stack[len(stack)-1].scope
				if len(scope.assets) == 0 {
					scope.assets = previous.assets
				}
				if len(scope.exchanges) == 0 {
					scope.exchanges = previous.exchanges
				}
			}
		}
		if level == 0 {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, frame{level, scope})
	}
	result := noticeQuestionRelationScope{}
	for _, entry := range stack {
		if len(entry.scope.assets) > 0 {
			result.assets = entry.scope.assets
		}
		if len(entry.scope.exchanges) > 0 {
			result.exchanges = entry.scope.exchanges
		}
	}
	return result
}

func noticeQuestionRelationScopeMatches(scope noticeQuestionRelationScope, inline string, source noticeQuestionEventRelationSource, all []noticeQuestionEventRelationSource) bool {
	named := noticeQuestionRelationNamedScope(inline, all)
	// Existing prose scope gives explicit local subjects precedence over a
	// general section label. Anonymous relations inherit their section scope.
	if len(named.assets) > 0 {
		scope.assets = named.assets
	}
	if len(named.exchanges) > 0 {
		scope.exchanges = named.exchanges
	}
	for _, asset := range scope.assets {
		if asset != source.Assets[0] {
			return false
		}
	}
	for _, exchange := range scope.exchanges {
		if exchange != source.Exchange {
			return false
		}
	}
	return true
}
