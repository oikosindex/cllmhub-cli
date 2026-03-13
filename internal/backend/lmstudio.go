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

const defaultLMStudioURL = "http://localhost:1234"

// LMStudio implements the Backend interface for LM Studio (OpenAI-compatible API)
type LMStudio struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

// NewLMStudio creates a new LM Studio backend
func NewLMStudio(cfg Config) (*LMStudio, error) {
	url := cfg.URL
	if url == "" {
		url = defaultLMStudioURL
	}

	if err := CheckInsecureAPIKey(url, cfg.APIKey); err != nil {
		return nil, err
	}

	return &LMStudio{
		url:    url,
		model:  cfg.Model,
		apiKey: cfg.APIKey,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (l *LMStudio) Name() string {
	return "lmstudio"
}

// URL returns the backend endpoint URL
func (l *LMStudio) URL() string {
	return l.url
}

// Complete sends a prompt and returns the full completion
func (l *LMStudio) Complete(ctx context.Context, req *Request) (*Response, error) {
	oaiReq := openAIRequest{
		Model:       l.model,
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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if l.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+l.apiKey)
	}

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lmstudio error (status %d): %s", resp.StatusCode, string(body))
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

// Stream sends a prompt and streams tokens via the callback
func (l *LMStudio) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	oaiReq := openAIRequest{
		Model:       l.model,
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

	httpReq, err := http.NewRequestWithContext(ctx, "POST", l.url+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if l.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+l.apiKey)
	}

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("lmstudio error (status %d): %s", resp.StatusCode, string(body))
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

// ListModels returns all models available in LM Studio.
func (l *LMStudio) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", l.url+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if l.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.apiKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lmstudio not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lmstudio returned status %d", resp.StatusCode)
	}

	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return nil, fmt.Errorf("failed to parse lmstudio models: %w", err)
	}

	var models []string
	for _, m := range modelsResp.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// Health checks if LM Studio is available
func (l *LMStudio) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", l.url+"/v1/models", nil)
	if err != nil {
		return err
	}
	if l.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.apiKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("lmstudio not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lmstudio returned status %d", resp.StatusCode)
	}

	return nil
}
