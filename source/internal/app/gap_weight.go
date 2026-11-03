package app

import (
	"strings"

	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

// gapWeightWindowSec is the trading history that weighs an observation gap:
// the ten minutes before it began.
const gapWeightWindowSec = 600

// weighOpenGap records the open domestic gap's share of KRW trading in the ten
// minutes before it began (an empty scope means every exchange, so 100%). It
// runs on the owner goroutine after each step that can open or widen a gap.
func (r *Runtime) weighOpenGap() {
	r.coverage.WeighOpenGap(func(context coverage.GapContext, start int64) (float64, float64, int64, bool) {
		values, ok := r.aggregator.IntervalSnapshot(
			domain.UnixSecond(start-gapWeightWindowSec-1), domain.UnixSecond(start-1),
		)
		if !ok {
			return 0, 0, 0, false
		}
		var scopeKRW, totalKRW float64
		for key, totals := range values {
			if domain.Scope(strings.ToLower(key.Scope)) != domain.ScopeDomestic || !strings.EqualFold(key.Quote, "KRW") {
				continue
			}
			totalKRW += totals.KRWVolume
			if context.Covers(key.Exchange, key.Market) {
				scopeKRW += totals.KRWVolume
			}
		}
		return scopeKRW, totalKRW, gapWeightWindowSec, true
	})
}
