package app

import (
	"fmt"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/tickertrend"
)

const (
	marketQuestionAuxiliaryNoticeLimit      = 5
	marketQuestionAuxiliaryRealtimeMaxAgeMS = int64(5_000)
)

func marketQuestionAuxiliaryFacts(
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
	driverAssets []string,
) *marketanalysis.MarketQuestionAuxiliaryFacts {
	requested := make(map[string]bool, len(query.RequestedExplanation))
	for _, value := range query.RequestedExplanation {
		requested[strings.ToUpper(strings.TrimSpace(value))] = true
	}
	if !requested["REALTIME"] && !requested["ORDERBOOK"] && !requested["NOTICES"] {
		return nil
	}
	result := &marketanalysis.MarketQuestionAuxiliaryFacts{
		Requested: append([]string(nil), query.RequestedExplanation...),
	}
	asset := strings.ToUpper(strings.TrimSpace(query.Asset))
	if requested["REALTIME"] {
		referenceMS := int64(payload.GeneratedAt * 1_000)
		if referenceMS <= 0 {
			referenceMS = payload.RealtimeFlow.AsOfMS
		}
		result.Realtime = marketQuestionRealtimeContext(payload.RealtimeFlow, asset, referenceMS)
	}
	if requested["ORDERBOOK"] {
		result.Orderbook = marketQuestionOrderbookContext(payload, asset)
	}
	if requested["NOTICES"] {
		result.Notices = marketQuestionNoticeContext(payload, query, asOfMS, driverAssets)
	}
	return result
}

func marketQuestionRealtimeContext(
	state marketanalysis.RealtimeFlowState,
	asset string,
	referenceMS int64,
) *marketanalysis.MarketQuestionRealtimeContext {
	ageMS := referenceMS - state.AsOfMS
	if ageMS < 0 {
		ageMS = 0
	}
	ageSec := float64(ageMS) / 1_000
	evidenceStatus := "UNAVAILABLE"
	for _, window := range state.Windows {
		if window.Status == "COMPLETE" && state.AsOfMS > 0 && state.Status != "" &&
			state.Status != "DEGRADED" && ageMS <= marketQuestionAuxiliaryRealtimeMaxAgeMS {
			evidenceStatus = "LIVE"
			break
		}
	}
	result := &marketanalysis.MarketQuestionRealtimeContext{
		AsOfMS: state.AsOfMS, AgeSec: &ageSec, Status: evidenceStatus, SignalStatus: state.Status, Reason: state.Reason,
		Direction: state.Direction, PrimaryWindowSec: state.PrimaryWindowSec,
		ShareDeltaPP: copyQuestionFloat(state.ShareDeltaPP),
		Windows:      append([]marketanalysis.RealtimeFlowWindow(nil), state.Windows...),
	}
	for _, value := range state.CurrentAssets {
		if !strings.EqualFold(value.Asset, asset) {
			continue
		}
		result.Asset = &marketanalysis.MarketQuestionRealtimeAssetContext{
			Asset: value.Asset, DomesticVolumeKRW: value.DomesticVolumeKRW,
			BithumbVolumeKRW: value.BithumbVolumeKRW, BithumbSharePct: copyQuestionFloat(value.BithumbSharePct),
			BithumbBuyPct: copyQuestionFloat(value.BithumbBuyPct), TopCompetitor: value.TopCompetitor,
			TopCompetitorVolumeKRW: value.TopCompetitorVolumeKRW, Orderbook: cloneMarketQuestionOrderbook(value.Orderbook),
		}
		break
	}
	return result
}

