package analysis

// Reset removes derived observations from a previous producer generation. It
// never changes the underlying trade history or official-candle collectors.
func (a *RealtimeFlowAnalyzer) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.frames = nil
	a.state = NewRealtimeFlowAnalyzer(a.config).state
}

// Reset starts a new movement baseline without replacing the shared pointer.
func (t *RollingMovementTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.samples = nil
	t.movement = nil
}
