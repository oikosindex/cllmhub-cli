package main

import (
	"fmt"
	"os"

	"github.com/cllmhub/cllmhub-cli/internal/auth"
	"github.com/spf13/cobra"
)

var (
	hubURL  string
	Version = "dev"
)

var rootCmd = &cobra.Command{
	Use:     "cllmhub",
	Short:   "cLLMHub CLI - Turn your local LLM into a production API",
	Long:    `cLLMHub turns your local LLM into a production API.
Publish models, create tokens, and share access with anyone.`,
	Version: Version,
}

func init() {
	defaultHubURL := "https://cllmhub.com"
	if saved := auth.LoadHubURL(); saved != "" {
		defaultHubURL = saved
	}
	rootCmd.PersistentFlags().StringVar(&hubURL, "hub-url", defaultHubURL, "LLMHub gateway URL")

	rootCmd.CompletionOptions.DisableDefaultCmd = true

	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
