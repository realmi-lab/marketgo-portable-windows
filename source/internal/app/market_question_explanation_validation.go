package app

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/marketquestion"
)

const marketQuestionExplanationValidatorVersion = "MARKET_QUESTION_EXPLANATION_VALIDATOR_V3"

var (
	marketQuestionPositiveRelation        = regexp.MustCompile(`(?:늘(?:었|어|어난|고)|증가|상승|높아|높았|많아|확대)`)
	marketQuestionNegativeRelation        = regexp.MustCompile(`(?:줄(?:었|어|어든|고)|감소|하락|낮아|낮았|적어|축소)`)
	marketQuestionFlatRelation            = regexp.MustCompile(`(?:같(?:습니다|다|았)|동일|유지|변함\s*없)`)
	marketQuestionContribution            = regexp.MustCompile(`(?:기여|완화|압력|밀어)`)
	marketQuestionUnavailable             = regexp.MustCompile(`(?:확인할\s*수\s*없|확정할\s*수\s*없|자료\s*(?:가\s*)?부족|미확인|계산\s*불가|지원하지\s*않)`)
	marketQuestionCausalAssertion         = regexp.MustCompile(`(?:때문에|로\s*인해|원인(?:이|으로)|자금\s*유입|매수세가\s*유입|촉발)`)
	marketQuestionOpenReference           = regexp.MustCompile(`(?:^|[.!?]\s+)(?:이|그|해당)\s*(?:금액|수치|비율|점유율|변화|결과|움직임|규모)`)
	marketQuestionFinancialProse          = regexp.MustCompile(`(?:[0-9]|%|원|거래대금|점유율|비중|가격|체결|업비트|빗썸|코인원|코빗|고팍스|증가|감소|상승|하락|기여|분모|rolling|WebSocket|공식|인과|원인|유입)`)
	marketQuestionClaimNegation           = regexp.MustCompile(`(?:아닙|아니|않(?:았|습|다|은|는|을)|못(?:했|합|하|한))`)
	marketQuestionClosedContributionClaim = regexp.MustCompile(`(?i)^(?:(?:현재|최근|공식|비교\s*구간에서|업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax|[a-z0-9]+)(?:의)?\s*)*(?:점유율\s*(?:변화\s*)?)?기여(?:도)?(?:은|는|이|가)?\s*[-+]?(?:[0-9]+(?:,[0-9]{3})*)(?:\.[0-9]+)?(?:e[-+]?[0-9]+)?\s*%p(?:입니다|이다|로\s*(?:확인|집계)됩니다?)?$`)
	marketQuestionExchangeCount           = regexp.MustCompile(`([0-9]+)\s*개\s*거래소`)
	marketQuestionAlternativeBasis        = regexp.MustCompile(`(?:이용자|사용자|가입자|보유\s*자산|예치금|잔고)`)
	marketQuestionShareDefinition         = regexp.MustCompile(`^(?:이는\s+|이\s*값은\s+)?(?:현재\s+)?(?:해당\s+)?공식\s+(?:(?:표본에서\s+원화로\s+거래된\s+금액)|(?:국내\s+전체\s+거래(?:대금|금액)))\s+중\s+(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:이|가)\s+차지하는\s+비중(?:을\s+뜻하며|이며)$`)
	marketQuestionBasisExclusion          = regexp.MustCompile(`^(?:(?:이용자|사용자|가입자)(?:\s*수)?|보유\s*자산|예치금|잔고)(?:(?:나|이나|와|과|·)\s*(?:(?:이용자|사용자|가입자)(?:\s*수)?|보유\s*자산|예치금|잔고))*\s+기준(?:의)?\s*(?:점유율|비중)?(?:은|는|이|가)?\s*(?:아닙니다|아니다|아니며)\s*[.]?$`)
	marketQuestionHeadingAssertion        = regexp.MustCompile(`(?:입니다|합니다|했습니다|됩니다|됐습니다|이다|했다|한다|된다)\s*$`)
	marketQuestionHeadingNoun             = regexp.MustCompile(`(?:의미|해석|범위|기준|한계|관계|구분|설명)$`)
	marketQuestionSourcePreamble          = regexp.MustCompile(`^(?:제공된\s+)?(?:현재\s+)?(?:해당\s+)?공식\s+rolling\s*24시간\s+원화마켓\s*(?:자료)?에서$`)
	marketQuestionDenomBridge             = regexp.MustCompile(`^(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*(?:점유율|비중)(?:의)?\s*(?:비교\s*)?기준인$`)
	marketQuestionPureCausalLimit         = regexp.MustCompile(`^(?:(?:이|제공된)\s*자료|공지와\s*(?:거래대금|점유율)\s*변화의\s*시간적\s*근접성|시간적\s*근접성|관측된\s*동시\s*발생)(?:만으로|자체로는?)\s*인과관계를\s*(?:단정|확정|입증)할\s*수\s*없습니다$`)
	marketQuestionClosedLevelClaim        = regexp.MustCompile(`(?i)^(?:(?:제공된|현재|최근|해당|공식|rolling\s*24시간|원화마켓|자료에서|비교\s*구간에서|국내\s*전체|국내\s*[0-9]+\s*개\s*거래소\s*기준|동일\s*코인\s*기준|업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*)*(?:점유율|비중|포착률|거래대금|거래금액|체결\s*건수)(?:은|는|이|가)?\s*[-+]?(?:[0-9]+(?:,[0-9]{3})*)(?:\.[0-9]+)?(?:e[-+]?[0-9]+)?\s*(?:%p|%|조원|억원|만원|원|건)(?:입니다|이다|로\s*(?:확인|집계)됩니다?)?$`)
	marketQuestionClosedChangeClaim       = regexp.MustCompile(`(?i)^(?:(?:현재|최근|비교\s*구간에서|직전\s*구간보다|이전보다|국내\s*전체|국내\s*[0-9]+\s*개\s*거래소\s*기준|동일\s*코인\s*기준|업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*)*(?:점유율|비중|포착률|거래대금|거래금액|체결\s*건수)(?:\s*변화(?:량)?)?(?:은|는|이|가)?\s*(?:[-+]?(?:[0-9]+(?:,[0-9]{3})*)(?:\.[0-9]+)?(?:e[-+]?[0-9]+)?\s*(?:%p|%|조원|억원|만원|원|건)\s*)?(?:(?:증가|상승|확대|감소|하락|축소)(?:했|하였|됐)?습니다|늘었습니다|줄었습니다|높아졌습니다|낮아졌습니다|(?:같|동일)습니다|유지됐습니다|변함\s*없습니다)$`)
)

