package notice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"market-share-go/services/collector/restbudget"
)

const detailRetryDelay = 6 * time.Hour

type detailJob struct {
	index int
	item  Item
}

type detailResult struct {
	index int
	item  Item
	err   error
}

func (m *Monitor) readDetails(
	ctx context.Context,
	exchange string,
	items []Item,
	detected time.Time,
	knownAssets map[string]struct{},
) ([]Item, error) {
	if len(items) == 0 {
		return items, nil
	}
	// Notice details are deliberately serialized. This bounded queue keeps the
	// notice subsystem to one active crawl/analysis job at a time on the NAS.
	workerCount := 1
	jobs := make(chan detailJob)
	results := make(chan detailResult, len(items))
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				item, err := m.readDetail(workCtx, job.item, detected, knownAssets)
				results <- detailResult{index: job.index, item: item, err: err}
				if err != nil {
					cancel()
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index, item := range items {
			select {
			case jobs <- detailJob{index: index, item: item}:
			case <-workCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	detailed := append([]Item(nil), items...)
	var detailErrors []error
	for result := range results {
		if result.err != nil {
			detailErrors = append(detailErrors, result.err)
			continue
		}
		detailed[result.index] = result.item
	}
	return detailed, errors.Join(detailErrors...)
}

func (m *Monitor) readDetail(
	ctx context.Context,
	item Item,
	detected time.Time,
	knownAssets map[string]struct{},
) (Item, error) {
	existing, found, err := m.store.LoadNoticeItem(ctx, item.Exchange, item.NoticeID)
	if err != nil {
		return Item{}, err
	}
	if found && noticeDetailUnchanged(existing, item) {
		return existing, nil
	}
	if found && existing.AnalysisStatus == "DETAIL_ERROR" &&
		detected.UnixMilli()-existing.DetailCheckedTSMS < detailRetryDelay.Milliseconds() {
		return existing, nil
	}
	item.DetailCheckedTSMS = detected.UnixMilli()
	if item.BodyExcerpt != "" {
		rawBody := item.BodyExcerpt
		item.BodyExcerpt = NormalizeContent(rawBody)
		item.ImageURLs = detailImageURLs(rawBody, item.URL)
		item.ContentSource = "INLINE_API"
		item = m.analyzeNoticeImages(ctx, item, existing, found, knownAssets)
		return finalizeNoticeAnalysis(item, knownAssets), nil
	}
	body, images, modified, source, err := m.fetchDetail(ctx, item)
	if err != nil {
		kind, _ := classifyPollError(item.Exchange, err, detected)
		if kind == "BITHUMB_RATE_LIMIT" {
			return Item{}, err
		}
		if found {
			return existing, nil
		}
		item.AnalysisStatus = "DETAIL_ERROR"
		return AnalyzeItem(item, knownAssets), nil
	}
	item.BodyExcerpt = NormalizeContent(body)
	item.ImageURLs = images
	item.UpdatedTSMS = max(item.UpdatedTSMS, modified)
	item.ContentSource = source
	item = m.analyzeNoticeImages(ctx, item, existing, found, knownAssets)
	return finalizeNoticeAnalysis(item, knownAssets), nil
}

func (m *Monitor) analyzeNoticeImages(ctx context.Context, item, existing Item, found bool, knownAssets map[string]struct{}) Item {
	if len(item.ImageURLs) == 0 || m.images == nil {
		return item
	}
	if found && equalStrings(existing.ImageURLs, item.ImageURLs) && len(existing.ImageAnalysis) > 0 {
		item.ImageText = existing.ImageText
		item.ImageAnalysis = append([]map[string]any(nil), existing.ImageAnalysis...)
		return item
	}
	assets := make([]string, 0, len(knownAssets))
	for asset := range knownAssets {
		assets = append(assets, asset)
	}
	item.ImageText, item.ImageAnalysis = m.images.Analyze(ctx, item.Exchange, item.ImageURLs, assets)
	return item
}

func finalizeNoticeAnalysis(item Item, knownAssets map[string]struct{}) Item {
	item = AnalyzeItem(item, knownAssets)
	item.AnalysisStatus = imageAnalysisStatus(item)
	if len(item.ImageURLs) > 0 && item.ImageText == "" && item.BodyExcerpt != "" {
		item.AnalysisConfidence = 0.8
	}
	return item
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (m *Monitor) fetchDetail(ctx context.Context, item Item) (string, []string, int64, string, error) {
	if frame, ok := domesticFrame(item.Exchange); ok {
		return m.fetchDomesticDetail(ctx, item, frame.Detail)
	}
	switch item.Exchange {
	case "binance":
		endpoint := "https://www.binance.com/bapi/composite/v1/public/cms/article/detail/query"
		payload, err := m.detailBytes(ctx, item.Exchange, endpoint, url.Values{"articleCode": {item.NoticeID}}, "application/json")
		if err != nil {
			return "", nil, 0, "", err
		}
		root, err := decodeDetailJSON(payload)
		if err != nil {
			return "", nil, 0, "", err
		}
		data := objectValue(root["data"])
		raw := firstText(data["body"], data["contentJson"])
		modified := noticeTimestamp(firstValue(data["lastUpdateTime"], data["publishDate"]))
		return raw, detailImageURLs(raw, item.URL), modified, "OFFICIAL_DETAIL_API", nil
	default:
		if item.URL == "" {
			return "", nil, 0, "", errors.New("notice detail URL is empty")
		}
		payload, err := m.detailBytes(ctx, item.Exchange, item.URL, nil, "text/html,application/xhtml+xml")
		if err != nil {
			return "", nil, 0, "", err
		}
		body, images, modified := ParseDetailPage(string(payload), item.URL)
		return body, images, modified, "OFFICIAL_DETAIL_PAGE", nil
	}
}

func (m *Monitor) fetchDomesticDetail(
	ctx context.Context,
	item Item,
	frame noticeDetailFrame,
) (string, []string, int64, string, error) {
	switch frame.Adapter {
	case "JSON":
		endpoint := fmt.Sprintf(frame.Endpoint, url.PathEscape(item.NoticeID))
		payload, err := m.detailBytes(ctx, item.Exchange, endpoint, frame.Query.values(0), frame.Accept)
		if err != nil {
			return "", nil, 0, "", err
		}
		root, err := decodeDetailJSON(payload)
		if err != nil {
			return "", nil, 0, "", err
		}
		data := objectValue(path(root, frame.DataPath...))
		raw := rowFirstText(data, frame.BodyFields)
		modified := noticeTimestamp(rowFirstValue(data, frame.UpdatedFields))
		return raw, detailImageURLs(raw, item.URL), modified, frame.Kind, nil
	case "KORBIT_CONTENTFUL":
		contentID := korbitContentfulID(item)
		if contentID == "" {
			return "", nil, 0, "", errors.New("Korbit notice Contentful ID is empty")
		}
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		payload, err := m.fetcher.GetBytes(requestCtx, restbudget.Request{
			Exchange: item.Exchange,
			Group:    "public",
			URL:      "https://portal-prod.korbit.co.kr/api/korbit/v2/contentful",
			Query: url.Values{
				"content_type": {"notice"},
				"sys.id":       {contentID},
			},
			Priority: restbudget.PriorityRegistry,
			Cost:     1,
			Accept:   frame.Accept,
			Headers: http.Header{
				"User-Agent":          {"Mozilla/5.0 (compatible; MarketShareNotice/1.0)"},
				"Accept-Language":     {"ko-KR,ko;q=0.9,en;q=0.7"},
				"Content-Type":        {"application/json"},
				"Korbit-Platform-Id":  {"21"},
				"Platform-Identifier": {"witcher_ios"},
			},
		}, defaultMaximumPageBytes)
		if err != nil {
			return "", nil, 0, "", err
		}
		raw, images, modified, err := decodeKorbitContentfulDetail(payload, item.URL)
		return raw, images, modified, frame.Kind, err
	case "PAGE":
		if item.URL == "" {
			return "", nil, 0, "", errors.New("notice detail URL is empty")
		}
		payload, err := m.detailBytes(ctx, item.Exchange, item.URL, nil, frame.Accept)
		if err != nil {
			return "", nil, 0, "", err
		}
		body, images, modified := ParseDetailPage(string(payload), item.URL)
		return body, images, modified, frame.Kind, nil
	default:
		return "", nil, 0, "", fmt.Errorf("unsupported domestic notice detail adapter: %s", frame.Adapter)
	}
}

func korbitContentfulID(item Item) string {
	for _, value := range []string{item.URL, item.NoticeID} {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err == nil {
			if id := strings.TrimSpace(parsed.Query().Get("noticeId")); id != "" {
				return id
			}
		}
	}
	if !strings.ContainsAny(item.NoticeID, "/?&") {
		return strings.TrimSpace(item.NoticeID)
	}
	return ""
}

func decodeKorbitContentfulDetail(payload []byte, sourceURL string) (string, []string, int64, error) {
	root, err := decodeDetailJSON(payload)
	if err != nil {
		return "", nil, 0, err
	}
	items := objectRows(root["items"])
	if len(items) != 1 {
		return "", nil, 0, errors.New("Korbit official detail did not return exactly one notice")
	}
	fields := objectValue(items[0]["fields"])
	body := contentfulText(fields["contents"])
	if body == "" {
		return "", nil, 0, errors.New("Korbit official detail body is empty")
	}
	images := contentfulImageURLs(fields["contents"], sourceURL)
	modified := noticeTimestamp(firstValue(fields["updatedAt"], objectValue(items[0]["sys"])["updatedAt"]))
	return body, images, modified, nil
}

func contentfulText(value any) string {
	values := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case map[string]any:
			if text, ok := typed["value"].(string); ok {
				if text = strings.TrimSpace(text); text != "" {
					values = append(values, text)
				}
				return
			}
			if content, found := typed["content"]; found {
				visit(content)
			}
		}
	}
	visit(value)
	return strings.TrimSpace(strings.Join(values, "\n"))
}

func contentfulImageURLs(value any, sourceURL string) []string {
	seen := map[string]bool{}
	result := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case map[string]any:
			for key, child := range typed {
				if text, ok := child.(string); ok && (strings.EqualFold(key, "url") || strings.EqualFold(key, "src")) {
					candidate := text
					if strings.HasPrefix(candidate, "//") {
						candidate = "https:" + candidate
					}
					resolved, resolveErr := resolveOfficialURL(sourceURL, candidate)
					if resolveErr == nil && imageExtension.MatchString(resolved) && !seen[resolved] {
						seen[resolved] = true
						result = append(result, resolved)
					}
				}
				visit(child)
			}
		}
	}
	visit(value)
	return result
}

