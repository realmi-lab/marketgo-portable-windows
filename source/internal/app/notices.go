package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/internal/notice"
)

const noticeSnapshotCacheTTL = 5 * time.Second

type noticeSnapshotCacheEntry struct {
	created              time.Time
	generation           uint64
	value                notice.Snapshot
	contentIdentity      [sha256.Size]byte
	contentIdentityReady bool
}

// Official timestamps do not identify later body, analysis or OCR writes.
// Stream one item at a time, outside cache locks, without copying the archive.
func noticeSnapshotContentIdentity(items []notice.Item) ([sha256.Size]byte, bool) {
	hasher := sha256.New()
	encoder := json.NewEncoder(hasher)
	for i := range items {
		if err := encoder.Encode(&items[i]); err != nil {
			return [sha256.Size]byte{}, false
		}
	}
	var identity [sha256.Size]byte
	copy(identity[:], hasher.Sum(nil))
	return identity, true
}

func (m *noticeSnapshotMemo) contentIdentity(snapshot notice.Snapshot) ([sha256.Size]byte, bool) {
	m.mu.Lock()
	entry := m.cache
	// A published snapshot is immutable. Its backing array identifies the exact
	// input already hashed during loading; later RAG requests need no rescan.
	if entry != nil && entry.contentIdentityReady && entry.value.Version == snapshot.Version &&
		len(entry.value.Items) == len(snapshot.Items) &&
		(len(snapshot.Items) == 0 || &entry.value.Items[0] == &snapshot.Items[0]) {
		identity := entry.contentIdentity
		m.mu.Unlock()
		return identity, true
	}
	m.mu.Unlock()
	return noticeSnapshotContentIdentity(snapshot.Items)
}

type noticeSnapshotCall struct {
	done       chan struct{}
	generation uint64
	value      notice.Snapshot
	err        error
}

type noticeSnapshotMemo struct {
	mu         sync.Mutex
	generation uint64
	cache      *noticeSnapshotCacheEntry
	inflight   *noticeSnapshotCall
}

func (m *noticeSnapshotMemo) invalidate() {
	m.mu.Lock()
	m.generation++
	m.cache = nil
	m.mu.Unlock()
}

func (s *Service) NoticeSnapshot(ctx context.Context) (notice.Snapshot, error) {
	s.noticeMemoOnce.Do(func() { s.noticeMemo = &noticeSnapshotMemo{} })
	return s.noticeMemo.get(ctx, func(queryCtx context.Context) (notice.Snapshot, error) {
		return s.loadNoticeSnapshot(queryCtx)
	})
}

// noticeSnapshotVersioned returns the local invalidation generation together
// with the snapshot. Notice-question answer caches use the generation to skip
// a repeated PostgreSQL read without surviving a Hermes ingest.
func (s *Service) noticeSnapshotVersioned(ctx context.Context) (notice.Snapshot, uint64, error) {
	s.noticeMemoOnce.Do(func() { s.noticeMemo = &noticeSnapshotMemo{} })
	return s.noticeMemo.getVersioned(ctx, func(queryCtx context.Context) (notice.Snapshot, error) {
		return s.loadNoticeSnapshot(queryCtx)
	})
}

func (s *Service) HermesNoticeSnapshot(ctx context.Context, token string) (notice.Snapshot, error) {
	if !s.hermesNoticeAuthorized(token) {
		return notice.Snapshot{}, notice.ErrHermesIngestUnauthorized
	}
	return s.NoticeSnapshot(ctx)
}

func (s *Service) loadNoticeSnapshot(ctx context.Context) (notice.Snapshot, error) {
	since := time.Now().AddDate(0, 0, -notice.HistoryDays)
	snapshot, err := s.repository.NoticeSnapshot(ctx, since)
	if err != nil {
		return notice.Snapshot{}, err
	}
	snapshot = notice.ActiveSourceSnapshot(snapshot)
	if s.noticePipelineV2Enabled {
		if store, ok := s.repository.(notice.PipelineStore); ok {
			projection, loadErr := store.NoticePipeline(ctx, notice.PipelineRequest{Operation: "snapshot"})
			if loadErr != nil {
				return notice.Snapshot{}, loadErr
			}
			snapshot.Version = notice.PipelineSnapshotVersion(snapshot.Version, projection.Items)
			positions := make(map[string]int, len(snapshot.Items))
			for i, item := range snapshot.Items {
				positions[item.Exchange+"\x00"+item.NoticeID] = i
			}
			for _, item := range projection.Items {
				if i, found := positions[item.Exchange+"\x00"+item.NoticeID]; found {
					snapshot.Items[i] = item
				} else {
					snapshot.Items = append(snapshot.Items, item)
				}
			}
		}
	}
	snapshot.PollIntervalSec = s.noticePoll.Seconds()
	if s.noticeMonitor != nil {
		snapshot.OCRBackend = s.noticeMonitor.OCRBackend()
	} else {
		snapshot.OCRBackend = s.noticeOCRBackend
		if snapshot.OCRBackend == "" {
			snapshot.OCRBackend = "hermes_remote_vision"
		}
	}
	return snapshot, nil
}

