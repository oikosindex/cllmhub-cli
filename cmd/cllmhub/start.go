package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/cllmhub/cllmhub-cli/internal/daemon"
	"github.com/cllmhub/cllmhub-cli/internal/engine"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the cLLMHub daemon",
	Long: `Start the cLLMHub daemon with configurable engine settings.

Engine flags control how llama-server runs. If not specified, defaults are
auto-detected based on your hardware (Apple Silicon, NVIDIA GPU, or CPU).`,
	Example: `  # Start with auto-detected defaults
  cllmhub start

  # Start with custom settings
  cllmhub start --ctx-size 8192 --flash-attn --slots 2

  # Start for CPU-only inference
  cllmhub start --n-gpu-layers 0 --ctx-size 2048 --slots 1`,
	RunE: runStart,
}

func init() {
	startCmd.Flags().Int("ctx-size", 0, "Context size for inference (0=auto-detect based on hardware)")
	startCmd.Flags().Bool("flash-attn", false, "Enable flash attention (auto-enabled on Apple Silicon/NVIDIA)")
	startCmd.Flags().Int("slots", 0, "Number of concurrent inference slots (0=auto-detect)")
	startCmd.Flags().Int("n-gpu-layers", -1, "Number of layers to offload to GPU (-1=auto, 0=CPU only)")
	startCmd.Flags().Int("batch-size", 0, "Batch size for prompt processing (0=auto-detect)")
}

func runStart(cmd *cobra.Command, args []string) error {
	// Check if already running
	if running, pid := daemon.IsRunning(); running {
		fmt.Printf("Daemon is already running (PID: %d)\n", pid)
		return nil
	}

	// Detect hardware defaults, then override with any user-specified flags
	cfg, _ := engine.DetectDefaults()

	if cmd != nil {
		if cmd.Flags().Changed("ctx-size") {
			cfg.CtxSize, _ = cmd.Flags().GetInt("ctx-size")
		}
		if cmd.Flags().Changed("flash-attn") {
			cfg.FlashAttn, _ = cmd.Flags().GetBool("flash-attn")
		}
		if cmd.Flags().Changed("slots") {
			cfg.Slots, _ = cmd.Flags().GetInt("slots")
		}
		if cmd.Flags().Changed("n-gpu-layers") {
			cfg.NGPULayers, _ = cmd.Flags().GetInt("n-gpu-layers")
		}
		if cmd.Flags().Changed("batch-size") {
			cfg.BatchSize, _ = cmd.Flags().GetInt("batch-size")
		}
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

	// Re-exec self with hidden __daemon command, forwarding resolved config
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable path: %w", err)
	}

	daemonArgs := []string{"__daemon",
		"--ctx-size", strconv.Itoa(cfg.CtxSize),
		"--flash-attn", strconv.FormatBool(cfg.FlashAttn),
		"--slots", strconv.Itoa(cfg.Slots),
		"--n-gpu-layers", strconv.Itoa(cfg.NGPULayers),
		"--batch-size", strconv.Itoa(cfg.BatchSize),
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
