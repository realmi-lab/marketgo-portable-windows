package hermesrunner

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	marketanalysis "market-share-go/internal/analysis"
)

const (
	marketQuestionHermesTimeout        = 5 * time.Minute
	maxMarketQuestionPrompt            = 2_100_000
	maxMarketQuestionHistoryAnswer     = 40_000
	maxMarketQuestionHistoryRunes      = 600_000
	maxMarketQuestionPoints            = 10
	maxMarketQuestionClaimsPerBlock    = 16
	MarketQuestionModel                = "gpt-6.1-sol"
	MarketQuestionReasoningEffort      = "xhigh"
	MarketQuestionTemperature          = 0.0
	MaxMarketQuestionOutputTokens      = 15_000
	MaxInteractiveQuestionOutputTokens = 5_000
	hermesQuestionFallbackModel        = "gpt-5.6-sol"
	hermesQuestionFallbackEffort       = "medium"
)

//go:embed prompts/market-question-answer.md
var marketQuestionPromptTemplate string

type MarketQuestionConfig struct {
	HermesBin    string
	OutputDir    string
	Model        string
	MaxTokens    int
	Temperature  float64
	CallTimeout  time.Duration
	CaptureInput bool
}

type marketQuestionInvoker func(
	context.Context,
	MarketQuestionConfig,
	string,
	string,
	string,
	string,
) ([]byte, error)

type marketQuestionModelResult struct {
	BindingScope string                                    `json:"binding_scope,omitempty"`
	Answer       string                                    `json:"answer"`
	KeyPoints    []string                                  `json:"key_points"`
	Caveat       string                                    `json:"caveat"`
	ClaimBlocks  []marketanalysis.MarketQuestionClaimBlock `json:"claim_blocks,omitempty"`
}

func AskMarketQuestion(
	ctx context.Context,
	config MarketQuestionConfig,
	question string,
	evidenceJSON []byte,
	asOfMS int64,
) (marketanalysis.MarketQuestionAnswer, error) {
	return AskMarketQuestionWithHistory(ctx, config, question, nil, evidenceJSON, asOfMS)
}

func AskMarketQuestionWithHistory(
	ctx context.Context,
	config MarketQuestionConfig,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	evidenceJSON []byte,
	asOfMS int64,
) (marketanalysis.MarketQuestionAnswer, error) {
	return askMarketQuestionWithHistoryAndInvokers(
		ctx, config, question, history, evidenceJSON, asOfMS, invokeHermesMarketQuestion,
	)
}

func askMarketQuestionWithInvokers(
	ctx context.Context,
	config MarketQuestionConfig,
	question string,
	evidenceJSON []byte,
	asOfMS int64,
	hermesInvoker marketQuestionInvoker,
) (marketanalysis.MarketQuestionAnswer, error) {
	return askMarketQuestionWithHistoryAndInvokers(
		ctx, config, question, nil, evidenceJSON, asOfMS, hermesInvoker,
	)
}

