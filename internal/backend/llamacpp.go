package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultLlamaCppURL = "http://localhost:8080"

// LlamaCpp implements the Backend interface for llama.cpp server
type LlamaCpp struct {
	url    string
	client *http.Client
}

// NewLlamaCpp creates a new llama.cpp backend
func NewLlamaCpp(cfg Config) (*LlamaCpp, error) {
	url := cfg.URL
	if url == "" {
		url = defaultLlamaCppURL
	}

	return &LlamaCpp{
		url: url,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (l *LlamaCpp) Name() string {
	return "llama.cpp"
}

// llamaCppRequest is the llama.cpp server request format
type llamaCppRequest struct {
	Prompt      string  `json:"prompt"`
	NPredict    int     `json:"n_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	Stream      bool    `json:"stream"`
}

// llamaCppResponse is the llama.cpp server response format
type llamaCppResponse struct {
	Content          string `json:"content"`
	Stop             bool   `json:"stop"`
	TokensEvaluated  int    `json:"tokens_evaluated"`
	TokensPredicted  int    `json:"tokens_predicted"`
}

// Complete sends a prompt and returns the full completion
func (l *LlamaCpp) Complete(ctx context.Context, req *Request) (*Response, error) {
	llamaReq := llamaCppRequest{
		Prompt:      req.Prompt,
		NPredict:    req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      false,
	}

	body, err := json.Marshal(llamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.url+"/completion", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama.cpp error (status %d): %s", resp.StatusCode, string(body))
	}

	var llamaResp llamaCppResponse
	if err := json.NewDecoder(resp.Body).Decode(&llamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &Response{
		Text:             llamaResp.Content,
		PromptTokens:     llamaResp.TokensEvaluated,
		CompletionTokens: llamaResp.TokensPredicted,
	}, nil
}

// Stream sends a prompt and streams tokens via the callback
func (l *LlamaCpp) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	llamaReq := llamaCppRequest{
		Prompt:      req.Prompt,
		NPredict:    req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      true,
	}

	body, err := json.Marshal(llamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.url+"/completion", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llama.cpp error (status %d): %s", resp.StatusCode, string(body))
	}

	var fullText string
	var promptTokens, completionTokens int

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		var llamaResp llamaCppResponse
		if err := json.Unmarshal([]byte(data), &llamaResp); err != nil {
			continue
		}

		fullText += llamaResp.Content

		if err := callback(llamaResp.Content, llamaResp.Stop); err != nil {
			return nil, err
		}

		if llamaResp.Stop {
			promptTokens = llamaResp.TokensEvaluated
			completionTokens = llamaResp.TokensPredicted
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading stream: %w", err)
	}

	return &Response{
		Text:             fullText,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}, nil
}

// Health checks if llama.cpp server is available
func (l *LlamaCpp) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", l.url+"/health", nil)
	if err != nil {
		return err
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("llama.cpp not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("llama.cpp returned status %d", resp.StatusCode)
	}

	return nil
}
