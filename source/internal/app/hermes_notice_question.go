package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/notice"
	"market-share-go/internal/noticewriter"
)

const (
	noticePersonaAuto           = "AUTO"
	noticePersonaFactResearcher = "FACT_RESEARCHER"
	noticePersonaVolumeAnalyst  = "VOLUME_ANALYST"
	noticePersonaEventBriefer   = "EVENT_BRIEFER"
	noticePersonaBithumbWriter  = "BITHUMB_STYLE_WRITER"
	noticeQuestionHistoryDays   = 30
	noticeQuestionRecentDays    = 7
	// Keep synchronous notice questions fast enough for the HTTP contract while
	// leaving room for the fixed prompt, a 500-rune question, and JSON escaping.
	noticeQuestionEvidenceJSONLimit = 16_000
	// The retrieval harness sends a small, evidence-rich candidate set to the
	// model instead of asking it to scan the whole 30-day catalog. The full
	// catalog remains available to the deterministic ranker below.
	noticeQuestionNoticeLimit        = 10
	noticeQuestionDeterministicLimit = 50
	// Model prompts stay bounded to ten evidence-rich rows, but deterministic
	// aggregate answers may describe more official notices. Keep every verified
	// link in that displayed aggregate (up to the deterministic catalog limit)
	// so the UI and the next question do not silently lose sources 11..N.
	noticeQuestionVerifiedSourceLimit   = noticeQuestionDeterministicLimit
	noticeQuestionAssetLimit            = 16
	noticeQuestionStyleLimit            = 3
	noticeQuestionScheduleLimit         = 4
	noticeQuestionScheduleEvidenceRunes = 180
	noticeQuestionPromptActionLimit     = 4
	noticeQuestionPromptSnippetLimit    = 2
	noticeQuestionPromptSnippetRunes    = 180
	noticeQuestionPromptSummaryRunes    = 220
	noticeQuestionPromptPolicyVersion   = "DOMESTIC_NOTICE_QUESTION_RAG_V17"
	// The HTTP request owns one budget, including snapshot retrieval and time
	// waiting for another interactive request. The runner inherits the remainder.
	noticeQuestionTotalTimeout = 5 * time.Minute
	// A short request-level cache skips the snapshot query and RAG projection
	// for an immediate repeat. The notice invalidation generation prevents it
	// from surviving an ingest; the short TTL also bounds sliding-window drift.
	noticeQuestionFastCacheTTL = 5 * time.Second
)

var noticeQuestionPersonas = map[string]struct{}{
	noticePersonaAuto: {}, noticePersonaFactResearcher: {}, noticePersonaVolumeAnalyst: {},
	noticePersonaEventBriefer: {}, noticePersonaBithumbWriter: {},
}

var domesticNoticeExchangeOrder = notice.DomesticExchanges()

type noticeQuestionIntent string

type bithumbWriterAction = noticewriter.Action

const (
	noticeQuestionIntentListing       noticeQuestionIntent = "LISTING"
	noticeQuestionIntentDelisting     noticeQuestionIntent = "DELISTING"
	noticeQuestionIntentCaution       noticeQuestionIntent = "CAUTION"
	noticeQuestionIntentDepositStop   noticeQuestionIntent = "DEPOSIT_STOP"
	noticeQuestionIntentDepositResume noticeQuestionIntent = "DEPOSIT_RESUME"
	noticeQuestionIntentDepositStatus noticeQuestionIntent = "DEPOSIT_STATUS"
	noticeQuestionIntentImportant     noticeQuestionIntent = "IMPORTANT_CHANGES"
	noticeQuestionIntentEvent         noticeQuestionIntent = "EVENT"
	noticeQuestionIntentMaintenance   noticeQuestionIntent = "MAINTENANCE"
)

const (
	bithumbWriterActionCautionDesignation = noticewriter.ActionCautionDesignation
	bithumbWriterActionCautionExtension   = noticewriter.ActionCautionExtension
	bithumbWriterActionCautionRelease     = noticewriter.ActionCautionRelease
	bithumbWriterActionCautionUrge        = noticewriter.ActionCautionUrge
	bithumbWriterActionDelisting          = noticewriter.ActionDelisting
	bithumbWriterActionDepositStop        = noticewriter.ActionDepositStop
	bithumbWriterActionDepositResume      = noticewriter.ActionDepositResume
)

type domesticNoticeQuestionEvidence struct {
	// Fixed policy fields stay in the typed server contract but are not repeated
	// in every model payload; the prompt owns the same immutable rules.
	Contract                       string                             `json:"-"`
	AsOfMS                         int64                              `json:"-"`
	Timezone                       string                             `json:"-"`
	Persona                        string                             `json:"-"`
	AllowedExchanges               []string                           `json:"-"`
	HistoryDays                    int                                `json:"-"`
	RecentWindowDays               int                                `json:"-"`
	Retrieval                      domesticNoticeRetrieval            `json:"retrieval"`
	ReferenceTimeKST               string                             `json:"reference_time_kst,omitempty"`
	SourceAvailability             []domesticNoticeSourceAvailability `json:"source_availability,omitempty"`
	VolumeScope                    *domesticNoticeVolumeScope         `json:"volume_scope,omitempty"`
	Notices                        []domesticNoticeEvidence           `json:"notices"`
	StyleExamples                  []domesticNoticeStyleEvidence      `json:"style_examples,omitempty"`
	ContextSourceIDs               []string                           `json:"conversation_context_source_ids,omitempty"`
	WriterVerifiedAssets           []string                           `json:"writer_verified_assets,omitempty"`
	WriterVerifiedSourceIDs        []string                           `json:"writer_verified_source_ids,omitempty"`
	WriterDraftContract            *noticewriter.DraftContract        `json:"writer_draft_contract,omitempty"`
	DailyAssetVolumes              []domesticNoticeVolumeDay          `json:"daily_asset_volumes,omitempty"`
	Limitations                    []string                           `json:"-"`
	requiresCollectionCompleteness bool
	cacheTemporalBoundaries        []int64
	promptJSON                     []byte
}

type domesticNoticeSourceAvailability struct {
	Exchange             string `json:"exchange"`
	HistoryStatus        string `json:"history_status"`
	ErrorKind            string `json:"error_kind,omitempty"`
	HistoryNote          string `json:"history_note,omitempty"`
	Complete             bool   `json:"history_complete"`
	LastHistoryScanAtKST string `json:"last_history_scan_at_kst,omitempty"`
}

type domesticNoticeVolumeScope struct {
	Status                string   `json:"status"`
	Assets                []string `json:"assets"`
	Exchanges             []string `json:"exchanges"`
	WindowStartKST        string   `json:"window_start_kst"`
	WindowEndKSTExclusive string   `json:"window_end_kst_exclusive"`
	QuerySourceIDs        []string `json:"source_notice_ids"`
	DayCount              int      `json:"day_count"`
	Limitation            string   `json:"limitation,omitempty"`
}

type domesticNoticeEvidence struct {
	SourceID        string                                      `json:"source_id"`
	Exchange        string                                      `json:"exchange"`
	NoticeID        string                                      `json:"-"`
	Title           string                                      `json:"title"`
	URL             string                                      `json:"-"`
	Category        string                                      `json:"category,omitempty"`
	PublishedTSMS   int64                                       `json:"-"`
	UpdatedTSMS     int64                                       `json:"-"`
	DetectedTSMS    int64                                       `json:"-"`
	PublishedAtKST  string                                      `json:"published_at_kst,omitempty"`
	UpdatedAtKST    string                                      `json:"updated_at_kst,omitempty"`
	Assets          []string                                    `json:"assets,omitempty"`
	Actions         []notice.Action                             `json:"actions,omitempty"`
	RiskTags        []string                                    `json:"risk_tags,omitempty"`
	Summary         string                                      `json:"summary,omitempty"`
	Schedules       []notice.Schedule                           `json:"schedules,omitempty"`
	AnalysisStatus  string                                      `json:"analysis_status,omitempty"`
	Importance      string                                      `json:"importance,omitempty"`
	MatchedEvidence []domesticNoticeMatchedEvidence             `json:"matched_evidence,omitempty"`
	BodyPassages    []string                                    `json:"body_passages,omitempty"`
	BodyTruncated   bool                                        `json:"body_truncated,omitempty"`
	BodyScope       string                                      `json:"body_scope,omitempty"`
	EventStatements []hermesrunner.NoticeQuestionEventStatement `json:"event_statements,omitempty"`

	// Retrieval-only source fields are deliberately excluded from the model
	// payload. The ranker searches the full evidence, then exposes only bounded
	// matching snippets with an explicit provenance kind.
	searchText            string
	bodyText              string
	bodyOriginalAvailable bool
	imageSources          []domesticNoticeImageSearchSource
	scheduleText          string
	riskText              string
}

