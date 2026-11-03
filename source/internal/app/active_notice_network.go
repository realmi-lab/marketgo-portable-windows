package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
)

// The dashboard polls this network every ten seconds. A shorter entry expired
// before each viewer's next request, so every poll recomputed the two interval
// reads and the cache only ever collapsed concurrent viewers. Outliving the
// poll keeps a steady viewer on cached evidence while staying well inside the
// ten-second realtime cadence.
const activeNoticeNetworkCacheTTL = 12 * time.Second

var activeNoticeNetworkWindows = [...]int64{
	60, 300, 600, 900, 1_800, 3_600, 7_200, 10_800, 21_600, 43_200, 86_400,
}

type ActiveNoticeNetwork struct {
	Version          string                      `json:"version"`
	AsOfMS           int64                       `json:"as_of_ms"`
	WindowSec        int64                       `json:"window_sec"`
	Status           string                      `json:"status"`
	Reason           string                      `json:"reason"`
	PreviousSharePct *float64                    `json:"previous_bithumb_share_pct"`
	CurrentSharePct  *float64                    `json:"current_bithumb_share_pct"`
	ShareDeltaPP     *float64                    `json:"share_delta_pp"`
	Notices          []ActiveNoticeNetworkNotice `json:"active_notices"`
	Assets           []ActiveNoticeNetworkAsset  `json:"assets"`
	Edges            []ActiveNoticeNetworkEdge   `json:"edges"`
	CausalClaim      bool                        `json:"causal_claim"`
}

type ActiveNoticeNetworkNotice struct {
	ID       string   `json:"id"`
	Exchange string   `json:"exchange"`
	Title    string   `json:"title"`
	URL      string   `json:"url"`
	Category string   `json:"category"`
	Assets   []string `json:"assets"`
	StartMS  int64    `json:"start_ms"`
	EndMS    *int64   `json:"end_ms"`
}

type ActiveNoticeNetworkAsset struct {
	Asset                      string   `json:"asset"`
	ContributionPP             *float64 `json:"contribution_pp"`
	CurrentDomesticVolumeKRW   float64  `json:"current_domestic_volume_krw"`
	CurrentBithumbVolumeKRW    float64  `json:"current_bithumb_volume_krw"`
	CurrentBithumbSharePct     *float64 `json:"current_bithumb_share_pct"`
	CurrentCompetitorVolumeKRW float64  `json:"current_competitor_volume_krw"`
	BithumbVolumeChangeKRW     *float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW  *float64 `json:"competitor_volume_change_krw"`
}

type ActiveNoticeNetworkEdge struct {
	Asset                   string   `json:"asset"`
	Exchange                string   `json:"exchange"`
	CurrentVolumeKRW        float64  `json:"current_volume_krw"`
	CurrentDomesticSharePct *float64 `json:"current_domestic_share_pct"`
}

type activeNoticeNetworkCacheEntry struct {
	created time.Time
	value   ActiveNoticeNetwork
}

type activeNoticeNetworkCall struct {
	done  chan struct{}
	value ActiveNoticeNetwork
	err   error
}

type activeNoticeNetworkMemo struct {
	mu       sync.Mutex
	cache    map[int64]activeNoticeNetworkCacheEntry
	inflight map[int64]*activeNoticeNetworkCall
}

func newActiveNoticeNetworkMemo() *activeNoticeNetworkMemo {
	return &activeNoticeNetworkMemo{
		cache: make(map[int64]activeNoticeNetworkCacheEntry), inflight: make(map[int64]*activeNoticeNetworkCall),
	}
}

