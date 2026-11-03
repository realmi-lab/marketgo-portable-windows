package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	collectionserver "market-share-go/contracts/collection"
	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/config"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/ingestlog"
	"market-share-go/internal/store/postgres"
)

// StartComputeService owns calculation state, never exchange connections.
func StartComputeService(parent context.Context, settings config.Config, logger *slog.Logger) (*Service, error) {
	return StartComputeServiceWithFreshState(parent, settings, logger, true)
}

// StartComputeServiceWithFreshState always starts at the committed journal head.
// The legacy flag is ignored: startup backlog replay is disabled by policy.
func StartComputeServiceWithFreshState(parent context.Context, settings config.Config, logger *slog.Logger, _ bool) (*Service, error) {
	if settings.StorageMode == config.StorageSQLiteMemory {
		return nil, errors.New("calculator requires its isolated PostgreSQL journal")
	}
	ctx, cancel := context.WithCancel(parent)
	service := &Service{cancel: cancel, logger: logger, startedAt: time.Now(), computeOnly: true, collectorURL: settings.CollectorURL, collectorToken: settings.CollectorToken}
	service.freshStartSecond = service.startedAt.Unix() + 1
	logger.Info("calculator starting at current journal head", "start_second", service.freshStartSecond)
	if err := service.start(ctx, settings); err != nil {
		cancel()
		service.cleanupFailedStart()
		return nil, err
	}
	service.running.Store(true)
	service.invalidateOperationalSnapshot()
	return service, nil
}

