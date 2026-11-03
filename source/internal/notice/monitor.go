package notice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"market-share-go/services/collector/restbudget"
)

const (
	defaultMaximumPageBytes = int64(8 << 20)
	defaultPageSafetyLimit  = 500
	incompleteRetry         = 15 * time.Minute
	completeRescan          = 6 * time.Hour
	bithumbBlockDuration    = 6 * time.Hour
)

type SourceConfig struct {
	Exchange    string
	URL         string
	PageStart   int
	Paginated   bool
	PageParser  bool
	FetchDetail bool
}

type noticeFetcher interface {
	GetBytes(context.Context, restbudget.Request, int64) ([]byte, error)
}

type noticeStore interface {
	LoadNoticeSources(context.Context) (map[string]Source, error)
	LoadNoticeItem(context.Context, string, string) (Item, bool, error)
	SaveNoticePoll(context.Context, string, []Item, Source) error
	SaveNoticePollError(context.Context, string, string, string, time.Time) error
}

type Monitor struct {
	fetcher     noticeFetcher
	store       noticeStore
	sources     []SourceConfig
	interval    time.Duration
	knownAssets func() map[string]struct{}
	logger      *slog.Logger
	now         func() time.Time
	images      ImageAnalyzer

	mu     sync.Mutex
	states map[string]Source
}

func DefaultSources() []SourceConfig {
	result := domesticSourceConfigs()
	return append(result, []SourceConfig{
		{Exchange: "binance", URL: "https://www.binance.com/bapi/composite/v1/public/cms/article/list/query", PageStart: 1, Paginated: true, FetchDetail: true},
		{Exchange: "bybit", URL: "https://announcements.bybit.com/en/", PageStart: 1, Paginated: true, PageParser: true, FetchDetail: true},
		{Exchange: "okx", URL: "https://www.okx.com/help/section/announcements-latest-announcements", PageStart: 1, Paginated: true, PageParser: true, FetchDetail: true},
	}...)
}

func SelectSources(names []string) ([]SourceConfig, error) {
	defaults := DefaultSources()
	if len(names) == 0 {
		return defaults, nil
	}
	byName := make(map[string]SourceConfig, len(defaults))
	for _, source := range defaults {
		byName[source.Exchange] = source
	}
	selected := make([]SourceConfig, 0, len(names))
	for _, name := range names {
		source, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unsupported notice source %q", name)
		}
		selected = append(selected, source)
	}
	return selected, nil
}

func NewMonitor(
	fetcher noticeFetcher,
	store noticeStore,
	sources []SourceConfig,
	interval time.Duration,
	knownAssets func() map[string]struct{},
	logger *slog.Logger,
) (*Monitor, error) {
	if fetcher == nil || store == nil {
		return nil, errors.New("notice fetcher and store are required")
	}
	if interval <= 0 {
		return nil, errors.New("notice poll interval must be positive")
	}
	if len(sources) == 0 {
		sources = DefaultSources()
	}
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if source.Exchange == "" || source.URL == "" {
			return nil, errors.New("notice source exchange and URL are required")
		}
		if _, duplicate := seen[source.Exchange]; duplicate {
			return nil, fmt.Errorf("duplicate notice source %s", source.Exchange)
		}
		seen[source.Exchange] = struct{}{}
	}
	if knownAssets == nil {
		knownAssets = func() map[string]struct{} { return map[string]struct{}{} }
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		fetcher: fetcher, store: store, sources: append([]SourceConfig(nil), sources...),
		interval: interval, knownAssets: knownAssets, logger: logger,
		now: time.Now, images: NewLocalImageAnalyzer(fetcher), states: make(map[string]Source, len(sources)),
	}, nil
}

func (m *Monitor) OCRBackend() string {
	if m == nil || m.images == nil {
		return "unavailable"
	}
	return m.images.Backend()
}

func (m *Monitor) SetImageAnalyzer(analyzer ImageAnalyzer) {
	if analyzer == nil {
		analyzer = NewLocalImageAnalyzer(m.fetcher)
	}
	m.images = analyzer
}

func (m *Monitor) Start(ctx context.Context) error {
	states, err := m.store.LoadNoticeSources(ctx)
	if err != nil {
		return fmt.Errorf("load notice source states: %w", err)
	}
	m.mu.Lock()
	for _, config := range m.sources {
		state := states[config.Exchange]
		if state.HistoryStatus == "" {
			state.HistoryStatus = "PENDING"
		}
		m.states[config.Exchange] = state
	}
	m.mu.Unlock()
	go m.run(ctx)
	return nil
}

