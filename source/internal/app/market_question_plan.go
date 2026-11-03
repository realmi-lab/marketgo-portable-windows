package app

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/marketquestion"
)

func marketQuestionQueryFromPlan(
	plan hermesrunner.MarketQuestionPlan,
	assetAliases map[string]string,
	reference time.Time,
) (marketanalysis.MarketQuestionQuery, error) {
	query := marketanalysis.MarketQuestionQuery{
		Intent:        marketanalysis.MarketQuestionCurrentOfficialShare,
		Comparison:    marketanalysis.MarketQuestionCurrentSnapshot,
		TimeUnit:      marketanalysis.MarketQuestionTimeNone,
		Metric:        marketanalysis.MarketQuestionMetricOverallShare,
		MarketScope:   strings.ToUpper(strings.TrimSpace(plan.MarketScope)),
		EndPolicy:     "LATEST_CLOSED_BOUNDARY",
		AnalysisDepth: strings.ToUpper(strings.TrimSpace(plan.AnswerDepth)),
		QuestionParts: append([]string(nil), plan.QuestionParts...),
	}
	if query.MarketScope == "" {
		query.MarketScope = "KRW"
	}
	if query.MarketScope != "KRW" && query.MarketScope != "ALL_DOMESTIC_CONVERTED" {
		return query, errors.New("planner selected an unsupported market scope")
	}
	if query.AnalysisDepth != "BRIEF" && query.AnalysisDepth != "STANDARD" && query.AnalysisDepth != "DETAILED" {
		return query, errors.New("planner selected an unsupported answer depth")
	}
	tools, err := normalizeMarketQuestionPlanTools(plan.EvidenceTools)
	if err != nil {
		return query, err
	}
	query.RequestedExplanation = marketQuestionExplanationFromTools(tools)
	query.Exchange = strings.ToLower(strings.TrimSpace(plan.Exchange))
	if query.Exchange != "" && !isDomesticRealtimeExchange(query.Exchange) {
		return query, errors.New("planner selected an unsupported exchange")
	}
	query.Asset = strings.ToUpper(strings.TrimSpace(plan.Asset))
	if query.Asset != "" {
		canonical := strings.ToUpper(strings.TrimSpace(assetAliases[query.Asset]))
		if canonical == "" {
			return query, errors.New("planner selected an asset outside the registered market universe")
		}
		query.Asset = canonical
	}
	metric, _, err := marketQuestionMetricFromPlan(plan.Metric, query.Asset, query.Exchange)
	if err != nil {
		return query, err
	}
	query.Metric = metric
	requestedComparison, comparisonExplicit, err := marketQuestionComparisonFromPlan(plan.Comparison)
	if err != nil {
		return query, err
	}
	rangeKind := marketquestion.RangeKind(strings.ToUpper(strings.TrimSpace(plan.RangeKind)))
	rangeRoute, err := validateMarketQuestionPlanRange(rangeKind, tools)
	if err != nil {
		return query, err
	}
	query.Comparison = rangeRoute.ExpectedComparison

	switch rangeKind {
	case marketquestion.RangeCurrent:
		if query.Asset != "" {
			query.Intent = marketanalysis.MarketQuestionAssetCapture
		}
	case marketquestion.RangeCalendarDayKST:
		date, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(plan.CalendarDateKST), marketQuestionKSTLocation)
		if parseErr != nil {
			return query, errors.New("planner returned an invalid KST calendar date")
		}
		query.Intent = marketanalysis.MarketQuestionCalendarDayShare
		query.EndPolicy = "EXACT_CALENDAR_DAY_END_KST"
		query.TargetDate = date.Format("2006-01-02")
		query.TargetStartMS, query.TargetEndMS = date.UnixMilli(), date.AddDate(0, 0, 1).UnixMilli()
	case marketquestion.RangeCalendarEndpointSeriesKST:
		if len(plan.CalendarDatesKST) < 2 || len(plan.CalendarDatesKST) > marketQuestionMaxDailyEndpointDates {
			return query, errors.New("planner returned an invalid daily endpoint date count")
		}
		dates := make([]time.Time, 0, len(plan.CalendarDatesKST))
		for _, rawDate := range plan.CalendarDatesKST {
			date, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(rawDate), marketQuestionKSTLocation)
			if parseErr != nil {
				return query, errors.New("planner returned an invalid KST endpoint date")
			}
			dates = append(dates, date)
		}
		dates, err = normalizeMarketQuestionCalendarDates(dates)
		if err != nil || len(dates) != len(plan.CalendarDatesKST) {
			return query, errors.New("planner returned invalid or duplicate KST endpoint dates")
		}
		if err := applyMarketQuestionDailyEndpointSeries(&query, dates, reference); err != nil {
			return query, err
		}
		query.RequestedExplanation = mergeMarketQuestionExplanations(
			query.RequestedExplanation,
			[]string{"DRIVERS", "EXCHANGES", "PATH"},
		)
	case marketquestion.RangeExactKST:
		start, startErr := parsePlannerKST(strings.TrimSpace(plan.StartKST))
		end, endErr := parsePlannerKST(strings.TrimSpace(plan.EndKST))
		if startErr != nil || endErr != nil || !end.After(start) || end.Sub(start) > 24*time.Hour {
			return query, errors.New("planner returned an invalid exact KST range")
		}
		if end.After(reference.Add(30 * time.Second)) {
			return query, errors.New("planner selected a future range")
		}
		query.Intent = marketanalysis.MarketQuestionHistoricalRange
		query.EndPolicy = "EXACT_USER_TIME_RANGE_KST"
		query.TargetDate = start.In(marketQuestionKSTLocation).Format("2006-01-02")
		query.TargetStartMS, query.TargetEndMS = start.UnixMilli(), end.UnixMilli()
		query.RequestedPathSec = plan.PathResolutionSec
		if tools[marketquestion.ToolWebsocketTimePath] {
			if !marketquestion.SupportsPathResolution(query.RequestedPathSec) {
				return query, errors.New("planner selected an unsupported path resolution")
			}
		} else if query.RequestedPathSec != 0 {
			return query, errors.New("planner selected a path resolution without the path tool")
		}
	case marketquestion.RangeRelativeWindow:
		maximumWindowSec := int64(86_400)
		if tools[marketquestion.ToolOfficialAlignedWindow] {
			maximumWindowSec = marketQuestionMaxOfficialWindowHours * 3_600
		}
		if plan.WindowSec < 10 || plan.WindowSec > maximumWindowSec {
			return query, errors.New("planner selected an invalid relative window")
		}
		if tools[marketquestion.ToolOfficialAlignedWindow] {
			if plan.WindowSec%3_600 != 0 {
				return query, errors.New("official aligned windows require whole hours")
			}
			query.Intent = marketanalysis.MarketQuestionOfficialWindowChange
			query.TimeValue, query.TimeUnit = plan.WindowSec/3_600, marketanalysis.MarketQuestionTimeHour
			query.EndPolicy = "LATEST_EXACT_ALIGNED_HOUR"
		} else {
			if plan.WindowSec%60 != 0 {
				return query, errors.New("observed relative windows require whole minutes")
			}
			query.Intent = marketanalysis.MarketQuestionWSIntervalCompare
			query.TimeValue, query.TimeUnit = plan.WindowSec/60, marketanalysis.MarketQuestionTimeMinute
			if plan.WindowSec%3_600 == 0 {
				query.TimeValue, query.TimeUnit = plan.WindowSec/3_600, marketanalysis.MarketQuestionTimeHour
			}
			query.EndPolicy = "LATEST_CLOSED_MINUTE_BOUNDARY"
		}
		if query.Asset != "" {
			query.Intent = marketanalysis.MarketQuestionAssetIntervalAnalysis
		}
	default:
		return query, errors.New("planner selected an unsupported range kind")
	}
	if comparisonExplicit && query.Comparison != requestedComparison {
		return query, fmt.Errorf(
			"planner comparison %q is incompatible with range kind %q",
			requestedComparison, rangeKind,
		)
	}
	if err := validateMarketQuestionPlanMetricRange(query.Metric, rangeKind, tools); err != nil {
		return query, err
	}
	if err := validateMarketQuestionQuery(query, reference); err != nil {
		return query, err
	}
	return query, nil
}

