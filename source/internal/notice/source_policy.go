package notice

import "strings"

// Temporary operational pause, not removal of adapters or persisted evidence.
// Restore only with an explicitly reviewed patch. This policy is notice-only;
// it must never control market ticker/WebSocket collection.
const foreignNoticeSourcesPaused = true

func SourcePaused(exchange string) bool {
	if !foreignNoticeSourcesPaused {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "binance", "bybit", "okx":
		return true
	default:
		return false
	}
}

func ActiveSources() []SourceConfig {
	result := []SourceConfig{}
	for _, source := range DefaultSources() {
		if !SourcePaused(source.Exchange) {
			result = append(result, source)
		}
	}
	return result
}

// ActiveSourceSnapshot projects the raw repository read before any shared
// market/LLM evidence is prepared. It never mutates or deletes stored rows.
func ActiveSourceSnapshot(snapshot Snapshot) Snapshot {
	if !foreignNoticeSourcesPaused {
		return snapshot
	}
	result := snapshot
	// Repository versions are epoch milliseconds. Give the paused projection a
	// distinct identity so an in-flight archive page from before the patch gets
	// 409 instead of silently mixing two source scopes under the same version.
	result.Version = snapshot.Version*2 + 1
	result.Items = make([]Item, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if !SourcePaused(item.Exchange) {
			result.Items = append(result.Items, item)
		}
	}
	result.Sources = make(map[string]Source, len(snapshot.Sources))
	result.HealthySourceCount, result.CompleteHistorySourceCount = 0, 0
	for exchange, source := range snapshot.Sources {
		if SourcePaused(exchange) {
			continue
		}
		result.Sources[exchange] = source
		if source.Error == "" {
			result.HealthySourceCount++
		}
		if source.HistoryStatus == "COMPLETE" || source.HistoryStatus == "HERMES_30D_ACTIVE" {
			result.CompleteHistorySourceCount++
		}
	}
	result.SourceCount = len(result.Sources)
	result.ErrorHistory = make([]SourceError, 0, len(snapshot.ErrorHistory))
	active := map[string]bool{}
	for _, item := range snapshot.ErrorHistory {
		if SourcePaused(item.Exchange) {
			continue
		}
		result.ErrorHistory = append(result.ErrorHistory, item)
		if item.Active {
			active[item.Exchange] = true
		}
	}
	result.ActiveErrorCount = len(active)
	result.ContextualItems, result.MarketEvidence = nil, nil
	result.MarketEvidencePrepared = false
	return result
}
