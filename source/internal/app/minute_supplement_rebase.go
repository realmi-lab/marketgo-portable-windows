package app

import (
	"math"
	"sort"
	"strings"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
	"market-share-go/internal/reconcile"
)

func validSupplementMinute(row reconcile.MinuteSupplement, endSecond int64) bool {
	return exactMinuteExchange(canonicalSupplementExchange(row.Exchange)) && strings.TrimSpace(row.Market) != "" &&
		strings.EqualFold(strings.TrimSpace(row.Quote), "KRW") &&
		(row.IntervalSec == 0 || row.IntervalSec == reconcile.MinuteSeconds) && int64(row.MinuteStart)%reconcile.MinuteSeconds == 0 &&
		row.MinuteStart >= 0 && endSecond >= reconcile.MinuteSeconds-1 &&
		int64(row.MinuteStart) <= endSecond-(reconcile.MinuteSeconds-1) &&
		row.KnownGapSeconds > 0 && row.KnownGapSeconds <= reconcile.MinuteSeconds
}

type supplementMinuteIdentity struct {
	exchange domain.Exchange
	market   string
	start    domain.UnixSecond
}

type supplementObservedMarket struct {
	exchange domain.Exchange
	market   string
}

func canonicalSupplementExchange(exchange domain.Exchange) domain.Exchange {
	return domain.Exchange(strings.ToLower(strings.TrimSpace(string(exchange))))
}

func canonicalSupplementEvidence(row reconcile.MinuteSupplement) reconcile.MinuteSupplement {
	row.Exchange = canonicalSupplementExchange(row.Exchange)
	row.Market = strings.ToUpper(strings.TrimSpace(row.Market))
	row.Base = strings.ToUpper(strings.TrimSpace(row.Base))
	row.Quote = strings.ToUpper(strings.TrimSpace(row.Quote))
	row.Source = strings.TrimSpace(row.Source)
	if row.IntervalSec == 0 {
		row.IntervalSec = reconcile.MinuteSeconds
	}
	// These are stale derived values, not the original official evidence.
	// The immutable current minute below replaces them for every retry.
	row.WSVolumeKRW = 0
	row.SupplementalVolumeKRW = 0
	row.ReplacementAdjustmentKRW = 0
	return row
}

