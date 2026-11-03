package alignedstage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
)

var ErrRequestLimit = errors.New("official page request budget exhausted")
var ErrRetryWait = errors.New("official page retry cooldown has not elapsed")
var ErrCacheRollover = errors.New("official page cache index is full; preserve this directory and resume the unchanged cursor with a new --receipt-directory")

// CacheRolloverError carries the rejected receipt's durable cooldown even
// though the last readable index cannot admit that receipt.
type CacheRolloverError struct{ RetryUntil time.Time }

func (e *CacheRolloverError) Error() string {
	if e.RetryUntil.IsZero() {
		return ErrCacheRollover.Error()
	}
	return fmt.Sprintf("%s; do not open or run the successor directory before retry_until=%s (UTC); retain the pending receipt", ErrCacheRollover, e.RetryUntil.UTC().Format(time.RFC3339Nano))
}
func (e *CacheRolloverError) Unwrap() error { return ErrCacheRollover }

const cacheAdapter = "exact-official-minute-page/v1"

type CacheReceipt struct {
	Adapter             string            `json:"adapter"`
	ProducerSHA         string            `json:"producer_sha256"`
	Attempt             int               `json:"attempt"`
	Market              domain.MarketPair `json:"market"`
	StartMS             int64             `json:"start_ms"`
	EndMS               int64             `json:"end_ms"`
	FetchedAtMS         int64             `json:"fetched_at_ms"`
	RequestURL          string            `json:"request_url"`
	HTTPStatus          int               `json:"http_status"`
	RawResponseComplete bool              `json:"raw_response_complete"`
	Raw                 []byte            `json:"raw_response"`
	RawSHA              string            `json:"raw_sha256"`
	Error               string            `json:"error,omitempty"`
	RetryAtMS           int64             `json:"retry_at_ms"`
}
type CacheEntry struct {
	File      string `json:"file"`
	SHA       string `json:"sha256"`
	MarketKey string `json:"market_key"`
	StartMS   int64  `json:"start_ms"`
	EndMS     int64  `json:"end_ms"`
	Accepted  bool   `json:"accepted"`
}
type cacheIndex struct {
	Adapter   string       `json:"adapter"`
	Attempts  int          `json:"attempts"`
	RetryAtMS int64        `json:"retry_at_ms"`
	Entries   []CacheEntry `json:"entries"`
}
type PageCache struct {
	dir      string
	producer string
	index    cacheIndex
	unlock   func()
}
type CacheResult struct {
	Candle        *official.MinuteCandle
	FetchedAt     time.Time
	Requests      int
	Hit           bool
	NoTradeProven bool
}

