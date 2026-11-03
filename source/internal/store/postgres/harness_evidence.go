package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type HarnessEvidence struct {
	CapturedAt           time.Time              `json:"captured_at"`
	DatabaseBytes        int64                  `json:"database_bytes"`
	WALBytes             uint64                 `json:"wal_bytes"`
	CurrentWALLSN        string                 `json:"current_wal_lsn,omitempty"`
	BlocksRead           int64                  `json:"blocks_read"`
	BlocksHit            int64                  `json:"blocks_hit"`
	TuplesReturned       int64                  `json:"tuples_returned"`
	TuplesFetched        int64                  `json:"tuples_fetched"`
	TuplesInserted       int64                  `json:"tuples_inserted"`
	TuplesUpdated        int64                  `json:"tuples_updated"`
	TuplesDeleted        int64                  `json:"tuples_deleted"`
	TransactionsCommit   int64                  `json:"transactions_commit"`
	TransactionsRollback int64                  `json:"transactions_rollback"`
	TemporaryBytes       int64                  `json:"temporary_bytes"`
	Deadlocks            int64                  `json:"deadlocks"`
	TableBytes           map[string]int64       `json:"table_bytes"`
	Pool                 PoolStats              `json:"pool"`
	TopStatements        []HarnessStatementStat `json:"top_statements,omitempty"`
	Warnings             []string               `json:"warnings,omitempty"`
}

type HarnessStatementStat struct {
	QueryID          string  `json:"query_id"`
	Calls            int64   `json:"calls"`
	TotalExecutionMS float64 `json:"total_execution_ms"`
	MeanExecutionMS  float64 `json:"mean_execution_ms"`
	Rows             int64   `json:"rows"`
	SharedBlocksHit  int64   `json:"shared_blocks_hit"`
	SharedBlocksRead int64   `json:"shared_blocks_read"`
	Query            string  `json:"query"`
}

func (r *Repository) HarnessDatabaseName(ctx context.Context) (string, error) {
	var name string
	if err := r.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return "", fmt.Errorf("read harness database identity: %w", err)
	}
	return name, nil
}

func (r *Repository) HarnessEvidence(ctx context.Context) (HarnessEvidence, error) {
	evidence := HarnessEvidence{
		CapturedAt: time.Now().UTC(), TableBytes: make(map[string]int64), Pool: r.PoolStats(),
	}
	err := r.pool.QueryRow(ctx, `
		SELECT pg_database_size(current_database()),
		       xact_commit, xact_rollback, blks_read, blks_hit,
		       tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted,
		       temp_bytes, deadlocks
		FROM pg_stat_database
		WHERE datname = current_database()`,
	).Scan(
		&evidence.DatabaseBytes,
		&evidence.TransactionsCommit, &evidence.TransactionsRollback,
		&evidence.BlocksRead, &evidence.BlocksHit,
		&evidence.TuplesReturned, &evidence.TuplesFetched,
		&evidence.TuplesInserted, &evidence.TuplesUpdated, &evidence.TuplesDeleted,
		&evidence.TemporaryBytes, &evidence.Deadlocks,
	)
	if err != nil {
		return HarnessEvidence{}, fmt.Errorf("collect pg_stat_database evidence: %w", err)
	}

	var walText string
	if err := r.pool.QueryRow(ctx, `SELECT COALESCE(wal_bytes, 0)::text FROM pg_stat_wal`).Scan(&walText); err != nil {
		evidence.Warnings = append(evidence.Warnings, "pg_stat_wal unavailable: "+err.Error())
	} else if parsed, parseErr := strconv.ParseUint(walText, 10, 64); parseErr != nil {
		evidence.Warnings = append(evidence.Warnings, "pg_stat_wal bytes are not an integer: "+parseErr.Error())
	} else {
		evidence.WALBytes = parsed
	}
	if err := r.pool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&evidence.CurrentWALLSN); err != nil {
		evidence.Warnings = append(evidence.Warnings, "current WAL LSN unavailable: "+err.Error())
	}

	for _, table := range []string{
		"volume_buckets", "trade_watermarks", "recent_trade_ids",
		"websocket_frames", "minute_observation_seals",
	} {
		var bytes int64
		if err := r.pool.QueryRow(ctx, `SELECT COALESCE(pg_total_relation_size(to_regclass($1)), 0)`, table).Scan(&bytes); err != nil {
			evidence.Warnings = append(evidence.Warnings, fmt.Sprintf("relation size %s unavailable: %v", table, err))
			continue
		}
		evidence.TableBytes[table] = bytes
	}

	var statementsAvailable bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements')`).Scan(&statementsAvailable); err != nil {
		evidence.Warnings = append(evidence.Warnings, "pg_stat_statements extension check failed: "+err.Error())
		return evidence, nil
	}
	if !statementsAvailable {
		evidence.Warnings = append(evidence.Warnings, "pg_stat_statements extension is not installed")
		return evidence, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT queryid::text, calls, total_exec_time, mean_exec_time, rows,
		       shared_blks_hit, shared_blks_read, query
		FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname=current_database())
		ORDER BY total_exec_time DESC
		LIMIT 10`)
	if err != nil {
		evidence.Warnings = append(evidence.Warnings, "pg_stat_statements query failed: "+err.Error())
		return evidence, nil
	}
	defer rows.Close()
	for rows.Next() {
		var statement HarnessStatementStat
		if err := rows.Scan(
			&statement.QueryID, &statement.Calls, &statement.TotalExecutionMS,
			&statement.MeanExecutionMS, &statement.Rows,
			&statement.SharedBlocksHit, &statement.SharedBlocksRead, &statement.Query,
		); err != nil {
			evidence.Warnings = append(evidence.Warnings, "pg_stat_statements row failed: "+err.Error())
			break
		}
		statement.Query = strings.Join(strings.Fields(statement.Query), " ")
		if len(statement.Query) > 300 {
			statement.Query = statement.Query[:300] + "…"
		}
		evidence.TopStatements = append(evidence.TopStatements, statement)
	}
	if err := rows.Err(); err != nil {
		evidence.Warnings = append(evidence.Warnings, "pg_stat_statements iteration failed: "+err.Error())
	}
	return evidence, nil
}
