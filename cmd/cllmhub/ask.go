package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/oikosindex/cllmhub-cli/internal/consumer"
	"github.com/spf13/cobra"
)

var (
	askModel     string
	askMaxTokens int
	askTemp      float64
	askStream    bool
)

var askCmd = &cobra.Command{
	Use:   "ask [prompt]",
	Short: "Send a prompt to a model and get a response",
	Long:  `Send a single prompt to a model on the LLMHub network and receive a completion.`,
	Example: `  cllmhub ask --model llama3-70b "What is NATS messaging?"
  cllmhub ask -m mixtral-8x7b --stream "Explain quantum computing"
  cllmhub ask -m llama3 --hub-url https://cllmhub.com "Hello"`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAsk,
}

func init() {
	askCmd.Flags().StringVarP(&askModel, "model", "m", "", "Model to use (required)")
	askCmd.Flags().IntVar(&askMaxTokens, "max-tokens", 512, "Maximum tokens in response")
	askCmd.Flags().Float64VarP(&askTemp, "temperature", "t", 0.7, "Sampling temperature")
	askCmd.Flags().BoolVarP(&askStream, "stream", "s", false, "Stream response tokens")

	askCmd.MarkFlagRequired("model")
}

func runAsk(cmd *cobra.Command, args []string) error {
	prompt := strings.Join(args, " ")

	c, err := consumer.New(consumer.Config{HubURL: hubURL})
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer c.Close()

	ctx := context.Background()

	if askStream {
		fmt.Printf("%s: ", askModel)
		err := c.Stream(ctx, consumer.StreamOptions{
			Model:       askModel,
			Prompt:      prompt,
			MaxTokens:   askMaxTokens,
			Temperature: askTemp,
			OnToken: func(token string) error {
				fmt.Print(token)
				return nil
			},
		})
		fmt.Println()
		return err
	}

	text, err := c.Ask(ctx, consumer.AskOptions{
		Model:       askModel,
		Prompt:      prompt,
		MaxTokens:   askMaxTokens,
		Temperature: askTemp,
	})
	if err != nil {
		return err
	}

	fmt.Printf("%s: %s\n", askModel, text)
	return nil
}
