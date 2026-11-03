package hermesrunner

import (
	"market-share-go/internal/notice"
	"regexp"
	"strings"
)

type workerScheduleBinding struct {
	stamp     int64
	end       bool
	precision string
}

func boundWorkerSchedule(s notice.Schedule, r analysisResult, e materializedEvidence) (workerScheduleBinding, bool) {
	if !notice.IsDomesticExchange(e.Item.Exchange) {
		return workerScheduleBinding{}, false
	}
	if !workerUnconditionalSourceLine(e.BodyText, strings.Split(strings.TrimSpace(s.Evidence), "\n")[0]) {
		return workerScheduleBinding{}, false
	}
	if stamp, ok := boundWorkerSuspensionTime(s, r, e); ok {
		return workerScheduleBinding{stamp: stamp, precision: "MINUTE"}, true
	}
	if stamp, ok := boundWorkerWithdrawalBlock(s, r, e); ok {
		return workerScheduleBinding{stamp: stamp, precision: "MINUTE"}, true
	}
	return boundWorkerPlannedEnd(s, r, e)
}

// Require the whole official line, not a substring with a condition trimmed
// off. Conservatively reject upstream conditional/revision context, including
// headings before the date block and following notes that explicitly govern
// it. Generic later boilerplate about possible changes remains distinct.
func workerUnconditionalSourceLine(body, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	lines := strings.Split(body, "\n")
	index, count := -1, 0
	for i, line := range lines {
		if strings.TrimSpace(line) == target {
			index = i
			count++
		}
	}
	if count != 1 {
		return false
	}
	prefix := strings.Join(lines[:index+1], "\n")
	for _, qualifier := range []string{"조건", "충족", "경우", "한하여", "한해", "전제", "변경", "정정", "취소", "수정", "가정", "검토", "잠정"} {
		if strings.Contains(prefix, qualifier) {
			return false
		}
	}
	insideBlock := true
	for _, raw := range lines[index+1:] {
		line := strings.Join(strings.Fields(raw), "")
		if line == "" || strings.HasPrefix(line, "■") {
			insideBlock = false
		}
		backReference := (strings.Contains(line, "일정") || strings.Contains(line, "종료일시")) &&
			(strings.Contains(line, "위") || strings.Contains(line, "상기") || strings.Contains(line, "해당") || strings.Contains(line, "본"))
		if !insideBlock && !backReference {
			continue
		}
		for _, qualifier := range []string{"조건", "충족", "경우", "한하여", "한해", "전제", "정정", "취소", "수정", "가정", "검토", "잠정"} {
			if strings.Contains(line, qualifier) {
				return false
			}
		}
		if strings.Contains(line, "변경") && !strings.Contains(line, "변경될수") && !strings.Contains(line, "변경될가능") {
			return false
		}
	}
	return true
}

func workerScheduleMatchesBinding(s notice.Schedule, r analysisResult, e materializedEvidence) bool {
	b, ok := boundWorkerSchedule(s, r, e)
	if !ok || !(strings.EqualFold(s.Timezone, "Asia/Seoul") || strings.EqualFold(s.Timezone, "KST")) {
		return false
	}
	precision := strings.EqualFold(s.Precision, b.precision)
	// Preserve validation of older single-clock results without producing new
	// SECOND claims for sources that only state minutes.
	if !precision && strings.EqualFold(s.Precision, "SECOND") {
		_, precision = boundWorkerSuspensionTime(s, r, e)
	}
	if !precision {
		return false
	}
	if b.end {
		return s.StartTSMS == 0 && normalizeTimestampMS(s.EndTSMS) == b.stamp
	}
	return s.EndTSMS == 0 && normalizeTimestampMS(s.StartTSMS) == b.stamp
}

