package notice

import (
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	plainBreakPattern = regexp.MustCompile(`(?i)<br\s*/?>|</(?:p|div|li|h[1-6]|tr)>`)
	plainTagPattern   = regexp.MustCompile(`<[^>]+>`)
	// A tilde is official range evidence (dates, hours and amounts), not
	// disposable Markdown decoration.
	plainMarkPattern  = regexp.MustCompile(`[*_` + "`" + `#>|]`)
	plainSpacePattern = regexp.MustCompile(`[ \t\r\f\v]+`)

	koreanDateTimePattern = regexp.MustCompile(
		`(?i)(?:(?P<year>20\d{2})\s*년\s*)?` +
			`(?P<month>\d{1,2})\s*월\s*(?P<day>\d{1,2})\s*일` +
			`(?:\s*\([^)]*\))?\s*` +
			`(?:(?P<ampm>오전|오후|AM|PM)\s*)?` +
			`(?P<hour>\d{1,2})(?:\s*[:시]\s*(?P<minute>\d{1,2}))?\s*분?`,
	)
	koreanDateOnlyPattern = regexp.MustCompile(
		`(?i)(?:(?P<year>20\d{2})\s*년\s*)?` +
			`(?P<month>\d{1,2})\s*월\s*(?P<day>\d{1,2})\s*일(?:\s*\([^)]*\))?`,
	)
	shortDateTimePattern = regexp.MustCompile(
		`(?i)(?:(?P<year>20\d{2}|\d{2})\s*[./]\s*)?` +
			`(?P<month>\d{1,2})\s*[./]\s*(?P<day>\d{1,2})(?:\s*\([^)]*\))?\s+` +
			`(?:(?P<ampm>오전|오후|AM|PM)\s*)?` +
			`(?P<hour>\d{1,2})(?::(?P<minute>\d{2})|\s*시(?:\s*(?P<minute_kor>\d{1,2})\s*분?)?)`,
	)
	shortDateOnlyPattern = regexp.MustCompile(
		`(?i)(?:(?P<year>20\d{2}|\d{2})\s*[./]\s*)?` +
			`(?P<month>\d{1,2})\s*[./]\s*(?P<day>\d{1,2})(?:\s*\([^)]*\))?`,
	)
	isoDateTimePattern = regexp.MustCompile(
		`(?P<year>20\d{2})-(?P<month>\d{1,2})-(?P<day>\d{1,2})` +
			`(?:\s*\([^)]*\))?(?:[T\s]+(?P<hour>\d{1,2}):(?P<minute>\d{2})(?::\d{2})?)?`,
	)
	englishDateTimePattern = regexp.MustCompile(
		`(?i)(?P<month>Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|` +
			`Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\.?\s+` +
			`(?P<day>\d{1,2})(?:st|nd|rd|th)?,?\s+(?P<year>20\d{2})` +
			`(?:,?\s+(?P<hour>\d{1,2})(?::(?P<minute>\d{2}))?\s*(?P<ampm>AM|PM))?`,
	)
	scheduleWhitespacePattern = regexp.MustCompile(`\s+`)
	scheduleTimezonePattern   = regexp.MustCompile(`(?i)\b(?:UTC|KST)\b`)
	scheduleUTCResource       = regexp.MustCompile(`(?i)\bUTC\b`)
	scheduleRecurringPattern  = regexp.MustCompile(`(?i)(?:매주|매일|매월|weekly|daily|monthly)`)
	scheduleChangedEndPattern = regexp.MustCompile(`(?i)(?:이벤트\s*)?기간\s*(?:\([^)]*(?:변경|연장|종료)[^)]*\)|변경|연장|종료)`)
	scheduleKoreanPattern     = regexp.MustCompile(`[가-힣]`)
)

var scheduleContextWords = []string{
	"입금", "출금", "거래", "상장", "유의", "경보", "점검", "업그레이드", "중단", "재개",
	"스냅샷", "스왑", "에어드랍", "개시", "시작", "종료", "기간", "예정", "일시", "효력", "지급",
	"매주", "매일", "매월", "deposit", "withdraw", "trading", "list", "warning", "caution",
	"maintenance", "upgrade", "suspend", "resume", "start", "begin", "end", "period", "until",
	"before", "from", "effective", "event", "campaign", "promotion", "competition", "reward",
	"distribution", "airdrop",
}

