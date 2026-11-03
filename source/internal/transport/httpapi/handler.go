package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/marketquestion"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/tickertrend"
	"market-share-go/services/collector/registry"
)

var seoulLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		return time.FixedZone("Asia/Seoul", 9*60*60)
	}
	return location
}()

const websocketWriteTimeout = 15 * time.Second

const (
	initialWebSocketFrameGrace = 750 * time.Millisecond
	websocketNoticeInterval    = time.Second
)

type StatusProvider interface {
	Health() Health
}

type AnalysisProvider interface {
	Analysis(context.Context, int64, string, float64) (marketanalysis.Analysis, error)
}

type AnalysisOptionsProvider interface {
	AnalysisWithOptions(context.Context, int64, string, float64, marketanalysis.RequestOptions) (marketanalysis.Analysis, error)
}
type ActiveNoticeNetworkProvider interface {
	ActiveNoticeNetwork(context.Context, int64) (any, error)
}

type HermesMarketQuestionProvider interface {
	AskHermesMarketQuestion(context.Context, string) (marketanalysis.MarketQuestionAnswer, error)
}

type GroundedMarketQuestionProvider interface {
	AskGroundedMarketQuestion(context.Context, string) (marketanalysis.GroundedMarketQuestionResponse, error)
}

type GroundedMarketQuestionHistoryProvider interface {
	AskGroundedMarketQuestionWithHistory(
		context.Context,
		string,
		[]marketanalysis.MarketQuestionConversationTurn,
	) (marketanalysis.GroundedMarketQuestionResponse, error)
}

type GroundedMarketQuestionStreamProvider interface {
	StreamGroundedMarketQuestion(
		context.Context,
		marketanalysis.GroundedMarketQuestionRequest,
		func(marketanalysis.MarketQuestionStageEvent) error,
	) error
}

type MarketQuestionEvidenceProvider interface {
	MarketQuestionEvidenceBundle(context.Context, string) (marketquestion.EvidenceBundleSnapshot, error)
}

type MarketQuestionFeedbackProvider interface {
	SubmitMarketQuestionFeedback(context.Context, marketanalysis.MarketQuestionFeedback) error
}

type HermesNoticeQuestionProvider interface {
	AskHermesNoticeQuestion(context.Context, string, string, []string) (marketanalysis.NoticeQuestionAnswer, error)
}

type MarketRowsProvider interface {
	MarketRows(context.Context, int64, string, string) (any, error)
}
type LiquidityProvider interface {
	LiquidityRows(context.Context, float64, string, string, string) (any, error)
}
type OrderBookProvider interface {
	OrderBookDetail(context.Context, string, string, float64) (any, error)
}
type TickProvider interface {
	RealtimeTick(context.Context, int64, string) (any, error)
}
type TickReferenceProvider interface {
	RealtimeTickReferences() marketanalysis.TickReferences
}
type RealtimeFlowStateProvider interface {
	RealtimeFlowState() marketanalysis.RealtimeFlowState
}
type WhaleAlertStateProvider interface {
	WhaleAlertState() marketanalysis.WhaleAlertState
}
type SnapshotProvider interface {
	Snapshot(context.Context, bool) (any, error)
}
type ReconciliationProvider interface {
	ReconciliationLatest(context.Context) (any, error)
}
type CalendarDayProvider interface {
	CalendarDay(context.Context, string, string) (any, error)
}
type NoticeProvider interface {
	NoticeSnapshot(context.Context) (any, error)
}
type HermesNoticeIngestProvider interface {
	IngestHermesNotices(context.Context, string, notice.HermesIngestRequest) (notice.HermesIngestResult, error)
}
type HermesNoticeSnapshotProvider interface {
	HermesNoticeSnapshot(context.Context, string) (notice.Snapshot, error)
}
type RuntimeStateProvider interface {
	RuntimeState(context.Context) map[string]any
}
type OfficialMovementProvider interface {
	OfficialMovementEvents(context.Context, int) ([]movement.Event, error)
	OfficialMovementEvent(context.Context, string) (*movement.Event, error)
}
type OfficialAlignedHistoryProvider interface {
	OfficialAlignedHistory(context.Context, int64, int64) (tickertrend.History, error)
}
type OfficialAlignedContributionProvider interface {
	OfficialAlignedContribution(context.Context, int64) (tickertrend.WindowComparison, error)
}

// WindowChangesProvider compares every rolling window with the one before it.
type WindowChangesProvider interface {
	WindowChanges(context.Context, string) (marketanalysis.WindowChanges, error)
}

// CoinSharesProvider lists Bithumb's share of each of the most traded coins.
type CoinSharesProvider interface {
	CoinShares(ctx context.Context, window int64, marketScope string, limit int) (marketanalysis.CoinShares, error)
}

// ClosedSecondsProvider lists the live window ending at each recent closed
// second, by exchange, so the live charts open with their recent past.
type ClosedSecondsProvider interface {
	ClosedSeconds(ctx context.Context, window int64, marketScope string) (any, error)
}

// ShareSeriesProvider lists Bithumb's share in each recent minute, ten
// minutes or half hour.
type ShareSeriesProvider interface {
	ShareSeries(ctx context.Context, bucketSec int64, marketScope string) (marketanalysis.ShareSeries, error)
}

// LiveCoinSharesProvider reads those coins' shares of the last minute anew,
// for the WebSocket's once-a-second coin shares.
type LiveCoinSharesProvider interface {
	LiveCoinShares(context.Context) (marketanalysis.LiveCoinShares, error)
}
type RealtimeFlowProvider interface {
	RealtimeFlowHistory(context.Context, marketanalysis.RealtimeFlowHistoryFilter) ([]marketanalysis.RealtimeFlowState, error)
}
type RealtimeFlowSummaryProvider interface {
	RealtimeFlowPeriodSummary(context.Context, int64, int64) (marketanalysis.RealtimeFlowPeriodSummary, error)
}
type RegistryHistoryProvider interface {
	RegistryHistory(context.Context, registry.HistoryFilter) (registry.History, error)
}

type Health struct {
	Status        string   `json:"status"`
	Running       bool     `json:"running"`
	RealtimeReady bool     `json:"realtime_ready"`
	DatabaseReady bool     `json:"database_ready"`
	Unhealthy     []string `json:"unhealthy_collectors"`
}

type staticStatus struct{}

func (staticStatus) Health() Health {
	return Health{
		Status: "starting", Running: true, RealtimeReady: false, Unhealthy: []string{},
	}
}

func NewHandler(logger *slog.Logger, status StatusProvider, providers ...AnalysisProvider) http.Handler {
	return newHandler(context.Background(), false, logger, status, providers...)
}

// NewAlwaysOnHandler is retained for focused warm-cache tests and explicit
// deployments. The production server uses NewHandler so unused calculation
// keys consume no background CPU.
func NewAlwaysOnHandler(ctx context.Context, logger *slog.Logger, status StatusProvider, providers ...AnalysisProvider) http.Handler {
	return newHandler(ctx, true, logger, status, providers...)
}

