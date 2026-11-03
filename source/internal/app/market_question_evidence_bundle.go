package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/marketquestion"
)

const marketQuestionEvidenceSourceID = "source_primary"
const marketQuestionNoticeSourceID = "source_notices"

var marketQuestionDomesticExchanges = []string{"upbit", "bithumb", "coinone", "korbit", "gopax"}

type marketQuestionEvidenceRuntimeIdentity struct {
	CreatedAtMS int64
	Registry    marketquestion.RegistryIdentity
	Revision    marketquestion.RevisionIdentity
	Replay      marketquestion.ReplayCapability
}

// buildMarketQuestionEvidenceBundle projects only existing typed calculation
// fields. It never parses ledger/display prose to recover a numeric value.
func buildMarketQuestionEvidenceBundle(
	response marketanalysis.GroundedMarketQuestionResponse,
	question string,
) (marketquestion.EvidenceBundle, error) {
	return buildMarketQuestionEvidenceBundleWithIdentity(response, question, marketQuestionEvidenceRuntimeIdentity{
		CreatedAtMS: time.Now().UnixMilli(),
		Registry: marketquestion.RegistryIdentity{
			Status: marketquestion.IdentityUnavailable,
			Reason: "CURRENT_RESPONSE_DOES_NOT_EXPOSE_HISTORICAL_REGISTRY_IDENTITY",
		},
		Revision: marketquestion.RevisionIdentity{
			Status:             marketquestion.IdentityUnavailable,
			AnalysisVersion:    response.Provenance.AnalysisVersion,
			AggregationVersion: response.Provenance.AggregationVersion,
			Reason:             "CURRENT_RESPONSE_DOES_NOT_EXPOSE_JOINT_DB_MEMORY_INPUT_REVISION",
		},
		Replay: marketquestion.ReplayCapability{
			RecalculationMode:     "CURRENT_REVISION_RECALCULATION",
			RetainedBundleStatus:  marketquestion.IdentityUnavailable,
			OriginalAsKnownStatus: marketquestion.IdentityUnavailable,
			Reason:                "IN_RESPONSE_BUNDLE_IS_NOT_PERSISTED_AND_UPSERTED_HISTORY_CANNOT_RECONSTRUCT_ORIGINAL_AS_KNOWN_STATE",
		},
	})
}

func buildMarketQuestionEvidenceBundleWithIdentity(
	response marketanalysis.GroundedMarketQuestionResponse,
	question string,
	identity marketQuestionEvidenceRuntimeIdentity,
) (marketquestion.EvidenceBundle, error) {
	if identity.CreatedAtMS <= 0 {
		identity.CreatedAtMS = time.Now().UnixMilli()
	}
	timeSemantics := marketQuestionEvidenceTime(response)
	scope := marketQuestionEvidenceScope(response)
	coverage := marketQuestionEvidenceCoverage(response)
	sources := []marketquestion.SourceIdentity{marketQuestionPrimaryEvidenceSource(response, timeSemantics)}
	builder := marketQuestionTypedFactBuilder{
		quality:  strings.TrimSpace(response.Quality.Status),
		sourceID: marketQuestionEvidenceSourceID,
		facts:    make([]marketquestion.EvidenceFact, 0, 64),
	}
	denominators := marketQuestionEvidenceDenominators(response, scope, identity.Registry, &builder)
	marketQuestionEvidenceCoreFacts(response, denominators, &builder)
	marketQuestionEvidencePathFacts(response, &builder)
	marketQuestionEvidenceAssetFacts(response, denominators, &builder)
	marketQuestionEvidenceCandleFacts(response, scope, &builder)
	marketQuestionEvidenceCoinFacts(response, &builder)
	marketQuestionEvidenceDailyFacts(response, &builder)
	notice := marketQuestionEvidenceNotice(response, &builder)
	if notice.Status != marketquestion.NoticeNotQueried {
		status := marketquestion.IdentityConfirmed
		reason := ""
		if notice.Status == marketquestion.NoticeFailed {
			status = marketquestion.IdentityUnavailable
			reason = notice.Reason
		}
		sources = append(sources, marketquestion.SourceIdentity{
			ID: marketQuestionNoticeSourceID, EvidenceLayer: "OFFICIAL_NOTICE_METADATA",
			Tool: marketquestion.ToolNoticeContext, Status: status, Reason: reason,
		})
	}
	if builder.err != nil {
		return marketquestion.EvidenceBundle{}, builder.err
	}
	contribution := marketQuestionEvidenceContribution(response, builder.facts)
	requirements := marketQuestionEvidenceRequirements(response, timeSemantics, scope, coverage, denominators, builder.facts, notice)
	input := marketquestion.EvidenceBundleInput{
		CreatedAtMS: identity.CreatedAtMS,
		QuestionContract: marketquestion.QuestionContract{
			QuestionDigest: marketQuestionEvidenceQuestionDigest(question),
			Metric:         response.Query.Metric, Comparison: response.Query.Comparison,
			TargetExchange: response.Query.Exchange, TargetAsset: response.Query.Asset,
			RequestedExplanation: append([]string(nil), response.Query.RequestedExplanation...),
			Requirements:         requirements,
		},
		Time: timeSemantics, Scope: scope, Coverage: coverage,
		Denominators: denominators, Sources: sources,
		Registry: identity.Registry, Revision: identity.Revision,
		Facts: builder.facts, Contribution: contribution, Notice: notice, Replay: identity.Replay,
		Warnings: append([]string(nil), response.Quality.Warnings...),
	}
	return marketquestion.NewEvidenceBundle(input)
}

func marketQuestionEvidencePathFacts(response marketanalysis.GroundedMarketQuestionResponse, builder *marketQuestionTypedFactBuilder) {
	path := response.Facts.PathSummary
	if path == nil {
		return
	}
	exchange := response.Query.Exchange
	if exchange == "" {
		exchange = "bithumb"
	}
	subject := marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: exchange}
	timeRef := marketQuestionBundleFactTime(response)
	text := "공식 시간별 관측점의 경로입니다. 시간봉 사이의 연속 움직임을 뜻하지 않습니다. 경로 상태: " + path.Status + ". " + path.Reason
	builder.context("fact_hourly_path_context", "HOURLY_PATH_CONTEXT", "CONTEXT", text, builder.sourceID, subject, timeRef)
	count := int64(path.PointCount)
	builder.integer("fact_hourly_path_observed_count", "PATH_OBSERVED_COUNT", "LEVEL", subject, &count, "COUNT", timeRef)
	if path.Status != "COMPLETE" || path.PointCount != path.ExpectedPointCount || path.ExpectedPointCount < 2 {
		return
	}
	for _, item := range []struct {
		id, metric, unit string
		value            *float64
	}{
		{"min_share", "PATH_MIN_SHARE_PCT", "PCT", path.MinSharePct},
		{"max_share", "PATH_MAX_SHARE_PCT", "PCT", path.MaxSharePct},
		{"largest_step", "PATH_LARGEST_STEP_PP", "PP", path.LargestStepDeltaPP},
		{"largest_step_abs_share", "PATH_LARGEST_STEP_ABS_SHARE_PCT", "PCT", path.LargestStepAbsSharePct},
	} {
		builder.float("fact_hourly_path_"+item.id, item.metric, "LEVEL", subject, item.value, item.unit, timeRef, nil, nil)
	}
	for _, item := range []struct {
		id, metric string
		value      int
	}{
		{"rising", "PATH_RISING_COUNT", path.RisingStepCount},
		{"falling", "PATH_FALLING_COUNT", path.FallingStepCount},
		{"flat", "PATH_FLAT_COUNT", path.FlatStepCount},
	} {
		count := int64(item.value)
		builder.integer("fact_hourly_path_"+item.id, item.metric, "LEVEL", subject, &count, "COUNT", timeRef)
	}
}

type marketQuestionTypedFactBuilder struct {
	quality  string
	sourceID string
	facts    []marketquestion.EvidenceFact
	err      error
}

func (builder *marketQuestionTypedFactBuilder) float(
	id, metric, role string,
	subject marketquestion.FactSubject,
	value *float64,
	unit string,
	timeRef marketquestion.FactTimeReference,
	denominatorIDs []string,
	relation *marketquestion.FactRelation,
) string {
	id = marketquestion.CanonicalFactID(id)
	if builder.err != nil {
		return id
	}
	quantity := marketquestion.UnknownQuantity(unit)
	if value != nil {
		quantity, builder.err = marketquestion.ConfirmedQuantity(strconv.FormatFloat(*value, 'f', -1, 64), unit)
		if builder.err != nil {
			return id
		}
	}
	builder.facts = append(builder.facts, marketquestion.EvidenceFact{
		ID: id, Metric: metric, Role: role, Subject: subject, Value: quantity, Time: timeRef,
		DenominatorIDs: append([]string(nil), denominatorIDs...), SourceID: builder.sourceID,
		AggregationPolicy: marketquestion.AggregationPolicyForFactMetric(metric),
		Quality:           builder.quality, Relation: relation,
	})
	return id
}

