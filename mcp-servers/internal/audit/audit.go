// Package audit writes per-member tool-call audit events to ~/.minti/audit.jsonl.
//
// Each line is a single JSON object; the file is append-only. Format frozen now
// per PRD §6.4 / G9 so the M6 Console reader doesn't need a migration.
//
// Concurrency: writes within a single MCP server process are mutex-guarded.
// Cross-process safety relies on POSIX O_APPEND + writes ≤ PIPE_BUF (4 KiB);
// events larger than that are not currently fenced — documented limitation, OK
// for M2 because no MCP call's args are anywhere near that size.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one record in audit.jsonl. New optional fields may be added later;
// readers must tolerate unknown keys.
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

type Logger struct {
	path string
	mu   sync.Mutex
}

// NewLogger creates a logger that appends to path. The parent dir is created
// on demand; the file itself is created on the first Write.
func NewLogger(path string) (*Logger, error) {
	if path == "" {
		return nil, fmt.Errorf("audit: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("audit: mkdir %s: %w", filepath.Dir(path), err)
	}
	return &Logger{path: path}, nil
}

// DefaultPath returns ~/.minti/audit.jsonl for the invoking user.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".minti", "audit.jsonl"), nil
}

// Default opens (or creates) the audit log at DefaultPath.
func Default() (*Logger, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return NewLogger(p)
}

func (l *Logger) Write(e Event) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.MemberID == "" {
		e.MemberID = "local"
	}

	b, err := json.Marshal(&e)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", l.path, err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("audit: write %s: %w", l.path, err)
	}
	return nil
}
