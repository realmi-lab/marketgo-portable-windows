package analysis

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

type ExpertSummary struct {
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Basis  []string `json:"basis,omitempty"`
}
type MarketComparison struct {
	Key               string   `json:"key"`
	Label             string   `json:"label"`
	PreviousVolumeKRW *float64 `json:"previous_volume_krw"`
	CurrentVolumeKRW  float64  `json:"current_volume_krw"`
	ChangeKRW         *float64 `json:"change_krw"`
	ChangePct         *float64 `json:"change_pct"`
}
type ShareBridge struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	ValuePP *float64 `json:"value_pp"`
	Kind    string   `json:"kind"`
}
type Breadth struct {
	AssetCount                       int      `json:"asset_count"`
	PositiveAssetCount               int      `json:"positive_asset_count"`
	NegativeAssetCount               int      `json:"negative_asset_count"`
	FlatAssetCount                   int      `json:"flat_asset_count"`
	PositiveContributionPP           float64  `json:"positive_contribution_pp"`
	NegativeContributionPP           float64  `json:"negative_contribution_pp"`
	TotalAbsoluteContributionPP      float64  `json:"total_absolute_contribution_pp"`
	Top1AbsoluteContributionSharePct *float64 `json:"top_1_absolute_contribution_share_pct"`
	Top5AbsoluteContributionSharePct *float64 `json:"top_5_absolute_contribution_share_pct"`
}
type ListingSegment struct {
	Segment                   string  `json:"segment"`
	Label                     string  `json:"label"`
	AssetCount                int     `json:"asset_count"`
	CurrentDomesticVolumeKRW  float64 `json:"current_domestic_volume_krw"`
	PreviousDomesticVolumeKRW float64 `json:"previous_domestic_volume_krw"`
	CurrentBithumbVolumeKRW   float64 `json:"current_bithumb_volume_krw"`
	PreviousBithumbVolumeKRW  float64 `json:"previous_bithumb_volume_krw"`
	DomesticVolumeChangeKRW   float64 `json:"domestic_volume_change_krw"`
	BithumbVolumeChangeKRW    float64 `json:"bithumb_volume_change_krw"`
	ContributionPP            float64 `json:"contribution_pp"`
}
type ListingStructureHeadline struct {
	Structure                 string       `json:"structure"`
	Label                     string       `json:"label"`
	AssetCount                int          `json:"asset_count"`
	CurrentDomesticVolumeKRW  float64      `json:"current_domestic_volume_krw"`
	PreviousDomesticVolumeKRW float64      `json:"previous_domestic_volume_krw"`
	DomesticVolumeChangeKRW   float64      `json:"domestic_volume_change_krw"`
	CurrentBithumbVolumeKRW   float64      `json:"current_bithumb_volume_krw"`
	PreviousBithumbVolumeKRW  float64      `json:"previous_bithumb_volume_krw"`
	BithumbVolumeChangeKRW    float64      `json:"bithumb_volume_change_krw"`
	ContributionPP            float64      `json:"contribution_pp"`
	LeadingAssets             []MajorAsset `json:"leading_assets"`
}
type AttributionHeadline struct {
	PrimaryAsset             *MajorAsset               `json:"primary_asset"`
	DominantListingStructure *ListingStructureHeadline `json:"dominant_listing_structure"`
	VolumeRelationship       string                    `json:"volume_relationship"`
}
type MajorAsset struct {
	AttributionAsset
	ListingSegment    string   `json:"listing_segment"`
	DomesticExchanges []string `json:"domestic_exchanges"`
}
type ExpertCheck struct {
	Code          string   `json:"code"`
	EvidenceLevel string   `json:"evidence_level"`
	Status        string   `json:"status"`
	Title         string   `json:"title"`
	Detail        string   `json:"detail"`
	Value         *float64 `json:"value"`
	Unit          string   `json:"unit"`
	Basis         []string `json:"basis"`
	EvidenceLabel string   `json:"evidence_label"`
	CausalClaim   bool     `json:"causal_claim"`
}
type ExpertBrief struct {
	Status                       string                `json:"status"`
	Comparison                   string                `json:"comparison"`
	PrimaryDriver                string                `json:"primary_driver"`
	MeasuredSummary              []ExpertSummary       `json:"measured_summary"`
	DiagnosticSummary            []ExpertSummary       `json:"diagnostic_summary"`
	ContextualSummary            []ExpertSummary       `json:"contextual_summary"`
	MarketComparison             []MarketComparison    `json:"market_comparison"`
	ShareBridge                  []ShareBridge         `json:"share_bridge"`
	ExchangeComparison           []AttributionExchange `json:"exchange_comparison"`
	QuoteComparison              []AttributionQuote    `json:"quote_comparison"`
	Breadth                      Breadth               `json:"breadth"`
	ListingSegments              []ListingSegment      `json:"listing_segments"`
	AttributionHeadline          AttributionHeadline   `json:"attribution_headline"`
	MajorAssets                  []MajorAsset          `json:"major_assets"`
	MajorAssetCoveragePct        *float64              `json:"major_asset_coverage_pct"`
	PositiveAssets               []MajorAsset          `json:"positive_assets"`
	PositiveAssetCoveragePct     *float64              `json:"positive_asset_coverage_pct"`
	NegativeAssets               []MajorAsset          `json:"negative_assets"`
	NegativeAssetCoveragePct     *float64              `json:"negative_asset_coverage_pct"`
	AllAssetsInChangeAttribution int                   `json:"all_assets_in_change_attribution"`
	Checks                       []ExpertCheck         `json:"checks"`
	CausalClaim                  bool                  `json:"causal_claim"`
}

