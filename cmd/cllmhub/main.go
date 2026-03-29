package main

import (
	"fmt"
	"os"

	"github.com/cllmhub/cllmhub-cli/internal/versioncheck"
	"github.com/spf13/cobra"
)

var (
	Version    = "dev"
	verChecker *versioncheck.Checker
)

var rootCmd = &cobra.Command{
	Use:   "cllmhub",
	Short: "cLLMHub CLI - Publish local LLMs to the cLLMHub network",
	Long: `cLLMHub publishes local LLMs to the cLLMHub network.
Connect your existing inference backend and share models with anyone.

Quick start:

  1. Login to cLLMHub:     cllmhub login

  2. Publish from Ollama:  cllmhub publish -m llama3 -b ollama

  3. Check status:         cllmhub status

Supported backends: Ollama, vLLM, LM Studio, llama.cpp, MLX`,
	SilenceUsage: true,
	Version:      Version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd, args)
	},
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cmd.Name() != "update" {
			verChecker = versioncheck.New(Version)
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if verChecker == nil {
			return
		}
		if r := verChecker.Result(); r != nil && r.Available {
			fmt.Printf("\nA new version of cllmhub is available: %s (current: %s)\n", r.LatestVersion, r.CurrentVersion)
			fmt.Println("Run \"cllmhub update\" to upgrade.")
		}
	},
}

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(unpublishCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)

	// Daemon commands
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(logsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
