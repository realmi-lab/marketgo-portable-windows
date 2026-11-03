package notice

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"market-share-go/services/collector/restbudget"
)

const maximumImageBytes = int64(12 << 20)

type ImageAnalyzer interface {
	Backend() string
	Analyze(context.Context, string, []string, []string) (string, []map[string]any)
}

type LocalImageAnalyzer struct {
	fetcher    noticeFetcher
	executable string
	imageSlot  chan struct{}
}

func NewLocalImageAnalyzer(fetcher noticeFetcher) *LocalImageAnalyzer {
	executable, _ := exec.LookPath("tesseract")
	return &LocalImageAnalyzer{fetcher: fetcher, executable: executable, imageSlot: make(chan struct{}, 1)}
}

func (a *LocalImageAnalyzer) Backend() string {
	if a != nil && a.executable != "" {
		return "tesseract"
	}
	return "unavailable"
}

func (a *LocalImageAnalyzer) Analyze(ctx context.Context, exchange string, urls []string, knownAssets []string) (string, []map[string]any) {
	results := make([]map[string]any, 0, len(urls))
	if a.Backend() == "unavailable" {
		for _, imageURL := range urls {
			result := emptyImageResult(imageURL)
			result["status"] = "OCR_UNAVAILABLE"
			results = append(results, result)
		}
		return "", results
	}
	recognized := []string{}
	for _, imageURL := range urls {
		text, result := a.analyzeImage(ctx, exchange, imageURL, knownAssets)
		if text != "" {
			recognized = append(recognized, text)
		}
		results = append(results, result)
	}
	return strings.Join(recognized, "\n"), results
}

func (a *LocalImageAnalyzer) analyzeImage(
	ctx context.Context,
	exchange string,
	imageURL string,
	knownAssets []string,
) (string, map[string]any) {
	result := emptyImageResult(imageURL)
	release, ok := a.acquireImageSlot(ctx)
	if !ok {
		result["status"] = "OCR_ERROR"
		return "", result
	}
	defer release()
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	data, err := a.fetcher.GetBytes(requestCtx, restbudget.Request{
		Exchange: exchange, Group: "public", URL: imageURL, Priority: restbudget.PriorityRegistry,
		Cost: 1, Accept: "image/*", Headers: http.Header{"User-Agent": {"Mozilla/5.0 (compatible; MarketShareNotice/1.0)"}},
	}, maximumImageBytes)
	cancel()
	if err != nil {
		if strings.Contains(err.Error(), "exceeds") {
			result["status"] = "SKIPPED_OVERSIZE"
		}
		return "", result
	}
	result["bytes"] = len(data)
	if int64(len(data)) > maximumImageBytes {
		result["status"] = "SKIPPED_OVERSIZE"
		return "", result
	}
	text, status := a.recognize(ctx, imageURL, data)
	result["status"], result["text"] = status, truncateRunes(text, 20_000)
	if text != "" {
		profile := NoticeImageSemanticProfile(text, knownAssets)
		result["semantic_type"], result["signals"], result["interpretation"] = profile.SemanticType, profile.Signals, profile.Interpretation
		result["dates"], result["assets"], result["values"] = profile.Dates, profile.Assets, profile.Values
	}
	return text, result
}

func (a *LocalImageAnalyzer) acquireImageSlot(ctx context.Context) (func(), bool) {
	if a.imageSlot == nil {
		return func() {}, true
	}
	select {
	case a.imageSlot <- struct{}{}:
		return func() { <-a.imageSlot }, true
	case <-ctx.Done():
		return nil, false
	}
}

func (a *LocalImageAnalyzer) recognize(ctx context.Context, rawURL string, data []byte) (string, string) {
	if a.executable == "" {
		return "", "OCR_UNAVAILABLE"
	}
	suffix := ".img"
	if parsed, err := url.Parse(rawURL); err == nil {
		candidate := filepath.Ext(parsed.Path)
		if len(candidate) > 1 && len(candidate) <= 10 && !strings.ContainsAny(candidate, `/\\`) {
			suffix = candidate
		}
	}
	file, err := os.CreateTemp("", "market-share-notice-*"+suffix)
	if err != nil {
		return "", "OCR_ERROR"
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return "", "OCR_ERROR"
	}
	if err = file.Close(); err != nil {
		return "", "OCR_ERROR"
	}
	commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, a.executable, path, "stdout", "-l", "kor+eng").Output()
	if err != nil || commandCtx.Err() != nil {
		return "", "OCR_ERROR"
	}
	return PlainText(string(output)), "ANALYZED"
}

func emptyImageResult(imageURL string) map[string]any {
	return map[string]any{
		"url": imageURL, "status": "DOWNLOAD_ERROR", "text": "", "bytes": 0,
		"semantic_type": "UNKNOWN", "signals": []string{},
		"interpretation": "OCR 결과가 없어 이미지 의미를 분류하지 못했습니다.",
	}
}

func imageAnalysisStatus(item Item) string {
	if len(item.ImageURLs) == 0 || item.ImageText != "" {
		return item.AnalysisStatus
	}
	statuses := make(map[string]bool)
	for _, row := range item.ImageAnalysis {
		statuses[strings.ToUpper(strings.TrimSpace(textValue(row["status"])))] = true
	}
	if statuses["OCR_UNAVAILABLE"] {
		return "TEXT_ANALYZED_IMAGE_UNAVAILABLE"
	}
	if len(item.ImageAnalysis) > 0 {
		return "TEXT_ANALYZED_IMAGE_ERROR"
	}
	return item.AnalysisStatus
}
