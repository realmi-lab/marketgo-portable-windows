package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/internal/store/postgres"
)

// Version 2 allows longer private preparation while the current writer runs.
// Older controllers still use their conservative version-1 deadlines.
const deploymentCheckpointPreparationProtocol = 2

const deploymentPreparationLimit = 60 * time.Second

type DeploymentStatus struct {
	DeploymentState
	CheckpointProtocol            int                         `json:"checkpoint_protocol,omitempty"`
	CheckpointPreparationProtocol int                         `json:"checkpoint_preparation_protocol,omitempty"`
	CheckpointPrepared            bool                        `json:"checkpoint_prepared"`
	CheckpointCaptureGeneration   uint64                      `json:"checkpoint_capture_generation,omitempty"`
	CheckpointCaptureStartedAtMS  int64                       `json:"checkpoint_capture_started_at_ms,omitempty"`
	CheckpointCaptureAgeMS        int64                       `json:"checkpoint_capture_age_ms,omitempty"`
	CheckpointCaptureHealthy      bool                        `json:"checkpoint_capture_healthy"`
	CheckpointCaptureError        string                      `json:"checkpoint_capture_error,omitempty"`
	Checkpoint                    *DeploymentCheckpointResult `json:"checkpoint,omitempty"`
	WarmReady                     bool                        `json:"warm_ready"`
	TrafficReady                  bool                        `json:"traffic_ready"`
	RealtimeReady                 bool                        `json:"realtime_ready"`
	DatabaseReady                 bool                        `json:"database_ready"`
	UnhealthyExchanges            []string                    `json:"unhealthy_exchanges,omitempty"`
	OfficialStatus                string                      `json:"official_status"`
	OfficialAsOf                  float64                     `json:"official_as_of"`
	OfficialAgeMS                 int64                       `json:"official_age_ms"`
	OfficialCollectionMS          int64                       `json:"official_collection_span_ms"`
	OfficialReadyExchanges        int                         `json:"official_ready_exchanges"`
	OfficialExpected              int                         `json:"official_expected_exchanges"`
	ExactWindowStatus             string                      `json:"exact_window_status"`
	ExactWindowComplete           bool                        `json:"exact_window_complete"`
	ExactWindowCoverageSec        int64                       `json:"exact_window_coverage_sec"`
	ExactWindowGapSec             int64                       `json:"exact_window_gap_sec"`
}

