package httpapi

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/notice"
)

const noticeArchiveDays int64 = 30
const noticeArchiveDayMS int64 = 86_400_000

type noticeArchivePage struct {
	Snapshot     any                            `json:"snapshot"`
	Total        int                            `json:"total"`
	Offset       int                            `json:"offset"`
	Limit        int                            `json:"limit"`
	AsOfMS       int64                          `json:"as_of_ms"`
	FromMS       int64                          `json:"from_ms"`
	SourceCounts map[string]noticeArchiveCounts `json:"source_counts"`
}

// noticeHeadline is a notice as a list of titles shows it, asked for with
// fields=headline: about a twentieth of the whole item, whose body, images
// and analysis such a list never reads.
type noticeHeadline struct {
	Exchange           string   `json:"exchange"`
	NoticeID           string   `json:"notice_id"`
	Title              string   `json:"title"`
	URL                string   `json:"url"`
	Category           string   `json:"category"`
	PublishedTSMS      int64    `json:"published_ts_ms"`
	DetectedTSMS       int64    `json:"detected_ts_ms"`
	TimestampPrecision string   `json:"timestamp_precision"`
	Assets             []string `json:"assets"`
	AnalysisStatus     string   `json:"analysis_status"`
}

// noticeSnapshotFields is notice.Snapshot without its JSON method, so a
// struct that embeds it can replace its items.
type noticeSnapshotFields notice.Snapshot

// noticeHeadlineSnapshot is the snapshot with its items as headlines; the
// outer Items hides the embedded one.
type noticeHeadlineSnapshot struct {
	noticeSnapshotFields
	Items []noticeHeadline `json:"items"`
}

func noticeHeadlines(items []notice.Item) []noticeHeadline {
	rows := make([]noticeHeadline, len(items))
	for index, item := range items {
		rows[index] = noticeHeadline{
			Exchange: item.Exchange, NoticeID: item.NoticeID, Title: item.Title,
			URL: item.URL, Category: item.Category,
			PublishedTSMS: item.PublishedTSMS, DetectedTSMS: item.DetectedTSMS,
			TimestampPrecision: item.TimestampPrecision, Assets: item.Assets,
			AnalysisStatus: item.AnalysisStatus,
		}
	}
	return rows
}

type noticeArchiveCounts struct {
	Stored       int `json:"stored"`
	BodyMissing  int `json:"body_missing"`
	ImagePending int `json:"image_pending"`
}

// The archive reads the shared immutable raw snapshot. It never expands the
// market-reaction calculation or streams the entire archive on every tick.
func noticeArchiveHandler(provider NoticeProvider, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixMilli()
		limit, limitOK := queryInt64(r, "limit", 20)
		offset, offsetOK := queryInt64(r, "offset", 0)
		asOf, asOfOK := queryInt64(r, "as_of_ms", now)
		version, versionOK := queryInt64(r, "version", 0)
		if !limitOK || limit < 1 || limit > 100 || !offsetOK || offset < 0 || offset > 1_000_000 || !asOfOK || asOf < now-noticeArchiveDayMS || asOf > now || !versionOK || version < 0 || offset > 0 && (r.URL.Query().Get("as_of_ms") == "" || r.URL.Query().Get("version") == "") {
			unprocessable(w, "invalid notice page: limit 1..100, nonnegative offset/version and a recent server as_of_ms are required")
			return
		}
		if provider == nil {
			unavailable(w)
			return
		}
		value, err := provider.NoticeSnapshot(r.Context())
		if err != nil {
			logRequestFailure(logger, r, "notice archive read failed", err)
			unavailable(w)
			return
		}
		var snapshot notice.Snapshot
		switch typed := value.(type) {
		case notice.Snapshot:
			snapshot = typed
		case *notice.Snapshot:
			if typed == nil {
				unavailable(w)
				return
			}
			snapshot = *typed
		default:
			unavailable(w)
			return
		}
		// Never silently mix offset pages from two versions after a new ingest.
		if offset > 0 && version != snapshot.Version {
			writeJSON(w, http.StatusConflict, map[string]string{"detail": "notice archive changed; reload the first page"})
			return
		}
		from := asOf - noticeArchiveDays*noticeArchiveDayMS
		rows := make([]notice.Item, 0, len(snapshot.Items))
		counts := make(map[string]noticeArchiveCounts, len(snapshot.Sources))
		for exchange := range snapshot.Sources {
			counts[exchange] = noticeArchiveCounts{}
		}
		for _, item := range snapshot.Items {
			stamp := noticePublicationMS(item)
			if stamp >= from && stamp <= asOf {
				rows = append(rows, item)
				count := counts[item.Exchange]
				count.Stored++
				if strings.TrimSpace(item.OriginalBodyText) == "" && strings.TrimSpace(item.BodyExcerpt) == "" {
					count.BodyMissing++
				}
				if archiveImagePending(item) {
					count.ImagePending++
				}
				counts[item.Exchange] = count
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			left, right := noticePublicationMS(rows[i]), noticePublicationMS(rows[j])
			if left != right {
				return left > right
			}
			if rows[i].Exchange != rows[j].Exchange {
				return rows[i].Exchange < rows[j].Exchange
			}
			return rows[i].NoticeID < rows[j].NoticeID
		})
		total := len(rows)
		start := min(int(offset), total)
		snapshot.Items = rows[start:min(start+int(limit), total)]
		snapshot.ContextualItems = nil
		snapshot.MarketEvidence = nil
		snapshot.MarketEvidencePrepared = false
		snapshot.HistoryDays = int(noticeArchiveDays)
		page := noticeArchivePage{Snapshot: snapshot, Total: total, Offset: int(offset), Limit: int(limit), AsOfMS: asOf, FromMS: from, SourceCounts: counts}
		if r.URL.Query().Get("fields") == "headline" {
			page.Snapshot = noticeHeadlineSnapshot{noticeSnapshotFields(snapshot), noticeHeadlines(snapshot.Items)}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, page)
	}
}

func archiveImagePending(item notice.Item) bool {
	for _, imageURL := range item.ImageURLs {
		complete := false
		for _, row := range item.ImageAnalysis {
			if row["url"] != imageURL {
				continue
			}
			switch row["status"] {
			case "ANALYZED", "NO_TEXT", "UNSUPPORTED":
				complete = true
			}
		}
		if !complete {
			return true
		}
	}
	return false
}

func noticePublicationMS(item notice.Item) int64 {
	if item.PublishedTSMS > 0 {
		return item.PublishedTSMS
	}
	return item.DetectedTSMS
}
