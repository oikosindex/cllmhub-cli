package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show hub connectivity status",
	Long:  `Check that the LLMHub gateway is reachable and healthy.`,
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	url := strings.TrimRight(hubURL, "/") + "/health"
	fmt.Printf("Checking hub at %s ...\n", hubURL)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Println("Status:  Unreachable")
		return fmt.Errorf("cannot reach hub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Println("Status:  Healthy")
	} else {
		fmt.Printf("Status:  Unhealthy (HTTP %d)\n", resp.StatusCode)
	}

	return nil
}
