package app

import (
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/domain"
	"market-share-go/contracts/collection/registry"
)

const operationalSnapshotTTL = time.Second

// operationalSnapshot is immutable after publication. Health, readiness and
// the observation monitor all consume the same collector/registry generation,
// so a busy WebSocket fan-out cannot repeatedly copy and sort collector state.
type operationalSnapshot struct {
	generatedAt   time.Time
	running       bool
	databaseReady bool
	metrics       RuntimeMetrics
	consumerAlive bool
	engine        aggregate.EngineInfo
	health        HealthState
	readiness     marketanalysis.Readiness
	registry      registry.Snapshot
	universe      domain.MarketUniverse
	collectors    map[domain.Exchange]collector.ExchangeHealth
	exchangeReady map[domain.Exchange]bool
}

type operationalInputs struct {
	metrics       RuntimeMetrics
	consumerAlive bool
	engine        aggregate.EngineInfo
	registry      registry.Snapshot
	universe      domain.MarketUniverse
	collectors    map[domain.Exchange]collector.ExchangeHealth
}

func (s *Service) operationalState(now time.Time) *operationalSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	if cached := s.operational.Load(); s.operationalSnapshotFresh(cached, now) {
		return cached
	}

	s.operationalMu.Lock()
	defer s.operationalMu.Unlock()
	if cached := s.operational.Load(); s.operationalSnapshotFresh(cached, now) {
		return cached
	}

	running := s.running.Load()
	databaseReady := s.databaseReady.Load()
	inputs := s.readOperationalInputs(now)
	collectorHealth := cloneCollectorHealthMap(inputs.collectors)
	registryHealth := cloneRegistrySnapshot(inputs.registry)
	if collectorHealth == nil {
		collectorHealth = map[domain.Exchange]collector.ExchangeHealth{}
	}
	if registryHealth.Exchanges == nil {
		registryHealth.Exchanges = map[domain.Exchange]registry.ExchangeSnapshot{}
	}

	health := HealthState{
		Status:        "starting",
		Running:       running,
		DatabaseReady: databaseReady,
		Unhealthy:     []string{},
	}
	dependenciesReady := s.operationalSource != nil || (s.registry != nil && s.collectors != nil && s.runtime != nil)
	if running && dependenciesReady {
		health = buildHealthState(
			running,
			databaseReady,
			inputs.consumerAlive,
			aggregationErrorActive(inputs.metrics),
			registryHealth,
			collectorHealth,
		)
	}
	readiness := buildReadiness(
		running,
		s.startedAt,
		now,
		collectorHealth,
		inputs.universe,
		inputs.metrics,
		inputs.consumerAlive,
		databaseReady,
	)
	exchangeReady := make(map[domain.Exchange]bool, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		row := collectorHealth[exchange]
		exchangeReady[exchange] = domesticKRWCollectorReady(exchange, row)
	}

	current := &operationalSnapshot{
		generatedAt:   now,
		running:       running,
		databaseReady: databaseReady,
		metrics:       inputs.metrics,
		consumerAlive: inputs.consumerAlive,
		engine:        inputs.engine,
		health:        health,
		readiness:     readiness,
		registry:      registryHealth,
		universe:      inputs.universe,
		collectors:    collectorHealth,
		exchangeReady: exchangeReady,
	}
	s.operational.Store(current)
	return current
}

func (s *Service) readOperationalInputs(now time.Time) operationalInputs {
	if s.operationalSource != nil {
		return s.operationalSource(now)
	}
	result := operationalInputs{
		registry:   registry.Snapshot{Exchanges: map[domain.Exchange]registry.ExchangeSnapshot{}},
		collectors: map[domain.Exchange]collector.ExchangeHealth{},
	}
	if s.collectors != nil {
		result.collectors = s.collectors.Health(now)
	}
	if s.registry != nil {
		result.registry, result.universe = s.registry.View()
	}
	if s.runtime != nil {
		result.metrics = s.runtime.Metrics()
		result.consumerAlive = s.runtime.Running()
		result.engine = s.runtime.EngineInfo()
	}
	return result
}

