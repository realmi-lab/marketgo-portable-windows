package registry

import (
	"market-share-go/internal/domain"
)

type Snapshot struct {
	Version     uint64                               `json:"version"`
	UpdatedAt   float64                              `json:"updated_at"`
	MarketCount int                                  `json:"market_count"`
	AssetCount  int                                  `json:"asset_count"`
	Exchanges   map[domain.Exchange]ExchangeSnapshot `json:"exchanges"`
}

type ExchangeSnapshot struct {
	MarketCount          int            `json:"market_count"`
	AssetCount           int            `json:"asset_count"`
	Quotes               map[string]int `json:"quotes"`
	PublicFeeMarketCount int            `json:"public_fee_market_count"`
	Error                string         `json:"error"`
}
