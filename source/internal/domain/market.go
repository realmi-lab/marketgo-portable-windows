package domain

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type Exchange string

const (
	ExchangeUpbit   Exchange = "upbit"
	ExchangeBithumb Exchange = "bithumb"
	ExchangeCoinone Exchange = "coinone"
	ExchangeKorbit  Exchange = "korbit"
	ExchangeGopax   Exchange = "gopax"
	ExchangeBinance Exchange = "binance"
	ExchangeBybit   Exchange = "bybit"
	ExchangeOKX     Exchange = "okx"
)

var DomesticExchanges = [...]Exchange{
	ExchangeUpbit,
	ExchangeBithumb,
	ExchangeCoinone,
	ExchangeKorbit,
	ExchangeGopax,
}

var GlobalExchanges = [...]Exchange{
	ExchangeBinance,
	ExchangeBybit,
	ExchangeOKX,
}

// GlobalReferenceExchanges defines the exchanges allowed to contribute to
// public global market figures. GlobalExchanges remains the infrastructure
// list for optional collectors, while analysis is intentionally Binance Spot
// only.
var GlobalReferenceExchanges = [...]Exchange{
	ExchangeBinance,
}

func IsGlobalReferenceExchange(exchange Exchange) bool {
	return exchange == ExchangeBinance
}

func (e Exchange) Valid() bool {
	switch e {
	case ExchangeUpbit, ExchangeBithumb, ExchangeCoinone, ExchangeKorbit,
		ExchangeGopax, ExchangeBinance, ExchangeBybit, ExchangeOKX:
		return true
	default:
		return false
	}
}

func ParseExchange(value string) (Exchange, error) {
	exchange := Exchange(strings.ToLower(strings.TrimSpace(value)))
	if !exchange.Valid() {
		return "", fmt.Errorf("unsupported exchange %q", value)
	}
	return exchange, nil
}

type Scope string

const (
	ScopeDomestic Scope = "domestic"
	ScopeGlobal   Scope = "global"
)

func ScopeMatchesExchange(scope Scope, exchange Exchange) bool {
	if !exchange.Valid() || (scope != ScopeDomestic && scope != ScopeGlobal) {
		return false
	}
	return scopeForExchange(exchange) == scope
}

type MarketPair struct {
	Exchange  Exchange `json:"exchange"`
	Symbol    string   `json:"symbol"`
	Base      string   `json:"base"`
	Quote     string   `json:"quote"`
	Scope     Scope    `json:"scope"`
	MakerFee  *float64 `json:"maker_fee_rate"`
	TakerFee  *float64 `json:"taker_fee_rate"`
	FeeSource string   `json:"fee_source"`
}

func (m MarketPair) Key() string {
	return string(m.Exchange) + "\x00" + strings.ToUpper(m.Symbol)
}

func (m MarketPair) Validate() error {
	if !m.Exchange.Valid() {
		return errors.New("market pair has an unsupported exchange")
	}
	if strings.TrimSpace(m.Symbol) == "" || strings.TrimSpace(m.Base) == "" || strings.TrimSpace(m.Quote) == "" {
		return errors.New("market pair symbol, base, and quote are required")
	}
	if m.Scope != ScopeDomestic && m.Scope != ScopeGlobal {
		return fmt.Errorf("market pair has invalid scope %q", m.Scope)
	}
	if expected := scopeForExchange(m.Exchange); m.Scope != expected {
		return fmt.Errorf("market pair scope %q does not match %s scope %q", m.Scope, m.Exchange, expected)
	}
	for name, value := range map[string]*float64{"maker fee": m.MakerFee, "taker fee": m.TakerFee} {
		if value != nil && (!finite(*value) || *value < 0 || *value >= 1) {
			return fmt.Errorf("%s must be finite and in [0,1)", name)
		}
	}
	return nil
}

type MarketUniverse struct {
	markets []MarketPair
	byKey   map[string]MarketPair
}

