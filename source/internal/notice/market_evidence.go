package notice

// MarketEvidenceItem is the immutable, pre-analyzed subset reused by market
// reaction consumers. Building it once avoids repeated body parsing and
// schedule extraction for every analysis projection.
type MarketEvidenceItem struct {
	Item             Item
	Brief            Brief
	CurrentLifecycle bool
}

func PrepareMarketEvidenceSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Items = AssignEventGroupIDs(snapshot.Items)
	snapshot.MarketEvidence = buildMarketEvidenceItems(snapshot.Items)
	snapshot.MarketEvidencePrepared = true
	return snapshot
}

func PreparedMarketEvidence(snapshot Snapshot) []MarketEvidenceItem {
	if snapshot.MarketEvidencePrepared {
		return snapshot.MarketEvidence
	}
	return buildMarketEvidenceItems(AssignEventGroupIDs(snapshot.Items))
}

func buildMarketEvidenceItems(items []Item) []MarketEvidenceItem {
	latest := make(map[string]Item)
	for _, item := range items {
		if item.EventGroupID == "" || !IsMarketEvidenceExchange(item.Exchange) {
			continue
		}
		previous, found := latest[item.EventGroupID]
		if !found || marketEvidenceRecency(item) > marketEvidenceRecency(previous) {
			latest[item.EventGroupID] = item
		}
	}
	result := make([]MarketEvidenceItem, 0)
	for _, item := range items {
		if !IsMarketEvidenceExchange(item.Exchange) {
			continue
		}
		imageConsistency := VerifyItemImageConsistency(item)
		brief := summarizeWithImageConsistency(item, imageConsistency)
		item.Schedules = brief.Schedules
		item.preparedImageConsistency = imageConsistency
		item.imageConsistencyPrepared = true
		current := item.EventGroupID == "" || latest[item.EventGroupID].NoticeID == item.NoticeID
		result = append(result, MarketEvidenceItem{Item: item, Brief: brief, CurrentLifecycle: current})
	}
	return result
}

// ImageConsistencyForAnalysis reuses the content-only verification attached
// to a prepared market-evidence item. Raw notice items retain the same
// behavior by calculating the value on demand.
func ImageConsistencyForAnalysis(item Item) ImageConsistency {
	if item.imageConsistencyPrepared {
		return item.preparedImageConsistency
	}
	return VerifyItemImageConsistency(item)
}

func marketEvidenceRecency(item Item) int64 {
	result := item.PublishedTSMS
	if item.UpdatedTSMS > result {
		result = item.UpdatedTSMS
	}
	if item.DetectedTSMS > result {
		result = item.DetectedTSMS
	}
	return result
}
