package provider

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/cllmhub/cllmhub-cli/internal/audit"
	"github.com/cllmhub/cllmhub-cli/internal/auth"
	"github.com/cllmhub/cllmhub-cli/internal/backend"
	"github.com/cllmhub/cllmhub-cli/internal/hub"
	"golang.org/x/time/rate"
)

// Provider manages the lifecycle of a published model
type Provider struct {
	id          string
	model       string
	description string
	backend     backend.Backend
	hub         *hub.HubClient
	hubCfg      hub.ConnectConfig

	mu             sync.Mutex
	requestCount   int64
	queueDepth     int
	startTime      time.Time
	modelServerUp  bool

	ctx    context.Context
	cancel context.CancelFunc

	audit    *audit.Logger
	limiter  *rate.Limiter
	tokenMgr *auth.TokenManager
}

// Config holds provider configuration
type Config struct {
	Model         string
	Description   string
	MaxConcurrent int
	Token         string
	Backend       backend.Config
	HubURL        string
	LogFile       string
	RateLimit     int // requests per minute, 0 = unlimited
	TokenManager  *auth.TokenManager
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

	hubCfg := hub.ConnectConfig{
		HubURL:        cfg.HubURL,
		ProviderID:    providerID,
		Model:         cfg.Model,
		Backend:       cfg.Backend.Type,
		Description:   cfg.Description,
		MaxConcurrent: maxConcurrent,
		Token:         cfg.Token,
	}

	// Connect to hub via WebSocket
	hubClient, err := hub.Connect(hubCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to hub: %w", err)
	}

	p := &Provider{
		id:            providerID,
		model:         cfg.Model,
		description:   cfg.Description,
		backend:       b,
		hub:           hubClient,
		hubCfg:        hubCfg,
		startTime:      time.Now(),
		modelServerUp:  true,
		tokenMgr:       cfg.TokenManager,
	}

	// Set up audit logger
	if cfg.LogFile != "" {
		logger, err := audit.NewLogger(cfg.LogFile)
		if err != nil {
			hubClient.Close()
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		p.audit = logger
	}

	// Set up rate limiter
	if cfg.RateLimit > 0 {
		rps := float64(cfg.RateLimit) / 60.0
		p.limiter = rate.NewLimiter(rate.Limit(rps), cfg.RateLimit)
	}

	return p, nil
}

// Start begins listening for inference requests.
// If the WebSocket connection drops, it automatically reconnects once per minute.
func (p *Provider) Start(ctx context.Context) error {
	p.ctx, p.cancel = context.WithCancel(ctx)

	fmt.Printf("✓ Connected to LLMHub network\n")
	fmt.Printf("✓ Model %q published as %s (max concurrent: %d)\n", p.model, p.id, p.hubCfg.MaxConcurrent)
	fmt.Printf("✓ Listening for requests via WebSocket\n")

	// Watch for token manager death and shut down the provider.
	if p.tokenMgr != nil {
		go func() {
			select {
			case <-p.tokenMgr.Dead:
				fmt.Printf("\n✗ Authentication expired — run 'cllmhub login' and restart\n")
				p.cancel()
			case <-p.ctx.Done():
			}
		}()
	}

	// Send initial heartbeat so the provider is immediately visible.
	p.sendHeartbeat()

	for {
		// Block on the read loop — dispatches requests to handleRequest.
		// On each hub ping, reply with a heartbeat to refresh the provider TTL.
		err := p.hub.ReadLoop(p.ctx, p.handleRequest, p.sendHeartbeat)

		// If the parent context was cancelled, this is a deliberate shutdown.
		if p.ctx.Err() != nil {
			return err
		}

		// Connection dropped unexpectedly — attempt to reconnect.
		fmt.Printf("\n⚠ Connection lost: %v\n", err)
		fmt.Printf("  Will attempt to reconnect every 60 seconds...\n")

		if !p.reconnectLoop() {
			if p.ctx.Err() != nil {
				return p.ctx.Err()
			}
			return fmt.Errorf("failed to reconnect after %d attempts", maxReconnectAttempts)
		}
	}
}

const (
	maxReconnectAttempts    = 5
	maxHealthCheckAttempts  = 5
	healthCheckInterval     = 60 * time.Second
)

// reconnectLoop tries to re-establish the hub WebSocket once per minute.
// Returns true on success, false if the context was cancelled or attempts exhausted.
func (p *Provider) reconnectLoop() bool {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		select {
		case <-p.ctx.Done():
			return false
		case <-ticker.C:
			fmt.Printf("⚠ Reconnect attempt %d/%d...\n", attempt, maxReconnectAttempts)

			// Use a fresh token if available.
			cfg := p.hubCfg
			if p.tokenMgr != nil {
				if t := p.tokenMgr.AccessToken(); t != "" {
					cfg.Token = t
				}
			}

			newClient, err := hub.Connect(cfg)
			if err != nil {
				fmt.Printf("⚠ Reconnect failed: %v\n", err)
				continue
			}

			p.hub = newClient
			fmt.Printf("✓ Reconnected to LLMHub network\n")
			return true
		}
	}

	fmt.Printf("✗ Failed to reconnect after %d attempts, giving up\n", maxReconnectAttempts)
	return false
}

