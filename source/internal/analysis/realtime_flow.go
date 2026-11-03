package analysis

import (
	"errors"
	"maps"
	"math"
	"sort"
	"strings"
	"sync"

	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
)

const (
	RealtimeFlowVersion = "REALTIME_FLOW_V2"

	realtimeFlowMaxFrames        = 180
	realtimeFlowMaxDrivers       = 15
	realtimeFlowMaxCurrentAssets = 12
	realtimeFlowHistorySec       = 120
)

var realtimeFlowWindows = [...]int64{10, 30, 60}

// RealtimeFlowWindow compares two adjacent, equally sized WebSocket windows.
// A value is actionable only when Status is COMPLETE.
type RealtimeFlowWindow struct {
	DurationSec       int64    `json:"duration_sec"`
	Status            string   `json:"status"`
	CurrentSharePct   *float64 `json:"current_share_pct"`
	PreviousSharePct  *float64 `json:"previous_share_pct"`
	ShareDeltaPP      *float64 `json:"share_delta_pp"`
	CurrentVolumeKRW  float64  `json:"current_volume_krw"`
	PreviousVolumeKRW float64  `json:"previous_volume_krw"`
	ThresholdPP       float64  `json:"threshold_pp"`
}

type RealtimeFlowExchangeImpact struct {
	Exchange          string                      `json:"exchange"`
	ContributionPP    float64                     `json:"contribution_pp"`
	VolumeChangeKRW   float64                     `json:"volume_change_krw"`
	PreviousVolumeKRW float64                     `json:"previous_volume_krw"`
	CurrentVolumeKRW  float64                     `json:"current_volume_krw"`
	BuyPct            *float64                    `json:"buy_pct"`
	Orderbook         *movement.RealtimeOrderbook `json:"orderbook,omitempty"`
}

