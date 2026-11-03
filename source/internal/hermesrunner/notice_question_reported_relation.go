package hermesrunner

import (
	"regexp"
	"strings"
)

const noticeRelationDateToken = `(?:[12][0-9]{3}\s*(?:년|[-./])\s*[0-9]{1,2}\s*(?:월|[-./])\s*[0-9]{1,2}일?|[0-9]{1,2}\s*월\s*[0-9]{1,2}\s*일)`
const noticeRelationClockToken = `(?:(?:오전|오후)\s*)?[0-9]{1,2}(?::[0-9]{2}(?::[0-9]{2})?|시(?:\s*[0-9]{1,2}분(?:\s*[0-9]{1,2}초)?)?)`
const noticeRelationEndpointToken = noticeRelationDateToken + `(?:\s*(?:\([월화수목금토일]\))?\s*` + noticeRelationClockToken + `(?:\s*\((?:KST|UTC|GMT)\))?)?`

// This recognizes a reporting relationship, not an assertion that a service
// began/stopped at an observed execution time. Unknown constructions remain
// subject to the ordinary hard-fact gate; no answer wording is synthesized.
var noticeReportedStopAssociation = regexp.MustCompile(`(?m)^[\t ]*(?P<subject>[가-힣A-Z \t]{0,60}?)(?P<report>` + noticeRelationEndpointToken + `)[\t ]*완료[\t ]*(?:문구|안내|공지|보고)(?:는|은|가)[\t ]*(?P<plan>` + noticeRelationEndpointToken + `)(?:부터[\t ]*시작된[\t ]*이전[\t ]*(?:입출금[\t ]*)?중단[\t ]*건|[\t ]*(?:입출금[\t ]*)?중단[\t ]*일정)에[\t ]*관한[\t ]*(?:것|내용|안내)입니다\.(?:[\t ]*따라서 이를[\t ]*(?P<later>` + noticeRelationEndpointToken + `)[\t ]*시작 건의 재개 근거로 볼 수는 없습니다\.)?`)

func noticeQuestionProjectReportedStopRelations(text string, sources []noticeQuestionEventRelationSource) string {
	var projected strings.Builder
	cursor := 0
	for _, span := range noticeReportedStopAssociation.FindAllStringIndex(text, -1) {
		projected.WriteString(text[cursor:span[0]])
		scope := noticeQuestionRelationScopeAt(text[:span[0]], sources)
		projected.WriteString(noticeQuestionProjectReportedStopStatement(text[span[0]:span[1]], sources, scope))
		cursor = span[1]
	}
	projected.WriteString(text[cursor:])
	return projected.String()
}

func noticeQuestionProjectReportedStopStatement(statement string, sources []noticeQuestionEventRelationSource, scope noticeQuestionRelationScope) string {
	parts := noticeReportedStopAssociation.FindStringSubmatch(statement)
	value := func(name string) string { return parts[noticeReportedStopAssociation.SubexpIndex(name)] }
	endpoint := func(role, raw string) noticeQuestionEventEndpoint {
		_, values := noticeQuestionCalendarDates(raw)
		zone := ""
		for _, z := range []string{"KST", "UTC", "GMT"} {
			if strings.Contains(raw, z) {
				zone = z
			}
		}
		return noticeQuestionEventEndpoint{Role: role, Values: values, Passage: raw, Zone: zone}
	}
	planned, reported := endpoint("PLANNED_STOP", value("plan")), endpoint("COMPLETION_REPORT", value("report"))
	matches := 0
	for _, source := range sources {
		relation, ok := noticeQuestionUniqueStopReport(source)
		if !ok || !noticeQuestionRelationSubjectMatches(value("subject"), source) || !noticeQuestionRelationScopeMatches(scope, value("subject"), source, sources) {
			continue
		}
		if noticeQuestionEventRelationMatches(relation, source.SourceID, source.Exchange, source.Assets[0], planned, reported) {
			if value("later") != "" && !noticeQuestionReportPrecedesOtherPlan(relation, endpoint("PLANNED_STOP", value("later")), sources) {
				continue
			}
			matches++
		}
	}
	if matches != 1 {
		return statement
	}
	// Only this independently bound reporting relation leaves the projection.
	// Original text still reaches numerical checks and the mandatory full audit.
	return ""
}

// An earlier completion report cannot establish resumption of a distinct,
// later scheduled suspension. This does not assert nonresumption or classify
// every prior notice as a separate event.
func noticeQuestionReportPrecedesOtherPlan(relation noticeQuestionEventRelation, claim noticeQuestionEventEndpoint, sources []noticeQuestionEventRelationSource) bool {
	matches := 0
	for _, source := range sources {
		if source.SourceID == "" || source.SourceID == relation.SourceID || source.Exchange != relation.Exchange || len(source.Assets) != 1 || source.Assets[0] != relation.Asset || source.BodyScope != "FULL_STORED_ORIGINAL" || (source.BodyTruncated != nil && *source.BodyTruncated) || !strings.Contains(source.Title, "입출금") || !strings.Contains(source.Title, "중단") {
			continue
		}
		plans := map[string]noticeQuestionEventEndpoint{}
		ambiguous := false
		for _, body := range source.Passages {
			for _, line := range strings.Split(body, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "중단 일정:") {
					_, values := noticeQuestionCalendarDates(line)
					if len(values) > 0 && (strings.Contains(line, "중단") || strings.Contains(line, "완료") || strings.Contains(line, "재개")) {
						ambiguous = true
					}
					continue
				}
				_, values := noticeQuestionCalendarDates(line)
				dates, clocks := noticeAnswerCalendarDate.FindAllStringIndex(line, -1), noticeAnswerClock.FindAllStringIndex(line, -1)
				if len(dates) != 1 || len(clocks) != 1 || values["CLOCK:24:00:00"] || noticeQuestionEndpointOrder(noticeQuestionEventEndpoint{Values: values}) == "" {
					ambiguous = true
					continue
				}
				if clocks[0][0] < dates[0][1] || strings.TrimSpace(line[:dates[0][0]]) != "중단 일정:" || !noticeRelationWeekdayGap.MatchString(line[dates[0][1]:clocks[0][0]]) || strings.TrimSpace(line[clocks[0][1]:]) != "(KST)" {
					ambiguous = true
					continue
				}
				plans[line] = noticeQuestionEventEndpoint{Role: "PLANNED_STOP", Values: values, Passage: line, Zone: "KST"}
			}
		}
		if ambiguous || len(plans) != 1 {
			continue
		}
		for _, plan := range plans {
			if noticeQuestionEventEndpointMatches(claim, plan) && noticeQuestionEndpointOrder(plan) > noticeQuestionEndpointOrder(relation.CompletionReport) {
				matches++
			}
		}
	}
	return matches == 1
}
func noticeQuestionRelationSubjectMatches(text string, source noticeQuestionEventRelationSource) bool {
	names := map[string]string{"coinone": "코인원", "bithumb": "빗썸", "upbit": "업비트", "korbit": "코빗", "gopax": "고팍스"}
	for _, token := range strings.Fields(text) {
		if token == "별도의" || token == "과거" || token == "이전" {
			continue
		}
		token = strings.TrimSuffix(token, "의")
		if token != source.Assets[0] && token != names[source.Exchange] {
			return false
		}
	}
	return true
}
