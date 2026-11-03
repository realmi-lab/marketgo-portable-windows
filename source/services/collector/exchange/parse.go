package exchange

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	contract "market-share-go/contracts/collection/exchange"
	"math"
	"strconv"
	"strings"

	"market-share-go/internal/domain"
)

type MessageKind = contract.MessageKind

const (
	MessageOther    MessageKind = "OTHER"
	MessageSnapshot MessageKind = "SNAPSHOT"
	MessageTrade    MessageKind = "TRADE"
)

// ParsedMessage is the normalized result of decoding one websocket payload.
// Collectors use this path so message classification, subscription errors,
// trade parsing, order-book parsing, and expected-count accounting all share
// the exact same JSON document instead of decoding the payload repeatedly.
type ParsedMessage = contract.ParsedMessage

func decode(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("payload must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("payload must contain one JSON value")
		}
		return nil, fmt.Errorf("decode payload trailer: %w", err)
	}
	return result, nil
}

func MessageType(exchange domain.Exchange, payload []byte) (MessageKind, error) {
	row, err := decode(payload)
	if err != nil {
		return MessageOther, err
	}
	return messageType(exchange, row), nil
}

func ParseMessage(
	exchange domain.Exchange,
	payload []byte,
	universe domain.MarketUniverse,
	received domain.UnixMilli,
) (ParsedMessage, error) {
	if exchange == domain.ExchangeUpbit || exchange == domain.ExchangeBithumb {
		return parseDomesticMessage(exchange, payload, universe, received)
	}
	if domesticOrderBookPayloadCandidate(exchange, payload) {
		if message, handled, fastErr := parseDomesticOrderBookMessage(exchange, payload, universe, received); fastErr == nil && handled {
			return message, nil
		}
	}
	row, err := decode(payload)
	if err != nil {
		return ParsedMessage{}, err
	}
	if !exchange.Valid() {
		return ParsedMessage{}, fmt.Errorf("unsupported exchange %q", exchange)
	}
	result := ParsedMessage{Kind: messageType(exchange, row)}
	if message, rejected := subscriptionError(row); rejected {
		result.SubscriptionError = message
		return result, nil
	}
	if result.Kind == MessageTrade {
		parsed := parseTradeRows(exchange, row, universe, received)
		result.Trades = parsed.events
		result.ExpectedTradeEvents = parsed.expected
		result.ExpectedTradeMatches = parsed.expectedMatches
		result.EarliestTradeTS = parsed.earliest
		return result, nil
	}
	// A frame already classified as a trade or trade snapshot cannot also be
	// an order-book event. Only unclassified frames need the exchange-specific
	// order-book checks.
	if result.Kind == MessageOther {
		result.OrderBooks = parseOrderBooks(exchange, row, universe, received)
	}
	return result, nil
}

func messageType(exchange domain.Exchange, payload map[string]any) MessageKind {
	switch exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		if lower(firstString(payload, "type", "ty")) != "trade" {
			return MessageOther
		}
		if upper(firstString(payload, "stream_type", "st")) == "SNAPSHOT" {
			return MessageSnapshot
		}
		return MessageTrade
	case domain.ExchangeCoinone:
		if upper(firstString(payload, "response_type", "r")) == "DATA" && upper(firstString(payload, "channel", "c")) == "TRADE" {
			return MessageTrade
		}
	case domain.ExchangeKorbit:
		if lower(firstString(payload, "type")) != "trade" {
			return MessageOther
		}
		if value, _ := payload["snapshot"].(bool); value {
			return MessageSnapshot
		}
		return MessageTrade
	case domain.ExchangeGopax:
		if firstString(payload, "n") == "PublicTradeEvent" {
			return MessageTrade
		}
	case domain.ExchangeBinance:
		row := nestedMap(payload, "data")
		if row == nil {
			row = payload
		}
		if firstString(row, "e") == "trade" {
			return MessageTrade
		}
	case domain.ExchangeBybit:
		if strings.HasPrefix(firstString(payload, "topic"), "publicTrade.") {
			return MessageTrade
		}
	case domain.ExchangeOKX:
		// Successful/error control frames echo arg.channel but contain no trade
		// rows. Only data frames may enter trade accounting.
		if firstString(payload, "event") == "" && firstString(nestedMap(payload, "arg"), "channel") == "trades" {
			return MessageTrade
		}
	}
	return MessageOther
}

