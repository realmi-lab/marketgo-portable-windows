package app

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Spend a shared context budget on official body passages after retrieval.
// Small result sets retain substantially more context; a broad query keeps
// its source breadth. No asset, notice identifier, or reason template is used.
const noticeQuestionBodyRuneBudget = 2800

func enrichDomesticNoticeBodyPassages(evidence *domesticNoticeQuestionEvidence, question string) {
	terms := noticeQuestionSearchTermPattern.FindAllString(strings.ToLower(question), -1)
	for index := range terms {
		terms[index] = noticeQuestionNormalizeSearchTerm(terms[index])
	}
	budgets := domesticNoticeBodyBudgets(evidence.Notices, terms, noticeQuestionBodyRuneBudget)
	for index := range evidence.Notices {
		row := &evidence.Notices[index]
		row.BodyPassages, row.BodyTruncated = selectDomesticNoticeBodyPassages(row.bodyText, terms, budgets[index])
		row.BodyScope = "UNKNOWN"
		if len(row.BodyPassages) > 0 {
			row.BodyScope = "EXCERPT_ONLY"
			if row.bodyOriginalAvailable && !row.BodyTruncated && strings.Join(row.BodyPassages, "\n") == strings.TrimSpace(row.bodyText) {
				row.BodyScope = "FULL_STORED_ORIGINAL"
			}
		}
	}
}

// Preserve a minimum excerpt for every available body, then allocate the
// remaining context by inverse title-document frequency. A specific name in
// the question deserves more surrounding conditions than a topic shared by
// every candidate. Short bodies release their unused budget to other rows.
func domesticNoticeBodyBudgets(rows []domesticNoticeEvidence, terms []string, total int) []int {
	budgets := make([]int, len(rows))
	lengths := make([]int, len(rows))
	titles := make([]string, len(rows))
	count := 0
	for index, row := range rows {
		lengths[index] = utf8.RuneCountInString(strings.TrimSpace(row.bodyText))
		titles[index] = strings.ToLower(row.Title)
		if lengths[index] > 0 {
			count++
		}
	}
	if total <= 0 || count == 0 {
		return budgets
	}
	unique := make(map[string]bool)
	for _, term := range terms {
		if utf8.RuneCountInString(term) >= 2 {
			unique[term] = true
		}
	}
	orderedTerms := make([]string, 0, len(unique))
	for term := range unique {
		orderedTerms = append(orderedTerms, term)
	}
	sort.Strings(orderedTerms)
	weights := make([]float64, len(rows))
	for _, term := range orderedTerms {
		frequency := 0
		for _, title := range titles {
			if strings.Contains(title, term) {
				frequency++
			}
		}
		if frequency == 0 {
			continue
		}
		for index, title := range titles {
			if strings.Contains(title, term) {
				weights[index] += 1 / float64(frequency)
			}
		}
	}
	minimum := make([]int, len(rows))
	remaining := total
	for index := range rows {
		minimum[index] = min(lengths[index], total/count/2)
		budgets[index] = minimum[index]
		remaining -= minimum[index]
	}
	// A bounded highest-average allocation avoids rounding away small shares.
	// Stable row order resolves ties; no map iteration affects the result.
	for remaining > 0 {
		best := -1
		bestScore := -1.0
		for index := range rows {
			if budgets[index] >= lengths[index] {
				continue
			}
			score := weights[index] / float64(budgets[index]-minimum[index]+1)
			if score > bestScore {
				best, bestScore = index, score
			}
		}
		if best < 0 {
			break
		}
		if bestScore == 0 {
			// With no remaining title match, share context evenly instead of
			// making catalog order the source of relevance.
			for index := range rows {
				if budgets[index] >= lengths[index] {
					continue
				}
				score := 1 / float64(budgets[index]-minimum[index]+1)
				if score > bestScore {
					best, bestScore = index, score
				}
			}
		}
		budgets[best]++
		remaining--
	}
	return budgets
}

func selectDomesticNoticeBodyPassages(body string, terms []string, budget int) ([]string, bool) {
	body = strings.TrimSpace(body)
	if body == "" || budget <= 0 {
		return nil, body != ""
	}
	if utf8.RuneCountInString(body) <= budget {
		return []string{body}, false
	}
	// Paragraph/line boundaries retain the label next to its following facts.
	// A large paragraph is split with a little overlap so a sentence spanning
	// the boundary can still be interpreted. Chunks are always source substrings.
	chunkSize := min(600, budget)
	var passages []string
	seen := map[string]bool{}
	appendPassage := func(text string) {
		if text != "" && !seen[text] {
			seen[text] = true
			passages = append(passages, text)
		}
	}
	var current strings.Builder
	flush := func() {
		if value := strings.TrimSpace(current.String()); value != "" {
			runes := []rune(value)
			for len(runes) > chunkSize {
				appendPassage(string(runes[:chunkSize]))
				runes = runes[chunkSize-min(60, chunkSize/4):]
			}
			if len(runes) > 0 {
				appendPassage(string(runes))
			}
		}
		current.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	flush()
	// Inverse passage frequency favors specific requested information over a
	// repeated asset/name in a salutation, heading or boilerplate paragraph.
	frequencies := make(map[string]int)
	for _, term := range terms {
		if utf8.RuneCountInString(term) < 2 {
			continue
		}
		if _, seen := frequencies[term]; seen {
			continue
		}
		frequencies[term] = 0
		for _, passage := range passages {
			if strings.Contains(strings.ToLower(passage), term) {
				frequencies[term]++
			}
		}
	}
	order := make([]int, len(passages))
	scores := make([]float64, len(passages))
	orderedTerms := make([]string, 0, len(frequencies))
	for term := range frequencies {
		orderedTerms = append(orderedTerms, term)
	}
	sort.Strings(orderedTerms)
	for i, passage := range passages {
		order[i] = i
		for _, term := range orderedTerms {
			frequency := frequencies[term]
			if frequency > 0 && strings.Contains(strings.ToLower(passage), term) {
				scores[i] += 1 / float64(frequency)
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return scores[order[i]] > scores[order[j]] })
	selected := make(map[int]string)
	remaining := budget
	for _, index := range order {
		if remaining <= 0 {
			break
		}
		text := passages[index]
		length := utf8.RuneCountInString(text)
		if length > remaining {
			// Keep a useful excerpt centered on the query, never a bare prefix
			// that could drop the only matching words in a long paragraph.
			text = domesticNoticePassageWindow(text, terms, remaining)
		}
		selected[index] = text
		remaining -= utf8.RuneCountInString(text)
	}
	result := make([]string, 0, len(selected))
	for i := range passages {
		if text, ok := selected[i]; ok {
			result = append(result, text)
		}
	}
	return result, true
}

func domesticNoticePassageWindow(text string, terms []string, budget int) string {
	runes := []rune(text)
	if len(runes) <= budget {
		return text
	}
	lower := strings.ToLower(text)
	start := 0
	for _, term := range terms {
		if len(term) == 0 {
			continue
		}
		if index := strings.Index(lower, term); index >= 0 {
			start = max(0, utf8.RuneCountInString(lower[:index])-budget/4)
			break
		}
	}
	start = min(start, len(runes)-budget)
	return string(runes[start : start+budget])
}
