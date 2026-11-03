package notice

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

const briefValueLimit = 180

var (
	briefMarkdownLinkPattern = regexp.MustCompile(`\[([^]]+)\]\([^)]*\)`)
	briefWhitespacePattern   = regexp.MustCompile(`\s+`)
	briefPairPattern         = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,11})/(?:KRW|USDT|USDC|USDE|USDS|USD1|BTC|ETH)\b`)
	briefAssetPattern        = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,11})\b`)
	briefEventPattern        = regexp.MustCompile(`(?i)이벤트|프로모션|대회|리워드|에어드랍|airdrop|campaign`)
	briefBodyPeriodPattern   = regexp.MustCompile(`(?i)이벤트|프로모션|에어드랍|참여\s*기간|event\s+period|promotion\s+period`)

	briefCautionKindPattern     = regexp.MustCompile(`(?i)유의종목|투자유의|거래유의|market\s+warning|\bcaution\b`)
	briefTransferKindPattern    = regexp.MustCompile(`(?i)입출금\s*(?:중단|재개)|입금\s*(?:중단|재개)|출금\s*(?:중단|재개)|(?:deposit|withdrawal?)\s+(?:suspend|resume|halt)`)
	briefIncidentKindPattern    = regexp.MustCompile(`(?i)시스템\s*장애|서비스\s*장애|접속\s*장애|보안\s*(?:사고|이슈)|system\s+outage|service\s+(?:outage|disruption)|security\s+incident`)
	briefMaintenanceKindPattern = regexp.MustCompile(`(?i)정기\s*점검|시스템\s*점검|서비스\s*점검|\bmaintenance\b`)
	briefPolicyKindPattern      = regexp.MustCompile(`(?i)약관|개인정보\s*처리|정책\s*(?:변경|개정)|수수료\s*(?:변경|조정)|terms\s+(?:change|update)|privacy\s+(?:policy|update)|fee\s+(?:change|update)`)
	briefEventKindPattern       = regexp.MustCompile(`(?i)이벤트|프로모션|캠페인|대회|리워드|에어드랍|\bevent\b|\bcampaign\b|\bpromotion\b|\bairdrop\b`)

	briefScopePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:영향|이용\s*제한|서비스|점검|적용|변경)\s*(?:대상|범위)\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:affected\s+(?:service|scope|users?)|service\s+scope|applies\s+to)\s*[:：-]?\s*(.+)`),
	}
	briefActionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:참여|신청|이용|고객|사용자|회원)\s*(?:방법|조치|확인\s*사항)\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:필요\s*조치|조치\s*사항|해야\s*할\s*일)\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:how\s+to\s+participate|action\s+required|user\s+action)\s*[:：-]?\s*(.+)`),
	}
	briefReasonPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:사유|원인|점검\s*내용|장애\s*내용|변경\s*사유)\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:reason|cause|maintenance\s+details|incident\s+details)\s*[:：-]?\s*(.+)`),
	}
	briefStatusPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:현재|처리|복구|진행|서비스|지원)\s*상태\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:current|service|recovery)\s+status\s*[:：-]?\s*(.+)`),
	}
	briefChangePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:변경|개편|업데이트|적용|주요)\s*(?:내용|사항)\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:수수료|fee)\s*(?:변경|조정|정책)?\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:what(?:'s|\s+is)?\s+changing|changes?|update\s+details)\s*[:：-]?\s*(.+)`),
	}
	briefMarketPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:대상|지원|거래)\s*(?:마켓|페어)\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:markets?|trading\s+pairs?)\s*[:：-]\s*(.+)`),
	}
	briefMarketValuePattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,11}/(?:KRW|USDT|USDC|USDE|USDS|USD1|BTC|ETH)\b`)
	briefNetworkPatterns    = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:대상|지원|입출금)\s*네트워크\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:network|chain)\s*[:：-]\s*(.+)`),
	}
	briefCautionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:유의|주의|제외|제한)\s*(?:사항|조건|대상)?\s*[:：-]\s*(.+)`),
		regexp.MustCompile(`(?i)(?:caution|warning|exclusions?|restrictions?)\s*[:：-]\s*(.+)`),
	}
	briefIgnoredLinePattern = regexp.MustCompile(`(?i)^(?:안녕하세요|감사합니다|공지사항|안내사항|유의사항|주의사항|hello|dear\s+users?)\.?$`)
	briefActionLinePattern  = regexp.MustCompile(`(?i)변경|중단|재개|점검|지원|적용|지급|제공|종료|발생|복구|참여|change|suspend|resume|maintenance|support|apply|reward|end|restore`)
	briefNumberLinePattern  = regexp.MustCompile(`\d|[:：]`)
	briefDynamicFactPattern = regexp.MustCompile(`^\s*[-•■*]?\s*([가-힣A-Za-z][가-힣A-Za-z0-9\s/·()_\-]{1,24})\s*[:：]\s*(.+?)\s*$`)
	briefFactKeyPattern     = regexp.MustCompile(`[^가-힣a-z0-9]`)

	briefTargetPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:이벤트\s*)?(?:대상자|참여\s*대상|참가\s*대상)\s*(?:[:：-]\s*|(?:은|는)\s+)(.+)`),
		regexp.MustCompile(`(?i)지급\s*대상\s*(?:[:：-]\s*|(?:은|는)\s+)(.+)`),
		regexp.MustCompile(`(?i)(?:이벤트\s*)?대상\s*(?:고객|회원)\s*(?:[:：-]\s*|(?:은|는)\s+)(.+)`),
		regexp.MustCompile(`(?i)적용\s*대상\s*(?:[:：-]\s*|(?:은|는)\s+)(.+)`),
		regexp.MustCompile(`(?i)참여\s*(?:고객|회원|자격)\s*(?:[:：-]\s*|(?:은|는)\s+)(.+)`),
		regexp.MustCompile(`(?i)(?:eligible\s+participants?|who\s+can\s+participate|for\s+whom)\s*[:：-]?\s*(.+)`),
	}
	briefRewardPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(?:[-•■*]\s*)?(?:총\s*)?(?:상금|보상금|보상|리워드|혜택)\s*(?:[:：-]|은|는)\s*(.+)`),
		regexp.MustCompile(`(?i)^\s*(?:[-•■*]\s*)?(?:total\s+)?(?:prize|reward|bonus|rewards?)\s*(?:[:：-]|is)\s*(.+)`),
	}
	briefRewardValuePattern = regexp.MustCompile(`(?i)\d|무료|free|fee`)
	briefPrizePattern       = regexp.MustCompile(`(?i)상금|보상금|총\s*상금|prize`)
	briefMaximumPattern     = regexp.MustCompile(`(?i)최대|총\s*\d|상당|maximum|total`)
	briefMarketScopePattern = regexp.MustCompile(`(?i)\b([A-Z][A-Z0-9]{1,11})\s*(?:마켓|market)\b.{0,80}(?:전\s*종목|모든|all)|(?:전\s*종목|모든|all).{0,80}\b([A-Z][A-Z0-9]{1,11})\s*(?:마켓|market)\b`)
)

