package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"market-share-go/internal/analysis"
	"market-share-go/internal/app"
	"market-share-go/internal/config"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/services/collector/registry"
	"market-share-go/internal/tickertrend"
	"market-share-go/internal/transport/httpapi"
)

const (
	// Grounded questions run a bounded retrieval planner and then a bounded
	// answer generator. Their individual 120-second limits are sequential, so
	// the HTTP envelope must outlive both calls plus evidence validation.
	backendWriteTimeout = 300 * time.Second
	backendIdleTimeout  = 330 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("market-share server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	settings.VisionOCRBin = hermesrunner.ResolveVisionOCRBin(settings.VisionOCRBin)
	debug.SetMemoryLimit(int64(settings.MemoryLimitMiB) << 20)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: settings.LogLevel}))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service, err := app.StartService(ctx, settings, logger)
	if err != nil {
		return err
	}
	// Hydration intentionally materializes and then compacts a large PostgreSQL
	// history before the HTTP server is exposed. Return the temporary scan and
	// conversion pages to the OS once here; a periodic forced GC would trade the
	// saved resident memory for avoidable steady-state CPU and latency.
	debug.FreeOSMemory()

	handler := httpapi.NewHandler(logger, serviceStatus{service: service}, serviceAPI{service: service})
	if settings.DeploymentMode != "standalone" {
		handler = httpapi.WithHermesProxyAuthorization(handler, settings.DeploymentControlToken)
	}
	handler = withDeploymentControl(handler, service, settings.DeploymentControlToken, logger)
	if frontendRoot := strings.TrimSpace(os.Getenv("MARKET_SHARE_FRONTEND_ROOT")); frontendRoot != "" {
		handler, err = integratedFrontendHandler(frontendRoot, handler)
		if err != nil {
			_ = service.Stop(context.Background())
			return err
		}
	}
	server := newBackendServer(settings.ListenAddress(), handler)
	errCh := make(chan error, 1)
	go func() {
		logger.Info("market-share server listening", "address", settings.ListenAddress())
		errCh <- server.ListenAndServe()
	}()
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MARKET_SHARE_OPEN_BROWSER")), "true") {
		go openBrowserWhenReady(ctx, settings.ListenAddress(), "http://"+settings.ListenAddress(), logger)
	}
	if settings.HermesNoticeAutostart {
		go hermesrunner.SuperviseWorker(ctx, hermesrunner.WorkerConfig{
			Bin:        settings.HermesNoticeWorkerBin,
			BackendURL: "http://" + settings.ListenAddress(), Token: settings.HermesNoticeIngestToken,
			HermesBin: settings.HermesNoticeBin, VisionOCRBin: settings.VisionOCRBin, OutputDir: settings.HermesNoticeOutputDir,
			Interval:                     settings.NoticePoll,
			MaxAnalysesPerExchangePerRun: settings.HermesNoticeMaxAnalysesPerExchangePerRun,
		}, logger)
	}
	go releaseStartupMemory(ctx, logger)

	select {
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), settings.ShutdownTimeout)
		defer cancel()
		_ = service.Stop(shutdownCtx)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), settings.ShutdownTimeout)
		defer cancel()
		logger.Info("market-share server shutting down")
		serverErr := server.Shutdown(shutdownCtx)
		serviceErr := service.Stop(shutdownCtx)
		return errors.Join(serverErr, serviceErr)
	}
}

func newBackendServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address,
		// Standard analysis streams are demand-driven. Matching browser
		// subscribers still share one producer and the latest immutable frame,
		// but an unused window/scope no longer consumes CPU in the background.
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      backendWriteTimeout,
		IdleTimeout:       backendIdleTimeout,
	}
}

func releaseStartupMemory(ctx context.Context, logger *slog.Logger) {
	// Registry discovery, collector subscription setup, and the initial notice
	// exchange create another short allocation wave after StartService returns.
	// Reclaim it once after startup settles, without imposing periodic forced-GC
	// work on the steady-state market-data loop.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		debug.FreeOSMemory()
		logger.Info("released transient startup memory")
	}
}

type serviceStatus struct {
	service *app.Service
}

type serviceAPI struct{ service *app.Service }

var _ httpapi.RegistryHistoryProvider = serviceAPI{}
var _ httpapi.RealtimeFlowStateProvider = serviceAPI{}
var _ httpapi.WhaleAlertStateProvider = serviceAPI{}
var _ httpapi.RealtimeFlowSummaryProvider = serviceAPI{}
var _ httpapi.OfficialAlignedHistoryProvider = serviceAPI{}
var _ httpapi.OfficialAlignedContributionProvider = serviceAPI{}
var _ httpapi.AnalysisOptionsProvider = serviceAPI{}
var _ httpapi.HermesMarketQuestionProvider = serviceAPI{}
var _ httpapi.GroundedMarketQuestionProvider = serviceAPI{}
var _ httpapi.GroundedMarketQuestionHistoryProvider = serviceAPI{}
var _ httpapi.MarketQuestionFeedbackProvider = serviceAPI{}
var _ httpapi.HermesNoticeQuestionProvider = serviceAPI{}
var _ httpapi.ActiveNoticeNetworkProvider = serviceAPI{}

