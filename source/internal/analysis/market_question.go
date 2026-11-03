package analysis

import (
	"errors"

	"market-share-go/internal/marketquestion"
	"market-share-go/internal/movement"
)

var ErrInvalidMarketQuestion = errors.New("market question must contain between 2 and 500 characters")
var ErrRestrictedLLMQuestion = errors.New("database internals and data-completeness questions are restricted")
var ErrMarketQuestionACDisabled = errors.New("quantitative-first market question path is disabled")
var ErrMarketQuestionEvidenceUnavailable = errors.New("retained market question evidence is unavailable")

// MaxMarketQuestionConversationTurns is the maximum number of completed
// question/answer exchanges that an interactive market-share request may
// carry forward. Numeric claims are still rebuilt from current server
// evidence on every turn; history is conversational context only.
const MaxMarketQuestionConversationTurns = 15

// MarketQuestionConversationTurn is one completed exchange remembered by the
// market-share LLM. The assistant field contains prose, not the server fact
// envelope, so old snapshots cannot be mistaken for current evidence.
type MarketQuestionConversationTurn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// MarketQuestionConversationScope carries only server-confirmed analysis
// conditions into a follow-up. It is separate from assistant prose so a model
// sentence can never become the next turn's metric, denominator or time basis.
type MarketQuestionConversationScope struct {
	Contract          string                           `json:"contract"`
	Query             MarketQuestionQuery              `json:"query"`
	EvidenceBundleID  string                           `json:"evidence_bundle_id"`
	Revision          marketquestion.RevisionIdentity  `json:"revision"`
	CreatedAtMS       int64                            `json:"created_at_ms"`
	FulfillmentStatus marketquestion.FulfillmentStatus `json:"fulfillment_status"`
	AsOfMS            int64                            `json:"as_of_ms"`
	Token             string                           `json:"token"`
}

type GroundedMarketQuestionRequest struct {
	Question          string                           `json:"question"`
	History           []MarketQuestionConversationTurn `json:"history,omitempty"`
	ConversationScope *MarketQuestionConversationScope `json:"conversation_scope,omitempty"`
}

type MarketQuestionValidationStatus string

const (
	MarketQuestionValidationSupported    MarketQuestionValidationStatus = "SUPPORTED"
	MarketQuestionValidationConflict     MarketQuestionValidationStatus = "CONFLICT"
	MarketQuestionValidationUndetermined MarketQuestionValidationStatus = "UNDETERMINED"
)

type MarketQuestionExplanationStatus string

const (
	MarketQuestionExplanationPending    MarketQuestionExplanationStatus = "PENDING"
	MarketQuestionExplanationAccepted   MarketQuestionExplanationStatus = "ACCEPTED"
	MarketQuestionExplanationPartial    MarketQuestionExplanationStatus = "PARTIAL"
	MarketQuestionExplanationFailed     MarketQuestionExplanationStatus = "FAILED"
	MarketQuestionExplanationTimeout    MarketQuestionExplanationStatus = "TIMEOUT"
	MarketQuestionExplanationCanceled   MarketQuestionExplanationStatus = "CANCELED"
	MarketQuestionExplanationUnverified MarketQuestionExplanationStatus = "UNVERIFIED"
)

type MarketQuestionOverallStatus string

const (
	MarketQuestionOverallPending  MarketQuestionOverallStatus = "PENDING_EXPLANATION"
	MarketQuestionOverallComplete MarketQuestionOverallStatus = "COMPLETE"
	MarketQuestionOverallPartial  MarketQuestionOverallStatus = "PARTIAL"
	MarketQuestionOverallFailed   MarketQuestionOverallStatus = "FAILED"
	MarketQuestionOverallTimeout  MarketQuestionOverallStatus = "TIMEOUT"
	MarketQuestionOverallCanceled MarketQuestionOverallStatus = "CANCELED"
)

type MarketQuestionClaimReference struct {
	FactID   string `json:"fact_id"`
	Relation string `json:"relation"`
	Text     string `json:"text"`
}

// MarketQuestionClaimBlock is the smallest explanation unit that may be
// published independently. Claims name typed facts and relations; the block
// text must still pass the server's semantic and numeric guards.
type MarketQuestionClaimBlock struct {
	Text   string                         `json:"text"`
	Claims []MarketQuestionClaimReference `json:"claims"`
}

type MarketQuestionExplanationBlock struct {
	Index     int                            `json:"index"`
	Text      string                         `json:"text,omitempty"`
	Status    MarketQuestionValidationStatus `json:"status"`
	Reason    string                         `json:"reason,omitempty"`
	ClaimRefs []string                       `json:"claim_refs"`
}

type MarketQuestionCallBudget struct {
	Plan               int `json:"plan"`
	Explanation        int `json:"explanation"`
	Repair             int `json:"repair"`
	SemanticValidation int `json:"semantic_validation,omitempty"`
	Total              int `json:"total"`
	MaxTotal           int `json:"max_total"`
}

