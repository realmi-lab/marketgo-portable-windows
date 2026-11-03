package notice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const ArchivePageLimit = 200

// ArchiveQuery selects publication timestamps in [FromTSMS, ToTSMS).
// The cursor binds the range and uses publication/identity, never mutable revision time.
type ArchiveQuery struct {
	FromTSMS int64  `json:"from_ts_ms"`
	ToTSMS   int64  `json:"to_ts_ms"`
	Limit    int    `json:"limit"`
	Cursor   string `json:"cursor,omitempty"`
}

type ArchiveCursor struct {
	FromTSMS      int64  `json:"from"`
	ToTSMS        int64  `json:"to"`
	PublishedTSMS int64  `json:"published"`
	Exchange      string `json:"exchange"`
	NoticeID      string `json:"id"`
	Revision      string `json:"revision"`
}

type ArchivePage struct {
	ItemRevisions map[string]string `json:"item_revisions"`
	Items         []Item            `json:"items"`
	FromTSMS      int64             `json:"from_ts_ms"`
	ToTSMS        int64             `json:"to_ts_ms"`
	HasMore       bool              `json:"has_more"`
	NextCursor    string            `json:"next_cursor,omitempty"`
	Revision      string            `json:"revision"`
}

type ArchiveStore interface {
	NoticeArchivePage(context.Context, ArchiveQuery) (ArchivePage, error)
}

func (q ArchiveQuery) Validate() (ArchiveCursor, error) {
	invalid := func() (ArchiveCursor, error) {
		return ArchiveCursor{}, fmt.Errorf("%w: invalid notice archive range or cursor", ErrHermesIngestInvalid)
	}
	if q.FromTSMS <= 0 || q.ToTSMS <= q.FromTSMS || q.ToTSMS-q.FromTSMS > int64(366*24*time.Hour/time.Millisecond) || q.Limit < 1 || q.Limit > ArchivePageLimit || len(q.Cursor) > 2048 {
		return invalid()
	}
	if q.Cursor == "" {
		return ArchiveCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	if err != nil {
		return invalid()
	}
	var c ArchiveCursor
	if json.Unmarshal(raw, &c) != nil || c.FromTSMS != q.FromTSMS || c.ToTSMS != q.ToTSMS || c.PublishedTSMS < q.FromTSMS || c.PublishedTSMS >= q.ToTSMS || !IsDomesticExchange(c.Exchange) || c.NoticeID == "" || len(c.Revision) != 32 {
		return invalid()
	}
	return c, nil
}

func (q ArchiveQuery) CursorAfter(item Item, revision string) string {
	raw, _ := json.Marshal(ArchiveCursor{q.FromTSMS, q.ToTSMS, item.PublishedTSMS, item.Exchange, item.NoticeID, revision})
	return base64.RawURLEncoding.EncodeToString(raw)
}
