package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cllmhub/cllmhub-cli/internal/paths"
)

const (
	maxLogSize    = 5 * 1024 * 1024 // 5MB
	maxLogBackups = 3
)

// NewLogger creates a structured JSON logger writing to ~/.cllmhub/logs/daemon.log.
// It returns the logger and the underlying file (caller must close).
func NewLogger() (*slog.Logger, *os.File, error) {
	logDir, err := paths.LogDir()
	if err != nil {
		return nil, nil, err
	}

	logPath := filepath.Join(logDir, "daemon.log")

	// Rotate if needed
	rotateLog(logPath)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open log file: %w", err)
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(handler), f, nil
}

func rotateLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}

	// Shift existing backups
	for i := maxLogBackups - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", path, i)
		new := fmt.Sprintf("%s.%d", path, i+1)
		os.Rename(old, new)
	}
	os.Rename(path, path+".1")
}

