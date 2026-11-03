package hermesrunner

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/noticewriter"
)

const (
	maxNoticeQuestionPrompt     = 64_000
	maxNoticeQuestionSources    = 10
	noticeQuestionPrimaryModel  = MarketQuestionModel
	noticeQuestionPrimaryEffort = MarketQuestionReasoningEffort
	// Generation and the mandatory full audit share one five-minute budget.
	// A shorter caller deadline and cancellation remain authoritative.
	noticeQuestionHermesTimeout = 5 * time.Minute
)

//go:embed prompts/domestic-notice-question.md
var noticeQuestionPromptTemplate string

//go:embed prompts/domestic-notice-writer.md
var noticeQuestionWriterPromptTemplate string

//go:embed prompts/domestic-notice-audit.md
var noticeQuestionAuditPromptTemplate string

var (
	noticeQuestionNumericToken = regexp.MustCompile(`[+-]?(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?`)
	bithumbDraftTickerToken    = regexp.MustCompile(`[A-Z0-9][A-Z0-9._-]{0,14}`)
)

type noticeQuestionModelResult struct {
	Answer          string   `json:"answer"`
	KeyPoints       []string `json:"key_points"`
	Caveat          string   `json:"caveat"`
	SourceNoticeIDs []string `json:"source_notice_ids"`
}

type noticeQuestionStyleExample struct {
	SourceID       string `json:"source_id"`
	OpeningExcerpt string `json:"opening_excerpt"`
	ClosingExcerpt string `json:"closing_excerpt"`
}

func AskNoticeQuestion(
	ctx context.Context,
	config MarketQuestionConfig,
	persona, question string,
	evidenceJSON []byte,
	asOfMS int64,
) (marketanalysis.NoticeQuestionAnswer, error) {
	return askNoticeQuestionWithInvokers(
		ctx, config, persona, question, evidenceJSON, asOfMS, invokeHermesMarketQuestion,
	)
}

func askNoticeQuestionWithInvokers(
	ctx context.Context,
	config MarketQuestionConfig,
	persona, question string,
	evidenceJSON []byte,
	asOfMS int64,
	hermesInvoker marketQuestionInvoker,
) (marketanalysis.NoticeQuestionAnswer, error) {
	if err := ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	questionCtx, cancel := context.WithTimeout(ctx, noticeQuestionHermesTimeout)
	defer cancel()
	persona, question = strings.TrimSpace(persona), strings.TrimSpace(question)
	if persona == "" || question == "" || !utf8.ValidString(persona+question) {
		return marketanalysis.NoticeQuestionAnswer{}, errors.New("notice question and persona must be valid UTF-8 text")
	}
	if !json.Valid(evidenceJSON) {
		return marketanalysis.NoticeQuestionAnswer{}, errors.New("notice question evidence must be valid JSON")
	}
	if err := noticeQuestionEvidenceAvailable(persona, evidenceJSON); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	prompt := noticeQuestionPrompt(persona, question, evidenceJSON)
	if len([]byte(prompt)) > maxNoticeQuestionPrompt {
		return marketanalysis.NoticeQuestionAnswer{}, errors.New("notice question evidence exceeds the Hermes prompt limit")
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = noticeQuestionPrimaryModel
	}
	runDir, err := marketQuestionRunDir(config, "notice-questions")
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	releasePriority, err := beginInteractiveQuestionPriority(config.OutputDir)
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	defer releasePriority()
	if !strings.EqualFold(persona, "BITHUMB_STYLE_WRITER") {
		return askNoticeQuestionAudited(questionCtx, config, persona, question, evidenceJSON, asOfMS, runDir, hermesInvoker)
	}
	primaryEffort := noticeQuestionPrimaryEffort
	if model == noticeQuestionPrimaryModel {
		config.Temperature = 0
	}
	raw, primaryErr := hermesInvoker(questionCtx, config, prompt, model, primaryEffort, runDir)
	if primaryErr == nil {
		if result, decodeErr := decodeNoticeQuestionResult(raw); decodeErr == nil {
			result = normalizeNoticeQuestionResult(result, persona, question)
			if groundingErr := validateNoticeQuestionResult(result, persona, question, evidenceJSON); groundingErr == nil {
				return noticeQuestionAnswer(result, persona, "HERMES_NOTICE", model, asOfMS), nil
			} else {
				primaryErr = groundingErr
			}
		} else {
			primaryErr = decodeErr
		}
	}
	return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("Hermes Bithumb writer primary attempt failed; use deterministic draft fallback: %w", primaryErr)
}