func marketQuestionOrderbookContext(
	payload marketanalysis.Analysis,
	asset string,
) *marketanalysis.MarketQuestionOrderbookContext {
	if asset == "" {
		return &marketanalysis.MarketQuestionOrderbookContext{Status: "UNAVAILABLE", Source: "ASSET_REQUIRED"}
	}
	for _, value := range payload.RealtimeFlow.CurrentAssets {
		if !strings.EqualFold(value.Asset, asset) || value.Orderbook == nil {
			continue
		}
		book := value.Orderbook
		return &marketanalysis.MarketQuestionOrderbookContext{
			Asset: asset, Source: "WEBSOCKET_REALTIME_FLOW", Status: book.Status, AsOfMS: payload.RealtimeFlow.AsOfMS,
			AgeSec: copyQuestionFloat(book.AgeSec), Imbalance: copyQuestionFloat(book.Imbalance),
			SpreadBPS: copyQuestionFloat(book.SpreadBPS), BidDepthKRW: copyQuestionFloat(book.BidDepthKRW),
			AskDepthKRW: copyQuestionFloat(book.AskDepthKRW), BuySlippageBPS: copyQuestionFloat(book.BuySlippageBPS),
			SellSlippageBPS: copyQuestionFloat(book.SellSlippageBPS),
		}
	}
	for _, value := range payload.Assets {
		if !strings.EqualFold(value.Asset, asset) {
			continue
		}
		book := value.Microstructure
		return &marketanalysis.MarketQuestionOrderbookContext{
			Asset: asset, Source: "ANALYSIS_ASSET_MICROSTRUCTURE", Status: book.Status,
			AsOfMS: int64(payload.Context.OrderBookAsOf * 1_000),
			AgeSec: copyQuestionFloat(book.BithumbOrderBookMaxAgeSec), SpreadBPS: copyQuestionFloat(book.BithumbMedianSpreadBPS),
			BuySlippageBPS:  copyQuestionFloat(book.BithumbBuySlippageBPS),
			SellSlippageBPS: copyQuestionFloat(book.BithumbSellSlippageBPS),
		}
	}
	return &marketanalysis.MarketQuestionOrderbookContext{
		Asset: asset, Source: "WEBSOCKET_ORDERBOOK", Status: "UNAVAILABLE",
	}
}

func marketQuestionNoticeContext(
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
	driverAssets []string,
) *marketanalysis.MarketQuestionNoticeContext {
	windowSec := int64(86_400)
	if seconds, requested := marketQuestionWSWindowSec(query); requested {
		windowSec = seconds
	} else if seconds, requested := marketQuestionOfficialWindowSec(query); requested {
		windowSec = seconds
	}
	result := &marketanalysis.MarketQuestionNoticeContext{
		WindowStartMS: asOfMS - windowSec*1_000, WindowEndMS: asOfMS,
		SourceCount: payload.Notices.SourceCount, HealthySourceCount: payload.Notices.HealthySourceCount,
		Matches: []marketanalysis.MarketQuestionNoticeFact{}, CausalClaim: false,
	}
	prepared := append([]notice.MarketEvidenceItem(nil), notice.PreparedMarketEvidence(payload.Notices)...)
	sort.SliceStable(prepared, func(i, j int) bool {
		return marketQuestionNoticeAnchor(prepared[i].Item) > marketQuestionNoticeAnchor(prepared[j].Item)
	})
	asset := strings.ToUpper(strings.TrimSpace(query.Asset))
	driverAssetSet := marketQuestionNoticeAssetSet(driverAssets)
	for _, value := range prepared {
		if !value.CurrentLifecycle || asset != "" && !marketQuestionNoticeHasAsset(value, asset) ||
			asset == "" && len(driverAssetSet) > 0 && !marketQuestionNoticeHasAnyAsset(value, driverAssetSet) {
			continue
		}
		anchor := marketQuestionNoticeAnchor(value.Item)
		period := marketQuestionNoticePeriod(value.Brief.Schedules, result.WindowStartMS, result.WindowEndMS)
		relation := "NOTICE_IN_WINDOW"
		if anchor < result.WindowStartMS || anchor > result.WindowEndMS {
			if !marketQuestionNoticeScheduleOverlaps(value.Brief.Schedules, result.WindowStartMS, result.WindowEndMS) {
				continue
			}
			relation = "SCHEDULE_OVERLAPS_WINDOW"
		}
		title := strings.TrimSpace(value.Item.TranslatedTitleKO)
		if title == "" {
			title = strings.TrimSpace(value.Item.Title)
		}
		result.Matches = append(result.Matches, marketanalysis.MarketQuestionNoticeFact{
			Exchange: value.Item.Exchange, NoticeID: value.Item.NoticeID,
			Title: truncateMarketQuestionText(title, 200), AnchorMS: anchor,
			Assets: marketQuestionNoticeAssets(value), Relation: relation, Period: period, CausalClaim: false,
		})
		if len(result.Matches) == marketQuestionAuxiliaryNoticeLimit {
			break
		}
	}
	return result
}

