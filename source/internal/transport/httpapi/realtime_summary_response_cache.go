package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const (
	realtimeSummaryResponseCacheTTL     = 5 * time.Second
	realtimeSummaryResponseCacheBytes   = 24 << 20
	realtimeSummaryResponseCacheEntries = 8
)

type realtimeSummaryResponseKey [sha256.Size]byte

type realtimeSummaryResponseEntry struct {
	plain     []byte
	gzipOnce  sync.Once
	gzip      []byte
	expiresAt time.Time
}

type realtimeSummaryResponseItem struct {
	entry *realtimeSummaryResponseEntry
	size  int
}

type realtimeSummaryResponseCache struct {
	mu      sync.Mutex
	entries map[realtimeSummaryResponseKey]realtimeSummaryResponseItem
	order   []realtimeSummaryResponseKey
	bytes   int
}

func newRealtimeSummaryResponseCache() *realtimeSummaryResponseCache {
	return &realtimeSummaryResponseCache{
		entries: make(map[realtimeSummaryResponseKey]realtimeSummaryResponseItem),
	}
}

func realtimeSummaryResponseCacheKey(plain []byte) realtimeSummaryResponseKey {
	return sha256.Sum256(plain)
}

func (c *realtimeSummaryResponseCache) encode(
	value marketanalysis.RealtimeFlowPeriodSummary,
	wantGzip bool,
) (analysisResponseEncoding, error) {
	// Summary counts are not an immutable identity: corrections can change
	// drivers, coverage or status without changing counts. Hash the exact wire
	// representation, and reuse those bytes instead of marshaling a second time.
	plain, err := json.Marshal(value)
	if err != nil {
		return analysisResponseEncoding{}, err
	}
	key := realtimeSummaryResponseCacheKey(plain)
	entry := c.plainEntry(key, plain)
	result := analysisResponseEncoding{plain: entry.plain}
	if !wantGzip || len(entry.plain) < 1024 {
		return result, nil
	}
	entry.gzipOnce.Do(func() {
		compressed, gzipErr := gzipPayload(entry.plain)
		if gzipErr != nil {
			return
		}
		entry.gzip = compressed
		c.accountGzip(key, entry, len(compressed))
	})
	result.gzip = entry.gzip
	return result, nil
}

func (c *realtimeSummaryResponseCache) plainEntry(
	key realtimeSummaryResponseKey,
	plain []byte,
) *realtimeSummaryResponseEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.entries[key]; ok {
		if time.Now().Before(cached.entry.expiresAt) {
			c.touchLocked(key)
			return cached.entry
		}
		c.removeLocked(key)
	}
	entry := &realtimeSummaryResponseEntry{
		plain: plain, expiresAt: time.Now().Add(realtimeSummaryResponseCacheTTL),
	}
	c.storeLocked(key, entry)
	return entry
}

func (c *realtimeSummaryResponseCache) storeLocked(
	key realtimeSummaryResponseKey,
	entry *realtimeSummaryResponseEntry,
) {
	size := len(entry.plain)
	if size > realtimeSummaryResponseCacheBytes {
		return
	}
	c.removeLocked(key)
	for len(c.order) >= realtimeSummaryResponseCacheEntries ||
		c.bytes+size > realtimeSummaryResponseCacheBytes {
		c.evictOldestLocked()
	}
	c.entries[key] = realtimeSummaryResponseItem{entry: entry, size: size}
	c.order = append(c.order, key)
	c.bytes += size
}

func (c *realtimeSummaryResponseCache) accountGzip(
	key realtimeSummaryResponseKey,
	entry *realtimeSummaryResponseEntry,
	size int,
) {
	if size == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[key]
	if !ok || item.entry != entry {
		return
	}
	item.size += size
	c.entries[key] = item
	c.bytes += size
	for c.bytes > realtimeSummaryResponseCacheBytes && len(c.order) > 0 {
		c.evictOldestLocked()
	}
}

func (c *realtimeSummaryResponseCache) touchLocked(key realtimeSummaryResponseKey) {
	for index, candidate := range c.order {
		if candidate != key {
			continue
		}
		copy(c.order[index:], c.order[index+1:])
		c.order[len(c.order)-1] = key
		return
	}
}

func (c *realtimeSummaryResponseCache) removeLocked(key realtimeSummaryResponseKey) {
	item, ok := c.entries[key]
	if ok {
		c.bytes -= item.size
		delete(c.entries, key)
	}
	for index, candidate := range c.order {
		if candidate != key {
			continue
		}
		copy(c.order[index:], c.order[index+1:])
		c.order = c.order[:len(c.order)-1]
		return
	}
}

func (c *realtimeSummaryResponseCache) evictOldestLocked() {
	if len(c.order) == 0 {
		return
	}
	c.removeLocked(c.order[0])
}

func writeRealtimeSummaryRequestJSON(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	value marketanalysis.RealtimeFlowPeriodSummary,
	cache *realtimeSummaryResponseCache,
) {
	if cache == nil {
		writeRequestJSON(writer, request, status, value)
		return
	}
	wantGzip := acceptsGzip(request)
	encoding, err := cache.encode(value, wantGzip)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"detail": "response serialization failed"})
		return
	}
	payload := encoding.plain
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Add("Vary", "Accept-Encoding")
	if wantGzip && len(encoding.gzip) > 0 {
		writer.Header().Set("Content-Encoding", "gzip")
		payload = encoding.gzip
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}