func NewMarketUniverse(markets []MarketPair) (MarketUniverse, error) {
	byKey := make(map[string]MarketPair, len(markets))
	for _, market := range markets {
		market.Symbol = strings.TrimSpace(market.Symbol)
		market.Base = strings.ToUpper(strings.TrimSpace(market.Base))
		market.Quote = strings.ToUpper(strings.TrimSpace(market.Quote))
		if err := market.Validate(); err != nil {
			return MarketUniverse{}, err
		}
		key := market.Key()
		if previous, exists := byKey[key]; exists {
			if !sameMarketPair(previous, market) {
				return MarketUniverse{}, fmt.Errorf("conflicting duplicate market identity %s:%s", market.Exchange, market.Symbol)
			}
			continue
		}
		byKey[key] = cloneMarketPair(market)
	}
	normalized := make([]MarketPair, 0, len(byKey))
	for _, market := range byKey {
		normalized = append(normalized, market)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Exchange == normalized[j].Exchange {
			return normalized[i].Symbol < normalized[j].Symbol
		}
		return normalized[i].Exchange < normalized[j].Exchange
	})
	return MarketUniverse{markets: normalized, byKey: byKey}, nil
}

func sameMarketPair(left, right MarketPair) bool {
	return left.Exchange == right.Exchange && left.Symbol == right.Symbol &&
		left.Base == right.Base && left.Quote == right.Quote && left.Scope == right.Scope &&
		left.FeeSource == right.FeeSource && sameOptionalFloat(left.MakerFee, right.MakerFee) &&
		sameOptionalFloat(left.TakerFee, right.TakerFee)
}

func sameOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (u MarketUniverse) Markets() []MarketPair {
	result := make([]MarketPair, len(u.markets))
	for index, market := range u.markets {
		result[index] = cloneMarketPair(market)
	}
	return result
}

func (u MarketUniverse) Find(exchange Exchange, symbol string) (MarketPair, bool) {
	market, ok := u.byKey[string(exchange)+"\x00"+strings.ToUpper(strings.TrimSpace(symbol))]
	return cloneMarketPair(market), ok
}

func (u MarketUniverse) ForExchange(exchange Exchange) []MarketPair {
	result := make([]MarketPair, 0)
	for _, market := range u.markets {
		if market.Exchange == exchange {
			result = append(result, cloneMarketPair(market))
		}
	}
	return result
}

func cloneMarketPair(market MarketPair) MarketPair {
	if market.MakerFee != nil {
		value := *market.MakerFee
		market.MakerFee = &value
	}
	if market.TakerFee != nil {
		value := *market.TakerFee
		market.TakerFee = &value
	}
	return market
}

type UnixMilli int64
type UnixSecond int64

type TradeSide string

const (
	TradeSideUnknown TradeSide = ""
	TradeSideBuy     TradeSide = "BUY"
	TradeSideSell    TradeSide = "SELL"
)

type TradeEvent struct {
	Exchange    Exchange  `json:"exchange"`
	Market      string    `json:"market"`
	Base        string    `json:"base"`
	Quote       string    `json:"quote"`
	Scope       Scope     `json:"scope"`
	TradeID     string    `json:"trade_id"`
	Price       float64   `json:"price"`
	Quantity    float64   `json:"quantity"`
	EventTS     UnixMilli `json:"event_ts_ms"`
	ReceivedTS  UnixMilli `json:"received_ts_ms"`
	Side        TradeSide `json:"side"`
	QuoteAmount *float64  `json:"quote_amount"`
	// MatchCount records how many exchange matches an aggregated trade row
	// represents. It is collector-internal metadata and is intentionally kept
	// out of the public/replay JSON contract. Zero preserves compatibility with
	// callers that construct a single-match TradeEvent directly.
	MatchCount uint64 `json:"-"`
}

func (t TradeEvent) Validate() error {
	if !t.Exchange.Valid() {
		return errors.New("trade has an unsupported exchange")
	}
	if strings.TrimSpace(t.Market) == "" || strings.TrimSpace(t.Base) == "" || strings.TrimSpace(t.Quote) == "" {
		return errors.New("trade market, base, and quote are required")
	}
	if expected := scopeForExchange(t.Exchange); t.Scope != expected {
		return fmt.Errorf("trade scope %q does not match %s scope %q", t.Scope, t.Exchange, expected)
	}
	if strings.TrimSpace(t.TradeID) == "" {
		return errors.New("trade identity is required")
	}
	if !positiveFinite(t.Price) || !positiveFinite(t.Quantity) {
		return errors.New("trade price and quantity must be positive and finite")
	}
	if t.EventTS <= 0 || t.ReceivedTS <= 0 {
		return errors.New("trade timestamps must be positive milliseconds")
	}
	if t.Side != TradeSideUnknown && t.Side != TradeSideBuy && t.Side != TradeSideSell {
		return fmt.Errorf("invalid trade side %q", t.Side)
	}
	if t.QuoteAmount != nil && !positiveFinite(*t.QuoteAmount) {
		return errors.New("quote amount must be positive and finite when supplied")
	}
	if t.MatchCount > math.MaxInt64 {
		return errors.New("trade match count exceeds PostgreSQL BIGINT capacity")
	}
	return nil
}

