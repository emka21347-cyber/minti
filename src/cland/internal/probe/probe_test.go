package probe

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------- CPU benchmark + EMA ----------

func TestCPUScore_Positive(t *testing.T) {
	p := New()
	hw := p.Sample()
	if hw.CPUScore <= 0 {
		t.Errorf("CPU score should be positive, got %d", hw.CPUScore)
	}
}

func TestSmoothedCPU_ConvergenceVariance(t *testing.T) {
	// With a deterministic benchmark override, EMA should converge after
	// the first sample (which seeds emaCPU = raw) and subsequent samples
	// stay at that value. Variance bound = 0.
	p := New()
	p.SetCPUBenchOverride(func() int { return 1500 })
	scores := make([]int, 10)
	for i := range scores {
		scores[i] = p.Sample().CPUScore
	}
	for i, s := range scores {
		if s != 1500 {
			t.Errorf("call %d: got %d, want 1500", i, s)
		}
	}
}

func TestSmoothedCPU_EMABlend(t *testing.T) {
	p := New()
	// First sample seeds EMA = 1000.
	p.SetCPUBenchOverride(func() int { return 1000 })
	_ = p.Sample()
	// Switch to 2000; EMA next = 0.3*2000 + 0.7*1000 = 1300.
	p.SetCPUBenchOverride(func() int { return 2000 })
	got := p.Sample().CPUScore
	if math.Abs(float64(got-1300)) > 1 {
		t.Errorf("expected EMA blend ~1300, got %d", got)
	}
}

// ---------- platform readers (smoke, just don't crash) ----------

func TestReadRAMGB_NonZero(t *testing.T) {
	got := readRAMGB()
	if got <= 0 {
		t.Errorf("RAMGB should be > 0 on this host (Linux + Windows test envs both have RAM); got %f", got)
	}
}

func TestReadUptime24h_InRange(t *testing.T) {
	got := readUptime24h()
	if got < 0 || got > 1 {
		t.Errorf("uptime_24h must be in [0,1]; got %f", got)
	}
}

func TestReadOnBattery_DoesntCrash(t *testing.T) {
	_ = readOnBattery()
}

func TestReadNvidiaSMI_DoesntCrash(t *testing.T) {
	vram, _ := readNvidiaSMI()
	if vram < 0 {
		t.Errorf("VRAM should never be negative; got %f", vram)
	}
	// We don't assert > 0 because non-NVIDIA hosts legitimately return 0.
}

// ---------- RuntimeClient ----------

func TestRuntimeClient_Caches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(RuntimeCapabilities{
			Kind: "ollama", Healthy: true,
			Models: []RuntimeModel{{Name: "llama3.2:3b", Resident: true}},
		})
	}))
	defer srv.Close()

	c := NewRuntimeClient(srv.URL, time.Minute)
	for i := 0; i < 5; i++ {
		caps, err := c.Get(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(caps.Models) != 1 {
			t.Errorf("call %d: model count wrong", i)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 upstream call (cached), got %d", calls)
	}
}

func TestRuntimeClient_RefreshesAfterTTL(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(RuntimeCapabilities{Kind: "ollama"})
	}))
	defer srv.Close()
	c := NewRuntimeClient(srv.URL, 10*time.Millisecond)
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("expected 2 upstream calls after TTL expiry, got %d", calls)
	}
}

func TestRuntimeClient_ReturnsStaleOnFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(RuntimeCapabilities{Kind: "ollama", Healthy: true})
	}))
	defer srv.Close()
	c := NewRuntimeClient(srv.URL, 10*time.Millisecond)
	first, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Healthy {
		t.Fatal("first call should succeed")
	}
	time.Sleep(30 * time.Millisecond)
	second, err := c.Get(context.Background())
	if err == nil {
		t.Errorf("second call should return error (upstream 503)")
	}
	if second == nil {
		t.Errorf("should return cached value alongside the error")
	}
}

func TestRuntimeClient_NoServerNoCache(t *testing.T) {
	c := NewRuntimeClient("http://127.0.0.1:1", 30*time.Second)
	_, err := c.Get(context.Background())
	if err == nil {
		t.Errorf("unreachable server with no cache should error")
	}
}

// ---------- helpers ----------

func TestResidentModels(t *testing.T) {
	caps := &RuntimeCapabilities{
		Models: []RuntimeModel{
			{Name: "llama3.2:3b", Resident: true},
			{Name: "qwen2.5:7b", Resident: false},
			{Name: "deepseek-r1:32b", Resident: true},
		},
	}
	got := caps.ResidentModels()
	want := map[string]bool{"llama3.2:3b": true, "deepseek-r1:32b": true}
	if len(got) != 2 {
		t.Fatalf("expected 2 resident, got %d", len(got))
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unexpected resident model %q", m)
		}
	}
}

func TestRemoteAPIs(t *testing.T) {
	if got := (&RuntimeCapabilities{}).RemoteAPIs(); got != nil {
		t.Errorf("empty caps → nil, got %v", got)
	}
	caps := &RuntimeCapabilities{RemoteAPIVendor: "anthropic"}
	if got := caps.RemoteAPIs(); len(got) != 1 || got[0] != "anthropic" {
		t.Errorf("expected [anthropic], got %v", got)
	}
}
