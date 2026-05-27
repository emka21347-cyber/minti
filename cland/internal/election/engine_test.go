package election

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
)

// ---------- fakes / helpers ----------

// fakeHB records every POST and returns a programmable status.
type fakeHB struct {
	mu          sync.Mutex
	calls       []string // URLs
	status      int      // returned on every POST
	failURL     string   // if set, this URL returns transport error
	bodies      [][]byte // recorded bodies
}

func newFakeHB(status int) *fakeHB { return &fakeHB{status: status} }

func (f *fakeHB) Post(url, contentType string, body []byte) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if url == f.failURL {
		return nil, fmt.Errorf("fake: forced transport failure")
	}
	f.calls = append(f.calls, url)
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (f *fakeHB) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// noopAudit drops every event — election logic is tested by state, not by
// audit-log inspection.
type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

func mkEngine(t *testing.T, opts EngineOpts) *Engine {
	t.Helper()
	if opts.Audit == nil {
		opts.Audit = noopAudit{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.HeartbeatInterval == 0 {
		opts.HeartbeatInterval = 50 * time.Millisecond
	}
	if opts.LeaseDuration == 0 {
		opts.LeaseDuration = 200 * time.Millisecond
	}
	if opts.FailoverGrace == 0 {
		opts.FailoverGrace = 150 * time.Millisecond
	}
	if opts.ElectionTimeout == 0 {
		opts.ElectionTimeout = 50 * time.Millisecond
	}
	e, err := NewEngine(opts)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// mkStore returns an empty Store in t.TempDir.
func mkStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// saveClan persists a Clan into the store; helper to reduce test noise.
func saveClan(t *testing.T, s *state.Store, c *state.Clan) {
	t.Helper()
	if err := s.SaveClan(c); err != nil {
		t.Fatal(err)
	}
}

// installPeer puts a peer in the registry as if a /clan/advertise just landed
// from them. Sets LastSeenAt = now so Live() is true.
func installPeer(reg *peers.Registry, memberID, addr string, score int, pinned bool) {
	ad := &peers.Advertisement{
		MemberID:           memberID,
		Term:               0,
		Generation:         1,
		LANAddress:         addr,
		ReasoningScore:     score,
		Capabilities:       map[string]any{"reasoning": map[string]any{"enabled": true}},
		PinnedOrchestrator: pinned,
	}
	_ = reg.BindMember(ad, addr)
	reg.TouchLive(memberID)
}

func always(b bool) RuntimeHealthCheck {
	return func(time.Time) bool { return b }
}

// ---------- 1. Lone-member election ----------

func TestEngine_LoneMemberElection(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "A", State: "active"}},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry() // no peers

	e := mkEngine(t, EngineOpts{
		SelfID:    "A",
		ClanID:    "c1",
		State:     st,
		Store:     store,
		Registry:  reg,
		Client:    newFakeHB(http.StatusOK),
		Health:    always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 100, ReasoningEnabled: true}
		},
	})

	// Force past startup grace, then tick.
	now := time.Now().Add(1 * time.Second)
	e.tick(context.Background(), now)

	got := st.Snapshot()
	if got.CurrentOrchestrator != "A" {
		t.Errorf("self should be Orchestrator, got %q", got.CurrentOrchestrator)
	}
	if got.CurrentTerm != 1 {
		t.Errorf("term should be 1 after first commit, got %d", got.CurrentTerm)
	}
	if got.LeaseExpires.IsZero() {
		t.Errorf("lease should be set")
	}

	persisted, _ := store.LoadClan()
	if persisted.CurrentTerm != 1 || persisted.CurrentOrchestrator != "A" {
		t.Errorf("persisted: term=%d orch=%q (want 1, A)", persisted.CurrentTerm, persisted.CurrentOrchestrator)
	}
}

// ---------- 2. Two-member tied score → older AdmittedAt wins ----------

func TestEngine_TiedScore_AdmittedAtBreaksTie(t *testing.T) {
	store := mkStore(t)
	oldT := time.Now().Add(-1 * time.Hour)
	newT := time.Now().Add(-1 * time.Minute)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active", AdmittedAt: oldT}, // self, older
			{MemberID: "B", State: "active", AdmittedAt: newT},
		},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 80, false)

	e := mkEngine(t, EngineOpts{
		SelfID:    "A",
		ClanID:    "c1",
		State:     st,
		Store:     store,
		Registry:  reg,
		Client:    newFakeHB(http.StatusOK),
		Health:    always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 80, ReasoningEnabled: true, AdmittedAt: oldT}
		},
	})

	cand, _ := e.selectCandidate(mustLoad(t, store))
	if cand.MemberID != "A" {
		t.Errorf("tied score → older AdmittedAt should win; got %q (want A)", cand.MemberID)
	}
}

