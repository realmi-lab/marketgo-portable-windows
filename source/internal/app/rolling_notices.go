package app

import (
	"fmt"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/notice"
)

var rollingNoticePriority = map[string]int{
	"DEPOSIT": 40, "WITHDRAWAL": 40, "DEPOSIT_WITHDRAWAL": 40,
	"DELISTING": 38, "CAUTION": 36, "LISTING": 32, "QUOTE_MARKET_CHANGE": 30,
	"NETWORK_UPGRADE": 24, "EVENT": 20, "AIRDROP": 20, "SNAPSHOT": 18,
}

func attachRollingNoticeEvidence(monitor *marketanalysis.RollingMonitor, snapshot notice.Snapshot, nowMS int64) {
	if monitor == nil {
		return
	}
	driverAssets := rollingDriverAssets(*monitor)
	movementStart := int64(0)
	if monitor.Movement.StartedAtMS != nil {
		movementStart = *monitor.Movement.StartedAtMS
	}
	byExchange := make(map[string][]marketanalysis.RollingNoticeEvidence, len(domain.DomesticExchanges))
	available := make(map[string]int, len(domain.DomesticExchanges))
	for _, prepared := range notice.PreparedMarketEvidence(snapshot) {
		item, brief := prepared.Item, prepared.Brief
		exchange := strings.ToLower(strings.TrimSpace(item.Exchange))
		available[exchange]++
		if brief.Kind == "EVENT" && len(brief.Assets) > 0 {
			item.Assets = append([]string(nil), brief.Assets...)
		} else {
			item.Assets = mergedNoticeAssets(item.Assets, brief.Assets)
		}
		item.Schedules = brief.Schedules
		anchor := rollingNoticeAnchor(item, movementStart, nowMS)
		row := buildRollingNoticeEvidence(item, brief, driverAssets, movementStart, anchor, nowMS)
		if !rollingNoticeEligible(item, brief, row, movementStart, nowMS, prepared.CurrentLifecycle) {
			continue
		}
		byExchange[exchange] = append(byExchange[exchange], row)
	}
	evidence := marketanalysis.RollingEvidence{
		DomesticNotices:               []marketanalysis.RollingNoticeEvidence{},
		DomesticNoticeCounts:          make(map[string]int, len(domain.DomesticExchanges)),
		DomesticNoticeAvailableCounts: make(map[string]int, len(domain.DomesticExchanges)),
		DomesticNoticeSources:         make(map[string]marketanalysis.RollingNoticeSource, len(domain.DomesticExchanges)),
	}
	for _, exchange := range domain.DomesticExchanges {
		name := string(exchange)
		rows := byExchange[name]
		sort.SliceStable(rows, func(i, j int) bool { return rollingNoticeRecentLess(rows[i], rows[j]) })
		if len(rows) > 30 {
			rows = rows[:30]
		}
		evidence.DomesticNotices = append(evidence.DomesticNotices, rows...)
		evidence.DomesticNoticeCounts[name] = len(rows)
		evidence.DomesticNoticeAvailableCounts[name] = available[name]
		evidence.DomesticNoticeSources[name] = rollingNoticeSource(snapshot.Sources[name], len(rows), available[name])
	}
	if movementStart > 0 {
		windowStart := movementStart - 3_600_000
		evidence.NoticeWindowStartMS = &windowStart
	}
	monitor.Evidence = evidence
	monitor.Briefing = buildRollingBriefing(*monitor, evidence.DomesticNotices)
}

