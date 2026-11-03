package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/marketquestion"
	"market-share-go/internal/movement"
	"market-share-go/internal/tickertrend"
)

type marketQuestionCacheEntry struct {
	key      string
	response marketanalysis.GroundedMarketQuestionResponse
}

type marketQuestionRunOptions struct {
	strictAC bool
	scope    marketanalysis.MarketQuestionConversationScope
	emit     func(marketanalysis.MarketQuestionStageEvent) error
	state    *marketQuestionRunState
}

type marketQuestionRunState struct {
	started              time.Time
	queueStarted         time.Time
	planningStarted      time.Time
	retrievalStarted     time.Time
	serializationStarted time.Time
	explanationStarted   time.Time
	validationStarted    time.Time
	quantitative         *marketanalysis.GroundedMarketQuestionResponse
	bundle               *marketquestion.EvidenceBundleSnapshot
	calls                marketanalysis.MarketQuestionCallBudget
	timing               marketanalysis.MarketQuestionTimingMS
	sequence             int
	emitFailed           bool
}

const marketQuestionPointLimit = 10

const marketQuestionPromptPolicyVersion = "GROUNDED_MARKET_QUESTION_PROMPT_POLICY_V38"
const marketQuestionRetrievalStrategy = "TYPED_BOUNDED_SERVER_FACTS_V2"
const marketQuestionRequestTimeout = 5 * time.Minute
const marketQuestionResponseReserveMax = 5 * time.Second

type marketQuestionResponseReserveKey struct{}

type officialMovementQuestionEvidence struct {
	Direction        string                    `json:"direction"`
	State            string                    `json:"state"`
	StartedAtMS      int64                     `json:"started_at_ms"`
	EndedAtMS        *int64                    `json:"ended_at_ms"`
	BaselineSharePct float64                   `json:"baseline_share_pct"`
	CurrentSharePct  float64                   `json:"current_share_pct"`
	ChangePP         float64                   `json:"change_pp"`
	ThresholdPP      float64                   `json:"threshold_pp"`
	QualityStatus    string                    `json:"quality_status"`
	Drivers          []movement.Driver         `json:"asset_drivers"`
	ExchangeDrivers  []movement.ExchangeDriver `json:"exchange_drivers"`
	Notices          []movement.NoticeEvidence `json:"notice_context"`
}

func (s *Service) AskHermesMarketQuestion(ctx context.Context, question string) (marketanalysis.MarketQuestionAnswer, error) {
	if s == nil || !s.llmEnabled {
		return marketanalysis.MarketQuestionAnswer{}, ErrLLMDisabled
	}
	response, err := s.AskGroundedMarketQuestion(ctx, question)
	if err != nil {
		return marketanalysis.MarketQuestionAnswer{}, err
	}
	return marketQuestionAnswerFromResponse(response), nil
}

func (s *Service) AskGroundedMarketQuestion(ctx context.Context, question string) (marketanalysis.GroundedMarketQuestionResponse, error) {
	return s.AskGroundedMarketQuestionWithHistory(ctx, question, nil)
}

func (s *Service) AskGroundedMarketQuestionWithHistory(
	ctx context.Context,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
) (marketanalysis.GroundedMarketQuestionResponse, error) {
	if s == nil || !s.llmEnabled {
		return marketanalysis.GroundedMarketQuestionResponse{}, ErrLLMDisabled
	}
	callerCtx := ctx
	ctx, cancel := marketQuestionRequestContext(ctx, s.marketQuestionTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, marketQuestionResponseReserveKey{}, marketQuestionResponseReserve(callerCtx, ctx))
	if err := ctx.Err(); err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	question = strings.TrimSpace(question)
	if !utf8.ValidString(question) || len([]rune(question)) < 2 || len([]rune(question)) > 500 {
		return marketanalysis.GroundedMarketQuestionResponse{}, marketanalysis.ErrInvalidMarketQuestion
	}
	trace := s.beginMarketQuestionLearning(question, len(history))
	response, err := s.askGroundedMarketQuestionWithHistory(ctx, question, history, trace)
	if contextErr := ctx.Err(); contextErr != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, contextErr
	}
	if s.finishMarketQuestionLearning(ctx, trace, response, err) {
		response.RequestID = trace.RequestID
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, contextErr
	}
	return response, err
}

// StreamGroundedMarketQuestion publishes immutable quantitative evidence
// before starting prose generation. Once that first event is emitted, model
// timeout, cancellation or validation failure becomes an explicit terminal
// state and never discards or rewrites the quantitative payload.
func (s *Service) StreamGroundedMarketQuestion(
	ctx context.Context,
	request marketanalysis.GroundedMarketQuestionRequest,
	emit func(marketanalysis.MarketQuestionStageEvent) error,
) error {
	if s == nil || !s.llmEnabled {
		return ErrLLMDisabled
	}
	if !s.marketQuestionACEnabled {
		return marketanalysis.ErrMarketQuestionACDisabled
	}
	if emit == nil {
		return errors.New("market question stage emitter is required")
	}
	callerCtx := ctx
	ctx, cancel := marketQuestionRequestContext(ctx, s.marketQuestionTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, marketQuestionResponseReserveKey{}, marketQuestionResponseReserve(callerCtx, ctx))
	question := strings.TrimSpace(request.Question)
	if !utf8.ValidString(question) || len([]rune(question)) < 2 || len([]rune(question)) > 500 {
		return marketanalysis.ErrInvalidMarketQuestion
	}
	scope, err := s.validateMarketQuestionConversationScope(request.ConversationScope)
	if err != nil {
		return err
	}
	state := &marketQuestionRunState{started: time.Now()}
	state.calls.MaxTotal = 2
	state.timing.RequestStarted = state.started.UnixMilli()
	trace := s.beginMarketQuestionLearning(question, len(request.History))
	options := marketQuestionRunOptions{strictAC: true, scope: scope, emit: emit, state: state}
	response, runErr := s.askGroundedMarketQuestionWithHistoryAndOptions(ctx, question, request.History, trace, options)
	if state.quantitative == nil {
		return runErr
	}
	if runErr != nil {
		if state.emitFailed {
			return runErr
		}
		response = *state.quantitative
		marketQuestionSetTerminalFailure(&response, runErr, state)
		if emitErr := marketQuestionEmitStage(options, marketanalysis.MarketQuestionStageTerminal, marketQuestionStageStatusForResponse(response), response); emitErr != nil {
			return emitErr
		}
		return nil
	}
	if s.finishMarketQuestionLearning(ctx, trace, response, nil) {
		response.RequestID = trace.RequestID
	}
	if response.Orchestration != nil && response.Orchestration.ExplanationStatus != marketanalysis.MarketQuestionExplanationPending {
		if err := marketQuestionEmitStage(options, marketanalysis.MarketQuestionStageExplanationReady, marketQuestionStageStatusForResponse(response), response); err != nil {
			return err
		}
	}
	return marketQuestionEmitStage(options, marketanalysis.MarketQuestionStageTerminal, marketQuestionStageStatusForResponse(response), response)
}

// One budget includes retrieval, serialized execution, planning, answering,
// repair and optional learning persistence. A shorter caller deadline wins.
func marketQuestionRequestContext(ctx context.Context, configured ...time.Duration) (context.Context, context.CancelFunc) {
	budget := marketQuestionRequestTimeout
	if len(configured) > 0 && configured[0] > 0 {
		budget = min(configured[0], marketQuestionRequestTimeout)
	}
	return context.WithTimeout(ctx, budget)
}

// Reserve a small tail only when the Service owns the effective deadline. A
// shorter caller deadline remains authoritative and must still surface as an
// error instead of being converted into a successful fallback.
func marketQuestionResponseReserve(callerCtx, requestCtx context.Context) time.Duration {
	requestDeadline, ok := requestCtx.Deadline()
	if !ok {
		return 0
	}
	if callerDeadline, ok := callerCtx.Deadline(); ok && !requestDeadline.Before(callerDeadline) {
		return 0
	}
	remaining := time.Until(requestDeadline)
	if remaining <= 0 {
		return 0
	}
	return min(marketQuestionResponseReserveMax, remaining/10)
}

func marketQuestionProviderContext(ctx context.Context) (context.Context, context.CancelFunc) {
	reserve, _ := ctx.Value(marketQuestionResponseReserveKey{}).(time.Duration)
	deadline, ok := ctx.Deadline()
	if !ok || reserve <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, deadline.Add(-reserve))
}

// A newly generated Analysis does not refresh an older selected observation.
// Keep unknown source time unknown instead of substituting the request clock.
func marketQuestionSnapshotAsOfMS(payload marketanalysis.Analysis, query marketanalysis.MarketQuestionQuery) int64 {
	switch query.Intent {
	case marketanalysis.MarketQuestionAssetCapture:
		return currentMarketQuestionAssetBasis(payload).AsOfMS
	case marketanalysis.MarketQuestionCurrentOfficialShare:
		asOf := payload.OfficialDomestic24h.AsOf
		if !finitePositive(asOf) || asOf > 1e11 {
			return 0
		}
		return int64(asOf * 1_000)
	default:
		return int64(payload.GeneratedAt * 1_000)
	}
}

func (s *Service) askGroundedMarketQuestionWithHistory(
	ctx context.Context,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	trace *marketQuestionLearningTrace,
) (marketanalysis.GroundedMarketQuestionResponse, error) {
	return s.askGroundedMarketQuestionWithHistoryAndOptions(ctx, question, history, trace, marketQuestionRunOptions{})
}

