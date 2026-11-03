package httpapi

import (
	"sync"
	"time"

	"market-share-go/contracts/collection/official"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/tickertrend"
)

type analysisProjectionSet struct {
	mu                  sync.Mutex
	payload             *marketanalysis.Analysis
	now                 time.Time
	projections         map[analysisProjectionKey]analysisProjection
	messages            map[analysisMessageKey]*sharedJSONValue
	compactNotices      map[string]any
	compactNoticesBuilt bool
}

type analysisMessageKey struct {
	view        string
	projection  string
	partial     bool
	tickVersion uint64
}

type analysisProjectionKey struct {
	view       string
	projection string
}

const dashboardAnalysisProjection = "dashboard_v1"

// briefAnalysisProjection is the main screen's briefing (view overview): only
// what it reads. It has its own name because the Unix dashboard reads much
// more from dashboard_v1's overview; a server that predates it sends the
// briefing the whole compact overview instead.
const briefAnalysisProjection = "dashboard_brief_v1"

// detailAnalysisProjection is the drivers and quality screens' projection. It
// has its own name so a server that predates it sends those screens the
// whole analysis instead of another view's dashboard_v1 projection.
const detailAnalysisProjection = "dashboard_detail_v1"

// The main dashboard's shell needs no notices. Its detail screens read the
// same fields as detail_v1 except notices, which they fetch as headlines.
// Older clients and the nested notice workspace keep their existing views.
const screenAnalysisProjection = "dashboard_screen_v1"

func newAnalysisProjectionSet(payload *marketanalysis.Analysis, now time.Time) *analysisProjectionSet {
	return &analysisProjectionSet{
		payload: payload, now: now,
		projections: make(map[analysisProjectionKey]analysisProjection),
		messages:    make(map[analysisMessageKey]*sharedJSONValue),
	}
}

func (s *analysisProjectionSet) get(view string) analysisProjection {
	return s.getProjected(view, "")
}

func (s *analysisProjectionSet) getProjected(view, projection string) analysisProjection {
	view, projection = normalizeProjectedAnalysisView(view, projection)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(view, projection)
}

func (s *analysisProjectionSet) getLocked(view, projectionName string) analysisProjection {
	key := analysisProjectionKey{view: view, projection: projectionName}
	if projection, ok := s.projections[key]; ok {
		return projection
	}
	var projection analysisProjection
	// Only these wire views consume compact notices. Other dashboard views
	// either carry the original snapshot or omit notices entirely.
	needsCompactNotices := (view == "overview" && projectionName != briefAnalysisProjection) ||
		(view == "summary" && projectionName != dashboardAnalysisProjection)
	if needsCompactNotices && !s.compactNoticesBuilt {
		s.compactNotices = compactAnalysisNotices(s.payload.Notices, s.now)
		s.compactNoticesBuilt = true
	}
	if projectionName == screenAnalysisProjection {
		projection = buildScreenAnalysisProjection(s.payload, view)
	} else if projectionName == briefAnalysisProjection {
		projection = buildBriefAnalysisProjection(s.payload)
	} else if projectionName == detailAnalysisProjection {
		projection = buildDetailAnalysisProjection(s.payload, view)
	} else if projectionName == dashboardAnalysisProjection {
		projection = buildDashboardAnalysisProjection(s.payload, view, s.compactNotices)
	} else if view == "summary" || view == "overview" {
		projection = buildCompactAnalysisProjection(s.payload, view, s.compactNotices)
	} else {
		projection = buildTypedAnalysisProjection(s.payload, view, s.now)
	}
	full := &sharedJSONValue{value: projection.full}
	projection.full = full
	if view == "summary2" || (projectionName == dashboardAnalysisProjection && (view == "summary" || view == "full")) ||
		(projectionName == detailAnalysisProjection && view == "quality") || projectionName == briefAnalysisProjection || projectionName == screenAnalysisProjection {
		// These views omit notices in both frames. Share their identical JSON
		// while keeping the outer full/partial message metadata distinct.
		projection.partial = full
	} else {
		projection.partial = &sharedJSONValue{value: projection.partial}
	}
	s.projections[key] = projection
	return projection
}

func (s *analysisProjectionSet) message(view string, partial bool, tickVersion uint64, state map[string]any) *sharedJSONValue {
	return s.messageProjected(view, "", partial, tickVersion, state)
}

func (s *analysisProjectionSet) messageProjected(view, projection string, partial bool, tickVersion uint64, state map[string]any) *sharedJSONValue {
	view, projection = normalizeProjectedAnalysisView(view, projection)
	key := analysisMessageKey{view: view, projection: projection, partial: partial, tickVersion: tickVersion}
	s.mu.Lock()
	defer s.mu.Unlock()
	if message := s.messages[key]; message != nil {
		return message
	}
	selected := s.getLocked(view, projection)
	projected := selected.full
	if partial {
		projected = selected.partial
	}
	message := &sharedJSONValue{value: map[string]any{
		"type": "market_share_update",
		"data": analysisUpdateData(s.payload, projected, state, tickVersion, partial),
	}}
	s.messages[key] = message
	return message
}

