package reconcile

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
)

const (
	MinuteSeconds = int64(60)
	MinuteMS      = int64(60_000)

	SourceWebSocket        = "WEBSOCKET_TRADE"
	SourceWebSocketSeal    = "WEBSOCKET_MINUTE_SEAL"
	SourceOfficialCandle   = "OFFICIAL_MINUTE_CANDLE"
	SourceOfficialBaseOnly = "OFFICIAL_CANDLE_BASE_ONLY"
	SourceNone             = "NONE"

	DetailObserved     = "OBSERVED"
	DetailObservedZero = "OBSERVED_ZERO"
	DetailUnknown      = "UNKNOWN"

	StatusVerifiedWS        = "VERIFIED_WS"
	StatusMismatch          = "MISMATCH"
	StatusWSMissing         = "WS_MISSING_CANDLE_PRESENT"
	StatusRecoveredCandle   = "RECOVERED_CANDLE"
	StatusReplacedCandle    = "REPLACED_OFFICIAL_MINUTE"
	StatusCandleBaseOnly    = "CANDLE_BASE_ONLY"
	StatusCandleUnavailable = "CANDLE_UNAVAILABLE"
	StatusIncompleteBatch   = "INCOMPLETE_BATCH"
)

type WSTradeMinute struct {
	Exchange             domain.Exchange   `json:"exchange"`
	Market               string            `json:"market"`
	Base                 string            `json:"base"`
	Quote                string            `json:"quote"`
	MinuteStart          domain.UnixSecond `json:"minute_start"`
	Open                 *float64          `json:"open"`
	High                 *float64          `json:"high"`
	Low                  *float64          `json:"low"`
	Close                *float64          `json:"close"`
	BaseVolume           float64           `json:"base_volume"`
	QuoteVolume          float64           `json:"quote_volume"`
	KRWVolume            *float64          `json:"krw_volume"`
	TradeCount           int64             `json:"trade_count"`
	BuyKRWVolume         *float64          `json:"buy_krw_volume"`
	SellKRWVolume        *float64          `json:"sell_krw_volume"`
	FirstTradeTS         *domain.UnixMilli `json:"first_trade_ts_ms"`
	LastTradeTS          *domain.UnixMilli `json:"last_trade_ts_ms"`
	UnresolvedTradeCount int64             `json:"unresolved_trade_count"`
	CoverageStatus       string            `json:"coverage_status"`
	GapSeconds           int64             `json:"gap_seconds"`
	SourceKind           string            `json:"source_kind"`
	DetailStatus         string            `json:"detail_status"`
	CreatedAt            time.Time         `json:"created_at"`
}

func (m *WSTradeMinute) Normalize() error {
	if m == nil {
		return errors.New("WebSocket minute is nil")
	}
	m.Market = strings.TrimSpace(m.Market)
	m.Base = strings.ToUpper(strings.TrimSpace(m.Base))
	m.Quote = strings.ToUpper(strings.TrimSpace(m.Quote))
	if !m.Exchange.Valid() || m.Market == "" || m.Base == "" || m.Quote == "" {
		return errors.New("WebSocket minute identity is invalid")
	}
	if err := validateMinute(m.MinuteStart); err != nil {
		return err
	}
	for name, value := range map[string]*float64{"open": m.Open, "high": m.High, "low": m.Low, "close": m.Close} {
		if value != nil && !positive(*value) {
			return fmt.Errorf("%s must be positive and finite", name)
		}
	}
	if !nonnegative(m.BaseVolume) || !nonnegative(m.QuoteVolume) {
		return errors.New("WebSocket minute volumes must be non-negative and finite")
	}
	for name, value := range map[string]*float64{"krw_volume": m.KRWVolume, "buy_krw_volume": m.BuyKRWVolume, "sell_krw_volume": m.SellKRWVolume} {
		if value != nil && !nonnegative(*value) {
			return fmt.Errorf("%s must be non-negative and finite", name)
		}
	}
	if m.TradeCount < 0 || m.UnresolvedTradeCount < 0 {
		return errors.New("trade counts must be non-negative")
	}
	if m.GapSeconds < 0 || m.GapSeconds > MinuteSeconds {
		return errors.New("gap seconds must be between 0 and 60")
	}
	if m.CoverageStatus == "" {
		m.CoverageStatus = DetailObserved
	}
	m.CoverageStatus = strings.ToUpper(m.CoverageStatus)
	m.SourceKind = SourceWebSocket
	m.DetailStatus = DetailObserved
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	return nil
}

