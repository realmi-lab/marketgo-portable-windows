package app

import (
	"math"
	"sort"
	"strings"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
)

const minuteSupplementBasis = "WEBSOCKET_PLUS_EXACT_OFFICIAL_MINUTE_RESIDUAL"

type minuteSupplementApplication struct {
	byExchange                      map[domain.Exchange]float64
	byMarket                        map[aggregate.VolumeKey]float64
	totalVolumeKRW                  float64
	bithumbVolumeKRW                float64
	rowCount                        int
	gapSeconds                      int64
	exchanges                       []string
	subminuteEstimate               bool
	supplementSegments              []coverage.Segment
	intervalSec                     int64
	replacementCount                int
	replacementVolumeKRW            float64
	replacementAdjustmentKRW        float64
	replacementBithumbAdjustmentKRW float64
	byMarketReplacement             map[aggregate.VolumeKey]float64
	byMarketLegacySupplement        map[aggregate.VolumeKey]float64
}

func applyMinuteSupplements(
	rows []reconcile.MinuteSupplement,
	state coverage.State,
	windowStart, windowEnd int64,
) minuteSupplementApplication {
	result := minuteSupplementApplication{
		byExchange:               make(map[domain.Exchange]float64),
		byMarket:                 make(map[aggregate.VolumeKey]float64),
		byMarketReplacement:      make(map[aggregate.VolumeKey]float64),
		byMarketLegacySupplement: make(map[aggregate.VolumeKey]float64),
	}
	if windowEnd < windowStart {
		return result
	}
	exchangeSet := make(map[string]struct{})
	// A duplicate retry is one market-minute, never another amount to add.
	// Conflicting official values for the same identity are unusable.
	type minuteKey struct {
		exchange domain.Exchange
		market   string
		start    domain.UnixSecond
	}
	unique := make(map[minuteKey]reconcile.MinuteSupplement, len(rows))
	conflicting := make(map[minuteKey]bool)
	for _, row := range rows {
		key := minuteKey{row.Exchange, strings.ToUpper(strings.TrimSpace(row.Market)), row.MinuteStart}
		if old, exists := unique[key]; exists && old != row {
			conflicting[key] = true
		}
		unique[key] = row
	}
	keys := make([]minuteKey, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].start != keys[j].start {
			return keys[i].start < keys[j].start
		}
		if keys[i].exchange != keys[j].exchange {
			return keys[i].exchange < keys[j].exchange
		}
		return keys[i].market < keys[j].market
	})
	for _, key := range keys {
		row := unique[key]
		if conflicting[key] || row.NotionalMethod != "" || !exactMinuteExchange(row.Exchange) || !strings.EqualFold(strings.TrimSpace(row.Quote), "KRW") ||
			(!row.ReplaceWholeMinute && !finitePositive(row.SupplementalVolumeKRW)) {
			continue
		}
		if row.ReplaceWholeMinute && !validWholeMinuteAdjustment(row) {
			continue
		}
		intervalSec := row.IntervalSec
		if intervalSec <= 0 {
			intervalSec = reconcile.MinuteSeconds
		}
		minuteStart := int64(row.MinuteStart)
		minuteEnd := minuteStart + intervalSec - 1
		// A candle proves its whole closed minute, not any particular second.
		// Never distribute a residual across a clipped minute or a 10s signal.
		if intervalSec != reconcile.MinuteSeconds || minuteStart%reconcile.MinuteSeconds != 0 ||
			minuteStart < windowStart || minuteEnd > windowEnd {
			continue
		}
		result.intervalSec = reconcile.MinuteSeconds

		gapSegments := applicableCandleGapSegments(state, row, minuteStart, minuteEnd)
		minuteGapSeconds := segmentSeconds(gapSegments)
		if minuteGapSeconds > 0 {
			result.supplementSegments = append(
				result.supplementSegments,
				gapSegments...,
			)
		} else if len(gapSegments) == 0 && row.KnownGapSeconds > 0 &&
			!gapHistoryOverlapsMinute(state, minuteStart, minuteEnd) && !exactRecoveryOverlapsMinute(state, minuteStart, minuteEnd) {
			// An aged-out gap may retain persisted minute evidence. Its complete
			// residual is usable only for the complete minute; timing stays unknown.
			result.gapSeconds = max(result.gapSeconds, min(row.KnownGapSeconds, intervalSec))
		} else {
			continue
		}
		volume := row.SupplementalVolumeKRW
		if row.ReplaceWholeMinute {
			volume = row.ReplacementAdjustmentKRW
		}
		if !row.ReplaceWholeMinute && !finitePositive(volume) {
			continue
		}
		result.byExchange[row.Exchange] += volume
		marketKey := aggregate.VolumeKey{
			Scope: string(domain.ScopeDomestic), Exchange: string(row.Exchange),
			Market: strings.ToUpper(strings.TrimSpace(row.Market)),
			Base:   strings.ToUpper(strings.TrimSpace(row.Base)), Quote: "KRW",
		}
		result.byMarket[marketKey] += volume
		if row.ReplaceWholeMinute {
			result.replacementCount++
			result.replacementVolumeKRW += row.OfficialVolumeKRW
			result.replacementAdjustmentKRW += volume
			result.byMarketReplacement[marketKey] += row.OfficialVolumeKRW
			if row.Exchange == domain.ExchangeBithumb {
				result.replacementBithumbAdjustmentKRW += volume
			}
		} else {
			result.byMarketLegacySupplement[marketKey] += volume
		}
		result.totalVolumeKRW += volume
		if row.Exchange == domain.ExchangeBithumb {
			result.bithumbVolumeKRW += volume
		}
		result.rowCount++
		exchangeSet[string(row.Exchange)] = struct{}{}
	}
	result.exchanges = make([]string, 0, len(exchangeSet))
	for exchange := range exchangeSet {
		result.exchanges = append(result.exchanges, exchange)
	}
	sort.Strings(result.exchanges)
	result.gapSeconds = max(result.gapSeconds, segmentSeconds(normalizeCoverageSegments(result.supplementSegments)))
	result.supplementSegments = nil
	return result
}

