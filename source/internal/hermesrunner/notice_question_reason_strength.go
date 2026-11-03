package hermesrunner

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// This is a bounded evidence-strength check, not general entailment. It learns
// the review subjects from official passages and never treats a failed overall
// listing review as proof that every reviewed subject was individually faulty.
var (
	noticeReasonSentence      = regexp.MustCompile(`[.!?](?:\s+|$)|[\r\n;；]+`)
	noticeReasonContrast      = regexp.MustCompile(`(?:하지만|그러나|반면|한편|지만|으나|했으며|하였으며|으며|이며|이고|했고|하였고|하고)`)
	noticeReasonReview        = regexp.MustCompile(`종합(?:적으로|적인|적)?(?:검토|심사|평가|고려|판단|(?:거래지원)?유지기준)|(?:검토|심사|평가|고려)(?:한)?(?:항목|사항|대상)`)
	noticeReasonCriterionCase = regexp.MustCompile(`(?:는|은|한)행위|(?:는|한|있는)경우`)
	noticeReasonListNext      = regexp.MustCompile(`^(?:등|과|와|및|[·,])`)
	noticeReasonWords         = regexp.MustCompile(`[가-힣A-Za-z][가-힣A-Za-z0-9]{1,}`)
	noticeReasonFault         = regexp.MustCompile(`문제|위반|미흡|미충족|미이행|미준수|불투명|불합리|부족|지연|차질|실패|불이행|허위|부적절|미공시|미공개|(?:공시|공개|이행|준수|제공|충분|적절|투명|합리)[가-힣]{0,8}(?:않|못)`)
	noticeReasonDenial        = regexp.MustCompile(`(?:단정|확정|판단|해석|일반화)(?:할|하기|해)?수(?:는|가)?없|(?:단정|확정|판단|해석)(?:하지(?:는)?)?(?:못|않)|볼수(?:는|가)?없|보기어렵|볼수없|(?:뜻|의미|말)(?:은|는|이|가)?(?:아니|아닙|아님|아닌)|(?:문제|위반|부족|미흡)(?:이라는|라고|이라고|로|이|가)?(?:뜻|의미|말)?(?:은|는|이|가)?(?:아니|아닙|아님|아닌)|(?:문제|위반|미흡|부족)[가-힣]{0,16}(?:확인되지않|확인하지않|확인하지못|확인할수없|명시되지않|명시하지않|특정하지않|특정되지않)|(?:부족|미흡|위반)하지않|문제가없|^(?:부족|미흡|위반)(?:했|하였|한|됐|되었)?(?:는지|은지|인지)[가-힣]{0,24}(?:밝히지않|명시하지않|설명하지않)`)
	noticeReasonCollective    = regexp.MustCompile(`양사|양쪽|두거래소|두곳|둘다|공통|공히|동일한사유|같은사유`)
	noticeReasonAllItems      = regexp.MustCompile(`(?:모든|모두|개별|각각|전체|전부)[가-힣]{0,10}(?:항목|위반|문제|미흡)`)
	noticeReasonUnresolved    = regexp.MustCompile(`해소되지(?:도)?(?:않|못)|미해소`)
)

func noticeClaimJoinedFinitePredicate(text string) bool {
	return noticeClaimJoinedFiniteBoundary(text) > 0
}

func noticeReasonCompact(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("*_`'\"“”‘’", r) {
			return -1
		}
		return unicode.ToLower(r)
	}, text)
}

type noticeClaimMatrix uint8

const (
	noticeClaimAsserted noticeClaimMatrix = iota
	noticeClaimDocumentAbsent
	noticeClaimOverstatement
	noticeClaimEvidenceUnconfirmed
	noticeClaimInquiryTopic
)

type noticeClaimStatementFrame struct {
	Proposition       string
	Matrix            noticeClaimMatrix
	Target            noticeClaimTarget
	Negated           bool
	Residual          string
	EmbeddedGuarantee bool
	Inquiry           *noticeEvidenceInquiry
}

type noticeClaimTarget uint8

const (
	noticeClaimTargetUnknown noticeClaimTarget = iota
	noticeClaimTargetDocument
	noticeClaimTargetGuarantee
)

