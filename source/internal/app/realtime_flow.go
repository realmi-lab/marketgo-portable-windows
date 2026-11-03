package app

import (
	"math"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/orderbook"
)

const realtimeFlowAssetLimit = 30

func buildRealtimeFlowObservation(asOfMS int64, complete bool, rows []marketanalysis.Row, books []orderbook.Row) movement.RealtimeObservation {
	assets := make(map[string]*movement.RealtimeAssetObservation)
	order := make([]string, 0)
	numericValid := true
	for _, row := range rows {
		asset := strings.ToUpper(strings.TrimSpace(row.Base))
		exchange := strings.ToLower(strings.TrimSpace(string(row.Exchange)))
		if asset == "" || row.Scope != domain.ScopeDomestic || strings.ToUpper(strings.TrimSpace(row.Quote)) != "KRW" || !isDomesticRealtimeExchange(exchange) {
			continue
		}
		item := assets[asset]
		if item == nil {
			item = &movement.RealtimeAssetObservation{
				Asset: asset, ExchangeVolumesKRW: map[string]float64{},
				ExchangeTradeCounts: map[string]int64{},
				ExchangeBuyKRW:      map[string]float64{}, ExchangeSellKRW: map[string]float64{},
				ExchangeOrderbooks: map[string]*movement.RealtimeOrderbook{},
			}
			assets[asset] = item
			order = append(order, asset)
		}
		volume, volumeOK := nonnegativeFlow(row.VolumeKRW)
		buy, buyOK := nonnegativeFlow(row.BuyVolumeKRW)
		sell, sellOK := nonnegativeFlow(row.SellVolumeKRW)
		if !volumeOK || !buyOK || !sellOK {
			numericValid = false
		}
		if !addRealtimeFlow(item.ExchangeVolumesKRW, exchange, volume) {
			numericValid = false
		}
		if row.TradeCount < 0 || item.ExchangeTradeCounts[exchange] > math.MaxInt64-row.TradeCount {
			numericValid = false
		} else {
			item.ExchangeTradeCounts[exchange] += row.TradeCount
		}
		if !addRealtimeFlow(item.ExchangeBuyKRW, exchange, buy) {
			numericValid = false
		}
		if !addRealtimeFlow(item.ExchangeSellKRW, exchange, sell) {
			numericValid = false
		}
		if exchange == string(domain.ExchangeBithumb) {
			if !addFiniteVolume(&item.BithumbVolumeKRW, volume) {
				numericValid = false
			}
			if !addFiniteVolume(&item.BithumbBuyKRW, buy) {
				numericValid = false
			}
			if !addFiniteVolume(&item.BithumbSellKRW, sell) {
				numericValid = false
			}
		} else {
			if !addFiniteVolume(&item.CompetitorVolumeKRW, volume) {
				numericValid = false
			}
		}
	}

	for _, item := range assets {
		total := item.BithumbVolumeKRW
		if !addFiniteVolume(&total, item.CompetitorVolumeKRW) {
			numericValid = false
		}
	}
	sort.Strings(order)
	top := append([]string(nil), order...)
	sort.SliceStable(top, func(i, j int) bool {
		left, right := realtimeFlowTotal(assets[top[i]]), realtimeFlowTotal(assets[top[j]])
		if left != right {
			return left > right
		}
		return top[i] < top[j]
	})
	if len(top) > realtimeFlowAssetLimit {
		top = top[:realtimeFlowAssetLimit]
	}
	topAssets := make(map[string]struct{}, len(top))
	for _, asset := range top {
		topAssets[asset] = struct{}{}
	}
	for _, row := range books {
		asset := strings.ToUpper(strings.TrimSpace(row.Base))
		exchange := strings.ToLower(strings.TrimSpace(string(row.Exchange)))
		item := assets[asset]
		if item == nil || !isDomesticRealtimeExchange(exchange) {
			continue
		}
		if _, included := topAssets[asset]; !included {
			continue
		}
		book := &movement.RealtimeOrderbook{
			Status: row.Status, AgeSec: row.AgeSeconds, Imbalance: row.Imbalance,
			SpreadBPS: row.SpreadBPS, BidDepthKRW: row.BidDepthKRW, AskDepthKRW: row.AskDepthKRW,
			BuySlippageBPS:  row.BuyExecution.SlippageBPS,
			SellSlippageBPS: row.SellExecution.SlippageBPS,
		}
		if book.Status == "" {
			book.Status = "UNAVAILABLE"
		}
		item.ExchangeOrderbooks[exchange] = book
		if exchange == string(domain.ExchangeBithumb) {
			item.Orderbook = book
		}
	}

	result := movement.RealtimeObservation{AsOfMS: asOfMS, Status: "DEGRADED", Complete: complete && numericValid, Assets: make([]movement.RealtimeAssetObservation, 0, len(order))}
	if result.Complete {
		result.Status = "LIVE"
	}
	for _, asset := range order {
		result.Assets = append(result.Assets, *assets[asset])
	}
	return result
}

func isDomesticRealtimeExchange(exchange string) bool {
	for _, candidate := range domain.DomesticExchanges {
		if exchange == string(candidate) {
			return true
		}
	}
	return false
}

func nonnegativeFlow(value float64) (float64, bool) {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

func addRealtimeFlow(values map[string]float64, exchange string, value float64) bool {
	total := values[exchange]
	valid := addFiniteVolume(&total, value)
	values[exchange] = total
	return valid
}

func realtimeFlowTotal(value *movement.RealtimeAssetObservation) float64 {
	total := value.BithumbVolumeKRW
	_ = addFiniteVolume(&total, value.CompetitorVolumeKRW)
	return total
}