func (m *Monitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		if err := m.PollOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			m.logger.Warn("notice poll completed with source errors", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) PollOnce(ctx context.Context) error {
	var pollErrors []error
	for _, source := range m.sources {
		if err := m.pollSource(ctx, source); err != nil {
			pollErrors = append(pollErrors, fmt.Errorf("%s: %w", source.Exchange, err))
		}
	}
	return errors.Join(pollErrors...)
}

func (m *Monitor) pollSource(ctx context.Context, config SourceConfig) error {
	now := m.now()
	state := m.sourceState(config.Exchange)
	if state.BlockedUntilTS > float64(now.UnixNano())/1e9 {
		return nil
	}
	scanHistory := historyScanNeeded(state, now)
	items, complete, pages, err := m.readLists(ctx, config, scanHistory, now)
	if err != nil {
		kind, blockedUntil := classifyPollError(config.Exchange, err, now)
		m.rememberError(config.Exchange, kind, err, blockedUntil, now)
		if saveErr := m.store.SaveNoticePollError(ctx, config.Exchange, kind, err.Error(), blockedUntil); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	state.ErrorKind, state.Error = "", ""
	state.ErrorStartedTS, state.ErrorLastSeenTS = 0, 0
	state.ErrorOccurrences, state.BlockedUntilTS = 0, 0
	if scanHistory {
		state.LastHistoryScanTS = float64(now.UnixNano()) / 1e9
		state.PagesScanned = int64(pages)
		state.OldestFetchedTSMS = oldestPublished(items)
		state.HistoryStatus, state.HistoryNote = historyResult(config, complete)
	}
	if err := m.store.SaveNoticePoll(ctx, config.Exchange, items, state); err != nil {
		return err
	}
	m.setSourceState(config.Exchange, state)
	return nil
}

func (m *Monitor) readLists(
	ctx context.Context,
	config SourceConfig,
	scanHistory bool,
	detected time.Time,
) ([]Item, bool, int, error) {
	cutoff := detected.AddDate(0, 0, -HistoryDays).UnixMilli()
	knownAssets := m.knownAssets()
	collected := make(map[string]Item)
	page := config.PageStart
	pagesScanned := 0
	historyComplete := false
	for {
		rows, err := m.readPage(ctx, config, page, detected, knownAssets)
		if err != nil {
			return nil, false, pagesScanned, err
		}
		pagesScanned++
		before := len(collected)
		for _, item := range rows {
			key := item.NoticeID
			if key == "" {
				key = item.URL
			}
			if key != "" {
				collected[key] = item
			}
		}
		if pageReachedCutoff(config.Exchange, rows, cutoff) {
			historyComplete = true
			break
		}
		if !scanHistory || !config.Paginated || len(rows) == 0 || len(collected) == before || pagesScanned >= defaultPageSafetyLimit {
			break
		}
		page++
	}
	items := make([]Item, 0, len(collected))
	for _, item := range collected {
		revisionTS := RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
		if item.PublishedTSMS == 0 || revisionTS >= cutoff {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := PublicationTimestampMS(items[i].PublishedTSMS, items[i].UpdatedTSMS, items[i].DetectedTSMS)
		right := PublicationTimestampMS(items[j].PublishedTSMS, items[j].UpdatedTSMS, items[j].DetectedTSMS)
		return left > right
	})
	if config.FetchDetail {
		detailed, err := m.readDetails(ctx, config.Exchange, items, detected, knownAssets)
		if err != nil {
			return nil, false, pagesScanned, err
		}
		items = detailed
	}
	return items, historyComplete, pagesScanned, nil
}

func (m *Monitor) readPage(
	ctx context.Context,
	config SourceConfig,
	page int,
	detected time.Time,
	knownAssets map[string]struct{},
) ([]Item, error) {
	endpoint := config.URL
	query := noticePageQuery(config.Exchange, page)
	if config.Exchange == "okx" && page > 1 {
		endpoint = strings.TrimRight(endpoint, "/") + fmt.Sprintf("/page/%d", page)
	}
	accept := "application/json"
	if config.PageParser {
		accept = "text/html,application/xhtml+xml"
	}
	payload, err := m.fetcher.GetBytes(ctx, restbudget.Request{
		Exchange: config.Exchange,
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
	if err != nil {
		return nil, err
	}
	if config.PageParser {
		return ParsePage(config.Exchange, string(payload), detected, knownAssets)
	}
	return ParsePayload(config.Exchange, payload, detected, knownAssets)
}

func noticePageQuery(exchange string, page int) url.Values {
	if frame, ok := domesticFrame(exchange); ok {
		return frame.Query.values(page)
	}
	switch exchange {
	case "binance":
		return url.Values{"type": {"1"}, "pageNo": {fmt.Sprint(page)}, "pageSize": {"20"}}
	case "bybit":
		return url.Values{"page": {fmt.Sprint(page)}}
	default:
		return nil
	}
}

func historyScanNeeded(state Source, now time.Time) bool {
	last := time.Unix(0, int64(state.LastHistoryScanTS*1e9))
	switch state.HistoryStatus {
	case "COMPLETE", "SOURCE_LIMITED":
		return state.LastHistoryScanTS == 0 || now.Sub(last) >= completeRescan
	case "INCOMPLETE":
		return state.LastHistoryScanTS == 0 || now.Sub(last) >= incompleteRetry
	default:
		return true
	}
}

func historyResult(config SourceConfig, complete bool) (string, string) {
	if complete {
		return "COMPLETE", "31일 경계까지 공식 목록을 확인했습니다."
	}
	if !config.Paginated {
		return "SOURCE_LIMITED", "공식 공개 목록이 최신 항목만 제공해 31일 전체를 보장할 수 없습니다."
	}
	return "INCOMPLETE", "공식 목록에서 31일 경계에 도달하지 못했습니다."
}

func pageReachedCutoff(exchange string, rows []Item, cutoff int64) bool {
	if len(rows) == 0 {
		return false
	}
	if exchange == "binance" {
		newestByCategory := make(map[string]int64)
		for _, item := range rows {
			if item.PublishedTSMS > newestByCategory[item.Category] {
				newestByCategory[item.Category] = item.PublishedTSMS
			}
		}
		for _, newest := range newestByCategory {
			if newest == 0 || newest > cutoff {
				return false
			}
		}
		return len(newestByCategory) > 0
	}
	newest := int64(0)
	oldest := int64(0)
	for _, item := range rows {
		if item.PublishedTSMS <= 0 {
			continue
		}
		newest = max(newest, item.PublishedTSMS)
		if oldest == 0 || item.PublishedTSMS < oldest {
			oldest = item.PublishedTSMS
		}
	}
	if exchange == "korbit" {
		return oldest > 0 && oldest <= cutoff
	}
	return newest > 0 && newest <= cutoff
}

func oldestPublished(items []Item) int64 {
	oldest := int64(0)
	for _, item := range items {
		if item.PublishedTSMS > 0 && (oldest == 0 || item.PublishedTSMS < oldest) {
			oldest = item.PublishedTSMS
		}
	}
	return oldest
}

func classifyPollError(exchange string, err error, now time.Time) (string, time.Time) {
	if exchange != "bithumb" {
		return "SOURCE_ERROR", time.Time{}
	}
	var httpError *restbudget.HTTPError
	message := strings.ToLower(err.Error())
	if (errors.As(err, &httpError) && (httpError.Status == http.StatusForbidden || httpError.Status == http.StatusTooManyRequests)) ||
		strings.Contains(message, "rate limit") || strings.Contains(message, "cloudflare") || strings.Contains(message, "access denied") {
		return "BITHUMB_RATE_LIMIT", now.Add(bithumbBlockDuration)
	}
	return "SOURCE_ERROR", time.Time{}
}

func (m *Monitor) sourceState(exchange string) Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[exchange]
	if state.HistoryStatus == "" {
		state.HistoryStatus = "PENDING"
	}
	return state
}

func (m *Monitor) setSourceState(exchange string, state Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[exchange] = state
}

func (m *Monitor) rememberError(exchange, kind string, err error, blockedUntil, now time.Time) {
	state := m.sourceState(exchange)
	if state.ErrorStartedTS == 0 {
		state.ErrorStartedTS = float64(now.UnixNano()) / 1e9
	}
	state.ErrorKind = kind
	state.Error = err.Error()
	state.ErrorLastSeenTS = float64(now.UnixNano()) / 1e9
	state.ErrorOccurrences++
	if !blockedUntil.IsZero() {
		state.BlockedUntilTS = float64(blockedUntil.UnixNano()) / 1e9
	}
	m.setSourceState(exchange, state)
}
