package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
	"market-share-go/internal/store/postgres"
)

type ingestionMinuteCheckpoint struct {
	Version             int                       `json:"version"`
	Accumulator         json.RawMessage           `json:"accumulator,omitempty"`
	ObservedSince       int64                     `json:"observed_since"`
	LastHealthAt        int64                     `json:"last_health_at"`
	ClosedBefore        int64                     `json:"closed_before"`
	HasFinalized        bool                      `json:"has_finalized"`
	RegistryFingerprint string                    `json:"registry_fingerprint"`
	RegistryChangedAt   int64                     `json:"registry_changed_at"`
	RecentSeals         []reconcile.MinuteSeal    `json:"recent_seals,omitempty"`
	RecoveryEvents      uint64                    `json:"recovery_events"`
	Recoveries          []ingestionMinuteRecovery `json:"recoveries,omitempty"`
}

type ingestionMinuteRecovery = postgres.IngestionMinuteRecovery

// Live accepted events may trail receipt by thirty seconds. Keep that entire
// existing admissible interval plus the old close grace before sealing OHLC.
const ingestionMinuteCloseDelaySec = (aggregate.MaxLiveEventReceiveSkew + reconcile.CloseGraceMS) / 1000

func (s *ingestionRuntimeState) observeMinute(trade aggregate.AcceptedTrade) error {
	if s.minutes == nil {
		s.minutes = reconcile.NewAccumulator()
	}
	minute := int64(trade.EventSecond) / 60 * 60
	if s.minuteState.HasFinalized && minute < s.minuteState.ClosedBefore {
		s.recordLateMinute(trade, minute)
		return nil
	}
	return s.minutes.IngestAccepted(trade)
}

func (s *ingestionRuntimeState) recordLateMinute(trade aggregate.AcceptedTrade, minute int64) {
	m := &s.minuteState
	m.RecoveryEvents++
	index := -1
	for i, row := range m.Recoveries {
		if row.Exchange == domain.Exchange(trade.Key.Exchange) && row.Market == trade.Key.Market && row.MinuteStart == minute {
			index = i
			break
		}
	}
	if index < 0 {
		if len(m.Recoveries) == 64 {
			copy(m.Recoveries, m.Recoveries[1:])
			m.Recoveries = m.Recoveries[:63]
		}
		m.Recoveries = append(m.Recoveries, ingestionMinuteRecovery{Exchange: domain.Exchange(trade.Key.Exchange), Market: trade.Key.Market, MinuteStart: minute})
		index = len(m.Recoveries) - 1
	}
	m.Recoveries[index].Events++
	found := false
	for i, row := range s.derived.Recoveries {
		if row.Exchange == m.Recoveries[index].Exchange && row.Market == trade.Key.Market && row.MinuteStart == minute {
			s.derived.Recoveries[i] = m.Recoveries[index]
			found = true
			break
		}
	}
	if !found {
		s.derived.Recoveries = append(s.derived.Recoveries, m.Recoveries[index])
	}
	for i, seal := range m.RecentSeals {
		if string(seal.Exchange) != trade.Key.Exchange || int64(seal.MinuteStart) != minute {
			continue
		}
		seal.Status = reconcile.SealMissing
		seal.CoverageSeconds, seal.GapSeconds = 0, 60
		seal.ObservedZeroCount = 0
		seal.UnknownMarketCount = seal.RegistryMarketCount - seal.ObservedMarketCount
		seal.GapMarketCount = seal.RegistryMarketCount
		seal.UpdatedAt = time.UnixMilli(int64(trade.ReceivedTS)).UTC()
		m.RecentSeals[i] = seal
		s.putSeal(seal)
	}
}

func (s *ingestionRuntimeState) observeBook(book domain.OrderBookTopSnapshot) {
	for i, existing := range s.derived.Books {
		if existing.Exchange == book.Exchange && existing.Market == book.Market {
			if book.ReceivedTS >= existing.ReceivedTS {
				s.derived.Books[i] = book
			}
			return
		}
	}
	s.derived.Books = append(s.derived.Books, book)
}

