package fx

import (
	"container/heap"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"

	"market-share-go/internal/domain"
)

type edgeKey struct{ source, destination string }

type observationVersion struct {
	priority  uint8
	primary   uint64
	secondary uint64
}

type observation struct {
	rate    float64
	time    domain.UnixMilli
	version observationVersion
}

// venueObservations holds one venue's recent observations of an edge, oldest
// first. A rate is resolved as of a time and may use only an observation made
// at or before it. Keeping just the newest observation made every trade whose
// receipt preceded the venue's latest update unresolvable: order books update
// several times a second, so a trade processed a few milliseconds after a
// newer USDT/KRW book counted as unconverted and withheld the whole
// all-domestic share.
type venueObservations []observation

type edgeSet map[edgeKey]map[string]venueObservations

const (
	// observationHistorySpanMS is how far before a venue's newest observation a
	// rate can still be resolved. Trades are converted within seconds of
	// receipt and an analysis snapshot is taken at the start of its second.
	observationHistorySpanMS = 30_000
	// observationBucketMS thins a busy venue to one observation per bucket, the
	// latest one. A query can then fall back at most one bucket further than
	// the exact latest earlier observation, and never to a later one.
	observationBucketMS = 100
)

// at returns the latest observation made at or before now.
func (h venueObservations) at(now domain.UnixMilli) (observation, bool) {
	index := sort.Search(len(h), func(i int) bool { return h[i].time > now })
	if index == 0 {
		return observation{}, false
	}
	return h[index-1], true
}

// newest returns the latest observation of the venue.
func (h venueObservations) newest() (observation, bool) {
	if len(h) == 0 {
		return observation{}, false
	}
	return h[len(h)-1], true
}

type Resolution struct {
	Rate             float64
	Path             []string
	MaxAgeMS         int64
	SourceCount      int
	MaxDispersionPct float64
}

type SnapshotResolution struct {
	RateKRW               float64  `json:"rate_krw"`
	ExecutableBuyRateKRW  *float64 `json:"executable_buy_rate_krw"`
	ExecutableSellRateKRW *float64 `json:"executable_sell_rate_krw"`
	ExecutableSpreadBPS   *float64 `json:"executable_spread_bps"`
	ExecutableStatus      string   `json:"executable_status"`
	Path                  []string `json:"path"`
	HopCount              int      `json:"hop_count"`
	MaxAgeMS              int64    `json:"max_age_ms"`
	SourceCount           int      `json:"source_count"`
	MaxDispersionPercent  float64  `json:"max_dispersion_pct"`
}

type Snapshot struct {
	Transport                      string                         `json:"transport"`
	RESTFallbackEnabled            bool                           `json:"rest_fallback_enabled"`
	VolumeConversionPolicy         string                         `json:"volume_conversion_policy"`
	VolumeConversionAnchorExchange string                         `json:"volume_conversion_anchor_exchange"`
	VolumeConversionAnchorPair     string                         `json:"volume_conversion_anchor_pair"`
	MaxAgeMS                       int64                          `json:"max_age_ms"`
	MaxHops                        *int                           `json:"max_hops"`
	UpdatedAtMS                    domain.UnixMilli               `json:"updated_at_ms"`
	ValuationEdgeCount             int                            `json:"valuation_edge_count"`
	ExecutableEdgeCount            int                            `json:"executable_edge_count"`
	RegisteredMarketCount          int                            `json:"registered_market_count"`
	QuoteAssets                    map[string]*SnapshotResolution `json:"quote_assets"`
	UnresolvedQuoteAssets          []string                       `json:"unresolved_quote_assets"`
}

type Oracle struct {
	mu              sync.RWMutex
	maxAge          int64
	maxHops         *int
	edges           edgeSet
	volumeEdges     edgeSet
	executableEdges edgeSet
	quoteAssets     map[string]struct{}
	marketCount     int
	updatedAt       domain.UnixMilli
}

