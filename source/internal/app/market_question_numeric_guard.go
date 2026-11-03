package app

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/tickertrend"
)

var marketQuestionNumericToken = regexp.MustCompile(`[+-]?(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?`)
var marketQuestionKoreanNumericClaim = regexp.MustCompile(`(?:[영공일이삼사오육칠팔구십백천만억조]+(?:점[영공일이삼사오육칠팔구십백천만억조]+)?|한|두|세|네|다섯|여섯|일곱|여덟|아홉|열|스무|서른|마흔|쉰|예순|일흔|여든|아흔)\s*(?:%p|%|퍼센트포인트|퍼센트|프로|조원|억원|만원|원|밀리초|시간|분|초|일|건|개|회)`)
var marketQuestionEnglishNumericClaim = regexp.MustCompile(`(?i)\b(?:zero|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|hundred|thousand|million|billion|trillion)(?:[- ]+(?:one|two|three|four|five|six|seven|eight|nine|ten|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|hundred|thousand|million|billion|trillion))*\s*(?:percent(?:age points?)?|hours?|minutes?|seconds?|milliseconds?|krw|won)\b`)
var marketQuestionTextualSignedNumber = regexp.MustCompile(`(?i)(?:마이너스|플러스|minus|plus)\s*[0-9]`)
var marketQuestionDirectValueDenial = regexp.MustCompile(`(?i)^\s*(?:%p|%포인트|%|퍼센트포인트|퍼센트|조원|억원|만원|원|krw|건|개|회)\s*[)）]?\s*(?:이|가|은|는)?\s*(?:아닙니다|아니다|아니라|아니며|아니고|아니었|아니에요|아니예요|아니죠|아니네요)`)
var marketQuestionDirectValueStrictlyBelow = regexp.MustCompile(`(?i)^\s*(?:%p|%포인트|%|퍼센트포인트|퍼센트|조원|억원|만원|원|krw|건|개|회)\s*[)）]?\s*에\s*(?:못\s*미(?:칩니다|친다|쳐요|쳤습니다)|미치지\s*못(?:합니다|한다|해요|했습니다))`)
var marketQuestionPrimaryRiseClaim = regexp.MustCompile(`(?:점유율|빗썸\s*비중)[^!?\n]{0,80}(?:상승|증가|올랐|올라|확대)`)
var marketQuestionPrimaryFallClaim = regexp.MustCompile(`(?:점유율|빗썸\s*비중)[^!?\n]{0,80}(?:하락|감소|내렸|내려|축소)`)
var marketQuestionUpperNamedTerm = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,11}\b`)
var marketQuestionLowercaseAnalyticalTerm = regexp.MustCompile(`\bmovement\b`)

// Lowercase ticker is a source noun only in these explicit official-data
// contexts. Do not grant a general TICKER symbol or remove coin/token claims.
var marketQuestionLowercaseTickerSource = regexp.MustCompile(`공식\s+ticker(?:\s+(?:자료|표본|집계값|데이터)(?:의|를|을|는|은|로|에서|입니다|이다|\s|[.,;:]|$)|(?:가|는)\s*(?:아닌|아니라)\s*(?:WebSocket|웹소켓))`)

var marketQuestionSystemNamedTerms = map[string]bool{
	"AI": true, "API": true, "BPS": true, "DB": true, "GPT": true, "HERMES": true,
	"HTTP": true, "JSON": true, "KRW": true, "KST": true, "LLM": true, "OHLC": true,
	"REST": true, "SQL": true, "UTC": true, "WS": true,
	"ZDR": true, "LIVE": true, "OPENROUTER": true, "PARTIAL": true, "CONFIRMED": true,
	"UNAVAILABLE": true, "WEBSOCKET": true, "COMPLETE": true, "NORMAL": true,
	"WATCH": true, "ALERT": true, "DEGRADED": true, "WARMING": true,
}

var marketQuestionKoreanAssetNames = map[string]string{
	"비트코인": "BTC", "이더리움": "ETH", "리플": "XRP",
	"솔라나": "SOL", "도지코인": "DOGE", "에이다": "ADA",
}

var marketQuestionExchangeNames = map[string]string{
	"upbit": "upbit", "업비트": "upbit", "bithumb": "bithumb", "빗썸": "bithumb",
	"coinone": "coinone", "코인원": "coinone", "korbit": "korbit", "코빗": "korbit",
	"gopax": "gopax", "고팍스": "gopax",
}

var marketQuestionUnsupportedExchangeNames = map[string]string{
	"binance": "binance", "바이낸스": "binance",
}

// marketQuestionAnswerNumbersAllowed prevents a prose provider from becoming
// a second calculator. Every Arabic numeric value in provider prose must also
// occur in the deterministic server briefing with the same unit class. Raw
// question numbers are intentionally excluded because they are untrusted user
// claims, not market evidence.
func marketQuestionAnswerNumbersAllowed(
	provider, deterministic marketanalysis.MarketQuestionAnswer,
) bool {
	return len(marketQuestionRejectedNumericClaims(provider, deterministic)) == 0
}

func marketQuestionRejectedNumericClaims(
	provider, deterministic marketanalysis.MarketQuestionAnswer,
) []string {
	allowed := map[string]map[string]bool{}
	source := marketQuestionAnswerText(deterministic)
	providerText := marketQuestionAnswerText(provider)
	if containsUnsupportedMarketQuestionNumericForm(providerText) {
		return []string{"unsupported numeric form"}
	}
	if marketQuestionDirectValueDenied(providerText) {
		return []string{"directly negated numeric value"}
	}
	var temporalOK bool
	providerText, source, temporalOK = marketQuestionValidatedTemporalText(providerText, source)
	if !temporalOK {
		return []string{"unsupported date or time"}
	}
	for _, location := range marketQuestionNumericToken.FindAllStringIndex(source, -1) {
		token := source[location[0]:location[1]]
		if normalized, ok := normalizeMarketQuestionNumericToken(token); ok {
			if allowed[normalized] == nil {
				allowed[normalized] = map[string]bool{}
			}
			allowed[normalized][marketQuestionNumericUnit(source[location[1]:])] = true
		}
	}
	rejected := []string{}
	for _, location := range marketQuestionNumericToken.FindAllStringIndex(providerText, -1) {
		token := providerText[location[0]:location[1]]
		normalized, ok := normalizeMarketQuestionNumericToken(token)
		if !ok || allowed[normalized] == nil {
			rejected = append(rejected, token+"/"+marketQuestionNumericUnit(providerText[location[1]:]))
			continue
		}
		unit := marketQuestionNumericUnit(providerText[location[1]:])
		if !allowed[normalized][unit] {
			rejected = append(rejected, token+"/"+unit)
		}
	}
	return rejected
}

// A verified display value must be asserted, not immediately denied or used as
// a strict upper bound. Scope this to the value's own unit and direct predicate:
// a preceding correction ("업비트가 아니라 빗썸 35%"), another subject's comparison
// before the value, or a qualification of certainty is not a denial of it.
// This does not parse arbitrary Korean negation, quotations or inference.
func marketQuestionDirectValueDenied(text string) bool {
	// Markdown emphasis may wrap just the value, including a single asterisk
	// or underscore. A closing parenthesis is handled at the unit boundary so
	// prose inside a following parenthetical is not treated as direct denial.
	text = strings.NewReplacer("*", "", "_", "", "`", "").Replace(text)
	for _, at := range marketQuestionNumericToken.FindAllStringIndex(text, -1) {
		if marketQuestionDirectValueDenial.MatchString(text[at[1]:]) ||
			marketQuestionDirectValueStrictlyBelow.MatchString(text[at[1]:]) {
			return true
		}
	}
	return false
}

