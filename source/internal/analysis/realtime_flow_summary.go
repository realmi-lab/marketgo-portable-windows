package analysis

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

const RealtimeFlowSummarySampleSec int64 = 10

type RealtimeFlowPeriodDriver struct {
	Rank                      int     `json:"rank"`
	Asset                     string  `json:"asset"`
	CumulativeContributionPP  float64 `json:"cumulative_contribution_pp"`
	AbsoluteContributionPP    float64 `json:"absolute_contribution_pp"`
	PeakContributionPP        float64 `json:"peak_contribution_pp"`
	ObservationCount          int     `json:"observation_count"`
	PositiveObservationCount  int     `json:"positive_observation_count"`
	NegativeObservationCount  int     `json:"negative_observation_count"`
	DirectionConsistencyPct   float64 `json:"direction_consistency_pct"`
	BithumbVolumeChangeKRW    float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW float64 `json:"competitor_volume_change_krw"`
	FirstObservedAtMS         int64   `json:"first_observed_at_ms"`
	LastObservedAtMS          int64   `json:"last_observed_at_ms"`
	EpisodeCount              int     `json:"episode_count"`
}

// RealtimeFlowDriverBreadth measures concentration from each driver's signed
// period net contribution. Absolute net values are used as weights so opposing
// observations for one asset do not masquerade as independent drivers.
type RealtimeFlowDriverBreadth struct {
	Basis                            string  `json:"basis"`
	PositiveDriverCount              int     `json:"positive_driver_count"`
	NegativeDriverCount              int     `json:"negative_driver_count"`
	EffectiveDriverCount             float64 `json:"effective_driver_count"`
	Top1AbsoluteContributionSharePct float64 `json:"top1_absolute_contribution_share_pct"`
	Top3AbsoluteContributionSharePct float64 `json:"top3_absolute_contribution_share_pct"`
	Top5AbsoluteContributionSharePct float64 `json:"top5_absolute_contribution_share_pct"`
}

// Coverage segments use half-open [start_ms, end_ms) ranges and together cover
// the complete requested period without substituting data into missing time.
type RealtimeFlowCoverageSegment struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type RealtimeFlowEpisodeDriver struct {
	Rank                      int     `json:"rank"`
	Asset                     string  `json:"asset"`
	NetContributionPP         float64 `json:"net_contribution_pp"`
	GrossContributionPP       float64 `json:"gross_contribution_pp"`
	PeakContributionPP        float64 `json:"peak_contribution_pp"`
	ObservationCount          int     `json:"observation_count"`
	BithumbVolumeChangeKRW    float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW float64 `json:"competitor_volume_change_krw"`
}

type RealtimeFlowEpisode struct {
	ID                        string                      `json:"id"`
	StartMS                   int64                       `json:"start_ms"`
	EndMS                     int64                       `json:"end_ms"`
	Direction                 string                      `json:"direction"`
	Status                    string                      `json:"status"`
	ObservationCount          int                         `json:"observation_count"`
	DurationSec               int64                       `json:"duration_sec"`
	NetShareChangePP          float64                     `json:"net_share_change_pp"`
	GrossShareChangePP        float64                     `json:"gross_share_change_pp"`
	PeakShareChangePP         float64                     `json:"peak_share_change_pp"`
	AttributedSampleCount     int                         `json:"attributed_sample_count"`
	TopAsset                  string                      `json:"top_asset"`
	TopAssetContributionPP    float64                     `json:"top_asset_contribution_pp"`
	TopExchange               string                      `json:"top_exchange"`
	TopExchangeContributionPP float64                     `json:"top_exchange_contribution_pp"`
	Drivers                   []RealtimeFlowEpisodeDriver `json:"drivers"`
}

