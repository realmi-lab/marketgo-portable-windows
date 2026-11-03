package postgres

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"market-share-go/internal/domain"
)

// WebSocketFrameStore uses an isolated one-connection pool. Raw ingestion can
// therefore apply its own backpressure without consuming the analysis pool.
type WebSocketFrameStore struct {
	pool *pgxpool.Pool
}

const (
	websocketPayloadIdentity int16 = 0
	websocketPayloadGZIP     int16 = 1
	minimumGZIPPayload             = 256
)

var websocketGZIPWriters = sync.Pool{New: func() any {
	writer, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	if err != nil {
		panic(err)
	}
	return writer
}}

func OpenWebSocketFrameStore(ctx context.Context, databaseURL string) (*WebSocketFrameStore, error) {
	config, err := parsePoolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	return openWebSocketFrameStore(ctx, config)
}

func openWebSocketFrameStore(ctx context.Context, config *pgxpool.Config) (*WebSocketFrameStore, error) {
	config.MaxConns = 1
	config.MinConns = 1
	config.MaxConnIdleTime = 5 * time.Minute
	config.ConnConfig.RuntimeParams["application_name"] = "market_share_websocket_journal"
	config.ConnConfig.RuntimeParams["timezone"] = "Asia/Seoul"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create WebSocket journal pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping WebSocket journal PostgreSQL: %w", err)
	}
	if err := checkWebSocketRetentionSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &WebSocketFrameStore{pool: pool}, nil
}

