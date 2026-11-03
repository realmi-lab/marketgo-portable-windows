package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
)

const (
	movementNoticeRelinkLimit = 100
	movementNearbyMS          = int64(time.Hour / time.Millisecond)
)

type movementNoticeCandidate struct {
	evidence  movement.NoticeEvidence
	assets    []string
	schedules []notice.Schedule
}

type movementNoticeCandidateCache struct {
	mu     sync.Mutex
	loaded bool
	key    movementNoticeCandidateKey
	value  []movementNoticeCandidate
}

type movementNoticeCandidateKey struct {
	startMS int64
	endMS   int64
}

func (c *movementNoticeCandidateCache) get(
	ctx context.Context,
	key movementNoticeCandidateKey,
	load func(context.Context, int64, int64) ([]movementNoticeCandidate, error),
) ([]movementNoticeCandidate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded && c.key == key {
		return cloneMovementNoticeCandidates(c.value), nil
	}
	value, err := load(ctx, key.startMS, key.endMS)
	if err != nil {
		return nil, err
	}
	c.value = cloneMovementNoticeCandidates(value)
	c.key = key
	c.loaded = true
	return cloneMovementNoticeCandidates(c.value), nil
}

func (c *movementNoticeCandidateCache) invalidate() {
	c.mu.Lock()
	c.loaded = false
	c.key = movementNoticeCandidateKey{}
	c.value = nil
	c.mu.Unlock()
}

func cloneMovementNoticeCandidates(values []movementNoticeCandidate) []movementNoticeCandidate {
	result := append([]movementNoticeCandidate(nil), values...)
	for index := range result {
		result[index].assets = append([]string(nil), values[index].assets...)
		result[index].schedules = append([]notice.Schedule(nil), values[index].schedules...)
		result[index].evidence.MatchedAssets = append([]string(nil), values[index].evidence.MatchedAssets...)
	}
	return result
}

type movementNoticeRelinkStore interface {
	recentMovementNoticeEvents(context.Context, int) ([]movement.Event, error)
	movementNoticeCandidates(context.Context, int64, int64) ([]movementNoticeCandidate, error)
	replaceMovementNoticeEvidence(context.Context, map[string][]movement.NoticeEvidence) error
}

func (r *Repository) MovementNoticeEvidence(
	ctx context.Context,
	event *movement.Event,
	drivers []movement.Driver,
) ([]movement.NoticeEvidence, error) {
	startMS, endMS := movementEventPeriod(event)
	candidates, err := r.movementNoticeCandidates(ctx, startMS, endMS)
	if err != nil {
		return nil, err
	}
	return buildMovementNoticeEvidence(event, drivers, candidates), nil
}

// RelinkRecentMovementNotices refreshes only notice evidence. Event direction,
// share values, drivers, and stored snapshots remain untouched.
func (r *Repository) RelinkRecentMovementNotices(ctx context.Context) (map[string][]movement.NoticeEvidence, error) {
	r.movementEvidenceMu.Lock()
	defer r.movementEvidenceMu.Unlock()
	return relinkRecentMovementNotices(ctx, r, movementNoticeRelinkLimit)
}

func relinkRecentMovementNotices(
	ctx context.Context,
	store movementNoticeRelinkStore,
	limit int,
) (map[string][]movement.NoticeEvidence, error) {
	events, err := store.recentMovementNoticeEvents(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("load recent official movements for notice relink: %w", err)
	}
	if len(events) == 0 {
		return map[string][]movement.NoticeEvidence{}, nil
	}
	periodStart, periodEnd := movementEventsPeriod(events)
	candidates, err := store.movementNoticeCandidates(ctx, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("load notice candidates for official movement relink: %w", err)
	}
	replacements := make(map[string][]movement.NoticeEvidence, len(events))
	for index := range events {
		event := &events[index]
		replacements[event.EventID] = buildMovementNoticeEvidence(event, event.Drivers, candidates)
	}
	if err := store.replaceMovementNoticeEvidence(ctx, replacements); err != nil {
		return nil, fmt.Errorf("replace official movement notice evidence: %w", err)
	}
	return replacements, nil
}

