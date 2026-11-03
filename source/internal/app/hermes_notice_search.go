package app

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"market-share-go/internal/notice"
)

const (
	noticeQuestionSnippetLimit = noticeQuestionPromptSnippetLimit
	noticeQuestionSnippetRunes = noticeQuestionPromptSnippetRunes
	noticeQuestionActionLimit  = 8
)

var (
	noticeQuestionSearchTermPattern         = regexp.MustCompile(`[0-9A-Za-z가-힣][0-9A-Za-z가-힣._-]*`)
	noticeQuestionDaysPattern               = regexp.MustCompile(`(최근\s*)?([0-9]+)\s*일`)
	noticeQuestionDisplayCountPattern       = regexp.MustCompile(`(?:^|[\s,(])([0-9]+|한|두|세|네|다섯|여섯|일곱|여덟|아홉|열)[\t ]*(?:건|개)(?:의|를|만|는|가|은|을|이)?(?:$|[\s,.!?])`)
	noticeQuestionSingleCountPattern        = regexp.MustCompile(`(?:^|[\s,(])하나(?:의|를|만|는|가)?(?:$|[\s,.!?])`)
	noticeQuestionISODatePattern            = regexp.MustCompile(`([12][0-9]{3})[-./]([01]?[0-9])[-./]([0-3]?[0-9])`)
	noticeQuestionKoreanDatePattern         = regexp.MustCompile(`(?:([12][0-9]{3})\s*년\s*)?([01]?[0-9])\s*월\s*([0-3]?[0-9])\s*일`)
	noticeQuestionMarketListPattern         = regexp.MustCompile(`(?i)(?:^|[\s,(])((?:KRW|BTC|USDT)(?:\s*,\s*(?:KRW|BTC|USDT))*)\s*마켓`)
	noticeQuestionJoinPattern               = regexp.MustCompile(`(?:과|와)\s+|\s+(?:및|또는|그리고)\s+|[·/,]`)
	noticeQuestionReferencePattern          = regexp.MustCompile(`(?:^|[\s,.;!?])(?:그|이|저|해당)\s+[가-힣A-Za-z]`)
	noticeQuestionSwitchPattern             = regexp.MustCompile(`(?:^|\s)(?:그|이|저|해당)\s+\S+?\s*(?:말고|대신)(?:\s|$)`)
	noticeQuestionExclusionScopePattern     = regexp.MustCompile(`(?:\s+(?:중|중에서|중에|가운데)|에서)\s+`)
	noticeQuestionCountContinuationPattern  = regexp.MustCompile(`몇\s*(?:건|개)(?:이고|이며)\s*`)
	noticeQuestionCautionLifecyclePattern   = regexp.MustCompile(`(?:^|[^가-힣A-Za-z0-9])유의\s*(?:종목\s*)?(?:지정|연장|해제|촉구)`)
	noticeQuestionCautionActionPattern      = regexp.MustCompile(`지정\s*(?:연장|해제)|지정|연장|해제|촉구`)
	noticeQuestionCautionActionJoinPattern  = regexp.MustCompile(`^\s*(?:(?:과|와)\s*|(?:및|또는|그리고)\s+|[·/,]\s*)(?:(?:거래|투자)?\s*유의\s*(?:종목\s*)?)?$`)
	noticeQuestionCautionActionEndPattern   = regexp.MustCompile(`^(?:$|[\s,.!?/·]|(?:과|와|은|는|이|가|을|를|만|부터|까지)(?:\s|$))`)
	noticeQuestionCautionActionStartPattern = regexp.MustCompile(`(?:^|[^가-힣A-Za-z0-9]|유의|종목)$`)
)

var noticeQuestionSearchStopWords = map[string]struct{}{
	"게시된": {}, "등록된": {}, "발표된": {}, "발행된": {}, "올라온": {}, "부터": {}, "까지": {}, "오늘까지": {}, "현재까지": {}, "지금까지": {},
	"거래소": {}, "공지": {}, "공지사항": {}, "내용": {}, "관련": {}, "대한": {},
	"거래소별": {}, "거래소별로": {}, "목록": {}, "항목": {},
	"유형": {}, "유형별": {}, "유형별로": {}, "종류": {}, "종류별": {}, "종류별로": {}, "카테고리별": {}, "카테고리별로": {},
	"한국": {}, "국내": {}, "대한민국": {},
	"그중": {}, "그중에서": {}, "이중": {}, "이중에서": {},
	"최근": {}, "최신": {}, "지난": {}, "정확히": {}, "빠르게": {}, "새로": {}, "현재": {}, "지금": {},
	"오늘": {}, "오늘자": {}, "어제": {}, "이번": {}, "이번주": {}, "지난주": {}, "이번달": {}, "지난달": {},
	"최근순": {}, "최신순": {}, "게시순": {}, "날짜순": {}, "시간순": {},
	"공식": {}, "공식적": {}, "공식적인": {},
	"이내": {}, "내에": {}, "안에": {}, "동안": {}, "기준": {},
	"중복": {}, "중복없이": {}, "없이": {}, "모아서": {}, "한눈에": {},
	"제외": {}, "제외해": {}, "제외해줘": {}, "제외해주세요": {}, "빼고": {},
	"간단히": {}, "짧게": {}, "요점": {}, "핵심만": {},
	"개수": {}, "건수": {}, "몇개": {}, "몇건": {}, "하나": {}, "한개": {}, "한건": {}, "총": {}, "전부": {}, "모두": {},
	"개야": {}, "개인가": {}, "개인가요": {}, "개입니까": {}, "개예요": {},
	"건이야": {}, "건인가": {}, "건인가요": {}, "건입니까": {}, "건이에요": {}, "건이죠": {},
	"정도": {}, "정도야": {}, "되나요": {}, "됩니까": {},
	"어떤": {}, "무엇": {}, "뭐가": {}, "뭐야": {}, "있는": {}, "있어": {}, "들어간": {},
	"알려줘": {}, "알려": {}, "찾아줘": {}, "찾아": {}, "보여줘": {}, "보여": {},
	"정리해줘": {}, "정리해": {}, "설명해줘": {}, "설명해": {}, "해주세요": {}, "해줘": {},
	"정리하고": {}, "여부": {}, "상태": {}, "상태별": {}, "상태별로": {},
	"사용자": {}, "관점": {}, "공통점": {}, "차이": {}, "시사점": {},
	"요약": {}, "요약해": {}, "요약해줘": {}, "요약해주세요": {},
	"분류": {}, "분류해": {}, "분류해줘": {}, "나열": {}, "나열해": {}, "나열해줘": {},
	"초안": {}, "작성": {}, "작성해": {}, "작성하고": {}, "써": {}, "써줘": {}, "써주세요": {},
	"만들어": {}, "바꿔": {}, "고쳐": {}, "다듬어": {}, "반영해": {}, "반영해서": {},
	"문법": {}, "말투": {}, "문체": {}, "구조": {}, "형식": {},
	"코인": {}, "자산": {}, "자산별": {}, "자산별로": {}, "일별": {}, "거래량": {}, "거래대금": {}, "볼륨": {},
	"coverage": {}, "커버리지": {}, "함께": {}, "같이": {},
	"재개": {}, "완료": {},
	"krw": {},
	"the": {}, "a": {}, "an": {}, "of": {}, "for": {}, "notice": {}, "notices": {},
	"find": {}, "show": {}, "tell": {}, "about": {}, "recent": {}, "latest": {}, "newest": {},
}

// These are query-operation stems, not notice-domain vocabulary. Korean
// particles and request endings produce many surface forms (분석해줘,
// 공통적인, 근거와, 영향은). Treating the stem as presentation grammar keeps
// semantic questions open-ended without adding one exception per phrasing.
var noticeQuestionPresentationStems = []string{
	"분석", "설명", "해석", "요약", "정리", "비교", "공통", "차이", "특징", "근거", "핵심",
	"의미", "이유", "원인", "사유", "영향", "시사", "관점", "문법", "말투", "문체", "형식", "구조", "방식",
}

var noticeQuestionExchangeAliases = map[string][]string{
	"upbit":   {"upbit", "업비트"},
	"bithumb": {"bithumb", "빗썸"},
	"coinone": {"coinone", "코인원"},
	"korbit":  {"korbit", "코빗"},
	"gopax":   {"gopax", "고팍스"},
}

type domesticNoticeMatchedEvidence struct {
	Source   string `json:"source"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url,omitempty"`
}

type domesticNoticeRetrieval struct {
	Strategy                string                          `json:"strategy"`
	HistoryDays             int                             `json:"history_days,omitempty"`
	ReferenceDateKST        string                          `json:"reference_date_kst,omitempty"`
	InvalidCalendarDate     bool                            `json:"invalid_calendar_date,omitempty"`
	BackoffUsed             bool                            `json:"backoff_used,omitempty"`
	EligibleNoticeCount     int                             `json:"eligible_notice_count"`
	MatchedNoticeCount      int                             `json:"matched_notice_count"`
	ReturnedNoticeCount     int                             `json:"returned_notice_count"`
	CandidateLimit          int                             `json:"candidate_limit"`
	Intent                  string                          `json:"intent,omitempty"`
	Intents                 []string                        `json:"intents,omitempty"`
	ExchangeNoticeCounts    []domesticNoticeExchangeCount   `json:"exchange_notice_counts,omitempty"`
	CautionAction           string                          `json:"caution_action,omitempty"`
	CautionActions          []string                        `json:"caution_actions,omitempty"`
	QueryTerms              []string                        `json:"query_terms,omitempty"`
	Exchanges               []string                        `json:"exchanges,omitempty"`
	Assets                  []string                        `json:"assets,omitempty"`
	ExcludedFilters         []domesticNoticeSearchExclusion `json:"excluded_filters,omitempty"`
	ListingMarkets          []string                        `json:"listing_markets,omitempty"`
	WindowDays              int                             `json:"window_days,omitempty"`
	RequestedWindowDays     string                          `json:"requested_window_days,omitempty"`
	WindowLimited           bool                            `json:"window_limited,omitempty"`
	WindowLabel             string                          `json:"window_label,omitempty"`
	WindowStartKST          string                          `json:"window_start_kst,omitempty"`
	WindowEndKSTExclusive   string                          `json:"window_end_kst_exclusive,omitempty"`
	WindowOpen              bool                            `json:"window_open,omitempty"`
	RequireNamedAsset       bool                            `json:"require_named_asset,omitempty"`
	RequirePrimaryAsset     bool                            `json:"require_primary_asset,omitempty"`
	ContextApplied          bool                            `json:"context_applied,omitempty"`
	Truncated               bool                            `json:"truncated,omitempty"`
	DepositSuspensionCount  int                             `json:"deposit_suspension_count,omitempty"`
	DepositResumptionCount  int                             `json:"deposit_resumption_count,omitempty"`
	CautionDesignationCount int                             `json:"caution_designation_count,omitempty"`
	CautionExtensionCount   int                             `json:"caution_extension_count,omitempty"`
	CautionReleaseCount     int                             `json:"caution_release_count,omitempty"`
	ImportantTradingCount   int                             `json:"important_trading_count,omitempty"`
	ImportantRiskCount      int                             `json:"important_risk_count,omitempty"`
	ImportantOperationCount int                             `json:"important_operation_count,omitempty"`
	PublicationOrder        *domesticNoticePublicationOrder `json:"publication_order,omitempty"`
}

