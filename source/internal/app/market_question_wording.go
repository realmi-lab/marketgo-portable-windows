package app

import (
	"strings"
	"unicode"
)

// koreanWithAnd appends the connective particle that matches the word's final
// sound: "30분과", "10초와", "빗썸과", "업비트와".
func koreanWithAnd(word string) string {
	if koreanEndsWithConsonant(word) {
		return word + "과"
	}
	return word + "와"
}

// koreanWithTopic appends the topic particle that matches the word's final
// sound: "BTC는", "SOL은".
func koreanWithTopic(word string) string {
	if koreanEndsWithConsonant(word) {
		return word + "은"
	}
	return word + "는"
}

// marketQuestionAssetIdentityNote discloses that coins are matched by symbol.
const marketQuestionAssetIdentityNote = "거래소별 같은 심볼을 같은 코인으로 묶은 값이며, 같은 토큰(계약 주소)인지는 확인하지 않았습니다."

// koreanEndsWithConsonant reports whether the last spoken syllable of word has
// a final consonant. Digits follow their Sino-Korean reading (영·일·삼·육·칠·팔)
// and Latin letters their spelled names (엘·엠·엔·알); "%" reads as 퍼센트.
func koreanEndsWithConsonant(word string) bool {
	runes := []rune(strings.TrimSpace(word))
	for index := len(runes) - 1; index >= 0; index-- {
		value := runes[index]
		switch {
		case value >= 0xAC00 && value <= 0xD7A3:
			return (value-0xAC00)%28 != 0
		case value >= '0' && value <= '9':
			return strings.ContainsRune("013678", value)
		case value == '%':
			return false
		case unicode.IsLetter(value):
			return strings.ContainsRune("LMNRlmnr", value)
		}
	}
	return false
}

// marketQuestionReasonLabel turns an internal unavailability code into a short
// reader-facing phrase. A code without a known meaning returns "" so callers
// omit it instead of showing the raw code.
func marketQuestionReasonLabel(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, rule := range []struct{ marker, label string }{
		{"READ_FAILED", "자료 조회 실패"},
		{"NOT_CONFIGURED", "자료 조회 경로 미구성"},
		{"EXCEEDS", "조회 가능 범위 초과"},
		{"INCOMPLETE", "비교 구간 자료 불완전"},
		{"NOT_LIVE", "확정되지 않은 표본 포함"},
		{"MISSING", "필요한 시점의 표본 없음"},
		{"SAMPLES_REQUIRED", "연속 표본 부족"},
		{"NOT_AVAILABLE", "필요한 자료 없음"},
		{"NOT_ALIGNED", "날짜 경계와 시간봉 불일치"},
	} {
		if code != "" && strings.Contains(code, rule.marker) {
			return rule.label
		}
	}
	return ""
}
