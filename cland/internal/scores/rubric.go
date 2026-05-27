// Package scores computes the two routing scores defined in
// docs/clan-protocol.md §6.3 + §6.4:
//
//   - `reasoning_score`: per-backend integer, max across enabled/available
//     backends. Loaded from /etc/minti/reasoning-scores.yaml (a list of
//     `{backend, score}` entries with backend strings like
//     `local:llama3.1:70b-q4` or `remote-api:anthropic:claude-opus-4-7`).
//   - `system_score`: hardware-derived integer per the §6.4 formula. Uses
//     a sliding 5-minute window of failed cross-Clan requests as a penalty.
//
// All three are pure functions of explicit inputs — no globals, no I/O —
// to keep them trivially testable.
package scores

import (
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// RubricEntry is one row of /etc/minti/reasoning-scores.yaml.
type RubricEntry struct {
	Backend string `yaml:"backend"`
	Score   int    `yaml:"score"`
}

// Rubric is the loaded table — order-preserving so users can read the file
// top-to-bottom.
type Rubric struct {
	Entries []RubricEntry `yaml:"entries"`
}

// LoadRubric reads /etc/minti/reasoning-scores.yaml (or a custom path).
// Missing file → empty rubric + no error (the daemon will simply publish
// reasoning_score=0 until the user installs one).
func LoadRubric(path string) (*Rubric, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Rubric{}, nil
		}
		return nil, fmt.Errorf("scores: read %s: %w", path, err)
	}
	var r Rubric
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("scores: parse %s: %w", path, err)
	}
	for i, e := range r.Entries {
		if e.Backend == "" {
			return nil, fmt.Errorf("scores: entry %d has empty backend", i)
		}
		if e.Score < 0 || e.Score > 100 {
			return nil, fmt.Errorf("scores: entry %q score %d out of range [0,100]", e.Backend, e.Score)
		}
	}
	return &r, nil
}

// ReasoningScore returns the maximum rubric score over the intersection of
// rubric entries and the member's actually-available backends.
//
//   - residentModels — model names the local runtime currently has loaded
//     (e.g. "llama3.2:3b", "qwen2.5:7b"). Matched against rubric entries
//     of the form "local:<model>".
//   - remoteAPIs    — configured remote-api backends (e.g. ["anthropic",
//     "openai"]). Matched against rubric entries of the form
//     "remote-api:<vendor>:..." — any entry whose vendor token is in this
//     set is considered available.
//
// Returns 0 when no rubric entry matches anything available.
func ReasoningScore(rubric *Rubric, residentModels []string, remoteAPIs []string) int {
	if rubric == nil {
		return 0
	}
	residentSet := make(map[string]bool, len(residentModels))
	for _, m := range residentModels {
		residentSet[m] = true
	}
	remoteSet := make(map[string]bool, len(remoteAPIs))
	for _, v := range remoteAPIs {
		remoteSet[v] = true
	}
	best := 0
	for _, e := range rubric.Entries {
		if !backendAvailable(e.Backend, residentSet, remoteSet) {
			continue
		}
		if e.Score > best {
			best = e.Score
		}
	}
	return best
}

// backendAvailable parses entry strings like "local:llama3.1:70b-q4" and
// "remote-api:anthropic:claude-opus-4-7" and reports whether the host can
// actually serve it.
func backendAvailable(backend string, resident, remote map[string]bool) bool {
	// Strip kind prefix.
	for kind, isAvail := range map[string]func(string) bool{
		"local:":      func(s string) bool { return resident[s] },
		"remote-api:": func(s string) bool {
			// Vendor is the first segment after "remote-api:".
			vendor := s
			if idx := indexByte(s, ':'); idx >= 0 {
				vendor = s[:idx]
			}
			return remote[vendor]
		},
	} {
		if hasPrefix(backend, kind) {
			return isAvail(backend[len(kind):])
		}
	}
	return false
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

// ---------- system_score (§6.4) ----------

// Hardware is the input bundle the probe package will populate.
type Hardware struct {
	CPUScore           int     // benchmark normalized to 0..2000
	RAMGB              float64
	VRAMGB             float64
	NVMeThroughputGbps float64
	GPU                string
	OnBattery          bool
	Uptime24h          float64 // 0..1 fraction of last 24h up
}

// SystemScore evaluates the §6.4 formula. recentFailed is normalized 0..1
// per the v0.2 spec edit (count of failed cross-Clan requests in the last
// 5 min, divided by 20, clamped to [0,1]).
func SystemScore(hw Hardware, recentFailed float64) int {
	if recentFailed < 0 {
		recentFailed = 0
	}
	if recentFailed > 1 {
		recentFailed = 1
	}
	battery := 0.0
	if hw.OnBattery {
		battery = 1.0
	}
	uptime := clamp01(hw.Uptime24h)

	score := 0.40*normalize(hw.VRAMGB, 0, 48) +
		0.20*normalize(hw.RAMGB, 0, 128) +
		0.20*normalize(float64(hw.CPUScore), 0, 2000) +
		0.10*normalize(hw.NVMeThroughputGbps, 0, 10) +
		0.10*uptime -
		0.10*battery -
		0.10*recentFailed

	scaled := math.Round(score * 100.0)
	if scaled < 0 {
		scaled = 0
	}
	if scaled > 100 {
		scaled = 100
	}
	return int(scaled)
}

func normalize(x, lo, hi float64) float64 {
	if hi <= lo {
		return 0
	}
	v := (x - lo) / (hi - lo)
	return clamp01(v)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---------- RecentFailures sliding-window counter ----------

// FailureWindow is the spec §6.4 window over which we count failed
// cross-Clan requests. Saturates at FailureSaturation per the v0.2 spec.
const (
	FailureWindow     = 5 * time.Minute
	FailureSaturation = 20.0
)

// RecentFailures is a fixed-capacity ring buffer of failure timestamps.
// Concurrent calls are mutex-guarded. Process restart clears the counter
// per the v0.2 spec definition.
type RecentFailures struct {
	mu     sync.Mutex
	times  []time.Time
	window time.Duration
}

// NewRecentFailures returns a tracker over the spec-default 5-minute window.
func NewRecentFailures() *RecentFailures {
	return &RecentFailures{window: FailureWindow}
}

// Record adds a failure at `t`. Older entries past the window are pruned.
func (rf *RecentFailures) Record(t time.Time) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	cutoff := t.Add(-rf.window)
	kept := rf.times[:0]
	for _, ts := range rf.times {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	rf.times = append(kept, t)
}

// Count returns the number of failures in the trailing window relative to
// `now`. Side effect: prunes expired entries.
func (rf *RecentFailures) Count(now time.Time) int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	cutoff := now.Add(-rf.window)
	kept := rf.times[:0]
	for _, ts := range rf.times {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	rf.times = kept
	return len(kept)
}

// Normalized returns Count(now) / FailureSaturation, clamped to [0,1].
// Plug straight into SystemScore's recentFailed parameter.
func (rf *RecentFailures) Normalized(now time.Time) float64 {
	c := float64(rf.Count(now))
	v := c / FailureSaturation
	if v > 1 {
		return 1
	}
	return v
}