func marketQuestionNoticeDriverAssets(
	official tickertrend.WindowComparison,
	websocket marketanalysis.MarketQuestionIntervalEvidence,
) []string {
	seen := map[string]bool{}
	result := []string{}
	add := func(asset string) {
		asset = strings.ToUpper(strings.TrimSpace(asset))
		if asset != "" && !seen[asset] {
			seen[asset] = true
			result = append(result, asset)
		}
	}
	for _, driver := range websocket.Drivers {
		add(driver.Asset)
	}
	for _, driver := range official.Drivers {
		add(driver.Asset)
	}
	return result
}

func marketQuestionNoticeAssetSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.ToUpper(strings.TrimSpace(value)); value != "" {
			result[value] = true
		}
	}
	return result
}

func marketQuestionNoticeHasAnyAsset(value notice.MarketEvidenceItem, assets map[string]bool) bool {
	for _, current := range marketQuestionNoticeAssets(value) {
		if assets[strings.ToUpper(strings.TrimSpace(current))] {
			return true
		}
	}
	return false
}

func marketQuestionNoticePeriod(
	values []notice.Schedule,
	windowStartMS, windowEndMS int64,
) *marketanalysis.MarketQuestionNoticePeriod {
	var selected *marketanalysis.MarketQuestionNoticePeriod
	selectedRank := -1
	selectedDistance := int64(0)
	for _, value := range values {
		if value.StartTSMS <= 0 || value.EndTSMS <= 0 || value.EndTSMS < value.StartTSMS {
			continue
		}
		relation, applies, rank := marketQuestionNoticePeriodRelation(
			value.StartTSMS, value.EndTSMS, windowStartMS, windowEndMS,
		)
		distance := marketQuestionNoticePeriodDistance(value.StartTSMS, value.EndTSMS, windowStartMS, windowEndMS)
		if selected != nil && (rank < selectedRank || rank == selectedRank && distance >= selectedDistance) {
			continue
		}
		selected = &marketanalysis.MarketQuestionNoticePeriod{
			Kind: value.Kind, StartMS: value.StartTSMS, EndMS: value.EndTSMS,
			Timezone: value.Timezone, Precision: value.Precision,
			Relation: relation, AppliesToQuestionWindow: applies,
		}
		selectedRank, selectedDistance = rank, distance
	}
	return selected
}

func marketQuestionNoticePeriodRelation(
	periodStartMS, periodEndMS, windowStartMS, windowEndMS int64,
) (string, bool, int) {
	switch {
	case periodStartMS <= windowStartMS && periodEndMS >= windowEndMS:
		return "QUESTION_WINDOW_WITHIN_NOTICE_PERIOD", true, 4
	case periodStartMS >= windowStartMS && periodEndMS <= windowEndMS:
		return "NOTICE_PERIOD_WITHIN_QUESTION_WINDOW", true, 3
	case periodStartMS <= windowEndMS && periodEndMS >= windowStartMS:
		return "NOTICE_PERIOD_OVERLAPS_QUESTION_WINDOW", true, 2
	default:
		return "NOTICE_PERIOD_OUTSIDE_QUESTION_WINDOW", false, 1
	}
}

func marketQuestionNoticePeriodDistance(
	periodStartMS, periodEndMS, windowStartMS, windowEndMS int64,
) int64 {
	if periodEndMS < windowStartMS {
		return windowStartMS - periodEndMS
	}
	if periodStartMS > windowEndMS {
		return periodStartMS - windowEndMS
	}
	return 0
}

func marketQuestionNoticeAnchor(item notice.Item) int64 {
	if item.PublishedTSMS > 0 {
		return item.PublishedTSMS
	}
	if item.DetectedTSMS > 0 {
		return item.DetectedTSMS
	}
	return item.UpdatedTSMS
}