func buildRollingNoticeEvidence(item notice.Item, brief notice.Brief, driverAssets map[string]bool, movementStart, anchor, nowMS int64) marketanalysis.RollingNoticeEvidence {
	assets := make([]string, 0, len(item.Assets))
	assetRelated := false
	for _, asset := range item.Assets {
		asset = strings.ToUpper(strings.TrimSpace(asset))
		if asset == "" {
			continue
		}
		assets = appendUniqueString(assets, asset)
		assetRelated = assetRelated || driverAssets[asset]
	}
	sort.Strings(assets)
	detailAvailable := brief.DetailAvailable
	contentAssetRelated := detailAvailable && assetRelated
	activeNow, scheduleRelated, currentWindowSchedule := rollingScheduleFlags(item, movementStart, nowMS)
	timeRelated := movementStart > 0 && anchor > 0 && anchor >= movementStart-3_600_000 && anchor <= movementStart+3_600_000
	contentScheduleRelated := detailAvailable && currentWindowSchedule
	related := assetRelated || timeRelated || scheduleRelated || contentScheduleRelated
	contentBasis, relationReason := rollingNoticeRelation(contentAssetRelated, assetRelated, contentScheduleRelated, scheduleRelated, timeRelated, detailAvailable)
	category := strings.ToUpper(strings.TrimSpace(item.Category))
	priority := rollingNoticePriority[category]
	if priority == 0 {
		priority = 8
	}
	if detailAvailable {
		priority += 30
	}
	if contentAssetRelated {
		priority += 45
	} else if assetRelated {
		priority += 20
	}
	if scheduleRelated {
		priority += 30
	}
	if contentScheduleRelated {
		priority += 35
		if (brief.Kind == "EVENT" || brief.Kind == "AIRDROP") && activeNow {
			priority += 60
		}
	}
	if activeNow {
		priority += 15
	}
	if timeRelated {
		priority += 20
	}
	priorityLabel := "일반"
	if priority >= 80 {
		priorityLabel = "높음"
	} else if priority >= 45 {
		priorityLabel = "중간"
	}
	relation := "TIME_ORDER_UNAVAILABLE"
	if movementStart > 0 && anchor > 0 {
		if anchor <= movementStart {
			relation = "BEFORE_MOVEMENT"
		} else {
			relation = "AFTER_MOVEMENT"
		}
	}
	return marketanalysis.RollingNoticeEvidence{
		Exchange: item.Exchange, NoticeID: item.NoticeID, Title: item.Title, URL: item.URL,
		Category: item.Category, PublishedTSMS: item.PublishedTSMS, UpdatedTSMS: item.UpdatedTSMS,
		DetectedTSMS: item.DetectedTSMS,
		AnchorTSMS:   optionalRollingAnchor(anchor), Assets: assets, Relation: relation, Related: related,
		RelatedLabel: boolLabel(related, "관련 있음", "관련 없음"), RelationReason: relationReason,
		TimingLabel: rollingNoticeTiming(movementStart, anchor), ContentBasis: contentBasis,
		ContentPriorityScore: priority, PriorityLabel: priorityLabel, ContentRelated: contentAssetRelated,
		TimeRelated: timeRelated, ContentScheduleRelated: contentScheduleRelated,
		ScheduleActiveNow: activeNow, ScheduleRelated: scheduleRelated, MarketContext: []map[string]any{},
		ImageConsistency: notice.ImageConsistencyIfPresent(item),
		Brief:            brief,
	}
}

func rollingDriverAssets(monitor marketanalysis.RollingMonitor) map[string]bool {
	result := make(map[string]bool)
	bridges := monitor.Bridges
	if len(bridges) == 0 {
		bridges = []marketanalysis.RollingBridge{monitor.Bridge}
	}
	for _, bridge := range bridges {
		for _, driver := range bridge.AssetDrivers {
			if base := strings.ToUpper(strings.TrimSpace(driver.Base)); base != "" {
				result[base] = true
			}
		}
		for _, driver := range bridge.ObservedAssetDrivers {
			if base := strings.ToUpper(strings.TrimSpace(driver.Base)); base != "" {
				result[base] = true
			}
		}
	}
	return result
}

func rollingNoticeAnchor(item notice.Item, movementStart, nowMS int64) int64 {
	anchors := make([]int64, 0, len(item.Schedules)*2+1)
	if item.PublishedTSMS > 0 {
		anchors = append(anchors, item.PublishedTSMS)
	} else if item.DetectedTSMS > 0 {
		anchors = append(anchors, item.DetectedTSMS)
	}
	for _, schedule := range item.Schedules {
		if schedule.StartTSMS > 0 {
			anchors = append(anchors, schedule.StartTSMS)
		}
		if schedule.EndTSMS > 0 {
			anchors = append(anchors, schedule.EndTSMS)
		}
	}
	if len(anchors) == 0 {
		return item.PublishedTSMS
	}
	if movementStart > 0 {
		closest := anchors[0]
		for _, anchor := range anchors[1:] {
			if absInt64(anchor-movementStart) < absInt64(closest-movementStart) {
				closest = anchor
			}
		}
		return closest
	}
	past, earliest := int64(0), anchors[0]
	for _, anchor := range anchors {
		if anchor <= nowMS && anchor > past {
			past = anchor
		}
		if anchor < earliest {
			earliest = anchor
		}
	}
	if past > 0 {
		return past
	}
	return earliest
}

func rollingScheduleFlags(item notice.Item, movementStart, nowMS int64) (bool, bool, bool) {
	activeNow := notice.ScheduleActiveDuring(item.Schedules, item.Category, item.Title, nowMS, nowMS)
	movementOverlap, currentOverlap := false, false
	movementWindowStart, currentWindowStart := int64(0), nowMS-86_400_000
	if movementStart > 0 {
		movementWindowStart = movementStart - 3_600_000
		movementOverlap = notice.ScheduleActiveDuring(item.Schedules, item.Category, item.Title, movementWindowStart, nowMS)
	}
	currentOverlap = notice.ScheduleActiveDuring(item.Schedules, item.Category, item.Title, currentWindowStart, nowMS)
	return activeNow, movementOverlap, currentOverlap
}