// marketQuestionAnswerNamedTermsAllowed rejects named market claims that do
// not occur in the bounded server evidence. It intentionally covers the
// stable entity forms the product teaches the model to use: uppercase asset
// symbols, verified Korean aliases, and the five domestic exchanges.
func marketQuestionAnswerNamedTermsAllowed(answer marketanalysis.MarketQuestionAnswer, evidence []byte) bool {
	// User request metadata must never authorize an otherwise unknown symbol.
	var envelope map[string]json.RawMessage
	if json.Unmarshal(evidence, &envelope) == nil {
		stripParts := func(raw json.RawMessage) json.RawMessage {
			var query map[string]json.RawMessage
			if json.Unmarshal(raw, &query) == nil && query["question_parts"] != nil {
				delete(query, "question_parts")
				raw, _ = json.Marshal(query)
			}
			return raw
		}
		if raw, ok := envelope["query"]; ok {
			envelope["query"] = stripParts(raw)
		}
		var plan map[string]json.RawMessage
		if json.Unmarshal(envelope["retrieval_plan"], &plan) == nil && plan["validated_query"] != nil {
			plan["validated_query"] = stripParts(plan["validated_query"])
			envelope["retrieval_plan"], _ = json.Marshal(plan)
		}
		evidence, _ = json.Marshal(envelope)
	}
	text := marketQuestionAnswerText(answer)
	// A lower-case analytical noun is prose, not an asset ticker. Preserve the
	// upper-case spelling as a named term so an invented MOVEMENT symbol still
	// fails closed. Do not derive this allow-list from arbitrary JSON fields.
	namedText := marketQuestionLowercaseAnalyticalTerm.ReplaceAllString(text, "        ")
	namedText = marketQuestionLowercaseTickerSource.ReplaceAllStringFunc(namedText, func(source string) string {
		return strings.Replace(source, "ticker", "      ", 1)
	})
	evidenceUpper := strings.ToUpper(string(evidence))
	evidenceTerms := map[string]bool{}
	for _, term := range marketQuestionUpperNamedTerm.FindAllString(evidenceUpper, -1) {
		evidenceTerms[term] = true
	}
	for _, term := range marketQuestionUpperNamedTerm.FindAllString(strings.ToUpper(namedText), -1) {
		if marketQuestionSystemNamedTerms[term] {
			continue
		}
		if !evidenceTerms[term] {
			return false
		}
	}
	for name, symbol := range marketQuestionKoreanAssetNames {
		if strings.Contains(text, name) && !strings.Contains(evidenceUpper, `"`+symbol+`"`) {
			return false
		}
	}
	lowerText := strings.ToLower(text)
	lowerEvidence := strings.ToLower(string(evidence))
	for name, canonical := range marketQuestionExchangeNames {
		if strings.Contains(lowerText, name) && !strings.Contains(lowerEvidence, canonical) {
			return false
		}
	}
	for name, canonical := range marketQuestionUnsupportedExchangeNames {
		if strings.Contains(lowerText, name) && !strings.Contains(lowerEvidence, canonical) {
			return false
		}
	}
	return true
}

