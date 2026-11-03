package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	"market-share-go/internal/domain"
	"market-share-go/internal/orderbook"
)

type LiquidityExchange struct {
	Exchange                           domain.Exchange     `json:"exchange"`
	RegisteredMarketCount              int                 `json:"registered_market_count"`
	LiveMarketCount                    int                 `json:"live_market_count"`
	LiveMarketPct                      *float64            `json:"live_market_pct"`
	RegisteredAssetCount               int                 `json:"registered_asset_count"`
	LiveAssetCount                     int                 `json:"live_asset_count"`
	LiveAssetPct                       *float64            `json:"live_asset_pct"`
	FullyLiveAssetCount                int                 `json:"fully_live_asset_count"`
	PartialLiveAssetCount              int                 `json:"partial_live_asset_count"`
	UnavailableAssetCount              int                 `json:"unavailable_asset_count"`
	RegisteredAssetSelectedVolumeKRW   float64             `json:"registered_asset_selected_volume_krw"`
	LiveAssetSelectedVolumeKRW         float64             `json:"live_asset_selected_volume_krw"`
	LiveAssetSelectedVolumeCoveragePct *float64            `json:"live_asset_selected_volume_coverage_pct"`
	BuyCompleteAssetPct                *float64            `json:"buy_complete_asset_pct"`
	SellCompleteAssetPct               *float64            `json:"sell_complete_asset_pct"`
	MedianAssetSpreadBPS               *float64            `json:"median_asset_spread_bps"`
	AverageBuyAssetSlippageBPS         *float64            `json:"average_buy_asset_slippage_bps"`
	AverageSellAssetSlippageBPS        *float64            `json:"average_sell_asset_slippage_bps"`
	AverageBuyAssetSlippageScenarios   []SlippageScenario  `json:"average_buy_asset_slippage_scenarios"`
	AverageSellAssetSlippageScenarios  []SlippageScenario  `json:"average_sell_asset_slippage_scenarios"`
	DepthKRW                           *float64            `json:"depth_krw"`
	CumulativeDepthKRW                 map[string]*float64 `json:"cumulative_depth_krw"`
	BidDepthKRW                        *float64            `json:"bid_depth_krw"`
	AskDepthKRW                        *float64            `json:"ask_depth_krw"`
	LiquiditySharePct                  *float64            `json:"liquidity_share_pct"`
	MedianSpreadBPS                    *float64            `json:"median_spread_bps"`
	AverageSpreadBPS                   *float64            `json:"average_spread_bps"`
	AverageImbalance                   *float64            `json:"average_imbalance"`
	AverageUpdateRateHz                *float64            `json:"average_update_rate_hz"`
	AverageEventLatencyMS              *float64            `json:"average_event_latency_ms"`
	BuyCompleteMarketPct               *float64            `json:"buy_complete_market_pct"`
	SellCompleteMarketPct              *float64            `json:"sell_complete_market_pct"`
	AverageBuySlippageBPS              *float64            `json:"average_buy_slippage_bps"`
	AverageSellSlippageBPS             *float64            `json:"average_sell_slippage_bps"`
	KnownBidOrderCount                 int64               `json:"known_bid_order_count"`
	KnownAskOrderCount                 int64               `json:"known_ask_order_count"`
	SequenceGapCount                   int64               `json:"sequence_gap_count"`
}

type SlippageScenario struct {
	NotionalKRW        float64  `json:"notional_krw"`
	AverageSlippageBPS *float64 `json:"average_slippage_bps"`
	CompleteAssetCount int      `json:"complete_asset_count"`
	CompleteAssetPct   *float64 `json:"complete_asset_pct"`
}

type ListingSlippageExchange struct {
	Exchange                          domain.Exchange    `json:"exchange"`
	RegisteredAssetCount              int                `json:"registered_asset_count"`
	LiveAssetCount                    int                `json:"live_asset_count"`
	AverageBuyAssetSlippageScenarios  []SlippageScenario `json:"average_buy_asset_slippage_scenarios"`
	AverageSellAssetSlippageScenarios []SlippageScenario `json:"average_sell_asset_slippage_scenarios"`
}

type SlippageListingGroup struct {
	Key                  string                    `json:"key"`
	Label                string                    `json:"label"`
	RegisteredAssetCount int                       `json:"registered_asset_count"`
	Exchanges            []ListingSlippageExchange `json:"exchanges"`
}

type AssetExchangeSlippage struct {
	Asset             string          `json:"asset"`
	Exchange          domain.Exchange `json:"exchange"`
	Market            string          `json:"market"`
	SelectedVolumeKRW float64         `json:"selected_volume_krw"`
	Status            string          `json:"status"`
	AgeSeconds        *float64        `json:"age_sec"`
	DepthKRW          *float64        `json:"depth_krw"`
	BuyComplete       bool            `json:"buy_complete"`
	SellComplete      bool            `json:"sell_complete"`
	BuyFillPct        float64         `json:"buy_fill_pct"`
	SellFillPct       float64         `json:"sell_fill_pct"`
	BuySlippageBPS    *float64        `json:"buy_slippage_bps"`
	SellSlippageBPS   *float64        `json:"sell_slippage_bps"`
	BestBidKRW        *float64        `json:"best_bid_krw"`
	BestAskKRW        *float64        `json:"best_ask_krw"`
	BuyVWAPKRW        *float64        `json:"buy_vwap_krw"`
	SellVWAPKRW       *float64        `json:"sell_vwap_krw"`
}
type PriceValidation struct {
	Status                 string   `json:"status"`
	Reason                 string   `json:"reason"`
	ObservedGapBPS         *float64 `json:"observed_gap_bps"`
	QuarantineThresholdBPS float64  `json:"quarantine_threshold_bps"`
	BuyExchange            *string  `json:"buy_exchange"`
	SellExchange           *string  `json:"sell_exchange"`
	MaxOrderBookAgeSec     *float64 `json:"max_orderbook_age_sec"`
}

// PriceDislocation is deliberately typed because this object is built once per
// analyzed asset. The previous map[string]any representation allocated two
// maps and boxed every scalar on each analysis refresh.
type PriceDislocation struct {
	ReferenceNotionalKRW                    float64 `json:"reference_notional_krw"`
	FeesIncluded                            bool    `json:"fees_included"`
	ExecutableWithinObservedTenLevels       bool    `json:"executable_within_observed_ten_levels"`
	MatchedQuantityIsConservative           bool    `json:"matched_quantity_is_conservative"`
	LatencyIncluded                         bool    `json:"latency_included"`
	BalanceAvailabilityIncluded             bool    `json:"balance_availability_included"`
	TransferAssumed                         bool    `json:"transfer_assumed"`
	TradeSignal                             bool    `json:"trade_signal"`
	PriceSignalReleased                     *bool   `json:"price_signal_released,omitempty"`
	BuyExchange, BuyMarket                  string
	SellExchange, SellMarket                string
	BuyVWAPKRW, SellVWAPKRW                 float64
	BuyOrderBookAgeSec, SellOrderBookAgeSec float64
	GrossGapBPS, MatchedBaseQuantity        float64
	GrossBuyCostKRW, GrossSellProceedsKRW   float64
	GrossProfitKRW                          float64
	BuyTakerFeeRate, SellTakerFeeRate       *float64
	BuyFeeSource, SellFeeSource             string
	BuyFeeKRW, SellFeeKRW                   *float64
	NetBuyCostKRW, NetSellProceedsKRW       *float64
	NetProfitKRW, NetGapBPS                 *float64
	hasExecutableGap                        bool
}

type priceDislocationBaseJSON struct {
	ReferenceNotionalKRW              float64 `json:"reference_notional_krw"`
	FeesIncluded                      bool    `json:"fees_included"`
	ExecutableWithinObservedTenLevels bool    `json:"executable_within_observed_ten_levels"`
	MatchedQuantityIsConservative     bool    `json:"matched_quantity_is_conservative"`
	LatencyIncluded                   bool    `json:"latency_included"`
	BalanceAvailabilityIncluded       bool    `json:"balance_availability_included"`
	TransferAssumed                   bool    `json:"transfer_assumed"`
	TradeSignal                       bool    `json:"trade_signal"`
	PriceSignalReleased               *bool   `json:"price_signal_released,omitempty"`
}

func (p PriceDislocation) baseJSON() priceDislocationBaseJSON {
	return priceDislocationBaseJSON{
		ReferenceNotionalKRW: p.ReferenceNotionalKRW, FeesIncluded: p.FeesIncluded,
		ExecutableWithinObservedTenLevels: p.ExecutableWithinObservedTenLevels,
		MatchedQuantityIsConservative:     p.MatchedQuantityIsConservative,
		LatencyIncluded:                   p.LatencyIncluded, BalanceAvailabilityIncluded: p.BalanceAvailabilityIncluded,
		TransferAssumed: p.TransferAssumed, TradeSignal: p.TradeSignal,
		PriceSignalReleased: p.PriceSignalReleased,
	}
}

