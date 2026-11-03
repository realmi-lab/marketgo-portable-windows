package aggregate

import "market-share-go/internal/domain"

// hotSecondRetentionSeconds bounds the map-heavy exact-second working set.
// Older rows remain exact, but move into compact minute-owned slices. This
// preserves arbitrary second-boundary queries while avoiding one Go map per
// populated second across the 48-hour comparison horizon.
const hotSecondRetentionSeconds domain.UnixSecond = 10 * 60

type compactBucketRow struct {
	keyID  volumeKeyID
	totals Totals
}

type compactMinute struct {
	seconds [60][]compactBucketRow
}

func (a *Aggregator) compactRows(second domain.UnixSecond) []compactBucketRow {
	block := a.coldMinutes[blockStart(second, minuteBlockSeconds)]
	if block == nil {
		return nil
	}
	return block.seconds[int(second-blockStart(second, minuteBlockSeconds))]
}

func (a *Aggregator) retainedTotal(second domain.UnixSecond, keyID volumeKeyID) (Totals, bool) {
	if rows := a.buckets[second]; rows != nil {
		value, found := rows[keyID]
		return value, found
	}
	for _, row := range a.compactRows(second) {
		if row.keyID == keyID {
			return row.totals, true
		}
	}
	return Totals{}, false
}

func (a *Aggregator) setRetainedTotal(second domain.UnixSecond, keyID volumeKeyID, totals Totals, compact bool) {
	if !compact {
		rows := a.buckets[second]
		if rows == nil {
			a.recordBucketSecond(second)
			rows = make(map[volumeKeyID]Totals)
			a.buckets[second] = rows
		}
		rows[keyID] = totals
		return
	}
	start := blockStart(second, minuteBlockSeconds)
	block := a.coldMinutes[start]
	offset := int(second - start)
	var rows []compactBucketRow
	if block != nil {
		rows = block.seconds[offset]
	}
	for index := range rows {
		if rows[index].keyID == keyID {
			rows[index].totals = totals
			return
		}
	}
	if len(rows) == 0 {
		a.recordBucketSecond(second)
	}
	if block == nil {
		block = &compactMinute{}
		a.coldMinutes[start] = block
	}
	block.seconds[offset] = append(rows, compactBucketRow{keyID: keyID, totals: totals})
}

func (a *Aggregator) forEachRetainedRow(second domain.UnixSecond, visit func(volumeKeyID, Totals)) {
	if rows := a.buckets[second]; rows != nil {
		for keyID, totals := range rows {
			visit(keyID, totals)
		}
		return
	}
	for _, row := range a.compactRows(second) {
		visit(row.keyID, row.totals)
	}
}

func (a *Aggregator) addInternedSecond(target map[volumeKeyID]Totals, second domain.UnixSecond) {
	a.forEachRetainedRow(second, func(keyID volumeKeyID, value Totals) {
		total := target[keyID]
		total.Add(value)
		target[keyID] = total
	})
}

func (a *Aggregator) addMaterializedSecond(target map[VolumeKey]Totals, second domain.UnixSecond) {
	a.forEachRetainedRow(second, func(keyID volumeKeyID, value Totals) {
		key := a.volumeKey(keyID)
		total := target[key]
		total.Add(value)
		target[key] = total
	})
}

func (a *Aggregator) materializeSecond(second domain.UnixSecond, target map[VolumeKey]Totals) map[VolumeKey]Totals {
	clear(target)
	a.forEachRetainedRow(second, func(keyID volumeKeyID, totals Totals) {
		target[a.volumeKey(keyID)] = totals
	})
	return target
}

func (a *Aggregator) compactHotBucketsThrough(cutoff domain.UnixSecond) {
	// Only hot maps can require packing. Scanning the retained-second index
	// revisits up to 48 hours of already-compacted rows on every owner tick.
	// Inspecting the maps also covers restored or delayed hot rows without a
	// separate compaction watermark that could skip them.
	for second, rows := range a.buckets {
		if second > cutoff || len(rows) == 0 {
			continue
		}
		start := blockStart(second, minuteBlockSeconds)
		block := a.coldMinutes[start]
		if block == nil {
			block = &compactMinute{}
			a.coldMinutes[start] = block
		}
		offset := int(second - start)
		packed := make([]compactBucketRow, 0, len(rows))
		for keyID, totals := range rows {
			packed = append(packed, compactBucketRow{keyID: keyID, totals: totals})
		}
		block.seconds[offset] = packed
		delete(a.buckets, second)
	}
}

func (a *Aggregator) deleteRetainedSecond(second domain.UnixSecond) {
	delete(a.buckets, second)
	start := blockStart(second, minuteBlockSeconds)
	block := a.coldMinutes[start]
	if block == nil {
		return
	}
	block.seconds[int(second-start)] = nil
	for _, rows := range block.seconds {
		if len(rows) > 0 {
			return
		}
	}
	delete(a.coldMinutes, start)
}

func (a *Aggregator) retainedSecondCount() int {
	count := len(a.buckets)
	for _, block := range a.coldMinutes {
		for _, rows := range block.seconds {
			if len(rows) > 0 {
				count++
			}
		}
	}
	return count
}

func (a *Aggregator) hasRetainedSecond(second domain.UnixSecond) bool {
	return len(a.buckets[second]) > 0 || len(a.compactRows(second)) > 0
}
