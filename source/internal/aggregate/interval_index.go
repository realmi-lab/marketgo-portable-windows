package aggregate

import (
	"math"
	"sort"

	"market-share-go/internal/domain"
)

const (
	minuteBlockSeconds domain.UnixSecond = 60
	hourBlockSeconds   domain.UnixSecond = 60 * minuteBlockSeconds
	// Each window and its previous comparison add up to two partial boundary
	// hours of minute blocks. Twelve default windows touch at most about 530
	// distinct boundary minutes per second; a smaller limit evicted blocks that
	// the same second needed again and rebuilt them from seconds. Measured with
	// 800-1,500 markets trading every minute, the limit of 256 kept 21-41 MB but
	// cost 62-135 ms and 64-127 MB of allocation per read of every window; 640
	// keeps 32-64 MB at 27-46 ms and 3-6 MB. Keep headroom without retaining
	// all 2,880 minute value maps.
	minuteValueCacheLimit = 640
	// Covers every aligned hour of the 48-hour retention horizon for the
	// current and previous intervals of every default window.
	hourValueCacheLimit = 128
)

// intervalBlock is a derived cache. populatedSeconds tracks authoritative
// second buckets; values can always be discarded and rebuilt from those
// buckets. Rebuilds use a fixed chronological hierarchy, so a delayed trade
// cannot make query results depend on Go map iteration or arrival order.
type intervalBlock struct {
	values           map[volumeKeyID]Totals
	populatedSeconds int
	dirty            bool
}

// intervalValueCache bounds only materialized aggregate maps. Block metadata
// and exact second buckets stay authoritative for the full retention horizon.
// Deterministic round-robin eviction is sufficient because eviction affects
// cost, never results.
type intervalValueCache struct {
	slots []*intervalBlock
	next  int
}

func (c *intervalValueCache) admit(block *intervalBlock, limit int) {
	if block == nil || limit <= 0 {
		panic("aggregate: invalid interval value cache admission")
	}
	firstEmpty := -1
	for index, cached := range c.slots {
		if cached == block {
			return
		}
		if cached == nil && firstEmpty < 0 {
			firstEmpty = index
		}
	}
	if firstEmpty >= 0 {
		c.slots[firstEmpty] = block
		return
	}
	if len(c.slots) < limit {
		c.slots = append(c.slots, block)
		return
	}
	if evicted := c.slots[c.next]; evicted != nil {
		evicted.values = nil
		evicted.dirty = true
	}
	c.slots[c.next] = block
	c.next++
	if c.next == len(c.slots) {
		c.next = 0
	}
}

func (c *intervalValueCache) release(block *intervalBlock) {
	if block == nil {
		return
	}
	block.values = nil
	block.dirty = true
	for index, cached := range c.slots {
		if cached == block {
			c.slots[index] = nil
			return
		}
	}
}

func (c *intervalValueCache) reset() {
	for _, block := range c.slots {
		if block != nil {
			block.values = nil
			block.dirty = true
		}
	}
	c.slots = nil
	c.next = 0
}

