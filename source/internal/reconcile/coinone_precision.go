package reconcile

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
)

const StatusSourcePrecisionDifference = "SOURCE_PRECISION_DIFFERENCE"

// CoinonePrecisionEvidence binds raw decimals to the exact stored source row.
// A legacy float-only row is never assigned an inferred decimal precision.
func CoinonePrecisionEvidence(c OfficialMinuteCandle) (official.CoinoneDecimalEvidence, bool) {
	var e official.CoinoneDecimalEvidence
	if strings.HasPrefix(c.NotionalMethod, official.CoinonePrecisionMethodPrefix) {
		var err error
		e, err = official.DecodeCoinonePrecisionMethod(c.NotionalMethod)
		if err != nil {
			return e, false
		}
	} else if c.NotionalMethod == official.ExactQuoteNotional && c.DecimalEvidence != nil {
		e = *c.DecimalEvidence
	} else {
		return e, false
	}
	if e.Validate() != nil || c.Exchange != domain.ExchangeCoinone || c.SourceKind != official.CoinonePrecisionSource || c.Exchange != e.Exchange || c.Market != e.Market || c.Base != e.Base || c.Quote != e.Quote || c.MinuteStart < 0 || int64(c.MinuteStart) > math.MaxInt64/1000 || int64(c.MinuteStart)*1000 != e.MinuteStartMS || !c.QuoteVolumeExact || c.QuoteVolume == nil {
		return e, false
	}
	_, base, quote := e.Values()
	if c.BaseVolume != base || *c.QuoteVolume != quote {
		return e, false
	}
	for _, pair := range []struct {
		raw   string
		value *float64
	}{{e.Open, c.Open}, {e.High, c.High}, {e.Low, c.Low}, {e.Close, c.Close}} {
		v, err := strconv.ParseFloat(pair.raw, 64)
		if err != nil || pair.value == nil || *pair.value != v {
			return e, false
		}
	}
	return e, true
}

// WithCoinoneSingleTradePrecision changes only provenance, never source values.
// It requires the original WS row, one exact event, matching quantity/OHLC and
// an independently verified rational truncation relation to the raw API value.
func WithCoinoneSingleTradePrecision(ws *WSTradeMinute, c OfficialMinuteCandle) (OfficialMinuteCandle, bool) {
	e, ok := CoinonePrecisionEvidence(c)
	if !ok || ws == nil || ws.Exchange != c.Exchange || ws.Market != c.Market || ws.Base != c.Base || ws.Quote != c.Quote || ws.MinuteStart != c.MinuteStart || ws.SourceKind != SourceWebSocket || ws.DetailStatus != DetailObserved || ws.KRWVolume == nil || ws.FirstTradeTS == nil || ws.LastTradeTS == nil || *ws.FirstTradeTS != *ws.LastTradeTS || int64(*ws.FirstTradeTS)/1000 < int64(c.MinuteStart) || int64(*ws.LastTradeTS)/1000 >= int64(c.MinuteStart)+60 {
		return c, false
	}
	if !e.ProvesSingleTrade(ws.QuoteVolume, *ws.KRWVolume, ws.BaseVolume, ws.TradeCount, ws.UnresolvedTradeCount) {
		return c, false
	}
	price, _, _ := e.Values()
	for _, v := range []*float64{ws.Open, ws.High, ws.Low, ws.Close} {
		if v == nil || *v != price {
			return c, false
		}
	}
	method, err := e.Encode()
	if err != nil {
		return c, false
	}
	c.NotionalMethod = method
	return c, true
}

func precisionDifferenceDecision(ws *WSTradeMinute, c *OfficialMinuteCandle) (Decision, bool) {
	if c == nil || !strings.HasPrefix(c.NotionalMethod, official.CoinonePrecisionMethodPrefix) {
		return Decision{}, false
	}
	checked, ok := WithCoinoneSingleTradePrecision(ws, *c)
	if !ok {
		return Decision{}, false
	}
	e, _ := CoinonePrecisionEvidence(checked)
	baseDelta, quoteDelta := delta(ws, c.BaseVolume, false), delta(ws, *c.QuoteVolume, true)
	return Decision{
		Status:            StatusSourcePrecisionDifference,
		Reason:            fmt.Sprintf("Coinone raw quote %s is the exact four-decimal truncation of one observed trade; original WS amount retained; no supplemental amount or coverage granted", e.QuoteVolume),
		BaseVolumeMatches: boolPointer(true), QuoteVolumeMatches: boolPointer(false), OHLCMatches: boolPointer(true),
		BaseVolumeDelta: baseDelta, QuoteVolumeDelta: quoteDelta,
		EffectiveBaseVolume: floatPointer(ws.BaseVolume), EffectiveQuoteVolume: floatPointer(ws.QuoteVolume), EffectiveKRWVolume: cloneFloat(ws.KRWVolume),
		EffectiveSource: SourceWebSocket, TradeDetailStatus: DetailUnknown,
	}, true
}

func boolPointer(v bool) *bool { return &v }
