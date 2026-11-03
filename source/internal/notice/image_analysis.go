package notice

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ImageProfile struct {
	SemanticType   string   `json:"semantic_type"`
	Signals        []string `json:"signals"`
	Dates          []string `json:"dates,omitempty"`
	Times          []string `json:"times,omitempty"`
	Assets         []string `json:"assets,omitempty"`
	Values         []string `json:"values,omitempty"`
	Topics         []string `json:"topics,omitempty"`
	KeyLines       []string `json:"key_lines,omitempty"`
	Interpretation string   `json:"interpretation"`
}

type ImageConsistency struct {
	Status            string   `json:"status"`
	SemanticTypes     []string `json:"semantic_types"`
	MatchedSignals    []string `json:"matched_signals"`
	ConflictSignals   []string `json:"conflict_signals"`
	UnverifiedSignals []string `json:"unverified_signals,omitempty"`
	Reason            string   `json:"reason"`
}

func (profile ImageProfile) MarshalJSON() ([]byte, error) {
	payload := map[string]any{
		"semantic_type": profile.SemanticType, "signals": profile.Signals,
		"interpretation": profile.Interpretation,
	}
	if profile.SemanticType != "UNKNOWN" {
		payload["dates"] = nonNilStrings(profile.Dates)
		if len(profile.Times) > 0 {
			payload["times"] = profile.Times
		}
		payload["assets"] = nonNilStrings(profile.Assets)
		payload["values"] = nonNilStrings(profile.Values)
		payload["topics"] = nonNilStrings(profile.Topics)
		payload["key_lines"] = nonNilStrings(profile.KeyLines)
	}
	return json.Marshal(payload)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

var (
	imageValuePattern       = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9./-])((?:\d{1,3}(?:,\d{3})+|\d+(?:\.\d+)?))[ \t]*(만|억)?[ \t]*(원|%|[A-Za-z][A-Za-z0-9]{1,11})`)
	imageTimePattern        = regexp.MustCompile(`\b(?:[01]?\d|2[0-3]):[0-5]\d(?::[0-5]\d)?\b`)
	imageAssetPattern       = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,11})\b`)
	imageAnchorPattern      = regexp.MustCompile(`[가-힣]{2,}|[A-Za-z][A-Za-z0-9_-]{2,}`)
	imageDatePattern        = regexp.MustCompile(`(?i)(?:\d{4}[./-]\s*)?(\d{1,2})[./-]\s*(\d{1,2})`)
	imageKoreanDatePattern  = regexp.MustCompile(`(?i)(?:20\d{2}\s*년\s*)?(\d{1,2})\s*월\s*(\d{1,2})\s*일`)
	imageEnglishDatePattern = regexp.MustCompile(`(?i)\b(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+20\d{2}`)
)