func ingestionRegistryFingerprint(markets []domain.MarketPair) string {
	identities := make([]string, 0, len(markets))
	for _, market := range markets {
		identities = append(identities, string(market.Exchange)+"\x00"+strings.ToUpper(market.Symbol)+"\x00"+strings.ToUpper(market.Base)+"\x00"+strings.ToUpper(market.Quote))
	}
	sort.Strings(identities)
	hash := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	return hex.EncodeToString(hash[:])
}

func ingestionSealFingerprint(markets []domain.MarketPair) string {
	identities := make([]string, 0, len(markets))
	for _, market := range markets {
		identities = append(identities, strings.ToUpper(strings.TrimSpace(market.Symbol))+"\x00"+strings.ToUpper(strings.TrimSpace(market.Base))+"\x00"+strings.ToUpper(strings.TrimSpace(market.Quote))+"\n")
	}
	sort.Strings(identities)
	hash := sha256.Sum256([]byte(strings.Join(identities, "")))
	return hex.EncodeToString(hash[:])
}

func (s *Service) recordIngestionRegistryChange(atNS int64, markets []domain.MarketPair) {
	if s.runtime.ingestion == nil {
		return
	}
	state := &s.runtime.ingestion.minuteState
	fingerprint := ingestionRegistryFingerprint(markets)
	if state.RegistryFingerprint != fingerprint {
		state.RegistryFingerprint = fingerprint
		state.RegistryChangedAt = atNS / 1e9
	}
}

// finalizeIngestionMinutes is owner-only and uses a durable collector health
// timestamp. It performs no HTTP or SQL. Closed rows, conservative seals and
// the remaining open OHLC state are committed by the same cursor transaction.
func (s *Service) finalizeIngestionMinutes(at time.Time) error {
	state := s.runtime.ingestion
	if state == nil {
		return nil
	}
	m := &state.minuteState
	if m.ObservedSince == 0 {
		m.ObservedSince = at.Unix()
	}
	// applyCollectedObservation owns health-gap invalidation and has already
	// applied this observation. Invalidating again here would erase the first
	// recovered second and reopen a duplicate gap after it was closed.
	m.LastHealthAt = max(m.LastHealthAt, at.Unix())
	boundary := (at.Unix() - ingestionMinuteCloseDelaySec) / 60 * 60
	if boundary <= 0 {
		return nil
	}
	if m.ClosedBefore == 0 {
		m.ClosedBefore = at.Unix() / 60 * 60
	}
	// Coverage cannot change during this owner turn. Reuse one immutable
	// snapshot instead of cloning all historical gaps for every market/seal.
	gaps := s.runtime.coverage.State().Gaps
	exchangeGaps := append([]coverage.Gap(nil), gaps...)
	for i := range exchangeGaps {
		exchangeGaps[i].Context.AffectedMarkets = nil
	}
	if boundary > m.ClosedBefore {
		rows, err := state.minutes.DrainClosed(domain.UnixSecond(boundary))
		if err != nil {
			return err
		}
		for i := range rows {
			minute := int64(rows[i].MinuteStart)
			gap := s.ingestionMinuteGap(rows[i].Exchange, rows[i].Market, minute, gaps)
			rows[i].GapSeconds = gap
			if gap > 0 {
				rows[i].CoverageStatus = reconcile.SealPartial
			}
		}
		state.derived.Minutes = append(state.derived.Minutes, rows...)
		// Long unobserved gaps remain absent/UNKNOWN. Do not manufacture an
		// unbounded calendar of zero rows when the collector resumes.
		start := max(m.ClosedBefore, boundary-120)
		markets := s.registry.Universe().Markets()
		for minute := start; minute < boundary; minute += 60 {
			for _, exchange := range domain.DomesticExchanges {
				members := map[string]domain.MarketPair{}
				for _, market := range markets {
					if market.Exchange == exchange && strings.EqualFold(market.Quote, "KRW") {
						members[market.Symbol] = market
					}
				}
				observed := int64(0)
				for _, row := range rows {
					if row.Exchange == exchange && int64(row.MinuteStart) == minute && strings.EqualFold(row.Quote, "KRW") {
						observed++
						members[row.Market] = domain.MarketPair{Exchange: exchange, Symbol: row.Market, Base: row.Base, Quote: row.Quote, Scope: domain.ScopeDomestic}
					}
				}
				if len(members) == 0 {
					continue
				}
				pairs := make([]domain.MarketPair, 0, len(members))
				for _, market := range members {
					pairs = append(pairs, market)
				}
				seal := reconcile.MinuteSeal{Exchange: exchange, MinuteStart: domain.UnixSecond(minute), RegistryMarketCount: int64(len(pairs)), ObservedMarketCount: observed, RegistryFingerprint: ingestionSealFingerprint(pairs), SealedAt: at.UTC(), UpdatedAt: at.UTC()}
				s.applyIngestionSealGap(&seal, s.ingestionMinuteGap(exchange, "", minute, exchangeGaps))
				m.RecentSeals = append(m.RecentSeals, seal)
				state.putSeal(seal)
			}
		}
		m.ClosedBefore = boundary
		m.HasFinalized = true
	}
	// Retroactive parser/transport loss can only downgrade prior proof. Keep a
	// bounded audit horizon and never replace a stored gap with inferred zeros.
	kept := m.RecentSeals[:0]
	for _, seal := range m.RecentSeals {
		if int64(seal.MinuteStart) < boundary-30*60 {
			continue
		}
		gap := s.ingestionMinuteGap(seal.Exchange, "", int64(seal.MinuteStart), exchangeGaps)
		if gap > seal.GapSeconds {
			s.applyIngestionSealGap(&seal, gap)
			seal.UpdatedAt = at.UTC()
			state.putSeal(seal)
		}
		kept = append(kept, seal)
	}
	m.RecentSeals = kept
	return nil
}

