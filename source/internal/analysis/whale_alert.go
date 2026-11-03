package analysis

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

const (
	WhaleAlertVersion           = "WHALE_ALERT_V1"
	WhaleAlertUnavailable       = "UNAVAILABLE"
	DefaultWhaleThresholdKRW    = 500_000_000
	DefaultWhaleWindow          = time.Second
	DefaultWhaleRetention       = 100
	defaultWhaleFreshness       = 30 * time.Second
	defaultWhaleFutureTolerance = 5 * time.Second
)

type WhaleAlertKind string

const (
	WhaleAlertSingle WhaleAlertKind = "SINGLE"
	WhaleAlertBurst  WhaleAlertKind = "BURST"
)

// WhaleAlertEvent describes a public execution signal, not a customer or an
// account. Public exchange feeds do not expose participant identity.
type WhaleAlertEvent struct {
	ID           string           `json:"id"`
	Kind         WhaleAlertKind   `json:"kind"`
	Exchange     string           `json:"exchange"`
	Market       string           `json:"market"`
	Asset        string           `json:"asset"`
	Quote        string           `json:"quote"`
	Side         domain.TradeSide `json:"side"`
	NotionalKRW  float64          `json:"notional_krw"`
	BaseQuantity float64          `json:"base_quantity"`
	TradeCount   uint64           `json:"trade_count"`
	StartedAtMS  int64            `json:"started_at_ms"`
	EventAtMS    int64            `json:"event_at_ms"`
	DetectedAtMS int64            `json:"detected_at_ms"`
	WindowMS     int64            `json:"window_ms"`
}

type WhaleAlertState struct {
	Version       string            `json:"version"`
	ThresholdKRW  float64           `json:"threshold_krw"`
	WindowMS      int64             `json:"window_ms"`
	GeneratedAtMS int64             `json:"generated_at_ms"`
	Events        []WhaleAlertEvent `json:"events"`
	Status        string            `json:"status,omitempty"`
	Reason        string            `json:"reason,omitempty"`
}

