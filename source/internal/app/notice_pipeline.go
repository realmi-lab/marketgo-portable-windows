package app

import (
	"context"

	"market-share-go/internal/notice"
)

func (s *Service) NoticePipeline(ctx context.Context, token string, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	if !s.hermesNoticeAuthorized(token) {
		return notice.PipelineResponse{}, notice.ErrHermesIngestUnauthorized
	}
	if !s.noticePipelineV2Enabled {
		return notice.PipelineResponse{}, notice.ErrPipelineDisabled
	}
	store, ok := s.repository.(notice.PipelineStore)
	if !ok {
		return notice.PipelineResponse{}, notice.ErrPipelineDisabled
	}
	out, err := store.NoticePipeline(ctx, req)
	if err == nil && (req.Operation == "complete" || req.Operation == "source") {
		s.noticeMemoOnce.Do(func() { s.noticeMemo = &noticeSnapshotMemo{} })
		s.noticeMemo.invalidate()
	}
	return out, err
}

func (s *gatedServiceRepository) NoticePipeline(ctx context.Context, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	store, ok := s.serviceRepository.(notice.PipelineStore)
	if !ok {
		return notice.PipelineResponse{}, notice.ErrPipelineDisabled
	}
	var out notice.PipelineResponse
	err := s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "notice_pipeline_v2", run: func(writeCtx context.Context) error {
		var err error
		out, err = store.NoticePipeline(writeCtx, req)
		return err
	}})
	return out, err
}
