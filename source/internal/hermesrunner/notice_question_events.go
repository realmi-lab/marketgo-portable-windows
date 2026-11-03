package hermesrunner

import (
	"fmt"
	"regexp"
	"strings"
)

var noticeClaimTimeEquality = regexp.MustCompile(`^(?:은|는)?(?:같은|동일한)(?:시점|시각|일시)(?:이)?(아닙니다|아니다|입니다|이다)[.!?]?$`)
var noticeClaimTimeNoun = regexp.MustCompile(`시점|시각|일시|일정|때`)
var noticeClaimTimeReportedComparison = regexp.MustCompile(`(같은|동일한|다른)(?:시점|시각|일시|일정|때)(?:을|를)(?:안내|공지|제시)(?:했|하였)습니다$`)
var noticeClaimTimeRelationSpan = regexp.MustCompile(`(?:같은|동일한|다른)(?:시점|시각|일시|일정|때)(?:을|를|으로|로)(?:안내|공지|제시)|(?:시점|시각|일시|일정|때)(?:은|는)?(?:서로)?(?:같|동일|다르)`)
var noticeClaimExcerptSubject = regexp.MustCompile(`(?:발췌|인용|제공)[\t ]*범위(?:가|는|도|만)?`)

func noticeQuestionDocumentExtentSubject(text string) bool {
	action := noticeClaimAction.FindStringIndex(text)
	if len(action) == 0 {
		return false
	}
	prefix := text[:action[0]]
	return (strings.Contains(prefix, "원문") || strings.Contains(prefix, "본문") || strings.Contains(prefix, "공지")) && noticeClaimExcerptSubject.MatchString(prefix)
}

// A time comparison relates two events; negating equality does not negate
// either event. Only explicit two-event comparisons with source timestamps
// are consumed here. Other prose remains under ordinary claim validation.
func noticeQuestionTimeComparisons(text string, sources []noticeQuestionClaimSource, official [][]noticeQuestionClaim, identifiers, knownWords map[string]bool, numericOnly ...bool) (string, error) {
	remaining := text
	for _, clause := range noticeQuestionClaimClauses(text) {
		if len(numericOnly) > 0 && numericOnly[0] && !noticeQuestionNumericToken.MatchString(clause) {
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(clause), "?") {
			question := strings.TrimSuffix(strings.TrimSpace(clause), "?")
			if noticeQuestionTimeComparisonPredicate(question).detected {
				// This span asks about a relation rather than asserting one.
				// Earlier and later independent clauses remain in the text.
				remaining = strings.Replace(remaining, clause, "", 1)
				continue
			}
		}
		actions := noticeClaimAction.FindAllStringIndex(clause, -1)
		comparison := noticeQuestionTimeComparisonPredicate(clause)
		if !comparison.detected {
			continue
		}
		if !comparison.resolved {
			if len(numericOnly) > 0 && numericOnly[0] {
				continue
			}
			return "", fmt.Errorf("notice answer time comparison has an unresolved relational predicate: %s", clause)
		}
		wantEqual := comparison.equal
		exchanges := noticeReasonMentionedExchanges(clause, sources)
		crossVenue := len(actions) == 1 && len(exchanges) == 2
		if len(actions) != 2 && !crossVenue {
			if len(numericOnly) > 0 && numericOnly[0] {
				continue
			}
			return "", fmt.Errorf("notice answer time comparison has unresolved event participants: %s", clause)
		}
		leftTopic, leftAction := noticeQuestionClaimFacets(clause[:actions[0][1]])
		rightTopic, rightAction := leftTopic, leftAction
		if !crossVenue {
			between := clause[actions[0][1]:actions[1][0]]
			if !strings.ContainsAny(between, "와과") {
				if len(numericOnly) > 0 && numericOnly[0] {
					continue
				}
				return "", fmt.Errorf("notice answer time comparison has unresolved coordination: %s", clause)
			}
			rightTopic, rightAction = noticeQuestionClaimFacets(between + clause[actions[1][0]:actions[1][1]])
		}
		if leftTopic == "" || rightTopic == "" {
			if len(numericOnly) > 0 && numericOnly[0] {
				continue
			}
			return "", fmt.Errorf("notice answer time comparison has an unresolved event object: %s", clause)
		}
		for _, word := range noticeClaimTicker.FindAllString(clause, -1) {
			if len(word) > 1 && !knownWords[word] {
				return "", fmt.Errorf("notice answer names an unsupported event target: %s", word)
			}
		}
		found := false
		venueEvents := map[string][]noticeQuestionClaim{}
		for i, source := range sources {
			if len(exchanges) > 0 && !slicesContainFold(exchanges, source.Exchange) {
				continue
			}
			otherAsset := false
			for asset := range identifiers {
				if noticeQuestionHasAnchor(clause, asset) && !slicesContainFold(source.Assets, asset) {
					otherAsset = true
				}
			}
			if otherAsset {
				continue
			}
			left, right := []noticeQuestionClaim{}, []noticeQuestionClaim{}
			for _, event := range official[i] {
				if event.reported || event.negative || event.role == "OLD" || len(event.values) == 0 {
					continue
				}
				wrongAsset := false
				for asset := range identifiers {
					if noticeQuestionHasAnchor(clause, asset) && !noticeQuestionHasAnchor(event.text, asset) {
						for other := range identifiers {
							if other != asset && noticeQuestionHasAnchor(event.text, other) {
								wrongAsset = true
							}
						}
					}
				}
				if wrongAsset {
					continue
				}
				if event.topic == leftTopic && event.action == leftAction {
					left = append(left, event)
				}
				if event.topic == rightTopic && event.action == rightAction {
					right = append(right, event)
				}
			}
			if len(left) == 0 || len(right) == 0 {
				if len(exchanges) > 0 {
					return "", fmt.Errorf("notice answer time relation lacks one event in a named source: %s", clause)
				}
				continue
			}
			if crossVenue {
				venueEvents[strings.ToLower(source.Exchange)] = append(venueEvents[strings.ToLower(source.Exchange)], left...)
				continue
			}
			for _, a := range left {
				for _, b := range right {
					if noticeQuestionTimeEventScope(a.text) != noticeQuestionTimeEventScope(b.text) {
						return "", fmt.Errorf("notice answer time relation combines different event scopes: %s", clause)
					}
					equal, known := noticeQuestionSameEventTime(a.values, b.values)
					if !known || equal != wantEqual {
						return "", fmt.Errorf("notice answer time relation is unsupported by its source: %s", clause)
					}
					found = true
				}
			}
		}
		if crossVenue {
			left, right := venueEvents[strings.ToLower(exchanges[0])], venueEvents[strings.ToLower(exchanges[1])]
			if len(left) == 0 || len(right) == 0 {
				return "", fmt.Errorf("notice answer time comparison lacks a named venue's event: %s", clause)
			}
			for _, a := range left {
				for _, b := range right {
					equal, known := noticeQuestionSameEventTime(a.values, b.values)
					if !known || equal != wantEqual || noticeQuestionTimeEventScope(a.text) != noticeQuestionTimeEventScope(b.text) {
						return "", fmt.Errorf("notice answer cross-venue time relation is unsupported: %s", clause)
					}
					found = true
				}
			}
		}
		if !found {
			return "", fmt.Errorf("notice answer time relation has no paired event times: %s", clause)
		}
		remaining = strings.Replace(remaining, clause, "", 1)
	}
	return remaining, nil
}