func marketQuestionNoticeHasAsset(value notice.MarketEvidenceItem, asset string) bool {
	for _, current := range marketQuestionNoticeAssets(value) {
		if strings.EqualFold(current, asset) {
			return true
		}
	}
	return false
}

func marketQuestionNoticeAssets(value notice.MarketEvidenceItem) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, values := range [][]string{value.Item.Assets, value.Brief.Assets} {
		for _, current := range values {
			current = strings.ToUpper(strings.TrimSpace(current))
			if current != "" && !seen[current] {
				seen[current] = true
				result = append(result, current)
			}
		}
	}
	sort.Strings(result)
	return result
}

func marketQuestionNoticeScheduleOverlaps(values []notice.Schedule, startMS, endMS int64) bool {
	for _, value := range values {
		end := value.EndTSMS
		if end == 0 {
			end = value.StartTSMS
		}
		if value.StartTSMS <= endMS && end >= startMS {
			return true
		}
	}
	return false
}

func truncateMarketQuestionText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func cloneMarketQuestionOrderbook(value *movement.RealtimeOrderbook) *movement.RealtimeOrderbook {
	if value == nil {
		return nil
	}
	return &movement.RealtimeOrderbook{
		Status: value.Status, AgeSec: copyQuestionFloat(value.AgeSec), Imbalance: copyQuestionFloat(value.Imbalance),
		SpreadBPS: copyQuestionFloat(value.SpreadBPS), BidDepthKRW: copyQuestionFloat(value.BidDepthKRW),
		AskDepthKRW: copyQuestionFloat(value.AskDepthKRW), BuySlippageBPS: copyQuestionFloat(value.BuySlippageBPS),
		SellSlippageBPS: copyQuestionFloat(value.SellSlippageBPS),
	}
}

func enrichMarketQuestionAuxiliaryFacts(
	answer marketanalysis.MarketQuestionAnswer,
	facts *marketanalysis.MarketQuestionAuxiliaryFacts,
) marketanalysis.MarketQuestionAnswer {
	if facts == nil {
		return answer
	}
	parts := []string{}
	if facts.Realtime != nil {
		parts = append(parts, marketQuestionRealtimeLabel(*facts.Realtime))
	}
	if facts.Orderbook != nil {
		label := "호가 확인 불가"
		if facts.Orderbook.Status == "LIVE" || facts.Orderbook.Status == "COMPLETE" {
			label = "호가 확인"
			if facts.Orderbook.SpreadBPS != nil {
				label += fmt.Sprintf(" · 스프레드 %.2fbps", *facts.Orderbook.SpreadBPS)
			}
			if facts.Orderbook.Imbalance != nil {
				label += fmt.Sprintf(" · 잔량 불균형 %.3f", *facts.Orderbook.Imbalance)
			}
		}
		parts = append(parts, label)
	}
	if facts.Notices != nil {
		parts = append(parts, fmt.Sprintf("구간·자산 일치 공지 %d건(원인 단정 아님)", len(facts.Notices.Matches)))
	}
	if len(parts) == 0 {
		return answer
	}
	point := "보조 맥락: " + strings.Join(parts, " / ")
	if marketQuestionContainsString(answer.KeyPoints, point) {
		return answer
	}
	if len(answer.KeyPoints) < marketQuestionPointLimit {
		answer.KeyPoints = append(answer.KeyPoints, point)
	} else if len(answer.KeyPoints) > 0 {
		answer.KeyPoints[len(answer.KeyPoints)-1] = point
	}
	return answer
}

// marketQuestionRealtimeLabel keeps the documented 10-second-first choice and
// states the observation time; a result that cannot be judged gives no number.
func marketQuestionRealtimeLabel(realtime marketanalysis.MarketQuestionRealtimeContext) string {
	if realtime.Status == "LIVE" {
		windows := append([]marketanalysis.RealtimeFlowWindow(nil), realtime.Windows...)
		sort.SliceStable(windows, func(i, j int) bool { return windows[i].DurationSec < windows[j].DurationSec })
		for _, window := range windows {
			if window.Status == "COMPLETE" && window.ShareDeltaPP != nil {
				return fmt.Sprintf(
					"실시간 체결 빗썸 점유율 %d초 %+.2f%%p(실제 관측 기준 KST %s, 공식 티커·분봉과 별도)",
					window.DurationSec, *window.ShareDeltaPP, marketQuestionKST(realtime.AsOfMS),
				)
			}
		}
	}
	if realtime.SignalStatus == "DEGRADED" {
		return "실시간 체결 흐름 판단 보류(일부 거래소 체결 누락)"
	}
	return "실시간 체결 흐름 판단 보류(최신 완전 자료 없음)"
}

