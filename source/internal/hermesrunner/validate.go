package hermesrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"market-share-go/internal/notice"
)

var (
	numberPattern = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
	urlPattern    = regexp.MustCompile(`https?://\S+`)
	imageLink     = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	textLink      = regexp.MustCompile(`\[([^\]]+)\]\(https?://[^)]*\)`)
	koreanPattern = regexp.MustCompile(`[가-힣]`)
	assetSymbol   = regexp.MustCompile(`^[A-Za-z0-9]*[A-Za-z][A-Za-z0-9]{0,19}$`)
)

var monthNames = [][]string{
	nil,
	{"january", "jan"}, {"february", "feb"}, {"march", "mar"},
	{"april", "apr"}, {"may"}, {"june", "jun"}, {"july", "jul"},
	{"august", "aug"}, {"september", "sep", "sept"},
	{"october", "oct"}, {"november", "nov"}, {"december", "dec"},
}

func validateResult(result analysisResult, evidence materializedEvidence) error {
	if strings.TrimSpace(result.Summary) == "" {
		return errors.New("Hermes detail result has no summary")
	}
	if summaryNonEmptyLineCount(result.Summary) > 5 {
		return errors.New("Hermes detail summary exceeds five non-empty lines")
	}
	expectedLanguage, expectedTranslation := "ko", "NOT_REQUIRED"
	if isForeignExchange(evidence.Item.Exchange) {
		expectedLanguage, expectedTranslation = "en", "TRANSLATED"
	}
	if !strings.EqualFold(strings.TrimSpace(result.OriginalLanguage), expectedLanguage) {
		return fmt.Errorf("original_language must be %s for %s", expectedLanguage, evidence.Item.Exchange)
	}
	if !strings.EqualFold(strings.TrimSpace(result.TranslationState), expectedTranslation) {
		return fmt.Errorf("translation_status must be %s for %s", expectedTranslation, evidence.Item.Exchange)
	}
	importance := strings.ToUpper(strings.TrimSpace(result.Importance))
	if importance != "NORMAL" && importance != "IMPORTANT" {
		return errors.New("importance must be NORMAL or IMPORTANT")
	}
	if strings.TrimSpace(result.Category) == "" {
		return errors.New("category is required")
	}
	if math.IsNaN(result.Confidence) || math.IsInf(result.Confidence, 0) || result.Confidence < 0 || result.Confidence > 1 {
		return errors.New("analysis_confidence must be between 0 and 1")
	}
	passages, passageErr := workerOfficialPassages(evidence)
	if passageErr != nil {
		return passageErr
	}
	official := strings.Join(append([]string{evidence.Title}, passages...), "\n")
	if notice.IsDomesticExchange(evidence.Item.Exchange) {
		if err := validateDomesticSummaryGrounding(result.Summary, official); err != nil {
			return err
		}
	} else if err := ensureNumbersGrounded("summary", result.Summary, official); err != nil {
		return err
	}
	if err := validateAssets([]string(result.Assets), official); err != nil {
		return err
	}
	if notice.IsDomesticExchange(evidence.Item.Exchange) {
		if err := validateWorkerImageSummaryEvidence(result.Summary, evidence); err != nil {
			return err
		}
		if err := validateNoticeQuestionClaimText(result.Summary, []noticeQuestionClaimSource{{
			Title: evidence.Title, Exchange: evidence.Item.Exchange,
			Assets: []string(result.Assets), Passages: passages,
		}}); err != nil {
			return fmt.Errorf("summary: %w", err)
		}
	}
	actions, err := notice.NormalizeActions(result.Actions)
	if err != nil {
		return err
	}
	for index, action := range actions {
		if !containsNormalizedEvidence(official, action.Evidence) &&
			!containsNormalizedCompositeEvidence(official, action.Evidence) {
			return fmt.Errorf("actions[%d].evidence must be a verbatim official title/body excerpt", index)
		}
	}
	if isForeignExchange(evidence.Item.Exchange) {
		if strings.TrimSpace(result.TranslatedTitle) == "" || strings.TrimSpace(result.TranslatedBody) == "" {
			return errors.New("foreign notice requires Korean title and full-body translation")
		}
		if !koreanPattern.MatchString(result.TranslatedTitle + result.TranslatedBody) {
			return errors.New("foreign notice translation has no Korean text")
		}
		if err := ensureNumbersGrounded("translated_title_ko", result.TranslatedTitle, evidence.Title); err != nil {
			return err
		}
		if err := ensureNumbersGrounded("translated_body_text_ko", result.TranslatedBody, evidence.BodyText); err != nil {
			return err
		}
		if missing := tokenDifference(numericTokens(evidence.BodyText), numericTokens(result.TranslatedBody)); len(missing) > 0 {
			return fmt.Errorf("foreign full-body translation omitted official numbers: %s", strings.Join(missing[:min(8, len(missing))], ", "))
		}
	} else if strings.TrimSpace(result.TranslatedTitle+result.TranslatedBody) != "" {
		return errors.New("Korean notice must not return translated title or body")
	}
	for index, schedule := range result.Schedules {
		if strings.TrimSpace(schedule.Kind) == "" || strings.TrimSpace(schedule.Evidence) == "" {
			return fmt.Errorf("schedule[%d] requires kind and evidence", index)
		}
		if !containsNormalizedEvidence(official, schedule.Evidence) &&
			!containsNormalizedCompositeEvidence(official, schedule.Evidence) {
			return fmt.Errorf("schedule[%d].evidence must be a verbatim official title/body excerpt", index)
		}
		if err := ensureNumbersGrounded(fmt.Sprintf("schedule[%d].evidence", index), schedule.Evidence, official); err != nil {
			return err
		}
		// Re-bind this individual row to the complete official source. An audit
		// marker alone cannot grant an exception to another model schedule.
		bound := workerScheduleMatchesBinding(schedule, result, evidence)
		if !bound {
			if err := validateScheduleTimestamp(fmt.Sprintf("schedule[%d].start_ts_ms", index), schedule.StartTSMS, schedule); err != nil {
				return err
			}
		}
		if !bound {
			if err := validateScheduleTimestamp(fmt.Sprintf("schedule[%d].end_ts_ms", index), schedule.EndTSMS, schedule); err != nil {
				return err
			}
		}
	}
	if !sameOrderedStrings([]string(result.ImageURLs), evidence.ImageURLs) {
		return errors.New("image_urls must exactly match the official image URL order")
	}
	if err := validateImageCoverage(result.ImageAnalysis, evidence); err != nil {
		return err
	}
	expectedImageText := expectedAggregatedImageText(result.ImageAnalysis, evidence)
	if strings.TrimSpace(result.ImageText) != expectedImageText {
		return errors.New("image_text must exactly aggregate analyzed image rows in official URL order")
	}
	return nil
}

