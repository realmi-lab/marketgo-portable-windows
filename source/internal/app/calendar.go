package app

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

const (
	calendarDayContractVersion     = "CALENDAR_DAY_V1"
	calendarAggregationAvailable   = "AVAILABLE"
	calendarAggregationQuarantined = "QUARANTINED_NUMERIC_AGGREGATION"
)

var seoulLocation = mustLoadLocation("Asia/Seoul")

type CalendarMarketRow struct {
	MarketRow
	ObservedInInterval bool `json:"observed_in_interval"`
}

type CalendarAssetRow struct {
	Scope               domain.Scope `json:"scope"`
	Asset               string       `json:"asset"`
	AggregationStatus   string       `json:"aggregation_status"`
	VolumeKRW           float64      `json:"volume_krw"`
	NativeQuoteVolume   float64      `json:"native_quote_volume"`
	TradeCount          uint64       `json:"trade_count"`
	UnresolvedFXCount   uint64       `json:"unresolved_fx_count"`
	BaseQuantity        float64      `json:"base_quantity"`
	MarketCount         int          `json:"market_count"`
	TradedMarketCount   int          `json:"traded_market_count"`
	ObservedMarketCount int          `json:"observed_market_count"`
	Exchanges           []string     `json:"exchanges"`
	Quotes              []string     `json:"quotes"`
}

type CalendarDayResponse struct {
	ContractVersion     string              `json:"contract_version"`
	BarType             string              `json:"bar_type"`
	Day                 string              `json:"day"`
	Timezone            string              `json:"timezone"`
	IntervalStart       int64               `json:"interval_start"`
	IntervalEnd         int64               `json:"interval_end"`
	Status              string              `json:"status"`
	Coverage            coverage.Result     `json:"coverage"`
	ZeroFilled          bool                `json:"zero_filled"`
	Rows                []CalendarMarketRow `json:"rows"`
	DataSource          map[string]any      `json:"data_source"`
	MarketScope         string              `json:"market_scope"`
	QuoteFilter         string              `json:"quote_filter"`
	MarketCount         int                 `json:"market_count"`
	ObservedMarketCount int                 `json:"observed_market_count"`
	ObservedAssetCount  int                 `json:"observed_asset_count"`
	AssetCount          int                 `json:"asset_count"`
	AggregationStatus   string              `json:"aggregation_status"`
	TotalVolumeKRW      float64             `json:"total_volume_krw"`
	TotalTradeCount     uint64              `json:"total_trade_count"`
	UnresolvedFXCount   uint64              `json:"unresolved_fx_count"`
	Assets              []CalendarAssetRow  `json:"assets"`
}

func (s *Service) CalendarDay(ctx context.Context, day, marketScope string) (CalendarDayResponse, error) {
	start, end, normalizedDay, err := calendarBounds(day)
	if err != nil {
		return CalendarDayResponse{}, err
	}
	marketScope = strings.ToUpper(strings.TrimSpace(marketScope))
	if marketScope == "" {
		marketScope = "KRW"
	}
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return CalendarDayResponse{}, ErrUnsupportedMarketScope
	}
	read, err := s.runtime.atomicRead(
		ctx,
		[]aggregate.IntervalRequest{{Start: domain.UnixSecond(start - 1), End: domain.UnixSecond(end)}},
		[]coverageInterval{{start: start, end: end}},
	)
	if err != nil {
		return CalendarDayResponse{}, err
	}
	if len(read.intervals) != 1 || !read.intervals[0].OK || len(read.coverage) != 1 {
		return CalendarDayResponse{}, errors.New("invalid atomic calendar interval")
	}
	markets := filterAnalysisMarkets(s.registry.Universe().Markets(), marketScope)
	response := buildCalendarDay(normalizedDay, start, end, marketScope, markets, read.intervals[0].Values, read.coverage[0])
	response.DataSource["aggregation_version"] = read.version
	response.DataSource["confirmation_semantics"] = "TRANSPORT_COVERAGE_ONLY"
	response.DataSource["official_reconciled"] = false
	return response, nil
}