func marketQuestionMetricFromPlan(
	raw, asset, exchange string,
) (marketanalysis.MarketQuestionMetric, bool, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	explicit := value != ""
	if value == "" {
		switch {
		case asset != "":
			value = string(marketanalysis.MarketQuestionMetricAssetCombined)
		case exchange != "" && exchange != string(domain.ExchangeBithumb):
			value = string(marketanalysis.MarketQuestionMetricTargetExchange)
		default:
			value = string(marketanalysis.MarketQuestionMetricOverallShare)
		}
	}
	metric := marketquestion.MetricID(value)
	definition, ok := marketquestion.Metric(metric)
	if !ok {
		return "", true, fmt.Errorf("planner selected unsupported metric %q", value)
	}
	switch definition.Asset {
	case marketquestion.AssetForbidden:
		if asset != "" {
			return "", true, errors.New("selected metric does not accept an asset")
		}
	case marketquestion.AssetRequired:
		if asset == "" {
			return "", true, errors.New("asset metric requires one registered asset")
		}
	}
	switch definition.Exchange {
	case marketquestion.ExchangeEmptyOrBithumb:
		if exchange != "" && exchange != string(domain.ExchangeBithumb) {
			return "", true, errors.New("Bithumb metric is incompatible with a non-Bithumb exchange")
		}
	case marketquestion.ExchangeRequiredNonBithumb:
		if exchange == "" || exchange == string(domain.ExchangeBithumb) {
			return "", true, errors.New("target exchange metric requires one non-Bithumb exchange and no asset")
		}
	}
	return metric, explicit, nil
}

