package hermesrunner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"market-share-go/internal/notice"
	"market-share-go/services/collector/restbudget"
)

type PipelineWorkerConfig struct {
	Mode     string
	WorkerID string
	SpoolDir string
	Runner   Config
	Backend  BackendClient
	// Invoke is a test seam; production supplies the isolated model command.
	Invoke func(context.Context, notice.PipelineResponse) (notice.PipelineDerivation, []byte, error)
}

type pipelineCapture struct {
	upstream notice.HermesEvidenceFetcher
	spool    *noticeSpool
	job      notice.PipelineJob
	send     func(context.Context, notice.PipelineRequest) (notice.PipelineResponse, error)
	objects  []string
	byURL    map[string]string
}

func (c *pipelineCapture) GetBytes(ctx context.Context, req restbudget.Request, limit int64) ([]byte, error) {
	if err := c.spool.capacity(2 * notice.PipelineMaxObjectBytes); err != nil {
		return nil, err
	}
	raw, err := c.upstream.GetBytes(ctx, req, limit)
	if err != nil {
		return nil, err
	}
	id, err := c.spool.archive(ctx, c.job, raw, req.Accept, c.send)
	if err != nil {
		return nil, err
	}
	c.objects = append(c.objects, id)
	c.byURL[req.URL] = id
	return raw, nil
}

func RunPipelineWorker(ctx context.Context, cfg PipelineWorkerConfig, logger *slog.Logger) error {
	if cfg.Runner.TargetedReanalysis != nil {
		return errors.New("targeted reanalysis is not a V2 daemon operation")
	}
	if cfg.Mode != "v2-collector" && cfg.Mode != "v2-analyzer" {
		return errors.New("invalid V2 worker mode")
	}
	if logger == nil {
		logger = slog.Default()
	}
	spool, err := newNoticeSpool(cfg.SpoolDir, 256<<20)
	if err != nil {
		return err
	}
	unlock, err := lockNoticeSpool(cfg.SpoolDir)
	if err != nil {
		return err
	}
	defer unlock()
	if cfg.WorkerID == "" {
		cfg.WorkerID = notice.PipelineID()
	}
	capabilities, err := cfg.Backend.Pipeline(ctx, notice.PipelineRequest{Operation: "capabilities"})
	if err != nil {
		return err
	}
	if capabilities.SchemaVersion != notice.PipelineSchemaVersion || capabilities.Policy != notice.PipelinePolicy {
		return errors.New("notice API and worker policy mismatch")
	}
	stages := []string{"LIST_PAGE", "BODY"}
	if cfg.Mode == "v2-analyzer" {
		stages = []string{"OCR", "SEMANTIC_AUDIT", "SEMANTIC_GENERATE"}
	}
	for ctx.Err() == nil {
		if err = spool.flush(ctx, cfg.Backend.Pipeline); err != nil {
			logger.Error("notice spool recovery pending", "error", err)
			if !pipelineWait(ctx, time.Second*10) {
				break
			}
			continue
		}
		worked := false
		for _, stage := range stages {
			claim, claimErr := cfg.Backend.Pipeline(ctx, notice.PipelineRequest{Operation: "claim", WorkerID: cfg.WorkerID, Stage: stage})
			if claimErr != nil {
				logger.Error("notice job claim failed", "error", claimErr)
				break
			}
			if claim.Job == nil {
				continue
			}
			worked = true
			if err = runPipelineJob(ctx, cfg, spool, claim); err != nil {
				logger.Error("notice job failed", "stage", stage, "job_id", claim.Job.ID, "error", err)
			}
		}
		if !worked && !pipelineWait(ctx, 5*time.Second) {
			break
		}
	}
	return ctx.Err()
}