func ExpectedTradeEventCount(exchange domain.Exchange, payload []byte) (int, error) {
	row, err := decode(payload)
	if err != nil {
		return 0, err
	}
	return expectedTradeEventCount(exchange, row, messageType(exchange, row)), nil
}

func expectedTradeEventCount(exchange domain.Exchange, row map[string]any, kind MessageKind) int {
	if kind != MessageTrade {
		return 0
	}
	switch exchange {
	case domain.ExchangeKorbit, domain.ExchangeBybit, domain.ExchangeOKX:
		return rawTradeRowCount(row, "data")
	default:
		return 1
	}
}

func rawTradeRowCount(row map[string]any, field string) int {
	values, ok := row[field].([]any)
	if !ok || len(values) == 0 {
		// A payload classified as a trade must contain at least one raw event.
		// Treat a missing, non-array, or empty data field as one unparsed event
		// instead of silently certifying it as a zero-event message.
		return 1
	}
	return len(values)
}

func ParseTrades(exchange domain.Exchange, payload []byte, universe domain.MarketUniverse, received domain.UnixMilli) ([]domain.TradeEvent, error) {
	row, err := decode(payload)
	if err != nil {
		return nil, err
	}
	if !exchange.Valid() {
		return nil, fmt.Errorf("unsupported exchange %q", exchange)
	}
	if messageType(exchange, row) != MessageTrade {
		return nil, nil
	}
	return parseTradeRows(exchange, row, universe, received).events, nil
}

type tradeParseResult struct {
	events          []domain.TradeEvent
	expected        int
	expectedMatches uint64
	earliest        domain.UnixMilli
}