type MarketQuestionTimingMS struct {
	RequestStarted        int64  `json:"request_started"`
	PlanningCompleted     *int64 `json:"planning_completed,omitempty"`
	QuantitativeReady     *int64 `json:"quantitative_ready,omitempty"`
	ExplanationStarted    *int64 `json:"explanation_started,omitempty"`
	ExplanationCompleted  *int64 `json:"explanation_completed,omitempty"`
	ValidationCompleted   *int64 `json:"validation_completed,omitempty"`
	QueueDuration         *int64 `json:"queue_duration,omitempty"`
	PlanDuration          *int64 `json:"plan_duration,omitempty"`
	RetrievalCalculation  *int64 `json:"retrieval_calculation_duration,omitempty"`
	SerializationDuration *int64 `json:"serialization_duration,omitempty"`
	ModelDuration         *int64 `json:"model_duration,omitempty"`
	ValidationDuration    *int64 `json:"validation_duration,omitempty"`
	Total                 *int64 `json:"total,omitempty"`
	EvidenceBytes         *int64 `json:"evidence_bytes,omitempty"`
}

type MarketQuestionEvidenceIdentity struct {
	ContractVersion   string                           `json:"contract_version"`
	BundleID          string                           `json:"bundle_id"`
	CatalogVersion    string                           `json:"catalog_version"`
	Revision          marketquestion.RevisionIdentity  `json:"revision"`
	FulfillmentStatus marketquestion.FulfillmentStatus `json:"fulfillment_status"`
	CreatedAtMS       int64                            `json:"created_at_ms"`
}

type MarketQuestionOrchestration struct {
	Mode               string                           `json:"mode"`
	OverallStatus      MarketQuestionOverallStatus      `json:"overall_status"`
	QuantitativeStatus string                           `json:"quantitative_status"`
	ExplanationStatus  MarketQuestionExplanationStatus  `json:"explanation_status"`
	ValidationStatus   MarketQuestionValidationStatus   `json:"validation_status"`
	Blocks             []MarketQuestionExplanationBlock `json:"blocks"`
	Calls              MarketQuestionCallBudget         `json:"calls"`
	TimingMS           MarketQuestionTimingMS           `json:"timing_ms"`
}

