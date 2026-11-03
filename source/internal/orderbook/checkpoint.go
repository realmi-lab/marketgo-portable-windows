package orderbook

import (
	"encoding/json"
	"errors"
	"market-share-go/internal/domain"
	"sort"
	"time"
)

type checkpointBook struct {
	Pair                                                                           domain.MarketPair
	Bids, Asks                                                                     []domain.OrderBookLevel
	Initialized                                                                    bool
	Sequence                                                                       *int64
	SourceDepth                                                                    *int
	FirstReceivedTS, LastReceivedTS, LastEventTS                                   domain.UnixMilli
	UpdateCount, ValidUpdateCount, SequenceGapCount, CrossedCount, BestChangeCount int64
	UpdateRateHz, PreviousBestBid, PreviousBestAsk                                 float64
	HasPreviousBest                                                                bool
}
type storeCheckpoint struct {
	SchemaVersion                         int
	StaleAfter                            time.Duration
	Version, ReceivedEvents, SequenceGaps int64
	Books                                 []checkpointBook
}

// Checkpoint is an owner-only durable copy, including initialization and delta
// sequence state. Replaying a delta into an empty book is never a substitute.
func (s *Store) Checkpoint() ([]byte, error) {
	c := storeCheckpoint{SchemaVersion: 1, StaleAfter: s.staleAfter, Version: s.Version, ReceivedEvents: s.ReceivedEvents, SequenceGaps: s.SequenceGaps}
	if len(s.books) > 0 {
		c.Books = make([]checkpointBook, 0, len(s.books))
	}
	for _, b := range s.books {
		c.Books = append(c.Books, checkpointBook{Pair: b.pair, Bids: b.sortedBids, Asks: b.sortedAsks, Initialized: b.initialized, Sequence: b.sequence, SourceDepth: b.sourceDepth, FirstReceivedTS: b.firstReceivedTS, LastReceivedTS: b.lastReceivedTS, LastEventTS: b.lastEventTS, UpdateCount: b.updateCount, ValidUpdateCount: b.validUpdateCount, SequenceGapCount: b.sequenceGapCount, CrossedCount: b.crossedCount, BestChangeCount: b.bestChangeCount, UpdateRateHz: b.updateRateHz, PreviousBestBid: b.previousBestBid, PreviousBestAsk: b.previousBestAsk, HasPreviousBest: b.hasPreviousBest})
	}
	sort.Slice(c.Books, func(i, j int) bool {
		a, b := c.Books[i].Pair, c.Books[j].Pair
		if a.Exchange != b.Exchange {
			return a.Exchange < b.Exchange
		}
		return a.Symbol < b.Symbol
	})
	return json.Marshal(c)
}
func (s *Store) RestoreCheckpoint(data []byte) error {
	var c storeCheckpoint
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.SchemaVersion != 1 || c.StaleAfter != s.staleAfter {
		return errors.New("incompatible order book checkpoint")
	}
	books := make(map[bookKey]*bookState, len(c.Books))
	for _, row := range c.Books {
		if err := row.Pair.Validate(); err != nil {
			return err
		}
		key := eventKey(row.Pair.Exchange, row.Pair.Symbol)
		if books[key] != nil {
			return errors.New("duplicate checkpoint book")
		}
		b := &bookState{pair: row.Pair, bids: make(map[float64]domain.OrderBookLevel), asks: make(map[float64]domain.OrderBookLevel), initialized: row.Initialized, sequence: row.Sequence, sourceDepth: row.SourceDepth, firstReceivedTS: row.FirstReceivedTS, lastReceivedTS: row.LastReceivedTS, lastEventTS: row.LastEventTS, updateCount: row.UpdateCount, validUpdateCount: row.ValidUpdateCount, sequenceGapCount: row.SequenceGapCount, crossedCount: row.CrossedCount, bestChangeCount: row.BestChangeCount, updateRateHz: row.UpdateRateHz, previousBestBid: row.PreviousBestBid, previousBestAsk: row.PreviousBestAsk, hasPreviousBest: row.HasPreviousBest}
		for _, levels := range []struct {
			rows   []domain.OrderBookLevel
			target map[float64]domain.OrderBookLevel
		}{{row.Bids, b.bids}, {row.Asks, b.asks}} {
			for _, level := range levels.rows {
				if err := level.Validate(); err != nil {
					return err
				}
				if level.Quantity <= 0 {
					return errors.New("nonpositive checkpoint level")
				}
				if _, exists := levels.target[level.Price]; exists {
					return errors.New("duplicate checkpoint price")
				}
				levels.target[level.Price] = level
			}
		}
		b.sortAndTrim()
		books[key] = b
	}
	s.books = books
	s.Version = c.Version
	s.ReceivedEvents = c.ReceivedEvents
	s.SequenceGaps = c.SequenceGaps
	return nil
}
