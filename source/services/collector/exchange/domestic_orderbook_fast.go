package exchange

import (
	"bytes"
	"encoding/json"
	"math"

	"market-share-go/internal/domain"
)

// The three feeds below previously crossed the generic map[string]any parser
// for every order-book frame. Candidate detection is only a routing hint: a
// shape mismatch falls back to the generic parser, preserving wire tolerance.
func domesticOrderBookPayloadCandidate(exchange domain.Exchange, payload []byte) bool {
	switch exchange {
	case domain.ExchangeCoinone:
		return bytes.Contains(payload, []byte("ORDERBOOK")) || bytes.Contains(payload, []byte("orderbook"))
	case domain.ExchangeKorbit:
		return bytes.Contains(payload, []byte("orderbook")) || bytes.Contains(payload, []byte("ORDERBOOK"))
	case domain.ExchangeGopax:
		return bytes.Contains(payload, []byte("SubscribeToOrderBook")) ||
			bytes.Contains(payload, []byte("SubscribeToTradingPair")) ||
			bytes.Contains(payload, []byte("OrderBookEvent"))
	default:
		return false
	}
}

type domesticBookLevel struct {
	Price      domesticNumber `json:"price"`
	ShortPrice domesticNumber `json:"p"`
	Quantity   domesticNumber `json:"qty"`
	ShortQty   domesticNumber `json:"q"`
	Volume     domesticNumber `json:"volume"`
	EntryID    domesticNumber `json:"entryId"`
	UpdatedAt  domesticNumber `json:"updatedAt"`
}

type coinoneBookData struct {
	Quote       string              `json:"quote_currency"`
	ShortQuote  string              `json:"qc"`
	Target      string              `json:"target_currency"`
	ShortTarget string              `json:"tc"`
	Timestamp   domesticNumber      `json:"timestamp"`
	ShortTime   domesticNumber      `json:"t"`
	ID          domesticNumber      `json:"id"`
	ShortID     domesticNumber      `json:"i"`
	Bids        []domesticBookLevel `json:"bids"`
	ShortBids   []domesticBookLevel `json:"b"`
	Asks        []domesticBookLevel `json:"asks"`
	ShortAsks   []domesticBookLevel `json:"a"`
}

type coinoneBookMessage struct {
	ResponseType string          `json:"response_type"`
	ShortType    string          `json:"r"`
	Channel      string          `json:"channel"`
	ShortChannel string          `json:"c"`
	Data         coinoneBookData `json:"data"`
	ShortData    coinoneBookData `json:"d"`
}

type korbitBookData struct {
	Timestamp domesticNumber      `json:"timestamp"`
	Bids      []domesticBookLevel `json:"bids"`
	Asks      []domesticBookLevel `json:"asks"`
}

type korbitBookMessage struct {
	Type      string         `json:"type"`
	Symbol    string         `json:"symbol"`
	Timestamp domesticNumber `json:"timestamp"`
	Data      korbitBookData `json:"data"`
}

type gopaxBookData struct {
	Market     string              `json:"tradingPairName"`
	MaxEntryID domesticNumber      `json:"maxEntryId"`
	Bids       []domesticBookLevel `json:"bid"`
	Asks       []domesticBookLevel `json:"ask"`
}

type gopaxBookMessage struct {
	Name string        `json:"n"`
	Data gopaxBookData `json:"o"`
}

func parseDomesticOrderBookMessage(
	exchange domain.Exchange,
	payload []byte,
	universe domain.MarketUniverse,
	received domain.UnixMilli,
) (ParsedMessage, bool, error) {
	switch exchange {
	case domain.ExchangeCoinone:
		return parseCoinoneOrderBookMessage(payload, universe, received)
	case domain.ExchangeKorbit:
		return parseKorbitOrderBookMessage(payload, universe, received)
	case domain.ExchangeGopax:
		return parseGopaxOrderBookMessage(payload, universe, received)
	default:
		return ParsedMessage{}, false, nil
	}
}

func parseCoinoneOrderBookMessage(payload []byte, universe domain.MarketUniverse, received domain.UnixMilli) (ParsedMessage, bool, error) {
	var row coinoneBookMessage
	if err := json.Unmarshal(payload, &row); err != nil {
		return ParsedMessage{}, false, err
	}
	responseType := row.ResponseType
	if responseType == "" {
		responseType = row.ShortType
	}
	channel := row.Channel
	if channel == "" {
		channel = row.ShortChannel
	}
	if upper(responseType) != "DATA" || upper(channel) != "ORDERBOOK" {
		return ParsedMessage{}, false, nil
	}
	data := row.Data
	if data.Quote == "" && data.ShortQuote == "" && data.Target == "" && data.ShortTarget == "" {
		data = row.ShortData
	}
	quote, target := data.Quote, data.Target
	if quote == "" {
		quote = data.ShortQuote
	}
	if target == "" {
		target = data.ShortTarget
	}
	pair, ok := universe.Find(domain.ExchangeCoinone, upper(quote)+"-"+upper(target))
	if !ok {
		return ParsedMessage{}, false, nil
	}
	bids := data.Bids
	if len(bids) == 0 {
		bids = data.ShortBids
	}
	asks := data.Asks
	if len(asks) == 0 {
		asks = data.ShortAsks
	}
	parsedBids, parsedAsks := typedDomesticBookSides(bids, asks, false)
	if len(parsedBids) == 0 && len(parsedAsks) == 0 {
		return ParsedMessage{}, false, nil
	}
	timestamp := firstDomesticNumber(data.Timestamp, data.ShortTime)
	sequence := domesticInteger(firstDomesticNumber(data.ID, data.ShortID))
	return domesticBookResult(pair, parsedBids, parsedAsks, timestamp, received, domain.BookActionSnapshot, sequence), true, nil
}

