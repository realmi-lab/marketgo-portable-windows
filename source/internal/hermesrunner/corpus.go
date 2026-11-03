package hermesrunner

import (
	"context"
	"fmt"
	"market-share-go/internal/notice"
	"sort"
	"strings"
	"time"
)

func ParseCorpusRange(from, to string) (*notice.HermesCorpusRange, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" && to == "" {
		return nil, nil
	}
	start, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return nil, fmt.Errorf("corpus start requires RFC3339 with timezone: %w", err)
	}
	end, err := time.Parse(time.RFC3339, to)
	if err != nil {
		return nil, fmt.Errorf("corpus end requires RFC3339 with timezone: %w", err)
	}
	r := &notice.HermesCorpusRange{FromTSMS: start.UnixMilli(), ToTSMS: end.UnixMilli()}
	if err = r.Validate(time.Now()); err != nil {
		return nil, err
	}
	return r, nil
}

// ExecuteOnce runs one caller-bounded checkpointed batch without a scheduler.
func ExecuteOnce(ctx context.Context, config Config) error { return executeOnce(ctx, config) }

func (p *pipeline) withinIngestWindow(item notice.Item, now time.Time) bool {
	if p.config.CorpusRange == nil {
		return withinCurrentIngestWindow(item, now)
	}
	r := p.config.CorpusRange
	return item.Exchange == p.config.CorpusExchange && notice.IsDomesticExchange(item.Exchange) && item.PublishedTSMS >= r.FromTSMS && item.PublishedTSMS <= r.ToTSMS
}

func (p *pipeline) countDueAnalysisCandidates(items []notice.Item, existing map[string]notice.Item, now time.Time) int {
	n := 0
	for _, item := range items {
		if p.withinIngestWindow(item, now) && !existingListMetadataIsCurrent(existing[noticeKey(item.Exchange, item.NoticeID)], item, now) {
			n++
		}
	}
	return n
}

func (p *pipeline) discover(ctx context.Context, checkpoint func(context.Context, string, int, []notice.Item) error) (map[string][]notice.Item, map[string]error) {
	if p.config.CorpusRange == nil {
		return p.collector.DiscoverWithCheckpoint(ctx, checkpoint)
	}
	ex := p.config.CorpusExchange
	seen := map[string]notice.Item{}
	err := p.collector.DiscoverCorpusExchange(ctx, ex, *p.config.CorpusRange, func(ctx context.Context, exchange string, page int, rows []notice.Item) error {
		if err := writeJSON(p.outputDir+"/"+exchange+"-corpus-page-"+time.Now().Format("150405.000000000")+".json", rows); err != nil {
			return err
		}
		if err := checkpoint(ctx, exchange, page, rows); err != nil {
			return err
		}
		for _, item := range rows {
			if p.withinIngestWindow(item, time.Now()) {
				seen[noticeKey(item.Exchange, item.NoticeID)] = item
			}
		}
		return nil
	})
	items := []notice.Item{}
	for _, item := range seen {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].PublishedTSMS > items[j].PublishedTSMS })
	failures := map[string]error{}
	if err != nil {
		failures[ex] = err
	}
	// A partial source can still drain acknowledged rows, while its source
	// failure remains in the report and no completeness heartbeat is sent.
	return map[string][]notice.Item{ex: items}, failures
}

func (p *pipeline) corpusAnalysisPrecondition(ctx context.Context, pending notice.Item) (map[string]string, error) {
	snapshot, err := p.config.LoadExisting(ctx)
	if err != nil {
		return nil, fmt.Errorf("corpus preimage reload: %w", err)
	}
	for _, current := range snapshot.Items {
		if current.Exchange == pending.Exchange && current.NoticeID == pending.NoticeID {
			if current.ContentHash == pending.ContentHash && current.OriginalBodyText == pending.OriginalBodyText && current.PublishedTSMS == pending.PublishedTSMS && current.OriginalTitle == pending.OriginalTitle && current.DetailCheckedTSMS == pending.DetailCheckedTSMS && current.UpdatedTSMS == pending.UpdatedTSMS && current.AnalysisStatus == pending.AnalysisStatus {
				key := current.Exchange + ":" + current.NoticeID
				token := snapshot.ItemRevisions[key]
				if token != "" {
					return map[string]string{key: token}, nil
				}
				return nil, fmt.Errorf("corpus row revision missing: %s", key)
			}
			return nil, fmt.Errorf("corpus source/analysis changed before model: %s/%s", pending.Exchange, pending.NoticeID)
		}
	}
	return nil, fmt.Errorf("corpus preimage missing: %s/%s", pending.Exchange, pending.NoticeID)
}
