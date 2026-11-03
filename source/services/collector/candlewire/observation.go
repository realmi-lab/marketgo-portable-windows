package wscandle

import "time"

// Observation is an in-process batch item. Exactly one value is populated;
// it does not change the persisted JSON representation of that value.
type Observation struct {
	Candle   *Candle
	Ticker   *BithumbTicker
	Chart    *CoinoneChart
	Received time.Time
}
