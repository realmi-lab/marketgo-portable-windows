package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type CompactPartitionAction struct {
	Table   string `json:"table"`
	Action  string `json:"action"`
	Applied bool   `json:"applied"`
}

// ManageCompactIngestionPartitions is an explicit DB-owner operation, never a
// collector startup/append side effect. It prepares a week and removes only
// already-empty, expired daily children. It never moves or deletes live rows,
// removes the default partition, or touches journal identities.
func (r *Repository) ManageCompactIngestionPartitions(parent context.Context, apply bool) ([]CompactPartitionAction, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='1200ms'`); err != nil {
		return nil, err
	}
	var today int64
	var schema string
	if err = tx.QueryRow(ctx, `SELECT floor(extract(epoch FROM clock_timestamp())/86400)::bigint,n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=to_regclass('ingestion_frame_blocks') AND c.relkind='p'`).Scan(&today, &schema); err != nil {
		return nil, errors.New("compact partitioned schema is not prepared")
	}
	parentName := pgx.Identifier{schema, "ingestion_frame_blocks"}.Sanitize()
	if apply {
		var locked bool
		if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, ingestionMaintenanceLock).Scan(&locked); err != nil {
			return nil, err
		}
		if !locked {
			return nil, errors.New("ingestion maintenance is busy; retry later")
		}
		// Fail quickly if collection or a reader is using the parent. No service
		// termination, unbounded DDL wait or forced disconnection is permitted.
		if _, err = tx.Exec(ctx, `LOCK TABLE ONLY `+parentName+` IN ACCESS EXCLUSIVE MODE NOWAIT`); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE i.inhparent=$1::regclass`, parentName)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		existing[name] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var actions []CompactPartitionAction
	for day := today; day <= today+7; day++ {
		name := fmt.Sprintf("ingestion_frame_blocks_d%d", day)
		if existing[name] {
			continue
		}
		a := CompactPartitionAction{Table: name, Action: "prepare"}
		if apply {
			// PostgreSQL refuses this if DEFAULT already contains this day's
			// rows. Keep them intact for a separately reviewed move, never delete.
			query := fmt.Sprintf(`CREATE TABLE %s PARTITION OF %s FOR VALUES FROM (%d) TO (%d)`, pgx.Identifier{schema, name}.Sanitize(), parentName, day, day+1)
			if _, err = tx.Exec(ctx, query); err != nil {
				return nil, err
			}
			a.Applied = true
		}
		actions = append(actions, a)
	}
	for name := range existing {
		const prefix = "ingestion_frame_blocks_d"
		day, parseErr := strconv.ParseInt(strings.TrimPrefix(name, prefix), 10, 64)
		if !strings.HasPrefix(name, prefix) || parseErr != nil || day < 0 || day >= today-2 || name != fmt.Sprint(prefix, day) {
			continue
		}
		table := pgx.Identifier{schema, name}.Sanitize()
		if apply {
			if _, err = tx.Exec(ctx, `LOCK TABLE `+table+` IN ACCESS EXCLUSIVE MODE NOWAIT`); err != nil {
				return nil, err
			}
		}
		var populated bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+`)`).Scan(&populated); err != nil {
			return nil, err
		}
		if populated {
			continue
		}
		a := CompactPartitionAction{Table: name, Action: "drop_empty"}
		if apply {
			if _, err = tx.Exec(ctx, `DROP TABLE `+table); err != nil {
				return nil, err
			}
			a.Applied = true
		}
		actions = append(actions, a)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return actions, nil
}
