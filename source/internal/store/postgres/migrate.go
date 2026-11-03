package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockID serializes schema changes even when two one-off migration
// containers are started at the same time. The transaction-scoped lock is
// released automatically on commit, rollback, connection loss, or cancellation.
const migrationLockID int64 = 0x4d5347524d494752

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	bootstrap, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration bootstrap: %w", err)
	}
	defer func() { _ = bootstrap.Rollback(ctx) }()
	if _, err := bootstrap.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock migration bootstrap: %w", err)
	}
	if _, err := bootstrap.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS market_share_schema_migrations (
            version TEXT PRIMARY KEY,
            applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	if err := bootstrap.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration bootstrap: %w", err)
	}

	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		content, err := migrationFiles.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("lock migration %s: %w", version, err)
		}
		var applied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(
            SELECT 1 FROM market_share_schema_migrations WHERE version = $1
        )`, version).Scan(&applied); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied {
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit migration check %s: %w", version, err)
			}
			continue
		}
		if _, err = tx.Exec(ctx, string(content)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO market_share_schema_migrations(version) VALUES ($1)`, version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}
