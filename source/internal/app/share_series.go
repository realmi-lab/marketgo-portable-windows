package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
)

// ErrUnsupportedShareSeriesBucket: share series come in minutes, ten minutes
// and half hours only; hours are the official aligned history.
var ErrUnsupportedShareSeriesBucket = errors.New("bucket_sec must be 60, 600 or 1800")

// shareSeriesLengths is how many closed buckets each bucket size lists: two
// hours of minutes, a day of ten minutes or of half hours.
var shareSeriesLengths = map[int64]int{60: 120, 600: 144, 1_800: 48}

const (
	// A bucket read this long after it ended is settled. Newer ones are read
	// again. Later accepted trades or gap corrections invalidate only the
	// affected settled buckets through the runtime's correction frontier.
	shareSeriesSettleSec = 180
	// One read covers at most an hour of buckets, so filling a day does not
	// hold up the journal for long.
	shareSeriesReadSec = 3_600
)

// ShareSeries lists Bithumb's share in each of the last closed buckets of
// bucketSec seconds. Settled buckets are kept between calls, so each call
// reads only buckets that are new, still settling, or explicitly corrected.
func (s *Service) ShareSeries(ctx context.Context, bucketSec int64, marketScope string) (marketanalysis.ShareSeries, error) {
	length, ok := shareSeriesLengths[bucketSec]
	if !ok {
		return marketanalysis.ShareSeries{}, ErrUnsupportedShareSeriesBucket
	}
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return marketanalysis.ShareSeries{}, ErrUnsupportedMarketScope
	}
	if s.runtime == nil {
		return marketanalysis.ShareSeries{}, ErrStopped
	}
	s.shareSeries.mu.Lock()
	defer s.shareSeries.mu.Unlock()
	// The correction frontier uses the same trade/coverage barrier as bucket
	// reads, so an admitted late trade cannot remain hidden by a settled cache.
	frontier, err := s.runtime.atomicReadSelectedWithCorrections(ctx, nil, nil, nil, false, &s.shareSeries.revision)
	if err != nil {
		return marketanalysis.ShareSeries{}, err
	}
	newest := time.Now().Unix() - 1
	if s.runtime.ingestion != nil {
		newest = min(newest, frontier.journalHealthSecond)
	}
	s.shareSeries.applyCorrections(frontier.shareSeriesCorrections)
	end := (newest+1)/bucketSec*bucketSec - 1
	first := end - int64(length)*bucketSec + 1

	kept := s.shareSeries.buckets(shareSeriesKey{scope: marketScope, bucketSec: bucketSec})
	var due []int64
	for start := first; start <= end; start += bucketSec {
		if bucket, found := kept[start]; !found || bucket.readAt-(start+bucketSec-1) < shareSeriesSettleSec {
			due = append(due, start)
		}
	}
	perRead := max(1, int(shareSeriesReadSec/bucketSec))
	for from := 0; from < len(due); from += perRead {
		if err := s.readShareSeriesBuckets(ctx, kept, due[from:min(from+perRead, len(due))], bucketSec, marketScope, newest); err != nil {
			return marketanalysis.ShareSeries{}, err
		}
	}
	result := marketanalysis.ShareSeries{
		MarketScope: marketScope, BucketSec: bucketSec, AsOfSecond: end,
		Points: make([]marketanalysis.ShareSeriesPoint, 0, length),
	}
	for start := range kept {
		if start < first {
			delete(kept, start)
		}
	}
	for start := first; start <= end; start += bucketSec {
		result.Points = append(result.Points, kept[start].point)
	}
	return result, nil
}

// readShareSeriesBuckets reads the buckets starting at starts in one read.
func (s *Service) readShareSeriesBuckets(
	ctx context.Context, kept map[int64]shareSeriesBucket,
	starts []int64, bucketSec int64, marketScope string, newest int64,
) error {
	intervals := make([]aggregate.IntervalRequest, len(starts))
	coverage := make([]coverageInterval, len(starts))
	for index, start := range starts {
		last := start + bucketSec - 1
		intervals[index] = aggregate.IntervalRequest{Start: domain.UnixSecond(start - 1), End: domain.UnixSecond(last)}
		coverage[index] = coverageInterval{start: start, end: last}
	}
	read, err := s.runtime.atomicReadIntervals(ctx, intervals, coverage)
	if err != nil {
		return err
	}
	if len(read.intervals) != len(starts) || len(read.coverage) != len(starts) {
		return errors.New("share series read is incomplete")
	}
	for index, start := range starts {
		if !read.intervals[index].OK {
			return errors.New("share series read is incomplete")
		}
		observed := read.coverage[index]
		rows := analysisRows(filterAnalysisValues(read.intervals[index].Values, marketScope))
		kept[start] = shareSeriesBucket{
			point:  marketanalysis.NewShareSeriesPoint(start, rows, observed.WindowComplete, min(bucketSec, observed.CoverageSec)),
			readAt: newest,
		}
	}
	return nil
}

type shareSeriesKey struct {
	scope     string
	bucketSec int64
}

// shareSeriesBucket is a bucket's point and the newest applied second when
// it was read.
type shareSeriesBucket struct {
	point  marketanalysis.ShareSeriesPoint
	readAt int64
}

// shareSeriesMemo keeps the buckets of each scope and size. Its lock also
// lets one caller at a time read, so concurrent callers share the reads.
type shareSeriesMemo struct {
	mu       sync.Mutex
	kept     map[shareSeriesKey]map[int64]shareSeriesBucket
	revision uint64
}

func (m *shareSeriesMemo) buckets(key shareSeriesKey) map[int64]shareSeriesBucket {
	if m.kept == nil {
		m.kept = make(map[shareSeriesKey]map[int64]shareSeriesBucket)
	}
	if m.kept[key] == nil {
		m.kept[key] = make(map[int64]shareSeriesBucket)
	}
	return m.kept[key]
}
