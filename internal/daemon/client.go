package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client communicates with the daemon over the Unix socket.
type Client struct {
	httpClient *http.Client
	sockPath   string
	authToken  string
}

// NewClient creates a daemon client.
func NewClient() (*Client, error) {
	sockPath, err := SocketPath()
	if err != nil {
		return nil, err
	}

	token, err := LoadDaemonToken()
	if err != nil {
		return nil, fmt.Errorf("failed to load daemon auth token: %w", err)
	}

	return &Client{
		sockPath:  sockPath,
		authToken: token,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", sockPath, 5*time.Second)
				},
			},
			Timeout: 10 * time.Second,
		},
	}, nil
}

// IsRunning checks if the daemon is running by verifying the PID file lock.
func IsRunning() (bool, int) {
	pidPath, err := PIDFile()
	if err != nil {
		return false, 0
	}

	f, err := os.Open(pidPath)
	if err != nil {
		return false, 0
	}
	defer f.Close()

	// Try to acquire a non-blocking exclusive lock.
	// If we succeed, no daemon holds the lock — clean up the stale file.
	err = lockFile(f)
	if err == nil {
		// Lock acquired — no daemon is running, stale PID file.
		unlockFile(f)
		os.Remove(pidPath)
		return false, 0
	}

	// Lock is held by another process — daemon is running. Read the PID.
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}

	return true, pid
}

// doRequest creates an HTTP request with the daemon auth token.
func (c *Client) doRequest(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.authToken)
	return c.httpClient.Do(req)
}

// Health checks if the daemon is responding.
func (c *Client) Health() error {
	resp, err := c.httpClient.Get("http://daemon/api/health")
	if err != nil {
		return fmt.Errorf("daemon not responding: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	return nil
}

// Status returns the daemon status.
func (c *Client) Status() (*StatusResponse, error) {
	resp, err := c.doRequest("GET", "http://daemon/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &status, nil
}

// Stop requests the daemon to shut down.
func (c *Client) Stop() error {
	resp, err := c.doRequest("POST", "http://daemon/api/stop", nil)
	if err != nil {
		return fmt.Errorf("cannot connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	return nil
}

// Publish requests the daemon to publish one or more models.
func (c *Client) Publish(specs []PublishModelSpec) (*PublishResponse, error) {
	body, _ := json.Marshal(PublishRequest{Models: specs})
	resp, err := c.doRequest("POST", "http://daemon/api/publish", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("publish failed: %s", string(data))
	}

	var result PublishResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &result, nil
}

// Unpublish requests the daemon to unpublish one or more models.
func (c *Client) Unpublish(modelNames []string) (*PublishResponse, error) {
	body, _ := json.Marshal(UnpublishRequest{Models: modelNames})
	resp, err := c.doRequest("POST", "http://daemon/api/unpublish", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unpublish failed: %s", string(data))
	}

	var result PublishResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &result, nil
}