// appendMarketQuestionNoticeParagraph answers the notice part of a question in
// the server briefing body. Matching notices are time or coin overlaps only.
func appendMarketQuestionNoticeParagraph(
	answer marketanalysis.MarketQuestionAnswer,
	facts *marketanalysis.MarketQuestionAuxiliaryFacts,
) marketanalysis.MarketQuestionAnswer {
	if facts == nil || facts.Notices == nil {
		return answer
	}
	notices := facts.Notices
	paragraph := "같은 기간·관련 코인과 겹치는 국내 거래소 공지는 확인되지 않았습니다."
	if notices.HealthySourceCount < notices.SourceCount || notices.SourceCount == 0 {
		paragraph = "같은 기간·관련 코인과 겹치는 국내 거래소 공지는 확인되지 않았지만, 공지 수집원이 모두 정상은 아니어서 공지가 없었다고 단정하지 않습니다."
	}
	if len(notices.Matches) > 0 {
		labels := make([]string, 0, len(notices.Matches))
		for _, match := range notices.Matches {
			label := rollingExchangeLabel(match.Exchange) + " ‘" + match.Title + "’"
			if match.Period != nil {
				label += "(" + marketQuestionNoticePeriodLabel(*match.Period) + ")"
			}
			labels = append(labels, label)
		}
		paragraph = fmt.Sprintf(
			"같은 기간·관련 코인과 겹치는 국내 거래소 공지는 %d건입니다: %s. 시간이나 코인이 겹친다는 사실만으로 공지가 점유율 변화의 원인이라고 단정하지 않습니다.",
			len(notices.Matches), strings.Join(labels, ", "),
		)
	}
	answer.Answer = strings.TrimSpace(answer.Answer + "\n\n" + paragraph)
	return answer
}

func marketQuestionNoticePeriodLabel(period marketanalysis.MarketQuestionNoticePeriod) string {
	label := fmt.Sprintf(
		"공지 기간 KST %s~%s",
		marketQuestionKST(period.StartMS), marketQuestionKST(period.EndMS),
	)
	switch period.Relation {
	case "QUESTION_WINDOW_WITHIN_NOTICE_PERIOD":
		return label + " · 질문 구간 전체 해당"
	case "NOTICE_PERIOD_WITHIN_QUESTION_WINDOW":
		return label + " · 공지 기간 전체가 질문 구간에 해당"
	case "NOTICE_PERIOD_OVERLAPS_QUESTION_WINDOW":
		return label + " · 질문 구간 일부 해당"
	default:
		return label + " · 질문 구간 미해당"
	}
}

func marketQuestionAuxiliaryWarnings(facts *marketanalysis.MarketQuestionAuxiliaryFacts) []string {
	if facts == nil {
		return nil
	}
	warnings := []string{}
	if facts.Realtime != nil && facts.Realtime.Status != "LIVE" {
		warnings = append(warnings, "실시간 체결 흐름이 판단 가능한 상태가 아니어서 보조 진단은 확정하지 않았습니다.")
	}
	if facts.Orderbook != nil && facts.Orderbook.Status != "LIVE" && facts.Orderbook.Status != "COMPLETE" {
		warnings = append(warnings, "호가 자료가 완전하지 않아 호가 진단은 확정하지 않았습니다.")
	}
	if facts.Notices != nil && facts.Notices.SourceCount == 0 {
		warnings = append(warnings, "공지 수집원이 없어 공지 0건을 공지 없음으로 볼 수 없습니다.")
	} else if facts.Notices != nil && facts.Notices.HealthySourceCount < facts.Notices.SourceCount {
		warnings = append(warnings, "공지 수집원 일부가 정상이 아니어서 공지 없음이 전체 부재를 뜻하지 않습니다.")
	}
	return warnings
}
