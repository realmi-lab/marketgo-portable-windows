package app

import (
	"regexp"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

var marketQuestionFactReference = regexp.MustCompile(`^\[\[(fact_[a-p]{32})\]\]$`)

func marketQuestionComparisonReferences(facts []marketQuestionDisplayFact) map[string]string {
	index := map[string]marketQuestionDisplayFact{}
	for _, fact := range facts {
		if _, exists := index[fact.ID]; exists {
			return nil
		}
		index[fact.ID] = fact
	}
	references := map[string]string{}
	for _, fact := range facts {
		c := fact.Comparison
		if c == nil || len(c.SourceFactIDs) != 2 || c.SourceFactIDs[0] == c.SourceFactIDs[1] || c.Operation != "TARGET_MINUS_PEER" || c.Denominator != "domestic" || fact.EvidenceLayer != "CURRENT_OFFICIAL_TICKER" || fact.Quality != "LIVE" || fact.Period == "" {
			continue
		}
		metric := ""
		switch fact.Metric {
		case "EXCHANGE_SHARE_GAP_PP":
			metric = "EXCHANGE_SHARE_PCT"
		case "EXCHANGE_VOLUME_GAP_KRW":
			metric = "EXCHANGE_VOLUME_KRW"
		default:
			continue
		}
		a, aok := index[c.SourceFactIDs[0]]
		b, bok := index[c.SourceFactIDs[1]]
		if !aok || !bok || a.Exchange != fact.Exchange || b.Exchange != c.Against || a.Exchange == b.Exchange || a.Metric != metric || b.Metric != metric || a.Period != fact.Period || b.Period != fact.Period || a.EvidenceLayer != fact.EvidenceLayer || b.EvidenceLayer != fact.EvidenceLayer || a.Quality != "LIVE" || b.Quality != "LIVE" {
			continue
		}
		references[fact.ID] = fact.Text
	}
	return references
}

// References are complete atomic lines, not interpolated scalar values. The
// server supplies both parties, metric, unit and direction as one verified claim.
func resolveMarketQuestionFactReferences(answer marketanalysis.MarketQuestionAnswer, facts []marketQuestionDisplayFact) (marketanalysis.MarketQuestionAnswer, bool) {
	references := marketQuestionComparisonReferences(facts)
	valid := true
	resolve := func(text string) string {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if !strings.Contains(line, "[[") {
				continue
			}
			match := marketQuestionFactReference.FindStringSubmatch(strings.TrimSpace(line))
			if len(match) != 2 || references[match[1]] == "" {
				valid = false
				continue
			}
			lines[i] = references[match[1]]
		}
		return strings.Join(lines, "\n")
	}
	answer.Answer = resolve(answer.Answer)
	points := append([]string(nil), answer.KeyPoints...)
	for i := range points {
		points[i] = resolve(points[i])
	}
	answer.KeyPoints = points
	answer.Caveat = resolve(answer.Caveat)
	return answer, valid
}

// An exact server comparison line is already bound to its pair of source IDs.
// Other text still uses the existing guards; merely reusing a gap's number
// cannot authorize that number as another venue, metric or period.
func marketQuestionBoundComparisonLines(text string, facts []marketQuestionDisplayFact) map[string]bool {
	known := map[string]bool{}
	for _, text := range marketQuestionComparisonReferences(facts) {
		known[text] = true
	}
	bound := map[string]bool{}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if known[strings.TrimSpace(line)] {
			bound[strings.TrimSuffix(strings.TrimSpace(line), ".")] = true
		}
	}
	return bound
}
