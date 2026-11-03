package notice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var noticeSourceTypes = map[string]string{
	"binance": "official_page_json",
	"bybit":   "official_page", "okx": "official_page",
}

var noticeCategoryRules = []struct {
	category string
	words    []string
}{
	{"DELISTING", []string{"거래지원 종료", "거래 지원 종료", "거래지원종료", "상장폐지", "delist", "trading termination"}},
	// Promotion intent is more specific than a listing phrase in titles such as
	// "원화마켓 추가 기념 에어드랍 이벤트".
	{"AIRDROP", []string{"에어드랍", "airdrop"}},
	{"MARKET_ADDITION", []string{"원화마켓 추가", "원화 마켓 추가", "krw market addition", "add krw market"}},
	{"QUOTE_MARKET_CHANGE", []string{"기준통화 마켓 변경", "기준 통화 마켓 변경", "quote market change", "quote asset change", "change quote asset"}},
	{"LISTING", []string{"신규 거래지원", "거래지원 개시", "디지털 자산 추가", "마켓 추가", "마켓추가", "new listing", "will list", "adds trading pair"}},
	{"CAUTION", []string{"유의종목", "유의 종목", "유의촉구", "유의 촉구", "투자유의", "투자 유의", "거래유의", "거래 유의", "market warning", "caution"}},
	{"SECURITY", []string{"보안 사고", "보안 이슈", "해킹", "security incident", "security breach", "security issue", "hacked", "exploit"}},
	{"WALLET", []string{"지갑 장애", "지갑 오류", "wallet outage", "wallet issue", "wallet error", "wallet unavailable"}},
	{"NETWORK", []string{"네트워크 장애", "네트워크 오류", "네트워크 불안정", "network outage", "network issue", "network error", "network failure", "network unavailable"}},
	{"DEPOSIT_WITHDRAWAL", []string{"입출금", "입금 중단", "출금 중단", "입금 일시 중단", "출금 일시 중단", "입금 일시 중지", "출금 일시 중지", "입금 재개", "출금 재개", "deposit suspended", "withdrawal suspended", "deposit resumed", "withdrawal resumed", "suspend deposit", "suspend withdrawal", "resume deposit", "resume withdrawal", "deposit halted", "withdrawal halted"}},
	{"NETWORK_UPGRADE", []string{"네트워크 업그레이드", "network upgrade", "network migration", "chain upgrade"}},
	{"TOKEN_SWAP", []string{"토큰 스왑", "토큰스왑", "token swap", "token migration", "swap support"}},
	{"REBRAND", []string{"리브랜딩", "브랜드 변경", "토큰명 변경", "rebranding", "rebrand"}},
	{"SNAPSHOT", []string{"스냅샷", "snapshot"}},
	{"EVENT", []string{"이벤트", "프로모션", "캠페인", "챌린지", "페스타", "대회", "리워드", "event", "campaign", "promotion", "competition", "reward program"}},
	{"MAINTENANCE", []string{"점검", "maintenance", "system upgrade", "api update"}},
	{"UPDATE", []string{"업데이트", "서비스 개선", "service update"}},
}

