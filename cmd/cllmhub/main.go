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
	Use:     "cllmhub",
	Short:   "cLLMHub CLI - Turn your local LLM into a production API",
	Long: `cLLMHub turns your local LLM into a production API.
Publish models, create tokens, and share access with anyone.

Quick start with Hugging Face models:

  1. Set your HF token:    cllmhub hf-token set <token>
     (get one at https://huggingface.co/settings/tokens)

  2. Search for models:    cllmhub models --search mistral

  3. Download a model:     cllmhub download TheBloke/Mistral-7B-v0.1-GGUF

  4. Login to cLLMHub:     cllmhub login

  5. Publish it:           cllmhub publish Mistral-7B-v0.1

Or use an external backend (Ollama, vLLM, LM Studio):

  cllmhub publish -m "llama3-70b" -b ollama`,
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
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)

	// Daemon commands
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(daemonCmd)

	// Model management commands
	rootCmd.AddCommand(downloadCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(unpublishCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(hfTokenCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
