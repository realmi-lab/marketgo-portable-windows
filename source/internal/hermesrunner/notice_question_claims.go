package hermesrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// These are direct source passages, not a model's summary or schedule labels.
// Background domestic summaries can use this same bounded claim boundary.
type noticeQuestionClaimSource struct {
	SourceID string
	Title    string
	Exchange string
	Assets   []string
	Passages []string
	// Original body only; excludes flattened snippets and reporting metadata.
	OriginalBodyPassages []string
	// Server excerpt status is a document fact, never a service restriction.
	BodyTruncated *bool
	BodyScope     string
}

func noticeQuestionEvidenceAvailable(persona string, payload []byte) error {
	if strings.EqualFold(strings.TrimSpace(persona), "BITHUMB_STYLE_WRITER") {
		return nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return err
	}
	var notices []json.RawMessage
	_ = json.Unmarshal(root["notices"], &notices)
	if len(notices) > 0 {
		return nil
	}
	// Even a server volume series cannot establish an exchange's notice action.
	// The existing scoped fallback can explain zero candidates and table values
	// without asking an ungrounded model to invent prose about a service state.
	return errors.New("notice question has no retrieved official candidates; use the scoped server fallback")
}

// Preserve server metadata outside notice rows. Within a row only official
// title/body excerpts, sourced OCR and timestamps may license factual values.
// A summary, extracted schedule or action label never enlarges that set.
func noticeQuestionOfficialRow(raw json.RawMessage) json.RawMessage {
	var row map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil {
		return raw
	}
	result := map[string]json.RawMessage{}
	for _, key := range []string{"source_id", "exchange", "assets", "title", "body", "body_text", "original_body_text", "body_passages", "body_truncated", "body_scope", "published_at_kst", "updated_at_kst"} {
		if value, ok := row[key]; ok {
			result[key] = value
		}
	}
	var snippets []struct {
		Source string `json:"source"`
		Text   string `json:"text"`
	}
	_ = json.Unmarshal(row["matched_evidence"], &snippets)
	trusted := []map[string]string{}
	for _, snippet := range snippets {
		switch strings.ToUpper(strings.TrimSpace(snippet.Source)) {
		case "BODY", "IMAGE", "TITLE":
			trusted = append(trusted, map[string]string{"source": snippet.Source, "text": snippet.Text})
		}
	}
	if len(trusted) > 0 {
		result["matched_evidence"], _ = json.Marshal(trusted)
	}
	encoded, _ := json.Marshal(result)
	return encoded
}