func (s *analysisProjectionSet) builtViewCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.projections)
}

func normalizeAnalysisView(view string) string {
	switch view {
	case "events", "summary", "overview", "summary2":
		return view
	default:
		return "full"
	}
}

// Only the drivers and quality views have the detail projection and only the
// overview has the brief projection; any other view that asks for one gets
// its whole analysis.
func normalizeProjectedAnalysisView(view, projection string) (string, string) {
	switch projection {
	case screenAnalysisProjection:
		if view == "events" || view == "drivers" {
			return view, projection
		}
		projection = ""
	case detailAnalysisProjection:
		if view == "drivers" || view == "quality" {
			return view, projection
		}
		projection = ""
	case briefAnalysisProjection:
		if view == "overview" {
			return view, projection
		}
		projection = ""
	}
	return normalizeAnalysisView(view), projection
}

func buildScreenAnalysisProjection(payload *marketanalysis.Analysis, view string) analysisProjection {
	if view == "events" {
		value := &dashboardEventsAnalysisProjection{
			AnalysisProjection: screenAnalysisProjection, WindowSec: payload.WindowSec, Window: payload.Window, GeneratedAt: payload.GeneratedAt,
		}
		return analysisProjection{full: value, partial: value}
	}
	value := buildDetailAnalysisProjection(payload, view).full.(*dashboardDetailAnalysisProjection)
	value.AnalysisProjection = screenAnalysisProjection
	value.Notices = nil
	return analysisProjection{full: value, partial: value}
}

type analysisWithoutNotices struct {
	*marketanalysis.Analysis
	Notices any `json:"notices,omitempty"`
}

type compactAnalysisProjection struct {
	*marketanalysis.Analysis
	Assets  []compactAnalysisAsset `json:"assets"`
	Notices any                    `json:"notices,omitempty"`
}

type compactAnalysisAsset struct {
	Asset               string   `json:"asset"`
	DomesticExchanges   []string `json:"domestic_exchanges"`
	DomesticVolumeKRW   float64  `json:"domestic_volume_krw"`
	BithumbVolumeKRW    *float64 `json:"bithumb_volume_krw,omitempty"`
	CompetitorVolumeKRW *float64 `json:"competitor_volume_krw,omitempty"`
	ListedOnBithumb     *bool    `json:"listed_on_bithumb,omitempty"`
	ExclusiveOnBithumb  *bool    `json:"exclusive_on_bithumb,omitempty"`
}

type dashboardAttributionProjection struct {
	*marketanalysis.Attribution
	Assets               []marketanalysis.AttributionAsset `json:"assets"`
	Quotes               []marketanalysis.AttributionQuote `json:"quotes"`
	AssetExchangeNetwork any                               `json:"asset_exchange_network,omitempty"`
}

type dashboardLiquidityProjection struct {
	AnalysisDepth        int                                `json:"analysis_depth"`
	ReferenceNotionalKRW float64                            `json:"reference_notional_krw"`
	ObservedDepthOnly    bool                               `json:"observed_depth_only"`
	ExtrapolationUsed    bool                               `json:"extrapolation_used"`
	DomesticDepthKRW     *float64                           `json:"domestic_depth_krw"`
	BithumbDepthKRW      *float64                           `json:"bithumb_depth_krw"`
	Exchanges            []marketanalysis.LiquidityExchange `json:"exchanges"`
}

type dashboardSummaryAnalysisProjection struct {
	AnalysisProjection  string                         `json:"analysis_projection"`
	WindowSec           int64                          `json:"window_sec"`
	Window              string                         `json:"window"`
	Definition          string                         `json:"definition"`
	GeneratedAt         float64                        `json:"generated_at"`
	Context             marketanalysis.Context         `json:"context"`
	Summary             marketanalysis.Summary         `json:"summary"`
	OfficialDomestic24h official.DomesticTickerSummary `json:"official_domestic_24h"`
	OfficialTicker1m    tickertrend.State              `json:"official_ticker_1m"`
	// OfficialShareMonitor (overview only) carries the official rising or
	// falling phase the briefing leads with.
	OfficialShareMonitor *movement.State                   `json:"official_share_monitor,omitempty"`
	Exchanges            []marketanalysis.ExchangeAnalysis `json:"exchanges"`
	ExpertBrief          marketanalysis.ExpertBrief        `json:"expert_brief"`
	ChangeAttribution    dashboardAttributionProjection    `json:"change_attribution"`
	Momentum             []marketanalysis.Momentum         `json:"momentum"`
	EvidenceLayers       marketanalysis.EvidenceLayers     `json:"evidence_layers"`
	Collectors           map[string]any                    `json:"collectors"`
	Assets               []compactAnalysisAsset            `json:"assets"`
	Liquidity            dashboardLiquidityProjection      `json:"liquidity"`
	DataQuality          map[string]any                    `json:"data_quality,omitempty"`
	Notices              any                               `json:"notices,omitempty"`
}

