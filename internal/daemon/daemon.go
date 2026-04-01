package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/auth"
)

// StatusResponse is returned by GET /api/status.
type StatusResponse struct {
	PID    int           `json:"pid"`
	Uptime int64         `json:"uptime_seconds"`
	Models []ModelStatus `json:"models"`
}

// ModelStatus represents the state of a single model.
type ModelStatus struct {
	Name       string `json:"name"`
	State      string `json:"state"`       // "published", "error"
	Backend    string `json:"backend"`     // "ollama", "vllm", "lmstudio", "mlx", "llamacpp"
	ProviderID string `json:"provider_id"` // cLLMHub provider ID
}

// PublishRequest is the body for POST /api/publish.
type PublishRequest struct {
	Models []PublishModelSpec `json:"models"`
}

// PublishModelSpec describes a model to publish via an external backend.
type PublishModelSpec struct {
	Name          string `json:"name"`
	BackendType   string `json:"backend_type"`              // "ollama", "vllm", "lmstudio", "mlx", "llamacpp"
	BackendURL    string `json:"backend_url,omitempty"`     // override default backend URL
	BackendAPIKey string `json:"backend_api_key,omitempty"`
	MaxConcurrent int    `json:"max_concurrent,omitempty"` // 0 = auto-detect on first request
	Description   string `json:"description,omitempty"`
}

// UnpublishRequest is the body for POST /api/unpublish.
type UnpublishRequest struct {
	Models []string `json:"models"`
}

// PublishResponse is the response for POST /api/publish.
type PublishResponse struct {
	Results []PublishResult `json:"results"`
}

