package app

import (
	"context"
	"errors"
	"math"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/services/collector/official"
)

func (s *Service) monitorOfficialMovements(ctx context.Context, updates <-chan official.DomesticTickerSummary) {
	var lastAsOfMS int64
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case summary, ok := <-updates:
			if !ok {
				return
			}
			if summary.MissedUpdateCount > 0 {
				// The aligned-candle channel is intentionally latest-state. A
				// coalesced publication means the omitted state could have crossed
				// or reversed a movement threshold, so explicitly break continuity
				// before accepting the latest state as a new baseline.
				gapAsOfMS := coalescedMovementGapAsOfMS(summary, lastAsOfMS)
				if err := s.movementTracker.ObserveGap(ctx, gapAsOfMS); err != nil {
					if !errors.Is(err, movement.ErrNonMonotonicObservation) {
						s.logger.Warn("official movement coalescing gap failed", "error", err, "missed_updates", summary.MissedUpdateCount)
						continue
					}
				}
				lastAsOfMS = gapAsOfMS
			}
			snapshot := movementSnapshot(summary)
			if snapshot == nil {
				asOfMS := movementGapAsOfMS(summary, lastAsOfMS, time.Now())
				if err := s.movementTracker.ObserveGap(ctx, asOfMS); err != nil {
					if errors.Is(err, movement.ErrNonMonotonicObservation) {
						lastAsOfMS = asOfMS
						continue
					}
					s.logger.Warn("official movement gap failed", "error", err)
					continue
				}
				lastAsOfMS = asOfMS
				continue
			}
			if snapshot.AsOfMS <= lastAsOfMS {
				continue
			}
			if s.tickerTrend != nil {
				bithumbCloses := summary.Exchanges[domain.ExchangeBithumb].ClosePricesKRW
				if err := s.tickerTrend.ObserveWithBithumbCloses(ctx, snapshot, bithumbCloses); err != nil {
					s.logger.Warn("official aligned 1h sample failed", "error", err)
				}
			}
			if err := s.movementTracker.Observe(ctx, snapshot); err != nil {
				if errors.Is(err, movement.ErrNonMonotonicObservation) {
					lastAsOfMS = snapshot.AsOfMS
					continue
				}
				s.logger.Warn("official movement sample failed", "error", err)
				continue
			}
			lastAsOfMS = snapshot.AsOfMS
		}
	}
}

func coalescedMovementGapAsOfMS(summary official.DomesticTickerSummary, lastAsOfMS int64) int64 {
	if finiteMovementNumber(summary.AsOf) && summary.AsOf > 0 {
		currentAsOfMS := int64(summary.AsOf * 1_000)
		if currentAsOfMS > lastAsOfMS+1 {
			return currentAsOfMS - 1
		}
	}
	return lastAsOfMS + 1
}

func movementGapAsOfMS(summary official.DomesticTickerSummary, lastAsOfMS int64, observedAt time.Time) int64 {
	asOfMS := observedAt.UnixMilli()
	if finiteMovementNumber(summary.AsOf) && summary.AsOf > 0 {
		sourceAsOfMS := int64(summary.AsOf * 1_000)
		if sourceAsOfMS > asOfMS {
			asOfMS = sourceAsOfMS
		}
	}
	if asOfMS <= lastAsOfMS {
		return lastAsOfMS + 1
	}
	return asOfMS
}

func movementSnapshot(summary official.DomesticTickerSummary) *movement.Snapshot {
	if summary.Status != "LIVE" || summary.TotalVolumeKRW == nil ||
		summary.BithumbVolumeKRW == nil || summary.BithumbSharePercent == nil ||
		summary.AsOf <= 0 || !finiteMovementNumber(*summary.TotalVolumeKRW) ||
		!finiteMovementNumber(*summary.BithumbVolumeKRW) ||
		!finiteMovementNumber(*summary.BithumbSharePercent) ||
		*summary.TotalVolumeKRW <= 0 || *summary.BithumbVolumeKRW < 0 ||
		*summary.BithumbVolumeKRW > *summary.TotalVolumeKRW ||
		*summary.BithumbSharePercent < 0 || *summary.BithumbSharePercent > 100 {
		return nil
	}
	included := summary.IncludedExchanges
	if len(included) == 0 {
		included = append([]domain.Exchange(nil), domain.DomesticExchanges[:]...)
	}
	if len(included) < 2 {
		return nil
	}
	assets := make(map[movement.AssetKey]float64)
	exchanges := make(map[string]movement.ExchangeSnapshot, len(included))
	bithumbIncluded := false
	for _, exchange := range included {
		if exchange == domain.ExchangeBithumb {
			bithumbIncluded = true
		}
		row, found := summary.Exchanges[exchange]
		if !found || row.VolumeKRW == nil || !finiteMovementNumber(*row.VolumeKRW) ||
			*row.VolumeKRW < 0 || row.MarketCount < 1 {
			return nil
		}
		rowAssets := make(map[string]float64, len(row.Assets))
		for asset, volume := range row.Assets {
			if !finiteMovementNumber(volume) || volume < 0 {
				return nil
			}
			assets[movement.AssetKey{Exchange: string(exchange), Asset: asset}] = volume
			rowAssets[asset] = volume
		}
		exchanges[string(exchange)] = movement.ExchangeSnapshot{
			Exchange: string(exchange), Status: row.Status, Source: row.Source,
			VolumeKRW: *row.VolumeKRW, MarketCount: row.MarketCount,
			ExchangeTimestampMS: copyMovementInt(row.ExchangeTimestampMS), Assets: rowAssets,
		}
	}
	if !bithumbIncluded {
		return nil
	}
	return &movement.Snapshot{
		AsOfMS:           int64(summary.AsOf * 1_000),
		Status:           summary.Status,
		Source:           summary.Source,
		CollectionSpanMS: summary.CollectionSpanMS,
		TotalVolumeKRW:   *summary.TotalVolumeKRW,
		BithumbVolumeKRW: *summary.BithumbVolumeKRW,
		BithumbSharePct:  *summary.BithumbSharePercent,
		Exchanges:        exchanges,
		Assets:           assets,
	}
}

func finiteMovementNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func copyMovementInt(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Service) OfficialMovementState() movement.State {
	return filterMovementStateNotices(s.movementTracker.State())
}

func (s *Service) OfficialMovementEvents(ctx context.Context, limit int) ([]movement.Event, error) {
	events, err := s.repository.RecentMovementEventsBySource(ctx, official.Aligned24hSource, limit)
	if err != nil {
		return nil, err
	}
	for index := range events {
		events[index] = filterMovementEventNotices(events[index])
	}
	return events, nil
}

func (s *Service) OfficialMovementEvent(ctx context.Context, eventID string) (*movement.Event, error) {
	event, err := s.repository.MovementEvent(ctx, eventID)
	if err != nil || event == nil {
		return event, err
	}
	if event.Source != official.Aligned24hSource {
		return nil, movement.ErrEventNotFound
	}
	filtered := filterMovementEventNotices(*event)
	return &filtered, nil
}
