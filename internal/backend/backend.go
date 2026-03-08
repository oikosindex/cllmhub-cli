package backend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
)

// Backend defines the interface for LLM inference backends
type Backend interface {
	// Name returns the backend type name
	Name() string

	// URL returns the backend endpoint URL
	URL() string

	// Complete sends a prompt and returns the full completion
	Complete(ctx context.Context, req *Request) (*Response, error)

	// Stream sends a prompt and streams tokens via the callback
	Stream(ctx context.Context, req *Request, callback func(token string, done bool) error) (*Response, error)

	// Health checks if the backend is available
	Health(ctx context.Context) error

	// ListModels returns the models available on the backend.
	// Returns nil, nil if the backend does not support listing.
	ListModels(ctx context.Context) ([]string, error)
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

// CheckInsecureAPIKey returns an error if an API key is being sent over
// plain HTTP to a non-localhost host.
func CheckInsecureAPIKey(rawURL, apiKey string) error {
	if apiKey == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	if u.Scheme != "http" {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing to send API key over plain HTTP to remote host %q; use HTTPS or remove the API key", host)
}

// IsConnectionError returns true if the error indicates the model server
// is unreachable (connection refused, timeout, no route, etc.).
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return false
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
