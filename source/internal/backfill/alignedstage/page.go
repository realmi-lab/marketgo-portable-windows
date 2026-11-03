// Package alignedstage retains official minute evidence for a frozen comparison
// cohort. It never publishes an endpoint or claims historical universe coverage.
package alignedstage

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
)

const MinuteMS int64 = 60000
const HourMS int64 = 60 * MinuteMS

type Page struct {
	StartMS int64                           `json:"start_ms"`
	EndMS   int64                           `json:"end_ms"`
	Candles map[int64]official.MinuteCandle `json:"candles"`
	Missing []int64                         `json:"missing_minutes"`
}

// DecodePage follows the existing minute-derived aligned-hour admission: only
// Upbit's documented no-trade omissions with a proven left boundary are zeros.
func DecodePage(m domain.MarketPair, raw []byte, start, end, windowStart int64, previous map[int64]official.MinuteCandle) (Page, error) {
	p := Page{StartMS: start, EndMS: end, Candles: map[int64]official.MinuteCandle{}, Missing: []int64{}}
	if start%MinuteMS != 0 || end%MinuteMS != 0 || end <= start || end-start > 199*MinuteMS {
		return p, errors.New("invalid minute page bounds")
	}
	candles, err := official.ParseCandles(m.Exchange, m.Symbol, raw)
	if err != nil {
		return p, err
	}
	if len(candles) > int((end-start)/MinuteMS+1) {
		return p, errors.New("response exceeds requested candle count")
	}
	oldest := end
	for _, c := range candles {
		if c.StartMS >= end || c.StartMS%MinuteMS != 0 {
			return p, errors.New("response crossed exclusive minute boundary")
		}
		if c.Exchange != m.Exchange || c.Market != m.Symbol || c.QuoteVolumeKRW == nil || c.NotionalMethod != official.ExactQuoteNotional {
			return p, errors.New("official exact quote identity missing")
		}
		if err := c.Validate(); err != nil {
			return p, err
		}
		oldest = min(oldest, c.StartMS)
		if c.StartMS < windowStart {
			continue
		}
		if old, ok := p.Candles[c.StartMS]; ok && !reflect.DeepEqual(old, c) {
			return p, fmt.Errorf("conflicting duplicate minute %d", c.StartMS)
		}
		if old, ok := previous[c.StartMS]; ok && !reflect.DeepEqual(old, c) {
			return p, fmt.Errorf("conflicting minute across pages %d", c.StartMS)
		}
		p.Candles[c.StartMS] = c
	}
	for minute := start; minute < end; minute += MinuteMS {
		if _, ok := p.Candles[minute]; ok {
			continue
		}
		if _, ok := previous[minute]; ok {
			return p, fmt.Errorf("conflicting minute omission %d", minute)
		}
		if m.Exchange == domain.ExchangeUpbit && oldest <= start {
			continue
		}
		p.Missing = append(p.Missing, minute)
	}
	return p, nil
}

type Hour struct {
	StartMS         int64    `json:"start_ms"`
	QuoteVolume     *float64 `json:"quote_volume"`
	ObservedMinutes int      `json:"observed_minutes"`
	MissingMinutes  int      `json:"missing_minutes"`
}

func Rollup(pages []Page, start, end int64) ([]Hour, error) {
	if start%HourMS != 0 || end-start != 24*HourMS {
		return nil, errors.New("rollup requires a fixed 24-hour window")
	}
	minutes := map[int64]official.MinuteCandle{}
	missing := map[int64]bool{}
	covered := map[int64]bool{}
	for _, p := range pages {
		for at := p.StartMS; at < p.EndMS; at += MinuteMS {
			if covered[at] {
				return nil, errors.New("overlapping page ownership")
			}
			covered[at] = true
		}
		for at, c := range p.Candles {
			if old, ok := minutes[at]; ok && !reflect.DeepEqual(old, c) {
				return nil, errors.New("rollup conflicting evidence")
			}
			minutes[at] = c
		}
		for _, at := range p.Missing {
			missing[at] = true
		}
	}
	result := make([]Hour, 0, 24)
	for at := start; at < end; at += HourMS {
		h := Hour{StartMS: at}
		total := 0.0
		for minute := at; minute < at+HourMS; minute += MinuteMS {
			if !covered[minute] || missing[minute] {
				h.MissingMinutes++
				continue
			}
			if c, ok := minutes[minute]; ok {
				h.ObservedMinutes++
				total += *c.QuoteVolumeKRW
			}
		}
		if math.IsNaN(total) || math.IsInf(total, 0) {
			return nil, errors.New("nonfinite official hour sum")
		}
		if h.MissingMinutes == 0 {
			value := total
			h.QuoteVolume = &value
		}
		result = append(result, h)
	}
	return result, nil
}
