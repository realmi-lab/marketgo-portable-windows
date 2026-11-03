// Package marketquestion owns the versioned semantic vocabulary accepted by
// the market-share question planner. It deliberately contains no SQL,
// executable expressions, or calculation callbacks: repositories and Go
// calculators remain the only owners of numeric work.
package marketquestion

const CatalogVersion = "MARKET_QUESTION_SEMANTICS_V1"

type MetricID string

const (
	MetricOverallBithumbShare       MetricID = "OVERALL_BITHUMB_SHARE"
	MetricTargetExchangeShareVolume MetricID = "TARGET_EXCHANGE_SHARE_AND_VOLUME"
	MetricAssetCombined             MetricID = "BITHUMB_CAPTURE_AND_TOTAL_SHARE_CONTRIBUTION"
	MetricAssetBithumbCapture       MetricID = "ASSET_BITHUMB_CAPTURE"
	MetricAssetShareWithinBithumb   MetricID = "ASSET_SHARE_WITHIN_BITHUMB"
	MetricAssetTotalContribution    MetricID = "ASSET_TOTAL_SHARE_CONTRIBUTION"
)

type ComparisonID string

const (
	ComparisonCurrentSnapshot          ComparisonID = "CURRENT_SNAPSHOT"
	ComparisonCalendarDayClose         ComparisonID = "CALENDAR_DAY_CLOSE"
	ComparisonDailyRolling24hEndpoints ComparisonID = "DAILY_ROLLING_24H_ENDPOINTS"
	ComparisonFixedHistoricalInterval  ComparisonID = "FIXED_HISTORICAL_INTERVAL"
	ComparisonRolling24hEndpointDelta  ComparisonID = "ROLLING_24H_ENDPOINT_DELTA"
	ComparisonCurrentPreviousInterval  ComparisonID = "CURRENT_VS_PREVIOUS_EQUAL_INTERVAL"
)

type RangeKind string

const (
	RangeCurrent                   RangeKind = "CURRENT"
	RangeCalendarDayKST            RangeKind = "CALENDAR_DAY_KST"
	RangeCalendarEndpointSeriesKST RangeKind = "CALENDAR_ENDPOINT_SERIES_KST"
	RangeExactKST                  RangeKind = "EXACT_RANGE_KST"
	RangeRelativeWindow            RangeKind = "RELATIVE_WINDOW"
)

type EvidenceToolID string

const (
	ToolCurrentOfficialTicker       EvidenceToolID = "CURRENT_OFFICIAL_TICKER"
	ToolOfficialCalendarDay         EvidenceToolID = "OFFICIAL_CALENDAR_DAY"
	ToolOfficialDailyEndpointSeries EvidenceToolID = "OFFICIAL_DAILY_ENDPOINT_SERIES"
	ToolOfficialAlignedWindow       EvidenceToolID = "OFFICIAL_ALIGNED_WINDOW"
	ToolOfficialFixedIntervalCandle EvidenceToolID = "OFFICIAL_FIXED_INTERVAL_CANDLE_COMPARISON"
	ToolWebsocketRangeAggregate     EvidenceToolID = "WEBSOCKET_RANGE_AGGREGATE"
	ToolWebsocketTimePath           EvidenceToolID = "WEBSOCKET_TIME_PATH"
	ToolExchangeAttribution         EvidenceToolID = "EXCHANGE_ATTRIBUTION"
	ToolAssetAttribution            EvidenceToolID = "ASSET_ATTRIBUTION"
	ToolRealtime10sSignalSummary    EvidenceToolID = "REALTIME_10S_SIGNAL_SUMMARY"
	ToolCurrentRealtimeFlowSummary  EvidenceToolID = "CURRENT_REALTIME_FLOW_SUMMARY"
	// ToolDataQuality is retained as a legacy internal identifier so stored
	// plans and server tests can still be decoded. It is intentionally absent
	// from the public planner catalog and every public range allowlist.
	ToolDataQuality       EvidenceToolID = "DATA_QUALITY"
	ToolNoticeContext     EvidenceToolID = "NOTICE_CONTEXT"
	ToolOrderbookSnapshot EvidenceToolID = "ORDERBOOK_SNAPSHOT"
)

