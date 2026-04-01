package provider

import (
	"context"
	"fmt"
	"log"
	"log/slog"
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

	autoDetectSlots bool      // true when MaxConcurrent was 0 (auto-detect)
	slotsOnce       sync.Once // lazy-detect concurrent slots on first request
	watch           bool      // proactively watch backend health

	ctx    context.Context
	cancel context.CancelFunc

	audit    *audit.Logger
	limiter  *rate.Limiter
	tokenMgr *auth.TokenManager
	logger   *slog.Logger
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
	Logger        *slog.Logger // optional; if nil, prints to stdout
	Watch         bool         // proactively watch backend health
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

	// MaxConcurrent 0 means auto-detect on first request; register with 1 initially.
	maxConcurrent := cfg.MaxConcurrent
	registerConcurrent := maxConcurrent
	if registerConcurrent < 1 {
		registerConcurrent = 1
	}

	hubCfg := hub.ConnectConfig{
		HubURL:        cfg.HubURL,
		ProviderID:    providerID,
		Model:         cfg.Model,
		Backend:       cfg.Backend.Type,
		Description:   cfg.Description,
		MaxConcurrent: registerConcurrent,
		Token:         cfg.Token,
	}

	// Connect to hub via WebSocket
	hubClient, err := hub.Connect(hubCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to hub: %w", err)
	}

	p := &Provider{
		id:              providerID,
		model:           cfg.Model,
		description:     cfg.Description,
		backend:         b,
		hub:             hubClient,
		hubCfg:          hubCfg,
		startTime:       time.Now(),
		modelServerUp:   true,
		autoDetectSlots: maxConcurrent < 1,
		watch:           cfg.Watch,
		tokenMgr:        cfg.TokenManager,
		logger:          cfg.Logger,
	}

	// Give the hub client access to fresh tokens for HTTP requests (alerts).
	if cfg.TokenManager != nil {
		hubClient.SetTokenFunc(cfg.TokenManager.AccessToken)
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

	p.logf("✓ Connected to cLLMHub network\n")
	p.logf("✓ Model %q published as %s (max concurrent: %d)\n", p.model, p.id, p.hubCfg.MaxConcurrent)
	p.logf("✓ Listening for requests via WebSocket\n")

	// Watch for token manager death and shut down the provider.
	if p.tokenMgr != nil {
		go func() {
			select {
			case <-p.tokenMgr.Dead:
				p.logf("\n✗ Authentication expired — run 'cllmhub login' and restart\n")
				p.cancel()
			case <-p.ctx.Done():
			}
		}()
	}

	// Start proactive health check loop — detects backend going down
	// even when no requests are flowing. Only runs with --watch flag.
	if p.watch {
		go p.healthCheckLoop()
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

		// If the model server is down, onModelServerDown is handling
		// recovery — don't reconnect here or we'd re-publish a dead model.
		p.mu.Lock()
		up := p.modelServerUp
		p.mu.Unlock()
		if !up {
			// Wait for recovery (onModelServerDown) or shutdown.
			<-p.ctx.Done()
			return p.ctx.Err()
		}

		// Connection dropped unexpectedly — attempt to reconnect.
		p.logf("\n⚠ Connection lost: %v\n", err)
		p.logf("  Will attempt to reconnect every 60 seconds...\n")

		if !p.reconnectLoop() {
			if p.ctx.Err() != nil {
				return p.ctx.Err()
			}
			return fmt.Errorf("failed to reconnect after %d attempts", maxReconnectAttempts)
		}
	}
}

const (
	maxReconnectAttempts       = 5
	maxHealthCheckAttempts     = 2
	healthCheckInterval        = 60 * time.Second
	proactiveHealthInterval    = 30 * time.Second
)

// reconnectLoop tries to re-establish the hub WebSocket.
// Attempts immediately, then waits 60 seconds between retries.
// Returns true on success, false if the context was cancelled or attempts exhausted.
func (p *Provider) reconnectLoop() bool {
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		// Wait before retrying (but not before the first attempt).
		if attempt > 1 {
			select {
			case <-p.ctx.Done():
				return false
			case <-time.After(60 * time.Second):
			}
		}

		select {
		case <-p.ctx.Done():
			return false
		default:
		}

		p.logf("⚠ Reconnect attempt %d/%d...\n", attempt, maxReconnectAttempts)

		// Use a fresh token if available.
		cfg := p.hubCfg
		if p.tokenMgr != nil {
			if t := p.tokenMgr.AccessToken(); t != "" {
				cfg.Token = t
			}
		}

		newClient, err := hub.Connect(cfg)
		if err != nil {
			p.logf("⚠ Reconnect failed: %v\n", err)
			continue
		}

		p.hub = newClient
		p.logf("✓ Reconnected to cLLMHub network\n")
		return true
	}

	p.logf("✗ Failed to reconnect after %d attempts, giving up\n", maxReconnectAttempts)
	return false
}

// healthCheckLoop periodically pings the backend to detect it going down
// even when no inference requests are flowing.
func (p *Provider) healthCheckLoop() {
	ticker := time.NewTicker(proactiveHealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			up := p.modelServerUp
			p.mu.Unlock()
			if !up {
				continue // already in recovery, skip
			}

			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			err := p.backend.Health(ctx)
			cancel()

			if err != nil {
				p.logf("⚠ Proactive health check failed: %v\n", err)
				go p.onModelServerDown()
			}
		}
	}
}

