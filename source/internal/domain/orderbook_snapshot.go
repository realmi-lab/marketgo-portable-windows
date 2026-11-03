package domain

import (
	"errors"
	"fmt"
	"strings"
)

const PersistedOrderBookDepth = 10

// OrderBookTopSnapshot is one complete, calculated top-of-book state. Unlike
// an exchange frame it never represents a delta: both sides are copied from
// the runtime-owned reconstructed book after the incoming event is applied.
type OrderBookTopSnapshot struct {
	Exchange   Exchange
	Market     string
	Base       string
	Quote      string
	EventTS    UnixMilli
	ReceivedTS UnixMilli
	Sequence   *int64
	Bids       []OrderBookLevel
	Asks       []OrderBookLevel
}

func (s OrderBookTopSnapshot) Key() string {
	return string(s.Exchange) + "\x00" + strings.ToUpper(strings.TrimSpace(s.Market))
}

func (s OrderBookTopSnapshot) Validate() error {
	if !s.Exchange.Valid() || strings.TrimSpace(s.Market) == "" || strings.TrimSpace(s.Base) == "" || strings.TrimSpace(s.Quote) == "" {
		return errors.New("order-book snapshot identity is invalid")
	}
	if s.EventTS <= 0 || s.ReceivedTS <= 0 {
		return errors.New("order-book snapshot timestamps must be positive")
	}
	if len(s.Bids) == 0 || len(s.Asks) == 0 {
		return errors.New("order-book snapshot requires both sides")
	}
	if len(s.Bids) > PersistedOrderBookDepth || len(s.Asks) > PersistedOrderBookDepth {
		return fmt.Errorf("order-book snapshot exceeds %d levels per side", PersistedOrderBookDepth)
	}
	for _, side := range [][]OrderBookLevel{s.Bids, s.Asks} {
		for _, level := range side {
			if err := level.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