// A summary is derived text, never new authority for a recombined date or
// changed unit. Keep the same tuple checks used at the question boundary.
func validateDomesticSummaryGrounding(summary, official string) error {
	claimText, claimedDates := noticeQuestionCalendarDates(summary)
	officialText, officialDates := noticeQuestionCalendarDates(official)
	if unsupported := tokenDifference(claimedDates, officialDates); len(unsupported) > 0 {
		return fmt.Errorf("summary contains calendar/time values absent from official text: %s", strings.Join(unsupported[:min(8, len(unsupported))], ", "))
	}
	// Compare ordinary numbers after the same date/clock normalization on both
	// sides. Dotted dates are not decimals, and 15시 equals 15:00; tuple identity
	// above still rejects dates or clocks assembled from different official facts.
	if err := ensureNumbersGrounded("summary", claimText, officialText); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"official_body": official})
	if err != nil {
		return err
	}
	if err := validateNoticeQuestionQuantities(noticeQuestionModelResult{Answer: summary}, payload); err != nil {
		return fmt.Errorf("summary: %w", err)
	}
	return nil
}

func summaryNonEmptyLineCount(value string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func sameOrderedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}

func validateAnalyzedItem(item notice.Item, evidence notice.HermesEvidence) error {
	return validateResult(analysisResult{
		OriginalLanguage: item.OriginalLanguage,
		TranslatedTitle:  item.TranslatedTitleKO, TranslatedBody: item.TranslatedBodyKO,
		TranslationState: item.TranslationStatus, Summary: item.Summary,
		Category: item.Category, Importance: item.Importance, Assets: flexibleStrings(item.Assets),
		Actions:   item.Actions,
		Schedules: item.Schedules, RiskTags: flexibleStrings(item.RiskTags), ImageURLs: flexibleStrings(item.ImageURLs),
		ImageText: item.ImageText, ImageAnalysis: item.ImageAnalysis, Confidence: item.AnalysisConfidence,
	}, materializedEvidence{HermesEvidence: evidence})
}

