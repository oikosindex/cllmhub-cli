package audit

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Entry represents a single audit log entry (metadata only, no payloads).
type Entry struct {
	Timestamp  time.Time `json:"timestamp"`
	RequestID  string    `json:"request_id"`
	Model      string    `json:"model"`
	Stream     bool      `json:"stream"`
	LatencyMs  int64     `json:"latency_ms,omitempty"`
	Tokens     int       `json:"tokens,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Logger writes JSON-lines audit entries to a file.
// A nil *Logger is safe to use; all methods are no-ops.
type Logger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// NewLogger opens (or creates) the log file with 0600 permissions.
func NewLogger(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f, enc: json.NewEncoder(f)}, nil
}

// Log writes an entry. It is a no-op on a nil receiver.
func (l *Logger) Log(e Entry) {
	if l == nil {
		return
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enc.Encode(e)
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