func containsUnsupportedMarketQuestionNumericForm(value string) bool {
	for _, current := range value {
		if unicode.IsDigit(current) && (current < '0' || current > '9') {
			return true
		}
		if strings.ContainsRune("−－＋", current) {
			return true
		}
	}
	return hasUnsupportedMarketQuestionKoreanNumericClaim(strings.ToLower(value)) ||
		marketQuestionEnglishNumericClaim.MatchString(value) ||
		marketQuestionTextualSignedNumber.MatchString(value)
}

func hasUnsupportedMarketQuestionKoreanNumericClaim(value string) bool {
	for _, location := range marketQuestionKoreanNumericClaim.FindAllStringIndex(value, -1) {
		// "가능한 시간" contains the byte substring "한 시간", but the
		// final syllable of an adjective is not a standalone quantity token.
		if location[0] > 0 {
			previous, _ := utf8.DecodeLastRuneInString(value[:location[0]])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) {
				continue
			}
		}
		match := value[location[0]:location[1]]
		// Joined "조건" is the ordinary noun condition, not 조 + 건.
		// Keep the spaced quantity "조 건" and longer numerals rejected.
		if match == "조건" {
			continue
		}
		switch strings.ReplaceAll(strings.TrimSpace(match), " ", "") {
		case "조원", "억원", "만원", "이시간", "이분", "이일", "일일", "만일", "구분", "조회", "사회", "만회":
			// These are Arabic-number currency suffixes such as 3.18조원.
			// The leading scale character is not a standalone Korean numeral.
			// "이 시간", "이 분", and "이 일일" are Korean demonstrative
			// phrases; a Korean quantity uses "두 시간" or an Arabic form.
			// Joined ordinary words such as 조회/구분/사회 are not quantities.
			continue
		default:
			if match == "백분" && (strings.HasPrefix(value[location[1]:], "율") || strings.HasPrefix(value[location[1]:], "위")) {
				continue
			}
			return true
		}
	}
	return false
}