type OfficialMinuteCandle struct {
	Exchange           domain.Exchange   `json:"exchange"`
	Market             string            `json:"market"`
	Base               string            `json:"base"`
	Quote              string            `json:"quote"`
	MinuteStart        domain.UnixSecond `json:"minute_start"`
	Open               *float64          `json:"open"`
	High               *float64          `json:"high"`
	Low                *float64          `json:"low"`
	Close              *float64          `json:"close"`
	BaseVolume         float64           `json:"base_volume"`
	QuoteVolume        *float64          `json:"quote_volume"`
	QuoteVolumeExact   bool              `json:"quote_volume_exact"`
	SourceKind         string            `json:"source_kind"`
	NotionalMethod     string            `json:"notional_method"`
	ObservationAttempt int               `json:"observation_attempt"`
	FetchedAt          time.Time         `json:"fetched_at"`
	CreatedAt          time.Time         `json:"created_at"`
	// Ephemeral parser evidence. Only a proved exception is persisted in the
	// versioned NotionalMethod envelope; ordinary legacy rows are unchanged.
	DecimalEvidence *official.CoinoneDecimalEvidence `json:"decimal_evidence,omitempty"`
}

type Result struct {
	Exchange               domain.Exchange   `json:"exchange"`
	Market                 string            `json:"market"`
	Base                   string            `json:"base"`
	Quote                  string            `json:"quote"`
	MinuteStart            domain.UnixSecond `json:"minute_start"`
	Status                 string            `json:"status"`
	Reason                 string            `json:"reason"`
	WSPresent              bool              `json:"ws_present"`
	OfficialPresent        bool              `json:"official_present"`
	KnownGapSeconds        int64             `json:"known_gap_seconds"`
	BaseVolumeMatches      *bool             `json:"base_volume_matches"`
	QuoteVolumeMatches     *bool             `json:"quote_volume_matches"`
	OHLCMatches            *bool             `json:"ohlc_matches"`
	WSBaseVolume           *float64          `json:"ws_base_volume"`
	WSQuoteVolume          *float64          `json:"ws_quote_volume"`
	WSTradeCount           *int64            `json:"ws_trade_count"`
	OfficialBaseVolume     *float64          `json:"official_base_volume"`
	OfficialQuoteVolume    *float64          `json:"official_quote_volume"`
	BaseVolumeDelta        *float64          `json:"base_volume_delta"`
	QuoteVolumeDelta       *float64          `json:"quote_volume_delta"`
	EffectiveBaseVolume    *float64          `json:"effective_base_volume"`
	EffectiveQuoteVolume   *float64          `json:"effective_quote_volume"`
	EffectiveKRWVolume     *float64          `json:"effective_krw_volume"`
	EffectiveTradeCount    *int64            `json:"effective_trade_count"`
	EffectiveBuyKRWVolume  *float64          `json:"effective_buy_krw_volume"`
	EffectiveSellKRWVolume *float64          `json:"effective_sell_krw_volume"`
	EffectiveSource        string            `json:"effective_source"`
	TradeDetailStatus      string            `json:"trade_detail_status"`
	CheckedAt              time.Time         `json:"checked_at"`
}

