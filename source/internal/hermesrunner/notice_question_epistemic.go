package hermesrunner

import (
	"fmt"
	"slices"
	"strings"
)

// An inquiry is not a negative event. Its operand asks for either an actual
// outcome or a policy/state; those require different evidence to resolve.
// Keep it separate from document absence (a quoted proposition is not stated).
type noticeEvidenceInquiry struct {
	Proposition  string
	Direction    string
	Processing   bool
	Actual       bool
	Possible     bool
	Ordered      bool
	Attribute    string // empty asks about outcome/policy; TIME asks for its stated schedule
	Alternatives []noticeEvidenceInquiry
	Procedure    string
}

var noticeEvidenceReportingVerbs = []string{"제공", "제시", "명시", "기재", "확정", "안내", "확인", "설명"}

// Parse the outer evidence predicate first, then bind its complement. Only a
// closed document-location phrase may sit between the complement and predicate;
// an arbitrary intervening service assertion cannot become an epistemic aside.
func noticeClaimEvidenceFrame(text string, sources []noticeQuestionClaimSource) (noticeClaimStatementFrame, bool) {
	if frame, ok := noticeClaimCoordinatedInquiryFrame(text, sources); ok {
		return frame, true
	}
	if frame, ok := noticeClaimInquiryTopicFrame(text); ok {
		return frame, true
	}
	if frame, ok := noticeClaimEvidenceFieldFrame(text, sources); ok {
		return frame, true
	}
	matrix := ""
	for _, predicate := range []string{"확인되지않습니다", "확인되지않았습니다", "확인할수없습니다", "판단할수없습니다", "알수없습니다", "파악할수없습니다", "없습니다"} {
		if strings.HasSuffix(text, predicate) {
			matrix = predicate
			break
		}
	}
	if matrix == "" {
		return noticeClaimStatementFrame{}, false
	}
	before := strings.TrimSuffix(text, matrix)
	for _, boundary := range noticeClaimQuotation.FindAllStringIndex(before, -1) {
		ending := before[boundary[0]:boundary[1]]
		if ending != "는지" && ending != "은지" {
			continue
		}
		tail := before[boundary[1]:]
		for _, particle := range []string{"는", "은", "가", "를"} {
			tail = strings.TrimPrefix(tail, particle)
		}
		if !noticeEvidenceDocumentLocation(tail, sources) && !noticeEvidenceInquiryDocumentTail(tail+matrix, sources) {
			continue
		}
		proposition := before[:boundary[0]]
		residual := ""
		if split := noticeClaimJoinedFiniteBoundary(proposition); split > 0 {
			residual, proposition = proposition[:split], proposition[split:]
		}
		if split := strings.LastIndexAny(proposition, ",;"); split >= 0 && noticeClaimJoinedFinitePredicate(proposition[:split]) {
			residual += proposition[:split]
			proposition = proposition[split+1:]
		}
		proposition = strings.Trim(proposition, ",; ")
		if proposition == "" {
			continue
		}
		inquiry := noticeEvidenceInquiry{Proposition: proposition, Direction: noticeConditionTopic(proposition), Processing: noticeConditionProcessingPredicate(proposition), Ordered: noticeConditionOrder.MatchString(proposition), Possible: noticeConditionPossible.MatchString(proposition)}
		if at := noticeConditionProcess.FindStringIndex(proposition); at != nil {
			inquiry.Actual = noticeEvidencePastPredicate(proposition[at[1]:])
		} else if at := noticeClaimAction.FindStringIndex(proposition); at != nil {
			inquiry.Actual = noticeEvidencePastPredicate(proposition[at[1]:])
		}
		return noticeClaimStatementFrame{Proposition: proposition, Matrix: noticeClaimEvidenceUnconfirmed, Target: noticeClaimTargetDocument, Negated: true, Residual: residual, Inquiry: &inquiry}, true
	}
	return noticeClaimStatementFrame{}, false
}