func (s *Service) DeploymentStatus() DeploymentStatus {
	if s == nil || s.deployment == nil {
		return DeploymentStatus{DeploymentState: DeploymentState{Phase: DeploymentPhaseFault}, OfficialStatus: "UNAVAILABLE"}
	}
	state := s.deployment.state()
	health := s.HealthState()
	result := DeploymentStatus{
		DeploymentState:    state,
		RealtimeReady:      health.RealtimeReady,
		DatabaseReady:      health.DatabaseReady,
		UnhealthyExchanges: append([]string(nil), health.Unhealthy...),
		OfficialStatus:     "WARMING",
	}
	if s.postgresRepository != nil && s.deployment.leaseRequired {
		result.CheckpointProtocol = 2
		result.CheckpointPreparationProtocol = deploymentCheckpointPreparationProtocol
		result.CheckpointPrepared = s.deploymentPrepared.Load()
		result.Checkpoint = s.deploymentCheckpointResult.Load()
		if result.CheckpointPrepared {
			capture := s.deploymentPreparedCapture.Load()
			if capture == nil || s.runtime == nil {
				result.CheckpointCaptureError = "prepared capture attestation is unavailable"
			} else {
				captureCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				captureStatus, err := s.runtime.preparedDeploymentCaptureStatus(captureCtx, capture)
				cancel()
				if err != nil {
					result.CheckpointCaptureError = err.Error()
				} else {
					result.CheckpointCaptureGeneration = captureStatus.generation
					result.CheckpointCaptureStartedAtMS = captureStatus.started.UnixMilli()
					result.CheckpointCaptureAgeMS = max(0, captureStatus.age.Milliseconds())
					result.CheckpointCaptureHealthy = captureStatus.healthy
					if captureStatus.err != nil {
						result.CheckpointCaptureError = captureStatus.err.Error()
					}
				}
			}
		} else if s.runtime != nil {
			captureCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			captureStatus, err := s.runtime.currentDeploymentCaptureStatus(captureCtx)
			cancel()
			if err != nil {
				result.CheckpointCaptureError = err.Error()
			} else if captureStatus.generation != 0 || captureStatus.err != nil {
				result.CheckpointCaptureGeneration = captureStatus.generation
				result.CheckpointCaptureStartedAtMS = captureStatus.started.UnixMilli()
				result.CheckpointCaptureAgeMS = max(0, captureStatus.age.Milliseconds())
				result.CheckpointCaptureHealthy = captureStatus.healthy
				if captureStatus.err != nil {
					result.CheckpointCaptureError = captureStatus.err.Error()
				}
			}
		}
	}
	if s.officialDomestic != nil {
		official := s.officialDomestic.Snapshot()
		result.OfficialStatus = official.Status
		result.OfficialAsOf = official.AsOf
		result.OfficialCollectionMS = official.CollectionSpanMS
		result.OfficialReadyExchanges = official.ReadyExchangeCount
		result.OfficialExpected = official.ExpectedExchangeCount
		if official.AsOf > 0 {
			result.OfficialAgeMS = max(0, time.Now().UnixMilli()-int64(official.AsOf*1000))
		}
	}
	if s.runtime != nil {
		coverageCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		end := time.Now().Unix() - 1
		window, err := s.runtime.Coverage(coverageCtx, end-9, end)
		cancel()
		if err == nil {
			result.ExactWindowStatus = window.Status
			result.ExactWindowComplete = window.WindowComplete
			result.ExactWindowCoverageSec = window.CoverageSec
			result.ExactWindowGapSec = window.GapSeconds
		}
	}
	result.WarmReady = health.Running && health.DatabaseReady && health.RealtimeReady &&
		result.OfficialStatus == "LIVE" && result.OfficialExpected == 5 &&
		result.OfficialReadyExchanges == result.OfficialExpected && result.OfficialAgeMS <= 10_000 &&
		result.ExactWindowStatus == "CONFIRMED" && result.ExactWindowComplete &&
		result.ExactWindowCoverageSec >= 10 && result.ExactWindowGapSec == 0
	result.TrafficReady = result.WarmReady && state.Phase == DeploymentPhaseActive && state.WriterLease
	return result
}

