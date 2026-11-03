package reconcile

import (
	"encoding/json"
	"errors"
	"sort"

	"market-share-go/internal/domain"
)

type accumulatorOrder struct {
	Event, Received domain.UnixMilli
	TradeID         string
}
type accumulatorMinute struct {
	Row            WSTradeMinute
	First, Last    accumulatorOrder
	KRW, Buy, Sell float64
}
type accumulatorCheckpoint struct {
	Version int
	Minutes []accumulatorMinute
}

// Checkpoint retains open OHLC ordering and unresolved conversion totals.
// Storing only the displayed WSTradeMinute would lose those fields on restart.
func (a *Accumulator) Checkpoint() ([]byte, error) {
	c := accumulatorCheckpoint{Version: 1}
	for _, b := range a.minutes {
		if b.first == nil || b.last == nil {
			return nil, errors.New("minute order state missing")
		}
		c.Minutes = append(c.Minutes, accumulatorMinute{Row: b.row,
			First: accumulatorOrder{b.first.event, b.first.received, b.first.tradeID},
			Last:  accumulatorOrder{b.last.event, b.last.received, b.last.tradeID}, KRW: b.krw, Buy: b.buy, Sell: b.sell})
	}
	sort.Slice(c.Minutes, func(i, j int) bool {
		a, b := c.Minutes[i].Row, c.Minutes[j].Row
		if a.MinuteStart != b.MinuteStart {
			return a.MinuteStart < b.MinuteStart
		}
		if a.Exchange != b.Exchange {
			return a.Exchange < b.Exchange
		}
		return a.Market < b.Market
	})
	return json.Marshal(c)
}

func (a *Accumulator) RestoreCheckpoint(data []byte) error {
	var c accumulatorCheckpoint
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Version != 1 {
		return errors.New("unsupported minute accumulator checkpoint")
	}
	minutes := make(map[minuteKey]*minuteBuilder, len(c.Minutes))
	for _, v := range c.Minutes {
		if err := v.Row.Normalize(); err != nil {
			return err
		}
		if v.Row.TradeCount <= 0 || !nonnegative(v.KRW) || !nonnegative(v.Buy) || !nonnegative(v.Sell) || v.First.Event <= 0 || v.Last.Event < v.First.Event {
			return errors.New("invalid minute accumulator checkpoint")
		}
		key := minuteKey{v.Row.Exchange, v.Row.Market, v.Row.MinuteStart}
		if minutes[key] != nil {
			return errors.New("duplicate minute checkpoint")
		}
		minutes[key] = &minuteBuilder{row: v.Row, first: &orderKey{v.First.Event, v.First.Received, v.First.TradeID}, last: &orderKey{v.Last.Event, v.Last.Received, v.Last.TradeID}, krw: v.KRW, buy: v.Buy, sell: v.Sell}
	}
	a.minutes = minutes
	return nil
}
