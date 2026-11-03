// Package wscandle decodes native exchange candle evidence without inventing
// closed intervals from rolling ticker snapshots.
package wscandle

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"time"
)

// Candle retains decimal wire values without a binary floating point roundtrip.
// Closed requires source finality or independently established complete coverage;
// elapsed wall time alone does not establish it. BoundaryVerified only confirms
// that Start and End are actual, non-rolling candle boundaries.
type Candle struct {
	Exchange, Market, Asset, Quote                  string
	Interval                                        time.Duration
	Start, End                                      time.Time
	Open, High, Low, Close, BaseVolume, QuoteVolume string
	Closed, BoundaryVerified                        bool
	Source                                          string
	SourceTimestampMS                               int64
}

// Validate checks identity, boundaries and exact decimal OHLCV consistency.
func (c Candle) Validate() error { return validateCandle(c) }

var decimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

func decimal(s string) (*big.Rat, error) {
	if len(s) > 128 || !decimalPattern.MatchString(s) {
		return nil, fmt.Errorf("invalid nonnegative decimal %q", s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid decimal")
	}
	return r, nil
}
func validateCandle(c Candle) error {
	if c.Exchange == "" || c.Market == "" || c.Asset == "" || c.Quote == "" || c.Source == "" {
		return fmt.Errorf("missing candle identity")
	}
	if c.Interval <= 0 || c.Start.IsZero() || !c.End.Equal(c.Start.Add(c.Interval)) {
		return fmt.Errorf("invalid interval boundaries")
	}
	return validateValues(c.Open, c.High, c.Low, c.Close, c.BaseVolume, c.QuoteVolume)
}
func validateValues(o, h, l, c, v, q string) error {
	values := make([]*big.Rat, 6)
	for i, s := range []string{o, h, l, c, v, q} {
		r, e := decimal(s)
		if e != nil {
			return e
		}
		values[i] = r
	}
	if values[0].Sign() <= 0 || values[1].Sign() <= 0 || values[2].Sign() <= 0 || values[3].Sign() <= 0 {
		return fmt.Errorf("nonpositive price")
	}
	if values[1].Cmp(values[0]) < 0 || values[1].Cmp(values[3]) < 0 || values[2].Cmp(values[0]) > 0 || values[2].Cmp(values[3]) > 0 || values[1].Cmp(values[2]) < 0 {
		return fmt.Errorf("inconsistent OHLC")
	}
	return nil
}
func cmp(a, b string) int { x, _ := decimal(a); y, _ := decimal(b); return x.Cmp(y) }
func sameValues(a, b Candle) bool {
	for i, s := range []string{a.Open, a.High, a.Low, a.Close, a.BaseVolume, a.QuoteVolume} {
		if cmp(s, []string{b.Open, b.High, b.Low, b.Close, b.BaseVolume, b.QuoteVolume}[i]) != 0 {
			return false
		}
	}
	return true
}
func sumDecimal(values []string) string {
	sum := new(big.Rat)
	scale := 0
	for _, v := range values {
		r, _ := decimal(v)
		sum.Add(sum, r)
		for i := range v {
			if v[i] == '.' && len(v)-i-1 > scale {
				scale = len(v) - i - 1
			}
		}
	}
	return sum.FloatString(scale)
}

// Rollup24Hours accepts exactly 24 distinct consecutive aligned closed hours.
// Equivalent retries are ignored; conflicting retries, gaps, partial intervals,
// or rolling snapshots are errors. It never fills absent hours with zeroes.
func Rollup24Hours(input []Candle) (Candle, error) {
	var zero Candle
	if len(input) == 0 {
		return zero, fmt.Errorf("missing hourly candles")
	}
	first := input[0]
	unique := map[int64]Candle{}
	for _, c := range input {
		if e := validateCandle(c); e != nil {
			return zero, e
		}
		if c.Exchange != first.Exchange || c.Market != first.Market || c.Asset != first.Asset || c.Quote != first.Quote || c.Source != first.Source {
			return zero, fmt.Errorf("mixed candle identity or source")
		}
		if c.Interval != time.Hour || !c.Closed || !c.BoundaryVerified || !c.Start.Equal(c.Start.Truncate(time.Hour)) {
			return zero, fmt.Errorf("unverified or partial hourly interval")
		}
		key := c.Start.Unix()
		if old, ok := unique[key]; ok {
			if !sameValues(old, c) {
				return zero, fmt.Errorf("conflicting hourly retry")
			}
			continue
		}
		unique[key] = c
	}
	if len(unique) != 24 {
		return zero, fmt.Errorf("need exactly 24 distinct hours; got %d", len(unique))
	}
	hours := make([]Candle, 0, 24)
	for _, c := range unique {
		hours = append(hours, c)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Start.Before(hours[j].Start) })
	out := hours[0]
	out.Interval = 24 * time.Hour
	out.End = hours[23].End
	out.Close = hours[23].Close
	out.Source = "hourly_rollup:" + first.Source
	base, quote := make([]string, 0, 24), make([]string, 0, 24)
	for i, c := range hours {
		if i > 0 && !c.Start.Equal(hours[i-1].End) {
			return zero, fmt.Errorf("missing or overlapping hour")
		}
		if cmp(c.High, out.High) > 0 {
			out.High = c.High
		}
		if cmp(c.Low, out.Low) < 0 {
			out.Low = c.Low
		}
		base = append(base, c.BaseVolume)
		quote = append(quote, c.QuoteVolume)
	}
	out.BaseVolume = sumDecimal(base)
	out.QuoteVolume = sumDecimal(quote)
	return out, nil
}
