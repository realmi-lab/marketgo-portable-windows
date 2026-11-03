package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const marketQuestionAttemptRetention = 90 * 24 * time.Hour

func (r *Repository) SaveMarketQuestionLearningAttempt(
	ctx context.Context,
	attempt marketanalysis.MarketQuestionLearningAttempt,
) error {
	if r == nil || r.pool == nil {
		return errors.New("save market question learning attempt: repository is closed")
	}
	if attempt.AskedAt.IsZero() {
		attempt.AskedAt = time.Now()
	}
	if attempt.LatencyMS < 0 {
		attempt.LatencyMS = 0
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin market question learning attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Raw questions are review material, not permanent market evidence. Keep
	// cleanup bounded so interactive traffic cannot trigger an unbounded delete.
	if _, err := tx.Exec(ctx, `
		DELETE FROM market_question_attempts
		WHERE request_id IN (
			SELECT request_id FROM market_question_attempts
			WHERE expires_at <= now()
			ORDER BY expires_at
			LIMIT 1000
		)`,
	); err != nil {
		return fmt.Errorf("prune expired market question learning attempts: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO market_question_attempts (
			request_id, asked_at, expires_at, question_text, question_sha256,
			history_turn_count, catalog_version, planner_plan_json,
			planner_query_json, planner_error, plan_validation_error,
			deterministic_query_json, deterministic_error, planner_parser_agree,
			accepted_query_json, plan_source, evidence_layer, quality_status,
			provider_source, provider_model, provider_fallback,
			answer_guard_status, outcome, error_kind, latency_ms,
			answer_policy_version
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26
		)
		ON CONFLICT (request_id) DO NOTHING`,
		attempt.RequestID, attempt.AskedAt, attempt.AskedAt.Add(marketQuestionAttemptRetention),
		attempt.Question, attempt.QuestionSHA256, attempt.HistoryTurnCount,
		attempt.CatalogVersion, nullableQuestionJSON(attempt.PlannerPlanJSON),
		nullableQuestionJSON(attempt.PlannerQueryJSON), attempt.PlannerError,
		attempt.PlanValidationError, nullableQuestionJSON(attempt.DeterministicQueryJSON),
		attempt.DeterministicError, attempt.PlannerParserAgree,
		nullableQuestionJSON(attempt.AcceptedQueryJSON), attempt.PlanSource,
		attempt.EvidenceLayer, attempt.QualityStatus, attempt.ProviderSource,
		attempt.ProviderModel, attempt.ProviderFallback, attempt.AnswerGuardStatus,
		attempt.Outcome, attempt.ErrorKind, attempt.LatencyMS, attempt.AnswerPolicyVersion,
	)
	if err != nil {
		return fmt.Errorf("save market question learning attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit market question learning attempt: %w", err)
	}
	return nil
}

func (r *Repository) SaveMarketQuestionFeedback(
	ctx context.Context,
	feedback marketanalysis.MarketQuestionFeedback,
) error {
	if r == nil || r.pool == nil {
		return errors.New("save market question feedback: repository is closed")
	}
	feedback, err := feedback.NormalizeAndValidate()
	if err != nil {
		return err
	}
	var correctedQuestion any
	if feedback.CorrectedQuestion != "" {
		correctedQuestion = feedback.CorrectedQuestion
	}
	command, err := r.pool.Exec(ctx, `
		INSERT INTO market_question_feedback (
			request_id, verdict, corrected_question, received_at
		)
		SELECT request_id, $2, $3, now()
		FROM market_question_attempts
		WHERE request_id = $1 AND expires_at > now()
		ON CONFLICT (request_id) DO UPDATE SET
			verdict = EXCLUDED.verdict,
			corrected_question = EXCLUDED.corrected_question,
			received_at = EXCLUDED.received_at`,
		feedback.RequestID, feedback.Verdict, correctedQuestion,
	)
	if err != nil {
		return fmt.Errorf("save market question feedback: %w", err)
	}
	if command.RowsAffected() == 0 {
		return marketanalysis.ErrMarketQuestionAttemptNotFound
	}
	return nil
}

func nullableQuestionJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