func Summarize(item Item) Brief {
	return summarizeWithImageConsistency(item, VerifyItemImageConsistency(item))
}

func summarizeWithImageConsistency(item Item, imageConsistency ImageConsistency) Brief {
	category := strings.ToUpper(strings.TrimSpace(item.Category))
	if category == "" {
		category = "OTHER"
	}
	riskTags := make(map[string]bool, len(item.RiskTags))
	for _, tag := range item.RiskTags {
		riskTags[strings.ToUpper(strings.TrimSpace(tag))] = true
	}
	bodyText := item.BodyExcerpt
	summaryText := ""
	if bodyText == "" {
		summaryText = item.Summary
	}
	detailText := firstBriefValue(bodyText, summaryText)
	detailSource := "TITLE"
	if bodyText != "" {
		detailSource = "BODY"
	} else if summaryText != "" {
		detailSource = "SUMMARY"
	}
	ocrUsable := item.ImageText != "" && imageConsistency.Status == "MATCHED"
	imageText := ""
	if ocrUsable {
		imageText = item.ImageText
	}
	combined := joinBriefText(item.Title, detailText, imageText)
	primaryFactText := joinBriefText(detailText, item.Title)
	schedules := append(make([]Schedule, 0, len(item.Schedules)), item.Schedules...)
	hasCompleteSchedule := false
	for _, schedule := range schedules {
		if schedule.StartTSMS > 0 && schedule.EndTSMS > schedule.StartTSMS {
			hasCompleteSchedule = true
			break
		}
	}
	bodyPeriodPreferred := isEventCategory(category) || category == "SNAPSHOT" || riskTags["EVENT"] || briefBodyPeriodPattern.MatchString(combined)
	if combined != "" && (!hasCompleteSchedule || bodyPeriodPreferred) {
		reference := item.PublishedTSMS
		if reference == 0 {
			reference = item.DetectedTSMS
		}
		schedules = appendNewBriefSchedules(schedules, ExtractSchedules(combined, category, reference))
	}
	kind := briefDynamicKind(category, riskTags, combined)
	assetSourceText := joinBriefText(item.Title, detailText, imageText)
	assets := briefAssets(item.Assets, assetSourceText, kind == "EVENT")
	selected := briefScheduleCandidates(schedules, kind)
	periodStart, periodEnd := briefPeriod(selected)
	precision := briefPrecision(selected)
	recurrence := briefRecurrence(combined, schedules)
	type sourcedValue struct{ value, source string }
	sourced := func(extractor func(string) string) sourcedValue {
		if value := extractor(primaryFactText); value != "" {
			return sourcedValue{value, detailSource}
		}
		if ocrUsable {
			if value := extractor(item.ImageText); value != "" {
				return sourcedValue{value, "OCR"}
			}
		}
		return sourcedValue{}
	}
	rewardExtractor := func(content string) string {
		if kind == "EVENT" {
			if value := briefEventRewards(content); value != "" {
				return value
			}
		}
		if value := briefReward(content); value != "" {
			return value
		}
		lowered := strings.ToLower(content)
		if strings.Contains(lowered, "수수료 무료") || strings.Contains(lowered, "fee-free") {
			return "대상 거래 수수료 무료"
		}
		if strings.Contains(lowered, "수수료 인하") || strings.Contains(lowered, "fee discount") {
			return "대상 거래 수수료 인하"
		}
		return ""
	}
	operationKeywords := briefOperationKeywords(kind)
	target := sourced(briefTarget)
	reward := sourced(rewardExtractor)
	scope := sourced(briefScope)
	action := sourced(briefAction)
	reason := sourced(briefReason)
	statusValue := sourced(briefStatus)
	change := sourced(briefChange)
	market := sourced(briefMarket)
	network := sourced(briefNetwork)
	caution := sourced(briefCaution)
	eventConditions := sourced(briefEventConditions)
	operation := sourced(func(content string) string { return briefMatchingLine(content, operationKeywords) })
	detailAvailable := detailText != "" || item.ImageText != ""
	periodText := briefPeriodText(periodStart, periodEnd, precision, func() string {
		if periodStart == 0 && periodEnd == 0 {
			return recurrence
		}
		return ""
	}())
	periodAvailable := periodStart > 0 || periodEnd > 0 || recurrence != ""
	assetSource := "METADATA"
	upperPrimary, upperImage := strings.ToUpper(primaryFactText), strings.ToUpper(item.ImageText)
	if anyBriefAssetInText(assets, upperPrimary) {
		assetSource = detailSource
	} else if ocrUsable && anyBriefAssetInText(assets, upperImage) {
		assetSource = "OCR"
	}
	headline := briefFirstMeaningfulLine(detailText, item.Title)
	headlineSource := detailSource
	if headline == "" && ocrUsable {
		headline = briefFirstMeaningfulLine(item.ImageText, item.Title)
		headlineSource = "OCR"
	}
	if headline == "" {
		headline = briefValue(item.Title)
		if headline == "" {
			headline = "상세 내용을 확보하지 못했습니다."
		}
		headlineSource = "TITLE"
	}
	lines := make([]BriefLine, 0, 8)
	seenKeys, seenValues := map[string]bool{}, map[string]bool{}
	addFact := func(key, label, value, source string) {
		value = briefValue(value)
		comparable := strings.ToLower(briefWhitespacePattern.ReplaceAllString(value, ""))
		if value == "" || seenKeys[key] || seenValues[comparable] || len(lines) >= 8 {
			return
		}
		if source == "" {
			source = "BODY"
		}
		seenKeys[key], seenValues[comparable] = true, true
		lines = append(lines, BriefLine{Key: key, Label: label, Value: value, Source: source})
	}
	assetValue := strings.Join(assets, ", ")
	switch kind {
	case "EVENT":
		if periodAvailable {
			addFact("period", "이벤트 기간", periodText, "SCHEDULE")
		}
		addFact("target", "참여 대상", target.value, target.source)
		addFact("asset", "대상 자산", assetValue, assetSource)
		addFact("market", "대상 마켓·상품", firstBriefValue(market.value, briefMarketScope(primaryFactText)), firstBriefValue(market.source, detailSource))
		addFact("reward", "혜택·보상", reward.value, reward.source)
		addFact("action", "참여 방법", action.value, action.source)
		addFact("caution", "필수 조건", firstBriefValue(eventConditions.value, caution.value, operation.value), firstBriefValue(eventConditions.source, caution.source, operation.source))
	case "LISTING_STATUS":
		addFact("asset", "대상 자산", assetValue, assetSource)
		addFact("market", "지원 마켓", market.value, market.source)
		if periodAvailable {
			addFact("period", "거래·지원 일정", periodText, "SCHEDULE")
		}
		addFact("operation", "주요 조치", operation.value, operation.source)
		addFact("scope", "영향 범위", firstBriefValue(scope.value, target.value), firstBriefValue(scope.source, target.source))
		addFact("caution", "확인 사항", caution.value, caution.source)
	case "DEPOSIT_WITHDRAWAL":
		addFact("asset", "대상 자산", assetValue, assetSource)
		addFact("network", "대상 네트워크", network.value, network.source)
		if periodAvailable {
			addFact("period", "중단·재개 일정", periodText, "SCHEDULE")
		}
		addFact("status", "처리 상태", firstBriefValue(statusValue.value, operation.value), firstBriefValue(statusValue.source, operation.source))
		addFact("reason", "사유", reason.value, reason.source)
		addFact("action", "사용자 확인", firstBriefValue(action.value, caution.value), firstBriefValue(action.source, caution.source))
	case "CAUTION":
		addFact("asset", "대상 자산", assetValue, assetSource)
		if periodAvailable {
			addFact("period", "지정·해제 일정", periodText, "SCHEDULE")
		}
		addFact("operation", "조치 내용", operation.value, operation.source)
		addFact("reason", "지정 사유", reason.value, reason.source)
		addFact("action", "사용자 확인", firstBriefValue(action.value, caution.value), firstBriefValue(action.source, caution.source))
	case "MAINTENANCE":
		if periodAvailable {
			addFact("period", "점검 시간", periodText, "SCHEDULE")
		}
		addFact("scope", "영향 서비스", firstBriefValue(scope.value, target.value), firstBriefValue(scope.source, target.source))
		addFact("status", "이용 제한", firstBriefValue(operation.value, statusValue.value), firstBriefValue(operation.source, statusValue.source))
		addFact("reason", "점검 내용", firstBriefValue(reason.value, change.value), firstBriefValue(reason.source, change.source))
		addFact("action", "사용자 조치", action.value, action.source)
	case "INCIDENT":
		if periodAvailable {
			addFact("period", "발생·복구 시간", periodText, "SCHEDULE")
		}
		addFact("scope", "영향 범위", firstBriefValue(scope.value, target.value), firstBriefValue(scope.source, target.source))
		addFact("asset", "관련 자산", assetValue, assetSource)
		addFact("status", "현재 상태", statusValue.value, statusValue.source)
		addFact("reason", "사고·장애 내용", firstBriefValue(reason.value, operation.value), firstBriefValue(reason.source, operation.source))
		addFact("action", "사용자 조치", firstBriefValue(action.value, caution.value), firstBriefValue(action.source, caution.source))
	case "POLICY":
		if periodAvailable {
			addFact("period", "시행일", periodText, "SCHEDULE")
		}
		addFact("target", "적용 대상", firstBriefValue(target.value, scope.value), firstBriefValue(target.source, scope.source))
		addFact("change", "변경 내용", firstBriefValue(change.value, operation.value), firstBriefValue(change.source, operation.source))
		addFact("action", "사용자 조치", action.value, action.source)
		addFact("caution", "확인 사항", caution.value, caution.source)
	case "ASSET_OPERATION":
		if periodAvailable {
			addFact("period", "처리 일정", periodText, "SCHEDULE")
		}
		addFact("asset", "대상 자산", assetValue, assetSource)
		addFact("network", "대상 네트워크", network.value, network.source)
		addFact("operation", "주요 내용", firstBriefValue(operation.value, change.value, reason.value), firstBriefValue(operation.source, change.source, reason.source))
		addFact("action", "사용자 확인", firstBriefValue(action.value, caution.value), firstBriefValue(action.source, caution.source))
	default:
		if periodAvailable {
			addFact("period", "적용 일정", periodText, "SCHEDULE")
		}
		addFact("scope", "적용 대상", firstBriefValue(scope.value, target.value), firstBriefValue(scope.source, target.source))
		addFact("change", "변경·안내 내용", firstBriefValue(change.value, statusValue.value, operation.value), firstBriefValue(change.source, statusValue.source, operation.source))
		addFact("reason", "배경·사유", reason.value, reason.source)
		addFact("action", "사용자 확인", firstBriefValue(action.value, caution.value), firstBriefValue(action.source, caution.source))
	}
	for _, source := range []struct{ content, name string }{{primaryFactText, detailSource}, {imageText, "OCR"}} {
		if source.content == "" {
			continue
		}
		for _, fact := range briefDynamicLabeledFacts(source.content) {
			addFact(briefFactKey(fact[0]), fact[0], fact[1], source.name)
		}
	}
	if len(lines) == 0 {
		addFact("headline", "핵심 내용", headline, headlineSource)
	}
	status := item.AnalysisStatus
	if status == "" {
		status = "DETAIL_PENDING"
	}
	return Brief{
		FactSchema: "DYNAMIC_V1", Kind: kind, DetailAvailable: detailAvailable, DetailSource: detailSource, AnalysisStatus: status,
		Headline: headline, HeadlineSource: headlineSource, OCRUsed: headlineSource == "OCR" || briefLinesUseSource(lines, "OCR"), OCRBlockedByConflict: item.ImageText != "" && imageConsistency.Status != "MATCHED",
		PeriodStartTSMS: optionalBriefInt(periodStart), PeriodEndTSMS: optionalBriefInt(periodEnd),
		PeriodPrecision: precision, Target: optionalBriefString(target.value), Reward: optionalBriefString(reward.value),
		Operation: optionalBriefString(operation.value), Assets: assets, Schedules: schedules,
		TimelineSchedules: briefTimelineSchedules(category, schedules), Lines: lines,
	}
}

