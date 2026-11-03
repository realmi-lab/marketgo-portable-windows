package analysis

import (
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/contracts/collection/official"
	"market-share-go/internal/tickertrend"
)

type WindowState struct {
	Status             string                             `json:"status"`
	CoverageStatus     string                             `json:"coverage_status"`
	WSStatus           string                             `json:"ws_status,omitempty"`
	CoverageSec        int64                              `json:"coverage_sec"`
	GapSeconds         int64                              `json:"gap_seconds"`
	WindowComplete     bool                               `json:"window_complete"`
	RateDenominatorSec int64                              `json:"-"`
	DataSource         map[string]any                     `json:"data_source"`
	TotalVolumeKRW     float64                            `json:"total_volume_krw"`
	BithumbShare       *float64                           `json:"bithumb_share_pct"`
	BithumbShareStatus string                             `json:"bithumb_share_status"`
	UnresolvedFXCount  uint64                             `json:"unresolved_fx_count"`
	Exchanges          map[domain.Exchange]WindowExchange `json:"exchanges"`
}

type WindowExchange struct {
	VolumeKRW float64 `json:"volume_krw"`
}

type MarketTotals struct {
	OfficialReplacementVolumeKRW float64  `json:"official_replacement_volume_krw,omitempty"`
	TradeDetailIncomplete        bool     `json:"trade_detail_incomplete,omitempty"`
	VolumeKRW                    float64  `json:"volume_krw"`
	TradeCount                   int64    `json:"trade_count"`
	UnresolvedFXCount            int64    `json:"unresolved_fx_count"`
	AverageTradeKRW              *float64 `json:"average_trade_krw"`
	BuyVolumeKRW                 float64  `json:"buy_volume_krw"`
	SellVolumeKRW                float64  `json:"sell_volume_krw"`
	AggressorBuyPct              *float64 `json:"aggressor_buy_pct"`
}

type Concentration struct {
	TradedAssetCount     int      `json:"traded_asset_count"`
	HHI                  *float64 `json:"hhi"`
	LeadingAsset         string   `json:"leading_asset"`
	LeadingAssetSharePct *float64 `json:"leading_asset_share_pct"`
	Top5SharePct         *float64 `json:"top_5_share_pct"`
	Top10SharePct        *float64 `json:"top_10_share_pct"`
}

type ExchangeAnalysis struct {
	Exchange domain.Exchange `json:"exchange"`
	MarketTotals
	SharePct                             *float64 `json:"share_pct"`
	TradeCountSharePct                   *float64 `json:"trade_count_share_pct"`
	TradesPerSecond                      float64  `json:"trades_per_sec"`
	RegisteredMarketCount                int      `json:"registered_market_count"`
	RegisteredAssetCount                 int      `json:"registered_asset_count"`
	ListedAssetDomesticVolumeCoveragePct *float64 `json:"listed_asset_domestic_volume_coverage_pct"`
	TradedMarketCount                    int      `json:"traded_market_count"`
	Concentration
}

type SharedListingSummary struct {
	Status                    string   `json:"status"`
	AssetCount                int      `json:"asset_count"`
	CurrentDomesticVolumeKRW  float64  `json:"current_domestic_volume_krw"`
	CurrentBithumbVolumeKRW   float64  `json:"current_bithumb_volume_krw"`
	CurrentSharePct           *float64 `json:"current_share_pct"`
	PreviousDomesticVolumeKRW *float64 `json:"previous_domestic_volume_krw"`
	PreviousBithumbVolumeKRW  *float64 `json:"previous_bithumb_volume_krw"`
	PreviousSharePct          *float64 `json:"previous_share_pct"`
	ShareChangePP             *float64 `json:"share_change_pp"`
	DomesticVolumeCoveragePct *float64 `json:"domestic_volume_coverage_pct"`
}

