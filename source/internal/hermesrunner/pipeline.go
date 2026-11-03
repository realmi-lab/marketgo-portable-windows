package hermesrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/notice"
)

const (
	analysisPolicyVersion = "notice-analysis-v11-schema-first"
	analysisWindowDays    = 30
	// An official updated timestamp accelerates refresh but is not assumed to
	// cover silent body or same-URL image-byte changes. Revalidate every notice
	// on a bounded cadence while avoiding work on every ten-minute poll.
	noticeEvidenceRevalidationInterval = 24 * time.Hour
	// A failed model run does not invalidate official raw evidence. Avoid
	// refetching hundreds of unchanged pages every polling interval, while
	// still retrying analysis and checking silent edits once per day.
	rawDetailAnalysisRetryInterval = 24 * time.Hour
	// Transient model failures retain the durable official detail and retry
	// sooner than a structurally invalid model result. Runtime configuration
	// failures stop the current sweep after one item so a missing executable
	// cannot stamp every notice as failed in a few seconds.
	rawDetailTransientRetryInterval = 30 * time.Minute
	rawDetailRuntimeRetryInterval   = 10 * time.Minute
	// Discovery metadata is not a completed detail fetch. Drain the initial
	// domestic backlog gradually without fetching hundreds of pages per poll.
	rawDetailPrefetchLimit = 2
)

type pipeline struct {
	config           Config
	collector        *notice.HermesSourceCollector
	runID            string
	outputDir        string
	usageDir         string
	collectionErrors map[string][]error
	knownRevisions   map[string]string
}

type runReport struct {
	CorpusRange *notice.HermesCorpusRange `json:"corpus_range,omitempty"`
	RunID       string                    `json:"run_id"`
	StartedAt   time.Time                 `json:"started_at"`
	FinishedAt  time.Time                 `json:"finished_at"`
	Exchanges   map[string]exchangeReport `json:"exchanges"`
}

type exchangeReport struct {
	DiscoveryStatus    string `json:"discovery_status,omitempty"`
	Discovered         int    `json:"discovered"`
	Prefetched         int    `json:"prefetched"`
	AnalysisAttempts   int    `json:"analysis_attempts"`
	Analyzed           int    `json:"analyzed"`
	OnDemandIndexed    int    `json:"on_demand_indexed"`
	Skipped            int    `json:"skipped"`
	DeferredByRunLimit int    `json:"deferred_by_run_limit"`
	Failed             int    `json:"failed"`
	Status             string `json:"status"`
}

func newPipeline(config Config, collector *notice.HermesSourceCollector) *pipeline {
	knownRevisions := map[string]string{}
	if config.CorpusRange != nil && config.Submit != nil {
		bounds := *config.CorpusRange
		config.CorpusRange = &bounds
		submit := config.Submit
		config.Submit = func(ctx context.Context, request notice.HermesIngestRequest) (notice.HermesIngestResult, error) {
			request.CorpusRange = &bounds
			request.WindowStartMS = bounds.FromTSMS
			// Refreshes of already accepted analyses carry the initial stored
			// version too. Fresh model success/failure supplies its own token
			// captured after the pending checkpoint and before invoking Hermes.
			if request.ExpectedRevisions == nil {
				for _, item := range request.Items {
					if item.AnalysisStatus == "ANALYZED" || item.AnalysisStatus == "ANALYSIS_NOT_REQUIRED" {
						request.ExpectedRevisions = map[string]string{}
						for _, row := range request.Items {
							key := row.Exchange + ":" + row.NoticeID
							request.ExpectedRevisions[key] = knownRevisions[key]
						}
						break
					}
				}
			}
			return submit(ctx, request)
		}
	}
	if config.CollectorOnly {
		config.DisableModelAnalysis = true
		config.VisionOCRBin = ""
	}
	if config.MaxAnalysesPerExchangePerRun <= 0 {
		config.MaxAnalysesPerExchangePerRun = 1
	}
	runID := fmt.Sprintf("%d-%d", time.Now().UTC().UnixMilli(), os.Getpid())
	root := strings.TrimSpace(config.OutputDir)
	if root == "" {
		root = filepath.Join(".tmp", "hermes-notices-go")
	}
	output := filepath.Join(root, runID)
	return &pipeline{
		config: config, collector: collector, runID: runID,
		outputDir: output, usageDir: filepath.Join(output, "usage"), knownRevisions: knownRevisions,
	}
}

