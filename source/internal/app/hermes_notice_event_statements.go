package app

import "market-share-go/internal/hermesrunner"

// Compute only after original evidence fits; the caller admits annotations
// only into spare budget. Do not read summaries, schedules or wall time.
func annotateDomesticNoticeEventStatements(evidence *domesticNoticeQuestionEvidence) {
	for i := range evidence.Notices {
		row := &evidence.Notices[i]
		row.EventStatements = hermesrunner.NoticeQuestionEventStatements(row.SourceID, row.Exchange, row.Title, row.Assets, row.BodyPassages)
	}
}