func resolveOfficialURL(sourceURL, candidate string) (string, error) {
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(sourceURL)
	if err != nil {
		return "", err
	}
	parsed = base.ResolveReference(parsed)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("unsupported official image URL scheme")
	}
	return parsed.String(), nil
}

func (m *Monitor) detailBytes(
	ctx context.Context,
	exchange, endpoint string,
	query url.Values,
	accept string,
) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return m.fetcher.GetBytes(requestCtx, restbudget.Request{
		Exchange: exchange,
		Group:    "public",
		URL:      endpoint,
		Query:    query,
		Priority: restbudget.PriorityRegistry,
		Cost:     1,
		Accept:   accept,
		Headers: http.Header{
			"User-Agent":      {"Mozilla/5.0 (compatible; MarketShareNotice/1.0)"},
			"Accept-Language": {"ko-KR,ko;q=0.9,en;q=0.7"},
		},
	}, defaultMaximumPageBytes)
}

func decodeDetailJSON(payload []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("decode notice detail: %w", err)
	}
	return value, nil
}

func noticeDetailUnchanged(existing, current Item) bool {
	return existing.Title == current.Title &&
		existing.Category == current.Category &&
		existing.SourceType == current.SourceType &&
		existing.PublishedTSMS == current.PublishedTSMS &&
		existing.UpdatedTSMS == current.UpdatedTSMS &&
		existing.TimestampPrecision == current.TimestampPrecision &&
		existing.AnalysisStatus != "LIST_ONLY" && existing.AnalysisStatus != "DETAIL_ERROR"
}
