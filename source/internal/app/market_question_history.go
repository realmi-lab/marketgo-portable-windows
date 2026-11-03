package app

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	marketanalysis "market-share-go/internal/analysis"
)

func normalizeMarketQuestionHistory(
	history []marketanalysis.MarketQuestionConversationTurn,
) ([]marketanalysis.MarketQuestionConversationTurn, error) {
	if len(history) > marketanalysis.MaxMarketQuestionConversationTurns {
		return nil, fmt.Errorf(
			"%w: conversation history must contain at most %d turns",
			marketanalysis.ErrInvalidMarketQuestion,
			marketanalysis.MaxMarketQuestionConversationTurns,
		)
	}
	cleaned := make([]marketanalysis.MarketQuestionConversationTurn, 0, len(history))
	for _, turn := range history {
		question := strings.TrimSpace(turn.Question)
		questionRunes := utf8.RuneCountInString(question)
		if !utf8.ValidString(question) || questionRunes < 2 || questionRunes > 500 {
			return nil, fmt.Errorf("%w: conversation history contains invalid text", marketanalysis.ErrInvalidMarketQuestion)
		}
		// Prior answers are untrusted and unused, including their length and encoding.
		cleaned = append(cleaned, marketanalysis.MarketQuestionConversationTurn{
			Question: question,
			Answer:   isolatedMarketQuestionHistoryAnswer,
		})
	}
	return cleaned, nil
}

func marketQuestionWithConversationContext(
	question string,
	history []marketanalysis.MarketQuestionConversationTurn,
	assetAliases map[string]string,
	reference time.Time,
) string {
	if len(history) == 0 || !marketQuestionIsConversationalFollowUp(question) {
		return question
	}
	// Resolve user questions in order. Parsing only the last raw question loses
	// its inherited period after a turn such as "그럼 업비트는?". Never consult
	// earlier assistant prose or turn it into a numeric/context source.
	var previous *marketanalysis.MarketQuestionQuery
	for _, turn := range history {
		contextual := turn.Question
		if previous != nil {
			contextual = marketQuestionWithPreviousQuery(contextual, *previous, assetAliases, reference)
		}
		resolved, err := parseMarketQuestionAt(contextual, assetAliases, reference)
		if err != nil {
			previous = nil
			continue
		}
		previous = &resolved
	}
	if previous == nil {
		return question
	}
	return marketQuestionWithPreviousQuery(question, *previous, assetAliases, reference)
}

func marketQuestionWithPreviousQuery(
	question string,
	previous marketanalysis.MarketQuestionQuery,
	assetAliases map[string]string,
	reference time.Time,
) string {
	if !marketQuestionIsConversationalFollowUp(question) {
		return question
	}
	currentExchange, exchangeErr := parseMarketQuestionExchange(question)
	currentAsset, assetErr := parseMarketQuestionAsset(question, assetAliases)
	_, durationFound, durationErr := parseMarketQuestionDuration(question)
	_, dateFound, dateErr := parseMarketQuestionCalendarDate(question, reference)
	clockCount := len(marketQuestionClockPattern.FindAllStringSubmatch(question, -1))
	if exchangeErr != nil || assetErr != nil || durationErr != nil || dateErr != nil ||
		clockCount != 0 && clockCount != 2 || marketQuestionUnsupportedKoreanClockPattern.MatchString(question) {
		return question
	}

	context := make([]string, 0, 4)
	if currentExchange == "" && previous.Exchange != "" {
		context = append(context, previous.Exchange)
	}
	if currentAsset == "" && previous.Asset != "" {
		context = append(context, previous.Asset)
	}
	if !dateFound && !durationFound && !marketQuestionExplicitCurrentFollowUp(question) {
		switch {
		case clockCount == 2:
			// New clock endpoints override old endpoints; inherit only their
			// unambiguous calendar date, never append a second clock pair.
			if previous.TargetDate != "" && !strings.Contains(previous.TargetDate, "..") {
				context = append(context, previous.TargetDate)
			}
		case len(previous.TargetDates) >= 2:
			context = append(context, strings.Join(previous.TargetDates, " · "), "날짜별")
		case previous.EndPolicy == "EXACT_USER_TIME_RANGE_KST" && previous.TargetStartMS > 0 && previous.TargetEndMS > previous.TargetStartMS:
			start := time.UnixMilli(previous.TargetStartMS).In(marketQuestionKSTLocation)
			end := time.UnixMilli(previous.TargetEndMS).In(marketQuestionKSTLocation)
			// The parser handles a clock pair crossing midnight. Keep seconds,
			// and do not replace an exact interval with its display-only date.
			context = append(context, start.Format("2006-01-02 15:04:05")+"~"+end.Format("15:04:05"))
		case previous.TargetDate != "":
			context = append(context, previous.TargetDate)
		case previous.TimeValue > 0:
			unit := "분"
			if previous.TimeUnit == marketanalysis.MarketQuestionTimeHour {
				unit = "시간"
			}
			context = append(context, fmt.Sprintf("%d%s", previous.TimeValue, unit))
			if previous.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval {
				context = append(context, "직전 동일 구간")
			}
		}
	}
	if len(context) > 0 {
		return strings.TrimSpace(question) + " (이전 대화 기준: " + strings.Join(context, " ") + ")"
	}
	return question
}

func marketQuestionExplicitCurrentFollowUp(question string) bool {
	compact := strings.Join(strings.Fields(question), "")
	// These phrases request a current auxiliary comparison while retaining the
	// historical period; they are not a replacement with CURRENT_SNAPSHOT.
	for _, phrase := range []string{
		"현재도", "지금도", "현재시간", "현재시각", "현재기준", "현재까지", "현재흐름", "지금흐름",
	} {
		compact = strings.ReplaceAll(compact, phrase, "")
	}
	return strings.Contains(compact, "현재") || strings.Contains(compact, "지금")
}

func marketQuestionIsConversationalFollowUp(question string) bool {
	return marketQuestionReferencesPriorContext(question) || containsAnyString(strings.ToLower(question),
		"더 자세", "자세히", "설명해", "왜 그래", "왜 그런", "이유는", "원인은", "근거는", "왜?", "어때?", "어떻게?", "원인은?")
}

// Presentation requests can occur in a fully specified first turn. Only an
// actual reference to prior context prevents its deterministic plan; callers
// resolving existing conversations still use the broader follow-up predicate.
func marketQuestionReferencesPriorContext(question string) bool {
	question = strings.ToLower(strings.TrimSpace(question))
	if question == "" {
		return false
	}
	for _, phrase := range []string{
		"그건", "그게", "그거", "그 거래소", "그 코인", "그럼", "그러면",
		"그 이유", "그때", "그 기간", "그 구간", "그 이후", "아까", "방금", "이전", "앞서", "해당 거래소", "해당 코인",
		"같은 기간", "같은 조건", "계속", "이어서",
		"같은 시간", "말고",
		"다시 설명",
	} {
		if strings.Contains(question, phrase) {
			return true
		}
	}
	return false
}
