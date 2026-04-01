package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// URL returns the backend endpoint URL
func (o *Ollama) URL() string {
	return o.url
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

// ollamaChatRequest is the Ollama /api/chat request format for multimodal messages.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  struct {
		NumPredict  int     `json:"num_predict,omitempty"`
		Temperature float64 `json:"temperature,omitempty"`
		TopP        float64 `json:"top_p,omitempty"`
	} `json:"options,omitempty"`
}

// ollamaChatResponse is the Ollama /api/chat response format.
type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count,omitempty"`
	EvalCount       int  `json:"eval_count,omitempty"`
}

// Complete sends a prompt and returns the full completion
func (o *Ollama) Complete(ctx context.Context, req *Request) (*Response, error) {
	if len(req.Messages) > 0 {
		return o.completeChat(ctx, req)
	}

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

func (o *Ollama) completeChat(ctx context.Context, req *Request) (*Response, error) {
	msgs, err := convertToOllamaMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}
	chatReq := ollamaChatRequest{
		Model:    o.model,
		Messages: msgs,
		Stream:   false,
	}
	chatReq.Options.NumPredict = req.MaxTokens
	chatReq.Options.Temperature = req.Temperature
	chatReq.Options.TopP = req.TopP

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.url+"/api/chat", bytes.NewReader(body))
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

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &Response{
		Text:             chatResp.Message.Content,
		PromptTokens:     chatResp.PromptEvalCount,
		CompletionTokens: chatResp.EvalCount,
	}, nil
}

// Stream sends a prompt and streams tokens via the callback
func (o *Ollama) Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	if len(req.Messages) > 0 {
		return o.streamChat(ctx, req, callback)
	}

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

func (o *Ollama) streamChat(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error) {
	msgs, err := convertToOllamaMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}
	chatReq := ollamaChatRequest{
		Model:    o.model,
		Messages: msgs,
		Stream:   true,
	}
	chatReq.Options.NumPredict = req.MaxTokens
	chatReq.Options.Temperature = req.Temperature
	chatReq.Options.TopP = req.TopP

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.url+"/api/chat", bytes.NewReader(body))
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
		var chatResp ollamaChatResponse
		if err := json.Unmarshal(scanner.Bytes(), &chatResp); err != nil {
			continue
		}

		fullText += chatResp.Message.Content

		if err := callback(chatResp.Message.Content, chatResp.Done); err != nil {
			return nil, err
		}

		if chatResp.Done {
			promptTokens = chatResp.PromptEvalCount
			completionTokens = chatResp.EvalCount
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

// convertToOllamaMessages transforms OpenAI-format messages (where content may
// be an array of parts) into Ollama's format (content as string, images as a
// separate base64 array). Messages that already have string content pass through.
func convertToOllamaMessages(raw json.RawMessage) (json.RawMessage, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return raw, nil
	}

	type ollamaMsg struct {
		Role    string   `json:"role"`
		Content string   `json:"content"`
		Images  []string `json:"images,omitempty"`
	}

	type contentPart struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url,omitempty"`
	}

	var result []ollamaMsg
	for _, msgRaw := range messages {
		var probe struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(msgRaw, &probe); err != nil {
			return raw, nil
		}

		// If content is a string, pass through as-is.
		var contentStr string
		if err := json.Unmarshal(probe.Content, &contentStr); err == nil {
			result = append(result, ollamaMsg{Role: probe.Role, Content: contentStr})
			continue
		}

		// Content is an array of parts — convert to Ollama format.
		var parts []contentPart
		if err := json.Unmarshal(probe.Content, &parts); err != nil {
			return raw, nil
		}

		var textParts []string
		var images []string
		for _, p := range parts {
			switch p.Type {
			case "text":
				textParts = append(textParts, p.Text)
			case "image_url":
				if p.ImageURL == nil {
					continue
				}
				img := p.ImageURL.URL
				// Strip data URI prefix to get raw base64.
				if idx := strings.Index(img, ";base64,"); idx != -1 {
					img = img[idx+8:]
				}
				// Validate that it looks like base64.
				if _, err := base64.StdEncoding.DecodeString(img); err == nil {
					images = append(images, img)
				}
			}
		}

		result = append(result, ollamaMsg{
			Role:    probe.Role,
			Content: strings.Join(textParts, "\n"),
			Images:  images,
		})
	}

	converted, err := json.Marshal(result)
	if err != nil {
		return raw, nil
	}
	return converted, nil
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

// ListModels returns all models available in Ollama.
func (o *Ollama) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", o.url+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("failed to parse ollama models: %w", err)
	}

	var models []string
	for _, m := range tagsResp.Models {
		models = append(models, m.Name)
	}
	return models, nil
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