var eventContextWords = []string{
	"이벤트", "프로모션", "대회", "리워드", "에어드랍",
	"event", "campaign", "promotion", "competition", "reward", "airdrop",
}

var allowedScheduleKinds = map[string]struct{}{
	"TRADING": {}, "DEPOSIT": {}, "WITHDRAWAL": {}, "CAUTION": {}, "EVENT": {},
	"MAINTENANCE": {}, "LISTING": {}, "DELISTING": {}, "DEPOSIT_WITHDRAWAL": {},
	"NETWORK_UPGRADE": {}, "TOKEN_SWAP": {}, "REBRAND": {}, "SNAPSHOT": {}, "AIRDROP": {},
	"REWARD_DISTRIBUTION": {}, "ELIGIBILITY_DEADLINE": {},
}

var englishMonth = map[string]time.Month{
	"jan": time.January, "january": time.January,
	"feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March,
	"apr": time.April, "april": time.April,
	"may": time.May,
	"jun": time.June, "june": time.June,
	"jul": time.July, "july": time.July,
	"aug": time.August, "august": time.August,
	"sep": time.September, "september": time.September,
	"oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December,
}

type scheduleMatch struct {
	start       int
	end         int
	timestampMS int64
	hasTime     bool
}

type schedulePattern struct {
	pattern *regexp.Regexp
	english bool
}

