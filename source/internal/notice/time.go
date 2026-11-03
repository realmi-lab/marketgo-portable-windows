package notice

// PublicationTimestampMS is the chronology key for "latest notices" lists.
// An official modification is revision metadata and must not make an older
// notice look newly published. Detection is the explicit fallback when an
// official publication timestamp is unavailable.
func PublicationTimestampMS(published, updated, detected int64) int64 {
	if published > 0 {
		return published
	}
	if detected > 0 {
		return detected
	}
	return updated
}

// RevisionTimestampMS is the freshness key for incremental collection. It is
// intentionally separate from publication chronology so an official update
// can trigger evidence refresh without reordering the public notice list.
func RevisionTimestampMS(published, updated, detected int64) int64 {
	if official := max(published, updated); official > 0 {
		return official
	}
	return detected
}