// Coordinated interrogatives share one reporting predicate. Keep each operand
// under that predicate, rather than treating the first comma as an assertion.
// Only closed interrogative links are accepted; finite statements remain in
// Residual or fail this parse and are handled by the ordinary claim guards.
func noticeClaimCoordinatedInquiryFrame(text string, sources []noticeQuestionClaimSource) (noticeClaimStatementFrame, bool) {
	boundaries := noticeClaimQuotation.FindAllStringIndex(text, -1)
	queries := [][2]int{}
	for _, b := range boundaries {
		if ending := text[b[0]:b[1]]; ending == "는지" || ending == "은지" {
			queries = append(queries, [2]int{b[0], b[1]})
		}
	}
	if len(queries) < 2 {
		return noticeClaimStatementFrame{}, false
	}
	last := queries[len(queries)-1]
	tail := text[last[1]:]
	for _, prefix := range []string{"는", "은", "등", "구체적인", "처리절차", "절차", "는", "은"} {
		tail = strings.TrimPrefix(tail, prefix)
	}
	if !noticeEvidenceDocumentReportingAbsence(tail, sources) {
		return noticeClaimStatementFrame{}, false
	}
	operand, residual := text[:queries[0][0]], ""
	if split := noticeClaimJoinedFiniteBoundary(operand); split > 0 {
		residual, operand = operand[:split], operand[split:]
	}
	if split := strings.LastIndexAny(operand, ",;"); split >= 0 {
		residual += operand[:split]
		operand = operand[split+1:]
	}
	parent := &noticeEvidenceInquiry{Proposition: text[:last[0]], Direction: noticeConditionTopic(operand)}
	for i, b := range queries {
		part := operand
		if i > 0 {
			part = text[queries[i-1][1]:b[0]]
			linked := false
			for _, link := range []string{",", "또는", "혹은"} {
				if strings.HasPrefix(part, link) {
					part = strings.TrimPrefix(part, link)
					linked = true
					break
				}
			}
			if !linked || noticeClaimJoinedFiniteBoundary(part) > 0 || strings.ContainsAny(part, ",;") {
				return noticeClaimStatementFrame{}, false
			}
		}
		query := noticeEvidenceInquiry{Proposition: part, Direction: noticeConditionTopic(part), Processing: noticeConditionProcessingPredicate(part), Ordered: noticeConditionOrder.MatchString(part), Possible: noticeConditionPossible.MatchString(part)}
		if i > 0 {
			// Carry the shared subject/asset into later elided operands without
			// reclassifying their own predicate as a processing assertion.
			query.Proposition = operand + " " + part
		}
		if query.Direction == "" {
			query.Direction = parent.Direction
		}
		if at := noticeConditionProcess.FindStringIndex(part); at != nil {
			query.Actual = noticeEvidencePastPredicate(part[at[1]:])
		}
		if !query.Processing {
			for _, predicate := range []string{"확인을거치", "확인하", "확인되"} {
				if strings.HasSuffix(part, predicate) {
					query.Procedure = "확인"
					break
				}
			}
			if query.Procedure == "" {
				return noticeClaimStatementFrame{}, false
			}
		}
		parent.Alternatives = append(parent.Alternatives, query)
	}
	return noticeClaimStatementFrame{Proposition: parent.Proposition, Matrix: noticeClaimEvidenceUnconfirmed, Target: noticeClaimTargetDocument, Negated: true, Residual: residual, Inquiry: parent}, true
}

func noticeEvidenceInquiryDocumentTail(text string, sources []noticeQuestionClaimSource) bool {
	bound := false
	for _, relation := range []string{"에관한", "에대한"} {
		if strings.HasPrefix(text, relation) {
			text, bound = strings.TrimPrefix(text, relation), true
			break
		}
	}
	if !bound {
		return false
	}
	for _, modifier := range []string{"별도", "구체적인", "명시적인", "추가"} {
		text = strings.TrimPrefix(text, modifier)
	}
	for _, noun := range []string{"문구", "내용", "안내", "설명", "진술"} {
		if !strings.HasPrefix(text, noun) {
			continue
		}
		tail := strings.TrimPrefix(text, noun)
		for _, particle := range []string{"은", "는", "이", "가"} {
			tail = strings.TrimPrefix(tail, particle)
		}
		return noticeEvidenceDocumentReportingAbsence(tail, sources)
	}
	return false
}