func marketQuestionValidateExplanationBlocks(
	answer marketanalysis.MarketQuestionAnswer,
	bundle marketquestion.EvidenceBundleSnapshot,
) (marketanalysis.MarketQuestionAnswer, []marketanalysis.MarketQuestionExplanationBlock, marketanalysis.MarketQuestionValidationStatus) {
	if !marketQuestionProviderAnswerCacheable(answer.Source) || len(answer.ClaimBlocks) == 0 {
		return marketanalysis.MarketQuestionAnswer{Source: answer.Source, Model: answer.Model, AsOfMS: answer.AsOfMS}, []marketanalysis.MarketQuestionExplanationBlock{{
			Index: 0, Status: marketanalysis.MarketQuestionValidationUndetermined,
			Reason: "MODEL_EXPLANATION_HAS_NO_TYPED_CLAIM_BLOCKS", ClaimRefs: []string{},
		}}, marketanalysis.MarketQuestionValidationUndetermined
	}
	factIndex := make(map[string]marketquestion.EvidenceFact, len(bundle.Facts))
	for _, fact := range bundle.Facts {
		factIndex[fact.ID] = fact
	}
	paragraphs := marketQuestionExplanationParagraphs(answer.Answer)
	blocks := make([]marketanalysis.MarketQuestionExplanationBlock, 0, len(answer.ClaimBlocks)+len(paragraphs))
	accepted := make([]string, 0, len(answer.ClaimBlocks))
	overall := marketanalysis.MarketQuestionValidationSupported
	covered := make([]bool, len(paragraphs))
	for index, claimBlock := range answer.ClaimBlocks {
		result := marketanalysis.MarketQuestionExplanationBlock{
			Index: index, Text: claimBlock.Text, Status: marketanalysis.MarketQuestionValidationSupported,
			ClaimRefs: make([]string, 0, len(claimBlock.Claims)),
		}
		paragraphIndex := -1
		for candidate, paragraph := range paragraphs {
			if !covered[candidate] && marketQuestionClaimBlockMatchesParagraph(paragraph, claimBlock.Text) {
				paragraphIndex = candidate
				covered[candidate] = true
				break
			}
		}
		if paragraphIndex < 0 || marketQuestionOpenReference.MatchString(claimBlock.Text) {
			result.Status = marketanalysis.MarketQuestionValidationUndetermined
			result.Reason = "BLOCK_IS_NOT_A_CLOSED_ANSWER_PARAGRAPH"
		}
		if len(claimBlock.Claims) == 0 && result.Status == marketanalysis.MarketQuestionValidationSupported {
			result.Status = marketanalysis.MarketQuestionValidationUndetermined
			result.Reason = "BLOCK_HAS_NO_TYPED_FACT_REFERENCE"
		}
		for _, claim := range claimBlock.Claims {
			result.ClaimRefs = append(result.ClaimRefs, claim.FactID)
			if strings.TrimSpace(claim.Text) == "" || !strings.Contains(claimBlock.Text, claim.Text) {
				result.Status = marketanalysis.MarketQuestionValidationConflict
				result.Reason = "CLAIM_TEXT_IS_NOT_IN_BLOCK"
				continue
			}
			fact, ok := factIndex[claim.FactID]
			if !ok {
				result.Status = marketanalysis.MarketQuestionValidationConflict
				result.Reason = "UNKNOWN_FACT_REFERENCE"
				continue
			}
			relationStatus, reason := marketQuestionClaimRelationStatus(claim, claimBlock.Text, fact, bundle)
			if relationStatus == marketanalysis.MarketQuestionValidationConflict ||
				(result.Status == marketanalysis.MarketQuestionValidationSupported && relationStatus == marketanalysis.MarketQuestionValidationUndetermined) {
				result.Status, result.Reason = relationStatus, reason
			}
		}
		if status, reason := marketQuestionClaimCoverageStatus(claimBlock, bundle); result.Status == marketanalysis.MarketQuestionValidationSupported && status != marketanalysis.MarketQuestionValidationSupported {
			result.Status, result.Reason = status, reason
		}
		if marketQuestionCausalAssertion.MatchString(claimBlock.Text) && !marketQuestionBlockDeniesCausalityLocally(claimBlock) {
			result.Status = marketanalysis.MarketQuestionValidationConflict
			result.Reason = "UNSUPPORTED_CAUSAL_ASSERTION"
		}
		if result.Status == marketanalysis.MarketQuestionValidationSupported {
			accepted = append(accepted, strings.TrimSpace(claimBlock.Text))
		} else {
			// Withheld model prose must not survive in the public status object.
			// Keep only diagnostic codes and fact IDs.
			result.Text = ""
		}
		overall = marketQuestionMergeValidationStatus(overall, result.Status)
		blocks = append(blocks, result)
	}
	for index := range paragraphs {
		if covered[index] {
			continue
		}
		blocks = append(blocks, marketanalysis.MarketQuestionExplanationBlock{
			Index: len(blocks), Status: marketanalysis.MarketQuestionValidationUndetermined,
			Reason: "ANSWER_PARAGRAPH_IS_NOT_CLAIM_BOUND", ClaimRefs: []string{},
		})
		overall = marketQuestionMergeValidationStatus(overall, marketanalysis.MarketQuestionValidationUndetermined)
	}
	filtered := marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(accepted, "\n\n"), Source: answer.Source, Model: answer.Model, AsOfMS: answer.AsOfMS,
	}
	return filtered, blocks, overall
}

