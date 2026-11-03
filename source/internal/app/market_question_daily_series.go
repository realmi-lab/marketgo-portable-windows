package app

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/tickertrend"
)

const (
	marketQuestionDailyDriverLimit    = 5
	marketQuestionDailyPricePathLimit = 20
)

func marketQuestionDailyEndpointSeriesRequested(query marketanalysis.MarketQuestionQuery) bool {
	return query.Intent == marketanalysis.MarketQuestionDailyEndpointSeries &&
		query.Comparison == marketanalysis.MarketQuestionDailyRolling24hEndpoints
}

func marketQuestionDailyRecentIntervalRequested(query marketanalysis.MarketQuestionQuery) bool {
	if !marketQuestionDailyEndpointSeriesRequested(query) {
		return false
	}
	for _, value := range query.RequestedExplanation {
		if strings.EqualFold(strings.TrimSpace(value), "REALTIME") {
			return true
		}
	}
	return false
}

func marketQuestionDailyEndpointBuckets(query marketanalysis.MarketQuestionQuery) ([]int64, error) {
	if !marketQuestionDailyEndpointSeriesRequested(query) ||
		len(query.TargetDates) < 2 || len(query.TargetDates) > marketQuestionMaxDailyEndpointDates ||
		query.TargetEndMS <= 0 {
		return nil, errors.New("invalid daily endpoint series query")
	}
	anchor := time.UnixMilli(query.TargetEndMS).In(marketQuestionKSTLocation)
	result := make([]int64, 0, len(query.TargetDates))
	previousMS := int64(0)
	for _, value := range query.TargetDates {
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), marketQuestionKSTLocation)
		if err != nil {
			return nil, errors.New("invalid daily endpoint date")
		}
		endpoint := time.Date(
			day.Year(), day.Month(), day.Day(), anchor.Hour(), anchor.Minute(), anchor.Second(), 0,
			marketQuestionKSTLocation,
		)
		bucketMS := endpoint.UnixMilli()
		if bucketMS%int64(time.Hour/time.Millisecond) != 0 || bucketMS <= previousMS {
			return nil, errors.New("daily endpoints must be unique ascending aligned hours")
		}
		result = append(result, bucketMS)
		previousMS = bucketMS
	}
	if result[0] != query.TargetStartMS || result[len(result)-1] != query.TargetEndMS {
		return nil, errors.New("daily endpoint query bounds do not match its dates")
	}
	return result, nil
}

func dailyEndpointSeriesFacts(series tickertrend.DailyEndpointSeries) *marketanalysis.MarketQuestionDailyEndpointSeriesFacts {
	result := &marketanalysis.MarketQuestionDailyEndpointSeriesFacts{
		ContractVersion:          series.ContractVersion,
		Status:                   series.Status,
		Reason:                   series.Reason,
		Points:                   make([]marketanalysis.MarketQuestionDailyEndpointPoint, 0, len(series.Points)),
		Transitions:              make([]marketanalysis.MarketQuestionDailyEndpointTransition, 0, len(series.Transitions)),
		PathComplete:             series.PathComplete,
		ExpectedHourlyPointCount: series.ExpectedHourlyPointCount,
		ObservedHourlyPointCount: series.ObservedHourlyPointCount,
		MissingBucketMS:          append([]int64(nil), series.MissingBucketMS...),
		NonLiveBucketMS:          append([]int64(nil), series.NonLiveBucketMS...),
		IncludedExchanges:        append([]string(nil), series.IncludedExchanges...),
		ExcludedExchanges:        cloneDailyStringMap(series.ExcludedExchanges),
	}
	for _, point := range series.Points {
		result.Points = append(result.Points, marketanalysis.MarketQuestionDailyEndpointPoint{
			BucketMS: point.BucketMS, ObservedAtMS: point.ObservedAtMS,
			Status: point.Status, Reason: point.Reason,
			ExactEndpointAvailable: point.ExactEndpointAvailable,
			AssetDetailsAvailable:  point.AssetDetailsAvailable,
			TotalVolumeKRW:         copyQuestionFloat(point.TotalVolumeKRW),
			BithumbVolumeKRW:       copyQuestionFloat(point.BithumbVolumeKRW),
			BithumbSharePct:        copyQuestionFloat(point.BithumbSharePct),
			AssetRowCount:          point.AssetRowCount,
		})
	}
	for _, transition := range series.Transitions {
		result.Transitions = append(result.Transitions, dailyEndpointTransitionFacts(transition))
	}
	result.Cumulative = dailyEndpointTransitionFacts(series.Cumulative)
	result.PricePaths = dailyEndpointPricePaths(series)
	return result
}

func dailyEndpointSeriesFactsWithRecent(
	series tickertrend.DailyEndpointSeries,
	recent *marketanalysis.MarketQuestionIntervalEvidence,
) *marketanalysis.MarketQuestionDailyEndpointSeriesFacts {
	result := dailyEndpointSeriesFacts(series)
	result.RecentInterval = dailyRecentIntervalFacts(recent)
	return result
}

func dailyRecentIntervalFacts(
	evidence *marketanalysis.MarketQuestionIntervalEvidence,
) *marketanalysis.MarketQuestionDailyRecentInterval {
	if evidence == nil {
		return nil
	}
	positive := []marketanalysis.MarketQuestionFactDriver{}
	negative := []marketanalysis.MarketQuestionFactDriver{}
	for _, driver := range evidence.Drivers {
		value := marketanalysis.MarketQuestionFactDriver{
			Asset: driver.Asset, ContributionPP: driver.ContributionPP,
			BithumbVolumeChangeKRW:    driver.BithumbVolumeChangeKRW,
			CompetitorVolumeChangeKRW: driver.CompetitorVolumeChangeKRW,
		}
		if driver.ContributionPP > 0 {
			positive = append(positive, value)
		} else if driver.ContributionPP < 0 {
			negative = append(negative, value)
		}
	}
	sort.SliceStable(positive, func(i, j int) bool { return positive[i].ContributionPP > positive[j].ContributionPP })
	sort.SliceStable(negative, func(i, j int) bool { return negative[i].ContributionPP < negative[j].ContributionPP })
	return &marketanalysis.MarketQuestionDailyRecentInterval{
		Status: evidence.Status, Reason: evidence.Reason, Source: evidence.Source,
		WindowSec: evidence.WindowSec, AsOfMS: evidence.AsOfMS,
		Previous: evidence.Previous, Current: evidence.Current,
		ShareDeltaPP:            copyQuestionFloat(evidence.ShareDeltaPP),
		DomesticVolumeDeltaKRW:  copyQuestionFloat(evidence.DomesticVolumeDeltaKRW),
		BithumbVolumeDeltaKRW:   copyQuestionFloat(evidence.BithumbVolumeDeltaKRW),
		PositiveDrivers:         append([]marketanalysis.MarketQuestionFactDriver(nil), positive[:min(len(positive), marketQuestionDailyDriverLimit)]...),
		NegativeDrivers:         append([]marketanalysis.MarketQuestionFactDriver(nil), negative[:min(len(negative), marketQuestionDailyDriverLimit)]...),
		DriverContributionSumPP: copyQuestionFloat(evidence.DriverContributionSumPP),
		DriverResidualPP:        copyQuestionFloat(evidence.DriverResidualPP),
		CurrentCoverage:         evidence.CurrentCoverage, PreviousCoverage: evidence.PreviousCoverage,
	}
}