func normalizeAnalyzedItem(
	evidence materializedEvidence,
	decision analysisDecision,
	hash string,
	checkedMS int64,
) notice.Item {
	result := decision.Result
	foreign := isForeignExchange(evidence.Item.Exchange)
	originalTitle := strings.TrimSpace(evidence.Title)
	title := originalTitle
	translationStatus := "NOT_REQUIRED"
	if foreign {
		title = strings.TrimSpace(result.TranslatedTitle)
		translationStatus = "TRANSLATED"
	}
	importance := strings.ToUpper(strings.TrimSpace(result.Importance))
	if importance != "IMPORTANT" {
		importance = "NORMAL"
	}
	bodyForDisplay := evidence.BodyText
	if foreign {
		bodyForDisplay = result.TranslatedBody
	}
	item := evidence.Item
	item.Title = title
	item.Category = strings.ToUpper(strings.TrimSpace(result.Category))
	if item.Category == "" {
		item.Category = "OTHER"
	}
	item.Assets = normalizedAnalyzedAssets([]string(result.Assets))
	item.Actions, _ = notice.NormalizeActions(result.Actions)
	item.SourceType = "HERMES"
	item.BodyExcerpt = truncateRunes(strings.TrimSpace(bodyForDisplay), 1500)
	item.Summary = strings.TrimSpace(result.Summary)
	item.Schedules = normalizeSchedules(result.Schedules)
	item.RiskTags = normalizedStrings([]string(result.RiskTags))
	item.ImageURLs = append([]string(nil), evidence.ImageURLs...)
	item.ImageText = strings.TrimSpace(result.ImageText)
	item.ImageAnalysis = normalizeImageAnalysis(result.ImageAnalysis, evidence)
	item.ContentSource, item.AnalysisStatus = "HERMES_AI", "ANALYZED"
	item.AnalysisConfidence = clampConfidence(result.Confidence)
	item.ContentHash, item.DetailCheckedTSMS = hash, checkedMS
	item.OriginalLanguage = "ko"
	if foreign {
		item.OriginalLanguage = "en"
	}
	item.OriginalTitle, item.OriginalBodyText = originalTitle, strings.TrimSpace(evidence.BodyText)
	item.TranslatedTitleKO, item.TranslatedBodyKO = "", ""
	if foreign {
		item.TranslatedTitleKO, item.TranslatedBodyKO = strings.TrimSpace(result.TranslatedTitle), strings.TrimSpace(result.TranslatedBody)
	}
	item.TranslationStatus, item.Importance = translationStatus, importance
	item.AnalysisProvider, item.AnalysisModel = decision.Provider, decision.Model
	item.ReasoningEffort, item.EscalationReason = decision.Reasoning, decision.EscalationReason
	if result.scheduleNormalization != "" {
		item.EscalationReason = strings.Trim(strings.Join([]string{item.EscalationReason, result.scheduleNormalization}, "+"), "+")
	}
	item.AnalysisPolicy = analysisPolicyVersion
	return item
}

