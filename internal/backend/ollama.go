package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultOllamaURL = "http://localhost:11434"

// Ollama implements the Backend interface for Ollama
type Ollama struct {
	url    string
	model  string
	client *http.Client
}

// NewOllama creates a new Ollama backend
func NewOllama(cfg Config) (*Ollama, error) {
	url := cfg.URL
	if url == "" {
		url = defaultOllamaURL
	}

	return &Ollama{
		url:   url,
		model: cfg.Model,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

// Name returns the backend type
func (o *Ollama) Name() string {
	return "ollama"
}

// ollamaRequest is the Ollama API request format
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Options struct {
		NumPredict  int     `json:"num_predict,omitempty"`
		Temperature float64 `json:"temperature,omitempty"`
		TopP        float64 `json:"top_p,omitempty"`
	} `json:"options,omitempty"`
}

// ollamaResponse is the Ollama API response format
type ollamaResponse struct {
	Model     string `json:"model"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
	Context   []int  `json:"context,omitempty"`
	PromptEvalCount int `json:"prompt_eval_count,omitempty"`
	EvalCount       int `json:"eval_count,omitempty"`
}

// Complete sends a prompt and returns the full completion
func (o *Ollama) Complete(ctx context.Context, req *Request) (*Response, error) {
	ollamaReq := ollamaRequest{
		Model:  o.model,
		Prompt: req.Prompt,
		Stream: false,
	}
	ollamaReq.Options.NumPredict = req.MaxTokens
	ollamaReq.Options.Temperature = req.Temperature
	ollamaReq.Options.TopP = req.TopP

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.url+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(body))
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &Response{
		Text:             ollamaResp.Response,
		PromptTokens:     ollamaResp.PromptEvalCount,
		CompletionTokens: ollamaResp.EvalCount,
	}, nil
}

// Stream sends a prompt and streams tokens via the callback
func (o *Ollama) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	ollamaReq := ollamaRequest{
		Model:  o.model,
		Prompt: req.Prompt,
		Stream: true,
	}
	ollamaReq.Options.NumPredict = req.MaxTokens
	ollamaReq.Options.Temperature = req.Temperature
	ollamaReq.Options.TopP = req.TopP

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.url+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(body))
	}

	var fullText string
	var promptTokens, completionTokens int

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var ollamaResp ollamaResponse
		if err := json.Unmarshal(scanner.Bytes(), &ollamaResp); err != nil {
			continue
		}

		fullText += ollamaResp.Response

		if err := callback(ollamaResp.Response, ollamaResp.Done); err != nil {
			return nil, err
		}

		if ollamaResp.Done {
			promptTokens = ollamaResp.PromptEvalCount
			completionTokens = ollamaResp.EvalCount
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

// Health checks if Ollama is available and the configured model exists
func (o *Ollama) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", o.url+"/api/tags", nil)
	if err != nil {
		return err
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return fmt.Errorf("failed to parse ollama models: %w", err)
	}

	var available []string
	for _, m := range tagsResp.Models {
		// Ollama returns names like "llama3:latest" — match with or without the tag
		name := m.Name
		available = append(available, name)
		// Strip ":latest" for comparison
		base := name
		if idx := len(name) - len(":latest"); idx > 0 && name[idx:] == ":latest" {
			base = name[:idx]
		}
		if name == o.model || base == o.model {
			return nil
		}
	}

	if len(available) == 0 {
		return fmt.Errorf("model %q not found in ollama — no models available, run:\n  ollama pull %s", o.model, o.model)
	}

	return fmt.Errorf("model %q not found in ollama\n\nAvailable models:\n  %s\n\nTo pull it, run:\n  ollama pull %s",
		o.model, formatModelList(available), o.model)
}

func formatModelList(models []string) string {
	result := ""
	for i, m := range models {
		if i > 0 {
			result += "\n  "
		}
		result += m
	}
	return result
}
