package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
)

const (
	// Heavy analysis frames start at a ten-second comparison window. Realtime
	// ticks still publish independently, so refreshing the multi-megabyte tree
	// twice inside the same minimum window only burns CPU and bandwidth.
	defaultAnalysisInterval  = 10 * time.Second
	summary2AnalysisInterval = 10 * time.Second
	analysisPrewarmStagger   = 250 * time.Millisecond
)

var dashboardAnalysisScopes = [...]string{"KRW", "ALL_DOMESTIC_CONVERTED"}

type analysisBroadcastKey struct {
	window   int64
	scope    string
	notional float64
	profile  string
}

type analysisProjection struct {
	full    any
	partial any
}

// sharedJSONValue lazily serializes a projected analysis once. Every browser
// still receives its own WebSocket envelope, but the large analysis tree is
// encoded to identical JSON bytes once and then reused by all subscribers.
type sharedJSONValue struct {
	value   any
	once    sync.Once
	payload []byte
	err     error
}

func (v *sharedJSONValue) MarshalJSON() ([]byte, error) {
	return v.bytes()
}

func (v *sharedJSONValue) bytes() ([]byte, error) {
	v.once.Do(func() {
		v.payload, v.err = json.Marshal(v.value)
	})
	return v.payload, v.err
}

type sharedAnalysisFrame struct {
	payload     marketanalysis.Analysis
	projections *analysisProjectionSet
	state       map[string]any
	err         error
}

func (f sharedAnalysisFrame) projection(view string, partial bool) any {
	projection := f.projections.get(view)
	if partial {
		return projection.partial
	}
	return projection.full
}

type sharedAnalysisStream struct {
	key         analysisBroadcastKey
	ctx         context.Context
	cancel      context.CancelFunc
	pinned      bool
	startDelay  time.Duration
	subscribers map[chan sharedAnalysisFrame]struct{}
	latest      *sharedAnalysisFrame
}

// analysisBroadcaster owns one analysis producer for each calculation input.
// Matching production keys are demand-driven and share one calculation. An
// explicit always-on handler may pin a finite warm set. The default
// full/summary/overview/events views share the same calculation; summary2 has
// different reaction windows and therefore uses a separate profile.
// Projection, mapping, runtime-state capture, and final JSON encoding happen
// once per publication/wire key, not once per browser connection.
type analysisBroadcaster struct {
	provider     AnalysisProvider
	runtimeState RuntimeStateProvider
	logger       *slog.Logger

	mu      sync.Mutex
	streams map[analysisBroadcastKey]*sharedAnalysisStream

	defaultInterval  time.Duration
	summary2Interval time.Duration
	prewarmStagger   time.Duration
}

func newAnalysisBroadcaster(provider AnalysisProvider, runtimeState RuntimeStateProvider, logger *slog.Logger) *analysisBroadcaster {
	if logger == nil {
		logger = slog.Default()
	}
	return &analysisBroadcaster{
		provider: provider, runtimeState: runtimeState, logger: logger,
		streams:          make(map[analysisBroadcastKey]*sharedAnalysisStream),
		defaultInterval:  defaultAnalysisInterval,
		summary2Interval: summary2AnalysisInterval,
		prewarmStagger:   analysisPrewarmStagger,
	}
}

type analysisWarmSpec struct {
	window   int64
	scope    string
	notional float64
	view     string
}

func dashboardAnalysisWarmSpecs() []analysisWarmSpec {
	specs := make([]analysisWarmSpec, 0, len(aggregate.DefaultWindows)*len(dashboardAnalysisScopes)+1)
	// Start the landing page's official 24-hour frame first.
	specs = append(specs, analysisWarmSpec{window: 86400, scope: "KRW", view: "summary2"})
	for _, scope := range dashboardAnalysisScopes {
		for _, window := range aggregate.DefaultWindows {
			specs = append(specs, analysisWarmSpec{window: window, scope: scope, view: "overview"})
		}
	}
	return specs
}

// prewarm is available only to explicit always-on handlers. Production uses
// NewHandler and therefore leaves these calculations demand-driven. Unbounded
// custom notionals are always demand-driven.
func (b *analysisBroadcaster) prewarm(ctx context.Context, specs []analysisWarmSpec) {
	if b == nil || b.provider == nil || len(specs) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	streams := make([]*sharedAnalysisStream, 0, len(specs))
	b.mu.Lock()
	for _, spec := range specs {
		key := analysisKeyFor(spec.window, spec.scope, spec.notional, spec.view)
		if stream := b.streams[key]; stream != nil {
			stream.pinned = true
			continue
		}
		streamCtx, cancel := context.WithCancel(ctx)
		stream := &sharedAnalysisStream{
			key: key, ctx: streamCtx, cancel: cancel, pinned: true,
			startDelay:  time.Duration(len(streams)) * b.prewarmStagger,
			subscribers: make(map[chan sharedAnalysisFrame]struct{}),
		}
		b.streams[key] = stream
		streams = append(streams, stream)
	}
	b.mu.Unlock()
	for _, stream := range streams {
		go b.run(stream)
	}
}