type evidenceDefinition struct{ key, label, description, claimScope string }

var evidenceDefinitions = map[string]evidenceDefinition{
	"MEASURED":   {"measured", "확정 계산", "선택 구간과 직전 동일 구간의 공개 체결·거래금액으로 직접 계산한 값입니다. 수치가 확인됐다는 뜻이지 외부 원인이 증명됐다는 뜻은 아닙니다.", "DIRECT_MEASUREMENT_OR_ACCOUNTING_ATTRIBUTION"},
	"DIAGNOSTIC": {"diagnostic", "진단 관측", "두 개 이상의 관측값을 비교해 조건에 맞는 상태를 표시한 결과입니다. 관측된 관계를 설명할 뿐 원인을 증명하지 않습니다.", "EXPLANATORY_SIGNAL_NOT_CAUSATION"},
	"CONTEXTUAL": {"contextual", "시간적 맥락", "공지·이벤트 시간과 점유율 변화가 같은 시간대인지 기록한 것입니다. 함께 발생했다는 사실만 보여주며 인과관계는 아닙니다.", "TEMPORAL_ASSOCIATION_ONLY"},
}

type listingSegmentDefinition struct{ key, label string }

var listingSegmentDefinitions = []listingSegmentDefinition{
	{"BITHUMB_EXCLUSIVE", "빗썸 단독상장"},
	{"BITHUMB_UPBIT_SHARED", "빗썸·업비트 동시상장"},
	{"BITHUMB_OTHER_SHARED", "빗썸·기타 거래소 상장"},
	{"BITHUMB_UNLISTED", "빗썸 미상장"},
}

type listingStructureDefinition struct {
	key      string
	label    string
	segments []string
}

var listingStructureDefinitions = []listingStructureDefinition{
	{"BITHUMB_EXCLUSIVE", "단독상장", []string{"BITHUMB_EXCLUSIVE"}},
	{"BITHUMB_SHARED", "동시상장", []string{"BITHUMB_UPBIT_SHARED", "BITHUMB_OTHER_SHARED"}},
	{"BITHUMB_UNLISTED", "미상장 공백", []string{"BITHUMB_UNLISTED"}},
}

func Finalize(result Analysis) Analysis {
	result.Findings, result.EvidenceLayers = separateFindings(result.Findings)
	result.ExpertBrief = buildExpertBrief(result)
	return result
}

