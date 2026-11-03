package notice

import "strings"

// BuildOfficialSummary projects at most maxLines concise facts from official
// title/body evidence. It is deterministic so domestic notices can expose a
// useful card summary without spending a background model request.
func BuildOfficialSummary(item Item, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	if strings.TrimSpace(item.OriginalBodyText) != "" {
		item.BodyExcerpt = item.OriginalBodyText
	}
	brief := Summarize(item)
	lines := make([]string, 0, maxLines)
	seen := make(map[string]bool, maxLines)
	appendLine := func(label, value string) {
		label, value = strings.TrimSpace(label), strings.TrimSpace(value)
		if value == "" || len(lines) >= maxLines {
			return
		}
		line := value
		if label != "" {
			line = label + ": " + value
		}
		key := strings.ToLower(briefWhitespacePattern.ReplaceAllString(line, ""))
		if seen[key] {
			return
		}
		seen[key] = true
		lines = append(lines, line)
	}
	for _, fact := range brief.Lines {
		appendLine(fact.Label, fact.Value)
	}
	if len(lines) == 0 {
		appendLine("핵심 내용", brief.Headline)
	}
	return strings.Join(lines, "\n")
}
