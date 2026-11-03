package app

import (
	a "market-share-go/internal/analysis"
	"regexp"
	"strings"
)

// These complete noun phrases distinguish a same-asset market denominator
// from an all-asset market denominator. A written asset must match the query
// AND an actual typed domestic amount; query metadata cannot create evidence.
var marketQuestionDomesticNamedAssetScope = regexp.MustCompile(`^(?:국내(?:동일코인인|같은코인인)?([A-Z][A-Z0-9]{1,11})(?:전체)?|([A-Z][A-Z0-9]{1,11})국내(?:전체)?)거래(?:대금|금액)$`)
var marketQuestionAssetRatioSubjectEnd = regexp.MustCompile(`(?i)^\s*(?:[A-Z][A-Z0-9]{1,11}\s+)?(?:업비트|빗썸|코인원|코빗|고팍스|upbit|bithumb|coinone|korbit|gopax)(?:의)?\s*(?:내부\s*)?(?:[A-Z][A-Z0-9]{1,11}(?:의)?\s*)?(?:포착률|(?:내부\s*)?비중|(?:전체\s*)?점유율(?:\s*수준\s*기여)?)(?:은|는)\s*`)
var marketQuestionAssetPrecedingOperand = regexp.MustCompile(`(?s)^\s*(.+?)\s*(?:대비|를\s*분모|을\s*분모)\s*(?:약\s*)?$`)

func marketQuestionHasDomesticAssetAmount(facts []marketQuestionDisplayFact, query a.MarketQuestionQuery) bool {
	for _, fact := range facts {
		if query.Asset != "" && fact.Asset == query.Asset && fact.Exchange == "domestic" && fact.Metric == "DOMESTIC_VOLUME_KRW" {
			return true
		}
	}
	return false
}

func marketQuestionTypedAssetDenominator(text string, facts []marketQuestionDisplayFact, query a.MarketQuestionQuery) string {
	text = strings.ToUpper(strings.Join(strings.Fields(strings.TrimSpace(text)), ""))
	for _, suffix := range []string{"입니다", "이다", "은", "는", "이", "가"} {
		if strings.HasSuffix(text, suffix) {
			text = strings.TrimSuffix(text, suffix)
			break
		}
	}
	hasDomesticAsset := marketQuestionHasDomesticAssetAmount(facts, query)
	if match := marketQuestionDomesticNamedAssetScope.FindStringSubmatch(text); len(match) > 0 {
		asset := match[1]
		if asset == "" {
			asset = match[2]
		}
		if asset == strings.ToUpper(query.Asset) && hasDomesticAsset {
			return "domestic_asset"
		}
		return "unverified_asset_scope"
	}
	for _, amount := range []string{"거래대금", "거래금액"} {
		for _, scope := range []string{"국내동일코인", "국내같은코인"} {
			if text == scope+amount && hasDomesticAsset {
				return "domestic_asset"
			}
		}
		for _, scope := range []string{"국내전체", "국내전체코인", "국내모든코인", "국내전체자산", "국내모든자산"} {
			if text == scope+amount {
				return "domestic"
			}
		}
		for name, exchange := range marketQuestionExchangeNames {
			for _, scope := range []string{"전체", "모든코인", "모든자산"} {
				if text == strings.ToUpper(name)+scope+amount {
					return exchange
				}
			}
		}
	}
	return "unverified_asset_scope"
}

func marketQuestionAssetTableDenominator(headers, cells []string, facts []marketQuestionDisplayFact, query a.MarketQuestionQuery) string {
	result := ""
	for i, header := range headers {
		header = strings.TrimSpace(header)
		if !strings.Contains(header, "분모") {
			continue
		}
		if header == "지표와 분모" && result == "" && i < len(cells) {
			_, operand, separated := strings.Cut(cells[i], "—")
			operand, _, scoped := strings.Cut(operand, " 중 ")
			if !separated || !scoped {
				return "unverified_asset_scope"
			}
			result = marketQuestionTypedAssetDenominator(operand, facts, query)
			continue
		}
		if header != "분모" || result != "" || i >= len(cells) {
			return "unverified_asset_scope"
		}
		result = marketQuestionTypedAssetDenominator(cells[i], facts, query)
	}
	return result
}

// A preceding denominator belongs to this number only when its comparison
// operator is directly adjacent. Resolve its asset identity before combining
// it with any following explanation or a separate table column.
func marketQuestionAssetPrecedingDenominator(prefix string, facts []marketQuestionDisplayFact, query a.MarketQuestionQuery) string {
	// The ratio subject is a separate numerator claim. Start its denominator
	// operand after the metric predicate, retaining every operand modifier.
	if end := marketQuestionAssetRatioSubjectEnd.FindStringIndex(prefix); end != nil {
		prefix = prefix[end[1]:]
	}
	// Consume the entire operand, not a later substring starting at a known
	// venue or 국내. An unknown leading modifier must reach the typed parser.
	if operand := marketQuestionAssetPrecedingOperand.FindStringSubmatch(prefix); operand != nil {
		return marketQuestionTypedAssetDenominator(operand[1], facts, query)
	}
	return ""
}
