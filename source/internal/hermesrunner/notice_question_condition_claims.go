package hermesrunner

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// These checks cover explicit prerequisites and service-processing promises.
// They do not turn missing policy text into a negative service fact.
var (
	noticeConditionRequired       = regexp.MustCompile(`([가-힣]{2,8})[\t ]*(?:해야|하여야|하셔야)`)
	noticeConditionPossible       = regexp.MustCompile(`수(?:는|도)?있|가능성|가능하|가능합|가능할`)
	noticeConditionFree           = regexp.MustCompile(`무료|수수료(?:가|는)?없|수수료없이|수수료(?:가|는)?발생하지않`)
	noticeConditionGuaranteed     = regexp.MustCompile(`(?:반드시|무조건|항상|예외없이)[가-힣]{0,12}(?:출금(?:됩|된다|된다고)|처리|지급|반영|성공)|(?:출금|처리|반영|성공)[가-힣]{0,10}보장`)
	noticeConditionProcess        = regexp.MustCompile(`처리|반영`)
	noticeConditionProcessVerb    = regexp.MustCompile(`^(?:하|한|할|합|해|했|되|된|될|됩|돼|됐)`)
	noticeConditionOrder          = regexp.MustCompile(`순차|순서대로|차례대로`)
	noticeConditionDirection      = regexp.MustCompile(`입출금|입금|입고|출금|출고|들어온`)
	noticeConditionTimeContext    = regexp.MustCompile(`^(?:지원|서비스)?(?:재개|중단|중지)(?:시점)?(?:이후|후|뒤|시|기간)`)
	noticeConditionNegative       = regexp.MustCompile(`미(?:처리|반영)|(?:처리|반영)(?:가|이)?(?:되지|하지|되지도|하지도)않|(?:처리|반영)(?:가|이)?불가|(?:처리|반영)할수없|(?:처리|반영)(?:가|이)?안(?:되|될|됩)`)
	noticeConditionUnknown        = regexp.MustCompile(`(?:약속|여부|방침|안내|설명|내용|방식|방법)[가-힣]{0,16}(?:확인되지않|명시되지않|명시하지않|적혀있지않|기재되어있지않|없|찾을수없)|(?:단정|확인|판단)할수(?:는|가)?없|(?:뜻|의미)(?:은|는|가|이)?(?:아니|아닙|아님)`)
	noticeConditionPendingObject  = regexp.MustCompile(`(?:처리|반영)(?:(?:가|이)?완료)?(?:되지|하지)않(?:은|았던)(입금|출금|신청분|신청건|건|자산|가상자산|금액)`)
	noticeConditionDocumentObject = regexp.MustCompile(`반영(?:된|되는|되어있는)((?:공지|원문|본문)(?:제목|내용)?|제목|수정시각)`)
)

// A pending object's negative state modifies the noun; it is not the polarity
// of the later processing predicate. Preserve its noun/direction in this
// predicate-only projection. Other claim checks still receive the full text.
func noticeConditionPredicateText(text string) string {
	text = noticeConditionPendingObject.ReplaceAllString(noticeReasonCompact(text), "$1")
	// Reflected in a document/title describes reported information, not money
	// credited to an account. Keep the document object for the other guards.
	return noticeConditionDocumentObject.ReplaceAllString(text, "$1")
}

func noticeConditionTopic(text string) string {
	text = noticeConditionPredicateText(text)
	if at := noticeConditionProcess.FindStringIndex(text); at != nil {
		text = text[:at[0]]
	}
	// A temporal modifier ("입출금 재개 후") cannot replace the object
	// being processed ("입금분" / "들어온 자산"). Keep the latest actual
	// direction and leave processing/reflection as equivalent outcome verbs.
	topic := ""
	for _, location := range noticeConditionDirection.FindAllStringIndex(text, -1) {
		word := text[location[0]:location[1]]
		if word == "입출금" && noticeConditionTimeContext.MatchString(text[location[1]:]) {
			continue
		}
		switch word {
		case "입금", "입고", "들어온":
			topic = "DEPOSIT"
		case "출금", "출고":
			topic = "WITHDRAW"
		default:
			topic = "TRANSFER"
		}
	}
	return topic
}

