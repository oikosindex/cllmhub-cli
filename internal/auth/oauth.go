package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// DeviceAuthResponse is the response from the device authorization endpoint.
type DeviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// TokenResponse is the response from the token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// tokenErrorResponse represents an OAuth error from the token endpoint.
type tokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// PermanentOAuthError represents an OAuth error that will not succeed on retry
// (e.g. invalid_grant, invalid_client).
type PermanentOAuthError struct {
	Code        string
	Description string
}

func (e *PermanentOAuthError) Error() string {
	return fmt.Sprintf("%s — %s", e.Code, e.Description)
}

// permanentOAuthErrors lists OAuth error codes that are not worth retrying.
var permanentOAuthErrors = map[string]bool{
	"invalid_grant":  true,
	"invalid_client": true,
	"unauthorized_client": true,
}

const oauthClientID = "cllmhub-cli"

// minPollInterval is the minimum polling interval for the device code flow.
// Overridden in tests to avoid slow test runs.
var minPollInterval = 5 * time.Second

// StartDeviceAuth initiates the OAuth 2.0 device authorization flow.
func StartDeviceAuth(ctx context.Context, hubURL string) (*DeviceAuthResponse, error) {
	endpoint := hubURL + "/oauth/device/authorize"

	body, _ := json.Marshal(map[string]string{
		"client_id": oauthClientID,
		"scope":     "provider",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact authorization server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device authorization failed (HTTP %d)", resp.StatusCode)
	}

	var dar DeviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&dar); err != nil {
		return nil, fmt.Errorf("invalid device authorization response: %w", err)
	}
	return &dar, nil
}

// PollForToken polls the token endpoint until the user approves, denies, or the code expires.
func PollForToken(ctx context.Context, hubURL string, dar *DeviceAuthResponse) (*TokenResponse, error) {
	endpoint := hubURL + "/oauth/token"
	interval := time.Duration(dar.Interval) * time.Second
	if interval < minPollInterval {
		interval = minPollInterval
	}

	deadline := time.Now().Add(time.Duration(dar.ExpiresIn) * time.Second)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device code expired; please run 'cllmhub login' again")
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		form := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {dar.DeviceCode},
			"client_id":   {oauthClientID},
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue // transient network error, retry
		}

		if resp.StatusCode == http.StatusOK {
			var tr TokenResponse
			err := json.NewDecoder(resp.Body).Decode(&tr)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("invalid token response: %w", err)
			}
			return &tr, nil
		}

		var errResp tokenErrorResponse
		json.NewDecoder(resp.Body).Decode(&errResp)
		resp.Body.Close()

		switch errResp.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += minPollInterval
			continue
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		case "expired_token":
			return nil, fmt.Errorf("device code expired; please run 'cllmhub login' again")
		default:
			return nil, fmt.Errorf("token error: %s — %s", errResp.Error, errResp.ErrorDescription)
		}
	}
}

// RefreshAccessToken exchanges a refresh token for a new access token.
func RefreshAccessToken(ctx context.Context, hubURL, refreshToken string) (*TokenResponse, error) {
	endpoint := hubURL + "/oauth/token"

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {oauthClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to contact token endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp tokenErrorResponse
		json.NewDecoder(resp.Body).Decode(&errResp)
		if permanentOAuthErrors[errResp.Error] {
			return nil, &PermanentOAuthError{Code: errResp.Error, Description: errResp.ErrorDescription}
		}
		return nil, fmt.Errorf("token refresh failed: %s — %s", errResp.Error, errResp.ErrorDescription)
	}

	var tr TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	return &tr, nil
}

// RevokeToken revokes a refresh token server-side (RFC 7009).
func RevokeToken(ctx context.Context, hubURL, refreshToken string) error {
	endpoint := hubURL + "/oauth/revoke"

	form := url.Values{
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {oauthClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact revocation endpoint: %w", err)
	}
	defer resp.Body.Close()

	// RFC 7009: server MUST respond with 200 even if token is invalid
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// HasDisplay reports whether the current environment likely has a graphical
// display available (i.e. not a headless server or SSH session).
func HasDisplay() bool {
	// SSH session — no local display
	if os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		// macOS always has a display unless over SSH (checked above)
		return true
	case "windows":
		return true
	case "linux":
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	default:
		return false
	}
}

// OpenBrowser opens the given URL in the user's default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	return cmd.Start()
}