func (p *pipeline) run(ctx context.Context) error {
	if err := os.MkdirAll(p.usageDir, 0o700); err != nil {
		return fmt.Errorf("create Hermes output directory: %w", err)
	}
	started := time.Now().UTC()
	report := runReport{RunID: p.runID, StartedAt: started, Exchanges: map[string]exchangeReport{}, CorpusRange: p.config.CorpusRange}
	snapshot, err := p.config.LoadExisting(ctx)
	if err != nil {
		return fmt.Errorf("load existing notices: %w", err)
	}
	if p.config.TargetedReanalysis != nil {
		return p.runTargetedReanalysis(ctx, snapshot, p.analyze)
	}
	existing := make(map[string]notice.Item, len(snapshot.Items))
	for key, value := range snapshot.ItemRevisions {
		p.knownRevisions[key] = value
	}
	for _, item := range snapshot.Items {
		existing[noticeKey(item.Exchange, item.NoticeID)] = item
	}
	windowStart := time.Now().UTC().Add(-analysisWindowDays * 24 * time.Hour).UnixMilli()
	discovered, sourceFailures := p.discover(ctx, func(pageCtx context.Context, exchange string, page int, items []notice.Item) error {
		return p.persistDiscoveryPage(pageCtx, exchange, page, items, existing, windowStart)
	})
	var runErrors []error
	for exchange, sourceErr := range sourceFailures {
		runErrors = append(runErrors, fmt.Errorf("%s discovery: %w", exchange, sourceErr))
		_ = writeText(filepath.Join(p.outputDir, exchange+"-discovery-error.txt"), sourceErr.Error())
		report.Exchanges[exchange] = exchangeReport{Status: "DISCOVERY_FAILED", Failed: 1}
	}
	if err := p.submitDiscoveryHeartbeat(ctx, discovered, windowStart); err != nil {
		return err
	}
	order := make([]string, 0, len(discovered))
	for exchange := range discovered {
		order = append(order, exchange)
	}
	sort.Slice(order, func(i, j int) bool {
		if len(discovered[order[i]]) == len(discovered[order[j]]) {
			return order[i] < order[j]
		}
		return len(discovered[order[i]]) < len(discovered[order[j]])
	})
	queueNow := time.Now().UTC()
	for exchange, items := range discovered {
		discovered[exchange] = orderedHermesAnalysisItems(items, existing, queueNow)
	}
	prefetchedByExchange := make(map[string]int, len(order))
	for _, exchange := range order {
		prefetchedByExchange[exchange] = p.prefetchDomesticRawDetails(
			ctx, exchange, discovered[exchange], existing, windowStart,
		)
		runErrors = append(runErrors, p.collectionErrors[exchange]...)
	}
	localOCRBackfilledByExchange := make(map[string]int, len(order))
	for _, exchange := range order {
		if p.config.CollectorOnly {
			break
		}
		for index, discoveredItem := range discovered[exchange] {
			key := noticeKey(exchange, discoveredItem.NoticeID)
			backfilled, ok := p.backfillMissingLocalImageAnalysis(ctx, existing[key], discoveredItem)
			if !ok {
				continue
			}
			request := notice.HermesIngestRequest{
				BatchID:       p.runID + "-" + fmt.Sprintf("%s-%04d-apple-vision", exchange, index),
				WindowStartMS: windowStart, Items: []notice.Item{backfilled},
			}
			if _, submitErr := p.config.Submit(ctx, request); submitErr != nil {
				runErrors = append(runErrors, fmt.Errorf("%s %s Apple Vision OCR submit: %w", exchange, discoveredItem.NoticeID, submitErr))
				continue
			}
			existing[key] = backfilled
			localOCRBackfilledByExchange[exchange]++
		}
	}
	for _, exchange := range order {
		items := discovered[exchange]
		analysisBudget := newPerExchangeAnalysisBudget(p.config.MaxAnalysesPerExchangePerRun)
		venueReport := exchangeReport{
			Discovered: len(items), Prefetched: prefetchedByExchange[exchange],
			OnDemandIndexed: localOCRBackfilledByExchange[exchange], Status: "COMPLETED",
			Failed: len(p.collectionErrors[exchange]),
		}
		if sourceFailures[exchange] != nil {
			venueReport.Failed++
		}
		if p.config.CorpusRange != nil {
			venueReport.DiscoveryStatus = "BOUNDARY_OR_SOURCE_END_REACHED"
			if sourceFailures[exchange] != nil {
				venueReport.DiscoveryStatus = "PARTIAL"
			}
		}
		for index, discoveredItem := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !p.withinIngestWindow(discoveredItem, time.Now().UTC()) {
				venueReport.Skipped++
				continue
			}
			key := noticeKey(exchange, discoveredItem.NoticeID)
			current := existing[key]
			if existingListMetadataIsCurrent(current, discoveredItem, time.Now().UTC()) {
				venueReport.Skipped++
				continue
			}
			if p.config.DisableModelAnalysis {
				venueReport.DeferredByRunLimit = p.countDueAnalysisCandidates(items[index:], existing, time.Now().UTC())
				venueReport.Skipped += len(items) - index
				break
			}
			if analysisBudget.Exhausted() {
				venueReport.DeferredByRunLimit = p.countDueAnalysisCandidates(items[index:], existing, time.Now().UTC())
				venueReport.Skipped += len(items) - index
				break
			}
			evidence, fetchErr := p.collector.FetchEvidence(ctx, discoveredItem)
			evidence, fetchErr = persistedImageOnlyEvidenceFallback(current, discoveredItem, evidence, fetchErr)
			if fetchErr != nil {
				if errors.Is(fetchErr, notice.ErrHermesEvidenceEmpty) {
					checked := deferredEmptyEvidenceItem(discoveredItem, time.Now().UTC().UnixMilli())
					request := notice.HermesIngestRequest{
						BatchID:       p.runID + "-" + fmt.Sprintf("%s-%04d-empty-detail", exchange, index),
						WindowStartMS: windowStart, Items: []notice.Item{checked},
					}
					if _, submitErr := p.config.Submit(ctx, request); submitErr != nil {
						runErrors = append(runErrors, fmt.Errorf("%s %s empty detail checkpoint: %w", exchange, discoveredItem.NoticeID, submitErr))
					} else {
						existing[key] = checked
					}
				}
				venueReport.Failed++
				runErrors = append(runErrors, fmt.Errorf("%s %s detail: %w", exchange, discoveredItem.NoticeID, fetchErr))
				_ = writeText(filepath.Join(p.outputDir, fmt.Sprintf("%s-%04d-detail-error.txt", exchange, index)), fetchErr.Error())
				continue
			}
			stem := fmt.Sprintf("%s-%04d", exchange, index)
			materialized, cleanup, materializeErr := p.materializeImages(ctx, evidence, stem)
			if materializeErr != nil {
				venueReport.Failed++
				runErrors = append(runErrors, materializeErr)
				continue
			}
			evidence = materialized.HermesEvidence
			hash := materializedSourceContentHash(materialized)
			if current.ContentHash == hash {
				materialized = reuseStoredLocalImageAnalysis(current, materialized)
			}
			if existingAnalysisIsCurrent(current, hash) {
				cleanup()
				checkedMS := time.Now().UTC().UnixMilli()
				refreshed := refreshUnchangedAnalysis(current, evidence, checkedMS)
				request := notice.HermesIngestRequest{
					BatchID:       p.runID + "-" + fmt.Sprintf("%s-%04d-unchanged", exchange, index),
					WindowStartMS: windowStart, Items: []notice.Item{refreshed},
				}
				if _, submitErr := p.config.Submit(ctx, request); submitErr != nil {
					venueReport.Failed++
					runErrors = append(runErrors, fmt.Errorf("%s %s unchanged checkpoint: %w", exchange, discoveredItem.NoticeID, submitErr))
					continue
				}
				existing[key] = refreshed
				venueReport.Skipped++
				continue
			}
			materialized = p.withLocalImageAnalysis(ctx, materialized)
			checkedMS := time.Now().UTC().UnixMilli()
			pending := attachLocalImageAnalysis(pendingEvidenceItem(evidence, hash, checkedMS), materialized)
			pendingRequest := notice.HermesIngestRequest{
				BatchID: p.runID + "-" + stem + "-detail-checkpoint", WindowStartMS: windowStart,
				Items: []notice.Item{pending},
			}
			if _, submitErr := p.config.Submit(ctx, pendingRequest); submitErr != nil {
				venueReport.Failed++
				runErrors = append(runErrors, fmt.Errorf("%s %s detail checkpoint: %w", exchange, discoveredItem.NoticeID, submitErr))
				continue
			}
			existing[key] = pending
			var expected map[string]string
			if p.config.CorpusRange != nil {
				var preimageErr error
				expected, preimageErr = p.corpusAnalysisPrecondition(ctx, pending)
				if preimageErr != nil {
					cleanup()
					venueReport.Failed++
					runErrors = append(runErrors, preimageErr)
					_ = writeText(filepath.Join(p.outputDir, stem+"-preimage-error.txt"), preimageErr.Error())
					continue
				}
			}
			analysisBudget.Acquire()
			decision, attempts, analyzeErr := p.analyzeWithContinuation(ctx, materialized, stem)
			venueReport.AnalysisAttempts += attempts
			cleanup()
			if analyzeErr != nil {
				deferred, runtimeUnavailable := deferredEvidenceAfterAnalysisFailure(
					evidence, hash, time.Now().UTC().UnixMilli(), analyzeErr,
				)
				deferred = attachLocalImageAnalysis(deferred, materialized)
				request := notice.HermesIngestRequest{
					BatchID: p.runID + "-" + stem + "-analysis-failed", WindowStartMS: windowStart,
					Items: []notice.Item{deferred}, ExpectedRevisions: expected,
				}
				if _, submitErr := p.config.Submit(ctx, request); submitErr != nil {
					runErrors = append(runErrors, fmt.Errorf("%s %s deferred detail submit: %w", exchange, discoveredItem.NoticeID, submitErr))
				}
				venueReport.Failed++
				runErrors = append(runErrors, fmt.Errorf("%s %s analysis: %w", exchange, discoveredItem.NoticeID, analyzeErr))
				_ = writeText(filepath.Join(p.outputDir, stem+"-analysis-error.txt"), analyzeErr.Error())
				if runtimeUnavailable {
					return fmt.Errorf("%s %s Hermes runtime unavailable: %w", exchange, discoveredItem.NoticeID, analyzeErr)
				}
				continue
			}
			analyzed := normalizeAnalyzedItem(materialized, decision, hash, time.Now().UTC().UnixMilli())
			request := notice.HermesIngestRequest{
				BatchID: p.runID + "-" + stem, WindowStartMS: windowStart,
				Items: []notice.Item{analyzed}, ExpectedRevisions: expected,
			}
			if err := writeJSON(filepath.Join(p.outputDir, stem+"-checkpoint.json"), request); err != nil {
				return err
			}
			if _, submitErr := p.config.Submit(ctx, request); submitErr != nil {
				venueReport.Failed++
				runErrors = append(runErrors, fmt.Errorf("%s %s submit: %w", exchange, discoveredItem.NoticeID, submitErr))
				_ = writeText(filepath.Join(p.outputDir, stem+"-submit-error.txt"), submitErr.Error())
				continue
			}
			existing[key] = analyzed
			venueReport.Analyzed++
		}
		if venueReport.Failed > 0 {
			venueReport.Status = "PARTIAL_FAILED"
		} else if venueReport.DeferredByRunLimit > 0 {
			venueReport.Status = "PARTIAL_LIMITED"
		}
		report.Exchanges[exchange] = venueReport
	}
	report.FinishedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(p.outputDir, "run.json"), report); err != nil {
		return err
	}
	return errors.Join(runErrors...)
}

