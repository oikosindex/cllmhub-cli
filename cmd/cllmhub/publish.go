package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"syscall"

	"github.com/cllmhub/cllmhub-cli/internal/auth"
	"github.com/cllmhub/cllmhub-cli/internal/backend"
	"github.com/cllmhub/cllmhub-cli/internal/provider"
	"github.com/spf13/cobra"
)

var (
	publishModel         string
	publishBackend       string
	publishBackendURL    string
	publishDescription   string
	publishMaxConcurrent int
	publishLogFile       string
	publishRateLimit     int
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a local LLM to the LLMHub network",
	Long: `Start a long-running process that connects to the cLLMHub gateway via WebSocket,
advertises the model in the registry, and bridges incoming requests
to the local inference backend.

Supported backends: ollama, llama.cpp, vllm, custom`,
	Example: `  cllmhub publish --model "llama3-70b" --backend ollama
  cllmhub publish --model "mixtral-8x7b" --backend vllm --hub-url https://cllmhub.com`,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVarP(&publishModel, "model", "m", "", "Model name to publish (required)")
	publishCmd.Flags().StringVarP(&publishBackend, "backend", "b", "ollama", "Backend type: ollama, llama.cpp, vllm, custom")
	publishCmd.Flags().StringVar(&publishBackendURL, "backend-url", "", "Backend endpoint URL (overrides default for the backend type)")
	publishCmd.Flags().StringVarP(&publishDescription, "description", "d", "", "Model description")
	publishCmd.Flags().IntVarP(&publishMaxConcurrent, "max-concurrent", "c", 1, "Maximum concurrent requests")
	publishCmd.Flags().StringVar(&publishLogFile, "log-file", "", "Path to audit log file (JSON lines)")
	publishCmd.Flags().IntVar(&publishRateLimit, "rate-limit", 0, "Max requests per minute (0 = unlimited)")

	publishCmd.MarkFlagRequired("model")
}

func runPublish(cmd *cobra.Command, args []string) error {
	token, tokenMgr, err := auth.ResolveTokenManager(hubURL)
	if err != nil {
		return err
	}

	if !regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`).MatchString(publishModel) {
		return fmt.Errorf("invalid model name %q: only alphanumerics, dots, underscores, colons, slashes, and hyphens are allowed", publishModel)
	}
	if len(publishDescription) > 500 {
		return fmt.Errorf("description too long (%d chars): maximum is 500", len(publishDescription))
	}

	fmt.Printf("Publishing model %q with backend %s\n", publishModel, publishBackend)
	fmt.Printf("  Hub:   %s\n", hubURL)
	if publishDescription != "" {
		fmt.Printf("  Description: %s\n", publishDescription)
	}
	fmt.Println()

	cfg := provider.Config{
		Model:         publishModel,
		Description:   publishDescription,
		MaxConcurrent: publishMaxConcurrent,
		Token:         token,
		Backend: backend.Config{
			Type:  publishBackend,
			URL:   publishBackendURL,
			Model: publishModel,
		},
		HubURL:       hubURL,
		LogFile:      publishLogFile,
		RateLimit:    publishRateLimit,
		TokenManager: tokenMgr,
	}

	p, err := provider.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize provider: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SIGINT (Ctrl+C) = full shutdown.
	// SIGTERM (e.g. from system update) = close WebSocket so reconnect logic kicks in.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigChan {
			if sig == syscall.SIGINT {
				fmt.Println("\nShutting down provider...")
				p.Stop()
				cancel()
				return
			}
			// SIGTERM: close the WebSocket to trigger reconnect
			fmt.Println("\nReceived SIGTERM, closing connection for reconnect...")
			p.CloseConnection()
		}
	}()

	err = p.Start(ctx)
	if err != nil && err == context.Canceled {
		return nil // clean shutdown via signal
	}
	return err
}