type MarketQuestionEvidenceRetention struct {
	Status      string `json:"status"`
	ExpiresAtMS *int64 `json:"expires_at_ms,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type MarketQuestionStage string

const (
	MarketQuestionStageQuantitativeReady MarketQuestionStage = "QUANTITATIVE_READY"
	MarketQuestionStageExplanationReady  MarketQuestionStage = "EXPLANATION_READY"
	MarketQuestionStageTerminal          MarketQuestionStage = "TERMINAL"
)

type MarketQuestionStageStatus string

const (
	MarketQuestionStageReady      MarketQuestionStageStatus = "READY"
	MarketQuestionStageAccepted   MarketQuestionStageStatus = "ACCEPTED"
	MarketQuestionStagePartial    MarketQuestionStageStatus = "PARTIAL"
	MarketQuestionStageFailed     MarketQuestionStageStatus = "FAILED"
	MarketQuestionStageTimeout    MarketQuestionStageStatus = "TIMEOUT"
	MarketQuestionStageCanceled   MarketQuestionStageStatus = "CANCELED"
	MarketQuestionStageUnverified MarketQuestionStageStatus = "UNVERIFIED"
)

type MarketQuestionStageEvent struct {
	Sequence    int                            `json:"sequence"`
	Stage       MarketQuestionStage            `json:"stage"`
	Status      MarketQuestionStageStatus      `json:"status"`
	EmittedAtMS int64                          `json:"emitted_at_ms"`
	Response    GroundedMarketQuestionResponse `json:"response"`
}

// MarketQuestionAnswer is a grounded explanation of the latest shared market
// analysis. Source is HERMES when the external orchestrator completed,
// HERMES_SOL_FALLBACK when Hermes retried with GPT-5.6 Sol, OPENROUTER when
// the optional privacy-restricted fixed free model completed, and
// SERVER_BRIEFING_FALLBACK when the deterministic server briefing was used.
type MarketQuestionAnswer struct {
	Answer      string                     `json:"answer"`
	KeyPoints   []string                   `json:"key_points"`
	Caveat      string                     `json:"caveat"`
	Source      string                     `json:"source"`
	Model       string                     `json:"model,omitempty"`
	AsOfMS      int64                      `json:"as_of_ms"`
	ClaimBlocks []MarketQuestionClaimBlock `json:"-"`
}

type MarketQuestionIntent string

const (
	MarketQuestionCurrentOfficialShare  MarketQuestionIntent = "CURRENT_OFFICIAL_SHARE"
	MarketQuestionCalendarDayShare      MarketQuestionIntent = "CALENDAR_DAY_SHARE"
	MarketQuestionDailyEndpointSeries   MarketQuestionIntent = "DAILY_ENDPOINT_SERIES"
	MarketQuestionHistoricalRange       MarketQuestionIntent = "HISTORICAL_RANGE_ANALYSIS"
	MarketQuestionOfficialWindowChange  MarketQuestionIntent = "OFFICIAL_WINDOW_CHANGE"
	MarketQuestionWSIntervalCompare     MarketQuestionIntent = "WS_INTERVAL_COMPARE"
	MarketQuestionAssetIntervalAnalysis MarketQuestionIntent = "ASSET_INTERVAL_ANALYSIS"
	MarketQuestionAssetCapture          MarketQuestionIntent = "ASSET_CAPTURE"
	MarketQuestionMovementExplanation   MarketQuestionIntent = "MOVEMENT_EXPLANATION"
	MarketQuestionNoticeReaction        MarketQuestionIntent = "NOTICE_REACTION"
	MarketQuestionDataQuality           MarketQuestionIntent = "DATA_QUALITY"
)

type MarketQuestionTimeUnit string

const (
	MarketQuestionTimeNone   MarketQuestionTimeUnit = "NONE"
	MarketQuestionTimeMinute MarketQuestionTimeUnit = "MINUTE"
	MarketQuestionTimeHour   MarketQuestionTimeUnit = "HOUR"
)

type MarketQuestionComparison = marketquestion.ComparisonID

const (
	MarketQuestionCurrentSnapshot           = marketquestion.ComparisonCurrentSnapshot
	MarketQuestionCalendarDayClose          = marketquestion.ComparisonCalendarDayClose
	MarketQuestionDailyRolling24hEndpoints  = marketquestion.ComparisonDailyRolling24hEndpoints
	MarketQuestionFixedHistoricalInterval   = marketquestion.ComparisonFixedHistoricalInterval
	MarketQuestionRolling24hEndpointDelta   = marketquestion.ComparisonRolling24hEndpointDelta
	MarketQuestionCurrentVsPreviousInterval = marketquestion.ComparisonCurrentPreviousInterval
)

type MarketQuestionMetric = marketquestion.MetricID

const (
	MarketQuestionMetricOverallShare   = marketquestion.MetricOverallBithumbShare
	MarketQuestionMetricTargetExchange = marketquestion.MetricTargetExchangeShareVolume
	MarketQuestionMetricAssetCombined  = marketquestion.MetricAssetCombined
	MarketQuestionMetricAssetCapture   = marketquestion.MetricAssetBithumbCapture
	MarketQuestionMetricAssetInternal  = marketquestion.MetricAssetShareWithinBithumb
	MarketQuestionMetricContribution   = marketquestion.MetricAssetTotalContribution
)

// MarketQuestionQuery is the deterministic interpretation of a natural-language
// question. Numeric facts are calculated later; no model participates in this
// parsing contract.
type MarketQuestionQuery struct {
	Intent               MarketQuestionIntent     `json:"intent"`
	TimeValue            int64                    `json:"time_value"`
	TimeUnit             MarketQuestionTimeUnit   `json:"time_unit"`
	Comparison           MarketQuestionComparison `json:"comparison"`
	Metric               MarketQuestionMetric     `json:"metric"`
	Exchange             string                   `json:"exchange,omitempty"`
	PeerExchange         string                   `json:"peer_exchange,omitempty"`
	Asset                string                   `json:"asset,omitempty"`
	ComparedAssets       []string                 `json:"compared_assets,omitempty"`
	PeriodAssumption     string                   `json:"period_assumption,omitempty"`
	MarketScope          string                   `json:"market_scope"`
	EndPolicy            string                   `json:"end_policy"`
	TargetDate           string                   `json:"target_date,omitempty"`
	TargetDates          []string                 `json:"target_dates,omitempty"`
	TargetStartMS        int64                    `json:"target_start_ms,omitempty"`
	TargetEndMS          int64                    `json:"target_end_ms,omitempty"`
	RequestedPathSec     int64                    `json:"requested_path_sec,omitempty"`
	AnalysisDepth        string                   `json:"analysis_depth,omitempty"`
	QuestionParts        []string                 `json:"question_parts,omitempty"`
	RequestedExplanation []string                 `json:"requested_explanation"`
}

// MarketQuestionTargetExchangeEvidence contains server-calculated facts for
// the exchange explicitly named by the user. It is deliberately separate from
// the legacy Bithumb fields so an Upbit, Coinone, Korbit, or GOPAX question can
// never silently inherit Bithumb's numerator.
type MarketQuestionTargetExchangeEvidence struct {
	Exchange             string   `json:"exchange"`
	PreviousVolumeKRW    *float64 `json:"previous_volume_krw"`
	CurrentVolumeKRW     *float64 `json:"current_volume_krw"`
	VolumeDeltaKRW       *float64 `json:"volume_delta_krw"`
	PreviousSharePct     *float64 `json:"previous_share_pct"`
	CurrentSharePct      *float64 `json:"current_share_pct"`
	ShareDeltaPP         *float64 `json:"share_delta_pp"`
	PreviousTradeCount   *int64   `json:"previous_trade_count"`
	CurrentTradeCount    *int64   `json:"current_trade_count"`
	OfficialTickerStatus string   `json:"official_ticker_status,omitempty"`
	Official24hVolumeKRW *float64 `json:"official_24h_volume_krw"`
	Official24hSharePct  *float64 `json:"official_24h_share_pct"`
}

// MarketQuestionIntervalWindow is one fully closed WebSocket-observation
// interval. Numeric fields are pointers because an incomplete interval must
// not be presented as a confirmed market fact.
type MarketQuestionIntervalWindow struct {
	StartMS            int64    `json:"start_ms"`
	EndMS              int64    `json:"end_ms"`
	DomesticVolumeKRW  *float64 `json:"domestic_volume_krw"`
	BithumbVolumeKRW   *float64 `json:"bithumb_volume_krw"`
	BithumbSharePct    *float64 `json:"bithumb_share_pct"`
	DomesticTradeCount *int64   `json:"domestic_trade_count"`
	BithumbTradeCount  *int64   `json:"bithumb_trade_count"`
}

type MarketQuestionIntervalCoverage struct {
	Status         string `json:"status"`
	CoverageSec    int64  `json:"coverage_sec"`
	RequiredSec    int64  `json:"required_sec"`
	GapSeconds     int64  `json:"gap_seconds"`
	WindowComplete bool   `json:"window_complete"`
}

type MarketQuestionAssetInterval struct {
	Asset                                 string   `json:"asset"`
	CanonicalAssetID                      string   `json:"canonical_asset_id"`
	ContractIdentityStatus                string   `json:"contract_identity_status"`
	ContractIdentityVerified              bool     `json:"contract_identity_verified"`
	ObservedExchanges                     []string `json:"observed_exchanges"`
	CurrentBithumbCapturePct              *float64 `json:"current_bithumb_capture_pct"`
	PreviousBithumbCapturePct             *float64 `json:"previous_bithumb_capture_pct"`
	BithumbCaptureChangePP                *float64 `json:"bithumb_capture_change_pp"`
	CurrentShareWithinBithumbPct          *float64 `json:"current_share_within_bithumb_pct"`
	PreviousShareWithinBithumbPct         *float64 `json:"previous_share_within_bithumb_pct"`
	ShareWithinBithumbChangePP            *float64 `json:"share_within_bithumb_change_pp"`
	CurrentTotalShareContributionPct      *float64 `json:"current_total_share_contribution_pct"`
	PreviousTotalShareContributionPct     *float64 `json:"previous_total_share_contribution_pct"`
	TotalShareContributionChangePP        *float64 `json:"total_share_contribution_change_pp"`
	ChangeAttributionPP                   *float64 `json:"change_attribution_pp"`
	CurrentDomesticVolumeKRW              *float64 `json:"current_domestic_volume_krw"`
	PreviousDomesticVolumeKRW             *float64 `json:"previous_domestic_volume_krw"`
	CurrentBithumbVolumeKRW               *float64 `json:"current_bithumb_volume_krw"`
	PreviousBithumbVolumeKRW              *float64 `json:"previous_bithumb_volume_krw"`
	CurrentTradeCount                     *int64   `json:"current_trade_count"`
	PreviousTradeCount                    *int64   `json:"previous_trade_count"`
	CurrentAggressorBuyPct                *float64 `json:"current_aggressor_buy_pct"`
	PreviousAggressorBuyPct               *float64 `json:"previous_aggressor_buy_pct"`
	AggressorBuyChangePP                  *float64 `json:"aggressor_buy_change_pp"`
	CurrentDirectionalCoveragePct         *float64 `json:"current_directional_coverage_pct"`
	PreviousDirectionalCoveragePct        *float64 `json:"previous_directional_coverage_pct"`
	CurrentBithumbAggressorBuyPct         *float64 `json:"current_bithumb_aggressor_buy_pct"`
	PreviousBithumbAggressorBuyPct        *float64 `json:"previous_bithumb_aggressor_buy_pct"`
	BithumbAggressorBuyChangePP           *float64 `json:"bithumb_aggressor_buy_change_pp"`
	CurrentBithumbDirectionalCoveragePct  *float64 `json:"current_bithumb_directional_coverage_pct"`
	PreviousBithumbDirectionalCoveragePct *float64 `json:"previous_bithumb_directional_coverage_pct"`
}

// MarketQuestionIntervalEvidence is calculated exclusively by Go from one
// atomic aggregator read. The language model may explain these values but may
// not derive or repair them.
type MarketQuestionIntervalEvidence struct {
	CandleComparison        *MarketQuestionCandleComparison       `json:"candle_comparison,omitempty"`
	CoinComparison          *MarketQuestionCoinComparison         `json:"coin_comparison,omitempty"`
	Status                  string                                `json:"status"`
	Reason                  string                                `json:"reason,omitempty"`
	Source                  string                                `json:"source"`
	Semantics               string                                `json:"semantics"`
	MarketScope             string                                `json:"market_scope"`
	WindowSec               int64                                 `json:"window_sec"`
	AsOfMS                  int64                                 `json:"as_of_ms"`
	AggregationVersion      uint64                                `json:"aggregation_version"`
	Current                 MarketQuestionIntervalWindow          `json:"current"`
	Previous                MarketQuestionIntervalWindow          `json:"previous"`
	ShareDeltaPP            *float64                              `json:"share_delta_pp"`
	DomesticVolumeDeltaKRW  *float64                              `json:"domestic_volume_delta_krw"`
	BithumbVolumeDeltaKRW   *float64                              `json:"bithumb_volume_delta_krw"`
	PrimaryDriver           string                                `json:"primary_driver,omitempty"`
	DriverContributionSumPP *float64                              `json:"driver_contribution_sum_pp"`
	DriverResidualPP        *float64                              `json:"driver_residual_pp"`
	TargetExchange          *MarketQuestionTargetExchangeEvidence `json:"target_exchange,omitempty"`
	Asset                   *MarketQuestionAssetInterval          `json:"asset,omitempty"`
	Drivers                 []AttributionAsset                    `json:"asset_drivers"`
	ExchangeDrivers         []AttributionExchange                 `json:"exchange_drivers"`
	CurrentCoverage         MarketQuestionIntervalCoverage        `json:"current_coverage"`
	PreviousCoverage        MarketQuestionIntervalCoverage        `json:"previous_coverage"`
}

type MarketQuestionFactDriver struct {
	Asset                     string  `json:"asset"`
	ContributionPP            float64 `json:"contribution_pp"`
	BithumbVolumeChangeKRW    float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW float64 `json:"competitor_volume_change_krw"`
}

type MarketQuestionFactExchangeDriver struct {
	Exchange        string  `json:"exchange"`
	ContributionPP  float64 `json:"contribution_pp"`
	VolumeChangeKRW float64 `json:"volume_change_krw"`
}

type MarketQuestionDailyEndpointPoint struct {
	BucketMS               int64    `json:"bucket_ms"`
	ObservedAtMS           int64    `json:"observed_at_ms"`
	Status                 string   `json:"status"`
	Reason                 string   `json:"reason,omitempty"`
	ExactEndpointAvailable bool     `json:"exact_endpoint_available"`
	AssetDetailsAvailable  bool     `json:"asset_details_available"`
	TotalVolumeKRW         *float64 `json:"total_volume_krw"`
	BithumbVolumeKRW       *float64 `json:"bithumb_volume_krw"`
	BithumbSharePct        *float64 `json:"bithumb_share_pct"`
	AssetRowCount          int      `json:"asset_row_count"`
}

type MarketQuestionDailyEndpointDriver struct {
	Rank                        int      `json:"rank"`
	Asset                       string   `json:"asset"`
	ContributionPP              float64  `json:"contribution_pp"`
	BithumbVolumeChangeKRW      float64  `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW   float64  `json:"competitor_volume_change_krw"`
	PreviousBithumbVolumeKRW    float64  `json:"previous_bithumb_volume_krw"`
	CurrentBithumbVolumeKRW     float64  `json:"current_bithumb_volume_krw"`
	PreviousCompetitorVolumeKRW float64  `json:"previous_competitor_volume_krw"`
	CurrentCompetitorVolumeKRW  float64  `json:"current_competitor_volume_krw"`
	PreviousClosePriceKRW       *float64 `json:"previous_close_price_krw"`
	CurrentClosePriceKRW        *float64 `json:"current_close_price_krw"`
	ClosePriceChangeKRW         *float64 `json:"close_price_change_krw"`
	ClosePriceChangePct         *float64 `json:"close_price_change_pct"`
}

