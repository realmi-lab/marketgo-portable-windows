package notice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"market-share-go/services/collector/restbudget"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DiscoverCorpusExchange checkpoints official list pages in a fixed publication
// interval. Nonpaginated lists remain SOURCE_LIMITED even when a pinned old row
// predates the requested boundary. Checkpoint rows retain out-of-window metadata
// for audit; callers must filter before submitting corpus items.
func (c *HermesSourceCollector) DiscoverCorpusExchange(ctx context.Context, exchange string, bounds HermesCorpusRange, checkpoint func(context.Context, string, int, []Item) error) error {
	if err := bounds.Validate(c.now()); err != nil {
		return err
	}
	if !IsDomesticExchange(exchange) {
		return fmt.Errorf("domestic corpus excludes %s", exchange)
	}
	if exchange == "korbit" {
		return c.discoverKorbitCorpus(ctx, bounds, checkpoint)
	}
	for _, source := range c.sources {
		if source.Exchange != exchange {
			continue
		}
		seen := map[string]string{}
		for page, n := source.PageStart, 0; n < defaultPageSafetyLimit; page, n = page+1, n+1 {
			rows, err := c.monitor.readPage(ctx, source, page, c.now(), map[string]struct{}{})
			if err != nil {
				return fmt.Errorf("page %d: %w", page, err)
			}
			for _, item := range rows {
				key := item.NoticeID
				if key == "" {
					key = item.URL
				}
				fingerprint := corpusMetadataFingerprint(item)
				if prior, found := seen[key]; found && prior != fingerprint {
					return fmt.Errorf("page %d source metadata changed for %s", page, key)
				}
			}
			if checkpoint != nil {
				if err = checkpoint(ctx, exchange, page, rows); err != nil {
					return fmt.Errorf("checkpoint page %d: %w", page, err)
				}
			}
			if !source.Paginated {
				return fmt.Errorf("SOURCE_LIMITED: %s official adapter provides no historical pagination", exchange)
			}
			for _, item := range rows {
				if item.PublishedTSMS <= 0 {
					return fmt.Errorf("page %d has unresolved official publication for %s", page, item.NoticeID)
				}
			}
			if len(rows) == 0 {
				return nil
			}
			added := 0
			for _, item := range rows {
				key := item.NoticeID
				if key == "" {
					key = item.URL
				}
				if key != "" && seen[key] == "" {
					seen[key] = corpusMetadataFingerprint(item)
					added++
				}
			}
			if added == 0 {
				return fmt.Errorf("page %d repeated without reaching corpus boundary", page)
			}
			if pageReachedCutoff(exchange, rows, bounds.FromTSMS-1) {
				return nil
			}
		}
		return fmt.Errorf("official corpus pagination safety limit reached")
	}
	return fmt.Errorf("unsupported domestic source %s", exchange)
}

