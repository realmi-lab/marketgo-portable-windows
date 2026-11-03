package hermesrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/notice"
)

const appleVisionOCRTimeout = 90 * time.Second

type appleVisionInput struct {
	Images               []appleVisionInputImage `json:"images"`
	RecognitionLanguages []string                `json:"recognition_languages"`
	CustomWords          []string                `json:"custom_words"`
}

type appleVisionInputImage struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type appleVisionOutput struct {
	Engine  string                    `json:"engine"`
	Results []appleVisionOutputResult `json:"results"`
}

type appleVisionOutputResult struct {
	ID         string                    `json:"id"`
	Status     string                    `json:"status"`
	Text       string                    `json:"text"`
	Confidence float64                   `json:"confidence"`
	Regions    []appleVisionOutputRegion `json:"regions"`
	Error      string                    `json:"error"`
}

type appleVisionOutputRegion struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
}

func (p *pipeline) withLocalImageAnalysis(ctx context.Context, evidence materializedEvidence) materializedEvidence {
	if strings.TrimSpace(p.config.VisionOCRBin) == "" || isForeignExchange(evidence.Item.Exchange) || len(evidence.ImageURLs) == 0 {
		return evidence
	}
	rows, err := runAppleVisionOCR(ctx, p.config.VisionOCRBin, evidence)
	if err != nil || localVisionFailed(rows) {
		// The local helper is an optimization. Preserve the existing remote
		// Terra/xhigh vision path when it is missing, interrupted, returns
		// invalid data, or explicitly reports a per-image engine failure.
		return evidence
	}
	evidence.LocalImageAnalysis = rows
	return evidence
}

func localVisionFailed(rows []map[string]any) bool {
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(mapString(row, "status")), "VISION_FAILED") {
			return true
		}
	}
	return false
}

func (p *pipeline) backfillMissingLocalImageAnalysis(
	ctx context.Context,
	current, discovered notice.Item,
) (notice.Item, bool) {
	if strings.TrimSpace(p.config.VisionOCRBin) == "" || !notice.IsDomesticExchange(discovered.Exchange) ||
		!strings.EqualFold(strings.TrimSpace(current.AnalysisStatus), "ANALYSIS_NOT_REQUIRED") ||
		strings.TrimSpace(current.OriginalBodyText) == "" || len(current.ImageURLs) == 0 ||
		len(current.ImageAnalysis) > 0 {
		return notice.Item{}, false
	}
	evidence, err := p.collector.FetchEvidence(ctx, discovered)
	if err != nil {
		return notice.Item{}, false
	}
	materialized, cleanup, err := p.materializeImages(ctx, evidence, "apple-vision-backfill")
	if err != nil {
		return notice.Item{}, false
	}
	defer cleanup()
	if len(materialized.ImageURLs) == 0 {
		return notice.Item{}, false
	}
	materialized = p.withLocalImageAnalysis(ctx, materialized)
	if !hasLocalImageAnalysis(materialized) {
		return notice.Item{}, false
	}
	hash := materializedSourceContentHash(materialized)
	item := normalizeOnDemandNotice(materialized.HermesEvidence, hash, time.Now().UTC().UnixMilli())
	return attachLocalImageAnalysis(item, materialized), true
}

func reuseStoredLocalImageAnalysis(current notice.Item, evidence materializedEvidence) materializedEvidence {
	if len(current.ImageAnalysis) != len(evidence.ImageURLs) || len(evidence.ImageURLs) == 0 {
		return evidence
	}
	localByURL := make(map[string]localImage, len(evidence.LocalImages))
	for _, image := range evidence.LocalImages {
		localByURL[image.URL] = image
	}
	for index, imageURL := range evidence.ImageURLs {
		row := current.ImageAnalysis[index]
		local := localByURL[imageURL]
		if mapString(row, "url") != imageURL || mapString(row, "ocr_engine") != "apple_vision" ||
			local.SHA256 == "" || !strings.EqualFold(mapString(row, "content_sha256"), local.SHA256) {
			return evidence
		}
	}
	evidence.LocalImageAnalysis = append([]map[string]any(nil), current.ImageAnalysis...)
	return evidence
}

func runAppleVisionOCR(
	ctx context.Context,
	executable string,
	evidence materializedEvidence,
) ([]map[string]any, error) {
	input := appleVisionInput{
		Images: []appleVisionInputImage{}, RecognitionLanguages: []string{"ko-KR", "en-US"},
		CustomWords: localOCRCustomWords(evidence),
	}
	for _, image := range evidence.LocalImages {
		if image.LocalPath != "" {
			input.Images = append(input.Images, appleVisionInputImage{ID: image.URL, Path: image.LocalPath})
		}
	}
	if len(input.Images) == 0 {
		return unavailableLocalImageRows(evidence), nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, appleVisionOCRTimeout)
	defer cancel()
	command := exec.CommandContext(callCtx, strings.TrimSpace(executable))
	command.Stdin = bytes.NewReader(payload)
	stdout := newBoundedOutputBuffer(512 << 10)
	stderr := newBoundedOutputBuffer(8 << 10)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if callCtx.Err() != nil {
			return nil, callCtx.Err()
		}
		return nil, fmt.Errorf("Apple Vision OCR failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Exceeded() {
		return nil, errors.New("Apple Vision OCR response exceeded 512 KiB")
	}
	var output appleVisionOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return nil, fmt.Errorf("decode Apple Vision OCR response: %w", err)
	}
	if output.Engine != "apple_vision" {
		return nil, fmt.Errorf("unexpected Apple Vision OCR engine %q", output.Engine)
	}
	return localImageRowsFromVision(evidence, output.Results)
}