func validateNoticeQuestionResult(result noticeQuestionModelResult, persona, question string, evidenceJSON []byte) error {
	if err := noticeQuestionEvidenceAvailable(persona, evidenceJSON); err != nil {
		return err
	}
	if err := validateNoticeQuestionResultNumbers(result, question, evidenceJSON); err != nil {
		return err
	}
	var evidence struct {
		Notices []struct {
			SourceID string `json:"source_id"`
		} `json:"notices"`
		StyleExamples           []noticeQuestionStyleExample `json:"style_examples"`
		WriterVerifiedAssets    []string                     `json:"writer_verified_assets"`
		WriterVerifiedSourceIDs []string                     `json:"writer_verified_source_ids"`
		WriterDraftContract     *noticewriter.DraftContract  `json:"writer_draft_contract"`
	}
	if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
		return fmt.Errorf("decode notice source catalog: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(persona), "BITHUMB_STYLE_WRITER") {
		if err := validateBithumbWriterResultWithContract(
			result, question, evidence.WriterVerifiedAssets, evidence.StyleExamples, evidence.WriterDraftContract,
		); err != nil {
			return err
		}
	}
	allowed := make(map[string]struct{}, len(evidence.Notices)+len(evidence.StyleExamples)+len(evidence.WriterVerifiedSourceIDs))
	for _, row := range evidence.Notices {
		allowed[strings.TrimSpace(row.SourceID)] = struct{}{}
	}
	for _, row := range evidence.StyleExamples {
		allowed[strings.TrimSpace(row.SourceID)] = struct{}{}
	}
	for _, sourceID := range evidence.WriterVerifiedSourceIDs {
		allowed[strings.TrimSpace(sourceID)] = struct{}{}
	}
	if len(evidence.Notices) > 0 && len(result.SourceNoticeIDs) == 0 {
		return errors.New("notice answer must cite the retrieved sources used to answer or establish insufficient evidence")
	}
	for _, sourceID := range result.SourceNoticeIDs {
		if _, exists := allowed[strings.TrimSpace(sourceID)]; !exists {
			return fmt.Errorf("notice answer cites a source absent from retrieved evidence: %s", sourceID)
		}
	}
	if strings.EqualFold(strings.TrimSpace(persona), "BITHUMB_STYLE_WRITER") {
		return nil
	}
	return validateNoticeQuestionClaimText(noticeQuestionResultText(result), noticeQuestionClaimSources(noticeQuestionCitedEvidence(result.SourceNoticeIDs, evidenceJSON)))
}

const (
	bithumbWriterDraftMarker     = "[공식 게시 전 검토용 초안]"
	bithumbWriterDraftDisclaimer = "이 문안은 공식 게시 전 검토용 초안이며 실제 빗썸 공지가 아닙니다."
	bithumbWriterReviewWarning   = "공식 게시 전에 반드시 확인"
	bithumbWriterReviewNote      = "※ 이 초안의 자산, 일정, 사유, 정책 및 후속 조치는 공식 게시 전에 반드시 확인해 주세요."
)

func normalizeNoticeQuestionResult(result noticeQuestionModelResult, persona, question string) noticeQuestionModelResult {
	if !strings.EqualFold(strings.TrimSpace(persona), "BITHUMB_STYLE_WRITER") ||
		!noticeWriterDraftRequested(question) {
		return result
	}
	answer := strings.TrimSpace(result.Answer)
	if strings.HasPrefix(answer, bithumbWriterDraftMarker) &&
		strings.Contains(result.Caveat, bithumbWriterDraftDisclaimer) {
		if !strings.Contains(answer, bithumbWriterReviewWarning) {
			answer += "\n\n" + bithumbWriterReviewNote
		}
		result.Answer = answer
	}
	return result
}