type RealtimeFlowPeriodSummary struct {
	ContractVersion        string                        `json:"contract_version"`
	FromMS                 int64                         `json:"from_ms"`
	ToMS                   int64                         `json:"to_ms"`
	WindowSec              int64                         `json:"window_sec"`
	SampleSec              int64                         `json:"sample_sec"`
	Status                 string                        `json:"status"`
	ExpectedSampleCount    int                           `json:"expected_sample_count"`
	ObservedSampleCount    int                           `json:"observed_sample_count"`
	MissingSampleCount     int                           `json:"missing_sample_count"`
	ObservedSec            int64                         `json:"observed_sec"`
	MissingSec             int64                         `json:"missing_sec"`
	CoveragePct            float64                       `json:"coverage_pct"`
	CoverageSegments       []RealtimeFlowCoverageSegment `json:"coverage_segments"`
	ObservedShareChangePP  float64                       `json:"observed_share_change_pp"`
	SignalSampleCount      int                           `json:"signal_sample_count"`
	WatchSampleCount       int                           `json:"watch_sample_count"`
	AlertSampleCount       int                           `json:"alert_sample_count"`
	AttributedSampleCount  int                           `json:"attributed_sample_count"`
	AttributedSec          int64                         `json:"attributed_sec"`
	AttributionMissingSec  int64                         `json:"attribution_missing_sec"`
	AttributionCoveragePct float64                       `json:"attribution_coverage_pct"`
	ResidualContributionPP float64                       `json:"residual_contribution_pp"`
	DriverBreadth          RealtimeFlowDriverBreadth     `json:"driver_breadth"`
	Episodes               []RealtimeFlowEpisode         `json:"episodes"`
	Drivers                []RealtimeFlowPeriodDriver    `json:"drivers"`

	// Set only when a request selected part of the episode list: episodes
	// then holds that part and episode_total counts the period's episodes.
	EpisodesFromMS *int64 `json:"episodes_from_ms,omitempty"`
	EpisodeLimit   *int   `json:"episode_limit,omitempty"`
	EpisodeTotal   *int   `json:"episode_total,omitempty"`
}

// SelectRealtimeFlowEpisodes keeps the episodes that end at or after fromMS
// and, when limit is positive, only the limit latest of them, in their
// original order. Every other field is kept. Screens show the last minutes
// or the latest few episodes, but the whole day was about 5,000 episodes and
// 7 MB of JSON per request. The source is shared and is not modified.
func SelectRealtimeFlowEpisodes(source RealtimeFlowPeriodSummary, fromMS int64, limit int) RealtimeFlowPeriodSummary {
	result := source
	total := len(source.Episodes)
	result.EpisodeTotal = &total
	if fromMS > 0 {
		result.EpisodesFromMS = &fromMS
	}
	if limit > 0 {
		result.EpisodeLimit = &limit
	}
	kept := make([]int, 0, len(source.Episodes))
	for index := range source.Episodes {
		if source.Episodes[index].EndMS >= fromMS {
			kept = append(kept, index)
		}
	}
	if limit > 0 && len(kept) > limit {
		sort.SliceStable(kept, func(i, j int) bool {
			return source.Episodes[kept[i]].EndMS > source.Episodes[kept[j]].EndMS
		})
		kept = kept[:limit]
		sort.Ints(kept)
	}
	result.Episodes = make([]RealtimeFlowEpisode, len(kept))
	for position, index := range kept {
		result.Episodes[position] = source.Episodes[index]
	}
	return result
}

type realtimeFlowPeriodDriverAccumulator struct {
	RealtimeFlowPeriodDriver
}

type realtimeFlowPeriodSample struct {
	state  RealtimeFlowState
	window *RealtimeFlowWindow
}