func newHandler(ctx context.Context, alwaysOnAnalysis bool, logger *slog.Logger, status StatusProvider, providers ...AnalysisProvider) http.Handler {
	if status == nil {
		status = staticStatus{}
	}
	mux := http.NewServeMux()
	var analysisProvider AnalysisProvider
	var hermesMarketQuestionProvider HermesMarketQuestionProvider
	var groundedMarketQuestionProvider GroundedMarketQuestionProvider
	var groundedMarketQuestionStreamProvider GroundedMarketQuestionStreamProvider
	var marketQuestionEvidenceProvider MarketQuestionEvidenceProvider
	var marketQuestionFeedbackProvider MarketQuestionFeedbackProvider
	var hermesNoticeQuestionProvider HermesNoticeQuestionProvider
	var marketRowsProvider MarketRowsProvider
	var liquidityProvider LiquidityProvider
	var orderBookProvider OrderBookProvider
	var tickProvider TickProvider
	var tickReferenceProvider TickReferenceProvider
	var realtimeFlowStateProvider RealtimeFlowStateProvider
	var whaleAlertStateProvider WhaleAlertStateProvider
	var snapshotProvider SnapshotProvider
	var reconciliationProvider ReconciliationProvider
	var calendarDayProvider CalendarDayProvider
	var noticeProvider NoticeProvider
	var hermesNoticeIngestProvider HermesNoticeIngestProvider
	var hermesNoticeSnapshotProvider HermesNoticeSnapshotProvider
	var runtimeStateProvider RuntimeStateProvider
	var officialMovementProvider OfficialMovementProvider
	var officialAlignedHistoryProvider OfficialAlignedHistoryProvider
	var officialAlignedContributionProvider OfficialAlignedContributionProvider
	var windowChangesProvider WindowChangesProvider
	var coinSharesProvider CoinSharesProvider
	var liveCoinSharesProvider LiveCoinSharesProvider
	var shareSeriesProvider ShareSeriesProvider
	var closedSecondsProvider ClosedSecondsProvider
	var realtimeFlowProvider RealtimeFlowProvider
	var realtimeFlowSummaryProvider RealtimeFlowSummaryProvider
	var registryHistoryProvider RegistryHistoryProvider
	var activeNoticeNetworkProvider ActiveNoticeNetworkProvider
	if len(providers) > 0 {
		analysisProvider = providers[0]
		hermesMarketQuestionProvider, _ = any(providers[0]).(HermesMarketQuestionProvider)
		groundedMarketQuestionProvider, _ = any(providers[0]).(GroundedMarketQuestionProvider)
		groundedMarketQuestionStreamProvider, _ = any(providers[0]).(GroundedMarketQuestionStreamProvider)
		marketQuestionEvidenceProvider, _ = any(providers[0]).(MarketQuestionEvidenceProvider)
		marketQuestionFeedbackProvider, _ = any(providers[0]).(MarketQuestionFeedbackProvider)
		hermesNoticeQuestionProvider, _ = any(providers[0]).(HermesNoticeQuestionProvider)
		marketRowsProvider, _ = any(providers[0]).(MarketRowsProvider)
		liquidityProvider, _ = any(providers[0]).(LiquidityProvider)
		orderBookProvider, _ = any(providers[0]).(OrderBookProvider)
		tickProvider, _ = any(providers[0]).(TickProvider)
		tickReferenceProvider, _ = any(providers[0]).(TickReferenceProvider)
		realtimeFlowStateProvider, _ = any(providers[0]).(RealtimeFlowStateProvider)
		whaleAlertStateProvider, _ = any(providers[0]).(WhaleAlertStateProvider)
		snapshotProvider, _ = any(providers[0]).(SnapshotProvider)
		reconciliationProvider, _ = any(providers[0]).(ReconciliationProvider)
		calendarDayProvider, _ = any(providers[0]).(CalendarDayProvider)
		noticeProvider, _ = any(providers[0]).(NoticeProvider)
		hermesNoticeIngestProvider, _ = any(providers[0]).(HermesNoticeIngestProvider)
		hermesNoticeSnapshotProvider, _ = any(providers[0]).(HermesNoticeSnapshotProvider)
		registerNoticePipeline(mux, providers[0])
		registerIndexDay(mux, providers[0])
		registerRealtimeIndex(mux, providers[0])
		registerIndexFilters(mux, analysisProvider)
		registerNoticePortal(mux, analysisProvider)
		runtimeStateProvider, _ = any(providers[0]).(RuntimeStateProvider)
		officialMovementProvider, _ = any(providers[0]).(OfficialMovementProvider)
		officialAlignedHistoryProvider, _ = any(providers[0]).(OfficialAlignedHistoryProvider)
		officialAlignedContributionProvider, _ = any(providers[0]).(OfficialAlignedContributionProvider)
		windowChangesProvider, _ = any(providers[0]).(WindowChangesProvider)
		coinSharesProvider, _ = any(providers[0]).(CoinSharesProvider)
		liveCoinSharesProvider, _ = any(providers[0]).(LiveCoinSharesProvider)
		shareSeriesProvider, _ = any(providers[0]).(ShareSeriesProvider)
		closedSecondsProvider, _ = any(providers[0]).(ClosedSecondsProvider)
		realtimeFlowProvider, _ = any(providers[0]).(RealtimeFlowProvider)
		realtimeFlowSummaryProvider, _ = any(providers[0]).(RealtimeFlowSummaryProvider)
		registryHistoryProvider, _ = any(providers[0]).(RegistryHistoryProvider)
		activeNoticeNetworkProvider, _ = any(providers[0]).(ActiveNoticeNetworkProvider)
	}
	sharedTicks := newTickBroadcaster(tickProvider, logger)
	sharedAnalyses := newAnalysisBroadcaster(analysisProvider, runtimeStateProvider, logger)
	analysisResponses := newAnalysisResponseCache()
	realtimeSummaryResponses := newRealtimeSummaryResponseCache()
	if alwaysOnAnalysis {
		sharedAnalyses.prewarm(ctx, dashboardAnalysisWarmSpecs())
	}
	var sharedTickReferences, sharedRealtimeFlow, sharedRealtimeFlowCompact, sharedRealtimeFlowStrip, sharedWhaleAlerts, sharedCoinShares *stateBroadcaster
	if tickReferenceProvider != nil {
		sharedTickReferences = newStateBroadcaster(
			func(context.Context) any {
				references := tickReferenceProvider.RealtimeTickReferences()
				references.OfficialShareMonitor = *compactMovementState(references.OfficialShareMonitor)
				return references
			},
			"market_share_tick_references", "references", tickReferenceInterval, logger,
		)
	}
	if realtimeFlowStateProvider != nil {
		sharedRealtimeFlow = newStateBroadcaster(
			func(context.Context) any {
				return marketanalysis.PublishedRealtimeFlowState(realtimeFlowStateProvider.RealtimeFlowState())
			},
			"market_share_realtime_flow", "realtime_flow", realtimeFlowInterval, logger,
		)
		sharedRealtimeFlowCompact = newStateBroadcaster(
			func(context.Context) any {
				return compactRealtimeFlow(realtimeFlowStateProvider.RealtimeFlowState())
			},
			"market_share_realtime_flow", "realtime_flow", realtimeFlowInterval, logger,
		)
		sharedRealtimeFlowStrip = newStateBroadcaster(
			func(context.Context) any {
				return stripRealtimeFlow(realtimeFlowStateProvider.RealtimeFlowState())
			},
			"market_share_realtime_flow", "realtime_flow", realtimeFlowInterval, logger,
		)
	}
	if whaleAlertStateProvider != nil {
		sharedWhaleAlerts = newStateBroadcaster(
			func(context.Context) any { return whaleAlertStateProvider.WhaleAlertState() },
			"market_share_whale_alerts", "whale_alerts", whaleAlertInterval, logger,
		)
	}
	if liveCoinSharesProvider != nil {
		sharedCoinShares = newStateBroadcaster(
			func(ctx context.Context) any {
				shares, err := liveCoinSharesProvider.LiveCoinShares(ctx)
				if err != nil {
					return map[string]string{"status": "UNAVAILABLE", "detail": boundedTickErrorDetail(err.Error())}
				}
				return shares
			},
			"market_share_coin_shares", "coin_shares", liveCoinSharesInterval, logger,
		)
	}
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"service":   "market-share-intelligence",
			"dashboard": "/market-share",
			"docs":      "/docs",
		})
	})
	mux.HandleFunc("GET /api/health/live", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /api/health", func(writer http.ResponseWriter, _ *http.Request) {
		health := status.Health()
		code := http.StatusOK
		if !health.Running {
			code = http.StatusServiceUnavailable
		}
		writeJSON(writer, code, struct {
			Health
			GeneratedAt  float64 `json:"generated_at"`
			Transport    string  `json:"live_market_data_transport"`
			RESTFallback bool    `json:"rest_market_data_fallback_enabled"`
		}{
			Health:       health,
			GeneratedAt:  float64(time.Now().UnixNano()) / 1e9,
			Transport:    "WEBSOCKET_ONLY",
			RESTFallback: false,
		})
	})
	mux.HandleFunc("GET /api/health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		health := status.Health()
		code := http.StatusOK
		if !health.Running || !health.RealtimeReady || !health.DatabaseReady {
			code = http.StatusServiceUnavailable
		}
		writeJSON(writer, code, health)
	})
	mux.HandleFunc("POST /internal/hermes/notices", func(writer http.ResponseWriter, request *http.Request) {
		if hermesNoticeIngestProvider == nil {
			unavailable(writer)
			return
		}
		if !hermesTransportAllowed(request) {
			writeJSON(writer, http.StatusForbidden, map[string]string{"detail": "Hermes notice ingest is loopback-only"})
			return
		}
		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"detail": notice.ErrHermesIngestUnauthorized.Error()})
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 8<<20)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var payload notice.HermesIngestRequest
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "invalid Hermes notice JSON"})
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "Hermes notice request must contain one JSON object"})
			return
		}
		result, err := hermesNoticeIngestProvider.IngestHermesNotices(request.Context(), token, payload)
		switch {
		case errors.Is(err, notice.ErrHermesIngestUnauthorized):
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"detail": err.Error()})
		case errors.Is(err, notice.ErrHermesIngestDisabled):
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
		case errors.Is(err, notice.ErrHermesIngestInvalid):
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		case errors.Is(err, notice.ErrHermesIngestConflict):
			writeJSON(writer, http.StatusConflict, map[string]string{"detail": err.Error()})
		case err != nil:
			logRequestFailure(logger, request, "Hermes notice ingest failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "Hermes notice ingest failed"})
		default:
			writeJSON(writer, http.StatusOK, result)
		}
	})
	mux.HandleFunc("GET /internal/hermes/notices", func(writer http.ResponseWriter, request *http.Request) {
		if hermesNoticeSnapshotProvider == nil {
			unavailable(writer)
			return
		}
		if !hermesTransportAllowed(request) {
			writeJSON(writer, http.StatusForbidden, map[string]string{"detail": "Hermes notice snapshot is loopback-only"})
			return
		}
		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"detail": notice.ErrHermesIngestUnauthorized.Error()})
			return
		}
		if request.URL.RawQuery != "" {
			serveHermesNoticeArchivePage(writer, request, token, hermesNoticeSnapshotProvider)
			return
		}
		result, err := hermesNoticeSnapshotProvider.HermesNoticeSnapshot(request.Context(), token)
		if errors.Is(err, notice.ErrHermesIngestUnauthorized) {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"detail": err.Error()})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "Hermes notice snapshot failed"})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	})
	mux.HandleFunc("GET /api/market-share/notices", noticeArchiveHandler(noticeProvider, logger))
	mux.HandleFunc("GET /api/market-share/analysis", func(writer http.ResponseWriter, request *http.Request) {
		if analysisProvider == nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "market-share service is not ready"})
			return
		}
		window, ok := queryInt64(request, "window", 60)
		if !ok {
			unprocessable(writer, "window must be an integer")
			return
		}
		if !validWindow(window) {
			badWindow(writer)
			return
		}
		if !validNotional(request.URL.Query().Get("notional_krw")) {
			badNotional(writer)
			return
		}
		marketScope, ok := queryMarketScope(request)
		if !ok {
			badMarketScope(writer)
			return
		}
		notional, _ := queryNotional(request)
		payload, err := analysisProvider.Analysis(request.Context(), window, marketScope, notional)
		if err != nil {
			logRequestFailure(logger, request, "market-share analysis failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeAnalysisRequestJSON(writer, request, http.StatusOK, payload, analysisResponses)
	})
	mux.HandleFunc("GET /api/market-share/active-notice-network", func(writer http.ResponseWriter, request *http.Request) {
		if activeNoticeNetworkProvider == nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "active notice network unavailable"})
			return
		}
		windowSec, valid := queryInt64(request, "window_sec", 3600)
		if !valid || !activeNoticeNetworkWindowAllowed(windowSec) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "window_sec must be one of 60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, or 86400"})
			return
		}
		result, err := activeNoticeNetworkProvider.ActiveNoticeNetwork(request.Context(), windowSec)
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/market-share/hermes-question", func(writer http.ResponseWriter, request *http.Request) {
		if hermesMarketQuestionProvider == nil {
			unavailable(writer)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 4_096)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var payload struct {
			Question string `json:"question"`
		}
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "invalid Hermes question JSON"})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "Hermes question request must contain one JSON object"})
			return
		}
		answer, err := hermesMarketQuestionProvider.AskHermesMarketQuestion(request.Context(), payload.Question)
		switch {
		case errors.Is(err, marketanalysis.ErrRestrictedLLMQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrRestrictedLLMQuestion.Error()})
		case errors.Is(err, marketanalysis.ErrInvalidMarketQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestion.Error()})
		case err != nil:
			logRequestFailure(logger, request, "Hermes market question failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "Hermes market question is unavailable"})
		default:
			writeRequestJSON(writer, request, http.StatusOK, answer)
		}
	})
	mux.HandleFunc("POST /api/market-share/grounded-question", func(writer http.ResponseWriter, request *http.Request) {
		if groundedMarketQuestionProvider == nil {
			unavailable(writer)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 2_100_000)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var payload struct {
			Question string                                          `json:"question"`
			History  []marketanalysis.MarketQuestionConversationTurn `json:"history,omitempty"`
		}
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "invalid grounded question JSON"})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "grounded question request must contain one JSON object"})
			return
		}
		if len(payload.History) > marketanalysis.MaxMarketQuestionConversationTurns {
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestion.Error()})
			return
		}
		var answer marketanalysis.GroundedMarketQuestionResponse
		var err error
		if historyProvider, ok := groundedMarketQuestionProvider.(GroundedMarketQuestionHistoryProvider); ok {
			answer, err = historyProvider.AskGroundedMarketQuestionWithHistory(
				request.Context(), payload.Question, payload.History,
			)
		} else if len(payload.History) > 0 {
			err = marketanalysis.ErrInvalidMarketQuestion
		} else {
			answer, err = groundedMarketQuestionProvider.AskGroundedMarketQuestion(request.Context(), payload.Question)
		}
		switch {
		case errors.Is(err, marketanalysis.ErrRestrictedLLMQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrRestrictedLLMQuestion.Error()})
		case errors.Is(err, marketanalysis.ErrInvalidMarketQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestion.Error()})
		case errors.Is(err, context.DeadlineExceeded):
			logger.Error("grounded market question timed out", "error", err)
			writeJSON(writer, http.StatusGatewayTimeout, map[string]string{"detail": "grounded market question timed out"})
		case err != nil:
			logRequestFailure(logger, request, "grounded market question failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "grounded market question is unavailable"})
		default:
			writeRequestJSON(writer, request, http.StatusOK, answer)
		}
	})
	mux.HandleFunc("POST /api/market-share/grounded-question/stream", func(writer http.ResponseWriter, request *http.Request) {
		if groundedMarketQuestionStreamProvider == nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"detail": "quantitative-first question stream is unavailable"})
			return
		}
		flusher, ok := writer.(http.Flusher)
		if !ok {
			writeJSON(writer, http.StatusNotImplemented, map[string]string{"detail": "response streaming is unavailable"})
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 2_100_000)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var payload marketanalysis.GroundedMarketQuestionRequest
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "invalid grounded question stream JSON"})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "grounded question stream request must contain one JSON object"})
			return
		}
		if len(payload.History) > marketanalysis.MaxMarketQuestionConversationTurns {
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestion.Error()})
			return
		}
		started := false
		streamEncoder := json.NewEncoder(writer)
		err := groundedMarketQuestionStreamProvider.StreamGroundedMarketQuestion(request.Context(), payload, func(event marketanalysis.MarketQuestionStageEvent) error {
			if !started {
				writer.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
				writer.Header().Set("Cache-Control", "no-store")
				writer.WriteHeader(http.StatusOK)
				started = true
			}
			if err := streamEncoder.Encode(event); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		})
		if started {
			return
		}
		switch {
		case errors.Is(err, marketanalysis.ErrMarketQuestionACDisabled):
			writeJSON(writer, http.StatusNotFound, map[string]string{"detail": err.Error()})
		case errors.Is(err, marketanalysis.ErrRestrictedLLMQuestion), errors.Is(err, marketanalysis.ErrInvalidMarketQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": err.Error()})
		case err != nil:
			logger.Error("grounded market question stream failed before quantitative evidence", "error", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "grounded market question stream is unavailable"})
		default:
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "grounded market question stream ended without evidence"})
		}
	})
	mux.HandleFunc("GET /api/market-share/grounded-question/evidence/{bundle_id}", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if marketQuestionEvidenceProvider == nil {
			unavailable(writer)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		bundle, err := marketQuestionEvidenceProvider.MarketQuestionEvidenceBundle(ctx, request.PathValue("bundle_id"))
		switch {
		case errors.Is(err, marketanalysis.ErrMarketQuestionEvidenceUnavailable):
			writeJSON(writer, http.StatusNotFound, map[string]string{"detail": err.Error()})
		case err != nil:
			logger.Error("retained market question evidence lookup failed", "error", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "retained market question evidence is unavailable"})
		default:
			writeRequestJSON(writer, request, http.StatusOK, bundle)
		}
	})
	mux.HandleFunc("POST /api/market-share/grounded-question-feedback", func(writer http.ResponseWriter, request *http.Request) {
		if marketQuestionFeedbackProvider == nil {
			unavailable(writer)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 2_048)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var feedback marketanalysis.MarketQuestionFeedback
		if err := decoder.Decode(&feedback); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "invalid market question feedback JSON"})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "market question feedback request must contain one JSON object"})
			return
		}
		feedback, err := feedback.NormalizeAndValidate()
		if err != nil {
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestionFeedback.Error()})
			return
		}
		err = marketQuestionFeedbackProvider.SubmitMarketQuestionFeedback(request.Context(), feedback)
		switch {
		case errors.Is(err, marketanalysis.ErrInvalidMarketQuestionFeedback):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestionFeedback.Error()})
		case errors.Is(err, marketanalysis.ErrMarketQuestionAttemptNotFound):
			writeJSON(writer, http.StatusNotFound, map[string]string{"detail": marketanalysis.ErrMarketQuestionAttemptNotFound.Error()})
		case errors.Is(err, marketanalysis.ErrMarketQuestionLearningDisabled):
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": marketanalysis.ErrMarketQuestionLearningDisabled.Error()})
		case err != nil:
			logRequestFailure(logger, request, "market question feedback failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "market question feedback is unavailable"})
		default:
			writer.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /api/market-share/hermes-notice-question", func(writer http.ResponseWriter, request *http.Request) {
		if hermesNoticeQuestionProvider == nil {
			unavailable(writer)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 4_096)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var payload struct {
			Persona          string   `json:"persona"`
			Question         string   `json:"question"`
			ContextSourceIDs []string `json:"context_source_ids"`
		}
		if err := decoder.Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "invalid Hermes notice question JSON"})
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "Hermes notice question request must contain one JSON object"})
			return
		}
		answer, err := hermesNoticeQuestionProvider.AskHermesNoticeQuestion(request.Context(), payload.Persona, payload.Question, payload.ContextSourceIDs)
		switch {
		case errors.Is(err, marketanalysis.ErrRestrictedLLMQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrRestrictedLLMQuestion.Error()})
		case errors.Is(err, marketanalysis.ErrInvalidMarketQuestion):
			writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": marketanalysis.ErrInvalidMarketQuestion.Error()})
		case err != nil:
			logRequestFailure(logger, request, "Hermes domestic notice question failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "Hermes domestic notice question is unavailable"})
		default:
			writeRequestJSON(writer, request, http.StatusOK, answer)
		}
	})
	mux.HandleFunc("GET /api/market-share", func(writer http.ResponseWriter, request *http.Request) {
		if snapshotProvider == nil {
			unavailable(writer)
			return
		}
		includeMarkets := false
		if raw := strings.TrimSpace(request.URL.Query().Get("include_markets")); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				unprocessable(writer, "include_markets must be a boolean")
				return
			}
			includeMarkets = parsed
		}
		payload, err := snapshotProvider.Snapshot(request.Context(), includeMarkets)
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/registry-history", func(writer http.ResponseWriter, request *http.Request) {
		if registryHistoryProvider == nil {
			unavailable(writer)
			return
		}
		fromMS, fromOK := queryInt64(request, "from_ms", 0)
		toMS, toOK := queryInt64(request, "to_ms", 0)
		limit, limitOK := queryInt64(request, "limit", 100)
		if !fromOK || !toOK || fromMS < 0 || toMS < 0 ||
			fromMS > 0 && toMS > 0 && fromMS > toMS {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "from_ms and to_ms must form a valid Unix-millisecond range"})
			return
		}
		if !limitOK || limit < 1 || limit > 200 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "limit must be between 1 and 200"})
			return
		}
		exchangeName := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("exchange")))
		if exchangeName == "all" {
			exchangeName = ""
		}
		var exchange domain.Exchange
		if exchangeName != "" {
			parsed, err := domain.ParseExchange(exchangeName)
			if err != nil {
				writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "exchange is unsupported"})
				return
			}
			exchange = parsed
		}
		eventType := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("event_type")))
		if eventType != "" && eventType != "DISCOVERED" && eventType != "ADDED" && eventType != "REMOVED" {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "event_type must be DISCOVERED, ADDED, or REMOVED"})
			return
		}
		filter := registry.HistoryFilter{
			FromMS: fromMS, ToMS: toMS, Exchange: exchange,
			EventType: eventType,
			Asset:     strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("asset"))),
			Limit:     int(limit),
		}
		payload, err := registryHistoryProvider.RegistryHistory(request.Context(), filter)
		if err != nil {
			logRequestFailure(logger, request, "market registry history failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "market registry history is unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/reconciliation/latest", func(writer http.ResponseWriter, request *http.Request) {
		if reconciliationProvider == nil {
			unavailable(writer)
			return
		}
		payload, err := reconciliationProvider.ReconciliationLatest(request.Context())
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/official-movements", func(writer http.ResponseWriter, request *http.Request) {
		if officialMovementProvider == nil {
			unavailable(writer)
			return
		}
		limit, ok := queryInt64(request, "limit", 20)
		if !ok || limit < 1 || limit > 200 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "limit must be between 1 and 200"})
			return
		}
		items, err := officialMovementProvider.OfficialMovementEvents(request.Context(), int(limit))
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	})
	mux.HandleFunc("GET /api/market-share/official-aligned-history", func(writer http.ResponseWriter, request *http.Request) {
		if officialAlignedHistoryProvider == nil {
			unavailable(writer)
			return
		}
		fromMS, fromOK := queryInt64(request, "from_ms", 0)
		toMS, toOK := queryInt64(request, "to_ms", 0)
		intervalMS := tickertrend.DefaultInterval * 1_000
		if !fromOK || !toOK || fromMS <= 0 || toMS < fromMS ||
			fromMS%intervalMS != 0 || toMS%intervalMS != 0 ||
			(toMS-fromMS)/intervalMS+1 > tickertrend.MaxHistoryPoints {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "from_ms and to_ms must form an exact-hour range of at most 1000 points"})
			return
		}
		payload, err := officialAlignedHistoryProvider.OfficialAlignedHistory(request.Context(), fromMS, toMS)
		if err != nil {
			logRequestFailure(logger, request, "official aligned history failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "official aligned history is unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/official-aligned-contribution", func(writer http.ResponseWriter, request *http.Request) {
		if officialAlignedContributionProvider == nil {
			unavailable(writer)
			return
		}
		toMS, ok := queryInt64(request, "to_ms", 0)
		intervalMS := tickertrend.DefaultInterval * 1_000
		if !ok || toMS <= 0 || toMS%intervalMS != 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "to_ms must be a positive exact UTC-hour Unix timestamp in milliseconds"})
			return
		}
		payload, err := officialAlignedContributionProvider.OfficialAlignedContribution(request.Context(), toMS)
		if err != nil {
			logRequestFailure(logger, request, "official aligned contribution failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "official aligned contribution is unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/window-changes", func(writer http.ResponseWriter, request *http.Request) {
		if windowChangesProvider == nil {
			unavailable(writer)
			return
		}
		marketScope, ok := queryMarketScope(request)
		if !ok {
			badMarketScope(writer)
			return
		}
		payload, err := windowChangesProvider.WindowChanges(request.Context(), marketScope)
		if err != nil {
			logRequestFailure(logger, request, "window changes failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "window changes are unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/coin-shares", func(writer http.ResponseWriter, request *http.Request) {
		if coinSharesProvider == nil {
			unavailable(writer)
			return
		}
		window, ok := queryInt64(request, "window", 600)
		if !ok {
			unprocessable(writer, "window must be an integer")
			return
		}
		if !validWindow(window) {
			badWindow(writer)
			return
		}
		marketScope, ok := queryMarketScope(request)
		if !ok {
			badMarketScope(writer)
			return
		}
		limit, ok := queryInt64(request, "limit", marketanalysis.DefaultCoinShareCount)
		if !ok || limit < 1 || limit > marketanalysis.MaxCoinShareCount {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": fmt.Sprintf("limit must be between 1 and %d", marketanalysis.MaxCoinShareCount)})
			return
		}
		payload, err := coinSharesProvider.CoinShares(request.Context(), window, marketScope, int(limit))
		if err != nil {
			logRequestFailure(logger, request, "coin shares failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "coin shares are unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/closed-seconds", func(writer http.ResponseWriter, request *http.Request) {
		if closedSecondsProvider == nil {
			unavailable(writer)
			return
		}
		window, ok := queryInt64(request, "window", 10)
		if !ok || (window != 10 && window != 30 && window != 60) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "window must be 10, 30 or 60"})
			return
		}
		marketScope, ok := queryMarketScope(request)
		if !ok {
			badMarketScope(writer)
			return
		}
		seconds, err := closedSecondsProvider.ClosedSeconds(request.Context(), window, marketScope)
		if err != nil {
			logRequestFailure(logger, request, "closed seconds failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "closed seconds are unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, map[string]any{
			"window_sec": window, "market_scope": marketScope, "seconds": seconds,
		})
	})
	mux.HandleFunc("GET /api/market-share/share-series", func(writer http.ResponseWriter, request *http.Request) {
		if shareSeriesProvider == nil {
			unavailable(writer)
			return
		}
		bucketSec, ok := queryInt64(request, "bucket_sec", 60)
		if !ok || (bucketSec != 60 && bucketSec != 600 && bucketSec != 1_800) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "bucket_sec must be 60, 600 or 1800"})
			return
		}
		marketScope, ok := queryMarketScope(request)
		if !ok {
			badMarketScope(writer)
			return
		}
		payload, err := shareSeriesProvider.ShareSeries(request.Context(), bucketSec, marketScope)
		if err != nil {
			logRequestFailure(logger, request, "share series failed", err)
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "share series is unavailable"})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/official-movements/{eventID}", func(writer http.ResponseWriter, request *http.Request) {
		if officialMovementProvider == nil {
			unavailable(writer)
			return
		}
		event, err := officialMovementProvider.OfficialMovementEvent(
			request.Context(), strings.TrimSpace(request.PathValue("eventID")),
		)
		if errors.Is(err, movement.ErrEventNotFound) {
			writeJSON(writer, http.StatusNotFound, map[string]string{"detail": "official movement not found"})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, event)
	})
	mux.HandleFunc("GET /api/market-share/realtime-history", func(writer http.ResponseWriter, request *http.Request) {
		if realtimeFlowProvider == nil {
			unavailable(writer)
			return
		}
		fromMS, fromOK := queryInt64(request, "from_ms", 0)
		toMS, toOK := queryInt64(request, "to_ms", 0)
		if !fromOK || !toOK || fromMS < 0 || toMS < 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "from_ms and to_ms must be positive Unix timestamps in milliseconds"})
			return
		}
		if fromMS > 0 && toMS > 0 && fromMS > toMS {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "from_ms must not be later than to_ms"})
			return
		}
		status := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("status")))
		if status != "" && !validRealtimeFlowStatus(status) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "status must be WARMING, NORMAL, WATCH, ALERT, or DEGRADED"})
			return
		}
		limit, limitOK := queryInt64(request, "limit", 100)
		if !limitOK || limit < 1 || limit > 1_000 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "limit must be between 1 and 1000"})
			return
		}
		asset := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("asset")))
		exchange := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("exchange")))
		if len(asset) > 64 || len(exchange) > 64 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "asset and exchange filters must be at most 64 characters"})
			return
		}
		items, err := realtimeFlowProvider.RealtimeFlowHistory(request.Context(), marketanalysis.RealtimeFlowHistoryFilter{
			FromMS: fromMS, ToMS: toMS, Status: status, Asset: asset, Exchange: exchange, Limit: int(limit),
		})
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		// The history comes from a shared memo; publish rounded copies only.
		published := make([]marketanalysis.RealtimeFlowState, len(items))
		for index, item := range items {
			published[index] = marketanalysis.PublishedRealtimeFlowState(item)
		}
		writeRequestJSON(writer, request, http.StatusOK, map[string]any{
			"items": published, "count": len(published), "contract_version": marketanalysis.RealtimeFlowVersion,
		})
	})
	mux.HandleFunc("GET /api/market-share/realtime-summary", func(writer http.ResponseWriter, request *http.Request) {
		if realtimeFlowSummaryProvider == nil {
			unavailable(writer)
			return
		}
		toMS, toOK := queryInt64(request, "to_ms", 0)
		windowSec, windowOK := queryInt64(request, "window_sec", 86_400)
		if !toOK || toMS <= 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "to_ms must be a positive Unix timestamp in milliseconds"})
			return
		}
		if !windowOK || windowSec != 86_400 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "window_sec must be 86400"})
			return
		}
		// Optional: only the episodes a screen shows. Totals, drivers and
		// coverage still describe the whole 24 hours.
		episodesFromMS, fromOK := queryInt64(request, "episodes_from_ms", 0)
		episodeLimit, limitOK := queryInt64(request, "episode_limit", 0)
		if !fromOK || episodesFromMS < 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "episodes_from_ms must be a non-negative Unix timestamp in milliseconds"})
			return
		}
		if !limitOK || episodeLimit < 0 || episodeLimit > 10_000 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "episode_limit must be between 1 and 10000"})
			return
		}
		payload, err := realtimeFlowSummaryProvider.RealtimeFlowPeriodSummary(request.Context(), toMS, windowSec)
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		if episodesFromMS > 0 || episodeLimit > 0 {
			payload = marketanalysis.SelectRealtimeFlowEpisodes(payload, episodesFromMS, int(episodeLimit))
		}
		// The summary is memoized; publish a rounded copy, never the shared value.
		writeRealtimeSummaryRequestJSON(writer, request, http.StatusOK,
			marketanalysis.PublishedRealtimeFlowPeriodSummary(payload), realtimeSummaryResponses)
	})
	mux.HandleFunc("GET /api/market-share/markets", func(writer http.ResponseWriter, request *http.Request) {
		if marketRowsProvider == nil {
			unavailable(writer)
			return
		}
		window, ok := queryInt64(request, "window", 86400)
		if !ok {
			unprocessable(writer, "window must be an integer")
			return
		}
		if !validWindow(window) {
			badWindow(writer)
			return
		}
		payload, err := marketRowsProvider.MarketRows(request.Context(), window, request.URL.Query().Get("scope"), request.URL.Query().Get("exchange"))
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/calendar-day", func(writer http.ResponseWriter, request *http.Request) {
		if calendarDayProvider == nil {
			unavailable(writer)
			return
		}
		day := strings.TrimSpace(request.URL.Query().Get("day"))
		if day == "" {
			day = time.Now().In(seoulLocation).Format(time.DateOnly)
		}
		payload, err := calendarDayProvider.CalendarDay(request.Context(), day, request.URL.Query().Get("market_scope"))
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/liquidity", func(writer http.ResponseWriter, request *http.Request) {
		if liquidityProvider == nil {
			unavailable(writer)
			return
		}
		notional, ok := queryNotional(request)
		if !ok {
			badNotional(writer)
			return
		}
		payload, err := liquidityProvider.LiquidityRows(request.Context(), notional, request.URL.Query().Get("scope"), request.URL.Query().Get("exchange"), request.URL.Query().Get("asset"))
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/market-share/orderbook/{exchange}/{market}", func(writer http.ResponseWriter, request *http.Request) {
		if orderBookProvider == nil {
			unavailable(writer)
			return
		}
		notional, ok := queryNotional(request)
		if !ok {
			badNotional(writer)
			return
		}
		payload, err := orderBookProvider.OrderBookDetail(request.Context(), request.PathValue("exchange"), request.PathValue("market"), notional)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, payload)
	})
	mux.HandleFunc("GET /api/ws/market-share", func(writer http.ResponseWriter, request *http.Request) {
		serveMarketShareWebSocket(
			writer, request, sharedAnalyses, sharedTicks, sharedTickReferences,
			realtimeFlowStreams{full: sharedRealtimeFlow, compact: sharedRealtimeFlowCompact, strip: sharedRealtimeFlowStrip},
			sharedWhaleAlerts, sharedCoinShares, noticeProvider, logger,
		)
	})
	return recoverMiddleware(logger, securityHeaders(mux))
}

