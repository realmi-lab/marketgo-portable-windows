package official

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/domain"
)

const (
	MinuteMS               = int64(60_000)
	RecentCandleCount      = 3
	ExactQuoteNotional     = "EXACT_CANDLE_QUOTE_VOLUME"
	BaseVolumeOnlyNotional = "BASE_VOLUME_ONLY"
)

var CandleURLs = map[domain.Exchange]string{
	domain.ExchangeUpbit:   "https://api.upbit.com/v1/candles/minutes/1",
	domain.ExchangeBithumb: "https://api.bithumb.com/v1/candles/minutes/1",
	domain.ExchangeCoinone: "https://api.coinone.co.kr/public/v2/chart/{quote}/{base}",
	domain.ExchangeKorbit:  "https://api.digitalx.miraeasset.com/v2/candles",
	domain.ExchangeGopax:   "https://api.gopax.co.kr/trading-pairs/{market}/candles",
}

var candleSources = map[domain.Exchange]string{
	domain.ExchangeUpbit:   "UPBIT_OFFICIAL_MINUTE_CANDLE_REST",
	domain.ExchangeBithumb: "BITHUMB_OFFICIAL_MINUTE_CANDLE_REST",
	domain.ExchangeCoinone: "COINONE_OFFICIAL_MINUTE_CANDLE_REST",
	domain.ExchangeKorbit:  "KORBIT_OFFICIAL_MINUTE_CANDLE_REST",
	domain.ExchangeGopax:   "GOPAX_OFFICIAL_MINUTE_CANDLE_REST",
}

var ErrQuoteVolumeUnavailable = errors.New("minute candle exposes base volume only; close x base volume is forbidden")

type CandleRequest struct {
	Exchange      domain.Exchange `json:"exchange"`
	Market        string          `json:"market"`
	URL           string          `json:"url"`
	Params        map[string]any  `json:"params"`
	TargetStartMS int64           `json:"target_start_ms"`
}

type MinuteCandle struct {
	Exchange        domain.Exchange         `json:"exchange"`
	Market          string                  `json:"market"`
	StartMS         int64                   `json:"start_ms"`
	Open            float64                 `json:"open"`
	High            float64                 `json:"high"`
	Low             float64                 `json:"low"`
	Close           float64                 `json:"close"`
	BaseVolume      float64                 `json:"base_volume"`
	QuoteVolumeKRW  *float64                `json:"quote_volume_krw"`
	NotionalMethod  string                  `json:"notional_method"`
	Source          string                  `json:"source"`
	DecimalEvidence *CoinoneDecimalEvidence `json:"decimal_evidence,omitempty"`
}

func (c MinuteCandle) Validate() error {
	if c.StartMS <= 0 || c.StartMS%MinuteMS != 0 {
		return errors.New("candle start must be a positive UTC minute boundary")
	}
	for name, value := range map[string]float64{
		"open": c.Open, "high": c.High, "low": c.Low, "close": c.Close,
	} {
		if !positiveNumber(value) {
			return fmt.Errorf("candle %s must be positive and finite", name)
		}
	}
	if c.Low > c.High || c.Open < c.Low || c.Open > c.High || c.Close < c.Low || c.Close > c.High {
		return errors.New("candle OHLC values are inconsistent")
	}
	if !finiteNonnegative(c.BaseVolume) {
		return errors.New("base volume must be non-negative and finite")
	}
	switch c.NotionalMethod {
	case ExactQuoteNotional:
		if c.QuoteVolumeKRW == nil || !finiteNonnegative(*c.QuoteVolumeKRW) {
			return errors.New("exact quote-volume candle requires non-negative KRW volume")
		}
	case BaseVolumeOnlyNotional:
		if c.QuoteVolumeKRW != nil {
			return errors.New("base-only candle must not carry inferred KRW volume")
		}
	default:
		return fmt.Errorf("unsupported candle notional method %q", c.NotionalMethod)
	}
	return nil
}