// dashboardBriefAnalysisProjection is what the briefing reads (BriefScreen and
// the shared cards it shows). It leaves out the overview's notices (the
// briefing reads them over HTTP), data quality, coin rows, collectors and the
// expert brief's coin lists, about two thirds of the overview frame. A card
// that starts reading another field there needs it added here.
type dashboardBriefAnalysisProjection struct {
	AnalysisProjection   string                            `json:"analysis_projection"`
	WindowSec            int64                             `json:"window_sec"`
	Window               string                            `json:"window"`
	Definition           string                            `json:"definition"`
	GeneratedAt          float64                           `json:"generated_at"`
	Context              marketanalysis.Context            `json:"context"`
	Summary              marketanalysis.Summary            `json:"summary"`
	OfficialDomestic24h  official.DomesticTickerSummary    `json:"official_domestic_24h"`
	OfficialTicker1m     tickertrend.State                 `json:"official_ticker_1m"`
	OfficialShareMonitor *movement.State                   `json:"official_share_monitor"`
	Exchanges            []marketanalysis.ExchangeAnalysis `json:"exchanges"`
	ExpertBrief          briefExpertBrief                  `json:"expert_brief"`
	ChangeAttribution    dashboardAttributionProjection    `json:"change_attribution"`
	Momentum             []marketanalysis.Momentum         `json:"momentum"`
	Liquidity            dashboardLiquidityProjection      `json:"liquidity"`
}

// briefExpertBrief is the part of the expert brief the briefing reads: the
// leading coin and the change by listing group.
type briefExpertBrief struct {
	AttributionHeadline marketanalysis.AttributionHeadline `json:"attribution_headline"`
	MajorAssets         []marketanalysis.MajorAsset        `json:"major_assets"`
	ListingSegments     []marketanalysis.ListingSegment    `json:"listing_segments"`
}

// dashboardDetailAnalysisProjection is what the coin-drivers and data-quality
// screens read (MarketShareDriversAnalysis and MarketShareQualityAnalysis in
// the frontend). The whole analysis was 6.4 MB per frame; these screens do not
// show the 24-hour rolling monitor (1.9 MB), per-asset order-book detail
// (1.3 MB), per-asset slippage tables (1.6 MB), briefs, or the realtime flow,
// which has its own stream. Outer nil fields hide the embedded ones.
type dashboardDetailAnalysisProjection struct {
	*marketanalysis.Analysis
	AnalysisProjection string `json:"analysis_projection"`
	// Shadows the embedded field with the top-driver copy (see compactMovementState).
	OfficialShareMonitor *movement.State          `json:"official_share_monitor"`
	Rolling24h           any                      `json:"rolling_24h,omitempty"`
	ExpertBrief          any                      `json:"expert_brief,omitempty"`
	ReactionSummary      any                      `json:"reaction_summary,omitempty"`
	RealtimeFlow         any                      `json:"realtime_flow,omitempty"`
	Liquidity            dashboardDetailLiquidity `json:"liquidity"`
	Assets               any                      `json:"assets,omitempty"`
	Notices              *notice.Snapshot         `json:"notices,omitempty"`
}

// detailExpertBrief is the part of the expert brief the change screens read:
// how widely coins moved and the change by listing group.
type detailExpertBrief struct {
	Breadth         marketanalysis.Breadth          `json:"breadth"`
	ListingSegments []marketanalysis.ListingSegment `json:"listing_segments"`
}

type dashboardDetailLiquidity struct {
	dashboardLiquidityProjection
	PriceAnomalyPolicy map[string]any `json:"price_anomaly_policy"`
	QuarantinedAssets  []any          `json:"quarantined_assets"`
}

type dashboardDriversAsset struct {
	*marketanalysis.AssetAnalysis
	AssetIdentity  any `json:"asset_identity,omitempty"`
	Microstructure any `json:"microstructure,omitempty"`
}

type dashboardEventsAnalysisProjection struct {
	AnalysisProjection string           `json:"analysis_projection"`
	WindowSec          int64            `json:"window_sec"`
	Window             string           `json:"window"`
	GeneratedAt        float64          `json:"generated_at"`
	Notices            *notice.Snapshot `json:"notices,omitempty"`
}

type dashboardIntelligenceMicrostructure struct {
	Status                 string   `json:"status"`
	DomesticDepthKRW       *float64 `json:"domestic_depth_krw"`
	BithumbDepthKRW        *float64 `json:"bithumb_depth_krw"`
	BithumbMedianSpreadBPS *float64 `json:"bithumb_median_spread_bps"`
	SpreadGapBPS           *float64 `json:"spread_gap_bps"`
	BithumbBuySlippageBPS  *float64 `json:"bithumb_buy_slippage_bps"`
	BithumbSellSlippageBPS *float64 `json:"bithumb_sell_slippage_bps"`
	BithumbBuyFillPct      *float64 `json:"bithumb_buy_fill_pct"`
	BithumbSellFillPct     *float64 `json:"bithumb_sell_fill_pct"`
}

