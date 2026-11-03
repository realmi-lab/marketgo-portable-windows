package httpapi

import (
	"context"
	"market-share-go/internal/indexday"
	"net/http"
	"strings"
	"time"
)

type realtimeIndexProvider interface {
	RealtimeIndex(context.Context, int64, indexday.RealtimeFilter) (indexday.RealtimeResponse, error)
}

func registerRealtimeIndex(mux *http.ServeMux, provider any) {
	service, ok := provider.(realtimeIndexProvider)
	mux.HandleFunc("GET /api/market-share/realtime-index", func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			unavailable(w)
			return
		}
		window, valid := queryInt64(r, "window", 10)
		if !valid || !validWindow(window) {
			badWindow(w)
			return
		}
		q := r.URL.Query()
		quote := strings.ToUpper(q.Get("quote"))
		venue := strings.ToLower(q.Get("venue"))
		if quote == "" || quote == "ALL" {
			quote = "all"
		}
		if venue == "" {
			venue = "all"
		}
		if quote != "all" && quote != "KRW" && quote != "BTC" && quote != "USDT" {
			unprocessable(w, "unsupported quote")
			return
		}
		venues := map[string]bool{"all": true, "combined": true, "bithumb": true, "upbit": true, "coinone": true, "korbit": true, "gopax": true}
		if !venues[venue] {
			unprocessable(w, "unsupported venue")
			return
		}
		assets := map[string]bool{}
		for _, asset := range strings.Split(q.Get("assets"), ",") {
			if asset = strings.ToUpper(strings.TrimSpace(asset)); asset != "" {
				assets[asset] = true
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		payload, err := service.RealtimeIndex(ctx, window, indexday.RealtimeFilter{Assets: assets, FilterAssets: q.Has("assets"), Quote: quote, Venue: venue})
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "실시간 코인 자료를 불러오지 못했습니다."})
			return
		}
		writeRequestJSON(w, r, http.StatusOK, payload)
	})
}