func NoticeImageSemanticProfile(content string, knownAssets []string) ImageProfile {
	plain := PlainText(content)
	if plain == "" {
		return ImageProfile{SemanticType: "UNKNOWN", Signals: []string{}, Interpretation: "OCR 결과가 없어 이미지 의미를 분류하지 못했습니다."}
	}
	dates := imageDateSignals(plain)
	times := imageTimeSignals(plain)
	values, percentages, amounts := []string{}, []string{}, []string{}
	for _, value := range imageValueSignals(plain, knownAssets) {
		values = appendUniqueBrief(values, value)
		if strings.HasSuffix(value, "%") {
			percentages = append(percentages, value)
		} else {
			amounts = append(amounts, value)
		}
	}
	assets := imageAssetSignals(plain, knownAssets)
	lowered := strings.ToLower(plain)
	hasTrend := containsAnyFold(lowered, []string{"추이", "변화", "증가", "감소", "상승", "하락", "점유율", "거래량", "그래프", "차트", "trend", "change", "increase", "decrease", "share", "volume", "chart"})
	hasTable := containsAnyFold(lowered, []string{"표", "순위", "항목", "거래소", "목록", "table", "rank", "exchange", "item"})
	hasEvent := containsAnyFold(lowered, []string{"이벤트", "프로모션", "상금", "보상", "리워드", "무료", "event", "promotion", "prize", "reward", "free"})
	hasMaintenance := containsAnyFold(lowered, []string{"점검", "정기점검", "서비스 중단", "이용 제한", "maintenance", "scheduled maintenance", "temporarily unavailable"})
	hasIncident := containsAnyFold(lowered, []string{"장애", "오류", "접속 불가", "보안 사고", "복구", "incident", "outage", "service disruption", "system error", "restored"})
	hasPolicy := containsAnyFold(lowered, []string{"약관", "정책 변경", "개인정보", "수수료 변경", "terms", "policy update", "privacy policy", "fee change"})
	hasListing := containsAnyFold(lowered, []string{"신규 거래지원", "거래지원 종료", "상장", "마켓 추가", "new listing", "will list", "delist"})
	hasTransfer := containsAnyFold(lowered, []string{"입출금", "입금 중단", "출금 중단", "입금 재개", "출금 재개", "deposit suspended", "withdrawal suspended", "deposit resumed", "withdrawal resumed"})
	nonEmptyLines := briefLines(plain)
	topics := []string{}
	for _, topic := range []struct {
		name    string
		present bool
	}{{"EVENT", hasEvent}, {"MAINTENANCE", hasMaintenance}, {"INCIDENT", hasIncident}, {"POLICY", hasPolicy}, {"LISTING", hasListing}, {"DEPOSIT_WITHDRAWAL", hasTransfer}} {
		if topic.present {
			topics = append(topics, topic.name)
		}
	}
	signals := []string{}
	if len(dates) > 0 {
		signals = append(signals, "date")
	}
	if len(times) > 0 {
		signals = append(signals, "time")
	}
	if len(percentages) > 0 {
		signals = append(signals, "percentage")
	}
	if len(amounts) > 0 {
		signals = append(signals, "amount")
	}
	if len(assets) > 0 {
		signals = append(signals, "asset")
	}
	if hasTrend {
		signals = append(signals, "trend")
	}
	if hasTable || len(nonEmptyLines) >= 3 {
		signals = append(signals, "rows")
	}
	if hasEvent {
		signals = append(signals, "event")
	}
	if hasMaintenance {
		signals = append(signals, "maintenance")
	}
	if hasIncident {
		signals = append(signals, "incident")
	}
	if hasPolicy {
		signals = append(signals, "policy")
	}
	if hasListing {
		signals = append(signals, "listing")
	}
	if hasTransfer {
		signals = append(signals, "deposit_withdrawal")
	}
	keyLines := make([]string, 0, min(8, len(nonEmptyLines)))
	for _, line := range nonEmptyLines {
		if len(keyLines) == 8 {
			break
		}
		keyLines = append(keyLines, briefValue(line))
	}
	profile := ImageProfile{Signals: signals, Dates: dates, Times: times, Assets: assets, Values: values, Topics: topics, KeyLines: keyLines}
	switch {
	case hasTrend && (len(dates) > 0 || len(values) >= 2) || len(percentages) >= 2 && len(dates) > 0:
		profile.SemanticType = "CHART"
		profile.Interpretation = "추이·수치 신호가 있어 그래프/차트로 분류했습니다. OCR만으로 선의 방향·축 단위·원인은 확정하지 않습니다."
	case hasTable && (len(values) >= 2 || len(nonEmptyLines) >= 3) || len(values) >= 3 && len(nonEmptyLines) >= 3 && !hasTrend:
		profile.SemanticType = "TABLE"
		profile.Interpretation = "행·열형 항목 또는 수치 신호가 있어 표로 분류했습니다. OCR 열 정렬은 확인이 필요합니다."
	case hasIncident:
		profile.SemanticType = "INCIDENT_NOTICE"
		profile.Interpretation = "장애·보안·복구 관련 문구가 있는 공지 이미지입니다. 현재 상태는 원문과 함께 확인해야 합니다."
	case hasMaintenance:
		profile.SemanticType = "MAINTENANCE_NOTICE"
		profile.Interpretation = "점검 시간이나 서비스 제한을 알리는 공지 이미지로 분류했습니다."
	case hasPolicy:
		profile.SemanticType = "POLICY_NOTICE"
		profile.Interpretation = "약관·정책·수수료 변경을 알리는 공지 이미지로 분류했습니다."
	case hasListing || hasTransfer:
		profile.SemanticType = "SERVICE_NOTICE"
		profile.Interpretation = "거래지원 또는 입출금 상태를 알리는 공지 이미지로 분류했습니다."
	case hasEvent:
		profile.SemanticType = "BANNER"
		profile.Interpretation = "이벤트·혜택 문구 중심 이미지로 분류했습니다. 자산 이벤트인지 일반 프로모션인지는 문구로 구분합니다."
	case len(dates) > 0 || len(values) > 0 || len(assets) > 0:
		profile.SemanticType = "TEXT_WITH_DATA"
		profile.Interpretation = "문구와 날짜·수치·자산 신호가 함께 있는 이미지입니다."
	default:
		profile.SemanticType = "TEXT"
		profile.Interpretation = "텍스트 중심 이미지로 분류했습니다."
	}
	return profile
}

