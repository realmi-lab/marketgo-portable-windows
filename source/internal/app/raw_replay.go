package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/exchange"
	"market-share-go/internal/reconcile"
)

const rawReplayFrameLimit = 50_000

// replayRawWebSocketMinute is intentionally anomaly-only and serialised by
// MinuteService. It reads through the main PostgreSQL pool so the journal's
// dedicated write connection remains available to live collection.
func (s *Service) replayRawWebSocketMinute(ctx context.Context, candidate reconcile.RawReplayCandidate) (reconcile.RawReplayResult, error) {
	if s.postgresRepository == nil {
		return reconcile.RawReplayResult{}, errors.New("raw WebSocket replay is unavailable with ephemeral SQLite")
	}
	if err := candidate.Normalize(); err != nil {
		return reconcile.RawReplayResult{}, err
	}
	frames, truncated, err := s.postgresRepository.WebSocketFramesBetween(
		ctx, candidate.Exchange, candidate.ReceivedStartNS, candidate.ReceivedEndNS, rawReplayFrameLimit,
	)
	if err != nil {
		return reconcile.RawReplayResult{}, err
	}
	if truncated {
		return reconcile.RawReplayResult{FramesRead: len(frames)}, errors.New("raw WebSocket replay frame limit exceeded")
	}
	wanted := make(map[string]struct{}, len(candidate.Markets))
	for _, market := range candidate.Markets {
		wanted[market.Key()] = struct{}{}
	}
	universe := s.registry.Universe()
	result := reconcile.RawReplayResult{FramesRead: len(frames)}
	minuteStartMS := int64(candidate.MinuteStart) * 1000
	minuteEndMS := minuteStartMS + reconcile.MinuteMS
	for _, frame := range frames {
		parsed, parseErr := exchange.ParseMessage(
			candidate.Exchange,
			frame.Payload,
			universe,
			domain.UnixMilli(frame.ReceivedAtNS/1_000_000),
		)
		if parseErr != nil {
			return result, fmt.Errorf("reparse raw %s WebSocket frame: %w", candidate.Exchange, parseErr)
		}
		if parsed.ExpectedTradeEvents != len(parsed.Trades) {
			return result, fmt.Errorf("reparse raw %s WebSocket frame produced %d/%d trade events", candidate.Exchange, len(parsed.Trades), parsed.ExpectedTradeEvents)
		}
		for _, trade := range parsed.Trades {
			key := string(trade.Exchange) + "\x00" + strings.ToUpper(trade.Market)
			if _, ok := wanted[key]; !ok || int64(trade.EventTS) < minuteStartMS || int64(trade.EventTS) >= minuteEndMS {
				continue
			}
			if err := s.runtime.SubmitHistoricalTrade(ctx, trade); err != nil {
				return result, fmt.Errorf("submit raw WebSocket replay trade: %w", err)
			}
			result.TradesSubmitted++
		}
	}
	// A zero-interval owner read is a queue barrier. Returning only after the
	// replayed events were classified keeps the next reconciliation retry from
	// racing the historical submissions.
	if _, err := s.runtime.atomicRead(ctx, nil, nil); err != nil {
		return result, fmt.Errorf("wait for raw WebSocket replay barrier: %w", err)
	}
	return result, nil
}