type MarketQuestionDailyEndpointTransition struct {
	FromBucketMS              int64                               `json:"from_bucket_ms"`
	ToBucketMS                int64                               `json:"to_bucket_ms"`
	WindowSec                 int64                               `json:"window_sec"`
	Status                    string                              `json:"status"`
	Reason                    string                              `json:"reason,omitempty"`
	EndpointComparison        bool                                `json:"endpoint_comparison_available"`
	AssetAttributionAvailable bool                                `json:"asset_attribution_available"`
	PathComplete              bool                                `json:"path_complete"`
	ExpectedHourlyPointCount  int                                 `json:"expected_hourly_point_count"`
	ObservedHourlyPointCount  int                                 `json:"observed_hourly_point_count"`
	MissingBucketMS           []int64                             `json:"missing_bucket_ms"`
	ShareDeltaPP              *float64                            `json:"share_delta_pp"`
	DomesticVolumeDeltaKRW    *float64                            `json:"domestic_volume_delta_krw"`
	BithumbVolumeDeltaKRW     *float64                            `json:"bithumb_volume_delta_krw"`
	PositiveDrivers           []MarketQuestionDailyEndpointDriver `json:"positive_drivers"`
	NegativeDrivers           []MarketQuestionDailyEndpointDriver `json:"negative_drivers"`
	ExchangeDrivers           []MarketQuestionFactExchangeDriver  `json:"exchange_drivers"`
}