var (
	noticeClaimQuotation                 = regexp.MustCompile(`(?:다고|라고|다는|라는|는지|은지)`)
	noticeClaimReportNegative            = regexp.MustCompile(`^(?:(?:명시|기재|안내|설명)(?:하지|되지)|밝히지)(?:는)?않(?:았)?(?:습니다|다|음)$`)
	noticeClaimReportNominal             = regexp.MustCompile(`^((?:처리)?(?:방식|방법|방침|내용|설명|안내|약속|보장|문구|표현|진술))(?:은|는|이|가)`)
	noticeClaimReportAbsent              = regexp.MustCompile(`^없(?:습니다|다|음)$`)
	noticeClaimDocumentPlace             = regexp.MustCompile(`^(?:이|해당|본)?(?:공지|원문|본문)(?:에서|에)`)
	noticeClaimDocumentExistenceNegative = regexp.MustCompile(`^(?:실려|적혀|기재되어)있지(?:는)?않(?:았)?(?:습니다|다|음)$`)
	noticeClaimNegativeCopula            = regexp.MustCompile(`^(?:(?:것|사실)(?:은|는|이|가)?)?(?:아닙니다|아니다|아님|아니)$`)
	noticeClaimReportAdverb              = regexp.MustCompile(`^(?:더)?[가-힣]{1,8}(?:적으로|하게)`)
	noticeClaimGuaranteeNo               = regexp.MustCompile(`보장(?:되지|하지)(?:는)?않(?:았)?(?:습니다|다|음)$`)
	noticeClaimGuaranteeNominal          = regexp.MustCompile(`보장(?:(?:되|하)는|한)`)
	noticeClaimGuaranteeObject           = regexp.MustCompile(`(?:무료(?:로)?|정상|실제)?(?:입출금|입금|출금|처리|반영|성공)(?:가능(?:여부|성)|완료|성공)?(?:이|가|은|는|을|를)?$`)
	noticeClaimEvaluation                = regexp.MustCompile(`^(?:표현|말|해석)하는것(?:은|는|이)(?:원문|본문|공지|근거)보다강한해석(?:입니다|이다|임)$`)
	noticeClaimInterpretationProhibition = regexp.MustCompile(`(?:이라고|라고|다고|으로|로)해석(?:해서|하여서|하면|하기)?(?:는)?안(?:됩니다|된다|됨)`)
	noticeClaimGuaranteeDocumentPrefix   = regexp.MustCompile(`^(?:(?:제공된|이|해당|본)?(?:공지|원문|본문|문서))(?:에는|에|에서는|에서)(.+?)보장하는(?:문구|표현|설명|내용)(?:은|는|이|가)?(?:없음|없습니다|없다)$`)
	noticeClaimGuaranteeDocumentSuffix   = regexp.MustCompile(`^(.+?)보장하는(?:문구|표현|설명|내용)(?:은|는|이|가)?(?:(?:제공된|이|해당|본)?(?:공지|원문|본문|문서))(?:에는|에|에서는|에서)(?:없음|없습니다|없다)$`)
)

func noticeClaimTrimGuaranteeProposition(text string) (string, string) {
	text = strings.Trim(text, ",; ")
	residual := ""
	if boundary := strings.LastIndexAny(text, ",;"); boundary >= 0 {
		residual = strings.Trim(text[:boundary], ",; ")
		text = strings.Trim(text[boundary+1:], ",; ")
	}
	for _, suffix := range []string{"이라고", "라고", "다고", "을", "를"} {
		if strings.HasSuffix(text, suffix) {
			text = strings.TrimSuffix(text, suffix)
			break
		}
	}
	return text, residual
}

func noticeClaimGuaranteeDocumentFrame(compact string) (noticeClaimStatementFrame, bool) {
	for _, pattern := range []*regexp.Regexp{noticeClaimGuaranteeDocumentPrefix, noticeClaimGuaranteeDocumentSuffix} {
		match := pattern.FindStringSubmatch(compact)
		if len(match) == 0 {
			continue
		}
		proposition, residual := noticeClaimTrimGuaranteeProposition(match[1])
		if proposition == "" {
			return noticeClaimStatementFrame{}, false
		}
		return noticeClaimStatementFrame{
			Proposition: proposition, Matrix: noticeClaimDocumentAbsent,
			Target: noticeClaimTargetDocument, Negated: true,
			Residual: residual, EmbeddedGuarantee: true,
		}, true
	}
	return noticeClaimStatementFrame{}, false
}

