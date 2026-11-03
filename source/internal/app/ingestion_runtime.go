package app

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
	"market-share-go/internal/store/postgres"
)

type ingestionCheckpointStore interface {
	FlushWithCoverageCursorFXAndDerived(context.Context, []aggregate.Bucket, []aggregate.WatermarkRow, []aggregate.RecentIDRow, *coverage.State, int64, int64, []byte, *postgres.IngestionDerived) error
}

type ingestionRuntimeState struct {
	// Independent calculators read complete latest-book snapshots. Their book
	// state is not an input to trade replay and has a separate durable owner.
	latestBooks    bool
	store          ingestionCheckpointStore
	cursor         int64
	pendingCursor  int64
	pendingState   *coverage.State
	pendingFX      []byte
	pendingDerived *postgres.IngestionDerived
	flushing       bool
	minutes        *reconcile.Accumulator
	minuteState    ingestionMinuteCheckpoint
	derived        postgres.IngestionDerived
	derivedError   error
}

// enableIngestion must run before Start. Only journal batches may persist this
// runtime: advancing a timer must never checkpoint dedupe ahead of its cursor.
func (r *Runtime) enableIngestion(store ingestionCheckpointStore, cursor int64) {
	if r.ingestion == nil {
		r.ingestion = &ingestionRuntimeState{}
	}
	r.ingestion.store, r.ingestion.cursor = store, cursor
	r.updateMetrics(func(m *RuntimeMetrics) { m.JournalReplay = true })
	if r.ingestion.minutes == nil {
		r.ingestion.minutes = reconcile.NewAccumulator()
	}
	r.ingestion.minuteState.Version = 1
	r.ingestion.derived.Version = 1
}

// applyIngestionBatch prepares fully decoded batches on the canonical owner.
// A failed transaction retains absolute rows and retries without reapplying
// trades. This also handles a lost acknowledgement after PostgreSQL committed.
func (r *Runtime) applyIngestionBatch(ctx context.Context, previous, next int64, apply func()) error {
	var state *ingestionRuntimeState
	var buckets []aggregate.Bucket
	var watermarks []aggregate.WatermarkRow
	var recent []aggregate.RecentIDRow
	err := r.handoffOwnerTurn(ctx, func(pending *pendingFlush) error {
		state = r.ingestion
		if state == nil || state.cursor != previous || next <= previous {
			return errors.New("invalid computation ingestion cursor")
		}
		if state.flushing {
			return errors.New("computation checkpoint commit is already in flight")
		}
		if state.pendingCursor != 0 && state.pendingCursor != next {
			return errors.New("previous ingestion checkpoint must commit before another batch")
		}
		if state.pendingCursor == 0 {
			apply()
			r.weighOpenGap()
			buckets, watermarks, recent := pending.drain(r.aggregator, domain.UnixSecond(math.MaxInt64))
			pending.add(buckets, watermarks, recent)
			// Coverage is an absolute snapshot. A cursor-only batch can keep the
			// previously committed snapshot instead of replacing every segment.
			if r.coverageDirty {
				observation := r.coverage.PersistenceState()
				state.pendingState = &observation
			}
			state.pendingCursor = next
		}
		if state.derivedError != nil {
			return state.derivedError
		}
		if state.pendingFX == nil {
			checkpoint, err := r.ingestionCheckpoint()
			if err != nil {
				return err
			}
			state.pendingFX = checkpoint
			state.pendingDerived = state.derived.Clone()
		}
		bucketRows, watermarkRows, recentRows := pending.rows()
		// No owner scratch slices cross the asynchronous storage boundary.
		buckets, watermarks, recent = slices.Clone(bucketRows), slices.Clone(watermarkRows), slices.Clone(recentRows)
		state.flushing = true
		return nil
	})
	if err != nil {
		return err
	}
	// There is still exactly one sequential journal consumer/commit. The owner
	// remains free to serve reads and complete latest-book snapshots during SQL.
	writeErr := state.store.FlushWithCoverageCursorFXAndDerived(ctx, buckets, watermarks, recent, state.pendingState, previous, next, state.pendingFX, state.pendingDerived)
	// Cancellation cannot abandon an already prepared commit receipt. Join the
	// owner before returning, even on a lost acknowledgement or shutdown. Service
	// Stop joins this consumer before stopping the runtime or closing its store.
	return r.finishIngestionCommit(ctx, func(pending *pendingFlush) error {
		if r.ingestion != state || state.pendingCursor != next || !state.flushing {
			return errors.New("computation checkpoint ownership changed during commit")
		}
		state.flushing = false
		if writeErr != nil {
			r.updateMetrics(func(m *RuntimeMetrics) {
				m.FlushFailures++
				m.LastFlushError = writeErr.Error()
				m.LastFlushErrorAt = time.Now()
			})
			return writeErr
		}
		if state.pendingState != nil {
			r.coverage.ApplyPersistenceIdentities(*state.pendingState)
		}
		state.cursor = next
		state.pendingCursor = 0
		state.pendingState = nil
		state.pendingFX = nil
		state.pendingDerived = nil
		state.derived = postgres.IngestionDerived{Version: 1}
		pending.clear()
		r.coverageDirty = false
		r.updateMetrics(func(m *RuntimeMetrics) { m.LastFlush = time.Now() })
		return nil
	})
}

// Commit adoption is a receipt for an admitted owner operation, not a new
// trade/coverage barrier. A busy producer or canceled caller must not strand
// flushing=true. The response joins ownership just like handoffOwnerTurn.
func (r *Runtime) finishIngestionCommit(ctx context.Context, apply func(*pendingFlush) error) error {
	request := handoffOwnerRequest{ctx: context.WithoutCancel(ctx), run: apply, response: make(chan error, 1)}
	select {
	case r.handoffRequests <- request:
	case <-r.done:
		return ErrStopped
	}
	select {
	case err := <-request.response:
		return err
	case <-r.done:
		return ErrStopped
	}
}

type gatedIngestionCheckpointStore struct {
	next ingestionCheckpointStore
	gate *deploymentWriterGate
}

func (s gatedIngestionCheckpointStore) FlushWithCoverageCursorFXAndDerived(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow, observation *coverage.State, previous, next int64, fxCheckpoint []byte, derived *postgres.IngestionDerived) error {
	return s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "ingestion_checkpoint", run: func(writeCtx context.Context) error {
		return s.next.FlushWithCoverageCursorFXAndDerived(writeCtx, buckets, watermarks, recent, observation, previous, next, fxCheckpoint, derived)
	}})
}
