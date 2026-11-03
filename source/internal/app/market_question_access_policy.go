package app

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	marketanalysis "market-share-go/internal/analysis"
)

const isolatedMarketQuestionHistoryAnswer = "이전 답변 본문은 현재 요청의 사실 근거로 재사용하지 않습니다."

var marketQuestionCompleteAnswerModifier = regexp.MustCompile(`(^|[\s,])누락\s*없이\s*((?:설명|정리|요약|답변|작성)\s*(?:해\s*(?:주세요|주십시오|줘)|하세요|하라|해)|알려\s*(?:주세요|주십시오|줘))(\s*[.!?,:;]|\s*$)`)
var marketQuestionTableOutputFormat = regexp.MustCompile(`테이블\s*(?:형식)?(?:으)?로\s*(?:정리|요약|설명|작성|보여|표시)`)
var marketQuestionEnglishTableOutputFormat = regexp.MustCompile(`(\b(?:show|list|summarize|present|format|display|organize|write|reply|answer)\b[^.!?;:\n]*?\b(?:as|in)\s+(?:a\s+)?(?:markdown\s+)?)table(\s+format)?\b`)

// validatePublicLLMQuestionScope is the fail-closed boundary in front of both
// public LLM surfaces, their retrieval planners, and every repository read.
// The assistants answer market and official-notice questions; neither is a
// database console or a data-collection audit surface.
func validatePublicLLMQuestionScope(
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
) error {
	if marketQuestionAsksRestrictedInternalData(question) {
		return fmt.Errorf("%w: unsupported question scope", marketanalysis.ErrRestrictedLLMQuestion)
	}
	for _, turn := range history {
		if marketQuestionAsksRestrictedInternalData(turn.Question) {
			return fmt.Errorf("%w: unsupported conversation scope", marketanalysis.ErrRestrictedLLMQuestion)
		}
	}
	return nil
}

func marketQuestionAsksRestrictedInternalData(value string) bool {
	value = marketQuestionRemoveBenignDatabaseSourcePhrase(value)
	value = marketQuestionRemoveAnswerCompletenessModifier(value)
	value = marketQuestionRemoveTableOutputFormat(value)
	compact, asciiWords := marketQuestionPolicyForms(value)
	if compact == "" {
		return false
	}

	for _, marker := range []string{
		"데이터베이스", "디비", "포스트그레스", "에스큐엘", "테이블", "컬럼", "스키마",
		"쿼리문", "접속정보", "접속문자열", "연결문자열", "저장구조", "저장방식", "어디에저장",
		"내부저장", "내부데이터", "내부자료", "원시데이터", "원시자료", "원시행", "원본행",
		"누락", "결측", "미수집", "커버리지", "백필", "누락률", "수집률", "완전성",
		"데이터품질", "자료품질", "수집품질", "관측품질", "데이터공백", "자료공백",
		"수집공백", "관측공백", "저장공백", "기록되지않", "저장되지않", "수집되지않",
		"관측되지않", "없는자료", "없는데이터", "없는기록", "데이터가없는", "자료가없는",
		"빈구간", "빈시간", "빈시간대", "비어있는구간", "비어있는시간", "수집구멍", "관측구멍",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}

	if containsAnyString(compact, "빠진데이터", "빠진자료", "빠진값", "빠진행", "빠진구간", "빠진시간", "빠진기록") {
		return true
	}
	if containsAnyString(compact, "데이터가있는지", "데이터는있는지", "데이터있는지", "데이터가있나", "데이터있나", "데이터가없나", "데이터없는지", "자료가있는지", "자료가없나", "기록이있는지", "기록이없나") {
		return true
	}
	if containsAnyString(compact, "몇건저장", "몇개저장", "몇행", "행개수", "레코드수", "레코드개수") {
		return true
	}
	// Accuracy of stored/observed data is an audit request. Accuracy as an
	// answer-writing modifier ("정확히 설명") does not change the question scope.
	if containsAnyString(compact, "데이터", "자료", "수집", "관측", "기록") &&
		containsAnyString(compact, "정확한지", "정확한가", "정확성", "신뢰도", "신뢰성", "믿을수", "품질") {
		return true
	}

	wordSet := make(map[string]bool, len(asciiWords))
	for _, word := range asciiWords {
		wordSet[word] = true
		switch word {
		case "db", "database", "databases", "postgres", "postgresql", "sqlite", "mysql",
			"sql", "schema", "schemas", "table", "tables", "column", "columns", "row", "rows",
			"jdbc", "odbc", "missing", "null", "coverage", "completeness", "backfill", "uncollected",
			"omitted", "absence", "absent", "unobserved":
			return true
		}
	}

	joinedASCII := strings.Join(asciiWords, "")
	for _, marker := range []string{
		"database", "postgresql", "postgres", "sqlite", "mysql", "connectionstring",
		"databaseurl", "rawrows", "rawdata", "dataquality", "missingdata", "datagap",
		"droppedrows", "droppedframes", "rowcount", "recordcount",
	} {
		if strings.Contains(joinedASCII, marker) {
			return true
		}
	}
	if containsSpelledRestrictedInitialism(asciiWords) {
		return true
	}
	if wordSet["data"] && (wordSet["gap"] || wordSet["gaps"] || wordSet["quality"] || wordSet["availability"] || wordSet["accuracy"] || wordSet["accurate"] || wordSet["reliable"] || wordSet["trustworthy"]) {
		return true
	}
	if (wordSet["empty"] || wordSet["blank"] || wordSet["hole"] || wordSet["holes"] || wordSet["lack"] || wordSet["lacks"]) &&
		(wordSet["data"] || wordSet["record"] || wordSet["records"] || wordSet["interval"] || wordSet["intervals"] || wordSet["period"] || wordSet["periods"] || wordSet["observation"] || wordSet["observations"]) {
		return true
	}
	if wordSet["dropped"] && (wordSet["data"] || wordSet["row"] || wordSet["rows"] || wordSet["frame"] || wordSet["frames"] || wordSet["record"] || wordSet["records"]) {
		return true
	}
	return false
}

