package hermesrunner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"market-share-go/internal/notice"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// TargetedReanalysis is an explicit one-shot retry of a validation-rejected
// source after a deliberate correction. It never changes normal retry policy.
type TargetedReanalysis struct {
	Exchange             string `json:"exchange"`
	NoticeID             string `json:"notice_id"`
	ExpectedSourceSHA256 string `json:"expected_source_sha256"`
}

var targetedNoticeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func ParseTargetedReanalysis(exchange, id, hash string) (*TargetedReanalysis, error) {
	if exchange == "" && id == "" && hash == "" {
		return nil, nil
	}
	digest, err := hex.DecodeString(hash)
	if !notice.IsDomesticExchange(exchange) || exchange != strings.TrimSpace(exchange) || exchange != strings.ToLower(exchange) || !targetedNoticeID.MatchString(id) || err != nil || len(digest) != 32 || hash != strings.ToLower(hash) {
		return nil, errors.New("targeted reanalysis requires one exact domestic exchange, notice ID and lowercase source SHA256")
	}
	return &TargetedReanalysis{exchange, id, hash}, nil
}
func validateTargetedReanalysisConfig(c Config) error {
	if c.TargetedReanalysis == nil {
		return nil
	}
	target := c.TargetedReanalysis
	if parsed, err := ParseTargetedReanalysis(target.Exchange, target.NoticeID, target.ExpectedSourceSHA256); err != nil || parsed == nil {
		if err == nil {
			err = errors.New("empty targeted reanalysis identity")
		}
		return err
	}
	if c.CorpusRange == nil || c.CorpusRange.FromTSMS != c.CorpusRange.ToTSMS || c.CorpusExchange != target.Exchange || c.CollectorOnly || c.DisableModelAnalysis || c.MaxAnalysesPerExchangePerRun != 1 {
		return errors.New("targeted reanalysis requires a single publication instant, matching corpus exchange and exactly one model analysis")
	}
	return nil
}
func selectTargetedReanalysis(c Config, snapshot notice.Snapshot) (notice.Item, string, error) {
	if err := validateTargetedReanalysisConfig(c); err != nil {
		return notice.Item{}, "", err
	}
	target := c.TargetedReanalysis
	if target == nil {
		return notice.Item{}, "", errors.New("targeted reanalysis not enabled")
	}
	var selected notice.Item
	found := false
	for _, item := range snapshot.Items {
		if item.Exchange == target.Exchange && item.NoticeID == target.NoticeID {
			if found {
				return notice.Item{}, "", errors.New("duplicate targeted source")
			}
			selected = item
			found = true
		}
	}
	token := snapshot.ItemRevisions[target.Exchange+":"+target.NoticeID]
	version, tokenErr := strconv.ParseUint(token, 10, 32)
	if !found || tokenErr != nil || version == 0 || strconv.FormatUint(version, 10) != token || selected.PublishedTSMS != c.CorpusRange.FromTSMS || selected.ContentHash != target.ExpectedSourceSHA256 || selected.AnalysisStatus != "ANALYSIS_FAILED" || selected.EscalationReason != "ANALYSIS_VALIDATION_REJECTED" || selected.ContentSource != "HERMES_RAW_DETAIL" {
		return notice.Item{}, "", errors.New("target must be the unchanged existing validation-rejected raw-detail row with a current revision")
	}
	return selected, token, nil
}
func verifyTargetedMaterialized(current notice.Item, e materializedEvidence) error {
	if materializedSourceContentHash(e) != current.ContentHash || e.BodyText != current.OriginalBodyText || e.Title != current.OriginalTitle || e.Item.PublishedTSMS != current.PublishedTSMS || e.Item.UpdatedTSMS != current.UpdatedTSMS || !slices.Equal(e.ImageURLs, current.ImageURLs) {
		return errors.New("targeted official source changed; no reanalysis")
	}
	if len(e.ImageURLs) > 0 {
		if len(e.LocalImageAnalysis) != len(e.ImageURLs) {
			return errors.New("targeted reanalysis requires verified OCR for every image")
		}
		if _, err := verifiedWorkerImagePassages(e); err != nil {
			return err
		}
	}
	return nil
}

type targetedReanalysisReceipt struct {
	Mode             string              `json:"mode"`
	Target           *TargetedReanalysis `json:"target"`
	RunID            string              `json:"run_id"`
	StartedAt        time.Time           `json:"started_at"`
	FinishedAt       time.Time           `json:"finished_at"`
	InitialRevision  string              `json:"initial_revision"`
	ModelRevisions   map[string]string   `json:"model_revisions,omitempty"`
	AnalysisAttempts int                 `json:"analysis_attempts"`
	State            string              `json:"state"`
}

