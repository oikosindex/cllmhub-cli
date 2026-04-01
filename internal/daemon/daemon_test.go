package daemon

import (
	"log/slog"
	"os"
	"testing"
)

func TestNewBridgeManager(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bm := NewBridgeManager(logger, false)

	if bm.Count() != 0 {
		t.Errorf("Count = %d, want 0", bm.Count())
	}
	if len(bm.PublishedModels()) != 0 {
		t.Errorf("expected no published models")
	}
}

func TestBridgeManager_IsPublished(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bm := NewBridgeManager(logger, false)

	if bm.IsPublished("any-model") {
		t.Error("expected false for unpublished model")
	}
}

func TestBridgeManager_StopBridge_NotPublished(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bm := NewBridgeManager(logger, false)

	err := bm.StopBridge("missing")
	if err == nil {
		t.Fatal("expected error when stopping unpublished model")
	}
}

func TestBridgeManager_StopAll_Empty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bm := NewBridgeManager(logger, false)

	// Should not panic
	bm.StopAll()
}

func TestNewDaemon(t *testing.T) {
	d := New(Options{})
	if d == nil {
		t.Fatal("expected non-nil daemon")
	}
}

func TestStatusResponse_JSON(t *testing.T) {
	resp := StatusResponse{
		PID:    1234,
		Uptime: 60,
		Models: []ModelStatus{
			{Name: "model-a", State: "published"},
		},
	}
	if resp.PID != 1234 {
		t.Errorf("PID = %d, want 1234", resp.PID)
	}
	if len(resp.Models) != 1 {
		t.Errorf("expected 1 model, got %d", len(resp.Models))
	}
}

func TestPublishRequest_Empty(t *testing.T) {
	req := PublishRequest{Models: []PublishModelSpec{}}
	if len(req.Models) != 0 {
		t.Error("expected empty models list")
	}
}

func TestRotateLog(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/test.log"

	// Create a file larger than maxLogSize
	f, _ := os.Create(logPath)
	data := make([]byte, maxLogSize+1)
	f.Write(data)
	f.Close()

	rotateLog(logPath)

	// Original should be gone, .1 should exist
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("expected rotated file at %s.1: %v", logPath, err)
	}
}

func TestRotateLog_SmallFile(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/test.log"

	os.WriteFile(logPath, []byte("small"), 0600)

	rotateLog(logPath)

	// .1 should not exist
	if _, err := os.Stat(logPath + ".1"); err == nil {
		t.Error("did not expect rotation for small file")
	}
}

func TestRotateLog_NoFile(t *testing.T) {
	// Should not panic
	rotateLog("/nonexistent/path/test.log")
}
