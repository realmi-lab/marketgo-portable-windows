package hermesrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"market-share-go/internal/notice"
)

func runPipelineOCR(ctx context.Context, cfg PipelineWorkerConfig, claim notice.PipelineResponse) (notice.PipelineOCR, []byte, error) {
	d := notice.PipelineOCR{RevisionID: claim.Job.RevisionID, Engine: "apple_vision", Status: "UNAVAILABLE", Rows: []map[string]any{}}
	if cfg.Runner.VisionOCRBin == "" {
		raw, err := json.Marshal(d)
		return d, raw, err
	}
	if err := os.MkdirAll(cfg.Runner.OutputDir, 0700); err != nil {
		return d, nil, err
	}
	dir, err := os.MkdirTemp(cfg.Runner.OutputDir, "v2-ocr-")
	if err != nil {
		return d, nil, err
	}
	defer os.RemoveAll(dir)
	evidence := materializedEvidence{HermesEvidence: notice.HermesEvidence{Item: claim.Revision.Item, Title: claim.Revision.Item.Title}}
	for i, m := range claim.Revision.Media {
		if i >= 16 || m.Status != "COMPLETE" || m.ObjectID == "" {
			continue
		}
		out, fetchErr := cfg.Backend.Pipeline(ctx, notice.PipelineRequest{Operation: "object_get", Exchange: claim.Job.Exchange, JobID: claim.Job.ID, LeaseToken: claim.Job.LeaseToken, ObjectID: m.ObjectID})
		if fetchErr != nil {
			return d, nil, fetchErr
		}
		if out.Object == nil || notice.PipelineBytesDigest(out.Object.Bytes) != out.Object.SHA256 {
			return d, nil, notice.ErrPipelineConflict
		}
		path := filepath.Join(dir, fmt.Sprintf("image-%d", i))
		if err = os.WriteFile(path, out.Object.Bytes, 0600); err != nil {
			return d, nil, err
		}
		evidence.ImageURLs = append(evidence.ImageURLs, m.URL)
		evidence.LocalImages = append(evidence.LocalImages, localImage{URL: m.URL, LocalPath: path, Bytes: int64(len(out.Object.Bytes)), SHA256: out.Object.SHA256})
	}
	d.Rows, err = runAppleVisionOCR(ctx, cfg.Runner.VisionOCRBin, evidence)
	if err != nil {
		return d, nil, err
	}
	d.Status = "COMPLETE"
	if len(d.Rows) != len(claim.Revision.Media) {
		d.Status = "PARTIAL"
	}
	raw, err := json.Marshal(d)
	return d, raw, err
}
