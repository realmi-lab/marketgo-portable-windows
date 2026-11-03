package app

import (
	"context"
	"time"

	"market-share-go/internal/store/postgres"
)

// The monotonic projection cannot follow a forward wall-clock jump while the
// process lives. PostgreSQL checks this clock independently before any cleanup.
func (s *Service) monitorIngestionMaintenance(ctx context.Context, restored <-chan struct{}) {
	select {
	case <-restored:
	case <-ctx.Done():
		return
	}
	started := time.Now()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	var schedule ingestionMaintenanceSchedule
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			now := started.Add(time.Since(started))
			if ingestionMaintenanceShouldYield(now, s.runtime.Metrics()) {
				timer.Reset(time.Second)
				continue
			}
			lane, due := schedule.take(now)
			if !due {
				timer.Reset(time.Second)
				continue
			}
			snapshots := lane != 0
			var result postgres.IngestionMaintenanceResult
			var err error
			if snapshots {
				result, err = s.postgresRepository.MaintainLatestSnapshots(ctx, now, lane == 1)
			} else {
				result, err = s.postgresRepository.MaintainIngestion(ctx, now, false)
			}
			if err != nil && ctx.Err() == nil {
				s.logger.Warn("ingestion maintenance deferred", "snapshots", snapshots, "error", err)
			} else if result.Archived > 0 {
				s.logger.Info("ingestion maintenance progress", "snapshots", snapshots, "result", result)
			}
			schedule.finish(lane, started.Add(time.Since(started)), err != nil)
			ticks++
			if err == nil && ticks%10 == 0 {
				if _, err := s.postgresRepository.PruneCompactIngestion(ctx); err != nil && ctx.Err() == nil {
					s.logger.Warn("compact ingestion cleanup deferred", "error", err)
				}
			}
			timer.Reset(time.Second)
		}
	}
}

// Cleanup uses the same database as live commits. A successful unrelated lane
// must not immediately spend the capacity that ingestion needs after a timeout.
// Keep per-lane retry/fairness state intact while yielding to live persistence.
func ingestionMaintenanceShouldYield(now time.Time, metrics RuntimeMetrics) bool {
	return metrics.LastFlush.IsZero() || now.Sub(metrics.LastFlush) > 5*time.Second ||
		(!metrics.LastFlushErrorAt.IsZero() && now.Sub(metrics.LastFlushErrorAt) < 30*time.Second)
}

// Frame, recent-snapshot and full-snapshot maintenance have independent retry
// state. Success in one must not erase another's repeated database timeouts.
// A healthy lane remains no more frequent than the original three-turn cycle.
type ingestionMaintenanceSchedule struct {
	next  [3]time.Time
	delay [3]time.Duration
	turn  int
}

func (s *ingestionMaintenanceSchedule) take(now time.Time) (int, bool) {
	for offset := 0; offset < len(s.next); offset++ {
		lane := (s.turn + offset) % len(s.next)
		if !now.Before(s.next[lane]) {
			s.turn = (lane + 1) % len(s.next)
			return lane, true
		}
	}
	return 0, false
}

func (s *ingestionMaintenanceSchedule) finish(lane int, now time.Time, failed bool) {
	s.delay[lane] = ingestionMaintenanceDelay(s.delay[lane], failed)
	s.next[lane] = now.Add(max(3*time.Second, s.delay[lane]))
}

// Yield to live persistence after pressure; never catch up missed timer ticks.
func ingestionMaintenanceDelay(previous time.Duration, failed bool) time.Duration {
	if failed {
		return min(30*time.Second, max(time.Second, previous)*2)
	}
	return max(time.Second, previous/2)
}