func (builder *marketQuestionTypedFactBuilder) relation(operation marketquestion.AggregationOperationID, ids ...string) *marketquestion.FactRelation {
	if len(ids) == 0 {
		return nil
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = marketquestion.CanonicalFactID(id)
		found := false
		for _, fact := range builder.facts {
			if fact.ID == id && (fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		result = append(result, id)
	}
	return &marketquestion.FactRelation{Operation: operation, OperandFactIDs: result}
}

func (builder *marketQuestionTypedFactBuilder) integer(
	id, metric, role string,
	subject marketquestion.FactSubject,
	value *int64,
	unit string,
	timeRef marketquestion.FactTimeReference,
) string {
	if value == nil {
		return builder.float(id, metric, role, subject, nil, unit, timeRef, nil, nil)
	}
	quantity, err := marketquestion.ConfirmedQuantity(strconv.FormatInt(*value, 10), unit)
	if err != nil {
		builder.err = err
		return marketquestion.CanonicalFactID(id)
	}
	id = marketquestion.CanonicalFactID(id)
	builder.facts = append(builder.facts, marketquestion.EvidenceFact{
		ID: id, Metric: metric, Role: role, Subject: subject, Value: quantity, Time: timeRef,
		SourceID: builder.sourceID, AggregationPolicy: marketquestion.AggregationPolicyForFactMetric(metric), Quality: builder.quality,
	})
	return id
}

func (builder *marketQuestionTypedFactBuilder) context(
	id, metric, role, text, sourceID string,
	subject marketquestion.FactSubject,
	timeRef marketquestion.FactTimeReference,
) string {
	id = marketquestion.CanonicalFactID(id)
	builder.facts = append(builder.facts, marketquestion.EvidenceFact{
		ID: id, Metric: metric, Role: role, Subject: subject,
		Value: marketquestion.NotApplicableQuantity(), Time: timeRef,
		SourceID: sourceID, AggregationPolicy: marketquestion.AggregationPolicyForFactMetric(metric),
		Quality: builder.quality, Text: strings.TrimSpace(text),
	})
	return id
}

func marketQuestionEvidenceCoreFacts(
	response marketanalysis.GroundedMarketQuestionResponse,
	denominators []marketquestion.DenominatorIdentity,
	builder *marketQuestionTypedFactBuilder,
) {
	query := response.Query
	subject := func(exchange string) marketquestion.FactSubject {
		return marketquestion.FactSubject{MarketScope: query.MarketScope, Exchange: exchange}
	}
	denominatorID := func(role string) []string {
		id := "denominator_" + role
		for _, denominator := range denominators {
			if denominator.ID == id {
				return []string{id}
			}
		}
		return nil
	}
	if response.Facts.Previous != nil {
		window := response.Facts.Previous
		timeRef := marketQuestionWindowFactTime("PREVIOUS", window)
		domesticID := builder.float("fact_previous_domestic_volume_krw", "DOMESTIC_VOLUME_KRW", "LEVEL", subject("domestic"), window.DomesticVolumeKRW, "KRW", timeRef, nil, nil)
		bithumbID := builder.float("fact_previous_bithumb_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", subject("bithumb"), window.BithumbVolumeKRW, "KRW", timeRef, nil, nil)
		builder.float("fact_previous_bithumb_share_pct", "EXCHANGE_SHARE_PCT", "LEVEL", subject("bithumb"), window.BithumbSharePct, "PCT", timeRef, denominatorID("previous"), builder.relation(marketquestion.AggregationRecomputeRatio, bithumbID, domesticID))
		builder.integer("fact_previous_domestic_trade_count", "TRADE_COUNT", "LEVEL", subject("domestic"), window.DomesticTradeCount, "COUNT", timeRef)
		builder.integer("fact_previous_bithumb_trade_count", "TRADE_COUNT", "LEVEL", subject("bithumb"), window.BithumbTradeCount, "COUNT", timeRef)
	}
	if response.Facts.Current != nil {
		window := response.Facts.Current
		timeRef := marketQuestionWindowFactTime("CURRENT", window)
		domesticID := builder.float("fact_current_domestic_volume_krw", "DOMESTIC_VOLUME_KRW", "LEVEL", subject("domestic"), window.DomesticVolumeKRW, "KRW", timeRef, nil, nil)
		bithumbID := builder.float("fact_current_bithumb_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", subject("bithumb"), window.BithumbVolumeKRW, "KRW", timeRef, nil, nil)
		builder.float("fact_current_bithumb_share_pct", "EXCHANGE_SHARE_PCT", "LEVEL", subject("bithumb"), window.BithumbSharePct, "PCT", timeRef, denominatorID("current"), builder.relation(marketquestion.AggregationRecomputeRatio, bithumbID, domesticID))
		builder.integer("fact_current_domestic_trade_count", "TRADE_COUNT", "LEVEL", subject("domestic"), window.DomesticTradeCount, "COUNT", timeRef)
		builder.integer("fact_current_bithumb_trade_count", "TRADE_COUNT", "LEVEL", subject("bithumb"), window.BithumbTradeCount, "COUNT", timeRef)
	}
	if marketQuestionRequiresChange(query) && (query.Exchange == "" || strings.EqualFold(query.Exchange, "bithumb")) {
		builder.float("fact_share_delta_pp", "SHARE_DELTA_PP", "CHANGE", subject(marketQuestionEvidenceTargetExchange(query)), response.Facts.ShareDeltaPP, "PP", marketQuestionBundleFactTime(response), existingDenominatorIDs([]string{"denominator_previous", "denominator_current"}, denominators), builder.relation(marketquestion.AggregationEndpointDifference, "fact_previous_bithumb_share_pct", "fact_current_bithumb_share_pct"))
	}
	if marketQuestionRequiresChange(query) {
		builder.float("fact_domestic_volume_delta_krw", "VOLUME_DELTA_KRW", "CHANGE", subject("domestic"), response.Facts.DomesticVolumeDeltaKRW, "KRW", marketQuestionBundleFactTime(response), nil, builder.relation(marketquestion.AggregationEndpointDifference, "fact_previous_domestic_volume_krw", "fact_current_domestic_volume_krw"))
		builder.float("fact_bithumb_volume_delta_krw", "VOLUME_DELTA_KRW", "CHANGE", subject("bithumb"), response.Facts.BithumbVolumeDeltaKRW, "KRW", marketQuestionBundleFactTime(response), nil, builder.relation(marketquestion.AggregationEndpointDifference, "fact_previous_bithumb_volume_krw", "fact_current_bithumb_volume_krw"))
	}
	if target := response.Facts.TargetExchange; target != nil {
		exchange := strings.ToLower(strings.TrimSpace(target.Exchange))
		prevSubject, currSubject := subject(exchange), subject(exchange)
		prevTime, currTime := marketQuestionBundleFactTimeRole(response, "PREVIOUS"), marketQuestionBundleFactTimeRole(response, "CURRENT")
		prevVol := builder.float("fact_previous_target_exchange_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", prevSubject, target.PreviousVolumeKRW, "KRW", prevTime, nil, nil)
		currentVolume := target.CurrentVolumeKRW
		if currentVolume == nil && response.Query.Comparison == marketquestion.ComparisonCurrentSnapshot {
			currentVolume = target.Official24hVolumeKRW
		}
		currentShare := target.CurrentSharePct
		if currentShare == nil && response.Query.Comparison == marketquestion.ComparisonCurrentSnapshot {
			currentShare = target.Official24hSharePct
		}
		currVol := builder.float("fact_current_target_exchange_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", currSubject, currentVolume, "KRW", currTime, nil, nil)
		builder.float("fact_previous_target_exchange_share_pct", "EXCHANGE_SHARE_PCT", "LEVEL", prevSubject, target.PreviousSharePct, "PCT", prevTime, denominatorID("previous"), builder.relation(marketquestion.AggregationRecomputeRatio, prevVol, "fact_previous_domestic_volume_krw"))
		builder.float("fact_current_target_exchange_share_pct", "EXCHANGE_SHARE_PCT", "LEVEL", currSubject, currentShare, "PCT", currTime, denominatorID("current"), builder.relation(marketquestion.AggregationRecomputeRatio, currVol, "fact_current_domestic_volume_krw"))
		if marketQuestionRequiresChange(query) {
			builder.float("fact_target_exchange_share_delta_pp", "SHARE_DELTA_PP", "CHANGE", subject(exchange), target.ShareDeltaPP, "PP", marketQuestionBundleFactTime(response), existingDenominatorIDs([]string{"denominator_previous", "denominator_current"}, denominators), builder.relation(marketquestion.AggregationEndpointDifference, "fact_previous_target_exchange_share_pct", "fact_current_target_exchange_share_pct"))
			builder.float("fact_target_exchange_volume_delta_krw", "VOLUME_DELTA_KRW", "CHANGE", subject(exchange), target.VolumeDeltaKRW, "KRW", marketQuestionBundleFactTime(response), nil, builder.relation(marketquestion.AggregationEndpointDifference, prevVol, currVol))
		}
		builder.integer("fact_previous_target_exchange_trade_count", "TRADE_COUNT", "LEVEL", prevSubject, target.PreviousTradeCount, "COUNT", prevTime)
		builder.integer("fact_current_target_exchange_trade_count", "TRADE_COUNT", "LEVEL", currSubject, target.CurrentTradeCount, "COUNT", currTime)
	}
	componentIDs := make([]string, 0, len(response.Facts.Drivers))
	visibleContributionSum := 0.0
	for index, driver := range response.Facts.Drivers {
		id := fmt.Sprintf("fact_asset_contribution_%s_%d", marketQuestionEvidenceIDPart(driver.Asset), index)
		componentIDs = append(componentIDs, builder.float(id, "ASSET_ATTRIBUTION_PP", "CONTRIBUTION", subjectWithAsset(subject("bithumb"), driver.Asset), &driver.ContributionPP, "PP", marketQuestionBundleFactTime(response), []string{"denominator_previous", "denominator_current"}, nil))
		visibleContributionSum += driver.ContributionPP
	}
	for index, driver := range response.Facts.ExchangeDrivers {
		builder.float(fmt.Sprintf("fact_exchange_contribution_%s_%d", marketQuestionEvidenceIDPart(driver.Exchange), index), "EXCHANGE_ATTRIBUTION_PP", "CONTRIBUTION", subject(driver.Exchange), &driver.ContributionPP, "PP", marketQuestionBundleFactTime(response), []string{"denominator_previous", "denominator_current"}, nil)
	}
	if response.Facts.DriverContributionSumPP != nil {
		// DriverContributionSumPP is calculated before the response's top-N
		// presentation limit. Preserve the omitted component total separately so
		// the visible components are never represented as the complete sum.
		other := *response.Facts.DriverContributionSumPP - visibleContributionSum
		otherID := builder.float("fact_driver_other_contribution_pp", "OTHER_ASSET_ATTRIBUTION_PP", "OTHER", subject("bithumb"), &other, "PP", marketQuestionBundleFactTime(response), existingDenominatorIDs([]string{"denominator_previous", "denominator_current"}, denominators), nil)
		reconciliationIDs := append(append([]string(nil), componentIDs...), otherID)
		builder.float("fact_driver_contribution_sum_pp", "DRIVER_CONTRIBUTION_SUM_PP", "RECONCILIATION", subject("bithumb"), response.Facts.DriverContributionSumPP, "PP", marketQuestionBundleFactTime(response), existingDenominatorIDs([]string{"denominator_previous", "denominator_current"}, denominators), builder.relation(marketquestion.AggregationReconcileContributions, reconciliationIDs...))
	}
	builder.float("fact_driver_residual_pp", "DRIVER_RESIDUAL_PP", "RESIDUAL", subject("bithumb"), response.Facts.DriverResidualPP, "PP", marketQuestionBundleFactTime(response), existingDenominatorIDs([]string{"denominator_previous", "denominator_current"}, denominators), nil)
}

func marketQuestionEvidenceAssetFacts(response marketanalysis.GroundedMarketQuestionResponse, denominators []marketquestion.DenominatorIdentity, builder *marketQuestionTypedFactBuilder) {
	asset := response.Facts.AssetMetrics
	if asset == nil {
		return
	}
	subject := marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "bithumb", Asset: asset.Asset}
	previous, current := marketQuestionBundleFactTimeRole(response, "PREVIOUS"), marketQuestionBundleFactTimeRole(response, "CURRENT")
	rows := []struct {
		id, metric, role, unit string
		value                  *float64
		time                   marketquestion.FactTimeReference
		denoms                 []string
	}{
		{"fact_asset_previous_domestic_volume_krw", "DOMESTIC_VOLUME_KRW", "LEVEL", "KRW", asset.PreviousDomesticVolumeKRW, previous, nil},
		{"fact_asset_current_domestic_volume_krw", "DOMESTIC_VOLUME_KRW", "LEVEL", "KRW", asset.CurrentDomesticVolumeKRW, current, nil},
		{"fact_asset_previous_bithumb_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", "KRW", asset.PreviousBithumbVolumeKRW, previous, nil},
		{"fact_asset_current_bithumb_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", "KRW", asset.CurrentBithumbVolumeKRW, current, nil},
		{"fact_asset_previous_capture_pct", "ASSET_CAPTURE_PCT", "LEVEL", "PCT", asset.PreviousBithumbCapturePct, previous, []string{"denominator_previous"}},
		{"fact_asset_current_capture_pct", "ASSET_CAPTURE_PCT", "LEVEL", "PCT", asset.CurrentBithumbCapturePct, current, []string{"denominator_current"}},
		{"fact_asset_capture_delta_pp", "ASSET_CAPTURE_DELTA_PP", "CHANGE", "PP", asset.BithumbCaptureChangePP, marketQuestionBundleFactTime(response), []string{"denominator_previous", "denominator_current"}},
		{"fact_asset_previous_internal_pct", "ASSET_INTERNAL_PCT", "LEVEL", "PCT", asset.PreviousShareWithinBithumbPct, previous, []string{"denominator_previous"}},
		{"fact_asset_current_internal_pct", "ASSET_INTERNAL_PCT", "LEVEL", "PCT", asset.CurrentShareWithinBithumbPct, current, []string{"denominator_current"}},
		{"fact_asset_internal_delta_pp", "ASSET_INTERNAL_DELTA_PP", "CHANGE", "PP", asset.ShareWithinBithumbChangePP, marketQuestionBundleFactTime(response), []string{"denominator_previous", "denominator_current"}},
		{"fact_asset_previous_total_contribution_pct", "ASSET_TOTAL_SHARE_PCT", "LEVEL", "PCT", asset.PreviousTotalShareContributionPct, previous, []string{"denominator_previous"}},
		{"fact_asset_current_total_contribution_pct", "ASSET_TOTAL_SHARE_PCT", "LEVEL", "PCT", asset.CurrentTotalShareContributionPct, current, []string{"denominator_current"}},
		{"fact_asset_total_contribution_delta_pp", "ASSET_TOTAL_SHARE_DELTA_PP", "CHANGE", "PP", asset.TotalShareContributionChangePP, marketQuestionBundleFactTime(response), []string{"denominator_previous", "denominator_current"}},
		{"fact_asset_change_attribution_pp", "ASSET_ATTRIBUTION_PP", "CONTRIBUTION", "PP", asset.ChangeAttributionPP, marketQuestionBundleFactTime(response), []string{"denominator_previous", "denominator_current"}},
	}
	for _, row := range rows {
		builder.float(row.id, row.metric, row.role, subject, row.value, row.unit, row.time, existingDenominatorIDs(row.denoms, denominators), nil)
	}
}

func marketQuestionEvidenceCandleFacts(response marketanalysis.GroundedMarketQuestionResponse, scope marketquestion.ScopeIdentity, builder *marketQuestionTypedFactBuilder) {
	candle := response.Facts.CandleComparison
	if candle == nil {
		return
	}
	previous := marketquestion.FactTimeReference{Role: "PREVIOUS", StartMS: int64Ref(candle.StartMS), EndMS: int64Ref(candle.MiddleMS)}
	current := marketquestion.FactTimeReference{Role: "CURRENT", StartMS: int64Ref(candle.MiddleMS), EndMS: int64Ref(candle.EndMS)}
	comparison := marketquestion.FactTimeReference{Role: "COMPARISON", StartMS: int64Ref(candle.StartMS), EndMS: int64Ref(candle.EndMS)}
	subject := marketquestion.FactSubject{MarketScope: scope.ActualMarketScope, Exchange: candle.Target}
	previousDenominator := candle.PreviousDenominatorKRW
	currentDenominator := candle.CurrentDenominatorKRW
	previousVolume, currentVolume := candle.PreviousVolumeKRW, candle.CurrentVolumeKRW
	previousShare, currentShare := candle.PreviousSharePct, candle.CurrentSharePct
	shareDelta, volumeDelta := candle.ShareDeltaPP, candle.VolumeDeltaKRW
	builder.float("fact_candle_previous_denominator_krw", "DOMESTIC_VOLUME_KRW", "DENOMINATOR", marketquestion.FactSubject{MarketScope: scope.ActualMarketScope, Exchange: "included_set"}, &previousDenominator, "KRW", previous, nil, nil)
	builder.float("fact_candle_current_denominator_krw", "DOMESTIC_VOLUME_KRW", "DENOMINATOR", marketquestion.FactSubject{MarketScope: scope.ActualMarketScope, Exchange: "included_set"}, &currentDenominator, "KRW", current, nil, nil)
	prevID := builder.float("fact_candle_previous_target_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", subject, &previousVolume, "KRW", previous, nil, nil)
	currID := builder.float("fact_candle_current_target_volume_krw", "EXCHANGE_VOLUME_KRW", "LEVEL", subject, &currentVolume, "KRW", current, nil, nil)
	builder.float("fact_candle_previous_target_share_pct", "EXCHANGE_SHARE_PCT", "LEVEL", subject, &previousShare, "PCT", previous, []string{"denominator_previous"}, builder.relation(marketquestion.AggregationRecomputeRatio, prevID, "fact_candle_previous_denominator_krw"))
	builder.float("fact_candle_current_target_share_pct", "EXCHANGE_SHARE_PCT", "LEVEL", subject, &currentShare, "PCT", current, []string{"denominator_current"}, builder.relation(marketquestion.AggregationRecomputeRatio, currID, "fact_candle_current_denominator_krw"))
	builder.float("fact_candle_target_share_delta_pp", "SHARE_DELTA_PP", "CHANGE", subject, &shareDelta, "PP", comparison, []string{"denominator_previous", "denominator_current"}, builder.relation(marketquestion.AggregationEndpointDifference, "fact_candle_previous_target_share_pct", "fact_candle_current_target_share_pct"))
	builder.float("fact_candle_target_volume_delta_krw", "VOLUME_DELTA_KRW", "CHANGE", subject, &volumeDelta, "KRW", comparison, nil, builder.relation(marketquestion.AggregationEndpointDifference, prevID, currID))
}

func marketQuestionEvidenceCoinFacts(response marketanalysis.GroundedMarketQuestionResponse, builder *marketQuestionTypedFactBuilder) {
	coin := response.Facts.CoinComparison
	if coin == nil {
		return
	}
	timeRef := marketquestion.FactTimeReference{Role: "CURRENT", StartMS: int64Ref(coin.StartMS), EndMS: int64Ref(coin.EndMS)}
	builder.float("fact_coin_target_total_volume", "EXCHANGE_VOLUME_KRW", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Target}, coin.TargetVolumeKRW, "KRW", timeRef, nil, nil)
	builder.float("fact_coin_peer_total_volume", "EXCHANGE_VOLUME_KRW", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Peer}, coin.PeerVolumeKRW, "KRW", timeRef, nil, nil)
	builder.float("fact_coin_target_total_share", "EXCHANGE_SHARE_PCT", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Target}, coin.TargetSharePct, "PCT", timeRef, nil, nil)
	builder.float("fact_coin_peer_total_share", "EXCHANGE_SHARE_PCT", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Peer}, coin.PeerSharePct, "PCT", timeRef, nil, nil)
	for index, row := range coin.Rows {
		asset := strings.ToUpper(strings.TrimSpace(row.Asset))
		targetVolume, peerVolume, targetShare, peerShare, gap := row.TargetVolumeKRW, row.PeerVolumeKRW, row.TargetSharePct, row.PeerSharePct, row.ShareGapPP
		builder.float(fmt.Sprintf("fact_coin_%s_target_volume_%d", marketQuestionEvidenceIDPart(asset), index), "EXCHANGE_VOLUME_KRW", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Target, Asset: asset}, &targetVolume, "KRW", timeRef, nil, nil)
		builder.float(fmt.Sprintf("fact_coin_%s_peer_volume_%d", marketQuestionEvidenceIDPart(asset), index), "EXCHANGE_VOLUME_KRW", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Peer, Asset: asset}, &peerVolume, "KRW", timeRef, nil, nil)
		builder.float(fmt.Sprintf("fact_coin_%s_target_share_%d", marketQuestionEvidenceIDPart(asset), index), "ASSET_CAPTURE_PCT", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Target, Asset: asset}, &targetShare, "PCT", timeRef, nil, nil)
		builder.float(fmt.Sprintf("fact_coin_%s_peer_share_%d", marketQuestionEvidenceIDPart(asset), index), "ASSET_CAPTURE_PCT", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Peer, Asset: asset}, &peerShare, "PCT", timeRef, nil, nil)
		builder.float(fmt.Sprintf("fact_coin_%s_share_gap_%d", marketQuestionEvidenceIDPart(asset), index), "EXCHANGE_SHARE_GAP_PP", "CHANGE", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: coin.Target, Asset: asset}, &gap, "PP", timeRef, nil, nil)
	}
}

