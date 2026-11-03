package httpapi

import (
	"net/http"

	marketanalysis "market-share-go/internal/analysis"
)

// The notice portal reads three things from the 10-minute KRW analysis: each
// coin's observed volume, the window that volume covers, and the dated domestic
// notices. It used to download the whole analysis (about 6 MB) every minute for
// them. This projection copies those fields from the same cached analysis and
// keeps their JSON names, so nothing is recomputed or filled in.
const (
	noticePortalWindowSec   = 600
	noticePortalMarketScope = "KRW"
)

type NoticePortalResponse struct {
	Context    NoticePortalContext `json:"context"`
	Assets     []NoticePortalAsset `json:"assets"`
	Rolling24h NoticePortalRolling `json:"rolling_24h"`
}

type NoticePortalContext struct {
	SelectedWindowComplete    bool    `json:"selected_window_complete"`
	SelectedWindowCoverageSec int64   `json:"selected_window_coverage_sec"`
	SelectedWindowRequiredSec int64   `json:"selected_window_required_sec"`
	CurrentIntervalStart      float64 `json:"current_interval_start"`
	CurrentIntervalEnd        float64 `json:"current_interval_end"`
}

type NoticePortalAsset struct {
	Asset                string   `json:"asset"`
	DomesticVolumeKRW    *float64 `json:"domestic_volume_krw"`
	DomesticTradeCount   int64    `json:"domestic_trade_count"`
	DomesticBaseQuantity float64  `json:"domestic_base_quantity"`
}

type NoticePortalRolling struct {
	AsOfMS   int64                `json:"as_of_ms"`
	Evidence NoticePortalEvidence `json:"evidence"`
}

type NoticePortalEvidence struct {
	DomesticNotices []marketanalysis.RollingNoticeEvidence `json:"domestic_notices"`
}

func buildNoticePortal(payload marketanalysis.Analysis) NoticePortalResponse {
	assets := make([]NoticePortalAsset, 0, len(payload.Assets))
	for _, asset := range payload.Assets {
		assets = append(assets, NoticePortalAsset{
			Asset:                asset.Asset,
			DomesticVolumeKRW:    asset.DomesticVolumeKRW,
			DomesticTradeCount:   asset.DomesticTradeCount,
			DomesticBaseQuantity: asset.DomesticBaseQuantity,
		})
	}
	notices := payload.Rolling24h.Evidence.DomesticNotices
	if notices == nil {
		notices = []marketanalysis.RollingNoticeEvidence{}
	}
	context := payload.Context
	return NoticePortalResponse{
		Context: NoticePortalContext{
			SelectedWindowComplete:    context.SelectedWindowComplete,
			SelectedWindowCoverageSec: context.SelectedWindowCoverageSec,
			SelectedWindowRequiredSec: context.SelectedWindowRequiredSec,
			CurrentIntervalStart:      context.CurrentIntervalStart,
			CurrentIntervalEnd:        context.CurrentIntervalEnd,
		},
		Assets: assets,
		Rolling24h: NoticePortalRolling{
			AsOfMS:   payload.Rolling24h.AsOfMS,
			Evidence: NoticePortalEvidence{DomesticNotices: notices},
		},
	}
}

func registerNoticePortal(mux *http.ServeMux, provider AnalysisProvider) {
	mux.HandleFunc("GET /api/market-share/notice-portal", func(writer http.ResponseWriter, request *http.Request) {
		if provider == nil {
			unavailable(writer)
			return
		}
		payload, err := provider.Analysis(request.Context(), noticePortalWindowSec, noticePortalMarketScope, 0)
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, buildNoticePortal(payload))
	})
}
