package analysis

import (
	"sort"
	"strings"

	"market-share-go/internal/domain"
)

func (s *buildState) buildAssets() []AssetAnalysis {
	assetNames := make(map[string]struct{})
	for _, market := range s.input.Markets {
		assetNames[market.Base] = struct{}{}
	}
	for asset := range s.domesticByAsset {
		assetNames[asset] = struct{}{}
	}
	for asset := range s.globalByAsset {
		assetNames[asset] = struct{}{}
	}
	bithumbMarketCounts := make(map[string]int)
	for _, market := range s.marketsByExchange[domain.ExchangeBithumb] {
		bithumbMarketCounts[market.Base]++
	}

	result := make([]AssetAnalysis, 0, len(assetNames))
	for asset := range assetNames {
		if analysisCanceled(s.input.ctx) {
			return nil
		}
		result = append(result, s.buildAsset(asset, bithumbMarketCounts[asset]))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CompetitorVolumeKRW != result[j].CompetitorVolumeKRW {
			return result[i].CompetitorVolumeKRW > result[j].CompetitorVolumeKRW
		}
		if pointerValue(result[i].GlobalVolumeKRW) != pointerValue(result[j].GlobalVolumeKRW) {
			return pointerValue(result[i].GlobalVolumeKRW) > pointerValue(result[j].GlobalVolumeKRW)
		}
		return result[i].Asset < result[j].Asset
	})
	return result
}