// SummarizeRealtimeFlowPeriod aggregates server-stored, non-overlapping
// ten-second comparisons. Unusable or missing samples remain explicit gaps;
// they are never replaced with a coarser candle and presented as ten-second
// evidence.
func SummarizeRealtimeFlowPeriod(states []RealtimeFlowState, fromMS, toMS int64) RealtimeFlowPeriodSummary {
	result := RealtimeFlowPeriodSummary{
		ContractVersion:  RealtimeFlowVersion,
		FromMS:           fromMS,
		ToMS:             toMS,
		SampleSec:        RealtimeFlowSummarySampleSec,
		Status:           "UNAVAILABLE",
		CoverageSegments: []RealtimeFlowCoverageSegment{},
		DriverBreadth: RealtimeFlowDriverBreadth{
			Basis: "ABSOLUTE_NET_CONTRIBUTION",
		},
		Episodes: []RealtimeFlowEpisode{},
		Drivers:  []RealtimeFlowPeriodDriver{},
	}
	if toMS <= fromMS {
		return result
	}
	result.WindowSec = (toMS - fromMS) / 1_000
	result.ExpectedSampleCount = int(result.WindowSec / RealtimeFlowSummarySampleSec)
	if result.ExpectedSampleCount < 1 {
		return result
	}

	ordered := append([]RealtimeFlowState(nil), states...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].AsOfMS < ordered[j].AsOfMS })
	drivers := make(map[string]*realtimeFlowPeriodDriverAccumulator)
	seen := make(map[int64]struct{}, len(ordered))
	reasons := make(map[int64]string)
	samples := make([]realtimeFlowPeriodSample, 0, len(ordered))
	sampleMS := RealtimeFlowSummarySampleSec * 1_000
	for _, state := range ordered {
		if state.AsOfMS <= fromMS || state.AsOfMS > toMS {
			continue
		}
		// Persisted states can be emitted off-boundary immediately after a
		// process restart. They remain useful diagnostics, but are not one of
		// the non-overlapping ten-second observations promised by this summary.
		if state.AsOfMS%sampleMS != 0 {
			continue
		}
		bucket := state.AsOfMS / sampleMS
		if _, exists := seen[bucket]; exists {
			continue
		}
		window := realtimeFlowCompleteTenSecondWindow(state)
		if window == nil {
			reasons[bucket] = realtimeFlowUnavailableReason(state)
			continue
		}
		seen[bucket] = struct{}{}
		samples = append(samples, realtimeFlowPeriodSample{state: state, window: window})
		result.ObservedSampleCount++
		result.ObservedShareChangePP += *window.ShareDeltaPP
		switch state.Status {
		case "WATCH":
			result.WatchSampleCount++
			result.SignalSampleCount++
		case "ALERT":
			result.AlertSampleCount++
			result.SignalSampleCount++
		}

		attribution := state.TenSecondAttribution
		if attribution == nil || attribution.WindowSec != RealtimeFlowSummarySampleSec {
			continue
		}
		result.AttributedSampleCount++
		if finiteSignedAnalysisPointer(attribution.ResidualContributionPP) {
			result.ResidualContributionPP += *attribution.ResidualContributionPP
		}
		for _, driver := range attribution.Drivers {
			if driver.Asset == "" || !validSignedAnalysisNumber(driver.ContributionPP) {
				continue
			}
			row := drivers[driver.Asset]
			if row == nil {
				row = &realtimeFlowPeriodDriverAccumulator{}
				row.Asset = driver.Asset
				row.FirstObservedAtMS = state.AsOfMS
				drivers[driver.Asset] = row
			}
			row.CumulativeContributionPP += driver.ContributionPP
			row.AbsoluteContributionPP += math.Abs(driver.ContributionPP)
			if math.Abs(driver.ContributionPP) > math.Abs(row.PeakContributionPP) {
				row.PeakContributionPP = driver.ContributionPP
			}
			row.ObservationCount++
			if driver.ContributionPP > 0 {
				row.PositiveObservationCount++
			} else if driver.ContributionPP < 0 {
				row.NegativeObservationCount++
			}
			row.BithumbVolumeChangeKRW += driver.BithumbVolumeChangeKRW
			row.CompetitorVolumeChangeKRW += driver.CompetitorVolumeChangeKRW
			row.LastObservedAtMS = state.AsOfMS
		}
	}

	result.MissingSampleCount = max(0, result.ExpectedSampleCount-result.ObservedSampleCount)
	result.ObservedSec = int64(result.ObservedSampleCount) * RealtimeFlowSummarySampleSec
	result.MissingSec = int64(result.MissingSampleCount) * RealtimeFlowSummarySampleSec
	result.CoveragePct = float64(result.ObservedSampleCount) / float64(result.ExpectedSampleCount) * 100
	result.CoverageSegments = realtimeFlowCoverageSegments(fromMS, toMS, seen, reasons)
	result.AttributedSec = int64(result.AttributedSampleCount) * RealtimeFlowSummarySampleSec
	result.AttributionMissingSec = int64(max(0, result.ExpectedSampleCount-result.AttributedSampleCount)) * RealtimeFlowSummarySampleSec
	result.AttributionCoveragePct = float64(result.AttributedSampleCount) / float64(result.ExpectedSampleCount) * 100
	if result.ObservedSampleCount > 0 {
		result.Status = "PARTIAL"
	}
	if result.ObservedSampleCount >= result.ExpectedSampleCount {
		result.Status = "COMPLETE"
	}

	result.Drivers = make([]RealtimeFlowPeriodDriver, 0, len(drivers))
	for _, accumulated := range drivers {
		row := accumulated.RealtimeFlowPeriodDriver
		directional := max(row.PositiveObservationCount, row.NegativeObservationCount)
		if row.ObservationCount > 0 {
			row.DirectionConsistencyPct = float64(directional) / float64(row.ObservationCount) * 100
		}
		result.Drivers = append(result.Drivers, row)
	}
	sortRealtimeFlowPeriodDrivers(result.Drivers)
	result.DriverBreadth = realtimeFlowDriverBreadth(result.Drivers)
	episodes, episodeCounts := summarizeRealtimeFlowEpisodes(samples)
	result.Episodes = episodes
	for index := range result.Drivers {
		result.Drivers[index].EpisodeCount = episodeCounts[result.Drivers[index].Asset]
	}
	if len(result.Drivers) > 20 {
		result.Drivers = result.Drivers[:20]
	}
	for index := range result.Drivers {
		result.Drivers[index].Rank = index + 1
	}
	return result
}

