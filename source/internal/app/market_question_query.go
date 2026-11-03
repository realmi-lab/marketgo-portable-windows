package app

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
)

var (
	errAmbiguousMarketQuestionWindow    = errors.New("market question contains multiple comparison windows")
	errAmbiguousMarketQuestionAsset     = errors.New("market question contains multiple assets")
	errUnknownMarketQuestionAsset       = errors.New("market question explicitly names an unregistered asset")
	errAmbiguousMarketQuestionExchange  = errors.New("market question contains multiple exchanges")
	errAmbiguousMarketQuestionDate      = errors.New("market question contains multiple calendar dates")
	errInvalidMarketQuestionWindow      = errors.New("market question comparison window is invalid or too large")
	errInvalidMarketQuestionDate        = errors.New("market question calendar date is invalid")
	marketQuestionDurationPattern       = regexp.MustCompile(`(?i)([0-9]+|한)\s*(시간|분)`)
	marketQuestionSecondDurationPattern = regexp.MustCompile(`([0-9]+|한)\s*초`)
	marketQuestionExplicitAssetPattern  = regexp.MustCompile(`(?:^|[^A-Z0-9_])([A-Z][A-Z0-9_]*)\s*(?:코인|토큰)`)
	marketQuestionPrefixedAssetPattern  = regexp.MustCompile(`(?:^|[^가-힣A-Z0-9_])(?:코인|토큰)\s+([A-Z][A-Z0-9_]*)`)
	marketQuestionYMDPattern            = regexp.MustCompile(`([0-9]{4})\s*(?:년|[-./])\s*([0-9]{1,2})\s*(?:월|[-./])\s*([0-9]{1,2})\s*일?`)
	marketQuestionMonthDayPattern       = regexp.MustCompile(`([0-9]{1,2})\s*월\s*([0-9]{1,2})\s*일`)
	marketQuestionDayPattern            = regexp.MustCompile(`([0-9]{1,2})\s*일`)
	marketQuestionMonthDayRangePattern  = regexp.MustCompile(`([0-9]{1,2})\s*월\s*([0-9]{1,2})\s*(?:~|〜|∼)\s*([0-9]{1,2})\s*일`)
	marketQuestionDayRangePattern       = regexp.MustCompile(`([0-9]{1,2})\s*(?:~|〜|∼)\s*([0-9]{1,2})\s*일`)
	marketQuestionMonthDaySeriesPattern = regexp.MustCompile(
		`([0-9]{1,2})\s*월\s*([0-9]{1,2}(?:\s*·\s*[0-9]{1,2})+)\s*일`,
	)
	marketQuestionDaySeriesPattern = regexp.MustCompile(
		`([0-9]{1,2}(?:\s*·\s*[0-9]{1,2})+)\s*일`,
	)
	marketQuestionDaySeriesNumberPattern        = regexp.MustCompile(`[0-9]{1,2}`)
	marketQuestionRelativeDaysPattern           = regexp.MustCompile(`(?:최근|지난)\s*([0-9]+)\s*일(?:간|동안)?|([0-9]+)\s*일(?:간|동안)`)
	marketQuestionRelativeMonthPattern          = regexp.MustCompile(`(?:최근|지난)\s*(?:1|한)\s*(?:개월|달)(?:간|동안)?`)
	marketQuestionClockPattern                  = regexp.MustCompile(`(?:^|[^0-9])([01]?[0-9]|2[0-3]):([0-5][0-9])(?::([0-5][0-9]))?`)
	marketQuestionMidnightStartPattern          = regexp.MustCompile(`(?:^|[^0-9])(?:자정|0{1,2}시|00:00(?::00)?)부터`)
	marketQuestionUnsupportedKoreanClockPattern = regexp.MustCompile(
		`(?:(?:오전|오후)\s*)?(?:[01]?[0-9]|2[0-3])\s*시(?:각|\s*기준)`,
	)
	marketQuestionResolutionPattern    = regexp.MustCompile(`([0-9]+)\s*(초|분|시간)\s*(?:단위|간격|봉)`)
	marketQuestionBareDayPeriodPattern = regexp.MustCompile(
		`(?i)^\s*(?:봉|선|이동\s*평균|평균|수익률|변동성|기간|주기|윈도우|SMA|EMA|동안|간(?:\s|$)|치(?:\s|$)|전(?:\s|$)|후(?:\s|$))`,
	)
)

const marketQuestionMaxOfficialWindowHours int64 = 90 * 24

var marketQuestionKSTLocation = time.FixedZone("Asia/Seoul", 9*60*60)

type marketQuestionDuration struct {
	value int64
	unit  marketanalysis.MarketQuestionTimeUnit
	start int
	end   int
}

type marketQuestionCalendarClaim struct {
	date       time.Time
	start      int
	end        int
	confidence string
}

type marketQuestionCalendarRange struct {
	start time.Time
	end   time.Time
}

func parseMarketQuestion(
	question string,
	assetAliases map[string]string,
) (marketanalysis.MarketQuestionQuery, error) {
	return parseMarketQuestionAt(question, assetAliases, time.Now())
}

func parseMarketQuestionAt(
	question string,
	assetAliases map[string]string,
	reference time.Time,
) (marketanalysis.MarketQuestionQuery, error) {
	query, err := parseMarketQuestionUncheckedAt(question, assetAliases, reference)
	if err != nil {
		return query, err
	}
	if err := validateMarketQuestionQuery(query, reference); err != nil {
		return query, fmt.Errorf("%w: %v", errInvalidMarketQuestionWindow, err)
	}
	return query, nil
}