type AssetRequirement string

const (
	AssetForbidden AssetRequirement = "FORBIDDEN"
	AssetRequired  AssetRequirement = "REQUIRED_REGISTERED_ASSET"
)

type ExchangeRequirement string

const (
	ExchangeEmptyOrBithumb     ExchangeRequirement = "EMPTY_OR_BITHUMB"
	ExchangeRequiredNonBithumb ExchangeRequirement = "REQUIRED_NON_BITHUMB"
)

// MetricRangeRule defines a supported planner range without embedding an
// implementation expression. DisallowedTools captures semantic conflicts such
// as asking for a non-Bithumb exchange metric over the three-exchange official
// aligned window.
type MetricRangeRule struct {
	RangeKind       RangeKind
	DisallowedTools []EvidenceToolID
}

type MetricDefinition struct {
	ID                 MetricID
	Description        string
	DenominatorMeaning string
	Asset              AssetRequirement
	Exchange           ExchangeRequirement
	AllowedRanges      []MetricRangeRule
}

type ComparisonDefinition struct {
	ID          ComparisonID
	Description string
}

type EvidenceToolDefinition struct {
	ID          EvidenceToolID
	Description string
	Explanation string
}

// RangeRoute selects one comparison meaning for a range. SelectorTool is
// empty for the default route and otherwise activates a more specific route.
// RequiredTools are allowlisted evidence capabilities, not executable code.
type RangeRoute struct {
	SelectorTool       EvidenceToolID
	ExpectedComparison ComparisonID
	RequiredTools      []EvidenceToolID
	AllowedTools       []EvidenceToolID
}

type RangeDefinition struct {
	Kind        RangeKind
	Description string
	Routes      []RangeRoute
}

var metricDefinitions = []MetricDefinition{
	{
		ID:                 MetricOverallBithumbShare,
		Description:        "빗썸 전체 거래대금의 국내 시장 비중",
		DenominatorMeaning: "선택된 근거 계층의 국내 전체 거래대금",
		Asset:              AssetForbidden,
		Exchange:           ExchangeEmptyOrBithumb,
		AllowedRanges: []MetricRangeRule{
			{RangeKind: RangeCurrent},
			{RangeKind: RangeCalendarDayKST},
			{RangeKind: RangeCalendarEndpointSeriesKST},
			{RangeKind: RangeExactKST},
			{RangeKind: RangeRelativeWindow},
		},
	},
	{
		ID:                 MetricTargetExchangeShareVolume,
		Description:        "지정한 비-빗썸 거래소의 거래대금과 국내 시장 비중",
		DenominatorMeaning: "현재 ticker 또는 직접 관측 WebSocket 계층의 국내 전체 거래대금",
		Asset:              AssetForbidden,
		Exchange:           ExchangeRequiredNonBithumb,
		AllowedRanges: []MetricRangeRule{
			{RangeKind: RangeCurrent},
			{RangeKind: RangeRelativeWindow, DisallowedTools: []EvidenceToolID{ToolOfficialAlignedWindow}},
		},
	},
	{
		ID:                 MetricAssetCombined,
		Description:        "해당 자산의 빗썸 capture, 빗썸 내부 비중, 전체 점유율 기여를 함께 요청",
		DenominatorMeaning: "각 하위 지표가 선언한 국내 자산 거래대금·빗썸 전체 거래대금·국내 전체 거래대금",
		Asset:              AssetRequired,
		Exchange:           ExchangeEmptyOrBithumb,
		AllowedRanges: []MetricRangeRule{
			{RangeKind: RangeCurrent},
			{RangeKind: RangeRelativeWindow},
		},
	},
	{
		ID:                 MetricAssetBithumbCapture,
		Description:        "해당 자산 국내 거래에서 빗썸이 차지하는 비중",
		DenominatorMeaning: "국내 전체 거래소의 해당 자산 거래대금",
		Asset:              AssetRequired,
		Exchange:           ExchangeEmptyOrBithumb,
		AllowedRanges: []MetricRangeRule{
			{RangeKind: RangeCurrent},
			{RangeKind: RangeRelativeWindow},
		},
	},
	{
		ID:                 MetricAssetShareWithinBithumb,
		Description:        "빗썸 거래에서 해당 자산이 차지하는 내부 비중",
		DenominatorMeaning: "빗썸 전체 거래대금",
		Asset:              AssetRequired,
		Exchange:           ExchangeEmptyOrBithumb,
		AllowedRanges: []MetricRangeRule{
			{RangeKind: RangeCurrent},
			{RangeKind: RangeRelativeWindow},
		},
	},
	{
		ID:                 MetricAssetTotalContribution,
		Description:        "빗썸의 해당 자산 거래대금이 국내 전체 시장에서 차지하는 비중",
		DenominatorMeaning: "국내 전체 거래소의 전체 거래대금",
		Asset:              AssetRequired,
		Exchange:           ExchangeEmptyOrBithumb,
		AllowedRanges: []MetricRangeRule{
			{RangeKind: RangeCurrent},
			{RangeKind: RangeRelativeWindow},
		},
	},
}

