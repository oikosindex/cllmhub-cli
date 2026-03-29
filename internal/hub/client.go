package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// pinnedCertFingerprints contains SHA-256 fingerprints of trusted hub TLS
// certificates. If non-empty, connections are rejected unless the server
// certificate matches one of these fingerprints.
var pinnedCertFingerprints []string

// SetPinnedCertFingerprints configures TLS certificate pinning for hub connections.
// Each fingerprint should be a hex-encoded SHA-256 hash of the DER-encoded certificate.
func SetPinnedCertFingerprints(fingerprints []string) {
	pinnedCertFingerprints = fingerprints
}

func pinnedTLSConfig() *tls.Config {
	if len(pinnedCertFingerprints) == 0 {
		return nil
	}
	return &tls.Config{
		VerifyConnection: func(cs tls.ConnectionState) error {
			for _, cert := range cs.PeerCertificates {
				fingerprint := fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
				for _, pinned := range pinnedCertFingerprints {
					if fingerprint == pinned {
						return nil
					}
				}
			}
			return fmt.Errorf("TLS certificate does not match any pinned fingerprint")
		},
	}
}

// WebSocket message types (must match gateway/internal/provider/messages.go)
const (
	MsgTypeRegister    = "register"
	MsgTypeHeartbeat   = "heartbeat"
	MsgTypeResponse    = "response"
	MsgTypeStreamToken = "stream_token"
	MsgTypeError       = "error"
	MsgTypeRegistered  = "registered"
	MsgTypeRequest     = "request"
	MsgTypePing        = "ping"
)

// Envelope is used to peek at the message type.
type Envelope struct {
	Type string `json:"type"`
}

// RequestMsg is a forwarded inference request from the gateway.
type RequestMsg struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Params    InferenceParams `json:"params"`
}

// InferenceParams mirrors the gateway params.
type InferenceParams struct {
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	Stream      bool    `json:"stream,omitempty"`
}

// Usage contains token usage information.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// TokenFunc returns a fresh access token. Used by SendAlert to avoid stale tokens.
type TokenFunc func() string

// HubClient manages the WebSocket connection to the gateway for providers.
type HubClient struct {
	hubURL        string
	providerID    string
	model         string
	backend       string
	description   string
	maxConcurrent int
	token         string
	tokenFunc     TokenFunc

	ws   *websocket.Conn
	wsMu sync.Mutex
}

// ConnectConfig holds parameters for connecting to the hub.
type ConnectConfig struct {
	HubURL        string
	ProviderID    string
	Model         string
	Backend       string
	Description   string
	MaxConcurrent int
	Token         string
}

// Connect dials the gateway WebSocket, sends a register message, and waits for confirmation.
func Connect(cfg ConnectConfig) (*HubClient, error) {
	u, err := url.Parse(cfg.HubURL)
	if err != nil {
		return nil, fmt.Errorf("invalid hub URL: %w", err)
	}

	// Convert http(s) to ws(s)
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already correct
	default:
		u.Scheme = "ws"
	}
	u.Path = "/provider/ws"

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
		TLSClientConfig:  pinnedTLSConfig(),
	}
	ws, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to hub: %w", err)
	}

	c := &HubClient{
		hubURL:        cfg.HubURL,
		providerID:    cfg.ProviderID,
		model:         cfg.Model,
		backend:       cfg.Backend,
		description:   cfg.Description,
		maxConcurrent: cfg.MaxConcurrent,
		token:         cfg.Token,
		ws:            ws,
	}

	// Send register message.
	reg := map[string]interface{}{
		"type":           MsgTypeRegister,
		"provider_id":    cfg.ProviderID,
		"model":          cfg.Model,
		"backend":        cfg.Backend,
		"price":          0,
		"description":    cfg.Description,
		"max_concurrent": cfg.MaxConcurrent,
		"token":          cfg.Token,
	}

	if err := c.writeJSON(reg); err != nil {
		ws.Close()
		return nil, fmt.Errorf("failed to send register: %w", err)
	}

	// Wait for registered confirmation.
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		ws.Close()
		return nil, fmt.Errorf("failed to read register response: %w", err)
	}
	ws.SetReadDeadline(time.Time{})

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		ws.Close()
		return nil, fmt.Errorf("failed to parse register response: %w", err)
	}
	if env.Type == MsgTypeError {
		var errMsg struct {
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &errMsg)
		ws.Close()
		return nil, fmt.Errorf("registration failed: %s", errMsg.Message)
	}
	if env.Type != MsgTypeRegistered {
		ws.Close()
		return nil, fmt.Errorf("unexpected response type: %s", env.Type)
	}

	// Limit inbound WebSocket messages to 16MB to prevent memory exhaustion.
	ws.SetReadLimit(16 * 1024 * 1024)

	return c, nil
}

