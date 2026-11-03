package app

import "market-share-go/internal/tickertrend"

func (s *Service) officialTickerTrendState() tickertrend.State {
	if s == nil || s.tickerTrend == nil {
		return tickertrend.State{
			Version: tickertrend.Version, IntervalSec: tickertrend.DefaultInterval,
			Status: "UNAVAILABLE", Reason: "TRACKER_NOT_CONFIGURED",
		}
	}
	return s.tickerTrend.State()
}

func officialTickerTrendInterval(state tickertrend.State) (int64, int64, bool) {
	if state.Status != "LIVE" || state.Previous == nil || state.Current == nil {
		return 0, 0, false
	}
	start := state.Previous.ObservedAtMS / 1000
	end := state.Current.ObservedAtMS / 1000
	if start <= 0 || end <= start {
		return 0, 0, false
	}
	return start, end, true
}

func enrichOfficialTickerTrend(
	state tickertrend.State,
	read analysisReadView,
	start int64,
	end int64,
	ready bool,
) tickertrend.State {
	if !ready {
		return state
	}
	values := filterDomesticKRW(read.interval(start, end))
	coverage := read.coverageFor(start+1, end)
	markets := make([]tickertrend.IncomingMarket, 0, len(values))
	for key, totals := range values {
		markets = append(markets, tickertrend.IncomingMarket{
			Exchange: string(key.Exchange), Asset: key.Base, VolumeKRW: totals.KRWVolume,
		})
	}
	requiredSec := end - start
	webSocketCoverage := min(requiredSec, coverage.WebSocketCoverage)
	webSocketStatus := "PARTIAL"
	if webSocketCoverage <= 0 {
		webSocketStatus = "UNAVAILABLE"
	} else if webSocketCoverage >= requiredSec {
		webSocketStatus = "CONFIRMED"
	}
	complete := webSocketStatus == "CONFIRMED"
	return tickertrend.WithIncomingEstimate(state, markets, webSocketStatus, webSocketCoverage, complete)
}