// validateMarketQuestionQuery is the common admission boundary after either
// planner or deterministic parsing. A fallback must not bypass the catalog or
// turn an unsupported target/window into a different supported calculation.
func validateMarketQuestionQuery(query marketanalysis.MarketQuestionQuery, reference time.Time) error {
	if len(query.ComparedAssets) > 0 {
		if len(query.ComparedAssets) < 2 || len(query.ComparedAssets) > 4 || query.Intent != marketanalysis.MarketQuestionAssetCapture || query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot || query.MarketScope != "KRW" || query.PeerExchange != "" || query.Exchange != "" && query.Exchange != "bithumb" || query.Asset != query.ComparedAssets[0] {
			return errors.New("unsupported current asset comparison")
		}
		seen := map[string]bool{}
		for _, asset := range query.ComparedAssets {
			if asset == "" || asset != strings.ToUpper(asset) || seen[asset] {
				return errors.New("invalid compared assets")
			}
			seen[asset] = true
		}
	}
	currentPair := query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare && query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.PeerExchange == "bithumb" && query.MarketScope == "KRW"
	if query.PeerExchange != "" && (!isDomesticRealtimeExchange(query.PeerExchange) || !isDomesticRealtimeExchange(query.Exchange) || query.PeerExchange == query.Exchange || (!currentPair && (query.Intent != marketanalysis.MarketQuestionWSIntervalCompare || query.Comparison != marketanalysis.MarketQuestionCurrentVsPreviousInterval)) || query.Asset != "") {
		return errors.New("unsupported same-window coin pair")
	}
	if query.MarketScope != "KRW" && query.MarketScope != "ALL_DOMESTIC_CONVERTED" {
		return errors.New("unsupported query market scope")
	}
	if query.Exchange != "" && !isDomesticRealtimeExchange(query.Exchange) {
		return errors.New("unsupported query exchange")
	}
	if query.Metric == "" {
		return errors.New("query metric must be resolved before admission")
	}
	if _, _, err := marketQuestionMetricFromPlan(string(query.Metric), query.Asset, query.Exchange); err != nil {
		return err
	}
	validIntent := false
	switch query.Intent {
	case marketanalysis.MarketQuestionCurrentOfficialShare, marketanalysis.MarketQuestionMovementExplanation:
		validIntent = query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.Asset == ""
	case marketanalysis.MarketQuestionAssetCapture:
		validIntent = query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot && query.Asset != ""
	case marketanalysis.MarketQuestionOfficialWindowChange:
		validIntent = query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta && query.Asset == ""
	case marketanalysis.MarketQuestionWSIntervalCompare:
		validIntent = query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval && query.Asset == ""
	case marketanalysis.MarketQuestionAssetIntervalAnalysis:
		validIntent = query.Asset != "" && (query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta || query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval)
	case marketanalysis.MarketQuestionNoticeReaction:
		validIntent = query.Comparison == marketanalysis.MarketQuestionCurrentSnapshot || query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta || query.Comparison == marketanalysis.MarketQuestionCurrentVsPreviousInterval
	case marketanalysis.MarketQuestionCalendarDayShare:
		validIntent = query.Comparison == marketanalysis.MarketQuestionCalendarDayClose
	case marketanalysis.MarketQuestionDailyEndpointSeries:
		validIntent = query.Comparison == marketanalysis.MarketQuestionDailyRolling24hEndpoints
	case marketanalysis.MarketQuestionHistoricalRange:
		validIntent = query.Comparison == marketanalysis.MarketQuestionFixedHistoricalInterval
	}
	if !validIntent {
		return errors.New("query intent does not match its comparison and asset")
	}
	tools := map[marketquestion.EvidenceToolID]bool{}
	var kind marketquestion.RangeKind
	switch query.Comparison {
	case marketanalysis.MarketQuestionCurrentSnapshot:
		kind = marketquestion.RangeCurrent
		if query.TimeValue != 0 || query.TargetStartMS != 0 || query.TargetEndMS != 0 {
			return errors.New("current query contains a historical window")
		}
	case marketanalysis.MarketQuestionCalendarDayClose:
		kind = marketquestion.RangeCalendarDayKST
		start := time.UnixMilli(query.TargetStartMS).In(marketQuestionKSTLocation)
		if query.Intent != marketanalysis.MarketQuestionCalendarDayShare ||
			start.Format("15:04:05.000") != "00:00:00.000" ||
			query.TargetDate != start.Format("2006-01-02") ||
			query.TargetEndMS != start.AddDate(0, 0, 1).UnixMilli() ||
			start.After(calendarDateAtOffset(reference.In(marketQuestionKSTLocation), 0)) {
			return errors.New("invalid calendar-day query bounds")
		}
	case marketanalysis.MarketQuestionDailyRolling24hEndpoints:
		kind = marketquestion.RangeCalendarEndpointSeriesKST
		if err := validateMarketQuestionDailyEndpointScope(query); err != nil {
			return err
		}
		if _, err := marketQuestionDailyEndpointBuckets(query); err != nil {
			return err
		}
		latestSafe := reference.In(marketQuestionKSTLocation).Add(-2 * time.Minute).Truncate(time.Hour)
		if query.TargetEndMS-query.TargetStartMS > (31*24*time.Hour).Milliseconds() || query.TargetEndMS > latestSafe.UnixMilli() {
			return errors.New("daily endpoints exceed their retained range or safe boundary")
		}
	case marketanalysis.MarketQuestionFixedHistoricalInterval:
		kind = marketquestion.RangeExactKST
		if query.Intent != marketanalysis.MarketQuestionHistoricalRange ||
			query.TargetStartMS <= 0 || query.TargetEndMS <= query.TargetStartMS ||
			query.TargetStartMS%1_000 != 0 || query.TargetEndMS%1_000 != 0 ||
			query.TargetEndMS > reference.Add(30*time.Second).UnixMilli() {
			return errors.New("invalid exact query bounds")
		}
		if query.RequestedPathSec != 0 && !marketquestion.SupportsPathResolution(query.RequestedPathSec) {
			return errors.New("unsupported exact query path resolution")
		}
		if query.EndPolicy == "LATEST_CLOSED_MINUTE_30D_RANGE" {
			// This pre-existing repository route is deliberately not a new public
			// catalog capability. Preserve only its exact 30-day, asset-free KRW
			// contract; arbitrary non-Bithumb exact ranges remain unsupported.
			end := reference.In(marketQuestionKSTLocation).Truncate(time.Minute).Add(-time.Second)
			start := end.Add(-30 * 24 * time.Hour).Add(time.Second)
			if query.Asset != "" || query.MarketScope != "KRW" || query.RequestedPathSec != 3_600 ||
				query.TargetStartMS != start.UnixMilli() || query.TargetEndMS != end.UnixMilli() {
				return errors.New("invalid 30-day historical query contract")
			}
			return nil
		}
		if query.TargetEndMS-query.TargetStartMS+1_000 > 86_400_000 {
			return errors.New("exact query exceeds the inclusive 24-hour limit")
		}
	case marketanalysis.MarketQuestionRolling24hEndpointDelta, marketanalysis.MarketQuestionCurrentVsPreviousInterval:
		kind = marketquestion.RangeRelativeWindow
		if query.TimeValue <= 0 || (query.TimeUnit != marketanalysis.MarketQuestionTimeMinute && query.TimeUnit != marketanalysis.MarketQuestionTimeHour) {
			return errors.New("relative query requires a positive whole-minute/hour window")
		}
		maximum := marketQuestionMaxWSIntervalSec
		if query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta {
			maximum = marketQuestionMaxOfficialWindowHours * 3_600
			tools[marketquestion.ToolOfficialAlignedWindow] = true
		}
		unitSec := int64(60)
		if query.TimeUnit == marketanalysis.MarketQuestionTimeHour {
			unitSec = 3_600
		}
		if query.TimeValue > maximum/unitSec ||
			query.Comparison == marketanalysis.MarketQuestionRolling24hEndpointDelta && query.TimeValue*unitSec%3_600 != 0 {
			return errors.New("relative query exceeds its evidence range or resolution")
		}
	default:
		return errors.New("unsupported query comparison")
	}
	if query.MarketScope == "ALL_DOMESTIC_CONVERTED" &&
		query.Comparison != marketanalysis.MarketQuestionCurrentVsPreviousInterval &&
		query.Comparison != marketanalysis.MarketQuestionFixedHistoricalInterval {
		return errors.New("converted scope is unsupported by the selected official evidence")
	}
	return validateMarketQuestionPlanMetricRange(query.Metric, kind, tools)
}

