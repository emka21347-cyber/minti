// Package advertise runs the §4.2 capability-advertisement loop. Every
// `interval` (30 s default) it builds a fresh ad payload — hardware probe +
// runtime-adapter capabilities + computed scores — and POSTs it to every
// peer in the registry. Phase D timing per the plan:
//
//   - First ad deferred 5 s to let runtime-adapter come up (qwen3.6 fix).
//   - `Bump()` triggers an immediate broadcast, rate-limited to once per
//     second (the on-capability-change path).
//   - Per-peer failures (timeout, 5xx, HMAC-reject) record into the shared
//     `scores.RecentFailures` so the NEXT system_score is penalised.
//
// The package depends on peers (Registry), probe (hardware + runtime
// capabilities), scores (rubric + RecentFailures), and transport (HTTPS +
// HMAC client). It does NOT speak to the spec §5 election heartbeat — that's
// Phase E.
package advertise

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/probe"
	"github.com/minti/cland/internal/scores"
	"github.com/minti/cland/internal/transport"
)

const (
	DefaultInterval    = 30 * time.Second
	DefaultInitialWait = 5 * time.Second
	DefaultBumpRate    = 1 * time.Second
)

// Service is the periodic broadcaster.
type Service struct {
	ClanID         string
	MemberID       string
	LANAddress     string // our listen address — included in every ad so peers register our LISTEN port, not the incoming TCP source port
	Registry       *peers.Registry
	Prober         *probe.Prober
	RuntimeClient  *probe.RuntimeClient
	Rubric         *scores.Rubric
	RecentFailures *scores.RecentFailures
	Client         *transport.Client
	Log            *slog.Logger

	Interval    time.Duration
	InitialWait time.Duration
	BumpRate    time.Duration

	generation atomic.Uint64
	lastBumpNs atomic.Int64
	bumpCh     chan struct{}

	startOnce sync.Once
}

// Start spawns the broadcast goroutine. Blocks until ctx is cancelled.
// Idempotent — second call is a no-op.
func (s *Service) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		s.bumpCh = make(chan struct{}, 1)
		if s.Interval <= 0 {
			s.Interval = DefaultInterval
		}
		if s.InitialWait < 0 {
			s.InitialWait = DefaultInitialWait
		}
		if s.BumpRate <= 0 {
			s.BumpRate = DefaultBumpRate
		}
		if s.Log == nil {
			s.Log = slog.Default()
		}
		go s.run(ctx)
	})
}

// Bump asks the loop to broadcast right now. Rate-limited to once per
// `BumpRate` window (silently dropped if too soon after the previous bump
// or scheduled broadcast).
func (s *Service) Bump() {
	now := time.Now().UnixNano()
	last := s.lastBumpNs.Load()
	if last > 0 && time.Duration(now-last) < s.BumpRate {
		return
	}
	s.lastBumpNs.Store(now)
	select {
	case s.bumpCh <- struct{}{}:
	default:
		// Channel buffered at 1; an already-queued bump covers us.
	}
}

func (s *Service) run(ctx context.Context) {
	// Deferred first broadcast — give minti-runtime time to start so the
	// first ad doesn't wrongly say inference.enabled=false.
	if s.InitialWait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.InitialWait):
		}
	}
	s.broadcast(ctx)

	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.broadcast(ctx)
		case <-s.bumpCh:
			s.broadcast(ctx)
		}
	}
}

// broadcast: build the ad payload once, fan-out POST to every peer in the
// registry. Failures recorded in RecentFailures for the NEXT system_score.
func (s *Service) broadcast(ctx context.Context) {
	gen := s.generation.Add(1)
	payload, err := s.buildPayload(ctx, gen)
	if err != nil {
		s.Log.Warn("advertise: build payload failed", "err", err)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.Log.Warn("advertise: marshal failed", "err", err)
		return
	}

	// Send to every known address — both bound members (freshness updates)
	// AND candidate addresses (introductions to peers we've only seen via
	// mDNS/peer-add). Without the candidate path, the chicken-and-egg
	// problem keeps the registry frozen.
	candidates, members := s.Registry.Snapshot()
	addrs := make(map[string]struct{})
	for _, m := range members {
		if m.MemberID == s.MemberID || m.Address == "" {
			continue
		}
		addrs[m.Address] = struct{}{}
	}
	for _, c := range candidates {
		if c.Address != "" {
			addrs[c.Address] = struct{}{}
		}
	}
	if len(addrs) == 0 {
		return
	}

	var wg sync.WaitGroup
	for addr := range addrs {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			s.sendOne(ctx, a, body)
		}(addr)
	}
	wg.Wait()
}

