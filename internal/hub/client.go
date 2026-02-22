package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

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

// HubClient manages the WebSocket connection to the gateway for providers.
type HubClient struct {
	hubURL        string
	providerID    string
	model         string
	backend       string
	description   string
	maxConcurrent int
	token         string

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

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
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

	return c, nil
}

// ReadLoop reads messages from the WebSocket and dispatches requests to the callback.
// It blocks until the context is cancelled or the connection is closed.
func (c *HubClient) ReadLoop(ctx context.Context, onRequest func(req RequestMsg)) error {
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
			// No response needed; just keeps the connection alive.
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

// SendHeartbeat sends a heartbeat to the gateway.
func (c *HubClient) SendHeartbeat(queueDepth int, gpuUtil float64) error {
	msg := map[string]interface{}{
		"type":        MsgTypeHeartbeat,
		"provider_id": c.providerID,
		"model":       c.model,
		"queue_depth": queueDepth,
		"gpu_util":    gpuUtil,
	}
	return c.writeJSON(msg)
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
