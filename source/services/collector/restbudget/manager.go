package restbudget

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// MaxRateFraction is the production-runtime ceiling. Long-running live
	// collectors retain half of the published exchange quota so they leave
	// headroom for other processes sharing the same IP or account.
	MaxRateFraction = 0.5
	// BackfillRateFraction is reserved for an explicit, bounded backfill
	// process. It must never be used by the live service.
	BackfillRateFraction = 0.8
)

const (
	PriorityGapRepair    = 0
	PriorityRetry        = 10
	PriorityTicker       = 20
	PriorityActiveMarket = 30
	PriorityRoundRobin   = 40
	PriorityRegistry     = 50
)

var ErrClosed = errors.New("REST budget manager is closed")

type Policy struct {
	MaxRequests int
	Period      time.Duration
}

func (p Policy) validate() error {
	if p.MaxRequests <= 0 {
		return errors.New("max requests must be positive")
	}
	if p.Period <= 0 {
		return errors.New("period must be positive")
	}
	return nil
}

var OfficialPolicies = map[string]Policy{
	"upbit":   {MaxRequests: 10, Period: time.Second},
	"bithumb": {MaxRequests: 150, Period: time.Second},
	"coinone": {MaxRequests: 1200, Period: time.Minute},
	"korbit":  {MaxRequests: 50, Period: time.Second},
	"gopax":   {MaxRequests: 20, Period: time.Second},
	// Public announcement pages do not expose one stable shared quota. These
	// are deliberately conservative application caps, further halved by the
	// manager's default rate fraction.
	"binance": {MaxRequests: 2, Period: time.Second},
	"bybit":   {MaxRequests: 2, Period: time.Second},
	"okx":     {MaxRequests: 2, Period: time.Second},
}

type Lease struct {
	Exchange      string
	Group         string
	Cost          int
	Priority      int
	RequestedAt   time.Time
	GrantedAt     time.Time
	GrantSequence uint64
}

func (l Lease) Waited() time.Duration {
	if l.GrantedAt.Before(l.RequestedAt) {
		return 0
	}
	return l.GrantedAt.Sub(l.RequestedAt)
}

type Manager struct {
	ctx      context.Context
	cancel   context.CancelFunc
	fraction float64
	ceiling  float64
	policies map[string]Policy

	mu     sync.Mutex
	states map[string]*budgetState
	closed bool
	waiter atomic.Uint64
	grant  atomic.Uint64
}

type budgetState struct {
	mu sync.Mutex

	exchange string
	group    string
	policy   Policy
	fraction float64

	hardCapacity  int
	hardPeriod    time.Duration
	hardRate      float64
	effectiveRate float64
	waiters       waiterHeap
	history       []usage
	historyHead   int
	nextAllowed   time.Time
	blockedUntil  time.Time
	notify        chan struct{}

	totalRequests       uint64
	totalCost           uint64
	totalWait           time.Duration
	rateReductions      uint64
	recoverySteps       uint64
	response429         uint64
	response5xx         uint64
	transportErrors     uint64
	consecutiveHealthy  int
	lastStatus          *int
	lastRemaining       *int
	lastReportedLimit   *int
	lastHeaderGroup     string
	lastRequest         time.Time
	lastResponse        time.Time
	lastReductionReason string
}

type usage struct {
	at   time.Time
	cost int
}

type waiter struct {
	ctx         context.Context
	priority    int
	sequence    uint64
	requestedAt time.Time
	cost        int
	response    chan leaseResult
}

type leaseResult struct {
	lease Lease
	err   error
}

type waiterHeap []*waiter