func sortRealtimeFlowPeriodDrivers(drivers []RealtimeFlowPeriodDriver) {
	sort.Slice(drivers, func(i, j int) bool {
		left, right := drivers[i], drivers[j]
		if math.Abs(left.CumulativeContributionPP) != math.Abs(right.CumulativeContributionPP) {
			return math.Abs(left.CumulativeContributionPP) > math.Abs(right.CumulativeContributionPP)
		}
		if left.AbsoluteContributionPP != right.AbsoluteContributionPP {
			return left.AbsoluteContributionPP > right.AbsoluteContributionPP
		}
		if left.ObservationCount != right.ObservationCount {
			return left.ObservationCount > right.ObservationCount
		}
		return left.Asset < right.Asset
	})
}

func realtimeFlowDriverBreadth(drivers []RealtimeFlowPeriodDriver) RealtimeFlowDriverBreadth {
	result := RealtimeFlowDriverBreadth{Basis: "ABSOLUTE_NET_CONTRIBUTION"}
	weights := make([]float64, 0, len(drivers))
	var total, squares float64
	for _, driver := range drivers {
		switch {
		case driver.CumulativeContributionPP > 0:
			result.PositiveDriverCount++
		case driver.CumulativeContributionPP < 0:
			result.NegativeDriverCount++
		}
		weight := math.Abs(driver.CumulativeContributionPP)
		if weight == 0 {
			continue
		}
		weights = append(weights, weight)
		total += weight
		squares += weight * weight
	}
	if total == 0 || squares == 0 {
		return result
	}
	sort.Slice(weights, func(i, j int) bool { return weights[i] > weights[j] })
	result.EffectiveDriverCount = total * total / squares
	result.Top1AbsoluteContributionSharePct = realtimeFlowTopWeightShare(weights, 1, total)
	result.Top3AbsoluteContributionSharePct = realtimeFlowTopWeightShare(weights, 3, total)
	result.Top5AbsoluteContributionSharePct = realtimeFlowTopWeightShare(weights, 5, total)
	return result
}

func realtimeFlowTopWeightShare(weights []float64, count int, total float64) float64 {
	var value float64
	for index := 0; index < len(weights) && index < count; index++ {
		value += weights[index]
	}
	return value / total * 100
}

// Reasons describe stored evidence, not inferred network failures. A complete
// latest frame can still lack the earlier frames needed for a comparison.
func realtimeFlowUnavailableReason(state RealtimeFlowState) string {
	if state.Version != RealtimeFlowVersion {
		return "SAMPLE_UNUSABLE"
	}
	if state.DataQuality["complete_frame"] != true || state.DataQuality["frame_status"] != "LIVE" {
		return "OBSERVATION_INCOMPLETE"
	}
	if state.Status == "WARMING" || state.Status == "DEGRADED" {
		return "COMPARISON_INCOMPLETE"
	}
	for _, window := range state.Windows {
		if window.DurationSec == RealtimeFlowSummarySampleSec && window.Status != "COMPLETE" {
			return "COMPARISON_INCOMPLETE"
		}
	}
	return "SAMPLE_UNUSABLE"
}

