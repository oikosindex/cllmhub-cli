package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func setupTestHome(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
}

func TestSaveAndLoadCredentials(t *testing.T) {
	setupTestHome(t)

	want := credentials{
		AccessToken:  "at_abc",
		RefreshToken: "rt_xyz",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).Truncate(time.Second),
	}

	if err := SaveCredentials(want); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	got, err := LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}

	if got.AccessToken != want.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, want.RefreshToken)
	}
	if got.TokenType != want.TokenType {
		t.Errorf("TokenType = %q, want %q", got.TokenType, want.TokenType)
	}
}

func TestSaveOAuthCredentials(t *testing.T) {
	setupTestHome(t)

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := SaveOAuthCredentials("http://localhost", "at_123", "rt_456", "Bearer", expiresAt); err != nil {
		t.Fatalf("SaveOAuthCredentials: %v", err)
	}

	creds, err := LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.AccessToken != "at_123" {
		t.Errorf("AccessToken = %q, want %q", creds.AccessToken, "at_123")
	}
	if creds.RefreshToken != "rt_456" {
		t.Errorf("RefreshToken = %q, want %q", creds.RefreshToken, "rt_456")
	}
}

func TestLoadToken(t *testing.T) {
	setupTestHome(t)

	// No credentials file
	_, err := LoadToken()
	if err == nil {
		t.Fatal("expected error when no credentials file exists")
	}

	// Empty access token
	SaveCredentials(credentials{RefreshToken: "rt_only"})
	_, err = LoadToken()
	if err == nil {
		t.Fatal("expected error when access token is empty")
	}

	// Valid access token
	SaveCredentials(credentials{AccessToken: "at_good"})
	tok, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "at_good" {
		t.Errorf("token = %q, want %q", tok, "at_good")
	}
}

func TestLoadRefreshToken(t *testing.T) {
	setupTestHome(t)

	// No credentials
	_, err := LoadRefreshToken()
	if err == nil {
		t.Fatal("expected error when no credentials file exists")
	}

	// No refresh token
	SaveCredentials(credentials{AccessToken: "at_only"})
	_, err = LoadRefreshToken()
	if err == nil {
		t.Fatal("expected error when refresh token is empty")
	}

	// Valid refresh token
	SaveCredentials(credentials{AccessToken: "at", RefreshToken: "rt_good"})
	rt, err := LoadRefreshToken()
	if err != nil {
		t.Fatalf("LoadRefreshToken: %v", err)
	}
	if rt != "rt_good" {
		t.Errorf("refresh token = %q, want %q", rt, "rt_good")
	}
}

func TestRemoveCredentials(t *testing.T) {
	setupTestHome(t)

	// Remove when no file exists — should not error
	if err := RemoveCredentials(); err != nil {
		t.Fatalf("RemoveCredentials (no file): %v", err)
	}

	// Save then remove
	SaveCredentials(credentials{AccessToken: "at"})
	if err := RemoveCredentials(); err != nil {
		t.Fatalf("RemoveCredentials: %v", err)
	}

	_, err := LoadCredentials()
	if err == nil {
		t.Fatal("expected error after removing credentials")
	}
}

func TestCredentialsFilePermissions(t *testing.T) {
	setupTestHome(t)

	SaveCredentials(credentials{AccessToken: "at"})

	path, _ := credentialsPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

func TestResolveTokenManager_NoCredentials(t *testing.T) {
	setupTestHome(t)

	_, _, err := ResolveTokenManager("http://localhost")
	if err == nil {
		t.Fatal("expected error when no credentials exist")
	}
}

func TestResolveTokenManager_ValidOAuth(t *testing.T) {
	setupTestHome(t)

	SaveOAuthCredentials("http://localhost", "at_valid", "rt_valid", "Bearer", time.Now().Add(time.Hour))

	tok, tm, err := ResolveTokenManager("http://localhost")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tok != "at_valid" {
		t.Errorf("token = %q, want %q", tok, "at_valid")
	}
	if tm == nil {
		t.Fatal("expected non-nil TokenManager")
	}
	tm.Stop()
}

func TestResolveTokenManager_ExpiredWithRefresh(t *testing.T) {
	setupTestHome(t)

	// Mock refresh endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at_refreshed",
			RefreshToken: "rt_new",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	SaveOAuthCredentials(srv.URL, "at_expired", "rt_old", "Bearer", time.Now().Add(-time.Hour))

	tok, tm, err := ResolveTokenManager(srv.URL)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tok != "at_refreshed" {
		t.Errorf("token = %q, want %q", tok, "at_refreshed")
	}
	if tm == nil {
		t.Fatal("expected non-nil TokenManager")
	}
	tm.Stop()

	// Verify persisted to disk
	creds, _ := LoadCredentials()
	if creds.AccessToken != "at_refreshed" {
		t.Errorf("persisted AccessToken = %q, want %q", creds.AccessToken, "at_refreshed")
	}
}

func TestResolveTokenManager_ExpiredNoRefreshToken(t *testing.T) {
	setupTestHome(t)

	SaveCredentials(credentials{
		AccessToken: "at_expired",
		ExpiresAt:   time.Now().Add(-time.Hour),
	})

	_, _, err := ResolveTokenManager("http://localhost")
	if err == nil {
		t.Fatal("expected error when expired with no refresh token")
	}
}

func TestTokenManager_AccessToken(t *testing.T) {
	tm := NewTokenManager("http://localhost", "at_init", "rt_init", time.Now().Add(time.Hour))
	defer tm.Stop()

	if got := tm.AccessToken(); got != "at_init" {
		t.Errorf("AccessToken = %q, want %q", got, "at_init")
	}
}

func TestTokenManager_StopIsIdempotent(t *testing.T) {
	tm := NewTokenManager("http://localhost", "at", "rt", time.Now().Add(time.Hour))
	tm.Stop()
	// Calling Stop again should not panic or deadlock
}

func TestTokenManager_RefreshesOnExpiry(t *testing.T) {
	setupTestHome(t)

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at_new",
			RefreshToken: "rt_new",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		})
	}))
	defer srv.Close()

	// Token that expires in the past (triggers immediate refresh)
	tm := NewTokenManager(srv.URL, "at_old", "rt_old", time.Now().Add(-time.Minute))

	// Wait for the refresh to happen
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tm.AccessToken() == "at_new" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	tm.Stop()

	if tm.AccessToken() != "at_new" {
		t.Errorf("AccessToken = %q, want %q", tm.AccessToken(), "at_new")
	}
	if calls == 0 {
		t.Error("expected at least one refresh call")
	}
}