var comparisonDefinitions = []ComparisonDefinition{
	{ID: ComparisonCurrentSnapshot, Description: "현재 snapshot"},
	{ID: ComparisonCalendarDayClose, Description: "KST 달력일 종료값"},
	{ID: ComparisonDailyRolling24hEndpoints, Description: "여러 날짜의 같은 시각 rolling 24시간 endpoint"},
	{ID: ComparisonFixedHistoricalInterval, Description: "사용자가 지정한 고정 과거 구간"},
	{ID: ComparisonRolling24hEndpointDelta, Description: "rolling 24시간 점유율 endpoint 간 변화"},
	{ID: ComparisonCurrentPreviousInterval, Description: "현재 구간과 직전 동일 길이 구간"},
}

var evidenceToolDefinitions = []EvidenceToolDefinition{
	{ID: ToolCurrentOfficialTicker, Description: "현재 공식 rolling 24시간 거래소 점유율"},
	{ID: ToolOfficialCalendarDay, Description: "특정 KST 날짜의 자정부터 자정까지 공식 정렬 시간봉"},
	{ID: ToolOfficialDailyEndpointSeries, Description: "여러 KST 날짜의 같은 시각 rolling 24시간 공식 정렬 endpoint 비교"},
	{ID: ToolOfficialAlignedWindow, Description: "최근 N시간 공식 정렬 시간봉의 시작·종료 비교"},
	{ID: ToolOfficialFixedIntervalCandle, Description: "두 인접 고정 구간에 모두 공식 봉이 있는 동일 거래소·시장 거래대금 비교"},
	{ID: ToolWebsocketRangeAggregate, Description: "사용자가 지정한 과거 KST 구간의 10초 기본 체결 집계", Explanation: "AGGREGATE"},
	{ID: ToolWebsocketTimePath, Description: "과거 구간의 시간 경로", Explanation: "PATH"},
	{ID: ToolExchangeAttribution, Description: "거래소별 산술 기여", Explanation: "EXCHANGES"},
	{ID: ToolAssetAttribution, Description: "자산별 산술 기여", Explanation: "DRIVERS"},
	{ID: ToolRealtime10sSignalSummary, Description: "저장된 10초 신호 분포·상태·극값", Explanation: "HISTORICAL_SIGNALS"},
	{ID: ToolCurrentRealtimeFlowSummary, Description: "현재 10·30·60초 직접 관측 흐름", Explanation: "REALTIME"},
	{ID: ToolNoticeContext, Description: "시간·거래소·자산이 겹치는 저장 공지", Explanation: "NOTICES"},
	{ID: ToolOrderbookSnapshot, Description: "현재 호가 보조 근거", Explanation: "ORDERBOOK"},
}

