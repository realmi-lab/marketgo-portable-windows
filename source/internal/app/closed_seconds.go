package app

import (
	"context"
	"errors"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

// ErrUnsupportedClosedSecondsWindow: the recent closed seconds come for the
// windows of a minute or less, whose reads stay small.
var ErrUnsupportedClosedSecondsWindow = errors.New("window must be 10, 30 or 60 seconds")

// ClosedSecondsCount is how many recent closed seconds ClosedSeconds lists:
// the two minutes the live charts show.
const ClosedSecondsCount = 120

// ClosedSeconds lists, oldest first, the window of `window` seconds ending
// at each of the last ClosedSecondsCount closed seconds, as each realtime
// tick's ClosedSecond gives it, so a live chart opens with its recent past.
// Each second is read once and the windows are added up from the seconds,
// so the read stays the same small size however many windows overlap.
func (s *Service) ClosedSeconds(ctx context.Context, window int64, marketScope string) ([]TickClosedSecond, error) {
	if window != 10 && window != 30 && window != 60 {
		return nil, ErrUnsupportedClosedSecondsWindow
	}
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return nil, ErrUnsupportedMarketScope
	}
	if s.runtime == nil {
		return nil, ErrStopped
	}
	last := time.Now().Unix() - 1
	first := last - ClosedSecondsCount - window + 2 // the first window's first second
	count := int(last - first + 1)
	intervals := make([]aggregate.IntervalRequest, count)
	for index := range intervals {
		second := first + int64(index)
		intervals[index] = aggregate.IntervalRequest{Start: domain.UnixSecond(second - 1), End: domain.UnixSecond(second)}
	}
	coverage := make([]coverageInterval, ClosedSecondsCount)
	for index := range coverage {
		end := last - int64(ClosedSecondsCount-1-index)
		coverage[index] = coverageInterval{start: end - window + 1, end: end}
	}
	read, err := s.runtime.atomicReadIntervals(ctx, intervals, coverage)
	if err != nil {
		return nil, err
	}
	if len(read.intervals) != count || len(read.coverage) != ClosedSecondsCount {
		return nil, errors.New("closed seconds read is incomplete")
	}
	seconds := make([]domesticValuesSnapshot, count)
	for index, interval := range read.intervals {
		if !interval.OK {
			return nil, errors.New("closed seconds read is incomplete")
		}
		seconds[index] = aggregateDomesticValuesForScope(interval.Values, marketScope)
	}
	result := make([]TickClosedSecond, ClosedSecondsCount)
	for index := range result {
		// Window index ends at second index + window - 1 of the read.
		sum := domesticValuesSnapshot{byExchange: make(map[domain.Exchange]domesticExchangeValues), valid: true}
		for _, second := range seconds[index : index+int(window)] {
			sum.add(second)
		}
		observed := read.coverage[index]
		result[index] = TickClosedSecond{
			AsOfSecond: last - int64(ClosedSecondsCount-1-index), WindowComplete: observed.WindowComplete,
			CoverageSec: min(window, observed.CoverageSec), Exchanges: tickExchanges(sum),
		}
	}
	return result, nil
}

// add adds another stretch's domestic values; the sum is valid while both are.
func (v *domesticValuesSnapshot) add(other domesticValuesSnapshot) {
	// Every add runs: an overflow marks the sum invalid but does not stop it.
	valid := v.valid && other.valid
	if !addFiniteVolume(&v.totalVolume, other.totalVolume) {
		valid = false
	}
	if !addFiniteVolume(&v.bithumbVolume, other.bithumbVolume) {
		valid = false
	}
	if !addUint64(&v.trades, other.trades) {
		valid = false
	}
	if !addUint64(&v.unresolved, other.unresolved) {
		valid = false
	}
	for exchange, values := range other.byExchange {
		sum := v.byExchange[exchange]
		if !addFiniteVolume(&sum.volume, values.volume) {
			valid = false
		}
		if !addUint64(&sum.trades, values.trades) {
			valid = false
		}
		if !addUint64(&sum.unresolved, values.unresolved) {
			valid = false
		}
		v.byExchange[exchange] = sum
	}
	v.valid = valid
}
