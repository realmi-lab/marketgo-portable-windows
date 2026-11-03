package analysis

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"market-share-go/internal/domain"
	"market-share-go/internal/notice"
	"market-share-go/internal/orderbook"
)

const (
	partialReactionStudy      = "INCOMPLETE_CONTIGUOUS_COVERAGE"
	incompleteFXReactionStudy = "INCOMPLETE_FX_CONVERSION"
)

func incompleteReactionStatus(status string) bool {
	return status == partialReactionStudy || status == incompleteFXReactionStudy
}

var reactionOriginLabels = map[string]string{
	"DOMESTIC_NOTICE_FIRST": "국내 공지·일정이 먼저 관측",
	"GLOBAL_FIRST":          "Binance Spot 반응이 먼저 관측(보조)",
	"BINANCE_FIRST":         "Binance Spot 반응이 먼저 관측(레거시)",
	"MIXED":                 "국내·Binance Spot 반응이 비슷한 시각",
	"DOMESTIC_MARKET_FIRST": "국내 시장 반응이 먼저 관측",
	"UNRESOLVED":            "시간상 선후 판단 보류",
}

var reactionNoticePriority = map[string]int{
	"DEPOSIT_WITHDRAWAL": 3, "DEPOSIT": 3, "WITHDRAWAL": 3, "CAUTION": 3,
	"DELISTING": 3, "LISTING": 3, "SECURITY": 3, "WALLET": 3,
	"NETWORK": 3, "NETWORK_OUTAGE": 3, "NETWORK_UPGRADE": 2,
	"TOKEN_SWAP": 2, "REBRAND": 2, "SNAPSHOT": 2, "AIRDROP": 2,
	"MARKET_ADDITION": 2, "QUOTE_MARKET_CHANGE": 2, "MAINTENANCE": 2,
	"UPDATE": 1, "EVENT": 1,
}

var reactionEvidencePriority = map[string]int{"STRONG": 3, "MODERATE": 2, "WEAK": 1}
var reactionTerminalTitle = regexp.MustCompile(`(?i)완료|재개|해제|종료|취소|복구|종결|거래지원\s*종료|resum(?:e|ed)?|reopen|complete(?:d)?|closed|ended|cancel(?:led)?`)

const reactionCurrentWindowContributionScope = "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL"

// BuildReactionSummary creates the frontend reaction contract from the same
// immutable analysis and notice snapshots used by the rest of the response.
func BuildReactionSummary(analysis Analysis, snapshot notice.Snapshot, asOf float64) map[string]any {
	result, _ := BuildReactionSummaryWithProviders(analysis, snapshot, asOf, ReactionProviders{})
	return result
}

type ReactionStudyProvider func(item notice.Item, asset string, anchorTSMS int64, anchorKind string) (map[string]any, error)
type ReactionMicrostructureProvider func(asset string, anchorTSMS *int64, preWindowSec, postWindowSec, asOfMS int64) orderbook.HistoryComparison

type ReactionProviders struct {
	Study          ReactionStudyProvider
	Microstructure ReactionMicrostructureProvider
	PreWindowSec   int64
	PostWindowSec  int64
}

func BuildReactionSummaryWithStudies(analysis Analysis, snapshot notice.Snapshot, asOf float64, provider ReactionStudyProvider) (map[string]any, error) {
	return BuildReactionSummaryWithProviders(analysis, snapshot, asOf, ReactionProviders{Study: provider})
}

func BuildReactionSummaryWithProviders(analysis Analysis, snapshot notice.Snapshot, asOf float64, providers ReactionProviders) (map[string]any, error) {
	analysisMap := reactionSummaryInputMap(analysis)
	if snapshot.ContextualItems == nil {
		var err error
		snapshot, err = ContextualizeNoticesWithProviders(analysis, snapshot, asOf, providers)
		if err != nil {
			return nil, err
		}
	}
	// The summary reads only the contextual rows. Avoid serializing the full
	// collector snapshot (including its raw items and source history) again.
	noticeMap := map[string]any{"items": snapshot.ContextualItems}
	return buildReactionSummaryMapsWithProviders(analysisMap, noticeMap, asOf, providers), nil
}

// reactionSummaryInputMap converts only the fields read by the map-oriented
// compatibility layer below. Converting the complete Analysis used to encode
// and decode notices, liquidity, realtime flow, and reference state a second
// time even though none of them participate in the reaction summary. On the
// production 30-day notice set that transient copy can be much larger than the
// final response.
func reactionSummaryInputMap(source Analysis) map[string]any {
	momentum := make([]any, len(source.Momentum))
	for index, row := range source.Momentum {
		momentum[index] = map[string]any{
			"window": row.Window, "share_pct": row.SharePct,
			"status": row.Status, "coverage_sec": row.CoverageSec,
		}
	}

	assets := make([]any, len(source.Assets))
	for index, row := range source.Assets {
		book := row.Microstructure
		assets[index] = map[string]any{
			"asset": row.Asset, "gap_type": row.GapType,
			"domestic_exchanges":           row.DomesticExchanges,
			"domestic_volume_krw":          row.DomesticVolumeKRW,
			"domestic_volume_change_krw":   row.DomesticVolumeChangeKRW,
			"bithumb_volume_change_krw":    row.BithumbVolumeChangeKRW,
			"listed_on_bithumb":            row.ListedOnBithumb,
			"share_change_contribution_pp": row.ShareChangeContributionPP,
			"microstructure": map[string]any{
				"status": book.Status, "bithumb_mid_krw": book.BithumbMidKRW,
				"bithumb_median_spread_bps": book.BithumbMedianSpreadBPS,
				"bithumb_depth_krw":         book.BithumbDepthKRW,
				"bithumb_buy_slippage_bps":  book.BithumbBuySlippageBPS,
				"bithumb_sell_slippage_bps": book.BithumbSellSlippageBPS,
				"bithumb_buy_fill_pct":      book.BithumbBuyFillPct,
				"bithumb_sell_fill_pct":     book.BithumbSellFillPct,
				"global_reference_mid_krw":  book.GlobalReferenceMidKRW,
				"global_depth_krw":          book.GlobalDepthKRW,
			},
		}
	}

	findings := make([]any, len(source.Findings))
	for index, row := range source.Findings {
		findings[index] = map[string]any{
			"kind": row.Kind, "title": row.Title, "detail": row.Detail,
			"asset": row.Asset, "value_krw": row.ValueKRW, "value_pct": row.ValuePct,
		}
	}

	return map[string]any{
		"window": source.Window,
		"context": map[string]any{
			"analysis_context_id":    source.Context.AnalysisContextID,
			"current_interval_start": source.Context.CurrentIntervalStart,
			"orderbook_as_of":        source.Context.OrderBookAsOf,
			"market_scope":           source.Context.MarketScope,
			"selected_window_status": source.Context.SelectedWindowStatus,
		},
		"summary": map[string]any{
			"domestic_volume_krw":       source.Summary.DomesticVolumeKRW,
			"global_context_volume_krw": source.Summary.GlobalContextVolumeKRW,
			"bithumb_share_pct":         source.Summary.BithumbSharePct,
		},
		"change_attribution": map[string]any{
			"previous_share_pct": source.ChangeAttribution.PreviousSharePercent,
			"share_change_pp":    source.ChangeAttribution.ShareChangePP,
		},
		"momentum": momentum,
		"assets":   assets,
		"findings": findings,
	}
}

// ContextualizeNoticesWithProviders attaches the selected analysis window,
// asset measurements, and the strongest measured reaction study to notice
// rows. The raw collector snapshot remains available in Snapshot.Items.
func ContextualizeNoticesWithProviders(analysis Analysis, snapshot notice.Snapshot, asOf float64, providers ReactionProviders) (notice.Snapshot, error) {
	items := notice.AssignEventGroupIDs(snapshot.Items)
	contextual, err := contextualReactionNotices(analysis, items, int64(asOf*1000), providers.Study)
	if err != nil {
		return notice.Snapshot{}, err
	}
	snapshot.ContextualItems = contextual
	return snapshot, nil
}

