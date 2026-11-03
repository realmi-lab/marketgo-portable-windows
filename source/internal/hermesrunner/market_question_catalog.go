package hermesrunner

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/marketquestion"
)

func renderMarketQuestionPlanPrompt(
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	reference time.Time,
) string {
	questionJSON, _ := json.Marshal(strings.TrimSpace(question))
	historyJSON, _ := json.Marshal(history)
	referenceJSON, _ := json.Marshal(reference.In(time.FixedZone("Asia/Seoul", 9*60*60)).Format(time.RFC3339))
	return strings.NewReplacer(
		"{{MARKET_QUESTION_SEMANTIC_CATALOG}}", marketQuestionPlannerCatalogMarkdown(),
		"{{DEFAULT_PLAN_EXAMPLE_JSON}}", marketQuestionDefaultPlanExampleJSON(),
		"{{USER_QUESTION_JSON}}", string(questionJSON),
		"{{CONVERSATION_HISTORY_JSON}}", string(historyJSON),
		"{{REFERENCE_TIME_JSON}}", string(referenceJSON),
	).Replace(marketQuestionPlanPromptTemplate)
}

func marketQuestionPlannerCatalogMarkdown() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "catalog_version: `%s`\n\n", marketquestion.CatalogVersion)
	builder.WriteString("### 허용 근거 도구\n\n")
	for _, tool := range marketquestion.EvidenceTools() {
		fmt.Fprintf(&builder, "- `%s`: %s\n", tool.ID, tool.Description)
	}

	builder.WriteString("\n### 허용 지표\n\n")
	for _, metric := range marketquestion.Metrics() {
		fmt.Fprintf(
			&builder,
			"- `%s`: %s. 국내시장 범위: %s. asset: `%s`; exchange: `%s`; 허용 범위: %s\n",
			metric.ID,
			metric.Description,
			metric.DenominatorMeaning,
			metric.Asset,
			metric.Exchange,
			formatMetricRanges(metric.AllowedRanges),
		)
	}

	builder.WriteString("\n### 허용 비교 의미\n\n")
	for _, comparison := range marketquestion.Comparisons() {
		fmt.Fprintf(&builder, "- `%s`: %s\n", comparison.ID, comparison.Description)
	}

	builder.WriteString("\n### 범위별 비교와 필수 근거\n\n")
	for _, rangeDefinition := range marketquestion.Ranges() {
		fmt.Fprintf(&builder, "- `%s`: %s\n", rangeDefinition.Kind, rangeDefinition.Description)
		for _, route := range rangeDefinition.Routes {
			condition := "기본"
			if route.SelectorTool != "" {
				condition = fmt.Sprintf("`%s` 선택 시", route.SelectorTool)
			}
			fmt.Fprintf(
				&builder,
				"  - %s → comparison `%s`; 필수 근거: %s; 허용 근거: %s\n",
				condition,
				route.ExpectedComparison,
				formatEvidenceToolIDs(route.RequiredTools),
				formatEvidenceToolIDs(route.AllowedTools),
			)
		}
	}

	builder.WriteString("\n### 시간 경로 해상도 후보\n\n- 정확히 ")
	for index, seconds := range marketquestion.PathResolutionSeconds() {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString("`")
		builder.WriteString(strconv.FormatInt(seconds, 10))
		builder.WriteString("`")
	}
	builder.WriteString("초 중 하나\n")
	return strings.TrimSpace(builder.String())
}

func marketQuestionDefaultPlanExampleJSON() string {
	example := MarketQuestionPlan{
		RangeKind:        string(marketquestion.RangeCurrent),
		CalendarDatesKST: []string{},
		MarketScope:      "KRW",
		Metric:           string(marketquestion.MetricOverallBithumbShare),
		Comparison:       string(marketquestion.ComparisonCurrentSnapshot),
		EvidenceTools:    []string{string(marketquestion.ToolCurrentOfficialTicker)},
		AnswerDepth:      "DETAILED",
		QuestionParts:    []string{"사용자 질문에서 그대로 복사한 요청 부분"},
		Reason:           "질문과 선택 근거를 한 문장으로 설명",
	}
	raw, _ := json.MarshalIndent(example, "", "  ")
	return string(raw)
}

func formatMetricRanges(rules []marketquestion.MetricRangeRule) string {
	parts := make([]string, 0, len(rules))
	for _, rule := range rules {
		value := fmt.Sprintf("`%s`", rule.RangeKind)
		if len(rule.DisallowedTools) > 0 {
			value += " (제외 근거: " + formatEvidenceToolIDs(rule.DisallowedTools) + ")"
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, ", ")
}

func formatEvidenceToolIDs(tools []marketquestion.EvidenceToolID) string {
	if len(tools) == 0 {
		return "없음"
	}
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		parts = append(parts, fmt.Sprintf("`%s`", tool))
	}
	return strings.Join(parts, ", ")
}