// Treat dates and clocks as complete claims, never as an unordered bag of
// scalar digits. Display precision may decrease (milliseconds to seconds or
// minutes), but the date/time pair must remain one actual source observation.
var marketQuestionTemporalClaim = regexp.MustCompile(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}(?:[ T]+[0-9]{1,2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]{1,3})?)?)?\b|\b[0-9]{1,2}:[0-9]{2}(?::[0-9]{2}(?:\.[0-9]{1,3})?)?\b`)

func marketQuestionValidatedTemporalText(provider, source string) (string, string, bool) {
	clean := strings.NewReplacer("**", "", "__", "", "`", "")
	provider, source = clean.Replace(provider), clean.Replace(source)
	dates, clocks, pairs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, token := range marketQuestionTemporalClaim.FindAllString(source, -1) {
		date, clock, ok := marketQuestionTemporalParts(token)
		if !ok {
			continue
		}
		if date != "" {
			dates[date] = true
		}
		if clock != "" {
			clocks[clock] = true
		}
		if date != "" && clock != "" {
			pairs[date+" "+clock] = true
		}
	}
	compatible := func(value string, choices map[string]bool) bool {
		for choice := range choices {
			if choice == value || (strings.HasPrefix(choice, value) && len(choice) > len(value) && (choice[len(value)] == ':' || choice[len(value)] == '.')) {
				return true
			}
		}
		return false
	}
	for _, token := range marketQuestionTemporalClaim.FindAllString(provider, -1) {
		date, clock, ok := marketQuestionTemporalParts(token)
		if !ok {
			return provider, source, false
		}
		switch {
		case date != "" && clock != "":
			if !compatible(date+" "+clock, pairs) {
				return provider, source, false
			}
		case date != "":
			if !dates[date] {
				return provider, source, false
			}
		default:
			if !compatible(clock, clocks) {
				return provider, source, false
			}
		}
	}
	return marketQuestionTemporalClaim.ReplaceAllString(provider, " "), marketQuestionTemporalClaim.ReplaceAllString(source, " "), true
}

func marketQuestionTemporalParts(token string) (date, clock string, ok bool) {
	if len(token) >= 10 && token[4] == '-' && token[7] == '-' {
		date = token[:10]
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return "", "", false
		}
		token = strings.TrimSpace(strings.TrimPrefix(token[10:], "T"))
	}
	if token != "" {
		parts := strings.Split(token, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return "", "", false
		}
		hour, errH := strconv.Atoi(parts[0])
		minute, errM := strconv.Atoi(parts[1])
		if errH != nil || errM != nil || hour > 23 || minute > 59 {
			return "", "", false
		}
		if len(parts) == 3 {
			second, err := strconv.ParseFloat(parts[2], 64)
			if err != nil || second >= 60 {
				return "", "", false
			}
		}
		if len(parts[0]) == 1 {
			token = "0" + token
		}
		clock = token
	}
	return date, clock, date != "" || clock != ""
}

