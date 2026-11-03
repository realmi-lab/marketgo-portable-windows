package official

import (
	"market-share-go/internal/domain"
	"sort"
)

// CachedAlignedHour is an already validated official KRW candle. Reading this
// snapshot never fetches history, repairs gaps, or changes collection state.
type CachedAlignedHour struct {
	Exchange    domain.Exchange
	Market      string
	StartMS     int64
	QuoteVolume float64
}

func (s *AlignedCandleService) CachedHourlyCandles() []CachedAlignedHour {
	if s == nil {
		return nil
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	rows := make([]CachedAlignedHour, 0)
	for key, window := range s.cache {
		for at, amount := range window.values {
			rows = append(rows, CachedAlignedHour{key.exchange, key.market, at, amount})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Exchange != rows[j].Exchange {
			return rows[i].Exchange < rows[j].Exchange
		}
		if rows[i].Market != rows[j].Market {
			return rows[i].Market < rows[j].Market
		}
		return rows[i].StartMS < rows[j].StartMS
	})
	return rows
}
