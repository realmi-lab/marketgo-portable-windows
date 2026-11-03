package app

import (
	"regexp"
	"strings"
)

var marketQuestionBindingGroupHeader = regexp.MustCompile(`(?i)^(?:의|은|는|이|가)?\s*(?:(?:현재|공식|rolling\s*24\s*시간|KRW|원화|거래대금|거래금액|점유율|비중|과|와|및|그리고)\s*)*(?:은|는|이|가)?\s*$`)
var marketQuestionBindingGroupMetric = regexp.MustCompile(`거래대금|거래금액|점유율|비중`)
var marketQuestionBindingGroupNumberStart = regexp.MustCompile(`^\s*(?:[(（]\s*)?(?:약\s*)?[+-]?[0-9]`)
var marketQuestionBindingGroupUnit = regexp.MustCompile(`(?i)^(?:%포인트|%p|%|퍼센트포인트|퍼센트|조원|억원|만원|원|KRW|건|개|회)`)
var marketQuestionBindingGroupJoin = regexp.MustCompile(`^\s*(?:[,·]|와|과|및|그리고)\s*`)
var marketQuestionBindingGroupEnd = regexp.MustCompile(`^\s*(?:$|입니다|이다|이며|이고|였|였다|이었|이었다|;|아닙|아니|보다|으로|로|를|을)`)
var marketQuestionBindingGroupNextSubject = regexp.MustCompile(`(?i)^(?:(?:현재|공식)\s*)*(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)\s*(?:은|는|이|가|의|거래대금|거래금액|점유율|비중)`)
var marketQuestionBindingGroupPriorComparison = regexp.MustCompile(`(?i)^\s*(?:%p|%|퍼센트포인트|퍼센트|조원|억원|만원|원|KRW)\s*보다\s*(?:크|작|높|낮)고\s*,\s*$`)

type marketQuestionGroupNumber struct {
	start int
	unit  string
}

func marketQuestionBindingGroupConflict(grouped string, other ...string) bool {
	if grouped != "" {
		for _, owner := range other {
			if owner != "" && owner != grouped {
				return true
			}
		}
	}
	return false
}

// Explicit "venues ... 각각 values" groups own source token positions, not
// sentence-wide ordinals. An empty map means no applicable group; false means
// a declared group is unsupported and must not fall back to the last venue.
// Tuple shape uses repeated metric/unit order, never the numeric values.
func marketQuestionBindingGroups(sentence string) (map[int]string, bool) {
	bindings := map[int]string{}
	for offset := 0; offset < len(sentence); {
		relative := strings.Index(sentence[offset:], "각각")
		if relative < 0 {
			break
		}
		marker := offset + relative
		offset = marker + len("각각")
		if hasValue, _ := marketQuestionBindingNumericKinds(sentence[offset:]); !hasValue {
			// Distributive qualitative prose declares no numeric group.
			continue
		}
		head := marketQuestionBindingLocalNumericPrefix(sentence[:marker])
		venues := marketQuestionBindingVenue.FindAllStringIndex(head, -1)
		if len(venues) == 0 {
			continue
		}
		last := venues[len(venues)-1]
		header := head[last[1]:]
		start := len(venues) - 1
		for start > 0 && marketQuestionBindingAggregateSeparator.MatchString(head[venues[start-1][1]:venues[start][0]]) {
			start--
		}
		if !marketQuestionBindingGroupHeader.MatchString(header) {
			// A declared multi-venue group with unsupported modifiers cannot
			// silently fall back to its last recognized venue. Nor can an
			// unknown trailing member disappear from a declared list.
			if len(venues)-start >= 2 || marketQuestionBindingUnknownListTail.MatchString(header) {
				return nil, false
			}
			continue
		}
		venues = venues[start:]
		leading := head[:venues[0][0]]
		if marketQuestionBindingLeadingListJoin.MatchString(leading) && !marketQuestionBindingLeadingClauseEnd.MatchString(leading) && !marketQuestionBindingGroupPriorComparison.MatchString(leading) {
			return nil, false
		}
		if operands := marketQuestionBindingVenue.FindAllStringIndex(leading, -1); len(operands) > 0 && marketQuestionBindingExcludedOperand.MatchString(leading[operands[len(operands)-1][1]:]) {
			return nil, false
		}
		if len(venues) < 2 {
			continue
		}
		if !marketQuestionBindingGroupNumberStart.MatchString(sentence[offset:]) {
			// Explicit subjects remain direct claims. An otherwise declared
			// numeric group with malformed punctuation must not disappear.
			if marketQuestionBindingGroupNextSubject.MatchString(strings.TrimSpace(sentence[offset:])) {
				continue
			}
			if hasValue, _ := marketQuestionBindingNumericKinds(sentence[offset:]); hasValue {
				return nil, false
			}
			continue
		}
		names, seen := []string{}, map[string]bool{}
		for _, at := range venues {
			name := marketQuestionExchangeNames[strings.ToLower(head[at[0]:at[1]])]
			if name == "" || seen[name] {
				return nil, false
			}
			names, seen[name] = append(names, name), true
		}
		numbers, tupleSizes, ok := marketQuestionBindingGroupNumbers(sentence, offset)
		if !ok || len(numbers) == 0 || len(numbers)%len(names) != 0 {
			return nil, false
		}
		width := len(numbers) / len(names)
		if width < 1 || width > 2 {
			return nil, false
		}
		if len(tupleSizes) > 0 {
			if len(tupleSizes) != len(names) {
				return nil, false
			}
			for _, size := range tupleSizes {
				if size != width {
					return nil, false
				}
			}
		}
		declared := []string{}
		for _, label := range marketQuestionBindingGroupMetric.FindAllString(header, -1) {
			unit := "PCT"
			if label == "거래대금" || label == "거래금액" {
				unit = "KRW"
			}
			declared = append(declared, unit)
		}
		if len(declared) > 0 && len(declared) != width {
			return nil, false
		}
		pattern := make([]string, width)
		for i, number := range numbers {
			unit := number.unit
			if strings.HasPrefix(unit, "KRW") {
				unit = "KRW"
			}
			column := i % width
			if i < width {
				pattern[column] = unit
			}
			if unit != pattern[column] || len(declared) > 0 && unit != declared[column] {
				return nil, false
			}
			bindings[number.start] = names[i/width]
		}
		if width == 2 && !((pattern[0] == "KRW" && pattern[1] == "PCT") || (pattern[0] == "PCT" && pattern[1] == "KRW")) {
			return nil, false
		}
	}
	return bindings, true
}

