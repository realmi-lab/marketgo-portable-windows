package analysis

import (
	"math"
	"sort"

	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/notice"
)

const RollingWindowSec int64 = 86400

var flowHorizons = []int64{60, 300, 600, 1200, 1800, 3600}
var flowNames = map[int64]string{60: "1m", 300: "5m", 600: "10m", 1200: "20m", 1800: "30m", 3600: "1h"}

type RollingInput struct {
	AsOf            int64
	Current         []Row
	CurrentCoverage coverage.Result
	Horizons        map[int64]RollingInterval
	ClosedMinutes   map[int64]ClosedMinuteInterval
}

type RollingInterval struct {
	Incoming         []Row
	Expiring         []Row
	PreviousCoverage coverage.Result
	IncomingCoverage coverage.Result
	ExpiringCoverage coverage.Result
}

type ClosedMinuteInterval struct {
	Previous         []Row
	Current          []Row
	PreviousCoverage coverage.Result
	CurrentCoverage  coverage.Result
}

type RollingTotals struct {
	Bithumb    float64
	Competitor float64
	Domestic   float64
}

type Rolling24h struct {
	CurrentSharePct   *float64        `json:"current_share_pct"`
	BithumbVolumeKRW  *float64        `json:"bithumb_volume_krw"`
	DomesticVolumeKRW *float64        `json:"domestic_volume_krw"`
	Status            string          `json:"status"`
	Coverage          coverage.Result `json:"coverage"`
}

type RollingHorizon struct {
	Window                      string                     `json:"window"`
	DurationSec                 int64                      `json:"duration_sec"`
	Status                      string                     `json:"status"`
	Rolling24hSharePct          *float64                   `json:"rolling_24h_share_pct"`
	PreviousRolling24hSharePct  *float64                   `json:"previous_rolling_24h_share_pct"`
	Rolling24hDeltaPP           *float64                   `json:"rolling_24h_delta_pp"`
	RecentFlowSharePct          *float64                   `json:"recent_flow_share_pct"`
	ExpiringFlowSharePct        *float64                   `json:"expiring_flow_share_pct"`
	RecentFlowVolumeKRW         float64                    `json:"recent_flow_volume_krw"`
	RecentBithumbVolumeKRW      float64                    `json:"recent_bithumb_volume_krw"`
	RecentCompetitorVolumeKRW   float64                    `json:"recent_competitor_volume_krw"`
	ExpiringFlowVolumeKRW       float64                    `json:"expiring_flow_volume_krw"`
	ExpiringBithumbVolumeKRW    float64                    `json:"expiring_bithumb_volume_krw"`
	ExpiringCompetitorVolumeKRW float64                    `json:"expiring_competitor_volume_krw"`
	ReplacementGapPP            *float64                   `json:"replacement_gap_pp"`
	IncomingEffectPP            *float64                   `json:"incoming_effect_pp"`
	ExpiringEffectPP            *float64                   `json:"expiring_effect_pp"`
	TotalEffectPP               *float64                   `json:"total_effect_pp"`
	ReconciliationErrorPP       *float64                   `json:"reconciliation_error_pp"`
	Coverage                    map[string]coverage.Result `json:"coverage"`
	Anomaly                     RollingAnomaly             `json:"anomaly"`
}

type RollingAnomaly struct {
	Status   string   `json:"status"`
	MedianPP *float64 `json:"median_pp"`
	MADPP    *float64 `json:"mad_pp"`
	RobustZ  *float64 `json:"robust_z"`
}

type RollingDriver struct {
	Base                        string         `json:"base,omitempty"`
	Exchange                    string         `json:"exchange,omitempty"`
	AssetIdentity               *AssetIdentity `json:"asset_identity,omitempty"`
	IncomingEffectPP            float64        `json:"incoming_effect_pp"`
	ExpiringEffectPP            float64        `json:"expiring_effect_pp"`
	ContributionPP              *float64       `json:"contribution_pp"`
	RecentBithumbVolumeKRW      float64        `json:"recent_bithumb_volume_krw"`
	RecentCompetitorVolumeKRW   float64        `json:"recent_competitor_volume_krw"`
	ExpiringBithumbVolumeKRW    float64        `json:"expiring_bithumb_volume_krw"`
	ExpiringCompetitorVolumeKRW float64        `json:"expiring_competitor_volume_krw"`
	PreviousVolumeKRW           float64        `json:"previous_volume_krw"`
	RecentVolumeKRW             float64        `json:"recent_volume_krw"`
	RecentVsExpiringChangePct   *float64       `json:"recent_vs_expiring_volume_change_pct"`
	Reason                      string         `json:"reason"`
}