func (c *HermesSourceCollector) discoverKorbitCorpus(ctx context.Context, bounds HermesCorpusRange, checkpoint func(context.Context, string, int, []Item) error) error {
	seen := map[string]string{}
	frozenTotal, previousLast := -1, int64(0)
	for page := 0; page < defaultPageSafetyLimit; page++ {
		payload, err := c.monitor.fetcher.GetBytes(ctx, restbudget.Request{Exchange: "korbit", Group: "public", URL: "https://portal-prod.korbit.co.kr/api/korbit/v2/contentful", Query: url.Values{"content_type": {"notice"}, "limit": {"100"}, "skip": {strconv.Itoa(page * 100)}, "order": {"-fields.createdAt"}}, Priority: restbudget.PriorityRegistry, Cost: 1, Accept: "application/json", Headers: http.Header{"Korbit-Platform-Id": {"21"}, "Platform-Identifier": {"witcher_ios"}}}, defaultMaximumPageBytes)
		if err != nil {
			return fmt.Errorf("Korbit corpus page %d: %w", page, err)
		}
		rows, total, skip, err := decodeKorbitCorpusPage(payload, c.now())
		if err != nil {
			return err
		}
		if skip != page*100 {
			return fmt.Errorf("Korbit corpus returned unexpected offset %d", skip)
		}
		if frozenTotal >= 0 && total != frozenTotal {
			return fmt.Errorf("Korbit corpus total changed during pagination")
		}
		frozenTotal = total
		if previousLast > 0 && len(rows) > 0 && rows[0].PublishedTSMS > previousLast {
			return fmt.Errorf("Korbit corpus cross-page publication order changed")
		}
		for _, row := range rows {
			if prior, found := seen[row.NoticeID]; found && prior != corpusMetadataFingerprint(row) {
				return fmt.Errorf("Korbit corpus source metadata changed for %s", row.NoticeID)
			}
		}
		if checkpoint != nil {
			if err = checkpoint(ctx, "korbit", page, rows); err != nil {
				return err
			}
		}
		added := 0
		newest := int64(0)
		for _, row := range rows {
			if seen[row.NoticeID] == "" {
				seen[row.NoticeID] = corpusMetadataFingerprint(row)
				added++
			}
			newest = max(newest, row.PublishedTSMS)
		}
		if len(rows) > 0 {
			previousLast = rows[len(rows)-1].PublishedTSMS
		}
		if len(rows) == 0 {
			if skip < total {
				return fmt.Errorf("Korbit corpus empty page before official total")
			}
			return nil
		}
		if added == 0 {
			return fmt.Errorf("Korbit corpus repeated page %d", page)
		}
		if newest < bounds.FromTSMS || skip+len(rows) >= total {
			return nil
		}
	}
	return fmt.Errorf("Korbit corpus pagination safety limit reached")
}

func corpusMetadataFingerprint(item Item) string {
	item.DetectedTSMS = 0
	raw, _ := json.Marshal(item)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func decodeKorbitCorpusPage(payload []byte, detected time.Time) ([]Item, int, int, error) {
	var data struct {
		Total *int `json:"total"`
		Skip  *int `json:"skip"`
		Items []struct {
			Sys struct {
				ID string `json:"id"`
			} `json:"sys"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, 0, 0, err
	}
	if data.Total == nil || data.Skip == nil || *data.Total < 0 || *data.Skip < 0 || data.Items == nil {
		return nil, 0, 0, fmt.Errorf("invalid Korbit corpus page")
	}
	rows := []Item{}
	previous := int64(0)
	for _, entry := range data.Items {
		fields := entry.Fields
		published := korbitCorpusTimestamp(fields["createdAt"])
		title := textValue(fields["title"])
		if entry.Sys.ID == "" || published <= 0 || title == "" {
			return nil, 0, 0, fmt.Errorf("Korbit corpus row lacks official id/title/publication")
		}
		if previous > 0 && published > previous {
			return nil, 0, 0, fmt.Errorf("Korbit corpus publication order changed")
		}
		previous = published
		item := Item{Exchange: "korbit", NoticeID: entry.Sys.ID, Title: title, OriginalTitle: title, URL: "https://www.korbit.co.kr/notice/detail/?noticeId=" + url.QueryEscape(entry.Sys.ID), Category: Classify(title, textValue(fields["category"])), PublishedTSMS: published, UpdatedTSMS: korbitCorpusTimestamp(fields["updatedAt"]), DetectedTSMS: detected.UnixMilli(), TimestampPrecision: "SECOND"}
		rows = append(rows, item)
	}
	return rows, *data.Total, *data.Skip, nil
}

func korbitCorpusTimestamp(value any) int64 {
	if stamp := noticeTimestamp(value); stamp > 0 {
		return stamp
	}
	stamp, err := time.Parse("2006-01-02T15:04Z07:00", textValue(value))
	if err != nil {
		return 0
	}
	return stamp.UnixMilli()
}