func rollingNoticeEligible(
	item notice.Item,
	brief notice.Brief,
	row marketanalysis.RollingNoticeEvidence,
	movementStart, nowMS int64,
	currentLifecycle bool,
) bool {
	if row.ScheduleActiveNow && currentLifecycle {
		return true
	}
	if !notice.IsReactionLifecycleKind(brief.Kind) && !notice.IsReactionLifecycleCategory(item.Category) {
		return false
	}
	windowStart, windowEnd := nowMS-86_400_000, nowMS
	if movementStart > 0 {
		windowStart, windowEnd = movementStart-3_600_000, movementStart+3_600_000
	}
	for _, anchor := range rollingReactionAnchors(item) {
		if anchor >= windowStart && anchor <= windowEnd {
			return true
		}
	}
	return false
}

func rollingReactionAnchors(item notice.Item) []int64 {
	result := make([]int64, 0, len(item.Schedules)*2+1)
	if item.PublishedTSMS > 0 {
		result = append(result, item.PublishedTSMS)
	} else if item.DetectedTSMS > 0 {
		result = append(result, item.DetectedTSMS)
	}
	for _, schedule := range item.Schedules {
		if schedule.StartTSMS > 0 {
			result = append(result, schedule.StartTSMS)
		}
		if schedule.EndTSMS > 0 {
			result = append(result, schedule.EndTSMS)
		}
	}
	return result
}

func rollingNoticeRelation(contentAsset, asset, contentSchedule, schedule, timeRelated, detail bool) (string, string) {
	switch {
	case contentAsset && contentSchedule:
		return "본문 자산·기간", "공지 본문 자산·기간과 현재 24시간 관측 기간이 겹침"
	case contentSchedule:
		return "본문 기간", "공지 본문에 설명된 일정이 현재 24시간 관측 기간과 겹침"
	case contentAsset && schedule:
		return "본문 자산·일정", "공지 본문 자산·일정이 변화 구간과 겹침"
	case contentAsset && timeRelated:
		return "본문 자산·시간대", "공지 본문 자산이 변화 시작 전후 구간과 겹침"
	case contentAsset:
		return "본문 자산", "공지 본문 자산과 변화 기여 코인이 겹침"
	case asset:
		return "목록 자산", "공지 자산과 변화 기여 코인이 겹침(상세 본문 없음)"
	case schedule || timeRelated:
		return "공지 일정·시간대", "공지 일정·시각이 변화 시작 전후"
	case detail:
		return "본문 확보", "공지 본문은 확보했지만 기여 코인·변화 시간대 불일치"
	default:
		return "연결 근거 없음", "공지 본문·기여 코인·변화 시간대 불일치"
	}
}

func rollingNoticeTiming(movementStart, anchor int64) string {
	if movementStart == 0 {
		return "변화 시작 시각 정보 없음"
	}
	if anchor == 0 {
		return "공지 시각 정보 없음"
	}
	distanceSec := (movementStart - anchor) / 1000
	if distanceSec > -60 && distanceSec < 60 {
		return "변화 시작과 같은 시간대"
	}
	minutes := max(int64(1), absInt64(distanceSec)/60)
	if distanceSec > 0 {
		return fmt.Sprintf("변화 시작 기준 %d분 전", minutes)
	}
	return fmt.Sprintf("변화 시작 기준 %d분 후", minutes)
}

func rollingNoticeRecentLess(left, right marketanalysis.RollingNoticeEvidence) bool {
	leftRecent := max(left.PublishedTSMS, left.UpdatedTSMS, left.DetectedTSMS)
	rightRecent := max(right.PublishedTSMS, right.UpdatedTSMS, right.DetectedTSMS)
	if leftRecent != rightRecent {
		return leftRecent > rightRecent
	}
	if left.PublishedTSMS != right.PublishedTSMS {
		return left.PublishedTSMS > right.PublishedTSMS
	}
	if left.UpdatedTSMS != right.UpdatedTSMS {
		return left.UpdatedTSMS > right.UpdatedTSMS
	}
	return left.NoticeID < right.NoticeID
}

func rollingNoticeSource(source notice.Source, selected, available int) marketanalysis.RollingNoticeSource {
	status := "LIMITED"
	pending := source.Error == "not_polled" || source.ErrorKind == "NOT_POLLED"
	if pending {
		status = "PENDING"
	} else if source.Error != "" {
		status = "ERROR"
	} else if selected >= 30 {
		status = "OK"
	}
	history := source.HistoryStatus
	if history == "" {
		history = "PENDING"
	}
	lastSuccess := source.LastHistoryScanTS
	var errorValue *string
	if source.Error != "" && !pending {
		value := source.Error
		errorValue = &value
	}
	return marketanalysis.RollingNoticeSource{Status: status, FetchedCount: available, HistoryStatus: history, LastSuccessTS: lastSuccess, Error: errorValue}
}

func mergedNoticeAssets(left, right []string) []string {
	result := make([]string, 0, len(left)+len(right))
	for _, values := range [][]string{left, right} {
		for _, value := range values {
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "" {
				result = appendUniqueString(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func optionalRollingAnchor(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func boolLabel(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
