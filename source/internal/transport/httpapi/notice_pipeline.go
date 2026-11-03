package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"market-share-go/internal/notice"
)

type NoticePipelineProvider interface {
	NoticePipeline(context.Context, string, notice.PipelineRequest) (notice.PipelineResponse, error)
}

func registerNoticePipeline(mux *http.ServeMux, provider any) {
	p, _ := provider.(NoticePipelineProvider)
	for _, op := range []string{"capabilities", "source", "claim", "heartbeat", "object", "object_get", "page", "complete", "fail"} {
		mux.HandleFunc("POST /internal/hermes/notices/v2/"+op, func(w http.ResponseWriter, r *http.Request) {
			if p == nil {
				unavailable(w)
				return
			}
			if !hermesTransportAllowed(r) {
				writeJSON(w, 403, map[string]string{"detail": "loopback transport required"})
				return
			}
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeJSON(w, 401, map[string]string{"detail": "authorization required"})
				return
			}
			var req notice.PipelineRequest
			if op == "object" {
				r.Body = http.MaxBytesReader(w, r.Body, notice.PipelineMaxObjectBytes)
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					writeJSON(w, 413, map[string]string{"detail": "object too large"})
					return
				}
				req.RequestID = r.Header.Get("X-Notice-Request-ID")
				req.JobID = r.Header.Get("X-Notice-Job-ID")
				req.Exchange = r.Header.Get("X-Notice-Exchange")
				req.SourceGeneration, _ = strconv.ParseInt(r.Header.Get("X-Notice-Generation"), 10, 64)
				req.Object = &notice.PipelineObject{Bytes: raw, MediaType: r.Header.Get("Content-Type"), SHA256: r.Header.Get("X-Notice-SHA256")}
			} else {
				r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
				d := json.NewDecoder(r.Body)
				d.DisallowUnknownFields()
				if err := d.Decode(&req); err != nil {
					writeJSON(w, 400, map[string]string{"detail": "invalid pipeline JSON"})
					return
				}
				if err := d.Decode(&struct{}{}); err != io.EOF {
					writeJSON(w, 400, map[string]string{"detail": "one JSON object required"})
					return
				}
			}
			req.Operation = op
			out, err := p.NoticePipeline(r.Context(), token, req)
			switch {
			case errors.Is(err, notice.ErrHermesIngestUnauthorized):
				writeJSON(w, 401, map[string]string{"detail": "unauthorized"})
			case errors.Is(err, notice.ErrPipelineInvalid):
				writeJSON(w, 400, map[string]string{"detail": err.Error()})
			case errors.Is(err, notice.ErrPipelineConflict):
				writeJSON(w, 409, map[string]string{"detail": err.Error()})
			case err != nil:
				writeJSON(w, 503, map[string]string{"detail": "notice pipeline unavailable"})
			default:
				writeJSON(w, 200, out)
			}
		})
	}
}
