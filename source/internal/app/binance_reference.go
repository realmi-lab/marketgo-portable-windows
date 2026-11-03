package app

import (
	"math"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
	"market-share-go/internal/orderbook"
)

const (
	premiumIdentityVerified         = "VERIFIED_EXACT_MARKETS"
	premiumIdentitySymbolOnly       = "SYMBOL_MATCH_UNVERIFIED"
	premiumIdentityProofMissing     = "EXACT_MARKET_PROOF_MISSING"
	premiumIdentityConflict         = "CONTRACT_IDENTITY_CONFLICT"
	premiumIdentityBithumbMissing   = "BITHUMB_KRW_MARKET_UNAVAILABLE"
	premiumIdentityBithumbAmbiguous = "AMBIGUOUS_BITHUMB_KRW_MARKET"

	premiumReferenceLive             = "LIVE"
	premiumReferenceStaleSummary     = "STALE_BINANCE_SUMMARY"
	premiumReferenceStaleClose       = "STALE_BINANCE_CLOSE_TIME"
	premiumReferenceIdentityRejected = "IDENTITY_NOT_ELIGIBLE"
	premiumReferencePriceUnavailable = "BITHUMB_PRICE_UNAVAILABLE"
	premiumReferencePriceQuarantined = "BITHUMB_PRICE_QUARANTINED"
)

// officialBinance24hSnapshot keeps Binance as a reference-only data source.
// The optional KRW values are estimates at the current Bithumb USDT/KRW
// volume-conversion rate; they never enter the rolling share aggregates.
func (s *Service) officialBinance24hSnapshot(now time.Time) official.BinanceTicker24hSummary {
	if s.binanceTicker == nil {
		return official.BinanceTicker24hSummary{}
	}
	snapshot := s.binanceTicker.Snapshot()
	if s.runtime != nil && s.runtime.oracle != nil {
		if now.IsZero() {
			now = time.Now()
		}
		resolution := s.runtime.oracle.ResolveVolumeToKRW("USDT", domain.UnixMilli(now.UnixMilli()))
		if resolution != nil {
			rate := resolution.Rate
			rateAsOf := now.Add(-time.Duration(max(0, resolution.MaxAgeMS)) * time.Millisecond)
			snapshot = official.ConvertBinanceTicker24hToEstimatedKRW(snapshot, &rate, rateAsOf)
		}
	}
	markets := []domain.MarketPair(nil)
	if s.registry != nil {
		markets = s.registry.Universe().Markets()
	}
	return attachBinancePremiumIdentity(snapshot, markets, s.contractIdentities)
}

// attachBinancePremiumIdentity distinguishes a useful symbol-level reference
// from independently reviewed exact-market identity. A missing proof does not
// suppress the user-requested premium display, but the API marks it as
// unverified. A known conflict or ambiguous Bithumb market is never eligible.
func attachBinancePremiumIdentity(
	summary official.BinanceTicker24hSummary,
	markets []domain.MarketPair,
	proofs map[string]marketanalysis.ContractIdentityProof,
) official.BinanceTicker24hSummary {
	summary.Assets = append([]official.BinanceTicker24hAsset(nil), summary.Assets...)
	bithumbByAsset := make(map[string][]domain.MarketPair)
	for _, market := range markets {
		asset := strings.ToUpper(strings.TrimSpace(market.Base))
		if market.Scope != domain.ScopeDomestic || market.Exchange != domain.ExchangeBithumb ||
			!strings.EqualFold(strings.TrimSpace(market.Quote), "KRW") || asset == "" {
			continue
		}
		bithumbByAsset[asset] = append(bithumbByAsset[asset], market)
	}
	for index := range summary.Assets {
		row := &summary.Assets[index]
		asset := strings.ToUpper(strings.TrimSpace(row.Asset))
		bithumbMarkets := bithumbByAsset[asset]
		row.PremiumIdentityVerified = false
		row.PremiumBithumbMarket = ""
		row.PremiumBithumbMidKRW = nil
		row.PremiumStatus = premiumReferencePriceUnavailable
		switch len(bithumbMarkets) {
		case 0:
			row.PremiumIdentityStatus = premiumIdentityBithumbMissing
			continue
		case 1:
			row.PremiumBithumbMarket = bithumbMarkets[0].Symbol
		default:
			row.PremiumIdentityStatus = premiumIdentityBithumbAmbiguous
			continue
		}

		proof, found := proofs[asset]
		if !found {
			row.PremiumIdentityStatus = premiumIdentitySymbolOnly
			continue
		}
		if err := proof.Validate(asset); err != nil {
			row.PremiumIdentityStatus = premiumIdentityConflict
			continue
		}
		canonical := strings.TrimSpace(proof.CanonicalAssetID)
		binanceIdentity, binanceFound := proof.Markets[marketanalysis.ContractMarketKey(domain.ExchangeBinance, row.Market)]
		bithumbIdentity, bithumbFound := proof.Markets[marketanalysis.ContractMarketKey(domain.ExchangeBithumb, bithumbMarkets[0].Symbol)]
		switch {
		case binanceFound && bithumbFound && strings.TrimSpace(binanceIdentity) == canonical && strings.TrimSpace(bithumbIdentity) == canonical:
			row.PremiumIdentityStatus = premiumIdentityVerified
			row.PremiumIdentityVerified = true
		case (binanceFound && strings.TrimSpace(binanceIdentity) != canonical) || (bithumbFound && strings.TrimSpace(bithumbIdentity) != canonical):
			row.PremiumIdentityStatus = premiumIdentityConflict
		default:
			row.PremiumIdentityStatus = premiumIdentityProofMissing
		}
	}
	return summary
}

