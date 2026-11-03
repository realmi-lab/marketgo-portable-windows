package notice

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"market-share-go/services/collector/restbudget"
)

const HermesHistoryDays = 30

var ErrHermesEvidenceEmpty = errors.New("official notice detail body is empty")

var officialPageMetadataPrefix = regexp.MustCompile(`(?i)^published\s+on\s+[a-z]+\s+\d{1,2},\s+\d{4}(?:\s*[·|,-]?\s*\d+\s*min(?:ute)?s?\s+read)?\s*`)

// HermesEvidenceFetcher is the narrow official-source transport contract used
// by the Go Hermes pipeline. The shared REST-budget client satisfies it.
type HermesEvidenceFetcher interface {
	GetBytes(context.Context, restbudget.Request, int64) ([]byte, error)
}

type HermesEvidence struct {
	BodyMarkup string
	Item       Item
	Title      string
	BodyText   string
	ImageURLs  []string
}

// HermesSourceCollector reuses the official exchange parsers without starting
// the retired notice Monitor or its local OCR/semantic analysis path.
type HermesSourceCollector struct {
	monitor          *Monitor
	sources          []SourceConfig
	now              func() time.Time
	detailRetryDelay time.Duration
}

func NewHermesSourceCollector(fetcher HermesEvidenceFetcher) (*HermesSourceCollector, error) {
	if fetcher == nil {
		return nil, errors.New("Hermes official evidence fetcher is required")
	}
	return &HermesSourceCollector{
		monitor: &Monitor{fetcher: fetcher},
		sources: ActiveSources(),
		now:     time.Now,
		// The shared REST client already performs short retries. A final bounded
		// cooldown handles exchange waiting pages whose 429 window outlives those
		// attempts, without slowing ordinary detail collection.
		detailRetryDelay: 2 * time.Second,
	}, nil
}

// Discover returns recent official list metadata for every configured venue.
// Source failures are isolated so one venue cannot hide the other active venues.
func (c *HermesSourceCollector) Discover(ctx context.Context) (map[string][]Item, map[string]error) {
	return c.DiscoverWithCheckpoint(ctx, nil)
}

// DiscoverWithCheckpoint acknowledges each page before fetching the next one.
// Failed sources never appear in the completed-source map. Their acknowledged
// pages remain durable even when a later page is unavailable.
func (c *HermesSourceCollector) DiscoverWithCheckpoint(ctx context.Context, checkpoint func(context.Context, string, int, []Item) error) (map[string][]Item, map[string]error) {
	result := make(map[string][]Item, len(c.sources))
	failures := make(map[string]error)
	for _, source := range c.sources {
		items, err := c.discoverSourceWithCheckpoint(ctx, source, checkpoint)
		if err != nil {
			failures[source.Exchange] = err
			continue
		}
		result[source.Exchange] = items
	}
	return result, failures
}

func (c *HermesSourceCollector) discoverSource(ctx context.Context, source SourceConfig) ([]Item, error) {
	return c.discoverSourceWithCheckpoint(ctx, source, nil)
}

func (c *HermesSourceCollector) DiscoverExchange(ctx context.Context, exchange string, checkpoint func(context.Context, string, int, []Item) error) error {
	for _, source := range c.sources {
		if source.Exchange == exchange {
			_, err := c.discoverSourceWithCheckpoint(ctx, source, checkpoint)
			return err
		}
	}
	return fmt.Errorf("unsupported official notice source %s", exchange)
}

