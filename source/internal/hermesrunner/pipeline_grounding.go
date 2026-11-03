package hermesrunner

import (
	"encoding/json"
	"strings"

	"market-share-go/internal/notice"
)

// Background summaries use the same original-evidence hard guards as V16.
// The independent semantic audit remains necessary; these checks cannot
// establish semantic completeness by themselves.
func validatePipelineSummaryGrounding(revision notice.PipelineRevision, d notice.PipelineDerivation) error {
	sourceID := revision.Item.Exchange + ":" + revision.Item.NoticeID
	evidence, _ := json.Marshal(map[string]any{"notices": []any{map[string]any{"source_id": sourceID, "exchange": revision.Item.Exchange, "title": revision.Item.OriginalTitle, "original_body_text": revision.Item.OriginalBodyText, "body_scope": "FULL_STORED_ORIGINAL", "body_truncated": false, "assets": revision.Item.Assets}}})
	texts := append([]string{}, d.RequiredConditions...)
	for _, line := range d.Summary {
		texts = append(texts, line.Text)
	}
	result := noticeQuestionModelResult{Answer: strings.Join(texts, "\n"), SourceNoticeIDs: []string{sourceID}}
	if err := validateNoticeQuestionResultNumbers(result, "", evidence); err != nil {
		return err
	}
	if err := validateNoticeQuestionQuantities(result, evidence); err != nil {
		return err
	}
	return validateNoticeQuestionFactBindings(result.Answer, noticeQuestionClaimSources(evidence))
}
