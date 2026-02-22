package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Custom implements the Backend interface for any HTTP endpoint
// that accepts a simple JSON request and returns a JSON response.
//
// Expected request format:
//
//	{
//	  "prompt": "...",
//	  "max_tokens": 512,
//	  "temperature": 0.7
//	}
//
// Expected response format:
//
//	{
//	  "text": "...",
//	  "prompt_tokens": 10,
//	  "completion_tokens": 100
//	}
type Custom struct {
	url    string
	apiKey string
	client *http.Client
}

// NewCustom creates a new custom backend
func NewCustom(cfg Config) (*Custom, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("custom backend requires a URL")
	}

	return &Custom{
		url:    cfg.URL,
		apiKey: cfg.APIKey,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (c *Custom) Name() string {
	return "custom"
}

// customRequest is the simple request format for custom backends
type customRequest struct {
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	Stream      bool    `json:"stream,omitempty"`
}

// customResponse is the simple response format for custom backends
type customResponse struct {
	Text             string `json:"text"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

// Complete sends a prompt and returns the full completion
func (c *Custom) Complete(ctx context.Context, req *Request) (*Response, error) {
	customReq := customRequest{
		Prompt:      req.Prompt,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      false,
	}

	body, err := json.Marshal(customReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("custom backend error (status %d): %s", resp.StatusCode, string(body))
	}

	var customResp customResponse
	if err := json.NewDecoder(resp.Body).Decode(&customResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &Response{
		Text:             customResp.Text,
		PromptTokens:     customResp.PromptTokens,
		CompletionTokens: customResp.CompletionTokens,
	}, nil
}

// Stream is not supported for custom backends; falls back to Complete
func (c *Custom) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	resp, err := c.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Emit the full response as a single token
	if err := callback(resp.Text, true); err != nil {
		return nil, err
	}

	return resp, nil
}

// Health checks if the custom backend is available
func (c *Custom) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("custom backend not reachable: %w", err)
	}
	defer resp.Body.Close()

	// Accept any 2xx or 4xx (might return 405 for GET on a POST-only endpoint)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("custom backend returned status %d", resp.StatusCode)
	}

	return nil
}