// realtimeFlowWithoutSeries is the realtime flow without the one-second share
// series (60 points, about 29 KB of each 65 KB frame), for screens that do not
// draw it.
type realtimeFlowWithoutSeries struct {
	marketanalysis.RealtimeFlowState
	RecentShare1S any `json:"recent_share_1s,omitempty"`
}

// realtimeFlowStrip is the realtime flow as the main screen's pages without
// its charts read it (the live strip under the menu and the window cards): its
// windows, status and leading coin, about 4 KB of each 65 KB frame. The driver
// counts still describe the whole flow.
type realtimeFlowStrip struct {
	realtimeFlowWithoutSeries
	TenSecondAttribution any                                 `json:"ten_second_attribution,omitempty"`
	CurrentAssets        any                                 `json:"current_assets,omitempty"`
	ExchangeDrivers      any                                 `json:"exchange_drivers,omitempty"`
	Drivers              []marketanalysis.RealtimeFlowDriver `json:"drivers"`
}

func compactRealtimeFlow(state marketanalysis.RealtimeFlowState) realtimeFlowWithoutSeries {
	state.RecentShare1S = nil
	return realtimeFlowWithoutSeries{RealtimeFlowState: marketanalysis.PublishedRealtimeFlowState(state)}
}

func stripRealtimeFlow(state marketanalysis.RealtimeFlowState) realtimeFlowStrip {
	// Omit unread fields before the detached publication copy and rounding.
	// Copying sixty seconds of maps just to discard them dominates this path.
	state.RecentShare1S, state.TenSecondAttribution = nil, nil
	state.CurrentAssets, state.ExchangeDrivers = nil, nil
	state.Drivers = state.Drivers[:min(1, len(state.Drivers))]
	published := marketanalysis.PublishedRealtimeFlowState(state)
	return realtimeFlowStrip{
		realtimeFlowWithoutSeries: realtimeFlowWithoutSeries{RealtimeFlowState: published},
		Drivers:                   published.Drivers,
	}
}

