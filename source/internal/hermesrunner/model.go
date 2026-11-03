package hermesrunner

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
	textunicode "golang.org/x/text/encoding/unicode"
	"market-share-go/internal/notice"
)

var paidAPIEnvironment = map[string]struct{}{
	"OPENAI_API_KEY": {}, "OPENROUTER_API_KEY": {}, "ANTHROPIC_API_KEY": {},
	"FIREWORKS_API_KEY": {}, "GOOGLE_API_KEY": {}, "GEMINI_API_KEY": {},
	"OPENCODE_ZEN_API_KEY": {}, "OPENAI_BASE_URL": {},
}

var hermesUTF8Environment = map[string]string{
	"PYTHONUTF8":       "1",
	"PYTHONIOENCODING": "utf-8",
	"LANG":             "C.UTF-8",
	"LC_ALL":           "C.UTF-8",
}

const (
	noticeAnalysisAttemptTimeout = 5 * time.Minute
	noticeAnalysisModel          = MarketQuestionModel
	noticeAnalysisEffort         = "low"
	noticeVisionAnalysisModel    = MarketQuestionModel
	noticeVisionAnalysisEffort   = noticeAnalysisEffort
)

//go:embed prompts/notice-content-analysis.md
var noticeContentAnalysisPromptTemplate string

//go:embed prompts/notice-image-analysis.md
var noticeImageAnalysisPromptTemplate string