func appendNewBriefSchedules(stored, inferred []Schedule) []Schedule {
	known := make(map[[3]string]bool, len(stored))
	for _, schedule := range stored {
		known[[3]string{intString(schedule.StartTSMS), intString(schedule.EndTSMS), schedule.Kind}] = true
	}
	for _, schedule := range inferred {
		key := [3]string{intString(schedule.StartTSMS), intString(schedule.EndTSMS), schedule.Kind}
		if !known[key] {
			stored = append(stored, schedule)
		}
	}
	return stored
}

func briefKind(category string, riskTags map[string]bool, content string) string {
	eventWords := briefEventPattern.MatchString(strings.ToLower(content))
	switch {
	case category == "CAUTION":
		return "CAUTION"
	case (category == "DEPOSIT" || category == "WITHDRAWAL" || category == "DEPOSIT_WITHDRAWAL") && !eventWords:
		return "DEPOSIT_WITHDRAWAL"
	case category == "DELISTING" || (category == "LISTING" || category == "QUOTE_MARKET_CHANGE") && !eventWords:
		return "LISTING_STATUS"
	case isEventCategory(category):
		return "EVENT"
	case category == "MARKET_ADDITION" && !eventWords:
		return "LISTING_STATUS"
	case riskTags["CAUTION"] || containsAnyFold(content, []string{"유의종목", "투자유의", "거래유의", "market warning", "caution"}):
		return "CAUTION"
	case !eventWords && containsAnyFold(content, []string{"입출금", "입금 중단", "출금 중단", "deposit suspended", "withdrawal suspended"}):
		return "DEPOSIT_WITHDRAWAL"
	case riskTags["EVENT"] || eventWords:
		return "EVENT"
	default:
		if category == "" {
			return "NOTICE"
		}
		return category
	}
}