func (s *WebSocketFrameStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// AppendWebSocketFrames keeps retry identity even after payload expiry. Shared
// connection locks serialize an insert with archival of that same connection;
// an INSERT waiting on a deleted primary key cannot resurrect an archived row.
func (s *WebSocketFrameStore) AppendWebSocketFrames(ctx context.Context, frames []domain.WebSocketFrame) error {
	if len(frames) == 0 {
		return nil
	}
	batch, err := makeWebSocketFrameBatch(frames)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin WebSocket frame batch: %w", err)
	}
	defer rollbackWebSocketTransaction(tx)
	if err := lockWebSocketArchiveConnections(ctx, tx, batch.connectionIDs, batch.sequences, false); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO websocket_frames (
			exchange_name, connection_id, frame_sequence,
			message_type, received_at_ns, payload_encoding,
			payload_uncompressed_bytes, payload, frame_class, market
		)
		SELECT incoming.exchange_name, incoming.connection_id,
			incoming.frame_sequence, incoming.message_type,
			incoming.received_at_ns, incoming.payload_encoding,
			incoming.payload_uncompressed_bytes, incoming.payload,
			incoming.frame_class, incoming.market
		FROM unnest(
			$1::text[], $2::text[], $3::bigint[], $4::smallint[],
			$5::bigint[], $6::smallint[], $7::integer[], $8::bytea[],
			$9::text[], $10::text[]
		) AS incoming(
			exchange_name, connection_id, frame_sequence, message_type,
			received_at_ns, payload_encoding, payload_uncompressed_bytes, payload,
			frame_class, market
		)
		JOIN websocket_frame_archive_connections AS archived
		  ON archived.connection_id = incoming.connection_id
		 AND archived.sequence_block = (incoming.frame_sequence - 1) / 4096
		WHERE NOT (archived.archived_sequences @> incoming.frame_sequence)
		ON CONFLICT (connection_id, frame_sequence) DO NOTHING`,
		batch.exchanges, batch.connectionIDs, batch.sequences, batch.messageTypes,
		batch.receivedAtNS, batch.payloadEncodings, batch.uncompressedBytes, batch.payloads,
		batch.frameClasses, batch.markets,
	); err != nil {
		return fmt.Errorf("insert WebSocket frame batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit WebSocket frame batch: %w", err)
	}
	return nil
}

type websocketFrameBatch struct {
	exchanges         []string
	connectionIDs     []string
	sequences         []int64
	messageTypes      []int16
	receivedAtNS      []int64
	payloadEncodings  []int16
	uncompressedBytes []int32
	payloads          [][]byte
	frameClasses      []string
	markets           []string
}

func makeWebSocketFrameBatch(frames []domain.WebSocketFrame) (websocketFrameBatch, error) {
	batch := websocketFrameBatch{
		exchanges:         make([]string, len(frames)),
		connectionIDs:     make([]string, len(frames)),
		sequences:         make([]int64, len(frames)),
		messageTypes:      make([]int16, len(frames)),
		receivedAtNS:      make([]int64, len(frames)),
		payloadEncodings:  make([]int16, len(frames)),
		uncompressedBytes: make([]int32, len(frames)),
		payloads:          make([][]byte, len(frames)),
		frameClasses:      make([]string, len(frames)),
		markets:           make([]string, len(frames)),
	}
	for index, frame := range frames {
		if !frame.Exchange.Valid() || frame.Sequence == 0 || frame.Sequence > math.MaxInt64 || frame.ConnectionID == "" || frame.ReceivedAtNS <= 0 || len(frame.Payload) > math.MaxInt32 {
			return websocketFrameBatch{}, errors.New("invalid WebSocket frame identity, receive time or payload length")
		}
		frameClass := frame.FrameClass
		if frameClass == "" {
			frameClass = domain.FrameUnknown
		}
		if !domain.ValidWebSocketFrameClass(frameClass) {
			return websocketFrameBatch{}, fmt.Errorf("invalid WebSocket frame class %q", frameClass)
		}
		if len(frame.Market) > domain.WebSocketFrameMarketMaxBytes || !utf8.ValidString(frame.Market) {
			return websocketFrameBatch{}, errors.New("invalid WebSocket frame market identifier")
		}
		payload, encoding, err := encodeWebSocketPayload(frame.Payload)
		if err != nil {
			return websocketFrameBatch{}, fmt.Errorf("compress WebSocket frame payload: %w", err)
		}
		batch.exchanges[index] = string(frame.Exchange)
		batch.connectionIDs[index] = frame.ConnectionID
		batch.sequences[index] = int64(frame.Sequence)
		batch.messageTypes[index] = frame.MessageType
		batch.receivedAtNS[index] = frame.ReceivedAtNS
		batch.payloadEncodings[index] = encoding
		batch.uncompressedBytes[index] = int32(len(frame.Payload))
		batch.payloads[index] = payload
		batch.frameClasses[index] = frameClass
		batch.markets[index] = frame.Market
	}
	return batch, nil
}

func encodeWebSocketPayload(payload []byte) ([]byte, int16, error) {
	if len(payload) < minimumGZIPPayload {
		return payload, websocketPayloadIdentity, nil
	}
	var compressed bytes.Buffer
	compressed.Grow(len(payload) / 2)
	writer := websocketGZIPWriters.Get().(*gzip.Writer)
	writer.Reset(&compressed)
	_, writeErr := writer.Write(payload)
	closeErr := writer.Close()
	writer.Reset(io.Discard)
	websocketGZIPWriters.Put(writer)
	if err := errors.Join(writeErr, closeErr); err != nil {
		return nil, 0, err
	}
	if compressed.Len() >= len(payload) {
		return payload, websocketPayloadIdentity, nil
	}
	return compressed.Bytes(), websocketPayloadGZIP, nil
}

func DecodeWebSocketPayload(payload []byte, encoding int16, uncompressedBytes int) ([]byte, error) {
	if uncompressedBytes < 0 {
		return nil, errors.New("negative WebSocket payload length")
	}
	switch encoding {
	case websocketPayloadIdentity:
		if len(payload) != uncompressedBytes {
			return nil, fmt.Errorf("identity WebSocket frame length %d, want %d", len(payload), uncompressedBytes)
		}
		return append([]byte(nil), payload...), nil
	case websocketPayloadGZIP:
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("open WebSocket frame gzip payload: %w", err)
		}
		decoded, readErr := io.ReadAll(io.LimitReader(reader, int64(uncompressedBytes)+1))
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("decode WebSocket frame gzip payload: %w", err)
		}
		if len(decoded) != uncompressedBytes {
			return nil, fmt.Errorf("decoded WebSocket frame length %d, want %d", len(decoded), uncompressedBytes)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported WebSocket payload encoding %d", encoding)
	}
}

func (s *WebSocketFrameStore) PoolStats() PoolStats {
	if s == nil || s.pool == nil {
		return PoolStats{}
	}
	stats := s.pool.Stat()
	return PoolStats{
		MaxConnections: stats.MaxConns(), TotalConnections: stats.TotalConns(),
		Acquired: stats.AcquiredConns(), Idle: stats.IdleConns(),
		AcquireCount: stats.AcquireCount(), AcquireDuration: stats.AcquireDuration(),
	}
}

// WebSocketFramesBetween reads a bounded chronological slice through the main
// repository pool. Replay inspection must not borrow the journal's dedicated
// single write connection and stall live frame persistence.
func (r *Repository) WebSocketFramesBetween(
	ctx context.Context,
	exchange domain.Exchange,
	startNS, endNS int64,
	limit int,
) ([]domain.WebSocketFrame, bool, error) {
	if !exchange.Valid() || startNS <= 0 || endNS < startNS {
		return nil, false, errors.New("invalid WebSocket replay range")
	}
	if limit < 1 {
		return nil, false, errors.New("WebSocket replay limit must be positive")
	}
	rows, err := r.pool.Query(ctx, `
        SELECT connection_id, frame_sequence, message_type, received_at_ns,
               payload_encoding, payload_uncompressed_bytes, payload, frame_class, market
        FROM websocket_frames
        WHERE exchange_name=$1 AND received_at_ns BETWEEN $2 AND $3
        ORDER BY received_at_ns, connection_id, frame_sequence
        LIMIT $4`, exchange, startNS, endNS, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("query WebSocket replay frames: %w", err)
	}
	defer rows.Close()
	frames := make([]domain.WebSocketFrame, 0, min(limit, 1024))
	truncated := false
	for rows.Next() {
		var frame domain.WebSocketFrame
		var sequence int64
		var encoding int16
		var uncompressedBytes int
		var payload []byte
		if err := rows.Scan(
			&frame.ConnectionID, &sequence, &frame.MessageType, &frame.ReceivedAtNS,
			&encoding, &uncompressedBytes, &payload,
			&frame.FrameClass, &frame.Market,
		); err != nil {
			return nil, false, fmt.Errorf("scan WebSocket replay frame: %w", err)
		}
		if len(frames) == limit {
			truncated = true
			break
		}
		decoded, err := DecodeWebSocketPayload(payload, encoding, uncompressedBytes)
		if err != nil {
			return nil, false, err
		}
		frame.Exchange = exchange
		frame.Sequence = uint64(sequence)
		frame.Payload = decoded
		frames = append(frames, frame)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return frames, truncated, nil
}
