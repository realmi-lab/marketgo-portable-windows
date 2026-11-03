package collector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"market-share-go/internal/domain"
)

const (
	frameBatchSize      = 64
	frameBatchQueueSize = 256
	// Raw bytes plus a conservative parsed-envelope allowance, shared by sockets.
	frameBatchMemoryBudget int64 = 32 << 20
	frameBatchDelay              = 2 * time.Millisecond
)

type pendingFrame struct {
	envelope FrameEnvelope
	cost     int64
}

// collectBatchedFrames reads ahead within a bounded process-wide budget. The
// worker commits contiguous connection sequences before publishing health/data.
func (m *Manager) collectBatchedFrames(parent context.Context, connection Connection, name domain.Exchange, connectionID string, universe domain.MarketUniverse, state *chunkState, books *orderBookCoalescer, pingErrors <-chan error) (bool, bool, error) {
	ctx, stopRead := context.WithCancel(parent)
	defer stopRead()
	queue := make(chan pendingFrame, frameBatchQueueSize)
	done := make(chan error, 1)
	go func() {
		var result error
		defer func() { done <- result }()
		for first := range queue {
			batch := []pendingFrame{first}
			timer := time.NewTimer(frameBatchDelay)
		gather:
			for len(batch) < frameBatchSize {
				select {
				case next, ok := <-queue:
					if !ok {
						break gather
					}
					batch = append(batch, next)
				case <-timer.C:
					break gather
				}
			}
			timer.Stop()
			envelopes := make([]FrameEnvelope, len(batch))
			for i := range batch {
				envelopes[i] = batch[i].envelope
			}
			err := m.onBatch(ctx, envelopes)
			if err != nil {
				result = errors.Join(result, fmt.Errorf("persist %s websocket batch: %w", name, err))
				stopRead()
				for _, frame := range batch {
					m.frameBudget.Release(frame.cost)
				}
				// The failed batch is not applied. The owner stops on durability failure.
				for frame := range queue {
					m.frameBudget.Release(frame.cost)
				}
				return
			}
			for _, frame := range batch {
				if result == nil {
					e := frame.envelope
					var parseErr error
					if e.ParseError != "" {
						parseErr = errors.New(e.ParseError)
					}
					if err := m.handleParsedConnectionFrameWithBookCoalescer(ctx, connection, name, universe, e.Frame.Payload, time.Unix(0, e.Frame.ReceivedAtNS), e.Message, parseErr, state, books); err != nil {
						result = err
						stopRead()
					}
				}
				m.frameBudget.Release(frame.cost)
			}
			// After a protocol/processing error, still persist already received frames.
		}
	}()
	var sequence uint64
	var readErr error
readLoop:
	for {
		select {
		case err := <-pingErrors:
			readErr = err
			break readLoop
		case <-ctx.Done():
			readErr = ctx.Err()
			break readLoop
		default:
		}
		readCtx, cancelRead := context.WithTimeout(ctx, readTimeout)
		kind, payload, err := connection.Read(readCtx)
		cancelRead()
		if err != nil {
			readErr = err
			break
		}
		if kind != MessageText && kind != MessageBinary {
			continue
		}
		at := time.Now()
		sequence++
		// A single maximum-sized frame is allowed; the socket message-size limit
		// still applies. Budget pressure blocks reading instead of dropping bytes.
		cost := min(int64(len(payload))*4+1024, frameBatchMemoryBudget)
		if err := m.frameBudget.Acquire(ctx, cost); err != nil {
			readErr = err
			break
		}
		parsed, parseErr := parseConnectionPayload(name, payload, universe, at)
		class, market := classifyJournalFrame(parsed, parseErr)
		envelope := FrameEnvelope{Frame: domain.WebSocketFrame{Exchange: name, ConnectionID: connectionID, Sequence: sequence, MessageType: int16(kind), ReceivedAtNS: at.UnixNano(), Payload: payload, FrameClass: class, Market: market}, Message: parsed}
		if parseErr != nil {
			envelope.ParseError = parseErr.Error()
		}
		select {
		case queue <- pendingFrame{envelope, cost}:
		case <-ctx.Done():
			m.frameBudget.Release(cost)
			readErr = ctx.Err()
			break readLoop
		}
	}
	close(queue)
	workerErr := <-done
	if workerErr != nil {
		return true, false, workerErr
	}
	return true, false, readErr
}
