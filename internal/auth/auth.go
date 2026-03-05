package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	dirName         = ".cllmhub"
	credentialsFile = "credentials"
)

type credentials struct {
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	HubURL       string    `json:"hub_url,omitempty"`
}

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, dirName, credentialsFile), nil
}

func ensureCredentialsDir() (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create credentials directory: %w", err)
	}
	return path, nil
}

// SaveOAuthCredentials stores OAuth tokens in ~/.cllmhub/credentials.
func SaveOAuthCredentials(hubURL, accessToken, refreshToken, tokenType string, expiresAt time.Time) error {
	return SaveCredentials(credentials{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		ExpiresAt:    expiresAt,
		HubURL:       hubURL,
	})
}

// LoadHubURL returns the hub URL stored in credentials, or empty string if not set.
func LoadHubURL() string {
	creds, err := LoadCredentials()
	if err != nil {
		return ""
	}
	return creds.HubURL
}

// SaveCredentials writes the credentials struct to disk.
func SaveCredentials(creds credentials) error {
	path, err := ensureCredentialsDir()
	if err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write credentials: %w", err)
	}
	return nil
}

// LoadCredentials reads the full credentials struct from disk.
func LoadCredentials() (credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return credentials{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, err
	}
	var creds credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return credentials{}, fmt.Errorf("invalid credentials file: %w", err)
	}
	return creds, nil
}

// LoadToken reads the access token from ~/.cllmhub/credentials.
func LoadToken() (string, error) {
	creds, err := LoadCredentials()
	if err != nil {
		return "", err
	}
	if creds.AccessToken == "" {
		return "", fmt.Errorf("credentials file contains no token")
	}
	return creds.AccessToken, nil
}

// LoadRefreshToken reads the refresh token from credentials.
func LoadRefreshToken() (string, error) {
	creds, err := LoadCredentials()
	if err != nil {
		return "", err
	}
	if creds.RefreshToken == "" {
		return "", fmt.Errorf("no refresh token in credentials")
	}
	return creds.RefreshToken, nil
}

// RemoveCredentials deletes the credentials file.
func RemoveCredentials() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove credentials: %w", err)
	}
	return nil
}

// TokenManager handles automatic access token refresh in the background.
// It is safe for concurrent use.
type TokenManager struct {
	hubURL string

	mu          sync.RWMutex
	accessToken string
	refreshTok  string
	expiresAt   time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTokenManager creates a TokenManager that will refresh the access token
// 5 minutes before expiry. Call Stop() when done.
func NewTokenManager(hubURL, accessToken, refreshToken string, expiresAt time.Time) *TokenManager {
	ctx, cancel := context.WithCancel(context.Background())
	tm := &TokenManager{
		hubURL:      hubURL,
		accessToken: accessToken,
		refreshTok:  refreshToken,
		expiresAt:   expiresAt,
		ctx:         ctx,
		cancel:      cancel,
	}
	tm.wg.Add(1)
	go tm.refreshLoop()
	return tm
}

// AccessToken returns the current access token.
func (tm *TokenManager) AccessToken() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.accessToken
}

// Stop shuts down the background refresh goroutine.
func (tm *TokenManager) Stop() {
	tm.cancel()
	tm.wg.Wait()
}

func (tm *TokenManager) refreshLoop() {
	defer tm.wg.Done()
	for {
		tm.mu.RLock()
		until := time.Until(tm.expiresAt) - 5*time.Minute
		tm.mu.RUnlock()

		if until < 0 {
			until = 0
		}

		timer := time.NewTimer(until)
		select {
		case <-tm.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		tm.mu.RLock()
		rt := tm.refreshTok
		tm.mu.RUnlock()

		resp, err := RefreshAccessToken(tm.ctx, tm.hubURL, rt)
		if err != nil {
			log.Printf("token refresh failed: %v (will retry in 30s)", err)
			retryTimer := time.NewTimer(30 * time.Second)
			select {
			case <-tm.ctx.Done():
				retryTimer.Stop()
				return
			case <-retryTimer.C:
			}
			continue
		}

		expiresAt := time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)

		tm.mu.Lock()
		tm.accessToken = resp.AccessToken
		tm.refreshTok = resp.RefreshToken
		tm.expiresAt = expiresAt
		tm.mu.Unlock()

		// Persist to disk
		if err := SaveOAuthCredentials(tm.hubURL, resp.AccessToken, resp.RefreshToken, resp.TokenType, expiresAt); err != nil {
			log.Printf("failed to save refreshed credentials: %v", err)
		}
	}
}

// ResolveTokenManager loads OAuth credentials from disk and returns a
// TokenManager for automatic background refresh.
func ResolveTokenManager(hubURL string) (string, *TokenManager, error) {
	creds, err := LoadCredentials()
	if err != nil {
		return "", nil, fmt.Errorf("not authenticated: run 'cllmhub login' first")
	}

	if creds.AccessToken == "" {
		return "", nil, fmt.Errorf("not authenticated: run 'cllmhub login' first")
	}

	// If expired, try an immediate sync refresh
	if time.Now().After(creds.ExpiresAt) {
		if creds.RefreshToken == "" {
			return "", nil, fmt.Errorf("session expired: run 'cllmhub login' to re-authenticate")
		}
		resp, err := RefreshAccessToken(context.Background(), hubURL, creds.RefreshToken)
		if err != nil {
			return "", nil, fmt.Errorf("session expired and refresh failed: %w\nRun 'cllmhub login' to re-authenticate", err)
		}
		creds.AccessToken = resp.AccessToken
		creds.RefreshToken = resp.RefreshToken
		creds.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
		creds.TokenType = resp.TokenType

		if err := SaveCredentials(creds); err != nil {
			log.Printf("failed to save refreshed credentials: %v", err)
		}
	}

	tm := NewTokenManager(hubURL, creds.AccessToken, creds.RefreshToken, creds.ExpiresAt)
	return creds.AccessToken, tm, nil
}
