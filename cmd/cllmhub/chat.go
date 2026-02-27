package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/oikosindex/cllmhub-cli/internal/consumer"
	"github.com/spf13/cobra"
)

var chatModel string

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with a model",
	Long:  `Start an interactive chat session with a model on the LLMHub network. Type your messages and receive responses in real-time.`,
	Example: `  cllmhub chat --model llama3-70b
  cllmhub chat -m mixtral-8x7b --hub-url https://cllmhub.com`,
	RunE: runChat,
}

func init() {
	chatCmd.Flags().StringVarP(&chatModel, "model", "m", "", "Model to chat with (required)")
	chatCmd.MarkFlagRequired("model")
}

func runChat(cmd *cobra.Command, args []string) error {
	c, err := consumer.New(consumer.Config{HubURL: hubURL})
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer c.Close()

	fmt.Printf("Starting chat with %s (type 'exit' to quit)\n\n", chatModel)

	scanner := bufio.NewScanner(os.Stdin)
	ctx := context.Background()

	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "exit" {
			fmt.Println("Goodbye!")
			break
		}

		fmt.Printf("%s: ", chatModel)
		err := c.Stream(ctx, consumer.StreamOptions{
			Model:       chatModel,
			Prompt:      input,
			MaxTokens:   1024,
			Temperature: 0.7,
			OnToken: func(token string) error {
				fmt.Print(token)
				return nil
			},
		})
		fmt.Println()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}

	return scanner.Err()
}
