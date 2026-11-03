package alignedstage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
	"market-share-go/services/collector/restbudget"
)

// HTTPFetcher has one caller per locked stage. Retain Retry-After outside the
// in-process budget, so starting another bounded invocation cannot bypass it.
type HTTPFetcher struct {
	client  *restbudget.Client
	capture *captureTransport
}
type captureTransport struct {
	status int
	header http.Header
	raw    bytes.Buffer
}
type capturedBody struct {
	io.Reader
	io.Closer
}

func (t *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	t.status = response.StatusCode
	t.header = response.Header.Clone()
	response.Body = capturedBody{io.TeeReader(response.Body, &t.raw), response.Body}
	return response, nil
}
func NewHTTPFetcher(manager *restbudget.Manager) *HTTPFetcher {
	t := &captureTransport{}
	return &HTTPFetcher{client: &restbudget.Client{HTTP: &http.Client{Timeout: 8 * time.Second, Transport: t}, Budgets: manager, Attempts: 1}, capture: t}
}
func (f *HTTPFetcher) Fetch(ctx context.Context, market domain.MarketPair, start, end int64) (FetchResult, error) {
	request, err := official.BuildCandleRequest(market.Exchange, market.Symbol, end+1)
	if err != nil {
		return FetchResult{}, err
	}
	count := (end-start)/MinuteMS + 1
	if market.Exchange == domain.ExchangeCoinone {
		request.Params["size"] = count
	} else {
		request.Params["count"] = count
	}
	query := url.Values{}
	for key, value := range request.Params {
		query.Set(key, fmt.Sprint(value))
	}
	group := "public"
	if market.Exchange == domain.ExchangeUpbit {
		group = "candle"
	}
	f.capture.status = 0
	f.capture.header = nil
	f.capture.raw.Reset()
	raw, err := f.client.GetBytes(ctx, restbudget.Request{Exchange: string(market.Exchange), Group: group, URL: request.URL, Query: query, Priority: restbudget.PriorityGapRepair, Cost: 1}, 2<<20)
	if err != nil {
		raw = append([]byte{}, f.capture.raw.Bytes()...)
	}
	return FetchResult{Raw: raw, HTTPStatus: f.capture.status, RequestURL: request.URL + "?" + query.Encode(), RetryAt: retryAfter(f.capture.header.Get("Retry-After"), time.Now())}, err
}
func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		seconds = min(seconds, int64((1<<63-1)/int64(time.Second)))
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at
	}
	return time.Time{}
}