func marketQuestionEvidenceDailyFacts(response marketanalysis.GroundedMarketQuestionResponse, builder *marketQuestionTypedFactBuilder) {
	series := response.Facts.DailyEndpointSeries
	if series == nil {
		return
	}
	for index, point := range series.Points {
		timeRef := marketquestion.FactTimeReference{Role: "ENDPOINT", AsOfMS: int64Ref(point.BucketMS)}
		builder.float(fmt.Sprintf("fact_daily_endpoint_domestic_volume_%d", index), "DOMESTIC_VOLUME_KRW", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "domestic"}, point.TotalVolumeKRW, "KRW", timeRef, nil, nil)
		builder.float(fmt.Sprintf("fact_daily_endpoint_bithumb_volume_%d", index), "EXCHANGE_VOLUME_KRW", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "bithumb"}, point.BithumbVolumeKRW, "KRW", timeRef, nil, nil)
		builder.float(fmt.Sprintf("fact_daily_endpoint_bithumb_share_%d", index), "EXCHANGE_SHARE_PCT", "LEVEL", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "bithumb"}, point.BithumbSharePct, "PCT", timeRef, nil, nil)
	}
	if series.Cumulative.EndpointComparison {
		timeRef := marketquestion.FactTimeReference{Role: "COMPARISON", StartMS: positiveInt64Ref(series.Cumulative.FromBucketMS), EndMS: positiveInt64Ref(series.Cumulative.ToBucketMS)}
		builder.float("fact_daily_cumulative_share_delta_pp", "SHARE_DELTA_PP", "CHANGE", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "bithumb"}, series.Cumulative.ShareDeltaPP, "PP", timeRef, nil, nil)
		builder.float("fact_daily_cumulative_domestic_volume_delta_krw", "VOLUME_DELTA_KRW", "CHANGE", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "domestic"}, series.Cumulative.DomesticVolumeDeltaKRW, "KRW", timeRef, nil, nil)
		builder.float("fact_daily_cumulative_bithumb_volume_delta_krw", "VOLUME_DELTA_KRW", "CHANGE", marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: "bithumb"}, series.Cumulative.BithumbVolumeDeltaKRW, "KRW", timeRef, nil, nil)
	}
}

