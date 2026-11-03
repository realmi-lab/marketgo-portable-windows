// Package sqlite provides the desktop-only ephemeral storage adapter. It uses
// a named SQLite in-memory database: no database file, WAL, or journal is ever
// created on disk, and closing the process discards every row.
package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/reconcile"
	"market-share-go/services/collector/registry"
	"market-share-go/internal/store/postgres"
	"market-share-go/internal/tickertrend"
)

const BackendName = "sqlite-memory"

type Repository struct {
	db *sql.DB
}

func OpenMemory(ctx context.Context) (*Repository, error) {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("create SQLite memory identity: %w", err)
	}
	// mode=memory is the critical no-file contract. The unique shared-cache name
	// keeps every database/sql connection on the same transient database while
	// preventing two services in one process from seeing each other's rows.
	dsn := "file:market-share-" + hex.EncodeToString(token[:]) + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open in-memory SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	repository := &Repository{db: db}
	if err := repository.initialize(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return repository, nil
}

func (r *Repository) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys=ON`,
		`PRAGMA journal_mode=MEMORY`,
		`PRAGMA temp_store=MEMORY`,
		`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE registry_refreshes (
			observed_at_ms INTEGER NOT NULL,
			exchange_name TEXT NOT NULL,
			payload_json BLOB NOT NULL,
			PRIMARY KEY(observed_at_ms, exchange_name)
		)`,
		`CREATE TABLE movement_samples (
			as_of_ms INTEGER PRIMARY KEY,
			source TEXT NOT NULL,
			payload_json BLOB NOT NULL
		)`,
		`CREATE TABLE movement_events (
			event_id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			event_json BLOB NOT NULL,
			baseline_json BLOB,
			current_json BLOB
		)`,
		`CREATE TABLE ticker_samples (
			bucket_ms INTEGER PRIMARY KEY,
			payload_json BLOB NOT NULL
		)`,
		`CREATE TABLE realtime_flow (
			as_of_ms INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			payload_json BLOB NOT NULL
		)`,
		`CREATE TABLE notices (
			exchange_name TEXT NOT NULL,
			notice_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			anchor_ms INTEGER NOT NULL,
			payload_json BLOB NOT NULL,
			PRIMARY KEY(exchange_name, notice_id)
		)`,
		`CREATE TABLE notice_sources (
			exchange_name TEXT PRIMARY KEY,
			last_scan REAL NOT NULL,
			window_start_ms INTEGER NOT NULL,
			payload_json BLOB NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize in-memory SQLite: %w", err)
		}
	}
	var backingFile string
	if err := r.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&backingFile); err != nil {
		return fmt.Errorf("verify SQLite memory database: %w", err)
	}
	if strings.TrimSpace(backingFile) != "" {
		return fmt.Errorf("SQLite memory contract failed: unexpected backing file %q", backingFile)
	}
	return nil
}

func (r *Repository) Close() {
	if r != nil && r.db != nil {
		_ = r.db.Close()
	}
}

func (r *Repository) Ping(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("in-memory SQLite is closed")
	}
	return r.db.PingContext(ctx)
}

// FlushWithCoverage deliberately does not duplicate the high-rate trade
// buckets, raw frames, or exact trade IDs. They remain in the bounded runtime
// owner and disappear on exit; the desktop mode has no restart hydration.
func (r *Repository) FlushWithCoverage(
	context.Context,
	[]aggregate.Bucket,
	[]aggregate.WatermarkRow,
	[]aggregate.RecentIDRow,
	*coverage.State,
) error {
	return nil
}

func (r *Repository) StreamBuckets(
	_ context.Context,
	_ domain.UnixSecond,
	consume func(aggregate.Bucket) error,
) error {
	if consume == nil {
		return errors.New("stream aggregation buckets: consume callback is required")
	}
	return nil
}

func (r *Repository) LoadHydrationState(
	context.Context,
	domain.UnixSecond,
	domain.UnixSecond,
) (postgres.State, error) {
	return postgres.State{}, nil
}

