package reconcile

import (
	"math"
	"strings"
)

type Decision struct {
	Status                 string   `json:"status"`
	Reason                 string   `json:"reason"`
	BaseVolumeMatches      *bool    `json:"base_volume_matches"`
	QuoteVolumeMatches     *bool    `json:"quote_volume_matches"`
	OHLCMatches            *bool    `json:"ohlc_matches"`
	BaseVolumeDelta        *float64 `json:"base_volume_delta"`
	QuoteVolumeDelta       *float64 `json:"quote_volume_delta"`
	EffectiveBaseVolume    *float64 `json:"effective_base_volume"`
	EffectiveQuoteVolume   *float64 `json:"effective_quote_volume"`
	EffectiveKRWVolume     *float64 `json:"effective_krw_volume"`
	EffectiveTradeCount    *int64   `json:"effective_trade_count"`
	EffectiveBuyKRWVolume  *float64 `json:"effective_buy_krw_volume"`
	EffectiveSellKRWVolume *float64 `json:"effective_sell_krw_volume"`
	EffectiveSource        string   `json:"effective_source"`
	TradeDetailStatus      string   `json:"trade_detail_status"`
}

func Decide(ws *WSTradeMinute, candle *OfficialMinuteCandle, knownGapSeconds int64) Decision {
	return decide(ws, candle, knownGapSeconds)
}

// DecideWithObservedZero uses an exchange-minute seal as positive transport
// evidence only when the specific market has no known gap. The synthetic zero
// exists only for comparison; no per-market ws_trade_minutes row is created.
func DecideWithObservedZero(ws *WSTradeMinute, candle *OfficialMinuteCandle, knownGapSeconds int64, observedZero bool) Decision {
	if ws != nil || !observedZero || knownGapSeconds > 0 || candle == nil {
		return decide(ws, candle, knownGapSeconds)
	}
	zero := 0.0
	sealed := WSTradeMinute{
		Exchange: candle.Exchange, Market: candle.Market, Base: candle.Base, Quote: candle.Quote,
		MinuteStart: candle.MinuteStart, KRWVolume: &zero, BuyKRWVolume: &zero, SellKRWVolume: &zero,
		CoverageStatus: SealComplete, SourceKind: SourceWebSocketSeal, DetailStatus: DetailObservedZero,
	}
	result := decide(&sealed, candle, 0)
	if result.Status == StatusVerifiedWS {
		result.Reason = "complete minute seal and absent trade row prove an observed zero matching the official candle"
	}
	return result
}

func decide(ws *WSTradeMinute, candle *OfficialMinuteCandle, knownGapSeconds int64) Decision {
	gap := clampGap(knownGapSeconds)
	if ws != nil {
		if ws.GapSeconds > gap {
			gap = ws.GapSeconds
		}
		if gap == 0 && (ws.CoverageStatus == "PARTIAL" || ws.CoverageStatus == "MISSING" || ws.CoverageStatus == "GAPPED") {
			gap = 1
		}
	}
	if candle == nil {
		return withoutCandle(ws)
	}
	if precision, ok := precisionDifferenceDecision(ws, candle); ok {
		return precision
	}
	if strings.HasPrefix(candle.NotionalMethod, "COINONE_SINGLE_TRADE_QUOTE_TRUNCATION_") {
		result := withoutCandle(ws)
		result.Status = StatusMismatch
		result.Reason = "source precision evidence no longer proves one identical observed trade; no candle amount was added"
		return result
	}

	baseMatch := match(optionalWS(ws, func(v *WSTradeMinute) *float64 { return floatPointer(v.BaseVolume) }), floatPointer(candle.BaseVolume))
	var officialQuote *float64
	if candle.QuoteVolumeExact {
		officialQuote = candle.QuoteVolume
	}
	quoteMatch := match(optionalWS(ws, func(v *WSTradeMinute) *float64 { return floatPointer(v.QuoteVolume) }), officialQuote)
	ohlcMatch := allAvailable(
		match(optionalWS(ws, func(v *WSTradeMinute) *float64 { return v.Open }), candle.Open),
		match(optionalWS(ws, func(v *WSTradeMinute) *float64 { return v.High }), candle.High),
		match(optionalWS(ws, func(v *WSTradeMinute) *float64 { return v.Low }), candle.Low),
		match(optionalWS(ws, func(v *WSTradeMinute) *float64 { return v.Close }), candle.Close),
	)
	baseDelta := delta(ws, candle.BaseVolume, false)
	quoteDelta := delta(ws, valueOrZero(candle.QuoteVolume), true)
	if !candle.QuoteVolumeExact {
		quoteDelta = nil
	}

	if isBaseOnly(candle.Exchange) || !candle.QuoteVolumeExact {
		return baseOnly(ws, candle, baseMatch, ohlcMatch, baseDelta)
	}
	if gap > 0 {
		return Decision{
			Status: StatusRecoveredCandle, Reason: gapReason(gap), BaseVolumeMatches: baseMatch,
			QuoteVolumeMatches: quoteMatch, OHLCMatches: ohlcMatch, BaseVolumeDelta: baseDelta,
			QuoteVolumeDelta: quoteDelta, EffectiveBaseVolume: floatPointer(candle.BaseVolume),
			EffectiveQuoteVolume: cloneFloat(candle.QuoteVolume), EffectiveKRWVolume: krwQuote(candle),
			EffectiveSource: candle.SourceKind, TradeDetailStatus: DetailUnknown,
		}
	}
	if ws == nil {
		return Decision{Status: StatusWSMissing, Reason: "official candle exists but no proven WebSocket minute or gap is present", EffectiveSource: SourceNone, TradeDetailStatus: DetailUnknown}
	}
	verified := noFalse(baseMatch, quoteMatch, ohlcMatch) && anyTrue(baseMatch, quoteMatch, ohlcMatch)
	status, reason := StatusMismatch, "WebSocket and official aggregates diverge; the WebSocket source was retained"
	if verified {
		status, reason = StatusVerifiedWS, "WebSocket aggregate matches the exact official closed-minute candle"
	}
	result := fromWS(ws)
	result.Status, result.Reason = status, reason
	result.BaseVolumeMatches, result.QuoteVolumeMatches, result.OHLCMatches = baseMatch, quoteMatch, ohlcMatch
	result.BaseVolumeDelta, result.QuoteVolumeDelta = baseDelta, quoteDelta
	return result
}