func realtimeFlowCoverageSegments(fromMS, toMS int64, observed map[int64]struct{}, reasons map[int64]string) []RealtimeFlowCoverageSegment {
	if toMS <= fromMS {
		return []RealtimeFlowCoverageSegment{}
	}
	const observedStatus = "OBSERVED"
	const missingStatus = "MISSING"
	sampleMS := RealtimeFlowSummarySampleSec * 1_000
	result := make([]RealtimeFlowCoverageSegment, 0)
	for start := fromMS; start < toMS; {
		bucketEnd := (start/sampleMS + 1) * sampleMS
		end := min(bucketEnd, toMS)
		status := missingStatus
		reason := reasons[bucketEnd/sampleMS]
		if reason == "" {
			reason = "NO_SAMPLE"
		}
		if _, ok := observed[bucketEnd/sampleMS]; ok {
			status = observedStatus
			reason = ""
		}
		if len(result) > 0 && result[len(result)-1].Status == status && result[len(result)-1].Reason == reason {
			result[len(result)-1].EndMS = end
		} else {
			result = append(result, RealtimeFlowCoverageSegment{StartMS: start, EndMS: end, Status: status, Reason: reason})
		}
		start = end
	}
	return result
}

type realtimeFlowEpisodeAccumulator struct {
	episode   RealtimeFlowEpisode
	lastAsOf  int64
	drivers   map[string]*RealtimeFlowEpisodeDriver
	exchanges map[string]float64
}

func summarizeRealtimeFlowEpisodes(samples []realtimeFlowPeriodSample) ([]RealtimeFlowEpisode, map[string]int) {
	result := make([]RealtimeFlowEpisode, 0)
	episodeCounts := make(map[string]int)
	var current *realtimeFlowEpisodeAccumulator
	flush := func() {
		if current == nil {
			return
		}
		episode, assets := current.finish()
		result = append(result, episode)
		for _, asset := range assets {
			episodeCounts[asset]++
		}
		current = nil
	}
	for _, sample := range samples {
		if sample.state.Status != "WATCH" && sample.state.Status != "ALERT" {
			flush()
			continue
		}
		direction := realtimeFlowEpisodeDirection(*sample.window.ShareDeltaPP)
		if direction == "" {
			flush()
			continue
		}
		if current == nil || current.episode.Direction != direction || sample.state.AsOfMS-current.lastAsOf > 20_000 {
			flush()
			current = newRealtimeFlowEpisodeAccumulator(sample.state.AsOfMS, direction)
		}
		current.add(sample)
	}
	flush()
	return result, episodeCounts
}

func newRealtimeFlowEpisodeAccumulator(asOfMS int64, direction string) *realtimeFlowEpisodeAccumulator {
	startMS := asOfMS - RealtimeFlowSummarySampleSec*1_000
	return &realtimeFlowEpisodeAccumulator{
		episode: RealtimeFlowEpisode{
			ID:        "realtime-flow-" + strconv.FormatInt(startMS, 10) + "-" + strings.ToLower(direction),
			StartMS:   startMS,
			EndMS:     asOfMS,
			Direction: direction,
			Status:    "WATCH",
			Drivers:   []RealtimeFlowEpisodeDriver{},
		},
		lastAsOf:  asOfMS,
		drivers:   make(map[string]*RealtimeFlowEpisodeDriver),
		exchanges: make(map[string]float64),
	}
}