func (p *pipeline) runTargetedReanalysis(ctx context.Context, snapshot notice.Snapshot, analyze func(context.Context, materializedEvidence, string) (analysisDecision, error)) (runErr error) {
	current, revision, err := selectTargetedReanalysis(p.config, snapshot)
	if err != nil {
		return err
	}
	target := p.config.TargetedReanalysis
	record := targetedReanalysisReceipt{Mode: "EXPLICIT_TARGETED_VALIDATION_REANALYSIS", Target: target, RunID: p.runID, StartedAt: time.Now().UTC(), InitialRevision: revision, State: "SOURCE_CHECK"}
	receiptPath := filepath.Join(p.outputDir, "targeted-reanalysis.json")
	if err = writeJSON(receiptPath, record); err != nil {
		return err
	}
	defer func() {
		record.FinishedAt = time.Now().UTC()
		if err := writeJSON(receiptPath, record); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("targeted receipt write failed; reconcile API before retry: %w", err))
		}
	}()
	if err = writeJSON(filepath.Join(p.outputDir, "targeted-before.json"), current); err != nil {
		return err
	}
	// Listing discovery verifies the source metadata but does not checkpoint any
	// row or heartbeat. A same-timestamp peer is never selected or written.
	var listed notice.Item
	matches := 0
	err = p.collector.DiscoverCorpusExchange(ctx, target.Exchange, *p.config.CorpusRange, func(_ context.Context, _ string, _ int, items []notice.Item) error {
		for _, item := range items {
			if item.Exchange == target.Exchange && item.NoticeID == target.NoticeID {
				if matches == 0 {
					listed = item
					matches++
				} else if !reflect.DeepEqual(listed, item) {
					return errors.New("targeted listing changed between pages")
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if matches != 1 || !existingOfficialMetadataMatches(current, listed) || listed.UpdatedTSMS > current.UpdatedTSMS {
		return errors.New("targeted official listing differs or is absent")
	}
	// Clear only the transient request excerpt to force an official detail fetch;
	// preserve stored timestamps and all raw data. Never submit this request item.
	detailItem := current
	detailItem.Title = current.OriginalTitle
	detailItem.BodyExcerpt = ""
	evidence, err := p.collector.FetchEvidence(ctx, detailItem)
	if err != nil {
		return err
	}
	// Keep the bounded fresh image bytes in this private one-shot output for
	// source/OCR/response provenance; normal scheduled runs still clean caches.
	materialized, _, err := p.materializeImages(ctx, evidence, "targeted")
	if err != nil {
		return err
	}
	if materializedSourceContentHash(materialized) != target.ExpectedSourceSHA256 {
		return errors.New("targeted source hash changed during fresh fetch")
	}
	materialized = reuseStoredLocalImageAnalysis(current, materialized)
	materialized = p.withLocalImageAnalysis(ctx, materialized)
	if err = verifyTargetedMaterialized(current, materialized); err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(p.outputDir, "targeted-evidence.json"), materialized); err != nil {
		return err
	}
	// Journal the unmodified normal API request and its actual receipt, including
	// ambiguous errors. Never resubmit on a missing response/receipt.
	sequence := 0
	submit := func(request notice.HermesIngestRequest) error {
		request.CorpusRange = p.config.CorpusRange
		request.WindowStartMS = p.config.CorpusRange.FromTSMS
		sequence++
		stem := filepath.Join(p.outputDir, fmt.Sprintf("targeted-submit-%02d", sequence))
		if err := writeJSON(stem+"-request.json", request); err != nil {
			return err
		}
		response, err := p.config.Submit(ctx, request)
		if saveErr := writeJSON(stem+"-receipt.json", map[string]any{"response": response, "transport_error": err != nil}); saveErr != nil {
			return fmt.Errorf("Submit finished but receipt write failed; reconcile API before retry: %w", saveErr)
		}
		if err != nil {
			return err
		}
		if response.Accepted != 1 {
			return errors.New("targeted CAS response did not accept exactly one item")
		}
		return nil
	}
	pending := attachLocalImageAnalysis(pendingEvidenceItem(materialized.HermesEvidence, current.ContentHash, time.Now().UnixMilli()), materialized)
	record.State = "PENDING_CAS"
	if err = submit(notice.HermesIngestRequest{BatchID: p.runID + "-targeted-pending", Items: []notice.Item{pending}, ExpectedRevisions: map[string]string{target.Exchange + ":" + target.NoticeID: revision}}); err != nil {
		return err
	}
	expected, err := p.corpusAnalysisPrecondition(ctx, pending)
	if err != nil {
		return err
	}
	record.ModelRevisions = expected
	record.State = "MODEL_STARTED"
	record.AnalysisAttempts = 1
	if err = writeJSON(receiptPath, record); err != nil {
		return err
	}
	// Leave up to five seconds for failure/success CAS inside the same whole-path
	// deadline. The domestic model path has no continuation or provider retry.
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("targeted reanalysis requires a whole-path deadline")
	}
	modelCtx, cancel := context.WithDeadline(ctx, deadline.Add(-5*time.Second))
	decision, analysisErr := analyze(modelCtx, materialized, "targeted")
	cancel()
	var final notice.Item
	if analysisErr != nil {
		final, _ = deferredEvidenceAfterAnalysisFailure(materialized.HermesEvidence, current.ContentHash, time.Now().UnixMilli(), analysisErr)
		final = attachLocalImageAnalysis(final, materialized)
	} else {
		final = normalizeAnalyzedItem(materialized, decision, current.ContentHash, time.Now().UnixMilli())
	}
	record.State = "FINAL_CAS"
	if err = submit(notice.HermesIngestRequest{BatchID: p.runID + "-targeted-final", Items: []notice.Item{final}, ExpectedRevisions: expected}); err != nil {
		return errors.Join(analysisErr, err)
	}
	record.State = "ANALYSIS_FAILED"
	if analysisErr == nil {
		record.State = "ANALYZED"
	}
	return analysisErr
}