func marketQuestionComparisonFromPlan(
	raw string,
) (marketanalysis.MarketQuestionComparison, bool, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if value == "" {
		return "", false, nil
	}
	comparison := marketquestion.ComparisonID(value)
	_, ok := marketquestion.Comparison(comparison)
	if !ok {
		return "", true, fmt.Errorf("planner selected unsupported comparison %q", value)
	}
	return comparison, true, nil
}

func validateMarketQuestionPlanMetricRange(
	metric marketanalysis.MarketQuestionMetric,
	rangeKind marketquestion.RangeKind,
	tools map[marketquestion.EvidenceToolID]bool,
) error {
	if marketquestion.MetricAllowsRange(metric, rangeKind, tools) {
		return nil
	}
	return fmt.Errorf("planner metric %q is incompatible with range kind %q", metric, rangeKind)
}

func validateMarketQuestionPlanRange(
	rangeKind marketquestion.RangeKind,
	tools map[marketquestion.EvidenceToolID]bool,
) (marketquestion.RangeRoute, error) {
	route, ok := marketquestion.ResolveRangeRoute(rangeKind, tools)
	if !ok {
		return marketquestion.RangeRoute{}, errors.New("planner selected an unsupported range kind")
	}
	for _, required := range route.RequiredTools {
		if !tools[required] {
			return marketquestion.RangeRoute{}, fmt.Errorf(
				"planner range kind %q omitted required evidence tool %q",
				rangeKind, required,
			)
		}
	}
	for selected := range tools {
		if !marketquestion.RangeRouteAllowsTool(route, selected) {
			return marketquestion.RangeRoute{}, fmt.Errorf(
				"planner evidence tool %q is incompatible with range kind %q",
				selected, rangeKind,
			)
		}
	}
	return route, nil
}