func (r *Repository) recentMovementNoticeEvents(ctx context.Context, limit int) ([]movement.Event, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+movementEventColumns+`
		FROM official_share_events
		WHERE source=$1 AND state IN ('ACTIVE','ENDED')
		ORDER BY updated_at_ms DESC LIMIT $2`, alignedDomesticMovementSource, limit)
	if err != nil {
		return nil, err
	}
	events := make([]movement.Event, 0, limit)
	indexes := make(map[string]int, limit)
	for rows.Next() {
		event, scanErr := scanMovementEvent(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		indexes[event.EventID] = len(events)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(events) == 0 {
		return events, nil
	}
	eventIDs := make([]string, 0, len(events))
	for index := range events {
		eventIDs = append(eventIDs, events[index].EventID)
	}
	driverRows, err := r.pool.Query(ctx, `SELECT event_id, asset, rank_no
		FROM official_share_event_drivers
		WHERE event_id = ANY($1::text[])
		ORDER BY event_id, rank_no`, eventIDs)
	if err != nil {
		return nil, err
	}
	defer driverRows.Close()
	for driverRows.Next() {
		var eventID string
		var driver movement.Driver
		if err := driverRows.Scan(&eventID, &driver.Asset, &driver.Rank); err != nil {
			return nil, err
		}
		if index, found := indexes[eventID]; found {
			events[index].Drivers = append(events[index].Drivers, driver)
		}
	}
	return events, driverRows.Err()
}

func buildMovementNoticeEvidence(
	event *movement.Event,
	drivers []movement.Driver,
	candidates []movementNoticeCandidate,
) []movement.NoticeEvidence {
	driverAssets := movementDriverAssets(drivers)
	result := make([]movement.NoticeEvidence, 0, len(candidates))
	for _, candidate := range candidates {
		evidence, linked := linkMovementNotice(event, candidate, driverAssets)
		if linked {
			result = append(result, evidence)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return movementEvidencePrecedes(result[i], result[j])
	})
	for index := range result {
		result[index].MatchedAssets = append([]string(nil), result[index].MatchedAssets...)
	}
	return result
}

func (r *Repository) movementNoticeCandidates(ctx context.Context, startMS, endMS int64) ([]movementNoticeCandidate, error) {
	startMS, endMS = normalizedMovementPeriod(startMS, endMS)
	key := movementNoticeCandidateKey{startMS: startMS, endMS: endMS}
	return r.movementCandidates.get(ctx, key, r.loadMovementNoticeCandidates)
}

func (r *Repository) loadMovementNoticeCandidates(ctx context.Context, startMS, endMS int64) ([]movementNoticeCandidate, error) {
	nearStart, nearEnd := startMS-movementNearbyMS, endMS+movementNearbyMS
	rows, err := r.pool.Query(ctx, `WITH relevant_notices AS (
            SELECT exchange_name, notice_id
            FROM exchange_notices
            WHERE exchange_name IN ('upbit','bithumb')
              AND category IN ('LISTING','DELISTING','MARKET_ADDITION','QUOTE_MARKET_CHANGE',
                               'DEPOSIT','WITHDRAWAL','DEPOSIT_WITHDRAWAL','CAUTION')
              AND COALESCE(NULLIF(published_ts_ms, 0), detected_ts_ms) BETWEEN $1 AND $2
            UNION
            SELECT schedules.exchange_name, schedules.notice_id
            FROM exchange_notice_schedules AS schedules
            JOIN exchange_notices AS notices
              ON notices.exchange_name=schedules.exchange_name
             AND notices.notice_id=schedules.notice_id
            WHERE schedules.exchange_name IN ('upbit','bithumb')
              AND (schedules.start_ts_ms > 0 OR schedules.end_ts_ms > 0)
              AND (
                  ((schedules.start_ts_ms=0 OR schedules.start_ts_ms <= $4)
                   AND (schedules.end_ts_ms=0 OR schedules.end_ts_ms >= $3))
                  OR (
                      notices.category IN ('LISTING','DELISTING','MARKET_ADDITION','QUOTE_MARKET_CHANGE',
                                           'DEPOSIT','WITHDRAWAL','DEPOSIT_WITHDRAWAL','CAUTION')
                      AND (schedules.start_ts_ms BETWEEN $1 AND $2
                           OR schedules.end_ts_ms BETWEEN $1 AND $2)
                  )
              )
        )
        SELECT notices.exchange_name, notices.notice_id, notices.title, notices.url,
               notices.category, notices.published_ts_ms, notices.detected_ts_ms,
               notices.assets_json, notices.schedules_json
        FROM relevant_notices
        JOIN exchange_notices AS notices USING (exchange_name, notice_id)
        ORDER BY notices.sort_ts_ms DESC`, nearStart, nearEnd, startMS, endMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]movementNoticeCandidate, 0)
	for rows.Next() {
		var candidate movementNoticeCandidate
		var assetsJSON, schedulesJSON string
		if err := rows.Scan(
			&candidate.evidence.Exchange, &candidate.evidence.NoticeID,
			&candidate.evidence.Title, &candidate.evidence.URL,
			&candidate.evidence.Category, &candidate.evidence.PublishedTSMS,
			&candidate.evidence.DetectedTSMS, &assetsJSON, &schedulesJSON,
		); err != nil {
			return nil, err
		}
		candidate.assets = extractMovementAssets(assetsJSON)
		if err := json.Unmarshal([]byte(schedulesJSON), &candidate.schedules); err != nil {
			return nil, fmt.Errorf("decode movement notice schedules: %w", err)
		}
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func movementEventPeriod(event *movement.Event) (int64, int64) {
	if event == nil {
		return 0, 0
	}
	return normalizedMovementPeriod(event.StartedAtMS, event.UpdatedAtMS)
}

func movementEventsPeriod(events []movement.Event) (int64, int64) {
	startMS, endMS := int64(0), int64(0)
	for index := range events {
		currentStart, currentEnd := movementEventPeriod(&events[index])
		if startMS == 0 || currentStart < startMS {
			startMS = currentStart
		}
		if currentEnd > endMS {
			endMS = currentEnd
		}
	}
	return normalizedMovementPeriod(startMS, endMS)
}

func normalizedMovementPeriod(startMS, endMS int64) (int64, int64) {
	if startMS <= 0 {
		startMS = endMS
	}
	if endMS <= 0 {
		endMS = startMS
	}
	if endMS < startMS {
		startMS, endMS = endMS, startMS
	}
	return startMS, endMS
}

func movementDriverAssets(drivers []movement.Driver) map[string]struct{} {
	result := make(map[string]struct{}, len(drivers))
	for _, driver := range drivers {
		asset := strings.ToUpper(strings.TrimSpace(driver.Asset))
		if asset != "" {
			result[asset] = struct{}{}
		}
	}
	return result
}

func linkMovementNotice(
	event *movement.Event,
	candidate movementNoticeCandidate,
	topAssets map[string]struct{},
) (movement.NoticeEvidence, bool) {
	evidence := candidate.evidence
	if !notice.IsMarketEvidenceExchange(evidence.Exchange) {
		return movement.NoticeEvidence{}, false
	}
	evidence.MatchedAssets = matchingMovementAssets(candidate.assets, topAssets)
	if len(candidate.assets) > 0 && len(evidence.MatchedAssets) == 0 {
		return movement.NoticeEvidence{}, false
	}
	windowStart := event.StartedAtMS - movementNearbyMS
	windowEnd := event.UpdatedAtMS + movementNearbyMS
	if notice.IsReactionLifecycleCategory(evidence.Category) {
		if anchor, relation, found := nearestMovementLifecycleAnchor(candidate, event.StartedAtMS, windowStart, windowEnd); found {
			evidence.AnchorTSMS = anchor
			evidence.Relation = relation
			return evidence, true
		}
	}
	if notice.ScheduleActiveDuring(candidate.schedules, evidence.Category, evidence.Title, event.StartedAtMS, event.UpdatedAtMS) {
		evidence.AnchorTSMS = movementActiveScheduleAnchor(candidate.schedules, event.StartedAtMS)
		evidence.Relation = "SCHEDULE_ACTIVE"
		return evidence, true
	}
	return movement.NoticeEvidence{}, false
}

func nearestMovementLifecycleAnchor(candidate movementNoticeCandidate, target, start, end int64) (int64, string, bool) {
	type anchor struct {
		at       int64
		relation string
	}
	values := make([]anchor, 0, len(candidate.schedules)*2+1)
	published := candidate.evidence.PublishedTSMS
	if published <= 0 {
		published = candidate.evidence.DetectedTSMS
	}
	if published > 0 {
		values = append(values, anchor{published, "PUBLISHED_NEAR"})
	}
	for _, schedule := range candidate.schedules {
		if schedule.StartTSMS > 0 {
			values = append(values, anchor{schedule.StartTSMS, "SCHEDULE_START_NEAR"})
		}
		if schedule.EndTSMS > 0 {
			values = append(values, anchor{schedule.EndTSMS, "SCHEDULE_END_NEAR"})
		}
	}
	var selected anchor
	found := false
	for _, value := range values {
		if !movementTimeWithin(value.at, start, end) {
			continue
		}
		if !found || absMovementTime(value.at-target) < absMovementTime(selected.at-target) {
			selected, found = value, true
		}
	}
	return selected.at, selected.relation, found
}

func movementActiveScheduleAnchor(schedules []notice.Schedule, target int64) int64 {
	selected := target
	distance := int64(^uint64(0) >> 1)
	for _, schedule := range schedules {
		for _, value := range []int64{schedule.StartTSMS, schedule.EndTSMS} {
			if value <= 0 {
				continue
			}
			current := absMovementTime(value - target)
			if current < distance {
				selected, distance = value, current
			}
		}
	}
	return selected
}

func absMovementTime(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func matchingMovementAssets(assets []string, topAssets map[string]struct{}) []string {
	if len(assets) == 0 || len(topAssets) == 0 {
		return nil
	}
	matchCount := 0
	allCanonical := true
	for _, asset := range assets {
		normalized := strings.ToUpper(strings.TrimSpace(asset))
		if _, found := topAssets[normalized]; !found {
			continue
		}
		matchCount++
		allCanonical = allCanonical && normalized == asset
	}
	if matchCount == 0 {
		return nil
	}
	if matchCount == len(assets) && allCanonical {
		return assets
	}
	result := make([]string, 0, matchCount)
	for _, asset := range assets {
		normalized := strings.ToUpper(strings.TrimSpace(asset))
		if _, found := topAssets[normalized]; found {
			result = append(result, normalized)
		}
	}
	return result
}

func firstMovementTimeWithin(values []int64, start, end int64) (int64, bool) {
	for _, value := range values {
		if movementTimeWithin(value, start, end) {
			return value, true
		}
	}
	return 0, false
}

func movementTimeWithin(value, start, end int64) bool {
	return value >= start && value <= end
}

func movementEvidenceScore(evidence movement.NoticeEvidence) int {
	score := 0
	if evidence.Relation == "SCHEDULE_ACTIVE" {
		score += 4
	}
	if evidence.Relation == "SCHEDULE_START_NEAR" || evidence.Relation == "SCHEDULE_END_NEAR" {
		score += 5
	}
	if len(evidence.MatchedAssets) > 0 {
		score += 3
	}
	if evidence.Relation == "PUBLISHED_NEAR" {
		score += 2
	}
	return score
}

func movementEvidencePrecedes(left, right movement.NoticeEvidence) bool {
	leftScore, rightScore := movementEvidenceScore(left), movementEvidenceScore(right)
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	return left.AnchorTSMS > right.AnchorTSMS
}

func extractMovementAssets(raw string) []string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil
	}
	result := make([]string, 0)
	walkMovementAssets(value, &result)
	return result
}

func walkMovementAssets(value any, result *[]string) {
	switch value := value.(type) {
	case string:
		*result = append(*result, value)
	case []any:
		for _, item := range value {
			walkMovementAssets(item, result)
		}
	case map[string]any:
		for key, item := range value {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "asset") || strings.Contains(lower, "symbol") {
				walkMovementAssets(item, result)
				continue
			}
			walkMovementAssetContainers(item, result)
		}
	}
}

