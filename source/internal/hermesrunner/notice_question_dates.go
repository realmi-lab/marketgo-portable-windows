package hermesrunner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	noticeAnswerCalendarDate = regexp.MustCompile(`([12][0-9]{3})\s*(?:[-./]|년)\s*([0-9]{1,2})\s*(?:[-./]|월)\s*([0-9]{1,2})(?:일)?`)
	noticeAnswerMonthDay     = regexp.MustCompile(`([0-9]{1,2})\s*월\s*([0-9]{1,2})\s*일`)
	noticeAnswerSlashDay     = regexp.MustCompile(`([0-9]{1,2})/([0-9]{1,2})(?:[\t ]*\([월화수목금토일]\))?[\t ]+`)
	noticeAnswerClock        = regexp.MustCompile(`(?:(오전|오후)\s*)?([0-9]{1,2})\s*(?::\s*([0-9]{2})(?:\s*:\s*([0-9]{2}))?|시(?:\s*([0-9]{1,2})\s*분)?(?:\s*([0-9]{1,2})\s*초)?)`)
)

// Calendar dates are identities, not a bag of reusable month/day numbers.
// Normalize only valid full dates, allowing equivalent official formats while
// rejecting a new date assembled from components of different official dates.
func noticeQuestionCalendarDates(text string) (string, map[string]bool) {
	text = noticeQuestionGroundingText(text)
	text = noticeQuestionClockQualifiedSlashDates(text)
	dates := map[string]bool{}
	// Extract clocks while calendar suffixes still delimit their numbers.
	// Replacing "9월 5일" first would manufacture "5 시" in "9월 5일 시작".
	text = noticeQuestionNormalizeClockTuples(text, dates)
	normalized := noticeAnswerCalendarDate.ReplaceAllStringFunc(text, func(raw string) string {
		parts := noticeAnswerCalendarDate.FindStringSubmatch(raw)
		year, _ := strconv.Atoi(parts[1])
		month, _ := strconv.Atoi(parts[2])
		day, _ := strconv.Atoi(parts[3])
		date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if date.Year() != year || int(date.Month()) != month || date.Day() != day {
			dates["INVALID:"+raw] = true
			return raw
		}
		dates[date.Format("2006-01-02")] = true
		dates["MONTH_DAY:"+date.Format("01-02")] = true
		return fmt.Sprintf("%d %d %d", year, month, day)
	})
	normalized = noticeAnswerMonthDay.ReplaceAllStringFunc(normalized, func(raw string) string {
		parts := noticeAnswerMonthDay.FindStringSubmatch(raw)
		month, _ := strconv.Atoi(parts[1])
		day, _ := strconv.Atoi(parts[2])
		// A yearless February 29 is valid; do not invent a year or rollover.
		date := time.Date(2000, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if int(date.Month()) != month || date.Day() != day {
			dates["INVALID:"+raw] = true
			return raw
		}
		dates["MONTH_DAY:"+date.Format("01-02")] = true
		return fmt.Sprintf("%d %d", month, day)
	})
	return normalized, dates
}

// A slash date immediately followed by a clock is a schedule, not two
// independently reusable numbers. Keep yearless dates yearless and leave
// fractions / ratios without a clock unchanged.
func noticeQuestionClockQualifiedSlashDates(text string) string {
	var normalized strings.Builder
	cursor := 0
	for _, span := range noticeAnswerSlashDay.FindAllStringSubmatchIndex(text, -1) {
		start, end := span[0], span[1]
		if start > 0 && (text[start-1] >= '0' && text[start-1] <= '9' || strings.ContainsRune("./-", rune(text[start-1]))) {
			continue
		}
		clock := noticeAnswerClock.FindStringIndex(text[end:])
		if len(clock) == 0 || clock[0] != 0 {
			continue
		}
		normalized.WriteString(text[cursor:start])
		normalized.WriteString(text[span[2]:span[3]] + "월 " + text[span[4]:span[5]] + "일" + text[span[5]:end])
		cursor = end
	}
	normalized.WriteString(text[cursor:])
	return normalized.String()
}

// A clock's hour/minute/second are one value. Normalize ordinary Korean and
// colon notation without converting timezones or binding the clock to an event.
func noticeQuestionNormalizeClockTuples(text string, values map[string]bool) string {
	var normalized strings.Builder
	cursor := 0
	calendarSpans := noticeAnswerCalendarDate.FindAllStringIndex(text, -1)
	for _, match := range noticeAnswerClock.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		inCalendar := false
		for _, span := range calendarSpans {
			if start >= span[0] && start < span[1] {
				inCalendar = true
				break
			}
		}
		if inCalendar {
			continue
		}
		if start > 0 && (text[start-1] >= '0' && text[start-1] <= '9' || text[start-1] == ':') {
			continue
		}
		if end < len(text) && (text[end] >= '0' && text[end] <= '9' || text[end] == ':') {
			values["INVALID:"+text[start:end+1]] = true
			continue
		}
		// "3시간" is a duration, not the clock "3시".
		if strings.HasPrefix(text[end:], "간") && strings.HasSuffix(text[start:end], "시") {
			continue
		}
		part := func(group int) string {
			if match[group*2] < 0 {
				return ""
			}
			return text[match[group*2]:match[group*2+1]]
		}
		hour, _ := strconv.Atoi(part(2))
		minuteText, secondText := part(3), part(4)
		if minuteText == "" {
			minuteText, secondText = part(5), part(6)
		}
		minute, _ := strconv.Atoi(minuteText)
		second, _ := strconv.Atoi(secondText)
		meridiem := part(1)
		valid := minute <= 59 && second <= 59 && hour <= 24 && (hour != 24 || minute == 0 && second == 0)
		if meridiem != "" {
			valid = valid && hour <= 12
			hour %= 12
			if meridiem == "오후" {
				hour += 12
			}
		}
		normalized.WriteString(text[cursor:start])
		if valid {
			values[fmt.Sprintf("CLOCK:%02d:%02d:%02d", hour, minute, second)] = true
			normalized.WriteString(fmt.Sprintf("%d %d %d", hour, minute, second))
		} else {
			values["INVALID:"+text[start:end]] = true
			normalized.WriteString(text[start:end])
		}
		cursor = end
	}
	normalized.WriteString(text[cursor:])
	return normalized.String()
}
