package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/auth"
	"github.com/cllmhub/cllmhub-cli/internal/backend"
	"github.com/cllmhub/cllmhub-cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	loginHubURL       string
	loginUseLocalhost bool
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

func init() {
	loginCmd.Flags().StringVar(&loginHubURL, "hub-url", "https://cllmhub.com", "LLMHub gateway URL")
	loginCmd.Flags().MarkHidden("hub-url")
	loginCmd.Flags().BoolVarP(&loginUseLocalhost, "local", "l", false, "Use localhost hub (http://localhost:8080)")
	loginCmd.Flags().MarkHidden("local")
}

func runLogin(cmd *cobra.Command, args []string) error {
	hubURL := loginHubURL
	if loginUseLocalhost {
		hubURL = "http://localhost:8080"
	}

	// Capture existing credentials so we can revoke them after a successful login.
	oldCreds, oldCredsErr := auth.LoadCredentials()

	// If the user is already logged in, show who they are and ask for confirmation.
	if oldCredsErr == nil {
		if username := fetchCurrentUsername(oldCreds.HubURL, oldCreds.AccessToken); username != "" {
			fmt.Printf("You are already logged in as %s.\n", username)
			fmt.Println("Logging in again will invalidate your current session across all terminals.")
			fmt.Print("\nDo you want to continue? [y/N] ")

			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Login cancelled.")
				return nil
			}
			fmt.Println()
		}
	}

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

	// New login succeeded — clean up old credentials.
	if oldCredsErr == nil && oldCreds.RefreshToken != "" {
		oldHubURL := oldCreds.HubURL
		if oldHubURL == "" {
			oldHubURL = hubURL
		}
		revokeCtx, revokeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer revokeCancel()
		_ = auth.RevokeToken(revokeCtx, oldHubURL, oldCreds.RefreshToken)
	}
	_ = auth.RemoveCredentials()

	expiresAt := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if err := auth.SaveOAuthCredentials(hubURL, tr.AccessToken, tr.RefreshToken, tr.TokenType, expiresAt); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}

	fmt.Println("\nAuthenticated successfully!")

	// Try to list models from local backends for quick publish.
	if entries := listLocalModels(); len(entries) > 0 {
		labels := make([]string, len(entries))
		for i, e := range entries {
			labels[i] = fmt.Sprintf("%s (%s)", e.name, e.backend)
		}
		fmt.Println()
	selectModel:
		for {
			idx := tui.Select("Select a model to publish (or Esc to skip):", labels)
			if idx < 0 {
				break
			}
			selected := entries[idx]
			maxConc := tui.InputInt(fmt.Sprintf("Max concurrent requests for %s:", selected.name), 1)
			if maxConc < 0 {
				continue selectModel
			}
			if maxConc < 1 {
				maxConc = 1
			}
			fmt.Printf("Publishing %s...\n\n", selected.name)
			execCmd := exec.Command(os.Args[0], "publish", "-m", selected.name, "-b", selected.backend, "-c", fmt.Sprintf("%d", maxConc))
			execCmd.Stdin = os.Stdin
			execCmd.Stdout = os.Stdout
			execCmd.Stderr = os.Stderr
			return execCmd.Run()
		}
	} else {
		fmt.Println()
		fmt.Println("To publish a model:")
		fmt.Println("  cllmhub publish -m <model-name>")
	}

	return nil
}

// modelEntry holds a model name and the backend it came from.
type modelEntry struct {
	name    string
	backend string
}

// listLocalModels queries Ollama, vLLM, and LM Studio for available models.
// Returns all models found across all backends.
func listLocalModels() []modelEntry {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var entries []modelEntry

	if b, err := backend.NewOllama(backend.Config{}); err == nil {
		if models, err := b.ListModels(ctx); err == nil {
			for _, m := range models {
				entries = append(entries, modelEntry{name: m, backend: "ollama"})
			}
		}
	}

	if b, err := backend.NewVLLM(backend.Config{}); err == nil {
		if models, err := b.ListModels(ctx); err == nil {
			for _, m := range models {
				entries = append(entries, modelEntry{name: m, backend: "vllm"})
			}
		}
	}

	if b, err := backend.NewLMStudio(backend.Config{}); err == nil {
		if models, err := b.ListModels(ctx); err == nil {
			for _, m := range models {
				entries = append(entries, modelEntry{name: m, backend: "lmstudio"})
			}
		}
	}

	return entries
}

// fetchCurrentUsername tries to get the username from the hub. Returns empty string on any failure.
func fetchCurrentUsername(savedHubURL, accessToken string) string {
	if savedHubURL == "" {
		return ""
	}
	url := savedHubURL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/me", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var info struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return ""
	}
	return info.Username
}