// parseTradeRows receives a row whose kind is already known to be MessageTrade.
// Besides avoiding a second classification pass, it collects the earliest raw
// event time while traversing batched exchange rows for parsing.
func parseTradeRows(exchange domain.Exchange, row map[string]any, universe domain.MarketUniverse, received domain.UnixMilli) tradeParseResult {
	result := tradeParseResult{
		events:   make([]domain.TradeEvent, 0, 1),
		expected: expectedTradeEventCount(exchange, row, MessageTrade),
	}
	result.expectedMatches = uint64(result.expected)
	observeEventTime := func(value any) domain.UnixMilli {
		parsed, ok := strictEpochMilli(value)
		if !ok {
			return 0
		}
		if result.earliest == 0 || parsed < result.earliest {
			result.earliest = parsed
		}
		return parsed
	}
	appendEvent := func(pair domain.MarketPair, tradeID, price, quantity any, eventTS domain.UnixMilli, side, quoteAmount, matchCount any) {
		event, ok := tradeEvent(pair, tradeID, price, quantity, eventTS, received, side, quoteAmount, matchCount)
		if ok {
			result.events = append(result.events, event)
		}
	}

	switch exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		eventTS := observeEventTime(first(row, "trade_timestamp", "ttms"))
		if pair, ok := universe.Find(exchange, firstString(row, "code", "cd")); ok {
			appendEvent(pair, first(row, "sequential_id", "sid"), first(row, "trade_price", "tp"), first(row, "trade_volume", "tv"), eventTS, first(row, "ask_bid", "ab"), nil, nil)
		}
	case domain.ExchangeCoinone:
		data := firstMap(row, "data", "d")
		eventTS := observeEventTime(first(data, "timestamp", "t"))
		symbol := upper(firstString(data, "quote_currency", "qc")) + "-" + upper(firstString(data, "target_currency", "tc"))
		if pair, ok := universe.Find(exchange, symbol); ok {
			side := ""
			if sellerMaker, found := optionalBool(data, "is_seller_maker", "sm"); found {
				side = "SELL"
				if sellerMaker {
					side = "BUY"
				}
			}
			appendEvent(pair, first(data, "id", "i"), first(data, "price", "p"), first(data, "qty", "q"), eventTS, side, nil, nil)
		}
	case domain.ExchangeKorbit:
		pair, ok := universe.Find(exchange, firstString(row, "symbol"))
		for _, value := range rawRows(row, "data") {
			data, rowOK := value.(map[string]any)
			if !rowOK {
				continue
			}
			eventTS := observeEventTime(data["timestamp"])
			if !ok {
				continue
			}
			side := ""
			if buyerTaker, found := optionalBool(data, "isBuyerTaker"); found {
				side = "SELL"
				if buyerTaker {
					side = "BUY"
				}
			}
			appendEvent(pair, data["tradeId"], data["price"], data["qty"], eventTS, side, nil, nil)
		}
	case domain.ExchangeGopax:
		data := firstMap(row, "o")
		eventTS := observeEventTime(data["occurredAt"])
		if pair, ok := universe.Find(exchange, firstString(data, "tradingPairName")); ok {
			side := ""
			if isBuy, found := optionalBool(data, "isBuy"); found {
				side = "SELL"
				if isBuy {
					side = "BUY"
				}
			}
			appendEvent(pair, data["tradeId"], data["price"], data["baseAmount"], eventTS, side, data["quoteAmount"], nil)
		}
	case domain.ExchangeBinance:
		data := nestedMap(row, "data")
		if data == nil {
			data = row
		}
		eventTS := observeEventTime(data["T"])
		if pair, ok := universe.Find(exchange, firstString(data, "s")); ok {
			side := ""
			if buyerMaker, found := optionalBool(data, "m"); found {
				side = "BUY"
				if buyerMaker {
					side = "SELL"
				}
			}
			appendEvent(pair, data["t"], data["p"], data["q"], eventTS, side, nil, nil)
		}
	case domain.ExchangeBybit:
		for _, value := range rawRows(row, "data") {
			data, ok := value.(map[string]any)
			if !ok {
				continue
			}
			eventTS := observeEventTime(first(data, "T"))
			if pair, ok := universe.Find(exchange, firstString(data, "s")); ok {
				appendEvent(pair, data["i"], data["p"], data["v"], eventTS, data["S"], nil, nil)
			}
		}
	case domain.ExchangeOKX:
		arg := nestedMap(row, "arg")
		result.expectedMatches = 0
		rows := rawRows(row, "data")
		if len(rows) == 0 {
			result.expectedMatches = uint64(result.expected)
		}
		for _, value := range rows {
			data, ok := value.(map[string]any)
			if !ok {
				result.expectedMatches = saturatingUint64Add(result.expectedMatches, 1)
				continue
			}
			eventTS := observeEventTime(data["ts"])
			matchCount := exchangeMatchCount(data["count"])
			expectedMatches := matchCount
			if matchCount == 0 || matchCount > math.MaxInt64 {
				expectedMatches = 1
			}
			result.expectedMatches = saturatingUint64Add(result.expectedMatches, expectedMatches)
			if matchCount == 0 || matchCount > math.MaxInt64 {
				// OKX trades are aggregate rows and count is the number of
				// underlying matches. Guessing one would undercount both volume
				// events and loss magnitude, so quarantine the entire raw row.
				continue
			}
			symbol := firstString(data, "instId")
			if symbol == "" {
				symbol = firstString(arg, "instId")
			}
			if pair, ok := universe.Find(exchange, symbol); ok {
				appendEvent(pair, data["tradeId"], data["px"], data["sz"], eventTS, data["side"], nil, matchCount)
			}
		}
	}
	return result
}