func (s *Service) ingestionMinuteGap(exchange domain.Exchange, market string, minute int64, gaps []coverage.Gap) int64 {
	m := s.runtime.ingestion.minuteState
	for _, recovery := range m.Recoveries {
		if recovery.MinuteStart == minute && recovery.Exchange == exchange && (market == "" || recovery.Market == market) {
			return 60
		}
	}
	if minute < m.ObservedSince || (m.RegistryChangedAt >= minute && m.RegistryChangedAt < minute+60) {
		return 60
	}
	if m.LastHealthAt < minute+59 {
		return 60
	}
	return reconcile.KnownGapSeconds(exchange, market, domain.UnixSecond(minute), gaps, 0)
}

func (s *Service) applyIngestionSealGap(seal *reconcile.MinuteSeal, gap int64) {
	seal.GapSeconds = gap
	seal.CoverageSeconds = 60 - gap
	absent := seal.RegistryMarketCount - seal.ObservedMarketCount
	seal.ObservedZeroCount, seal.UnknownMarketCount, seal.GapMarketCount = 0, 0, 0
	seal.Status = reconcile.SealComplete
	if gap == 0 {
		seal.ObservedZeroCount = absent
		return
	}
	seal.Status = reconcile.SealPartial
	if gap == 60 {
		seal.Status = reconcile.SealMissing
	}
	seal.UnknownMarketCount = absent
	seal.GapMarketCount = seal.RegistryMarketCount
}

func (s *ingestionRuntimeState) putSeal(seal reconcile.MinuteSeal) {
	for i, existing := range s.derived.Seals {
		if existing.Exchange == seal.Exchange && existing.MinuteStart == seal.MinuteStart {
			s.derived.Seals[i] = seal
			return
		}
	}
	s.derived.Seals = append(s.derived.Seals, seal)
}