// onModelServerDown is triggered when a backend request fails with a connection error.
// It unpublishes the model immediately, runs health checks, and either republishes or stays unpublished.
func (p *Provider) onModelServerDown() {
	p.mu.Lock()
	if !p.modelServerUp {
		p.mu.Unlock()
		return // already in recovery
	}
	p.modelServerUp = false
	p.mu.Unlock()

	p.logf("\n⚠ Model server unreachable: %s\n", p.backend.URL())

	// Unpublish: close the hub WebSocket so the model is no longer available.
	// Close first so the model is removed from the hub immediately.
	p.logf("⚠ Unpublishing model %q\n", p.model)
	p.hub.Close()

	// Alert: model_server_unreachable (async — don't delay unpublish)
	go p.hub.SendAlert(hub.Alert{
		ProviderID: p.id,
		Model:      p.model,
		AlertType:  "model_server_unreachable",
		Message:    fmt.Sprintf("Model server unreachable at %s, unpublishing model", p.backend.URL()),
		Timestamp:  time.Now(),
	})

	p.logf("  Will check again — %d attempts, 60 seconds apart...\n", maxHealthCheckAttempts)

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
				p.logf("⚠ Health check %d/%d... failed\n", attempt, maxHealthCheckAttempts)
				continue
			}

			// Backend recovered — republish by reconnecting to hub.
			p.logf("✓ Model server recovered, republishing...\n")

			cfg := p.hubCfg
			if p.tokenMgr != nil {
				if t := p.tokenMgr.AccessToken(); t != "" {
					cfg.Token = t
				}
			}

			newClient, err := hub.Connect(cfg)
			if err != nil {
				p.logf("✗ Failed to republish: %v\n", err)
				continue
			}

			p.hub = newClient
			if p.tokenMgr != nil {
				p.hub.SetTokenFunc(p.tokenMgr.AccessToken)
			}

			p.mu.Lock()
			p.modelServerUp = true
			p.mu.Unlock()

			p.logf("✓ Model %q republished\n", p.model)

			p.hub.SendAlert(hub.Alert{
				ProviderID: p.id,
				Model:      p.model,
				AlertType:  "model_server_recovered",
				Message:    "Model server recovered, model republished",
				Timestamp:  time.Now(),
			})

			// Send a heartbeat so the provider is immediately visible again.
			p.sendHeartbeat()
			return
		}
	}

	// All attempts failed — stay unpublished.
	p.logf("✗ Model server down after %d attempts, staying unpublished\n", maxHealthCheckAttempts)

	p.hub.SendAlert(hub.Alert{
		ProviderID: p.id,
		Model:      p.model,
		AlertType:  "model_server_down",
		Message:    fmt.Sprintf("Model server down after %d attempts, model stays unpublished", maxHealthCheckAttempts),
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
	if p.hub != nil {
		// Send unregister while the WebSocket is still open.
		p.logf("⚠ Unregistering model %q (provider %s)\n", p.model, p.id)
		if err := p.hub.SendUnpublish(); err != nil {
			p.logf("✗ Failed to send unregister: %v\n", err)
		} else {
			p.logf("✓ Unregister message sent for model %q\n", p.model)
		}
	}
	// Cancel the context so ReadLoop and Start() know this is a
	// deliberate shutdown and don't attempt to reconnect.
	if p.cancel != nil {
		p.cancel()
	}
	if p.hub != nil {
		p.hub.Disconnect()
		p.logf("✓ Disconnected from hub\n")
	}
	if p.tokenMgr != nil {
		p.tokenMgr.Stop()
	}
	p.audit.Close()
}

// detectSlots probes the backend for concurrent slot count and updates the hub.
func (p *Provider) detectSlots() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slots, err := p.backend.ConcurrentSlots(ctx)
	if err != nil || slots <= 1 {
		return
	}

	p.logf("✓ Detected %d concurrent slots\n", slots)
	p.hubCfg.MaxConcurrent = slots
	if err := p.hub.UpdateMaxConcurrent(slots); err != nil {
		p.logf("⚠ Failed to update hub with detected slots: %v\n", err)
	}
}

// handleRequest processes incoming inference requests from the hub
func (p *Provider) handleRequest(req hub.RequestMsg) {
	// Lazy-detect concurrent slots on first request.
	if p.autoDetectSlots {
		p.slotsOnce.Do(func() {
			go p.detectSlots()
		})
	}

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
		Messages:    req.Messages,
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

	// Don't send done=true in the per-token callback; we send the final
	// done message after the loop with full text and usage attached.
	resp, err := p.backend.Stream(p.ctx, backendReq, func(token string, done bool) error {
		if done {
			return nil // skip — final message sent below
		}
		err := p.hub.SendStreamToken(req.RequestID, token, tokenIndex, false, "", nil)
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

	// Send single final message with usage
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

// logf prints to stdout or logs via slog if a logger is configured.
func (p *Provider) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Info(fmt.Sprintf(format, args...), "model", p.model, "provider_id", p.id)
	} else {
		fmt.Printf(format, args...)
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
