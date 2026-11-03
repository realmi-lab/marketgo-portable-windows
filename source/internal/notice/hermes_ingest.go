package notice

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	HermesIngestWindowDays = 30
	HermesMaxBatchItems    = 500
	hermesMaxRunDuration   = 24 * time.Hour
)

var (
	ErrHermesIngestUnauthorized = errors.New("Hermes notice ingest authorization failed")
	ErrHermesIngestDisabled     = errors.New("Hermes notice ingest is not configured")
	ErrHermesIngestInvalid      = errors.New("invalid Hermes notice ingest payload")
	ErrHermesIngestConflict     = errors.New("Hermes notice expected revision changed")
)

type HermesIngestRequest struct {
	BatchID           string             `json:"batch_id"`
	WindowStartMS     int64              `json:"window_start_ts_ms"`
	ScannedExchanges  []string           `json:"scanned_exchanges,omitempty"`
	Items             []Item             `json:"items"`
	CorpusRange       *HermesCorpusRange `json:"corpus_range,omitempty"`
	ExpectedRevisions map[string]string  `json:"expected_revisions,omitempty"`
}

// HermesCorpusRange is an explicit publication interval for a domestic corpus.
// It never changes retention or claims source discovery completeness.
type HermesCorpusRange struct {
	FromTSMS int64 `json:"from_ts_ms"`
	ToTSMS   int64 `json:"to_ts_ms"`
}

func (r HermesCorpusRange) Validate(now time.Time) error {
	if r.FromTSMS <= 0 || r.ToTSMS < r.FromTSMS || r.ToTSMS > now.UnixMilli() || r.ToTSMS-r.FromTSMS >= (366*24*time.Hour).Milliseconds() {
		return fmt.Errorf("%w: corpus_range requires an explicit past publication interval of at most 366 days", ErrHermesIngestInvalid)
	}
	return nil
}

type HermesIngestResult struct {
	BatchID          string `json:"batch_id"`
	Accepted         int    `json:"accepted"`
	CreatedRevisions int    `json:"created_revisions"`
	Unchanged        int    `json:"unchanged"`
	ScannedSources   int    `json:"scanned_sources"`
	WindowStartMS    int64  `json:"window_start_ts_ms"`
}

func NormalizeHermesIngest(request HermesIngestRequest, now time.Time) (HermesIngestRequest, error) {
	request.BatchID = strings.TrimSpace(request.BatchID)
	if request.BatchID == "" || len(request.BatchID) > 128 {
		return HermesIngestRequest{}, fmt.Errorf("%w: batch_id is required and must be at most 128 bytes", ErrHermesIngestInvalid)
	}
	if len(request.Items) > HermesMaxBatchItems {
		return HermesIngestRequest{}, fmt.Errorf("%w: items must contain at most %d notices", ErrHermesIngestInvalid, HermesMaxBatchItems)
	}
	if err := validateHermesExpectedRevisions(request); err != nil {
		return HermesIngestRequest{}, err
	}
	scanned, err := normalizeHermesScannedExchanges(request.ScannedExchanges)
	if err != nil {
		return HermesIngestRequest{}, err
	}
	request.ScannedExchanges = scanned
	if len(request.Items) == 0 && len(request.ScannedExchanges) == 0 {
		return HermesIngestRequest{}, fmt.Errorf("%w: items or scanned_exchanges is required", ErrHermesIngestInvalid)
	}
	serverWindowStart := now.Add(-HermesIngestWindowDays * 24 * time.Hour).UnixMilli()
	if request.CorpusRange != nil {
		if err := request.CorpusRange.Validate(now); err != nil {
			return HermesIngestRequest{}, err
		}
		if len(request.ScannedExchanges) != 0 {
			return HermesIngestRequest{}, fmt.Errorf("%w: corpus batches cannot assert a recent-window source heartbeat", ErrHermesIngestInvalid)
		}
		request.WindowStartMS = request.CorpusRange.FromTSMS
		for index, item := range request.Items {
			if !IsDomesticExchange(item.Exchange) || item.PublishedTSMS < request.CorpusRange.FromTSMS || item.PublishedTSMS > request.CorpusRange.ToTSMS {
				return HermesIngestRequest{}, fmt.Errorf("%w: items[%d] is outside the domestic publication corpus", ErrHermesIngestInvalid, index)
			}
			normalized, err := normalizeHermesItem(item, request.WindowStartMS, now)
			if err != nil {
				return HermesIngestRequest{}, fmt.Errorf("%w: items[%d]: %v", ErrHermesIngestInvalid, index, err)
			}
			request.Items[index] = normalized
		}
		return request, nil
	}
	if request.WindowStartMS == 0 {
		request.WindowStartMS = serverWindowStart
	}
	if request.WindowStartMS < serverWindowStart-hermesMaxRunDuration.Milliseconds() || request.WindowStartMS > now.UnixMilli() {
		return HermesIngestRequest{}, fmt.Errorf("%w: window_start_ts_ms must describe the current 30-day window", ErrHermesIngestInvalid)
	}
	// Hermes can legitimately spend more than five minutes fetching and
	// analyzing official pages. Keep the submitted timestamp as a bounded run
	// freshness check, then enforce the exact server-side 30-day boundary for
	// every notice instead of widening the accepted item window.
	request.WindowStartMS = serverWindowStart
	for index := range request.Items {
		item, err := normalizeHermesItem(request.Items[index], request.WindowStartMS, now)
		if err != nil {
			return HermesIngestRequest{}, fmt.Errorf("%w: items[%d]: %v", ErrHermesIngestInvalid, index, err)
		}
		request.Items[index] = item
	}
	return request, nil
}

