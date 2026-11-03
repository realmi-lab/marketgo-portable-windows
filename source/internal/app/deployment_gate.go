package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/config"
)

type DeploymentPhase string

const (
	DeploymentPhaseShadow    DeploymentPhase = "SHADOW"
	DeploymentPhasePrepared  DeploymentPhase = "PREPARED"
	DeploymentPhasePromoting DeploymentPhase = "PROMOTING"
	DeploymentPhaseActive    DeploymentPhase = "ACTIVE"
	DeploymentPhaseFault     DeploymentPhase = "FAULT"
)

var (
	ErrDeploymentTransition = errors.New("invalid deployment phase transition")
	ErrDeploymentBufferFull = errors.New("deployment handoff buffer is full")
	ErrDeploymentNotActive  = errors.New("deployment instance is not the active writer")
)

type deploymentLease interface {
	Valid() bool
	Release(context.Context) error
}

type bufferedDeploymentWrite struct {
	name  string
	bytes int64
	run   func(context.Context) error
}

type DeploymentState struct {
	Instance       string          `json:"instance"`
	Phase          DeploymentPhase `json:"phase"`
	WriterLease    bool            `json:"writer_lease"`
	BufferedWrites int             `json:"buffered_writes"`
	BufferedBytes  int64           `json:"buffered_bytes"`
	BufferLimit    int64           `json:"buffer_limit_bytes"`
	InFlightWrites int             `json:"in_flight_writes"`
	LastTransition time.Time       `json:"last_transition"`
	LastError      string          `json:"last_error,omitempty"`
}

// deploymentWriterGate is the serialized ownership boundary for every
// canonical write. SHADOW deliberately discards writes while the candidate
// warms. PREPARED and PROMOTING retain a short, bounded overlap window so the
// winner can flush it after acquiring the PostgreSQL writer lease.
type deploymentWriterGate struct {
	mu                  sync.Mutex
	instance            string
	phase               DeploymentPhase
	leaseRequired       bool
	lease               deploymentLease
	acquire             func(context.Context) (deploymentLease, error)
	bufferLimit         int64
	bufferBytes         int64
	buffer              []bufferedDeploymentWrite
	inFlight            int
	lastTransition      time.Time
	lastError           string
	reconciliationFloor int64
}

func newDeploymentWriterGate(
	ctx context.Context,
	settings config.Config,
	acquire func(context.Context) (deploymentLease, error),
) (*deploymentWriterGate, error) {
	mode := settings.DeploymentMode
	if mode == config.DeploymentManaged {
		active := settings.DeploymentInitialActive
		value, err := os.ReadFile(settings.DeploymentActiveFile)
		if err == nil {
			active = strings.ToLower(strings.TrimSpace(string(value)))
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read managed deployment state: %w", err)
		}
		if active != "blue" && active != "green" {
			return nil, fmt.Errorf("managed deployment state contains invalid active instance %q", active)
		}
		if settings.DeploymentInstance == active {
			mode = config.DeploymentActive
		} else {
			mode = config.DeploymentStandby
		}
	}
	gate := &deploymentWriterGate{
		instance:       settings.DeploymentInstance,
		phase:          DeploymentPhaseActive,
		bufferLimit:    int64(settings.DeploymentBufferMiB) << 20,
		lastTransition: time.Now(),
	}
	if mode == config.DeploymentStandalone {
		return gate, nil
	}
	gate.leaseRequired = true
	gate.acquire = acquire
	if mode == config.DeploymentStandby {
		gate.phase = DeploymentPhaseShadow
		return gate, nil
	}
	if acquire == nil {
		return nil, errors.New("deployment writer lease provider is not configured")
	}
	lease, err := acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("activate deployment writer: %w", err)
	}
	gate.lease = lease
	return gate, nil
}

func (g *deploymentWriterGate) state() DeploymentState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stateLocked()
}

func (g *deploymentWriterGate) stateLocked() DeploymentState {
	leaseValid := !g.leaseRequired
	if g.leaseRequired && g.lease != nil {
		leaseValid = g.lease.Valid()
	}
	return DeploymentState{
		Instance: g.instance, Phase: g.phase, WriterLease: leaseValid,
		BufferedWrites: len(g.buffer), BufferedBytes: g.bufferBytes,
		BufferLimit: g.bufferLimit, InFlightWrites: g.inFlight,
		LastTransition: g.lastTransition, LastError: g.lastError,
	}
}

func (g *deploymentWriterGate) isActive() bool {
	state := g.state()
	return state.Phase == DeploymentPhaseActive && state.WriterLease
}

