package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/movement"
)

const alignedDomesticMovementSource = "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H"

const movementEventColumns = `
    event_id, source, direction, state, started_at_ms, confirmed_at_ms,
    updated_at_ms, ended_at_ms, baseline_share_pct, current_share_pct,
    extreme_share_pct, change_pp, extreme_change_pp, threshold_pp,
    quality_status`

func (r *Repository) SaveMovementSample(ctx context.Context, snapshot *movement.Snapshot) error {
	return saveMovementSample(ctx, r.pool, snapshot)
}

func saveMovementSample(ctx context.Context, executor movementSampleExecutor, snapshot *movement.Snapshot) error {
	exchanges, err := json.Marshal(snapshot.Exchanges)
	if err != nil {
		return fmt.Errorf("encode official share sample exchanges: %w", err)
	}
	_, err = executor.Exec(ctx, `
        INSERT INTO official_share_samples (
            as_of_ms, status, source, collection_span_ms,
            total_volume_krw, bithumb_volume_krw, bithumb_share_pct, exchanges_json
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT(as_of_ms) DO UPDATE SET
            status=EXCLUDED.status, source=EXCLUDED.source,
            collection_span_ms=EXCLUDED.collection_span_ms,
            total_volume_krw=EXCLUDED.total_volume_krw,
            bithumb_volume_krw=EXCLUDED.bithumb_volume_krw,
            bithumb_share_pct=EXCLUDED.bithumb_share_pct,
            exchanges_json=EXCLUDED.exchanges_json`,
		snapshot.AsOfMS, snapshot.Status, snapshot.Source, snapshot.CollectionSpanMS,
		snapshot.TotalVolumeKRW, snapshot.BithumbVolumeKRW, snapshot.BithumbSharePct, exchanges,
	)
	if err != nil {
		return fmt.Errorf("save official share sample: %w", err)
	}
	return nil
}

func (r *Repository) SaveMovementEvent(
	ctx context.Context,
	event *movement.Event,
	baseline *movement.Snapshot,
	extreme *movement.Snapshot,
) error {
	if event.Source == "" && baseline != nil {
		event.Source = baseline.Source
	}
	if err := movement.ValidateEventDirection(event); err != nil {
		return fmt.Errorf("save official movement event: %w", err)
	}
	r.movementEvidenceMu.Lock()
	defer r.movementEvidenceMu.Unlock()
	latestEvidence, err := r.MovementNoticeEvidence(ctx, event, event.Drivers)
	if err != nil {
		return fmt.Errorf("refresh official movement notice evidence: %w", err)
	}
	event.Notices = latestEvidence
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin official movement event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertMovementEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := replaceMovementDetails(ctx, tx, event, baseline, extreme); err != nil {
		return err
	}
	if err := captureMovementFlowEvidence(ctx, tx, event, extreme); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit official movement event: %w", err)
	}
	return nil
}

func upsertMovementEvent(ctx context.Context, tx pgx.Tx, event *movement.Event) error {
	_, err := tx.Exec(ctx, `INSERT INTO official_share_events (`+movementEventColumns+`)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
        ON CONFLICT(event_id) DO UPDATE SET
            source=EXCLUDED.source, state=EXCLUDED.state, updated_at_ms=EXCLUDED.updated_at_ms,
            ended_at_ms=EXCLUDED.ended_at_ms, current_share_pct=EXCLUDED.current_share_pct,
            extreme_share_pct=EXCLUDED.extreme_share_pct, change_pp=EXCLUDED.change_pp,
            extreme_change_pp=EXCLUDED.extreme_change_pp, quality_status=EXCLUDED.quality_status`,
		event.EventID, event.Source, event.Direction, event.State, event.StartedAtMS,
		event.ConfirmedAtMS, event.UpdatedAtMS, event.EndedAtMS,
		event.BaselineSharePct, event.CurrentSharePct, event.ExtremeSharePct,
		event.ChangePP, event.ExtremeChangePP, event.ThresholdPP, event.QualityStatus,
	)
	if err != nil {
		return fmt.Errorf("upsert official movement event: %w", err)
	}
	return nil
}