func (s *Service) sendOne(ctx context.Context, addr string, body []byte) {
	url := "https://" + addr + "/clan/advertise"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.RecentFailures.Record(time.Now())
		s.Log.Warn("advertise: build request failed", "addr", addr, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		s.RecentFailures.Record(time.Now())
		s.Log.Debug("advertise: POST failed", "addr", addr, "err", err)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusUnauthorized {
		s.RecentFailures.Record(time.Now())
		s.Log.Debug("advertise: peer rejected", "addr", addr, "status", resp.StatusCode)
	}
}

// buildPayload constructs the §4.2 advertisement body.
func (s *Service) buildPayload(ctx context.Context, generation uint64) (*peers.Advertisement, error) {
	if s.Prober == nil || s.RuntimeClient == nil || s.Rubric == nil || s.RecentFailures == nil {
		return nil, fmt.Errorf("advertise: misconfigured service (nil dep)")
	}
	hw := s.Prober.Sample()
	caps, _ := s.RuntimeClient.Get(ctx) // tolerate transient failures
	residentModels := caps.ResidentModels()
	remoteAPIs := caps.RemoteAPIs()

	reasoningScore := scores.ReasoningScore(s.Rubric, residentModels, remoteAPIs)
	systemScore := scores.SystemScore(toScoresHardware(hw), s.RecentFailures.Normalized(time.Now()))

	cap := map[string]any{
		"reasoning": map[string]any{
			"enabled":  reasoningScore > 0,
			"backends": backendsFromScores(s.Rubric, residentModels, remoteAPIs),
		},
		"inference": map[string]any{
			"enabled":         caps != nil && caps.Healthy,
			"models_resident": residentModels,
		},
		"vision-gen": map[string]any{"enabled": false},
		"embeddings": map[string]any{"enabled": false},
		"tools":      map[string]any{"enabled": false},
		"storage":    map[string]any{"enabled": false},
	}
	return &peers.Advertisement{
		MemberID:       s.MemberID,
		ClanID:         s.ClanID,
		Generation:     generation,
		OS:             runtime.GOOS,
		LANAddress:     s.LANAddress,
		Hardware: map[string]any{
			"cpu_score":             hw.CPUScore,
			"ram_gb":                hw.RAMGB,
			"vram_gb":               hw.VRAMGB,
			"gpu":                   hw.GPU,
			"on_battery":            hw.OnBattery,
			"uptime_24h":            hw.Uptime24h,
			"nvme_throughput_gbps":  hw.NVMeThroughputGbps,
		},
		ReasoningScore: reasoningScore,
		SystemScore:    systemScore,
		Capabilities:   cap,
		Load:           0, // Phase D doesn't measure live load; Phase F's router will populate
	}, nil
}

func toScoresHardware(hw probe.Hardware) scores.Hardware {
	return scores.Hardware{
		CPUScore:           hw.CPUScore,
		RAMGB:              hw.RAMGB,
		VRAMGB:             hw.VRAMGB,
		NVMeThroughputGbps: hw.NVMeThroughputGbps,
		GPU:                hw.GPU,
		OnBattery:          hw.OnBattery,
		Uptime24h:          hw.Uptime24h,
	}
}

// backendsFromScores assembles the per-backend list the §4.2 schema expects
// inside `capabilities.reasoning.backends`. One entry per matching rubric row.
func backendsFromScores(rubric *scores.Rubric, residentModels, remoteAPIs []string) []map[string]any {
	if rubric == nil {
		return nil
	}
	resident := setOf(residentModels)
	remote := setOf(remoteAPIs)
	out := make([]map[string]any, 0)
	for _, e := range rubric.Entries {
		if !available(e.Backend, resident, remote) {
			continue
		}
		entry := map[string]any{
			"backend":         e.Backend,
			"reasoning_score": e.Score,
			"available":       true,
		}
		out = append(out, entry)
	}
	return out
}

func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func available(backend string, resident, remote map[string]bool) bool {
	const localP = "local:"
	const remoteP = "remote-api:"
	if len(backend) >= len(localP) && backend[:len(localP)] == localP {
		return resident[backend[len(localP):]]
	}
	if len(backend) >= len(remoteP) && backend[:len(remoteP)] == remoteP {
		rest := backend[len(remoteP):]
		// First segment of the rest is the vendor.
		end := len(rest)
		for i := 0; i < len(rest); i++ {
			if rest[i] == ':' {
				end = i
				break
			}
		}
		return remote[rest[:end]]
	}
	return false
}