type dashboardIntelligenceAsset struct {
	Asset                     string                              `json:"asset"`
	DomesticVolumeKRW         *float64                            `json:"domestic_volume_krw"`
	BithumbCapturePct         *float64                            `json:"bithumb_capture_pct"`
	CaptureGapVsVenuePP       *float64                            `json:"capture_gap_vs_venue_pp"`
	ShareChangeContributionPP *float64                            `json:"share_change_contribution_pp"`
	Microstructure            dashboardIntelligenceMicrostructure `json:"microstructure"`
}

type dashboardIntelligenceAttributionAsset struct {
	Asset                     string  `json:"asset"`
	ContributionPP            float64 `json:"contribution_pp"`
	BithumbVolumeChangeKRW    float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW float64 `json:"competitor_volume_change_krw"`
}

type dashboardIntelligenceAttributionProjection struct {
	*marketanalysis.Attribution
	Assets []dashboardIntelligenceAttributionAsset `json:"assets"`
	Quotes []marketanalysis.AttributionQuote       `json:"quotes"`
}

type dashboardIntelligenceAnalysisProjection struct {
	AnalysisProjection  string                                     `json:"analysis_projection"`
	GeneratedAt         float64                                    `json:"generated_at"`
	Context             marketanalysis.Context                     `json:"context"`
	OfficialDomestic24h official.DomesticTickerSummary             `json:"official_domestic_24h"`
	OfficialBinance24h  official.BinanceTicker24hSummary           `json:"official_binance_24h"`
	OfficialTicker1m    tickertrend.State                          `json:"official_ticker_1m"`
	RealtimeFlow        marketanalysis.RealtimeFlowState           `json:"realtime_flow"`
	Exchanges           []marketanalysis.ExchangeAnalysis          `json:"exchanges"`
	Liquidity           dashboardLiquidityProjection               `json:"liquidity"`
	ChangeAttribution   dashboardIntelligenceAttributionProjection `json:"change_attribution"`
	Assets              []dashboardIntelligenceAsset               `json:"assets"`
}

type dashboardSummary2AnalysisProjection struct {
	AnalysisProjection   string                            `json:"analysis_projection"`
	OfficialDomestic24h  *official.DomesticTickerSummary   `json:"official_domestic_24h"`
	OfficialShareMonitor *movement.State                   `json:"official_share_monitor"`
	OfficialTicker1m     *tickertrend.State                `json:"official_ticker_1m"`
	ChangeAttribution    dashboardAttributionProjection    `json:"change_attribution"`
	Exchanges            []marketanalysis.ExchangeAnalysis `json:"exchanges"`
	Liquidity            dashboardLiquidityProjection      `json:"liquidity"`
	Rolling24h           summary2RollingMonitorProjection  `json:"rolling_24h"`
	ReactionSummary      map[string]any                    `json:"reaction_summary"`
}

type summary2AnalysisProjection struct {
	WindowSec            int64                             `json:"window_sec"`
	Window               string                            `json:"window"`
	GeneratedAt          float64                           `json:"generated_at"`
	Definition           string                            `json:"definition"`
	Context              *marketanalysis.Context           `json:"context"`
	Summary              *marketanalysis.Summary           `json:"summary"`
	OfficialDomestic24h  *official.DomesticTickerSummary   `json:"official_domestic_24h"`
	OfficialBinance24h   *official.BinanceTicker24hSummary `json:"official_binance_24h"`
	OfficialShareMonitor *movement.State                   `json:"official_share_monitor"`
	OfficialTicker1m     *tickertrend.State                `json:"official_ticker_1m"`
	ChangeAttribution    *marketanalysis.Attribution       `json:"change_attribution"`
	Exchanges            []marketanalysis.ExchangeAnalysis `json:"exchanges"`
	Liquidity            *marketanalysis.LiquidityAnalysis `json:"liquidity"`
	RealtimeFlow         *marketanalysis.RealtimeFlowState `json:"realtime_flow"`
	Rolling24h           summary2RollingMonitorProjection  `json:"rolling_24h"`
	ReactionSummary      map[string]any                    `json:"reaction_summary"`
}

type summary2RollingMonitorProjection struct {
	*marketanalysis.RollingMonitor
	ClosedMinuteComparisons summary2ClosedMinuteComparisonsProjection `json:"closed_minute_comparisons"`
	Bridge                  summary2RollingBridgeProjection           `json:"bridge"`
	Bridges                 []summary2RollingBridgeProjection         `json:"bridges"`
	Evidence                summary2RollingEvidenceProjection         `json:"evidence"`
}

type summary2RollingBridgeProjection struct {
	*marketanalysis.RollingBridge
	AssetDrivers            []marketanalysis.RollingDriver      `json:"asset_drivers"`
	ExchangeDrivers         []marketanalysis.RollingDriver      `json:"exchange_drivers"`
	ObservedAssetDrivers    []marketanalysis.ObservedFlowDriver `json:"observed_asset_drivers"`
	ObservedExchangeDrivers []marketanalysis.ObservedFlowDriver `json:"observed_exchange_drivers"`
}