// Publication order describes stored search matches, never website completeness
// or event time. Only returned sources can be selected as answer citations.
type domesticNoticePublicationOrder struct {
	Scope                       string   `json:"scope"`
	LatestPublishedAtKST        string   `json:"latest_published_at_kst,omitempty"`
	LatestReturnedSourceIDs     []string `json:"latest_returned_source_ids"`
	LatestPublicationCount      int      `json:"latest_publication_count"`
	UnknownPublicationTimeCount int      `json:"unknown_publication_time_count"`
}

type domesticNoticeExchangeCount struct {
	Exchange string `json:"exchange"`
	Count    int    `json:"count"`
}

// Every filter is a conjunction; matching any filter excludes the notice.
// Keeping the predicates together avoids turning "exchange A asset B" into
// an exclusion of every notice from A and every notice concerning B.
type domesticNoticeSearchExclusion struct {
	Exchanges      []string `json:"exchanges,omitempty"`
	Assets         []string `json:"assets,omitempty"`
	Intents        []string `json:"intents,omitempty"`
	CautionAction  string   `json:"caution_action,omitempty"`
	CautionActions []string `json:"caution_actions,omitempty"`
	ListingMarkets []string `json:"listing_markets,omitempty"`
	QueryTerms     []string `json:"query_terms,omitempty"`
}

type domesticNoticeImageSearchSource struct {
	URL  string
	Text string
}

type domesticNoticeRetrievalQuery struct {
	Intent                   noticeQuestionIntent
	Intents                  []noticeQuestionIntent
	Terms                    []string
	RequireAllTerms          bool
	Exclusions               []domesticNoticeRetrievalQuery
	Exchanges                map[string]struct{}
	Assets                   map[string]struct{}
	ListingMarkets           map[string]struct{}
	WindowDays               int
	RequestedWindowDays      string
	WindowLimited            bool
	WindowCutoff             int64
	WindowEndExclusive       int64
	WindowLabel              string
	WindowStartKST           string
	WindowEndKSTExclusive    string
	WindowOpen               bool
	InvalidCalendarDate      bool
	StrictCautionDesignation bool
	ExactCautionAction       bithumbWriterAction
	CautionActions           []bithumbWriterAction
	RequireNamedAsset        bool
	RequirePrimaryAsset      bool
}

type domesticNoticeRankedRows struct {
	items   []domesticNoticeEvidence
	scores  []int
	anchors []int64
}

func (rows domesticNoticeRankedRows) Len() int { return len(rows.items) }

func (rows domesticNoticeRankedRows) Less(i, j int) bool {
	if rows.scores[i] != rows.scores[j] {
		return rows.scores[i] > rows.scores[j]
	}
	if rows.anchors[i] != rows.anchors[j] {
		return rows.anchors[i] > rows.anchors[j]
	}
	return rows.items[i].SourceID < rows.items[j].SourceID
}

func (rows domesticNoticeRankedRows) Swap(i, j int) {
	rows.items[i], rows.items[j] = rows.items[j], rows.items[i]
	rows.scores[i], rows.scores[j] = rows.scores[j], rows.scores[i]
	rows.anchors[i], rows.anchors[j] = rows.anchors[j], rows.anchors[i]
}

func buildDomesticNoticeSearchEvidence(item notice.Item, sourceID string) domesticNoticeEvidence {
	title := strings.TrimSpace(item.OriginalTitle)
	if title == "" {
		title = item.Title
	}
	body := strings.TrimSpace(item.OriginalBodyText)
	if body == "" {
		body = strings.TrimSpace(item.BodyExcerpt)
	}
	summary := truncateNoticeQuestionText(item.Summary, 320)
	assets := domesticNoticeQuestionItemAssets(item)
	category := canonicalNoticeQuestionCategory(title, item.Category)
	effectiveItem := item
	effectiveItem.Title = title
	effectiveItem.Category = category
	actions := boundedNoticeQuestionActions(notice.EffectiveActions(effectiveItem))
	schedules := domesticNoticeQuestionItemSchedules(item, body, category)
	scheduleParts := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		scheduleParts = append(scheduleParts, strings.TrimSpace(strings.Join([]string{
			schedule.Kind, schedule.Timezone, schedule.Precision, schedule.Evidence,
		}, " ")))
	}
	scheduleText := strings.Join(scheduleParts, "\n")
	riskText := strings.Join(item.RiskTags, " ")
	imageSources := domesticNoticeImageSearchSources(item)
	imageParts := make([]string, 0, len(imageSources))
	for _, image := range imageSources {
		imageParts = append(imageParts, image.Text)
	}
	searchText := strings.ToLower(strings.Join([]string{
		item.Title, item.OriginalTitle, category, strings.Join(assets, " "), summary,
		body, noticeQuestionActionSearchText(actions), scheduleText, riskText, strings.Join(imageParts, "\n"),
	}, "\n"))
	updatedAtKST := ""
	if item.UpdatedTSMS > 0 && item.UpdatedTSMS != item.PublishedTSMS {
		updatedAtKST = noticeQuestionKSTTimestamp(item.UpdatedTSMS)
	}
	return domesticNoticeEvidence{
		SourceID: sourceID, Exchange: item.Exchange, NoticeID: item.NoticeID,
		Title: title, URL: item.URL, Category: category,
		PublishedTSMS: item.PublishedTSMS, UpdatedTSMS: item.UpdatedTSMS, DetectedTSMS: item.DetectedTSMS,
		PublishedAtKST: noticeQuestionKSTTimestamp(item.PublishedTSMS),
		UpdatedAtKST:   updatedAtKST,
		Assets:         assets, Actions: actions,
		RiskTags: append([]string(nil), item.RiskTags...), Summary: summary,
		Schedules:      append([]notice.Schedule(nil), schedules...),
		AnalysisStatus: item.AnalysisStatus, Importance: item.Importance,
		searchText: searchText, bodyText: body, imageSources: imageSources,
		bodyOriginalAvailable: strings.TrimSpace(item.OriginalBodyText) != "",
		scheduleText:          scheduleText, riskText: riskText,
	}
}

func boundedNoticeQuestionActions(actions []notice.Action) []notice.Action {
	result := make([]notice.Action, 0, min(len(actions), noticeQuestionActionLimit))
	for _, action := range actions {
		action.Evidence = truncateNoticeQuestionText(action.Evidence, noticeQuestionSnippetRunes)
		result = append(result, action)
		if len(result) == noticeQuestionActionLimit {
			break
		}
	}
	return result
}

func noticeQuestionActionSearchText(actions []notice.Action) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, strings.Join([]string{action.Subject, action.Verb, action.Evidence}, " "))
	}
	return strings.Join(parts, "\n")
}

func domesticNoticeQuestionItemSchedules(item notice.Item, body, category string) []notice.Schedule {
	if len(item.Schedules) > 0 {
		return append([]notice.Schedule(nil), item.Schedules...)
	}
	status := strings.ToUpper(strings.TrimSpace(item.AnalysisStatus))
	if status != "ANALYSIS_FAILED" && status != "ANALYSIS_DEFERRED_QUOTA" &&
		status != "TRANSLATION_PENDING" && status != "ANALYSIS_NOT_REQUIRED" {
		return []notice.Schedule{}
	}
	if !noticeQuestionItemMatches(item.Title, category, noticeQuestionIntentDepositStop) {
		return []notice.Schedule{}
	}
	reference := item.PublishedTSMS
	if reference <= 0 {
		reference = item.DetectedTSMS
	}
	return notice.ExtractSchedules(strings.TrimSpace(item.Title+"\n"+body), category, reference)
}

func domesticNoticeImageSearchSources(item notice.Item) []domesticNoticeImageSearchSource {
	result := make([]domesticNoticeImageSearchSource, 0, len(item.ImageAnalysis))
	seen := map[string]struct{}{}
	officialURLs := make(map[string]bool, len(item.ImageURLs))
	for _, url := range item.ImageURLs {
		officialURLs[strings.TrimSpace(url)] = true
	}
	appendSource := func(url, text string) {
		url, text = strings.TrimSpace(url), strings.TrimSpace(text)
		if text == "" || url == "" || (len(officialURLs) > 0 && !officialURLs[url]) {
			return
		}
		key := url + "\x00" + text
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		result = append(result, domesticNoticeImageSearchSource{URL: url, Text: text})
	}
	for _, row := range item.ImageAnalysis {
		if !strings.EqualFold(noticeQuestionMapString(row, "status"), "ANALYZED") {
			continue
		}
		// ANALYZED transcripts are tied to downloaded official bytes by the
		// ingestion runner. Semantic interpretation, translation and signals
		// remain derived output; they cannot become Source=IMAGE fact authority.
		appendSource(noticeQuestionMapString(row, "url"), noticeQuestionMapString(row, "text"))
	}
	// ImageText is a legacy aggregate with no per-image provenance. Rebuild
	// exclusively from the transcript rows instead of trusting that copy.
	return result
}

func noticeQuestionMapString(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return strings.TrimSpace(value)
}