func VerifyImageConsistency(bodyText, imageText string, imageAnalysis []map[string]any, knownAssets []string) ImageConsistency {
	body, image := PlainText(bodyText), PlainText(imageText)
	if body == "" {
		return unverifiedImageConsistency("공지 본문이 없어 이미지와 비교하지 못했습니다.")
	}
	if image == "" {
		return unverifiedImageConsistency("이미지 OCR 결과가 없어 본문과 비교하지 못했습니다.")
	}
	bodyProfile := NoticeImageSemanticProfile(body, knownAssets)
	imageProfile := NoticeImageSemanticProfile(image, knownAssets)
	semanticTypes := []string{}
	for _, row := range imageAnalysis {
		value := strings.ToUpper(strings.TrimSpace(textValue(row["semantic_type"])))
		if value != "" && value != "UNKNOWN" {
			semanticTypes = appendUniqueBrief(semanticTypes, value)
		}
	}
	if len(semanticTypes) == 0 && imageProfile.SemanticType != "" {
		semanticTypes = append(semanticTypes, imageProfile.SemanticType)
	}
	sort.Strings(semanticTypes)
	matched, conflicts, unverified := []string{}, []string{}, []string{}
	compareImageSets("date", bodyProfile.Dates, imageProfile.Dates, true, &matched, &conflicts, &unverified)
	compareImageSets("time", bodyProfile.Times, imageProfile.Times, true, &matched, &conflicts, &unverified)
	strictAssets := len(bodyProfile.Assets) <= 6 && len(imageProfile.Assets) <= 6
	compareImageSets("asset", bodyProfile.Assets, imageProfile.Assets, strictAssets, &matched, &conflicts, &unverified)
	strictValues := !containsBriefValue(semanticTypes, "CHART") && !containsBriefValue(semanticTypes, "TABLE")
	compareImageSets("value", bodyProfile.Values, imageProfile.Values, strictValues, &matched, &conflicts, &unverified)
	if intersectionCount(imageAnchorSignals(body), imageAnchorSignals(image)) >= 2 {
		matched = appendUniqueBrief(matched, "text")
	}
	result := ImageConsistency{SemanticTypes: semanticTypes, MatchedSignals: matched, ConflictSignals: conflicts, UnverifiedSignals: unverified}
	switch {
	case len(conflicts) > 0:
		result.Status = "CONFLICT"
		result.Reason = "본문과 이미지의 " + readableImageSignals(conflicts) + " 신호가 서로 달라 원문 직접 확인이 필요합니다."
	case len(unverified) > 0:
		result.Status = "PARTIAL"
		result.Reason = "이미지의 " + readableImageSignals(unverified) + " 중 본문에서 같은 값을 확인하지 못한 항목은 확정 정보·일정 계산에 사용하지 않습니다. 원문 이미지를 직접 확인해야 합니다."
	case len(matched) > 0:
		result.Status = "MATCHED"
		result.Reason = "본문과 이미지에서 " + readableImageSignals(matched) + " 신호가 겹칩니다. OCR 오인식과 그래프 축 의미까지 확정한 결과는 아닙니다."
	default:
		result.Status = "PARTIAL"
		result.Reason = "OCR은 확보했지만 본문과 직접 대조할 구조화 신호가 부족해 부분 확인으로 남겼습니다."
	}
	return result
}

// VerifyItemImageConsistency always prefers the exact full official body.
// The display excerpt may be truncated before a numeric claim appears.
func VerifyItemImageConsistency(item Item) ImageConsistency {
	if !HasImageEvidence(item) {
		return ImageConsistency{
			Status: "NOT_APPLICABLE", SemanticTypes: []string{}, MatchedSignals: []string{},
			ConflictSignals: []string{}, UnverifiedSignals: []string{}, Reason: "",
		}
	}
	body := item.OriginalBodyText
	if strings.TrimSpace(body) == "" {
		body = item.BodyExcerpt
	}
	if strings.TrimSpace(body) == "" {
		body = item.Summary
	}
	if strings.TrimSpace(item.ImageText) == "" {
		return unverifiedImageConsistency(missingImageTextReason(item))
	}
	return VerifyImageConsistency(body, item.ImageText, item.ImageAnalysis, item.Assets)
}

