package app

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
	"market-share-go/internal/orderbook"
)

var ErrUnknownMarket = errors.New("unknown market")

type MarketRow struct {
	Scope             domain.Scope    `json:"scope"`
	Exchange          domain.Exchange `json:"exchange"`
	Market            string          `json:"market"`
	Base              string          `json:"base"`
	Quote             string          `json:"quote"`
	NativeVolume      float64         `json:"native_volume"`
	VolumeKRW         float64         `json:"volume_krw"`
	TradeCount        uint64          `json:"trade_count"`
	UnresolvedFXCount uint64          `json:"unresolved_fx_count"`
	BuyVolumeKRW      float64         `json:"buy_volume_krw"`
	SellVolumeKRW     float64         `json:"sell_volume_krw"`
	BaseQuantity      float64         `json:"base_quantity"`
}
type MarketRowsResponse struct {
	WindowSec   int64       `json:"window_sec"`
	Scope       string      `json:"scope"`
	Exchange    string      `json:"exchange"`
	Total       int         `json:"total"`
	Markets     []MarketRow `json:"markets"`
	GeneratedAt float64     `json:"generated_at"`
}
type LiquidityRowsResponse struct {
	ReferenceNotionalKRW float64         `json:"reference_notional_krw"`
	Total                int             `json:"total"`
	Markets              []orderbook.Row `json:"markets"`
	GeneratedAt          float64         `json:"generated_at"`
}

func (s *Service) MarketRows(ctx context.Context, window int64, scope, exchange string) (MarketRowsResponse, error) {
	if !aggregate.SupportsWindow(window) {
		return MarketRowsResponse{}, ErrUnsupportedWindow
	}
	normalizedScope, err := normalizeScope(scope)
	if err != nil {
		return MarketRowsResponse{}, err
	}
	normalizedExchange := normalizeExchangeFilter(exchange)
	endSecond := time.Now().Unix() - 1
	read, err := s.runtime.atomicRead(ctx, []aggregate.IntervalRequest{{
		Start: domain.UnixSecond(endSecond - window),
		End:   domain.UnixSecond(endSecond),
	}}, nil)
	if err != nil {
		return MarketRowsResponse{}, err
	}
	if !read.intervals[0].OK {
		return MarketRowsResponse{}, ErrUnsupportedWindow
	}
	rows := marketRowsFromValues(read.intervals[0].Values, normalizedScope, normalizedExchange)
	scopeLabel := normalizedScope
	if scopeLabel == "" {
		scopeLabel = "all"
	}
	exchangeLabel := normalizedExchange
	if exchangeLabel == "" {
		exchangeLabel = "all"
	}
	return MarketRowsResponse{WindowSec: window, Scope: scopeLabel, Exchange: exchangeLabel, Total: len(rows), Markets: rows, GeneratedAt: float64(time.Now().UnixNano()) / 1e9}, nil
}

func marketRowsFromValues(values map[aggregate.VolumeKey]aggregate.Totals, normalizedScope, normalizedExchange string) []MarketRow {
	rows := make([]MarketRow, 0, len(values))
	for key, total := range values {
		if strings.EqualFold(key.Scope, string(domain.ScopeGlobal)) {
			continue
		}
		if normalizedScope != "" && key.Scope != normalizedScope {
			continue
		}
		if normalizedExchange != "" && key.Exchange != normalizedExchange {
			continue
		}
		rows = append(rows, marketRow(key, total))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].VolumeKRW == rows[j].VolumeKRW {
			if rows[i].Exchange == rows[j].Exchange {
				return rows[i].Market < rows[j].Market
			}
			return rows[i].Exchange < rows[j].Exchange
		}
		return rows[i].VolumeKRW > rows[j].VolumeKRW
	})
	return rows
}

func (s *Service) LiquidityRows(ctx context.Context, notional float64, scope, exchange, asset string) (LiquidityRowsResponse, error) {
	notional, err := s.referenceNotional(notional)
	if err != nil {
		return LiquidityRowsResponse{}, err
	}
	normalizedScope, err := normalizeScope(scope)
	if err != nil {
		return LiquidityRowsResponse{}, err
	}
	normalizedExchange := normalizeExchangeFilter(exchange)
	asset = strings.ToUpper(strings.TrimSpace(asset))
	markets := s.registry.Universe().Markets()
	filtered := make([]domain.MarketPair, 0, len(markets))
	for _, market := range markets {
		if market.Scope == domain.ScopeGlobal {
			continue
		}
		if normalizedScope != "" && string(market.Scope) != normalizedScope {
			continue
		}
		if normalizedExchange != "" && string(market.Exchange) != normalizedExchange {
			continue
		}
		if asset != "" && market.Base != asset {
			continue
		}
		filtered = append(filtered, market)
	}
	filtered = orderBookMarkets(filtered)
	activity := s.collectors.MarketActivitySnapshot(time.Now())
	quiet := quietOrderBookMarkets(activity, s.runtime.OrderBookStaleAfterSec())
	rows, err := s.runtime.OrderBookRowsWithQuiet(ctx, filtered, notional, quiet)
	if err != nil {
		return LiquidityRowsResponse{}, err
	}
	return LiquidityRowsResponse{ReferenceNotionalKRW: notional, Total: len(rows), Markets: rows, GeneratedAt: float64(time.Now().UnixNano()) / 1e9}, nil
}

func (s *Service) OrderBookDetail(ctx context.Context, exchangeName, marketName string, notional float64) (orderbook.Detail, error) {
	notional, err := s.referenceNotional(notional)
	if err != nil {
		return orderbook.Detail{}, err
	}
	exchange, err := domain.ParseExchange(exchangeName)
	if err != nil {
		return orderbook.Detail{}, ErrUnknownMarket
	}
	pair, ok := s.registry.Universe().Find(exchange, marketName)
	if !ok || pair.Scope == domain.ScopeGlobal {
		return orderbook.Detail{}, ErrUnknownMarket
	}
	return s.runtime.OrderBookDetail(ctx, pair, notional)
}

func (s *Service) referenceNotional(value float64) (float64, error) {
	if value == 0 {
		value = s.referenceOrderKRW
	}
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("notional_krw must be finite and greater than zero")
	}
	return value, nil
}
func normalizeScope(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "all" {
		value = ""
	}
	if value != "" && value != "domestic" && value != "global" {
		return "", errors.New("scope must be one of all, domestic, or global")
	}
	return value, nil
}
func normalizeExchangeFilter(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "all" {
		return ""
	}
	return value
}
func marketRow(key aggregate.VolumeKey, total aggregate.Totals) MarketRow {
	return MarketRow{Scope: domain.Scope(key.Scope), Exchange: domain.Exchange(key.Exchange), Market: key.Market, Base: key.Base, Quote: key.Quote, NativeVolume: total.NativeVolume, VolumeKRW: total.KRWVolume, TradeCount: total.TradeCount, UnresolvedFXCount: total.UnresolvedCount, BuyVolumeKRW: total.BuyKRWVolume, SellVolumeKRW: total.SellKRWVolume, BaseQuantity: total.BaseVolume}
}
