package backfill

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/domain"
)

const (
	RawTradeSource               = "OFFICIAL_RAW_TRADE_REST_V1"
	CandleSource                 = "OFFICIAL_CANDLE_REST_V1"
	RawTradeBucketSource         = "OFFICIAL_RAW_TRADE_REST"
	CandleBucketSource           = "OFFICIAL_CANDLE_REST"
	RateFraction                 = 0.8
	OfficialReconciliationStatus = "OFFICIAL_RECONCILIATION"
)

func SourceForExchange(exchange domain.Exchange) (string, error) {
	switch exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeGopax:
		return RawTradeSource, nil
	case domain.ExchangeCoinone, domain.ExchangeKorbit:
		return CandleSource, nil
	default:
		return "", fmt.Errorf("unsupported historical backfill exchange: %s", exchange)
	}
}

var RequestsPerSecond = map[domain.Exchange]float64{
	domain.ExchangeUpbit: 8, domain.ExchangeBithumb: 120, domain.ExchangeGopax: 16,
	domain.ExchangeCoinone: 16, domain.ExchangeKorbit: 40,
}

var ErrCandleReplacement = errors.New("official candles are reconciliation evidence only; they cannot replace WebSocket trade buckets")

type ObservationGap struct {
	ID                int64
	Start             domain.UnixSecond
	End               domain.UnixSecond
	AffectedExchanges []domain.Exchange
	AffectedMarkets   []string
}

func (g ObservationGap) Validate() error {
	if g.ID <= 0 {
		return errors.New("gap ID must be positive")
	}
	if g.End < g.Start {
		return errors.New("gap end must not precede its start")
	}
	return nil
}

type HistoricalTrade struct {
	Exchange       domain.Exchange
	Market         string
	Base           string
	Quote          string
	EventSecond    domain.UnixSecond
	Price          float64
	Quantity       float64
	Side           domain.TradeSide
	SourceIdentity string
}

func (t HistoricalTrade) Validate() error {
	if !t.Exchange.Valid() || strings.TrimSpace(t.Market) == "" || strings.TrimSpace(t.Base) == "" || strings.ToUpper(t.Quote) != "KRW" {
		return errors.New("historical trade identity must be a direct KRW market")
	}
	if !positive(t.Price) || !positive(t.Quantity) || t.EventSecond <= 0 {
		return errors.New("historical trade values must be positive and finite")
	}
	if strings.TrimSpace(t.SourceIdentity) == "" {
		return errors.New("historical trade has no stable source identity")
	}
	return nil
}

func (t HistoricalTrade) KRWVolume() float64 { return t.Price * t.Quantity }

type HistoricalCandle struct {
	Exchange        domain.Exchange
	Market          string
	Base            string
	Quote           string
	Start           domain.UnixSecond
	DurationSeconds int64
	KRWVolume       float64
	BaseVolume      float64
	NotionalMethod  string
}

func (c HistoricalCandle) End() domain.UnixSecond {
	return c.Start + domain.UnixSecond(c.DurationSeconds) - 1
}

type Bucket struct {
	Second                   domain.UnixSecond
	Scope                    domain.Scope
	Exchange                 domain.Exchange
	Market                   string
	Base                     string
	Quote                    string
	NativeVolume             float64
	KRWVolume                float64
	TradeCount               int64
	UnresolvedCount          int64
	BuyKRWVolume             float64
	SellKRWVolume            float64
	BaseVolume               float64
	SourceGranularitySeconds int64
	SourceKind               string
	NotionalMethod           string
}

type FetchResult struct {
	Gap                        ObservationGap
	Exchange                   domain.Exchange
	Source                     string
	SourceGranularitySeconds   int64
	NotionalMethod             string
	Markets                    []domain.MarketPair
	Histories                  map[string][]HistoricalTrade
	Candles                    map[string][]HistoricalCandle
	CandleCoverage             map[string][2]domain.UnixSecond
	Failures                   map[string]string
	RegistryError              string
	RequestCount               int64
	EffectiveRequestsPerSecond float64
	RateReductions             int64
}

