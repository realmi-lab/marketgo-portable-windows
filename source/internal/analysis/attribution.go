package analysis

import (
	"math"
	"sort"
	"strings"

	"market-share-go/internal/domain"
)

const ContributionMethod = "NORMALIZED_SHARE_EFFECT"

// ContributionMethodVersion identifies the current arithmetic contract of
// ChangeAttribution. Increment it when the coefficients or reconciliation
// arithmetic changes; presentation-only driver limits do not change it.
const ContributionMethodVersion = "NORMALIZED_SHARE_EFFECT_V1"
const ContributionNote = "점유율 기여도(%p)는 빗썸과 경쟁 거래소의 거래금액 변화를 전체 국내시장 거래금액에 정규화한 효과입니다. 거래금액 증감(원)은 원시 금액 차이이므로 두 값의 부호가 다를 수 있습니다."

// MixCaptureMethod names the second split of a share change. For each coin,
// with w its weight in domestic trading amount and c Bithumb's share inside
// the coin, mix = Δw·(c̄−S̄) and capture = w̄·Δc, where bars are the two
// windows' midpoints and S is Bithumb's whole share. A coin traded in only one
// window keeps the other window's c, so all of its change is mix. Summed over
// coins, mix plus capture equals the share change exactly.
const MixCaptureMethod = "MIDPOINT_MIX_CAPTURE_V1"

const (
	AssetExchangeNetworkBasis        = "NODE_TOTALS_SUM_RETURNED_EDGES_CURRENT_PREVIOUS_DOMESTIC_TRADING_AMOUNT_KRW"
	AssetExchangeNetworkSelection    = "TOP_16_CURRENT_DOMESTIC_TRADING_AMOUNT_UNION_TOP_16_ABSOLUTE_CHANGE_CAPPED_24_BY_BEST_SOURCE_RANK_ALL_ACTIVE_DOMESTIC_EXCHANGE_EDGES"
	AssetExchangeNetworkRelationship = "TRADING_AMOUNT_ASSOCIATION_NOT_ASSET_TRANSFER"
)

type Row struct {
	Scope     domain.Scope    `json:"scope"`
	Exchange  domain.Exchange `json:"exchange"`
	Market    string          `json:"market"`
	Base      string          `json:"base"`
	Quote     string          `json:"quote"`
	VolumeKRW float64         `json:"volume_krw"`
	// SupplementalVolumeKRW is the portion of VolumeKRW supplied by an exact
	// official candle/ticker source without trade-level detail. It carries no
	// invented trade count, side, or base quantity.
	SupplementalVolumeKRW        float64 `json:"supplemental_volume_krw,omitempty"`
	OfficialReplacementVolumeKRW float64 `json:"official_replacement_volume_krw,omitempty"`
	TradeDetailIncomplete        bool    `json:"trade_detail_incomplete,omitempty"`
	TradeCount                   int64   `json:"trade_count"`
	UnresolvedFXCount            int64   `json:"unresolved_fx_count"`
	BuyVolumeKRW                 float64 `json:"buy_volume_krw"`
	SellVolumeKRW                float64 `json:"sell_volume_krw"`
	BaseQuantity                 float64 `json:"base_quantity"`
}

type AssetIdentity struct {
	NormalizedSymbol         string `json:"normalized_symbol"`
	CanonicalAssetID         string `json:"canonical_asset_id"`
	ContractIdentityStatus   string `json:"contract_identity_status"`
	ContractIdentityVerified bool   `json:"contract_identity_verified"`
	Source                   string `json:"source"`
}

func Identity(asset string) AssetIdentity {
	return AssetIdentity{NormalizedSymbol: asset, CanonicalAssetID: "SYMBOL:" + asset, ContractIdentityStatus: "UNVERIFIED", ContractIdentityVerified: false, Source: "NORMALIZED_BASE_SYMBOL_ONLY"}
}

type AttributionAsset struct {
	Asset                         string        `json:"asset"`
	AssetIdentity                 AssetIdentity `json:"asset_identity"`
	ContributionPP                float64       `json:"contribution_pp"`
	BithumbDirectEffectPP         float64       `json:"bithumb_direct_effect_pp"`
	CompetitorEffectPP            float64       `json:"competitor_effect_pp"`
	DomesticMarketSizeEffectPP    float64       `json:"domestic_market_size_effect_pp"`
	CurrentDomesticVolumeKRW      float64       `json:"current_domestic_volume_krw"`
	PreviousDomesticVolumeKRW     float64       `json:"previous_domestic_volume_krw"`
	DomesticVolumeChangeKRW       float64       `json:"domestic_volume_change_krw"`
	DomesticVolumeChangePercent   *float64      `json:"domestic_volume_change_pct"`
	CurrentBithumbVolumeKRW       float64       `json:"current_bithumb_volume_krw"`
	PreviousBithumbVolumeKRW      float64       `json:"previous_bithumb_volume_krw"`
	BithumbVolumeChangeKRW        float64       `json:"bithumb_volume_change_krw"`
	BithumbVolumeChangePercent    *float64      `json:"bithumb_volume_change_pct"`
	CurrentUpbitVolumeKRW         float64       `json:"current_upbit_volume_krw"`
	PreviousUpbitVolumeKRW        float64       `json:"previous_upbit_volume_krw"`
	UpbitVolumeChangeKRW          float64       `json:"upbit_volume_change_krw"`
	UpbitVolumeChangePercent      *float64      `json:"upbit_volume_change_pct"`
	CurrentCompetitorVolumeKRW    float64       `json:"current_competitor_volume_krw"`
	PreviousCompetitorVolumeKRW   float64       `json:"previous_competitor_volume_krw"`
	CompetitorVolumeChangeKRW     float64       `json:"competitor_volume_change_krw"`
	CurrentBithumbCapturePercent  *float64      `json:"current_bithumb_capture_pct"`
	PreviousBithumbCapturePercent *float64      `json:"previous_bithumb_capture_pct"`
	BithumbCaptureChangePP        *float64      `json:"bithumb_capture_change_pp"`
	CurrentDomesticSharePercent   *float64      `json:"current_domestic_share_pct"`
	PreviousDomesticSharePercent  *float64      `json:"previous_domestic_share_pct"`
	CurrentTradeCount             int64         `json:"current_trade_count"`
	PreviousTradeCount            int64         `json:"previous_trade_count"`
	// MixEffectPP and CaptureEffectPP are this coin's terms of MixCaptureMethod.
	// They split the share change differently from ContributionPP, so a coin's
	// two terms need not add up to its ContributionPP; only the totals match.
	// Both are null when the window's split is not given.
	MixEffectPP        *float64 `json:"mix_effect_pp"`
	CaptureEffectPP    *float64 `json:"capture_effect_pp"`
	ContributionMethod string   `json:"contribution_method"`
	ContributionNote   string   `json:"contribution_note"`
}
type AttributionExchange struct {
	Exchange             domain.Exchange `json:"exchange"`
	ContributionPP       float64         `json:"contribution_pp"`
	CurrentVolumeKRW     float64         `json:"current_volume_krw"`
	PreviousVolumeKRW    float64         `json:"previous_volume_krw"`
	VolumeChangeKRW      float64         `json:"volume_change_krw"`
	VolumeChangePercent  *float64        `json:"volume_change_pct"`
	CurrentSharePercent  *float64        `json:"current_share_pct"`
	PreviousSharePercent *float64        `json:"previous_share_pct"`
	ShareChangePP        *float64        `json:"share_change_pp"`
}
type AttributionQuote struct {
	Quote                         string   `json:"quote"`
	ContributionPP                float64  `json:"contribution_pp"`
	CurrentVolumeKRW              float64  `json:"current_volume_krw"`
	PreviousVolumeKRW             float64  `json:"previous_volume_krw"`
	VolumeChangeKRW               float64  `json:"volume_change_krw"`
	VolumeChangePercent           *float64 `json:"volume_change_pct"`
	CurrentBithumbVolumeKRW       float64  `json:"current_bithumb_volume_krw"`
	PreviousBithumbVolumeKRW      float64  `json:"previous_bithumb_volume_krw"`
	BithumbVolumeChangeKRW        float64  `json:"bithumb_volume_change_krw"`
	CurrentBithumbCapturePercent  *float64 `json:"current_bithumb_capture_pct"`
	PreviousBithumbCapturePercent *float64 `json:"previous_bithumb_capture_pct"`
}

