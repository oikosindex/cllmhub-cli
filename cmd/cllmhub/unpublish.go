package main

import (
	"fmt"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/spf13/cobra"
)

var unpublishCmd = &cobra.Command{
	Use:   "unpublish <model...>",
	Short: "Stop serving one or more published models",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runUnpublish,
}

func runUnpublish(cmd *cobra.Command, args []string) error {
	running, _ := daemon.IsRunning()
	if !running {
		return fmt.Errorf("daemon is not running — no models are published")
	}

	client, err := daemon.NewClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	for _, name := range args {
		fmt.Printf("Unpublishing %s...\n", name)
	}

	resp, err := client.Unpublish(args)
	if err != nil {
		return err
	}

	var failures int
	for _, r := range resp.Results {
		if r.Success {
			fmt.Printf("%-20s unpublished\n", r.Model)
		} else {
			fmt.Printf("%-20s error: %s\n", r.Model, r.Error)
			failures++
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d of %d models failed to unpublish", failures, len(args))
	}
	return nil
}
