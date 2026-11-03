package movement

import (
	"encoding/json"
	"errors"
)

var ErrEventNotFound = errors.New("official movement not found")

type AssetKey struct {
	Exchange string `json:"exchange"`
	Asset    string `json:"asset"`
}

type ExchangeSnapshot struct {
	Exchange            string             `json:"exchange"`
	Status              string             `json:"status"`
	Source              string             `json:"source"`
	VolumeKRW           float64            `json:"volume_krw"`
	MarketCount         int                `json:"market_count"`
	ExchangeTimestampMS *int64             `json:"exchange_timestamp_ms"`
	Assets              map[string]float64 `json:"-"`
}

type Snapshot struct {
	AsOfMS           int64                       `json:"as_of_ms"`
	Status           string                      `json:"status"`
	Source           string                      `json:"source"`
	CollectionSpanMS int64                       `json:"collection_span_ms"`
	TotalVolumeKRW   float64                     `json:"total_volume_krw"`
	BithumbVolumeKRW float64                     `json:"bithumb_volume_krw"`
	BithumbSharePct  float64                     `json:"bithumb_share_pct"`
	Exchanges        map[string]ExchangeSnapshot `json:"exchanges"`
	Assets           map[AssetKey]float64        `json:"-"`
}

type RealtimeOrderbook struct {
	Status          string   `json:"status"`
	AgeSec          *float64 `json:"age_sec"`
	Imbalance       *float64 `json:"imbalance"`
	SpreadBPS       *float64 `json:"spread_bps"`
	BidDepthKRW     *float64 `json:"bid_depth_krw"`
	AskDepthKRW     *float64 `json:"ask_depth_krw"`
	BuySlippageBPS  *float64 `json:"buy_slippage_bps"`
	SellSlippageBPS *float64 `json:"sell_slippage_bps"`
}

type RealtimeAssetObservation struct {
	Asset               string                        `json:"asset"`
	BithumbVolumeKRW    float64                       `json:"bithumb_volume_krw"`
	CompetitorVolumeKRW float64                       `json:"competitor_volume_krw"`
	BithumbBuyKRW       float64                       `json:"bithumb_buy_krw"`
	BithumbSellKRW      float64                       `json:"bithumb_sell_krw"`
	ExchangeVolumesKRW  map[string]float64            `json:"exchange_volumes_krw"`
	ExchangeTradeCounts map[string]int64              `json:"exchange_trade_counts"`
	ExchangeBuyKRW      map[string]float64            `json:"exchange_buy_krw"`
	ExchangeSellKRW     map[string]float64            `json:"exchange_sell_krw"`
	ExchangeOrderbooks  map[string]*RealtimeOrderbook `json:"exchange_orderbooks"`
	Orderbook           *RealtimeOrderbook            `json:"orderbook,omitempty"`
}

type RealtimeObservation struct {
	AsOfMS   int64                      `json:"as_of_ms"`
	Status   string                     `json:"status"`
	Complete bool                       `json:"complete"`
	Assets   []RealtimeAssetObservation `json:"assets"`
}

type Driver struct {
	Rank                        int     `json:"rank"`
	Asset                       string  `json:"asset"`
	ContributionPP              float64 `json:"contribution_pp"`
	BithumbVolumeChangeKRW      float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW   float64 `json:"competitor_volume_change_krw"`
	PreviousBithumbVolumeKRW    float64 `json:"previous_bithumb_volume_krw"`
	CurrentBithumbVolumeKRW     float64 `json:"current_bithumb_volume_krw"`
	PreviousCompetitorVolumeKRW float64 `json:"previous_competitor_volume_krw"`
	CurrentCompetitorVolumeKRW  float64 `json:"current_competitor_volume_krw"`
}

type ExchangeDriver struct {
	Rank              int     `json:"rank"`
	Exchange          string  `json:"exchange"`
	ContributionPP    float64 `json:"contribution_pp"`
	VolumeChangeKRW   float64 `json:"volume_change_krw"`
	PreviousVolumeKRW float64 `json:"previous_volume_krw"`
	CurrentVolumeKRW  float64 `json:"current_volume_krw"`
	LeadingAsset      string  `json:"leading_asset"`
	LeadingAssetPP    float64 `json:"leading_asset_contribution_pp"`
}

type NoticeEvidence struct {
	Exchange      string   `json:"exchange"`
	NoticeID      string   `json:"notice_id"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Category      string   `json:"category"`
	PublishedTSMS int64    `json:"published_ts_ms"`
	DetectedTSMS  int64    `json:"detected_ts_ms"`
	AnchorTSMS    int64    `json:"anchor_ts_ms"`
	Relation      string   `json:"relation"`
	MatchedAssets []string `json:"matched_assets"`
}

// FlowEvidence is a bounded, immutable copy of the nearest stored WebSocket
// flow observation at an important official movement checkpoint. Payload is
// the versioned REALTIME_FLOW contract, not generated prose.
type FlowEvidence struct {
	SnapshotKind   string          `json:"snapshot_kind"`
	OfficialAsOfMS int64           `json:"official_as_of_ms"`
	FlowAsOfMS     int64           `json:"flow_as_of_ms"`
	LagMS          int64           `json:"lag_ms"`
	Payload        json.RawMessage `json:"payload"`
}

type Event struct {
	EventID          string           `json:"event_id"`
	Source           string           `json:"source"`
	Direction        string           `json:"direction"`
	State            string           `json:"state"`
	StartedAtMS      int64            `json:"started_at_ms"`
	ConfirmedAtMS    *int64           `json:"confirmed_at_ms"`
	UpdatedAtMS      int64            `json:"updated_at_ms"`
	EndedAtMS        *int64           `json:"ended_at_ms"`
	BaselineSharePct float64          `json:"baseline_share_pct"`
	CurrentSharePct  float64          `json:"current_share_pct"`
	ExtremeSharePct  float64          `json:"extreme_share_pct"`
	ChangePP         float64          `json:"change_pp"`
	ExtremeChangePP  float64          `json:"extreme_change_pp"`
	ThresholdPP      float64          `json:"threshold_pp"`
	QualityStatus    string           `json:"quality_status"`
	Drivers          []Driver         `json:"drivers"`
	ExchangeDrivers  []ExchangeDriver `json:"exchange_drivers"`
	FlowEvidence     []FlowEvidence   `json:"flow_evidence"`
	Notices          []NoticeEvidence `json:"notices"`
}

type SamplePoint struct {
	AsOfMS          int64   `json:"as_of_ms"`
	BithumbSharePct float64 `json:"bithumb_share_pct"`
	Status          string  `json:"status"`
}

type State struct {
	Version       string         `json:"version"`
	GeneratedAtMS int64          `json:"generated_at_ms"`
	Current       *Snapshot      `json:"current"`
	Movement      *Event         `json:"movement"`
	RecentEvents  []Event        `json:"recent_events"`
	Trend         []SamplePoint  `json:"trend"`
	DataQuality   map[string]any `json:"data_quality"`
}