func validateHermesExpectedRevisions(request HermesIngestRequest) error {
	if request.ExpectedRevisions == nil {
		if request.CorpusRange != nil {
			for _, item := range request.Items {
				status, reason := strings.ToUpper(strings.TrimSpace(item.AnalysisStatus)), strings.ToUpper(strings.TrimSpace(item.EscalationReason))
				if status == "" {
					status = "ANALYZED"
				} // Match normalizeHermesItem's default.
				if status == "ANALYZED" || status == "ANALYSIS_NOT_REQUIRED" || strings.HasPrefix(reason, "RETRY_") || (status == "ANALYSIS_FAILED" && item.ContentSource != "HERMES_RAW_DETAIL_EMPTY") {
					return fmt.Errorf("%w: corpus analysis outcome requires expected_revisions", ErrHermesIngestInvalid)
				}
			}
		}
		return nil
	}
	if len(request.Items) == 0 || len(request.ExpectedRevisions) != len(request.Items) {
		return fmt.Errorf("%w: expected_revisions must match every item", ErrHermesIngestInvalid)
	}
	seen := map[string]bool{}
	for _, item := range request.Items {
		key := strings.ToLower(strings.TrimSpace(item.Exchange)) + ":" + strings.TrimSpace(item.NoticeID)
		token := request.ExpectedRevisions[key]
		value, err := strconv.ParseUint(token, 10, 32)
		if seen[key] || err != nil || value == 0 || strconv.FormatUint(value, 10) != token {
			return fmt.Errorf("%w: missing/invalid/duplicate expected revision for %s", ErrHermesIngestInvalid, key)
		}
		seen[key] = true
	}
	return nil
}