func noticeQuestionMapStrings(value any) []string {
	result := []string{}
	switch rows := value.(type) {
	case []string:
		result = append(result, rows...)
	case []any:
		for _, row := range rows {
			if text, ok := row.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
	}
	return result
}

func noticeQuestionKSTTimestamp(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.UnixMilli(value).In(seoulLocation).Format("2006-01-02 15:04:05 MST")
}

func retrieveDomesticNoticeQuestionRows(
	items []domesticNoticeEvidence,
	question string,
	contextSourceIDs []string,
	now time.Time,
) ([]domesticNoticeEvidence, domesticNoticeRetrieval) {
	query := buildDomesticNoticeRetrievalQuery(items, question, now)
	candidateLimit := noticeQuestionLatestCandidateLimit(question)
	result := make([]domesticNoticeEvidence, 0, min(len(items), noticeQuestionNoticeLimit))
	seen := map[string]struct{}{}
	appendRow := func(item domesticNoticeEvidence) {
		if len(result) == candidateLimit {
			return
		}
		if _, duplicate := seen[item.SourceID]; duplicate {
			return
		}
		seen[item.SourceID] = struct{}{}
		item = projectDomesticNoticePromptRow(item, query)
		result = append(result, item)
	}

	explicitContextAssets := len(query.Assets) > 0
	contextApplied := len(contextSourceIDs) > 0 && (noticeQuestionUsesConversationContext(question, query) ||
		(explicitContextAssets && noticeQuestionNeedsVolume("", question)))
	// A follow-up that points at verified prior sources is always an
	// intersection, never permission to reopen the complete 30-day catalog.
	// Structured venue, asset, intent, action, market, and time constraints are
	// applied to those sources below. Lexical synthesis words such as "의미" do
	// not disqualify an otherwise valid referenced notice.
	scanCatalog := !contextApplied
	contextCandidates := map[string]struct{}(nil)
	var contextRows []domesticNoticeEvidence
	if contextApplied {
		// Building a 30-day source-ID map was one of the largest per-question
		// allocations. Follow-up context is the only path that needs random ID
		// lookup, so ordinary questions now skip it entirely.
		byID := make(map[string]domesticNoticeEvidence, len(items))
		for _, item := range items {
			byID[item.SourceID] = item
		}
		contextCandidates = make(map[string]struct{}, len(contextSourceIDs))
		contextQuery := query
		contextQuery.Terms = nil
		// query.Assets is enriched from included context rows for downstream
		// volume/ambiguity handling. Keep that mutation out of the refinement
		// predicate; an explicit asset is checked conservatively just below.
		contextQuery.Assets = map[string]struct{}{}
		contextQuery.RequirePrimaryAsset = false
		for _, id := range contextSourceIDs {
			if item, ok := byID[id]; ok {
				if explicitContextAssets && !noticeQuestionPrimaryAssetMatch(item, query.Assets) {
					continue
				}
				if !domesticNoticeRetrievalMatch(item, contextQuery) {
					continue
				}
				if _, duplicate := contextCandidates[item.SourceID]; duplicate {
					continue
				}
				contextCandidates[item.SourceID] = struct{}{}
				contextRows = append(contextRows, item)
				if explicitContextAssets {
					continue
				}
				for _, asset := range domesticNoticeEvidencePrimaryAssets(item) {
					asset = strings.ToUpper(strings.TrimSpace(asset))
					if asset != "" {
						query.Assets[asset] = struct{}{}
					}
				}
			}
		}
	}
	if contextApplied {
		if candidateLimit < noticeQuestionNoticeLimit {
			sortDomesticNoticePublicationRows(contextRows)
		}
		for _, item := range contextRows {
			appendRow(item)
		}
	}

	matches := make([]domesticNoticeEvidence, 0, len(items))
	if scanCatalog {
		for _, item := range items {
			if domesticNoticeRetrievalMatch(item, query) {
				matches = append(matches, item)
			}
		}
	}
	backoffUsed := false
	structuredBackoffUsed := false
	if scanCatalog && len(matches) == 0 && len(query.Terms) > 0 &&
		!fallbackNoticeQuestionWantsCount(question) && !noticeQuestionNeedsVolume("", question) {
		// An unfamiliar wording must not turn the product into a closed intent
		// allowlist. Relax only free-text terms while preserving every structured
		// exchange, asset, period, market and lifecycle constraint, then rank the
		// bounded candidates by character overlap and recency. The model receives
		// an explicit backoff flag and must not describe these as exact matches.
		// A named asset, or an explicit exchange plus lifecycle, is already a
		// strong retrieval anchor. Unseen presentation language must not hide
		// that evidence merely because it shares no character bigrams. This is
		// candidate retrieval, not proof that the requested fact exists. Exact
		// counts/volumes never broaden their matching predicate in this way.
		anchored := len(query.Assets) > 0 || (len(query.Exchanges) > 0 && query.Intent != "")
		for _, item := range items {
			if !domesticNoticeRetrievalBackoffMatch(item, query) {
				continue
			}
			score := domesticNoticeFuzzyScore(item, query.Terms)
			if score > 0 || anchored {
				matches = append(matches, item)
				structuredBackoffUsed = structuredBackoffUsed || score == 0
			}
		}
		if len(matches) > 0 {
			backoffUsed = true
			sortDomesticNoticeBackoffRows(matches, query)
		}
	}
	if !backoffUsed {
		sortDomesticNoticeRetrievalRows(matches, query)
	}
	matches = domesticNoticeDiverseOrder(matches, query)
	if candidateLimit < noticeQuestionNoticeLimit {
		sortDomesticNoticePublicationRows(matches)
	}
	for _, item := range matches {
		appendRow(item)
	}
	matchedCount := len(matches)
	eligibleCount := len(items)
	exchangeCounts := domesticNoticeExchangeCounts(matches)
	if contextApplied {
		matchedCount = len(contextCandidates)
		eligibleCount = len(contextSourceIDs)
		exchangeCounts = domesticNoticeExchangeCounts(contextRows)
	}
	publicationRows := matches
	if contextApplied {
		publicationRows = contextRows
	}

	strategy := "DETERMINISTIC_MULTIMODAL_LEXICAL_V4"
	if backoffUsed {
		strategy = "DETERMINISTIC_MULTIMODAL_FUZZY_BACKOFF_V4"
	}
	if structuredBackoffUsed {
		strategy = "DETERMINISTIC_STRUCTURED_BACKOFF_V6"
	}
	retrieval := domesticNoticeRetrieval{
		Strategy: strategy, BackoffUsed: backoffUsed, InvalidCalendarDate: query.InvalidCalendarDate,
		EligibleNoticeCount: eligibleCount, MatchedNoticeCount: matchedCount,
		ReturnedNoticeCount: len(result), CandidateLimit: candidateLimit,
		Intent: string(query.Intent), QueryTerms: append([]string(nil), query.Terms...),
		Intents: domesticNoticeIntentNames(query.Intents), ExchangeNoticeCounts: exchangeCounts,
		CautionAction: string(query.ExactCautionAction), CautionActions: domesticNoticeCautionActionNames(query.CautionActions),
		Exchanges: sortedNoticeQuestionSet(query.Exchanges), Assets: sortedNoticeQuestionSet(query.Assets),
		ExcludedFilters: domesticNoticeExcludedFilterEvidence(query.Exclusions),
		ListingMarkets:  sortedNoticeQuestionSet(query.ListingMarkets),
		WindowDays:      query.WindowDays, WindowLabel: query.WindowLabel,
		RequestedWindowDays: query.RequestedWindowDays, WindowLimited: query.WindowLimited,
		WindowStartKST: query.WindowStartKST, WindowEndKSTExclusive: query.WindowEndKSTExclusive,
		WindowOpen: query.WindowOpen, RequireNamedAsset: query.RequireNamedAsset,
		RequirePrimaryAsset: query.RequirePrimaryAsset, ContextApplied: contextApplied,
		Truncated:        matchedCount > len(result),
		PublicationOrder: domesticNoticeMatchedPublicationOrder(publicationRows, result),
	}
	return result, retrieval
}

func sortDomesticNoticePublicationRows(rows []domesticNoticeEvidence) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].PublishedTSMS != rows[j].PublishedTSMS {
			return rows[i].PublishedTSMS > rows[j].PublishedTSMS
		}
		return rows[i].SourceID < rows[j].SourceID
	})
}

// An explicit latest-N request spends the body budget on those N documents.
// Counts and publication-order facts still describe every matching stored row.
func noticeQuestionLatestCandidateLimit(question string) int {
	text := strings.ToLower(noticeQuestionPositiveSearchText(question))
	if fallbackNoticeQuestionWantsCount(question) || !containsNoticeQuestionAny(text, "최신", "최근순", "latest", "newest") {
		return noticeQuestionNoticeLimit
	}
	if noticeQuestionSingleCountPattern.MatchString(text) {
		return 1
	}
	count := noticeQuestionDisplayCountPattern.FindStringSubmatch(text)
	if len(count) < 2 {
		return noticeQuestionNoticeLimit
	}
	native := map[string]int{"한": 1, "두": 2, "세": 3, "네": 4, "다섯": 5, "여섯": 6, "일곱": 7, "여덟": 8, "아홉": 9, "열": 10}
	n := native[count[1]]
	if n == 0 {
		n, _ = strconv.Atoi(count[1])
	}
	if n < 1 || n > noticeQuestionNoticeLimit {
		return noticeQuestionNoticeLimit
	}
	return n
}

func domesticNoticeMatchedPublicationOrder(matches, returned []domesticNoticeEvidence) *domesticNoticePublicationOrder {
	if len(matches) == 0 {
		return nil
	}
	order := &domesticNoticePublicationOrder{Scope: "MATCHED_STORED_NOTICES", LatestReturnedSourceIDs: []string{}}
	var latest int64
	for _, row := range matches {
		if row.PublishedTSMS <= 0 {
			order.UnknownPublicationTimeCount++
		} else if row.PublishedTSMS > latest {
			latest = row.PublishedTSMS
			order.LatestPublicationCount = 1
		} else if row.PublishedTSMS == latest {
			order.LatestPublicationCount++
		}
	}
	if latest > 0 {
		order.LatestPublishedAtKST = noticeQuestionKSTTimestamp(latest)
		for _, row := range returned {
			if row.PublishedTSMS == latest {
				order.LatestReturnedSourceIDs = append(order.LatestReturnedSourceIDs, row.SourceID)
			}
		}
	}
	return order
}

// domesticNoticeDiverseOrder preserves deterministic relevance order while
// reserving the front of broad domestic queries for one result per exchange.
// It is intentionally disabled for explicit exchange or asset requests, where
// diversity would weaken the user's constraint. No result is removed here, so
// matched/truncated counts still describe the complete candidate set.
func domesticNoticeDiverseOrder(items []domesticNoticeEvidence, query domesticNoticeRetrievalQuery) []domesticNoticeEvidence {
	// A comparison must retain a source for each requested type even when
	// recent rows of one type would otherwise occupy every model slot.
	if len(query.Intents) > 1 || len(query.CautionActions) > 1 {
		target := 0
		appendRepresentative := func(intent noticeQuestionIntent, action bithumbWriterAction) {
			for index := target; index < len(items); index++ {
				if !domesticNoticeEvidenceMatchesIntent(items[index], intent) ||
					(action != "" && domesticNoticeEvidenceCautionAction(items[index]) != action) {
					continue
				}
				candidate := items[index]
				copy(items[target+1:index+1], items[target:index])
				items[target] = candidate
				target++
				break
			}
		}
		intents := query.Intents
		if len(intents) == 0 {
			intents = []noticeQuestionIntent{query.Intent}
		}
		for _, intent := range intents {
			if intent == noticeQuestionIntentCaution && len(query.CautionActions) > 1 {
				for _, action := range query.CautionActions {
					appendRepresentative(intent, action)
				}
			} else {
				appendRepresentative(intent, "")
			}
		}
		return items
	}
	if len(items) < 2 || len(query.Exchanges) > 0 || len(query.Assets) > 0 || query.Intent == "" {
		return items
	}
	seenExchange := make(map[string]struct{}, len(domesticNoticeExchangeOrder))
	for target := 0; target < len(items); target++ {
		candidateIndex := -1
		for index := target; index < len(items); index++ {
			exchange := strings.ToLower(strings.TrimSpace(items[index].Exchange))
			if _, seen := seenExchange[exchange]; !seen {
				candidateIndex = index
				break
			}
		}
		if candidateIndex < 0 {
			break
		}
		candidate := items[candidateIndex]
		copy(items[target+1:candidateIndex+1], items[target:candidateIndex])
		items[target] = candidate
		seenExchange[strings.ToLower(strings.TrimSpace(candidate.Exchange))] = struct{}{}
	}
	return items
}

