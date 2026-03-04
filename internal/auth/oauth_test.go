package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func fastPoll(t *testing.T) {
	t.Helper()
	old := minPollInterval
	minPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { minPollInterval = old })
}

func TestStartDeviceAuth_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != oauthClientID {
			t.Errorf("client_id = %q, want %q", body["client_id"], oauthClientID)
		}

		json.NewEncoder(w).Encode(DeviceAuthResponse{
			DeviceCode:              "dev-code-123",
			UserCode:                "ABCD-1234",
			VerificationURI:         "https://example.com/device",
			VerificationURIComplete: "https://example.com/device?code=ABCD-1234",
			ExpiresIn:               900,
			Interval:                5,
		})
	}))
	defer srv.Close()

	dar, err := StartDeviceAuth(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("StartDeviceAuth: %v", err)
	}
	if dar.DeviceCode != "dev-code-123" {
		t.Errorf("DeviceCode = %q, want %q", dar.DeviceCode, "dev-code-123")
	}
	if dar.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q, want %q", dar.UserCode, "ABCD-1234")
	}
	if dar.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want 900", dar.ExpiresIn)
	}
}

func TestStartDeviceAuth_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := StartDeviceAuth(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error on server error")
	}
}

func TestStartDeviceAuth_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := StartDeviceAuth(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestPollForToken_ImmediateSuccess(t *testing.T) {
	fastPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at_poll",
			RefreshToken: "rt_poll",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	dar := &DeviceAuthResponse{
		DeviceCode: "dev-123",
		ExpiresIn:  60,
		Interval:   0,
	}

	tr, err := PollForToken(context.Background(), srv.URL, dar)
	if err != nil {
		t.Fatalf("PollForToken: %v", err)
	}
	if tr.AccessToken != "at_poll" {
		t.Errorf("AccessToken = %q, want %q", tr.AccessToken, "at_poll")
	}
}

func TestPollForToken_PendingThenSuccess(t *testing.T) {
	fastPoll(t)
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(tokenErrorResponse{Error: "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at_after_pending",
			RefreshToken: "rt_after_pending",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	dar := &DeviceAuthResponse{
		DeviceCode: "dev-123",
		ExpiresIn:  30,
		Interval:   0,
	}

	tr, err := PollForToken(context.Background(), srv.URL, dar)
	if err != nil {
		t.Fatalf("PollForToken: %v", err)
	}
	if tr.AccessToken != "at_after_pending" {
		t.Errorf("AccessToken = %q, want %q", tr.AccessToken, "at_after_pending")
	}
	if count.Load() < 3 {
		t.Errorf("expected at least 3 requests, got %d", count.Load())
	}
}

func TestPollForToken_AccessDenied(t *testing.T) {
	fastPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{Error: "access_denied"})
	}))
	defer srv.Close()

	dar := &DeviceAuthResponse{
		DeviceCode: "dev-123",
		ExpiresIn:  30,
		Interval:   0,
	}

	_, err := PollForToken(context.Background(), srv.URL, dar)
	if err == nil {
		t.Fatal("expected error on access denied")
	}
	if got := err.Error(); got != "authorization denied by user" {
		t.Errorf("error = %q, want 'authorization denied by user'", got)
	}
}

func TestPollForToken_ExpiredToken(t *testing.T) {
	fastPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{Error: "expired_token"})
	}))
	defer srv.Close()

	dar := &DeviceAuthResponse{
		DeviceCode: "dev-123",
		ExpiresIn:  30,
		Interval:   0,
	}

	_, err := PollForToken(context.Background(), srv.URL, dar)
	if err == nil {
		t.Fatal("expected error on expired token")
	}
}

func TestPollForToken_ContextCancelled(t *testing.T) {
	fastPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{Error: "authorization_pending"})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	dar := &DeviceAuthResponse{
		DeviceCode: "dev-123",
		ExpiresIn:  60,
		Interval:   0,
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	_, err := PollForToken(ctx, srv.URL, dar)
	if err == nil {
		t.Fatal("expected error on context cancel")
	}
}

func TestPollForToken_SlowDown(t *testing.T) {
	fastPoll(t)
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(tokenErrorResponse{Error: "slow_down"})
			return
		}
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at_slow",
			RefreshToken: "rt_slow",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	dar := &DeviceAuthResponse{
		DeviceCode: "dev-123",
		ExpiresIn:  60,
		Interval:   0,
	}

	tr, err := PollForToken(context.Background(), srv.URL, dar)
	if err != nil {
		t.Fatalf("PollForToken: %v", err)
	}
	if tr.AccessToken != "at_slow" {
		t.Errorf("AccessToken = %q, want %q", tr.AccessToken, "at_slow")
	}
}

func TestRefreshAccessToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "rt_old" {
			t.Errorf("refresh_token = %q", r.FormValue("refresh_token"))
		}
		if r.FormValue("client_id") != oauthClientID {
			t.Errorf("client_id = %q", r.FormValue("client_id"))
		}

		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at_new",
			RefreshToken: "rt_new",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	tr, err := RefreshAccessToken(context.Background(), srv.URL, "rt_old")
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if tr.AccessToken != "at_new" {
		t.Errorf("AccessToken = %q, want %q", tr.AccessToken, "at_new")
	}
	if tr.RefreshToken != "rt_new" {
		t.Errorf("RefreshToken = %q, want %q", tr.RefreshToken, "rt_new")
	}
}

func TestRefreshAccessToken_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(tokenErrorResponse{
			Error:            "invalid_grant",
			ErrorDescription: "refresh token revoked",
		})
	}))
	defer srv.Close()

	_, err := RefreshAccessToken(context.Background(), srv.URL, "rt_bad")
	if err == nil {
		t.Fatal("expected error on invalid grant")
	}
}

func TestRevokeToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		r.ParseForm()
		if r.FormValue("token") != "rt_revoke" {
			t.Errorf("token = %q", r.FormValue("token"))
		}
		if r.FormValue("token_type_hint") != "refresh_token" {
			t.Errorf("token_type_hint = %q", r.FormValue("token_type_hint"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := RevokeToken(context.Background(), srv.URL, "rt_revoke")
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
}

func TestRevokeToken_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := RevokeToken(context.Background(), srv.URL, "rt_any")
	if err == nil {
		t.Fatal("expected error on server error")
	}
}

func TestHasDisplay_SSHSession(t *testing.T) {
	t.Setenv("SSH_CLIENT", "192.168.1.1 12345 22")
	t.Setenv("SSH_TTY", "")
	if HasDisplay() {
		t.Error("expected false when SSH_CLIENT is set")
	}
}

func TestHasDisplay_SSHTty(t *testing.T) {
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "/dev/pts/0")
	if HasDisplay() {
		t.Error("expected false when SSH_TTY is set")
	}
}

func TestHasDisplay_NoSSH(t *testing.T) {
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")

	// On the host running tests (macOS/Linux with display), it should detect correctly.
	// We can't fully control runtime.GOOS, but we verify it doesn't panic.
	// On CI (Linux without DISPLAY), it returns false which is correct.
	_ = HasDisplay()
}

func TestHasDisplay_LinuxWithDisplay(t *testing.T) {
	// Only meaningful on Linux, but safe to run everywhere
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
	original := os.Getenv("DISPLAY")
	t.Setenv("DISPLAY", ":0")
	defer t.Setenv("DISPLAY", original)

	// Can't check the result on non-Linux (runtime.GOOS check happens first),
	// but verify no panic
	_ = HasDisplay()
}