func (s *Service) IngestHermesNotices(
	ctx context.Context,
	token string,
	request notice.HermesIngestRequest,
) (notice.HermesIngestResult, error) {
	if s.hermesNoticeIngestToken == "" {
		return notice.HermesIngestResult{}, notice.ErrHermesIngestDisabled
	}
	if !s.hermesNoticeAuthorized(token) {
		return notice.HermesIngestResult{}, notice.ErrHermesIngestUnauthorized
	}
	// An old/in-flight worker may still submit after the API cutover. Reject the
	// whole paused-source batch before any repository write or source heartbeat.
	for _, exchange := range request.ScannedExchanges {
		if notice.SourcePaused(exchange) {
			return notice.HermesIngestResult{}, fmt.Errorf("%w: notice source %s is temporarily paused", notice.ErrHermesIngestInvalid, exchange)
		}
	}
	for _, item := range request.Items {
		if notice.SourcePaused(item.Exchange) {
			return notice.HermesIngestResult{}, fmt.Errorf("%w: notice source %s is temporarily paused", notice.ErrHermesIngestInvalid, item.Exchange)
		}
	}
	normalized, err := notice.NormalizeHermesIngest(request, time.Now())
	if err != nil {
		return notice.HermesIngestResult{}, err
	}
	result, err := s.repository.SaveHermesNoticeBatch(ctx, normalized)
	if err != nil {
		return notice.HermesIngestResult{}, err
	}
	s.noticeMemoOnce.Do(func() { s.noticeMemo = &noticeSnapshotMemo{} })
	s.noticeMemo.invalidate()
	if result.Accepted == 0 {
		// A discovery heartbeat changes source freshness only. It must not run
		// movement relinking or invalidate market candidates when no notice row
		// changed.
		return result, nil
	}
	replacements, err := s.repository.RelinkRecentMovementNotices(ctx)
	if err != nil {
		return notice.HermesIngestResult{}, fmt.Errorf("relink official movements after Hermes notice ingest: %w", err)
	}
	if s.movementTracker != nil {
		s.movementTracker.ReplaceNoticeEvidence(replacements)
	}
	return result, nil
}

func (s *Service) hermesNoticeAuthorized(token string) bool {
	expected := s.hermesNoticeIngestToken
	return expected != "" && len(token) == len(expected) && subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (m *noticeSnapshotMemo) get(ctx context.Context, load func(context.Context) (notice.Snapshot, error)) (notice.Snapshot, error) {
	value, _, err := m.getVersioned(ctx, load)
	return value, err
}

func (m *noticeSnapshotMemo) generationValue() uint64 {
	m.mu.Lock()
	generation := m.generation
	m.mu.Unlock()
	return generation
}

func (m *noticeSnapshotMemo) getVersioned(
	ctx context.Context,
	load func(context.Context) (notice.Snapshot, error),
) (notice.Snapshot, uint64, error) {
	for {
		value, generation, err := m.getVersionedOnce(ctx, load)
		if err != nil {
			return notice.Snapshot{}, 0, err
		}
		if generation == m.generationValue() {
			return value, generation, nil
		}
		// An ingest completed while the previous database read was in flight.
		// Retry so callers never pair old evidence with the new generation.
	}
}

func (m *noticeSnapshotMemo) getVersionedOnce(
	ctx context.Context,
	load func(context.Context) (notice.Snapshot, error),
) (notice.Snapshot, uint64, error) {
	now := time.Now()
	m.mu.Lock()
	if m.cache != nil && now.Sub(m.cache.created) < noticeSnapshotCacheTTL {
		value := m.cache.value
		generation := m.cache.generation
		m.mu.Unlock()
		return value, generation, nil
	}
	if m.inflight != nil {
		call := m.inflight
		m.mu.Unlock()
		return waitForNoticeSnapshot(ctx, call)
	}
	call := &noticeSnapshotCall{done: make(chan struct{}), generation: m.generation}
	m.inflight = call
	m.mu.Unlock()

	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	go func() {
		defer cancel()
		call.value, call.err = load(queryCtx)
		var contentIdentity [sha256.Size]byte
		var contentIdentityReady bool
		if call.err == nil {
			contentIdentity, contentIdentityReady = noticeSnapshotContentIdentity(call.value.Items)
			m.mu.Lock()
			// Source polling/error status also advances Snapshot.Version. Prepared
			// evidence depends only on Items; retain fresh status/version while
			// reusing the exact content-identified, immutable evidence.
			if m.cache != nil && contentIdentityReady && m.cache.contentIdentityReady &&
				m.cache.contentIdentity == contentIdentity && m.cache.value.MarketEvidencePrepared {
				call.value.MarketEvidence = m.cache.value.MarketEvidence
				call.value.MarketEvidencePrepared = true
			}
			m.mu.Unlock()
			if !call.value.MarketEvidencePrepared {
				call.value = notice.PrepareMarketEvidenceSnapshot(call.value)
			}
		}
		m.mu.Lock()
		m.inflight = nil
		if call.err == nil && m.generation == call.generation {
			m.cache = &noticeSnapshotCacheEntry{
				created: time.Now(), generation: call.generation, value: call.value,
				contentIdentity: contentIdentity, contentIdentityReady: contentIdentityReady,
			}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return waitForNoticeSnapshot(ctx, call)
}

func waitForNoticeSnapshot(ctx context.Context, call *noticeSnapshotCall) (notice.Snapshot, uint64, error) {
	select {
	case <-ctx.Done():
		return notice.Snapshot{}, 0, ctx.Err()
	case <-call.done:
		return call.value, call.generation, call.err
	}
}

func noticeAssets(universe domain.MarketUniverse) map[string]struct{} {
	assets := make(map[string]struct{})
	for _, market := range universe.Markets() {
		for _, asset := range []string{market.Base, market.Quote} {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset != "" {
				assets[asset] = struct{}{}
			}
		}
	}
	return assets
}
