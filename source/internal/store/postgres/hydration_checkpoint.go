package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

type hydrationQueryer interface {
	bucketQueryer
	rowQuerier
}

type hydrationTransactionStarter interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// StreamHydrationCheckpoint streams buckets and reads the accompanying dedupe,
// coverage and exact-recovery evidence from one PostgreSQL MVCC snapshot. The
// callback must populate a private, unpublished aggregate: a later callback,
// query, cancellation or commit failure invalidates the entire checkpoint.
// Buckets are never accumulated in State.Buckets. No migration or write occurs.
// The caller's earlier deadline wins. Private preparation may take up to 60s;
// final handoff refreshes still inherit their much shorter caller deadline.
func (r *Repository) StreamHydrationCheckpoint(
	ctx context.Context,
	bucketSince, recentSince domain.UnixSecond,
	consume func(aggregate.Bucket) error,
) (State, error) {
	return streamHydrationCheckpoint(ctx, r.pool, bucketSince, recentSince, consume)
}

func streamHydrationCheckpoint(ctx context.Context, database hydrationTransactionStarter, bucketSince, recentSince domain.UnixSecond, consume func(aggregate.Bucket) error) (State, error) {
	return streamHydrationCheckpointWithin(ctx, database, bucketSince, recentSince, consume, 60*time.Second)
}

// StreamStartupHydrationCheckpoint is for a stopped calculator's cold startup.
// It keeps one read-only MVCC snapshot and all validation, but allows the full
// retained state to load on a contended host. Warm handoffs retain their budget.
func (r *Repository) StreamStartupHydrationCheckpoint(ctx context.Context, bucketSince, recentSince domain.UnixSecond, consume func(aggregate.Bucket) error) (State, error) {
	return streamHydrationCheckpointWithin(ctx, r.pool, bucketSince, recentSince, consume, 5*time.Minute)
}

func streamHydrationCheckpointWithin(ctx context.Context, database hydrationTransactionStarter, bucketSince, recentSince domain.UnixSecond, consume func(aggregate.Bucket) error, budget time.Duration) (State, error) {
	if consume == nil {
		return State{}, errors.New("hydration checkpoint requires a bucket consumer")
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	tx, err := database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return State{}, fmt.Errorf("begin read-only hydration checkpoint: %w", err)
	}
	defer func() {
		// A cancelled caller must not leave the pooled connection in a live
		// transaction. Rollback remains bounded and does not inherit cancellation.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cleanupCancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	if err := streamBuckets(ctx, tx, bucketSince, func(row aggregate.Bucket) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return consume(row)
	}); err != nil {
		return State{}, fmt.Errorf("stream hydration checkpoint: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	state, err := loadHydrationState(ctx, tx, bucketSince, recentSince)
	if err != nil {
		return State{}, fmt.Errorf("read hydration checkpoint metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return State{}, fmt.Errorf("finish hydration checkpoint: %w", err)
	}
	return state, nil
}