func (c MinuteCandle) QuoteVolume() (float64, error) {
	if c.QuoteVolumeKRW == nil {
		return 0, fmt.Errorf("%s: %w", c.Exchange, ErrQuoteVolumeUnavailable)
	}
	return *c.QuoteVolumeKRW, nil
}

func ClosedMinuteStart(asOfMS int64) (int64, error) {
	if asOfMS <= 0 {
		return 0, errors.New("as-of timestamp must be positive")
	}
	target := (asOfMS/MinuteMS - 1) * MinuteMS
	if target <= 0 {
		return 0, errors.New("as-of timestamp is too early to have a closed minute")
	}
	return target, nil
}

func BuildCandleRequest(exchangeName domain.Exchange, market string, asOfMS int64) (CandleRequest, error) {
	endpoint, ok := CandleURLs[exchangeName]
	if !ok {
		return CandleRequest{}, fmt.Errorf("unsupported official candle exchange: %s", exchangeName)
	}
	base, quote, err := krwMarketParts(market)
	if err != nil {
		return CandleRequest{}, err
	}
	target, err := ClosedMinuteStart(asOfMS)
	if err != nil {
		return CandleRequest{}, err
	}
	params := make(map[string]any)
	switch exchangeName {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		params["market"] = quote + "-" + base
		exclusiveEnd := time.UnixMilli(target + MinuteMS)
		if exchangeName == domain.ExchangeUpbit {
			params["to"] = exclusiveEnd.UTC().Format("2006-01-02T15:04:05Z")
		} else {
			kst := time.FixedZone("KST", 9*60*60)
			params["to"] = exclusiveEnd.In(kst).Format("2006-01-02T15:04:05")
		}
		params["count"] = RecentCandleCount
	case domain.ExchangeCoinone:
		endpoint = strings.ReplaceAll(endpoint, "{quote}", quote)
		endpoint = strings.ReplaceAll(endpoint, "{base}", base)
		params["interval"] = "1m"
		params["timestamp"] = target
		params["size"] = RecentCandleCount
	case domain.ExchangeKorbit:
		params["symbol"] = strings.ToLower(base + "_" + quote)
		params["interval"] = 1
		params["start"] = target - (RecentCandleCount-1)*MinuteMS
		params["end"] = target + MinuteMS
		params["limit"] = RecentCandleCount
	case domain.ExchangeGopax:
		nativeMarket := base + "-" + quote
		endpoint = strings.ReplaceAll(endpoint, "{market}", nativeMarket)
		params["start"] = target - (RecentCandleCount-1)*MinuteMS
		params["end"] = target + MinuteMS - 1
		params["interval"] = 1
		params["limit"] = RecentCandleCount
	}
	return CandleRequest{
		Exchange: exchangeName, Market: market, URL: endpoint,
		Params: params, TargetStartMS: target,
	}, nil
}

func ParseCandles(exchangeName domain.Exchange, market string, payload []byte) ([]MinuteCandle, error) {
	if _, _, err := krwMarketParts(market); err != nil {
		return nil, err
	}
	root, err := decodeAny(payload)
	if err != nil {
		return nil, err
	}
	var candles []MinuteCandle
	switch exchangeName {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		candles, err = parseUpbitStyleCandles(exchangeName, market, root, 1)
	case domain.ExchangeCoinone:
		candles, err = parseCoinoneCandles(market, root)
	case domain.ExchangeKorbit:
		candles, err = parseKorbitCandles(market, root)
	case domain.ExchangeGopax:
		candles, err = parseGopaxCandles(market, root)
	default:
		err = fmt.Errorf("unsupported official candle exchange: %s", exchangeName)
	}
	if err != nil {
		return nil, err
	}
	return deduplicateCandles(candles)
}

func ParseClosedCandle(exchangeName domain.Exchange, market string, payload []byte, asOfMS int64) (*MinuteCandle, error) {
	candles, err := ParseCandles(exchangeName, market, payload)
	if err != nil {
		return nil, err
	}
	target, err := ClosedMinuteStart(asOfMS)
	if err != nil {
		return nil, err
	}
	return SelectCandle(candles, target)
}