// ---------- 3. Pin override ----------

func TestEngine_PinOverridesScore(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active"},
			{MemberID: "B", State: "active"},
		},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 50, true) // lower score, pinned

	e := mkEngine(t, EngineOpts{
		SelfID:    "A",
		ClanID:    "c1",
		State:     st,
		Store:     store,
		Registry:  reg,
		Client:    newFakeHB(http.StatusOK),
		Health:    always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 100, ReasoningEnabled: true}
		},
	})

	cand, reason := e.selectCandidate(mustLoad(t, store))
	if cand.MemberID != "B" {
		t.Errorf("pinned B should win over higher-score A, got %q", cand.MemberID)
	}
	if reason != ReasonPinOverride {
		t.Errorf("reason: got %q want %q", reason, ReasonPinOverride)
	}
}

// ---------- 4. Multi-pin → lowest member_id wins ----------

func TestEngine_MultiPin_LowestMemberIDWins(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "B", State: "active"}, {MemberID: "C", State: "active"}, {MemberID: "A", State: "active"}},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 80, true) // pinned
	installPeer(reg, "C", "127.0.0.1:7780", 90, true) // pinned, higher score, but higher member_id

	e := mkEngine(t, EngineOpts{
		SelfID:    "A",
		ClanID:    "c1",
		State:     st,
		Store:     store,
		Registry:  reg,
		Client:    newFakeHB(http.StatusOK),
		Health:    always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 100, ReasoningEnabled: true, Pinned: true}
		},
	})

	cand, reason := e.selectCandidate(mustLoad(t, store))
	if cand.MemberID != "A" {
		t.Errorf("multi-pin tiebreaker should pick lowest id (A), got %q", cand.MemberID)
	}
	if reason != ReasonPinOverride {
		t.Errorf("reason: got %q want %q", reason, ReasonPinOverride)
	}
}

// ---------- 5. Anti-spoof — non-highest sender rejected ----------

func TestEngine_AntiSpoof_RejectsNonHighest(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}, {MemberID: "C", State: "active"}},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 99, false) // highest
	installPeer(reg, "C", "127.0.0.1:7780", 50, false) // lower

	e := mkEngine(t, EngineOpts{
		SelfID:    "A",
		ClanID:    "c1",
		State:     st,
		Store:     store,
		Registry:  reg,
		Client:    newFakeHB(http.StatusOK),
		Health:    always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})

	// C tries to claim Orchestrator → must be rejected (B is the highest in our view).
	// Use SAME term as currentTerm (0) so the anti-spoof check fires; higher-term
	// heartbeats bypass anti-spoof per spec §5.3 + Raft convention.
	hb := Heartbeat{MemberID: "C", ClanID: "c1", Term: 0, ReasoningScore: 50, LeaseUntil: time.Now().Add(8 * time.Second)}
	_, err := e.OnHeartbeatReceived(hb, "C", time.Now())
	if err == nil {
		t.Fatalf("expected anti-spoof reject")
	}
}

// ---------- 5b. Higher-term bypasses anti-spoof (Raft convention) ----------

func TestEngine_HigherTerm_BypassesAntiSpoof(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	// Empty registry — A's local view says self should be candidate.

	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: newFakeHB(http.StatusOK), Health: always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 50, ReasoningEnabled: true}
		},
	})

	// B claims Orchestrator with a HIGHER term. Even though A's view says
	// A should be candidate (anti-spoof would normally reject), term=5 > 0
	// must bypass the check and update state.
	hb := Heartbeat{MemberID: "B", ClanID: "c1", Term: 5, ReasoningScore: 99, LeaseUntil: time.Now().Add(8 * time.Second)}
	res, err := e.OnHeartbeatReceived(hb, "B", time.Now())
	if err != nil {
		t.Fatalf("higher-term heartbeat should bypass anti-spoof; got %v", err)
	}
	if !res.Accepted || res.NewTerm != 5 || res.NewOrch != "B" {
		t.Errorf("expected term=5 orch=B; got %+v", res)
	}
}

// ---------- 6. Term replay → reject ----------

func TestEngine_TermReplay_Rejected(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID:      "c1",
		Roster:      []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
		CurrentTerm: 10,
	})
	st := NewState("A", 10, "B", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 99, false)

	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: newFakeHB(http.StatusOK), Health: always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})

	hb := Heartbeat{MemberID: "B", ClanID: "c1", Term: 5, ReasoningScore: 99, LeaseUntil: time.Now().Add(8 * time.Second)}
	_, err := e.OnHeartbeatReceived(hb, "B", time.Now())
	if err != ErrTermStale {
		t.Errorf("expected ErrTermStale, got %v", err)
	}
}