type summary2ClosedMinuteComparisonsProjection struct {
	*marketanalysis.ClosedMinuteComparisons
	Comparisons []summary2ClosedMinuteComparisonProjection `json:"comparisons"`
}

type summary2ClosedMinuteComparisonProjection struct {
	*marketanalysis.ClosedMinuteComparison
	AssetChanges []marketanalysis.ClosedMinuteAssetChange `json:"asset_changes"`
}

type summary2RollingEvidenceProjection struct {
	*marketanalysis.RollingEvidence
	DomesticNotices []*marketanalysis.RollingNoticeEvidence `json:"domestic_notices"`
}

func buildTypedAnalysisProjection(payload *marketanalysis.Analysis, view string, now time.Time) analysisProjection {
	if payload == nil {
		payload = &marketanalysis.Analysis{}
	}
	switch view {
	case "summary2":
		value := summary2AnalysisProjection{
			WindowSec: payload.WindowSec, Window: payload.Window,
			GeneratedAt: payload.GeneratedAt, Definition: payload.Definition,
			Context: &payload.Context, Summary: &payload.Summary,
			OfficialDomestic24h:  &payload.OfficialDomestic24h,
			OfficialBinance24h:   &payload.OfficialBinance24h,
			OfficialShareMonitor: compactMovementState(payload.OfficialShareMonitor),
			OfficialTicker1m:     &payload.OfficialTicker1m,
			ChangeAttribution:    &payload.ChangeAttribution,
			Exchanges:            payload.Exchanges,
			Liquidity:            &payload.Liquidity,
			RealtimeFlow:         &payload.RealtimeFlow,
			Rolling24h:           compactSummary2MonitorTyped(&payload.Rolling24h),
			ReactionSummary:      compactSummary2Reaction(payload.ReactionSummary),
		}
		return analysisProjection{full: &value, partial: &value}
	case "summary", "overview":
		return buildCompactAnalysisProjection(payload, view, compactAnalysisNotices(payload.Notices, now))
	default:
		return analysisProjection{
			full:    payload,
			partial: analysisWithoutNotices{Analysis: payload},
		}
	}
}

func buildCompactAnalysisProjection(payload *marketanalysis.Analysis, view string, notices map[string]any) analysisProjection {
	assets := compactAnalysisAssets(payload.Assets, view)
	full := &compactAnalysisProjection{Analysis: payload, Assets: assets, Notices: notices}
	partial := &compactAnalysisProjection{Analysis: payload, Assets: assets}
	return analysisProjection{full: full, partial: partial}
}

