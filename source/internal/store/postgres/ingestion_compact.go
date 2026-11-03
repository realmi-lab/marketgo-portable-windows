package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/ingestlog"
)

const compactIngestionSetting = "ingestion-storage-codec"

// This setting is read when opening the writer; changing it never interrupts a
// running collector. DB schema and compatible readers must be staged first.
func (w *IngestionWriter) loadCompactSetting(ctx context.Context) error {
	var setting string
	if err := w.conn.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM aggregation_metadata WHERE key=$1),'')`, compactIngestionSetting).Scan(&setting); err != nil {
		return err
	}
	if setting != "" && setting != "legacy" && setting != "compact-v1" {
		return errors.New("unsupported ingestion storage codec setting")
	}
	if setting == "compact-v1" {
		var ready bool
		if err := w.conn.QueryRow(ctx, `SELECT to_regclass('ingestion_frame_blocks') IS NOT NULL AND to_regclass('ingestion_snapshot_blocks') IS NOT NULL`).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return errors.New("compact ingestion schema is not prepared")
		}
		w.compact = true
	}
	return nil
}

// Store each kind independently, so permanent registry/loss history cannot pin
// frame or periodic snapshot blocks. Encoding never changes original hashes.
func storeCompactIngestion(ctx context.Context, tx pgx.Tx, events []ingestlog.Event, rows [][]any) error {
	var day int64
	if err := tx.QueryRow(ctx, `SELECT floor(extract(epoch FROM clock_timestamp())/86400)::bigint`).Scan(&day); err != nil {
		return err
	}
	groups := make(map[string][]int)
	for i, event := range events {
		// Exceptional large envelopes retain the compatible legacy encoding.
		// A compression limit must never discard valid received evidence.
		if len(event.Payload) > compactIngestionMaxBytes/2 {
			continue
		}
		groups[event.Kind] = append(groups[event.Kind], i)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, kind := range keys {
		remaining := groups[kind]
		for len(remaining) > 0 {
			n, size := 0, 0
			for n < len(remaining) && size+len(events[remaining[n]].Payload) <= compactIngestionMaxBytes/2 {
				size += len(events[remaining[n]].Payload)
				n++
			}
			indices := remaining[:n]
			remaining = remaining[n:]
			batch := make([]ingestlog.Event, len(indices))
			for i, index := range indices {
				batch[i] = events[index]
			}
			block, err := encodeCompactIngestion(batch)
			if errors.Is(err, errCompactIngestionCapacity) {
				// JSON escaping may grow unusual but valid extension fields.
				// Retain their original representation instead of blocking input.
				continue
			}
			if err != nil {
				return err
			}
			first, last := batch[0].Cursor, batch[len(batch)-1].Cursor
			table := "ingestion_snapshot_blocks"
			if kind == "frame" {
				table = "ingestion_frame_blocks"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO `+table+` (day,first_cursor,last_cursor,version,replay,raw,replay_hash,raw_hash) VALUES($1,$2,$3,1,$4,$5,$6,$7)`, day, first, last, block.Replay, block.Raw, block.ReplayHash[:], block.RawHash[:]); err != nil {
				return err
			}
			for i, index := range indices {
				// Exact allocation of block bytes, not an assertion about disk pages.
				size := len(block.Replay) + len(block.Raw)
				allocation := size / len(indices)
				if i < size%len(indices) {
					allocation++
				}
				header, err := compactIngestionHeader(events[index], day, first, allocation)
				if err != nil {
					return err
				}
				rows[index][5] = json.RawMessage(header)
			}
		}
	}
	return nil
}

type compactBlockKey struct{ day, first int64 }