func marketQuestionClaimRelationStatus(
	claim marketanalysis.MarketQuestionClaimReference,
	blockText string,
	fact marketquestion.EvidenceFact,
	bundle marketquestion.EvidenceBundleSnapshot,
) (marketanalysis.MarketQuestionValidationStatus, string) {
	relation := strings.ToUpper(strings.TrimSpace(claim.Relation))
	text := strings.TrimSpace(claim.Text)
	if text == "" {
		return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_IS_EMPTY"
	}
	if relation == "COINCIDENCE_ONLY" {
		if marketQuestionClaimIsPureCausalLimit(text) {
			return marketanalysis.MarketQuestionValidationSupported, ""
		}
		return marketanalysis.MarketQuestionValidationUndetermined, "COINCIDENCE_LIMIT_IS_NOT_CLOSED"
	}
	boundedDefinition := marketQuestionClaimIsBoundedShareDefinition(text, fact, bundle)
	if marketQuestionClaimNegation.MatchString(text) && !boundedDefinition {
		if marketQuestionAlternativeBasis.MatchString(text) && !marketQuestionClaimHasQuantityUnit(text, fact.Value.Unit) {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_DEFINITION_NEGATION_IS_NOT_CLOSED"
		}
		return marketanalysis.MarketQuestionValidationConflict, "CLAIM_NEGATES_TYPED_FACT"
	}
	expected := marketQuestionEvidenceFactRelation(fact)
	if expected == "" {
		return marketanalysis.MarketQuestionValidationUndetermined, "FACT_RELATION_IS_NOT_DECIDABLE"
	}
	if relation != expected {
		return marketanalysis.MarketQuestionValidationConflict, "CLAIM_RELATION_CONFLICTS_WITH_TYPED_FACT"
	}
	if status, reason := marketQuestionClaimFactSemantics(text, blockText, fact, bundle, relation, boundedDefinition); status != marketanalysis.MarketQuestionValidationSupported {
		return status, reason
	}
	if marketQuestionClaimHasQuantityUnit(text, fact.Value.Unit) && !marketQuestionClaimContainsTypedValue(text, fact.Value) {
		return marketanalysis.MarketQuestionValidationConflict, "CLAIM_QUANTITY_CONFLICTS_WITH_TYPED_FACT"
	}
	positive, negative := marketQuestionPositiveRelation.MatchString(text), marketQuestionNegativeRelation.MatchString(text)
	switch relation {
	case "INCREASE":
		if negative && !positive {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TEXT_REVERSES_INCREASE"
		}
		if !positive || negative {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_INCREASE_IS_AMBIGUOUS"
		}
	case "DECREASE":
		if positive && !negative {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TEXT_REVERSES_DECREASE"
		}
		if !negative || positive {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_DECREASE_IS_AMBIGUOUS"
		}
	case "UNCHANGED":
		if positive || negative {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TEXT_REVERSES_UNCHANGED"
		}
		if !marketQuestionFlatRelation.MatchString(text) {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_UNCHANGED_IS_AMBIGUOUS"
		}
	case "LEVEL":
		if marketQuestionClaimHasQuantityUnit(text, fact.Value.Unit) && !marketQuestionClaimContainsTypedValue(text, fact.Value) {
			return marketanalysis.MarketQuestionValidationConflict, "LEVEL_VALUE_CONFLICTS_WITH_TYPED_FACT"
		}
		if !marketQuestionClaimHasQuantityUnit(text, fact.Value.Unit) && !boundedDefinition {
			return marketanalysis.MarketQuestionValidationUndetermined, "LEVEL_VALUE_OR_DEFINITION_IS_NOT_EXPLICIT"
		}
	case "CONTRIBUTION":
		if !marketQuestionContribution.MatchString(text) {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_CONTRIBUTION_IS_AMBIGUOUS"
		}
	case "UNAVAILABLE":
		if !marketQuestionUnavailable.MatchString(text) {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_UNAVAILABLE_IS_AMBIGUOUS"
		}
	}
	if !boundedDefinition && !marketQuestionClaimIsClosedFactStatement(text, relation) {
		return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_HAS_UNVERIFIED_SEMANTICS"
	}
	return marketanalysis.MarketQuestionValidationSupported, ""
}

