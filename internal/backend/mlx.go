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

const defaultMLXURL = "http://localhost:8080"

// MLX implements the Backend interface for mlx-lm (OpenAI-compatible API)
type MLX struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

// NewMLX creates a new MLX backend
func NewMLX(cfg Config) (*MLX, error) {
	url := cfg.URL
	if url == "" {
		url = defaultMLXURL
	}

	if err := CheckInsecureAPIKey(url, cfg.APIKey); err != nil {
		return nil, err
	}

	return &MLX{
		url:    url,
		model:  cfg.Model,
		apiKey: cfg.APIKey,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (m *MLX) Name() string {
	return "mlx"
}

// URL returns the backend endpoint URL
func (m *MLX) URL() string {
	return m.url
}

// Complete sends a prompt and returns the full completion
func (m *MLX) Complete(ctx context.Context, req *Request) (*Response, error) {
	if len(req.Messages) > 0 {
		return m.completeChat(ctx, req)
	}

	oaiReq := openAIRequest{
		Model:       m.model,
		Prompt:      req.Prompt,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      false,
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mlx error (status %d): %s", resp.StatusCode, string(body))
	}

	var oaiResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	text := ""
	if len(oaiResp.Choices) > 0 {
		text = oaiResp.Choices[0].Text
	}

	return &Response{
		Text:             text,
		PromptTokens:     oaiResp.Usage.PromptTokens,
		CompletionTokens: oaiResp.Usage.CompletionTokens,
	}, nil
}

func (m *MLX) completeChat(ctx context.Context, req *Request) (*Response, error) {
	chatReq := openAIChatRequest{
		Model:       m.model,
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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mlx error (status %d): %s", resp.StatusCode, string(body))
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
func (m *MLX) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	if len(req.Messages) > 0 {
		return m.streamChat(ctx, req, callback)
	}

	oaiReq := openAIRequest{
		Model:       m.model,
		Prompt:      req.Prompt,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      true,
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mlx error (status %d): %s", resp.StatusCode, string(body))
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

		var oaiResp openAIResponse
		if err := json.Unmarshal([]byte(data), &oaiResp); err != nil {
			continue
		}

		if len(oaiResp.Choices) > 0 {
			token := oaiResp.Choices[0].Text
			fullText += token

			done := oaiResp.Choices[0].FinishReason != ""
			if err := callback(token, done); err != nil {
				return nil, err
			}

			if done {
				promptTokens = oaiResp.Usage.PromptTokens
				completionTokens = oaiResp.Usage.CompletionTokens
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

func (m *MLX) streamChat(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	chatReq := openAIChatRequest{
		Model:       m.model,
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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mlx error (status %d): %s", resp.StatusCode, string(body))
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

// ListModels returns all models available in the mlx-lm server.
func (m *MLX) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", m.url+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mlx not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mlx returned status %d", resp.StatusCode)
	}

	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse mlx models: %w", err)
	}

	var models []string
	for _, mod := range modelsResp.Data {
		models = append(models, mod.ID)
	}
	return models, nil
}

// Health checks if the mlx-lm server is available
func (m *MLX) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", m.url+"/v1/models", nil)
	if err != nil {
		return err
	}
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("mlx not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mlx returned status %d", resp.StatusCode)
	}

	return nil
}
