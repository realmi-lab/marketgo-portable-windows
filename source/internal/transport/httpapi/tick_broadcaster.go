package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const realtimeTickInterval = 500 * time.Millisecond

const maxSharedTickErrorDetailBytes = 256

const (
	sharedTickMessage      = "market_share_tick"
	sharedTickErrorMessage = "market_share_tick_error"
)

type tickBroadcastKey struct {
	window int64
	scope  string
}

type sharedTickFrame struct {
	payload     []byte
	message     []byte
	messageType string
	available   bool
	version     uint64
	versioned   bool
	identity    tickIdentity
}

type tickIdentity [sha256.Size]byte

type sharedTickStream struct {
	key         tickBroadcastKey
	ctx         context.Context
	cancel      context.CancelFunc
	subscribers map[chan sharedTickFrame]struct{}
	latest      *sharedTickFrame
}

// tickBroadcaster owns one producer per (window, scope). Subscribers receive
// the same immutable serialized tick, so collector snapshots, calculations and
// JSON encoding do not scale with the number of browser connections.
type tickBroadcaster struct {
	provider TickProvider
	logger   *slog.Logger
	interval time.Duration

	mu      sync.Mutex
	streams map[tickBroadcastKey]*sharedTickStream
}

func newTickBroadcaster(provider TickProvider, logger *slog.Logger) *tickBroadcaster {
	return newTickBroadcasterWithInterval(provider, logger, realtimeTickInterval)
}

func newTickBroadcasterWithInterval(provider TickProvider, logger *slog.Logger, interval time.Duration) *tickBroadcaster {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = realtimeTickInterval
	}
	return &tickBroadcaster{
		provider: provider,
		logger:   logger,
		interval: interval,
		streams:  make(map[tickBroadcastKey]*sharedTickStream),
	}
}

func (b *tickBroadcaster) subscribe(window int64, scope string) (<-chan sharedTickFrame, func()) {
	key := tickBroadcastKey{window: window, scope: scope}
	updates := make(chan sharedTickFrame, 1)

	b.mu.Lock()
	stream := b.streams[key]
	if stream == nil {
		ctx, cancel := context.WithCancel(context.Background())
		stream = &sharedTickStream{
			key:         key,
			ctx:         ctx,
			cancel:      cancel,
			subscribers: make(map[chan sharedTickFrame]struct{}),
		}
		b.streams[key] = stream
		go b.run(stream)
	}
	stream.subscribers[updates] = struct{}{}
	if stream.latest != nil {
		updates <- *stream.latest
	}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			current := b.streams[key]
			if current == stream {
				delete(stream.subscribers, updates)
				if len(stream.subscribers) == 0 {
					delete(b.streams, key)
					stream.cancel()
				}
			}
			b.mu.Unlock()
		})
	}
	return updates, unsubscribe
}

func (b *tickBroadcaster) run(stream *sharedTickStream) {
	// Keep transport status independent of a blocked provider. Only one provider
	// call may run at a time: a timeout must not accumulate overlapping retries.
	done := make(chan *sharedTickFrame, 1)
	var started time.Time
	pending := false
	start := func() {
		pending = true
		started = time.Now()
		go func() {
			done <- b.loadFrame(stream)
		}()
	}
	start()
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stream.ctx.Done():
			return
		case frame := <-done:
			pending = false
			if frame != nil {
				b.broadcast(stream, *frame)
			}
		case <-ticker.C:
			if !pending {
				start()
			} else if time.Since(started) >= 5*time.Second {
				b.broadcast(stream, unavailableTickFrame(stream.key, "TICK_PROVIDER_STALLED",
					errors.New("realtime calculation has not completed; retaining last valid values")))
			}
		}
	}
}

func (b *tickBroadcaster) publish(stream *sharedTickStream) {
	if frame := b.loadFrame(stream); frame != nil {
		b.broadcast(stream, *frame)
	}
}