// Introducing the question along which two sources are compared does not
// assert an answer to that question. Require both a comparison-topic matrix
// and an interrogative complement; ordinary comparative assertions stay in
// the normal guards. Nested reporting questions retain the inner service
// inquiry, not an invented promise by either source.
func noticeClaimInquiryTopicFrame(text string) (noticeClaimStatementFrame, bool) {
	if !strings.HasSuffix(text, "에있습니다") {
		return noticeClaimStatementFrame{}, false
	}
	at := strings.Index(text, "차이는")
	if at < 0 {
		return noticeClaimStatementFrame{}, false
	}
	operand := strings.TrimSuffix(text[at+len("차이는"):], "에있습니다")
	if !strings.HasSuffix(operand, "는지") && !strings.HasSuffix(operand, "은지") {
		return noticeClaimStatementFrame{}, false
	}
	residual := ""
	if boundary := strings.LastIndexAny(text[:at], ",;"); boundary >= 0 {
		residual = text[:boundary]
	}
	if boundary := noticeClaimJoinedFiniteBoundary(text[:at]); boundary > 0 {
		if boundary > len(residual) {
			residual = text[:boundary]
		}
	}
	if residual == "" && (noticeConditionProcess.MatchString(text[:at]) || noticeClaimAction.MatchString(text[:at])) {
		// An unexplained prefix with a service predicate is not merely the
		// comparison's subject noun phrase. Keep it asserted, without guessing
		// which portion the author intended as a comparison label.
		return noticeClaimStatementFrame{}, false
	}
	// The first question complement binds the service predicate. Everything
	// before it remains inside that inquiry unless it contains an independent
	// finite assertion, which must be validated separately.
	for _, boundary := range noticeClaimQuotation.FindAllStringIndex(operand, -1) {
		if !strings.HasSuffix(operand[boundary[0]:boundary[1]], "지") {
			continue
		}
		operand = operand[:boundary[0]]
		break
	}
	if boundary := noticeClaimJoinedFiniteBoundary(operand); boundary > 0 {
		residual += operand[:boundary]
		operand = operand[boundary:]
	}
	query := &noticeEvidenceInquiry{Proposition: operand, Direction: noticeConditionTopic(operand), Processing: noticeConditionProcessingPredicate(operand)}
	return noticeClaimStatementFrame{Proposition: operand, Matrix: noticeClaimInquiryTopic, Residual: residual, Inquiry: query}, true
}

// A missing event attribute is not a missing event. Temporal field nouns bind
// a TIME inquiry to their immediately preceding event, while the reporting
// predicate remains outside that operand. Earlier comma-separated assertions
// are preserved for the ordinary factual guards.
func noticeClaimEvidenceFieldFrame(text string, sources []noticeQuestionClaimSource) (noticeClaimStatementFrame, bool) {
	for _, field := range []string{"시점", "일정", "시각", "일시", "날짜", "방법", "방식", "방침", "문구", "여부", "결과"} {
		at := strings.LastIndex(text, field)
		if at < 0 {
			continue
		}
		tail := text[at+len(field):]
		for _, particle := range []string{"은", "는", "이", "가"} {
			tail = strings.TrimPrefix(tail, particle)
		}
		if !noticeEvidenceDocumentReportingAbsence(tail, sources) {
			continue
		}
		operand, residual := text[:at], ""
		if split := strings.LastIndexAny(operand, ",;"); split >= 0 {
			residual, operand = operand[:split], operand[split+1:]
		}
		if operand == "" {
			continue
		}
		inquiry := &noticeEvidenceInquiry{Proposition: operand, Attribute: "TIME"}
		if slices.Contains([]string{"방법", "방식", "방침", "문구", "여부", "결과"}, field) {
			// A quoted proposition or assurance has its own document truth
			// conditions, including guarantee strength. Do not replace that
			// existing frame with a generic missing-policy inquiry.
			if noticeClaimQuotation.MatchString(operand) || strings.Contains(operand, "보장") {
				continue
			}
			if !noticeConditionProcess.MatchString(operand) {
				continue
			}
			inquiry.Processing, inquiry.Direction = true, noticeConditionTopic(operand)
			inquiry.Attribute = "POLICY"
			if field == "여부" || field == "결과" {
				inquiry.Attribute, inquiry.Actual = "OUTCOME", true
			}
		}
		return noticeClaimStatementFrame{Proposition: operand, Matrix: noticeClaimEvidenceUnconfirmed, Target: noticeClaimTargetDocument, Negated: true, Residual: residual, Inquiry: inquiry}, true
	}
	return noticeClaimStatementFrame{}, false
}

