package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/marketquestion"
)

const marketQuestionLearningWriteTimeout = 2 * time.Second

type marketQuestionLearningTrace struct {
	marketanalysis.MarketQuestionLearningAttempt
}

func (s *Service) beginMarketQuestionLearning(
	question string,
	historyTurnCount int,
) *marketQuestionLearningTrace {
	if s == nil || !s.questionLearningEnabled || s.postgresRepository == nil {
		return nil
	}
	rawID := make([]byte, 16)
	if _, err := rand.Read(rawID); err != nil {
		if s.logger != nil {
			s.logger.Warn("market question learning request ID unavailable", "error", err)
		}
		return nil
	}
	digest := sha256.Sum256([]byte(question))
	historyTurnCount = min(max(historyTurnCount, 0), marketanalysis.MaxMarketQuestionConversationTurns)
	return &marketQuestionLearningTrace{MarketQuestionLearningAttempt: marketanalysis.MarketQuestionLearningAttempt{
		RequestID:           hex.EncodeToString(rawID),
		AskedAt:             time.Now(),
		Question:            question,
		QuestionSHA256:      hex.EncodeToString(digest[:]),
		HistoryTurnCount:    historyTurnCount,
		CatalogVersion:      marketquestion.CatalogVersion,
		AnswerGuardStatus:   "NOT_RUN",
		Outcome:             "FAILED",
		AnswerPolicyVersion: marketQuestionPromptPolicyVersion,
	}}
}

func (s *Service) finishMarketQuestionLearning(
	ctx context.Context,
	trace *marketQuestionLearningTrace,
	response marketanalysis.GroundedMarketQuestionResponse,
	answerErr error,
) bool {
	if trace == nil || s == nil || s.postgresRepository == nil || ctx.Err() != nil {
		return false
	}
	trace.LatencyMS = max(0, time.Since(trace.AskedAt).Milliseconds())
	trace.ErrorKind = marketQuestionLearningErrorKind(answerErr)
	switch {
	case answerErr == nil:
		trace.Outcome = "COMPLETED"
	case errors.Is(answerErr, marketanalysis.ErrRestrictedLLMQuestion):
		trace.Outcome = "RESTRICTED"
	case errors.Is(answerErr, marketanalysis.ErrInvalidMarketQuestion):
		trace.Outcome = "INVALID"
	default:
		trace.Outcome = "FAILED"
	}
	if answerErr == nil {
		trace.EvidenceLayer = response.Facts.EvidenceLayer
		trace.QualityStatus = response.Quality.Status
		trace.ProviderSource = response.Provider.Source
		trace.ProviderModel = response.Provider.Model
		trace.ProviderFallback = response.Provider.FallbackUsed
	}
	writeCtx, cancel := context.WithTimeout(ctx, marketQuestionLearningWriteTimeout)
	defer cancel()
	write := func(ctx context.Context) error {
		return s.postgresRepository.SaveMarketQuestionLearningAttempt(ctx, trace.MarketQuestionLearningAttempt)
	}
	var err error
	if s.deployment != nil {
		err = s.deployment.dispatchActiveOnly(writeCtx, bufferedDeploymentWrite{name: "market_question_learning", run: write})
	} else {
		err = write(writeCtx)
	}
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("market question learning attempt was not persisted", "error", err)
		}
		return false
	}
	return true
}

func (s *Service) SubmitMarketQuestionFeedback(
	ctx context.Context,
	feedback marketanalysis.MarketQuestionFeedback,
) error {
	if s == nil || !s.questionLearningEnabled || s.postgresRepository == nil {
		return marketanalysis.ErrMarketQuestionLearningDisabled
	}
	normalized, err := feedback.NormalizeAndValidate()
	if err != nil {
		return err
	}
	write := func(writeCtx context.Context) error {
		return s.postgresRepository.SaveMarketQuestionFeedback(writeCtx, normalized)
	}
	if s.deployment != nil {
		return s.deployment.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "market_question_feedback", run: write})
	}
	return write(ctx)
}

func marketQuestionLearningJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func marketQuestionLearningDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	value := []rune(strings.TrimSpace(err.Error()))
	if len(value) > 2_000 {
		value = value[:2_000]
	}
	return string(value)
}

func marketQuestionLearningErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, marketanalysis.ErrRestrictedLLMQuestion):
		return "RESTRICTED_SCOPE"
	case errors.Is(err, marketanalysis.ErrInvalidMarketQuestion):
		return "INVALID_QUESTION"
	case errors.Is(err, context.Canceled):
		return "CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEADLINE_EXCEEDED"
	default:
		return "INTERNAL_ERROR"
	}
}

func marketQuestionLearningQueriesAgree(
	planner marketanalysis.MarketQuestionQuery,
	deterministic marketanalysis.MarketQuestionQuery,
) bool {
	plannerJSON := marketQuestionLearningJSON(planner)
	deterministicJSON := marketQuestionLearningJSON(deterministic)
	return len(plannerJSON) > 0 && string(plannerJSON) == string(deterministicJSON)
}