func marketQuestionNumericUnit(suffix string) string {
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	if strings.HasPrefix(suffix, "%p") || strings.HasPrefix(suffix, "%포인트") || strings.HasPrefix(suffix, "퍼센트포인트") {
		return "PP"
	}
	if strings.HasPrefix(suffix, "%") || strings.HasPrefix(suffix, "퍼센트") {
		return "PCT"
	}
	for _, unit := range []struct {
		prefix string
		class  string
	}{
		{"밀리초", "TIME_MS"}, {"ms", "TIME_MS"},
		{"시간", "TIME_HOUR"}, {"hour", "TIME_HOUR"},
		{"분", "TIME_MINUTE"}, {"minute", "TIME_MINUTE"},
		{"초", "TIME_SECOND"}, {"second", "TIME_SECOND"}, {"sec", "TIME_SECOND"},
		{"일", "TIME_DAY"}, {"day", "TIME_DAY"},
		{"조원", "KRW_TRILLION"}, {"억원", "KRW_HUNDRED_MILLION"},
		{"만원", "KRW_TEN_THOUSAND"}, {"원", "KRW"}, {"krw", "KRW"},
		{"건", "COUNT"}, {"개", "COUNT"}, {"회", "COUNT"},
	} {
		if strings.HasPrefix(suffix, unit.prefix) {
			return unit.class
		}
	}
	return ""
}

func marketQuestionAnswerSafetyAllowed(
	answer marketanalysis.MarketQuestionAnswer,
	query marketanalysis.MarketQuestionQuery,
	official tickertrend.WindowComparison,
	websocket marketanalysis.MarketQuestionIntervalEvidence,
	payload marketanalysis.Analysis,
) bool {
	return marketQuestionAnswerSafetyAllowedWithDaily(
		answer, query, official, tickertrend.DailyEndpointSeries{}, websocket, payload,
	)
}

