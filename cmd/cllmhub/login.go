package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/auth"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with cLLMHub",
	Long: `Starts an OAuth 2.0 device authorization flow.

You'll get a one-time code and a URL. Open the URL on any device
(phone, laptop, etc.), enter the code, and approve access.
The CLI will automatically detect the authorization and save your credentials.`,
	RunE: runLogin,
}

func runLogin(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Println("Initiating device authorization...")

	dar, err := auth.StartDeviceAuth(ctx, hubURL)
	if err != nil {
		return fmt.Errorf("device authorization failed: %w", err)
	}

	browserURL := dar.VerificationURIComplete
	if browserURL == "" {
		browserURL = dar.VerificationURI
	}

	// Always show the URL and code — this is the primary UX
	fmt.Printf("\nOpen this URL on any device:\n\n  %s\n\n", browserURL)
	fmt.Printf("Then enter the code: %s\n\n", dar.UserCode)

	// Try to open a browser as a convenience, but only if a display is available
	if auth.HasDisplay() {
		if err := auth.OpenBrowser(browserURL); err == nil {
			fmt.Println("(A browser window was opened for you.)")
			fmt.Println()
		}
	}

	fmt.Println("Waiting for authorization...")

	tr, err := auth.PollForToken(ctx, hubURL, dar)
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if err := auth.SaveOAuthCredentials(hubURL, tr.AccessToken, tr.RefreshToken, tr.TokenType, expiresAt); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}

	fmt.Println("\nAuthenticated successfully!")
	fmt.Println()
	fmt.Println("Next steps — publish a model to the cLLMHub network:")
	fmt.Println()
	fmt.Println("  1. Make sure your inference backend is running (e.g. Ollama, vLLM)")
	fmt.Println("  2. Run:")
	fmt.Println()
	fmt.Println("       cllmhub publish --model <model-name> --backend ollama")
	fmt.Println()
	fmt.Println("     This keeps a long-running process that bridges requests from the")
	fmt.Println("     network to your local backend. Press Ctrl+C to stop publishing.")
	return nil
}
