package app

import (
	"context"
	"math"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/orderbook"
)

var realtimeBestBookAssets = [...]string{"XRP", "USDT", "BTC", "ETH", "SOL"}

var realtimeBestBookExchanges = [...]domain.Exchange{
	domain.ExchangeUpbit,
	domain.ExchangeBithumb,
}

type TickExchange struct {
	Exchange           domain.Exchange `json:"exchange"`
	SharePct           *float64        `json:"share_pct"`
	VolumeKRW          float64         `json:"volume_krw"`
	TradeCount         uint64          `json:"trade_count"`
	TradeCountSharePct *float64        `json:"trade_count_share_pct"`
}

// TickClosedSecond is a window ending at a closed second, by exchange. The
// tick's own window includes the still-open second, so its totals dip each
// time a second starts; this one does not, and the live charts draw it.
type TickClosedSecond struct {
	AsOfSecond     int64          `json:"as_of_second"`
	WindowComplete bool           `json:"window_complete"`
	CoverageSec    int64          `json:"coverage_sec"`
	Exchanges      []TickExchange `json:"exchanges"`
}

type TickAssetExchange struct {
	Exchange  domain.Exchange `json:"exchange"`
	SharePct  *float64        `json:"share_pct"`
	VolumeKRW float64         `json:"volume_krw"`
}

type TickAssetExchangeShare struct {
	Asset             string              `json:"asset"`
	DomesticVolumeKRW float64             `json:"domestic_volume_krw"`
	Exchanges         []TickAssetExchange `json:"exchanges"`
}

type TickSummary struct {
	DomesticVolumeKRW         float64  `json:"domestic_volume_krw"`
	BithumbVolumeKRW          float64  `json:"bithumb_volume_krw"`
	BithumbSharePct           *float64 `json:"bithumb_share_pct"`
	BithumbShareStatus        string   `json:"bithumb_share_status"`
	UnresolvedFXCount         uint64   `json:"unresolved_fx_count"`
	DomesticTradeCount        uint64   `json:"domestic_trade_count"`
	BithumbTradeCount         uint64   `json:"bithumb_trade_count"`
	BithumbTradeCountSharePct *float64 `json:"bithumb_trade_count_share_pct"`
}

// TickBestOrderBook is the compact browser projection of one WebSocket order
// book. It intentionally exposes only the best level; trade history and deeper
// levels stay out of the 500 ms realtime frame.
type TickBestOrderBook struct {
	Exchange       domain.Exchange `json:"exchange"`
	Asset          string          `json:"asset"`
	Market         string          `json:"market"`
	Status         string          `json:"status"`
	AgeSeconds     *float64        `json:"age_sec"`
	BidPriceKRW    *float64        `json:"bid_price_krw"`
	BidQuantity    *float64        `json:"bid_quantity"`
	BidNotionalKRW *float64        `json:"bid_notional_krw"`
	AskPriceKRW    *float64        `json:"ask_price_krw"`
	AskQuantity    *float64        `json:"ask_quantity"`
	AskNotionalKRW *float64        `json:"ask_notional_krw"`
}