type domesticNoticeStyleEvidence struct {
	SourceID       string `json:"source_id"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	OpeningExcerpt string `json:"opening_excerpt,omitempty"`
	ClosingExcerpt string `json:"closing_excerpt,omitempty"`
}

type domesticNoticeVolumeDay struct {
	Day                     string                      `json:"day"`
	IntervalStartKST        string                      `json:"interval_start_kst,omitempty"`
	IntervalEndKSTExclusive string                      `json:"interval_end_kst_exclusive,omitempty"`
	CoverageScope           string                      `json:"coverage_scope,omitempty"`
	Status                  string                      `json:"status"`
	CoverageStatus          string                      `json:"coverage_status"`
	CoverageSec             int64                       `json:"coverage_sec"`
	RequiredSec             int64                       `json:"required_sec"`
	ObservedMarketCount     int                         `json:"observed_market_count"`
	Assets                  []domesticNoticeVolumeAsset `json:"assets"`
	Error                   string                      `json:"error,omitempty"`
}

type domesticNoticeVolumeAsset struct {
	Asset         string   `json:"asset"`
	VolumeKRW     float64  `json:"volume_krw"`
	TradeCount    uint64   `json:"trade_count"`
	MarketCount   int      `json:"market_count"`
	ObservedCount int      `json:"observed_market_count"`
	Exchanges     []string `json:"exchanges"`
}

type noticeQuestionCacheEntry struct {
	key                  string
	requestKey           string
	snapshotGeneration   uint64
	requestCacheDeadline time.Time
	answerCacheDeadline  time.Time
	answer               marketanalysis.NoticeQuestionAnswer
}

const noticeQuestionAnswerCacheLimit = 32
const noticeQuestionAnswerCacheTTL = time.Minute

type noticeQuestionAnswerCache struct {
	entries []noticeQuestionCacheEntry
}

type noticeQuestionFlight struct {
	done   chan struct{}
	answer marketanalysis.NoticeQuestionAnswer
	err    error
}

func (cache *noticeQuestionAnswerCache) Get(key string) (marketanalysis.NoticeQuestionAnswer, bool) {
	for index, entry := range cache.entries {
		if entry.key != key || !time.Now().Before(entry.answerCacheDeadline) {
			continue
		}
		copy(cache.entries[1:index+1], cache.entries[:index])
		cache.entries[0] = entry
		return cloneNoticeQuestionAnswer(entry.answer), true
	}
	return marketanalysis.NoticeQuestionAnswer{}, false
}

func (cache *noticeQuestionAnswerCache) GetFreshRequest(
	requestKey string,
	snapshotGeneration uint64,
	now time.Time,
) (marketanalysis.NoticeQuestionAnswer, bool) {
	if requestKey == "" || now.IsZero() {
		return marketanalysis.NoticeQuestionAnswer{}, false
	}
	for index, entry := range cache.entries {
		if entry.requestKey != requestKey || entry.snapshotGeneration != snapshotGeneration ||
			!now.Before(entry.requestCacheDeadline) || !now.Before(entry.answerCacheDeadline) {
			continue
		}
		copy(cache.entries[1:index+1], cache.entries[:index])
		cache.entries[0] = entry
		return cloneNoticeQuestionAnswer(entry.answer), true
	}
	return marketanalysis.NoticeQuestionAnswer{}, false
}

func (cache *noticeQuestionAnswerCache) Put(key string, answer marketanalysis.NoticeQuestionAnswer) {
	cache.put(noticeQuestionCacheEntry{key: key, answer: cloneNoticeQuestionAnswer(answer)})
}

func (cache *noticeQuestionAnswerCache) PutFreshRequest(
	key, requestKey string,
	snapshotGeneration uint64,
	deadline time.Time,
	answer marketanalysis.NoticeQuestionAnswer,
) {
	cache.put(noticeQuestionCacheEntry{
		key: key, requestKey: requestKey, snapshotGeneration: snapshotGeneration,
		requestCacheDeadline: deadline, answer: cloneNoticeQuestionAnswer(answer),
	})
}

func (cache *noticeQuestionAnswerCache) put(entry noticeQuestionCacheEntry) {
	entry.answerCacheDeadline = time.Now().Add(noticeQuestionAnswerCacheTTL)
	for index, current := range cache.entries {
		if current.key != entry.key {
			continue
		}
		// Promoting an existing answer to the request shortcut must not extend
		// its total lifetime when no new analysis occurred.
		if current.answer.AsOfMS == entry.answer.AsOfMS {
			entry.answerCacheDeadline = current.answerCacheDeadline
		}
		copy(cache.entries[1:index+1], cache.entries[:index])
		cache.entries[0] = entry
		return
	}
	if len(cache.entries) < noticeQuestionAnswerCacheLimit {
		cache.entries = append(cache.entries, noticeQuestionCacheEntry{})
	}
	copy(cache.entries[1:], cache.entries[:len(cache.entries)-1])
	cache.entries[0] = entry
}

func cloneNoticeQuestionAnswer(value marketanalysis.NoticeQuestionAnswer) marketanalysis.NoticeQuestionAnswer {
	value.KeyPoints = slices.Clone(value.KeyPoints)
	value.Sources = slices.Clone(value.Sources)
	value.SourceNoticeIDs = slices.Clone(value.SourceNoticeIDs)
	return value
}

type domesticNoticeRAGIndex struct {
	version                int64
	contentIdentity        [sha256.Size]byte
	itemCount              int
	rows                   map[string]domesticNoticeEvidence
	selectionCount         int
	selectionFirstExchange string
	selectionFirstNoticeID string
	selectionLastExchange  string
	selectionLastNoticeID  string
	orderedRows            []domesticNoticeEvidence
	catalog                map[string]marketanalysis.NoticeQuestionSource
}

func (s *Service) AskHermesNoticeQuestion(
	ctx context.Context,
	persona, question string,
	contextSourceIDs []string,
) (result marketanalysis.NoticeQuestionAnswer, resultErr error) {
	if s == nil || !s.llmEnabled {
		return marketanalysis.NoticeQuestionAnswer{}, ErrLLMDisabled
	}
	question = strings.TrimSpace(question)
	persona = strings.ToUpper(strings.TrimSpace(persona))
	if persona == "" {
		persona = noticePersonaAuto
	}
	if !utf8.ValidString(question) || len([]rune(question)) < 2 || len([]rune(question)) > 500 {
		return marketanalysis.NoticeQuestionAnswer{}, marketanalysis.ErrInvalidMarketQuestion
	}
	if err := validatePublicLLMQuestionScope(question, nil); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	if _, valid := noticeQuestionPersonas[persona]; !valid {
		return marketanalysis.NoticeQuestionAnswer{}, marketanalysis.ErrInvalidMarketQuestion
	}
	ctx, cancel := context.WithTimeout(ctx, noticeQuestionTotalTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	persona = resolvedNoticeQuestionPersona(persona, question)
	requestNow := time.Now().UTC()
	archiveRequest := noticeQuestionUsesArchive(question, contextSourceIDs, requestNow)
	requestCacheKey := domesticNoticeQuestionRequestCacheKey(persona, question, contextSourceIDs)
	s.noticeMemoOnce.Do(func() { s.noticeMemo = &noticeSnapshotMemo{} })
	if !archiveRequest && !noticeQuestionNeedsVolume(persona, question) {
		snapshotGeneration := s.noticeMemo.generationValue()
		s.hermesNoticeQuestionMu.Lock()
		cached, ok := s.hermesNoticeQuestionCache.GetFreshRequest(
			requestCacheKey, snapshotGeneration, requestNow,
		)
		s.hermesNoticeQuestionMu.Unlock()
		if ok {
			return cached, nil
		}
	}
	snapshot, snapshotGeneration, err := s.noticeSnapshotVersioned(ctx)
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	now := requestNow
	if archiveRequest {
		snapshot, err = s.noticeQuestionArchiveSnapshot(ctx, snapshot, question, contextSourceIDs, now)
		if err != nil {
			return marketanalysis.NoticeQuestionAnswer{}, err
		}
	}
	evidence, catalog := s.buildDomesticNoticeQuestionEvidence(ctx, snapshot, persona, question, contextSourceIDs, now)
	if err := ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	if evidence.Retrieval.InvalidCalendarDate || (fallbackNoticeQuestionWantsCount(question) && evidence.Retrieval.MatchedNoticeCount == 0 && len(domesticNoticeIncompleteSources(evidence)) > 0) {
		return finalizeDomesticNoticeQuestionAnswer(buildDomesticNoticeQuestionFallback(evidence, persona, question), evidence, catalog, persona), nil
	}
	if answer, ok := bithumbWriterMultiActionClarification(evidence, persona, question); ok {
		return finalizeDomesticNoticeQuestionAnswer(answer, evidence, catalog, persona), nil
	}
	if assets := ambiguousNoticeFollowUpAssets(evidence, question); len(assets) > 1 {
		answer := marketanalysis.NoticeQuestionAnswer{
			Answer:    "직전 답변의 공식 공지에는 여러 자산이 포함되어 있습니다. 다음 중 하나의 티커를 지정해 다시 질문해 주세요: " + strings.Join(assets, ", "),
			KeyPoints: []string{"자산 선택 필요", strings.Join(assets, ", ")},
			Caveat:    "복수 자산 문맥에서 임의의 코인을 선택하지 않았습니다.",
			Source:    "SERVER_NOTICE_CLARIFICATION", AsOfMS: evidence.AsOfMS,
			SourceNoticeIDs: append([]string(nil), evidence.ContextSourceIDs...),
		}
		return finalizeDomesticNoticeQuestionAnswer(answer, evidence, catalog, persona), nil
	}
	if noticeQuestionUsesExactServerAnswer(persona, question) {
		answer := buildDomesticNoticeQuestionFallback(evidence, persona, question)
		answer.Source = "SERVER_NOTICE_DETERMINISTIC"
		return finalizeDomesticNoticeQuestionAnswer(answer, evidence, catalog, persona), nil
	}
	evidenceJSON := evidence.promptJSON
	if len(evidenceJSON) == 0 {
		evidenceJSON, err = json.Marshal(evidence)
		if err != nil {
			return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("encode domestic notice question evidence: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	cacheKey := domesticNoticeQuestionCacheKeyFromPayload(persona, question, evidence, evidenceJSON)
	s.hermesNoticeQuestionMu.Lock()
	if cached, ok := s.hermesNoticeQuestionCache.Get(cacheKey); ok {
		if !archiveRequest && s.noticeMemo.generationValue() == snapshotGeneration && !noticeQuestionNeedsVolume(persona, question) {
			s.hermesNoticeQuestionCache.PutFreshRequest(
				cacheKey, requestCacheKey, snapshotGeneration,
				domesticNoticeQuestionCacheDeadline(evidence, time.Now()), cached,
			)
		}
		s.hermesNoticeQuestionMu.Unlock()
		return cached, nil
	}
	if existing := s.hermesNoticeQuestionFlights[cacheKey]; existing != nil {
		s.hermesNoticeQuestionMu.Unlock()
		select {
		case <-ctx.Done():
			return marketanalysis.NoticeQuestionAnswer{}, ctx.Err()
		case <-existing.done:
			if existing.err != nil {
				return marketanalysis.NoticeQuestionAnswer{}, existing.err
			}
			answer := cloneNoticeQuestionAnswer(existing.answer)
			return answer, nil
		}
	}
	if s.hermesNoticeQuestionFlights == nil {
		s.hermesNoticeQuestionFlights = make(map[string]*noticeQuestionFlight)
	}
	flight := &noticeQuestionFlight{done: make(chan struct{})}
	s.hermesNoticeQuestionFlights[cacheKey] = flight
	if s.hermesNoticeQuestionRunGate == nil {
		s.hermesNoticeQuestionRunGate = make(chan struct{}, 1)
	}
	runGate := s.hermesNoticeQuestionRunGate
	s.hermesNoticeQuestionMu.Unlock()
	// Publish every terminal result, including cancellation while queued. A
	// same-key waiter must never receive an empty successful answer or hang.
	defer func() {
		s.hermesNoticeQuestionMu.Lock()
		flight.answer = cloneNoticeQuestionAnswer(result)
		flight.err = resultErr
		delete(s.hermesNoticeQuestionFlights, cacheKey)
		close(flight.done)
		s.hermesNoticeQuestionMu.Unlock()
	}()

	// Keep one interactive Hermes process at a time: its priority marker and
	// local runtime are shared. Cache hits and same-key waiters no longer sit
	// behind another execution. Waiting uses the same request deadline as the
	// model, and cancellation does not leave an unbounded mutex waiter behind.
	select {
	case runGate <- struct{}{}:
		defer func() { <-runGate }()
	case <-ctx.Done():
		return marketanalysis.NoticeQuestionAnswer{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	noticeQuestionConfig := domesticNoticeQuestionModelConfig(
		s.hermesQuestionConfig, persona, question, evidence,
	)
	answer, askErr := hermesrunner.AskNoticeQuestion(
		ctx, noticeQuestionConfig, persona, question, evidenceJSON, now.UnixMilli(),
	)
	if err := ctx.Err(); err != nil {
		if askErr != nil && s.logger != nil {
			s.logger.Warn("Hermes domestic notice question canceled", "error", askErr, "persona", persona)
		}
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	if askErr != nil {
		if s.logger != nil {
			s.logger.Warn("Hermes domestic notice question used server fallback", "error", askErr, "persona", persona)
		}
		fallbackEvidence := evidence
		if fallbackNoticeQuestionWantsExchangeGroups(question) && !evidence.Retrieval.ContextApplied {
			fallbackEvidence = expandedGroupedFallbackEvidence(snapshot, evidence, question, now)
			// The fallback now describes more official notices than the model
			// sample. Validate its links against that same verified snapshot.
			_, catalog = s.domesticNoticeQuestionRAGRows(snapshot, domesticNoticeQuestionScopedItems(snapshot.Items, now, question, contextSourceIDs))
		}
		answer = buildDomesticNoticeQuestionFallback(fallbackEvidence, persona, question)
		evidence = fallbackEvidence
	}
	finalized := finalizeDomesticNoticeQuestionAnswer(answer, evidence, catalog, persona)
	s.hermesNoticeQuestionMu.Lock()
	if finalized.Source == "HERMES_NOTICE" || finalized.Source == "HERMES_NOTICE_SOL_FALLBACK" {
		if !archiveRequest && s.noticeMemo.generationValue() == snapshotGeneration && !noticeQuestionNeedsVolume(persona, question) {
			s.hermesNoticeQuestionCache.PutFreshRequest(
				cacheKey, requestCacheKey, snapshotGeneration,
				domesticNoticeQuestionCacheDeadline(evidence, time.Now()), finalized,
			)
		} else {
			s.hermesNoticeQuestionCache.Put(cacheKey, finalized)
		}
	}
	s.hermesNoticeQuestionMu.Unlock()
	return finalized, nil
}

func domesticNoticeQuestionRequestCacheKey(persona, question string, contextSourceIDs []string) string {
	contextJSON, _ := json.Marshal(contextSourceIDs)
	normalizedQuestion := strings.Join(strings.Fields(strings.ToLower(question)), " ")
	hash := sha256.Sum256(contextJSON)
	return fmt.Sprintf("%s:%s:%s:%x", noticeQuestionPromptPolicyVersion+":"+hermesrunner.NoticeQuestionPolicyIdentity(),
		strings.ToUpper(strings.TrimSpace(persona)), normalizedQuestion, hash)
}

func domesticNoticeQuestionModelConfig(
	base hermesrunner.MarketQuestionConfig,
	persona, question string,
	evidence domesticNoticeQuestionEvidence,
) hermesrunner.MarketQuestionConfig {
	// Every interactive notice answer uses the same 5,000-token upper bound as
	// market answers. Preserve the application-selected model; a persona must
	// not silently lower the output budget or change the model route.
	base.MaxTokens = hermesrunner.MaxInteractiveQuestionOutputTokens
	return base
}

func domesticNoticeQuestionCacheKey(persona, question string, evidence domesticNoticeQuestionEvidence) string {
	payload, _ := json.Marshal(evidence)
	return domesticNoticeQuestionCacheKeyFromPayload(persona, question, evidence, payload)
}

func domesticNoticeQuestionCacheKeyFromPayload(
	persona, question string,
	evidence domesticNoticeQuestionEvidence,
	payload []byte,
) string {
	// Start with the actual model projection, removing only the reference
	// clock that is handled by the temporal identity and bounded cache lifetime.
	hasher := sha256.New()
	// The response preserves its original AsOfMS. Equivalent facts may reuse
	// an answer, but a scheduled start/end changes its temporal identity.
	var cachePayload map[string]json.RawMessage
	if json.Unmarshal(payload, &cachePayload) == nil {
		delete(cachePayload, "reference_time_kst")
		payload, _ = json.Marshal(cachePayload)
	}
	_, _ = hasher.Write(payload)
	// Raw identity and epoch fields stay out of the model prompt, but an
	// official revision must still invalidate a cached answer even when its
	// query-local excerpt happens to remain unchanged.
	for _, row := range evidence.Notices {
		contentDigest := sha256.Sum256([]byte(row.searchText))
		_, _ = fmt.Fprintf(hasher, "\x00%s:%d:%d:%d:%x:%s",
			row.SourceID, row.PublishedTSMS, row.UpdatedTSMS, row.DetectedTSMS, contentDigest, row.URL)
	}
	for _, boundary := range domesticNoticeTemporalBoundaries(evidence) {
		_, _ = fmt.Fprintf(hasher, ":%d:%t", boundary, boundary <= evidence.AsOfMS)
	}

	digest := hasher.Sum(nil)
	normalizedQuestion := strings.Join(strings.Fields(strings.ToLower(question)), " ")
	return fmt.Sprintf("%s:%s:%s:%x", noticeQuestionPromptPolicyVersion+":"+hermesrunner.NoticeQuestionPolicyIdentity(),
		strings.ToUpper(strings.TrimSpace(persona)), normalizedQuestion, digest)
}

func domesticNoticeTemporalBoundaries(evidence domesticNoticeQuestionEvidence) []int64 {
	if evidence.cacheTemporalBoundaries != nil {
		return evidence.cacheTemporalBoundaries
	}
	boundaries := make([]int64, 0)
	for _, row := range evidence.Notices {
		for _, schedule := range row.Schedules {
			for _, boundary := range []int64{schedule.StartTSMS, schedule.EndTSMS} {
				if boundary > 0 {
					boundaries = append(boundaries, boundary)
				}
			}
		}
	}
	slices.Sort(boundaries)
	return slices.Compact(boundaries)
}

func domesticNoticeTemporalBoundariesForSources(selected, allRows []domesticNoticeEvidence) []int64 {
	ids := make(map[string]struct{}, len(selected))
	for _, row := range selected {
		ids[row.SourceID] = struct{}{}
	}
	fullRows := make([]domesticNoticeEvidence, 0, len(selected))
	for _, row := range allRows {
		if _, ok := ids[row.SourceID]; ok {
			fullRows = append(fullRows, row)
		}
	}
	return domesticNoticeTemporalBoundaries(domesticNoticeQuestionEvidence{Notices: fullRows})
}

func domesticNoticeQuestionCacheDeadline(evidence domesticNoticeQuestionEvidence, now time.Time) time.Time {
	deadline := now.Add(noticeQuestionFastCacheTTL)
	for _, boundary := range domesticNoticeTemporalBoundaries(evidence) {
		// A pre-transition result completing after the event must not enter the
		// request shortcut. Preserve every boundary captured before prompt fitting.
		if boundary > evidence.AsOfMS && boundary < deadline.UnixMilli() {
			deadline = time.UnixMilli(boundary)
		}
	}
	return deadline
}

func ambiguousNoticeFollowUpAssets(evidence domesticNoticeQuestionEvidence, question string) []string {
	if len(evidence.ContextSourceIDs) == 0 || !noticeQuestionUsesAssetPronoun(question) {
		return nil
	}
	contextIDs := make(map[string]struct{}, len(evidence.ContextSourceIDs))
	for _, sourceID := range evidence.ContextSourceIDs {
		contextIDs[sourceID] = struct{}{}
	}
	known := map[string]struct{}{}
	for _, item := range evidence.Notices {
		if _, inContext := contextIDs[item.SourceID]; !inContext {
			continue
		}
		for _, asset := range domesticNoticeEvidencePrimaryAssets(item) {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset != "" {
				known[asset] = struct{}{}
			}
		}
	}
	if len(noticeQuestionExplicitAssets(question, known)) == 1 {
		return nil
	}
	assets := make([]string, 0, len(known))
	for asset := range known {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	return assets
}

func noticeQuestionUsesAssetPronoun(question string) bool {
	text := strings.ToLower(question)
	return containsNoticeQuestionAny(text, "그 코인", "그 자산", "해당 코인", "해당 자산")
}

func expandedGroupedFallbackEvidence(
	snapshot notice.Snapshot,
	evidence domesticNoticeQuestionEvidence,
	question string,
	now time.Time,
) domesticNoticeQuestionEvidence {
	items := domesticNoticeQuestionScopedItems(snapshot.Items, now, question, evidence.ContextSourceIDs)
	rows := make([]domesticNoticeEvidence, 0, len(items))
	for _, item := range items {
		rows = append(rows, buildDomesticNoticeSearchEvidence(
			item, noticeQuestionSourceID(item.Exchange, item.NoticeID),
		))
	}
	evidence.Notices, evidence.Retrieval = expandedDeterministicNoticeRows(
		rows, question, now, evidence.Retrieval,
	)
	return evidence
}

func finalizeDomesticNoticeQuestionAnswer(
	answer marketanalysis.NoticeQuestionAnswer,
	evidence domesticNoticeQuestionEvidence,
	catalog map[string]marketanalysis.NoticeQuestionSource,
	persona string,
) marketanalysis.NoticeQuestionAnswer {
	answer.Persona = persona
	if incomplete := domesticNoticeIncompleteSources(evidence); len(incomplete) > 0 {
		limitation := domesticNoticeExchangeLabels(incomplete) + "의 공지 수집 완료를 확인하지 못해 마지막 수집 자료만 확인했습니다."
		if !strings.Contains(answer.Caveat, limitation) {
			answer.Caveat = strings.TrimSpace(answer.Caveat + " " + limitation)
		}
	} else if evidence.requiresCollectionCompleteness && len(evidence.SourceAvailability) > 0 && !evidence.Retrieval.ContextApplied {
		limitation := "목록과 건수는 마지막 수집 자료 기준입니다."
		if !strings.Contains(answer.Caveat, limitation) {
			answer.Caveat = strings.TrimSpace(answer.Caveat + " " + limitation)
		}
	}
	if scope := evidence.VolumeScope; scope != nil && scope.WindowStartKST != "" && scope.WindowEndKSTExclusive != "" {
		limitation := "거래대금 조회 범위: " + domesticNoticeExchangeLabels(scope.Exchanges) + " · " + strings.Join(scope.Assets, ", ") + " · " + scope.WindowStartKST + " 이상 " + scope.WindowEndKSTExclusive + " 미만. 수신 완전성은 국내 전체 연결 기준이며 선택 자산의 완전성을 뜻하지 않습니다."
		if !strings.Contains(answer.Caveat, limitation) {
			answer.Caveat = strings.TrimSpace(answer.Caveat + " " + limitation)
		}
	}
	if evidence.VolumeScope != nil && evidence.VolumeScope.Limitation != "" && !strings.Contains(answer.Caveat, evidence.VolumeScope.Limitation) {
		answer.Caveat = strings.TrimSpace(answer.Caveat + " " + evidence.VolumeScope.Limitation)
	}
	if evidence.Retrieval.WindowLimited {
		limitation := "요청한 " + evidence.Retrieval.RequestedWindowDays + "일 전체가 아니라 조회 가능한 최근 " + fmt.Sprint(evidence.Retrieval.WindowDays) + "일 범위만 확인했습니다."
		if !strings.Contains(answer.Caveat, limitation) {
			answer.Caveat = strings.TrimSpace(answer.Caveat + " " + limitation)
		}
	}
	answer.Sources = validatedNoticeQuestionSources(answer.SourceNoticeIDs, catalog)
	if len(answer.Sources) == 0 && evidence.Retrieval.MatchedNoticeCount > 0 {
		// Aggregate answers can be grounded in retrieval metadata without citing
		// a single notice. Still return the bounded official candidate set so the
		// UI exposes auditable links and validated follow-up context.
		answer.Sources = validatedNoticeQuestionSources(noticeQuestionEvidenceSourceIDs(evidence.Notices), catalog)
		if evidence.Retrieval.Truncated && !strings.Contains(answer.Caveat, "출처 목록") {
			answer.Caveat = strings.TrimSpace(answer.Caveat + fmt.Sprintf(
				" 공식 공지 출처 목록은 전체 일치 %d건 중 상위 %d건 후보입니다.",
				evidence.Retrieval.MatchedNoticeCount, len(answer.Sources),
			))
		}
	}
	answer.SourceNoticeIDs = nil
	return answer
}

func noticeQuestionUsesDeterministicFactAnswer(persona, question string) bool {
	if persona == noticePersonaBithumbWriter {
		return false
	}
	if persona != noticePersonaFactResearcher && persona != noticePersonaAuto &&
		persona != noticePersonaVolumeAnalyst && persona != noticePersonaEventBriefer {
		return false
	}
	if fallbackNoticeQuestionWantsCount(question) || fallbackNoticeQuestionWantsExchangeGroups(question) {
		return true
	}
	if noticeQuestionRequiresLLMSynthesis(question) {
		return false
	}
	if noticeQuestionNeedsVolume(persona, question) {
		return persona != noticePersonaEventBriefer
	}
	intent := noticeQuestionIntentFor(question)
	switch persona {
	case noticePersonaEventBriefer:
		return intent == noticeQuestionIntentEvent
	case noticePersonaVolumeAnalyst:
		return false
	default:
		return intent != ""
	}
}

// The server owns calculations, not general answer wording. A count/volume
// request may also ask about conditions in arbitrary language. Always give
// the model the computed facts so it can answer the complete request without
// requiring the question to contain an allowlisted synthesis verb.
func noticeQuestionUsesExactServerAnswer(persona, question string) bool {
	if persona == noticePersonaBithumbWriter {
		// A publication draft has a fixed server-owned contract and every
		// unverified fact remains unresolved. Asking a model to reproduce that
		// document adds latency and failure modes without adding information.
		// Style analysis remains on the evidence-grounded model path.
		return bithumbWriterDraftRequested(question) && !bithumbWriterWantsStyleAnalysis(question)
	}
	return false
}

func noticeQuestionRequiresLLMSynthesis(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	return containsNoticeQuestionAny(text,
		"의미", "설명", "분석", "해석", "왜", "원인", "사유", "영향",
		"비교", "차이", "공통점", "상관관계", "인과관계", "시사점",
	)
}

func (s *Service) buildDomesticNoticeQuestionEvidence(
	ctx context.Context,
	snapshot notice.Snapshot,
	persona, question string, contextSourceIDs []string,
	now time.Time,
) (domesticNoticeQuestionEvidence, map[string]marketanalysis.NoticeQuestionSource) {
	allItems := domesticNoticeQuestionScopedItems(snapshot.Items, now, question, contextSourceIDs)
	allRows, catalog := s.domesticNoticeQuestionRAGRows(snapshot, allItems)
	contextSourceIDs = validatedNoticeQuestionSourceIDs(contextSourceIDs, catalog)
	rows, retrieval := retrieveDomesticNoticeQuestionRows(allRows, question, contextSourceIDs, now)
	if retrieval.ContextApplied {
		contextSourceIDs = noticeQuestionContextIDsInRows(contextSourceIDs, rows)
	} else {
		contextSourceIDs = nil
	}
	writerActions := bithumbWriterRequestedActions(question)
	if persona == noticePersonaBithumbWriter && len(writerActions) > 1 {
		rows, retrieval = mixedBithumbWriterFactRows(allRows, writerActions, retrieval)
		contextSourceIDs = noticeQuestionContextIDsInRows(contextSourceIDs, rows)
	} else if persona == noticePersonaBithumbWriter && bithumbWriterActionForText(question) != "" &&
		(!bithumbWriterWantsStyleAnalysis(question) || len(writerActions) == 1) {
		rows, retrieval = exactBithumbWriterFactRows(allRows, question, now, retrieval)
		contextSourceIDs = noticeQuestionContextIDsInRows(contextSourceIDs, rows)
	}
	if noticeQuestionUsesDeterministicFactAnswer(persona, question) && !retrieval.ContextApplied {
		switch {
		case noticeQuestionIntentFor(question) == noticeQuestionIntentDepositStatus:
			rows, retrieval = expandedDepositStatusRows(allRows, question, now, retrieval)
		case noticeQuestionIntentFor(question) == noticeQuestionIntentCaution && noticeQuestionWantsCautionStatusBreakdown(question):
			rows, retrieval = expandedCautionStatusRows(allRows, question, now, retrieval)
		case noticeQuestionIntentFor(question) == noticeQuestionIntentImportant:
			rows, retrieval = expandedImportantNoticeRows(allRows, question, now, retrieval)
		case fallbackNoticeQuestionWantsExchangeGroups(question):
			rows, retrieval = expandedDeterministicNoticeRows(allRows, question, now, retrieval)
		}
	}
	style := []domesticNoticeStyleEvidence{}
	if persona == noticePersonaBithumbWriter {
		style = domesticNoticeQuestionStyleExamples(allItems, allRows, question, now)
	}
	evidence := domesticNoticeQuestionEvidence{
		Contract: "DOMESTIC_NOTICE_QUESTION_V1", AsOfMS: now.UnixMilli(), Timezone: "Asia/Seoul",
		Persona: persona, AllowedExchanges: append([]string(nil), domesticNoticeExchangeOrder...),
		HistoryDays: noticeQuestionHistoryDays, RecentWindowDays: noticeQuestionRecentDays,
		Retrieval: retrieval, Notices: rows, StyleExamples: style, ContextSourceIDs: contextSourceIDs,
		ReferenceTimeKST:               now.In(seoulLocation).Format("2006-01-02 15:04:05 MST"),
		requiresCollectionCompleteness: fallbackNoticeQuestionWantsCount(question) || fallbackNoticeQuestionWantsExchangeGroups(question) || containsNoticeQuestionAny(question, "전체", "모든", "전부", "목록", "리스트", "최근", "최신"),
		Limitations: []string{
			"공지와 거래량의 시간적 동시성은 인과관계가 아니다.",
			"일별 거래량은 WebSocket 관측 범위가 불완전하면 확정값이 아니다.",
			"해외 거래소 공지는 근거에서 제외한다.",
		},
	}
	evidence.SourceAvailability = domesticNoticeSourceAvailabilityFor(snapshot, evidence)
	if persona == noticePersonaBithumbWriter {
		evidence.WriterVerifiedAssets, evidence.WriterVerifiedSourceIDs = bithumbWriterVerifiedDraftAssets(evidence, question)
		if action := bithumbWriterActionForText(question); bithumbWriterDraftRequested(question) && action != "" {
			if contract, ok := noticewriter.ContractFor(action); ok {
				evidence.WriterDraftContract = &contract
			}
		}
	}
	if noticeQuestionNeedsVolume(persona, question) {
		evidence.VolumeScope = domesticNoticeMeasurementScope(evidence, question, now)
		if evidence.VolumeScope.Status == "READY" {
			evidence.DailyAssetVolumes = s.domesticNoticeScopedDailyVolumes(ctx, *evidence.VolumeScope)
		}
	}
	if persona == noticePersonaBithumbWriter {
		evidence = compactBithumbWriterDraftEvidence(evidence, question)
	}
	evidence.ContextSourceIDs = noticeQuestionContextIDsInRows(evidence.ContextSourceIDs, evidence.Notices)
	completeDomesticNoticeExchangeCounts(&evidence)
	if !noticeQuestionUsesExactServerAnswer(persona, question) {
		// Scope facts in the prompt must also be numeric evidence. A calendar
		// date provides future/past context without invalidating the LRU every
		// second. Exact intraday availability still requires explicit evidence.
		evidence.Retrieval.HistoryDays = noticeQuestionHistoryDays
		if noticeQuestionUsesArchive(question, contextSourceIDs, now) {
			evidence.Retrieval.HistoryDays = 0
		}
		evidence.Retrieval.ReferenceDateKST = now.In(seoulLocation).Format("2006-01-02")
		if len(evidence.Notices) > noticeQuestionNoticeLimit {
			evidence.Notices = evidence.Notices[:noticeQuestionNoticeLimit]
			evidence.Retrieval.ReturnedNoticeCount = len(evidence.Notices)
			evidence.Retrieval.Truncated = true
		}
		if persona != noticePersonaBithumbWriter {
			enrichDomesticNoticeBodyPassages(&evidence, question)
		}
		evidence = fitDomesticNoticeQuestionEvidence(evidence, noticeQuestionEvidenceJSONLimit)
	}
	// Retrieval and byte fitting both cap schedule arrays. Resolve the final
	// source IDs back to complete source rows before deriving cache transitions.
	evidence.cacheTemporalBoundaries = domesticNoticeTemporalBoundariesForSources(evidence.Notices, allRows)
	selectedCatalog := make(map[string]marketanalysis.NoticeQuestionSource, len(evidence.Notices)+len(evidence.StyleExamples))
	for _, row := range evidence.Notices {
		if source, ok := catalog[row.SourceID]; ok {
			selectedCatalog[row.SourceID] = source
		}
	}
	for _, example := range evidence.StyleExamples {
		if source, ok := catalog[example.SourceID]; ok {
			selectedCatalog[example.SourceID] = source
		}
	}
	for _, sourceID := range evidence.WriterVerifiedSourceIDs {
		if source, ok := catalog[sourceID]; ok {
			selectedCatalog[sourceID] = source
		}
	}
	return evidence, selectedCatalog
}

// Zero is an observed query result only for an exchange included in this
// retrieval scope. Complete the server aggregates before any model sample is
// capped; ContextApplied and the remaining retrieval predicates keep these
// counts tied to the selected prior sources, period, asset and notice type.
func completeDomesticNoticeExchangeCounts(evidence *domesticNoticeQuestionEvidence) {
	// An invalid predicate or missing retrieval result is not an observed zero.
	if evidence.Retrieval.InvalidCalendarDate || strings.TrimSpace(evidence.Retrieval.Strategy) == "" {
		return
	}
	scope := domesticNoticeQueryExchanges(*evidence)
	counts := make(map[string]int, len(evidence.Retrieval.ExchangeNoticeCounts))
	for _, count := range evidence.Retrieval.ExchangeNoticeCounts {
		counts[strings.ToLower(strings.TrimSpace(count.Exchange))] = count.Count
	}
	selected := make(map[string]struct{}, len(scope))
	for _, exchange := range scope {
		if exchange = strings.ToLower(strings.TrimSpace(exchange)); exchange != "" {
			selected[exchange] = struct{}{}
		}
	}
	result := make([]domesticNoticeExchangeCount, 0, len(selected))
	total := 0
	for _, exchange := range sortedNoticeQuestionSet(selected) {
		count := counts[exchange]
		if count == 0 && !evidence.Retrieval.ContextApplied && !domesticNoticeSourceComplete(*evidence, exchange) {
			continue
		}
		result = append(result, domesticNoticeExchangeCount{Exchange: exchange, Count: count})
		total += count
	}
	// Do not turn an incomplete or inconsistent aggregate into a claim of
	// complete exchange coverage. Normal retrieval computes this exact sum.
	if total == evidence.Retrieval.MatchedNoticeCount {
		evidence.Retrieval.ExchangeNoticeCounts = result
	}
}

func domesticNoticeExchangeLabels(exchanges []string) string {
	labels := make([]string, 0, len(exchanges))
	for _, exchange := range exchanges {
		labels = append(labels, rollingExchangeLabel(exchange))
	}
	return strings.Join(labels, ", ")
}

func domesticNoticeSourceAvailabilityFor(snapshot notice.Snapshot, evidence domesticNoticeQuestionEvidence) []domesticNoticeSourceAvailability {
	scope := domesticNoticeQueryExchanges(evidence)
	result := make([]domesticNoticeSourceAvailability, 0, len(scope))
	for _, exchange := range scope {
		source, exists := snapshot.Sources[exchange]
		status := strings.ToUpper(strings.TrimSpace(source.HistoryStatus))
		if !exists || status == "" {
			status = "UNKNOWN"
		}
		complete := (status == "COMPLETE" || status == "HERMES_30D_ACTIVE") && source.ErrorKind == "" && source.Error == ""
		result = append(result, domesticNoticeSourceAvailability{
			Exchange: exchange, HistoryStatus: status, ErrorKind: source.ErrorKind,
			HistoryNote: truncateNoticeQuestionText(source.HistoryNote, 160), Complete: complete,
			LastHistoryScanAtKST: domesticNoticeHistoryScanTime(source.LastHistoryScanTS),
		})
	}
	return result
}

func domesticNoticeHistoryScanTime(seconds float64) string {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return ""
	}
	return time.UnixMilli(int64(seconds * 1000)).In(seoulLocation).Format(time.RFC3339)
}

func domesticNoticeQueryExchanges(evidence domesticNoticeQuestionEvidence) []string {
	scope := evidence.Retrieval.Exchanges
	if len(scope) == 0 {
		scope = evidence.AllowedExchanges
	}
	result := make([]string, 0, len(scope))
	for _, exchange := range scope {
		excluded := false
		for _, filter := range evidence.Retrieval.ExcludedFilters {
			if len(filter.Assets) == 0 && len(filter.Intents) == 0 && filter.CautionAction == "" && len(filter.ListingMarkets) == 0 && len(filter.QueryTerms) == 0 && slices.Contains(filter.Exchanges, exchange) {
				excluded = true
				break
			}
		}
		if !excluded {
			result = append(result, exchange)
		}
	}
	return result
}

func domesticNoticeSourceComplete(evidence domesticNoticeQuestionEvidence, exchange string) bool {
	for _, source := range evidence.SourceAvailability {
		if source.Exchange == exchange {
			return source.Complete
		}
	}
	return false
}

func domesticNoticeIncompleteSources(evidence domesticNoticeQuestionEvidence) []string {
	if evidence.Retrieval.ContextApplied {
		return nil
	}
	var result []string
	for _, source := range evidence.SourceAvailability {
		if !source.Complete {
			result = append(result, strings.ToUpper(source.Exchange))
		}
	}
	return result
}

func (s *Service) domesticNoticeQuestionRAGRows(
	snapshot notice.Snapshot,
	orderedItems []notice.Item,
) ([]domesticNoticeEvidence, map[string]marketanalysis.NoticeQuestionSource) {
	// The DB version tracks the newest official timestamp, not every write.
	// Enriching an older notice's body/OCR/analysis can preserve both version
	// and count. Reuse the content identity computed by the snapshot loader;
	// uncached snapshots are hashed outside the RAG lock.
	if snapshot.Version > 0 {
		s.noticeMemoOnce.Do(func() { s.noticeMemo = &noticeSnapshotMemo{} })
		contentIdentity, validIdentity := s.noticeMemo.contentIdentity(snapshot)
		if !validIdentity {
			// Malformed optional metadata must never admit a stale cache hit.
			return domesticNoticeQuestionRowsFromIndex(nil, orderedItems)
		}
		s.noticeQuestionRAGMu.Lock()
		if s.noticeQuestionRAGIndex.version != snapshot.Version ||
			s.noticeQuestionRAGIndex.contentIdentity != contentIdentity ||
			s.noticeQuestionRAGIndex.itemCount != len(snapshot.Items) || s.noticeQuestionRAGIndex.rows == nil {
			rows := make(map[string]domesticNoticeEvidence, len(snapshot.Items))
			for _, item := range snapshot.Items {
				if !notice.IsDomesticExchange(item.Exchange) {
					continue
				}
				sourceID := noticeQuestionSourceID(item.Exchange, item.NoticeID)
				rows[sourceID] = buildDomesticNoticeSearchEvidence(item, sourceID)
			}
			s.noticeQuestionRAGIndex = domesticNoticeRAGIndex{
				version: snapshot.Version, contentIdentity: contentIdentity, itemCount: len(snapshot.Items), rows: rows,
			}
		}
		firstExchange, firstNoticeID, lastExchange, lastNoticeID := "", "", "", ""
		if len(orderedItems) > 0 {
			firstExchange, firstNoticeID = orderedItems[0].Exchange, orderedItems[0].NoticeID
			lastItem := orderedItems[len(orderedItems)-1]
			lastExchange, lastNoticeID = lastItem.Exchange, lastItem.NoticeID
		}
		if s.noticeQuestionRAGIndex.selectionCount != len(orderedItems) ||
			s.noticeQuestionRAGIndex.selectionFirstExchange != firstExchange ||
			s.noticeQuestionRAGIndex.selectionFirstNoticeID != firstNoticeID ||
			s.noticeQuestionRAGIndex.selectionLastExchange != lastExchange ||
			s.noticeQuestionRAGIndex.selectionLastNoticeID != lastNoticeID ||
			s.noticeQuestionRAGIndex.orderedRows == nil || s.noticeQuestionRAGIndex.catalog == nil {
			orderedRows, catalog := domesticNoticeQuestionRowsFromIndex(s.noticeQuestionRAGIndex.rows, orderedItems)
			s.noticeQuestionRAGIndex.selectionCount = len(orderedItems)
			s.noticeQuestionRAGIndex.selectionFirstExchange = firstExchange
			s.noticeQuestionRAGIndex.selectionFirstNoticeID = firstNoticeID
			s.noticeQuestionRAGIndex.selectionLastExchange = lastExchange
			s.noticeQuestionRAGIndex.selectionLastNoticeID = lastNoticeID
			s.noticeQuestionRAGIndex.orderedRows = orderedRows
			s.noticeQuestionRAGIndex.catalog = catalog
		}
		rows, catalog := s.noticeQuestionRAGIndex.orderedRows, s.noticeQuestionRAGIndex.catalog
		s.noticeQuestionRAGMu.Unlock()
		return rows, catalog
	}
	return domesticNoticeQuestionRowsFromIndex(nil, orderedItems)
}

func domesticNoticeQuestionRowsFromIndex(
	indexed map[string]domesticNoticeEvidence,
	orderedItems []notice.Item,
) ([]domesticNoticeEvidence, map[string]marketanalysis.NoticeQuestionSource) {
	rows := make([]domesticNoticeEvidence, 0, len(orderedItems))
	catalog := make(map[string]marketanalysis.NoticeQuestionSource, len(orderedItems))
	for _, item := range orderedItems {
		sourceID := noticeQuestionSourceID(item.Exchange, item.NoticeID)
		row, ok := indexed[sourceID]
		if !ok {
			row = buildDomesticNoticeSearchEvidence(item, sourceID)
		}
		rows = append(rows, row)
		catalog[sourceID] = marketanalysis.NoticeQuestionSource{
			Exchange: item.Exchange, NoticeID: item.NoticeID, Title: item.Title,
			URL: item.URL, PublishedTSMS: item.PublishedTSMS,
		}
	}
	return rows, catalog
}

func compactBithumbWriterDraftEvidence(evidence domesticNoticeQuestionEvidence, question string) domesticNoticeQuestionEvidence {
	if bithumbWriterWantsStyleAnalysis(question) {
		return evidence
	}
	// Keep at most three minimal rows for provenance. Full snippets duplicate the
	// style examples, consume model context, and still cannot fill mandatory
	// [확인 필요] fields.
	const writerDraftFactLimit = 3
	compact := make([]domesticNoticeEvidence, 0, min(len(evidence.Notices), writerDraftFactLimit))
	for _, row := range evidence.Notices {
		compact = append(compact, domesticNoticeEvidence{
			SourceID: row.SourceID, Exchange: row.Exchange, NoticeID: row.NoticeID,
			Title: row.Title, URL: row.URL, Category: row.Category,
			PublishedTSMS: row.PublishedTSMS, PublishedAtKST: row.PublishedAtKST,
			Assets: append([]string(nil), row.Assets...), Actions: append([]notice.Action(nil), row.Actions...),
		})
		if len(compact) == writerDraftFactLimit {
			break
		}
	}
	evidence.Notices = compact
	evidence.Retrieval.ReturnedNoticeCount = len(compact)
	evidence.Retrieval.CandidateLimit = writerDraftFactLimit
	evidence.Retrieval.Truncated = evidence.Retrieval.MatchedNoticeCount > len(compact)
	evidence.DailyAssetVolumes = nil
	return evidence
}

func expandedCautionStatusRows(
	allRows []domesticNoticeEvidence,
	question string,
	now time.Time,
	retrieval domesticNoticeRetrieval,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	matches := fallbackNoticeMatches(allRows, question, now.UnixMilli())
	designated := make([]domesticNoticeEvidence, 0, len(matches))
	extended := make([]domesticNoticeEvidence, 0, len(matches))
	released := make([]domesticNoticeEvidence, 0, len(matches))
	for _, item := range matches {
		switch domesticNoticeEvidenceCautionAction(item) {
		case bithumbWriterActionCautionExtension:
			extended = append(extended, item)
		case bithumbWriterActionCautionRelease:
			released = append(released, item)
		case bithumbWriterActionCautionDesignation:
			designated = append(designated, item)
		}
	}
	retrieval.CautionDesignationCount = len(designated)
	retrieval.CautionExtensionCount = len(extended)
	retrieval.CautionReleaseCount = len(released)
	groups := [][]domesticNoticeEvidence{designated, extended, released}
	countRows := make([]domesticNoticeEvidence, 0, len(matches))
	present := 0
	for _, group := range groups {
		countRows = append(countRows, group...)
		if len(group) > 0 {
			present++
		}
	}
	result := make([]domesticNoticeEvidence, 0, noticeQuestionNoticeLimit)
	indexes := make([]int, len(groups))
	if present > 0 {
		quota := noticeQuestionNoticeLimit / present
		for groupIndex, group := range groups {
			for indexes[groupIndex] < len(group) && indexes[groupIndex] < quota {
				result = append(result, group[indexes[groupIndex]])
				indexes[groupIndex]++
			}
		}
	}
	for len(result) < noticeQuestionNoticeLimit {
		added := false
		for groupIndex, group := range groups {
			if len(result) == noticeQuestionNoticeLimit {
				break
			}
			if indexes[groupIndex] >= len(group) {
				continue
			}
			result = append(result, group[indexes[groupIndex]])
			indexes[groupIndex]++
			added = true
		}
		if !added {
			break
		}
	}
	retrieval.EligibleNoticeCount = len(allRows)
	retrieval.MatchedNoticeCount = len(designated) + len(extended) + len(released)
	retrieval.ExchangeNoticeCounts = domesticNoticeExchangeCounts(countRows)
	retrieval.ReturnedNoticeCount = len(result)
	retrieval.CandidateLimit = noticeQuestionNoticeLimit
	retrieval.Truncated = retrieval.MatchedNoticeCount > len(result)
	retrieval.PublicationOrder = domesticNoticeMatchedPublicationOrder(countRows, result)
	return result, retrieval
}

func exactBithumbWriterFactRows(
	allRows []domesticNoticeEvidence,
	question string,
	now time.Time,
	retrieval domesticNoticeRetrieval,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	matches := fallbackNoticeMatches(allRows, question, now.UnixMilli())
	exact := make([]domesticNoticeEvidence, 0, len(matches))
	intent := noticeQuestionIntentFor(question)
	for _, row := range matches {
		if !strings.EqualFold(row.Exchange, "bithumb") ||
			!noticeQuestionStyleCompatible(row.Title, row.Category, intent, question) {
			continue
		}
		exact = append(exact, row)
	}
	result := exact
	if len(result) > noticeQuestionNoticeLimit {
		result = result[:noticeQuestionNoticeLimit]
	}
	retrieval.MatchedNoticeCount = len(exact)
	retrieval.ExchangeNoticeCounts = domesticNoticeExchangeCounts(exact)
	retrieval.ReturnedNoticeCount = len(result)
	retrieval.CandidateLimit = noticeQuestionNoticeLimit
	retrieval.Truncated = len(exact) > len(result)
	retrieval.PublicationOrder = domesticNoticeMatchedPublicationOrder(exact, result)
	return result, retrieval
}

func mixedBithumbWriterFactRows(
	allRows []domesticNoticeEvidence,
	actions []bithumbWriterAction,
	retrieval domesticNoticeRetrieval,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	groups := make(map[bithumbWriterAction][]domesticNoticeEvidence, len(actions))
	wanted := make(map[bithumbWriterAction]struct{}, len(actions))
	for _, action := range actions {
		wanted[action] = struct{}{}
	}
	matched := 0
	matchedRows := make([]domesticNoticeEvidence, 0)
	for _, row := range allRows {
		if !strings.EqualFold(row.Exchange, "bithumb") {
			continue
		}
		action := bithumbWriterActionForEvidence(row)
		if _, ok := wanted[action]; !ok {
			continue
		}
		groups[action] = append(groups[action], row)
		matchedRows = append(matchedRows, row)
		matched++
	}
	result := make([]domesticNoticeEvidence, 0, min(matched, noticeQuestionNoticeLimit))
	indexes := make(map[bithumbWriterAction]int, len(actions))
	for len(result) < noticeQuestionNoticeLimit {
		added := false
		for _, action := range actions {
			index := indexes[action]
			if index >= len(groups[action]) {
				continue
			}
			result = append(result, groups[action][index])
			indexes[action] = index + 1
			added = true
			if len(result) == noticeQuestionNoticeLimit {
				break
			}
		}
		if !added {
			break
		}
	}
	retrieval.MatchedNoticeCount = matched
	retrieval.ExchangeNoticeCounts = domesticNoticeExchangeCounts(matchedRows)
	retrieval.ReturnedNoticeCount = len(result)
	retrieval.CandidateLimit = noticeQuestionNoticeLimit
	retrieval.Truncated = matched > len(result)
	retrieval.PublicationOrder = domesticNoticeMatchedPublicationOrder(matchedRows, result)
	return result, retrieval
}

func expandedDeterministicNoticeRows(
	allRows []domesticNoticeEvidence,
	question string,
	now time.Time,
	retrieval domesticNoticeRetrieval,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	matches := fallbackNoticeMatches(allRows, question, now.UnixMilli())
	result := matches
	if len(result) > noticeQuestionDeterministicLimit {
		result = result[:noticeQuestionDeterministicLimit]
	}
	retrieval.EligibleNoticeCount = len(allRows)
	retrieval.MatchedNoticeCount = len(matches)
	retrieval.ExchangeNoticeCounts = domesticNoticeExchangeCounts(matches)
	retrieval.ReturnedNoticeCount = len(result)
	retrieval.CandidateLimit = noticeQuestionDeterministicLimit
	retrieval.Truncated = len(matches) > len(result)
	retrieval.PublicationOrder = domesticNoticeMatchedPublicationOrder(matches, result)
	return result, retrieval
}

func expandedDepositStatusRows(
	allRows []domesticNoticeEvidence,
	question string,
	now time.Time,
	retrieval domesticNoticeRetrieval,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	matches := fallbackNoticeMatches(allRows, question, now.UnixMilli())
	suspended := make([]domesticNoticeEvidence, 0, len(matches))
	resumed := make([]domesticNoticeEvidence, 0, len(matches))
	for _, item := range matches {
		switch {
		case domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositResume):
			resumed = append(resumed, item)
		case domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositStop):
			suspended = append(suspended, item)
		}
	}
	retrieval.DepositSuspensionCount = len(suspended)
	retrieval.DepositResumptionCount = len(resumed)
	result := make([]domesticNoticeEvidence, 0, noticeQuestionNoticeLimit)
	seen := make(map[string]struct{}, noticeQuestionNoticeLimit)
	appendBounded := func(items []domesticNoticeEvidence, limit int) {
		for _, item := range items {
			if len(result) == noticeQuestionNoticeLimit || limit == 0 {
				return
			}
			if _, duplicate := seen[item.SourceID]; duplicate {
				continue
			}
			seen[item.SourceID] = struct{}{}
			result = append(result, item)
			limit--
		}
	}
	appendBounded(suspended, noticeQuestionNoticeLimit/2)
	appendBounded(resumed, noticeQuestionNoticeLimit/2)
	// Preserve a balanced sample when both states are plentiful, but lend any
	// unused half to the state that actually has more official notices.
	appendBounded(suspended, noticeQuestionNoticeLimit-len(result))
	appendBounded(resumed, noticeQuestionNoticeLimit-len(result))
	retrieval.EligibleNoticeCount = len(allRows)
	retrieval.MatchedNoticeCount = len(suspended) + len(resumed)
	countRows := make([]domesticNoticeEvidence, 0, retrieval.MatchedNoticeCount)
	countRows = append(countRows, suspended...)
	countRows = append(countRows, resumed...)
	retrieval.ExchangeNoticeCounts = domesticNoticeExchangeCounts(countRows)
	retrieval.ReturnedNoticeCount = len(result)
	retrieval.CandidateLimit = noticeQuestionNoticeLimit
	retrieval.Truncated = retrieval.MatchedNoticeCount > len(result)
	retrieval.PublicationOrder = domesticNoticeMatchedPublicationOrder(countRows, result)
	return result, retrieval
}

func expandedImportantNoticeRows(
	allRows []domesticNoticeEvidence,
	question string,
	now time.Time,
	retrieval domesticNoticeRetrieval,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	matches := fallbackNoticeMatches(allRows, question, now.UnixMilli())
	trade, risk, operations := splitImportantNoticeRows(matches)
	retrieval.ImportantTradingCount = len(trade)
	retrieval.ImportantRiskCount = len(risk)
	retrieval.ImportantOperationCount = len(operations)
	result := make([]domesticNoticeEvidence, 0, noticeQuestionNoticeLimit)
	seen := make(map[string]struct{}, noticeQuestionNoticeLimit)
	appendBounded := func(items []domesticNoticeEvidence, limit int) {
		for _, item := range items {
			if len(result) == noticeQuestionNoticeLimit || limit == 0 {
				return
			}
			if _, duplicate := seen[item.SourceID]; duplicate {
				continue
			}
			seen[item.SourceID] = struct{}{}
			result = append(result, item)
			limit--
		}
	}
	appendBounded(trade, 3)
	appendBounded(risk, 3)
	appendBounded(operations, 4)
	appendBounded(matches, noticeQuestionNoticeLimit-len(result))
	retrieval.EligibleNoticeCount = len(allRows)
	retrieval.MatchedNoticeCount = len(matches)
	retrieval.ExchangeNoticeCounts = domesticNoticeExchangeCounts(matches)
	retrieval.ReturnedNoticeCount = len(result)
	retrieval.CandidateLimit = noticeQuestionNoticeLimit
	retrieval.Truncated = len(matches) > len(result)
	retrieval.PublicationOrder = domesticNoticeMatchedPublicationOrder(matches, result)
	return result, retrieval
}

func splitImportantNoticeRows(items []domesticNoticeEvidence) (trade, risk, operations []domesticNoticeEvidence) {
	for _, item := range items {
		if len(item.Actions) > 0 {
			switch {
			case notice.HasAction(item.Actions, "TRADING_SUPPORT"):
				trade = append(trade, item)
			case notice.HasAction(item.Actions, "CAUTION") || notice.HasAction(item.Actions, "SECURITY"):
				risk = append(risk, item)
			case notice.HasAction(item.Actions, "DEPOSIT_WITHDRAWAL") || notice.HasAction(item.Actions, "MAINTENANCE"):
				operations = append(operations, item)
			}
			continue
		}
		category := strings.ToUpper(strings.TrimSpace(item.Category))
		switch {
		case category == "LISTING" || category == "MARKET_ADDITION" || category == "DELISTING" ||
			category == "TRADING_SUPPORT_TERMINATION":
			trade = append(trade, item)
		case category == "CAUTION" || category == "SECURITY" || category == "SECURITY_INCIDENT":
			risk = append(risk, item)
		case strings.HasPrefix(category, "DEPOSIT_WITHDRAWAL") || category == "MAINTENANCE":
			operations = append(operations, item)
		}
	}
	return trade, risk, operations
}

func fitDomesticNoticeQuestionEvidence(evidence domesticNoticeQuestionEvidence, maximumBytes int) domesticNoticeQuestionEvidence {
	if evidence.cacheTemporalBoundaries == nil {
		evidence.cacheTemporalBoundaries = domesticNoticeTemporalBoundaries(evidence)
	}
	if maximumBytes <= 0 {
		return evidence
	}
	// Spend the existing budget on authoritative evidence first. Auxiliary
	// annotations must never force a passage or source out of the payload.
	for i := range evidence.Notices {
		evidence.Notices[i].EventStatements = nil
	}
	for {
		if order := evidence.Retrieval.PublicationOrder; order != nil {
			order.LatestReturnedSourceIDs = noticeQuestionContextIDsInRows(order.LatestReturnedSourceIDs, evidence.Notices)
		}
		payload, err := marshalDomesticNoticeQuestionPrompt(evidence, maximumBytes)
		if err != nil || len(payload) <= maximumBytes {
			if err == nil {
				evidence.promptJSON = payload
				annotateDomesticNoticeEventStatements(&evidence)
				annotated, annotationErr := marshalDomesticNoticeQuestionPrompt(evidence, maximumBytes)
				if annotationErr == nil && len(annotated) <= maximumBytes {
					evidence.promptJSON = annotated
				} else {
					for i := range evidence.Notices {
						evidence.Notices[i].EventStatements = nil
					}
				}
			}
			return evidence
		}
		if shrinkDomesticNoticeQuestionEvidence(&evidence) {
			continue
		}
		if len(evidence.Notices) > 1 {
			evidence.Notices = evidence.Notices[:len(evidence.Notices)-1]
			evidence.Retrieval.ReturnedNoticeCount = len(evidence.Notices)
			evidence.Retrieval.Truncated = true
			continue
		}
		if len(evidence.StyleExamples) > 1 {
			evidence.StyleExamples = evidence.StyleExamples[:len(evidence.StyleExamples)-1]
			continue
		}
		if len(evidence.DailyAssetVolumes) > 0 {
			// Never substitute fewer days or different assets to fit a model
			// budget. Keep the requested measurement scope and report the limit.
			evidence.DailyAssetVolumes = nil
			if evidence.VolumeScope != nil {
				evidence.VolumeScope.Status = "EVIDENCE_LIMIT"
				evidence.VolumeScope.Limitation = "요청 범위의 거래대금 표가 답변 근거 한도를 넘었습니다. 자산 또는 기간을 좁혀 다시 질문해 주세요."
			}
			continue
		}
		return evidence
	}
}

// shrinkDomesticNoticeQuestionEvidence spends the byte budget on breadth
// before depth. Each pass removes redundant detail from every candidate while
// retaining at least one official snippet, action and schedule per row. Only
// after these stages are exhausted may the caller discard a complete notice.
func shrinkDomesticNoticeQuestionEvidence(evidence *domesticNoticeQuestionEvidence) bool {
	changed := false
	// Reduce optional body depth before discarding a source. Keep an explicit
	// truncation marker so a model cannot treat the selected passages as a full
	// document. The existing provenance snippets/actions remain available.
	for index := range evidence.Notices {
		row := &evidence.Notices[index]
		if len(row.BodyPassages) == 0 {
			continue
		}
		if len(row.BodyPassages) > 1 {
			row.BodyPassages = row.BodyPassages[:len(row.BodyPassages)-1]
		} else if utf8.RuneCountInString(row.BodyPassages[0]) > 200 {
			row.BodyPassages = []string{string([]rune(row.BodyPassages[0])[:200])}
		} else {
			row.BodyPassages = nil
		}
		row.BodyTruncated = true
		row.BodyScope = "EXCERPT_ONLY"
		changed = true
	}
	if changed {
		return true
	}
	for index := range evidence.Notices {
		row := &evidence.Notices[index]
		if len(row.MatchedEvidence) > 1 {
			row.MatchedEvidence = row.MatchedEvidence[:1]
			changed = true
		}
	}
	if changed {
		return true
	}
	for index := range evidence.Notices {
		row := &evidence.Notices[index]
		if len(row.MatchedEvidence) > 0 && row.Summary != "" {
			row.Summary = ""
			changed = true
		}
	}
	if changed {
		return true
	}
	for index := range evidence.Notices {
		row := &evidence.Notices[index]
		if len(row.Actions) > 2 {
			row.Actions = row.Actions[:2]
			changed = true
		}
		if len(row.Schedules) > 2 {
			row.Schedules = row.Schedules[:2]
			changed = true
		}
	}
	if changed {
		return true
	}
	for index := range evidence.Notices {
		row := &evidence.Notices[index]
		for snippetIndex := range row.MatchedEvidence {
			trimmed := truncateNoticeQuestionText(row.MatchedEvidence[snippetIndex].Text, 120)
			if trimmed != row.MatchedEvidence[snippetIndex].Text {
				row.MatchedEvidence[snippetIndex].Text = trimmed
				changed = true
			}
		}
		for actionIndex := range row.Actions {
			trimmed := truncateNoticeQuestionText(row.Actions[actionIndex].Evidence, 120)
			if trimmed != row.Actions[actionIndex].Evidence {
				row.Actions[actionIndex].Evidence = trimmed
				changed = true
			}
		}
		for scheduleIndex := range row.Schedules {
			trimmed := truncateNoticeQuestionText(row.Schedules[scheduleIndex].Evidence, 120)
			if trimmed != row.Schedules[scheduleIndex].Evidence {
				row.Schedules[scheduleIndex].Evidence = trimmed
				changed = true
			}
		}
	}
	if changed {
		return true
	}
	for index := range evidence.StyleExamples {
		trimmed := truncateNoticeQuestionText(evidence.StyleExamples[index].Body, 240)
		if trimmed != evidence.StyleExamples[index].Body {
			evidence.StyleExamples[index].Body = trimmed
			changed = true
		}
	}
	if changed {
		return true
	}
	if len(evidence.StyleExamples) > 1 {
		evidence.StyleExamples = evidence.StyleExamples[:len(evidence.StyleExamples)-1]
		return true
	}
	return false
}

func noticeQuestionContextIDsInRows(contextSourceIDs []string, rows []domesticNoticeEvidence) []string {
	available := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		available[row.SourceID] = struct{}{}
	}
	result := make([]string, 0, len(contextSourceIDs))
	for _, sourceID := range contextSourceIDs {
		if _, exists := available[sourceID]; exists {
			result = append(result, sourceID)
		}
	}
	return result
}

func domesticNoticeQuestionItems(items []notice.Item, now time.Time) []notice.Item {
	cutoff := now.Add(-noticeQuestionHistoryDays * 24 * time.Hour).UnixMilli()
	result := make([]notice.Item, 0, len(items))
	for _, item := range items {
		if !notice.IsDomesticExchange(item.Exchange) {
			continue
		}
		anchor := noticeQuestionItemAnchorMS(item)
		if anchor < cutoff {
			continue
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := noticeQuestionItemAnchorMS(result[i])
		right := noticeQuestionItemAnchorMS(result[j])
		return left > right
	})
	return result
}

func prioritizedDomesticNoticeQuestionRows(items []domesticNoticeEvidence, question string, contextSourceIDs []string, now time.Time) []domesticNoticeEvidence {
	rows, _ := retrieveDomesticNoticeQuestionRows(items, question, contextSourceIDs, now)
	return rows
}

func noticeQuestionAssets(items []notice.Item, question string, contextSourceIDs []string, now time.Time) []string {
	known := map[string]struct{}{}
	for _, item := range items {
		for _, asset := range domesticNoticeQuestionItemAssets(item) {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset != "" {
				known[asset] = struct{}{}
			}
		}
	}
	selected := noticeQuestionExplicitAssets(question, known)
	// An explicitly named ticker is the measurement target. The surrounding
	// notice may legitimately contain several assets, but those siblings are
	// provenance context and must not silently expand a single-asset volume
	// question into a batch query.
	if len(selected) > 0 {
		return selected[:min(len(selected), noticeQuestionAssetLimit)]
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, asset := range selected {
		selectedSet[asset] = struct{}{}
	}
	intent := noticeQuestionIntentFor(question)
	if intent == "" {
		contextSet := make(map[string]struct{}, len(contextSourceIDs))
		for _, id := range contextSourceIDs {
			contextSet[id] = struct{}{}
		}
		for _, item := range items {
			if _, contextual := contextSet[noticeQuestionSourceID(item.Exchange, item.NoticeID)]; !contextual {
				continue
			}
			for _, asset := range domesticNoticeQuestionItemAssets(item) {
				asset = strings.ToUpper(strings.TrimSpace(asset))
				if asset == "" {
					continue
				}
				if _, exists := selectedSet[asset]; !exists {
					selected = append(selected, asset)
					selectedSet[asset] = struct{}{}
				}
			}
		}
	}
	cutoff := now.Add(-noticeQuestionRecentDays * 24 * time.Hour).UnixMilli()
	if len(selected) > 0 && intent == "" {
		return selected[:min(len(selected), noticeQuestionAssetLimit)]
	}
	for _, item := range items {
		if noticeQuestionItemAnchorMS(item) < cutoff || !noticeQuestionItemMatches(item.Title, canonicalNoticeQuestionCategory(item.Title, item.Category), intent) {
			continue
		}
		for _, asset := range domesticNoticeQuestionItemAssets(item) {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset == "" {
				continue
			}
			if _, exists := selectedSet[asset]; !exists {
				selected = append(selected, asset)
				selectedSet[asset] = struct{}{}
			}
			if len(selected) == noticeQuestionAssetLimit {
				return selected
			}
		}
	}
	return selected
}

func noticeQuestionItemAnchorMS(item notice.Item) int64 {
	return notice.PublicationTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
}

func domesticNoticeEvidenceAnchorMS(item domesticNoticeEvidence) int64 {
	return notice.PublicationTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
}

func normalizedNoticeQuestionAssets(groups ...[]string) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for _, values := range groups {
		for _, value := range values {
			value = strings.ToUpper(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, duplicate := seen[value]; duplicate {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func domesticNoticeQuestionItemAssets(item notice.Item) []string {
	titleAssets := notice.TitleAssets(item.Title)
	switch strings.ToUpper(strings.TrimSpace(item.AnalysisStatus)) {
	case "ANALYSIS_FAILED", "ANALYSIS_DEFERRED_QUOTA", "TRANSLATION_PENDING":
		// A raw fallback has no validated model extraction. Restrict it to the
		// conservative title parser and an explicitly labelled official target
		// asset section. Never scan the whole body: reward units such as BTC and
		// challenge round numbers must not survive as affected ticker symbols.
		return normalizedNoticeQuestionAssets(titleAssets, domesticNoticeOfficialTargetAssets(item.OriginalBodyText))
	default:
		return normalizedNoticeQuestionAssets(item.Assets, titleAssets)
	}
}

func domesticNoticeOfficialTargetAssets(body string) []string {
	result := []string{}
	collecting := false
	for _, raw := range strings.FieldsFunc(body, func(character rune) bool {
		return character == '\n' || character == '\r'
	}) {
		line := strings.TrimSpace(raw)
		label := strings.TrimSpace(strings.TrimLeft(line, "-•· "))
		if label == "대상 가상자산" || label == "대상 디지털 자산" {
			collecting = true
			continue
		}
		if !collecting || line == "" {
			continue
		}
		assets := notice.TitleAssets(line)
		if len(assets) == 0 {
			if len(result) > 0 {
				break
			}
			continue
		}
		result = append(result, assets...)
	}
	return normalizedNoticeQuestionAssets(result)
}

func noticeQuestionExplicitAssets(question string, known map[string]struct{}) []string {
	upper := strings.ToUpper(question)
	result := []string{}
	for asset := range known {
		for offset := 0; offset <= len(upper)-len(asset); {
			index := strings.Index(upper[offset:], asset)
			if index < 0 {
				break
			}
			index += offset
			leftOK := index == 0 || !isNoticeQuestionASCIIAlphaNumeric(upper[index-1])
			right := index + len(asset)
			rightOK := right == len(upper) || !isNoticeQuestionASCIIAlphaNumeric(upper[right])
			if leftOK && rightOK {
				result = append(result, asset)
				break
			}
			offset = index + len(asset)
		}
	}
	sort.Strings(result)
	return result
}

func isNoticeQuestionASCIIAlphaNumeric(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func domesticNoticeQuestionStyleExamples(items []notice.Item, rows []domesticNoticeEvidence, question string, now time.Time) []domesticNoticeStyleEvidence {
	preferred := fallbackNoticeMatches(rows, question, now.UnixMilli())
	preferredIDs := make(map[string]struct{}, len(preferred))
	for _, item := range preferred {
		preferredIDs[item.SourceID] = struct{}{}
	}
	rowByID := make(map[string]domesticNoticeEvidence, len(rows))
	for _, row := range rows {
		rowByID[row.SourceID] = row
	}
	intent := noticeQuestionIntentFor(question)
	result := make([]domesticNoticeStyleEvidence, 0, noticeQuestionStyleLimit)
	seen := map[string]struct{}{}
	seenSignature := map[string]struct{}{}
	appendMatching := func(limit int, eligible func(string, domesticNoticeEvidence) bool) {
		startCount := len(result)
		for _, item := range items {
			if len(result) == noticeQuestionStyleLimit || (limit > 0 && len(result)-startCount == limit) {
				break
			}
			if item.Exchange != "bithumb" {
				continue
			}
			sourceID := noticeQuestionSourceID(item.Exchange, item.NoticeID)
			if _, duplicate := seen[sourceID]; duplicate {
				continue
			}
			row, exists := rowByID[sourceID]
			if !exists || !eligible(sourceID, row) {
				continue
			}
			body := item.OriginalBodyText
			if strings.TrimSpace(body) == "" {
				body = item.BodyExcerpt
			}
			if strings.TrimSpace(body) == "" {
				continue
			}
			title := item.OriginalTitle
			if strings.TrimSpace(title) == "" {
				title = item.Title
			}
			opening, closing := bithumbNoticeStyleExcerpts(body)
			compactBody := compactNoticeStyleBody(body, opening, closing)
			signatureBytes := sha256.Sum256([]byte(opening + "\x00" + compactBody + "\x00" + closing))
			signature := fmt.Sprintf("%x", signatureBytes)
			if _, duplicate := seenSignature[signature]; duplicate {
				continue
			}
			result = append(result, domesticNoticeStyleEvidence{
				SourceID: sourceID, Title: title,
				Body:           compactBody,
				OpeningExcerpt: opening, ClosingExcerpt: closing,
			})
			seen[sourceID] = struct{}{}
			seenSignature[signature] = struct{}{}
		}
	}
	requestedActions := bithumbWriterRequestedActions(question)
	if len(requestedActions) > 1 {
		for _, action := range requestedActions {
			before := len(result)
			appendMatching(1, func(sourceID string, row domesticNoticeEvidence) bool {
				_, preferred := preferredIDs[sourceID]
				return preferred && bithumbWriterActionForEvidence(row) == action
			})
			if len(result) == before {
				appendMatching(1, func(_ string, row domesticNoticeEvidence) bool {
					return bithumbWriterActionForEvidence(row) == action
				})
			}
		}
	}
	appendMatching(0, func(sourceID string, row domesticNoticeEvidence) bool {
		_, match := preferredIDs[sourceID]
		return match && noticeQuestionStyleCompatible(row.Title, row.Category, intent, question)
	})
	appendMatching(0, func(_ string, row domesticNoticeEvidence) bool {
		return noticeQuestionStyleCompatible(row.Title, row.Category, intent, question)
	})
	if intent == "" {
		appendMatching(0, func(_ string, _ domesticNoticeEvidence) bool { return true })
	}
	return result
}

func compactNoticeStyleBody(body, opening, closing string) string {
	body = strings.TrimSpace(body)
	for _, duplicate := range []string{opening, closing} {
		duplicate = strings.TrimSpace(duplicate)
		if duplicate != "" {
			body = strings.Replace(body, duplicate, "", 1)
		}
	}
	body = strings.TrimSpace(body)
	// Opening and closing are carried in their own role-labelled fields. The
	// body excerpt only needs enough middle structure to show headings, list
	// grammar and caution wording without repeating a full historical notice.
	return truncateNoticeQuestionText(body, 420)
}

func bithumbNoticeStyleExcerpts(body string) (string, string) {
	return noticewriter.ExtractStyleExcerpts("bithumb", body)
}

func noticeQuestionStyleCompatible(title, category string, intent noticeQuestionIntent, question string) bool {
	text := strings.ToLower(title)
	category = strings.ToUpper(strings.TrimSpace(category))
	if actions := bithumbWriterRequestedActions(question); bithumbWriterWantsStyleAnalysis(question) && len(actions) > 1 {
		actual := bithumbWriterActionForTitle(text, category)
		for _, action := range actions {
			if actual == action {
				return true
			}
		}
		return false
	}
	requestedAction := bithumbWriterActionForText(question)
	if requestedAction != "" {
		if requestedAction == bithumbWriterActionCautionDesignation ||
			requestedAction == bithumbWriterActionCautionExtension ||
			requestedAction == bithumbWriterActionCautionRelease ||
			requestedAction == bithumbWriterActionCautionUrge {
			return requestedAction == noticeQuestionCautionActionForTitle(text, category)
		}
		return requestedAction == bithumbWriterActionForTitle(text, category)
	}
	switch intent {
	case noticeQuestionIntentDepositStop, noticeQuestionIntentDepositResume:
		return category == "DEPOSIT_WITHDRAWAL" || containsNoticeQuestionAny(text, "입출금", "입금", "출금")
	case noticeQuestionIntentListing:
		return category == "LISTING" || category == "MARKET_ADDITION"
	case noticeQuestionIntentDelisting:
		return category == "DELISTING"
	case noticeQuestionIntentCaution:
		if category == "DELISTING" || containsNoticeQuestionAny(text, "거래지원 종료", "거래지원종료", "상장폐지") {
			return false
		}
		return category == "CAUTION"
	case noticeQuestionIntentEvent:
		return category == "EVENT" || category == "AIRDROP"
	case noticeQuestionIntentMaintenance:
		return category == "MAINTENANCE"
	default:
		return true
	}
}

func noticeQuestionCautionActionForTitle(title, category string) bithumbWriterAction {
	text := strings.ToLower(strings.TrimSpace(title))
	category = strings.ToUpper(strings.TrimSpace(category))
	switch {
	case category == "DELISTING" || containsNoticeQuestionAny(text, "거래지원 종료", "거래지원종료", "상장폐지", "마켓 제거"):
		return ""
	case containsNoticeQuestionAny(text, "유의촉구", "유의 촉구"):
		return bithumbWriterActionCautionUrge
	case containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "해제"):
		return bithumbWriterActionCautionRelease
	case containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "연장"):
		return bithumbWriterActionCautionExtension
	case category == "CAUTION" || containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의"):
		return bithumbWriterActionCautionDesignation
	default:
		return ""
	}
}

func bithumbWriterActionForText(value string) bithumbWriterAction {
	return noticewriter.InferAction(value)
}

func bithumbWriterRequestedActions(value string) []bithumbWriterAction {
	text := strings.ToLower(strings.TrimSpace(value))
	result := []bithumbWriterAction{}
	appendAction := func(action bithumbWriterAction) {
		if action == "" {
			return
		}
		for _, existing := range result {
			if existing == action {
				return
			}
		}
		result = append(result, action)
	}

	if containsNoticeQuestionAny(text, "유의촉구", "유의 촉구") {
		appendAction(bithumbWriterActionCautionUrge)
	} else if containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "해제") {
		appendAction(bithumbWriterActionCautionRelease)
	} else if containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "연장") {
		appendAction(bithumbWriterActionCautionExtension)
	} else if containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") {
		appendAction(bithumbWriterActionCautionDesignation)
	}
	if containsNoticeQuestionAny(text, "거래지원 종료", "거래지원종료", "상장폐지", "마켓 제거") {
		appendAction(bithumbWriterActionDelisting)
	}
	if containsNoticeQuestionAny(text, "입출금", "입금", "출금") {
		switch {
		case containsNoticeQuestionAny(text, "재개", "재개완료", "중단 완료", "중지 완료", "(완료)"):
			appendAction(bithumbWriterActionDepositResume)
		case containsNoticeQuestionAny(text, "중단", "중지", "차단"):
			appendAction(bithumbWriterActionDepositStop)
		}
	}
	return result
}

func bithumbWriterActionLabel(action bithumbWriterAction) string {
	return noticewriter.ActionLabel(action)
}

func bithumbWriterActionForTitle(title, category string) bithumbWriterAction {
	text := strings.ToLower(strings.TrimSpace(title))
	category = strings.ToUpper(strings.TrimSpace(category))
	switch {
	case category == "DELISTING" || containsNoticeQuestionAny(text, "거래지원 종료", "거래지원종료", "상장폐지", "마켓 제거"):
		return bithumbWriterActionDelisting
	case containsNoticeQuestionAny(text, "입출금", "입금", "출금") &&
		containsNoticeQuestionAny(text, "재개", "재개완료", "중단 완료", "중지 완료", "(완료)"):
		return bithumbWriterActionDepositResume
	case (strings.HasPrefix(category, "DEPOSIT_WITHDRAWAL") || containsNoticeQuestionAny(text, "입출금", "입금", "출금")) &&
		containsNoticeQuestionAny(text, "중단", "중지", "차단"):
		return bithumbWriterActionDepositStop
	case containsNoticeQuestionAny(text, "유의촉구", "유의 촉구"):
		return bithumbWriterActionCautionUrge
	case containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "해제"):
		return bithumbWriterActionCautionRelease
	case containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "연장"):
		return bithumbWriterActionCautionExtension
	case category == "CAUTION" || containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의"):
		return bithumbWriterActionCautionDesignation
	default:
		return ""
	}
}

func domesticNoticeMeasurementScope(evidence domesticNoticeQuestionEvidence, question string, now time.Time) *domesticNoticeVolumeScope {
	scope := &domesticNoticeVolumeScope{Status: "UNAVAILABLE", Assets: []string{}, Exchanges: domesticNoticeQueryExchanges(evidence), QuerySourceIDs: noticeQuestionEvidenceSourceIDs(evidence.Notices)}
	if len(scope.Exchanges) == 0 {
		scope.Limitation = "조회 대상 거래소가 없어 거래대금을 측정하지 않았습니다."
		return scope
	}
	if evidence.Retrieval.InvalidCalendarDate {
		scope.Limitation = "날짜가 유효하지 않아 거래대금을 조회하지 않았습니다."
		return scope
	}
	assets := map[string]struct{}{}
	for _, row := range evidence.Notices {
		for _, asset := range domesticNoticeEvidencePrimaryAssets(row) {
			assets[asset] = struct{}{}
		}
	}
	if requested := evidence.Retrieval.Assets; len(requested) > 0 {
		for asset := range assets {
			if !slices.Contains(requested, asset) {
				delete(assets, asset)
			}
		}
	}
	scope.Assets = sortedNoticeQuestionSet(assets)
	if len(scope.Assets) == 0 {
		scope.Limitation = "검색한 공지에서 측정할 자산을 확인하지 못해 거래대금을 조회하지 않았습니다."
		return scope
	}
	if len(scope.Assets) > noticeQuestionAssetLimit {
		scope.Limitation = "검색된 측정 자산 수가 한 번의 조회 한도를 넘습니다. 자산을 좁혀 다시 질문해 주세요."
		return scope
	}
	start, end := now.Add(-time.Duration(evidence.Retrieval.WindowDays)*24*time.Hour), now
	if evidence.Retrieval.WindowDays <= 0 {
		start = now.Add(-noticeQuestionHistoryDays * 24 * time.Hour)
	}
	var err error
	if value := evidence.Retrieval.WindowStartKST; value != "" {
		start, err = time.Parse(time.RFC3339, value)
		if err != nil {
			scope.Limitation = "공지 검색 기간을 거래대금 조회 기간으로 변환할 수 없습니다."
			return scope
		}
	}
	if value := evidence.Retrieval.WindowEndKSTExclusive; value != "" {
		end, err = time.Parse(time.RFC3339, value)
		if err != nil {
			scope.Limitation = "공지 검색 종료 시각을 확인할 수 없습니다."
			return scope
		}
		if end.After(now) {
			end = now
		}
	}
	if !start.Before(end) || end.Sub(start) > noticeQuestionHistoryDays*24*time.Hour {
		scope.Limitation = "요청 기간 전체의 거래대금을 현재 조회 범위에서 측정할 수 없습니다."
		return scope
	}
	start, end = start.In(seoulLocation), end.In(seoulLocation)
	scope.WindowStartKST, scope.WindowEndKSTExclusive = start.In(seoulLocation).Format(time.RFC3339), end.In(seoulLocation).Format(time.RFC3339)
	for cursor := start; cursor.Before(end); {
		scope.DayCount++
		cursor = time.Date(cursor.Year(), cursor.Month(), cursor.Day()+1, 0, 0, 0, 0, seoulLocation)
	}
	scope.Status = "READY"
	if evidence.Retrieval.Truncated {
		scope.Limitation = "거래대금 대상은 전달된 공지에서 확인한 자산이며 검색 전체의 모든 자산을 포함한다고 단정할 수 없습니다."
	}
	return scope
}

func (s *Service) domesticNoticeScopedDailyVolumes(ctx context.Context, scope domesticNoticeVolumeScope) []domesticNoticeVolumeDay {
	start, startErr := time.Parse(time.RFC3339, scope.WindowStartKST)
	end, endErr := time.Parse(time.RFC3339, scope.WindowEndKSTExclusive)
	if startErr != nil || endErr != nil {
		return nil
	}
	result := make([]domesticNoticeVolumeDay, 0, scope.DayCount)
	for cursor := start.In(seoulLocation); cursor.Before(end); {
		next := time.Date(cursor.Year(), cursor.Month(), cursor.Day()+1, 0, 0, 0, 0, seoulLocation)
		if next.After(end) {
			next = end
		}
		row := domesticNoticeVolumeDay{Day: cursor.Format(time.DateOnly), IntervalStartKST: cursor.Format(time.RFC3339), IntervalEndKSTExclusive: next.Format(time.RFC3339), CoverageScope: "ALL_DOMESTIC_TRANSPORT", Assets: []domesticNoticeVolumeAsset{}}
		calendar, err := s.domesticNoticeCalendarInterval(ctx, cursor, next)
		if err != nil {
			row.Status, row.Error = "UNAVAILABLE", truncateNoticeQuestionText(err.Error(), 300)
		} else {
			domesticNoticeProjectCalendarVolume(calendar, scope, &row)
		}
		result = append(result, row)
		if ctx.Err() != nil {
			break
		}
		cursor = next
	}
	return result
}

func domesticNoticeProjectCalendarVolume(calendar CalendarDayResponse, scope domesticNoticeVolumeScope, row *domesticNoticeVolumeDay) {
	row.Status, row.CoverageStatus = calendar.Status, calendar.Coverage.Status
	row.CoverageSec, row.RequiredSec = calendar.Coverage.CoverageSec, calendar.Coverage.RequiredSec
	rows := make([]CalendarMarketRow, 0, len(calendar.Rows))
	for _, market := range calendar.Rows {
		if slices.Contains(scope.Assets, market.Base) && slices.Contains(scope.Exchanges, string(market.Exchange)) {
			rows = append(rows, market)
			if market.ObservedInInterval {
				row.ObservedMarketCount++
			}
		}
	}
	assets, issues := calendarAssetRowsWithIssues(rows)
	for _, asset := range assets {
		if asset.ObservedMarketCount <= 0 || len(issues[calendarAssetLabel(asset.Scope, asset.Asset)]) > 0 || asset.AggregationStatus != calendarAggregationAvailable {
			continue
		}
		row.Assets = append(row.Assets, domesticNoticeVolumeAsset{Asset: asset.Asset, VolumeKRW: asset.VolumeKRW, TradeCount: asset.TradeCount, MarketCount: asset.MarketCount, ObservedCount: asset.ObservedMarketCount, Exchanges: append([]string(nil), asset.Exchanges...)})
	}
}

func (s *Service) domesticNoticeCalendarInterval(ctx context.Context, start, end time.Time) (CalendarDayResponse, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.DomesticNoticeCalendarInterval(ctx, start, end)
	}
	if s.runtime == nil || s.registry == nil {
		return CalendarDayResponse{}, fmt.Errorf("거래대금 관측 자료를 읽을 수 없습니다")
	}
	startSec, endSec := start.Unix(), end.Unix()-1
	read, err := s.runtime.atomicRead(ctx, []aggregate.IntervalRequest{{Start: domain.UnixSecond(startSec - 1), End: domain.UnixSecond(endSec)}}, []coverageInterval{{start: startSec, end: endSec}})
	if err != nil {
		return CalendarDayResponse{}, err
	}
	if len(read.intervals) != 1 || !read.intervals[0].OK || len(read.coverage) != 1 {
		return CalendarDayResponse{}, fmt.Errorf("요청 구간의 관측 자료를 확인할 수 없습니다")
	}
	return buildCalendarDay(start.Format(time.DateOnly), startSec, endSec, "KRW", filterAnalysisMarkets(s.registry.Universe().Markets(), "KRW"), read.intervals[0].Values, read.coverage[0]), nil
}

func noticeQuestionNeedsVolume(persona, question string) bool {
	if persona == noticePersonaVolumeAnalyst {
		return true
	}
	text := strings.ToLower(question)
	for _, keyword := range []string{"거래량", "거래대금", "볼륨", "volume"} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func buildDomesticNoticeQuestionFallback(evidence domesticNoticeQuestionEvidence, persona, question string) marketanalysis.NoticeQuestionAnswer {
	if evidence.Retrieval.InvalidCalendarDate {
		return marketanalysis.NoticeQuestionAnswer{
			Answer: "입력한 날짜가 달력에 없습니다. 날짜를 확인해 다시 질문해 주세요.",
			Source: "SERVER_NOTICE_CLARIFICATION", AsOfMS: evidence.AsOfMS, Persona: persona,
		}
	}
	if fallbackNoticeQuestionWantsCount(question) && evidence.Retrieval.MatchedNoticeCount == 0 && len(domesticNoticeIncompleteSources(evidence)) > 0 {
		return marketanalysis.NoticeQuestionAnswer{Answer: "현재 저장된 검색 결과에서는 해당 공지를 확인하지 못했습니다. 공지 수집이 완료되지 않아 전체 건수를 확정할 수 없습니다.", Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona}
	}
	if persona == noticePersonaBithumbWriter {
		ids := bithumbWriterAnswerSourceIDs(evidence, question)
		verifiedAssets := append([]string(nil), evidence.WriterVerifiedAssets...)
		factSourceIDs := append([]string(nil), evidence.WriterVerifiedSourceIDs...)
		if len(verifiedAssets) == 0 && len(factSourceIDs) == 0 {
			verifiedAssets, factSourceIDs = bithumbWriterVerifiedDraftAssets(evidence, question)
		}
		if len(factSourceIDs) > 0 {
			ids = append(factSourceIDs, ids...)
		}
		answer := bithumbWriterFallbackDraftWithEvidence(question, verifiedAssets, evidence.StyleExamples)
		keyPoints := []string{"요청한 조치의 선언형 공지 계약으로 구조를 적용하고, 검증되지 않은 사실 항목은 자리표시자로 남겼습니다."}
		caveat := "이 문안은 공식 게시 전 검토용 초안이며 실제 빗썸 공지가 아닙니다."
		if len(verifiedAssets) > 0 {
			keyPoints = append(keyPoints, "대상 티커는 사용자 질문과 같은 조치의 빗썸 공식 공지에서 교차 확인했습니다.")
			caveat += " 대상 티커만 질문과 같은 조치의 빗썸 공식 공지에서 교차 확인했으며 나머지 사실 항목은 확인이 필요합니다."
		}
		if bithumbWriterWantsStyleAnalysis(question) {
			answer = bithumbWriterFallbackAnalysis(question)
			if actions := bithumbWriterRequestedActions(question); len(actions) > 1 {
				labels := make([]string, 0, len(actions))
				for _, action := range actions {
					labels = append(labels, bithumbWriterActionLabel(action))
				}
				keyPoints = []string{
					strings.Join(labels, " · ") + " 문체를 서로 다른 구조로 비교했습니다.",
					"각 조치의 제목·정보 순서·유의사항을 한 공지처럼 섞지 않습니다.",
					"고유 문장을 복사하지 않고 공식 예시의 구조만 일반화합니다.",
				}
			} else {
				keyPoints = []string{
					"짧은 공식 안내말 뒤에 핵심 조치를 먼저 제시합니다.",
					"세부 정보와 유의사항을 표제·목록으로 분리합니다.",
					"지정·연장·해제·유의촉구·거래지원 종료를 서로 다른 조치로 표현합니다.",
				}
			}
			caveat = "공식 예시에서 문체와 구조만 일반화한 분석입니다. 현재 정책, 사실 정확성 또는 공식 게시 적합성을 보증하지 않습니다."
		}
		return marketanalysis.NoticeQuestionAnswer{
			Answer: answer, KeyPoints: keyPoints, Caveat: caveat,
			Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona,
			SourceNoticeIDs: ids, Sources: []marketanalysis.NoticeQuestionSource{},
		}
	}
	if noticeQuestionIntentFor(question) == noticeQuestionIntentDepositStatus {
		return buildDomesticDepositStatusFallback(evidence, persona, question)
	}
	if noticeQuestionIntentFor(question) == noticeQuestionIntentImportant {
		return buildDomesticImportantChangesFallback(evidence, persona, question)
	}
	if noticeQuestionIntentFor(question) == noticeQuestionIntentEvent && fallbackNoticeQuestionWantsExchangeGroups(question) {
		return buildDomesticGroupedEventFallback(evidence, persona, question)
	}
	matched := fallbackNoticeMatches(evidence.Notices, question, evidence.AsOfMS)
	if len(evidence.ContextSourceIDs) > 0 && noticeQuestionIntentFor(question) == "" {
		matched = noticeQuestionRowsForSourceIDs(evidence.Notices, evidence.ContextSourceIDs)
	}
	lines := make([]string, 0, min(len(matched), 10))
	ids := make([]string, 0, min(len(matched), 10))
	for _, item := range matched {
		when := fallbackNoticeDateLabel(item)
		assetText := ""
		if len(item.Assets) > 0 {
			assetText = " · " + strings.Join(item.Assets, ", ")
		}
		lines = append(lines, fmt.Sprintf("%s %s · %s%s · %s", when, strings.ToUpper(item.Exchange), fallbackNoticeQuestionCategoryLabel(item, question, evidence.AsOfMS), assetText, item.Title))
		ids = append(ids, item.SourceID)
		if len(lines) == 10 {
			break
		}
	}
	caveat := fallbackNoticeQuestionCaveat(question)
	displayedCount := len(lines)
	if fallbackNoticeQuestionWantsExchangeGroups(question) {
		displayedCount = len(matched)
	}
	if evidence.Retrieval.Truncated {
		if noticeQuestionWantsCautionStatusBreakdown(question) && !evidence.Retrieval.ContextApplied {
			caveat += fmt.Sprintf(" 전체 일치 %d건 중 상세 목록은 상태별로 선별한 %d건만 표시합니다.", evidence.Retrieval.MatchedNoticeCount, displayedCount)
		} else {
			caveat += fmt.Sprintf(" 전체 일치 %d건 중 상세 목록은 상위 %d건 후보만 표시합니다.", evidence.Retrieval.MatchedNoticeCount, displayedCount)
		}
	}
	if len(ids) < displayedCount {
		caveat += fmt.Sprintf(" 공식 공지 링크는 표시 목록 %d건 중 상위 %d건만 제공합니다.", displayedCount, len(ids))
	}
	if fallbackNoticeQuestionWantsCount(question) {
		count := evidence.Retrieval.MatchedNoticeCount
		if count == 0 && len(domesticNoticeIncompleteSources(evidence)) > 0 {
			return marketanalysis.NoticeQuestionAnswer{Answer: "현재 저장된 검색 결과에서는 해당 공지를 확인하지 못했습니다. 공지 수집이 완료되지 않아 전체 건수를 확정할 수 없습니다.", Caveat: caveat, Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona}
		}
		keyPoints := []string{fmt.Sprintf("전체 일치 %d건", count)}
		if len(evidence.Retrieval.ExchangeNoticeCounts) > 0 {
			// The answer already states the total. Reserve the bounded detail
			// list for every queried exchange, including observed zero matches.
			keyPoints = make([]string, 0, len(evidence.Retrieval.ExchangeNoticeCounts))
			for _, count := range evidence.Retrieval.ExchangeNoticeCounts {
				keyPoints = append(keyPoints, fmt.Sprintf("%s %d건", strings.ToUpper(count.Exchange), count.Count))
			}
		} else if !evidence.Retrieval.Truncated {
			keyPoints = append(keyPoints, fallbackNoticeExchangeCounts(matched)...)
		}
		return marketanalysis.NoticeQuestionAnswer{
			Answer:    fmt.Sprintf("현재 저장된 공지 중 검색 조건에 일치하는 항목은 %d건입니다.", count),
			KeyPoints: keyPoints, Caveat: caveat,
			Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona,
			SourceNoticeIDs: ids, Sources: []marketanalysis.NoticeQuestionSource{},
		}
	}
	answer := "현재 국내 공식 공지 근거에서 질문과 직접 연결되는 항목을 찾지 못했습니다."
	keyPoints := fallbackNoticeExchangeCounts(matched)
	if len(lines) > 0 {
		if fallbackNoticeQuestionWantsExchangeGroups(question) {
			answer = "현재 국내 공식 공지에서 확인되는 항목을 거래소별로 정리했습니다.\n\n" + fallbackNoticeGroupedLines(matched, question, evidence.AsOfMS)
		} else {
			answer = "현재 국내 공식 공지에서 확인되는 항목은 다음과 같습니다.\n\n- " + strings.Join(lines, "\n- ")
		}
	}
	if noticeQuestionIntentFor(question) == noticeQuestionIntentEvent && len(matched) > 0 {
		statusPoints := fallbackNoticeEventStatusCounts(matched)
		answer = "상태 요약(표시 목록): " + strings.Join(statusPoints, " · ") + "\n\n" + answer
		keyPoints = append(keyPoints, statusPoints...)
	}
	if noticeQuestionIntentFor(question) == noticeQuestionIntentCaution && len(matched) > 0 {
		statusPoints := fallbackNoticeCautionStatusCounts(matched)
		statusScope := "표시 목록"
		exactTotal := evidence.Retrieval.CautionDesignationCount + evidence.Retrieval.CautionExtensionCount + evidence.Retrieval.CautionReleaseCount
		if noticeQuestionWantsCautionStatusBreakdown(question) && !evidence.Retrieval.ContextApplied &&
			exactTotal > 0 && exactTotal == evidence.Retrieval.MatchedNoticeCount {
			statusPoints = []string{
				fmt.Sprintf("지정 %d건", evidence.Retrieval.CautionDesignationCount),
				fmt.Sprintf("연장 %d건", evidence.Retrieval.CautionExtensionCount),
				fmt.Sprintf("해제 %d건", evidence.Retrieval.CautionReleaseCount),
			}
			statusScope = "전체 일치"
		}
		answer = "상태 요약(" + statusScope + "): " + strings.Join(statusPoints, " · ") + "\n\n" + answer
		keyPoints = append(keyPoints, statusPoints...)
	}
	if len(evidence.DailyAssetVolumes) > 0 {
		volumeLines := fallbackNoticeVolumeLines(evidence.DailyAssetVolumes)
		if len(volumeLines) > 0 {
			answer += "\n\n일별 국내 KRW 거래대금(서버 관측값):\n- " + strings.Join(volumeLines, "\n- ")
		}
		answer += "\n\n자료 완전성 상태가 불완전한 날은 표시 금액을 확정값으로 보거나, 누락된 자산을 0원으로 해석할 수 없습니다."
	}
	return marketanalysis.NoticeQuestionAnswer{
		Answer: answer, KeyPoints: keyPoints, Caveat: caveat,
		Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona,
		SourceNoticeIDs: ids, Sources: []marketanalysis.NoticeQuestionSource{},
	}
}

func buildDomesticDepositStatusFallback(
	evidence domesticNoticeQuestionEvidence,
	persona, question string,
) marketanalysis.NoticeQuestionAnswer {
	suspended := make([]domesticNoticeEvidence, 0, len(evidence.Notices))
	resumed := make([]domesticNoticeEvidence, 0, len(evidence.Notices))
	for _, item := range evidence.Notices {
		switch {
		case domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositResume):
			resumed = append(resumed, item)
		case domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositStop):
			suspended = append(suspended, item)
		}
	}
	section := func(label string, items []domesticNoticeEvidence) string {
		if len(items) == 0 {
			return label + ": 표시할 공식 공지 없음"
		}
		lines := make([]string, 0, len(items))
		for _, item := range items {
			lines = append(lines, fallbackNoticeLineForQuestion(item, question, evidence.AsOfMS))
		}
		return label + "(최신 " + fmt.Sprintf("%d건", len(items)) + "):\n- " + strings.Join(lines, "\n- ")
	}
	suspensionCount := evidence.Retrieval.DepositSuspensionCount
	resumptionCount := evidence.Retrieval.DepositResumptionCount
	windowLabel := noticeQuestionWindowLabel(evidence.Retrieval)
	answer := fmt.Sprintf(
		"%s의 공식 공지 상태 요약: 중단 공지 %d건 · 재개·완료 공지 %d건\n\n%s\n\n%s",
		windowLabel, suspensionCount, resumptionCount,
		section("중단 공지", suspended), section("재개·완료 공지", resumed),
	)
	if noticeQuestionWantsCurrentDepositState(question) {
		answer = domesticNoticeLatestDepositStateSummary(evidence.Notices, question, evidence.AsOfMS, windowLabel) +
			"\n\n" + answer
	}
	ids := make([]string, 0, len(evidence.Notices))
	for _, item := range evidence.Notices {
		ids = append(ids, item.SourceID)
	}
	caveat := fallbackNoticeQuestionCaveat(question)
	if evidence.Retrieval.Truncated {
		caveat += fmt.Sprintf(
			" 전체 일치 %d건 중 각 상태의 최신 공지를 합쳐 %d건만 표시합니다.",
			evidence.Retrieval.MatchedNoticeCount, len(evidence.Notices),
		)
	}
	return marketanalysis.NoticeQuestionAnswer{
		Answer: answer,
		KeyPoints: []string{
			fmt.Sprintf("중단 공지 %d건", suspensionCount),
			fmt.Sprintf("재개·완료 공지 %d건", resumptionCount),
		},
		Caveat: caveat, Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona,
		SourceNoticeIDs: ids, Sources: []marketanalysis.NoticeQuestionSource{},
	}
}

func noticeQuestionWantsCurrentDepositState(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	return noticeQuestionIntentFor(question) == noticeQuestionIntentDepositStatus &&
		containsNoticeQuestionAny(text,
			"지금", "현재", "가능한가", "가능한지", "가능해", "가능 여부", "가능여부",
		)
}

func domesticNoticeLatestDepositStateSummary(
	items []domesticNoticeEvidence,
	question string,
	asOfMS int64,
	windowLabel string,
) string {
	latest := make(map[string]domesticNoticeEvidence, len(domesticNoticeExchangeOrder))
	for _, item := range items {
		exchange := strings.ToLower(strings.TrimSpace(item.Exchange))
		if !notice.IsDomesticExchange(exchange) {
			continue
		}
		current, found := latest[exchange]
		if !found || domesticNoticeEvidenceAnchorMS(item) > domesticNoticeEvidenceAnchorMS(current) {
			latest[exchange] = item
		}
	}
	lines := make([]string, 0, len(domesticNoticeExchangeOrder))
	for _, exchange := range domesticNoticeExchangeOrder {
		item, found := latest[exchange]
		if !found {
			lines = append(lines, fmt.Sprintf("- %s · %s 내 직접 근거 없음", strings.ToUpper(exchange), windowLabel))
			continue
		}
		status := "상태 판별 불가"
		switch {
		case domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositResume):
			status = "재개·완료"
		case fallbackNoticeDepositSuspensionIsFutureOnly(item, asOfMS):
			status = "중단 예정"
		case domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositStop):
			status = "중단(이후 재개 공지 미확인)"
		}
		lines = append(lines, fmt.Sprintf(
			"- %s · %s · %s · %s",
			strings.ToUpper(exchange), status, fallbackNoticeDateLabel(item), item.Title,
		))
	}
	return "거래소별 가장 최신 공식 공지 상태(실시간 서비스 상태의 확정값 아님):\n" + strings.Join(lines, "\n")
}

func buildDomesticImportantChangesFallback(
	evidence domesticNoticeQuestionEvidence,
	persona, question string,
) marketanalysis.NoticeQuestionAnswer {
	trade, risk, operations := splitImportantNoticeRows(evidence.Notices)
	section := func(label string, items []domesticNoticeEvidence) string {
		if len(items) == 0 {
			return label + ": 표시할 공식 공지 없음"
		}
		groups := groupDomesticNoticePresentationRows(items, false)
		lines := make([]string, 0, len(groups))
		for _, group := range groups {
			lines = append(lines, fallbackNoticePresentationLine(group, true))
		}
		return fmt.Sprintf("%s(표시 %d건) · 중복 통합 %d개 변화:\n- %s", label, len(items), len(groups), strings.Join(lines, "\n- "))
	}
	windowLabel := noticeQuestionWindowLabel(evidence.Retrieval)
	answer := fmt.Sprintf(
		"%s 운영상 주요 변화 후보: 거래지원 %d건 · 거래유의·보안 %d건 · 입출금·점검 %d건\n\n%s\n\n%s\n\n%s",
		windowLabel, evidence.Retrieval.ImportantTradingCount, evidence.Retrieval.ImportantRiskCount,
		evidence.Retrieval.ImportantOperationCount,
		section("거래지원 변화", trade), section("거래유의·보안", risk), section("입출금·점검", operations),
	)
	ids := make([]string, 0, len(evidence.Notices))
	for _, item := range evidence.Notices {
		ids = append(ids, item.SourceID)
	}
	caveat := fallbackNoticeQuestionCaveat(question)
	caveat += " 본문의 같은 자산·조치·날짜 묶음은 하나의 변화로 합쳤지만 집계 건수와 공식 출처는 거래소별 공지 단위로 유지합니다."
	if evidence.Retrieval.Truncated {
		caveat += fmt.Sprintf(
			" 전체 주요 변화 후보 %d건 중 세 범주를 균형 있게 합쳐 %d건만 표시합니다.",
			evidence.Retrieval.MatchedNoticeCount, len(evidence.Notices),
		)
	}
	return marketanalysis.NoticeQuestionAnswer{
		Answer: answer,
		KeyPoints: []string{
			fmt.Sprintf("거래지원 %d건", evidence.Retrieval.ImportantTradingCount),
			fmt.Sprintf("거래유의·보안 %d건", evidence.Retrieval.ImportantRiskCount),
			fmt.Sprintf("입출금·점검 %d건", evidence.Retrieval.ImportantOperationCount),
		},
		Caveat: caveat, Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona,
		SourceNoticeIDs: ids, Sources: []marketanalysis.NoticeQuestionSource{},
	}
}

type domesticNoticePresentationGroup struct {
	Items []domesticNoticeEvidence
}

func groupDomesticNoticePresentationRows(items []domesticNoticeEvidence, eventMode bool) []domesticNoticePresentationGroup {
	groups := make([]domesticNoticePresentationGroup, 0, len(items))
	byKey := make(map[string]int, len(items))
	for _, item := range items {
		key := domesticNoticePresentationKey(item, eventMode)
		if index, exists := byKey[key]; exists {
			groups[index].Items = append(groups[index].Items, item)
			continue
		}
		byKey[key] = len(groups)
		groups = append(groups, domesticNoticePresentationGroup{Items: []domesticNoticeEvidence{item}})
	}
	return groups
}

func domesticNoticePresentationKey(item domesticNoticeEvidence, eventMode bool) string {
	assets := normalizedNoticeQuestionAssets(item.Assets)
	if len(assets) == 0 {
		return "source:" + item.SourceID
	}
	action := domesticNoticePresentationAction(item, eventMode)
	period := fallbackNoticeDateLabelForAnchor(domesticNoticeEvidenceAnchorMS(item))
	if eventMode {
		if marker := domesticNoticeEventPeriodMarker(item.Title); marker != "" {
			period = marker
		}
	}
	return action + "|" + strings.Join(assets, ",") + "|" + period
}

func domesticNoticePresentationAction(item domesticNoticeEvidence, eventMode bool) string {
	category := strings.ToUpper(strings.TrimSpace(item.Category))
	title := strings.ToLower(item.Title)
	if len(item.Actions) > 0 {
		if eventMode && notice.HasAction(item.Actions, "EVENT") {
			switch {
			case notice.HasAction(item.Actions, "EVENT", "END", "COMPLETE"):
				return "EVENT:ended"
			case notice.HasAction(item.Actions, "EVENT", "EXTEND"):
				return "EVENT:extended"
			default:
				return "EVENT:active"
			}
		}
		switch {
		case notice.HasAction(item.Actions, "TRADING_SUPPORT", "START", "ADD"):
			return "listing"
		case notice.HasAction(item.Actions, "TRADING_SUPPORT", "END"):
			return "delisting"
		case notice.HasAction(item.Actions, "CAUTION", "RELEASE"):
			return "caution:released"
		case notice.HasAction(item.Actions, "CAUTION", "EXTEND"):
			return "caution:extended"
		case notice.HasAction(item.Actions, "CAUTION", "URGE"):
			return "caution:urging"
		case notice.HasAction(item.Actions, "CAUTION", "DESIGNATE"):
			return "caution:designated"
		case notice.HasAction(item.Actions, "DEPOSIT_WITHDRAWAL", "RESUME", "COMPLETE"):
			return "deposit:resumed"
		case notice.HasAction(item.Actions, "DEPOSIT_WITHDRAWAL", "SUSPEND"):
			return "deposit:suspended"
		case notice.HasAction(item.Actions, "SECURITY"):
			return "security"
		case notice.HasAction(item.Actions, "MAINTENANCE"):
			return "maintenance"
		}
	}
	if eventMode {
		switch {
		case containsNoticeQuestionAny(title, "종료", "결과 안내", "당첨자"):
			return category + ":ended"
		case strings.Contains(title, "연장"):
			return category + ":extended"
		default:
			return category + ":active"
		}
	}
	switch {
	case category == "LISTING" || category == "MARKET_ADDITION":
		return "listing"
	case category == "DELISTING" || category == "TRADING_SUPPORT_TERMINATION":
		return "delisting"
	case category == "CAUTION":
		switch {
		case strings.Contains(title, "연장"):
			return "caution:extended"
		case strings.Contains(title, "해제"):
			return "caution:released"
		case containsNoticeQuestionAny(title, "유의촉구", "유의 촉구"):
			return "caution:urging"
		default:
			return "caution:designated"
		}
	case category == "SECURITY" || category == "SECURITY_INCIDENT":
		return "security"
	case strings.HasPrefix(category, "DEPOSIT_WITHDRAWAL"):
		if domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositResume) {
			return "deposit:resumed"
		}
		return "deposit:suspended"
	case category == "MAINTENANCE":
		return "maintenance"
	default:
		return category
	}
}

func domesticNoticeEventPeriodMarker(title string) string {
	compact := strings.ReplaceAll(strings.ToLower(title), " ", "")
	for month := 1; month <= 12; month++ {
		for week := 1; week <= 5; week++ {
			marker := fmt.Sprintf("%d월%d주", month, week)
			if strings.Contains(compact, marker) {
				return marker
			}
		}
	}
	return ""
}

func fallbackNoticePresentationLine(group domesticNoticePresentationGroup, includeExchanges bool) string {
	if len(group.Items) == 0 {
		return ""
	}
	if len(group.Items) == 1 {
		if includeExchanges {
			return fallbackNoticeLine(group.Items[0])
		}
		item := group.Items[0]
		assetText := ""
		if len(item.Assets) > 0 {
			assetText = " · " + strings.Join(item.Assets, ", ")
		}
		return fmt.Sprintf("%s · %s%s · %s", fallbackNoticeDateLabel(item), noticeQuestionCategoryLabel(item.Category), assetText, item.Title)
	}
	first := group.Items[0]
	parts := []string{fallbackNoticePresentationDateRange(group.Items)}
	if includeExchanges {
		parts = append(parts, strings.Join(fallbackNoticePresentationExchanges(group.Items), "/"))
	}
	parts = append(parts, noticeQuestionCategoryLabel(first.Category))
	if len(first.Assets) > 0 {
		parts = append(parts, strings.Join(first.Assets, ", "))
	}
	parts = append(parts, fmt.Sprintf("동일 자산·조치 공식 공지 %d건", len(group.Items)))
	return strings.Join(parts, " · ")
}

func fallbackNoticePresentationDateRange(items []domesticNoticeEvidence) string {
	minimum, maximum := int64(0), int64(0)
	for _, item := range items {
		anchor := domesticNoticeEvidenceAnchorMS(item)
		if anchor <= 0 {
			continue
		}
		if minimum == 0 || anchor < minimum {
			minimum = anchor
		}
		if anchor > maximum {
			maximum = anchor
		}
	}
	left, right := fallbackNoticeDateLabelForAnchor(minimum), fallbackNoticeDateLabelForAnchor(maximum)
	if left == right || left == "" {
		return right
	}
	return left + "~" + right
}

func fallbackNoticeDateLabelForAnchor(value int64) string {
	if value <= 0 {
		return "시각 미확인"
	}
	return time.UnixMilli(value).In(seoulLocation).Format("2006-01-02")
}

func fallbackNoticePresentationExchanges(items []domesticNoticeEvidence) []string {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if exchange := strings.ToLower(strings.TrimSpace(item.Exchange)); exchange != "" {
			seen[exchange] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for _, exchange := range domesticNoticeExchangeOrder {
		if _, exists := seen[exchange]; exists {
			result = append(result, strings.ToUpper(exchange))
		}
	}
	return result
}

func buildDomesticGroupedEventFallback(
	evidence domesticNoticeQuestionEvidence,
	persona, question string,
) marketanalysis.NoticeQuestionAnswer {
	matched := fallbackNoticeMatches(evidence.Notices, question, evidence.AsOfMS)
	groups := groupDomesticNoticePresentationRows(matched, true)
	shared := make([]domesticNoticePresentationGroup, 0)
	byExchange := make(map[string][]domesticNoticePresentationGroup)
	for _, group := range groups {
		exchanges := fallbackNoticePresentationExchanges(group.Items)
		if len(exchanges) > 1 {
			shared = append(shared, group)
			continue
		}
		if len(exchanges) == 1 {
			byExchange[strings.ToLower(exchanges[0])] = append(byExchange[strings.ToLower(exchanges[0])], group)
		}
	}
	sections := make([]string, 0, 6)
	if len(shared) > 0 {
		lines := make([]string, 0, len(shared))
		noticeCount := 0
		for _, group := range shared {
			lines = append(lines, "- "+fallbackNoticePresentationLine(group, true))
			noticeCount += len(group.Items)
		}
		sections = append(sections, fmt.Sprintf("거래소 공동 공지 (%d건) · 중복 통합 %d개 변화\n%s", noticeCount, len(shared), strings.Join(lines, "\n")))
	}
	for _, exchange := range domesticNoticeExchangeOrder {
		venueGroups := byExchange[exchange]
		if len(venueGroups) == 0 {
			continue
		}
		lines := make([]string, 0, len(venueGroups))
		noticeCount := 0
		for _, group := range venueGroups {
			lines = append(lines, "- "+fallbackNoticePresentationLine(group, false))
			noticeCount += len(group.Items)
		}
		sections = append(sections, fmt.Sprintf("%s (%d건) · 중복 통합 %d개 변화\n%s", strings.ToUpper(exchange), noticeCount, len(venueGroups), strings.Join(lines, "\n")))
	}
	statusPoints := fallbackNoticeEventStatusCounts(matched)
	answer := "상태 요약(공식 공지 기준): " + strings.Join(statusPoints, " · ") +
		fmt.Sprintf("\n표시 요약: 공식 공지 %d건 → 중복 통합 %d개 변화\n\n", len(matched), len(groups)) + strings.Join(sections, "\n\n")
	ids := make([]string, 0, min(len(matched), noticeQuestionVerifiedSourceLimit))
	for _, item := range matched {
		ids = append(ids, item.SourceID)
		if len(ids) == noticeQuestionVerifiedSourceLimit {
			break
		}
	}
	caveat := fallbackNoticeQuestionCaveat(question) + " 같은 자산·이벤트 상태·주차 또는 날짜가 겹치는 다거래소 공지는 본문에서 하나로 묶었지만 공식 공지 건수는 합치지 않았습니다."
	if len(ids) < len(matched) {
		caveat += fmt.Sprintf(" 공식 공지 링크는 %d건 중 최신 %d건만 제공합니다.", len(matched), len(ids))
	}
	keyPoints := append(fallbackNoticeExchangeCounts(matched), statusPoints...)
	return marketanalysis.NoticeQuestionAnswer{
		Answer: answer, KeyPoints: keyPoints, Caveat: caveat,
		Source: "SERVER_NOTICE_FALLBACK", AsOfMS: evidence.AsOfMS, Persona: persona,
		SourceNoticeIDs: ids, Sources: []marketanalysis.NoticeQuestionSource{},
	}
}

func fallbackNoticeLine(item domesticNoticeEvidence) string {
	assetText := ""
	if len(item.Assets) > 0 {
		assetText = " · " + strings.Join(item.Assets, ", ")
	}
	return fmt.Sprintf(
		"%s %s · %s%s · %s",
		fallbackNoticeDateLabel(item), strings.ToUpper(item.Exchange),
		noticeQuestionCategoryLabel(item.Category), assetText, item.Title,
	)
}

func fallbackNoticeLineForQuestion(item domesticNoticeEvidence, question string, asOfMS int64) string {
	assetText := ""
	if len(item.Assets) > 0 {
		assetText = " · " + strings.Join(item.Assets, ", ")
	}
	return fmt.Sprintf(
		"%s %s · %s%s · %s",
		fallbackNoticeDateLabel(item), strings.ToUpper(item.Exchange),
		fallbackNoticeQuestionCategoryLabel(item, question, asOfMS), assetText, item.Title,
	)
}

func fallbackNoticeQuestionCategoryLabel(item domesticNoticeEvidence, question string, asOfMS int64) string {
	label := noticeQuestionCategoryLabel(item.Category)
	intent := noticeQuestionIntentFor(question)
	if intent != noticeQuestionIntentDepositStop && intent != noticeQuestionIntentDepositStatus {
		return label
	}
	if !domesticNoticeEvidenceMatchesIntent(item, noticeQuestionIntentDepositStop) ||
		!fallbackNoticeDepositSuspensionIsFutureOnly(item, asOfMS) {
		return label
	}
	return "입출금 중단 예정"
}

func fallbackNoticeDepositSuspensionIsFutureOnly(item domesticNoticeEvidence, asOfMS int64) bool {
	future := false
	for _, schedule := range item.Schedules {
		kind := strings.ToUpper(strings.TrimSpace(schedule.Kind))
		if kind != "DEPOSIT" && kind != "WITHDRAWAL" && kind != "DEPOSIT_WITHDRAWAL" &&
			!strings.Contains(kind, "SUSPENSION") {
			continue
		}
		evidence := strings.ToLower(schedule.Evidence)
		explicitSuspensionKind := strings.Contains(kind, "SUSPENSION")
		resumeOnlyEvidence := containsNoticeQuestionAny(evidence, "재개", "완료", "resume") &&
			!containsNoticeQuestionAny(evidence, "중단", "중지", "suspend", "halt")
		if !explicitSuspensionKind && resumeOnlyEvidence {
			continue
		}
		if schedule.StartTSMS <= 0 {
			continue
		}
		if schedule.StartTSMS <= asOfMS {
			return false
		}
		future = true
	}
	return future
}

func bithumbWriterWantsStyleAnalysis(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	if bithumbWriterDraftRequested(text) {
		return false
	}
	return containsNoticeQuestionAny(text,
		"분석", "특징", "어떻게", "설명", "알려", "정리", "문법", "말투", "문체", "정보 순서",
	)
}

func bithumbWriterDraftRequested(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	if strings.Contains(text, "초안") {
		return true
	}
	if !containsNoticeQuestionAny(text, "작성", "써", "만들", "바꿔", "고쳐", "다듬", "반영") {
		return false
	}
	if containsNoticeQuestionAny(text, "공지문", "안내문", "문안", "게시글", "글을", "글 써") {
		return true
	}
	compact := strings.Join(strings.Fields(text), "")
	return containsNoticeQuestionAny(compact, "공지작성", "공지를작성", "공지써", "공지를써", "공지만들", "공지를만들", "공지로작성", "공지로만들", "공지처럼써")
}

func resolvedNoticeQuestionPersona(persona, question string) string {
	if persona != noticePersonaAuto {
		return persona
	}
	text := strings.ToLower(strings.TrimSpace(question))
	if !containsNoticeQuestionAny(text, "빗썸", "bithumb") {
		return persona
	}
	if bithumbWriterDraftRequested(text) || containsNoticeQuestionAny(text,
		"문법", "말투", "문체", "정보 순서", "작성 특징", "작성특징",
	) {
		return noticePersonaBithumbWriter
	}
	return persona
}

func bithumbWriterMultiActionClarification(
	evidence domesticNoticeQuestionEvidence,
	persona, question string,
) (marketanalysis.NoticeQuestionAnswer, bool) {
	if persona != noticePersonaBithumbWriter || bithumbWriterWantsStyleAnalysis(question) {
		return marketanalysis.NoticeQuestionAnswer{}, false
	}
	actions := bithumbWriterRequestedActions(question)
	if len(actions) <= 1 {
		return marketanalysis.NoticeQuestionAnswer{}, false
	}
	labels := make([]string, 0, len(actions))
	for _, action := range actions {
		labels = append(labels, bithumbWriterActionLabel(action))
	}
	ids := make([]string, 0, len(evidence.StyleExamples))
	ids = append(ids, bithumbWriterAnswerSourceIDs(evidence, question)...)
	return marketanalysis.NoticeQuestionAnswer{
		Answer:    "서로 다른 공지 조치가 함께 요청되었습니다. 한 초안에 임의로 합치지 않고 다음 중 하나를 지정해 다시 요청해 주세요: " + strings.Join(labels, ", "),
		KeyPoints: []string{"조치 선택 필요", strings.Join(labels, ", ")},
		Caveat:    "거래유의·거래지원·입출금 조치는 제목, 사실 항목과 유의사항이 서로 달라 하나의 게시용 초안으로 합치지 않았습니다.",
		Source:    "SERVER_NOTICE_CLARIFICATION", AsOfMS: evidence.AsOfMS,
		SourceNoticeIDs: ids,
	}, true
}

func bithumbWriterAnswerSourceIDs(evidence domesticNoticeQuestionEvidence, question string) []string {
	actions := bithumbWriterRequestedActions(question)
	if len(actions) <= 1 {
		ids := make([]string, 0, min(len(evidence.StyleExamples), noticeQuestionStyleLimit))
		for _, item := range evidence.StyleExamples {
			ids = append(ids, item.SourceID)
			if len(ids) == noticeQuestionStyleLimit {
				break
			}
		}
		return ids
	}

	styleByAction := map[bithumbWriterAction]string{}
	for _, item := range evidence.StyleExamples {
		action := bithumbWriterActionForTitle(item.Title, "")
		if action != "" && styleByAction[action] == "" {
			styleByAction[action] = item.SourceID
		}
	}
	factByAction := map[bithumbWriterAction]string{}
	for _, item := range evidence.Notices {
		action := bithumbWriterActionForEvidence(item)
		if action != "" && factByAction[action] == "" {
			factByAction[action] = item.SourceID
		}
	}
	ids := make([]string, 0, len(actions))
	for _, action := range actions {
		if id := styleByAction[action]; id != "" {
			ids = append(ids, id)
			continue
		}
		if id := factByAction[action]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func bithumbWriterFallbackAnalysis(question string) string {
	structure := "인사말 → 빗썸 브랜드 문구 → 제목·핵심 조치 → 적용 대상·일정·사유 → 유의사항 → 유형별 맺음말 → 감사 인사 순서로 정보를 배치합니다."
	specific := "조치의 종류를 제목과 첫 문단에서 명확히 한 뒤, 확인 항목을 표제와 목록으로 나눕니다."
	actions := bithumbWriterRequestedActions(question)
	switch {
	case len(actions) > 1:
		labels := make([]string, 0, len(actions))
		for _, action := range actions {
			labels = append(labels, bithumbWriterActionLabel(action))
		}
		structure = "요청된 " + strings.Join(labels, "·") + " 유형을 별도 절로 나눕니다. 거래유의형은 정책·사유·검토 일정을, 입출금형은 네트워크·중단 또는 재개 시점·서비스 영향을 각각 앞세웁니다."
		specific = "서로 다른 조치의 제목과 사실 항목을 한 공지처럼 합치지 않고, 유형별 정보 순서와 유의사항의 차이를 나란히 설명합니다."
	case noticeQuestionIntentFor(question) == noticeQuestionIntentCaution:
		structure = "인사말·브랜드 문구 → 제목 → 적용 정책·절차 → 대상별 지정 사유 → 지정일 → 연장·해제·거래지원 종료 검토 일정 → 투자자 유의사항 → 거래유의형 맺음말 → 감사 인사 순서입니다."
		specific = "지정, 연장, 해제, 유의촉구, 거래지원 종료를 같은 상태처럼 섞지 않고 해당 조치를 정확한 동사로 구분합니다."
	case noticeQuestionIntentFor(question) == noticeQuestionIntentDepositStop || noticeQuestionIntentFor(question) == noticeQuestionIntentDepositResume:
		structure = "인사말·브랜드 문구 → 제목 → 중단 또는 재개 조치 → 대상 자산·네트워크 → 적용 시점·조건 → 서비스 영향 → 유의사항 → 입출금형 맺음말 → 감사 인사 순서입니다."
		specific = "중단 전 신청 건, 입금 반영 지연, 거래 가능 여부, 재개 조건을 서로 다른 확인 항목으로 분리합니다."
	}
	return "확인된 빗썸 공식 공지 예시에서 일반화할 수 있는 문체·작성 특징은 다음과 같습니다.\n\n" +
		"- 문법·말투: 존댓말 기반의 공식 안내체를 사용하고, 핵심 조치를 앞부분에서 짧고 분명하게 밝힙니다.\n" +
		"- 정보 순서: " + structure + "\n" +
		"- 표제·목록: 일정, 대상, 사유, 후속 조치처럼 검토가 필요한 정보를 문단 안에 묻지 않고 항목별로 분리합니다.\n" +
		"- 유의사항: 이용자가 확인해야 할 운영 영향과 후속 안내 방식을 별도 절로 두며, 확인되지 않은 내용은 `[확인 필요]`로 남깁니다.\n" +
		"- 조치 표현: " + specific + "\n\n" +
		"이 분석은 공식 예시의 구조를 일반화한 것이며, 고유 문장을 복사하거나 현재 정책과 사실을 대신 검증하지 않습니다."
}

func bithumbWriterFallbackDraft(question string) string {
	return bithumbWriterFallbackDraftWithAssets(question, nil)
}

func bithumbWriterFallbackDraftWithAssets(question string, verifiedAssets []string) string {
	return bithumbWriterFallbackDraftWithEvidence(question, verifiedAssets, nil)
}

func bithumbWriterFallbackDraftWithEvidence(question string, verifiedAssets []string, styleExamples []domesticNoticeStyleEvidence) string {
	return noticewriter.Render(noticewriter.RenderInput{
		Action:   bithumbWriterActionForText(question),
		Assets:   verifiedAssets,
		Envelope: bithumbWriterEnvelope(question, styleExamples),
	})
}

func bithumbWriterEnvelope(question string, styleExamples []domesticNoticeStyleEvidence) noticewriter.Envelope {
	salutation := "안녕하세요."
	brandLine := "당신의 안전한 금융 파트너, 빗썸입니다."
	for _, example := range styleExamples {
		for _, line := range strings.Split(example.OpeningExcerpt, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case noticewriter.IsSalutation("bithumb", line):
				salutation = line
			case noticewriter.IsBrandLine("bithumb", line) && !strings.ContainsAny(line, "0123456789[]"):
				brandLine = line
			}
		}
		if strings.TrimSpace(example.OpeningExcerpt) != "" {
			break
		}
	}

	contract, _ := noticewriter.ContractFor(bithumbWriterActionForText(question))
	canonicalPromise := noticewriter.CanonicalClosing(contract.ClosingKind)
	promise := canonicalPromise
	for _, example := range styleExamples {
		found := false
		for _, line := range strings.Split(example.ClosingExcerpt, "\n") {
			line = strings.TrimSpace(line)
			if noticewriter.IsClosingLine("bithumb", line) &&
				!strings.ContainsAny(line, "0123456789[]") {
				promise = line
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	return noticewriter.Envelope{Salutation: salutation, BrandLine: brandLine, Closing: promise}
}

func bithumbWriterVerifiedDraftAssets(
	evidence domesticNoticeQuestionEvidence,
	question string,
) ([]string, []string) {
	intent := noticeQuestionIntentFor(question)
	action := bithumbWriterActionForText(question)
	if action == "" {
		return nil, nil
	}
	known := map[string]struct{}{}
	rowsByAsset := map[string][]string{}
	for _, row := range evidence.Notices {
		if !strings.EqualFold(row.Exchange, "bithumb") ||
			!domesticNoticeEvidenceMatchesIntent(row, intent) ||
			!noticeQuestionStyleCompatible(row.Title, row.Category, intent, question) {
			continue
		}
		for _, asset := range domesticNoticeEvidencePrimaryAssets(row) {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset == "" {
				continue
			}
			known[asset] = struct{}{}
			rowsByAsset[asset] = append(rowsByAsset[asset], row.SourceID)
		}
	}
	explicit := noticeQuestionExplicitAssets(question, known)
	if len(explicit) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(explicit))
	seenIDs := map[string]struct{}{}
	for _, asset := range explicit {
		for _, id := range rowsByAsset[asset] {
			if _, duplicate := seenIDs[id]; duplicate {
				continue
			}
			seenIDs[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return explicit, ids
}

func fallbackNoticeEventStatusCounts(items []domesticNoticeEvidence) []string {
	ended, extended, other := 0, 0, 0
	for _, item := range items {
		if len(item.Actions) > 0 && notice.HasAction(item.Actions, "EVENT") {
			switch {
			case notice.HasAction(item.Actions, "EVENT", "END", "COMPLETE"):
				ended++
			case notice.HasAction(item.Actions, "EVENT", "EXTEND"):
				extended++
			default:
				other++
			}
			continue
		}
		title := strings.ToLower(item.Title)
		switch {
		case containsNoticeQuestionAny(title, "종료", "결과 안내"):
			ended++
		case strings.Contains(title, "연장"):
			extended++
		default:
			other++
		}
	}
	return []string{
		fmt.Sprintf("종료·결과 %d건", ended),
		fmt.Sprintf("연장 %d건", extended),
		fmt.Sprintf("진행·기타 %d건", other),
	}
}

func fallbackNoticeCautionStatusCounts(items []domesticNoticeEvidence) []string {
	designated, extended, released := 0, 0, 0
	for _, item := range items {
		switch domesticNoticeEvidenceCautionAction(item) {
		case bithumbWriterActionCautionRelease:
			released++
		case bithumbWriterActionCautionExtension:
			extended++
		case bithumbWriterActionCautionDesignation:
			designated++
		}
	}
	return []string{
		fmt.Sprintf("지정 %d건", designated),
		fmt.Sprintf("연장 %d건", extended),
		fmt.Sprintf("해제 %d건", released),
	}
}

func noticeQuestionEvidenceSourceIDs(items []domesticNoticeEvidence) []string {
	result := make([]string, 0, min(len(items), 10))
	for _, item := range items {
		if sourceID := strings.TrimSpace(item.SourceID); sourceID != "" {
			result = append(result, sourceID)
		}
		if len(result) == 10 {
			break
		}
	}
	return result
}

func fallbackNoticeQuestionWantsCount(question string) bool {
	text := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(question), " ", ""))
	return containsNoticeQuestionAny(text, "건수", "개수", "몇건", "몇개")
}

func fallbackNoticeQuestionWantsExchangeGroups(question string) bool {
	text := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(question), " ", ""))
	return strings.Contains(text, "거래소별")
}

func fallbackNoticeExchangeCounts(items []domesticNoticeEvidence) []string {
	counts := map[string]int{}
	for _, item := range items {
		exchange := strings.ToLower(strings.TrimSpace(item.Exchange))
		if exchange != "" {
			counts[exchange]++
		}
	}
	result := make([]string, 0, min(len(counts), 5))
	for _, exchange := range domesticNoticeExchangeOrder {
		if count := counts[exchange]; count > 0 {
			result = append(result, fmt.Sprintf("%s %d건", strings.ToUpper(exchange), count))
		}
	}
	return result
}

func fallbackNoticeGroupedLines(items []domesticNoticeEvidence, question string, asOfMS int64) string {
	groups := map[string][]string{}
	for _, item := range items {
		exchange := strings.ToLower(strings.TrimSpace(item.Exchange))
		if exchange == "" {
			continue
		}
		assetText := ""
		if len(item.Assets) > 0 {
			assetText = " · " + strings.Join(item.Assets, ", ")
		}
		groups[exchange] = append(groups[exchange], fmt.Sprintf(
			"- %s · %s%s · %s", fallbackNoticeDateLabel(item), fallbackNoticeQuestionCategoryLabel(item, question, asOfMS), assetText, item.Title,
		))
	}
	sections := make([]string, 0, len(groups))
	for _, exchange := range domesticNoticeExchangeOrder {
		lines := groups[exchange]
		if len(lines) == 0 {
			continue
		}
		sections = append(sections, fmt.Sprintf("%s (%d건)\n%s", strings.ToUpper(exchange), len(lines), strings.Join(lines, "\n")))
	}
	return strings.Join(sections, "\n\n")
}

func fallbackNoticeDateLabel(item domesticNoticeEvidence) string {
	format := func(value int64) string {
		return time.UnixMilli(value).In(seoulLocation).Format("2006-01-02")
	}
	published := item.PublishedTSMS
	updated := item.UpdatedTSMS
	if published > 0 && updated > published && format(updated) != format(published) {
		return "게시 " + format(published) + " · 수정 " + format(updated)
	}
	if published > 0 {
		return format(published)
	}
	if updated > 0 {
		return "수정 " + format(updated)
	}
	if item.DetectedTSMS > 0 {
		return "탐지 " + format(item.DetectedTSMS)
	}
	return "시각 미확인"
}

func fallbackNoticeQuestionCaveat(question string) string {
	base := "서버의 국내 공식 공지와 관측된 KRW 체결 자료만 사용했습니다. 공지와 거래량의 동시성은 인과관계가 아닙니다."
	switch noticeQuestionIntentFor(question) {
	case noticeQuestionIntentDepositStop:
		return base + " 최근 중단 공지 목록에는 미래 적용 예정인 공지가 포함될 수 있습니다. 공식 제목·본문의 명시적 중단 시각이 기준 시각보다 미래인 경우에만 '입출금 중단 예정'으로 표시하며, 재개·완료 공지는 제외했습니다. 시각이 지났거나 불명인 표시는 현재 입출금 가능 여부를 뜻하지 않으므로 각 원문에서 다시 확인해야 합니다."
	case noticeQuestionIntentDepositResume:
		return base + " 재개·완료 표시는 공지 제목 기준이며, 이후 다시 중단됐을 수 있으므로 현재 입출금 가능 여부는 각 거래소 원문에서 다시 확인해야 합니다."
	case noticeQuestionIntentDepositStatus:
		return base + " 중단과 재개·완료는 최근 기간에 게시된 공지의 상태 구분입니다. 수정 시각은 게시 시각과 분리해 표시합니다. 같은 자산이 양쪽에 모두 나타날 수 있으며 현재 입출금 가능 상태를 뜻하지 않으므로 가장 최신 거래소 원문을 다시 확인해야 합니다."
	case noticeQuestionIntentImportant:
		return base + " 주요 변화는 거래지원, 거래유의·보안, 입출금·점검 공지를 운영 영향 후보로 분류한 결과이며 심각도 순위가 아닙니다. 홍보 이벤트와 후기성 게시물은 제외했습니다."
	case noticeQuestionIntentEvent:
		return base + " 최근 기간은 실제 게시 시각으로 판정합니다. 수정 시각만 최근인 과거 공지는 최신 게시물로 올리지 않으며, 반환된 공지의 종료·연장 표시와 별도 수정 시각을 함께 확인해야 합니다."
	case noticeQuestionIntentCaution:
		return base + " 지정·연장·해제 상태는 공지 제목 기준이며, 현재 거래유의 상태는 이후 공지와 각 거래소 원문에서 다시 확인해야 합니다."
	case noticeQuestionIntentDelisting:
		if containsNoticeQuestionAny(strings.ToLower(question), "코인", "자산", "종목") {
			return base + " 특정 코인·자산·종목을 묻는 경우 공식 제목 또는 검증된 자산 목록에서 대상을 확인할 수 있는 공지만 표시합니다. 수정된 일정·정책 안내는 일반 공지 조회에서 별도로 확인할 수 있습니다."
		}
		return base + " 최근 기간에 실제 게시된 거래지원 종료 공지와 일정·정책 안내를 표시합니다. 수정 시각만 최근인 과거 공지는 최신 게시물로 올리지 않으며, 특정 종료 자산은 각 원문에서 다시 확인해야 합니다."
	case noticeQuestionIntentListing:
		return base + " 거래지원 마켓은 공지 제목 기준이며 모든 자산이 KRW 마켓에 상장됐다는 뜻은 아닙니다."
	default:
		return base
	}
}

func noticeQuestionWindowLabel(retrieval domesticNoticeRetrieval) string {
	if label := strings.TrimSpace(retrieval.WindowLabel); label != "" {
		return label
	}
	if retrieval.WindowDays > 0 {
		return fmt.Sprintf("최근 %d일", retrieval.WindowDays)
	}
	return "최근 30일 수집 범위"
}

func noticeQuestionRowsForSourceIDs(items []domesticNoticeEvidence, ids []string) []domesticNoticeEvidence {
	byID := make(map[string]domesticNoticeEvidence, len(items))
	for _, item := range items {
		byID[item.SourceID] = item
	}
	result := make([]domesticNoticeEvidence, 0, len(ids))
	for _, id := range ids {
		if item, exists := byID[id]; exists {
			result = append(result, item)
		}
	}
	return result
}

func fallbackNoticeMatches(items []domesticNoticeEvidence, question string, asOfMS int64) []domesticNoticeEvidence {
	query := buildDomesticNoticeRetrievalQuery(items, question, time.UnixMilli(asOfMS))
	result := make([]domesticNoticeEvidence, 0)
	for _, item := range items {
		if domesticNoticeRetrievalMatch(item, query) {
			result = append(result, item)
		}
	}
	sortDomesticNoticeRetrievalRows(result, query)
	return result
}

func noticeQuestionIntentFor(question string) noticeQuestionIntent {
	text := strings.ToLower(strings.TrimSpace(question))
	resumeExcluded := noticeQuestionExcludesDepositResume(text)
	switch {
	case noticeQuestionWantsImportantChanges(text):
		return noticeQuestionIntentImportant
	case containsNoticeQuestionAny(text, "거래지원 종료", "거래지원종료", "상장폐지", "마켓 제거"):
		return noticeQuestionIntentDelisting
	case containsNoticeQuestionAny(text, "신규 거래지원", "거래지원된", "거래지원한", "상장", "마켓 추가", "디지털 자산 추가"):
		return noticeQuestionIntentListing
	case containsNoticeQuestionAny(text, "유의종목", "유의 종목", "거래유의", "투자유의", "유의촉구"):
		return noticeQuestionIntentCaution
	case !resumeExcluded && noticeQuestionWantsDepositStatusPair(text):
		return noticeQuestionIntentDepositStatus
	case !resumeExcluded && (containsNoticeQuestionAny(text, "재개", "재개완료", "중단 완료", "중지 완료") ||
		(containsNoticeQuestionAny(text, "입출금", "입금", "출금") && strings.Contains(text, "(완료)"))):
		return noticeQuestionIntentDepositResume
	case containsNoticeQuestionAny(text, "중단", "중지", "차단"):
		return noticeQuestionIntentDepositStop
	case containsNoticeQuestionAny(text, "입출금", "입금", "출금"):
		// Naming an operation is not an assertion that it is suspended.
		// Preserve both suspension and resumption evidence for the model.
		return noticeQuestionIntentDepositStatus
	case containsNoticeQuestionAny(text, "이벤트", "에어드랍", "프로모션", "혜택", "챌린지", "페스타"):
		return noticeQuestionIntentEvent
	case containsNoticeQuestionAny(text, "점검", "유지보수"):
		return noticeQuestionIntentMaintenance
	default:
		return ""
	}
}

func noticeQuestionExcludesDepositResume(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if containsNoticeQuestionAny(text, "제외하지", "빼지 마", "빼지마") {
		return false
	}
	if !containsNoticeQuestionAny(text, "제외", "빼고", "빼줘", "빼 줘") {
		return false
	}
	return containsNoticeQuestionAny(text, "재개", "완료")
}

func noticeQuestionWantsImportantChanges(text string) bool {
	return containsNoticeQuestionAny(text,
		"중요한 변화", "주요 변화", "핵심 변화", "중요 변화",
		"중요 공지", "주요 공지", "핵심 공지",
	)
}

func noticeQuestionWantsCautionStatusBreakdown(question string) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	return containsNoticeQuestionAny(text,
		"상태별", "상태 별", "지정·연장", "지정/연장", "지정 및 연장", "지정과 연장", "지정, 연장",
		"연장·해제", "연장/해제", "연장 및 해제", "연장과 해제", "연장, 해제", "연장 해제",
	)
}

func noticeQuestionWantsDepositStatusPair(text string) bool {
	if containsNoticeQuestionAny(text,
		"중단·재개", "중단/재개", "중단 및 재개", "중단과 재개", "중단, 재개", "중단 재개",
		"중지·재개", "중지/재개", "중지 및 재개", "중지와 재개", "중지, 재개", "중지 재개",
		"차단·재개", "차단/재개", "차단 및 재개", "차단과 재개", "차단, 재개", "차단 재개",
		"중단된 코인과 재개된", "중단된 자산과 재개된", "중지된 코인과 재개된", "차단된 코인과 재개된",
	) {
		return true
	}
	depositRelated := containsNoticeQuestionAny(text, "입출금", "입금", "출금")
	statusRequested := containsNoticeQuestionAny(text,
		"상태", "현황", "가능", "할 수 있", "되나", "되는지",
	)
	explicitSingleState := containsNoticeQuestionAny(text, "중단", "중지", "차단", "재개", "완료")
	return depositRelated && statusRequested && !explicitSingleState
}

func domesticNoticeEvidenceMatchesIntent(item domesticNoticeEvidence, intent noticeQuestionIntent) bool {
	if len(item.Actions) == 0 {
		// The indexed category is already canonicalized from the complete official
		// title. Resolve category-level intents without rerunning the Unicode-heavy
		// title action parser for every candidate on every question.
		category := strings.ToUpper(strings.TrimSpace(item.Category))
		switch intent {
		case noticeQuestionIntentListing:
			if category == "LISTING" || category == "MARKET_ADDITION" {
				return true
			}
		case noticeQuestionIntentDelisting:
			if category == "DELISTING" || category == "TRADING_SUPPORT_TERMINATION" {
				return true
			}
		case noticeQuestionIntentCaution:
			if category == "CAUTION" {
				return true
			}
		case noticeQuestionIntentEvent:
			if category == "EVENT" {
				return true
			}
		case noticeQuestionIntentMaintenance:
			if category == "MAINTENANCE" {
				return true
			}
		case noticeQuestionIntentImportant:
			switch category {
			case "LISTING", "MARKET_ADDITION", "DELISTING", "TRADING_SUPPORT_TERMINATION",
				"CAUTION", "SECURITY", "SECURITY_INCIDENT", "DEPOSIT_WITHDRAWAL", "MAINTENANCE":
				return true
			}
		}
	}
	actions := effectiveNoticeQuestionActions(item)
	switch intent {
	case noticeQuestionIntentListing:
		return notice.HasAction(actions, "TRADING_SUPPORT", "START", "ADD")
	case noticeQuestionIntentDelisting:
		return notice.HasAction(actions, "TRADING_SUPPORT", "END")
	case noticeQuestionIntentCaution:
		return notice.HasAction(actions, "CAUTION")
	case noticeQuestionIntentDepositResume:
		return notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "RESUME", "COMPLETE")
	case noticeQuestionIntentDepositStop:
		return notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "SUSPEND") &&
			!notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "RESUME", "COMPLETE")
	case noticeQuestionIntentDepositStatus:
		return notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "SUSPEND", "RESUME", "COMPLETE")
	case noticeQuestionIntentImportant:
		for _, subject := range []string{"TRADING_SUPPORT", "CAUTION", "DEPOSIT_WITHDRAWAL", "SECURITY", "MAINTENANCE"} {
			if notice.HasAction(actions, subject) {
				return true
			}
		}
		return false
	case noticeQuestionIntentEvent:
		return notice.HasAction(actions, "EVENT")
	case noticeQuestionIntentMaintenance:
		return notice.HasAction(actions, "MAINTENANCE")
	default:
		return true
	}
}

func effectiveNoticeQuestionActions(item domesticNoticeEvidence) []notice.Action {
	if len(item.Actions) > 0 {
		return item.Actions
	}
	return notice.InferTitleActions(item.Title, item.Category)
}

func domesticNoticeEvidenceCautionAction(item domesticNoticeEvidence) bithumbWriterAction {
	actions := effectiveNoticeQuestionActions(item)
	for _, candidate := range []struct {
		verb   string
		action bithumbWriterAction
	}{
		{"RELEASE", bithumbWriterActionCautionRelease},
		{"EXTEND", bithumbWriterActionCautionExtension},
		{"URGE", bithumbWriterActionCautionUrge},
		{"DESIGNATE", bithumbWriterActionCautionDesignation},
	} {
		if notice.HasAction(actions, "CAUTION", candidate.verb) {
			return candidate.action
		}
	}
	return ""
}

func bithumbWriterActionForEvidence(item domesticNoticeEvidence) bithumbWriterAction {
	actions := effectiveNoticeQuestionActions(item)
	if action := domesticNoticeEvidenceCautionAction(item); action != "" {
		return action
	}
	switch {
	case notice.HasAction(actions, "TRADING_SUPPORT", "END"):
		return bithumbWriterActionDelisting
	case notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "RESUME", "COMPLETE"):
		return bithumbWriterActionDepositResume
	case notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "SUSPEND"):
		return bithumbWriterActionDepositStop
	default:
		return ""
	}
}

func domesticNoticeEvidenceHasCautionAction(item domesticNoticeEvidence, action bithumbWriterAction) bool {
	return domesticNoticeEvidenceCautionAction(item) == action
}

func domesticNoticeEvidenceIsCautionDesignationFamily(item domesticNoticeEvidence) bool {
	switch domesticNoticeEvidenceCautionAction(item) {
	case bithumbWriterActionCautionDesignation,
		bithumbWriterActionCautionExtension,
		bithumbWriterActionCautionRelease:
		return true
	default:
		return false
	}
}

func noticeQuestionItemMatches(title, category string, intent noticeQuestionIntent) bool {
	actions := notice.InferTitleActions(title, category)
	switch intent {
	case noticeQuestionIntentListing:
		return notice.HasAction(actions, "TRADING_SUPPORT", "START", "ADD")
	case noticeQuestionIntentDelisting:
		return notice.HasAction(actions, "TRADING_SUPPORT", "END")
	case noticeQuestionIntentCaution:
		return notice.HasAction(actions, "CAUTION")
	case noticeQuestionIntentDepositResume:
		return notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "RESUME", "COMPLETE")
	case noticeQuestionIntentDepositStop:
		return notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "SUSPEND") &&
			!notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "RESUME", "COMPLETE")
	case noticeQuestionIntentDepositStatus:
		return notice.HasAction(actions, "DEPOSIT_WITHDRAWAL", "SUSPEND", "RESUME", "COMPLETE")
	case noticeQuestionIntentImportant:
		for _, subject := range []string{"TRADING_SUPPORT", "CAUTION", "DEPOSIT_WITHDRAWAL", "SECURITY", "MAINTENANCE"} {
			if notice.HasAction(actions, subject) {
				return true
			}
		}
		return false
	case noticeQuestionIntentEvent:
		return notice.HasAction(actions, "EVENT")
	case noticeQuestionIntentMaintenance:
		return notice.HasAction(actions, "MAINTENANCE")
	default:
		return true
	}
}

func canonicalNoticeQuestionCategory(title, supplied string) string {
	text := strings.ToLower(title)
	switch {
	case strings.Contains(text, "에어드랍"):
		return "AIRDROP"
	case containsNoticeQuestionAny(text, "이벤트", "프로모션", "챌린지", "페스타", "혜택", "상금"):
		return "EVENT"
	case containsNoticeQuestionAny(text, "거래지원 종료", "거래지원종료", "상장폐지"):
		return "DELISTING"
	case containsNoticeQuestionAny(text, "신규 거래지원", "거래지원 개시", "원화 마켓 추가", "원화마켓 추가", "마켓 디지털 자산 추가", "디지털 자산 추가"):
		return "LISTING"
	default:
		return notice.Classify(title, supplied)
	}
}

func containsNoticeQuestionAny(value string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(value, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func noticeQuestionCategoryLabel(category string) string {
	category = strings.ToUpper(strings.TrimSpace(category))
	switch {
	case category == "LISTING" || category == "MARKET_ADDITION":
		return "신규 거래지원"
	case category == "DELISTING" || category == "TRADING_SUPPORT_TERMINATION":
		return "거래지원 종료"
	case category == "CAUTION":
		return "거래유의"
	case strings.HasPrefix(category, "DEPOSIT_WITHDRAWAL"):
		return "입출금"
	case category == "SECURITY" || category == "SECURITY_INCIDENT":
		return "보안"
	case category == "EVENT":
		return "이벤트"
	case category == "AIRDROP":
		return "에어드랍"
	case category == "MAINTENANCE":
		return "점검"
	default:
		return category
	}
}

func fallbackNoticeVolumeLines(days []domesticNoticeVolumeDay) []string {
	result := make([]string, 0, len(days))
	for _, day := range days {
		coverage := fallbackNoticeVolumeCoverageDetail(day)
		dayLabel := day.Day
		if day.IntervalStartKST != "" && day.IntervalEndKSTExclusive != "" {
			dayLabel += " [" + day.IntervalStartKST + " 이상, " + day.IntervalEndKSTExclusive + " 미만]"
		}
		unavailable := strings.EqualFold(day.Status, "UNAVAILABLE") ||
			strings.EqualFold(day.CoverageStatus, "UNAVAILABLE") || day.ObservedMarketCount <= 0
		if unavailable || len(day.Assets) == 0 {
			status := strings.TrimSpace(day.Status)
			if status == "" {
				status = "UNAVAILABLE"
			}
			result = append(result, fmt.Sprintf("%s · %s%s · 선택 자산의 국내 KRW 관측값 없음", dayLabel, status, coverage))
			continue
		}
		assets := make([]string, 0, len(day.Assets))
		for _, asset := range day.Assets {
			assetText := fmt.Sprintf("%s %.0f원", asset.Asset, asset.VolumeKRW)
			details := make([]string, 0, 2)
			if asset.MarketCount > 0 {
				details = append(details, fmt.Sprintf(
					"자산 KRW 마켓 %d/%d개 관측", asset.ObservedCount, asset.MarketCount,
				))
			}
			if len(asset.Exchanges) > 0 {
				exchanges := make([]string, 0, len(asset.Exchanges))
				for _, exchange := range asset.Exchanges {
					exchanges = append(exchanges, strings.ToUpper(strings.TrimSpace(exchange)))
				}
				details = append(details, strings.Join(exchanges, "/"))
			}
			if len(details) > 0 {
				assetText += " [" + strings.Join(details, " · ") + "]"
			}
			assets = append(assets, assetText)
		}
		status := strings.TrimSpace(day.Status)
		if coverage := strings.TrimSpace(day.CoverageStatus); coverage != "" && !strings.EqualFold(coverage, status) {
			status += "/" + coverage
		}
		result = append(result, fmt.Sprintf("%s · %s%s · %s", dayLabel, status, coverage, strings.Join(assets, ", ")))
	}
	return result
}

func fallbackNoticeVolumeCoverageDetail(day domesticNoticeVolumeDay) string {
	result := ""
	if day.RequiredSec > 0 {
		if day.CoverageScope == "ALL_DOMESTIC_TRANSPORT" {
			result += fmt.Sprintf(" · 국내 전체 수신 관측 %d/%d초", day.CoverageSec, day.RequiredSec)
		} else {
			result += fmt.Sprintf(" · 전체 KRW coverage %d/%d초", day.CoverageSec, day.RequiredSec)
		}
	}
	if day.ObservedMarketCount > 0 {
		if day.CoverageScope == "ALL_DOMESTIC_TRANSPORT" {
			result += fmt.Sprintf(" · 선택 범위 관측 마켓 %d개", day.ObservedMarketCount)
		} else {
			result += fmt.Sprintf(" · 전체 관측 마켓 %d개", day.ObservedMarketCount)
		}
	}
	return result
}

func validatedNoticeQuestionSources(ids []string, catalog map[string]marketanalysis.NoticeQuestionSource) []marketanalysis.NoticeQuestionSource {
	result := make([]marketanalysis.NoticeQuestionSource, 0, min(len(ids), noticeQuestionVerifiedSourceLimit))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		item, ok := catalog[id]
		if !ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, item)
		if len(result) == noticeQuestionVerifiedSourceLimit {
			break
		}
	}
	return result
}

func validatedNoticeQuestionSourceIDs(ids []string, catalog map[string]marketanalysis.NoticeQuestionSource) []string {
	sources := validatedNoticeQuestionSources(ids, catalog)
	result := make([]string, 0, len(sources))
	for _, source := range sources {
		result = append(result, noticeQuestionSourceID(source.Exchange, source.NoticeID))
	}
	return result
}

func noticeQuestionSourceID(exchange, noticeID string) string {
	return strings.ToLower(strings.TrimSpace(exchange)) + ":" + strings.TrimSpace(noticeID)
}

func truncateNoticeQuestionText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if maximum <= 0 {
		return ""
	}
	if len([]rune(value)) <= maximum {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximum-1])) + "…"
}