func projectDomesticNoticePromptRow(item domesticNoticeEvidence, query domesticNoticeRetrievalQuery) domesticNoticeEvidence {
	item.Actions = boundedDomesticNoticePromptActions(item.Actions, query)
	item.Schedules = boundedDomesticNoticeQuestionSchedules(item.Schedules, query.Terms)
	item.MatchedEvidence = domesticNoticeMatchedSnippets(item, query.Terms)
	item.Summary = truncateNoticeQuestionText(item.Summary, noticeQuestionPromptSummaryRunes)
	if len(item.MatchedEvidence) > 0 && len(query.Terms) > 0 {
		// A query-local official snippet is stronger and usually contains the
		// same sentence as the generic summary. Do not pay for both.
		item.Summary = ""
	}
	if len(item.RiskTags) > 6 {
		item.RiskTags = append([]string(nil), item.RiskTags[:6]...)
	} else {
		item.RiskTags = append([]string(nil), item.RiskTags...)
	}
	return item
}

func boundedDomesticNoticePromptActions(values []notice.Action, query domesticNoticeRetrievalQuery) []notice.Action {
	result := make([]notice.Action, 0, min(len(values), noticeQuestionPromptActionLimit))
	selected := make([]bool, len(values))
	appendMatching := func(requireMatch bool) {
		for index, value := range values {
			if len(result) == noticeQuestionPromptActionLimit || selected[index] {
				continue
			}
			searchable := value.Subject + " " + value.Verb + " " + value.Evidence
			matched := actionMatchesNoticeQuery(value, query) ||
				(len(query.Terms) > 0 && noticeQuestionTermMatchCount(searchable, query.Terms) > 0)
			if matched != requireMatch {
				continue
			}
			selected[index] = true
			value.Evidence = truncateNoticeQuestionText(
				noticeQuestionEvidenceSnippet(value.Evidence, query.Terms),
				noticeQuestionPromptSnippetRunes,
			)
			result = append(result, value)
		}
	}
	appendMatching(true)
	appendMatching(false)
	return result
}

func actionMatchesNoticeIntent(value notice.Action, intent noticeQuestionIntent) bool {
	subject := strings.ToUpper(strings.TrimSpace(value.Subject))
	switch intent {
	case noticeQuestionIntentListing, noticeQuestionIntentDelisting:
		return subject == "TRADING_SUPPORT"
	case noticeQuestionIntentCaution:
		return subject == "CAUTION"
	case noticeQuestionIntentDepositStop, noticeQuestionIntentDepositResume, noticeQuestionIntentDepositStatus:
		return subject == "DEPOSIT_WITHDRAWAL"
	case noticeQuestionIntentEvent:
		return subject == "EVENT"
	case noticeQuestionIntentMaintenance:
		return subject == "MAINTENANCE"
	case noticeQuestionIntentImportant:
		return subject == "TRADING_SUPPORT" || subject == "CAUTION" || subject == "DEPOSIT_WITHDRAWAL" ||
			subject == "SECURITY" || subject == "MAINTENANCE"
	default:
		return false
	}
}

func actionMatchesNoticeQuery(value notice.Action, query domesticNoticeRetrievalQuery) bool {
	if len(query.Intents) == 0 {
		return actionMatchesNoticeIntent(value, query.Intent)
	}
	for _, intent := range query.Intents {
		if actionMatchesNoticeIntent(value, intent) {
			return true
		}
	}
	return false
}

