package orderbook

import (
	"math"
	"sort"
	"strconv"
	"time"

	"market-share-go/internal/domain"
)

var depthCheckpoints = [...]int{1, 3, 5, AnalysisDepth}

type RateResolver func(quote string) (float64, bool)

type ExecutionScenario struct {
	NotionalKRW float64   `json:"notional_krw"`
	Execution   Execution `json:"execution"`
}

type Row struct {
	Scope                  domain.Scope        `json:"scope"`
	Exchange               domain.Exchange     `json:"exchange"`
	Market                 string              `json:"market"`
	Base                   string              `json:"base"`
	Quote                  string              `json:"quote"`
	Status                 string              `json:"status"`
	AnalysisDepth          int                 `json:"analysis_depth"`
	SourceDepth            *int                `json:"source_depth"`
	BidLevelCount          int                 `json:"bid_level_count"`
	AskLevelCount          int                 `json:"ask_level_count"`
	AgeSeconds             *float64            `json:"age_sec"`
	EventLatencyMS         *int64              `json:"event_latency_ms"`
	BestBid                *float64            `json:"best_bid"`
	BestAsk                *float64            `json:"best_ask"`
	BestBidKRW             *float64            `json:"best_bid_krw"`
	BestAskKRW             *float64            `json:"best_ask_krw"`
	BestBidQuantity        *float64            `json:"best_bid_quantity"`
	BestAskQuantity        *float64            `json:"best_ask_quantity"`
	BestBidNotionalKRW     *float64            `json:"best_bid_notional_krw"`
	BestAskNotionalKRW     *float64            `json:"best_ask_notional_krw"`
	MidPrice               *float64            `json:"mid_price"`
	MidPriceKRW            *float64            `json:"mid_price_krw"`
	Microprice             *float64            `json:"microprice"`
	MicropriceKRW          *float64            `json:"microprice_krw"`
	SpreadBPS              *float64            `json:"spread_bps"`
	BidDepthKRW            *float64            `json:"bid_depth_krw"`
	AskDepthKRW            *float64            `json:"ask_depth_krw"`
	DepthKRW               *float64            `json:"depth_krw"`
	CumulativeDepthKRW     map[string]*float64 `json:"cumulative_depth_krw"`
	Imbalance              *float64            `json:"imbalance"`
	BidOrderCount          *int64              `json:"bid_order_count"`
	AskOrderCount          *int64              `json:"ask_order_count"`
	UpdateRateHz           float64             `json:"update_rate_hz"`
	BestChangeRateHz       float64             `json:"best_change_rate_hz"`
	ValidUpdatePercent     float64             `json:"valid_update_pct"`
	SequenceGapCount       int64               `json:"sequence_gap_count"`
	BuyQuoteKRWRate        *float64            `json:"buy_quote_krw_rate,omitempty"`
	SellQuoteKRWRate       *float64            `json:"sell_quote_krw_rate,omitempty"`
	ExecutionFXComplete    bool                `json:"execution_fx_complete"`
	ExecutionBlockedReason string              `json:"execution_blocked_reason"`
	BuyExecution           Execution           `json:"buy_execution"`
	SellExecution          Execution           `json:"sell_execution"`
	BuyExecutionScenarios  []ExecutionScenario `json:"buy_execution_scenarios"`
	SellExecutionScenarios []ExecutionScenario `json:"sell_execution_scenarios"`
}

type LevelView struct {
	Level                 int      `json:"level"`
	Price                 float64  `json:"price"`
	Quantity              float64  `json:"quantity"`
	OrderCount            *int64   `json:"order_count"`
	NotionalNativeQuote   float64  `json:"notional_native_quote"`
	NotionalKRW           *float64 `json:"notional_krw"`
	CumulativeBase        float64  `json:"cumulative_base_quantity"`
	CumulativeNativeQuote float64  `json:"cumulative_native_quote"`
	CumulativeKRW         *float64 `json:"cumulative_krw"`
}

type Detail struct {
	Row
	Bids              []LevelView `json:"bids"`
	Asks              []LevelView `json:"asks"`
	ObservedDepthOnly bool        `json:"observed_depth_only"`
	ExtrapolationUsed bool        `json:"extrapolation_used"`
}