func briefDynamicKind(category string, riskTags map[string]bool, content string) string {
	lowered := strings.ToLower(content)
	switch {
	case category == "LISTING" || category == "DELISTING" || category == "MARKET_ADDITION" || category == "QUOTE_MARKET_CHANGE":
		return "LISTING_STATUS"
	case category == "DEPOSIT" || category == "WITHDRAWAL" || category == "DEPOSIT_WITHDRAWAL":
		return "DEPOSIT_WITHDRAWAL"
	case category == "CAUTION" || riskTags["CAUTION"] || briefCautionKindPattern.MatchString(lowered):
		return "CAUTION"
	case briefTransferKindPattern.MatchString(lowered):
		return "DEPOSIT_WITHDRAWAL"
	case category == "SECURITY" || category == "WALLET" || category == "NETWORK" || category == "NETWORK_OUTAGE" || category == "SYSTEM" || briefIncidentKindPattern.MatchString(lowered):
		return "INCIDENT"
	case category == "MAINTENANCE" || briefMaintenanceKindPattern.MatchString(lowered):
		return "MAINTENANCE"
	case category == "NETWORK_UPGRADE" || category == "TOKEN_SWAP" || category == "REBRAND" || category == "SNAPSHOT":
		return "ASSET_OPERATION"
	case category == "EVENT" || category == "AIRDROP" || riskTags["EVENT"]:
		// Explicit source metadata is stronger than legal boilerplate in the body.
		// Event footers routinely mention terms, fees, privacy, and policies.
		return "EVENT"
	case category == "POLICY" || category == "FEE_POLICY" || category == "TERMS" || category == "PRIVACY" || briefPolicyKindPattern.MatchString(lowered):
		return "POLICY"
	case briefEventKindPattern.MatchString(lowered):
		return "EVENT"
	case category == "UPDATE" || category == "GUIDE" || category == "NOTICE" || category == "ANNOUNCEMENT" || category == "신규서비스" || category == "안내" || category == "업데이트":
		return "SERVICE_UPDATE"
	default:
		return "GENERAL"
	}
}