func (s *Service) askGroundedMarketQuestionWithHistoryAndOptions(
	ctx context.Context,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	trace *marketQuestionLearningTrace,
	options marketQuestionRunOptions,
) (marketanalysis.GroundedMarketQuestionResponse, error) {
	history, err := normalizeMarketQuestionHistory(history)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	if err := validatePublicLLMQuestionScope(question, history); err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	userHistory := history
	if options.strictAC && options.scope.Contract != "" {
		history = marketQuestionHistoryWithConfirmedScope(history, options.scope)
	}
	payload, err := s.Analysis(ctx, 86_400, "KRW", 0)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	registryMarkets := []domain.MarketPair{}
	if s.intelligenceEvidence != nil {
		registryMarkets, err = s.intelligenceEvidence.RegistryMarkets(ctx)
		if err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, err
		}
	}
	if s.registry != nil {
		registryMarkets = s.registry.Universe().Markets()
	}
	asOfMS := int64(payload.GeneratedAt * 1_000)
	aliases := marketQuestionAliases(payload, registryMarkets)
	reference := time.UnixMilli(asOfMS)
	// Planning and answering share one bounded Hermes lane. Hermes chooses
	// server-declared evidence tools; Go validates the structured plan before
	// any PostgreSQL or immutable-snapshot read is executed.
	if options.state != nil {
		options.state.queueStarted = time.Now()
	}
	s.hermesQuestionMu.Lock()
	if s.hermesQuestionRunGate == nil {
		s.hermesQuestionRunGate = make(chan struct{}, 1)
	}
	runGate := s.hermesQuestionRunGate
	s.hermesQuestionMu.Unlock()
	// The lane still serializes planning, answering, and cache access, but a
	// canceled request must not remain blocked behind another model execution.
	select {
	case runGate <- struct{}{}:
		defer func() { <-runGate }()
	case <-ctx.Done():
		return marketanalysis.GroundedMarketQuestionResponse{}, ctx.Err()
	}
	if options.state != nil {
		value := time.Since(options.state.queueStarted).Milliseconds()
		options.state.timing.QueueDuration = &value
		options.state.planningStarted = time.Now()
	}
	// Both branches may be ready for an already-canceled request. Never invoke
	// the provider if cancellation won just before an otherwise free slot.
	if err := ctx.Err(); err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	releasePriority, err := hermesrunner.BeginInteractiveQuestionPriority(
		s.hermesQuestionConfig.OutputDir,
	)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	defer releasePriority()
	queryQuestion := marketQuestionWithConversationContext(question, userHistory, aliases, reference)
	signedFollowUp := options.strictAC && options.scope.Contract != "" && marketQuestionIsConversationalFollowUp(question)
	var deterministicQuery marketanalysis.MarketQuestionQuery
	var deterministicErr error
	if signedFollowUp {
		deterministicQuery, deterministicErr = marketQuestionResolveSignedFollowUp(question, options.scope.Query, aliases, reference)
	} else {
		deterministicQuery, deterministicErr = parseMarketQuestionAt(queryQuestion, aliases, reference)
	}
	if trace != nil {
		trace.DeterministicQueryJSON = marketQuestionLearningJSON(deterministicQuery)
		trace.DeterministicError = marketQuestionLearningDiagnostic(deterministicErr)
	}
	var retrievalPlan hermesrunner.MarketQuestionPlan
	query := deterministicQuery
	retrievalPlanSource := "DETERMINISTIC_EXPLICIT_OFFICIAL_WINDOW"
	deterministicCurrent := useDeterministicCurrentSnapshotPlan(
		options.strictAC, question, userHistory, options.scope, deterministicQuery, deterministicErr, reference,
	)
	if signedFollowUp && deterministicErr != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf("signed conversation scope follow-up is unsupported: %w", deterministicErr)
	}
	if signedFollowUp {
		retrievalPlanSource = "SIGNED_CONVERSATION_SCOPE"
	} else if deterministicCurrent {
		retrievalPlanSource = "DETERMINISTIC_CURRENT_SNAPSHOT"
	} else if useDeterministicCurrentAssetPlan(question, history, deterministicQuery, deterministicErr) {
		retrievalPlanSource = "DETERMINISTIC_EXPLICIT_CURRENT_ASSET"
	} else if useDeterministicOfficialEvidencePlan(question, history, deterministicQuery, deterministicErr) {
		retrievalPlanSource = "DETERMINISTIC_EXPLICIT_OFFICIAL_EVIDENCE"
	} else if !(deterministicErr == nil && (deterministicQuery.PeerExchange != "" || len(deterministicQuery.ComparedAssets) > 1)) && !useDeterministicOfficialWindowPlan(history, deterministicQuery, deterministicErr) {
		var planErr error
		if options.state != nil {
			options.state.calls.Plan++
			options.state.calls.Total++
		}
		retrievalPlan, planErr = hermesrunner.PlanMarketQuestion(
			ctx, s.marketShareQuestionConfig(), question, history, reference,
		)
		if errors.Is(planErr, hermesrunner.ErrMarketQuestionInputCapture) {
			return marketanalysis.GroundedMarketQuestionResponse{}, planErr
		}
		if err := ctx.Err(); err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, err
		}
		plannerQuery, validationErr := marketQuestionQueryFromPlan(retrievalPlan, aliases, reference)
		if planErr == nil && validationErr == nil {
			validationErr = validateMarketQuestionPlanConversationSemantics(question, queryQuestion, plannerQuery, aliases, reference)
		}
		if trace != nil {
			trace.PlannerPlanJSON = marketQuestionLearningJSON(retrievalPlan)
			trace.PlannerQueryJSON = marketQuestionLearningJSON(plannerQuery)
			trace.PlannerError = marketQuestionLearningDiagnostic(planErr)
			trace.PlanValidationError = marketQuestionLearningDiagnostic(validationErr)
			if planErr == nil && validationErr == nil && deterministicErr == nil {
				agree := marketQuestionLearningQueriesAgree(plannerQuery, deterministicQuery)
				trace.PlannerParserAgree = &agree
			}
		}
		query = plannerQuery
		retrievalPlanSource = "HERMES_STRUCTURED_PLANNER"
		if planErr != nil || validationErr != nil {
			query = deterministicQuery
			if deterministicErr != nil {
				return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf(
					"%w: planner=%v validation=%v fallback=%v", marketanalysis.ErrInvalidMarketQuestion, planErr, validationErr, deterministicErr,
				)
			}
			retrievalPlanSource = "DETERMINISTIC_PARSER_FALLBACK"
			if s.logger != nil {
				s.logger.Warn("market question retrieval planner fell back", "planner_error", planErr, "validation_error", validationErr)
			}
		}
	}
	if scopeErr := validateMarketQuestionQuery(query, reference); scopeErr != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf(
			"%w: %v", marketanalysis.ErrInvalidMarketQuestion, scopeErr,
		)
	}
	if options.state != nil {
		now := time.Now()
		completed := now.UnixMilli()
		duration := now.Sub(options.state.planningStarted).Milliseconds()
		options.state.timing.PlanningCompleted = &completed
		options.state.timing.PlanDuration = &duration
		options.state.retrievalStarted = now
	}
	query = preserveMarketQuestionExplicitPrice(question, query)
	if len(query.ComparedAssets) > 1 {
		retrievalPlanSource = "DETERMINISTIC_EXPLICIT_ASSET_COMPARISON"
	}
	if query.PeerExchange != "" {
		retrievalPlanSource = "DETERMINISTIC_EXPLICIT_COIN_PAIR"
		if query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
			retrievalPlanSource = "DETERMINISTIC_EXPLICIT_CURRENT_SHARE_PAIR"
		}
	}
	asOfMS = marketQuestionSnapshotAsOfMS(payload, query)
	if trace != nil {
		trace.AcceptedQueryJSON = marketQuestionLearningJSON(query)
		trace.PlanSource = retrievalPlanSource
	}
	officialWindow := tickertrend.WindowComparison{Status: "UNAVAILABLE", Reason: "WINDOW_NOT_REQUESTED"}
	calendarDayRequested := query.Intent == marketanalysis.MarketQuestionCalendarDayShare
	if calendarDayRequested {
		officialWindow.WindowSec = 86_400
		officialWindow.Reason = "TRACKER_NOT_CONFIGURED"
		if s.tickerTrend != nil || s.intelligenceEvidence != nil {
			officialWindow, err = s.QuestionCalendarDayClose(ctx, query.TargetStartMS, query.TargetEndMS)
			if err != nil {
				return marketanalysis.GroundedMarketQuestionResponse{}, err
			}
			if officialWindow.Current != nil {
				asOfMS = officialWindow.Current.BucketMS
			}
		}
	}
	officialWindowSec, officialWindowRequested := marketQuestionOfficialWindowSec(query)
	if officialWindowRequested {
		officialWindow.WindowSec = officialWindowSec
		officialWindow.Reason = "TRACKER_NOT_CONFIGURED"
	}
	if officialWindowRequested && (s.tickerTrend != nil || s.intelligenceEvidence != nil) {
		officialWindow, err = s.QuestionOfficialWindow(ctx, officialWindowSec)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("official window coin attribution hydration failed", "window_sec", officialWindowSec, "error", err)
			}
			if s.intelligenceEvidence != nil {
				return marketanalysis.GroundedMarketQuestionResponse{}, err
			}
			officialWindow = s.tickerTrend.CompareWindow(officialWindowSec)
		}
	}
	dailySeries := tickertrend.DailyEndpointSeries{
		ContractVersion: tickertrend.DailySeriesVersion,
		Status:          "UNAVAILABLE",
		Reason:          "DAILY_ENDPOINT_SERIES_NOT_REQUESTED",
		Points:          []tickertrend.DailyEndpointPoint{},
		Transitions:     []tickertrend.DailyEndpointTransition{},
		MissingBucketMS: []int64{}, NonLiveBucketMS: []int64{},
		IncludedExchanges: []string{"upbit", "bithumb", "coinone"},
		ExcludedExchanges: map[string]string{
			"korbit": "OFFICIAL_CANDLE_QUOTE_VOLUME_UNAVAILABLE",
			"gopax":  "OFFICIAL_CANDLE_QUOTE_VOLUME_UNAVAILABLE",
		},
	}
	dailySeriesRequested := marketQuestionDailyEndpointSeriesRequested(query)
	dailyRecentIntervalRequested := marketQuestionDailyRecentIntervalRequested(query)
	if dailySeriesRequested {
		endpointsMS, endpointErr := marketQuestionDailyEndpointBuckets(query)
		if endpointErr != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, endpointErr
		}
		dailySeries.Reason = "TRACKER_NOT_CONFIGURED"
		asOfMS = endpointsMS[len(endpointsMS)-1]
		if s.tickerTrend != nil || s.intelligenceEvidence != nil {
			dailySeries, err = s.QuestionDailyEndpoints(ctx, endpointsMS)
			if err != nil {
				return marketanalysis.GroundedMarketQuestionResponse{}, err
			}
		}
	}
	wsInterval := unavailableMarketQuestionInterval(query, asOfMS, "INTERVAL_NOT_REQUESTED")
	_, wsIntervalRequested := marketQuestionWSWindowSec(query)
	if dailyRecentIntervalRequested {
		recentQuery := query
		recentQuery.Intent = marketanalysis.MarketQuestionWSIntervalCompare
		recentQuery.Comparison = marketanalysis.MarketQuestionCurrentVsPreviousInterval
		recentQuery.TimeValue = 10
		recentQuery.TimeUnit = marketanalysis.MarketQuestionTimeMinute
		recentQuery.EndPolicy = "LATEST_CLOSED_MINUTE_BOUNDARY"
		wsInterval, err = s.marketQuestionIntervalEvidence(ctx, recentQuery, int64(payload.GeneratedAt)-1)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("daily endpoint recent interval unavailable", "error", err)
			}
			wsInterval = unavailableMarketQuestionInterval(
				recentQuery, int64(payload.GeneratedAt*1_000), "RECENT_INTERVAL_READ_FAILED",
			)
		}
	}
	if wsIntervalRequested {
		wsInterval = unavailableMarketQuestionInterval(query, asOfMS, "OFFICIAL_CANDLE_PRIMARY")
		s.addQuestionCandleFallback(ctx, query, &wsInterval)
		if wsInterval.CandleComparison == nil {
			wsInterval, err = s.marketQuestionIntervalEvidence(ctx, query, asOfMS/1_000-1)
			if err != nil {
				if s.logger != nil {
					s.logger.Warn("question WS interval unavailable", "error", err)
				}
				wsInterval = unavailableMarketQuestionInterval(query, asOfMS, "INTERVAL_READ_FAILED")
			}
		}
		asOfMS = wsInterval.AsOfMS
		s.addQuestionCandleFallback(ctx, query, &wsInterval)
	}
	historicalRange := historicalQuestionEvidence{
		Status: "UNAVAILABLE", Reason: "HISTORICAL_RANGE_NOT_REQUESTED", Source: "WEBSOCKET_10S_BASE",
		Exchanges: []historicalQuestionExchange{}, TopAssets: []historicalQuestionAsset{},
		Path: []historicalQuestionPathPoint{}, Signals10s: historicalQuestionSignals{Status: "UNAVAILABLE", Statuses: map[string]int{}},
	}
	historicalRangeRequested := query.Intent == marketanalysis.MarketQuestionHistoricalRange
	if historicalRangeRequested {
		historicalRange, err = s.marketQuestionHistoricalEvidence(ctx, query)
		if err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, err
		}
		asOfMS = query.TargetEndMS
	}
	marketQuestionConfig := s.marketShareQuestionConfig()
	providerName, providerModel := "hermes", marketQuestionConfig.Model
	auxiliaryAsOfMS := asOfMS
	if officialWindowRequested && officialWindow.Current != nil {
		auxiliaryAsOfMS = officialWindow.Current.BucketMS
	}
	driverAssets := marketQuestionNoticeDriverAssets(officialWindow, wsInterval)
	if dailySeriesRequested {
		driverAssets = append(driverAssets, marketQuestionDailyDriverAssets(dailySeries)...)
	}
	// Preserve the user's query and use current observations only as context.
	// No observation object is not a synthetic realtime measurement.
	auxQuery := query
	if payload.RealtimeFlow.AsOfMS > 0 {
		auxQuery = marketQuestionDefaultRealtimeContext(query)
	}
	auxiliary := marketQuestionAuxiliaryFacts(payload, auxQuery, auxiliaryAsOfMS, driverAssets)
	fallback := buildMarketQuestionFallbackForQuery(payload, officialWindow, wsInterval, query, asOfMS)
	if dailySeriesRequested {
		fallback = buildDailyEndpointSeriesFallback(dailySeries, query, asOfMS)
	}
	if historicalRangeRequested {
		fallback = historicalQuestionFallback(historicalRange, asOfMS)
	}
	fallback = enrichMarketQuestionAuxiliaryFacts(fallback, auxiliary)
	if !dailySeriesRequested && !historicalRangeRequested {
		fallback = appendMarketQuestionNoticeParagraph(fallback, auxiliary)
	}
	if dailySeriesRequested {
		if dailyRecentIntervalRequested {
			fallback = enrichDailyEndpointRecentIntervalAnswer(fallback, &wsInterval)
		} else {
			fallback = enrichDailyEndpointRealtimeAnswer(fallback, auxiliary)
		}
	}
	planEvidence := marketQuestionPlanEvidence(retrievalPlan, retrievalPlanSource, query)
	var evidenceEnvelope map[string]any
	if historicalRangeRequested {
		evidenceEnvelope = buildHistoricalMarketQuestionEvidence(query, historicalRange, planEvidence, fallback)
	} else {
		evidenceEnvelope = buildMarketQuestionEvidence(payload, officialWindow, wsInterval, query, auxiliary)
		if dailySeriesRequested {
			var recent *marketanalysis.MarketQuestionIntervalEvidence
			if dailyRecentIntervalRequested {
				recent = &wsInterval
				evidenceEnvelope["daily_recent_interval"] = dailyRecentIntervalFacts(recent)
			}
			evidenceEnvelope["official_daily_endpoint_series"] = dailyEndpointSeriesFactsWithRecent(dailySeries, recent)
		}
		evidenceEnvelope["retrieval_plan"] = planEvidence
		evidenceEnvelope["verified_display_briefing"] = map[string]any{
			"answer": fallback.Answer, "key_points": fallback.KeyPoints, "caveat": fallback.Caveat,
			"instruction": "Numeric values and units in this briefing are server-formatted and safe to quote verbatim.",
		}
	}
	facts := buildMarketQuestionFactLedger(payload, officialWindow, wsInterval, query, fallback)
	if wsInterval.CoinComparison != nil {
		evidenceEnvelope["server_coin_comparison"] = map[string]any{
			"facts":            wsInterval.CoinComparison,
			"rendering_policy": "Only when facts.status is CONFIRMED does the server append a verified same-window exchange and coin table. Otherwise there are no verified coin measurements or rankings: explain that limitation and do not promise a table. Do not reproduce table cells. Explain the overall comparison using verified_facts; the appendix owns coin quantities, amounts, shares, denominator, ranking scope and missing values. Do not add unrelated current rolling-24h references or infer actual fund transfers.",
		}
	}
	evidenceEnvelope["verified_facts"] = facts
	validationBriefing := fallback
	if len(facts) > 0 {
		validationBriefing = marketQuestionFactLedgerBriefing(facts)
		// The same fallback paragraphs are already in the ledger. Avoid sending
		// a second identical numeric briefing to the generation and repair calls.
		delete(evidenceEnvelope, "verified_display_briefing")
	}
	if options.state != nil {
		now := time.Now()
		value := now.Sub(options.state.retrievalStarted).Milliseconds()
		options.state.timing.RetrievalCalculation = &value
		options.state.serializationStarted = now
	}
	evidence, err := json.Marshal(evidenceEnvelope)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf("encode Hermes market evidence: %w", err)
	}
	cacheKey := marketQuestionResponseCacheKeyWithHistory(
		evidence, question, history, providerName, providerModel,
	)
	if options.strictAC {
		marketQuestionConfig.MaxTokens = hermesrunner.MaxInteractiveQuestionOutputTokens
		quantitative := buildGroundedMarketQuestionResponse(payload, officialWindow, wsInterval, query, auxiliary, fallback)
		if dailySeriesRequested {
			var recent *marketanalysis.MarketQuestionIntervalEvidence
			if dailyRecentIntervalRequested {
				recent = &wsInterval
			}
			quantitative = enrichDailyEndpointSeriesResponseWithRecent(quantitative, dailySeries, recent)
		}
		if historicalRangeRequested {
			quantitative = enrichHistoricalQuestionResponse(quantitative, historicalRange)
		}
		bundle, bundleErr := buildMarketQuestionEvidenceBundle(quantitative, question)
		if bundleErr != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf("build market question evidence bundle: %w", bundleErr)
		}
		snapshot := bundle.Snapshot()
		cacheKey, err = bundle.ExplanationCacheKey(marketquestion.ExplanationCacheIdentity{
			Language: "ko", DetailLevel: string(query.AnalysisDepth), Model: providerModel,
			PromptPolicyVersion: marketQuestionPromptPolicyVersion,
			ValidatorVersion:    marketQuestionExplanationValidatorVersion,
		})
		if err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf("build market question explanation cache identity: %w", err)
		}
		// The strict path gives the model one typed bundle plus the existing
		// display ledger used by the numeric guard. Do not repeat fallback prose,
		// auxiliary payloads or legacy evidence sections already projected into
		// the immutable bundle.
		displayValues := make([]string, 0, len(facts))
		for _, fact := range facts {
			if text := strings.TrimSpace(fact.Text); text != "" {
				displayValues = append(displayValues, text)
			}
		}
		factRelations := make(map[string]string, len(snapshot.Facts))
		for _, fact := range snapshot.Facts {
			factRelations[fact.ID] = marketQuestionEvidenceFactRelation(fact)
		}
		strictEvidenceEnvelope := map[string]any{
			"query": query, "retrieval_plan": planEvidence,
			"typed_evidence_bundle": snapshot, "verified_display_values": displayValues, "fact_relations": factRelations,
		}
		evidence, err = json.Marshal(strictEvidenceEnvelope)
		if err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, fmt.Errorf("encode typed Hermes market evidence: %w", err)
		}
		if options.state != nil {
			now := time.Now()
			value := now.Sub(options.state.serializationStarted).Milliseconds()
			bytes := int64(len(evidence))
			options.state.timing.SerializationDuration = &value
			options.state.timing.EvidenceBytes = &bytes
		}
		if err := s.marketQuestionPublishQuantitative(ctx, question, quantitative, bundle, options); err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, err
		}
	}

	if !s.marketQuestionCaptureInput && s.hermesQuestionCache.key == cacheKey &&
		marketQuestionProviderAnswerCacheable(s.hermesQuestionCache.response.Provider.Source) {
		if trace != nil {
			trace.AnswerGuardStatus = "CACHE_HIT"
		}
		if options.strictAC && options.state != nil && options.state.quantitative != nil && s.hermesQuestionCache.response.Orchestration != nil {
			now := time.Now()
			total := now.Sub(options.state.started).Milliseconds()
			options.state.timing.Total = &total
			cached, cacheErr := marketQuestionApplyCachedExplanation(*options.state.quantitative, s.hermesQuestionCache.response, options.state)
			return cached, cacheErr
		}
		return s.hermesQuestionCache.response, nil
	}

	var answer marketanalysis.MarketQuestionAnswer
	var askErr error
	providerCtx, cancelProvider := marketQuestionProviderContext(ctx)
	defer cancelProvider()
	strictGuardStatus := marketanalysis.MarketQuestionValidationSupported
	strictGuardReason := ""
	if options.state != nil {
		options.state.explanationStarted = time.Now()
		started := options.state.explanationStarted.UnixMilli()
		options.state.timing.ExplanationStarted = &started
		options.state.calls.Explanation++
		options.state.calls.Total++
	}
	answer, askErr = hermesrunner.AskMarketQuestionWithHistory(
		providerCtx, marketQuestionConfig, question, history, evidence, asOfMS,
	)
	if options.state != nil {
		now := time.Now()
		completed := now.UnixMilli()
		duration := now.Sub(options.state.explanationStarted).Milliseconds()
		options.state.timing.ExplanationCompleted = &completed
		options.state.timing.ModelDuration = &duration
	}
	if errors.Is(askErr, hermesrunner.ErrMarketQuestionInputCapture) {
		return marketanalysis.GroundedMarketQuestionResponse{}, askErr
	}
	if options.strictAC && (errors.Is(askErr, context.DeadlineExceeded) || errors.Is(askErr, context.Canceled)) {
		return marketanalysis.GroundedMarketQuestionResponse{}, askErr
	}
	if err := ctx.Err(); err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	if askErr == nil && dailySeriesRequested {
		answer = groundDailyEndpointProviderAnswer(answer, fallback)
	}
	if askErr != nil {
		strictGuardStatus = marketanalysis.MarketQuestionValidationUndetermined
		strictGuardReason = "MODEL_EXPLANATION_FAILED"
		if trace != nil {
			trace.AnswerGuardStatus = "PROVIDER_ERROR_FALLBACK"
		}
		if s.logger != nil {
			s.logger.Warn("market question provider fell back to server briefing", "provider", providerName, "error", askErr)
		}
		answer = fallback
	} else if !options.strictAC {
		var referencesAllowed bool
		answer, referencesAllowed = resolveMarketQuestionFactReferences(answer, facts)
		numbersAllowed := marketQuestionAnswerNumbersAllowed(answer, validationBriefing)
		namedTermsAllowed := marketQuestionAnswerNamedTermsAllowed(answer, evidence)
		factBindingsAllowed := referencesAllowed && marketQuestionAnswerFactBindingsAllowed(answer, facts, query)
		questionComplete := marketQuestionAnswerCompletenessAllowed(answer, facts, query, question)
		safetyAllowed := marketQuestionAnswerSafetyAllowedWithDaily(answer, query, officialWindow, dailySeries, wsInterval, payload)
		dailyStructureComplete := !dailySeriesRequested || marketQuestionDailyAnswerCompleteWithRecent(answer, dailySeries, dailyRecentIntervalRequested)
		// Daily grounding can discard the whole provider body. A valid server
		// briefing is not a successful model answer: offer one repair and retain
		// fallback provenance/cache isolation if no grounded model prose remains.
		providerProseRetained := !dailySeriesRequested || marketQuestionProviderAnswerCacheable(answer.Source)
		if !numbersAllowed || !namedTermsAllowed || !factBindingsAllowed || !safetyAllowed || !questionComplete || !dailyStructureComplete || !providerProseRetained {
			if !numbersAllowed || !namedTermsAllowed || !referencesAllowed || !factBindingsAllowed || !safetyAllowed {
				strictGuardStatus = marketanalysis.MarketQuestionValidationConflict
				strictGuardReason = "EXISTING_FACT_GUARD_CONFLICT"
			} else {
				strictGuardStatus = marketanalysis.MarketQuestionValidationUndetermined
				strictGuardReason = "EXISTING_COMPLETENESS_GUARD_UNDETERMINED"
			}
			if s.logger != nil {
				s.logger.Warn(
					"market question provider claims rejected",
					"provider", providerName,
					"numbers_allowed", numbersAllowed,
					"rejected_numeric_claims", marketQuestionRejectedNumericClaims(answer, validationBriefing),
					"named_terms_allowed", namedTermsAllowed,
					"fact_bindings_allowed", factBindingsAllowed,
					"question_complete", questionComplete,
					"safety_allowed", safetyAllowed,
					"daily_structure_complete", dailyStructureComplete,
					"provider_prose_retained", providerProseRetained,
				)
			}

			if options.strictAC {
				if trace != nil {
					trace.AnswerGuardStatus = "STRICT_BLOCK_VALIDATION_REQUIRED"
				}
			} else {
				// Give the prose provider one bounded repair opportunity. The
				// same server display-fact ledger remains the numeric allow-list, and
				// named terms are rechecked against the original evidence rather than
				// the rejected draft embedded in the repair request.
				repairEnvelope := make(map[string]any, len(evidenceEnvelope)+1)
				for key, value := range evidenceEnvelope {
					repairEnvelope[key] = value
				}
				repairEnvelope["repair_request"] = map[string]any{
					"draft": map[string]any{
						"answer": answer.Answer, "key_points": answer.KeyPoints, "caveat": answer.Caveat,
					},
					"failed_checks": map[string]any{
						"numbers": !numbersAllowed, "rejected_numeric_claims": marketQuestionRejectedNumericClaims(answer, validationBriefing),
						"named_terms": !namedTermsAllowed, "safety_or_direction": !safetyAllowed,
						"fact_bindings":           !factBindingsAllowed,
						"question_completeness":   !questionComplete,
						"required_structure":      !dailyStructureComplete,
						"provider_prose_retained": !providerProseRetained,
					},
					"instruction": "Rewrite the full answer from verified facts; do not explain the repair.",
				}
				repairEvidence, repairEncodeErr := json.Marshal(repairEnvelope)
				var repaired marketanalysis.MarketQuestionAnswer
				var repairErr error
				if repairEncodeErr != nil {
					repairErr = repairEncodeErr
				} else {
					repaired, repairErr = hermesrunner.AskMarketQuestionWithHistory(
						providerCtx, marketQuestionConfig, question, history, repairEvidence, asOfMS,
					)
				}
				if errors.Is(repairErr, hermesrunner.ErrMarketQuestionInputCapture) {
					return marketanalysis.GroundedMarketQuestionResponse{}, repairErr
				}
				if repairErr == nil && dailySeriesRequested {
					repaired = groundDailyEndpointProviderAnswer(repaired, fallback)
				}
				if err := ctx.Err(); err != nil {
					return marketanalysis.GroundedMarketQuestionResponse{}, err
				}
				var repairReferencesAllowed bool
				repaired, repairReferencesAllowed = resolveMarketQuestionFactReferences(repaired, facts)
				repairAccepted := repairErr == nil && repairReferencesAllowed &&
					(!dailySeriesRequested || marketQuestionProviderAnswerCacheable(repaired.Source)) &&
					marketQuestionAnswerNumbersAllowed(repaired, validationBriefing) &&
					marketQuestionAnswerNamedTermsAllowed(repaired, evidence) &&
					marketQuestionAnswerFactBindingsAllowed(repaired, facts, query) &&
					marketQuestionAnswerCompletenessAllowed(repaired, facts, query, question) &&
					marketQuestionAnswerSafetyAllowedWithDaily(repaired, query, officialWindow, dailySeries, wsInterval, payload) &&
					(!dailySeriesRequested || marketQuestionDailyAnswerCompleteWithRecent(repaired, dailySeries, dailyRecentIntervalRequested))
				if repairAccepted {
					answer = repaired
					if trace != nil {
						trace.AnswerGuardStatus = "PROVIDER_REPAIR_ACCEPTED"
					}
				} else {
					answer = fallback
					if trace != nil {
						trace.AnswerGuardStatus = "CLAIMS_REJECTED_FALLBACK"
					}
					if s.logger != nil {
						s.logger.Warn("market question provider repair rejected", "provider", providerName, "error", repairErr)
					}
				}
			}
		} else {
			if trace != nil {
				trace.AnswerGuardStatus = "PROVIDER_ACCEPTED"
			}
			if answer.Source == "HERMES_SOL_FALLBACK" && s.logger != nil {
				s.logger.Warn(
					"Hermes market question used Sol fallback",
					"model", answer.Model,
					"reasoning", "medium",
				)
			}
		}
	}
	if !options.strictAC {
		if officialWindowRequested {
			answer = enrichOfficialHourCoinImpacts(answer, officialWindow, query.Asset)
		}
		if wsIntervalRequested {
			answer = enrichWSIntervalFacts(answer, wsInterval)
			if query.PeerExchange == "" {
				answer = enrichTargetExchangeCurrentTickerReference(answer, payload, query)
			}
		}
		answer = enrichCurrentMarketQuestionAssetFacts(answer, payload, query)
		answer = enrichMarketQuestionIdentityWarning(answer, payload, officialWindow, wsInterval, query)
		answer = enrichMarketQuestionAuxiliaryFacts(answer, auxiliary)
		if c := wsInterval.CandleComparison; c != nil {
			answer.Answer += fmt.Sprintf("\n\n비교에 포함된 동일 종목: 업비트 %d개 · 빗썸 %d개 · 코인원 %d개. 두 구간 모두 봉이 있는 종목만 포함한 부분 시장 비교입니다.", c.MarketCounts["upbit"], c.MarketCounts["bithumb"], c.MarketCounts["coinone"])
		}
		if report := marketQuestionCoinComparisonReport(wsInterval.CoinComparison); report != "" {
			answer.Answer = report + "\n\n" + answer.Answer
		}
	}
	response := buildGroundedMarketQuestionResponse(payload, officialWindow, wsInterval, query, auxiliary, answer)
	if dailySeriesRequested {
		var recent *marketanalysis.MarketQuestionIntervalEvidence
		if dailyRecentIntervalRequested {
			recent = &wsInterval
		}
		response = enrichDailyEndpointSeriesResponseWithRecent(response, dailySeries, recent)
	}
	if historicalRangeRequested {
		response = enrichHistoricalQuestionResponse(response, historicalRange)
	}
	if options.strictAC && options.state != nil && options.state.quantitative != nil && options.state.bundle != nil {
		options.state.validationStarted = time.Now()
		filtered, blocks, validationStatus := marketQuestionValidateExplanationBlocks(answer, *options.state.bundle)
		if askErr == nil && validationStatus != marketanalysis.MarketQuestionValidationSupported && marketQuestionProviderAnswerCacheable(answer.Source) {
			_, paragraphs := marketQuestionReviewInput(answer, *options.state.bundle)
			if len(paragraphs) > 0 {
				options.state.calls.SemanticValidation++
				options.state.calls.Total++
				options.state.calls.MaxTotal = max(options.state.calls.MaxTotal, options.state.calls.Total)
				verdicts, reviewErr := marketQuestionSemanticReview(providerCtx, marketQuestionConfig, answer, *options.state.bundle)
				if reviewErr == nil {
					filtered, blocks, validationStatus = marketQuestionApplySemanticReview(answer, *options.state.bundle, verdicts)
				} else if s.logger != nil {
					s.logger.Warn("market question semantic review unavailable", "error", reviewErr)
				}
			}
		}
		validationStatus = marketQuestionMergeValidationStatus(validationStatus, strictGuardStatus)
		complete := marketQuestionAnswerCompletenessAllowed(filtered, facts, query, question)
		if (query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot || query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta) && query.Asset == "" && len(query.ComparedAssets) == 0 && query.PeerExchange == "" &&
			(query.Metric == marketanalysis.MarketQuestionMetricOverallShare || query.Metric == marketanalysis.MarketQuestionMetricTargetExchange) {
			complete = marketQuestionTypedExchangeExplanationComplete(blocks, *options.state.bundle, question)
		}
		if strings.TrimSpace(filtered.Answer) != "" &&
			(!complete || !marketQuestionRetainedExplanationCoversRequestedParts(filtered.Answer, query)) {
			validationStatus = marketQuestionMergeValidationStatus(validationStatus, marketanalysis.MarketQuestionValidationUndetermined)
			blocks = append(blocks, marketanalysis.MarketQuestionExplanationBlock{
				Index: len(blocks), Status: marketanalysis.MarketQuestionValidationUndetermined,
				Reason: "RETAINED_EXPLANATION_DOES_NOT_FULFILL_QUESTION", ClaimRefs: []string{},
			})
		}
		if strictGuardStatus == marketanalysis.MarketQuestionValidationConflict {
			filtered = marketanalysis.MarketQuestionAnswer{Source: answer.Source, Model: answer.Model, AsOfMS: answer.AsOfMS}
			blocks = append(blocks, marketanalysis.MarketQuestionExplanationBlock{
				Index: len(blocks), Status: strictGuardStatus, Reason: strictGuardReason, ClaimRefs: []string{},
			})
		} else if strictGuardStatus == marketanalysis.MarketQuestionValidationUndetermined && strictGuardReason != "" {
			blocks = append(blocks, marketanalysis.MarketQuestionExplanationBlock{
				Index: len(blocks), Status: strictGuardStatus, Reason: strictGuardReason, ClaimRefs: []string{},
			})
		}
		response, err = marketQuestionCloneResponse(*options.state.quantitative)
		if err != nil {
			return marketanalysis.GroundedMarketQuestionResponse{}, err
		}
		response.Provider = marketanalysis.MarketQuestionProvider{
			Source: answer.Source, Model: answer.Model,
			FallbackUsed: strings.Contains(strings.ToUpper(answer.Source), "FALLBACK"), AsOfMS: answer.AsOfMS,
		}
		response.Prose = marketanalysis.MarketQuestionProse{
			Answer: filtered.Answer, KeyPoints: filtered.KeyPoints, Caveat: filtered.Caveat, Blocks: blocks,
		}
		explanationStatus := marketanalysis.MarketQuestionExplanationAccepted
		overallStatus := marketanalysis.MarketQuestionOverallComplete
		if validationStatus != marketanalysis.MarketQuestionValidationSupported {
			overallStatus = marketanalysis.MarketQuestionOverallPartial
			if strings.TrimSpace(filtered.Answer) != "" {
				explanationStatus = marketanalysis.MarketQuestionExplanationPartial
			} else if validationStatus == marketanalysis.MarketQuestionValidationConflict {
				explanationStatus = marketanalysis.MarketQuestionExplanationFailed
			} else {
				explanationStatus = marketanalysis.MarketQuestionExplanationUnverified
			}
		}
		if response.EvidenceBundle != nil && response.EvidenceBundle.Fulfillment.Status != marketquestion.FulfillmentFulfilled {
			overallStatus = marketanalysis.MarketQuestionOverallPartial
		}
		now := time.Now()
		completed := now.UnixMilli()
		validationDuration := now.Sub(options.state.validationStarted).Milliseconds()
		total := now.Sub(options.state.started).Milliseconds()
		options.state.timing.ValidationCompleted = &completed
		options.state.timing.ValidationDuration = &validationDuration
		options.state.timing.Total = &total
		response.Orchestration = &marketanalysis.MarketQuestionOrchestration{
			Mode: marketQuestionACMode, OverallStatus: overallStatus,
			QuantitativeStatus: marketQuestionQuantitativeStatus(response), ExplanationStatus: explanationStatus,
			ValidationStatus: validationStatus, Blocks: blocks,
			Calls: options.state.calls, TimingMS: options.state.timing,
		}
	}
	if err := ctx.Err(); err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	cacheValidated := !options.strictAC || (response.Orchestration != nil &&
		(response.Orchestration.ExplanationStatus == marketanalysis.MarketQuestionExplanationAccepted ||
			response.Orchestration.ExplanationStatus == marketanalysis.MarketQuestionExplanationPartial))
	if cacheValidated && marketQuestionProviderAnswerCacheable(response.Provider.Source) {
		s.hermesQuestionCache = marketQuestionCacheEntry{key: cacheKey, response: response}
	}
	return response, nil
}

