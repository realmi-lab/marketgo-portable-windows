package official

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"market-share-go/internal/domain"
)

const (
	CoinonePrecisionMethodPrefix   = "COINONE_SINGLE_TRADE_QUOTE_TRUNCATION_V1:"
	CoinonePrecisionSource         = "COINONE_OFFICIAL_MINUTE_CANDLE_REST"
	MaxCoinonePrecisionMethodBytes = 2048
)

// CoinoneDecimalEvidence preserves API NumberStrings, not decimals inferred
// from stored floats. Its versioned encoding fits the existing notional_method
// TEXT column without changing the legacy EXACT_CANDLE_QUOTE_VOLUME contract.
// The policy proves one observed case; it does not assume an exchange-wide
// rounding rule or a per-trade error allowance for multi-trade candles.
type CoinoneDecimalEvidence struct {
	Version       int             `json:"version"`
	Exchange      domain.Exchange `json:"exchange"`
	Market        string          `json:"market"`
	Base          string          `json:"base"`
	Quote         string          `json:"quote"`
	MinuteStartMS int64           `json:"minute_start_ms"`
	Source        string          `json:"source"`
	Open          string          `json:"open"`
	High          string          `json:"high"`
	Low           string          `json:"low"`
	Close         string          `json:"close"`
	TargetVolume  string          `json:"target_volume"`
	QuoteVolume   string          `json:"quote_volume"`
}

func coinoneDecimalEvidence(market string, start int64, row map[string]any) *CoinoneDecimalEvidence {
	base, quote, err := krwMarketParts(market)
	if err != nil {
		return nil
	}
	e := CoinoneDecimalEvidence{Version: 1, Exchange: domain.ExchangeCoinone, Market: market, Base: base, Quote: quote, MinuteStartMS: start, Source: CoinonePrecisionSource}
	fields := []struct {
		name   string
		target *string
	}{{"open", &e.Open}, {"high", &e.High}, {"low", &e.Low}, {"close", &e.Close}, {"target_volume", &e.TargetVolume}, {"quote_volume", &e.QuoteVolume}}
	for _, field := range fields {
		value, ok := row[field.name].(string)
		if !ok {
			return nil
		}
		*field.target = value
	}
	if e.Validate() != nil {
		return nil
	}
	return &e
}

func decimalRat(value string) (*big.Rat, error) {
	if len(value) == 0 || len(value) > 64 {
		return nil, errors.New("invalid decimal length")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 1 && parts[0][0] == '0' {
		return nil, errors.New("invalid decimal integer")
	}
	for _, part := range parts {
		if len(part) == 0 {
			return nil, errors.New("empty decimal component")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return nil, errors.New("non-decimal source value")
			}
		}
	}
	r, ok := new(big.Rat).SetString(value)
	f, err := strconv.ParseFloat(value, 64)
	if !ok || err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, errors.New("unrepresentable decimal")
	}
	return r, nil
}

func (e CoinoneDecimalEvidence) Validate() error {
	base, quote, err := krwMarketParts(e.Market)
	if err != nil || e.Version != 1 || e.Exchange != domain.ExchangeCoinone || e.Source != CoinonePrecisionSource || e.Market != "KRW-"+base || e.Base != base || e.Quote != quote || e.MinuteStartMS <= 0 || e.MinuteStartMS%MinuteMS != 0 {
		return errors.New("invalid Coinone precision identity")
	}
	for _, raw := range []string{e.Open, e.High, e.Low, e.Close, e.TargetVolume, e.QuoteVolume} {
		if _, err := decimalRat(raw); err != nil {
			return err
		}
	}
	return nil
}

func (e CoinoneDecimalEvidence) Encode() (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	method := CoinonePrecisionMethodPrefix + string(data)
	if len(method) > MaxCoinonePrecisionMethodBytes {
		return "", errors.New("precision evidence exceeds size limit")
	}
	return method, nil
}

func DecodeCoinonePrecisionMethod(method string) (CoinoneDecimalEvidence, error) {
	var e CoinoneDecimalEvidence
	if !strings.HasPrefix(method, CoinonePrecisionMethodPrefix) || len(method) > MaxCoinonePrecisionMethodBytes {
		return e, errors.New("unsupported precision method")
	}
	raw := strings.TrimPrefix(method, CoinonePrecisionMethodPrefix)
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return e, err
	}
	canonical, err := e.Encode()
	if err != nil || canonical != method {
		return e, errors.New("noncanonical precision evidence")
	}
	return e, nil
}

func (e CoinoneDecimalEvidence) Values() (price, base, quote float64) {
	price, _ = strconv.ParseFloat(e.Open, 64)
	base, _ = strconv.ParseFloat(e.TargetVolume, 64)
	quote, _ = strconv.ParseFloat(e.QuoteVolume, 64)
	return
}

// ProvesSingleTrade requires exact observed identity/amount equality, then
// proves decimal truncation using rationals. No tolerance is added. Four
// fractional digits must be present in this raw source value: coarse values,
// missing raw strings, multi-trade totals, and different currencies fail.
func (e CoinoneDecimalEvidence) ProvesSingleTrade(native, krw, base float64, trades, unresolved int64) bool {
	if e.Validate() != nil || trades != 1 || unresolved != 0 {
		return false
	}
	parts := strings.Split(e.QuoteVolume, ".")
	if len(parts) != 2 || len(parts[1]) != 4 {
		return false
	}
	p, _ := decimalRat(e.Open)
	q, _ := decimalRat(e.QuoteVolume)
	b, _ := decimalRat(e.TargetVolume)
	if p.Sign() <= 0 || q.Sign() <= 0 || b.Sign() <= 0 {
		return false
	}
	for _, raw := range []string{e.High, e.Low, e.Close} {
		r, _ := decimalRat(raw)
		if r.Cmp(p) != 0 {
			return false
		}
	}
	price, quantity, reported := e.Values()
	// The persisted WS numbers have no original decimal strings. Admit only
	// raw decimals equal to their shortest round-trip decimal value, not an
	// arbitrarily finer value that happens to round to the same float.
	priceRoundTrip, priceErr := decimalRat(strconv.FormatFloat(price, 'f', -1, 64))
	baseRoundTrip, baseErr := decimalRat(strconv.FormatFloat(base, 'f', -1, 64))
	if priceErr != nil || baseErr != nil || p.Cmp(priceRoundTrip) != 0 || b.Cmp(baseRoundTrip) != 0 {
		return false
	}
	if base != quantity || native != price*quantity || krw != native || !(krw > reported) {
		return false
	}
	product := new(big.Rat).Mul(p, b)
	if product.Cmp(q) <= 0 {
		return false
	}
	scaled := new(big.Rat).Mul(product, big.NewRat(10000, 1))
	floor := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	return new(big.Rat).SetFrac(floor, big.NewInt(10000)).Cmp(q) == 0
}
