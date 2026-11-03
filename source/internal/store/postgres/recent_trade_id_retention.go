package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"market-share-go/internal/domain"
)

type recentTradeIDDeleteExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// PruneRecentTradeIDs deletes at most limit exact-ID checkpoints older than
// cutoff. Volume buckets and monotonic trade watermarks are intentionally not
// part of this maintenance operation.
func (r *Repository) PruneRecentTradeIDs(
	ctx context.Context,
	cutoff domain.UnixSecond,
	limit int,
) (int64, error) {
	if r == nil || r.pool == nil {
		return 0, errors.New("recent trade ID retention repository is unavailable")
	}
	return pruneRecentTradeIDs(ctx, r.pool, cutoff, limit)
}

func pruneRecentTradeIDs(
	ctx context.Context,
	executor recentTradeIDDeleteExecutor,
	cutoff domain.UnixSecond,
	limit int,
) (int64, error) {
	if executor == nil {
		return 0, errors.New("recent trade ID retention executor is unavailable")
	}
	if cutoff <= 0 {
		return 0, errors.New("recent trade ID retention cutoff must be positive")
	}
	if limit < 1 || limit > 100_000 {
		return 0, errors.New("recent trade ID retention batch must be between 1 and 100000")
	}
	tag, err := executor.Exec(ctx, `
		WITH expired AS (
			SELECT ctid
			FROM recent_trade_ids
			WHERE event_second < $1
			ORDER BY event_second
			LIMIT $2
		)
		DELETE FROM recent_trade_ids AS retained
		USING expired
		WHERE retained.ctid = expired.ctid
		  AND retained.event_second < $1`, int64(cutoff), limit)
	if err != nil {
		return 0, fmt.Errorf("prune recent trade IDs: %w", err)
	}
	return tag.RowsAffected(), nil
}