// AssetExchangeNetwork is a bipartite view of domestic trading amount by
// asset and exchange. An edge is an observed market association, not an
// exchange-to-exchange or wallet-to-wallet asset transfer.
type AssetExchangeNetwork struct {
	Status             string                     `json:"status"`
	UnavailableReason  string                     `json:"unavailable_reason,omitempty"`
	Basis              string                     `json:"basis"`
	Relationship       string                     `json:"relationship"`
	Selection          string                     `json:"selection"`
	TotalAssetCount    int                        `json:"total_asset_count"`
	ReturnedAssetCount int                        `json:"returned_asset_count"`
	TotalEdgeCount     int                        `json:"total_edge_count"`
	ReturnedEdgeCount  int                        `json:"returned_edge_count"`
	Nodes              []AssetExchangeNetworkNode `json:"nodes"`
	Edges              []AssetExchangeNetworkEdge `json:"edges"`
}

type AssetExchangeNetworkNode struct {
	ID                string  `json:"id"`
	Kind              string  `json:"kind"`
	Label             string  `json:"label"`
	CurrentVolumeKRW  float64 `json:"current_volume_krw"`
	PreviousVolumeKRW float64 `json:"previous_volume_krw"`
	VolumeChangeKRW   float64 `json:"volume_change_krw"`
	ContributionPP    float64 `json:"contribution_pp"`
	ConnectionCount   int     `json:"connection_count"`
}

type AssetExchangeNetworkEdge struct {
	Rank                int             `json:"rank"`
	ID                  string          `json:"id"`
	SourceID            string          `json:"source_id"`
	TargetID            string          `json:"target_id"`
	Asset               string          `json:"asset"`
	Exchange            domain.Exchange `json:"exchange"`
	CurrentVolumeKRW    float64         `json:"current_volume_krw"`
	PreviousVolumeKRW   float64         `json:"previous_volume_krw"`
	VolumeChangeKRW     float64         `json:"volume_change_krw"`
	VolumeChangePercent *float64        `json:"volume_change_pct"`
	ContributionPP      float64         `json:"contribution_pp"`
	VolumeDirection     string          `json:"volume_direction"`
	ShareEffect         string          `json:"share_effect"`
}

type Attribution struct {
	Status                        string                `json:"status"`
	UnavailableReason             string                `json:"unavailable_reason,omitempty"`
	ProvisionalReason             string                `json:"provisional_reason,omitempty"`
	IncludedExchanges             []domain.Exchange     `json:"included_exchanges,omitempty"`
	ExcludedExchanges             []domain.Exchange     `json:"excluded_exchanges,omitempty"`
	ComparisonWindowComplete      bool                  `json:"comparison_window_complete"`
	CurrentSharePercent           *float64              `json:"current_share_pct"`
	PreviousSharePercent          *float64              `json:"previous_share_pct"`
	ShareChangePP                 *float64              `json:"share_change_pp"`
	CurrentVolumeKRW              float64               `json:"current_volume_krw"`
	PreviousVolumeKRW             *float64              `json:"previous_volume_krw"`
	MarketVolumeChangeKRW         *float64              `json:"market_volume_change_krw"`
	MarketVolumeChangePercent     *float64              `json:"market_volume_change_pct"`
	CurrentBithumbVolumeKRW       float64               `json:"current_bithumb_volume_krw"`
	PreviousBithumbVolumeKRW      *float64              `json:"previous_bithumb_volume_krw"`
	BithumbVolumeChangeKRW        *float64              `json:"bithumb_volume_change_krw"`
	BithumbVolumeChangePercent    *float64              `json:"bithumb_volume_change_pct"`
	CurrentCompetitorVolumeKRW    float64               `json:"current_competitor_volume_krw"`
	PreviousCompetitorVolumeKRW   *float64              `json:"previous_competitor_volume_krw"`
	CompetitorVolumeChangeKRW     *float64              `json:"competitor_volume_change_krw"`
	CompetitorVolumeChangePercent *float64              `json:"competitor_volume_change_pct"`
	BithumbDirectEffectPP         *float64              `json:"bithumb_direct_effect_pp"`
	CompetitorEffectPP            *float64              `json:"competitor_effect_pp"`
	DomesticMarketSizeEffectPP    *float64              `json:"domestic_market_size_effect_pp"`
	ReconciliationErrorPP         *float64              `json:"reconciliation_error_pp"`
	MixEffectPP                   *float64              `json:"mix_effect_pp"`
	CaptureEffectPP               *float64              `json:"capture_effect_pp"`
	MixCaptureMethod              string                `json:"mix_capture_method,omitempty"`
	PrimaryDriver                 string                `json:"primary_driver"`
	Exchanges                     []AttributionExchange `json:"exchanges"`
	Assets                        []AttributionAsset    `json:"assets"`
	Quotes                        []AttributionQuote    `json:"quotes"`
	AssetExchangeNetwork          AssetExchangeNetwork  `json:"asset_exchange_network"`
	Method                        string                `json:"method"`
	ContributionMethod            string                `json:"contribution_method"`
	ContributionNote              string                `json:"contribution_note"`
}