// Replace only an explicit output-format phrase. Every other table reference,
// restricted term, and history question still passes through the same policy.
func marketQuestionRemoveTableOutputFormat(value string) string {
	value = marketQuestionTableOutputFormat.ReplaceAllStringFunc(value, func(format string) string {
		return strings.ReplaceAll(format, "테이블", "표")
	})
	return marketQuestionEnglishTableOutputFormat.ReplaceAllStringFunc(value, func(format string) string {
		// "Show what is stored in a table" describes a storage object, even
		// though it starts with an output verb. Preserve that table reference.
		_, words := marketQuestionPolicyForms(format)
		for _, word := range words {
			switch word {
			case "store", "stores", "stored", "storage", "save", "saved", "kept",
				"contain", "contains", "contained", "containing", "content", "contents",
				"record", "records", "entry", "entries", "field", "fields", "cell", "cells":
				return format
			}
		}
		return marketQuestionEnglishTableOutputFormat.ReplaceAllString(format, "${1}formatted output${2}")
	})
}

// Only remove an adverb attached to an answer-writing verb. This is not an
// allow rule: every remaining restricted term is still checked below, including
// another occurrence of missing data in the very same request or its history.
func marketQuestionRemoveAnswerCompletenessModifier(value string) string {
	var result strings.Builder
	previous := 0
	for _, match := range marketQuestionCompleteAnswerModifier.FindAllStringSubmatchIndex(value, -1) {
		result.WriteString(value[previous:match[0]])
		prefix, _ := marketQuestionPolicyForms(value[:match[0]])
		restrictedSubject := false
		for _, subject := range []string{"데이터", "자료", "수집", "관측", "저장", "구간", "시간", "기록"} {
			for _, particle := range []string{"", "가", "이", "의", "은", "는"} {
				if strings.HasSuffix(prefix, subject+particle) {
					restrictedSubject = true
				}
			}
		}
		if restrictedSubject {
			result.WriteString(value[match[0]:match[1]])
		} else {
			result.WriteString(value[match[2]:match[3]] + "꼼꼼히 " + value[match[4]:match[5]] + value[match[6]:match[7]])
		}
		previous = match[1]
	}
	result.WriteString(value[previous:])
	return result.String()
}

// marketQuestionRemoveBenignDatabaseSourcePhrase distinguishes a user naming
// their stored data as the source from asking for database internals. Any
// remaining table/schema/raw-row/missing-data term is still rejected by the
// fail-closed checks below. This lets natural questions such as "내 DB에서
// 지난 7일 점유율을 분석해줘" reach the bounded market query path.
func marketQuestionRemoveBenignDatabaseSourcePhrase(value string) string {
	normalized := strings.ToLower(norm.NFKC.String(value))
	replacer := strings.NewReplacer(
		"내 db에서", "저장 자료에서",
		"내db에서", "저장 자료에서",
		"우리 db에서", "저장 자료에서",
		"우리db에서", "저장 자료에서",
		"db에서", "저장 자료에서",
		"database에서", "저장 자료에서",
		"내 데이터베이스에서", "저장 자료에서",
		"내데이터베이스에서", "저장 자료에서",
		"데이터베이스에서", "저장 자료에서",
	)
	return replacer.Replace(normalized)
}

func marketQuestionPolicyForms(value string) (string, []string) {
	value = strings.ToLower(norm.NFKC.String(value))
	var compact strings.Builder
	words := []string{}
	var asciiWord strings.Builder
	flushASCII := func() {
		if asciiWord.Len() == 0 {
			return
		}
		words = append(words, asciiWord.String())
		asciiWord.Reset()
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			compact.WriteRune(char)
		}
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			asciiWord.WriteRune(char)
		} else {
			flushASCII()
		}
	}
	flushASCII()
	return compact.String(), words
}

func containsSpelledRestrictedInitialism(words []string) bool {
	var run strings.Builder
	check := func() bool {
		value := run.String()
		run.Reset()
		return value == "db" || value == "sql"
	}
	for _, word := range words {
		if len(word) == 1 && word[0] >= 'a' && word[0] <= 'z' {
			run.WriteString(word)
			continue
		}
		if check() {
			return true
		}
	}
	return check()
}

func containsAnyString(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