func missingImageTextReason(item Item) string {
	if len(item.ImageAnalysis) == 0 &&
		strings.EqualFold(strings.TrimSpace(item.AnalysisStatus), "ANALYSIS_NOT_REQUIRED") {
		return "공지 본문이 있어 이미지 텍스트 분석 대상에서 제외되어 본문과 비교하지 않았습니다."
	}
	statuses := make(map[string]bool, len(item.ImageAnalysis))
	for _, row := range item.ImageAnalysis {
		statuses[strings.ToUpper(strings.TrimSpace(textValue(row["status"])))] = true
	}
	switch {
	case statuses["PENDING"]:
		return "이미지 텍스트 분석이 아직 끝나지 않아 본문과 비교하지 않았습니다."
	case statuses["UNSUPPORTED"] || statuses["OCR_UNAVAILABLE"]:
		return "지원되는 이미지 텍스트 분석기를 사용할 수 없어 본문과 비교하지 못했습니다. 원문 이미지를 직접 확인해 주세요."
	case statuses["DOWNLOAD_FAILED"] || statuses["DOWNLOAD_ERROR"]:
		return "이미지를 내려받지 못해 본문과 비교하지 못했습니다. 원문 이미지 링크를 확인해 주세요."
	case statuses["VISION_FAILED"] || statuses["OCR_ERROR"]:
		return "이미지의 텍스트를 인식하지 못해 본문과 비교하지 못했습니다. 원문 이미지를 직접 확인해 주세요."
	case statuses["NO_TEXT"]:
		return "이미지에서 비교할 텍스트를 찾지 못해 본문과 비교하지 않았습니다."
	default:
		return "이미지 텍스트 분석 결과가 없어 본문과 비교하지 못했습니다. 원문 이미지를 직접 확인해 주세요."
	}
}

// HasImageEvidence distinguishes a real stored notice image from the absence
// of an image. Callers must not present missing image evidence as a failed
// body/OCR verification.
func HasImageEvidence(item Item) bool {
	return len(item.ImageURLs) > 0 || strings.TrimSpace(item.ImageText) != "" || len(item.ImageAnalysis) > 0
}

func ImageConsistencyIfPresent(item Item) *ImageConsistency {
	if !HasImageEvidence(item) {
		return nil
	}
	value := VerifyItemImageConsistency(item)
	return &value
}

