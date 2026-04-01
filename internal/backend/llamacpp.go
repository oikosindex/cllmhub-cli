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
	model  string
	client *http.Client
}

// NewLlamaCpp creates a new llama.cpp backend
func NewLlamaCpp(cfg Config) (*LlamaCpp, error) {
	url := cfg.URL
	if url == "" {
		url = defaultLlamaCppURL
	}

	return &LlamaCpp{
		url:   url,
		model: cfg.Model,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (l *LlamaCpp) Name() string {
	return "llama.cpp"
}

// URL returns the backend endpoint URL
func (l *LlamaCpp) URL() string {
	return l.url
}

// llamaCppRequest is the llama.cpp server request format
type llamaCppRequest struct {
	Model       string  `json:"model,omitempty"`
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
	if len(req.Messages) > 0 {
		return l.completeChat(ctx, req)
	}

	llamaReq := llamaCppRequest{
		Model:       l.model,
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

func (l *LlamaCpp) completeChat(ctx context.Context, req *Request) (*Response, error) {
	chatReq := openAIChatRequest{
		Model:       l.model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      false,
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.url+"/v1/chat/completions", bytes.NewReader(body))
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

	var chatResp openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	text := ""
	if len(chatResp.Choices) > 0 {
		text = chatResp.Choices[0].Message.Content
	}

	return &Response{
		Text:             text,
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
	}, nil
}

// Stream sends a prompt and streams tokens via the callback
func (l *LlamaCpp) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	if len(req.Messages) > 0 {
		return l.streamChat(ctx, req, callback)
	}

	llamaReq := llamaCppRequest{
		Model:       l.model,
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

func (l *LlamaCpp) streamChat(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	chatReq := openAIChatRequest{
		Model:       l.model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      true,
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.url+"/v1/chat/completions", bytes.NewReader(body))
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
		if data == "[DONE]" {
			if err := callback("", true); err != nil {
				return nil, err
			}
			break
		}

		var chatResp openAIChatResponse
		if err := json.Unmarshal([]byte(data), &chatResp); err != nil {
			continue
		}

		if len(chatResp.Choices) > 0 {
			token := chatResp.Choices[0].Delta.Content
			fullText += token

			done := chatResp.Choices[0].FinishReason != ""
			if err := callback(token, done); err != nil {
				return nil, err
			}

			if done {
				promptTokens = chatResp.Usage.PromptTokens
				completionTokens = chatResp.Usage.CompletionTokens
			}
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

// ListModels is not supported for llama.cpp (single-model server).
func (l *LlamaCpp) ListModels(ctx context.Context) ([]string, error) {
	return nil, nil
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