func parseMarketQuestionUncheckedAt(
	question string,
	assetAliases map[string]string,
	reference time.Time,
) (marketanalysis.MarketQuestionQuery, error) {
	normalized := strings.TrimSpace(question)
	result := marketanalysis.MarketQuestionQuery{
		Intent:      marketanalysis.MarketQuestionCurrentOfficialShare,
		TimeUnit:    marketanalysis.MarketQuestionTimeNone,
		Comparison:  marketanalysis.MarketQuestionCurrentSnapshot,
		Metric:      marketanalysis.MarketQuestionMetricOverallShare,
		MarketScope: "KRW",
		EndPolicy:   "LATEST_CLOSED_BOUNDARY",
	}
	result.AnalysisDepth = marketQuestionAnalysisDepth(normalized)
	result.RequestedPathSec = parseMarketQuestionResolution(normalized)

	duration, found, err := parseMarketQuestionDuration(normalized)
	if err != nil {
		return result, err
	}
	calendarDates, err := parseMarketQuestionCalendarDates(normalized, reference)
	if err != nil {
		return result, err
	}
	clockStart, clockEnd, todayToNow, err := parseMarketQuestionTodayToNow(normalized, calendarDates, reference)
	if err != nil {
		return result, err
	}
	if !todayToNow && marketQuestionHasCurrentReferenceDate(normalized, calendarDates, reference) {
		calendarDates = nil
	}
	dateFound := len(calendarDates) > 0
	calendarDate := time.Time{}
	if dateFound {
		calendarDate = calendarDates[0]
	}
	if len(calendarDates) > 1 && found {
		return result, errAmbiguousMarketQuestionDate
	}
	clockRangeFound := todayToNow
	if !todayToNow {
		clockStart, clockEnd, clockRangeFound, err = parseMarketQuestionClockRange(normalized, calendarDate)
		if err != nil {
			return result, err
		}
	}
	if found && dateFound && !clockRangeFound && result.RequestedPathSec == 0 {
		return result, errAmbiguousMarketQuestionDate
	}
	asset, err := parseMarketQuestionAsset(normalized, assetAliases)
	if errors.Is(err, errAmbiguousMarketQuestionAsset) && !found && !dateFound && !clockRangeFound && !containsAnyString(normalized, "말고", "제외", "빼고", "아닌") && containsAnyString(normalized, "비교", " 중", "중에", "몰렸", "몰려", "많", "집중") {
		assets, assetsErr := parseMarketQuestionAssets(normalized, assetAliases)
		if assetsErr == nil && len(assets) >= 2 && len(assets) <= 4 {
			result.ComparedAssets = assets
			asset, err = assets[0], nil
		}
	}
	if err != nil {
		return result, err
	}
	result.Asset = asset
	exchange, err := parseMarketQuestionExchange(normalized)
	if err != nil {
		return result, err
	}
	result.Exchange = exchange
	if len(result.ComparedAssets) > 0 && exchange != "" && exchange != "bithumb" {
		return result, errAmbiguousMarketQuestionAsset
	}
	if target, pair := marketQuestionCurrentSharePair(normalized); pair {
		if found || dateFound || clockRangeFound || asset != "" || marketQuestionExplicitAllDomesticConverted(normalized) {
			return result, errAmbiguousMarketQuestionExchange
		}
		result.Exchange, result.PeerExchange = target, "bithumb"
	}
	if todayToNow && (asset != "" || exchange != "" && exchange != string(domain.ExchangeBithumb)) {
		// Preserve the existing EXACT_RANGE_KST catalog boundary; this natural
		// language alias must not add new exchange/asset calculation routes.
		return result, errInvalidMarketQuestionWindow
	}
	if exchange != "" && exchange != string(domain.ExchangeBithumb) && asset == "" {
		result.Metric = marketanalysis.MarketQuestionMetricTargetExchange
	}
	if found && marketQuestionExplicitOfficialWindow(normalized) &&
		(durationSeconds(duration)%3_600 != 0 || exchange != "" && exchange != string(domain.ExchangeBithumb) || marketQuestionUsesWebSocketInterval(duration, normalized)) {
		return result, errInvalidMarketQuestionWindow
	}
	result.RequestedExplanation = marketQuestionExplanations(normalized)
	if target, peer, ok := marketQuestionCoinPair(normalized); ok && !dateFound && !clockRangeFound && asset == "" && !marketQuestionExplicitOfficialWindow(normalized) {
		result.Exchange, result.PeerExchange = target, peer
		result.Intent = marketanalysis.MarketQuestionWSIntervalCompare
		result.Comparison = marketanalysis.MarketQuestionCurrentVsPreviousInterval
		result.TimeValue, result.TimeUnit = duration.value, duration.unit
		if !found {
			result.TimeValue, result.TimeUnit = 1, marketanalysis.MarketQuestionTimeHour
			result.PeriodAssumption = "기간을 지정하지 않아 최근 1시간의 완료된 구간을 비교했습니다."
		}
		result.EndPolicy = "LATEST_CLOSED_MINUTE_BOUNDARY"
		result.RequestedExplanation = mergeMarketQuestionExplanations(result.RequestedExplanation, []string{"EXCHANGES", "QUALITY"})
		return result, nil
	}
	if marketQuestionExplicitAllDomesticConverted(normalized) {
		result.MarketScope = "ALL_DOMESTIC_CONVERTED"
	}
	if marketQuestionExplicitAllDomesticConverted(normalized) && marketQuestionExplicitKRWOnly(normalized) {
		return result, errInvalidMarketQuestionWindow
	}
	if marketQuestionRelativeMonthPattern.MatchString(normalized) {
		end := reference.In(marketQuestionKSTLocation).Truncate(time.Minute).Add(-time.Second)
		start := end.Add(-30 * 24 * time.Hour).Add(time.Second)
		result.Intent = marketanalysis.MarketQuestionHistoricalRange
		result.Comparison = marketanalysis.MarketQuestionFixedHistoricalInterval
		result.EndPolicy = "LATEST_CLOSED_MINUTE_30D_RANGE"
		result.TargetDate = start.Format("2006-01-02") + ".." + end.Format("2006-01-02")
		result.TargetStartMS, result.TargetEndMS = start.UnixMilli(), end.UnixMilli()
		result.TimeValue, result.TimeUnit = 30*24, marketanalysis.MarketQuestionTimeHour
		result.RequestedPathSec = 3_600
		result.RequestedExplanation = mergeMarketQuestionExplanations(
			result.RequestedExplanation,
			[]string{"DRIVERS", "EXCHANGES", "PATH", "QUALITY"},
		)
		return result, nil
	}

	if len(calendarDates) > 1 {
		if clockRangeFound {
			return result, errAmbiguousMarketQuestionDate
		}
		if err := applyMarketQuestionDailyEndpointSeries(&result, calendarDates, reference); err != nil {
			return result, err
		}
		required := []string{"DRIVERS", "EXCHANGES", "PATH", "QUALITY"}
		if marketQuestionRequestsDailyRealtime(normalized) {
			required = append(required, "REALTIME")
		}
		result.RequestedExplanation = mergeMarketQuestionExplanations(result.RequestedExplanation, required)
		return result, nil
	}
	if dateFound && clockRangeFound {
		result.Intent = marketanalysis.MarketQuestionHistoricalRange
		result.Comparison = marketanalysis.MarketQuestionFixedHistoricalInterval
		result.EndPolicy = "EXACT_USER_TIME_RANGE_KST"
		result.TargetDate = calendarDate.Format("2006-01-02")
		result.TargetStartMS = clockStart.UnixMilli()
		result.TargetEndMS = clockEnd.UnixMilli()
		return result, nil
	}
	if dateFound {
		start := calendarDate.In(marketQuestionKSTLocation)
		end := start.AddDate(0, 0, 1)
		result.Intent = marketanalysis.MarketQuestionCalendarDayShare
		result.Comparison = marketanalysis.MarketQuestionCalendarDayClose
		result.EndPolicy = "EXACT_CALENDAR_DAY_END_KST"
		result.TargetDate = start.Format("2006-01-02")
		result.TargetStartMS = start.UnixMilli()
		result.TargetEndMS = end.UnixMilli()
		if asset != "" {
			result.Metric = marketQuestionAssetMetric(normalized)
		}
		return result, nil
	}

	if asset != "" {
		result.Metric = marketQuestionAssetMetric(normalized)
		if found {
			result.Intent = marketanalysis.MarketQuestionAssetIntervalAnalysis
			applyMarketQuestionDuration(&result, duration, normalized)
			applyMarketQuestionExchangeInterval(&result)
		} else {
			result.Intent = marketanalysis.MarketQuestionAssetCapture
		}
		if marketQuestionRequestsNotices(normalized) {
			result.Intent = marketanalysis.MarketQuestionNoticeReaction
		}
		return result, nil
	}

	if found {
		applyMarketQuestionDuration(&result, duration, normalized)
		applyMarketQuestionExchangeInterval(&result)
		return result, nil
	}
	if marketQuestionAsksRestrictedInternalData(normalized) {
		return result, marketanalysis.ErrRestrictedLLMQuestion
	} else if marketQuestionAsksMovement(normalized) {
		result.Intent = marketanalysis.MarketQuestionMovementExplanation
	}
	return result, nil
}

func marketQuestionAnalysisDepth(question string) string {
	for _, marker := range []string{
		"상세", "자세히", "분석", "원인", "기여", "구조", "왜", "진단", "어떤 코인", "코인별",
		"등락", "상위", "순위", "랭킹", "주도", "견인", "영향", "오르고", "올린", "내린", "떨어", "추이", "반등",
	} {
		if strings.Contains(question, marker) {
			return "DETAILED"
		}
	}
	return "SUMMARY"
}

