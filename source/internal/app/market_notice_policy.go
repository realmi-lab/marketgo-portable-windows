package app

import (
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
)

func marketReactionNoticeItems(snapshot notice.Snapshot, asOfMS, windowSec int64) []notice.Item {
	windowStart := asOfMS - max(int64(1), windowSec)*1_000
	preparedItems := notice.PreparedMarketEvidence(snapshot)
	result := make([]notice.Item, 0, len(preparedItems))
	for _, prepared := range preparedItems {
		item, brief := prepared.Item, prepared.Brief
		if prepared.CurrentLifecycle && notice.ScheduleActiveDuring(item.Schedules, item.Category, item.Title, asOfMS, asOfMS) {
			result = append(result, item)
			continue
		}
		if !notice.IsReactionLifecycleKind(brief.Kind) && !notice.IsReactionLifecycleCategory(item.Category) {
			continue
		}
		for _, anchor := range rollingReactionAnchors(item) {
			if anchor >= windowStart && anchor <= asOfMS {
				result = append(result, item)
				break
			}
		}
	}
	return result
}

func filterMarketReactionSnapshot(snapshot notice.Snapshot, asOfMS, windowSec int64) notice.Snapshot {
	snapshot.Items = marketReactionNoticeItems(snapshot, asOfMS, windowSec)
	snapshot.ContextualItems = nil
	return snapshot
}

func marketNoticeAssetsForMicrostructure(snapshot notice.Snapshot, asOfMS int64) []string {
	filtered := marketReactionNoticeItems(snapshot, asOfMS, marketanalysis.RollingWindowSec)
	result := make([]string, 0)
	for _, item := range filtered {
		result = append(result, item.Assets...)
	}
	return result
}

func marketMovementNotices(values []movement.NoticeEvidence) []movement.NoticeEvidence {
	result := make([]movement.NoticeEvidence, 0, len(values))
	for _, value := range values {
		if !notice.IsMarketEvidenceExchange(value.Exchange) {
			continue
		}
		switch value.Relation {
		case "SCHEDULE_ACTIVE", "SCHEDULE_START_NEAR", "SCHEDULE_END_NEAR":
			result = append(result, value)
		case "PUBLISHED_NEAR":
			if notice.IsReactionLifecycleCategory(value.Category) {
				result = append(result, value)
			}
		}
	}
	return result
}

func filterMovementEventNotices(event movement.Event) movement.Event {
	event.Notices = marketMovementNotices(event.Notices)
	return event
}

func filterMovementStateNotices(state movement.State) movement.State {
	if state.Movement != nil {
		filtered := filterMovementEventNotices(*state.Movement)
		state.Movement = &filtered
	}
	state.RecentEvents = append([]movement.Event(nil), state.RecentEvents...)
	for index := range state.RecentEvents {
		state.RecentEvents[index] = filterMovementEventNotices(state.RecentEvents[index])
	}
	return state
}