// realtimeFlowStreams picks the once-a-second realtime flow a WebSocket client
// asked for with realtime_flow: "none" for screens that never read it,
// "compact" for the flow without the one-second series, "strip" for what the
// main screen's pages without its charts read (realtimeFlowStrip); anything
// else keeps the whole flow, as for clients that predate the parameter.
type realtimeFlowStreams struct {
	full, compact, strip *stateBroadcaster
}

func (s realtimeFlowStreams) forRequest(request *http.Request) *stateBroadcaster {
	switch strings.ToLower(strings.TrimSpace(request.URL.Query().Get("realtime_flow"))) {
	case "none":
		return nil
	case "compact":
		return s.compact
	case "strip":
		return s.strip
	default:
		return s.full
	}
}

func bearerToken(header string) (string, bool) {
	prefix, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(prefix, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func isLoopbackRequest(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeRequestJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"detail": "response serialization failed"})
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Add("Vary", "Accept-Encoding")
	if acceptsGzip(request) && len(payload) >= 1024 {
		if compressed, gzipErr := gzipPayload(payload); gzipErr == nil {
			writer.Header().Set("Content-Encoding", "gzip")
			payload = compressed
		}
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}

func writeAnalysisRequestJSON(writer http.ResponseWriter, request *http.Request, status int, value marketanalysis.Analysis, cache *analysisResponseCache) {
	if cache == nil {
		writeRequestJSON(writer, request, status, value)
		return
	}
	wantGzip := acceptsGzip(request)
	encoding, err := cache.encode(value, wantGzip)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"detail": "response serialization failed"})
		return
	}
	payload := encoding.plain
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Add("Vary", "Accept-Encoding")
	if wantGzip && len(encoding.gzip) > 0 {
		writer.Header().Set("Content-Encoding", "gzip")
		payload = encoding.gzip
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}