func withoutCandle(ws *WSTradeMinute) Decision {
	if ws == nil {
		return Decision{Status: StatusCandleUnavailable, Reason: "official closed-minute candle is unavailable", EffectiveSource: SourceNone, TradeDetailStatus: DetailUnknown}
	}
	result := fromWS(ws)
	result.Status = StatusCandleUnavailable
	result.Reason = "official closed-minute candle is unavailable"
	return result
}

func baseOnly(ws *WSTradeMinute, candle *OfficialMinuteCandle, baseMatch, ohlcMatch *bool, baseDelta *float64) Decision {
	if ws == nil {
		return Decision{Status: StatusCandleBaseOnly, Reason: "official candle exposes base volume only; KRW turnover cannot be recovered", BaseVolumeMatches: baseMatch, OHLCMatches: ohlcMatch, BaseVolumeDelta: baseDelta, EffectiveBaseVolume: floatPointer(candle.BaseVolume), EffectiveSource: candle.SourceKind, TradeDetailStatus: DetailUnknown}
	}
	result := fromWS(ws)
	result.Status = StatusCandleBaseOnly
	result.Reason = "official candle is base-only and is diagnostic evidence, not a quote-volume replacement"
	result.BaseVolumeMatches, result.OHLCMatches, result.BaseVolumeDelta = baseMatch, ohlcMatch, baseDelta
	return result
}

func fromWS(ws *WSTradeMinute) Decision {
	count := ws.TradeCount
	source := ws.SourceKind
	if source == "" {
		source = SourceWebSocket
	}
	detail := ws.DetailStatus
	if detail == "" {
		detail = DetailObserved
	}
	return Decision{EffectiveBaseVolume: floatPointer(ws.BaseVolume), EffectiveQuoteVolume: floatPointer(ws.QuoteVolume), EffectiveKRWVolume: cloneFloat(ws.KRWVolume), EffectiveTradeCount: &count, EffectiveBuyKRWVolume: cloneFloat(ws.BuyKRWVolume), EffectiveSellKRWVolume: cloneFloat(ws.SellKRWVolume), EffectiveSource: source, TradeDetailStatus: detail}
}

func match(a, b *float64) *bool {
	if a == nil || b == nil {
		return nil
	}
	value := math.Abs(*a-*b) <= math.Max(1e-8, 1e-6*math.Max(math.Abs(*a), math.Abs(*b)))
	return &value
}

func allAvailable(values ...*bool) *bool {
	found, result := false, true
	for _, value := range values {
		if value != nil {
			found = true
			result = result && *value
		}
	}
	if !found {
		return nil
	}
	return &result
}

func noFalse(values ...*bool) bool {
	for _, value := range values {
		if value != nil && !*value {
			return false
		}
	}
	return true
}
func anyTrue(values ...*bool) bool {
	for _, value := range values {
		if value != nil && *value {
			return true
		}
	}
	return false
}
func clampGap(value int64) int64 {
	if value < 0 {
		return 0
	}
	if value > MinuteSeconds {
		return MinuteSeconds
	}
	return value
}
func optionalWS(ws *WSTradeMinute, get func(*WSTradeMinute) *float64) *float64 {
	if ws == nil {
		return nil
	}
	return get(ws)
}
func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
func delta(ws *WSTradeMinute, officialValue float64, quote bool) *float64 {
	if ws == nil {
		return nil
	}
	value := ws.BaseVolume
	if quote {
		value = ws.QuoteVolume
	}
	result := officialValue - value
	return &result
}
func krwQuote(c *OfficialMinuteCandle) *float64 {
	if c.Quote != "KRW" {
		return nil
	}
	return cloneFloat(c.QuoteVolume)
}
func gapReason(gap int64) string {
	return "known WebSocket gap (" + integer(gap) + "s) recovered from exact official aggregate"
}