func marketQuestionEvidenceDenominators(response marketanalysis.GroundedMarketQuestionResponse, scope marketquestion.ScopeIdentity, registry marketquestion.RegistryIdentity, builder *marketQuestionTypedFactBuilder) []marketquestion.DenominatorIdentity {
	result := []marketquestion.DenominatorIdentity{}
	registryID := ""
	if registry.Status == marketquestion.IdentityConfirmed {
		registryID = registry.Identity
	}
	add := func(id, meaning, role string, value *float64) {
		quantity := marketquestion.UnknownQuantity("KRW")
		if value != nil {
			var err error
			quantity, err = marketquestion.ConfirmedQuantity(strconv.FormatFloat(*value, 'f', -1, 64), "KRW")
			if err != nil {
				builder.err = err
				return
			}
		}
		result = append(result, marketquestion.DenominatorIdentity{ID: id, Meaning: meaning, Scope: scope.ActualMarketScope, Value: quantity, RegistryID: registryID, SourceID: marketQuestionEvidenceSourceID})
	}
	if candle := response.Facts.CandleComparison; candle != nil {
		add("denominator_previous", marketQuestionEvidenceDenominatorMeaning(response, scope), "previous", &candle.PreviousDenominatorKRW)
		add("denominator_current", marketQuestionEvidenceDenominatorMeaning(response, scope), "current", &candle.CurrentDenominatorKRW)
		return result
	}
	if response.Facts.Previous != nil {
		add("denominator_previous", marketQuestionEvidenceDenominatorMeaning(response, scope), "previous", response.Facts.Previous.DomesticVolumeKRW)
	}
	if response.Facts.Current != nil {
		add("denominator_current", marketQuestionEvidenceDenominatorMeaning(response, scope), "current", response.Facts.Current.DomesticVolumeKRW)
	}
	return result
}

func marketQuestionEvidenceDenominatorMeaning(response marketanalysis.GroundedMarketQuestionResponse, scope marketquestion.ScopeIdentity) string {
	if response.Facts.CoinComparison != nil {
		return "대상·비교 거래소의 동일 코인 거래대금 합계"
	}
	if response.Facts.CandleComparison != nil {
		return "두 구간에 모두 공식 봉이 있는 동일 종목·포함 거래소 거래대금 합계"
	}
	if response.Query.Asset != "" && response.Query.Metric == marketquestion.MetricAssetBithumbCapture {
		return "포함 거래소의 동일 자산 거래대금"
	}
	return "포함 거래소·시장 집합의 KRW 거래대금 합계"
}

func marketQuestionPrimaryEvidenceSource(response marketanalysis.GroundedMarketQuestionResponse, semantics marketquestion.TimeSemantics) marketquestion.SourceIdentity {
	status := marketquestion.IdentityConfirmed
	reason := ""
	if strings.TrimSpace(response.Facts.EvidenceLayer) == "" {
		status = marketquestion.IdentityUnavailable
		reason = "RESPONSE_HAS_NO_TYPED_EVIDENCE_LAYER"
	}
	return marketquestion.SourceIdentity{ID: marketQuestionEvidenceSourceID, EvidenceLayer: response.Facts.EvidenceLayer, Tool: marketQuestionEvidenceTool(response), Status: status, ObservedAtMS: semantics.Actual.ObservedAtMS, Reason: reason}
}

func marketQuestionEvidenceTool(response marketanalysis.GroundedMarketQuestionResponse) marketquestion.EvidenceToolID {
	layer := strings.ToUpper(response.Facts.EvidenceLayer)
	switch {
	case strings.Contains(layer, "DAILY_ROLLING"):
		return marketquestion.ToolOfficialDailyEndpointSeries
	case strings.Contains(layer, "CALENDAR_DAY"):
		return marketquestion.ToolOfficialCalendarDay
	case strings.Contains(layer, "OFFICIAL") && strings.Contains(layer, "ALIGNED") && response.Query.Comparison == marketquestion.ComparisonRolling24hEndpointDelta:
		return marketquestion.ToolOfficialAlignedWindow
	case strings.Contains(layer, "OFFICIAL_REST_CANDLE_MATCHED_MARKETS") && response.Query.Comparison == marketquestion.ComparisonCurrentPreviousInterval:
		return marketquestion.ToolOfficialFixedIntervalCandle
	case strings.Contains(layer, "OFFICIAL") && response.Query.Comparison == marketquestion.ComparisonCurrentSnapshot:
		return marketquestion.ToolCurrentOfficialTicker
	case strings.Contains(layer, "WEBSOCKET"):
		return marketquestion.ToolWebsocketRangeAggregate
	default:
		return ""
	}
}