type ObservedFlowDriver struct {
	Base                        string         `json:"base,omitempty"`
	Exchange                    string         `json:"exchange,omitempty"`
	AssetIdentity               *AssetIdentity `json:"asset_identity,omitempty"`
	RecentBithumbVolumeKRW      *float64       `json:"recent_bithumb_volume_krw"`
	RecentCompetitorVolumeKRW   *float64       `json:"recent_competitor_volume_krw"`
	RecentVolumeKRW             *float64       `json:"recent_volume_krw"`
	RecentFlowSharePct          *float64       `json:"recent_flow_share_pct"`
	ExpiringBithumbVolumeKRW    *float64       `json:"expiring_bithumb_volume_krw"`
	ExpiringCompetitorVolumeKRW *float64       `json:"expiring_competitor_volume_krw"`
	ExpiringVolumeKRW           *float64       `json:"expiring_volume_krw"`
	ExpiringFlowSharePct        *float64       `json:"expiring_flow_share_pct"`
	ReplacementGapPP            *float64       `json:"replacement_gap_pp"`
	RecentVsExpiringChangePct   *float64       `json:"recent_vs_expiring_volume_change_pct"`
	Reason                      string         `json:"reason"`
}

type RollingBridge struct {
	Window                  string               `json:"window"`
	DurationSec             int64                `json:"duration_sec"`
	Status                  string               `json:"status"`
	PreviousSharePct        *float64             `json:"previous_share_pct"`
	CurrentSharePct         *float64             `json:"current_share_pct"`
	IncomingEffectPP        *float64             `json:"incoming_effect_pp"`
	ExpiringEffectPP        *float64             `json:"expiring_effect_pp"`
	TotalEffectPP           *float64             `json:"total_effect_pp"`
	ReconciliationErrorPP   *float64             `json:"reconciliation_error_pp"`
	AssetDrivers            []RollingDriver      `json:"asset_drivers"`
	ExchangeDrivers         []RollingDriver      `json:"exchange_drivers"`
	ObservedAssetDrivers    []ObservedFlowDriver `json:"observed_asset_drivers"`
	ObservedExchangeDrivers []ObservedFlowDriver `json:"observed_exchange_drivers"`
}

type RollingMonitor struct {
	Version                 string                  `json:"version"`
	MarketScope             string                  `json:"market_scope"`
	AsOfMS                  int64                   `json:"as_of_ms"`
	WindowSec               int64                   `json:"window_sec"`
	Rolling24h              Rolling24h              `json:"rolling_24h"`
	Horizons                []RollingHorizon        `json:"horizons"`
	ClosedMinuteComparisons ClosedMinuteComparisons `json:"closed_minute_comparisons"`
	Bridge                  RollingBridge           `json:"bridge"`
	Bridges                 []RollingBridge         `json:"bridges"`
	Movement                RollingMovement         `json:"movement"`
	Trend                   []RollingTrend          `json:"trend"`
	Briefing                RollingBriefing         `json:"briefing"`
	DataQuality             map[string]any          `json:"data_quality"`
	Evidence                RollingEvidence         `json:"evidence"`
}

type RollingBriefing struct {
	Status string  `json:"status"`
	Window *string `json:"window"`
	Text   string  `json:"text"`
}

type RollingEvidence struct {
	DomesticNotices               []RollingNoticeEvidence        `json:"domestic_notices"`
	DomesticNoticeCounts          map[string]int                 `json:"domestic_notice_counts"`
	DomesticNoticeAvailableCounts map[string]int                 `json:"domestic_notice_available_counts"`
	DomesticNoticeSources         map[string]RollingNoticeSource `json:"domestic_notice_sources"`
	CausalClaim                   bool                           `json:"causal_claim"`
	NoticeWindowStartMS           *int64                         `json:"notice_window_start_ms"`
}

type RollingNoticeSource struct {
	Status        string  `json:"status"`
	FetchedCount  int     `json:"fetched_count"`
	HistoryStatus string  `json:"history_status"`
	LastSuccessTS float64 `json:"last_success_ts"`
	Error         *string `json:"error"`
}