func contextualReactionNotices(analysis Analysis, items []notice.Item, asOfMS int64, provider ReactionStudyProvider) ([]any, error) {
	assets := make(map[string]AssetAnalysis, len(analysis.Assets))
	for _, asset := range analysis.Assets {
		assets[strings.ToUpper(asset.Asset)] = asset
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		row := reactionNoticeMap(item)
		if consistency := notice.ImageConsistencyIfPresent(item); consistency != nil {
			row["image_consistency"] = reactionImageConsistencyMap(*consistency)
		}
		relevant := reactionNoticeRelevant(row, asOfMS, analysis.WindowSec)
		domestic := reactionDomestic(item.Exchange)
		row["within_selected_window"] = relevant
		contexts := make([]any, 0, len(item.Assets))
		if domestic {
			for _, symbol := range item.Assets {
				asset, found := assets[strings.ToUpper(symbol)]
				if !found {
					continue
				}
				contexts = append(contexts, map[string]any{
					"asset": asset.Asset, "domestic_volume_krw": asset.DomesticVolumeKRW,
					"bithumb_capture_pct":            asset.BithumbCapturePct,
					"upbit_capture_pct":              asset.UpbitCapturePct,
					"current_window_contribution_pp": asset.ShareChangeContributionPP,
					"contribution_scope":             reactionCurrentWindowContributionScope,
					"share_change_contribution_pp":   nil,
					"exclusive_on_bithumb":           asset.ExclusiveOnBithumb,
					"shared_bithumb_upbit":           asset.SharedBithumbUpbit,
					"gap_type":                       asset.GapType,
				})
			}
		}
		row["market_context"] = contexts
		studies := []any{}
		if provider != nil && relevant && domestic {
			for _, anchor := range reactionItemAnchors(item) {
				if anchor.at > asOfMS {
					continue
				}
				for _, asset := range item.Assets {
					study, err := provider(item, strings.ToUpper(asset), anchor.at, anchor.kind)
					if err != nil {
						return nil, err
					}
					if study != nil {
						study["anchor_kind"] = anchor.kind
						study["anchor_evidence"] = anchor.evidence
						studies = append(studies, study)
					}
				}
			}
		}
		candidateCount := 0
		if domestic {
			candidateCount = len(item.Assets) * len(reactionItemAnchors(item))
		}
		row["reaction_candidate_count"] = candidateCount
		row["reaction_eligible_count"] = len(studies)
		selectedStudies := strongestContextualStudies(studies)
		row["reaction_studies"] = selectedStudies
		row["reaction_status"] = "INSUFFICIENT_OBSERVATION"
		completed := false
		for _, value := range selectedStudies {
			study := reactionMap(value)
			status := reactionString(study["status"])
			if status == "LIVE_ESTIMATE" || status == "MATURE" {
				completed = true
			}
			if status := reactionString(study["statistical_status"]); status != "" {
				row["reaction_status"] = status
			}
		}
		row["inflection_status"] = "INSUFFICIENT_OBSERVATION"
		if (relevant && len(contexts) > 0 && analysis.ChangeAttribution.Status == "CONFIRMED") || completed {
			row["inflection_status"] = "OBSERVED_NEAR_NOTICE_OR_SCHEDULE"
		}
		result = append(result, row)
	}
	return result, nil
}

func reactionImageConsistencyMap(value notice.ImageConsistency) map[string]any {
	result := map[string]any{
		"status": value.Status, "semantic_types": value.SemanticTypes,
		"matched_signals": value.MatchedSignals, "conflict_signals": value.ConflictSignals,
		"reason": value.Reason,
	}
	if len(value.UnverifiedSignals) > 0 {
		result["unverified_signals"] = value.UnverifiedSignals
	}
	return result
}

func reactionNoticeMap(item notice.Item) map[string]any {
	row := map[string]any{
		"exchange": item.Exchange, "notice_id": item.NoticeID, "title": item.Title,
		"url": item.URL, "category": item.Category, "published_ts_ms": item.PublishedTSMS,
		"detected_ts_ms": item.DetectedTSMS, "timestamp_precision": item.TimestampPrecision,
		"assets": item.Assets, "actions": item.Actions, "source_type": item.SourceType, "updated_ts_ms": item.UpdatedTSMS,
		"body_excerpt": item.BodyExcerpt, "summary": item.Summary,
		"schedules": reactionScheduleMaps(item.Schedules), "risk_tags": item.RiskTags,
		"image_urls": item.ImageURLs, "image_text": item.ImageText,
		"image_analysis": item.ImageAnalysis, "content_source": item.ContentSource,
		"analysis_status": item.AnalysisStatus, "analysis_confidence": item.AnalysisConfidence,
		"content_hash": item.ContentHash, "detail_checked_ts_ms": item.DetailCheckedTSMS,
		"event_group_id": item.EventGroupID,
	}
	for key, value := range map[string]string{
		"original_language": item.OriginalLanguage, "original_title": item.OriginalTitle,
		"original_body_text": item.OriginalBodyText, "translated_title_ko": item.TranslatedTitleKO,
		"translated_body_text_ko": item.TranslatedBodyKO, "translation_status": item.TranslationStatus,
		"importance": item.Importance, "analysis_provider": item.AnalysisProvider,
		"analysis_model": item.AnalysisModel, "reasoning_effort": item.ReasoningEffort,
		"escalation_reason": item.EscalationReason, "analysis_policy_version": item.AnalysisPolicy,
	} {
		if value != "" {
			row[key] = value
		}
	}
	return row
}

func reactionScheduleMaps(values []notice.Schedule) any {
	if values == nil {
		return nil
	}
	result := make([]any, len(values))
	for index, value := range values {
		row := map[string]any{
			"kind": value.Kind, "start_ts_ms": value.StartTSMS, "end_ts_ms": value.EndTSMS,
		}
		if value.Timezone != "" {
			row["timezone"] = value.Timezone
		}
		if value.Precision != "" {
			row["precision"] = value.Precision
		}
		if value.Evidence != "" {
			row["evidence"] = value.Evidence
		}
		result[index] = row
	}
	return result
}

func strongestContextualStudies(values []any) []any {
	if len(values) == 0 {
		return []any{}
	}
	latestAnchor := int64(0)
	for _, value := range values {
		if anchor := reactionTimestamp(reactionMap(value)["anchor_ts_ms"]); anchor != nil && *anchor > latestAnchor {
			latestAnchor = *anchor
		}
	}
	candidates := values
	if latestAnchor > 0 {
		anchored := make([]any, 0, len(values))
		for _, value := range values {
			if anchor := reactionTimestamp(reactionMap(value)["anchor_ts_ms"]); anchor != nil && *anchor == latestAnchor {
				anchored = append(anchored, value)
			}
		}
		if len(anchored) > 0 {
			candidates = anchored
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := reactionMap(candidates[i]), reactionMap(candidates[j])
		leftStatus, rightStatus := reactionString(left["status"]), reactionString(right["status"])
		leftRank := map[string]int{"MATURE": 2, "LIVE_ESTIMATE": 1}[leftStatus]
		rightRank := map[string]int{"MATURE": 2, "LIVE_ESTIMATE": 1}[rightStatus]
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if leftConfidence, rightConfidence := reactionValue(left["evidence_confidence_pct"]), reactionValue(right["evidence_confidence_pct"]); leftConfidence != rightConfidence {
			return leftConfidence > rightConfidence
		}
		return reactionValue(left["observed_post_sec"]) > reactionValue(right["observed_post_sec"])
	})
	return []any{candidates[0]}
}

type reactionAnchor struct {
	at             int64
	kind, evidence string
}

func reactionItemAnchors(item notice.Item) []reactionAnchor {
	result := []reactionAnchor{}
	if strings.EqualFold(item.TimestampPrecision, "SECOND") && item.PublishedTSMS > 0 {
		result = append(result, reactionAnchor{item.PublishedTSMS, "NOTICE_PUBLISHED", item.Title})
	}
	for _, schedule := range item.Schedules {
		if schedule.StartTSMS > 0 {
			result = append(result, reactionAnchor{schedule.StartTSMS, schedule.Kind + "_START", schedule.Evidence})
		}
		if schedule.EndTSMS > 0 {
			result = append(result, reactionAnchor{schedule.EndTSMS, schedule.Kind + "_END", schedule.Evidence})
		}
	}
	if strings.EqualFold(item.TimestampPrecision, "DETECTED") && len(result) == 0 && item.DetectedTSMS > 0 {
		result = append(result, reactionAnchor{item.DetectedTSMS, "NOTICE_DETECTED", item.Title})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].at < result[j].at })
	return result
}

func buildReactionSummaryMaps(analysis, notices map[string]any, asOf float64) map[string]any {
	return buildReactionSummaryMapsWithProviders(analysis, notices, asOf, ReactionProviders{})
}