func buildDashboardAnalysisProjection(payload *marketanalysis.Analysis, view string, notices map[string]any) analysisProjection {
	if payload == nil {
		payload = &marketanalysis.Analysis{}
	}
	switch view {
	case "events":
		full := &dashboardEventsAnalysisProjection{
			AnalysisProjection: dashboardAnalysisProjection,
			WindowSec:          payload.WindowSec, Window: payload.Window,
			GeneratedAt: payload.GeneratedAt, Notices: &payload.Notices,
		}
		partial := &dashboardEventsAnalysisProjection{
			AnalysisProjection: dashboardAnalysisProjection,
			WindowSec:          payload.WindowSec, Window: payload.Window,
			GeneratedAt: payload.GeneratedAt,
		}
		return analysisProjection{full: full, partial: partial}
	case "summary", "overview":
		value := &dashboardSummaryAnalysisProjection{
			AnalysisProjection: dashboardAnalysisProjection,
			WindowSec:          payload.WindowSec, Window: payload.Window,
			Definition: payload.Definition, GeneratedAt: payload.GeneratedAt,
			Context: payload.Context, Summary: payload.Summary,
			OfficialDomestic24h: payload.OfficialDomestic24h,
			OfficialTicker1m:    payload.OfficialTicker1m,
			Exchanges:           payload.Exchanges, ExpertBrief: payload.ExpertBrief,
			ChangeAttribution: attributionWithoutRows(&payload.ChangeAttribution),
			Momentum:          payload.Momentum, EvidenceLayers: payload.EvidenceLayers,
			Collectors: payload.Collectors,
			Assets:     compactAnalysisAssets(payload.Assets, view),
			Liquidity:  compactDashboardLiquidity(payload.Liquidity),
		}
		if view == "overview" {
			// The overview lists no gap records (up to 100); the quality view does.
			value.DataQuality = withoutMapKeys(payload.DataQuality, "observation_gaps")
			value.Notices = notices
			value.OfficialShareMonitor = compactMovementState(payload.OfficialShareMonitor)
		}
		partial := *value
		partial.Notices = nil
		return analysisProjection{full: value, partial: &partial}
	case "summary2":
		value := &dashboardSummary2AnalysisProjection{
			AnalysisProjection:   dashboardAnalysisProjection,
			OfficialDomestic24h:  &payload.OfficialDomestic24h,
			OfficialShareMonitor: compactMovementState(payload.OfficialShareMonitor),
			OfficialTicker1m:     &payload.OfficialTicker1m,
			ChangeAttribution:    attributionWithoutRows(&payload.ChangeAttribution),
			Exchanges:            payload.Exchanges,
			Liquidity:            compactDashboardLiquidity(payload.Liquidity),
			Rolling24h:           compactSummary2MonitorTyped(&payload.Rolling24h),
			ReactionSummary:      compactSummary2Reaction(payload.ReactionSummary),
		}
		return analysisProjection{full: value, partial: value}
	case "full":
		assets := make([]dashboardIntelligenceAsset, len(payload.Assets))
		for index := range payload.Assets {
			row := &payload.Assets[index]
			assets[index] = dashboardIntelligenceAsset{
				Asset: row.Asset, DomesticVolumeKRW: row.DomesticVolumeKRW,
				BithumbCapturePct:         row.BithumbCapturePct,
				CaptureGapVsVenuePP:       row.CaptureGapVsVenuePP,
				ShareChangeContributionPP: row.ShareChangeContributionPP,
				Microstructure:            compactDashboardMicrostructure(row.Microstructure),
			}
		}
		attributionAssets := make([]dashboardIntelligenceAttributionAsset, len(payload.ChangeAttribution.Assets))
		for index := range payload.ChangeAttribution.Assets {
			row := &payload.ChangeAttribution.Assets[index]
			attributionAssets[index] = dashboardIntelligenceAttributionAsset{
				Asset: row.Asset, ContributionPP: row.ContributionPP,
				BithumbVolumeChangeKRW:    row.BithumbVolumeChangeKRW,
				CompetitorVolumeChangeKRW: row.CompetitorVolumeChangeKRW,
			}
		}
		value := &dashboardIntelligenceAnalysisProjection{
			AnalysisProjection: dashboardAnalysisProjection,
			GeneratedAt:        payload.GeneratedAt, Context: payload.Context,
			OfficialDomestic24h: payload.OfficialDomestic24h,
			OfficialBinance24h:  payload.OfficialBinance24h,
			OfficialTicker1m:    payload.OfficialTicker1m,
			RealtimeFlow:        payload.RealtimeFlow, Exchanges: payload.Exchanges,
			Liquidity: compactDashboardLiquidity(payload.Liquidity),
			ChangeAttribution: dashboardIntelligenceAttributionProjection{
				Attribution: &payload.ChangeAttribution,
				Assets:      attributionAssets,
				Quotes:      []marketanalysis.AttributionQuote{},
			},
			Assets: assets,
		}
		return analysisProjection{full: value, partial: value}
	default:
		return buildTypedAnalysisProjection(payload, view, time.Now())
	}
}

func buildBriefAnalysisProjection(payload *marketanalysis.Analysis) analysisProjection {
	if payload == nil {
		payload = &marketanalysis.Analysis{}
	}
	value := &dashboardBriefAnalysisProjection{
		AnalysisProjection: briefAnalysisProjection,
		WindowSec:          payload.WindowSec, Window: payload.Window,
		Definition: payload.Definition, GeneratedAt: payload.GeneratedAt,
		Context: payload.Context, Summary: payload.Summary,
		OfficialDomestic24h:  payload.OfficialDomestic24h,
		OfficialTicker1m:     payload.OfficialTicker1m,
		OfficialShareMonitor: compactMovementState(payload.OfficialShareMonitor),
		Exchanges:            payload.Exchanges,
		ExpertBrief: briefExpertBrief{
			AttributionHeadline: payload.ExpertBrief.AttributionHeadline,
			MajorAssets:         payload.ExpertBrief.MajorAssets,
			ListingSegments:     payload.ExpertBrief.ListingSegments,
		},
		ChangeAttribution: attributionWithoutRows(&payload.ChangeAttribution),
		Momentum:          payload.Momentum,
		Liquidity:         compactDashboardLiquidity(payload.Liquidity),
	}
	return analysisProjection{full: value, partial: value}
}

func buildDetailAnalysisProjection(payload *marketanalysis.Analysis, view string) analysisProjection {
	if payload == nil {
		payload = &marketanalysis.Analysis{}
	}
	value := &dashboardDetailAnalysisProjection{
		Analysis: payload, AnalysisProjection: detailAnalysisProjection,
		OfficialShareMonitor: compactMovementState(payload.OfficialShareMonitor),
		Liquidity: dashboardDetailLiquidity{
			dashboardLiquidityProjection: compactDashboardLiquidity(payload.Liquidity),
			PriceAnomalyPolicy:           payload.Liquidity.PriceAnomalyPolicy,
			QuarantinedAssets:            payload.Liquidity.QuarantinedAssets,
		},
	}
	if view == "quality" {
		// The quality screen shows no coin rows or notices.
		return analysisProjection{full: value, partial: value}
	}
	// An empty list stays [] (a nil interface is what omitempty drops).
	assets := make([]dashboardDriversAsset, len(payload.Assets))
	for index := range payload.Assets {
		assets[index] = dashboardDriversAsset{AssetAnalysis: &payload.Assets[index]}
	}
	value.Assets = assets
	value.ExpertBrief = detailExpertBrief{
		Breadth: payload.ExpertBrief.Breadth, ListingSegments: payload.ExpertBrief.ListingSegments,
	}
	partial := *value
	value.Notices = &payload.Notices
	return analysisProjection{full: value, partial: &partial}
}

