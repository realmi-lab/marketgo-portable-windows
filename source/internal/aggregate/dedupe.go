package aggregate

import (
	"strconv"

	"market-share-go/internal/domain"
)

type marketIdentity struct {
	exchange domain.Exchange
	market   string
}

type recentIdentities struct {
	queue          []recentIdentity
	set            map[string]struct{}
	latestObserved domain.UnixSecond
}

type recentIdentity struct {
	tradeID        string
	observedSecond domain.UnixSecond
}

const (
	exactIDRetentionSeconds domain.UnixSecond = 300
	maxExactIDsPerMarket                      = 131_072
)

type WatermarkRow struct {
	Exchange domain.Exchange
	Market   string
	TradeID  int64
}

type RecentIDRow struct {
	Exchange    domain.Exchange
	Market      string
	TradeID     string
	EventSecond domain.UnixSecond
}

// dirtyRecentIdentity is an append-only persistence journal entry. Claim has
// already proved the exact identity is new, so a second hierarchy of maps
// cannot improve dedupe correctness; it only repeats hashing and allocation
// on the trade hot path. bucketSecond remains separate from EventSecond
// because delayed trades must commit with their aggregation bucket while the
// five-minute restart horizon is based on observation time.
type dirtyRecentIdentity struct {
	bucketSecond domain.UnixSecond
	row          RecentIDRow
}

type Dedupe struct {
	maxIDs          int
	recent          map[marketIdentity]*recentIdentities
	legacyRecent    map[marketIdentity]map[string]struct{}
	watermarks      map[marketIdentity]int64
	dirtyWatermarks map[domain.UnixSecond]map[marketIdentity]int64
	dirtyRecent     []dirtyRecentIdentity
	drainWatermarks map[marketIdentity]int64
}

func NewDedupe(maxIDsPerMarket int) *Dedupe {
	if maxIDsPerMarket < 1 {
		maxIDsPerMarket = 1
	}
	return &Dedupe{
		maxIDs:          maxIDsPerMarket,
		recent:          make(map[marketIdentity]*recentIdentities),
		legacyRecent:    make(map[marketIdentity]map[string]struct{}),
		watermarks:      make(map[marketIdentity]int64),
		dirtyWatermarks: make(map[domain.UnixSecond]map[marketIdentity]int64),
		drainWatermarks: make(map[marketIdentity]int64),
	}
}

func (d *Dedupe) RestoreWatermark(exchange domain.Exchange, market string, tradeID int64) {
	key := marketIdentity{exchange: exchange, market: market}
	if !monotonicExchange(exchange) {
		// Older checkpoints used numeric high-water marks for exchanges whose
		// public contracts only guarantee a unique trade ID. Preserve the one
		// identity the checkpoint proves was observed without treating every
		// lower ID as a duplicate.
		ids := d.legacyRecent[key]
		if ids == nil {
			ids = make(map[string]struct{})
			d.legacyRecent[key] = ids
		}
		ids[strconv.FormatInt(tradeID, 10)] = struct{}{}
		return
	}
	if current, ok := d.watermarks[key]; !ok || tradeID > current {
		d.watermarks[key] = tradeID
	}
}

func (d *Dedupe) RestoreRecentID(exchange domain.Exchange, market, tradeID string, observedSecond domain.UnixSecond) {
	d.remember(marketIdentity{exchange: exchange, market: market}, tradeID, observedSecond)
}

func (d *Dedupe) Claim(event domain.TradeEvent, bucketSecond, observedSecond domain.UnixSecond) bool {
	key := marketIdentity{exchange: event.Exchange, market: event.Market}
	if monotonicExchange(event.Exchange) {
		if numericID, err := strconv.ParseInt(event.TradeID, 10, 64); err == nil {
			if watermark, exists := d.watermarks[key]; exists && numericID <= watermark {
				return false
			}
			d.watermarks[key] = numericID
			rows := d.dirtyWatermarks[bucketSecond]
			if rows == nil {
				rows = make(map[marketIdentity]int64)
				d.dirtyWatermarks[bucketSecond] = rows
			}
			if current, ok := rows[key]; !ok || numericID > current {
				rows[key] = numericID
			}
			return true
		}
	}
	if _, legacy := d.legacyRecent[key][event.TradeID]; legacy || !d.remember(key, event.TradeID, observedSecond) {
		return false
	}
	// Drain with the aggregation bucket so a persisted bucket and its dedupe
	// identity commit atomically. Store observation time in the row so restart
	// hydration can select the same five-minute in-memory dedupe horizon without
	// deleting older PostgreSQL history.
	d.dirtyRecent = append(d.dirtyRecent, dirtyRecentIdentity{
		bucketSecond: bucketSecond,
		row: RecentIDRow{
			Exchange: event.Exchange, Market: event.Market,
			TradeID: event.TradeID, EventSecond: observedSecond,
		},
	})
	return true
}