type perExchangeAnalysisBudget struct {
	limit int
	used  int
}

func newPerExchangeAnalysisBudget(limit int) *perExchangeAnalysisBudget {
	if limit <= 0 {
		limit = 1
	}
	return &perExchangeAnalysisBudget{limit: limit}
}

func (b *perExchangeAnalysisBudget) Exhausted() bool {
	return b != nil && b.used >= b.limit
}

func (b *perExchangeAnalysisBudget) Acquire() {
	if b != nil && !b.Exhausted() {
		b.used++
	}
}

func countDueAnalysisCandidates(items []notice.Item, existing map[string]notice.Item, now time.Time) int {
	count := 0
	for _, item := range items {
		if withinCurrentIngestWindow(item, now) &&
			!existingListMetadataIsCurrent(existing[noticeKey(item.Exchange, item.NoticeID)], item, now) {
			count++
		}
	}
	return count
}

func (p *pipeline) submitDiscoveryHeartbeat(
	ctx context.Context,
	discovered map[string][]notice.Item,
	windowStart int64,
) error {
	if p.config.CorpusRange != nil {
		return nil
	}
	scannedExchanges := successfulDiscoveryExchanges(discovered)
	if len(scannedExchanges) == 0 {
		return nil
	}
	if _, err := p.config.Submit(ctx, notice.HermesIngestRequest{
		BatchID: p.runID + "-discovery-heartbeat", WindowStartMS: windowStart,
		ScannedExchanges: scannedExchanges, Items: []notice.Item{},
	}); err != nil {
		return fmt.Errorf("submit discovery heartbeat: %w", err)
	}
	return nil
}