func OpenPageCache(ctx context.Context, dir, producerSHA string) (*PageCache, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) || len(producerSHA) != 64 {
		return nil, errors.New("absolute private cache and producer SHA256 required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("cache must be private")
	}
	release, err := lock(dir)
	if err != nil {
		return nil, err
	}
	c := &PageCache{dir: dir, producer: producerSHA, unlock: release}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	err = readJSON(filepath.Join(dir, "index.json"), &c.index)
	if os.IsNotExist(err) {
		c.index = cacheIndex{Adapter: cacheAdapter, Entries: []CacheEntry{}}
		err = c.save()
	}
	if err != nil {
		return nil, err
	}
	if c.index.Adapter != cacheAdapter || c.index.Attempts != len(c.index.Entries) {
		return nil, errors.New("cache index identity mismatch")
	}
	for i, entry := range c.index.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := c.load(entry)
		if err != nil {
			return nil, err
		}
		if r.Attempt != i+1 || entry.File != fmt.Sprintf("%08d.json", i+1) {
			return nil, errors.New("cache receipt sequence mismatch")
		}
	}
	ok = true
	return c, nil
}
func (c *PageCache) Close() {
	if c.unlock != nil {
		c.unlock()
		c.unlock = nil
	}
}
func (c *PageCache) save() error {
	return c.saveIndex(c.index)
}
func (c *PageCache) saveIndex(next cacheIndex) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	// writeJSON appends one newline. Never replace a readable checkpoint with
	// one that the shared bounded reader cannot reopen.
	if len(data)+1 > evidenceReadLimit {
		rollover := &CacheRolloverError{}
		if next.RetryAtMS > 0 {
			rollover.RetryUntil = time.UnixMilli(next.RetryAtMS)
		}
		return rollover
	}
	if err := writeJSON(filepath.Join(c.dir, "index.json"), next, false); err != nil {
		return err
	}
	c.index = next
	return nil
}
func marketKey(m domain.MarketPair) string { return string(m.Exchange) + "/" + m.Symbol }
func (c *PageCache) load(e CacheEntry) (CacheReceipt, error) {
	var r CacheReceipt
	if filepath.Base(e.File) != e.File || strings.Contains(e.File, "..") {
		return r, errors.New("invalid cache receipt path")
	}
	data, err := readEvidence(filepath.Join(c.dir, "receipts", e.File))
	if err != nil {
		return r, err
	}
	if digest(data) != e.SHA {
		return r, errors.New("cached receipt hash mismatch")
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	if r.Adapter != cacheAdapter || r.RawSHA != digest(r.Raw) || r.StartMS != e.StartMS || r.EndMS != e.EndMS || marketKey(r.Market) != e.MarketKey || e.Accepted != (r.Error == "" && r.RawResponseComplete && r.HTTPStatus == 200) {
		return r, errors.New("cached receipt identity mismatch")
	}
	return r, nil
}
func (c *PageCache) decode(r CacheReceipt, known map[int64]official.MinuteCandle) (Page, error) {
	return DecodePage(r.Market, r.Raw, r.StartMS, r.EndMS, 0, known)
}

func (c *PageCache) admit(ctx context.Context, name string, r CacheReceipt) error {
	if r.Adapter != cacheAdapter || len(r.ProducerSHA) != 64 || r.RawSHA != digest(r.Raw) || r.Attempt != c.index.Attempts+1 || r.StartMS <= 0 || r.StartMS%MinuteMS != 0 || r.EndMS%MinuteMS != 0 || r.EndMS <= r.StartMS || r.EndMS-r.StartMS > 199*MinuteMS {
		return errors.New("invalid pending cache receipt")
	}
	if r.Error == "" {
		if !r.RawResponseComplete || r.HTTPStatus != 200 {
			return errors.New("bounded error body cannot prove candles")
		}
		known := map[int64]official.MinuteCandle{}
		oldPages := []Page{}
		for _, entry := range c.index.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.Accepted || entry.MarketKey != marketKey(r.Market) {
				continue
			}
			old, err := c.load(entry)
			if err != nil {
				return err
			}
			p, err := c.decode(old, nil)
			if err != nil {
				return err
			}
			oldPages = append(oldPages, p)
			for at, candle := range p.Candles {
				known[at] = candle
			}
		}
		p, err := c.decode(r, known)
		if err != nil {
			return err
		}
		// A previously proven Upbit no-trade omission is also immutable evidence.
		if r.Market.Exchange == domain.ExchangeUpbit {
			for _, old := range oldPages {
				missing := map[int64]bool{}
				for _, at := range old.Missing {
					missing[at] = true
				}
				for at := range p.Candles {
					if at >= old.StartMS && at < old.EndMS && !missing[at] {
						if _, exists := old.Candles[at]; !exists {
							return errors.New("official candle conflicts with proven no-trade omission")
						}
					}
				}
			}
		}
	}
	data, err := readEvidence(filepath.Join(c.dir, "receipts", name))
	if err != nil {
		return err
	}
	next := c.index
	next.Entries = append(append([]CacheEntry(nil), c.index.Entries...), CacheEntry{name, digest(data), marketKey(r.Market), r.StartMS, r.EndMS, r.Error == "" && r.RawResponseComplete})
	next.Attempts = r.Attempt
	if r.Error != "" {
		next.RetryAtMS = max(c.index.RetryAtMS, r.RetryAtMS)
	}
	return c.saveIndex(next)
}

