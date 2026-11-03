package hermesrunner

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Quantities are paired with explicit units. This deliberately excludes
// calendar/time suffixes: their identity is checked separately, and a ticker
// or a timezone is not an amount. No asset-specific answer rules are used.
var (
	noticeExplicitQuantity    = regexp.MustCompile(`([+-]?(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?)\s*((?:[십백천만억조]\s*)*)(퍼센트포인트|퍼센트|포인트|토큰|코인|원|달러|개|건|명|회|종|배|%[pP]?|[A-Za-z][A-Za-z0-9]*)`)
	noticeQuantityAssetPrefix = regexp.MustCompile(`(?:^|[^A-Za-z0-9])([A-Z][A-Z0-9]{0,14})(?:은|는|을|를)?[\t ]+$`)
	noticeQuantityPrefix      = regexp.MustCompile(`([$₩€£¥]|[A-Za-z][A-Za-z0-9]{1,14})[\t ]+([+-]?(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?)|([$₩€£¥])([+-]?(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?)`)
	noticeQuantityPrefixScale = regexp.MustCompile(`^((?:[십백천만억조][\t ]*)+)`)
)

func validateNoticeQuestionQuantities(result noticeQuestionModelResult, evidenceJSON []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(evidenceJSON)))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return fmt.Errorf("decode notice quantity evidence: %w", err)
	}
	if err := expandNoticeQuantityVolumeTable(root); err != nil {
		return err
	}
	identifiers := map[string]bool{}
	var collectIdentifiers func(string, any)
	collectIdentifiers = func(key string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for childKey, child := range typed {
				collectIdentifiers(childKey, child)
			}
		case []any:
			for _, child := range typed {
				collectIdentifiers(key, child)
			}
		case string:
			if key == "assets" || key == "asset" || key == "writer_verified_assets" {
				identifiers[strings.ToUpper(noticeQuestionGroundingText(typed))] = true
			}
		}
	}
	collectIdentifiers("", root)
	allowed := map[string]bool{}
	var collect func(string, string, any)
	collect = func(parent, key string, value any) {
		if noticeQuestionNumericIdentityField(key) || key == "asset" {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for childKey, child := range typed {
				collect(key, childKey, child)
			}
		case []any:
			for _, child := range typed {
				collect(parent, key, child)
			}
		case string:
			if key == "assets" || key == "writer_verified_assets" {
				return
			}
			for _, quantity := range noticeQuestionExplicitQuantities(typed, identifiers) {
				allowed[quantity] = true
			}
		case json.Number:
			if unit := noticeQuestionQuantityFieldUnit(parent, key); unit != "" {
				if number, ok := normalizeNoticeQuestionNumericToken(string(typed)); ok {
					allowed[number+"|"+unit] = true
				}
			}
		}
	}
	collect("", "", root)
	text := noticeQuestionAnswerGroundingText(noticeQuestionResultText(result))
	for _, quantity := range noticeQuestionExplicitQuantities(text, identifiers) {
		if !allowed[quantity] {
			return fmt.Errorf("notice answer contains a quantity/unit pair absent from cited evidence: %s", quantity)
		}
	}
	return nil
}

// The compact payload is a self-describing table, not unlabelled amounts.
// Reconstruct its declared columns before deriving units, preserving the same
// field semantics as the ordinary daily_asset_volumes object representation.
func expandNoticeQuantityVolumeTable(root any) error {
	object, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	table, compact := object["daily_asset_volumes"].(map[string]any)
	if !compact {
		return nil
	}
	columns, ok := table["columns"].(map[string]any)
	if !ok {
		return fmt.Errorf("notice quantity volume table has no column schema")
	}
	dayColumns, dayOK := columns["days"].([]any)
	assetColumns, assetOK := columns["assets"].([]any)
	days, rowsOK := table["days"].([]any)
	if !dayOK || !assetOK || !rowsOK {
		return fmt.Errorf("notice quantity volume table has invalid columns or days")
	}
	decodeRow := func(names []any, values any, allowed map[string]bool) (map[string]any, error) {
		row, ok := values.([]any)
		if !ok || len(row) != len(names) || len(names) == 0 {
			return nil, fmt.Errorf("notice quantity volume row differs from its column schema")
		}
		decoded := make(map[string]any, len(names))
		for index, raw := range names {
			name, ok := raw.(string)
			if !ok || !allowed[name] {
				return nil, fmt.Errorf("notice quantity volume table has an unknown column")
			}
			if _, duplicate := decoded[name]; duplicate {
				return nil, fmt.Errorf("notice quantity volume table has a duplicate column: %s", name)
			}
			decoded[name] = row[index]
		}
		return decoded, nil
	}
	dayFields := map[string]bool{"day": true, "interval_start_kst": true, "interval_end_kst_exclusive": true, "coverage_scope": true, "status": true, "coverage_status": true, "coverage_sec": true, "required_sec": true, "observed_market_count": true, "error": true, "assets": true}
	assetFields := map[string]bool{"volume_krw": true, "trade_count": true, "market_count": true, "observed_market_count": true, "exchanges": true}
	decodedDays := make([]any, 0, len(days))
	for _, rawDay := range days {
		day, err := decodeRow(dayColumns, rawDay, dayFields)
		if err != nil {
			return err
		}
		if day["assets"] != nil {
			assets, ok := day["assets"].([]any)
			if !ok {
				return fmt.Errorf("notice quantity volume table has invalid asset rows")
			}
			decodedAssets := make([]any, 0, len(assets))
			for _, rawAsset := range assets {
				assetRow, ok := rawAsset.(map[string]any)
				if !ok {
					return fmt.Errorf("notice quantity volume table has an invalid asset row")
				}
				asset, err := decodeRow(assetColumns, assetRow["values"], assetFields)
				if err != nil {
					return err
				}
				identity, ok := assetRow["asset"].(string)
				if !ok || identity == "" {
					return fmt.Errorf("notice quantity volume table has no asset identity")
				}
				asset["asset"] = identity
				decodedAssets = append(decodedAssets, asset)
			}
			day["assets"] = decodedAssets
		}
		decodedDays = append(decodedDays, day)
	}
	object["daily_asset_volumes"] = decodedDays
	return nil
}

