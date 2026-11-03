package orderbook

import (
	"math"
	"sort"
	"strings"
	"sync"
)

const (
	DefaultHistoryRetentionSeconds = int64(86_400)
	DefaultHistoryMaxPoints        = 10_000
)

var HistoryFields = []string{"bithumb_mid_krw", "bithumb_spread_bps", "bithumb_depth_krw", "bithumb_buy_slippage_bps", "bithumb_sell_slippage_bps", "bithumb_buy_fill_pct", "bithumb_sell_fill_pct", "global_reference_mid_krw", "global_depth_krw"}
var bithumbHistoryFields = map[string]struct{}{"bithumb_mid_krw": {}, "bithumb_spread_bps": {}, "bithumb_depth_krw": {}, "bithumb_buy_slippage_bps": {}, "bithumb_sell_slippage_bps": {}, "bithumb_buy_fill_pct": {}, "bithumb_sell_fill_pct": {}}

type MicrostructureRow struct {
	Asset  string
	Values map[string]*float64
}
type historyPoint struct {
	timestamp int64
	values    map[string]float64
}

type HistoryComparison struct {
	Status              string              `json:"status"`
	PrePointCount       int                 `json:"pre_point_count"`
	PostPointCount      int                 `json:"post_point_count"`
	PreObservedStartMS  *int64              `json:"pre_observed_start_ms"`
	PreObservedEndMS    *int64              `json:"pre_observed_end_ms"`
	PostObservedStartMS *int64              `json:"post_observed_start_ms"`
	PostObservedEndMS   *int64              `json:"post_observed_end_ms"`
	Before              map[string]*float64 `json:"before"`
	After               map[string]*float64 `json:"after"`
	ChangePercent       map[string]*float64 `json:"change_pct"`
	Note                string              `json:"note"`
}

type MicrostructureHistory struct {
	mu          sync.RWMutex
	retentionMS int64
	maxPoints   int
	samples     map[string][]historyPoint
}

func NewMicrostructureHistory(retentionSeconds int64, maxPoints int) *MicrostructureHistory {
	if retentionSeconds < 1 {
		retentionSeconds = DefaultHistoryRetentionSeconds
	}
	if maxPoints < 2 {
		maxPoints = DefaultHistoryMaxPoints
	}
	return &MicrostructureHistory{retentionMS: retentionSeconds * 1000, maxPoints: maxPoints, samples: map[string][]historyPoint{}}
}

