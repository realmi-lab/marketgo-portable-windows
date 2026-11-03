package restbudget

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maximumFeedbackDelay = 24 * time.Hour

type StateSnapshot struct {
	Exchange               string        `json:"exchange"`
	Group                  string        `json:"group"`
	OfficialMaxRequests    int           `json:"official_max_requests"`
	OfficialPeriod         time.Duration `json:"official_period"`
	RateFraction           float64       `json:"rate_fraction"`
	HardCapacity           int           `json:"hard_capacity"`
	HardPeriod             time.Duration `json:"hard_period"`
	HardRatePerSecond      float64       `json:"hard_rate_per_sec"`
	EffectiveRatePerSecond float64       `json:"effective_rate_per_sec"`
	RollingWindowCost      int           `json:"rolling_window_cost"`
	QueuedRequests         int           `json:"queued_requests"`
	QueuedCost             int           `json:"queued_cost"`
	TotalRequests          uint64        `json:"total_requests"`
	TotalCost              uint64        `json:"total_cost"`
	AverageWait            time.Duration `json:"average_wait"`
	RateReductions         uint64        `json:"rate_reductions"`
	RecoverySteps          uint64        `json:"recovery_steps"`
	Response429Count       uint64        `json:"response_429_count"`
	Response5xxCount       uint64        `json:"response_5xx_count"`
	TransportErrorCount    uint64        `json:"transport_error_count"`
	BlockedFor             time.Duration `json:"blocked_for"`
	NextRequestIn          time.Duration `json:"next_request_in"`
	LastStatus             *int          `json:"last_status"`
	LastRemaining          *int          `json:"last_remaining"`
	LastReportedLimit      *int          `json:"last_reported_limit"`
	LastHeaderGroup        string        `json:"last_header_group"`
	LastReductionReason    string        `json:"last_reduction_reason"`
	LastRequest            time.Time     `json:"last_request"`
	LastResponse           time.Time     `json:"last_response"`
}

type Snapshot struct {
	RateFraction        float64                  `json:"rate_fraction"`
	HardCeilingFraction float64                  `json:"hard_ceiling_fraction"`
	Closed              bool                     `json:"closed"`
	Groups              map[string]StateSnapshot `json:"groups"`
}