func boundWorkerWithdrawalBlock(s notice.Schedule, r analysisResult, e materializedEvidence) (int64, bool) {
	kind := strings.ToUpper(strings.TrimSpace(s.Kind))
	if (kind != "SUSPENSION" && kind != "WITHDRAWAL_SUSPEND") || len(r.Assets) != 1 {
		return 0, false
	}
	excerpt := strings.TrimSpace(s.Evidence)
	if excerpt == "" || strings.Count(e.BodyText, excerpt) != 1 {
		return 0, false
	}
	lines := strings.Split(excerpt, "\n")
	if len(lines) != 3 {
		return 0, false
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	if !strings.HasPrefix(lines[0], "중단 일정:") || !strings.HasPrefix(lines[1], "중단 사유:") || !strings.HasPrefix(lines[2], "중단 영향:") {
		return 0, false
	}
	date := strings.TrimSpace(strings.TrimPrefix(lines[0], "중단 일정:"))
	if workerExplicitKST.FindString(date) != date {
		return 0, false
	}
	if !containsExplicitAsset(lines[1], r.Assets[0]) || !containsExplicitAsset(lines[2], r.Assets[0]) || !strings.Contains(lines[2], "출금 중단") {
		return 0, false
	}
	for _, bad := range []string{"입금", "입출금", "거래", "조건", "경우", "변경", "취소", "재개", "종료", "~", "→"} {
		if strings.Contains(excerpt, bad) {
			return 0, false
		}
	}
	assets := notice.TitleAssets(e.Title + "\n" + e.BodyText)
	if len(assets) != 1 || assets[0] != r.Assets[0] || !strings.Contains(e.Title, "출금") || strings.Contains(e.Title, "입금") || strings.Contains(e.Title, "입출금") || !strings.Contains(e.Title, "중단") {
		return 0, false
	}
	actionBound := false
	for _, a := range r.Actions {
		if a.Subject == "DEPOSIT_WITHDRAWAL" && a.Verb == "SUSPEND" && containsNormalizedEvidence(e.BodyText, a.Evidence) && containsExplicitAsset(a.Evidence, r.Assets[0]) && strings.Contains(a.Evidence, "출금") && !strings.Contains(a.Evidence, "입금") && !strings.Contains(a.Evidence, "입출금") && strings.Contains(a.Evidence, "중단") {
			actionBound = true
		}
	}
	if !actionBound {
		return 0, false
	}
	// An independently labelled completion update may coexist with the start.
	// It never supplies a timezone or endpoint to this suspension schedule.
	for _, line := range strings.Split(e.BodyText, "\n") {
		line = strings.TrimSpace(line)
		if line == lines[0] || !workerClock.MatchString(line) {
			continue
		}
		if !strings.HasPrefix(line, "업데이트:") || !strings.HasSuffix(line, "점검이 완료되었습니다.") || len(workerClock.FindAllString(line, -1)) != 1 {
			return 0, false
		}
	}
	return strictWorkerKSTTimestamp(date)
}

var workerPlannedEndKST = regexp.MustCompile(`^(20\d{2})\.\s*(\d{1,2})\.\s*(\d{1,2})\s*\(([월화수목금토일])\)\s*(\d{1,2})시(?:\s*(\d{1,2})분)?\s*\(KST\)\s*예정$`)

func boundWorkerPlannedEnd(s notice.Schedule, r analysisResult, e materializedEvidence) (workerScheduleBinding, bool) {
	prefix := ""
	subject := ""
	switch strings.ToUpper(strings.TrimSpace(s.Kind)) {
	case "TRADING_SUPPORT_END":
		prefix = "ㆍ거래(매수/매도) 종료 일시:"
		subject = "TRADING_SUPPORT"
	case "WITHDRAWAL_SUPPORT_END":
		prefix = "ㆍ출금지원 종료 일시:"
		subject = "DEPOSIT_WITHDRAWAL"
	default:
		return workerScheduleBinding{}, false
	}
	line := strings.TrimSpace(s.Evidence)
	if !strings.HasPrefix(line, prefix) || strings.Count(e.BodyText, line) != 1 || strings.ContainsAny(line, "\n\r") || len(r.Assets) == 0 {
		return workerScheduleBinding{}, false
	}
	match := workerPlannedEndKST.FindStringSubmatch(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
	if len(match) == 0 {
		return workerScheduleBinding{}, false
	}
	assets := notice.TitleAssets(e.Title)
	if len(assets) != len(normalizedStrings([]string(r.Assets))) {
		return workerScheduleBinding{}, false
	}
	for _, asset := range r.Assets {
		if !containsExplicitAsset(e.Title, asset) {
			return workerScheduleBinding{}, false
		}
	}
	groupBound, roleBound := false, false
	for _, a := range r.Actions {
		if !containsNormalizedCompositeEvidence(e.BodyText, a.Evidence) && !containsNormalizedEvidence(e.BodyText, a.Evidence) {
			continue
		}
		if a.Subject == "TRADING_SUPPORT" && a.Verb == "END" && strings.Contains(a.Evidence, "거래지원 종료를 결정") {
			all := true
			for _, asset := range r.Assets {
				all = all && containsExplicitAsset(a.Evidence, asset)
			}
			groupBound = groupBound || all
		}
		if a.Subject == subject && a.Verb == "END" {
			if subject == "TRADING_SUPPORT" {
				roleBound = roleBound || strings.Contains(a.Evidence, "거래지원 종료")
			} else {
				roleBound = roleBound || strings.TrimSpace(a.Evidence) == line
			}
		}
	}
	if !groupBound || !roleBound {
		return workerScheduleBinding{}, false
	}
	minute := match[6]
	precision := "MINUTE"
	if minute == "" {
		minute = "00"
		precision = "HOUR"
	}
	if len(minute) == 1 {
		minute = "0" + minute
	}
	hour := match[5]
	if len(hour) == 1 {
		hour = "0" + hour
	}
	date := match[1] + "년 " + match[2] + "월 " + match[3] + "일(" + match[4] + ") " + hour + ":" + minute + " (KST)"
	stamp, ok := strictWorkerKSTTimestamp(date)
	return workerScheduleBinding{stamp: stamp, end: true, precision: precision}, ok
}