// Reasons a comparison turns PROVISIONAL.
const (
	// ProvisionalIncompleteExchangeSet leaves out exchanges that are unhealthy now.
	ProvisionalIncompleteExchangeSet = "INCOMPLETE_EXCHANGE_SET"
	// ProvisionalMinorObservationGap leaves out small exchanges whose recorded
	// gaps explain every missing second of the comparison.
	ProvisionalMinorObservationGap = "MINOR_OBSERVATION_GAP"
	// ProvisionalObservedRange compares every exchange over the seconds that
	// were observed, when leaving exchanges out does not explain the missing
	// seconds. It leaves no exchange out.
	ProvisionalObservedRange = "OBSERVED_RANGE"
)

// Comparison is the exchange set a change comparison uses. It is complete,
// provisional (ProvisionalReason says why Excluded were left out) or neither,
// in which case the change is unavailable.
type Comparison struct {
	Complete          bool
	ProvisionalReason string
	Included          []domain.Exchange
	Excluded          []domain.Exchange
}

// CompareWindows attributes the change between the current and previous rows
// of a comparison, which hold only its included exchanges.
func CompareWindows(currentRows, previousRows []Row, comparison Comparison) Attribution {
	result := ChangeAttribution(currentRows, previousRows, comparison.Complete || comparison.ProvisionalReason != "")
	if comparison.ProvisionalReason == "" || result.Status != "CONFIRMED" {
		return result
	}
	result.Status = "PROVISIONAL"
	result.ProvisionalReason = comparison.ProvisionalReason
	result.IncludedExchanges = append([]domain.Exchange(nil), comparison.Included...)
	result.ExcludedExchanges = append([]domain.Exchange(nil), comparison.Excluded...)
	if result.AssetExchangeNetwork.Status == "CONFIRMED" {
		result.AssetExchangeNetwork.Status = "PROVISIONAL"
	}
	return result
}

type groupedValue struct {
	market, bithumb, upbit float64
	trades, bithumbTrades  int64
}

func ChangeAttribution(currentRows, previousRows []Row, complete bool) Attribution {
	current, currentInputValid := canonicalDomesticRows(currentRows)
	previous, previousInputValid := canonicalDomesticRows(previousRows)
	currentTotal, currentBithumb := totalAndBithumb(current)
	previousTotal, previousBithumb := totalAndBithumb(previous)
	currentReady := currentInputValid && rowsFXComplete(current)
	previousReady := previousInputValid && rowsFXComplete(previous)
	identityStable := domesticIdentityStable(current, previous)
	currentShare := optionalPercent(currentBithumb, currentTotal, currentReady)
	previousShare := optionalPercent(previousBithumb, previousTotal, previousReady)
	currentCompetitor := math.Max(0, currentTotal-currentBithumb)
	previousCompetitor := math.Max(0, previousTotal-previousBithumb)
	valid := complete && currentReady && previousReady && identityStable && currentTotal > 0 && previousTotal > 0
	unavailableReason := attributionUnavailableReason(complete, currentInputValid, previousInputValid, rowsFXComplete(current), rowsFXComplete(previous), identityStable, currentTotal, previousTotal)
	result := Attribution{Status: "UNAVAILABLE", UnavailableReason: unavailableReason, ComparisonWindowComplete: complete, CurrentSharePercent: currentShare, CurrentVolumeKRW: currentTotal, CurrentBithumbVolumeKRW: currentBithumb, CurrentCompetitorVolumeKRW: currentCompetitor, PrimaryDriver: "UNAVAILABLE", Exchanges: []AttributionExchange{}, Assets: []AttributionAsset{}, Quotes: []AttributionQuote{}, AssetExchangeNetwork: unavailableAssetExchangeNetwork(unavailableReason), Method: "빗썸 거래금액과 경쟁 거래소 거래금액의 선형경로 통합 기여도", ContributionMethod: ContributionMethod, ContributionNote: ContributionNote}
	if !valid {
		return result
	}
	bithumbCoefficient, competitorCoefficient := pathCoefficients(previousBithumb, previousCompetitor, currentBithumb, currentCompetitor)
	direct := (currentBithumb - previousBithumb) * bithumbCoefficient * 100
	competitor := (currentCompetitor - previousCompetitor) * competitorCoefficient * 100
	if !finiteAnalysisNumbers(bithumbCoefficient, competitorCoefficient, direct, competitor) {
		result.UnavailableReason = "NON_FINITE_ATTRIBUTION"
		result.AssetExchangeNetwork = unavailableAssetExchangeNetwork(result.UnavailableReason)
		return result
	}
	shareChange := *currentShare - *previousShare
	reconciliationError := shareChange - direct - competitor
	tolerance := math.Max(1e-10, math.Abs(shareChange)*1e-12)
	if !finiteAnalysisNumbers(shareChange, reconciliationError) || math.Abs(reconciliationError) > tolerance {
		result.UnavailableReason = "NUMERIC_RECONCILIATION_FAILURE"
		result.AssetExchangeNetwork = unavailableAssetExchangeNetwork(result.UnavailableReason)
		return result
	}
	result.Status = "CONFIRMED"
	result.UnavailableReason = ""
	result.PreviousSharePercent = previousShare
	result.ShareChangePP = pointer(shareChange)
	result.PreviousVolumeKRW = pointer(previousTotal)
	result.MarketVolumeChangeKRW = pointer(currentTotal - previousTotal)
	result.MarketVolumeChangePercent = changePercent(currentTotal, previousTotal)
	result.PreviousBithumbVolumeKRW = pointer(previousBithumb)
	result.BithumbVolumeChangeKRW = pointer(currentBithumb - previousBithumb)
	result.BithumbVolumeChangePercent = changePercent(currentBithumb, previousBithumb)
	result.PreviousCompetitorVolumeKRW = pointer(previousCompetitor)
	result.CompetitorVolumeChangeKRW = pointer(currentCompetitor - previousCompetitor)
	result.CompetitorVolumeChangePercent = changePercent(currentCompetitor, previousCompetitor)
	result.BithumbDirectEffectPP = pointer(direct)
	result.CompetitorEffectPP = pointer(competitor)
	result.DomesticMarketSizeEffectPP = pointer(competitor)
	result.ReconciliationErrorPP = pointer(reconciliationError)
	if direct == 0 && competitor == 0 {
		result.PrimaryDriver = "NO_CHANGE"
	} else if math.Abs(direct) == math.Abs(competitor) {
		result.PrimaryDriver = "BALANCED"
	} else if math.Abs(direct) > math.Abs(competitor) {
		result.PrimaryDriver = "BITHUMB_VOLUME_LED"
	} else {
		result.PrimaryDriver = "COMPETITOR_VOLUME_LED"
	}
	result.Assets = attributeAssets(current, previous, currentTotal, previousTotal, bithumbCoefficient, competitorCoefficient)
	if mix, capture, ok := applyMixCapture(result.Assets, previousTotal, currentTotal, *previousShare, *currentShare); ok {
		result.MixEffectPP, result.CaptureEffectPP = pointer(mix), pointer(capture)
		result.MixCaptureMethod = MixCaptureMethod
	}
	result.Exchanges = attributeExchanges(current, previous, currentTotal, previousTotal, bithumbCoefficient, competitorCoefficient)
	result.Quotes = attributeQuotes(current, previous, bithumbCoefficient, competitorCoefficient)
	result.AssetExchangeNetwork = buildAssetExchangeNetwork(current, previous, result.Assets, bithumbCoefficient, competitorCoefficient)
	return result
}

