package app

import (
	"context"
	"errors"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/internal/orderbookfeed"
)

// One reader owns its transport cursor. Only successful owner admission advances it.
// Configured live mode never falls back to lagging DB books; an unavailable feed
// keeps the actual receipt timestamp, allowing existing stale-book UI semantics.
func (s *Service) monitorLiveOrderBooks(ctx context.Context) {
	client := orderbookfeed.NewClient(s.collectorURL, s.collectorToken)
	var cursor orderbookfeed.Cursor
	seen := make(map[string]liveOrderBookVersion)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastError time.Time
	s.logger.Info("order-book source enabled", "source", "collector-memory", "poll_ms", 250)
	for ctx.Err() == nil {
		frame, err := client.Read(ctx, cursor)
		if err == nil {
			applyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = applyLiveOrderBooks(applyCtx, frame, seen, func(ctx context.Context, books []domain.OrderBookTopSnapshot) ([]bool, error) {
				events := make([]domain.OrderBookEvent, len(books))
				for index, book := range books {
					events[index] = orderBookEventFromLatest(book)
				}
				return s.runtime.SubmitOrderBooks(ctx, events)
			})
			cancel()
			if err == nil {
				cursor = frame.Cursor
			}
		}
		if err != nil && ctx.Err() == nil && time.Since(lastError) >= 10*time.Second {
			s.logger.Warn("live order-book read/apply failed", "error", err)
			lastError = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type liveOrderBookVersion struct {
	// Fixed scalar arrays preserve exact equality without retaining decoded
	// transport slices or copy-allocating a second set of levels.
	receivedTS  domain.UnixMilli
	eventTS     domain.UnixMilli
	base        string
	quote       string
	sequence    int64
	hasSequence bool
	bids        [domain.PersistedOrderBookDepth]liveOrderBookLevel
	asks        [domain.PersistedOrderBookDepth]liveOrderBookLevel
	bidCount    uint8
	askCount    uint8
	epoch       string
	revision    uint64
}

type liveOrderBookLevel struct {
	price, quantity      float64
	orderCount, updateID int64
	hasOrderCount, hasID bool
}

var errLiveOrderBookNotAdmitted = errors.New("live order-book owner did not admit snapshot")

func newerLiveBook(next domain.OrderBookTopSnapshot, previous liveOrderBookVersion, transportSuccessor bool) bool {
	if next.ReceivedTS != previous.receivedTS {
		return next.ReceivedTS > previous.receivedTS
	}
	if next.Sequence != nil && previous.hasSequence {
		return *next.Sequence > previous.sequence
	}
	return transportSuccessor && !previous.equal(next)
}

func applyLiveOrderBooks(ctx context.Context, frame orderbookfeed.Frame, seen map[string]liveOrderBookVersion, apply func(context.Context, []domain.OrderBookTopSnapshot) ([]bool, error)) error {
	for next := 0; next < len(frame.Books); {
		var books [maxOrderBookBatchSize]domain.OrderBookTopSnapshot
		var revisions [maxOrderBookBatchSize]uint64
		var keys [maxOrderBookBatchSize]string
		count := 0
		for next < len(frame.Books) && count < len(books) {
			book := frame.Books[next]
			revision := uint64(0)
			if len(frame.BookRevisions) == len(frame.Books) {
				revision = frame.BookRevisions[next]
			}
			next++
			key := book.Key()
			previous, exists := seen[key]
			transportSuccessor := exists && previous.epoch == frame.Cursor.Epoch && revision > previous.revision
			if exists && !newerLiveBook(book, previous, transportSuccessor) {
				continue
			}
			books[count], revisions[count], keys[count] = book, revision, key
			count++
		}
		if count == 0 {
			continue
		}
		accepted, err := apply(ctx, books[:count])
		if err != nil {
			return err
		}
		if len(accepted) != count {
			return errors.New("live order-book owner returned invalid admission count")
		}
		rejected := false
		for index := range accepted {
			if !accepted[index] {
				rejected = true
				continue
			}
			seen[keys[index]] = newLiveOrderBookVersion(books[index], frame.Cursor.Epoch, revisions[index])
		}
		if rejected {
			return errLiveOrderBookNotAdmitted
		}
	}
	return nil
}

func newLiveOrderBookVersion(book domain.OrderBookTopSnapshot, epoch string, revision uint64) liveOrderBookVersion {
	version := liveOrderBookVersion{receivedTS: book.ReceivedTS, eventTS: book.EventTS, base: book.Base, quote: book.Quote,
		bidCount: uint8(len(book.Bids)), askCount: uint8(len(book.Asks)), epoch: epoch, revision: revision}
	if book.Sequence != nil {
		version.sequence, version.hasSequence = *book.Sequence, true
	}
	for index := range book.Bids {
		version.bids[index] = newLiveOrderBookLevel(book.Bids[index])
	}
	for index := range book.Asks {
		version.asks[index] = newLiveOrderBookLevel(book.Asks[index])
	}
	return version
}

func newLiveOrderBookLevel(level domain.OrderBookLevel) liveOrderBookLevel {
	version := liveOrderBookLevel{price: level.Price, quantity: level.Quantity}
	if level.OrderCount != nil {
		version.orderCount, version.hasOrderCount = *level.OrderCount, true
	}
	if level.UpdateID != nil {
		version.updateID, version.hasID = *level.UpdateID, true
	}
	return version
}

func (previous liveOrderBookVersion) equal(book domain.OrderBookTopSnapshot) bool {
	if previous.eventTS != book.EventTS || previous.base != book.Base || previous.quote != book.Quote ||
		int(previous.bidCount) != len(book.Bids) || int(previous.askCount) != len(book.Asks) ||
		previous.hasSequence != (book.Sequence != nil) || previous.hasSequence && previous.sequence != *book.Sequence {
		return false
	}
	for index := range book.Bids {
		if !previous.bids[index].equal(book.Bids[index]) {
			return false
		}
	}
	for index := range book.Asks {
		if !previous.asks[index].equal(book.Asks[index]) {
			return false
		}
	}
	return true
}

func (previous liveOrderBookLevel) equal(level domain.OrderBookLevel) bool {
	return previous.price == level.Price && previous.quantity == level.Quantity &&
		previous.hasOrderCount == (level.OrderCount != nil) && (!previous.hasOrderCount || previous.orderCount == *level.OrderCount) &&
		previous.hasID == (level.UpdateID != nil) && (!previous.hasID || previous.updateID == *level.UpdateID)
}
