package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	tickReferenceInterval = 5 * time.Second
	realtimeFlowInterval  = time.Second
	whaleAlertInterval    = 250 * time.Millisecond
	// The live coin shares move on by one second per call, so asking several
	// times a second sends each second soon after it closes and catches up
	// after a slow read.
	liveCoinSharesInterval = 250 * time.Millisecond
)

type sharedStateFrame struct {
	messageType string
	field       string
	payload     []byte
	message     []byte
}

type sharedStateStream struct {
	ctx         context.Context
	cancel      context.CancelFunc
	subscribers map[chan sharedStateFrame]struct{}
	latest      *sharedStateFrame
}

// stateBroadcaster serializes one independently refreshed state snapshot and
// shares the exact immutable bytes across all WebSocket clients. Identical
// polls are suppressed, while a late subscriber still receives the latest
// complete snapshot immediately.
type stateBroadcaster struct {
	provider    func(context.Context) any
	messageType string
	field       string
	interval    time.Duration
	logger      *slog.Logger

	mu     sync.Mutex
	stream *sharedStateStream
}

func newStateBroadcaster(provider func(context.Context) any, messageType, field string, interval time.Duration, logger *slog.Logger) *stateBroadcaster {
	if provider == nil {
		return nil
	}
	if interval <= 0 {
		interval = time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &stateBroadcaster{
		provider: provider, messageType: messageType, field: field,
		interval: interval, logger: logger,
	}
}

func (b *stateBroadcaster) subscribe() (<-chan sharedStateFrame, func()) {
	updates := make(chan sharedStateFrame, 1)
	b.mu.Lock()
	stream := b.stream
	if stream == nil {
		ctx, cancel := context.WithCancel(context.Background())
		stream = &sharedStateStream{
			ctx: ctx, cancel: cancel,
			subscribers: make(map[chan sharedStateFrame]struct{}),
		}
		b.stream = stream
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
			if b.stream == stream {
				delete(stream.subscribers, updates)
				if len(stream.subscribers) == 0 {
					b.stream = nil
					stream.cancel()
				}
			}
			b.mu.Unlock()
		})
	}
	return updates, unsubscribe
}

func (b *stateBroadcaster) run(stream *sharedStateStream) {
	b.publish(stream)
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stream.ctx.Done():
			return
		case <-ticker.C:
			b.publish(stream)
		}
	}
}

func (b *stateBroadcaster) publish(stream *sharedStateStream) {
	if stream.ctx.Err() != nil {
		return
	}
	value := b.provider(stream.ctx)
	if stream.ctx.Err() != nil {
		return
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) < 2 || payload[0] != '{' {
		if err == nil {
			err = errors.New("shared state must encode as a JSON object")
		}
		b.logger.Debug("shared websocket state encoding failed", "type", b.messageType, "error", err)
		return
	}
	frame := sharedStateFrame{
		messageType: b.messageType, field: b.field, payload: payload,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stream != stream {
		return
	}
	if stream.latest != nil && bytes.Equal(stream.latest.payload, frame.payload) {
		return
	}
	// Unchanged polls need no second allocation/copy of the entire payload.
	frame.message = typedStateMessage(b.messageType, b.field, payload)
	stream.latest = &frame
	for subscriber := range stream.subscribers {
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

func typedStateMessage(messageType, field string, payload []byte) []byte {
	if messageType == "" || field == "" || len(payload) < 2 || payload[0] != '{' {
		return nil
	}
	message := make([]byte, 0, len(payload)+len(messageType)+len(field)+24)
	message = append(message, `{"type":"`...)
	message = append(message, messageType...)
	message = append(message, `","`...)
	message = append(message, field...)
	message = append(message, `":`...)
	message = append(message, payload...)
	message = append(message, '}')
	return message
}