func attributionUnavailableReason(complete, currentInputValid, previousInputValid, currentFXComplete, previousFXComplete, identityStable bool, currentTotal, previousTotal float64) string {
	switch {
	case !complete:
		return "COMPARISON_WINDOW_INCOMPLETE"
	case !currentInputValid:
		return "CURRENT_DOMESTIC_INPUT_INVALID"
	case !previousInputValid:
		return "PREVIOUS_DOMESTIC_INPUT_INVALID"
	case !currentFXComplete:
		return "CURRENT_UNRESOLVED_FX"
	case !previousFXComplete:
		return "PREVIOUS_UNRESOLVED_FX"
	case !identityStable:
		return "CROSS_WINDOW_MARKET_IDENTITY_CONFLICT"
	case currentTotal <= 0:
		return "NO_CURRENT_DOMESTIC_VOLUME"
	case previousTotal <= 0:
		return "NO_PREVIOUS_DOMESTIC_VOLUME"
	default:
		return "UNAVAILABLE"
	}
}

func attributeAssets(current, previous []Row, currentTotal, previousTotal, bithumbCoefficient, competitorCoefficient float64) []AttributionAsset {
	currentValues := groupRows(current, func(row Row) string { return row.Base })
	previousValues := groupRows(previous, func(row Row) string { return row.Base })
	keys := unionKeys(currentValues, previousValues)
	result := make([]AttributionAsset, 0, len(keys))
	for _, asset := range keys {
		c, p := currentValues[asset], previousValues[asset]
		currentCompetitor := math.Max(0, c.market-c.bithumb)
		previousCompetitor := math.Max(0, p.market-p.bithumb)
		direct := (c.bithumb - p.bithumb) * bithumbCoefficient * 100
		competitor := (currentCompetitor - previousCompetitor) * competitorCoefficient * 100
		currentCapture, previousCapture := percent(c.bithumb, c.market), percent(p.bithumb, p.market)
		result = append(result, AttributionAsset{Asset: asset, AssetIdentity: Identity(asset), ContributionPP: direct + competitor, BithumbDirectEffectPP: direct, CompetitorEffectPP: competitor, DomesticMarketSizeEffectPP: competitor, CurrentDomesticVolumeKRW: c.market, PreviousDomesticVolumeKRW: p.market, DomesticVolumeChangeKRW: c.market - p.market, DomesticVolumeChangePercent: changePercent(c.market, p.market), CurrentBithumbVolumeKRW: c.bithumb, PreviousBithumbVolumeKRW: p.bithumb, BithumbVolumeChangeKRW: c.bithumb - p.bithumb, BithumbVolumeChangePercent: changePercent(c.bithumb, p.bithumb), CurrentUpbitVolumeKRW: c.upbit, PreviousUpbitVolumeKRW: p.upbit, UpbitVolumeChangeKRW: c.upbit - p.upbit, UpbitVolumeChangePercent: changePercent(c.upbit, p.upbit), CurrentCompetitorVolumeKRW: currentCompetitor, PreviousCompetitorVolumeKRW: previousCompetitor, CompetitorVolumeChangeKRW: currentCompetitor - previousCompetitor, CurrentBithumbCapturePercent: currentCapture, PreviousBithumbCapturePercent: previousCapture, BithumbCaptureChangePP: difference(currentCapture, previousCapture), CurrentDomesticSharePercent: percent(c.market, currentTotal), PreviousDomesticSharePercent: percent(p.market, previousTotal), CurrentTradeCount: c.trades, PreviousTradeCount: p.trades, ContributionMethod: ContributionMethod, ContributionNote: ContributionNote})
	}
	sort.Slice(result, func(i, j int) bool {
		return contributionPrecedes(result[i].ContributionPP, result[j].ContributionPP, result[i].Asset, result[j].Asset)
	})
	return result
}

// applyMixCapture fills each coin's MixCaptureMethod terms and returns their
// totals in percentage points. Shares are percentages of the whole domestic
// amount. ok is false, and every term stays zero, when the totals do not
// reconcile to the share change.
func applyMixCapture(assets []AttributionAsset, previousTotal, currentTotal, previousSharePct, currentSharePct float64) (mix, capture float64, ok bool) {
	if previousTotal <= 0 || currentTotal <= 0 {
		return 0, 0, false
	}
	midShare := (previousSharePct + currentSharePct) / 200
	mixes := make([]float64, len(assets))
	captures := make([]float64, len(assets))
	for index, asset := range assets {
		previousWeight := asset.PreviousDomesticVolumeKRW / previousTotal
		currentWeight := asset.CurrentDomesticVolumeKRW / currentTotal
		previousCapture, previousTraded := captureRatio(asset.PreviousBithumbVolumeKRW, asset.PreviousDomesticVolumeKRW)
		currentCapture, currentTraded := captureRatio(asset.CurrentBithumbVolumeKRW, asset.CurrentDomesticVolumeKRW)
		switch {
		case !previousTraded && !currentTraded:
			continue // no trading in either window: both terms are 0
		case !previousTraded:
			previousCapture = currentCapture
		case !currentTraded:
			currentCapture = previousCapture
		}
		mixes[index] = (currentWeight - previousWeight) * ((previousCapture+currentCapture)/2 - midShare) * 100
		captures[index] = (previousWeight + currentWeight) / 2 * (currentCapture - previousCapture) * 100
		mix += mixes[index]
		capture += captures[index]
	}
	shareChange := currentSharePct - previousSharePct
	if !finiteAnalysisNumbers(mix, capture) || math.Abs(mix+capture-shareChange) > math.Max(1e-8, math.Abs(shareChange)*1e-9) {
		return 0, 0, false // the coins' terms stay null
	}
	for index := range assets {
		assets[index].MixEffectPP, assets[index].CaptureEffectPP = pointer(mixes[index]), pointer(captures[index])
	}
	return mix, capture, true
}

