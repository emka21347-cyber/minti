package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite_CreatesFileAndDir(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "audit.jsonl")
	l, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	if err := l.Write(Event{Server: "minti-mcp-fs", Tool: "whoami", Decision: "allow"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("audit file not created: %v", err)
	}
}

func TestWrite_DefaultsMemberAndTimestamp(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewLogger(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Write(Event{Server: "s", Tool: "t", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(logPath)
	line := strings.TrimSpace(string(b))
	var e Event
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.MemberID != "local" {
		t.Errorf("member_id = %q, want local", e.MemberID)
	}
	if e.Timestamp.IsZero() {
		t.Errorf("timestamp not auto-set")
	}
}

func TestWrite_AppendsMultiple(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewLogger(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := l.Write(Event{Server: "s", Tool: "t", Decision: "allow"}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(logPath)
	lines := strings.Count(strings.TrimRight(string(b), "\n"), "\n") + 1
	if lines != 3 {
		t.Errorf("got %d lines, want 3", lines)
	}
}
