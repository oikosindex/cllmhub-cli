package backend

import (
	"context"
	"fmt"
)

// Backend defines the interface for LLM inference backends
type Backend interface {
	// Name returns the backend type name
	Name() string

	// Complete sends a prompt and returns the full completion
	Complete(ctx context.Context, req *Request) (*Response, error)

	// Stream sends a prompt and streams tokens via the callback
	Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error)

	// Health checks if the backend is available
	Health(ctx context.Context) error
}

// Request represents an inference request to a backend
type Request struct {
	Prompt      string
	MaxTokens   int
	Temperature float64
	TopP        float64
}

// Response represents an inference response from a backend
type Response struct {
	Text             string
	PromptTokens     int
	CompletionTokens int
}

// Config holds backend configuration
type Config struct {
	Type     string // "ollama", "llamacpp", "vllm", "custom"
	URL      string
	Model    string
	APIKey   string // for custom backends that need auth
}

// New creates a backend based on the config type
func New(cfg Config) (Backend, error) {
	switch cfg.Type {
	case "ollama":
		return NewOllama(cfg)
	case "llamacpp", "llama.cpp":
		return NewLlamaCpp(cfg)
	case "vllm":
		return NewVLLM(cfg)
	case "custom":
		return NewCustom(cfg)
	default:
		return nil, fmt.Errorf("unknown backend type: %s", cfg.Type)
	}
}
