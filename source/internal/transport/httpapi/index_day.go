package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"market-share-go/internal/indexday"
)

type indexDayProvider interface {
	IndexDay(context.Context, string) (indexday.Response, error)
}

func registerIndexDay(mux *http.ServeMux, provider any) {
	service, ok := provider.(indexDayProvider)
	mux.HandleFunc("GET /api/market-share/index-day", func(w http.ResponseWriter, r *http.Request) {
		day := r.URL.Query().Get("day")
		_, _, err := indexday.Window(day, time.Now())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "오늘 또는 지난 날짜를 YYYY-MM-DD 형식으로 선택하세요."})
			return
		}
		if !ok {
			unavailable(w)
			return
		}
		if source := r.URL.Query().Get("source"); source == "hourly_rolling" || source == "ticker_24h" {
			if day != time.Now().In(indexday.Seoul).Format(time.DateOnly) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "최신 롤링 합계는 오늘 날짜로 조회하세요."})
				return
			}
			if source == "ticker_24h" {
				day = "ticker:" + day
			} else {
				day = "rolling:" + day
			}
		}
		payload, err := service.IndexDay(r.Context(), day)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "선택한 날짜의 시간별 자료를 불러오지 못했습니다."})
			return
		}
		if payload.StorageMode == "ROLLING_24H_LATEST" || payload.StorageMode == "MEMORY_HOURLY" {
			payload.Domestic = rollingResponseGrid(payload, r.URL.Query())
		}
		if payload.StorageMode == "TICKER_24H" {
			payload.Domestic = tickerFilterGrid(payload, r.URL.Query())
		}
		if r.URL.Query().Get("projection") == "domestic_grid" {
			writeRequestJSON(w, r, http.StatusOK, indexGridProjection(payload))
			return
		}
		writeRequestJSON(w, r, http.StatusOK, payload)
	})
}

func tickerFilterGrid(payload indexday.Response, query url.Values) indexday.DomesticGrid {
	assets := map[string]bool{}
	for _, asset := range strings.Split(query.Get("assets"), ",") {
		assets[asset] = true
	}
	rows := []indexday.RealtimeRow{}
	for _, row := range payload.Rows {
		if query.Has("assets") && !assets[row.Base] {
			continue
		}
		quote, venue := query.Get("quote"), query.Get("venue")
		if quote != "" && quote != "all" && !strings.EqualFold(quote, row.Quote) {
			continue
		}
		if venue != "" && venue != "all" && venue != row.Exchange && !(venue == "combined" && (row.Exchange == "bithumb" || row.Exchange == "upbit")) {
			continue
		}
		rows = append(rows, indexday.RealtimeRow{Market: row.Market, Amount: row.VolumeKRW, Partial: payload.Status != "COMPLETE" || row.Status != "COMPLETE"})
	}
	return indexday.BuildRealtimeDomestic(rows)
}

// Filter controls consume only the server-calculated grid. Keep the full
// archive contract unchanged for readers that need per-market evidence.
type indexGridResponse struct {
	ContractVersion string                `json:"contract_version"`
	Day             string                `json:"day"`
	Status          string                `json:"status"`
	StorageMode     string                `json:"storage_mode,omitempty"`
	Domestic        indexday.DomesticGrid `json:"domestic"`
}

func indexGridProjection(payload indexday.Response) indexGridResponse {
	return indexGridResponse{"INDEX_DAY_GRID_V1", payload.Day, payload.Status, payload.StorageMode, payload.Domestic}
}

func rollingResponseGrid(payload indexday.Response, query url.Values) indexday.DomesticGrid {
	quote, venue := query.Get("quote"), query.Get("venue")
	// The repository already built this immutable grid. Only filters need a
	// new denominator; do not cache across requests or mutate the source grid.
	if !query.Has("assets") && (quote == "" || quote == "all") && (venue == "" || venue == "all") && payload.Domestic.Totals != nil {
		return payload.Domestic
	}
	return rollingFilterGrid(payload.Rows, query)
}

func rollingFilterGrid(rows []indexday.Row, query url.Values) indexday.DomesticGrid {
	assets := map[string]bool{}
	for _, asset := range strings.Split(query.Get("assets"), ",") {
		assets[asset] = true
	}
	hasAssets := query.Has("assets")
	quote, venue := query.Get("quote"), query.Get("venue")
	capacity := len(rows)
	if hasAssets {
		capacity = min(capacity, 16)
	}
	selected := make([]indexday.Row, 0, capacity)
	for _, row := range rows {
		if hasAssets && !assets[row.Base] {
			continue
		}
		if quote != "" && quote != "all" && !strings.EqualFold(quote, row.Quote) {
			continue
		}
		if venue != "" && venue != "all" {
			if venue == "combined" {
				if row.Exchange != "bithumb" && row.Exchange != "upbit" {
					continue
				}
			} else if row.Exchange != venue {
				continue
			}
		}
		selected = append(selected, row)
	}
	return indexday.BuildDomestic(selected)
}