func validateMarketQuestionPlanExplicitDates(
	question string,
	query marketanalysis.MarketQuestionQuery,
	reference time.Time,
) error {
	if marketQuestionRelativeDaysPattern.MatchString(question) && !marketQuestionRequestsRelativeDailySeries(question) &&
		query.Comparison == marketanalysis.MarketQuestionDailyRolling24hEndpoints {
		return errors.New("planner changed a continuous relative-day window into daily endpoints")
	}
	dates, err := parseMarketQuestionCalendarDates(question, reference)
	if err != nil {
		return err
	}
	if len(dates) < 2 {
		return nil
	}
	expected := marketanalysis.MarketQuestionQuery{}
	if err := applyMarketQuestionDailyEndpointSeries(&expected, dates, reference); err != nil {
		return err
	}
	if query.Intent != marketanalysis.MarketQuestionDailyEndpointSeries ||
		query.Comparison != marketanalysis.MarketQuestionDailyRolling24hEndpoints ||
		len(query.TargetDates) != len(expected.TargetDates) ||
		query.TargetStartMS != expected.TargetStartMS || query.TargetEndMS != expected.TargetEndMS {
		return errors.New("planner did not preserve the explicit daily endpoint series")
	}
	for index := range expected.TargetDates {
		if query.TargetDates[index] != expected.TargetDates[index] {
			return errors.New("planner changed an explicit daily endpoint date")
		}
	}
	requestsRealtime := marketQuestionRequestsDailyRealtime(question)
	selectedRealtime := marketQuestionQueryRequestsExplanation(query, "REALTIME")
	if requestsRealtime && !selectedRealtime {
		return errors.New("planner omitted the requested current realtime comparison")
	}
	if !requestsRealtime && selectedRealtime {
		return errors.New("planner added an unrequested current realtime comparison")
	}
	return nil
}

