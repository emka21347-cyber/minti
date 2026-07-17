package panels

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// -update regenerates golden files instead of asserting against them.
// Run as: `go test ./internal/tui/panels/ -update` after a deliberate
// rendering change.
var updateGoldens = flag.Bool("update", false, "regenerate golden files")

// ansiEscape strips colour codes so goldens are stable regardless of
// terminal capabilities. Styling is verified by eye in the live smoke;
// structure is what these tests lock down.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI returns s with any ANSI CSI sequences removed, trailing
// whitespace removed per line, and any stray CR bytes dropped (defence
// in depth against CRLF-on-Windows checkouts — .gitattributes pins the
// goldens to LF but a misconfigured developer machine would otherwise
// break every assertGolden silently).
func stripANSI(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// assertGolden compares `got` against the golden file at
// testdata/golden/<name>.txt. With -update, writes `got` to the golden
// file (creating it if missing).
//
// Each line of `got` is right-trimmed and ANSI-stripped before
// comparison — colours and trailing padding are styling concerns, not
// structural.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	got = stripANSI(got)
	path := filepath.Join("testdata", "golden", name+".txt")

	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n(hint: run `go test ./... -update` to create it)", path, err)
	}

	if got != string(want) {
		t.Errorf("golden mismatch for %s.\n--- want ---\n%s\n--- got ---\n%s",
			name, string(want), got)
	}
}
