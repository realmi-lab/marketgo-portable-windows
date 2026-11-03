package official

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strings"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/restbudget"
)

func historicalMinuteHourSource(exchange domain.Exchange) string {
	return strings.ToUpper(string(exchange)) + "_OFFICIAL_1M_TO_ALIGNED_1H_CANDLES_24H"
}

// Each page covers at most 199 minutes, reserving one response row as evidence
// of the left boundary. Only Upbit documents omitted no-trade candles. For
// other venues every minute must be explicit; an absent row is not a zero.
func (s *AlignedCandleService) fetchMinuteDerivedHours(ctx context.Context, market domain.MarketPair, startMS, endMS int64) (map[int64]MinuteCandle, error) {
	minutes := make(map[int64]MinuteCandle)
	for pageEnd := endMS; pageEnd > startMS; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pageStart := max(startMS, pageEnd-int64(alignedCandleMaximumRequestCount-1)*MinuteMS)
		request, err := BuildCandleRequest(market.Exchange, market.Symbol, pageEnd+1)
		if err != nil {
			return nil, err
		}
		count := (pageEnd-pageStart)/MinuteMS + 1
		switch market.Exchange {
		case domain.ExchangeCoinone:
			request.Params["size"] = count
		case domain.ExchangeKorbit:
			request.Params["start"] = pageStart - MinuteMS
			request.Params["end"] = pageEnd
			request.Params["limit"] = count
		case domain.ExchangeGopax:
			request.Params["start"] = pageStart - MinuteMS
			request.Params["end"] = pageEnd - 1
			request.Params["limit"] = count
		default:
			request.Params["count"] = count
		}
		query := url.Values{}
		for key, value := range request.Params {
			query.Set(key, fmt.Sprint(value))
		}
		group := "public"
		if market.Exchange == domain.ExchangeUpbit {
			group = "candle"
		}
		raw, err := restbudget.FetchJSONBytes(ctx, s.client, restbudget.Request{
			Exchange: string(market.Exchange), Group: group, URL: request.URL,
			Query: query, Priority: restbudget.PriorityGapRepair, Cost: 1,
		}, alignedCandleResponseLimit)
		if err != nil {
			return nil, err
		}
		candles, err := ParseCandles(market.Exchange, market.Symbol, raw)
		if err != nil {
			return nil, err
		}
		if len(candles) > int(count) {
			return nil, fmt.Errorf("minute response exceeds requested row count %d", count)
		}
		page := make(map[int64]bool, len(candles))
		oldest := pageEnd
		for _, candle := range candles {
			if candle.StartMS >= pageEnd {
				return nil, fmt.Errorf("minute response crossed exclusive end %d", pageEnd)
			}
			oldest = min(oldest, candle.StartMS)
			if candle.StartMS < startMS {
				continue
			}
			if _, err := candle.QuoteVolume(); err != nil {
				return nil, err
			}
			if previous, exists := minutes[candle.StartMS]; exists && !reflect.DeepEqual(previous, candle) {
				return nil, fmt.Errorf("conflicting minute evidence across pages at %d", candle.StartMS)
			}
			minutes[candle.StartMS] = candle
			page[candle.StartMS] = true
		}
		for minute := pageStart; minute < pageEnd; minute += MinuteMS {
			if page[minute] {
				continue
			}
			if _, exists := minutes[minute]; exists {
				return nil, fmt.Errorf("conflicting minute omission across pages at %d", minute)
			}
			if !(market.Exchange == domain.ExchangeUpbit && oldest <= pageStart) {
				return nil, fmt.Errorf("official minute evidence requires verification at %d", minute)
			}
		}
		pageEnd = pageStart
	}
	return rollupHistoricalMinutes(minutes, startMS, endMS)
}

func rollupHistoricalMinutes(minutes map[int64]MinuteCandle, startMS, endMS int64) (map[int64]MinuteCandle, error) {
	hours := make(map[int64]MinuteCandle)
	// Traverse in time order for deterministic sums, first open and last close.
	for start := startMS; start < endMS; start += MinuteMS {
		minute, exists := minutes[start]
		if !exists {
			continue // Only transport-validated Upbit no-trade omissions reach here.
		}
		hourStart := start / AlignedHourMS * AlignedHourMS
		hour, exists := hours[hourStart]
		if !exists {
			hour = minute
			hour.StartMS = hourStart
			hour.BaseVolume = 0
			hour.QuoteVolumeKRW = floatPointer(0)
			hour.DecimalEvidence = nil // A single-minute precision proof cannot describe an hour.
			hour.Source = historicalMinuteHourSource(minute.Exchange)
		}
		hour.High = math.Max(hour.High, minute.High)
		hour.Low = math.Min(hour.Low, minute.Low)
		hour.Close = minute.Close
		hour.BaseVolume += minute.BaseVolume
		amount := *hour.QuoteVolumeKRW + *minute.QuoteVolumeKRW
		hour.QuoteVolumeKRW = &amount
		if err := hour.Validate(); err != nil {
			return nil, fmt.Errorf("minute-derived hour at %d: %w", hourStart, err)
		}
		hours[hourStart] = hour
	}
	return hours, nil
}
