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

const defaultVLLMURL = "http://localhost:8000"

// VLLM implements the Backend interface for vLLM (OpenAI-compatible API)
type VLLM struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

// NewVLLM creates a new vLLM backend
func NewVLLM(cfg Config) (*VLLM, error) {
	url := cfg.URL
	if url == "" {
		url = defaultVLLMURL
	}

	return &VLLM{
		url:    url,
		model:  cfg.Model,
		apiKey: cfg.APIKey,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (v *VLLM) Name() string {
	return "vllm"
}

// openAIRequest is the OpenAI-compatible request format
type openAIRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	Stream      bool    `json:"stream"`
}

// openAIResponse is the OpenAI-compatible response format
type openAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Choices []struct {
		Text         string `json:"text"`
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Complete sends a prompt and returns the full completion
func (v *VLLM) Complete(ctx context.Context, req *Request) (*Response, error) {
	vllmReq := openAIRequest{
		Model:       v.model,
		Prompt:      req.Prompt,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      false,
	}

	body, err := json.Marshal(vllmReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", v.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if v.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+v.apiKey)
	}

	resp, err := v.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vllm error (status %d): %s", resp.StatusCode, string(body))
	}

	var vllmResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&vllmResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	text := ""
	if len(vllmResp.Choices) > 0 {
		text = vllmResp.Choices[0].Text
	}

	return &Response{
		Text:             text,
		PromptTokens:     vllmResp.Usage.PromptTokens,
		CompletionTokens: vllmResp.Usage.CompletionTokens,
	}, nil
}

// Stream sends a prompt and streams tokens via the callback
func (v *VLLM) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	vllmReq := openAIRequest{
		Model:       v.model,
		Prompt:      req.Prompt,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      true,
	}

	body, err := json.Marshal(vllmReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", v.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if v.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+v.apiKey)
	}

	resp, err := v.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vllm error (status %d): %s", resp.StatusCode, string(body))
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

		var vllmResp openAIResponse
		if err := json.Unmarshal([]byte(data), &vllmResp); err != nil {
			continue
		}

		if len(vllmResp.Choices) > 0 {
			token := vllmResp.Choices[0].Text
			fullText += token

			done := vllmResp.Choices[0].FinishReason != ""
			if err := callback(token, done); err != nil {
				return nil, err
			}

			if done {
				promptTokens = vllmResp.Usage.PromptTokens
				completionTokens = vllmResp.Usage.CompletionTokens
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

// Health checks if vLLM is available
func (v *VLLM) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", v.url+"/v1/models", nil)
	if err != nil {
		return err
	}
	if v.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+v.apiKey)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vllm not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vllm returned status %d", resp.StatusCode)
	}

	return nil
}