type RealtimeTick struct {
	Version              uint64                   `json:"version"`
	StreamVersion        uint64                   `json:"stream_version"`
	GeneratedAt          float64                  `json:"generated_at"`
	Heartbeat            bool                     `json:"heartbeat,omitempty"`
	WindowSec            int64                    `json:"window_sec"`
	Status               string                   `json:"status"`
	WSRealtimeComplete   bool                     `json:"ws_realtime_complete"`
	WSStatus             string                   `json:"ws_status"`
	WindowComplete       bool                     `json:"window_complete"`
	CoverageSec          int64                    `json:"coverage_sec"`
	CoverageStatus       string                   `json:"coverage_status"`
	GapSeconds           int64                    `json:"gap_seconds"`
	DataSource           map[string]any           `json:"data_source"`
	MarketScope          string                   `json:"market_scope"`
	LiveTransport        string                   `json:"live_market_data_transport"`
	RESTFallback         bool                     `json:"rest_fallback_enabled"`
	Readiness            marketanalysis.Readiness `json:"readiness"`
	Exchanges            []TickExchange           `json:"exchanges"`
	AssetExchangeShares  []TickAssetExchangeShare `json:"asset_exchange_shares"`
	Summary              TickSummary              `json:"summary"`
	BestOrderBookSource  string                   `json:"best_order_book_source"`
	BestOrderBookVersion int64                    `json:"best_order_book_version"`
	BestOrderBookAsOf    float64                  `json:"best_order_book_as_of"`
	BestOrderBooks       []TickBestOrderBook      `json:"best_order_books"`
	// ClosedSecond is the same window ending at the last closed second.
	ClosedSecond *TickClosedSecond `json:"closed_second,omitempty"`
}

func (t RealtimeTick) TickVersion() uint64 {
	if t.StreamVersion > 0 {
		return t.StreamVersion
	}
	return t.Version
}

func (s *Service) RealtimeTickReferences() marketanalysis.TickReferences {
	now := time.Now()
	references := marketanalysis.TickReferences{
		OfficialBinance24h: s.officialBinance24hSnapshot(now),
		OfficialTicker1m:   s.officialTickerTrendState(),
	}
	if s.officialDomestic != nil {
		references.OfficialDomestic24h = s.officialDomestic.Snapshot()
	}
	if s.movementTracker != nil {
		references.OfficialShareMonitor = s.movementTracker.State()
	}
	return references
}