func SelectCandle(candles []MinuteCandle, targetStartMS int64) (*MinuteCandle, error) {
	if targetStartMS <= 0 || targetStartMS%MinuteMS != 0 {
		return nil, errors.New("target start is not aligned to a UTC minute")
	}
	recent := append([]MinuteCandle(nil), candles...)
	sort.Slice(recent, func(i, j int) bool { return recent[i].StartMS > recent[j].StartMS })
	if len(recent) > RecentCandleCount {
		recent = recent[:RecentCandleCount]
	}
	for _, candle := range recent {
		if candle.StartMS == targetStartMS {
			result := candle
			return &result, nil
		}
	}
	return nil, nil
}

func parseUpbitStyleCandles(exchangeName domain.Exchange, market string, root any, expectedUnit int) ([]MinuteCandle, error) {
	base, _, _ := krwMarketParts(market)
	expected := "KRW-" + base
	rows, err := mappingRows(root, string(exchangeName))
	if err != nil {
		return nil, err
	}
	result := make([]MinuteCandle, 0, len(rows))
	for _, row := range rows {
		if returned := strings.ToUpper(text(row["market"])); returned != "" && returned != expected {
			return nil, fmt.Errorf("%s returned %s for requested %s", exchangeName, returned, expected)
		}
		if unit := text(row["unit"]); unit != "" && unit != strconv.Itoa(expectedUnit) {
			return nil, fmt.Errorf("%s returned candle unit %s, expected %d", exchangeName, unit, expectedUnit)
		}
		start, err := utcMinute(text(row["candle_date_time_utc"]))
		if err != nil {
			return nil, err
		}
		candle, err := candleFromValues(exchangeName, market, start,
			row["opening_price"], row["high_price"], row["low_price"], row["trade_price"],
			row["candle_acc_trade_volume"], row["candle_acc_trade_price"])
		if err != nil {
			return nil, err
		}
		result = append(result, candle)
	}
	return result, nil
}

func parseCoinoneCandles(market string, root any) ([]MinuteCandle, error) {
	object, ok := root.(map[string]any)
	if !ok || !strings.EqualFold(text(object["result"]), "success") {
		return nil, errors.New("Coinone candle response is not successful")
	}
	rows, err := mappingRows(object["chart"], "coinone")
	if err != nil {
		return nil, err
	}
	result := make([]MinuteCandle, 0, len(rows))
	for _, row := range rows {
		start, err := minuteTimestamp(row["timestamp"])
		if err != nil {
			return nil, err
		}
		candle, err := candleFromValues(domain.ExchangeCoinone, market, start,
			row["open"], row["high"], row["low"], row["close"], row["target_volume"], row["quote_volume"])
		if err != nil {
			return nil, err
		}
		candle.DecimalEvidence = coinoneDecimalEvidence(market, start, row)
		result = append(result, candle)
	}
	return result, nil
}

func parseKorbitCandles(market string, root any) ([]MinuteCandle, error) {
	object, ok := root.(map[string]any)
	if !ok || object["success"] != true {
		return nil, errors.New("Korbit candle response is not successful")
	}
	rows, err := mappingRows(object["data"], "korbit")
	if err != nil {
		return nil, err
	}
	result := make([]MinuteCandle, 0, len(rows))
	for _, row := range rows {
		start, err := minuteTimestamp(row["timestamp"])
		if err != nil {
			return nil, err
		}
		candle, err := candleFromValues(domain.ExchangeKorbit, market, start,
			row["open"], row["high"], row["low"], row["close"], row["volume"], nil)
		if err != nil {
			return nil, err
		}
		result = append(result, candle)
	}
	return result, nil
}

func parseGopaxCandles(market string, root any) ([]MinuteCandle, error) {
	rows, ok := root.([]any)
	if !ok {
		return nil, errors.New("GOPAX candle response is not a list")
	}
	result := make([]MinuteCandle, 0, len(rows))
	for _, raw := range rows {
		row, ok := raw.([]any)
		if !ok || len(row) != 6 {
			return nil, errors.New("GOPAX candle response has an invalid row")
		}
		start, err := minuteTimestamp(row[0])
		if err != nil {
			return nil, err
		}
		candle, err := candleFromValues(domain.ExchangeGopax, market, start,
			row[3], row[2], row[1], row[4], row[5], nil)
		if err != nil {
			return nil, err
		}
		result = append(result, candle)
	}
	return result, nil
}