func (item *realtimeFlowEpisodeAccumulator) add(sample realtimeFlowPeriodSample) {
	delta := *sample.window.ShareDeltaPP
	item.episode.EndMS = sample.state.AsOfMS
	item.lastAsOf = sample.state.AsOfMS
	item.episode.ObservationCount++
	item.episode.NetShareChangePP += delta
	item.episode.GrossShareChangePP += math.Abs(delta)
	if math.Abs(delta) > math.Abs(item.episode.PeakShareChangePP) {
		item.episode.PeakShareChangePP = delta
	}
	if sample.state.Status == "ALERT" {
		item.episode.Status = "ALERT"
	}
	attribution := sample.state.TenSecondAttribution
	if attribution == nil || attribution.WindowSec != RealtimeFlowSummarySampleSec {
		return
	}
	item.episode.AttributedSampleCount++
	for _, driver := range attribution.Drivers {
		if driver.Asset == "" || !validSignedAnalysisNumber(driver.ContributionPP) {
			continue
		}
		row := item.drivers[driver.Asset]
		if row == nil {
			row = &RealtimeFlowEpisodeDriver{Asset: driver.Asset}
			item.drivers[driver.Asset] = row
		}
		row.NetContributionPP += driver.ContributionPP
		row.GrossContributionPP += math.Abs(driver.ContributionPP)
		if math.Abs(driver.ContributionPP) > math.Abs(row.PeakContributionPP) {
			row.PeakContributionPP = driver.ContributionPP
		}
		row.ObservationCount++
		if validSignedAnalysisNumber(driver.BithumbVolumeChangeKRW) {
			row.BithumbVolumeChangeKRW += driver.BithumbVolumeChangeKRW
		}
		if validSignedAnalysisNumber(driver.CompetitorVolumeChangeKRW) {
			row.CompetitorVolumeChangeKRW += driver.CompetitorVolumeChangeKRW
		}
	}
	for _, exchange := range attribution.ExchangeDrivers {
		if exchange.Exchange == "" || !validSignedAnalysisNumber(exchange.ContributionPP) {
			continue
		}
		item.exchanges[exchange.Exchange] += exchange.ContributionPP
	}
}