// Get is source-only caching. Destination gap/registry admission still happens
// independently in the repository transaction for every selected minute.
func (c *PageCache) Get(ctx context.Context, m domain.MarketPair, minute, from, to int64, remaining int, fetcher Fetcher, now func() time.Time) (CacheResult, error) {
	result := CacheResult{}
	if minute < from || minute+MinuteMS > to || from%MinuteMS != 0 || to%MinuteMS != 0 || minute%MinuteMS != 0 || to <= from || to-from > 48*HourMS {
		return result, errors.New("invalid cache candidate window")
	}
	if m.Quote != "KRW" || m.Scope != domain.ScopeDomestic || m.Base == "" || (m.Exchange != domain.ExchangeBithumb && m.Exchange != domain.ExchangeUpbit && m.Exchange != domain.ExchangeCoinone) {
		return result, errors.New("unsupported cache market")
	}
	selectCandle := func(r CacheReceipt, hit bool) (CacheResult, error) {
		if r.Market != m {
			return result, errors.New("cached market identity mismatch")
		}
		p, err := c.decode(r, nil)
		if err != nil {
			return result, err
		}
		result.Hit = hit
		result.FetchedAt = time.UnixMilli(r.FetchedAtMS)
		if candle, exists := p.Candles[minute]; exists {
			copy := candle
			result.Candle = &copy
		} else if m.Exchange == domain.ExchangeUpbit {
			result.NoTradeProven = true
			for _, at := range p.Missing {
				if at == minute {
					result.NoTradeProven = false
				}
			}
		}
		return result, nil
	}
	for i := len(c.index.Entries) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		entry := c.index.Entries[i]
		if entry.Accepted && entry.MarketKey == marketKey(m) && entry.StartMS <= minute && minute < entry.EndMS {
			r, err := c.load(entry)
			if err != nil {
				return result, err
			}
			return selectCandle(r, true)
		}
	}
	if c.index.RetryAtMS > now().UnixMilli() {
		return result, ErrRetryWait
	}
	start := from + ((minute-from)/(199*MinuteMS))*(199*MinuteMS)
	end := min(to, start+199*MinuteMS)
	name := fmt.Sprintf("%08d.json", c.index.Attempts+1)
	var receipt CacheReceipt
	err := readJSON(filepath.Join(c.dir, "receipts", name), &receipt)
	if os.IsNotExist(err) {
		if remaining < 1 {
			return result, ErrRequestLimit
		}
		fetched, fetchErr := fetcher.Fetch(ctx, m, start, end)
		result.Requests = 1
		receipt = CacheReceipt{Adapter: cacheAdapter, ProducerSHA: c.producer, Attempt: c.index.Attempts + 1, Market: m, StartMS: start, EndMS: end, FetchedAtMS: now().UnixMilli(), RequestURL: fetched.RequestURL, HTTPStatus: fetched.HTTPStatus, RawResponseComplete: fetchErr == nil, Raw: fetched.Raw, RawSHA: digest(fetched.Raw)}
		if fetchErr != nil {
			receipt.Error = fetchErr.Error()
			receipt.RetryAtMS = max(now().Add(time.Minute).UnixMilli(), fetched.RetryAt.UnixMilli())
		}
		if err := writeJSON(filepath.Join(c.dir, "receipts", name), receipt, true); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	}
	if err := c.admit(ctx, name, receipt); err != nil {
		return result, err
	}
	if receipt.Error != "" {
		return result, errors.New(receipt.Error)
	}
	if marketKey(receipt.Market) != marketKey(m) || receipt.StartMS > minute || receipt.EndMS <= minute {
		return result, errors.New("recovered orphan receipt; retry the unchanged candidate")
	}
	return selectCandle(receipt, result.Requests == 0)
}
