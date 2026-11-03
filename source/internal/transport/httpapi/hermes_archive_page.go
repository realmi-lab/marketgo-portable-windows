package httpapi

import (
	"context"
	"errors"
	"market-share-go/internal/notice"
	"net/http"
	"net/url"
	"strconv"
)

type HermesNoticeArchiveProvider interface {
	HermesNoticeArchivePage(context.Context, string, notice.ArchiveQuery) (notice.ArchivePage, error)
}

func serveHermesNoticeArchivePage(w http.ResponseWriter, r *http.Request, token string, provider any) {
	values, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid archive query encoding"})
		return
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "from_ts_ms" && key != "to_ts_ms" && key != "limit" && key != "cursor") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid archive query"})
			return
		}
	}
	from, e1 := strconv.ParseInt(values.Get("from_ts_ms"), 10, 64)
	to, e2 := strconv.ParseInt(values.Get("to_ts_ms"), 10, 64)
	limit := notice.ArchivePageLimit
	var e3 error
	if values.Has("limit") {
		limit, e3 = strconv.Atoi(values.Get("limit"))
	}
	query := notice.ArchiveQuery{FromTSMS: from, ToTSMS: to, Limit: limit, Cursor: values.Get("cursor")}
	_, err := query.Validate()
	if e1 != nil || e2 != nil || e3 != nil || err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid archive range or cursor"})
		return
	}
	p, ok := provider.(HermesNoticeArchiveProvider)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "notice archive range reader unavailable"})
		return
	}
	page, err := p.HermesNoticeArchivePage(r.Context(), token, query)
	switch {
	case errors.Is(err, notice.ErrHermesIngestUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": err.Error()})
	case errors.Is(err, notice.ErrHermesIngestInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "notice archive read failed"})
	default:
		writeJSON(w, http.StatusOK, page)
	}
}