type localImage struct {
	URL       string `json:"url"`
	LocalPath string `json:"local_path,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Error     string `json:"error,omitempty"`
}

type materializedEvidence struct {
	notice.HermesEvidence
	LocalImages        []localImage
	LocalImageAnalysis []map[string]any
}

type analysisResult struct {
	scheduleNormalization string            // Go-owned audit marker; never accepted from model JSON.
	OriginalLanguage      string            `json:"original_language"`
	TranslatedTitle       string            `json:"translated_title_ko"`
	TranslatedBody        string            `json:"translated_body_text_ko"`
	TranslationState      string            `json:"translation_status"`
	Summary               string            `json:"summary"`
	Category              string            `json:"category"`
	Importance            string            `json:"importance"`
	Assets                flexibleStrings   `json:"assets"`
	Actions               []notice.Action   `json:"actions"`
	Schedules             []notice.Schedule `json:"schedules"`
	RiskTags              flexibleStrings   `json:"risk_tags"`
	ImageURLs             flexibleStrings   `json:"image_urls"`
	ImageText             string            `json:"image_text"`
	ImageAnalysis         []map[string]any  `json:"image_analysis"`
	Confidence            float64           `json:"analysis_confidence"`
}

type flexibleStrings []string

func (values *flexibleStrings) UnmarshalJSON(payload []byte) error {
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	result := []string{}
	appendValue := func(value any) {
		switch typed := value.(type) {
		case string:
			if typed = strings.TrimSpace(typed); typed != "" {
				result = append(result, typed)
			}
		case map[string]any:
			for _, key := range []string{"symbol", "asset", "code", "ticker", "tag", "url", "value", "name"} {
				if text, ok := typed[key].(string); ok && strings.TrimSpace(text) != "" {
					result = append(result, strings.TrimSpace(text))
					return
				}
			}
		}
	}
	switch typed := raw.(type) {
	case []any:
		for _, value := range typed {
			appendValue(value)
		}
	default:
		appendValue(typed)
	}
	*values = result
	return nil
}

type analysisDecision struct {
	Result           analysisResult
	Provider         string
	Model            string
	Reasoning        string
	EscalationReason string
}

func (p *pipeline) analyze(ctx context.Context, evidence materializedEvidence, stem string) (analysisDecision, error) {
	prompt, err := detailPrompt(evidence)
	if err != nil {
		return analysisDecision{}, err
	}
	toolsets := ""
	if requiresModelVision(evidence) {
		toolsets = "vision"
	}
	return p.invokeCodex(ctx, prompt, evidence, toolsets, stem, "")
}

func (p *pipeline) invokeCodex(
	ctx context.Context,
	prompt string, evidence materializedEvidence, toolsets, stem, reason string,
) (analysisDecision, error) {
	model, effort := noticeAnalysisRuntime(toolsets)
	result, err := p.invoke(ctx, prompt, "openai-codex", model, effort, toolsets, stem+"-openai-codex")
	assetsDropped := false
	if err == nil {
		result, assetsDropped, err = prepareAnalysisResultForValidation(result, evidence)
	}
	if err != nil {
		return analysisDecision{}, err
	}
	if assetsDropped {
		if reason == "" {
			reason = "UNSUPPORTED_ASSET_DROPPED"
		} else {
			reason += "+UNSUPPORTED_ASSET_DROPPED"
		}
	}
	return analysisDecision{Result: result, Provider: "openai-codex", Model: model, Reasoning: effort, EscalationReason: reason}, nil
}

func noticeAnalysisRuntime(toolsets string) (string, string) {
	if strings.EqualFold(strings.TrimSpace(toolsets), "vision") {
		return noticeVisionAnalysisModel, noticeVisionAnalysisEffort
	}
	return noticeAnalysisModel, noticeAnalysisEffort
}

func (p *pipeline) analyzeWithContinuation(
	ctx context.Context,
	evidence materializedEvidence,
	stem string,
) (analysisDecision, int, error) {
	if notice.IsDomesticExchange(evidence.Item.Exchange) {
		attemptCtx, cancel := context.WithTimeout(ctx, noticeAnalysisAttemptTimeout)
		defer cancel()
		decision, err := p.analyze(attemptCtx, evidence, stem)
		return decision, 1, err
	}
	return runNoticeAnalysisAttempts(
		ctx, noticeAnalysisAttemptTimeout,
		func(attemptCtx context.Context) (analysisDecision, error) {
			return p.analyze(attemptCtx, evidence, stem)
		},
		func(attemptCtx context.Context, firstErr error) (analysisDecision, error) {
			return p.continueAnalysis(attemptCtx, evidence, stem, firstErr)
		},
	)
}

func runNoticeAnalysisAttempts(
	ctx context.Context,
	attemptTimeout time.Duration,
	first func(context.Context) (analysisDecision, error),
	continuation func(context.Context, error) (analysisDecision, error),
) (analysisDecision, int, error) {
	if attemptTimeout <= 0 {
		attemptTimeout = noticeAnalysisAttemptTimeout
	}
	firstCtx, cancelFirst := context.WithTimeout(ctx, attemptTimeout)
	decision, firstErr := first(firstCtx)
	cancelFirst()
	if firstErr == nil {
		return decision, 1, nil
	}
	if err := ctx.Err(); err != nil {
		return analysisDecision{}, 1, err
	}
	continuationCtx, cancelContinuation := context.WithTimeout(ctx, attemptTimeout)
	decision, continuationErr := continuation(continuationCtx, firstErr)
	cancelContinuation()
	if continuationErr == nil {
		return decision, 2, nil
	}
	return analysisDecision{}, 2, fmt.Errorf(
		"notice analysis failed after one 5-minute continuation: first: %v; continuation: %w",
		firstErr, continuationErr,
	)
}

func (p *pipeline) continueAnalysis(
	ctx context.Context,
	evidence materializedEvidence,
	stem string,
	firstErr error,
) (analysisDecision, error) {
	prompt, err := detailPrompt(evidence)
	if err != nil {
		return analysisDecision{}, err
	}
	reason, _, _ := analysisFailureRetryDisposition(firstErr)
	if reason == "" {
		reason = "FIRST_ATTEMPT_FAILED"
	}
	reasonJSON, _ := json.Marshal(reason)
	prompt += "\n\n# CONTINUATION_ATTEMPT\n" +
		"The first bounded analysis attempt did not produce an accepted result. " +
		"Continue with the same official evidence; do not browse or invent facts. " +
		"Return one complete JSON object satisfying the original contract.\n" +
		"FIRST_ATTEMPT_FAILURE_CLASS=" + string(reasonJSON)
	toolsets := ""
	if requiresModelVision(evidence) {
		toolsets = "vision"
	}
	model, effort := noticeAnalysisRuntime(toolsets)
	result, err := p.invoke(
		ctx, prompt, "openai-codex", model, effort, toolsets,
		stem+"-openai-codex-continuation",
	)
	assetsDropped := false
	if err == nil {
		result, assetsDropped, err = prepareAnalysisResultForValidation(result, evidence)
	}
	if err != nil {
		return analysisDecision{}, err
	}
	escalation := "CONTINUATION_AFTER_FAILURE"
	if assetsDropped {
		escalation += "+UNSUPPORTED_ASSET_DROPPED"
	}
	return analysisDecision{
		Result: result, Provider: "openai-codex", Model: model,
		Reasoning: effort, EscalationReason: escalation,
	}, nil
}

func (p *pipeline) invoke(
	ctx context.Context,
	prompt, provider, model, reasoning, toolsets, usageStem string,
) (analysisResult, error) {
	promptBytes, _ := normalizeHermesUTF8([]byte(prompt))
	prompt = string(promptBytes)
	if runtime.GOOS == "windows" && len(prompt) > 24_000 {
		return analysisResult{}, errors.New("official notice evidence exceeds the safe Windows Hermes command-line limit")
	}
	hermesBin := strings.TrimSpace(p.config.HermesBin)
	if hermesBin == "" {
		hermesBin = "hermes"
	}
	workingDir, err := filepath.Abs(p.outputDir)
	if err != nil {
		return analysisResult{}, fmt.Errorf("resolve isolated Hermes working directory: %w", err)
	}
	usagePath, err := filepath.Abs(filepath.Join(p.usageDir, usageStem+"-usage.json"))
	if err != nil {
		return analysisResult{}, fmt.Errorf("resolve Hermes usage path: %w", err)
	}
	arguments := isolatedHermesArguments(
		prompt, provider, model, usagePath, reasoning, toolsets, workingDir,
	)
	timeout := 5 * time.Minute
	if configured := timeoutFromEnvironment(provider); configured > 0 {
		timeout = configured
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := waitForInteractiveQuestion(callCtx, p.config.OutputDir); err != nil {
			return analysisResult{}, err
		}
		command := exec.Command(hermesBin, arguments...)
		command.Dir = workingDir
		command.Env = cleanChildEnvironment()
		stdout := newBoundedOutputBuffer(maxHermesResponseBytes)
		stderr := newBoundedOutputBuffer(maxHermesDiagnosticBytes)
		command.Stdout, command.Stderr = stdout, stderr
		if err := runBackgroundHermesCommand(callCtx, p.config.OutputDir, command); err != nil {
			if errors.Is(err, errHermesAnalysisPreempted) {
				// Explicit targeted reanalysis permits at most one model start.
				// Only the normal backlog may resume after interactive priority.
				if p.config.TargetedReanalysis != nil {
					return analysisResult{}, err
				}
				continue
			}
			message := tail(strings.TrimSpace(stderr.String()+"\n"+stdout.String()), maxLogOutputBytes)
			if stderr.Exceeded() {
				message = strings.TrimSpace(message + "\n[diagnostic output truncated]")
			}
			if message == "" {
				message = err.Error()
			}
			return analysisResult{}, fmt.Errorf("Hermes %s/%s failed: %s", provider, model, message)
		}
		if stdout.Exceeded() {
			return analysisResult{}, fmt.Errorf("Hermes %s/%s response exceeded the safe output limit", provider, model)
		}
		if strings.Contains(stdout.String(), "Codex app-server turn failed:") {
			return analysisResult{}, errors.New(tail(stdout.String(), maxLogOutputBytes))
		}
		normalized, _ := normalizeHermesUTF8(stdout.Bytes())
		_ = os.WriteFile(filepath.Join(p.outputDir, usageStem+"-response.json"), normalized, 0o600)
		return decodeHermesResult(normalized)
	}
}

func timeoutFromEnvironment(provider string) time.Duration {
	if provider != "openai-codex" {
		return 0
	}
	name := "HERMES_NOTICE_OAUTH_TIMEOUT_SECONDS"
	minimum, maximum := 30, 900
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < minimum {
		return 0
	}
	return time.Duration(min(value, maximum)) * time.Second
}

func cleanChildEnvironment() []string {
	result := make([]string, 0, len(os.Environ())+len(hermesUTF8Environment))
	codexBinDir := preferredCodexBinDir()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		normalized := strings.ToUpper(name)
		if _, blocked := paidAPIEnvironment[normalized]; blocked {
			continue
		}
		if restrictedChildEnvironmentName(normalized) {
			continue
		}
		if _, forced := hermesUTF8Environment[normalized]; forced {
			continue
		}
		if codexBinDir != "" && normalized == "PATH" {
			continue
		}
		result = append(result, entry)
	}
	for name, value := range hermesUTF8Environment {
		result = append(result, name+"="+value)
	}
	if codexBinDir != "" {
		result = append(result, "PATH="+codexBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		result = append(result, "HERMES_CODEX_BIN_DIR="+codexBinDir)
	}
	return result
}

func preferredCodexBinDir() string {
	resolved, _ := ResolveCodexBinDir(os.Getenv("HERMES_CODEX_BIN_DIR"))
	return resolved
}

func decodeHermesResult(raw []byte) (analysisResult, error) {
	raw, _ = normalizeHermesUTF8(raw)
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		return analysisResult{}, errors.New("Hermes did not return a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(text[start : end+1]))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var result analysisResult
	if err := decoder.Decode(&result); err != nil {
		return analysisResult{}, fmt.Errorf("decode Hermes JSON: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return analysisResult{}, fmt.Errorf("decode Hermes JSON: %w", err)
	}
	return result, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return fmt.Errorf("unexpected trailing JSON content: %w", err)
	}
	return nil
}

// normalizeHermesUTF8 keeps the pipeline moving when a Windows child process
// ignores the requested UTF-8 environment. It prefers lossless decoding and
// only substitutes irrecoverable bytes as a final fallback.
func normalizeHermesUTF8(raw []byte) ([]byte, string) {
	if utf8.Valid(raw) {
		return raw, "utf-8"
	}
	if len(raw) >= 2 {
		switch {
		case raw[0] == 0xff && raw[1] == 0xfe:
			if decoded, err := textunicode.UTF16(textunicode.LittleEndian, textunicode.ExpectBOM).NewDecoder().Bytes(raw); err == nil && utf8.Valid(decoded) {
				return decoded, "utf-16"
			}
		case raw[0] == 0xfe && raw[1] == 0xff:
			if decoded, err := textunicode.UTF16(textunicode.BigEndian, textunicode.ExpectBOM).NewDecoder().Bytes(raw); err == nil && utf8.Valid(decoded) {
				return decoded, "utf-16"
			}
		}
	}
	if decoded, err := korean.EUCKR.NewDecoder().Bytes(raw); err == nil && utf8.Valid(decoded) {
		return decoded, "cp949/euc-kr"
	}
	return bytes.ToValidUTF8(raw, []byte("\uFFFD")), "utf-8-repaired"
}

func detailPrompt(evidence materializedEvidence) (string, error) {
	imagePassages, err := verifiedWorkerImagePassages(evidence)
	if err != nil {
		return "", err
	}
	contentEvidence := map[string]any{
		"official_title": evidence.Title, "official_body_text": evidence.BodyText,
	}
	if len(imagePassages) > 0 {
		contentEvidence["verified_native_ocr"] = imagePassages
	}
	discovered := map[string]any{
		"published_ts_ms":     evidence.Item.PublishedTSMS,
		"updated_ts_ms":       evidence.Item.UpdatedTSMS,
		"timestamp_precision": evidence.Item.TimestampPrecision,
		"category_hint":       evidence.Item.Category,
	}
	contentJSON, err := json.Marshal(contentEvidence)
	if err != nil {
		return "", err
	}
	discoveredJSON, err := json.Marshal(discovered)
	if err != nil {
		return "", err
	}
	exchangeJSON, err := json.Marshal(evidence.Item.Exchange)
	if err != nil {
		return "", err
	}
	translationRule := `The notice is Korean. Set original_language to "ko", translation_status to "NOT_REQUIRED", and keep translated_title_ko and translated_body_text_ko empty.`
	if isForeignExchange(evidence.Item.Exchange) {
		translationRule = `The notice is English. Set original_language to "en" and translation_status to "TRANSLATED". Translate the complete title and body into Korean without omissions. Preserve every source number, symbol, and unit.`
	}
	prompt := strings.NewReplacer(
		"{{POLICY_VERSION}}", analysisPolicyVersion,
		"{{EXCHANGE_JSON}}", string(exchangeJSON),
		"{{LIST_METADATA_JSON}}", string(discoveredJSON),
		"{{CONTENT_EVIDENCE_JSON}}", string(contentJSON),
		"{{TRANSLATION_RULE}}", translationRule,
	).Replace(noticeContentAnalysisPromptTemplate)
	if len(evidence.ImageURLs) == 0 || hasLocalImageAnalysis(evidence) {
		return strings.TrimSpace(prompt), nil
	}
	imageEvidence := map[string]any{
		"official_image_urls": evidence.ImageURLs,
		"local_images":        evidence.LocalImages,
	}
	imageJSON, err := json.Marshal(imageEvidence)
	if err != nil {
		return "", err
	}
	imagePrompt := strings.NewReplacer(
		"{{POLICY_VERSION}}", analysisPolicyVersion,
		"{{IMAGE_EVIDENCE_JSON}}", string(imageJSON),
	).Replace(noticeImageAnalysisPromptTemplate)
	return strings.TrimSpace(prompt) + "\n\n" + strings.TrimSpace(imagePrompt), nil
}

func requiresModelVision(evidence materializedEvidence) bool {
	return len(evidence.ImageURLs) > 0 && !hasLocalImageAnalysis(evidence)
}

func freeFailureExhaustsRun(err error) bool {
	message := strings.ToLower(err.Error())
	for _, term := range []string{
		"quota", "rate limit", "rate-limit", "too many requests", "exhausted",
		"model not found", "unknown model", "invalid model", "no available model",
		"provider unavailable", "service unavailable", "http 404", "http 429",
	} {
		if strings.Contains(message, term) {
			return true
		}
	}
	return false
}