type noticeQuestionTimeComparison struct {
	detected bool
	resolved bool
	equal    bool
}

func noticeQuestionTimeComparisonPredicate(text string) noticeQuestionTimeComparison {
	if strings.HasSuffix(strings.TrimSpace(text), "?") {
		return noticeQuestionTimeComparison{}
	}
	compact := strings.TrimRight(noticeReasonCompact(text), ".!?")
	action := noticeClaimAction.MatchString(text)
	if !noticeClaimTimeNoun.MatchString(compact) || !action && !noticeClaimTopic.MatchString(text) {
		return noticeQuestionTimeComparison{}
	}
	for _, ending := range []string{"같습니다", "동일합니다", "다릅니다"} {
		if strings.HasSuffix(compact, ending) {
			return noticeQuestionTimeComparison{true, action, ending != "다릅니다"}
		}
	}
	if match := noticeClaimTimeReportedComparison.FindStringSubmatch(compact); len(match) > 0 {
		return noticeQuestionTimeComparison{true, action, match[1] != "다른"}
	}
	if actions := noticeClaimAction.FindAllStringIndex(text, -1); len(actions) > 0 {
		tail := noticeReasonCompact(text[actions[len(actions)-1][1]:])
		if match := noticeClaimTimeEquality.FindStringSubmatch(tail); len(match) > 0 {
			return noticeQuestionTimeComparison{true, true, !strings.HasPrefix(match[1], "아")}
		}
	}
	// Detection and interpretation are separate. A finite comparison with a
	// recognized relation but unbound reporting grammar must not become zero
	// claims. Titles, questions and ordinary schedule descriptions are not
	// relational assertions merely because they mention an event and a time.
	if noticeClaimTimeRelationSpan.MatchString(compact) {
		for _, ending := range []string{"습니다", "입니다", "이다", "한다", "했다"} {
			if strings.HasSuffix(compact, ending) {
				return noticeQuestionTimeComparison{detected: true}
			}
		}
	}
	return noticeQuestionTimeComparison{}
}

func noticeQuestionTimeEventScope(text string) string {
	parts := []string{noticeQuestionNetworkScope(text)}
	for _, match := range noticeClaimNetwork.FindAllStringSubmatch(text, -1) {
		if match[1] != "다른" && match[1] != "해당" && match[1] != "모든" && match[1] != "시" && !strings.HasSuffix(match[1], "의") {
			parts = append(parts, strings.ToUpper(match[1]))
		}
	}
	if strings.Contains(text, "월렛") {
		parts = append(parts, "WALLET")
	}
	return strings.Join(parts, "|")
}

func slicesContainFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func noticeQuestionSameEventTime(left, right map[string]bool) (bool, bool) {
	date := func(values map[string]bool) string {
		if full := noticeQuestionFullDateValue(values); full != "" {
			return full
		}
		for value := range values {
			if strings.HasPrefix(value, "MONTH_DAY:") {
				return value
			}
		}
		return ""
	}
	clock := func(values map[string]bool) string {
		for value := range values {
			if strings.HasPrefix(value, "CLOCK:") {
				return value
			}
		}
		return ""
	}
	a, b := date(left), date(right)
	if a == "" || b == "" {
		return false, false
	}
	full := !strings.HasPrefix(a, "MONTH_DAY:") && !strings.HasPrefix(b, "MONTH_DAY:")
	monthDay := func(value string) string {
		if strings.HasPrefix(value, "MONTH_DAY:") {
			return strings.TrimPrefix(value, "MONTH_DAY:")
		}
		return value[5:]
	}
	if full && a != b || monthDay(a) != monthDay(b) {
		return false, true
	}
	x, y := clock(left), clock(right)
	if x == "" || y == "" {
		return false, false
	}
	if x != y {
		return false, true
	}
	return full, full
}

// "거래소" names an organisation, not a trading predicate. Keep byte
// positions intact so clause/value boundaries still refer to the same text.
type noticeQuestionTopicMatcher struct{}

func (noticeQuestionTopicMatcher) FindAllStringIndex(text string, n int) [][]int {
	result := [][]int{}
	for _, location := range noticeClaimTopicPattern.FindAllStringIndex(text, -1) {
		if text[location[0]:location[1]] == "거래" && strings.HasPrefix(text[location[1]:], "소") {
			continue
		}
		// 등록증 is a document noun, not a publication/registration event.
		// Keep a later standalone 등록 predicate and its byte offsets intact.
		if text[location[0]:location[1]] == "등록" && strings.HasPrefix(text[location[1]:], "증") {
			continue
		}
		result = append(result, location)
		if n > 0 && len(result) >= n {
			break
		}
	}
	return result
}
func (m noticeQuestionTopicMatcher) FindStringIndex(text string) []int {
	locations := m.FindAllStringIndex(text, 1)
	if len(locations) == 0 {
		return nil
	}
	return locations[0]
}
func (m noticeQuestionTopicMatcher) FindString(text string) string {
	location := m.FindStringIndex(text)
	if len(location) == 0 {
		return ""
	}
	return text[location[0]:location[1]]
}
func (m noticeQuestionTopicMatcher) MatchString(text string) bool {
	return len(m.FindStringIndex(text)) > 0
}

// The inflected release verb "해제한" contains the bytes for "제한" but
// does not assert a restriction. Preserve offsets while excluding that
// morphological overlap; actual 제한 predicates remain unchanged.
type noticeQuestionActionMatcher struct{}

