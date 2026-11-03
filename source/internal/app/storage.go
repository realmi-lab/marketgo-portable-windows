package app

import (
	"context"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/reconcile"
	"market-share-go/services/collector/registry"
	"market-share-go/internal/tickertrend"
)

// serviceRepository is the storage surface shared by the production
// PostgreSQL adapter and the desktop-only ephemeral SQLite adapter. High-rate
// raw-frame and reconciliation persistence remain PostgreSQL-only and are
// composed separately in Service.start.
type serviceRepository interface {
	AggregationStore
	startupHydrationStore
	MarketRegistryStore
	movement.Store
	tickertrend.Store
	recentTradeIDPruner

	Ping(context.Context) error
	Close()
	ReadMetadata(context.Context, string) (string, bool, error)
	WriteMetadata(context.Context, string, string) error

	SaveRealtimeFlowStateWithSummary(context.Context, marketanalysis.RealtimeFlowState) (marketanalysis.RealtimeFlowState, error)
	RealtimeFlowHistory(context.Context, marketanalysis.RealtimeFlowHistoryFilter) ([]marketanalysis.RealtimeFlowState, error)
	RealtimeFlowSummaryHistory(context.Context, int64, int64) ([]marketanalysis.RealtimeFlowState, error)

	NoticeSnapshot(context.Context, time.Time) (notice.Snapshot, error)
	SaveHermesNoticeBatch(context.Context, notice.HermesIngestRequest) (notice.HermesIngestResult, error)
	RelinkRecentMovementNotices(context.Context) (map[string][]movement.NoticeEvidence, error)
	LoadNoticeSources(context.Context) (map[string]notice.Source, error)
	LoadNoticeItem(context.Context, string, string) (notice.Item, bool, error)
	SaveNoticePoll(context.Context, string, []notice.Item, notice.Source) error
	SaveNoticePollError(context.Context, string, string, string, time.Time) error

	LoadActiveMarketRegistry(context.Context) ([]domain.MarketPair, error)
	MarketRegistryHistory(context.Context, registry.HistoryFilter) (registry.History, error)
	MovementEvent(context.Context, string) (*movement.Event, error)
	RecoveredMinuteSupplements(context.Context, domain.UnixSecond, domain.UnixSecond) ([]reconcile.MinuteSupplement, error)
	RecoveredTenMinuteSupplements(context.Context, domain.UnixSecond, domain.UnixSecond) ([]reconcile.MinuteSupplement, error)
}