func dailyEndpointTransitionFacts(
	transition tickertrend.DailyEndpointTransition,
) marketanalysis.MarketQuestionDailyEndpointTransition {
	positive, negative := selectDailyEndpointDrivers(transition.Drivers, marketQuestionDailyDriverLimit)
	exchanges := make([]marketanalysis.MarketQuestionFactExchangeDriver, 0, min(len(transition.ExchangeDrivers), marketQuestionDailyDriverLimit))
	for _, driver := range transition.ExchangeDrivers[:min(len(transition.ExchangeDrivers), marketQuestionDailyDriverLimit)] {
		exchanges = append(exchanges, marketanalysis.MarketQuestionFactExchangeDriver{
			Exchange: driver.Exchange, ContributionPP: driver.ContributionPP,
			VolumeChangeKRW: driver.VolumeChangeKRW,
		})
	}
	return marketanalysis.MarketQuestionDailyEndpointTransition{
		FromBucketMS: transition.FromBucketMS, ToBucketMS: transition.ToBucketMS,
		WindowSec: transition.WindowSec, Status: transition.Status, Reason: transition.Reason,
		EndpointComparison:        transition.EndpointComparison,
		AssetAttributionAvailable: transition.AssetAttributionAvailable,
		PathComplete:              transition.PathComplete,
		ExpectedHourlyPointCount:  transition.ExpectedHourlyPointCount,
		ObservedHourlyPointCount:  transition.ObservedHourlyPointCount,
		MissingBucketMS:           append([]int64(nil), transition.MissingBucketMS...),
		ShareDeltaPP:              copyQuestionFloat(transition.ShareDeltaPP),
		DomesticVolumeDeltaKRW:    copyQuestionFloat(transition.DomesticVolumeDeltaKRW),
		BithumbVolumeDeltaKRW:     copyQuestionFloat(transition.BithumbVolumeDeltaKRW),
		PositiveDrivers:           positive, NegativeDrivers: negative, ExchangeDrivers: exchanges,
	}
}

func selectDailyEndpointDrivers(
	values []tickertrend.DailyEndpointDriver,
	limit int,
) ([]marketanalysis.MarketQuestionDailyEndpointDriver, []marketanalysis.MarketQuestionDailyEndpointDriver) {
	positiveRaw := make([]tickertrend.DailyEndpointDriver, 0, len(values))
	negativeRaw := make([]tickertrend.DailyEndpointDriver, 0, len(values))
	for _, value := range values {
		switch {
		case value.ContributionPP > 0:
			positiveRaw = append(positiveRaw, value)
		case value.ContributionPP < 0:
			negativeRaw = append(negativeRaw, value)
		}
	}
	sort.SliceStable(positiveRaw, func(i, j int) bool {
		return positiveRaw[i].ContributionPP > positiveRaw[j].ContributionPP
	})
	sort.SliceStable(negativeRaw, func(i, j int) bool {
		return negativeRaw[i].ContributionPP < negativeRaw[j].ContributionPP
	})
	convert := func(values []tickertrend.DailyEndpointDriver) []marketanalysis.MarketQuestionDailyEndpointDriver {
		values = values[:min(len(values), limit)]
		result := make([]marketanalysis.MarketQuestionDailyEndpointDriver, 0, len(values))
		for index, value := range values {
			result = append(result, marketanalysis.MarketQuestionDailyEndpointDriver{
				Rank: index + 1, Asset: value.Asset, ContributionPP: value.ContributionPP,
				BithumbVolumeChangeKRW:      value.BithumbVolumeChangeKRW,
				CompetitorVolumeChangeKRW:   value.CompetitorVolumeChangeKRW,
				PreviousBithumbVolumeKRW:    value.PreviousBithumbVolumeKRW,
				CurrentBithumbVolumeKRW:     value.CurrentBithumbVolumeKRW,
				PreviousCompetitorVolumeKRW: value.PreviousCompetitorVolumeKRW,
				CurrentCompetitorVolumeKRW:  value.CurrentCompetitorVolumeKRW,
				PreviousClosePriceKRW:       copyQuestionFloat(value.PreviousClosePriceKRW),
				CurrentClosePriceKRW:        copyQuestionFloat(value.CurrentClosePriceKRW),
				ClosePriceChangeKRW:         copyQuestionFloat(value.ClosePriceChangeKRW),
				ClosePriceChangePct:         copyQuestionFloat(value.ClosePriceChangePct),
			})
		}
		return result
	}
	return convert(positiveRaw), convert(negativeRaw)
}

func dailyEndpointPricePaths(series tickertrend.DailyEndpointSeries) []marketanalysis.MarketQuestionDailyPricePath {
	assets := []string{}
	seen := map[string]bool{}
	addDrivers := func(values []tickertrend.DailyEndpointDriver) {
		positive, negative := selectDailyEndpointDrivers(values, marketQuestionDailyDriverLimit)
		for _, drivers := range [][]marketanalysis.MarketQuestionDailyEndpointDriver{positive, negative} {
			for _, driver := range drivers {
				asset := strings.ToUpper(strings.TrimSpace(driver.Asset))
				if asset != "" && !seen[asset] && len(assets) < marketQuestionDailyPricePathLimit {
					seen[asset] = true
					assets = append(assets, asset)
				}
			}
		}
	}
	addDrivers(series.Cumulative.Drivers)
	for _, transition := range series.Transitions {
		addDrivers(transition.Drivers)
	}
	result := make([]marketanalysis.MarketQuestionDailyPricePath, 0, len(assets))
	for _, asset := range assets {
		path := marketanalysis.MarketQuestionDailyPricePath{
			Asset: asset, Status: "COMPLETE",
			Points: make([]marketanalysis.MarketQuestionDailyPricePoint, 0, len(series.Points)),
		}
		prices := make([]float64, 0, len(series.Points))
		for _, point := range series.Points {
			price, found := point.BithumbClosePricesKRW[asset]
			var pricePointer *float64
			if found && price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0) {
				pricePointer = float64Pointer(price)
				prices = append(prices, price)
			} else {
				path.Status = "INCOMPLETE"
			}
			path.Points = append(path.Points, marketanalysis.MarketQuestionDailyPricePoint{
				BucketMS: point.BucketMS, ClosePriceKRW: pricePointer,
			})
		}
		if path.Status == "COMPLETE" && len(prices) == len(series.Points) && len(prices) >= 2 {
			path.Direction = classifyDailyPricePath(prices)
			change := (prices[len(prices)-1]/prices[0] - 1) * 100
			path.ChangePct = float64Pointer(change)
		} else {
			path.Direction = "UNAVAILABLE"
		}
		result = append(result, path)
	}
	return result
}

func classifyDailyPricePath(prices []float64) string {
	if len(prices) < 2 {
		return "UNAVAILABLE"
	}
	signs := make([]int, 0, len(prices)-1)
	for index := 1; index < len(prices); index++ {
		delta := prices[index] - prices[index-1]
		tolerance := math.Max(math.Abs(prices[index]), math.Abs(prices[index-1])) * 1e-12
		switch {
		case delta > tolerance:
			signs = append(signs, 1)
		case delta < -tolerance:
			signs = append(signs, -1)
		}
	}
	if len(signs) == 0 {
		return "FLAT"
	}
	all := func(target int) bool {
		for _, sign := range signs {
			if sign != target {
				return false
			}
		}
		return true
	}
	if all(1) {
		return "MONOTONIC_UP"
	}
	if all(-1) {
		return "MONOTONIC_DOWN"
	}
	oneTurn := func(first, second int) bool {
		switched := false
		for _, sign := range signs {
			if !switched && sign == first {
				continue
			}
			if sign == second {
				switched = true
				continue
			}
			return false
		}
		return switched
	}
	if oneTurn(1, -1) {
		return "UP_THEN_DOWN"
	}
	if oneTurn(-1, 1) {
		return "DOWN_THEN_UP"
	}
	return "MIXED"
}

func cloneDailyStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func marketQuestionDailyDriverAssets(series tickertrend.DailyEndpointSeries) []string {
	seen := map[string]bool{}
	result := []string{}
	add := func(values []tickertrend.DailyEndpointDriver) {
		for _, value := range values {
			asset := strings.ToUpper(strings.TrimSpace(value.Asset))
			if asset != "" && !seen[asset] {
				seen[asset] = true
				result = append(result, asset)
			}
		}
	}
	add(series.Cumulative.Drivers)
	for _, transition := range series.Transitions {
		add(transition.Drivers)
	}
	return result
}

func enrichDailyEndpointSeriesResponse(
	response marketanalysis.GroundedMarketQuestionResponse,
	series tickertrend.DailyEndpointSeries,
) marketanalysis.GroundedMarketQuestionResponse {
	return enrichDailyEndpointSeriesResponseWithRecent(response, series, nil)
}

