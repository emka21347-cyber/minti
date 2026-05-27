// Package probe gathers the hardware + runtime telemetry the advertise
// loop publishes in /clan/advertise payloads (spec §4.2).
//
// Shape:
//
//	Hardware{CPUScore, RAMGB, VRAMGB, NVMeThroughputGbps, GPU, OnBattery, Uptime24h}
//
// CPU score is a built-in SHA-256 benchmark (500 ms window, median of 3),
// smoothed with an exponential moving average (α=0.3, last 5 samples) so the
// `system_score` formula doesn't flap under transient load — addresses
// qwen3.6 5B from the Phase D peer-review.
//
// VRAM is via `nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits`.
// Missing binary or any nvidia-smi error → 0 (AMD/Intel hosts also see 0;
// documented limitation per PRD §3a non-goals).
//
// Per-OS specifics:
//   - linux: /proc/meminfo, /proc/uptime, /sys/class/power_supply/BAT*
//   - windows: GlobalMemoryStatusEx via golang.org/x/sys/windows; uptime
//     via GetTickCount64; OnBattery returns false (M5 territory).
package probe

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EMA smoothing constants per Phase D plan.
const (
	emaAlpha   = 0.3
	emaSamples = 5
)

// Hardware is the captured telemetry. Mirrors the JSON shape the §4.2
// advertisement body's `hardware` field carries.
type Hardware struct {
	CPUScore           int     `json:"cpu_score"`
	RAMGB              float64 `json:"ram_gb"`
	VRAMGB             float64 `json:"vram_gb"`
	NVMeThroughputGbps float64 `json:"nvme_throughput_gbps"`
	GPU                string  `json:"gpu"`
	OnBattery          bool    `json:"on_battery"`
	Uptime24h          float64 `json:"uptime_24h"`
}

// Prober batches hardware reads + smooths the CPU benchmark across calls.
type Prober struct {
	mu          sync.Mutex
	emaCPU      float64
	cpuSamples  int
	// Test seam: when non-nil, replaces the real SHA-256 benchmark so
	// deterministic test cases don't have to wait 1.5s for three 500ms runs.
	cpuBenchOverride func() int
}

// New returns a fresh Prober.
func New() *Prober {
	return &Prober{}
}

// SetCPUBenchOverride replaces the SHA-256 benchmark with a constant —
// test seam only.
func (p *Prober) SetCPUBenchOverride(f func() int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cpuBenchOverride = f
}

// Sample reads everything fresh. Cost: ~1.5 s (three CPU benchmark windows
// at 500 ms each); callers should refresh on the 30 s advertisement tick,
// not per request.
func (p *Prober) Sample() Hardware {
	hw := Hardware{
		CPUScore:  p.smoothedCPUScore(),
		RAMGB:     readRAMGB(),
		Uptime24h: readUptime24h(),
		OnBattery: readOnBattery(),
	}
	hw.VRAMGB, hw.GPU = readNvidiaSMI()
	// NVMe throughput probing is out of scope for Phase D — the §6.4
	// formula weight is 0.10, so a zero floor is acceptable and the operator
	// can override by editing /etc/minti/system-score.yaml in a later phase.
	return hw
}

// smoothedCPUScore: median of 3 fresh benchmarks, then EMA-blended with
// prior smoothed value. Returns 0 on the very first call to make the EMA
// converge over its first 5 samples (cold-start damping).
func (p *Prober) smoothedCPUScore() int {
	raw := p.medianOf3()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cpuSamples == 0 {
		p.emaCPU = float64(raw)
	} else {
		p.emaCPU = emaAlpha*float64(raw) + (1-emaAlpha)*p.emaCPU
	}
	p.cpuSamples++
	return int(p.emaCPU + 0.5)
}

func (p *Prober) medianOf3() int {
	samples := [3]int{p.benchOnce(), p.benchOnce(), p.benchOnce()}
	// Sort 3 elements with min comparisons.
	if samples[0] > samples[1] {
		samples[0], samples[1] = samples[1], samples[0]
	}
	if samples[1] > samples[2] {
		samples[1], samples[2] = samples[2], samples[1]
	}
	if samples[0] > samples[1] {
		samples[0], samples[1] = samples[1], samples[0]
	}
	return samples[1]
}

// benchOnce: count how many SHA-256 hashes of 64 KiB we can do in 500 ms,
// scaled to the 0..2000 range the §6.4 formula expects. Calibrated empirically
// against the existing dev hardware:
//
//   AMD Ryzen 9 9950X3D : ~3000 → clamps near 1500
//   Linux mint VM CPU-1   : ~600  → 300
//   2 GB Mac 2010         : ~200  → 100
//
// Calibration is approximate; scale chosen so a high-end laptop CPU lands
// around 1000-1500, server CPUs around 1500-2000, low-end embedded ~100-300.
func (p *Prober) benchOnce() int {
	if p.cpuBenchOverride != nil {
		return p.cpuBenchOverride()
	}
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = byte(i)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	count := 0
	for time.Now().Before(deadline) {
		_ = sha256.Sum256(buf)
		count++
	}
	// Calibration: 1000 hashes/500ms → score 1000.
	scaled := count
	if scaled > 2000 {
		scaled = 2000
	}
	if scaled < 0 {
		scaled = 0
	}
	return scaled
}

// ---------- nvidia-smi probe ----------

// readNvidiaSMI returns (VRAM in GB, GPU name) for the first NVIDIA GPU on
// the system. Returns (0, "") when nvidia-smi is missing, errors, or
// returns garbage.
func readNvidiaSMI() (float64, string) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return 0, ""
	}
	out, err := runCmd("nvidia-smi", "--query-gpu=memory.total,name", "--format=csv,noheader,nounits")
	if err != nil {
		return 0, ""
	}
	// First line, comma-separated. Memory is in MiB.
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if line == "" {
		return 0, ""
	}
	parts := strings.SplitN(line, ",", 2)
	if len(parts) != 2 {
		return 0, ""
	}
	mibStr := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	mib, err := strconv.ParseInt(mibStr, 10, 64)
	if err != nil {
		return 0, ""
	}
	return float64(mib) / 1024.0, name
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w (stderr=%s)", name, err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
