package app

import (
	"context"
	"errors"
	"log/slog"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/config"
	"market-share-go/internal/domain"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/serviceconfig"
	"market-share-go/internal/store/postgres"
	"market-share-go/internal/tickertrend"
	"time"
)

// IntelligenceEvidenceProvider reads immutable, calculated evidence from the
// calculation owner; no Runtime, collector or market writer lives here.
type IntelligenceEvidenceProvider interface {
	QuestionOfficialWindow(context.Context, int64) (tickertrend.WindowComparison, error)
	QuestionCalendarDayClose(context.Context, int64, int64) (tickertrend.WindowComparison, error)
	QuestionDailyEndpoints(context.Context, []int64) (tickertrend.DailyEndpointSeries, error)
	AnalysisWithOptions(context.Context, int64, string, float64, marketanalysis.RequestOptions) (marketanalysis.Analysis, error)
	MarketQuestionIntervalEvidence(context.Context, marketanalysis.MarketQuestionQuery, int64) (marketanalysis.MarketQuestionIntervalEvidence, error)
	MarketQuestionHistoricalEvidence(context.Context, marketanalysis.MarketQuestionQuery) (HistoricalQuestionEvidence, error)
	DomesticNoticeCalendarInterval(context.Context, time.Time, time.Time) (CalendarDayResponse, error)
	RegistryMarkets(context.Context) ([]domain.MarketPair, error)
}
type HistoricalQuestionEvidence = historicalQuestionEvidence

func StartIntelligenceService(parent context.Context, settings config.Config, logger *slog.Logger, evidence IntelligenceEvidenceProvider) (*Service, error) {
	if evidence == nil {
		return nil, errors.New("calculation evidence service required")
	}
	if err := validateIntelligenceSettings(settings); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	repo, err := postgres.Open(ctx, settings.DatabaseURL, 8)
	if err != nil {
		cancel()
		return nil, err
	}
	s := &Service{cancel: cancel, logger: logger, startedAt: time.Now(), repository: repo, postgresRepository: repo, storageMode: settings.StorageMode, llmEnabled: settings.LLMEnabled, questionLearningEnabled: settings.QuestionLearningEnabled, noticePoll: settings.NoticePoll, noticeOCRBackend: "hermes_remote_vision", hermesNoticeIngestToken: settings.HermesNoticeIngestToken, noticePipelineV2Enabled: settings.NoticePipelineV2Enabled, intelligenceEvidence: evidence, referenceOrderKRW: settings.ReferenceOrderKRW}
	s.hermesQuestionConfig = hermesrunner.MarketQuestionConfig{HermesBin: settings.HermesNoticeBin, OutputDir: settings.HermesNoticeOutputDir, MaxTokens: hermesrunner.MaxInteractiveQuestionOutputTokens}
	s.marketQuestionTimeout = settings.DevelopmentQuestionTimeout
	s.marketQuestionCaptureInput = settings.DevelopmentQuestionCaptureInput
	s.marketQuestionACEnabled = settings.QuestionACEnabled
	s.marketQuestionEvidenceRetention = time.Duration(settings.QuestionEvidenceRetentionDays) * 24 * time.Hour
	s.running.Store(true)
	s.databaseReady.Store(true)
	s.startBackground(ctx, s.monitorDatabase)
	return s, nil
}

func validateIntelligenceSettings(settings config.Config) error {
	if settings.StorageMode != config.StoragePostgres {
		return errors.New("intelligence service requires explicit PostgreSQL storage")
	}
	if err := serviceconfig.ValidateDatabaseURL(settings.DatabaseURL); err != nil {
		return err
	}
	return serviceconfig.ValidateStateDir(settings.HermesNoticeOutputDir)
}
func (s *Service) MarketQuestionIntervalEvidence(ctx context.Context, q marketanalysis.MarketQuestionQuery, end int64) (marketanalysis.MarketQuestionIntervalEvidence, error) {
	return s.marketQuestionIntervalEvidence(ctx, q, end)
}
func (s *Service) MarketQuestionHistoricalEvidence(ctx context.Context, q marketanalysis.MarketQuestionQuery) (HistoricalQuestionEvidence, error) {
	return s.marketQuestionHistoricalEvidence(ctx, q)
}
func (s *Service) DomesticNoticeCalendarInterval(ctx context.Context, start, end time.Time) (CalendarDayResponse, error) {
	return s.domesticNoticeCalendarInterval(ctx, start, end)
}
func (s *Service) RegistryMarkets(ctx context.Context) ([]domain.MarketPair, error) {
	if s.registry == nil {
		return nil, ErrStopped
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.registry.Universe().Markets(), nil
}

func (s *Service) QuestionOfficialWindow(ctx context.Context, window int64) (tickertrend.WindowComparison, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.QuestionOfficialWindow(ctx, window)
	}
	if s.tickerTrend == nil {
		return tickertrend.WindowComparison{Status: "UNAVAILABLE", Reason: "TRACKER_NOT_CONFIGURED"}, nil
	}
	return s.tickerTrend.CompareWindowContext(ctx, window)
}
func (s *Service) QuestionCalendarDayClose(ctx context.Context, start, end int64) (tickertrend.WindowComparison, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.QuestionCalendarDayClose(ctx, start, end)
	}
	if s.tickerTrend == nil {
		return tickertrend.WindowComparison{Status: "UNAVAILABLE", Reason: "TRACKER_NOT_CONFIGURED"}, nil
	}
	return s.tickerTrend.CalendarDayClose(ctx, start, end)
}
func (s *Service) QuestionDailyEndpoints(ctx context.Context, endpoints []int64) (tickertrend.DailyEndpointSeries, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.QuestionDailyEndpoints(ctx, endpoints)
	}
	if s.tickerTrend == nil {
		return tickertrend.DailyEndpointSeries{}, ErrStopped
	}
	return s.tickerTrend.CompareDailyEndpoints(ctx, endpoints)
}
func (s *Service) IntelligenceHealth() HealthState {
	state := HealthState{Status: "unavailable", Running: s.running.Load(), DatabaseReady: s.databaseReady.Load(), Unhealthy: []string{}}
	if state.Running && state.DatabaseReady {
		state.Status = "healthy"
	}
	return state
}
