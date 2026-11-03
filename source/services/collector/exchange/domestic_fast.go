package exchange

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"

	"market-share-go/internal/domain"
)

// domesticMessage covers the full and SIMPLE Upbit/Bithumb wire names. These
// two feeds dominate local frame volume, so decoding directly into fields
// avoids allocating a map entry and boxed interface value for every property.
type domesticMessage struct {
	Type       string         `json:"type"`
	SimpleType string         `json:"ty"`
	Code       string         `json:"code"`
	SimpleCode string         `json:"cd"`
	StreamType string         `json:"stream_type"`
	SimpleST   string         `json:"st"`
	TradePrice domesticNumber `json:"trade_price"`
	SimpleTP   domesticNumber `json:"tp"`
	TradeSize  domesticNumber `json:"trade_volume"`
	SimpleTV   domesticNumber `json:"tv"`
	TradeTime  domesticNumber `json:"trade_timestamp"`
	SimpleTTMS domesticNumber `json:"ttms"`
	TradeID    domesticScalar `json:"sequential_id"`
	SimpleSID  domesticScalar `json:"sid"`
	AskBid     string         `json:"ask_bid"`
	SimpleAB   string         `json:"ab"`
	Timestamp  domesticNumber `json:"timestamp"`
	SimpleTMS  domesticNumber `json:"tms"`
	Units      []domesticUnit `json:"orderbook_units"`
	SimpleOBU  []domesticUnit `json:"obu"`

	ResponseType string                `json:"response_type"`
	Event        string                `json:"event"`
	Status       string                `json:"status"`
	Success      *bool                 `json:"success"`
	Message      string                `json:"message"`
	Msg          string                `json:"msg"`
	RetMsg       string                `json:"ret_msg"`
	Error        *domesticMessageError `json:"error"`
}

type domesticMessageError struct {
	Name    domesticScalar `json:"name"`
	Code    domesticScalar `json:"code"`
	Message string         `json:"message"`
	Msg     string         `json:"msg"`
}

type domesticUnit struct {
	BidPrice domesticNumber `json:"bid_price"`
	BidSize  domesticNumber `json:"bid_size"`
	AskPrice domesticNumber `json:"ask_price"`
	AskSize  domesticNumber `json:"ask_size"`
	SimpleBP domesticNumber `json:"bp"`
	SimpleBS domesticNumber `json:"bs"`
	SimpleAP domesticNumber `json:"ap"`
	SimpleAS domesticNumber `json:"as"`
}

// domesticNumber accepts both JSON numbers and the quoted numeric fields used
// by some domestic SIMPLE payloads. It parses from the decoder-owned bytes so
// the order-book hot path does not allocate one RawMessage and one temporary
// string for every price and quantity.
type domesticNumber struct {
	value   float64
	present bool
	valid   bool
}

func (n *domesticNumber) UnmarshalJSON(payload []byte) error {
	n.value, n.present, n.valid = 0, false, false
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		return nil
	}
	if payload[0] == '"' {
		if len(payload) < 2 || payload[len(payload)-1] != '"' {
			return nil
		}
		quoted := payload[1 : len(payload)-1]
		if len(quoted) == 0 {
			return nil
		}
		n.present = true
		if bytes.IndexByte(quoted, '\\') >= 0 {
			var decoded string
			if json.Unmarshal(payload, &decoded) != nil {
				return nil
			}
			value, err := strconv.ParseFloat(decoded, 64)
			if err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
				n.value, n.valid = value, true
			}
			return nil
		}
		payload = quoted
	}
	if len(payload) == 0 {
		return nil
	}
	n.present = true
	value, err := strconv.ParseFloat(unsafe.String(unsafe.SliceData(payload), len(payload)), 64)
	if err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
		n.value, n.valid = value, true
	}
	return nil
}

type domesticScalar struct {
	value   string
	present bool
}

func (s *domesticScalar) UnmarshalJSON(payload []byte) error {
	s.value, s.present = "", false
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		return nil
	}
	if payload[0] == '"' {
		var decoded string
		if json.Unmarshal(payload, &decoded) != nil {
			return nil
		}
		if decoded == "" {
			return nil
		}
		s.present = true
		s.value = strings.TrimSpace(decoded)
		return nil
	}
	s.present = true
	s.value = string(payload)
	return nil
}