func normalizeHermesScannedExchanges(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		exchange := strings.ToLower(strings.TrimSpace(value))
		if exchange == "" {
			continue
		}
		if _, supported := hermesOfficialHosts[exchange]; !supported {
			return nil, fmt.Errorf("%w: unsupported scanned exchange %q", ErrHermesIngestInvalid, exchange)
		}
		if _, duplicate := seen[exchange]; duplicate {
			continue
		}
		seen[exchange] = struct{}{}
		result = append(result, exchange)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeHermesItem(item Item, windowStartMS int64, now time.Time) (Item, error) {
	item.Exchange = strings.ToLower(strings.TrimSpace(item.Exchange))
	item.NoticeID = strings.TrimSpace(item.NoticeID)
	item.Title = strings.TrimSpace(item.Title)
	item.URL = strings.TrimSpace(item.URL)
	item.Category = strings.ToUpper(strings.TrimSpace(item.Category))
	item.OriginalLanguage = strings.ToLower(strings.TrimSpace(item.OriginalLanguage))
	item.OriginalTitle = strings.TrimSpace(item.OriginalTitle)
	item.TranslationStatus = strings.ToUpper(strings.TrimSpace(item.TranslationStatus))
	item.Importance = strings.ToUpper(strings.TrimSpace(item.Importance))
	item.AnalysisProvider = strings.ToLower(strings.TrimSpace(item.AnalysisProvider))
	item.AnalysisModel = strings.TrimSpace(item.AnalysisModel)
	item.ReasoningEffort = strings.ToLower(strings.TrimSpace(item.ReasoningEffort))
	item.EscalationReason = strings.ToUpper(strings.TrimSpace(item.EscalationReason))
	item.AnalysisStatus = strings.ToUpper(strings.TrimSpace(item.AnalysisStatus))
	item.AnalysisPolicy = strings.TrimSpace(item.AnalysisPolicy)

	if _, ok := hermesOfficialHosts[item.Exchange]; !ok {
		return Item{}, fmt.Errorf("unsupported exchange %q", item.Exchange)
	}
	if item.NoticeID == "" || len(item.NoticeID) > 256 {
		return Item{}, errors.New("notice_id is required and must be at most 256 bytes")
	}
	if item.Title == "" || len(item.Title) > 2000 {
		return Item{}, errors.New("title is required and must be at most 2000 bytes")
	}
	if !OfficialNoticeURLMatchesID(item.Exchange, item.NoticeID, item.URL) {
		return Item{}, errors.New("url must be the canonical HTTPS notice URL with the matching official notice_id")
	}
	anchorMS := max(item.PublishedTSMS, item.UpdatedTSMS)
	if anchorMS <= 0 {
		anchorMS = item.DetectedTSMS
	}
	if anchorMS < windowStartMS || anchorMS > now.Add(24*time.Hour).UnixMilli() {
		return Item{}, errors.New("notice timestamp is outside the current 30-day ingest window")
	}
	if !isLowerHexSHA256(item.ContentHash) {
		return Item{}, errors.New("content_hash must be a lowercase SHA-256 hex digest")
	}
	if len(item.OriginalBodyText) > 1_000_000 || len(item.TranslatedBodyKO) > 1_000_000 {
		return Item{}, errors.New("notice body exceeds the 1 MB field limit")
	}
	if item.OriginalTitle == "" {
		item.OriginalTitle = item.Title
	}
	if item.OriginalLanguage == "" {
		if isForeignNoticeExchange(item.Exchange) {
			return Item{}, errors.New("original_language is required for foreign notices")
		}
		item.OriginalLanguage = "ko"
	}
	if item.Importance == "" {
		item.Importance = "NORMAL"
	}
	if item.Importance != "NORMAL" && item.Importance != "IMPORTANT" {
		return Item{}, errors.New("importance must be NORMAL or IMPORTANT")
	}
	if item.AnalysisStatus == "" {
		item.AnalysisStatus = "ANALYZED"
	}
	if !validHermesAnalysisStatus(item.AnalysisStatus) {
		return Item{}, errors.New("unsupported analysis_status")
	}
	actions, err := NormalizeActions(item.Actions)
	if err != nil {
		return Item{}, err
	}
	if item.AnalysisStatus != "ANALYZED" && len(actions) > 0 {
		return Item{}, errors.New("raw notices must not claim analyzed actions")
	}
	officialText := strings.Join([]string{item.OriginalTitle, item.OriginalBodyText}, "\n")
	for _, action := range actions {
		if !containsOfficialEvidence(officialText, action.Evidence) {
			return Item{}, errors.New("action evidence must be a verbatim official title/body excerpt")
		}
	}
	item.Actions = actions
	if err := validateHermesModelPolicy(item); err != nil {
		return Item{}, err
	}
	if isForeignNoticeExchange(item.Exchange) {
		if item.TranslationStatus == "" {
			item.TranslationStatus = "PENDING"
		}
		if item.TranslationStatus != "TRANSLATED" && item.TranslationStatus != "PENDING" && item.TranslationStatus != "FAILED" {
			return Item{}, errors.New("foreign translation_status must be TRANSLATED, PENDING, or FAILED")
		}
		if item.TranslationStatus == "TRANSLATED" && strings.TrimSpace(item.TranslatedTitleKO) == "" {
			return Item{}, errors.New("translated_title_ko is required when translation_status is TRANSLATED")
		}
	} else {
		item.TranslationStatus = "NOT_REQUIRED"
	}
	if item.DetectedTSMS <= 0 {
		item.DetectedTSMS = now.UnixMilli()
	}
	item.SourceType = "HERMES"
	if item.ContentSource == "" {
		if item.AnalysisStatus == "ANALYZED" {
			item.ContentSource = "HERMES_AI"
		} else {
			item.ContentSource = "HERMES_RAW"
		}
	}
	item.Assets = nonNilStringSlice(item.Assets)
	if item.Actions == nil {
		item.Actions = []Action{}
	}
	item.Schedules = nonNilScheduleSlice(item.Schedules)
	item.RiskTags = nonNilStringSlice(item.RiskTags)
	item.ImageURLs = nonNilStringSlice(item.ImageURLs)
	if item.ImageAnalysis == nil {
		item.ImageAnalysis = []map[string]any{}
	}
	return item, nil
}

func containsOfficialEvidence(officialText, evidence string) bool {
	if strings.Contains(officialText, evidence) {
		return true
	}
	// HTML-to-text extraction may wrap one official sentence across lines.
	// Normalize whitespace only; punctuation and wording must remain identical.
	return strings.Contains(strings.Join(strings.Fields(officialText), " "), strings.Join(strings.Fields(evidence), " "))
}

func validateHermesModelPolicy(item Item) error {
	if item.AnalysisStatus == "ANALYSIS_DEFERRED_QUOTA" || item.AnalysisStatus == "TRANSLATION_PENDING" ||
		item.AnalysisStatus == "ANALYSIS_FAILED" || item.AnalysisStatus == "ANALYSIS_NOT_REQUIRED" {
		if item.AnalysisProvider != "" || item.AnalysisModel != "" || item.ReasoningEffort != "" {
			return errors.New("raw notices without a successful analysis must not claim a provider or model")
		}
		return nil
	}
	switch item.AnalysisProvider {
	case "openai-codex":
		if item.AnalysisModel == "gpt-6.1-sol" && (item.ReasoningEffort == "low" || item.ReasoningEffort == "xhigh") {
			return nil
		}
		if item.AnalysisModel == "gpt-6-astra" && item.ReasoningEffort == "low" {
			return nil
		}
		if item.AnalysisModel == "gpt-5.6-sol" && item.ReasoningEffort == "medium" {
			return nil
		}
		if item.AnalysisModel == "gpt-5.6-terra" && item.ReasoningEffort == "medium" {
			return nil
		}
		// Accept earlier analyses when unchanged notices are replayed on restart.
		if item.AnalysisModel == "gpt-5.6-terra" && item.ReasoningEffort == "xhigh" {
			return nil
		}
		return errors.New("Codex analysis requires gpt-6.1-sol/low or a compatible legacy analysis model/effort")
	default:
		return errors.New("analysis_provider must be openai-codex")
	}
}

func validHermesAnalysisStatus(status string) bool {
	switch status {
	case "ANALYZED", "ANALYSIS_DEFERRED_QUOTA", "TRANSLATION_PENDING", "ANALYSIS_FAILED", "ANALYSIS_NOT_REQUIRED":
		return true
	default:
		return false
	}
}

func isForeignNoticeExchange(exchange string) bool {
	return exchange == "binance" || exchange == "bybit" || exchange == "okx"
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func nonNilStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilScheduleSlice(values []Schedule) []Schedule {
	if values == nil {
		return []Schedule{}
	}
	return values
}