// useDeterministicCurrentSnapshotPlan skips the planning model only when the
// existing parser and catalog have already resolved one explicit, first-turn
// current level query. Follow-ups, comparisons, causal requests and unsupported
// combinations keep the structured planner/admission path.
func useDeterministicCurrentSnapshotPlan(
	strictAC bool,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	scope marketanalysis.MarketQuestionConversationScope,
	query marketanalysis.MarketQuestionQuery,
	parseErr error,
	reference time.Time,
) bool {
	if !strictAC || parseErr != nil || len(history) != 0 || scope.Contract != "" ||
		!marketQuestionExplicitCurrentFollowUp(question) || marketQuestionReferencesPriorContext(question) ||
		marketQuestionRequestsNotices(question) || marketQuestionAsksMovement(question) {
		return false
	}
	if query.Intent != marketanalysis.MarketQuestionCurrentOfficialShare ||
		query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot || query.Exchange == "" ||
		query.Asset != "" || query.PeerExchange != "" ||
		query.TimeValue != 0 || query.TimeUnit != marketanalysis.MarketQuestionTimeNone ||
		query.MarketScope != "KRW" || query.EndPolicy != "LATEST_CLOSED_BOUNDARY" || len(query.RequestedExplanation) != 0 {
		return false
	}
	if query.Metric != marketanalysis.MarketQuestionMetricOverallShare && query.Metric != marketanalysis.MarketQuestionMetricTargetExchange {
		return false
	}
	return validateMarketQuestionQuery(query, reference) == nil
}

