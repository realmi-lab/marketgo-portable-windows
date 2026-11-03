package movement

import "context"

type EventWrite struct {
	Event             *Event
	Baseline, Extreme *Snapshot
}
type ObservationCommit func(context.Context, *Snapshot, []EventWrite) error

type stagedObservationStore struct {
	Store
	sample *Snapshot
	events []EventWrite
}

func (s *stagedObservationStore) SaveMovementSample(_ context.Context, value *Snapshot) error {
	s.sample = cloneSnapshot(value)
	return nil
}
func (s *stagedObservationStore) SaveMovementEvent(_ context.Context, value *Event, baseline, extreme *Snapshot) error {
	s.events = append(s.events, EventWrite{cloneEvent(value), cloneSnapshot(baseline), cloneSnapshot(extreme)})
	return nil
}

// ObserveAtomic calculates privately and publishes memory only after every
// sample/event write commits. A failed commit can be retried without a false
// duplicate classification caused by prematurely advanced lastObservedMS.
func (t *Tracker) ObserveAtomic(ctx context.Context, snapshot *Snapshot, commit ObservationCommit) error {
	return t.observeAtomic(ctx, func(candidate *Tracker) error { return candidate.Observe(ctx, snapshot) }, commit)
}
func (t *Tracker) ObserveGapAtomic(ctx context.Context, at int64, commit ObservationCommit) error {
	return t.observeAtomic(ctx, func(candidate *Tracker) error { return candidate.ObserveGap(ctx, at) }, commit)
}

// The observation is computed on a private copy and committed while only
// writeMu is held: State and the other readers keep returning the last
// published state during the database commit instead of waiting for it.
// writeMu keeps every other writer out until the result is published, so the
// copy cannot go stale, and a notice relink that arrives meanwhile is applied
// after this publication rather than overwritten by it.
func (t *Tracker) observeAtomic(ctx context.Context, observe func(*Tracker) error, commit ObservationCommit) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	stage := &stagedObservationStore{Store: t.store}
	t.mu.RLock()
	candidate := &Tracker{cfg: t.cfg, store: stage, baseline: cloneSnapshot(t.baseline), candidateDirection: t.candidateDirection, candidateCount: t.candidateCount, releaseCount: t.releaseCount, lastObservedMS: t.lastObservedMS, active: cloneEvent(t.active), extreme: cloneSnapshot(t.extreme), current: cloneSnapshot(t.current), trend: append([]SamplePoint(nil), t.trend...)}
	for i := range t.recent {
		candidate.recent = append(candidate.recent, *cloneEvent(&t.recent[i]))
	}
	t.mu.RUnlock()
	if err := observe(candidate); err != nil {
		return err
	}
	if err := commit(ctx, stage.sample, stage.events); err != nil {
		return err
	}
	for _, write := range stage.events {
		if candidate.active != nil && candidate.active.EventID == write.Event.EventID {
			candidate.active.Notices = cloneNoticeEvidence(write.Event.Notices)
		}
		for i := range candidate.recent {
			if candidate.recent[i].EventID == write.Event.EventID {
				candidate.recent[i].Notices = cloneNoticeEvidence(write.Event.Notices)
			}
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.baseline = candidate.baseline
	t.candidateDirection = candidate.candidateDirection
	t.candidateCount = candidate.candidateCount
	t.releaseCount = candidate.releaseCount
	t.lastObservedMS = candidate.lastObservedMS
	t.active = candidate.active
	t.extreme = candidate.extreme
	t.current = candidate.current
	t.trend = candidate.trend
	t.recent = candidate.recent
	return nil
}