func candleFromValues(
	exchangeName domain.Exchange,
	market string,
	start int64,
	open, high, low, close, baseVolume, quoteVolume any,
) (MinuteCandle, error) {
	values := make([]float64, 5)
	var err error
	for index, raw := range []any{open, high, low, close} {
		values[index], err = positiveFloat(raw)
		if err != nil {
			return MinuteCandle{}, err
		}
	}
	values[4], err = nonnegative(baseVolume, "base_volume")
	if err != nil {
		return MinuteCandle{}, err
	}
	candle := MinuteCandle{
		Exchange: exchangeName, Market: market, StartMS: start,
		Open: values[0], High: values[1], Low: values[2], Close: values[3],
		BaseVolume: values[4], Source: candleSources[exchangeName],
	}
	if exchangeName == domain.ExchangeUpbit || exchangeName == domain.ExchangeBithumb || exchangeName == domain.ExchangeCoinone {
		value, err := nonnegative(quoteVolume, "quote_volume_krw")
		if err != nil {
			return MinuteCandle{}, err
		}
		candle.QuoteVolumeKRW = &value
		candle.NotionalMethod = ExactQuoteNotional
	} else {
		candle.NotionalMethod = BaseVolumeOnlyNotional
	}
	return candle, candle.Validate()
}

func deduplicateCandles(candles []MinuteCandle) ([]MinuteCandle, error) {
	byStart := make(map[int64]MinuteCandle, len(candles))
	for _, candle := range candles {
		if previous, found := byStart[candle.StartMS]; found && !reflect.DeepEqual(previous, candle) {
			return nil, errors.New("official candle response has conflicting minute rows")
		}
		byStart[candle.StartMS] = candle
	}
	starts := make([]int64, 0, len(byStart))
	for start := range byStart {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	result := make([]MinuteCandle, 0, len(starts))
	for _, start := range starts {
		result = append(result, byStart[start])
	}
	return result, nil
}

func mappingRows(root any, exchangeName string) ([]map[string]any, error) {
	values, ok := root.([]any)
	if !ok {
		return nil, fmt.Errorf("%s candle response is not a list", exchangeName)
	}
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s candle response has an invalid row", exchangeName)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func krwMarketParts(market string) (base, quote string, err error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(market), "_", "-"))
	parts := strings.Split(normalized, "-")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid KRW market: %s", market)
	}
	if parts[0] == "KRW" && parts[1] != "KRW" {
		return parts[1], "KRW", nil
	}
	if parts[1] == "KRW" && parts[0] != "KRW" {
		return parts[0], "KRW", nil
	}
	return "", "", fmt.Errorf("official minute reconciliation supports KRW markets only: %s", market)
}

func minuteTimestamp(value any) (int64, error) {
	parsed, err := strconv.ParseInt(text(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("candle timestamp must be a positive integer")
	}
	if parsed%MinuteMS != 0 {
		return 0, errors.New("candle timestamp is not aligned to a UTC minute")
	}
	return parsed, nil
}

func utcMinute(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, errors.New("candle_date_time_utc is not an ISO UTC timestamp")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		parsed, err = time.ParseInLocation("2006-01-02T15:04:05", value, time.UTC)
	}
	if err != nil {
		return 0, errors.New("candle_date_time_utc is not an ISO UTC timestamp")
	}
	return minuteTimestamp(parsed.UTC().UnixMilli())
}

func positiveFloat(value any) (float64, error) {
	parsed, err := strconv.ParseFloat(text(value), 64)
	if err != nil || !positiveNumber(parsed) {
		return 0, errors.New("OHLC value must be positive and finite")
	}
	return parsed, nil
}

func positiveNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