// useDeterministicOfficialWindowPlan keeps explicit first-turn official-window
// questions on the typed parser path. Their range, comparison and evidence
// family are already unambiguous, so a second model call cannot add meaning;
// skipping it also leaves the public reverse-proxy response budget to Sol.
// Follow-up turns and every other intent still use the structured planner.
func useDeterministicOfficialWindowPlan(
	history []marketanalysis.MarketQuestionConversationTurn,
	query marketanalysis.MarketQuestionQuery,
	parseErr error,
) bool {
	if parseErr != nil || len(history) != 0 {
		return false
	}
	return query.Intent == marketanalysis.MarketQuestionOfficialWindowChange &&
		query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta &&
		query.Metric == marketanalysis.MarketQuestionMetricOverallShare &&
		(query.Exchange == "" || query.Exchange == string(domain.ExchangeBithumb))
}

func marketQuestionProviderAnswerCacheable(source string) bool {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "HERMES", "HERMES_SOL_FALLBACK":
		return true
	default:
		return false
	}
}

func marketQuestionResponseCacheKey(evidence []byte, provider, model string) string {
	return marketQuestionResponseCacheKeyWithHistory(evidence, "", nil, provider, model)
}

func marketQuestionResponseCacheKeyWithHistory(
	evidence []byte,
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	provider, model string,
) string {
	digestInput, _ := json.Marshal(struct {
		Evidence json.RawMessage                                 `json:"evidence"`
		Question string                                          `json:"question"`
		History  []marketanalysis.MarketQuestionConversationTurn `json:"history,omitempty"`
	}{
		Evidence: append(json.RawMessage(nil), evidence...),
		Question: strings.TrimSpace(question),
		History:  history,
	})
	digest := sha256.Sum256(digestInput)
	return fmt.Sprintf(
		"%s:%s:%s:%x",
		marketQuestionPromptPolicyVersion, strings.ToLower(strings.TrimSpace(provider)),
		strings.TrimSpace(model), digest,
	)
}