func (d *Dedupe) Seen(event domain.TradeEvent) bool {
	key := marketIdentity{exchange: event.Exchange, market: event.Market}
	if monotonicExchange(event.Exchange) {
		if numericID, err := strconv.ParseInt(event.TradeID, 10, 64); err == nil {
			watermark, exists := d.watermarks[key]
			return exists && numericID <= watermark
		}
	}
	state := d.recent[key]
	if state != nil {
		if _, exists := state.set[event.TradeID]; exists {
			return true
		}
	}
	_, exists := d.legacyRecent[key][event.TradeID]
	return exists
}

func (d *Dedupe) SeenAt(event domain.TradeEvent, observedSecond domain.UnixSecond) bool {
	if !monotonicExchange(event.Exchange) {
		d.prepareRecent(marketIdentity{exchange: event.Exchange, market: event.Market}, observedSecond)
	}
	return d.Seen(event)
}

func (d *Dedupe) remember(key marketIdentity, tradeID string, observedSecond domain.UnixSecond) bool {
	state := d.prepareRecent(key, observedSecond)
	if _, exists := state.set[tradeID]; exists {
		return false
	}
	state.queue = append(state.queue, recentIdentity{tradeID: tradeID, observedSecond: state.latestObserved})
	state.set[tradeID] = struct{}{}
	for len(state.queue) > d.maxIDs {
		dropOldestRecent(state)
	}
	return true
}

func (d *Dedupe) prepareRecent(key marketIdentity, observedSecond domain.UnixSecond) *recentIdentities {
	state := d.recent[key]
	if state == nil {
		state = &recentIdentities{set: make(map[string]struct{})}
		d.recent[key] = state
	}
	if observedSecond > state.latestObserved {
		state.latestObserved = observedSecond
	}
	cutoff := state.latestObserved - exactIDRetentionSeconds
	for len(state.queue) > 0 && state.queue[0].observedSecond < cutoff {
		dropOldestRecent(state)
	}
	return state
}

func dropOldestRecent(state *recentIdentities) {
	oldest := state.queue[0]
	// Clear the string before advancing the slice. A slice can keep its backing
	// array alive after the logical head moves, so leaving tradeID populated
	// retains expired identifiers until a future append reallocates the array.
	state.queue[0] = recentIdentity{}
	state.queue = state.queue[1:]
	if len(state.queue) == 0 {
		// Release a possibly large burst allocation immediately when the market
		// becomes idle after all exact identities expire.
		state.queue = nil
	}
	delete(state.set, oldest.tradeID)
}

func (d *Dedupe) DrainBefore(current domain.UnixSecond) ([]WatermarkRow, []RecentIDRow) {
	return d.DrainBeforeInto(current, nil, nil)
}

// DrainBeforeInto appends closed dedupe rows into caller-owned reusable
// buffers. Runtime persistence uses this path once per second to avoid
// allocating two proportional slices on every healthy PostgreSQL flush.
func (d *Dedupe) DrainBeforeInto(
	current domain.UnixSecond,
	watermarkRows []WatermarkRow,
	recentRows []RecentIDRow,
) ([]WatermarkRow, []RecentIDRow) {
	watermarkRows = watermarkRows[:0]
	recentRows = recentRows[:0]
	clear(d.drainWatermarks)
	for second, rows := range d.dirtyWatermarks {
		if second >= current {
			continue
		}
		for key, tradeID := range rows {
			if existing, ok := d.drainWatermarks[key]; !ok || tradeID > existing {
				d.drainWatermarks[key] = tradeID
			}
		}
		delete(d.dirtyWatermarks, second)
	}
	for key, tradeID := range d.drainWatermarks {
		watermarkRows = append(watermarkRows, WatermarkRow{Exchange: key.exchange, Market: key.market, TradeID: tradeID})
	}
	retained := 0
	for index := range d.dirtyRecent {
		entry := d.dirtyRecent[index]
		if entry.bucketSecond < current {
			recentRows = append(recentRows, entry.row)
			continue
		}
		if retained != index {
			d.dirtyRecent[retained] = entry
		}
		retained++
	}
	// Clear drained string headers so an unusually large journal backing array
	// cannot keep trade identifiers alive after PostgreSQL has accepted them.
	clear(d.dirtyRecent[retained:])
	d.dirtyRecent = d.dirtyRecent[:retained]
	return watermarkRows, recentRows
}

func monotonicExchange(exchange domain.Exchange) bool {
	// Korbit documents tradeId ordering per currency pair. Other exchanges'
	// numeric IDs are identities only: arrival order can differ from numeric
	// order, so they must use the bounded exact-ID path above.
	return exchange == domain.ExchangeKorbit
}