// attributionWithoutRows is the change attribution without its coin and quote
// rows, which the summary screens do not list.
func attributionWithoutRows(source *marketanalysis.Attribution) dashboardAttributionProjection {
	return dashboardAttributionProjection{
		Attribution: source,
		Assets:      []marketanalysis.AttributionAsset{},
		Quotes:      []marketanalysis.AttributionQuote{},
	}
}

func compactDashboardLiquidity(source marketanalysis.LiquidityAnalysis) dashboardLiquidityProjection {
	return dashboardLiquidityProjection{
		AnalysisDepth:        source.AnalysisDepth,
		ReferenceNotionalKRW: source.ReferenceNotionalKRW,
		ObservedDepthOnly:    source.ObservedDepthOnly,
		ExtrapolationUsed:    source.ExtrapolationUsed,
		DomesticDepthKRW:     source.DomesticDepthKRW,
		BithumbDepthKRW:      source.BithumbDepthKRW,
		Exchanges:            source.Exchanges,
	}
}

func compactDashboardMicrostructure(source marketanalysis.AssetMicrostructure) dashboardIntelligenceMicrostructure {
	return dashboardIntelligenceMicrostructure{
		Status:                 source.Status,
		DomesticDepthKRW:       source.DomesticDepthKRW,
		BithumbDepthKRW:        source.BithumbDepthKRW,
		BithumbMedianSpreadBPS: source.BithumbMedianSpreadBPS,
		SpreadGapBPS:           source.SpreadGapBPS,
		BithumbBuySlippageBPS:  source.BithumbBuySlippageBPS,
		BithumbSellSlippageBPS: source.BithumbSellSlippageBPS,
		BithumbBuyFillPct:      source.BithumbBuyFillPct,
		BithumbSellFillPct:     source.BithumbSellFillPct,
	}
}

func compactAnalysisAssets(assets []marketanalysis.AssetAnalysis, view string) []compactAnalysisAsset {
	if assets == nil {
		return nil
	}
	rows := make([]compactAnalysisAsset, 0, len(assets))
	for index := range assets {
		asset := &assets[index]
		exchanges := asset.DomesticExchanges
		if exchanges == nil {
			exchanges = []string{}
		}
		domesticVolume := 0.0
		if asset.DomesticVolumeKRW != nil {
			domesticVolume = *asset.DomesticVolumeKRW
		}
		row := compactAnalysisAsset{
			Asset: asset.Asset, DomesticExchanges: exchanges,
			DomesticVolumeKRW: domesticVolume,
		}
		if view == "summary" {
			row.BithumbVolumeKRW = &asset.BithumbVolumeKRW
			row.CompetitorVolumeKRW = &asset.CompetitorVolumeKRW
			row.ListedOnBithumb = &asset.ListedOnBithumb
			row.ExclusiveOnBithumb = &asset.ExclusiveOnBithumb
		}
		rows = append(rows, row)
	}
	return rows
}

func compactAnalysisNotices(snapshot notice.Snapshot, now time.Time) map[string]any {
	var rows []any
	cutoffMS := float64(now.Add(-5 * time.Minute).UnixMilli())
	if snapshot.ContextualItems != nil {
		rows = make([]any, 0, len(snapshot.ContextualItems))
		for _, raw := range snapshot.ContextualItems {
			item, ok := raw.(map[string]any)
			if !ok || !noticeRelevant(item, cutoffMS) {
				continue
			}
			rows = append(rows, compactNotice(item))
		}
	} else if snapshot.Items != nil {
		rows = make([]any, 0, len(snapshot.Items))
		for _, item := range snapshot.Items {
			if !typedNoticeRelevant(item, int64(cutoffMS)) {
				continue
			}
			rows = append(rows, compactTypedNotice(item))
		}
	}
	result := map[string]any{
		"version": snapshot.Version, "poll_interval_sec": snapshot.PollIntervalSec,
		"history_days": snapshot.HistoryDays, "ocr_backend": snapshot.OCRBackend,
		"healthy_source_count":          snapshot.HealthySourceCount,
		"complete_history_source_count": snapshot.CompleteHistorySourceCount,
		"active_error_count":            snapshot.ActiveErrorCount, "error_history": snapshot.ErrorHistory,
		"source_count": snapshot.SourceCount, "sources": snapshot.Sources,
		"items": rows,
	}
	if snapshot.ContextualItems != nil {
		result["causal_claim"] = false
	}
	return result
}