func (s *Service) monitorOrderBookLatest(ctx context.Context, ingestionRestored <-chan struct{}) {
	select {
	case <-ingestionRestored:
	case <-ctx.Done():
		return
	}
	if s.collectorURL != "" {
		s.monitorLiveOrderBooks(ctx)
		return
	}
	seen := make(map[string]postgres.OrderBookLatestVersion)
	var retries ingestionRetry
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		versions := make([]postgres.OrderBookLatestVersion, 0, len(seen))
		for _, version := range seen {
			versions = append(versions, version)
		}
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rows, err := s.postgresRepository.ReadOrderBookLatestSince(readCtx, versions)
		cancel()
		if err == nil {
			retries.reset()
			for _, snapshot := range rows {
				// The query filters by receipt time and, for same-ms updates,
				// sequence. A timestamp-only guard would discard those successors.
				event := orderBookEventFromLatest(snapshot)
				if _, applyErr := s.runtime.SubmitOrderBook(ctx, event); applyErr != nil {
					if ctx.Err() != nil {
						return
					}
					s.logger.Warn("latest order-book apply failed", "exchange", snapshot.Exchange, "market", strings.TrimSpace(snapshot.Market), "error", applyErr)
					continue
				}
				seen[snapshot.Key()] = postgres.OrderBookLatestVersion{Exchange: snapshot.Exchange, Market: snapshot.Market, ReceivedTS: snapshot.ReceivedTS, Sequence: snapshot.Sequence}
			}
		} else if ctx.Err() == nil {
			s.logger.Warn("latest order-book read failed", "error", err)
			if !retries.wait(ctx) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func orderBookEventFromLatest(snapshot domain.OrderBookTopSnapshot) domain.OrderBookEvent {
	scope := domain.ScopeGlobal
	for _, exchange := range domain.DomesticExchanges {
		if snapshot.Exchange == exchange {
			scope = domain.ScopeDomestic
			break
		}
	}
	return domain.OrderBookEvent{Exchange: snapshot.Exchange, Market: snapshot.Market, Base: snapshot.Base, Quote: snapshot.Quote, Scope: scope,
		Bids: snapshot.Bids, Asks: snapshot.Asks, EventTS: snapshot.EventTS, ReceivedTS: snapshot.ReceivedTS,
		Action: domain.BookActionSnapshot, Sequence: snapshot.Sequence}
}

func (s *Service) startIngestionConsumer(ctx context.Context, cursor int64) <-chan struct{} {
	restored := make(chan struct{})
	s.startBackground(ctx, func(ctx context.Context) {
		var restoreRetry ingestionRetry
		for ctx.Err() == nil {
			if err := s.restoreIngestionSnapshots(ctx, cursor); err == nil {
				break
			} else {
				s.markIngestionBlocked(err)
				s.logger.Error("computation snapshot restore blocked", "error", err)
			}
			if !restoreRetry.wait(ctx) {
				return
			}
		}
		close(restored)
		var lastOfficialAsOfMS int64
		var readRetry ingestionRetry
		for ctx.Err() == nil {
			batch, err := s.postgresRepository.ReadIngestionForCompute(ctx, cursor, ingestlog.MaxReadBatch)
			if err == nil && len(batch) > 0 {
				var apply func()
				apply, err = s.decodeIngestionBatch(cursor, batch)
				if err == nil {
					err = s.persistIngestionOfficial(ctx, batch, &lastOfficialAsOfMS)
				}
				if err == nil {
					next := batch[len(batch)-1].Cursor
					var commitRetry ingestionRetry
					// Keep exactly this batch until its absolute checkpoint commits. A lost
					// SQL acknowledgement must not cause the events to be applied twice.
					for ctx.Err() == nil {
						commitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
						err = s.runtime.applyIngestionBatch(commitCtx, cursor, next, apply)
						cancel()
						if err == nil {
							cursor = next
							s.invalidateOperationalSnapshot()
							break
						}
						// Owner admission/encoding can time out before SQL starts.
						// Record those failures too so readiness and background
						// cleanup see pressure throughout the retained-batch retry.
						if ctx.Err() == nil {
							s.markIngestionBlocked(err)
						}
						s.logger.Error("computation checkpoint blocked", "cursor", cursor, "error", err)
						if !commitRetry.wait(ctx) {
							return
						}
					}
				}
			}
			if err != nil {
				s.markIngestionBlocked(err)
				s.logger.Error("computation journal blocked", "cursor", cursor, "error", err)
			}
			// Partial batches wait briefly so the next transaction amortizes the
			// full owner checkpoint. Full batches keep draining backlog without
			// imposing an artificial maximum throughput.
			if err != nil {
				if !readRetry.wait(ctx) {
					return
				}
				continue
			}
			readRetry.reset()
			if len(batch) < ingestlog.MaxReadBatch {
				if !waitIngestion(ctx, 250*time.Millisecond) {
					return
				}
			}
		}
	})
	return restored
}

// Full successful batches still drain immediately. Only failed DB/restore
// attempts back off, preserving the exact pending batch and cursor.
type ingestionRetry struct{ delay time.Duration }

func (r *ingestionRetry) reset() { r.delay = 0 }
func (r *ingestionRetry) next() time.Duration {
	if r.delay == 0 {
		r.delay = 250 * time.Millisecond
	}
	delay := r.delay
	r.delay = min(2*r.delay, 5*time.Second)
	return delay
}
func (r *ingestionRetry) wait(ctx context.Context) bool { return waitIngestion(ctx, r.next()) }
func waitIngestion(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Decode the whole transaction before mutating any owner state. A malformed or
// future-version event cannot leave a partially applied, acknowledged batch.
func (s *Service) decodeIngestionBatch(cursor int64, events []ingestlog.Event) (func(), error) {
	operations := make([]func(), 0, len(events))
	for i, event := range events {
		if err := event.Validate(); err != nil {
			return nil, err
		}
		if event.ProjectionError != "" {
			return nil, fmt.Errorf("ingestion projection at cursor %d requires repair: %s", event.Cursor, event.ProjectionError)
		}
		if event.Cursor != cursor+int64(i)+1 {
			return nil, errors.New("computation journal cursor gap")
		}
		if s.freshStartSecond > 0 && event.ReceivedAtNS/int64(time.Second) < s.freshStartSecond && event.Kind != "registry" && event.Kind != "official" {
			continue
		}
		var operation func()
		switch event.Kind {
		case "frame":
			// Order books now have a bounded latest-only path owned by the
			// collector. Decode only trades from legacy frame rows so calculator
			// restart cannot replay stale books or contend for latest-row writes.
			var value struct {
				Message struct {
					Trades []domain.TradeEvent
				}
			}
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				return nil, err
			}
			for _, trade := range value.Message.Trades {
				if s.beforeFreshStart(trade) {
					continue
				}
				if trade.Validate() == nil && int64(trade.ReceivedTS)/1000 <= time.Now().Unix()-s.runtime.retentionSec && int64(trade.EventTS)/1000 <= time.Now().Unix()-s.runtime.retentionSec {
					return nil, errors.New("journal trade is older than calculator retention; historical recovery required")
				}
			}
			operation = func() {
				for _, trade := range value.Message.Trades {
					if s.beforeFreshStart(trade) {
						continue
					}
					s.runtime.processTrade(trade, false)
				}
			}
		case "loss":
			var value collector.ObservationLoss
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				return nil, err
			}
			operation = func() {
				if !observationLossAffectsKRW(value) {
					return
				}
				start, end := observationLossBounds(value)
				s.runtime.invalidateDomesticCoverage(start, end, coverage.GapContext{AffectedExchanges: []string{string(value.Exchange)}, WeightWindowSec: 10, ReasonCodes: observationLossReasonCodes(value)})
				s.runtime.coverageDirty = true
			}
		case "registry":
			var value collectionserver.RegistryPayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				return nil, err
			}
			universe, err := domain.NewMarketUniverse(value.Markets)
			if err != nil {
				return nil, err
			}
			operation = func() {
				s.recordIngestionRegistryChange(event.ReceivedAtNS, value.Markets)
				_ = s.registry.RestoreRemoteSnapshot(value.Snapshot, value.Markets)
				s.runtime.oracle.Configure(universe)
			}
		case "official":
			var value collectionserver.OfficialPayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				return nil, err
			}
			if value.Source == "domestic" || value.Source == "aligned" {
				decoded, err := value.DecodeDomestic()
				if err != nil {
					return nil, err
				}
				value.Domestic = decoded
			}
			switch value.Source {
			case "domestic":
				if value.Domestic == nil {
					return nil, errors.New("missing domestic ticker payload")
				}
				operation = func() { s.officialDomestic.RestoreRemoteSnapshot(*value.Domestic) }
			case "aligned":
				if value.Domestic == nil {
					return nil, errors.New("missing aligned candle payload")
				}
				operation = func() { s.officialAlignedDomestic.RestoreRemoteSnapshot(*value.Domestic) }
			case "binance":
				if value.Binance == nil {
					return nil, errors.New("missing binance ticker payload")
				}
				operation = func() { s.binanceTicker.RestoreRemoteSnapshot(*value.Binance) }
			default:
				return nil, fmt.Errorf("unknown official input %q", value.Source)
			}
		case "health":
			var value collectionserver.HealthPayload
			// The custom decoder already validates the full JSON document.
			// Avoid the outer json.Unmarshal validation of the same large bytes.
			if err := value.UnmarshalJSON(event.Payload); err != nil {
				return nil, err
			}
			health, err := value.DecodeHealth()
			if err != nil {
				return nil, err
			}
			if value.At.IsZero() {
				return nil, errors.New("missing collector observation time")
			}
			operation = func() {
				s.collectors.RestoreRemoteSnapshot(value.At, health, value.Activity)
				s.applyCollectedObservation(value.At, health)
				if err := s.finalizeIngestionMinutes(value.At); err != nil {
					s.runtime.ingestion.derivedError = err
				}
			}
		default:
			return nil, fmt.Errorf("unsupported computation event %q", event.Kind)
		}
		operations = append(operations, operation)
	}
	return func() {
		for _, apply := range operations {
			apply()
		}
	}, nil
}

