package hermesrunner

import (
	"strings"
	"unicode/utf8"
)

// NoticeQuestionEventStatement is generation guidance about a particular
// supplied passage, not an independently observed service state. The original
// passages remain the only fact authority; no clock or derived summary is read.
type NoticeQuestionEventStatement struct {
	Passage      int    `json:"passage_index"`
	Event        string `json:"event"`
	State        string `json:"state"`
	Text         string `json:"statement"`
	RevisionRole string `json:"revision_role,omitempty"`
}

func NoticeQuestionEventStatements(sourceID, exchange, title string, assets, passages []string) []NoticeQuestionEventStatement {
	source := noticeQuestionClaimSource{SourceID: sourceID, Exchange: exchange, Title: title, Assets: assets, Passages: passages}
	identifiers := map[string]bool{"KST": true, "UTC": true, "GMT": true}
	for _, asset := range assets {
		identifiers[strings.ToUpper(asset)] = true
	}
	var result []NoticeQuestionEventStatement
	seen := map[string]bool{}
	for index, passage := range passages {
		for _, claim := range noticeQuestionTextClaims(noticeQuestionGroundingText(passage), title, identifiers, source) {
			if claim.negative || claim.action == "" || claim.topic == "" || claim.action == "AVAILABLE" || utf8.RuneCountInString(claim.text) > 180 {
				continue
			}
			state := ""
			switch claim.mode {
			case "COMPLETED":
				state = "COMPLETION_STATED"
			case "PLANNED":
				state = "SCHEDULE_STATED"
			case "CONDITIONAL", "POSSIBLE":
				continue
			default:
				if len(claim.values) > 0 && strings.Contains(claim.text, "일정") {
					state = "SCHEDULE_STATED"
				}
			}
			if state == "" {
				continue
			}
			key := state + "\x00" + claim.text
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, NoticeQuestionEventStatement{Passage: index, Event: claim.topic + "/" + claim.action, State: state, Text: claim.text, RevisionRole: claim.role})
			if len(result) == 2 {
				return result
			}
		}
	}
	return result
}