func buildReactionSummaryMapsWithMicrostructure(analysis, notices map[string]any, asOf float64, provider ReactionMicrostructureProvider) map[string]any {
	return buildReactionSummaryMapsWithProviders(analysis, notices, asOf, ReactionProviders{Microstructure: provider})
}

func buildReactionSummaryMapsWithProviders(analysis, notices map[string]any, asOf float64, providers ReactionProviders) map[string]any {
	context := reactionMap(analysis["context"])
	windowStart := int64(0)
	if value := reactionNumber(context["current_interval_start"]); value != nil {
		windowStart = int64(*value * 1000)
	}
	microstructureAsOf := int64(asOf * 1000)
	if value := reactionNumber(context["orderbook_as_of"]); value != nil && *value > 0 {
		microstructureAsOf = int64(*value * 1000)
	}
	preWindow, postWindow := providers.PreWindowSec, providers.PostWindowSec
	if preWindow < 1 {
		preWindow = 1800
	}
	if postWindow < 1 {
		postWindow = 1800
	}
	assetRows := make(map[string]map[string]any)
	for _, value := range reactionSlice(analysis["assets"]) {
		row := reactionMap(value)
		assetRows[strings.ToUpper(reactionString(row["asset"]))] = row
	}
	events := make([]map[string]any, 0)
	for _, value := range reactionSlice(notices["items"]) {
		item := reactionMap(value)
		if !reactionDomestic(reactionString(item["exchange"])) || !reactionBool(item["within_selected_window"]) {
			continue
		}
		events = append(events, reactionEvent(item, assetRows, windowStart, int64(asOf*1000), microstructureAsOf, preWindow, postWindow, providers.Microstructure))
	}
	sort.SliceStable(events, func(i, j int) bool { return reactionEventLess(events[i], events[j]) })
	events = mergeReactionEvents(events)

	linkedByAsset := make(map[string]map[string]any)
	for _, event := range events {
		for _, value := range reactionSlice(event["linked_assets"]) {
			asset := reactionMap(value)
			name := reactionString(asset["asset"])
			previous, found := linkedByAsset[name]
			if !found || (reactionNumber(previous["current_window_contribution_pp"]) == nil && reactionNumber(asset["current_window_contribution_pp"]) != nil) {
				linkedByAsset[name] = asset
			}
		}
	}
	linked := make([]map[string]any, 0, len(linkedByAsset))
	for _, asset := range linkedByAsset {
		linked = append(linked, asset)
	}
	sort.Slice(linked, func(i, j int) bool { return reactionString(linked[i]["asset"]) < reactionString(linked[j]["asset"]) })
	contribution, positive, negative, positiveCount, negativeCount := reactionCurrentWindowContributionTotals(linked)
	summary := reactionMap(analysis["summary"])
	change := reactionMap(analysis["change_attribution"])
	domesticVolume := reactionNumber(summary["domestic_volume_krw"])
	currentShare := reactionNumber(summary["bithumb_share_pct"])
	currentAvailable := domesticVolume != nil && *domesticVolume > 0 && currentShare != nil
	if !currentAvailable {
		currentShare = nil
	}
	linkedVolume := reactionLinkedVolume(linked)
	var coverage *float64
	if domesticVolume != nil && *domesticVolume != 0 && linkedVolume != nil {
		coverage = reactionFloat(*linkedVolume / *domesticVolume * 100)
	}
	origin := unresolvedReactionOrigin()
	if len(events) > 0 {
		origin = reactionMap(events[0]["origin"])
	}
	result := map[string]any{
		"analysis_context_id": context["analysis_context_id"], "as_of": asOf,
		"selected_window": reactionString(analysis["window"]),
		"market_scope":    reactionDefaultString(context["market_scope"], "KRW"),
		"market_share": map[string]any{
			"current_pct": currentShare, "current_available": currentAvailable,
			"previous_pct": reactionNumber(change["previous_share_pct"]),
			"change_pp":    reactionNumber(change["share_change_pp"]),
			"status":       reactionDefaultString(context["selected_window_status"], "UNAVAILABLE"),
		},
		"origin": origin,
		"notice_summary": map[string]any{
			"event_count": len(events), "linked_asset_count": len(linked),
			"linked_asset_current_window_contribution_pp":      contribution,
			"positive_current_window_contribution_pp":          positive,
			"negative_current_window_contribution_pp":          negative,
			"positive_current_window_contribution_asset_count": positiveCount,
			"negative_current_window_contribution_asset_count": negativeCount,
			"contribution_scope":                               reactionCurrentWindowContributionScope,
			// Deprecated impact fields are intentionally null: these values are
			// current-window attribution, not a measured causal notice impact.
			"linked_asset_impact_pp": nil, "positive_impact_pp": nil,
			"negative_impact_pp": nil, "positive_impact_asset_count": nil,
			"negative_impact_asset_count": nil, "domestic_volume_coverage_pct": coverage,
		},
		"top_notice": nil, "largest_exchange_change": largestReactionExchangeChange(events),
		"events": reactionMapSliceAny(events),
	}
	if len(events) > 0 {
		result["top_notice"] = events[0]
		result["no_notice_context"] = map[string]any{
			"status":  "NOTICE_AVAILABLE",
			"message": "공지와 겹치는 자산의 현재 선택 구간 점유율 기여는 별도 계산값이며, 공지 전후 반응이나 인과 기여도로 해석하지 않습니다.",
		}
	} else {
		result["no_notice_context"] = noReactionNoticeContext(analysis)
	}
	return result
}

func reactionEvent(item map[string]any, assets map[string]map[string]any, windowStart, asOfMS, microstructureAsOfMS, preWindow, postWindow int64, provider ReactionMicrostructureProvider) map[string]any {
	linked := reactionLinkedAssets(item, assets)
	eventAt, basis := reactionEventAnchor(item, windowStart, asOfMS)
	study := strongestReactionStudy(item, eventAt)
	originType, domesticAt, globalAt := reactionOrigin(study, eventAt)
	binanceAt := reactionBinanceStart(study)
	evidence, reasons := reactionEvidence(linked, study)
	state, label := reactionState(item, study)
	contribution, positive, negative, _, _ := reactionCurrentWindowContributionTotals(linked)
	exchangeReactions := reactionExchangeRows(study)
	domesticBefore, domesticAfter, comparisonComplete := reactionDomesticComparison(exchangeReactions, study)
	var lead *float64
	if domesticAt != nil && globalAt != nil {
		lead = reactionFloat(float64(*domesticAt-*globalAt) / 1000)
	}
	timeline := reactionTimeline(item, domesticAt, globalAt)
	groupID := reactionString(item["event_group_id"])
	if groupID == "" {
		groupID = reactionString(item["exchange"]) + ":" + reactionString(item["notice_id"])
	}
	return map[string]any{
		"event_id":       reactionString(item["exchange"]) + ":" + reactionString(item["notice_id"]),
		"event_group_id": groupID, "exchange": reactionString(item["exchange"]),
		"event_type": reactionDefaultString(item["category"], "OTHER"),
		"title":      reactionString(item["title"]), "source_url": reactionString(item["url"]),
		"published_at_ms":     reactionTimestamp(item["published_ts_ms"]),
		"timestamp_precision": reactionDefaultString(item["timestamp_precision"], "SECOND"),
		"collected_at_ms":     reactionTimestamp(item["detected_ts_ms"]),
		"effective_at_ms":     reactionFirstSchedule(item), "ended_at_ms": reactionLastSchedule(item),
		"affected_assets": reactionSlice(item["assets"]), "linked_assets": reactionMapSliceAny(linked),
		"linked_asset_current_window_contribution_pp": contribution,
		"positive_current_window_contribution_pp":     positive,
		"negative_current_window_contribution_pp":     negative,
		"contribution_scope":                          reactionCurrentWindowContributionScope,
		"linked_asset_impact_pp":                      nil, "positive_impact_pp": nil, "negative_impact_pp": nil,
		"domestic_volume_before_krw": reactionIf(comparisonComplete, domesticBefore),
		"domestic_volume_after_krw":  reactionIf(comparisonComplete, domesticAfter),
		"domestic_volume_change_pct": reactionPercent(reactionIf(comparisonComplete, domesticBefore), reactionIf(comparisonComplete, domesticAfter)),
		"bithumb_share_change_pp":    reactionStudyNumber(study, "bithumb_share_change_pp"),
		"origin": map[string]any{
			"type": originType, "label": reactionOriginLabels[originType], "event_time_ms": eventAt,
			"event_time_basis": basis, "notice_time_ms": reactionPrecisePublished(item),
			"effective_time_ms": reactionFirstSchedule(item), "domestic_reaction_time_ms": domesticAt,
			"global_reaction_time_ms": globalAt,
			// Deprecated compatibility alias. The global reference is Binance Spot,
			// so both fields intentionally carry the same evidence.
			"binance_reaction_time_ms": binanceAt, "lead_seconds": lead, "evidence_level": evidence,
		},
		"reaction_state": state, "reaction_label": label, "maturity": reactionMaturity(study),
		"evidence_level": evidence, "evidence_reasons": reasons, "reaction_study": study,
		"timeline": reactionMapSliceAny(timeline), "exchange_reactions": reactionMapSliceAny(exchangeReactions),
		"global_context":  reactionGlobalContext(study),
		"binance_context": reactionBinanceContext(study), "microstructure": reactionMicrostructure(item, assets, eventAt, microstructureAsOfMS, preWindow, postWindow, provider),
		"data_quality": reactionDataQuality(study, comparisonComplete),
	}
}

