package official

import ()

type BinanceTicker24hAsset struct {
	Asset                 string   `json:"asset"`
	Market                string   `json:"market"`
	QuoteAsset            string   `json:"quote_asset"`
	QuoteVolumeUSDT       float64  `json:"quote_volume_usdt"`
	LastPriceUSDT         float64  `json:"last_price_usdt"`
	BestBidPriceUSDT      float64  `json:"best_bid_price_usdt"`
	BestBidQuantity       float64  `json:"best_bid_quantity"`
	BestAskPriceUSDT      float64  `json:"best_ask_price_usdt"`
	BestAskQuantity       float64  `json:"best_ask_quantity"`
	EstimatedVolumeKRW    *float64 `json:"estimated_volume_krw"`
	EstimatedLastPriceKRW *float64 `json:"estimated_last_price_krw"`
	EstimatedBestBidKRW   *float64 `json:"estimated_best_bid_krw"`
	EstimatedBestAskKRW   *float64 `json:"estimated_best_ask_krw"`
	// Premium identity metadata is attached by the app after it compares the
	// exact Binance and Bithumb registry markets. The raw ticker service never
	// claims that equal base symbols prove equal contracts.
	PremiumIdentityStatus   string `json:"premium_identity_status"`
	PremiumIdentityVerified bool   `json:"premium_identity_verified"`
	PremiumBithumbMarket    string `json:"premium_bithumb_market,omitempty"`
	// PremiumBithumbMidKRW is populated only from the exact Bithumb KRW
	// order-book market named by PremiumBithumbMarket. It stays nil when the
	// Binance reference or the Bithumb book is stale or otherwise ineligible.
	PremiumBithumbMidKRW *float64 `json:"premium_bithumb_mid_krw"`
	PremiumBPS           *float64 `json:"premium_bps"`
	PremiumStatus        string   `json:"premium_status"`
	OpenTimeMS           int64    `json:"open_time_ms"`
	CloseTimeMS          int64    `json:"close_time_ms"`
}

type BinanceTicker24hSummary struct {
	Status              string  `json:"status"`
	Source              string  `json:"source"`
	QuoteAsset          string  `json:"quote_asset"`
	WindowSec           int64   `json:"window_sec"`
	AsOf                float64 `json:"as_of"`
	ExpectedMarketCount int     `json:"expected_market_count"`
	ReadyMarketCount    int     `json:"ready_market_count"`
	// ObservedQuoteVolumeUSDT remains a lower bound when Status is PARTIAL.
	// TotalQuoteVolumeUSDT is published only for an exact complete market set.
	ObservedQuoteVolumeUSDT float64                 `json:"observed_quote_volume_usdt"`
	TotalQuoteVolumeUSDT    *float64                `json:"total_quote_volume_usdt"`
	EstimatedTotalVolumeKRW *float64                `json:"estimated_total_volume_krw"`
	USDTKRWRate             *float64                `json:"usdt_krw_rate"`
	ConversionAsOf          *float64                `json:"conversion_as_of"`
	ConversionStatus        string                  `json:"conversion_status"`
	Assets                  []BinanceTicker24hAsset `json:"assets"`
	LastError               *string                 `json:"last_error"`
}
