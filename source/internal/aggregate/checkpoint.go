package aggregate

import (
	"errors"
	"fmt"
	"math"

	"market-share-go/internal/domain"
)

// ReplaceCheckpointBucket applies an absolute canonical row to a private,
// unpublished checkpoint. Unlike RestoreBucket it never adds the old row
// twice. The caller advances once to current before refreshing a checkpoint.
// On any error the private checkpoint must be discarded, not published.
func (a *Aggregator) ReplaceCheckpointBucket(row Bucket, current domain.UnixSecond) (added, changed bool, err error) {
	if current <= 0 || row.Second <= 0 || row.Second <= current-domain.UnixSecond(a.retention)-5 {
		return false, false, errors.New("checkpoint row lies outside retained time")
	}
	id, known := a.identities[identityOf(row.Key)]
	if known && a.volumeKey(id) != row.Key {
		return false, false, errors.New("checkpoint changed a canonical market identity")
	}
	old, exists := a.retainedTotal(row.Second, id)
	if !known || !exists {
		if err := a.RestoreBucket(row, current); err != nil {
			return false, false, err
		}
		return true, true, nil
	}
	if old == row.Totals {
		return false, false, nil
	}
	if err := row.Totals.Validate(); err != nil {
		return false, false, err
	}
	clear(a.subtractScratch)
	a.subtractScratch[row.Key] = old
	for _, window := range a.storage {
		if row.Second > current-domain.UnixSecond(window) {
			if err := a.rolling.SubtractBatch(window, a.subtractScratch); err != nil {
				return false, false, fmt.Errorf("replace checkpoint rolling total: %w", err)
			}
		}
	}
	if err := a.rolling.Ingest(int64(row.Second), int64(current), row.Key, row.Totals); err != nil {
		return false, false, fmt.Errorf("replace checkpoint rolling total: %w", err)
	}
	a.setRetainedTotal(row.Second, id, row.Totals, row.Second <= current-hotSecondRetentionSeconds)
	a.invalidateIntervalBlocks(row.Second)
	return false, true, nil
}

// CheckpointRowCount is used with the refresh's seen and added row counts to
// detect deleted canonical rows. It does not allocate a second row-key index.
func (a *Aggregator) CheckpointRowCount(after domain.UnixSecond) int64 {
	var count int64
	for _, second := range a.populatedSeconds(after+1, domain.UnixSecond(math.MaxInt64)) {
		a.forEachRetainedRow(second, func(volumeKeyID, Totals) { count++ })
	}
	return count
}

// ResetCheckpointDedupe is only for a private checkpoint whose complete bucket
// refresh and ID rows came from the same database snapshot. It never merges
// unrelated ID ranges into a live aggregate.
func (a *Aggregator) ResetCheckpointDedupe(watermarks []WatermarkRow, recent []RecentIDRow) error {
	a.dedupe = NewDedupe(maxExactIDsPerMarket)
	for _, row := range watermarks {
		if err := a.RestoreWatermark(row); err != nil {
			return err
		}
	}
	for _, row := range recent {
		if err := a.RestoreRecentID(row); err != nil {
			return err
		}
	}
	return nil
}

// ContinueCheckpointVersion prevents an owner swap from reusing an analysis
// cache version even when the rebuilt aggregate has replayed very few trades.
func (a *Aggregator) ContinueCheckpointVersion(previous Metrics) error {
	if previous.Version == math.MaxUint64 {
		return errors.New("aggregation version exhausted")
	}
	version := previous.Version + 1
	if a.metrics.Version > version {
		version = a.metrics.Version
	}
	a.metrics = previous
	a.metrics.Version = version
	return nil
}