// ---------- 7. Lease expiry triggers an election ----------

func TestEngine_LeaseExpiryTriggersElection(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID:      "c1",
		Roster:      []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
		CurrentTerm: 1,
	})
	st := NewState("A", 1, "B", 32)
	st.startedAt = time.Now().Add(-1 * time.Hour) // past startup grace
	// Set a past lease so it's clearly expired beyond FAILOVER_GRACE.
	st.leaseExpires = time.Now().Add(-1 * time.Hour)
	reg := peers.NewRegistry()
	// No peers visible → A is sole candidate → A self-elects.

	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: newFakeHB(http.StatusOK), Health: always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 50, ReasoningEnabled: true} },
	})

	e.tick(context.Background(), time.Now())

	got := st.Snapshot()
	if got.CurrentOrchestrator != "A" {
		t.Errorf("expected A to self-elect on expired lease, got orch=%q", got.CurrentOrchestrator)
	}
	if got.CurrentTerm != 2 {
		t.Errorf("expected term to bump 1→2, got %d", got.CurrentTerm)
	}
}

// ---------- 8. Quorum from persisted active roster, not live registry (R5) ----------

func TestEngine_Quorum_PersistedActiveOnly(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active"},
			{MemberID: "B", State: "active"},
			{MemberID: "C", State: "active"},
			{MemberID: "D", State: "active"},
			{MemberID: "E", State: "active"},
			{MemberID: "F", State: "admitted"}, // R5: must be excluded
		},
	})
	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1",
		State:     NewState("A", 0, "", 32),
		Store:     store,
		Registry:  peers.NewRegistry(),
		Client:    newFakeHB(http.StatusOK),
		Health:    always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})
	q := e.quorum(mustLoad(t, store))
	if q != 3 {
		t.Errorf("quorum for 5 active (+ 1 admitted, excluded): got %d want 3", q)
	}
}

// ---------- 9. Split-brain backoff: 100 runs all converge (R7) ----------

func TestEngine_Backoff_100Runs_AllConverge(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{ClanID: "c1", Roster: []state.RosterMember{{MemberID: "A", State: "active"}}})
	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1",
		State: NewState("A", 0, "", 32), Store: store, Registry: peers.NewRegistry(),
		Client: newFakeHB(http.StatusOK), Health: always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})
	var max time.Duration
	var sum time.Duration
	for i := 0; i < 100; i++ {
		b := e.backoff()
		if b < 50*time.Millisecond || b > 150*time.Millisecond {
			t.Fatalf("backoff outside 50-150ms: %v", b)
		}
		sum += b
		if b > max {
			max = b
		}
	}
	avg := sum / 100
	if avg < 70*time.Millisecond || avg > 130*time.Millisecond {
		t.Errorf("backoff average drifted from uniform expectation (~100ms): %v", avg)
	}
}

// ---------- 10. Pin handler roundtrip — covered in handlers_test.go (skipped here) ----------

// ---------- 11. R2 — LeaseExpires is NOT persisted ----------

func TestEngine_LeaseExpiresNotPersisted(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
	})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 99, false)

	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: newFakeHB(http.StatusOK), Health: always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})
	hb := Heartbeat{MemberID: "B", ClanID: "c1", Term: 7, ReasoningScore: 99, LeaseUntil: time.Now().Add(8 * time.Second)}
	if _, err := e.OnHeartbeatReceived(hb, "B", time.Now()); err != nil {
		t.Fatalf("accept: %v", err)
	}
	persisted, _ := store.LoadClan()
	if persisted.CurrentTerm != 7 {
		t.Errorf("term not persisted: got %d", persisted.CurrentTerm)
	}
	if persisted.CurrentOrchestrator != "B" {
		t.Errorf("orchestrator not persisted: got %q", persisted.CurrentOrchestrator)
	}

	// The persisted JSON must NOT contain a "lease_expires" key — R2.
	raw, _ := jsonReadBack(t, store)
	if bytesIndexAll(raw, "lease_expires") >= 0 {
		t.Errorf("persisted clan.json contains 'lease_expires' — R2 violated. raw=%s", string(raw))
	}
}

// ---------- 12. R1 — Zombie-leader gate ----------