func askMarketQuestionWithHistoryAndInvokers(
	ctx context.Context,
	config MarketQuestionConfig,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	evidenceJSON []byte,
	asOfMS int64,
	hermesInvoker marketQuestionInvoker,
) (marketanalysis.MarketQuestionAnswer, error) {
	if err := ctx.Err(); err != nil {
		return marketanalysis.MarketQuestionAnswer{}, err
	}
	question = strings.TrimSpace(question)
	if question == "" || !utf8.ValidString(question) {
		return marketanalysis.MarketQuestionAnswer{}, errors.New("market question must be valid UTF-8 text")
	}
	if !json.Valid(evidenceJSON) {
		return marketanalysis.MarketQuestionAnswer{}, errors.New("market question evidence must be valid JSON")
	}
	if err := validateMarketQuestionHistory(history); err != nil {
		return marketanalysis.MarketQuestionAnswer{}, err
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = MarketQuestionModel
	}
	compactEvidence, factIDs := compactMarketQuestionFactIDs(evidenceJSON)
	prompt := marketQuestionPromptWithHistory(question, history, compactEvidence)
	promptBytes, _ := normalizeHermesUTF8([]byte(prompt))
	if len(promptBytes) > maxMarketQuestionPrompt {
		return marketanalysis.MarketQuestionAnswer{}, errors.New("market question evidence exceeds the Hermes prompt limit")
	}

	root := strings.TrimSpace(config.OutputDir)
	if root == "" {
		root = filepath.Join(".tmp", "hermes-notices")
	}
	runDir := filepath.Join(root, "market-questions")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return marketanalysis.MarketQuestionAnswer{}, marketQuestionCaptureSetupError(config, fmt.Errorf("create Hermes question output directory: %w", err))
	}
	runDir, err := filepath.Abs(runDir)
	if err != nil {
		return marketanalysis.MarketQuestionAnswer{}, marketQuestionCaptureSetupError(config, fmt.Errorf("resolve market question output directory: %w", err))
	}

	effort := "medium"
	if model == MarketQuestionModel {
		effort = MarketQuestionReasoningEffort
		// The selected Codex route omits sampling temperature. Preserve legacy helper
		// overrides separately from the product's explicitly selected route.
		config.Temperature = 0
	}
	raw, hermesErr := hermesInvoker(
		ctx, config, string(promptBytes), model, effort, runDir,
	)
	if hermesErr == nil {
		result, decodeErr := decodeMarketQuestionResult(raw)
		if decodeErr == nil {
			decodeErr = expandMarketQuestionFactIDs(&result, factIDs)
		}
		if decodeErr == nil {
			return marketQuestionAnswer(result, "HERMES", model, asOfMS), nil
		}
		hermesErr = decodeErr
	}
	if !shouldRetryHermesMarketQuestion(ctx, model, hermesErr) {
		return marketanalysis.MarketQuestionAnswer{}, fmt.Errorf(
			"Hermes market question primary attempt failed without Sol retry: %w",
			hermesErr,
		)
	}

	raw, fallbackErr := hermesInvoker(
		ctx, config, string(promptBytes), hermesQuestionFallbackModel,
		hermesQuestionFallbackEffort, runDir,
	)
	if fallbackErr == nil {
		result, decodeErr := decodeMarketQuestionResult(raw)
		if decodeErr == nil {
			decodeErr = expandMarketQuestionFactIDs(&result, factIDs)
		}
		if decodeErr == nil {
			return marketQuestionAnswer(result, "HERMES_SOL_FALLBACK", hermesQuestionFallbackModel, asOfMS), nil
		}
		fallbackErr = decodeErr
	}
	return marketanalysis.MarketQuestionAnswer{}, fmt.Errorf(
		"Hermes market question attempts failed: primary: %v; Sol fallback: %v",
		hermesErr, fallbackErr,
	)
}

func shouldRetryHermesMarketQuestion(ctx context.Context, primaryModel string, err error) bool {
	if ctx.Err() != nil || errors.Is(err, ErrMarketQuestionInputCapture) || errors.Is(err, context.DeadlineExceeded) ||
		strings.EqualFold(strings.TrimSpace(primaryModel), hermesQuestionFallbackModel) ||
		strings.EqualFold(strings.TrimSpace(primaryModel), MarketQuestionModel) {
		return false
	}
	return !errors.Is(err, exec.ErrNotFound) &&
		!errors.Is(err, os.ErrNotExist) &&
		!errors.Is(err, os.ErrPermission)
}

