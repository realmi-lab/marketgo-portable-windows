package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"sync"

	marketanalysis "market-share-go/internal/analysis"
)

const (
	analysisResponseCacheBytes   = 32 << 20
	analysisResponseCacheEntries = 16
)

type analysisResponseEncoding struct {
	plain []byte
	gzip  []byte
}

type analysisResponseEntry struct {
	plain    []byte
	gzipOnce sync.Once
	gzip     []byte
}

type analysisResponseCacheItem struct {
	entry *analysisResponseEntry
	size  int
}

type analysisResponseCall struct {
	done  chan struct{}
	entry *analysisResponseEntry
	err   error
}

// analysisResponseCache reuses the expensive JSON and gzip representation of
// immutable analysis results. The context ID is a digest of the complete
// payload, so an ID hit cannot mix calculations from different input states.
// Both entry count and retained bytes are bounded.
type analysisResponseCache struct {
	mu         sync.Mutex
	entries    map[string]analysisResponseCacheItem
	inFlight   map[string]*analysisResponseCall
	order      []string
	bytes      int
	maxBytes   int
	maxEntries int
}

func newAnalysisResponseCache() *analysisResponseCache {
	return &analysisResponseCache{
		entries:    make(map[string]analysisResponseCacheItem),
		inFlight:   make(map[string]*analysisResponseCall),
		maxBytes:   analysisResponseCacheBytes,
		maxEntries: analysisResponseCacheEntries,
	}
}

func (c *analysisResponseCache) encode(value marketanalysis.Analysis, wantGzip bool) (analysisResponseEncoding, error) {
	key := analysisResponseCacheKey(value)
	entry, err := c.plainEntry(key, value)
	if err != nil {
		return analysisResponseEncoding{}, err
	}
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

func (c *analysisResponseCache) plainEntry(key string, value marketanalysis.Analysis) (*analysisResponseEntry, error) {
	if key == "" {
		return encodeAnalysisResponsePlain(value)
	}

	c.mu.Lock()
	if cached, found := c.entries[key]; found {
		c.touch(key)
		c.mu.Unlock()
		return cached.entry, nil
	}
	if call, found := c.inFlight[key]; found {
		c.mu.Unlock()
		<-call.done
		return call.entry, call.err
	}
	call := &analysisResponseCall{done: make(chan struct{})}
	c.inFlight[key] = call
	c.mu.Unlock()

	call.entry, call.err = encodeAnalysisResponsePlain(value)

	c.mu.Lock()
	delete(c.inFlight, key)
	if call.err == nil {
		c.store(key, call.entry)
	}
	close(call.done)
	c.mu.Unlock()
	return call.entry, call.err
}

func analysisResponseCacheKey(value marketanalysis.Analysis) string {
	identity := value.Context.AnalysisContextID
	if len(identity) != 35 || !strings.HasPrefix(identity, "ms-") {
		return ""
	}
	for _, value := range identity[3:] {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return ""
		}
	}
	return identity
}

func encodeAnalysisResponsePlain(value marketanalysis.Analysis) (*analysisResponseEntry, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &analysisResponseEntry{plain: plain}, nil
}

var responseGzipWriters = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(io.Discard, 5)
	return writer
}}

func gzipPayload(payload []byte) ([]byte, error) {
	var compressed bytes.Buffer
	encoder := responseGzipWriters.Get().(*gzip.Writer)
	encoder.Reset(&compressed)
	defer func() {
		// Do not let an idle writer retain a request's response buffer.
		encoder.Reset(io.Discard)
		responseGzipWriters.Put(encoder)
	}()
	var err error
	if _, err = encoder.Write(payload); err == nil {
		err = encoder.Close()
	} else {
		_ = encoder.Close()
	}
	if err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func (c *analysisResponseCache) store(key string, entry *analysisResponseEntry) {
	size := len(entry.plain)
	if size > c.maxBytes || c.maxEntries < 1 {
		return
	}
	for len(c.order) >= c.maxEntries || c.bytes+size > c.maxBytes {
		c.evictOldest()
	}
	c.entries[key] = analysisResponseCacheItem{entry: entry, size: size}
	c.order = append(c.order, key)
	c.bytes += size
}

func (c *analysisResponseCache) accountGzip(key string, entry *analysisResponseEntry, size int) {
	if key == "" || size == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, found := c.entries[key]
	if !found || item.entry != entry {
		return
	}
	item.size += size
	c.entries[key] = item
	c.bytes += size
	for c.bytes > c.maxBytes && len(c.order) > 0 {
		c.evictOldest()
	}
}

func (c *analysisResponseCache) evictOldest() {
	oldest := c.order[0]
	c.order = c.order[1:]
	c.bytes -= c.entries[oldest].size
	delete(c.entries, oldest)
}

func (c *analysisResponseCache) touch(key string) {
	for index, candidate := range c.order {
		if candidate != key {
			continue
		}
		copy(c.order[index:], c.order[index+1:])
		c.order[len(c.order)-1] = key
		return
	}
}