func cloneTotalsMap(values map[VolumeKey]Totals) map[VolumeKey]Totals {
	result := make(map[VolumeKey]Totals, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

// populatedRangeExclusive intersects (start,end] with the authoritative
// sparse second index without evaluating start+1, which would overflow at the
// upper int64 boundary.
func populatedRangeExclusive(
	seconds []domain.UnixSecond,
	start, end domain.UnixSecond,
) (domain.UnixSecond, domain.UnixSecond, bool) {
	if len(seconds) == 0 || end <= start {
		return 0, 0, false
	}
	first := sort.Search(len(seconds), func(index int) bool { return seconds[index] > start })
	last := sort.Search(len(seconds), func(index int) bool { return seconds[index] > end })
	if first >= last {
		return 0, 0, false
	}
	return seconds[first], seconds[last-1], true
}

func (a *Aggregator) accumulateHourRange(
	first, last domain.UnixSecond,
	target map[volumeKeyID]Totals,
) {
	fullFirst, fullLast, ok := fullBlockRange(first, last, hourBlockSeconds)
	if !ok {
		a.accumulateMinuteRange(first, last, target)
		return
	}
	if first < fullFirst {
		a.accumulateMinuteRange(first, fullFirst-1, target)
	}
	start, end := bucketSecondRange(a.hourStarts, fullFirst, fullLast)
	for _, blockStart := range a.hourStarts[start:end] {
		addInternedRows(target, a.cachedHourRows(blockStart))
	}
	tail := blockEnd(fullLast, hourBlockSeconds)
	if tail < last {
		a.accumulateMinuteRange(tail+1, last, target)
	}
}

func (a *Aggregator) accumulateMinuteRange(
	first, last domain.UnixSecond,
	target map[volumeKeyID]Totals,
) {
	fullFirst, fullLast, ok := fullBlockRange(first, last, minuteBlockSeconds)
	if !ok {
		a.accumulateSecondRange(first, last, target)
		return
	}
	if first < fullFirst {
		a.accumulateSecondRange(first, fullFirst-1, target)
	}
	start, end := bucketSecondRange(a.minuteStarts, fullFirst, fullLast)
	for _, blockStart := range a.minuteStarts[start:end] {
		addInternedRows(target, a.cachedMinuteRows(blockStart))
	}
	tail := blockEnd(fullLast, minuteBlockSeconds)
	if tail < last {
		a.accumulateSecondRange(tail+1, last, target)
	}
}

func (a *Aggregator) accumulateSecondRange(
	first, last domain.UnixSecond,
	target map[volumeKeyID]Totals,
) {
	for _, second := range a.populatedSeconds(first, last) {
		a.addInternedSecond(target, second)
	}
}

func addInternedRows(target, rows map[volumeKeyID]Totals) {
	for keyID, value := range rows {
		total := target[keyID]
		total.Add(value)
		target[keyID] = total
	}
}

func (a *Aggregator) cachedMinuteRows(start domain.UnixSecond) map[volumeKeyID]Totals {
	block := a.minuteBlocks[start]
	if block == nil {
		return nil
	}
	if !block.dirty {
		return block.values
	}
	if block.values == nil {
		a.minuteValueCache.admit(block, minuteValueCacheLimit)
		block.values = make(map[volumeKeyID]Totals)
	} else {
		clear(block.values)
	}
	for _, second := range a.populatedSeconds(start, blockEnd(start, minuteBlockSeconds)) {
		a.addInternedSecond(block.values, second)
	}
	block.dirty = false
	return block.values
}

func (a *Aggregator) cachedHourRows(start domain.UnixSecond) map[volumeKeyID]Totals {
	block := a.hourBlocks[start]
	if block == nil {
		return nil
	}
	if !block.dirty {
		return block.values
	}
	if block.values == nil {
		a.hourValueCache.admit(block, hourValueCacheLimit)
		block.values = make(map[volumeKeyID]Totals)
	} else {
		clear(block.values)
	}
	// Build hours directly from authoritative seconds. Building through minute
	// values would populate every one of the 2,880 minute maps in a 48-hour
	// query even though those maps are not otherwise needed.
	for _, second := range a.populatedSeconds(start, blockEnd(start, hourBlockSeconds)) {
		a.addInternedSecond(block.values, second)
	}
	block.dirty = false
	return block.values
}

func (a *Aggregator) invalidateIntervalBlocks(second domain.UnixSecond) {
	minuteStart := blockStart(second, minuteBlockSeconds)
	hourStart := blockStart(second, hourBlockSeconds)
	minute := a.minuteBlocks[minuteStart]
	hour := a.hourBlocks[hourStart]
	if minute == nil || hour == nil {
		// This is only reachable after package-local state replacement. Rebuild
		// from the authoritative bucket map rather than accepting a stale cache.
		a.rebuildSparseIndexes()
		minute = a.minuteBlocks[minuteStart]
		hour = a.hourBlocks[hourStart]
	}
	if minute != nil {
		minute.dirty = true
	}
	if hour != nil {
		hour.dirty = true
	}
}

func (a *Aggregator) recordIntervalBlockSecond(second domain.UnixSecond) {
	if a.minuteBlocks == nil {
		a.minuteBlocks = make(map[domain.UnixSecond]*intervalBlock)
	}
	if a.hourBlocks == nil {
		a.hourBlocks = make(map[domain.UnixSecond]*intervalBlock)
	}
	minuteStart := blockStart(second, minuteBlockSeconds)
	minute := a.minuteBlocks[minuteStart]
	if minute == nil {
		minute = &intervalBlock{dirty: true}
		a.minuteBlocks[minuteStart] = minute
		insertSortedSecond(&a.minuteStarts, minuteStart)
	}
	minute.populatedSeconds++
	minute.dirty = true

	hourStart := blockStart(second, hourBlockSeconds)
	hour := a.hourBlocks[hourStart]
	if hour == nil {
		hour = &intervalBlock{dirty: true}
		a.hourBlocks[hourStart] = hour
		insertSortedSecond(&a.hourStarts, hourStart)
	}
	hour.populatedSeconds++
	hour.dirty = true
}

func (a *Aggregator) forgetIntervalBlockSecond(second domain.UnixSecond) {
	if removed := forgetBlockSecond(a.minuteBlocks, blockStart(second, minuteBlockSeconds)); removed != nil {
		a.minuteValueCache.release(removed)
	}
	if removed := forgetBlockSecond(a.hourBlocks, blockStart(second, hourBlockSeconds)); removed != nil {
		a.hourValueCache.release(removed)
	}
}

func forgetBlockSecond(
	blocks map[domain.UnixSecond]*intervalBlock,
	start domain.UnixSecond,
) *intervalBlock {
	block := blocks[start]
	if block == nil || block.populatedSeconds <= 0 {
		panic("aggregate: sparse interval block population is inconsistent")
	}
	block.populatedSeconds--
	if block.populatedSeconds == 0 {
		delete(blocks, start)
		return block
	}
	block.dirty = true
	return nil
}

func (a *Aggregator) compactIntervalBlockStarts() {
	a.minuteStarts = retainBlockStarts(a.minuteStarts, a.minuteBlocks)
	a.hourStarts = retainBlockStarts(a.hourStarts, a.hourBlocks)
}

func retainBlockStarts(
	starts []domain.UnixSecond,
	blocks map[domain.UnixSecond]*intervalBlock,
) []domain.UnixSecond {
	kept := starts[:0]
	for _, start := range starts {
		if blocks[start] != nil {
			kept = append(kept, start)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func (a *Aggregator) rebuildSparseIndexes() {
	a.minuteValueCache.reset()
	a.hourValueCache.reset()
	seconds := make([]domain.UnixSecond, 0, a.retainedSecondCount())
	for second, rows := range a.buckets {
		if len(rows) == 0 {
			delete(a.buckets, second)
			delete(a.dirty, second)
			continue
		}
		seconds = append(seconds, second)
	}
	for start, block := range a.coldMinutes {
		if block == nil {
			delete(a.coldMinutes, start)
			continue
		}
		empty := true
		for offset, rows := range block.seconds {
			if len(rows) == 0 {
				continue
			}
			empty = false
			seconds = append(seconds, start+domain.UnixSecond(offset))
		}
		if empty {
			delete(a.coldMinutes, start)
		}
	}
	sort.Slice(seconds, func(i, j int) bool { return seconds[i] < seconds[j] })
	a.bucketSeconds = seconds
	a.minuteBlocks = make(map[domain.UnixSecond]*intervalBlock)
	a.hourBlocks = make(map[domain.UnixSecond]*intervalBlock)
	a.minuteStarts = nil
	a.hourStarts = nil
	for _, second := range seconds {
		a.recordIntervalBlockSecond(second)
	}
}

func insertSortedSecond(values *[]domain.UnixSecond, value domain.UnixSecond) {
	seconds := *values
	if len(seconds) == 0 || value > seconds[len(seconds)-1] {
		*values = append(seconds, value)
		return
	}
	position := sort.Search(len(seconds), func(index int) bool { return seconds[index] >= value })
	if position < len(seconds) && seconds[position] == value {
		return
	}
	seconds = append(seconds, 0)
	copy(seconds[position+1:], seconds[position:])
	seconds[position] = value
	*values = seconds
}

func fullBlockRange(
	first, last, size domain.UnixSecond,
) (domain.UnixSecond, domain.UnixSecond, bool) {
	if last < first || size <= 0 {
		return 0, 0, false
	}
	fullFirst := blockStart(first, size)
	if fullFirst < first {
		if fullFirst > domain.UnixSecond(math.MaxInt64)-size {
			return 0, 0, false
		}
		fullFirst += size
	}
	fullLast := blockStart(last, size)
	if blockEnd(fullLast, size) > last {
		fullLast -= size
	}
	if fullLast < fullFirst {
		return 0, 0, false
	}
	return fullFirst, fullLast, true
}

func blockStart(second, size domain.UnixSecond) domain.UnixSecond {
	remainder := second % size
	if remainder < 0 {
		remainder += size
	}
	return second - remainder
}

func blockEnd(start, size domain.UnixSecond) domain.UnixSecond {
	width := size - 1
	if start > domain.UnixSecond(math.MaxInt64)-width {
		return domain.UnixSecond(math.MaxInt64)
	}
	return start + width
}
