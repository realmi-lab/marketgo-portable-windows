package coverage

// ContinueCheckpoint restores an already running producer's exact checkpoint.
// Unlike Restore (process startup), this does not manufacture a restart gap or
// join two independent timelines. The caller may append only separately
// captured observations after this checkpoint and must replay real losses.
func ContinueCheckpoint(state State) *Tracker {
	t := &Tracker{
		coverageStart: state.CoverageStart, lastObserved: state.LastObserved,
		invalidThrough: state.InvalidThrough,
		liveStart:      state.LiveStart, segments: normalizeSegments(state.Segments),
		historicalRecoveries: normalizeHistoricalRecoveries(state.HistoricalRecoveries),
		gaps:                 copyGaps(state.Gaps),
	}
	if t.liveStart <= 0 {
		t.liveStart = state.LastObserved
	}
	for i := range t.gaps {
		ensureGapPersistenceToken(&t.gaps[i])
		if t.gaps[i].Status == "OPEN" && t.gaps[i].End == nil {
			index := i
			t.activeGap = &index
			t.invalidThrough = maxInt64(t.invalidThrough, t.gaps[i].Start)
		}
	}
	return t
}
