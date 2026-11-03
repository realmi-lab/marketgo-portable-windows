package hermesrunner

import (
	"regexp"
	"strings"
)

var noticeRelationWeekdayGap = regexp.MustCompile(`^\s*(?:\([월화수목금토일]\))?\s*$`)
var noticeRelationTransferResumption = regexp.MustCompile(`^입출금\s+서비스\s+재개까지\s+기다려주신\s+고객님께\s+감사드립니다\.$`)

// A relation records an official notice's plan and later completion report.
// The update clock is a reporting clock, never an actual execution endpoint.
// It cannot be combined with another notice, even for the same ticker.
type noticeQuestionEventEndpoint struct {
	Role    string
	Values  map[string]bool
	Passage string
	Zone    string
}
type noticeQuestionEventRelation struct {
	SourceID, Exchange, Asset, Event string
	PlannedStop, CompletionReport    noticeQuestionEventEndpoint
}

// Decode this from the original notice row, not noticeQuestionClaimSources:
// that representation flattens snippets and metadata into its Passages.
type noticeQuestionEventRelationSource struct {
	SourceID      string   `json:"source_id"`
	Exchange      string   `json:"exchange"`
	Assets        []string `json:"assets"`
	Title         string   `json:"title"`
	BodyScope     string   `json:"body_scope"`
	BodyTruncated *bool    `json:"body_truncated"`
	Passages      []string `json:"body_passages"`
}

func noticeQuestionUniqueStopReport(source noticeQuestionEventRelationSource) (noticeQuestionEventRelation, bool) {
	relation := noticeQuestionEventRelation{}
	if source.SourceID == "" || source.Exchange == "" || len(source.Assets) != 1 || source.BodyScope != "FULL_STORED_ORIGINAL" || (source.BodyTruncated != nil && *source.BodyTruncated) {
		return relation, false
	}
	if !strings.Contains(source.Title, "입출금") || !strings.Contains(source.Title, "중단") {
		return relation, false
	}
	plans, reports := map[string]noticeQuestionEventEndpoint{}, map[string]noticeQuestionEventEndpoint{}
	for _, passage := range source.Passages {
		lines := strings.Split(passage, "\n")
		for lineIndex, line := range lines {
			line = strings.TrimSpace(line)
			if (strings.Contains(line, "입출금") || strings.Contains(line, "입금") || strings.Contains(line, "출금")) && (strings.Contains(line, "진행중") || strings.Contains(line, "진행 중") || strings.Contains(line, "계속") || strings.Contains(line, "완료되지") || strings.Contains(line, "미완료")) {
				return relation, false
			}
			role := ""
			if strings.HasPrefix(line, "중단 일정:") {
				role = "PLANNED_STOP"
			}
			if strings.HasPrefix(line, "업데이트:") && strings.Contains(line, "점검이 완료되었습니다.") {
				role = "COMPLETION_REPORT"
			}
			if role == "" {
				_, values := noticeQuestionCalendarDates(line)
				if len(values) > 0 && (strings.Contains(line, "중단") || strings.Contains(line, "완료") || strings.Contains(line, "재개")) {
					return relation, false
				}
				continue
			}
			// A labelled update timestamp must directly introduce the completion
			// statement. Do not attach a detached date or another service's
			// maintenance to this transfer event merely through the notice title.
			dateSpans := noticeAnswerCalendarDate.FindAllStringIndex(line, -1)
			clockSpans := noticeAnswerClock.FindAllStringIndex(line, -1)
			if len(dateSpans) != 1 || len(clockSpans) != 1 {
				return relation, false
			}
			dateSpan, clockSpan := dateSpans[0], clockSpans[0]
			if clockSpan[0] < dateSpan[1] || !noticeRelationWeekdayGap.MatchString(line[dateSpan[1]:clockSpan[0]]) {
				return relation, false
			}
			prefix, suffix := strings.TrimSpace(line[:dateSpan[0]]), strings.TrimSpace(line[clockSpan[1]:])
			if role == "PLANNED_STOP" {
				if prefix != "중단 일정:" || suffix != "(KST)" {
					return relation, false
				}
			} else {
				if prefix != "업데이트:" {
					return relation, false
				}
				switch suffix {
				case "(KST) 입출금 점검이 완료되었습니다.", "(KST) 입출금 서비스 점검이 완료되었습니다.":
				case "(KST) 점검이 완료되었습니다.":
					if lineIndex+1 >= len(lines) || !noticeRelationTransferResumption.MatchString(strings.TrimSpace(lines[lineIndex+1])) {
						return relation, false
					}
				default:
					return relation, false
				}
			}
			// An endpoint must be an assertion, not a condition, quote or denial.
			if strings.ContainsAny(line, "?？\"“”") || strings.Contains(line, "않") || strings.Contains(line, "아니") || strings.Contains(line, "만약") || strings.Contains(line, "경우") || strings.Contains(line, "완료되면") {
				return relation, false
			}
			_, values := noticeQuestionCalendarDates(line)
			dates, clocks, fullDates := 0, 0, 0
			for value := range values {
				if strings.HasPrefix(value, "INVALID:") {
					return relation, false
				}
				if strings.HasPrefix(value, "MONTH_DAY:") {
					dates++
				}
				if strings.HasPrefix(value, "CLOCK:") {
					// This helper does not normalize date rollover. The shared
					// calendar parser may support24:00 elsewhere, but it cannot
					// establish strict endpoint ordering here.
					if strings.HasPrefix(value, "CLOCK:24:") {
						return relation, false
					}
					clocks++
				}
				if len(value) == 10 && value[4] == '-' && value[7] == '-' {
					fullDates++
				}
			}
			if dates != 1 || clocks != 1 || fullDates != 1 {
				return relation, false
			}
			if !strings.Contains(line, "(KST)") {
				return relation, false
			}
			endpoint := noticeQuestionEventEndpoint{Role: role, Values: values, Passage: line, Zone: "KST"}
			if role == "PLANNED_STOP" {
				plans[line] = endpoint
			} else {
				reports[line] = endpoint
			}
		}
	}
	if len(plans) != 1 || len(reports) != 1 {
		return relation, false
	}
	relation.SourceID, relation.Exchange, relation.Asset, relation.Event = source.SourceID, source.Exchange, source.Assets[0], "TRANSFER_STOP"
	for _, endpoint := range plans {
		relation.PlannedStop = endpoint
	}
	for _, endpoint := range reports {
		relation.CompletionReport = endpoint
	}
	if noticeQuestionEndpointOrder(relation.PlannedStop) == "" || noticeQuestionEndpointOrder(relation.CompletionReport) <= noticeQuestionEndpointOrder(relation.PlannedStop) {
		return noticeQuestionEventRelation{}, false
	}
	return relation, true
}

