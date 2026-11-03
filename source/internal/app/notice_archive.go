package app

import (
	"context"
	"fmt"
	"market-share-go/internal/notice"
	"time"
)

func (s *Service) HermesNoticeArchivePage(ctx context.Context, token string, query notice.ArchiveQuery) (notice.ArchivePage, error) {
	if !s.hermesNoticeAuthorized(token) {
		return notice.ArchivePage{}, notice.ErrHermesIngestUnauthorized
	}
	return s.noticeArchivePage(ctx, query)
}

func (s *Service) noticeArchivePage(ctx context.Context, query notice.ArchiveQuery) (notice.ArchivePage, error) {
	if _, err := query.Validate(); err != nil {
		return notice.ArchivePage{}, err
	}
	store, ok := s.repository.(notice.ArchiveStore)
	if !ok {
		return notice.ArchivePage{}, fmt.Errorf("notice archive range reader unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return store.NoticeArchivePage(ctx, query)
}
