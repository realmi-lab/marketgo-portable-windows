// Package orderbookfeed transports replaceable latest books independently of DB writes.
package orderbookfeed

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
)

const Path = "/internal/order-books"
const maxBody = 16 << 20

type Cursor struct {
	Epoch    string `json:"epoch"`
	Revision uint64 `json:"revision"`
}
type Frame struct {
	Version int                           `json:"version"`
	Cursor  Cursor                        `json:"cursor"`
	Books   []domain.OrderBookTopSnapshot `json:"books"`
	// BookRevisions is positional with Books. It is additive in version 1 so
	// old readers ignore it and new readers can conservatively accept old frames.
	BookRevisions []uint64 `json:"book_revisions,omitempty"`
}
type entry struct {
	revision uint64
	book     domain.OrderBookTopSnapshot
}
type Feed struct {
	mu     sync.Mutex
	cursor Cursor
	latest map[string]entry
}

func New() *Feed {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return &Feed{cursor: Cursor{Epoch: hex.EncodeToString(id[:])}, latest: make(map[string]entry)}
}
func copyBook(dst, src domain.OrderBookTopSnapshot) domain.OrderBookTopSnapshot {
	bids, asks, sequence := dst.Bids, dst.Asks, dst.Sequence
	dst = src
	dst.Bids = copyLevels(bids, src.Bids)
	dst.Asks = copyLevels(asks, src.Asks)
	if src.Sequence != nil {
		if sequence == nil {
			sequence = new(int64)
		}
		*sequence = *src.Sequence
		dst.Sequence = sequence
	}
	return dst
}

// Publish copies into exclusive reusable buffers; neither DB batches nor HTTP readers share them.
func (f *Feed) Publish(book domain.OrderBookTopSnapshot) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursor.Revision++
	old := f.latest[book.Key()]
	f.latest[book.Key()] = entry{f.cursor.Revision, copyBook(old.book, book)}
}
func (f *Feed) Snapshot(after Cursor) Frame {
	f.mu.Lock()
	defer f.mu.Unlock()
	full := after.Epoch != f.cursor.Epoch || after.Revision > f.cursor.Revision
	frame := Frame{Version: 1, Cursor: f.cursor, Books: make([]domain.OrderBookTopSnapshot, 0), BookRevisions: make([]uint64, 0)}
	for _, e := range f.latest {
		if full || e.revision > after.Revision {
			frame.Books = append(frame.Books, copyBook(domain.OrderBookTopSnapshot{}, e.book))
			frame.BookRevisions = append(frame.BookRevisions, e.revision)
		}
	}
	return frame
}

// Handler is disabled without a token, allowing old collector configurations to remain valid.
func (f *Feed) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if f == nil || token == "" {
			http.NotFound(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		revision, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if err != nil && r.URL.Query().Get("after") != "" {
			http.Error(w, "invalid cursor", 400)
			return
		}
		frame := f.Snapshot(Cursor{r.URL.Query().Get("epoch"), revision})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(frame)
	})
}

type Client struct {
	origin, token string
	http          *http.Client
	// Retain at most one ordinary response buffer. Large startup snapshots
	// are released, and concurrent readers never share an in-use buffer.
	buffers chan []byte
}

func NewClient(origin, token string) *Client {
	return &Client{origin: strings.TrimRight(origin, "/"), token: token, buffers: make(chan []byte, 1), http: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Read(ctx context.Context, after Cursor) (Frame, error) {
	var frame Frame
	q := url.Values{"epoch": {after.Epoch}, "after": {strconv.FormatUint(after.Revision, 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+Path+"?"+q.Encode(), nil)
	if err != nil {
		return frame, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return frame, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return frame, fmt.Errorf("order-book feed HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBody {
		return frame, errors.New("order-book feed too large")
	}
	body, err := readBody(io.LimitReader(resp.Body, maxBody+1), c.takeBuffer(), resp.ContentLength)
	defer c.releaseBuffer(body)
	if err != nil {
		return frame, err
	}
	if len(body) > maxBody {
		return frame, errors.New("order-book feed too large")
	}
	if err := json.Unmarshal(body, &frame); err != nil {
		return frame, err
	}
	sameEpoch := frame.Cursor.Epoch == after.Epoch
	if frame.Version != 1 || frame.Cursor.Epoch == "" || (sameEpoch && frame.Cursor.Revision < after.Revision) ||
		(sameEpoch && frame.Cursor.Revision == after.Revision && len(frame.Books) != 0) {
		return frame, errors.New("invalid order-book feed cursor/version")
	}
	if len(frame.BookRevisions) != 0 && len(frame.BookRevisions) != len(frame.Books) {
		return frame, errors.New("invalid order-book feed book revisions")
	}
	seen := make(map[string]bool, len(frame.Books))
	for index, book := range frame.Books {
		if err := book.Validate(); err != nil {
			return frame, err
		}
		key := book.Key()
		if seen[key] {
			return frame, errors.New("duplicate order-book feed market")
		}
		seen[key] = true
		if len(frame.BookRevisions) != 0 {
			revision := frame.BookRevisions[index]
			if revision == 0 || revision > frame.Cursor.Revision || (sameEpoch && revision <= after.Revision) {
				return frame, errors.New("invalid order-book feed book revision")
			}
		}
	}
	return frame, nil
}

const maxRetainedBody = 1 << 20

func (c *Client) takeBuffer() []byte {
	select {
	case buffer := <-c.buffers:
		return buffer
	default:
		return nil
	}
}

func (c *Client) releaseBuffer(buffer []byte) {
	if cap(buffer) > maxRetainedBody {
		return
	}
	select {
	case c.buffers <- buffer[:0]:
	default:
	}
}

// Reuse a buffer only while it fits. Delegate growth to io.ReadAll rather than
// duplicating its allocation strategy; the caller separately bounds the reader.
func readBody(r io.Reader, body []byte, length int64) ([]byte, error) {
	if cap(body) == 0 || length > int64(cap(body)) {
		return io.ReadAll(r)
	}
	for len(body) < cap(body) {
		n, err := r.Read(body[len(body):cap(body)])
		body = body[:len(body)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return body, err
		}
	}
	var next [1]byte
	for {
		n, err := r.Read(next[:])
		if err != nil {
			body = append(body, next[:n]...)
			if err == io.EOF {
				err = nil
			}
			return body, err
		}
		if n > 0 {
			return io.ReadAll(io.MultiReader(bytes.NewReader(body), bytes.NewReader(next[:n]), r))
		}
	}
}

func copyLevels(dst, src []domain.OrderBookLevel) []domain.OrderBookLevel {
	if cap(dst) < len(src) {
		dst = make([]domain.OrderBookLevel, len(src))
	} else {
		dst = dst[:len(src)]
	}
	for i, level := range src {
		count, update := dst[i].OrderCount, dst[i].UpdateID
		dst[i] = level
		if level.OrderCount != nil {
			if count == nil {
				count = new(int64)
			}
			*count = *level.OrderCount
			dst[i].OrderCount = count
		}
		if level.UpdateID != nil {
			if update == nil {
				update = new(int64)
			}
			*update = *level.UpdateID
			dst[i].UpdateID = update
		}
	}
	return dst
}
