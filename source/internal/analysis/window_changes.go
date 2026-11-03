package analysis

import (
	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

// WindowChanges compares each rolling window with the window just before it,
// all from one observation, so the windows can be read side by side.
type WindowChanges struct {
	MarketScope string `json:"market_scope"`
	// AsOfSecond is the last closed second every window includes.
	AsOfSecond int64          `json:"as_of_second"`
	Windows    []WindowChange `json:"windows"`
}

// WindowChange is one window's change attribution without its per-coin
// detail. Every number is the attribution's own.
type WindowChange struct {
	WindowSec            int64             `json:"window_sec"`
	Window               string            `json:"window"`
	Status               string            `json:"status"`
	UnavailableReason    string            `json:"unavailable_reason,omitempty"`
	ProvisionalReason    string            `json:"provisional_reason,omitempty"`
	ExcludedExchanges    []domain.Exchange `json:"excluded_exchanges,omitempty"`
	CurrentSharePercent  *float64          `json:"current_share_pct"`
	PreviousSharePercent *float64          `json:"previous_share_pct"`
	ShareChangePP        *float64          `json:"share_change_pp"`
	CurrentVolumeKRW     float64           `json:"current_volume_krw"`
	PreviousVolumeKRW    *float64          `json:"previous_volume_krw"`
	// The trading changes behind the direct and competitor effects.
	BithumbVolumeChangeKRW    *float64              `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW *float64              `json:"competitor_volume_change_krw"`
	BithumbDirectEffectPP     *float64              `json:"bithumb_direct_effect_pp"`
	CompetitorEffectPP        *float64              `json:"competitor_effect_pp"`
	MixEffectPP               *float64              `json:"mix_effect_pp"`
	CaptureEffectPP           *float64              `json:"capture_effect_pp"`
	PrimaryDriver             string                `json:"primary_driver"`
	Exchanges                 []AttributionExchange `json:"exchanges"`
	RisingAssets              []WindowAssetChange   `json:"rising_assets"`
	FallingAssets             []WindowAssetChange   `json:"falling_assets"`
	Breadth                   Breadth               `json:"breadth"`
}

// WindowAssetChange is one coin's part of a window's change.
type WindowAssetChange struct {
	Asset                     string  `json:"asset"`
	ContributionPP            float64 `json:"contribution_pp"`
	BithumbVolumeChangeKRW    float64 `json:"bithumb_volume_change_krw"`
	CompetitorVolumeChangeKRW float64 `json:"competitor_volume_change_krw"`
}

// windowChangeAssetLimit is how many rising and falling coins a window lists.
const windowChangeAssetLimit = 3

// SummarizeWindowChange keeps a window's totals, exchanges and breadth, and
// its largest rising and falling coins.
func SummarizeWindowChange(window int64, attribution Attribution) WindowChange {
	result := WindowChange{
		WindowSec: window, Window: aggregate.WindowName(window),
		Status:                    attribution.Status,
		UnavailableReason:         attribution.UnavailableReason,
		ProvisionalReason:         attribution.ProvisionalReason,
		ExcludedExchanges:         attribution.ExcludedExchanges,
		CurrentSharePercent:       attribution.CurrentSharePercent,
		PreviousSharePercent:      attribution.PreviousSharePercent,
		ShareChangePP:             attribution.ShareChangePP,
		CurrentVolumeKRW:          attribution.CurrentVolumeKRW,
		PreviousVolumeKRW:         attribution.PreviousVolumeKRW,
		BithumbVolumeChangeKRW:    attribution.BithumbVolumeChangeKRW,
		CompetitorVolumeChangeKRW: attribution.CompetitorVolumeChangeKRW,
		BithumbDirectEffectPP:     attribution.BithumbDirectEffectPP,
		CompetitorEffectPP:        attribution.CompetitorEffectPP,
		MixEffectPP:               attribution.MixEffectPP,
		CaptureEffectPP:           attribution.CaptureEffectPP,
		PrimaryDriver:             attribution.PrimaryDriver,
		Exchanges:                 attribution.Exchanges,
		RisingAssets:              []WindowAssetChange{},
		FallingAssets:             []WindowAssetChange{},
		Breadth:                   buildBreadth(attribution.Assets),
	}
	// Assets are ordered by contribution size, so the first ones of each sign
	// are the largest.
	for _, asset := range attribution.Assets {
		change := WindowAssetChange{
			Asset: asset.Asset, ContributionPP: asset.ContributionPP,
			BithumbVolumeChangeKRW: asset.BithumbVolumeChangeKRW, CompetitorVolumeChangeKRW: asset.CompetitorVolumeChangeKRW,
		}
		switch {
		case asset.ContributionPP > 0 && len(result.RisingAssets) < windowChangeAssetLimit:
			result.RisingAssets = append(result.RisingAssets, change)
		case asset.ContributionPP < 0 && len(result.FallingAssets) < windowChangeAssetLimit:
			result.FallingAssets = append(result.FallingAssets, change)
		}
	}
	return result
}