func (b *tickBroadcaster) loadFrame(stream *sharedTickStream) *sharedTickFrame {
	value, err := b.provider.RealtimeTick(stream.ctx, stream.key.window, stream.key.scope)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			b.logger.Debug("shared websocket tick failed", "error", err)
			frame := unavailableTickFrame(stream.key, "TICK_PROVIDER_ERROR", err)
			return &frame
		}
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) < 2 || payload[0] != '{' {
		if err == nil {
			err = errors.New("realtime tick must encode as a JSON object")
		}
		b.logger.Debug("shared websocket tick encoding failed", "error", err)
		frame := unavailableTickFrame(stream.key, "TICK_ENCODING_ERROR", err)
		return &frame
	}
	version, versioned := tickVersionFromValue(value)
	if !versioned {
		version, versioned = tickVersionFromJSON(payload)
	}
	identity, err := tickPayloadIdentity(payload)
	if err != nil {
		frame := unavailableTickFrame(stream.key, "TICK_ENCODING_ERROR", err)
		return &frame
	}
	message := typedObjectMessage(sharedTickMessage, payload)
	return &sharedTickFrame{
		payload: payload, messageType: sharedTickMessage, available: true,
		message: message, version: version, versioned: versioned, identity: identity,
	}
}

// A trade/book counter does not change when a window expires or coverage and
// readiness change. Fingerprint the actual published state once per shared
// frame. Only its publication timestamp is excluded; all numerical and status
// fields, including the closed-second boundary, participate in the identity.
func tickPayloadIdentity(payload []byte) (tickIdentity, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return tickIdentity{}, err
	}
	delete(fields, "generated_at")
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	digest := sha256.New()
	for _, key := range keys {
		_, _ = digest.Write([]byte(key))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(fields[key])
		_, _ = digest.Write([]byte{0})
	}
	var identity tickIdentity
	copy(identity[:], digest.Sum(nil))
	return identity, nil
}

func (b *tickBroadcaster) broadcast(stream *sharedTickStream, frame sharedTickFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.streams[stream.key] != stream {
		return
	}
	stream.latest = &frame
	for subscriber := range stream.subscribers {
		select {
		case subscriber <- frame:
		default:
			// The newest tick supersedes an unwritten older tick for this client.
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

func unavailableTickFrame(key tickBroadcastKey, code string, cause error) sharedTickFrame {
	detail := "realtime tick unavailable"
	if cause != nil {
		detail = boundedTickErrorDetail(cause.Error())
	}
	payload, err := json.Marshal(map[string]any{
		"generated_at": float64(time.Now().UnixNano()) / 1e9,
		"status":       "UNAVAILABLE",
		"window_sec":   key.window,
		"market_scope": key.scope,
		"error_code":   code,
		"detail":       detail,
	})
	if err != nil {
		// All values above are primitive JSON values. Keep a valid object as a
		// defensive last resort so writeSharedTickFrame never sees an empty body.
		payload = []byte(`{"status":"UNAVAILABLE","error_code":"TICK_ENCODING_ERROR","detail":"realtime tick unavailable"}`)
	}
	return sharedTickFrame{
		payload: payload, message: typedObjectMessage(sharedTickErrorMessage, payload),
		messageType: sharedTickErrorMessage,
	}
}

func typedObjectMessage(messageType string, payload []byte) []byte {
	if messageType == "" || len(payload) < 2 || payload[0] != '{' {
		return nil
	}
	message := make([]byte, 0, len(payload)+len(messageType)+12)
	message = append(message, `{"type":"`...)
	message = append(message, messageType...)
	message = append(message, `",`...)
	message = append(message, payload[1:]...)
	return message
}

func boundedTickErrorDetail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "realtime tick unavailable"
	}
	if len(value) <= maxSharedTickErrorDetailBytes {
		return value
	}
	limit := maxSharedTickErrorDetailBytes - len("...")
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "..."
}

func tickVersionFromJSON(payload []byte) (uint64, bool) {
	var versioned struct {
		Version *uint64 `json:"version"`
	}
	if err := json.Unmarshal(payload, &versioned); err != nil || versioned.Version == nil {
		return 0, false
	}
	return *versioned.Version, true
}

func tickVersionFromValue(value any) (uint64, bool) {
	if versioned, ok := value.(interface{ TickVersion() uint64 }); ok {
		return versioned.TickVersion(), true
	}
	values, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	switch version := values["version"].(type) {
	case uint64:
		return version, true
	case uint:
		return uint64(version), true
	case int:
		if version >= 0 {
			return uint64(version), true
		}
	case int64:
		if version >= 0 {
			return uint64(version), true
		}
	case float64:
		if version >= 0 && version == float64(uint64(version)) {
			return uint64(version), true
		}
	}
	return 0, false
}