func queryInt64(request *http.Request, name string, fallback int64) (int64, bool) {
	value := strings.TrimSpace(request.URL.Query().Get(name))
	if value == "" {
		return fallback, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}

func validWindow(window int64) bool {
	return aggregate.SupportsWindow(window)
}

func activeNoticeNetworkWindowAllowed(window int64) bool {
	switch window {
	case 60, 300, 600, 900, 1_800, 3_600, 7_200, 10_800, 21_600, 43_200, 86_400:
		return true
	default:
		return false
	}
}

func validRealtimeFlowStatus(status string) bool {
	switch status {
	case "WARMING", "NORMAL", "WATCH", "ALERT", "DEGRADED":
		return true
	default:
		return false
	}
}

func validNotional(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	value, err := strconv.ParseFloat(raw, 64)
	return err == nil && value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func queryNotional(request *http.Request) (float64, bool) {
	raw := strings.TrimSpace(request.URL.Query().Get("notional_krw"))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseFloat(raw, 64)
	return value, err == nil && value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func unavailable(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "market-share service is not ready"})
}

// logRequestFailure logs a provider call that failed while serving request.
// When the client has already gone away (a browser closed the page or moved
// on, a question was cancelled) the request context is cancelled and the
// provider reports exactly that. Nothing failed on the server, so it is logged
// at debug level instead of as an error; the response written afterwards goes
// nowhere and is harmless.
func logRequestFailure(logger *slog.Logger, request *http.Request, message string, err error) {
	if errors.Is(err, context.Canceled) && request.Context().Err() != nil {
		logger.Debug(message, "error", err, "client_canceled", true)
		return
	}
	logger.Error(message, "error", err)
}
func badWindow(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "window must be one of " + fmt.Sprint(aggregate.DefaultWindows)})
}

