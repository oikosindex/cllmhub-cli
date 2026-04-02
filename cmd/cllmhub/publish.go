package main

import (
	"fmt"
	"regexp"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/cllmhub/cllmhub-cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	publishModel         string
	publishBackend       string
	publishBackendURL    string
	publishBackendAPIKey string
	publishDescription   string
	publishMaxConcurrent int
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a local LLM to the cLLMHub network",
	Long: `Publish models to the cLLMHub network via the daemon.

All models are published through the background daemon, which manages
bridge services. The terminal is never blocked.

Use -m/-b flags to publish models served by an external backend (Ollama, vLLM, etc.).
If no flags are provided, the CLI will discover running backends and let you pick a model.

Supported backends: ollama, llama.cpp, vllm, lmstudio, mlx`,
	Example: `  # Publish a model from Ollama
  cllmhub publish -m "llama3-70b" -b ollama

  # Publish a model from vLLM with a custom URL
  cllmhub publish -m "mixtral-8x7b" -b vllm --backend-url http://localhost:9000

  # Publish with authentication
  cllmhub publish -m "my-model" -b mlx --api-key sk-xxx

  # Interactive selection from detected backends
  cllmhub publish`,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVarP(&publishModel, "model", "m", "", "Model name to publish")
	publishCmd.Flags().StringVarP(&publishBackend, "backend", "b", "ollama", "Backend type: ollama, llama.cpp, vllm, lmstudio, mlx")
	publishCmd.Flags().StringVar(&publishBackendURL, "backend-url", "", "Backend endpoint URL (overrides default for the backend type)")
	publishCmd.Flags().StringVar(&publishBackendAPIKey, "api-key", "", "API key for the backend server")
	publishCmd.Flags().StringVarP(&publishDescription, "description", "d", "", "Model description")
	publishCmd.Flags().IntVar(&publishMaxConcurrent, "max-concurrent", 0, "Max concurrent slots ceiling (default: auto-detect, starting at 1, max 5)")
}

func runPublish(cmd *cobra.Command, args []string) error {
	// If -m flag provided, publish that model with the specified backend
	if cmd.Flags().Changed("model") || cmd.Flags().Changed("backend") {
		if publishModel == "" {
			return fmt.Errorf("model name is required: use -m <model>")
		}
		return publishViaDaemon(publishModel, publishBackend, publishBackendURL, publishBackendAPIKey, publishDescription, publishMaxConcurrent)
	}

	// Interactive TUI selection from detected backends
	available := listAllPublishable()
	if len(available) == 0 {
		return fmt.Errorf("no models found — start Ollama, vLLM, LM Studio, or MLX first")
	}

	labels := make([]string, len(available))
	for i, m := range available {
		labels[i] = m.label
	}

	idx := tui.Select("Select a model to publish:", labels)
	if idx < 0 {
		return fmt.Errorf("no model selected")
	}
	selected := available[idx]

	return publishViaDaemon(selected.name, selected.source, publishBackendURL, publishBackendAPIKey, publishDescription, publishMaxConcurrent)
}

// publishableModel represents a model that can be published, from any source.
type publishableModel struct {
	name   string
	source string // "ollama", "vllm", "lmstudio", "mlx"
	label  string // display label
}

// listAllPublishable returns all models available from external backends.
func listAllPublishable() []publishableModel {
	var all []publishableModel

	for _, e := range listLocalModels(publishBackendAPIKey) {
		if e.needsKey {
			fmt.Printf("  ⚠ %s server detected but requires authentication — use: --api-key <key>\n", e.backend)
			continue
		}
		all = append(all, publishableModel{
			name:   e.name,
			source: e.backend,
			label:  fmt.Sprintf("%s (%s)", e.name, e.backend),
		})
	}

	return all
}

// ensureDaemon makes sure the daemon is running, starting it if needed.
func ensureDaemon() error {
	running, _ := daemon.IsRunning()
	if !running {
		fmt.Println("Starting daemon...")
		if err := runStart(nil, nil); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}
	}
	return nil
}

// publishViaDaemon publishes a model served by an external backend through the daemon.
func publishViaDaemon(model, backendType, backendURL, apiKey, description string, maxConcurrent int) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9._:/-]+$`).MatchString(model) {
		return fmt.Errorf("invalid model name %q: only alphanumerics, dots, underscores, colons, slashes, and hyphens are allowed", model)
	}
	if len(description) > 500 {
		return fmt.Errorf("description too long (%d chars): maximum is 500", len(description))
	}

	if err := ensureDaemon(); err != nil {
		return err
	}

	client, err := daemon.NewClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	spec := daemon.PublishModelSpec{
		Name:          model,
		BackendType:   backendType,
		BackendURL:    backendURL,
		BackendAPIKey: apiKey,
		Description:   description,
		MaxConcurrent: maxConcurrent,
	}

	fmt.Printf("Publishing %s (backend: %s)...\n", model, backendType)
	return printPublishResults(client.Publish([]daemon.PublishModelSpec{spec}))
}

func printPublishResults(resp *daemon.PublishResponse, err error) error {
	if err != nil {
		return err
	}

	var failures int
	for _, r := range resp.Results {
		status := "published"
		if r.Already {
			status = "already published"
		} else if !r.Success {
			fmt.Printf("%-20s error: %s\n", r.Model, r.Error)
			failures++
			continue
		}
		if r.ProviderID != "" {
			fmt.Printf("%-20s %s (%s)\n", r.Model, status, r.ProviderID)
		} else {
			fmt.Printf("%-20s %s\n", r.Model, status)
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d of %d models failed to publish", failures, len(resp.Results))
	}
	return nil
}