func parseDomesticMessage(
	exchange domain.Exchange,
	payload []byte,
	universe domain.MarketUniverse,
	received domain.UnixMilli,
) (ParsedMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ParsedMessage{}, fmt.Errorf("payload must be a JSON object")
	}
	var row domesticMessage
	if err := json.Unmarshal(trimmed, &row); err != nil {
		return ParsedMessage{}, err
	}
	if message, rejected := domesticSubscriptionError(row, trimmed); rejected {
		return ParsedMessage{SubscriptionError: message}, nil
	}

	messageType := row.Type
	if messageType == "" {
		messageType = row.SimpleType
	}
	if lower(messageType) == "trade" {
		streamType := row.StreamType
		if streamType == "" {
			streamType = row.SimpleST
		}
		if upper(streamType) == "SNAPSHOT" {
			return ParsedMessage{Kind: MessageSnapshot}, nil
		}
		result := ParsedMessage{Kind: MessageTrade, ExpectedTradeEvents: 1, ExpectedTradeMatches: 1}
		tradeTime := firstDomesticNumber(row.TradeTime, row.SimpleTTMS)
		result.EarliestTradeTS, _ = strictDomesticEpochMilli(tradeTime)
		code := row.Code
		if code == "" {
			code = row.SimpleCode
		}
		pair, ok := universe.Find(exchange, code)
		if !ok {
			return result, nil
		}
		tradeID := firstDomesticScalar(row.TradeID, row.SimpleSID)
		price, priceOK := domesticNumberValue(firstDomesticNumber(row.TradePrice, row.SimpleTP))
		quantity, quantityOK := domesticNumberValue(firstDomesticNumber(row.TradeSize, row.SimpleTV))
		if tradeID == "" || !priceOK || !quantityOK || result.EarliestTradeTS <= 0 || price <= 0 || quantity <= 0 {
			return result, nil
		}
		side := row.AskBid
		if side == "" {
			side = row.SimpleAB
		}
		event := domain.TradeEvent{
			Exchange: pair.Exchange, Market: pair.Symbol, Base: pair.Base, Quote: pair.Quote,
			Scope: pair.Scope, TradeID: tradeID, Price: price, Quantity: quantity,
			EventTS: result.EarliestTradeTS, ReceivedTS: received, Side: normalizeTradeSide(side),
		}
		if event.Validate() == nil {
			result.Trades = append(result.Trades, event)
		}
		return result, nil
	}

	result := ParsedMessage{Kind: MessageOther}
	if lower(messageType) != "orderbook" {
		return result, nil
	}
	code := row.Code
	if code == "" {
		code = row.SimpleCode
	}
	pair, ok := universe.Find(exchange, code)
	if !ok {
		return result, nil
	}
	units := row.Units
	if len(units) == 0 {
		units = row.SimpleOBU
	}
	capacity := min(len(units), DomesticOrderBookDepth)
	bids := make([]domain.OrderBookLevel, 0, capacity)
	asks := make([]domain.OrderBookLevel, 0, capacity)
	for _, unit := range units {
		if len(bids) >= DomesticOrderBookDepth && len(asks) >= DomesticOrderBookDepth {
			break
		}
		if price, priceOK := domesticNumberValue(firstDomesticNumber(unit.BidPrice, unit.SimpleBP)); priceOK && price > 0 {
			if quantity, quantityOK := domesticNumberValue(firstDomesticNumber(unit.BidSize, unit.SimpleBS)); len(bids) < DomesticOrderBookDepth && quantityOK && quantity >= 0 {
				bids = append(bids, domain.OrderBookLevel{Price: price, Quantity: quantity})
			}
		}
		if price, priceOK := domesticNumberValue(firstDomesticNumber(unit.AskPrice, unit.SimpleAP)); priceOK && price > 0 {
			if quantity, quantityOK := domesticNumberValue(firstDomesticNumber(unit.AskSize, unit.SimpleAS)); len(asks) < DomesticOrderBookDepth && quantityOK && quantity >= 0 {
				asks = append(asks, domain.OrderBookLevel{Price: price, Quantity: quantity})
			}
		}
	}
	if len(bids) == 0 && len(asks) == 0 {
		return result, nil
	}
	timestamp := firstDomesticNumber(row.Timestamp, row.SimpleTMS)
	eventTime, ok := strictDomesticEpochMilli(timestamp)
	if !ok {
		eventTime = received
	}
	depth := DomesticOrderBookDepth
	result.OrderBooks = append(result.OrderBooks, domain.OrderBookEvent{
		Exchange: pair.Exchange, Market: pair.Symbol, Base: pair.Base, Quote: pair.Quote,
		Scope: pair.Scope, Bids: bids, Asks: asks, EventTS: eventTime, ReceivedTS: received,
		Action: domain.BookActionSnapshot, SourceDepth: &depth,
	})
	return result, nil
}

func domesticSubscriptionError(row domesticMessage, raw []byte) (string, bool) {
	binanceStyleError := row.Code != "" && (row.Msg != "" || row.Message != "")
	if row.Error == nil && (row.Success == nil || *row.Success) && lower(row.Event) != "error" &&
		upper(row.ResponseType) != "ERROR" && lower(row.Status) != "fail" &&
		lower(row.Status) != "error" && !binanceStyleError {
		return "", false
	}
	if row.Error != nil {
		name := firstDomesticScalar(row.Error.Name, row.Error.Code)
		message := row.Error.Message
		if message == "" {
			message = row.Error.Msg
		}
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
	for _, message := range []string{row.RetMsg, row.Msg, row.Message} {
		if strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message), true
		}
	}
	return string(raw), true
}

func firstDomesticNumber(values ...domesticNumber) domesticNumber {
	for _, value := range values {
		if value.present {
			return value
		}
	}
	return domesticNumber{}
}

func domesticNumberValue(value domesticNumber) (float64, bool) {
	return value.value, value.valid
}

func firstDomesticScalar(values ...domesticScalar) string {
	for _, value := range values {
		if value.present {
			return value.value
		}
	}
	return ""
}

func strictDomesticEpochMilli(value domesticNumber) (domain.UnixMilli, bool) {
	if !value.valid {
		return 0, false
	}
	raw := value.value
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
