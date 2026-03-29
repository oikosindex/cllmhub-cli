package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
)

func TestCheckInsecureAPIKey_EmptyKey(t *testing.T) {
	if err := CheckInsecureAPIKey("http://remote.example.com", ""); err != nil {
		t.Errorf("expected nil for empty API key, got %v", err)
	}
}

func TestCheckInsecureAPIKey_HTTPS(t *testing.T) {
	if err := CheckInsecureAPIKey("https://remote.example.com", "secret"); err != nil {
		t.Errorf("expected nil for HTTPS, got %v", err)
	}
}

func TestCheckInsecureAPIKey_Localhost(t *testing.T) {
	cases := []string{
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	}
	for _, u := range cases {
		if err := CheckInsecureAPIKey(u, "secret"); err != nil {
			t.Errorf("expected nil for %q, got %v", u, err)
		}
	}
}

func TestCheckInsecureAPIKey_RemoteHTTP(t *testing.T) {
	err := CheckInsecureAPIKey("http://remote.example.com", "secret")
	if err == nil {
		t.Fatal("expected error for API key over HTTP to remote host")
	}
}

func TestCheckInsecureAPIKey_InvalidURL(t *testing.T) {
	// Invalid URL should not error (fail open)
	if err := CheckInsecureAPIKey("://bad", "key"); err != nil {
		t.Errorf("expected nil for invalid URL, got %v", err)
	}
}

func TestIsConnectionError_Nil(t *testing.T) {
	if IsConnectionError(nil) {
		t.Error("expected false for nil error")
	}
}

func TestIsConnectionError_OpError(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("connection refused")}
	if !IsConnectionError(err) {
		t.Error("expected true for net.OpError")
	}
}

func TestIsConnectionError_DNSError(t *testing.T) {
	err := &net.DNSError{Err: "no such host", Name: "bad.host"}
	if !IsConnectionError(err) {
		t.Error("expected true for net.DNSError")
	}
}

func TestIsConnectionError_ECONNREFUSED(t *testing.T) {
	if !IsConnectionError(syscall.ECONNREFUSED) {
		t.Error("expected true for ECONNREFUSED")
	}
}

func TestIsConnectionError_GenericError(t *testing.T) {
	if IsConnectionError(fmt.Errorf("something else")) {
		t.Error("expected false for generic error")
	}
}

func TestNew_ValidTypes(t *testing.T) {
	cases := []struct {
		typ  string
		name string
	}{
		{"ollama", "ollama"},
		{"llamacpp", "llama.cpp"},
		{"llama.cpp", "llama.cpp"},
		{"vllm", "vllm"},
		{"lmstudio", "lmstudio"},
		{"mlx", "mlx"},
	}

	for _, tc := range cases {
		b, err := New(Config{Type: tc.typ, URL: "http://localhost:1234"})
		if err != nil {
			t.Errorf("New(%q): %v", tc.typ, err)
			continue
		}
		if b.Name() != tc.name {
			t.Errorf("New(%q).Name() = %q, want %q", tc.typ, b.Name(), tc.name)
		}
	}
}

func TestNew_UnknownType(t *testing.T) {
	_, err := New(Config{Type: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown backend type")
	}
}

func TestLlamaCpp_DefaultURL(t *testing.T) {
	b, err := NewLlamaCpp(Config{})
	if err != nil {
		t.Fatalf("NewLlamaCpp: %v", err)
	}
	if b.URL() != "http://localhost:8080" {
		t.Errorf("URL = %q, want %q", b.URL(), "http://localhost:8080")
	}
}

func TestLlamaCpp_CustomURL(t *testing.T) {
	b, err := NewLlamaCpp(Config{URL: "http://myhost:9090"})
	if err != nil {
		t.Fatalf("NewLlamaCpp: %v", err)
	}
	if b.URL() != "http://myhost:9090" {
		t.Errorf("URL = %q, want %q", b.URL(), "http://myhost:9090")
	}
}

func TestLlamaCpp_Name(t *testing.T) {
	b, _ := NewLlamaCpp(Config{})
	if b.Name() != "llama.cpp" {
		t.Errorf("Name = %q, want %q", b.Name(), "llama.cpp")
	}
}

func TestLlamaCpp_Complete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completion" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}

		json.NewEncoder(w).Encode(llamaCppResponse{
			Content:         "Hello world",
			Stop:            true,
			TokensEvaluated: 10,
			TokensPredicted: 5,
		})
	}))
	defer srv.Close()

	b, _ := NewLlamaCpp(Config{URL: srv.URL, Model: "test-model"})
	resp, err := b.Complete(context.Background(), &Request{
		Prompt:    "Say hello",
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Text != "Hello world" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello world")
	}
	if resp.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", resp.PromptTokens)
	}
	if resp.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", resp.CompletionTokens)
	}
}

func TestLlamaCpp_Complete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	b, _ := NewLlamaCpp(Config{URL: srv.URL})
	_, err := b.Complete(context.Background(), &Request{Prompt: "test"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestLlamaCpp_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")

		chunks := []llamaCppResponse{
			{Content: "Hello", Stop: false},
			{Content: " world", Stop: true, TokensEvaluated: 10, TokensPredicted: 2},
		}
		for _, c := range chunks {
			data, _ := json.Marshal(c)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	b, _ := NewLlamaCpp(Config{URL: srv.URL})

	var tokens []string
	resp, err := b.Stream(context.Background(), &Request{Prompt: "test"}, func(token string, done bool) error {
		tokens = append(tokens, token)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Text != "Hello world" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello world")
	}
	if len(tokens) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(tokens))
	}
}

func TestLlamaCpp_Health_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b, _ := NewLlamaCpp(Config{URL: srv.URL})
	if err := b.Health(context.Background()); err != nil {
		t.Errorf("Health: %v", err)
	}
}

func TestLlamaCpp_Health_Down(t *testing.T) {
	b, _ := NewLlamaCpp(Config{URL: "http://127.0.0.1:1"})
	err := b.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestLlamaCpp_ListModels(t *testing.T) {
	b, _ := NewLlamaCpp(Config{})
	models, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if models != nil {
		t.Errorf("expected nil for llama.cpp ListModels, got %v", models)
	}
}
