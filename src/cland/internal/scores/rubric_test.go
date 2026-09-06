package scores

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------- Rubric loader ----------

func TestLoadRubric_MissingFileReturnsEmpty(t *testing.T) {
	r, err := LoadRubric(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 0 {
		t.Errorf("missing file should yield empty rubric, got %d entries", len(r.Entries))
	}
}

func TestLoadRubric_Parses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.yaml")
	mustWrite(t, path, `entries:
  - backend: "local:llama3.1:70b-q4"
    score: 72
  - backend: "remote-api:anthropic:claude-opus-4-7"
    score: 95
`)
	r, err := LoadRubric(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(r.Entries))
	}
	if r.Entries[1].Backend != "remote-api:anthropic:claude-opus-4-7" {
		t.Errorf("entry 1 wrong: %+v", r.Entries[1])
	}
}

func TestLoadRubric_RejectsBadInputs(t *testing.T) {
	dir := t.TempDir()

	cases := map[string]string{
		"empty backend":         "entries:\n  - backend: \"\"\n    score: 50\n",
		"negative score":        "entries:\n  - backend: \"x\"\n    score: -1\n",
		"score over 100":        "entries:\n  - backend: \"x\"\n    score: 101\n",
		"malformed yaml":        ": not valid",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".yaml")
			mustWrite(t, path, content)
			if _, err := LoadRubric(path); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---------- ReasoningScore ----------

func TestReasoningScore_MaxOverAvailable(t *testing.T) {
	r := &Rubric{Entries: []RubricEntry{
		{Backend: "local:llama3.2:3b", Score: 35},
		{Backend: "local:llama3.1:70b-q4", Score: 72},
		{Backend: "remote-api:anthropic:claude-opus-4-7", Score: 95},
	}}
	// Only the 3B is resident, no remote APIs configured → 35.
	got := ReasoningScore(r, []string{"llama3.2:3b"}, nil)
	if got != 35 {
		t.Errorf("got %d, want 35", got)
	}
	// Add 70B → max becomes 72.
	got = ReasoningScore(r, []string{"llama3.2:3b", "llama3.1:70b-q4"}, nil)
	if got != 72 {
		t.Errorf("got %d, want 72", got)
	}
	// Add Anthropic remote → max becomes 95.
	got = ReasoningScore(r, []string{"llama3.2:3b"}, []string{"anthropic"})
	if got != 95 {
		t.Errorf("got %d, want 95", got)
	}
}

func TestReasoningScore_NoMatchReturnsZero(t *testing.T) {
	r := &Rubric{Entries: []RubricEntry{
		{Backend: "local:hermes3:8b", Score: 58},
	}}
	got := ReasoningScore(r, []string{"phi-3.5:mini"}, nil)
	if got != 0 {
		t.Errorf("no match should return 0, got %d", got)
	}
}

func TestReasoningScore_NilRubric(t *testing.T) {
	if got := ReasoningScore(nil, []string{"anything"}, nil); got != 0 {
		t.Errorf("nil rubric should return 0, got %d", got)
	}
}

func TestReasoningScore_UnknownBackendKindIgnored(t *testing.T) {
	r := &Rubric{Entries: []RubricEntry{
		{Backend: "weird-kind:foo", Score: 999}, // not local: / remote-api:
		{Backend: "local:llama3.2:3b", Score: 35},
	}}
	got := ReasoningScore(r, []string{"llama3.2:3b"}, nil)
	if got != 35 {
		t.Errorf("unknown kind should be ignored; expected 35, got %d", got)
	}
}

// ---------- SystemScore (§6.4 hand-computed expected values) ----------

func TestSystemScore_FullSpec(t *testing.T) {
	// 5090-class: VRAM=32, RAM=64, CPU=1500, NVMe=5, uptime=0.95, not on battery, 0 fails.
	hw := Hardware{
		CPUScore:           1500,
		RAMGB:              64,
		VRAMGB:             32,
		NVMeThroughputGbps: 5,
		Uptime24h:          0.95,
		OnBattery:          false,
	}
	// 0.40 * (32/48) + 0.20 * (64/128) + 0.20 * (1500/2000) + 0.10 * (5/10) + 0.10 * 0.95 - 0 - 0
	//      = 0.40 * 0.6667 + 0.20 * 0.5 + 0.20 * 0.75 + 0.10 * 0.5 + 0.10 * 0.95
	//      = 0.2667 + 0.1 + 0.15 + 0.05 + 0.095 = 0.6617
	// * 100 → 66 (rounded).
	got := SystemScore(hw, 0)
	if got < 65 || got > 67 {
		t.Errorf("5090 system_score = %d, want ~66", got)
	}
}

func TestSystemScore_OnBatteryPenalises(t *testing.T) {
	hw := Hardware{CPUScore: 1500, RAMGB: 64, VRAMGB: 32, NVMeThroughputGbps: 5, Uptime24h: 0.95}
	plugged := SystemScore(hw, 0)
	hw.OnBattery = true
	battery := SystemScore(hw, 0)
	if battery >= plugged {
		t.Errorf("on_battery should reduce score: plugged=%d battery=%d", plugged, battery)
	}
	if plugged-battery != 10 {
		t.Errorf("on_battery should subtract exactly 10 (0.10*100); got delta %d", plugged-battery)
	}
}

func TestSystemScore_RecentFailedPenalises(t *testing.T) {
	hw := Hardware{CPUScore: 1500, RAMGB: 64, VRAMGB: 32, NVMeThroughputGbps: 5, Uptime24h: 0.95}
	clean := SystemScore(hw, 0)
	saturated := SystemScore(hw, 1.0)
	if clean-saturated != 10 {
		t.Errorf("recent_failed=1.0 should subtract exactly 10; got delta %d", clean-saturated)
	}
}

func TestSystemScore_ZeroHardware(t *testing.T) {
	if got := SystemScore(Hardware{}, 0); got != 0 {
		t.Errorf("zero hardware → 0, got %d", got)
	}
}

func TestSystemScore_LowSpec(t *testing.T) {
	// 2GB Mac: VRAM=0, RAM=2, CPU=300, NVMe=0, uptime=0.5, on battery.
	hw := Hardware{
		CPUScore:           300,
		RAMGB:              2,
		Uptime24h:          0.5,
		OnBattery:          true,
	}
	got := SystemScore(hw, 0)
	// 0 + 0.20*(2/128) + 0.20*(300/2000) + 0 + 0.10*0.5 - 0.10*1
	// = 0 + 0.003125 + 0.03 + 0.05 - 0.1 = -0.0169 → clamped to 0
	if got != 0 {
		t.Errorf("low-spec on-battery node should clamp to 0; got %d", got)
	}
}

func TestSystemScore_Saturates(t *testing.T) {
	// Extreme over-cap inputs should clamp to 100.
	hw := Hardware{CPUScore: 999_999, RAMGB: 9999, VRAMGB: 9999, NVMeThroughputGbps: 9999, Uptime24h: 1}
	got := SystemScore(hw, 0)
	if got != 100 {
		t.Errorf("over-saturated hardware → 100, got %d", got)
	}
}

// ---------- RecentFailures sliding window ----------

func TestRecentFailures_BasicCountAndPrune(t *testing.T) {
	now := time.Now()
	rf := NewRecentFailures()
	for i := 0; i < 5; i++ {
		rf.Record(now.Add(time.Duration(-i) * time.Second))
	}
	if got := rf.Count(now); got != 5 {
		t.Errorf("count within window: got %d, want 5", got)
	}
}

func TestRecentFailures_PrunesPastWindow(t *testing.T) {
	now := time.Now()
	rf := NewRecentFailures()
	rf.Record(now.Add(-10 * time.Minute)) // outside 5-min window
	rf.Record(now.Add(-2 * time.Minute))
	rf.Record(now.Add(-30 * time.Second))
	if got := rf.Count(now); got != 2 {
		t.Errorf("count should drop expired: got %d, want 2", got)
	}
}

func TestRecentFailures_NormalizedSaturates(t *testing.T) {
	now := time.Now()
	rf := NewRecentFailures()
	for i := 0; i < 30; i++ {
		rf.Record(now.Add(time.Duration(-i) * time.Second))
	}
	got := rf.Normalized(now)
	if got != 1.0 {
		t.Errorf("30 failures should saturate to 1.0; got %f", got)
	}
}

func TestRecentFailures_NormalizedFractional(t *testing.T) {
	now := time.Now()
	rf := NewRecentFailures()
	for i := 0; i < 10; i++ {
		rf.Record(now.Add(time.Duration(-i) * time.Second))
	}
	got := rf.Normalized(now)
	if math.Abs(got-0.5) > 0.001 {
		t.Errorf("10/20 → 0.5; got %f", got)
	}
}