func invokeHermesMarketQuestion(
	ctx context.Context,
	config MarketQuestionConfig,
	prompt, model, reasoning, runDir string,
) ([]byte, error) {
	trace := questionTrace(ctx)
	if trace == nil {
		ctx = prepareQuestionCall(ctx, "market_question")
		trace = questionTrace(ctx)
	}
	callID := trace.CallID
	usagePath := filepath.Join(runDir, fmt.Sprintf("%d-usage.json", callID))
	hermesBin := strings.TrimSpace(config.HermesBin)
	if hermesBin == "" {
		hermesBin = "hermes"
	}
	budget := marketQuestionHermesTimeout
	if config.CallTimeout > 0 {
		budget = min(config.CallTimeout, marketQuestionHermesTimeout)
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if config.CaptureInput {
		captureErr := callCtx.Err()
		if captureErr == nil {
			captureErr = captureMarketQuestionInput(runDir, callID, []byte(prompt))
		}
		if captureErr != nil {
			finishQuestionTrace(runDir, trace, captureErr)
			return nil, fmt.Errorf("%w: %w", ErrMarketQuestionInputCapture, captureErr)
		}
	}
	arguments := isolatedHermesArguments(
		prompt, "openai-codex", model, usagePath, reasoning, "", runDir,
	)
	command := exec.CommandContext(callCtx, hermesBin, arguments...)
	command.Dir = runDir
	command.Env = marketQuestionEnvironment(config.MaxTokens, config.Temperature)
	stdout := newBoundedOutputBuffer(maxHermesResponseBytes)
	stderr := newBoundedOutputBuffer(maxHermesDiagnosticBytes)
	command.Stdout, command.Stderr = stdout, stderr
	if deadline, ok := callCtx.Deadline(); ok {
		trace.EffectiveRemainingMS = max(0, time.Until(deadline).Milliseconds())
	}
	trace.Result = "PREPARED"
	writeQuestionTrace(runDir, trace)
	runErr := runCommand(callCtx, command)
	finishQuestionTrace(runDir, trace, runErr)
	if stdout.Exceeded() {
		return nil, errors.New("Hermes market question response exceeded the safe output limit")
	}
	if runErr != nil {
		message := tail(strings.TrimSpace(stderr.String()+"\n"+stdout.String()), maxLogOutputBytes)
		if stderr.Exceeded() {
			message = strings.TrimSpace(message + "\n[diagnostic output truncated]")
		}
		if message == "" || message == runErr.Error() {
			return nil, fmt.Errorf("Hermes market question failed: %w", runErr)
		}
		return nil, fmt.Errorf("Hermes market question failed: %w: %s", runErr, message)
	}
	normalized, _ := normalizeHermesUTF8(stdout.Bytes())
	_ = os.WriteFile(filepath.Join(runDir, fmt.Sprintf("%d-response.json", callID)), normalized, 0o600)
	return normalized, nil
}

func marketQuestionAnswer(
	result marketQuestionModelResult,
	source, model string,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	return marketanalysis.MarketQuestionAnswer{
		Answer: result.Answer, KeyPoints: result.KeyPoints, Caveat: result.Caveat,
		Source: source, Model: model, AsOfMS: asOfMS,
		ClaimBlocks: append([]marketanalysis.MarketQuestionClaimBlock(nil), result.ClaimBlocks...),
	}
}

func marketQuestionEnvironment(maxTokens int, temperature float64) []string {
	if maxTokens <= 0 {
		maxTokens = MaxMarketQuestionOutputTokens
	}
	maxTokens = min(maxTokens, MaxMarketQuestionOutputTokens)
	base := cleanChildEnvironment()
	result := make([]string, 0, len(base)+2)
	for _, item := range base {
		upper := strings.ToUpper(item)
		if strings.HasPrefix(upper, "HERMES_MAX_TOKENS=") ||
			strings.HasPrefix(upper, "HERMES_TEMPERATURE=") {
			continue
		}
		result = append(result, item)
	}
	result = append(result, fmt.Sprintf("HERMES_MAX_TOKENS=%d", maxTokens))
	if temperature > 0 && temperature <= 2 {
		result = append(result, "HERMES_TEMPERATURE="+strconv.FormatFloat(temperature, 'f', -1, 64))
	}
	return result
}

func marketQuestionPrompt(question string, evidenceJSON []byte) string {
	return marketQuestionPromptWithHistory(question, nil, evidenceJSON)
}

func marketQuestionPromptWithHistory(
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	evidenceJSON []byte,
) string {
	questionJSON, _ := json.Marshal(question)
	if history == nil {
		history = []marketanalysis.MarketQuestionConversationTurn{}
	}
	historyJSON, _ := json.Marshal(history)
	prompt := strings.ReplaceAll(
		marketQuestionPromptTemplate,
		"{{USER_QUESTION_JSON}}",
		string(questionJSON),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"{{SELECTED_ANALYSIS_GUIDANCE}}",
		marketQuestionPromptRouteGuidance(evidenceJSON),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"{{SERVER_EVIDENCE_JSON}}",
		string(evidenceJSON),
	)
	prompt = strings.ReplaceAll(
		prompt,
		"{{CONVERSATION_HISTORY_JSON}}",
		string(historyJSON),
	)
	var envelope struct {
		Bundle json.RawMessage `json:"typed_evidence_bundle"`
	}
	if json.Unmarshal(evidenceJSON, &envelope) == nil && len(envelope.Bundle) > 0 && string(envelope.Bundle) != "null" {
		prompt = `당신은 국내 거래소 점유율의 한국어 분석가다. 사용자에게 서버 정량 표가 이미 표시된다.
질문의 각 요구에 결론·근거·의미를 연결한다. 변화의 이유나 기여를 요청한 경우 확인된 양방향 기여와 한계를 설명한다.
최신 질문의 명시적인 분량 요청을 우선한다. 간단히 요청하면 핵심 지표와 필요한 기준·한계만 답한다.
상세 요청은 제공된 근거로 각 하위 질문을 설명한다. 일부 미확인이 확인된 항목의 설명을 지우지 않게 한다.
같은 표나 수치를 반복하지 말고 각 문단에 서로 다른 판단을 담는다. 분량을 채우기 위한 일반론은 쓰지 않는다.
외부 검색, 도구, 추가 계산, 미래 예측, 투자 권유를 하지 않는다. 산술 기여를 경제적 인과나 가격 등락으로 바꾸지 않는다.

근거 계약:
- typed_evidence_bundle.facts의 ID, 주체, 지표, 값, 기간, 분모, 출처와 상태만 사실 근거다.
- 현재 rolling 24시간 값, 공식 시간봉의 endpoint 비교, 직접 관측 구간은 서로 다른 자료다.
- 숫자는 해당 fact 값 또는 verified_display_values의 동일 대상·지표·기간 표시값을 그대로 쓴다.
- 점유율은 %, 변화와 기여는 %p, 거래금액은 원이다. 분모·거래소·코인 이름과 지표를 해당 숫자 가까이에 명시한다.
- 공식 비교는 실제 시작·끝 시각과 포함 거래소를 따른다. 현재 구성을 과거 분모로 바꾸지 않는다.
- UNKNOWN 또는 제외 항목을 0으로 쓰지 않는다. 없는 경로·공지·가격·보조 자료를 추정하지 않는다.
- 제공된 fact로 증명할 수 없는 요청은 미확인임을 밝힌다. 일부 미확인이 확인된 값까지 지우지 않게 한다.
- 문단의 각 사실 절은 같은 문단의 claims에 연결한다. 숫자가 없는 시장 주장도 예외가 아니다.
- 수치마다 주체·지표·기간을 명확히 한다. 여러 지표를 비교·해석할 때는 해당 근거 ID를 모두 연결한다.
- claim의 relation은 fact_relations에 지정된 해당 ID의 값을 그대로 쓴다. 양수 기여도도 CONTRIBUTION이다.
- 숫자는 verified_display_values에 표시된 값만 인용한다. 그 목록에 없는 원시 잔차·기타 값은 인용하지 않는다.
- 정의·분모·시간 기준은 정량 표에 이미 표시된다. 이를 다시 부정문이나 다른 기간과의 대비로 풀어 쓰지 않는다.
- 대화 기록은 후속 질문의 범위에만 사용하고 과거 숫자를 현재 사실로 쓰지 않는다.
- repair_request가 있으면 원래 유효한 설명을 유지하고 지적된 근거 연결만 고친다.

출력: binding_scope는 BLOCK, claim_blocks만 반환한다. answer, key_points, caveat와 claims.text를 중복 출력하지 않는다.
서버가 문단을 순서대로 연결한다. 각 text는 완전한 한국어 Markdown 문단이다.
각 문단의 claims에 실제 fact_id와 해당 relation을 나열한다. claims는 그 문단 전체를 뒷받침해야 한다. 근거 없는 사실을 덧붙이지 않는다. 문단별 독립 검증을 위해 서로 다른 논점은 분리한다.
relation은 LEVEL|INCREASE|DECREASE|UNCHANGED|CONTRIBUTION|COINCIDENCE_ONLY|UNAVAILABLE 중 하나다.
제목에는 수치나 시장 판단을 쓰지 않는다. 코드펜스 없이 JSON만 출력한다.
{"binding_scope":"BLOCK","claim_blocks":[{"text":"설명 문단","claims":[{"fact_id":"fact ID","relation":"LEVEL"}]}]}

USER_QUESTION_JSON:
` + string(questionJSON) + "\nCONVERSATION_HISTORY_JSON:\n" + string(historyJSON) + "\nSERVER_EVIDENCE_JSON:\n" + string(evidenceJSON)
	}
	return strings.TrimSpace(prompt)
}

func marketQuestionPromptRouteGuidance(evidenceJSON []byte) string {
	var envelope struct {
		Query             marketanalysis.MarketQuestionQuery `json:"query"`
		WebsocketInterval struct {
			CandleComparison *marketanalysis.MarketQuestionCandleComparison `json:"candle_comparison"`
		} `json:"websocket_interval"`
		CurrentAssetSnapshot struct {
			Source string `json:"source"`
		} `json:"current_asset_snapshot"`
	}
	_ = json.Unmarshal(evidenceJSON, &envelope)
	query := envelope.Query
	guidance := make([]string, 0, 8)

	switch query.Comparison {
	case marketanalysis.MarketQuestionFixedHistoricalInterval:
		guidance = append(guidance,
			"- `historical_range`가 최우선 근거다. 사용자가 지정한 전체 구간 합계와 표시용 경로를 구분하고, coverage가 불완전하면 누락을 0이나 인접 시각으로 채우지 않는다.",
			"- 근거가 있으면 거래소 구조, 고점→저점 산술 기여, 시작→마지막 구간, 10초 신호를 질문과 관련된 순서로 해석한다. 상세 질문이라도 정보가 없는 절을 억지로 만들지 않는다.",
		)
	case marketanalysis.MarketQuestionDailyRolling24hEndpoints:
		guidance = append(guidance,
			"- `official_daily_endpoint_series`가 최우선 근거다. 같은 KST 시각의 공식 endpoint별 원점, 인접 날짜 변화, 첫날→마지막 날 누적을 서로 섞지 않는다.",
			"- 이 경로는 검증 계약상 `요약 결론`, `날짜별 원점`, 각 인접 날짜 구간, `첫날→마지막 날 누적`, `가격 경로`, `데이터 품질`을 식별 가능한 Markdown 제목으로 포함한다. 상승·하락 기여 코인은 별도로 설명한다.",
		)
	case marketanalysis.MarketQuestionCalendarDayClose:
		guidance = append(guidance,
			"- `official_calendar_day`가 최우선 근거다. KST 00:00부터 다음 날 00:00까지의 공식 캘린더 마감값으로 답하고 rolling ticker와 바꾸지 않는다.",
			"- 마감 endpoint가 있으면 중간 경로가 불완전해도 마감값은 답할 수 있다. 다만 경로 전체의 고점·저점·지속성은 완전할 때만 말한다.",
		)
	case marketanalysis.MarketQuestionRolling24hEndpointDelta:
		guidance = append(guidance,
			"- `official_window`가 최우선 근거다. 실제 `window_sec`의 시작·끝 공식 endpoint 점유율, %p 변화, 시작점 대비 상대 변화율, 대상 거래소 거래대금과 국내 전체 거래대금의 상대 변화를 함께 연결한다.",
			"- 코인 기여는 가격 등락이 아니라 거래대금 이동의 산술 분해다. 양(상승 방어)·음(하락 압력) 기여를 분리하고 각 방향 상위 항목의 대상 거래소/경쟁 거래소 거래대금 변화를 해석한다.",
			"- `path_summary.status=COMPLETE`이면 전체 고점·저점·최대 시간 변화와 지속성을 설명한다. 불완전하면 `observed_segments`의 공백을 건너지 않는 연속 관측 구간만 설명하고, 공백 사이 움직임은 추정하지 않는다.",
			"- 상세 답변에서 근거가 존재하면 시작·끝 점유율과 변화폭, 대상 거래소와 국내 전체 거래대금 변화, 상승·하락 양방향 코인 기여, 거래소 기여, 시간 경로를 생략하지 않는다. 자유형 Markdown 구조는 유지하되 각 문단은 서로 다른 판단을 추가한다.",
		)
	case marketanalysis.MarketQuestionCurrentVsPreviousInterval:
		if envelope.WebsocketInterval.CandleComparison != nil {
			guidance = append(guidance,
				"- `websocket_interval.candle_comparison`의 공식 봉 동일 종목 묶음 비교가 사용 가능한 수치 근거다. WS가 불완전하다는 이유만으로 이 검증된 비교까지 거절하지 않는다. 직전·최근 거래대금과 점유율 및 변화를 verified_facts로 설명한다.",
				"- 모든 수치 결론에 동일 종목 묶음 기준임을 명시한다. 포함된 업비트·빗썸·코인원 종목 합계가 분모이며 전체 시장 점유율은 자료가 부족하여 확정할 수 없음을 밝힌다. 실제 KST 비교 구간을 표시하며 최신 분 경계로 바꾸지 않는다.",
				"- WebSocket 직접 관측은 여전히 불완전하다. requested_context의 LIVE 실시간 지표가 있으면 별도 보조 해석으로 연결하되 봉 거래대금에 더하지 않는다. 확인되지 않은 매수·매도 방향이나 10초 수치는 만들지 않는다.")
			break
		}
		guidance = append(guidance,
			"- `websocket_interval`이 최우선 근거다. 같은 길이의 직전·현재 직접 관측 구간만 비교하며 공식 시간봉이라고 부르지 않는다.",
			"- `status=CONFIRMED`일 때만 수치를 확정한다. 질문 대상의 직전·현재 거래대금, 점유율 수준과 변화폭을 연결하되 거래대금 증가와 점유율 상승을 같은 뜻으로 쓰지 않는다. 숫자는 검증된 표시값만 인용한다.",
		)
		switch {
		case query.Asset != "":
			guidance = append(guidance,
				"- `asset`의 선택 지표에 먼저 답한다. 질문과 관련되고 값이 확인될 때만 해당 코인의 국내/빗썸 거래대금, 체결 건수, 매수 비중을 보강한다. 매수 비중은 방향 확인 범위를 함께 구분하며 가격 등락으로 해석하지 않는다.",
			)
		case query.Metric == marketanalysis.MarketQuestionMetricTargetExchange ||
			(query.Exchange != "" && !strings.EqualFold(query.Exchange, "bithumb")):
			guidance = append(guidance,
				"- `target_exchange`의 직전·현재 거래대금과 비중, 변화폭이 주 답변이다. 질문 거래소의 변화를 빗썸의 변화로 바꾸지 않는다. 빗썸 기준 코인·거래소 기여를 질문 거래소의 기여라고 쓰지 않는다.",
			)
		default:
			guidance = append(guidance,
				"- 변화 요인을 묻거나 상세 근거가 필요하면 `asset_drivers`와 `exchange_drivers` 중 실제 제공된 양방향 기여를 설명한다. 최종 방향을 민 힘과 반대 방향의 힘을 나누고 빗썸/국내 거래대금 변화와 연결한다. 자료에 없는 가격·시간 경로는 만들지 않는다.",
			)
		}
	default:
		if query.Intent == marketanalysis.MarketQuestionAssetCapture {
			guidance = append(guidance, "- `current_asset_snapshot`이 최우선 근거다. 코인의 빗썸 포착률·빗썸 내부 비중·국내 전체 점유율 몫을 구분하고, 한 시점의 코인 비중을 변화 기여나 가격 움직임으로 설명하지 않는다.")
			switch envelope.CurrentAssetSnapshot.Source {
			case "OFFICIAL_DOMESTIC_24H_TICKERS":
				guidance = append(guidance, "- 현재 코인 비율은 공식 rolling 24시간 ticker를 사용한다. 직접 관측 구간이나 체결 건수로 바꾸지 않으며, 공식 snapshot의 상태와 기준 시각을 따른다.")
			case "WEBSOCKET_ROLLING_WINDOW":
				guidance = append(guidance, "- 완전한 직접 관측 window일 때만 현재 코인 비율을 확정한다. 공식 ticker와 다른 근거이며 실제 window의 관측 상태와 기준 시각을 따른다.")
			default:
				guidance = append(guidance, "- 현재 코인 자료의 source와 상태가 확인되지 않으면 근거 계층이나 수치를 추정하지 않는다.")
			}
		} else {
			guidance = append(guidance,
				"- `official_24h`가 최우선 근거다. 현재 질문이면 사용자가 지목한 거래소의 공식 rolling 24시간 ticker를 사용하고 다른 거래소 수치로 대체하지 않는다.",
				"- 현재값 질문에는 요청 거래소의 비중·거래대금과 국내 전체라는 비교 기준을 먼저 답한다. 기본 상세 해설에서는 같은 공식 표본에 이미 제공된 관련 거래소의 검증된 현재값을 비교해 요청 거래소 중심으로 현재 시장 구성을 설명한다. 순위나 격차를 새로 계산하지 않는다. 명시적으로 짧게 요청하면 비교 확장은 생략한다.",
				"- 변화 이유 질문이면 확인된 movement와 거래대금 기여만 설명한다. 변화가 확인되지 않았으면 질문의 전제를 바로잡는다.",
				"- 현재 한 시점만 있으면 현재 값의 의미·분모·시장 범위를 설명하고 변화 원인을 진단한 것처럼 쓰지 않는다. 비교 기간이 있어야 확인 가능한 항목은 한 번만 밝히며, 무관한 실시간 흐름이나 공지로 빈 분석을 채우지 않는다.",
			)
		}
	}
	if query.Asset != "" {
		guidance = append(guidance, marketQuestionAssetMetricGuidance(query.Metric))
	}

	requested := make(map[string]bool, len(query.RequestedExplanation))
	for _, item := range query.RequestedExplanation {
		requested[strings.ToUpper(strings.TrimSpace(item))] = true
	}
	if requested["PRICE"] {
		guidance = append(guidance,
			"- 사용자는 가격 등락과의 관계도 물었다. 서버 근거에 가격 시계열이나 서버 계산 상관값이 있을 때만 방향·동행성을 말한다. 없으면 가격 관계는 계산 불가라고 짧게 밝히고, 거래대금 기여를 가격 상관처럼 표현하지 않는다.",
		)
	}
	if requested["EXCHANGES"] {
		if query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.Intent != marketanalysis.MarketQuestionAssetCapture {
			guidance = append(guidance,
				"- 사용자는 거래소 구조를 요청했다. `official_24h.ticker`의 검증된 거래소별 거래대금과 비중을 비교하되, 현재 시장 구성을 과거 변화의 기여로 표현하지 않는다.",
			)
		} else {
			guidance = append(guidance,
				"- 사용자는 거래소 구조를 요청했다. 선택된 근거 계층의 거래소별 금액·비중·기여 중 실제 제공된 값을 비교한다. 다른 계층의 현재 ticker나 빗썸 기준 기여로 질문 거래소의 변화를 대신하지 않는다.",
			)
		}
	}
	if requested["DRIVERS"] {
		guidance = append(guidance,
			"- 사용자는 변화 요인을 요청했다. 서버가 제공한 양방향 코인 기여와 거래소 기여만 사용해 최종 방향을 민 힘과 반대로 완화한 힘을 분리한다. 기여 자료가 없으면 그 사실과 대신 설명 가능한 시장 구조를 구체적으로 적는다.",
		)
	}
	if requested["PATH"] {
		guidance = append(guidance,
			"- 사용자는 시간 경로를 요청했다. 전체 경로가 완전하면 고점·저점·최대 변화와 집중 구간을 연결하고, 불완전하면 공백을 건너지 않는 연속 관측 구간만 별도로 설명한다.",
		)
	}
	if requested["REALTIME"] || requested["ORDERBOOK"] || requested["NOTICES"] {
		guidance = append(guidance,
			"- `requested_context`는 사용자가 요청한 보조 맥락만 담는다. 상태가 완전한 실시간·호가 값만 쓰고, 공지는 시간/자산이 겹친 정황일 뿐 원인으로 단정하지 않는다.",
		)
	}
	if strings.EqualFold(strings.TrimSpace(query.AnalysisDepth), "DETAILED") {
		guidance = append(guidance,
			"- 상세 분석 요청이다. 질문의 하위 쟁점과 서버 근거량에 맞춰 충분히 깊게 설명한다. 형식·문단 수·글자 수를 미리 고정하지 말고, 설명력이 있는 관점을 스스로 선택해 결론과 근거, 반대 방향의 힘, 판단 가능한 범위와 불가능한 범위를 자연스럽게 연결한다.",
		)
	}
	return strings.Join(guidance, "\n")
}

func marketQuestionAssetMetricGuidance(metric marketanalysis.MarketQuestionMetric) string {
	switch metric {
	case marketanalysis.MarketQuestionMetricAssetCapture:
		return "- 선택 지표는 국내 전체의 해당 코인 거래대금 중 빗썸 몫이다. 이 비율에 먼저 답한다. 빗썸 내부 코인 비중과 바꾸지 않는다."
	case marketanalysis.MarketQuestionMetricAssetInternal:
		return "- 선택 지표는 빗썸 전체 거래대금 중 해당 코인 몫이다. 이 비율에 먼저 답한다. 국내 동일 코인의 빗썸 포착률과 바꾸지 않는다."
	case marketanalysis.MarketQuestionMetricContribution:
		return "- 선택 지표는 국내 전체 거래대금 중 빗썸의 해당 코인 몫이다. 이 비율에 먼저 답한다. 비율의 수준·변화와 별도 산술 기여도는 구분한다."
	default:
		return "- 코인 지표를 함께 물으면 국내 동일 코인 중 빗썸 몫, 빗썸 내부 코인 비중, 국내 전체 중 빗썸 해당 코인 몫을 각 분모와 함께 구분한다. 질문의 핵심 비율에 먼저 답한다."
	}
}

func validateMarketQuestionHistory(history []marketanalysis.MarketQuestionConversationTurn) error {
	if len(history) > marketanalysis.MaxMarketQuestionConversationTurns {
		return errors.New("market question history exceeds 15 turns")
	}
	totalRunes := 0
	for _, turn := range history {
		question, answer := strings.TrimSpace(turn.Question), strings.TrimSpace(turn.Answer)
		if !utf8.ValidString(question) || !utf8.ValidString(answer) ||
			utf8.RuneCountInString(question) < 2 || utf8.RuneCountInString(question) > 500 ||
			answer == "" || utf8.RuneCountInString(answer) > maxMarketQuestionHistoryAnswer {
			return errors.New("market question history contains invalid text")
		}
		totalRunes += utf8.RuneCountInString(question) + utf8.RuneCountInString(answer)
		if totalRunes > maxMarketQuestionHistoryRunes {
			return errors.New("market question history exceeds the prompt budget")
		}
	}
	return nil
}

func decodeMarketQuestionResult(raw []byte) (marketQuestionModelResult, error) {
	text, err := marketQuestionJSONObjectText(raw)
	if err != nil {
		return marketQuestionModelResult{}, fmt.Errorf("decode Hermes market question: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var result marketQuestionModelResult
	if err := decoder.Decode(&result); err != nil {
		return marketQuestionModelResult{}, fmt.Errorf("decode Hermes market question: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return marketQuestionModelResult{}, fmt.Errorf("decode Hermes market question: %w", err)
	}
	result.Answer = strings.TrimSpace(result.Answer)
	result.Caveat = strings.TrimSpace(result.Caveat)
	cleaned := make([]string, 0, min(len(result.KeyPoints), maxMarketQuestionPoints))
	seen := make(map[string]bool, maxMarketQuestionPoints)
	for _, point := range result.KeyPoints {
		point = strings.TrimSpace(point)
		if point == "" || seen[point] {
			continue
		}
		seen[point] = true
		cleaned = append(cleaned, point)
		if len(cleaned) == maxMarketQuestionPoints {
			break
		}
	}
	result.KeyPoints = cleaned
	cleanedBlocks := make([]marketanalysis.MarketQuestionClaimBlock, 0, len(result.ClaimBlocks))
	for _, block := range result.ClaimBlocks {
		block.Text = strings.TrimSpace(block.Text)
		if block.Text == "" || len(cleanedBlocks) == maxMarketQuestionPoints {
			continue
		}
		if (len(block.Claims) == 0 && result.BindingScope != "BLOCK") || len(block.Claims) > maxMarketQuestionClaimsPerBlock {
			return marketQuestionModelResult{}, errors.New("Hermes market question claim block is unbound or too large")
		}
		seenClaims := make(map[string]bool, len(block.Claims))
		for index := range block.Claims {
			claim := &block.Claims[index]
			claim.FactID = strings.TrimSpace(claim.FactID)
			claim.Relation = strings.ToUpper(strings.TrimSpace(claim.Relation))
			claim.Text = strings.TrimSpace(claim.Text)
			if claim.Text == "" && result.BindingScope == "BLOCK" {
				claim.Text = block.Text
			}
			// One typed fact can support separate, closed semantic spans in the
			// same paragraph (for example its numeric level and the definition of
			// that level). Only an exact repeated binding is malformed; the app
			// validator still evaluates every distinct span independently.
			claimKey := claim.FactID + "/" + claim.Relation + "/" + claim.Text
			if !validMarketQuestionClaimReference(*claim) || claim.Text == "" || !strings.Contains(block.Text, claim.Text) || seenClaims[claimKey] {
				return marketQuestionModelResult{}, errors.New("Hermes market question claim block contains an invalid reference")
			}
			seenClaims[claimKey] = true
		}
		block.Claims = append([]marketanalysis.MarketQuestionClaimReference(nil), block.Claims...)
		cleanedBlocks = append(cleanedBlocks, block)
	}
	result.ClaimBlocks = cleanedBlocks
	if result.Answer == "" && len(cleanedBlocks) > 0 {
		paragraphs := make([]string, 0, len(cleanedBlocks))
		for _, block := range cleanedBlocks {
			paragraphs = append(paragraphs, block.Text)
		}
		result.Answer = strings.Join(paragraphs, "\n\n")
	}
	if result.Answer == "" {
		return marketQuestionModelResult{}, errors.New("Hermes market question answer is empty")
	}
	return result, nil
}

func validMarketQuestionClaimReference(claim marketanalysis.MarketQuestionClaimReference) bool {
	if !strings.HasPrefix(claim.FactID, "fact_") || len(claim.FactID) > 101 || len(claim.FactID) <= len("fact_") {
		return false
	}
	for _, value := range strings.TrimPrefix(claim.FactID, "fact_") {
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '_' {
			return false
		}
	}
	switch claim.Relation {
	case "LEVEL", "INCREASE", "DECREASE", "UNCHANGED", "CONTRIBUTION", "COINCIDENCE_ONLY", "UNAVAILABLE":
		return true
	default:
		return false
	}
}

// Only a complete object, optionally wrapped by one complete JSON/plain code
// fence, is compatible output. Never search for braces inside logs or arrays:
// doing so would hide trailing primitive values from the caller's EOF check.
func marketQuestionJSONObjectText(raw []byte) (string, error) {
	normalized, _ := normalizeHermesUTF8(raw)
	text := strings.TrimSpace(string(normalized))
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		header := strings.ToLower(strings.TrimSpace(lines[0]))
		if len(lines) < 3 || (header != "```json" && header != "```") || strings.TrimSpace(lines[len(lines)-1]) != "```" {
			return "", errors.New("market question requires a complete JSON code fence")
		}
		text = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	if !strings.HasPrefix(text, "{") {
		return "", errors.New("market question requires one JSON object")
	}
	return text, nil
}