var (
	noticeSpacePattern  = regexp.MustCompile(`\s+`)
	noticeUpperPattern  = regexp.MustCompile(`\(([A-Z0-9]{1,20})\)`)
	noticeParenPattern  = regexp.MustCompile(`\(([^()]*)\)`)
	titleAssetPattern   = regexp.MustCompile(`^[A-Za-z0-9]*[A-Za-z][A-Za-z0-9]{0,19}$`)
	noticeTokenPattern  = regexp.MustCompile(`[A-Za-z0-9]{1,40}`)
	noticeSuppliedClass = regexp.MustCompile(`[A-Z가-힣]`)
	strongAssetBefore   = regexp.MustCompile(`(?i)(?:will\s+list|to\s+list|new\s+listing|delist|support\s+for|trading\s+of|deposits?\s+of|withdrawals?\s+of|상장|거래지원|지원\s*종료|입출금|심볼\s*변경|자산)[^A-Za-z0-9가-힣]*$`)
	strongAssetAfter    = regexp.MustCompile(`(?i)^[^A-Za-z0-9가-힣]*(?:token|coin|network|market|pair|holders?|holding|airdrop|distribution|event|reward|토큰|코인|네트워크|마켓|페어|보유|에어드랍|지급|이벤트)(?:[^A-Za-z0-9_가-힣]|$)`)
	symbolAssetBefore   = regexp.MustCompile(`(?i)(?:symbol|ticker|심볼|티커)\s*(?::|→|->)\s*$`)
	numberAssetBefore   = regexp.MustCompile(`\d[\d,.]*\s*$`)
)

var titleAssetExcluded = map[string]struct{}{
	"API": {}, "FAQ": {}, "HTML": {}, "ID": {}, "PDF": {}, "URL": {}, "UTC": {}, "KST": {},
	"MAINNET": {}, "NETWORK": {}, "CHAIN": {}, "SMART": {}, "ERC20": {}, "BEP20": {}, "POS": {}, "N3": {},
}

func ParsePayload(exchange string, payload []byte, detected time.Time, knownAssets map[string]struct{}) ([]Item, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode %s notice payload: %w", exchange, err)
	}
	detectedMS := detected.UnixMilli()
	if frame, ok := domesticFrame(exchange); ok && frame.PayloadRows != nil {
		if _, ok := path(root, frame.PayloadRows.RowsPath...).([]any); !ok {
			return nil, fmt.Errorf("%s official notice response is missing its rows array", exchange)
		}
		return parseRowsWithFrame(exchange, root, *frame.PayloadRows, detectedMS, knownAssets), nil
	}
	var result []Item
	switch exchange {
	case "binance":
		seen := make(map[string]struct{})
		for _, catalog := range objectRows(path(root, "data", "catalogs")) {
			for _, row := range objectRows(catalog["articles"]) {
				id := firstText(row["code"], row["id"])
				if _, found := seen[id]; found {
					continue
				}
				seen[id] = struct{}{}
				result = append(result, normalizedItem(exchange, id, textValue(row["title"]),
					"https://www.binance.com/en/support/announcement/detail/"+id,
					row["releaseDate"], detectedMS, textValue(catalog["catalogName"]),
					row["updateDate"], "", knownAssets))
			}
		}
	default:
		return nil, fmt.Errorf("unsupported JSON notice exchange: %s", exchange)
	}
	return result, nil
}

func normalizedItem(
	exchange, id, title, url string,
	published any,
	detectedMS int64,
	suppliedCategory string,
	updated any,
	body string,
	knownAssets map[string]struct{},
) Item {
	title = noticeSpacePattern.ReplaceAllString(html.UnescapeString(title), " ")
	title = strings.TrimSpace(title)
	publishedMS := noticeTimestamp(published)
	precision := "SECOND"
	if publishedMS == 0 {
		precision = "DETECTED"
	}
	return Item{
		Exchange: exchange, NoticeID: id, Title: title, URL: url,
		Category: Classify(title, suppliedCategory), PublishedTSMS: publishedMS,
		DetectedTSMS: detectedMS, TimestampPrecision: precision,
		Assets: MentionedAssets(title, knownAssets), SourceType: sourceTypeFor(exchange),
		UpdatedTSMS: noticeTimestamp(updated), BodyExcerpt: body,
		Schedules: []Schedule{}, RiskTags: []string{}, ImageURLs: []string{},
		ImageAnalysis: []map[string]any{}, ContentSource: "LIST_ONLY",
		AnalysisStatus: "LIST_ONLY",
	}
}