func captureRatio(bithumb, domestic float64) (float64, bool) {
	if domestic <= 0 {
		return 0, false
	}
	return bithumb / domestic, true
}

func attributeExchanges(current, previous []Row, currentTotal, previousTotal, bithumbCoefficient, competitorCoefficient float64) []AttributionExchange {
	cValues := groupRows(current, func(row Row) string { return string(row.Exchange) })
	pValues := groupRows(previous, func(row Row) string { return string(row.Exchange) })
	result := make([]AttributionExchange, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		c, p := cValues[string(exchange)], pValues[string(exchange)]
		coefficient := competitorCoefficient
		if exchange == domain.ExchangeBithumb {
			coefficient = bithumbCoefficient
		}
		currentShare, previousShare := percent(c.market, currentTotal), percent(p.market, previousTotal)
		result = append(result, AttributionExchange{Exchange: exchange, ContributionPP: (c.market - p.market) * coefficient * 100, CurrentVolumeKRW: c.market, PreviousVolumeKRW: p.market, VolumeChangeKRW: c.market - p.market, VolumeChangePercent: changePercent(c.market, p.market), CurrentSharePercent: currentShare, PreviousSharePercent: previousShare, ShareChangePP: difference(currentShare, previousShare)})
	}
	sort.Slice(result, func(i, j int) bool {
		return contributionPrecedes(result[i].ContributionPP, result[j].ContributionPP, string(result[i].Exchange), string(result[j].Exchange))
	})
	return result
}

func attributeQuotes(current, previous []Row, bithumbCoefficient, competitorCoefficient float64) []AttributionQuote {
	cValues := groupRows(current, func(row Row) string { return row.Quote })
	pValues := groupRows(previous, func(row Row) string { return row.Quote })
	keys := unionKeys(cValues, pValues)
	result := make([]AttributionQuote, 0, len(keys))
	for _, quote := range keys {
		c, p := cValues[quote], pValues[quote]
		venueChange := c.bithumb - p.bithumb
		competitorChange := (c.market - c.bithumb) - (p.market - p.bithumb)
		result = append(result, AttributionQuote{Quote: quote, ContributionPP: (venueChange*bithumbCoefficient + competitorChange*competitorCoefficient) * 100, CurrentVolumeKRW: c.market, PreviousVolumeKRW: p.market, VolumeChangeKRW: c.market - p.market, VolumeChangePercent: changePercent(c.market, p.market), CurrentBithumbVolumeKRW: c.bithumb, PreviousBithumbVolumeKRW: p.bithumb, BithumbVolumeChangeKRW: c.bithumb - p.bithumb, CurrentBithumbCapturePercent: percent(c.bithumb, c.market), PreviousBithumbCapturePercent: percent(p.bithumb, p.market)})
	}
	sort.Slice(result, func(i, j int) bool {
		return contributionPrecedes(result[i].ContributionPP, result[j].ContributionPP, result[i].Quote, result[j].Quote)
	})
	return result
}

type assetExchangeNetworkKey struct {
	asset    string
	exchange domain.Exchange
}

type assetExchangeNetworkVolumes struct {
	current  float64
	previous float64
}

type assetExchangeNetworkCandidate struct {
	asset       string
	current     float64
	previous    float64
	currentRank int
	changeRank  int
}

func unavailableAssetExchangeNetwork(reason string) AssetExchangeNetwork {
	return AssetExchangeNetwork{
		Status:            "UNAVAILABLE",
		UnavailableReason: reason,
		Basis:             AssetExchangeNetworkBasis,
		Relationship:      AssetExchangeNetworkRelationship,
		Selection:         AssetExchangeNetworkSelection,
		Nodes:             []AssetExchangeNetworkNode{},
		Edges:             []AssetExchangeNetworkEdge{},
	}
}

func buildAssetExchangeNetwork(
	current, previous []Row,
	assets []AttributionAsset,
	bithumbCoefficient, competitorCoefficient float64,
) AssetExchangeNetwork {
	const (
		sourceAssetLimit   = 16
		returnedAssetLimit = 24
	)
	network := AssetExchangeNetwork{
		Status:       "CONFIRMED",
		Basis:        AssetExchangeNetworkBasis,
		Relationship: AssetExchangeNetworkRelationship,
		Selection:    AssetExchangeNetworkSelection,
		Nodes:        []AssetExchangeNetworkNode{},
		Edges:        []AssetExchangeNetworkEdge{},
	}

	currentValues := groupAssetExchangeNetworkRows(current)
	previousValues := groupAssetExchangeNetworkRows(previous)
	keys := make(map[assetExchangeNetworkKey]struct{}, len(currentValues)+len(previousValues))
	for key := range currentValues {
		keys[key] = struct{}{}
	}
	for key := range previousValues {
		keys[key] = struct{}{}
	}
	volumesByAsset := make(map[string]assetExchangeNetworkVolumes)
	for key := range keys {
		currentVolume, previousVolume := currentValues[key], previousValues[key]
		if currentVolume <= 0 && previousVolume <= 0 {
			continue
		}
		network.TotalEdgeCount++
		volume := volumesByAsset[key.asset]
		volume.current = finiteSaturatingAdd(volume.current, currentVolume)
		volume.previous = finiteSaturatingAdd(volume.previous, previousVolume)
		volumesByAsset[key.asset] = volume
	}
	network.TotalAssetCount = len(volumesByAsset)

	candidates := make([]assetExchangeNetworkCandidate, 0, len(volumesByAsset))
	for asset, volume := range volumesByAsset {
		candidates = append(candidates, assetExchangeNetworkCandidate{
			asset: asset, current: volume.current, previous: volume.previous,
		})
	}
	selected := selectAssetExchangeNetworkCandidates(candidates, sourceAssetLimit, returnedAssetLimit)
	network.ReturnedAssetCount = len(selected)
	selectedSet := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		selectedSet[candidate.asset] = struct{}{}
	}

	for key := range keys {
		if _, ok := selectedSet[key.asset]; !ok {
			continue
		}
		currentVolume, previousVolume := currentValues[key], previousValues[key]
		if currentVolume <= 0 && previousVolume <= 0 {
			continue
		}
		coefficient := competitorCoefficient
		if key.exchange == domain.ExchangeBithumb {
			coefficient = bithumbCoefficient
		}
		change := currentVolume - previousVolume
		contribution := change * coefficient * 100
		network.Edges = append(network.Edges, AssetExchangeNetworkEdge{
			ID:                  assetExchangeNetworkEdgeID(key.asset, key.exchange),
			SourceID:            "asset:" + key.asset,
			TargetID:            "exchange:" + string(key.exchange),
			Asset:               key.asset,
			Exchange:            key.exchange,
			CurrentVolumeKRW:    currentVolume,
			PreviousVolumeKRW:   previousVolume,
			VolumeChangeKRW:     change,
			VolumeChangePercent: changePercent(currentVolume, previousVolume),
			ContributionPP:      contribution,
			VolumeDirection:     assetExchangeNetworkVolumeDirection(change),
			ShareEffect:         assetExchangeNetworkShareEffect(contribution),
		})
	}
	sort.Slice(network.Edges, func(i, j int) bool {
		left, right := network.Edges[i], network.Edges[j]
		if left.CurrentVolumeKRW != right.CurrentVolumeKRW {
			return left.CurrentVolumeKRW > right.CurrentVolumeKRW
		}
		if math.Abs(left.VolumeChangeKRW) != math.Abs(right.VolumeChangeKRW) {
			return math.Abs(left.VolumeChangeKRW) > math.Abs(right.VolumeChangeKRW)
		}
		if math.Abs(left.ContributionPP) != math.Abs(right.ContributionPP) {
			return math.Abs(left.ContributionPP) > math.Abs(right.ContributionPP)
		}
		if left.Asset != right.Asset {
			return left.Asset < right.Asset
		}
		return left.Exchange < right.Exchange
	})
	for index := range network.Edges {
		network.Edges[index].Rank = index + 1
	}
	network.ReturnedEdgeCount = len(network.Edges)
	network.Nodes = buildAssetExchangeNetworkNodes(network.Edges, selected, assets)
	return network
}