func (m *activeNoticeNetworkMemo) get(ctx context.Context, windowSec int64, calculate func(context.Context) (ActiveNoticeNetwork, error)) (ActiveNoticeNetwork, error) {
	now := time.Now()
	m.mu.Lock()
	if cached, ok := m.cache[windowSec]; ok && now.Sub(cached.created) < activeNoticeNetworkCacheTTL {
		m.mu.Unlock()
		return cached.value, nil
	}
	if call := m.inflight[windowSec]; call != nil {
		m.mu.Unlock()
		return waitActiveNoticeNetwork(ctx, call)
	}
	call := &activeNoticeNetworkCall{done: make(chan struct{})}
	m.inflight[windowSec] = call
	m.mu.Unlock()
	calculationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	go func() {
		defer cancel()
		call.value, call.err = calculate(calculationCtx)
		m.mu.Lock()
		delete(m.inflight, windowSec)
		if call.err == nil {
			m.cache[windowSec] = activeNoticeNetworkCacheEntry{created: time.Now(), value: call.value}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return waitActiveNoticeNetwork(ctx, call)
}

func waitActiveNoticeNetwork(ctx context.Context, call *activeNoticeNetworkCall) (ActiveNoticeNetwork, error) {
	select {
	case <-ctx.Done():
		return ActiveNoticeNetwork{}, ctx.Err()
	case <-call.done:
		return call.value, call.err
	}
}

func (s *Service) ActiveNoticeNetwork(ctx context.Context, windowSec int64) (ActiveNoticeNetwork, error) {
	if !supportedActiveNoticeNetworkWindow(windowSec) {
		return ActiveNoticeNetwork{}, ErrUnsupportedWindow
	}
	s.activeNoticeNetworkMemoOnce.Do(func() { s.activeNoticeNetworkMemo = newActiveNoticeNetworkMemo() })
	return s.activeNoticeNetworkMemo.get(ctx, windowSec, func(calculationCtx context.Context) (ActiveNoticeNetwork, error) {
		return s.calculateActiveNoticeNetwork(calculationCtx, windowSec)
	})
}

func (s *Service) calculateActiveNoticeNetwork(ctx context.Context, windowSec int64) (ActiveNoticeNetwork, error) {
	if s.runtime == nil {
		return ActiveNoticeNetwork{}, ErrStopped
	}
	query := marketanalysis.MarketQuestionQuery{
		TimeValue:   windowSec / 60,
		TimeUnit:    marketanalysis.MarketQuestionTimeMinute,
		Comparison:  marketanalysis.MarketQuestionCurrentVsPreviousInterval,
		MarketScope: "KRW",
	}
	plan, err := newMarketQuestionIntervalPlan(query, time.Now().Unix()-1)
	if err != nil {
		return ActiveNoticeNetwork{}, err
	}
	read, err := s.runtime.atomicRead(ctx, []aggregate.IntervalRequest{
		{Start: domain.UnixSecond(plan.previousStart - 1), End: domain.UnixSecond(plan.previousEnd)},
		{Start: domain.UnixSecond(plan.currentStart - 1), End: domain.UnixSecond(plan.currentEnd)},
	}, []coverageInterval{
		{start: plan.previousStart, end: plan.previousEnd},
		{start: plan.currentStart, end: plan.currentEnd},
	})
	if err != nil {
		return ActiveNoticeNetwork{}, err
	}
	if len(read.intervals) != 2 || len(read.coverage) != 2 ||
		!read.intervals[0].OK || !read.intervals[1].OK {
		return ActiveNoticeNetwork{}, errors.New("invalid atomic network interval read")
	}
	previous := analysisRows(filterAnalysisValues(read.intervals[0].Values, "KRW"))
	current := analysisRows(filterAnalysisValues(read.intervals[1].Values, "KRW"))
	complete := read.coverage[0].WindowComplete && read.coverage[1].WindowComplete &&
		read.coverage[0].CoverageSec >= windowSec && read.coverage[1].CoverageSec >= windowSec
	attribution := marketanalysis.ChangeAttribution(current, previous, complete)
	previousStart, previousEnd := float64(plan.previousStart), float64(plan.previousEnd+1)
	analysis := marketanalysis.Analysis{
		WindowSec:   windowSec,
		GeneratedAt: float64(plan.currentEnd + 1),
		Context: marketanalysis.Context{
			WindowSec: windowSec, AggregationVersion: read.version,
			CurrentIntervalStart:    float64(plan.currentStart),
			CurrentIntervalEnd:      float64(plan.currentEnd + 1),
			ComparisonIntervalStart: &previousStart,
			ComparisonIntervalEnd:   &previousEnd,
		},
		ChangeAttribution: attribution,
	}
	return rollingActiveNoticeNetwork(windowSec, analysis), nil
}

func supportedActiveNoticeNetworkWindow(windowSec int64) bool {
	for _, supported := range activeNoticeNetworkWindows {
		if windowSec == supported {
			return true
		}
	}
	return false
}

func rollingActiveNoticeNetwork(windowSec int64, analysis marketanalysis.Analysis) ActiveNoticeNetwork {
	attribution := analysis.ChangeAttribution
	network := attribution.AssetExchangeNetwork
	asOfMS := int64(analysis.Context.CurrentIntervalEnd * 1_000)
	if asOfMS <= 0 {
		asOfMS = int64(analysis.GeneratedAt * 1_000)
	}
	result := ActiveNoticeNetwork{
		Version: "ACTIVE_NOTICE_SHARE_NETWORK_V1", AsOfMS: asOfMS, WindowSec: windowSec,
		Status: network.Status, Reason: network.UnavailableReason,
		PreviousSharePct: attribution.PreviousSharePercent,
		CurrentSharePct:  attribution.CurrentSharePercent,
		ShareDeltaPP:     attribution.ShareChangePP,
		Notices:          []ActiveNoticeNetworkNotice{},
		Assets:           []ActiveNoticeNetworkAsset{},
		Edges:            []ActiveNoticeNetworkEdge{},
		CausalClaim:      false,
	}
	selectedAssets := make(map[string]struct{}, network.ReturnedAssetCount)
	for _, node := range network.Nodes {
		if node.Kind == "ASSET" {
			selectedAssets[strings.ToUpper(node.Label)] = struct{}{}
		}
	}
	assetDomesticVolumes := make(map[string]float64, len(selectedAssets))
	for _, asset := range attribution.Assets {
		name := strings.ToUpper(asset.Asset)
		if _, selected := selectedAssets[name]; !selected {
			continue
		}
		contribution := asset.ContributionPP
		bithumbChange := asset.BithumbVolumeChangeKRW
		competitorChange := asset.CompetitorVolumeChangeKRW
		result.Assets = append(result.Assets, ActiveNoticeNetworkAsset{
			Asset: name, ContributionPP: &contribution,
			CurrentDomesticVolumeKRW:   asset.CurrentDomesticVolumeKRW,
			CurrentBithumbVolumeKRW:    asset.CurrentBithumbVolumeKRW,
			CurrentBithumbSharePct:     ratioPercent(asset.CurrentBithumbVolumeKRW, asset.CurrentDomesticVolumeKRW),
			CurrentCompetitorVolumeKRW: asset.CurrentCompetitorVolumeKRW,
			BithumbVolumeChangeKRW:     &bithumbChange, CompetitorVolumeChangeKRW: &competitorChange,
		})
		assetDomesticVolumes[name] = asset.CurrentDomesticVolumeKRW
	}
	for _, edge := range network.Edges {
		asset := strings.ToUpper(edge.Asset)
		result.Edges = append(result.Edges, ActiveNoticeNetworkEdge{
			Asset: asset, Exchange: string(edge.Exchange),
			CurrentVolumeKRW:        edge.CurrentVolumeKRW,
			CurrentDomesticSharePct: ratioPercent(edge.CurrentVolumeKRW, assetDomesticVolumes[asset]),
		})
	}
	return result
}