func (noticeQuestionActionMatcher) FindAllStringIndex(text string, n int) [][]int {
	result := [][]int{}
	for _, location := range noticeClaimActionPattern.FindAllStringIndex(text, -1) {
		if text[location[0]:location[1]] == "제한" && strings.HasSuffix(text[:location[0]], "해") {
			continue
		}
		// "중단 시작" is the onset of a suspension, not an additional
		// service START. Preserve an independent "중단 후 거래 시작".
		if text[location[0]:location[1]] == "시작" && len(result) > 0 {
			previous := result[len(result)-1]
			gap := strings.TrimSpace(text[previous[1]:location[0]])
			if noticeQuestionAction(text[previous[0]:previous[1]]) == "STOP" && (gap == "" || gap == "이" || gap == "을" || gap == "의") {
				continue
			}
		}
		result = append(result, location)
		if n > 0 && len(result) >= n {
			break
		}
	}
	return result
}
func (m noticeQuestionActionMatcher) FindStringIndex(text string) []int {
	locations := m.FindAllStringIndex(text, 1)
	if len(locations) > 0 {
		return locations[0]
	}
	return nil
}
func (m noticeQuestionActionMatcher) FindString(text string) string {
	if location := m.FindStringIndex(text); len(location) > 0 {
		return text[location[0]:location[1]]
	}
	return ""
}
func (m noticeQuestionActionMatcher) MatchString(text string) bool {
	return len(m.FindStringIndex(text)) > 0
}

// Extract local predicates rather than selecting one action for a sentence.
// The vocabulary describes service actions, never a particular notice/asset.
var (
	noticeClaimActionPattern = regexp.MustCompile(`재개|재지원|정상화|종료|마감|폐지|끝나|끝난|끝납|중단|중지|정지|멈(?:춥|추|췄|춘)|연장|개시|시작|오픈|취소|제한|(?:정상(?:적으로)?|계속)[\t ]*(?:지원|이용)|지원되지|불가(?:합니다|하다고|하다|함)`)
	noticeClaimAction        = noticeQuestionActionMatcher{}
	noticeClaimActionLink    = regexp.MustCompile(`(?:되었|됐|했|된|될|한)?(?:으며|고|며|다가|지만|이후|후|뒤|시점|기간|시|및|이나|거나|또는)`)
	noticeClaimPossible      = regexp.MustCompile(`수[\t ]*있|가능성|가능하|가능합|가능할`)
	noticeClaimAlternative   = regexp.MustCompile(`(?:이나|거나|또는|및|과|와)[\t ]*$`)
	noticeClaimNegative      = regexp.MustCompile(`(?:되지|하지|하지는|되지는)[\t ]*않|(?:이|가|는)?[\t ]*아닙|아니었|아니라고|아니라는`)
	noticeClaimCompleted     = regexp.MustCompile(`^(?:되었|됐|했|되었던|된[\t ]|완료|[\t ]*완료)`)
	noticeClaimHeading       = regexp.MustCompile(`(?:시점|일시|일정|시각|기간)[\t ]*[:：]?[\t ]*$`)
	noticeClaimDateLine      = regexp.MustCompile(`^[\t \-*•ㆍ]*(?:(?:기존|변경)[\t ]*[:：][\t ]*)?[0-9]`)
	noticeClaimConditional   = regexp.MustCompile(`^(?:된|될|한)?[\t ]*(?:뒤|후|이후|전|이전|시점|시|중|기간)`)
	noticeClaimCause         = regexp.MustCompile(`때문|위해|따(?:라|른)|로[\t ]*인[해한]|사유로|(?:업그레이드|점검|장애|오류|이슈)(?:으)?로`)
	noticeClaimNetwork       = regexp.MustCompile(`([가-힣A-Za-z0-9]+)(?:\([A-Za-z0-9]+\))?[\t ]*네트워크`)
	noticeClaimReportedClock = regexp.MustCompile(`([0-9]{1,2}:[0-9]{2})은[\t ]*(.*?)(?:공지[\t ]*)?제목[^\n,.!?]*수정[\t ]*시각[^\n,.!?]*`)
)

func noticeQuestionAction(word string) string {
	switch word {
	case "재개", "재지원", "정상화":
		return "RESUME"
	case "종료", "마감", "폐지", "끝나", "끝난", "끝납":
		return "END"
	case "중단", "중지", "정지", "지원되지", "불가":
		return "STOP"
	case "연장":
		return "EXTEND"
	case "개시", "시작", "오픈":
		return "START"
	case "취소":
		return "CANCEL"
	case "제한":
		return "RESTRICT"
	}
	if strings.HasPrefix(word, "정상") || strings.HasPrefix(word, "계속") {
		return "AVAILABLE"
	}
	if strings.HasPrefix(word, "멈") {
		return "STOP"
	}
	if strings.HasPrefix(word, "불가") {
		return "STOP"
	}
	return ""
}

// Only a local schedule label supplies an omitted predicate to a following
// date row. A publication title containing a later update is not that label.
func noticeQuestionLocalScheduleHeadings(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\n\\n\n", "\n\n"), "\n")
	heading := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if (noticeClaimHeading.MatchString(trimmed) || strings.HasSuffix(trimmed, "안내")) && noticeClaimTopic.MatchString(trimmed) && noticeClaimAction.MatchString(trimmed) && !noticeQuestionNumericToken.MatchString(trimmed) {
			heading = trimmed
			continue
		}
		_, dates := noticeQuestionCalendarDates(trimmed)
		// A local service heading also owns a labeled date row that repeats
		// the action but omits its object ("입출금 중단 안내 / 중단 일시: ...").
		// An explicit different object starts its own event instead.
		labeledDate := len(dates) > 0 && noticeClaimAction.MatchString(trimmed) && !noticeClaimTopic.MatchString(trimmed)
		if heading != "" && (noticeClaimDateLine.MatchString(trimmed) || labeledDate) {
			lines[i] = heading + " " + trimmed
			continue
		}
		if trimmed != "" && trimmed != "\\n" {
			heading = ""
		}
	}
	return strings.Join(lines, "\n")
}

func noticeQuestionActionAtoms(text string) []string {
	locations := noticeClaimAction.FindAllStringIndex(text, -1)
	if len(locations) > 0 && noticeClaimConditional.MatchString(text[locations[0][1]:]) {
		prefix := text[:locations[0][0]]
		topic, _ := noticeQuestionClaimFacets(prefix)
		if positions := noticeClaimTopic.FindAllStringIndex(prefix, -1); len(positions) > 0 {
			last := positions[len(positions)-1]
			if prefix[last[0]:last[1]] == "지갑주소" {
				topic = "ADDRESS"
			}
		}
		// A required address/application is not the service that ends in
		// "발급해야 종료 후에도 ...". Keep the requirement for its own guard.
		if (topic == "ADDRESS" || topic == "APPLICATION") && strings.Contains(prefix, "해야") {
			return []string{prefix, text[locations[0][0]:]}
		}
	}
	if firstTopic := noticeClaimTopic.FindStringIndex(text); len(firstTopic) > 0 && len(locations) > 0 && locations[0][0] < firstTopic[0] {
		prefix := text[locations[0][1]:firstTopic[0]]
		if noticeClaimConditional.MatchString(prefix) {
			return append([]string{text[:firstTopic[0]]}, noticeQuestionActionAtoms(text[firstTopic[0]:])...)
		}
	}
	if len(locations) < 2 {
		return []string{text}
	}
	parts := []string{}
	start := 0
	topic := noticeClaimTopic.FindString(text)
	for i := 0; i < len(locations)-1; i++ {
		between := text[locations[i][1]:locations[i+1][0]]
		link := noticeClaimActionLink.FindStringIndex(between)
		cut := locations[i+1][0]
		if len(link) > 0 {
			cut = locations[i][1] + link[1]
		} else if topics := noticeClaimTopic.FindAllStringIndex(between, -1); len(topics) > 0 {
			cut = locations[i][1] + topics[len(topics)-1][0]
		}
		part := text[start:cut]
		if start > 0 && !noticeClaimTopic.MatchString(part) {
			part = topic + " " + part
		}
		parts = append(parts, part)
		start = cut
	}
	part := text[start:]
	if !noticeClaimTopic.MatchString(part) {
		part = topic + " " + part
	}
	return append(parts, part)
}