func localOCRCustomWords(evidence materializedEvidence) []string {
	values := append([]string(nil), evidence.Item.Assets...)
	values = append(values, notice.TitleAssets(evidence.Title)...)
	values = append(values, "BTC", "ETH", "KRW", "USDT", "USDC")
	result := normalizedStrings(values)
	sort.Strings(result)
	return result
}

func localImageRowsFromVision(
	evidence materializedEvidence,
	results []appleVisionOutputResult,
) ([]map[string]any, error) {
	byURL := make(map[string]appleVisionOutputResult, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.ID) == "" || byURL[result.ID].ID != "" {
			return nil, errors.New("Apple Vision OCR returned an empty or duplicate image id")
		}
		byURL[result.ID] = result
	}
	localByURL := make(map[string]localImage, len(evidence.LocalImages))
	for _, image := range evidence.LocalImages {
		localByURL[image.URL] = image
	}
	rows := make([]map[string]any, 0, len(evidence.ImageURLs))
	for _, imageURL := range evidence.ImageURLs {
		local := localByURL[imageURL]
		if local.LocalPath == "" {
			status := "DOWNLOAD_FAILED"
			if strings.Contains(strings.ToLower(local.Error), "unsupported image signature") {
				status = "UNSUPPORTED"
			}
			row := localImageFailureRow(imageURL, status, local.Bytes, local.Error)
			row["content_sha256"] = local.SHA256
			rows = append(rows, row)
			continue
		}
		result, found := byURL[imageURL]
		if !found {
			return nil, fmt.Errorf("Apple Vision OCR omitted downloaded image %s", imageURL)
		}
		status := strings.ToUpper(strings.TrimSpace(result.Status))
		text := truncateRunes(strings.TrimSpace(result.Text), 20_000)
		switch status {
		case "ANALYZED":
			if text == "" {
				return nil, fmt.Errorf("Apple Vision OCR analyzed image without text: %s", imageURL)
			}
			profile := notice.NoticeImageSemanticProfile(text, evidence.Item.Assets)
			rows = append(rows, map[string]any{
				"url": imageURL, "status": status, "text": text, "translated_text_ko": "",
				"semantic_type": profile.SemanticType, "signals": profile.Signals,
				"interpretation": profile.Interpretation, "bytes": local.Bytes,
				"ocr_engine": "apple_vision", "ocr_confidence": clampConfidence(result.Confidence),
				"text_regions": normalizedVisionRegions(result.Regions), "failure_reason": "",
				"content_sha256": local.SHA256,
			})
		case "NO_TEXT", "VISION_FAILED":
			row := localImageFailureRow(imageURL, status, local.Bytes, result.Error)
			row["ocr_engine"] = "apple_vision"
			row["ocr_confidence"] = float64(0)
			row["text_regions"] = []map[string]any{}
			row["content_sha256"] = local.SHA256
			rows = append(rows, row)
		default:
			return nil, fmt.Errorf("Apple Vision OCR returned unsupported status %q", status)
		}
	}
	if len(byURL) != len(inputLocalImageURLs(evidence)) {
		return nil, errors.New("Apple Vision OCR returned an unexpected image result")
	}
	return rows, nil
}

func unavailableLocalImageRows(evidence materializedEvidence) []map[string]any {
	rows := make([]map[string]any, 0, len(evidence.ImageURLs))
	localByURL := make(map[string]localImage, len(evidence.LocalImages))
	for _, image := range evidence.LocalImages {
		localByURL[image.URL] = image
	}
	for _, imageURL := range evidence.ImageURLs {
		local := localByURL[imageURL]
		status := "DOWNLOAD_FAILED"
		if strings.Contains(strings.ToLower(local.Error), "unsupported image signature") {
			status = "UNSUPPORTED"
		}
		rows = append(rows, localImageFailureRow(imageURL, status, local.Bytes, local.Error))
	}
	return rows
}

func localImageFailureRow(url, status string, bytes int64, reason string) map[string]any {
	return map[string]any{
		"url": url, "status": status, "text": "", "translated_text_ko": "",
		"semantic_type": "UNKNOWN", "signals": []string{}, "interpretation": "", "bytes": bytes,
		"failure_reason": truncateRunes(strings.TrimSpace(reason), 300),
	}
}

func normalizedVisionRegions(regions []appleVisionOutputRegion) []map[string]any {
	result := make([]map[string]any, 0, min(len(regions), 256))
	for _, region := range regions[:min(len(regions), 256)] {
		if strings.TrimSpace(region.Text) == "" || region.X < 0 || region.Y < 0 ||
			region.Width < 0 || region.Height < 0 || region.X+region.Width > 1.0001 || region.Y+region.Height > 1.0001 {
			continue
		}
		result = append(result, map[string]any{
			"text":       truncateRunes(strings.TrimSpace(region.Text), 2_000),
			"confidence": clampConfidence(region.Confidence),
			"x":          region.X, "y": region.Y, "width": region.Width, "height": region.Height,
		})
	}
	return result
}

func inputLocalImageURLs(evidence materializedEvidence) []string {
	result := []string{}
	for _, image := range evidence.LocalImages {
		if image.LocalPath != "" {
			result = append(result, image.URL)
		}
	}
	return result
}

func hasLocalImageAnalysis(evidence materializedEvidence) bool {
	return len(evidence.ImageURLs) > 0 && len(evidence.LocalImageAnalysis) == len(evidence.ImageURLs)
}

func attachLocalImageAnalysis(item notice.Item, evidence materializedEvidence) notice.Item {
	if !hasLocalImageAnalysis(evidence) {
		return item
	}
	item.ImageURLs = append([]string(nil), evidence.ImageURLs...)
	item.ImageAnalysis = normalizeImageAnalysis(evidence.LocalImageAnalysis, evidence)
	item.ImageText = expectedAggregatedImageText(item.ImageAnalysis, evidence)
	return item
}
