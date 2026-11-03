package app

import (
	"context"
	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
	"market-share-go/internal/indexday"
	"time"
)

func (s *Service) RealtimeIndex(ctx context.Context, window int64, filter indexday.RealtimeFilter) (indexday.RealtimeResponse, error) {
	if !aggregate.SupportsWindow(window) {
		return indexday.RealtimeResponse{}, ErrUnsupportedWindow
	}
	if s == nil || s.runtime == nil {
		return indexday.RealtimeResponse{}, ErrStopped
	}
	end := time.Now().Unix() - 1
	read, err := s.runtime.atomicReadIntervals(ctx, []aggregate.IntervalRequest{{Start: domain.UnixSecond(end - window), End: domain.UnixSecond(end)}}, []coverageInterval{{start: end - window + 1, end: end}})
	if err != nil {
		return indexday.RealtimeResponse{}, err
	}
	if !read.intervals[0].OK {
		return indexday.RealtimeResponse{}, ErrUnsupportedWindow
	}
	observed := read.coverage[0]
	rows := make([]indexday.RealtimeRow, 0, len(read.intervals[0].Values))
	for key, value := range read.intervals[0].Values {
		if key.Scope != string(domain.ScopeDomestic) {
			continue
		}
		if filter.Quote != "all" && filter.Quote != "" && key.Quote != filter.Quote {
			continue
		}
		if filter.Venue != "all" && filter.Venue != "" && key.Exchange != filter.Venue {
			if filter.Venue != "combined" || (key.Exchange != "bithumb" && key.Exchange != "upbit") {
				continue
			}
		}
		if filter.FilterAssets && !filter.Assets[key.Base] {
			continue
		}
		var amount *float64
		if value.UnresolvedCount == 0 && value.Validate() == nil {
			v := value.KRWVolume
			amount = &v
		}
		rows = append(rows, indexday.RealtimeRow{Market: indexday.Market{Exchange: key.Exchange, Market: key.Market, Base: key.Base, Quote: key.Quote}, Amount: amount, Partial: !observed.WindowComplete})
	}
	grid := indexday.BuildRealtimeDomestic(rows)
	status := "COMPLETE"
	if !observed.WindowComplete || grid.Totals["domestic"].Partial {
		status = "PARTIAL"
	}
	return indexday.RealtimeResponse{Response: indexday.Response{
		ContractVersion: "REALTIME_INDEX_V1", StorageMode: "REALTIME_WINDOW", Day: time.Unix(end, 0).In(indexday.Seoul).Format(time.DateOnly), Start: end - window + 1, End: end + 1, Status: status,
		Membership: "OBSERVED_TRADES", Rows: []indexday.Row{}, Exchanges: []indexday.Exchange{}, Domestic: grid,
	}, WindowSec: window, AsOfSecond: end, WindowComplete: observed.WindowComplete, CoverageSec: min(window, observed.CoverageSec)}, nil
}
