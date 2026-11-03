package hermesrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/notice"
)

func (c BackendClient) Pipeline(ctx context.Context, input notice.PipelineRequest) (notice.PipelineResponse, error) {
	var out notice.PipelineResponse
	base, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || base.Host == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return out, errors.New("invalid pipeline backend URL")
	}
	base.Path = "/internal/hermes/notices/v2/" + input.Operation
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	if notice.PipelineOperation(base.Path) == "" {
		return out, notice.ErrPipelineInvalid
	}
	data, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	if input.Operation == "object" {
		if input.Object == nil {
			return out, notice.ErrPipelineInvalid
		}
		data = input.Object.Bytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if token := strings.TrimSpace(c.TransportToken); token != "" {
		req.Header.Set("X-Marketgo-Hermes-Proxy", token)
	}
	req.Header.Set("Content-Type", "application/json")
	if input.Operation == "object" {
		req.Header.Set("Content-Type", input.Object.MediaType)
		req.Header.Set("X-Notice-Request-ID", input.RequestID)
		req.Header.Set("X-Notice-Job-ID", input.JobID)
		req.Header.Set("X-Notice-Exchange", input.Exchange)
		req.Header.Set("X-Notice-Generation", strconv.FormatInt(input.SourceGeneration, 10))
		req.Header.Set("X-Notice-SHA256", input.Object.SHA256)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.StatusCode == 409 {
		return out, notice.ErrPipelineConflict
	}
	if response.StatusCode != 200 {
		return out, fmt.Errorf("pipeline HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBackendResponseBytes+1))
	if err != nil {
		return out, err
	}
	if int64(len(raw)) > maxBackendResponseBytes {
		return out, errors.New("pipeline response too large")
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