func enrichDailyEndpointSeriesResponseWithRecent(
	response marketanalysis.GroundedMarketQuestionResponse,
	series tickertrend.DailyEndpointSeries,
	recent *marketanalysis.MarketQuestionIntervalEvidence,
) marketanalysis.GroundedMarketQuestionResponse {
	facts := dailyEndpointSeriesFactsWithRecent(series, recent)
	auxiliary := response.Facts.Auxiliary
	response.Facts = marketanalysis.MarketQuestionFacts{
		EvidenceLayer:          "OFFICIAL_ALIGNED_DAILY_ROLLING_24H_ENDPOINTS",
		WindowSec:              series.Cumulative.WindowSec,
		ShareDeltaPP:           copyQuestionFloat(series.Cumulative.ShareDeltaPP),
		DomesticVolumeDeltaKRW: copyQuestionFloat(series.Cumulative.DomesticVolumeDeltaKRW),
		BithumbVolumeDeltaKRW:  copyQuestionFloat(series.Cumulative.BithumbVolumeDeltaKRW),
		Drivers:                dailyEndpointCumulativeFactDrivers(facts.Cumulative),
		ExchangeDrivers:        append([]marketanalysis.MarketQuestionFactExchangeDriver(nil), facts.Cumulative.ExchangeDrivers...),
		DailyEndpointSeries:    facts,
		Auxiliary:              auxiliary,
	}
	if series.Cumulative.AssetAttributionAvailable && series.Cumulative.ShareDeltaPP != nil {
		sum := 0.0
		for _, driver := range series.Cumulative.Drivers {
			sum += driver.ContributionPP
		}
		response.Facts.DriverContributionSumPP = float64Pointer(sum)
		response.Facts.DriverResidualPP = float64Pointer(*series.Cumulative.ShareDeltaPP - sum)
	}
	if len(facts.Points) > 0 {
		response.Facts.Previous = dailyEndpointFactWindow(facts.Points[0])
		response.Facts.Current = dailyEndpointFactWindow(facts.Points[len(facts.Points)-1])
	}
	response.Quality = marketanalysis.MarketQuestionQuality{
		Status:                      series.Status,
		EndpointComparisonAvailable: series.Cumulative.EndpointComparison,
		PathComplete:                series.PathComplete,
		Warnings: append(
			dailyEndpointQualityWarnings(series, facts),
			marketQuestionAuxiliaryWarnings(auxiliary)...,
		),
	}
	response.Provenance.OfficialBucketMS = 0
	if len(series.Points) > 0 {
		response.Provenance.OfficialBucketMS = series.Points[len(series.Points)-1].BucketMS
		response.Provider.AsOfMS = series.Points[len(series.Points)-1].BucketMS
	}
	response.Provenance.ComparisonEndMS = response.Query.TargetEndMS
	response.Provenance.AsOfKind = "OFFICIAL_ALIGNED_SAME_KST_HOUR_ENDPOINT_SERIES"
	return response
}

func dailyEndpointFactWindow(point marketanalysis.MarketQuestionDailyEndpointPoint) *marketanalysis.MarketQuestionIntervalWindow {
	return &marketanalysis.MarketQuestionIntervalWindow{
		StartMS: point.BucketMS, EndMS: point.BucketMS,
		DomesticVolumeKRW: copyQuestionFloat(point.TotalVolumeKRW),
		BithumbVolumeKRW:  copyQuestionFloat(point.BithumbVolumeKRW),
		BithumbSharePct:   copyQuestionFloat(point.BithumbSharePct),
	}
}

func dailyEndpointCumulativeFactDrivers(
	transition marketanalysis.MarketQuestionDailyEndpointTransition,
) []marketanalysis.MarketQuestionFactDriver {
	result := make([]marketanalysis.MarketQuestionFactDriver, 0, len(transition.PositiveDrivers)+len(transition.NegativeDrivers))
	for _, values := range [][]marketanalysis.MarketQuestionDailyEndpointDriver{transition.PositiveDrivers, transition.NegativeDrivers} {
		for _, value := range values {
			result = append(result, marketanalysis.MarketQuestionFactDriver{
				Asset: value.Asset, ContributionPP: value.ContributionPP,
				BithumbVolumeChangeKRW:    value.BithumbVolumeChangeKRW,
				CompetitorVolumeChangeKRW: value.CompetitorVolumeChangeKRW,
			})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return math.Abs(result[i].ContributionPP) > math.Abs(result[j].ContributionPP)
	})
	return result
}

func dailyEndpointQualityWarnings(
	series tickertrend.DailyEndpointSeries,
	facts *marketanalysis.MarketQuestionDailyEndpointSeriesFacts,
) []string {
	warnings := []string{
		"과거 동일 시각 비교는 업비트·빗썸·코인원의 exact 정렬 시간봉만 사용하며 코빗·고팍스는 포함하지 않습니다.",
	}
	if !series.Cumulative.EndpointComparison {
		warnings = append(warnings, "첫 날짜와 마지막 날짜의 정확한 endpoint가 모두 없어 누적 변화를 확정하지 않았습니다.")
	}
	if series.Cumulative.EndpointComparison && !series.Cumulative.AssetAttributionAvailable {
		warnings = append(warnings, "endpoint 점유율은 비교 가능하지만 코인별 market row가 부족해 기여도를 확정하지 않았습니다.")
	}
	if !series.PathComplete {
		warnings = append(warnings, fmt.Sprintf(
			"endpoint 사이 정렬 시간봉 경로에 %d개 누락 또는 비-LIVE 표본이 있습니다.",
			len(series.MissingBucketMS)+len(series.NonLiveBucketMS),
		))
	}
	priceComplete := 0
	for _, path := range facts.PricePaths {
		if path.Status == "COMPLETE" {
			priceComplete++
		}
	}
	if len(facts.PricePaths) > 0 && priceComplete < len(facts.PricePaths) {
		warnings = append(warnings, "일부 주요 코인은 모든 선택 날짜의 정확한 빗썸 종가가 없어 가격 경로를 추정하지 않았습니다.")
	}
	warnings = append(warnings, "과거 백필 표본의 마켓 범위는 백필 실행 시점의 등록 마켓 집합을 따릅니다.")
	return warnings
}