type RollingNoticeEvidence struct {
	Exchange               string                   `json:"exchange"`
	NoticeID               string                   `json:"notice_id"`
	Title                  string                   `json:"title"`
	URL                    string                   `json:"url"`
	Category               string                   `json:"category"`
	PublishedTSMS          int64                    `json:"published_ts_ms"`
	UpdatedTSMS            int64                    `json:"updated_ts_ms"`
	DetectedTSMS           int64                    `json:"-"`
	AnchorTSMS             *int64                   `json:"anchor_ts_ms"`
	Assets                 []string                 `json:"assets"`
	Relation               string                   `json:"relation"`
	Related                bool                     `json:"related"`
	RelatedLabel           string                   `json:"related_label"`
	RelationReason         string                   `json:"relation_reason"`
	TimingLabel            string                   `json:"timing_label"`
	ContentBasis           string                   `json:"content_basis"`
	ContentPriorityScore   int                      `json:"content_priority_score"`
	PriorityLabel          string                   `json:"priority_label"`
	ContentRelated         bool                     `json:"content_related"`
	TimeRelated            bool                     `json:"time_related"`
	ContentScheduleRelated bool                     `json:"content_schedule_related"`
	ScheduleActiveNow      bool                     `json:"schedule_active_now"`
	ScheduleRelated        bool                     `json:"schedule_related"`
	MarketContext          []map[string]any         `json:"market_context"`
	ImageConsistency       *notice.ImageConsistency `json:"image_consistency,omitempty"`
	Brief                  notice.Brief             `json:"brief"`
	CausalClaim            bool                     `json:"causal_claim"`
}

type ClosedMinuteComparisons struct {
	Source      string                   `json:"source"`
	BarSec      int64                    `json:"bar_sec"`
	AsOfMS      *int64                   `json:"as_of_ms"`
	Comparisons []ClosedMinuteComparison `json:"comparisons"`
}

type ClosedMinuteComparison struct {
	Window                    string                     `json:"window"`
	DurationSec               int64                      `json:"duration_sec"`
	Status                    string                     `json:"status"`
	ClosedAtMS                int64                      `json:"closed_at_ms"`
	PreviousSharePct          *float64                   `json:"previous_share_pct"`
	CurrentSharePct           *float64                   `json:"current_share_pct"`
	ShareDeltaPP              *float64                   `json:"share_delta_pp"`
	PreviousBithumbVolumeKRW  float64                    `json:"previous_bithumb_volume_krw"`
	PreviousDomesticVolumeKRW float64                    `json:"previous_domestic_volume_krw"`
	CurrentBithumbVolumeKRW   float64                    `json:"current_bithumb_volume_krw"`
	CurrentDomesticVolumeKRW  float64                    `json:"current_domestic_volume_krw"`
	Coverage                  map[string]coverage.Result `json:"coverage"`
	AssetChanges              []ClosedMinuteAssetChange  `json:"asset_changes"`
}

type ClosedMinuteAssetChange struct {
	Base                        string  `json:"base"`
	PreviousBithumbVolumeKRW    float64 `json:"previous_bithumb_volume_krw"`
	CurrentBithumbVolumeKRW     float64 `json:"current_bithumb_volume_krw"`
	BithumbVolumeChangeKRW      float64 `json:"bithumb_volume_change_krw"`
	PreviousCompetitorVolumeKRW float64 `json:"previous_competitor_volume_krw"`
	CurrentCompetitorVolumeKRW  float64 `json:"current_competitor_volume_krw"`
	CompetitorVolumeChangeKRW   float64 `json:"competitor_volume_change_krw"`
	PreviousDomesticVolumeKRW   float64 `json:"previous_domestic_volume_krw"`
	CurrentDomesticVolumeKRW    float64 `json:"current_domestic_volume_krw"`
	DomesticVolumeChangeKRW     float64 `json:"domestic_volume_change_krw"`
	Reason                      string  `json:"reason"`
}

type RollingMovement struct {
	MovementID    *string  `json:"movement_id"`
	Direction     string   `json:"direction"`
	State         string   `json:"state"`
	CurrentStatus string   `json:"current_status"`
	StartedAtMS   *int64   `json:"started_at_ms"`
	LastUpdatedMS int64    `json:"last_updated_at_ms"`
	DurationSec   int64    `json:"duration_seconds"`
	ShareAtStart  *float64 `json:"share_at_start_pct"`
	CurrentShare  *float64 `json:"current_share_pct"`
	ExtremeShare  *float64 `json:"extreme_share_pct"`
	TotalChangePP *float64 `json:"total_change_pp"`
}

