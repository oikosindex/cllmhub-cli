package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/spf13/cobra"
)

var startWatch bool

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the cLLMHub daemon",
	Long: `Start the cLLMHub daemon.

The daemon manages model publishing bridges that connect external backends
(Ollama, vLLM, LM Studio, MLX, llama.cpp) to the cLLMHub network.

By default, models are unpublished only when a client request fails to reach
the backend. Use --watch to proactively monitor backend health and unpublish
unreachable models even when no requests are flowing.`,
	Example: `  cllmhub start
  cllmhub start --watch`,
	RunE: runStart,
}

func init() {
	startCmd.Flags().BoolVarP(&startWatch, "watch", "w", false, "Proactively watch backend health and unpublish unreachable models")
}

func runStart(cmd *cobra.Command, args []string) error {
	// Check if already running
	if running, pid := daemon.IsRunning(); running {
		fmt.Printf("Daemon is already running (PID: %d)\n", pid)
		return nil
	}

	// Open log file for daemon stdout/stderr
	logDir, err := daemon.LogDir()
	if err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logFile, err := os.OpenFile(
		logDir+"/daemon.log",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0600,
	)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	// Re-exec self with hidden __daemon command
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}

	daemonArgs := []string{"__daemon"}
	if startWatch {
		daemonArgs = append(daemonArgs, "--watch")
	}
	daemonProcess := exec.Command(executable, daemonArgs...)
	daemonProcess.Stdout = logFile
	daemonProcess.Stderr = logFile
	setDetachedProcess(daemonProcess)

	if err := daemonProcess.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for socket to appear (up to 5 seconds)
	sockPath, err := daemon.SocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			// Socket exists, verify daemon is responding
			client, err := daemon.NewClient()
			if err == nil {
				if err := client.Health(); err == nil {
					fmt.Printf("Daemon started (PID: %d)\n", daemonProcess.Process.Pid)
					return nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("daemon started but not responding — check logs: %s/daemon.log", logDir)
}