// queryMarketScope reads market_scope, which defaults to KRW.
func queryMarketScope(request *http.Request) (string, bool) {
	scope := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("market_scope")))
	if scope == "" {
		scope = "KRW"
	}
	return scope, scope == "KRW" || scope == "ALL_DOMESTIC_CONVERTED"
}

func badMarketScope(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusBadRequest, map[string]string{"detail": "market_scope must be one of ('KRW', 'ALL_DOMESTIC_CONVERTED')"})
}
func badNotional(writer http.ResponseWriter) {
	unprocessable(writer, "notional_krw must be finite and greater than zero")
}
func unprocessable(writer http.ResponseWriter, detail string) {
	writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"detail": detail})
}

func acceptsGzip(request *http.Request) bool {
	wildcard := false
	for _, header := range request.Header.Values("Accept-Encoding") {
		for _, value := range strings.Split(header, ",") {
			parts := strings.Split(value, ";")
			coding := strings.TrimSpace(parts[0])
			if !strings.EqualFold(coding, "gzip") && coding != "*" {
				continue
			}
			quality := 1.0
			for _, parameter := range parts[1:] {
				name, raw, found := strings.Cut(parameter, "=")
				if strings.EqualFold(strings.TrimSpace(name), "q") {
					parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
					if !found || err != nil || math.IsNaN(parsed) || parsed < 0 || parsed > 1 {
						quality = 0
						break
					}
					quality = parsed
				}
			}
			if strings.EqualFold(coding, "gzip") {
				return quality > 0
			}
			wildcard = quality > 0
		}
	}
	return wildcard
}

