package main

import (
	"fmt"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/cllmhub/cllmhub-cli/internal/models"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the cLLMHub daemon status",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	running, _ := daemon.IsRunning()
	if !running {
		fmt.Println("Daemon is not running")
		return nil
	}

	client, err := daemon.NewClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	status, err := client.Status()
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	uptime := time.Duration(status.Uptime) * time.Second

	fmt.Printf("Daemon:  running (PID: %d, uptime: %s)\n", status.PID, formatDuration(uptime))
	fmt.Printf("Engine:  %s\n", status.Engine)

	if len(status.Models) == 0 {
		fmt.Println("\nNo published models")
	} else {
		fmt.Println("\nPublished models:")
		registry, _ := models.LoadRegistry()
		for _, m := range status.Models {
			alias := ""
			if registry != nil {
				if entry, ok := registry.Get(m.Name); ok {
					alias = entry.Alias
				}
			}
			backendLabel := m.Backend
			if backendLabel == "" {
				backendLabel = "engine"
			}
			stateStr := fmt.Sprintf("%s (%s)", m.State, backendLabel)
			if alias != "" {
				fmt.Printf("  %-6s %-20s %s\n", alias, m.Name, stateStr)
			} else {
				fmt.Printf("  %-6s %-20s %s\n", "-", m.Name, stateStr)
			}
		}
	}

	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}
