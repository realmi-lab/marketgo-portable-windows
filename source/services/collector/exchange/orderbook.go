package exchange

import (
	"strings"

	"market-share-go/internal/domain"
)

// DomesticOrderBookDepth is the complete in-process depth policy: ten bids
// and ten asks. Some venues only offer deeper wire snapshots, so parsers apply
// the cap even when a subscription cannot request it natively.
const DomesticOrderBookDepth = 10

func ParseOrderBooks(exchange domain.Exchange, payload []byte, universe domain.MarketUniverse, received domain.UnixMilli) ([]domain.OrderBookEvent, error) {
	if domesticOrderBookPayloadCandidate(exchange, payload) {
		if message, handled, fastErr := parseDomesticOrderBookMessage(exchange, payload, universe, received); fastErr == nil && handled {
			return message.OrderBooks, nil
		}
	}
	row, err := decode(payload)
	if err != nil {
		return nil, err
	}
	if !exchange.Valid() {
		return nil, nil
	}
	return parseOrderBooks(exchange, row, universe, received), nil
}

func parseOrderBooks(exchange domain.Exchange, row map[string]any, universe domain.MarketUniverse, received domain.UnixMilli) []domain.OrderBookEvent {
	events := make([]domain.OrderBookEvent, 0, 1)
	appendBook := func(pair domain.MarketPair, bids, asks []domain.OrderBookLevel, eventTS any, action domain.BookAction, sequence, previous any, depth *int) {
		if len(bids) == 0 && len(asks) == 0 && action != domain.BookActionDelta {
			return
		}
		if pair.Scope == domain.ScopeDomestic {
			bids = trimOrderBookLevels(bids, DomesticOrderBookDepth)
			asks = trimOrderBookLevels(asks, DomesticOrderBookDepth)
			retainedDepth := DomesticOrderBookDepth
			if depth != nil && *depth > 0 && *depth < retainedDepth {
				retainedDepth = *depth
			}
			depth = &retainedDepth
		}
		events = append(events, domain.OrderBookEvent{
			Exchange: pair.Exchange, Market: pair.Symbol, Base: pair.Base, Quote: pair.Quote,
			Scope: pair.Scope, Bids: bids, Asks: asks, EventTS: epochMilli(eventTS, received),
			ReceivedTS: received, Action: action, Sequence: integer(sequence),
			PreviousSequence: integer(previous), SourceDepth: depth,
		})
	}

	switch exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		if lower(firstString(row, "type", "ty")) != "orderbook" {
			return nil
		}
		pair, ok := universe.Find(exchange, firstString(row, "code", "cd"))
		if !ok {
			return nil
		}
		bids, asks := pairedLevels(first(row, "orderbook_units", "obu"))
		depth := 15
		appendBook(pair, bids, asks, first(row, "timestamp", "tms"), domain.BookActionSnapshot, nil, nil, &depth)
	case domain.ExchangeCoinone:
		if upper(firstString(row, "response_type", "r")) != "DATA" || upper(firstString(row, "channel", "c")) != "ORDERBOOK" {
			return nil
		}
		data := firstMap(row, "data", "d")
		symbol := upper(firstString(data, "quote_currency", "qc")) + "-" + upper(firstString(data, "target_currency", "tc"))
		if pair, ok := universe.Find(exchange, symbol); ok {
			appendBook(pair, levels(first(data, "bids", "b")), levels(first(data, "asks", "a")), first(data, "timestamp", "t"), domain.BookActionSnapshot, first(data, "id", "i"), nil, nil)
		}
	case domain.ExchangeKorbit:
		if lower(firstString(row, "type")) != "orderbook" {
			return nil
		}
		data := nestedMap(row, "data")
		if pair, ok := universe.Find(exchange, firstString(row, "symbol")); ok {
			depth := 30
			eventTS := first(data, "timestamp")
			if eventTS == nil {
				eventTS = first(row, "timestamp")
			}
			appendBook(pair, levels(data["bids"]), levels(data["asks"]), eventTS, domain.BookActionSnapshot, nil, nil, &depth)
		}
	case domain.ExchangeGopax:
		name := firstString(row, "n")
		if name != "SubscribeToOrderBook" && name != "SubscribeToTradingPair" && name != "OrderBookEvent" {
			return nil
		}
		data := nestedMap(row, "o")
		if pair, ok := universe.Find(exchange, firstString(data, "tradingPairName")); ok {
			bids := levels(data["bid"])
			asks := levels(data["ask"])
			sequence := first(data, "maxEntryId")
			if sequence == nil {
				sequence = highestUpdateID(bids, asks)
			}
			action := domain.BookActionSnapshot
			if name == "OrderBookEvent" {
				action = domain.BookActionDelta
			}
			depth := 20
			appendBook(pair, bids, asks, highestTimestamp(data["bid"], data["ask"], received), action, sequence, nil, &depth)
		}
	case domain.ExchangeBinance:
		data := nestedMap(row, "data")
		if data == nil {
			data = row
		}
		if _, ok := data["bids"].([]any); !ok {
			if _, short := data["b"].([]any); !short {
				return nil
			}
		}
		symbol := upper(firstString(data, "s"))
		if symbol == "" {
			symbol = upper(strings.Split(firstString(row, "stream"), "@")[0])
		}
		if pair, ok := universe.Find(exchange, symbol); ok {
			depth := 10
			appendBook(pair, levels(first(data, "bids", "b")), levels(first(data, "asks", "a")), first(data, "E", "T"), domain.BookActionSnapshot, first(data, "lastUpdateId", "u"), nil, &depth)
		}
	case domain.ExchangeBybit:
		topic := firstString(row, "topic")
		if !strings.HasPrefix(topic, "orderbook.") {
			return nil
		}
		data := nestedMap(row, "data")
		if pair, ok := universe.Find(exchange, firstString(data, "s")); ok {
			action := domain.BookAction(lower(firstString(row, "type")))
			if action != domain.BookActionDelta {
				action = domain.BookActionSnapshot
			}
			if sequence := integer(data["u"]); sequence != nil && *sequence == 1 {
				action = domain.BookActionSnapshot
			}
			var depth *int
			parts := strings.Split(topic, ".")
			if len(parts) >= 3 {
				if value := integer(parts[1]); value != nil && *value > 0 && uint64(*value) <= uint64(^uint(0)>>1) {
					parsed := int(*value)
					depth = &parsed
				}
			}
			appendBook(pair, levels(data["b"]), levels(data["a"]), first(row, "cts", "ts"), action, data["u"], nil, depth)
		}
	case domain.ExchangeOKX:
		arg := nestedMap(row, "arg")
		channel := firstString(arg, "channel")
		if channel != "books" && channel != "books-rpi" {
			return nil
		}
		pair, ok := universe.Find(exchange, firstString(arg, "instId"))
		if !ok {
			return nil
		}
		for _, data := range nestedRows(row, "data") {
			action := domain.BookActionDelta
			if firstString(row, "action") == "snapshot" {
				action = domain.BookActionSnapshot
			}
			depth := 400
			appendBook(pair, levels(data["bids"]), levels(data["asks"]), data["ts"], action, data["seqId"], data["prevSeqId"], &depth)
			break
		}
	}
	return events
}