func noticeClaimInterpretationFrame(compact string) (noticeClaimStatementFrame, bool) {
	location := noticeClaimInterpretationProhibition.FindStringIndex(compact)
	if location == nil {
		return noticeClaimStatementFrame{}, false
	}
	// The prohibition must be the matrix predicate. Text following it is an
	// independent assertion and stays in Residual instead of sharing negation.
	after := strings.Trim(compact[location[1]:], ",; ")
	if after != "" && compact[location[1]] != ',' && compact[location[1]] != ';' {
		return noticeClaimStatementFrame{}, false
	}
	before := strings.Trim(compact[:location[0]], ",; ")
	residual := after
	if boundary := strings.LastIndexAny(before, ",;"); boundary >= 0 && noticeClaimJoinedFinitePredicate(before[:boundary]) {
		residual = strings.Trim(strings.Join([]string{before[:boundary], residual}, ". "), ". ")
		before = strings.Trim(before[boundary+1:], ",; ")
	} else if boundary := noticeClaimJoinedFiniteBoundary(before); boundary > 0 {
		// A finite clause joined directly with -고/-해서/etc. remains an
		// asserted reason or service event. Only the following complement is
		// governed by the interpretation prohibition.
		residual = strings.Trim(strings.Join([]string{before[:boundary], residual}, ". "), ". ")
		before = strings.Trim(before[boundary:], ",; ")
	}
	if before == "" {
		return noticeClaimStatementFrame{}, false
	}
	return noticeClaimStatementFrame{
		Proposition: before, Matrix: noticeClaimOverstatement,
		Negated: true, Residual: residual,
	}, true
}

func noticeClaimJoinedFiniteBoundary(text string) int {
	boundary := 0
	// These are the same bounded finite connectors already treated as
	// independent by noticeClaimJoinedFinitePredicate. Exclude the prenominal
	// "되어있는", whose predicate belongs to its following noun.
	finiteText := strings.ReplaceAll(text, "되어있는", strings.Repeat(" ", len("되어있는")))
	for _, ending := range []string{"되어", "하여서", "해서", "있어", "으므로"} {
		if index := strings.LastIndex(finiteText, ending); index >= 0 && index+len(ending) > boundary {
			boundary = index + len(ending)
		}
	}
	for index, r := range text {
		past := r >= '가' && r <= '힣' && (r-'가')%28 == 20
		if !past && !strings.ContainsRune("되하이있없", r) {
			continue
		}
		next := index + len(string(r))
		for _, ending := range []string{"고", "는데", "지만", "으나", "으므로", "으며"} {
			if ending == "고" && !past {
				continue
			}
			if next < len(text) && strings.HasPrefix(text[next:], ending) && next+len(ending) > boundary {
				boundary = next + len(ending)
			}
		}
	}
	return boundary
}

// A quoted proposition is not asserted when the complete matrix predicate
// denies its presence in a document or judges that wording to overstate it.
// This accepts one independent clause, not an entire paragraph. Anchoring the
// matrix prevents a following independent fact from sharing its negation.
func noticeClaimStatement(text string, sources ...noticeQuestionClaimSource) noticeClaimStatementFrame {
	compact := strings.TrimRight(noticeReasonCompact(text), ".!?。 ")
	if frame, ok := noticeClaimEvidenceFrame(compact, sources); ok {
		return frame
	}
	if frame, ok := noticeClaimInterpretationFrame(compact); ok {
		return frame
	}
	if frame, ok := noticeClaimGuaranteeDocumentFrame(compact); ok {
		return frame
	}
	for _, quote := range noticeClaimQuotation.FindAllStringIndex(compact, -1) {
		if noticeClaimJoinedFinitePredicate(compact[:quote[0]]) {
			continue
		}
		matrix := compact[quote[1]:]
		if strings.HasSuffix(compact[quote[0]:quote[1]], "지") {
			matrix = strings.TrimPrefix(strings.TrimPrefix(matrix, "는"), "은")
		}
		// The quotation may name a policy/content noun which is the object of
		// the reporting predicate. That nominal bridge is not a service action.
		bridgeName := ""
		if bridge := noticeClaimReportNominal.FindStringSubmatchIndex(matrix); bridge != nil {
			bridgeName = matrix[bridge[2]:bridge[3]]
			matrix = matrix[bridge[1]:]
		}
		matrix, documentLocated := noticeClaimDocumentLocation(matrix, sources)
		if adverb := noticeClaimReportAdverb.FindStringIndex(matrix); adverb != nil {
			matrix = matrix[adverb[1]:]
		}
		if noticeClaimReportNegative.MatchString(matrix) || (noticeEvidenceReportingAbsence(matrix) && !noticeClaimReportAbsent.MatchString(matrix)) || (documentLocated && noticeClaimDocumentExistenceNegative.MatchString(matrix)) || (bridgeName != "" && (noticeClaimNegativeCopula.MatchString(matrix) || noticeClaimReportAbsent.MatchString(matrix))) {
			return noticeClaimStatementFrame{Proposition: compact[:quote[0]], Matrix: noticeClaimDocumentAbsent, Target: noticeClaimTargetDocument, Negated: true, EmbeddedGuarantee: bridgeName == "보장"}
		}
		if noticeClaimEvaluation.MatchString(matrix) {
			return noticeClaimStatementFrame{Proposition: compact[:quote[0]], Matrix: noticeClaimOverstatement}
		}
	}
	guarantee := noticeClaimGuaranteeNo.FindStringIndex(compact)
	if guarantee == nil {
		for _, nominal := range noticeClaimGuaranteeNominal.FindAllStringIndex(compact, -1) {
			if noticeClaimNegativeCopula.MatchString(compact[nominal[1]:]) {
				guarantee = nominal
				break
			}
		}
	}
	if guarantee != nil {
		if object := noticeClaimGuaranteeObject.FindStringIndex(compact[:guarantee[0]]); object != nil {
			// The assurance target is only this terminal noun phrase. Earlier
			// factual predicates retain their own polarity and must be checked.
			return noticeClaimStatementFrame{
				Proposition: compact[object[0]:], Matrix: noticeClaimAsserted,
				Target: noticeClaimTargetGuarantee, Negated: true,
				Residual: strings.TrimRight(compact[:object[0]], ",; "),
			}
		}
	}
	return noticeClaimStatementFrame{Proposition: compact, Matrix: noticeClaimAsserted}
}