func separateFindings(findings []Finding) ([]Finding, EvidenceLayers) {
	layers := EvidenceLayers{SeparationRule: "MEASURED는 수치 확인, DIAGNOSTIC은 설명 후보, CONTEXTUAL은 시간적 동시성으로만 해석합니다.", Measured: newEvidenceLayer("MEASURED"), Diagnostic: newEvidenceLayer("DIAGNOSTIC"), Contextual: newEvidenceLayer("CONTEXTUAL")}
	result := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		level := findingEvidence(finding.Kind)
		definition := evidenceDefinitions[level]
		finding.EvidenceLevel = level
		finding.EvidenceLabel = definition.label
		finding.ClaimScope = definition.claimScope
		finding.CausalClaim = false
		result = append(result, finding)
		switch level {
		case "MEASURED":
			layers.Measured.Items = append(layers.Measured.Items, finding)
		case "CONTEXTUAL":
			layers.Contextual.Items = append(layers.Contextual.Items, finding)
		default:
			layers.Diagnostic.Items = append(layers.Diagnostic.Items, finding)
		}
	}
	return result, layers
}
func newEvidenceLayer(level string) EvidenceLayer {
	d := evidenceDefinitions[level]
	return EvidenceLayer{Level: level, Label: d.label, Description: d.description, ClaimScope: d.claimScope, Items: []Finding{}}
}
func findingEvidence(kind string) string {
	switch kind {
	case "SHARE_LOSS_DRIVER", "SHARE_GAIN_DRIVER", "MOMENTUM":
		return "MEASURED"
	case "NOTICE_CONTEXT", "NOTICE_REACTION", "UPBIT_LISTING_PRESSURE":
		return "CONTEXTUAL"
	default:
		return "DIAGNOSTIC"
	}
}

func buildExpertBrief(result Analysis) ExpertBrief {
	changes := result.ChangeAttribution.Assets
	breadth := buildBreadth(changes)
	metadata := make(map[string]AssetAnalysis, len(result.Assets))
	for _, asset := range result.Assets {
		metadata[asset.Asset] = asset
	}
	segments := buildListingSegments(changes, metadata)
	positive, positiveCoverage := selectMajor(changes, metadata, true)
	negative, negativeCoverage := selectMajor(changes, metadata, false)
	major := make([]MajorAsset, 0, len(positive)+len(negative))
	major = append(major, positive...)
	major = append(major, negative...)
	sort.Slice(major, func(i, j int) bool { return math.Abs(major[i].ContributionPP) > math.Abs(major[j].ContributionPP) })
	selected := 0.0
	for _, row := range major {
		selected += math.Abs(row.ContributionPP)
	}
	headline := buildAttributionHeadline(result.ChangeAttribution, metadata, segments)
	brief := ExpertBrief{Status: result.ChangeAttribution.Status, Comparison: "선택 구간과 직전 동일 길이 구간", PrimaryDriver: result.ChangeAttribution.PrimaryDriver, MeasuredSummary: []ExpertSummary{}, DiagnosticSummary: []ExpertSummary{}, ContextualSummary: []ExpertSummary{}, MarketComparison: expertMarketComparison(result.ChangeAttribution), ShareBridge: expertShareBridge(result.ChangeAttribution), ExchangeComparison: result.ChangeAttribution.Exchanges, QuoteComparison: result.ChangeAttribution.Quotes, Breadth: breadth, ListingSegments: segments, AttributionHeadline: headline, MajorAssets: major, MajorAssetCoveragePct: percent(selected, breadth.TotalAbsoluteContributionPP), PositiveAssets: positive, PositiveAssetCoveragePct: positiveCoverage, NegativeAssets: negative, NegativeAssetCoveragePct: negativeCoverage, AllAssetsInChangeAttribution: len(changes), Checks: expertChecks(result, breadth, segments)}
	if result.ChangeAttribution.Status == "CONFIRMED" {
		a := result.ChangeAttribution
		brief.MeasuredSummary = append(brief.MeasuredSummary, ExpertSummary{Title: "점유율 변화", Detail: fmt.Sprintf("빗썸 점유율은 %.2f%%에서 %.2f%%로 %s 변했습니다.", pointerValue(a.PreviousSharePercent), pointerValue(a.CurrentSharePercent), expertPoints(pointerValue(a.ShareChangePP)))}, ExpertSummary{Title: "빗썸·경쟁 거래소 분해", Detail: fmt.Sprintf("빗썸 자체 거래금액 효과 %s, 경쟁 거래소 거래 변화 효과 %s입니다.", expertPoints(pointerValue(a.BithumbDirectEffectPP)), expertPoints(pointerValue(a.CompetitorEffectPP)))})
		if len(changes) > 0 {
			driver := changes[0]
			brief.MeasuredSummary = append(brief.MeasuredSummary, ExpertSummary{Title: "최대 변화 기여 코인 " + driver.Asset, Detail: fmt.Sprintf("전체 점유율 변화에 %s 기여했고, 빗썸 거래금액은 %s 변했습니다.", expertPoints(driver.ContributionPP), expertWon(driver.BithumbVolumeChangeKRW))})
		}
	}
	for _, check := range brief.Checks {
		brief.DiagnosticSummary = append(brief.DiagnosticSummary, ExpertSummary{Title: check.Title, Detail: check.Detail, Basis: check.Basis})
	}
	for _, finding := range result.EvidenceLayers.Contextual.Items {
		brief.ContextualSummary = append(brief.ContextualSummary, ExpertSummary{Title: finding.Title, Detail: finding.Detail})
	}
	return brief
}