func (v analysisReadView) supplementsForWindow(_ int64) ([]reconcile.MinuteSupplement, string) {
	return v.minuteSupplements, v.supplementStatus
}

// minuteSupplementsByWindow computes the scope-independent candle overlay once
// for an immutable analysis read. KRW and all-domestic-converted windows use
// the same exact KRW residuals, so recomputing this scan for each scope only
// duplicates work and allocations.
func minuteSupplementsByWindow(view analysisReadView, now int64) map[int64]minuteSupplementApplication {
	result := make(map[int64]minuteSupplementApplication, len(aggregate.DefaultWindows))
	for _, window := range aggregate.DefaultWindows {
		rows, _ := view.supplementsForWindow(window)
		result[window] = applyMinuteSupplements(rows, view.coverageState, now-window+1, now)
	}
	return result
}

func supplementBasis(_ int64) string {
	return minuteSupplementBasis
}

func supplementIntervalForWindow(_ int64) int64 {
	return reconcile.MinuteSeconds
}

func exactMinuteExchange(exchange domain.Exchange) bool {
	return exchange == domain.ExchangeUpbit || exchange == domain.ExchangeBithumb || exchange == domain.ExchangeCoinone
}

func exactRecoveryOverlapsMinute(state coverage.State, start, end int64) bool {
	for _, recovery := range state.HistoricalRecoveries {
		if recovery.End >= start && recovery.Start <= end {
			return true
		}
	}
	return false
}

func applyMinuteSupplementsToAnalysisRows(
	rows []marketanalysis.Row,
	supplement minuteSupplementApplication,
) []marketanalysis.Row {
	if (supplement.totalVolumeKRW <= 0 && supplement.replacementCount == 0) || len(supplement.byMarket) == 0 {
		return rows
	}
	result := append([]marketanalysis.Row(nil), rows...)
	index := make(map[string]int, len(result))
	for rowIndex, row := range result {
		if row.Scope == domain.ScopeDomestic {
			index[analysisSupplementKey(row.Exchange, row.Market)] = rowIndex
		}
	}
	for key, volume := range supplement.byMarket {
		replacementVolume, replaced := supplement.byMarketReplacement[key]
		supplementalVolume := volume
		if replaced {
			supplementalVolume = supplement.byMarketLegacySupplement[key]
		}
		if !replaced && !finitePositive(volume) {
			continue
		}
		exchange := domain.Exchange(strings.ToLower(strings.TrimSpace(key.Exchange)))
		market := strings.ToUpper(strings.TrimSpace(key.Market))
		if rowIndex, found := index[analysisSupplementKey(exchange, market)]; found {
			result[rowIndex].VolumeKRW += volume
			result[rowIndex].SupplementalVolumeKRW += supplementalVolume
			if replaced {
				result[rowIndex].OfficialReplacementVolumeKRW += replacementVolume
				result[rowIndex].TradeDetailIncomplete = true
			}
			continue
		}
		result = append(result, marketanalysis.Row{
			Scope: domain.ScopeDomestic, Exchange: exchange, Market: market,
			Base: strings.ToUpper(strings.TrimSpace(key.Base)), Quote: "KRW",
			VolumeKRW: volume, SupplementalVolumeKRW: supplementalVolume,
			OfficialReplacementVolumeKRW: replacementVolume, TradeDetailIncomplete: replaced,
		})
		index[analysisSupplementKey(exchange, market)] = len(result) - 1
	}
	return result
}