func reactionLinkedAssets(item map[string]any, assets map[string]map[string]any) []map[string]any {
	result := make([]map[string]any, 0)
	seen := make(map[string]bool)
	for _, value := range reactionSlice(item["market_context"]) {
		context := reactionMap(value)
		name := strings.ToUpper(reactionString(context["asset"]))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		asset := assets[name]
		gapType := reactionDefaultString(asset["gap_type"], reactionDefaultString(context["gap_type"], "NO_RECENT_DOMESTIC_VOLUME"))
		volume := reactionNumber(context["domestic_volume_krw"])
		exchanges := reactionSlice(asset["domestic_exchanges"])
		if gapType == "NO_RECENT_DOMESTIC_VOLUME" && len(exchanges) == 0 {
			volume = nil
		}
		result = append(result, map[string]any{
			"asset":                          name,
			"current_window_contribution_pp": reactionNumber(context["current_window_contribution_pp"]),
			"contribution_scope":             reactionCurrentWindowContributionScope,
			"impact_pp":                      nil,
			"domestic_volume_krw":            volume, "domestic_volume_change_krw": reactionNumber(asset["domestic_volume_change_krw"]),
			"bithumb_volume_change_krw": reactionNumber(asset["bithumb_volume_change_krw"]),
			"listed_on_bithumb":         reactionBool(asset["listed_on_bithumb"]), "gap_type": gapType,
			"domestic_exchanges": exchanges,
		})
	}
	return result
}

func reactionEventAnchor(item map[string]any, windowStart, asOfMS int64) (*int64, string) {
	type candidate struct {
		at    int64
		basis string
	}
	candidates := make([]candidate, 0)
	published := reactionPrecisePublished(item)
	if published != nil {
		candidates = append(candidates, candidate{*published, "NOTICE"})
	}
	for _, value := range reactionSlice(item["schedules"]) {
		schedule := reactionMap(value)
		if at := reactionTimestamp(schedule["start_ts_ms"]); at != nil {
			candidates = append(candidates, candidate{*at, "EFFECTIVE"})
		}
		if at := reactionTimestamp(schedule["end_ts_ms"]); at != nil {
			candidates = append(candidates, candidate{*at, "SCHEDULE"})
		}
	}
	if strings.EqualFold(reactionDefaultString(item["timestamp_precision"], "SECOND"), "DETECTED") && len(candidates) == 0 {
		if at := reactionTimestamp(item["detected_ts_ms"]); at != nil {
			candidates = append(candidates, candidate{*at, "DETECTED"})
		}
	}
	selected := candidate{}
	found := false
	for _, value := range candidates {
		if windowStart > 0 && value.at >= windowStart && value.at <= asOfMS && (!found || value.at > selected.at) {
			selected, found = value, true
		}
	}
	if !found && windowStart > 0 {
		for _, value := range candidates {
			if (value.basis == "EFFECTIVE" || value.basis == "SCHEDULE") && value.at <= asOfMS && (!found || value.at > selected.at) {
				selected, found = value, true
			}
		}
	}
	if !found && published != nil {
		return reactionInt64(*published), "NOTICE"
	}
	if !found {
		for _, value := range reactionSlice(item["schedules"]) {
			if at := reactionTimestamp(reactionMap(value)["start_ts_ms"]); at != nil && (!found || *at < selected.at) {
				selected, found = candidate{*at, "EFFECTIVE"}, true
			}
		}
	}
	if !found && strings.EqualFold(reactionDefaultString(item["timestamp_precision"], "SECOND"), "DETECTED") {
		if at := reactionTimestamp(item["detected_ts_ms"]); at != nil {
			return at, "DETECTED"
		}
	}
	if !found {
		return nil, "UNAVAILABLE"
	}
	return reactionInt64(selected.at), selected.basis
}

func strongestReactionStudy(item map[string]any, eventAt *int64) map[string]any {
	studies := make([]map[string]any, 0)
	for _, value := range reactionSlice(item["reaction_studies"]) {
		study := reactionMap(value)
		status := reactionString(study["status"])
		if status == "LIVE_ESTIMATE" || status == "MATURE" || incompleteReactionStatus(status) {
			studies = append(studies, study)
		}
	}
	if eventAt != nil {
		anchored := make([]map[string]any, 0, len(studies))
		hasExplicitAnchor := false
		for _, study := range studies {
			if at := reactionTimestamp(study["anchor_ts_ms"]); at != nil {
				hasExplicitAnchor = true
				if *at == *eventAt {
					anchored = append(anchored, study)
				}
			}
		}
		if len(anchored) > 0 {
			studies = anchored
		} else if hasExplicitAnchor {
			unanchored := make([]map[string]any, 0, len(studies))
			for _, study := range studies {
				if reactionTimestamp(study["anchor_ts_ms"]) == nil {
					unanchored = append(unanchored, study)
				}
			}
			studies = unanchored
		}
	}
	if len(studies) == 0 {
		return nil
	}
	sort.SliceStable(studies, func(i, j int) bool {
		leftPartial, rightPartial := incompleteReactionStatus(reactionString(studies[i]["status"])), incompleteReactionStatus(reactionString(studies[j]["status"]))
		if leftPartial != rightPartial {
			return !leftPartial
		}
		left, right := reactionValue(studies[i]["evidence_confidence_pct"]), reactionValue(studies[j]["evidence_confidence_pct"])
		if left != right {
			return left > right
		}
		return reactionValue(studies[i]["observed_post_sec"]) > reactionValue(studies[j]["observed_post_sec"])
	})
	return studies[0]
}

func reactionOrigin(study map[string]any, eventAt *int64) (string, *int64, *int64) {
	if study == nil || incompleteReactionStatus(reactionString(study["status"])) {
		return "UNRESOLVED", nil, nil
	}
	domestic := reactionTimestamp(study["domestic_reaction_start_ts_ms"])
	global := reactionTimestamp(study["global_reaction_start_ts_ms"])
	if domestic == nil {
		return "UNRESOLVED", nil, global
	}
	if global != nil {
		delta := *global - *domestic
		if delta >= -5000 && delta <= 5000 {
			return "MIXED", domestic, global
		}
		if *global < *domestic {
			return "GLOBAL_FIRST", domestic, global
		}
	}
	if eventAt != nil && *eventAt <= *domestic {
		return "DOMESTIC_NOTICE_FIRST", domestic, global
	}
	return "DOMESTIC_MARKET_FIRST", domestic, global
}

func reactionEvidence(linked []map[string]any, study map[string]any) (string, []string) {
	reasons := []string{}
	if len(linked) > 0 {
		reasons = append(reasons, "공지 대상 자산 매핑 완료")
	} else {
		reasons = append(reasons, "공지 대상 자산 또는 점유율 연결값 없음")
	}
	if study == nil {
		reasons = append(reasons, "공지 전후 반응 구간 미확보")
		if len(linked) > 0 {
			return "WEAK", reasons
		}
		return "UNAVAILABLE", reasons
	}
	if incompleteReactionStatus(reactionString(study["status"])) {
		reasons = append(reasons, "공지 전후 일부 거래금액 관측 확보")
		if reactionString(study["status"]) == incompleteFXReactionStudy {
			reasons = append(reasons, "원화 환산이 끝나지 않은 체결이 있어 반응 판단 보류")
		} else {
			reasons = append(reasons, "연속 관측 공백으로 부분 결과만 사용")
		}
		if reactionBool(study["contiguous_market_coverage"]) {
			reasons = append(reasons, "공지 전후 연속 관측 구간 확인")
		}
		return "WEAK", reasons
	}
	reasons = append(reasons, "공지 전후 거래금액 관측 완료")
	if reactionBool(study["contiguous_market_coverage"]) {
		reasons = append(reasons, "공지 전후 연속 관측 구간 확인")
		if reactionTimestamp(study["domestic_reaction_start_ts_ms"]) != nil || reactionTimestamp(study["global_reaction_start_ts_ms"]) != nil {
			return "STRONG", reasons
		}
	}
	return "MODERATE", reasons
}