// A document's optional owner must come from the current source scope. Do not
// consume arbitrary text before "공지에" as if it were an owner noun.
func noticeClaimDocumentLocation(matrix string, sources []noticeQuestionClaimSource) (string, bool) {
	if place := noticeClaimDocumentPlace.FindStringIndex(matrix); place != nil {
		return matrix[place[1]:], true
	}
	for _, source := range sources {
		for _, alias := range noticeReasonExchangeAliases(source.Exchange) {
			alias = noticeReasonCompact(alias)
			if alias == "" || !strings.HasPrefix(matrix, alias) {
				continue
			}
			owned := strings.TrimPrefix(matrix[len(alias):], "의")
			if place := noticeClaimDocumentPlace.FindStringIndex(owned); place != nil {
				return owned[place[1]:], true
			}
		}
	}
	return matrix, false
}

func noticeReasonTerms(sources []noticeQuestionClaimSource) []string {
	terms := []string{}
	identities := map[string]bool{}
	for _, source := range sources {
		for _, asset := range source.Assets {
			identities[strings.ToLower(asset)] = true
		}
		for _, exchange := range noticeReasonExchangeAliases(source.Exchange) {
			identities[strings.ToLower(exchange)] = true
		}
	}
	for _, source := range sources {
		if strings.HasPrefix(source.SourceID, "server-volume:") {
			continue
		}
		for _, passage := range source.Passages {
			for _, sentence := range noticeReasonSentence.Split(passage, -1) {
				compact := noticeReasonCompact(sentence)
				review := noticeReasonReview.FindStringIndex(compact)
				if review == nil {
					continue
				}
				// Tokenize only the criteria before the overall assessment. Appending
				// words such as "maintenance standard not met" would erase this boundary.
				before := compact[:review[0]]
				for _, word := range noticeReasonWords.FindAllString(sentence, -1) {
					term := strings.ToLower(word)
					for _, suffix := range []string{"에서는", "으로", "에서", "에게", "에는", "에대한", "의", "에", "가", "을", "를", "은", "는", "과", "와"} {
						if strings.HasSuffix(term, suffix) && len([]rune(strings.TrimSuffix(term, suffix))) >= 2 {
							term = strings.TrimSuffix(term, suffix)
							break
						}
					}
					// An action criterion can appear as a conjugated negative
					// ("공시하지 않는 행위"). Keep its noun stem so a later
					// named "공시" claim refers to the same source-derived item.
					if strings.HasSuffix(term, "하지") && len([]rune(strings.TrimSuffix(term, "하지"))) >= 2 {
						term = strings.TrimSuffix(term, "하지")
					}
					if identities[term] {
						continue
					}
					switch term {
					case "재단", "프로젝트", "발행주체", "운영주체", "중요사항", "대한", "적시", "적절", "변경", "절차", "부분", "사항", "상황", "관련된", "관련", "거래지원", "지원", "유지", "기준", "결과", "등을", "각각", "사업":
						continue
					}
					if strings.HasPrefix(term, "종합") || strings.HasPrefix(term, "검토") || !strings.Contains(before, term) {
						continue
					}
					// Korean abstract-noun suffixes preserve the same criterion in prose
					// ("투명성" / "투명하지 않다"), without inventing a subject dictionary.
					if strings.HasSuffix(term, "성") && len([]rune(term)) >= 3 {
						term = strings.TrimSuffix(term, "성")
					}
					if len([]rune(term)) >= 2 {
						terms = append(terms, term)
					}
				}
			}
		}
	}
	slices.Sort(terms)
	return slices.Compact(terms)
}