func serveMarketShareWebSocket(
	writer http.ResponseWriter,
	request *http.Request,
	analyses *analysisBroadcaster,
	ticks *tickBroadcaster,
	tickReferences *stateBroadcaster,
	realtimeFlowStreams realtimeFlowStreams,
	whaleAlerts *stateBroadcaster,
	coinShares *stateBroadcaster,
	notices NoticeProvider,
	logger *slog.Logger,
) {
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		// The proxy forwards these frames over the public network. Compress large
		// snapshots without retaining a compression window per browser. Peers
		// without extension support still receive the same uncompressed JSON.
		CompressionMode:      websocket.CompressionNoContextTakeover,
		CompressionThreshold: 4096,
	})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	window, windowOK := queryInt64(request, "window", 60)
	scope, scopeOK := queryMarketScope(request)
	view := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("view")))
	projection := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("projection")))
	if projection != dashboardAnalysisProjection && projection != detailAnalysisProjection && projection != briefAnalysisProjection && projection != screenAnalysisProjection {
		projection = ""
	}
	// Keep projection admission identical to the encoding layer, including
	// notice subscription decisions for the additive shell projection.
	_, projection = normalizeProjectedAnalysisView(view, projection)
	if view == "" {
		view = "full"
	}
	if !windowOK || !validWindow(window) || !validNotional(request.URL.Query().Get("notional_krw")) || !scopeOK {
		_ = connection.Close(websocket.StatusCode(4400), "invalid market-share parameters")
		return
	}
	session := &websocketSession{started: time.Now()}
	connectionID := strconv.FormatInt(session.started.UnixMilli(), 36) + "-" + strconv.FormatUint(websocketSessionSequence.Add(1), 36)
	logger = logger.With("connection_id", connectionID, "view", view, "window", window)
	logger.Info("websocket session opened")
	defer session.logEnd(logger)
	ctx, cancel := context.WithCancel(context.WithValue(request.Context(), websocketSessionKey{}, session))
	defer cancel()
	// This is a server-push stream, but control frames still need a reader.
	// Answer ping/close frames and release subscriptions when the peer leaves.
	go func() {
		_, _, readErr := connection.Reader(ctx)
		if readErr == nil {
			_ = connection.Close(websocket.StatusPolicyViolation, "unexpected data message")
		} else if ctx.Err() == nil {
			session.recordPeer(readErr)
			logger.Info("websocket peer disconnected", "view", view, "window", window,
				"close_code", websocket.CloseStatus(readErr), "error", readErr)
		}
		cancel()
	}()
	wantsAnalysis := !(view == "events" && projection == screenAnalysisProjection)
	if (wantsAnalysis && (analyses == nil || analyses.provider == nil)) || ticks == nil || ticks.provider == nil {
		_ = writeWebSocketJSON(ctx, connection, map[string]any{
			"type": "market_share_unavailable", "generated_at": float64(time.Now().UnixNano()) / 1e9,
		})
		return
	}
	tickUpdates, unsubscribeTicks := ticks.subscribe(window, scope)
	defer unsubscribeTicks()
	notional, _ := queryNotional(request)
	var analysisUpdates <-chan sharedAnalysisFrame
	if wantsAnalysis {
		var unsubscribeAnalyses func()
		analysisUpdates, unsubscribeAnalyses = analyses.subscribe(window, scope, notional, view)
		defer unsubscribeAnalyses()
	}
	var tickReferenceUpdates, realtimeFlowUpdates, whaleAlertUpdates, coinShareUpdates <-chan sharedStateFrame
	unsubscribeTickReferences := func() {}
	unsubscribeRealtimeFlow := func() {}
	unsubscribeWhaleAlerts := func() {}
	unsubscribeCoinShares := func() {}
	if tickReferences != nil {
		tickReferenceUpdates, unsubscribeTickReferences = tickReferences.subscribe()
	}
	if realtimeFlow := realtimeFlowStreams.forRequest(request); realtimeFlow != nil {
		realtimeFlowUpdates, unsubscribeRealtimeFlow = realtimeFlow.subscribe()
	}
	if whaleAlerts != nil {
		whaleAlertUpdates, unsubscribeWhaleAlerts = whaleAlerts.subscribe()
	}
	// Only a screen that draws the coins' live shares asks for them.
	if coinShares != nil && strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("coin_shares")), "live") {
		coinShareUpdates, unsubscribeCoinShares = coinShares.subscribe()
	}
	defer unsubscribeTickReferences()
	defer unsubscribeRealtimeFlow()
	defer unsubscribeWhaleAlerts()
	defer unsubscribeCoinShares()
	type noticeResult struct {
		payload any
		err     error
	}
	noticeResults := make(chan noticeResult, 1)
	noticeRunning := false
	// New notice workspaces receive notices only in the versioned analysis.
	// Legacy clients retain their independent warmup snapshot stream.
	wantsNotices := view == "events" && projection != screenAnalysisProjection &&
		!strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("notice_snapshot")), "none")
	var noticeSchedule <-chan time.Time
	var lastTickVersion uint64
	var lastTickIdentity tickIdentity
	haveTickVersion := false
	var lastNoticeVersion int64 = -1
	var lastAnalysisNoticeVersion int64 = -1
	startNotice := func() {
		if noticeRunning || !wantsNotices || notices == nil {
			return
		}
		noticeRunning = true
		go func() {
			payload, noticeErr := notices.NoticeSnapshot(ctx)
			select {
			case noticeResults <- noticeResult{payload: payload, err: noticeErr}:
			case <-ctx.Done():
			}
		}()
	}
	deliverAnalysis := func(result sharedAnalysisFrame) error {
		if result.err != nil {
			if err := writeWebSocketJSON(ctx, connection, map[string]any{
				"type": "market_share_analysis_error", "generated_at": float64(time.Now().UnixNano()) / 1e9,
				"detail": boundedTickErrorDetail(result.err.Error()),
			}); err != nil {
				return err
			}
			return nil
		}
		noticeVersion := result.payload.Notices.Version
		partialAnalysis := lastAnalysisNoticeVersion >= 0 && noticeVersion == lastAnalysisNoticeVersion
		message := result.projections.messageProjected(view, projection, partialAnalysis, lastTickVersion, result.state)
		if err := writeSharedJSONValue(ctx, connection, message); err != nil {
			return err
		}
		lastAnalysisNoticeVersion = noticeVersion
		return nil
	}
	deliverNotice := func(result noticeResult) error {
		if result.err != nil {
			logger.Debug("websocket notice snapshot failed", "error", result.err)
			noticeSchedule = time.After(websocketNoticeInterval)
			return nil
		}
		if version, ok := messageVersion(result.payload); ok && version != lastNoticeVersion {
			wireSnapshot := limitWebSocketNoticeSnapshot(result.payload, websocketNoticeItemLimit)
			if err := writeWebSocketJSON(ctx, connection, map[string]any{
				"type": "market_share_notice_snapshot", "generated_at": float64(time.Now().UnixNano()) / 1e9,
				"notices": wireSnapshot,
			}); err != nil {
				return err
			}
			lastNoticeVersion = version
		}
		noticeSchedule = time.After(websocketNoticeInterval)
		return nil
	}

	// Start expensive views independently of the shared tick producer. Their
	// first frames are held briefly so a healthy immediate tick keeps the
	// historical tick-first wire ordering. If the tick producer stalls, the
	// bounded gate opens and analysis/notices still reach the client.
	startNotice()
	frameGate := time.NewTimer(initialWebSocketFrameGrace)
	defer frameGate.Stop()
	frameGateC := frameGate.C
	noticeGate := time.NewTimer(initialWebSocketFrameGrace)
	defer noticeGate.Stop()
	var noticeGateC <-chan time.Time
	initialNoticeComplete := !wantsNotices || notices == nil
	if !initialNoticeComplete {
		noticeGateC = noticeGate.C
	} else if !noticeGate.Stop() {
		select {
		case <-noticeGate.C:
		default:
		}
	}
	gateOpen := false
	noticeGateOpen := initialNoticeComplete
	var pendingAnalysis *sharedAnalysisFrame
	var pendingNotice *noticeResult
	var pendingTickReferences *sharedStateFrame
	var pendingRealtimeFlow *sharedStateFrame
	var pendingWhaleAlerts *sharedStateFrame
	var pendingCoinShares *sharedStateFrame
	flushPending := func() error {
		if !gateOpen {
			return nil
		}
		if pendingNotice != nil {
			result := *pendingNotice
			pendingNotice = nil
			initialNoticeComplete = true
			if err := deliverNotice(result); err != nil {
				return err
			}
		}
		if pendingTickReferences != nil {
			frame := *pendingTickReferences
			pendingTickReferences = nil
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return err
			}
		}
		if pendingRealtimeFlow != nil {
			frame := *pendingRealtimeFlow
			pendingRealtimeFlow = nil
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return err
			}
		}
		if pendingWhaleAlerts != nil {
			frame := *pendingWhaleAlerts
			pendingWhaleAlerts = nil
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return err
			}
		}
		if pendingCoinShares != nil {
			frame := *pendingCoinShares
			pendingCoinShares = nil
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return err
			}
		}
		if pendingAnalysis != nil && (initialNoticeComplete || noticeGateOpen) {
			result := *pendingAnalysis
			pendingAnalysis = nil
			if err := deliverAnalysis(result); err != nil {
				return err
			}
		}
		return nil
	}
	transportHeartbeat := time.NewTicker(websocketTransportHeartbeatInterval)
	defer transportHeartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-transportHeartbeat.C:
			if err := writeWebSocketJSON(ctx, connection, map[string]any{"type": "market_share_transport_heartbeat", "connection_id": connectionID}); err != nil {
				return
			}
		case frame := <-tickUpdates:
			heartbeat := frame.available && frame.versioned && haveTickVersion && frame.identity == lastTickIdentity
			if frame.available && frame.versioned {
				lastTickVersion = frame.version
				lastTickIdentity = frame.identity
				haveTickVersion = true
			}
			if err := writeSharedTickFrame(ctx, connection, frame, heartbeat); err != nil {
				return
			}
			if !gateOpen {
				gateOpen = true
				frameGateC = nil
				if !frameGate.Stop() {
					select {
					case <-frameGate.C:
					default:
					}
				}
				if err := flushPending(); err != nil {
					return
				}
			}
		case frame := <-tickReferenceUpdates:
			if !gateOpen {
				pendingTickReferences = &frame
				continue
			}
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return
			}
		case frame := <-realtimeFlowUpdates:
			if !gateOpen {
				pendingRealtimeFlow = &frame
				continue
			}
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return
			}
		case frame := <-whaleAlertUpdates:
			if !gateOpen {
				pendingWhaleAlerts = &frame
				continue
			}
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return
			}
		case frame := <-coinShareUpdates:
			if !gateOpen {
				pendingCoinShares = &frame
				continue
			}
			if err := writeSharedStateFrame(ctx, connection, frame); err != nil {
				return
			}
		case result := <-analysisUpdates:
			if !gateOpen || (!initialNoticeComplete && !noticeGateOpen) {
				pendingAnalysis = &result
				continue
			}
			if err := deliverAnalysis(result); err != nil {
				return
			}
		case result := <-noticeResults:
			noticeRunning = false
			initialNoticeComplete = true
			if noticeGateC != nil {
				noticeGateC = nil
				if !noticeGate.Stop() {
					select {
					case <-noticeGate.C:
					default:
					}
				}
			}
			if !gateOpen {
				pendingNotice = &result
				continue
			}
			if err := deliverNotice(result); err != nil {
				return
			}
			if err := flushPending(); err != nil {
				return
			}
		case <-frameGateC:
			frameGateC = nil
			gateOpen = true
			if err := flushPending(); err != nil {
				return
			}
		case <-noticeGateC:
			noticeGateC = nil
			noticeGateOpen = true
			if err := flushPending(); err != nil {
				return
			}
		case <-noticeSchedule:
			noticeSchedule = nil
			startNotice()
		}
	}
}

func analysisUpdateData(payload *marketanalysis.Analysis, projected any, state map[string]any, tickVersion uint64, partial bool) map[string]any {
	if payload == nil {
		payload = &marketanalysis.Analysis{}
	}
	result := make(map[string]any, len(state)+6)
	for key, value := range state {
		result[key] = value
	}
	result["version"] = payload.Context.AnalysisVersion
	result["tick_version"] = tickVersion
	result["generated_at"] = payload.GeneratedAt
	result["partial_analysis"] = partial
	result["analysis"] = projected
	return result
}

