package app

import (
	"context"
	"sort"
	"time"

	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/domain"
	"market-share-go/internal/orderbook"
)

type orderBookEvidence struct {
	Health        orderbook.Health
	Diagnostics   []map[string]any
	Quiet         map[string]bool
	StaleAfterSec float64
}

func (s *Service) orderBookEvidence(ctx context.Context, now time.Time, universe domain.MarketUniverse) (orderBookEvidence, error) {
	markets := orderBookMarkets(universe.Markets())
	activity := s.collectors.MarketActivitySnapshot(now)
	return s.orderBookEvidenceFor(ctx, now, markets, activity)
}

func (s *Service) orderBookEvidenceFor(ctx context.Context, now time.Time, markets []domain.MarketPair, activity map[string]collector.MarketActivity) (orderBookEvidence, error) {
	health, err := s.runtime.OrderBookHealth(ctx, markets, now)
	if err != nil {
		return orderBookEvidence{}, err
	}
	return buildOrderBookEvidence(health, activity, s.runtime.OrderBookStaleAfterSec()), nil
}

func buildOrderBookEvidence(health orderbook.Health, activity map[string]collector.MarketActivity, staleAfterSec float64) orderBookEvidence {
	diagnostics, quiet := diagnoseOrderBooks(health, activity, staleAfterSec)
	return orderBookEvidence{Health: health, Diagnostics: diagnostics, Quiet: quiet, StaleAfterSec: staleAfterSec}
}

func orderBookMarkets(markets []domain.MarketPair) []domain.MarketPair {
	result := make([]domain.MarketPair, 0, len(markets))
	for _, market := range markets {
		if market.Scope == domain.ScopeDomestic && isDomesticAnalysisExchange(market.Exchange) {
			result = append(result, market)
		}
	}
	return result
}

func quietOrderBookMarkets(activity map[string]collector.MarketActivity, threshold float64) map[string]bool {
	result := make(map[string]bool)
	for key, state := range activity {
		if state.Connected && !state.SubscriptionError && olderThan(state.OrderBookAgeSec, threshold) &&
			freshEnough(state.ChunkOrderBookAgeSec, threshold) && !freshEnough(state.TradeAgeSec, threshold) {
			result[key] = true
		}
	}
	return result
}