func calendarBounds(day string) (int64, int64, string, error) {
	parsed, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(day), seoulLocation)
	if err != nil {
		return 0, 0, "", errors.New("day must use YYYY-MM-DD")
	}
	start := parsed.Unix()
	end := parsed.AddDate(0, 0, 1).Unix() - 1
	return start, end, parsed.Format(time.DateOnly), nil
}

func buildCalendarDay(day string, start, end int64, marketScope string, markets []domain.MarketPair, values map[aggregate.VolumeKey]aggregate.Totals, intervalCoverage coverage.Result) CalendarDayResponse {
	rows, identityConflicts := calendarMarketRowsWithConflicts(marketScope, markets, values)
	assets, assetNumericIssues := calendarAssetRowsWithIssues(rows)
	response := CalendarDayResponse{
		ContractVersion: calendarDayContractVersion, BarType: "CALENDAR_DAY", Day: day,
		Timezone: seoulLocation.String(), IntervalStart: start, IntervalEnd: end,
		Status: intervalCoverage.Status, Coverage: intervalCoverage, ZeroFilled: false,
		Rows: rows, MarketScope: marketScope, QuoteFilter: calendarQuoteFilter(marketScope),
		MarketCount: len(rows), AssetCount: len(assets), Assets: assets,
		AggregationStatus: calendarAggregationAvailable,
		DataSource: map[string]any{
			"transport": "WEBSOCKET_ONLY", "bar_type": "CALENDAR_DAY",
			"contract_version": calendarDayContractVersion, "bucket_granularity": "1_SECOND",
		},
	}
	numericIssues := make(map[string]struct{})
	for _, row := range rows {
		calendarAddFinite(&response.TotalVolumeKRW, row.VolumeKRW, "total_volume_krw", numericIssues)
		calendarAddUint64(&response.TotalTradeCount, row.TradeCount, "total_trade_count", numericIssues)
		calendarAddUint64(&response.UnresolvedFXCount, row.UnresolvedFXCount, "unresolved_fx_count", numericIssues)
		if row.ObservedInInterval {
			response.ObservedMarketCount++
		}
	}
	for _, asset := range assets {
		if asset.ObservedMarketCount > 0 {
			response.ObservedAssetCount++
		}
	}
	if len(numericIssues) > 0 {
		// The market rows are the audit evidence and remain untouched. Their
		// aggregate cannot be represented safely, so do not publish a wrapped
		// counter or +/-Inf as if it were a real total.
		response.TotalVolumeKRW = 0
		response.TotalTradeCount = 0
		response.UnresolvedFXCount = 0
	}
	if len(numericIssues) > 0 || len(assetNumericIssues) > 0 {
		response.AggregationStatus = calendarAggregationQuarantined
		if response.Status == "CONFIRMED" {
			response.Status = "PROVISIONAL"
		}
	}
	response.DataSource["numeric_aggregation_status"] = response.AggregationStatus
	if issues := calendarNumericIssueLabels(numericIssues, assetNumericIssues); len(issues) > 0 {
		response.DataSource["numeric_aggregation_issues"] = issues
	}
	if response.Status == "CONFIRMED" && response.UnresolvedFXCount > 0 {
		response.Status = "PROVISIONAL"
	}
	if len(identityConflicts) > 0 {
		// A reused exchange symbol cannot safely be attributed to either asset.
		// The conflicting totals are quarantined by calendarMarketRowsWithConflicts;
		// keep the transport status honest instead of presenting the result as
		// fully confirmed.
		response.DataSource["market_identity_status"] = "QUARANTINED"
		response.DataSource["market_identity_conflicts"] = identityConflicts
		if response.Status == "CONFIRMED" {
			response.Status = "PROVISIONAL"
		}
	} else {
		response.DataSource["market_identity_status"] = "OK"
	}
	return response
}

func calendarMarketRows(marketScope string, markets []domain.MarketPair, values map[aggregate.VolumeKey]aggregate.Totals) []CalendarMarketRow {
	rows, _ := calendarMarketRowsWithConflicts(marketScope, markets, values)
	return rows
}