type MarketQuestionDailyPricePoint struct {
	BucketMS      int64    `json:"bucket_ms"`
	ClosePriceKRW *float64 `json:"close_price_krw"`
}

type MarketQuestionDailyPricePath struct {
	Asset     string                          `json:"asset"`
	Status    string                          `json:"status"`
	Direction string                          `json:"direction"`
	ChangePct *float64                        `json:"change_pct"`
	Points    []MarketQuestionDailyPricePoint `json:"points"`
}

type MarketQuestionDailyRecentInterval struct {
	Status                  string                         `json:"status"`
	Reason                  string                         `json:"reason,omitempty"`
	Source                  string                         `json:"source"`
	WindowSec               int64                          `json:"window_sec"`
	AsOfMS                  int64                          `json:"as_of_ms"`
	Previous                MarketQuestionIntervalWindow   `json:"previous"`
	Current                 MarketQuestionIntervalWindow   `json:"current"`
	ShareDeltaPP            *float64                       `json:"share_delta_pp"`
	DomesticVolumeDeltaKRW  *float64                       `json:"domestic_volume_delta_krw"`
	BithumbVolumeDeltaKRW   *float64                       `json:"bithumb_volume_delta_krw"`
	PositiveDrivers         []MarketQuestionFactDriver     `json:"positive_drivers"`
	NegativeDrivers         []MarketQuestionFactDriver     `json:"negative_drivers"`
	DriverContributionSumPP *float64                       `json:"driver_contribution_sum_pp"`
	DriverResidualPP        *float64                       `json:"driver_residual_pp"`
	CurrentCoverage         MarketQuestionIntervalCoverage `json:"current_coverage"`
	PreviousCoverage        MarketQuestionIntervalCoverage `json:"previous_coverage"`
}

