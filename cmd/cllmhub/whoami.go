package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/auth"
	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the currently logged-in user",
	RunE:  runWhoami,
}

type userInfoResponse struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

func runWhoami(cmd *cobra.Command, args []string) error {
	token, err := auth.LoadToken()
	if err != nil {
		return fmt.Errorf("not logged in: run 'cllmhub login' first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hubURL+"/api/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact hub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("session expired: run 'cllmhub login' to re-authenticate")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch user info (HTTP %d)", resp.StatusCode)
	}

	var info userInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("invalid response from hub: %w", err)
	}

	fmt.Printf("Logged in as %s (%s)\n", info.Username, info.Email)
	return nil
}