func noticeConditionSameTopic(claim, source string) bool {
	left, right := noticeConditionTopic(claim), noticeConditionTopic(source)
	return left == "" || left == right || right == "TRANSFER"
}

func noticeConditionSourceClauses(sources []noticeQuestionClaimSource) []string {
	result := []string{}
	for _, source := range sources {
		for _, passage := range source.Passages {
			for _, clause := range noticeReasonClauses(passage, nil) {
				result = append(result, clause.text)
			}
		}
	}
	return result
}

func noticeConditionIsUnknown(text string, sources ...noticeQuestionClaimSource) bool {
	return noticeClaimStatement(text, sources...).Matrix != noticeClaimAsserted || noticeConditionUnknown.MatchString(noticeReasonCompact(text))
}

func noticeConditionProcessingPredicate(text string) bool {
	compact := noticeConditionPredicateText(text)
	if !noticeConditionProcess.MatchString(compact) {
		return false
	}
	if noticeConditionNegative.MatchString(compact) || noticeConditionOrder.MatchString(compact) || noticeConditionGuaranteed.MatchString(compact) {
		return true
	}
	for _, process := range noticeConditionProcess.FindAllStringIndex(compact, -1) {
		// Require a processing predicate, not just the noun "processing" in a
		// policy comparison. Korean outcome verbs use 하다/되다 conjugations;
		// explicit negative, ordered and guaranteed outcomes are handled above.
		tail := compact[process[1]:]
		for _, particle := range []string{"이", "가", "은", "는", "도", "을", "를"} {
			if strings.HasPrefix(tail, particle) {
				tail = strings.TrimPrefix(tail, particle)
				break
			}
		}
		if noticeConditionProcessVerb.MatchString(tail) {
			return true
		}
		if possible := noticeConditionPossible.FindStringIndex(tail); possible != nil && possible[0] == 0 {
			return true
		}
	}
	return false
}

func noticeConditionFreeClaim(text string) bool {
	compact := noticeReasonCompact(text)
	return noticeConditionFree.MatchString(compact) && !noticeReasonDenial.MatchString(compact) && !strings.Contains(compact, "무료가아니") && !strings.Contains(compact, "무료는아니")
}

func noticeConditionNegatesPrerequisite(claim string, originals []string) bool {
	compact := noticeReasonCompact(claim)
	if noticeConditionIsUnknown(claim) {
		return false
	}
	for _, original := range originals {
		if !noticeConditionSameTopic(claim, original) {
			continue
		}
		for _, match := range noticeConditionRequired.FindAllStringSubmatch(original, -1) {
			action := noticeReasonCompact(match[1])
			for _, suffix := range []string{"하지않아도", "하지않더라도", "하지않고도", "없이", "불필요"} {
				if strings.Contains(compact, action+suffix) {
					return true
				}
			}
		}
	}
	return false
}

func noticeConditionSupported(claim string, sources []noticeQuestionClaimSource) bool {
	return noticeConditionSupportedWithGuarantee(claim, sources, false)
}