// onModelServerDown is triggered when a backend request fails with a connection error.
// It sends an alert, runs health checks, and either recovers or shuts down.
func (p *Provider) onModelServerDown() {
	p.mu.Lock()
	if !p.modelServerUp {
		p.mu.Unlock()
		return // already in recovery
	}
	p.modelServerUp = false
	p.mu.Unlock()

	fmt.Printf("\n⚠ Model server unreachable: %s\n", p.backend.URL())
	fmt.Printf("  Attempting recovery — %d checks, 60 seconds apart...\n", maxHealthCheckAttempts)

	// Alert 1: model_server_unreachable
	p.hub.SendAlert(hub.Alert{
		ProviderID: p.id,
		Model:      p.model,
		AlertType:  "model_server_unreachable",
		Message:    fmt.Sprintf("Model server unreachable at %s, attempting recovery (%d attempts)", p.backend.URL(), maxHealthCheckAttempts),
		Timestamp:  time.Now(),
	})

	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for attempt := 1; attempt <= maxHealthCheckAttempts; attempt++ {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			err := p.backend.Health(ctx)
			cancel()

			if err != nil {
				fmt.Printf("⚠ Health check %d/%d... failed\n", attempt, maxHealthCheckAttempts)
				continue
			}

			// Recovered
			p.mu.Lock()
			p.modelServerUp = true
			p.mu.Unlock()

			fmt.Printf("✓ Model server recovered\n")

			// Alert 3: model_server_recovered
			p.hub.SendAlert(hub.Alert{
				ProviderID: p.id,
				Model:      p.model,
				AlertType:  "model_server_recovered",
				Message:    "Model server recovered",
				Timestamp:  time.Now(),
			})
			return
		}
	}

	// All attempts failed
	fmt.Printf("✗ Model server down after %d attempts, shutting down\n", maxHealthCheckAttempts)

	// Alert 2: model_server_down
	p.hub.SendAlert(hub.Alert{
		ProviderID: p.id,
		Model:      p.model,
		AlertType:  "model_server_down",
		Message:    fmt.Sprintf("Model server down after %d attempts, provider shutting down", maxHealthCheckAttempts),
		Timestamp:  time.Now(),
	})

	p.Stop()
}

// CloseConnection closes the current WebSocket without stopping the provider,
// allowing the reconnect loop in Start to re-establish the connection.
func (p *Provider) CloseConnection() {
	if p.hub != nil {
		p.hub.Close()
	}
}

// Stop gracefully shuts down the provider
func (p *Provider) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.hub != nil {
		p.hub.Close()
	}
	if p.tokenMgr != nil {
		p.tokenMgr.Stop()
	}
	p.audit.Close()
}