func (h waiterHeap) Len() int { return len(h) }
func (h waiterHeap) Less(i, j int) bool {
	if h[i].priority == h[j].priority {
		return h[i].sequence < h[j].sequence
	}
	return h[i].priority < h[j].priority
}
func (h waiterHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *waiterHeap) Push(value any) { *h = append(*h, value.(*waiter)) }
func (h *waiterHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func New(parent context.Context, rateFraction float64, policies map[string]Policy) (*Manager, error) {
	return newManager(parent, rateFraction, MaxRateFraction, policies)
}

// NewBackfill creates a manager for an explicit bounded recovery run. The
// manager still observes response headers and reduces its effective rate on
// quota pressure, but it may use at most 80% of the published official quota.
func NewBackfill(parent context.Context, policies map[string]Policy) (*Manager, error) {
	return newManager(parent, BackfillRateFraction, BackfillRateFraction, policies)
}

// NewBackfillAt creates an explicit bounded backfill manager at the requested
// fraction of the published quota. The hard 80% backfill ceiling still
// applies, while callers may reserve more headroom for a concurrently running
// live service by selecting a lower fraction such as 0.7.
func NewBackfillAt(parent context.Context, rateFraction float64, policies map[string]Policy) (*Manager, error) {
	return newManager(parent, rateFraction, BackfillRateFraction, policies)
}

func newManager(parent context.Context, rateFraction, ceiling float64, policies map[string]Policy) (*Manager, error) {
	if !finitePositive(rateFraction) {
		return nil, errors.New("rate fraction must be finite and positive")
	}
	if !finitePositive(ceiling) || ceiling > 1 {
		return nil, errors.New("rate ceiling must be finite and in (0,1]")
	}
	if rateFraction > ceiling {
		rateFraction = ceiling
	}
	if policies == nil {
		policies = OfficialPolicies
	}
	policyCopy := make(map[string]Policy, len(policies))
	for exchange, policy := range policies {
		if err := policy.validate(); err != nil {
			return nil, fmt.Errorf("%s policy: %w", exchange, err)
		}
		policyCopy[strings.ToLower(strings.TrimSpace(exchange))] = policy
	}
	ctx, cancel := context.WithCancel(parent)
	return &Manager{
		ctx: ctx, cancel: cancel, fraction: rateFraction, ceiling: ceiling,
		policies: policyCopy, states: make(map[string]*budgetState),
	}, nil
}

func (m *Manager) Acquire(ctx context.Context, exchange, group string, cost, priority int) (Lease, error) {
	if cost <= 0 {
		return Lease{}, errors.New("request cost must be positive")
	}
	state, err := m.state(exchange, group)
	if err != nil {
		return Lease{}, err
	}
	if cost > state.hardCapacity {
		return Lease{}, fmt.Errorf(
			"request cost %d exceeds %s:%s hard capacity %d",
			cost, state.exchange, state.group, state.hardCapacity,
		)
	}
	request := &waiter{
		ctx: ctx, priority: priority, sequence: m.waiter.Add(1),
		requestedAt: time.Now(), cost: cost, response: make(chan leaseResult, 1),
	}
	state.mu.Lock()
	heap.Push(&state.waiters, request)
	state.mu.Unlock()
	wake(state.notify)

	select {
	case result := <-request.response:
		return result.lease, result.err
	case <-ctx.Done():
		wake(state.notify)
		return Lease{}, ctx.Err()
	case <-m.ctx.Done():
		return Lease{}, ErrClosed
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	states := make([]*budgetState, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, state)
	}
	m.mu.Unlock()
	for _, state := range states {
		wake(state.notify)
	}
}

func (m *Manager) state(exchange, group string) (*budgetState, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	group = CanonicalGroup(exchange, group)
	key := exchange + "\x00" + group
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if state := m.states[key]; state != nil {
		return state, nil
	}
	policy, ok := m.policies[exchange]
	if !ok {
		return nil, fmt.Errorf("unsupported official REST exchange %q", exchange)
	}
	hardRate := float64(policy.MaxRequests) / policy.Period.Seconds() * m.fraction
	capacity := int(math.Floor(float64(policy.MaxRequests) * m.fraction))
	hardPeriod := policy.Period
	if capacity < 1 {
		capacity = 1
		hardPeriod = time.Duration(float64(time.Second) / hardRate)
	}
	state := &budgetState{
		exchange: exchange, group: group, policy: policy, fraction: m.fraction,
		hardCapacity: capacity, hardPeriod: hardPeriod,
		hardRate: hardRate, effectiveRate: hardRate,
		nextAllowed: time.Now(), notify: make(chan struct{}, 1),
	}
	heap.Init(&state.waiters)
	m.states[key] = state
	go m.dispatch(state)
	return state, nil
}

func (m *Manager) dispatch(state *budgetState) {
	for {
		request, delay := nextRequest(state)
		if request == nil {
			select {
			case <-m.ctx.Done():
				failWaiters(state, ErrClosed)
				return
			case <-state.notify:
				continue
			}
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-m.ctx.Done():
				timer.Stop()
				failWaiters(state, ErrClosed)
				return
			case <-state.notify:
				timer.Stop()
				continue
			case <-timer.C:
				continue
			}
		}
		grant := m.grantRequest(state, request)
		select {
		case request.response <- leaseResult{lease: grant}:
		case <-request.ctx.Done():
		case <-m.ctx.Done():
			failWaiters(state, ErrClosed)
			return
		}
	}
}