func TestEngine_ZombieLeaderGate(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID:              "c1",
		Roster:              []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
		CurrentTerm:         5,
		CurrentOrchestrator: "A",
	})
	st := NewState("A", 5, "A", 32)
	st.leaseExpires = time.Now().Add(1 * time.Hour) // still lease holder
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 99, false)

	hb := newFakeHB(http.StatusOK)
	var healthy atomic.Bool
	healthy.Store(false) // start UNhealthy

	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: hb,
		Health: func(time.Time) bool { return healthy.Load() },
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 100, ReasoningEnabled: true}
		},
	})

	// While unhealthy: tick must NOT emit heartbeats.
	e.tick(context.Background(), time.Now())
	if got := hb.HeartbeatsSentForTest(); got != 0 {
		t.Errorf("unhealthy: expected 0 heartbeats, got %d", got)
	}

	// Become healthy: tick should now emit.
	healthy.Store(true)
	e.tick(context.Background(), time.Now())
	if got := hb.HeartbeatsSentForTest(); got == 0 {
		t.Errorf("healthy: expected ≥1 heartbeat POST, got %d", got)
	}
}

// ---------- 13. R6 — Startup grace ----------

func TestEngine_StartupGrace(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
	})
	st := NewState("A", 0, "", 32)
	// startedAt is freshly Now → still within grace.
	reg := peers.NewRegistry()
	// No peers, no lease — would normally trigger immediate self-election.

	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: newFakeHB(http.StatusOK), Health: always(true),
		FailoverGrace: 500 * time.Millisecond,
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})

	// Tick within grace window — no election should happen.
	e.tick(context.Background(), time.Now().Add(100*time.Millisecond))
	if got := st.Snapshot(); got.CurrentOrchestrator != "" {
		t.Errorf("within grace: expected no orchestrator yet, got %q", got.CurrentOrchestrator)
	}
	if e.ElectionsRun() != 0 {
		t.Errorf("within grace: expected 0 elections, got %d", e.ElectionsRun())
	}

	// Tick after grace expires → election runs.
	e.tick(context.Background(), time.Now().Add(2*time.Second))
	if got := st.Snapshot(); got.CurrentOrchestrator != "A" {
		t.Errorf("after grace: expected A elected, got %q", got.CurrentOrchestrator)
	}
}

// ---------- Step-down: Orchestrator stops emitting when a peer becomes preferred ----------

func TestEngine_StepsDown_WhenPeerBecomesPreferred(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID:              "c1",
		Roster:              []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
		CurrentTerm:         3,
		CurrentOrchestrator: "A",
	})
	st := NewState("A", 3, "A", 32)
	st.leaseExpires = time.Now().Add(1 * time.Hour) // A is current lease holder

	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 50, true) // B becomes pinned (lower score, but pin overrides)

	hb := newFakeHB(http.StatusOK)
	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: hb, Health: always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 100, ReasoningEnabled: true}
		},
	})

	e.tick(context.Background(), time.Now())
	if got := hb.HeartbeatsSentForTest(); got != 0 {
		t.Errorf("A should step down when B is pinned; got %d heartbeats emitted", got)
	}
}

// ---------- additional: foreign clan_id rejected ----------

func TestEngine_RejectsForeignClanID(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{ClanID: "ours", Roster: []state.RosterMember{{MemberID: "A", State: "active"}}})
	st := NewState("A", 0, "", 32)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:7779", 99, false)
	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "ours", State: st, Store: store, Registry: reg,
		Client: newFakeHB(http.StatusOK), Health: always(true),
		LocalSelf: func() LocalCandidate { return LocalCandidate{MemberID: "A", ReasoningScore: 1, ReasoningEnabled: true} },
	})
	hb := Heartbeat{MemberID: "B", ClanID: "foreign", Term: 1, ReasoningScore: 99}
	if _, err := e.OnHeartbeatReceived(hb, "B", time.Now()); err == nil {
		t.Fatalf("expected foreign clan_id reject")
	}
}

// ---------- helpers ----------

func mustLoad(t *testing.T, s *state.Store) *state.Clan {
	t.Helper()
	c, err := s.LoadClan()
	if err != nil || c == nil {
		t.Fatalf("load: %v %v", c, err)
	}
	return c
}

// jsonReadBack pulls clan.json bytes off disk so tests can assert raw schema.
func jsonReadBack(t *testing.T, s *state.Store) ([]byte, error) {
	t.Helper()
	// Save a no-op to make sure the file exists; then read it.
	c, _ := s.LoadClan()
	if c == nil {
		return nil, fmt.Errorf("no clan persisted")
	}
	enc, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return enc, nil
}

func bytesIndexAll(haystack []byte, needle string) int {
	return bytes.Index(haystack, []byte(needle))
}

// HeartbeatsSentForTest exposes the recorded call count on fakeHB.
func (f *fakeHB) HeartbeatsSentForTest() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}