func (p PriceDislocation) MarshalJSON() ([]byte, error) {
	base := p.baseJSON()
	if !p.hasExecutableGap {
		return json.Marshal(base)
	}
	return json.Marshal(struct {
		priceDislocationBaseJSON
		BuyExchange          string   `json:"buy_exchange"`
		BuyMarket            string   `json:"buy_market"`
		SellExchange         string   `json:"sell_exchange"`
		SellMarket           string   `json:"sell_market"`
		BuyVWAPKRW           float64  `json:"buy_vwap_krw"`
		SellVWAPKRW          float64  `json:"sell_vwap_krw"`
		BuyOrderBookAgeSec   float64  `json:"buy_orderbook_age_sec"`
		SellOrderBookAgeSec  float64  `json:"sell_orderbook_age_sec"`
		GrossGapBPS          float64  `json:"gross_gap_bps"`
		MatchedBaseQuantity  float64  `json:"matched_base_quantity"`
		GrossBuyCostKRW      float64  `json:"gross_buy_cost_krw"`
		GrossSellProceedsKRW float64  `json:"gross_sell_proceeds_krw"`
		GrossProfitKRW       float64  `json:"gross_profit_krw"`
		BuyTakerFeeRate      *float64 `json:"buy_taker_fee_rate"`
		SellTakerFeeRate     *float64 `json:"sell_taker_fee_rate"`
		BuyFeeSource         string   `json:"buy_fee_source"`
		SellFeeSource        string   `json:"sell_fee_source"`
		BuyFeeKRW            *float64 `json:"buy_fee_krw"`
		SellFeeKRW           *float64 `json:"sell_fee_krw"`
		NetBuyCostKRW        *float64 `json:"net_buy_cost_krw"`
		NetSellProceedsKRW   *float64 `json:"net_sell_proceeds_krw"`
		NetProfitKRW         *float64 `json:"net_profit_krw"`
		NetGapBPS            *float64 `json:"net_gap_bps"`
	}{
		priceDislocationBaseJSON: base,
		BuyExchange:              p.BuyExchange, BuyMarket: p.BuyMarket,
		SellExchange: p.SellExchange, SellMarket: p.SellMarket,
		BuyVWAPKRW: p.BuyVWAPKRW, SellVWAPKRW: p.SellVWAPKRW,
		BuyOrderBookAgeSec: p.BuyOrderBookAgeSec, SellOrderBookAgeSec: p.SellOrderBookAgeSec,
		GrossGapBPS: p.GrossGapBPS, MatchedBaseQuantity: p.MatchedBaseQuantity,
		GrossBuyCostKRW: p.GrossBuyCostKRW, GrossSellProceedsKRW: p.GrossSellProceedsKRW,
		GrossProfitKRW: p.GrossProfitKRW, BuyTakerFeeRate: p.BuyTakerFeeRate,
		SellTakerFeeRate: p.SellTakerFeeRate, BuyFeeSource: p.BuyFeeSource,
		SellFeeSource: p.SellFeeSource, BuyFeeKRW: p.BuyFeeKRW, SellFeeKRW: p.SellFeeKRW,
		NetBuyCostKRW: p.NetBuyCostKRW, NetSellProceedsKRW: p.NetSellProceedsKRW,
		NetProfitKRW: p.NetProfitKRW, NetGapBPS: p.NetGapBPS,
	})
}

type AssetMicrostructure struct {
	Asset                        string           `json:"asset"`
	AssetIdentity                AssetIdentity    `json:"asset_identity"`
	Status                       string           `json:"status"`
	LiveMarketCount              int              `json:"live_market_count"`
	BithumbLiveMarketCount       int              `json:"bithumb_live_market_count"`
	BithumbOrderBookMaxAgeSec    *float64         `json:"bithumb_orderbook_max_age_sec"`
	CompetitorOrderBookMaxAgeSec *float64         `json:"competitor_orderbook_max_age_sec"`
	DomesticDepthKRW             *float64         `json:"domestic_depth_krw"`
	BithumbDepthKRW              *float64         `json:"bithumb_depth_krw"`
	CompetitorDepthKRW           *float64         `json:"competitor_depth_krw"`
	BithumbLiquidityCapturePct   *float64         `json:"bithumb_liquidity_capture_pct"`
	BithumbMedianSpreadBPS       *float64         `json:"bithumb_median_spread_bps"`
	BestCompetitorSpreadBPS      *float64         `json:"best_competitor_spread_bps"`
	SpreadGapBPS                 *float64         `json:"spread_gap_bps"`
	BithumbBuySlippageBPS        *float64         `json:"bithumb_buy_slippage_bps"`
	BithumbSellSlippageBPS       *float64         `json:"bithumb_sell_slippage_bps"`
	BithumbBuyFillPct            *float64         `json:"bithumb_buy_fill_pct"`
	BithumbSellFillPct           *float64         `json:"bithumb_sell_fill_pct"`
	BithumbKRWMarket             string           `json:"bithumb_krw_market,omitempty"`
	BithumbBestBidKRW            *float64         `json:"bithumb_best_bid_krw"`
	BithumbBestAskKRW            *float64         `json:"bithumb_best_ask_krw"`
	BithumbBuyVWAPKRW            *float64         `json:"bithumb_buy_vwap_krw"`
	BithumbSellVWAPKRW           *float64         `json:"bithumb_sell_vwap_krw"`
	BithumbBuyBaseQuantity       *float64         `json:"bithumb_buy_base_quantity"`
	BithumbSellBaseQuantity      *float64         `json:"bithumb_sell_base_quantity"`
	BestCompetitorBuySlippageBPS *float64         `json:"best_competitor_buy_slippage_bps"`
	BuySlippageGapBPS            *float64         `json:"buy_slippage_gap_bps"`
	BestBithumbAskKRW            *float64         `json:"best_bithumb_ask_krw"`
	BestCompetitorAskKRW         *float64         `json:"best_competitor_ask_krw"`
	BithumbBuyPriceGapBPS        *float64         `json:"bithumb_buy_price_gap_bps"`
	GlobalLiveMarketCount        int              `json:"global_live_market_count"`
	GlobalDepthKRW               *float64         `json:"global_depth_krw"`
	GlobalReferenceMidKRW        *float64         `json:"global_reference_mid_krw"`
	GlobalReferenceExchangeCount int              `json:"global_reference_exchange_count"`
	GlobalReferenceExchanges     []string         `json:"global_reference_exchanges"`
	GlobalReferenceDispersionBPS *float64         `json:"global_reference_dispersion_bps"`
	GlobalBestBidKRW             *float64         `json:"global_best_bid_krw"`
	GlobalBestAskKRW             *float64         `json:"global_best_ask_krw"`
	BithumbMidKRW                *float64         `json:"bithumb_mid_krw"`
	BithumbGlobalPremiumBPS      *float64         `json:"bithumb_global_premium_bps"`
	GlobalReferenceMethod        string           `json:"global_reference_method"`
	PriceValidation              PriceValidation  `json:"price_validation"`
	PriceDislocation             PriceDislocation `json:"price_dislocation"`
}
type LiquidityAnalysis struct {
	AnalysisDepth         int                     `json:"analysis_depth"`
	ReferenceNotionalKRW  float64                 `json:"reference_notional_krw"`
	ObservedDepthOnly     bool                    `json:"observed_depth_only"`
	ExtrapolationUsed     bool                    `json:"extrapolation_used"`
	DomesticDepthKRW      *float64                `json:"domestic_depth_krw"`
	BithumbDepthKRW       *float64                `json:"bithumb_depth_krw"`
	Exchanges             []LiquidityExchange     `json:"exchanges"`
	SlippageListingGroups []SlippageListingGroup  `json:"slippage_listing_groups"`
	AssetExchanges        []AssetExchangeSlippage `json:"asset_exchanges"`
	ComparablePolicy      map[string]any          `json:"comparable_policy"`
	ComparableExchanges   []any                   `json:"comparable_exchanges"`
	PriceAnomalyPolicy    map[string]any          `json:"price_anomaly_policy"`
	QuarantinedAssets     []any                   `json:"quarantined_assets"`
	GlobalDepthKRW        *float64                `json:"global_depth_krw"`
	GlobalExchanges       []LiquidityExchange     `json:"global_exchanges"`
	Assets                []AssetMicrostructure   `json:"assets"`
	GlobalOrderBookStatus string                  `json:"global_orderbook_status,omitempty"`
}

type TakerFee struct {
	Rate   float64
	Source string
}