func groupAssetExchangeNetworkRows(rows []Row) map[assetExchangeNetworkKey]float64 {
	result := make(map[assetExchangeNetworkKey]float64)
	for _, row := range rows {
		key := assetExchangeNetworkKey{asset: row.Base, exchange: row.Exchange}
		result[key] = finiteSaturatingAdd(result[key], row.VolumeKRW)
	}
	return result
}

func selectAssetExchangeNetworkCandidates(candidates []assetExchangeNetworkCandidate, sourceLimit, returnedLimit int) []assetExchangeNetworkCandidate {
	byCurrent := append([]assetExchangeNetworkCandidate(nil), candidates...)
	sort.Slice(byCurrent, func(i, j int) bool {
		if byCurrent[i].current != byCurrent[j].current {
			return byCurrent[i].current > byCurrent[j].current
		}
		if math.Abs(byCurrent[i].current-byCurrent[i].previous) != math.Abs(byCurrent[j].current-byCurrent[j].previous) {
			return math.Abs(byCurrent[i].current-byCurrent[i].previous) > math.Abs(byCurrent[j].current-byCurrent[j].previous)
		}
		return byCurrent[i].asset < byCurrent[j].asset
	})
	currentRanks := make(map[string]int, len(byCurrent))
	for index, candidate := range byCurrent {
		currentRanks[candidate.asset] = index + 1
	}

	byChange := append([]assetExchangeNetworkCandidate(nil), candidates...)
	sort.Slice(byChange, func(i, j int) bool {
		left := math.Abs(byChange[i].current - byChange[i].previous)
		right := math.Abs(byChange[j].current - byChange[j].previous)
		if left != right {
			return left > right
		}
		if byChange[i].current != byChange[j].current {
			return byChange[i].current > byChange[j].current
		}
		return byChange[i].asset < byChange[j].asset
	})
	changeRanks := make(map[string]int, len(byChange))
	for index, candidate := range byChange {
		changeRanks[candidate.asset] = index + 1
	}

	union := make(map[string]assetExchangeNetworkCandidate, min(len(candidates), sourceLimit*2))
	for index := 0; index < len(byCurrent) && index < sourceLimit; index++ {
		union[byCurrent[index].asset] = byCurrent[index]
	}
	for index := 0; index < len(byChange) && index < sourceLimit; index++ {
		union[byChange[index].asset] = byChange[index]
	}
	result := make([]assetExchangeNetworkCandidate, 0, len(union))
	for _, candidate := range union {
		candidate.currentRank = currentRanks[candidate.asset]
		candidate.changeRank = changeRanks[candidate.asset]
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		leftBest := min(result[i].currentRank, result[i].changeRank)
		rightBest := min(result[j].currentRank, result[j].changeRank)
		if leftBest != rightBest {
			return leftBest < rightBest
		}
		leftSum := result[i].currentRank + result[i].changeRank
		rightSum := result[j].currentRank + result[j].changeRank
		if leftSum != rightSum {
			return leftSum < rightSum
		}
		if result[i].current != result[j].current {
			return result[i].current > result[j].current
		}
		leftChange := math.Abs(result[i].current - result[i].previous)
		rightChange := math.Abs(result[j].current - result[j].previous)
		if leftChange != rightChange {
			return leftChange > rightChange
		}
		return result[i].asset < result[j].asset
	})
	if len(result) > returnedLimit {
		result = result[:returnedLimit]
	}
	return result
}

func buildAssetExchangeNetworkNodes(
	edges []AssetExchangeNetworkEdge,
	selected []assetExchangeNetworkCandidate,
	assets []AttributionAsset,
) []AssetExchangeNetworkNode {
	nodes := make(map[string]AssetExchangeNetworkNode, len(selected)+len(domain.DomesticExchanges))
	for _, edge := range edges {
		assetID := edge.SourceID
		assetNode := nodes[assetID]
		assetNode.ID, assetNode.Kind, assetNode.Label = assetID, "ASSET", edge.Asset
		assetNode.CurrentVolumeKRW += edge.CurrentVolumeKRW
		assetNode.PreviousVolumeKRW += edge.PreviousVolumeKRW
		assetNode.VolumeChangeKRW += edge.VolumeChangeKRW
		assetNode.ContributionPP += edge.ContributionPP
		assetNode.ConnectionCount++
		nodes[assetID] = assetNode

		exchangeID := edge.TargetID
		exchangeNode := nodes[exchangeID]
		exchangeNode.ID, exchangeNode.Kind, exchangeNode.Label = exchangeID, "EXCHANGE", string(edge.Exchange)
		exchangeNode.CurrentVolumeKRW += edge.CurrentVolumeKRW
		exchangeNode.PreviousVolumeKRW += edge.PreviousVolumeKRW
		exchangeNode.VolumeChangeKRW += edge.VolumeChangeKRW
		exchangeNode.ContributionPP += edge.ContributionPP
		exchangeNode.ConnectionCount++
		nodes[exchangeID] = exchangeNode
	}

	// Asset attribution is computed from the same complete set of returned
	// venue edges. Assigning its reconciled values prevents accumulation order
	// from changing the public node total at extreme floating-point scales.
	assetValues := make(map[string]AttributionAsset, len(assets))
	for _, asset := range assets {
		assetValues[asset.Asset] = asset
	}
	for _, candidate := range selected {
		id := "asset:" + candidate.asset
		node, ok := nodes[id]
		if !ok {
			continue
		}
		if value, found := assetValues[candidate.asset]; found {
			node.CurrentVolumeKRW = value.CurrentDomesticVolumeKRW
			node.PreviousVolumeKRW = value.PreviousDomesticVolumeKRW
			node.VolumeChangeKRW = value.DomesticVolumeChangeKRW
			node.ContributionPP = value.ContributionPP
		}
		nodes[id] = node
	}

	result := make([]AssetExchangeNetworkNode, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node)
	}
	assetOrder := make(map[string]int, len(selected))
	for index, candidate := range selected {
		assetOrder[candidate.asset] = index
	}
	exchangeOrder := make(map[string]int, len(domain.DomesticExchanges))
	for index, exchange := range domain.DomesticExchanges {
		exchangeOrder[string(exchange)] = index
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind == "EXCHANGE"
		}
		if result[i].Kind == "EXCHANGE" {
			return exchangeOrder[result[i].Label] < exchangeOrder[result[j].Label]
		}
		return assetOrder[result[i].Label] < assetOrder[result[j].Label]
	})
	return result
}