func briefOperationKeywords(kind string) []string {
	switch kind {
	case "CAUTION":
		return []string{"유의종목", "투자유의", "거래유의", "market warning", "caution"}
	case "DEPOSIT_WITHDRAWAL":
		return []string{"입출금", "입금", "출금", "deposit", "withdraw", "suspend", "resume"}
	case "LISTING_STATUS":
		return []string{"거래지원", "상장", "마켓 추가", "listing", "delist"}
	case "MAINTENANCE":
		return []string{"점검", "이용 제한", "중단", "maintenance", "unavailable"}
	case "INCIDENT":
		return []string{"장애", "오류", "보안", "복구", "incident", "outage", "restored"}
	case "ASSET_OPERATION":
		return []string{"업그레이드", "스왑", "리브랜딩", "스냅샷", "migration", "swap"}
	default:
		return []string{"변경", "적용", "지원", "update", "change"}
	}
}

func briefFactValue(content string, patterns []*regexp.Regexp, keywords []string) string {
	if len(patterns) > 0 {
		if value := briefLabeledValue(content, patterns); value != "" {
			return value
		}
	}
	if len(keywords) > 0 {
		return briefMatchingLine(content, keywords)
	}
	return ""
}

func briefScope(content string) string {
	return briefFactValue(content, briefScopePatterns, []string{"영향 범위", "이용 제한", "대상 서비스", "점검 대상", "affected service", "service unavailable", "temporarily unavailable"})
}

func briefAction(content string) string {
	if value := briefFactValue(content, briefActionPatterns, nil); value != "" {
		return value
	}
	if value := briefStepSummary(content); value != "" {
		return value
	}
	return briefMatchingLine(content, []string{"참여 방법", "신청 방법", "필요 조치", "조치 사항", "action required"})
}

func briefReason(content string) string {
	return briefFactValue(content, briefReasonPatterns, []string{"점검 내용", "장애 원인", "변경 사유", "maintenance details"})
}

func briefStatus(content string) string {
	return briefFactValue(content, briefStatusPatterns, []string{"정상화", "복구 완료", "서비스 재개", "이용 가능", "이용 불가", "temporarily unavailable", "service restored", "resolved"})
}

func briefChange(content string) string {
	return briefFactValue(content, briefChangePatterns, []string{"변경 내용", "주요 변경", "업데이트 내용", "수수료 변경", "정책 변경", "what is changing", "update details"})
}

func briefMarket(content string) string {
	if value := briefFactValue(content, briefMarketPatterns, nil); value != "" {
		return value
	}
	result := []string{}
	for _, value := range briefMarketValuePattern.FindAllString(content, -1) {
		value = strings.ToUpper(value)
		result = appendUniqueBrief(result, value)
		if len(result) == 8 {
			break
		}
	}
	return strings.Join(result, ", ")
}

func briefNetwork(content string) string {
	return briefFactValue(content, briefNetworkPatterns, []string{"네트워크 전환", "네트워크 업그레이드", "network migration"})
}

func briefCaution(content string) string {
	labeled := briefLabeledValue(content, briefCautionPatterns)
	matching := briefMatchingLine(content, []string{"유의사항", "주의사항", "제외", "제한", "not eligible", "warning"})
	if labeled != "" && matching != "" && !strings.Contains(matching, labeled) {
		return briefValue(matching + " · " + labeled)
	}
	return firstBriefValue(labeled, matching)
}

func briefFirstMeaningfulLine(content, title string) string {
	titleValue := briefValue(title)
	best, bestScore := "", -1
	for _, line := range briefLines(content) {
		line = briefValue(line)
		if len([]rune(line)) < 6 || briefIgnoredLinePattern.MatchString(line) || titleValue != "" && line == titleValue {
			continue
		}
		score := 0
		if briefActionLinePattern.MatchString(line) {
			score += 3
		}
		if briefNumberLinePattern.MatchString(line) {
			score++
		}
		if score > bestScore {
			best, bestScore = line, score
		}
	}
	if best != "" {
		return best
	}
	return titleValue
}

func briefDynamicLabeledFacts(content string) [][2]string {
	ignored := map[string]bool{"공지": true, "공지사항": true, "안내": true, "안내사항": true, "내용": true, "상세내용": true, "참고": true, "목차": true, "notice": true, "details": true, "description": true}
	result := make([][2]string, 0)
	for _, line := range strings.Split(PlainText(content), "\n") {
		match := briefDynamicFactPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		label := strings.Trim(strings.TrimSpace(briefWhitespacePattern.ReplaceAllString(match[1], " ")), " -")
		value := briefValue(match[2])
		if value == "" || ignored[strings.ReplaceAll(strings.ToLower(label), " ", "")] {
			continue
		}
		pair := [2]string{truncateRunes(label, 28), value}
		found := false
		for _, existing := range result {
			if existing == pair {
				found = true
				break
			}
		}
		if !found {
			result = append(result, pair)
		}
	}
	return result
}

