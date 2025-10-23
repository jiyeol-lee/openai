package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://api.openai.com/v1"

// Client handles OpenAI API requests
type Client struct {
	httpClient *http.Client
	apiKey     string
}

// ClientOption is a functional option for configuring the Client
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// NewClient creates a new OpenAI client
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// doRequest performs an HTTP request with proper headers
func (c *Client) doRequest(
	ctx context.Context,
	method, path string,
	body io.Reader,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf(
			"API error (status %s): %s",
			resp.Status,
			strings.TrimSpace(string(data)),
		)
	}

	return resp, nil
}

// marshalRequest marshals a request body to JSON
func marshalRequest(v any) (io.Reader, error) {
	bodyBytes, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return bytes.NewReader(bodyBytes), nil
}
