package app

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Parts preserve user requests, never facts or executable retrieval commands.
// This bounds and anchors planner metadata; it is not a semantic proof that
// the planner found every request or that the answer fulfilled each one.
func validateMarketQuestionParts(question string, parts []string) error {
	if len(parts) > 8 {
		return errors.New("planner returned too many question parts")
	}
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		if !utf8.ValidString(part) || strings.TrimSpace(part) == "" ||
			utf8.RuneCountInString(part) > 500 || !strings.Contains(question, part) || seen[part] {
			return errors.New("planner question part is not a unique verbatim user request")
		}
		seen[part] = true
	}
	return nil
}