func imageDateSignals(content string) []string {
	values := []string{}
	add := func(month, day int) {
		parsed := time.Date(2000, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if parsed.Month() != time.Month(month) || parsed.Day() != day {
			return
		}
		values = appendUniqueBrief(values, parsed.Format("01-02"))
	}
	for _, match := range imageDatePattern.FindAllStringSubmatch(content, -1) {
		add(integerValue(match[1]), integerValue(match[2]))
	}
	for _, match := range imageKoreanDatePattern.FindAllStringSubmatch(content, -1) {
		add(integerValue(match[1]), integerValue(match[2]))
	}
	for _, match := range imageEnglishDatePattern.FindAllStringSubmatch(content, -1) {
		add(int(englishMonth[strings.ToLower(match[1])]), integerValue(match[2]))
	}
	sort.Strings(values)
	return values
}

func imageTimeSignals(content string) []string {
	values := imageTimePattern.FindAllString(content, -1)
	result := make([]string, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		hour, _ := strconv.Atoi(parts[0])
		value = strconv.Itoa(hour)
		if hour < 10 {
			value = "0" + value
		}
		value += ":" + parts[1]
		if len(parts) == 2 {
			value += ":00"
		} else {
			value += ":" + parts[2]
		}
		result = appendUniqueBrief(result, value)
	}
	sort.Strings(result)
	return result
}

func imageValueSignals(content string, knownAssets []string) []string {
	allowed := map[string]bool{
		"원": true, "%": true, "KRW": true, "USD": true, "USDT": true,
		"USDC": true, "USDE": true, "USDS": true, "USD1": true,
	}
	for _, asset := range knownAssets {
		if value := strings.ToUpper(strings.TrimSpace(asset)); value != "" {
			allowed[value] = true
		}
	}
	values := []string{}
	for _, match := range imageValuePattern.FindAllStringSubmatch(content, -1) {
		unit := strings.ToUpper(match[3])
		if match[3] == "원" {
			unit = "원"
		}
		if !allowed[unit] {
			continue
		}
		number, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
		if err != nil {
			continue
		}
		switch match[2] {
		case "만":
			number *= 10_000
		case "억":
			number *= 100_000_000
		}
		canonical := strconv.FormatFloat(number, 'f', -1, 64) + unit
		values = appendUniqueBrief(values, canonical)
	}
	sort.Strings(values)
	return values
}

func imageAssetSignals(content string, knownAssets []string) []string {
	known := make(map[string]bool, len(knownAssets))
	for _, asset := range knownAssets {
		known[strings.ToUpper(strings.TrimSpace(asset))] = true
	}
	ignored := map[string]bool{"API": true, "CHART": true, "DATE": true, "END": true, "EVENT": true, "FAQ": true, "FEE": true, "FREE": true, "HTML": true, "ID": true, "KST": true, "KRW": true, "MAKER": true, "PERIOD": true, "PDF": true, "PRIZE": true, "REWARD": true, "SHARE": true, "START": true, "TABLE": true, "TAKER": true, "TIME": true, "TOTAL": true, "TRADING": true, "URL": true, "UTC": true, "VOLUME": true, "UPBIT": true, "BITHUMB": true, "COINONE": true, "KORBIT": true, "GOPAX": true}
	result := []string{}
	for _, line := range strings.Split(content, "\n") {
		for _, match := range imageAssetPattern.FindAllStringSubmatch(line, -1) {
			asset := strings.ToUpper(match[1])
			if ignored[asset] || !known[asset] && !containsAnyFold(line, []string{"거래", "입금", "출금", "보유", "이벤트", "상금", "보상", "리워드", "지급", "코인", "종목", "상장", "지원", "에어드랍", "event", "reward", "coin", "listing", "trading", "deposit", "withdraw", "holder", "distribution"}) {
				continue
			}
			result = appendUniqueBrief(result, asset)
		}
	}
	return result
}

func imageAnchorSignals(content string) map[string]bool {
	ignored := map[string]bool{"공지": true, "안내": true, "이벤트": true, "기간": true, "대상": true, "내용": true, "확인": true, "시작": true, "종료": true, "참여": true, "제공": true, "notice": true, "guide": true, "event": true, "period": true, "target": true, "start": true, "end": true, "details": true}
	result := map[string]bool{}
	for _, value := range imageAnchorPattern.FindAllString(content, -1) {
		value = strings.ToLower(value)
		if !ignored[value] {
			result[value] = true
		}
	}
	return result
}

func compareImageSets(label string, left, right []string, strict bool, matched, conflicts, unverified *[]string) {
	if len(right) == 0 {
		return
	}
	if len(left) == 0 {
		*unverified = appendUniqueBrief(*unverified, label)
		return
	}
	seen := make(map[string]bool, len(left))
	for _, value := range left {
		seen[value] = true
	}
	matchedAny, unmatchedAny := false, false
	for _, value := range right {
		if seen[value] {
			matchedAny = true
		} else {
			unmatchedAny = true
		}
	}
	if matchedAny {
		*matched = appendUniqueBrief(*matched, label)
	}
	if unmatchedAny {
		if strict && !matchedAny {
			*conflicts = appendUniqueBrief(*conflicts, label)
		} else {
			*unverified = appendUniqueBrief(*unverified, label)
		}
	}
}

func intersectionCount(left, right map[string]bool) int {
	count := 0
	for value := range left {
		if right[value] {
			count++
		}
	}
	return count
}

func readableImageSignals(values []string) string {
	labels := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case "date":
			labels = append(labels, "날짜·기간")
		case "asset":
			labels = append(labels, "자산")
		case "value":
			labels = append(labels, "금액·비율")
		case "time":
			labels = append(labels, "시각")
		case "text":
			labels = append(labels, "본문 문구")
		default:
			labels = append(labels, value)
		}
	}
	return strings.Join(labels, ", ")
}

func unverifiedImageConsistency(reason string) ImageConsistency {
	return ImageConsistency{Status: "UNVERIFIED", SemanticTypes: []string{}, MatchedSignals: []string{}, ConflictSignals: []string{}, UnverifiedSignals: []string{}, Reason: reason}
}

func containsBriefValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
