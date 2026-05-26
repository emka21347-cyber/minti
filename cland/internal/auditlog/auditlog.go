// Package auditlog writes per-member audit events to ~/.minti/audit.jsonl.
//
// **Schema must stay byte-identical with mcp-servers/internal/audit/audit.go**
// — both daemons write to the same file, and a future Console UI reader (M6)
// expects one schema. We duplicate the code rather than promote the M2
// `internal/` package across module boundaries (per D-M4.6); the schema is
// frozen per PRD §6.4 / G9 so the divergence risk is bounded. If you change
// any field here, mirror it in mcp-servers/internal/audit/audit.go in the
// same commit.
//
// Concurrency: writes within a single cland process are mutex-guarded.
// Cross-process safety (cland + each spawned MCP server child) relies on
// POSIX O_APPEND + writes that fit in a single PIPE_BUF (4 KiB).
package auditlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event mirrors the schema in mcp-servers/internal/audit. New optional fields
// may be added at the end without breaking readers.
type Event struct {
	Timestamp  time.Time      `json:"ts"`
	MemberID   string         `json:"member_id"`
	Server     string         `json:"server"`
	Tool       string         `json:"tool"`
	Decision   string         `json:"decision"` // "allow" | "deny"
	Args       map[string]any `json:"args,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Error      string         `json:"error,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
}

// Logger is the interface every cland subsystem holds to write audit events.
// Single method — implementations are simple wrappers around a JSONL writer.
type Logger interface {
	Write(Event) error
}

// FileLogger appends one JSON event per line to a file.
type FileLogger struct {
	path string
	mu   sync.Mutex
}

// NewFileLogger returns a Logger that appends to `path`. Parent directories
// are created on demand; the file is created on first Write.
func NewFileLogger(path string) (*FileLogger, error) {
	if path == "" {
		return nil, fmt.Errorf("auditlog: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("auditlog: mkdir: %w", err)
	}
	return &FileLogger{path: path}, nil
}

// DefaultPath returns ~/.minti/audit.jsonl for the invoking user.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".minti", "audit.jsonl"), nil
}

func (l *FileLogger) Write(e Event) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.MemberID == "" {
		e.MemberID = "local"
	}

	b, err := json.Marshal(&e)
	if err != nil {
		return fmt.Errorf("auditlog: marshal: %w", err)
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("auditlog: open %s: %w", l.path, err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("auditlog: write: %w", err)
	}
	return nil
}