func parseMarketQuestionResolution(question string) int64 {
	match := marketQuestionResolutionPattern.FindStringSubmatch(question)
	if len(match) < 3 {
		return 0
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	switch match[2] {
	case "시간":
		value *= 3_600
	case "분":
		value *= 60
	}
	if value < 10 || value > 3_600 {
		return 0
	}
	return value
}

func parseMarketQuestionClockRange(question string, date time.Time) (time.Time, time.Time, bool, error) {
	if marketQuestionUnsupportedKoreanClockPattern.MatchString(question) {
		return time.Time{}, time.Time{}, false, errInvalidMarketQuestionWindow
	}
	matches := marketQuestionClockPattern.FindAllStringSubmatch(question, -1)
	if len(matches) == 0 {
		return time.Time{}, time.Time{}, false, nil
	}
	if date.IsZero() || len(matches) != 2 {
		return time.Time{}, time.Time{}, false, errInvalidMarketQuestionWindow
	}
	parse := func(match []string) (time.Time, error) {
		if len(match) < 4 {
			return time.Time{}, errInvalidMarketQuestionWindow
		}
		hour, hourErr := strconv.Atoi(match[1])
		minute, minuteErr := strconv.Atoi(match[2])
		second := 0
		var secondErr error
		if match[3] != "" {
			second, secondErr = strconv.Atoi(match[3])
		}
		if hourErr != nil || minuteErr != nil || secondErr != nil {
			return time.Time{}, errInvalidMarketQuestionWindow
		}
		return time.Date(date.Year(), date.Month(), date.Day(), hour, minute, second, 0, marketQuestionKSTLocation), nil
	}
	start, err := parse(matches[0])
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	end, err := parse(matches[1])
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !end.After(start) {
		end = end.AddDate(0, 0, 1)
	}
	if end.Sub(start) > 24*time.Hour {
		return time.Time{}, time.Time{}, false, errInvalidMarketQuestionWindow
	}
	return start, end, true, nil
}

func parseMarketQuestionExchange(question string) (string, error) {
	if target, ok := marketQuestionCurrentSharePair(question); ok {
		return target, nil
	}
	if target, _, ok := marketQuestionCoinPair(question); ok {
		return target, nil
	}
	found := map[string]struct{}{}
	excluded := false
	upperQuestion := strings.ToUpper(question)
	for alias, canonical := range marketQuestionExchangeNames {
		present, negated := marketQuestionTargetAliasPresent(upperQuestion, strings.ToUpper(alias))
		excluded = excluded || negated
		if present {
			found[canonical] = struct{}{}
		}
	}
	if len(found) > 1 || len(found) == 0 && excluded {
		return "", errAmbiguousMarketQuestionExchange
	}
	for exchange := range found {
		return exchange, nil
	}
	return "", nil
}

// Non-Bithumb exchange changes cannot use the Bithumb-only aligned endpoint
// contract. Route them to the persisted WebSocket observation instead. The
// existing Bithumb wording keeps its official aligned-hour semantics.
func applyMarketQuestionExchangeInterval(result *marketanalysis.MarketQuestionQuery) {
	if result == nil || result.Exchange == "" || result.Exchange == string(domain.ExchangeBithumb) || result.TimeValue <= 0 {
		return
	}
	result.Intent = marketanalysis.MarketQuestionWSIntervalCompare
	if result.Asset != "" {
		result.Intent = marketanalysis.MarketQuestionAssetIntervalAnalysis
	}
	result.Comparison = marketanalysis.MarketQuestionCurrentVsPreviousInterval
	result.EndPolicy = "LATEST_CLOSED_MINUTE_BOUNDARY"
}

func parseMarketQuestionCalendarDate(question string, reference time.Time) (time.Time, bool, error) {
	dates, err := parseMarketQuestionCalendarDates(question, reference)
	if err != nil {
		return time.Time{}, false, err
	}
	if len(dates) == 0 {
		return time.Time{}, false, nil
	}
	if len(dates) > 1 {
		return time.Time{}, false, errAmbiguousMarketQuestionDate
	}
	return dates[0], true, nil
}

const marketQuestionMaxDailyEndpointDates = 8

func parseMarketQuestionCalendarDates(question string, reference time.Time) ([]time.Time, error) {
	reference = reference.In(marketQuestionKSTLocation)
	if marketQuestionContainsFutureRelativeDate(question) {
		return nil, errInvalidMarketQuestionDate
	}
	if dates, found, err := parseMarketQuestionRelativeDaySeries(question, reference); found || err != nil {
		return dates, err
	}

	dates := []time.Time{}
	claims := []marketQuestionCalendarClaim{}
	ranges := []marketQuestionCalendarRange{}

	type explicitDateContext struct {
		start        int
		end          int
		year         int
		month        int
		day          int
		explicitYear bool
	}
	occupied := [][2]int{}
	contexts := []explicitDateContext{}
	today := calendarDateAtOffset(reference, 0)
	appendDate := func(year, month, day, start, end int, confidence string) (time.Time, error) {
		candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, marketQuestionKSTLocation)
		if candidate.Year() != year || int(candidate.Month()) != month || candidate.Day() != day {
			return time.Time{}, errInvalidMarketQuestionDate
		}
		if candidate.After(today) {
			return time.Time{}, errInvalidMarketQuestionDate
		}
		dates = append(dates, candidate)
		claims = append(claims, marketQuestionCalendarClaim{
			date: candidate, start: start, end: end, confidence: confidence,
		})
		return candidate, nil
	}
	parsePart := func(match []int, group int) (int, error) {
		start, end := 2*group, 2*group+1
		if end >= len(match) || match[start] < 0 || match[end] <= match[start] {
			return 0, errInvalidMarketQuestionDate
		}
		value, err := strconv.Atoi(question[match[start]:match[end]])
		if err != nil {
			return 0, errInvalidMarketQuestionDate
		}
		return value, nil
	}
	overlaps := func(start, end int) bool {
		for _, span := range occupied {
			if start < span[1] && span[0] < end {
				return true
			}
		}
		return false
	}
	appendRelative := func(marker string, offset int) error {
		for searchStart := 0; searchStart < len(question); {
			relative := strings.Index(question[searchStart:], marker)
			if relative < 0 {
				return nil
			}
			start := searchStart + relative
			end := start + len(marker)
			searchStart = end
			if overlaps(start, end) {
				continue
			}
			date := calendarDateAtOffset(reference, offset)
			if _, err := appendDate(date.Year(), int(date.Month()), date.Day(), start, end, "HIGH"); err != nil {
				return err
			}
			occupied = append(occupied, [2]int{start, end})
		}
		return nil
	}
	// Parse the longer relative word first so "엊그제" does not also emit a
	// second claim for its "그제" suffix.
	for _, relative := range []struct {
		marker string
		offset int
	}{{"엊그제", -2}, {"그제", -2}, {"어제", -1}, {"오늘", 0}} {
		if err := appendRelative(relative.marker, relative.offset); err != nil {
			return nil, err
		}
	}

	for _, match := range marketQuestionYMDPattern.FindAllStringSubmatchIndex(question, -1) {
		year, yearErr := parsePart(match, 1)
		month, monthErr := parsePart(match, 2)
		day, dayErr := parsePart(match, 3)
		if yearErr != nil || monthErr != nil || dayErr != nil {
			return nil, errInvalidMarketQuestionDate
		}
		if _, err := appendDate(year, month, day, match[0], match[1], "HIGH"); err != nil {
			return nil, err
		}
		occupied = append(occupied, [2]int{match[0], match[1]})
		contexts = append(contexts, explicitDateContext{
			start: match[0], end: match[1], year: year, month: month, day: day, explicitYear: true,
		})
	}
	inheritedYearForMonth := func(offset, month int) (int, bool, error) {
		selected := explicitDateContext{end: -1}
		for _, context := range contexts {
			if context.explicitYear && context.end <= offset && context.end > selected.end {
				selected = context
			}
		}
		if selected.end < 0 {
			return reference.Year(), false, nil
		}
		// A decreasing month after an explicit year may mean either an
		// out-of-order date or a year rollover. Do not invent either meaning.
		if month < selected.month {
			return 0, true, errAmbiguousMarketQuestionDate
		}
		return selected.year, true, nil
	}
	for _, match := range marketQuestionMonthDayRangePattern.FindAllStringSubmatchIndex(question, -1) {
		if overlaps(match[0], match[1]) {
			continue
		}
		month, monthErr := parsePart(match, 1)
		startDay, startErr := parsePart(match, 2)
		endDay, endErr := parsePart(match, 3)
		if monthErr != nil || startErr != nil || endErr != nil || endDay < startDay {
			return nil, errInvalidMarketQuestionDate
		}
		year, inherited, yearErr := inheritedYearForMonth(match[0], month)
		if yearErr != nil {
			return nil, yearErr
		}
		start, appendErr := appendDate(year, month, startDay, match[0], match[1], "HIGH")
		if appendErr != nil {
			return nil, appendErr
		}
		end, appendErr := appendDate(year, month, endDay, match[0], match[1], "HIGH")
		if appendErr != nil {
			return nil, appendErr
		}
		ranges = append(ranges, marketQuestionCalendarRange{start: start, end: end})
		occupied = append(occupied, [2]int{match[0], match[1]})
		contexts = append(contexts, explicitDateContext{
			start: match[0], end: match[1], year: year, month: month, day: endDay, explicitYear: inherited,
		})
	}
	for _, match := range marketQuestionMonthDaySeriesPattern.FindAllStringSubmatchIndex(question, -1) {
		if overlaps(match[0], match[1]) || marketQuestionNumericMatchHasDigitPrefix(question, match[0]) {
			continue
		}
		month, monthErr := parsePart(match, 1)
		if monthErr != nil || len(match) < 6 || match[4] < 0 || match[5] <= match[4] {
			return nil, errInvalidMarketQuestionDate
		}
		year, inherited, yearErr := inheritedYearForMonth(match[0], month)
		if yearErr != nil {
			return nil, yearErr
		}
		dayMatches := marketQuestionDaySeriesNumberPattern.FindAllStringIndex(question[match[4]:match[5]], -1)
		if len(dayMatches) < 2 {
			return nil, errInvalidMarketQuestionDate
		}
		lastDay := 0
		for _, dayMatch := range dayMatches {
			day, parseErr := strconv.Atoi(question[match[4]+dayMatch[0] : match[4]+dayMatch[1]])
			if parseErr != nil {
				return nil, errInvalidMarketQuestionDate
			}
			if lastDay > 0 && day < lastDay {
				return nil, errAmbiguousMarketQuestionDate
			}
			if _, appendErr := appendDate(year, month, day, match[0], match[1], "HIGH"); appendErr != nil {
				return nil, appendErr
			}
			lastDay = day
		}
		occupied = append(occupied, [2]int{match[0], match[1]})
		contexts = append(contexts, explicitDateContext{
			start: match[0], end: match[1], year: year, month: month, day: lastDay, explicitYear: inherited,
		})
	}
	for _, match := range marketQuestionMonthDayPattern.FindAllStringSubmatchIndex(question, -1) {
		if overlaps(match[0], match[1]) {
			continue
		}
		month, monthErr := parsePart(match, 1)
		day, dayErr := parsePart(match, 2)
		if monthErr != nil || dayErr != nil {
			return nil, errInvalidMarketQuestionDate
		}
		year, inherited, yearErr := inheritedYearForMonth(match[0], month)
		if yearErr != nil {
			return nil, yearErr
		}
		if _, err := appendDate(year, month, day, match[0], match[1], "HIGH"); err != nil {
			return nil, err
		}
		occupied = append(occupied, [2]int{match[0], match[1]})
		contexts = append(contexts, explicitDateContext{
			start: match[0], end: match[1], year: year, month: month, day: day, explicitYear: inherited,
		})
	}
	nearestPrecedingContext := func(offset int) (explicitDateContext, bool) {
		selected := explicitDateContext{end: -1}
		for _, context := range contexts {
			if context.end <= offset && context.end > selected.end {
				selected = context
			}
		}
		return selected, selected.end >= 0
	}
	inferredYearMonth := func(offset int) (int, int) {
		if selected, found := nearestPrecedingContext(offset); found {
			return selected.year, selected.month
		}
		unique := map[[2]int]bool{}
		for _, context := range contexts {
			unique[[2]int{context.year, context.month}] = true
		}
		if len(unique) == 1 {
			for value := range unique {
				return value[0], value[1]
			}
		}
		selected := explicitDateContext{}
		futureFound := false
		for _, context := range contexts {
			if context.start >= offset && (!futureFound || context.start < selected.start) {
				selected = context
				futureFound = true
			}
		}
		if futureFound {
			return selected.year, selected.month
		}
		return reference.Year(), int(reference.Month())
	}
	for _, match := range marketQuestionDayRangePattern.FindAllStringSubmatchIndex(question, -1) {
		if overlaps(match[0], match[1]) {
			continue
		}
		startDay, startErr := parsePart(match, 1)
		endDay, endErr := parsePart(match, 2)
		year, month := inferredYearMonth(match[0])
		if startErr != nil || endErr != nil || endDay < startDay {
			return nil, errInvalidMarketQuestionDate
		}
		start, appendErr := appendDate(year, month, startDay, match[0], match[1], "HIGH")
		if appendErr != nil {
			return nil, appendErr
		}
		end, appendErr := appendDate(year, month, endDay, match[0], match[1], "HIGH")
		if appendErr != nil {
			return nil, appendErr
		}
		ranges = append(ranges, marketQuestionCalendarRange{start: start, end: end})
		occupied = append(occupied, [2]int{match[0], match[1]})
		contexts = append(contexts, explicitDateContext{
			start: match[0], end: match[1], year: year, month: month, day: endDay,
		})
	}
	for _, match := range marketQuestionDaySeriesPattern.FindAllStringSubmatchIndex(question, -1) {
		if overlaps(match[0], match[1]) || marketQuestionNumericMatchHasDigitPrefix(question, match[0]) ||
			len(match) < 4 || match[2] < 0 || match[3] <= match[2] {
			continue
		}
		year, month := inferredYearMonth(match[0])
		dayMatches := marketQuestionDaySeriesNumberPattern.FindAllStringIndex(question[match[2]:match[3]], -1)
		if len(dayMatches) < 2 {
			return nil, errInvalidMarketQuestionDate
		}
		lastDay := 0
		for _, dayMatch := range dayMatches {
			day, parseErr := strconv.Atoi(question[match[2]+dayMatch[0] : match[2]+dayMatch[1]])
			if parseErr != nil {
				return nil, errInvalidMarketQuestionDate
			}
			if lastDay > 0 && day < lastDay {
				return nil, errAmbiguousMarketQuestionDate
			}
			if _, appendErr := appendDate(year, month, day, match[0], match[1], "HIGH"); appendErr != nil {
				return nil, appendErr
			}
			lastDay = day
		}
		occupied = append(occupied, [2]int{match[0], match[1]})
		contexts = append(contexts, explicitDateContext{
			start: match[0], end: match[1], year: year, month: month, day: lastDay,
		})
	}
	for _, match := range marketQuestionDayPattern.FindAllStringSubmatchIndex(question, -1) {
		if len(match) < 4 || match[2] < 0 || match[3] <= match[2] || overlaps(match[0], match[1]) {
			continue
		}
		if match[0] > 0 {
			prefix := question[:match[0]]
			previous := prefix[len(prefix)-1]
			if previous >= '0' && previous <= '9' || strings.HasSuffix(strings.TrimSpace(prefix), "월") {
				continue
			}
		}
		if marketQuestionBareDayIsPeriod(question, match[1]) {
			continue
		}
		day, err := parsePart(match, 1)
		if err != nil {
			return nil, errInvalidMarketQuestionDate
		}
		if context, found := nearestPrecedingContext(match[0]); found && day < context.day &&
			marketQuestionDirectDateListSeparator(question, context.end, match[0]) {
			return nil, errAmbiguousMarketQuestionDate
		}
		year, month := inferredYearMonth(match[0])
		if _, err := appendDate(year, month, day, match[0], match[1], "HIGH"); err != nil {
			return nil, err
		}
	}
	if len(dates) == 0 {
		return []time.Time{}, nil
	}
	normalized, err := normalizeMarketQuestionCalendarDates(dates)
	if err != nil {
		return nil, err
	}
	orderedClaims := append([]marketQuestionCalendarClaim(nil), claims...)
	sort.SliceStable(orderedClaims, func(i, j int) bool {
		if orderedClaims[i].start != orderedClaims[j].start {
			return orderedClaims[i].start < orderedClaims[j].start
		}
		return orderedClaims[i].end < orderedClaims[j].end
	})
	for index := 1; index < len(orderedClaims); index++ {
		left, right := orderedClaims[index-1], orderedClaims[index]
		if left.confidence != "HIGH" || right.confidence != "HIGH" || left.end > right.start ||
			left.start == right.start && left.end == right.end ||
			!marketQuestionDirectDateRange(question, left, right) {
			continue
		}
		if right.date.Before(left.date) {
			return nil, errAmbiguousMarketQuestionDate
		}
		ranges = append(ranges, marketQuestionCalendarRange{start: left.date, end: right.date})
	}
	if len(ranges) == 0 || len(normalized) < 2 {
		return normalized, nil
	}
	expanded := append([]time.Time(nil), normalized...)
	for _, dateRange := range ranges {
		if dateRange.end.Before(dateRange.start) {
			return nil, errAmbiguousMarketQuestionDate
		}
		for cursor := dateRange.start; !cursor.After(dateRange.end); cursor = cursor.AddDate(0, 0, 1) {
			expanded = append(expanded, cursor)
		}
	}
	return normalizeMarketQuestionCalendarDates(expanded)
}