func New(maxAgeMS int64, maxHops *int) *Oracle {
	if maxAgeMS < 1 {
		maxAgeMS = 120_000
	}
	return &Oracle{
		maxAge: maxAgeMS, maxHops: maxHops, edges: make(edgeSet),
		volumeEdges: make(edgeSet), executableEdges: make(edgeSet),
		quoteAssets: make(map[string]struct{}),
	}
}

func (o *Oracle) Configure(universe domain.MarketUniverse) {
	quotes := map[string]struct{}{"KRW": {}}
	markets := universe.Markets()
	for _, market := range markets {
		if quote := asset(market.Quote); quote != "" {
			quotes[quote] = struct{}{}
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.quoteAssets = quotes
	o.marketCount = len(markets)
	o.edges = filterEdges(o.edges, quotes)
	o.volumeEdges = filterEdges(o.volumeEdges, quotes)
	o.executableEdges = filterEdges(o.executableEdges, quotes)
}

func (o *Oracle) Update(base, quote string, price float64, source string, received domain.UnixMilli) {
	base, quote = asset(base), asset(quote)
	if base == "" || quote == "" || base == quote || !positive(price) || received <= 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	version := tradeObservationVersion(base, quote, price)
	put(o.edges, base, quote, source, price, received, version)
	put(o.edges, quote, base, source, 1/price, received, version)
	if volumeConversionEdge(base, quote, source) {
		put(o.volumeEdges, base, quote, source, price, received, version)
		put(o.volumeEdges, quote, base, source, 1/price, received, version)
	}
	o.quoteAssets[quote] = struct{}{}
	if received > o.updatedAt {
		o.updatedAt = received
	}
}

func (o *Oracle) ObserveTrade(event domain.TradeEvent) {
	if event.Validate() != nil {
		return
	}
	o.mu.RLock()
	_, baseIsQuote := o.quoteAssets[asset(event.Base)]
	_, quoteIsQuote := o.quoteAssets[asset(event.Quote)]
	o.mu.RUnlock()
	if baseIsQuote && quoteIsQuote {
		o.Update(event.Base, event.Quote, event.Price, string(event.Exchange), event.ReceivedTS)
	}
}

func (o *Oracle) UpdateOrderBook(base, quote string, bid, ask float64, source string, received domain.UnixMilli) {
	base, quote = asset(base), asset(quote)
	if base == "" || quote == "" || base == quote || !positive(bid) || !positive(ask) || bid > ask || received <= 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.quoteAssets) > 0 {
		if _, ok := o.quoteAssets[base]; !ok {
			return
		}
		if _, ok := o.quoteAssets[quote]; !ok {
			return
		}
	}
	midpoint := bid + (ask-bid)/2
	if !positive(midpoint) {
		return
	}
	version := orderBookObservationVersion(base, quote, bid, ask)
	put(o.edges, base, quote, source, midpoint, received, version)
	put(o.edges, quote, base, source, 1/midpoint, received, version)
	put(o.executableEdges, base, quote, source, bid, received, version)
	put(o.executableEdges, quote, base, source, 1/ask, received, version)
	if volumeConversionEdge(base, quote, source) {
		put(o.volumeEdges, base, quote, source, midpoint, received, version)
		put(o.volumeEdges, quote, base, source, 1/midpoint, received, version)
	}
	if received > o.updatedAt {
		o.updatedAt = received
	}
}

func (o *Oracle) ResolveToKRW(value string, now domain.UnixMilli) *Resolution {
	return o.resolveWith(value, "KRW", now, false, false)
}

func (o *Oracle) ResolveVolumeToKRW(value string, now domain.UnixMilli) *Resolution {
	return o.resolveWith(value, "KRW", now, true, false)
}

func (o *Oracle) RateToKRW(value string, now domain.UnixMilli) *float64 {
	return rate(o.ResolveToKRW(value, now))
}

func (o *Oracle) VolumeRateToKRW(value string, now domain.UnixMilli) *float64 {
	return rate(o.ResolveVolumeToKRW(value, now))
}

func (o *Oracle) ExecutableRateToKRW(value, side string, now domain.UnixMilli) (*float64, error) {
	value = asset(value)
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "sell":
		return rate(o.resolveWith(value, "KRW", now, false, true)), nil
	case "buy":
		resolution := o.resolveWith("KRW", value, now, false, true)
		if resolution == nil || !positive(resolution.Rate) {
			return nil, nil
		}
		result := 1 / resolution.Rate
		return &result, nil
	default:
		return nil, errors.New("side must be 'buy' or 'sell'")
	}
}