// ContractIdentityProof is operator-reviewed evidence that exact exchange
// markets trade the same on-chain asset. Symbol equality alone is never proof.
type ContractIdentityProof struct {
	CanonicalAssetID string            `json:"canonical_asset_id"`
	Source           string            `json:"source"`
	Markets          map[string]string `json:"markets"`
	Evidence         map[string]string `json:"evidence"`
}

func (p ContractIdentityProof) Validate(asset string) error {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	canonical := strings.TrimSpace(p.CanonicalAssetID)
	if asset == "" {
		return fmt.Errorf("asset symbol is empty")
	}
	if canonical == "" || strings.EqualFold(canonical, "SYMBOL:"+asset) {
		return fmt.Errorf("canonical_asset_id must identify the contract or native network asset")
	}
	if source := strings.TrimSpace(p.Source); source == "" || source == "NORMALIZED_BASE_SYMBOL_ONLY" {
		return fmt.Errorf("source must describe independently verified identity evidence")
	}
	if len(p.Markets) < 2 {
		return fmt.Errorf("at least two exact exchange markets are required")
	}
	for key, marketCanonical := range p.Markets {
		exchange, market, ok := contractMarketKeyParts(key)
		if !ok {
			return fmt.Errorf("invalid market key %q; expected exchange:market", key)
		}
		if key != ContractMarketKey(exchange, market) {
			return fmt.Errorf("market key %q is not normalized", key)
		}
		if strings.TrimSpace(marketCanonical) != canonical {
			return fmt.Errorf("market %q identity does not match canonical_asset_id", key)
		}
		reference, err := url.Parse(strings.TrimSpace(p.Evidence[key]))
		if err != nil || reference.Scheme != "https" || reference.Host == "" {
			return fmt.Errorf("market %q requires an HTTPS evidence URL", key)
		}
	}
	return nil
}

type LiquidityOptions struct {
	ctx                    context.Context
	NotionalKRW            float64
	SlippageNotionalsKRW   []float64
	QuarantineBPS          float64
	Weights                map[string]float64
	TakerFees              map[string]TakerFee
	ContractIdentities     map[string]ContractIdentityProof
	GlobalCoverageComplete bool
}

func BuildLiquidity(rows []orderbook.Row, notional, quarantine float64, weights map[string]float64) LiquidityAnalysis {
	return BuildLiquidityWithOptions(rows, LiquidityOptions{NotionalKRW: notional, QuarantineBPS: quarantine, Weights: weights})
}

func BuildLiquidityContext(ctx context.Context, rows []orderbook.Row, options LiquidityOptions) (LiquidityAnalysis, error) {
	options.ctx = ctx
	if err := ctx.Err(); err != nil {
		return LiquidityAnalysis{}, err
	}
	result := BuildLiquidityWithOptions(rows, options)
	if err := ctx.Err(); err != nil {
		return LiquidityAnalysis{}, err
	}
	return result, nil
}
func BuildLiquidityWithOptions(rows []orderbook.Row, options LiquidityOptions) LiquidityAnalysis {

	notional, quarantine, weights := options.NotionalKRW, max(0, options.QuarantineBPS), options.Weights
	domestic := make([]*orderbook.Row, 0, len(rows))
	global := make([]*orderbook.Row, 0, len(rows)/4)
	for index := range rows {
		if analysisCanceled(options.ctx) {
			return LiquidityAnalysis{}
		}
		row := &rows[index]
		switch row.Scope {
		case domain.ScopeDomestic:
			domestic = append(domestic, row)
		case domain.ScopeGlobal:
			if domain.IsGlobalReferenceExchange(row.Exchange) {
				global = append(global, row)
			}
		}
	}
	referenceRows := make([]*orderbook.Row, 0, len(domestic)+len(global))
	referenceRows = append(referenceRows, domestic...)
	referenceRows = append(referenceRows, global...)
	domesticLive, globalLive := usableBooks(domestic), usableBooks(global)
	domesticDepth := sumBookPointers(domesticLive, func(row *orderbook.Row) *float64 { return row.DepthKRW })
	globalDepth := sumBookPointers(globalLive, func(row *orderbook.Row) *float64 { return row.DepthKRW })
	scenarioNotionals := liquidityScenarioNotionals(referenceRows, options.SlippageNotionalsKRW)
	result := LiquidityAnalysis{AnalysisDepth: orderbook.AnalysisDepth, ReferenceNotionalKRW: notional, ObservedDepthOnly: true, ExtrapolationUsed: false, DomesticDepthKRW: domesticDepth, Exchanges: summarizeBookExchanges(domestic, domesticDepth, weights, scenarioNotionals), SlippageListingGroups: buildSlippageListingGroups(domestic, scenarioNotionals), AssetExchanges: buildAssetExchangeSlippage(domestic, weights), ComparablePolicy: map[string]any{"analysis_depth": 10, "both_sides_complete_required": true, "fresh_orderbook_required": true, "fx_required": true, "asset_weight": "SELECTED_WINDOW_DOMESTIC_VOLUME", "canonical_policy": "NORMALIZED_BASE_SYMBOL", "identity_status": "UNVERIFIED", "identity_source": "NORMALIZED_BASE_SYMBOL_ONLY", "contract_identity_verified": false}, ComparableExchanges: buildComparableExchanges(domestic, weights), PriceAnomalyPolicy: map[string]any{"quarantine_threshold_bps": quarantine, "contract_identity_required_for_release": true, "released_price_signals": false}, QuarantinedAssets: []any{}, GlobalDepthKRW: globalDepth, GlobalExchanges: summarizeBookExchanges(global, globalDepth, weights, nil), GlobalOrderBookStatus: "UNAVAILABLE"}
	result.BithumbDepthKRW = sumBookPointersWhere(domesticLive, func(row *orderbook.Row) bool {
		return row.Exchange == domain.ExchangeBithumb
	}, func(row *orderbook.Row) *float64 { return row.DepthKRW })
	result.Assets = buildAssetMicrostructures(referenceRows, options)
	result.QuarantinedAssets = quarantinedAssetRows(result.Assets)
	applyIdentityPolicy(&result)
	if len(globalLive) > 0 {
		result.GlobalOrderBookStatus = "AVAILABLE"
	}
	return result
}