type ViewOptions struct {
	MidRate              RateResolver
	BuyRate              RateResolver
	SellRate             RateResolver
	NotionalKRW          float64
	SlippageNotionalsKRW []float64
	Now                  time.Time
	QuietMarkets         map[string]bool
}

func (s *Store) Rows(markets []domain.MarketPair, options ViewOptions) []Row {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	scenarios := uniquePositive(options.SlippageNotionalsKRW)
	rows := make([]Row, 0, len(markets))
	for _, pair := range markets {
		pairScenarios := scenarios
		if pair.Scope != domain.ScopeDomestic {
			pairScenarios = nil
		}
		rows = append(rows, s.row(pair, rowOptions{
			midRate:     options.MidRate,
			buyRate:     fallbackResolver(options.BuyRate, options.MidRate),
			sellRate:    fallbackResolver(options.SellRate, options.MidRate),
			notionalKRW: options.NotionalKRW,
			scenarios:   pairScenarios,
			now:         now,
			quiet:       options.QuietMarkets[pair.Key()],
		}))
	}
	return rows
}

func (s *Store) Detail(pair domain.MarketPair, options ViewOptions) Detail {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	row := s.row(pair, rowOptions{
		midRate:     options.MidRate,
		buyRate:     fallbackResolver(options.BuyRate, options.MidRate),
		sellRate:    fallbackResolver(options.SellRate, options.MidRate),
		notionalKRW: options.NotionalKRW,
		now:         now,
	})
	state := s.books[eventKey(pair.Exchange, pair.Symbol)]
	if state == nil || !state.initialized {
		return Detail{Row: row, ObservedDepthOnly: true}
	}
	rate, _ := resolve(options.MidRate, pair.Quote)
	return Detail{
		Row:               row,
		Bids:              levelViews(observed(state.sortedBids), rate),
		Asks:              levelViews(observed(state.sortedAsks), rate),
		ObservedDepthOnly: true,
	}
}

type rowOptions struct {
	midRate     RateResolver
	buyRate     RateResolver
	sellRate    RateResolver
	notionalKRW float64
	scenarios   []float64
	now         time.Time
	quiet       bool
}

func (s *Store) row(pair domain.MarketPair, options rowOptions) Row {
	row := emptyRow(pair, options.notionalKRW, options.scenarios)
	state := s.books[eventKey(pair.Exchange, pair.Symbol)]
	if state == nil || !state.initialized {
		if state != nil && state.sequenceGapCount > 0 {
			row.Status = "SEQUENCE_GAP"
			row.SequenceGapCount = state.sequenceGapCount
		}
		return row
	}

	bids := observed(state.sortedBids)
	asks := observed(state.sortedAsks)
	if len(bids) == 0 || len(asks) == 0 {
		row.Status = "INCOMPLETE"
		return row
	}

	midRate, _ := resolve(options.midRate, pair.Quote)
	buyRate, buyRateOK := resolve(options.buyRate, pair.Quote)
	sellRate, sellRateOK := resolve(options.sellRate, pair.Quote)
	populateMarketMetrics(&row, state, bids, asks, midRate, buyRate, sellRate, options.now)
	row.Status = s.status(row, midRate, options.quiet)
	populateExecutions(&row, bids, asks, buyRate, buyRateOK, sellRate, sellRateOK, options)
	return row
}

func emptyRow(pair domain.MarketPair, notionalKRW float64, scenarios []float64) Row {
	row := Row{
		Scope: pair.Scope, Exchange: pair.Exchange, Market: pair.Symbol,
		Base: pair.Base, Quote: pair.Quote, Status: "WAITING",
		AnalysisDepth:          AnalysisDepth,
		CumulativeDepthKRW:     map[string]*float64{"1": nil, "3": nil, "5": nil, "10": nil},
		BuyExecution:           SimulateExecution(nil, "buy", notionalKRW, 0),
		SellExecution:          SimulateExecution(nil, "sell", notionalKRW, 0),
		BuyExecutionScenarios:  make([]ExecutionScenario, 0, len(scenarios)),
		SellExecutionScenarios: make([]ExecutionScenario, 0, len(scenarios)),
	}
	for _, notional := range scenarios {
		row.BuyExecutionScenarios = append(row.BuyExecutionScenarios, ExecutionScenario{
			NotionalKRW: notional, Execution: SimulateExecution(nil, "buy", notional, 0),
		})
		row.SellExecutionScenarios = append(row.SellExecutionScenarios, ExecutionScenario{
			NotionalKRW: notional, Execution: SimulateExecution(nil, "sell", notional, 0),
		})
	}
	return row
}