func noticeQuestionClaimSources(payload []byte) []noticeQuestionClaimSource {
	var root struct {
		Notices []json.RawMessage `json:"notices"`
	}
	_ = json.Unmarshal(payload, &root)
	sources := make([]noticeQuestionClaimSource, 0, len(root.Notices))
	for _, raw := range root.Notices {
		var row struct {
			SourceID  string   `json:"source_id"`
			Title     string   `json:"title"`
			Exchange  string   `json:"exchange"`
			Assets    []string `json:"assets"`
			Passages  []string `json:"body_passages"`
			Body      string   `json:"body"`
			BodyText  string   `json:"body_text"`
			Original  string   `json:"original_body_text"`
			Published string   `json:"published_at_kst"`
			Updated   string   `json:"updated_at_kst"`
			Truncated *bool    `json:"body_truncated"`
			Scope     string   `json:"body_scope"`
			Snippets  []struct {
				Source string `json:"source"`
				Text   string `json:"text"`
			} `json:"matched_evidence"`
		}
		if json.Unmarshal(noticeQuestionOfficialRow(raw), &row) != nil {
			continue
		}
		parts := append([]string{}, row.Passages...)
		parts = append(parts, row.Body, row.BodyText, row.Original)
		for _, snippet := range row.Snippets {
			parts = append(parts, snippet.Text)
		}
		if row.Published != "" {
			parts = append(parts, "게시 "+row.Published)
		}
		if row.Updated != "" {
			parts = append(parts, "수정 "+row.Updated)
		}
		scope := "UNKNOWN"
		switch row.Scope {
		case "FULL_STORED_ORIGINAL", "EXCERPT_ONLY":
			scope = row.Scope
		}
		if scope == "FULL_STORED_ORIGINAL" && row.Truncated != nil && *row.Truncated {
			scope = "EXCERPT_ONLY"
		}
		sources = append(sources, noticeQuestionClaimSource{SourceID: row.SourceID, Title: row.Title, Exchange: row.Exchange, Assets: row.Assets, Passages: parts, OriginalBodyPassages: append([]string(nil), row.Passages...), BodyTruncated: row.Truncated, BodyScope: scope})
	}
	// Each labelled server volume row is its own source. A value from another
	// asset/day cannot be borrowed to explain this row's turnover.
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var table map[string]any
	if decoder.Decode(&table) == nil && expandNoticeQuantityVolumeTable(table) == nil {
		if days, ok := table["daily_asset_volumes"].([]any); ok {
			for _, rawDay := range days {
				day, ok := rawDay.(map[string]any)
				if !ok {
					continue
				}
				assets, _ := day["assets"].([]any)
				for _, rawAsset := range assets {
					row, ok := rawAsset.(map[string]any)
					if !ok {
						continue
					}
					asset, _ := row["asset"].(string)
					date, _ := day["day"].(string)
					volume, ok := row["volume_krw"].(json.Number)
					if !ok || asset == "" || date == "" {
						continue
					}
					exchange := ""
					if exchanges, ok := row["exchanges"].([]any); ok && len(exchanges) == 1 {
						exchange, _ = exchanges[0].(string)
					}
					text := fmt.Sprintf("%s %s 거래대금 %s원", asset, date, volume.String())
					sources = append(sources, noticeQuestionClaimSource{SourceID: "server-volume:" + asset + ":" + date, Title: asset + " 거래대금", Exchange: exchange, Assets: []string{asset}, Passages: []string{text}})
				}
			}
		}
	}
	return sources
}

var (
	noticeClaimTicker         = regexp.MustCompile(`[A-Z][A-Z0-9]{0,14}`)
	noticeClaimClauseBoundary = regexp.MustCompile(`(?:[.!?][\t ]+|[\r\n;；]+|,[\t ]+|이며|이고)`)
	noticeClaimTopicPattern   = regexp.MustCompile(`(?i:API[\t ]*거래(?:지원)?)|기술(?:적)?[\t ]*지원|입출금|거래대금|거래유의|거래(?:지원|서비스)?|매매|출금|출고|입금|입고|주문|월렛|지갑주소|업그레이드|지급|분배|배분|보상|참여|신청|응모|유의종목|게시|등록|수정|업데이트`)
	noticeClaimTopic          = noticeQuestionTopicMatcher{}
)

type noticeQuestionClaim struct {
	text           string
	topic          string
	action         string
	values         map[string]bool
	negative       bool
	mode           string
	role           string
	scope          string
	reported       bool
	origin         int
	conditions     []noticeQuestionEventCondition
	documentAbsent bool
}

