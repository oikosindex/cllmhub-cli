package main

import (
	"fmt"
	"regexp"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/cllmhub/cllmhub-cli/internal/engine"
	"github.com/cllmhub/cllmhub-cli/internal/models"
	"github.com/cllmhub/cllmhub-cli/internal/tui"
	"github.com/spf13/cobra"
)

var (
	publishModel         string
	publishBackend       string
	publishBackendURL    string
	publishDescription   string
	publishMaxConcurrent int
)

var publishCmd = &cobra.Command{
	Use:   "publish [model...]",
	Short: "Publish a local LLM to the cLLMHub network",
	Long: `Publish models to the cLLMHub network via the daemon.

All models are published through the background daemon, which manages
engine and bridge services. The terminal is never blocked.

Positional arguments publish downloaded GGUF models via the local engine.
Use -m/-b flags to publish models served by an external backend (Ollama, vLLM, etc.).

Supported backends: ollama, llama.cpp, vllm, lmstudio, mlx`,
	Example: `  # Publish downloaded models via engine
  cllmhub publish llama3-8b mistral-7b

  # Publish a model from Ollama
  cllmhub publish -m "llama3-70b" -b ollama

  # Publish a model from vLLM
  cllmhub publish -m "mixtral-8x7b" -b vllm`,
	RunE: runPublish,
}

func init() {
	publishCmd.Flags().StringVarP(&publishModel, "model", "m", "", "Model name to publish")
	publishCmd.Flags().StringVarP(&publishBackend, "backend", "b", "ollama", "Backend type: ollama, llama.cpp, vllm, lmstudio, mlx")
	publishCmd.Flags().StringVar(&publishBackendURL, "backend-url", "", "Backend endpoint URL (overrides default for the backend type)")
	publishCmd.Flags().StringVarP(&publishDescription, "description", "d", "", "Model description")
	publishCmd.Flags().IntVarP(&publishMaxConcurrent, "max-concurrent", "c", 0, "Maximum concurrent requests (0 = auto-detect)")
}

// publishableModel represents a model that can be published, from any source.
type publishableModel struct {
	name   string
	source string // "gguf", "ollama", "vllm", "lmstudio", "mlx"
	label  string // display label
}

func runPublish(cmd *cobra.Command, args []string) error {
	// Positional args → GGUF models via engine
	if len(args) > 0 && !cmd.Flags().Changed("model") && !cmd.Flags().Changed("backend") {
		return publishViaDaemon(args)
	}

	// If -m flag provided, publish that model with the specified backend
	if cmd.Flags().Changed("model") || cmd.Flags().Changed("backend") {
		if publishModel == "" {
			return fmt.Errorf("model name is required: use -m <model>")
		}
		return publishExternalViaDaemon(publishModel, publishBackend, publishBackendURL, publishDescription, publishMaxConcurrent)
	}

	// Interactive TUI selection
	available := listAllPublishable()
	if len(available) == 0 {
		return fmt.Errorf("no models found\n  Download GGUF models: cllmhub download <repo>\n  Or start Ollama/vLLM/LM Studio/MLX")
	}

	labels := make([]string, len(available))
	for i, m := range available {
		labels[i] = m.label
	}

	for {
		idx := tui.Select("Select a model to publish:", labels)
		if idx < 0 {
			return fmt.Errorf("no model selected")
		}
		selected := available[idx]

		if selected.source == "gguf" {
			return publishViaDaemon([]string{selected.name})
		}

		return publishExternalViaDaemon(selected.name, selected.source, publishBackendURL, publishDescription, publishMaxConcurrent)
	}
}

// listAllPublishable returns all models that can be published:
// downloaded GGUF models + models from external backends (Ollama, vLLM, LM Studio).
func listAllPublishable() []publishableModel {
	var all []publishableModel

	// Downloaded GGUF models
	if registry, err := models.LoadRegistry(); err == nil {
		for _, entry := range registry.List() {
			if entry.State == "ready" {
				all = append(all, publishableModel{
					name:   entry.Name,
					source: "gguf",
					label:  fmt.Sprintf("%s (downloaded, %.1f GB)", entry.Name, float64(entry.SizeBytes)/(1024*1024*1024)),
				})
			}
		}
	}

	// External backend models
	for _, e := range listLocalModels() {
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

// publishViaDaemon publishes downloaded GGUF models through the daemon engine.
func publishViaDaemon(modelNames []string) error {
	cfg, profile := engine.DetectDefaults()
	fmt.Printf("Hardware profile: %s\n", profile)
	fmt.Printf("Engine config:    %s\n", cfg.Summary())

	registry, err := models.LoadRegistry()
	if err != nil {
		return fmt.Errorf("failed to load model registry: %w", err)
	}

	specs := make([]daemon.PublishModelSpec, 0, len(modelNames))
	for _, name := range modelNames {
		resolved, ok := registry.ResolveAlias(name)
		if !ok {
			return fmt.Errorf("model %q not found — run 'cllmhub models' to see available models", name)
		}
		entry, _ := registry.Get(resolved)
		if entry.State != "ready" {
			return fmt.Errorf("model %q is not ready (state: %s)", resolved, entry.State)
		}
		specs = append(specs, daemon.PublishModelSpec{Name: resolved})
	}

	if err := ensureDaemon(); err != nil {
		return err
	}

	client, err := daemon.NewClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	for _, s := range specs {
		fmt.Printf("Publishing %s...\n", s.Name)
	}

	return printPublishResults(client.Publish(specs))
}

// publishExternalViaDaemon publishes a model served by an external backend through the daemon.
func publishExternalViaDaemon(model, backendType, backendURL, description string, maxConcurrent int) error {
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
		MaxConcurrent: maxConcurrent,
		Description:   description,
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