func successfulDiscoveryExchanges(discovered map[string][]notice.Item) []string {
	result := make([]string, 0, len(discovered))
	for exchange := range discovered {
		exchange = strings.ToLower(strings.TrimSpace(exchange))
		if exchange != "" {
			result = append(result, exchange)
		}
	}
	sort.Strings(result)
	return result
}

func orderedHermesAnalysisItems(
	items []notice.Item,
	existing map[string]notice.Item,
	now time.Time,
) []notice.Item {
	result := append([]notice.Item(nil), items...)
	sort.SliceStable(result, func(i, j int) bool {
		left := hermesAnalysisQueuePriority(result[i], existing, now)
		right := hermesAnalysisQueuePriority(result[j], existing, now)
		return left < right
	})
	return result
}

func hermesAnalysisQueuePriority(item notice.Item, existing map[string]notice.Item, now time.Time) int {
	if current, found := existing[noticeKey(item.Exchange, item.NoticeID)]; found &&
		existingListMetadataIsCurrent(current, item, now) {
		// Already-current rows are cheap skips, but moving them behind real work
		// prevents a long recent catalog prefix from delaying due candidates.
		return 2
	}
	switch deferredEvidenceCategory(item.Title, item.Category) {
	case "DELISTING", "MARKET_ADDITION", "QUOTE_MARKET_CHANGE", "LISTING", "CAUTION",
		"SECURITY", "WALLET", "NETWORK", "DEPOSIT_WITHDRAWAL", "NETWORK_UPGRADE",
		"TOKEN_SWAP", "REBRAND", "MAINTENANCE":
		return 0
	default:
		return 1
	}
}

func withinCurrentIngestWindow(item notice.Item, now time.Time) bool {
	anchor := max(item.PublishedTSMS, item.UpdatedTSMS)
	if anchor <= 0 {
		anchor = item.DetectedTSMS
	}
	// Leave a small boundary margin because validation recomputes the exact
	// 30-day cutoff a few milliseconds after this check.
	return anchor >= now.Add(-notice.HermesIngestWindowDays*24*time.Hour).Add(5*time.Second).UnixMilli() &&
		anchor <= now.Add(24*time.Hour).UnixMilli()
}

func noticeKey(exchange, noticeID string) string {
	return strings.ToLower(strings.TrimSpace(exchange)) + "\x00" + strings.TrimSpace(noticeID)
}

func shouldPersistDiscoveryCheckpoint(existing map[string]notice.Item, item notice.Item) bool {
	_, found := existing[noticeKey(item.Exchange, item.NoticeID)]
	return !found
}

func shouldPrefetchRawDetail(current, discovered notice.Item, now time.Time) bool {
	return !isForeignExchange(discovered.Exchange) &&
		strings.EqualFold(strings.TrimSpace(current.ContentSource), "HERMES_RAW") &&
		strings.EqualFold(strings.TrimSpace(current.AnalysisStatus), "ANALYSIS_DEFERRED_QUOTA") &&
		strings.TrimSpace(current.OriginalBodyText) == "" &&
		!existingListMetadataIsCurrent(current, discovered, now)
}

func persistedImageOnlyEvidenceFallback(
	current, discovered notice.Item,
	evidence notice.HermesEvidence,
	fetchErr error,
) (notice.HermesEvidence, error) {
	if fetchErr == nil || strings.TrimSpace(current.OriginalBodyText) != "" || len(current.ImageURLs) == 0 {
		return evidence, fetchErr
	}
	// A previously verified image-only official revision remains usable when
	// the exchange temporarily blocks the detail HTML. Image bytes are still
	// downloaded and hashed before model analysis.
	return notice.HermesEvidence{
		Item: discovered, Title: strings.TrimSpace(discovered.Title),
		ImageURLs: append([]string(nil), current.ImageURLs...),
	}, nil
}

func (p *pipeline) prefetchDomesticRawDetails(
	ctx context.Context,
	exchange string,
	items []notice.Item,
	existing map[string]notice.Item,
	windowStart int64,
) int {
	prefetched := 0
	attempted := 0
	limit := rawDetailPrefetchLimit
	if p.config.CorpusRange != nil && p.config.MaxDetailsPerExchangePerRun > 0 {
		limit = min(p.config.MaxDetailsPerExchangePerRun, 20)
	}
	for index, discoveredItem := range items {
		if attempted >= limit {
			break
		}
		if ctx.Err() != nil || !p.withinIngestWindow(discoveredItem, time.Now().UTC()) {
			continue
		}
		key := noticeKey(exchange, discoveredItem.NoticeID)
		current, found := existing[key]
		due := shouldPrefetchRawDetail(current, discoveredItem, time.Now().UTC())
		if p.config.CollectorOnly {
			due = !isForeignExchange(exchange) && !existingCollectionIsCurrent(current, discoveredItem, time.Now().UTC())
		}
		if !found || !due {
			continue
		}
		attempted++
		evidence, err := p.collector.FetchEvidence(ctx, discoveredItem)
		if !p.config.CollectorOnly {
			evidence, err = persistedImageOnlyEvidenceFallback(current, discoveredItem, evidence, err)
		}
		if err != nil || (strings.TrimSpace(evidence.BodyText) == "" && len(evidence.ImageURLs) == 0) {
			if err == nil {
				err = notice.ErrHermesEvidenceEmpty
			}
			p.recordCollectionError(exchange, discoveredItem.NoticeID, "detail", err)
			continue
		}
		stem := fmt.Sprintf("%s-%04d-prefetch", exchange, index)
		materialized, cleanup, materializeErr := p.materializeImages(ctx, evidence, stem)
		if materializeErr != nil {
			p.recordCollectionError(exchange, discoveredItem.NoticeID, "images", materializeErr)
			continue
		}
		for _, image := range materialized.LocalImages {
			if image.Error != "" {
				p.recordCollectionError(exchange, discoveredItem.NoticeID, "image", errors.New(image.Error))
			}
		}
		evidence = materialized.HermesEvidence
		hash := materializedSourceContentHash(materialized)
		checkedMS := time.Now().UTC().UnixMilli()
		pending := pendingEvidenceItem(evidence, hash, checkedMS)
		if p.config.CollectorOnly && current.ContentHash == hash {
			// Revalidation of identical bytes must not discard accepted analysis.
			pending = refreshUnchangedAnalysis(current, evidence, checkedMS)
		}
		cleanup()
		request := notice.HermesIngestRequest{
			BatchID:       fmt.Sprintf("%s-%s-%04d-prefetch", p.runID, exchange, index),
			WindowStartMS: windowStart, Items: []notice.Item{pending},
		}
		if _, err := p.config.Submit(ctx, request); err != nil {
			p.recordCollectionError(exchange, discoveredItem.NoticeID, "submit", err)
			continue
		}
		existing[key] = pending
		prefetched++
	}
	return prefetched
}