func (s *Service) marketShareQuestionConfig() hermesrunner.MarketQuestionConfig {
	config := s.hermesQuestionConfig
	config.Model = hermesrunner.MarketQuestionModel
	config.MaxTokens = hermesrunner.MaxMarketQuestionOutputTokens
	config.Temperature = hermesrunner.MarketQuestionTemperature
	config.CallTimeout = s.marketQuestionTimeout
	config.CaptureInput = s.marketQuestionCaptureInput
	return config
}

func marketQuestionAnswerFromResponse(response marketanalysis.GroundedMarketQuestionResponse) marketanalysis.MarketQuestionAnswer {
	return marketanalysis.MarketQuestionAnswer{
		Answer: response.Prose.Answer, KeyPoints: response.Prose.KeyPoints, Caveat: response.Prose.Caveat,
		Source: response.Provider.Source, Model: response.Provider.Model, AsOfMS: response.Provider.AsOfMS,
	}
}

func buildMarketQuestionEvidence(
	payload marketanalysis.Analysis,
	officialWindow tickertrend.WindowComparison,
	wsInterval marketanalysis.MarketQuestionIntervalEvidence,
	query marketanalysis.MarketQuestionQuery,
	auxiliary *marketanalysis.MarketQuestionAuxiliaryFacts,
) map[string]any {
	monitor := payload.OfficialShareMonitor
	event := monitor.Movement
	if event == nil && len(monitor.RecentEvents) > 0 {
		event = &monitor.RecentEvents[0]
	}
	var movementEvidence *officialMovementQuestionEvidence
	if event != nil {
		notices := marketMovementNotices(event.Notices)
		movementEvidence = &officialMovementQuestionEvidence{
			Direction: event.Direction, State: event.State,
			StartedAtMS: event.StartedAtMS, EndedAtMS: event.EndedAtMS,
			BaselineSharePct: event.BaselineSharePct, CurrentSharePct: event.CurrentSharePct,
			ChangePP: event.ChangePP, ThresholdPP: event.ThresholdPP,
			QualityStatus:   event.QualityStatus,
			Drivers:         marketQuestionMovementDrivers(event.Drivers, notices, 5),
			ExchangeDrivers: boundedMovementExchangeDrivers(event.ExchangeDrivers, 5),
			Notices:         notices,
		}
	}
	result := map[string]any{
		"contract": "GROUNDED_MARKET_QUESTION_V1",
		"as_of_ms": marketQuestionSnapshotAsOfMS(payload, query),
		"query":    query,
		"retrieval_policy": map[string]any{
			"strategy":                        marketQuestionRetrievalStrategy,
			"persistent_store":                "POSTGRESQL",
			"model_generated_sql":             false,
			"raw_database_rows_sent_to_model": false,
		},
	}
	currentAsset := currentMarketQuestionAssetEvidence(payload, query.Asset)
	if len(query.ComparedAssets) > 1 {
		items := []map[string]any{}
		for _, asset := range query.ComparedAssets {
			items = append(items, currentMarketQuestionAssetEvidence(payload, asset))
		}
		currentAsset = map[string]any{"compared_assets": items, "rank_among_confirmed_assets": currentMarketQuestionComparedAssetRanks(payload, query), "comparison_scope": "same selected snapshot and period; server ranks only confirmed requested amounts, equal amounts share a rank; missing assets are not smaller"}
	}
	official24h := map[string]any{
		"ticker":             payload.OfficialDomestic24h,
		"current":            monitor.Current,
		"selected_movement":  movementEvidence,
		"event_threshold_pp": monitor.DataQuality["event_threshold_pp"],
	}
	officialCalendar := map[string]any{
		"requested": calendarMarketQuestionRequested(query), "target_date": query.TargetDate,
		"start_ms": query.TargetStartMS, "end_ms": query.TargetEndMS, "close": officialWindow,
		"semantics": "KST_CALENDAR_DAY_SUM_OF_24_EXACT_ALIGNED_HOURLY_CANDLES",
	}
	websocketRolling := map[string]any{
		"rolling_24h":  payload.Rolling24h.Rolling24h,
		"movement":     payload.Rolling24h.Movement,
		"briefing":     payload.Rolling24h.Briefing,
		"data_quality": payload.Rolling24h.DataQuality,
	}

	// Only send the evidence family selected by the deterministic query plan.
	// This keeps broad product knowledge in code while the model sees a compact,
	// question-specific fact packet instead of every available market layer.
	switch query.Comparison {
	case marketanalysis.MarketQuestionCalendarDayClose:
		result["official_calendar_day"] = officialCalendar
	case marketanalysis.MarketQuestionRolling24hEndpointDelta:
		result["official_window"] = officialWindow
	case marketanalysis.MarketQuestionCurrentVsPreviousInterval:
		result["websocket_interval"] = wsInterval
		if wsInterval.CandleComparison != nil {
			result["candle_comparison_policy"] = "Use the official candle comparison as the primary available numerical analysis, always qualified as 동일 종목 묶음 기준. It is a fixed subset with full candles in both windows, NOT the whole market. State both actual KST windows and the denominator. Candles do not establish WS completeness. Explain the computed comparison without inventing missing markets. Realtime context is separate supplementary evidence, never added to candle amounts."
		}
	case marketanalysis.MarketQuestionDailyRolling24hEndpoints:
		// The bounded daily series is attached by the caller after this common envelope is built.
	default:
		switch query.Intent {
		case marketanalysis.MarketQuestionAssetCapture:
			result["current_asset_snapshot"] = currentAsset
		case marketanalysis.MarketQuestionMovementExplanation:
			result["official_24h"] = official24h
			result["websocket_rolling_24h"] = websocketRolling
		default:
			result["official_24h"] = official24h
		}
	}
	if auxiliary != nil {
		result["requested_context"] = auxiliary
		result["evidence_composition_policy"] = "Use official ticker or candle facts for the main level and period analysis. Explain verified COMPLETE 10-second results from requested_context.realtime as separate latest market context, with their actual observation time. Never infer 10-second values from candles or sum different windows. Unavailable realtime context does not invalidate official facts; explain only its missing part."
	}
	return result
}

func marketQuestionAliases(payload marketanalysis.Analysis, markets []domain.MarketPair) map[string]string {
	aliases := map[string]string{
		"비트코인": "BTC", "이더리움": "ETH", "리플": "XRP",
		"솔라나": "SOL", "도지코인": "DOGE", "에이다": "ADA",
	}
	for _, asset := range payload.Assets {
		symbol := strings.ToUpper(strings.TrimSpace(asset.Asset))
		if symbol != "" {
			aliases[symbol] = symbol
		}
	}
	for _, market := range markets {
		if market.Scope != domain.ScopeDomestic || !strings.EqualFold(market.Quote, "KRW") {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(market.Base))
		if symbol != "" {
			aliases[symbol] = symbol
		}
	}
	return aliases
}

func marketQuestionOfficialWindowSec(query marketanalysis.MarketQuestionQuery) (int64, bool) {
	if query.Comparison != marketanalysis.MarketQuestionRolling24hEndpointDelta || query.TimeValue <= 0 {
		return 0, false
	}
	switch query.TimeUnit {
	case marketanalysis.MarketQuestionTimeHour:
		return query.TimeValue * 3_600, true
	case marketanalysis.MarketQuestionTimeMinute:
		if query.TimeValue%60 == 0 {
			return query.TimeValue / 60 * 3_600, true
		}
	}
	return 0, false
}

