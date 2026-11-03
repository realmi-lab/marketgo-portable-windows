package analysis

import (
	"sort"
	"strings"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

// How many coins a coin-share list holds by default and at most.
const (
	DefaultCoinShareCount = 50
	MaxCoinShareCount     = 100
)

// The list a coin-share response orders its coins by: the official 24-hour
// tickers, or the realtime 24 hours while they are incomplete.
const (
	CoinShareRankingOfficial24h = "OFFICIAL_24H_TICKER"
	CoinShareRankingRealtime24h = "REALTIME_24H"
)

// A coin share's status says why its share is missing.
const (
	CoinShareAvailable    = "AVAILABLE"
	CoinShareNoTrades     = "NO_TRADES"     // nothing traded on a domestic exchange
	CoinShareNotObserved  = "NOT_OBSERVED"  // the window was partly observed and saw no trades
	CoinShareUnresolvedFX = "UNRESOLVED_FX" // some trades have no KRW value yet
	CoinShareUnavailable  = "UNAVAILABLE"   // missing official data or invalid/conflicting input
)

// CoinShares is Bithumb's share of each coin's domestic trading, from the
// official 24-hour tickers and from one realtime window.
type CoinShares struct {
	MarketScope string `json:"market_scope"`
	WindowSec   int64  `json:"window_sec"`
	Window      string `json:"window"`
	// AsOfSecond is the last closed second the realtime window includes.
	AsOfSecond     int64 `json:"as_of_second"`
	WindowComplete bool  `json:"window_complete"`
	CoverageSec    int64 `json:"coverage_sec"`
	// OfficialMinuteSupplementUsed says official minute candles filled some of
	// the window's recorded gaps; like the analysis screens, the dashboard
	// then does not call its shares an observed-range basis.
	OfficialMinuteSupplementUsed bool `json:"official_minute_supplement_used"`
	// Official24hStatus is the tickers' own status (LIVE, STALE or PARTIAL),
	// or UNAVAILABLE before any exchange's ticker is in. Official24hAsOf is
	// when the earliest ticker in the official values was received (Unix
	// seconds), or 0 without official values.
	Official24hStatus string      `json:"official_24h_status"`
	Official24hAsOf   float64     `json:"official_24h_as_of"`
	Ranking           string      `json:"ranking"`
	Coins             []CoinShare `json:"coins"`
}

// CoinShare is one coin's share over the official 24 hours and the window.
type CoinShare struct {
	Asset       string         `json:"asset"`
	Official24h CoinShareValue `json:"official_24h"`
	Realtime    CoinShareValue `json:"realtime"`
}

// CoinShareValue holds a coin's domestic and Bithumb trading and Bithumb's
// share of it. Values that are not known stay nil.
type CoinShareValue struct {
	Status            string   `json:"status"`
	DomesticVolumeKRW *float64 `json:"domestic_volume_krw"`
	BithumbVolumeKRW  *float64 `json:"bithumb_volume_krw"`
	BithumbSharePct   *float64 `json:"bithumb_share_pct"`
	// UnresolvedFXCount is how many trades have no KRW value (UNRESOLVED_FX).
	UnresolvedFXCount int64 `json:"unresolved_fx_count,omitempty"`
}

type CoinSharesInput struct {
	MarketScope string
	WindowSec   int64
	AsOfSecond  int64
	// Window and Rows are the realtime window's state and rows.
	Window WindowState
	Rows   []Row
	// OfficialRows are the official 24-hour tickers' coin rows, or nil.
	OfficialRows      []Row
	Official24hStatus string
	Official24hAsOf   float64
	// Realtime24hRows order the coins while there are no official rows.
	Realtime24hRows []Row
	Limit           int
}

// BuildCoinShares lists up to Limit coins with the most domestic trading.
func BuildCoinShares(input CoinSharesInput) CoinShares {
	window, official := rowsByCoin(input.Rows), rowsByCoin(input.OfficialRows)
	supplemented, _ := input.Window.DataSource["official_minute_supplement_used"].(bool)
	result := CoinShares{
		MarketScope: input.MarketScope, WindowSec: input.WindowSec, Window: aggregate.WindowName(input.WindowSec),
		AsOfSecond: input.AsOfSecond, WindowComplete: input.Window.WindowComplete, CoverageSec: input.Window.CoverageSec,
		OfficialMinuteSupplementUsed: supplemented,
		Official24hStatus:            input.Official24hStatus, Official24hAsOf: input.Official24hAsOf,
		Ranking: CoinShareRankingOfficial24h, Coins: []CoinShare{},
	}
	ranking := official
	if input.OfficialRows == nil {
		result.Ranking, ranking = CoinShareRankingRealtime24h, rowsByCoin(input.Realtime24hRows)
	}
	for _, asset := range largestCoins(ranking, input.Limit) {
		coin := CoinShare{
			Asset: asset, Official24h: CoinShareValue{Status: CoinShareUnavailable},
			Realtime: realtimeCoinShare(coinRowsFor(window, asset), input.Window.WindowComplete),
		}
		if input.OfficialRows != nil {
			coin.Official24h = realtimeCoinShare(coinRowsFor(official, asset), true)
		}
		result.Coins = append(result.Coins, coin)
	}
	return result
}

// The live coin shares: the window each covers, long enough that most listed
// coins trade in it and short enough to move with the market, and how many
// coins they list, eight rows of seven tiles on the page.
const (
	LiveCoinShareWindowSec = 60
	LiveCoinShareCount     = 56
)

// LiveCoinShares is Bithumb's share of each listed coin's domestic trading in
// the last minute, read anew every second for the WebSocket.
type LiveCoinShares struct {
	WindowSec int64 `json:"window_sec"`
	// AsOfSecond is the last closed second the window includes.
	AsOfSecond     int64           `json:"as_of_second"`
	WindowComplete bool            `json:"window_complete"`
	CoverageSec    int64           `json:"coverage_sec"`
	Ranking        string          `json:"ranking"`
	Coins          []LiveCoinShare `json:"coins"`
}

// LiveCoinShare is one coin's share of the window.
type LiveCoinShare struct {
	Asset string `json:"asset"`
	CoinShareValue
}

type LiveCoinSharesInput struct {
	// Coins are the coins to list, in order, with the order's name.
	Coins   []CoinShare
	Ranking string
	// Rows are the window's rows; it ends at AsOfSecond.
	Rows           []Row
	WindowSec      int64
	AsOfSecond     int64
	WindowComplete bool
	CoverageSec    int64
}

// BuildLiveCoinShares gives each coin, in the given order, its share of the
// window.
func BuildLiveCoinShares(input LiveCoinSharesInput) LiveCoinShares {
	window := rowsByCoin(input.Rows)
	result := LiveCoinShares{
		WindowSec: input.WindowSec, AsOfSecond: input.AsOfSecond, WindowComplete: input.WindowComplete,
		CoverageSec: input.CoverageSec, Ranking: input.Ranking, Coins: make([]LiveCoinShare, 0, len(input.Coins)),
	}
	for _, coin := range input.Coins {
		result.Coins = append(result.Coins, LiveCoinShare{
			Asset: coin.Asset, CoinShareValue: realtimeCoinShare(coinRowsFor(window, coin.Asset), input.WindowComplete),
		})
	}
	return result
}

// FirstCoins is the list cut to its first limit coins. The coins stay shared
// with the full list.
func (c CoinShares) FirstCoins(limit int) CoinShares {
	count := max(0, min(limit, len(c.Coins)))
	c.Coins = c.Coins[:count:count]
	return c
}

type coinRows struct {
	rows    []Row
	invalid bool
}

// Validate per coin so a bad market cannot erase evidence of invalid input or
// suppress unrelated coins. A market attributed to two assets invalidates both.
func rowsByCoin(rows []Row) map[string]coinRows {
	result := make(map[string]coinRows)
	identities := make(map[string]string)
	unidentified := false
	for _, row := range rows {
		if row.Scope != domain.ScopeDomestic {
			continue
		}
		asset := strings.ToUpper(strings.TrimSpace(row.Base))
		if asset == "" {
			unidentified = true
			continue
		}
		group := result[asset]
		group.rows = append(group.rows, row)
		identity := strings.ToLower(strings.TrimSpace(string(row.Exchange))) + "/" + strings.ToUpper(strings.TrimSpace(row.Market))
		if previous, exists := identities[identity]; exists && previous != asset {
			other := result[previous]
			other.invalid, group.invalid = true, true
			result[previous] = other
		}
		identities[identity] = asset
		result[asset] = group
	}
	for asset, group := range result {
		canonical, valid := canonicalDomesticRows(group.rows)
		group.rows, group.invalid = canonical, group.invalid || !valid
		result[asset] = group
	}
	if unidentified {
		// Without an asset identity no coin can safely claim observed zero.
		result[""] = coinRows{invalid: true}
	}
	return result
}

func coinRowsFor(groups map[string]coinRows, asset string) coinRows {
	group := groups[asset]
	group.invalid = group.invalid || groups[""].invalid
	return group
}

// realtimeCoinShare is a coin's share of the window from its domestic rows.
// No trades in a partly observed window are unknown, not zero.
func realtimeCoinShare(group coinRows, windowComplete bool) CoinShareValue {
	if group.invalid {
		return CoinShareValue{Status: CoinShareUnavailable}
	}
	rows := group.rows
	if !hasObservedRows(rows) && !windowComplete {
		return CoinShareValue{Status: CoinShareNotObserved}
	}
	if unresolved := sumUnresolvedFX(rows); unresolved > 0 {
		return CoinShareValue{Status: CoinShareUnresolvedFX, UnresolvedFXCount: unresolved}
	}
	return coinShareValue(totalAndBithumb(rows))
}

func coinShareValue(total, bithumb float64) CoinShareValue {
	value := CoinShareValue{
		Status: CoinShareAvailable, DomesticVolumeKRW: pointer(total),
		BithumbVolumeKRW: pointer(bithumb), BithumbSharePct: percent(bithumb, total),
	}
	if value.BithumbSharePct == nil {
		value.Status = CoinShareNoTrades
	}
	return value
}

func sumUnresolvedFX(rows []Row) int64 {
	total := int64(0)
	for _, row := range rows {
		total = saturatingAddInt64(total, row.UnresolvedFXCount)
	}
	return total
}

// largestCoins is up to limit coins by domestic trading, largest first.
func largestCoins(rowsByCoin map[string]coinRows, limit int) []string {
	volumes := make(map[string]float64, len(rowsByCoin))
	coins := make([]string, 0, len(rowsByCoin))
	for asset, group := range rowsByCoin {
		if asset == "" {
			continue
		}
		volumes[asset], _ = totalAndBithumb(group.rows)
		coins = append(coins, asset)
	}
	sort.Slice(coins, func(i, j int) bool {
		left, right := coinRowsFor(rowsByCoin, coins[i]), coinRowsFor(rowsByCoin, coins[j])
		if left.invalid != right.invalid {
			return !left.invalid
		}
		if left.invalid {
			return coins[i] < coins[j]
		}
		if volumes[coins[i]] != volumes[coins[j]] {
			return volumes[coins[i]] > volumes[coins[j]]
		}
		return coins[i] < coins[j]
	})
	return coins[:max(0, min(limit, len(coins)))]
}