func (p *pipeline) recordCollectionError(exchange, id, stage string, err error) {
	if p.collectionErrors == nil {
		p.collectionErrors = make(map[string][]error)
	}
	failure := fmt.Errorf("%s %s collection %s: %w", exchange, id, stage, err)
	p.collectionErrors[exchange] = append(p.collectionErrors[exchange], failure)
	_ = writeText(filepath.Join(p.outputDir, fmt.Sprintf("%s-prefetch-%04d-error.txt", exchange, len(p.collectionErrors[exchange]))), failure.Error())
}

// Collection freshness is independent of analysis admission. A durable pending
// body is current for the collector, while an analyzer must resume it immediately.
func existingCollectionIsCurrent(current, discovered notice.Item, now time.Time) bool {
	if (strings.TrimSpace(current.OriginalBodyText) == "" && len(current.ImageURLs) == 0) ||
		len(current.ContentHash) != sha256.Size*2 || current.DetailCheckedTSMS <= 0 ||
		hasKnownDecorativeNoticeImageURL(current.ImageURLs) ||
		!existingOfficialMetadataMatches(current, discovered) || discovered.UpdatedTSMS > current.UpdatedTSMS {
		return false
	}
	checkedAt := time.UnixMilli(current.DetailCheckedTSMS)
	return !checkedAt.After(now.Add(5*time.Minute)) && now.Sub(checkedAt) < noticeEvidenceRevalidationInterval
}

func sourceContentHash(title, body string, images []string) string {
	ordered := append([]string(nil), images...)
	sort.Strings(ordered)
	payload := map[string]any{
		"body": strings.TrimSpace(body), "images": ordered, "title": strings.TrimSpace(title),
	}
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
	digest := sha256.Sum256([]byte(strings.TrimSuffix(builder.String(), "\n")))
	return hex.EncodeToString(digest[:])
}

// materializedSourceContentHash binds an analyzed revision to the bytes that
// vision actually received, not only to an image URL that may later serve
// different content. Failed downloads are also part of the identity so a
// later successful fetch creates a new revision and retries image analysis.
func materializedSourceContentHash(evidence materializedEvidence) string {
	localByURL := make(map[string]localImage, len(evidence.LocalImages))
	for _, image := range evidence.LocalImages {
		localByURL[image.URL] = image
	}
	images := make([]map[string]any, 0, len(evidence.ImageURLs))
	for _, imageURL := range evidence.ImageURLs {
		local := localByURL[imageURL]
		images = append(images, map[string]any{
			"url": imageURL, "sha256": local.SHA256, "bytes": local.Bytes,
			"error": strings.TrimSpace(local.Error),
		})
	}
	payload := map[string]any{
		"body":   strings.TrimSpace(evidence.BodyText),
		"images": images,
		"title":  strings.TrimSpace(evidence.Title),
	}
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
	digest := sha256.Sum256([]byte(strings.TrimSuffix(builder.String(), "\n")))
	return hex.EncodeToString(digest[:])
}

func existingAnalysisIsCurrent(current notice.Item, hash string) bool {
	// Content identity, not prompt provenance, controls incremental validity.
	// A policy-only change must never fan out into an implicit 30-day LLM rerun;
	// a deliberate bounded backfill can opt in when an old result is unusable.
	status := strings.ToUpper(strings.TrimSpace(current.AnalysisStatus))
	if requiresDomesticSemanticAnalysis(current) {
		return false
	}
	for _, imageURL := range current.ImageURLs {
		complete := false
		for _, row := range current.ImageAnalysis {
			if mapString(row, "url") != imageURL {
				continue
			}
			switch strings.ToUpper(mapString(row, "status")) {
			case "ANALYZED", "NO_TEXT", "UNSUPPORTED":
				complete = true
			}
		}
		if !complete {
			return false
		}
	}
	return (status == "ANALYZED" || status == "ANALYSIS_NOT_REQUIRED") &&
		current.ContentHash == hash
}

