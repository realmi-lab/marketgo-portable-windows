package analysis

import (
	"math"

	"market-share-go/internal/domain"
)

const numericIntegrityScope = "DOMESTIC_ANALYSIS_ARITHMETIC"

type NumericIntegrityIssue struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// NumericIntegrityResult describes arithmetic in one finalized analysis only.
// PASS does not certify observation coverage, exchange truth, or backfill dedupe.
type NumericIntegrityResult struct {
	Status            string                  `json:"status"`
	CheckedScope      string                  `json:"checked_scope"`
	MarketScope       string                  `json:"market_scope"`
	WindowSec         int64                   `json:"window_sec"`
	CheckCount        int                     `json:"check_count"`
	ComparisonChecked bool                    `json:"comparison_checked"`
	Issues            []NumericIntegrityIssue `json:"issues"`
}

// CheckNumericIntegrity checks final published amounts and shares without
// changing them or treating candle supplementation as exact trade coverage.
func CheckNumericIntegrity(value Analysis) NumericIntegrityResult {
	result := NumericIntegrityResult{
		Status: "PASS", CheckedScope: numericIntegrityScope,
		MarketScope: value.Context.MarketScope, WindowSec: value.WindowSec,
		Issues: []NumericIntegrityIssue{},
	}
	issue := func(status, code, field, message string) {
		result.Issues = append(result.Issues, NumericIntegrityIssue{Code: code, Field: field, Message: message})
		if status == "FAIL" || result.Status != "FAIL" {
			result.Status = status
		}
	}
	checkRange := func(field string, number, low, high float64) bool {
		result.CheckCount++
		if !finiteAnalysisNumbers(number) || number < low || number > high {
			issue("FAIL", "INVALID_NUMBER", field, "계산 값이 유효한 숫자 범위를 벗어났습니다.")
			return false
		}
		return true
	}
	checkPercent := func(field string, number *float64) bool {
		if number == nil {
			issue("UNAVAILABLE", "MISSING_VALUE", field, "점유율 계산 자료가 아직 없습니다.")
			return false
		}
		return checkRange(field, *number, 0, 100)
	}
	checkEqual := func(field string, actual, expected, absoluteTolerance, relativeTolerance float64) {
		result.CheckCount++
		tolerance := math.Max(absoluteTolerance, relativeTolerance*math.Max(math.Abs(actual), math.Abs(expected)))
		if !finiteAnalysisNumbers(actual, expected) || math.Abs(actual-expected) > tolerance {
			issue("FAIL", "CALCULATION_MISMATCH", field, "같은 구간의 금액·점유율 계산이 일치하지 않습니다.")
		}
	}
	if value.WindowSec <= 0 || (value.Context.MarketScope != "KRW" && value.Context.MarketScope != "ALL_DOMESTIC_CONVERTED") {
		issue("UNAVAILABLE", "MISSING_SCOPE", "context", "검사할 시장 범위와 기간이 아직 없습니다.")
	}
	totalValid := checkRange("summary.domestic_volume_krw", value.Summary.DomesticVolumeKRW, 0, math.MaxFloat64)
	bithumbValid := checkRange("summary.bithumb_volume_krw", value.Summary.BithumbVolumeKRW, 0, math.MaxFloat64)
	if totalValid && value.Summary.DomesticVolumeKRW == 0 {
		issue("UNAVAILABLE", "ZERO_TOTAL", "summary.domestic_volume_krw", "국내 거래금액이 0이므로 점유율을 검증할 수 없습니다.")
	}
	shareValid := checkPercent("summary.bithumb_share_pct", value.Summary.BithumbSharePct)
	rows := make(map[domain.Exchange]ExchangeAnalysis, len(value.Exchanges))
	allRowsValid, allSharesValid := true, true
	volumeSum, shareSum := 0.0, 0.0
	for _, row := range value.Exchanges {
		field := "exchanges." + string(row.Exchange)
		result.CheckCount++
		if !domain.ScopeMatchesExchange(domain.ScopeDomestic, row.Exchange) {
			issue("FAIL", "UNEXPECTED_EXCHANGE", field, "국내 점유율 표에 집계 범위 밖 거래소가 있습니다.")
			allRowsValid = false
			continue
		}
		if _, exists := rows[row.Exchange]; exists {
			issue("FAIL", "DUPLICATE_EXCHANGE", field, "국내 점유율 표에 같은 거래소가 두 번 있습니다.")
			allRowsValid = false
			continue
		}
		rows[row.Exchange] = row
		rowValid := checkRange(field+".volume_krw", row.VolumeKRW, 0, math.MaxFloat64)
		rowShareValid := checkPercent(field+".share_pct", row.SharePct)
		allRowsValid = allRowsValid && rowValid
		allSharesValid = allSharesValid && rowShareValid
		if rowValid {
			volumeSum += row.VolumeKRW
		}
		if rowShareValid {
			shareSum += *row.SharePct
		}
		if totalValid && value.Summary.DomesticVolumeKRW > 0 && rowValid && rowShareValid {
			checkEqual(field+".share_pct", *row.SharePct, row.VolumeKRW/value.Summary.DomesticVolumeKRW*100, 1e-9, 0)
		}
	}
	for _, exchange := range domain.DomesticExchanges {
		if _, exists := rows[exchange]; !exists {
			issue("UNAVAILABLE", "MISSING_EXCHANGE", "exchanges."+string(exchange), "국내 거래소별 계산 자료가 아직 모두 없습니다.")
			allRowsValid, allSharesValid = false, false
		}
	}
	// 1e-12 relative allows different nonnegative summation orders (1 KRW at
	// one trillion KRW); the 1e-6 KRW floor handles sub-won rounding. Shares
	// use 1e-9 percentage points, far below the displayed 0.01% precision.
	if totalValid && allRowsValid {
		checkEqual("domestic_exchange_volume_sum", volumeSum, value.Summary.DomesticVolumeKRW, 1e-6, 1e-12)
	}
	if row, exists := rows[domain.ExchangeBithumb]; exists && bithumbValid && finiteAnalysisNumbers(row.VolumeKRW) && row.VolumeKRW >= 0 {
		checkEqual("bithumb_exchange_volume", row.VolumeKRW, value.Summary.BithumbVolumeKRW, 1e-6, 1e-12)
	}
	if totalValid && value.Summary.DomesticVolumeKRW > 0 && bithumbValid && shareValid {
		checkEqual("summary.bithumb_share_pct", *value.Summary.BithumbSharePct, value.Summary.BithumbVolumeKRW/value.Summary.DomesticVolumeKRW*100, 1e-9, 0)
	}
	if totalValid && value.Summary.DomesticVolumeKRW > 0 && allRowsValid && allSharesValid {
		checkEqual("domestic_exchange_share_sum", shareSum, 100, 1e-9, 0)
	}
	attribution := value.ChangeAttribution
	if attribution.Status == "CONFIRMED" || attribution.Status == "PROVISIONAL" {
		currentTotalValid := checkRange("change_attribution.current_volume_krw", attribution.CurrentVolumeKRW, 0, math.MaxFloat64)
		previousTotalValid := false
		if attribution.PreviousVolumeKRW == nil {
			issue("UNAVAILABLE", "MISSING_VALUE", "change_attribution.previous_volume_krw", "직전 구간의 국내 거래금액 자료가 없습니다.")
		} else {
			previousTotalValid = checkRange("change_attribution.previous_volume_krw", *attribution.PreviousVolumeKRW, 0, math.MaxFloat64)
		}
		if currentTotalValid && attribution.CurrentVolumeKRW == 0 {
			issue("UNAVAILABLE", "ZERO_TOTAL", "change_attribution.current_volume_krw", "비교 구간의 국내 거래금액이 0이므로 변화를 검증할 수 없습니다.")
			currentTotalValid = false
		}
		if previousTotalValid && *attribution.PreviousVolumeKRW == 0 {
			issue("UNAVAILABLE", "ZERO_TOTAL", "change_attribution.previous_volume_krw", "직전 구간의 국내 거래금액이 0이므로 변화를 검증할 수 없습니다.")
			previousTotalValid = false
		}
		currentValid := checkPercent("change_attribution.current_share_pct", attribution.CurrentSharePercent)
		previousValid := checkPercent("change_attribution.previous_share_pct", attribution.PreviousSharePercent)
		deltaValid := false
		if attribution.ShareChangePP == nil {
			issue("UNAVAILABLE", "MISSING_VALUE", "change_attribution.share_change_pp", "직전 구간과의 점유율 변화 계산 자료가 없습니다.")
		} else {
			deltaValid = checkRange("change_attribution.share_change_pp", *attribution.ShareChangePP, -100, 100)
		}
		if currentTotalValid && previousTotalValid && currentValid && previousValid && deltaValid {
			checkEqual("change_attribution.share_change_pp", *attribution.ShareChangePP, *attribution.CurrentSharePercent-*attribution.PreviousSharePercent, 1e-9, 0)
			result.ComparisonChecked = true
		}
	}
	return result
}