func normalizeImageAnalysis(rows []map[string]any, evidence materializedEvidence) []map[string]any {
	localToURL := map[string]string{}
	bytesByURL := map[string]int64{}
	hashByURL := map[string]string{}
	for _, local := range evidence.LocalImages {
		if local.LocalPath != "" {
			localToURL[local.LocalPath] = local.URL
		}
		if local.Bytes > 0 {
			bytesByURL[local.URL] = local.Bytes
		}
		if local.SHA256 != "" {
			hashByURL[local.URL] = local.SHA256
		}
	}
	official := map[string]bool{}
	for _, imageURL := range evidence.ImageURLs {
		official[imageURL] = true
	}
	byURL := map[string]map[string]any{}
	for _, row := range rows {
		url := strings.TrimSpace(mapString(row, "url"))
		if mapped := localToURL[url]; mapped != "" {
			url = mapped
		}
		if !official[url] || byURL[url] != nil {
			continue
		}
		copyRow := map[string]any{
			"url": url, "status": strings.ToUpper(defaultString(mapString(row, "status"), "PENDING")),
			"text":               strings.TrimSpace(mapString(row, "text")),
			"translated_text_ko": strings.TrimSpace(mapString(row, "translated_text_ko")),
			"semantic_type":      strings.ToUpper(defaultString(mapString(row, "semantic_type"), "UNKNOWN")),
			"signals":            normalizedStrings(mapStrings(row["signals"])),
			"interpretation":     strings.TrimSpace(mapString(row, "interpretation")),
			"bytes":              max(mapInt64(row["bytes"]), bytesByURL[url]),
		}
		engine := strings.TrimSpace(mapString(row, "ocr_engine"))
		if engine == "" && strings.EqualFold(mapString(row, "status"), "ANALYZED") {
			engine = "hermes_remote_vision"
		}
		if engine != "" {
			copyRow["ocr_engine"] = engine
		}
		if confidence, ok := exactUnitFloat64(row["ocr_confidence"]); ok {
			copyRow["ocr_confidence"] = confidence
		}
		if regions, ok := normalizedTextRegions(row["text_regions"]); ok {
			copyRow["text_regions"] = regions
		}
		if reason := strings.TrimSpace(mapString(row, "failure_reason")); reason != "" {
			copyRow["failure_reason"] = truncateRunes(reason, 300)
		}
		if hashByURL[url] != "" {
			copyRow["content_sha256"] = hashByURL[url]
		}
		byURL[url] = copyRow
	}
	result := make([]map[string]any, 0, len(evidence.ImageURLs))
	for _, imageURL := range evidence.ImageURLs {
		row := byURL[imageURL]
		if row == nil {
			row = map[string]any{
				"url": imageURL, "status": "PENDING", "text": "", "translated_text_ko": "",
				"semantic_type": "UNKNOWN", "signals": []string{}, "interpretation": "", "bytes": bytesByURL[imageURL],
			}
		}
		result = append(result, row)
	}
	return result
}

func validateImageCoverage(rows []map[string]any, evidence materializedEvidence) error {
	localByURL := map[string]localImage{}
	for _, local := range evidence.LocalImages {
		localByURL[local.URL] = local
	}
	if len(rows) != len(evidence.ImageURLs) {
		return fmt.Errorf("image_analysis row count=%d, want %d official images", len(rows), len(evidence.ImageURLs))
	}
	for index, row := range rows {
		if err := validateImageAnalysisRowShape(row, index); err != nil {
			return err
		}
		url := strings.TrimSpace(mapString(row, "url"))
		imageURL := strings.TrimSpace(evidence.ImageURLs[index])
		if url != imageURL {
			return fmt.Errorf("image_analysis[%d].url must match official image order: got %s want %s", index, url, imageURL)
		}
		status := strings.ToUpper(strings.TrimSpace(mapString(row, "status")))
		switch status {
		case "ANALYZED", "DOWNLOAD_FAILED", "VISION_FAILED", "NO_TEXT", "UNSUPPORTED":
		default:
			return fmt.Errorf("image_analysis has unsupported status %q for official image: %s", status, imageURL)
		}
		local := localByURL[imageURL]
		if local.LocalPath != "" && status != "ANALYZED" && status != "VISION_FAILED" && status != "NO_TEXT" {
			return fmt.Errorf("downloaded official image has invalid status %s: %s", status, imageURL)
		}
		if local.LocalPath == "" {
			expectedStatus := "DOWNLOAD_FAILED"
			if strings.Contains(strings.ToLower(local.Error), "unsupported image signature") {
				expectedStatus = "UNSUPPORTED"
			}
			if status != expectedStatus {
				return fmt.Errorf("unavailable official image status=%s, want %s: %s", status, expectedStatus, imageURL)
			}
		}
		rowBytes, validBytes := exactNonnegativeInt64(row["bytes"])
		if !validBytes || rowBytes != local.Bytes {
			return fmt.Errorf("image_analysis bytes do not match downloaded evidence: %s", imageURL)
		}
		if digest := strings.TrimSpace(mapString(row, "content_sha256")); digest != "" &&
			!strings.EqualFold(digest, local.SHA256) {
			return fmt.Errorf("image_analysis content_sha256 does not match downloaded evidence: %s", imageURL)
		}
		semanticType := strings.ToUpper(strings.TrimSpace(mapString(row, "semantic_type")))
		hasMeaning := strings.TrimSpace(mapString(row, "text")+mapString(row, "translated_text_ko")+
			mapString(row, "interpretation")) != "" || len(mapStrings(row["signals"])) > 0 ||
			(semanticType != "" && semanticType != "UNKNOWN")
		if status == "ANALYZED" && !hasMeaning {
			return fmt.Errorf("image_analysis has no usable result for official image: %s", imageURL)
		}
		if status != "ANALYZED" && hasMeaning {
			return fmt.Errorf("failed image_analysis row contains unsupported extracted evidence: %s", imageURL)
		}
		if status == "ANALYZED" && !isForeignExchange(evidence.Item.Exchange) &&
			strings.TrimSpace(mapString(row, "translated_text_ko")) != "" {
			return fmt.Errorf("Korean analyzed image must not contain a translated copy: %s", imageURL)
		}
		if status == "ANALYZED" {
			transcript := mapString(row, "text")
			derived := []struct {
				field string
				value string
			}{
				{field: "translated_text_ko", value: mapString(row, "translated_text_ko")},
				{field: "interpretation", value: mapString(row, "interpretation")},
				{field: "signals", value: strings.Join(mapStrings(row["signals"]), " ")},
			}
			for _, field := range derived {
				if err := ensureNumbersGrounded(fmt.Sprintf("image_analysis[%d].%s", index, field.field), field.value, transcript); err != nil {
					return err
				}
			}
		}
		if status == "ANALYZED" && isForeignExchange(evidence.Item.Exchange) &&
			strings.TrimSpace(mapString(row, "text")) != "" &&
			!koreanPattern.MatchString(mapString(row, "translated_text_ko")) {
			return fmt.Errorf("foreign analyzed image text requires Korean translation: %s", imageURL)
		}
	}
	return nil
}

