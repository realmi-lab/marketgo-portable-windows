package app

import (
	"strings"
	"time"

	"market-share-go/internal/coverage"
)

const shareSeriesCorrectionLimit = 128

type shareSeriesCorrection struct {
	revision      uint64
	start, end    int64
	convertedOnly bool
}

type shareSeriesCorrectionSnapshot struct {
	revision uint64
	reset    bool
	changes  []shareSeriesCorrection
}

// The runtime owner records only changes old enough to affect a settled
// bucket. Recent trades already cause the ordinary settling buckets to reload.
// A bounded log lets all scopes/bucket sizes consume changes independently of
// the request that first encountered them, without retaining event history.
type shareSeriesCorrectionLog struct {
	revision         uint64
	discardedThrough uint64
	changes          []shareSeriesCorrection
}

func (l *shareSeriesCorrectionLog) record(start, end, now int64, convertedOnly bool) {
	end = min(end, now-shareSeriesSettleSec)
	if start <= 0 || end < start {
		return
	}
	l.revision++
	if len(l.changes) > 0 {
		last := &l.changes[len(l.changes)-1]
		if last.convertedOnly == convertedOnly && start <= last.end+1 && end >= last.start-1 {
			last.start, last.end, last.revision = min(last.start, start), max(last.end, end), l.revision
			return
		}
	}
	if len(l.changes) == shareSeriesCorrectionLimit {
		l.discardedThrough = l.changes[0].revision
		copy(l.changes, l.changes[1:])
		l.changes = l.changes[:shareSeriesCorrectionLimit-1]
	}
	l.changes = append(l.changes, shareSeriesCorrection{
		revision: l.revision, start: start, end: end, convertedOnly: convertedOnly,
	})
}

func (l *shareSeriesCorrectionLog) since(revision uint64) shareSeriesCorrectionSnapshot {
	result := shareSeriesCorrectionSnapshot{revision: l.revision, reset: revision < l.discardedThrough}
	if result.reset || revision == l.revision {
		return result
	}
	for _, change := range l.changes {
		if change.revision > revision {
			result.changes = append(result.changes, change)
		}
	}
	return result
}

func (m *shareSeriesMemo) applyCorrections(snapshot shareSeriesCorrectionSnapshot) {
	if snapshot.reset {
		clear(m.kept)
	} else {
		for _, change := range snapshot.changes {
			for key, buckets := range m.kept {
				if change.convertedOnly && key.scope == "KRW" {
					continue
				}
				for start := range buckets {
					if start <= change.end && start+key.bucketSec-1 >= change.start {
						delete(buckets, start)
					}
				}
			}
		}
	}
	m.revision = snapshot.revision
}

func (r *Runtime) recordShareSeriesTradeCorrection(second int64, quote string, now int64) {
	if second > now-shareSeriesSettleSec {
		return
	}
	r.shareSeriesCorrections.record(second, second, now, !strings.EqualFold(strings.TrimSpace(quote), "KRW"))
}

// InvalidateFrom removes coverage from start onwards, including already
// observed seconds after the detection timestamp. Invalidate matching settled
// cache entries from the same owner turn before changing the tracker.
func (r *Runtime) invalidateDomesticCoverage(start, detected int64, gap coverage.GapContext) {
	if start <= 0 || start > detected {
		start = detected
	}
	r.shareSeriesCorrections.record(start, max(detected, r.coverage.LastObservedSecond()), time.Now().Unix(), false)
	r.coverage.InvalidateFrom(start, detected, gap)
}
