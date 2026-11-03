package hermesrunner

import (
	"regexp"
	"strings"
)

// A monetary-equivalent crypto reward plus an explicit offer is a payment
// topic. This is not a generic classification of the courtesy verb 드립니다.
var noticeRewardOffer = regexp.MustCompile(`\d[\d,.]*\s*(?:만|억|천)?\s*(?:원|KRW)\s*(?:상당(?:의)?)?\s*(비트코인|이더리움|[A-Z][A-Z0-9]{1,14})(?:을|를)?\s*(?:드려요|드립니다)[.!！]?$`)
var noticeRewardEquivalentAsset = regexp.MustCompile(`\d[\d,.]*\s*(?:만|억|천)?\s*(?:원|KRW)\s*(?:상당(?:의)?)?\s*([가-힣A-Za-z][가-힣A-Za-z0-9]*)`)
var noticeRewardCompleted = regexp.MustCompile(`(?:지급|제공|수령|수취|입금)(?:이|은|을|를)?\s*(?:완료|끝|마쳤)|지급(?:했|하였|되었|됐)|받았|수령했|이미.*(?:지급|수령|받)`)
var noticeRewardDenied = regexp.MustCompile(`(?:지급|제공|수령|수취|입금)(?:하|되|받)?지(?:는|도)?\s*(?:않|못)|(?:지급|제공|수령|수취|입금)(?:할|될|받을)\s*수(?:는|도)?\s*없|(?:지급|제공)해\s*드리지(?:는|도)?\s*(?:않|못)|(?:안|못)\s*(?:지급|제공|수령|수취|입금)|(?:지급|제공|수령|수취|입금)(?:이|은|을|를)?\s*(?:없|불가|불가능|취소)`)
var noticeRewardEligibility = regexp.MustCompile(`[가-힣][가-힣 \t]*(?:에게만|한테만|에만|회원만|고객만|가입자만|참여자만)`)

func noticeRewardEligibilityMatches(claim, official, title string) bool {
	compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
	answer := compact(claim)
	for _, condition := range noticeRewardEligibility.FindAllString(official+"\n"+title, -1) {
		if !strings.Contains(answer, compact(condition)) {
			return false
		}
	}
	return true
}

func noticeQuestionExplicitRewardOffer(text, title string) string {
	context := text + "\n" + title
	if strings.ContainsAny(context, "?？\"'“”‘’「」") {
		return ""
	}
	for _, word := range []string{"안내드", "말씀드", "사기", "피싱", "주의", "경고", "가정", "예시", "최대", "추첨", "조건", "경우", "한해", "한하여", "충족", "전제", "선착순"} {
		if strings.Contains(context, word) {
			return ""
		}
	}
	match := noticeRewardOffer.FindStringSubmatch(strings.TrimSpace(text))
	if len(match) == 0 {
		return ""
	}
	switch match[1] {
	case "API", "KST", "UTC", "GMT", "JSON", "KRW":
		return ""
	}
	return match[1]
}

func noticeQuestionRewardOfferAssetMatches(claim, official, title string) bool {
	offered := noticeQuestionExplicitRewardOffer(official, title)
	if offered == "" {
		return true
	}
	_, action := noticeQuestionClaimFacets(claim)
	negative, _ := noticeQuestionActionState(claim)
	if action != "" || negative || noticeRewardCompleted.MatchString(claim) || noticeRewardDenied.MatchString(claim) || !noticeRewardEligibilityMatches(claim, official, title) {
		return false
	}
	match := noticeRewardEquivalentAsset.FindStringSubmatch(claim)
	if len(match) == 0 {
		return true
	}
	asset := strings.TrimSuffix(strings.TrimSuffix(match[1], "을"), "를")
	switch asset {
	case "입니다", "이며", "이고", "지급", "지급합니다", "보상", "혜택", "제공", "제공합니다":
		return true
	}
	return asset == offered
}
