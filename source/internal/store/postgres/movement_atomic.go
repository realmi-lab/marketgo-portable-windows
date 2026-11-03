package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"market-share-go/internal/movement"
)

type movementSampleExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// CommitMovementObservation persists the sample and all movement transitions
// in one transaction. The tracker installs its calculated state after success.
func (r *Repository) CommitMovementObservation(ctx context.Context, sample *movement.Snapshot, events []movement.EventWrite) error {
	r.movementEvidenceMu.Lock()
	defer r.movementEvidenceMu.Unlock()
	for _, write := range events {
		if err := movement.ValidateEventDirection(write.Event); err != nil {
			return err
		}
		evidence, err := r.MovementNoticeEvidence(ctx, write.Event, write.Event.Drivers)
		if err != nil {
			return err
		}
		write.Event.Notices = evidence
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if sample != nil {
		if err := saveMovementSample(ctx, tx, sample); err != nil {
			return err
		}
	}
	for _, write := range events {
		if err := upsertMovementEvent(ctx, tx, write.Event); err != nil {
			return err
		}
		if err := replaceMovementDetails(ctx, tx, write.Event, write.Baseline, write.Extreme); err != nil {
			return err
		}
		if err := captureMovementFlowEvidence(ctx, tx, write.Event, write.Extreme); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit official movement observation: %w", err)
	}
	return nil
}
