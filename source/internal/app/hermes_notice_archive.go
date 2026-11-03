package app

import (
	"context"
	"fmt"
	"market-share-go/internal/notice"
	"regexp"
	"sort"
	"strings"
	"time"
)

func noticeQuestionUsesArchive(question string, ids []string, now time.Time) bool {
	w := noticeQuestionTimeWindowFor(question, now)
	return (w.StartMS > 0 && w.StartMS < now.Add(-noticeQuestionHistoryDays*24*time.Hour).UnixMilli()) || len(ids) > 0
}

func domesticNoticeQuestionScopedItems(items []notice.Item, now time.Time, question string, ids []string) []notice.Item {
	w := noticeQuestionTimeWindowFor(question, now)
	if w.StartMS == 0 && len(ids) == 0 {
		return domesticNoticeQuestionItems(items, now)
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	result := []notice.Item{}
	for _, item := range items {
		if !notice.IsDomesticExchange(item.Exchange) {
			continue
		}
		anchor := noticeQuestionItemAnchorMS(item)
		within := anchor >= now.Add(-noticeQuestionHistoryDays*24*time.Hour).UnixMilli()
		if w.StartMS > 0 {
			within = anchor >= w.StartMS && anchor < w.EndExclusiveMS
		}
		if within || (w.StartMS == 0 && selected[noticeQuestionSourceID(item.Exchange, item.NoticeID)]) {
			result = append(result, item)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return noticeQuestionItemAnchorMS(result[i]) > noticeQuestionItemAnchorMS(result[j])
	})
	return result
}

// Range reads never enter the global recent snapshot or the request shortcut.
// A changing corpus or a resource cap returns an error, never a partial count.
func (s *Service) noticeQuestionArchiveSnapshot(ctx context.Context, snapshot notice.Snapshot, question string, ids []string, now time.Time) (notice.Snapshot, error) {
	w := noticeQuestionTimeWindowFor(question, now)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	changed := false
	if w.StartMS > 0 && w.EndExclusiveMS > w.StartMS {
		query := notice.ArchiveQuery{FromTSMS: w.StartMS, ToTSMS: w.EndExclusiveMS, Limit: notice.ArchivePageLimit}
		items := []notice.Item{}
		for pages := 0; ; pages++ {
			if pages == 10 {
				return notice.Snapshot{}, fmt.Errorf("notice question range exceeds 2000 rows; narrow the publication interval")
			}
			page, err := s.noticeArchivePage(ctx, query)
			if err != nil {
				return notice.Snapshot{}, err
			}
			items = append(items, page.Items...)
			if !page.HasMore {
				break
			}
			if page.NextCursor == "" || page.NextCursor == query.Cursor {
				return notice.Snapshot{}, fmt.Errorf("notice archive cursor did not advance")
			}
			query.Cursor = page.NextCursor
		}
		snapshot.Items = items
		changed = true
	} else if len(ids) > 0 {
		if len(ids) > noticeQuestionVerifiedSourceLimit {
			return notice.Snapshot{}, fmt.Errorf("too many notice context sources")
		}
		items := append([]notice.Item(nil), snapshot.Items...)
		seen := map[string]bool{}
		for _, item := range items {
			seen[noticeQuestionSourceID(item.Exchange, item.NoticeID)] = true
		}
		for _, id := range ids {
			exchange, noticeID, ok := strings.Cut(id, ":")
			if !ok || !notice.IsDomesticExchange(exchange) || noticeID == "" || seen[id] {
				continue
			}
			item, found, err := s.repository.LoadNoticeItem(ctx, exchange, noticeID)
			if err != nil {
				return notice.Snapshot{}, err
			}
			if found {
				items = append(items, item)
				seen[id] = true
				changed = true
			}
		}
		snapshot.Items = items
	}
	if !changed {
		return snapshot, nil
	}
	// Collection status for a rolling view cannot attest an arbitrary older range.
	sources := make(map[string]notice.Source, len(snapshot.Sources))
	for exchange, source := range snapshot.Sources {
		source.HistoryStatus = "RANGE_UNVERIFIED"
		sources[exchange] = source
	}
	snapshot.Sources = sources
	snapshot.CompleteHistorySourceCount = 0
	snapshot.MarketEvidence = nil
	snapshot.MarketEvidencePrepared = false
	return snapshot, nil
}

func noticeQuestionExplicitRangeWindow(text string, now time.Time) (noticeQuestionTimeWindow, bool) {
	if !strings.Contains(text, "부터") && !strings.Contains(text, "~") {
		return noticeQuestionTimeWindow{}, false
	}
	type dated struct {
		index int
		value time.Time
	}
	var dates []dated
	for _, pattern := range []*regexp.Regexp{noticeQuestionISODatePattern, noticeQuestionKoreanDatePattern} {
		for _, m := range pattern.FindAllStringSubmatchIndex(text, -1) {
			group := func(i int) string {
				if m[i] < 0 {
					return ""
				}
				return text[m[i]:m[i+1]]
			}
			date, ok := noticeQuestionValidKSTDate(group(2), group(4), group(6), now.In(seoulLocation))
			if !ok {
				return noticeQuestionTimeWindow{Invalid: true}, true
			}
			dates = append(dates, dated{m[0], date})
		}
	}
	if len(dates) == 0 {
		return noticeQuestionTimeWindow{}, false
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].index < dates[j].index })
	start := dates[0].value
	var end time.Time
	if len(dates) == 2 {
		end = dates[1].value.AddDate(0, 0, 1)
	} else if len(dates) == 1 && (containsNoticeQuestionAny(text, "오늘", "현재", "지금") || (strings.Contains(text, "부터") && !strings.Contains(text, "까지"))) {
		end = now.In(seoulLocation).Add(time.Millisecond)
	} else {
		return noticeQuestionTimeWindow{Invalid: true}, true
	}
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		return noticeQuestionTimeWindow{Invalid: true}, true
	}
	open := end.After(now)
	filterEnd := end.UnixMilli()
	if open {
		filterEnd = now.UnixMilli() + 1
	}
	return noticeQuestionTimeWindow{StartMS: start.UnixMilli(), EndExclusiveMS: filterEnd, StartKST: start.Format(time.RFC3339), EndKSTExclusive: end.Format(time.RFC3339), Label: "지정 게시 기간(KST)", Open: open}, true
}