func briefFactKey(label string) string {
	normalized := briefFactKeyPattern.ReplaceAllString(strings.ToLower(label), "")
	aliases := []struct {
		key   string
		words []string
	}{
		{"period", []string{"기간", "일정", "일시", "시행일", "적용일", "시각"}},
		{"target", []string{"대상자", "참여대상", "적용대상"}},
		{"asset", []string{"코인", "자산", "종목"}},
		{"reward", []string{"상금", "보상", "혜택", "리워드"}},
		{"market", []string{"마켓", "페어"}},
		{"network", []string{"네트워크", "체인"}},
		{"action", []string{"방법", "사용자조치", "필요조치"}},
		{"caution", []string{"유의", "주의", "제외", "제한"}},
		{"status", []string{"상태", "복구"}},
		{"change", []string{"변경내용", "업데이트내용", "개편내용"}},
		{"reason", []string{"사유", "원인"}},
	}
	for _, alias := range aliases {
		for _, word := range alias.words {
			if strings.Contains(normalized, word) {
				return alias.key
			}
		}
	}
	return "custom:" + normalized
}

func anyBriefAssetInText(assets []string, content string) bool {
	for _, asset := range assets {
		if strings.Contains(content, asset) {
			return true
		}
	}
	return false
}

func briefLinesUseSource(lines []BriefLine, source string) bool {
	for _, line := range lines {
		if line.Source == source {
			return true
		}
	}
	return false
}

func briefAssets(existing []string, content string, filterEventRewards bool) []string {
	candidates := make([]string, 0, len(existing)+4)
	for _, asset := range existing {
		asset = strings.ToUpper(strings.TrimSpace(asset))
		if asset != "" {
			candidates = appendUniqueBrief(candidates, asset)
		}
	}
	pairs := briefPairPattern.FindAllStringSubmatch(content, -1)
	for _, match := range pairs {
		asset := strings.ToUpper(match[1])
		if asset != "KRW" {
			candidates = appendUniqueBrief(candidates, asset)
		}
	}
	if len(pairs) == 0 {
		bodyAssets := briefBodyAssets(content)
		if filterEventRewards {
			bodyAssets = briefEventBodyAssets(content)
		}
		for _, asset := range bodyAssets {
			candidates = appendUniqueBrief(candidates, asset)
		}
	}
	rewardQuotes := map[string]bool{"USDT": true, "USDC": true, "USDE": true, "USDS": true, "USD1": true, "KRW": true, "BTC": true, "ETH": true}
	lines := briefLines(content)
	result := make([]string, 0, len(candidates))
	for _, asset := range candidates {
		rewardOnly := false
		if filterEventRewards && rewardQuotes[asset] {
			for _, line := range lines {
				if strings.Contains(strings.ToUpper(line), asset) && containsAnyFold(line, []string{"상금", "보상", "리워드", "혜택", "지급", "테더", "reward", "prize", "bonus"}) {
					rewardOnly = true
					break
				}
			}
		}
		if !rewardOnly {
			result = append(result, asset)
		}
	}
	return result
}

func briefBodyAssets(content string) []string {
	ignored := map[string]bool{"AI": true, "API": true, "FAQ": true, "HTML": true, "ID": true, "PDF": true, "STEP": true, "URL": true, "UTC": true, "KST": true, "KRW": true, "USDT": true, "USDC": true, "USDE": true, "USDS": true, "USD1": true, "KB": true, "NH": true, "IBK": true, "KYC": true, "DAXA": true, "IDX": true, "SCTIDXC01": true, "TWAP": true, "UPBIT": true}
	result := []string{}
	for _, line := range strings.Split(content, "\n") {
		if !containsAnyFold(line, []string{"거래", "입금", "출금", "보유", "에어드랍", "지급", "이벤트", "종목", "상장", "지원", "airdrop", "trading", "deposit", "withdraw", "holder", "holding", "event", "reward", "distribution"}) {
			continue
		}
		for _, match := range briefAssetPattern.FindAllStringSubmatch(line, -1) {
			asset := strings.ToUpper(match[1])
			if !ignored[asset] {
				result = appendUniqueBrief(result, asset)
			}
		}
	}
	return result
}

func briefEventBodyAssets(content string) []string {
	lines := make([]string, 0)
	for _, line := range briefLines(content) {
		if containsAnyFold(line, []string{"상금", "보상", "리워드", "혜택", "지급", "이용권", "쿠폰", "reward", "prize", "bonus"}) {
			continue
		}
		if containsAnyFold(line, []string{"대상", "보유", "거래", "종목", "마켓", "페어", "eligible", "holder", "trading", "market"}) {
			lines = append(lines, line)
		}
	}
	return briefBodyAssets(strings.Join(lines, "\n"))
}

