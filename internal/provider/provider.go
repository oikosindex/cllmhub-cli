package provider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oikosindex/cllmhub-cli/internal/backend"
	"github.com/oikosindex/cllmhub-cli/internal/hub"
)

// Provider manages the lifecycle of a published model
type Provider struct {
	id          string
	model       string
	description string
	backend     backend.Backend
	hub         *hub.HubClient

	mu           sync.Mutex
	requestCount int64
	queueDepth   int
	startTime    time.Time

	ctx    context.Context
	cancel context.CancelFunc
}

// Config holds provider configuration
type Config struct {
	Model         string
	Description   string
	MaxConcurrent int
	Token         string
	Backend       backend.Config
	HubURL        string
}

// New creates a new provider instance
func New(cfg Config) (*Provider, error) {
	// Create backend
	b, err := backend.New(cfg.Backend)
	if err != nil {
		return nil, fmt.Errorf("failed to create backend: %w", err)
	}

	// Check backend health
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Health(ctx); err != nil {
		return nil, fmt.Errorf("backend health check failed: %w", err)
	}

	providerID := uuid.New().String()[:8]

	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}

	// Connect to hub via WebSocket
	hubClient, err := hub.Connect(hub.ConnectConfig{
		HubURL:        cfg.HubURL,
		ProviderID:    providerID,
		Model:         cfg.Model,
		Backend:       cfg.Backend.Type,
		Description:   cfg.Description,
		MaxConcurrent: maxConcurrent,
		Token:         cfg.Token,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to hub: %w", err)
	}

	return &Provider{
		id:          providerID,
		model:       cfg.Model,
		description: cfg.Description,
		backend:     b,
		hub:         hubClient,
		startTime:   time.Now(),
	}, nil
}

// Start begins listening for inference requests
func (p *Provider) Start(ctx context.Context) error {
	p.ctx, p.cancel = context.WithCancel(ctx)

	fmt.Printf("✓ Connected to LLMHub network\n")
	fmt.Printf("✓ Model %q published as %s\n", p.model, p.id)
	fmt.Printf("✓ Listening for requests via WebSocket\n")

	// Start heartbeat
	go p.heartbeatLoop()

	// Block on the read loop — dispatches requests to handleRequest
	return p.hub.ReadLoop(p.ctx, p.handleRequest)
}

// Stop gracefully shuts down the provider
func (p *Provider) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.hub != nil {
		p.hub.Close()
	}
}

// handleRequest processes incoming inference requests from the hub
func (p *Provider) handleRequest(req hub.RequestMsg) {
	p.mu.Lock()
	p.queueDepth++
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.queueDepth--
		p.mu.Unlock()
	}()

	start := time.Now()

	backendReq := &backend.Request{
		Prompt:      req.Prompt,
		MaxTokens:   req.Params.MaxTokens,
		Temperature: req.Params.Temperature,
		TopP:        req.Params.TopP,
	}

	if req.Params.Stream {
		p.handleStreamingRequest(req, backendReq, start)
	} else {
		p.handleNonStreamingRequest(req, backendReq, start)
	}
}

func (p *Provider) handleNonStreamingRequest(req hub.RequestMsg, backendReq *backend.Request, start time.Time) {
	resp, err := p.backend.Complete(p.ctx, backendReq)
	if err != nil {
		p.hub.SendError(req.RequestID, fmt.Sprintf("inference failed: %v", err))
		return
	}

	latency := time.Since(start).Milliseconds()

	p.hub.SendResponse(req.RequestID, resp.Text, p.id, latency, hub.Usage{
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
		TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
	})

	p.recordRequest(resp.PromptTokens + resp.CompletionTokens)
}

func (p *Provider) handleStreamingRequest(req hub.RequestMsg, backendReq *backend.Request, start time.Time) {
	tokenIndex := 0

	resp, err := p.backend.Stream(p.ctx, backendReq, func(token string, done bool) error {
		err := p.hub.SendStreamToken(req.RequestID, token, tokenIndex, done, "", nil)
		tokenIndex++
		return err
	})

	if err != nil {
		p.hub.SendError(req.RequestID, fmt.Sprintf("streaming failed: %v", err))
		return
	}

	// Send final token with usage
	usage := &hub.Usage{
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
		TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
	}
	p.hub.SendStreamToken(req.RequestID, "", tokenIndex, true, resp.Text, usage)

	p.recordRequest(resp.PromptTokens + resp.CompletionTokens)
}

func (p *Provider) recordRequest(tokens int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requestCount++
}

func (p *Provider) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send initial heartbeat
	p.sendHeartbeat()

	for {
		select {
		case <-ticker.C:
			p.sendHeartbeat()
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Provider) sendHeartbeat() {
	p.mu.Lock()
	queueDepth := p.queueDepth
	p.mu.Unlock()

	p.hub.SendHeartbeat(queueDepth, 0)
}

// Status returns the current provider status
func (p *Provider) Status() ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	return ProviderStatus{
		ProviderID:   p.id,
		Model:        p.model,
		Status:       "online",
		Uptime:       int64(time.Since(p.startTime).Seconds()),
		RequestCount: p.requestCount,
		QueueDepth:   p.queueDepth,
		GPUUtil:      0,
		Timestamp:    time.Now(),
	}
}

// ProviderStatus represents detailed provider status
type ProviderStatus struct {
	ProviderID   string    `json:"provider_id"`
	Model        string    `json:"model"`
	Status       string    `json:"status"`
	Uptime       int64     `json:"uptime_seconds"`
	RequestCount int64     `json:"request_count"`
	QueueDepth   int       `json:"queue_depth"`
	GPUUtil      float64   `json:"gpu_util"`
	Timestamp    time.Time `json:"timestamp"`
}