func marketQuestionEvidenceTime(response marketanalysis.GroundedMarketQuestionResponse) marketquestion.TimeSemantics {
	result := marketquestion.TimeSemantics{Timezone: "Asia/Seoul", Status: marketquestion.IdentityUnavailable, Reason: "RESPONSE_HAS_NO_ACTUAL_EVIDENCE_TIME"}
	query := response.Query
	switch query.Comparison {
	case marketquestion.ComparisonCurrentSnapshot:
		result.Basis, result.AggregationSemantics = marketquestion.TimeBasisRolling24hSnapshot, marketquestion.TimeAggregationSnapshotLevel
	case marketquestion.ComparisonCalendarDayClose:
		result.Basis, result.AggregationSemantics = marketquestion.TimeBasisCalendarDay, marketquestion.TimeAggregationClosedIntervalSum
	case marketquestion.ComparisonDailyRolling24hEndpoints:
		result.Basis, result.AggregationSemantics = marketquestion.TimeBasisDailyEndpointSeries, marketquestion.TimeAggregationEndpointDifference
	case marketquestion.ComparisonFixedHistoricalInterval:
		result.Basis, result.AggregationSemantics = marketquestion.TimeBasisFixedInterval, marketquestion.TimeAggregationClosedIntervalSum
	case marketquestion.ComparisonRolling24hEndpointDelta:
		result.Basis, result.AggregationSemantics = marketquestion.TimeBasisRolling24hEndpointComparison, marketquestion.TimeAggregationEndpointDifference
	case marketquestion.ComparisonCurrentPreviousInterval:
		result.Basis, result.AggregationSemantics = marketquestion.TimeBasisAdjacentFixedIntervals, marketquestion.TimeAggregationAdjacentIntervalSum
	}
	result.RequestedWindowSec = marketQuestionEvidenceRequestedWindowSec(query)
	if query.TargetStartMS > 0 {
		result.Requested.StartMS = int64Ref(query.TargetStartMS)
	}
	if query.TargetEndMS > 0 {
		result.Requested.EndMS = int64Ref(query.TargetEndMS)
	}
	if response.Facts.Previous != nil {
		result.Actual.StartMS = positiveInt64Ref(response.Facts.Previous.StartMS)
		if result.Actual.StartMS == nil {
			result.Actual.StartMS = positiveInt64Ref(response.Facts.Previous.EndMS)
		}
	}
	if response.Facts.Current != nil {
		result.Actual.EndMS = positiveInt64Ref(response.Facts.Current.EndMS)
		if result.Actual.EndMS == nil {
			result.Actual.EndMS = positiveInt64Ref(response.Facts.Current.StartMS)
		}
	}
	if candle := response.Facts.CandleComparison; candle != nil {
		result.Actual.StartMS = positiveInt64Ref(candle.StartMS)
		result.Actual.EndMS = positiveInt64Ref(candle.EndMS)
	}
	if coin := response.Facts.CoinComparison; coin != nil {
		result.Actual.StartMS = positiveInt64Ref(coin.StartMS)
		result.Actual.EndMS = positiveInt64Ref(coin.EndMS)
	}
	if series := response.Facts.DailyEndpointSeries; series != nil && len(series.Points) > 0 {
		result.Actual.StartMS = positiveInt64Ref(series.Points[0].BucketMS)
		result.Actual.EndMS = positiveInt64Ref(series.Points[len(series.Points)-1].BucketMS)
	}
	actualIntervalMissing := result.Actual.StartMS == nil || result.Actual.EndMS == nil || *result.Actual.StartMS >= *result.Actual.EndMS
	if (query.Comparison == marketquestion.ComparisonCalendarDayClose || query.Comparison == marketquestion.ComparisonFixedHistoricalInterval) &&
		actualIntervalMissing && query.TargetStartMS > 0 && query.TargetEndMS > query.TargetStartMS && response.Facts.Current != nil {
		result.Actual.StartMS = int64Ref(query.TargetStartMS)
		result.Actual.EndMS = int64Ref(query.TargetEndMS)
	}
	if response.Provenance.ComparisonEndMS > 0 {
		result.Actual.EndMS = int64Ref(response.Provenance.ComparisonEndMS)
	}
	if response.Provider.AsOfMS > 0 {
		result.Actual.AsOfMS = int64Ref(response.Provider.AsOfMS)
		if response.Facts.CandleComparison == nil {
			result.Actual.ObservedAtMS = int64Ref(response.Provider.AsOfMS)
		}
	}
	if query.Comparison == marketquestion.ComparisonCurrentSnapshot {
		result.Actual.StartMS, result.Actual.EndMS = nil, nil
	}
	if result.Actual.StartMS != nil && result.Actual.EndMS != nil && *result.Actual.EndMS >= *result.Actual.StartMS {
		result.ActualWindowSec = (*result.Actual.EndMS - *result.Actual.StartMS) / 1000
	}
	if result.Requested.EndMS != nil && result.Actual.EndMS != nil {
		result.AlignmentDeltaMS = *result.Actual.EndMS - *result.Requested.EndMS
	}
	actualTimeConfirmed := result.Actual.StartMS != nil && result.Actual.EndMS != nil && *result.Actual.StartMS < *result.Actual.EndMS
	if query.Comparison == marketquestion.ComparisonCurrentSnapshot {
		actualTimeConfirmed = result.Actual.AsOfMS != nil
	}
	if actualTimeConfirmed {
		result.Status, result.Reason = marketquestion.IdentityConfirmed, ""
	}
	return result
}

func marketQuestionEvidenceRequestedWindowSec(query marketanalysis.MarketQuestionQuery) int64 {
	if query.TargetStartMS > 0 && query.TargetEndMS > query.TargetStartMS {
		return (query.TargetEndMS - query.TargetStartMS) / 1000
	}
	switch query.TimeUnit {
	case marketanalysis.MarketQuestionTimeMinute:
		return query.TimeValue * 60
	case marketanalysis.MarketQuestionTimeHour:
		return query.TimeValue * 3600
	default:
		return 0
	}
}

func marketQuestionEvidenceScope(response marketanalysis.GroundedMarketQuestionResponse) marketquestion.ScopeIdentity {
	requested := append([]string(nil), marketQuestionDomesticExchanges...)
	result := marketquestion.ScopeIdentity{RequestedMarketScope: response.Query.MarketScope, RequestedAssetSet: "REQUESTED_MARKET_REGISTRY", RequestedExchanges: requested, Status: marketquestion.ScopeUnavailable, Reason: "RESPONSE_DOES_NOT_EXPOSE_INCLUDED_EXCHANGE_OR_MARKET_IDENTITY"}
	if coin := response.Facts.CoinComparison; coin != nil {
		result.RequestedExchanges = []string{coin.Target, coin.Peer}
		result.IncludedExchanges = []string{coin.Target, coin.Peer}
		result.ActualMarketScope = response.Query.MarketScope
		result.RequestedAssetSet, result.ActualAssetSet = "SAME_ASSET_PAIR", "SAME_ASSET_PAIR"
		result.SameAssetSet, result.Status, result.Reason = true, marketquestion.ScopeExact, ""
		return result
	}
	if candle := response.Facts.CandleComparison; candle != nil {
		for exchange := range candle.MarketCounts {
			result.IncludedExchanges = append(result.IncludedExchanges, exchange)
		}
		for exchange, reason := range candle.ExcludedExchanges {
			result.ExcludedExchanges = append(result.ExcludedExchanges, marketquestion.ScopeExclusion{Exchange: exchange, Reason: reason})
		}
		result.ActualMarketScope = "KRW_SAME_ASSET_SET_OFFICIAL_CANDLES"
		result.ActualAssetSet = "INTERSECTION_WITH_COMPLETE_CANDLES_IN_BOTH_INTERVALS"
		result.SameAssetSet, result.Status = true, marketquestion.ScopeSubstitutedPartial
		result.Reason = "ACTUAL_SCOPE_IS_COMPLETE_SAME_ASSET_SUBSET_NOT_DOMESTIC_ALL_MARKETS"
		return result
	}
	if series := response.Facts.DailyEndpointSeries; series != nil && len(series.IncludedExchanges) > 0 {
		result.IncludedExchanges = append([]string(nil), series.IncludedExchanges...)
		for exchange, reason := range series.ExcludedExchanges {
			result.ExcludedExchanges = append(result.ExcludedExchanges, marketquestion.ScopeExclusion{Exchange: exchange, Reason: reason})
		}
		result.ActualMarketScope, result.ActualAssetSet = "KRW_INCLUDED_EXCHANGES", "HISTORICAL_ENDPOINT_ASSET_SET"
		result.Status, result.Reason = marketQuestionCompareScope(requested, result.IncludedExchanges)
		return result
	}
	layer := strings.ToUpper(response.Facts.EvidenceLayer)
	switch {
	case strings.Contains(layer, "5_EXCHANGE") && response.Quality.Status == "LIVE":
		result.IncludedExchanges = append([]string(nil), marketQuestionDomesticExchanges...)
		result.ActualMarketScope, result.ActualAssetSet = response.Query.MarketScope, "SOURCE_SNAPSHOT_MARKET_SET"
		result.Status, result.Reason = marketquestion.ScopeExact, ""
	case strings.Contains(layer, "OFFICIAL") && strings.Contains(layer, "ALIGNED"):
		result.IncludedExchanges = []string{"upbit", "bithumb", "coinone"}
		result.ExcludedExchanges = []marketquestion.ScopeExclusion{{Exchange: "korbit", Reason: "EXACT_KRW_ALIGNED_SOURCE_NOT_INCLUDED"}, {Exchange: "gopax", Reason: "EXACT_KRW_ALIGNED_SOURCE_NOT_INCLUDED"}}
		result.ActualMarketScope, result.ActualAssetSet = "KRW_THREE_EXCHANGE_ALIGNED", "SOURCE_ENDPOINT_MARKET_SET"
		result.Status, result.Reason = marketquestion.ScopeSubstitutedPartial, "REQUESTED_DOMESTIC_SCOPE_WAS_REDUCED_TO_THREE_EXCHANGE_ALIGNED_SOURCE"
	}
	return result
}

