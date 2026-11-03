package official

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	contract "market-share-go/contracts/collection/official"
	"math"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/domain"
)

var TickerURLs = map[domain.Exchange]string{
	domain.ExchangeUpbit:   "https://api.upbit.com/v1/ticker/all?quote_currencies=KRW",
	domain.ExchangeBithumb: "https://api.bithumb.com/public/ticker/ALL_KRW",
	domain.ExchangeCoinone: "https://api.coinone.co.kr/public/v2/ticker_new/KRW",
	domain.ExchangeKorbit:  "https://api.digitalx.miraeasset.com/v2/tickers",
	domain.ExchangeGopax:   "https://api.gopax.co.kr/tickers",
}

// MaxTickerCollectionSpan bounds the time skew between the first request and
// the completed five-exchange snapshot. The five requests run in parallel, but
// slower domestic public APIs regularly need more than three seconds. Ten
// seconds is still a narrow common-time window for a rolling 24-hour ticker and
// avoids turning a healthy slow response into a permanently partial snapshot.
const MaxTickerCollectionSpan = 10 * time.Second

// MaxTickerExchangeTimestampAge quarantines an otherwise valid response when
// the freshest exchange timestamp shows that the payload was cached for too
// long. Upbit, Korbit, and GOPAX expose per-market trade timestamps, so those
// adapters use the freshest KRW market; quiet markets with an old or zero
// timestamp therefore do not invalidate a fresh all-market response. When a
// registry provider is configured, the service first requires an exact market
// set and recomputes this freshest timestamp from verified markets only.
const MaxTickerExchangeTimestampAge = 2 * time.Minute

// MaxTickerExchangeTimestampFutureSkew permits a small amount of clock skew
// without accepting a payload timestamp that is materially in the future.
const MaxTickerExchangeTimestampFutureSkew = 30 * time.Second

// MaxTickerSnapshotAge prevents a successfully collected snapshot from
// remaining LIVE indefinitely when refreshes stop. It deliberately matches
// the exchange timestamp tolerance: both the payload and our collection must
// still be recent enough to represent current official state.
const MaxTickerSnapshotAge = MaxTickerExchangeTimestampAge

type TickerSnapshot = contract.TickerSnapshot

type DomesticTickerSummary = contract.DomesticTickerSummary

func ParseTicker(exchangeName domain.Exchange, payload []byte, receivedAt time.Time) (TickerSnapshot, error) {
	root, err := decodeAny(payload)
	if err != nil {
		return TickerSnapshot{}, err
	}
	switch exchangeName {
	case domain.ExchangeUpbit:
		rows := filterRows(root, func(row map[string]any) bool {
			return strings.HasPrefix(strings.ToUpper(text(row["market"])), "KRW-")
		})
		return tickerRows(exchangeName, "UPBIT_KRW_ALL_TICKER", rows, "acc_trade_price_24h", "market", "timestamp", receivedAt, nil)
	case domain.ExchangeBithumb:
		return parseBithumbTicker(root, receivedAt)
	case domain.ExchangeCoinone:
		object, ok := root.(map[string]any)
		if !ok || !strings.EqualFold(text(object["result"]), "success") {
			return TickerSnapshot{}, errors.New("Coinone KRW ticker response is not successful")
		}
		rows := filterRows(object["tickers"], func(row map[string]any) bool {
			return strings.EqualFold(text(row["quote_currency"]), "KRW")
		})
		return tickerRows(exchangeName, "COINONE_KRW_ALL_TICKER", rows, "quote_volume", "target_currency", "", receivedAt, timestamp(object["server_time"]))
	case domain.ExchangeKorbit:
		object, ok := root.(map[string]any)
		if !ok || object["success"] != true {
			return TickerSnapshot{}, errors.New("Korbit ticker response is not successful")
		}
		rows := filterRows(object["data"], func(row map[string]any) bool {
			return strings.HasSuffix(strings.ToLower(text(row["symbol"])), "_krw")
		})
		return tickerRows(exchangeName, "KORBIT_KRW_ALL_TICKER", rows, "quoteVolume", "symbol", "lastTradedAt", receivedAt, nil)
	case domain.ExchangeGopax:
		rows := filterRows(root, func(row map[string]any) bool {
			return strings.HasSuffix(strings.ToUpper(text(row["tradingPairName"])), "-KRW")
		})
		return tickerRows(exchangeName, "GOPAX_KRW_ALL_TICKERS", rows, "quoteVolume", "tradingPairName", "lastTraded", receivedAt, nil)
	default:
		return TickerSnapshot{}, fmt.Errorf("unsupported domestic official ticker exchange: %s", exchangeName)
	}
}