func (r FetchResult) SuccessfulMarkets() []string {
	seen := map[string]struct{}{}
	for key := range r.Histories {
		seen[key] = struct{}{}
	}
	for key := range r.Candles {
		seen[key] = struct{}{}
	}
	for key := range r.CandleCoverage {
		seen[key] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
func (r FetchResult) Status() string {
	if r.RegistryError != "" {
		return "FAILED_REGISTRY"
	}
	if len(r.SuccessfulMarkets()) == 0 {
		return "FAILED"
	}
	if len(r.Failures) > 0 {
		return "PARTIAL_CURRENT_REGISTRY"
	}
	return "APPLIED_CURRENT_REGISTRY"
}

type WriteResult struct {
	GapID                                                                            int64
	Exchange                                                                         domain.Exchange
	Status                                                                           string
	Skipped                                                                          bool
	MarketCount, SuccessfulMarketCount, FetchedTradeCount, BucketCount, RequestCount int64
	RecoveryStatus                                                                   string  `json:"recovery_status"`
	RecoveredGapIDs                                                                  []int64 `json:"recovered_gap_ids"`
	HistoricalUsed                                                                   bool    `json:"historical_used"`
	RestartRequired                                                                  bool    `json:"restart_required"`
}

const (
	RecoveryExactOfficialRawTrade = "EXACT_OFFICIAL_RAW_TRADE_RECOVERY"
	RecoveryNoExplicitMarkets     = "NOT_RECOVERED_NO_EXPLICIT_MARKET_SCOPE"
	RecoveryAmbiguousExchange     = "NOT_RECOVERED_AMBIGUOUS_EXCHANGE_SCOPE"
	RecoveryUnsupportedSource     = "NOT_RECOVERED_NON_EXACT_SOURCE"
	RecoveryMissingEvidence       = "NOT_RECOVERED_MISSING_EXACT_MARKET_EVIDENCE"
)

// RecoveryEvidence is the immutable proof needed to promote an official raw
// trade backfill into historical coverage. Current registry membership alone
// is intentionally insufficient: a market may have been delisted since the
// gap, so only the markets recorded on the original gap can be healed.
type RecoveryEvidence struct {
	Exchange                 domain.Exchange
	Source                   string
	Status                   string
	Start                    domain.UnixSecond
	End                      domain.UnixSecond
	SourceGranularitySeconds int64
	NotionalMethod           string
	SuccessfulMarkets        []string
}

type RecoveryDecision struct {
	Eligible bool
	Status   string
}

func RecoveryEvidenceFromFetch(result FetchResult) RecoveryEvidence {
	return RecoveryEvidence{
		Exchange: result.Exchange, Source: result.Source, Status: result.Status(),
		Start: result.Gap.Start, End: result.Gap.End,
		SourceGranularitySeconds: result.SourceGranularitySeconds,
		NotionalMethod:           result.NotionalMethod,
		SuccessfulMarkets:        result.SuccessfulMarkets(),
	}
}

// ExactRecoveryDecision fails closed. A single gap-wide segment can be
// recovered only when its persisted scope names one exchange and every
// affected market has exact, second-granularity official raw-trade evidence
// over the complete gap. Multi-exchange gaps remain open because the current
// schema does not bind each affected market to an exchange.
func ExactRecoveryDecision(gap ObservationGap, evidence []RecoveryEvidence) RecoveryDecision {
	if len(gap.AffectedMarkets) == 0 {
		return RecoveryDecision{Status: RecoveryNoExplicitMarkets}
	}
	if len(gap.AffectedExchanges) != 1 {
		return RecoveryDecision{Status: RecoveryAmbiguousExchange}
	}
	target := gap.AffectedExchanges[0]
	if source, err := SourceForExchange(target); err != nil || source != RawTradeSource {
		return RecoveryDecision{Status: RecoveryUnsupportedSource}
	}
	required := normalizedMarketSet(gap.AffectedMarkets)
	for _, row := range evidence {
		if row.Exchange != target || row.Source != RawTradeSource ||
			row.SourceGranularitySeconds != 1 || row.NotionalMethod != "EXACT_TRADE" ||
			row.Start != gap.Start || row.End != gap.End ||
			(row.Status != "APPLIED_CURRENT_REGISTRY" && row.Status != "PARTIAL_CURRENT_REGISTRY") {
			continue
		}
		successful := normalizedMarketSet(row.SuccessfulMarkets)
		complete := true
		for market := range required {
			if _, ok := successful[market]; !ok {
				complete = false
				break
			}
		}
		if complete {
			return RecoveryDecision{Eligible: true, Status: RecoveryExactOfficialRawTrade}
		}
	}
	return RecoveryDecision{Status: RecoveryMissingEvidence}
}

func normalizedMarketSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := strings.ToUpper(strings.TrimSpace(value)); normalized != "" {
			result[normalized] = struct{}{}
		}
	}
	return result
}

func AggregateTrades(trades []HistoricalTrade) ([]Bucket, error) {
	type key struct {
		second              domain.UnixSecond
		exchange            domain.Exchange
		market, base, quote string
	}
	totals := make(map[key]*Bucket)
	for _, trade := range trades {
		if err := trade.Validate(); err != nil {
			return nil, err
		}
		k := key{trade.EventSecond, trade.Exchange, trade.Market, strings.ToUpper(trade.Base), "KRW"}
		row := totals[k]
		if row == nil {
			row = &Bucket{Second: k.second, Scope: domain.ScopeDomestic, Exchange: k.exchange, Market: k.market, Base: k.base, Quote: k.quote, SourceGranularitySeconds: 1, SourceKind: RawTradeBucketSource, NotionalMethod: "EXACT_TRADE"}
			totals[k] = row
		}
		volume := trade.KRWVolume()
		row.NativeVolume += volume
		row.KRWVolume += volume
		row.TradeCount++
		row.BaseVolume += trade.Quantity
		if trade.Side == domain.TradeSideBuy {
			row.BuyKRWVolume += volume
		} else if trade.Side == domain.TradeSideSell {
			row.SellKRWVolume += volume
		}
	}
	rows := make([]Bucket, 0, len(totals))
	for _, row := range totals {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Second != rows[j].Second {
			return rows[i].Second < rows[j].Second
		}
		if rows[i].Exchange != rows[j].Exchange {
			return rows[i].Exchange < rows[j].Exchange
		}
		return rows[i].Market < rows[j].Market
	})
	return rows, nil
}