func buildBreadth(rows []AttributionAsset) Breadth {
	b := Breadth{AssetCount: len(rows)}
	absolute := make([]float64, 0, len(rows))
	for _, row := range rows {
		value := row.ContributionPP
		absolute = append(absolute, math.Abs(value))
		b.TotalAbsoluteContributionPP += math.Abs(value)
		if value > 0 {
			b.PositiveAssetCount++
			b.PositiveContributionPP += value
		} else if value < 0 {
			b.NegativeAssetCount++
			b.NegativeContributionPP += value
		} else {
			b.FlatAssetCount++
		}
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(absolute)))
	if b.TotalAbsoluteContributionPP > 0 {
		b.Top1AbsoluteContributionSharePct = percent(sumFloat(absolute[:min(1, len(absolute))]), b.TotalAbsoluteContributionPP)
		b.Top5AbsoluteContributionSharePct = percent(sumFloat(absolute[:min(5, len(absolute))]), b.TotalAbsoluteContributionPP)
	}
	return b
}
func listingSegment(asset AssetAnalysis) string {
	if asset.ExclusiveOnBithumb {
		return "BITHUMB_EXCLUSIVE"
	}
	if asset.SharedBithumbUpbit {
		return "BITHUMB_UPBIT_SHARED"
	}
	if asset.ListedOnBithumb {
		return "BITHUMB_OTHER_SHARED"
	}
	return "BITHUMB_UNLISTED"
}
func buildListingSegments(changes []AttributionAsset, metadata map[string]AssetAnalysis) []ListingSegment {
	values := make(map[string]*ListingSegment)
	for _, item := range listingSegmentDefinitions {
		values[item.key] = &ListingSegment{Segment: item.key, Label: item.label}
	}
	for _, change := range changes {
		segment := values[listingSegment(metadata[change.Asset])]
		segment.AssetCount++
		segment.CurrentDomesticVolumeKRW += change.CurrentDomesticVolumeKRW
		segment.PreviousDomesticVolumeKRW += change.PreviousDomesticVolumeKRW
		segment.CurrentBithumbVolumeKRW += change.CurrentBithumbVolumeKRW
		segment.PreviousBithumbVolumeKRW += change.PreviousBithumbVolumeKRW
		segment.ContributionPP += change.ContributionPP
	}
	result := make([]ListingSegment, 0, 4)
	for _, item := range listingSegmentDefinitions {
		segment := values[item.key]
		segment.DomesticVolumeChangeKRW = segment.CurrentDomesticVolumeKRW - segment.PreviousDomesticVolumeKRW
		segment.BithumbVolumeChangeKRW = segment.CurrentBithumbVolumeKRW - segment.PreviousBithumbVolumeKRW
		result = append(result, *segment)
	}
	return result
}

