package hermesrunner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/notice"
)

// Deliberately narrow: explicit year, minute and KST, with no inferred year,
// timezone, range endpoint or event completion. More complex schedules remain
// unavailable until their roles can be bound by an equally strict parser.
var workerExplicitKST = regexp.MustCompile(`(?i)(20\d{2})\s*년\s*(\d{1,2})\s*월\s*(\d{1,2})\s*일(?:\s*\([월화수목금토일](?:요일)?\))?\s*(\d{1,2}):(\d{2})\s*\(?KST\)?`)
var workerClock = regexp.MustCompile(`\d{1,2}:\d{2}`)
var workerWeekday = regexp.MustCompile(`일\s*\(([월화수목금토일])(?:요일)?\)`)

func normalizeMissingWorkerScheduleTimes(result analysisResult, source materializedEvidence) (analysisResult, error) {
	if !notice.IsDomesticExchange(source.Item.Exchange) {
		return result, nil
	}
	result.Schedules = append([]notice.Schedule(nil), result.Schedules...)
	normalized, unavailable := false, false
	for i, s := range result.Schedules {
		uncertain := s.StartTSMS == 0 && s.EndTSMS == 0 || strings.EqualFold(s.Precision, "APPROXIMATE") || strings.EqualFold(s.Precision, "UNKNOWN")
		if !uncertain {
			continue
		}
		binding, ok := boundWorkerSchedule(s, result, source)
		if ok {
			start, end := binding.stamp, int64(0)
			if binding.end {
				start, end = 0, binding.stamp
			}
			if s.StartTSMS != 0 && normalizeTimestampMS(s.StartTSMS) != start || s.EndTSMS != 0 && normalizeTimestampMS(s.EndTSMS) != end {
				return analysisResult{}, fmt.Errorf("schedule[%d] supplied time conflicts with bound official event", i)
			}
			s.StartTSMS, s.EndTSMS, s.Timezone, s.Precision = start, end, "Asia/Seoul", binding.precision
			normalized = true
		} else if s.StartTSMS == 0 && s.EndTSMS == 0 {
			// Zero is only the existing wire representation of unavailable time;
			// UNKNOWN makes that state explicit and positive-time consumers exclude it.
			s.StartTSMS, s.EndTSMS, s.Precision = 0, 0, "UNKNOWN"
			unavailable = true
		}
		result.Schedules[i] = s
	}
	markers := []string{}
	if normalized {
		markers = append(markers, "SERVER_SCHEDULE_TIME_NORMALIZED_V1")
	}
	if unavailable {
		markers = append(markers, "SERVER_SCHEDULE_TIME_UNAVAILABLE_V1")
	}
	result.scheduleNormalization = strings.Join(markers, "+")
	return result, nil
}

func boundWorkerSuspensionTime(s notice.Schedule, result analysisResult, source materializedEvidence) (int64, bool) {
	if strings.ToUpper(strings.TrimSpace(s.Kind)) != "DEPOSIT_WITHDRAWAL_SUSPEND" || len(result.Assets) != 1 {
		return 0, false
	}
	line := strings.TrimSpace(s.Evidence)
	body := source.BodyText
	// Composite excerpts, repeated dates, ranges and conditional/revised times
	// cannot borrow the title's event or asset identity.
	if strings.ContainsAny(line, "\n\r") || strings.Count(body, line) != 1 || line == "" {
		return 0, false
	}
	for _, word := range []string{"또는", "변경", "기존", "취소", "확인 후", "확인 시", "조건", "경우", "예정이었", "재개", "종료", "까지", "UTC", "~", "→"} {
		if strings.Contains(line, word) {
			return 0, false
		}
	}
	matches := workerExplicitKST.FindAllStringSubmatch(line, -1)
	if len(matches) != 1 || len(workerExplicitKST.FindAllString(body, -1)) != 1 || len(workerClock.FindAllString(body, -1)) != 1 {
		return 0, false
	}
	assets := notice.TitleAssets(source.Title + "\n" + body)
	if len(assets) != 1 || assets[0] != result.Assets[0] {
		return 0, false
	}
	titleActions := notice.InferTitleActions(source.Title, "")
	if !notice.HasAction(titleActions, "DEPOSIT_WITHDRAWAL", "SUSPEND") {
		return 0, false
	}
	actionBound := false
	for _, action := range result.Actions {
		if action.Subject == "DEPOSIT_WITHDRAWAL" && action.Verb == "SUSPEND" &&
			containsNormalizedEvidence(body, action.Evidence) && containsExplicitAsset(action.Evidence, result.Assets[0]) &&
			strings.Contains(action.Evidence, "입출금") && (strings.Contains(action.Evidence, "중단") || strings.Contains(action.Evidence, "중지")) {
			actionBound = true
		}
	}
	if !actionBound {
		return 0, false
	}
	// Either the dated line names the same asset and suspension itself, or an
	// exact 'suspension schedule' field immediately follows the transfer context.
	direct := containsExplicitAsset(line, result.Assets[0]) && strings.Contains(line, "입출금") && (strings.Contains(line, "중단") || strings.Contains(line, "중지")) && !strings.Contains(line, "거래")
	if !direct {
		rest := strings.TrimSpace(strings.TrimPrefix(line, "중단 일정:"))
		if rest == line || rest != matches[0][0] {
			return 0, false
		}
		offset := strings.Index(body, line)
		prefix := strings.TrimSpace(body[:offset])
		if index := strings.LastIndex(prefix, "\n"); index >= 0 {
			prefix = strings.TrimSpace(prefix[index+1:])
		}
		if !strings.Contains(prefix, "입출금") || strings.Contains(prefix, "거래") || strings.Contains(prefix, "재개") {
			return 0, false
		}
	}
	return strictWorkerKSTTimestamp(line)
}

func strictWorkerKSTTimestamp(line string) (int64, bool) {
	matches := workerExplicitKST.FindAllStringSubmatch(line, -1)
	if len(matches) != 1 || strings.Contains(strings.ToUpper(line), "UTC") {
		return 0, false
	}
	values := make([]int, 5)
	for i := range values {
		values[i], _ = strconv.Atoi(matches[0][i+1])
	}
	if values[3] > 23 || values[4] > 59 {
		return 0, false
	}
	stamp := time.Date(values[0], time.Month(values[1]), values[2], values[3], values[4], 0, 0, time.FixedZone("KST", 9*3600))
	if stamp.Year() != values[0] || int(stamp.Month()) != values[1] || stamp.Day() != values[2] {
		return 0, false
	}
	if weekday := workerWeekday.FindStringSubmatch(matches[0][0]); len(weekday) > 1 && weekday[1] != string([]rune("일월화수목금토")[stamp.Weekday()]) {
		return 0, false
	}
	return stamp.UnixMilli(), true
}