func validateBithumbWriterResult(
	result noticeQuestionModelResult,
	question string,
	verifiedAssets []string,
	styleExamples []noticeQuestionStyleExample,
) error {
	return validateBithumbWriterResultWithContract(result, question, verifiedAssets, styleExamples, nil)
}

func validateBithumbWriterResultWithContract(
	result noticeQuestionModelResult,
	question string,
	verifiedAssets []string,
	styleExamples []noticeQuestionStyleExample,
	draftContract *noticewriter.DraftContract,
) error {
	draft := noticeWriterDraftRequested(question)
	answer := strings.TrimSpace(result.Answer)
	caveat := strings.TrimSpace(result.Caveat)
	if !draft {
		if strings.HasPrefix(answer, bithumbWriterDraftMarker) {
			return errors.New("Bithumb style analysis was returned as a publication draft")
		}
		if !strings.Contains(caveat, "보증하지 않습니다") {
			return errors.New("Bithumb style analysis lacks the non-endorsement caveat")
		}
		return nil
	}
	if !strings.HasPrefix(answer, bithumbWriterDraftMarker) {
		return errors.New("Bithumb draft lacks the pre-publication marker")
	}
	if !strings.Contains(answer, "유의사항") {
		return errors.New("Bithumb draft lacks a caution section")
	}
	if !strings.Contains(answer, "[확인 필요]") {
		return errors.New("Bithumb draft promotes every fact without a verification placeholder")
	}
	if !strings.Contains(answer, bithumbWriterReviewWarning) {
		return errors.New("Bithumb draft lacks the final fact-verification warning")
	}
	if !strings.Contains(caveat, bithumbWriterDraftDisclaimer) {
		return errors.New("Bithumb draft caveat does not clearly disclaim official status")
	}
	action := noticewriter.InferAction(question)
	if draftContract != nil {
		if draftContract.Version != noticewriter.ContractVersion || !strings.EqualFold(draftContract.Exchange, "bithumb") ||
			draftContract.Action == "" || (action != "" && draftContract.Action != action) {
			return errors.New("Bithumb draft contract does not match the requested action")
		}
		action = draftContract.Action
	}
	if err := validateBithumbDraftEnvelope(answer, styleExamples); err != nil {
		return err
	}
	if err := validateBithumbDraftActionForAction(answer, action); err != nil {
		return err
	}
	if err := validateBithumbDraftFactPlaceholdersForAction(answer, action); err != nil {
		return err
	}
	if err := validateBithumbDraftVerifiedAssets(answer, result.KeyPoints, caveat, verifiedAssets); err != nil {
		return err
	}
	for _, misleading := range []string{"공식 공지입니다", "게시 완료되었습니다", "공식 게시가 완료"} {
		if strings.Contains(answer, misleading) || strings.Contains(caveat, misleading) {
			return fmt.Errorf("Bithumb draft falsely claims official publication: %s", misleading)
		}
	}
	return nil
}