func populateMarketMetrics(
	row *Row,
	state *bookState,
	bids, asks []domain.OrderBookLevel,
	midRate, buyRate, sellRate float64,
	now time.Time,
) {
	bestBid := bids[0].Price
	bestAsk := asks[0].Price
	mid := bestBid + (bestAsk-bestBid)/2
	age := math.Max(0, float64(now.UnixMilli()-int64(state.lastReceivedTS))/1000)
	latency := maxInt64(0, int64(state.lastReceivedTS-state.lastEventTS))

	row.SourceDepth = copyInt(state.sourceDepth)
	row.BidLevelCount, row.AskLevelCount = len(bids), len(asks)
	row.AgeSeconds, row.EventLatencyMS = pointer(age), &latency
	row.BestBid, row.BestAsk, row.MidPrice = pointer(bestBid), pointer(bestAsk), pointer(mid)
	row.BestBidKRW = multiplied(bestBid, sellRate)
	row.BestAskKRW = multiplied(bestAsk, buyRate)
	row.BestBidQuantity = pointer(bids[0].Quantity)
	row.BestAskQuantity = pointer(asks[0].Quantity)
	row.BestBidNotionalKRW = multiplied(bestBid*bids[0].Quantity, sellRate)
	row.BestAskNotionalKRW = multiplied(bestAsk*asks[0].Quantity, buyRate)
	row.MidPriceKRW = multiplied(mid, midRate)

	topQuantity := bids[0].Quantity + asks[0].Quantity
	microprice := mid
	if positive(topQuantity) {
		bidWeight := bids[0].Quantity / topQuantity
		askWeight := asks[0].Quantity / topQuantity
		candidate := bestAsk*bidWeight + bestBid*askWeight
		if positive(candidate) {
			microprice = candidate
		}
	}
	row.Microprice = pointer(microprice)
	row.MicropriceKRW = multiplied(microprice, midRate)
	if mid > 0 {
		row.SpreadBPS = pointer((bestAsk - bestBid) / mid * 10_000)
	}

	bidNative := nativeDepth(bids)
	askNative := nativeDepth(asks)
	totalNative := bidNative + askNative
	row.BidDepthKRW = multiplied(bidNative, midRate)
	row.AskDepthKRW = multiplied(askNative, midRate)
	row.DepthKRW = multiplied(totalNative, midRate)
	if totalNative > 0 {
		row.Imbalance = pointer((bidNative - askNative) / totalNative)
	}
	row.CumulativeDepthKRW = cumulativeDepth(bids, asks, midRate)
	row.BidOrderCount = orderCount(bids)
	row.AskOrderCount = orderCount(asks)
	row.UpdateRateHz = state.updateRateHz
	elapsed := math.Max(0.001, float64(state.lastReceivedTS-state.firstReceivedTS)/1000)
	row.BestChangeRateHz = float64(state.bestChangeCount) / elapsed
	if state.updateCount > 0 {
		row.ValidUpdatePercent = float64(state.validUpdateCount) / float64(state.updateCount) * 100
	}
	row.SequenceGapCount = state.sequenceGapCount
}

func (s *Store) status(row Row, midRate float64, quiet bool) string {
	if row.AgeSeconds != nil && *row.AgeSeconds > s.staleAfter.Seconds() {
		if quiet {
			return "QUIET"
		}
		return "STALE"
	}
	if row.BestBid != nil && row.BestAsk != nil && *row.BestBid >= *row.BestAsk {
		return "CROSSED"
	}
	if !positive(midRate) {
		return "FX_UNRESOLVED"
	}
	return "LIVE"
}

