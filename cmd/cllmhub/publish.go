package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/oikosindex/cllmhub-cli/internal/backend"
	"github.com/oikosindex/cllmhub-cli/internal/provider"
	"github.com/spf13/cobra"
)

var (
	publishModel         string
	publishBackend       string
	publishBackendURL    string
	publishDescription   string
	publishMaxConcurrent int
	publishToken         string
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a local LLM to the LLMHub network",
	Long: `Start a long-running process that connects to the cLLMHub gateway via WebSocket,
advertises the model in the registry, and bridges incoming requests
to the local inference backend.

Supported backends: ollama, llama.cpp, vllm, custom`,
	Example: `  cllmhub publish --model "llama3-70b" --backend ollama --token <your-token>
  cllmhub publish --model "mixtral-8x7b" --backend vllm --token <your-token> --hub-url https://cllmhub.com`,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVarP(&publishModel, "model", "m", "", "Model name to publish (required)")
	publishCmd.Flags().StringVarP(&publishBackend, "backend", "b", "ollama", "Backend type: ollama, llama.cpp, vllm, custom")
	publishCmd.Flags().StringVar(&publishBackendURL, "backend-url", "", "Backend endpoint URL (overrides default for the backend type)")
	publishCmd.Flags().StringVarP(&publishDescription, "description", "d", "", "Model description")
	publishCmd.Flags().IntVarP(&publishMaxConcurrent, "max-concurrent", "c", 1, "Maximum concurrent requests")
	publishCmd.Flags().StringVarP(&publishToken, "token", "t", "", "Provider token from the LLMHub dashboard (required)")

	publishCmd.MarkFlagRequired("model")
	publishCmd.MarkFlagRequired("token")
}

func runPublish(cmd *cobra.Command, args []string) error {
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
		Token:         publishToken,
		Backend: backend.Config{
			Type:  publishBackend,
			URL:   publishBackendURL,
			Model: publishModel,
		},
		HubURL: hubURL,
	}

	p, err := provider.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize provider: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down provider...")
		p.Stop()
		cancel()
	}()

	return p.Start(ctx)
}