func noticeQuestionActionState(text string) (bool, string) {
	location := noticeClaimAction.FindStringIndex(text)
	if len(location) == 0 {
		return false, ""
	}
	word, tail := text[location[0]:location[1]], text[location[1]:]
	negative := noticeClaimNegative.MatchString(tail)
	if word == "지원되지" {
		// "지원되지 않습니다" is STOP, while denying that restriction is
		// a different assertion. Do not erase a second negation.
		negative = strings.Contains(tail, "것은 아닙") || strings.Contains(tail, "것이 아닙")
	}
	mode := ""
	if noticeClaimPossible.MatchString(tail) {
		mode = "POSSIBLE"
	}
	if strings.Contains(tail, "예정") {
		mode = "PLANNED"
	}
	if noticeClaimCompleted.MatchString(tail) {
		mode = "COMPLETED"
	}
	if strings.Contains(tail, "완료되었습니다") || strings.Contains(tail, "완료됐") || strings.Contains(tail, "완료가 확인") {
		mode = "COMPLETED"
	}
	if noticeClaimConditional.MatchString(tail) {
		mode = "CONDITIONAL"
	}
	if noticeQuestionAction(word) == "AVAILABLE" {
		mode = ""
	}
	return negative, mode
}

func noticeQuestionRevisionRole(text string) string {
	// A revision label and its perspective are separate: "변경 전" names
	// the old schedule, whereas bare "변경"/"변경 후" names the new one.
	// Resolve this before matching the label, retaining the direction when a
	// model explains the same explicit old/new source rows in ordinary prose.
	if _, tail, ok := strings.Cut(text, "변경"); ok {
		tail = strings.TrimSpace(tail)
		if strings.HasPrefix(tail, "전") && (len(tail) == len("전") || strings.HasPrefix(tail[len("전"):], " ") || strings.HasPrefix(tail[len("전"):], ":")) {
			return "OLD"
		}
	}
	if strings.Contains(text, "기존") {
		return "OLD"
	}
	if strings.Contains(text, "변경") {
		return "NEW"
	}
	return ""
}