type AssetAnalysis struct {
	OfficialMinuteReplacementUsed      bool                `json:"official_minute_replacement_used,omitempty"`
	OfficialMinuteReplacementVolumeKRW float64             `json:"official_minute_replacement_volume_krw,omitempty"`
	OfficialMinuteReplacementExchanges []string            `json:"official_minute_replacement_exchanges,omitempty"`
	TradeDetailIncomplete              bool                `json:"trade_detail_incomplete,omitempty"`
	Asset                              string              `json:"asset"`
	AssetIdentity                      AssetIdentity       `json:"asset_identity"`
	ListedOnBithumb                    bool                `json:"listed_on_bithumb"`
	GapType                            string              `json:"gap_type"`
	DomesticVolumeKRW                  *float64            `json:"domestic_volume_krw"`
	GlobalVolumeKRW                    *float64            `json:"global_volume_krw"`
	BithumbVolumeKRW                   float64             `json:"bithumb_volume_krw"`
	UpbitVolumeKRW                     float64             `json:"upbit_volume_krw"`
	UpbitCapturePct                    *float64            `json:"upbit_capture_pct"`
	BithumbMinusUpbitCapturePP         *float64            `json:"bithumb_minus_upbit_capture_pp"`
	CompetitorVolumeKRW                float64             `json:"competitor_volume_krw"`
	BithumbCapturePct                  *float64            `json:"bithumb_capture_pct"`
	CaptureGapVsVenuePP                *float64            `json:"capture_gap_vs_venue_pp"`
	DomesticVolumeSharePct             *float64            `json:"domestic_volume_share_pct"`
	GlobalVolumeSharePct               *float64            `json:"global_volume_share_pct"`
	GlobalMinusDomesticInterestPP      *float64            `json:"global_minus_domestic_interest_pp"`
	CompetitorLeader                   string              `json:"competitor_leader"`
	CompetitorLeaderVolumeKRW          float64             `json:"competitor_leader_volume_krw"`
	DomesticExchanges                  []string            `json:"domestic_exchanges"`
	DomesticExchangeCount              int                 `json:"domestic_exchange_count"`
	ExclusiveOnBithumb                 bool                `json:"exclusive_on_bithumb"`
	SharedBithumbUpbit                 bool                `json:"shared_bithumb_upbit"`
	BithumbMarketCount                 int                 `json:"bithumb_market_count"`
	BithumbTradeCount                  int64               `json:"bithumb_trade_count"`
	DomesticTradeCount                 int64               `json:"domestic_trade_count"`
	BithumbTradeCountCapturePct        *float64            `json:"bithumb_trade_count_capture_pct"`
	BithumbBaseQuantity                float64             `json:"bithumb_base_quantity"`
	DomesticBaseQuantity               float64             `json:"domestic_base_quantity"`
	BithumbBaseQuantityCapturePct      *float64            `json:"bithumb_base_quantity_capture_pct"`
	Quotes                             []string            `json:"quotes"`
	ShareChangeContributionPP          *float64            `json:"share_change_contribution_pp"`
	DomesticVolumeChangeKRW            *float64            `json:"domestic_volume_change_krw"`
	BithumbVolumeChangeKRW             *float64            `json:"bithumb_volume_change_krw"`
	Microstructure                     AssetMicrostructure `json:"microstructure"`
}

type QuoteAnalysis struct {
	Quote                 string   `json:"quote"`
	VolumeKRW             float64  `json:"volume_krw"`
	MarketSharePct        *float64 `json:"market_share_pct"`
	BithumbVolumeKRW      float64  `json:"bithumb_volume_krw"`
	BithumbCapturePct     *float64 `json:"bithumb_capture_pct"`
	RegisteredMarketCount int      `json:"registered_market_count"`
	BithumbMarketCount    int      `json:"bithumb_market_count"`
	TradedMarketCount     int      `json:"traded_market_count"`
	UnresolvedFXCount     int64    `json:"unresolved_fx_count"`
	Exchanges             []string `json:"exchanges"`
}

type Momentum struct {
	Window             string         `json:"window"`
	DurationSec        int64          `json:"duration_sec"`
	SharePct           *float64       `json:"share_pct"`
	ShareStatus        string         `json:"share_status"`
	UnresolvedFXCount  uint64         `json:"unresolved_fx_count"`
	VolumeKRW          float64        `json:"volume_krw"`
	VolumePerSecKRW    float64        `json:"volume_per_sec_krw"`
	MarketVolumePerSec float64        `json:"market_volume_per_sec_krw"`
	RateDenominatorSec int64          `json:"rate_denominator_sec"`
	Status             string         `json:"status"`
	CoverageStatus     string         `json:"coverage_status"`
	WSStatus           string         `json:"ws_status,omitempty"`
	CoverageSec        int64          `json:"coverage_sec"`
	GapSeconds         int64          `json:"gap_seconds"`
	DataSource         map[string]any `json:"data_source"`
	WindowComplete     bool           `json:"window_complete"`
}