func briefScheduleCandidates(schedules []Schedule, kind string) []Schedule {
	preferred := map[string]map[string]bool{
		"EVENT":              {"EVENT": true, "AIRDROP": true, "SNAPSHOT": true},
		"CAUTION":            {"CAUTION": true},
		"DEPOSIT_WITHDRAWAL": {"DEPOSIT": true, "WITHDRAWAL": true, "DEPOSIT_WITHDRAWAL": true},
		"LISTING_STATUS":     {"TRADING": true, "LISTING": true, "DELISTING": true, "WITHDRAWAL": true},
	}[kind]
	selected := make([]Schedule, 0, len(schedules))
	for _, schedule := range schedules {
		if preferred[strings.ToUpper(schedule.Kind)] {
			selected = append(selected, schedule)
		}
	}
	if len(selected) == 0 {
		selected = append(selected, schedules...)
	}
	complete := make([]Schedule, 0, len(selected))
	for _, schedule := range selected {
		if schedule.StartTSMS > 0 && schedule.EndTSMS > schedule.StartTSMS {
			complete = append(complete, schedule)
		}
	}
	if len(complete) > 0 {
		if kind == "EVENT" {
			for _, schedule := range selected {
				if schedule.StartTSMS == 0 && schedule.EndTSMS > 0 {
					complete = append(complete, schedule)
				}
			}
		}
		selected = complete
	}
	explicit := make([]Schedule, 0, len(selected))
	for _, schedule := range selected {
		if containsAnyFold(schedule.Evidence, []string{"기간", "참여 기간", "지급 기준 기간", "event period", "promotion period", "reward period", "from", "until"}) {
			explicit = append(explicit, schedule)
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	return selected
}

func briefPeriod(schedules []Schedule) (int64, int64) {
	start, end := int64(0), int64(0)
	for _, schedule := range schedules {
		if schedule.StartTSMS > 0 && (start == 0 || schedule.StartTSMS < start) {
			start = schedule.StartTSMS
		}
		if schedule.EndTSMS > end {
			end = schedule.EndTSMS
		}
	}
	return start, end
}

func briefPrecision(schedules []Schedule) string {
	result := "SECOND"
	for _, schedule := range schedules {
		switch strings.ToUpper(schedule.Precision) {
		case "DAY":
			return "DAY"
		case "RECURRING":
			result = "RECURRING"
		case "HOUR":
			if result != "RECURRING" {
				result = "HOUR"
			}
		case "MINUTE":
			if result == "SECOND" {
				result = "MINUTE"
			}
		}
	}
	return result
}

func briefTimelineSchedules(category string, schedules []Schedule) []Schedule {
	listing := category == "LISTING" || category == "MARKET_ADDITION"
	grouped := make(map[string]Schedule)
	for _, schedule := range schedules {
		if strings.ToUpper(schedule.Precision) == "RECURRING" || schedule.StartTSMS <= 0 {
			continue
		}
		if listing {
			if schedule.EndTSMS > 0 && schedule.EndTSMS < schedule.StartTSMS {
				continue
			}
		} else if schedule.EndTSMS <= schedule.StartTSMS {
			continue
		}
		location := locationForSchedule(schedule.Timezone)
		key := time.UnixMilli(schedule.StartTSMS).In(location).Format("2006-01-02")
		current, found := grouped[key]
		if !found {
			grouped[key] = schedule
			continue
		}
		current.StartTSMS = min(current.StartTSMS, schedule.StartTSMS)
		current.EndTSMS = max(current.EndTSMS, schedule.EndTSMS)
		if current.Evidence == "" && schedule.Evidence != "" {
			current.Evidence = schedule.Evidence
		}
		grouped[key] = current
	}
	result := make([]Schedule, 0, len(grouped))
	for _, schedule := range grouped {
		result = append(result, schedule)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartTSMS == result[j].StartTSMS {
			return result[i].EndTSMS < result[j].EndTSMS
		}
		return result[i].StartTSMS < result[j].StartTSMS
	})
	return result
}

func briefPeriodText(start, end int64, precision, recurrence string) string {
	if recurrence != "" {
		return recurrence
	}
	format := func(value int64) string {
		if value <= 0 {
			return "시작·종료 시각 정보 없음"
		}
		layout := "2006-01-02 15:04"
		if precision == "DAY" {
			layout = "2006-01-02"
		}
		return time.UnixMilli(value).In(seoulNoticeLocation()).Format(layout)
	}
	switch {
	case start > 0 && end > 0:
		return format(start) + " ~ " + format(end)
	case start > 0:
		return format(start) + " ~ 종료 시각 정보 없음"
	case end > 0:
		return "시작 시각 정보 없음 ~ " + format(end)
	default:
		return "기간·조치 시각 정보 없음"
	}
}

func briefRecurrence(content string, schedules []Schedule) string {
	for _, schedule := range schedules {
		if strings.ToUpper(schedule.Precision) == "RECURRING" {
			if value := briefValue(schedule.Evidence); value != "" {
				return value
			}
		}
	}
	return briefMatchingLine(content, []string{"매주", "매일", "매월", "weekly", "daily", "monthly"})
}

func briefTarget(content string) string {
	if value := briefLabeledValue(content, briefTargetPatterns); value != "" {
		return value
	}
	if value := briefValueAfterHeading(content, "이벤트 대상", "참여 대상", "참가 대상"); value != "" {
		return value
	}
	best, score := "", 0
	for _, line := range briefLines(content) {
		lowered := strings.ToLower(line)
		if !containsAnyFold(lowered, []string{"대상", "eligible", "holder", "보유자", "신청할 수"}) || containsAnyFold(lowered, []string{"제외", "제한", "지급되지", "불가", "not eligible"}) || !containsAnyFold(lowered, []string{"고객", "회원", "보유", "홀더", "계정", "account", "holder"}) {
			continue
		}
		candidateScore := 1
		if strings.Contains(lowered, "대상") || strings.Contains(lowered, "eligible") {
			candidateScore = 2
		}
		if strings.Contains(lowered, "신청할 수") {
			candidateScore++
		}
		if candidateScore > score {
			best, score = briefValue(line), candidateScore
		}
	}
	return best
}

func briefReward(content string) string {
	if value := briefLabeledValue(content, briefRewardPatterns); value != "" && briefRewardValuePattern.MatchString(value) {
		return value
	}
	best, bestScore := "", -1
	for _, line := range briefLines(content) {
		if !containsAnyFold(line, []string{"상금", "보상", "리워드", "혜택", "reward", "prize", "bonus"}) || !briefRewardValuePattern.MatchString(line) {
			continue
		}
		score := 2
		if briefPrizePattern.MatchString(line) {
			score += 3
		}
		if briefMaximumPattern.MatchString(line) {
			score++
		}
		if score > bestScore {
			best, bestScore = briefValue(line), score
		}
	}
	return best
}

func briefEventRewards(content string) string {
	values := make([]string, 0, 3)
	for _, line := range briefLines(content) {
		if briefSectionHeading(line) || !briefRewardValuePattern.MatchString(line) ||
			!containsAnyFold(line, []string{"이용권", "할인 쿠폰", "비트코인", "BTC", "iPhone", "상금", "보상", "리워드", "reward", "prize", "bonus"}) ||
			!containsAnyFold(line, []string{"드려", "드립니다", "지급", "선정", "증정", "reward", "prize", "bonus"}) {
			continue
		}
		values = append(values, briefValue(line))
		if len(values) == 3 {
			break
		}
	}
	if len(values) == 0 {
		return briefReward(content)
	}
	return strings.Join(values, " · ")
}

func briefEventConditions(content string) string {
	type candidate struct {
		value string
		score int
	}
	candidates := make([]candidate, 0, 4)
	for _, line := range briefLines(content) {
		if briefSectionHeading(line) || !containsAnyFold(line, []string{
			"유지해야", "유지한 회원", "거래 조건", "30일 이내", "공개 상태를 유지", "선정 대상에서 제외", "혜택 상당액은 소멸",
		}) {
			continue
		}
		score := 1
		if containsAnyFold(line, []string{"거래 조건", "30일 이내", "혜택 상당액은 소멸"}) {
			score += 4
		}
		if containsAnyFold(line, []string{"기준일", "유지해야", "유지한 회원"}) {
			score += 3
		}
		candidates = append(candidates, candidate{value: briefValue(line), score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	values := make([]string, 0, 2)
	for _, item := range candidates {
		values = append(values, item.value)
		if len(values) == 2 {
			break
		}
	}
	return strings.Join(values, " · ")
}

func briefStepSummary(content string) string {
	values := make([]string, 0, 4)
	for _, line := range briefLines(content) {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "STEP ") {
			continue
		}
		values = append(values, briefValue(line))
		if len(values) == 4 {
			break
		}
	}
	return strings.Join(values, " · ")
}

func briefValueAfterHeading(content string, headings ...string) string {
	lines := briefLines(content)
	for index, line := range lines {
		heading := normalizedBriefHeading(line)
		matched := false
		for _, candidate := range headings {
			if strings.EqualFold(heading, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for next := index + 1; next < len(lines); next++ {
			if briefSectionHeading(lines[next]) {
				break
			}
			if value := briefValue(lines[next]); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizedBriefHeading(value string) string {
	return strings.TrimSpace(strings.TrimLeft(briefValue(value), "■●◆▶▷•*-# "))
}

func briefSectionHeading(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, ":") || strings.Contains(trimmed, "：") {
		return false
	}
	normalized := normalizedBriefHeading(trimmed)
	return normalized != trimmed && len([]rune(normalized)) <= 32
}

func briefOperation(kind, content string) string {
	switch kind {
	case "CAUTION":
		return briefMatchingLine(content, []string{"유의종목", "투자유의", "거래유의", "market warning", "caution"})
	case "DEPOSIT_WITHDRAWAL":
		return briefMatchingLine(content, []string{"입출금", "입금", "출금", "deposit", "withdraw", "suspend", "resume"})
	case "LISTING_STATUS":
		return briefMatchingLine(content, []string{"거래지원", "상장", "거래지원 종료", "listing", "delist"})
	case "EVENT":
		if value := briefMatchingLine(content, []string{"유의", "주의", "제외", "warning"}); value != "" {
			return value
		}
		return briefMatchingLine(content, []string{"입출금", "입금", "출금", "withdraw"})
	default:
		return ""
	}
}

func briefMatchingLine(content string, keywords []string) string {
	for _, line := range briefLines(content) {
		lowered := strings.ToLower(line)
		if briefSectionHeading(line) || lowered == "유의사항" || lowered == "주의사항" || lowered == "유의" || lowered == "주의" || lowered == "caution" || lowered == "warning" {
			continue
		}
		if containsAnyFold(lowered, keywords) {
			return briefValue(line)
		}
	}
	return ""
}

func briefLabeledValue(content string, patterns []*regexp.Regexp) string {
	for _, line := range briefLines(content) {
		for _, pattern := range patterns {
			match := pattern.FindStringSubmatch(line)
			if len(match) > 1 {
				if value := briefValue(match[1]); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func briefMarketScope(content string) string {
	for _, line := range briefLines(content) {
		match := briefMarketScopePattern.FindStringSubmatch(line)
		if len(match) > 2 {
			market := firstBriefValue(match[1], match[2])
			if market != "" {
				return strings.ToUpper(market) + " 마켓 전 종목"
			}
		}
	}
	return ""
}

func briefValue(value string) string {
	const tildePlaceholder = "～"
	value = strings.ReplaceAll(value, "~", tildePlaceholder)
	value = briefMarkdownLinkPattern.ReplaceAllString(value, "$1")
	value = strings.ReplaceAll(PlainText(value), tildePlaceholder, "~")
	value = briefWhitespacePattern.ReplaceAllString(value, " ")
	value = strings.Trim(value, " -:：")
	return truncateRunes(value, briefValueLimit)
}

func briefLines(content string) []string {
	raw := strings.Split(PlainText(content), "\n")
	result := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(briefWhitespacePattern.ReplaceAllString(line, " "))
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func joinBriefText(values ...string) string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return strings.Join(result, "\n")
}

func appendUniqueBrief(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func optionalBriefString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalBriefInt(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func firstBriefValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func briefAbsentValue(value string, available bool, unknown string) string {
	if value != "" {
		return value
	}
	if available {
		return "해당 내용 없음"
	}
	return unknown
}

func briefOperationValue(value string, available bool, unknown string) string {
	return briefAbsentValue(value, available, unknown)
}

func intString(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := [20]byte{}
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buffer[index] = '-'
	}
	return string(buffer[index:])
}
