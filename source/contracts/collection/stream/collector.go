package collector

import (
	"market-share-go/contracts/collection/exchange"
	"market-share-go/internal/domain"
	"time"
)

type FrameEnvelope struct {
	Frame            domain.WebSocketFrame
	Message          exchange.ParsedMessage
	ParseError       string
	TradeMatchCounts []uint64
}

type ObservationLoss struct {
	Exchange            domain.Exchange
	Quote               string
	DetectedAt          time.Time
	EarliestEventTS     domain.UnixMilli
	Reason              string
	MissingTradeEvents  int
	MissingTradeMatches uint64
}