func tickerRows(
	exchangeName domain.Exchange,
	source string,
	rows []map[string]any,
	volumeField string,
	assetField string,
	timestampField string,
	receivedAt time.Time,
	responseTimestamp *int64,
) (TickerSnapshot, error) {
	if len(rows) == 0 {
		return TickerSnapshot{}, fmt.Errorf("%s ticker has no KRW market rows", exchangeName)
	}
	total := 0.0
	assets := make(map[string]float64, len(rows))
	markets := make(map[string]struct{}, len(rows))
	marketTimestamps := make(map[string]int64, len(rows))
	var latestTimestampMS int64
	for _, row := range rows {
		asset := tickerAsset(exchangeName, text(row[assetField]))
		marketID := CanonicalKRWMarketID(asset)
		if marketID == "" {
			return TickerSnapshot{}, fmt.Errorf("%s ticker has an empty normalized market identity", exchangeName)
		}
		if _, duplicate := markets[marketID]; duplicate {
			return TickerSnapshot{}, fmt.Errorf("%s ticker has duplicate normalized market identity %s", exchangeName, marketID)
		}
		markets[marketID] = struct{}{}

		value, err := nonnegative(row[volumeField], volumeField)
		if err != nil {
			return TickerSnapshot{}, err
		}
		total += value
		assets[asset] = value
		if math.IsInf(total, 0) {
			return TickerSnapshot{}, fmt.Errorf("%s ticker total is not finite", exchangeName)
		}

		rowTimestamp := responseTimestamp
		if timestampField != "" {
			rowTimestamp = timestamp(row[timestampField])
		}
		if rowTimestamp != nil {
			marketTimestamps[marketID] = *rowTimestamp
			latestTimestampMS = max(latestTimestampMS, *rowTimestamp)
		}
	}
	var exchangeTimestamp *int64
	if latestTimestampMS > 0 {
		exchangeTimestamp = &latestTimestampMS
	}
	if err := validateTickerTimestamp(exchangeName, receivedAt, exchangeTimestamp); err != nil {
		return TickerSnapshot{}, err
	}
	return TickerSnapshot{
		Exchange: exchangeName, Status: "LIVE", Source: source,
		VolumeKRW: &total, MarketCount: len(rows),
		AsOf:                float64(receivedAt.UnixNano()) / 1e9,
		ExchangeTimestampMS: exchangeTimestamp,
		Assets:              assets,
		Markets:             markets,
		MarketTimestampsMS:  marketTimestamps,
		ReceivedAt:          receivedAt,
	}, nil
}

func parseBithumbTicker(root any, receivedAt time.Time) (TickerSnapshot, error) {
	object, ok := root.(map[string]any)
	if !ok || text(object["status"]) != "0000" {
		return TickerSnapshot{}, errors.New("Bithumb ALL_KRW ticker response is not successful")
	}
	data, ok := object["data"].(map[string]any)
	if !ok {
		return TickerSnapshot{}, errors.New("Bithumb ALL_KRW ticker response has no market rows")
	}
	rows := make([]map[string]any, 0, len(data))
	for symbol, raw := range data {
		if strings.EqualFold(symbol, "date") {
			continue
		}
		row, ok := raw.(map[string]any)
		if !ok {
			return TickerSnapshot{}, errors.New("Bithumb ALL_KRW ticker has an invalid market row")
		}
		row["__asset"] = symbol
		rows = append(rows, row)
	}
	return tickerRows(
		domain.ExchangeBithumb, "BITHUMB_ALL_KRW_TICKER", rows,
		"acc_trade_value_24H", "__asset", "", receivedAt, timestamp(data["date"]),
	)
}

func tickerAsset(exchangeName domain.Exchange, market string) string {
	normalized := strings.ToUpper(strings.TrimSpace(market))
	switch exchangeName {
	case domain.ExchangeUpbit:
		if !strings.HasPrefix(normalized, "KRW-") {
			return ""
		}
		return strings.TrimPrefix(normalized, "KRW-")
	case domain.ExchangeBithumb, domain.ExchangeCoinone:
		return normalized
	case domain.ExchangeKorbit:
		if !strings.HasSuffix(normalized, "_KRW") {
			return ""
		}
		return strings.TrimSuffix(normalized, "_KRW")
	case domain.ExchangeGopax:
		if !strings.HasSuffix(normalized, "-KRW") {
			return ""
		}
		return strings.TrimSuffix(normalized, "-KRW")
	default:
		return ""
	}
}

// CanonicalKRWMarketID maps exchange-specific KRW symbols to the single
// internal identity used by both ticker and registry validation.
func CanonicalKRWMarketID(base string) string {
	base = strings.ToUpper(strings.TrimSpace(base))
	if base == "" {
		return ""
	}
	return "KRW-" + base
}