func AggregateCandles(candles []HistoricalCandle) ([]Bucket, error) {
	type key struct {
		second               domain.UnixSecond
		exchange             domain.Exchange
		market, base, method string
		duration             int64
	}
	totals := map[key]*Bucket{}
	for _, candle := range candles {
		if strings.ToUpper(candle.Quote) != "KRW" {
			return nil, errors.New("only direct KRW candles can be aggregated")
		}
		if candle.DurationSeconds < 1 || !nonnegative(candle.KRWVolume) || !nonnegative(candle.BaseVolume) {
			return nil, errors.New("historical candle values are invalid")
		}
		if candle.KRWVolume <= 0 && candle.BaseVolume <= 0 {
			continue
		}
		k := key{candle.Start, candle.Exchange, candle.Market, strings.ToUpper(candle.Base), candle.NotionalMethod, candle.DurationSeconds}
		row := totals[k]
		if row == nil {
			row = &Bucket{Second: k.second, Scope: domain.ScopeDomestic, Exchange: k.exchange, Market: k.market, Base: k.base, Quote: "KRW", SourceGranularitySeconds: k.duration, SourceKind: CandleBucketSource, NotionalMethod: k.method}
			totals[k] = row
		}
		row.NativeVolume += candle.KRWVolume
		row.KRWVolume += candle.KRWVolume
		row.BaseVolume += candle.BaseVolume
	}
	rows := make([]Bucket, 0, len(totals))
	for _, row := range totals {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Second < rows[j].Second })
	return rows, nil
}

func FullCandleRange(gap ObservationGap, duration int64) (domain.UnixSecond, domain.UnixSecond, bool) {
	if duration < 1 {
		return 0, 0, false
	}
	first := domain.UnixSecond((int64(gap.Start) + duration - 1) / duration * duration)
	last := domain.UnixSecond((int64(gap.End) - duration + 1) / duration * duration)
	return first, last, first <= last
}
func IntersectingCandleRange(gap ObservationGap, duration int64) (domain.UnixSecond, domain.UnixSecond) {
	return domain.UnixSecond(int64(gap.Start) / duration * duration), domain.UnixSecond(int64(gap.End) / duration * duration)
}
func CandleChunks(start, end domain.UnixSecond, duration int64, maxCandles int) [][2]domain.UnixSecond {
	if duration < 1 || maxCandles < 1 || end < start {
		return nil
	}
	span := domain.UnixSecond(duration * int64(maxCandles))
	result := make([][2]domain.UnixSecond, 0)
	for cursor := start; cursor <= end; {
		chunkEnd := cursor + span - domain.UnixSecond(duration)
		if chunkEnd > end {
			chunkEnd = end
		}
		result = append(result, [2]domain.UnixSecond{cursor, chunkEnd})
		cursor = chunkEnd + domain.UnixSecond(duration)
	}
	return result
}

func Detail(result FetchResult) map[string]any {
	decision := ExactRecoveryDecision(result.Gap, []RecoveryEvidence{RecoveryEvidenceFromFetch(result)})
	return map[string]any{"source": result.Source, "source_granularity_sec": result.SourceGranularitySeconds, "notional_method": result.NotionalMethod, "rate_fraction": RateFraction, "configured_requests_per_second": RequestsPerSecond[result.Exchange], "effective_requests_per_second": result.EffectiveRequestsPerSecond, "rate_reductions": result.RateReductions, "successful_markets": result.SuccessfulMarkets(), "failed_markets": result.Failures, "registry_error": result.RegistryError, "candle_coverage": result.CandleCoverage, "scope": "CURRENT_ACTIVE_DIRECT_KRW_MARKETS_ONLY", "gap_id": result.Gap.ID, "gap_start_second": result.Gap.Start, "gap_end_second": result.Gap.End, "affected_markets": result.Gap.AffectedMarkets, "recovery_status": decision.Status, "completed_at": time.Now().UTC()}
}

func ValidateFetchResult(result FetchResult) error {
	if err := result.Gap.Validate(); err != nil {
		return err
	}
	if _, ok := RequestsPerSecond[result.Exchange]; !ok {
		return fmt.Errorf("unsupported historical backfill exchange: %s", result.Exchange)
	}
	if result.Source == CandleSource {
		return ErrCandleReplacement
	}
	if result.Source != "" && result.Source != RawTradeSource {
		return fmt.Errorf("unsupported historical backfill source: %s", result.Source)
	}
	for market, history := range result.Histories {
		for _, trade := range history {
			if trade.Exchange != result.Exchange || trade.Market != market || trade.EventSecond < result.Gap.Start || trade.EventSecond > result.Gap.End {
				return errors.New("historical trade falls outside its exchange, market, or gap boundary")
			}
		}
	}
	return nil
}
func positive(v float64) bool    { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }
func nonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
