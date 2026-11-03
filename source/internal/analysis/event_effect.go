package analysis

import (
	"math"
	"sort"
	"strings"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

type EventEffectInput struct {
	Asset                 string
	Exchange              string
	MarketScope           string
	AnchorTSMS            int64
	PreWindowSec          int64
	PostWindowSec         int64
	Now                   int64
	RetentionSec          int64
	Buckets               []aggregate.Bucket
	Coverage              coverage.Result
	GlobalCoverage        coverage.Result
	ContractIdentityProof *ContractIdentityProof
	// ListedExchanges is a request-generation snapshot of exchanges that list
	// this asset. It prevents complete transport coverage from turning a market
	// that does not exist on a venue into a fabricated observed zero.
	ListedExchanges map[domain.Exchange]bool
}

type eventSecondKey struct {
	second   int64
	scope    string
	exchange string
}

type eventValue struct {
	volume       float64
	trades       int64
	unresolvedFX int64
}

// EventEffect compares one notice-aligned post interval with its immediately
// preceding baseline. It intentionally returns the Python wire vocabulary so
// reaction-summary projection can preserve null, status, and provenance rules.
func EventEffect(input EventEffectInput) map[string]any {
	asset := strings.ToUpper(strings.TrimSpace(input.Asset))
	venue := strings.ToLower(strings.TrimSpace(input.Exchange))
	marketScope := strings.ToUpper(strings.TrimSpace(input.MarketScope))
	if marketScope != "ALL_DOMESTIC_CONVERTED" {
		marketScope = "KRW"
	}
	preDuration := max(int64(1), input.PreWindowSec)
	postDuration := max(int64(1), input.PostWindowSec)
	common := map[string]any{
		"asset": asset, "exchange": venue, "anchor_ts_ms": input.AnchorTSMS,
		"market_scope":   marketScope,
		"pre_window_sec": preDuration, "post_window_sec": postDuration,
		"causal_claim": false,
	}
	anchor := input.AnchorTSMS / 1000
	if asset == "" || venue == "" || anchor <= 0 {
		return reactionMerge(common, map[string]any{"status": "INVALID_ANCHOR"})
	}
	if anchor > input.Now {
		return reactionMerge(common, map[string]any{"status": "SCHEDULED", "seconds_until_anchor": anchor - input.Now})
	}
	if input.RetentionSec > 0 && anchor-preDuration <= input.Now-input.RetentionSec {
		return reactionMerge(common, map[string]any{"status": "OUTSIDE_MARKET_RETENTION", "retention_sec": input.RetentionSec})
	}
	preStart := anchor - preDuration
	postEnd := min(input.Now, anchor+postDuration)
	observedPost := max(int64(0), postEnd-anchor)
	if observedPost <= 0 {
		return reactionMerge(common, map[string]any{"status": "WAITING_FOR_POST_WINDOW"})
	}

	values := make(map[eventSecondKey]eventValue)
	identityParticipants := make(map[string]struct{})
	hasDomesticParticipant, hasGlobalParticipant := false, false
	for _, bucket := range input.Buckets {
		second := int64(bucket.Second)
		if !strings.EqualFold(bucket.Key.Base, asset) || second < preStart || second >= postEnd {
			continue
		}
		scope := domain.Scope(strings.ToLower(strings.TrimSpace(bucket.Key.Scope)))
		exchange := domain.Exchange(strings.ToLower(strings.TrimSpace(bucket.Key.Exchange)))
		if scope == domain.ScopeDomestic && marketScope == "KRW" && !strings.EqualFold(bucket.Key.Quote, "KRW") {
			continue
		}
		switch scope {
		case domain.ScopeDomestic:
			if !isDomesticEventExchange(exchange) {
				continue
			}
			hasDomesticParticipant = true
			identityParticipants[ContractMarketKey(exchange, bucket.Key.Market)] = struct{}{}
		case domain.ScopeGlobal:
			if !domain.IsGlobalReferenceExchange(exchange) {
				continue
			}
			hasGlobalParticipant = true
			identityParticipants[ContractMarketKey(exchange, bucket.Key.Market)] = struct{}{}
		default:
			continue
		}
		key := eventSecondKey{second: second, scope: string(scope), exchange: string(exchange)}
		value := values[key]
		value.volume += bucket.Totals.KRWVolume
		value.trades += int64(bucket.Totals.TradeCount)
		value.unresolvedFX = saturatingAddInt64(value.unresolvedFX, int64(min(bucket.Totals.UnresolvedCount, uint64(math.MaxInt64))))
		values[key] = value
	}
	totals := func(start, end int64, scope, target string) eventValue {
		result := eventValue{}
		for key, value := range values {
			if key.second < start || key.second >= end || (scope != "" && key.scope != scope) || (target != "" && key.exchange != target) {
				continue
			}
			result.volume += value.volume
			result.trades += value.trades
			result.unresolvedFX = saturatingAddInt64(result.unresolvedFX, value.unresolvedFX)
		}
		return result
	}
	observedSeconds := func(start, end int64, scope, target string) int64 {
		seen := make(map[int64]struct{})
		for key := range values {
			if key.second < start || key.second >= end || (scope != "" && key.scope != scope) || (target != "" && key.exchange != target) {
				continue
			}
			seen[key.second] = struct{}{}
		}
		return int64(len(seen))
	}
	rate := func(value float64, seconds int64) float64 { return value / float64(max(int64(1), seconds)) }
	multiplier := func(before, after float64) *float64 {
		if before <= 0 {
			return nil
		}
		return reactionFloat(after / before)
	}

	exchangePre, exchangePost := totals(preStart, anchor, "", venue), totals(anchor, postEnd, "", venue)
	domesticPre, domesticPost := totals(preStart, anchor, string(domain.ScopeDomestic), ""), totals(anchor, postEnd, string(domain.ScopeDomestic), "")
	globalPre, globalPost := totals(preStart, anchor, string(domain.ScopeGlobal), ""), totals(anchor, postEnd, string(domain.ScopeGlobal), "")
	bithumbPre, bithumbPost := totals(preStart, anchor, "", string(domain.ExchangeBithumb)), totals(anchor, postEnd, "", string(domain.ExchangeBithumb))
	exchangeFXComplete := exchangePre.unresolvedFX == 0 && exchangePost.unresolvedFX == 0
	domesticFXComplete := domesticPre.unresolvedFX == 0 && domesticPost.unresolvedFX == 0
	globalFXComplete := globalPre.unresolvedFX == 0 && globalPost.unresolvedFX == 0
	exchangePreRate, exchangePostRate := rate(exchangePre.volume, preDuration), rate(exchangePost.volume, observedPost)
	domesticPreRate, domesticPostRate := rate(domesticPre.volume, preDuration), rate(domesticPost.volume, observedPost)
	globalPreRate, globalPostRate := rate(globalPre.volume, preDuration), rate(globalPost.volume, observedPost)

	venueSeconds := make(map[int64]float64)
	for key, value := range values {
		if key.exchange == venue {
			venueSeconds[key.second] += value.volume
		}
	}
	preLogs, postLogs := make([]float64, 0, preDuration), make([]float64, 0, observedPost)
	for second := preStart; second < anchor; second++ {
		preLogs = append(preLogs, math.Log1p(venueSeconds[second]))
	}
	for second := anchor; second < postEnd; second++ {
		postLogs = append(postLogs, math.Log1p(venueSeconds[second]))
	}
	deviation := eventStdDev(preLogs)
	var standardized *float64
	if len(preLogs) > 0 && len(postLogs) > 0 && deviation > 1e-12 {
		standardized = reactionFloat((eventMean(postLogs) - eventMean(preLogs)) / deviation)
	}
	exchangeMultiplier := multiplier(exchangePreRate, exchangePostRate)
	domesticMultiplier := multiplier(domesticPreRate, domesticPostRate)
	globalMultiplier := multiplier(globalPreRate, globalPostRate)
	historicalIdentityVerified := verifiedEventContractIdentity(
		asset,
		input.ContractIdentityProof,
		identityParticipants,
		hasDomesticParticipant,
		hasGlobalParticipant,
	)
	globalSignalVerified := input.GlobalCoverage.WindowComplete && historicalIdentityVerified && globalFXComplete
	if !globalSignalVerified {
		globalMultiplier = nil
	}
	preShare, postShare := reactionPercentOf(bithumbPre.volume, domesticPre.volume), reactionPercentOf(bithumbPost.volume, domesticPost.volume)

	exchanges := append(append([]domain.Exchange{}, domain.DomesticExchanges[:]...), domain.GlobalReferenceExchanges[:]...)
	exchangeRows := make(map[string]any, len(exchanges))
	for _, exchange := range exchanges {
		name := string(exchange)
		before, after := totals(preStart, anchor, "", name), totals(anchor, postEnd, "", name)
		preObserved, postObserved := observedSeconds(preStart, anchor, "", name), observedSeconds(anchor, postEnd, "", name)
		status := "UNAVAILABLE"
		if isDomesticEventExchange(exchange) {
			switch {
			case input.Coverage.WindowComplete && (input.ListedExchanges[exchange] || preObserved > 0 || postObserved > 0):
				// A complete transport window proves that an absent bucket is a
				// genuine zero-trade observation, not missing market data.
				preObserved, postObserved = preDuration, observedPost
				status = "OBSERVED"
				if before.trades == 0 || after.trades == 0 {
					status = "OBSERVED_ZERO"
				}
			case preObserved > 0 || postObserved > 0:
				status = "PARTIAL_OBSERVATION"
			}
		} else if isGlobalEventExchange(exchange) {
			switch {
			case globalSignalVerified && (preObserved > 0 || postObserved > 0):
				// Exact historical identity plus complete global transport coverage
				// makes an absent side a genuine zero-trade observation.
				preObserved, postObserved = preDuration, observedPost
				status = "OBSERVED"
				if before.trades == 0 || after.trades == 0 {
					status = "OBSERVED_ZERO"
				}
			case preObserved > 0 || postObserved > 0:
				status = "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY"
			}
		}
		rowMultiplier := multiplier(rate(before.volume, preDuration), rate(after.volume, observedPost))
		if isDomesticEventExchange(exchange) && (!input.Coverage.WindowComplete || status == "UNAVAILABLE") {
			rowMultiplier = nil
		}
		if isGlobalEventExchange(exchange) && !globalSignalVerified {
			rowMultiplier = nil
			if status == "OBSERVED" || status == "OBSERVED_ZERO" || status == "PARTIAL_OBSERVATION" {
				status = "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY"
			}
		}
		var preRate, postRate any = rate(before.volume, preDuration), rate(after.volume, observedPost)
		if before.unresolvedFX > 0 {
			preRate = nil
		}
		if after.unresolvedFX > 0 {
			postRate = nil
		}
		if before.unresolvedFX > 0 || after.unresolvedFX > 0 {
			// KRWVolume remains the known subtotal; transport coverage cannot
			// establish the missing conversion or make that subtotal a full rate.
			status, rowMultiplier = "UNRESOLVED_FX", nil
		}
		exchangeRows[name] = map[string]any{
			"pre_volume_krw": before.volume, "post_volume_krw": after.volume,
			"pre_observed_seconds": preObserved, "post_observed_seconds": postObserved,
			"data_status": status, "pre_volume_per_sec_krw": preRate,
			"post_volume_per_sec_krw": postRate,
			"volume_multiplier":       rowMultiplier,
			"pre_trade_count":         before.trades, "post_trade_count": after.trades,
			"pre_unresolved_fx_count": before.unresolvedFX, "post_unresolved_fx_count": after.unresolvedFX,
		}
	}

	continuous := input.Coverage.WindowComplete
	signalStrength := math.Min(1, math.Abs(reactionValue(standardized))/3)
	if standardized == nil && exchangeMultiplier != nil && *exchangeMultiplier > 0 {
		signalStrength = math.Min(1, math.Abs(math.Log(*exchangeMultiplier))/math.Log(4))
	}
	sampleStrength := math.Min(1, float64(observedPost)/300)
	tradeStrength := math.Min(1, math.Min(float64(exchangePre.trades)/100, float64(exchangePost.trades)/50))
	confidence := (0.5*signalStrength + 0.3*sampleStrength + 0.2*tradeStrength) * 100
	origin := "NO_CLEAR_ORIGIN"
	if globalSignalVerified && globalMultiplier != nil && domesticMultiplier != nil && *globalMultiplier > math.Max(1.25, *domesticMultiplier*1.2) {
		origin = "GLOBAL_MARKET_LED_CANDIDATE"
	} else if exchangeMultiplier != nil && *exchangeMultiplier >= 1.5 {
		origin = "NOTICE_ALIGNED_EXCHANGE_ACTIVITY"
	}
	status := "LIVE_ESTIMATE"
	if !continuous {
		status = partialReactionStudy
	} else if !domesticFXComplete {
		status = incompleteFXReactionStudy
	} else if observedPost >= postDuration {
		status = "MATURE"
	}
	statisticalStatus := "NO_ROBUST_SHIFT"
	if observedPost < 300 {
		statisticalStatus = "EARLY_SIGNAL"
	} else if standardized != nil && math.Abs(*standardized) >= 3 {
		statisticalStatus = "ROBUST_SHIFT"
	}

	venueReactionStart := eventReactionStart(values, preStart, anchor, postEnd, "", venue, continuous)
	domesticReactionStart := eventReactionStart(values, preStart, anchor, postEnd, string(domain.ScopeDomestic), "", continuous)
	var globalReactionStart, binanceReactionStart *int64
	if globalSignalVerified {
		// The public global reference is Binance Spot only. Keep the legacy
		// global field and the explicit Binance field numerically identical.
		globalReactionStart = eventReactionStart(values, preStart, anchor, postEnd, string(domain.ScopeGlobal), string(domain.ExchangeBinance), true)
		binanceReactionStart = eventReactionStart(values, preStart, anchor, postEnd, string(domain.ScopeGlobal), string(domain.ExchangeBinance), true)
	}
	globalSignalStatus := "VERIFIED"
	if !globalSignalVerified {
		globalSignalStatus = "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY"
		if input.GlobalCoverage.WindowComplete && historicalIdentityVerified && !globalFXComplete {
			globalSignalStatus = "QUARANTINED_UNRESOLVED_FX"
		}
	}
	globalIdentityStatus := "VERIFIED_EXACT_HISTORICAL_MARKETS"
	if !historicalIdentityVerified {
		globalIdentityStatus = "QUARANTINED_EXACT_HISTORICAL_MARKETS_UNPROVEN"
	}
	domesticSignalStatus := "VERIFIED"
	var confidenceValue any = confidence
	var exchangePreRateValue any = exchangePreRate
	var exchangePostRateValue any = exchangePostRate
	var domesticPreRateValue any = domesticPreRate
	var domesticPostRateValue any = domesticPostRate
	var globalPreRateValue any = globalPreRate
	var globalPostRateValue any = globalPostRate
	if exchangePre.unresolvedFX > 0 {
		exchangePreRateValue = nil
	}
	if exchangePost.unresolvedFX > 0 {
		exchangePostRateValue = nil
	}
	if !exchangeFXComplete {
		exchangeMultiplier, standardized, venueReactionStart = nil, nil, nil
	}
	if domesticPre.unresolvedFX > 0 {
		domesticPreRateValue, preShare = nil, nil
	}
	if domesticPost.unresolvedFX > 0 {
		domesticPostRateValue, postShare = nil, nil
	}
	if !domesticFXComplete {
		domesticSignalStatus = "QUARANTINED_UNRESOLVED_FX"
		domesticMultiplier, domesticReactionStart = nil, nil
		confidenceValue, origin, statisticalStatus = nil, "UNRESOLVED", "QUARANTINED_UNRESOLVED_FX"
	}
	if globalPre.unresolvedFX > 0 {
		globalPreRateValue = nil
	}
	if globalPost.unresolvedFX > 0 {
		globalPostRateValue = nil
	}
	if !continuous {
		// Raw volumes and counts remain available in exchange_reactions, but
		// ratios, normalized rates, shares, dispersion, timing, and origin are
		// not identifiable across a transport gap. Nulling them prevents a
		// missing second from being silently interpreted as zero activity.
		domesticSignalStatus = "QUARANTINED_PARTIAL_COVERAGE"
		confidenceValue = nil
		exchangePreRateValue, exchangePostRateValue = nil, nil
		domesticPreRateValue, domesticPostRateValue = nil, nil
		exchangeMultiplier, domesticMultiplier = nil, nil
		preShare, postShare = nil, nil
		standardized = nil
		statisticalStatus = "QUARANTINED_PARTIAL_COVERAGE"
		origin = "UNRESOLVED"
		venueReactionStart, domesticReactionStart = nil, nil
	}
	return reactionMerge(common, map[string]any{
		"status": status, "observed_post_sec": observedPost,
		"exchange_pre_volume_per_sec_krw": exchangePreRateValue, "exchange_post_volume_per_sec_krw": exchangePostRateValue,
		"exchange_volume_multiplier": exchangeMultiplier, "domestic_pre_volume_per_sec_krw": domesticPreRateValue,
		"domestic_post_volume_per_sec_krw": domesticPostRateValue, "domestic_volume_multiplier": domesticMultiplier,
		"global_pre_volume_per_sec_krw": globalPreRateValue, "global_post_volume_per_sec_krw": globalPostRateValue,
		"global_pre_observed_seconds":  observedSeconds(preStart, anchor, string(domain.ScopeGlobal), ""),
		"global_post_observed_seconds": observedSeconds(anchor, postEnd, string(domain.ScopeGlobal), ""),
		"global_volume_multiplier":     globalMultiplier, "exchange_reactions": exchangeRows,
		"bithumb_pre_share_pct": preShare, "bithumb_post_share_pct": postShare,
		"bithumb_share_change_pp":       reactionDifference(postShare, preShare),
		"standardized_log_volume_shift": standardized, "statistical_status": statisticalStatus,
		"origin_candidate": origin, "evidence_confidence_pct": confidenceValue,
		"venue_reaction_start_ts_ms":    venueReactionStart,
		"domestic_reaction_start_ts_ms": domesticReactionStart,
		"global_reaction_start_ts_ms":   globalReactionStart,
		"binance_reaction_start_ts_ms":  binanceReactionStart,
		"exchange_pre_trade_count":      exchangePre.trades, "exchange_post_trade_count": exchangePost.trades,
		"exchange_pre_unresolved_fx_count": exchangePre.unresolvedFX, "exchange_post_unresolved_fx_count": exchangePost.unresolvedFX,
		"domestic_pre_unresolved_fx_count": domesticPre.unresolvedFX, "domestic_post_unresolved_fx_count": domesticPost.unresolvedFX,
		"global_pre_unresolved_fx_count": globalPre.unresolvedFX, "global_post_unresolved_fx_count": globalPost.unresolvedFX,
		"contiguous_market_coverage":        continuous,
		"contiguous_global_coverage":        input.GlobalCoverage.WindowComplete,
		"global_contract_identity_verified": historicalIdentityVerified,
		"global_contract_identity_status":   globalIdentityStatus,
		"global_signal_status":              globalSignalStatus,
		"domestic_signal_status":            domesticSignalStatus,
	})
}

// verifiedEventContractIdentity deliberately validates the participants in
// the historical event buckets, not a boolean produced from the current
// market window. This prevents a current symbol/contract mapping from
// releasing an older bucket that used a different exact exchange market.
func verifiedEventContractIdentity(
	asset string,
	proof *ContractIdentityProof,
	participants map[string]struct{},
	hasDomesticParticipant, hasGlobalParticipant bool,
) bool {
	if proof == nil || !hasDomesticParticipant || !hasGlobalParticipant || proof.Validate(asset) != nil {
		return false
	}
	canonical := strings.TrimSpace(proof.CanonicalAssetID)
	for participant := range participants {
		if strings.TrimSpace(proof.Markets[participant]) != canonical {
			return false
		}
	}
	return len(participants) >= 2
}

func isDomesticEventExchange(exchange domain.Exchange) bool {
	for _, candidate := range domain.DomesticExchanges {
		if exchange == candidate {
			return true
		}
	}
	return false
}

func isGlobalEventExchange(exchange domain.Exchange) bool {
	return domain.IsGlobalReferenceExchange(exchange)
}

func eventReactionStart(values map[eventSecondKey]eventValue, preStart, anchor, postEnd int64, scope, target string, missingIsObservedZero bool) *int64 {
	observed := make(map[int64]float64)
	for key, value := range values {
		if (scope == "" || key.scope == scope) && (target == "" || key.exchange == target) {
			observed[key.second] += value.volume
		}
	}
	baseline := make([]float64, 0)
	for second := preStart; second < anchor; second++ {
		if value, found := observed[second]; found || missingIsObservedZero {
			baseline = append(baseline, math.Log1p(value))
		}
	}
	if len(baseline) < 3 {
		return nil
	}
	level, deviation := eventMean(baseline), math.Max(.35, 2*eventStdDev(baseline))
	for second := anchor; second < postEnd-2; second++ {
		shifted, complete := 0, true
		for offset := int64(0); offset < 3; offset++ {
			value, found := observed[second+offset]
			if !found && !missingIsObservedZero {
				complete = false
				break
			}
			if math.Abs(math.Log1p(value)-level) >= deviation {
				shifted++
			}
		}
		if complete && shifted >= 2 {
			return reactionInt64(second * 1000)
		}
	}
	return nil
}

func eventMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
func eventStdDev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := eventMean(values)
	total := 0.0
	for _, value := range values {
		difference := value - mean
		total += difference * difference
	}
	return math.Sqrt(total / float64(len(values)))
}
func reactionPercentOf(part, whole float64) *float64 {
	if whole <= 0 {
		return nil
	}
	return reactionFloat(part / whole * 100)
}
func reactionDifference(left, right *float64) *float64 {
	if left == nil || right == nil {
		return nil
	}
	return reactionFloat(*left - *right)
}
func reactionMerge(left, right map[string]any) map[string]any {
	result := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

// Keep deterministic ordering available to callers that persist study rows.
func SortEventBuckets(rows []aggregate.Bucket) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Second < rows[j].Second })
}