func marketQuestionCompareScope(requested, actual []string) (marketquestion.ScopeStatus, string) {
	left, right := append([]string(nil), requested...), append([]string(nil), actual...)
	sort.Strings(left)
	sort.Strings(right)
	if strings.Join(left, ",") == strings.Join(right, ",") {
		return marketquestion.ScopeExact, ""
	}
	return marketquestion.ScopeSubstitutedPartial, "REQUESTED_AND_ACTUAL_EXCHANGE_MEMBERSHIP_DIFFER"
}

func marketQuestionEvidenceCoverage(response marketanalysis.GroundedMarketQuestionResponse) marketquestion.CoverageIdentity {
	coverage := response.Quality.CurrentCoverage
	result := marketquestion.CoverageIdentity{Basis: "SOURCE_REPORTED_TIME_COVERAGE", Status: marketquestion.IdentityUnavailable, Reason: "RESPONSE_DOES_NOT_EXPOSE_COVERAGE_BASIS_FOR_THIS_EVIDENCE_LAYER"}
	if coverage.RequiredSec > 0 || coverage.CoverageSec > 0 || coverage.GapSeconds > 0 {
		result.ConfirmedUnits, result.RequiredUnits, result.GapUnits, result.Unit = coverage.CoverageSec, coverage.RequiredSec, coverage.GapSeconds, "SECOND"
		if coverage.Status == "CONFIRMED" && coverage.WindowComplete && coverage.GapSeconds == 0 && coverage.CoverageSec >= coverage.RequiredSec {
			result.Status, result.Reason = marketquestion.IdentityConfirmed, ""
		} else {
			result.Reason = "SOURCE_REPORTED_WINDOW_INCOMPLETE_OR_UNCONFIRMED"
		}
		return result
	}
	if response.Facts.DailyEndpointSeries != nil {
		series := response.Facts.DailyEndpointSeries
		result.Basis, result.ConfirmedUnits, result.RequiredUnits, result.Unit = "EXPECTED_VS_OBSERVED_ALIGNED_HOURLY_ENDPOINTS", int64(series.ObservedHourlyPointCount), int64(series.ExpectedHourlyPointCount), "HOURLY_POINT"
		if series.PathComplete && series.ExpectedHourlyPointCount > 0 && series.ObservedHourlyPointCount == series.ExpectedHourlyPointCount {
			result.Status, result.Reason = marketquestion.IdentityConfirmed, ""
		}
		return result
	}
	if path := response.Facts.PathSummary; path != nil && path.ExpectedPointCount > 0 {
		result.Basis, result.ConfirmedUnits, result.RequiredUnits, result.Unit = "EXPECTED_VS_OBSERVED_ALIGNED_HOURLY_POINTS", int64(path.PointCount), int64(path.ExpectedPointCount), "HOURLY_POINT"
		result.GapUnits = result.RequiredUnits - result.ConfirmedUnits
		if path.Status == "COMPLETE" && path.PointCount == path.ExpectedPointCount {
			result.Status, result.Reason = marketquestion.IdentityConfirmed, ""
		} else {
			result.Reason = "ALIGNED_HOURLY_PATH_INCOMPLETE_OR_UNCONFIRMED"
		}
		return result
	}
	if candle := response.Facts.CandleComparison; candle != nil {
		for _, count := range candle.MarketCounts {
			result.ConfirmedUnits += int64(count)
		}
		result.Basis, result.Unit = "INCLUDED_SAME_ASSET_MARKETS_WITH_BOTH_INTERVAL_CANDLES", "MARKET"
		result.Reason = "REQUESTED_REGISTRY_MARKET_COUNT_IS_NOT_AVAILABLE_FOR_DENOMINATOR_COVERAGE"
		return result
	}
	layer := strings.ToUpper(strings.TrimSpace(response.Facts.EvidenceLayer))
	if strings.Contains(layer, "5_EXCHANGE") && response.Quality.Status == "LIVE" {
		result.Basis, result.ConfirmedUnits, result.RequiredUnits, result.Unit = "EXCHANGE_MEMBERSHIP_ONLY_NOT_AMOUNT_COVERAGE", 5, 5, "EXCHANGE"
		result.Status, result.Reason = marketquestion.IdentityConfirmed, ""
		return result
	}
	if strings.Contains(layer, "OFFICIAL") && strings.Contains(layer, "ALIGNED") {
		result.Basis, result.ConfirmedUnits, result.RequiredUnits, result.GapUnits, result.Unit = "EXCHANGE_MEMBERSHIP_ONLY_NOT_AMOUNT_COVERAGE", 3, 5, 2, "EXCHANGE"
		result.Reason = "TWO_REQUESTED_EXCHANGES_ARE_NOT_INCLUDED_IN_THE_ALIGNED_SOURCE"
	}
	return result
}

func marketQuestionEvidenceNotice(response marketanalysis.GroundedMarketQuestionResponse, builder *marketQuestionTypedFactBuilder) marketquestion.NoticeEvidence {
	requested := containsMarketQuestionString(response.Query.RequestedExplanation, "NOTICES") || response.Query.Intent == marketanalysis.MarketQuestionNoticeReaction
	context := response.Facts.Auxiliary
	if !requested && (context == nil || context.Notices == nil) {
		return marketquestion.NoticeEvidence{Status: marketquestion.NoticeNotQueried}
	}
	if context == nil || context.Notices == nil {
		return marketquestion.NoticeEvidence{Status: marketquestion.NoticeFailed, Reason: "NOTICE_CONTEXT_REQUESTED_BUT_NOT_QUERIED"}
	}
	notices := context.Notices
	result := marketquestion.NoticeEvidence{SourceCount: notices.SourceCount, HealthySourceCount: notices.HealthySourceCount, CausalClaim: false}
	if notices.SourceCount == 0 || notices.HealthySourceCount < notices.SourceCount {
		result.Status, result.Reason = marketquestion.NoticeFailed, "NOTICE_SOURCE_QUERY_INCOMPLETE"
		return result
	}
	if len(notices.Matches) == 0 {
		result.Status, result.Reason = marketquestion.NoticeNotFound, "NO_RELATED_NOTICE_FOUND_WITHIN_SEARCH_SCOPE"
		return result
	}
	result.Status = marketquestion.NoticeFound
	for index, notice := range notices.Matches {
		id := fmt.Sprintf("fact_notice_%s_%d", marketQuestionEvidenceIDPart(notice.NoticeID), index)
		result.FactIDs = append(result.FactIDs, builder.context(id, "NOTICE_CONTEXT", "CONTEXT", notice.Title, marketQuestionNoticeSourceID, marketquestion.FactSubject{MarketScope: response.Query.MarketScope, Exchange: notice.Exchange, Asset: strings.Join(notice.Assets, ",")}, marketquestion.FactTimeReference{Role: "NOTICE_ANCHOR", AsOfMS: int64Ref(notice.AnchorMS)}))
	}
	return result
}

func marketQuestionEvidenceContribution(response marketanalysis.GroundedMarketQuestionResponse, facts []marketquestion.EvidenceFact) marketquestion.ContributionIdentity {
	components := []string{}
	other := ""
	residual := ""
	for _, fact := range facts {
		if fact.Metric == "ASSET_ATTRIBUTION_PP" && fact.Role == "CONTRIBUTION" {
			components = append(components, fact.ID)
		}
		if fact.ID == marketquestion.CanonicalFactID("fact_driver_other_contribution_pp") && (fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
			other = fact.ID
		}
		if fact.ID == marketquestion.CanonicalFactID("fact_driver_residual_pp") && (fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
			residual = fact.ID
		}
	}
	if len(components) == 0 && other == "" && residual == "" {
		return marketquestion.ContributionIdentity{Status: marketquestion.IdentityUnavailable, Reason: "TYPED_CONTRIBUTION_COMPONENTS_OR_RESIDUAL_NOT_AVAILABLE"}
	}
	result := marketquestion.ContributionIdentity{
		Status: marketquestion.IdentityUnavailable, Method: marketanalysis.ContributionMethod,
		ComponentFactIDs: components, OtherFactID: other, ResidualFactID: residual,
		ResidualKind: "SOURCE_RECONCILIATION_RESIDUAL_MAY_INCLUDE_UPSTREAM_UNPUBLISHED_COMPONENTS",
		Reason:       "SOURCE_PATH_DOES_NOT_EXPOSE_VERSIONED_FULL_ATTRIBUTION_PROVENANCE",
	}
	if len(components) == 0 || other == "" || residual == "" {
		result.Reason = "TYPED_CONTRIBUTION_COMPONENTS_OTHER_OR_RESIDUAL_NOT_AVAILABLE"
		return result
	}
	if response.Query.Intent == marketanalysis.MarketQuestionWSIntervalCompare && strings.EqualFold(strings.TrimSpace(response.Facts.EvidenceLayer), "WEBSOCKET_RANGE_AGGREGATE") {
		result.Status = marketquestion.IdentityConfirmed
		result.MethodVersion = marketanalysis.ContributionMethodVersion
		result.ResidualKind = "NUMERIC_RECONCILIATION_ERROR_AFTER_FULL_ASSET_DECOMPOSITION"
		result.Reason = ""
	}
	return result
}