// attachBinancePremiumBithumbMidpoints joins the Binance reference to the
// exact Bithumb KRW market selected by identity enrichment. Asset-level
// liquidity medians are deliberately not accepted: ALL_DOMESTIC_CONVERTED may
// contain multiple Bithumb quote markets for the same base asset.
func attachBinancePremiumBithumbMidpoints(
	summary official.BinanceTicker24hSummary,
	books []orderbook.Row,
	liquidity []marketanalysis.AssetMicrostructure,
	now time.Time,
) official.BinanceTicker24hSummary {
	summary.Assets = append([]official.BinanceTicker24hAsset(nil), summary.Assets...)
	quarantined := make(map[string]bool, len(liquidity))
	for _, row := range liquidity {
		if row.PriceValidation.Status == "QUARANTINED" {
			quarantined[strings.ToUpper(strings.TrimSpace(row.Asset))] = true
		}
	}
	exactMids := make(map[string]*float64)
	for _, row := range books {
		if row.Exchange != domain.ExchangeBithumb || row.Scope != domain.ScopeDomestic ||
			!strings.EqualFold(strings.TrimSpace(row.Quote), "KRW") || row.Status != "LIVE" ||
			row.MidPriceKRW == nil || math.IsNaN(*row.MidPriceKRW) || math.IsInf(*row.MidPriceKRW, 0) || *row.MidPriceKRW <= 0 {
			continue
		}
		value := *row.MidPriceKRW
		exactMids[strings.ToUpper(strings.TrimSpace(row.Market))] = &value
	}
	for index := range summary.Assets {
		row := &summary.Assets[index]
		row.PremiumBithumbMidKRW = nil
		row.PremiumBPS = nil
		switch {
		case summary.Status == "STALE" || summary.Status == "UNAVAILABLE":
			row.PremiumStatus = premiumReferenceStaleSummary
		case !official.BinanceTickerAssetFreshAt(row.CloseTimeMS, now):
			row.PremiumStatus = premiumReferenceStaleClose
		case row.PremiumIdentityStatus != premiumIdentityVerified &&
			row.PremiumIdentityStatus != premiumIdentitySymbolOnly &&
			row.PremiumIdentityStatus != premiumIdentityProofMissing:
			row.PremiumStatus = premiumReferenceIdentityRejected
		case quarantined[strings.ToUpper(strings.TrimSpace(row.Asset))]:
			row.PremiumStatus = premiumReferencePriceQuarantined
		default:
			mid := exactMids[strings.ToUpper(strings.TrimSpace(row.PremiumBithumbMarket))]
			if mid == nil {
				row.PremiumStatus = premiumReferencePriceUnavailable
				continue
			}
			value := *mid
			row.PremiumBithumbMidKRW = &value
			row.PremiumStatus = premiumReferenceLive
			row.PremiumBPS = binanceReferencePremiumBPS(row.EstimatedLastPriceKRW, row.PremiumBithumbMidKRW)
		}
	}
	return summary
}

func binanceReferencePremiumBPS(referenceKRW, bithumbMidKRW *float64) *float64 {
	if referenceKRW == nil || bithumbMidKRW == nil ||
		math.IsNaN(*referenceKRW) || math.IsInf(*referenceKRW, 0) || *referenceKRW <= 0 ||
		math.IsNaN(*bithumbMidKRW) || math.IsInf(*bithumbMidKRW, 0) || *bithumbMidKRW <= 0 {
		return nil
	}
	value := (*bithumbMidKRW / *referenceKRW - 1) * 10_000
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}
