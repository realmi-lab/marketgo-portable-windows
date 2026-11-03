package reconcile

import (
	"errors"
	"math"
	"sort"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

type minuteKey struct {
	exchange domain.Exchange
	market   string
	minute   domain.UnixSecond
}
type orderKey struct {
	event, received domain.UnixMilli
	tradeID         string
}

type minuteBuilder struct {
	row            WSTradeMinute
	first, last    *orderKey
	krw, buy, sell float64
}

type Accumulator struct{ minutes map[minuteKey]*minuteBuilder }

func NewAccumulator() *Accumulator { return &Accumulator{minutes: make(map[minuteKey]*minuteBuilder)} }

func (a *Accumulator) IngestAccepted(trade aggregate.AcceptedTrade) error {
	minute := domain.UnixSecond(int64(trade.EventSecond) / MinuteSeconds * MinuteSeconds)
	key := minuteKey{domain.Exchange(trade.Key.Exchange), trade.Key.Market, minute}
	builder := a.minutes[key]
	if builder == nil {
		builder = &minuteBuilder{row: WSTradeMinute{Exchange: domain.Exchange(trade.Key.Exchange), Market: trade.Key.Market, Base: trade.Key.Base, Quote: trade.Key.Quote, MinuteStart: key.minute, CoverageStatus: DetailObserved}}
		a.minutes[key] = builder
	}
	return builder.ingestAccepted(trade)
}

func (b *minuteBuilder) ingestAccepted(trade aggregate.AcceptedTrade) error {
	value := trade.Totals
	baseVolume := b.row.BaseVolume + value.BaseVolume
	quoteVolume := b.row.QuoteVolume + value.NativeVolume
	krw := b.krw + value.KRWVolume
	buy := b.buy + value.BuyKRWVolume
	sell := b.sell + value.SellKRWVolume
	if !positive(baseVolume) || !positive(quoteVolume) ||
		!nonnegative(krw) || !nonnegative(buy) || !nonnegative(sell) {
		return errors.New("accepted trade would overflow WebSocket minute volumes")
	}
	if value.TradeCount > math.MaxInt64 || uint64(b.row.TradeCount) > uint64(math.MaxInt64)-value.TradeCount ||
		value.UnresolvedCount > math.MaxInt64 || uint64(b.row.UnresolvedTradeCount) > uint64(math.MaxInt64)-value.UnresolvedCount {
		return errors.New("accepted trade would overflow WebSocket minute counts")
	}

	// Commit only after every derived value is known to be representable.
	key := orderKey{trade.EventTS, trade.ReceivedTS, trade.TradeID}
	if b.first == nil || less(key, *b.first) {
		b.first = &key
		b.row.Open = floatPointer(trade.Price)
		ts := trade.EventTS
		b.row.FirstTradeTS = &ts
	}
	if b.last == nil || !less(key, *b.last) {
		b.last = &key
		b.row.Close = floatPointer(trade.Price)
		ts := trade.EventTS
		b.row.LastTradeTS = &ts
	}
	if b.row.High == nil || trade.Price > *b.row.High {
		b.row.High = floatPointer(trade.Price)
	}
	if b.row.Low == nil || trade.Price < *b.row.Low {
		b.row.Low = floatPointer(trade.Price)
	}
	b.row.BaseVolume = baseVolume
	b.row.QuoteVolume = quoteVolume
	b.row.TradeCount += int64(value.TradeCount)
	b.row.UnresolvedTradeCount += int64(value.UnresolvedCount)
	b.krw, b.buy, b.sell = krw, buy, sell
	return nil
}

func (a *Accumulator) MarkGap(exchange domain.Exchange, market string, minute domain.UnixSecond, gapSeconds int64) error {
	if err := validateMinute(minute); err != nil {
		return err
	}
	builder := a.minutes[minuteKey{exchange, market, minute}]
	if builder == nil {
		return errors.New("a gap can be attached only after at least one trade created the minute")
	}
	gap := clampGap(gapSeconds)
	if gap > builder.row.GapSeconds {
		builder.row.GapSeconds = gap
	}
	if builder.row.GapSeconds > 0 {
		builder.row.CoverageStatus = "PARTIAL"
	}
	return nil
}

func (a *Accumulator) Get(exchange domain.Exchange, market string, minute domain.UnixSecond) (*WSTradeMinute, error) {
	if err := validateMinute(minute); err != nil {
		return nil, err
	}
	builder := a.minutes[minuteKey{exchange, market, minute}]
	if builder == nil {
		return nil, nil
	}
	row, err := builder.build()
	return &row, err
}

func (a *Accumulator) DrainClosed(before domain.UnixSecond) ([]WSTradeMinute, error) {
	boundary := domain.UnixSecond(int64(before) / MinuteSeconds * MinuteSeconds)
	keys := make([]minuteKey, 0)
	for key := range a.minutes {
		if key.minute < boundary {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].minute != keys[j].minute {
			return keys[i].minute < keys[j].minute
		}
		if keys[i].exchange != keys[j].exchange {
			return keys[i].exchange < keys[j].exchange
		}
		return keys[i].market < keys[j].market
	})
	rows := make([]WSTradeMinute, 0, len(keys))
	for _, key := range keys {
		row, err := a.minutes[key].build()
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		delete(a.minutes, key)
	}
	return rows, nil
}

func (a *Accumulator) Len() int { return len(a.minutes) }

func (b *minuteBuilder) build() (WSTradeMinute, error) {
	row := b.row
	if row.UnresolvedTradeCount == 0 {
		row.KRWVolume = floatPointer(b.krw)
		row.BuyKRWVolume = floatPointer(b.buy)
		row.SellKRWVolume = floatPointer(b.sell)
	}
	err := row.Normalize()
	return row, err
}

func less(a, b orderKey) bool {
	if a.event != b.event {
		return a.event < b.event
	}
	if a.received != b.received {
		return a.received < b.received
	}
	return a.tradeID < b.tradeID
}