func analysisKeyFor(window int64, scope string, notional float64, view string) analysisBroadcastKey {
	profile := "default"
	if view == "summary2" {
		profile = "summary2"
	}
	return analysisBroadcastKey{window: window, scope: scope, notional: notional, profile: profile}
}

func (b *analysisBroadcaster) subscribe(window int64, scope string, notional float64, view string) (<-chan sharedAnalysisFrame, func()) {
	key := analysisKeyFor(window, scope, notional, view)
	updates := make(chan sharedAnalysisFrame, 1)

	start := false
	b.mu.Lock()
	stream := b.streams[key]
	if stream == nil {
		ctx, cancel := context.WithCancel(context.Background())
		stream = &sharedAnalysisStream{
			key: key, ctx: ctx, cancel: cancel,
			subscribers: make(map[chan sharedAnalysisFrame]struct{}),
		}
		b.streams[key] = stream
		start = true
	}
	stream.subscribers[updates] = struct{}{}
	if stream.latest != nil {
		updates <- *stream.latest
	}
	b.mu.Unlock()
	if start {
		go b.run(stream)
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			current := b.streams[key]
			if current == stream {
				delete(stream.subscribers, updates)
				if len(stream.subscribers) == 0 && !stream.pinned {
					delete(b.streams, key)
					stream.cancel()
				}
			}
			b.mu.Unlock()
		})
	}
	return updates, unsubscribe
}

func (b *analysisBroadcaster) run(stream *sharedAnalysisStream) {
	defer b.removeStoppedStream(stream)
	if stream.startDelay > 0 {
		timer := time.NewTimer(stream.startDelay)
		select {
		case <-stream.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	interval := b.refreshInterval(stream.key.profile)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	b.publish(stream)
	for {
		select {
		case <-stream.ctx.Done():
			return
		case <-ticker.C:
			b.publish(stream)
		}
	}
}

func (b *analysisBroadcaster) refreshInterval(profile string) time.Duration {
	if profile == "summary2" {
		return b.summary2Interval
	}
	return b.defaultInterval
}

func (b *analysisBroadcaster) removeStoppedStream(stream *sharedAnalysisStream) {
	b.mu.Lock()
	if b.streams[stream.key] == stream && stream.ctx.Err() != nil {
		delete(b.streams, stream.key)
	}
	b.mu.Unlock()
}

func analysisRefreshInterval(profile string) time.Duration {
	if profile == "summary2" {
		return summary2AnalysisInterval
	}
	return defaultAnalysisInterval
}

func (b *analysisBroadcaster) publish(stream *sharedAnalysisStream) {
	options := marketanalysis.RequestOptions{}
	if stream.key.profile == "summary2" {
		options.ReactionPreWindowSec = 600
		options.ReactionPostWindowSec = min(stream.key.window, int64(3600))
	}

	var payload marketanalysis.Analysis
	var err error
	if provider, ok := b.provider.(AnalysisOptionsProvider); ok {
		payload, err = provider.AnalysisWithOptions(
			stream.ctx, stream.key.window, stream.key.scope, stream.key.notional, options,
		)
	} else {
		payload, err = b.provider.Analysis(stream.ctx, stream.key.window, stream.key.scope, stream.key.notional)
	}
	frame := sharedAnalysisFrame{payload: payload, err: err}
	if err == nil {
		if b.alreadyPublished(stream, payload) {
			return
		}
		frame.projections = newAnalysisProjectionSet(&frame.payload, time.Now())
		if b.runtimeState != nil {
			frame.state = b.runtimeState.RuntimeState(stream.ctx)
		}
	}
	if frame.err != nil {
		b.logger.Debug("shared websocket analysis failed", "error", frame.err)
	}
	b.broadcast(stream, frame)
}

func (b *analysisBroadcaster) alreadyPublished(stream *sharedAnalysisStream, payload marketanalysis.Analysis) bool {
	observationID := payload.Context.AnalysisContextID
	if observationID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	current := b.streams[stream.key]
	return current == stream && stream.latest != nil && stream.latest.err == nil &&
		stream.latest.payload.Context.AnalysisContextID == observationID
}

func (b *analysisBroadcaster) broadcast(stream *sharedAnalysisStream, frame sharedAnalysisFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.streams[stream.key] != stream {
		return
	}
	// A failed refresh is useful to active clients, but it must not replace the
	// last completed frame replayed to a newly connected dashboard.
	if frame.err == nil {
		stream.latest = &frame
	}
	for subscriber := range stream.subscribers {
		if frame.err != nil {
			// Do not evict an already queued successful replay with a refresh
			// error. An established client with an empty queue still receives the
			// error and can retain its last rendered values.
			select {
			case subscriber <- frame:
			default:
			}
			continue
		}
		select {
		case subscriber <- frame:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- frame:
			default:
			}
		}
	}
}