func noticeReasonAssertsFault(text, term string) bool {
	statement := noticeClaimStatement(text)
	if statement.Residual != "" && noticeReasonAssertsFault(statement.Residual, term) {
		return true
	}
	if statement.Matrix != noticeClaimAsserted {
		return false
	}
	compact := statement.Proposition
	faults := noticeReasonFault.FindAllStringIndex(compact, -1)
	review := noticeReasonReview.FindStringIndex(compact)
	for _, fault := range faults {
		// Negation applies locally to this clause. Contrast clauses are split by
		// the caller, so a later disclaimer cannot pardon an earlier assertion.
		tail := compact[fault[0]:]
		if noticeReasonDenial.MatchString(tail) {
			continue
		}
		// Normative cases/actions inside a composite review list do not say
		// this case occurred. Its following overall verdict cannot supply an
		// individual finding for every item in that list.
		if review != nil && fault[0] < review[0] && noticeReasonCriterionCase.MatchString(compact[fault[1]:review[0]]) {
			continue
		}
		// "문제" also means a subject for consideration. When coordinated as
		// an item in the review object, it does not assert an individual fault.
		// A finding predicate ("문제를 확인") or explicit 위반/미흡 still does.
		if review != nil && fault[1] <= review[0] && compact[fault[0]:fault[1]] == "문제" && noticeReasonListNext.MatchString(compact[fault[1]:review[0]]) {
			continue
		}
		if term == "" {
			return true
		}
		for offset := 0; offset < len(compact); {
			index := strings.Index(compact[offset:], term)
			if index < 0 {
				break
			}
			index += offset
			offset = index + len(term)
			// In "공시 미흡과 투명성 검토", the defect before the next
			// criterion does not propagate through a list conjunction.
			// A prenominal qualifier such as "미흡한 공시" still binds.
			if fault[1] <= index {
				bridge := compact[fault[1]:index]
				if !(strings.HasPrefix(bridge, "한") || strings.HasPrefix(bridge, "했던") || strings.HasPrefix(bridge, "된") || strings.HasPrefix(bridge, "인")) || strings.ContainsAny(bridge, ",·") || strings.Contains(bridge, "및") || strings.Contains(bridge, "과") || strings.Contains(bridge, "등") {
					continue
				}
			}
			// Overall negative assessment following a criteria list does not assert
			// any individual defect in a criterion mentioned only before the review.
			if review != nil && index < review[0] && fault[0] >= review[0] {
				continue
			}
			left, right := min(index, fault[0]), max(index+len(term), fault[1])
			if len([]rune(compact[left:right])) <= 45 {
				return true
			}
		}
	}
	return false
}

func noticeReasonExchangeAliases(exchange string) []string {
	aliases := []string{strings.ToLower(exchange)}
	for _, row := range []struct{ id, name string }{{"upbit", "업비트"}, {"bithumb", "빗썸"}, {"coinone", "코인원"}, {"korbit", "코빗"}, {"gopax", "고팍스"}} {
		if strings.EqualFold(exchange, row.id) {
			aliases = append(aliases, row.name)
		}
	}
	return aliases
}