func existingListMetadataIsCurrent(current, discovered notice.Item, now time.Time) bool {
	if strings.EqualFold(strings.TrimSpace(current.ContentSource), "HERMES_RAW") &&
		strings.EqualFold(strings.TrimSpace(current.AnalysisStatus), "ANALYSIS_DEFERRED_QUOTA") {
		// A list-only checkpoint never fetched the body. The per-exchange
		// prefetch/model budgets, not a false daily completion, bound this work.
		return false
	}
	if requiresDomesticSemanticAnalysis(current) {
		return false
	}
	status := strings.ToUpper(strings.TrimSpace(current.AnalysisStatus))
	analyzed := status == "ANALYZED" || status == "ANALYSIS_NOT_REQUIRED"
	rawCheckpoint := isRawDetailCheckpoint(current)
	if (!analyzed && !rawCheckpoint) ||
		len(current.ContentHash) != sha256.Size*2 || current.DetailCheckedTSMS <= 0 {
		return false
	}
	if hasKnownDecorativeNoticeImageURL(current.ImageURLs) {
		return false
	}
	if !existingOfficialMetadataMatches(current, discovered) {
		return false
	}
	if rawCheckpoint {
		checkedAt := time.UnixMilli(current.DetailCheckedTSMS)
		if checkedAt.After(now.Add(5*time.Minute)) || now.Sub(checkedAt) >= rawDetailCheckpointRetryInterval(current) {
			return false
		}
		return discovered.UpdatedTSMS <= current.UpdatedTSMS
	}
	if discovered.UpdatedTSMS > current.UpdatedTSMS {
		return false
	}
	checkedAt := time.UnixMilli(current.DetailCheckedTSMS)
	if checkedAt.After(now.Add(5 * time.Minute)) {
		return false
	}
	return now.Sub(checkedAt) < noticeEvidenceRevalidationInterval
}

func existingOfficialMetadataMatches(current, discovered notice.Item) bool {
	if !strings.EqualFold(strings.TrimSpace(current.Exchange), strings.TrimSpace(discovered.Exchange)) ||
		strings.TrimSpace(current.NoticeID) != strings.TrimSpace(discovered.NoticeID) ||
		strings.TrimSpace(current.URL) != strings.TrimSpace(discovered.URL) ||
		current.PublishedTSMS != discovered.PublishedTSMS {
		return false
	}
	originalTitle := strings.TrimSpace(current.OriginalTitle)
	if originalTitle == "" {
		originalTitle = strings.TrimSpace(current.Title)
	}
	if originalTitle != strings.TrimSpace(discovered.Title) {
		return false
	}
	if precision := strings.TrimSpace(discovered.TimestampPrecision); precision != "" &&
		!strings.EqualFold(strings.TrimSpace(current.TimestampPrecision), precision) {
		return false
	}
	return true
}

func requiresDomesticSemanticAnalysis(item notice.Item) bool {
	if !notice.IsDomesticExchange(item.Exchange) ||
		!strings.EqualFold(strings.TrimSpace(item.AnalysisStatus), "ANALYSIS_NOT_REQUIRED") {
		return false
	}
	source := strings.ToUpper(strings.TrimSpace(item.ContentSource))
	// Older domestic failures were stored as completed deterministic summaries.
	// Resume only those explicit failed attempts, through the existing bounded
	// per-exchange queue; a policy change does not invalidate analyzed revisions.
	return source == "HERMES_RAW_DETAIL" ||
		source == "DETERMINISTIC_FALLBACK" && strings.EqualFold(strings.TrimSpace(item.EscalationReason), "MODEL_ANALYSIS_FAILED")
}

func existingRawDetailCheckpointIsCurrent(current, discovered notice.Item, now time.Time) bool {
	return isRawDetailCheckpoint(current) && existingListMetadataIsCurrent(current, discovered, now)
}

func isRawDetailCheckpoint(item notice.Item) bool {
	status := strings.ToUpper(strings.TrimSpace(item.AnalysisStatus))
	source := strings.ToUpper(strings.TrimSpace(item.ContentSource))
	switch {
	case status == "ANALYSIS_NOT_REQUIRED" && source == "HERMES_RAW_DETAIL":
		return strings.TrimSpace(item.OriginalBodyText) != ""
	case status == "ANALYSIS_FAILED" && source == "HERMES_RAW_DETAIL":
		return strings.TrimSpace(item.OriginalBodyText) != ""
	case status == "ANALYSIS_FAILED" && source == "HERMES_RAW_DETAIL_EMPTY":
		return true
	case status == "ANALYSIS_DEFERRED_QUOTA" && source == "HERMES_RAW":
		// Persisted discovery metadata is a checkpoint, but never a completed
		// body fetch. existingListMetadataIsCurrent keeps it in the bounded queue.
		return true
	case status == "ANALYSIS_DEFERRED_QUOTA" && source == "HERMES_RAW_DETAIL" &&
		strings.HasPrefix(strings.ToUpper(strings.TrimSpace(item.EscalationReason)), "RETRY_"):
		// A detail checkpoint without a reason is an in-flight write and must be
		// resumed immediately after an interrupted worker. A classified transient
		// failure is a completed attempt and observes its bounded retry interval.
		return strings.TrimSpace(item.OriginalBodyText) != "" || len(item.ImageURLs) > 0
	default:
		return false
	}
}

func rawDetailCheckpointRetryInterval(item notice.Item) time.Duration {
	switch strings.ToUpper(strings.TrimSpace(item.EscalationReason)) {
	case "RETRY_RUNTIME_UNAVAILABLE":
		return rawDetailRuntimeRetryInterval
	case "RETRY_QUOTA_OR_RATE_LIMIT", "RETRY_TIMEOUT_OR_PREEMPTION", "RETRY_PROVIDER_TRANSIENT":
		return rawDetailTransientRetryInterval
	default:
		return rawDetailAnalysisRetryInterval
	}
}