func (s *buildState) buildAsset(asset string, bithumbMarketCount int) AssetAnalysis {
	localRows, worldRows := s.domesticByAsset[asset], s.globalByAsset[asset]
	domesticRegistered := false
	for _, exchange := range domain.DomesticExchanges {
		if _, listed := s.listedAssets[exchange][asset]; listed {
			domesticRegistered = true
			break
		}
	}
	selected := s.input.DomesticWindows[s.input.Window]
	localObserved := hasObservedRows(localRows) || (selected.WindowComplete && domesticRegistered)
	worldObserved := hasObservedRows(worldRows)
	localFXComplete := rowsFXComplete(localRows)
	worldFXComplete := rowsFXComplete(worldRows)
	localVolume := sumRows(localRows, func(row Row) float64 { return row.VolumeKRW })
	worldVolume := sumRows(worldRows, func(row Row) float64 { return row.VolumeKRW })
	bithumbRows := filterRows(localRows, func(row Row) bool { return row.Exchange == domain.ExchangeBithumb })
	bithumbVolume := sumRows(bithumbRows, func(row Row) float64 { return row.VolumeKRW })
	upbitVolume := sumRows(filterRows(localRows, func(row Row) bool { return row.Exchange == domain.ExchangeUpbit }), func(row Row) float64 { return row.VolumeKRW })
	localTrades, bithumbTrades := sumTrades(localRows), sumTrades(bithumbRows)
	localBase := sumRows(localRows, func(row Row) float64 { return row.BaseQuantity })
	bithumbBase := sumRows(bithumbRows, func(row Row) float64 { return row.BaseQuantity })
	competitors := make(map[domain.Exchange]float64)
	for _, row := range localRows {
		if row.Exchange != domain.ExchangeBithumb {
			competitors[row.Exchange] += row.VolumeKRW
		}
	}
	leader, leaderVolume := competitorLeader(competitors)
	_, listed := s.listedAssets[domain.ExchangeBithumb][asset]
	listingExchanges := s.assetExchanges(asset)
	capture := optionalPercent(bithumbVolume, localVolume, localFXComplete)
	upbitCapture := optionalPercent(upbitVolume, localVolume, localFXComplete)
	competitorVolume := max(0.0, localVolume-bithumbVolume)
	localValue := optionalObserved(localVolume, localObserved)
	worldValue := optionalObserved(worldVolume, worldObserved && worldFXComplete)
	domesticShare := optionalPercent(localVolume, s.domesticTotal, localObserved && s.domesticInputValid && s.domesticFXComplete)
	globalShare := optionalPercent(worldVolume, s.globalTotal, worldObserved && s.globalFXComplete)
	identity := verifiedVolumeIdentity(
		asset, localRows, worldRows, s.input.Markets,
		s.input.ContractIdentities, s.input.CrossScopeComplete && s.globalFXComplete,
	)
	var globalMinusDomesticInterest *float64
	if identity.ContractIdentityVerified {
		globalMinusDomesticInterest = difference(globalShare, domesticShare)
	}
	row := AssetAnalysis{
		Asset: asset, AssetIdentity: identity, ListedOnBithumb: listed,
		GapType:           gapType(listed, bithumbVolume, competitorVolume, localVolume, capture, s.bithumbShare, localFXComplete),
		DomesticVolumeKRW: localValue, GlobalVolumeKRW: worldValue,
		BithumbVolumeKRW: bithumbVolume, UpbitVolumeKRW: upbitVolume,
		UpbitCapturePct: upbitCapture, BithumbMinusUpbitCapturePP: difference(capture, upbitCapture),
		CompetitorVolumeKRW: competitorVolume, BithumbCapturePct: capture,
		CaptureGapVsVenuePP:    difference(capture, s.bithumbShare),
		DomesticVolumeSharePct: domesticShare, GlobalVolumeSharePct: globalShare,
		GlobalMinusDomesticInterestPP: globalMinusDomesticInterest,
		CompetitorLeader:              string(leader), CompetitorLeaderVolumeKRW: leaderVolume,
		DomesticExchanges: listingExchanges, DomesticExchangeCount: len(listingExchanges),
		ExclusiveOnBithumb: len(listingExchanges) == 1 && listingExchanges[0] == string(domain.ExchangeBithumb),
		SharedBithumbUpbit: contains(listingExchanges, string(domain.ExchangeBithumb)) && contains(listingExchanges, string(domain.ExchangeUpbit)),
		BithumbMarketCount: bithumbMarketCount, BithumbTradeCount: bithumbTrades,
		DomesticTradeCount: localTrades, BithumbTradeCountCapturePct: percent(float64(bithumbTrades), float64(localTrades)),
		BithumbBaseQuantity: bithumbBase, DomesticBaseQuantity: localBase,
		BithumbBaseQuantityCapturePct: percent(bithumbBase, localBase), Quotes: uniqueQuotes(localRows),
	}
	replacementExchanges := make(map[string]bool)
	for _, source := range localRows {
		if source.TradeDetailIncomplete {
			row.OfficialMinuteReplacementUsed = true
			row.TradeDetailIncomplete = true
			row.OfficialMinuteReplacementVolumeKRW += source.OfficialReplacementVolumeKRW
			replacementExchanges[string(source.Exchange)] = true
		}
	}
	for exchange := range replacementExchanges {
		row.OfficialMinuteReplacementExchanges = append(row.OfficialMinuteReplacementExchanges, exchange)
	}
	sort.Strings(row.OfficialMinuteReplacementExchanges)
	if row.TradeDetailIncomplete {
		row.BithumbTradeCountCapturePct = nil
		row.BithumbBaseQuantityCapturePct = nil
	}
	if attribution, ok := s.attributionByAsset[asset]; ok {
		row.ShareChangeContributionPP = pointer(attribution.ContributionPP)
		row.DomesticVolumeChangeKRW = pointer(attribution.DomesticVolumeChangeKRW)
		row.BithumbVolumeChangeKRW = pointer(attribution.BithumbVolumeChangeKRW)
	}
	return row
}

func rowsFXComplete(rows []Row) bool {
	for _, row := range rows {
		if row.UnresolvedFXCount > 0 {
			return false
		}
	}
	return true
}