// PublishResult is the result for a single model publish.
type PublishResult struct {
	Model      string `json:"model"`
	Success    bool   `json:"success"`
	Already    bool   `json:"already,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Options holds configuration for the daemon.
type Options struct {
	Watch bool // Proactively watch backend health and unpublish unreachable models
}

// Daemon is the background process that manages bridge services.
type Daemon struct {
	mu        sync.RWMutex
	startTime time.Time
	logger    *slog.Logger
	logFile   *os.File
	watch     bool

	bridges *BridgeManager

	authToken string
	pidFile   *os.File
	listener  net.Listener
	server    *http.Server
	ctx       context.Context
	cancel    context.CancelFunc
}

// New creates a new Daemon instance.
func New(opts Options) *Daemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{
		ctx:    ctx,
		cancel: cancel,
		watch:  opts.Watch,
	}
}

// Run starts the daemon. It blocks until shutdown.
func (d *Daemon) Run() error {
	logger, logFile, err := NewLogger()
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	d.logger = logger
	d.logFile = logFile
	defer logFile.Close()

	d.startTime = time.Now()
	d.bridges = NewBridgeManager(logger, d.watch)

	// Generate and write auth token
	if err := d.writeAuthToken(); err != nil {
		return fmt.Errorf("failed to write auth token: %w", err)
	}
	defer d.removeAuthToken()

	// Write PID file
	if err := d.writePID(); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer d.removePID()

	// Create Unix socket listener
	sockPath, err := SocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	// Remove stale socket if exists
	os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}
	d.listener = listener
	defer func() {
		listener.Close()
		os.Remove(sockPath)
	}()

	// Set socket permissions
	if err := os.Chmod(sockPath, 0600); err != nil {
		d.logger.Warn("failed to set socket permissions", "error", err)
	}

	// Set up HTTP server on Unix socket with auth middleware
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	d.server = &http.Server{Handler: d.authMiddleware(mux)}

	d.logger.Info("daemon started", "pid", os.Getpid(), "socket", sockPath)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in background
	serverErr := make(chan error, 1)
	go func() {
		if err := d.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Wait for shutdown signal or server error
	select {
	case <-sigCh:
		d.logger.Info("received shutdown signal")
	case err := <-serverErr:
		if err != nil {
			d.logger.Error("server error", "error", err)
			return err
		}
	case <-d.ctx.Done():
		d.logger.Info("shutdown requested via API")
	}

	d.shutdown()
	return nil
}

func (d *Daemon) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", d.handleHealth)
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("POST /api/stop", d.handleStop)
	mux.HandleFunc("POST /api/publish", d.handlePublish)
	mux.HandleFunc("POST /api/unpublish", d.handleUnpublish)
	mux.HandleFunc("POST /api/reauth", d.handleReauth)
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := StatusResponse{
		PID:    os.Getpid(),
		Uptime: int64(time.Since(d.startTime).Seconds()),
		Models: []ModelStatus{},
	}

	// Add published models with backend info
	for _, info := range d.bridges.PublishedModelsWithBackend() {
		resp.Models = append(resp.Models, ModelStatus{
			Name:       info.Name,
			State:      "published",
			Backend:    info.Backend,
			ProviderID: info.ProviderID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) handleStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopping"})

	// Trigger shutdown after response is sent
	go func() {
		time.Sleep(100 * time.Millisecond)
		d.cancel()
	}()
}

const maxRequestBodySize = 1024 * 1024 // 1MB

func (d *Daemon) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req PublishRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if len(req.Models) == 0 {
		http.Error(w, `{"error":"no models specified"}`, http.StatusBadRequest)
		return
	}

	// Load credentials
	hubURL, err := auth.LoadHubURL()
	if err != nil {
		http.Error(w, `{"error":"not authenticated — run 'cllmhub login' first"}`, http.StatusUnauthorized)
		return
	}

	token, tokenMgr, err := auth.ResolveTokenManager(hubURL)
	if err != nil {
		d.logger.Error("auth token resolution failed", "error", err)
		http.Error(w, `{"error":"authentication failed — run 'cllmhub login'"}`, http.StatusUnauthorized)
		return
	}

	resp := PublishResponse{Results: make([]PublishResult, 0, len(req.Models))}

	for _, spec := range req.Models {
		result := PublishResult{Model: spec.Name}
		if d.bridges.IsPublished(spec.Name) {
			result.Success = true
			result.Already = true
		} else if err := d.bridges.StartBridge(spec, hubURL, token, tokenMgr); err != nil {
			result.Error = err.Error()
		} else {
			result.Success = true
		}
		resp.Results = append(resp.Results, result)
	}

	// Wait briefly for provider IDs to be assigned by the hub.
	for attempt := 0; attempt < 10; attempt++ {
		allResolved := true
		for _, r := range resp.Results {
			if r.Success && d.bridges.ProviderID(r.Model) == "" {
				allResolved = false
				break
			}
		}
		if allResolved {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Fill in provider IDs.
	for i := range resp.Results {
		if resp.Results[i].Success {
			resp.Results[i].ProviderID = d.bridges.ProviderID(resp.Results[i].Model)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	var req UnpublishRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if len(req.Models) == 0 {
		http.Error(w, `{"error":"no models specified"}`, http.StatusBadRequest)
		return
	}

	resp := PublishResponse{Results: make([]PublishResult, 0, len(req.Models))}
	for _, name := range req.Models {
		result := PublishResult{Model: name}
		if err := d.bridges.StopBridge(name); err != nil {
			result.Error = err.Error()
		} else {
			result.Success = true
		}
		resp.Results = append(resp.Results, result)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Daemon) handleReauth(w http.ResponseWriter, r *http.Request) {
	published := d.bridges.PublishedModels()
	if len(published) > 0 {
		d.logger.Info("reauth: stopping all bridges for credential refresh", "models", published)
		d.bridges.StopAll()
	}

	d.logger.Info("reauth: credentials refreshed, ready for new publishes")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ok",
		"unpublished": published,
	})
}

func (d *Daemon) shutdown() {
	d.logger.Info("shutting down daemon")

	// Stop all bridges
	d.bridges.StopAll()

	// Gracefully shut down HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := d.server.Shutdown(shutdownCtx); err != nil {
		d.logger.Error("server shutdown error", "error", err)
	}

	d.logger.Info("daemon stopped")
}

func (d *Daemon) writePID() error {
	pidPath, err := PIDFile()
	if err != nil {
		return err
	}

	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("cannot open PID file: %w", err)
	}

	// Acquire exclusive advisory lock — prevents two daemons from running simultaneously.
	if err := lockFile(f); err != nil {
		f.Close()
		return fmt.Errorf("another daemon is already running (cannot lock PID file)")
	}

	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		f.Close()
		return fmt.Errorf("cannot write PID: %w", err)
	}

	// Keep the file open (and locked) for the lifetime of the daemon.
	d.pidFile = f
	return nil
}

func (d *Daemon) removePID() {
	if d.pidFile != nil {
		unlockFile(d.pidFile)
		d.pidFile.Close()
	}
	pidPath, err := PIDFile()
	if err != nil {
		return
	}
	os.Remove(pidPath)
}

func (d *Daemon) writeAuthToken() error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("cannot generate auth token: %w", err)
	}
	d.authToken = hex.EncodeToString(b)

	tokenPath, err := DaemonTokenPath()
	if err != nil {
		return err
	}
	return os.WriteFile(tokenPath, []byte(d.authToken), 0600)
}

func (d *Daemon) removeAuthToken() {
	tokenPath, err := DaemonTokenPath()
	if err != nil {
		return
	}
	os.Remove(tokenPath)
}

func (d *Daemon) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoint is unauthenticated for simple liveness checks.
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(d.authToken)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoadDaemonToken reads the daemon auth token from disk.
func LoadDaemonToken() (string, error) {
	tokenPath, err := DaemonTokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", fmt.Errorf("cannot read daemon token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}