func pipelineWait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func runPipelineJob(ctx context.Context, cfg PipelineWorkerConfig, spool *noticeSpool, claim notice.PipelineResponse) error {
	job := *claim.Job
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				_, err := cfg.Backend.Pipeline(runCtx, notice.PipelineRequest{Operation: "heartbeat", Exchange: job.Exchange, JobID: job.ID, LeaseToken: job.LeaseToken})
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	req := notice.PipelineRequest{Operation: "complete", RequestID: notice.PipelineID(), Exchange: job.Exchange, JobID: job.ID, LeaseToken: job.LeaseToken}
	var err error
	if job.Stage == "LIST_PAGE" || job.Stage == "BODY" {
		capture := &pipelineCapture{upstream: cfg.Runner.Fetcher, spool: spool, job: job, send: cfg.Backend.Pipeline, byURL: map[string]string{}}
		collector, makeErr := notice.NewHermesSourceCollector(capture)
		if makeErr != nil {
			return makeErr
		}
		if job.Stage == "LIST_PAGE" {
			err = collector.DiscoverExchange(runCtx, job.Exchange, func(pageCtx context.Context, exchange string, page int, items []notice.Item) error {
				pageReq := req
				pageReq.Operation = "page"
				pageReq.RequestID = notice.PipelineID()
				pageReq.Page = &notice.PipelinePage{Ordinal: page, Items: items, ObjectIDs: append([]string(nil), capture.objects...)}
				_, err := cfg.Backend.Pipeline(pageCtx, pageReq)
				capture.objects = nil
				return err
			})
			req.Page = &notice.PipelinePage{Complete: true}
		} else {
			if job.Item == nil {
				return notice.ErrPipelineInvalid
			}
			item := *job.Item
			item.BodyExcerpt = "" // Always archive an actual official detail response.
			var evidence notice.HermesEvidence
			evidence, err = collector.FetchEvidence(runCtx, item)
			if err == nil {
				markupID := ""
				if evidence.BodyMarkup != "" {
					var archiveErr error
					markupID, archiveErr = spool.archive(runCtx, job, []byte(evidence.BodyMarkup), "text/html", cfg.Backend.Pipeline)
					if archiveErr != nil {
						return archiveErr
					}
					capture.objects = append(capture.objects, markupID)
				}
				config := cfg.Runner
				config.Fetcher = capture
				p := newPipeline(config, collector)
				if err = os.MkdirAll(p.outputDir, 0700); err == nil {
					var materialized materializedEvidence
					var cleanup func()
					materialized, cleanup, err = p.materializeImages(runCtx, evidence, "v2")
					if cleanup != nil {
						defer cleanup()
					}
					if err == nil {
						rev := notice.PipelineRevision{Item: evidence.Item, BodyMarkupObjectID: markupID, ObjectIDs: capture.objects, Media: []notice.PipelineMedia{}}
						rev.Item.OriginalTitle = evidence.Title
						rev.Item.OriginalBodyText = evidence.BodyText
						rev.Item.ImageURLs = evidence.ImageURLs
						imageRows := map[string]localImage{}
						for _, image := range materialized.LocalImages {
							imageRows[image.URL] = image
						}
						for _, imageURL := range evidence.ImageURLs {
							image, present := imageRows[imageURL]
							status := "COMPLETE"
							if image.Error != "" {
								status = "FAILED"
							}
							if !present {
								status = "POLICY_UNSUPPORTED"
								image.Error = "DECORATIVE_IMAGE"
							}
							rev.Media = append(rev.Media, notice.PipelineMedia{URL: imageURL, ObjectID: capture.byURL[imageURL], Status: status, ErrorCode: image.Error})
						}
						err = notice.ValidatePipelineRevision(&rev)
						req.Revision = &rev
					}
				}
			}
		}
	} else if job.Stage == "OCR" {
		var d notice.PipelineOCR
		var raw []byte
		d, raw, err = runPipelineOCR(runCtx, cfg, claim)
		if len(raw) > 0 {
			id, saveErr := spool.archive(runCtx, job, raw, "application/json", cfg.Backend.Pipeline)
			if saveErr != nil {
				err = saveErr
			} else {
				d.RawObjectIDs = []string{id}
			}
		}
		req.OCR = &d
	} else {
		invoke := cfg.Invoke
		if invoke == nil {
			invoke = func(callCtx context.Context, input notice.PipelineResponse) (notice.PipelineDerivation, []byte, error) {
				return invokePipelineModel(callCtx, cfg.Runner, input)
			}
		}
		var d notice.PipelineDerivation
		var raw []byte
		d, raw, err = invoke(runCtx, claim)
		if len(raw) > 0 {
			id, saveErr := spool.archive(runCtx, job, raw, "application/json", cfg.Backend.Pipeline)
			if saveErr != nil {
				err = saveErr
			} else {
				d.RawObjectIDs = []string{id}
			}
		}
		req.Derivation = &d
	}
	if err != nil {
		failed := req
		failed.Operation = "fail"
		failed.Page = nil
		failed.Revision = nil
		failed.Derivation = nil
		failed.OCR = nil
		failed.ErrorCode = "WORKER_STAGE_FAILED"
		if errors.Is(err, notice.ErrPipelineInputBudget) {
			failed.ErrorCode = "FULL_REVIEW_INPUT_LIMIT"
		}
		_, _ = cfg.Backend.Pipeline(ctx, failed)
		return err
	}
	_, err = cfg.Backend.Pipeline(runCtx, req)
	return err
}
