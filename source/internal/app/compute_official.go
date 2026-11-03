package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	collectionserver "market-share-go/contracts/collection"
	"market-share-go/internal/domain"
	"market-share-go/internal/ingestlog"
	"market-share-go/internal/movement"
)

// persistIngestionOfficial processes every aligned observation in journal
// order, before acknowledging its cursor. Unlike a live latest-state channel,
// a durable backlog must not coalesce away historical hour samples.
func (s *Service) persistIngestionOfficial(ctx context.Context, events []ingestlog.Event, lastAsOfMS *int64) error {
	return s.persistIngestionOfficialWithCommit(ctx, events, lastAsOfMS, func(commitCtx context.Context, sample *movement.Snapshot, writes []movement.EventWrite) error {
		return s.deployment.dispatchActiveOnly(commitCtx, bufferedDeploymentWrite{name: "official_observation", run: func(writeCtx context.Context) error {
			return s.postgresRepository.CommitMovementObservation(writeCtx, sample, writes)
		}})
	})
}

func (s *Service) persistIngestionOfficialWithCommit(ctx context.Context, events []ingestlog.Event, lastAsOfMS *int64, commit movement.ObservationCommit) error {
	for _, event := range events {
		if event.Kind != "official" {
			continue
		}
		var payload collectionserver.OfficialPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		if payload.Source != "aligned" {
			continue
		}
		summary, err := payload.DecodeDomestic()
		if err != nil {
			return err
		}
		if s.movementTracker == nil || s.tickerTrend == nil {
			return errors.New("official history trackers are unavailable")
		}
		if summary.MissedUpdateCount > 0 {
			at := coalescedMovementGapAsOfMS(*summary, *lastAsOfMS)
			if err := s.movementTracker.ObserveGapAtomic(ctx, at, commit); err != nil && !errors.Is(err, movement.ErrNonMonotonicObservation) {
				return err
			}
			*lastAsOfMS = at
		}
		snapshot := movementSnapshot(*summary)
		if snapshot == nil {
			at := movementGapAsOfMS(*summary, *lastAsOfMS, time.Unix(0, event.ReceivedAtNS))
			if err := s.movementTracker.ObserveGapAtomic(ctx, at, commit); err != nil && !errors.Is(err, movement.ErrNonMonotonicObservation) {
				return err
			}
			*lastAsOfMS = at
			continue
		}
		if snapshot.AsOfMS <= *lastAsOfMS {
			continue
		}
		if err := s.tickerTrend.ObserveWithBithumbCloses(ctx, snapshot, summary.Exchanges[domain.ExchangeBithumb].ClosePricesKRW); err != nil {
			return err
		}
		if err := s.movementTracker.ObserveAtomic(ctx, snapshot, commit); err != nil && !errors.Is(err, movement.ErrNonMonotonicObservation) {
			return err
		}
		*lastAsOfMS = snapshot.AsOfMS
	}
	return nil
}