func (item *realtimeFlowEpisodeAccumulator) finish() (RealtimeFlowEpisode, []string) {
	item.episode.DurationSec = (item.episode.EndMS - item.episode.StartMS) / 1_000
	item.episode.Drivers = make([]RealtimeFlowEpisodeDriver, 0, len(item.drivers))
	assets := make([]string, 0, len(item.drivers))
	for asset, driver := range item.drivers {
		item.episode.Drivers = append(item.episode.Drivers, *driver)
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	sort.Slice(item.episode.Drivers, func(i, j int) bool {
		left, right := item.episode.Drivers[i], item.episode.Drivers[j]
		if math.Abs(left.NetContributionPP) != math.Abs(right.NetContributionPP) {
			return math.Abs(left.NetContributionPP) > math.Abs(right.NetContributionPP)
		}
		if left.GrossContributionPP != right.GrossContributionPP {
			return left.GrossContributionPP > right.GrossContributionPP
		}
		return left.Asset < right.Asset
	})
	if len(item.episode.Drivers) > 0 {
		item.episode.TopAsset = item.episode.Drivers[0].Asset
		item.episode.TopAssetContributionPP = item.episode.Drivers[0].NetContributionPP
	}
	if len(item.episode.Drivers) > 5 {
		item.episode.Drivers = item.episode.Drivers[:5]
	}
	for index := range item.episode.Drivers {
		item.episode.Drivers[index].Rank = index + 1
	}
	for exchange, contribution := range item.exchanges {
		if item.episode.TopExchange == "" || math.Abs(contribution) > math.Abs(item.episode.TopExchangeContributionPP) ||
			(math.Abs(contribution) == math.Abs(item.episode.TopExchangeContributionPP) && exchange < item.episode.TopExchange) {
			item.episode.TopExchange = exchange
			item.episode.TopExchangeContributionPP = contribution
		}
	}
	return item.episode, assets
}

func realtimeFlowEpisodeDirection(delta float64) string {
	switch {
	case delta > 0:
		return "UP"
	case delta < 0:
		return "DOWN"
	default:
		return ""
	}
}

func realtimeFlowCompleteTenSecondWindow(state RealtimeFlowState) *RealtimeFlowWindow {
	if state.Version != RealtimeFlowVersion || state.Status == "WARMING" || state.Status == "DEGRADED" {
		return nil
	}
	if state.DataQuality["complete_frame"] != true || state.DataQuality["frame_status"] != "LIVE" {
		return nil
	}
	for index := range state.Windows {
		window := &state.Windows[index]
		if window.DurationSec == RealtimeFlowSummarySampleSec && window.Status == "COMPLETE" &&
			finiteNonnegativeAnalysisPointer(window.CurrentSharePct) && finiteNonnegativeAnalysisPointer(window.PreviousSharePct) &&
			finiteSignedAnalysisPointer(window.ShareDeltaPP) {
			return window
		}
	}
	return nil
}

func validSignedAnalysisNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteSignedAnalysisPointer(value *float64) bool {
	return value != nil && validSignedAnalysisNumber(*value)
}

func finiteNonnegativeAnalysisPointer(value *float64) bool {
	return value != nil && validAnalysisNumber(*value)
}

// PublishedRealtimeFlowPeriodSummary is the transport form of a period
// summary. Summaries are memoized and shared between requests, so the
// returned copy owns every slice it rounds and the source is never modified.
// Counts, timestamps and the effective driver count are not percentages or
// amounts and keep their value.
func PublishedRealtimeFlowPeriodSummary(source RealtimeFlowPeriodSummary) RealtimeFlowPeriodSummary {
	result := source
	result.CoveragePct = roundPublishedPercent(source.CoveragePct)
	result.ObservedShareChangePP = roundPublishedPercent(source.ObservedShareChangePP)
	result.AttributionCoveragePct = roundPublishedPercent(source.AttributionCoveragePct)
	result.ResidualContributionPP = roundPublishedPercent(source.ResidualContributionPP)
	breadth := &result.DriverBreadth
	breadth.Top1AbsoluteContributionSharePct = roundPublishedPercent(breadth.Top1AbsoluteContributionSharePct)
	breadth.Top3AbsoluteContributionSharePct = roundPublishedPercent(breadth.Top3AbsoluteContributionSharePct)
	breadth.Top5AbsoluteContributionSharePct = roundPublishedPercent(breadth.Top5AbsoluteContributionSharePct)
	// Coverage segments carry no amounts or percentages and are only read.

	// Keep nil and empty distinct: they encode as null and [] respectively.
	if source.Drivers != nil {
		result.Drivers = make([]RealtimeFlowPeriodDriver, len(source.Drivers))
	}
	for index, driver := range source.Drivers {
		driver.CumulativeContributionPP = roundPublishedPercent(driver.CumulativeContributionPP)
		driver.AbsoluteContributionPP = roundPublishedPercent(driver.AbsoluteContributionPP)
		driver.PeakContributionPP = roundPublishedPercent(driver.PeakContributionPP)
		driver.DirectionConsistencyPct = roundPublishedPercent(driver.DirectionConsistencyPct)
		driver.BithumbVolumeChangeKRW = roundPublishedKRW(driver.BithumbVolumeChangeKRW)
		driver.CompetitorVolumeChangeKRW = roundPublishedKRW(driver.CompetitorVolumeChangeKRW)
		result.Drivers[index] = driver
	}

	if source.Episodes != nil {
		result.Episodes = make([]RealtimeFlowEpisode, len(source.Episodes))
	}
	for index, episode := range source.Episodes {
		episode.NetShareChangePP = roundPublishedPercent(episode.NetShareChangePP)
		episode.GrossShareChangePP = roundPublishedPercent(episode.GrossShareChangePP)
		episode.PeakShareChangePP = roundPublishedPercent(episode.PeakShareChangePP)
		episode.TopAssetContributionPP = roundPublishedPercent(episode.TopAssetContributionPP)
		episode.TopExchangeContributionPP = roundPublishedPercent(episode.TopExchangeContributionPP)
		var drivers []RealtimeFlowEpisodeDriver
		if episode.Drivers != nil {
			drivers = make([]RealtimeFlowEpisodeDriver, len(episode.Drivers))
		}
		for driverIndex, driver := range episode.Drivers {
			driver.NetContributionPP = roundPublishedPercent(driver.NetContributionPP)
			driver.GrossContributionPP = roundPublishedPercent(driver.GrossContributionPP)
			driver.PeakContributionPP = roundPublishedPercent(driver.PeakContributionPP)
			driver.BithumbVolumeChangeKRW = roundPublishedKRW(driver.BithumbVolumeChangeKRW)
			driver.CompetitorVolumeChangeKRW = roundPublishedKRW(driver.CompetitorVolumeChangeKRW)
			drivers[driverIndex] = driver
		}
		episode.Drivers = drivers
		result.Episodes[index] = episode
	}
	return result
}