func reactionState(item, study map[string]any) (string, string) {
	if study == nil {
		status := strings.ToUpper(reactionString(item["reaction_status"]))
		if status == "NO_CLEAR_SHIFT" || status == "NO_ROBUST_SHIFT" {
			return "NO_CLEAR_REACTION", "뚜렷한 반응 없음"
		}
		return "UNAVAILABLE", "판단 보류"
	}
	if incompleteReactionStatus(reactionString(study["status"])) {
		return "PARTIAL_OBSERVATION", "자료 부족"
	}
	observed := reactionValue(study["observed_post_sec"])
	if reactionString(study["statistical_status"]) == "NO_ROBUST_SHIFT" && observed >= 60 {
		return "NO_CLEAR_REACTION", "뚜렷한 반응 없음"
	}
	if observed < 60 {
		return "JUST_STARTED", "방금 시작"
	}
	if observed < reactionDefaultValue(study["post_window_sec"], 1800) {
		return "EXPANDING", "확대 중"
	}
	if multiplier := reactionNumber(study["domestic_volume_multiplier"]); multiplier != nil && math.Abs(*multiplier-1) >= .2 {
		return "SUSTAINING", "유지 중"
	}
	return "WEAKENING", "약화 중"
}

func reactionExchangeRows(study map[string]any) []map[string]any {
	if study == nil {
		return []map[string]any{}
	}
	raw := reactionMap(study["exchange_reactions"])
	rows := make([]map[string]any, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		values := reactionMap(raw[string(exchange)])
		before, after := reactionNumber(values["pre_volume_krw"]), reactionNumber(values["post_volume_krw"])
		if reactionValue(values["pre_observed_seconds"]) == 0 && values["pre_observed_seconds"] != nil {
			before = nil
		}
		if reactionValue(values["post_observed_seconds"]) == 0 && values["post_observed_seconds"] != nil {
			after = nil
		}
		beforeCount, afterCount := reactionNonnegativeInt(values["pre_trade_count"]), reactionNonnegativeInt(values["post_trade_count"])
		if before == nil {
			beforeCount = nil
		}
		if after == nil {
			afterCount = nil
		}
		status := reactionString(values["data_status"])
		if before == nil || after == nil {
			status = "UNAVAILABLE"
		} else if status == "OBSERVED_ZERO" {
			status = "OBSERVED_ZERO"
		} else if status == "" || status == "OBSERVED" {
			// Empty is accepted for legacy persisted study rows, which predate
			// the explicit data_status field and were already observed.
			status = "OBSERVED"
		}
		var change *float64
		if status == "OBSERVED" || status == "OBSERVED_ZERO" {
			change = reactionPercent(before, after)
		}
		row := map[string]any{
			"exchange": string(exchange), "before_volume_krw": before, "after_volume_krw": after,
			"volume_change_pct": change, "before_trade_count": beforeCount,
			"after_trade_count": afterCount, "data_status": status,
		}
		if value, exists := values["pre_unresolved_fx_count"]; exists {
			row["before_unresolved_fx_count"] = reactionNonnegativeInt(value)
		}
		if value, exists := values["post_unresolved_fx_count"]; exists {
			row["after_unresolved_fx_count"] = reactionNonnegativeInt(value)
		}
		rows = append(rows, row)
	}
	return rows
}

func reactionDomesticComparison(rows []map[string]any, study map[string]any) (*float64, *float64, bool) {
	before, after, observed := 0.0, 0.0, 0
	for _, row := range rows {
		status := reactionString(row["data_status"])
		if status != "OBSERVED" && status != "OBSERVED_ZERO" {
			continue
		}
		before += reactionValue(row["before_volume_krw"])
		after += reactionValue(row["after_volume_krw"])
		observed++
	}
	complete := study != nil && !incompleteReactionStatus(reactionString(study["status"])) && reactionBool(study["contiguous_market_coverage"]) && observed == len(domain.DomesticExchanges)
	return reactionFloat(before), reactionFloat(after), complete
}

func reactionBinanceContext(study map[string]any) map[string]any {
	note := "해외 실시간 흐름과 공지의 연결 분석은 제공하지 않습니다. 바이낸스 24시간 자료는 참고값과 코인별 가격 차이 표시에만 사용합니다."
	result := map[string]any{
		"status": "UNAVAILABLE", "price_change_pct": nil, "volume_multiplier": nil, "volume_change_pct": nil,
		"before_volume_krw": nil, "after_volume_krw": nil, "reaction_start_ts_ms": nil,
		"before_price_krw": nil, "after_price_krw": nil, "before_depth_krw": nil,
		"after_depth_krw": nil, "depth_change_pct": nil, "current_price_krw": nil,
		"current_depth_krw": nil, "microstructure_status": "DISABLED", "asset_snapshots": []any{}, "note": note,
	}
	if study == nil {
		return result
	}
	rows := reactionMap(study["exchange_reactions"])
	binance := reactionMap(rows[string(domain.ExchangeBinance)])
	before, after := reactionNumber(binance["pre_volume_krw"]), reactionNumber(binance["post_volume_krw"])
	legacyAggregate := len(binance) == 0 && (study["global_pre_volume_krw"] != nil || study["global_post_volume_krw"] != nil)
	if legacyAggregate {
		// The persisted Python contract named its Binance-only values "global_*".
		// Preserve those old rows while
		// refusing that fallback for new studies, which always contain per-venue
		// exchange_reactions.
		before, after = reactionNumber(study["global_pre_volume_krw"]), reactionNumber(study["global_post_volume_krw"])
		binance = map[string]any{
			"pre_observed_seconds":  study["global_pre_observed_seconds"],
			"post_observed_seconds": study["global_post_observed_seconds"],
			"volume_multiplier":     study["global_volume_multiplier"],
			"data_status":           "OBSERVED",
		}
	}
	if binance["pre_observed_seconds"] != nil && reactionValue(binance["pre_observed_seconds"]) == 0 {
		before = nil
	}
	if binance["post_observed_seconds"] != nil && reactionValue(binance["post_observed_seconds"]) == 0 {
		after = nil
	}
	dataStatus := reactionString(binance["data_status"])
	verified := legacyAggregate || reactionString(study["global_signal_status"]) == "VERIFIED"
	if verified && before != nil && after != nil && (dataStatus == "OBSERVED" || dataStatus == "OBSERVED_ZERO" || dataStatus == "") {
		result["status"] = "OBSERVED"
	} else if before != nil || after != nil {
		result["status"] = "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY"
	}
	result["before_volume_krw"], result["after_volume_krw"] = before, after
	if result["status"] == "OBSERVED" {
		result["volume_multiplier"] = reactionNumber(binance["volume_multiplier"])
		result["volume_change_pct"] = reactionPercent(before, after)
		result["reaction_start_ts_ms"] = reactionTimestamp(study["binance_reaction_start_ts_ms"])
		if legacyAggregate {
			result["reaction_start_ts_ms"] = reactionTimestamp(study["global_reaction_start_ts_ms"])
		}
	}
	return result
}

func reactionBinanceStart(study map[string]any) *int64 {
	if study == nil {
		return nil
	}
	if value := reactionTimestamp(study["binance_reaction_start_ts_ms"]); value != nil {
		return value
	}
	// Legacy persisted studies predate per-exchange rows and used the global_*
	// fields for Binance alone.
	rows := reactionMap(study["exchange_reactions"])
	if len(reactionMap(rows[string(domain.ExchangeBinance)])) == 0 &&
		(study["global_pre_volume_krw"] != nil || study["global_post_volume_krw"] != nil) {
		return reactionTimestamp(study["global_reaction_start_ts_ms"])
	}
	return nil
}