func walkMovementAssetContainers(value any, result *[]string) {
	switch value := value.(type) {
	case map[string]any:
		walkMovementAssets(value, result)
	case []any:
		for _, item := range value {
			if _, isObject := item.(map[string]any); isObject {
				walkMovementAssets(item, result)
			}
		}
	}
}

func extractMovementSchedule(raw string) []int64 {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil
	}
	result := make([]int64, 0)
	walkMovementSchedule(value, &result)
	return result
}

func walkMovementSchedule(value any, result *[]int64) {
	switch value := value.(type) {
	case float64:
		appendMovementTimestamp(value, result)
	case string:
		if number, err := strconv.ParseInt(value, 10, 64); err == nil {
			appendMovementTimestamp(float64(number), result)
		} else if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			*result = append(*result, parsed.UnixMilli())
		}
	case []any:
		for _, item := range value {
			walkMovementSchedule(item, result)
		}
	case map[string]any:
		for key, item := range value {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "time") || strings.Contains(lower, "date") ||
				strings.Contains(lower, "start") || strings.Contains(lower, "end") {
				walkMovementSchedule(item, result)
			}
		}
	}
}

func appendMovementTimestamp(value float64, result *[]int64) {
	if value > 1e12 && value < 1e14 {
		*result = append(*result, int64(value))
	} else if value > 1e9 && value < 1e11 {
		*result = append(*result, int64(value*1_000))
	}
}