func validateBithumbDraftEnvelope(answer string, styleExamples []noticeQuestionStyleExample) error {
	salutations := []string{}
	brandLines := []string{}
	closingLines := []string{}
	for _, example := range styleExamples {
		for _, line := range strings.Split(example.OpeningExcerpt, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case noticewriter.IsSalutation("bithumb", line):
				salutations = appendUniqueNoticeQuestionText(salutations, line)
			case noticewriter.IsBrandLine("bithumb", line):
				brandLines = appendUniqueNoticeQuestionText(brandLines, line)
			}
		}
		for _, line := range strings.Split(example.ClosingExcerpt, "\n") {
			line = strings.TrimSpace(line)
			if noticewriter.IsClosingLine("bithumb", line) {
				closingLines = appendUniqueNoticeQuestionText(closingLines, line)
			}
		}
	}
	if len(salutations) == 0 && len(brandLines) == 0 && len(closingLines) == 0 {
		return nil
	}

	titleIndex := -1
	if title := bithumbDraftTitleLine(answer); title != "" {
		titleIndex = strings.Index(answer, title)
	}
	salutationIndex := firstNoticeQuestionTextIndex(answer, salutations)
	brandIndex := firstNoticeQuestionTextIndex(answer, brandLines)
	if len(salutations) > 0 && salutationIndex < 0 {
		return errors.New("Bithumb draft does not match the retrieved opening salutation")
	}
	if len(brandLines) > 0 && brandIndex < 0 {
		return errors.New("Bithumb draft does not match the retrieved Bithumb brand line")
	}
	if titleIndex >= 0 && ((salutationIndex >= 0 && salutationIndex > titleIndex) || (brandIndex >= 0 && brandIndex > titleIndex)) {
		return errors.New("Bithumb draft title appears before the retrieved opening envelope")
	}
	if salutationIndex >= 0 && brandIndex >= 0 && salutationIndex > brandIndex {
		return errors.New("Bithumb draft opening salutation and brand line are out of order")
	}

	closingIndex := firstNoticeQuestionTextIndex(answer, closingLines)
	thanksIndex := strings.LastIndex(answer, "감사합니다.")
	reviewIndex := strings.LastIndex(answer, bithumbWriterReviewWarning)
	if len(closingLines) > 0 && closingIndex < 0 {
		return errors.New("Bithumb draft does not match a retrieved action-specific closing")
	}
	if thanksIndex < 0 {
		return errors.New("Bithumb draft lacks the retrieved final thanks")
	}
	if closingIndex >= 0 && closingIndex > thanksIndex {
		return errors.New("Bithumb draft action-specific closing appears after the final thanks")
	}
	if reviewIndex >= 0 && thanksIndex > reviewIndex {
		return errors.New("Bithumb draft verification note appears before the official-format closing")
	}
	return nil
}

func appendUniqueNoticeQuestionText(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func firstNoticeQuestionTextIndex(text string, options []string) int {
	best := -1
	for _, option := range options {
		if index := strings.Index(text, option); index >= 0 && (best < 0 || index < best) {
			best = index
		}
	}
	return best
}

func validateBithumbDraftAction(answer, question string) error {
	return validateBithumbDraftActionForAction(answer, noticewriter.InferAction(question))
}

func validateBithumbDraftActionForAction(answer string, expected noticewriter.Action) error {
	if expected == "" {
		return nil
	}
	title := strings.ToLower(bithumbDraftTitleLine(answer))
	if title == "" {
		return errors.New("Bithumb draft lacks an action-specific title")
	}
	if noticewriter.InferAction(title) != expected {
		return fmt.Errorf("Bithumb draft title does not match requested action %s: %s", expected, title)
	}
	return nil
}

func bithumbDraftTitleLine(answer string) string {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != bithumbWriterDraftMarker && strings.Contains(line, "안내") && !strings.Contains(line, "안녕하세요") {
			return line
		}
	}
	return ""
}

func validateBithumbDraftVerifiedAssets(answer string, keyPoints []string, caveat string, verifiedAssets []string) error {
	title := bithumbDraftTitleLine(answer)
	target, _ := bithumbDraftFieldValue(answer, "대상 가상자산")
	if target == "" {
		return errors.New("Bithumb draft lacks a target-asset field")
	}

	verified := normalizedBithumbDraftTickerSet(verifiedAssets)
	titleTickers := bithumbDraftTickerSet(title)
	targetTickers := bithumbDraftTickerSet(target)
	allOutput := answer + "\n" + strings.Join(keyPoints, "\n") + "\n" + caveat
	answerTickers := bithumbDraftTickerSet(allOutput)
	if len(verified) == 0 {
		if !strings.Contains(title, "확인 필요") || !strings.Contains(target, "[확인 필요]") || len(answerTickers) > 0 {
			return errors.New("Bithumb draft promotes a ticker without same-action verified evidence")
		}
		return nil
	}
	for ticker := range answerTickers {
		if _, allowed := verified[ticker]; !allowed {
			return fmt.Errorf("Bithumb draft contains an unverified ticker outside the target fields: %s", ticker)
		}
	}
	if !sameBithumbDraftTickerSet(titleTickers, verified) || !sameBithumbDraftTickerSet(targetTickers, verified) {
		return fmt.Errorf("Bithumb draft target tickers do not match same-action verified assets: verified=%v title=%v target=%v",
			sortedBithumbDraftTickers(verified), sortedBithumbDraftTickers(titleTickers), sortedBithumbDraftTickers(targetTickers))
	}
	return nil
}

func normalizedBithumbDraftTickerSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func bithumbDraftTickerSet(value string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, token := range bithumbDraftTickerToken.FindAllString(strings.ToUpper(value), -1) {
		token = strings.Trim(token, ".-_")
		if token != "" {
			result[token] = struct{}{}
		}
	}
	return result
}

func sameBithumbDraftTickerSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for ticker := range left {
		if _, exists := right[ticker]; !exists {
			return false
		}
	}
	return true
}

func sortedBithumbDraftTickers(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateBithumbDraftFactPlaceholders(answer, question string) error {
	return validateBithumbDraftFactPlaceholdersForAction(answer, noticewriter.InferAction(question))
}

func validateBithumbDraftFactPlaceholdersForAction(answer string, action noticewriter.Action) error {
	for _, field := range noticewriter.RequiredPlaceholderFields(action) {
		label := strings.TrimSuffix(field, ": [확인 필요]")
		value, exists := bithumbDraftFieldValue(answer, label)
		if !exists || !strings.Contains(value, "[확인 필요]") {
			return fmt.Errorf("Bithumb draft fact field is not safely unresolved: %s", field)
		}
	}
	return nil
}

func bithumbDraftFieldValue(answer, label string) (string, bool) {
	replacer := strings.NewReplacer("**", "", "__", "", "`", "")
	for _, raw := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(replacer.Replace(raw))
		index := strings.Index(line, label)
		if index < 0 {
			continue
		}
		prefix := strings.TrimSpace(line[:index])
		if prefix != "" && prefix != "-" && prefix != "•" && prefix != "·" {
			continue
		}
		value := strings.TrimSpace(line[index+len(label):])
		if !strings.HasPrefix(value, ":") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(value, ":")), true
	}
	return "", false
}

func noticeWriterDraftRequested(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	for _, phrase := range []string{
		"초안", "작성해", "작성 해", "작성하고", "써줘", "써 줘", "써주세요", "써 주세요", "글을 써", "글 써",
		"만들어", "바꿔", "고쳐", "다듬어", "반영해서", "반영해",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func validateNoticeQuestionResultNumbers(result noticeQuestionModelResult, _ string, evidenceJSON []byte) error {
	allowed := map[string]struct{}{}
	// A number in the user's question is a claim to verify, not official
	// evidence. Query scope such as a lookback period must be represented in
	// the server-built evidence contract before the model may repeat it.
	citedEvidence := noticeQuestionCitedEvidence(result.SourceNoticeIDs, evidenceJSON)
	groundingText := noticeQuestionNumericEvidenceText(citedEvidence)
	groundingText, allowedDates := noticeQuestionCalendarDates(groundingText)
	for _, location := range noticeQuestionNumericToken.FindAllStringIndex(groundingText, -1) {
		token := groundingText[location[0]:location[1]]
		if normalized, ok := normalizeNoticeQuestionNumericToken(token); ok {
			allowed[normalized] = struct{}{}
		}
		// A hyphen inside an ISO date is punctuation, not a negative sign.
		// Models may restate 2026-08-23 as "23일"; allow that component only
		// when the hyphen is immediately preceded by another ASCII digit.
		if strings.HasPrefix(token, "-") && location[0] > 0 &&
			groundingText[location[0]-1] >= '0' && groundingText[location[0]-1] <= '9' {
			if normalized, ok := normalizeNoticeQuestionNumericToken(strings.TrimPrefix(token, "-")); ok {
				allowed[normalized] = struct{}{}
			}
		}
	}
	answerText := noticeQuestionAnswerGroundingText(noticeQuestionResultText(result))
	answerText, answerDates := noticeQuestionCalendarDates(answerText)
	for date := range answerDates {
		if strings.HasPrefix(date, "INVALID:") || !allowedDates[date] {
			return fmt.Errorf("notice answer contains a date or clock absent from cited evidence: %s", date)
		}
	}
	for _, token := range noticeQuestionNumericToken.FindAllString(answerText, -1) {
		normalized, ok := normalizeNoticeQuestionNumericToken(token)
		if !ok {
			return fmt.Errorf("notice answer contains an invalid numeric token %q", token)
		}
		if _, grounded := allowed[normalized]; !grounded {
			return fmt.Errorf("notice answer contains a number absent from server evidence: %s", token)
		}
	}
	return validateNoticeQuestionQuantities(result, citedEvidence)
}

// Metadata describes the server-selected query scope, but facts from notice
// rows and writing examples must belong to the sources the answer actually
// cites. An unrelated candidate cannot license another notice's reward/date.
func noticeQuestionCitedEvidence(sourceIDs []string, evidenceJSON []byte) []byte {
	var root map[string]json.RawMessage
	if json.Unmarshal(evidenceJSON, &root) != nil {
		return nil
	}
	cited := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		cited[strings.TrimSpace(id)] = true
	}
	for _, key := range []string{"notices", "style_examples"} {
		var rows []json.RawMessage
		if json.Unmarshal(root[key], &rows) != nil {
			continue
		}
		selected := make([]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			var identity struct {
				SourceID string `json:"source_id"`
			}
			if json.Unmarshal(row, &identity) == nil && cited[strings.TrimSpace(identity.SourceID)] {
				if key == "notices" {
					row = noticeQuestionOfficialRow(row)
				}
				selected = append(selected, row)
			}
		}
		root[key], _ = json.Marshal(selected)
	}
	payload, _ := json.Marshal(root)
	return payload
}

func noticeQuestionNumericEvidenceText(evidenceJSON []byte) string {
	decoder := json.NewDecoder(strings.NewReader(string(evidenceJSON)))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return ""
	}
	var result strings.Builder
	var appendValue func(string, any)
	appendValue = func(key string, value any) {
		key = strings.ToLower(strings.TrimSpace(key))
		if noticeQuestionNumericIdentityField(key) {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for childKey, child := range typed {
				appendValue(childKey, child)
			}
		case []any:
			for _, child := range typed {
				appendValue(key, child)
			}
		case string:
			result.WriteString(noticeQuestionGroundingText(typed))
			// Keep fields/passages separate: their trailing and leading digits
			// must not form a date or clock simply because the JSON was flattened.
			result.WriteString("\n|\n")
		case json.Number:
			result.WriteString(string(typed))
			result.WriteString("\n|\n")
		}
	}
	appendValue("", root)
	return result.String()
}