// MinuteSupplement is the exact KRW-notional remainder of an official
// closed-minute candle after subtracting trades already observed over the
// WebSocket. It is an analysis overlay: it must never be written back into
// the second aggregate store because a minute candle cannot recover trade
// timing, count, or aggressor side.
type MinuteSupplement struct {
	Exchange                 domain.Exchange   `json:"exchange"`
	Market                   string            `json:"market"`
	Base                     string            `json:"base"`
	Quote                    string            `json:"quote"`
	MinuteStart              domain.UnixSecond `json:"minute_start"`
	IntervalSec              int64             `json:"interval_sec"`
	KnownGapSeconds          int64             `json:"known_gap_seconds"`
	WSVolumeKRW              float64           `json:"ws_volume_krw"`
	OfficialVolumeKRW        float64           `json:"official_volume_krw"`
	SupplementalVolumeKRW    float64           `json:"supplemental_volume_krw"`
	Source                   string            `json:"source"`
	NotionalMethod           string            `json:"notional_method,omitempty"`
	ReplaceWholeMinute       bool              `json:"replace_whole_minute,omitempty"`
	ReplacementAdjustmentKRW float64           `json:"replacement_adjustment_krw,omitempty"`
}

type ExchangeSummary struct {
	Exchange               domain.Exchange   `json:"exchange"`
	MinuteStart            domain.UnixSecond `json:"minute_start"`
	Status                 string            `json:"status"`
	MarketCount            int64             `json:"market_count"`
	WSMarketCount          int64             `json:"ws_market_count"`
	OfficialMarketCount    int64             `json:"official_market_count"`
	VerifiedCount          int64             `json:"verified_count"`
	RecoveredCount         int64             `json:"recovered_count"`
	MismatchCount          int64             `json:"mismatch_count"`
	WSMissingCount         int64             `json:"ws_missing_count"`
	BaseOnlyCount          int64             `json:"base_only_count"`
	CandleUnavailableCount int64             `json:"candle_unavailable_count"`
	UnknownDetailCount     int64             `json:"unknown_detail_count"`
	WSQuoteVolume          float64           `json:"ws_quote_volume"`
	OfficialQuoteVolume    *float64          `json:"official_quote_volume"`
	EffectiveQuoteVolume   *float64          `json:"effective_quote_volume"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

type Summary struct {
	MinuteStart            *domain.UnixSecond         `json:"minute_start"`
	SealMinuteStart        *domain.UnixSecond         `json:"seal_minute_start"`
	Status                 string                     `json:"status"`
	ExchangeCount          int64                      `json:"exchange_count"`
	MarketCount            int64                      `json:"market_count"`
	VerifiedCount          int64                      `json:"verified_count"`
	RecoveredCount         int64                      `json:"recovered_count"`
	MismatchCount          int64                      `json:"mismatch_count"`
	WSMissingCount         int64                      `json:"ws_missing_count"`
	BaseOnlyCount          int64                      `json:"base_only_count"`
	CandleUnavailableCount int64                      `json:"candle_unavailable_count"`
	UnknownDetailCount     int64                      `json:"unknown_detail_count"`
	EffectiveQuoteVolume   *float64                   `json:"effective_quote_volume"`
	Exchanges              map[string]ExchangeSummary `json:"exchanges"`
	MinuteSeals            map[string]MinuteSeal      `json:"minute_seals"`
}

type ImmutableMinuteConflict struct{ Message string }

func (e *ImmutableMinuteConflict) Error() string { return e.Message }

func StatusForCounts(counts map[string]int64) string {
	for _, status := range []string{StatusMismatch, StatusWSMissing, StatusCandleUnavailable, StatusIncompleteBatch, StatusSourcePrecisionDifference, StatusRecoveredCandle, StatusCandleBaseOnly} {
		if counts[status] > 0 || status == StatusRecoveredCandle && counts[StatusReplacedCandle] > 0 {
			return status
		}
	}
	return StatusVerifiedWS
}

// StatusForCompleteness preserves the most severe observed reconciliation
// status, but refuses to call a batch verified unless every expected member
// produced a summary row. Missing evidence is not positive verification.
func StatusForCompleteness(counts map[string]int64, actual, expected int64) string {
	status := StatusForCounts(counts)
	if expected <= 0 || actual != expected {
		switch status {
		case StatusMismatch, StatusWSMissing, StatusCandleUnavailable:
			return status
		default:
			return StatusIncompleteBatch
		}
	}
	return status
}

func CandleFromOfficial(c official.MinuteCandle, base, quote string, attempt int, fetchedAt time.Time) (OfficialMinuteCandle, error) {
	row := OfficialMinuteCandle{
		Exchange: c.Exchange, Market: c.Market, Base: base, Quote: quote,
		MinuteStart: domain.UnixSecond(c.StartMS / 1000),
		Open:        floatPointer(c.Open), High: floatPointer(c.High), Low: floatPointer(c.Low), Close: floatPointer(c.Close),
		BaseVolume: c.BaseVolume, QuoteVolume: cloneFloat(c.QuoteVolumeKRW),
		QuoteVolumeExact: c.QuoteVolumeKRW != nil, SourceKind: c.Source,
		NotionalMethod: c.NotionalMethod, ObservationAttempt: attempt, FetchedAt: fetchedAt,
		DecimalEvidence: c.DecimalEvidence,
	}
	return row, row.Normalize()
}

func (c *OfficialMinuteCandle) Normalize() error {
	if c == nil {
		return errors.New("official candle is nil")
	}
	c.Market = strings.TrimSpace(c.Market)
	c.Base = strings.ToUpper(strings.TrimSpace(c.Base))
	c.Quote = strings.ToUpper(strings.TrimSpace(c.Quote))
	if !c.Exchange.Valid() || c.Market == "" || c.Base == "" || c.Quote == "" {
		return errors.New("official candle identity is invalid")
	}
	if err := validateMinute(c.MinuteStart); err != nil {
		return err
	}
	for name, value := range map[string]*float64{"open": c.Open, "high": c.High, "low": c.Low, "close": c.Close} {
		if value != nil && !positive(*value) {
			return fmt.Errorf("%s must be positive and finite", name)
		}
	}
	if !nonnegative(c.BaseVolume) {
		return errors.New("base volume must be non-negative and finite")
	}
	if c.QuoteVolume != nil && !nonnegative(*c.QuoteVolume) {
		return errors.New("quote volume must be non-negative and finite")
	}
	if strings.HasPrefix(c.NotionalMethod, "COINONE_SINGLE_TRADE_QUOTE_TRUNCATION_") {
		if _, ok := CoinonePrecisionEvidence(*c); !ok {
			return errors.New("source precision evidence does not match the stored candle")
		}
	}
	if isBaseOnly(c.Exchange) {
		c.QuoteVolume = nil
		c.QuoteVolumeExact = false
		if c.SourceKind == "" || c.SourceKind == SourceOfficialCandle {
			c.SourceKind = SourceOfficialBaseOnly
		}
		c.NotionalMethod = official.BaseVolumeOnlyNotional
	} else {
		if c.QuoteVolumeExact && c.QuoteVolume == nil {
			return errors.New("an exact quote-volume candle requires quote volume")
		}
		if c.SourceKind == "" {
			c.SourceKind = SourceOfficialCandle
		}
		if c.NotionalMethod == "" {
			c.NotionalMethod = official.ExactQuoteNotional
		}
	}
	if c.ObservationAttempt < 0 {
		return errors.New("observation attempt must be non-negative")
	}
	if c.FetchedAt.IsZero() {
		c.FetchedAt = time.Now().UTC()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	return nil
}

func MinuteStart(timestamp domain.UnixMilli) domain.UnixSecond {
	return domain.UnixSecond(int64(timestamp) / MinuteMS * MinuteSeconds)
}

func validateMinute(value domain.UnixSecond) error {
	if value < 0 || int64(value)%MinuteSeconds != 0 {
		return errors.New("minute start must be aligned to a 60-second boundary")
	}
	return nil
}

func isBaseOnly(exchange domain.Exchange) bool {
	return exchange == domain.ExchangeKorbit || exchange == domain.ExchangeGopax
}

func positive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}

func nonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func floatPointer(value float64) *float64 { return &value }

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