func validateImageAnalysisRowShape(row map[string]any, index int) error {
	required := []string{"url", "status", "text", "translated_text_ko", "semantic_type", "signals", "interpretation", "bytes"}
	for _, field := range required {
		if _, exists := row[field]; !exists {
			return fmt.Errorf("image_analysis[%d] is missing %s", index, field)
		}
	}
	for field := range row {
		switch field {
		case "url", "status", "text", "translated_text_ko", "semantic_type", "signals", "interpretation", "bytes",
			"ocr_engine", "ocr_confidence", "text_regions", "failure_reason", "content_sha256":
		default:
			return fmt.Errorf("image_analysis[%d] contains unknown field %s", index, field)
		}
	}
	for _, field := range []string{"url", "status", "text", "translated_text_ko", "semantic_type", "interpretation"} {
		if _, ok := row[field].(string); !ok {
			return fmt.Errorf("image_analysis[%d].%s must be a string", index, field)
		}
	}
	if !isStringList(row["signals"]) {
		return fmt.Errorf("image_analysis[%d].signals must be a string array", index)
	}
	for _, field := range []string{"ocr_engine", "failure_reason", "content_sha256"} {
		if value, exists := row[field]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("image_analysis[%d].%s must be a string", index, field)
			}
		}
	}
	if value, exists := row["ocr_confidence"]; exists {
		if _, ok := exactUnitFloat64(value); !ok {
			return fmt.Errorf("image_analysis[%d].ocr_confidence must be between 0 and 1", index)
		}
	}
	if value, exists := row["text_regions"]; exists {
		if _, ok := normalizedTextRegions(value); !ok {
			return fmt.Errorf("image_analysis[%d].text_regions is invalid", index)
		}
	}
	return nil
}

func exactUnitFloat64(value any) (float64, bool) {
	var result float64
	switch typed := value.(type) {
	case float64:
		result = typed
	case float32:
		result = float64(typed)
	case int:
		result = float64(typed)
	case int64:
		result = float64(typed)
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return 0, false
		}
		result = parsed
	default:
		return 0, false
	}
	return result, !math.IsNaN(result) && !math.IsInf(result, 0) && result >= 0 && result <= 1
}