func marketQuestionClaimIsPureCausalLimit(text string) bool {
	text = marketQuestionNormalizeClaimText(text)
	return !marketQuestionNumericToken.MatchString(text) && !marketQuestionCausalAssertion.MatchString(text) &&
		marketQuestionPureCausalLimit.MatchString(text)
}

func marketQuestionClaimIsClosedFactStatement(text, relation string) bool {
	text = marketQuestionNormalizeClaimText(text)
	switch relation {
	case "LEVEL":
		return marketQuestionClosedLevelClaim.MatchString(text)
	case "INCREASE", "DECREASE", "UNCHANGED":
		return marketQuestionClosedChangeClaim.MatchString(text)
	case "CONTRIBUTION":
		return marketQuestionClosedContributionClaim.MatchString(text)
	default:
		// Contribution and unavailability prose needs a separate typed grammar.
		// Until then, retain the quantitative result and withhold the prose.
		return false
	}
}

func marketQuestionNormalizeClaimText(text string) string {
	text = strings.ToLower(strings.NewReplacer("**", "", "__", "", "`", "").Replace(strings.TrimSpace(text)))
	text = strings.TrimSpace(strings.TrimRight(text, ".!?"))
	return strings.Join(strings.Fields(text), " ")
}

func marketQuestionClaimIsBoundedShareDefinition(text string, fact marketquestion.EvidenceFact, bundle marketquestion.EvidenceBundleSnapshot) bool {
	if marketQuestionClaimHasQuantityUnit(text, fact.Value.Unit) || fact.Relation == nil ||
		fact.Relation.Operation != marketquestion.AggregationRecomputeRatio || len(fact.DenominatorIDs) == 0 {
		return false
	}
	lower := strings.ToLower(text)
	metric := strings.ToUpper(strings.TrimSpace(fact.Metric))
	if (!strings.Contains(metric, "SHARE") && !strings.Contains(metric, "CAPTURE")) ||
		(!strings.Contains(lower, "점유율") && !strings.Contains(lower, "비중") && !strings.Contains(lower, "포착률")) {
		return false
	}
	parts := strings.Split(lower, ",")
	if len(parts) != 2 || !marketQuestionShareDefinition.MatchString(strings.TrimSpace(parts[0])) ||
		!marketQuestionBasisExclusion.MatchString(strings.TrimSpace(parts[1])) ||
		!strings.EqualFold(bundle.Scope.ActualMarketScope, "KRW") || !marketQuestionScopeHasFullDomesticMembership(bundle.Scope) {
		return false
	}
	mentioned := marketQuestionMentionedExchanges(parts[0])
	exchange := strings.ToLower(strings.TrimSpace(fact.Subject.Exchange))
	if len(mentioned) != 1 || !mentioned[exchange] {
		return false
	}
	knownDenominators := make(map[string]struct{}, len(bundle.Denominators))
	for _, denominator := range bundle.Denominators {
		knownDenominators[denominator.ID] = struct{}{}
	}
	for _, id := range fact.DenominatorIDs {
		if _, ok := knownDenominators[id]; !ok {
			return false
		}
	}
	officialSource := false
	for _, source := range bundle.Sources {
		if source.ID == fact.SourceID && strings.Contains(strings.ToUpper(source.EvidenceLayer), "OFFICIAL") {
			officialSource = true
			break
		}
	}
	if !officialSource {
		return false
	}
	return true
}

func marketQuestionEvidenceFactRelation(fact marketquestion.EvidenceFact) string {
	if fact.Role == "CONTEXT" {
		return "LEVEL"
	}
	if fact.Value.State == marketquestion.QuantityUnknown || fact.Value.State == marketquestion.QuantityNotApplicable {
		return "UNAVAILABLE"
	}
	metric := strings.ToUpper(strings.TrimSpace(fact.Metric))
	if strings.Contains(metric, "ATTRIBUTION") || strings.Contains(metric, "CONTRIBUTION") {
		return "CONTRIBUTION"
	}
	temporalChange := strings.EqualFold(strings.TrimSpace(fact.Role), "CHANGE") &&
		((fact.Relation != nil && fact.Relation.Operation == marketquestion.AggregationEndpointDifference) ||
			strings.EqualFold(strings.TrimSpace(fact.Time.Role), "COMPARISON"))
	if temporalChange && (strings.Contains(metric, "_DELTA") || strings.HasSuffix(metric, "_CHANGE") || strings.Contains(metric, "_CHANGE_")) {
		value, ok := new(big.Rat).SetString(strings.TrimSpace(fact.Value.Decimal))
		if !ok {
			return ""
		}
		switch value.Sign() {
		case 1:
			return "INCREASE"
		case -1:
			return "DECREASE"
		default:
			return "UNCHANGED"
		}
	}
	return "LEVEL"
}

func marketQuestionBlockDeniesCausalityLocally(block marketanalysis.MarketQuestionClaimBlock) bool {
	// A positive causal assertion is not cured by a disclaimer elsewhere in
	// the same claim or paragraph. Pure causal-limit statements contain none of
	// these positive cues and are checked by the COINCIDENCE_ONLY grammar.
	return !marketQuestionCausalAssertion.MatchString(block.Text)
}

func marketQuestionClaimBlockMatchesParagraph(paragraph, blockText string) bool {
	paragraph, blockText = strings.TrimSpace(paragraph), strings.TrimSpace(blockText)
	if paragraph == blockText {
		return true
	}
	lines := strings.Split(strings.ReplaceAll(paragraph, "\r\n", "\n"), "\n")
	for len(lines) > 0 && marketQuestionSafeNonAssertiveHeading(lines[0]) {
		lines = lines[1:]
	}
	return len(lines) > 0 && strings.TrimSpace(strings.Join(lines, "\n")) == blockText
}

func marketQuestionSafeNonAssertiveHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}
	trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	if trimmed == "" || marketQuestionNumericToken.MatchString(trimmed) || len(marketQuestionMentionedExchanges(strings.ToLower(trimmed))) != 0 ||
		marketQuestionPositiveRelation.MatchString(trimmed) || marketQuestionNegativeRelation.MatchString(trimmed) ||
		marketQuestionFlatRelation.MatchString(trimmed) || marketQuestionContribution.MatchString(trimmed) ||
		marketQuestionUnavailable.MatchString(trimmed) || marketQuestionCausalAssertion.MatchString(trimmed) ||
		marketQuestionClaimNegation.MatchString(trimmed) || marketQuestionHeadingAssertion.MatchString(trimmed) {
		return false
	}
	return marketQuestionHeadingNoun.MatchString(trimmed)
}

func marketQuestionClaimCoverageStatus(block marketanalysis.MarketQuestionClaimBlock, bundle marketquestion.EvidenceBundleSnapshot) (marketanalysis.MarketQuestionValidationStatus, string) {
	remainder := block.Text
	for _, claim := range block.Claims {
		text := strings.TrimSpace(claim.Text)
		if text == "" {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_TEXT_IS_EMPTY"
		}
		at := strings.Index(remainder, text)
		if at < 0 {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TEXT_IS_NOT_IN_BLOCK"
		}
		remainder = remainder[:at] + strings.Repeat(" ", len(text)) + remainder[at+len(text):]
	}
	if !marketQuestionUncoveredContextSupported(remainder, block, bundle) {
		return marketanalysis.MarketQuestionValidationUndetermined, "BLOCK_HAS_UNCOVERED_FINANCIAL_PROSE"
	}
	return marketanalysis.MarketQuestionValidationSupported, ""
}

func marketQuestionUncoveredContextSupported(remainder string, block marketanalysis.MarketQuestionClaimBlock, bundle marketquestion.EvidenceBundleSnapshot) bool {
	lower := strings.ToLower(strings.NewReplacer("**", " ", "__", " ", "`", " ").Replace(remainder))
	referenced := make(map[string]marketquestion.EvidenceFact, len(block.Claims))
	allFacts := make(map[string]marketquestion.EvidenceFact, len(bundle.Facts))
	for _, fact := range bundle.Facts {
		allFacts[fact.ID] = fact
	}
	for _, claim := range block.Claims {
		if fact, ok := allFacts[claim.FactID]; ok {
			referenced[fact.ID] = fact
		}
	}
	if len(referenced) == 0 {
		return false
	}
	for _, segment := range strings.FieldsFunc(lower, func(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '\n' }) {
		segment = marketQuestionNormalizeUncoveredSegment(segment)
		if segment == "" {
			continue
		}
		if marketQuestionSourcePreamble.MatchString(segment) {
			if !marketQuestionUncoveredPreambleMatchesBundle(referenced, bundle) {
				return false
			}
			continue
		}
		if marketQuestionDenomBridge.MatchString(segment) {
			if !marketQuestionUncoveredDenomBridgeMatchesFacts(segment, referenced) {
				return false
			}
			continue
		}
		if marketQuestionFinancialProse.MatchString(segment) || strings.TrimSpace(segment) != "" {
			return false
		}
	}
	return true
}

func marketQuestionNormalizeUncoveredSegment(value string) string {
	value = strings.TrimSpace(strings.Trim(value, ",;: "))
	for _, suffix := range []string{"입니다", "이다", "이며", "이고"} {
		value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
		value = strings.TrimSpace(strings.Trim(value, ",;: "))
	}
	return strings.Join(strings.Fields(value), " ")
}

func marketQuestionUncoveredPreambleMatchesBundle(facts map[string]marketquestion.EvidenceFact, bundle marketquestion.EvidenceBundleSnapshot) bool {
	if bundle.Time.Basis != marketquestion.TimeBasisRolling24hSnapshot ||
		bundle.Time.AggregationSemantics != marketquestion.TimeAggregationSnapshotLevel || bundle.Scope.ActualMarketScope != "KRW" {
		return false
	}
	for _, fact := range facts {
		for _, source := range bundle.Sources {
			if source.ID == fact.SourceID && strings.Contains(strings.ToUpper(source.EvidenceLayer), "OFFICIAL") {
				return true
			}
		}
	}
	return false
}

func marketQuestionUncoveredDenomBridgeMatchesFacts(text string, facts map[string]marketquestion.EvidenceFact) bool {
	mentioned := marketQuestionMentionedExchanges(text)
	if len(mentioned) != 1 {
		return false
	}
	for _, fact := range facts {
		metric := strings.ToUpper(fact.Metric)
		if (strings.Contains(metric, "SHARE") || strings.Contains(metric, "CAPTURE")) && mentioned[strings.ToLower(fact.Subject.Exchange)] && len(fact.DenominatorIDs) > 0 {
			return true
		}
	}
	return false
}