func marketQuestionEvidenceRequirements(response marketanalysis.GroundedMarketQuestionResponse, timeSemantics marketquestion.TimeSemantics, scope marketquestion.ScopeIdentity, coverage marketquestion.CoverageIdentity, denominators []marketquestion.DenominatorIdentity, facts []marketquestion.EvidenceFact, notice marketquestion.NoticeEvidence) []marketquestion.QuestionRequirement {
	confirmed := func(ids ...string) []string {
		result := []string{}
		for _, id := range ids {
			id = marketquestion.CanonicalFactID(id)
			for _, fact := range facts {
				if fact.ID == id && (fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
					result = append(result, id)
				}
			}
		}
		return result
	}
	requirements := marketQuestionEvidenceMetricRequirements(response, facts)
	timeReq := marketquestion.QuestionRequirement{ID: "time", Kind: "TIME", Required: true, Status: marketquestion.RequirementSatisfied}
	if timeSemantics.Status != marketquestion.IdentityConfirmed {
		timeReq.Status, timeReq.Reason = marketquestion.RequirementUnavailable, timeSemantics.Reason
	}
	requirements = append(requirements, timeReq)
	scopeReq := marketquestion.QuestionRequirement{ID: "scope", Kind: "SCOPE", Required: true, Status: marketquestion.RequirementSatisfied}
	if scope.Status != marketquestion.ScopeExact {
		scopeReq.Status, scopeReq.Reason = marketquestion.RequirementUnavailable, scope.Reason
	}
	requirements = append(requirements, scopeReq)
	coverageReq := marketquestion.QuestionRequirement{ID: "coverage", Kind: "COVERAGE", Required: true, Status: marketquestion.RequirementSatisfied}
	if coverage.Status != marketquestion.IdentityConfirmed {
		coverageReq.Status, coverageReq.Reason = marketquestion.RequirementUnavailable, coverage.Reason
	}
	requirements = append(requirements, coverageReq)
	denominatorFactIDs := confirmed("fact_current_domestic_volume_krw", "fact_previous_domestic_volume_krw", "fact_candle_current_denominator_krw", "fact_candle_previous_denominator_krw")
	if len(denominators) == 0 && len(denominatorFactIDs) == 0 {
		requirements = append(requirements, marketquestion.QuestionRequirement{ID: "denominator", Kind: "DENOMINATOR", Required: true, Status: marketquestion.RequirementUnavailable, Reason: "DENOMINATOR_MEMBERSHIP_OR_VALUE_UNAVAILABLE"})
	} else {
		requirements = append(requirements, evidenceRequirement("denominator", "DENOMINATOR", true, denominatorFactIDs, "DENOMINATOR_VALUE_UNAVAILABLE"))
	}
	if marketQuestionRequiresChange(response.Query) {
		requirements = append(requirements, marketQuestionEvidenceChangeRequirements(response, facts)...)
	}
	if containsMarketQuestionString(response.Query.RequestedExplanation, "DRIVERS") || containsMarketQuestionString(response.Query.RequestedExplanation, "EXCHANGES") || response.Query.Intent == marketanalysis.MarketQuestionMovementExplanation {
		ids := []string{}
		for _, fact := range facts {
			if fact.Role == "CONTRIBUTION" && (fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
				ids = append(ids, fact.ID)
			}
		}
		requirements = append(requirements, evidenceRequirement("attribution", "ATTRIBUTION", true, ids, "ARITHMETIC_ATTRIBUTION_UNAVAILABLE"))
	}
	if containsMarketQuestionString(response.Query.RequestedExplanation, "NOTICES") || response.Query.Intent == marketanalysis.MarketQuestionNoticeReaction {
		status, reason := marketquestion.RequirementSatisfied, ""
		if notice.Status == marketquestion.NoticeFailed || notice.Status == marketquestion.NoticeNotQueried {
			status, reason = marketquestion.RequirementUnavailable, notice.Reason
		}
		requirements = append(requirements, marketquestion.QuestionRequirement{ID: "notice_context", Kind: "NOTICE_CONTEXT", Required: true, Status: status, FactIDs: append([]string(nil), notice.FactIDs...), Reason: reason})
		requirements = append(requirements, marketquestion.QuestionRequirement{ID: "causality_limit", Kind: "CAUSALITY_LIMIT", Required: true, Status: marketquestion.RequirementSatisfied})
	}
	return requirements
}

func marketQuestionEvidenceMetricRequirements(response marketanalysis.GroundedMarketQuestionResponse, facts []marketquestion.EvidenceFact) []marketquestion.QuestionRequirement {
	query := response.Query
	requirements := []marketquestion.QuestionRequirement{}
	add := func(id, kind, metric, role, exchange string, minimum int) {
		ids := confirmedEvidenceFactIDs(facts, metric, role, exchange, query.Asset)
		requirements = append(requirements, evidenceRequirementWithMinimum(id, kind, ids, minimum, "REQUESTED_"+strings.ToUpper(id)+"_UNAVAILABLE"))
	}
	if coin := response.Facts.CoinComparison; coin != nil {
		requirements = append(requirements,
			evidenceRequirement("metric_target_share", "RATIO", true, confirmedEvidenceExactFactIDs(facts, "fact_coin_target_total_share"), "REQUESTED_METRIC_TARGET_SHARE_UNAVAILABLE"),
			evidenceRequirement("metric_target_volume", "AMOUNT", true, confirmedEvidenceExactFactIDs(facts, "fact_coin_target_total_volume"), "REQUESTED_METRIC_TARGET_VOLUME_UNAVAILABLE"),
			evidenceRequirement("metric_peer_share", "RATIO", true, confirmedEvidenceExactFactIDs(facts, "fact_coin_peer_total_share"), "REQUESTED_METRIC_PEER_SHARE_UNAVAILABLE"),
			evidenceRequirement("metric_peer_volume", "AMOUNT", true, confirmedEvidenceExactFactIDs(facts, "fact_coin_peer_total_volume"), "REQUESTED_METRIC_PEER_VOLUME_UNAVAILABLE"),
			evidenceRequirementWithMinimum("metric_coin_target_share", "RATIO", confirmedEvidenceAssetFactIDs(facts, "ASSET_CAPTURE_PCT", coin.Target), 1, "REQUESTED_COIN_TARGET_SHARE_BREAKDOWN_UNAVAILABLE"),
			evidenceRequirementWithMinimum("metric_coin_target_volume", "AMOUNT", confirmedEvidenceAssetFactIDs(facts, "EXCHANGE_VOLUME_KRW", coin.Target), 1, "REQUESTED_COIN_TARGET_VOLUME_BREAKDOWN_UNAVAILABLE"),
			evidenceRequirementWithMinimum("metric_coin_peer_share", "RATIO", confirmedEvidenceAssetFactIDs(facts, "ASSET_CAPTURE_PCT", coin.Peer), 1, "REQUESTED_COIN_PEER_SHARE_BREAKDOWN_UNAVAILABLE"),
			evidenceRequirementWithMinimum("metric_coin_peer_volume", "AMOUNT", confirmedEvidenceAssetFactIDs(facts, "EXCHANGE_VOLUME_KRW", coin.Peer), 1, "REQUESTED_COIN_PEER_VOLUME_BREAKDOWN_UNAVAILABLE"),
		)
		return requirements
	}
	if query.Comparison == marketquestion.ComparisonDailyRolling24hEndpoints {
		add("metric_share_endpoint_series", "RATIO", "EXCHANGE_SHARE_PCT", "ENDPOINT", "bithumb", 2)
		add("metric_volume_endpoint_series", "AMOUNT", "EXCHANGE_VOLUME_KRW", "ENDPOINT", "bithumb", 2)
		return requirements
	}
	if query.Metric == marketquestion.MetricOverallBithumbShare || query.Metric == marketquestion.MetricTargetExchangeShareVolume {
		exchange := "bithumb"
		if query.Metric == marketquestion.MetricTargetExchangeShareVolume {
			exchange = strings.ToLower(strings.TrimSpace(query.Exchange))
		}
		if candle := response.Facts.CandleComparison; candle != nil {
			exchange = strings.ToLower(strings.TrimSpace(candle.Target))
		}
		roles := []string{"CURRENT"}
		if marketQuestionRequiresChange(query) {
			roles = []string{"PREVIOUS", "CURRENT"}
		}
		for _, role := range roles {
			suffix := strings.ToLower(role)
			add("metric_"+suffix+"_share", "RATIO", "EXCHANGE_SHARE_PCT", role, exchange, 1)
			add("metric_"+suffix+"_volume", "AMOUNT", "EXCHANGE_VOLUME_KRW", role, exchange, 1)
		}
		return requirements
	}
	metricNames := map[marketquestion.MetricID][]string{
		marketquestion.MetricAssetCombined:           {"ASSET_CAPTURE_PCT", "ASSET_INTERNAL_PCT", "ASSET_TOTAL_SHARE_PCT"},
		marketquestion.MetricAssetBithumbCapture:     {"ASSET_CAPTURE_PCT"},
		marketquestion.MetricAssetShareWithinBithumb: {"ASSET_INTERNAL_PCT"},
		marketquestion.MetricAssetTotalContribution:  {"ASSET_TOTAL_SHARE_PCT"},
	}[query.Metric]
	roles := []string{"CURRENT"}
	if marketQuestionRequiresChange(query) {
		roles = []string{"PREVIOUS", "CURRENT"}
	}
	for _, metric := range metricNames {
		for _, role := range roles {
			id := "metric_" + strings.ToLower(strings.TrimSuffix(metric, "_PCT")) + "_" + strings.ToLower(role)
			add(id, "RATIO", metric, role, "bithumb", 1)
		}
	}
	if len(requirements) == 0 {
		requirements = append(requirements, marketquestion.QuestionRequirement{ID: "metric", Kind: "METRIC", Required: true, Status: marketquestion.RequirementUnsupported, Reason: "METRIC_REQUIREMENTS_NOT_CATALOGUED"})
	}
	return requirements
}