func domesticNoticeExchangeCounts(rows []domesticNoticeEvidence) []domesticNoticeExchangeCount {
	counts := make(map[string]int)
	seen := make(map[string]bool)
	for _, row := range rows {
		if row.SourceID != "" && seen[row.SourceID] {
			continue
		}
		seen[row.SourceID] = true
		exchange := strings.ToLower(strings.TrimSpace(row.Exchange))
		if exchange != "" {
			counts[exchange]++
		}
	}
	result := make([]domesticNoticeExchangeCount, 0, len(counts))
	for exchange, count := range counts {
		result = append(result, domesticNoticeExchangeCount{Exchange: exchange, Count: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Exchange < result[j].Exchange })
	return result
}

func domesticNoticeRetrievalBackoffMatch(item domesticNoticeEvidence, query domesticNoticeRetrievalQuery) bool {
	relaxed := query
	relaxed.Terms = nil
	return domesticNoticeRetrievalMatch(item, relaxed)
}

func sortDomesticNoticeBackoffRows(items []domesticNoticeEvidence, query domesticNoticeRetrievalQuery) {
	ranked := domesticNoticeRankedRows{
		items: items, scores: make([]int, len(items)), anchors: make([]int64, len(items)),
	}
	for index, item := range items {
		ranked.scores[index] = domesticNoticeFuzzyScore(item, query.Terms)
		ranked.anchors[index] = domesticNoticeEvidenceAnchorMS(item)
	}
	sort.Stable(ranked)
}

func domesticNoticeFuzzyScore(item domesticNoticeEvidence, terms []string) int {
	title := strings.ToLower(item.Title)
	metadata := strings.ToLower(strings.Join([]string{
		item.Category, strings.Join(item.Assets, " "), item.Summary,
		noticeQuestionActionSearchText(item.Actions), item.scheduleText, item.riskText,
	}, "\n"))
	body := strings.ToLower(item.bodyText)
	score := 0
	for _, term := range terms {
		grams := noticeQuestionRuneBigrams(term)
		for _, gram := range grams {
			switch {
			case strings.Contains(title, gram):
				score += 12
			case strings.Contains(metadata, gram):
				score += 6
			case strings.Contains(body, gram):
				score += 2
			}
		}
	}
	return score
}

func noticeQuestionRuneBigrams(value string) []string {
	runes := []rune(strings.TrimSpace(strings.ToLower(value)))
	if len(runes) < 2 {
		return nil
	}
	result := make([]string, 0, len(runes)-1)
	seen := map[string]struct{}{}
	for index := 0; index+1 < len(runes); index++ {
		gram := string(runes[index : index+2])
		if _, duplicate := seen[gram]; duplicate {
			continue
		}
		seen[gram] = struct{}{}
		result = append(result, gram)
	}
	return result
}

func domesticNoticeEvidencePrimaryAssets(item domesticNoticeEvidence) []string {
	if assets := notice.TitleAssets(item.Title); len(assets) > 0 {
		return assets
	}
	return normalizedNoticeQuestionAssets(item.Assets)
}

func noticeQuestionPrimaryAssetMatch(item domesticNoticeEvidence, wanted map[string]struct{}) bool {
	for _, asset := range domesticNoticeEvidencePrimaryAssets(item) {
		if _, ok := wanted[strings.ToUpper(strings.TrimSpace(asset))]; ok {
			return true
		}
	}
	return false
}

func boundedDomesticNoticeQuestionSchedules(values []notice.Schedule, terms []string) []notice.Schedule {
	result := make([]notice.Schedule, 0, min(len(values), noticeQuestionScheduleLimit))
	selected := make([]bool, len(values))
	appendMatching := func(requireMatch bool) {
		for index, value := range values {
			if len(result) == noticeQuestionScheduleLimit || selected[index] {
				continue
			}
			searchable := strings.Join([]string{value.Kind, value.Timezone, value.Precision, value.Evidence}, " ")
			matched := len(terms) > 0 && noticeQuestionTermMatchCount(searchable, terms) > 0
			if matched != requireMatch {
				continue
			}
			selected[index] = true
			value.Evidence = noticeQuestionEvidenceSnippet(value.Evidence, terms)
			value.Evidence = truncateNoticeQuestionText(value.Evidence, noticeQuestionScheduleEvidenceRunes)
			result = append(result, value)
		}
	}
	appendMatching(true)
	appendMatching(false)
	return result
}

func noticeQuestionUsesConversationContext(question string, query domesticNoticeRetrievalQuery) bool {
	text := strings.ToLower(strings.TrimSpace(question))
	if noticeQuestionSwitchPattern.MatchString(text) {
		return false
	}
	// Demonstratives refer to the preceding source independently of the noun
	// chosen by the user (event, condition, schedule, policy, etc.). New target
	// filters still intersect that source set in the caller.
	if noticeQuestionReferencePattern.MatchString(text) {
		return true
	}
	for _, phrase := range []string{
		"그 코인", "그 자산", "해당 코인", "해당 자산", "그 공지", "해당 공지", "이 공지",
		"위 공지", "앞서", "앞에서", "방금", "그거", "그것", "그중", "그 중", "이중", "이 중",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return query.Intent == "" && len(query.Terms) == 0 && len(query.Exchanges) == 0 &&
		len(query.Assets) == 0 && query.WindowDays == 0 && query.WindowCutoff == 0 && query.WindowEndExclusive == 0
}

func buildDomesticNoticeRetrievalQuery(items []domesticNoticeEvidence, question string, now time.Time) domesticNoticeRetrievalQuery {
	positive, excluded := noticeQuestionSearchClauses(question)
	text := strings.ToLower(strings.TrimSpace(positive))
	query := domesticNoticeRetrievalQuery{
		Intent:          noticeQuestionQueryIntentFor(positive),
		Terms:           noticeQuestionSearchTerms(positive),
		RequireAllTerms: fallbackNoticeQuestionWantsCount(question) || noticeQuestionNeedsVolume("", question),
		Exchanges:       map[string]struct{}{}, Assets: map[string]struct{}{}, ListingMarkets: map[string]struct{}{},
	}
	if match := noticeQuestionCountContinuationPattern.FindStringIndex(positive); match != nil {
		// A coordinated explanation after the count operator does not narrow
		// the counted set. The full question still selects body passages.
		query.Terms = noticeQuestionSearchTerms(positive[:match[0]])
	}
	query.Intents = domesticNoticeJoinedIntents(positive, query.Intent)
	if len(query.Intents) > 1 {
		query.Intent = query.Intents[0]
	}
	listingIntent := query.Intent
	if domesticNoticeQueryHasIntent(query, noticeQuestionIntentListing) {
		listingIntent = noticeQuestionIntentListing
	}
	query.ListingMarkets = noticeQuestionListingMarkets(text, listingIntent)
	query.StrictCautionDesignation = domesticNoticeQueryHasIntent(query, noticeQuestionIntentCaution) &&
		containsNoticeQuestionAny(text, "거래유의종목", "거래 유의종목", "거래 유의 종목", "유의종목", "유의 종목") &&
		!containsNoticeQuestionAny(text, "유의촉구", "유의 촉구")
	if domesticNoticeQueryHasIntent(query, noticeQuestionIntentCaution) {
		query.ExactCautionAction = noticeQuestionExactCautionAction(text, noticeQuestionIntentCaution)
		query.CautionActions = noticeQuestionJoinedCautionActions(text)
		if len(query.CautionActions) > 1 {
			query.ExactCautionAction = ""
			for _, action := range query.CautionActions {
				if action == bithumbWriterActionCautionUrge {
					query.StrictCautionDesignation = false
				}
			}
		}
	}
	query.RequireNamedAsset = domesticNoticeQueryHasIntent(query, noticeQuestionIntentDelisting) &&
		containsNoticeQuestionAny(text, "코인", "자산", "종목")
	for exchange, aliases := range noticeQuestionExchangeAliases {
		for _, alias := range aliases {
			if strings.Contains(text, strings.ToLower(alias)) {
				query.Exchanges[exchange] = struct{}{}
				break
			}
		}
	}
	knownAssets := map[string]struct{}{}
	for _, item := range items {
		for _, asset := range item.Assets {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset != "" {
				knownAssets[asset] = struct{}{}
			}
		}
	}
	for _, asset := range noticeQuestionExplicitAssets(positive, knownAssets) {
		query.Assets[asset] = struct{}{}
	}
	for market := range query.ListingMarkets {
		// In "KRW 마켓 상장" the quote currency is a market constraint, not
		// the requested asset. Keep BTC as an asset in ordinary "BTC 거래량"
		// questions by removing it only when the explicit market phrase exists.
		delete(query.Assets, market)
	}
	if noticeQuestionNeedsVolume("", question) {
		// This product reports domestic notional in KRW. In phrases such as
		// "국내 KRW 거래대금", KRW is the quote/measurement currency, not a
		// requested crypto asset. A malformed historical notice can still put
		// KRW in the global known-asset set, so remove it explicitly here.
		delete(query.Assets, "KRW")
	}
	query.RequirePrimaryAsset = noticeQuestionNeedsVolume("", question) && len(query.Assets) > 0
	query.Terms = noticeQuestionSpecificTerms(query.Terms, query)
	if (len(query.Intents) > 1 || len(query.CautionActions) > 1) && domesticNoticeQueryHasIntent(query, noticeQuestionIntentCaution) {
		query.Terms = noticeQuestionWithoutPresentationTerms(query.Terms, "지정", "연장", "해제", "촉구")
	}
	if query.Intent == noticeQuestionIntentEvent &&
		strings.Contains(text, "종료") && strings.Contains(text, "연장") &&
		containsNoticeQuestionAny(text, "여부", "상태", "구분", "분류") {
		query.Terms = noticeQuestionWithoutPresentationTerms(query.Terms, "종료", "연장", "여부", "상태", "구분", "분류")
	}
	if query.Intent == noticeQuestionIntentCaution &&
		containsNoticeQuestionAny(text, "상태별", "상태 별", "지정·연장", "지정/연장", "지정 및 연장") {
		query.Terms = noticeQuestionWithoutPresentationTerms(query.Terms, "지정", "연장", "해제", "상태", "구분", "분류", "현황")
	}
	for _, clause := range excluded {
		// Reuse the same positive grammar for a negative predicate. The splitter
		// has removed its exclusion operator, so this call cannot recurse on it.
		filter := buildDomesticNoticeRetrievalQuery(items, clause, now)
		// A bare action after an explicitly scoped caution set inherits that
		// type ("유의 지정과 해제 중 해제 제외"). Apply the action predicate,
		// not a body-text exclusion that could erase designation notices merely
		// because their explanation mentions a later release.
		if domesticNoticeQueryHasIntent(query, noticeQuestionIntentCaution) && filter.Intent == "" && len(filter.Terms) > 0 &&
			len(noticeQuestionWithoutPresentationTerms(filter.Terms, "지정", "연장", "해제", "촉구")) == 0 {
			filter.Intent = noticeQuestionIntentCaution
			filter.ExactCautionAction = noticeQuestionExactCautionAction(clause, noticeQuestionIntentCaution)
			filter.CautionActions = noticeQuestionJoinedCautionActions(clause)
			if len(filter.CautionActions) > 1 {
				filter.ExactCautionAction = ""
			}
			filter.Terms = nil
		}
		filter.WindowDays, filter.WindowCutoff, filter.WindowEndExclusive = 0, 0, 0
		filter.WindowLabel, filter.WindowStartKST, filter.WindowEndKSTExclusive = "", "", ""
		filter.RequestedWindowDays, filter.WindowLimited, filter.InvalidCalendarDate = "", false, false
		filter.RequireAllTerms = true
		if len(filter.Exchanges) > 0 || len(filter.Assets) > 0 || filter.Intent != "" || len(filter.Terms) > 0 {
			query.Exclusions = append(query.Exclusions, filter)
		}
	}
	// Exclusions restrict targets, not the requested publication window. The
	// period can precede the negative clause ("최근 7일 빗썸 제외 ...").
	window := noticeQuestionTimeWindowFor(strings.ToLower(question), now)
	query.InvalidCalendarDate = window.Invalid
	query.WindowDays = window.Days
	query.RequestedWindowDays, query.WindowLimited = window.RequestedDays, window.Limited
	query.WindowCutoff = window.StartMS
	query.WindowEndExclusive = window.EndExclusiveMS
	query.WindowLabel = window.Label
	query.WindowStartKST = window.StartKST
	query.WindowEndKSTExclusive = window.EndKSTExclusive
	query.WindowOpen = window.Open
	for _, pattern := range []*regexp.Regexp{noticeQuestionISODatePattern, noticeQuestionKoreanDatePattern} {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if !noticeQuestionCalendarDatePartsValid(match[1], match[2], match[3]) {
				query.InvalidCalendarDate = true
			}
		}
	}
	return query
}

func domesticNoticeExcludedFilterEvidence(filters []domesticNoticeRetrievalQuery) []domesticNoticeSearchExclusion {
	result := make([]domesticNoticeSearchExclusion, 0, len(filters))
	for _, filter := range filters {
		intents := filter.Intents
		if len(intents) == 0 && filter.Intent != "" {
			intents = []noticeQuestionIntent{filter.Intent}
		}
		result = append(result, domesticNoticeSearchExclusion{
			Exchanges: sortedNoticeQuestionSet(filter.Exchanges), Assets: sortedNoticeQuestionSet(filter.Assets),
			Intents: domesticNoticeIntentNames(intents), CautionAction: string(filter.ExactCautionAction),
			CautionActions: domesticNoticeCautionActionNames(filter.CautionActions),
			ListingMarkets: sortedNoticeQuestionSet(filter.ListingMarkets), QueryTerms: append([]string(nil), filter.Terms...),
		})
	}
	return result
}

// Coordinated noun phrases form a union of explicit requested types. Reuse
// the existing type parser for each clause instead of allowing its first
// matching type to erase the other side of a comparison. Single-type and
// existing combined deposit-status questions retain their original predicate.
func domesticNoticeJoinedIntents(question string, primary noticeQuestionIntent) []noticeQuestionIntent {
	if primary == noticeQuestionIntentDepositStatus || primary == noticeQuestionIntentImportant {
		return nil
	}
	positive := noticeQuestionPositiveSearchText(question)
	clauses := noticeQuestionJoinPattern.Split(positive, -1)
	seen := make(map[noticeQuestionIntent]bool)
	var result []noticeQuestionIntent
	for _, clause := range clauses {
		intent := noticeQuestionQueryIntentFor(clause)
		if intent != "" && !seen[intent] {
			seen[intent] = true
			result = append(result, intent)
		}
	}
	if len(result) < 2 {
		return nil
	}
	return result
}

// A lifecycle-qualified shorthand has the same domain meaning as its full
// notice type. Normalize that noun phrase before both single- and multi-type
// planning; bare "유의사항" remains ordinary notice prose.
func noticeQuestionQueryIntentFor(question string) noticeQuestionIntent {
	canonical := noticeQuestionCautionLifecyclePattern.ReplaceAllStringFunc(question, func(phrase string) string {
		return strings.Replace(phrase, "유의", "거래유의", 1)
	})
	return noticeQuestionIntentFor(canonical)
}

// Only adjacent, explicitly coordinated action nouns form a selected set.
// Intervening prose such as "지정 사유와 해제 조건" is not a list of notice
// actions. "지정 해제" remains the single release action without a connector.
func noticeQuestionJoinedCautionActions(question string) []bithumbWriterAction {
	matches := noticeQuestionCautionActionPattern.FindAllStringIndex(question, -1)
	seen := make(map[bithumbWriterAction]bool)
	var result []bithumbWriterAction
	for start := 0; start < len(matches); {
		end := start
		for end+1 < len(matches) && noticeQuestionCautionActionJoinPattern.MatchString(question[matches[end][1]:matches[end+1][0]]) {
			end++
		}
		if end > start && noticeQuestionCautionActionStartPattern.MatchString(question[:matches[start][0]]) &&
			noticeQuestionCautionActionEndPattern.MatchString(question[matches[end][1]:]) {
			for index := start; index <= end; index++ {
				phrase := question[matches[index][0]:matches[index][1]]
				action := noticeQuestionExactCautionAction("유의 "+phrase, noticeQuestionIntentCaution)
				if action != "" && !seen[action] {
					seen[action] = true
					result = append(result, action)
				}
			}
		}
		start = end + 1
	}
	if len(result) < 2 {
		return nil
	}
	return result
}

func domesticNoticeCautionActionNames(actions []bithumbWriterAction) []string {
	if len(actions) == 0 {
		return nil
	}
	result := make([]string, len(actions))
	for index, action := range actions {
		result[index] = string(action)
	}
	return result
}

func domesticNoticeIntentNames(intents []noticeQuestionIntent) []string {
	if len(intents) == 0 {
		return nil
	}
	result := make([]string, len(intents))
	for index, intent := range intents {
		result[index] = string(intent)
	}
	return result
}

func domesticNoticeQueryHasIntent(query domesticNoticeRetrievalQuery, wanted noticeQuestionIntent) bool {
	if len(query.Intents) == 0 {
		return query.Intent == wanted
	}
	for _, intent := range query.Intents {
		if intent == wanted {
			return true
		}
	}
	return false
}

func noticeQuestionExactCautionAction(text string, intent noticeQuestionIntent) bithumbWriterAction {
	if intent != noticeQuestionIntentCaution || noticeQuestionWantsCautionStatusBreakdown(text) {
		return ""
	}
	switch {
	case containsNoticeQuestionAny(text, "유의촉구", "유의 촉구"):
		return bithumbWriterActionCautionUrge
	case strings.Contains(text, "해제"):
		return bithumbWriterActionCautionRelease
	case strings.Contains(text, "연장"):
		return bithumbWriterActionCautionExtension
	case strings.Contains(text, "지정"):
		return bithumbWriterActionCautionDesignation
	default:
		return ""
	}
}

func noticeQuestionListingMarkets(text string, intent noticeQuestionIntent) map[string]struct{} {
	result := map[string]struct{}{}
	if intent != noticeQuestionIntentListing {
		return result
	}
	text = strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(text, "원화 마켓") || strings.Contains(text, "원화마켓") {
		result["KRW"] = struct{}{}
	}
	for _, match := range noticeQuestionMarketListPattern.FindAllStringSubmatch(strings.ToUpper(text), -1) {
		if len(match) < 2 {
			continue
		}
		for _, market := range strings.Split(match[1], ",") {
			market = strings.ToUpper(strings.TrimSpace(market))
			if market != "" {
				result[market] = struct{}{}
			}
		}
	}
	return result
}

func noticeQuestionWithoutPresentationTerms(terms []string, ignored ...string) []string {
	result := make([]string, 0, len(terms))
	for _, term := range terms {
		drop := false
		for _, value := range ignored {
			if term == value || strings.HasPrefix(term, value) {
				drop = true
				break
			}
		}
		if !drop {
			result = append(result, term)
		}
	}
	return result
}

func noticeQuestionSearchTerms(question string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	positiveText := noticeQuestionSearchTextWithoutTimeWindow(noticeQuestionPositiveSearchText(question))
	positiveText = noticeQuestionDisplayCountPattern.ReplaceAllString(positiveText, " ")
	for _, raw := range noticeQuestionSearchTermPattern.FindAllString(strings.ToLower(positiveText), -1) {
		term := noticeQuestionNormalizeSearchTerm(strings.Trim(raw, "._-"))
		if utf8.RuneCountInString(term) < 2 {
			continue
		}
		if noticeQuestionIsSearchStopTerm(term) {
			continue
		}
		if noticeQuestionDaysPattern.MatchString(term) {
			continue
		}
		if _, duplicate := seen[term]; duplicate {
			continue
		}
		seen[term] = struct{}{}
		result = append(result, term)
	}
	return result
}

// Calendar expressions constrain publication time and must not also become
// lexical requirements. Without this separation, a question such as "오늘
// 상장 공지" searches every notice body for the word "오늘" and commonly
// returns no exact candidates even when notices were published today.
func noticeQuestionSearchTextWithoutTimeWindow(value string) string {
	value = noticeQuestionISODatePattern.ReplaceAllStringFunc(value, func(candidate string) string {
		match := noticeQuestionISODatePattern.FindStringSubmatch(candidate)
		if len(match) == 4 && noticeQuestionCalendarDatePartsValid(match[1], match[2], match[3]) {
			return " "
		}
		return candidate
	})
	value = noticeQuestionKoreanDatePattern.ReplaceAllStringFunc(value, func(candidate string) string {
		match := noticeQuestionKoreanDatePattern.FindStringSubmatch(candidate)
		if len(match) == 4 && noticeQuestionCalendarDatePartsValid(match[1], match[2], match[3]) {
			return " "
		}
		return candidate
	})
	value = noticeQuestionDaysPattern.ReplaceAllString(value, " ")
	for _, phrase := range []string{
		"오늘자", "오늘", "어제", "이번 주", "이번주", "지난 주", "지난주",
		"이번 달", "이번달", "지난 달", "지난달", "최근 일주일", "지난 일주일",
		"일주일", "최근 한 달", "최근 한달", "지난 한 달", "지난 한달", "한 달", "한달",
	} {
		value = strings.ReplaceAll(value, phrase, " ")
	}
	return value
}

// Both structured predicates and lexical terms use the same clause polarity.
func noticeQuestionPositiveSearchText(question string) string {
	positive, _ := noticeQuestionSearchClauses(question)
	return positive
}

func noticeQuestionSearchClauses(question string) (string, []string) {
	clauses := strings.FieldsFunc(question, func(character rune) bool {
		switch character {
		case '.', '?', '!', ';', '\n', '\r', ',':
			return true
		default:
			return false
		}
	})
	result := make([]string, 0, len(clauses))
	var excluded []string
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		lower := strings.ToLower(clause)
		markerIndex, markerLength := -1, 0
		for _, marker := range []string{
			"제외해주세요", "제외해줘", "제외하고", "제외해서", "제외한", "제외",
			"빼 주세요", "빼줘", "빼 줘", "빼고",
		} {
			for start := 0; start < len(lower); {
				index := strings.Index(lower[start:], marker)
				if index < 0 {
					break
				}
				index += start
				start = index + len(marker)
				if strings.HasPrefix(lower[index:], "제외하지") {
					continue
				}
				if marker == "제외" {
					// A bare noun in "참여 제외 조건" describes a notice fact;
					// it is not an instruction to exclude the preceding event.
					suffix := strings.TrimSpace(lower[start:])
					if suffix != "" && !strings.HasPrefix(suffix, "해") {
						continue
					}
				}
				if markerIndex < 0 || index < markerIndex {
					markerIndex, markerLength = index, len(marker)
				}
				break
			}
		}
		if markerIndex < 0 {
			result = append(result, clause)
			continue
		}
		negative := strings.TrimSpace(clause[:markerIndex])
		// In a selected set, the text before "중" remains positive context:
		// "업비트와 빗썸 중 빗썸 제외" excludes only the final selector.
		if scopes := noticeQuestionExclusionScopePattern.FindAllStringIndex(negative, -1); len(scopes) > 0 {
			last := scopes[len(scopes)-1]
			result = append(result, strings.TrimSpace(negative[:last[0]]))
			negative = strings.TrimSpace(negative[last[1]:])
		}
		if negative != "" {
			excluded = append(excluded, negative)
		}
		// In "A는 제외하고 B" only B is a positive retrieval clause. If
		// the marker ends the clause, nothing from that negative clause is used.
		if suffix := strings.TrimSpace(clause[markerIndex+markerLength:]); suffix != "" {
			positiveSuffix, negativeSuffix := noticeQuestionSearchClauses(suffix)
			if positiveSuffix != "" {
				result = append(result, positiveSuffix)
			}
			excluded = append(excluded, negativeSuffix...)
		}
	}
	// Preserve clause coordination for the multi-intent parser as well.
	return strings.Join(result, " , "), excluded
}