func marketQuestionClaimFactSemantics(
	text string,
	blockText string,
	fact marketquestion.EvidenceFact,
	bundle marketquestion.EvidenceBundleSnapshot,
	relation string,
	boundedDefinition bool,
) (marketanalysis.MarketQuestionValidationStatus, string) {
	lower := strings.ToLower(text)
	contextText := strings.ToLower(strings.TrimSpace(blockText))
	if contextText == "" {
		contextText = lower
	}
	exchange := strings.ToLower(strings.TrimSpace(fact.Subject.Exchange))
	if exchange != "" {
		mentioned := marketQuestionMentionedExchanges(lower)
		if len(mentioned) > 1 {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_HAS_MULTIPLE_EXCHANGE_SUBJECTS"
		}
		if exchange == "domestic" {
			if !strings.Contains(lower, "국내") && !strings.Contains(lower, "전체 시장") {
				return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_DOMESTIC_SUBJECT_IS_IMPLICIT"
			}
		} else if len(mentioned) == 0 {
			contextMentioned := marketQuestionMentionedExchanges(contextText)
			if len(contextMentioned) != 1 || !contextMentioned[exchange] {
				return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_EXCHANGE_SUBJECT_IS_IMPLICIT"
			}
		} else if !mentioned[exchange] {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_EXCHANGE_SUBJECT_CONFLICT"
		}
	}
	asset := strings.ToUpper(strings.TrimSpace(fact.Subject.Asset))
	if asset != "" && !strings.Contains(strings.ToUpper(text), asset) {
		return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_ASSET_SUBJECT_IS_IMPLICIT"
	}
	metric := strings.ToUpper(strings.TrimSpace(fact.Metric))
	hasShareWords := strings.Contains(lower, "점유율") || strings.Contains(lower, "비중") || strings.Contains(lower, "포착률")
	hasVolumeWords := strings.Contains(lower, "거래대금") || strings.Contains(lower, "거래금액")
	hasCountWords := strings.Contains(lower, "체결") || strings.Contains(lower, "건수")
	metricFamilies := 0
	for _, present := range []bool{hasShareWords, hasVolumeWords, hasCountWords} {
		if present {
			metricFamilies++
		}
	}
	if metricFamilies > 1 && !(boundedDefinition && hasShareWords && hasVolumeWords && !hasCountWords) {
		return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_HAS_MULTIPLE_METRIC_FAMILIES"
	}
	switch {
	case strings.Contains(metric, "SHARE") || strings.Contains(metric, "CAPTURE"):
		if !strings.Contains(lower, "점유율") && !strings.Contains(lower, "비중") && !strings.Contains(lower, "포착률") {
			if strings.Contains(lower, "거래대금") || strings.Contains(lower, "가격") || strings.Contains(lower, "체결") {
				return marketanalysis.MarketQuestionValidationConflict, "CLAIM_METRIC_CONFLICT"
			}
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_SHARE_METRIC_IS_IMPLICIT"
		}
	case strings.Contains(metric, "VOLUME"):
		if !strings.Contains(lower, "거래대금") && !strings.Contains(lower, "거래금액") {
			if strings.Contains(lower, "점유율") || strings.Contains(lower, "가격") || strings.Contains(lower, "체결 건수") {
				return marketanalysis.MarketQuestionValidationConflict, "CLAIM_METRIC_CONFLICT"
			}
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_VOLUME_METRIC_IS_IMPLICIT"
		}
	case strings.Contains(metric, "TRADE_COUNT"):
		if !strings.Contains(lower, "체결") && !strings.Contains(lower, "건수") {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_COUNT_METRIC_IS_IMPLICIT"
		}
	case strings.Contains(metric, "ATTRIBUTION") || strings.Contains(metric, "CONTRIBUTION"):
		if !marketQuestionContribution.MatchString(text) {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_CONTRIBUTION_METRIC_IS_IMPLICIT"
		}
	default:
		return marketanalysis.MarketQuestionValidationUndetermined, "FACT_METRIC_FAMILY_IS_NOT_SUPPORTED"
	}
	if status, reason := marketQuestionClaimTimeStatus(lower+" "+contextText, fact.Time, relation, bundle); status != marketanalysis.MarketQuestionValidationSupported {
		return status, reason
	}
	if bundle.Time.AggregationSemantics == marketquestion.TimeAggregationEndpointDifference &&
		(strings.Contains(lower, "신규 거래대금") || strings.Contains(lower, "동안 발생한 거래대금") || strings.Contains(lower, "해당 구간 거래대금")) {
		return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TREATS_ENDPOINT_DIFFERENCE_AS_INTERVAL_FLOW"
	}
	if status, reason := marketQuestionClaimDenominatorStatus(lower, fact, bundle); status != marketanalysis.MarketQuestionValidationSupported {
		if status == marketanalysis.MarketQuestionValidationConflict {
			return status, reason
		}
		if contextStatus, contextReason := marketQuestionClaimDenominatorStatus(contextText, fact, bundle); contextStatus != marketanalysis.MarketQuestionValidationSupported {
			return contextStatus, contextReason
		}
	}
	sourceFound := fact.SourceID == ""
	for _, source := range bundle.Sources {
		if source.ID != fact.SourceID {
			continue
		}
		sourceFound = true
		layer := strings.ToUpper(source.EvidenceLayer)
		claimsWS := strings.Contains(contextText, "websocket") || strings.Contains(contextText, "웹소켓") || strings.Contains(contextText, "직접 관측")
		claimsOfficial := strings.Contains(contextText, "공식")
		if claimsWS && !strings.Contains(layer, "WEBSOCKET") && !strings.Contains(layer, "DIRECT") {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_SOURCE_CONFLICT"
		}
		if claimsOfficial && strings.Contains(layer, "WEBSOCKET") {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_SOURCE_CONFLICT"
		}
	}
	if !sourceFound {
		return marketanalysis.MarketQuestionValidationUndetermined, "FACT_SOURCE_IDENTITY_IS_NOT_AVAILABLE"
	}
	return marketanalysis.MarketQuestionValidationSupported, ""
}

