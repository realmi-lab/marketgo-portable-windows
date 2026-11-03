package notice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

var officialNoticeCategories = map[string]struct{}{
	"공시": {}, "신규서비스": {}, "안내": {}, "업데이트": {}, "후기": {},
}

var bodyAssetCategories = map[string]struct{}{
	"LISTING": {}, "DELISTING": {}, "DEPOSIT_WITHDRAWAL": {}, "CAUTION": {},
	"EVENT": {}, "MAINTENANCE": {}, "SECURITY": {}, "WALLET": {}, "NETWORK": {},
	"NETWORK_UPGRADE": {}, "TOKEN_SWAP": {}, "REBRAND": {}, "SNAPSHOT": {}, "AIRDROP": {},
	"MARKET_ADDITION": {}, "QUOTE_MARKET_CHANGE": {},
}

var noticeRiskRules = []struct {
	tag   string
	words []string
}{
	{"SUSPENSION", []string{"중단", "중지", "suspend", "paused"}},
	{"RESUMPTION", []string{"재개", "resume", "완료"}},
	{"CAUTION", []string{"유의종목", "투자유의", "거래유의", "market warning"}},
	{"DELISTING", []string{"거래지원 종료", "상장폐지", "delist"}},
	{"LISTING", []string{"신규 거래지원", "거래지원 개시", "will list", "new listing"}},
	{"EVENT", []string{"이벤트", "프로모션", "대회", "리워드", "event", "campaign", "promotion", "competition", "reward", "airdrop", "에어드랍"}},
}

func AnalyzeItem(item Item, knownAssets map[string]struct{}) Item {
	combinedValues := make([]string, 0, 3)
	for _, value := range []string{item.Title, item.BodyExcerpt, item.ImageText} {
		if value != "" {
			combinedValues = append(combinedValues, value)
		}
	}
	combined := strings.Join(combinedValues, "\n")
	plain := PlainText(combined)
	category := Classify(item.Title, "")
	if category == "OTHER" {
		if _, official := officialNoticeCategories[item.Category]; official {
			category = item.Category
		}
	}
	if category == "OTHER" {
		category = Classify(plain, "")
	}
	if category == "OTHER" && item.Category != "" {
		category = item.Category
	}
	item.Category = category

	lowered := strings.ToLower(plain)
	item.RiskTags = []string{}
	for _, rule := range noticeRiskRules {
		if containsAnyFold(lowered, rule.words) {
			item.RiskTags = append(item.RiskTags, rule.tag)
		}
	}
	reference := item.PublishedTSMS
	if reference == 0 {
		reference = item.DetectedTSMS
	}
	item.Schedules = ExtractSchedules(combined, category, reference)

	assetContext := item.Title
	if _, useBody := bodyAssetCategories[category]; useBody {
		assetContext = combined
	}
	assetUniverse := make(map[string]struct{}, len(knownAssets)+len(item.Assets))
	for asset := range knownAssets {
		assetUniverse[asset] = struct{}{}
	}
	for _, asset := range item.Assets {
		assetUniverse[strings.ToUpper(asset)] = struct{}{}
	}
	item.Assets = MentionedAssets(assetContext, assetUniverse)
	sort.Strings(item.Assets)
	item.Summary = truncateRunes(scheduleWhitespacePattern.ReplaceAllString(plain, " "), 500)
	digest := sha256.Sum256([]byte(combined))
	item.ContentHash = hex.EncodeToString(digest[:])
	if item.BodyExcerpt != "" {
		item.AnalysisStatus = "ANALYZED"
		item.AnalysisConfidence = 1
		if len(item.ImageURLs) > 0 && item.ImageText == "" {
			item.AnalysisStatus = "TEXT_ANALYZED_IMAGE_PENDING"
		}
	} else if item.AnalysisConfidence == 0 {
		item.AnalysisConfidence = 0.35
	}
	return item
}

func NormalizeContent(value string) string {
	raw := strings.TrimSpace(value)
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		var decoded any
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			return PlainText(jsonText(decoded))
		}
	}
	return PlainText(value)
}