func buildAttributionHeadline(attribution Attribution, metadata map[string]AssetAnalysis, segments []ListingSegment) AttributionHeadline {
	changes := attribution.Assets
	headline := AttributionHeadline{VolumeRelationship: attributionVolumeRelationship(attribution)}
	if len(changes) > 0 && math.Abs(changes[0].ContributionPP) > 0 {
		primary := majorAsset(changes[0], metadata)
		headline.PrimaryAsset = &primary
	}

	segmentByKey := make(map[string]ListingSegment, len(segments))
	for _, segment := range segments {
		segmentByKey[segment.Segment] = segment
	}
	structures := make([]ListingStructureHeadline, 0, len(listingStructureDefinitions))
	for _, definition := range listingStructureDefinitions {
		structure := ListingStructureHeadline{Structure: definition.key, Label: definition.label, LeadingAssets: []MajorAsset{}}
		for _, segmentKey := range definition.segments {
			segment := segmentByKey[segmentKey]
			structure.AssetCount += segment.AssetCount
			structure.CurrentDomesticVolumeKRW += segment.CurrentDomesticVolumeKRW
			structure.PreviousDomesticVolumeKRW += segment.PreviousDomesticVolumeKRW
			structure.CurrentBithumbVolumeKRW += segment.CurrentBithumbVolumeKRW
			structure.PreviousBithumbVolumeKRW += segment.PreviousBithumbVolumeKRW
			structure.ContributionPP += segment.ContributionPP
		}
		structure.DomesticVolumeChangeKRW = structure.CurrentDomesticVolumeKRW - structure.PreviousDomesticVolumeKRW
		structure.BithumbVolumeChangeKRW = structure.CurrentBithumbVolumeKRW - structure.PreviousBithumbVolumeKRW
		structures = append(structures, structure)
	}
	if len(structures) == 0 {
		return headline
	}
	sort.SliceStable(structures, func(i, j int) bool {
		return math.Abs(structures[i].ContributionPP) > math.Abs(structures[j].ContributionPP)
	})
	if math.Abs(structures[0].ContributionPP) == 0 {
		return headline
	}
	dominant := structures[0]
	definition := listingStructureDefinitionFor(dominant.Structure)
	for _, change := range changes {
		if len(dominant.LeadingAssets) >= 3 {
			break
		}
		segment := listingSegment(metadata[change.Asset])
		if !containsString(definition.segments, segment) || math.Signbit(change.ContributionPP) != math.Signbit(dominant.ContributionPP) || change.ContributionPP == 0 {
			continue
		}
		dominant.LeadingAssets = append(dominant.LeadingAssets, majorAsset(change, metadata))
	}
	headline.DominantListingStructure = &dominant
	return headline
}

func attributionVolumeRelationship(attribution Attribution) string {
	if attribution.Status != "CONFIRMED" || attribution.ShareChangePP == nil || attribution.BithumbVolumeChangeKRW == nil || attribution.MarketVolumeChangeKRW == nil {
		return ""
	}
	shareChange := *attribution.ShareChangePP
	bithumbChange := *attribution.BithumbVolumeChangeKRW
	marketChange := *attribution.MarketVolumeChangeKRW
	if shareChange > 0 && bithumbChange < 0 && marketChange < 0 {
		return "SHARE_UP_BOTH_VOLUMES_DOWN"
	}
	if shareChange < 0 && bithumbChange > 0 && marketChange > 0 {
		return "SHARE_DOWN_BOTH_VOLUMES_UP"
	}
	return ""
}

func listingStructureDefinitionFor(key string) listingStructureDefinition {
	for _, definition := range listingStructureDefinitions {
		if definition.key == key {
			return definition
		}
	}
	return listingStructureDefinition{}
}