func marketQuestionMentionedExchanges(text string) map[string]bool {
	result := map[string]bool{}
	for name, exchange := range marketQuestionExchangeNames {
		if strings.Contains(text, name) {
			result[exchange] = true
		}
	}
	return result
}

func marketQuestionClaimTimeStatus(text string, value marketquestion.FactTimeReference, relation string, bundle marketquestion.EvidenceBundleSnapshot) (marketanalysis.MarketQuestionValidationStatus, string) {
	switch strings.ToUpper(strings.TrimSpace(value.Role)) {
	case "CURRENT":
		if strings.Contains(text, "직전") || strings.Contains(text, "이전") || strings.Contains(text, "시작") {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TIME_ROLE_CONFLICT"
		}
		if !strings.Contains(text, "현재") && !strings.Contains(text, "최근") && !strings.Contains(text, "끝") {
			compact := strings.Join(strings.Fields(strings.ToLower(text)), "")
			if bundle.Time.Basis != marketquestion.TimeBasisRolling24hSnapshot ||
				bundle.Time.AggregationSemantics != marketquestion.TimeAggregationSnapshotLevel ||
				!strings.Contains(compact, "제공된공식rolling24시간") {
				return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_CURRENT_TIME_IS_IMPLICIT"
			}
		}
	case "PREVIOUS":
		if strings.Contains(text, "현재") || strings.Contains(text, "최근") || strings.Contains(text, "끝") {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_TIME_ROLE_CONFLICT"
		}
		if !strings.Contains(text, "직전") && !strings.Contains(text, "이전") && !strings.Contains(text, "시작") {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_PREVIOUS_TIME_IS_IMPLICIT"
		}
	case "COMPARISON":
		if (relation == "INCREASE" || relation == "DECREASE" || relation == "UNCHANGED") &&
			!strings.Contains(text, "변화") && !strings.Contains(text, "비교") && !strings.Contains(text, "보다") && !strings.Contains(text, "→") && !strings.Contains(text, "늘") && !strings.Contains(text, "줄") && !strings.Contains(text, "상승") && !strings.Contains(text, "하락") && !strings.Contains(text, "증가") && !strings.Contains(text, "감소") {
			return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_COMPARISON_TIME_IS_IMPLICIT"
		}
	default:
		return marketanalysis.MarketQuestionValidationUndetermined, "FACT_TIME_ROLE_IS_NOT_SUPPORTED"
	}
	return marketanalysis.MarketQuestionValidationSupported, ""
}

func marketQuestionClaimDenominatorStatus(text string, fact marketquestion.EvidenceFact, bundle marketquestion.EvidenceBundleSnapshot) (marketanalysis.MarketQuestionValidationStatus, string) {
	if len(fact.DenominatorIDs) == 0 {
		return marketanalysis.MarketQuestionValidationSupported, ""
	}
	metric := strings.ToUpper(fact.Metric)
	if !strings.Contains(metric, "SHARE") && !strings.Contains(metric, "CAPTURE") {
		return marketanalysis.MarketQuestionValidationSupported, ""
	}
	denominators := make(map[string]struct{}, len(bundle.Denominators))
	for _, denominator := range bundle.Denominators {
		denominators[denominator.ID] = struct{}{}
	}
	for _, id := range fact.DenominatorIDs {
		if _, ok := denominators[id]; !ok {
			return marketanalysis.MarketQuestionValidationUndetermined, "FACT_DENOMINATOR_IDENTITY_IS_NOT_AVAILABLE"
		}
	}
	explicitScopedCount := false
	if match := marketQuestionExchangeCount.FindStringSubmatch(text); len(match) == 2 {
		claimed, err := strconv.Atoi(match[1])
		if err != nil || claimed != len(bundle.Scope.IncludedExchanges) {
			return marketanalysis.MarketQuestionValidationConflict, "CLAIM_DENOMINATOR_SCOPE_CONFLICT"
		}
		explicitScopedCount = true
	}
	claimsUniversalDomestic := strings.Contains(text, "국내 전체") || strings.Contains(text, "국내 시장 전체") || strings.Contains(text, "전체 시장")
	if claimsUniversalDomestic && !marketQuestionScopeHasFullDomesticMembership(bundle.Scope) {
		return marketanalysis.MarketQuestionValidationConflict, "CLAIM_ENLARGES_PARTIAL_DENOMINATOR_SCOPE"
	}
	if !explicitScopedCount && !strings.Contains(text, "분모") && !strings.Contains(text, "국내") && !strings.Contains(text, "전체") && !strings.Contains(text, "동일 코인") {
		return marketanalysis.MarketQuestionValidationUndetermined, "CLAIM_DENOMINATOR_IS_IMPLICIT"
	}
	return marketanalysis.MarketQuestionValidationSupported, ""
}

