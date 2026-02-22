package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var hubURL string

var rootCmd = &cobra.Command{
	Use:   "llmhub",
	Short: "cLLMHub CLI - Turn your local LLM into a production API",
	Long: `cLLMHub turns your local LLM into a production API.
Publish models, create tokens, and share access with anyone.`,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&hubURL, "hub-url", "https://cllmhub.com", "LLMHub gateway URL")

	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(askCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(modelsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