type RollingTrend struct {
	AsOfMS               int64    `json:"as_of_ms"`
	Rolling24hSharePct   *float64 `json:"rolling_24h_share_pct"`
	RecentFlowSharePct   *float64 `json:"recent_flow_share_pct"`
	ExpiringFlowSharePct *float64 `json:"expiring_flow_share_pct"`
	IncomingEffectPP     *float64 `json:"incoming_effect_pp"`
	ExpiringEffectPP     *float64 `json:"expiring_effect_pp"`
	QualityStatus        string   `json:"quality_status"`
}

type rollingEntry struct {
	horizon                                   RollingHorizon
	previous, incoming, expiring              []Row
	previousTotals, baseTotals, currentTotals RollingTotals
}

func BuildRollingMonitor(input RollingInput) RollingMonitor {
	currentTotals := rollingTotals(input.Current)
	currentShare := percent(currentTotals.Bithumb, currentTotals.Domestic)
	monitor := RollingMonitor{
		Version: "ROLLING_SHARE_MONITOR_V1", MarketScope: "DOMESTIC_KRW_SPOT_ONLY",
		AsOfMS: input.AsOf * 1000, WindowSec: RollingWindowSec,
		Rolling24h: Rolling24h{CurrentSharePct: currentShare, Status: input.CurrentCoverage.Status, Coverage: input.CurrentCoverage},
		Horizons:   make([]RollingHorizon, 0, len(flowHorizons)), Bridges: make([]RollingBridge, 0, len(flowHorizons)),
		DataQuality: map[string]any{"status": input.CurrentCoverage.Status, "current_coverage_sec": input.CurrentCoverage.CoverageSec, "current_required_sec": RollingWindowSec, "retained_history_required_sec": RollingWindowSec + int64(3600), "zero_filled": false, "calculation_source": "WEBSOCKET_TRADE_ONLY", "official_rest_included": false},
		Movement:    RollingMovement{Direction: "STABLE", State: "STABLE", CurrentStatus: "STABLE", LastUpdatedMS: input.AsOf * 1000, CurrentShare: currentShare, ExtremeShare: currentShare},
		Trend:       []RollingTrend{},
		Briefing:    RollingBriefing{Status: "UNAVAILABLE", Text: "24시간 점유율과 변화 원인을 계산할 자료가 아직 없습니다."},
		Evidence:    emptyRollingEvidence(),
	}
	monitor.ClosedMinuteComparisons = BuildClosedMinuteComparisons(input.AsOf, input.ClosedMinutes)
	if currentShare != nil {
		monitor.Rolling24h.BithumbVolumeKRW = pointer(currentTotals.Bithumb)
		monitor.Rolling24h.DomesticVolumeKRW = pointer(currentTotals.Domestic)
	} else {
		monitor.Rolling24h.Status = "UNAVAILABLE"
	}
	entries := make(map[int64]rollingEntry)
	for _, duration := range flowHorizons {
		interval := input.Horizons[duration]
		previousRows := shiftedRows(input.Current, interval.Incoming, interval.Expiring)
		previousTotals := rollingTotals(previousRows)
		incomingTotals, expiringTotals := rollingTotals(interval.Incoming), rollingTotals(interval.Expiring)
		previousShare := percent(previousTotals.Bithumb, previousTotals.Domestic)
		recentShare := percent(incomingTotals.Bithumb, incomingTotals.Domestic)
		expiringShare := percent(expiringTotals.Bithumb, expiringTotals.Domestic)
		status := rollingCoverageStatus(input.CurrentCoverage, interval.PreviousCoverage, interval.IncomingCoverage, interval.ExpiringCoverage)
		comparable := status == "CONFIRMED" && currentShare != nil && previousShare != nil && currentTotals.Domestic > 0 && previousTotals.Domestic > 0
		base := baseRollingTotals(previousTotals, expiringTotals)
		horizon := RollingHorizon{
			Window: flowNames[duration], DurationSec: duration, Status: status,
			Rolling24hSharePct: currentShare, RecentFlowSharePct: recentShare, ExpiringFlowSharePct: expiringShare,
			RecentFlowVolumeKRW: incomingTotals.Domestic, RecentBithumbVolumeKRW: incomingTotals.Bithumb, RecentCompetitorVolumeKRW: incomingTotals.Competitor,
			ExpiringFlowVolumeKRW: expiringTotals.Domestic, ExpiringBithumbVolumeKRW: expiringTotals.Bithumb, ExpiringCompetitorVolumeKRW: expiringTotals.Competitor,
			ReplacementGapPP: difference(recentShare, expiringShare),
			Coverage:         map[string]coverage.Result{"current": input.CurrentCoverage, "previous": interval.PreviousCoverage, "incoming": interval.IncomingCoverage, "expiring": interval.ExpiringCoverage},
		}
		if comparable {
			baseShare := percent(base.Bithumb, base.Domestic)
			if baseShare != nil {
				horizon.PreviousRolling24hSharePct = previousShare
				horizon.Rolling24hDeltaPP = pointer(*currentShare - *previousShare)
				horizon.ExpiringEffectPP = pointer(*baseShare - *previousShare)
				horizon.IncomingEffectPP = pointer(*currentShare - *baseShare)
				horizon.TotalEffectPP = pointer(*horizon.IncomingEffectPP + *horizon.ExpiringEffectPP)
				horizon.ReconciliationErrorPP = pointer(*horizon.Rolling24hDeltaPP - *horizon.TotalEffectPP)
			}
		} else if status != "PARTIAL" {
			horizon.Status = "UNAVAILABLE"
		}
		monitor.Horizons = append(monitor.Horizons, horizon)
		entries[duration] = rollingEntry{horizon: horizon, previous: previousRows, incoming: interval.Incoming, expiring: interval.Expiring, previousTotals: previousTotals, baseTotals: base, currentTotals: currentTotals}
	}
	for _, duration := range flowHorizons {
		entry := entries[duration]
		bridge := buildRollingBridge(entry)
		monitor.Bridges = append(monitor.Bridges, bridge)
		if duration == 600 {
			monitor.Bridge = bridge
		}
	}
	return monitor
}

