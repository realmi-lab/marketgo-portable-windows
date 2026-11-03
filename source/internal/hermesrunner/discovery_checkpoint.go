package hermesrunner

import (
	"context"
	"fmt"
	"time"

	"market-share-go/internal/notice"
)

func (p *pipeline) persistDiscoveryPage(ctx context.Context, exchange string, page int, items []notice.Item, existing map[string]notice.Item, windowStart int64) error {
	now := time.Now().UTC()
	raw := make([]notice.Item, 0, len(items))
	for _, item := range items {
		if p.withinIngestWindow(item, now) && shouldPersistDiscoveryCheckpoint(existing, item) {
			raw = append(raw, deferredItem(item, sourceContentHash(item.Title, "", nil), now.UnixMilli()))
		}
	}
	for start := 0; start < len(raw); start += notice.HermesMaxBatchItems {
		end := min(start+notice.HermesMaxBatchItems, len(raw))
		if _, err := p.config.Submit(ctx, notice.HermesIngestRequest{
			BatchID:       fmt.Sprintf("%s-%s-page-%d-%d", p.runID, exchange, page, start),
			WindowStartMS: windowStart, Items: raw[start:end],
		}); err != nil {
			return fmt.Errorf("submit %s discovery page %d: %w", exchange, page, err)
		}
		for _, item := range raw[start:end] {
			existing[noticeKey(item.Exchange, item.NoticeID)] = item
		}
	}
	return nil
}
