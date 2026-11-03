package hermesrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

// NoticeQuestionPolicyIdentity binds cache entries to both stages and their
// actual prompts. A request cannot reuse an answer admitted by another audit.
func NoticeQuestionPolicyIdentity() string {
	return noticeSemanticHash([]byte("FACT_BINDINGS_FULL_AUDIT_V23\x00" + noticeQuestionPrimaryModel + "\x00" + noticeQuestionPrimaryEffort + "\x00" + noticeQuestionPromptTemplate + "\x00" + noticeQuestionAuditPromptTemplate))
}

func validateNoticeQuestionHardFacts(result noticeQuestionModelResult, evidence []byte) error {
	if err := noticeSemanticMechanicalGate(result, evidence); err != nil {
		return err
	}
	sources := noticeQuestionClaimSources(noticeQuestionCitedEvidence(result.SourceNoticeIDs, evidence))
	known := map[string]bool{"KST": true, "UTC": true, "GMT": true, "API": true, "JSON": true}
	for _, source := range sources {
		for _, word := range noticeClaimTicker.FindAllString(source.Title+" "+strings.Join(source.Passages, " ")+" "+strings.Join(source.Assets, " "), -1) {
			known[word] = true
		}
	}
	for _, word := range noticeClaimTicker.FindAllString(noticeQuestionResultText(result), -1) {
		if len(word) > 1 && !known[word] {
			return fmt.Errorf("notice answer names an unsupported event target: %s", word)
		}
	}
	return validateNoticeQuestionFactBindings(noticeQuestionResultText(result), sources)
}

func askNoticeQuestionAudited(ctx context.Context, config MarketQuestionConfig, persona, question string, evidence []byte, asOfMS int64, runDir string, invoke marketQuestionInvoker) (marketanalysis.NoticeQuestionAnswer, error) {
	// CaptureInput is market-only. Notice audit input already has its own
	// mandatory correlated writer; isolate direct callers as well as Service.
	config.CaptureInput = false
	config.Model = noticeQuestionPrimaryModel
	config.Temperature = 0
	generationCtx := prepareQuestionCall(ctx, "notice_generation")
	raw, err := invoke(generationCtx, config, noticeQuestionPrompt(persona, question, evidence), noticeQuestionPrimaryModel, noticeQuestionPrimaryEffort, runDir)
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("notice generation failed: %w", err)
	}
	result, err := decodeNoticeQuestionResult(raw)
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	if err = validateNoticeQuestionHardFacts(result, evidence); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("notice FACT_BINDINGS rejected: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	segments, sources := noticeSemanticSegments(result), noticeSemanticSources(result, evidence)
	prompt, spans := noticeSemanticVerdictInput(segments, sources)
	if len(segments) == 0 || len(segments) > 128 || len(prompt) > maxNoticeQuestionPrompt {
		return marketanalysis.NoticeQuestionAnswer{}, errors.New("notice full audit input exceeds bounded scope")
	}
	auditDir, err := marketQuestionRunDir(config, "notice-question-audits")
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	auditCtx := prepareQuestionCall(ctx, "notice_full_audit")
	// Keep the exact bound input alongside the provider's existing raw output.
	if err = os.WriteFile(filepath.Join(auditDir, fmt.Sprintf("%d-input.txt", questionTrace(auditCtx).CallID)), []byte(prompt), 0600); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	raw, err = invoke(auditCtx, config, prompt, noticeQuestionPrimaryModel, noticeQuestionPrimaryEffort, auditDir)
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("notice full audit failed: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	object, err := marketQuestionJSONObjectText(raw)
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	var verdict noticeSemanticVerdictAudit
	decoder := json.NewDecoder(strings.NewReader(object))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&verdict); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("notice audit schema rejected: %w", err)
	}
	if err = requireJSONEOF(decoder); err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, err
	}
	audit, err := noticeSemanticExpandVerdicts(verdict, segments, sources, spans)
	if err == nil {
		err = validateNoticeSemanticAudit(audit, segments, sources)
	}
	if err != nil {
		return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("notice audit bindings rejected: %w", err)
	}
	if audit.Verdict != "SUPPORTED" {
		return marketanalysis.NoticeQuestionAnswer{}, fmt.Errorf("notice full audit verdict: %s", audit.Verdict)
	}
	return noticeQuestionAnswer(result, persona, "HERMES_NOTICE", noticeQuestionPrimaryModel, asOfMS), nil
}