func buildMarketQuestionFallback(
	payload marketanalysis.Analysis,
	officialHour tickertrend.WindowComparison,
	question string,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	query, err := parseMarketQuestion(question, nil)
	if err == nil {
		if _, requested := marketQuestionOfficialWindowSec(query); requested {
			return buildOfficialHourFallback(officialHour, asOfMS)
		}
	}
	return buildMarketQuestionFallbackForQuery(payload, officialHour, unavailableMarketQuestionInterval(query, asOfMS, "INTERVAL_NOT_REQUESTED"), query, asOfMS)
}

func buildMarketQuestionFallbackForQuery(
	payload marketanalysis.Analysis,
	officialWindow tickertrend.WindowComparison,
	wsInterval marketanalysis.MarketQuestionIntervalEvidence,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	if query.Intent == marketanalysis.MarketQuestionCalendarDayShare {
		return buildCalendarDayShareFallback(officialWindow, query, asOfMS)
	}
	if query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
		return buildCurrentOfficialShareFallbackForQuery(payload, query, asOfMS)
	}
	if query.Intent == marketanalysis.MarketQuestionDataQuality {
		return buildMarketQuestionDataQualityFallback(payload, asOfMS)
	}
	if query.Intent == marketanalysis.MarketQuestionAssetCapture && query.Asset != "" {
		return buildCurrentMarketQuestionAssetFallback(payload, query, asOfMS)
	}
	if _, requested := marketQuestionWSWindowSec(query); requested {
		if query.PeerExchange != "" {
			return buildWSIntervalFallback(wsInterval, asOfMS)
		}
		return enrichTargetExchangeCurrentTickerReference(buildWSIntervalFallback(wsInterval, asOfMS), payload, query)
	}
	if _, requested := marketQuestionOfficialWindowSec(query); requested {
		return enrichOfficialHourCoinImpacts(buildOfficialHourFallback(officialWindow, asOfMS), officialWindow, query.Asset)
	}
	monitor := payload.OfficialShareMonitor
	event := monitor.Movement
	if event == nil && len(monitor.RecentEvents) > 0 {
		event = &monitor.RecentEvents[0]
	}
	paragraphs := []string{}
	points := []string{}
	if event != nil {
		direction := "상승"
		if event.Direction == "DOWN" || event.ChangePP < 0 {
			direction = "하락"
		}
		paragraphs = append(paragraphs, fmt.Sprintf(
			"공식 24시간 점유율은 기준 %.2f%%에서 %.2f%%로 %+.2f%%p %s한 이벤트가 관측됐습니다.",
			event.BaselineSharePct, event.CurrentSharePct, event.ChangePP, direction,
		))
		measurementDetails := []string{}
		if event.ThresholdPP > 0 {
			measurementDetails = append(measurementDetails, fmt.Sprintf("이벤트 판정 기준은 %.2f%%p", event.ThresholdPP))
		}
		if strings.TrimSpace(event.QualityStatus) != "" {
			measurementDetails = append(measurementDetails, "자료 품질 상태는 "+event.QualityStatus)
		}
		if len(measurementDetails) > 0 {
			paragraphs = append(paragraphs, strings.Join(measurementDetails, "이고 ")+"입니다. 이 값은 업비트·빗썸·코인원의 동일 UTC 종료시각 1시간봉 24개 합계를 비교한 결과입니다.")
		}

		assetDetails := []string{}
		for _, driver := range boundedMovementDrivers(event.Drivers, 3) {
			contributionDirection := "상승 기여"
			if driver.ContributionPP < 0 {
				contributionDirection = "하락 기여"
			}
			assetDetails = append(assetDetails, fmt.Sprintf(
				"%s는 빗썸 거래대금 %s, 경쟁 거래소 거래대금 %s 변화와 함께 %+.2f%%p의 %s를 보였습니다",
				driver.Asset, formatQuestionKRW(driver.BithumbVolumeChangeKRW),
				formatQuestionKRW(driver.CompetitorVolumeChangeKRW), driver.ContributionPP, contributionDirection,
			))
			points = append(points, marketQuestionDriverPoint(driver, contributionDirection))
		}
		if len(assetDetails) > 0 {
			paragraphs = append(paragraphs, "코인별 산술 분해에서는 "+strings.Join(assetDetails, "; ")+". 기여도 부호는 각 코인이 빗썸 점유율을 어느 방향으로 움직였는지를 뜻합니다.")
		} else {
			paragraphs = append(paragraphs, "현재 이벤트에는 코인별 기여 자료가 없어 거래소 전체 변화보다 더 세부적으로 분해할 수 없습니다.")
		}

		exchangeDetails := []string{}
		for _, driver := range boundedMovementExchangeDrivers(event.ExchangeDrivers, 3) {
			label := rollingExchangeLabel(driver.Exchange)
			exchangeDetails = append(exchangeDetails, fmt.Sprintf(
				"%s 거래대금은 %s 변했고 빗썸 점유율에 대한 산술 기여는 %+.2f%%p였습니다",
				label, formatQuestionKRW(driver.VolumeChangeKRW), driver.ContributionPP,
			))
			points = append(points, fmt.Sprintf(
				"%s 거래대금 %s · 빗썸 점유율 기여 %+.2f%%p",
				label, formatQuestionKRW(driver.VolumeChangeKRW), driver.ContributionPP,
			))
		}
		if len(exchangeDetails) > 0 {
			paragraphs = append(paragraphs, "거래소별로 보면 "+strings.Join(exchangeDetails, "; ")+".")
		}

		noticeDetails := []string{}
		for _, item := range marketMovementNotices(event.Notices) {
			label := rollingExchangeLabel(item.Exchange)
			noticeDetails = append(noticeDetails, fmt.Sprintf("%s의 ‘%s’", label, item.Title))
		}
		if len(noticeDetails) > 0 {
			paragraphs = append(paragraphs, "같은 시기 또는 관련 자산 기준으로 "+strings.Join(noticeDetails, ", ")+" 공지가 함께 포착됐습니다. 다만 시간이나 자산이 겹친다는 사실만으로 해당 공지가 점유율 변화의 원인이라고 단정할 수는 없습니다.")
		}
	} else if monitor.Current != nil {
		threshold := 2.0
		if value, ok := monitor.DataQuality["event_threshold_pp"].(float64); ok && value > 0 {
			threshold = value
		}
		paragraphs = append(paragraphs, fmt.Sprintf(
			"현재 공식 24시간 빗썸 점유율은 %.2f%%이며, 연속 기준점 대비 -%.0f%%p 하락 이벤트는 확인되지 않았습니다. 따라서 현재 공식 자료만으로는 하락을 전제로 원인을 설명할 수 없습니다.",
			monitor.Current.BithumbSharePct, threshold,
		))
		paragraphs = append(paragraphs, fmt.Sprintf(
			"현재 국내 공식 24시간 총 거래대금은 %s이고, 이 중 빗썸 거래대금은 %s입니다. 이벤트 기준을 넘는 움직임이 없으므로 특정 코인이나 경쟁 거래소를 하락 원인으로 지목하지 않습니다.",
			formatQuestionKRWAmount(monitor.Current.TotalVolumeKRW), formatQuestionKRWAmount(monitor.Current.BithumbVolumeKRW),
		))
		points = append(points, fmt.Sprintf("국내 공식 24시간 거래금액은 %.0f원입니다.", monitor.Current.TotalVolumeKRW))
	}
	rollingBriefing := strings.TrimSpace(payload.Rolling24h.Briefing.Text)
	if rollingBriefing != "" {
		paragraphs = append(paragraphs, "별도의 WebSocket 직접 관측 진단은 다음과 같습니다. "+rollingBriefing+" 이 진단층은 정렬 1시간봉 24시간 점유율과 서로 다른 자료이므로 두 값을 합치거나 같은 지표로 취급하지 않습니다.")
	}
	if len(paragraphs) == 0 {
		paragraphs = append(paragraphs, "현재 확보된 서버 자료만으로는 24시간 점유율 변화 원인을 설명할 수 없습니다. 공식 24시간 비교 시점이나 기여도 자료가 준비된 뒤 다시 확인해야 합니다.")
	}
	if len(points) > marketQuestionPointLimit {
		points = points[:marketQuestionPointLimit]
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(paragraphs, "\n\n"), KeyPoints: points,
		Caveat: "수치 기여는 관측된 거래대금 변화의 산술적 분해입니다. 공지나 특정 거래와의 시간적 연관성만으로 인과관계가 증명되지는 않습니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func calendarMarketQuestionRequested(query marketanalysis.MarketQuestionQuery) bool {
	return query.Intent == marketanalysis.MarketQuestionCalendarDayShare
}

func marketQuestionRequestsOneHour(question string) bool {
	query, err := parseMarketQuestion(question, nil)
	if err != nil || query.Intent != marketanalysis.MarketQuestionOfficialWindowChange {
		return false
	}
	return query.TimeUnit == marketanalysis.MarketQuestionTimeHour && query.TimeValue == 1 ||
		query.TimeUnit == marketanalysis.MarketQuestionTimeMinute && query.TimeValue == 60
}