type calendarMarketIdentity struct {
	scope    string
	exchange string
	market   string
}

type calendarMarketCandidate struct {
	key   aggregate.VolumeKey
	total aggregate.Totals
}

// calendarMarketRowsWithConflicts joins the current registry and historical
// aggregates by the stable market identity (scope, exchange, symbol). The
// historical key owns Base/Quote metadata for the requested day; a later
// registry metadata change must not create a second zero-filled row.
//
// Equivalent duplicate historical keys collapse to one row without adding the
// same totals twice. If their metadata or totals disagree, they can represent
// a reused symbol or split accounting keys. One deterministic metadata row is
// then returned with zero totals and the identity is reported as quarantined.
func calendarMarketRowsWithConflicts(marketScope string, markets []domain.MarketPair, values map[aggregate.VolumeKey]aggregate.Totals) ([]CalendarMarketRow, []string) {
	historical := make(map[calendarMarketIdentity][]calendarMarketCandidate, len(values))
	for key, total := range values {
		key = canonicalCalendarKey(key)
		if key.Scope != string(domain.ScopeDomestic) {
			continue
		}
		identity := calendarIdentity(key)
		historical[identity] = append(historical[identity], calendarMarketCandidate{key: key, total: total})
	}

	current := make(map[calendarMarketIdentity][]aggregate.VolumeKey, len(markets))
	for _, market := range markets {
		key := canonicalCalendarKey(aggregate.VolumeKey{
			Scope: string(market.Scope), Exchange: string(market.Exchange), Market: market.Symbol,
			Base: market.Base, Quote: market.Quote,
		})
		if key.Scope != string(domain.ScopeDomestic) {
			continue
		}
		identity := calendarIdentity(key)
		current[identity] = append(current[identity], key)
	}

	identities := make(map[calendarMarketIdentity]struct{}, len(historical)+len(current))
	for identity := range historical {
		identities[identity] = struct{}{}
	}
	for identity := range current {
		identities[identity] = struct{}{}
	}
	ordered := make([]calendarMarketIdentity, 0, len(identities))
	for identity := range identities {
		ordered = append(ordered, identity)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].scope != ordered[j].scope {
			return ordered[i].scope < ordered[j].scope
		}
		if ordered[i].exchange != ordered[j].exchange {
			return ordered[i].exchange < ordered[j].exchange
		}
		return ordered[i].market < ordered[j].market
	})

	rows := make([]CalendarMarketRow, 0, len(ordered))
	conflicts := make([]string, 0)
	for _, identity := range ordered {
		if candidates := historical[identity]; len(candidates) > 0 {
			sort.Slice(candidates, func(i, j int) bool { return calendarCandidateLess(candidates[i], candidates[j]) })
			selectedIndex := calendarMatchingCandidate(candidates, marketScope)
			if selectedIndex < 0 {
				continue
			}
			selected := candidates[selectedIndex]
			observed := true
			if reason := calendarHistoricalConflict(candidates); reason != "" {
				selected.total = aggregate.Totals{}
				observed = false
				conflicts = append(conflicts, calendarIdentityLabel(identity)+":"+reason)
			}
			rows = append(rows, CalendarMarketRow{MarketRow: marketRow(selected.key, selected.total), ObservedInInterval: observed})
			continue
		}

		candidates := current[identity]
		sort.Slice(candidates, func(i, j int) bool { return calendarKeyLess(candidates[i], candidates[j]) })
		selectedIndex := calendarMatchingKey(candidates, marketScope)
		if selectedIndex < 0 {
			continue
		}
		if calendarRegistryMetadataConflict(candidates) {
			conflicts = append(conflicts, calendarIdentityLabel(identity)+":REGISTRY_METADATA_CONFLICT")
		}
		rows = append(rows, CalendarMarketRow{MarketRow: marketRow(candidates[selectedIndex], aggregate.Totals{}), ObservedInInterval: false})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Scope != rows[j].Scope {
			return rows[i].Scope < rows[j].Scope
		}
		if rows[i].Exchange != rows[j].Exchange {
			return rows[i].Exchange < rows[j].Exchange
		}
		return rows[i].Market < rows[j].Market
	})
	return rows, conflicts
}