func majorAsset(change AttributionAsset, metadata map[string]AssetAnalysis) MajorAsset {
	asset := metadata[change.Asset]
	return MajorAsset{AttributionAsset: change, ListingSegment: listingSegment(asset), DomesticExchanges: append([]string(nil), asset.DomesticExchanges...)}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func selectMajor(changes []AttributionAsset, metadata map[string]AssetAnalysis, positive bool) ([]MajorAsset, *float64) {
	rows := make([]AttributionAsset, 0)
	total := 0.0
	for _, row := range changes {
		if (positive && row.ContributionPP > 0) || (!positive && row.ContributionPP < 0) {
			rows = append(rows, row)
			total += math.Abs(row.ContributionPP)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return math.Abs(rows[i].ContributionPP) > math.Abs(rows[j].ContributionPP) })
	selected := 0.0
	result := []MajorAsset{}
	for _, row := range rows {
		if total <= 0 || selected/total >= .8 {
			break
		}
		asset := metadata[row.Asset]
		result = append(result, MajorAsset{AttributionAsset: row, ListingSegment: listingSegment(asset), DomesticExchanges: append([]string(nil), asset.DomesticExchanges...)})
		selected += math.Abs(row.ContributionPP)
	}
	return result, percent(selected, total)
}
func expertMarketComparison(a Attribution) []MarketComparison {
	return []MarketComparison{{"domestic", "국내 전체", a.PreviousVolumeKRW, a.CurrentVolumeKRW, a.MarketVolumeChangeKRW, a.MarketVolumeChangePercent}, {"bithumb", "빗썸", a.PreviousBithumbVolumeKRW, a.CurrentBithumbVolumeKRW, a.BithumbVolumeChangeKRW, a.BithumbVolumeChangePercent}, {"competitors", "경쟁 거래소 합계", a.PreviousCompetitorVolumeKRW, a.CurrentCompetitorVolumeKRW, a.CompetitorVolumeChangeKRW, a.CompetitorVolumeChangePercent}}
}
func expertShareBridge(a Attribution) []ShareBridge {
	return []ShareBridge{{"previous", "직전 점유율", a.PreviousSharePercent, "TOTAL"}, {"bithumb", "빗썸 거래금액 효과", a.BithumbDirectEffectPP, "CHANGE"}, {"competitors", "경쟁 거래소 거래 변화 효과", a.CompetitorEffectPP, "CHANGE"}, {"current", "현재 점유율", a.CurrentSharePercent, "TOTAL"}}
}

func expertChecks(result Analysis, b Breadth, segments []ListingSegment) []ExpertCheck {
	checks := make([]ExpertCheck, 0, 9)
	checks = appendIfPresent(checks, volumePaceCheck(result.ChangeAttribution))
	checks = append(checks, concentrationCheck(b))
	checks = appendIfPresent(checks, liquidityConversionCheck(result))
	checks = append(checks, tradeSizeCheck(result.Summary))
	checks = appendIfPresent(checks, aggressorFlowCheck(result.Exchanges))
	checks = appendIfPresent(checks, executionCostCheck(result.Liquidity))
	checks = append(checks, exclusiveListingCheck(b, segments))
	checks = appendIfPresent(checks, globalDemandCheck(result))
	checks = appendIfPresent(checks, quoteDriverCheck(result.ChangeAttribution.Quotes))
	return checks
}

func appendIfPresent(checks []ExpertCheck, check *ExpertCheck) []ExpertCheck {
	if check != nil {
		return append(checks, *check)
	}
	return checks
}

func volumePaceCheck(a Attribution) *ExpertCheck {
	if a.BithumbVolumeChangePercent == nil || a.CompetitorVolumeChangePercent == nil {
		return nil
	}
	gap := *a.BithumbVolumeChangePercent - *a.CompetitorVolumeChangePercent
	status := "NEUTRAL"
	if gap > 0 {
		status = "STRENGTH"
	} else if gap < 0 {
		status = "RISK"
	}
	check := newCheck("VOLUME_PACE_GAP", status, "빗썸과 경쟁 거래소의 거래금액 변화 속도", fmt.Sprintf("빗썸과 경쟁 거래소 합계의 거래금액 변화율 차이는 %+.2f%%p입니다.", gap), pointer(gap), "%p")
	return &check
}

func concentrationCheck(b Breadth) ExpertCheck {
	value, status := b.Top5AbsoluteContributionSharePct, "UNAVAILABLE"
	detail := "점유율 변화 기여 자료가 없어 코인 집중도를 계산하지 않습니다."
	if value != nil {
		status = "BROAD"
		if *value >= 70 {
			status = "CONCENTRATED"
		}
		detail = fmt.Sprintf("코인별 점유율 기여 절대값 중 상위 5개가 %.1f%%를 차지합니다.", *value)
	}
	return newCheck("DRIVER_CONCENTRATION", status, "점유율 변화의 코인 집중도", detail, value, "%")
}

func liquidityConversionCheck(result Analysis) *ExpertCheck {
	venue := liquidityExchange(result.Liquidity.Exchanges, "bithumb")
	if venue == nil {
		return nil
	}
	if venue.LiquiditySharePct == nil || result.Summary.BithumbSharePct == nil {
		check := newCheck("LIQUIDITY_CONVERSION_GAP", "UNAVAILABLE", "10단계 호가 비중과 거래금액 점유율 차이", "10단계 호가 비중 또는 거래금액 점유율 자료가 없어 차이를 계산하지 않습니다.", nil, "%p")
		return &check
	}
	gap := *venue.LiquiditySharePct - *result.Summary.BithumbSharePct
	status := "ALIGNED"
	if gap >= 2 {
		status = "CAPACITY"
	} else if gap <= -2 {
		status = "RISK"
	}
	check := newCheck("LIQUIDITY_CONVERSION_GAP", status, "10단계 호가 비중과 거래금액 점유율 차이", "빗썸의 10단계 호가 물량 비중에서 거래금액 점유율을 뺀 값입니다. 원인으로 단정하지 않습니다.", pointer(gap), "%p")
	return &check
}

func tradeSizeCheck(summary Summary) ExpertCheck {
	if summary.BithumbTradeCountSharePct == nil || summary.BithumbSharePct == nil {
		return newCheck("TRADE_SIZE_MIX", "UNAVAILABLE", "체결 건수 비중과 거래금액 비중", "체결 건수 비중 또는 거래금액 점유율 자료가 없어 주문 크기 구성을 계산하지 않습니다.", nil, "%p")
	}
	gap := *summary.BithumbTradeCountSharePct - *summary.BithumbSharePct
	status := "ALIGNED"
	if gap > 2 {
		status = "SMALLER_TICKET_MIX"
	} else if gap < -2 {
		status = "LARGER_TICKET_MIX"
	}
	return newCheck("TRADE_SIZE_MIX", status, "체결 건수 비중과 거래금액 비중", "빗썸 체결 건수 점유율에서 거래금액 점유율을 뺀 값입니다.", pointer(gap), "%p")
}

func aggressorFlowCheck(exchanges []ExchangeAnalysis) *ExpertCheck {
	var bithumb *ExchangeAnalysis
	competitorBuy, competitorSell := 0.0, 0.0
	for index := range exchanges {
		row := &exchanges[index]
		if row.Exchange == "bithumb" {
			bithumb = row
		} else {
			competitorBuy += row.BuyVolumeKRW
			competitorSell += row.SellVolumeKRW
		}
	}
	if bithumb == nil || bithumb.AggressorBuyPct == nil || competitorBuy+competitorSell <= 0 {
		return nil
	}
	competitorPct := competitorBuy / (competitorBuy + competitorSell) * 100
	gap := *bithumb.AggressorBuyPct - competitorPct
	status := "ALIGNED"
	if gap >= 5 {
		status = "BUY_FLOW_HEAVIER"
	} else if gap <= -5 {
		status = "SELL_FLOW_HEAVIER"
	}
	check := newCheck("AGGRESSOR_FLOW_GAP", status, "매수 주도 체결 흐름", "거래소가 제공한 매수 주도 체결 비율 차이이며 고객 순매수를 의미하지 않습니다.", pointer(gap), "%p")
	return &check
}

func executionCostCheck(liquidity LiquidityAnalysis) *ExpertCheck {
	bithumb := liquidityExchange(liquidity.Exchanges, "bithumb")
	if bithumb == nil || bithumb.MedianSpreadBPS == nil {
		return nil
	}
	competitor := make([]float64, 0)
	for index := range liquidity.Exchanges {
		row := &liquidity.Exchanges[index]
		if row.Exchange != "bithumb" && row.MedianSpreadBPS != nil {
			competitor = append(competitor, *row.MedianSpreadBPS)
		}
	}
	if len(competitor) == 0 {
		return nil
	}
	best := competitor[0]
	for _, value := range competitor[1:] {
		best = min(best, value)
	}
	gap := *bithumb.MedianSpreadBPS - best
	status := "ALIGNED"
	if gap < 0 {
		status = "STRENGTH"
	} else if gap > 0 {
		status = "RISK"
	}
	check := newCheck("EXECUTION_COST_GAP", status, "대표 매수·매도 가격 차이 비교", "빗썸의 대표 매수·매도 가격 차이와 경쟁 거래소 중 가장 낮은 값의 차이입니다. 확인된 10단계 호가만 사용합니다.", pointer(gap/100), "%")
	return &check
}

func exclusiveListingCheck(b Breadth, segments []ListingSegment) ExpertCheck {
	exclusive := 0.0
	for _, segment := range segments {
		if segment.Segment == "BITHUMB_EXCLUSIVE" {
			exclusive = max(0, segment.ContributionPP)
		}
	}
	dependency := percent(exclusive, b.PositiveContributionPP)
	status, detail := "UNAVAILABLE", "양(+)의 점유율 기여 자료가 없어 빗썸 단독상장 비중을 계산하지 않습니다."
	if dependency != nil {
		status = "DIVERSIFIED"
		if *dependency >= 50 {
			status = "CONCENTRATED"
		}
		detail = fmt.Sprintf("양(+)의 점유율 기여 중 빗썸 단독상장 자산 묶음의 비중이 %.1f%%입니다.", *dependency)
	}
	return newCheck("EXCLUSIVE_LISTING_DEPENDENCY", status, "양(+) 기여 중 빗썸 단독상장 비중", detail, dependency, "%")
}

func globalDemandCheck(result Analysis) *ExpertCheck {
	if result.Summary.BithumbSharePct == nil {
		return nil
	}
	var selected *AssetAnalysis
	for index := range result.Assets {
		asset := &result.Assets[index]
		if !asset.AssetIdentity.ContractIdentityVerified || asset.GlobalMinusDomesticInterestPP == nil || *asset.GlobalMinusDomesticInterestPP <= 0 || asset.BithumbCapturePct == nil || *asset.BithumbCapturePct >= *result.Summary.BithumbSharePct {
			continue
		}
		if selected == nil || *asset.GlobalMinusDomesticInterestPP > *selected.GlobalMinusDomesticInterestPP {
			selected = asset
		}
	}
	if selected == nil {
		return nil
	}
	check := newCheck("GLOBAL_DEMAND_CONVERSION_GAP", "OPPORTUNITY", selected.Asset+" 바이낸스 현물 관심 대비 국내·빗썸 거래 부족", "바이낸스 현물과 국내에서 같은 코인의 거래금액 비중 차이를 비교한 관측이며, 국내 유입이나 인과관계를 뜻하지 않습니다.", copyPointer(selected.GlobalMinusDomesticInterestPP), "%p")
	return &check
}

func quoteDriverCheck(quotes []AttributionQuote) *ExpertCheck {
	if len(quotes) == 0 {
		return nil
	}
	selected := quotes[0]
	for _, row := range quotes[1:] {
		if math.Abs(row.ContributionPP) > math.Abs(selected.ContributionPP) {
			selected = row
		}
	}
	check := newCheck("QUOTE_MARKET_DRIVER", "WATCH", selected.Quote+" 마켓 변화 기여", fmt.Sprintf("기준통화별 변화 중 %s 마켓의 점유율 기여가 %s로 가장 큽니다. 변화의 위치이지 원인을 뜻하지 않습니다.", selected.Quote, expertPoints(selected.ContributionPP)), pointer(selected.ContributionPP), "%p")
	return &check
}

func liquidityExchange(rows []LiquidityExchange, exchange string) *LiquidityExchange {
	for index := range rows {
		if string(rows[index].Exchange) == exchange {
			return &rows[index]
		}
	}
	return nil
}

// expertPoints writes a signed percentage-point change with two decimals,
// three only when two would print a non-zero change as 0.00 (the dashboard rule).
func expertPoints(value float64) string {
	decimals := 2
	if value != 0 && math.Round(math.Abs(value)*100) == 0 {
		decimals = 3
	}
	return fmt.Sprintf("%+.*f%%p", decimals, value)
}

// expertWon writes a signed whole-won change with thousands separators.
func expertWon(value float64) string {
	digits := strconv.FormatInt(int64(math.Round(math.Abs(value))), 10)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	sign := "+"
	if value < 0 {
		sign = "-"
	}
	return sign + digits + "원"
}

func newCheck(code, status, title, detail string, value *float64, unit string) ExpertCheck {
	return ExpertCheck{Code: code, EvidenceLevel: "DIAGNOSTIC", Status: status, Title: title, Detail: detail, Value: value, Unit: unit, Basis: []string{}, EvidenceLabel: "진단 관측"}
}
func sumFloat(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total
}
