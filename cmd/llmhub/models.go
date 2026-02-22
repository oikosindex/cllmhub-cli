package main

import (
	"context"
	"fmt"

	"github.com/oikosindex/cllmhub-cli/internal/consumer"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List available models on the network",
	Long:  `Browse the catalog of available models from the LLMHub gateway.`,
	RunE:  runModels,
}

func runModels(cmd *cobra.Command, args []string) error {
	c, err := consumer.New(consumer.Config{HubURL: hubURL})
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer c.Close()

	ctx := context.Background()
	models, err := c.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("failed to list models: %w", err)
	}

	if len(models) == 0 {
		fmt.Println("No models available.")
		return nil
	}

	fmt.Println("MODEL              OWNER      ID")
	fmt.Println("─────────────────────────────────────────")
	for _, m := range models {
		fmt.Printf("%-18s %-10s %s\n", m.ID, m.OwnedBy, m.Object)
	}

	return nil
}