func reactionGlobalContext(study map[string]any) map[string]any {
	result := map[string]any{
		"status": "UNAVAILABLE", "before_volume_krw": nil, "after_volume_krw": nil,
		"volume_multiplier": nil, "volume_change_pct": nil, "reaction_start_ts_ms": nil,
		"included_exchanges": []any{},
	}
	if study == nil {
		return result
	}
	binance := reactionBinanceContext(study)
	before, after := reactionNumber(binance["before_volume_krw"]), reactionNumber(binance["after_volume_krw"])
	if before == nil && after == nil {
		return result
	}
	result["status"] = binance["status"]
	result["before_volume_krw"], result["after_volume_krw"] = before, after
	result["volume_multiplier"] = binance["volume_multiplier"]
	result["volume_change_pct"] = binance["volume_change_pct"]
	result["reaction_start_ts_ms"] = binance["reaction_start_ts_ms"]
	result["included_exchanges"] = []any{string(domain.ExchangeBinance)}
	return result
}

func reactionMicrostructure(item map[string]any, assets map[string]map[string]any, eventAt *int64, asOfMS, preWindow, postWindow int64, provider ReactionMicrostructureProvider) []any {
	result := []any{}
	for _, linked := range reactionLinkedAssets(item, assets) {
		name := reactionString(linked["asset"])
		book := reactionMap(assets[name]["microstructure"])
		mid, depth := reactionNumber(book["bithumb_mid_krw"]), reactionNumber(book["bithumb_depth_krw"])
		if depth != nil && *depth == 0 && mid == nil {
			depth = nil
		}
		comparison := emptyReactionComparison()
		if provider != nil {
			comparison = reactionHistoryComparisonMap(provider(name, eventAt, preWindow, postWindow, asOfMS))
		}
		comparisonStatus := reactionDefaultString(comparison["status"], "CURRENT_SNAPSHOT_ONLY")
		note := "호가·슬리피지는 현재 값이며 공지 전후 이력 비교값이 아닙니다."
		if comparisonStatus != "CURRENT_SNAPSHOT_ONLY" {
			note = reactionString(comparison["note"])
		}
		result = append(result, map[string]any{
			"asset": name, "as_of_ms": asOfMS, "status": reactionDefaultString(book["status"], "UNAVAILABLE"),
			"bithumb_mid_krw": mid, "bithumb_spread_bps": reactionNumber(book["bithumb_median_spread_bps"]),
			"bithumb_depth_krw": depth, "bithumb_buy_slippage_bps": reactionNumber(book["bithumb_buy_slippage_bps"]),
			"bithumb_sell_slippage_bps": reactionNumber(book["bithumb_sell_slippage_bps"]),
			"bithumb_buy_fill_pct":      reactionNumber(book["bithumb_buy_fill_pct"]), "bithumb_sell_fill_pct": reactionNumber(book["bithumb_sell_fill_pct"]),
			"global_reference_mid_krw": reactionNumber(book["global_reference_mid_krw"]), "global_depth_krw": reactionNumber(book["global_depth_krw"]),
			"comparison_status": comparisonStatus, "comparison": comparison, "note": note,
		})
	}
	return result
}

func reactionHistoryComparisonMap(value orderbook.HistoryComparison) map[string]any {
	return map[string]any{
		"status": value.Status, "pre_point_count": float64(value.PrePointCount),
		"post_point_count":       float64(value.PostPointCount),
		"pre_observed_start_ms":  reactionJSONInt(value.PreObservedStartMS),
		"pre_observed_end_ms":    reactionJSONInt(value.PreObservedEndMS),
		"post_observed_start_ms": reactionJSONInt(value.PostObservedStartMS),
		"post_observed_end_ms":   reactionJSONInt(value.PostObservedEndMS),
		"before":                 reactionNullableNumberMap(value.Before),
		"after":                  reactionNullableNumberMap(value.After),
		"change_pct":             reactionNullableNumberMap(value.ChangePercent), "note": value.Note,
	}
}

func reactionJSONInt(value *int64) any {
	if value == nil {
		return nil
	}
	return float64(*value)
}

func reactionNullableNumberMap(source map[string]*float64) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		if value == nil {
			result[key] = nil
		} else {
			result[key] = *value
		}
	}
	return result
}

func emptyReactionComparison() map[string]any {
	fields := []string{"bithumb_mid_krw", "bithumb_spread_bps", "bithumb_depth_krw", "bithumb_buy_slippage_bps", "bithumb_sell_slippage_bps", "bithumb_buy_fill_pct", "bithumb_sell_fill_pct", "global_reference_mid_krw", "global_depth_krw"}
	empty := func() map[string]any {
		row := make(map[string]any, len(fields))
		for _, field := range fields {
			row[field] = nil
		}
		return row
	}
	return map[string]any{"status": "CURRENT_SNAPSHOT_ONLY", "pre_point_count": 0, "post_point_count": 0, "pre_observed_start_ms": nil, "pre_observed_end_ms": nil, "post_observed_start_ms": nil, "post_observed_end_ms": nil, "before": empty(), "after": empty(), "change_pct": empty(), "note": "공지 전후 호가 자료가 없어 현재 값만 표시합니다."}
}

func reactionTimeline(item map[string]any, domestic, global *int64) []map[string]any {
	published := reactionPrecisePublished(item)
	rows := []map[string]any{{"kind": "NOTICE", "label": "공지 게시", "at_ms": published, "status": reactionObserved(published)}}
	seen := make(map[string]bool)
	for _, value := range reactionSlice(item["schedules"]) {
		schedule := reactionMap(value)
		for _, point := range []struct{ field, kind, label string }{{"start_ts_ms", "EFFECTIVE", "적용 시각"}, {"end_ts_ms", "ENDED", "종료·재개 시각"}} {
			at := reactionTimestamp(schedule[point.field])
			if at == nil {
				continue
			}
			key := point.kind + strconv.FormatInt(*at, 10)
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, map[string]any{"kind": point.kind, "label": point.label, "at_ms": at, "status": "OBSERVED"})
		}
	}
	rows = append(rows, map[string]any{"kind": "DOMESTIC_REACTION", "label": "국내 반응 시작", "at_ms": domestic, "status": reactionObserved(domestic)}, map[string]any{"kind": "GLOBAL_REACTION", "label": "Binance Spot 보조 반응 시각", "at_ms": global, "status": reactionObserved(global)})
	return rows
}

func mergeReactionEvents(events []map[string]any) []map[string]any {
	grouped, order := make(map[string]map[string]any), []string{}
	for _, event := range events {
		group := reactionDefaultString(event["event_group_id"], reactionString(event["event_id"]))
		current, found := grouped[group]
		if !found {
			current = event
			current["related_notice_count"] = 1
			current["related_event_ids"] = []any{event["event_id"]}
			grouped[group] = current
			order = append(order, group)
			continue
		}
		current["related_notice_count"] = reactionInt(current["related_notice_count"]) + 1
		current["related_event_ids"] = append(reactionSlice(current["related_event_ids"]), event["event_id"])
		current["affected_assets"] = reactionUnique(append(reactionSlice(current["affected_assets"]), reactionSlice(event["affected_assets"])...))
		linked := make(map[string]map[string]any)
		for _, source := range []any{current["linked_assets"], event["linked_assets"]} {
			for _, value := range reactionSlice(source) {
				row := reactionMap(value)
				name := reactionString(row["asset"])
				previous := linked[name]
				if previous == nil || (reactionNumber(previous["current_window_contribution_pp"]) == nil && reactionNumber(row["current_window_contribution_pp"]) != nil) {
					linked[name] = row
				}
			}
		}
		rows := make([]map[string]any, 0, len(linked))
		for _, row := range linked {
			rows = append(rows, row)
		}
		current["linked_assets"] = reactionMapSliceAny(rows)
		contribution, positive, negative, _, _ := reactionCurrentWindowContributionTotals(rows)
		current["linked_asset_current_window_contribution_pp"] = contribution
		current["positive_current_window_contribution_pp"] = positive
		current["negative_current_window_contribution_pp"] = negative
		current["contribution_scope"] = reactionCurrentWindowContributionScope
		current["linked_asset_impact_pp"], current["positive_impact_pp"], current["negative_impact_pp"] = nil, nil, nil
		current["effective_at_ms"] = reactionMinTimestamp(current["effective_at_ms"], event["effective_at_ms"])
		current["ended_at_ms"] = reactionMaxTimestamp(current["ended_at_ms"], event["ended_at_ms"])
		timeline := make(map[string]map[string]any)
		for _, source := range []any{current["timeline"], event["timeline"]} {
			for _, value := range reactionSlice(source) {
				row := reactionMap(value)
				key := reactionString(row["kind"]) + ":" + strconv.FormatInt(reactionTimestampValue(row["at_ms"]), 10)
				if _, found := timeline[key]; !found {
					timeline[key] = row
				}
			}
		}
		timelineRows := make([]map[string]any, 0, len(timeline))
		for _, row := range timeline {
			timelineRows = append(timelineRows, row)
		}
		sort.Slice(timelineRows, func(i, j int) bool {
			left, right := reactionTimestamp(timelineRows[i]["at_ms"]), reactionTimestamp(timelineRows[j]["at_ms"])
			if left == nil || right == nil {
				return left != nil
			}
			if *left == *right {
				return reactionString(timelineRows[i]["kind"]) < reactionString(timelineRows[j]["kind"])
			}
			return *left < *right
		})
		current["timeline"] = reactionMapSliceAny(timelineRows)
	}
	result := make([]map[string]any, 0, len(order))
	for _, group := range order {
		result = append(result, grouped[group])
	}
	return result
}

