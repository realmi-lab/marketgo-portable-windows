package app

import (
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/domain"
)

func (s *Service) Readiness(now time.Time) marketanalysis.Readiness {
	if now.IsZero() {
		now = time.Now()
	}
	return cloneReadiness(s.operationalState(now).readiness)
}

func buildReadiness(
	running bool,
	startedAt, now time.Time,
	collectors map[domain.Exchange]collector.ExchangeHealth,
	universe domain.MarketUniverse,
	metrics RuntimeMetrics,
	consumerAlive, databaseReady bool,
) marketanalysis.Readiness {
	expected := readinessExchanges(collectors)
	result := marketanalysis.Readiness{
		EnabledExchangeCount:    len(expected),
		ReadyExchanges:          []string{},
		BlockingExchanges:       []string{},
		Venues:                  make([]marketanalysis.ReadinessVenue, 0, len(expected)),
		AggregatorConsumerAlive: consumerAlive,
		UpdatedAt:               float64(now.UnixNano()) / 1e9,
	}
	if !startedAt.IsZero() {
		result.ElapsedSec = max(0, now.Sub(startedAt).Seconds())
	}
	for _, exchange := range expected {
		health := collectors[exchange]
		marketCount := health.MarketCount
		connectedMarketCount := health.ConnectedMarketCount
		parseErrorChunks := health.ActiveTradeParseErrorChunks
		ready := health.TradeStreamReady
		if isDomesticExchange(exchange) {
			marketCount = health.KRWMarketCount
			connectedMarketCount = health.KRWConnectedMarketCount
			parseErrorChunks = health.KRWActiveTradeParseErrorChunks
			ready = domesticKRWCollectorReady(exchange, health)
		}
		if health.Enabled && marketCount > 0 {
			result.ConfiguredExchangeCount++
		}
		if ready {
			result.ReadyExchanges = append(result.ReadyExchanges, string(exchange))
		} else {
			result.BlockingExchanges = append(result.BlockingExchanges, string(exchange))
		}
		result.RegisteredMarketCount += marketCount
		result.ConnectedMarketCount += connectedMarketCount
		result.ActiveTradeParseErrorChunks += parseErrorChunks
		quotes := make(map[string]map[string]struct{})
		assets := make(map[string]struct{})
		for _, market := range universe.ForExchange(exchange) {
			assets[market.Base] = struct{}{}
			if quotes[market.Quote] == nil {
				quotes[market.Quote] = make(map[string]struct{})
			}
			quotes[market.Quote][market.Base] = struct{}{}
		}
		quoteCounts := make(map[string]int, len(quotes))
		for quote, symbols := range quotes {
			quoteCounts[quote] = len(symbols)
		}
		result.Venues = append(result.Venues, marketanalysis.ReadinessVenue{
			Exchange: string(exchange), MarketCount: marketCount,
			ConnectedMarketCount: connectedMarketCount,
			UniqueSymbolCount:    len(assets), QuoteSymbolCounts: quoteCounts,
			Ready: ready,
		})
	}
	result.ReadyExchangeCount = len(result.ReadyExchanges)
	result.ConnectedMarketPercent = ratioPercent(float64(result.ConnectedMarketCount), float64(result.RegisteredMarketCount))
	hasError := !databaseReady || aggregationErrorActive(metrics) || result.ActiveTradeParseErrorChunks > 0
	switch {
	case !running:
		result.Phase = "STOPPED"
	case result.ConfiguredExchangeCount < result.EnabledExchangeCount:
		result.Phase = "REGISTRY"
	case hasError:
		result.Phase = "DEGRADED"
	case !consumerAlive:
		result.Phase = "AGGREGATING"
	case result.ConnectedMarketCount < result.RegisteredMarketCount:
		result.Phase = "CONNECTING"
	case result.ReadyExchangeCount < result.EnabledExchangeCount:
		result.Phase = "OBSERVING"
	default:
		result.Phase = "READY"
	}
	return result
}

func readinessExchanges(collectors map[domain.Exchange]collector.ExchangeHealth) []domain.Exchange {
	result := append([]domain.Exchange(nil), domain.DomesticExchanges[:]...)
	for _, exchange := range domain.GlobalExchanges {
		if collectors[exchange].Enabled {
			result = append(result, exchange)
		}
	}
	return result
}