func buildOfficialHourFallback(comparison tickertrend.WindowComparison, asOfMS int64) marketanalysis.MarketQuestionAnswer {
	paragraphs := []string{}
	points := []string{}
	windowLabel := marketQuestionWindowLabel(comparison.WindowSec)
	if comparison.Status == "LIVE" && comparison.EndpointComparison && comparison.Previous != nil && comparison.Current != nil && comparison.ShareDeltaPP != nil {
		shareRelative := relativeQuestionChange(
			float64Pointer(comparison.Previous.BithumbSharePct),
			float64Pointer(comparison.Current.BithumbSharePct),
		)
		paragraphs = append(paragraphs, fmt.Sprintf(
			"지난 %s 공식 24시간 점유율은 %.2f%%에서 %.2f%%로 바뀌었고 변화폭은 %+.2f%%p(%s)입니다.",
			windowLabel,
			comparison.Previous.BithumbSharePct, comparison.Current.BithumbSharePct,
			*comparison.ShareDeltaPP, marketQuestionShareDirectionLabel(*comparison.ShareDeltaPP),
		))
		if shareRelative != nil {
			paragraphs[len(paragraphs)-1] = strings.TrimSuffix(paragraphs[len(paragraphs)-1], "입니다.") +
				fmt.Sprintf("이며, 시작점 대비 상대 변화율은 %+.2f%%입니다.", *shareRelative)
		}
		if detail := officialMarketQuestionEndpointDetail(comparison); detail != "" {
			paragraphs = append(paragraphs, detail)
		}
		volumeDetails := []string{}
		if comparison.DomesticVolumeDeltaKRW != nil {
			detail := "국내 전체 거래대금 " + formatQuestionKRW(*comparison.DomesticVolumeDeltaKRW)
			if change := relativeQuestionChange(float64Pointer(comparison.Previous.TotalVolumeKRW), float64Pointer(comparison.Current.TotalVolumeKRW)); change != nil {
				detail += fmt.Sprintf("(%+.2f%%)", *change)
			}
			volumeDetails = append(volumeDetails, detail)
		}
		if comparison.BithumbVolumeDeltaKRW != nil {
			detail := "빗썸 거래대금 " + formatQuestionKRW(*comparison.BithumbVolumeDeltaKRW)
			if change := relativeQuestionChange(float64Pointer(comparison.Previous.BithumbVolumeKRW), float64Pointer(comparison.Current.BithumbVolumeKRW)); change != nil {
				detail += fmt.Sprintf("(%+.2f%%)", *change)
			}
			volumeDetails = append(volumeDetails, detail)
		}
		if len(volumeDetails) > 0 {
			paragraphs = append(paragraphs, "같은 두 시점 사이에는 "+strings.Join(volumeDetails, ", ")+" 변화가 관측됐습니다. 점유율은 빗썸 거래대금뿐 아니라 다른 국내 거래소 거래대금이 함께 변한 결과입니다.")
		}
		if paragraph, point := officialMarketQuestionPathText(comparison.PathSummary); paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
			points = append(points, point)
		}
		if paragraph, point := officialMarketQuestionPriceRelationshipText(comparison); paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
			points = append(points, point)
		}

		positiveDrivers, negativeDrivers := directionalMovementDrivers(comparison.Drivers, 5)
		if len(positiveDrivers)+len(negativeDrivers) > 0 {
			paragraphs = append(paragraphs, "코인별 산술 기여를 최종 방향을 밀어준 힘과 반대로 방어한 힘으로 나누면 다음과 같습니다.")
			if details := officialMarketQuestionDriverDetails(positiveDrivers, "상승 기여", &points); len(details) > 0 {
				paragraphs = append(paragraphs, "점유율 하락을 방어하거나 상승 쪽으로 민 코인은 "+strings.Join(details, ", ")+"였습니다.")
			}
			if details := officialMarketQuestionDriverDetails(negativeDrivers, "하락 기여", &points); len(details) > 0 {
				paragraphs = append(paragraphs, "반대로 점유율을 아래로 끌어내린 코인은 "+strings.Join(details, ", ")+"였습니다.")
			}
			paragraphs = append(paragraphs, "이 기여도는 가격 등락이 아니라 각 코인의 빗썸·경쟁 거래소 거래대금 이동을 점유율 산식으로 분해한 값입니다.")
		} else {
			paragraphs = append(paragraphs, "코인별 공식 기여 자료가 없어 이번 답변은 거래소 전체 거래대금 변화까지만 설명할 수 있습니다.")
		}

		exchangeDetails := []string{}
		for _, driver := range boundedMovementExchangeDrivers(comparison.ExchangeDrivers, 2) {
			label := rollingExchangeLabel(driver.Exchange)
			exchangeDetails = append(exchangeDetails, fmt.Sprintf("%s 거래대금 %s, 빗썸 점유율 기여 %+.2f%%p", label, formatQuestionKRW(driver.VolumeChangeKRW), driver.ContributionPP))
			points = append(points, exchangeDetails[len(exchangeDetails)-1])
		}
		if len(exchangeDetails) > 0 {
			paragraphs = append(paragraphs, "거래소별 변화에서는 "+strings.Join(exchangeDetails, "; ")+"가 주요 항목이었습니다.")
		}

		if comparison.PathComplete {
			paragraphs = append(paragraphs, fmt.Sprintf("1시간봉 이력은 %d/%d개로 연속 구간이 완전합니다.", comparison.ObservedSampleCount, comparison.ExpectedSampleCount))
		} else {
			paragraphs = append(paragraphs, fmt.Sprintf("중간 1시간봉 이력은 %d/%d개여서 경로에는 공백이 있지만, 시작점과 끝점의 정렬 값은 확보되어 %s 변화 자체는 측정할 수 있습니다.", comparison.ObservedSampleCount, comparison.ExpectedSampleCount, windowLabel))
			points = append(points, fmt.Sprintf("중간 1시간봉 이력 %d/%d개 · %s 양 끝 시점 비교 가능", comparison.ObservedSampleCount, comparison.ExpectedSampleCount, windowLabel))
		}
	} else {
		paragraphs = append(paragraphs,
			fmt.Sprintf("정렬 1시간봉 이력이 아직 %s 비교에 필요한 시작점과 끝점을 모두 확보하지 못했습니다. 따라서 현재는 %s 점유율 변화나 코인별 기여도를 수치로 확정할 수 없습니다.", windowLabel, windowLabel),
			fmt.Sprintf("현재 확보된 연속 표본은 %d/%d개이며 상태는 %s입니다. 누락된 공식 시점이 채워진 뒤 같은 질문을 다시 확인해야 합니다.", comparison.ObservedSampleCount, comparison.ExpectedSampleCount, comparison.Status),
		)
		points = append(points, fmt.Sprintf("연속 표본 %d/%d개", comparison.ObservedSampleCount, comparison.ExpectedSampleCount))
		if reason := marketQuestionReasonLabel(comparison.Reason); reason != "" {
			points = append(points, "자료 미확보 사유: "+reason)
		}
	}
	if len(points) > marketQuestionPointLimit {
		points = points[:marketQuestionPointLimit]
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(paragraphs, "\n\n"), KeyPoints: points,
		Caveat: windowLabel + " 변화는 동일 UTC 종료시각의 정렬 시간봉 24시간 합계 두 시점 차이를 산술적으로 분해한 값입니다. 개별 거래나 공지가 변화의 원인이었다는 뜻은 아닙니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func marketQuestionWindowLabel(windowSec int64) string {
	if windowSec > 0 && windowSec%(24*3_600) == 0 {
		return fmt.Sprintf("%d일", windowSec/(24*3_600))
	}
	if windowSec > 0 && windowSec%3_600 == 0 {
		return fmt.Sprintf("%d시간", windowSec/3_600)
	}
	return fmt.Sprintf("%d초", windowSec)
}

// enrichOfficialHourCoinImpacts keeps the model's natural-language synthesis,
// but makes the server-owned asset attribution impossible to omit. The sign of
// contribution_pp describes the effect on Bithumb share; volume changes remain
// supporting arithmetic rather than a causal claim.
func enrichOfficialHourCoinImpacts(
	answer marketanalysis.MarketQuestionAnswer,
	comparison tickertrend.WindowComparison,
	requestedAsset string,
) marketanalysis.MarketQuestionAnswer {
	if comparison.Status != "LIVE" || !comparison.EndpointComparison {
		return answer
	}
	mandatory := officialMarketQuestionKeyPoints(comparison)
	mandatory = append(mandatory, officialMarketQuestionRequestedAssetPoints(comparison, requestedAsset)...)
	drivers := boundedMovementDrivers(comparison.Drivers, 3)
	requestedAsset = strings.ToUpper(strings.TrimSpace(requestedAsset))
	if requestedAsset != "" && !marketQuestionMovementDriversContain(drivers, requestedAsset) {
		for _, driver := range comparison.Drivers {
			if strings.EqualFold(driver.Asset, requestedAsset) {
				drivers = append(drivers, driver)
				break
			}
		}
	}
	if len(drivers) == 0 {
		answer.KeyPoints = append(mandatory, append([]string{
			"코인별 기여 자료가 없어 이번 답변은 거래소 전체 거래대금 변화만 설명합니다.",
		}, answer.KeyPoints...)...)
		if len(answer.KeyPoints) > marketQuestionPointLimit {
			answer.KeyPoints = answer.KeyPoints[:marketQuestionPointLimit]
		}
		return answer
	}

	coinSummary := make([]string, 0, len(drivers))
	coinPoints := make([]string, 0, len(drivers))
	answerText := strings.ToUpper(answer.Answer)
	allNamed := true
	for _, driver := range drivers {
		direction := "상승 기여"
		if driver.ContributionPP < 0 {
			direction = "하락 기여"
		}
		asset := strings.ToUpper(strings.TrimSpace(driver.Asset))
		if asset == "" {
			continue
		}
		if !strings.Contains(answerText, asset) {
			allNamed = false
		}
		coinSummary = append(coinSummary, fmt.Sprintf("%s %+.2f%%p(%s)", asset, driver.ContributionPP, direction))
		coinPoints = append(coinPoints, marketQuestionDriverPoint(driver, direction))
	}
	if len(coinSummary) == 0 {
		return answer
	}
	if !allNamed {
		answer.Answer = strings.TrimSpace(answer.Answer) + " 코인별 산술 기여는 " + strings.Join(coinSummary, ", ") + " 순으로 컸습니다."
	}
	mergedPoints := append([]string{}, mandatory...)
	mergedPoints = append(mergedPoints, coinPoints...)
	for _, point := range answer.KeyPoints {
		if len(mergedPoints) >= marketQuestionPointLimit {
			break
		}
		duplicate := false
		for _, existing := range mergedPoints {
			if point == existing {
				duplicate = true
				break
			}
		}
		if !duplicate {
			mergedPoints = append(mergedPoints, point)
		}
	}
	answer.KeyPoints = mergedPoints
	return answer
}

func officialMarketQuestionKeyPoints(comparison tickertrend.WindowComparison) []string {
	points := []string{}
	if comparison.Previous != nil && comparison.Current != nil && comparison.ShareDeltaPP != nil {
		points = append(points, fmt.Sprintf(
			"공식 %s 시점 비교: %.2f%% → %.2f%% · %+.2f%%p",
			marketQuestionWSWindowLabel(comparison.WindowSec), comparison.Previous.BithumbSharePct,
			comparison.Current.BithumbSharePct, *comparison.ShareDeltaPP,
		))
	}
	if comparison.BithumbVolumeDeltaKRW != nil && comparison.DomesticVolumeDeltaKRW != nil {
		points = append(points, fmt.Sprintf(
			"거래대금 변화: 빗썸 %s · 국내 전체 %s",
			formatQuestionKRW(*comparison.BithumbVolumeDeltaKRW), formatQuestionKRW(*comparison.DomesticVolumeDeltaKRW),
		))
	}
	if _, point := officialMarketQuestionPathText(comparison.PathSummary); point != "" {
		points = append(points, point)
	}
	if _, point := officialMarketQuestionPriceRelationshipText(comparison); point != "" {
		points = append(points, point)
	}
	if !comparison.PathComplete {
		points = append(points, "시작·끝 endpoint는 비교 가능하지만 중간 공식 시간봉 경로는 불완전합니다.")
	}
	if len(comparison.ExchangeDrivers) > 0 {
		driver := comparison.ExchangeDrivers[0]
		points = append(points, fmt.Sprintf(
			"주요 거래소 기여 %s %+.2f%%p · 거래대금 변화 %s",
			rollingExchangeLabel(driver.Exchange), driver.ContributionPP, formatQuestionKRW(driver.VolumeChangeKRW),
		))
	}
	return points
}