func (s *Service) RealtimeTick(ctx context.Context, window int64, marketScope string) (RealtimeTick, error) {
	if !aggregate.SupportsWindow(window) {
		return RealtimeTick{}, ErrUnsupportedWindow
	}
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return RealtimeTick{}, ErrUnsupportedMarketScope
	}
	now := time.Now()
	endSecond := now.Unix()
	closed := endSecond - 1
	read, err := s.runtime.atomicReadIntervals(ctx, []aggregate.IntervalRequest{
		{Start: domain.UnixSecond(endSecond - window), End: domain.UnixSecond(endSecond)},
		{Start: domain.UnixSecond(closed - window), End: domain.UnixSecond(closed)},
	}, []coverageInterval{
		{start: endSecond - window + 1, end: endSecond},
		{start: closed - window + 1, end: closed},
	})
	if err != nil {
		return RealtimeTick{}, err
	}
	closedSecond := closedSecondWindow(read.intervals[1].Values, read.coverage[1], window, marketScope, closed)
	values := read.intervals[0].Values
	values = filterAnalysisValues(values, marketScope)
	coverage := read.coverage[0]
	domestic := aggregateDomesticValues(values)
	domesticVolume, domesticTrades, unresolved := domestic.totalVolume, domestic.trades, domestic.unresolved
	operational := s.operationalState(now)
	health := operational.health
	status := coverage.Status
	if status == "CONFIRMED" && !health.RealtimeReady && health.RegistryFallbackActive {
		status = "PROVISIONAL"
	} else if status == "CONFIRMED" && !health.RealtimeReady {
		status = "PARTIAL"
	} else if status == "CONFIRMED" && health.RegistryFallbackActive {
		status = "PROVISIONAL"
	} else if status == "CONFIRMED" && unresolved > 0 {
		status = "PROVISIONAL"
	} else if status == "CONFIRMED" {
		// RealtimeTick intentionally includes the current, still-open second.
		status = "PROVISIONAL"
	}
	exchanges := tickExchanges(domestic)
	bithumb := domestic.byExchange[domain.ExchangeBithumb]
	bithumbShare, shareStatus := domesticVolumeShare(bithumb.volume, domesticVolume, unresolved, domestic.valid)
	assetExchangeShares := realtimePriorityAssetExchangeShares(values)
	bestBooks := make([]TickBestOrderBook, 0, len(realtimeBestBookAssets)*len(realtimeBestBookExchanges))
	var bestBookVersion int64
	var bestBookAsOf float64
	if bookSnapshot, bookErr := s.runtime.OrderBookSnapshotWithQuiet(
		ctx, realtimeBestOrderBookMarkets(), 0, nil, now,
	); bookErr == nil {
		bestBooks = projectRealtimeBestOrderBooks(bookSnapshot.Rows)
		bestBookVersion = bookSnapshot.Version
		bestBookAsOf = float64(bookSnapshot.AsOf.UnixNano()) / 1e9
	}
	tick := RealtimeTick{Version: read.version, StreamVersion: realtimeTickStreamVersion(read.version, bestBookVersion), GeneratedAt: float64(now.UnixNano()) / 1e9, WindowSec: window, Status: status, WSRealtimeComplete: health.RealtimeReady, WSStatus: "DEGRADED", WindowComplete: coverage.WindowComplete, CoverageSec: min(window, coverage.CoverageSec), CoverageStatus: coverage.Status, GapSeconds: coverage.GapSeconds, DataSource: map[string]any{"aggregation_version": read.version, "as_of_kind": "CURRENT_PARTIAL_SECOND", "confirmation_semantics": "PROVISIONAL_TRANSPORT_OBSERVATION", "official_reconciled": false, "bithumb_share_status": shareStatus, "unresolved_fx_count": unresolved, "global_realtime_policy": "POLICY_DISABLED", "binance_reference_source": "OFFICIAL_ROLLING_24H_TICKER_REFERENCE_ONLY"}, MarketScope: marketScope, LiveTransport: "WEBSOCKET_ONLY", RESTFallback: false, Readiness: cloneReadiness(operational.readiness), Exchanges: exchanges, AssetExchangeShares: assetExchangeShares, Summary: TickSummary{DomesticVolumeKRW: domesticVolume, BithumbVolumeKRW: bithumb.volume, BithumbSharePct: bithumbShare, BithumbShareStatus: shareStatus, UnresolvedFXCount: unresolved, DomesticTradeCount: domesticTrades, BithumbTradeCount: bithumb.trades, BithumbTradeCountSharePct: ratioUint(bithumb.trades, domesticTrades)}, BestOrderBookSource: "UPBIT_BITHUMB_WEBSOCKET_ORDERBOOK", BestOrderBookVersion: bestBookVersion, BestOrderBookAsOf: bestBookAsOf, BestOrderBooks: bestBooks, ClosedSecond: &closedSecond}
	if health.RealtimeReady {
		tick.WSStatus = "LIVE"
	}
	// Official ticker, Binance reference, movement and realtime-flow snapshots
	// are carried by the slower analysis frame. Repeating those large,
	// independently versioned values in this 500 ms transport tick made JSON
	// and network work scale with the realtime cadence.
	return tick, nil
}

// tickExchanges is each domestic exchange's share, trading and trades.
func tickExchanges(domestic domesticValuesSnapshot) []TickExchange {
	exchanges := make([]TickExchange, 0, len(domain.DomesticExchanges))
	for _, name := range domain.DomesticExchanges {
		row := domestic.byExchange[name]
		share, _ := domesticVolumeShare(row.volume, domestic.totalVolume, domestic.unresolved, domestic.valid)
		exchanges = append(exchanges, TickExchange{
			Exchange: name, SharePct: share, VolumeKRW: row.volume, TradeCount: row.trades,
			TradeCountSharePct: ratioUint(row.trades, domestic.trades),
		})
	}
	return exchanges
}

// closedSecondWindow is the window of `window` seconds ending at the closed
// second asOf, from its interval values and coverage.
func closedSecondWindow(
	values map[aggregate.VolumeKey]aggregate.Totals, observed coverage.Result,
	window int64, marketScope string, asOf int64,
) TickClosedSecond {
	return TickClosedSecond{
		AsOfSecond: asOf, WindowComplete: observed.WindowComplete, CoverageSec: min(window, observed.CoverageSec),
		Exchanges: tickExchanges(aggregateDomesticValuesForScope(values, marketScope)),
	}
}

