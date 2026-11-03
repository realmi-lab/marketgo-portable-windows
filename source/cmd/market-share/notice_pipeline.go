package main

import (
	"context"
	"market-share-go/internal/notice"
)

func (s serviceAPI) NoticePipeline(ctx context.Context, token string, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	return s.service.NoticePipeline(ctx, token, req)
}
