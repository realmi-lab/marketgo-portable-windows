package app

import (
	"regexp"
	"strings"
)

var marketQuestionMarkdownHeading = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$`)

// marketQuestionMarkdownSection returns one Markdown section without tying the
// validator to the sentences a prose model happens to choose. A section ends
// at the next heading of the same or a higher level.
func marketQuestionMarkdownSection(text, targetHeading string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	target := normalizeMarketQuestionHeading(targetHeading)
	start, level := -1, 0
	for index, line := range lines {
		match := marketQuestionMarkdownHeading.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		currentLevel := len(match[1])
		if start < 0 {
			if normalizeMarketQuestionHeading(match[2]) == target {
				start, level = index+1, currentLevel
			}
			continue
		}
		if currentLevel <= level {
			return strings.TrimSpace(strings.Join(lines[start:index], "\n")), true
		}
	}
	if start >= 0 {
		return strings.TrimSpace(strings.Join(lines[start:], "\n")), true
	}
	return "", false
}

// marketQuestionMarkdownSectionIntro excludes child subsections such as
// positive/negative driver tables. Those headings describe attribution, not
// the direction of the section's primary share comparison.
func marketQuestionMarkdownSectionIntro(text, targetHeading string) (string, bool) {
	section, found := marketQuestionMarkdownSection(text, targetHeading)
	if !found {
		return "", false
	}
	lines := strings.Split(section, "\n")
	for index, line := range lines {
		if marketQuestionMarkdownHeading.MatchString(line) {
			return strings.TrimSpace(strings.Join(lines[:index], "\n")), true
		}
	}
	return strings.TrimSpace(section), true
}

// replaceMarketQuestionMarkdownSection keeps the original heading and replaces
// only its contents. It is used to combine a narrowly allowed model-authored
// section with server-authored factual sections without depending on the
// model's exact prose.
func replaceMarketQuestionMarkdownSection(text, targetHeading, replacement string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	target := normalizeMarketQuestionHeading(targetHeading)
	headingIndex, endIndex, level := -1, len(lines), 0
	for index, line := range lines {
		match := marketQuestionMarkdownHeading.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		currentLevel := len(match[1])
		if headingIndex < 0 {
			if normalizeMarketQuestionHeading(match[2]) == target {
				headingIndex, level = index, currentLevel
			}
			continue
		}
		if currentLevel <= level {
			endIndex = index
			break
		}
	}
	if headingIndex < 0 {
		return text, false
	}
	result := make([]string, 0, len(lines)+strings.Count(replacement, "\n"))
	result = append(result, lines[:headingIndex+1]...)
	if replacement = strings.TrimSpace(replacement); replacement != "" {
		result = append(result, "", replacement)
	}
	if endIndex < len(lines) {
		result = append(result, "")
		result = append(result, lines[endIndex:]...)
	}
	return strings.TrimSpace(strings.Join(result, "\n")), true
}

// marketQuestionMarkdownTableRows returns GFM table rows as cells while
// discarding delimiter rows. Keeping the row boundary is important for
// grounded-answer validation: a date or asset in one row must not authorize a
// numeric claim copied from another row.
func marketQuestionMarkdownTableRows(text string) [][]string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	rows := make([][]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Count(line, "|") < 2 {
			continue
		}
		line = strings.TrimPrefix(line, "|")
		line = strings.TrimSuffix(line, "|")
		rawCells := strings.Split(line, "|")
		cells := make([]string, 0, len(rawCells))
		delimiterRow := true
		for _, cell := range rawCells {
			cell = strings.TrimSpace(cell)
			cells = append(cells, cell)
			if !marketQuestionMarkdownTableDelimiterCell(cell) {
				delimiterRow = false
			}
		}
		if len(cells) >= 2 && !delimiterRow {
			rows = append(rows, cells)
		}
	}
	return rows
}

func marketQuestionMarkdownTableDelimiterCell(value string) bool {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, ":")
	value = strings.TrimSuffix(value, ":")
	if len(value) < 3 {
		return false
	}
	for _, current := range value {
		if current != '-' {
			return false
		}
	}
	return true
}

func normalizeMarketQuestionHeading(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "*_`~"))
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func marketQuestionDefinitiveDirectionClaims(text string) (rise, fall bool) {
	for _, clause := range marketQuestionDirectionClauses(strings.ToLower(text)) {
		clause = strings.TrimSpace(clause)
		if clause == "" || marketQuestionUncertaintyClaim(clause) {
			continue
		}
		rise = rise || marketQuestionPrimaryRiseClaim.MatchString(clause)
		fall = fall || marketQuestionPrimaryFallClaim.MatchString(clause)
	}
	return rise, fall
}

func marketQuestionDirectionClauses(text string) []string {
	runes := []rune(text)
	start := 0
	result := make([]string, 0, strings.Count(text, "\n")+1)
	appendClause := func(end int) {
		if value := strings.TrimSpace(string(runes[start:end])); value != "" {
			result = append(result, value)
		}
		start = end + 1
	}
	for index, value := range runes {
		separator := false
		switch value {
		case '\n', '\r', '!', '?', '。', '！', '？':
			separator = true
		case '.':
			// A decimal point belongs to the same evidence claim. Sentence
			// punctuation does not have digits on both sides.
			separator = index == 0 || index+1 == len(runes) ||
				!isMarketQuestionASCIIDigit(runes[index-1]) || !isMarketQuestionASCIIDigit(runes[index+1])
		}
		if separator {
			appendClause(index)
		}
	}
	if start < len(runes) {
		if value := strings.TrimSpace(string(runes[start:])); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func isMarketQuestionASCIIDigit(value rune) bool {
	return value >= '0' && value <= '9'
}

func marketQuestionUncertaintyClaim(text string) bool {
	for _, marker := range []string{
		"확정할 수 없", "확정하지 않", "완전하지 않", "자료가 부족", "미확보",
		"확정 전", "계산할 수 없", "비교할 수 없", "대기", "불확실",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func marketQuestionSectionDirectionAllowed(text string, delta *float64, confirmed bool) bool {
	rise, fall := marketQuestionDefinitiveDirectionClaims(text)
	if !confirmed || delta == nil {
		return !rise && !fall && marketQuestionUncertaintyClaim(strings.ToLower(text))
	}
	switch {
	case *delta >= 0.005:
		return !fall
	case *delta <= -0.005:
		return !rise
	default:
		return !rise && !fall
	}
}
