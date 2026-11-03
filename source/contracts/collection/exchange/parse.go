package exchange

import (
	"market-share-go/internal/domain"
)

type MessageKind string

type ParsedMessage struct {
	Kind                 MessageKind
	ExpectedTradeEvents  int
	ExpectedTradeMatches uint64
	EarliestTradeTS      domain.UnixMilli
	Trades               []domain.TradeEvent
	OrderBooks           []domain.OrderBookEvent
	SubscriptionError    string
}
