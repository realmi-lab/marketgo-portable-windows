package app

import (
	"encoding/json"
	"errors"
	"market-share-go/internal/coverage"
	"market-share-go/internal/orderbook"
	"market-share-go/internal/reconcile"
	"market-share-go/internal/store/postgres"
	"time"
)

type ingestionOwnerCheckpoint struct {
	Version                int                        `json:"version"`
	FX                     json.RawMessage            `json:"fx"`
	Books                  json.RawMessage            `json:"books"`
	BooksSource            string                     `json:"books_source,omitempty"`
	Minutes                *ingestionMinuteCheckpoint `json:"minutes,omitempty"`
	Derived                *postgres.IngestionDerived `json:"derived,omitempty"`
	CoverageInvalidThrough *int64                     `json:"coverage_invalid_through,omitempty"`
}

func (r *Runtime) ingestionCheckpoint() ([]byte, error) {
	fxState, err := r.oracle.Checkpoint()
	if err != nil {
		return nil, err
	}
	bookStore := r.books
	latestBooks := r.ingestion != nil && r.ingestion.latestBooks
	if latestBooks {
		// Keep a valid v1 empty-book envelope for rollback readers. The collector
		// owns durable latest books; serializing them into every trade checkpoint
		// duplicates its writes and cannot make them atomic with that trade cursor.
		bookStore = orderbook.NewStore(time.Duration(r.books.StaleAfterSec() * float64(time.Second)))
	}
	books, err := bookStore.Checkpoint()
	if err != nil {
		return nil, err
	}
	c := ingestionOwnerCheckpoint{Version: 1, FX: fxState, Books: books}
	if latestBooks {
		c.BooksSource = "orderbook_latest"
	}
	if r.ingestion != nil {
		invalidThrough := r.coverage.InvalidThroughSecond()
		c.CoverageInvalidThrough = &invalidThrough
		minuteState := r.ingestion.minuteState
		minuteState.Accumulator, err = r.ingestion.minutes.Checkpoint()
		if err != nil {
			return nil, err
		}
		c.Minutes = &minuteState
	}
	return json.Marshal(c)
}
func (r *Runtime) restoreIngestionCheckpoint(data []byte) error {
	var c ingestionOwnerCheckpoint
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Version != 1 || len(c.FX) == 0 || len(c.Books) == 0 {
		return errors.New("invalid ingestion owner checkpoint")
	}
	if c.BooksSource != "" && c.BooksSource != "orderbook_latest" {
		return errors.New("unsupported ingestion book source")
	}
	if c.BooksSource == "orderbook_latest" && (r.ingestion == nil || !r.ingestion.latestBooks) {
		return errors.New("latest-book checkpoint requires independent latest-book input")
	}
	var restoredCoverage *coverage.Tracker
	if c.CoverageInvalidThrough != nil {
		state := r.coverage.State()
		if *c.CoverageInvalidThrough < 0 {
			return errors.New("invalid coverage invalidation checkpoint")
		}
		for _, gap := range state.Gaps {
			// Repeated invalidation moves CoverageStart past the still-active
			// gap. LastObserved=0 keeps that loss strict; only a resumed window
			// may treat an older retained gap as belonging to a prior owner.
			if gap.Status == "OPEN" && gap.End == nil && (state.LastObserved == 0 || gap.Start >= state.CoverageStart) && *c.CoverageInvalidThrough < gap.Start {
				return errors.New("coverage invalidation checkpoint precedes open gap")
			}
		}
		state.InvalidThrough = *c.CoverageInvalidThrough
		restoredCoverage = coverage.ContinueCheckpoint(state)
	}
	books := orderbook.NewStore(time.Duration(r.books.StaleAfterSec() * float64(time.Second)))
	if err := books.RestoreCheckpoint(c.Books); err != nil {
		return err
	}
	var minutes *reconcile.Accumulator
	if c.Minutes != nil {
		minutes = reconcile.NewAccumulator()
		if c.Minutes.Version != 1 {
			return errors.New("unsupported ingestion minute checkpoint")
		}
		if err := minutes.RestoreCheckpoint(c.Minutes.Accumulator); err != nil {
			return err
		}
	}
	if err := r.oracle.RestoreCheckpoint(c.FX); err != nil {
		return err
	}
	r.books = books
	if restoredCoverage != nil {
		r.coverage = restoredCoverage
	}
	if minutes != nil {
		if r.ingestion == nil {
			r.ingestion = &ingestionRuntimeState{}
		}
		r.ingestion.minutes = minutes
		r.ingestion.minuteState = *c.Minutes
		r.ingestion.minuteState.Accumulator = nil
		r.updateMetrics(func(m *RuntimeMetrics) { m.IngestionMinuteRecoveryEvents = r.ingestion.minuteState.RecoveryEvents })
	}
	return nil
}