// dispatch returns executed=false when a SHADOW instance intentionally drops
// the write. A PREPARED instance reports success after accepting a cloned
// operation into its bounded handoff buffer.
func (g *deploymentWriterGate) dispatch(ctx context.Context, operation bufferedDeploymentWrite) (executed bool, err error) {
	if g == nil {
		return true, operation.run(ctx)
	}
	g.mu.Lock()
	switch g.phase {
	case DeploymentPhaseShadow:
		g.mu.Unlock()
		return false, nil
	case DeploymentPhasePrepared, DeploymentPhasePromoting:
		if operation.bytes < 0 || g.bufferBytes > g.bufferLimit-operation.bytes {
			g.phase = DeploymentPhaseFault
			g.lastTransition = time.Now()
			g.lastError = ErrDeploymentBufferFull.Error()
			g.mu.Unlock()
			return false, ErrDeploymentBufferFull
		}
		g.buffer = append(g.buffer, operation)
		g.bufferBytes += operation.bytes
		g.mu.Unlock()
		return false, nil
	case DeploymentPhaseActive:
		if g.leaseRequired && (g.lease == nil || !g.lease.Valid()) {
			g.phase = DeploymentPhaseFault
			g.lastTransition = time.Now()
			g.lastError = "writer lease is not valid"
			g.mu.Unlock()
			return false, errors.New("writer lease is not valid")
		}
		g.inFlight++
		g.mu.Unlock()
		err := operation.run(ctx)
		g.mu.Lock()
		g.inFlight--
		g.mu.Unlock()
		return true, err
	default:
		err := ErrDeploymentNotActive
		if g.lastError != "" {
			err = fmt.Errorf("%w: %s", err, g.lastError)
		}
		g.mu.Unlock()
		return false, err
	}
}

// dispatchActiveOnly protects request/response writes whose result cannot be
// acknowledged before it is actually committed. Unlike background evidence,
// these operations are never queued during the sub-second ownership gap.
func (g *deploymentWriterGate) dispatchActiveOnly(ctx context.Context, operation bufferedDeploymentWrite) error {
	if g == nil {
		return operation.run(ctx)
	}
	g.mu.Lock()
	if g.phase != DeploymentPhaseActive || g.leaseRequired && (g.lease == nil || !g.lease.Valid()) {
		g.mu.Unlock()
		return ErrDeploymentNotActive
	}
	g.inFlight++
	g.mu.Unlock()
	err := operation.run(ctx)
	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return err
}

func (g *deploymentWriterGate) prepare() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase == DeploymentPhasePrepared {
		return nil
	}
	if g.phase != DeploymentPhaseShadow {
		return fmt.Errorf("%w: prepare from %s", ErrDeploymentTransition, g.phase)
	}
	g.phase = DeploymentPhasePrepared
	g.lastTransition = time.Now()
	g.lastError = ""
	return nil
}

func (g *deploymentWriterGate) demote(ctx context.Context) error {
	g.mu.Lock()
	if g.phase == DeploymentPhasePrepared && g.lease == nil {
		g.mu.Unlock()
		return nil
	}
	if g.phase != DeploymentPhaseActive {
		phase := g.phase
		g.mu.Unlock()
		return fmt.Errorf("%w: demote from %s", ErrDeploymentTransition, phase)
	}
	g.phase = DeploymentPhasePrepared
	g.lastTransition = time.Now()
	for g.inFlight > 0 {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			g.mu.Lock()
			// The lease is still held, so fail the attempted transition and
			// leave the old instance as the only active writer.
			g.phase = DeploymentPhaseActive
			g.lastError = ctx.Err().Error()
			g.lastTransition = time.Now()
			g.mu.Unlock()
			return ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		g.mu.Lock()
	}
	lease := g.lease
	g.lease = nil
	g.mu.Unlock()
	if lease != nil {
		if err := lease.Release(ctx); err != nil {
			g.mu.Lock()
			g.phase = DeploymentPhaseFault
			g.lastError = err.Error()
			g.lastTransition = time.Now()
			g.mu.Unlock()
			return err
		}
	}
	return nil
}

func (g *deploymentWriterGate) promote(ctx context.Context) error {
	return g.promoteRebased(ctx, nil)
}

// promoteRebased establishes ownership before reading the final canonical
// checkpoint, but cannot publish ACTIVE or flush old absolute writes until the
// owner has replaced its buckets, matching dedupe and coverage atomically.
func (g *deploymentWriterGate) promoteRebased(ctx context.Context, rebase func(context.Context) error) error {
	g.mu.Lock()
	if g.phase == DeploymentPhaseActive && (!g.leaseRequired || g.lease != nil && g.lease.Valid()) {
		g.mu.Unlock()
		return nil
	}
	if g.phase != DeploymentPhasePrepared {
		phase := g.phase
		g.mu.Unlock()
		return fmt.Errorf("%w: promote from %s", ErrDeploymentTransition, phase)
	}
	g.phase = DeploymentPhasePromoting
	g.lastTransition = time.Now()
	g.mu.Unlock()

	var lease deploymentLease
	var err error
	if g.leaseRequired {
		if g.acquire == nil {
			err = errors.New("deployment writer lease provider is not configured")
		} else {
			lease, err = g.acquire(ctx)
		}
		if err != nil {
			g.promotionFailed(err, nil, 0)
			return err
		}
		g.mu.Lock()
		g.lease = lease
		g.mu.Unlock()
	}
	if rebase != nil {
		if err := rebase(ctx); err != nil {
			return g.failPromotion(ctx, fmt.Errorf("rebase canonical checkpoint: %w", err), lease, nil, 0)
		}
	}

	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return g.failPromotion(ctx, err, lease, nil, 0)
		}
		if g.phase == DeploymentPhaseFault {
			err := errors.New(g.lastError)
			g.mu.Unlock()
			return g.failPromotion(ctx, err, lease, nil, 0)
		}
		if len(g.buffer) == 0 {
			g.phase = DeploymentPhaseActive
			g.lastTransition = time.Now()
			g.lastError = ""
			g.mu.Unlock()
			return nil
		}
		batch := g.buffer
		batchBytes := g.bufferBytes
		g.buffer = nil
		g.bufferBytes = 0
		g.mu.Unlock()

		for index, operation := range batch {
			if err := operation.run(ctx); err != nil {
				remaining := append([]bufferedDeploymentWrite(nil), batch[index:]...)
				return g.failPromotion(ctx, fmt.Errorf("flush buffered %s: %w", operation.name, err), lease, remaining, batchBytes)
			}
			batchBytes -= operation.bytes
		}
	}
}