var (
	noticeGroundingReferenceDefinition = regexp.MustCompile(`(?m)^[\t ]{0,3}\[[^\]\n]+\]:[^\n]*`)
	noticeGroundingReferenceLink       = regexp.MustCompile(`!?\[([^\]\n]*)\]\[[^\]\n]*\]`)
	noticeGroundingURL                 = regexp.MustCompile("(?i)(?:https?://|www\\.)[^\\s<>()\\[\\]\\\"'`]+")
	noticeGroundingHTMLTag             = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(?:[\t ]+[^<>\n]*)?[\t ]*/?>`)
	noticeGroundingMarkup              = []*regexp.Regexp{
		regexp.MustCompile(`(?s)\*\*(.*?)\*\*`),
		regexp.MustCompile(`(?s)__(.*?)__`),
		regexp.MustCompile(`(?s)\*([^*]+)\*`),
		regexp.MustCompile(`(?s)_([^_]+)_`),
		regexp.MustCompile("(?s)`([^`]+)`"),
		regexp.MustCompile(`(?s)~~(.*?)~~`),
	}
)

func noticeQuestionResultText(result noticeQuestionModelResult) string {
	return result.Answer + "\n|\n" + strings.Join(result.KeyPoints, "\n|\n") + "\n|\n" + result.Caveat
}

// The notice UI displays the answer as text. Angle-bracket content, including
// tag-shaped text, must not hide a numeric claim. Official HTML attributes are
// still excluded when extracting evidence, independently of answer display.
func noticeQuestionAnswerGroundingText(text string) string {
	text = noticeQuestionGroundingText(strings.ReplaceAll(text, "<", "〈"))
	return noticeQuestionAnswerEnumerationText(text)
}

var noticeAnswerEnumeration = regexp.MustCompile(`(?m)^[\t ]*(?:#{1,6}[\t ]+)?([0-9]{1,9})[.)][\t ]+`)

// Consecutive Markdown section/list labels are presentation, not evidence
// quantities. Keep isolated numeric prefixes and all numbers inside the text.
func noticeQuestionAnswerEnumerationText(text string) string {
	matches := noticeAnswerEnumeration.FindAllStringSubmatchIndex(text, -1)
	remove := map[int]bool{}
	for start := 0; start < len(matches); {
		end := start
		for end < len(matches) && text[matches[end][2]:matches[end][3]] == fmt.Sprint(end-start+1) {
			end++
		}
		if end-start >= 2 {
			for i := start; i < end; i++ {
				remove[i] = true
			}
		}
		if end == start {
			end++
		}
		start = end
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if remove[i] {
			text = text[:matches[i][2]] + text[matches[i][1]:]
		}
	}
	return text
}

// Extract the displayed prose used for numeric grounding. Link labels remain
// evidence, but destinations, reference definitions and HTML attributes do not.
// Apply the same representation to the answer so supported Markdown cannot
// split a calendar date into otherwise reusable numeric components.
func noticeQuestionGroundingText(text string) string {
	text = noticeGroundingReferenceDefinition.ReplaceAllString(text, "")
	text = noticeQuestionInlineLinkLabels(text)
	text = noticeGroundingReferenceLink.ReplaceAllString(text, "$1")
	text = noticeGroundingHTMLTag.ReplaceAllString(text, " ")
	text = noticeGroundingURL.ReplaceAllString(text, "")
	for _, markup := range noticeGroundingMarkup {
		text = markup.ReplaceAllString(text, "$1")
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) && r != '\n' && r != '\r' {
			return ' '
		}
		if r >= '0' && r <= '9' {
			return r
		}
		// Every Unicode Nd range consists of ordered ten-digit sets. Some
		// adjacent styles share one range, hence modulo ten after the stride.
		if unicode.IsDigit(r) {
			if r <= 0xffff {
				for _, entry := range unicode.Digit.R16 {
					if uint16(r) >= entry.Lo && uint16(r) <= entry.Hi && (uint16(r)-entry.Lo)%entry.Stride == 0 {
						return '0' + rune(((uint16(r)-entry.Lo)/entry.Stride)%10)
					}
				}
			} else {
				for _, entry := range unicode.Digit.R32 {
					if uint32(r) >= entry.Lo && uint32(r) <= entry.Hi && (uint32(r)-entry.Lo)%entry.Stride == 0 {
						return '0' + rune(((uint32(r)-entry.Lo)/entry.Stride)%10)
					}
				}
			}
		}
		switch r {
		case '\u2212', '\ufe63', '\uff0d':
			return '-'
		case '\ufe62', '\uff0b':
			return '+'
		case '\uff0e', '\u066b':
			return '.'
		case '\uff0c', '\u066c':
			return ','
		case '\uff0f':
			return '/'
		case '\uff1a':
			return ':'
		default:
			return r
		}
	}, text)
}