// Observation time comes from the collector journal, never replay wall time.
// This preserves a collector outage even when calculator catch-up is fast.
func (s *Service) applyCollectedObservation(at time.Time, health map[domain.Exchange]collector.ExchangeHealth) {
	previousObservation := s.runtime.coverage.LastObservedSecond()
	if previousObservation > 0 && at.Unix()-previousObservation > 3 {
		s.runtime.invalidateDomesticCoverage(previousObservation+1, at.Unix()-1, coverage.GapContext{WeightWindowSec: 10, ReasonCodes: []string{"COLLECTOR_HEALTH_JOURNAL_GAP"}})
	}
	snapshot, universe := s.registry.View()
	complete := true
	from := at.Unix()
	gap := coverage.GapContext{WeightWindowSec: 10}
	for _, name := range domain.DomesticExchanges {
		row := health[name]
		registered := snapshot.Exchanges[name]
		if registered.MarketCount == 0 || !collectorKRWSubscriptionSetMatches(universe.ForExchange(name), row) || !domesticKRWCollectorReady(name, row) {
			complete = false
			gap.AffectedExchanges = append(gap.AffectedExchanges, string(name))
			invalid := observationKRWInvalidFrom(row, at.Unix())
			if invalid > 0 && invalid < from {
				from = invalid
			}
		}
	}
	if complete {
		s.runtime.coverage.Advance(at.Unix(), true, gap)
	} else {
		gap.ReasonCodes = []string{"COLLECTOR_OBSERVATION_INCOMPLETE"}
		s.runtime.invalidateDomesticCoverage(from, at.Unix(), gap)
	}
	s.runtime.coverageDirty = true
}