func (s *Service) PrepareDeployment(ctx context.Context) error {
	if s == nil || s.deployment == nil {
		return errors.New("deployment control is unavailable")
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	if s.postgresRepository == nil || s.runtime == nil {
		return s.deployment.prepare()
	}
	phase := s.deployment.state().Phase
	if phase != DeploymentPhaseShadow && phase != DeploymentPhasePrepared && phase != DeploymentPhaseActive {
		return ErrDeploymentTransition
	}
	reused, err := s.reusePreparedDeploymentCheckpoint(ctx, phase)
	if err != nil {
		return err
	}
	if reused {
		return nil
	}
	if err := s.discardPreparedCheckpoint(ctx); err != nil {
		return fmt.Errorf("discard previous deployment checkpoint: %w", err)
	}
	prepareCtx, cancel := context.WithTimeout(ctx, deploymentPreparationLimit)
	defer cancel()
	return s.prepareDeploymentCheckpoint(prepareCtx, s.postgresRepository, phase)
}

func (s *Service) reusePreparedDeploymentCheckpoint(ctx context.Context, phase DeploymentPhase) (bool, error) {
	// An active rollback checkpoint is always rebuilt for a new rollout. This
	// prevents a controller from starting its freshness budget at the request
	// while the server silently reuses an older capture generation.
	if phase == DeploymentPhaseActive {
		return false, nil
	}
	p := s.deploymentCheckpoint
	if p == nil || p.capture == nil || p.aggregator == nil || time.Since(p.capture.started) >= deploymentCaptureMaxAge/2 {
		return false, nil
	}
	reusable, err := s.runtime.deploymentCaptureReusable(ctx, p.capture)
	if err != nil {
		return false, fmt.Errorf("validate prepared deployment capture: %w", err)
	}
	if !reusable {
		return false, nil
	}
	if err := s.deployment.prepare(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) prepareDeploymentCheckpoint(ctx context.Context, store deploymentHydrationStore, phase DeploymentPhase) error {
	p, err := s.runtime.prepareRuntimeCheckpoint(ctx, store)
	if err != nil {
		return err
	}
	// Rehearse the exact full-row refresh before releasing the old writer.
	// Leave five seconds of the handoff group for lease, replay, flush and route.
	probeCtx, cancelProbe := context.WithTimeout(ctx, 5*time.Second)
	_, probe, err := refreshRuntimeCheckpoint(probeCtx, p)
	cancelProbe()
	if err != nil {
		if detachErr := s.runtime.discardFailedRuntimeCheckpoint(ctx, p); detachErr != nil {
			err = errors.Join(err, fmt.Errorf("discard failed deployment checkpoint: %w", detachErr))
		}
		return err
	}
	if probe.RefreshMS > 5_000 {
		err = errors.New("canonical refresh exceeds the safe handoff preparation budget")
		if detachErr := s.runtime.discardFailedRuntimeCheckpoint(ctx, p); detachErr != nil {
			err = errors.Join(err, fmt.Errorf("discard failed deployment checkpoint: %w", detachErr))
		}
		return err
	}
	s.deploymentCheckpoint = p
	s.deploymentPreparedCapture.Store(p.capture)
	s.deploymentPrepared.Store(true)
	if phase == DeploymentPhaseActive {
		return nil
	}
	return s.deployment.prepare()
}

func (s *Service) PromoteDeployment(ctx context.Context) error {
	if s == nil || s.deployment == nil {
		return errors.New("deployment control is unavailable")
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	state := s.deployment.state()
	if state.Phase == DeploymentPhaseActive && state.WriterLease {
		if err := s.discardPreparedCheckpoint(ctx); err != nil {
			return fmt.Errorf("discard active rollback checkpoint: %w", err)
		}
		return s.deployment.promote(ctx)
	}
	status := s.DeploymentStatus()
	if !status.WarmReady {
		return errors.New("candidate is not warm: five-exchange official and WebSocket readiness are required")
	}
	if s.postgresRepository == nil || s.runtime == nil {
		return s.deployment.promote(ctx)
	}
	return s.promotePreparedDeployment(ctx, s.postgresRepository)
}

func (s *Service) promotePreparedDeployment(ctx context.Context, checkpointStore deploymentHydrationStore) error {
	var installedCapture *runtimeDeploymentCapture
	err := s.deployment.promoteRebased(ctx, func(handoffCtx context.Context) (rebaseErr error) {
		defer func() {
			if rebaseErr != nil {
				s.closePreparedCheckpoint()
			}
		}()
		// Keep cleanup/release/route time inside the controller's ten-second group.
		rebaseCtx, cancel := context.WithTimeout(handoffCtx, 7*time.Second)
		defer cancel()
		p := s.deploymentCheckpoint
		var state postgres.State
		var result DeploymentCheckpointResult
		var err error
		if err = s.runtime.markDeploymentSnapshot(rebaseCtx); err != nil {
			return err
		}
		if p == nil {
			// Compatibility with an older controller that did not pre-prepare
			// the outgoing slot for rollback. Demotion already began capture.
			var capture *runtimeDeploymentCapture
			if err = s.runtime.handoffOwnerTurn(rebaseCtx, func(*pendingFlush) error { capture = s.runtime.deploymentCapture; return nil }); err != nil {
				return err
			}
			if capture == nil {
				return errors.New("rollback checkpoint capture was not prepared")
			}
			p, err = buildRuntimeCheckpoint(rebaseCtx, checkpointStore, capture)
			if err != nil {
				return err
			}
			s.deploymentCheckpoint = p
			state = p.state
			if err = p.aggregator.ResetCheckpointDedupe(state.Watermarks, state.RecentIDs); err != nil {
				return err
			}
			result = DeploymentCheckpointResult{Protocol: 2, Rows: p.rows, RefreshMS: p.prepareDuration.Milliseconds(), AsOf: time.Now().Unix()}
		} else {
			state, result, err = refreshRuntimeCheckpoint(rebaseCtx, p)
			if err != nil {
				return err
			}
		}
		if err = s.runtime.installRuntimeCheckpoint(rebaseCtx, p, state, &result, s.deployment); err != nil {
			return err
		}
		installedCapture = p.capture
		s.resetDeploymentDerivedState()
		s.deploymentCheckpoint = nil
		s.deploymentPrepared.Store(false)
		s.deploymentPreparedCapture.Store(nil)
		s.deploymentCheckpointResult.Store(&result)
		return nil
	})
	if err != nil {
		return err
	}
	// The callback clears the consumed private checkpoint but keeps the exact
	// capture through buffered-write flush. A post-install failure can therefore
	// retry through the p == nil recovery path without losing in-flight events.
	// Detach only after ACTIVE is fully established and before acknowledging it.
	if err := s.runtime.detachExactDeploymentCapture(ctx, installedCapture); err != nil {
		return fmt.Errorf("discard installed deployment capture: %w", err)
	}
	return nil
}

func (s *Service) DemoteDeployment(ctx context.Context, expectedCaptureGeneration uint64) error {
	if s == nil || s.deployment == nil {
		return errors.New("deployment control is unavailable")
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	if s.runtime == nil || s.postgresRepository == nil || s.deployment.state().Phase != DeploymentPhaseActive {
		return s.deployment.demote(ctx)
	}
	prepared := s.deploymentCheckpoint
	return s.runtime.handoffOwnerTurn(ctx, func(pending *pendingFlush) error {
		capture := s.runtime.deploymentCapture
		if prepared != nil {
			if expectedCaptureGeneration == 0 {
				return errors.New("prepared rollback demotion requires its capture generation")
			}
			if capture != prepared.capture || capture == nil {
				return errors.New("prepared rollback capture generation changed before demotion")
			}
			age := time.Since(capture.started)
			if capture.err != nil || age < 0 || age >= deploymentCaptureMaxAge/2 {
				return errors.New("prepared rollback capture is stale or exceeded its bound")
			}
			if capture.generation != expectedCaptureGeneration {
				return errors.New("prepared rollback capture generation does not match the controller")
			}
		} else {
			if expectedCaptureGeneration != 0 {
				return errors.New("controller expected a prepared rollback capture")
			}
			age := time.Duration(0)
			if capture != nil {
				age = time.Since(capture.started)
			}
			if capture == nil || capture.err != nil || age < 0 || age >= deploymentCaptureMaxAge/2 {
				capture = &runtimeDeploymentCapture{}
				s.runtime.installDeploymentCapture(capture)
			}
		}
		now := domain.UnixSecond(time.Now().Unix())
		before := max(now+1, s.runtime.aggregator.LatestBucketSecond()+1)
		if err := s.runtime.flushThrough(ctx, now, before, pending); err != nil {
			return err
		}
		return s.deployment.demote(ctx)
	})
}

func (s *Service) AbortDeployment(ctx context.Context) error {
	if s == nil || s.deployment == nil {
		return errors.New("deployment control is unavailable")
	}
	s.deploymentMu.Lock()
	defer s.deploymentMu.Unlock()
	if err := s.deployment.shadow(ctx); err != nil {
		return err
	}
	s.closePreparedCheckpoint()
	if s.runtime != nil {
		return s.runtime.handoffOwnerTurn(ctx, func(*pendingFlush) error { s.runtime.deploymentCapture = nil; return nil })
	}
	return nil
}

func (s *Service) closePreparedCheckpoint() {
	s.deploymentPrepared.Store(false)
	s.deploymentPreparedCapture.Store(nil)
	if p := s.deploymentCheckpoint; p != nil && p.aggregator != nil {
		p.aggregator.Close()
	}
	s.deploymentCheckpoint = nil
}

func (s *Service) discardPreparedCheckpoint(ctx context.Context) error {
	p := s.deploymentCheckpoint
	if p == nil {
		if s.runtime != nil {
			if err := s.runtime.discardCurrentDeploymentCapture(ctx); err != nil {
				return err
			}
		}
		s.deploymentPrepared.Store(false)
		s.deploymentPreparedCapture.Store(nil)
		return nil
	}
	if s.runtime == nil {
		if p.aggregator != nil {
			p.aggregator.Close()
			p.aggregator = nil
		}
		s.deploymentCheckpoint = nil
		s.deploymentPrepared.Store(false)
		s.deploymentPreparedCapture.Store(nil)
		return nil
	}
	if err := s.runtime.discardPreparedRuntimeCheckpoint(ctx, p); err != nil {
		return err
	}
	s.deploymentCheckpoint = nil
	s.deploymentPrepared.Store(false)
	s.deploymentPreparedCapture.Store(nil)
	return nil
}