func trimOrderBookLevels(levels []domain.OrderBookLevel, limit int) []domain.OrderBookLevel {
	if limit > 0 && len(levels) > limit {
		return levels[:limit:limit]
	}
	return levels
}

func levels(value any) []domain.OrderBookLevel {
	rows, _ := value.([]any)
	result := make([]domain.OrderBookLevel, 0, len(rows))
	for _, raw := range rows {
		var priceValue, quantityValue any
		var orderCount, updateID *int64
		switch row := raw.(type) {
		case []any:
			if len(row) < 2 {
				continue
			}
			priceValue, quantityValue = row[0], row[1]
			if len(row) >= 4 {
				orderCount = integer(row[3])
			}
		case map[string]any:
			priceValue = first(row, "price", "p")
			quantityValue = first(row, "qty", "q", "volume")
			orderCount = integer(first(row, "orderCount", "count"))
			updateID = integer(row["entryId"])
		default:
			continue
		}
		price, priceOK := number(priceValue)
		quantity, quantityOK := number(quantityValue)
		if !priceOK || !quantityOK || price <= 0 || quantity < 0 {
			continue
		}
		result = append(result, domain.OrderBookLevel{Price: price, Quantity: quantity, OrderCount: orderCount, UpdateID: updateID})
	}
	return result
}

func pairedLevels(value any) ([]domain.OrderBookLevel, []domain.OrderBookLevel) {
	rows, _ := value.([]any)
	bids := make([]domain.OrderBookLevel, 0, len(rows))
	asks := make([]domain.OrderBookLevel, 0, len(rows))
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if price, priceOK := number(first(row, "bid_price", "bp")); priceOK {
			if quantity, quantityOK := number(first(row, "bid_size", "bs")); quantityOK && price > 0 && quantity >= 0 {
				bids = append(bids, domain.OrderBookLevel{Price: price, Quantity: quantity})
			}
		}
		if price, priceOK := number(first(row, "ask_price", "ap")); priceOK {
			if quantity, quantityOK := number(first(row, "ask_size", "as")); quantityOK && price > 0 && quantity >= 0 {
				asks = append(asks, domain.OrderBookLevel{Price: price, Quantity: quantity})
			}
		}
	}
	return bids, asks
}

func highestUpdateID(sides ...[]domain.OrderBookLevel) any {
	var highest *int64
	for _, side := range sides {
		for _, level := range side {
			if level.UpdateID != nil && (highest == nil || *level.UpdateID > *highest) {
				value := *level.UpdateID
				highest = &value
			}
		}
	}
	if highest == nil {
		return nil
	}
	return *highest
}

func highestTimestamp(values ...any) any {
	var highest float64
	var fallback any
	for i, value := range values {
		if i == len(values)-1 {
			fallback = value
			break
		}
		rows, _ := value.([]any)
		for _, raw := range rows {
			row, _ := raw.(map[string]any)
			if parsed, ok := number(row["updatedAt"]); ok && parsed > highest {
				highest = parsed
			}
		}
	}
	if highest > 0 {
		return highest
	}
	return fallback
}