func noticeConditionSupportedWithGuarantee(claim string, sources []noticeQuestionClaimSource, requireGuarantee bool) bool {
	statement := noticeClaimStatement(claim, sources...)
	if statement.Matrix == noticeClaimDocumentAbsent {
		// Absence is a claim about this source, not a license to ignore it. If
		// the original does explicitly support the embedded promise, denying
		// that the document states it is itself unsupported.
		return !noticeConditionSupportedWithGuarantee(statement.Proposition, sources, statement.EmbeddedGuarantee)
	}
	if statement.Target == noticeClaimTargetGuarantee && statement.Negated {
		if statement.Residual != "" && !noticeConditionSupported(statement.Residual, sources) {
			return false
		}
		// A denial of a guarantee is neither a guarantee nor a categorical
		// impossibility. It still contradicts a matching explicit guarantee.
		for _, original := range noticeConditionSourceClauses(sources) {
			raw := noticeConditionPredicateText(original)
			frame := noticeClaimStatement(original, sources...)
			if noticeConditionSameTopic(statement.Proposition, original) && noticeConditionGuaranteed.MatchString(raw) && !frame.Negated && !noticeConditionIsUnknown(original, sources...) && !noticeConditionPossible.MatchString(raw) {
				return false
			}
		}
		return true
	}
	compact := noticeConditionPredicateText(statement.Proposition)
	if noticeConditionIsUnknown(claim, sources...) {
		return true
	}
	originals := noticeConditionSourceClauses(sources)
	if noticeConditionNegatesPrerequisite(claim, originals) {
		return false
	}
	if noticeConditionFreeClaim(claim) {
		supported := false
		for _, original := range originals {
			if noticeConditionSameTopic(claim, original) && noticeConditionFreeClaim(original) {
				supported = true
				break
			}
		}
		if !supported {
			return false
		}
	}
	if (requireGuarantee || noticeConditionGuaranteed.MatchString(compact)) && !noticeReasonDenial.MatchString(compact) {
		supported := false
		for _, original := range originals {
			raw := noticeConditionPredicateText(original)
			if noticeConditionSameTopic(claim, original) && noticeConditionGuaranteed.MatchString(raw) && !noticeClaimStatement(original, sources...).Negated && !noticeConditionIsUnknown(original, sources...) && !noticeConditionPossible.MatchString(raw) {
				supported = true
				break
			}
		}
		if !supported {
			return false
		}
	}
	// Processing statements need a source predicate for this asset/direction.
	// "A processing promise is not stated" is handled above, independently of
	// "the deposit will never be processed", which is a negative factual claim.
	if !noticeConditionProcessingPredicate(compact) {
		return true
	}
	negative := noticeConditionNegative.MatchString(compact)
	possible := noticeConditionPossible.MatchString(compact)
	ordered := noticeConditionOrder.MatchString(compact)
	for _, original := range originals {
		raw := noticeConditionPredicateText(original)
		if !noticeConditionSameTopic(claim, original) || !noticeConditionProcessingPredicate(raw) || noticeConditionIsUnknown(original, sources...) {
			continue
		}
		if noticeConditionNegative.MatchString(raw) != negative {
			continue
		}
		if noticeConditionPossible.MatchString(raw) && !possible {
			continue
		}
		if ordered && !noticeConditionOrder.MatchString(raw) {
			continue
		}
		return true
	}
	return false
}

// validateNoticeQuestionConditionClaims retains the scope and strength of
// explicit prerequisites/processing promises in direct official passages.
// It does not infer unnamed recipients or policy conditions omitted by both
// the claim and the retrieved source. Existing numeric/event guards own those.
func validateNoticeQuestionConditionClaims(text string, sources []noticeQuestionClaimSource) error {
	for _, clause := range noticeReasonClauses(text, sources) {
		compact := noticeReasonCompact(clause.text)
		if !noticeConditionProcess.MatchString(compact) && !noticeConditionFree.MatchString(compact) && !noticeConditionGuaranteed.MatchString(compact) && !strings.Contains(compact, "않아도") && !strings.Contains(compact, "않더라도") && !strings.Contains(compact, "않고도") && !strings.Contains(compact, "없이") && !strings.Contains(compact, "불필요") {
			continue
		}
		if noticeConditionIsUnknown(clause.text, sources...) && noticeClaimStatement(clause.text, sources...).Matrix != noticeClaimDocumentAbsent {
			continue
		}
		exchanges := noticeReasonMentionedExchanges(clause.text, sources)
		if len(exchanges) == 0 {
			exchanges = clause.exchanges
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
			if strings.HasPrefix(source.SourceID, "server-volume:") {
				continue
			}
			exchange := strings.ToLower(source.Exchange)
			if len(exchanges) > 0 && !slices.Contains(exchanges, exchange) {
				continue
			}
			if len(assets) > 0 {
				matched := false
				for _, asset := range source.Assets {
					if slices.Contains(assets, asset) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			groups[exchange] = append(groups[exchange], source)
		}
		if len(groups) == 0 {
			continue
		}
		collective := len(exchanges) > 0 || noticeReasonCollective.MatchString(compact) || strings.Contains(compact, "모두")
		anySupported := false
		for _, group := range groups {
			supported := noticeConditionSupported(clause.text, group)
			anySupported = anySupported || supported
			if collective && !supported {
				return fmt.Errorf("notice answer condition/processing strength is unsupported for its source: %s", strings.TrimSpace(clause.text))
			}
		}
		if !anySupported {
			return fmt.Errorf("notice answer condition/processing claim is absent from official evidence: %s", strings.TrimSpace(clause.text))
		}
	}
	return nil
}
