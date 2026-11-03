package hermesrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type questionCallKey struct{}
type questionCallTrace struct {
	CallID               int64      `json:"call_id"`
	Stage                string     `json:"stage"`
	PreparedAt           time.Time  `json:"prepared_at"`
	ExecStartedAt        *time.Time `json:"exec_started_at,omitempty"`
	ExitedAt             *time.Time `json:"exited_at,omitempty"`
	EffectiveRemainingMS int64      `json:"effective_remaining_ms"`
	Result               string     `json:"result"`
	ProviderStart        string     `json:"provider_start"`
}

func prepareQuestionCall(ctx context.Context, stage string) context.Context {
	now := time.Now().UTC()
	return context.WithValue(ctx, questionCallKey{}, &questionCallTrace{CallID: now.UnixNano(), Stage: stage, PreparedAt: now, ProviderStart: "UNKNOWN"})
}
func questionTrace(ctx context.Context) *questionCallTrace {
	trace, _ := ctx.Value(questionCallKey{}).(*questionCallTrace)
	return trace
}
func writeQuestionTrace(dir string, trace *questionCallTrace) {
	raw, err := json.Marshal(trace)
	if err == nil {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d-execution.json", trace.CallID)), raw, 0600)
	}
}
func finishQuestionTrace(dir string, trace *questionCallTrace, err error) {
	now := time.Now().UTC()
	trace.ExitedAt = &now
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		trace.Result = "DEADLINE_EXCEEDED"
	case errors.Is(err, context.Canceled):
		trace.Result = "CANCELED"
	case err != nil && trace.ExecStartedAt == nil:
		trace.Result = "START_FAILED"
	case err != nil:
		trace.Result = "PROCESS_FAILED"
	default:
		trace.Result = "COMPLETED"
	}
	writeQuestionTrace(dir, trace)
}
