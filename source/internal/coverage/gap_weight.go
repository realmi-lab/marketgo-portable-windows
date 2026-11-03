package coverage

import (
	"slices"
	"strings"
)

// GapWeigher returns the domestic KRW trading value of a gap's scope and of
// the whole market over windowSec seconds before start. ok is false when that
// history is not retained.
type GapWeigher func(context GapContext, start int64) (scopeKRW, totalKRW float64, windowSec int64, ok bool)

// WeighOpenGap records how much of the market the open gap's scope carried
// just before it began: ObservedVolumeAtRisk is that scope's percentage of
// domestic KRW trading. A scope is weighed once. Widening it clears the weight
// (see mergedWeight), so the next call weighs the wider scope.
//
// The weight stays unknown when another gap of the same exchanges touches the
// weighing window: trading that was not observed there would make the gap
// look smaller than it is.
func (t *Tracker) WeighOpenGap(weigh GapWeigher) {
	if t.activeGap == nil {
		return
	}
	active := *t.activeGap
	gap := &t.gaps[active]
	if gap.Context.ObservedVolumeAtRisk != nil {
		return
	}
	scope, total, window, ok := weigh(gap.Context, gap.Start)
	if !ok || !(total > 0) || !(scope >= 0 && scope <= total) {
		return // nothing traded to weigh against, or inconsistent sums
	}
	if t.otherGapOfSameExchangesTouches(active, gap.Start-window, gap.Start-1) {
		return
	}
	risk := 100 * scope / total
	gap.Context.ObservedVolumeKRW = &scope
	gap.Context.ObservedTotalVolumeKRW = &total
	gap.Context.ObservedVolumeAtRisk = &risk
	gap.Context.WeightWindowSec = window
}

// otherGapOfSameExchangesTouches reports whether a gap other than the active
// one names one of its exchanges (an empty list names every exchange) and
// overlaps [from, to].
func (t *Tracker) otherGapOfSameExchangesTouches(active int, from, to int64) bool {
	exchanges := t.gaps[active].Context.AffectedExchanges
	for index, other := range t.gaps {
		end := to // an unfinished gap reaches every later second
		if other.End != nil {
			end = *other.End
		}
		if index == active || other.Start > to || end < from {
			continue
		}
		if len(exchanges) == 0 || len(other.Context.AffectedExchanges) == 0 ||
			slices.ContainsFunc(exchanges, other.Context.CoversExchange) {
			return true
		}
	}
	return false
}

// Covers reports whether the gap includes an exchange's market. An empty
// exchange or market list means every exchange or market.
func (c GapContext) Covers(exchange, market string) bool {
	return c.CoversExchange(exchange) && scopeIncludes(c.AffectedMarkets, market)
}

// CoversExchange reports whether the gap includes any market of an exchange.
func (c GapContext) CoversExchange(exchange string) bool {
	return scopeIncludes(c.AffectedExchanges, exchange)
}

func scopeIncludes(scope []string, value string) bool {
	if len(scope) == 0 {
		return true
	}
	return slices.ContainsFunc(scope, func(item string) bool {
		return strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value))
	})
}

// mergedWeight returns the side whose trading weight still describes the
// merged scope. A widened scope has no weight until it is weighed again.
func mergedWeight(existing, update, merged GapContext) GapContext {
	if update.ObservedVolumeAtRisk != nil && sameScope(update, merged) {
		return update
	}
	if sameScope(existing, merged) {
		return existing
	}
	return GapContext{}
}

func sameScope(left, right GapContext) bool {
	return slices.Equal(left.AffectedExchanges, right.AffectedExchanges) &&
		slices.Equal(left.AffectedMarkets, right.AffectedMarkets)
}

func (c *GapContext) copyWeight(source GapContext) {
	c.ObservedVolumeKRW = source.ObservedVolumeKRW
	c.ObservedTotalVolumeKRW = source.ObservedTotalVolumeKRW
	c.ObservedVolumeAtRisk = source.ObservedVolumeAtRisk
}