func noticeQuestionInlineLinkLabels(text string) string {
	var result strings.Builder
	for cursor := 0; cursor < len(text); {
		start := strings.IndexByte(text[cursor:], '[')
		if start < 0 {
			result.WriteString(text[cursor:])
			break
		}
		start += cursor
		labelDepth, end := 1, start+1
		for ; end < len(text); end++ {
			if text[end] == '\\' && end+1 < len(text) {
				end++
				continue
			}
			if text[end] == '[' {
				labelDepth++
			} else if text[end] == ']' {
				labelDepth--
				if labelDepth == 0 {
					break
				}
			}
		}
		if labelDepth != 0 || end+1 >= len(text) || text[end+1] != '(' {
			result.WriteString(text[cursor : start+1])
			cursor = start + 1
			continue
		}
		depth, closing := 1, end+2
		for ; closing < len(text); closing++ {
			if text[closing] == '\\' && closing+1 < len(text) {
				closing++
				continue
			}
			if text[closing] == '(' {
				depth++
			} else if text[closing] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if depth != 0 {
			result.WriteString(text[cursor:])
			break
		}
		result.WriteString(text[cursor:start])
		result.WriteString(text[start+1 : end])
		cursor = closing + 1
	}
	return result.String()
}

func noticeQuestionNumericIdentityField(key string) bool {
	if strings.HasSuffix(key, "_ts_ms") {
		return true
	}
	switch key {
	case "source_id", "source_notice_ids", "notice_id", "url", "image_url",
		"conversation_context_source_ids", "writer_verified_source_ids",
		"contract", "version", "strategy", "as_of_ms":
		return true
	default:
		return false
	}
}

func normalizeNoticeQuestionNumericToken(token string) (string, bool) {
	// Compare decimal text exactly; float64 would merge distinct integers above
	// 2^53 and can also erase differences in small fractional amounts.
	value := strings.ReplaceAll(strings.TrimPrefix(token, "+"), ",", "")
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return "", false
			}
		}
	}
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	if len(parts) == 2 {
		if fraction := strings.TrimRight(parts[1], "0"); fraction != "" {
			whole += "." + fraction
		}
	}
	if negative && whole != "0" {
		whole = "-" + whole
	}
	return whole, true
}

