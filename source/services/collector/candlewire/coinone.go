package wscandle

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const CoinoneEndpoint = "wss://stream.coinone.co.kr"
const CoinoneUnverifiedVolumeWindow = "DOCUMENTED_ROLLING_24H_UNVERIFIED"

// CoinoneChart is native CHART evidence, deliberately not Candle. The official
// CHART documentation calls both volume fields rolling 24-hour quantities; they
// must not enter interval volume sums until their semantics are verified.
type CoinoneChart struct {
	Exchange, Market, Asset, Quote      string
	Interval                            time.Duration
	Start, End                          time.Time
	Open, High, Low, Close              string
	QuoteVolume, TargetVolume           string
	SourceTimestampMS                   int64
	SourceID, QuoteVolumeWindow, Source string
}

var coinoneSymbolPattern = regexp.MustCompile(`^[A-Z0-9]+$`)
var coinoneIDPattern = regexp.MustCompile(`^[0-9]{1,32}$`)

// Validate validates source evidence without claiming finality or candle volume.
func (c CoinoneChart) Validate() error {
	if c.Exchange != "coinone" || c.Source != "websocket_coinone_chart" || c.QuoteVolumeWindow != CoinoneUnverifiedVolumeWindow || !coinoneSymbolPattern.MatchString(c.Asset) || !coinoneSymbolPattern.MatchString(c.Quote) || c.Market != c.Quote+"-"+c.Asset || !coinoneIDPattern.MatchString(c.SourceID) {
		return fmt.Errorf("invalid Coinone chart identity")
	}
	if c.Interval != time.Minute && c.Interval != time.Hour && c.Interval != 24*time.Hour {
		return fmt.Errorf("unsupported Coinone interval")
	}
	if c.Start.Year() < 2009 || !c.Start.Equal(c.Start.Truncate(c.Interval)) || !c.End.Equal(c.Start.Add(c.Interval)) || c.SourceTimestampMS <= 0 || time.UnixMilli(c.SourceTimestampMS).Before(c.Start) {
		return fmt.Errorf("invalid Coinone chart timestamps")
	}
	// Validation is only numerical. This does not reinterpret either volume field.
	return validateValues(c.Open, c.High, c.Low, c.Close, c.TargetVolume, c.QuoteVolume)
}

// CoinoneSubscriptions returns one native request per market and interval. The
// input uses quote-asset identity (for example KRW-BTC), as with Upbit.
func CoinoneSubscriptions(markets []string) ([][]byte, error) {
	if len(markets) == 0 {
		return nil, fmt.Errorf("missing Coinone markets")
	}
	var requests [][]byte
	seen := map[string]bool{}
	for _, market := range markets {
		pair := strings.Split(market, "-")
		if len(pair) != 2 || !coinoneSymbolPattern.MatchString(pair[0]) || !coinoneSymbolPattern.MatchString(pair[1]) {
			return nil, fmt.Errorf("invalid Coinone market %q", market)
		}
		if seen[market] {
			continue
		}
		seen[market] = true
		for _, interval := range []string{"1m", "1h", "1d"} {
			request := struct {
				RequestType string            `json:"request_type"`
				Channel     string            `json:"channel"`
				Topic       map[string]string `json:"topic"`
				Format      string            `json:"format"`
			}{"SUBSCRIBE", "CHART", map[string]string{"quote_currency": pair[0], "target_currency": pair[1], "interval": interval}, "DEFAULT"}
			payload, err := json.Marshal(request)
			if err != nil {
				return nil, err
			}
			requests = append(requests, payload)
		}
	}
	return requests, nil
}

// ParseCoinone accepts DEFAULT CHART data frames. Subscription acknowledgements
// and other channels are not market observations.
func ParseCoinone(raw []byte) (CoinoneChart, error) {
	var wire struct {
		ResponseType string `json:"response_type"`
		Channel      string `json:"channel"`
		Data         struct {
			Quote           string `json:"quote_currency"`
			Asset           string `json:"target_currency"`
			Interval        string `json:"interval"`
			Timestamp       int64  `json:"timestamp"`
			CandleTimestamp int64  `json:"candle_timestamp"`
			ID              string `json:"id"`
			Open            string `json:"first"`
			High            string `json:"high"`
			Low             string `json:"low"`
			Close           string `json:"last"`
			QuoteVolume     string `json:"quote_volume"`
			TargetVolume    string `json:"target_volume"`
		} `json:"data"`
	}
	var zero CoinoneChart
	if err := json.Unmarshal(raw, &wire); err != nil {
		return zero, err
	}
	if wire.ResponseType != "DATA" || wire.Channel != "CHART" {
		return zero, fmt.Errorf("not Coinone CHART data")
	}
	d := wire.Data
	interval := map[string]time.Duration{"1m": time.Minute, "1h": time.Hour, "1d": 24 * time.Hour}[d.Interval]
	start := time.UnixMilli(d.CandleTimestamp).UTC()
	c := CoinoneChart{Exchange: "coinone", Market: d.Quote + "-" + d.Asset, Asset: d.Asset, Quote: d.Quote, Interval: interval, Start: start, End: start.Add(interval), Open: d.Open, High: d.High, Low: d.Low, Close: d.Close, QuoteVolume: d.QuoteVolume, TargetVolume: d.TargetVolume, SourceTimestampMS: d.Timestamp, SourceID: d.ID, QuoteVolumeWindow: CoinoneUnverifiedVolumeWindow, Source: "websocket_coinone_chart"}
	if err := c.Validate(); err != nil {
		return zero, err
	}
	return c, nil
}