func validWholeMinuteAdjustment(row reconcile.MinuteSupplement) bool {
	if row.Base == "" || row.Source != strings.ToUpper(string(row.Exchange))+"_OFFICIAL_MINUTE_CANDLE_REST" ||
		row.OfficialVolumeKRW < 0 || row.WSVolumeKRW < 0 || row.SupplementalVolumeKRW != 0 {
		return false
	}
	for _, amount := range []float64{row.OfficialVolumeKRW, row.WSVolumeKRW, row.ReplacementAdjustmentKRW} {
		if math.IsNaN(amount) || math.IsInf(amount, 0) {
			return false
		}
	}
	return row.ReplacementAdjustmentKRW == row.OfficialVolumeKRW-row.WSVolumeKRW
}

func analysisSupplementKey(exchange domain.Exchange, market string) string {
	return strings.ToLower(strings.TrimSpace(string(exchange))) + "\x00" + strings.ToUpper(strings.TrimSpace(market))
}

func gapHistoryOverlapsMinute(state coverage.State, minuteStart, minuteEnd int64) bool {
	for _, gap := range state.Gaps {
		gapEnd := minuteEnd
		if gap.End != nil {
			gapEnd = *gap.End
		}
		if gapEnd >= minuteStart && gap.Start <= minuteEnd {
			return true
		}
	}
	return false
}

func applicableCandleGapSegments(
	state coverage.State,
	row reconcile.MinuteSupplement,
	minuteStart, minuteEnd int64,
) []coverage.Segment {
	segments := make([]coverage.Segment, 0)
	for _, gap := range state.Gaps {
		gapEnd := minuteEnd
		if gap.End != nil {
			gapEnd = *gap.End
		}
		if gapEnd < minuteStart || gap.Start > minuteEnd || !gap.Context.Covers(string(row.Exchange), row.Market) {
			continue
		}
		segments = append(segments, coverage.Segment{Start: max(minuteStart, gap.Start), End: min(minuteEnd, gapEnd)})
	}
	segments = normalizeCoverageSegments(segments)
	for _, recovery := range state.HistoricalRecoveries {
		for _, segment := range segments {
			if recovery.End >= segment.Start && recovery.Start <= segment.End {
				// The reconciliation residual predates the exact raw-trade replay.
				// Its split cannot be recomputed safely, so prefer the exact replay
				// and suppress the candle overlay for this market-minute entirely.
				return nil
			}
		}
	}
	return segments
}

func clipCoverageSegments(values []coverage.Segment, start, end int64) []coverage.Segment {
	result := make([]coverage.Segment, 0, len(values))
	for _, value := range values {
		clipped := coverage.Segment{Start: max(value.Start, start), End: min(value.End, end)}
		if clipped.End >= clipped.Start {
			result = append(result, clipped)
		}
	}
	return normalizeCoverageSegments(result)
}

func normalizeCoverageSegments(values []coverage.Segment) []coverage.Segment {
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Start == values[j].Start {
			return values[i].End < values[j].End
		}
		return values[i].Start < values[j].Start
	})
	result := values[:1]
	for _, value := range values[1:] {
		last := &result[len(result)-1]
		if value.Start <= last.End+1 {
			last.End = max(last.End, value.End)
			continue
		}
		result = append(result, value)
	}
	return result
}

func segmentSeconds(values []coverage.Segment) int64 {
	var result int64
	for _, value := range values {
		if value.End >= value.Start {
			result += value.End - value.Start + 1
		}
	}
	return result
}

func inclusiveOverlap(aStart, aEnd, bStart, bEnd int64) int64 {
	start, end := max(aStart, bStart), min(aEnd, bEnd)
	if end < start {
		return 0
	}
	return end - start + 1
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