func saturatingUint64Add(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

func tradeEvent(pair domain.MarketPair, tradeID, price, quantity any, eventTime domain.UnixMilli, received domain.UnixMilli, side, quoteAmount, matchCount any) (domain.TradeEvent, bool) {
	identity := stringValue(tradeID)
	priceValue, priceOK := number(price)
	quantityValue, quantityOK := number(quantity)
	if identity == "" || !priceOK || !quantityOK || eventTime <= 0 || priceValue <= 0 || quantityValue <= 0 {
		return domain.TradeEvent{}, false
	}
	var officialQuote *float64
	if quoteAmount != nil {
		if value, ok := number(quoteAmount); ok && value > 0 {
			officialQuote = &value
		}
	}
	event := domain.TradeEvent{
		Exchange: pair.Exchange, Market: pair.Symbol, Base: pair.Base, Quote: pair.Quote,
		Scope: pair.Scope, TradeID: identity, Price: priceValue, Quantity: quantityValue,
		EventTS: eventTime, ReceivedTS: received,
		Side: normalizeTradeSide(side), QuoteAmount: officialQuote,
		MatchCount: exchangeMatchCount(matchCount),
	}
	if err := event.Validate(); err != nil {
		return domain.TradeEvent{}, false
	}
	return event, true
}

func normalizeTradeSide(value any) domain.TradeSide {
	switch upper(stringValue(value)) {
	case "BUY", "BID":
		return domain.TradeSideBuy
	case "SELL", "ASK":
		return domain.TradeSideSell
	default:
		return domain.TradeSideUnknown
	}
}

func first(row map[string]any, names ...string) any {
	for _, name := range names {
		if value, ok := row[name]; ok && value != nil && stringValue(value) != "" {
			return value
		}
	}
	return nil
}

func firstString(row map[string]any, names ...string) string {
	return stringValue(first(row, names...))
}
func firstMap(row map[string]any, names ...string) map[string]any {
	for _, name := range names {
		if value := nestedMap(row, name); value != nil {
			return value
		}
	}
	return map[string]any{}
}
func optionalBool(row map[string]any, names ...string) (bool, bool) {
	for _, name := range names {
		if value, ok := row[name]; ok {
			parsed, valid := value.(bool)
			return parsed, valid
		}
	}
	return false, false
}
func nestedMap(row map[string]any, name string) map[string]any {
	value, _ := row[name].(map[string]any)
	return value
}
func rawRows(row map[string]any, name string) []any {
	values, _ := row[name].([]any)
	return values
}
func nestedRows(row map[string]any, name string) []map[string]any {
	values := rawRows(row, name)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}
func number(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(stringValue(value), 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

// exchangeMatchCount returns zero for absent or malformed metadata. The
// domain-level EffectiveMatchCount method deliberately interprets zero as the
// backwards-compatible single-match default.
func exchangeMatchCount(value any) uint64 {
	if value == nil {
		return 0
	}
	parsed, err := strconv.ParseUint(stringValue(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0
	}
	return parsed
}
func integer(value any) *int64 {
	var result int64
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := strconv.ParseInt(typed.String(), 10, 64); err == nil {
			result = parsed
			return &result
		}
		return integerFloat(typed.String())
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return nil
		}
		result = parsed
		return &result
	case float64:
		return integerFromFloat(typed)
	case int:
		result = int64(typed)
		return &result
	case int64:
		result = typed
		return &result
	case int32:
		result = int64(typed)
		return &result
	case uint:
		if uint64(typed) > math.MaxInt64 {
			return nil
		}
		result = int64(typed)
		return &result
	case uint64:
		if typed > math.MaxInt64 {
			return nil
		}
		result = int64(typed)
		return &result
	default:
		return nil
	}
}

func integerFloat(value string) *int64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return integerFromFloat(parsed)
}

func integerFromFloat(value float64) *int64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value >= math.Exp2(63) || value < -math.Exp2(63) {
		return nil
	}
	result := int64(value)
	return &result
}
func epochMilli(value any, fallback domain.UnixMilli) domain.UnixMilli {
	parsed, ok := strictEpochMilli(value)
	if !ok {
		return fallback
	}
	return parsed
}

func strictEpochMilli(value any) (domain.UnixMilli, bool) {
	raw, ok := number(value)
	if !ok {
		return 0, false
	}
	if raw > 100_000_000_000_000 {
		raw /= 1000
	} else if raw < 100_000_000_000 {
		raw *= 1000
	}
	if raw <= 0 || raw > math.MaxInt64 || math.Trunc(raw) != raw {
		return 0, false
	}
	return domain.UnixMilli(raw), true
}

func subscriptionError(row map[string]any) (string, bool) {
	responseType := upper(stringValue(row["response_type"]))
	event := lower(stringValue(row["event"]))
	status := lower(stringValue(row["status"]))
	success, hasSuccess := row["success"].(bool)
	nestedError := nestedMap(row, "error")
	_, hasCode := row["code"]
	binanceError := hasCode && firstString(row, "msg", "message") != ""
	if nestedError == nil && (!hasSuccess || success) && event != "error" && responseType != "ERROR" &&
		status != "fail" && status != "error" && !binanceError {
		return "", false
	}
	if nestedError != nil {
		name := firstString(nestedError, "name", "code")
		message := firstString(nestedError, "message", "msg")
		if name != "" && message != "" {
			return name + ": " + message, true
		}
		if message != "" {
			return message, true
		}
		if name != "" {
			return name, true
		}
	}
	for _, key := range []string{"ret_msg", "msg", "message"} {
		if value := stringValue(row[key]); value != "" {
			return value, true
		}
	}
	encoded, _ := json.Marshal(row)
	return string(encoded), true
}
func upper(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }
func lower(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