func Classify(title, supplied string) string {
	text := strings.ToLower(supplied + " " + title)
	for _, rule := range noticeCategoryRules {
		for _, word := range rule.words {
			if strings.Contains(text, word) {
				return rule.category
			}
		}
	}
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(supplied), " ", "_"))
	if noticeSuppliedClass.MatchString(normalized) {
		return normalized
	}
	return "OTHER"
}

func MentionedAssets(content string, knownAssets map[string]struct{}) []string {
	found := make(map[string]struct{})
	for _, match := range noticeUpperPattern.FindAllStringSubmatchIndex(content, -1) {
		asset := strings.ToUpper(content[match[2]:match[3]])
		if _, known := knownAssets[asset]; known && !isContextToken(asset) {
			found[asset] = struct{}{}
		}
	}
	for _, indexes := range noticeTokenPattern.FindAllStringIndex(content, -1) {
		if (indexes[0] > 0 && isASCIIAlphaNumeric(content[indexes[0]-1])) ||
			(indexes[1] < len(content) && isASCIIAlphaNumeric(content[indexes[1]])) {
			continue
		}
		raw := content[indexes[0]:indexes[1]]
		if raw != strings.ToUpper(raw) || len(raw) <= 1 {
			continue
		}
		asset := strings.ToUpper(raw)
		if isContextToken(asset) {
			continue
		}
		if _, known := knownAssets[asset]; !known {
			continue
		}
		lineStart, lineEnd := noticeLineBounds(content, indexes[0], indexes[1])
		before := content[lineStart:indexes[0]]
		after := content[indexes[1]:lineEnd]
		line := strings.ToLower(content[lineStart:lineEnd])
		if strongAssetBefore.MatchString(lastRunes(before, 120)) ||
			strongAssetAfter.MatchString(firstRunes(after, 120)) ||
			symbolAssetBefore.MatchString(lastRunes(before, 120)) ||
			(numberAssetBefore.MatchString(before) && containsAnyFold(line, []string{
				"reward", "prize", "airdrop", "distribution", "holder",
				"보상", "리워드", "상금", "에어드랍", "분배", "홀더",
			})) {
			found[asset] = struct{}{}
		}
	}
	result := make([]string, 0, len(found))
	for asset := range found {
		result = append(result, asset)
	}
	sort.Strings(result)
	return result
}

// TitleAssets extracts only ticker-shaped values explicitly delimited by
// parentheses in an official title. It is intentionally conservative so a
// list-only notice still has useful assets when the runtime market registry is
// unavailable, without treating dates, counts, network names, or prose as a
// ticker.
func TitleAssets(title string) []string {
	found := map[string]struct{}{}
	for _, match := range noticeParenPattern.FindAllStringSubmatch(title, -1) {
		content := strings.TrimSpace(match[1])
		parts := []string{content}
		if strings.Contains(content, ",") {
			parts = strings.Split(content, ",")
		}
		marketGroup := strings.Contains(strings.ToLower(content), "마켓") || strings.Contains(strings.ToLower(content), "market")
		for _, part := range parts {
			part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "마켓"))
			part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "market"))
			if !titleAssetPattern.MatchString(part) {
				continue
			}
			if len(part) == 1 && part != strings.ToUpper(part) {
				continue
			}
			asset := strings.ToUpper(part)
			if part != strings.ToUpper(part) && len(part) > 6 {
				continue
			}
			if _, excluded := titleAssetExcluded[asset]; excluded {
				continue
			}
			if marketGroup && (asset == "KRW" || asset == "BTC" || asset == "USDT") {
				continue
			}
			found[asset] = struct{}{}
		}
	}
	for _, indexes := range noticeTokenPattern.FindAllStringIndex(title, -1) {
		raw := title[indexes[0]:indexes[1]]
		if raw != strings.ToUpper(raw) || !titleAssetPattern.MatchString(raw) {
			continue
		}
		asset := strings.ToUpper(raw)
		if _, excluded := titleAssetExcluded[asset]; excluded {
			continue
		}
		_, afterEnd := noticeLineBounds(title, indexes[0], indexes[1])
		after := firstRunes(title[indexes[1]:afterEnd], 80)
		if (asset == "KRW" || asset == "BTC" || asset == "USDT") && containsAnyFold(after, []string{"마켓", "market"}) {
			continue
		}
		if strongAssetAfter.MatchString(after) {
			found[asset] = struct{}{}
		}
	}
	result := make([]string, 0, len(found))
	for asset := range found {
		result = append(result, asset)
	}
	sort.Strings(result)
	return result
}