func reactionEventLess(left, right map[string]any) bool {
	for _, pair := range [][2]float64{{math.Abs(reactionValue(left["linked_asset_current_window_contribution_pp"])), math.Abs(reactionValue(right["linked_asset_current_window_contribution_pp"]))}, {math.Abs(reactionValue(left["domestic_volume_change_pct"])), math.Abs(reactionValue(right["domestic_volume_change_pct"]))}} {
		if pair[0] != pair[1] {
			return pair[0] > pair[1]
		}
	}
	leftPriority, rightPriority := reactionNoticePriority[strings.ToUpper(reactionString(left["event_type"]))], reactionNoticePriority[strings.ToUpper(reactionString(right["event_type"]))]
	if leftPriority != rightPriority {
		return leftPriority > rightPriority
	}
	leftEvidence, rightEvidence := reactionEvidencePriority[reactionString(left["evidence_level"])], reactionEvidencePriority[reactionString(right["evidence_level"])]
	if leftEvidence != rightEvidence {
		return leftEvidence > rightEvidence
	}
	return reactionValue(reactionMap(left["origin"])["event_time_ms"]) > reactionValue(reactionMap(right["origin"])["event_time_ms"])
}

func largestReactionExchangeChange(events []map[string]any) any {
	var best map[string]any
	bestMagnitude := -1.0
	for _, event := range events {
		study := reactionMap(event["reaction_study"])
		if incompleteReactionStatus(reactionString(study["status"])) {
			continue
		}
		for _, value := range reactionSlice(event["exchange_reactions"]) {
			row := reactionMap(value)
			change := reactionNumber(row["volume_change_pct"])
			before, after := reactionNumber(row["before_volume_krw"]), reactionNumber(row["after_volume_krw"])
			if change == nil || before == nil || after == nil || math.Abs(*change) <= bestMagnitude {
				continue
			}
			bestMagnitude = math.Abs(*change)
			best = map[string]any{
				"event_id": event["event_id"], "event_title": event["title"], "exchange": row["exchange"],
				"volume_change_pct": change, "volume_change_krw": *after - *before,
				"linked_asset_count":             len(reactionSlice(event["linked_assets"])),
				"current_window_contribution_pp": event["linked_asset_current_window_contribution_pp"],
				"contribution_scope":             reactionCurrentWindowContributionScope,
				"share_impact_pp":                nil,
			}
		}
	}
	if best == nil {
		return nil
	}
	return best
}

func noReactionNoticeContext(analysis map[string]any) map[string]any {
	summary := reactionMap(analysis["summary"])
	short, hour := map[string]any(nil), map[string]any(nil)
	for _, value := range reactionSlice(analysis["momentum"]) {
		row := reactionMap(value)
		if reactionString(row["window"]) == "10s" {
			short = row
		}
		if reactionString(row["window"]) == "1h" {
			hour = row
		}
	}
	shortShare, hourShare := reactionNumber(short["share_pct"]), reactionNumber(hour["share_pct"])
	var change *float64
	if shortShare != nil && hourShare != nil {
		change = reactionFloat(*shortShare - *hourShare)
	}
	var leading map[string]any
	for _, value := range reactionSlice(analysis["assets"]) {
		row := reactionMap(value)
		if reactionNumber(row["share_change_contribution_pp"]) == nil {
			continue
		}
		if leading == nil || math.Abs(reactionValue(row["share_change_contribution_pp"])) > math.Abs(reactionValue(leading["share_change_contribution_pp"])) {
			leading = row
		}
	}
	priorities := map[string]int{"LIQUIDITY_GAP": 5, "SPREAD_GAP": 4, "SLIPPAGE_GAP": 3, "GLOBAL_INTEREST_GAP": 2, "CAPTURE_GAP": 1}
	var signal map[string]any
	for _, value := range reactionSlice(analysis["findings"]) {
		row := reactionMap(value)
		if priorities[reactionString(row["kind"])] > priorities[reactionString(signal["kind"])] {
			signal = row
		}
	}
	observed := reactionNumber(summary["domestic_volume_krw"]) != nil || reactionNumber(summary["global_context_volume_krw"]) != nil || shortShare != nil || hourShare != nil || leading != nil || signal != nil
	message := "관련 국내 공지는 없지만 현재 시장 움직임 자료도 충분히 확보되지 않았습니다."
	if observed {
		message = "관련 국내 공지 없이 국내 5개 거래소에서 관측된 시장 움직임입니다. Binance Spot 수치는 비교 보조 자료일 뿐, 공지와의 인과관계로 해석하지 않습니다."
	}
	var leadingResult any
	if leading != nil {
		leadingResult = map[string]any{"asset": reactionString(leading["asset"]), "share_change_contribution_pp": reactionNumber(leading["share_change_contribution_pp"]), "domestic_volume_krw": reactionNumber(leading["domestic_volume_krw"])}
	}
	var signalResult any
	if signal != nil {
		signalResult = map[string]any{"kind": reactionString(signal["kind"]), "title": reactionString(signal["title"]), "detail": reactionString(signal["detail"]), "asset": reactionNullableString(signal["asset"]), "value_krw": reactionNumber(signal["value_krw"]), "value_pct": reactionNumber(signal["value_pct"]), "value_bps": reactionNumber(signal["value_bps"])}
	}
	globalVolume := reactionNumber(summary["global_context_volume_krw"])
	return map[string]any{"status": "NO_RELEVANT_NOTICE", "message": message, "domestic_volume_krw": reactionNumber(summary["domestic_volume_krw"]), "global_context_volume_krw": globalVolume, "binance_volume_krw": globalVolume, "domestic_share_pct": reactionNumber(summary["bithumb_share_pct"]), "momentum": map[string]any{"short_share_pct": shortShare, "long_share_pct": hourShare, "change_pp": change, "short_status": reactionDefaultString(short["status"], "UNAVAILABLE"), "long_status": reactionDefaultString(hour["status"], "UNAVAILABLE"), "short_coverage_sec": reactionInt(short["coverage_sec"]), "long_coverage_sec": reactionInt(hour["coverage_sec"])}, "leading_asset": leadingResult, "market_signal": signalResult}
}

func reactionNoticeRelevant(item map[string]any, asOfMS int64, windowSec int64) bool {
	start := asOfMS - windowSec*1000
	openEndedStart := asOfMS - max(windowSec, 86_400)*1000
	if at := reactionPrecisePublished(item); at != nil && *at >= start && *at <= asOfMS {
		return true
	}
	hasSchedule, futureEndOnly, recentSchedule := false, false, false
	for _, value := range reactionSlice(item["schedules"]) {
		schedule := reactionMap(value)
		startAt, endAt := reactionTimestampValue(schedule["start_ts_ms"]), reactionTimestampValue(schedule["end_ts_ms"])
		if startAt > 0 || endAt > 0 {
			hasSchedule = true
		}
		if startAt > 0 && startAt <= asOfMS && endAt > 0 && endAt >= start {
			return true
		}
		if startAt > 0 && startAt >= openEndedStart && startAt <= asOfMS && endAt == 0 && !reactionTerminalTitle.MatchString(reactionString(item["title"])) {
			return true
		}
		if startAt == 0 && endAt >= start && endAt <= asOfMS {
			return true
		}
		if startAt == 0 && endAt > asOfMS {
			futureEndOnly = true
		}
		if (startAt >= openEndedStart && startAt <= asOfMS) || (endAt >= openEndedStart && endAt <= asOfMS) {
			recentSchedule = true
		}
	}
	if futureEndOnly && recentSchedule && strings.EqualFold(reactionString(item["category"]), "EVENT") && !reactionTerminalTitle.MatchString(reactionString(item["title"])) {
		return true
	}
	if strings.EqualFold(reactionDefaultString(item["timestamp_precision"], "SECOND"), "DETECTED") && !hasSchedule {
		if at := reactionTimestamp(item["detected_ts_ms"]); at != nil && *at >= start && *at <= asOfMS {
			return true
		}
	}
	return false
}

