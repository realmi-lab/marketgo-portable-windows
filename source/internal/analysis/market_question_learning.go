package analysis

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MarketQuestionFeedbackCorrect       = "CORRECT"
	MarketQuestionFeedbackWrongPeriod   = "WRONG_PERIOD"
	MarketQuestionFeedbackWrongTarget   = "WRONG_TARGET"
	MarketQuestionFeedbackWrongMetric   = "WRONG_METRIC"
	MarketQuestionFeedbackWrongAnalysis = "WRONG_ANALYSIS"
)

var (
	ErrInvalidMarketQuestionFeedback  = errors.New("market question feedback is invalid")
	ErrMarketQuestionAttemptNotFound  = errors.New("market question attempt was not found")
	ErrMarketQuestionLearningDisabled = errors.New("market question learning is disabled")
)

// MarketQuestionLearningAttempt records only the information needed to audit
// question interpretation. It deliberately excludes answer prose, numeric
// evidence, conversation contents, credentials, and raw database rows.
type MarketQuestionLearningAttempt struct {
	RequestID              string
	AskedAt                time.Time
	Question               string
	QuestionSHA256         string
	HistoryTurnCount       int
	CatalogVersion         string
	PlannerPlanJSON        json.RawMessage
	PlannerQueryJSON       json.RawMessage
	PlannerError           string
	PlanValidationError    string
	DeterministicQueryJSON json.RawMessage
	DeterministicError     string
	PlannerParserAgree     *bool
	AcceptedQueryJSON      json.RawMessage
	PlanSource             string
	EvidenceLayer          string
	QualityStatus          string
	ProviderSource         string
	ProviderModel          string
	ProviderFallback       bool
	AnswerGuardStatus      string
	Outcome                string
	ErrorKind              string
	LatencyMS              int64
	AnswerPolicyVersion    string
}

// MarketQuestionFeedback is an explicit user label for one persisted attempt.
// A wrong interpretation requires a corrected natural-language question; it
// is still review material, not an automatically trusted training example.
type MarketQuestionFeedback struct {
	RequestID         string `json:"request_id"`
	Verdict           string `json:"verdict"`
	CorrectedQuestion string `json:"corrected_question,omitempty"`
}

func (f MarketQuestionFeedback) NormalizeAndValidate() (MarketQuestionFeedback, error) {
	f.RequestID = strings.ToLower(strings.TrimSpace(f.RequestID))
	f.Verdict = strings.ToUpper(strings.TrimSpace(f.Verdict))
	f.CorrectedQuestion = strings.TrimSpace(f.CorrectedQuestion)
	if len(f.RequestID) != 32 {
		return MarketQuestionFeedback{}, ErrInvalidMarketQuestionFeedback
	}
	for _, value := range f.RequestID {
		if !strings.ContainsRune("0123456789abcdef", value) {
			return MarketQuestionFeedback{}, ErrInvalidMarketQuestionFeedback
		}
	}
	validVerdict := false
	for _, verdict := range []string{
		MarketQuestionFeedbackCorrect,
		MarketQuestionFeedbackWrongPeriod,
		MarketQuestionFeedbackWrongTarget,
		MarketQuestionFeedbackWrongMetric,
		MarketQuestionFeedbackWrongAnalysis,
	} {
		if f.Verdict == verdict {
			validVerdict = true
			break
		}
	}
	if !validVerdict || !utf8.ValidString(f.CorrectedQuestion) {
		return MarketQuestionFeedback{}, ErrInvalidMarketQuestionFeedback
	}
	correctionLength := utf8.RuneCountInString(f.CorrectedQuestion)
	if f.Verdict == MarketQuestionFeedbackCorrect {
		if correctionLength != 0 {
			return MarketQuestionFeedback{}, ErrInvalidMarketQuestionFeedback
		}
		return f, nil
	}
	if correctionLength < 2 || correctionLength > 500 {
		return MarketQuestionFeedback{}, ErrInvalidMarketQuestionFeedback
	}
	return f, nil
}
