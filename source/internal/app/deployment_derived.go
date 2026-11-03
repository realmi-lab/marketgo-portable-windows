package app

import (
	"errors"
	"time"
)

var errDeploymentDerivedStateChanged = errors.New("derived numerical state changed during server handoff; retry the read")

func (s *Service) deploymentDerivedActive() bool {
	return s.deployment == nil || s.deployment.isActive()
}

// resetDeploymentDerivedState publishes no inferred history. It resets only
// derived analyzers/caches to WARMING; Runtime buckets and coverage are untouched.
// In-flight samplers must recheck the generation before observing or persisting.
// The mutex can wait for at most one bounded one-second derived persistence call.
func (s *Service) resetDeploymentDerivedState() {
	s.deploymentDerivedMu.Lock()
	defer s.deploymentDerivedMu.Unlock()
	if s.realtimeFlow != nil {
		s.realtimeFlow.Reset()
	}
	if s.rollingTracker != nil {
		s.rollingTracker.Reset()
	}
	s.rollingPersistMu.Lock()
	s.rollingPersistedAt = time.Time{}
	s.rollingPersistMu.Unlock()
	s.initRealtimeSummaryState()
	s.realtimeSummaryRing.reset()
	s.realtimePeriodSummaryMemo.reset()
	s.analysisMemoOnce.Do(func() { s.analysisMemo = newAnalysisMemo() })
	s.analysisReadMemoOnce.Do(func() { s.analysisReadMemo = newAnalysisReadMemo() })
	s.eventStudyMemoOnce.Do(func() { s.eventStudyMemo = newEventStudyMemo() })
	// Clear the inner immutable read before the outer response memo: otherwise
	// a newly admitted outer calculation could cache an old inner value.
	s.analysisReadMemo.reset()
	s.eventStudyMemo.reset()
	s.analysisMemo.reset()
	// Publish the generation only after every dependent cache has been fenced.
	// A sampler started during reset therefore remains an old-generation read.
	s.deploymentDerivedGeneration.Add(1)
}