// Consume the entire syntactic number list before checking arity. Stopping
// after a requested number of values would silently accept an extra tuple.
func marketQuestionBindingGroupNumbers(sentence string, start int) ([]marketQuestionGroupNumber, []int, bool) {
	var numbers []marketQuestionGroupNumber
	var tupleSizes []int
	position := start
	trimSpace := func() { position = len(sentence) - len(strings.TrimLeft(sentence[position:], " \t\r\n")) }
	trimSpace()
	parenthesized := strings.HasPrefix(sentence[position:], "(") || strings.HasPrefix(sentence[position:], "（")
	closing, tupleStart := "", 0
	openTuple := func() bool {
		trimSpace()
		for _, pair := range [][2]string{{"(", ")"}, {"（", "）"}} {
			if strings.HasPrefix(sentence[position:], pair[0]) {
				position += len(pair[0])
				closing, tupleStart = pair[1], len(numbers)
				return true
			}
		}
		return false
	}
	if parenthesized && !openTuple() {
		return nil, nil, false
	}
	for {
		for position < len(sentence) {
			trimSpace()
			if strings.HasPrefix(sentence[position:], "약") {
				position += len("약")
				continue
			}
			break
		}
		at := marketQuestionNumericToken.FindStringIndex(sentence[position:])
		if at == nil || at[0] != 0 {
			return nil, nil, false
		}
		numberStart := position
		position += at[1]
		unitText := marketQuestionBindingGroupUnit.FindString(sentence[position:])
		if unitText == "" {
			return nil, nil, false
		}
		numbers = append(numbers, marketQuestionGroupNumber{numberStart, marketQuestionNumericUnit(sentence[position:])})
		position += len(unitText)
		trimSpace()
		tupleClosed := false
		if parenthesized && strings.HasPrefix(sentence[position:], closing) {
			position += len(closing)
			tupleSizes = append(tupleSizes, len(numbers)-tupleStart)
			tupleClosed = true
		}
		if (!parenthesized || tupleClosed) && marketQuestionBindingGroupEnd.MatchString(sentence[position:]) {
			return numbers, tupleSizes, true
		}
		join := marketQuestionBindingGroupJoin.FindString(sentence[position:])
		if join == "" {
			return nil, nil, false
		}
		position += len(join)
		if (!parenthesized || tupleClosed) && strings.Contains(join, ",") && marketQuestionBindingGroupNextSubject.MatchString(sentence[position:]) {
			return numbers, tupleSizes, true
		}
		if tupleClosed && !openTuple() {
			return nil, nil, false
		}
	}
}