func replaceMovementDetails(
	ctx context.Context,
	tx pgx.Tx,
	event *movement.Event,
	baseline *movement.Snapshot,
	extreme *movement.Snapshot,
) error {
	for _, table := range []string{
		"official_share_event_drivers",
		"official_share_event_exchange_drivers",
		"official_share_event_assets",
		"official_share_event_evidence",
	} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE event_id=$1", event.EventID); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	for _, row := range event.Drivers {
		_, err := tx.Exec(ctx, `INSERT INTO official_share_event_drivers VALUES
            ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, event.EventID, row.Asset, row.Rank,
			row.ContributionPP, row.BithumbVolumeChangeKRW, row.CompetitorVolumeChangeKRW,
			row.PreviousBithumbVolumeKRW, row.CurrentBithumbVolumeKRW,
			row.PreviousCompetitorVolumeKRW, row.CurrentCompetitorVolumeKRW)
		if err != nil {
			return fmt.Errorf("insert official movement driver: %w", err)
		}
	}
	for _, row := range event.ExchangeDrivers {
		_, err := tx.Exec(ctx, `INSERT INTO official_share_event_exchange_drivers VALUES
            ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, event.EventID, row.Exchange, row.Rank,
			row.ContributionPP, row.VolumeChangeKRW, row.PreviousVolumeKRW,
			row.CurrentVolumeKRW, row.LeadingAsset, row.LeadingAssetPP)
		if err != nil {
			return fmt.Errorf("insert official movement exchange driver: %w", err)
		}
	}
	for kind, snapshot := range map[string]*movement.Snapshot{"BASELINE": baseline, "CURRENT": extreme} {
		for key, volume := range snapshot.Assets {
			_, err := tx.Exec(ctx, `INSERT INTO official_share_event_assets VALUES
                ($1,$2,$3,$4,$5,$6)`, event.EventID, kind, key.Exchange, key.Asset, volume, snapshot.AsOfMS)
			if err != nil {
				return fmt.Errorf("insert official movement asset: %w", err)
			}
		}
	}
	return insertMovementEvidence(ctx, tx, event.EventID, event.Notices)
}

func insertMovementEvidence(
	ctx context.Context,
	tx pgx.Tx,
	eventID string,
	rows []movement.NoticeEvidence,
) error {
	for _, row := range rows {
		assets, err := json.Marshal(row.MatchedAssets)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO official_share_event_evidence VALUES
			($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, eventID, row.Exchange,
			row.NoticeID, row.Title, row.URL, row.Category, row.PublishedTSMS,
			row.DetectedTSMS, row.AnchorTSMS, row.Relation, string(assets))
		if err != nil {
			return fmt.Errorf("insert official movement evidence: %w", err)
		}
	}
	return nil
}

const movementFlowEvidenceMaxLagMS int64 = 15_000

func captureMovementFlowEvidence(
	ctx context.Context,
	tx pgx.Tx,
	event *movement.Event,
	extreme *movement.Snapshot,
) error {
	if event.ConfirmedAtMS != nil {
		if err := upsertMovementFlowEvidence(ctx, tx, event.EventID, "CONFIRMED", *event.ConfirmedAtMS, true); err != nil {
			return err
		}
	}
	if extreme != nil && extreme.AsOfMS > 0 {
		if err := upsertMovementFlowEvidence(ctx, tx, event.EventID, "EXTREME", extreme.AsOfMS, false); err != nil {
			return err
		}
	}
	if event.EndedAtMS != nil {
		if err := upsertMovementFlowEvidence(ctx, tx, event.EventID, "ENDED", *event.EndedAtMS, false); err != nil {
			return err
		}
	}
	return nil
}

