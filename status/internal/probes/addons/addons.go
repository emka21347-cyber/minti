// Package addons reads /var/lib/minti/packs/*.installed marker files
// dropped by minti-pack-fetch (M6+). Each marker is a one-line JSON
// document; we pull the kind + timestamp.
package addons

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const markerDir = "/var/lib/minti/packs"

// Pack is one installed addon.
type Pack struct {
	Name string
	Kind string // "ollama-model" | "kiwix-zim"
	At   time.Time
}

// Probe scans markerDir for *.installed files. Empty result = no addons.
// Returns nil error on a missing dir (pre-M6 hosts have no /var/lib/minti/packs).
func Probe(ctx context.Context) ([]Pack, error) {
	entries, err := os.ReadDir(markerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Pack, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".installed") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".installed")
		p := Pack{Name: name}

		// Try to parse the JSON body for kind + timestamp; fall back
		// to filesystem mtime if the marker is malformed.
		if b, err := os.ReadFile(filepath.Join(markerDir, e.Name())); err == nil {
			var body struct {
				Kind      string    `json:"kind"`
				Timestamp time.Time `json:"timestamp"`
			}
			if json.Unmarshal(b, &body) == nil {
				p.Kind = body.Kind
				p.At = body.Timestamp
			}
		}
		if p.At.IsZero() {
			if fi, err := e.Info(); err == nil {
				p.At = fi.ModTime()
			}
		}
		out = append(out, p)
	}
	return out, nil
}