func typedNoticeRelevant(item notice.Item, cutoffMS int64) bool {
	precision := item.TimestampPrecision
	if precision == "" {
		precision = "SECOND"
	}
	if precision == "SECOND" && item.PublishedTSMS >= cutoffMS {
		return true
	}
	for _, schedule := range item.Schedules {
		if schedule.StartTSMS >= cutoffMS || schedule.EndTSMS >= cutoffMS {
			return true
		}
	}
	return precision == "DETECTED" && item.DetectedTSMS >= cutoffMS
}

func compactTypedNotice(item notice.Item) map[string]any {
	assets := item.Assets
	if assets == nil {
		assets = []string{}
	}
	schedules := item.Schedules
	if schedules == nil {
		schedules = []notice.Schedule{}
	}
	return map[string]any{
		"exchange": item.Exchange, "notice_id": item.NoticeID,
		"title": item.Title, "url": item.URL, "category": item.Category,
		"published_ts_ms": item.PublishedTSMS, "detected_ts_ms": item.DetectedTSMS,
		"timestamp_precision": item.TimestampPrecision, "assets": assets,
		"market_context": []any{}, "summary": item.Summary, "schedules": schedules,
		"within_selected_window": false, "reaction_studies": []any{},
	}
}

func compactSummary2MonitorTyped(source *marketanalysis.RollingMonitor) summary2RollingMonitorProjection {
	if source == nil {
		source = &marketanalysis.RollingMonitor{}
	}
	result := summary2RollingMonitorProjection{
		RollingMonitor: source,
		Bridge:         compactSummary2BridgeTyped(&source.Bridge),
		Evidence: summary2RollingEvidenceProjection{
			RollingEvidence: &source.Evidence,
			DomesticNotices: compactSummary2NoticesTyped(source.Evidence.DomesticNotices),
		},
		ClosedMinuteComparisons: summary2ClosedMinuteComparisonsProjection{
			ClosedMinuteComparisons: &source.ClosedMinuteComparisons,
		},
	}
	if source.Bridges != nil {
		result.Bridges = make([]summary2RollingBridgeProjection, len(source.Bridges))
		for index := range source.Bridges {
			result.Bridges[index] = compactSummary2BridgeTyped(&source.Bridges[index])
		}
	}
	comparisons := source.ClosedMinuteComparisons.Comparisons
	if comparisons != nil {
		result.ClosedMinuteComparisons.Comparisons = make([]summary2ClosedMinuteComparisonProjection, len(comparisons))
		for index := range comparisons {
			comparison := &comparisons[index]
			result.ClosedMinuteComparisons.Comparisons[index] = summary2ClosedMinuteComparisonProjection{
				ClosedMinuteComparison: comparison,
				AssetChanges:           limitRows(comparison.AssetChanges),
			}
		}
	}
	return result
}

func compactSummary2BridgeTyped(source *marketanalysis.RollingBridge) summary2RollingBridgeProjection {
	if source == nil {
		source = &marketanalysis.RollingBridge{}
	}
	return summary2RollingBridgeProjection{
		RollingBridge:           source,
		AssetDrivers:            limitRows(source.AssetDrivers),
		ExchangeDrivers:         limitRows(source.ExchangeDrivers),
		ObservedAssetDrivers:    limitRows(source.ObservedAssetDrivers),
		ObservedExchangeDrivers: limitRows(source.ObservedExchangeDrivers),
	}
}

func compactSummary2NoticesTyped(source []marketanalysis.RollingNoticeEvidence) []*marketanalysis.RollingNoticeEvidence {
	if source == nil {
		return nil
	}
	counts := make(map[string]int)
	capacity := min(len(source), summary2RowLimit*len(domain.DomesticExchanges))
	rows := make([]*marketanalysis.RollingNoticeEvidence, 0, capacity)
	for index := range source {
		item := &source[index]
		if counts[item.Exchange] >= summary2RowLimit {
			continue
		}
		counts[item.Exchange]++
		rows = append(rows, item)
	}
	return rows
}

func withoutMapKeys(source map[string]any, keys ...string) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	for _, key := range keys {
		delete(result, key)
	}
	return result
}

// compactMovementState keeps each official movement event's top coin drivers.
// Every event lists all ~500 coins (about 190 KB), the screens draw the top six,
// and the stream resends the state every few seconds; the HTTP event detail
// (/official-movements/{id}) still returns the full list.
func compactMovementState(state movement.State) *movement.State {
	compact := func(event movement.Event) movement.Event {
		event.Drivers = limitRows(event.Drivers)
		return event
	}
	if state.Movement != nil {
		current := compact(*state.Movement)
		state.Movement = &current
	}
	if state.RecentEvents != nil {
		events := make([]movement.Event, len(state.RecentEvents))
		for index, event := range state.RecentEvents {
			events[index] = compact(event)
		}
		state.RecentEvents = events
	}
	return &state
}

func limitRows[T any](rows []T) []T {
	if len(rows) > summary2RowLimit {
		return rows[:summary2RowLimit]
	}
	return rows
}

func compactSummary2Reaction(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	return selectMapKeys(source, "analysis_context_id", "origin", "top_notice")
}