func assetExchangeNetworkEdgeID(asset string, exchange domain.Exchange) string {
	return "asset-exchange:" + asset + ":" + string(exchange)
}

func assetExchangeNetworkVolumeDirection(change float64) string {
	switch {
	case change > 0:
		return "INCREASE"
	case change < 0:
		return "DECREASE"
	default:
		return "UNCHANGED"
	}
}

func assetExchangeNetworkShareEffect(contribution float64) string {
	switch {
	case contribution > 0:
		return "RAISES_BITHUMB_SHARE"
	case contribution < 0:
		return "LOWERS_BITHUMB_SHARE"
	default:
		return "NEUTRAL"
	}
}

func canonicalDomesticRows(rows []Row) ([]Row, bool) {
	result := make([]Row, 0, len(rows))
	valid := true
	for _, row := range rows {
		if row.Scope != domain.ScopeDomestic {
			continue
		}
		row.Exchange = domain.Exchange(strings.ToLower(strings.TrimSpace(string(row.Exchange))))
		row.Market = strings.ToUpper(strings.TrimSpace(row.Market))
		row.Base = strings.ToUpper(strings.TrimSpace(row.Base))
		row.Quote = strings.ToUpper(strings.TrimSpace(row.Quote))
		if !isDomesticAnalysisExchange(row.Exchange) || row.Market == "" || row.Base == "" || row.Quote == "" ||
			!validAnalysisNumber(row.VolumeKRW) || !validAnalysisNumber(row.SupplementalVolumeKRW) ||
			!validAnalysisNumber(row.OfficialReplacementVolumeKRW) ||
			row.SupplementalVolumeKRW > row.VolumeKRW || !validAnalysisNumber(row.BuyVolumeKRW) ||
			!validAnalysisNumber(row.SellVolumeKRW) || !validAnalysisNumber(row.BaseQuantity) ||
			row.TradeCount < 0 || row.UnresolvedFXCount < 0 || row.UnresolvedFXCount > row.TradeCount ||
			(row.OfficialReplacementVolumeKRW > 0 && !row.TradeDetailIncomplete) ||
			(row.TradeCount == 0 && (row.VolumeKRW > row.SupplementalVolumeKRW+row.OfficialReplacementVolumeKRW || row.BuyVolumeKRW > 0 || row.SellVolumeKRW > 0 || row.BaseQuantity > 0)) {
			valid = false
			continue
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return analysisRowLess(result[i], result[j]) })
	unique := result[:0]
	for _, row := range result {
		if len(unique) > 0 && unique[len(unique)-1].Exchange == row.Exchange && unique[len(unique)-1].Market == row.Market {
			// Interval analysis accepts exactly one aggregate per logical market.
			// Quarantine a repeated/conflicting identity and keep one deterministic
			// raw row so a duplicate can never inflate the headline denominator.
			valid = false
			continue
		}
		unique = append(unique, row)
	}
	if !domesticTotalsFit(unique) {
		valid = false
	}
	return unique, valid
}

func isDomesticAnalysisExchange(exchange domain.Exchange) bool {
	for _, candidate := range domain.DomesticExchanges {
		if exchange == candidate {
			return true
		}
	}
	return false
}

func validAnalysisNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func analysisRowLess(left, right Row) bool {
	if left.Exchange != right.Exchange {
		return left.Exchange < right.Exchange
	}
	if left.Market != right.Market {
		return left.Market < right.Market
	}
	if left.Base != right.Base {
		return left.Base < right.Base
	}
	if left.Quote != right.Quote {
		return left.Quote < right.Quote
	}
	if left.VolumeKRW != right.VolumeKRW {
		return left.VolumeKRW < right.VolumeKRW
	}
	if left.SupplementalVolumeKRW != right.SupplementalVolumeKRW {
		return left.SupplementalVolumeKRW < right.SupplementalVolumeKRW
	}
	if left.OfficialReplacementVolumeKRW != right.OfficialReplacementVolumeKRW {
		return left.OfficialReplacementVolumeKRW < right.OfficialReplacementVolumeKRW
	}
	if left.TradeDetailIncomplete != right.TradeDetailIncomplete {
		return !left.TradeDetailIncomplete
	}
	if left.TradeCount != right.TradeCount {
		return left.TradeCount < right.TradeCount
	}
	if left.UnresolvedFXCount != right.UnresolvedFXCount {
		return left.UnresolvedFXCount < right.UnresolvedFXCount
	}
	if left.BuyVolumeKRW != right.BuyVolumeKRW {
		return left.BuyVolumeKRW < right.BuyVolumeKRW
	}
	if left.SellVolumeKRW != right.SellVolumeKRW {
		return left.SellVolumeKRW < right.SellVolumeKRW
	}
	return left.BaseQuantity < right.BaseQuantity
}

func domesticTotalsFit(rows []Row) bool {
	total, bithumb := 0.0, 0.0
	for _, row := range rows {
		if total > math.MaxFloat64-row.VolumeKRW {
			return false
		}
		total += row.VolumeKRW
		if row.Exchange == domain.ExchangeBithumb {
			if bithumb > math.MaxFloat64-row.VolumeKRW {
				return false
			}
			bithumb += row.VolumeKRW
		}
	}
	return true
}

