package wscandle

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const UpbitEndpoint = "wss://api.upbit.com/websocket/v1"

var upbitMarketPattern = regexp.MustCompile(`^[A-Z0-9]+-[A-Z0-9]+$`)

// UpbitSubscriptions requests native candles, without REST ticker polling.
func UpbitSubscriptions(markets []string) ([]byte, error) {
	if len(markets) == 0 {
		return nil, fmt.Errorf("missing Upbit markets")
	}
	codes := make([]string, 0, len(markets))
	seen := map[string]bool{}
	for _, market := range markets {
		if !upbitMarketPattern.MatchString(market) {
			return nil, fmt.Errorf("invalid Upbit market %q", market)
		}
		if !seen[market] {
			codes = append(codes, market)
			seen[market] = true
		}
	}
	request := []any{map[string]string{"ticket": "marketgo2-candles"}}
	for _, kind := range []string{"candle.1m", "candle.10m", "candle.60m"} {
		request = append(request, struct {
			Type  string   `json:"type"`
			Codes []string `json:"codes"`
		}{kind, codes})
	}
	request = append(request, map[string]string{"format": "DEFAULT"})
	return json.Marshal(request)
}

// ParseUpbit preserves cumulative candle values and the source update timestamp.
// A native update confirms candle boundaries, but provides no finality flag.
func ParseUpbit(raw []byte) (Candle, error) {
	var wire struct {
		Type      string      `json:"type"`
		Code      string      `json:"code"`
		Start     string      `json:"candle_date_time_utc"`
		Timestamp int64       `json:"timestamp"`
		Open      json.Number `json:"opening_price"`
		High      json.Number `json:"high_price"`
		Low       json.Number `json:"low_price"`
		Close     json.Number `json:"trade_price"`
		Base      json.Number `json:"candle_acc_trade_volume"`
		Quote     json.Number `json:"candle_acc_trade_price"`
	}
	var zero Candle
	if err := json.Unmarshal(raw, &wire); err != nil {
		return zero, err
	}
	interval := map[string]time.Duration{"candle.1m": time.Minute, "candle.10m": 10 * time.Minute, "candle.60m": time.Hour}[wire.Type]
	if interval == 0 || !upbitMarketPattern.MatchString(wire.Code) {
		return zero, fmt.Errorf("invalid Upbit candle type or market")
	}
	start, err := time.Parse("2006-01-02T15:04:05", wire.Start)
	if err != nil || start.Year() < 2009 || !start.Equal(start.Truncate(interval)) {
		return zero, fmt.Errorf("invalid Upbit candle boundary")
	}
	if wire.Timestamp <= 0 || time.UnixMilli(wire.Timestamp).Before(start) {
		return zero, fmt.Errorf("invalid Upbit source timestamp")
	}
	values := make([]string, 6)
	for i, n := range []json.Number{wire.Open, wire.High, wire.Low, wire.Close, wire.Base, wire.Quote} {
		values[i], err = upbitDecimal(n.String())
		if err != nil {
			return zero, err
		}
	}
	pair := strings.Split(wire.Code, "-")
	c := Candle{Exchange: "upbit", Market: wire.Code, Asset: pair[1], Quote: pair[0], Interval: interval, Start: start, End: start.Add(interval), Open: values[0], High: values[1], Low: values[2], Close: values[3], BaseVolume: values[4], QuoteVolume: values[5], BoundaryVerified: true, Source: "websocket_upbit_candle", SourceTimestampMS: wire.Timestamp}
	if err := validateCandle(c); err != nil {
		return zero, err
	}
	return c, nil
}

// JSON numbers may use exponent notation; expand decimal text exactly, with no
// float64 roundtrip and a bounded expansion for untrusted source payloads.
func upbitDecimal(s string) (string, error) {
	if len(s) > 128 {
		return "", fmt.Errorf("oversized decimal")
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'e' || r == 'E' })
	if len(parts) == 2 {
		exponent, err := strconv.Atoi(parts[1])
		if err != nil || exponent < -100 || exponent > 100 {
			return "", fmt.Errorf("invalid decimal exponent")
		}
		mantissa := parts[0]
		if _, err := decimal(mantissa); err != nil {
			return "", err
		}
		point := strings.IndexByte(mantissa, '.')
		if point < 0 {
			point = len(mantissa)
		}
		digits := strings.ReplaceAll(mantissa, ".", "")
		point += exponent
		switch {
		case point <= 0:
			s = "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			s = digits + strings.Repeat("0", point-len(digits))
		default:
			s = digits[:point] + "." + digits[point:]
		}
	}
	if _, err := decimal(s); err != nil {
		return "", err
	}
	return s, nil
}
