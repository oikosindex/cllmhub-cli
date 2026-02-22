package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/oikosindex/cllmhub-cli/internal/hub"
)

// Consumer manages requests to the LLMHub network via the gateway HTTP API.
type Consumer struct {
	client *hub.ConsumerClient
}

// Config holds consumer configuration
type Config struct {
	HubURL string
}

// New creates a new consumer instance
func New(cfg Config) (*Consumer, error) {
	return &Consumer{
		client: hub.NewConsumerClient(cfg.HubURL),
	}, nil
}

// Close is a no-op for the HTTP-based consumer (no persistent connection).
func (c *Consumer) Close() {}

// AskOptions configures a single request
type AskOptions struct {
	Model       string
	Prompt      string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
}

// Ask sends a prompt and waits for a complete response
func (c *Consumer) Ask(ctx context.Context, opts AskOptions) (string, error) {
	text, err := c.client.Ask(opts.Model, opts.Prompt, opts.MaxTokens, opts.Temperature)
	if err != nil {
		return "", err
	}
	return text, nil
}

// StreamOptions configures a streaming request
type StreamOptions struct {
	Model       string
	Prompt      string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
	OnToken     func(token string) error
}

// Stream sends a prompt and streams the response tokens
func (c *Consumer) Stream(ctx context.Context, opts StreamOptions) error {
	return c.client.Stream(opts.Model, opts.Prompt, opts.MaxTokens, opts.Temperature, opts.OnToken)
}

// ListModels queries the gateway for available models
func (c *Consumer) ListModels(ctx context.Context) ([]hub.ModelData, error) {
	models, err := c.client.ListModels()
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	return models, nil
}