func (o *Oracle) Snapshot(now domain.UnixMilli) Snapshot {
	if now <= 0 {
		now = domain.UnixMilli(1)
	}
	o.mu.RLock()
	assets := make([]string, 0, len(o.quoteAssets))
	for value := range o.quoteAssets {
		assets = append(assets, value)
	}
	maxAge, maxHops := o.maxAge, copyMaxHops(o.maxHops)
	updatedAt, valuationEdges := o.updatedAt, len(o.edges)
	executableEdges, markets := len(o.executableEdges), o.marketCount
	o.mu.RUnlock()
	sort.Strings(assets)

	result := Snapshot{
		Transport: "WEBSOCKET_ONLY", RESTFallbackEnabled: false,
		VolumeConversionPolicy:         "BITHUMB_USDT_KRW_ANCHORED",
		VolumeConversionAnchorExchange: "bithumb", VolumeConversionAnchorPair: "USDT/KRW",
		MaxAgeMS: maxAge, MaxHops: maxHops, UpdatedAtMS: updatedAt,
		ValuationEdgeCount: valuationEdges, ExecutableEdgeCount: executableEdges,
		RegisteredMarketCount: markets, QuoteAssets: make(map[string]*SnapshotResolution, len(assets)),
		UnresolvedQuoteAssets: []string{},
	}
	for _, value := range assets {
		resolution := o.ResolveVolumeToKRW(value, now)
		if resolution == nil {
			result.QuoteAssets[value] = nil
			result.UnresolvedQuoteAssets = append(result.UnresolvedQuoteAssets, value)
			continue
		}
		buy, _ := o.ExecutableRateToKRW(value, "buy", now)
		sell, _ := o.ExecutableRateToKRW(value, "sell", now)
		buy, sell, status := ValidateExecutableRates(buy, sell)
		var spread *float64
		if buy != nil && sell != nil && *sell > 0 {
			value := (*buy / *sell - 1) * 10_000
			if finite(value) {
				spread = &value
			}
		}
		result.QuoteAssets[value] = &SnapshotResolution{
			RateKRW: resolution.Rate, ExecutableBuyRateKRW: buy,
			ExecutableSellRateKRW: sell, ExecutableSpreadBPS: spread,
			ExecutableStatus: status, Path: append([]string(nil), resolution.Path...),
			HopCount: max(0, len(resolution.Path)-1), MaxAgeMS: resolution.MaxAgeMS,
			SourceCount: resolution.SourceCount, MaxDispersionPercent: resolution.MaxDispersionPct,
		}
	}
	return result
}