func (g *deploymentWriterGate) failPromotion(ctx context.Context, cause error, lease deploymentLease, remaining []bufferedDeploymentWrite, remainingBytes int64) error {
	var releaseErr error
	if lease != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		releaseErr = lease.Release(cleanup)
		cancel()
	}
	g.promotionFailed(cause, remaining, remainingBytes)
	if releaseErr != nil {
		g.mu.Lock()
		g.lease = lease
		g.phase = DeploymentPhaseFault
		g.lastError = errors.Join(cause, releaseErr).Error()
		g.mu.Unlock()
	}
	return errors.Join(cause, releaseErr)
}

func (g *deploymentWriterGate) setReconciliationFloor(minute int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reconciliationFloor = max(g.reconciliationFloor, minute)
}

// A warmed accumulator is not an absolute canonical history checkpoint.
// Preserve existing immutable evidence and leave the crossing minute unknown
// instead of flushing a partial old-generation minute or stale complete seal.
func (g *deploymentWriterGate) reconciliationMinuteAllowed(minute int64) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return minute >= g.reconciliationFloor
}

// discardBufferedAggregation is called in the Runtime owner turn immediately
// before its rebased absolute flush. Old derived snapshots are invalid too;
// immutable journal writes are kept.
func staleCheckpointWrite(name string) bool {
	return name == "aggregation" || name == "realtime_flow" || name == "metadata"
}

func (g *deploymentWriterGate) discardBufferedAggregation() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase != DeploymentPhasePromoting {
		return ErrDeploymentTransition
	}
	kept := g.buffer[:0]
	for _, op := range g.buffer {
		if staleCheckpointWrite(op.name) {
			g.bufferBytes -= op.bytes
			continue
		}
		kept = append(kept, op)
	}
	clear(g.buffer[len(kept):])
	g.buffer = kept
	return nil
}

func (g *deploymentWriterGate) replaceBufferedAggregation(ctx context.Context, operation bufferedDeploymentWrite) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.phase != DeploymentPhasePromoting || operation.name != "aggregation" {
		return ErrDeploymentTransition
	}
	retainedBytes := g.bufferBytes
	for _, op := range g.buffer {
		if staleCheckpointWrite(op.name) {
			retainedBytes -= op.bytes
		}
	}
	if operation.bytes < 0 || retainedBytes > g.bufferLimit-operation.bytes {
		return ErrDeploymentBufferFull
	}
	kept := g.buffer[:0]
	for _, op := range g.buffer {
		if !staleCheckpointWrite(op.name) {
			kept = append(kept, op)
		}
	}
	clear(g.buffer[len(kept):])
	g.buffer = append(kept, operation)
	g.bufferBytes = retainedBytes + operation.bytes
	return nil
}

func (g *deploymentWriterGate) promotionFailed(err error, remaining []bufferedDeploymentWrite, remainingBytes int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(remaining) > 0 {
		g.buffer = append(remaining, g.buffer...)
		g.bufferBytes += remainingBytes
	}
	g.lease = nil
	g.phase = DeploymentPhasePrepared
	g.lastTransition = time.Now()
	g.lastError = err.Error()
}

func (g *deploymentWriterGate) shadow(ctx context.Context) error {
	g.mu.Lock()
	if g.phase == DeploymentPhaseActive || g.phase == DeploymentPhasePromoting {
		g.mu.Unlock()
		return fmt.Errorf("%w: shadow from active ownership", ErrDeploymentTransition)
	}
	lease := g.lease
	g.lease = nil
	g.phase = DeploymentPhaseShadow
	g.buffer = nil
	g.bufferBytes = 0
	g.lastError = ""
	g.lastTransition = time.Now()
	g.mu.Unlock()
	if lease != nil {
		return lease.Release(ctx)
	}
	return nil
}

func (g *deploymentWriterGate) close(ctx context.Context) error {
	g.mu.Lock()
	lease := g.lease
	g.lease = nil
	g.phase = DeploymentPhaseShadow
	g.buffer = nil
	g.bufferBytes = 0
	g.lastTransition = time.Now()
	g.mu.Unlock()
	if lease != nil {
		return lease.Release(ctx)
	}
	return nil
}