func populateExecutions(
	row *Row,
	bids, asks []domain.OrderBookLevel,
	buyRate float64,
	buyRateOK bool,
	sellRate float64,
	sellRateOK bool,
	options rowOptions,
) {
	// QUIET only explains why an old book may be unsurprising; it is not proof
	// that this market's subscription is still synchronized. Executable values
	// require a fresh LIVE book so a silent per-market subscription failure can
	// never release stale prices or depth.
	usable := row.Status == "LIVE"
	row.ExecutionFXComplete = usable && buyRateOK && sellRateOK
	if !usable {
		row.ExecutionBlockedReason = row.Status
	} else if !row.ExecutionFXComplete {
		row.ExecutionBlockedReason = "EXECUTABLE_FX_UNRESOLVED"
	}
	if buyRateOK {
		row.BuyQuoteKRWRate = pointer(buyRate)
	}
	if sellRateOK {
		row.SellQuoteKRWRate = pointer(sellRate)
	}
	if !usable {
		buyRate, sellRate = 0, 0
	}
	row.BuyExecution = SimulateExecution(asks, "buy", options.notionalKRW, buyRate)
	row.SellExecution = SimulateExecution(bids, "sell", options.notionalKRW, sellRate)
	for index, notional := range options.scenarios {
		row.BuyExecutionScenarios[index].Execution = SimulateExecution(asks, "buy", notional, buyRate)
		row.SellExecutionScenarios[index].Execution = SimulateExecution(bids, "sell", notional, sellRate)
	}
}

func observed(levels []domain.OrderBookLevel) []domain.OrderBookLevel {
	return trim(levels, AnalysisDepth)
}

func levelViews(levels []domain.OrderBookLevel, rate float64) []LevelView {
	views := make([]LevelView, 0, len(levels))
	var cumulativeBase, cumulativeQuote float64
	for index, level := range levels {
		notional := level.Price * level.Quantity
		cumulativeBase += level.Quantity
		cumulativeQuote += notional
		views = append(views, LevelView{
			Level: index + 1, Price: level.Price, Quantity: level.Quantity,
			OrderCount: level.OrderCount, NotionalNativeQuote: notional,
			NotionalKRW: multiplied(notional, rate), CumulativeBase: cumulativeBase,
			CumulativeNativeQuote: cumulativeQuote, CumulativeKRW: multiplied(cumulativeQuote, rate),
		})
	}
	return views
}

func cumulativeDepth(bids, asks []domain.OrderBookLevel, rate float64) map[string]*float64 {
	result := make(map[string]*float64, len(depthCheckpoints))
	for _, depth := range depthCheckpoints {
		value := nativeDepth(trim(bids, depth)) + nativeDepth(trim(asks, depth))
		result[strconv.Itoa(depth)] = multiplied(value, rate)
	}
	return result
}

func nativeDepth(levels []domain.OrderBookLevel) float64 {
	var total float64
	for _, level := range levels {
		total += level.Price * level.Quantity
	}
	return total
}

func orderCount(levels []domain.OrderBookLevel) *int64 {
	var total int64
	found := false
	for _, level := range levels {
		if level.OrderCount != nil {
			if *level.OrderCount > math.MaxInt64-total {
				return nil
			}
			total += *level.OrderCount
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}

func multiplied(value, rate float64) *float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || !positive(rate) {
		return nil
	}
	result := value * rate
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil
	}
	return pointer(result)
}

func resolve(resolver RateResolver, quote string) (float64, bool) {
	if resolver == nil {
		return 0, false
	}
	value, ok := resolver(quote)
	return value, ok && positive(value)
}

func fallbackResolver(primary, fallback RateResolver) RateResolver {
	if primary != nil {
		return primary
	}
	return fallback
}

func uniquePositive(values []float64) []float64 {
	seen := make(map[float64]struct{}, len(values))
	result := make([]float64, 0, len(values))
	for _, value := range values {
		if positive(value) {
			seen[value] = struct{}{}
		}
	}
	for value := range seen {
		result = append(result, value)
	}
	sort.Float64s(result)
	return result
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