func nextRequest(state *budgetState) (*waiter, time.Duration) {
	state.mu.Lock()
	defer state.mu.Unlock()
	for state.waiters.Len() > 0 && state.waiters[0].ctx.Err() != nil {
		heap.Pop(&state.waiters)
	}
	if state.waiters.Len() == 0 {
		return nil, 0
	}
	now := time.Now()
	request := state.waiters[0]
	delay := maximumDuration(
		time.Until(state.nextAllowed),
		time.Until(state.blockedUntil),
		rollingDelay(state, now, request.cost),
	)
	if delay > 0 {
		return request, delay
	}
	return heap.Pop(&state.waiters).(*waiter), 0
}

func (m *Manager) grantRequest(state *budgetState, request *waiter) Lease {
	now := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.history = append(state.history, usage{at: now, cost: request.cost})
	state.totalRequests++
	state.totalCost += uint64(request.cost)
	state.totalWait += maxDuration(0, now.Sub(request.requestedAt))
	state.lastRequest = now
	rate := math.Min(state.hardRate, state.effectiveRate)
	interval := time.Duration(float64(time.Second) * float64(request.cost) / math.Max(rate, 1e-9))
	if state.nextAllowed.Before(now) {
		state.nextAllowed = now
	}
	state.nextAllowed = state.nextAllowed.Add(interval)
	return Lease{
		Exchange: state.exchange, Group: state.group, Cost: request.cost,
		Priority: request.priority, RequestedAt: request.requestedAt, GrantedAt: now,
		GrantSequence: m.grant.Add(1),
	}
}

func rollingDelay(state *budgetState, now time.Time, cost int) time.Duration {
	cutoff := now.Add(-state.hardPeriod)
	for state.historyHead < len(state.history) && !state.history[state.historyHead].at.After(cutoff) {
		state.historyHead++
	}
	// Advancing a logical head keeps the steady-state request path O(1) and
	// allocation-free as entries expire. Compact only occasionally so a long-
	// lived budget cannot retain an ever-growing backing array.
	if state.historyHead == len(state.history) {
		clear(state.history)
		state.history = state.history[:0]
		state.historyHead = 0
	} else if state.historyHead >= 64 && state.historyHead*2 >= len(state.history) {
		retained := copy(state.history, state.history[state.historyHead:])
		clear(state.history[retained:])
		state.history = state.history[:retained]
		state.historyHead = 0
	}
	used := 0
	for _, item := range state.history[state.historyHead:] {
		used += item.cost
	}
	needed := used + cost - state.hardCapacity
	if needed <= 0 {
		return 0
	}
	released := 0
	for _, item := range state.history[state.historyHead:] {
		released += item.cost
		if released >= needed {
			return maxDuration(0, item.at.Add(state.hardPeriod).Sub(now))
		}
	}
	return state.hardPeriod
}

func failWaiters(state *budgetState, err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	for state.waiters.Len() > 0 {
		request := heap.Pop(&state.waiters).(*waiter)
		select {
		case request.response <- leaseResult{err: err}:
		default:
		}
	}
}

func CanonicalGroup(exchange, group string) string {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	group = strings.ToLower(strings.TrimSpace(group))
	if exchange != "upbit" {
		return "public"
	}
	switch group {
	case "candle", "candles":
		return "candle"
	case "ticker", "tickers":
		return "ticker"
	case "market", "markets", "registry":
		return "market"
	case "trade", "trades":
		return "trade"
	case "", "default", "public":
		return "default"
	default:
		return group
	}
}

func wake(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}

func maximumDuration(values ...time.Duration) time.Duration {
	var result time.Duration
	for _, value := range values {
		if value > result {
			result = value
		}
	}
	return result
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
