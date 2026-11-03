package exchange

import (
	"encoding/json"
	"fmt"
	"strings"

	"market-share-go/internal/domain"
)

var WebSocketURLs = map[domain.Exchange]string{
	domain.ExchangeUpbit:   "wss://api.upbit.com/websocket/v1",
	domain.ExchangeBithumb: "wss://ws-api.bithumb.com/websocket/v1",
	domain.ExchangeCoinone: "wss://stream.coinone.co.kr",
	domain.ExchangeKorbit:  "wss://ws-api.digitalx.miraeasset.com/v2/public",
	domain.ExchangeGopax:   "wss://wsapi.gopax.co.kr",
	domain.ExchangeBinance: "wss://stream.binance.com:9443/stream",
	domain.ExchangeBybit:   "wss://stream.bybit.com/v5/public/spot",
	domain.ExchangeOKX:     "wss://ws.okx.com:8443/ws/v5/public",
}

var ChunkSizes = map[domain.Exchange]int{
	domain.ExchangeUpbit: 200, domain.ExchangeBithumb: 200,
	domain.ExchangeCoinone: 100, domain.ExchangeKorbit: 200,
	domain.ExchangeGopax: 50, domain.ExchangeBinance: 300,
	domain.ExchangeBybit: 50, domain.ExchangeOKX: 25,
}

// GOPAX rejects an order-book limit below 20. The shared parser still trims
// the wire snapshot to DomesticOrderBookDepth before it enters runtime state.
const gopaxMinimumOrderBookDepth = 20

// SubscriptionPayloads returns protocol payloads for one already-bounded
// market chunk. It never truncates the supplied market list.
func SubscriptionPayloads(exchange domain.Exchange, markets []domain.MarketPair) ([][]byte, error) {
	payloads := make([]any, 0)
	symbols := make([]string, 0, len(markets))
	for _, market := range markets {
		if market.Exchange != exchange {
			return nil, fmt.Errorf("market %s belongs to %s, not %s", market.Symbol, market.Exchange, exchange)
		}
		symbols = append(symbols, market.Symbol)
	}
	switch exchange {
	case domain.ExchangeUpbit:
		books := make([]string, len(symbols))
		for i, symbol := range symbols {
			books[i] = symbol + ".15"
		}
		payloads = append(payloads, []any{
			map[string]any{"ticket": "market-share-upbit"},
			map[string]any{"type": "trade", "codes": symbols, "is_only_realtime": true},
			map[string]any{"type": "orderbook", "codes": books},
			map[string]any{"format": "SIMPLE"},
		})
	case domain.ExchangeBithumb:
		payloads = append(payloads, []any{
			map[string]any{"ticket": "market-share-bithumb"},
			map[string]any{"type": "trade", "codes": symbols, "isOnlyRealtime": true},
			map[string]any{"type": "orderbook", "codes": symbols},
			map[string]any{"format": "SIMPLE"},
		})
	case domain.ExchangeCoinone:
		for _, market := range markets {
			for _, channel := range []string{"TRADE", "ORDERBOOK"} {
				payloads = append(payloads, map[string]any{
					"request_type": "SUBSCRIBE", "channel": channel,
					"topic":  map[string]any{"quote_currency": market.Quote, "target_currency": market.Base},
					"format": "SHORT",
				})
			}
		}
	case domain.ExchangeKorbit:
		payloads = append(payloads, []any{
			map[string]any{"method": "subscribe", "type": "trade", "symbols": symbols},
			map[string]any{"method": "subscribe", "type": "orderbook", "symbols": symbols},
		})
	case domain.ExchangeGopax:
		for i, market := range markets {
			payloads = append(payloads, map[string]any{
				"i": i + 1, "n": "SubscribeToTradingPair",
				"o": map[string]any{
					"tradingPairName": market.Symbol,
					"limit":           max(DomesticOrderBookDepth, gopaxMinimumOrderBookDepth),
				},
			})
		}
	case domain.ExchangeBinance:
		streams := make([]string, len(symbols))
		for i, symbol := range symbols {
			streams[i] = strings.ToLower(symbol) + "@trade"
		}
		payloads = append(payloads, map[string]any{"method": "SUBSCRIBE", "params": streams, "id": 1})
	case domain.ExchangeBybit:
		topics := make([]string, 0, len(symbols)*2)
		for _, symbol := range symbols {
			topics = append(topics, "publicTrade."+symbol, "orderbook.50."+symbol)
		}
		for start := 0; start < len(topics); start += 10 {
			end := min(start+10, len(topics))
			payloads = append(payloads, map[string]any{"op": "subscribe", "args": topics[start:end]})
		}
	case domain.ExchangeOKX:
		args := make([]map[string]string, 0, len(symbols)*2)
		for _, symbol := range symbols {
			args = append(args,
				map[string]string{"channel": "trades", "instId": symbol},
				map[string]string{"channel": "books", "instId": symbol},
			)
		}
		payloads = append(payloads, map[string]any{"op": "subscribe", "args": args})
	default:
		return nil, fmt.Errorf("unsupported exchange %q", exchange)
	}
	result := make([][]byte, 0, len(payloads))
	for _, payload := range payloads {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		result = append(result, encoded)
	}
	return result, nil
}