func realtimePriorityAssetExchangeShares(values map[aggregate.VolumeKey]aggregate.Totals) []TickAssetExchangeShare {
	priority := make(map[string]struct{}, len(realtimeBestBookAssets))
	valuesByAsset := make(map[string]map[aggregate.VolumeKey]aggregate.Totals, len(realtimeBestBookAssets))
	for _, asset := range realtimeBestBookAssets {
		priority[asset] = struct{}{}
		valuesByAsset[asset] = make(map[aggregate.VolumeKey]aggregate.Totals)
	}
	for key, value := range values {
		asset := strings.ToUpper(strings.TrimSpace(key.Base))
		if _, ok := priority[asset]; ok {
			valuesByAsset[asset][key] = value
		}
	}

	result := make([]TickAssetExchangeShare, 0, len(realtimeBestBookAssets))
	for _, asset := range realtimeBestBookAssets {
		domestic := aggregateDomesticValues(valuesByAsset[asset])
		exchanges := make([]TickAssetExchange, 0, len(realtimeBestBookExchanges))
		for _, exchange := range realtimeBestBookExchanges {
			row := domestic.byExchange[exchange]
			share, _ := domesticVolumeShare(row.volume, domestic.totalVolume, domestic.unresolved, domestic.valid)
			exchanges = append(exchanges, TickAssetExchange{
				Exchange: exchange, SharePct: share, VolumeKRW: row.volume,
			})
		}
		result = append(result, TickAssetExchangeShare{
			Asset: asset, DomesticVolumeKRW: domestic.totalVolume, Exchanges: exchanges,
		})
	}
	return result
}

// realtimeTickStreamVersion changes when either trade aggregation or the
// order-book store changes. Both counters are monotonic; their sum therefore
// prevents an order-book-only update from being collapsed into a heartbeat.
func realtimeTickStreamVersion(aggregationVersion uint64, bookVersion int64) uint64 {
	if bookVersion <= 0 {
		return aggregationVersion
	}
	book := uint64(bookVersion)
	if aggregationVersion > math.MaxUint64-book {
		return math.MaxUint64
	}
	return aggregationVersion + book
}

func realtimeBestOrderBookMarkets() []domain.MarketPair {
	markets := make([]domain.MarketPair, 0, len(realtimeBestBookAssets)*len(realtimeBestBookExchanges))
	for _, asset := range realtimeBestBookAssets {
		for _, exchange := range realtimeBestBookExchanges {
			markets = append(markets, domain.MarketPair{
				Exchange: exchange,
				Symbol:   "KRW-" + asset,
				Base:     asset,
				Quote:    "KRW",
				Scope:    domain.ScopeDomestic,
			})
		}
	}
	return markets
}

func projectRealtimeBestOrderBooks(rows []orderbook.Row) []TickBestOrderBook {
	books := make([]TickBestOrderBook, 0, len(rows))
	for _, row := range rows {
		books = append(books, TickBestOrderBook{
			Exchange: row.Exchange, Asset: row.Base, Market: row.Market,
			Status: row.Status, AgeSeconds: row.AgeSeconds,
			BidPriceKRW: row.BestBidKRW, BidQuantity: row.BestBidQuantity,
			BidNotionalKRW: row.BestBidNotionalKRW,
			AskPriceKRW:    row.BestAskKRW, AskQuantity: row.BestAskQuantity,
			AskNotionalKRW: row.BestAskNotionalKRW,
		})
	}
	return books
}
func ratioUint(part, whole uint64) *float64 {
	if whole == 0 {
		return nil
	}
	value := float64(part) / float64(whole) * 100
	return &value
}