func PlainText(value string) string {
	text := html.UnescapeString(value)
	text = plainBreakPattern.ReplaceAllString(text, "\n")
	text = plainTagPattern.ReplaceAllString(text, " ")
	text = plainMarkPattern.ReplaceAllString(text, " ")
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(plainSpacePattern.ReplaceAllString(line, " "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ExtractSchedules(content, category string, referenceTSMS int64) []Schedule {
	lines := strings.Split(PlainText(content), "\n")
	result := make([]Schedule, 0)
	for index, rawLine := range lines {
		line := normalizeScheduleLine(rawLine)
		if line == "" || !containsDigit(line) {
			continue
		}
		contextLine := line
		if !containsAnyFold(line, scheduleContextWords) && index > 0 {
			previous := normalizeScheduleLine(lines[index-1])
			if previous != "" && len([]rune(previous)) <= 160 && !containsDigit(previous) {
				contextLine = previous + ": " + line
			}
		}
		if !containsAnyFold(contextLine, scheduleContextWords) {
			continue
		}
		kinds := scheduleKinds(contextLine, category)
		if len(kinds) == 1 && kinds[0] == "EVENT" && category == "EVENT" &&
			!containsAnyFold(contextLine, eventContextWords) {
			continue
		}
		location, timezone := scheduleLocation(lines, index, line)
		matches := parseScheduleMatches(line, referenceTSMS, location)
		if len(matches) == 0 {
			if scheduleRecurringPattern.MatchString(contextLine) {
				result = appendRecurringSchedules(result, kinds, timezone, contextLine)
			}
			continue
		}
		result = appendTimedSchedules(result, kinds, matches, timezone, contextLine, category)
	}
	return uniqueSchedules(result)
}

func appendRecurringSchedules(result []Schedule, kinds []string, timezone, evidence string) []Schedule {
	for _, kind := range kinds {
		if _, allowed := allowedScheduleKinds[kind]; !allowed {
			continue
		}
		result = append(result, Schedule{
			Kind: kind, Timezone: timezone, Evidence: truncateRunes(evidence, 500), Precision: "RECURRING",
		})
	}
	return result
}

func appendTimedSchedules(
	result []Schedule,
	kinds []string,
	matches []scheduleMatch,
	timezone, evidence, category string,
) []Schedule {
	endOnly := len(matches) == 1 && isEventCategory(category) && scheduleChangedEndPattern.MatchString(evidence)
	hasDateOnly := false
	for _, match := range matches {
		hasDateOnly = hasDateOnly || !match.hasTime
	}
	precision := "SECOND"
	if hasDateOnly {
		precision = "DAY"
	}
	location := locationForSchedule(timezone)
	for _, kind := range kinds {
		if _, allowed := allowedScheduleKinds[kind]; !allowed {
			continue
		}
		start := matches[0].timestampMS
		end := int64(0)
		if endOnly {
			start = 0
			end = matches[0].timestampMS
			if !matches[0].hasTime {
				end = scheduleDayEnd(end, location)
			}
		} else if len(matches) > 1 && matches[1].timestampMS > matches[0].timestampMS {
			end = matches[1].timestampMS
			if !matches[1].hasTime {
				end = scheduleDayEnd(end, location)
			}
		} else if !matches[0].hasTime {
			end = scheduleDayEnd(start, location)
		}
		result = append(result, Schedule{
			Kind: kind, StartTSMS: start, EndTSMS: end, Timezone: timezone,
			Evidence: truncateRunes(evidence, 500), Precision: precision,
		})
	}
	return result
}

func parseScheduleMatches(line string, referenceTSMS int64, location *time.Location) []scheduleMatch {
	patterns := []schedulePattern{
		{pattern: isoDateTimePattern},
		{pattern: koreanDateTimePattern},
		{pattern: shortDateTimePattern},
		{pattern: koreanDateOnlyPattern},
		{pattern: shortDateOnlyPattern},
		{pattern: englishDateTimePattern, english: true},
	}
	matches := make([]scheduleMatch, 0, 2)
	for _, candidate := range patterns {
		for _, indexes := range candidate.pattern.FindAllStringSubmatchIndex(line, -1) {
			values := namedScheduleValues(candidate.pattern, line, indexes)
			if candidate.pattern == shortDateOnlyPattern && values["year"] == "" &&
				regexp.MustCompile(`^\d{1,2}\.\d{1,2}$`).MatchString(strings.TrimSpace(line[indexes[0]:indexes[1]])) {
				continue
			}
			if !scheduleBoundaries(line, indexes[0], indexes[1]) || overlapsScheduleMatch(matches, indexes[0], indexes[1]) {
				continue
			}
			timestamp := parsedScheduleTime(values, referenceTSMS, location, candidate.english)
			if timestamp <= 0 {
				continue
			}
			matches = append(matches, scheduleMatch{
				start: indexes[0], end: indexes[1], timestampMS: timestamp, hasTime: values["hour"] != "",
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
	return matches
}

func parsedScheduleTime(values map[string]string, referenceTSMS int64, location *time.Location, english bool) int64 {
	reference := time.Now().In(location)
	if referenceTSMS > 0 {
		reference = time.UnixMilli(referenceTSMS).In(location)
	}
	year := reference.Year()
	if values["year"] != "" {
		year, _ = strconv.Atoi(values["year"])
		if year < 100 {
			year += 2000
		}
	}
	month := time.Month(integerValue(values["month"]))
	if english {
		month = englishMonth[strings.ToLower(values["month"])]
	}
	day := integerValue(values["day"])
	hour := integerValue(values["hour"])
	minute := integerValue(firstNonEmpty(values["minute"], values["minute_kor"]))
	ampm := strings.ToUpper(values["ampm"])
	if (ampm == "PM" || ampm == "오후") && hour < 12 {
		hour += 12
	}
	if (ampm == "AM" || ampm == "오전") && hour == 12 {
		hour = 0
	}
	parsed := time.Date(year, month, day, hour, minute, 0, 0, location)
	if parsed.Year() != year || parsed.Month() != month || parsed.Day() != day ||
		parsed.Hour() != hour || parsed.Minute() != minute {
		return 0
	}
	return parsed.UnixMilli()
}

func namedScheduleValues(pattern *regexp.Regexp, line string, indexes []int) map[string]string {
	values := make(map[string]string)
	for index, name := range pattern.SubexpNames() {
		if index == 0 || name == "" || 2*index+1 >= len(indexes) {
			continue
		}
		start, end := indexes[2*index], indexes[2*index+1]
		if start >= 0 && end >= start {
			values[name] = line[start:end]
		}
	}
	return values
}

func scheduleKinds(line, category string) []string {
	text := strings.ToLower(line)
	if containsAnyFold(text, []string{"기준일", "까지 유지", "유지해야", "eligibility deadline", "must remain"}) {
		return []string{"ELIGIBILITY_DEADLINE"}
	}
	if category == "EVENT" && containsAnyFold(text, []string{"지급 일정", "지급 예정", "일괄 지급", "reward distribution", "reward payment"}) {
		return []string{"REWARD_DISTRIBUTION"}
	}
	if isEventCategory(category) && containsAnyFold(text, []string{"기간", "period", "시작", "종료", "start", "end"}) {
		if category == "AIRDROP" {
			return []string{"AIRDROP"}
		}
		return []string{"EVENT"}
	}
	if containsAnyFold(text, []string{"참여 기간", "participation period"}) {
		return []string{"EVENT"}
	}
	if containsAnyFold(text, eventContextWords) {
		if category == "AIRDROP" {
			return []string{"AIRDROP"}
		}
		return []string{"EVENT"}
	}
	if containsAnyFold(text, []string{"스냅샷", "snapshot"}) {
		if category == "SNAPSHOT" {
			return []string{"SNAPSHOT"}
		}
		return []string{"EVENT"}
	}
	if containsAnyFold(text, []string{"스왑", "swap", "migration"}) {
		if category == "TOKEN_SWAP" {
			return []string{"TOKEN_SWAP"}
		}
		return []string{"EVENT"}
	}
	if strings.Contains(text, "입출금") ||
		(containsAnyFold(text, []string{"입금", "deposit"}) && containsAnyFold(text, []string{"출금", "withdraw"})) {
		return []string{"DEPOSIT", "WITHDRAWAL"}
	}
	if containsAnyFold(text, []string{"거래지원", "거래 시작", "상장", "trading", "list"}) {
		if category == "DELISTING" && containsAnyFold(text, []string{"종료", "end", "termination"}) {
			return []string{"DELISTING"}
		}
		return []string{"TRADING"}
	}
	if containsAnyFold(text, []string{"입금", "deposit"}) {
		return []string{"DEPOSIT"}
	}
	if containsAnyFold(text, []string{"출금", "withdraw"}) {
		return []string{"WITHDRAWAL"}
	}
	if containsAnyFold(text, []string{"유의", "경보", "warning", "caution"}) {
		return []string{"CAUTION"}
	}
	if containsAnyFold(text, []string{"점검", "maintenance", "upgrade", "업그레이드"}) {
		if category == "NETWORK_UPGRADE" {
			return []string{"NETWORK_UPGRADE"}
		}
		return []string{"MAINTENANCE"}
	}
	return []string{category}
}

func scheduleLocation(lines []string, index int, line string) (*time.Location, string) {
	context := line
	if !scheduleTimezonePattern.MatchString(context) && !scheduleKoreanPattern.MatchString(line) {
		for previous := index - 1; previous >= 0; previous-- {
			if scheduleTimezonePattern.MatchString(lines[previous]) {
				context = lines[previous]
				break
			}
		}
	}
	if scheduleUTCResource.MatchString(context) {
		return time.UTC, "UTC"
	}
	return seoulNoticeLocation(), "Asia/Seoul"
}

func scheduleDayEnd(timestampMS int64, location *time.Location) int64 {
	value := time.UnixMilli(timestampMS).In(location)
	start := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, location)
	return start.AddDate(0, 0, 1).UnixMilli() - 1
}

func scheduleBoundaries(line string, start, end int) bool {
	for end > start && line[end-1] == ' ' {
		end--
	}
	return (start == 0 || !isASCIIAlphaNumeric(line[start-1])) &&
		(end == len(line) || !isASCIIAlphaNumeric(line[end]))
}

func overlapsScheduleMatch(matches []scheduleMatch, start, end int) bool {
	for _, match := range matches {
		if start < match.end && match.start < end {
			return true
		}
	}
	return false
}

func uniqueSchedules(values []Schedule) []Schedule {
	seen := make(map[string]struct{}, len(values))
	result := make([]Schedule, 0, len(values))
	for _, value := range values {
		key := value.Kind + "\x00" + strconv.FormatInt(value.StartTSMS, 10) + "\x00" +
			strconv.FormatInt(value.EndTSMS, 10) + "\x00" + value.Evidence + "\x00" + value.Precision
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeScheduleLine(value string) string {
	return strings.TrimSpace(scheduleWhitespacePattern.ReplaceAllString(value, " "))
}

func containsAnyFold(value string, words []string) bool {
	value = strings.ToLower(value)
	for _, word := range words {
		if strings.Contains(value, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func containsDigit(value string) bool {
	for _, character := range value {
		if character >= '0' && character <= '9' {
			return true
		}
	}
	return false
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func isEventCategory(category string) bool {
	return category == "EVENT" || category == "AIRDROP" || category == "SNAPSHOT"
}

func locationForSchedule(timezone string) *time.Location {
	if timezone == "UTC" {
		return time.UTC
	}
	return seoulNoticeLocation()
}

func integerValue(value string) int {
	result, _ := strconv.Atoi(strings.TrimSpace(value))
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	characters := []rune(value)
	if len(characters) <= limit {
		return value
	}
	return string(characters[:limit])
}
