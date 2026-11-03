package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

// The index screen only needs each asset's listing shape and the set of assets
// named by notices inside the selected window. It used to read the whole
// analysis for that, computing and transferring liquidity and reaction evidence
// it discards. Capable providers build just the projection inputs from the same
// shared market observation; older providers retain the full-analysis fallback.
const indexFiltersContractVersion = "INDEX_FILTERS_V1"

// IndexFiltersProvider supplies only Assets, Notices.ContextualItems, WindowSec
// and GeneratedAt. Its result is not a complete analysis response.
type IndexFiltersProvider interface {
	IndexFilterAnalysis(context.Context, int64, string) (marketanalysis.Analysis, error)
}

type IndexFiltersResponse struct {
	ContractVersion string             `json:"contract_version"`
	WindowSec       int64              `json:"window_sec"`
	MarketScope     string             `json:"market_scope"`
	GeneratedAt     float64            `json:"generated_at"`
	Assets          []IndexFilterAsset `json:"assets"`
	// NoticeAssets is the asset label set the index screen marks as "공지 있음".
	// An in-window notice contributes its official assets, or the assets its
	// market context resolved when the notice carries none.
	NoticeAssets []string `json:"notice_assets"`
}

type IndexFilterAsset struct {
	Asset              string `json:"asset"`
	ListedOnBithumb    bool   `json:"listed_on_bithumb"`
	ExclusiveOnBithumb bool   `json:"exclusive_on_bithumb"`
	SharedBithumbUpbit bool   `json:"shared_bithumb_upbit"`
}

func buildIndexFilters(payload marketanalysis.Analysis, marketScope string) IndexFiltersResponse {
	assets := make([]IndexFilterAsset, 0, len(payload.Assets))
	for _, asset := range payload.Assets {
		assets = append(assets, IndexFilterAsset{
			Asset:              asset.Asset,
			ListedOnBithumb:    asset.ListedOnBithumb,
			ExclusiveOnBithumb: asset.ExclusiveOnBithumb,
			SharedBithumbUpbit: asset.SharedBithumbUpbit,
		})
	}
	return IndexFiltersResponse{
		ContractVersion: indexFiltersContractVersion,
		WindowSec:       payload.WindowSec,
		MarketScope:     marketScope,
		GeneratedAt:     payload.GeneratedAt,
		Assets:          assets,
		NoticeAssets:    inWindowNoticeAssets(payload.Notices.ContextualItems),
	}
}

func inWindowNoticeAssets(items []any) []string {
	seen := make(map[string]struct{})
	for _, entry := range items {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if selected, _ := row["within_selected_window"].(bool); !selected {
			continue
		}
		official := noticeAssetStrings(row["assets"])
		if len(official) == 0 {
			official = marketContextAssetStrings(row["market_context"])
		}
		for _, asset := range official {
			seen[asset] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for asset := range seen {
		result = append(result, asset)
	}
	sort.Strings(result)
	return result
}

// Contextual rows built in process carry the notice's typed []string, while a
// row that crossed JSON carries []any. Reading only one of them silently drops
// every official asset of the other form.
func noticeAssetStrings(value any) []string {
	var values []string
	switch typed := value.(type) {
	case []string:
		values = typed
	case []any:
		values = make([]string, 0, len(typed))
		for _, entry := range typed {
			if asset, ok := entry.(string); ok {
				values = append(values, asset)
			}
		}
	default:
		return nil
	}
	result := make([]string, 0, len(values))
	for _, asset := range values {
		if strings.TrimSpace(asset) != "" {
			result = append(result, asset)
		}
	}
	return result
}

func marketContextAssetStrings(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, entry := range values {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if asset, _ := row["asset"].(string); strings.TrimSpace(asset) != "" {
			result = append(result, asset)
		}
	}
	return result
}

func registerIndexFilters(mux *http.ServeMux, provider AnalysisProvider) {
	mux.HandleFunc("GET /api/market-share/index-filters", func(writer http.ResponseWriter, request *http.Request) {
		if provider == nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": "market-share service is not ready"})
			return
		}
		window, ok := queryInt64(request, "window", 86_400)
		if !ok || !validWindow(window) {
			badWindow(writer)
			return
		}
		marketScope, ok := queryMarketScope(request)
		if !ok {
			badMarketScope(writer)
			return
		}
		var payload marketanalysis.Analysis
		var err error
		if filters, ok := provider.(IndexFiltersProvider); ok {
			payload, err = filters.IndexFilterAnalysis(request.Context(), window, marketScope)
		} else {
			payload, err = provider.Analysis(request.Context(), window, marketScope, 0)
		}
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"detail": err.Error()})
			return
		}
		writeRequestJSON(writer, request, http.StatusOK, buildIndexFilters(payload, marketScope))
	})
}