func (t TradeEvent) NativeQuoteNotional() float64 {
	if t.QuoteAmount != nil && positiveFinite(*t.QuoteAmount) {
		return *t.QuoteAmount
	}
	return t.Price * t.Quantity
}

func (t TradeEvent) EffectiveMatchCount() uint64 {
	if t.MatchCount == 0 {
		return 1
	}
	return t.MatchCount
}

func (t TradeEvent) DedupeKey() string {
	return string(t.Exchange) + "\x00" + t.Market + "\x00" + t.TradeID
}

type OrderBookLevel struct {
	Price      float64 `json:"price"`
	Quantity   float64 `json:"quantity"`
	OrderCount *int64  `json:"order_count"`
	UpdateID   *int64  `json:"update_id"`
}

func (l OrderBookLevel) Validate() error {
	if !positiveFinite(l.Price) || !finite(l.Quantity) || l.Quantity < 0 {
		return errors.New("order-book level price must be positive and quantity must be nonnegative and finite")
	}
	if l.OrderCount != nil && *l.OrderCount < 0 {
		return errors.New("order count cannot be negative")
	}
	if !finite(l.Price * l.Quantity) {
		return errors.New("order-book level notional must be finite")
	}
	return nil
}

type BookAction string

const (
	BookActionSnapshot BookAction = "snapshot"
	BookActionDelta    BookAction = "delta"
)

type OrderBookEvent struct {
	Exchange         Exchange         `json:"exchange"`
	Market           string           `json:"market"`
	Base             string           `json:"base"`
	Quote            string           `json:"quote"`
	Scope            Scope            `json:"scope"`
	Bids             []OrderBookLevel `json:"bids"`
	Asks             []OrderBookLevel `json:"asks"`
	EventTS          UnixMilli        `json:"event_ts_ms"`
	ReceivedTS       UnixMilli        `json:"received_ts_ms"`
	Action           BookAction       `json:"action"`
	Sequence         *int64           `json:"sequence"`
	PreviousSequence *int64           `json:"previous_sequence"`
	SourceDepth      *int             `json:"source_depth"`
	FullDepth        bool             `json:"full_depth"`
}

func (e OrderBookEvent) Validate() error {
	if !e.Exchange.Valid() {
		return errors.New("order book has an unsupported exchange")
	}
	if strings.TrimSpace(e.Market) == "" || strings.TrimSpace(e.Base) == "" || strings.TrimSpace(e.Quote) == "" {
		return errors.New("order-book market, base, and quote are required")
	}
	if expected := scopeForExchange(e.Exchange); e.Scope != expected {
		return fmt.Errorf("order-book scope %q does not match %s scope %q", e.Scope, e.Exchange, expected)
	}
	if e.EventTS <= 0 || e.ReceivedTS <= 0 {
		return errors.New("order-book timestamps must be positive milliseconds")
	}
	if e.Action != BookActionSnapshot && e.Action != BookActionDelta {
		return fmt.Errorf("invalid order-book action %q", e.Action)
	}
	if len(e.Bids) == 0 && len(e.Asks) == 0 && e.Action != BookActionDelta {
		return errors.New("order book must contain at least one level")
	}
	for _, side := range [][]OrderBookLevel{e.Bids, e.Asks} {
		totalNotional := 0.0
		for _, level := range side {
			if err := level.Validate(); err != nil {
				return err
			}
			totalNotional += level.Price * level.Quantity
			if !finite(totalNotional) {
				return errors.New("order-book side notional must be finite")
			}
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func positiveFinite(value float64) bool {
	return finite(value) && value > 0
}

func scopeForExchange(exchange Exchange) Scope {
	switch exchange {
	case ExchangeUpbit, ExchangeBithumb, ExchangeCoinone, ExchangeKorbit, ExchangeGopax:
		return ScopeDomestic
	case ExchangeBinance, ExchangeBybit, ExchangeOKX:
		return ScopeGlobal
	default:
		return ""
	}
}