var rangeDefinitions = []RangeDefinition{
	{
		Kind: RangeCurrent, Description: "기간 없는 현재 질문",
		Routes: []RangeRoute{{
			ExpectedComparison: ComparisonCurrentSnapshot,
			AllowedTools: []EvidenceToolID{
				ToolCurrentOfficialTicker,
				ToolExchangeAttribution,
				ToolAssetAttribution,
				ToolCurrentRealtimeFlowSummary,
				ToolNoticeContext,
				ToolOrderbookSnapshot,
			},
		}},
	},
	{
		Kind: RangeCalendarDayKST, Description: "특정 KST 날짜 전체",
		Routes: []RangeRoute{{
			ExpectedComparison: ComparisonCalendarDayClose,
			RequiredTools:      []EvidenceToolID{ToolOfficialCalendarDay},
			AllowedTools: []EvidenceToolID{
				ToolOfficialCalendarDay,
				ToolExchangeAttribution,
				ToolAssetAttribution,
				ToolNoticeContext,
			},
		}},
	},
	{
		Kind: RangeCalendarEndpointSeriesKST, Description: "둘 이상의 날짜를 같은 안전한 정수 시각 기준으로 비교",
		Routes: []RangeRoute{{
			ExpectedComparison: ComparisonDailyRolling24hEndpoints,
			RequiredTools:      []EvidenceToolID{ToolOfficialDailyEndpointSeries},
			AllowedTools: []EvidenceToolID{
				ToolOfficialDailyEndpointSeries,
				ToolExchangeAttribution,
				ToolAssetAttribution,
				ToolCurrentRealtimeFlowSummary,
			},
		}},
	},
	{
		Kind: RangeExactKST, Description: "사용자가 시작·종료 KST 시각을 지정한 구간",
		Routes: []RangeRoute{{
			ExpectedComparison: ComparisonFixedHistoricalInterval,
			RequiredTools:      []EvidenceToolID{ToolWebsocketRangeAggregate},
			AllowedTools: []EvidenceToolID{
				ToolWebsocketRangeAggregate,
				ToolWebsocketTimePath,
				ToolExchangeAttribution,
				ToolAssetAttribution,
				ToolRealtime10sSignalSummary,
				ToolNoticeContext,
			},
		}},
	},
	{
		Kind: RangeRelativeWindow, Description: "최근 N분 또는 N시간",
		Routes: []RangeRoute{
			{
				SelectorTool:       ToolOfficialAlignedWindow,
				ExpectedComparison: ComparisonRolling24hEndpointDelta,
				RequiredTools:      []EvidenceToolID{ToolOfficialAlignedWindow},
				AllowedTools: []EvidenceToolID{
					ToolOfficialAlignedWindow,
					ToolExchangeAttribution,
					ToolAssetAttribution,
					ToolCurrentRealtimeFlowSummary,
					ToolNoticeContext,
					ToolOrderbookSnapshot,
				},
			},
			{
				ExpectedComparison: ComparisonCurrentPreviousInterval,
				RequiredTools:      []EvidenceToolID{ToolWebsocketRangeAggregate},
				AllowedTools: []EvidenceToolID{
					ToolWebsocketRangeAggregate,
					ToolOfficialFixedIntervalCandle,
					ToolExchangeAttribution,
					ToolAssetAttribution,
					ToolCurrentRealtimeFlowSummary,
					ToolNoticeContext,
					ToolOrderbookSnapshot,
				},
			},
		},
	},
}

var pathResolutionSeconds = []int64{10, 30, 60, 300, 600, 1_800, 3_600}