func noticeQuestionEndpointOrder(endpoint noticeQuestionEventEndpoint) string {
	date, clock := "", ""
	for value := range endpoint.Values {
		if len(value) == 10 && value[4] == '-' && value[7] == '-' {
			if date != "" {
				return ""
			}
			date = value
		}
		if strings.HasPrefix(value, "CLOCK:") {
			if clock != "" {
				return ""
			}
			clock = strings.TrimPrefix(value, "CLOCK:")
		}
	}
	if date == "" || clock == "" {
		return ""
	}
	return date + "T" + clock
}

// The caller must provide separately parsed endpoint roles. This helper never
// licenses the union of their date sets or an actual-completion timestamp.
func noticeQuestionEventRelationMatches(relation noticeQuestionEventRelation, sourceID, exchange, asset string, planned, reported noticeQuestionEventEndpoint) bool {
	if relation.SourceID != sourceID || relation.Exchange != exchange || relation.Asset != asset || relation.Event != "TRANSFER_STOP" {
		return false
	}
	return noticeQuestionEventEndpointMatches(planned, relation.PlannedStop) && noticeQuestionEventEndpointMatches(reported, relation.CompletionReport)
}
func noticeQuestionEventEndpointMatches(claim, source noticeQuestionEventEndpoint) bool {
	if claim.Role != source.Role || len(claim.Values) == 0 {
		return false
	}
	if claim.Zone != "" && claim.Zone != source.Zone {
		return false
	}
	hasDate := false
	for value := range claim.Values {
		if !source.Values[value] {
			return false
		}
		if strings.HasPrefix(value, "MONTH_DAY:") {
			hasDate = true
		}
	}
	return hasDate
}