func (c *HermesSourceCollector) discoverSourceWithCheckpoint(ctx context.Context, source SourceConfig, checkpoint func(context.Context, string, int, []Item) error) ([]Item, error) {
	detected := c.now().UTC()
	cutoff := detected.Add(-HermesHistoryDays * 24 * time.Hour).UnixMilli()
	collected := make(map[string]Item)
	page := source.PageStart
	for pages := 0; pages < defaultPageSafetyLimit; pages++ {
		rows, err := c.monitor.readPage(ctx, source, page, detected, map[string]struct{}{})
		if err != nil {
			return nil, err
		}
		if checkpoint != nil {
			if err := checkpoint(ctx, source.Exchange, page, rows); err != nil {
				return nil, fmt.Errorf("checkpoint page %d: %w", page, err)
			}
		}
		before := len(collected)
		for _, item := range rows {
			key := strings.TrimSpace(item.NoticeID)
			if key == "" {
				key = strings.TrimSpace(item.URL)
			}
			if key != "" {
				collected[key] = item
			}
		}
		if pageReachedCutoff(source.Exchange, rows, cutoff) || !source.Paginated ||
			len(rows) == 0 {
			break
		}
		if len(collected) == before {
			return nil, errors.New("official notice pagination repeated without reaching history cutoff")
		}
		if pages == defaultPageSafetyLimit-1 {
			return nil, errors.New("official notice pagination safety limit reached")
		}
		page++
	}
	items := make([]Item, 0, len(collected))
	for _, item := range collected {
		anchor := RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
		if anchor >= cutoff {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := PublicationTimestampMS(items[i].PublishedTSMS, items[i].UpdatedTSMS, items[i].DetectedTSMS)
		right := PublicationTimestampMS(items[j].PublishedTSMS, items[j].UpdatedTSMS, items[j].DetectedTSMS)
		return left > right
	})
	return items, nil
}

// FetchEvidence retrieves only official body/image evidence. It intentionally
// does not run the retired Go semantic analyzer or local OCR implementation.
func (c *HermesSourceCollector) FetchEvidence(ctx context.Context, item Item) (HermesEvidence, error) {
	if SourcePaused(item.Exchange) {
		return HermesEvidence{}, fmt.Errorf("notice source %s is temporarily paused", item.Exchange)
	}
	body := item.BodyExcerpt
	images := []string(nil)
	updated := item.UpdatedTSMS
	if body != "" {
		images = detailImageURLs(body, item.URL)
	} else {
		raw, fetchedImages, modified, err := c.fetchDetailWithRateLimitRetry(ctx, item)
		if err != nil {
			return HermesEvidence{}, err
		}
		body, images, updated = raw, fetchedImages, max(updated, modified)
	}
	item.UpdatedTSMS = updated
	item.BodyExcerpt = ""
	cleanedBody := NormalizeContent(body)
	cleanedBody = cleanOfficialPageMetadata(item.Exchange, item.Title, cleanedBody)
	if cleanedBody == "" && len(images) == 0 {
		return HermesEvidence{}, fmt.Errorf("%w: %s", ErrHermesEvidenceEmpty, item.Exchange)
	}
	return HermesEvidence{
		BodyMarkup: body,
		Item:       item,
		Title:      strings.TrimSpace(item.Title),
		BodyText:   cleanedBody,
		ImageURLs:  append([]string(nil), images...),
	}, nil
}

func (c *HermesSourceCollector) fetchDetailWithRateLimitRetry(ctx context.Context, item Item) (string, []string, int64, error) {
	raw, images, modified, _, err := c.monitor.fetchDetail(ctx, item)
	var httpErr *restbudget.HTTPError
	if err == nil || !errors.As(err, &httpErr) || httpErr.Status != 429 {
		return raw, images, modified, err
	}
	if c.detailRetryDelay > 0 {
		timer := time.NewTimer(c.detailRetryDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", nil, 0, ctx.Err()
		case <-timer.C:
		}
	}
	raw, images, modified, _, err = c.monitor.fetchDetail(ctx, item)
	return raw, images, modified, err
}

// cleanOfficialPageMetadata removes only deterministic page chrome emitted
// ahead of an official article. Values such as "5 min read" are not claims
// made by the exchange and must not participate in numeric grounding.
func cleanOfficialPageMetadata(exchange, title, body string) string {
	if !strings.EqualFold(exchange, "okx") {
		return strings.TrimSpace(body)
	}
	result := strings.TrimSpace(body)
	trimmedTitle := strings.TrimSpace(title)
	if len(result) >= len(trimmedTitle) && strings.EqualFold(result[:len(trimmedTitle)], trimmedTitle) {
		result = strings.TrimSpace(result[len(trimmedTitle):])
	}
	return strings.TrimSpace(officialPageMetadataPrefix.ReplaceAllString(result, ""))
}