func (r *Repository) ReadMetadata(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (r *Repository) WriteMetadata(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (r *Repository) PruneRecentTradeIDs(context.Context, domain.UnixSecond, int) (int64, error) {
	return 0, nil
}

func (r *Repository) SaveMarketRegistryObservation(
	ctx context.Context,
	snapshot registry.Snapshot,
	universe domain.MarketUniverse,
	exchanges []domain.Exchange,
) error {
	observedAtMS := int64(snapshot.UpdatedAt * 1_000)
	if observedAtMS <= 0 {
		observedAtMS = time.Now().UnixMilli()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, exchange := range exchanges {
		row, ok := snapshot.Exchanges[exchange]
		if !ok {
			continue
		}
		previous := 0
		var previousPayload []byte
		queryErr := tx.QueryRowContext(ctx, `SELECT payload_json FROM registry_refreshes
			WHERE exchange_name=? ORDER BY observed_at_ms DESC LIMIT 1`, exchange).Scan(&previousPayload)
		if queryErr == nil {
			var prior registry.RefreshObservation
			if json.Unmarshal(previousPayload, &prior) == nil {
				previous = prior.ActiveMarketCount
			}
		} else if !errors.Is(queryErr, sql.ErrNoRows) {
			return queryErr
		}
		markets := universe.ForExchange(exchange)
		refresh := registry.RefreshObservation{
			ObservedAtMS: observedAtMS, Exchange: exchange,
			Status: "SUCCESS", Authoritative: row.Error == "",
			RegistryVersion: int64(snapshot.Version), PreviousMarketCount: previous,
			ObservedMarketCount: len(markets), ActiveMarketCount: row.MarketCount,
			ActiveAssetCount: row.AssetCount, QuoteCounts: cloneQuoteCounts(row.Quotes),
			PublicFeeCount: row.PublicFeeMarketCount, Error: row.Error,
		}
		if row.Error != "" {
			refresh.Status = "ERROR"
			refresh.ObservedMarketCount = 0
		}
		if previous == 0 && refresh.Authoritative {
			refresh.DiscoveredCount = len(markets)
			refresh.AddedCount = len(markets)
		}
		payload, marshalErr := json.Marshal(refresh)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO registry_refreshes(observed_at_ms,exchange_name,payload_json)
			VALUES(?,?,?) ON CONFLICT(observed_at_ms,exchange_name) DO UPDATE SET payload_json=excluded.payload_json`,
			observedAtMS, exchange, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) LoadActiveMarketRegistry(context.Context) ([]domain.MarketPair, error) {
	// The desktop SQLite database is intentionally ephemeral, so there is no
	// prior process membership to restore.
	return []domain.MarketPair{}, nil
}

func (r *Repository) MarketRegistryHistory(ctx context.Context, filter registry.HistoryFilter) (registry.History, error) {
	limit := filter.Limit
	if limit < 1 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `SELECT payload_json FROM registry_refreshes
		WHERE (?=0 OR observed_at_ms>=?) AND (?=0 OR observed_at_ms<=?)
		AND (?='' OR exchange_name=?) ORDER BY observed_at_ms DESC LIMIT ?`,
		filter.FromMS, filter.FromMS, filter.ToMS, filter.ToMS, filter.Exchange, filter.Exchange, limit)
	if err != nil {
		return registry.History{}, err
	}
	defer rows.Close()
	result := registry.History{Refreshes: []registry.RefreshObservation{}, Events: []registry.LifecycleEvent{}, GeneratedAt: float64(time.Now().UnixNano()) / 1e9}
	for rows.Next() {
		var payload []byte
		var item registry.RefreshObservation
		if err := rows.Scan(&payload); err != nil {
			return registry.History{}, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return registry.History{}, err
		}
		result.Refreshes = append(result.Refreshes, item)
	}
	return result, rows.Err()
}

func (r *Repository) SaveMovementSample(ctx context.Context, snapshot *movement.Snapshot) error {
	if snapshot == nil {
		return errors.New("movement snapshot is nil")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO movement_samples(as_of_ms,source,payload_json) VALUES(?,?,?)
		ON CONFLICT(as_of_ms) DO UPDATE SET source=excluded.source,payload_json=excluded.payload_json`,
		snapshot.AsOfMS, snapshot.Source, payload)
	return err
}

func (r *Repository) SaveMovementEvent(
	ctx context.Context,
	event *movement.Event,
	baseline *movement.Snapshot,
	current *movement.Snapshot,
) error {
	if err := movement.ValidateEventDirection(event); err != nil {
		return err
	}
	eventPayload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	baselinePayload, err := json.Marshal(baseline)
	if err != nil {
		return err
	}
	currentPayload, err := json.Marshal(current)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO movement_events(event_id,source,updated_at_ms,event_json,baseline_json,current_json)
		VALUES(?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET source=excluded.source,
		updated_at_ms=excluded.updated_at_ms,event_json=excluded.event_json,
		baseline_json=excluded.baseline_json,current_json=excluded.current_json`,
		event.EventID, event.Source, event.UpdatedAtMS, eventPayload, baselinePayload, currentPayload)
	return err
}

func (r *Repository) RecentMovementSamplesBySource(
	ctx context.Context,
	source string,
	sinceMS int64,
	limit int,
) ([]movement.SamplePoint, error) {
	if limit < 1 {
		limit = 180
	}
	rows, err := r.db.QueryContext(ctx, `SELECT payload_json FROM movement_samples
		WHERE source=? AND as_of_ms>=? ORDER BY as_of_ms DESC LIMIT ?`, source, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movement.SamplePoint, 0, limit)
	for rows.Next() {
		var payload []byte
		var snapshot movement.Snapshot
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &snapshot); err != nil {
			return nil, err
		}
		result = append(result, movement.SamplePoint{AsOfMS: snapshot.AsOfMS, BithumbSharePct: snapshot.BithumbSharePct, Status: snapshot.Status})
	}
	reverse(result)
	return result, rows.Err()
}

func (r *Repository) RecentMovementEventsBySource(ctx context.Context, source string, limit int) ([]movement.Event, error) {
	if limit < 1 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, `SELECT event_json FROM movement_events
		WHERE source=? ORDER BY updated_at_ms DESC LIMIT ?`, source, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movement.Event, 0, limit)
	for rows.Next() {
		var payload []byte
		var event movement.Event
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *Repository) MovementEvent(ctx context.Context, eventID string) (*movement.Event, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT event_json FROM movement_events WHERE event_id=?`, eventID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, movement.ErrEventNotFound
	}
	if err != nil {
		return nil, err
	}
	var event movement.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *Repository) MovementEventSnapshot(ctx context.Context, eventID, kind string) (*movement.Snapshot, error) {
	column := "current_json"
	if strings.EqualFold(kind, "BASELINE") {
		column = "baseline_json"
	}
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT `+column+` FROM movement_events WHERE event_id=?`, eventID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, movement.ErrEventNotFound
	}
	if err != nil {
		return nil, err
	}
	var snapshot *movement.Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (r *Repository) SaveOfficialTickerMinuteSample(ctx context.Context, sample tickertrend.Sample) error {
	payload, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO ticker_samples(bucket_ms,payload_json) VALUES(?,?)
		ON CONFLICT(bucket_ms) DO UPDATE SET payload_json=excluded.payload_json`, sample.BucketMS, payload)
	return err
}

func (r *Repository) RecentOfficialTickerMinuteSamples(ctx context.Context, sinceMS int64, limit int) ([]tickertrend.Sample, error) {
	if limit < 1 {
		limit = 48
	}
	rows, err := r.db.QueryContext(ctx, `SELECT payload_json FROM ticker_samples
		WHERE bucket_ms>=? ORDER BY bucket_ms DESC LIMIT ?`, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result, err := scanTickerSamples(rows)
	if err != nil {
		return nil, err
	}
	reverse(result)
	return result, nil
}

func (r *Repository) OfficialTickerMinuteSamplesBetween(ctx context.Context, startMS, endMS int64, limit int) ([]tickertrend.Sample, error) {
	if startMS <= 0 || endMS < startMS || limit < 1 {
		return []tickertrend.Sample{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT payload_json FROM ticker_samples
		WHERE bucket_ms>=? AND bucket_ms<=? ORDER BY bucket_ms LIMIT ?`, startMS, endMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTickerSamples(rows)
}

func (r *Repository) OfficialTickerMinuteMarkets(ctx context.Context, buckets []int64) (map[int64]map[movement.AssetKey]float64, error) {
	result := make(map[int64]map[movement.AssetKey]float64, len(buckets))
	for _, bucket := range buckets {
		var payload []byte
		err := r.db.QueryRowContext(ctx, `SELECT payload_json FROM ticker_samples WHERE bucket_ms=?`, bucket).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var sample tickertrend.Sample
		if err := json.Unmarshal(payload, &sample); err != nil {
			return nil, err
		}
		result[bucket] = cloneAssetVolumes(sample.Snapshot.Assets)
	}
	return result, nil
}

func (r *Repository) SaveRealtimeFlowStateWithSummary(
	ctx context.Context,
	state marketanalysis.RealtimeFlowState,
) (marketanalysis.RealtimeFlowState, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return marketanalysis.RealtimeFlowState{}, err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO realtime_flow(as_of_ms,status,payload_json) VALUES(?,?,?)
		ON CONFLICT(as_of_ms) DO UPDATE SET status=excluded.status,payload_json=excluded.payload_json`,
		state.AsOfMS, strings.ToUpper(state.Status), payload)
	if err != nil {
		return marketanalysis.RealtimeFlowState{}, err
	}
	return state, nil
}

func (r *Repository) RealtimeFlowHistory(ctx context.Context, filter marketanalysis.RealtimeFlowHistoryFilter) ([]marketanalysis.RealtimeFlowState, error) {
	limit := filter.Limit
	if limit < 1 {
		limit = 100
	}
	limit = min(limit, 9_000)
	rows, err := r.db.QueryContext(ctx, `SELECT payload_json FROM realtime_flow
		WHERE (?=0 OR as_of_ms>=?) AND (?=0 OR as_of_ms<=?) AND (?='' OR status=?)
		ORDER BY as_of_ms DESC LIMIT 9000`, filter.FromMS, filter.FromMS, filter.ToMS, filter.ToMS,
		strings.ToUpper(strings.TrimSpace(filter.Status)), strings.ToUpper(strings.TrimSpace(filter.Status)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]marketanalysis.RealtimeFlowState, 0, limit)
	for rows.Next() && len(result) < limit {
		var payload []byte
		var state marketanalysis.RealtimeFlowState
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &state); err != nil {
			return nil, err
		}
		if !realtimeStateMatches(state, filter) {
			continue
		}
		result = append(result, state)
	}
	return result, rows.Err()
}

func (r *Repository) RealtimeFlowSummaryHistory(ctx context.Context, fromMS, toMS int64) ([]marketanalysis.RealtimeFlowState, error) {
	if toMS <= fromMS {
		return []marketanalysis.RealtimeFlowState{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT payload_json FROM realtime_flow
		WHERE as_of_ms>? AND as_of_ms<=? ORDER BY as_of_ms`, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]marketanalysis.RealtimeFlowState, 0, int((toMS-fromMS)/10_000)+1)
	for rows.Next() {
		var payload []byte
		var state marketanalysis.RealtimeFlowState
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &state); err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, rows.Err()
}

func (r *Repository) SaveHermesNoticeBatch(ctx context.Context, request notice.HermesIngestRequest) (notice.HermesIngestResult, error) {
	result := notice.HermesIngestResult{BatchID: request.BatchID, Accepted: len(request.Items), WindowStartMS: request.WindowStartMS}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	exchanges := make(map[string]struct{})
	for _, exchange := range request.ScannedExchanges {
		exchanges[exchange] = struct{}{}
	}
	for _, item := range request.Items {
		var previousHash string
		err := tx.QueryRowContext(ctx, `SELECT content_hash FROM notices WHERE exchange_name=? AND notice_id=?`, item.Exchange, item.NoticeID).Scan(&previousHash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		if previousHash != item.ContentHash {
			result.CreatedRevisions++
		}
		payload, err := json.Marshal(item)
		if err != nil {
			return result, err
		}
		anchor := max(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
		if _, err := tx.ExecContext(ctx, `INSERT INTO notices(exchange_name,notice_id,content_hash,anchor_ms,payload_json)
			VALUES(?,?,?,?,?) ON CONFLICT(exchange_name,notice_id) DO UPDATE SET
			content_hash=excluded.content_hash,anchor_ms=excluded.anchor_ms,payload_json=excluded.payload_json`,
			item.Exchange, item.NoticeID, item.ContentHash, anchor, payload); err != nil {
			return result, err
		}
		exchanges[item.Exchange] = struct{}{}
	}
	now := float64(time.Now().UnixNano()) / 1e9
	for exchange := range exchanges {
		source := notice.Source{LastHistoryScanTS: now, HistoryStatus: "HERMES_30D_ACTIVE", HistoryNote: "Ephemeral SQLite session ingest", OldestFetchedTSMS: request.WindowStartMS}
		payload, marshalErr := json.Marshal(source)
		if marshalErr != nil {
			return result, marshalErr
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notice_sources(exchange_name,last_scan,window_start_ms,payload_json)
			VALUES(?,?,?,?) ON CONFLICT(exchange_name) DO UPDATE SET last_scan=excluded.last_scan,window_start_ms=excluded.window_start_ms,payload_json=excluded.payload_json`,
			exchange, now, request.WindowStartMS, payload); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.ScannedSources = len(exchanges)
	result.Unchanged = result.Accepted - result.CreatedRevisions
	return result, nil
}

func (r *Repository) NoticeSnapshot(ctx context.Context, since time.Time) (notice.Snapshot, error) {
	result := notice.Snapshot{
		HistoryDays: notice.HistoryDays, OCRBackend: "unavailable",
		Sources: map[string]notice.Source{}, Items: []notice.Item{}, ErrorHistory: []notice.SourceError{},
	}
	rows, err := r.db.QueryContext(ctx, `SELECT exchange_name,payload_json FROM notice_sources ORDER BY exchange_name`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var exchange string
		var payload []byte
		var source notice.Source
		if err := rows.Scan(&exchange, &payload); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal(payload, &source); err != nil {
			rows.Close()
			return result, err
		}
		result.Sources[exchange] = source
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	itemRows, err := r.db.QueryContext(ctx, `SELECT payload_json,anchor_ms FROM notices
		WHERE anchor_ms>=? ORDER BY anchor_ms DESC,exchange_name,notice_id`, since.UnixMilli())
	if err != nil {
		return result, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var payload []byte
		var anchor int64
		var item notice.Item
		if err := itemRows.Scan(&payload, &anchor); err != nil {
			return result, err
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
		result.Version = max(result.Version, anchor)
	}
	result.SourceCount = len(result.Sources)
	for exchange, source := range result.Sources {
		if source.Error == "" {
			result.HealthySourceCount++
		} else {
			result.ActiveErrorCount++
			result.ErrorHistory = append(result.ErrorHistory, notice.SourceError{
				Exchange: exchange, ErrorKind: source.ErrorKind, Message: source.Error,
				StartedTS: source.ErrorStartedTS, LastSeenTS: source.ErrorLastSeenTS,
				OccurrenceCount: source.ErrorOccurrences, BlockedUntilTS: source.BlockedUntilTS,
				Active: true,
			})
			result.Version = max(result.Version, int64(source.ErrorLastSeenTS*1_000))
		}
		if source.HistoryStatus == "COMPLETE" || source.HistoryStatus == "HERMES_30D_ACTIVE" {
			result.CompleteHistorySourceCount++
		}
	}
	sort.Slice(result.ErrorHistory, func(i, j int) bool {
		return result.ErrorHistory[i].StartedTS > result.ErrorHistory[j].StartedTS
	})
	return result, itemRows.Err()
}

func (r *Repository) RelinkRecentMovementNotices(context.Context) (map[string][]movement.NoticeEvidence, error) {
	return map[string][]movement.NoticeEvidence{}, nil
}

func (r *Repository) LoadNoticeSources(ctx context.Context) (map[string]notice.Source, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT exchange_name,payload_json FROM notice_sources`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]notice.Source)
	for rows.Next() {
		var exchange string
		var payload []byte
		var source notice.Source
		if err := rows.Scan(&exchange, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &source); err != nil {
			return nil, err
		}
		result[exchange] = source
	}
	return result, rows.Err()
}

func (r *Repository) LoadNoticeItem(ctx context.Context, exchange, noticeID string) (notice.Item, bool, error) {
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT payload_json FROM notices WHERE exchange_name=? AND notice_id=?`, exchange, noticeID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return notice.Item{}, false, nil
	}
	if err != nil {
		return notice.Item{}, false, err
	}
	var item notice.Item
	if err := json.Unmarshal(payload, &item); err != nil {
		return notice.Item{}, false, err
	}
	return item, true, nil
}

func (r *Repository) SaveNoticePoll(ctx context.Context, exchange string, items []notice.Item, source notice.Source) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		item.Exchange = exchange
		item.NoticeID = strings.TrimSpace(item.NoticeID)
		if item.NoticeID == "" {
			item.NoticeID = strings.TrimSpace(item.URL)
		}
		if item.NoticeID == "" {
			continue
		}
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		anchor := max(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
		if _, err := tx.ExecContext(ctx, `INSERT INTO notices(exchange_name,notice_id,content_hash,anchor_ms,payload_json)
			VALUES(?,?,?,?,?) ON CONFLICT(exchange_name,notice_id) DO UPDATE SET
			content_hash=excluded.content_hash,anchor_ms=excluded.anchor_ms,payload_json=excluded.payload_json`,
			exchange, item.NoticeID, item.ContentHash, anchor, payload); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(source)
	if err != nil {
		return err
	}
	windowStart := source.OldestFetchedTSMS
	if _, err := tx.ExecContext(ctx, `INSERT INTO notice_sources(exchange_name,last_scan,window_start_ms,payload_json)
		VALUES(?,?,?,?) ON CONFLICT(exchange_name) DO UPDATE SET last_scan=excluded.last_scan,
		window_start_ms=excluded.window_start_ms,payload_json=excluded.payload_json`,
		exchange, source.LastHistoryScanTS, windowStart, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) SaveNoticePollError(ctx context.Context, exchange, kind, message string, blockedUntil time.Time) error {
	sources, err := r.LoadNoticeSources(ctx)
	if err != nil {
		return err
	}
	source := sources[exchange]
	now := float64(time.Now().UnixNano()) / 1e9
	if source.ErrorStartedTS == 0 {
		source.ErrorStartedTS = now
	}
	source.ErrorKind = kind
	source.Error = message
	source.ErrorLastSeenTS = now
	source.ErrorOccurrences++
	source.BlockedUntilTS = float64(blockedUntil.UnixNano()) / 1e9
	return r.SaveNoticePoll(ctx, exchange, nil, source)
}

func (r *Repository) RecoveredMinuteSupplements(context.Context, domain.UnixSecond, domain.UnixSecond) ([]reconcile.MinuteSupplement, error) {
	return []reconcile.MinuteSupplement{}, nil
}

func (r *Repository) RecoveredTenMinuteSupplements(context.Context, domain.UnixSecond, domain.UnixSecond) ([]reconcile.MinuteSupplement, error) {
	return []reconcile.MinuteSupplement{}, nil
}

func cloneQuoteCounts(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneAssetVolumes(source map[movement.AssetKey]float64) map[movement.AssetKey]float64 {
	result := make(map[movement.AssetKey]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type rowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanTickerSamples(rows rowScanner) ([]tickertrend.Sample, error) {
	result := make([]tickertrend.Sample, 0)
	for rows.Next() {
		var payload []byte
		var sample tickertrend.Sample
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &sample); err != nil {
			return nil, err
		}
		result = append(result, sample)
	}
	return result, rows.Err()
}

func realtimeStateMatches(state marketanalysis.RealtimeFlowState, filter marketanalysis.RealtimeFlowHistoryFilter) bool {
	asset := strings.ToUpper(strings.TrimSpace(filter.Asset))
	exchange := strings.ToLower(strings.TrimSpace(filter.Exchange))
	if asset == "" && exchange == "" {
		return true
	}
	for _, driver := range state.Drivers {
		if asset != "" && !strings.EqualFold(driver.Asset, asset) {
			continue
		}
		if exchange == "" {
			return true
		}
		for _, impact := range driver.ExchangeImpacts {
			if strings.EqualFold(impact.Exchange, exchange) {
				return true
			}
		}
	}
	if asset == "" {
		for _, driver := range state.ExchangeDrivers {
			if strings.EqualFold(driver.Exchange, exchange) {
				return true
			}
		}
	}
	return false
}

func reverse[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

// Ensure deterministic query results when callers later combine maps into
// payloads. Kept here to make the storage adapter's ordering intent explicit.
func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