func noticeQuestionNormalizeSearchTerm(term string) string {
	term = strings.TrimSpace(strings.ToLower(term))
	if noticeQuestionIsKoreanCounterTerm(term) {
		return term
	}
	for _, suffix := range []string{
		"으로부터", "에게서", "에서는", "으로는", "까지는", "부터는", "에게는", "한테는",
		"이라고", "이라는", "이나", "라는", "라고", "에서", "에게", "한테", "으로", "로", "까지", "부터", "처럼", "보다",
		"에는", "에도", "은", "는", "이", "가", "을", "를", "의", "에", "와", "과", "도", "만",
		"나",
	} {
		if !strings.HasSuffix(term, suffix) {
			continue
		}
		base := strings.TrimSuffix(term, suffix)
		if utf8.RuneCountInString(base) >= 2 {
			return base
		}
	}
	return term
}

func noticeQuestionSpecificTerms(terms []string, query domesticNoticeRetrievalQuery) []string {
	ignored := map[string]struct{}{}
	for asset := range query.Assets {
		ignored[strings.ToLower(asset)] = struct{}{}
	}
	for exchange := range query.Exchanges {
		ignored[exchange] = struct{}{}
		for _, alias := range noticeQuestionExchangeAliases[exchange] {
			ignored[strings.ToLower(alias)] = struct{}{}
		}
	}
	intents := query.Intents
	if len(intents) == 0 {
		intents = []noticeQuestionIntent{query.Intent}
	}
	for _, intent := range intents {
		for _, term := range noticeQuestionIntentTerms(intent) {
			ignored[term] = struct{}{}
		}
	}
	result := make([]string, 0, len(terms))
	for _, term := range terms {
		if !noticeQuestionTermCoveredBySet(term, ignored) {
			result = append(result, term)
		}
	}
	return result
}

func noticeQuestionIsSearchStopTerm(term string) bool {
	if noticeQuestionIsKoreanCounterTerm(term) {
		return true
	}
	for _, stem := range noticeQuestionPresentationStems {
		if strings.HasPrefix(term, stem) {
			return true
		}
	}
	if _, stop := noticeQuestionSearchStopWords[term]; stop {
		return true
	}
	for stop := range noticeQuestionSearchStopWords {
		if utf8.RuneCountInString(stop) < 2 || !strings.HasPrefix(term, stop) {
			continue
		}
		suffix := strings.TrimPrefix(term, stop)
		switch suffix {
		case "은", "는", "이", "가", "을", "를", "의", "에", "에서", "로", "으로", "와", "과", "도", "만", "인가",
			"들", "들은", "들을", "들의", "들에", "들에서", "들로", "들과", "들도", "들만", "들이":
			return true
		}
	}
	return false
}

func noticeQuestionIsKoreanCounterTerm(term string) bool {
	term = strings.TrimSpace(term)
	term = strings.TrimPrefix(term, "몇")
	for _, counter := range []string{"건", "개"} {
		if !strings.HasPrefix(term, counter) {
			continue
		}
		switch strings.TrimPrefix(term, counter) {
		case "", "수", "이야", "인가", "인가요", "입니까", "이에요", "예요", "이죠", "이나", "이나요", "이고", "이며":
			return true
		}
	}
	return false
}

func noticeQuestionTermCoveredBySet(term string, values map[string]struct{}) bool {
	if _, exact := values[term]; exact {
		return true
	}
	for value := range values {
		if utf8.RuneCountInString(value) < 2 {
			continue
		}
		if strings.HasPrefix(term, value) || strings.HasPrefix(value, term) {
			return true
		}
	}
	return false
}