func noticeLineBounds(content string, start, end int) (int, int) {
	lineStart := strings.LastIndex(content[:start], "\n") + 1
	lineEndOffset := strings.Index(content[end:], "\n")
	if lineEndOffset < 0 {
		return lineStart, len(content)
	}
	return lineStart, end + lineEndOffset
}

func containsMarketContext(content string) bool {
	return containsAnyFold(content, []string{
		"list", "trade", "trading", "deposit", "withdraw", "support", "reward", "prize",
		"airdrop", "network", "market", "pair", "holder", "holding", "distribution", "event",
		"campaign", "상장", "거래", "입금", "출금", "지원", "보상", "에어드랍", "마켓",
		"페어", "보유", "지급", "분배", "이벤트", "프로모션", "대상",
	})
}

func firstRunes(value string, limit int) string {
	characters := []rune(value)
	if len(characters) > limit {
		characters = characters[:limit]
	}
	return string(characters)
}

func lastRunes(value string, limit int) string {
	characters := []rune(value)
	if len(characters) > limit {
		characters = characters[len(characters)-limit:]
	}
	return string(characters)
}

func isContextToken(value string) bool {
	switch value {
	case "API", "FAQ", "HTML", "ID", "PDF", "URL", "UTC", "KST":
		return true
	default:
		return false
	}
}

func noticeTimestamp(value any) int64 {
	if value == nil || textValue(value) == "" {
		return 0
	}
	if number, ok := numberValue(value); ok {
		if number <= 0 {
			return 0
		}
		if number > 10_000_000_000 {
			if number >= float64(math.MaxInt64) {
				return 0
			}
			return int64(number)
		}
		if number >= float64(math.MaxInt64)/1_000 {
			return 0
		}
		return int64(number * 1_000)
	}
	text := strings.ReplaceAll(strings.TrimSpace(textValue(value)), "Z", "+00:00")
	for _, layout := range []string{
		time.RFC3339Nano, "2006-01-02 15:04:05", time.DateOnly, "Jan 2, 2006",
	} {
		var parsed time.Time
		var err error
		if layout == time.RFC3339Nano {
			parsed, err = time.Parse(layout, text)
		} else {
			parsed, err = time.ParseInLocation(layout, text, seoulNoticeLocation())
		}
		if err == nil {
			milliseconds := parsed.UnixMilli()
			if milliseconds <= 0 {
				return 0
			}
			return milliseconds
		}
	}
	return 0
}

func seoulNoticeLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Seoul")
	if err == nil {
		return location
	}
	return time.FixedZone("Asia/Seoul", 9*60*60)
}

func path(root any, keys ...string) any {
	current := root
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

func objectRows(value any) []map[string]any {
	values, _ := value.([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if row, ok := value.(map[string]any); ok {
			result = append(result, row)
		}
	}
	return result
}

func firstValue(values ...any) any {
	for _, value := range values {
		if textValue(value) != "" {
			return value
		}
	}
	return nil
}

func firstText(values ...any) string { return textValue(firstValue(values...)) }

func textValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func numberValue(value any) (float64, bool) {
	number, err := strconv.ParseFloat(textValue(value), 64)
	return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func joinedStrings(value any) string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text := textValue(value); text != "" {
			result = append(result, text)
		}
	}
	return strings.Join(result, ",")
}
