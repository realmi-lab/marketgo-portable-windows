package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// WebSocketJournalStorageEvidence is scoped to one probe connection prefix so
// repeated runs in the same disposable database do not contaminate payload
// compression evidence. Relation and WAL deltas remain database-wide and are
// captured separately by HarnessEvidence.
type WebSocketJournalStorageEvidence struct {
	ConnectionPrefix   string                                    `json:"connection_prefix"`
	Rows               int64                                     `json:"rows"`
	IdentityRows       int64                                     `json:"identity_rows"`
	GZIPRows           int64                                     `json:"gzip_rows"`
	UncompressedBytes  int64                                     `json:"uncompressed_bytes"`
	StoredPayloadBytes int64                                     `json:"stored_payload_bytes"`
	TupleBytes         int64                                     `json:"tuple_bytes"`
	Exchanges          []WebSocketJournalExchangeStorageEvidence `json:"exchanges"`
}

type WebSocketJournalExchangeStorageEvidence struct {
	Exchange           string `json:"exchange"`
	Rows               int64  `json:"rows"`
	UncompressedBytes  int64  `json:"uncompressed_bytes"`
	StoredPayloadBytes int64  `json:"stored_payload_bytes"`
}

func (r *Repository) WebSocketJournalStorageEvidence(
	ctx context.Context,
	connectionPrefix string,
) (WebSocketJournalStorageEvidence, error) {
	connectionPrefix = strings.TrimSpace(connectionPrefix)
	if r == nil || r.pool == nil {
		return WebSocketJournalStorageEvidence{}, errors.New("WebSocket journal evidence repository is unavailable")
	}
	if connectionPrefix == "" || len(connectionPrefix) > 200 {
		return WebSocketJournalStorageEvidence{}, errors.New("WebSocket journal evidence connection prefix must contain 1..200 bytes")
	}
	evidence := WebSocketJournalStorageEvidence{ConnectionPrefix: connectionPrefix}
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE payload_encoding=0),
		       count(*) FILTER (WHERE payload_encoding=1),
		       COALESCE(sum(payload_uncompressed_bytes), 0),
		       COALESCE(sum(octet_length(payload)), 0),
		       COALESCE(sum(pg_column_size(frame)), 0)
		FROM websocket_frames AS frame
		WHERE left(connection_id, length($1))=$1`,
		connectionPrefix,
	).Scan(
		&evidence.Rows, &evidence.IdentityRows, &evidence.GZIPRows,
		&evidence.UncompressedBytes, &evidence.StoredPayloadBytes, &evidence.TupleBytes,
	); err != nil {
		return WebSocketJournalStorageEvidence{}, fmt.Errorf("collect WebSocket journal storage evidence: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT exchange_name, count(*),
		       COALESCE(sum(payload_uncompressed_bytes), 0),
		       COALESCE(sum(octet_length(payload)), 0)
		FROM websocket_frames
		WHERE left(connection_id, length($1))=$1
		GROUP BY exchange_name
		ORDER BY exchange_name`,
		connectionPrefix,
	)
	if err != nil {
		return WebSocketJournalStorageEvidence{}, fmt.Errorf("collect WebSocket journal exchange evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item WebSocketJournalExchangeStorageEvidence
		if err := rows.Scan(
			&item.Exchange, &item.Rows, &item.UncompressedBytes, &item.StoredPayloadBytes,
		); err != nil {
			return WebSocketJournalStorageEvidence{}, fmt.Errorf("scan WebSocket journal exchange evidence: %w", err)
		}
		evidence.Exchanges = append(evidence.Exchanges, item)
	}
	if err := rows.Err(); err != nil {
		return WebSocketJournalStorageEvidence{}, fmt.Errorf("iterate WebSocket journal exchange evidence: %w", err)
	}
	return evidence, nil
}