func marketQuestionScopeHasFullDomesticMembership(scope marketquestion.ScopeIdentity) bool {
	if scope.Status != marketquestion.ScopeExact || len(scope.ExcludedExchanges) != 0 ||
		len(scope.IncludedExchanges) != len(marketQuestionDomesticExchanges) {
		return false
	}
	included := make(map[string]struct{}, len(scope.IncludedExchanges))
	for _, exchange := range scope.IncludedExchanges {
		included[strings.ToLower(strings.TrimSpace(exchange))] = struct{}{}
	}
	if len(included) != len(marketQuestionDomesticExchanges) {
		return false
	}
	for _, exchange := range marketQuestionDomesticExchanges {
		if _, ok := included[exchange]; !ok {
			return false
		}
	}
	return true
}

func marketQuestionClaimContainsTypedValue(text string, quantity marketquestion.Quantity) bool {
	if quantity.State != marketquestion.QuantityConfirmedValue && quantity.State != marketquestion.QuantityConfirmedZero {
		return false
	}
	expectedRat, exactOK := new(big.Rat).SetString(strings.TrimSpace(quantity.Decimal))
	expected, err := strconv.ParseFloat(quantity.Decimal, 64)
	if err != nil {
		if !exactOK {
			return false
		}
	}
	for _, location := range marketQuestionNumericToken.FindAllStringIndex(text, -1) {
		claimed, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimPrefix(text[location[0]:location[1]], "+"), ",", ""), 64)
		if err != nil {
			continue
		}
		unit := marketQuestionNumericUnit(text[location[1]:])
		scale := 1.0
		switch strings.ToUpper(quantity.Unit) {
		case "KRW":
			switch unit {
			case "KRW_TRILLION":
				scale = 1e12
			case "KRW_HUNDRED_MILLION":
				scale = 1e8
			case "KRW_TEN_THOUSAND":
				scale = 1e4
			case "KRW":
			default:
				continue
			}
		case "PCT":
			if unit != "PCT" {
				continue
			}
		case "PP":
			if unit != "PP" {
				continue
			}
		case "COUNT":
			if unit != "COUNT" {
				continue
			}
		default:
			continue
		}
		claimed *= scale
		if exactOK && expected == 0 && expectedRat.Sign() != 0 {
			claimedRat, ok := new(big.Rat).SetString(strings.ReplaceAll(strings.TrimPrefix(text[location[0]:location[1]], "+"), ",", ""))
			if ok {
				claimedRat.Mul(claimedRat, new(big.Rat).SetInt64(int64(scale)))
				if claimedRat.Cmp(expectedRat) == 0 {
					return true
				}
			}
			continue
		}
		tolerance := math.Max(math.Abs(expected)*1e-9, scale*0.005000001)
		// Currency display values may use whole eok/won or one decimal place.
		// Match the stated precision, while never accepting a reversed sign.
		if strings.EqualFold(quantity.Unit, "KRW") && scale > 1 && claimed*expected > 0 {
			token := strings.ReplaceAll(text[location[0]:location[1]], ",", "")
			if !strings.ContainsAny(token, "eE") {
				places := 0
				if dot := strings.IndexByte(token, '.'); dot >= 0 {
					places = len(token) - dot - 1
				}
				tolerance = math.Max(math.Abs(expected)*1e-9, scale*0.500000001*math.Pow10(-places))
			}
		}
		if math.Abs(claimed-expected) <= tolerance {
			return true
		}
	}
	return false
}

func marketQuestionClaimHasQuantityUnit(text, expectedUnit string) bool {
	expectedUnit = strings.ToUpper(strings.TrimSpace(expectedUnit))
	for _, location := range marketQuestionNumericToken.FindAllStringIndex(text, -1) {
		unit := marketQuestionNumericUnit(text[location[1]:])
		switch expectedUnit {
		case "KRW":
			if strings.HasPrefix(unit, "KRW") {
				return true
			}
		case "PCT", "PP", "COUNT":
			if unit == expectedUnit {
				return true
			}
		}
	}
	return false
}

func marketQuestionExplanationParagraphs(text string) []string {
	values := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func marketQuestionMergeValidationStatus(current, next marketanalysis.MarketQuestionValidationStatus) marketanalysis.MarketQuestionValidationStatus {
	if current == marketanalysis.MarketQuestionValidationConflict || next == marketanalysis.MarketQuestionValidationConflict {
		return marketanalysis.MarketQuestionValidationConflict
	}
	if current == marketanalysis.MarketQuestionValidationUndetermined || next == marketanalysis.MarketQuestionValidationUndetermined {
		return marketanalysis.MarketQuestionValidationUndetermined
	}
	return marketanalysis.MarketQuestionValidationSupported
}

func marketQuestionRetainedExplanationCoversRequestedParts(text string, query marketanalysis.MarketQuestionQuery) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, requested := range query.RequestedExplanation {
		var markers []string
		switch strings.ToUpper(strings.TrimSpace(requested)) {
		case "DRIVERS", "EXCHANGES":
			markers = []string{"기여", "완화", "압력", "거래소별"}
		case "PATH":
			markers = []string{"경로", "추이", "고점", "저점", "지속", "구간"}
		case "PRICE":
			markers = []string{"가격"}
		case "REALTIME":
			markers = []string{"실시간", "직접 관측", "websocket", "웹소켓"}
		case "ORDERBOOK":
			markers = []string{"호가", "슬리피지"}
		case "NOTICES":
			markers = []string{"공지"}
		case "QUALITY":
			markers = []string{"자료", "근거", "공식", "직접 관측", "범위", "누락", "완전", "확인"}
		default:
			return false
		}
		covered := false
		for _, marker := range markers {
			if strings.Contains(text, marker) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}
