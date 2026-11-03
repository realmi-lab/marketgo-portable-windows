package registry

import "market-share-go/internal/domain"

type HistoryFilter struct {
	FromMS    int64
	ToMS      int64
	Exchange  domain.Exchange
	EventType string
	Asset     string
	Limit     int
}

type RefreshObservation struct {
	ObservedAtMS        int64           `json:"observed_at_ms"`
	Exchange            domain.Exchange `json:"exchange"`
	Status              string          `json:"status"`
	Authoritative       bool            `json:"authoritative"`
	RegistryVersion     int64           `json:"registry_version"`
	PreviousMarketCount int             `json:"previous_market_count"`
	ObservedMarketCount int             `json:"observed_market_count"`
	ActiveMarketCount   int             `json:"active_market_count"`
	ActiveAssetCount    int             `json:"active_asset_count"`
	QuoteCounts         map[string]int  `json:"quote_counts"`
	PublicFeeCount      int             `json:"public_fee_market_count"`
	DiscoveredCount     int             `json:"discovered_count"`
	AddedCount          int             `json:"added_count"`
	RemovedCount        int             `json:"removed_count"`
	Error               string          `json:"error"`
}

type LifecycleEvent struct {
	ObservedAtMS     int64           `json:"observed_at_ms"`
	Exchange         domain.Exchange `json:"exchange"`
	Market           string          `json:"market"`
	Base             string          `json:"base"`
	Quote            string          `json:"quote"`
	Scope            domain.Scope    `json:"scope"`
	EventType        string          `json:"event_type"`
	InitialDiscovery bool            `json:"initial_discovery"`
	MakerFee         *float64        `json:"maker_fee_rate"`
	TakerFee         *float64        `json:"taker_fee_rate"`
	FeeSource        string          `json:"fee_source"`
}

type History struct {
	Refreshes   []RefreshObservation `json:"refreshes"`
	Events      []LifecycleEvent     `json:"events"`
	GeneratedAt float64              `json:"generated_at"`
}
