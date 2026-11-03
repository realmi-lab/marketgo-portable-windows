package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/backfill"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool

	coverageMu         sync.Mutex
	coverageGapIDs     map[uint64]int64
	movementEvidenceMu sync.Mutex
	movementCandidates movementNoticeCandidateCache
}

type PoolStats struct {
	MaxConnections   int32
	TotalConnections int32
	Acquired         int32
	Idle             int32
	AcquireCount     int64
	AcquireDuration  time.Duration
}

func Open(ctx context.Context, databaseURL string, maxConnections int32) (*Repository, error) {
	return openRepository(ctx, databaseURL, maxConnections, false)
}

// OpenWhenReady keeps one pool during transient database startup/recovery.
// Only service entrypoints use it; validation, credentials and schema errors
// still fail. Cancellation stops waiting before any writer is acquired.
func OpenWhenReady(ctx context.Context, databaseURL string, maxConnections int32) (*Repository, error) {
	return openRepository(ctx, databaseURL, maxConnections, true)
}

func openRepository(ctx context.Context, databaseURL string, maxConnections int32, waitReady bool) (*Repository, error) {
	config, err := parsePoolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if maxConnections < 1 {
		maxConnections = 8
	}
	config.MaxConns = maxConnections
	config.MinConns = 1
	config.MaxConnIdleTime = 5 * time.Minute
	config.ConnConfig.RuntimeParams["application_name"] = "market_share_go"
	config.ConnConfig.RuntimeParams["timezone"] = "Asia/Seoul"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	if waitReady {
		err = waitDatabaseReady(ctx, pool.Ping, waitDatabaseRetry)
	} else {
		err = pool.Ping(ctx)
	}
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return &Repository{pool: pool, coverageGapIDs: make(map[uint64]int64)}, nil
}

func parsePoolConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// pgx parse errors can include the original URL. Do not let credentials
		// embedded in the URL escape through startup logs.
		return nil, errors.New("parse PostgreSQL configuration: invalid database URL")
	}
	return config, nil
}

func (r *Repository) Close() { r.pool.Close() }

func (r *Repository) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

func (r *Repository) PoolStats() PoolStats {
	stats := r.pool.Stat()
	return PoolStats{
		MaxConnections: stats.MaxConns(), TotalConnections: stats.TotalConns(),
		Acquired: stats.AcquiredConns(), Idle: stats.IdleConns(),
		AcquireCount: stats.AcquireCount(), AcquireDuration: stats.AcquireDuration(),
	}
}

func (r *Repository) Migrate(ctx context.Context) error { return Migrate(ctx, r.pool) }

func (r *Repository) ReadMetadata(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.pool.QueryRow(ctx, `SELECT value FROM aggregation_metadata WHERE key=$1`, key).Scan(&value)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read aggregation metadata: %w", err)
	}
	return value, true, nil
}

func (r *Repository) WriteMetadata(ctx context.Context, key, value string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO aggregation_metadata(key, value) VALUES ($1, $2)
		ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, value)
	if err != nil {
		return fmt.Errorf("write aggregation metadata: %w", err)
	}
	return nil
}

func (r *Repository) Flush(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow) error {
	return r.FlushWithCoverage(ctx, buckets, watermarks, recent, nil)
}

func (r *Repository) FlushWithCoverage(
	ctx context.Context,
	buckets []aggregate.Bucket,
	watermarks []aggregate.WatermarkRow,
	recent []aggregate.RecentIDRow,
	observation *coverage.State,
) error {
	return r.flushWithCoverage(ctx, buckets, watermarks, recent, observation, nil)
}

type ingestionCommit struct {
	previous, next int64
	fx             []byte
	derived        *IngestionDerived
	legacyDerived  bool
}

func (r *Repository) FlushWithCoverageAndCursor(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow, observation *coverage.State, previous, next int64) error {
	if previous < 0 || next <= previous {
		return errors.New("invalid computation cursor transition")
	}
	return r.flushWithCoverage(ctx, buckets, watermarks, recent, observation, &ingestionCommit{previous: previous, next: next})
}

func (r *Repository) FlushWithCoverageCursorAndFX(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow, observation *coverage.State, previous, next int64, fxCheckpoint []byte) error {
	if previous < 0 || next <= previous || len(fxCheckpoint) == 0 {
		return errors.New("invalid computation cursor or FX checkpoint")
	}
	return r.flushWithCoverage(ctx, buckets, watermarks, recent, observation, &ingestionCommit{previous: previous, next: next, fx: fxCheckpoint, legacyDerived: true})
}