type MarketQuestionDailyEndpointSeriesFacts struct {
	ContractVersion          string                                  `json:"contract_version"`
	Status                   string                                  `json:"status"`
	Reason                   string                                  `json:"reason,omitempty"`
	Points                   []MarketQuestionDailyEndpointPoint      `json:"points"`
	Transitions              []MarketQuestionDailyEndpointTransition `json:"transitions"`
	Cumulative               MarketQuestionDailyEndpointTransition   `json:"cumulative"`
	PricePaths               []MarketQuestionDailyPricePath          `json:"price_paths"`
	RecentInterval           *MarketQuestionDailyRecentInterval      `json:"recent_interval,omitempty"`
	PathComplete             bool                                    `json:"path_complete"`
	ExpectedHourlyPointCount int                                     `json:"expected_hourly_point_count"`
	ObservedHourlyPointCount int                                     `json:"observed_hourly_point_count"`
	MissingBucketMS          []int64                                 `json:"missing_bucket_ms"`
	NonLiveBucketMS          []int64                                 `json:"non_live_bucket_ms"`
	IncludedExchanges        []string                                `json:"included_exchanges"`
	ExcludedExchanges        map[string]string                       `json:"excluded_exchanges"`
}

// MarketQuestionPathSegment is an exact contiguous run of official points.
// A segment never crosses a missing or non-LIVE bucket, so it can safely be
// discussed even when the requested full path is incomplete.
type MarketQuestionPathSegment struct {
	StartMS            int64   `json:"start_ms"`
	EndMS              int64   `json:"end_ms"`
	PointCount         int     `json:"point_count"`
	StartSharePct      float64 `json:"start_share_pct"`
	EndSharePct        float64 `json:"end_share_pct"`
	ShareDeltaPP       float64 `json:"share_delta_pp"`
	MinSharePct        float64 `json:"min_share_pct"`
	MinShareAtMS       int64   `json:"min_share_at_ms"`
	MaxSharePct        float64 `json:"max_share_pct"`
	MaxShareAtMS       int64   `json:"max_share_at_ms"`
	LargestStepDeltaPP float64 `json:"largest_step_delta_pp"`
	LargestStepStartMS int64   `json:"largest_step_start_ms"`
	LargestStepEndMS   int64   `json:"largest_step_end_ms"`
}

// MarketQuestionPathSummary is a server-calculated view of every aligned
// official hourly point in the requested N-hour window. Full-path values are
// omitted when any bucket is unavailable; ObservedSegments preserves exact
// partial runs without interpolating across gaps.
type MarketQuestionPathSummary struct {
	Status                    string                      `json:"status"`
	Reason                    string                      `json:"reason"`
	PointCount                int                         `json:"point_count"`
	ExpectedPointCount        int                         `json:"expected_point_count"`
	StartMS                   int64                       `json:"start_ms"`
	EndMS                     int64                       `json:"end_ms"`
	MinSharePct               *float64                    `json:"min_share_pct"`
	MinShareAtMS              int64                       `json:"min_share_at_ms"`
	MaxSharePct               *float64                    `json:"max_share_pct"`
	MaxShareAtMS              int64                       `json:"max_share_at_ms"`
	LargestStepDeltaPP        *float64                    `json:"largest_step_delta_pp"`
	LargestStepStartMS        int64                       `json:"largest_step_start_ms"`
	LargestStepEndMS          int64                       `json:"largest_step_end_ms"`
	LargestStepAbsSharePct    *float64                    `json:"largest_step_abs_share_pct"`
	RisingStepCount           int                         `json:"rising_step_count"`
	FallingStepCount          int                         `json:"falling_step_count"`
	FlatStepCount             int                         `json:"flat_step_count"`
	Direction                 string                      `json:"direction"`
	DominantDirectionSharePct *float64                    `json:"dominant_direction_share_pct"`
	ObservedSegments          []MarketQuestionPathSegment `json:"observed_segments"`
}

