package tickertrend

import "market-share-go/internal/movement"

const (
	Version            = "OFFICIAL_ALIGNED_1H_CANDLE_TREND_V1"
	HistoryVersion     = "OFFICIAL_ALIGNED_1H_HISTORY_V1"
	DailySeriesVersion = "OFFICIAL_ALIGNED_1H_DAILY_ENDPOINT_SERIES_V1"
	DefaultInterval    = int64(3600)
	MaxHistoryPoints   = 1_000
	defaultTrendSize   = 48
	defaultDriverSize  = 30
)

type Sample struct {
	BucketMS              int64
	Snapshot              movement.Snapshot
	AssetRowCount         int
	BithumbClosePricesKRW map[string]float64
}

// PersistedMarketRow is the normalized per-exchange asset row used by bounded
// endpoint comparisons. ClosePriceKRW is populated only for an exact observed
// aligned candle close; nil is never filled from an adjacent bucket.
type PersistedMarketRow struct {
	VolumeKRW     float64
	ClosePriceKRW *float64
}

type Point struct {
	BucketMS         int64   `json:"bucket_ms"`
	ObservedAtMS     int64   `json:"observed_at_ms"`
	Status           string  `json:"status"`
	TotalVolumeKRW   float64 `json:"total_volume_krw"`
	BithumbVolumeKRW float64 `json:"bithumb_volume_krw"`
	BithumbSharePct  float64 `json:"bithumb_share_pct"`
	AssetRowCount    int     `json:"asset_row_count"`
}

// History is a bounded database-backed sequence of exact hourly rolling-24h
// endpoints. It is separate from WebSocket second-bucket windows.
type History struct {
	ContractVersion    string  `json:"contract_version"`
	Status             string  `json:"status"`
	Source             string  `json:"source"`
	IntervalSec        int64   `json:"interval_sec"`
	FromMS             int64   `json:"from_ms"`
	ToMS               int64   `json:"to_ms"`
	ExpectedPointCount int     `json:"expected_point_count"`
	ObservedPointCount int     `json:"observed_point_count"`
	MissingPointCount  int     `json:"missing_point_count"`
	Items              []Point `json:"items"`
}

// DailyEndpointPoint is one exact rolling-24h endpoint requested by the
// caller. Numeric values remain nil when that exact bucket is absent or not
// LIVE; a nearby bucket is never substituted.
type DailyEndpointPoint struct {
	BucketMS               int64              `json:"bucket_ms"`
	ObservedAtMS           int64              `json:"observed_at_ms"`
	Status                 string             `json:"status"`
	Reason                 string             `json:"reason"`
	ExactEndpointAvailable bool               `json:"exact_endpoint_available"`
	AssetDetailsAvailable  bool               `json:"asset_details_available"`
	TotalVolumeKRW         *float64           `json:"total_volume_krw"`
	BithumbVolumeKRW       *float64           `json:"bithumb_volume_krw"`
	BithumbSharePct        *float64           `json:"bithumb_share_pct"`
	AssetRowCount          int                `json:"asset_row_count"`
	BithumbClosePricesKRW  map[string]float64 `json:"bithumb_close_prices_krw"`
}

