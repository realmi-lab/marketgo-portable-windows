// Package indexday builds calendar-day views from non-overlapping hourly candles.
// It never consumes rolling ticker snapshots or estimates turnover from base volume.
package indexday

import (
	"fmt"
	"math"
	"sort"
	"time"
)

var Exchanges = []string{"bithumb", "upbit", "coinone", "korbit", "gopax"}
var Seoul = time.FixedZone("Asia/Seoul", 9*3600)

type Market struct {
	Exchange string `json:"exchange"`
	Market   string `json:"market"`
	Base     string `json:"base"`
	Quote    string `json:"quote"`
}
type Hour struct {
	Market
	Start         int64
	BaseVolume    *float64
	QuoteVolume   *float64
	VolumeKRW     *float64
	Source        string
	VerifiedEmpty bool
}
type Row struct {
	Market
	Hours               int      `json:"hours"`
	IgnoredEmptyHours   int      `json:"ignored_empty_hours"`
	AmountHours         int      `json:"amount_hours"`
	BaseVolume          *float64 `json:"base_volume"`
	QuoteVolume         *float64 `json:"quote_volume"`
	VolumeKRW           *float64 `json:"volume_krw"`
	ObservedVolumeKRW   *float64 `json:"observed_volume_krw"`
	ObservedAmountHours int      `json:"observed_amount_hours"`
	Status              string   `json:"status"`
	Sources             []string `json:"sources"`
}
type Exchange struct {
	Exchange           string   `json:"exchange"`
	VolumeKRW          *float64 `json:"volume_krw"`
	ConfirmedVolumeKRW *float64 `json:"confirmed_volume_krw"`
	SharePct           *float64 `json:"share_pct"`
	ObservedVolumeKRW  *float64 `json:"observed_volume_krw"`
	ObservedSharePct   *float64 `json:"observed_share_pct"`
	ObservedMarkets    int      `json:"observed_markets"`
	CompleteMarkets    int      `json:"complete_markets"`
	MarketCount        int      `json:"market_count"`
	Status             string   `json:"status"`
}
type Response struct {
	StorageMode     string       `json:"storage_mode,omitempty"`
	FailedMarkets   int          `json:"failed_markets,omitempty"`
	Domestic        DomesticGrid `json:"domestic"`
	ContractVersion string       `json:"contract_version"`
	Day             string       `json:"day"`
	Start           int64        `json:"start"`
	End             int64        `json:"end"`
	Status          string       `json:"status"`
	Membership      string       `json:"membership"`
	USDTCloseKRW    *float64     `json:"usdt_close_krw"`
	Rows            []Row        `json:"rows"`
	Exchanges       []Exchange   `json:"exchanges"`
}

func Bounds(day string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation(time.DateOnly, day, Seoul)
	if err != nil || start.Format(time.DateOnly) != day {
		return time.Time{}, time.Time{}, fmt.Errorf("day must use YYYY-MM-DD")
	}
	return start, start.AddDate(0, 0, 1), nil
}

// Window keeps past calendar days fixed. Today uses the last 24 closed hours.
func Window(day string, now time.Time) (time.Time, time.Time, error) {
	start, end, err := Bounds(day)
	if err != nil {
		return start, end, err
	}
	today, _, _ := Bounds(now.In(Seoul).Format(time.DateOnly))
	if start.After(today) {
		return start, end, fmt.Errorf("future date is unavailable")
	}
	if start.Equal(today) {
		end = now.UTC().Truncate(time.Hour)
		start = end.Add(-24 * time.Hour)
	}
	return start, end, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func number(v float64) *float64 {
	if !finite(v) {
		return nil
	}
	return &v
}
func key(m Market) string { return m.Exchange + "\x00" + m.Market + "\x00" + m.Base + "\x00" + m.Quote }
func domestic(e string) bool {
	for _, v := range Exchanges {
		if v == e {
			return true
		}
	}
	return false
}

// The legacy closing-rate argument is ignored. Non-KRW amounts must already
// carry the archived same-exchange, same-hour opening conversion.
func Build(day string, markets []Market, hours []Hour, _ map[string]float64) (Response, error) {
	return BuildAt(day, markets, hours, time.Now())
}

func BuildAt(day string, markets []Market, hours []Hour, now time.Time) (Response, error) {
	start, end, err := Window(day, now)
	if err != nil {
		return Response{}, err
	}
	out := Response{ContractVersion: "INDEX_DAY_HOURLY_V1", Day: day, Start: start.Unix(), End: end.Unix(), Status: "PARTIAL", Membership: "CURRENT_REGISTRY_AND_OBSERVED_HISTORY", Rows: []Row{}, Exchanges: []Exchange{}}
	groups := groupMarketHours(markets, hours, out.Start, out.End)
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	// Reuse grouping keys instead of allocating composite strings per comparison.
	sort.Strings(keys)
	out.Rows = make([]Row, 0, len(keys))
	for _, k := range keys {
		out.Rows = append(out.Rows, buildMarketRow(groups[k]))
	}
	out.Exchanges, out.Status = buildExchanges(out.Rows)
	out.Domestic = BuildDomestic(out.Rows)
	return out, nil
}