func marketQuestionContainsFutureRelativeDate(question string) bool {
	for _, marker := range []string{"내일", "모레", "글피"} {
		if strings.Contains(question, marker) {
			return true
		}
	}
	return false
}

func marketQuestionNumericMatchHasDigitPrefix(question string, start int) bool {
	if start <= 0 || start > len(question) {
		return false
	}
	previous := question[start-1]
	return previous >= '0' && previous <= '9'
}

func marketQuestionBareDayIsPeriod(question string, matchEnd int) bool {
	if matchEnd < 0 || matchEnd > len(question) {
		return false
	}
	return marketQuestionBareDayPeriodPattern.MatchString(question[matchEnd:])
}

func marketQuestionDirectDateListSeparator(question string, leftEnd, rightStart int) bool {
	if leftEnd < 0 || rightStart < leftEnd || rightStart > len(question) {
		return false
	}
	between := strings.TrimSpace(question[leftEnd:rightStart])
	switch between {
	case ",", "·", "과", "와", "그리고":
		return true
	default:
		return false
	}
}

func marketQuestionDirectDateRange(
	question string,
	left, right marketQuestionCalendarClaim,
) bool {
	if left.end < 0 || right.start < left.end || right.start > len(question) {
		return false
	}
	between := strings.TrimSpace(question[left.end:right.start])
	switch between {
	case "~", "〜", "∼", "-", "–", "—", "부터", "에서":
		return true
	default:
		return false
	}
}