func copyMaxHops(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func ValidateExecutableRates(buy, sell *float64) (*float64, *float64, string) {
	if buy != nil && sell != nil && *buy < *sell {
		return nil, nil, "CROSSED_EXECUTABLE_PATHS"
	}
	if buy != nil && sell != nil {
		return buy, sell, "LIVE"
	}
	if buy != nil || sell != nil {
		return buy, sell, "PARTIAL"
	}
	return nil, nil, "UNRESOLVED"
}

func (o *Oracle) resolveWith(start, destination string, now domain.UnixMilli, volume, executable bool) *Resolution {
	start, destination = asset(start), asset(destination)
	if start == "" || destination == "" || now <= 0 {
		return nil
	}
	// KRW-quoted trades are the dominant domestic path. Their conversion is
	// exactly the identity and must not clone or search the entire FX graph for
	// every event.
	if start == destination {
		return &Resolution{Rate: 1, Path: []string{start}, SourceCount: 1}
	}
	o.mu.RLock()
	// Resolution is read-only. Keep the read lock for the small graph walk
	// instead of cloning every edge/source map for every non-KRW trade. The
	// quote set is still copied because resolve adds its two endpoints locally.
	edges := o.edges
	if volume {
		edges = o.volumeEdges
	} else if executable {
		edges = o.executableEdges
	}
	quotes := copyAssets(o.quoteAssets)
	maxHops := o.maxHops
	maxAge := o.maxAge
	defer o.mu.RUnlock()
	return resolve(start, destination, edges, quotes, now, maxAge, maxHops)
}

type score struct {
	hops       int
	dispersion float64
	sources    int
	age        int64
}

func (s score) less(other score) bool {
	if s.hops != other.hops {
		return s.hops < other.hops
	}
	if s.dispersion != other.dispersion {
		return s.dispersion < other.dispersion
	}
	if s.sources != other.sources {
		return s.sources < other.sources
	}
	return s.age < other.age
}

func (s score) equal(other score) bool {
	return s == other
}

type pathState struct {
	node   string
	rate   float64
	path   []string
	score  score
	serial int
}
type pathQueue []pathState

func (q pathQueue) Len() int { return len(q) }
func (q pathQueue) Less(i, j int) bool {
	if q[i].score.equal(q[j].score) {
		return q[i].serial < q[j].serial
	}
	return q[i].score.less(q[j].score)
}
func (q pathQueue) Swap(i, j int)   { q[i], q[j] = q[j], q[i] }
func (q *pathQueue) Push(value any) { *q = append(*q, value.(pathState)) }
func (q *pathQueue) Pop() any {
	old := *q
	value := old[len(old)-1]
	*q = old[:len(old)-1]
	return value
}

func resolve(start, destination string, edges edgeSet, quotes map[string]struct{}, now domain.UnixMilli, maxAge int64, maxHops *int) *Resolution {
	if start == destination {
		return &Resolution{Rate: 1, Path: []string{start}, SourceCount: 1}
	}
	quotes[start], quotes[destination] = struct{}{}, struct{}{}
	destinations := make(map[string][]string)
	for key := range edges {
		if _, sourceOK := quotes[key.source]; !sourceOK {
			continue
		}
		if _, destinationOK := quotes[key.destination]; !destinationOK {
			continue
		}
		destinations[key.source] = append(destinations[key.source], key.destination)
	}
	for source := range destinations {
		sort.Strings(destinations[source])
		destinations[source] = unique(destinations[source])
	}
	initial := score{}
	queue := &pathQueue{{node: start, rate: 1, path: []string{start}, score: initial}}
	heap.Init(queue)
	best := map[string]score{start: initial}
	serial := 0
	for queue.Len() > 0 {
		current := heap.Pop(queue).(pathState)
		if known, ok := best[current.node]; !ok || !known.equal(current.score) {
			continue
		}
		if current.node == destination {
			return &Resolution{
				Rate:             current.rate,
				Path:             current.path,
				MaxAgeMS:         current.score.age,
				SourceCount:      -current.score.sources,
				MaxDispersionPct: current.score.dispersion,
			}
		}
		if maxHops != nil && current.score.hops >= *maxHops {
			continue
		}
		for _, next := range destinations[current.node] {
			if contains(current.path, next) {
				continue
			}
			edge, ok := fresh(edges[edgeKey{current.node, next}], now, maxAge)
			if !ok {
				continue
			}
			weakest := edge.sources
			if previous := -current.score.sources; previous > 0 && previous < weakest {
				weakest = previous
			}
			nextScore := score{
				hops:       current.score.hops + 1,
				dispersion: math.Max(current.score.dispersion, edge.dispersion),
				sources:    -weakest,
				age:        max(current.score.age, edge.age),
			}
			if known, exists := best[next]; exists && !nextScore.less(known) {
				continue
			}
			nextRate := current.rate * edge.rate
			if !positive(nextRate) {
				continue
			}
			best[next] = nextScore
			serial++
			path := append(append([]string(nil), current.path...), next)
			heap.Push(queue, pathState{
				node:   next,
				rate:   nextRate,
				path:   path,
				score:  nextScore,
				serial: serial,
			})
		}
	}
	return nil
}

type freshEdge struct {
	rate       float64
	age        int64
	sources    int
	dispersion float64
}

func fresh(observations map[string]venueObservations, now domain.UnixMilli, maxAge int64) (freshEdge, bool) {
	rates := make([]float64, 0, len(observations))
	oldest := int64(0)
	for _, history := range observations {
		// A later observation is never projected back onto an earlier time.
		observation, ok := history.at(now)
		if !ok {
			continue
		}
		age := int64(now - observation.time)
		if age > maxAge {
			continue
		}
		rates = append(rates, observation.rate)
		if age > oldest {
			oldest = age
		}
	}
	if len(rates) == 0 {
		return freshEdge{}, false
	}
	sort.Float64s(rates)
	middle := median(rates)
	dispersion := 0.0
	if middle > 0 {
		dispersion = (rates[len(rates)-1]/middle - rates[0]/middle) * 100
		if !finite(dispersion) || dispersion < 0 {
			return freshEdge{}, false
		}
	}
	return freshEdge{rate: middle, age: oldest, sources: len(rates), dispersion: dispersion}, true
}

func put(
	edges edgeSet,
	source, destination, venue string,
	rate float64,
	timestamp domain.UnixMilli,
	version observationVersion,
) {
	if !positive(rate) {
		return
	}
	key := edgeKey{source, destination}
	if edges[key] == nil {
		edges[key] = make(map[string]venueObservations)
	}
	history := edges[key][venue]
	next := observation{rate: rate, time: timestamp, version: version}
	current, ok := history.newest()
	switch {
	case !ok:
		history = append(history, next)
	case timestamp < current.time || (timestamp == current.time && !version.greater(current.version)):
		// An older observation arriving late never replaces a newer one.
		return
	case timestamp/observationBucketMS == current.time/observationBucketMS:
		history[len(history)-1] = next
	default:
		history = append(history, next)
	}
	edges[key][venue] = trimObservations(history)
}

// trimObservations drops observations that no query inside the history span
// can use: everything before the last observation made at or before the start
// of the span. That last one is kept, so every time in the span still finds
// its latest earlier observation.
func trimObservations(history venueObservations) venueObservations {
	newest := history[len(history)-1].time
	first := 0
	for first+1 < len(history) && history[first+1].time <= newest-observationHistorySpanMS {
		first++
	}
	if first == 0 {
		return history
	}
	return history[:copy(history, history[first:])]
}

func (v observationVersion) greater(other observationVersion) bool {
	if v.priority != other.priority {
		return v.priority > other.priority
	}
	if v.primary != other.primary {
		return v.primary > other.primary
	}
	return v.secondary > other.secondary
}

func tradeObservationVersion(base, quote string, rate float64) observationVersion {
	if base > quote {
		rate = 1 / rate
	}
	value := math.Float64bits(rate)
	return observationVersion{priority: 1, primary: value, secondary: value}
}

func orderBookObservationVersion(base, quote string, bid, ask float64) observationVersion {
	if base > quote {
		bid, ask = 1/ask, 1/bid
	}
	return observationVersion{
		priority:  2,
		primary:   math.Float64bits(bid),
		secondary: math.Float64bits(ask),
	}
}

func volumeConversionEdge(base, quote, source string) bool {
	if base != "KRW" && quote != "KRW" {
		return true
	}
	return ((base == "USDT" && quote == "KRW") || (base == "KRW" && quote == "USDT")) && strings.EqualFold(source, "bithumb")
}

func filterEdges(edges edgeSet, allowed map[string]struct{}) edgeSet {
	result := make(edgeSet)
	for key, values := range edges {
		_, sourceOK := allowed[key.source]
		_, destinationOK := allowed[key.destination]
		if sourceOK && destinationOK {
			result[key] = values
		}
	}
	return result
}

func copyAssets(values map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(values)+2)
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}

func rate(resolution *Resolution) *float64 {
	if resolution == nil || !positive(resolution.Rate) {
		return nil
	}
	value := resolution.Rate
	return &value
}

func asset(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }

func positive(value float64) bool { return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func median(values []float64) float64 {
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return values[middle-1] + (values[middle]-values[middle-1])/2
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func unique(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
