package hub

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ConsumerClient is an HTTP client that talks to the gateway's REST API.
type ConsumerClient struct {
	hubURL string
	client *http.Client
}

// NewConsumerClient creates a new consumer HTTP client.
func NewConsumerClient(hubURL string) *ConsumerClient {
	return &ConsumerClient{
		hubURL: strings.TrimRight(hubURL, "/"),
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// --- OpenAI-compatible types ---

// ChatMessage is an OpenAI chat message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest is the request body for /v1/chat/completions.
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

// ChatCompletionResponse is the response from /v1/chat/completions (non-streaming).
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

// ChatChoice is a single choice in the response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage contains token usage.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk is a streaming SSE chunk from the gateway.
type StreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
}

// StreamChoice is a choice within a streaming chunk.
type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        StreamDelta  `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

// StreamDelta holds incremental content.
type StreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ModelResponse is the response from /v1/models.
type ModelResponse struct {
	Object string      `json:"object"`
	Data   []ModelData `json:"data"`
}

// ModelData represents a single model entry.
type ModelData struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ErrorResponse is an API error.
type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Ask sends a non-streaming chat completion request and returns the text.
func (c *ConsumerClient) Ask(model, prompt string, maxTokens int, temperature float64) (string, error) {
	temp := temperature
	req := ChatCompletionRequest{
		Model:       model,
		Messages:    []ChatMessage{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: &temp,
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.client.Post(c.hubURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", c.parseError(resp)
	}

	var chatResp ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// Stream sends a streaming chat completion request and calls onToken for each token.
func (c *ConsumerClient) Stream(model, prompt string, maxTokens int, temperature float64, onToken func(token string) error) error {
	temp := temperature
	req := ChatCompletionRequest{
		Model:       model,
		Messages:    []ChatMessage{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: &temp,
		Stream:      true,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.hubURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		if strings.HasPrefix(data, "[ERROR]") {
			return fmt.Errorf("stream error: %s", strings.TrimPrefix(data, "[ERROR] "))
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if err := onToken(choice.Delta.Content); err != nil {
					return err
				}
			}
		}
	}

	return scanner.Err()
}

// ListModels queries the gateway for available models.
func (c *ConsumerClient) ListModels() ([]ModelData, error) {
	resp, err := c.client.Get(c.hubURL + "/v1/models")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var models ModelResponse
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return models.Data, nil
}

func (c *ConsumerClient) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, errResp.Error.Message)
	}
	return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
}