func Metrics() []MetricDefinition {
	result := make([]MetricDefinition, len(metricDefinitions))
	for index, definition := range metricDefinitions {
		result[index] = definition
		result[index].AllowedRanges = cloneMetricRangeRules(definition.AllowedRanges)
	}
	return result
}

func Metric(id MetricID) (MetricDefinition, bool) {
	for _, definition := range metricDefinitions {
		if definition.ID == id {
			definition.AllowedRanges = cloneMetricRangeRules(definition.AllowedRanges)
			return definition, true
		}
	}
	return MetricDefinition{}, false
}

func Comparisons() []ComparisonDefinition {
	return append([]ComparisonDefinition(nil), comparisonDefinitions...)
}

func Comparison(id ComparisonID) (ComparisonDefinition, bool) {
	for _, definition := range comparisonDefinitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return ComparisonDefinition{}, false
}

func EvidenceTools() []EvidenceToolDefinition {
	return append([]EvidenceToolDefinition(nil), evidenceToolDefinitions...)
}

func EvidenceTool(id EvidenceToolID) (EvidenceToolDefinition, bool) {
	for _, definition := range evidenceToolDefinitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return EvidenceToolDefinition{}, false
}

func Ranges() []RangeDefinition {
	result := make([]RangeDefinition, len(rangeDefinitions))
	for index, definition := range rangeDefinitions {
		result[index] = cloneRangeDefinition(definition)
	}
	return result
}

func Range(kind RangeKind) (RangeDefinition, bool) {
	for _, definition := range rangeDefinitions {
		if definition.Kind == kind {
			return cloneRangeDefinition(definition), true
		}
	}
	return RangeDefinition{}, false
}

func ResolveRangeRoute(kind RangeKind, selectedTools map[EvidenceToolID]bool) (RangeRoute, bool) {
	definition, ok := Range(kind)
	if !ok {
		return RangeRoute{}, false
	}
	for _, route := range definition.Routes {
		if route.SelectorTool != "" && selectedTools[route.SelectorTool] {
			return route, true
		}
	}
	for _, route := range definition.Routes {
		if route.SelectorTool == "" {
			return route, true
		}
	}
	return RangeRoute{}, false
}

func RangeRouteAllowsTool(route RangeRoute, tool EvidenceToolID) bool {
	for _, allowed := range route.AllowedTools {
		if tool == allowed {
			return true
		}
	}
	return false
}

func MetricAllowsRange(id MetricID, kind RangeKind, selectedTools map[EvidenceToolID]bool) bool {
	definition, ok := Metric(id)
	if !ok {
		return false
	}
	for _, rule := range definition.AllowedRanges {
		if rule.RangeKind != kind {
			continue
		}
		for _, tool := range rule.DisallowedTools {
			if selectedTools[tool] {
				return false
			}
		}
		return true
	}
	return false
}

func PathResolutionSeconds() []int64 {
	return append([]int64(nil), pathResolutionSeconds...)
}

func SupportsPathResolution(seconds int64) bool {
	for _, candidate := range pathResolutionSeconds {
		if seconds == candidate {
			return true
		}
	}
	return false
}

func cloneMetricRangeRules(values []MetricRangeRule) []MetricRangeRule {
	result := make([]MetricRangeRule, len(values))
	for index, value := range values {
		result[index] = value
		result[index].DisallowedTools = append([]EvidenceToolID(nil), value.DisallowedTools...)
	}
	return result
}

func cloneRangeDefinition(value RangeDefinition) RangeDefinition {
	value.Routes = append([]RangeRoute(nil), value.Routes...)
	for index := range value.Routes {
		value.Routes[index].RequiredTools = append([]EvidenceToolID(nil), value.Routes[index].RequiredTools...)
		value.Routes[index].AllowedTools = append([]EvidenceToolID(nil), value.Routes[index].AllowedTools...)
	}
	return value
}