func noticeEvidenceDocumentReportingAbsence(text string, sources []noticeQuestionClaimSource) bool {
	if noticeEvidenceReportingAbsence(text) {
		return true
	}
	for _, ending := range []string{"없습니다", "없다", "없음", "적혀있지않습니다"} {
		if strings.HasSuffix(text, ending) && noticeEvidenceDocumentLocation(strings.TrimSuffix(text, ending), sources) {
			return true
		}
	}
	for _, verb := range noticeEvidenceReportingVerbs {
		if at := strings.Index(text, verb); at > 0 && noticeEvidenceDocumentLocation(text[:at], sources) && noticeEvidenceReportingAbsence(text[at:]) {
			return true
		}
	}
	return false
}

func noticeEvidenceReportingAbsence(text string) bool {
	if text == "적혀있지않습니다" || text == "적혀있지않았습니다" {
		return true
	}
	if text == "없습니다" || text == "없다" || text == "없음" {
		return true
	}
	stem := text
	for _, ending := range []string{"않았습니다", "않습니다", "않았다", "않다", "않았음", "않음"} {
		stem = strings.TrimSuffix(stem, ending)
	}
	if stem == text {
		return false
	}
	verbs := noticeEvidenceReportingVerbs
	for _, verb := range verbs {
		for _, voice := range []string{"되지", "하지", "되어있지", "돼있지"} {
			if !strings.HasSuffix(stem, verb+voice) {
				continue
			}
			prefix := stem[:len(stem)-len(verb)-len(voice)]
			if prefix == "" {
				return true
			}
			for _, joined := range verbs {
				if prefix == joined+"되거나" || prefix == joined+"하거나" {
					return true
				}
			}
		}
	}
	return false
}

func noticeEvidenceDocumentLocation(text string, sources []noticeQuestionClaimSource) bool {
	// A missing location does not prove that the limitation is about the supplied
	// official evidence. Do not grant an unscoped statement a blanket exemption.
	for _, verb := range noticeEvidenceReportingVerbs {
		text = strings.TrimPrefix(text, verb+"된")
		text = strings.TrimPrefix(text, verb+"한")
	}
	for _, prefix := range []string{"이", "해당", "본"} {
		text = strings.TrimPrefix(text, prefix)
	}
	for _, source := range sources {
		for _, alias := range noticeReasonExchangeAliases(source.Exchange) {
			if alias != "" && strings.HasPrefix(text, noticeReasonCompact(alias)) {
				text = strings.TrimPrefix(text[len(noticeReasonCompact(alias)):], "의")
			}
		}
	}
	text = strings.TrimPrefix(text, "공식")
	for _, noun := range []string{"공지", "원문", "본문", "자료", "근거"} {
		if !strings.HasPrefix(text, noun) {
			continue
		}
		rest := strings.TrimPrefix(text, noun)
		for _, place := range []string{"에서", "으로", "만으로", "로", "에"} {
			if rest == place || rest == place+"는" {
				return true
			}
		}
	}
	return false
}

func noticeEvidencePastPredicate(tail string) bool {
	for _, past := range []string{"됐", "되었", "했", "하였", "완료", "않았"} {
		if strings.Contains(tail, past) {
			return true
		}
	}
	return false
}

type noticeProcessingFact struct {
	Text      string
	Direction string
	Actual    bool
	Possible  bool
	Negative  bool
	Ordered   bool
}