func noticeQuestionClaimStateMatches(claim, official noticeQuestionClaim) bool {
	for _, condition := range claim.conditions {
		matched := false
		for _, source := range official.conditions {
			if condition.action == source.action && condition.relation == source.relation && condition.relative == source.relative && (condition.topic == "" || condition.topic == source.topic || source.topic == "TRANSFER" && (condition.topic == "DEPOSIT" || condition.topic == "WITHDRAW")) {
				matched = true
				for value := range condition.values {
					if !source.values[value] {
						matched = false
						break
					}
				}
				if matched {
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	if official.reported && !claim.reported {
		return false
	}
	if (claim.topic == "TRANSFER" || claim.topic == "WITHDRAW" || claim.topic == "DEPOSIT") && (official.scope == "OTHER" && claim.scope != "OTHER" || claim.scope == "ALL" && official.scope != "ALL" || claim.scope == "OTHER" && official.scope != "OTHER") {
		return false
	}
	if claim.negative != official.negative {
		// Explicit normal support licenses a denial of interruption. Mere
		// absence of an interruption notice never does.
		if !(claim.negative && !official.negative && official.action == "AVAILABLE" && (claim.action == "STOP" || claim.action == "END")) {
			return false
		}
	}
	// A stated interruption is a restriction; a possible restriction does
	// not conversely establish interruption or unavailability.
	restrictionOfStop := claim.action == "RESTRICT" && official.action == "STOP"
	if claim.action != "" && claim.action != official.action && !restrictionOfStop && !(claim.negative && official.action == "AVAILABLE" && (claim.action == "STOP" || claim.action == "END")) {
		return false
	}
	if claim.mode == "COMPLETED" && official.mode != "COMPLETED" {
		return false
	}
	if official.mode == "POSSIBLE" && claim.mode != "POSSIBLE" {
		return false
	}
	if claim.role != "" && len(claim.values) > 0 && claim.role != official.role {
		return false
	}
	return true
}

// An extension needs adjacent old/new rows in one passage and one asset /
// named-network scope. It cannot borrow an update from another excerpt.
func noticeQuestionSourceEventRelations(claims []noticeQuestionClaim, source noticeQuestionClaimSource) []noticeQuestionClaim {
	for _, stamp := range source.Passages {
		if !strings.HasPrefix(stamp, "수정 ") {
			continue
		}
		_, values := noticeQuestionCalendarDates(stamp)
		date := noticeQuestionFullDateValue(values)
		if date == "" || !strings.Contains(source.Title, strings.ReplaceAll(date[5:], "-", "/")) {
			continue
		}
		// A trailing title clock which agrees with its update metadata has
		// the same reporting perspective, even if no second action in the
		// title happens to separate that clock from the displayed state.
		for i := range claims {
			if claims[i].origin != 0 {
				continue
			}
			for value := range claims[i].values {
				if strings.HasPrefix(value, "CLOCK:") && values[value] {
					claims[i].reported = true
				}
			}
		}
		for _, event := range noticeQuestionTextClaims(source.Title, source.Title, nil) {
			if event.action == "" || event.action == "STOP" {
				continue
			}
			event.values, event.reported = values, true
			claims = append(claims, event)
		}
	}
	// No cross-asset or cross-network inference from an unlabelled heading.
	networks := map[string]bool{}
	for _, text := range append([]string{source.Title}, source.Passages...) {
		for _, match := range noticeClaimNetwork.FindAllStringSubmatch(text, -1) {
			if match[1] != "다른" && match[1] != "해당" && match[1] != "모든" && match[1] != "시" && !strings.HasSuffix(match[1], "의") {
				networks[strings.ToUpper(match[1])] = true
			}
		}
	}
	if len(source.Assets) != 1 || len(networks) > 1 {
		return claims
	}
	for i, old := range claims {
		if old.role != "OLD" || i+1 >= len(claims) {
			continue
		}
		updated := claims[i+1]
		if updated.role != "NEW" || old.origin != updated.origin || old.topic != updated.topic || old.action != updated.action {
			continue
		}
		if noticeQuestionRevisionHeading(old.text, "기존") != noticeQuestionRevisionHeading(updated.text, "변경") {
			continue
		}
		oldDate, newDate := noticeQuestionFullDateValue(old.values), noticeQuestionFullDateValue(updated.values)
		if oldDate == "" || newDate == "" {
			continue
		}
		transition := old
		transition.text += " " + updated.text
		transition.role = "TRANSITION"
		transition.values = map[string]bool{}
		for value := range old.values {
			transition.values[value] = true
		}
		for value := range updated.values {
			transition.values[value] = true
		}
		transition.values["FROM_MONTH_DAY:"+oldDate[5:]] = true
		transition.values["TO_MONTH_DAY:"+newDate[5:]] = true
		claims = append(claims, transition)
		if newDate <= oldDate {
			continue
		}
		extension := updated
		extension.action, extension.mode, extension.role = "EXTEND", "COMPLETED", ""
		claims = append(claims, extension)
	}
	return claims
}

func noticeQuestionRevisionHeading(text, role string) string {
	before, _, _ := strings.Cut(text, role)
	return strings.Join(strings.Fields(before), " ")
}

func noticeQuestionNetworkScope(text string) string {
	if strings.Contains(text, "다른 네트워크") || strings.Contains(text, "다른네트워크") || noticeClaimOutsideNetwork.MatchString(text) {
		return "OTHER"
	}
	if strings.Contains(text, "모든 네트워크") || strings.Contains(text, "모든네트워크") {
		return "ALL"
	}
	return ""
}

var noticeClaimOutsideNetwork = regexp.MustCompile(`[가-힣A-Za-z0-9]+[\t ]*외[\t ]*네트워크`)

var noticeDocumentClockCoordination = regexp.MustCompile(`(?:게시|등록|수정|업데이트)(?:됐|되었|했)(?:고|으며|지만|는데)`)

// Publication/update are distinct timestamp roles. A finite coordination ends
// the first role before a following date; it must not pool both timestamps.
func noticeQuestionCoordinatedDocumentClocks(text string, identifiers map[string]bool) string {
	lines := strings.Split(text, "\n")
	documentListRecord := false
	for i, line := range lines {
		// Adjacent timestamp fields in a list share the document subject.
		// Only a bare update field inherits it; an explicit network/service
		// subject or any intervening non-record line ends this inheritance.
		field := strings.TrimSpace(line)
		isList := strings.HasPrefix(field, "- ") || strings.HasPrefix(field, "* ") || strings.HasPrefix(field, "• ")
		field = strings.TrimLeft(field, "-*• \t")
		label, _, hasColon := strings.Cut(field, ":")
		label = strings.Trim(label, "* \t")
		if documentListRecord && isList && hasColon && (label == "업데이트" || label == "업데이트일") {
			at := strings.Index(line, "업데이트")
			line = line[:at] + "공지 " + line[at:]
		}
		documentListRecord = isList && hasColon && noticeQuestionNumericToken.MatchString(line) && noticeQuestionUpdateHasDocumentSubject(line)
		matches := noticeDocumentClockCoordination.FindAllStringIndex(line, -1)
		for j := len(matches) - 1; j >= 0; j-- {
			b := matches[j][1]
			if !noticeQuestionNumericToken.MatchString(line[:b]) || !noticeQuestionNumericToken.MatchString(line[b:]) {
				continue
			}
			actors := ""
			if !noticeQuestionHasExplicitActor(line[b:], identifiers) {
				actors = noticeQuestionLeadingActors(line[:b], identifiers)
				if strings.Contains(line[:b], "게시") || strings.Contains(line[:b], "등록") || strings.Contains(line[:b], "공지") {
					actors += " 공지"
				}
			}
			line = line[:b] + "\n" + actors + " " + line[b:]
		}
		// A document record may list publication and update fields across a
		// comma. Preserve its subject before the ordinary clause splitter runs.
		for end := len(line); end > 0; {
			at := strings.LastIndex(line[:end], "업데이트")
			if at < 0 {
				break
			}
			if noticeQuestionUpdateHasDocumentSubject(line[:at]) && !strings.HasSuffix(line[:at], "공지 ") {
				line = line[:at] + "공지 " + line[at:]
			}
			end = at
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func noticeQuestionMetadataClauses(text string) string {
	// Keep a timestamp's explicit publication role when a sentence also
	// reports a later service event. Do not join it to that event's clock.
	for _, marker := range []string{"에 갱신된 공지는", "에 수정된 공지는", "에 게시된 공지는"} {
		if i := strings.Index(text, marker); i >= 0 {
			start := strings.LastIndex(text[:i], "\n") + 1
			prefix := text[start:i]
			if noticeQuestionNumericToken.MatchString(prefix) {
				text = text[:start] + "수정 " + prefix + "\n" + text[i+len(marker):]
			}
		}
	}
	if match := noticeClaimReportedClock.FindStringSubmatchIndex(text); len(match) > 0 {
		start := strings.LastIndex(text[:match[0]], "\n") + 1
		// Reporting metadata belongs to its own clause's subject, not an
		// earlier venue before a comma or coordinated independent statement.
		for _, boundary := range noticeClaimClauseBoundary.FindAllStringIndex(text[:match[0]], -1) {
			if text[boundary[0]] == '.' && boundary[0] > 0 && text[boundary[0]-1] >= '0' && text[boundary[0]-1] <= '9' {
				continue
			}
			if boundary[1] > start {
				start = boundary[1]
			}
		}
		subject := text[start:match[0]]
		event := text[match[4]:match[5]]
		stamp := text[match[2]:match[3]]
		stampSubject := subject
		if action := noticeClaimAction.FindStringIndex(stampSubject); len(action) > 0 {
			stampSubject = stampSubject[:action[0]]
		}
		text = text[:start] + subject + event + " 안내\n" + stampSubject + " 수정 " + stamp + text[match[1]:]
	}
	for _, clock := range noticeAnswerClock.FindAllStringIndex(text, -1) {
		tail := text[clock[1]:]
		if strings.HasPrefix(tail, "은 제목") || strings.HasPrefix(tail, "은 수정") {
			text = text[:clock[0]] + "\n수정 " + text[clock[0]:]
			break
		}
	}
	return text
}

func noticeQuestionReportedEvent(text string) bool {
	if strings.Contains(text, "정확") || strings.Contains(text, "실제") || strings.Contains(text, "시스템") || strings.Contains(text, "본문에서") {
		return false
	}
	return strings.Contains(text, "안내") || strings.Contains(text, "제목") || strings.Contains(text, "공지") || strings.Contains(text, "알림") || noticeQuestionClockLabel(text)
}

// A clock used as the grammatical subject of a displayed action label is
// reporting metadata. A statement that the service actually resumed at the
// clock remains an event assertion and cannot borrow the publication stamp.
func noticeQuestionClockLabel(text string) bool {
	clock := noticeAnswerClock.FindStringIndex(text)
	if len(clock) == 0 {
		return false
	}
	tail := strings.TrimSpace(text[clock[1]:])
	if !strings.HasPrefix(tail, "은") && !strings.HasPrefix(tail, "는") {
		return false
	}
	action := noticeClaimAction.FindStringIndex(tail)
	return len(action) > 0 && strings.Contains(tail[action[1]:], "표시")
}

type noticeQuestionEventCondition struct {
	topic, action, relation string
	values                  map[string]bool
	relative                bool
}

var noticeClaimTemporalTail = regexp.MustCompile(`^(?:된|될|한|되는)?[\t ]*(?:일[\t ]*)?(이전|이후|기간|전|후|뒤|시|중)(?:에는|에도|까지|부터|에|도)?`)
var noticeClaimSubjectParticle = regexp.MustCompile(`^(?:지원|서비스|이용)?(?:은|는|이|가)`)
var noticeClaimCoordinatedSubject = regexp.MustCompile(`^(?:지원|서비스|이용)?도`)
var noticeClaimModifierSubjectParticle = regexp.MustCompile(`^[\t ]*(?:이|가)[\t ]*$`)
var noticeClaimCoordinatedTail = regexp.MustCompile(`(?:으며|되며|하며|되고|하고|거나)[\t ]*$`)
var noticeClaimFiniteActionTail = regexp.MustCompile(`^[\t ]*(?:되|됩|돼|된|될|하|합|했|할|한|이[\t ]*있)`)

// A leading temporal clause governs each coordinated main predicate. Build
// each bounded frame independently so neither loses that condition when the
// source groups two outcomes but the answer discusses only one of them.
func noticeQuestionSharedTemporalClaims(text string, identifiers map[string]bool) []noticeQuestionClaim {
	actions := noticeClaimAction.FindAllStringIndex(text, -1)
	if len(actions) < 3 {
		return nil
	}
	relation, n := noticeQuestionTemporalRelation(text[actions[0][1]:])
	if relation == "" {
		return nil
	}
	for _, action := range actions[1:] {
		if relation, _ := noticeQuestionTemporalRelation(text[action[1]:]); relation != "" {
			return nil
		}
	}
	end := actions[0][1] + n
	frames := []noticeQuestionClaim{}
	for _, atom := range noticeQuestionActionAtoms(text[end:]) {
		frame, ok := noticeQuestionTemporalFrameWithCoordination(text[:end]+" "+atom, identifiers, true)
		if !ok {
			return nil
		}
		if noticeClaimPossible.MatchString(text) && noticeClaimAlternative.MatchString(atom) {
			frame.mode = "POSSIBLE"
		}
		frames = append(frames, frame)
	}
	return frames
}

func noticeQuestionTemporalRelation(tail string) (string, int) {
	match := noticeClaimTemporalTail.FindStringSubmatchIndex(tail)
	if len(match) == 0 {
		return "", 0
	}
	word := tail[match[2]:match[3]]
	if word == "시" && (strings.HasPrefix(tail[match[1]:], "각") || strings.HasPrefix(tail[match[1]:], "점")) {
		return "", 0
	}
	switch word {
	case "전", "이전":
		return "BEFORE", match[1]
	case "후", "뒤", "이후":
		return "AFTER", match[1]
	case "중", "기간":
		return "DURING", match[1]
	}
	return "AT", match[1]
}

func noticeQuestionEndsTemporalModifier(text string) bool {
	locations := noticeClaimAction.FindAllStringIndex(text, -1)
	if len(locations) == 0 {
		return false
	}
	last := locations[len(locations)-1]
	relation, n := noticeQuestionTemporalRelation(text[last[1]:])
	return relation != "" && strings.TrimSpace(text[last[1]+n:]) == ""
}

var noticeClaimListItem = regexp.MustCompile(`^[\t ]*[-*•⁃][\t ]+`)

// A temporal introduction governs the following list's objects. Distribute
// its predicate to noun-only items, preserving any item with its own predicate
// (including a negation) instead of replacing it with the introduction.
type noticeQuestionListProjection struct {
	Text       string
	Items      []string
	Denied     []string
	Unresolved string
}

var noticeClaimListReference = regexp.MustCompile(`(?:다음|아래)[\t ]*(?:기능|항목|서비스)`)
var noticeClaimListServicePredicate = regexp.MustCompile(`(?:지원|이용)(?:이|을|은|는)?[\t ]*(?:되|됩|하|합|할|된)`)

func noticeQuestionEventLists(text string) string {
	return noticeQuestionProjectEventLists(text, nil).Text
}

// Admission records every service-list item before any generic prose parser
// can drop a noun-only row. A recognized list with an unresolved object or
// predicate is distinct from an ordinary paragraph with no service claim.
func noticeQuestionProjectEventLists(text string, identifiers map[string]bool, sources ...noticeQuestionClaimSource) noticeQuestionListProjection {
	result := noticeQuestionListProjection{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		actions := noticeClaimAction.FindAllStringIndex(line, -1)
		if len(actions) == 1 {
			if relation, _ := noticeQuestionTemporalRelation(line[actions[0][1]:]); relation != "" && i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if next != "" && !noticeClaimListItem.MatchString(next) && noticeClaimTopic.MatchString(next) && noticeClaimAction.MatchString(next) {
					lines[i], lines[i+1] = line+" "+next, ""
					line = lines[i]
				}
			}
		}
		reference := noticeClaimListReference.FindStringIndex(line)
		if len(reference) == 0 {
			continue
		}
		items := []int{}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			if !noticeClaimListItem.MatchString(lines[j]) {
				break
			}
			items = append(items, j)
		}
		if len(items) == 0 {
			continue
		}
		main := noticeClaimAction.FindAllStringIndex(line[reference[1]:], -1)
		if len(main) == 0 && !noticeClaimListServicePredicate.MatchString(line[reference[1]:]) {
			// A list of reference headings is not a service assertion. Keep
			// its original text; any independent claims still get validated.
			continue
		}
		if len(main) != 1 {
			result.Unresolved = "service list has no unique finite predicate: " + line
			continue
		}
		action := []int{reference[1] + main[0][0], reference[1] + main[0][1]}
		if !noticeClaimFiniteActionTail.MatchString(line[action[1]:]) || !strings.HasSuffix(strings.TrimRight(noticeReasonCompact(line), ".!:;"), "다") {
			result.Unresolved = "service list predicate is unresolved: " + line
			continue
		}
		start := 0
		for _, punctuation := range []string{". ", "! ", "? "} {
			if at := strings.LastIndex(line[:reference[0]], punctuation); at >= 0 && at+2 > start {
				start = at + 2
			}
		}
		heading := line[start:]
		statement := noticeClaimStatement(heading, sources...)
		denied := statement.Matrix == noticeClaimDocumentAbsent
		if noticeClaimNegative.MatchString(line[action[1]:]) && strings.Contains(line[action[1]:], "안내") && !denied {
			result.Unresolved = "service list documentary polarity is unresolved: " + line
			continue
		}
		prefix := line[start:reference[0]]
		context := noticeQuestionLeadingActors(prefix, identifiers)
		if context == "" && start > 0 {
			context = noticeQuestionLeadingActors(line[:start], identifiers)
		}
		if context == "" && i > 0 {
			for j := i - 1; j >= 0; j-- {
				if strings.TrimSpace(lines[j]) != "" {
					context = noticeQuestionLeadingActors(lines[j], identifiers)
					break
				}
			}
		}
		temporal := ""
		modifiers := noticeClaimAction.FindAllStringIndex(prefix, -1)
		if len(modifiers) > 0 {
			last := modifiers[len(modifiers)-1]
			relation, n := noticeQuestionTemporalRelation(prefix[last[1]:])
			if relation == "" {
				result.Unresolved = "service list has an unresolved preceding predicate: " + line
				continue
			}
			temporal = strings.TrimSpace(prefix[:last[1]+n]) + " "
		}
		predicate := line[action[0]:]
		if denied {
			predicate = line[action[0]:action[1]]
		}
		lines[i] = line[:start]
		for _, j := range items {
			item := strings.TrimSpace(noticeClaimListItem.ReplaceAllString(lines[j], ""))
			if !noticeClaimTopic.MatchString(item) {
				result.Unresolved = "service list item has an unresolved object: " + item
				continue
			}
			if !noticeClaimAction.MatchString(item) {
				item += "은 " + predicate
			}
			if temporal != "" {
				item = temporal + item
			} else if context != "" {
				item = context + " " + item
			}
			if denied {
				result.Denied = append(result.Denied, item)
				lines[j] = ""
			} else {
				result.Items = append(result.Items, item)
				lines[j] = item
			}
		}
	}
	result.Text = strings.Join(lines, "\n")
	return result
}

// A single main predicate can have several temporal modifiers. Keep those
// modifiers attached to that predicate, rather than making the main subject
// the target of each modifier's action ("orders end", "trading stops").
func noticeQuestionTemporalFrame(text string, identifiers map[string]bool) (noticeQuestionClaim, bool) {
	return noticeQuestionTemporalFrameWithCoordination(text, identifiers, false)
}

func noticeQuestionTemporalFrameWithCoordination(text string, identifiers map[string]bool, coordinated bool) (noticeQuestionClaim, bool) {
	projected := text
	unavailable := false
	if strings.Contains(text, "이용") {
		if at := strings.LastIndex(text, "가능"); at >= 0 && at > strings.LastIndex(text, "중단") {
			if strings.HasSuffix(text[:at], "불") {
				unavailable = true
				at -= len("불")
			}
			length := len("가능")
			if unavailable {
				length += len("불")
			}
			projected = text[:at] + "정상 이용" + text[at+length:]
		}
	}
	actions := noticeClaimAction.FindAllStringIndex(projected, -1)
	main := -1
	type modifier struct {
		index, end int
		relation   string
		relative   bool
	}
	modifiers := []modifier{}
	for i, action := range actions {
		relation, n := noticeQuestionTemporalRelation(projected[action[1]:])
		if relation != "" {
			tail := strings.TrimSpace(projected[action[1]+n:])
			relative := strings.HasPrefix(tail, "요청한") || strings.HasPrefix(tail, "신청한") || strings.HasPrefix(tail, "접수한")
			modifiers = append(modifiers, modifier{i, action[1] + n, relation, relative})
			continue
		}
		if main >= 0 {
			return noticeQuestionClaim{}, false
		}
		main = i
	}
	if main < 0 || len(modifiers) == 0 {
		return noticeQuestionClaim{}, false
	}
	for _, modifier := range modifiers {
		if modifier.index > main {
			return noticeQuestionClaim{}, false
		}
	}
	mainAction := actions[main]
	subject := []int(nil)
	for _, topic := range noticeClaimTopic.FindAllStringIndex(projected[:mainAction[0]], -1) {
		modifierSubject := false
		for _, modifier := range modifiers {
			action := actions[modifier.index]
			if topic[1] > action[0] {
				continue
			}
			topics := noticeClaimTopic.FindAllStringIndex(projected[:action[0]], -1)
			if len(topics) > 0 && topics[len(topics)-1][0] == topic[0] && noticeClaimModifierSubjectParticle.MatchString(projected[topic[1]:action[0]]) {
				modifierSubject = true
			}
		}
		if modifierSubject {
			continue
		}
		tail := strings.TrimSpace(projected[topic[1]:mainAction[0]])
		if strings.HasPrefix(tail, "(") {
			if close := strings.Index(tail, ")"); close >= 0 {
				tail = strings.TrimSpace(tail[close+1:])
			}
		}
		compactTail := strings.ReplaceAll(tail, " ", "")
		if noticeClaimSubjectParticle.MatchString(compactTail) || coordinated && noticeClaimCoordinatedSubject.MatchString(compactTail) || noticeQuestionAction(projected[mainAction[0]:mainAction[1]]) == "RESTRICT" && (compactTail == "에" || compactTail == "에도" || compactTail == "에는") {
			subject = topic
		}
	}
	if len(subject) == 0 {
		return noticeQuestionClaim{}, false
	}
	topic, _ := noticeQuestionClaimFacets(projected[subject[0]:subject[1]])
	negative, mode := noticeQuestionActionState(projected[subject[0]:subject[1]] + " " + projected[mainAction[0]:])
	negative = negative || unavailable
	claim := noticeQuestionClaim{text: text, topic: topic, action: noticeQuestionAction(projected[mainAction[0]:mainAction[1]]), negative: negative, mode: mode, scope: noticeQuestionNetworkScope(text), reported: noticeQuestionReportedEvent(text)}
	var mainText strings.Builder
	cursor := 0
	for _, modifier := range modifiers {
		action := actions[modifier.index]
		// A bare temporal noun directly modifies the following subject in
		// "종료 전 주문은". The case-marked "종료 전에 주문은" instead
		// times the main predicate and must keep that different relation.
		if modifier.end <= subject[0] && strings.TrimSpace(projected[modifier.end:subject[0]]) == "" {
			tail := strings.TrimSpace(projected[action[1]:modifier.end])
			if strings.HasSuffix(tail, "전") {
				modifier.relative = true
			}
		}
		start := cursor
		if subject[1] > start && subject[1] < action[0] {
			start = subject[1]
		}
		mainText.WriteString(text[cursor:start])
		values := noticeQuestionFrameValues(text[start:modifier.end], identifiers)
		cursor = modifier.end
		conditionTopic := ""
		if topics := noticeClaimTopic.FindAllStringIndex(projected[:action[0]], -1); len(topics) > 0 {
			nearest := topics[len(topics)-1]
			if nearest[0] != subject[0] {
				conditionTopic, _ = noticeQuestionClaimFacets(projected[nearest[0]:nearest[1]])
			}
		}
		claim.conditions = append(claim.conditions, noticeQuestionEventCondition{topic: conditionTopic, action: noticeQuestionAction(projected[action[0]:action[1]]), relation: modifier.relation, values: values, relative: modifier.relative})
	}
	mainText.WriteString(text[cursor:])
	claim.values = noticeQuestionFrameValues(mainText.String(), identifiers)
	return claim, len(claim.conditions) > 0
}

func noticeQuestionFrameValues(text string, identifiers map[string]bool) map[string]bool {
	_, values := noticeQuestionCalendarDates(text)
	for _, quantity := range noticeQuestionExplicitQuantities(text, identifiers) {
		if !strings.HasSuffix(quantity, "|건") {
			values["QUANTITY:"+quantity] = true
		}
	}
	return values
}

func noticeQuestionFullDateValue(values map[string]bool) string {
	for value := range values {
		if len(value) == 10 && value[4] == '-' && value[7] == '-' {
			return value
		}
	}
	return ""
}

func noticeQuestionReadableTableClaims(text string) string {
	lines := strings.Split(text, "\n")
	var headers []string
	section := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			headers = nil
			if trimmed != "" {
				section = ""
				caption := strings.Trim(trimmed, "#* \t")
				action := noticeClaimAction.FindString(caption)
				isCaption := strings.HasPrefix(trimmed, "#") || noticeClaimHeading.MatchString(caption) || strings.HasSuffix(caption, "안내") || action != "" && strings.HasSuffix(caption, action)
				if isCaption && len(noticeClaimTopic.FindAllStringIndex(caption, -1)) == 1 && len(noticeClaimAction.FindAllStringIndex(caption, -1)) == 1 && !noticeQuestionNumericToken.MatchString(caption) {
					section = caption
				}
			}
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if i+1 < len(lines) && strings.Contains(lines[i+1], "---") {
			headers = cells
			lines[i] = ""
			continue
		}
		if strings.Contains(line, "---") {
			lines[i] = ""
			continue
		}
		if len(headers) != len(cells) || len(cells) < 2 {
			continue
		}
		// Row identity can occupy several columns. Keeping only the first
		// column loses the asset in a venue / asset / schedule table.
		actors := []string{strings.TrimSpace(cells[0])}
		identity := make(map[int]bool)
		for j := 1; j < len(cells); j++ {
			switch strings.TrimSpace(headers[j]) {
			case "거래소", "자산", "코인", "종목", "가상자산", "디지털 자산":
				actors = append(actors, strings.TrimSpace(cells[j]))
				identity[j] = true
			}
		}
		parts := []string{}
		for j := 1; j < len(cells); j++ {
			if identity[j] {
				continue
			}
			label := strings.Join(actors, " ")
			if strings.Contains(label, "사유") {
				label = "사유"
			}
			header := strings.TrimSpace(headers[j])
			if strings.Contains(header, "사유") {
				header = "사유"
			}
			// A generic start-time column under a single event heading names
			// that event's onset, not a different service START action. An
			// explicit column event or an end-time label keeps its own meaning.
			if section != "" && !noticeClaimTopic.MatchString(header) {
				actions := noticeClaimAction.FindAllStringIndex(header, -1)
				if len(actions) == 1 && noticeQuestionAction(header[actions[0][0]:actions[0][1]]) == noticeQuestionAction(noticeClaimAction.FindString(section)) {
					header = noticeClaimTopic.FindString(section) + " " + header
				} else if len(actions) == 0 && noticeClaimTimeNoun.MatchString(header) {
					header = section + " " + header
				} else if len(actions) == 1 && header[actions[0][0]:actions[0][1]] == "시작" && noticeClaimTimeNoun.MatchString(header) {
					header = section + " " + header[:actions[0][0]] + header[actions[0][1]:]
				}
			}
			parts = append(parts, label+" "+header+" "+strings.TrimSpace(cells[j]))
		}
		lines[i] = strings.Join(parts, "\n")
	}
	return strings.Join(lines, "\n")
}

func noticeQuestionTransitionValues(text string, values map[string]bool) bool {
	if !strings.Contains(text, "에서") || !strings.Contains(text, "변경") {
		return false
	}
	left, right, ok := strings.Cut(text, "에서")
	if !ok {
		return false
	}
	_, from := noticeQuestionCalendarDates(left)
	_, to := noticeQuestionCalendarDates(right)
	var a, b string
	for value := range from {
		if strings.HasPrefix(value, "MONTH_DAY:") {
			a = strings.TrimPrefix(value, "MONTH_DAY:")
		}
	}
	for value := range to {
		if strings.HasPrefix(value, "MONTH_DAY:") {
			b = strings.TrimPrefix(value, "MONTH_DAY:")
		}
	}
	if a == "" || b == "" {
		return false
	}
	values["FROM_MONTH_DAY:"+a], values["TO_MONTH_DAY:"+b] = true, true
	return true
}

// An omitted subject in a coordinated predicate inherits only the explicit
// leading actors, never their dates or another clause's stated subject.
func noticeQuestionLeadingActors(text string, identifiers map[string]bool) string {
	if location := noticeClaimAction.FindStringIndex(text); len(location) > 0 {
		text = text[:location[0]]
	}
	actors := []string{}
	for _, word := range noticeClaimTicker.FindAllString(text, -1) {
		if identifiers[word] {
			actors = append(actors, word)
		}
	}
	for _, name := range []string{"업비트", "빗썸", "코인원", "코빗", "고팍스"} {
		if strings.Contains(text, name) {
			actors = append(actors, name)
		}
	}
	return strings.Join(actors, " ")
}

func noticeQuestionHasExplicitActor(text string, identifiers map[string]bool) bool {
	for name := range identifiers {
		if noticeQuestionHasAnchor(text, name) {
			return true
		}
	}
	for _, name := range []string{"업비트", "빗썸", "코인원", "코빗", "고팍스"} {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}