func marketQuestionAnswerSafetyAllowedWithDaily(
	answer marketanalysis.MarketQuestionAnswer,
	query marketanalysis.MarketQuestionQuery,
	official tickertrend.WindowComparison,
	daily tickertrend.DailyEndpointSeries,
	websocket marketanalysis.MarketQuestionIntervalEvidence,
	payload marketanalysis.Analysis,
) bool {
	text := strings.ToLower(marketQuestionAnswerText(answer))
	if websocket.CandleComparison != nil && !questionCandleSafetyAllowed(text, websocket.CandleComparison) {
		return false
	}
	if !marketQuestionEvidenceLayerAllowedForPayload(text, query, payload) {
		return false
	}
	if !marketQuestionPrimaryDirectionAllowed(answer.Answer, query, official, daily, websocket) {
		return false
	}
	if !marketQuestionDailyRecentDirectionAllowed(answer.Answer, query, websocket) {
		return false
	}
	for _, forbidden := range []string{
		"매수하세요", "매도하세요", "매수를 추천", "매도를 추천", "투자해야",
		"공지 때문에", "공지가 원인", "원인입니다", "원인이다", "초래했습니다",
	} {
		if strings.Contains(text, forbidden) {
			return false
		}
	}
	unavailable := false
	if _, requested := marketQuestionWSWindowSec(query); requested {
		unavailable = websocket.Status != "CONFIRMED"
	} else if query.Intent == marketanalysis.MarketQuestionCalendarDayShare {
		unavailable = official.Status != "LIVE" || official.Current == nil
	} else if _, requested := marketQuestionOfficialWindowSec(query); requested {
		unavailable = official.Status != "LIVE" || !official.EndpointComparison
	} else if query.Intent == marketanalysis.MarketQuestionAssetCapture && query.Asset != "" {
		unavailable = !currentMarketQuestionAssetBasis(payload).Complete
	}
	if !unavailable {
		return true
	}
	if websocket.CandleComparison != nil {
		return questionCandleSafetyAllowed(text, websocket.CandleComparison)
	}
	for _, certain := range []string{
		"상승했습니다", "하락했습니다", "증가했습니다", "감소했습니다",
		"상승했다", "하락했다", "증가했다", "감소했다",
		"상승입니다", "하락입니다", "상승으로 확인", "하락으로 확인",
	} {
		if strings.Contains(text, certain) {
			return false
		}
	}
	for _, marker := range []string{
		"확정할 수 없", "확정하지 않", "완전하지 않", "자료가 부족", "미확보",
		"확정 전", "계산할 수 없", "비교할 수 없", "대기",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

var marketQuestionOfficialAssetObservationClaim = regexp.MustCompile(`(?i)(?:coverage|커버리지|websocket\s*(?:직접\s*)?관측).{0,60}(?:완전합니다|완전하다|완전한\s*상태|모두\s*수집|수집이\s*완료)|(?:websocket\s*(?:직접\s*)?관측|websocket\s*rolling|직접\s*관측\s*rolling).{0,30}(?:기준입니다|기준으로|관측값입니다|자료입니다)`)

func marketQuestionEvidenceLayerAllowedForPayload(text string, query marketanalysis.MarketQuestionQuery, payload marketanalysis.Analysis) bool {
	if query.Intent == marketanalysis.MarketQuestionAssetCapture && query.Asset != "" &&
		currentMarketQuestionAssetBasis(payload).Official {
		// The selected row provenance, not the shared ASSET_CAPTURE intent,
		// determines whether this is an official ticker or observed WS answer.
		officialQuery := query
		officialQuery.Intent = marketanalysis.MarketQuestionCurrentOfficialShare
		officialQuery.Comparison = marketanalysis.MarketQuestionCurrentSnapshot
		return marketQuestionEvidenceLayerAllowed(text, officialQuery) && !marketQuestionOfficialAssetObservationClaim.MatchString(text)
	}
	return marketQuestionEvidenceLayerAllowed(text, query)
}

func marketQuestionPrimaryDirectionAllowed(
	answer string,
	query marketanalysis.MarketQuestionQuery,
	official tickertrend.WindowComparison,
	daily tickertrend.DailyEndpointSeries,
	websocket marketanalysis.MarketQuestionIntervalEvidence,
) bool {
	if query.Metric != marketanalysis.MarketQuestionMetricOverallShare && query.Metric != marketanalysis.MarketQuestionMetricTargetExchange {
		return true
	}
	var delta *float64
	if _, requested := marketQuestionOfficialWindowSec(query); requested {
		delta = official.ShareDeltaPP
	} else if marketQuestionDailyEndpointSeriesRequested(query) {
		section := answer
		if summary, found := marketQuestionMarkdownSectionIntro(answer, "요약 결론"); found {
			section = summary
		}
		confirmed := daily.Status == "LIVE" && daily.Cumulative.EndpointComparison &&
			daily.Cumulative.ShareDeltaPP != nil
		return marketQuestionSectionDirectionAllowed(
			section, daily.Cumulative.ShareDeltaPP, confirmed,
		)
	} else if _, requested := marketQuestionWSWindowSec(query); requested {
		if websocket.CandleComparison != nil {
			delta = &websocket.CandleComparison.ShareDeltaPP
		} else if query.Exchange != "" && websocket.TargetExchange != nil {
			delta = websocket.TargetExchange.ShareDeltaPP
		} else {
			delta = websocket.ShareDeltaPP
		}
	}
	if delta == nil {
		return true
	}
	// The overall conclusion is required in the opening prose. Restrict the
	// direction guard to that sentence so a later phrase such as "USDT 상승
	// 기여" cannot be mistaken for a claim that the exchange's total share rose.
	intro := strings.ToLower(marketQuestionPrimaryIntro(answer))
	rise := marketQuestionPrimaryRiseClaim.MatchString(intro)
	fall := marketQuestionPrimaryFallClaim.MatchString(intro)
	switch {
	case *delta >= 0.005:
		return !fall
	case *delta <= -0.005:
		return !rise
	default:
		return !rise && !fall
	}
}

func marketQuestionPrimaryIntro(answer string) string {
	answer = strings.TrimSpace(answer)
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") ||
			strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			continue
		}
		for _, separator := range []string{". ", "다. ", "요. ", "\n"} {
			if index := strings.Index(line, separator); index >= 0 {
				return strings.TrimSpace(line[:index+len(strings.TrimSpace(separator))])
			}
		}
		return line
	}
	return answer
}

func marketQuestionDailyRecentDirectionAllowed(
	answer string,
	query marketanalysis.MarketQuestionQuery,
	websocket marketanalysis.MarketQuestionIntervalEvidence,
) bool {
	if !marketQuestionDailyRecentIntervalRequested(query) {
		return true
	}
	section, found := marketQuestionMarkdownSectionIntro(answer, "최근 단기 흐름")
	if !found {
		return false
	}
	confirmed := websocket.Status == "CONFIRMED" && websocket.ShareDeltaPP != nil &&
		websocket.Previous.BithumbSharePct != nil && websocket.Current.BithumbSharePct != nil
	return marketQuestionSectionDirectionAllowed(section, websocket.ShareDeltaPP, confirmed)
}