func noticeSourceProcessingFacts(source noticeQuestionClaimSource) []noticeProcessingFact {
	facts := []noticeProcessingFact{}
	for _, passage := range source.Passages {
		for _, clause := range noticeReasonClauses(passage, nil) {
			compact := noticeConditionPredicateText(clause.text)
			if !noticeConditionProcessingPredicate(compact) || noticeConditionIsUnknown(clause.text, source) {
				continue
			}
			at := noticeConditionProcess.FindStringIndex(compact)
			if at == nil {
				continue
			}
			facts = append(facts, noticeProcessingFact{Text: clause.text, Direction: noticeConditionTopic(compact), Actual: noticeEvidencePastPredicate(compact[at[1]:]), Possible: noticeConditionPossible.MatchString(compact), Negative: noticeConditionNegative.MatchString(compact), Ordered: noticeConditionOrder.MatchString(compact)})
		}
	}
	return facts
}

// Every named scope must have cited evidence. An absent venue or asset is not
// licensed by another source's lack of a processing promise.
func noticeEvidenceSourceGroups(text string, sources []noticeQuestionClaimSource) ([][]noticeQuestionClaimSource, error) {
	scopeSources := append([]noticeQuestionClaimSource{}, sources...)
	for _, exchange := range []string{"upbit", "bithumb", "coinone", "korbit", "gopax"} {
		scopeSources = append(scopeSources, noticeQuestionClaimSource{Exchange: exchange})
	}
	exchanges := noticeReasonMentionedExchanges(text, scopeSources)
	assets := []string{}
	for _, word := range noticeClaimTicker.FindAllString(text, -1) {
		if slices.Contains([]string{"KST", "UTC", "GMT", "API", "JSON"}, word) {
			continue
		}
		assets = append(assets, word)
	}
	if len(exchanges) == 0 {
		exchanges = []string{""}
	}
	if len(assets) == 0 {
		assets = []string{""}
	}
	groups := [][]noticeQuestionClaimSource{}
	for _, exchange := range exchanges {
		for _, asset := range assets {
			group := []noticeQuestionClaimSource{}
			for _, source := range sources {
				if source.SourceID == "" || strings.HasPrefix(source.SourceID, "server-volume:") || exchange != "" && !strings.EqualFold(source.Exchange, exchange) {
					continue
				}
				if asset != "" && !slices.ContainsFunc(source.Assets, func(candidate string) bool { return strings.EqualFold(candidate, asset) }) {
					continue
				}
				group = append(group, source)
			}
			if len(group) == 0 {
				return nil, fmt.Errorf("notice evidence limitation has no cited source for %s/%s", exchange, asset)
			}
			groups = append(groups, group)
		}
	}
	return groups, nil
}

func noticeEvidenceInquiryResolved(inquiry noticeEvidenceInquiry, sources []noticeQuestionClaimSource) bool {
	for _, alternative := range inquiry.Alternatives {
		if noticeEvidenceInquiryResolved(alternative, sources) {
			return true
		}
	}
	if inquiry.Procedure != "" {
		for _, source := range sources {
			for _, passage := range source.Passages {
				for _, clause := range noticeReasonClauses(passage, nil) {
					part := noticeReasonCompact(clause.text)
					if !noticeEvidenceOperandScopeMatches(inquiry.Proposition, part, source) || noticeConditionIsUnknown(part, source) {
						continue
					}
					if inquiry.Direction != "" && noticeConditionTopic(part) != inquiry.Direction && noticeConditionTopic(part) != "TRANSFER" {
						continue
					}
					for _, predicate := range []string{"확인을거칩", "확인을거친", "확인합니다", "확인후", "확인한후"} {
						if strings.Contains(part, predicate) {
							return true
						}
					}
				}
			}
		}
		return false
	}
	if inquiry.Processing {
		for _, source := range sources {
			for _, fact := range noticeSourceProcessingFacts(source) {
				if inquiry.Attribute == "POLICY" && fact.Actual && !fact.Ordered {
					continue
				}
				if !noticeEvidenceOperandScopeMatches(inquiry.Proposition, fact.Text, source) || inquiry.Ordered && !fact.Ordered {
					continue
				}
				if inquiry.Direction != "" && fact.Direction != inquiry.Direction && fact.Direction != "TRANSFER" {
					continue
				}
				// A prospective policy and a possibility do not establish what happened.
				// An explicit positive OR negative actual outcome resolves a whether/how
				// inquiry; its polarity must not turn an inquiry into a negative assertion.
				if fact.Possible && !inquiry.Possible || inquiry.Actual && !fact.Actual {
					continue
				}
				return true
			}
		}
		return false
	}
	// The same distinction applies to completion of a service event. Use the
	// existing typed event facts instead of borrowing dates from another topic.
	claims := noticeQuestionTextClaims(inquiry.Proposition, "", nil)
	for _, claim := range claims {
		if claim.topic == "" || claim.action == "" {
			continue
		}
		for _, source := range sources {
			for _, passage := range source.Passages {
				for _, fact := range noticeQuestionTextClaims(passage, source.Title, nil, source) {
					if inquiry.Attribute == "TIME" && len(fact.values) == 0 {
						continue
					}
					if noticeEvidenceOperandScopeMatches(inquiry.Proposition, fact.text, source) && fact.topic == claim.topic && fact.action == claim.action && (inquiry.Attribute == "TIME" || fact.mode != "POSSIBLE" && fact.mode != "PLANNED") && (!inquiry.Actual || fact.mode == "COMPLETED") {
						return true
					}
				}
			}
		}
	}
	return false
}