func noticeQuestionExplicitQuantities(text string, identifiers map[string]bool) []string {
	text = noticeQuestionGroundingText(text)
	quantities := []string{}
	for _, match := range noticeQuantityPrefix.FindAllStringSubmatchIndex(text, -1) {
		if match[0] > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:match[0]])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) {
				continue
			}
		}
		unitStart, unitEnd, numberStart, numberEnd := match[2], match[3], match[4], match[5]
		if unitStart < 0 {
			unitStart, unitEnd, numberStart, numberEnd = match[6], match[7], match[8], match[9]
		}
		unit := strings.ToUpper(text[unitStart:unitEnd])
		symbolic := strings.ContainsAny(unit, "$₩€£¥")
		switch unit {
		case "$":
			unit = "USD"
		case "₩":
			unit = "KRW"
		case "€":
			unit = "EUR"
		case "£":
			unit = "GBP"
		case "¥":
			unit = "JPY"
		case "KST", "UTC", "GMT":
			continue
		}
		// A following unit/date identifies what this number measures (ABC
		// 10원, BTC 2026년). The suffix path validates that explicit quantity.
		tail := strings.TrimLeft(text[numberEnd:], " \t")
		scale := ""
		if match := noticeQuantityPrefixScale.FindStringSubmatch(tail); len(match) > 1 {
			scale = strings.Join(strings.Fields(match[1]), "")
			tail = tail[len(match[0]):]
		}
		if len(tail) > 0 && !symbolic {
			r, _ := utf8.DecodeRuneInString(tail)
			if unicode.IsDigit(r) || strings.ContainsRune("년월일시분초원개건명회종배%십백천만억조:./", r) || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				continue
			}
		}
		if number, ok := normalizeNoticeQuestionNumericToken(text[numberStart:numberEnd]); ok {
			if scale != "" {
				unit = scale + "*" + unit
			}
			quantities = append(quantities, number+"|"+unit)
		}
	}
	for _, match := range noticeExplicitQuantity.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		// Do not read the suffix of a ticker/version/date/time as a quantity.
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			if previous >= 'A' && previous <= 'Z' || previous >= 'a' && previous <= 'z' || unicode.IsDigit(previous) || previous == ':' || previous == '/' || previous == '.' {
				continue
			}
		}
		if identifiers[strings.ToUpper(text[start:end])] {
			continue
		}
		if end < len(text) && (text[end] >= 'A' && text[end] <= 'Z' || text[end] >= 'a' && text[end] <= 'z' || text[end] >= '0' && text[end] <= '9') {
			continue
		}
		number, ok := normalizeNoticeQuestionNumericToken(text[match[2]:match[3]])
		if !ok {
			continue
		}
		scale := strings.Join(strings.Fields(text[match[4]:match[5]]), "")
		unit := strings.ToUpper(text[match[6]:match[7]])
		switch unit {
		case "KST", "UTC", "GMT":
			continue
		case "원":
			unit = "KRW"
		case "달러":
			unit = "USD"
		case "퍼센트":
			unit = "%"
		case "퍼센트포인트", "%P":
			unit = "%P"
		case "개", "토큰", "코인":
			// "ABC 10개" explicitly identifies the same asset as "10 ABC".
			// An isolated "10개" never inherits an asset from a different row.
			if prefix := noticeQuantityAssetPrefix.FindStringSubmatch(text[:start]); len(prefix) > 1 {
				unit = prefix[1]
			}
		}
		if scale != "" {
			unit = scale + "*" + unit
		}
		quantities = append(quantities, number+"|"+unit)
	}
	return quantities
}

// A structured field can license a unit only when its schema names that unit
// or counted entity. Unlabelled numbers and arbitrary *_count values do not.
func noticeQuestionQuantityFieldUnit(parent, key string) string {
	if strings.HasSuffix(key, "_krw") {
		return "KRW"
	}
	if strings.HasSuffix(key, "_percent") {
		return "%"
	}
	if strings.HasSuffix(key, "_notice_count") {
		return "건"
	}
	switch key {
	case "trade_count", "deposit_suspension_count", "deposit_resumption_count", "caution_designation_count", "caution_extension_count", "caution_release_count", "important_trading_count", "important_risk_count", "important_operation_count":
		return "건"
	case "market_count", "observed_market_count":
		return "개"
	case "count":
		if parent == "exchange_notice_counts" {
			return "건"
		}
	}
	return ""
}