// Check only targets and bounds stated in this user question. This is not a
// full equality check against the fallback parser: unmentioned context and
// analytical perspectives remain the structured planner's responsibility.
func validateMarketQuestionPlanExplicitTargetsAndWindow(
	question string,
	query marketanalysis.MarketQuestionQuery,
	assetAliases map[string]string,
	reference time.Time,
) error {
	if _, pair := marketQuestionCurrentSharePair(question); pair {
		expected, err := parseMarketQuestionAt(question, assetAliases, reference)
		if err != nil {
			return err
		}
		if query.Intent != expected.Intent || query.Exchange != expected.Exchange || query.PeerExchange != expected.PeerExchange || query.Comparison != expected.Comparison {
			return errors.New("planner changed the explicit current exchange pair")
		}
	}
	if marketQuestionHasUnsupportedSecondWindow(question) {
		return errors.New("second-based relative comparison is unsupported")
	}
	exchange, err := parseMarketQuestionExchange(question)
	if err != nil {
		return err
	}
	plannedExchange := strings.ToLower(strings.TrimSpace(query.Exchange))
	if plannedExchange == "" {
		plannedExchange = string(domain.ExchangeBithumb)
	}
	if exchange != "" && exchange != plannedExchange {
		return errors.New("planner changed or omitted the explicit exchange")
	}
	asset, err := parseMarketQuestionAsset(question, assetAliases)
	if err != nil {
		return err
	}
	if asset != "" {
		if !strings.EqualFold(asset, strings.TrimSpace(query.Asset)) {
			return errors.New("planner changed or omitted the explicit asset")
		}
		metric, explicitMetric := marketQuestionAssetMetricMeaning(question)
		// Combined supplies all three existing asset ratios. A vague asset
		// question does not force that default onto a more specific valid plan.
		if explicitMetric &&
			query.Metric != metric && query.Metric != marketanalysis.MarketQuestionMetricAssetCombined {
			return errors.New("planner changed the explicit asset metric denominator")
		}
	}
	if marketQuestionExplicitAllDomesticConverted(question) && query.MarketScope != "ALL_DOMESTIC_CONVERTED" {
		return errors.New("planner omitted the explicit converted market scope")
	}
	if marketQuestionExplicitKRWOnly(question) && query.MarketScope != "KRW" {
		return errors.New("planner changed the explicit KRW-only market scope")
	}

	dates, err := parseMarketQuestionCalendarDates(question, reference)
	if err != nil {
		return err
	}
	if start, end, requested, err := parseMarketQuestionTodayToNow(question, dates, reference); requested {
		if err != nil {
			return err
		}
		if query.Comparison != marketanalysis.MarketQuestionFixedHistoricalInterval ||
			query.TargetDate != start.Format("2006-01-02") ||
			query.TargetStartMS != start.UnixMilli() || query.TargetEndMS != end.UnixMilli() {
			return errors.New("planner changed the explicit elapsed KST day interval")
		}
		return nil
	}
	currentReferenceDate := marketQuestionHasCurrentReferenceDate(question, dates, reference)
	if currentReferenceDate {
		dates = nil
	}
	if len(dates) == 1 && query.TargetDate != dates[0].Format("2006-01-02") {
		return errors.New("planner changed or omitted the explicit calendar date")
	}
	clockCount := len(marketQuestionClockPattern.FindAllStringSubmatch(question, -1))
	if clockCount == 2 {
		date := reference.In(marketQuestionKSTLocation)
		if len(dates) == 1 {
			date = dates[0]
		}
		start, end, _, err := parseMarketQuestionClockRange(question, date)
		if err != nil {
			return err
		}
		plannedStart := time.UnixMilli(query.TargetStartMS).In(marketQuestionKSTLocation)
		plannedEnd := time.UnixMilli(query.TargetEndMS).In(marketQuestionKSTLocation)
		if query.Comparison != marketanalysis.MarketQuestionFixedHistoricalInterval ||
			plannedStart.Nanosecond() != 0 ||
			plannedStart.Format("15:04:05") != start.Format("15:04:05") ||
			plannedEnd.Sub(plannedStart) != end.Sub(start) ||
			len(dates) == 1 && (!plannedStart.Equal(start) || !plannedEnd.Equal(end)) {
			return errors.New("planner changed or omitted the explicit clock endpoints")
		}
		return nil
	}
	// Date-series and month semantics have their existing dedicated validator.
	// A path resolution such as "10분 단위" is not a second comparison window.
	if len(dates) > 0 || marketQuestionRelativeMonthPattern.MatchString(question) {
		return nil
	}
	periodQuestion := marketQuestionResolutionPattern.ReplaceAllString(question, "")
	duration, found, err := parseMarketQuestionDuration(periodQuestion)
	if err != nil {
		return err
	}
	if found {
		if marketQuestionExplicitOfficialWindow(periodQuestion) &&
			(durationSeconds(duration)%3_600 != 0 || exchange != "" && exchange != string(domain.ExchangeBithumb) || marketQuestionUsesWebSocketInterval(duration, periodQuestion)) {
			return errors.New("explicit official comparison is unsupported by this duration or exchange")
		}
		// The fallback parser reads the first relative-day phrase. A plan may
		// not silently select it when another explicit day window disagrees.
		for _, dayWindow := range marketQuestionRelativeDaysPattern.FindAllString(periodQuestion, -1) {
			other, otherFound, otherErr := parseMarketQuestionDuration(dayWindow)
			if otherErr != nil {
				return otherErr
			}
			if otherFound && durationSeconds(other) != durationSeconds(duration) {
				return errAmbiguousMarketQuestionWindow
			}
		}
		plannedDuration := marketQuestionDuration{value: query.TimeValue, unit: query.TimeUnit}
		if query.TimeValue <= 0 ||
			(query.TimeUnit != marketanalysis.MarketQuestionTimeHour && query.TimeUnit != marketanalysis.MarketQuestionTimeMinute) ||
			durationSeconds(plannedDuration) != durationSeconds(duration) ||
			(query.Comparison != marketanalysis.MarketQuestionCurrentVsPreviousInterval &&
				query.Comparison != marketanalysis.MarketQuestionRolling24hEndpointDelta) {
			return errors.New("planner changed or omitted the explicit relative window")
		}
		if marketQuestionUsesWebSocketInterval(duration, periodQuestion) && query.Comparison != marketanalysis.MarketQuestionCurrentVsPreviousInterval {
			return errors.New("planner replaced an explicit equal-interval comparison with official endpoints")
		}
		if marketQuestionExplicitOfficialWindow(periodQuestion) && !marketQuestionUsesWebSocketInterval(duration, periodQuestion) &&
			query.Comparison != marketanalysis.MarketQuestionRolling24hEndpointDelta {
			return errors.New("planner replaced the explicit official endpoint comparison with observed intervals")
		}
		return nil
	}
	if currentReferenceDate && query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot {
		return errors.New("planner changed the explicitly current reference into a historical query")
	}
	// "지금 지난주와 비교" is not an unambiguous current-only request.
	// Leave unresolved natural-language comparison periods to the planner.
	currentOnly := marketQuestionExplicitCurrentFollowUp(question) &&
		!containsAnyString(question, "최근", "지난", "과거", "비교", "대비", "전과", "전보다")
	if currentOnly && query.Comparison != marketanalysis.MarketQuestionCurrentSnapshot {
		return errors.New("planner replaced an explicit current request with a past window")
	}
	return nil
}