func marketQuestionEvidenceChangeRequirements(response marketanalysis.GroundedMarketQuestionResponse, facts []marketquestion.EvidenceFact) []marketquestion.QuestionRequirement {
	query := response.Query
	requirements := []marketquestion.QuestionRequirement{}
	add := func(id, kind, metric, exchange string) {
		ids := confirmedEvidenceFactIDs(facts, metric, "CHANGE", exchange, query.Asset)
		requirements = append(requirements, evidenceRequirement(id, kind, true, ids, "REQUESTED_"+strings.ToUpper(id)+"_UNAVAILABLE"))
	}
	if query.Metric == marketquestion.MetricAssetCombined || query.Metric == marketquestion.MetricAssetBithumbCapture {
		add("change_asset_capture", "CHANGE", "ASSET_CAPTURE_DELTA_PP", "bithumb")
	}
	if query.Metric == marketquestion.MetricAssetCombined || query.Metric == marketquestion.MetricAssetShareWithinBithumb {
		add("change_asset_internal", "CHANGE", "ASSET_INTERNAL_DELTA_PP", "bithumb")
	}
	if query.Metric == marketquestion.MetricAssetCombined || query.Metric == marketquestion.MetricAssetTotalContribution {
		add("change_asset_total_contribution", "CHANGE", "ASSET_TOTAL_SHARE_DELTA_PP", "bithumb")
	}
	if len(requirements) > 0 {
		return requirements
	}
	exchange := "bithumb"
	if query.Metric == marketquestion.MetricTargetExchangeShareVolume {
		exchange = strings.ToLower(strings.TrimSpace(query.Exchange))
	}
	if candle := response.Facts.CandleComparison; candle != nil {
		exchange = strings.ToLower(strings.TrimSpace(candle.Target))
	}
	add("change_share", "CHANGE", "SHARE_DELTA_PP", exchange)
	add("change_volume", "CHANGE", "VOLUME_DELTA_KRW", exchange)
	return requirements
}

func confirmedEvidenceFactIDs(facts []marketquestion.EvidenceFact, metric, role, exchange, asset string) []string {
	result := []string{}
	for _, fact := range facts {
		roleMatches := fact.Role == role
		if role != "CHANGE" {
			roleMatches = fact.Role == "LEVEL" && fact.Time.Role == role
		}
		if fact.Metric != metric || !roleMatches || (fact.Value.State != marketquestion.QuantityConfirmedValue && fact.Value.State != marketquestion.QuantityConfirmedZero) {
			continue
		}
		if exchange != "" && !strings.EqualFold(fact.Subject.Exchange, exchange) {
			continue
		}
		if asset != "" && fact.Subject.Asset != "" && !strings.EqualFold(fact.Subject.Asset, asset) {
			continue
		}
		result = append(result, fact.ID)
	}
	return result
}

func confirmedEvidenceExactFactIDs(facts []marketquestion.EvidenceFact, logicalIDs ...string) []string {
	result := []string{}
	for _, logicalID := range logicalIDs {
		id := marketquestion.CanonicalFactID(logicalID)
		for _, fact := range facts {
			if fact.ID == id && (fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
				result = append(result, id)
				break
			}
		}
	}
	return result
}

func confirmedEvidenceAssetFactIDs(facts []marketquestion.EvidenceFact, metric, exchange string) []string {
	result := []string{}
	for _, fact := range facts {
		if fact.Metric == metric && fact.Role == "LEVEL" && fact.Subject.Asset != "" && strings.EqualFold(fact.Subject.Exchange, exchange) &&
			(fact.Value.State == marketquestion.QuantityConfirmedValue || fact.Value.State == marketquestion.QuantityConfirmedZero) {
			result = append(result, fact.ID)
		}
	}
	return result
}

func evidenceRequirementWithMinimum(id, kind string, factIDs []string, minimum int, unavailableReason string) marketquestion.QuestionRequirement {
	result := evidenceRequirement(id, kind, true, factIDs, unavailableReason)
	if len(factIDs) < minimum {
		result.Status, result.Reason = marketquestion.RequirementUnavailable, unavailableReason
	}
	return result
}

func evidenceRequirement(id, kind string, required bool, factIDs []string, unavailableReason string) marketquestion.QuestionRequirement {
	result := marketquestion.QuestionRequirement{ID: id, Kind: kind, Required: required, Status: marketquestion.RequirementSatisfied, FactIDs: append([]string(nil), factIDs...)}
	if len(factIDs) == 0 {
		result.Status, result.Reason = marketquestion.RequirementUnavailable, unavailableReason
	}
	return result
}

func marketQuestionWindowFactTime(role string, window *marketanalysis.MarketQuestionIntervalWindow) marketquestion.FactTimeReference {
	result := marketquestion.FactTimeReference{Role: role, StartMS: positiveInt64Ref(window.StartMS), EndMS: positiveInt64Ref(window.EndMS)}
	if result.StartMS != nil && result.EndMS != nil && *result.StartMS == *result.EndMS {
		result.AsOfMS, result.StartMS, result.EndMS = result.EndMS, nil, nil
	}
	return result
}

func marketQuestionBundleFactTime(response marketanalysis.GroundedMarketQuestionResponse) marketquestion.FactTimeReference {
	result := marketquestion.FactTimeReference{Role: "COMPARISON"}
	if response.Facts.Previous != nil {
		result.StartMS = positiveInt64Ref(response.Facts.Previous.StartMS)
		if result.StartMS == nil {
			result.StartMS = positiveInt64Ref(response.Facts.Previous.EndMS)
		}
	}
	if response.Facts.Current != nil {
		result.EndMS = positiveInt64Ref(response.Facts.Current.EndMS)
		if result.EndMS == nil {
			result.EndMS = positiveInt64Ref(response.Facts.Current.StartMS)
		}
	}
	return result
}

func marketQuestionBundleFactTimeRole(response marketanalysis.GroundedMarketQuestionResponse, role string) marketquestion.FactTimeReference {
	if role == "PREVIOUS" && response.Facts.Previous != nil {
		return marketQuestionWindowFactTime(role, response.Facts.Previous)
	}
	if role == "CURRENT" && response.Facts.Current != nil {
		return marketQuestionWindowFactTime(role, response.Facts.Current)
	}
	return marketquestion.FactTimeReference{Role: role}
}

func marketQuestionEvidenceQuestionDigest(question string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(question)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return "question_" + hex.EncodeToString(sum[:16])
}

func marketQuestionEvidenceTargetExchange(query marketanalysis.MarketQuestionQuery) string {
	if query.Exchange != "" {
		return query.Exchange
	}
	return "bithumb"
}
func marketQuestionRequiresChange(query marketanalysis.MarketQuestionQuery) bool {
	if strings.TrimSpace(query.PeerExchange) != "" {
		return false
	}
	switch query.Comparison {
	case marketquestion.ComparisonRolling24hEndpointDelta,
		marketquestion.ComparisonCurrentPreviousInterval,
		marketquestion.ComparisonDailyRolling24hEndpoints:
		return true
	default:
		return false
	}
}
func subjectWithAsset(subject marketquestion.FactSubject, asset string) marketquestion.FactSubject {
	subject.Asset = asset
	return subject
}
func existingDenominatorIDs(ids []string, denominators []marketquestion.DenominatorIdentity) []string {
	result := []string{}
	for _, id := range ids {
		for _, d := range denominators {
			if d.ID == id {
				result = append(result, id)
			}
		}
	}
	return result
}
func int64Ref(value int64) *int64 { copy := value; return &copy }
func positiveInt64Ref(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return int64Ref(value)
}
func containsMarketQuestionString(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}
func marketQuestionEvidenceIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
