package hermesrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/notice"
)

type BackendClient struct {
	BaseURL        string
	Token          string
	TransportToken string
	HTTP           *http.Client
}

const maxBackendResponseBytes int64 = 32 << 20

func (c BackendClient) LoadExisting(ctx context.Context) (notice.Snapshot, error) {
	var result notice.Snapshot
	if err := c.do(ctx, http.MethodGet, nil, &result); err != nil {
		return notice.Snapshot{}, err
	}
	return result, nil
}

// LoadCorpus returns only a fully read, revision-consistent bounded archive.
// Any page error discards the partial snapshot so callers cannot treat omitted
// old rows as new. Retrying is an explicit new read, never an implicit merge.
func (c BackendClient) LoadCorpus(ctx context.Context, bounds notice.HermesCorpusRange) (notice.Snapshot, error) {
	if err := bounds.Validate(time.Now()); err != nil {
		return notice.Snapshot{}, err
	}
	query := notice.ArchiveQuery{FromTSMS: bounds.FromTSMS, ToTSMS: bounds.ToTSMS + 1, Limit: notice.ArchivePageLimit}
	if _, err := query.Validate(); err != nil {
		return notice.Snapshot{}, err
	}
	result := notice.Snapshot{ItemRevisions: map[string]string{}}
	revision := ""
	seenCursor := map[string]bool{}
	seenItem := map[string]bool{}
	for pageIndex := 0; pageIndex < 50; pageIndex++ {
		params := url.Values{"from_ts_ms": {strconv.FormatInt(query.FromTSMS, 10)}, "to_ts_ms": {strconv.FormatInt(query.ToTSMS, 10)}, "limit": {strconv.Itoa(query.Limit)}}
		if query.Cursor != "" {
			params.Set("cursor", query.Cursor)
		}
		var page notice.ArchivePage
		if err := c.doQuery(ctx, http.MethodGet, params, nil, &page); err != nil {
			return notice.Snapshot{}, err
		}
		if page.FromTSMS != query.FromTSMS || page.ToTSMS != query.ToTSMS || page.Revision == "" || len(page.Items) > query.Limit || len(page.ItemRevisions) != len(page.Items) || (revision != "" && revision != page.Revision) {
			return notice.Snapshot{}, errors.New("corpus archive range/revision changed")
		}
		revision = page.Revision
		for _, item := range page.Items {
			key := noticeKey(item.Exchange, item.NoticeID)
			if !notice.IsDomesticExchange(item.Exchange) || item.NoticeID == "" || item.PublishedTSMS < query.FromTSMS || item.PublishedTSMS >= query.ToTSMS || seenItem[key] {
				return notice.Snapshot{}, errors.New("corpus archive item identity/range mismatch")
			}
			seenItem[key] = true
			revisionKey := item.Exchange + ":" + item.NoticeID
			token := page.ItemRevisions[revisionKey]
			value, err := strconv.ParseUint(token, 10, 32)
			if err != nil || value == 0 || strconv.FormatUint(value, 10) != token {
				return notice.Snapshot{}, errors.New("corpus item revision missing or invalid")
			}
			result.ItemRevisions[revisionKey] = token
			result.Items = append(result.Items, item)
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				return notice.Snapshot{}, errors.New("corpus terminal page has unexpected cursor")
			}
			return result, nil
		}
		if len(page.Items) == 0 || page.NextCursor == "" || seenCursor[page.NextCursor] {
			return notice.Snapshot{}, errors.New("corpus archive cursor failed to advance")
		}
		seenCursor[page.NextCursor] = true
		query.Cursor = page.NextCursor
	}
	return notice.Snapshot{}, errors.New("corpus archive exceeds 10000-item bound; narrow requested interval")
}

func (c BackendClient) Submit(ctx context.Context, request notice.HermesIngestRequest) (notice.HermesIngestResult, error) {
	var result notice.HermesIngestResult
	if err := c.do(ctx, http.MethodPost, request, &result); err != nil {
		return notice.HermesIngestResult{}, err
	}
	return result, nil
}

func (c BackendClient) do(ctx context.Context, method string, input, output any) error {
	return c.doQuery(ctx, method, nil, input, output)
}

func (c BackendClient) doQuery(ctx context.Context, method string, query url.Values, input, output any) error {
	base, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return errors.New("Hermes backend URL must be an absolute HTTP URL without embedded credentials")
	}
	base.Path = "/internal/hermes/notices"
	base.RawQuery, base.Fragment = query.Encode(), ""
	var body io.Reader
	if input != nil {
		payload, encodeErr := json.Marshal(input)
		if encodeErr != nil {
			return encodeErr
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
	if token := strings.TrimSpace(c.TransportToken); token != "" {
		request.Header.Set("X-Marketgo-Hermes-Proxy", token)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return notice.ErrHermesIngestConflict
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4_000))
		return fmt.Errorf("Hermes backend %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	limited := &io.LimitedReader{R: response.Body, N: maxBackendResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(output); err != nil {
		if limited.N == 0 {
			return errors.New("Hermes backend response exceeded the size limit")
		}
		return fmt.Errorf("decode Hermes backend response: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		if limited.N == 0 {
			return errors.New("Hermes backend response exceeded the size limit")
		}
		return fmt.Errorf("decode Hermes backend response: %w", err)
	}
	if limited.N == 0 {
		return errors.New("Hermes backend response exceeded the size limit")
	}
	return nil
}
