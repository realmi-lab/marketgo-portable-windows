package official

import (
	"market-share-go/internal/domain"
	"time"
)

type TickerSnapshot struct {
	Exchange            domain.Exchange    `json:"exchange"`
	Status              string             `json:"status"`
	Source              string             `json:"source"`
	VolumeKRW           *float64           `json:"volume_krw"`
	SharePercent        *float64           `json:"share_pct,omitempty"`
	MarketCount         int                `json:"market_count"`
	AsOf                float64            `json:"as_of"`
	ExchangeTimestampMS *int64             `json:"exchange_timestamp_ms"`
	LastError           *string            `json:"last_error"`
	Assets              map[string]float64 `json:"-"`
	// ClosePricesKRW contains exact per-market closes when the snapshot was
	// built from aligned KRW candles. A rolling ticker snapshot does not
	// populate this map. Missing candles remain absent rather than being
	// inferred from an adjacent hour.
	ClosePricesKRW map[string]float64 `json:"-"`
	// Markets and MarketTimestampsMS retain the canonical KRW market identity
	// evidence needed to compare an official all-market response with the
	// registry. They deliberately remain outside the public JSON contract.
	Markets            map[string]struct{} `json:"-"`
	MarketTimestampsMS map[string]int64    `json:"-"`
	ReceivedAt         time.Time           `json:"-"`
	WindowStartMS      int64               `json:"window_start_ms,omitempty"`
	WindowEndMS        int64               `json:"window_end_ms,omitempty"`
	BarIntervalSec     int64               `json:"bar_interval_sec,omitempty"`
	RequiredBarCount   int                 `json:"required_bar_count,omitempty"`
}

type DomesticTickerSummary struct {
	Status                string                             `json:"status"`
	Source                string                             `json:"source"`
	AsOf                  float64                            `json:"as_of"`
	StartedAt             float64                            `json:"started_at"`
	CollectionSpanMS      int64                              `json:"collection_span_ms"`
	ExpectedExchangeCount int                                `json:"expected_exchange_count"`
	ReadyExchangeCount    int                                `json:"ready_exchange_count"`
	TotalVolumeKRW        *float64                           `json:"total_volume_krw"`
	BithumbVolumeKRW      *float64                           `json:"bithumb_volume_krw"`
	BithumbSharePercent   *float64                           `json:"bithumb_share_pct"`
	Exchanges             map[domain.Exchange]TickerSnapshot `json:"exchanges"`
	WindowStartMS         int64                              `json:"window_start_ms,omitempty"`
	WindowEndMS           int64                              `json:"window_end_ms,omitempty"`
	BarIntervalSec        int64                              `json:"bar_interval_sec,omitempty"`
	RequiredBarCount      int                                `json:"required_bar_count,omitempty"`
	IncludedExchanges     []domain.Exchange                  `json:"included_exchanges,omitempty"`
	ExcludedExchanges     map[domain.Exchange]string         `json:"excluded_exchanges,omitempty"`
	// UpdateSequence and MissedUpdateCount are internal stream-continuity
	// evidence. They are intentionally excluded from the public ticker JSON:
	// UpdateSequence identifies the publication, while MissedUpdateCount tells
	// the single consumer that one or more older pending publications were
	// overwritten by latest-state coalescing before this value was delivered.
	UpdateSequence    uint64 `json:"-"`
	MissedUpdateCount uint64 `json:"-"`
}