func reactionDataQuality(study map[string]any, complete bool) map[string]any {
	status := "UNAVAILABLE"
	if study != nil {
		status = "OBSERVED"
		if incompleteReactionStatus(reactionString(study["status"])) || !complete {
			status = "PARTIAL"
		}
	}
	note := "공지 연결과 시장 반응은 시간상 연관이며 인과관계가 아닙니다."
	if study != nil && reactionString(study["status"]) == partialReactionStudy {
		note = "연속 관측에 공백이 있어 확보된 구간만 표시하며 반응을 확정하지 않습니다."
	} else if study != nil && reactionString(study["status"]) == incompleteFXReactionStudy {
		note = "원화 환산이 끝나지 않은 체결이 있어 확인된 금액만 표시하며 반응을 확정하지 않습니다."
	}
	return map[string]any{"status": status, "observed_post_sec": reactionStudyNumber(study, "observed_post_sec"), "contiguous_market_coverage": study != nil && reactionBool(study["contiguous_market_coverage"]), "note": note}
}
func reactionMaturity(study map[string]any) string {
	if study == nil {
		return "UNAVAILABLE"
	}
	if incompleteReactionStatus(reactionString(study["status"])) {
		return "PARTIAL"
	}
	observed := reactionValue(study["observed_post_sec"])
	if observed >= 3600 {
		return "AFTERCARE"
	}
	if observed >= 600 {
		return "SUSTAINED"
	}
	return "EARLY"
}
func reactionCurrentWindowContributionTotals(rows []map[string]any) (*float64, *float64, *float64, any, any) {
	sum, positive, negative, posCount, negCount, found := 0.0, 0.0, 0.0, 0, 0, false
	for _, row := range rows {
		value := reactionNumber(row["current_window_contribution_pp"])
		if value == nil {
			continue
		}
		found = true
		sum += *value
		if *value > 0 {
			positive += *value
			posCount++
		}
		if *value < 0 {
			negative += *value
			negCount++
		}
	}
	if !found {
		return nil, nil, nil, nil, nil
	}
	return reactionFloat(sum), reactionFloat(positive), reactionFloat(negative), posCount, negCount
}
func reactionLinkedVolume(rows []map[string]any) *float64 {
	total, found := 0.0, false
	for _, row := range rows {
		if value := reactionNumber(row["domestic_volume_krw"]); value != nil {
			total += *value
			found = true
		}
	}
	if !found {
		return nil
	}
	return reactionFloat(total)
}
func reactionFirstSchedule(item map[string]any) *int64 {
	var result *int64
	for _, value := range reactionSlice(item["schedules"]) {
		if at := reactionTimestamp(reactionMap(value)["start_ts_ms"]); at != nil && (result == nil || *at < *result) {
			result = at
		}
	}
	return result
}
func reactionLastSchedule(item map[string]any) *int64 {
	var result *int64
	for _, value := range reactionSlice(item["schedules"]) {
		if at := reactionTimestamp(reactionMap(value)["end_ts_ms"]); at != nil && (result == nil || *at > *result) {
			result = at
		}
	}
	return result
}
func reactionPrecisePublished(item map[string]any) *int64 {
	if !strings.EqualFold(reactionDefaultString(item["timestamp_precision"], "SECOND"), "SECOND") {
		return nil
	}
	return reactionTimestamp(item["published_ts_ms"])
}
func reactionPercent(before, after *float64) *float64 {
	if before == nil || after == nil || *before <= 0 {
		return nil
	}
	return reactionFloat((*after / *before - 1) * 100)
}
func reactionStudyNumber(study map[string]any, key string) *float64 {
	if study == nil {
		return nil
	}
	return reactionNumber(study[key])
}
func reactionObserved(value *int64) string {
	if value != nil {
		return "OBSERVED"
	}
	return "UNAVAILABLE"
}
func unresolvedReactionOrigin() map[string]any {
	return map[string]any{"type": "UNRESOLVED", "label": reactionOriginLabels["UNRESOLVED"], "event_time_ms": nil, "event_time_basis": "UNAVAILABLE", "notice_time_ms": nil, "effective_time_ms": nil, "domestic_reaction_time_ms": nil, "global_reaction_time_ms": nil, "binance_reaction_time_ms": nil, "lead_seconds": nil, "evidence_level": "UNAVAILABLE"}
}

func reactionMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if row, ok := value.(map[string]any); ok {
		return row
	}
	return map[string]any{}
}
func reactionSlice(value any) []any {
	if value == nil {
		return []any{}
	}
	if rows, ok := value.([]any); ok {
		return rows
	}
	if rows, ok := value.([]string); ok {
		result := make([]any, len(rows))
		for index := range rows {
			result[index] = rows[index]
		}
		return result
	}
	return []any{}
}
func reactionMapSliceAny(rows []map[string]any) []any {
	result := make([]any, len(rows))
	for index := range rows {
		result[index] = rows[index]
	}
	return result
}
func reactionString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
func reactionNullableString(value any) any {
	if result := reactionString(value); result != "" {
		return result
	}
	return nil
}
func reactionDefaultString(value any, fallback string) string {
	if text := reactionString(value); text != "" {
		return text
	}
	return fallback
}
func reactionBool(value any) bool { result, _ := value.(bool); return result }
func reactionNumber(value any) *float64 {
	var result float64
	switch value := value.(type) {
	case float64:
		result = value
	case float32:
		result = float64(value)
	case int:
		result = float64(value)
	case int64:
		result = float64(value)
	case *float64:
		if value == nil {
			return nil
		}
		result = *value
	case *int64:
		if value == nil {
			return nil
		}
		result = float64(*value)
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return nil
		}
		result = parsed
	default:
		return nil
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil
	}
	return &result
}
func reactionValue(value any) float64 {
	if number := reactionNumber(value); number != nil {
		return *number
	}
	return 0
}
func reactionDefaultValue(value any, fallback float64) float64 {
	if number := reactionNumber(value); number != nil {
		return *number
	}
	return fallback
}
func reactionTimestamp(value any) *int64 {
	if result, ok := value.(*int64); ok {
		return result
	}
	number := reactionNumber(value)
	if number == nil || *number <= 0 {
		return nil
	}
	result := int64(*number)
	return &result
}
func reactionTimestampValue(value any) int64 {
	if result := reactionTimestamp(value); result != nil {
		return *result
	}
	return 0
}
func reactionNonnegativeInt(value any) *int64 {
	number := reactionNumber(value)
	if number == nil || *number < 0 {
		return nil
	}
	result := int64(*number)
	return &result
}
func reactionInt(value any) int {
	if number := reactionNumber(value); number != nil {
		return int(*number)
	}
	return 0
}
func reactionFloat(value float64) *float64 { return &value }
func reactionInt64(value int64) *int64     { return &value }
func reactionIf(condition bool, value *float64) *float64 {
	if condition {
		return value
	}
	return nil
}
func reactionAny(row map[string]any, key string) any {
	if row == nil {
		return nil
	}
	return row[key]
}
func reactionMinTimestamp(left, right any) *int64 {
	a, b := reactionTimestamp(left), reactionTimestamp(right)
	if a == nil {
		return b
	}
	if b == nil || *a < *b {
		return a
	}
	return b
}
func reactionMaxTimestamp(left, right any) *int64 {
	a, b := reactionTimestamp(left), reactionTimestamp(right)
	if a == nil {
		return b
	}
	if b == nil || *a > *b {
		return a
	}
	return b
}
func reactionUnique(values []any) []any {
	seen := map[string]bool{}
	result := []any{}
	for _, value := range values {
		key := reactionString(value)
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}
func reactionDomestic(exchange string) bool {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	for _, candidate := range domain.DomesticExchanges {
		if exchange == string(candidate) {
			return true
		}
	}
	return false
}