func buildDailyEndpointSeriesFallback(
	series tickertrend.DailyEndpointSeries,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	facts := dailyEndpointSeriesFacts(series)
	sections := []string{}
	points := []string{}

	if facts.Cumulative.EndpointComparison && len(facts.Points) >= 2 &&
		facts.Points[0].BithumbSharePct != nil && facts.Points[len(facts.Points)-1].BithumbSharePct != nil &&
		facts.Cumulative.ShareDeltaPP != nil {
		first, last := facts.Points[0], facts.Points[len(facts.Points)-1]
		summary := fmt.Sprintf(
			"업비트·빗썸·코인원 공식 정렬 시간봉 기준 빗썸 rolling 24시간 점유율은 **%s %.2f%%**에서 **%s %.2f%%**로 **%+.2f%%p** 변했습니다.",
			marketQuestionKST(first.BucketMS), *first.BithumbSharePct,
			marketQuestionKST(last.BucketMS), *last.BithumbSharePct,
			*facts.Cumulative.ShareDeltaPP,
		)
		if len(facts.Cumulative.PositiveDrivers) > 0 || len(facts.Cumulative.NegativeDrivers) > 0 {
			drivers := []string{}
			if len(facts.Cumulative.PositiveDrivers) > 0 {
				driver := facts.Cumulative.PositiveDrivers[0]
				drivers = append(drivers, fmt.Sprintf("가장 큰 누적 상승 기여는 **%s %+.3f%%p**", driver.Asset, driver.ContributionPP))
			}
			if len(facts.Cumulative.NegativeDrivers) > 0 {
				driver := facts.Cumulative.NegativeDrivers[0]
				drivers = append(drivers, fmt.Sprintf("가장 큰 누적 하락 기여는 **%s %+.3f%%p**", driver.Asset, driver.ContributionPP))
			}
			summary += " " + strings.Join(drivers, ", ") + "입니다."
		}
		sections = append(sections, "## 요약 결론\n\n"+summary)
		points = append(points, fmt.Sprintf(
			"공식 동일 시각 endpoint 누적 변화: %.2f%% → %.2f%% (%+.2f%%p)",
			*first.BithumbSharePct, *last.BithumbSharePct, *facts.Cumulative.ShareDeltaPP,
		))
	} else {
		sections = append(sections, "## 요약 결론\n\n요청한 날짜들의 정확한 공식 정렬 endpoint가 모두 확보되지 않아 점유율 변화와 코인별 등락 기여를 확정할 수 없습니다.")
		points = append(points, "요청한 동일 KST 시각 endpoint 비교 자료가 불완전합니다.")
	}

	sections = append(sections, dailyEndpointPointTable(facts.Points))
	sections = append(sections, dailyEndpointTransitionSections(facts.Transitions, facts.Cumulative))
	if priceSection := dailyEndpointPriceSection(facts.PricePaths); priceSection != "" {
		sections = append(sections, priceSection)
	}
	sections = append(sections, dailyEndpointQualitySection(series))

	for _, driver := range facts.Cumulative.PositiveDrivers[:min(len(facts.Cumulative.PositiveDrivers), 2)] {
		points = append(points, fmt.Sprintf(
			"누적 상승 기여 %s %+.3f%%p · 빗썸 %s / 경쟁 거래소 %s",
			driver.Asset, driver.ContributionPP, formatQuestionKRW(driver.BithumbVolumeChangeKRW),
			formatQuestionKRW(driver.CompetitorVolumeChangeKRW),
		))
	}
	for _, driver := range facts.Cumulative.NegativeDrivers[:min(len(facts.Cumulative.NegativeDrivers), 2)] {
		points = append(points, fmt.Sprintf(
			"누적 하락 기여 %s %+.3f%%p · 빗썸 %s / 경쟁 거래소 %s",
			driver.Asset, driver.ContributionPP, formatQuestionKRW(driver.BithumbVolumeChangeKRW),
			formatQuestionKRW(driver.CompetitorVolumeChangeKRW),
		))
	}
	points = append(points, fmt.Sprintf(
		"시간 경로 표본 %d/%d개 · 누락 %d개 · 비-LIVE %d개",
		series.ObservedHourlyPointCount, series.ExpectedHourlyPointCount,
		len(series.MissingBucketMS), len(series.NonLiveBucketMS),
	))
	if len(points) > marketQuestionPointLimit {
		points = points[:marketQuestionPointLimit]
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer:    strings.Join(nonemptyDailySections(sections), "\n\n"),
		KeyPoints: points,
		Caveat:    "코인 기여도는 두 endpoint의 거래대금 변화로 계산한 산술적 점유율 분해이며 가격이나 공지의 인과관계를 뜻하지 않습니다. 과거 동일 시각 수치는 업비트·빗썸·코인원 3개 거래소의 exact 정렬 시간봉만 사용하므로, 현재 5거래소 ticker 점유율 또는 사용자가 제시한 수치와 직접 섞지 않습니다. 누락된 endpoint·시간봉·종가는 가까운 값으로 대체하지 않습니다.",
		Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func dailyEndpointPointTable(points []marketanalysis.MarketQuestionDailyEndpointPoint) string {
	lines := []string{
		"## 비교 기준과 날짜별 핵심 수치",
		"",
		"각 행은 같은 KST 정수 시각에 끝나는 rolling 24시간 endpoint입니다. 질문에 적힌 점유율은 사실로 재사용하지 않고 저장된 공식 표본으로 다시 계산했습니다.",
		"",
		"| 기준 시각(KST) | 빗썸 점유율 | 빗썸 거래대금 | 3거래소 합계 | endpoint | 코인 상세 |",
		"|---|---:|---:|---:|---|---|",
	}
	for _, point := range points {
		share, bithumb, total := "확인 불가", "확인 불가", "확인 불가"
		if point.BithumbSharePct != nil {
			share = fmt.Sprintf("%.2f%%", *point.BithumbSharePct)
		}
		if point.BithumbVolumeKRW != nil {
			bithumb = formatQuestionKRWAmount(*point.BithumbVolumeKRW)
		}
		if point.TotalVolumeKRW != nil {
			total = formatQuestionKRWAmount(*point.TotalVolumeKRW)
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %s | %s | %s | %s |",
			marketQuestionKST(point.BucketMS), share, bithumb, total,
			dailyAvailabilityLabel(point.ExactEndpointAvailable),
			dailyAvailabilityLabel(point.AssetDetailsAvailable),
		))
	}
	return strings.Join(lines, "\n")
}

func dailyEndpointTransitionSections(
	transitions []marketanalysis.MarketQuestionDailyEndpointTransition,
	cumulative marketanalysis.MarketQuestionDailyEndpointTransition,
) string {
	sections := []string{"## 구간별 코인 상승·하락 기여"}
	for _, transition := range transitions {
		sections = append(sections, dailyEndpointTransitionSection(transition, false))
	}
	if len(transitions) > 1 {
		sections = append(sections,
			"## 첫 날짜부터 마지막 날짜까지 누적\n\n"+dailyEndpointTransitionSection(cumulative, true),
		)
	}
	return strings.Join(nonemptyDailySections(sections), "\n\n")
}

func dailyEndpointTransitionSection(
	transition marketanalysis.MarketQuestionDailyEndpointTransition,
	cumulative bool,
) string {
	label := marketQuestionKST(transition.FromBucketMS) + " → " + marketQuestionKST(transition.ToBucketMS)
	if cumulative {
		label += " 누적"
	}
	lines := []string{"### " + label, ""}
	if !transition.EndpointComparison || transition.ShareDeltaPP == nil {
		return strings.Join(append(lines, "정확한 양 endpoint가 없어 이 구간의 변화를 확정할 수 없습니다."), "\n")
	}
	lines = append(lines, fmt.Sprintf(
		"점유율 **%+.2f%%p**, 빗썸 거래대금 **%s**, 3거래소 합계 **%s** 변화입니다.",
		*transition.ShareDeltaPP,
		formatQuestionOptionalKRW(transition.BithumbVolumeDeltaKRW),
		formatQuestionOptionalKRW(transition.DomesticVolumeDeltaKRW),
	))
	if !transition.AssetAttributionAvailable {
		lines = append(lines, "", "코인별 endpoint 상세가 부족해 기여도를 확정하지 않았습니다.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", "#### 상승 기여 상위", "", dailyEndpointDriverTable(transition.PositiveDrivers))
	lines = append(lines, "", "#### 하락 기여 상위", "", dailyEndpointDriverTable(transition.NegativeDrivers))
	return strings.Join(lines, "\n")
}

func dailyEndpointDriverTable(drivers []marketanalysis.MarketQuestionDailyEndpointDriver) string {
	if len(drivers) == 0 {
		return "해당 방향의 확정 기여 코인이 없습니다."
	}
	lines := []string{
		"| 순위 | 코인 | 점유율 기여 | 빗썸 거래대금 변화 | 경쟁 거래소 변화 | 빗썸 종가 변화 |",
		"|---:|---|---:|---:|---:|---:|",
	}
	for _, driver := range drivers {
		price := "확인 불가"
		if driver.ClosePriceChangePct != nil {
			price = fmt.Sprintf("%+.2f%%", *driver.ClosePriceChangePct)
		}
		lines = append(lines, fmt.Sprintf(
			"| %d | %s | %+.3f%%p | %s | %s | %s |",
			driver.Rank, driver.Asset, driver.ContributionPP,
			formatQuestionKRW(driver.BithumbVolumeChangeKRW),
			formatQuestionKRW(driver.CompetitorVolumeChangeKRW), price,
		))
	}
	return strings.Join(lines, "\n")
}

func dailyEndpointPriceSection(paths []marketanalysis.MarketQuestionDailyPricePath) string {
	complete := make([]marketanalysis.MarketQuestionDailyPricePath, 0, len(paths))
	for _, path := range paths {
		if path.Status == "COMPLETE" && path.ChangePct != nil {
			complete = append(complete, path)
		}
	}
	if len(complete) == 0 {
		return ""
	}
	lines := []string{
		"## 주요 코인의 빗썸 가격 경로",
		"",
		"거래대금 기여와 가격 등락은 다른 지표입니다. 아래 가격은 각 선택 endpoint의 정확한 빗썸 1시간봉 종가입니다.",
		"",
		"| 코인 | 선택 날짜 종가 경로 | 누적 등락 | 형태 |",
		"|---|---|---:|---|",
	}
	for _, path := range complete[:min(len(complete), 10)] {
		prices := make([]string, 0, len(path.Points))
		for _, point := range path.Points {
			if point.ClosePriceKRW != nil {
				prices = append(prices, fmt.Sprintf("%s %s", marketQuestionKST(point.BucketMS), formatDailyClosePrice(*point.ClosePriceKRW)))
			}
		}
		lines = append(lines, fmt.Sprintf(
			"| %s | %s | %+.2f%% | %s |",
			path.Asset, strings.Join(prices, " → "), *path.ChangePct, dailyPriceDirectionLabel(path.Direction),
		))
	}
	return strings.Join(lines, "\n")
}

func dailyEndpointQualitySection(series tickertrend.DailyEndpointSeries) string {
	lines := []string{
		"## 데이터 품질과 해석 한계",
		"",
		fmt.Sprintf(
			"- 전체 시간 경로는 기대 **%d개** 중 **%d개**를 관측했습니다. exact endpoint 비교 가능 여부와 중간 경로 완전성은 별도로 판정합니다.",
			series.ExpectedHourlyPointCount, series.ObservedHourlyPointCount,
		),
		"- 포함 거래소: " + strings.Join(series.IncludedExchanges, ", ") + ". 코빗·고팍스는 과거 exact quote 거래대금 계층에 포함하지 않습니다.",
	}
	missing := append([]int64(nil), series.MissingBucketMS...)
	missing = append(missing, series.NonLiveBucketMS...)
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	if len(missing) == 0 {
		lines = append(lines, "- 누락 또는 비-LIVE 시간봉이 없습니다.")
	} else {
		labels := make([]string, 0, min(len(missing), 24))
		for _, value := range missing[:min(len(missing), 24)] {
			labels = append(labels, marketQuestionKST(value))
		}
		suffix := ""
		if len(missing) > len(labels) {
			suffix = fmt.Sprintf(" 외 %d개", len(missing)-len(labels))
		}
		lines = append(lines, "- 누락/비-LIVE 시각: "+strings.Join(labels, ", ")+suffix+".")
	}
	lines = append(lines,
		"- 과거 백필 표본은 백필 실행 당시 등록된 KRW 마켓 집합을 따르며, 사용자가 입력한 점유율 숫자는 검증 근거로 사용하지 않았습니다.",
	)
	return strings.Join(lines, "\n")
}

func dailyAvailabilityLabel(value bool) string {
	if value {
		return "확보"
	}
	return "미확보"
}

func formatQuestionOptionalKRW(value *float64) string {
	if value == nil {
		return "확인 불가"
	}
	return formatQuestionKRW(*value)
}

func formatDailyClosePrice(value float64) string {
	switch {
	case value >= 1_000:
		return fmt.Sprintf("%.0f원", value)
	case value >= 1:
		return fmt.Sprintf("%.2f원", value)
	default:
		return fmt.Sprintf("%.6f원", value)
	}
}

func dailyPriceDirectionLabel(value string) string {
	switch value {
	case "MONOTONIC_UP":
		return "연속 상승"
	case "MONOTONIC_DOWN":
		return "연속 하락"
	case "UP_THEN_DOWN":
		return "상승 후 하락"
	case "DOWN_THEN_UP":
		return "하락 후 상승"
	case "FLAT":
		return "보합"
	case "MIXED":
		return "혼조"
	default:
		return "확인 불가"
	}
}

func nonemptyDailySections(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func enrichDailyEndpointRecentIntervalAnswer(
	answer marketanalysis.MarketQuestionAnswer,
	evidence *marketanalysis.MarketQuestionIntervalEvidence,
) marketanalysis.MarketQuestionAnswer {
	facts := dailyRecentIntervalFacts(evidence)
	if facts == nil {
		return answer
	}
	windowLabel := fmt.Sprintf("%d초", facts.WindowSec)
	if facts.WindowSec >= 60 && facts.WindowSec%60 == 0 {
		windowLabel = fmt.Sprintf("%d분", facts.WindowSec/60)
	}
	lines := []string{"## 최근 단기 흐름", ""}
	if facts.Status != "CONFIRMED" || facts.Previous.BithumbSharePct == nil ||
		facts.Current.BithumbSharePct == nil || facts.ShareDeltaPP == nil {
		lines = append(lines, fmt.Sprintf(
			"최근 %s 직전 %s의 WebSocket 직접 관측 coverage가 완전하지 않아 현재 지속·둔화·반전을 확정하지 않았습니다. 현재 %d/%d초, 직전 %d/%d초 관측입니다.",
			koreanWithAnd(windowLabel), windowLabel,
			facts.CurrentCoverage.CoverageSec, facts.CurrentCoverage.RequiredSec,
			facts.PreviousCoverage.CoverageSec, facts.PreviousCoverage.RequiredSec,
		))
		answer.Answer = insertDailyAnswerSectionBeforeQuality(answer.Answer, strings.Join(lines, "\n"))
		return answer
	}
	lines = append(lines, fmt.Sprintf(
		"직전 %s 최근 %s의 WebSocket 직접 체결을 비교하면 빗썸 단기 점유율은 **%.2f%% → %.2f%%**, **%+.2f%%p**입니다.",
		koreanWithAnd(windowLabel), windowLabel, *facts.Previous.BithumbSharePct,
		*facts.Current.BithumbSharePct, *facts.ShareDeltaPP,
	))
	lines = append(lines, "", "### 단기 상승 기여", "", dailyRecentDriverTable(facts.PositiveDrivers))
	lines = append(lines, "", "### 단기 하락 기여", "", dailyRecentDriverTable(facts.NegativeDrivers))
	answer.Answer = insertDailyAnswerSectionBeforeQuality(answer.Answer, strings.Join(lines, "\n"))
	point := fmt.Sprintf(
		"최근 %s 직접 관측: %.2f%% → %.2f%% (%+.2f%%p)",
		windowLabel, *facts.Previous.BithumbSharePct, *facts.Current.BithumbSharePct, *facts.ShareDeltaPP,
	)
	if !marketQuestionContainsString(answer.KeyPoints, point) {
		if len(answer.KeyPoints) < marketQuestionPointLimit {
			answer.KeyPoints = append(answer.KeyPoints, point)
		} else if len(answer.KeyPoints) > 0 {
			answer.KeyPoints[len(answer.KeyPoints)-1] = point
		}
	}
	return answer
}

func dailyRecentDriverTable(drivers []marketanalysis.MarketQuestionFactDriver) string {
	if len(drivers) == 0 {
		return "해당 방향의 확정 기여 코인이 없습니다."
	}
	lines := []string{
		"| 순위 | 코인 | 점유율 기여 | 빗썸 거래대금 변화 | 경쟁 거래소 변화 |",
		"|---:|---|---:|---:|---:|",
	}
	for index, driver := range drivers {
		lines = append(lines, fmt.Sprintf(
			"| %d | %s | %+.3f%%p | %s | %s |",
			index+1, driver.Asset, driver.ContributionPP,
			formatQuestionKRW(driver.BithumbVolumeChangeKRW),
			formatQuestionKRW(driver.CompetitorVolumeChangeKRW),
		))
	}
	return strings.Join(lines, "\n")
}

func insertDailyAnswerSectionBeforeQuality(answer, section string) string {
	qualityMarker := "\n\n## 데이터 품질과 해석 한계"
	if index := strings.Index(answer, qualityMarker); index >= 0 {
		return strings.TrimSpace(answer[:index]) + "\n\n" + section + answer[index:]
	}
	return strings.TrimSpace(answer) + "\n\n" + section
}

func enrichDailyEndpointRealtimeAnswer(
	answer marketanalysis.MarketQuestionAnswer,
	auxiliary *marketanalysis.MarketQuestionAuxiliaryFacts,
) marketanalysis.MarketQuestionAnswer {
	if auxiliary == nil || auxiliary.Realtime == nil {
		return answer
	}
	realtime := auxiliary.Realtime
	lines := []string{"## 최근 단기 흐름", ""}
	if realtime.Status != "LIVE" {
		lines = append(lines, "현재 realtime-flow 근거가 LIVE 상태가 아니어서 과거 endpoint 추세가 지금도 이어지는지 확정하지 않았습니다.")
	} else {
		available := 0
		for _, window := range realtime.Windows {
			if window.Status != "COMPLETE" || window.ShareDeltaPP == nil {
				continue
			}
			available++
			lines = append(lines, fmt.Sprintf("- 최근 %d초 직접 관측 점유율 변화: %+.3f%%p", window.DurationSec, *window.ShareDeltaPP))
		}
		if available == 0 {
			lines = append(lines, "완전한 단기 window가 없어 현재 지속·둔화·반전을 확정하지 않았습니다.")
		}
	}
	answer.Answer = insertDailyAnswerSectionBeforeQuality(answer.Answer, strings.Join(lines, "\n"))
	return answer
}

func marketQuestionDailyAnswerComplete(
	answer marketanalysis.MarketQuestionAnswer,
	series tickertrend.DailyEndpointSeries,
) bool {
	return marketQuestionDailyAnswerCompleteWithRecent(answer, series, false)
}

// groundDailyEndpointProviderAnswer keeps tables and factual statements, plus
// KeyPoints and Caveat, server-owned. The model summary retains its dedicated
// binding checks. Non-summary interpretation may supplement a matching server
// section only when it adds no numeric, named-market, or realized-direction
// claim. The assembled answer must still pass the caller's normal guards.
func groundDailyEndpointProviderAnswer(
	provider marketanalysis.MarketQuestionAnswer,
	deterministic marketanalysis.MarketQuestionAnswer,
) marketanalysis.MarketQuestionAnswer {
	modelBody := provider.Answer
	modelContribution := false
	serverSummary, _ := marketQuestionMarkdownSection(deterministic.Answer, "요약 결론")
	provider.Answer = deterministic.Answer
	if summary, found := marketQuestionMarkdownSection(modelBody, "요약 결론"); found &&
		strings.IndexFunc(summary, unicode.IsLetter) >= 0 &&
		!strings.ContainsAny(summary, "|<>`") && !marketQuestionDailyHasObservationAssertion(summary) &&
		marketQuestionDailyProseIdentity(summary) != marketQuestionDailyProseIdentity(serverSummary) {
		if grounded, replaced := replaceMarketQuestionMarkdownSection(
			deterministic.Answer,
			"요약 결론",
			summary,
		); replaced {
			provider.Answer = grounded
			modelContribution = true
		}
	}
	seen := map[string]bool{}
	for _, paragraph := range strings.Split(deterministic.Answer, "\n\n") {
		seen[strings.Join(strings.Fields(paragraph), " ")] = true
	}
	// Match the actual server sections rather than a question-specific list of
	// headings. Never import model headings or tables, including when its
	// summary is absent, and do not move prose across evidence-layer sections.
	for _, line := range strings.Split(deterministic.Answer, "\n") {
		heading := marketQuestionMarkdownHeading.FindStringSubmatch(line)
		if len(heading) != 3 || len(heading[1]) != 2 ||
			normalizeMarketQuestionHeading(heading[2]) == normalizeMarketQuestionHeading("요약 결론") {
			continue
		}
		modelSection, found := marketQuestionMarkdownSection(modelBody, heading[2])
		if !found || strings.Contains(modelSection, "```") || strings.Contains(modelSection, "~~~") {
			continue
		}
		serverSection, _ := marketQuestionMarkdownSection(provider.Answer, heading[2])
		additions := []string{}
		for _, paragraph := range strings.Split(modelSection, "\n\n") {
			paragraph = strings.TrimSpace(paragraph)
			key := strings.Join(strings.Fields(paragraph), " ")
			if seen[key] || !marketQuestionDailyInterpretationParagraphAllowed(paragraph) {
				continue
			}
			seen[key] = true
			additions = append(additions, paragraph)
		}
		if len(additions) > 0 {
			// Put interpretations before the server section, not beneath its
			// last asset/interval subheading where their scope would change.
			var replaced bool
			provider.Answer, replaced = replaceMarketQuestionMarkdownSection(
				provider.Answer, heading[2], strings.Join(additions, "\n\n")+"\n\n"+serverSection,
			)
			modelContribution = modelContribution || replaced
		}
	}
	if !modelContribution {
		// Replacing every model sentence with server facts is a fallback, not a
		// successful model interpretation. Preserve honest UI/cache provenance.
		server := deterministic
		server.KeyPoints = append([]string(nil), deterministic.KeyPoints...)
		server.Source = "SERVER_BRIEFING_FALLBACK"
		server.Model = ""
		return server
	}
	provider.KeyPoints = append([]string(nil), deterministic.KeyPoints...)
	provider.Caveat = deterministic.Caveat
	return provider
}

func marketQuestionDailyProseIdentity(text string) string {
	text = strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

// Observation completeness/recovery is a typed server fact, never a free-form
// interpretation. Apply the same boundary to summaries and section additions.
// This lexical gate is conservative, not a proof of all natural-language claims.
var marketQuestionDailyObservationAssertion = regexp.MustCompile(`(?:완전|불완전)(?:합니다|하다|하므로|해서|하니|한\s*상태|히|하게)|(?:수집|확보|복구|관측)(?:이|은|는)?\s*(?:완료|된\s*상태|되었|됐|되어\s*있|돼\s*있)|(?:누락|공백)(?:이|은)?\s*(?:없|전혀\s*없)`)
var marketQuestionDailyObservationDisclaimer = regexp.MustCompile(`(?:완전|불완전)하다고\s*(?:단정|판단)할\s*수\s*없(?:습니다|다)`)

func marketQuestionDailyHasObservationAssertion(text string) bool {
	// A direct refusal to infer completeness is not a completeness assertion.
	// Remove only that local predicate; another claim in the same sentence or
	// paragraph must still be checked independently.
	return marketQuestionDailyObservationAssertion.MatchString(marketQuestionDailyObservationDisclaimer.ReplaceAllString(text, " "))
}

func marketQuestionDailyInterpretationParagraphAllowed(paragraph string) bool {
	if paragraph == "" || strings.ContainsAny(paragraph, "|`~<>[]") ||
		marketQuestionDailyHasObservationAssertion(paragraph) ||
		!marketQuestionAnswerNumbersAllowed(marketanalysis.MarketQuestionAnswer{Answer: paragraph}, marketanalysis.MarketQuestionAnswer{}) {
		return false
	}
	for _, line := range strings.Split(paragraph, "\n") {
		if marketQuestionMarkdownHeading.MatchString(line) {
			return false
		}
	}
	lower := strings.ToLower(paragraph)
	for name := range marketQuestionExchangeNames {
		if strings.Contains(lower, name) {
			return false
		}
	}
	for name := range marketQuestionKoreanAssetNames {
		if strings.Contains(paragraph, name) {
			return false
		}
	}
	for _, symbol := range marketQuestionUpperNamedTerm.FindAllString(strings.ToUpper(paragraph), -1) {
		if !marketQuestionSystemNamedTerms[symbol] {
			return false
		}
	}
	// Numeric-free is not synonymous with factual: realized direction, rank,
	// or leadership is still a market claim. Those claims need the server's
	// exact interval/asset binding and are not imported by this prose path.
	for _, claim := range []string{
		"했습니다", "하였습니다", "했다", "하였다", "나타났", "기록했", "주도", "가장", "최대", "최소",
		"상승입니다", "하락입니다", "증가입니다", "감소입니다", "올랐", "내렸", "커졌", "작아졌",
		"전망", "예측", "때문에", "덕분에", "탓에",
	} {
		if strings.Contains(lower, claim) {
			return false
		}
	}
	// Keep explanatory relationships, not arbitrary numeric-free assertions.
	// This is intentionally conservative; it is not a general semantic proof
	// of natural-language facts or permission to bypass the final validators.
	// Every sentence must itself explain a relationship. An accepted sentence
	// cannot authorize a neighboring numeric-free market/quality assertion.
	explainedSentences := 0
	for _, sentence := range strings.FieldsFunc(lower, func(r rune) bool { return r == '.' || r == ';' || r == '!' || r == '?' || r == '\n' }) {
		if strings.TrimSpace(sentence) == "" {
			continue
		}
		explains := false
		for _, explanation := range []string{
			"구별", "구분", "따로", "별도", "읽어", "읽는", "다른 지표", "같은 지표",
			"뜻하지", "의미하지", "단정할 수 없", "판단할 수 없", "인과관계",
		} {
			if strings.Contains(sentence, explanation) {
				explains = true
				break
			}
		}
		if !explains {
			return false
		}
		explainedSentences++
	}
	return explainedSentences > 0
}

func marketQuestionDailyAnswerCompleteWithRecent(
	answer marketanalysis.MarketQuestionAnswer,
	series tickertrend.DailyEndpointSeries,
	recentRequested bool,
) bool {
	body := answer.Answer
	for _, requiredHeading := range []string{
		"요약 결론",
		"비교 기준과 날짜별 핵심 수치",
		"구간별 코인 상승·하락 기여",
		"데이터 품질과 해석 한계",
	} {
		if _, found := marketQuestionMarkdownSection(body, requiredHeading); !found {
			return false
		}
	}
	if recentRequested {
		if _, found := marketQuestionMarkdownSection(body, "최근 단기 흐름"); !found {
			return false
		}
	}

	facts := dailyEndpointSeriesFacts(series)
	if !marketQuestionDailySummaryBindingAllowed(body, facts) {
		return false
	}
	if !marketQuestionDailyEndpointPointBindingsAllowed(body, facts.Points) {
		return false
	}
	transitionSection, found := marketQuestionMarkdownSection(body, "구간별 코인 상승·하락 기여")
	if !found {
		return false
	}
	for _, transition := range facts.Transitions {
		if !marketQuestionDailyTransitionBindingAllowed(transitionSection, transition, false) {
			return false
		}
	}

	// With more than one adjacent interval, cumulative facts are distinct
	// evidence and must live under their own H2 section. This prevents an
	// adjacent interval's valid numbers from authorizing a cumulative claim.
	if len(facts.Transitions) > 1 {
		cumulativeSection, cumulativeFound := marketQuestionMarkdownSection(
			body,
			"첫 날짜부터 마지막 날짜까지 누적",
		)
		if !cumulativeFound ||
			!marketQuestionDailyTransitionBindingAllowed(cumulativeSection, facts.Cumulative, true) {
			return false
		}
	}
	return true
}

type marketQuestionDailyNumericClaim struct {
	start      int
	end        int
	normalized string
	unit       string
}

type marketQuestionDailyNumericBinding struct {
	normalized string
	unit       string
}

func newMarketQuestionDailyNumericBinding(value float64, precision int, unit string) marketQuestionDailyNumericBinding {
	normalized, _ := normalizeMarketQuestionNumericToken(fmt.Sprintf("%.*f", precision, value))
	return marketQuestionDailyNumericBinding{normalized: normalized, unit: unit}
}

func marketQuestionDailySummaryBindingAllowed(
	answer string,
	facts *marketanalysis.MarketQuestionDailyEndpointSeriesFacts,
) bool {
	summary, found := marketQuestionMarkdownSection(answer, "요약 결론")
	if !found || facts == nil {
		return false
	}
	if !facts.Cumulative.EndpointComparison || facts.Cumulative.ShareDeltaPP == nil || len(facts.Points) < 2 {
		return marketQuestionUncertaintyClaim(strings.ToLower(summary)) &&
			len(marketQuestionDailyNumericClaims(summary, "PCT")) == 0 &&
			len(marketQuestionDailyNumericClaims(summary, "PP")) == 0
	}
	first, last := facts.Points[0], facts.Points[len(facts.Points)-1]
	if first.BithumbSharePct == nil || last.BithumbSharePct == nil {
		return false
	}

	claims := marketQuestionDailyNumericClaimLocations(summary)
	percentClaims := marketQuestionDailyClaimsWithUnit(claims, "PCT")
	pointClaims := marketQuestionDailyClaimsWithUnit(claims, "PP")
	for _, forbiddenUnit := range []string{
		"KRW", "KRW_TEN_THOUSAND", "KRW_HUNDRED_MILLION", "KRW_TRILLION",
		"COUNT", "TIME_MS", "TIME_SECOND", "TIME_MINUTE", "TIME_DAY",
	} {
		if len(marketQuestionDailyClaimsWithUnit(claims, forbiddenUnit)) > 0 {
			return false
		}
	}
	for _, hourClaim := range marketQuestionDailyClaimsWithUnit(claims, "TIME_HOUR") {
		if hourClaim.normalized != "24" {
			return false
		}
	}
	for _, claim := range claims {
		if claim.unit == "" && strings.HasPrefix(strings.TrimSpace(summary[claim.end:]), "거래소") {
			return false
		}
	}

	firstLabel, lastLabel := marketQuestionKST(first.BucketMS), marketQuestionKST(last.BucketMS)
	firstLabelAt, lastLabelAt := strings.Index(summary, firstLabel), strings.Index(summary, lastLabel)
	hasCumulativeNumericClaim := len(percentClaims) > 0 || len(pointClaims) > 0 ||
		firstLabelAt >= 0 || lastLabelAt >= 0
	if !hasCumulativeNumericClaim {
		return !marketQuestionDailySummaryMentionsDriver(summary, facts)
	}
	if strings.Count(summary, firstLabel) != 1 || strings.Count(summary, lastLabel) != 1 ||
		firstLabelAt >= lastLabelAt || len(percentClaims) != 2 || len(pointClaims) == 0 {
		return false
	}
	for _, point := range facts.Points[1 : len(facts.Points)-1] {
		if strings.Contains(summary, marketQuestionKST(point.BucketMS)) {
			return false
		}
	}
	wantFirst := newMarketQuestionDailyNumericBinding(*first.BithumbSharePct, 2, "PCT").normalized
	wantLast := newMarketQuestionDailyNumericBinding(*last.BithumbSharePct, 2, "PCT").normalized
	wantDelta := newMarketQuestionDailyNumericBinding(*facts.Cumulative.ShareDeltaPP, 2, "PP").normalized
	if percentClaims[0].normalized != wantFirst || percentClaims[1].normalized != wantLast ||
		!(firstLabelAt < percentClaims[0].start && percentClaims[0].start < lastLabelAt &&
			lastLabelAt < percentClaims[1].start && percentClaims[1].start < pointClaims[0].start) ||
		pointClaims[0].normalized != wantDelta {
		return false
	}
	return marketQuestionDailySummaryDriverClaimsAllowed(summary, pointClaims[1:], facts)
}

func marketQuestionDailySummaryDriverClaimsAllowed(
	summary string,
	claims []marketQuestionDailyNumericClaim,
	facts *marketanalysis.MarketQuestionDailyEndpointSeriesFacts,
) bool {
	drivers := append(
		append([]marketanalysis.MarketQuestionDailyEndpointDriver(nil), facts.Cumulative.PositiveDrivers...),
		facts.Cumulative.NegativeDrivers...,
	)
	mentioned := make(map[string]bool)
	for _, transition := range append(
		append([]marketanalysis.MarketQuestionDailyEndpointTransition(nil), facts.Transitions...),
		facts.Cumulative,
	) {
		for _, values := range [][]marketanalysis.MarketQuestionDailyEndpointDriver{
			transition.PositiveDrivers,
			transition.NegativeDrivers,
		} {
			for _, driver := range values {
				asset := strings.ToUpper(strings.TrimSpace(driver.Asset))
				if asset != "" && marketQuestionDailyContainsAsset(summary, asset) {
					mentioned[asset] = true
				}
			}
		}
	}
	if len(mentioned) != len(claims) {
		return false
	}
	for asset := range mentioned {
		matched := false
		for _, driver := range drivers {
			if !strings.EqualFold(asset, strings.TrimSpace(driver.Asset)) {
				continue
			}
			expected := newMarketQuestionDailyNumericBinding(driver.ContributionPP, 3, "PP").normalized
			for _, claim := range claims {
				if claim.normalized == expected && marketQuestionDailyAssetImmediatelyPrecedesClaim(summary, asset, claim.start) {
					matched = true
					break
				}
			}
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func marketQuestionDailySummaryMentionsDriver(
	summary string,
	facts *marketanalysis.MarketQuestionDailyEndpointSeriesFacts,
) bool {
	for _, transition := range append(
		append([]marketanalysis.MarketQuestionDailyEndpointTransition(nil), facts.Transitions...),
		facts.Cumulative,
	) {
		for _, values := range [][]marketanalysis.MarketQuestionDailyEndpointDriver{
			transition.PositiveDrivers,
			transition.NegativeDrivers,
		} {
			for _, driver := range values {
				if marketQuestionDailyContainsAsset(summary, driver.Asset) {
					return true
				}
			}
		}
	}
	return false
}

func marketQuestionDailyContainsAsset(text, asset string) bool {
	upper, asset := strings.ToUpper(text), strings.ToUpper(strings.TrimSpace(asset))
	for offset := 0; asset != "" && offset < len(upper); {
		index := strings.Index(upper[offset:], asset)
		if index < 0 {
			break
		}
		start, end := offset+index, offset+index+len(asset)
		if (start == 0 || !isMarketQuestionAssetByte(upper[start-1])) &&
			(end == len(upper) || !isMarketQuestionAssetByte(upper[end])) {
			return true
		}
		offset = end
	}
	for koreanName, symbol := range marketQuestionKoreanAssetNames {
		if strings.EqualFold(symbol, asset) && strings.Contains(text, koreanName) {
			return true
		}
	}
	return false
}

func marketQuestionDailyAssetImmediatelyPrecedesClaim(text, asset string, claimStart int) bool {
	if claimStart <= 0 || claimStart > len(text) {
		return false
	}
	prefix := text[:claimStart]
	upperPrefix, asset := strings.ToUpper(prefix), strings.ToUpper(strings.TrimSpace(asset))
	index := strings.LastIndex(upperPrefix, asset)
	if index >= 0 && claimStart-(index+len(asset)) <= 80 &&
		(index == 0 || !isMarketQuestionAssetByte(upperPrefix[index-1])) &&
		(index+len(asset) == len(upperPrefix) || !isMarketQuestionAssetByte(upperPrefix[index+len(asset)])) {
		return true
	}
	for koreanName, symbol := range marketQuestionKoreanAssetNames {
		if !strings.EqualFold(symbol, asset) {
			continue
		}
		if index = strings.LastIndex(prefix, koreanName); index >= 0 && claimStart-(index+len(koreanName)) <= 80 {
			return true
		}
	}
	return false
}

func isMarketQuestionAssetByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func marketQuestionDailyEndpointPointBindingsAllowed(
	answer string,
	points []marketanalysis.MarketQuestionDailyEndpointPoint,
) bool {
	section, found := marketQuestionMarkdownSection(answer, "비교 기준과 날짜별 핵심 수치")
	if !found {
		return false
	}
	rows := marketQuestionMarkdownTableRows(section)
	for _, point := range points {
		matching := marketQuestionDailyRowsWithExactCell(rows, marketQuestionKST(point.BucketMS))
		if len(matching) != 1 {
			return false
		}
		row := strings.Join(matching[0], " | ")
		if point.BithumbSharePct == nil {
			if len(marketQuestionDailyNumericClaims(row, "PCT")) != 0 ||
				(!strings.Contains(row, "확인 불가") && !strings.Contains(row, "미확보")) {
				return false
			}
			continue
		}
		expected := newMarketQuestionDailyNumericBinding(*point.BithumbSharePct, 2, "PCT")
		if !marketQuestionDailyOnlyNumericClaim(row, expected) {
			return false
		}
	}
	return true
}

func marketQuestionDailyTransitionBindingAllowed(
	parentSection string,
	transition marketanalysis.MarketQuestionDailyEndpointTransition,
	cumulative bool,
) bool {
	label := marketQuestionKST(transition.FromBucketMS) + " → " + marketQuestionKST(transition.ToBucketMS)
	if cumulative {
		label += " 누적"
	}
	section, found := marketQuestionMarkdownSection(parentSection, label)
	if !found {
		return false
	}
	intro, introFound := marketQuestionMarkdownSectionIntro(parentSection, label)
	if !introFound {
		return false
	}
	if !transition.EndpointComparison || transition.ShareDeltaPP == nil {
		return marketQuestionUncertaintyClaim(strings.ToLower(intro))
	}
	expectedDelta := newMarketQuestionDailyNumericBinding(*transition.ShareDeltaPP, 2, "PP")
	if !marketQuestionDailyOnlyNumericClaim(intro, expectedDelta) {
		return false
	}
	if !transition.AssetAttributionAvailable {
		return true
	}
	positiveSection, positiveFound := marketQuestionMarkdownSection(section, "상승 기여 상위")
	negativeSection, negativeFound := marketQuestionMarkdownSection(section, "하락 기여 상위")
	if !positiveFound || !negativeFound {
		return false
	}
	if len(transition.PositiveDrivers) > 0 &&
		!marketQuestionDailyDriverBindingAllowed(positiveSection, transition.PositiveDrivers[0]) {
		return false
	}
	if len(transition.NegativeDrivers) > 0 &&
		!marketQuestionDailyDriverBindingAllowed(negativeSection, transition.NegativeDrivers[0]) {
		return false
	}
	return true
}

func marketQuestionDailyDriverBindingAllowed(
	section string,
	driver marketanalysis.MarketQuestionDailyEndpointDriver,
) bool {
	rows := marketQuestionMarkdownTableRows(section)
	matching := marketQuestionDailyRowsWithExactCell(rows, driver.Asset)
	if len(matching) != 1 {
		return false
	}
	row := strings.Join(matching[0], " | ")
	expected := newMarketQuestionDailyNumericBinding(driver.ContributionPP, 3, "PP")
	return marketQuestionDailyOnlyNumericClaim(row, expected)
}

func marketQuestionDailyRowsWithExactCell(rows [][]string, expected string) [][]string {
	expected = normalizeMarketQuestionMarkdownCell(expected)
	result := make([][]string, 0, 1)
	for _, row := range rows {
		for _, cell := range row {
			if normalizeMarketQuestionMarkdownCell(cell) == expected {
				result = append(result, row)
				break
			}
		}
	}
	return result
}

func normalizeMarketQuestionMarkdownCell(value string) string {
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "*_`~"))
	return strings.ToUpper(strings.Join(strings.Fields(value), " "))
}

func marketQuestionDailyOnlyNumericClaim(text string, expected marketQuestionDailyNumericBinding) bool {
	claims := marketQuestionDailyNumericClaims(text, expected.unit)
	return len(claims) == 1 && claims[0] == expected.normalized
}

func marketQuestionDailyNumericClaims(text, unit string) []string {
	result := []string{}
	for _, claim := range marketQuestionDailyNumericClaimLocations(text) {
		if claim.unit == unit {
			result = append(result, claim.normalized)
		}
	}
	return result
}

func marketQuestionDailyNumericClaimLocations(text string) []marketQuestionDailyNumericClaim {
	result := []marketQuestionDailyNumericClaim{}
	for _, location := range marketQuestionNumericToken.FindAllStringIndex(text, -1) {
		normalized, ok := normalizeMarketQuestionNumericToken(text[location[0]:location[1]])
		if !ok {
			continue
		}
		result = append(result, marketQuestionDailyNumericClaim{
			start: location[0], end: location[1], normalized: normalized,
			unit: marketQuestionNumericUnit(text[location[1]:]),
		})
	}
	return result
}

func marketQuestionDailyClaimsWithUnit(
	claims []marketQuestionDailyNumericClaim,
	unit string,
) []marketQuestionDailyNumericClaim {
	result := make([]marketQuestionDailyNumericClaim, 0, len(claims))
	for _, claim := range claims {
		if claim.unit == unit {
			result = append(result, claim)
		}
	}
	return result
}
