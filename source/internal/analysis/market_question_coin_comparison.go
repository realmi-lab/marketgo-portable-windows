package analysis

// Coin comparison is a same-window projection, never an attribution ranking.
type MarketQuestionCoinComparison struct {
	Status          string                            `json:"status"`
	Target          string                            `json:"target"`
	Peer            string                            `json:"peer"`
	StartMS         int64                             `json:"start_ms"`
	EndMS           int64                             `json:"end_ms"`
	Denominator     string                            `json:"denominator"`
	TargetVolumeKRW *float64                          `json:"target_volume_krw"`
	PeerVolumeKRW   *float64                          `json:"peer_volume_krw"`
	TargetSharePct  *float64                          `json:"target_share_pct"`
	PeerSharePct    *float64                          `json:"peer_share_pct"`
	EligibleCount   int                               `json:"eligible_count"`
	LeadingCount    int                               `json:"leading_count"`
	Rows            []MarketQuestionCoinComparisonRow `json:"rows"`
}

type MarketQuestionCoinComparisonRow struct {
	Asset              string   `json:"asset"`
	TargetVolumeKRW    float64  `json:"target_volume_krw"`
	PeerVolumeKRW      float64  `json:"peer_volume_krw"`
	TargetBaseQuantity *float64 `json:"target_base_quantity"`
	PeerBaseQuantity   *float64 `json:"peer_base_quantity"`
	TargetSharePct     float64  `json:"target_share_pct"`
	PeerSharePct       float64  `json:"peer_share_pct"`
	ShareGapPP         float64  `json:"share_gap_pp"`
}