type Finding struct {
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	Detail        string   `json:"detail"`
	Asset         string   `json:"asset,omitempty"`
	Quote         string   `json:"quote,omitempty"`
	ValueKRW      *float64 `json:"value_krw,omitempty"`
	ValuePct      *float64 `json:"value_pct,omitempty"`
	Basis         []string `json:"basis,omitempty"`
	EvidenceLevel string   `json:"evidence_level"`
	EvidenceLabel string   `json:"evidence_label"`
	ClaimScope    string   `json:"claim_scope"`
	CausalClaim   bool     `json:"causal_claim"`
}

type EvidenceLayer struct {
	Level       string    `json:"level"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	ClaimScope  string    `json:"claim_scope"`
	CausalClaim bool      `json:"causal_claim"`
	Items       []Finding `json:"items"`
}

type EvidenceLayers struct {
	SeparationRule string        `json:"separation_rule"`
	Measured       EvidenceLayer `json:"measured"`
	Diagnostic     EvidenceLayer `json:"diagnostic"`
	Contextual     EvidenceLayer `json:"contextual"`
}

type Context struct {
	AnalysisContextID         string         `json:"analysis_context_id"`
	MarketScope               string         `json:"market_scope"`
	MarketScopeLabel          string         `json:"market_scope_label"`
	WindowSec                 int64          `json:"window_sec"`
	ComparisonEnabled         bool           `json:"comparison_enabled"`
	ComparisonWindowSec       int64          `json:"comparison_window_sec"`
	AssetScope                string         `json:"asset_scope"`
	AsOf                      float64        `json:"as_of"`
	AnalysisVersion           uint64         `json:"analysis_version"`
	AggregationVersion        uint64         `json:"aggregation_version"`
	SelectedWindowComplete    bool           `json:"selected_window_complete"`
	SelectedWindowStatus      string         `json:"selected_window_status"`
	SelectedWindowCoverageSec int64          `json:"selected_window_coverage_sec"`
	SelectedWindowRequiredSec int64          `json:"selected_window_required_sec"`
	CurrentIntervalStart      float64        `json:"current_interval_start"`
	CurrentIntervalEnd        float64        `json:"current_interval_end"`
	ComparisonIntervalStart   *float64       `json:"comparison_interval_start"`
	ComparisonIntervalEnd     *float64       `json:"comparison_interval_end"`
	DataSource                map[string]any `json:"data_source"`
	OrderBookAsOf             float64        `json:"orderbook_as_of"`
	OrderBookVersion          int64          `json:"orderbook_version"`
}

type Summary struct {
	DomesticVolumeKRW                       float64              `json:"domestic_volume_krw"`
	GlobalContextVolumeKRW                  float64              `json:"global_context_volume_krw"`
	BithumbVolumeKRW                        float64              `json:"bithumb_volume_krw"`
	BithumbSharePct                         *float64             `json:"bithumb_share_pct"`
	BithumbShareStatus                      string               `json:"bithumb_share_status"`
	DomesticTradeCount                      int64                `json:"domestic_trade_count"`
	BithumbTradeCount                       int64                `json:"bithumb_trade_count"`
	BithumbTradeCountSharePct               *float64             `json:"bithumb_trade_count_share_pct"`
	DomesticUnresolvedFXCount               int64                `json:"domestic_unresolved_fx_count"`
	GlobalUnresolvedFXCount                 int64                `json:"global_unresolved_fx_count"`
	DomesticAssetCount                      int                  `json:"domestic_asset_count"`
	BithumbListedAssetCount                 int                  `json:"bithumb_listed_asset_count"`
	BithumbListingCoveragePct               *float64             `json:"bithumb_listing_coverage_pct"`
	BithumbVolumeWeightedListingCoveragePct *float64             `json:"bithumb_volume_weighted_listing_coverage_pct"`
	UnlistedCompetitorVolumeKRW             float64              `json:"unlisted_competitor_volume_krw"`
	UpbitListedAssetCount                   int                  `json:"upbit_listed_asset_count"`
	UpbitListingCoveragePct                 *float64             `json:"upbit_listing_coverage_pct"`
	UpbitVolumeWeightedListingCoveragePct   *float64             `json:"upbit_volume_weighted_listing_coverage_pct"`
	UpbitUnlistedCompetitorVolumeKRW        float64              `json:"upbit_unlisted_competitor_volume_krw"`
	AnalyzedAssetCount                      int                  `json:"analyzed_asset_count"`
	AnalysisHasAssetLimit                   bool                 `json:"analysis_has_asset_limit"`
	SelectedWindowComplete                  bool                 `json:"selected_window_complete"`
	SelectedWindowCoverageSec               int64                `json:"selected_window_coverage_sec"`
	SelectedWindowRequiredSec               int64                `json:"selected_window_required_sec"`
	BithumbSharedListing                    SharedListingSummary `json:"bithumb_shared_listing"`
}

type Analysis struct {
	WindowSec            int64                            `json:"window_sec"`
	Window               string                           `json:"window"`
	Definition           string                           `json:"definition"`
	GeneratedAt          float64                          `json:"generated_at"`
	Context              Context                          `json:"context"`
	Summary              Summary                          `json:"summary"`
	OfficialDomestic24h  official.DomesticTickerSummary   `json:"official_domestic_24h"`
	OfficialBinance24h   official.BinanceTicker24hSummary `json:"official_binance_24h"`
	OfficialShareMonitor movement.State                   `json:"official_share_monitor"`
	// OfficialTicker1m retains the historical JSON field name for client
	// compatibility. Its value is the aligned completed 1-hour candle trend
	// (OFFICIAL_ALIGNED_1H_CANDLE_TREND_V1), never a 1-minute indicator.
	OfficialTicker1m  tickertrend.State  `json:"official_ticker_1m"`
	RealtimeFlow      RealtimeFlowState  `json:"realtime_flow"`
	Findings          []Finding          `json:"findings"`
	EvidenceLayers    EvidenceLayers     `json:"evidence_layers"`
	ExpertBrief       ExpertBrief        `json:"expert_brief"`
	ChangeAttribution Attribution        `json:"change_attribution"`
	Momentum          []Momentum         `json:"momentum"`
	Rolling24h        RollingMonitor     `json:"rolling_24h"`
	Exchanges         []ExchangeAnalysis `json:"exchanges"`
	QuoteMarkets      []QuoteAnalysis    `json:"quote_markets"`
	Assets            []AssetAnalysis    `json:"assets"`
	Liquidity         LiquidityAnalysis  `json:"liquidity"`
	DataQuality       map[string]any     `json:"data_quality"`
	Collectors        map[string]any     `json:"collectors"`
	FX                map[string]any     `json:"fx"`
	Registry          map[string]any     `json:"registry"`
	Readiness         Readiness          `json:"readiness"`
	Notices           notice.Snapshot    `json:"notices"`
	ReactionSummary   map[string]any     `json:"reaction_summary"`
}

// TickReferences contains independently refreshed reference state that is
// shared separately from the 500 ms rolling-volume tick.
type TickReferences struct {
	OfficialDomestic24h  official.DomesticTickerSummary   `json:"official_domestic_24h"`
	OfficialBinance24h   official.BinanceTicker24hSummary `json:"official_binance_24h"`
	OfficialShareMonitor movement.State                   `json:"official_share_monitor"`
	// Historical wire name; the state interval is one completed hour.
	OfficialTicker1m tickertrend.State `json:"official_ticker_1m"`
}

type RequestOptions struct {
	ReactionPreWindowSec  int64
	ReactionPostWindowSec int64
}

type ReadinessVenue struct {
	Exchange             string         `json:"exchange"`
	MarketCount          int            `json:"market_count"`
	ConnectedMarketCount int            `json:"connected_market_count"`
	UniqueSymbolCount    int            `json:"unique_symbol_count"`
	QuoteSymbolCounts    map[string]int `json:"quote_symbol_counts"`
	Ready                bool           `json:"ready"`
}

type Readiness struct {
	Phase                       string           `json:"phase"`
	EnabledExchangeCount        int              `json:"enabled_exchange_count"`
	ConfiguredExchangeCount     int              `json:"configured_exchange_count"`
	ReadyExchangeCount          int              `json:"ready_exchange_count"`
	ReadyExchanges              []string         `json:"ready_exchanges"`
	BlockingExchanges           []string         `json:"blocking_exchanges"`
	RegisteredMarketCount       int              `json:"registered_market_count"`
	ConnectedMarketCount        int              `json:"connected_market_count"`
	ConnectedMarketPercent      *float64         `json:"connected_market_pct"`
	Venues                      []ReadinessVenue `json:"venues"`
	AggregatorConsumerAlive     bool             `json:"aggregator_consumer_alive"`
	ActiveTradeParseErrorChunks int              `json:"active_trade_parse_error_chunks"`
	ElapsedSec                  float64          `json:"elapsed_sec"`
	UpdatedAt                   float64          `json:"updated_at"`
}