// MarshalJSON preserves the successful snapshot schema. An unavailable read
// carries no measurements: zero thresholds or empty events would falsely
// replace the last successful snapshot in public state consumers.
func (s WhaleAlertState) MarshalJSON() ([]byte, error) {
	if s.Status == WhaleAlertUnavailable {
		return json.Marshal(struct {
			Version string `json:"version"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
		}{s.Version, s.Status, s.Reason})
	}
	type stateJSON WhaleAlertState
	return json.Marshal(stateJSON(s))
}

type WhaleAlertConfig struct {
	ThresholdKRW float64
	Window       time.Duration
	Retention    int
	Freshness    time.Duration
}

type whaleSample struct {
	receivedMS int64
	notional   float64
	quantity   float64
	count      uint64
	large      bool
}

type whaleWindow struct {
	samples   []whaleSample
	head      int
	notional  float64
	quantity  float64
	count     uint64
	largeRows int
	latched   bool
	lastMS    int64
}

// WhaleAlertDetector is written by the runtime's canonical accepted-trade
// owner and read by API publishers. Its bounded state is safe for concurrent
// snapshots and does no I/O on the ingestion path.
type WhaleAlertDetector struct {
	mu            sync.RWMutex
	threshold     float64
	windowMS      int64
	retention     int
	freshnessMS   int64
	windows       map[string]*whaleWindow
	events        []WhaleAlertEvent
	generatedAtMS int64
	lastSweepMS   int64
	nextBurstID   uint64
}

func NewWhaleAlertDetector(config WhaleAlertConfig) *WhaleAlertDetector {
	threshold := config.ThresholdKRW
	if !finitePositiveWhale(threshold) {
		threshold = DefaultWhaleThresholdKRW
	}
	window := config.Window
	if window <= 0 {
		window = DefaultWhaleWindow
	}
	retention := config.Retention
	if retention <= 0 {
		retention = DefaultWhaleRetention
	}
	freshness := config.Freshness
	if freshness <= 0 {
		freshness = defaultWhaleFreshness
	}
	return &WhaleAlertDetector{
		threshold: threshold, windowMS: window.Milliseconds(), retention: retention,
		freshnessMS: freshness.Milliseconds(), windows: make(map[string]*whaleWindow),
		events: make([]WhaleAlertEvent, 0, retention),
	}
}

// Observe evaluates one already accepted, deduplicated and KRW-resolved trade.
// It returns true only when a new alert is added.
func (d *WhaleAlertDetector) Observe(trade aggregate.AcceptedTrade, observedAt time.Time) bool {
	nowMS := observedAt.UnixMilli()
	receivedMS := int64(trade.ReceivedTS)
	if !eligibleWhaleTrade(trade) || nowMS <= 0 || receivedMS <= 0 ||
		nowMS-receivedMS > d.freshnessMS || receivedMS-nowMS > defaultWhaleFutureTolerance.Milliseconds() {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweep(receivedMS)

	notional := trade.Totals.KRWVolume
	matchCount := trade.Totals.TradeCount
	if matchCount == 0 {
		matchCount = 1
	}
	largeSingle := matchCount == 1 && notional >= d.threshold
	emitted := false
	if largeSingle {
		event := d.eventForTrade(trade, nowMS)
		d.prepend(event)
		emitted = true
	}

	key := strings.Join([]string{trade.Key.Exchange, trade.Key.Market, string(trade.Side)}, "\x00")
	window := d.windows[key]
	if window == nil {
		window = &whaleWindow{}
		d.windows[key] = window
	}
	// Received timestamps describe the collector observation order. Clamp a
	// rare per-market regression so the bounded sliding window stays ordered.
	sampleMS := receivedMS
	if sampleMS < window.lastMS {
		sampleMS = window.lastMS
	}
	d.prune(window, sampleMS)
	window.samples = append(window.samples, whaleSample{
		receivedMS: sampleMS, notional: notional, quantity: trade.Totals.BaseVolume,
		count: matchCount, large: largeSingle,
	})
	window.notional += notional
	window.quantity += trade.Totals.BaseVolume
	window.count += matchCount
	if largeSingle {
		window.largeRows++
	}
	window.lastMS = sampleMS

	burstEligible := window.count >= 2 && window.largeRows == 0 && window.notional >= d.threshold
	if burstEligible && !window.latched {
		d.nextBurstID++
		firstMS := window.samples[window.head].receivedMS
		d.prepend(WhaleAlertEvent{
			ID:   fmt.Sprintf("burst:%s:%s:%s:%d:%d", trade.Key.Exchange, trade.Key.Market, trade.Side, firstMS, d.nextBurstID),
			Kind: WhaleAlertBurst, Exchange: trade.Key.Exchange, Market: trade.Key.Market,
			Asset: trade.Key.Base, Quote: trade.Key.Quote, Side: trade.Side,
			NotionalKRW: window.notional, BaseQuantity: window.quantity, TradeCount: window.count,
			StartedAtMS: firstMS, EventAtMS: int64(trade.EventTS), DetectedAtMS: nowMS, WindowMS: d.windowMS,
		})
		window.latched = true
		emitted = true
	} else if !burstEligible {
		window.latched = false
	}
	return emitted
}

func (d *WhaleAlertDetector) State() WhaleAlertState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return WhaleAlertState{
		Version: WhaleAlertVersion, ThresholdKRW: d.threshold, WindowMS: d.windowMS,
		GeneratedAtMS: d.generatedAtMS,
		Events:        append(make([]WhaleAlertEvent, 0, len(d.events)), d.events...),
	}
}

func (d *WhaleAlertDetector) eventForTrade(trade aggregate.AcceptedTrade, nowMS int64) WhaleAlertEvent {
	return WhaleAlertEvent{
		ID:   "single:" + trade.Key.Exchange + ":" + trade.Key.Market + ":" + trade.TradeID,
		Kind: WhaleAlertSingle, Exchange: trade.Key.Exchange, Market: trade.Key.Market,
		Asset: trade.Key.Base, Quote: trade.Key.Quote, Side: trade.Side,
		NotionalKRW: trade.Totals.KRWVolume, BaseQuantity: trade.Totals.BaseVolume,
		TradeCount: trade.Totals.TradeCount, StartedAtMS: int64(trade.ReceivedTS),
		EventAtMS: int64(trade.EventTS), DetectedAtMS: nowMS,
	}
}

func (d *WhaleAlertDetector) prepend(event WhaleAlertEvent) {
	d.events = append(d.events, WhaleAlertEvent{})
	copy(d.events[1:], d.events[:len(d.events)-1])
	d.events[0] = event
	if len(d.events) > d.retention {
		d.events = d.events[:d.retention]
	}
	d.generatedAtMS = event.DetectedAtMS
}

func (d *WhaleAlertDetector) prune(window *whaleWindow, nowMS int64) {
	cutoff := nowMS - d.windowMS
	for window.head < len(window.samples) && window.samples[window.head].receivedMS < cutoff {
		sample := window.samples[window.head]
		window.notional -= sample.notional
		window.quantity -= sample.quantity
		if sample.count <= window.count {
			window.count -= sample.count
		} else {
			window.count = 0
		}
		if sample.large {
			window.largeRows--
		}
		window.head++
	}
	if window.head > 0 && (window.head >= 256 || window.head*2 >= len(window.samples)) {
		window.samples = append([]whaleSample(nil), window.samples[window.head:]...)
		window.head = 0
	}
	if math.Abs(window.notional) < 1e-4 {
		window.notional = 0
	}
	if math.Abs(window.quantity) < 1e-12 {
		window.quantity = 0
	}
}

func (d *WhaleAlertDetector) sweep(nowMS int64) {
	if d.lastSweepMS > 0 && nowMS-d.lastSweepMS < d.windowMS {
		return
	}
	for key, window := range d.windows {
		d.prune(window, nowMS)
		if window.head >= len(window.samples) {
			delete(d.windows, key)
		}
	}
	d.lastSweepMS = nowMS
}

func eligibleWhaleTrade(trade aggregate.AcceptedTrade) bool {
	return trade.Key.Scope == string(domain.ScopeDomestic) &&
		(trade.Side == domain.TradeSideBuy || trade.Side == domain.TradeSideSell) &&
		finitePositiveWhale(trade.Totals.KRWVolume) &&
		finitePositiveWhale(trade.Totals.BaseVolume) && trade.Totals.UnresolvedCount == 0
}

func finitePositiveWhale(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