func normalizedTextRegions(value any) ([]map[string]any, bool) {
	raw, ok := value.([]map[string]any)
	if !ok {
		values, valuesOK := value.([]any)
		if !valuesOK {
			return nil, false
		}
		raw = make([]map[string]any, 0, len(values))
		for _, value := range values {
			region, regionOK := value.(map[string]any)
			if !regionOK {
				return nil, false
			}
			raw = append(raw, region)
		}
	}
	if len(raw) > 256 {
		return nil, false
	}
	result := make([]map[string]any, 0, len(raw))
	for _, region := range raw {
		text := strings.TrimSpace(mapString(region, "text"))
		confidence, confidenceOK := exactUnitFloat64(region["confidence"])
		x, xOK := exactUnitFloat64(region["x"])
		y, yOK := exactUnitFloat64(region["y"])
		width, widthOK := exactUnitFloat64(region["width"])
		height, heightOK := exactUnitFloat64(region["height"])
		if text == "" || !confidenceOK || !xOK || !yOK || !widthOK || !heightOK ||
			x+width > 1.0001 || y+height > 1.0001 {
			return nil, false
		}
		result = append(result, map[string]any{
			"text": truncateRunes(text, 2_000), "confidence": confidence,
			"x": x, "y": y, "width": width, "height": height,
		})
	}
	return result, true
}

func isStringList(value any) bool {
	switch values := value.(type) {
	case []string:
		return true
	case []any:
		for _, value := range values {
			if _, ok := value.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func exactNonnegativeInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil && parsed >= 0
	case float64:
		if typed < 0 || math.Trunc(typed) != typed || typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case int:
		return int64(typed), typed >= 0
	case int64:
		return typed, typed >= 0
	default:
		return 0, false
	}
}

func expectedAggregatedImageText(rows []map[string]any, evidence materializedEvidence) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(mapString(row, "status")), "ANALYZED") {
			continue
		}
		value := strings.TrimSpace(mapString(row, "text"))
		if isForeignExchange(evidence.Item.Exchange) {
			value = strings.TrimSpace(mapString(row, "translated_text_ko"))
		}
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n\n")
}

func validateAssets(assets []string, official string) error {
	normalized := normalizedAnalyzedAssets(assets)
	if len(normalizedStrings(assets)) > 0 && len(normalized) == 0 {
		return errors.New("assets must contain explicit ticker symbols, not display names without symbols")
	}
	for _, asset := range normalized {
		if !containsExplicitAsset(official, asset) {
			return fmt.Errorf("asset %s is absent from official text", asset)
		}
	}
	return nil
}

func prepareAnalysisResultForValidation(
	result analysisResult,
	evidence materializedEvidence,
) (analysisResult, bool, error) {
	if hasLocalImageAnalysis(evidence) {
		result.ImageURLs = flexibleStrings(append([]string(nil), evidence.ImageURLs...))
		result.ImageAnalysis = normalizeImageAnalysis(evidence.LocalImageAnalysis, evidence)
		result.ImageText = expectedAggregatedImageText(result.ImageAnalysis, evidence)
	}
	passages, passageErr := workerOfficialPassages(evidence)
	if passageErr != nil {
		return analysisResult{}, false, passageErr
	}
	official := strings.Join(append([]string{evidence.Title}, passages...), "\n")
	kept := make([]string, 0, len(result.Assets))
	dropped := false
	for _, raw := range result.Assets {
		extracted := normalizedAnalyzedAssets([]string{raw})
		if strings.TrimSpace(raw) != "" && len(extracted) == 0 {
			dropped = true
		}
		for _, asset := range extracted {
			if containsExplicitAsset(official, asset) {
				kept = append(kept, asset)
			} else {
				dropped = true
			}
		}
	}
	result.Assets = flexibleStrings(normalizedStrings(kept))
	actions, err := notice.NormalizeActions(result.Actions)
	if err != nil {
		return analysisResult{}, dropped, err
	}
	result.Actions = actions
	result, err = normalizeMissingWorkerScheduleTimes(result, evidence)
	if err != nil {
		return analysisResult{}, dropped, err
	}
	return result, dropped, validateResult(result, evidence)
}

