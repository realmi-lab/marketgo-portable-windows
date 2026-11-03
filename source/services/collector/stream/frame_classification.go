package collector

import (
	"market-share-go/internal/domain"
	"market-share-go/services/collector/exchange"
)

// classifyJournalFrame reuses the already parsed message; it never decodes a
// payload or claims that a parsed trade has passed the runtime's dedupe gate.
func classifyJournalFrame(message exchange.ParsedMessage, parseErr error) (string, string) {
	if parseErr != nil {
		return domain.FrameParseError, ""
	}
	if message.SubscriptionError != "" {
		return domain.FrameSubscriptionError, ""
	}
	if message.Kind == exchange.MessageSnapshot {
		return domain.FrameSnapshot, ""
	}
	if message.Kind != exchange.MessageTrade {
		return domain.FrameOther, ""
	}
	class := domain.FrameTrade
	var matches uint64
	market := ""
	for i, trade := range message.Trades {
		matches = saturatingCollectorAdd(matches, trade.EffectiveMatchCount())
		if i == 0 {
			market = trade.Market
		} else if market != trade.Market {
			market = ""
		}
	}
	if message.ExpectedTradeEvents > len(message.Trades) || message.ExpectedTradeMatches > matches {
		class = domain.FramePartialTrade
		// Missing events may belong to another market.
		market = ""
	}
	return class, market
}