type RealtimeFlowDriver struct {
	Rank                        int                          `json:"rank"`
	Asset                       string                       `json:"asset"`
	ContributionPP              float64                      `json:"contribution_pp"`
	BithumbVolumeChangeKRW      float64                      `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW   float64                      `json:"competitor_volume_change_krw"`
	PreviousBithumbVolumeKRW    float64                      `json:"previous_bithumb_volume_krw"`
	CurrentBithumbVolumeKRW     float64                      `json:"current_bithumb_volume_krw"`
	PreviousCompetitorVolumeKRW float64                      `json:"previous_competitor_volume_krw"`
	CurrentCompetitorVolumeKRW  float64                      `json:"current_competitor_volume_krw"`
	BithumbBuyPct               *float64                     `json:"bithumb_buy_pct"`
	TopCompetitor               string                       `json:"top_competitor"`
	TopCompetitorVolumeKRW      float64                      `json:"top_competitor_volume_krw"`
	ExchangeImpacts             []RealtimeFlowExchangeImpact `json:"exchange_impacts"`
	Orderbook                   *movement.RealtimeOrderbook  `json:"orderbook,omitempty"`
}

type RealtimeFlowExchangeDriver struct {
	Rank                       int     `json:"rank"`
	Exchange                   string  `json:"exchange"`
	ContributionPP             float64 `json:"contribution_pp"`
	VolumeChangeKRW            float64 `json:"volume_change_krw"`
	PreviousVolumeKRW          float64 `json:"previous_volume_krw"`
	CurrentVolumeKRW           float64 `json:"current_volume_krw"`
	LeadingAsset               string  `json:"leading_asset"`
	LeadingAssetContributionPP float64 `json:"leading_asset_contribution_pp"`
}

type RealtimeFlowCurrentAsset struct {
	Asset                  string                      `json:"asset"`
	DomesticVolumeKRW      float64                     `json:"domestic_volume_krw"`
	BithumbVolumeKRW       float64                     `json:"bithumb_volume_krw"`
	BithumbSharePct        *float64                    `json:"bithumb_share_pct"`
	BithumbBuyPct          *float64                    `json:"bithumb_buy_pct"`
	TopCompetitor          string                      `json:"top_competitor"`
	TopCompetitorVolumeKRW float64                     `json:"top_competitor_volume_krw"`
	Orderbook              *movement.RealtimeOrderbook `json:"orderbook,omitempty"`
}

// RealtimeFlowSharePoint1S is a server-derived one-second point retained only
// in the live state. It is intentionally excluded from PostgreSQL archives so
// the 60-second chart does not multiply long-term storage volume.
type RealtimeFlowSharePoint1S struct {
	AsOfMS                    int64               `json:"as_of_ms"`
	Status                    string              `json:"status"`
	Complete                  bool                `json:"complete"`
	DomesticVolume1SKRW       float64             `json:"domestic_volume_1s_krw"`
	BithumbVolume1SKRW        float64             `json:"bithumb_volume_1s_krw"`
	BithumbShare1SPct         *float64            `json:"bithumb_share_1s_pct"`
	ObservedBithumbShare1SPct *float64            `json:"observed_bithumb_share_1s_pct"`
	ExchangeVolumes1SKRW      map[string]float64  `json:"exchange_volumes_1s_krw"`
	ExchangeTradeCounts1S     map[string]int64    `json:"exchange_trade_counts_1s"`
	ExchangeShares1SPct       map[string]*float64 `json:"exchange_shares_1s_pct"`
}

// RealtimeFlowAttribution keeps driver evidence for one exact comparison
// window. The primary-window fields remain for alert compatibility, while the
// 10-second monitor always has its own correctly scoped attribution.
type RealtimeFlowAttribution struct {
	WindowSec              int64                        `json:"window_sec"`
	StoredDriverCount      int                          `json:"stored_driver_count"`
	TotalDriverCount       int                          `json:"total_driver_count"`
	ResidualContributionPP *float64                     `json:"residual_contribution_pp"`
	Drivers                []RealtimeFlowDriver         `json:"drivers"`
	ExchangeDrivers        []RealtimeFlowExchangeDriver `json:"exchange_drivers"`
}

// RealtimeFlowState is a diagnostic WebSocket signal. It is deliberately
// separate from the official rolling-24h ticker share and must never be used
// to force the two sources to match.
type RealtimeFlowState struct {
	Version                string                       `json:"version"`
	AsOfMS                 int64                        `json:"as_of_ms"`
	Status                 string                       `json:"status"`
	Direction              string                       `json:"direction"`
	Reason                 string                       `json:"reason"`
	PrimaryWindowSec       int64                        `json:"primary_window_sec"`
	ShareDeltaPP           *float64                     `json:"share_delta_pp"`
	StoredDriverCount      int                          `json:"stored_driver_count"`
	TotalDriverCount       int                          `json:"total_driver_count"`
	ResidualContributionPP *float64                     `json:"residual_contribution_pp"`
	DomesticVolume1SKRW    float64                      `json:"domestic_volume_1s_krw"`
	BithumbVolume1SKRW     float64                      `json:"bithumb_volume_1s_krw"`
	BithumbShare1SPct      *float64                     `json:"bithumb_share_1s_pct"`
	RecentShare1S          []RealtimeFlowSharePoint1S   `json:"recent_share_1s"`
	Windows                []RealtimeFlowWindow         `json:"windows"`
	Drivers                []RealtimeFlowDriver         `json:"drivers"`
	ExchangeDrivers        []RealtimeFlowExchangeDriver `json:"exchange_drivers"`
	TenSecondAttribution   *RealtimeFlowAttribution     `json:"ten_second_attribution,omitempty"`
	CurrentAssets          []RealtimeFlowCurrentAsset   `json:"current_assets"`
	DataQuality            map[string]any               `json:"data_quality"`
}

type RealtimeFlowHistoryFilter struct {
	FromMS   int64
	ToMS     int64
	Status   string
	Asset    string
	Exchange string
	Limit    int
}

type RealtimeFlowConfig struct {
	MinVolumePerSecondKRW float64
}

func (c RealtimeFlowConfig) withDefaults() RealtimeFlowConfig {
	if c.MinVolumePerSecondKRW <= 0 {
		c.MinVolumePerSecondKRW = 10_000_000
	}
	return c
}

type realtimeFlowAssetFrame struct {
	bithumb     float64
	competitor  float64
	exchanges   map[string]float64
	tradeCounts map[string]int64
	buys        map[string]float64
	sells       map[string]float64
	orderbooks  map[string]*movement.RealtimeOrderbook
	orderbook   *movement.RealtimeOrderbook
}

type realtimeFlowFrame struct {
	second   int64
	complete bool
	status   string
	assets   map[string]realtimeFlowAssetFrame
	totals   realtimeFlowFrameTotals
}

// realtimeFlowFrameTotals are a frame's market-wide sums, made once when the
// frame arrives: the one-second chart reads them for each of its 60 seconds
// every second.
type realtimeFlowFrameTotals struct {
	bithumb, domestic float64
	volumes           map[string]float64
	tradeCounts       map[string]int64
}

func newRealtimeFlowFrame(second int64, complete bool, status string, assets map[string]realtimeFlowAssetFrame) realtimeFlowFrame {
	values := realtimeFlowAggregate{assets: assets}
	bithumb, domestic := realtimeFlowTotals(values)
	return realtimeFlowFrame{
		second: second, complete: complete, status: status, assets: assets,
		totals: realtimeFlowFrameTotals{
			bithumb: bithumb, domestic: domestic,
			volumes:     realtimeFlowExchangeVolumes(values),
			tradeCounts: realtimeFlowExchangeTradeCounts(values),
		},
	}
}

type realtimeFlowAggregate struct {
	assets   map[string]realtimeFlowAssetFrame
	complete bool
	seconds  int64
}

type RealtimeFlowAnalyzer struct {
	mu     sync.RWMutex
	config RealtimeFlowConfig
	frames []realtimeFlowFrame
	state  RealtimeFlowState
}

func NewRealtimeFlowAnalyzer(config RealtimeFlowConfig) *RealtimeFlowAnalyzer {
	return &RealtimeFlowAnalyzer{
		config: config.withDefaults(),
		state: RealtimeFlowState{
			Version: RealtimeFlowVersion, Status: "WARMING", Direction: "UNKNOWN",
			Reason:        "비교에 필요한 실시간 체결을 모으고 있습니다.",
			RecentShare1S: []RealtimeFlowSharePoint1S{},
			Windows:       []RealtimeFlowWindow{}, Drivers: []RealtimeFlowDriver{},
			ExchangeDrivers: []RealtimeFlowExchangeDriver{}, CurrentAssets: []RealtimeFlowCurrentAsset{},
			DataQuality: map[string]any{"required_history_sec": realtimeFlowHistorySec},
		},
	}
}

func (a *RealtimeFlowAnalyzer) Observe(observation movement.RealtimeObservation) (RealtimeFlowState, error) {
	next, err := normalizeRealtimeFlowObservation(observation)
	if err != nil {
		return RealtimeFlowState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if size := len(a.frames); size > 0 {
		last := a.frames[size-1].second
		if next.second < last {
			return RealtimeFlowState{}, errors.New("realtime observation is older than the latest frame")
		}
		if next.second == last {
			a.frames[size-1] = next
		} else {
			a.frames = append(a.frames, next)
		}
	} else {
		a.frames = append(a.frames, next)
	}
	if len(a.frames) > realtimeFlowMaxFrames {
		a.frames = append([]realtimeFlowFrame(nil), a.frames[len(a.frames)-realtimeFlowMaxFrames:]...)
	}
	a.state = a.buildState(next)
	return cloneRealtimeFlowState(a.state), nil
}

func (a *RealtimeFlowAnalyzer) State() RealtimeFlowState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneRealtimeFlowState(a.state)
}

func normalizeRealtimeFlowObservation(observation movement.RealtimeObservation) (realtimeFlowFrame, error) {
	if observation.AsOfMS <= 0 {
		return realtimeFlowFrame{}, errors.New("realtime observation timestamp must be positive")
	}
	assets := make(map[string]realtimeFlowAssetFrame, len(observation.Assets))
	for _, row := range observation.Assets {
		asset := strings.ToUpper(strings.TrimSpace(row.Asset))
		if asset == "" {
			return realtimeFlowFrame{}, errors.New("realtime observation has an empty asset")
		}
		if _, exists := assets[asset]; exists {
			return realtimeFlowFrame{}, errors.New("realtime observation repeats an asset")
		}
		for _, value := range []float64{row.BithumbVolumeKRW, row.CompetitorVolumeKRW, row.BithumbBuyKRW, row.BithumbSellKRW} {
			if !validAnalysisNumber(value) {
				return realtimeFlowFrame{}, errors.New("realtime observation has an invalid volume")
			}
		}
		exchanges, err := normalizeRealtimeFlowValues(row.ExchangeVolumesKRW)
		if err != nil {
			return realtimeFlowFrame{}, err
		}
		if len(exchanges) == 0 {
			exchanges[string(domain.ExchangeBithumb)] = row.BithumbVolumeKRW
			exchanges["competitors"] = row.CompetitorVolumeKRW
		}
		tradeCounts, err := normalizeRealtimeFlowCounts(row.ExchangeTradeCounts)
		if err != nil {
			return realtimeFlowFrame{}, err
		}
		observedBithumb, observedCompetitors := 0.0, 0.0
		for exchange, volume := range exchanges {
			if exchange == string(domain.ExchangeBithumb) {
				observedBithumb += volume
			} else {
				observedCompetitors += volume
			}
		}
		if !realtimeFlowNearlyEqual(observedBithumb, row.BithumbVolumeKRW) || !realtimeFlowNearlyEqual(observedCompetitors, row.CompetitorVolumeKRW) {
			return realtimeFlowFrame{}, errors.New("realtime observation exchange volumes do not match totals")
		}
		buys, err := normalizeRealtimeFlowValues(row.ExchangeBuyKRW)
		if err != nil {
			return realtimeFlowFrame{}, err
		}
		sells, err := normalizeRealtimeFlowValues(row.ExchangeSellKRW)
		if err != nil {
			return realtimeFlowFrame{}, err
		}
		if _, found := buys[string(domain.ExchangeBithumb)]; !found && row.BithumbBuyKRW > 0 {
			buys[string(domain.ExchangeBithumb)] = row.BithumbBuyKRW
		}
		if _, found := sells[string(domain.ExchangeBithumb)]; !found && row.BithumbSellKRW > 0 {
			sells[string(domain.ExchangeBithumb)] = row.BithumbSellKRW
		}
		books := make(map[string]*movement.RealtimeOrderbook, len(row.ExchangeOrderbooks))
		for exchange, book := range row.ExchangeOrderbooks {
			name := strings.ToLower(strings.TrimSpace(exchange))
			if name != "" {
				books[name] = cloneRealtimeOrderbook(book)
			}
		}
		if _, found := books[string(domain.ExchangeBithumb)]; !found && row.Orderbook != nil {
			books[string(domain.ExchangeBithumb)] = cloneRealtimeOrderbook(row.Orderbook)
		}
		assets[asset] = realtimeFlowAssetFrame{
			bithumb: row.BithumbVolumeKRW, competitor: row.CompetitorVolumeKRW,
			exchanges: exchanges, tradeCounts: tradeCounts, buys: buys, sells: sells, orderbooks: books,
			orderbook: cloneRealtimeOrderbook(row.Orderbook),
		}
	}
	return newRealtimeFlowFrame(
		observation.AsOfMS/1_000, observation.Complete,
		strings.ToUpper(strings.TrimSpace(observation.Status)), assets,
	), nil
}

func normalizeRealtimeFlowValues(values map[string]float64) (map[string]float64, error) {
	result := make(map[string]float64, len(values))
	for key, value := range values {
		if !validAnalysisNumber(value) {
			return nil, errors.New("realtime observation has an invalid exchange volume")
		}
		name := strings.ToLower(strings.TrimSpace(key))
		if name != "" {
			result[name] = value
		}
	}
	return result, nil
}

func normalizeRealtimeFlowCounts(values map[string]int64) (map[string]int64, error) {
	result := make(map[string]int64, len(values))
	for key, value := range values {
		if value < 0 {
			return nil, errors.New("realtime observation has an invalid exchange trade count")
		}
		name := strings.ToLower(strings.TrimSpace(key))
		if name != "" {
			result[name] = value
		}
	}
	return result, nil
}

func realtimeFlowNearlyEqual(left, right float64) bool {
	tolerance := math.Max(0.01, math.Max(math.Abs(left), math.Abs(right))*1e-9)
	return math.Abs(left-right) <= tolerance
}

// PublishedRealtimeFlowState is the transport form of a realtime flow state.
// The analyzer, the persisted history and the period summaries keep full
// precision: Observe's state is saved and summed thousands of times over a
// day, where rounding each sample would accumulate error. Only the copy sent
// to viewers is rounded, and the source state is never modified.
func PublishedRealtimeFlowState(state RealtimeFlowState) RealtimeFlowState {
	published := roundPublishedState(cloneRealtimeFlowState(state))
	// The clone allocates empty collections. Archived states omit the
	// one-second series and publish it as null, so keep an absent collection
	// absent and the published JSON shape identical to the source.
	if state.RecentShare1S == nil {
		published.RecentShare1S = nil
	}
	if state.Windows == nil {
		published.Windows = nil
	}
	if state.Drivers == nil {
		published.Drivers = nil
	}
	if state.ExchangeDrivers == nil {
		published.ExchangeDrivers = nil
	}
	if state.CurrentAssets == nil {
		published.CurrentAssets = nil
	}
	if state.DataQuality == nil {
		published.DataQuality = nil
	}
	return published
}

// roundPublishedState rounds in place; callers pass a state they own.
func roundPublishedState(state RealtimeFlowState) RealtimeFlowState {
	krw := func(value float64) float64 {
		return roundPublishedKRW(value)
	}
	pct := func(value *float64) *float64 {
		return roundPublishedPercentPointer(value)
	}
	pctValue := func(value float64) float64 {
		return roundPublishedPercent(value)
	}
	state.ShareDeltaPP = pct(state.ShareDeltaPP)
	state.ResidualContributionPP = pct(state.ResidualContributionPP)
	state.DomesticVolume1SKRW = krw(state.DomesticVolume1SKRW)
	state.BithumbVolume1SKRW = krw(state.BithumbVolume1SKRW)
	state.BithumbShare1SPct = pct(state.BithumbShare1SPct)
	for index := range state.RecentShare1S {
		point := &state.RecentShare1S[index]
		point.DomesticVolume1SKRW = krw(point.DomesticVolume1SKRW)
		point.BithumbVolume1SKRW = krw(point.BithumbVolume1SKRW)
		point.BithumbShare1SPct = pct(point.BithumbShare1SPct)
		point.ObservedBithumbShare1SPct = pct(point.ObservedBithumbShare1SPct)
		point.ExchangeVolumes1SKRW = roundPublishedKRWMap(point.ExchangeVolumes1SKRW)
		point.ExchangeShares1SPct = roundPublishedPercentMap(point.ExchangeShares1SPct)
	}
	for index := range state.Windows {
		window := &state.Windows[index]
		window.CurrentSharePct = pct(window.CurrentSharePct)
		window.PreviousSharePct = pct(window.PreviousSharePct)
		window.ShareDeltaPP = pct(window.ShareDeltaPP)
		window.CurrentVolumeKRW = krw(window.CurrentVolumeKRW)
		window.PreviousVolumeKRW = krw(window.PreviousVolumeKRW)
		window.ThresholdPP = pctValue(window.ThresholdPP)
	}
	state.Drivers = roundPublishedDrivers(state.Drivers)
	state.ExchangeDrivers = roundPublishedExchangeDrivers(state.ExchangeDrivers)
	if state.TenSecondAttribution != nil {
		attribution := *state.TenSecondAttribution
		attribution.ResidualContributionPP = pct(attribution.ResidualContributionPP)
		attribution.Drivers = roundPublishedDrivers(attribution.Drivers)
		attribution.ExchangeDrivers = roundPublishedExchangeDrivers(attribution.ExchangeDrivers)
		state.TenSecondAttribution = &attribution
	}
	for index := range state.CurrentAssets {
		asset := &state.CurrentAssets[index]
		asset.DomesticVolumeKRW = krw(asset.DomesticVolumeKRW)
		asset.BithumbVolumeKRW = krw(asset.BithumbVolumeKRW)
		asset.TopCompetitorVolumeKRW = krw(asset.TopCompetitorVolumeKRW)
		asset.BithumbSharePct = pct(asset.BithumbSharePct)
		asset.BithumbBuyPct = pct(asset.BithumbBuyPct)
	}
	return state
}

func roundPublishedDrivers(drivers []RealtimeFlowDriver) []RealtimeFlowDriver {
	for index := range drivers {
		driver := &drivers[index]
		driver.ContributionPP = roundPublishedPercent(driver.ContributionPP)
		driver.BithumbBuyPct = roundPublishedPercentPointer(driver.BithumbBuyPct)
		for _, amount := range []*float64{
			&driver.BithumbVolumeChangeKRW, &driver.CompetitorVolumeChangeKRW,
			&driver.PreviousBithumbVolumeKRW, &driver.CurrentBithumbVolumeKRW,
			&driver.PreviousCompetitorVolumeKRW, &driver.CurrentCompetitorVolumeKRW,
			&driver.TopCompetitorVolumeKRW,
		} {
			*amount = roundPublishedKRW(*amount)
		}
		for impactIndex := range driver.ExchangeImpacts {
			impact := &driver.ExchangeImpacts[impactIndex]
			impact.ContributionPP = roundPublishedPercent(impact.ContributionPP)
			impact.BuyPct = roundPublishedPercentPointer(impact.BuyPct)
			for _, amount := range []*float64{
				&impact.VolumeChangeKRW, &impact.PreviousVolumeKRW, &impact.CurrentVolumeKRW,
			} {
				*amount = roundPublishedKRW(*amount)
			}
		}
	}
	return drivers
}

func roundPublishedExchangeDrivers(drivers []RealtimeFlowExchangeDriver) []RealtimeFlowExchangeDriver {
	for index := range drivers {
		driver := &drivers[index]
		driver.ContributionPP = roundPublishedPercent(driver.ContributionPP)
		driver.LeadingAssetContributionPP = roundPublishedPercent(driver.LeadingAssetContributionPP)
		for _, amount := range []*float64{
			&driver.VolumeChangeKRW, &driver.PreviousVolumeKRW, &driver.CurrentVolumeKRW,
		} {
			*amount = roundPublishedKRW(*amount)
		}
	}
	return drivers
}

func (a *RealtimeFlowAnalyzer) buildState(latest realtimeFlowFrame) RealtimeFlowState {
	bithumb1, domestic1 := latest.totals.bithumb, latest.totals.domestic
	historySpan := latest.second - a.frames[0].second + 1
	missing := max(int64(0), historySpan-int64(len(a.frames)))
	state := RealtimeFlowState{
		Version: RealtimeFlowVersion, AsOfMS: latest.second * 1_000,
		Status: "WARMING", Direction: "UNKNOWN",
		Reason:              "비교에 필요한 실시간 체결을 모으고 있습니다.",
		DomesticVolume1SKRW: domestic1, BithumbVolume1SKRW: bithumb1,
		BithumbShare1SPct: realtimeFlowPercent(bithumb1, domestic1),
		RecentShare1S:     a.recentSharePoints1S(latest.second),
		Windows:           []RealtimeFlowWindow{}, Drivers: []RealtimeFlowDriver{},
		ExchangeDrivers: []RealtimeFlowExchangeDriver{},
		CurrentAssets:   realtimeFlowCurrentAssets(latest.assets, realtimeFlowMaxCurrentAssets),
		DataQuality: map[string]any{
			"source":       "DOMESTIC_WEBSOCKET_TRADES_AND_TEN_LEVEL_ORDERBOOK_SUMMARY",
			"frame_status": latest.status, "complete_frame": latest.complete,
			"retained_frame_count": len(a.frames), "history_span_sec": historySpan,
			"missing_frame_count": missing, "required_history_sec": realtimeFlowHistorySec,
			"official_24h_reconciled": false,
		},
	}
	state.DataQuality["recent_share_1s_count"] = len(state.RecentShare1S)
	if !latest.complete || latest.status != "LIVE" {
		state.Status = "DEGRADED"
		state.Reason = "일부 거래소 체결이 빠져 있어 이번 변화는 판단하지 않습니다."
	}

	evaluations := make([]realtimeFlowEvaluation, 0, len(realtimeFlowWindows))
	for _, duration := range realtimeFlowWindows {
		item := a.evaluateWindow(latest.second, duration)
		evaluations = append(evaluations, item)
		state.Windows = append(state.Windows, item.window)
	}
	if len(evaluations) > 0 {
		state.TenSecondAttribution = realtimeFlowEvaluationAttribution(evaluations[0])
	}
	primary, dominant, up, down := chooseRealtimeFlowPrimary(evaluations)
	if primary >= 0 {
		selected := evaluations[primary]
		state.PrimaryWindowSec = selected.window.DurationSec
		state.ShareDeltaPP = copyFloat(selected.window.ShareDeltaPP)
		state.Drivers = selected.drivers
		state.StoredDriverCount = len(selected.drivers)
		state.TotalDriverCount = selected.totalDriverCount
		state.ExchangeDrivers = selected.exchangeDrivers
		if state.ShareDeltaPP != nil {
			residual := *state.ShareDeltaPP
			for _, driver := range state.Drivers {
				residual -= driver.ContributionPP
			}
			state.ResidualContributionPP = &residual
		}
	}
	if state.Status != "DEGRADED" && historySpan >= realtimeFlowHistorySec && missing > 0 {
		state.Status = "DEGRADED"
		state.Direction = "UNKNOWN"
		state.Reason = "실시간 체결 프레임 사이에 공백이 있어 변화 판단을 보류합니다."
		return state
	}
	if state.Status == "DEGRADED" {
		return state
	}
	switch {
	case up >= 2 || down >= 2:
		state.Status, state.Direction = "ALERT", dominant
		state.Reason = "여러 비교 구간에서 같은 방향의 점유율 변화가 이어지고 있습니다."
	case up == 1 || down == 1:
		state.Status, state.Direction = "WATCH", dominant
		if up > 0 && down > 0 {
			state.Reason = "비교 구간별 변화 방향이 엇갈려 더 긴 구간을 기준으로 확인하고 있습니다."
		} else {
			state.Reason = "한 비교 구간에서 큰 변화가 나타나 이어지는지 확인하고 있습니다."
		}
	case primary >= 0:
		state.Status, state.Direction = "NORMAL", "STABLE"
		state.Reason = "거래금액과 지속 시간을 함께 보면 평소 범위입니다."
	}
	return state
}

func (a *RealtimeFlowAnalyzer) recentSharePoints1S(latestSecond int64) []RealtimeFlowSharePoint1S {
	const retainedSeconds int64 = 60
	start := latestSecond - retainedSeconds + 1
	result := make([]RealtimeFlowSharePoint1S, 0, retainedSeconds)
	for _, frame := range a.frames {
		if frame.second < start || frame.second > latestSecond {
			continue
		}
		totals := frame.totals
		complete := frame.complete && frame.status == "LIVE"
		var share *float64
		exchangeShares := map[string]*float64{}
		if complete {
			share = realtimeFlowPercent(totals.bithumb, totals.domestic)
			for _, exchange := range domain.DomesticExchanges {
				exchangeShares[string(exchange)] = realtimeFlowPercent(totals.volumes[string(exchange)], totals.domestic)
			}
		}
		// The point gets its own maps, so the published state cannot change
		// the frame.
		result = append(result, RealtimeFlowSharePoint1S{
			AsOfMS: frame.second * 1_000, Status: frame.status, Complete: complete,
			DomesticVolume1SKRW: totals.domestic, BithumbVolume1SKRW: totals.bithumb,
			BithumbShare1SPct:         share,
			ObservedBithumbShare1SPct: realtimeFlowPercent(totals.bithumb, totals.domestic),
			ExchangeVolumes1SKRW:      maps.Clone(totals.volumes),
			ExchangeTradeCounts1S:     maps.Clone(totals.tradeCounts),
			ExchangeShares1SPct:       exchangeShares,
		})
	}
	return result
}

func realtimeFlowExchangeVolumes(values realtimeFlowAggregate) map[string]float64 {
	result := make(map[string]float64, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		result[string(exchange)] = 0
	}
	for _, row := range values.assets {
		for exchange, volume := range row.exchanges {
			result[exchange] += volume
		}
	}
	return result
}

func realtimeFlowExchangeTradeCounts(values realtimeFlowAggregate) map[string]int64 {
	result := make(map[string]int64, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		result[string(exchange)] = 0
	}
	for _, row := range values.assets {
		for exchange, count := range row.tradeCounts {
			result[exchange] += count
		}
	}
	return result
}

func realtimeFlowEvaluationAttribution(item realtimeFlowEvaluation) *RealtimeFlowAttribution {
	if item.window.Status != "COMPLETE" || item.window.ShareDeltaPP == nil {
		return nil
	}
	attribution := &RealtimeFlowAttribution{
		WindowSec:         item.window.DurationSec,
		StoredDriverCount: len(item.drivers),
		TotalDriverCount:  item.totalDriverCount,
		Drivers:           item.drivers,
		ExchangeDrivers:   item.exchangeDrivers,
	}
	residual := *item.window.ShareDeltaPP
	for _, driver := range item.drivers {
		residual -= driver.ContributionPP
	}
	attribution.ResidualContributionPP = &residual
	return attribution
}

type realtimeFlowEvaluation struct {
	window           RealtimeFlowWindow
	drivers          []RealtimeFlowDriver
	exchangeDrivers  []RealtimeFlowExchangeDriver
	totalDriverCount int
	crossed          bool
	direction        string
}

func (a *RealtimeFlowAnalyzer) evaluateWindow(now, duration int64) realtimeFlowEvaluation {
	result := realtimeFlowEvaluation{window: RealtimeFlowWindow{
		DurationSec: duration, Status: "PARTIAL", ThresholdPP: realtimeFlowThreshold(duration),
	}}
	current := aggregateRealtimeFlowFrames(a.frames, now-duration+1, now)
	previous := aggregateRealtimeFlowFrames(a.frames, now-2*duration+1, now-duration)
	currentBithumb, currentTotal := realtimeFlowTotals(current)
	previousBithumb, previousTotal := realtimeFlowTotals(previous)
	result.window.CurrentVolumeKRW, result.window.PreviousVolumeKRW = currentTotal, previousTotal
	result.window.CurrentSharePct = realtimeFlowPercent(currentBithumb, currentTotal)
	result.window.PreviousSharePct = realtimeFlowPercent(previousBithumb, previousTotal)
	if !current.complete || !previous.complete || current.seconds != duration || previous.seconds != duration {
		return result
	}
	result.window.Status = "COMPLETE"
	if result.window.CurrentSharePct == nil || result.window.PreviousSharePct == nil {
		return result
	}
	delta := *result.window.CurrentSharePct - *result.window.PreviousSharePct
	result.window.ShareDeltaPP = &delta
	result.drivers, result.exchangeDrivers, result.totalDriverCount = buildRealtimeFlowDrivers(previous, current)
	minimumVolume := float64(duration) * a.config.MinVolumePerSecondKRW
	if currentTotal < minimumVolume || previousTotal < minimumVolume || math.Abs(delta) < result.window.ThresholdPP {
		return result
	}
	result.crossed = true
	if delta < 0 {
		result.direction = "DOWN"
	} else {
		result.direction = "UP"
	}
	return result
}

func chooseRealtimeFlowPrimary(items []realtimeFlowEvaluation) (primary int, dominant string, up, down int) {
	primary = -1
	for index, item := range items {
		if !item.crossed {
			continue
		}
		primary = index
		if item.direction == "UP" {
			up++
		} else {
			down++
		}
	}
	if primary >= 0 {
		if up > down {
			dominant = "UP"
		} else if down > up {
			dominant = "DOWN"
		} else {
			dominant = items[primary].direction
		}
		for index, item := range items {
			if item.crossed && item.direction == dominant {
				primary = index
			}
		}
		return primary, dominant, up, down
	}
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].window.Status == "COMPLETE" {
			primary = index
			break
		}
	}
	return primary, "", up, down
}

func aggregateRealtimeFlowFrames(frames []realtimeFlowFrame, start, end int64) realtimeFlowAggregate {
	result := realtimeFlowAggregate{assets: make(map[string]realtimeFlowAssetFrame), complete: true}
	seen := make(map[int64]struct{})
	for _, item := range frames {
		if item.second < start || item.second > end {
			continue
		}
		seen[item.second] = struct{}{}
		result.complete = result.complete && item.complete && item.status == "LIVE"
		for asset, values := range item.assets {
			row := result.assets[asset]
			row.bithumb += values.bithumb
			row.competitor += values.competitor
			if row.exchanges == nil {
				row.exchanges = make(map[string]float64)
			}
			for exchange, value := range values.exchanges {
				row.exchanges[exchange] += value
			}
			if row.tradeCounts == nil {
				row.tradeCounts = make(map[string]int64)
			}
			for exchange, count := range values.tradeCounts {
				row.tradeCounts[exchange] += count
			}
			if row.buys == nil {
				row.buys = make(map[string]float64)
			}
			for exchange, value := range values.buys {
				row.buys[exchange] += value
			}
			if row.sells == nil {
				row.sells = make(map[string]float64)
			}
			for exchange, value := range values.sells {
				row.sells[exchange] += value
			}
			if row.orderbooks == nil {
				row.orderbooks = make(map[string]*movement.RealtimeOrderbook)
			}
			// Frames never change their books, and the drivers copy what they
			// publish (realtimeFlowOrderbook), so the window keeps the latest
			// frame's books as they are.
			for exchange, book := range values.orderbooks {
				row.orderbooks[exchange] = book
			}
			if values.orderbook != nil {
				row.orderbook = values.orderbook
			}
			result.assets[asset] = row
		}
	}
	result.seconds = int64(len(seen))
	if result.seconds == 0 {
		result.complete = false
	}
	return result
}

func buildRealtimeFlowDrivers(previous, current realtimeFlowAggregate) ([]RealtimeFlowDriver, []RealtimeFlowExchangeDriver, int) {
	previousBithumb, previousTotal := realtimeFlowTotals(previous)
	currentBithumb, currentTotal := realtimeFlowTotals(current)
	previousCompetitor := previousTotal - previousBithumb
	currentCompetitor := currentTotal - currentBithumb
	bithumbCoefficient, competitorCoefficient := pathCoefficients(
		previousBithumb, previousCompetitor, currentBithumb, currentCompetitor,
	)
	assets := make(map[string]struct{}, len(previous.assets)+len(current.assets))
	for asset := range previous.assets {
		assets[asset] = struct{}{}
	}
	for asset := range current.assets {
		assets[asset] = struct{}{}
	}
	drivers := make([]RealtimeFlowDriver, 0, len(assets))
	for asset := range assets {
		old, next := previous.assets[asset], current.assets[asset]
		oldCompetitor, nextCompetitor := realtimeFlowCompetitorTotal(old), realtimeFlowCompetitorTotal(next)
		impacts := realtimeFlowExchangeImpacts(old, next, bithumbCoefficient, competitorCoefficient)
		topExchange, topVolume := realtimeFlowTopCompetitor(next.exchanges)
		drivers = append(drivers, RealtimeFlowDriver{
			Asset: asset,
			ContributionPP: (next.bithumb-old.bithumb)*bithumbCoefficient*100 +
				(nextCompetitor-oldCompetitor)*competitorCoefficient*100,
			BithumbVolumeChangeKRW:    next.bithumb - old.bithumb,
			CompetitorVolumeChangeKRW: nextCompetitor - oldCompetitor,
			PreviousBithumbVolumeKRW:  old.bithumb, CurrentBithumbVolumeKRW: next.bithumb,
			PreviousCompetitorVolumeKRW: oldCompetitor, CurrentCompetitorVolumeKRW: nextCompetitor,
			BithumbBuyPct: realtimeFlowBuyPct(next, string(domain.ExchangeBithumb)),
			TopCompetitor: topExchange, TopCompetitorVolumeKRW: topVolume,
			ExchangeImpacts: impacts, Orderbook: realtimeFlowOrderbook(next, string(domain.ExchangeBithumb)),
		})
	}
	sort.Slice(drivers, func(i, j int) bool {
		left, right := math.Abs(drivers[i].ContributionPP), math.Abs(drivers[j].ContributionPP)
		if left != right {
			return left > right
		}
		return drivers[i].Asset < drivers[j].Asset
	})
	totalCount := len(drivers)
	if len(drivers) > realtimeFlowMaxDrivers {
		drivers = drivers[:realtimeFlowMaxDrivers]
	}
	for index := range drivers {
		drivers[index].Rank = index + 1
	}
	return drivers, realtimeFlowExchangeDrivers(previous, current, bithumbCoefficient, competitorCoefficient), totalCount
}

func realtimeFlowExchangeImpacts(previous, current realtimeFlowAssetFrame, bithumbCoefficient, competitorCoefficient float64) []RealtimeFlowExchangeImpact {
	exchanges := make(map[string]struct{}, len(previous.exchanges)+len(current.exchanges))
	for exchange := range previous.exchanges {
		exchanges[exchange] = struct{}{}
	}
	for exchange := range current.exchanges {
		exchanges[exchange] = struct{}{}
	}
	result := make([]RealtimeFlowExchangeImpact, 0, len(exchanges))
	for exchange := range exchanges {
		old, next := previous.exchanges[exchange], current.exchanges[exchange]
		coefficient := competitorCoefficient
		if exchange == string(domain.ExchangeBithumb) {
			coefficient = bithumbCoefficient
		}
		result = append(result, RealtimeFlowExchangeImpact{
			Exchange: exchange, ContributionPP: (next - old) * coefficient * 100,
			VolumeChangeKRW: next - old, PreviousVolumeKRW: old, CurrentVolumeKRW: next,
			BuyPct: realtimeFlowBuyPct(current, exchange), Orderbook: realtimeFlowOrderbook(current, exchange),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := math.Abs(result[i].ContributionPP), math.Abs(result[j].ContributionPP)
		if left != right {
			return left > right
		}
		return result[i].Exchange < result[j].Exchange
	})
	return result
}

func realtimeFlowExchangeDrivers(previous, current realtimeFlowAggregate, bithumbCoefficient, competitorCoefficient float64) []RealtimeFlowExchangeDriver {
	type total struct {
		contribution, previous, current float64
		leadingAsset                    string
		leadingContribution             float64
	}
	rows := make(map[string]total)
	assets := make(map[string]struct{}, len(previous.assets)+len(current.assets))
	for asset := range previous.assets {
		assets[asset] = struct{}{}
	}
	for asset := range current.assets {
		assets[asset] = struct{}{}
	}
	for asset := range assets {
		for _, impact := range realtimeFlowExchangeImpacts(previous.assets[asset], current.assets[asset], bithumbCoefficient, competitorCoefficient) {
			row := rows[impact.Exchange]
			row.contribution += impact.ContributionPP
			row.previous += impact.PreviousVolumeKRW
			row.current += impact.CurrentVolumeKRW
			if math.Abs(impact.ContributionPP) > math.Abs(row.leadingContribution) || row.leadingAsset == "" {
				row.leadingAsset, row.leadingContribution = asset, impact.ContributionPP
			}
			rows[impact.Exchange] = row
		}
	}
	result := make([]RealtimeFlowExchangeDriver, 0, len(rows))
	for exchange, row := range rows {
		result = append(result, RealtimeFlowExchangeDriver{
			Exchange: exchange, ContributionPP: row.contribution,
			VolumeChangeKRW:   row.current - row.previous,
			PreviousVolumeKRW: row.previous, CurrentVolumeKRW: row.current,
			LeadingAsset: row.leadingAsset, LeadingAssetContributionPP: row.leadingContribution,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := math.Abs(result[i].ContributionPP), math.Abs(result[j].ContributionPP)
		if left != right {
			return left > right
		}
		return result[i].Exchange < result[j].Exchange
	})
	for index := range result {
		result[index].Rank = index + 1
	}
	return result
}

func realtimeFlowCurrentAssets(assets map[string]realtimeFlowAssetFrame, limit int) []RealtimeFlowCurrentAsset {
	result := make([]RealtimeFlowCurrentAsset, 0, len(assets))
	for asset, row := range assets {
		domestic := row.bithumb + realtimeFlowCompetitorTotal(row)
		topExchange, topVolume := realtimeFlowTopCompetitor(row.exchanges)
		result = append(result, RealtimeFlowCurrentAsset{
			Asset: asset, DomesticVolumeKRW: domestic, BithumbVolumeKRW: row.bithumb,
			BithumbSharePct: realtimeFlowPercent(row.bithumb, domestic),
			BithumbBuyPct:   realtimeFlowBuyPct(row, string(domain.ExchangeBithumb)),
			TopCompetitor:   topExchange, TopCompetitorVolumeKRW: topVolume,
			Orderbook: realtimeFlowOrderbook(row, string(domain.ExchangeBithumb)),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DomesticVolumeKRW != result[j].DomesticVolumeKRW {
			return result[i].DomesticVolumeKRW > result[j].DomesticVolumeKRW
		}
		return result[i].Asset < result[j].Asset
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func realtimeFlowTotals(values realtimeFlowAggregate) (bithumb, total float64) {
	for _, row := range values.assets {
		bithumb += row.bithumb
		total += row.bithumb + realtimeFlowCompetitorTotal(row)
	}
	return bithumb, total
}

func realtimeFlowCompetitorTotal(row realtimeFlowAssetFrame) float64 {
	if len(row.exchanges) == 0 {
		return row.competitor
	}
	result := 0.0
	for exchange, value := range row.exchanges {
		if exchange != string(domain.ExchangeBithumb) {
			result += value
		}
	}
	return result
}

func realtimeFlowBuyPct(row realtimeFlowAssetFrame, exchange string) *float64 {
	return realtimeFlowPercent(row.buys[exchange], row.buys[exchange]+row.sells[exchange])
}

func realtimeFlowOrderbook(row realtimeFlowAssetFrame, exchange string) *movement.RealtimeOrderbook {
	if book := row.orderbooks[exchange]; book != nil {
		return cloneRealtimeOrderbook(book)
	}
	if exchange == string(domain.ExchangeBithumb) {
		return cloneRealtimeOrderbook(row.orderbook)
	}
	return nil
}

func realtimeFlowTopCompetitor(exchanges map[string]float64) (string, float64) {
	name, volume := "", 0.0
	for exchange, value := range exchanges {
		if exchange == string(domain.ExchangeBithumb) || value < volume || (value == volume && name != "" && exchange > name) {
			continue
		}
		name, volume = exchange, value
	}
	return name, volume
}

func realtimeFlowPercent(numerator, denominator float64) *float64 {
	if denominator <= 0 {
		return nil
	}
	value := numerator / denominator * 100
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

// The one-second series is published once per second and repeats a sixty
// entry window, so every extra float64 digit is resent sixty times. JSON
// encodes the shortest round-trip form of a full precision division, which
// is far past what any KRW amount or published percentage means.
//
// Published percentages and KRW amounts carry two decimals. Rounding must not
// move a value onto a boundary it has not reached: a real 0.001% observation
// would publish as the 0.00 this project reserves for observed zero, and a
// 99.997% share or coverage would publish as a complete 100.00%. Such a value
// keeps adding decimals until it stays on its own side of the boundary.
// Sums, comparisons and stored history are not rounded: only published copies.
const (
	realtimeFlowPublishedKRWDigits     = 2
	realtimeFlowPublishedPercentDigits = 2
	realtimeFlowPublishedMaxDigits     = 12
)

func roundHalfAwayFromZero(value float64, digits int) float64 {
	// A float64 at or above 2^52 has no fractional part, so rounding it is the
	// identity. Scaling it is worse than useless: the aggregators saturate an
	// overflowing sum to math.MaxFloat64, and value*scale then overflows to
	// +Inf, which encoding/json refuses and which would drop the whole frame.
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) >= 1<<52 {
		return value
	}
	scale := math.Pow(10, float64(digits))
	return math.Round(value*scale) / scale
}

// roundPublishedAvoiding keeps the requested resolution unless it would land
// exactly on one of the boundaries the original value does not equal.
func roundPublishedAvoiding(value float64, digits int, boundaries ...float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	crosses := func(rounded float64) bool {
		for _, boundary := range boundaries {
			if rounded == boundary && value != boundary {
				return true
			}
		}
		return false
	}
	for resolution := digits; resolution <= realtimeFlowPublishedMaxDigits; resolution++ {
		if rounded := roundHalfAwayFromZero(value, resolution); !crosses(rounded) {
			return rounded
		}
	}
	return value
}

func roundPublishedPercent(value float64) float64 {
	return roundPublishedAvoiding(value, realtimeFlowPublishedPercentDigits, 0, 100, -100)
}

func roundPublishedKRW(value float64) float64 {
	return roundPublishedAvoiding(value, realtimeFlowPublishedKRWDigits, 0)
}

func roundPublishedPercentPointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	rounded := roundPublishedPercent(*value)
	return &rounded
}

func roundPublishedKRWMap(values map[string]float64) map[string]float64 {
	for key, value := range values {
		values[key] = roundPublishedKRW(value)
	}
	return values
}

func roundPublishedPercentMap(values map[string]*float64) map[string]*float64 {
	for key, value := range values {
		values[key] = roundPublishedPercentPointer(value)
	}
	return values
}

func realtimeFlowThreshold(duration int64) float64 {
	switch duration {
	case 10:
		return 8
	case 30:
		return 5
	default:
		return 3
	}
}

func cloneRealtimeFlowState(source RealtimeFlowState) RealtimeFlowState {
	result := source
	result.ShareDeltaPP = copyFloat(source.ShareDeltaPP)
	result.ResidualContributionPP = copyFloat(source.ResidualContributionPP)
	result.BithumbShare1SPct = copyFloat(source.BithumbShare1SPct)
	result.RecentShare1S = append(make([]RealtimeFlowSharePoint1S, 0, len(source.RecentShare1S)), source.RecentShare1S...)
	for index := range result.RecentShare1S {
		result.RecentShare1S[index].BithumbShare1SPct = copyFloat(source.RecentShare1S[index].BithumbShare1SPct)
		result.RecentShare1S[index].ObservedBithumbShare1SPct = copyFloat(source.RecentShare1S[index].ObservedBithumbShare1SPct)
		result.RecentShare1S[index].ExchangeShares1SPct = make(map[string]*float64, len(source.RecentShare1S[index].ExchangeShares1SPct))
		for exchange, share := range source.RecentShare1S[index].ExchangeShares1SPct {
			result.RecentShare1S[index].ExchangeShares1SPct[exchange] = copyFloat(share)
		}
		result.RecentShare1S[index].ExchangeVolumes1SKRW = make(map[string]float64, len(source.RecentShare1S[index].ExchangeVolumes1SKRW))
		for exchange, volume := range source.RecentShare1S[index].ExchangeVolumes1SKRW {
			result.RecentShare1S[index].ExchangeVolumes1SKRW[exchange] = volume
		}
		result.RecentShare1S[index].ExchangeTradeCounts1S = make(map[string]int64, len(source.RecentShare1S[index].ExchangeTradeCounts1S))
		for exchange, count := range source.RecentShare1S[index].ExchangeTradeCounts1S {
			result.RecentShare1S[index].ExchangeTradeCounts1S[exchange] = count
		}
	}
	result.Windows = append(make([]RealtimeFlowWindow, 0, len(source.Windows)), source.Windows...)
	for index := range result.Windows {
		result.Windows[index].CurrentSharePct = copyFloat(source.Windows[index].CurrentSharePct)
		result.Windows[index].PreviousSharePct = copyFloat(source.Windows[index].PreviousSharePct)
		result.Windows[index].ShareDeltaPP = copyFloat(source.Windows[index].ShareDeltaPP)
	}
	result.Drivers = cloneRealtimeFlowDrivers(source.Drivers)
	result.ExchangeDrivers = append(
		make([]RealtimeFlowExchangeDriver, 0, len(source.ExchangeDrivers)),
		source.ExchangeDrivers...,
	)
	result.TenSecondAttribution = cloneRealtimeFlowAttribution(source.TenSecondAttribution)
	result.CurrentAssets = append(
		make([]RealtimeFlowCurrentAsset, 0, len(source.CurrentAssets)),
		source.CurrentAssets...,
	)
	for index := range result.CurrentAssets {
		result.CurrentAssets[index].BithumbSharePct = copyFloat(source.CurrentAssets[index].BithumbSharePct)
		result.CurrentAssets[index].BithumbBuyPct = copyFloat(source.CurrentAssets[index].BithumbBuyPct)
		result.CurrentAssets[index].Orderbook = cloneRealtimeOrderbook(source.CurrentAssets[index].Orderbook)
	}
	result.DataQuality = make(map[string]any, len(source.DataQuality))
	for key, value := range source.DataQuality {
		result.DataQuality[key] = value
	}
	return result
}

// CloneRealtimeFlowState returns a detached state for asynchronous persistence
// boundaries such as the blue/green handoff buffer.
func CloneRealtimeFlowState(source RealtimeFlowState) RealtimeFlowState {
	return cloneRealtimeFlowState(source)
}

func cloneRealtimeFlowAttribution(source *RealtimeFlowAttribution) *RealtimeFlowAttribution {
	if source == nil {
		return nil
	}
	result := *source
	result.ResidualContributionPP = copyFloat(source.ResidualContributionPP)
	result.Drivers = cloneRealtimeFlowDrivers(source.Drivers)
	result.ExchangeDrivers = append(
		make([]RealtimeFlowExchangeDriver, 0, len(source.ExchangeDrivers)),
		source.ExchangeDrivers...,
	)
	return &result
}

func cloneRealtimeFlowDrivers(source []RealtimeFlowDriver) []RealtimeFlowDriver {
	result := append(make([]RealtimeFlowDriver, 0, len(source)), source...)
	for index := range result {
		result[index].BithumbBuyPct = copyFloat(source[index].BithumbBuyPct)
		result[index].Orderbook = cloneRealtimeOrderbook(source[index].Orderbook)
		result[index].ExchangeImpacts = append(
			make([]RealtimeFlowExchangeImpact, 0, len(source[index].ExchangeImpacts)),
			source[index].ExchangeImpacts...,
		)
		for impactIndex := range result[index].ExchangeImpacts {
			impact := &result[index].ExchangeImpacts[impactIndex]
			impact.BuyPct = copyFloat(impact.BuyPct)
			impact.Orderbook = cloneRealtimeOrderbook(impact.Orderbook)
		}
	}
	return result
}

func cloneRealtimeOrderbook(source *movement.RealtimeOrderbook) *movement.RealtimeOrderbook {
	if source == nil {
		return nil
	}
	result := *source
	result.AgeSec = copyFloat(source.AgeSec)
	result.Imbalance = copyFloat(source.Imbalance)
	result.SpreadBPS = copyFloat(source.SpreadBPS)
	result.BidDepthKRW = copyFloat(source.BidDepthKRW)
	result.AskDepthKRW = copyFloat(source.AskDepthKRW)
	result.BuySlippageBPS = copyFloat(source.BuySlippageBPS)
	result.SellSlippageBPS = copyFloat(source.SellSlippageBPS)
	return &result
}

func copyFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