// DailyEndpointDriver augments the exact arithmetic share attribution with
// Bithumb's exact closing one-hour candle price at both endpoints. Price
// change remains unavailable unless both exact close values were persisted.
type DailyEndpointDriver struct {
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

// DailyEndpointTransition compares two selected endpoints. EndpointComparison
// can be true while PathComplete is false: exact endpoint arithmetic and
// interior hourly-path completeness are deliberately independent facts.
type DailyEndpointTransition struct {
	FromBucketMS              int64                     `json:"from_bucket_ms"`
	ToBucketMS                int64                     `json:"to_bucket_ms"`
	WindowSec                 int64                     `json:"window_sec"`
	Status                    string                    `json:"status"`
	Reason                    string                    `json:"reason"`
	EndpointComparison        bool                      `json:"endpoint_comparison_available"`
	AssetAttributionAvailable bool                      `json:"asset_attribution_available"`
	PathComplete              bool                      `json:"path_complete"`
	ExpectedHourlyPointCount  int                       `json:"expected_hourly_point_count"`
	ObservedHourlyPointCount  int                       `json:"observed_hourly_point_count"`
	MissingBucketMS           []int64                   `json:"missing_bucket_ms"`
	ShareDeltaPP              *float64                  `json:"share_delta_pp"`
	DomesticVolumeDeltaKRW    *float64                  `json:"domestic_volume_delta_krw"`
	BithumbVolumeDeltaKRW     *float64                  `json:"bithumb_volume_delta_krw"`
	Drivers                   []DailyEndpointDriver     `json:"asset_drivers"`
	ExchangeDrivers           []movement.ExchangeDriver `json:"exchange_drivers"`
}

// DailyEndpointSeries is a bounded comparison of 2-8 exact endpoints at the
// same KST clock hour. It returns every requested point, every adjacent
// transition, and the first-to-last cumulative comparison without imputing a
// missing endpoint or hourly path bucket.
type DailyEndpointSeries struct {
	ContractVersion          string                    `json:"contract_version"`
	Status                   string                    `json:"status"`
	Reason                   string                    `json:"reason"`
	Points                   []DailyEndpointPoint      `json:"points"`
	Transitions              []DailyEndpointTransition `json:"transitions"`
	Cumulative               DailyEndpointTransition   `json:"cumulative"`
	PathComplete             bool                      `json:"path_complete"`
	ExpectedHourlyPointCount int                       `json:"expected_hourly_point_count"`
	ObservedHourlyPointCount int                       `json:"observed_hourly_point_count"`
	MissingBucketMS          []int64                   `json:"missing_bucket_ms"`
	NonLiveBucketMS          []int64                   `json:"non_live_bucket_ms"`
	IncludedExchanges        []string                  `json:"included_exchanges"`
	ExcludedExchanges        map[string]string         `json:"excluded_exchanges"`
}

// WindowPathSegment is one exact contiguous run inside a requested official
// path. It never bridges a missing or non-LIVE hour. Segments allow the answer
// layer to explain what was actually observed without presenting a partial run
// as the complete requested period.
type WindowPathSegment struct {
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

// WindowPathSummary describes the sequence of aligned official hourly share
// points between two requested endpoints. Full-path quantitative fields remain
// nil unless every expected bucket is present and LIVE. ObservedSegments may
// still describe exact contiguous partial runs and never interpolate a gap.
type WindowPathSummary struct {
	Status                    string              `json:"status"`
	Reason                    string              `json:"reason"`
	PointCount                int                 `json:"point_count"`
	ExpectedPointCount        int                 `json:"expected_point_count"`
	StartMS                   int64               `json:"start_ms"`
	EndMS                     int64               `json:"end_ms"`
	MinSharePct               *float64            `json:"min_share_pct"`
	MinShareAtMS              int64               `json:"min_share_at_ms"`
	MaxSharePct               *float64            `json:"max_share_pct"`
	MaxShareAtMS              int64               `json:"max_share_at_ms"`
	LargestStepDeltaPP        *float64            `json:"largest_step_delta_pp"`
	LargestStepStartMS        int64               `json:"largest_step_start_ms"`
	LargestStepEndMS          int64               `json:"largest_step_end_ms"`
	LargestStepAbsSharePct    *float64            `json:"largest_step_abs_share_pct"`
	RisingStepCount           int                 `json:"rising_step_count"`
	FallingStepCount          int                 `json:"falling_step_count"`
	FlatStepCount             int                 `json:"flat_step_count"`
	Direction                 string              `json:"direction"`
	DominantDirectionSharePct *float64            `json:"dominant_direction_share_pct"`
	ObservedSegments          []WindowPathSegment `json:"observed_segments"`
}

// WindowPriceRelationship is a server-calculated exploratory Pearson
// correlation between an asset's exact Bithumb hourly close return and the
// simultaneous official Bithumb-share change. Only contiguous observed pairs
// participate; it is association evidence, never a causal attribution.
type WindowPriceRelationship struct {
	Rank        int     `json:"rank"`
	Asset       string  `json:"asset"`
	PairCount   int     `json:"pair_count"`
	Correlation float64 `json:"correlation"`
	Direction   string  `json:"direction"`
}

type IncomingMarket struct {
	Exchange  string
	Asset     string
	VolumeKRW float64
}

type EstimateTotals struct {
	IncomingBithumbVolumeKRW       float64  `json:"incoming_bithumb_volume_krw"`
	IncomingCompetitorVolumeKRW    float64  `json:"incoming_competitor_volume_krw"`
	EstimatedExpiringBithumbKRW    *float64 `json:"estimated_expiring_bithumb_krw"`
	EstimatedExpiringCompetitorKRW *float64 `json:"estimated_expiring_competitor_krw"`
	EstimatedExpiringSharePct      *float64 `json:"estimated_expiring_share_pct"`
}

type AssetExpirationEstimate struct {
	Asset                          string   `json:"asset"`
	Status                         string   `json:"status"`
	TickerBithumbNetChangeKRW      float64  `json:"ticker_bithumb_net_change_krw"`
	TickerCompetitorNetChangeKRW   float64  `json:"ticker_competitor_net_change_krw"`
	IncomingBithumbVolumeKRW       float64  `json:"incoming_bithumb_volume_krw"`
	IncomingCompetitorVolumeKRW    float64  `json:"incoming_competitor_volume_krw"`
	EstimatedExpiringBithumbKRW    *float64 `json:"estimated_expiring_bithumb_krw"`
	EstimatedExpiringCompetitorKRW *float64 `json:"estimated_expiring_competitor_krw"`
}

type ExchangeExpirationEstimate struct {
	Exchange                   string   `json:"exchange"`
	Status                     string   `json:"status"`
	TickerNetChangeKRW         float64  `json:"ticker_net_change_krw"`
	IncomingVolumeKRW          float64  `json:"incoming_volume_krw"`
	EstimatedExpiringVolumeKRW *float64 `json:"estimated_expiring_volume_krw"`
}

type ExpirationEstimate struct {
	Status          string                       `json:"status"`
	Reason          string                       `json:"reason"`
	Source          string                       `json:"source"`
	IntervalStartMS int64                        `json:"interval_start_ms"`
	IntervalEndMS   int64                        `json:"interval_end_ms"`
	RequiredSec     int64                        `json:"required_sec"`
	CoverageSec     int64                        `json:"coverage_sec"`
	CoverageStatus  string                       `json:"coverage_status"`
	Totals          EstimateTotals               `json:"totals"`
	Assets          []AssetExpirationEstimate    `json:"assets"`
	Exchanges       []ExchangeExpirationEstimate `json:"exchanges"`
}

type State struct {
	Version                string                    `json:"version"`
	GeneratedAtMS          int64                     `json:"generated_at_ms"`
	IntervalSec            int64                     `json:"interval_sec"`
	Status                 string                    `json:"status"`
	Reason                 string                    `json:"reason"`
	Previous               *Point                    `json:"previous"`
	Current                *Point                    `json:"current"`
	ShareDeltaPP           *float64                  `json:"share_delta_pp"`
	DomesticVolumeDeltaKRW *float64                  `json:"domestic_volume_delta_krw"`
	BithumbVolumeDeltaKRW  *float64                  `json:"bithumb_volume_delta_krw"`
	Drivers                []movement.Driver         `json:"drivers"`
	ExchangeDrivers        []movement.ExchangeDriver `json:"exchange_drivers"`
	Trend                  []Point                   `json:"trend"`
	ExpirationEstimate     ExpirationEstimate        `json:"expiration_estimate"`
	DataQuality            map[string]any            `json:"data_quality"`
}

type WindowComparison struct {
	WindowSec               int64                     `json:"window_sec"`
	Status                  string                    `json:"status"`
	Reason                  string                    `json:"reason"`
	Previous                *Point                    `json:"previous"`
	Current                 *Point                    `json:"current"`
	ShareDeltaPP            *float64                  `json:"share_delta_pp"`
	DomesticVolumeDeltaKRW  *float64                  `json:"domestic_volume_delta_krw"`
	BithumbVolumeDeltaKRW   *float64                  `json:"bithumb_volume_delta_krw"`
	Drivers                 []movement.Driver         `json:"asset_drivers"`
	ExchangeDrivers         []movement.ExchangeDriver `json:"exchange_drivers"`
	ObservedSampleCount     int                       `json:"observed_sample_count"`
	ExpectedSampleCount     int                       `json:"expected_sample_count"`
	MissingSampleCount      int                       `json:"missing_sample_count"`
	EndpointComparison      bool                      `json:"endpoint_comparison_available"`
	PathComplete            bool                      `json:"path_complete"`
	PathSummary             WindowPathSummary         `json:"path_summary"`
	PriceRelationshipStatus string                    `json:"price_relationship_status"`
	PriceRelationshipReason string                    `json:"price_relationship_reason"`
	PriceRelationships      []WindowPriceRelationship `json:"price_relationships"`
}