func upsertMovementFlowEvidence(
	ctx context.Context,
	tx pgx.Tx,
	eventID string,
	kind string,
	officialAsOfMS int64,
	preserveExisting bool,
) error {
	var flowAsOfMS int64
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT as_of_ms, payload_json
        FROM realtime_flow_observations
        WHERE as_of_ms <= $1 AND as_of_ms >= $1-$2
        ORDER BY as_of_ms DESC LIMIT 1`,
		officialAsOfMS, movementFlowEvidenceMaxLagMS,
	).Scan(&flowAsOfMS, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read official movement WebSocket evidence: %w", err)
	}
	conflict := `DO UPDATE SET
        official_as_of_ms=EXCLUDED.official_as_of_ms,
        flow_as_of_ms=EXCLUDED.flow_as_of_ms,
        lag_ms=EXCLUDED.lag_ms,
        payload_json=EXCLUDED.payload_json`
	if preserveExisting {
		conflict = "DO NOTHING"
	}
	_, err = tx.Exec(ctx, `INSERT INTO official_share_event_flow_evidence (
        event_id, snapshot_kind, official_as_of_ms, flow_as_of_ms, lag_ms, payload_json
    ) VALUES ($1,$2,$3,$4,$5,$6)
    ON CONFLICT(event_id, snapshot_kind) `+conflict,
		eventID, kind, officialAsOfMS, flowAsOfMS, officialAsOfMS-flowAsOfMS, payload,
	)
	if err != nil {
		return fmt.Errorf("save official movement WebSocket evidence: %w", err)
	}
	return nil
}

func (r *Repository) replaceMovementNoticeEvidence(
	ctx context.Context,
	replacements map[string][]movement.NoticeEvidence,
) error {
	if len(replacements) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin official movement notice relink: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	eventIDs := make([]string, 0, len(replacements))
	for eventID := range replacements {
		eventIDs = append(eventIDs, eventID)
	}
	sort.Strings(eventIDs)
	for _, eventID := range eventIDs {
		if _, err := tx.Exec(ctx, "DELETE FROM official_share_event_evidence WHERE event_id=$1", eventID); err != nil {
			return fmt.Errorf("clear official movement evidence: %w", err)
		}
		if err := insertMovementEvidence(ctx, tx, eventID, replacements[eventID]); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit official movement notice relink: %w", err)
	}
	return nil
}

func (r *Repository) RecentMovementSamples(ctx context.Context, sinceMS int64, limit int) ([]movement.SamplePoint, error) {
	rows, err := r.pool.Query(ctx, `SELECT as_of_ms, bithumb_share_pct, status
        FROM official_share_samples WHERE as_of_ms >= $1
        ORDER BY as_of_ms DESC LIMIT $2`, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movement.SamplePoint, 0, limit)
	for rows.Next() {
		var row movement.SamplePoint
		if err := rows.Scan(&row.AsOfMS, &row.BithumbSharePct, &row.Status); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, rows.Err()
}

func (r *Repository) RecentMovementSamplesBySource(
	ctx context.Context,
	source string,
	sinceMS int64,
	limit int,
) ([]movement.SamplePoint, error) {
	rows, err := r.pool.Query(ctx, `SELECT as_of_ms, bithumb_share_pct, status
        FROM official_share_samples WHERE source=$1 AND as_of_ms >= $2
        ORDER BY as_of_ms DESC LIMIT $3`, source, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movement.SamplePoint, 0, limit)
	for rows.Next() {
		var row movement.SamplePoint
		if err := rows.Scan(&row.AsOfMS, &row.BithumbSharePct, &row.Status); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, rows.Err()
}

func (r *Repository) RecentMovementEvents(ctx context.Context, limit int) ([]movement.Event, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+movementEventColumns+`
        FROM official_share_events ORDER BY updated_at_ms DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movement.Event, 0, limit)
	for rows.Next() {
		event, err := scanMovementEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := r.loadMovementDetailsBatch(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) RecentMovementEventsBySource(ctx context.Context, source string, limit int) ([]movement.Event, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+movementEventColumns+`
        FROM official_share_events WHERE source=$1 ORDER BY updated_at_ms DESC LIMIT $2`, source, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movement.Event, 0, limit)
	for rows.Next() {
		event, err := scanMovementEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := r.loadMovementDetailsBatch(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) MovementEvent(ctx context.Context, eventID string) (*movement.Event, error) {
	event, err := scanMovementEvent(r.pool.QueryRow(ctx, `SELECT `+movementEventColumns+`
        FROM official_share_events WHERE event_id=$1`, eventID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, movement.ErrEventNotFound
		}
		return nil, err
	}
	events := []movement.Event{event}
	if err := r.loadMovementDetailsBatch(ctx, events); err != nil {
		return nil, err
	}
	return &events[0], nil
}

type movementScanner interface{ Scan(...any) error }

func scanMovementEvent(row movementScanner) (movement.Event, error) {
	var event movement.Event
	err := row.Scan(&event.EventID, &event.Source, &event.Direction, &event.State, &event.StartedAtMS,
		&event.ConfirmedAtMS, &event.UpdatedAtMS, &event.EndedAtMS,
		&event.BaselineSharePct, &event.CurrentSharePct, &event.ExtremeSharePct,
		&event.ChangePP, &event.ExtremeChangePP, &event.ThresholdPP, &event.QualityStatus)
	return event, err
}

func (r *Repository) loadMovementDetailsBatch(ctx context.Context, events []movement.Event) error {
	if len(events) == 0 {
		return nil
	}
	indices := make(map[string]int, len(events))
	eventIDs := make([]string, len(events))
	for index := range events {
		indices[events[index].EventID] = index
		eventIDs[index] = events[index].EventID
		events[index].Notices = []movement.NoticeEvidence{}
		events[index].FlowEvidence = []movement.FlowEvidence{}
	}

	rows, err := r.pool.Query(ctx, `SELECT event_id, rank_no, asset, contribution_pp,
        bithumb_volume_change_krw, competitor_volume_change_krw,
        previous_bithumb_volume_krw, current_bithumb_volume_krw,
        previous_competitor_volume_krw, current_competitor_volume_krw
		FROM official_share_event_drivers WHERE event_id=ANY($1)
		ORDER BY event_id, rank_no`, eventIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var eventID string
		var row movement.Driver
		if err := rows.Scan(&eventID, &row.Rank, &row.Asset, &row.ContributionPP, &row.BithumbVolumeChangeKRW, &row.CompetitorVolumeChangeKRW, &row.PreviousBithumbVolumeKRW, &row.CurrentBithumbVolumeKRW, &row.PreviousCompetitorVolumeKRW, &row.CurrentCompetitorVolumeKRW); err != nil {
			rows.Close()
			return err
		}
		if index, ok := indices[eventID]; ok {
			events[index].Drivers = append(events[index].Drivers, row)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	exchangeRows, err := r.pool.Query(ctx, `SELECT event_id, rank_no, exchange_name, contribution_pp,
        volume_change_krw, previous_volume_krw, current_volume_krw,
        leading_asset, leading_asset_contribution_pp
		FROM official_share_event_exchange_drivers WHERE event_id=ANY($1)
		ORDER BY event_id, rank_no`, eventIDs)
	if err != nil {
		return err
	}
	for exchangeRows.Next() {
		var eventID string
		var row movement.ExchangeDriver
		if err := exchangeRows.Scan(&eventID, &row.Rank, &row.Exchange, &row.ContributionPP, &row.VolumeChangeKRW, &row.PreviousVolumeKRW, &row.CurrentVolumeKRW, &row.LeadingAsset, &row.LeadingAssetPP); err != nil {
			exchangeRows.Close()
			return err
		}
		if index, ok := indices[eventID]; ok {
			events[index].ExchangeDrivers = append(events[index].ExchangeDrivers, row)
		}
	}
	if err := exchangeRows.Err(); err != nil {
		exchangeRows.Close()
		return err
	}
	exchangeRows.Close()

	flowRows, err := r.pool.Query(ctx, `SELECT event_id, snapshot_kind,
        official_as_of_ms, flow_as_of_ms, lag_ms, payload_json
        FROM official_share_event_flow_evidence
        WHERE event_id=ANY($1)
        ORDER BY event_id, CASE snapshot_kind
            WHEN 'CONFIRMED' THEN 1 WHEN 'EXTREME' THEN 2 ELSE 3 END`, eventIDs)
	if err != nil {
		return err
	}
	for flowRows.Next() {
		var eventID string
		var evidence movement.FlowEvidence
		var payload []byte
		if err := flowRows.Scan(
			&eventID, &evidence.SnapshotKind, &evidence.OfficialAsOfMS,
			&evidence.FlowAsOfMS, &evidence.LagMS, &payload,
		); err != nil {
			flowRows.Close()
			return err
		}
		evidence.Payload = append([]byte(nil), payload...)
		if index, ok := indices[eventID]; ok {
			events[index].FlowEvidence = append(events[index].FlowEvidence, evidence)
		}
	}
	if err := flowRows.Err(); err != nil {
		flowRows.Close()
		return err
	}
	flowRows.Close()

	evidenceRows, err := r.pool.Query(ctx, `SELECT event_id, exchange_name, notice_id, title, url,
        category, published_ts_ms, detected_ts_ms, anchor_ts_ms, relation,
        matched_assets_json
        FROM official_share_event_evidence
		WHERE event_id=ANY($1) ORDER BY event_id, anchor_ts_ms DESC`, eventIDs)
	if err != nil {
		return err
	}
	defer evidenceRows.Close()
	for evidenceRows.Next() {
		var eventID string
		var evidence movement.NoticeEvidence
		var matchedAssets []byte
		if err := evidenceRows.Scan(
			&eventID, &evidence.Exchange, &evidence.NoticeID, &evidence.Title, &evidence.URL,
			&evidence.Category, &evidence.PublishedTSMS, &evidence.DetectedTSMS,
			&evidence.AnchorTSMS, &evidence.Relation, &matchedAssets,
		); err != nil {
			return err
		}
		if err := json.Unmarshal(matchedAssets, &evidence.MatchedAssets); err != nil {
			return fmt.Errorf("decode official movement evidence assets: %w", err)
		}
		if index, ok := indices[eventID]; ok {
			events[index].Notices = append(events[index].Notices, evidence)
		}
	}
	return evidenceRows.Err()
}

func (r *Repository) MovementEventSnapshot(ctx context.Context, eventID, kind string) (*movement.Snapshot, error) {
	rows, err := r.pool.Query(ctx, `SELECT exchange_name, asset, volume_krw, as_of_ms
        FROM official_share_event_assets WHERE event_id=$1 AND snapshot_kind=$2`, eventID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshot := &movement.Snapshot{Status: "LIVE", Source: "STORED_OFFICIAL_EVENT_SNAPSHOT", Exchanges: map[string]movement.ExchangeSnapshot{}, Assets: map[movement.AssetKey]float64{}}
	for rows.Next() {
		var exchange, asset string
		var volume float64
		var asOf int64
		if err := rows.Scan(&exchange, &asset, &volume, &asOf); err != nil {
			return nil, err
		}
		snapshot.Assets[movement.AssetKey{Exchange: exchange, Asset: asset}] = volume
		exchangeSnapshot := snapshot.Exchanges[exchange]
		exchangeSnapshot.Exchange = exchange
		exchangeSnapshot.Status = "LIVE"
		exchangeSnapshot.Source = snapshot.Source
		exchangeSnapshot.VolumeKRW += volume
		exchangeSnapshot.MarketCount++
		if exchangeSnapshot.Assets == nil {
			exchangeSnapshot.Assets = map[string]float64{}
		}
		exchangeSnapshot.Assets[asset] = volume
		snapshot.Exchanges[exchange] = exchangeSnapshot
		snapshot.TotalVolumeKRW += volume
		if exchange == "bithumb" {
			snapshot.BithumbVolumeKRW += volume
		}
		if asOf > snapshot.AsOfMS {
			snapshot.AsOfMS = asOf
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(snapshot.Assets) == 0 {
		return nil, pgx.ErrNoRows
	}
	snapshot.BithumbSharePct = snapshot.BithumbVolumeKRW / snapshot.TotalVolumeKRW * 100
	return snapshot, nil
}