func marketQuestionEvidenceLayerAllowed(text string, query marketanalysis.MarketQuestionQuery) bool {
	hasWebSocket := strings.Contains(text, "websocket") || strings.Contains(text, "직접 관측")
	hasOfficial := strings.Contains(text, "공식") || strings.Contains(text, "정렬 시간봉")
	switch query.Intent {
	case "", marketanalysis.MarketQuestionCurrentOfficialShare,
		marketanalysis.MarketQuestionOfficialWindowChange,
		marketanalysis.MarketQuestionCalendarDayShare,
		marketanalysis.MarketQuestionDailyEndpointSeries,
		marketanalysis.MarketQuestionWSIntervalCompare,
		marketanalysis.MarketQuestionHistoricalRange,
		marketanalysis.MarketQuestionAssetIntervalAnalysis,
		marketanalysis.MarketQuestionAssetCapture,
		marketanalysis.MarketQuestionMovementExplanation,
		marketanalysis.MarketQuestionNoticeReaction,
		marketanalysis.MarketQuestionDataQuality:
	default:
		return false
	}
	// ASSET_INTERVAL_ANALYSIS intentionally serves both official endpoint and
	// direct-observation comparisons. Classify by the validated comparison,
	// matching the evidence envelope and runner guidance, not that shared intent.
	switch query.Comparison {
	case marketanalysis.MarketQuestionRolling24hEndpointDelta,
		marketanalysis.MarketQuestionCalendarDayClose,
		marketanalysis.MarketQuestionDailyRolling24hEndpoints:
		return hasOfficial
	case marketanalysis.MarketQuestionCurrentVsPreviousInterval,
		marketanalysis.MarketQuestionFixedHistoricalInterval:
		// Continue through the unchanged direct-observation label checks below.
	case marketanalysis.MarketQuestionCurrentSnapshot:
		switch query.Intent {
		case marketanalysis.MarketQuestionCurrentOfficialShare:
			return hasOfficial
		case marketanalysis.MarketQuestionMovementExplanation, marketanalysis.MarketQuestionNoticeReaction,
			marketanalysis.MarketQuestionDataQuality:
			return hasOfficial || hasWebSocket
		case marketanalysis.MarketQuestionAssetCapture:
		default:
			return false
		}
	case "":
		// Preserve known older direct callers that omit comparison. A missing
		// or unknown intent is not a license to skip the evidence-layer gate.
		switch query.Intent {
		case marketanalysis.MarketQuestionCurrentOfficialShare, marketanalysis.MarketQuestionOfficialWindowChange,
			marketanalysis.MarketQuestionCalendarDayShare, marketanalysis.MarketQuestionDailyEndpointSeries:
			return hasOfficial
		case marketanalysis.MarketQuestionWSIntervalCompare, marketanalysis.MarketQuestionHistoricalRange,
			marketanalysis.MarketQuestionAssetIntervalAnalysis, marketanalysis.MarketQuestionAssetCapture:
		case marketanalysis.MarketQuestionMovementExplanation, marketanalysis.MarketQuestionNoticeReaction,
			marketanalysis.MarketQuestionDataQuality:
			return hasOfficial || hasWebSocket
		default:
			return false
		}
	default:
		return false
	}
	if !hasWebSocket {
		return false
	}
	for _, wrongPrimary := range []string{"공식 점유율은", "공식 24시간 점유율은", "공식 정렬 시간봉 점유율은"} {
		if strings.Contains(text, wrongPrimary) {
			return false
		}
	}
	return true
}

func marketQuestionAnswerText(answer marketanalysis.MarketQuestionAnswer) string {
	return answer.Answer + "\n" + strings.Join(answer.KeyPoints, "\n") + "\n" + answer.Caveat
}

func normalizeMarketQuestionNumericToken(token string) (string, bool) {
	value, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimPrefix(token, "+"), ",", ""), 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatFloat(value, 'g', -1, 64), true
}