func projectAnalysis(payload marketanalysis.Analysis, view string, omitNotices bool) any {
	return projectAnalysisAt(payload, view, omitNotices, time.Now())
}

func projectAnalysisAt(payload marketanalysis.Analysis, view string, omitNotices bool, now time.Time) any {
	projection := buildTypedAnalysisProjection(&payload, normalizeAnalysisView(view), now)
	if omitNotices {
		return projection.partial
	}
	return projection.full
}

const summary2RowLimit = 30

func noticeRelevant(item map[string]any, cutoffMS float64) bool {
	if noticeAnchorMS(item) >= cutoffMS {
		return true
	}
	if selected, _ := item["within_selected_window"].(bool); selected {
		return true
	}
	studies, _ := item["reaction_studies"].([]any)
	for _, raw := range studies {
		study, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		status, _ := study["status"].(string)
		if status == "MATURE" || status == "LIVE_ESTIMATE" || status == "INCOMPLETE_CONTIGUOUS_COVERAGE" {
			return true
		}
	}
	return false
}

func noticeAnchorMS(item map[string]any) float64 {
	precision, _ := item["timestamp_precision"].(string)
	if precision == "" {
		precision = "SECOND"
	}
	anchors := make([]float64, 0)
	if precision == "SECOND" {
		if published := numericValue(item["published_ts_ms"]); published > 0 {
			anchors = append(anchors, published)
		}
	}
	schedules, _ := item["schedules"].([]any)
	for _, raw := range schedules {
		schedule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"start_ts_ms", "end_ts_ms"} {
			if value := numericValue(schedule[field]); value > 0 {
				anchors = append(anchors, value)
			}
		}
	}
	if precision == "DETECTED" && len(anchors) == 0 {
		if detected := numericValue(item["detected_ts_ms"]); detected > 0 {
			anchors = append(anchors, detected)
		}
	}
	latest := 0.0
	for _, anchor := range anchors {
		if anchor > latest {
			latest = anchor
		}
	}
	return latest
}

func compactNotice(item map[string]any) map[string]any {
	precision := mapValue(item, "timestamp_precision", "SECOND")
	return map[string]any{
		"exchange":               mapValue(item, "exchange", ""),
		"notice_id":              mapValue(item, "notice_id", ""),
		"title":                  mapValue(item, "title", ""),
		"url":                    mapValue(item, "url", ""),
		"category":               mapValue(item, "category", ""),
		"published_ts_ms":        mapValue(item, "published_ts_ms", 0.0),
		"detected_ts_ms":         mapValue(item, "detected_ts_ms", 0.0),
		"timestamp_precision":    precision,
		"assets":                 mapValue(item, "assets", []any{}),
		"market_context":         mapValue(item, "market_context", []any{}),
		"summary":                mapValue(item, "summary", ""),
		"schedules":              mapValue(item, "schedules", []any{}),
		"within_selected_window": booleanValue(item["within_selected_window"]),
		"reaction_studies":       mapValue(item, "reaction_studies", []any{}),
	}
}

func mapValue(source map[string]any, key string, fallback any) any {
	value, found := source[key]
	if !found || value == nil {
		return fallback
	}
	return value
}

func booleanValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func numericValue(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case int32:
		return float64(number)
	case uint:
		return float64(number)
	case uint64:
		return float64(number)
	case uint32:
		return float64(number)
	default:
		return 0
	}
}

func selectMapKeys(source map[string]any, keys ...string) map[string]any {
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, found := source[key]; found {
			result[key] = value
		}
	}
	return result
}

const websocketNoticeItemLimit = 100

// limitWebSocketNoticeSnapshot keeps the HTTP history contract intact while
// avoiding sending rows the events client immediately discards. Snapshot
// metadata is preserved so source health and version tracking remain exact.
func limitWebSocketNoticeSnapshot(value any, limit int) any {
	if limit < 0 {
		limit = 0
	}
	limitTyped := func(snapshot notice.Snapshot) notice.Snapshot {
		if len(snapshot.Items) > limit {
			snapshot.Items = snapshot.Items[:limit]
		}
		if len(snapshot.ContextualItems) > limit {
			snapshot.ContextualItems = snapshot.ContextualItems[:limit]
		}
		return snapshot
	}
	switch snapshot := value.(type) {
	case notice.Snapshot:
		return limitTyped(snapshot)
	case *notice.Snapshot:
		if snapshot == nil {
			return snapshot
		}
		limited := limitTyped(*snapshot)
		return &limited
	case map[string]any:
		items, found := snapshot["items"]
		if !found {
			return snapshot
		}
		limited := make(map[string]any, len(snapshot))
		for key, item := range snapshot {
			limited[key] = item
		}
		switch rows := items.(type) {
		case []any:
			if len(rows) > limit {
				limited["items"] = rows[:limit]
			}
		case []notice.Item:
			if len(rows) > limit {
				limited["items"] = rows[:limit]
			}
		}
		return limited
	default:
		return value
	}
}

func messageVersion(value any) (int64, bool) {
	switch snapshot := value.(type) {
	case notice.Snapshot:
		return snapshot.Version, true
	case *notice.Snapshot:
		if snapshot != nil {
			return snapshot.Version, true
		}
	case map[string]any:
		switch version := snapshot["version"].(type) {
		case int64:
			return version, true
		case int:
			return int64(version), true
		case uint64:
			if version <= math.MaxInt64 {
				return int64(version), true
			}
		case float64:
			if version >= math.MinInt64 && version <= math.MaxInt64 && version == float64(int64(version)) {
				return int64(version), true
			}
		}
	}
	return 0, false
}

func writeSharedTickFrame(ctx context.Context, connection *websocket.Conn, frame sharedTickFrame, heartbeat bool) error {
	if len(frame.payload) < 2 || frame.payload[0] != '{' {
		return errors.New("shared tick frame must contain a JSON object")
	}
	messageType := frame.messageType
	if messageType == "" {
		messageType = sharedTickMessage
	}
	if heartbeat && frame.available {
		message := make([]byte, 0, 112)
		message = append(message, `{"type":"`...)
		message = append(message, messageType...)
		message = append(message, `","heartbeat":true,"version":`...)
		message = strconv.AppendUint(message, frame.version, 10)
		message = append(message, `,"generated_at":`...)
		message = strconv.AppendFloat(message, float64(time.Now().UnixNano())/1e9, 'f', 6, 64)
		message = append(message, '}')
		writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
		defer cancel()
		return writeWebSocketPayload(writeCtx, connection, message)
	}
	message := frame.message
	if len(message) == 0 {
		message = typedObjectMessage(messageType, frame.payload)
	}
	if len(message) == 0 {
		return errors.New("shared tick frame envelope is invalid")
	}
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
	defer cancel()
	return writeWebSocketPayload(writeCtx, connection, message)
}

func writeSharedStateFrame(ctx context.Context, connection *websocket.Conn, frame sharedStateFrame) error {
	if len(frame.payload) < 2 || frame.payload[0] != '{' || frame.messageType == "" || frame.field == "" {
		return errors.New("shared state frame is invalid")
	}
	message := frame.message
	if len(message) == 0 {
		message = typedStateMessage(frame.messageType, frame.field, frame.payload)
	}
	if len(message) == 0 {
		return errors.New("shared state frame envelope is invalid")
	}
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
	defer cancel()
	return writeWebSocketPayload(writeCtx, connection, message)
}

func writeWebSocketJSON(ctx context.Context, connection *websocket.Conn, value any) error {
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
	defer cancel()
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeWebSocketPayload(writeCtx, connection, payload)
}

func writeSharedJSONValue(ctx context.Context, connection *websocket.Conn, value *sharedJSONValue) error {
	if value == nil {
		return errors.New("shared JSON value is nil")
	}
	payload, err := value.bytes()
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
	defer cancel()
	return writeWebSocketPayload(writeCtx, connection, payload)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(writer, request)
	})
}

func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("http panic", "method", request.Method, "path", request.URL.Path, "error", recovered)
				writeJSON(writer, http.StatusInternalServerError, map[string]string{"detail": "internal server error"})
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

// Log write failures without payloads or request credentials. A deadline here
// distinguishes a blocked outgoing socket from a browser's explicit close.
func writeWebSocketPayload(ctx context.Context, connection *websocket.Conn, payload []byte) error {
	started := time.Now()
	err := connection.Write(ctx, websocket.MessageText, payload)
	observeWebSocketWrite(ctx, started, len(payload), err)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("websocket send failed", "bytes", len(payload), "close_code", websocket.CloseStatus(err), "error", err)
	}
	return err
}