func (s serviceAPI) Analysis(ctx context.Context, window int64, scope string, notional float64) (analysis.Analysis, error) {
	return s.service.Analysis(ctx, window, scope, notional)
}
func (s serviceAPI) AnalysisWithOptions(ctx context.Context, window int64, scope string, notional float64, options analysis.RequestOptions) (analysis.Analysis, error) {
	return s.service.AnalysisWithOptions(ctx, window, scope, notional, options)
}
func (s serviceAPI) AskHermesMarketQuestion(ctx context.Context, question string) (analysis.MarketQuestionAnswer, error) {
	return s.service.AskHermesMarketQuestion(ctx, question)
}
func (s serviceAPI) AskGroundedMarketQuestion(ctx context.Context, question string) (analysis.GroundedMarketQuestionResponse, error) {
	return s.service.AskGroundedMarketQuestion(ctx, question)
}
func (s serviceAPI) AskGroundedMarketQuestionWithHistory(ctx context.Context, question string, history []analysis.MarketQuestionConversationTurn) (analysis.GroundedMarketQuestionResponse, error) {
	return s.service.AskGroundedMarketQuestionWithHistory(ctx, question, history)
}
func (s serviceAPI) SubmitMarketQuestionFeedback(ctx context.Context, feedback analysis.MarketQuestionFeedback) error {
	return s.service.SubmitMarketQuestionFeedback(ctx, feedback)
}
func (s serviceAPI) AskHermesNoticeQuestion(ctx context.Context, persona, question string, contextSourceIDs []string) (analysis.NoticeQuestionAnswer, error) {
	return s.service.AskHermesNoticeQuestion(ctx, persona, question, contextSourceIDs)
}
func (s serviceAPI) ActiveNoticeNetwork(ctx context.Context, windowSec int64) (any, error) {
	return s.service.ActiveNoticeNetwork(ctx, windowSec)
}
func (s serviceAPI) MarketRows(ctx context.Context, window int64, scope, exchange string) (any, error) {
	return s.service.MarketRows(ctx, window, scope, exchange)
}
func (s serviceAPI) LiquidityRows(ctx context.Context, notional float64, scope, exchange, asset string) (any, error) {
	return s.service.LiquidityRows(ctx, notional, scope, exchange, asset)
}
func (s serviceAPI) OrderBookDetail(ctx context.Context, exchange, market string, notional float64) (any, error) {
	return s.service.OrderBookDetail(ctx, exchange, market, notional)
}
func (s serviceAPI) RealtimeTick(ctx context.Context, window int64, scope string) (any, error) {
	return s.service.RealtimeTick(ctx, window, scope)
}
func (s serviceAPI) RealtimeFlowState() analysis.RealtimeFlowState {
	return s.service.RealtimeFlowState()
}
func (s serviceAPI) WhaleAlertState() analysis.WhaleAlertState {
	return s.service.WhaleAlertState()
}
func (s serviceAPI) Snapshot(ctx context.Context, includeMarkets bool) (any, error) {
	return s.service.Snapshot(ctx, includeMarkets)
}
func (s serviceAPI) ReconciliationLatest(ctx context.Context) (any, error) {
	return s.service.ReconciliationLatest(ctx)
}
func (s serviceAPI) CalendarDay(ctx context.Context, day, scope string) (any, error) {
	return s.service.CalendarDay(ctx, day, scope)
}
func (s serviceAPI) NoticeSnapshot(ctx context.Context) (any, error) {
	return s.service.NoticeSnapshot(ctx)
}
func (s serviceAPI) IngestHermesNotices(ctx context.Context, token string, request notice.HermesIngestRequest) (notice.HermesIngestResult, error) {
	return s.service.IngestHermesNotices(ctx, token, request)
}
func (s serviceAPI) HermesNoticeSnapshot(ctx context.Context, token string) (notice.Snapshot, error) {
	return s.service.HermesNoticeSnapshot(ctx, token)
}
func (s serviceAPI) RuntimeState(ctx context.Context) map[string]any {
	return s.service.RuntimeState(ctx)
}
func (s serviceAPI) OfficialMovementEvents(ctx context.Context, limit int) ([]movement.Event, error) {
	return s.service.OfficialMovementEvents(ctx, limit)
}
func (s serviceAPI) OfficialMovementEvent(ctx context.Context, eventID string) (*movement.Event, error) {
	return s.service.OfficialMovementEvent(ctx, eventID)
}
func (s serviceAPI) OfficialAlignedHistory(ctx context.Context, fromMS, toMS int64) (tickertrend.History, error) {
	return s.service.OfficialAlignedHistory(ctx, fromMS, toMS)
}
func (s serviceAPI) OfficialAlignedContribution(ctx context.Context, toMS int64) (tickertrend.WindowComparison, error) {
	return s.service.OfficialAlignedContribution(ctx, toMS)
}
func (s serviceAPI) RealtimeFlowHistory(ctx context.Context, filter analysis.RealtimeFlowHistoryFilter) ([]analysis.RealtimeFlowState, error) {
	return s.service.RealtimeFlowHistory(ctx, filter)
}
func (s serviceAPI) RealtimeFlowPeriodSummary(ctx context.Context, toMS, windowSec int64) (analysis.RealtimeFlowPeriodSummary, error) {
	return s.service.RealtimeFlowPeriodSummary(ctx, toMS, windowSec)
}
func (s serviceAPI) RegistryHistory(ctx context.Context, filter registry.HistoryFilter) (registry.History, error) {
	return s.service.RegistryHistory(ctx, filter)
}

func (s serviceStatus) Health() httpapi.Health {
	health := s.service.HealthState()
	return httpapi.Health{
		Status:        health.Status,
		Running:       health.Running,
		RealtimeReady: health.RealtimeReady,
		DatabaseReady: health.DatabaseReady,
		Unhealthy:     health.Unhealthy,
	}
}