func noticeQuestionClaimFacets(text string) (string, string) {
	topic := noticeClaimTopic.FindString(text)
	if action := noticeClaimAction.FindStringIndex(text); len(action) > 0 {
		for _, location := range noticeClaimTopic.FindAllStringIndex(text[:action[0]], -1) {
			topic = text[location[0]:location[1]]
		}
	}
	// The nearest topic before a value owns it: "참여 보상 10 USDT" is
	// payment, while "참여 조건 10 USDT" is an application requirement.
	if value := noticeQuestionNumericToken.FindStringIndex(text); len(value) > 0 && !noticeClaimAction.MatchString(text) {
		for _, location := range noticeClaimTopic.FindAllStringIndex(text[:value[0]], -1) {
			topic = text[location[0]:location[1]]
		}
	}
	if strings.HasPrefix(strings.ToUpper(topic), "API") {
		topic = "API_TRADE"
	}
	if strings.HasPrefix(topic, "기술") {
		topic = "TECH_SUPPORT"
	}
	switch topic {
	case "입출금":
		topic = "TRANSFER"
	case "거래대금":
		topic = "VOLUME"
	case "거래", "거래지원", "거래서비스", "매매":
		topic = "TRADE"
	case "출금", "출고":
		topic = "WITHDRAW"
	case "입금", "입고":
		topic = "DEPOSIT"
	case "주문":
		topic = "ORDER"
	case "월렛":
		topic = "WALLET"
	case "지갑주소":
		topic = "ADDRESS"
	case "업그레이드":
		topic = "UPGRADE"
	case "지급", "분배", "배분", "보상":
		topic = "PAYMENT"
	case "참여", "신청", "응모":
		topic = "APPLICATION"
	case "거래유의", "유의종목":
		topic = "CAUTION"
	case "게시", "등록":
		topic = "PUBLISHED"
	case "수정":
		topic = "UPDATED"
	case "업데이트":
		// Software/network updates are service events, not document edits.
		prefix, _, _ := strings.Cut(text, "업데이트")
		if noticeQuestionUpdateHasDocumentSubject(prefix) {
			topic = "UPDATED"
		} else {
			topic = "UPGRADE"
		}
	}
	action := noticeQuestionAction(noticeClaimAction.FindString(text))
	if action == "RESTRICT" && noticeQuestionDocumentExtentSubject(text) {
		topic = "DOCUMENT_EXTENT"
	}
	// A title/update label describes how an action was reported, not the
	// service subject of that action. The reporting timestamp guard owns it.
	if (topic == "PUBLISHED" || topic == "UPDATED") && action != "" && strings.Contains(text, "제목") {
		topic = ""
	}
	return topic, action
}

func noticeQuestionUpdateHasDocumentSubject(prefix string) bool {
	document, service := -1, -1
	for _, word := range []string{"공지", "원문", "문서", "제목", "게시", "등록", "수정"} {
		document = max(document, strings.LastIndex(prefix, word))
	}
	for _, word := range []string{"네트워크", "메인넷", "프로토콜", "시스템", "소프트웨어"} {
		service = max(service, strings.LastIndex(prefix, word))
	}
	return document >= 0 && document > service
}