func (h *MicrostructureHistory) Record(rows []MicrostructureRow, asOfMS int64, assets []string) int {
	if asOfMS <= 0 {
		return 0
	}
	allowed := map[string]struct{}{}
	if assets != nil {
		for _, asset := range assets {
			allowed[normalizeAsset(asset)] = struct{}{}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for asset := range h.samples {
		h.prune(asset, asOfMS)
		if len(h.samples[asset]) == 0 {
			delete(h.samples, asset)
		}
	}
	count := 0
	for _, row := range rows {
		asset := normalizeAsset(row.Asset)
		if asset == "" {
			continue
		}
		if assets != nil {
			if _, ok := allowed[asset]; !ok {
				continue
			}
		}
		values := map[string]float64{}
		for _, field := range HistoryFields {
			value := row.Values[field]
			if value != nil && finite(*value) {
				values[field] = *value
			}
		}
		if len(values) == 0 {
			continue
		}
		h.prune(asset, asOfMS)
		points := h.samples[asset]
		point := historyPoint{asOfMS, values}
		if len(points) > 0 && points[len(points)-1].timestamp == asOfMS {
			points[len(points)-1] = point
		} else {
			points = append(points, point)
		}
		h.samples[asset] = points
		h.prune(asset, asOfMS)
		count++
	}
	return count
}

func (h *MicrostructureHistory) ClearGlobalReference() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	removed := 0
	for asset, points := range h.samples {
		retained := points[:0]
		for _, point := range points {
			for _, field := range []string{"global_reference_mid_krw", "global_depth_krw"} {
				if _, ok := point.values[field]; ok {
					delete(point.values, field)
					removed++
				}
			}
			if len(point.values) > 0 {
				retained = append(retained, point)
			}
		}
		clear(points[len(retained):])
		if len(retained) == 0 {
			delete(h.samples, asset)
		} else {
			h.samples[asset] = retained
		}
	}
	return removed
}

func (h *MicrostructureHistory) Compare(asset string, anchorMS int64, preSeconds, postSeconds int64, asOfMS *int64) HistoryComparison {
	if anchorMS <= 0 {
		return comparison(nil, nil)
	}
	if preSeconds < 1 {
		preSeconds = 1
	}
	if postSeconds < 1 {
		postSeconds = 1
	}
	start := anchorMS - preSeconds*1000
	end := anchorMS + postSeconds*1000
	if asOfMS != nil && *asOfMS > 0 && *asOfMS < end {
		end = *asOfMS
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	// Keep the read lock while comparison consumes each point's values map.
	// Copying only the outer slice did not make those nested maps immutable and
	// could race ClearGlobalReference, which deletes map entries under the write
	// lock. Holding the lock also avoids one proportional slice allocation.
	points := h.samples[normalizeAsset(asset)]
	pre := make([]historyPoint, 0)
	post := make([]historyPoint, 0)
	for _, point := range points {
		if point.timestamp >= start && point.timestamp < anchorMS {
			pre = append(pre, point)
		} else if point.timestamp >= anchorMS && point.timestamp <= end {
			post = append(post, point)
		}
	}
	return comparison(pre, post)
}

func (h *MicrostructureHistory) prune(asset string, latest int64) {
	points := h.samples[asset]
	cutoff := latest - h.retentionMS
	first := 0
	for first < len(points) && points[first].timestamp < cutoff {
		first++
	}
	clear(points[:first])
	points = points[first:]
	if len(points) > h.maxPoints {
		drop := len(points) - h.maxPoints
		clear(points[:drop])
		points = points[drop:]
	}
	h.samples[asset] = points
}

func comparison(allPre, allPost []historyPoint) HistoryComparison {
	pre := bithumbPoints(allPre)
	post := bithumbPoints(allPost)
	result := HistoryComparison{Status: "CURRENT_SNAPSHOT_ONLY", Before: emptyMetricMap(), After: emptyMetricMap(), ChangePercent: emptyMetricMap(), Note: "공지 전후 호가 자료가 없어 현재 값만 표시합니다."}
	for _, side := range []struct {
		target        map[string]*float64
		filtered, all []historyPoint
	}{{result.Before, pre, allPre}, {result.After, post, allPost}} {
		for _, field := range HistoryFields {
			source := side.all
			if _, domestic := bithumbHistoryFields[field]; domestic {
				source = side.filtered
			}
			values := make([]float64, 0)
			for _, point := range source {
				if value, ok := point.values[field]; ok {
					values = append(values, value)
				}
			}
			if len(values) > 0 {
				value := median(values)
				side.target[field] = &value
			}
		}
	}
	for _, field := range HistoryFields {
		before, after := result.Before[field], result.After[field]
		if before != nil && after != nil && *before > 0 {
			value := (*after / *before - 1) * 100
			result.ChangePercent[field] = &value
		}
	}
	result.PrePointCount = len(pre)
	result.PostPointCount = len(post)
	result.PreObservedStartMS, result.PreObservedEndMS = observedRange(pre)
	result.PostObservedStartMS, result.PostObservedEndMS = observedRange(post)
	if len(pre) > 0 && len(post) > 0 {
		result.Status = "BEFORE_AFTER_OBSERVED"
		result.Note = "공지 전후 양쪽 호가 자료가 확보되어 각 구간의 중앙값을 비교했습니다."
	} else if len(pre) > 0 || len(post) > 0 {
		result.Status = "BEFORE_AFTER_PARTIAL"
		result.Note = "공지 전후 한쪽 호가 자료만 확보되어 부분 비교로 표시합니다."
	}
	return result
}

func bithumbPoints(points []historyPoint) []historyPoint {
	result := make([]historyPoint, 0)
	for _, point := range points {
		for field := range bithumbHistoryFields {
			if _, ok := point.values[field]; ok {
				result = append(result, point)
				break
			}
		}
	}
	return result
}
func emptyMetricMap() map[string]*float64 {
	result := make(map[string]*float64, len(HistoryFields))
	for _, field := range HistoryFields {
		result[field] = nil
	}
	return result
}
func observedRange(points []historyPoint) (*int64, *int64) {
	if len(points) == 0 {
		return nil, nil
	}
	start, end := points[0].timestamp, points[0].timestamp
	for _, point := range points[1:] {
		if point.timestamp < start {
			start = point.timestamp
		}
		if point.timestamp > end {
			end = point.timestamp
		}
	}
	return &start, &end
}
func median(values []float64) float64 {
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 0 {
		return values[middle-1] + (values[middle]-values[middle-1])/2
	}
	return values[middle]
}
func normalizeAsset(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }
func finite(value float64) bool          { return !math.IsNaN(value) && !math.IsInf(value, 0) }