func noticeQuestionIntentTerms(intent noticeQuestionIntent) []string {
	switch intent {
	case noticeQuestionIntentListing:
		return []string{"상장", "신규", "거래지원", "거래지원된", "거래지원한", "마켓", "추가"}
	case noticeQuestionIntentDelisting:
		return []string{"상장폐지", "거래지원", "종료", "제거"}
	case noticeQuestionIntentCaution:
		return []string{"유의종목", "유의", "거래유의", "투자유의"}
	case noticeQuestionIntentDepositStop:
		return []string{"입출금", "입금", "출금", "중단", "중지", "차단"}
	case noticeQuestionIntentDepositResume:
		return []string{"입출금", "입금", "출금", "재개", "완료"}
	case noticeQuestionIntentDepositStatus:
		return []string{"입출금", "입금", "출금", "중단", "중지", "차단", "재개", "완료", "상태", "현황", "현재", "가능", "여부"}
	case noticeQuestionIntentImportant:
		return []string{"중요", "중요한", "주요", "핵심", "변화", "공지"}
	case noticeQuestionIntentEvent:
		return []string{"이벤트", "에어드랍", "프로모션", "혜택", "챌린지", "페스타"}
	case noticeQuestionIntentMaintenance:
		return []string{"점검", "유지보수", "maintenance"}
	default:
		return nil
	}
}

func noticeQuestionWindowDays(text string) int {
	if match := noticeQuestionDaysPattern.FindStringSubmatch(text); len(match) == 3 {
		if days, err := strconv.Atoi(match[2]); err != nil || days > noticeQuestionHistoryDays {
			return noticeQuestionHistoryDays
		} else if days >= 1 {
			return days
		}
	}
	if strings.Contains(text, "한 달") || strings.Contains(text, "한달") || strings.Contains(text, "30일") {
		return noticeQuestionHistoryDays
	}
	if strings.Contains(text, "최근") {
		return noticeQuestionRecentDays
	}
	return 0
}

type noticeQuestionTimeWindow struct {
	Invalid         bool
	Days            int
	RequestedDays   string
	Limited         bool
	StartMS         int64
	EndExclusiveMS  int64
	Label           string
	StartKST        string
	EndKSTExclusive string
	Open            bool
}

// noticeQuestionTimeWindowFor resolves calendar words in Asia/Seoul before
// falling back to the existing rolling N-day window. Named calendar windows
// carry stable human-readable boundaries into model evidence; rolling windows
// retain only WindowDays so the answer cache is not invalidated by the request
// clock when the selected official rows are byte-equivalent.
func noticeQuestionTimeWindowFor(text string, now time.Time) noticeQuestionTimeWindow {
	text = strings.ToLower(strings.TrimSpace(text))
	text = noticeQuestionPublicationDateText(text)
	if window, ok := noticeQuestionExplicitRangeWindow(text, now); ok {
		return window
	}
	nowKST := now.In(seoulLocation)
	startOfToday := time.Date(nowKST.Year(), nowKST.Month(), nowKST.Day(), 0, 0, 0, 0, seoulLocation)
	calendar := func(start, end time.Time, label string, open bool) noticeQuestionTimeWindow {
		filterEnd := end.UnixMilli()
		if open && nowKST.Before(end) {
			// Keep the stable calendar boundary in model evidence and cache
			// identity, while refusing corrupted or future-dated rows that have
			// not actually been published as of this request.
			filterEnd = now.UnixMilli() + 1
		}
		return noticeQuestionTimeWindow{
			StartMS: start.UnixMilli(), EndExclusiveMS: filterEnd, Label: label,
			StartKST: start.Format(time.RFC3339), EndKSTExclusive: end.Format(time.RFC3339), Open: open,
		}
	}

	switch {
	case strings.Contains(text, "오늘"):
		return calendar(startOfToday, startOfToday.AddDate(0, 0, 1), "오늘(KST)", true)
	case strings.Contains(text, "어제"):
		start := startOfToday.AddDate(0, 0, -1)
		return calendar(start, startOfToday, "어제(KST)", false)
	case containsNoticeQuestionAny(text, "이번 주", "이번주"):
		weekdayOffset := (int(startOfToday.Weekday()) + 6) % 7
		start := startOfToday.AddDate(0, 0, -weekdayOffset)
		return calendar(start, start.AddDate(0, 0, 7), "이번 주(KST·월요일 시작)", true)
	case containsNoticeQuestionAny(text, "지난 주", "지난주"):
		weekdayOffset := (int(startOfToday.Weekday()) + 6) % 7
		end := startOfToday.AddDate(0, 0, -weekdayOffset)
		return calendar(end.AddDate(0, 0, -7), end, "지난주(KST·월요일 시작)", false)
	case containsNoticeQuestionAny(text, "이번 달", "이번달"):
		start := time.Date(nowKST.Year(), nowKST.Month(), 1, 0, 0, 0, 0, seoulLocation)
		return calendar(start, start.AddDate(0, 1, 0), "이번 달(KST)", true)
	case containsNoticeQuestionAny(text, "지난 달", "지난달"):
		end := time.Date(nowKST.Year(), nowKST.Month(), 1, 0, 0, 0, 0, seoulLocation)
		return calendar(end.AddDate(0, -1, 0), end, "지난달(KST)", false)
	}

	if date, ok := noticeQuestionExplicitKSTDate(text, nowKST); ok {
		end := date.AddDate(0, 0, 1)
		open := date.Equal(startOfToday)
		return calendar(date, end, date.Format("2006-01-02")+"(KST)", open)
	}
	if noticeQuestionISODatePattern.MatchString(text) || noticeQuestionKoreanDatePattern.MatchString(text) {
		// An impossible explicit date must not be reinterpreted as a rolling
		// window from its day component (for example, February 30 -> 30 days).
		return noticeQuestionTimeWindow{}
	}

	days := noticeQuestionWindowDays(text)
	if containsNoticeQuestionAny(text, "일주일") {
		days = 7
	}
	if days == 0 {
		return noticeQuestionTimeWindow{}
	}
	window := noticeQuestionTimeWindow{
		Days: days, StartMS: now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli(),
		EndExclusiveMS: now.UnixMilli() + 1,
	}
	if match := noticeQuestionDaysPattern.FindStringSubmatch(text); len(match) == 3 {
		window.RequestedDays = match[2]
		requested, err := strconv.Atoi(match[2])
		window.Limited = err != nil || requested > noticeQuestionHistoryDays
	}
	return window
}

func noticeQuestionExplicitKSTDate(text string, nowKST time.Time) (time.Time, bool) {
	if match := noticeQuestionISODatePattern.FindStringSubmatch(text); len(match) == 4 {
		return noticeQuestionValidKSTDate(match[1], match[2], match[3], nowKST)
	}
	if match := noticeQuestionKoreanDatePattern.FindStringSubmatch(text); len(match) == 4 {
		return noticeQuestionValidKSTDate(match[1], match[2], match[3], nowKST)
	}
	return time.Time{}, false
}

// A date attached to an operation is a claim to inspect in the notice body,
// not a publication filter. Bare calendar lookups and explicitly posted dates
// keep the established calendar semantics. Both roles may occur in one query.
func noticeQuestionPublicationDateText(text string) string {
	var locations [][]int
	for _, pattern := range []*regexp.Regexp{noticeQuestionISODatePattern, noticeQuestionKoreanDatePattern} {
		locations = append(locations, pattern.FindAllStringIndex(text, -1)...)
	}
	sort.Slice(locations, func(i, j int) bool { return locations[i][0] < locations[j][0] })
	var result strings.Builder
	start := 0
	for _, location := range locations {
		if location[0] < start {
			continue
		}
		result.WriteString(text[start:location[0]])
		if noticeQuestionDateDescribesOperation(text, location[0], location[1]) {
			result.WriteByte(' ')
		} else {
			result.WriteString(text[location[0]:location[1]])
		}
		start = location[1]
	}
	result.WriteString(text[start:])
	return result.String()
}

func noticeQuestionDateDescribesOperation(text string, start, end int) bool {
	prefix := text[:start]
	if boundary := strings.LastIndexAny(prefix, ",;!?\n"); boundary >= 0 {
		prefix = prefix[boundary+1:]
	}
	suffix := text[end:]
	if boundary := strings.IndexAny(suffix, ",;!?\n"); boundary >= 0 {
		suffix = suffix[:boundary]
	}
	publication := []string{"게시", "등록", "발표", "발행", "올라온", "올린"}
	operations := []string{"입금", "출금", "지급", "마감", "신청", "참여", "적용", "시작", "종료", "중단", "재개", "정산"}
	lastPublication := strings.LastIndex(prefix, "공지")
	lastOperation := -1
	for _, word := range publication {
		lastPublication = max(lastPublication, strings.LastIndex(prefix, word))
	}
	for _, word := range operations {
		lastOperation = max(lastOperation, strings.LastIndex(prefix, word))
	}
	// A following publication predicate directly binds the date even when
	// an operation is named earlier: "출금 공지 중 9월 3일에 게시된 ...".
	firstPublication, firstOperation := len(suffix)+1, len(suffix)+1
	for _, word := range publication {
		if index := strings.Index(suffix, word); index >= 0 {
			firstPublication = min(firstPublication, index)
		}
	}
	for _, word := range operations {
		if index := strings.Index(suffix, word); index >= 0 {
			firstOperation = min(firstOperation, index)
		}
	}
	if firstPublication < firstOperation {
		return false
	}
	if lastOperation > lastPublication {
		return true
	}
	// With no preceding governor, an explicit temporal case particle can
	// bind the following operation; a bare "9월 3일 상장 공지" stays a lookup.
	after := strings.TrimSpace(suffix)
	return firstOperation < firstPublication &&
		(strings.HasPrefix(after, "에") || strings.HasPrefix(after, "부터") || strings.HasPrefix(after, "까지"))
}

func noticeQuestionValidKSTDate(yearText, monthText, dayText string, nowKST time.Time) (time.Time, bool) {
	month, monthErr := strconv.Atoi(monthText)
	day, dayErr := strconv.Atoi(dayText)
	if monthErr != nil || dayErr != nil {
		return time.Time{}, false
	}
	if strings.TrimSpace(yearText) != "" {
		year, err := strconv.Atoi(yearText)
		if err != nil {
			return time.Time{}, false
		}
		date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, seoulLocation)
		if date.Year() != year || int(date.Month()) != month || date.Day() != day {
			return time.Time{}, false
		}
		return date, true
	}
	// A month/day without a year denotes its most recent valid occurrence. This
	// handles January -> previous December and leap day without normalizing an
	// impossible calendar value into another month.
	for year := nowKST.Year(); year >= nowKST.Year()-4; year-- {
		date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, seoulLocation)
		if date.Year() == year && int(date.Month()) == month && date.Day() == day && !date.After(nowKST) {
			return date, true
		}
	}
	return time.Time{}, false
}