func parseKorbitOrderBookMessage(payload []byte, universe domain.MarketUniverse, received domain.UnixMilli) (ParsedMessage, bool, error) {
	var row korbitBookMessage
	if err := json.Unmarshal(payload, &row); err != nil {
		return ParsedMessage{}, false, err
	}
	if lower(row.Type) != "orderbook" {
		return ParsedMessage{}, false, nil
	}
	pair, ok := universe.Find(domain.ExchangeKorbit, row.Symbol)
	if !ok {
		return ParsedMessage{}, false, nil
	}
	bids, asks := typedDomesticBookSides(row.Data.Bids, row.Data.Asks, false)
	if len(bids) == 0 && len(asks) == 0 {
		return ParsedMessage{}, false, nil
	}
	timestamp := row.Data.Timestamp
	if !timestamp.present {
		timestamp = row.Timestamp
	}
	return domesticBookResult(pair, bids, asks, timestamp, received, domain.BookActionSnapshot, nil), true, nil
}

func parseGopaxOrderBookMessage(payload []byte, universe domain.MarketUniverse, received domain.UnixMilli) (ParsedMessage, bool, error) {
	var row gopaxBookMessage
	if err := json.Unmarshal(payload, &row); err != nil {
		return ParsedMessage{}, false, err
	}
	if row.Name != "SubscribeToOrderBook" && row.Name != "SubscribeToTradingPair" && row.Name != "OrderBookEvent" {
		return ParsedMessage{}, false, nil
	}
	pair, ok := universe.Find(domain.ExchangeGopax, row.Data.Market)
	if !ok {
		return ParsedMessage{}, false, nil
	}
	bids, asks := typedDomesticBookSides(row.Data.Bids, row.Data.Asks, true)
	if len(bids) == 0 && len(asks) == 0 && row.Name != "OrderBookEvent" {
		return ParsedMessage{}, false, nil
	}
	sequence := domesticInteger(row.Data.MaxEntryID)
	if sequence == nil {
		sequence = highestTypedUpdateID(bids, asks)
	}
	timestamp := highestTypedTimestamp(row.Data.Bids, row.Data.Asks)
	action := domain.BookActionSnapshot
	if row.Name == "OrderBookEvent" {
		action = domain.BookActionDelta
	}
	return domesticBookResult(pair, bids, asks, timestamp, received, action, sequence), true, nil
}

func typedDomesticBookSides(
	bidRows []domesticBookLevel,
	askRows []domesticBookLevel,
	withUpdateID bool,
) ([]domain.OrderBookLevel, []domain.OrderBookLevel) {
	bidCapacity := min(len(bidRows), DomesticOrderBookDepth)
	askCapacity := min(len(askRows), DomesticOrderBookDepth)
	storage := make([]domain.OrderBookLevel, bidCapacity+askCapacity)
	bids := storage[:0:bidCapacity]
	asks := storage[bidCapacity : bidCapacity : bidCapacity+askCapacity]
	bids = appendTypedDomesticLevels(bids, bidRows, withUpdateID)
	asks = appendTypedDomesticLevels(asks, askRows, withUpdateID)
	return bids, asks
}

func appendTypedDomesticLevels(
	result []domain.OrderBookLevel,
	rows []domesticBookLevel,
	withUpdateID bool,
) []domain.OrderBookLevel {
	for _, row := range rows {
		if len(result) >= DomesticOrderBookDepth {
			break
		}
		price := firstDomesticNumber(row.Price, row.ShortPrice)
		quantity := firstDomesticNumber(row.Quantity, row.ShortQty, row.Volume)
		if !price.valid || !quantity.valid || price.value <= 0 || quantity.value < 0 {
			continue
		}
		level := domain.OrderBookLevel{Price: price.value, Quantity: quantity.value}
		if withUpdateID {
			level.UpdateID = domesticInteger(row.EntryID)
		}
		result = append(result, level)
	}
	return result
}

func domesticBookResult(
	pair domain.MarketPair,
	bids, asks []domain.OrderBookLevel,
	timestamp domesticNumber,
	received domain.UnixMilli,
	action domain.BookAction,
	sequence *int64,
) ParsedMessage {
	eventTime, ok := strictDomesticEpochMilli(timestamp)
	if !ok {
		eventTime = received
	}
	depth := DomesticOrderBookDepth
	return ParsedMessage{Kind: MessageOther, OrderBooks: []domain.OrderBookEvent{{
		Exchange: pair.Exchange, Market: pair.Symbol, Base: pair.Base, Quote: pair.Quote,
		Scope: pair.Scope, Bids: bids, Asks: asks, EventTS: eventTime, ReceivedTS: received,
		Action: action, Sequence: sequence, SourceDepth: &depth,
	}}}
}

func domesticInteger(value domesticNumber) *int64 {
	if !value.valid || value.value < math.MinInt64 || value.value >= math.Exp2(63) || math.Trunc(value.value) != value.value {
		return nil
	}
	result := int64(value.value)
	return &result
}

func highestTypedUpdateID(sides ...[]domain.OrderBookLevel) *int64 {
	var highest *int64
	for _, side := range sides {
		for _, level := range side {
			if level.UpdateID != nil && (highest == nil || *level.UpdateID > *highest) {
				value := *level.UpdateID
				highest = &value
			}
		}
	}
	return highest
}

func highestTypedTimestamp(sides ...[]domesticBookLevel) domesticNumber {
	var highest domesticNumber
	for _, side := range sides {
		for _, level := range side {
			if level.UpdatedAt.valid && (!highest.valid || level.UpdatedAt.value > highest.value) {
				highest = level.UpdatedAt
			}
		}
	}
	return highest
}