func canonicalCalendarKey(key aggregate.VolumeKey) aggregate.VolumeKey {
	key.Scope = strings.ToLower(strings.TrimSpace(key.Scope))
	key.Exchange = strings.ToLower(strings.TrimSpace(key.Exchange))
	key.Market = strings.ToUpper(strings.TrimSpace(key.Market))
	key.Base = strings.ToUpper(strings.TrimSpace(key.Base))
	key.Quote = strings.ToUpper(strings.TrimSpace(key.Quote))
	return key
}

func calendarIdentity(key aggregate.VolumeKey) calendarMarketIdentity {
	return calendarMarketIdentity{scope: key.Scope, exchange: key.Exchange, market: key.Market}
}

func calendarIdentityLabel(identity calendarMarketIdentity) string {
	return identity.scope + ":" + identity.exchange + ":" + identity.market
}

func calendarMatchingCandidate(candidates []calendarMarketCandidate, marketScope string) int {
	for index, candidate := range candidates {
		if calendarKeyMatchesScope(candidate.key, marketScope) {
			return index
		}
	}
	return -1
}

func calendarMatchingKey(candidates []aggregate.VolumeKey, marketScope string) int {
	for index, candidate := range candidates {
		if calendarKeyMatchesScope(candidate, marketScope) {
			return index
		}
	}
	return -1
}

func calendarKeyLess(left, right aggregate.VolumeKey) bool {
	if left.Scope != right.Scope {
		return left.Scope < right.Scope
	}
	if left.Exchange != right.Exchange {
		return left.Exchange < right.Exchange
	}
	if left.Market != right.Market {
		return left.Market < right.Market
	}
	if left.Base != right.Base {
		return left.Base < right.Base
	}
	return left.Quote < right.Quote
}

func calendarCandidateLess(left, right calendarMarketCandidate) bool {
	if calendarKeyLess(left.key, right.key) {
		return true
	}
	if calendarKeyLess(right.key, left.key) {
		return false
	}
	if left.total.KRWVolume != right.total.KRWVolume {
		return left.total.KRWVolume < right.total.KRWVolume
	}
	if left.total.NativeVolume != right.total.NativeVolume {
		return left.total.NativeVolume < right.total.NativeVolume
	}
	if left.total.TradeCount != right.total.TradeCount {
		return left.total.TradeCount < right.total.TradeCount
	}
	if left.total.UnresolvedCount != right.total.UnresolvedCount {
		return left.total.UnresolvedCount < right.total.UnresolvedCount
	}
	if left.total.BuyKRWVolume != right.total.BuyKRWVolume {
		return left.total.BuyKRWVolume < right.total.BuyKRWVolume
	}
	if left.total.SellKRWVolume != right.total.SellKRWVolume {
		return left.total.SellKRWVolume < right.total.SellKRWVolume
	}
	return left.total.BaseVolume < right.total.BaseVolume
}

func calendarHistoricalConflict(candidates []calendarMarketCandidate) string {
	if len(candidates) < 2 {
		return ""
	}
	reference := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.key.Base != reference.key.Base || candidate.key.Quote != reference.key.Quote {
			return "HISTORICAL_METADATA_CONFLICT"
		}
	}
	for _, candidate := range candidates[1:] {
		if candidate.total != reference.total {
			return "HISTORICAL_TOTAL_CONFLICT"
		}
	}
	return ""
}

func calendarRegistryMetadataConflict(candidates []aggregate.VolumeKey) bool {
	if len(candidates) < 2 {
		return false
	}
	reference := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Base != reference.Base || candidate.Quote != reference.Quote {
			return true
		}
	}
	return false
}

func calendarKeyMatchesScope(key aggregate.VolumeKey, marketScope string) bool {
	if key.Scope != string(domain.ScopeDomestic) {
		return false
	}
	return marketScope == "ALL_DOMESTIC_CONVERTED" || (marketScope == "KRW" && strings.EqualFold(key.Quote, "KRW"))
}