func noticeQuestionCalendarDatePartsValid(yearText, monthText, dayText string) bool {
	month, monthErr := strconv.Atoi(monthText)
	day, dayErr := strconv.Atoi(dayText)
	if monthErr != nil || dayErr != nil {
		return false
	}
	year := 2000 // leap year keeps a yearless February 29 syntactically valid.
	if strings.TrimSpace(yearText) != "" {
		parsed, err := strconv.Atoi(yearText)
		if err != nil {
			return false
		}
		year = parsed
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return date.Year() == year && int(date.Month()) == month && date.Day() == day
}

func domesticNoticeRetrievalMatch(item domesticNoticeEvidence, query domesticNoticeRetrievalQuery) bool {
	// Invalid explicit dates remain invalid under lexical backoff and context
	// refinement; neither is permission to remove a requested date constraint.
	if query.InvalidCalendarDate {
		return false
	}
	for _, excluded := range query.Exclusions {
		if domesticNoticeRetrievalMatch(item, excluded) {
			return false
		}
	}
	if query.WindowCutoff > 0 && domesticNoticeEvidenceAnchorMS(item) < query.WindowCutoff {
		return false
	}
	if query.WindowEndExclusive > 0 && domesticNoticeEvidenceAnchorMS(item) >= query.WindowEndExclusive {
		return false
	}
	if len(query.Exchanges) > 0 {
		if _, ok := query.Exchanges[strings.ToLower(strings.TrimSpace(item.Exchange))]; !ok {
			return false
		}
	}
	if len(query.Assets) > 0 && !noticeQuestionRowHasAnyAsset(item, query.Assets) {
		return false
	}
	intents := query.Intents
	if len(intents) == 0 {
		intents = []noticeQuestionIntent{query.Intent}
	}
	intentMatch := false
	for _, intent := range intents {
		if domesticNoticeMatchesQueryIntent(item, query, intent) {
			intentMatch = true
			break
		}
	}
	if !intentMatch {
		return false
	}
	if query.RequirePrimaryAsset && !noticeQuestionStrongAssetMatch(item, query.Assets) {
		return false
	}
	if len(query.Terms) > 0 {
		searchable := domesticNoticeSearchableText(item)
		minimumMatches := 1
		if query.RequireAllTerms {
			// A server count must not promote a model summary or semantic tag
			// into proof that the requested event occurred in an official source.
			searchable = domesticNoticeOfficialSearchableText(item)
			minimumMatches = len(query.Terms)
		} else if query.Intent == "" && len(query.Terms) >= 3 {
			minimumMatches = 2
		}
		if noticeQuestionTermMatchCount(searchable, query.Terms) < minimumMatches {
			return false
		}
	}
	return true
}

func domesticNoticeMatchesQueryIntent(item domesticNoticeEvidence, query domesticNoticeRetrievalQuery, intent noticeQuestionIntent) bool {
	if intent != "" && !domesticNoticeEvidenceMatchesIntent(item, intent) {
		return false
	}
	if len(query.Intents) == 0 || intent == noticeQuestionIntentCaution {
		if query.StrictCautionDesignation && !domesticNoticeEvidenceIsCautionDesignationFamily(item) {
			return false
		}
		if query.ExactCautionAction != "" && query.ExactCautionAction != domesticNoticeEvidenceCautionAction(item) {
			return false
		}
		if len(query.CautionActions) > 1 {
			matched := false
			for _, action := range query.CautionActions {
				matched = matched || action == domesticNoticeEvidenceCautionAction(item)
			}
			if !matched {
				return false
			}
		}
	}
	if query.RequireNamedAsset && (len(query.Intents) == 0 || intent == noticeQuestionIntentDelisting) && len(domesticNoticeEvidencePrimaryAssets(item)) == 0 {
		return false
	}
	if len(query.ListingMarkets) > 0 && (len(query.Intents) == 0 || intent == noticeQuestionIntentListing) && !noticeQuestionListingMarketMatch(item.Title, query.ListingMarkets) {
		return false
	}
	return true
}

func noticeQuestionListingMarketMatch(title string, wanted map[string]struct{}) bool {
	available := map[string]struct{}{}
	text := strings.ToLower(title)
	if strings.Contains(text, "원화 마켓") || strings.Contains(text, "원화마켓") {
		available["KRW"] = struct{}{}
	}
	for _, match := range noticeQuestionMarketListPattern.FindAllStringSubmatch(strings.ToUpper(title), -1) {
		if len(match) < 2 {
			continue
		}
		for _, market := range strings.Split(match[1], ",") {
			available[strings.ToUpper(strings.TrimSpace(market))] = struct{}{}
		}
	}
	for market := range wanted {
		if _, exists := available[market]; exists {
			return true
		}
	}
	return false
}

func noticeQuestionStrongAssetMatch(item domesticNoticeEvidence, wanted map[string]struct{}) bool {
	for _, asset := range notice.TitleAssets(item.Title) {
		if _, exists := wanted[strings.ToUpper(strings.TrimSpace(asset))]; exists {
			return true
		}
	}
	if len(item.Assets) != 1 {
		return false
	}
	_, exists := wanted[strings.ToUpper(strings.TrimSpace(item.Assets[0]))]
	return exists
}

func sortDomesticNoticeRetrievalRows(items []domesticNoticeEvidence, query domesticNoticeRetrievalQuery) {
	ranked := domesticNoticeRankedRows{
		items: items, scores: make([]int, len(items)), anchors: make([]int64, len(items)),
	}
	for index, item := range items {
		ranked.scores[index] = domesticNoticeRetrievalScore(item, query)
		ranked.anchors[index] = domesticNoticeEvidenceAnchorMS(item)
	}
	sort.Stable(ranked)
}

func domesticNoticeRetrievalScore(item domesticNoticeEvidence, query domesticNoticeRetrievalQuery) int {
	score := 0
	if query.Intent != "" {
		score += 500
	}
	if len(query.Exchanges) > 0 {
		score += 400
	}
	if len(query.Assets) > 0 && noticeQuestionRowHasAnyAsset(item, query.Assets) {
		score += 700
	}
	if len(query.Terms) > 0 {
		searchable := domesticNoticeSearchableText(item)
		title := strings.ToLower(item.Title)
		summary := strings.ToLower(item.Summary)
		schedule := strings.ToLower(item.scheduleText)
		risk := strings.ToLower(item.riskText)
		body := strings.ToLower(item.bodyText)
		image := ""
		for _, source := range item.imageSources {
			image += "\n" + strings.ToLower(source.Text)
		}
		metadata := strings.ToLower(strings.Join(item.Assets, " ") + " " + item.Category + " " + risk)
		for _, term := range query.Terms {
			switch {
			case strings.Contains(title, term):
				score += 140
			case strings.Contains(metadata, term):
				score += 120
			case strings.Contains(summary, term):
				score += 90
			case strings.Contains(schedule, term):
				score += 80
			case strings.Contains(image, term):
				score += 70
			case strings.Contains(body, term):
				score += 60
			}
		}
		if len(query.Terms) > 1 && noticeQuestionTermMatchCount(searchable, query.Terms) == len(query.Terms) {
			score += 120
		}
	}
	// For a concrete notice type, every row already passed that intent filter;
	// recency must break otherwise equal matches. Importance is only a useful
	// tie-breaker for open-ended lexical discovery.
	if query.Intent == "" && strings.EqualFold(item.Importance, "IMPORTANT") {
		score += 5
	}
	return score
}

func noticeQuestionRowHasAnyAsset(item domesticNoticeEvidence, selected map[string]struct{}) bool {
	for _, asset := range item.Assets {
		if _, ok := selected[strings.ToUpper(strings.TrimSpace(asset))]; ok {
			return true
		}
	}
	return false
}

func domesticNoticeSearchableText(item domesticNoticeEvidence) string {
	if strings.TrimSpace(item.searchText) != "" {
		return item.searchText
	}
	schedules := make([]string, 0, len(item.Schedules))
	for _, schedule := range item.Schedules {
		schedules = append(schedules, schedule.Kind+" "+schedule.Evidence)
	}
	return strings.ToLower(strings.Join([]string{
		item.Title, item.Category, strings.Join(item.Assets, " "), item.Summary,
		noticeQuestionActionSearchText(item.Actions), strings.Join(schedules, "\n"),
		item.bodyText, item.scheduleText, item.riskText,
	}, "\n"))
}

func domesticNoticeOfficialSearchableText(item domesticNoticeEvidence) string {
	parts := make([]string, 0, 2+len(item.imageSources))
	parts = append(parts, item.Title, item.bodyText)
	for _, source := range item.imageSources {
		parts = append(parts, source.Text)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func noticeQuestionTermMatchCount(text string, terms []string) int {
	text = strings.ToLower(text)
	count := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			count++
		}
	}
	return count
}

func domesticNoticeMatchedSnippets(item domesticNoticeEvidence, terms []string) []domesticNoticeMatchedEvidence {
	result := make([]domesticNoticeMatchedEvidence, 0, noticeQuestionSnippetLimit)
	seen := map[string]struct{}{}
	appendSnippet := func(source, text, imageURL string, requireMatch bool) {
		if len(result) == noticeQuestionSnippetLimit {
			return
		}
		if requireMatch && len(terms) > 0 && noticeQuestionTermMatchCount(text, terms) == 0 {
			return
		}
		snippet := noticeQuestionEvidenceSnippet(text, terms)
		if snippet == "" {
			return
		}
		key := source + "\x00" + snippet
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		result = append(result, domesticNoticeMatchedEvidence{Source: source, Text: snippet, ImageURL: imageURL})
	}

	appendSnippet("TITLE", item.Title, "", true)
	appendSnippet("BODY", item.bodyText, "", true)
	for _, image := range item.imageSources {
		appendSnippet("IMAGE", image.Text, image.URL, true)
	}
	appendSnippet("SCHEDULE", item.scheduleText, "", true)
	appendSnippet("RISK_TAG", item.riskText, "", true)
	appendSnippet("SUMMARY", item.Summary, "", true)
	if len(result) < 2 {
		appendSnippet("SUMMARY", item.Summary, "", false)
		appendSnippet("BODY", item.bodyText, "", false)
	}
	return result
}

func noticeQuestionEvidenceSnippet(value string, terms []string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lines := strings.FieldsFunc(value, func(character rune) bool {
		return character == '\n' || character == '\r'
	})
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" && (len(terms) == 0 || noticeQuestionTermMatchCount(line, terms) > 0) {
			return truncateNoticeQuestionText(line, noticeQuestionSnippetRunes)
		}
	}
	return truncateNoticeQuestionText(strings.Join(strings.Fields(value), " "), noticeQuestionSnippetRunes)
}

func sortedNoticeQuestionSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