func marketQuestionRunDir(config MarketQuestionConfig, child string) (string, error) {
	root := strings.TrimSpace(config.OutputDir)
	if root == "" {
		root = ".tmp/hermes-notices"
	}
	runDir := root + "/" + child
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return "", fmt.Errorf("create Hermes question output directory: %w", err)
	}
	return filepath.Abs(runDir)
}

func noticeQuestionPrompt(persona, question string, evidenceJSON []byte) string {
	personaJSON, _ := json.Marshal(persona)
	questionJSON, _ := json.Marshal(question)
	template := noticeQuestionPromptTemplate
	if strings.EqualFold(strings.TrimSpace(persona), "BITHUMB_STYLE_WRITER") {
		template = noticeQuestionWriterPromptTemplate
	}
	prompt := strings.ReplaceAll(template, "{{PERSONA_JSON}}", string(personaJSON))
	prompt = strings.ReplaceAll(prompt, "{{USER_QUESTION_JSON}}", string(questionJSON))
	prompt = strings.ReplaceAll(prompt, "{{SERVER_EVIDENCE_JSON}}", string(evidenceJSON))
	return strings.TrimSpace(prompt)
}

func decodeNoticeQuestionResult(raw []byte) (noticeQuestionModelResult, error) {
	normalized, _ := normalizeHermesUTF8(raw)
	text := strings.TrimSpace(string(normalized))
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	if !strings.HasPrefix(text, "{") {
		return noticeQuestionModelResult{}, errors.New("Hermes notice question did not return JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var result noticeQuestionModelResult
	if err := decoder.Decode(&result); err != nil {
		return noticeQuestionModelResult{}, fmt.Errorf("decode Hermes notice question: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return noticeQuestionModelResult{}, fmt.Errorf("decode Hermes notice question: %w", err)
	}
	result.Answer = strings.TrimSpace(result.Answer)
	result.Caveat = strings.TrimSpace(result.Caveat)
	if len(result.SourceNoticeIDs) > maxNoticeQuestionSources || len(result.KeyPoints) > 5 {
		return noticeQuestionModelResult{}, errors.New("notice answer exceeds ten sources or five key points")
	}
	result.KeyPoints = normalizedLimitedStrings(result.KeyPoints, maxMarketQuestionPoints)
	result.SourceNoticeIDs = normalizedLimitedStrings(result.SourceNoticeIDs, maxNoticeQuestionSources)
	if result.Answer == "" {
		return noticeQuestionModelResult{}, errors.New("Hermes notice question answer is empty")
	}
	return result, nil
}

func normalizedLimitedStrings(values []string, limit int) []string {
	result := make([]string, 0, min(len(values), limit))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == limit {
			break
		}
	}
	return result
}

func noticeQuestionAnswer(result noticeQuestionModelResult, persona, source, model string, asOfMS int64) marketanalysis.NoticeQuestionAnswer {
	return marketanalysis.NoticeQuestionAnswer{
		Answer: result.Answer, KeyPoints: result.KeyPoints, Caveat: result.Caveat,
		SourceNoticeIDs: result.SourceNoticeIDs, Persona: persona,
		Source: source, Model: model, AsOfMS: asOfMS, Sources: []marketanalysis.NoticeQuestionSource{},
	}
}