func (s *Service) restoreIngestionSnapshots(ctx context.Context, cursor int64) error {
	if s.freshStartSecond > 0 {
		// Fresh mode must also bypass the historical DISTINCT snapshot scan.
		// Registry configuration is already loaded at startup. New journal
		// observations and latest books establish all remaining source state.
		return s.runtime.handoffOwnerTurn(ctx, func(*pendingFlush) error {
			s.runtime.oracle.Configure(s.registry.Universe())
			return nil
		})
	}
	fxCheckpoint, err := s.postgresRepository.ReadIngestionFXCheckpoint(ctx)
	if err != nil {
		return err
	}
	if cursor > 0 && len(fxCheckpoint) == 0 {
		return errors.New("committed ingestion cursor has no FX checkpoint")
	}
	events, err := s.postgresRepository.LoadIngestionSnapshots(ctx, cursor)
	if err != nil {
		return err
	}
	operations := make([]func(), 0, len(events))
	for _, event := range events {
		if event.Kind == "health" {
			var value collectionserver.HealthPayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				return err
			}
			health, err := value.DecodeHealth()
			if err != nil {
				return err
			}
			operations = append(operations, func() { s.collectors.RestoreRemoteSnapshot(value.At, health, value.Activity) })
			continue
		}
		if event.Kind != "registry" && event.Kind != "official" {
			return errors.New("unexpected bootstrap ingestion kind")
		}
		apply, err := s.decodeIngestionBatch(event.Cursor-1, []ingestlog.Event{event})
		if err != nil {
			return err
		}
		operations = append(operations, apply)
	}
	return s.runtime.handoffOwnerTurn(ctx, func(*pendingFlush) error {
		for _, apply := range operations {
			apply()
		}
		if len(fxCheckpoint) > 0 {
			return s.runtime.restoreIngestionCheckpoint(fxCheckpoint)
		}
		return nil
	})
}

func (s *Service) beforeFreshStart(trade domain.TradeEvent) bool {
	return s.freshStartSecond > 0 && (int64(trade.EventTS)/1000 < s.freshStartSecond || int64(trade.ReceivedTS)/1000 < s.freshStartSecond)
}

func (s *Service) markIngestionBlocked(err error) {
	s.runtime.updateMetrics(func(m *RuntimeMetrics) {
		m.LastFlushError = "ingestion blocked: " + err.Error()
		m.LastFlushErrorAt = time.Now()
	})
	s.invalidateOperationalSnapshot()
}