func normalizedAnalyzedAssets(values []string) []string {
	result := []string{}
	for _, value := range normalizedStrings(values) {
		extracted := notice.TitleAssets(value)
		if len(extracted) == 0 && assetSymbol.MatchString(value) {
			extracted = notice.TitleAssets("(" + value + ")")
		}
		result = append(result, extracted...)
	}
	return normalizedStrings(result)
}

func containsExplicitAsset(official, asset string) bool {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if asset == "" {
		return false
	}
	if strings.IndexFunc(asset, func(character rune) bool { return character > 127 }) >= 0 {
		return strings.Contains(strings.ToUpper(official), asset)
	}
	// A one-letter ticker must be uppercase in the official evidence itself.
	// Case-folding here would turn ordinary grammar such as `Token(s)` into S.
	if len(asset) > 1 {
		official = strings.ToUpper(official)
	}
	for offset := 0; offset <= len(official)-len(asset); {
		index := strings.Index(official[offset:], asset)
		if index < 0 {
			return false
		}
		index += offset
		leftOK := index == 0 || !isASCIIAlphaNumericByte(official[index-1])
		right := index + len(asset)
		rightOK := right == len(official) || !isASCIIAlphaNumericByte(official[right])
		if leftOK && rightOK {
			return true
		}
		offset = index + len(asset)
	}
	return false
}

func isASCIIAlphaNumericByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func containsNormalizedEvidence(official, evidence string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(value), " "))
	}
	evidence = normalize(evidence)
	return evidence != "" && strings.Contains(normalize(official), evidence)
}

func containsNormalizedCompositeEvidence(official, evidence string) bool {
	parts := strings.Split(evidence, " / ")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if !containsNormalizedEvidence(official, part) {
			return false
		}
	}
	return true
}

func ensureNumbersGrounded(label, value, official string) error {
	unsupported := tokenDifference(numericTokens(value), numericTokens(official))
	if len(unsupported) > 0 {
		return fmt.Errorf("%s contains numbers absent from official text: %s", label, strings.Join(unsupported[:min(8, len(unsupported))], ", "))
	}
	return nil
}

func numericTokens(value string) map[string]bool {
	text := strings.ToLower(claimText(value))
	result := map[string]bool{}
	for _, raw := range numberPattern.FindAllString(text, -1) {
		token := strings.ReplaceAll(raw, ",", "")
		if strings.Contains(token, ".") {
			token = strings.TrimRight(strings.TrimRight(token, "0"), ".")
		}
		parts := strings.SplitN(token, ".", 2)
		parts[0] = strings.TrimLeft(parts[0], "0")
		if parts[0] == "" {
			parts[0] = "0"
		}
		result[strings.Join(parts, ".")] = true
	}
	for month, names := range monthNames {
		for _, name := range names {
			if containsWord(text, name) {
				result[strconv.Itoa(month)] = true
			}
		}
	}
	return result
}

func claimText(value string) string {
	value = imageLink.ReplaceAllString(value, "")
	value = textLink.ReplaceAllString(value, "$1")
	return urlPattern.ReplaceAllString(value, "")
}