func diagnoseOrderBooks(health orderbook.Health, activity map[string]collector.MarketActivity, threshold float64) ([]map[string]any, map[string]bool) {
	issues := make(map[domain.Exchange][]map[string]any)
	quiet := make(map[string]bool)
	for _, issue := range health.Issues {
		state, found := activity[domain.MarketPair{Exchange: issue.Exchange, Symbol: issue.Market}.Key()]
		diagnosis := orderBookDiagnosis(issue, state, found, threshold)
		activityState := "STALE"
		if diagnosis == "LOW_ACTIVITY_CANDIDATE" {
			activityState = "QUIET"
			quiet[domain.MarketPair{Exchange: issue.Exchange, Symbol: issue.Market}.Key()] = true
		} else if diagnosis == "SEQUENCE_RECOVERY" {
			activityState = "GAP"
		} else if issue.Status != "STALE" {
			activityState = "UNINITIALIZED"
		}
		issues[issue.Exchange] = append(issues[issue.Exchange], map[string]any{
			"exchange": issue.Exchange, "market": issue.Market, "base": issue.Base,
			"quote": issue.Quote, "scope": issue.Scope, "status": issue.Status,
			"age_sec": issue.AgeSec, "diagnosis": diagnosis, "activity_state": activityState,
		})
	}
	result := make([]map[string]any, 0, len(health.Exchanges))
	for _, venue := range health.Exchanges {
		venueIssues := issues[venue.Exchange]
		if venueIssues == nil {
			venueIssues = []map[string]any{}
		}
		counts := make(map[string]int)
		for _, issue := range venueIssues {
			counts[issue["diagnosis"].(string)]++
		}
		result = append(result, map[string]any{
			"exchange": venue.Exchange, "registered_market_count": venue.RegisteredMarketCount,
			"initialized_market_count": venue.InitializedMarketCount, "fresh_market_count": venue.FreshMarketCount,
			"stale_market_count": venue.StaleMarketCount, "unavailable_market_count": venue.UnavailableMarketCount,
			"waiting_market_count": venue.WaitingMarketCount, "sequence_gap_market_count": venue.SequenceGapMarketCount,
			"initialized_market_pct": venue.InitializedMarketPct, "fresh_market_pct": venue.FreshMarketPct,
			"diagnoses": counts, "issues": venueIssues,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["exchange"].(domain.Exchange) < result[j]["exchange"].(domain.Exchange)
	})
	return result, quiet
}

func orderBookDiagnosis(issue orderbook.HealthIssue, activity collector.MarketActivity, found bool, threshold float64) string {
	if issue.Status == "SEQUENCE_GAP" {
		return "SEQUENCE_RECOVERY"
	}
	if !found || !activity.Connected {
		return "WS_DISCONNECTED"
	}
	if activity.SubscriptionError {
		return "SUBSCRIPTION_ERROR"
	}
	chunkBookFresh := freshEnough(activity.ChunkOrderBookAgeSec, threshold)
	chunkDataFresh := freshEnough(activity.ChunkDataAgeSec, threshold)
	if issue.Status != "STALE" || activity.OrderBookAgeSec == nil {
		if chunkBookFresh {
			return "SNAPSHOT_OR_SUBSCRIPTION_MISSING"
		}
		if chunkDataFresh {
			return "ORDERBOOK_SUBSCRIPTION_MISSING"
		}
		return "WS_STREAM_DELAY"
	}
	if freshEnough(activity.TradeAgeSec, threshold) {
		return "ORDERBOOK_DELAY_WHILE_TRADING"
	}
	if chunkBookFresh {
		return "LOW_ACTIVITY_CANDIDATE"
	}
	if chunkDataFresh {
		return "ORDERBOOK_STREAM_DELAY"
	}
	return "WS_DELAY_OR_INACTIVE_CHUNK"
}

func freshEnough(age *float64, threshold float64) bool { return age != nil && *age <= threshold }
func olderThan(age *float64, threshold float64) bool   { return age != nil && *age > threshold }

func liquidityQuality(evidence orderBookEvidence) map[string]any {
	registered, fresh, unavailable, stale, gaps, quiet := 0, 0, 0, 0, 0, 0
	for _, venue := range evidence.Health.Exchanges {
		if !domesticExchange(venue.Exchange) {
			continue
		}
		registered += venue.RegisteredMarketCount
		fresh += venue.FreshMarketCount
		unavailable += venue.UnavailableMarketCount
		stale += venue.StaleMarketCount
		gaps += venue.SequenceGapMarketCount
	}
	for _, venue := range evidence.Diagnostics {
		if domesticExchange(venue["exchange"].(domain.Exchange)) {
			quiet += venue["diagnoses"].(map[string]int)["LOW_ACTIVITY_CANDIDATE"]
		}
	}
	effectiveStale := max(0, stale-quiet)
	status := "HEALTHY"
	if unavailable > 0 || effectiveStale > 0 || gaps > 0 {
		status = "PARTIAL"
	}
	return map[string]any{
		"status": status, "registered_market_count": registered, "fresh_market_count": fresh,
		"unavailable_market_count": unavailable, "stale_market_count": stale,
		"quiet_market_count": quiet, "effective_stale_market_count": effectiveStale,
		"sequence_gap_market_count": gaps, "stale_after_sec": evidence.StaleAfterSec,
		"stale_books_excluded": true,
	}
}

func attachOrderBookQuality(quality map[string]any, evidence orderBookEvidence) {
	components, _ := quality["components"].(map[string]any)
	if components == nil {
		components = make(map[string]any)
		quality["components"] = components
	}
	components["liquidity"] = liquidityQuality(evidence)
	quality["orderbook_initialized_market_pct"] = evidence.Health.InitializedMarketPct
	quality["orderbook_unavailable_market_count"] = evidence.Health.UnavailableMarketCount
	quality["orderbook_stale_market_count"] = evidence.Health.StaleMarketCount
	quality["orderbook_sequence_gap_market_count"] = evidence.Health.SequenceGapMarketCount
	quality["orderbook_sequence_gap_count"] = evidence.Health.SequenceGaps
	quality["orderbook_health_by_exchange"] = evidence.Diagnostics
}

func domesticExchange(exchange domain.Exchange) bool {
	for _, candidate := range domain.DomesticExchanges {
		if candidate == exchange {
			return true
		}
	}
	return false
}