// PRICE is a requested explanatory perspective, not an additional retrieval
// tool. Preserve it from this question only, without dropping planner-specific
// explanations (for example AGGREGATE or HISTORICAL_SIGNALS) or adding reads.
func preserveMarketQuestionExplicitPrice(
	question string,
	query marketanalysis.MarketQuestionQuery,
) marketanalysis.MarketQuestionQuery {
	if !slices.Contains(marketQuestionExplanations(question), "PRICE") || marketQuestionQueryRequestsExplanation(query, "PRICE") {
		return query
	}
	query.RequestedExplanation = append(append([]string(nil), query.RequestedExplanation...), "PRICE")
	return query
}

// The current user question owns explicit changes; the resolved question adds
// only missing scope from prior user questions. Validate both so a valid plan
// cannot replace an inherited "same period" with CURRENT or a new duration.
// resolvedQuestion must come from marketQuestionWithConversationContext, never
// a previous assistant answer or an arbitrary model-generated instruction.
func validateMarketQuestionPlanConversationSemantics(
	question, resolvedQuestion string,
	query marketanalysis.MarketQuestionQuery,
	assetAliases map[string]string,
	reference time.Time,
) error {
	if err := validateMarketQuestionParts(question, query.QuestionParts); err != nil {
		return err
	}
	if err := validateMarketQuestionPlanExplicitSemantics(question, query, assetAliases, reference); err != nil {
		return err
	}
	if strings.TrimSpace(question) == strings.TrimSpace(resolvedQuestion) {
		return nil
	}
	if err := validateMarketQuestionPlanExplicitSemantics(resolvedQuestion, query, assetAliases, reference); err != nil {
		return fmt.Errorf("planner changed the resolved user conversation scope: %w", err)
	}
	return nil
}

