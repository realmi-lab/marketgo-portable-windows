package restbudget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maximumErrorBody = 300
const maximumResponseBody = int64(32 << 20)

type rawBytesClient interface {
	GetBytes(context.Context, Request, int64) ([]byte, error)
}

type jsonDestinationClient interface {
	GetJSON(context.Context, Request, any) error
}

// FetchJSONBytes lets production clients hand parsers the original response
// bytes. Legacy/test clients that expose only GetJSON retain compatibility,
// but pay the encode step only outside the production HTTP path.
func FetchJSONBytes(ctx context.Context, client any, request Request, maximumBytes int64) ([]byte, error) {
	if client == nil {
		return nil, errors.New("official REST client is required")
	}
	if bytesClient, ok := client.(rawBytesClient); ok {
		return bytesClient.GetBytes(ctx, request, maximumBytes)
	}
	jsonClient, ok := client.(jsonDestinationClient)
	if !ok {
		return nil, errors.New("official REST client cannot fetch JSON")
	}
	var payload any
	if err := jsonClient.GetJSON(ctx, request, &payload); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode legacy official REST response: %w", err)
	}
	if maximumBytes < 1 || maximumBytes > maximumResponseBody {
		maximumBytes = maximumResponseBody
	}
	if int64(len(raw)) > maximumBytes {
		return nil, fmt.Errorf("official REST response exceeds %d bytes", maximumBytes)
	}
	return raw, nil
}

type HTTPError struct {
	Status int
	Detail string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("official REST HTTP %d: %s", e.Status, e.Detail)
}

type Client struct {
	HTTP     *http.Client
	Budgets  *Manager
	Attempts int
}

type Request struct {
	Exchange string
	Group    string
	URL      string
	Query    url.Values
	Priority int
	Cost     int
	Accept   string
	Headers  http.Header
}

func (c *Client) GetJSON(ctx context.Context, request Request, destination any) error {
	if c.HTTP == nil || c.Budgets == nil {
		return errors.New("HTTP client and REST budget manager are required")
	}
	if destination == nil {
		return errors.New("JSON destination is required")
	}
	body, err := c.GetBytes(ctx, request, maximumResponseBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode official REST response: %w", err)
	}
	return nil
}

func (c *Client) GetBytes(ctx context.Context, request Request, maximumBytes int64) ([]byte, error) {
	if c.HTTP == nil || c.Budgets == nil {
		return nil, errors.New("HTTP client and REST budget manager are required")
	}
	if maximumBytes < 1 || maximumBytes > maximumResponseBody {
		maximumBytes = maximumResponseBody
	}
	attempts := c.Attempts
	if attempts < 1 {
		attempts = 4
	}
	if request.Cost < 1 {
		request.Cost = 1
	}
	priority := request.Priority
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && priority > PriorityRetry {
			priority = PriorityRetry
		}
		if _, err := c.Budgets.Acquire(
			ctx, request.Exchange, request.Group, request.Cost, priority,
		); err != nil {
			return nil, err
		}
		response, err := c.get(ctx, request)
		if err != nil {
			// Cancellation belongs to the caller, not the exchange's quota or health.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			_ = c.Budgets.ObserveTransportError(request.Exchange, request.Group)
			if attempt+1 < attempts {
				continue
			}
			return nil, fmt.Errorf("official REST request failed after %d attempts: %w", attempts, err)
		}
		observeErr := c.Budgets.ObserveResponse(
			request.Exchange, request.Group, response.StatusCode, response.Header,
		)
		if observeErr != nil {
			response.Body.Close()
			return nil, observeErr
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			lastErr = responseError(response)
			if attempt+1 < attempts {
				continue
			}
			return nil, lastErr
		}
		if response.StatusCode >= 400 {
			return nil, responseError(response)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read official REST response: %w", readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(body)) > maximumBytes {
			return nil, fmt.Errorf("official REST response exceeds %d bytes", maximumBytes)
		}
		return body, nil
	}
	return nil, fmt.Errorf("official REST request failed: %w", lastErr)
}

func (c *Client) get(ctx context.Context, request Request) (*http.Response, error) {
	endpoint, err := url.Parse(request.URL)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	for key, values := range request.Query {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	endpoint.RawQuery = query.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	accept := request.Accept
	if accept == "" {
		accept = "application/json"
	}
	httpRequest.Header.Set("Accept", accept)
	for name, values := range request.Headers {
		for _, value := range values {
			httpRequest.Header.Add(name, value)
		}
	}
	return c.HTTP.Do(httpRequest)
}

func responseError(response *http.Response) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumErrorBody+1))
	if err != nil {
		return &HTTPError{Status: response.StatusCode, Detail: err.Error()}
	}
	if len(body) > maximumErrorBody {
		body = body[:maximumErrorBody]
	}
	return &HTTPError{
		Status: response.StatusCode,
		Detail: strings.TrimSpace(string(body)),
	}
}