func noticeQuestionTextClaims(text, title string, identifiers map[string]bool, sources ...noticeQuestionClaimSource) []noticeQuestionClaim {
	text = noticeQuestionInlineLabelScopes(text, identifiers, sources)
	text = noticeQuestionCoordinatedDocumentClocks(text, identifiers)
	claims := []noticeQuestionClaim{}
	var previousRevision noticeQuestionClaim
	for _, sentence := range noticeQuestionClaimClauses(noticeQuestionMetadataClauses(noticeQuestionLocalScheduleHeadings(noticeQuestionReadableTableClaims(noticeQuestionEventLists(text))))) {
		// The condition/reason guards validate this document-level statement.
		// Its quoted proposition is not an asserted service event or negation.
		statement := noticeClaimStatement(sentence, sources...)
		if statement.Matrix != noticeClaimAsserted {
			continue
		}
		if statement.Target == noticeClaimTargetGuarantee && statement.Negated {
			claims = append(claims, noticeQuestionTextClaims(statement.Residual, title, identifiers, sources...)...)
			continue
		}
		if shared := noticeQuestionSharedTemporalClaims(sentence, identifiers); len(shared) > 0 {
			claims = append(claims, shared...)
			continue
		}
		if claim, ok := noticeQuestionTemporalFrame(sentence, identifiers); ok {
			claims = append(claims, claim)
			continue
		}
		actors := noticeQuestionLeadingActors(sentence, identifiers)
		// In "date: X가 아니라 Y", the date is asserted about Y. Remove only
		// the explicit negative topic phrase, never its dates/numbers or the
		// whole sentence. Every other positive clause is still validated.
		if contrast := strings.Index(sentence, "아니라"); contrast >= 0 {
			positions := noticeClaimTopic.FindAllStringIndex(sentence[:contrast], -1)
			if len(positions) > 0 {
				start := positions[len(positions)-1][0]
				negative := strings.TrimSpace(sentence[start:contrast])
				if (strings.HasSuffix(negative, "가") || strings.HasSuffix(negative, "이")) && !noticeQuestionNumericToken.MatchString(negative) {
					sentence = sentence[:start] + sentence[contrast+len("아니라"):]
				}
			}
		}
		// Independent topic clauses must not share an end date merely because
		// the original document omitted a comma between them.
		locations := noticeClaimTopic.FindAllStringIndex(sentence, -1)
		parts := []string{}
		start := 0
		for i := 1; i < len(locations); i++ {
			prefix := sentence[start:locations[i][0]]
			_, dates := noticeQuestionCalendarDates(prefix)
			if !noticeClaimCause.MatchString(prefix) && (len(dates) > 0 || len(noticeQuestionExplicitQuantities(prefix, identifiers)) > 0 || noticeClaimAction.MatchString(prefix)) {
				parts = append(parts, prefix)
				start = locations[i][0]
			}
		}
		parts = append(parts, sentence[start:])
		for _, part := range parts {
			for _, atom := range noticeQuestionActionAtoms(part) {
				part := atom
				if actors != "" && !noticeQuestionHasExplicitActor(part, identifiers) {
					part = actors + " " + part
				}
				_, values := noticeQuestionCalendarDates(part)
				for _, quantity := range noticeQuestionExplicitQuantities(part, identifiers) {
					// Notice counts are derived from the retrieved set, not an
					// individual source's statement. The server unit guard owns them.
					if !strings.HasSuffix(quantity, "|건") {
						values["QUANTITY:"+quantity] = true
					}
				}
				topic, action := noticeQuestionClaimFacets(part)
				if topic == "" && noticeQuestionExplicitRewardOffer(part, title) != "" {
					topic = "PAYMENT"
				}
				if topic == "UPGRADE" && (action == "STOP" || action == "RESUME") && noticeClaimCause.MatchString(part) {
					topic, _ = noticeQuestionClaimFacets(title)
				}
				if topic == "" {
					var inherited string
					topic, inherited = noticeQuestionClaimFacets(title)
					if action == "" {
						action = inherited
					}
				}
				if len(values) == 0 && (action == "" || topic == "" && !noticeQuestionHasExplicitActor(part, identifiers)) {
					continue
				}
				negative, mode := noticeQuestionActionState(part)
				// A trailing possibility governs coordinated alternatives, not a
				// preceding completed event in an unrelated sentence.
				if noticeClaimPossible.MatchString(sentence) && noticeClaimAlternative.MatchString(part) {
					mode = "POSSIBLE"
				}
				claim := noticeQuestionClaim{text: part, topic: topic, action: action, values: values, negative: negative, mode: mode, role: noticeQuestionRevisionRole(part), scope: noticeQuestionNetworkScope(part), reported: noticeQuestionReportedEvent(part)}
				if claim.role == "NEW" && topic == "" && previousRevision.role == "OLD" {
					claim.topic, claim.action = previousRevision.topic, previousRevision.action
					for name := range identifiers {
						if noticeQuestionHasAnchor(previousRevision.text, name) {
							claim.text = name + " " + claim.text
						}
					}
				}
				if noticeQuestionTransitionValues(part, values) {
					claim.role = "TRANSITION"
				}
				claims = append(claims, claim)
				previousRevision = claim
			}
		}
	}
	return claims
}

// A shared venue label does not make a following venue-specific sentence a
// claim about every venue in the label. Preserve the label as its own clause,
// inherit its asset, and let the explicit local venue own the dated action.
func noticeQuestionInlineLabelScopes(text string, identifiers map[string]bool, sources []noticeQuestionClaimSource) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		label, body, ok := strings.Cut(line, ":")
		if !ok || noticeQuestionNumericToken.MatchString(label) || len(noticeReasonMentionedExchanges(label, sources)) == 0 {
			continue
		}
		// Only the first sentence after the label may supply its local subject;
		// a later sentence cannot silently narrow an earlier assertion.
		clauses := noticeQuestionClaimClauses(body)
		if len(clauses) == 0 || len(noticeReasonMentionedExchanges(clauses[0], sources)) == 0 {
			continue
		}
		namedAsset := false
		inherited := []string{}
		for asset := range identifiers {
			namedAsset = namedAsset || noticeQuestionHasAnchor(clauses[0], asset)
			if noticeQuestionHasAnchor(label, asset) {
				inherited = append(inherited, asset)
			}
		}
		sort.Strings(inherited)
		if !namedAsset && len(inherited) > 0 {
			body = strings.Join(inherited, " ") + " " + body
		}
		lines[i] = label + "\n" + body
	}
	return strings.Join(lines, "\n")
}