// handleRequest processes incoming inference requests from the hub
func (p *Provider) handleRequest(req hub.RequestMsg) {
	// Reject requests while model server is down
	p.mu.Lock()
	up := p.modelServerUp
	p.mu.Unlock()
	if !up {
		p.hub.SendError(req.RequestID, "model server temporarily unavailable")
		return
	}

	// Rate limit check
	if p.limiter != nil && !p.limiter.Allow() {
		p.hub.SendError(req.RequestID, "rate limit exceeded")
		p.audit.Log(audit.Entry{
			RequestID: req.RequestID,
			Model:     req.Model,
			Stream:    req.Params.Stream,
			Error:     "rate limit exceeded",
		})
		return
	}

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

// sanitizeError logs the full error locally and returns a generic message for the hub.
func sanitizeError(requestID string, err error) string {
	log.Printf("[%s] backend error: %v", requestID, err)
	return "internal backend error"
}

func (p *Provider) handleNonStreamingRequest(req hub.RequestMsg, backendReq *backend.Request, start time.Time) {
	resp, err := p.backend.Complete(p.ctx, backendReq)
	if err != nil {
		if backend.IsConnectionError(err) {
			p.hub.SendError(req.RequestID, "model server temporarily unavailable")
			go p.onModelServerDown()
			return
		}
		msg := sanitizeError(req.RequestID, err)
		p.hub.SendError(req.RequestID, msg)
		p.audit.Log(audit.Entry{
			RequestID: req.RequestID,
			Model:     req.Model,
			Stream:    false,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     msg,
		})
		return
	}

	latency := time.Since(start).Milliseconds()

	p.hub.SendResponse(req.RequestID, resp.Text, p.id, latency, hub.Usage{
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
		TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
	})

	tokens := resp.PromptTokens + resp.CompletionTokens
	p.recordRequest(tokens)
	p.audit.Log(audit.Entry{
		RequestID: req.RequestID,
		Model:     req.Model,
		Stream:    false,
		LatencyMs: latency,
		Tokens:    tokens,
	})
}

func (p *Provider) handleStreamingRequest(req hub.RequestMsg, backendReq *backend.Request, start time.Time) {
	tokenIndex := 0

	resp, err := p.backend.Stream(p.ctx, backendReq, func(token string, done bool) error {
		err := p.hub.SendStreamToken(req.RequestID, token, tokenIndex, done, "", nil)
		tokenIndex++
		return err
	})

	if err != nil {
		if backend.IsConnectionError(err) {
			p.hub.SendError(req.RequestID, "model server temporarily unavailable")
			go p.onModelServerDown()
			return
		}
		msg := sanitizeError(req.RequestID, err)
		p.hub.SendError(req.RequestID, msg)
		p.audit.Log(audit.Entry{
			RequestID: req.RequestID,
			Model:     req.Model,
			Stream:    true,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     msg,
		})
		return
	}

	// Send final token with usage
	usage := &hub.Usage{
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
		TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
	}
	p.hub.SendStreamToken(req.RequestID, "", tokenIndex, true, resp.Text, usage)

	tokens := resp.PromptTokens + resp.CompletionTokens
	latency := time.Since(start).Milliseconds()
	p.recordRequest(tokens)
	p.audit.Log(audit.Entry{
		RequestID: req.RequestID,
		Model:     req.Model,
		Stream:    true,
		LatencyMs: latency,
		Tokens:    tokens,
	})
}

func (p *Provider) recordRequest(tokens int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requestCount++
}

func (p *Provider) sendHeartbeat() {
	p.mu.Lock()
	queueDepth := p.queueDepth
	p.mu.Unlock()

	var token string
	if p.tokenMgr != nil {
		token = p.tokenMgr.AccessToken()
	}
	p.hub.SendHeartbeatWithToken(queueDepth, 0, token)
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