// verifiedVolumeIdentity releases a cross-scope comparison only when operator-
// reviewed evidence covers every exact market that contributed an observed
// domestic or global trade. A normalized base symbol is useful for displaying
// the raw per-scope totals, but it is not evidence that two venues trade the
// same contract or native network asset.
func verifiedVolumeIdentity(
	asset string,
	domesticRows, globalRows []Row,
	markets []domain.MarketPair,
	proofs map[string]ContractIdentityProof,
	crossScopeComplete bool,
) AssetIdentity {
	identity := Identity(asset)
	if !crossScopeComplete {
		return identity
	}
	proof, ok := proofs[strings.ToUpper(strings.TrimSpace(asset))]
	if !ok || proof.Validate(asset) != nil {
		return identity
	}

	observedKeys := make(map[string]struct{})
	domesticObserved, globalObserved := false, false
	collect := func(rows []Row, scope domain.Scope) {
		for _, row := range rows {
			if row.TradeCount <= 0 {
				continue
			}
			observedKeys[ContractMarketKey(row.Exchange, row.Market)] = struct{}{}
			if scope == domain.ScopeDomestic {
				domesticObserved = true
			} else {
				globalObserved = true
			}
		}
	}
	collect(domesticRows, domain.ScopeDomestic)
	collect(globalRows, domain.ScopeGlobal)
	if !domesticObserved || !globalObserved || len(observedKeys) < 2 {
		return identity
	}

	canonical := strings.TrimSpace(proof.CanonicalAssetID)
	expectedDomestic, expectedGlobal := false, false
	for _, market := range markets {
		if !strings.EqualFold(strings.TrimSpace(market.Base), asset) {
			continue
		}
		if strings.TrimSpace(proof.Markets[ContractMarketKey(market.Exchange, market.Symbol)]) != canonical {
			return identity
		}
		if market.Scope == domain.ScopeDomestic {
			expectedDomestic = true
		} else if market.Scope == domain.ScopeGlobal {
			expectedGlobal = true
		}
	}
	if !expectedDomestic || !expectedGlobal {
		return identity
	}
	for key := range observedKeys {
		if strings.TrimSpace(proof.Markets[key]) != canonical {
			return identity
		}
	}
	identity.CanonicalAssetID = canonical
	identity.ContractIdentityStatus = "VERIFIED"
	identity.ContractIdentityVerified = true
	identity.Source = strings.TrimSpace(proof.Source)
	return identity
}

func (s *buildState) assetExchanges(asset string) []string {
	result := make([]string, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		if _, ok := s.listedAssets[exchange][asset]; ok {
			result = append(result, string(exchange))
		}
	}
	sort.Strings(result)
	return result
}

func competitorLeader(values map[domain.Exchange]float64) (domain.Exchange, float64) {
	var leader domain.Exchange
	volume := 0.0
	for exchange, candidate := range values {
		if candidate > volume || (candidate == volume && candidate > 0 && (leader == "" || exchange < leader)) {
			leader, volume = exchange, candidate
		}
	}
	return leader, volume
}

func gapType(listed bool, bithumbVolume, competitorVolume, localVolume float64, capture, venueShare *float64, fxComplete bool) string {
	switch {
	case !fxComplete:
		return "UNAVAILABLE_DATA_QUALITY"
	case localVolume > 0 && (capture == nil || venueShare == nil):
		return "UNAVAILABLE_DATA_QUALITY"
	case !listed && competitorVolume > 0:
		return "UNLISTED"
	case listed && bithumbVolume == 0 && competitorVolume > 0:
		return "NO_RECENT_BITHUMB_VOLUME"
	case localVolume > 0 && capture != nil && venueShare != nil && *capture < *venueShare:
		return "BELOW_VENUE_BASELINE"
	case localVolume > 0:
		return "AT_OR_ABOVE_BASELINE"
	default:
		return "NO_RECENT_DOMESTIC_VOLUME"
	}
}

func filterRows(rows []Row, keep func(Row) bool) []Row {
	result := make([]Row, 0, len(rows))
	for _, row := range rows {
		if keep(row) {
			result = append(result, row)
		}
	}
	return result
}

func optionalObserved(value float64, observed bool) *float64 {
	if !observed {
		return nil
	}
	return pointer(value)
}

func optionalPercent(part, whole float64, observed bool) *float64 {
	if !observed {
		return nil
	}
	return percent(part, whole)
}

func uniqueQuotes(rows []Row) []string {
	seen := make(map[string]struct{})
	for _, row := range rows {
		seen[row.Quote] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for quote := range seen {
		result = append(result, quote)
	}
	sort.Strings(result)
	return result
}

func pointerValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