func noticeQuestionClaimClauses(text string) []string {
	parts := []string{}
	cursor := 0
	for _, location := range noticeClaimClauseBoundary.FindAllStringIndex(text, -1) {
		start, end := location[0], location[1]
		// The terminal dot in a written date is part of its notation, not a
		// sentence boundary separating that date from its following action.
		if text[start] == '.' && start > 0 && text[start-1] >= '0' && text[start-1] <= '9' {
			continue
		}
		if text[start] == ',' {
			_, dates := noticeQuestionCalendarDates(text[cursor:start])
			next := strings.TrimLeft(text[end:], " \t")
			clock := noticeAnswerClock.FindStringIndex(next)
			if len(dates) > 0 && len(clock) > 0 && clock[0] == 0 {
				continue
			}
			if noticeQuestionEndsTemporalModifier(text[cursor:start]) {
				continue
			}
			prefixActions := noticeClaimAction.FindAllStringIndex(text[cursor:start], -1)
			if len(prefixActions) >= 2 && noticeClaimCoordinatedTail.MatchString(text[cursor:start]) {
				first := prefixActions[0]
				relation, _ := noticeQuestionTemporalRelation(text[cursor+first[1] : start])
				nextAction := noticeClaimAction.FindStringIndex(next)
				if relation != "" && len(nextAction) > 0 && noticeClaimTopic.MatchString(next[:nextAction[0]]) {
					if nextRelation, _ := noticeQuestionTemporalRelation(next[nextAction[1]:]); nextRelation == "" && noticeClaimFiniteActionTail.MatchString(next[nextAction[1]:]) {
						continue
					}
				}
			}
		}
		partEnd := start
		if text[start] == '?' {
			// Preserve interrogative punctuation for downstream assertion
			// admission; a following sentence must not turn a question into
			// an asserted equality just because the separator was stripped.
			partEnd++
		}
		parts = append(parts, text[cursor:partEnd])
		cursor = end
	}
	return append(parts, text[cursor:])
}

func noticeQuestionHasAnchor(text, anchor string) bool {
	upper, target := strings.ToUpper(text), strings.ToUpper(anchor)
	for offset := 0; offset < len(upper); {
		i := strings.Index(upper[offset:], target)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(target)
		leftOK := i == 0 || !(upper[i-1] >= 'A' && upper[i-1] <= 'Z' || upper[i-1] >= '0' && upper[i-1] <= '9')
		rightOK := end == len(upper) || !(upper[end] >= 'A' && upper[end] <= 'Z' || upper[end] >= '0' && upper[end] <= '9')
		if leftOK && rightOK {
			return true
		}
		offset = end
	}
	return false
}

// This checks explicit local relations, not full natural-language entailment.
// A named asset/exchange narrows the cited sources. A dated action must then
// occur with those same values in one official clause of a matching source.
// Implicit pronouns and unrecognised event language remain model-level limits.
func validateNoticeQuestionClaimText(answer string, sources []noticeQuestionClaimSource) error {
	return validateNoticeQuestionClaimBindings(answer, sources, false)
}

// FACT_BINDINGS checks explicit values and identities. Free-form semantic
// entailment is audited separately; the legacy lexical mode stays available
// for offline regression and noninteractive consumers.
func validateNoticeQuestionFactBindings(answer string, sources []noticeQuestionClaimSource) error {
	return validateNoticeQuestionClaimBindings(answer, sources, true)
}