func liquidityScenarioNotionals(rows []*orderbook.Row, configured []float64) []float64 {
	values := map[float64]struct{}{}
	for _, value := range configured {
		if value > 0 {
			values[value] = struct{}{}
		}
	}
	if len(configured) == 0 {
		for _, row := range rows {
			for _, scenario := range append(append([]orderbook.ExecutionScenario(nil), row.BuyExecutionScenarios...), row.SellExecutionScenarios...) {
				if scenario.NotionalKRW > 0 {
					values[scenario.NotionalKRW] = struct{}{}
				}
			}
		}
	}
	result := make([]float64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Float64s(result)
	return result
}

type comparableBook struct {
	weight              float64
	bithumb, competitor *orderbook.Row
}

type executableBookKey struct {
	exchange domain.Exchange
	asset    string
}

type executableBookCandidate struct {
	row   *orderbook.Row
	score float64
}

func buildComparableExchanges(rows []*orderbook.Row, weights map[string]float64) []any {
	registered := make(map[domain.Exchange]map[string]struct{}, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		registered[exchange] = map[string]struct{}{}
	}
	for _, row := range rows {
		registered[row.Exchange][row.Base] = struct{}{}
	}
	executable := indexExecutableBooks(rows)
	totalWeight := 0.0
	for _, weight := range weights {
		totalWeight += max(0, weight)
	}
	result := make([]any, 0, len(domain.DomesticExchanges)-1)
	for _, exchange := range domain.DomesticExchanges {
		if exchange == domain.ExchangeBithumb {
			continue
		}
		shared := make([]string, 0)
		for asset := range registered[domain.ExchangeBithumb] {
			if _, ok := registered[exchange][asset]; ok {
				shared = append(shared, asset)
			}
		}
		sort.Strings(shared)
		comparable := make([]comparableBook, 0, len(shared))
		for _, asset := range shared {
			bithumb, bithumbOK := executable[executableBookKey{exchange: domain.ExchangeBithumb, asset: asset}]
			competitor, competitorOK := executable[executableBookKey{exchange: exchange, asset: asset}]
			if bithumbOK && competitorOK {
				comparable = append(comparable, comparableBook{max(0, weights[asset]), bithumb.row, competitor.row})
			}
		}
		selectedWeight := 0.0
		for _, row := range comparable {
			selectedWeight += row.weight
		}
		bithumbDepth := comparableDepth(comparable, true)
		competitorDepth := comparableDepth(comparable, false)
		var depthShare *float64
		if bithumbDepth != nil && competitorDepth != nil {
			depthShare = percent(*bithumbDepth, *bithumbDepth+*competitorDepth)
		}
		weightingStatus := "NO_POSITIVE_SELECTED_VOLUME"
		if selectedWeight > 0 {
			weightingStatus = "SELECTED_WINDOW_DOMESTIC_VOLUME"
		}
		result = append(result, map[string]any{
			"exchange": exchange, "shared_asset_count": len(shared), "comparable_asset_count": len(comparable),
			"comparable_asset_pct": percent(float64(len(comparable)), float64(len(shared))),
			"selected_volume_krw":  selectedWeight, "selected_volume_coverage_pct": percent(selectedWeight, totalWeight),
			"weighted_median_bithumb_buy_slippage_bps":    weightedComparable(comparable, func(row comparableBook) *float64 { return row.bithumb.BuyExecution.SlippageBPS }),
			"weighted_median_competitor_buy_slippage_bps": weightedComparable(comparable, func(row comparableBook) *float64 { return row.competitor.BuyExecution.SlippageBPS }),
			"weighted_median_buy_slippage_gap_bps": weightedComparable(comparable, func(row comparableBook) *float64 {
				return difference(row.bithumb.BuyExecution.SlippageBPS, row.competitor.BuyExecution.SlippageBPS)
			}),
			"weighted_median_bithumb_sell_slippage_bps":    weightedComparable(comparable, func(row comparableBook) *float64 { return row.bithumb.SellExecution.SlippageBPS }),
			"weighted_median_competitor_sell_slippage_bps": weightedComparable(comparable, func(row comparableBook) *float64 { return row.competitor.SellExecution.SlippageBPS }),
			"weighted_median_sell_slippage_gap_bps": weightedComparable(comparable, func(row comparableBook) *float64 {
				return difference(row.bithumb.SellExecution.SlippageBPS, row.competitor.SellExecution.SlippageBPS)
			}),
			"weighted_median_bithumb_spread_bps":    weightedComparable(comparable, func(row comparableBook) *float64 { return row.bithumb.SpreadBPS }),
			"weighted_median_competitor_spread_bps": weightedComparable(comparable, func(row comparableBook) *float64 { return row.competitor.SpreadBPS }),
			"weighted_median_spread_gap_bps":        weightedComparable(comparable, func(row comparableBook) *float64 { return difference(row.bithumb.SpreadBPS, row.competitor.SpreadBPS) }),
			"bithumb_displayed_depth_krw":           bithumbDepth, "competitor_displayed_depth_krw": competitorDepth,
			"bithumb_displayed_depth_share_pct": depthShare, "weighting_status": weightingStatus,
		})
	}
	return result
}

func indexExecutableBooks(rows []*orderbook.Row) map[executableBookKey]executableBookCandidate {
	result := make(map[executableBookKey]executableBookCandidate, len(rows))
	for _, row := range rows {
		if !usableBook(row) || !row.BuyExecution.Complete || !row.SellExecution.Complete || row.SpreadBPS == nil || row.BuyExecution.SlippageBPS == nil || row.SellExecution.SlippageBPS == nil {
			continue
		}
		score := *row.BuyExecution.SlippageBPS + *row.SellExecution.SlippageBPS + *row.SpreadBPS
		key := executableBookKey{exchange: row.Exchange, asset: row.Base}
		if best, found := result[key]; !found || score < best.score {
			result[key] = executableBookCandidate{row: row, score: score}
		}
	}
	return result
}

func comparableDepth(rows []comparableBook, bithumb bool) *float64 {
	total, found := 0.0, false
	for _, row := range rows {
		value := row.competitor.DepthKRW
		if bithumb {
			value = row.bithumb.DepthKRW
		}
		if value != nil {
			total, found = total+*value, true
		}
	}
	if !found {
		return nil
	}
	return pointer(total)
}

func weightedComparable(rows []comparableBook, value func(comparableBook) *float64) *float64 {
	type point struct{ value, weight float64 }
	points, total := make([]point, 0, len(rows)), 0.0
	for _, row := range rows {
		if item := value(row); item != nil && row.weight > 0 {
			points, total = append(points, point{*item, row.weight}), total+row.weight
		}
	}
	if len(points) == 0 {
		return nil
	}
	sort.Slice(points, func(i, j int) bool { return points[i].value < points[j].value })
	cumulative := 0.0
	for _, point := range points {
		cumulative += point.weight
		if cumulative >= total/2 {
			return pointer(point.value)
		}
	}
	return pointer(points[len(points)-1].value)
}

type executableGap struct {
	BuyExchange, BuyMarket, SellExchange, SellMarket string
	BuyVWAP, SellVWAP                                float64
	BuyAge, SellAge                                  float64
	GrossGapBPS, MatchedQuantity                     float64
	GrossBuyCost, GrossSellProceeds, GrossProfit     float64
	BuyFeeRate, SellFeeRate                          *float64
	BuyFeeSource, SellFeeSource                      string
	BuyFee, SellFee                                  *float64
	NetBuyCost, NetSellProceeds, NetProfit           *float64
	NetGapBPS                                        *float64
	FeesIncluded                                     bool
}

func LiquidityFeeKey(exchange domain.Exchange, market string) string {
	return string(exchange) + "\x00" + market
}

func bestExecutableGap(rows []*orderbook.Row, options LiquidityOptions) *executableGap {
	var best executableGap
	found, feeComplete := false, false
	for _, buy := range rows {
		if analysisCanceled(options.ctx) {
			return nil
		}
		if !buy.BuyExecution.Complete || buy.BuyExecution.AveragePriceKRW == nil || *buy.BuyExecution.AveragePriceKRW <= 0 {
			continue
		}
		for _, sell := range rows {
			if analysisCanceled(options.ctx) {
				return nil
			}
			if sell.Exchange == buy.Exchange || !sell.SellExecution.Complete || sell.SellExecution.AveragePriceKRW == nil || *sell.SellExecution.AveragePriceKRW <= 0 {
				continue
			}
			buyPrice, sellPrice := *buy.BuyExecution.AveragePriceKRW, *sell.SellExecution.AveragePriceKRW
			buyQuantity, sellQuantity := buy.BuyExecution.FilledBaseQuantity, sell.SellExecution.FilledBaseQuantity
			if buyQuantity == 0 {
				buyQuantity = options.NotionalKRW / buyPrice
			}
			if sellQuantity == 0 {
				sellQuantity = options.NotionalKRW / sellPrice
			}
			quantity := min(buyQuantity, sellQuantity)
			if quantity <= 0 {
				continue
			}
			gap := executableGap{BuyExchange: string(buy.Exchange), BuyMarket: buy.Market, SellExchange: string(sell.Exchange), SellMarket: sell.Market, BuyVWAP: buyPrice, SellVWAP: sellPrice, BuyAge: pointerValue(buy.AgeSeconds), SellAge: pointerValue(sell.AgeSeconds), GrossGapBPS: (sellPrice/buyPrice - 1) * 10_000, MatchedQuantity: quantity, GrossBuyCost: quantity * buyPrice, GrossSellProceeds: quantity * sellPrice}
			gap.GrossProfit = gap.GrossSellProceeds - gap.GrossBuyCost
			gap.BuyFeeRate, gap.BuyFeeSource = takerFee(options.TakerFees, buy)
			gap.SellFeeRate, gap.SellFeeSource = takerFee(options.TakerFees, sell)
			if gap.BuyFeeRate != nil {
				gap.BuyFee = pointer(gap.GrossBuyCost * *gap.BuyFeeRate)
				gap.NetBuyCost = pointer(gap.GrossBuyCost + *gap.BuyFee)
			}
			if gap.SellFeeRate != nil {
				gap.SellFee = pointer(gap.GrossSellProceeds * *gap.SellFeeRate)
				gap.NetSellProceeds = pointer(gap.GrossSellProceeds - *gap.SellFee)
			}
			gap.FeesIncluded = gap.NetBuyCost != nil && gap.NetSellProceeds != nil
			if gap.FeesIncluded {
				gap.NetProfit = pointer(*gap.NetSellProceeds - *gap.NetBuyCost)
				if *gap.NetBuyCost > 0 {
					gap.NetGapBPS = pointer(*gap.NetProfit / *gap.NetBuyCost * 10_000)
				}
			}
			if gap.FeesIncluded {
				if !feeComplete || !found || pointerValue(gap.NetGapBPS) > pointerValue(best.NetGapBPS) {
					best, found, feeComplete = gap, true, true
				}
			} else if !feeComplete && (!found || gap.GrossGapBPS > best.GrossGapBPS) {
				best, found = gap, true
			}
		}
	}
	if !found {
		return nil
	}
	return &best
}

func takerFee(fees map[string]TakerFee, row *orderbook.Row) (*float64, string) {
	fee, ok := fees[LiquidityFeeKey(row.Exchange, row.Market)]
	if !ok || math.IsNaN(fee.Rate) || math.IsInf(fee.Rate, 0) || fee.Rate < 0 || fee.Rate >= 1 {
		return nil, ""
	}
	return pointer(fee.Rate), fee.Source
}

func priceValidation(gap *executableGap, quarantine float64, identity AssetIdentity) PriceValidation {
	result := PriceValidation{Status: "NOT_EVALUATED", Reason: "NO_EXECUTABLE_CROSS_VENUE_COMPARISON", QuarantineThresholdBPS: max(0, quarantine)}
	if gap == nil {
		return result
	}
	observed := gap.NetGapBPS
	if observed == nil {
		observed = pointer(gap.GrossGapBPS)
	}
	result.Status, result.Reason, result.ObservedGapBPS = "PROVISIONAL", "CONTRACT_IDENTITY_UNVERIFIED", observed
	result.BuyExchange, result.SellExchange = &gap.BuyExchange, &gap.SellExchange
	result.MaxOrderBookAgeSec = pointer(max(gap.BuyAge, gap.SellAge))
	if identity.ContractIdentityVerified {
		result.Status, result.Reason = "RELEASED", "CONTRACT_IDENTITY_VERIFIED"
		return result
	}
	if math.Abs(*observed) >= result.QuarantineThresholdBPS {
		result.Status, result.Reason = "QUARANTINED", "EXTREME_GAP_AND_CONTRACT_IDENTITY_UNVERIFIED"
	}
	return result
}

func priceDislocation(gap *executableGap, notional float64, released bool) PriceDislocation {
	result := PriceDislocation{ReferenceNotionalKRW: notional, FeesIncluded: gap != nil && gap.FeesIncluded, ExecutableWithinObservedTenLevels: gap != nil, MatchedQuantityIsConservative: gap != nil, hasExecutableGap: gap != nil}
	if released {
		priceSignalReleased := true
		result.PriceSignalReleased = &priceSignalReleased
	}
	if gap == nil {
		return result
	}
	result.BuyExchange, result.BuyMarket = gap.BuyExchange, gap.BuyMarket
	result.SellExchange, result.SellMarket = gap.SellExchange, gap.SellMarket
	result.BuyVWAPKRW, result.SellVWAPKRW = gap.BuyVWAP, gap.SellVWAP
	result.BuyOrderBookAgeSec, result.SellOrderBookAgeSec = gap.BuyAge, gap.SellAge
	result.GrossGapBPS, result.MatchedBaseQuantity = gap.GrossGapBPS, gap.MatchedQuantity
	result.GrossBuyCostKRW, result.GrossSellProceedsKRW = gap.GrossBuyCost, gap.GrossSellProceeds
	result.GrossProfitKRW = gap.GrossProfit
	result.BuyTakerFeeRate, result.SellTakerFeeRate = gap.BuyFeeRate, gap.SellFeeRate
	result.BuyFeeSource, result.SellFeeSource = gap.BuyFeeSource, gap.SellFeeSource
	result.BuyFeeKRW, result.SellFeeKRW = gap.BuyFee, gap.SellFee
	result.NetBuyCostKRW, result.NetSellProceedsKRW = gap.NetBuyCost, gap.NetSellProceeds
	result.NetProfitKRW, result.NetGapBPS = gap.NetProfit, gap.NetGapBPS
	return result
}

func quarantinedAssetRows(rows []AssetMicrostructure) []any {
	result := make([]any, 0)
	for _, row := range rows {
		if row.PriceValidation.Status != "QUARANTINED" {
			continue
		}
		result = append(result, map[string]any{"asset": row.Asset, "status": row.PriceValidation.Status, "reason": row.PriceValidation.Reason, "observed_gap_bps": row.PriceValidation.ObservedGapBPS, "quarantine_threshold_bps": row.PriceValidation.QuarantineThresholdBPS, "buy_exchange": row.PriceValidation.BuyExchange, "sell_exchange": row.PriceValidation.SellExchange, "max_orderbook_age_sec": row.PriceValidation.MaxOrderBookAgeSec})
	}
	return result
}

func verifiedContractIdentity(asset string, gap *executableGap, proofs map[string]ContractIdentityProof) AssetIdentity {
	identity := Identity(asset)
	if gap == nil {
		return identity
	}
	proof, ok := proofs[strings.ToUpper(strings.TrimSpace(asset))]
	if !ok || proof.Validate(asset) != nil {
		return identity
	}
	canonical := strings.TrimSpace(proof.CanonicalAssetID)
	buyKey := ContractMarketKey(domain.Exchange(gap.BuyExchange), gap.BuyMarket)
	sellKey := ContractMarketKey(domain.Exchange(gap.SellExchange), gap.SellMarket)
	if strings.TrimSpace(proof.Markets[buyKey]) != canonical || strings.TrimSpace(proof.Markets[sellKey]) != canonical {
		return identity
	}
	identity.CanonicalAssetID = canonical
	identity.ContractIdentityStatus = "VERIFIED"
	identity.ContractIdentityVerified = true
	identity.Source = strings.TrimSpace(proof.Source)
	return identity
}

func verifiedGlobalReferenceIdentity(
	asset string,
	bithumb, global []*orderbook.Row,
	proofs map[string]ContractIdentityProof,
) bool {
	if len(bithumb) == 0 || len(global) == 0 {
		return false
	}
	proof, ok := proofs[strings.ToUpper(strings.TrimSpace(asset))]
	if !ok || proof.Validate(asset) != nil {
		return false
	}
	canonical := strings.TrimSpace(proof.CanonicalAssetID)
	for _, rows := range [][]*orderbook.Row{bithumb, global} {
		for _, row := range rows {
			key := ContractMarketKey(row.Exchange, row.Market)
			if strings.TrimSpace(proof.Markets[key]) != canonical {
				return false
			}
		}
	}
	return true
}

func ContractMarketKey(exchange domain.Exchange, market string) string {
	return strings.ToLower(strings.TrimSpace(string(exchange))) + ":" + strings.TrimSpace(market)
}

func contractMarketKeyParts(key string) (domain.Exchange, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(key), ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	exchange, err := domain.ParseExchange(parts[0])
	if err != nil {
		return "", "", false
	}
	return exchange, strings.TrimSpace(parts[1]), true
}

func applyIdentityPolicy(result *LiquidityAnalysis) {
	evaluated, verified := 0, 0
	sources := map[string]struct{}{}
	for _, asset := range result.Assets {
		if asset.PriceValidation.Status == "NOT_EVALUATED" {
			continue
		}
		evaluated++
		if asset.AssetIdentity.ContractIdentityVerified {
			verified++
			sources[asset.AssetIdentity.Source] = struct{}{}
		}
	}
	if verified == 0 {
		return
	}
	result.PriceAnomalyPolicy["released_price_signals"] = true
	result.ComparablePolicy["contract_identity_verified"] = verified == evaluated
	result.ComparablePolicy["identity_status"] = "PARTIAL"
	if verified == evaluated {
		result.ComparablePolicy["identity_status"] = "VERIFIED"
	}
	result.ComparablePolicy["identity_source"] = "CONFIGURED_EXACT_MARKET_PROOF"
	result.ComparablePolicy["verified_asset_count"] = verified
	result.ComparablePolicy["evaluated_asset_count"] = evaluated
	result.ComparablePolicy["identity_evidence_sources"] = sortedSetKeys(sources)
}

func sortedSetKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func globalExchangeMids(rows []*orderbook.Row) map[string]float64 {
	byExchange := make(map[string][]float64)
	for _, row := range rows {
		if row.MidPriceKRW != nil {
			byExchange[string(row.Exchange)] = append(byExchange[string(row.Exchange)], *row.MidPriceKRW)
		}
	}
	result := make(map[string]float64, len(byExchange))
	for exchange, values := range byExchange {
		if median := medianPointer(values); median != nil {
			result[exchange] = *median
		}
	}
	return result
}

func mapFloatValues(values map[string]float64) []float64 {
	result := make([]float64, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func sortedMapKeys(values map[string]float64) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func referenceDispersion(values map[string]float64, reference *float64) *float64 {
	rows := mapFloatValues(values)
	if reference == nil || *reference == 0 || len(rows) < 2 {
		return nil
	}
	sort.Float64s(rows)
	return pointer((rows[len(rows)-1] - rows[0]) / *reference * 10_000)
}

func summarizeBookExchanges(rows []*orderbook.Row, totalDepth *float64, weights map[string]float64, scenarioNotionals []float64) []LiquidityExchange {
	by := make(map[domain.Exchange][]*orderbook.Row)
	for _, row := range rows {
		by[row.Exchange] = append(by[row.Exchange], row)
	}
	names := make([]domain.Exchange, 0, len(by))
	for name := range by {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	result := make([]LiquidityExchange, 0, len(names))
	for _, name := range names {
		venue := by[name]
		live := usableBooks(venue)
		registeredAssets := assetSet(venue)
		liveAssets := assetSet(live)
		fully, partial := bookAssetAvailability(venue, len(registeredAssets), len(liveAssets))
		registeredWeight, liveWeight := 0.0, 0.0
		for asset := range registeredAssets {
			registeredWeight += max(0, weights[asset])
		}
		for asset := range liveAssets {
			liveWeight += max(0, weights[asset])
		}
		depth := sumBookPointers(live, func(row *orderbook.Row) *float64 { return row.DepthKRW })
		spreads := bookValues(live, func(row *orderbook.Row) *float64 { return row.SpreadBPS })
		imbalances := bookValues(live, func(row *orderbook.Row) *float64 { return row.Imbalance })
		updates := bookValues(live, func(row *orderbook.Row) *float64 { return pointer(row.UpdateRateHz) })
		latencies := bookValues(live, func(row *orderbook.Row) *float64 {
			if row.EventLatencyMS == nil {
				return nil
			}
			return pointer(float64(*row.EventLatencyMS))
		})
		buys, sells := completeExecutions(live, true), completeExecutions(live, false)
		assets := summarizeAssetExecutions(venue, scenarioNotionals)
		summary := LiquidityExchange{Exchange: name, RegisteredMarketCount: len(venue), LiveMarketCount: len(live), LiveMarketPct: percent(float64(len(live)), float64(len(venue))), RegisteredAssetCount: len(registeredAssets), LiveAssetCount: len(liveAssets), LiveAssetPct: percent(float64(len(liveAssets)), float64(len(registeredAssets))), FullyLiveAssetCount: fully, PartialLiveAssetCount: partial, UnavailableAssetCount: len(registeredAssets) - len(liveAssets), RegisteredAssetSelectedVolumeKRW: registeredWeight, LiveAssetSelectedVolumeKRW: liveWeight, LiveAssetSelectedVolumeCoveragePct: percent(liveWeight, registeredWeight), BuyCompleteAssetPct: percent(float64(assets.buyComplete), float64(len(liveAssets))), SellCompleteAssetPct: percent(float64(assets.sellComplete), float64(len(liveAssets))), MedianAssetSpreadBPS: medianPointer(assets.spreads), AverageBuyAssetSlippageBPS: meanPointer(assets.buys), AverageSellAssetSlippageBPS: meanPointer(assets.sells), AverageBuyAssetSlippageScenarios: assets.buyScenarios, AverageSellAssetSlippageScenarios: assets.sellScenarios, DepthKRW: depth, BidDepthKRW: sumBookPointers(live, func(row *orderbook.Row) *float64 { return row.BidDepthKRW }), AskDepthKRW: sumBookPointers(live, func(row *orderbook.Row) *float64 { return row.AskDepthKRW }), MedianSpreadBPS: medianPointer(spreads), AverageSpreadBPS: meanPointer(spreads), AverageImbalance: meanPointer(imbalances), AverageUpdateRateHz: meanPointer(updates), AverageEventLatencyMS: meanPointer(latencies), BuyCompleteMarketPct: percent(float64(len(buys)), float64(len(live))), SellCompleteMarketPct: percent(float64(len(sells)), float64(len(live))), AverageBuySlippageBPS: meanPointer(buys), AverageSellSlippageBPS: meanPointer(sells), CumulativeDepthKRW: map[string]*float64{}}
		if depth != nil && totalDepth != nil {
			summary.LiquiditySharePct = percent(*depth, *totalDepth)
		}
		for _, level := range []string{"1", "3", "5", "10"} {
			summary.CumulativeDepthKRW[level] = sumBookPointers(live, func(row *orderbook.Row) *float64 {
				value := row.CumulativeDepthKRW[level]
				if value == nil && level == "10" {
					return row.DepthKRW
				}
				return value
			})
		}
		for _, row := range live {
			if row.BidOrderCount != nil {
				summary.KnownBidOrderCount += *row.BidOrderCount
			}
			if row.AskOrderCount != nil {
				summary.KnownAskOrderCount += *row.AskOrderCount
			}
			summary.SequenceGapCount += row.SequenceGapCount
		}
		result = append(result, summary)
	}
	return result
}

func buildSlippageListingGroups(rows []*orderbook.Row, scenarioNotionals []float64) []SlippageListingGroup {
	listedOn := make(map[string]map[domain.Exchange]struct{})
	for _, row := range rows {
		if !strings.EqualFold(row.Quote, "KRW") {
			continue
		}
		if listedOn[row.Base] == nil {
			listedOn[row.Base] = make(map[domain.Exchange]struct{})
		}
		listedOn[row.Base][row.Exchange] = struct{}{}
	}
	type groupSpec struct {
		key, label string
		matches    func(map[domain.Exchange]struct{}) bool
	}
	specs := []groupSpec{
		{
			key: "BITHUMB_EXCLUSIVE", label: "빗썸 단독상장",
			matches: func(exchanges map[domain.Exchange]struct{}) bool {
				_, bithumb := exchanges[domain.ExchangeBithumb]
				return bithumb && len(exchanges) == 1
			},
		},
		{
			key: "BITHUMB_UPBIT_COMMON", label: "공통상장",
			matches: func(exchanges map[domain.Exchange]struct{}) bool {
				_, bithumb := exchanges[domain.ExchangeBithumb]
				_, upbit := exchanges[domain.ExchangeUpbit]
				return bithumb && upbit
			},
		},
	}
	groups := make([]SlippageListingGroup, 0, len(specs))
	for _, spec := range specs {
		assets := make(map[string]struct{})
		for asset, exchanges := range listedOn {
			if spec.matches(exchanges) {
				assets[asset] = struct{}{}
			}
		}
		groupRows := make([]*orderbook.Row, 0, len(rows))
		for _, row := range rows {
			if _, ok := assets[row.Base]; !ok || !strings.EqualFold(row.Quote, "KRW") {
				continue
			}
			if row.Exchange == domain.ExchangeBithumb || (spec.key == "BITHUMB_UPBIT_COMMON" && row.Exchange == domain.ExchangeUpbit) {
				groupRows = append(groupRows, row)
			}
		}
		exchanges := summarizeListingSlippage(groupRows, scenarioNotionals)
		groups = append(groups, SlippageListingGroup{
			Key: spec.key, Label: spec.label, RegisteredAssetCount: len(assets), Exchanges: exchanges,
		})
	}
	return groups
}

// An asset is fully live unless at least one of its registered markets is
// unavailable. Reuse the existing set sizes and only track unavailable assets;
// the common all-live case needs no additional map allocation.
func bookAssetAvailability(rows []*orderbook.Row, registeredCount, liveCount int) (fully, partial int) {
	unavailable := make(map[string]struct{})
	for _, row := range rows {
		if !usableBook(row) {
			unavailable[row.Base] = struct{}{}
		}
	}
	fully = registeredCount - len(unavailable)
	return fully, liveCount - fully
}

// Listing groups need asset counts and scenario averages, not the full venue
// summary's depth, latency, spread and order-count reductions.
func summarizeListingSlippage(rows []*orderbook.Row, notionals []float64) []ListingSlippageExchange {
	byExchange := make(map[domain.Exchange][]*orderbook.Row)
	for _, row := range rows {
		byExchange[row.Exchange] = append(byExchange[row.Exchange], row)
	}
	names := make([]domain.Exchange, 0, len(byExchange))
	for name := range byExchange {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	result := make([]ListingSlippageExchange, 0, len(names))
	for _, name := range names {
		venue := byExchange[name]
		assets := summarizeAssetExecutions(venue, notionals)
		result = append(result, ListingSlippageExchange{
			Exchange: name, RegisteredAssetCount: len(assetSet(venue)), LiveAssetCount: len(assetSet(usableBooks(venue))),
			AverageBuyAssetSlippageScenarios: assets.buyScenarios, AverageSellAssetSlippageScenarios: assets.sellScenarios,
		})
	}
	return result
}

type assetExecutionSummary struct {
	buyComplete, sellComplete int
	spreads, buys, sells      []float64
	buyScenarios              []SlippageScenario
	sellScenarios             []SlippageScenario
}

func summarizeAssetExecutions(rows []*orderbook.Row, scenarioNotionals []float64) assetExecutionSummary {
	byAsset := make(map[string][]*orderbook.Row)
	for _, row := range rows {
		if usableBook(row) {
			byAsset[row.Base] = append(byAsset[row.Base], row)
		}
	}
	orderedNotionals := append([]float64(nil), scenarioNotionals...)
	sort.Float64s(orderedNotionals)
	uniqueNotionals := orderedNotionals[:0]
	for _, notional := range orderedNotionals {
		if !(notional > 0) || (len(uniqueNotionals) > 0 && uniqueNotionals[len(uniqueNotionals)-1] == notional) {
			continue
		}
		uniqueNotionals = append(uniqueNotionals, notional)
	}
	type scenarioTotals struct {
		notional        float64
		buySum, sellSum float64
		buyCount        int
		sellCount       int
	}
	scenarios := make([]scenarioTotals, len(uniqueNotionals))
	for index, notional := range uniqueNotionals {
		scenarios[index].notional = notional
	}
	assetCount := len(byAsset)
	result := assetExecutionSummary{
		spreads:       make([]float64, 0, assetCount),
		buys:          make([]float64, 0, assetCount),
		sells:         make([]float64, 0, assetCount),
		buyScenarios:  make([]SlippageScenario, 0, len(scenarios)),
		sellScenarios: make([]SlippageScenario, 0, len(scenarios)),
	}
	liveAssetCount := 0
	for _, assetRows := range byAsset {
		live := assetRows
		liveAssetCount++
		if spread, ok := minBookValue(live, func(row *orderbook.Row) *float64 { return row.SpreadBPS }); ok {
			result.spreads = append(result.spreads, spread)
		}
		if value, hasValue, complete := bestAssetExecution(live, true); complete {
			result.buyComplete++
			if hasValue {
				result.buys = append(result.buys, value)
			}
		}
		if value, hasValue, complete := bestAssetExecution(live, false); complete {
			result.sellComplete++
			if hasValue {
				result.sells = append(result.sells, value)
			}
		}
		for index := range scenarios {
			totals := &scenarios[index]
			if value, ok := bestScenarioExecution(live, true, totals.notional); ok {
				totals.buySum += value
				totals.buyCount++
			}
			if value, ok := bestScenarioExecution(live, false, totals.notional); ok {
				totals.sellSum += value
				totals.sellCount++
			}
		}
	}
	for _, totals := range scenarios {
		var buyAverage, sellAverage *float64
		if totals.buyCount > 0 {
			buyAverage = pointer(totals.buySum / float64(totals.buyCount))
		}
		if totals.sellCount > 0 {
			sellAverage = pointer(totals.sellSum / float64(totals.sellCount))
		}
		result.buyScenarios = append(result.buyScenarios, SlippageScenario{totals.notional, buyAverage, totals.buyCount, percent(float64(totals.buyCount), float64(liveAssetCount))})
		result.sellScenarios = append(result.sellScenarios, SlippageScenario{totals.notional, sellAverage, totals.sellCount, percent(float64(totals.sellCount), float64(liveAssetCount))})
	}
	return result
}

func bestAssetExecution(rows []*orderbook.Row, buy bool) (value float64, hasValue, complete bool) {
	for _, row := range rows {
		execution := row.SellExecution
		if buy {
			execution = row.BuyExecution
		}
		if !execution.Complete {
			continue
		}
		complete = true
		if execution.SlippageBPS != nil && (!hasValue || *execution.SlippageBPS < value) {
			value = *execution.SlippageBPS
			hasValue = true
		}
	}
	return value, hasValue, complete
}

func bestScenarioExecution(rows []*orderbook.Row, buy bool, notional float64) (value float64, found bool) {
	for _, row := range rows {
		scenarios := row.SellExecutionScenarios
		if buy {
			scenarios = row.BuyExecutionScenarios
		}
		for _, scenario := range scenarios {
			if scenario.NotionalKRW == notional && scenario.Execution.Complete && scenario.Execution.SlippageBPS != nil {
				if !found || *scenario.Execution.SlippageBPS < value {
					value = *scenario.Execution.SlippageBPS
					found = true
				}
				break
			}
		}
	}
	return value, found
}

type assetBookRows struct {
	local []*orderbook.Row
	world []*orderbook.Row
}

func buildAssetExchangeSlippage(rows []*orderbook.Row, weights map[string]float64) []AssetExchangeSlippage {
	selected := make(map[executableBookKey]*orderbook.Row)
	for _, row := range rows {
		if !strings.EqualFold(row.Quote, "KRW") ||
			(row.Exchange != domain.ExchangeBithumb && row.Exchange != domain.ExchangeUpbit) {
			continue
		}
		key := executableBookKey{exchange: row.Exchange, asset: row.Base}
		current := selected[key]
		if current == nil || preferAssetExchangeRow(row, current) {
			selected[key] = row
		}
	}
	result := make([]AssetExchangeSlippage, 0, len(selected))
	for key, row := range selected {
		result = append(result, AssetExchangeSlippage{
			Asset:             key.asset,
			Exchange:          key.exchange,
			Market:            row.Market,
			SelectedVolumeKRW: max(0, weights[key.asset]),
			Status:            row.Status,
			AgeSeconds:        copyPointer(row.AgeSeconds),
			DepthKRW:          copyPointer(row.DepthKRW),
			BuyComplete:       row.BuyExecution.Complete,
			SellComplete:      row.SellExecution.Complete,
			BuyFillPct:        row.BuyExecution.FillPercent,
			SellFillPct:       row.SellExecution.FillPercent,
			BuySlippageBPS:    copyPointer(row.BuyExecution.SlippageBPS),
			SellSlippageBPS:   copyPointer(row.SellExecution.SlippageBPS),
			BestBidKRW:        copyPointer(row.BestBidKRW),
			BestAskKRW:        copyPointer(row.BestAskKRW),
			BuyVWAPKRW:        copyPointer(row.BuyExecution.AveragePriceKRW),
			SellVWAPKRW:       copyPointer(row.SellExecution.AveragePriceKRW),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SelectedVolumeKRW != result[j].SelectedVolumeKRW {
			return result[i].SelectedVolumeKRW > result[j].SelectedVolumeKRW
		}
		if result[i].Asset != result[j].Asset {
			return result[i].Asset < result[j].Asset
		}
		return result[i].Exchange < result[j].Exchange
	})
	return result
}

func preferAssetExchangeRow(candidate, current *orderbook.Row) bool {
	candidateLive := candidate.Status == "LIVE"
	currentLive := current.Status == "LIVE"
	if candidateLive != currentLive {
		return candidateLive
	}
	if pointerValue(candidate.DepthKRW) != pointerValue(current.DepthKRW) {
		return pointerValue(candidate.DepthKRW) > pointerValue(current.DepthKRW)
	}
	return candidate.Market < current.Market
}

func buildAssetMicrostructures(rows []*orderbook.Row, options LiquidityOptions) []AssetMicrostructure {
	byAsset := make(map[string]*assetBookRows)
	for _, row := range rows {
		group := byAsset[row.Base]
		if group == nil {
			group = &assetBookRows{}
			byAsset[row.Base] = group
		}
		if !usableBook(row) {
			continue
		}
		if row.Scope == domain.ScopeDomestic {
			group.local = append(group.local, row)
		} else {
			group.world = append(group.world, row)
		}
	}
	result := make([]AssetMicrostructure, 0, len(byAsset))
	for asset, group := range byAsset {
		if analysisCanceled(options.ctx) {
			return nil
		}
		local, world := group.local, group.world
		bithumb := make([]*orderbook.Row, 0, 1)
		bithumbKRW := make([]*orderbook.Row, 0, 1)
		competitors := make([]*orderbook.Row, 0, len(local))
		for _, row := range local {
			if row.Exchange == domain.ExchangeBithumb {
				bithumb = append(bithumb, row)
				if strings.EqualFold(row.Quote, "KRW") {
					bithumbKRW = append(bithumbKRW, row)
				}
			} else {
				competitors = append(competitors, row)
			}
		}
		domesticDepth := sumBookPointers(local, func(row *orderbook.Row) *float64 { return row.DepthKRW })
		bithumbDepth := sumBookPointers(bithumb, func(row *orderbook.Row) *float64 { return row.DepthKRW })
		competitorDepth := sumBookPointers(competitors, func(row *orderbook.Row) *float64 { return row.DepthKRW })
		bithumbSpread := medianPointer(bookValues(bithumb, func(row *orderbook.Row) *float64 { return row.SpreadBPS }))
		competitorSpread := minPointer(bookValues(competitors, func(row *orderbook.Row) *float64 { return row.SpreadBPS }))
		bithumbBuy := minExecution(bithumb, true)
		bithumbSell := minExecution(bithumb, false)
		competitorBuy := minExecution(competitors, true)
		status := "COMPETITIVE"
		if len(bithumb) == 0 {
			status = "UNLISTED_OR_BOOK_MISSING"
		} else if domesticDepth != nil && bithumbDepth != nil && *domesticDepth > 0 && *bithumbDepth / *domesticDepth * 100 < 20 {
			status = "THIN_DEPTH"
		} else if bithumbSpread != nil && competitorSpread != nil && *bithumbSpread > *competitorSpread*1.5 {
			status = "WIDE_SPREAD"
		} else if bithumbBuy == nil && competitorBuy != nil {
			status = "INSUFFICIENT_TEN_LEVEL_DEPTH"
		} else if bithumbBuy != nil && competitorBuy != nil && *bithumbBuy > *competitorBuy+5 {
			status = "HIGH_SLIPPAGE"
		}
		globalMids := globalExchangeMids(world)
		globalMid := medianPointer(mapFloatValues(globalMids))
		bithumbMid := medianPointer(bookValues(bithumb, func(row *orderbook.Row) *float64 { return row.MidPriceKRW }))
		gap := bestExecutableGap(local, options)
		identity := verifiedContractIdentity(asset, gap, options.ContractIdentities)
		validation := priceValidation(gap, options.QuarantineBPS, identity)
		if validation.Status == "QUARANTINED" {
			status = "QUARANTINED"
		}
		item := AssetMicrostructure{Asset: asset, AssetIdentity: identity, Status: status, LiveMarketCount: len(local), BithumbLiveMarketCount: len(bithumb), BithumbOrderBookMaxAgeSec: maxBookAge(bithumb), CompetitorOrderBookMaxAgeSec: maxBookAge(competitors), DomesticDepthKRW: domesticDepth, BithumbDepthKRW: bithumbDepth, CompetitorDepthKRW: competitorDepth, BithumbMedianSpreadBPS: bithumbSpread, BestCompetitorSpreadBPS: competitorSpread, SpreadGapBPS: difference(bithumbSpread, competitorSpread), BithumbBuySlippageBPS: bithumbBuy, BithumbSellSlippageBPS: bithumbSell, BithumbBuyFillPct: medianPointer(bookValues(bithumb, func(row *orderbook.Row) *float64 { return pointer(row.BuyExecution.FillPercent) })), BithumbSellFillPct: medianPointer(bookValues(bithumb, func(row *orderbook.Row) *float64 { return pointer(row.SellExecution.FillPercent) })), BestCompetitorBuySlippageBPS: competitorBuy, BuySlippageGapBPS: difference(bithumbBuy, competitorBuy), BestBithumbAskKRW: minPointer(bookValues(bithumb, func(row *orderbook.Row) *float64 { return row.BestAskKRW })), BestCompetitorAskKRW: minPointer(bookValues(competitors, func(row *orderbook.Row) *float64 { return row.BestAskKRW })), GlobalLiveMarketCount: len(world), GlobalDepthKRW: sumBookPointers(world, func(row *orderbook.Row) *float64 { return row.DepthKRW }), GlobalReferenceMidKRW: globalMid, GlobalReferenceExchangeCount: len(globalMids), GlobalReferenceExchanges: sortedMapKeys(globalMids), GlobalReferenceDispersionBPS: referenceDispersion(globalMids, globalMid), GlobalBestBidKRW: maxPointer(bookValues(world, func(row *orderbook.Row) *float64 { return row.BestBidKRW })), GlobalBestAskKRW: minPointer(bookValues(world, func(row *orderbook.Row) *float64 { return row.BestAskKRW })), BithumbMidKRW: bithumbMid, GlobalReferenceMethod: "median of Binance Spot market mids", PriceValidation: validation, PriceDislocation: priceDislocation(gap, options.NotionalKRW, identity.ContractIdentityVerified)}
		item.BithumbBestBidKRW = maxPointer(bookValues(bithumbKRW, func(row *orderbook.Row) *float64 { return row.BestBidKRW }))
		item.BithumbBestAskKRW = minPointer(bookValues(bithumbKRW, func(row *orderbook.Row) *float64 { return row.BestAskKRW }))
		for _, row := range bithumbKRW {
			item.BithumbKRWMarket = row.Market
			if execution := row.BuyExecution; execution.Complete && execution.AveragePriceKRW != nil {
				item.BithumbBuyVWAPKRW = copyPointer(execution.AveragePriceKRW)
				item.BithumbBuyBaseQuantity = pointer(execution.FilledBaseQuantity)
			}
			if execution := row.SellExecution; execution.Complete && execution.AveragePriceKRW != nil {
				item.BithumbSellVWAPKRW = copyPointer(execution.AveragePriceKRW)
				item.BithumbSellBaseQuantity = pointer(execution.FilledBaseQuantity)
			}
		}
		if bithumbDepth != nil && domesticDepth != nil {
			item.BithumbLiquidityCapturePct = percent(*bithumbDepth, *domesticDepth)
		}
		if item.BestBithumbAskKRW != nil && item.BestCompetitorAskKRW != nil && *item.BestCompetitorAskKRW > 0 {
			item.BithumbBuyPriceGapBPS = pointer((*item.BestBithumbAskKRW / *item.BestCompetitorAskKRW - 1) * 10000)
		}
		if options.GlobalCoverageComplete &&
			verifiedGlobalReferenceIdentity(asset, bithumb, world, options.ContractIdentities) &&
			bithumbMid != nil && globalMid != nil && *globalMid > 0 {
			item.BithumbGlobalPremiumBPS = pointer((*bithumbMid / *globalMid - 1) * 10000)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return pointerValue(result[i].CompetitorDepthKRW) > pointerValue(result[j].CompetitorDepthKRW)
	})
	return result
}

func usableBook(row *orderbook.Row) bool { return row.Status == "LIVE" }
func usableBooks(rows []*orderbook.Row) []*orderbook.Row {
	r := make([]*orderbook.Row, 0, len(rows))
	for _, row := range rows {
		if usableBook(row) {
			r = append(r, row)
		}
	}
	return r
}
func assetSet(rows []*orderbook.Row) map[string]struct{} {
	r := map[string]struct{}{}
	for _, row := range rows {
		r[row.Base] = struct{}{}
	}
	return r
}
func assetSetByExchange(rows []*orderbook.Row) map[domain.Exchange]struct{} {
	r := map[domain.Exchange]struct{}{}
	for _, row := range rows {
		r[row.Exchange] = struct{}{}
	}
	return r
}
func sumBookPointers(rows []*orderbook.Row, get func(*orderbook.Row) *float64) *float64 {
	sum := 0.0
	found := false
	for _, row := range rows {
		if value := get(row); value != nil {
			sum += *value
			found = true
		}
	}
	if !found {
		return nil
	}
	return &sum
}
func sumBookPointersWhere(rows []*orderbook.Row, match func(*orderbook.Row) bool, get func(*orderbook.Row) *float64) *float64 {
	sum := 0.0
	found := false
	for _, row := range rows {
		if match(row) {
			if value := get(row); value != nil {
				sum += *value
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return &sum
}
func bookValues(rows []*orderbook.Row, get func(*orderbook.Row) *float64) []float64 {
	r := []float64{}
	for _, row := range rows {
		if value := get(row); value != nil {
			r = append(r, *value)
		}
	}
	return r
}
func minBookValue(rows []*orderbook.Row, get func(*orderbook.Row) *float64) (float64, bool) {
	minimum := 0.0
	found := false
	for _, row := range rows {
		if value := get(row); value != nil && (!found || *value < minimum) {
			minimum = *value
			found = true
		}
	}
	return minimum, found
}
func meanPointer(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	return pointer(sumFloat(v) / float64(len(v)))
}
func medianPointer(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	return pointer(medianFloat(v))
}
func minPointer(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	return pointer(slicesMin(v))
}
func maxPointer(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	return pointer(slicesMax(v))
}
func slicesMin(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		m = min(m, x)
	}
	return m
}
func slicesMax(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		m = max(m, x)
	}
	return m
}
func completeExecutions(rows []*orderbook.Row, buy bool) []float64 {
	r := []float64{}
	for _, row := range rows {
		execution := row.SellExecution
		if buy {
			execution = row.BuyExecution
		}
		if execution.Complete && execution.SlippageBPS != nil {
			r = append(r, *execution.SlippageBPS)
		}
	}
	return r
}
func minExecution(rows []*orderbook.Row, buy bool) *float64 {
	return minPointer(completeExecutions(rows, buy))
}
func maxBookAge(rows []*orderbook.Row) *float64 {
	if len(rows) == 0 {
		return nil
	}
	oldest := 0.0
	for _, row := range rows {
		if row.AgeSeconds != nil && *row.AgeSeconds > oldest {
			oldest = *row.AgeSeconds
		}
	}
	return pointer(oldest)
}
func exchangeNames(rows []orderbook.Row) []string {
	seen := map[string]struct{}{}
	for _, row := range rows {
		seen[string(row.Exchange)] = struct{}{}
	}
	r := make([]string, 0, len(seen))
	for x := range seen {
		r = append(r, x)
	}
	sort.Strings(r)
	return r
}