func analysisFailureRetryDisposition(err error) (reason string, retryable, runtimeUnavailable bool) {
	if err == nil {
		return "", false, false
	}
	text := strings.ToLower(err.Error())
	if containsAnyText(text,
		"executable file not found", "no such file or directory", "permission denied", "exec format error",
	) {
		return "RETRY_RUNTIME_UNAVAILABLE", true, true
	}
	if containsAnyText(text, "quota", "rate limit", "rate-limit", "too many requests", "exhausted") {
		return "RETRY_QUOTA_OR_RATE_LIMIT", true, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		containsAnyText(text, "context canceled", "context deadline exceeded", "timed out", "timeout") {
		return "RETRY_TIMEOUT_OR_PREEMPTION", true, false
	}
	if containsAnyText(text,
		"temporarily unavailable", "service unavailable", "connection reset", "connection refused",
		"unexpected eof", "provider unavailable", "exit status", "codex app-server turn failed",
	) {
		return "RETRY_PROVIDER_TRANSIENT", true, false
	}
	// Keep model/schema/evidence rejection distinct from infrastructure
	// failures. It is not an immediate transient retry, but the durable raw
	// checkpoint will be revalidated on the normal bounded analysis cadence.
	return "ANALYSIS_VALIDATION_REJECTED", false, false
}

func containsAnyText(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func refreshUnchangedAnalysis(current notice.Item, evidence notice.HermesEvidence, checkedMS int64) notice.Item {
	current.URL = strings.TrimSpace(evidence.Item.URL)
	current.PublishedTSMS = evidence.Item.PublishedTSMS
	current.UpdatedTSMS = max(current.UpdatedTSMS, evidence.Item.UpdatedTSMS)
	if precision := strings.TrimSpace(evidence.Item.TimestampPrecision); precision != "" {
		current.TimestampPrecision = precision
	}
	current.OriginalTitle = strings.TrimSpace(evidence.Title)
	current.DetailCheckedTSMS = checkedMS
	return current
}

func deferredItem(item notice.Item, hash string, checkedMS int64) notice.Item {
	foreign := isForeignExchange(item.Exchange)
	item.Category = deferredEvidenceCategory(item.Title, item.Category)
	item.SourceType = "HERMES"
	item.BodyExcerpt, item.Summary, item.ImageText = "", "", ""
	item.Assets, item.Schedules, item.RiskTags = notice.TitleAssets(item.Title), []notice.Schedule{}, []string{}
	item.ImageURLs, item.ImageAnalysis = []string{}, []map[string]any{}
	item.ContentSource, item.AnalysisStatus = "HERMES_RAW", "ANALYSIS_DEFERRED_QUOTA"
	item.AnalysisConfidence, item.ContentHash = 0, hash
	item.DetailCheckedTSMS = checkedMS
	item.OriginalTitle, item.OriginalBodyText = item.Title, ""
	item.OriginalLanguage = "ko"
	item.TranslationStatus = "NOT_REQUIRED"
	if foreign {
		item.OriginalLanguage, item.TranslationStatus = "en", "PENDING"
	}
	item.Importance = "NORMAL"
	item.AnalysisProvider, item.AnalysisModel, item.ReasoningEffort, item.EscalationReason = "", "", "", ""
	item.AnalysisPolicy = analysisPolicyVersion
	return item
}

func deferredEvidenceItem(evidence notice.HermesEvidence, hash string, checkedMS int64) notice.Item {
	item := deferredItem(evidence.Item, hash, checkedMS)
	item.Title = strings.TrimSpace(evidence.Title)
	item.Category = deferredEvidenceCategory(item.Title, evidence.Item.Category)
	item.Assets = notice.TitleAssets(item.Title)
	item.BodyExcerpt = truncateRunes(strings.TrimSpace(evidence.BodyText), 1_500)
	item.OriginalTitle = item.Title
	item.OriginalBodyText = strings.TrimSpace(evidence.BodyText)
	item.ImageURLs = append([]string(nil), evidence.ImageURLs...)
	item.ContentSource = "HERMES_RAW_DETAIL"
	item.AnalysisStatus = "ANALYSIS_FAILED"
	return item
}

func deferredEvidenceAfterAnalysisFailure(
	evidence notice.HermesEvidence,
	hash string,
	checkedMS int64,
	err error,
) (notice.Item, bool) {
	item := deferredEvidenceItem(evidence, hash, checkedMS)
	retryReason, retryable, runtimeUnavailable := analysisFailureRetryDisposition(err)
	item.EscalationReason = retryReason
	if retryable {
		item.AnalysisStatus = "ANALYSIS_DEFERRED_QUOTA"
	}
	if domesticNoticeCanUseOnDemandEvidence(evidence) {
		// Preserve a readable projection of the official body without marking a
		// failed model attempt complete or erasing its retry disposition.
		projection := normalizeOnDemandNotice(evidence, hash, checkedMS)
		item.Summary, item.Schedules, item.TitleActions = projection.Summary, projection.Schedules, projection.TitleActions
	}
	return item, runtimeUnavailable
}

func domesticNoticeUsesOnDemandAnalysis(exchange string) bool {
	return notice.IsDomesticExchange(exchange)
}

func domesticNoticeCanUseOnDemandEvidence(evidence notice.HermesEvidence) bool {
	return domesticNoticeUsesOnDemandAnalysis(evidence.Item.Exchange) &&
		strings.TrimSpace(evidence.BodyText) != ""
}

func normalizeOnDemandNotice(
	evidence notice.HermesEvidence,
	hash string,
	checkedMS int64,
) notice.Item {
	item := deferredEvidenceItem(evidence, hash, checkedMS)
	item.AnalysisStatus = "ANALYSIS_NOT_REQUIRED"
	item.EscalationReason = "ON_DEMAND_RAG"
	item.TitleActions = notice.InferTitleActions(item.Title, item.Category)
	reference := max(item.PublishedTSMS, item.UpdatedTSMS)
	if reference <= 0 {
		reference = item.DetectedTSMS
	}
	item.Schedules = notice.ExtractSchedules(
		strings.TrimSpace(item.Title+"\n"+item.OriginalBodyText), item.Category, reference,
	)
	item.Summary = notice.BuildOfficialSummary(item, 5)
	return item
}

func refreshDomesticOnDemandProjection(
	current, discovered notice.Item,
	checkedMS int64,
) (notice.Item, bool) {
	if !domesticNoticeUsesOnDemandAnalysis(discovered.Exchange) ||
		!strings.EqualFold(strings.TrimSpace(current.ContentSource), "HERMES_RAW_DETAIL") ||
		!strings.EqualFold(strings.TrimSpace(current.AnalysisStatus), "ANALYSIS_NOT_REQUIRED") ||
		strings.TrimSpace(current.OriginalBodyText) == "" ||
		len(current.ContentHash) != sha256.Size*2 {
		return notice.Item{}, false
	}
	originalTitle := strings.TrimSpace(current.OriginalTitle)
	if originalTitle == "" {
		originalTitle = strings.TrimSpace(current.Title)
	}
	if !strings.EqualFold(strings.TrimSpace(current.Exchange), strings.TrimSpace(discovered.Exchange)) ||
		strings.TrimSpace(current.NoticeID) != strings.TrimSpace(discovered.NoticeID) ||
		strings.TrimSpace(current.URL) != strings.TrimSpace(discovered.URL) ||
		current.PublishedTSMS != discovered.PublishedTSMS || originalTitle != strings.TrimSpace(discovered.Title) ||
		discovered.UpdatedTSMS > current.UpdatedTSMS {
		return notice.Item{}, false
	}
	needsSummary := strings.TrimSpace(current.Summary) == ""
	hasDecorative := hasKnownDecorativeNoticeImageURL(current.ImageURLs)
	if !needsSummary && !hasDecorative {
		return notice.Item{}, false
	}
	keptURLs := make([]string, 0, len(current.ImageURLs))
	for _, imageURL := range current.ImageURLs {
		if _, decorative := knownDecorativeNoticeImageURLs[strings.TrimSpace(imageURL)]; !decorative {
			keptURLs = append(keptURLs, imageURL)
		}
	}
	// When a notice mixes site chrome with a real image, refetch the bytes on
	// the next pass so the retained content hash remains byte-bound. A notice
	// containing only reviewed chrome can be safely normalized from durable
	// official text without another network or model request.
	mixedDecorativeEvidence := hasDecorative && len(keptURLs) > 0
	if mixedDecorativeEvidence && !needsSummary {
		return notice.Item{}, false
	}
	if mixedDecorativeEvidence {
		hasDecorative = false
		keptURLs = append([]string(nil), current.ImageURLs...)
	}
	result := current
	result.Summary = notice.BuildOfficialSummary(current, 5)
	result.DetailCheckedTSMS = checkedMS
	result.AnalysisPolicy = analysisPolicyVersion
	if hasDecorative {
		result.ImageURLs, result.ImageText, result.ImageAnalysis = []string{}, "", []map[string]any{}
		result.ContentHash = sourceContentHash(originalTitle, current.OriginalBodyText, nil)
	}
	return result, true
}

func promoteDomesticRawDetailForOnDemand(
	current, discovered notice.Item,
	checkedMS int64,
) (notice.Item, bool) {
	if !domesticNoticeUsesOnDemandAnalysis(discovered.Exchange) ||
		strings.TrimSpace(current.ContentSource) != "HERMES_RAW_DETAIL" ||
		strings.TrimSpace(current.OriginalBodyText) == "" ||
		len(current.ContentHash) != sha256.Size*2 ||
		strings.EqualFold(strings.TrimSpace(current.AnalysisStatus), "ANALYZED") ||
		strings.EqualFold(strings.TrimSpace(current.AnalysisStatus), "ANALYSIS_NOT_REQUIRED") {
		return notice.Item{}, false
	}
	originalTitle := strings.TrimSpace(current.OriginalTitle)
	if originalTitle == "" {
		originalTitle = strings.TrimSpace(current.Title)
	}
	if !strings.EqualFold(strings.TrimSpace(current.Exchange), strings.TrimSpace(discovered.Exchange)) ||
		strings.TrimSpace(current.NoticeID) != strings.TrimSpace(discovered.NoticeID) ||
		strings.TrimSpace(current.URL) != strings.TrimSpace(discovered.URL) ||
		current.PublishedTSMS != discovered.PublishedTSMS ||
		originalTitle != strings.TrimSpace(discovered.Title) ||
		discovered.UpdatedTSMS > current.UpdatedTSMS {
		return notice.Item{}, false
	}
	evidence := notice.HermesEvidence{
		Item: current, Title: originalTitle, BodyText: current.OriginalBodyText,
		ImageURLs: append([]string(nil), current.ImageURLs...),
	}
	return normalizeOnDemandNotice(evidence, current.ContentHash, checkedMS), true
}

func pendingEvidenceItem(evidence notice.HermesEvidence, hash string, checkedMS int64) notice.Item {
	item := deferredEvidenceItem(evidence, hash, checkedMS)
	item.AnalysisStatus = "ANALYSIS_DEFERRED_QUOTA"
	return item
}

func deferredEmptyEvidenceItem(item notice.Item, checkedMS int64) notice.Item {
	result := deferredItem(item, sourceContentHash(item.Title, "", nil), checkedMS)
	result.Category = deferredEvidenceCategory(result.Title, item.Category)
	result.ContentSource = "HERMES_RAW_DETAIL_EMPTY"
	result.AnalysisStatus = "ANALYSIS_FAILED"
	return result
}

func deferredEvidenceCategory(title, supplied string) string {
	text := strings.ToLower(title)
	if strings.Contains(text, "에어드랍") || strings.Contains(text, "airdrop") {
		return "AIRDROP"
	}
	for _, keyword := range []string{"프로모션", "챌린지", "페스타", "혜택", "상금", "promotion", "campaign"} {
		if strings.Contains(text, keyword) {
			return "EVENT"
		}
	}
	return notice.Classify(title, supplied)
}

func writeJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func writeText(path, value string) error {
	return os.WriteFile(path, []byte(truncate(value, 2_000)), 0o600)
}
