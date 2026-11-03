package app

import (
	"strings"

	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/domain"
)

// quietKRWExchanges trade so little that a connected subscription often
// brings no trade or order book for longer than the collector's freshness
// limit (30 seconds), as Korbit and Gopax do at night. For them a quiet
// subscription is an observation of no trades, not missing data (user
// decision, 2026-10-28). A dead connection still counts as missing: the
// collector pings and drops a connection that reads nothing for 90 seconds,
// and a dropped or reconnecting chunk is not connected.
var quietKRWExchanges = map[domain.Exchange]bool{
	domain.ExchangeKorbit: true,
	domain.ExchangeGopax:  true,
}

// domesticKRWCollectorReady reports whether an exchange's KRW markets are
// observed now: every KRW market subscribed without subscription or parse
// errors, with fresh data or, on a quiet exchange, every chunk connected.
func domesticKRWCollectorReady(exchange domain.Exchange, health collector.ExchangeHealth) bool {
	if !health.Enabled || health.KRWMarketCount == 0 ||
		health.KRWActiveTradeParseErrorChunks > 0 || health.KRWActiveSubscriptionErrorChunks > 0 {
		return false
	}
	if health.KRWRealtimeReady && health.KRWTradeStreamReady {
		return true
	}
	return quietKRWExchanges[exchange] && health.KRWChunkCount > 0 &&
		health.KRWConnectedChunks == health.KRWChunkCount &&
		health.KRWSubscribedMarketCount == health.KRWMarketCount
}

func domesticKRWMarkets(markets []domain.MarketPair) []domain.MarketPair {
	result := make([]domain.MarketPair, 0, len(markets))
	for _, market := range markets {
		if market.Scope == domain.ScopeDomestic && strings.EqualFold(strings.TrimSpace(market.Quote), "KRW") {
			result = append(result, market)
		}
	}
	return result
}

func collectorKRWSubscriptionSetMatches(markets []domain.MarketPair, health collector.ExchangeHealth) bool {
	expectedMarkets := domesticKRWMarkets(markets)
	if len(expectedMarkets) == 0 || len(health.KRWSubscribedMarketSymbols) != len(expectedMarkets) {
		return false
	}
	expected := make(map[string]struct{}, len(expectedMarkets))
	for _, market := range expectedMarkets {
		symbol := strings.ToUpper(strings.TrimSpace(market.Symbol))
		if symbol == "" {
			return false
		}
		expected[symbol] = struct{}{}
	}
	if len(expected) != len(expectedMarkets) {
		return false
	}
	for _, symbol := range health.KRWSubscribedMarketSymbols {
		if _, ok := expected[strings.ToUpper(strings.TrimSpace(symbol))]; !ok {
			return false
		}
	}
	return true
}

func observationKRWInvalidFrom(health collector.ExchangeHealth, detectedAt int64) int64 {
	if health.KRWOldestChunkTradeTS > 0 {
		return min(detectedAt, int64(health.KRWOldestChunkTradeTS))
	}
	if health.KRWOldestChunkDataTS > 0 {
		return min(detectedAt, int64(health.KRWOldestChunkDataTS))
	}
	return detectedAt
}

func observationLossAffectsKRW(loss collector.ObservationLoss) bool {
	quote := strings.ToUpper(strings.TrimSpace(loss.Quote))
	// Unknown or legacy mixed-quote chunks remain fail-closed. Quote-aware
	// production chunks make non-KRW losses explicit and safe to ignore here.
	return quote == "" || quote == "KRW"
}

func isDomesticExchange(exchange domain.Exchange) bool {
	for _, domestic := range domain.DomesticExchanges {
		if exchange == domestic {
			return true
		}
	}
	return false
}