// FlushWithCoverageCursorFXAndDerived commits restart state and transaction-only
// results together. The caller retains immutable inputs until acknowledgement.
func (r *Repository) FlushWithCoverageCursorFXAndDerived(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow, observation *coverage.State, previous, next int64, fxCheckpoint []byte, derived *IngestionDerived) error {
	if derived == nil || derived.Version != 1 {
		return errors.New("missing or unsupported typed ingestion results")
	}
	if previous < 0 || next <= previous || len(fxCheckpoint) == 0 {
		return errors.New("invalid computation cursor or FX checkpoint")
	}
	return r.flushWithCoverage(ctx, buckets, watermarks, recent, observation, &ingestionCommit{previous: previous, next: next, fx: fxCheckpoint, derived: derived})
}

func (r *Repository) flushWithCoverage(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow, observation *coverage.State, cursor *ingestionCommit) error {
	if len(buckets) == 0 && len(watermarks) == 0 && len(recent) == 0 && observation == nil && cursor == nil {
		return nil
	}
	if observation != nil {
		// Serializing coverage snapshots prevents two concurrent first flushes of
		// the same process-local gap token from inserting duplicate rows.
		r.coverageMu.Lock()
		defer r.coverageMu.Unlock()
		if r.coverageGapIDs == nil {
			r.coverageGapIDs = make(map[uint64]int64)
		}
		// Sequence values are intentionally reserved before the transaction. If
		// PostgreSQL commits but the client receives an ambiguous commit error, a
		// retry can upsert the same primary key instead of inserting a duplicate
		// logical gap.
		if err := reserveCoverageGapIDs(ctx, r.pool, observation.Gaps, r.coverageGapIDs); err != nil {
			return err
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if cursor != nil {
		var applied, committed int64
		if err := tx.QueryRow(ctx, `SELECT applied_cursor FROM computation_checkpoint WHERE singleton FOR UPDATE`).Scan(&applied); err != nil {
			return err
		}
		// Ambiguous commit retry has already installed these absolute values.
		if applied == cursor.next {
			return nil
		}
		if applied != cursor.previous {
			return fmt.Errorf("computation cursor conflict: stored=%d expected=%d", applied, cursor.previous)
		}
		if err := tx.QueryRow(ctx, `SELECT committed_cursor FROM ingestion_stream_head WHERE singleton`).Scan(&committed); err != nil {
			return err
		}
		if cursor.next > committed {
			return errors.New("computation cursor exceeds durable input")
		}
		var blocked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingestion_events WHERE cursor>$1 AND cursor<=$2 AND projection_error<>'')`, cursor.previous, cursor.next).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return errors.New("computation cursor crosses failed collection projection")
		}
		if _, err := tx.Exec(ctx, advanceIngestionCheckpointSQL, cursor.next, cursor.fx, cursor.previous); err != nil {
			return err
		}
	}
	batch := &pgx.Batch{}
	if len(buckets) > 0 {
		seconds := make([]int64, len(buckets))
		scopes := make([]string, len(buckets))
		exchanges := make([]string, len(buckets))
		markets := make([]string, len(buckets))
		bases := make([]string, len(buckets))
		quotes := make([]string, len(buckets))
		nativeVolumes := make([]float64, len(buckets))
		krwVolumes := make([]float64, len(buckets))
		tradeCounts := make([]uint64, len(buckets))
		unresolvedCounts := make([]uint64, len(buckets))
		buyKRWVolumes := make([]float64, len(buckets))
		sellKRWVolumes := make([]float64, len(buckets))
		baseVolumes := make([]float64, len(buckets))
		for index, row := range buckets {
			seconds[index] = int64(row.Second)
			scopes[index] = row.Key.Scope
			exchanges[index] = row.Key.Exchange
			markets[index] = row.Key.Market
			bases[index] = row.Key.Base
			quotes[index] = row.Key.Quote
			nativeVolumes[index] = row.Totals.NativeVolume
			krwVolumes[index] = row.Totals.KRWVolume
			tradeCounts[index] = row.Totals.TradeCount
			unresolvedCounts[index] = row.Totals.UnresolvedCount
			buyKRWVolumes[index] = row.Totals.BuyKRWVolume
			sellKRWVolumes[index] = row.Totals.SellKRWVolume
			baseVolumes[index] = row.Totals.BaseVolume
		}
		batch.Queue(`
            WITH incoming AS (
                SELECT values.*
                FROM unnest(
                    $1::bigint[], $2::text[], $3::text[], $4::text[],
                    $5::text[], $6::text[], $7::double precision[],
                    $8::double precision[], $9::bigint[], $10::bigint[],
                    $11::double precision[], $12::double precision[],
                    $13::double precision[]
                ) WITH ORDINALITY AS values(
                    second, scope, exchange_name, market, base, quote,
                    native_volume, krw_volume, trade_count, unresolved_count,
                    buy_krw_volume, sell_krw_volume, base_volume, ordinal
                )
            ), latest AS (
                SELECT DISTINCT ON (second, exchange_name, market)
                    second, scope, exchange_name, market, base, quote,
                    native_volume, krw_volume, trade_count, unresolved_count,
                    buy_krw_volume, sell_krw_volume, base_volume
                FROM incoming
                ORDER BY second, exchange_name, market, ordinal DESC
            )
            INSERT INTO volume_buckets (
                second, scope, exchange_name, market, base, quote,
                native_volume, krw_volume, trade_count, unresolved_count,
                buy_krw_volume, sell_krw_volume, base_volume,
                source_granularity_sec, source_kind, notional_method
            )
            SELECT incoming.second, incoming.scope, incoming.exchange_name,
                incoming.market, incoming.base, incoming.quote,
                incoming.native_volume, incoming.krw_volume,
                incoming.trade_count, incoming.unresolved_count,
                incoming.buy_krw_volume, incoming.sell_krw_volume,
                incoming.base_volume, 1, 'WEBSOCKET_TRADE', 'EXACT_TRADE'
            FROM latest AS incoming
            ON CONFLICT (second, exchange_name, market) DO UPDATE SET
                scope = EXCLUDED.scope,
                base = EXCLUDED.base,
                quote = EXCLUDED.quote,
                native_volume = EXCLUDED.native_volume,
                krw_volume = EXCLUDED.krw_volume,
                trade_count = EXCLUDED.trade_count,
                unresolved_count = EXCLUDED.unresolved_count,
                buy_krw_volume = EXCLUDED.buy_krw_volume,
                sell_krw_volume = EXCLUDED.sell_krw_volume,
                base_volume = EXCLUDED.base_volume,
                source_granularity_sec = 1,
                source_kind = 'WEBSOCKET_TRADE',
                notional_method = 'EXACT_TRADE'`,
			seconds, scopes, exchanges, markets, bases, quotes,
			nativeVolumes, krwVolumes, tradeCounts, unresolvedCounts,
			buyKRWVolumes, sellKRWVolumes, baseVolumes,
		)
	}
	if len(watermarks) > 0 {
		exchanges := make([]string, len(watermarks))
		markets := make([]string, len(watermarks))
		tradeIDs := make([]int64, len(watermarks))
		for index, row := range watermarks {
			exchanges[index] = string(row.Exchange)
			markets[index] = row.Market
			tradeIDs[index] = row.TradeID
		}
		batch.Queue(`
            INSERT INTO trade_watermarks(exchange_name, market, trade_id)
            SELECT incoming.exchange_name, incoming.market, MAX(incoming.trade_id)
            FROM unnest($1::text[], $2::text[], $3::bigint[])
                AS incoming(exchange_name, market, trade_id)
            GROUP BY incoming.exchange_name, incoming.market
            ON CONFLICT (exchange_name, market) DO UPDATE
            SET trade_id = GREATEST(trade_watermarks.trade_id, EXCLUDED.trade_id)`,
			exchanges, markets, tradeIDs)
	}
	if len(recent) > 0 {
		exchanges := make([]string, len(recent))
		markets := make([]string, len(recent))
		tradeIDs := make([]string, len(recent))
		eventSeconds := make([]int64, len(recent))
		for index, row := range recent {
			exchanges[index] = string(row.Exchange)
			markets[index] = row.Market
			tradeIDs[index] = row.TradeID
			eventSeconds[index] = int64(row.EventSecond)
		}
		batch.Queue(`
            INSERT INTO recent_trade_ids(exchange_name, market, trade_id, event_second)
            SELECT incoming.exchange_name, incoming.market, incoming.trade_id,
                MAX(incoming.event_second)
            FROM unnest($1::text[], $2::text[], $3::text[], $4::bigint[])
                AS incoming(exchange_name, market, trade_id, event_second)
            GROUP BY incoming.exchange_name, incoming.market, incoming.trade_id
            ON CONFLICT (exchange_name, market, trade_id) DO UPDATE
            SET event_second = GREATEST(recent_trade_ids.event_second, EXCLUDED.event_second)`,
			exchanges, markets, tradeIDs, eventSeconds)
	}
	results := tx.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		return fmt.Errorf("flush aggregation batch: %w", err)
	}
	var gapAssignments []coverageGapAssignment
	if observation != nil {
		gapAssignments, err = r.saveCoverage(ctx, tx, observation)
		if err != nil {
			return err
		}
	}
	if cursor != nil && len(cursor.fx) > 0 {
		derived := cursor.derived
		if cursor.legacyDerived {
			derived, err = decodeIngestionDerived(cursor.fx)
			if err != nil {
				return err
			}
		}
		if err := flushIngestionDerived(ctx, tx, derived); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit aggregation batch: %w", err)
	}
	if observation != nil {
		// A reserved identity becomes a persisted identity only after the
		// transaction reports a successful commit.
		for _, assignment := range gapAssignments {
			observation.Gaps[assignment.index].PersistenceID = assignment.id
		}
		// The supplied state is an absolute retained snapshot, so discarding
		// tokens no longer present also bounds the repository-side identity map.
		nextGapIDs := make(map[uint64]int64, len(observation.Gaps))
		for _, gap := range observation.Gaps {
			if gap.PersistenceToken != 0 && gap.PersistenceID > 0 {
				nextGapIDs[gap.PersistenceToken] = gap.PersistenceID
			}
		}
		r.coverageGapIDs = nextGapIDs
	}
	return nil
}

type State struct {
	Buckets        []aggregate.Bucket
	Watermarks     []aggregate.WatermarkRow
	RecentIDs      []aggregate.RecentIDRow
	Coverage       coverage.State
	CoverageLoaded bool
}

type bucketQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// StreamBuckets visits persisted buckets in database time order without first
// materializing the retained history in a second []Bucket. The callback must
// consume or copy the row before returning.
func (r *Repository) StreamBuckets(
	ctx context.Context,
	bucketSince domain.UnixSecond,
	consume func(aggregate.Bucket) error,
) error {
	return streamBuckets(ctx, r.pool, bucketSince, consume)
}

func streamBuckets(
	ctx context.Context,
	queryer bucketQueryer,
	bucketSince domain.UnixSecond,
	consume func(aggregate.Bucket) error,
) error {
	if consume == nil {
		return errors.New("stream aggregation buckets: consume callback is required")
	}
	rows, err := queryer.Query(ctx, `
        SELECT second, scope, exchange_name, market, base, quote,
               native_volume, krw_volume, trade_count, unresolved_count,
               buy_krw_volume, sell_krw_volume, base_volume
        FROM volume_buckets
        WHERE second > $1 AND source_kind NOT LIKE 'OFFICIAL_CANDLE%'
        ORDER BY second`, int64(bucketSince))
	if err != nil {
		return fmt.Errorf("query aggregation buckets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row aggregate.Bucket
		if err := rows.Scan(
			&row.Second, &row.Key.Scope, &row.Key.Exchange, &row.Key.Market, &row.Key.Base, &row.Key.Quote,
			&row.Totals.NativeVolume, &row.Totals.KRWVolume, &row.Totals.TradeCount, &row.Totals.UnresolvedCount,
			&row.Totals.BuyKRWVolume, &row.Totals.SellKRWVolume, &row.Totals.BaseVolume,
		); err != nil {
			return fmt.Errorf("scan aggregation bucket: %w", err)
		}
		if err := consume(row); err != nil {
			return fmt.Errorf("consume aggregation bucket: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read aggregation buckets: %w", err)
	}
	return nil
}

func (r *Repository) LoadState(
	ctx context.Context,
	bucketSince domain.UnixSecond,
	recentSince domain.UnixSecond,
) (State, error) {
	var buckets []aggregate.Bucket
	if err := r.StreamBuckets(ctx, bucketSince, func(row aggregate.Bucket) error {
		buckets = append(buckets, row)
		return nil
	}); err != nil {
		return State{}, err
	}
	state, err := r.LoadHydrationState(ctx, bucketSince, recentSince)
	if err != nil {
		return State{}, err
	}
	state.Buckets = buckets
	return state, nil
}

// LoadHydrationState loads only the compact checkpoint state that accompanies
// streamed buckets. Buckets are deliberately absent from the returned State.
func (r *Repository) LoadHydrationState(
	ctx context.Context,
	bucketSince domain.UnixSecond,
	recentSince domain.UnixSecond,
) (State, error) {
	return loadHydrationState(ctx, r.pool, bucketSince, recentSince)
}

func loadHydrationState(ctx context.Context, queryer hydrationQueryer, bucketSince, recentSince domain.UnixSecond) (State, error) {
	state := State{}
	watermarkRows, err := queryer.Query(ctx, `SELECT exchange_name, market, trade_id FROM trade_watermarks`)
	if err != nil {
		return state, err
	}
	defer watermarkRows.Close()
	for watermarkRows.Next() {
		var exchange string
		var row aggregate.WatermarkRow
		if err := watermarkRows.Scan(&exchange, &row.Market, &row.TradeID); err != nil {
			return state, err
		}
		row.Exchange = domain.Exchange(exchange)
		state.Watermarks = append(state.Watermarks, row)
	}
	if err := watermarkRows.Err(); err != nil {
		return state, err
	}
	watermarkRows.Close()

	recentRows, err := queryer.Query(ctx, `
        SELECT exchange_name, market, trade_id, event_second
        FROM recent_trade_ids
        WHERE event_second >= $1
        ORDER BY event_second, exchange_name, market, trade_id`, int64(recentSince))
	if err != nil {
		return state, err
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var exchange string
		var row aggregate.RecentIDRow
		if err := recentRows.Scan(&exchange, &row.Market, &row.TradeID, &row.EventSecond); err != nil {
			return state, err
		}
		row.Exchange = domain.Exchange(exchange)
		state.RecentIDs = append(state.RecentIDs, row)
	}
	if err := recentRows.Err(); err != nil {
		return state, err
	}
	recentRows.Close()
	if err := loadCoverage(ctx, queryer, bucketSince, &state); err != nil {
		return state, err
	}
	return state, nil
}

type coverageGapAssignment struct {
	index int
	id    int64
}

type gapIDSequence interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reserveCoverageGapIDs(
	ctx context.Context,
	sequence gapIDSequence,
	gaps []coverage.Gap,
	cached map[uint64]int64,
) error {
	resolved, err := resolveCoverageGapIDs(gaps, cached)
	if err != nil {
		return err
	}
	usedIDs := make(map[int64]struct{}, len(resolved))
	for _, id := range resolved {
		if id > 0 {
			usedIDs[id] = struct{}{}
		}
	}
	for index, gap := range gaps {
		if resolved[index] > 0 {
			continue
		}
		if gap.PersistenceToken == 0 {
			return fmt.Errorf("new observation gap %d has no stable persistence token", index)
		}
		var id int64
		if err := sequence.QueryRow(ctx, `
			SELECT nextval(pg_get_serial_sequence('observation_gap_events','gap_id'))`,
		).Scan(&id); err != nil {
			return fmt.Errorf("reserve observation gap ID for token %d: %w", gap.PersistenceToken, err)
		}
		if id <= 0 {
			return fmt.Errorf("reserve observation gap ID for token %d returned invalid ID %d", gap.PersistenceToken, id)
		}
		if _, exists := usedIDs[id]; exists {
			return fmt.Errorf("reserve observation gap ID for token %d returned duplicate ID %d", gap.PersistenceToken, id)
		}
		cached[gap.PersistenceToken] = id
		usedIDs[id] = struct{}{}
	}
	return nil
}

func resolveCoverageGapIDs(gaps []coverage.Gap, cached map[uint64]int64) ([]int64, error) {
	resolved := make([]int64, len(gaps))
	seenIDs := make(map[int64]int)
	seenTokens := make(map[uint64]int)
	for index, gap := range gaps {
		if gap.PersistenceID < 0 {
			return nil, fmt.Errorf("observation gap %d has invalid persistence ID %d", index, gap.PersistenceID)
		}
		if gap.PersistenceToken != 0 {
			if previous, exists := seenTokens[gap.PersistenceToken]; exists {
				return nil, fmt.Errorf("observation gaps %d and %d share persistence token %d", previous, index, gap.PersistenceToken)
			}
			seenTokens[gap.PersistenceToken] = index
		}
		id := gap.PersistenceID
		if cachedID, exists := cached[gap.PersistenceToken]; gap.PersistenceToken != 0 && exists {
			if cachedID <= 0 {
				return nil, fmt.Errorf("observation gap token %d has invalid cached persistence ID %d", gap.PersistenceToken, cachedID)
			}
			if id != 0 && id != cachedID {
				return nil, fmt.Errorf("observation gap %d persistence ID %d conflicts with cached ID %d", index, id, cachedID)
			}
			id = cachedID
		}
		if id != 0 {
			if previous, exists := seenIDs[id]; exists {
				return nil, fmt.Errorf("observation gaps %d and %d share persistence ID %d", previous, index, id)
			}
			seenIDs[id] = index
		}
		resolved[index] = id
	}
	return resolved, nil
}

func (r *Repository) saveCoverage(ctx context.Context, tx pgx.Tx, state *coverage.State) ([]coverageGapAssignment, error) {
	if _, err := tx.Exec(ctx, `
        INSERT INTO observation_coverage(singleton, contiguous_start, last_observed)
        VALUES (1, $1, $2)
        ON CONFLICT (singleton) DO UPDATE SET
            contiguous_start = EXCLUDED.contiguous_start,
			last_observed = EXCLUDED.last_observed
		WHERE (observation_coverage.contiguous_start, observation_coverage.last_observed)
			IS DISTINCT FROM (EXCLUDED.contiguous_start, EXCLUDED.last_observed)`,
		state.CoverageStart, state.LastObserved,
	); err != nil {
		return nil, fmt.Errorf("save observation coverage: %w", err)
	}
	// Coverage segments are an absolute snapshot. Retroactive loss detection can
	// shorten or remove an already persisted segment. Batch upsert the current
	// values, then remove rows absent from the snapshot; unchanged rows are not
	// rewritten, which avoids index churn and hundreds of per-second queries.
	segmentStarts := make([]int64, len(state.Segments))
	segmentEnds := make([]int64, len(state.Segments))
	for index, segment := range state.Segments {
		segmentStarts[index] = segment.Start
		segmentEnds[index] = segment.End
	}
	if len(segmentStarts) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO observation_segments(start_second, end_second)
			SELECT incoming.start_second, incoming.end_second
			FROM unnest($1::bigint[], $2::bigint[])
				AS incoming(start_second, end_second)
			ON CONFLICT (start_second) DO UPDATE SET
				end_second = EXCLUDED.end_second
			WHERE observation_segments.end_second IS DISTINCT FROM EXCLUDED.end_second`,
			segmentStarts, segmentEnds,
		); err != nil {
			return nil, fmt.Errorf("upsert observation segments: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM observation_segments
		WHERE NOT (start_second = ANY($1::bigint[]))`, segmentStarts); err != nil {
		return nil, fmt.Errorf("remove stale observation segments: %w", err)
	}
	gapIDs, err := resolveCoverageGapIDs(state.Gaps, r.coverageGapIDs)
	if err != nil {
		return nil, err
	}
	assignments := make([]coverageGapAssignment, 0)
	gapStarts := make([]int64, len(state.Gaps))
	gapEnds := make([]*int64, len(state.Gaps))
	gapStatuses := make([]string, len(state.Gaps))
	gapExchanges := make([]string, len(state.Gaps))
	gapMarkets := make([]string, len(state.Gaps))
	gapObservedVolumes := make([]*float64, len(state.Gaps))
	gapObservedTotals := make([]*float64, len(state.Gaps))
	gapObservedRisks := make([]*float64, len(state.Gaps))
	gapWeightWindows := make([]int64, len(state.Gaps))
	gapReasons := make([]string, len(state.Gaps))
	gapCreatedAt := make([]float64, len(state.Gaps))
	gapClosedAt := make([]*float64, len(state.Gaps))
	existingGapIDs := make([]int64, 0, len(state.Gaps))
	for index, gap := range state.Gaps {
		if gapIDs[index] <= 0 {
			return nil, fmt.Errorf("observation gap %d has no reserved persistence ID", index)
		}
		exchanges, err := json.Marshal(gap.Context.AffectedExchanges)
		if err != nil {
			return nil, err
		}
		markets, err := json.Marshal(gap.Context.AffectedMarkets)
		if err != nil {
			return nil, err
		}
		reasons, err := json.Marshal(gap.Context.ReasonCodes)
		if err != nil {
			return nil, err
		}
		createdAt := float64(gap.CreatedAt.UnixNano()) / 1e9
		var closedAt *float64
		if gap.ClosedAt != nil {
			value := float64(gap.ClosedAt.UnixNano()) / 1e9
			closedAt = &value
		}
		gapStarts[index] = gap.Start
		gapEnds[index] = gap.End
		gapStatuses[index] = gap.Status
		gapExchanges[index] = string(exchanges)
		gapMarkets[index] = string(markets)
		gapObservedVolumes[index] = gap.Context.ObservedVolumeKRW
		gapObservedTotals[index] = gap.Context.ObservedTotalVolumeKRW
		gapObservedRisks[index] = gap.Context.ObservedVolumeAtRisk
		gapWeightWindows[index] = gap.Context.WeightWindowSec
		gapReasons[index] = string(reasons)
		gapCreatedAt[index] = createdAt
		gapClosedAt[index] = closedAt
		if gap.PersistenceID > 0 {
			existingGapIDs = append(existingGapIDs, gapIDs[index])
			continue
		}
		assignments = append(assignments, coverageGapAssignment{index: index, id: gapIDs[index]})
	}
	if len(gapIDs) == 0 {
		return assignments, nil
	}
	if len(existingGapIDs) > 0 {
		var existingGapCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM observation_gap_events
			WHERE gap_id = ANY($1::bigint[])`, existingGapIDs).Scan(&existingGapCount); err != nil {
			return nil, fmt.Errorf("verify existing observation gaps: %w", err)
		}
		if existingGapCount != len(existingGapIDs) {
			return nil, fmt.Errorf("observation gap batch found %d of %d existing rows", existingGapCount, len(existingGapIDs))
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO observation_gap_events(
			gap_id, start_second, end_second, status, affected_exchanges_json,
			affected_markets_json, observed_volume_krw,
			observed_total_volume_krw, observed_volume_at_risk_pct,
			weight_window_sec, reason_codes_json, created_at, closed_at
		)
		SELECT incoming.gap_id, incoming.start_second, incoming.end_second,
			incoming.status, incoming.affected_exchanges_json,
			incoming.affected_markets_json, incoming.observed_volume_krw,
			incoming.observed_total_volume_krw, incoming.observed_volume_at_risk_pct,
			incoming.weight_window_sec, incoming.reason_codes_json,
			incoming.created_at, incoming.closed_at
		FROM unnest(
			$1::bigint[], $2::bigint[], $3::bigint[], $4::text[], $5::text[],
			$6::text[], $7::double precision[], $8::double precision[],
			$9::double precision[], $10::bigint[], $11::text[],
			$12::double precision[], $13::double precision[]
		) AS incoming(
			gap_id, start_second, end_second, status, affected_exchanges_json,
			affected_markets_json, observed_volume_krw,
			observed_total_volume_krw, observed_volume_at_risk_pct,
			weight_window_sec, reason_codes_json, created_at, closed_at
		)
		ON CONFLICT (gap_id) DO UPDATE SET
			start_second=EXCLUDED.start_second,
			end_second=EXCLUDED.end_second,
			status=CASE WHEN observation_gap_events.status=$14
				THEN observation_gap_events.status ELSE EXCLUDED.status END,
			affected_exchanges_json=EXCLUDED.affected_exchanges_json,
			affected_markets_json=EXCLUDED.affected_markets_json,
			observed_volume_krw=EXCLUDED.observed_volume_krw,
			observed_total_volume_krw=EXCLUDED.observed_total_volume_krw,
			observed_volume_at_risk_pct=EXCLUDED.observed_volume_at_risk_pct,
			weight_window_sec=EXCLUDED.weight_window_sec,
			reason_codes_json=EXCLUDED.reason_codes_json,
			closed_at=EXCLUDED.closed_at
		WHERE (
			observation_gap_events.start_second,
			observation_gap_events.end_second,
			observation_gap_events.status,
			observation_gap_events.affected_exchanges_json,
			observation_gap_events.affected_markets_json,
			observation_gap_events.observed_volume_krw,
			observation_gap_events.observed_total_volume_krw,
			observation_gap_events.observed_volume_at_risk_pct,
			observation_gap_events.weight_window_sec,
			observation_gap_events.reason_codes_json,
			observation_gap_events.closed_at
		) IS DISTINCT FROM (
			EXCLUDED.start_second,
			EXCLUDED.end_second,
			CASE WHEN observation_gap_events.status=$14
				THEN observation_gap_events.status ELSE EXCLUDED.status END,
			EXCLUDED.affected_exchanges_json,
			EXCLUDED.affected_markets_json,
			EXCLUDED.observed_volume_krw,
			EXCLUDED.observed_total_volume_krw,
			EXCLUDED.observed_volume_at_risk_pct,
			EXCLUDED.weight_window_sec,
			EXCLUDED.reason_codes_json,
			EXCLUDED.closed_at
		)`, gapIDs, gapStarts, gapEnds, gapStatuses, gapExchanges, gapMarkets,
		gapObservedVolumes, gapObservedTotals, gapObservedRisks, gapWeightWindows,
		gapReasons, gapCreatedAt, gapClosedAt, backfill.OfficialReconciliationStatus,
	); err != nil {
		return nil, fmt.Errorf("batch upsert observation gaps: %w", err)
	}
	var persistedGapCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM observation_gap_events
		WHERE gap_id = ANY($1::bigint[])`, gapIDs).Scan(&persistedGapCount); err != nil {
		return nil, fmt.Errorf("verify observation gap batch: %w", err)
	}
	if persistedGapCount != len(gapIDs) {
		return nil, fmt.Errorf("observation gap batch persisted %d of %d rows", persistedGapCount, len(gapIDs))
	}
	return assignments, nil
}

func (r *Repository) loadCoverage(ctx context.Context, since domain.UnixSecond, state *State) error {
	return loadCoverage(ctx, r.pool, since, state)
}

func loadCoverage(ctx context.Context, queryer hydrationQueryer, since domain.UnixSecond, state *State) error {
	var coverageStart, lastObserved int64
	err := queryer.QueryRow(ctx, `
        SELECT contiguous_start, last_observed
        FROM observation_coverage WHERE singleton=1`,
	).Scan(&coverageStart, &lastObserved)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if err == nil {
		if coverageStart <= 0 || lastObserved < 0 || lastObserved > 0 && coverageStart > lastObserved {
			return fmt.Errorf("invalid persisted observation coverage %d..%d", coverageStart, lastObserved)
		}
		state.CoverageLoaded = true
		state.Coverage.CoverageStart = coverageStart
		state.Coverage.LastObserved = lastObserved
	}
	segmentRows, err := queryer.Query(ctx, `
        SELECT start_second, end_second FROM observation_segments
        WHERE end_second >= $1 ORDER BY start_second`, int64(since))
	if err != nil {
		return err
	}
	for segmentRows.Next() {
		var segment coverage.Segment
		if err := segmentRows.Scan(&segment.Start, &segment.End); err != nil {
			segmentRows.Close()
			return err
		}
		if segment.Start <= 0 || segment.End < segment.Start {
			segmentRows.Close()
			return fmt.Errorf("invalid persisted observation segment %d..%d", segment.Start, segment.End)
		}
		state.Coverage.Segments = append(state.Coverage.Segments, segment)
	}
	if err := segmentRows.Err(); err != nil {
		segmentRows.Close()
		return err
	}
	segmentRows.Close()

	gapRows, err := queryer.Query(ctx, `
		SELECT gap_id, start_second, end_second, status, affected_exchanges_json,
               affected_markets_json, observed_volume_krw,
               observed_total_volume_krw, observed_volume_at_risk_pct,
               weight_window_sec, reason_codes_json, created_at, closed_at
        FROM observation_gap_events
        WHERE end_second IS NULL OR end_second >= $1
        ORDER BY gap_id`, int64(since))
	if err != nil {
		return err
	}
	defer gapRows.Close()
	for gapRows.Next() {
		var gap coverage.Gap
		var exchangesJSON, marketsJSON, reasonsJSON string
		var createdAt float64
		var closedAt *float64
		if err := gapRows.Scan(
			&gap.PersistenceID, &gap.Start, &gap.End, &gap.Status, &exchangesJSON, &marketsJSON,
			&gap.Context.ObservedVolumeKRW, &gap.Context.ObservedTotalVolumeKRW,
			&gap.Context.ObservedVolumeAtRisk, &gap.Context.WeightWindowSec,
			&reasonsJSON, &createdAt, &closedAt,
		); err != nil {
			return err
		}
		if err := decodeStringList(exchangesJSON, &gap.Context.AffectedExchanges); err != nil {
			return fmt.Errorf("decode observation gap affected exchanges: %w", err)
		}
		if err := decodeStringList(marketsJSON, &gap.Context.AffectedMarkets); err != nil {
			return fmt.Errorf("decode observation gap affected markets: %w", err)
		}
		if err := decodeStringList(reasonsJSON, &gap.Context.ReasonCodes); err != nil {
			return fmt.Errorf("decode observation gap reason codes: %w", err)
		}
		if gap.PersistenceID <= 0 {
			return fmt.Errorf("invalid persisted observation gap ID %d", gap.PersistenceID)
		}
		if gap.Start <= 0 || gap.End != nil && *gap.End < gap.Start {
			return fmt.Errorf("invalid persisted observation gap interval %d..%v", gap.Start, gap.End)
		}
		if gap.Context.WeightWindowSec < 1 || !finiteNonnegativePointers(
			gap.Context.ObservedVolumeKRW,
			gap.Context.ObservedTotalVolumeKRW,
			gap.Context.ObservedVolumeAtRisk,
		) {
			return fmt.Errorf("invalid persisted observation gap measurements at %d", gap.Start)
		}
		gap.CreatedAt, err = unixFloatTime(createdAt)
		if err != nil {
			return fmt.Errorf("decode observation gap created_at: %w", err)
		}
		if closedAt != nil {
			value, err := unixFloatTime(*closedAt)
			if err != nil {
				return fmt.Errorf("decode observation gap closed_at: %w", err)
			}
			gap.ClosedAt = &value
		}
		state.Coverage.Gaps = append(state.Coverage.Gaps, gap)
	}
	if err := gapRows.Err(); err != nil {
		gapRows.Close()
		return err
	}
	gapRows.Close()
	if err := loadHistoricalRecoveries(ctx, queryer, since, &state.Coverage); err != nil {
		return err
	}
	if len(state.Coverage.HistoricalRecoveries) > 0 {
		state.CoverageLoaded = true
	}
	return nil
}

func decodeStringList(raw string, target *[]string) error {
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return err
	}
	if *target == nil {
		*target = []string{}
	}
	return nil
}

func finiteNonnegativePointers(values ...*float64) bool {
	for _, value := range values {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return false
		}
	}
	return true
}

func unixFloatTime(value float64) (time.Time, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value >= float64(math.MaxInt64) {
		return time.Time{}, fmt.Errorf("invalid Unix timestamp %v", value)
	}
	seconds := int64(value)
	nanoseconds := int64((value - float64(seconds)) * 1e9)
	return time.Unix(seconds, nanoseconds), nil
}