func marketQuestionPositiveDailySeriesMarker(question, marker string) bool {
	for searchStart := 0; searchStart < len(question); {
		index := strings.Index(question[searchStart:], marker)
		if index < 0 {
			return false
		}
		index += searchStart
		suffix := question[index+len(marker):]
		compactSuffix := strings.ReplaceAll(strings.TrimSpace(suffix), " ", "")
		for _, particle := range []string{"은", "는", "이", "가", "을", "를", "만"} {
			if strings.HasPrefix(compactSuffix, particle) {
				compactSuffix = strings.TrimPrefix(compactSuffix, particle)
				break
			}
		}
		negated := false
		for _, negative := range []string{"말고", "아니라", "아니고", "제외", "빼고"} {
			if strings.HasPrefix(compactSuffix, negative) {
				negated = true
				break
			}
		}
		if !negated {
			return true
		}
		searchStart = index + len(marker)
	}
	return false
}

func parseMarketQuestionRelativeDaySeries(question string, reference time.Time) ([]time.Time, bool, error) {
	match := marketQuestionRelativeDaysPattern.FindStringSubmatch(question)
	if len(match) == 0 {
		return nil, false, nil
	}
	dailySeriesRequested := marketQuestionRequestsRelativeDailySeries(question)
	// "최근 3일" alone can mean either one 72-hour comparison or three
	// same-clock daily endpoints. Do not silently choose a business meaning.
	if !dailySeriesRequested {
		return nil, true, nil
	}
	raw := match[1]
	if raw == "" {
		raw = match[2]
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count < 2 || count > marketQuestionMaxDailyEndpointDates {
		return nil, true, errInvalidMarketQuestionWindow
	}
	dates := make([]time.Time, 0, count)
	for offset := -(count - 1); offset <= 0; offset++ {
		dates = append(dates, calendarDateAtOffset(reference, offset))
	}
	return dates, true, nil
}

func marketQuestionRequestsRelativeDailySeries(question string) bool {
	for _, marker := range []string{"매일", "일별", "날짜별", "각날짜", "하루씩"} {
		if marketQuestionPositiveDailySeriesMarker(question, marker) {
			return true
		}
	}
	return false
}

func normalizeMarketQuestionCalendarDates(values []time.Time) ([]time.Time, error) {
	seen := map[int64]bool{}
	result := make([]time.Time, 0, len(values))
	for _, value := range values {
		day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, marketQuestionKSTLocation)
		if !seen[day.UnixMilli()] {
			seen[day.UnixMilli()] = true
			result = append(result, day)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	if len(result) > marketQuestionMaxDailyEndpointDates {
		return nil, errInvalidMarketQuestionWindow
	}
	if len(result) > 1 && result[len(result)-1].Sub(result[0]) > 31*24*time.Hour {
		return nil, errInvalidMarketQuestionWindow
	}
	return result, nil
}

func applyMarketQuestionDailyEndpointSeries(
	result *marketanalysis.MarketQuestionQuery,
	dates []time.Time,
	reference time.Time,
) error {
	if result == nil || len(dates) < 2 || len(dates) > marketQuestionMaxDailyEndpointDates {
		return errInvalidMarketQuestionDate
	}
	latestSafe := reference.In(marketQuestionKSTLocation).Add(-2 * time.Minute).Truncate(time.Hour)
	result.TargetDates = make([]string, 0, len(dates))
	var first, last time.Time
	for index, date := range dates {
		endpoint := time.Date(
			date.Year(), date.Month(), date.Day(), latestSafe.Hour(), 0, 0, 0,
			marketQuestionKSTLocation,
		)
		if endpoint.After(latestSafe) {
			return errInvalidMarketQuestionDate
		}
		result.TargetDates = append(result.TargetDates, endpoint.Format("2006-01-02"))
		if index == 0 {
			first = endpoint
		}
		last = endpoint
	}
	result.Intent = marketanalysis.MarketQuestionDailyEndpointSeries
	result.Comparison = marketanalysis.MarketQuestionDailyRolling24hEndpoints
	result.EndPolicy = "SAME_KST_HOUR_EXACT_ALIGNED_ENDPOINTS"
	result.TargetDate = first.Format("2006-01-02") + ".." + last.Format("2006-01-02")
	result.TargetStartMS, result.TargetEndMS = first.UnixMilli(), last.UnixMilli()
	result.TimeValue = int64(last.Sub(first) / time.Hour)
	result.TimeUnit = marketanalysis.MarketQuestionTimeHour
	result.AnalysisDepth = "DETAILED"
	return nil
}

func marketQuestionRequestsDailyRealtime(question string) bool {
	compact := strings.ReplaceAll(strings.TrimSpace(question), " ", "")
	for _, marker := range []string{
		"현재시간", "현재시각", "현재기준", "현재까지", "현재흐름", "지금", "지금흐름", "오늘", "실시간", "방금", "최근흐름", "단기흐름",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func mergeMarketQuestionExplanations(current, required []string) []string {
	selected := map[string]bool{}
	for _, value := range append(append([]string{}, current...), required...) {
		selected[value] = true
	}
	order := []string{"DRIVERS", "EXCHANGES", "PATH", "PRICE", "REALTIME", "ORDERBOOK", "NOTICES", "QUALITY"}
	result := make([]string, 0, len(selected))
	for _, value := range order {
		if selected[value] {
			result = append(result, value)
		}
	}
	return result
}

func uniqueMarketQuestionCalendarDate(
	matches [][]string,
	yearIndex, monthIndex, dayIndex int,
	defaultYear int,
) (time.Time, bool, error) {
	var selected time.Time
	for _, match := range matches {
		if monthIndex >= len(match) || dayIndex >= len(match) || yearIndex >= len(match) {
			return time.Time{}, false, errInvalidMarketQuestionDate
		}
		year := defaultYear
		var err error
		if yearIndex >= 0 {
			year, err = strconv.Atoi(match[yearIndex])
			if err != nil {
				return time.Time{}, false, errInvalidMarketQuestionDate
			}
		}
		month, monthErr := strconv.Atoi(match[monthIndex])
		day, dayErr := strconv.Atoi(match[dayIndex])
		if monthErr != nil || dayErr != nil {
			return time.Time{}, false, errInvalidMarketQuestionDate
		}
		candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, marketQuestionKSTLocation)
		if candidate.Year() != year || int(candidate.Month()) != month || candidate.Day() != day {
			return time.Time{}, false, errInvalidMarketQuestionDate
		}
		if !selected.IsZero() && !candidate.Equal(selected) {
			return time.Time{}, false, errAmbiguousMarketQuestionDate
		}
		selected = candidate
	}
	if selected.IsZero() {
		return time.Time{}, false, nil
	}
	return selected, true, nil
}

func calendarDateAtOffset(reference time.Time, dayOffset int) time.Time {
	base := time.Date(reference.Year(), reference.Month(), reference.Day(), 0, 0, 0, 0, marketQuestionKSTLocation)
	return base.AddDate(0, 0, dayOffset)
}

func parseMarketQuestionDuration(question string) (marketQuestionDuration, bool, error) {
	if marketQuestionHasUnsupportedSecondWindow(question) {
		return marketQuestionDuration{}, false, errInvalidMarketQuestionWindow
	}
	matches := marketQuestionDurationPattern.FindAllStringSubmatchIndex(question, -1)
	durations := make([]marketQuestionDuration, 0, len(matches)+1)
	if dayMatch := marketQuestionRelativeDaysPattern.FindStringSubmatchIndex(question); len(dayMatch) >= 6 &&
		!marketQuestionRequestsRelativeDailySeries(question) {
		valueStart, valueEnd := dayMatch[2], dayMatch[3]
		if valueStart < 0 || valueEnd <= valueStart {
			valueStart, valueEnd = dayMatch[4], dayMatch[5]
		}
		value, err := strconv.ParseInt(question[valueStart:valueEnd], 10, 64)
		if err != nil || value <= 0 || value > marketQuestionMaxOfficialWindowHours/24 {
			return marketQuestionDuration{}, false, errInvalidMarketQuestionWindow
		}
		durations = append(durations, marketQuestionDuration{
			value: value * 24, unit: marketanalysis.MarketQuestionTimeHour,
			start: dayMatch[0], end: dayMatch[1],
		})
	}
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		rawValue := question[match[2]:match[3]]
		value := int64(1)
		var err error
		if rawValue != "한" {
			value, err = strconv.ParseInt(rawValue, 10, 64)
			if err != nil || value <= 0 {
				return marketQuestionDuration{}, false, errInvalidMarketQuestionWindow
			}
		}
		unit := marketanalysis.MarketQuestionTimeMinute
		if question[match[4]:match[5]] == "시간" {
			unit = marketanalysis.MarketQuestionTimeHour
		}
		if unit == marketanalysis.MarketQuestionTimeHour && value > marketQuestionMaxOfficialWindowHours ||
			unit == marketanalysis.MarketQuestionTimeMinute && value > marketQuestionMaxOfficialWindowHours*60 {
			return marketQuestionDuration{}, false, errInvalidMarketQuestionWindow
		}
		duration := marketQuestionDuration{value: value, unit: unit, start: match[0], end: match[1]}
		if marketQuestionRolling24hMetricLabel(question, duration) {
			continue
		}
		durations = append(durations, duration)
	}
	if len(durations) == 0 {
		return marketQuestionDuration{}, false, nil
	}
	seconds := durationSeconds(durations[0])
	for _, duration := range durations[1:] {
		if durationSeconds(duration) != seconds {
			return marketQuestionDuration{}, false, errAmbiguousMarketQuestionWindow
		}
	}
	return durations[0], true, nil
}

// A path resolution or an explicitly named fixed signal is not the question's
// comparison window. Other relative second amounts cannot be represented by
// the current minute/hour query type and must not silently become CURRENT.
func marketQuestionHasUnsupportedSecondWindow(question string) bool {
	period := marketQuestionResolutionPattern.ReplaceAllString(question, "")
	for _, match := range marketQuestionSecondDurationPattern.FindAllStringSubmatchIndex(period, -1) {
		suffix := strings.Join(strings.Fields(period[match[1]:]), "")
		seconds := period[match[2]:match[3]]
		if seconds == "10" && (strings.HasPrefix(suffix, "신호") || strings.HasPrefix(suffix, "실시간신호") || strings.HasPrefix(suffix, "결과")) {
			continue
		}
		if (seconds == "10" || seconds == "30" || seconds == "60") &&
			(strings.HasPrefix(suffix, "흐름") || strings.HasPrefix(suffix, "실시간흐름")) {
			continue
		}
		return true
	}
	return false
}

func marketQuestionExplicitKRWOnly(question string) bool {
	compact := strings.ToUpper(strings.Join(strings.Fields(question), ""))
	for _, phrase := range []string{"KRW마켓", "KRW시장", "KRW만", "KRW기준", "KRW범위", "원화마켓", "원화시장", "원화거래만", "원화거래대금만"} {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}

func marketQuestionRolling24hMetricLabel(question string, duration marketQuestionDuration) bool {
	if duration.unit != marketanalysis.MarketQuestionTimeHour || duration.value != 24 {
		return false
	}
	suffix := strings.TrimSpace(question[duration.end:])
	// "공식 24시간 거래대금" names the ticker's rolling metric, not a
	// request to compare two endpoints. Keep other explicit windows intact.
	prefix := strings.TrimSpace(question[:duration.start])
	if (strings.HasPrefix(suffix, "거래대금") || strings.HasPrefix(suffix, "거래금액")) &&
		(strings.HasSuffix(prefix, "공식") || strings.HasSuffix(strings.ToLower(prefix), "rolling")) {
		return true
	}
	if strings.HasPrefix(suffix, "점유율") || strings.HasPrefix(suffix, "이동점유율") {
		return true
	}
	compact := strings.ReplaceAll(strings.ToLower(question), " ", "")
	explicitInterval := strings.Contains(compact, "24시간동안") ||
		strings.Contains(compact, "24시간전보다") || strings.Contains(compact, "직전24시간")
	return !explicitInterval && marketQuestionAsksMovement(question)
}

func durationSeconds(duration marketQuestionDuration) int64 {
	if duration.unit == marketanalysis.MarketQuestionTimeHour {
		return duration.value * 3_600
	}
	return duration.value * 60
}

func applyMarketQuestionDuration(
	result *marketanalysis.MarketQuestionQuery,
	duration marketQuestionDuration,
	question string,
) {
	result.TimeValue, result.TimeUnit = duration.value, duration.unit
	if marketQuestionUsesWebSocketInterval(duration, question) {
		result.Intent = marketanalysis.MarketQuestionWSIntervalCompare
		if result.Asset != "" {
			result.Intent = marketanalysis.MarketQuestionAssetIntervalAnalysis
		}
		result.Comparison = marketanalysis.MarketQuestionCurrentVsPreviousInterval
		return
	}
	result.Intent = marketanalysis.MarketQuestionOfficialWindowChange
	if result.Asset != "" {
		result.Intent = marketanalysis.MarketQuestionAssetIntervalAnalysis
	}
	result.Comparison = marketanalysis.MarketQuestionRolling24hEndpointDelta
}

func marketQuestionUsesWebSocketInterval(duration marketQuestionDuration, question string) bool {
	compact := strings.ReplaceAll(strings.ToLower(question), " ", "")
	for _, phrase := range []string{"체결", "거래에서", "거래구간", "직전동일", "직전"} {
		if strings.Contains(compact, phrase) {
			return true
		}
	}
	// A whole-hour official endpoint window does not turn into a different
	// evidence layer merely because the user expresses two hours as 120 minutes.
	if marketQuestionExplicitOfficialWindow(question) && durationSeconds(duration)%3_600 == 0 {
		return false
	}
	if duration.unit == marketanalysis.MarketQuestionTimeMinute && duration.value != 60 {
		return true
	}
	return false
}

func marketQuestionExplicitOfficialWindow(question string) bool {
	compact := strings.Join(strings.Fields(question), "")
	// Official minute candles describe interval amounts, not rolling-24h
	// aligned endpoints. Preserve a separate explicit official-share request.
	compact = strings.NewReplacer("공식분봉", "분봉", "공식봉", "봉").Replace(compact)
	return strings.Contains(compact, "공식") && strings.Contains(compact, "점유율")
}

// "오늘 현재 ... 최근 30분" uses today to anchor now, not as a second
// calendar-day query. Never drop a historical date, an exact clock range, or
// an explicit day-close/path request. The same rule owns parser and planner.
func marketQuestionHasCurrentReferenceDate(question string, dates []time.Time, reference time.Time) bool {
	if len(dates) != 1 || !marketQuestionExplicitCurrentFollowUp(question) ||
		dates[0].Format("2006-01-02") != reference.In(marketQuestionKSTLocation).Format("2006-01-02") ||
		len(marketQuestionClockPattern.FindAllStringSubmatch(question, -1)) > 0 ||
		marketQuestionUnsupportedKoreanClockPattern.MatchString(question) {
		return false
	}
	return !containsAnyString(question, "하루", "마감", "일별", "날짜별", "부터")
}

// A requested elapsed KST day is an exact observed interval, not today's
// calendar close or a rolling-24h snapshot. Keep the existing inclusive-second
// range contract and stop at the last closed second of the server reference.
func parseMarketQuestionTodayToNow(question string, dates []time.Time, reference time.Time) (time.Time, time.Time, bool, error) {
	compact := strings.Join(strings.Fields(question), "")
	if !strings.Contains(compact, "오늘") || !strings.Contains(compact, "부터") ||
		!containsAnyString(compact, "지금까지", "현재까지") {
		return time.Time{}, time.Time{}, false, nil
	}
	if len(dates) != 1 || dates[0].Format("2006-01-02") != reference.In(marketQuestionKSTLocation).Format("2006-01-02") {
		return time.Time{}, time.Time{}, true, errAmbiguousMarketQuestionDate
	}
	if len(marketQuestionMidnightStartPattern.FindAllStringIndex(compact, -1)) != 1 {
		return time.Time{}, time.Time{}, true, errInvalidMarketQuestionWindow
	}
	clocks := marketQuestionClockPattern.FindAllStringSubmatch(question, -1)
	if len(clocks) > 1 || len(clocks) == 1 && (clocks[0][1] != "00" || clocks[0][2] != "00" || clocks[0][3] != "" && clocks[0][3] != "00") {
		return time.Time{}, time.Time{}, true, errAmbiguousMarketQuestionWindow
	}
	_, relative, err := parseMarketQuestionDuration(marketQuestionResolutionPattern.ReplaceAllString(question, ""))
	if err != nil || relative {
		return time.Time{}, time.Time{}, true, errAmbiguousMarketQuestionWindow
	}
	start := dates[0].In(marketQuestionKSTLocation)
	end := reference.In(marketQuestionKSTLocation).Truncate(time.Second).Add(-time.Second)
	if !end.After(start) || end.Sub(start) >= 24*time.Hour {
		return time.Time{}, time.Time{}, true, errInvalidMarketQuestionWindow
	}
	return start, end, true, nil
}

func parseMarketQuestionAsset(question string, aliases map[string]string) (string, error) {
	assets, err := parseMarketQuestionAssets(question, aliases)
	if err != nil {
		return "", err
	}
	if len(assets) > 1 {
		return "", errAmbiguousMarketQuestionAsset
	}
	if len(assets) == 1 {
		return assets[0], nil
	}
	return "", nil
}

func parseMarketQuestionAssets(question string, aliases map[string]string) ([]string, error) {
	upperQuestion := strings.ToUpper(question)
	found := map[string]struct{}{}
	excluded := false
	// Only an explicit symbol + coin/token slot (in either word order) is an
	// unknown-asset claim. The prefixed form requires a separate noun, so
	// explanatory text such as "코인별 share" is not interpreted as a symbol.
	// Ordinary English explanatory words must not invent an asset target. An
	// unknown explicit target cannot fall back to an unrelated overall share.
	slots := marketQuestionExplicitAssetPattern.FindAllStringSubmatchIndex(upperQuestion, -1)
	slots = append(slots, marketQuestionPrefixedAssetPattern.FindAllStringSubmatchIndex(upperQuestion, -1)...)
	for _, match := range slots {
		if marketQuestionTargetIsCorrected(upperQuestion[match[1]:]) {
			excluded = true
			continue
		}
		symbol := upperQuestion[match[2]:match[3]]
		asset := ""
		for alias, canonical := range aliases {
			if strings.EqualFold(strings.TrimSpace(alias), symbol) || strings.EqualFold(strings.TrimSpace(canonical), symbol) {
				asset = strings.ToUpper(strings.TrimSpace(canonical))
				break
			}
		}
		if asset == "" {
			return nil, errUnknownMarketQuestionAsset
		}
		found[asset] = struct{}{}
	}
	keys := make([]string, 0, len(aliases))
	for alias := range aliases {
		if strings.TrimSpace(alias) != "" {
			keys = append(keys, alias)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len([]rune(keys[i])) > len([]rune(keys[j])) })
	for _, alias := range keys {
		present, negated := marketQuestionTargetAliasPresent(upperQuestion, strings.ToUpper(alias))
		excluded = excluded || negated
		if !present {
			continue
		}
		asset := strings.ToUpper(strings.TrimSpace(aliases[alias]))
		if asset != "" {
			found[asset] = struct{}{}
		}
	}
	if len(found) == 0 && excluded {
		return nil, errAmbiguousMarketQuestionAsset
	}
	assets := make([]string, 0, len(found))
	for asset := range found {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	return assets, nil
}

// A directly corrected mention ("BTC 말고 ETH") is not a second requested
// target. Positive mentions elsewhere still count, and callers reject an
// excluded target without one unambiguous replacement rather than defaulting.
func marketQuestionTargetAliasPresent(question, alias string) (present, excluded bool) {
	if alias == "" {
		return false, false
	}
	asciiTicker := true
	for index := 0; index < len(alias); index++ {
		if !marketQuestionASCIIAlphaNumeric(alias[index]) {
			asciiTicker = false
			break
		}
	}
	for offset := 0; offset <= len(question)-len(alias); {
		index := strings.Index(question[offset:], alias)
		if index < 0 {
			break
		}
		index += offset
		leftOK := !asciiTicker || index == 0 || !marketQuestionASCIIAlphaNumeric(question[index-1])
		right := index + len(alias)
		rightOK := !asciiTicker || right == len(question) || !marketQuestionASCIIAlphaNumeric(question[right])
		if leftOK && rightOK {
			if marketQuestionTargetIsCorrected(question[right:]) {
				excluded = true
			} else {
				present = true
			}
		}
		offset = index + len(alias)
	}
	return present, excluded
}

func marketQuestionTargetIsCorrected(suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	for _, particle := range []string{"", "은", "는", "이", "가", "을", "를"} {
		if !strings.HasPrefix(suffix, particle) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(suffix, particle))
		// "말고도" is additive, not an exclusion.
		if strings.HasPrefix(rest, "말고") && !strings.HasPrefix(rest, "말고도") ||
			strings.HasPrefix(rest, "아니라") || strings.HasPrefix(rest, "대신") {
			return true
		}
	}
	return false
}

func marketQuestionASCIIAlphaNumeric(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func marketQuestionAssetMetric(question string) marketanalysis.MarketQuestionMetric {
	metric, _ := marketQuestionAssetMetricMeaning(question)
	return metric
}

func marketQuestionAssetMetricMeaning(question string) (marketanalysis.MarketQuestionMetric, bool) {
	compact := strings.ReplaceAll(strings.ToLower(question), " ", "")
	internal := strings.Contains(compact, "빗썸내") || strings.Contains(compact, "빗썸안")
	captureText := strings.ReplaceAll(compact, "빗썸안에서", "")
	// An explicit all-asset denominator is not the domestic total of this
	// particular coin. Weak wording such as "안에서" cannot override it.
	allAssets := false
	for _, clause := range strings.FieldsFunc(compact, func(r rune) bool {
		return r == '.' || r == '?' || r == '!' || r == ';' || r == '\n'
	}) {
		allAssets = allAssets || containsAnyString(clause, "국내모든코인", "국내전체코인", "국내모든자산", "국내전체자산") &&
			containsAnyString(clause, "거래금액", "거래대금") &&
			containsAnyString(clause, "분모", "안에서", "대비", "비중", "비율", "차지", "나눈", "나눠", "나누")
	}
	capture := containsAnyString(captureText, "포착률", "국내동일코인", "국내같은코인") ||
		!allAssets && containsAnyString(captureText, "코인안", "안에서", "어느거래소", "빗썸비중")
	contribution := allAssets
	for _, clause := range strings.FieldsFunc(compact, func(r rune) bool {
		return r == '.' || r == '?' || r == '!' || r == ';' || r == '\n'
	}) {
		contribution = contribution || strings.Contains(clause, "기여") && strings.Contains(clause, "전체점유율")
	}
	// Explicit ratio language wins over a generic contribution word in an
	// auxiliary explanation (for example how price contributes to turnover).
	// If several actual denominators are requested, preserve all via combined.
	selected := make([]marketanalysis.MarketQuestionMetric, 0, 3)
	if internal {
		selected = append(selected, marketanalysis.MarketQuestionMetricAssetInternal)
	}
	if capture {
		selected = append(selected, marketanalysis.MarketQuestionMetricAssetCapture)
	}
	if contribution {
		selected = append(selected, marketanalysis.MarketQuestionMetricContribution)
	}
	if len(selected) == 1 {
		return selected[0], true
	}
	if len(selected) == 0 && strings.Contains(compact, "기여") {
		return marketanalysis.MarketQuestionMetricContribution, true
	}
	return marketanalysis.MarketQuestionMetricAssetCombined, len(selected) > 1
}

func marketQuestionExplanations(question string) []string {
	requested := map[string]bool{}
	if strings.Contains(question, "왜") || strings.Contains(question, "이유") {
		requested["DRIVERS"], requested["EXCHANGES"], requested["QUALITY"] = true, true, true
	}
	// Detailed market language must survive the deterministic fallback path.
	// A structured planner can select attribution and path tools directly, but
	// long relative windows may intentionally fall back to this parser.  Keep
	// the user's requested analytical perspectives instead of reducing such a
	// question to a bare endpoint comparison.
	if containsAnyString(question,
		"어떤 코인", "코인별", "기여", "주도", "견인", "영향", "등락", "오르고", "내리고",
	) {
		requested["DRIVERS"], requested["EXCHANGES"], requested["QUALITY"] = true, true, true
	}
	if containsAnyString(question, "추이", "흐름", "고점", "저점", "최고", "최저", "급등", "급락") {
		requested["PATH"], requested["QUALITY"] = true, true
	}
	priceRelationRequested := containsAnyString(question, "가격", "상관", "등하락") ||
		strings.Contains(question, "관련") && containsAnyString(question, "코인 상승", "코인 하락", "코인 등락")
	if priceRelationRequested {
		requested["PRICE"], requested["DRIVERS"], requested["PATH"], requested["QUALITY"] = true, true, true, true
	}
	if marketQuestionRequestsNotices(question) {
		requested["NOTICES"] = true
	}
	if strings.Contains(strings.ReplaceAll(question, "신호가", "신호"), "호가") || strings.Contains(question, "슬리피지") {
		requested["ORDERBOOK"] = true
	}
	compact := strings.ReplaceAll(question, " ", "")
	if containsAnyString(compact, "지금도", "현재도", "실시간", "방금", "현재흐름", "최근흐름", "단기흐름", "10초신호", "10초결과") {
		requested["REALTIME"] = true
	}
	if strings.Contains(question, "계속") || strings.Contains(question, "한 번에") || strings.Contains(question, "한번에") ||
		strings.Contains(question, "점진") || strings.Contains(question, "경로") || strings.Contains(question, "집중") {
		requested["PATH"] = true
	}
	order := []string{"DRIVERS", "EXCHANGES", "PATH", "PRICE", "REALTIME", "ORDERBOOK", "NOTICES", "QUALITY"}
	result := make([]string, 0, len(requested))
	for _, item := range order {
		if requested[item] {
			result = append(result, item)
		}
	}
	return result
}

func marketQuestionRequestsNotices(question string) bool {
	for _, marker := range []string{
		"공지", "이벤트", "상장", "거래지원", "입출금", "유의종목", "이슈",
	} {
		if strings.Contains(question, marker) {
			return true
		}
	}
	return false
}

func marketQuestionAsksMovement(question string) bool {
	return strings.Contains(question, "왜") || strings.Contains(question, "이유") ||
		strings.Contains(question, "상승") || strings.Contains(question, "하락") ||
		strings.Contains(question, "변화")
}