func noticeEvidenceOperandScopeMatches(operand, fact string, source noticeQuestionClaimSource) bool {
	for _, asset := range source.Assets {
		if !noticeQuestionHasAnchor(operand, asset) {
			continue
		}
		if len(source.Assets) > 1 && !noticeQuestionHasAnchor(fact, asset) {
			return false
		}
		for _, other := range source.Assets {
			if other != asset && noticeQuestionHasAnchor(fact, other) && !noticeQuestionHasAnchor(fact, asset) {
				return false
			}
		}
	}
	return true
}

func validateNoticeQuestionEvidenceLimits(text string, sources []noticeQuestionClaimSource) error {
	// Split sentences, not the complement's internal topic/condition clauses.
	// The typed frame preserves independent finite claims in Residual.
	clauses := []noticeReasonClause{}
	for _, line := range strings.Split(text, "\n") {
		scope := noticeReasonLabelScope(line, sources)
		for _, sentence := range noticeReasonSentence.Split(line, -1) {
			clauses = append(clauses, noticeReasonClause{text: sentence, exchanges: scope})
		}
	}
	for _, clause := range clauses {
		sentence := clause.text
		frame := noticeClaimStatement(sentence, sources...)
		if frame.Matrix != noticeClaimEvidenceUnconfirmed && frame.Matrix != noticeClaimInquiryTopic {
			continue
		}
		if frame.Residual != "" {
			if err := validateNoticeQuestionClaimText(frame.Residual, sources); err != nil {
				return err
			}
		}
		if frame.Inquiry == nil {
			return fmt.Errorf("notice evidence limitation has no bound inquiry")
		}
		if len(frame.Inquiry.Alternatives) == 0 && !frame.Inquiry.Processing && !slices.ContainsFunc(noticeQuestionTextClaims(frame.Inquiry.Proposition, "", nil), func(claim noticeQuestionClaim) bool { return claim.topic != "" && claim.action != "" }) {
			return fmt.Errorf("notice evidence limitation has an unresolved inquiry operand: %s", frame.Proposition)
		}
		scopedSources := sources
		if len(clause.exchanges) > 0 {
			scopedSources = nil
			for _, source := range sources {
				if slices.Contains(clause.exchanges, strings.ToLower(source.Exchange)) {
					scopedSources = append(scopedSources, source)
				}
			}
		}
		groups, err := noticeEvidenceSourceGroups(sentence, scopedSources)
		if err != nil {
			return err
		}
		if frame.Matrix == noticeClaimInquiryTopic {
			continue
		}
		for _, group := range groups {
			if noticeEvidenceInquiryResolved(*frame.Inquiry, group) {
				return fmt.Errorf("notice evidence limitation contradicts an explicit source outcome or policy: %s", strings.TrimSpace(sentence))
			}
		}
	}
	return nil
}
