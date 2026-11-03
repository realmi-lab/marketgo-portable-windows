package notice

import "encoding/json"

const HistoryDays = 31

type Schedule struct {
	Kind      string `json:"kind"`
	StartTSMS int64  `json:"start_ts_ms"`
	EndTSMS   int64  `json:"end_ts_ms"`
	Timezone  string `json:"timezone,omitempty"`
	Precision string `json:"precision,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
}

type Action struct {
	Subject  string `json:"subject"`
	Verb     string `json:"verb"`
	Evidence string `json:"evidence"`
}

type BriefLine struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type Brief struct {
	FactSchema           string      `json:"fact_schema"`
	Kind                 string      `json:"kind"`
	DetailAvailable      bool        `json:"detail_available"`
	DetailSource         string      `json:"detail_source"`
	AnalysisStatus       string      `json:"analysis_status"`
	Headline             string      `json:"headline"`
	HeadlineSource       string      `json:"headline_source"`
	OCRUsed              bool        `json:"ocr_used"`
	OCRBlockedByConflict bool        `json:"ocr_blocked_by_conflict"`
	PeriodStartTSMS      *int64      `json:"period_start_ts_ms"`
	PeriodEndTSMS        *int64      `json:"period_end_ts_ms"`
	PeriodPrecision      string      `json:"period_precision"`
	Target               *string     `json:"target"`
	Reward               *string     `json:"reward"`
	Operation            *string     `json:"operation"`
	Assets               []string    `json:"assets"`
	Schedules            []Schedule  `json:"schedules"`
	TimelineSchedules    []Schedule  `json:"timeline_schedules"`
	Lines                []BriefLine `json:"lines"`
}

type Item struct {
	QualityV2          *PipelineQuality `json:"notice_quality_v2,omitempty"`
	Exchange           string           `json:"exchange"`
	NoticeID           string           `json:"notice_id"`
	Title              string           `json:"title"`
	URL                string           `json:"url"`
	Category           string           `json:"category"`
	PublishedTSMS      int64            `json:"published_ts_ms"`
	DetectedTSMS       int64            `json:"detected_ts_ms"`
	TimestampPrecision string           `json:"timestamp_precision"`
	Assets             []string         `json:"assets"`
	Actions            []Action         `json:"actions"`
	// TitleActions are deterministic lifecycle facts inferred only from the
	// complete official title. They are stored separately so callers can tell
	// them apart from grounded model Actions and old notices can be upgraded
	// without spending another LLM request.
	TitleActions       []Action         `json:"title_actions,omitempty"`
	SourceType         string           `json:"source_type"`
	UpdatedTSMS        int64            `json:"updated_ts_ms"`
	BodyExcerpt        string           `json:"body_excerpt"`
	Summary            string           `json:"summary"`
	Schedules          []Schedule       `json:"schedules"`
	RiskTags           []string         `json:"risk_tags"`
	ImageURLs          []string         `json:"image_urls"`
	ImageText          string           `json:"image_text"`
	ImageAnalysis      []map[string]any `json:"image_analysis"`
	ContentSource      string           `json:"content_source"`
	AnalysisStatus     string           `json:"analysis_status"`
	AnalysisConfidence float64          `json:"analysis_confidence"`
	ContentHash        string           `json:"content_hash"`
	DetailCheckedTSMS  int64            `json:"detail_checked_ts_ms"`
	EventGroupID       string           `json:"event_group_id"`
	OriginalLanguage   string           `json:"original_language,omitempty"`
	OriginalTitle      string           `json:"original_title,omitempty"`
	OriginalBodyText   string           `json:"original_body_text,omitempty"`
	TranslatedTitleKO  string           `json:"translated_title_ko,omitempty"`
	TranslatedBodyKO   string           `json:"translated_body_text_ko,omitempty"`
	TranslationStatus  string           `json:"translation_status,omitempty"`
	Importance         string           `json:"importance,omitempty"`
	AnalysisProvider   string           `json:"analysis_provider,omitempty"`
	AnalysisModel      string           `json:"analysis_model,omitempty"`
	ReasoningEffort    string           `json:"reasoning_effort,omitempty"`
	EscalationReason   string           `json:"escalation_reason,omitempty"`
	AnalysisPolicy     string           `json:"analysis_policy_version,omitempty"`

	// These fields belong only to the immutable prepared market-evidence copy.
	// They never enter PostgreSQL or the public JSON contract.
	preparedImageConsistency ImageConsistency
	imageConsistencyPrepared bool
}

type Source struct {
	LastHistoryScanTS float64 `json:"last_history_scan_ts"`
	HistoryStatus     string  `json:"history_status"`
	HistoryNote       string  `json:"history_note"`
	OldestFetchedTSMS int64   `json:"oldest_fetched_ts_ms"`
	PagesScanned      int64   `json:"pages_scanned"`
	ErrorKind         string  `json:"error_kind"`
	Error             string  `json:"error"`
	ErrorStartedTS    float64 `json:"error_started_ts"`
	ErrorLastSeenTS   float64 `json:"error_last_seen_ts"`
	ErrorOccurrences  int64   `json:"error_occurrences"`
	BlockedUntilTS    float64 `json:"blocked_until_ts"`
}

type SourceError struct {
	Exchange        string  `json:"exchange"`
	ErrorKind       string  `json:"error_kind"`
	Message         string  `json:"message"`
	StartedTS       float64 `json:"started_ts"`
	LastSeenTS      float64 `json:"last_seen_ts"`
	ResolvedTS      float64 `json:"resolved_ts"`
	OccurrenceCount int64   `json:"occurrence_count"`
	BlockedUntilTS  float64 `json:"blocked_until_ts"`
	Active          bool    `json:"active"`
}

type Snapshot struct {
	ItemRevisions              map[string]string    `json:"-"`
	Version                    int64                `json:"version"`
	PollIntervalSec            float64              `json:"poll_interval_sec"`
	HistoryDays                int                  `json:"history_days"`
	OCRBackend                 string               `json:"ocr_backend"`
	HealthySourceCount         int                  `json:"healthy_source_count"`
	CompleteHistorySourceCount int                  `json:"complete_history_source_count"`
	ActiveErrorCount           int                  `json:"active_error_count"`
	ErrorHistory               []SourceError        `json:"error_history"`
	SourceCount                int                  `json:"source_count"`
	Sources                    map[string]Source    `json:"sources"`
	Items                      []Item               `json:"items"`
	ContextualItems            []any                `json:"-"`
	MarketEvidence             []MarketEvidenceItem `json:"-"`
	MarketEvidencePrepared     bool                 `json:"-"`
}

// MarshalJSON keeps the collector snapshot contract unchanged until analysis
// has attached window-specific market context. Analysis responses then expose
// the contextual rows without mutating the stored notice records.
func (snapshot Snapshot) MarshalJSON() ([]byte, error) {
	type snapshotAlias Snapshot
	if snapshot.ContextualItems == nil {
		return json.Marshal(snapshotAlias(snapshot))
	}
	// The explicit fields shadow the alias' stored Items field while every
	// other snapshot field is encoded directly from the same typed value. This
	// avoids encoding the full stored item set, decoding it into a map, and
	// encoding the contextual item set a second time.
	return json.Marshal(struct {
		snapshotAlias
		Items       []any `json:"items"`
		CausalClaim bool  `json:"causal_claim"`
	}{
		snapshotAlias: snapshotAlias(snapshot),
		Items:         snapshot.ContextualItems,
		CausalClaim:   false,
	})
}