func officialMarketQuestionPathText(path tickertrend.WindowPathSummary) (string, string) {
	if path.Status != "COMPLETE" || path.MinSharePct == nil || path.MaxSharePct == nil || path.LargestStepDeltaPP == nil {
		segments := path.ObservedSegments
		if len(segments) == 0 {
			return "", ""
		}
		if len(segments) > 4 {
			segments = segments[:4]
		}
		details := make([]string, 0, len(segments))
		for _, segment := range segments {
			details = append(details, fmt.Sprintf(
				"%s~%s %+.2f%%p(%.2f%%→%.2f%%, 최저 %.2f%%·최고 %.2f%%, 최대 1시간 %+.2f%%p)",
				marketQuestionKST(segment.StartMS), marketQuestionKST(segment.EndMS), segment.ShareDeltaPP,
				segment.StartSharePct, segment.EndSharePct, segment.MinSharePct, segment.MaxSharePct,
				segment.LargestStepDeltaPP,
			))
		}
		paragraph := fmt.Sprintf(
			"전체 경로는 %d/%d개 시점만 관측돼 공백 구간의 움직임을 확정할 수 없습니다. 다만 공백을 건너지 않는 실제 연속 관측 구간은 %s로 확인됩니다.",
			path.PointCount, path.ExpectedPointCount, strings.Join(details, "; "),
		)
		point := fmt.Sprintf("공식 경로 %d/%d개 · 실제 연속 관측 구간 %d개(공백 사이 변화는 미추정)", path.PointCount, path.ExpectedPointCount, len(path.ObservedSegments))
		return paragraph, point
	}
	labels := map[string][2]string{
		"RISING":  {"상승만 관측", "하락 없이 상승만 나타났습니다"},
		"FALLING": {"하락만 관측", "상승 없이 하락만 나타났습니다"},
		"FLAT":    {"보합", "상승·하락 없이 보합이었습니다"},
		"MIXED":   {"상승·하락 혼재", "상승과 하락이 섞여 있었습니다"},
	}[path.Direction]
	direction, sentence := labels[0], labels[1]
	if direction == "" {
		direction, sentence = path.Direction, "방향은 "+path.Direction+"입니다"
	}
	concentration := ""
	if path.LargestStepAbsSharePct != nil {
		concentration = fmt.Sprintf(" 전체 시간별 절대 변화의 %.1f%%가 이 한 구간에 집중됐습니다.", *path.LargestStepAbsSharePct)
	}
	paragraph := fmt.Sprintf(
		"1시간 간격으로 본 공식 24시간 점유율은 최저 %.2f%%(%s), 최고 %.2f%%(%s)였습니다. 시간별 변화는 상승 %d회·하락 %d회·보합 %d회로 %s. 가장 큰 1시간 변화는 %s~%s의 %+.2f%%p였습니다.%s",
		*path.MinSharePct, marketQuestionKST(path.MinShareAtMS), *path.MaxSharePct, marketQuestionKST(path.MaxShareAtMS),
		path.RisingStepCount, path.FallingStepCount, path.FlatStepCount, sentence,
		marketQuestionKST(path.LargestStepStartMS), marketQuestionKST(path.LargestStepEndMS), *path.LargestStepDeltaPP,
		concentration,
	)
	point := fmt.Sprintf(
		"공식 시간별 경로: %s · 상승 %d/하락 %d/보합 %d · 최저 %.2f%%/최고 %.2f%% · 최대 1시간 %+.2f%%p",
		direction, path.RisingStepCount, path.FallingStepCount, path.FlatStepCount,
		*path.MinSharePct, *path.MaxSharePct, *path.LargestStepDeltaPP,
	)
	return paragraph, point
}

func officialMarketQuestionPriceRelationshipText(comparison tickertrend.WindowComparison) (string, string) {
	if comparison.PriceRelationshipStatus != "EXPLORATORY" || len(comparison.PriceRelationships) == 0 {
		return "", ""
	}
	values := comparison.PriceRelationships
	if len(values) > 5 {
		values = values[:5]
	}
	details := make([]string, 0, len(values))
	for _, value := range values {
		direction := "같은 방향 경향"
		if value.Direction == "OPPOSITE_DIRECTION" {
			direction = "반대 방향 경향"
		}
		details = append(details, fmt.Sprintf(
			"%s %+.3f(%d개 연속 시간쌍, %s)", value.Asset, value.Correlation, value.PairCount, direction,
		))
	}
	paragraph := "가격 자료가 함께 저장된 공백 없는 1시간쌍에서 코인 수익률과 빗썸 점유율 변화의 탐색적 동시간 상관을 계산하면 " +
		strings.Join(details, ", ") + " 순으로 절댓값이 컸습니다. 이는 표본 안에서 함께 움직인 정도일 뿐, 가격이 점유율 변화를 일으켰다는 뜻은 아닙니다."
	point := "가격-점유율 탐색 신호: " + strings.Join(details, ", ")
	return paragraph, point
}

func directionalMovementDrivers(values []movement.Driver, limitPerDirection int) ([]movement.Driver, []movement.Driver) {
	positive := make([]movement.Driver, 0, limitPerDirection)
	negative := make([]movement.Driver, 0, limitPerDirection)
	for _, driver := range values {
		switch {
		case driver.ContributionPP > 0 && len(positive) < limitPerDirection:
			positive = append(positive, driver)
		case driver.ContributionPP < 0 && len(negative) < limitPerDirection:
			negative = append(negative, driver)
		}
		if len(positive) == limitPerDirection && len(negative) == limitPerDirection {
			break
		}
	}
	return positive, negative
}

func officialMarketQuestionDriverDetails(drivers []movement.Driver, direction string, points *[]string) []string {
	details := make([]string, 0, len(drivers))
	for _, driver := range drivers {
		details = append(details, fmt.Sprintf(
			"%s %+.2f%%p(빗썸 %s / 타 거래소 %s)", driver.Asset, driver.ContributionPP,
			formatQuestionKRW(driver.BithumbVolumeChangeKRW), formatQuestionKRW(driver.CompetitorVolumeChangeKRW),
		))
		if len(*points) < marketQuestionPointLimit {
			*points = append(*points, marketQuestionDriverPoint(driver, direction))
		}
	}
	return details
}

func officialMarketQuestionRequestedAssetPoints(comparison tickertrend.WindowComparison, asset string) []string {
	metrics := officialMarketQuestionAssetMetrics(comparison, asset)
	if metrics == nil {
		return nil
	}
	points := []string{}
	if metrics.PreviousBithumbCapturePct != nil && metrics.CurrentBithumbCapturePct != nil && metrics.BithumbCaptureChangePP != nil {
		points = append(points, fmt.Sprintf(
			"%s 빗썸 포착률 %.2f%% → %.2f%% · %+.2f%%p",
			metrics.Asset, *metrics.PreviousBithumbCapturePct, *metrics.CurrentBithumbCapturePct, *metrics.BithumbCaptureChangePP,
		))
	}
	if metrics.PreviousShareWithinBithumbPct != nil && metrics.CurrentShareWithinBithumbPct != nil && metrics.ShareWithinBithumbChangePP != nil {
		points = append(points, fmt.Sprintf(
			"빗썸 내부 %s 비중 %.2f%% → %.2f%% · %+.2f%%p",
			metrics.Asset, *metrics.PreviousShareWithinBithumbPct, *metrics.CurrentShareWithinBithumbPct, *metrics.ShareWithinBithumbChangePP,
		))
	}
	if metrics.PreviousTotalShareContributionPct != nil && metrics.CurrentTotalShareContributionPct != nil && metrics.TotalShareContributionChangePP != nil {
		points = append(points, fmt.Sprintf(
			"%s 국내 전체 점유율 몫 %.2f%% → %.2f%% · %+.2f%%p",
			metrics.Asset, *metrics.PreviousTotalShareContributionPct, *metrics.CurrentTotalShareContributionPct, *metrics.TotalShareContributionChangePP,
		))
	}
	return points
}

func marketQuestionMovementDriversContain(drivers []movement.Driver, asset string) bool {
	for _, driver := range drivers {
		if strings.EqualFold(driver.Asset, asset) {
			return true
		}
	}
	return false
}

func enrichMarketQuestionIdentityWarning(
	answer marketanalysis.MarketQuestionAnswer,
	payload marketanalysis.Analysis,
	official tickertrend.WindowComparison,
	websocket marketanalysis.MarketQuestionIntervalEvidence,
	query marketanalysis.MarketQuestionQuery,
) marketanalysis.MarketQuestionAnswer {
	asset := strings.ToUpper(strings.TrimSpace(query.Asset))
	if asset == "" {
		return answer
	}
	verified := false
	if websocket.Asset != nil && strings.EqualFold(websocket.Asset.Asset, asset) {
		verified = websocket.Asset.ContractIdentityVerified
	} else if metrics := officialMarketQuestionAssetMetrics(official, asset); metrics != nil && metrics.CurrentDomesticVolumeKRW != nil {
		verified = metrics.ContractIdentityVerified
	} else if metrics := currentMarketQuestionAssetMetrics(payload, asset, false); metrics != nil {
		verified = metrics.ContractIdentityVerified
	}
	if verified {
		return answer
	}
	warning := koreanWithTopic(asset) + " " + marketQuestionAssetIdentityNote
	if marketQuestionContainsString(answer.KeyPoints, warning) {
		return answer
	}
	if len(answer.KeyPoints) < marketQuestionPointLimit {
		answer.KeyPoints = append(answer.KeyPoints, warning)
	} else if len(answer.KeyPoints) > 0 {
		answer.KeyPoints[len(answer.KeyPoints)-1] = warning
	}
	return answer
}

// marketQuestionDriverPoint is the single key-point form of a coin
// contribution, so the same driver from two briefing paths deduplicates.
func marketQuestionDriverPoint(driver movement.Driver, direction string) string {
	return fmt.Sprintf(
		"%s %+.2f%%p %s · 빗썸 %s / 타 거래소 %s",
		strings.ToUpper(strings.TrimSpace(driver.Asset)), driver.ContributionPP, direction,
		formatQuestionKRW(driver.BithumbVolumeChangeKRW), formatQuestionKRW(driver.CompetitorVolumeChangeKRW),
	)
}

func formatQuestionKRW(value float64) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	sign := "+"
	if value < 0 {
		sign = "-"
	}
	switch {
	case abs >= 1_000_000_000_000:
		return fmt.Sprintf("%s%.2f조원", sign, abs/1_000_000_000_000)
	case abs >= 100_000_000:
		return fmt.Sprintf("%s%.0f억원", sign, abs/100_000_000)
	case abs >= 10_000:
		return fmt.Sprintf("%s%.0f만원", sign, abs/10_000)
	case abs < 0.5:
		return "0원"
	default:
		return fmt.Sprintf("%s%.0f원", sign, abs)
	}
}

func formatQuestionKRWAmount(value float64) string {
	return strings.TrimPrefix(formatQuestionKRW(value), "+")
}

func boundedMovementDrivers(values []movement.Driver, limit int) []movement.Driver {
	return append([]movement.Driver(nil), values[:min(len(values), limit)]...)
}

// marketQuestionMovementDrivers keeps the compact leading-driver summary but
// also includes every driver explicitly matched by retained notice evidence.
// Notice eligibility is therefore independent of a presentation/prompt limit.
func marketQuestionMovementDrivers(
	values []movement.Driver,
	notices []movement.NoticeEvidence,
	leadingLimit int,
) []movement.Driver {
	leadingLimit = min(len(values), max(0, leadingLimit))
	result := append([]movement.Driver(nil), values[:leadingLimit]...)
	if leadingLimit == len(values) || len(notices) == 0 {
		return result
	}

	matched := make(map[string]struct{})
	for _, item := range notices {
		for _, asset := range item.MatchedAssets {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset != "" {
				matched[asset] = struct{}{}
			}
		}
	}
	if len(matched) == 0 {
		return result
	}

	included := make(map[string]struct{}, leadingLimit)
	for _, driver := range result {
		included[strings.ToUpper(strings.TrimSpace(driver.Asset))] = struct{}{}
	}
	for _, driver := range values[leadingLimit:] {
		asset := strings.ToUpper(strings.TrimSpace(driver.Asset))
		if _, linked := matched[asset]; !linked {
			continue
		}
		if _, exists := included[asset]; exists {
			continue
		}
		result = append(result, driver)
		included[asset] = struct{}{}
	}
	return result
}

func boundedMovementExchangeDrivers(values []movement.ExchangeDriver, limit int) []movement.ExchangeDriver {
	return append([]movement.ExchangeDriver(nil), values[:min(len(values), limit)]...)
}