// ReadLoop reads messages from the WebSocket and dispatches requests to the callback.
// It blocks until the context is cancelled or the connection is closed.
func (c *HubClient) ReadLoop(ctx context.Context, onRequest func(req RequestMsg), onPing func()) error {
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		c.ws.Close()
		close(done)
	}()

	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return fmt.Errorf("ws read error: %w", err)
			}
		}

		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}

		switch env.Type {
		case MsgTypeRequest:
			var req RequestMsg
			if err := json.Unmarshal(raw, &req); err != nil {
				log.Printf("invalid request message: %v", err)
				continue
			}
			go onRequest(req)
		case MsgTypePing:
			if onPing != nil {
				onPing()
			}
		}
	}
}

// SendResponse sends a non-streaming response back to the gateway.
func (c *HubClient) SendResponse(requestID, text, providerID string, latencyMs int64, usage Usage) error {
	msg := map[string]interface{}{
		"type":        MsgTypeResponse,
		"request_id":  requestID,
		"text":        text,
		"provider_id": providerID,
		"latency_ms":  latencyMs,
		"usage":       usage,
	}
	return c.writeJSON(msg)
}

// SendStreamToken sends a single streaming token back to the gateway.
func (c *HubClient) SendStreamToken(requestID, token string, index int, done bool, fullText string, usage *Usage) error {
	msg := map[string]interface{}{
		"type":       MsgTypeStreamToken,
		"request_id": requestID,
		"token":      token,
		"index":      index,
		"done":       done,
	}
	if fullText != "" {
		msg["text"] = fullText
	}
	if usage != nil {
		msg["usage"] = usage
	}
	return c.writeJSON(msg)
}

// SendError sends an error for a specific request back to the gateway.
func (c *HubClient) SendError(requestID, message string) error {
	msg := map[string]interface{}{
		"type":       MsgTypeError,
		"request_id": requestID,
		"message":    message,
	}
	return c.writeJSON(msg)
}

// UpdateMaxConcurrent notifies the hub of a new max_concurrent value.
func (c *HubClient) UpdateMaxConcurrent(maxConcurrent int) error {
	c.maxConcurrent = maxConcurrent
	msg := map[string]interface{}{
		"type":           MsgTypeHeartbeat,
		"provider_id":    c.providerID,
		"model":          c.model,
		"max_concurrent": maxConcurrent,
	}
	return c.writeJSON(msg)
}

// SendHeartbeat sends a heartbeat to the gateway.
func (c *HubClient) SendHeartbeat(queueDepth int, gpuUtil float64) error {
	return c.SendHeartbeatWithToken(queueDepth, gpuUtil, "")
}

// SendHeartbeatWithToken sends a heartbeat that includes a fresh access token.
// When token is non-empty, the gateway uses it to update the session credential.
func (c *HubClient) SendHeartbeatWithToken(queueDepth int, gpuUtil float64, token string) error {
	msg := map[string]interface{}{
		"type":        MsgTypeHeartbeat,
		"provider_id": c.providerID,
		"model":       c.model,
		"queue_depth": queueDepth,
		"gpu_util":    gpuUtil,
	}
	if token != "" {
		msg["token"] = token
	}
	return c.writeJSON(msg)
}

// Alert represents a CLI alert sent to the gateway.
type Alert struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	AlertType  string `json:"alert_type"`
	Message    string `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

// SendAlert posts an alert to the gateway's /api/cli-alerts endpoint.
func (c *HubClient) SendAlert(alert Alert) {
	body, err := json.Marshal(alert)
	if err != nil {
		log.Printf("failed to marshal alert: %v", err)
		return
	}

	u, err := url.Parse(c.hubURL)
	if err != nil {
		log.Printf("failed to parse hub URL for alert: %v", err)
		return
	}
	u.Path = "/api/cli-alerts"

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
	if err != nil {
		log.Printf("failed to create alert request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if t := c.currentToken(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("failed to send alert: %v", err)
		return
	}
	resp.Body.Close()
}

// SetTokenFunc sets a callback to retrieve fresh tokens for HTTP requests (e.g. alerts).
func (c *HubClient) SetTokenFunc(fn TokenFunc) {
	c.tokenFunc = fn
}

// currentToken returns the freshest available token.
func (c *HubClient) currentToken() string {
	if c.tokenFunc != nil {
		if t := c.tokenFunc(); t != "" {
			return t
		}
	}
	return c.token
}

// Close closes the WebSocket connection.
func (c *HubClient) Close() {
	if c.ws != nil {
		c.ws.Close()
	}
}

func (c *HubClient) writeJSON(v interface{}) error {
	c.wsMu.Lock()
	defer c.wsMu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.ws.WriteJSON(v)
}