func validateNoticeQuestionClaimBindings(answer string, sources []noticeQuestionClaimSource, factsOnly bool) error {
	if !factsOnly {
		if err := validateNoticeQuestionEvidenceLimits(answer, sources); err != nil {
			return err
		}
		if err := validateNoticeQuestionReasonStrength(answer, sources); err != nil {
			return err
		}
		if err := validateNoticeQuestionConditionClaims(answer, sources); err != nil {
			return err
		}
	}
	identifiers := map[string]bool{}
	exchangeAnchors := map[string]bool{}
	anchors := make([]map[string]bool, len(sources))
	for i, source := range sources {
		anchors[i] = map[string]bool{}
		for _, asset := range append(append([]string{}, source.Assets...), noticeClaimTicker.FindAllString(source.Title, -1)...) {
			if len(asset) == 0 || asset == "KST" || asset == "UTC" || asset == "GMT" {
				continue
			}
			anchors[i][strings.ToUpper(asset)] = true
			identifiers[strings.ToUpper(asset)] = true
		}
		if source.Exchange != "" {
			anchors[i][strings.ToUpper(source.Exchange)] = true
			exchangeAnchors[strings.ToUpper(source.Exchange)] = true
			for _, exchange := range []struct{ id, name string }{{"upbit", "업비트"}, {"bithumb", "빗썸"}, {"coinone", "코인원"}, {"korbit", "코빗"}, {"gopax", "고팍스"}} {
				if strings.EqualFold(source.Exchange, exchange.id) {
					anchors[i][exchange.name] = true
					exchangeAnchors[exchange.name] = true
				}
			}
		}
	}
	allAnchors := map[string]bool{}
	for _, set := range anchors {
		for anchor := range set {
			allAnchors[anchor] = true
		}
	}
	officialClaims := make([][]noticeQuestionClaim, len(sources))
	knownWords := map[string]bool{"KST": true, "UTC": true, "GMT": true, "API": true, "JSON": true}
	for identifier := range identifiers {
		knownWords[identifier] = true
	}
	for i, source := range sources {
		for origin, passage := range append([]string{source.Title}, source.Passages...) {
			for _, word := range noticeClaimTicker.FindAllString(passage, -1) {
				knownWords[word] = true
			}
			for _, quantity := range noticeQuestionExplicitQuantities(noticeQuestionGroundingText(passage), identifiers) {
				for _, word := range noticeClaimTicker.FindAllString(quantity, -1) {
					knownWords[word] = true
				}
			}
			parsed := noticeQuestionTextClaims(noticeQuestionGroundingText(passage), source.Title, identifiers, source)
			for j := range parsed {
				parsed[j].origin = origin
			}
			officialClaims[i] = append(officialClaims[i], parsed...)
		}
		for j := range officialClaims[i] {
			officialClaims[i][j].reported = false
		}
		officialClaims[i] = noticeQuestionSourceEventRelations(officialClaims[i], source)
		if source.BodyTruncated != nil {
			officialClaims[i] = append(officialClaims[i], noticeQuestionClaim{topic: "DOCUMENT_EXTENT", action: "RESTRICT", negative: !*source.BodyTruncated})
		}
	}
	projection := noticeQuestionProjectEventLists(noticeQuestionAnswerGroundingText(answer), identifiers, sources...)
	if factsOnly {
		// Semantic lists without explicit values belong to the full audit.
		projection.Text, projection.Unresolved, projection.Items, projection.Denied = noticeQuestionAnswerGroundingText(answer), "", nil, nil
	}
	if projection.Unresolved != "" {
		return fmt.Errorf("notice answer semantic admission failed: %s", projection.Unresolved)
	}
	listClaims := []noticeQuestionClaim{}
	for _, group := range []struct {
		items  []string
		denied bool
	}{{projection.Items, false}, {projection.Denied, true}} {
		for _, item := range group.items {
			parsed := noticeQuestionTextClaims(item, "", identifiers, sources...)
			resolved := false
			for i := range parsed {
				if parsed[i].topic != "" && parsed[i].action != "" && parsed[i].mode != "CONDITIONAL" {
					resolved = true
				}
				parsed[i].documentAbsent = group.denied
			}
			if !resolved {
				return fmt.Errorf("notice answer semantic admission failed for a service list item: %s", item)
			}
			if group.denied {
				listClaims = append(listClaims, parsed...)
			}
		}
	}
	// Preserve every heading, delimiter and table scope before value binding.
	eventText, err := noticeQuestionTimeComparisons(projection.Text, sources, officialClaims, identifiers, knownWords, factsOnly)
	if err != nil {
		return err
	}
	if factsOnly {
		relationSources := make([]noticeQuestionEventRelationSource, 0, len(sources))
		for _, source := range sources {
			relationSources = append(relationSources, noticeQuestionEventRelationSource{SourceID: source.SourceID, Exchange: source.Exchange, Assets: source.Assets, Title: source.Title, BodyScope: source.BodyScope, BodyTruncated: source.BodyTruncated, Passages: source.OriginalBodyPassages})
		}
		eventText = noticeQuestionProjectReportedStopRelations(eventText, relationSources)
	}
	claims := append(noticeQuestionTextClaims(eventText, "", identifiers, sources...), listClaims...)
	for _, claim := range claims {
		for _, word := range noticeClaimTicker.FindAllString(claim.text, -1) {
			if len(word) > 1 && !knownWords[word] {
				return fmt.Errorf("notice answer names an unsupported event target: %s", word)
			}
		}
		if factsOnly && len(claim.values) == 0 {
			continue
		}
		mentionedAssets, mentionedExchanges := []string{}, []string{}
		for anchor := range allAnchors {
			if noticeQuestionHasAnchor(claim.text, anchor) {
				if exchangeAnchors[anchor] {
					mentionedExchanges = append(mentionedExchanges, anchor)
				} else {
					mentionedAssets = append(mentionedAssets, anchor)
				}
			}
		}
		// Untargeted quantities and reporting metadata retain their numeric
		// guard. An asserted service action is not metadata and still needs
		// event evidence even when the writer omits its subject.
		if len(mentionedAssets)+len(mentionedExchanges) == 0 && claim.topic == "" && (claim.action == "" || claim.reported) {
			continue
		}
		if len(mentionedAssets) == 0 {
			mentionedAssets = []string{""}
		}
		if len(mentionedExchanges) == 0 {
			mentionedExchanges = []string{""}
		}
		for _, asset := range mentionedAssets {
			for _, exchange := range mentionedExchanges {
				matched := false
				for i := range sources {
					if asset != "" && !anchors[i][asset] || exchange != "" && !anchors[i][exchange] {
						continue
					}
					for _, official := range officialClaims[i] {
						if !noticeQuestionRewardOfferAssetMatches(claim.text, official.text, sources[i].Title) {
							continue
						}
						if claim.topic != "" && claim.topic != official.topic && !(official.topic == "TRANSFER" && (claim.topic == "DEPOSIT" || claim.topic == "WITHDRAW")) {
							continue
						}
						if !claim.documentAbsent && !noticeQuestionClaimStateMatches(claim, official) {
							continue
						}
						if asset != "" && !noticeQuestionHasAnchor(official.text, asset) {
							namedOther := false
							for other := range identifiers {
								if other != asset && noticeQuestionHasAnchor(official.text, other) {
									namedOther = true
									break
								}
							}
							if namedOther {
								continue
							}
						}
						all := true
						for value := range claim.values {
							if !official.values[value] {
								all = false
								break
							}
						}
						if all {
							if claim.documentAbsent {
								positive := claim
								positive.mode = official.mode
								if noticeQuestionClaimStateMatches(positive, official) {
									return fmt.Errorf("notice answer denies an event stated by its source: %s", claim.text)
								}
								negative := claim
								negative.negative = !negative.negative
								if !noticeQuestionClaimStateMatches(negative, official) {
									continue
								}
							}
							matched = true
							if !claim.documentAbsent {
								break
							}
						}
					}
					if matched {
						break
					}
				}
				if !matched && len(sources) > 0 {
					return fmt.Errorf("notice answer event/asset and values are not linked in a cited official passage: %s", strings.TrimFunc(claim.text, unicode.IsSpace))
				}
			}
		}
	}
	return nil
}
