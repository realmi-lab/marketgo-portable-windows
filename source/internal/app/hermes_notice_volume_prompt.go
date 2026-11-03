package app

import "encoding/json"

// marshalDomesticNoticeQuestionPrompt retains the ordinary evidence schema
// unless its byte budget is exceeded. Repeated volume field names can then be
// represented once as columns without dropping dates, observations or values.
// This is a model projection only: typed evidence and fallback rendering retain
// their original records. The caller still owns the final whole-prompt budget.
func marshalDomesticNoticeQuestionPrompt(evidence domesticNoticeQuestionEvidence, maximumBytes int) ([]byte, error) {
	payload, err := json.Marshal(evidence)
	if err != nil || maximumBytes <= 0 || len(payload) <= maximumBytes || len(evidence.DailyAssetVolumes) == 0 {
		return payload, err
	}

	volume := domesticNoticeVolumePromptTable{
		Columns: domesticNoticeVolumePromptColumns{
			Days:   []string{"day", "status", "coverage_status", "coverage_sec", "required_sec", "observed_market_count", "error", "assets", "interval_start_kst", "interval_end_kst_exclusive", "coverage_scope"},
			Assets: []string{"volume_krw", "trade_count", "market_count", "observed_market_count", "exchanges"},
		},
		Days: make([][]any, 0, len(evidence.DailyAssetVolumes)),
	}
	for _, day := range evidence.DailyAssetVolumes {
		// Preserve a missing assets array separately from an observed empty
		// array. Zero amounts remain explicit values in an asset row.
		var assets []domesticNoticeVolumePromptAsset
		if day.Assets != nil {
			assets = make([]domesticNoticeVolumePromptAsset, 0, len(day.Assets))
		}
		for _, asset := range day.Assets {
			assets = append(assets, domesticNoticeVolumePromptAsset{
				Asset: asset.Asset,
				Values: []any{
					asset.VolumeKRW, asset.TradeCount, asset.MarketCount,
					asset.ObservedCount, asset.Exchanges,
				},
			})
		}
		volume.Days = append(volume.Days, []any{
			day.Day, day.Status, day.CoverageStatus, day.CoverageSec, day.RequiredSec,
			day.ObservedMarketCount, day.Error, assets,
			day.IntervalStartKST, day.IntervalEndKSTExclusive, day.CoverageScope,
		})
	}

	// Embedding the same evidence type preserves its JSON tags and all other
	// fields. The explicit shallow field replaces only daily_asset_volumes.
	return json.Marshal(struct {
		domesticNoticeQuestionEvidence
		DailyAssetVolumes domesticNoticeVolumePromptTable `json:"daily_asset_volumes"`
	}{domesticNoticeQuestionEvidence: evidence, DailyAssetVolumes: volume})
}

type domesticNoticeVolumePromptColumns struct {
	Days   []string `json:"days"`
	Assets []string `json:"assets"`
}

// The asset key remains an identity boundary for evidence validators. Putting
// the ticker in an unnamed value position could promote its digits into facts.
// Columns.Assets describes Values only; it never describes the Asset identity.
type domesticNoticeVolumePromptAsset struct {
	Asset  string `json:"asset"`
	Values []any  `json:"values"`
}

type domesticNoticeVolumePromptTable struct {
	Columns domesticNoticeVolumePromptColumns `json:"columns"`
	Days    [][]any                           `json:"days"`
}
