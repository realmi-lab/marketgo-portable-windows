package exchange

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"market-share-go/internal/domain"
)

func ParseRegistry(exchange domain.Exchange, payload []byte, quoteHint string) (domain.MarketUniverse, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return domain.MarketUniverse{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return domain.MarketUniverse{}, err
	}
	if !exchange.Valid() {
		return domain.MarketUniverse{}, fmt.Errorf("unsupported exchange %q", exchange)
	}
	pairs := make([]domain.MarketPair, 0)
	add := func(symbol, base, quote string, maker, taker *float64, feeSource string) {
		symbol = strings.TrimSpace(symbol)
		base = upper(base)
		quote = upper(quote)
		if symbol == "" || base == "" || quote == "" || base == quote {
			return
		}
		scope := domain.ScopeGlobal
		for _, domestic := range domain.DomesticExchanges {
			if exchange == domestic {
				scope = domain.ScopeDomestic
				break
			}
		}
		pairs = append(pairs, domain.MarketPair{
			Exchange: exchange, Symbol: symbol, Base: base, Quote: quote, Scope: scope,
			MakerFee: maker, TakerFee: taker, FeeSource: feeSource,
		})
	}

	object, _ := root.(map[string]any)
	switch exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		for _, row := range objectRows(root) {
			market := upper(firstString(row, "market"))
			quote, base, ok := strings.Cut(market, "-")
			if ok {
				add(market, base, quote, nil, nil, "")
			}
		}
	case domain.ExchangeCoinone:
		for _, row := range rowsValue(object["markets"]) {
			status, _ := number(row["trade_status"])
			if status != 1 && status != 2 && status != 3 {
				continue
			}
			quote := upper(firstString(row, "quote_currency"))
			if quote == "" {
				quote = upper(quoteHint)
			}
			base := upper(firstString(row, "target_currency"))
			add(quote+"-"+base, base, quote, nil, nil, "")
		}
	case domain.ExchangeKorbit:
		for _, row := range rowsValue(object["data"]) {
			if lower(firstString(row, "status")) != "launched" {
				continue
			}
			symbol := lower(firstString(row, "symbol"))
			base := firstString(row, "baseCurrency")
			quote := firstString(row, "quoteCurrency")
			if (base == "" || quote == "") && strings.Contains(symbol, "_") {
				parts := strings.Split(symbol, "_")
				base, quote = strings.Join(parts[:len(parts)-1], "_"), parts[len(parts)-1]
			}
			add(symbol, base, quote, nil, nil, "")
		}
	case domain.ExchangeGopax:
		for _, row := range objectRows(root) {
			add(firstString(row, "name"), firstString(row, "baseAsset"), firstString(row, "quoteAsset"), percentage(row["makerFeePercent"]), percentage(row["takerFeePercent"]), "OFFICIAL_TRADING_PAIR_API")
		}
	case domain.ExchangeBinance:
		for _, row := range rowsValue(object["symbols"]) {
			if firstString(row, "status") != "TRADING" {
				continue
			}
			if allowed, exists := row["isSpotTradingAllowed"].(bool); exists && !allowed {
				continue
			}
			add(firstString(row, "symbol"), firstString(row, "baseAsset"), firstString(row, "quoteAsset"), nil, nil, "")
		}
	case domain.ExchangeBybit:
		result := nestedMap(object, "result")
		for _, row := range rowsValue(result["list"]) {
			if lower(firstString(row, "status")) != "trading" {
				continue
			}
			add(firstString(row, "symbol"), firstString(row, "baseCoin"), firstString(row, "quoteCoin"), nil, nil, "")
		}
	case domain.ExchangeOKX:
		for _, row := range rowsValue(object["data"]) {
			if lower(firstString(row, "state")) != "live" {
				continue
			}
			add(firstString(row, "instId"), firstString(row, "baseCcy"), firstString(row, "quoteCcy"), nil, nil, "")
		}
	}
	return domain.NewMarketUniverse(pairs)
}

func percentage(value any) *float64 {
	parsed, ok := number(value)
	if !ok {
		return nil
	}
	parsed /= 100
	if parsed < 0 || parsed >= 1 {
		return nil
	}
	return &parsed
}

func objectRows(value any) []map[string]any {
	return rowsValue(value)
}

func rowsValue(value any) []map[string]any {
	values, _ := value.([]any)
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if row, ok := value.(map[string]any); ok {
			rows = append(rows, row)
		}
	}
	return rows
}
