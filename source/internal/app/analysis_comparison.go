package app

import (
	"slices"
	"strings"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

// minorGapVolumeAtRiskPct is the largest share of domestic KRW trading, in
// percent and measured just before a gap, that the gap's exchanges may carry
// for a comparison to continue without them. Leaving out exchanges this small
// moves Bithumb's share by at most about 0.1% of itself.
const minorGapVolumeAtRiskPct = 0.1

// minorGapWeightAgeSec is how long a measured gap weight speaks for later
// gaps of the same exchanges that could not be weighed (see gapWeight).
const minorGapWeightAgeSec int64 = 24 * 3600

// analysisWindowRows returns a window's current and previous domestic rows
// and the comparison deciding which exchanges they may use. Each window's
// analysis and the cross-window changes read a window the same way.
func analysisWindowRows(
	snapshot analysisReadSnapshot,
	window int64,
	marketScope string,
) (currentRows, previousRows []marketanalysis.Row, comparison marketanalysis.Comparison) {
	end, read := snapshot.endSecond, snapshot.view
	comparison = everyExchangeComparison(false)
	var previous map[aggregate.VolumeKey]aggregate.Totals
	if window != marketanalysis.RollingWindowSec { // 24 hours has no earlier window to compare
		previous = read.interval(end-2*window, end-window)
		comparison = chooseAnalysisComparison(
			snapshot.windows[marketScope][window].WindowComplete &&
				read.coverageFor(end-2*window+1, end-window).WindowComplete &&
				snapshot.operational.health.RealtimeReady,
			marketScope, snapshot.operational, read.coverageState, end-2*window+1, end,
		)
	}
	return analysisCurrentRows(snapshot, window, marketScope), analysisRows(filterAnalysisValues(previous, marketScope)), comparison
}

// analysisCurrentRows returns a window's rows with the official minute
// supplements that fill its recorded gaps.
func analysisCurrentRows(snapshot analysisReadSnapshot, window int64, marketScope string) []marketanalysis.Row {
	end, read := snapshot.endSecond, snapshot.view
	supplementRows, _ := read.supplementsForWindow(window)
	supplement := applyMinuteSupplements(supplementRows, read.coverageState, end-window+1, end)
	return applyMinuteSupplementsToAnalysisRows(
		analysisRows(filterAnalysisValues(read.interval(end-window, end), marketScope)), supplement,
	)
}

func everyExchangeComparison(complete bool) marketanalysis.Comparison {
	return marketanalysis.Comparison{
		Complete: complete, Included: slices.Clone(domain.DomesticExchanges[:]), Excluded: []domain.Exchange{},
	}
}

// chooseAnalysisComparison decides how the change over [start, end], the
// previous and current windows together, can be compared. A complete span
// uses every exchange. An incomplete one leaves out exchanges when that keeps
// Bithumb and a competitor: first the exchanges that are unhealthy now,
// otherwise (KRW only) small exchanges whose recorded gaps explain every
// missing second. Failing that, while every collector is healthy and the
// realtime feed ready, every exchange is compared over the seconds observed
// (관측범위기준). A comparison with Bithumb or every competitor unhealthy now
// stays unavailable.
func chooseAnalysisComparison(
	complete bool,
	marketScope string,
	operational *analysisOperationalSnapshot,
	coverageState coverage.State,
	start, end int64,
) marketanalysis.Comparison {
	if complete {
		return everyExchangeComparison(true)
	}
	included, excluded := analysisComparisonExchangeSets(marketScope, operational.collectors)
	result := marketanalysis.Comparison{Included: included, Excluded: excluded}
	if len(excluded) > 0 {
		if comparableExchangeSet(included) {
			result.ProvisionalReason = marketanalysis.ProvisionalIncompleteExchangeSet
		}
		return result
	}
	if !operational.health.RealtimeReady {
		return result
	}
	observedRange := everyExchangeComparison(false)
	observedRange.ProvisionalReason = marketanalysis.ProvisionalObservedRange
	if marketScope != "KRW" {
		return observedRange
	}
	gapExchanges, ok := minorGapExchanges(coverageState, start, end)
	if !ok {
		return observedRange
	}
	minor := marketanalysis.Comparison{ProvisionalReason: marketanalysis.ProvisionalMinorObservationGap, Excluded: gapExchanges}
	for _, exchange := range domain.DomesticExchanges {
		if !slices.Contains(gapExchanges, exchange) {
			minor.Included = append(minor.Included, exchange)
		}
	}
	if !comparableExchangeSet(minor.Included) {
		return observedRange
	}
	return minor
}

// comparableExchangeSet keeps Bithumb and at least one competitor.
func comparableExchangeSet(included []domain.Exchange) bool {
	return containsExchange(included, domain.ExchangeBithumb) && len(included) >= 2
}

// minorGapExchanges returns the domestic exchanges to leave out when every
// gap touching [start, end] is minor and together they explain each second
// that was not observed there. ok is false otherwise.
func minorGapExchanges(state coverage.State, start, end int64) ([]domain.Exchange, bool) {
	explained := slices.Clone(state.Segments)
	named := map[domain.Exchange]bool{}
	for _, gap := range state.Gaps {
		gapEnd := end // an open gap reaches the end of the span
		if gap.End != nil {
			gapEnd = *gap.End
		}
		if gapEnd < start || gap.Start > end {
			continue
		}
		if !minorGap(gap.Context, gapWeight(state.Gaps, gap)) {
			return nil, false
		}
		for _, name := range gap.Context.AffectedExchanges {
			named[domain.Exchange(strings.ToLower(strings.TrimSpace(name)))] = true
		}
		explained = append(explained, coverage.Segment{Start: gap.Start, End: gapEnd})
	}
	if len(named) == 0 || !coverage.CalculateWithHistorical(explained, state.HistoricalRecoveries, start, end).WindowComplete {
		return nil, false
	}
	var excluded []domain.Exchange
	for _, exchange := range domain.DomesticExchanges {
		if named[exchange] {
			excluded = append(excluded, exchange)
			delete(named, exchange)
		}
	}
	return excluded, len(named) == 0 // every named exchange must be domestic
}

// minorGap is a gap over whole named exchanges, Bithumb not among them, that
// carried at most minorGapVolumeAtRiskPct of trading when it began (risk, see
// gapWeight). A gap over some markets of an exchange is never minor: its
// weight is those markets' trading, while leaving the exchange out would drop
// all of its markets.
func minorGap(context coverage.GapContext, risk *float64) bool {
	if len(context.AffectedExchanges) == 0 || len(context.AffectedMarkets) > 0 ||
		context.CoversExchange(string(domain.ExchangeBithumb)) {
		return false // every exchange, some markets only, or Bithumb itself
	}
	return risk != nil && *risk <= minorGapVolumeAtRiskPct
}

// gapWeight is the share of trading a gap's exchanges carried when it began.
// A gap is not weighed when an earlier gap of the same exchanges covers the
// minutes it would be weighed against, as when an exchange's collector drops
// out every few minutes; it then borrows the last weight measured for exactly
// those exchanges in the day before, which was measured over observed
// trading. With none, the weight stays unknown.
func gapWeight(gaps []coverage.Gap, gap coverage.Gap) *float64 {
	if gap.Context.ObservedVolumeAtRisk != nil {
		return gap.Context.ObservedVolumeAtRisk
	}
	var weight *float64
	latest := gap.Start - minorGapWeightAgeSec
	for _, other := range gaps {
		measured := other.Context.ObservedVolumeAtRisk
		if measured == nil || other.Start > gap.Start || other.Start < latest ||
			len(other.Context.AffectedMarkets) > 0 ||
			!slices.Equal(other.Context.AffectedExchanges, gap.Context.AffectedExchanges) {
			continue
		}
		weight, latest = measured, other.Start
	}
	return weight
}