type MarketQuestionPriceRelationship struct {
	Rank        int     `json:"rank"`
	Asset       string  `json:"asset"`
	PairCount   int     `json:"pair_count"`
	Correlation float64 `json:"correlation"`
	Direction   string  `json:"direction"`
}

type MarketQuestionRealtimeAssetContext struct {
	Asset                  string                      `json:"asset"`
	DomesticVolumeKRW      float64                     `json:"domestic_volume_krw"`
	BithumbVolumeKRW       float64                     `json:"bithumb_volume_krw"`
	BithumbSharePct        *float64                    `json:"bithumb_share_pct"`
	BithumbBuyPct          *float64                    `json:"bithumb_buy_pct"`
	TopCompetitor          string                      `json:"top_competitor"`
	TopCompetitorVolumeKRW float64                     `json:"top_competitor_volume_krw"`
	Orderbook              *movement.RealtimeOrderbook `json:"orderbook,omitempty"`
}

type MarketQuestionRealtimeContext struct {
	AsOfMS           int64                               `json:"as_of_ms"`
	AgeSec           *float64                            `json:"age_sec"`
	Status           string                              `json:"status"`
	SignalStatus     string                              `json:"signal_status"`
	Reason           string                              `json:"reason"`
	Direction        string                              `json:"direction"`
	PrimaryWindowSec int64                               `json:"primary_window_sec"`
	ShareDeltaPP     *float64                            `json:"share_delta_pp"`
	Windows          []RealtimeFlowWindow                `json:"windows"`
	Asset            *MarketQuestionRealtimeAssetContext `json:"asset,omitempty"`
}

type MarketQuestionOrderbookContext struct {
	Asset           string   `json:"asset"`
	Source          string   `json:"source"`
	Status          string   `json:"status"`
	AsOfMS          int64    `json:"as_of_ms"`
	AgeSec          *float64 `json:"age_sec"`
	Imbalance       *float64 `json:"imbalance"`
	SpreadBPS       *float64 `json:"spread_bps"`
	BidDepthKRW     *float64 `json:"bid_depth_krw"`
	AskDepthKRW     *float64 `json:"ask_depth_krw"`
	BuySlippageBPS  *float64 `json:"buy_slippage_bps"`
	SellSlippageBPS *float64 `json:"sell_slippage_bps"`
}

type MarketQuestionNoticeFact struct {
	Exchange    string                      `json:"exchange"`
	NoticeID    string                      `json:"notice_id"`
	Title       string                      `json:"title"`
	AnchorMS    int64                       `json:"anchor_ms"`
	Assets      []string                    `json:"assets"`
	Relation    string                      `json:"relation"`
	Period      *MarketQuestionNoticePeriod `json:"period,omitempty"`
	CausalClaim bool                        `json:"causal_claim"`
}

type MarketQuestionNoticePeriod struct {
	Kind                    string `json:"kind"`
	StartMS                 int64  `json:"start_ms"`
	EndMS                   int64  `json:"end_ms"`
	Timezone                string `json:"timezone,omitempty"`
	Precision               string `json:"precision,omitempty"`
	Relation                string `json:"relation"`
	AppliesToQuestionWindow bool   `json:"applies_to_question_window"`
}

type MarketQuestionNoticeContext struct {
	WindowStartMS      int64                      `json:"window_start_ms"`
	WindowEndMS        int64                      `json:"window_end_ms"`
	SourceCount        int                        `json:"source_count"`
	HealthySourceCount int                        `json:"healthy_source_count"`
	Matches            []MarketQuestionNoticeFact `json:"matches"`
	CausalClaim        bool                       `json:"causal_claim"`
}

type MarketQuestionAuxiliaryFacts struct {
	Requested []string                        `json:"requested"`
	Realtime  *MarketQuestionRealtimeContext  `json:"realtime,omitempty"`
	Orderbook *MarketQuestionOrderbookContext `json:"orderbook,omitempty"`
	Notices   *MarketQuestionNoticeContext    `json:"notices,omitempty"`
}