// Compute reads omit the raw column entirely. Full reads reconstruct byte-exact
// exchange payloads; both paths authenticate their independently compressed data.
func resolveCompactIngestion(ctx context.Context, tx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, events []ingestlog.Event, includeRaw bool) error {
	pointers := make([]compactIngestionPointer, len(events))
	for i, event := range events {
		pointer, err := compactPointer(event.Payload)
		if err != nil {
			return err
		}
		pointers[i] = pointer
	}
	for _, frames := range []bool{true, false} {
		wanted := map[compactBlockKey][]int{}
		var days, firsts []int64
		for i, pointer := range pointers {
			if pointer.Version == 0 || (events[i].Kind == "frame") != frames {
				continue
			}
			key := compactBlockKey{pointer.Day, pointer.Block}
			if _, exists := wanted[key]; !exists {
				days, firsts = append(days, key.day), append(firsts, key.first)
			}
			wanted[key] = append(wanted[key], i)
		}
		if len(wanted) == 0 {
			continue
		}
		table := "ingestion_snapshot_blocks"
		if frames {
			table = "ingestion_frame_blocks"
		}
		rows, err := tx.Query(ctx, `SELECT b.day,b.first_cursor,b.version,b.replay,b.replay_hash,CASE WHEN $3 THEN b.raw END,CASE WHEN $3 THEN b.raw_hash END FROM `+table+` b JOIN unnest($1::bigint[],$2::bigint[]) AS requested(day,first_cursor) USING(day,first_cursor)`, days, firsts, includeRaw && frames)
		if err != nil {
			return err
		}
		// Decode one bounded block at a time, assigning only requested records.
		for rows.Next() {
			var key compactBlockKey
			var version int
			var replay, replayHash, raw, rawHash []byte
			if err := rows.Scan(&key.day, &key.first, &version, &replay, &replayHash, &raw, &rawHash); err != nil {
				rows.Close()
				return err
			}
			if version != compactIngestionVersion {
				rows.Close()
				return errors.New("unsupported compact ingestion block version")
			}
			replay, err = expandIngestionBytes(replay, replayHash)
			if err != nil {
				rows.Close()
				return err
			}
			records, err := readCompactRecords(replay)
			if err != nil {
				rows.Close()
				return err
			}
			var originals map[int64][]byte
			if includeRaw && frames {
				raw, err = expandIngestionBytes(raw, rawHash)
				if err == nil {
					originals, err = readCompactRecords(raw)
				}
				if err != nil {
					rows.Close()
					return err
				}
			}
			// Index by block once instead of rescanning the whole requested batch
			// after every block. Frame and snapshot tables have separate indices.
			for _, i := range wanted[key] {
				payload, exists := records[events[i].Cursor]
				if !exists {
					rows.Close()
					return errors.New("compact ingestion record missing; refusing cursor advance")
				}
				if includeRaw && frames {
					payload, err = restoreCompactFrame(payload, originals[events[i].Cursor])
					if err != nil {
						rows.Close()
						return err
					}
				}
				events[i].Payload = bytes.Clone(payload)
			}
			delete(wanted, key)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(wanted) != 0 {
			return fmt.Errorf("%d compact ingestion blocks missing; historical recovery required", len(wanted))
		}
	}
	return nil
}

// Purge only blocks whose entire cursor span is already archived. The operation
// follows normal retention and can never shorten it or delete a pending/error
// input. Old readers remain compatible while compact writes are disabled.
func (r *Repository) PruneCompactIngestion(parent context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	var prepared bool
	if err := r.pool.QueryRow(ctx, `SELECT to_regclass('ingestion_frame_blocks') IS NOT NULL`).Scan(&prepared); err != nil || !prepared {
		return 0, err
	}
	var total int64
	for _, table := range []string{"ingestion_frame_blocks", "ingestion_snapshot_blocks"} {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return total, err
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer stop()
			_ = tx.Rollback(cleanup)
		}()
		if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='800ms'`); err != nil {
			return total, err
		}
		var locked bool
		if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, ingestionMaintenanceLock).Scan(&locked); err != nil {
			return total, err
		}
		if !locked {
			return total, nil
		}
		key := "compact-prune-" + table
		var saved string
		if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM aggregation_metadata WHERE key=$1),'{}')`, key).Scan(&saved); err != nil {
			return total, err
		}
		var position struct{ Day, First int64 }
		if err = json.Unmarshal([]byte(saved), &position); err != nil {
			return total, err
		}
		if position.Day < 0 || position.First < 0 {
			return total, errors.New("invalid compact cleanup position")
		}
		var count int64
		// Bound the index walk as well as deletion. Registry/loss anchors and
		// unresolved frames are revisited after wrap, never an unbounded scan.
		err = tx.QueryRow(ctx, `WITH scan AS MATERIALIZED (
 SELECT day,first_cursor FROM `+table+` WHERE (day,first_cursor)>($1,$2)
 AND day<=floor(extract(epoch FROM clock_timestamp()-interval '48 hours')/86400)::bigint
 ORDER BY day,first_cursor LIMIT 512
), candidates AS MATERIALIZED (
 SELECT b.day,b.first_cursor FROM `+table+` b JOIN scan s USING(day,first_cursor)
 WHERE b.created_at < clock_timestamp()-interval '48 hours'
 AND b.last_cursor <= (SELECT applied_cursor FROM computation_checkpoint WHERE singleton)
 AND NOT EXISTS (SELECT 1 FROM ingestion_events e WHERE e.cursor BETWEEN b.first_cursor AND b.last_cursor
  AND e.archived_at IS NULL AND (e.payload->>'CompactBlock')=b.first_cursor::text
  AND (e.payload->>'CompactDay')=b.day::text)
 ORDER BY b.day,b.first_cursor FOR UPDATE OF b SKIP LOCKED
), removed AS (DELETE FROM `+table+` b USING candidates c WHERE b.day=c.day AND b.first_cursor=c.first_cursor RETURNING 1)
SELECT (SELECT count(*) FROM removed),COALESCE((SELECT day FROM scan ORDER BY day DESC,first_cursor DESC LIMIT 1),0),
COALESCE((SELECT first_cursor FROM scan ORDER BY day DESC,first_cursor DESC LIMIT 1),0)`, position.Day, position.First).Scan(&count, &position.Day, &position.First)
		if err != nil {
			return total, err
		}
		encoded, err := json.Marshal(position)
		if err != nil {
			return total, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO aggregation_metadata(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, string(encoded)); err != nil {
			return total, err
		}
		if err = tx.Commit(ctx); err != nil {
			return total, err
		}
		total += count
	}
	return total, nil
}
