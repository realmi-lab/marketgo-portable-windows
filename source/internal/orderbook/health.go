package orderbook

import (
	"sort"
	"time"

	"market-share-go/internal/domain"
)

type HealthIssue struct {
	Exchange domain.Exchange `json:"exchange"`
	Market   string          `json:"market"`
	Base     string          `json:"base"`
	Quote    string          `json:"quote"`
	Scope    domain.Scope    `json:"scope"`
	Status   string          `json:"status"`
	AgeSec   *float64        `json:"age_sec"`
}

type ExchangeHealth struct {
	Exchange               domain.Exchange `json:"exchange"`
	RegisteredMarketCount  int             `json:"registered_market_count"`
	InitializedMarketCount int             `json:"initialized_market_count"`
	FreshMarketCount       int             `json:"fresh_market_count"`
	StaleMarketCount       int             `json:"stale_market_count"`
	UnavailableMarketCount int             `json:"unavailable_market_count"`
	WaitingMarketCount     int             `json:"waiting_market_count"`
	SequenceGapMarketCount int             `json:"sequence_gap_market_count"`
	InitializedMarketPct   *float64        `json:"initialized_market_pct"`
	FreshMarketPct         *float64        `json:"fresh_market_pct"`
}

type Health struct {
	AnalysisDepth          int              `json:"analysis_depth"`
	RegisteredMarketCount  int              `json:"registered_market_count"`
	InitializedMarketCount int              `json:"initialized_market_count"`
	FreshMarketCount       int              `json:"fresh_market_count"`
	StaleMarketCount       int              `json:"stale_market_count"`
	UnavailableMarketCount int              `json:"unavailable_market_count"`
	WaitingMarketCount     int              `json:"waiting_market_count"`
	SequenceGapMarketCount int              `json:"sequence_gap_market_count"`
	InitializedMarketPct   *float64         `json:"initialized_market_pct"`
	ReceivedEvents         int64            `json:"received_events"`
	SequenceGaps           int64            `json:"sequence_gaps"`
	MarketFilterApplied    bool             `json:"market_filter_applied"`
	Exchanges              []ExchangeHealth `json:"exchanges"`
	Issues                 []HealthIssue    `json:"issues"`
}

func (s *Store) Health(markets []domain.MarketPair, now time.Time) Health {
	if now.IsZero() {
		now = time.Now()
	}
	result := Health{
		AnalysisDepth: AnalysisDepth, RegisteredMarketCount: len(markets),
		ReceivedEvents: s.ReceivedEvents, SequenceGaps: s.SequenceGaps,
		Exchanges: []ExchangeHealth{}, Issues: []HealthIssue{},
	}
	byExchange := make(map[domain.Exchange]*ExchangeHealth)
	for _, pair := range markets {
		venue := byExchange[pair.Exchange]
		if venue == nil {
			venue = &ExchangeHealth{Exchange: pair.Exchange}
			byExchange[pair.Exchange] = venue
		}
		venue.RegisteredMarketCount++
		s.inspectHealth(pair, now, &result, venue)
	}
	result.UnavailableMarketCount = len(markets) - result.InitializedMarketCount
	result.InitializedMarketPct = percent(result.InitializedMarketCount, len(markets))
	for _, venue := range byExchange {
		venue.InitializedMarketPct = percent(venue.InitializedMarketCount, venue.RegisteredMarketCount)
		venue.FreshMarketPct = percent(venue.FreshMarketCount, venue.RegisteredMarketCount)
		result.Exchanges = append(result.Exchanges, *venue)
	}
	sort.Slice(result.Exchanges, func(i, j int) bool {
		return result.Exchanges[i].Exchange < result.Exchanges[j].Exchange
	})
	return result
}

func (s *Store) inspectHealth(
	pair domain.MarketPair,
	now time.Time,
	result *Health,
	venue *ExchangeHealth,
) {
	state := s.books[eventKey(pair.Exchange, pair.Symbol)]
	if state == nil {
		result.WaitingMarketCount++
		venue.WaitingMarketCount++
		venue.UnavailableMarketCount++
		result.Issues = append(result.Issues, healthIssue(pair, "WAITING", nil))
		return
	}
	if !state.initialized {
		venue.UnavailableMarketCount++
		status := "UNINITIALIZED"
		if state.sequenceGapCount > 0 {
			status = "SEQUENCE_GAP"
			result.SequenceGapMarketCount++
			venue.SequenceGapMarketCount++
		}
		result.Issues = append(result.Issues, healthIssue(pair, status, nil))
		return
	}
	result.InitializedMarketCount++
	venue.InitializedMarketCount++
	age := mathMax(0, float64(now.UnixMilli()-int64(state.lastReceivedTS))/1000)
	if age <= s.staleAfter.Seconds() {
		result.FreshMarketCount++
		venue.FreshMarketCount++
		return
	}
	result.StaleMarketCount++
	venue.StaleMarketCount++
	result.Issues = append(result.Issues, healthIssue(pair, "STALE", pointer(age)))
}

func healthIssue(pair domain.MarketPair, status string, age *float64) HealthIssue {
	return HealthIssue{
		Exchange: pair.Exchange, Market: pair.Symbol, Base: pair.Base,
		Quote: pair.Quote, Scope: pair.Scope, Status: status, AgeSec: age,
	}
}

func percent(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	return pointer(float64(numerator) / float64(denominator) * 100)
}

func mathMax(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