func domesticIdentityStable(current, previous []Row) bool {
	type identity struct {
		base  string
		quote string
	}
	seen := make(map[string]identity, len(current)+len(previous))
	for _, rows := range [][]Row{previous, current} {
		for _, row := range rows {
			key := string(row.Exchange) + "\x00" + row.Market
			candidate := identity{base: row.Base, quote: row.Quote}
			if existing, ok := seen[key]; ok && existing != candidate {
				return false
			}
			seen[key] = candidate
		}
	}
	return true
}

func finiteAnalysisNumbers(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
func totalAndBithumb(rows []Row) (float64, float64) {
	total, bithumb := 0.0, 0.0
	for _, row := range rows {
		total = finiteSaturatingAdd(total, row.VolumeKRW)
		if row.Exchange == domain.ExchangeBithumb {
			bithumb = finiteSaturatingAdd(bithumb, row.VolumeKRW)
		}
	}
	return total, bithumb
}
func groupRows(rows []Row, key func(Row) string) map[string]groupedValue {
	result := map[string]groupedValue{}
	for _, row := range rows {
		name := key(row)
		value := result[name]
		value.market = finiteSaturatingAdd(value.market, row.VolumeKRW)
		value.trades = saturatingAddInt64(value.trades, row.TradeCount)
		if row.Exchange == domain.ExchangeBithumb {
			value.bithumb = finiteSaturatingAdd(value.bithumb, row.VolumeKRW)
			value.bithumbTrades = saturatingAddInt64(value.bithumbTrades, row.TradeCount)
		}
		if row.Exchange == domain.ExchangeUpbit {
			value.upbit = finiteSaturatingAdd(value.upbit, row.VolumeKRW)
		}
		result[name] = value
	}
	return result
}
func unionKeys(left, right map[string]groupedValue) []string {
	seen := map[string]struct{}{}
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
func pathCoefficients(previousBithumb, previousCompetitor, currentBithumb, currentCompetitor float64) (float64, float64) {
	previousTotal := previousBithumb + previousCompetitor
	currentTotal := currentBithumb + currentCompetitor
	if previousTotal <= 0 || currentTotal <= 0 {
		return 0, 0
	}

	change := currentTotal - previousTotal
	scale := max(previousTotal, currentTotal)
	if math.Abs(change) > scale*1e-8 {
		// Reparameterize the exact line integral using endpoint shares and the
		// log total-volume ratio. This avoids subtracting two huge, nearly equal
		// coefficient terms when the window totals differ by many orders of
		// magnitude while preserving each venue/competitor effect separately.
		deltaB := currentBithumb - previousBithumb
		allocation := deltaB / change
		shareDelta := currentBithumb/currentTotal - previousBithumb/previousTotal
		logRatio := math.Log(currentTotal) - math.Log(previousTotal)
		bithumb := ((1-allocation)*logRatio + shareDelta) / change
		competitor := (shareDelta - allocation*logRatio) / change
		return correctedPathCoefficients(bithumb, competitor, previousBithumb, previousCompetitor, currentBithumb, currentCompetitor)
	}

	// Normalize the near-constant-total branch so squared totals neither
	// overflow nor lose the smaller endpoint. Coefficients computed in
	// normalized units scale by 1/volume.
	previousB := previousBithumb / scale
	previousC := previousCompetitor / scale
	currentB := currentBithumb / scale
	currentC := currentCompetitor / scale
	previousD := previousTotal / scale
	currentD := currentTotal / scale

	inverseEndpointProduct := 1 / (previousD * currentD)
	centeredMoment := pathCenteredMoment(previousD, currentD)
	bithumb := ((previousC+currentC)/2*inverseEndpointProduct + (currentC-previousC)*centeredMoment) / scale
	competitor := -((previousB+currentB)/2*inverseEndpointProduct + (currentB-previousB)*centeredMoment) / scale

	return correctedPathCoefficients(bithumb, competitor, previousBithumb, previousCompetitor, currentBithumb, currentCompetitor)
}

func correctedPathCoefficients(bithumb, competitor, previousBithumb, previousCompetitor, currentBithumb, currentCompetitor float64) (float64, float64) {
	previousTotal := previousBithumb + previousCompetitor
	currentTotal := currentBithumb + currentCompetitor
	deltaB := currentBithumb - previousBithumb
	deltaC := currentCompetitor - previousCompetitor
	targetPP := currentBithumb/currentTotal*100 - previousBithumb/previousTotal*100
	for range 2 {
		actualPP := deltaB*bithumb*100 + deltaC*competitor*100
		residualPP := targetPP - actualPP
		if residualPP == 0 {
			break
		}
		if math.Abs(deltaB) >= math.Abs(deltaC) && deltaB != 0 {
			bithumb += (residualPP / 100) / deltaB
		} else if deltaC != 0 {
			competitor += (residualPP / 100) / deltaC
		} else {
			break
		}
	}
	return bithumb, competitor
}

// pathCenteredMoment returns integral((t-1/2)/D(t)^2, t=0..1). Centering
// separates the exactly constant-total case and avoids cancellation when the
// total changes by only a few ulps.
func pathCenteredMoment(previousTotal, currentTotal float64) float64 {
	middle := (previousTotal + currentTotal) / 2
	change := currentTotal - previousTotal
	if change == 0 {
		return 0
	}
	x := change / (previousTotal + currentTotal)
	if math.Abs(x) < 1e-3 {
		power := x
		xSquared := x * x
		sum := 0.0
		for order := 1; order <= 6; order++ {
			sum += float64(order) / float64(2*order+1) * power
			power *= xSquared
		}
		return -sum / (middle * middle)
	}
	logRatio := math.Log(currentTotal) - math.Log(previousTotal)
	return (logRatio - middle*change/previousTotal/currentTotal) / (change * change)
}

func contributionPrecedes(left, right float64, leftKey, rightKey string) bool {
	leftMagnitude, rightMagnitude := math.Abs(left), math.Abs(right)
	if leftMagnitude != rightMagnitude {
		return leftMagnitude > rightMagnitude
	}
	return leftKey < rightKey
}
func percent(part, whole float64) *float64 {
	if !validAnalysisNumber(part) || !validAnalysisNumber(whole) || whole <= 0 {
		return nil
	}
	value := part / whole * 100
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return pointer(value)
}
func difference(left, right *float64) *float64 {
	if left == nil || right == nil {
		return nil
	}
	value := *left - *right
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return pointer(value)
}
func changePercent(current, previous float64) *float64 {
	if !validAnalysisNumber(current) || !validAnalysisNumber(previous) {
		return nil
	}
	if previous <= 1 {
		return pointerIf(current <= 1, 0)
	}
	value := (current/previous - 1) * 100
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return pointer(value)
}
func pointerIf(condition bool, value float64) *float64 {
	if !condition {
		return nil
	}
	return &value
}
func pointer(value float64) *float64 { return &value }