type MarketQuestionFacts struct {
	CandleComparison        *MarketQuestionCandleComparison         `json:"candle_comparison,omitempty"`
	CoinComparison          *MarketQuestionCoinComparison           `json:"coin_comparison,omitempty"`
	EvidenceLayer           string                                  `json:"evidence_layer"`
	WindowSec               int64                                   `json:"window_sec"`
	Previous                *MarketQuestionIntervalWindow           `json:"previous"`
	Current                 *MarketQuestionIntervalWindow           `json:"current"`
	ShareDeltaPP            *float64                                `json:"share_delta_pp"`
	ShareRelativeChangePct  *float64                                `json:"share_relative_change_pct"`
	DomesticVolumeDeltaKRW  *float64                                `json:"domestic_volume_delta_krw"`
	DomesticVolumeChangePct *float64                                `json:"domestic_volume_change_pct"`
	BithumbVolumeDeltaKRW   *float64                                `json:"bithumb_volume_delta_krw"`
	BithumbVolumeChangePct  *float64                                `json:"bithumb_volume_change_pct"`
	PathSummary             *MarketQuestionPathSummary              `json:"path_summary,omitempty"`
	PriceRelationshipStatus string                                  `json:"price_relationship_status"`
	PriceRelationshipReason string                                  `json:"price_relationship_reason"`
	PriceRelationships      []MarketQuestionPriceRelationship       `json:"price_relationships"`
	AssetMetrics            *MarketQuestionAssetInterval            `json:"asset_metrics,omitempty"`
	ComparedAssetMetrics    []*MarketQuestionAssetInterval          `json:"compared_asset_metrics,omitempty"`
	ComparedAssetRanks      map[string]int                          `json:"compared_asset_ranks,omitempty"`
	Drivers                 []MarketQuestionFactDriver              `json:"drivers"`
	ExchangeDrivers         []MarketQuestionFactExchangeDriver      `json:"exchange_drivers"`
	DriverContributionSumPP *float64                                `json:"driver_contribution_sum_pp"`
	DriverResidualPP        *float64                                `json:"driver_residual_pp"`
	TargetExchange          *MarketQuestionTargetExchangeEvidence   `json:"target_exchange,omitempty"`
	DailyEndpointSeries     *MarketQuestionDailyEndpointSeriesFacts `json:"daily_endpoint_series,omitempty"`
	Auxiliary               *MarketQuestionAuxiliaryFacts           `json:"auxiliary,omitempty"`
}

type MarketQuestionQuality struct {
	Status                      string                         `json:"status"`
	EndpointComparisonAvailable bool                           `json:"endpoint_comparison_available"`
	PathComplete                bool                           `json:"path_complete"`
	CurrentCoverage             MarketQuestionIntervalCoverage `json:"current_coverage"`
	PreviousCoverage            MarketQuestionIntervalCoverage `json:"previous_coverage"`
	Warnings                    []string                       `json:"warnings"`
}

type MarketQuestionProse struct {
	Answer    string                           `json:"answer"`
	KeyPoints []string                         `json:"key_points"`
	Caveat    string                           `json:"caveat"`
	Blocks    []MarketQuestionExplanationBlock `json:"blocks,omitempty"`
}

type MarketQuestionProvider struct {
	Source       string `json:"source"`
	Model        string `json:"model,omitempty"`
	FallbackUsed bool   `json:"fallback_used"`
	AsOfMS       int64  `json:"as_of_ms"`
}

type MarketQuestionProvenance struct {
	AnalysisVersion            uint64 `json:"analysis_version"`
	AggregationVersion         uint64 `json:"aggregation_version"`
	AnswerPolicyVersion        string `json:"answer_policy_version"`
	RetrievalStrategy          string `json:"retrieval_strategy"`
	PersistentStore            string `json:"persistent_store"`
	ModelGeneratedSQL          bool   `json:"model_generated_sql"`
	RawDatabaseRowsSentToModel bool   `json:"raw_database_rows_sent_to_model"`
	OfficialBucketMS           int64  `json:"official_bucket_ms"`
	ComparisonEndMS            int64  `json:"comparison_end_ms"`
	AsOfKind                   string `json:"as_of_kind"`
	DisplayTimezone            string `json:"display_timezone"`
}

type GroundedMarketQuestionResponse struct {
	RequestID         string                                 `json:"request_id,omitempty"`
	Contract          string                                 `json:"contract"`
	Query             MarketQuestionQuery                    `json:"query"`
	Facts             MarketQuestionFacts                    `json:"facts"`
	Quality           MarketQuestionQuality                  `json:"quality"`
	Prose             MarketQuestionProse                    `json:"prose"`
	Provider          MarketQuestionProvider                 `json:"provider"`
	Provenance        MarketQuestionProvenance               `json:"provenance"`
	EvidenceBundle    *marketquestion.EvidenceBundleSnapshot `json:"evidence_bundle,omitempty"`
	Orchestration     *MarketQuestionOrchestration           `json:"orchestration,omitempty"`
	ConversationScope *MarketQuestionConversationScope       `json:"conversation_scope,omitempty"`
	EvidenceRetention *MarketQuestionEvidenceRetention       `json:"evidence_retention,omitempty"`
}

type NoticeQuestionSource struct {
	Exchange      string `json:"exchange"`
	NoticeID      string `json:"notice_id"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	PublishedTSMS int64  `json:"published_ts_ms"`
}

// NoticeQuestionAnswer is grounded only in domestic official notices and
// server-owned domestic volume evidence. SourceNoticeIDs is an internal bridge
// from the model result to the server-validated Sources list and is never
// serialized.
type NoticeQuestionAnswer struct {
	Answer          string                 `json:"answer"`
	KeyPoints       []string               `json:"key_points"`
	Caveat          string                 `json:"caveat"`
	Source          string                 `json:"source"`
	Model           string                 `json:"model,omitempty"`
	AsOfMS          int64                  `json:"as_of_ms"`
	Persona         string                 `json:"persona"`
	Sources         []NoticeQuestionSource `json:"sources"`
	SourceNoticeIDs []string               `json:"-"`
}
