package wscandle

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const BithumbLegacyURL = "wss://pubwss.bithumb.com/pub/ws"

// ErrBithumbUnpriced carries a validated identity and zero-activity observation,
// never a priced candle or proof of interval completeness. Validate still rejects it.
var ErrBithumbUnpriced = errors.New("unpriced zero-activity Bithumb ticker")

// BithumbTicker is a period ticker observation, not a finalized candle.
// Official legacy docs expose no period start/end, final flag or timezone for
// date/time. These fields remain verbatim, and must not be truncated into hours.
// https://apidocs.bithumb.com/v1.2.0/reference/빗썸-거래소-정보-수신
type BithumbTicker struct {
	Symbol, Asset, Quote, TickType, SourceDate, SourceTime string
	Open, High, Low, Close, BaseVolume, QuoteVolume        string
	Interval                                               time.Duration
}

// Validate verifies a manually constructed observation at persistence boundaries.
func (t BithumbTicker) Validate() error {
	asset, quote, err := splitSymbol(t.Symbol)
	if err != nil || asset != t.Asset || quote != t.Quote {
		return fmt.Errorf("invalid ticker identity")
	}
	duration, ok := map[string]time.Duration{"30M": 30 * time.Minute, "1H": time.Hour, "12H": 12 * time.Hour, "24H": 24 * time.Hour, "MID": 0}[t.TickType]
	if !ok || duration != t.Interval {
		return fmt.Errorf("invalid ticker interval")
	}
	if _, err = time.Parse("20060102150405", t.SourceDate+t.SourceTime); err != nil || len(t.SourceDate) != 8 || len(t.SourceTime) != 6 {
		return fmt.Errorf("invalid source date/time")
	}
	return validateValues(t.Open, t.High, t.Low, t.Close, t.BaseVolume, t.QuoteVolume)
}

func splitSymbol(s string) (string, string, error) {
	p := strings.Split(s, "_")
	if len(p) != 2 || p[0] == "" || p[1] == "" {
		return "", "", fmt.Errorf("invalid symbol")
	}
	for _, part := range p {
		for _, r := range part {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return "", "", fmt.Errorf("invalid symbol")
			}
		}
	}
	return p[0], p[1], nil
}

// BithumbHourlySubscription requests the native one-hour ticker only. Legacy
// ticker has no 1m or 10m type; 24h must come from verified hourly projections.
func BithumbHourlySubscription(symbols []string) ([]byte, error) {
	if len(symbols) == 0 {
		return nil, fmt.Errorf("no symbols")
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if _, _, e := splitSymbol(s); e != nil {
			return nil, e
		}
		if !seen[s] {
			clean = append(clean, s)
			seen[s] = true
		}
	}
	return json.Marshal(struct {
		Type      string   `json:"type"`
		Symbols   []string `json:"symbols"`
		TickTypes []string `json:"tickTypes"`
	}{"ticker", clean, []string{"1H"}})
}
func ParseBithumbTicker(raw []byte) (BithumbTicker, error) {
	var zero BithumbTicker
	if !utf8.Valid(raw) {
		return zero, fmt.Errorf("invalid UTF-8 ticker")
	}
	var m struct {
		Type    string `json:"type"`
		Content struct {
			Symbol   string          `json:"symbol"`
			TickType string          `json:"tickType"`
			Date     string          `json:"date"`
			Time     string          `json:"time"`
			Open     string          `json:"openPrice"`
			High     string          `json:"highPrice"`
			Low      string          `json:"lowPrice"`
			Close    string          `json:"closePrice"`
			Base     string          `json:"volume"`
			Quote    string          `json:"value"`
			Buy      json.RawMessage `json:"buyVolume"`
			Sell     json.RawMessage `json:"sellVolume"`
		} `json:"content"`
	}
	if e := json.Unmarshal(raw, &m); e != nil {
		return zero, e
	}
	if m.Type != "ticker" {
		return zero, fmt.Errorf("not a ticker")
	}
	c := m.Content
	asset, quote, e := splitSymbol(c.Symbol)
	if e != nil {
		return zero, e
	}
	duration, ok := map[string]time.Duration{"30M": 30 * time.Minute, "1H": time.Hour, "12H": 12 * time.Hour, "24H": 24 * time.Hour, "MID": 0}[c.TickType]
	if !ok {
		return zero, fmt.Errorf("unsupported tick type")
	}
	if _, e = time.Parse("20060102150405", c.Date+c.Time); e != nil || len(c.Date) != 8 || len(c.Time) != 6 {
		return zero, fmt.Errorf("invalid source date/time")
	}
	ticker := BithumbTicker{Symbol: c.Symbol, Asset: asset, Quote: quote, TickType: c.TickType, SourceDate: c.Date, SourceTime: c.Time, Open: c.Open, High: c.High, Low: c.Low, Close: c.Close, BaseVolume: c.Base, QuoteVolume: c.Quote, Interval: duration}
	if e = validateValues(c.Open, c.High, c.Low, c.Close, c.Base, c.Quote); e != nil {
		allZero := true
		for _, value := range []string{c.Open, c.High, c.Low, c.Close, c.Base, c.Quote} {
			number, err := decimal(value)
			if err != nil || number.Sign() != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			for _, rawVolume := range []json.RawMessage{c.Buy, c.Sell} {
				if len(rawVolume) == 0 {
					continue
				}
				var value string
				if err := json.Unmarshal(rawVolume, &value); err != nil {
					return zero, fmt.Errorf("invalid unpriced activity")
				}
				number, err := decimal(value)
				if err != nil || number.Sign() != 0 {
					return zero, fmt.Errorf("invalid unpriced activity")
				}
			}
			return ticker, ErrBithumbUnpriced
		}
		return zero, e
	}
	return ticker, nil
}