func emptyRollingEvidence() RollingEvidence {
	counts := make(map[string]int, len(domain.DomesticExchanges))
	available := make(map[string]int, len(domain.DomesticExchanges))
	sources := make(map[string]RollingNoticeSource, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		name := string(exchange)
		counts[name], available[name] = 0, 0
		sources[name] = RollingNoticeSource{Status: "PENDING", HistoryStatus: "PENDING"}
	}
	return RollingEvidence{DomesticNotices: []RollingNoticeEvidence{}, DomesticNoticeCounts: counts, DomesticNoticeAvailableCounts: available, DomesticNoticeSources: sources}
}

func rollingTotals(rows []Row) RollingTotals {
	result := RollingTotals{}
	for _, row := range rows {
		if row.Scope != domain.ScopeDomestic {
			continue
		}
		volume := max(0.0, finiteOrZero(row.VolumeKRW))
		result.Domestic += volume
		if row.Exchange == domain.ExchangeBithumb {
			result.Bithumb += volume
		} else {
			result.Competitor += volume
		}
	}
	return result
}

func BuildClosedMinuteComparisons(asOf int64, intervals map[int64]ClosedMinuteInterval) ClosedMinuteComparisons {
	result := ClosedMinuteComparisons{
		Source:      "WEBSOCKET_TRADE_CLOSED_MINUTE_GROUPS",
		BarSec:      60,
		Comparisons: []ClosedMinuteComparison{},
	}
	closedEnd := asOf/60*60 - 1
	if closedEnd <= 0 {
		return result
	}
	closedAtMS := closedEnd * 1000
	result.AsOfMS = &closedAtMS
	for _, duration := range []int64{600, 1800, 3600} {
		interval := intervals[duration]
		previousTotals := rollingTotals(interval.Previous)
		currentTotals := rollingTotals(interval.Current)
		status := rollingCoverageStatus(interval.PreviousCoverage, interval.CurrentCoverage)
		previousShare := percent(previousTotals.Bithumb, previousTotals.Domestic)
		currentShare := percent(currentTotals.Bithumb, currentTotals.Domestic)
		comparable := status == "CONFIRMED" && previousShare != nil && currentShare != nil
		row := ClosedMinuteComparison{
			Window: flowNames[duration], DurationSec: duration, Status: status,
			ClosedAtMS:                closedAtMS,
			PreviousBithumbVolumeKRW:  previousTotals.Bithumb,
			PreviousDomesticVolumeKRW: previousTotals.Domestic,
			CurrentBithumbVolumeKRW:   currentTotals.Bithumb,
			CurrentDomesticVolumeKRW:  currentTotals.Domestic,
			Coverage: map[string]coverage.Result{
				"previous": interval.PreviousCoverage,
				"current":  interval.CurrentCoverage,
			},
			AssetChanges: []ClosedMinuteAssetChange{},
		}
		if comparable {
			row.PreviousSharePct = previousShare
			row.CurrentSharePct = currentShare
			row.ShareDeltaPP = pointer(*currentShare - *previousShare)
			row.AssetChanges = closedMinuteAssetChanges(interval.Previous, interval.Current)
		} else if status != "PARTIAL" {
			row.Status = "UNAVAILABLE"
		}
		result.Comparisons = append(result.Comparisons, row)
	}
	return result
}