func calendarAssetRows(rows []CalendarMarketRow) []CalendarAssetRow {
	assets, _ := calendarAssetRowsWithIssues(rows)
	return assets
}

func calendarAssetRowsWithIssues(rows []CalendarMarketRow) ([]CalendarAssetRow, map[string][]string) {
	type assetKey struct{ scope, asset string }
	type accumulator struct {
		row       CalendarAssetRow
		exchanges map[string]struct{}
		quotes    map[string]struct{}
		issues    map[string]struct{}
	}
	byAsset := make(map[assetKey]*accumulator)
	for _, market := range rows {
		key := assetKey{scope: string(market.Scope), asset: market.Base}
		asset := byAsset[key]
		if asset == nil {
			asset = &accumulator{
				row:       CalendarAssetRow{Scope: market.Scope, Asset: market.Base, AggregationStatus: calendarAggregationAvailable},
				exchanges: make(map[string]struct{}), quotes: make(map[string]struct{}), issues: make(map[string]struct{}),
			}
			byAsset[key] = asset
		}
		calendarAddFinite(&asset.row.VolumeKRW, market.VolumeKRW, "volume_krw", asset.issues)
		calendarAddFinite(&asset.row.NativeQuoteVolume, market.NativeVolume, "native_quote_volume", asset.issues)
		calendarAddUint64(&asset.row.TradeCount, market.TradeCount, "trade_count", asset.issues)
		calendarAddUint64(&asset.row.UnresolvedFXCount, market.UnresolvedFXCount, "unresolved_fx_count", asset.issues)
		calendarAddFinite(&asset.row.BaseQuantity, market.BaseQuantity, "base_quantity", asset.issues)
		asset.row.MarketCount++
		if market.TradeCount > 0 {
			asset.row.TradedMarketCount++
		}
		if market.ObservedInInterval {
			asset.row.ObservedMarketCount++
		}
		asset.exchanges[string(market.Exchange)] = struct{}{}
		asset.quotes[market.Quote] = struct{}{}
	}
	assets := make([]CalendarAssetRow, 0, len(byAsset))
	issues := make(map[string][]string)
	for _, asset := range byAsset {
		asset.row.Exchanges = sortedSet(asset.exchanges)
		asset.row.Quotes = sortedSet(asset.quotes)
		if len(asset.issues) > 0 {
			asset.row.AggregationStatus = calendarAggregationQuarantined
			asset.row.VolumeKRW = 0
			asset.row.NativeQuoteVolume = 0
			asset.row.TradeCount = 0
			asset.row.UnresolvedFXCount = 0
			asset.row.BaseQuantity = 0
			issues[calendarAssetLabel(asset.row.Scope, asset.row.Asset)] = sortedSet(asset.issues)
		}
		assets = append(assets, asset.row)
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].Scope != assets[j].Scope {
			return assets[i].Scope < assets[j].Scope
		}
		return assets[i].Asset < assets[j].Asset
	})
	return assets, issues
}

func calendarAddFinite(total *float64, value float64, field string, issues map[string]struct{}) {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || *total > math.MaxFloat64-value {
		issues[field] = struct{}{}
		return
	}
	*total += value
}

func calendarAddUint64(total *uint64, value uint64, field string, issues map[string]struct{}) {
	if math.MaxUint64-*total < value {
		issues[field] = struct{}{}
		return
	}
	*total += value
}

func calendarAssetLabel(scope domain.Scope, asset string) string {
	return string(scope) + ":" + asset
}

func calendarNumericIssueLabels(responseIssues map[string]struct{}, assetIssues map[string][]string) []string {
	result := make([]string, 0, len(responseIssues)+len(assetIssues))
	for _, field := range sortedSet(responseIssues) {
		result = append(result, "calendar_day:"+field)
	}
	assets := make([]string, 0, len(assetIssues))
	for asset := range assetIssues {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	for _, asset := range assets {
		for _, field := range assetIssues[asset] {
			result = append(result, "asset:"+asset+":"+field)
		}
	}
	return result
}

func calendarQuoteFilter(scope string) string {
	if scope == "KRW" {
		return "KRW"
	}
	return "ALL"
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}