func (s *Service) operationalSnapshotFresh(snapshot *operationalSnapshot, now time.Time) bool {
	if snapshot == nil {
		return false
	}
	age := now.Sub(snapshot.generatedAt)
	return age >= 0 && age < operationalSnapshotTTL &&
		snapshot.running == s.running.Load() &&
		snapshot.databaseReady == s.databaseReady.Load()
}

func (s *Service) invalidateOperationalSnapshot() {
	s.operational.Store(nil)
}

func (s *Service) exchangeRealtimeReadiness(now time.Time) map[domain.Exchange]bool {
	snapshot := s.operationalState(now)
	result := make(map[domain.Exchange]bool, len(snapshot.exchangeReady))
	for exchange, ready := range snapshot.exchangeReady {
		result[exchange] = ready
	}
	return result
}

func cloneHealthState(value HealthState) HealthState {
	value.Unhealthy = append([]string(nil), value.Unhealthy...)
	value.ExcludedExchanges = append([]string(nil), value.ExcludedExchanges...)
	return value
}

func cloneCollectorHealthMap(values map[domain.Exchange]collector.ExchangeHealth) map[domain.Exchange]collector.ExchangeHealth {
	if values == nil {
		return nil
	}
	result := make(map[domain.Exchange]collector.ExchangeHealth, len(values))
	for exchange, value := range values {
		result[exchange] = cloneCollectorHealth(value)
	}
	return result
}

func cloneCollectorHealth(value collector.ExchangeHealth) collector.ExchangeHealth {
	value.SubscribedMarketSymbols = append([]string(nil), value.SubscribedMarketSymbols...)
	value.KRWSubscribedMarketSymbols = append([]string(nil), value.KRWSubscribedMarketSymbols...)
	value.Errors = append([]string(nil), value.Errors...)
	value.ConnectedMarketPercent = cloneOptionalFloat(value.ConnectedMarketPercent)
	value.FreshMarketPercent = cloneOptionalFloat(value.FreshMarketPercent)
	value.FreshSubscriptionPercent = cloneOptionalFloat(value.FreshSubscriptionPercent)
	value.MaxMessageAgeSec = cloneOptionalFloat(value.MaxMessageAgeSec)
	value.MaxDataAgeSec = cloneOptionalFloat(value.MaxDataAgeSec)
	value.MaxTradeAgeSec = cloneOptionalFloat(value.MaxTradeAgeSec)
	value.MaxOrderBookAgeSec = cloneOptionalFloat(value.MaxOrderBookAgeSec)
	return value
}

func cloneRegistrySnapshot(value registry.Snapshot) registry.Snapshot {
	if value.Exchanges == nil {
		return value
	}
	value.Exchanges = cloneRegistryExchanges(value.Exchanges)
	return value
}

func cloneRegistryExchanges(values map[domain.Exchange]registry.ExchangeSnapshot) map[domain.Exchange]registry.ExchangeSnapshot {
	result := make(map[domain.Exchange]registry.ExchangeSnapshot, len(values))
	for exchange, value := range values {
		if value.Quotes != nil {
			quotes := make(map[string]int, len(value.Quotes))
			for quote, count := range value.Quotes {
				quotes[quote] = count
			}
			value.Quotes = quotes
		}
		result[exchange] = value
	}
	return result
}

func cloneOptionalFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneReadiness(value marketanalysis.Readiness) marketanalysis.Readiness {
	value.ReadyExchanges = append([]string(nil), value.ReadyExchanges...)
	value.BlockingExchanges = append([]string(nil), value.BlockingExchanges...)
	value.Venues = append([]marketanalysis.ReadinessVenue(nil), value.Venues...)
	for index := range value.Venues {
		quotes := make(map[string]int, len(value.Venues[index].QuoteSymbolCounts))
		for quote, count := range value.Venues[index].QuoteSymbolCounts {
			quotes[quote] = count
		}
		value.Venues[index].QuoteSymbolCounts = quotes
	}
	if value.ConnectedMarketPercent != nil {
		percentage := *value.ConnectedMarketPercent
		value.ConnectedMarketPercent = &percentage
	}
	return value
}
