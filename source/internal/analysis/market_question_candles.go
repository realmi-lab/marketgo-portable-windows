package analysis

// Exact candle totals for a fixed, incomplete market basket. Never WS coverage.
type MarketQuestionCandleComparison struct {
	ExcludedExchanges      map[string]string `json:"excluded_exchanges"`
	Source                 string            `json:"source"`
	Status                 string            `json:"status"`
	Target                 string            `json:"target"`
	StartMS                int64             `json:"start_ms"`
	MiddleMS               int64             `json:"middle_ms"`
	EndMS                  int64             `json:"end_ms"`
	MarketCounts           map[string]int    `json:"market_counts"`
	PreviousVolumeKRW      float64           `json:"previous_volume_krw"`
	CurrentVolumeKRW       float64           `json:"current_volume_krw"`
	VolumeDeltaKRW         float64           `json:"volume_delta_krw"`
	PreviousSharePct       float64           `json:"previous_share_pct"`
	CurrentSharePct        float64           `json:"current_share_pct"`
	ShareDeltaPP           float64           `json:"share_delta_pp"`
	PreviousDenominatorKRW float64           `json:"previous_denominator_krw"`
	CurrentDenominatorKRW  float64           `json:"current_denominator_krw"`
}