// Reconciliation may predate late trades, journal replay or a writer handoff.
// Subtract the actual market-minute in the same immutable read used by every
// dashboard total. Never add a stale persisted residual on top of newer trades.
func rebaseMinuteSupplements(view analysisReadView, rows []reconcile.MinuteSupplement, endSecond int64) ([]reconcile.MinuteSupplement, int) {
	// Retain row indexes rather than copying every full evidence row into a map.
	unique := make(map[supplementMinuteIdentity]int, len(rows))
	rejected := make(map[supplementMinuteIdentity]bool)
	for index, row := range rows {
		row = canonicalSupplementEvidence(row)
		key := supplementMinuteIdentity{exchange: row.Exchange, market: row.Market, start: row.MinuteStart}
		if previous, exists := unique[key]; exists && canonicalSupplementEvidence(rows[previous]) != row {
			rejected[key] = true
		}
		unique[key] = index
		// Reject the identity before dropping a zero/negative residual. Otherwise
		// filtering one conflicting original could make the other look unique.
		if !validSupplementMinute(row, endSecond) || row.OfficialVolumeKRW < 0 ||
			math.IsNaN(row.OfficialVolumeKRW) || math.IsInf(row.OfficialVolumeKRW, 0) {
			rejected[key] = true
		}
		if row.ReplaceWholeMinute && (row.Base == "" || row.Source != strings.ToUpper(string(row.Exchange))+"_OFFICIAL_MINUTE_CANDLE_REST") {
			rejected[key] = true
		}
	}
	keys := make([]supplementMinuteIdentity, 0, len(unique))
	for key := range unique {
		if !rejected[key] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].start != keys[j].start {
			return keys[i].start < keys[j].start
		}
		if keys[i].exchange != keys[j].exchange {
			return keys[i].exchange < keys[j].exchange
		}
		return keys[i].market < keys[j].market
	})
	result := make([]reconcile.MinuteSupplement, 0, len(keys))
	conflicts := len(rejected)
	// Chronological identities allow one reusable index, not an additional map
	// for every retained minute. Each atomic minute is scanned once, O(K*M),
	// then each candidate uses one lookup instead of another full market scan.
	observedByMarket := make(map[supplementObservedMarket]float64)
	indexedMinute := domain.UnixSecond(-1)
	for _, key := range keys {
		row := canonicalSupplementEvidence(rows[unique[key]])
		if row.NotionalMethod != "" {
			observed, ok := provePrecisionSupplement(view, row)
			if !ok {
				conflicts++
				continue
			}
			row.WSVolumeKRW = observed
			row.SupplementalVolumeKRW = 0
			result = append(result, row)
			continue
		}
		if indexedMinute != row.MinuteStart {
			clear(observedByMarket)
			start := int64(row.MinuteStart)
			for volumeKey, totals := range view.interval(start-1, start+reconcile.MinuteSeconds-1) {
				if !strings.EqualFold(strings.TrimSpace(volumeKey.Scope), "domestic") ||
					!strings.EqualFold(strings.TrimSpace(volumeKey.Quote), "KRW") {
					continue
				}
				market := supplementObservedMarket{
					exchange: canonicalSupplementExchange(domain.Exchange(volumeKey.Exchange)),
					market:   strings.ToUpper(strings.TrimSpace(volumeKey.Market)),
				}
				if totals.KRWVolume < 0 || math.IsNaN(totals.KRWVolume) || math.IsInf(totals.KRWVolume, 0) {
					// Do not let a positive alias cancel a malformed negative row.
					observedByMarket[market] = math.NaN()
					continue
				}
				observedByMarket[market] += totals.KRWVolume
			}
			indexedMinute = row.MinuteStart
		}
		observed := observedByMarket[supplementObservedMarket{exchange: row.Exchange, market: row.Market}]
		if math.IsNaN(observed) || math.IsInf(observed, 0) {
			conflicts++
			continue
		}
		if row.ReplaceWholeMinute {
			row.WSVolumeKRW = observed
			// Signed accounting removes the observed contribution and selects
			// the whole official amount. It is not a positive-only supplement.
			row.ReplacementAdjustmentKRW = row.OfficialVolumeKRW - observed
			result = append(result, row)
			continue
		}
		residual, ok := reconcile.CanonicalMinuteResidual(row.OfficialVolumeKRW, observed)
		if !ok {
			conflicts++
			continue
		}
		if residual == 0 {
			continue
		}
		row.WSVolumeKRW = observed
		row.SupplementalVolumeKRW = residual
		row.IntervalSec = reconcile.MinuteSeconds
		result = append(result, row)
	}
	return result, conflicts
}

// This zero-only branch independently rechecks the current atomic runtime,
// not the possibly stale ws_trade_minutes row used when the evidence was saved.
func provePrecisionSupplement(view analysisReadView, row reconcile.MinuteSupplement) (float64, bool) {
	e, err := official.DecodeCoinonePrecisionMethod(row.NotionalMethod)
	if err != nil || e.Exchange != row.Exchange || e.Market != row.Market || e.Base != row.Base || e.Quote != row.Quote || e.Source != row.Source || e.MinuteStartMS/1000 != int64(row.MinuteStart) {
		return 0, false
	}
	_, _, reported := e.Values()
	if reported != row.OfficialVolumeKRW {
		return 0, false
	}
	var observed aggregate.Totals
	for key, totals := range view.interval(int64(row.MinuteStart)-1, int64(row.MinuteStart)+59) {
		if !strings.EqualFold(strings.TrimSpace(key.Scope), "domestic") || !strings.EqualFold(strings.TrimSpace(key.Quote), "KRW") || canonicalSupplementExchange(domain.Exchange(key.Exchange)) != row.Exchange || strings.ToUpper(strings.TrimSpace(key.Market)) != row.Market {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(key.Base)) != row.Base || !observed.CheckedAdd(totals) {
			return 0, false
		}
	}
	if observed.TradeCount != 1 || observed.UnresolvedCount != 0 {
		return 0, false
	}
	return observed.KRWVolume, e.ProvesSingleTrade(observed.NativeVolume, observed.KRWVolume, observed.BaseVolume, 1, 0)
}

func sourcePrecisionDifferenceCount(rows []reconcile.MinuteSupplement, start, end int64) int {
	count := 0
	for _, row := range rows {
		if row.NotionalMethod != "" && int64(row.MinuteStart) >= start && int64(row.MinuteStart)+59 <= end {
			count++
		}
	}
	return count
}