func marketQuestionQueryRequestsExplanation(query marketanalysis.MarketQuestionQuery, target string) bool {
	for _, value := range query.RequestedExplanation {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func normalizeMarketQuestionPlanTools(values []string) (map[marketquestion.EvidenceToolID]bool, error) {
	result := make(map[marketquestion.EvidenceToolID]bool, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		tool := marketquestion.EvidenceToolID(value)
		if _, ok := marketquestion.EvidenceTool(tool); !ok {
			return nil, fmt.Errorf("planner selected unsupported tool %q", value)
		}
		result[tool] = true
	}
	if len(result) == 0 {
		return nil, errors.New("planner selected no evidence tools")
	}
	return result, nil
}

func marketQuestionExplanationFromTools(tools map[marketquestion.EvidenceToolID]bool) []string {
	result := []string{}
	seen := map[string]bool{}
	for tool := range tools {
		definition, ok := marketquestion.EvidenceTool(tool)
		if ok && definition.Explanation != "" && !seen[definition.Explanation] {
			result = append(result, definition.Explanation)
			seen[definition.Explanation] = true
		}
	}
	sort.Strings(result)
	return result
}

func parsePlannerKST(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	_, offset := parsed.Zone()
	if offset != 9*60*60 {
		return time.Time{}, errors.New("planner time is not KST")
	}
	return parsed.In(marketQuestionKSTLocation), nil
}

func marketQuestionPlanEvidence(plan hermesrunner.MarketQuestionPlan, source string, query marketanalysis.MarketQuestionQuery) map[string]any {
	tools := make([]string, 0, len(plan.EvidenceTools))
	seen := map[string]bool{}
	for _, value := range plan.EvidenceTools {
		value = strings.ToUpper(strings.TrimSpace(value))
		if _, ok := marketquestion.EvidenceTool(marketquestion.EvidenceToolID(value)); ok && !seen[value] {
			tools, seen[value] = append(tools, value), true
		}
	}
	deterministicSource := strings.HasPrefix(source, "DETERMINISTIC_")
	if deterministicSource && query.Intent == marketanalysis.MarketQuestionOfficialWindowChange {
		for _, tool := range []marketquestion.EvidenceToolID{
			marketquestion.ToolOfficialAlignedWindow,
			marketquestion.ToolExchangeAttribution,
			marketquestion.ToolAssetAttribution,
		} {
			value := string(tool)
			if !seen[value] {
				tools, seen[value] = append(tools, value), true
			}
		}
	}
	if deterministicSource && marketQuestionDailyEndpointSeriesRequested(query) {
		for _, tool := range []marketquestion.EvidenceToolID{
			marketquestion.ToolOfficialDailyEndpointSeries, marketquestion.ToolAssetAttribution,
			marketquestion.ToolExchangeAttribution,
		} {
			value := string(tool)
			if !seen[value] {
				tools, seen[value] = append(tools, value), true
			}
		}
		if marketQuestionQueryRequestsExplanation(query, "REALTIME") && !seen[string(marketquestion.ToolCurrentRealtimeFlowSummary)] {
			value := string(marketquestion.ToolCurrentRealtimeFlowSummary)
			tools = append(tools, value)
			seen[value] = true
		}
	}
	if deterministicSource && query.Intent == marketanalysis.MarketQuestionHistoricalRange {
		for _, tool := range []marketquestion.EvidenceToolID{
			marketquestion.ToolWebsocketRangeAggregate,
			marketquestion.ToolWebsocketTimePath,
			marketquestion.ToolExchangeAttribution,
			marketquestion.ToolAssetAttribution,
		} {
			value := string(tool)
			if !seen[value] {
				tools, seen[value] = append(tools, value), true
			}
		}
	}
	sort.Strings(tools)
	rangeKind := strings.ToUpper(strings.TrimSpace(plan.RangeKind))
	if deterministicSource && query.Intent == marketanalysis.MarketQuestionOfficialWindowChange {
		rangeKind = "RELATIVE_WINDOW"
	}
	if deterministicSource && marketQuestionDailyEndpointSeriesRequested(query) {
		rangeKind = "CALENDAR_ENDPOINT_SERIES_KST"
	}
	if deterministicSource && query.Intent == marketanalysis.MarketQuestionHistoricalRange {
		rangeKind = "EXACT_RANGE_KST"
	}
	return map[string]any{
		"source": source, "planner_model": hermesrunner.MarketQuestionPlannerModel,
		"range_kind":              rangeKind,
		"selected_evidence_tools": tools, "validated_query": query,
		"model_generated_sql": false,
	}
}
