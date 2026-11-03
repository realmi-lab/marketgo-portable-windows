package hermesrunner

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const MarketQuestionPlannerModel = MarketQuestionModel

//go:embed prompts/market-question-plan.md
var marketQuestionPlanPromptTemplate string

type MarketQuestionPlan struct {
	RangeKind         string   `json:"range_kind"`
	CalendarDateKST   string   `json:"calendar_date_kst"`
	CalendarDatesKST  []string `json:"calendar_dates_kst"`
	StartKST          string   `json:"start_kst"`
	EndKST            string   `json:"end_kst"`
	WindowSec         int64    `json:"window_sec"`
	PathResolutionSec int64    `json:"path_resolution_sec"`
	MarketScope       string   `json:"market_scope"`
	Exchange          string   `json:"exchange"`
	Asset             string   `json:"asset"`
	Metric            string   `json:"metric"`
	Comparison        string   `json:"comparison"`
	EvidenceTools     []string `json:"evidence_tools"`
	AnswerDepth       string   `json:"answer_depth"`
	QuestionParts     []string `json:"question_parts,omitempty"`
	Reason            string   `json:"reason"`
}

func PlanMarketQuestion(
	ctx context.Context,
	config MarketQuestionConfig,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	reference time.Time,
) (MarketQuestionPlan, error) {
	prompt := renderMarketQuestionPlanPrompt(question, history, reference)
	config.Model = MarketQuestionPlannerModel
	config.Temperature = 0
	config.MaxTokens = min(config.MaxTokens, 2_000)
	if config.MaxTokens <= 0 {
		config.MaxTokens = 2_000
	}
	root := strings.TrimSpace(config.OutputDir)
	if root == "" {
		root = ".tmp/hermes-notices"
	}
	runDir := filepath.Join(root, "market-question-plans")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return MarketQuestionPlan{}, marketQuestionCaptureSetupError(config, fmt.Errorf("create market question plan directory: %w", err))
	}
	raw, err := invokeHermesMarketQuestion(ctx, config, strings.TrimSpace(prompt), config.Model, MarketQuestionReasoningEffort, runDir)
	if err != nil {
		return MarketQuestionPlan{}, err
	}
	return decodeMarketQuestionPlan(raw)
}

func decodeMarketQuestionPlan(raw []byte) (MarketQuestionPlan, error) {
	text, err := marketQuestionJSONObjectText(raw)
	if err != nil {
		return MarketQuestionPlan{}, fmt.Errorf("decode market question plan: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var plan MarketQuestionPlan
	if err := decoder.Decode(&plan); err != nil {
		return MarketQuestionPlan{}, fmt.Errorf("decode market question plan: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return MarketQuestionPlan{}, fmt.Errorf("decode market question plan: %w", err)
	}
	return plan, nil
}