func (m *Manager) ObserveResponse(exchange, group string, status int, headers http.Header) error {
	state, err := m.state(exchange, group)
	if err != nil {
		return err
	}
	now := time.Now()
	remaining, limit, resetAfter, headerGroup := remainingFeedback(state.exchange, headers, now)
	retryAfter := retryAfter(headers, now)
	if resetAfter > retryAfter {
		retryAfter = resetAfter
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastStatus = intPointer(status)
	state.lastResponse = now
	state.lastRemaining = remaining
	state.lastReportedLimit = limit
	state.lastHeaderGroup = headerGroup

	switch {
	case status == http.StatusTooManyRequests:
		state.response429++
		reduce(state, 0.5, "HTTP_429", now, maximumDuration(retryAfter, minDuration(time.Second, state.hardPeriod)))
		return nil
	case status >= 500:
		state.response5xx++
		minimumBlock := time.Duration(float64(time.Second) / math.Max(state.effectiveRate, 0.05))
		reduce(state, 0.75, "HTTP_"+strconv.Itoa(status), now, maximumDuration(retryAfter, minimumBlock))
		return nil
	}

	if remaining != nil {
		referenceLimit := state.policy.MaxRequests
		if limit != nil {
			referenceLimit = *limit
		}
		if *remaining <= 0 {
			reduce(state, 0.5, "REMAINING_EXHAUSTED", now, maximumDuration(resetAfter, minDuration(time.Second, state.hardPeriod)))
			return nil
		}
		if referenceLimit > 0 && float64(*remaining)/float64(referenceLimit) <= 0.1 {
			reduce(state, 0.8, "REMAINING_LOW", now, 0)
			return nil
		}
	}
	if status >= 200 && status < 400 {
		state.consecutiveHealthy++
		if state.consecutiveHealthy >= 20 && state.effectiveRate < state.hardRate {
			state.effectiveRate = math.Min(state.hardRate, state.effectiveRate*1.1)
			state.recoverySteps++
			state.consecutiveHealthy = 0
		}
	}
	wake(state.notify)
	return nil
}

func (m *Manager) ObserveTransportError(exchange, group string) error {
	state, err := m.state(exchange, group)
	if err != nil {
		return err
	}
	now := time.Now()
	state.mu.Lock()
	state.transportErrors++
	block := time.Duration(float64(time.Second) / math.Max(state.effectiveRate, 0.05))
	reduce(state, 0.75, "TRANSPORT_ERROR", now, block)
	state.mu.Unlock()
	wake(state.notify)
	return nil
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	states := make(map[string]*budgetState, len(m.states))
	for key, state := range m.states {
		states[key] = state
	}
	closed := m.closed
	fraction := m.fraction
	ceiling := m.ceiling
	m.mu.Unlock()

	now := time.Now()
	result := Snapshot{
		RateFraction: fraction, HardCeilingFraction: ceiling,
		Closed: closed, Groups: make(map[string]StateSnapshot, len(states)),
	}
	for _, state := range states {
		state.mu.Lock()
		rollingDelay(state, now, 0)
		queuedCost := 0
		queuedRequests := 0
		for _, request := range state.waiters {
			if request.ctx.Err() == nil {
				queuedRequests++
				queuedCost += request.cost
			}
		}
		rollingCost := 0
		for _, item := range state.history[state.historyHead:] {
			rollingCost += item.cost
		}
		averageWait := time.Duration(0)
		if state.totalRequests > 0 {
			averageWait = state.totalWait / time.Duration(state.totalRequests)
		}
		key := state.exchange + ":" + state.group
		result.Groups[key] = StateSnapshot{
			Exchange: state.exchange, Group: state.group,
			OfficialMaxRequests: state.policy.MaxRequests, OfficialPeriod: state.policy.Period,
			RateFraction: state.fraction, HardCapacity: state.hardCapacity,
			HardPeriod: state.hardPeriod, HardRatePerSecond: state.hardRate,
			EffectiveRatePerSecond: state.effectiveRate,
			RollingWindowCost:      rollingCost, QueuedRequests: queuedRequests, QueuedCost: queuedCost,
			TotalRequests: state.totalRequests, TotalCost: state.totalCost, AverageWait: averageWait,
			RateReductions: state.rateReductions, RecoverySteps: state.recoverySteps,
			Response429Count: state.response429, Response5xxCount: state.response5xx,
			TransportErrorCount: state.transportErrors,
			BlockedFor:          maxDuration(0, state.blockedUntil.Sub(now)),
			NextRequestIn:       maxDuration(0, state.nextAllowed.Sub(now)),
			LastStatus:          copyInt(state.lastStatus), LastRemaining: copyInt(state.lastRemaining),
			LastReportedLimit: copyInt(state.lastReportedLimit), LastHeaderGroup: state.lastHeaderGroup,
			LastReductionReason: state.lastReductionReason,
			LastRequest:         state.lastRequest, LastResponse: state.lastResponse,
		}
		state.mu.Unlock()
	}
	return result
}

func reduce(state *budgetState, factor float64, reason string, now time.Time, blockFor time.Duration) {
	minimum := math.Max(0.05, state.hardRate*0.05)
	lowered := math.Max(minimum, state.effectiveRate*factor)
	if lowered < state.effectiveRate-1e-12 {
		state.effectiveRate = lowered
		state.rateReductions++
		state.lastReductionReason = reason
	}
	state.consecutiveHealthy = 0
	blockedUntil := now.Add(blockFor)
	if blockFor > 0 && blockedUntil.After(state.blockedUntil) {
		state.blockedUntil = blockedUntil
	}
}

func remainingFeedback(exchange string, headers http.Header, now time.Time) (*int, *int, time.Duration, string) {
	var remaining, limit *int
	var resetAfter time.Duration
	headerGroup := ""
	if value := headers.Get("Remaining-Req"); value != "" {
		for _, field := range splitHeaderFields(value) {
			key, raw, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "group":
				headerGroup = strings.ToLower(strings.TrimSpace(raw))
			case "sec":
				remaining = nonnegativeInteger(raw)
			}
		}
	}
	if exchange == "korbit" {
		for _, field := range splitHeaderFields(headers.Get("Ratelimit")) {
			key, raw, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "remaining":
				remaining = nonnegativeInteger(raw)
			case "limit":
				limit = nonnegativeInteger(raw)
			case "reset":
				resetAfter = durationHeader(raw, now)
			}
		}
	}
	for _, name := range []string{"Public-Ratelimit-Remaining", "X-Ratelimit-Remaining", "Ratelimit-Remaining"} {
		if value := nonnegativeInteger(headers.Get(name)); value != nil {
			remaining = value
			break
		}
	}
	for _, name := range []string{"Public-Ratelimit-Limit", "X-Ratelimit-Limit", "X-Ratelimit-Burst-Capacity", "Ratelimit-Limit"} {
		if value := nonnegativeInteger(headers.Get(name)); value != nil {
			limit = value
			break
		}
	}
	for _, name := range []string{"X-Ratelimit-Reset-After", "Public-Ratelimit-Reset", "X-Ratelimit-Reset", "Ratelimit-Reset"} {
		if value := durationHeader(headers.Get(name), now); value > resetAfter {
			resetAfter = value
		}
	}
	return remaining, limit, resetAfter, headerGroup
}

func retryAfter(headers http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil || math.IsInf(seconds, 1) {
		return secondsDuration(seconds)
	}
	date, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return boundedFeedbackDelay(date.Sub(now))
}

func durationHeader(value string, now time.Time) time.Duration {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if math.IsInf(parsed, 1) {
		return maximumFeedbackDelay
	}
	if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, -1) {
		return 0
	}
	if parsed > 10_000_000_000 {
		if parsed >= float64(math.MaxInt64) {
			return maximumFeedbackDelay
		}
		return boundedFeedbackDelay(time.UnixMilli(int64(parsed)).Sub(now))
	}
	if parsed > 1_000_000_000 {
		if parsed >= float64(math.MaxInt64) {
			return maximumFeedbackDelay
		}
		return boundedFeedbackDelay(time.Unix(int64(parsed), 0).Sub(now))
	}
	return secondsDuration(parsed)
}

func secondsDuration(seconds float64) time.Duration {
	if math.IsInf(seconds, 1) {
		return maximumFeedbackDelay
	}
	if !finitePositive(seconds) {
		return 0
	}
	if seconds >= maximumFeedbackDelay.Seconds() {
		return maximumFeedbackDelay
	}
	return time.Duration(seconds * float64(time.Second))
}

func boundedFeedbackDelay(value time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	if value > maximumFeedbackDelay {
		return maximumFeedbackDelay
	}
	return value
}

func nonnegativeInteger(value string) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	if parsed < 0 {
		parsed = 0
	}
	return &parsed
}

func splitHeaderFields(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == ';' || character == ',' || character == ' ' || character == '\t'
	})
}

func intPointer(value int) *int { return &value }

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