func AggregateTickers(exchanges map[domain.Exchange]TickerSnapshot, startedAt, completedAt time.Time) DomesticTickerSummary {
	ordered := domain.DomesticExchanges[:]
	rows := make(map[domain.Exchange]TickerSnapshot, len(ordered))
	ready := 0
	total := 0.0
	totalValid := true
	live := true
	for _, name := range ordered {
		row := exchanges[name]
		// SharePercent belongs to this aggregate, not to the independently
		// collected exchange ticker. Clear any value carried by a prior summary
		// before deriving the current denominator-bound value below.
		row.SharePercent = nil
		rows[name] = row
		if row.VolumeKRW != nil && finiteNonnegative(*row.VolumeKRW) {
			ready++
			if totalValid {
				nextTotal := total + *row.VolumeKRW
				if finiteNonnegative(nextTotal) {
					total = nextTotal
				} else {
					totalValid = false
					live = false
				}
			}
		} else {
			live = false
		}
		if row.Status != "LIVE" {
			live = false
		}
	}
	complete := ready == len(ordered) && totalValid
	span := completedAt.Sub(startedAt)
	// Some platform clocks have a coarser resolution than a fast in-memory
	// refresh, so equal non-zero instants are valid. Missing instants and a
	// completion before the start are not.
	validTimeline := !startedAt.IsZero() && !completedAt.IsZero() && span >= 0
	collectionSpan := ceilPositiveMilliseconds(span)
	status := "PARTIAL"
	if complete {
		status = "STALE"
		if live && validTimeline && span <= MaxTickerCollectionSpan {
			status = "LIVE"
		}
	}
	result := DomesticTickerSummary{
		Status: status, Source: "OFFICIAL_DOMESTIC_24H_TICKERS",
		AsOf:                  float64(completedAt.UnixNano()) / 1e9,
		StartedAt:             float64(startedAt.UnixNano()) / 1e9,
		CollectionSpanMS:      collectionSpan,
		ExpectedExchangeCount: len(ordered), ReadyExchangeCount: ready, Exchanges: rows,
	}
	if complete {
		result.TotalVolumeKRW = floatPointer(total)
		if total > 0 {
			for _, name := range ordered {
				row := rows[name]
				row.SharePercent = floatPointer(*row.VolumeKRW / total * 100)
				rows[name] = row
			}
		}
	}
	if bithumb := rows[domain.ExchangeBithumb].VolumeKRW; bithumb != nil && finiteNonnegative(*bithumb) {
		result.BithumbVolumeKRW = floatPointer(*bithumb)
		if complete && total > 0 {
			result.BithumbSharePercent = floatPointer(*bithumb / total * 100)
		}
	}
	return result
}

func decodeAny(payload []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return nil, err
	}
	return value, nil
}

func filterRows(value any, include func(map[string]any) bool) []map[string]any {
	values, _ := value.([]any)
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		row, ok := value.(map[string]any)
		if ok && include(row) {
			rows = append(rows, row)
		}
	}
	return rows
}

func nonnegative(value any, field string) (float64, error) {
	number, err := strconv.ParseFloat(text(value), 64)
	if err != nil {
		return 0, fmt.Errorf("%s is not numeric", field)
	}
	if !finiteNonnegative(number) {
		return 0, fmt.Errorf("%s must be a non-negative finite number", field)
	}
	return number, nil
}

func text(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func timestamp(value any) *int64 {
	parsed, err := strconv.ParseInt(text(value), 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func validateTickerTimestamp(exchangeName domain.Exchange, receivedAt time.Time, exchangeTimestamp *int64) error {
	if receivedAt.IsZero() {
		return fmt.Errorf("%s ticker receipt time is missing", exchangeName)
	}
	if exchangeTimestamp == nil {
		return fmt.Errorf("%s ticker exchange timestamp is missing or invalid", exchangeName)
	}
	age := receivedAt.Sub(time.UnixMilli(*exchangeTimestamp))
	if age > MaxTickerExchangeTimestampAge {
		return fmt.Errorf("%s ticker exchange timestamp is stale by %s", exchangeName, age)
	}
	if age < -MaxTickerExchangeTimestampFutureSkew {
		return fmt.Errorf("%s ticker exchange timestamp is in the future by %s", exchangeName, -age)
	}
	return nil
}

func ceilPositiveMilliseconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	milliseconds := value / time.Millisecond
	if value%time.Millisecond != 0 {
		milliseconds++
	}
	return int64(milliseconds)
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func floatPointer(value float64) *float64 { return &value }