func noticeReasonMentionedExchanges(text string, sources []noticeQuestionClaimSource) []string {
	result := []string{}
	for _, source := range sources {
		if source.Exchange == "" || strings.HasPrefix(source.SourceID, "server-volume:") {
			continue
		}
		for _, alias := range noticeReasonExchangeAliases(source.Exchange) {
			if alias != "" && noticeQuestionHasAnchor(text, alias) {
				result = append(result, strings.ToLower(source.Exchange))
				break
			}
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

type noticeReasonClause struct {
	text      string
	exchanges []string
}

// Two independently introduced exchange subjects own separate predicates.
// A joint subject ("A와 B는") contains only one such introduction and stays
// intact, so the defect still needs support from both exchanges.
func noticeReasonSubjectClauses(text string, sources []noticeQuestionClaimSource) []string {
	aliases := []string{}
	for _, source := range sources {
		for _, alias := range noticeReasonExchangeAliases(source.Exchange) {
			if alias != "" {
				aliases = append(aliases, regexp.QuoteMeta(alias))
			}
		}
	}
	if len(aliases) == 0 {
		return []string{text}
	}
	boundary := regexp.MustCompile("(?i)(?:" + strings.Join(aliases, "|") + ")(?:에서는|에선|은|는)")
	locations := boundary.FindAllStringIndex(text, -1)
	if len(locations) < 2 {
		return []string{text}
	}
	result := []string{}
	start := 0
	for _, location := range locations[1:] {
		result = append(result, text[start:location[0]])
		start = location[0]
	}
	return append(result, text[start:])
}

func noticeReasonLabelScope(text string, sources []noticeQuestionClaimSource) []string {
	colon := strings.Index(text, ":")
	if colon < 0 {
		return nil
	}
	label := strings.Trim(noticeReasonCompact(text[:colon]), "-+•[]")
	for _, source := range sources {
		for _, alias := range noticeReasonExchangeAliases(source.Exchange) {
			if alias != "" && label == alias {
				return []string{strings.ToLower(source.Exchange)}
			}
		}
	}
	return nil
}

func noticeReasonClauses(text string, sources []noticeQuestionClaimSource) []noticeReasonClause {
	clauses := []noticeReasonClause{}
	add := func(text string, exchanges []string) {
		for _, sentence := range noticeReasonSentence.Split(text, -1) {
			for _, subjectClause := range noticeReasonSubjectClauses(sentence, sources) {
				scope := exchanges
				if named := noticeReasonMentionedExchanges(subjectClause, sources); len(named) > 0 {
					scope = named
				}
				for _, part := range noticeReasonContrast.Split(subjectClause, -1) {
					if strings.TrimSpace(part) != "" {
						clauses = append(clauses, noticeReasonClause{part, scope})
					}
				}
			}
		}
	}
	var tableColumns [][]string
	for _, line := range strings.Split(text, "\n") {
		if strings.Count(line, "|") >= 2 {
			cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
			columnScopes := make([][]string, len(cells))
			namedColumns := 0
			for i, cell := range cells {
				columnScopes[i] = noticeReasonMentionedExchanges(cell, sources)
				if len(columnScopes[i]) > 0 {
					namedColumns++
				}
			}
			if namedColumns >= 2 {
				tableColumns = columnScopes
				continue
			}
			rowScope := noticeReasonMentionedExchanges(cells[0], sources)
			for i, cell := range cells {
				scope := rowScope
				if len(scope) == 0 && i < len(tableColumns) {
					scope = tableColumns[i]
				}
				add(cell, scope)
			}
		} else {
			tableColumns = nil
			// An explicit bullet label owns all sentences in that list item,
			// just as a table row header owns each cell's source scope.
			add(line, noticeReasonLabelScope(line, sources))
		}
	}
	return clauses
}

func noticeReasonSupportsFault(source noticeQuestionClaimSource, term string) bool {
	for _, passage := range source.Passages {
		for _, clause := range noticeReasonClauses(passage, nil) {
			if noticeReasonAssertsFault(clause.text, term) {
				return true
			}
		}
	}
	return false
}

func noticeReasonStatesUnresolved(text string) bool {
	statement := noticeClaimStatement(text)
	if statement.Residual != "" && noticeReasonStatesUnresolved(statement.Residual) {
		return true
	}
	if statement.Matrix != noticeClaimAsserted {
		return false
	}
	for _, unresolved := range noticeReasonUnresolved.FindAllStringIndex(statement.Proposition, -1) {
		tail := statement.Proposition[unresolved[1]:]
		// A conditional/possible outcome or an open question is not a finding
		// that the reason remained unresolved.
		if strings.HasPrefix(tail, "으면") || strings.HasPrefix(tail, "을경우") || strings.HasPrefix(tail, "을수") || strings.HasPrefix(tail, "았는지") || strings.HasPrefix(tail, "는지") || strings.HasPrefix(tail, "여부") {
			continue
		}
		if !noticeReasonDenial.MatchString(statement.Proposition[unresolved[0]:]) {
			return true
		}
	}
	return false
}

func noticeReasonSourceGroups(clause noticeReasonClause, sources []noticeQuestionClaimSource) map[string][]noticeQuestionClaimSource {
	mentioned := noticeReasonMentionedExchanges(clause.text, sources)
	if len(mentioned) == 0 {
		mentioned = clause.exchanges
	}
	assets := []string{}
	for _, source := range sources {
		for _, asset := range source.Assets {
			if asset != "" && noticeQuestionHasAnchor(clause.text, asset) {
				assets = append(assets, asset)
			}
		}
	}
	groups := map[string][]noticeQuestionClaimSource{}
	for _, source := range sources {
		exchange := strings.ToLower(source.Exchange)
		if strings.HasPrefix(source.SourceID, "server-volume:") || (len(mentioned) > 0 && !slices.Contains(mentioned, exchange)) {
			continue
		}
		if len(assets) > 0 && !slices.ContainsFunc(source.Assets, func(asset string) bool { return slices.Contains(assets, asset) }) {
			continue
		}
		groups[exchange] = append(groups[exchange], source)
	}
	return groups
}

func noticeReasonSourceStatesUnresolved(source noticeQuestionClaimSource) bool {
	for _, passage := range source.Passages {
		for _, original := range noticeReasonClauses(passage, nil) {
			if noticeReasonStatesUnresolved(original.text) {
				return true
			}
		}
	}
	return false
}

// Not finding evidence of resolution is an epistemic limit, not a finding of
// non-resolution. A later summary must retain that distinction for each venue,
// even when an earlier paragraph quotes the original accurately.
func noticeReasonValidateResolution(clause noticeReasonClause, sources []noticeQuestionClaimSource) error {
	if !noticeReasonStatesUnresolved(clause.text) {
		return nil
	}
	groups := noticeReasonSourceGroups(clause, sources)
	collective := len(clause.exchanges) > 0 || len(noticeReasonMentionedExchanges(clause.text, sources)) > 0 || noticeReasonCollective.MatchString(noticeReasonCompact(clause.text))
	supported := false
	for _, group := range groups {
		ok := slices.ContainsFunc(group, noticeReasonSourceStatesUnresolved)
		supported = supported || ok
		if collective && !ok {
			return fmt.Errorf("notice answer reason-strength turns unconfirmed resolution into a non-resolution finding: %s", strings.TrimSpace(clause.text))
		}
	}
	if len(groups) > 0 && !supported {
		return fmt.Errorf("notice answer reason-strength states non-resolution absent from official evidence: %s", strings.TrimSpace(clause.text))
	}
	return nil
}

func noticeReasonValidateDocumentAbsence(clause noticeReasonClause, sources []noticeQuestionClaimSource, terms []string) error {
	statement := noticeClaimStatement(clause.text, sources...)
	if statement.Matrix != noticeClaimDocumentAbsent {
		return nil
	}
	criteria := []string{}
	for _, term := range terms {
		if noticeReasonAssertsFault(statement.Proposition, term) {
			criteria = append(criteria, term)
		}
	}
	unresolved := noticeReasonStatesUnresolved(statement.Proposition)
	if len(criteria) == 0 && !unresolved {
		return nil // No source-derived reason proposition within this guard's scope.
	}
	groups := noticeReasonSourceGroups(clause, sources)
	for _, group := range groups {
		supported := !unresolved || slices.ContainsFunc(group, noticeReasonSourceStatesUnresolved)
		for _, criterion := range criteria {
			supported = supported && slices.ContainsFunc(group, func(source noticeQuestionClaimSource) bool {
				return noticeReasonSupportsFault(source, criterion)
			})
		}
		if !supported {
			return nil
		}
	}
	if len(groups) > 0 {
		return fmt.Errorf("notice answer reason-strength reports an explicit source finding as unstated: %s", strings.TrimSpace(clause.text))
	}
	return nil
}

func noticeReasonValidateMetaProhibition(clause noticeReasonClause, sources []noticeQuestionClaimSource, terms []string) error {
	statement := noticeClaimStatement(clause.text, sources...)
	if statement.Matrix != noticeClaimOverstatement || !statement.Negated {
		return nil
	}
	if statement.Residual != "" {
		if err := validateNoticeQuestionReasonStrength(statement.Residual, sources); err != nil {
			return err
		}
	}
	explicitCriteria := []string{}
	for _, term := range terms {
		if noticeReasonAssertsFault(statement.Proposition, term) {
			explicitCriteria = append(explicitCriteria, term)
		}
	}
	groups := noticeReasonSourceGroups(clause, sources)
	if len(groups) == 0 {
		return nil
	}
	for _, group := range groups {
		groupTerms := noticeReasonTerms(group)
		criteria := append([]string{}, explicitCriteria...)
		// A generic "all reviewed items" proposition does not repeat the
		// criteria. Resolve its quantifier only inside this source group, so one
		// venue's individual findings cannot prove another venue's prohibition
		// false.
		if len(criteria) == 0 && noticeReasonAllItems.MatchString(statement.Proposition) && noticeReasonAssertsFault(statement.Proposition, "") {
			criteria = append(criteria, groupTerms...)
		}
		if len(criteria) == 0 {
			return nil
		}
		for _, criterion := range criteria {
			if !slices.ContainsFunc(group, func(source noticeQuestionClaimSource) bool {
				return noticeReasonSupportsFault(source, criterion)
			}) {
				return nil
			}
		}
	}
	return fmt.Errorf("notice answer reason-strength denies a finding explicitly stated by its source: %s", strings.TrimSpace(clause.text))
}

func noticeReasonValidateStatementResiduals(text string, sources []noticeQuestionClaimSource) error {
	for _, sentence := range noticeReasonSentence.Split(text, -1) {
		statement := noticeClaimStatement(sentence, sources...)
		if statement.Matrix == noticeClaimAsserted || statement.Residual == "" {
			continue
		}
		// The matrix governs only its proposition. A finite reason, condition,
		// or service event joined to it remains an ordinary grounded claim.
		if err := validateNoticeQuestionClaimText(statement.Residual, sources); err != nil {
			return fmt.Errorf("notice answer meta-statement has an unsupported independent claim: %w", err)
		}
	}
	return nil
}

// validateNoticeQuestionReasonStrength rejects an explicit defect attributed to
// a reviewed criterion unless the matching official source asserts that defect.
// A collective assertion needs support from every referenced exchange. Unknown
// subjects or implicit cross-sentence referents remain an entailment limitation.
func validateNoticeQuestionReasonStrength(text string, sources []noticeQuestionClaimSource) error {
	if err := noticeReasonValidateStatementResiduals(text, sources); err != nil {
		return err
	}
	allTerms := noticeReasonTerms(sources)
	for _, clause := range noticeReasonClauses(text, sources) {
		if err := noticeReasonValidateMetaProhibition(clause, sources, allTerms); err != nil {
			return err
		}
		if err := noticeReasonValidateDocumentAbsence(clause, sources, allTerms); err != nil {
			return err
		}
		if noticeClaimStatement(clause.text, sources...).Matrix == noticeClaimDocumentAbsent {
			continue // Its embedded reason was checked against the source above.
		}
		if err := noticeReasonValidateResolution(clause, sources); err != nil {
			return err
		}
		compact := noticeReasonCompact(clause.text)
		if !noticeReasonAssertsFault(clause.text, "") {
			continue
		}
		// Only a review belonging to this clause's venue/asset supplies its
		// criteria. Another venue's long policy context is not a vocabulary of
		// individual violations to impose on this source's independent finding.
		scopedSources := []noticeQuestionClaimSource{}
		for _, group := range noticeReasonSourceGroups(clause, sources) {
			scopedSources = append(scopedSources, group...)
		}
		if len(scopedSources) == 0 && len(allTerms) > 0 {
			return fmt.Errorf("notice answer reason-strength has no official source for its exchange/asset scope: %s", strings.TrimSpace(clause.text))
		}
		terms := noticeReasonTerms(scopedSources)
		mentioned := noticeReasonMentionedExchanges(clause.text, sources)
		if len(mentioned) == 0 {
			mentioned = clause.exchanges
		}
		collective := noticeReasonCollective.MatchString(compact) || (len(mentioned) != 1 && strings.Contains(compact, "모두"))
		assets := []string{}
		for _, source := range sources {
			for _, asset := range source.Assets {
				if asset != "" && noticeQuestionHasAnchor(clause.text, asset) {
					assets = append(assets, asset)
				}
			}
		}
		namedCriteria := []string{}
		for _, term := range terms {
			if strings.Contains(compact, term) {
				namedCriteria = append(namedCriteria, term)
			}
		}
		for _, term := range terms {
			// "Both named items individually violated" quantifies those items,
			// not every contextual word appearing before the original review.
			allItems := noticeReasonAllItems.MatchString(compact) && (len(namedCriteria) == 0 || slices.Contains(namedCriteria, term))
			if !noticeReasonAssertsFault(clause.text, term) && !(allItems && noticeReasonAssertsFault(clause.text, "")) {
				continue
			}
			perExchange := map[string]bool{}
			for _, source := range sources {
				if strings.HasPrefix(source.SourceID, "server-volume:") {
					continue
				}
				exchange := strings.ToLower(source.Exchange)
				if len(mentioned) > 0 && !slices.Contains(mentioned, exchange) {
					continue
				}
				if len(assets) > 0 {
					matches := false
					for _, asset := range source.Assets {
						if slices.Contains(assets, asset) {
							matches = true
							break
						}
					}
					if !matches {
						continue
					}
				}
				if _, ok := perExchange[exchange]; !ok {
					perExchange[exchange] = false
				}
				if noticeReasonSupportsFault(source, term) {
					perExchange[exchange] = true
				}
			}
			if len(perExchange) == 0 {
				continue
			}
			supported := false
			for _, ok := range perExchange {
				supported = supported || ok
				if collective || len(mentioned) > 0 {
					if !ok {
						return fmt.Errorf("notice answer reason-strength strengthens a reviewed criterion into an unsupported individual/common defect (%s): %s", term, strings.TrimSpace(clause.text))
					}
				}
			}
			if !supported {
				return fmt.Errorf("notice answer reason-strength states a defect absent from official review evidence (%s): %s", term, strings.TrimSpace(clause.text))
			}
		}
	}
	return nil
}