func closedMinuteAssetChanges(previousRows, currentRows []Row) []ClosedMinuteAssetChange {
	previous := closedMinuteGroups(previousRows)
	current := closedMinuteGroups(currentRows)
	keys := rollingGroupKeys(previous, current)
	result := make([]ClosedMinuteAssetChange, 0, len(keys))
	for _, base := range keys {
		old, latest := previous[base], current[base]
		oldDomestic := old.bithumb + old.competitor
		latestDomestic := latest.bithumb + latest.competitor
		if oldDomestic <= 0 && latestDomestic <= 0 {
			continue
		}
		bithumbChange := latest.bithumb - old.bithumb
		competitorChange := latest.competitor - old.competitor
		reason := "빗썸 거래금액 변화 없이 경쟁 거래금액이 변한 구간"
		switch {
		case bithumbChange > 0 && competitorChange <= 0:
			reason = "빗썸 거래금액이 늘고 경쟁 거래금액은 줄어든 구간"
		case bithumbChange < 0 && competitorChange >= 0:
			reason = "빗썸 거래금액이 줄고 경쟁 거래금액은 늘어난 구간"
		case bithumbChange > 0:
			reason = "빗썸·경쟁 거래금액이 함께 늘어난 구간"
		case bithumbChange < 0:
			reason = "빗썸·경쟁 거래금액이 함께 줄거나 빗썸이 더 줄어든 구간"
		}
		result = append(result, ClosedMinuteAssetChange{
			Base:                     base,
			PreviousBithumbVolumeKRW: old.bithumb, CurrentBithumbVolumeKRW: latest.bithumb,
			BithumbVolumeChangeKRW:      bithumbChange,
			PreviousCompetitorVolumeKRW: old.competitor, CurrentCompetitorVolumeKRW: latest.competitor,
			CompetitorVolumeChangeKRW: competitorChange,
			PreviousDomesticVolumeKRW: oldDomestic, CurrentDomesticVolumeKRW: latestDomestic,
			DomesticVolumeChangeKRW: latestDomestic - oldDomestic, Reason: reason,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if math.Abs(left.BithumbVolumeChangeKRW) != math.Abs(right.BithumbVolumeChangeKRW) {
			return math.Abs(left.BithumbVolumeChangeKRW) > math.Abs(right.BithumbVolumeChangeKRW)
		}
		if math.Abs(left.CompetitorVolumeChangeKRW) != math.Abs(right.CompetitorVolumeChangeKRW) {
			return math.Abs(left.CompetitorVolumeChangeKRW) > math.Abs(right.CompetitorVolumeChangeKRW)
		}
		return math.Abs(left.DomesticVolumeChangeKRW) > math.Abs(right.DomesticVolumeChangeKRW)
	})
	return result
}

func closedMinuteGroups(rows []Row) map[string]groupedRolling {
	result := make(map[string]groupedRolling)
	for _, row := range rows {
		if row.Scope != domain.ScopeDomestic || row.Base == "" {
			continue
		}
		group := result[row.Base]
		volume := max(0, finiteOrZero(row.VolumeKRW))
		if row.Exchange == domain.ExchangeBithumb {
			group.bithumb += volume
		} else {
			group.competitor += volume
		}
		result[row.Base] = group
	}
	return result
}

type rollingKey struct {
	scope       domain.Scope
	exchange    domain.Exchange
	base, quote string
}

func shiftedRows(current, incoming, expiring []Row) []Row {
	volumes := make(map[rollingKey]float64)
	add := func(rows []Row, sign float64) {
		for _, row := range rows {
			key := rollingKey{row.Scope, row.Exchange, row.Base, row.Quote}
			volumes[key] += sign * max(0, finiteOrZero(row.VolumeKRW))
		}
	}
	add(current, 1)
	add(incoming, -1)
	add(expiring, 1)
	result := make([]Row, 0, len(volumes))
	for key, volume := range volumes {
		if volume > 1e-9 {
			result = append(result, Row{Scope: key.scope, Exchange: key.exchange, Base: key.base, Quote: key.quote, VolumeKRW: volume})
		}
	}
	return result
}

func rollingCoverageStatus(values ...coverage.Result) string {
	hasCoverage := false
	for _, value := range values {
		if value.Status != "CONFIRMED" {
			if value.CoverageSec > 0 {
				hasCoverage = true
			}
			continue
		}
		hasCoverage = true
	}
	for _, value := range values {
		if value.Status != "CONFIRMED" {
			if hasCoverage {
				return "PARTIAL"
			}
			return "UNAVAILABLE"
		}
	}
	return "CONFIRMED"
}

func baseRollingTotals(previous, expiring RollingTotals) RollingTotals {
	bithumb := max(0, previous.Bithumb-expiring.Bithumb)
	competitor := max(0, previous.Competitor-expiring.Competitor)
	return RollingTotals{Bithumb: bithumb, Competitor: competitor, Domestic: bithumb + competitor}
}

func buildRollingBridge(entry rollingEntry) RollingBridge {
	horizon := entry.horizon
	bridge := RollingBridge{
		Window: horizon.Window, DurationSec: horizon.DurationSec, Status: horizon.Status,
		PreviousSharePct: horizon.PreviousRolling24hSharePct, CurrentSharePct: horizon.Rolling24hSharePct,
		IncomingEffectPP: horizon.IncomingEffectPP, ExpiringEffectPP: horizon.ExpiringEffectPP,
		TotalEffectPP: horizon.TotalEffectPP, ReconciliationErrorPP: horizon.ReconciliationErrorPP,
		AssetDrivers: []RollingDriver{}, ExchangeDrivers: []RollingDriver{},
		ObservedAssetDrivers:    observedDrivers(entry.expiring, entry.incoming, "base"),
		ObservedExchangeDrivers: observedDrivers(entry.expiring, entry.incoming, "exchange"),
	}
	if horizon.IncomingEffectPP != nil && horizon.ExpiringEffectPP != nil {
		bridge.AssetDrivers = exactDrivers(entry, "base")
		bridge.ExchangeDrivers = exactDrivers(entry, "exchange")
	}
	return bridge
}

type groupedRolling struct{ bithumb, competitor float64 }

func groupRolling(rows []Row, field string) map[string]groupedRolling {
	result := make(map[string]groupedRolling)
	for _, row := range rows {
		if row.Scope != domain.ScopeDomestic {
			continue
		}
		key := row.Base
		if field == "exchange" {
			key = string(row.Exchange)
		}
		if key == "" {
			continue
		}
		value := result[key]
		if row.Exchange == domain.ExchangeBithumb {
			value.bithumb += row.VolumeKRW
		} else {
			value.competitor += row.VolumeKRW
		}
		result[key] = value
	}
	return result
}

func exactDrivers(entry rollingEntry, field string) []RollingDriver {
	previous, expired, incoming := groupRolling(entry.previous, field), groupRolling(entry.expiring, field), groupRolling(entry.incoming, field)
	keys := rollingGroupKeys(previous, expired, incoming)
	expiringB, expiringC := pathCoefficients(entry.previousTotals.Bithumb, entry.previousTotals.Competitor, entry.baseTotals.Bithumb, entry.baseTotals.Competitor)
	incomingB, incomingC := pathCoefficients(entry.baseTotals.Bithumb, entry.baseTotals.Competitor, entry.currentTotals.Bithumb, entry.currentTotals.Competitor)
	result := make([]RollingDriver, 0, len(keys))
	for _, key := range keys {
		old, out, in := previous[key], expired[key], incoming[key]
		incomingEffect := (in.bithumb*incomingB + in.competitor*incomingC) * 100
		expiringEffect := (-out.bithumb*expiringB - out.competitor*expiringC) * 100
		row := RollingDriver{IncomingEffectPP: incomingEffect, ExpiringEffectPP: expiringEffect, ContributionPP: pointer(incomingEffect + expiringEffect), RecentBithumbVolumeKRW: in.bithumb, RecentCompetitorVolumeKRW: in.competitor, ExpiringBithumbVolumeKRW: out.bithumb, ExpiringCompetitorVolumeKRW: out.competitor, PreviousVolumeKRW: old.bithumb + old.competitor, RecentVolumeKRW: in.bithumb + in.competitor, RecentVsExpiringChangePct: changePercent(in.bithumb+in.competitor, out.bithumb+out.competitor), Reason: rollingDriverReason(incomingEffect, expiringEffect, field, in.competitor, out.competitor)}
		if field == "base" {
			row.Base = key
			identity := Identity(key)
			row.AssetIdentity = &identity
		} else {
			row.Exchange = key
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		return math.Abs(pointerValue(result[i].ContributionPP)) > math.Abs(pointerValue(result[j].ContributionPP))
	})
	return result
}

func observedDrivers(expiring, incoming []Row, field string) []ObservedFlowDriver {
	expired, recent := groupRolling(expiring, field), groupRolling(incoming, field)
	keys := rollingGroupKeys(expired, recent)
	result := make([]ObservedFlowDriver, 0, len(keys))
	for _, key := range keys {
		out, outObserved := expired[key]
		in, inObserved := recent[key]
		outTotal, inTotal := out.bithumb+out.competitor, in.bithumb+in.competitor
		if outTotal <= 0 && inTotal <= 0 {
			continue
		}
		inShare, outShare := percent(in.bithumb, inTotal), percent(out.bithumb, outTotal)
		gap := difference(inShare, outShare)
		reason := "관측된 한쪽 흐름만 있음"
		if gap != nil {
			if *gap > 0 {
				reason = "신규 거래에서 빗썸 몫이 더 높음"
			} else if *gap < 0 {
				reason = "만료 거래에서 빗썸 몫이 더 높음"
			} else {
				reason = "신규·만료 거래의 빗썸 몫이 같음"
			}
		}
		row := ObservedFlowDriver{RecentFlowSharePct: inShare, ExpiringFlowSharePct: outShare, ReplacementGapPP: gap, RecentVsExpiringChangePct: changePercent(inTotal, outTotal), Reason: reason}
		if inObserved {
			row.RecentBithumbVolumeKRW = pointer(in.bithumb)
			row.RecentCompetitorVolumeKRW = pointer(in.competitor)
			row.RecentVolumeKRW = pointer(inTotal)
		}
		if outObserved {
			row.ExpiringBithumbVolumeKRW = pointer(out.bithumb)
			row.ExpiringCompetitorVolumeKRW = pointer(out.competitor)
			row.ExpiringVolumeKRW = pointer(outTotal)
		}
		if field == "base" {
			row.Base = key
			identity := Identity(key)
			row.AssetIdentity = &identity
		} else {
			row.Exchange = key
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		leftGap := math.Abs(pointerValue(result[i].ReplacementGapPP))
		rightGap := math.Abs(pointerValue(result[j].ReplacementGapPP))
		if leftGap != rightGap {
			return leftGap > rightGap
		}
		leftVolume := pointerValue(result[i].RecentVolumeKRW) + pointerValue(result[i].ExpiringVolumeKRW)
		rightVolume := pointerValue(result[j].RecentVolumeKRW) + pointerValue(result[j].ExpiringVolumeKRW)
		if leftVolume != rightVolume {
			return leftVolume > rightVolume
		}
		return observedFlowDriverKey(result[i]) < observedFlowDriverKey(result[j])
	})
	return result
}

func observedFlowDriverKey(driver ObservedFlowDriver) string {
	if driver.Base != "" {
		return driver.Base
	}
	return driver.Exchange
}

func rollingGroupKeys(groups ...map[string]groupedRolling) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for key := range group {
			seen[key] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func rollingDriverReason(incoming, expiring float64, field string, incomingCompetitor, expiringCompetitor float64) string {
	if field == "exchange" {
		if incomingCompetitor > expiringCompetitor {
			return "최근 경쟁 거래금액 증가 기여"
		}
		if incomingCompetitor < expiringCompetitor {
			return "최근 경쟁 거래금액 감소 기여"
		}
		return "최근 경쟁 거래금액 흐름"
	}
	if incoming < 0 && math.Abs(incoming) >= math.Abs(expiring) {
		return "최근 거래금액 감소 기여"
	}
	if expiring < 0 {
		return "24시간 전 강한 구간 만료 기여"
	}
	if incoming < 0 {
		return "경쟁 거래금액 증가 기여"
	}
	if expiring > 0 {
		return "24시간 전 약한 구간 만료 기여"
	}
	return "최근 거래금액 흐름"
}