func tokenDifference(left, right map[string]bool) []string {
	result := []string{}
	for value := range left {
		if !right[value] {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func validateScheduleTimestamp(label string, timestamp int64, schedule notice.Schedule) error {
	timestamp = normalizeTimestampMS(timestamp)
	if timestamp <= 0 || strings.EqualFold(schedule.Precision, "APPROXIMATE") {
		return nil
	}
	zone, err := scheduleLocation(schedule.Timezone)
	if err != nil {
		return err
	}
	moment := time.UnixMilli(timestamp).In(zone)
	if moment.Year() < 2020 || moment.Year() > 2100 {
		return fmt.Errorf("%s has implausible year %d", label, moment.Year())
	}
	tokens := numericTokens(schedule.Evidence)
	monthSupported := tokens[strconv.Itoa(int(moment.Month()))]
	if !monthSupported {
		lower := strings.ToLower(schedule.Evidence)
		for _, name := range monthNames[int(moment.Month())] {
			monthSupported = monthSupported || containsWord(lower, name)
		}
	}
	if !monthSupported || !tokens[strconv.Itoa(moment.Day())] {
		return fmt.Errorf("%s calendar date is not supported by its evidence", label)
	}
	for token := range tokens {
		if len(token) == 4 && strings.HasPrefix(token, "20") && token != strconv.Itoa(moment.Year()) {
			return fmt.Errorf("%s year conflicts with its evidence", label)
		}
	}
	if !scheduleEvidenceSupportsMoment(label, schedule, moment, zone) {
		return fmt.Errorf("%s calendar time is not exactly supported by its evidence", label)
	}
	return nil
}

func scheduleEvidenceSupportsMoment(label string, schedule notice.Schedule, moment time.Time, zone *time.Location) bool {
	// A date row ending in "예정" still belongs to its immediately preceding
	// service heading. Preserve that local scope before parsing the timestamp;
	// the model's free-form kind must not decide whether a real date is found.
	parsed := notice.ExtractSchedules(noticeQuestionLocalScheduleHeadings(schedule.Evidence), schedule.Kind, moment.UnixMilli())
	dayPrecision := strings.EqualFold(strings.TrimSpace(schedule.Precision), "DAY")
	wantEnd := strings.Contains(label, ".end_ts_ms")
	for _, candidate := range parsed {
		timestamp := candidate.StartTSMS
		if wantEnd {
			timestamp = candidate.EndTSMS
		}
		if timestamp <= 0 {
			continue
		}
		parsedMoment := time.UnixMilli(normalizeTimestampMS(timestamp)).In(zone)
		if parsedMoment.Year() != moment.Year() || parsedMoment.Month() != moment.Month() || parsedMoment.Day() != moment.Day() {
			continue
		}
		if dayPrecision || parsedMoment.Hour() == moment.Hour() && parsedMoment.Minute() == moment.Minute() {
			return true
		}
	}
	return false
}

func scheduleLocation(value string) (*time.Location, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "UTC", "GMT":
		return time.UTC, nil
	case "KST", "ASIA/SEOUL", "JST", "ASIA/TOKYO":
		return time.FixedZone("UTC+9", 9*60*60), nil
	default:
		location, err := time.LoadLocation(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("unsupported schedule timezone %q", value)
		}
		return location, nil
	}
}

func importantHint(value string) bool {
	lower := strings.ToLower(value)
	for _, term := range []string{
		"상장", "거래지원", "상장폐지", "지원 종료", "유의종목", "입출금 중단",
		"보안 사고", "해킹", "장애", "delist", "suspend", "security incident", "major outage",
	} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return containsWord(lower, "list") || containsWord(lower, "listing") || containsWord(lower, "listed")
}

func isForeignExchange(exchange string) bool {
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "binance", "bybit", "okx":
		return true
	default:
		return false
	}
}

func containsWord(text, word string) bool {
	for start := 0; ; {
		index := strings.Index(text[start:], word)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || !isASCIIAlpha(text[index-1])
		after := index + len(word)
		afterOK := after == len(text) || !isASCIIAlpha(text[after])
		if beforeOK && afterOK {
			return true
		}
		start = index + len(word)
	}
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func normalizedStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func normalizeTimestampMS(value int64) int64 {
	if value > 0 && value < 1_000_000_000_000 {
		return value * 1_000
	}
	return value
}

func normalizeSchedules(values []notice.Schedule) []notice.Schedule {
	result := make([]notice.Schedule, 0, len(values))
	for _, value := range values {
		value.StartTSMS = normalizeTimestampMS(value.StartTSMS)
		value.EndTSMS = normalizeTimestampMS(value.EndTSMS)
		value.Kind = strings.ToUpper(strings.TrimSpace(value.Kind))
		value.Timezone = strings.TrimSpace(value.Timezone)
		value.Precision = strings.ToUpper(strings.TrimSpace(value.Precision))
		value.Evidence = strings.TrimSpace(value.Evidence)
		if value.Kind != "" {
			result = append(result, value)
		}
	}
	return result
}

func clampConfidence(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return min(value, 1)
}

func mapString(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return value
}

func mapStrings(value any) []string {
	rows, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]string); typedOK {
			return typed
		}
		return []string{}
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if text, ok := row.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func mapInt64(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		result, _ := strconv.ParseInt(string(typed), 10, 64)
		return result
	case float64:
		return int64(typed)
	case int64:
		return typed
	case string:
		result, _ := strconv.ParseInt(typed, 10, 64)
		return result
	default:
		return 0
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[len(value)-maximum:]
}

func truncateRunes(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return string(runes[:maximum])
}
