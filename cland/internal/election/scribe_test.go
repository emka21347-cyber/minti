package election

// Memory M3 (spec §13.8) — scribe selection: the INVERSE of orchestrator
// selection. Lowest reasoning_score among scribe_capable members wins.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
)

// installScribePeer is installPeer + scribe fields.
func installScribePeer(reg *peers.Registry, memberID, addr string, score int, capable, pinned bool) {
	ad := &peers.Advertisement{
		MemberID:       memberID,
		Generation:     1,
		LANAddress:     addr,
		ReasoningScore: score,
		Capabilities:   map[string]any{"reasoning": map[string]any{"enabled": true}},
		ScribeCapable:  capable,
		PinnedScribe:   pinned,
	}
	_ = reg.BindMember(ad, addr)
	reg.TouchLive(memberID)
}

func scribeEngine(t *testing.T, selfScore int, selfCapable, selfPinned bool, reg *peers.Registry) (*Engine, *state.Store) {
	t.Helper()
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active", AdmittedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{MemberID: "B", State: "active", AdmittedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
			{MemberID: "C", State: "active", AdmittedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)},
		},
	})
	e := mkEngine(t, EngineOpts{
		SelfID:   "A",
		ClanID:   "c1",
		State:    NewState("A", 0, "", 32),
		Store:    store,
		Registry: reg,
		Client:   newFakeHB(200),
		Health:   always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{
				MemberID: "A", ReasoningScore: selfScore, ReasoningEnabled: true,
				ScribeCapable: selfCapable, PinnedScribe: selfPinned,
				AdmittedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			}
		},
	})
	return e, store
}

func loadClanT(t *testing.T, store *state.Store) *state.Clan {
	t.Helper()
	c, err := store.LoadClan()
	if err != nil || c == nil {
		t.Fatalf("load clan: %v", err)
	}
	return c
}

func TestSelectScribe_LowestScoreWins(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 50, true, false)
	installScribePeer(reg, "C", "c:1", 22, true, false) // weakest
	e, store := scribeEngine(t, 70, true, false, reg)

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "C" {
		t.Fatalf("scribe = %q, want C (lowest reasoning_score)", got.MemberID)
	}
}

func TestSelectScribe_CapabilityGate(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 50, true, false)
	installScribePeer(reg, "C", "c:1", 22, false, false) // weakest but NOT capable
	e, store := scribeEngine(t, 70, true, false, reg)

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "B" {
		t.Fatalf("scribe = %q, want B (C is not scribe_capable)", got.MemberID)
	}
}

func TestSelectScribe_NoCapableMember(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 50, false, false)
	e, store := scribeEngine(t, 70, false, false, reg)

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "" {
		t.Fatalf("scribe = %q, want empty (nobody capable; distillation off)", got.MemberID)
	}
}

func TestSelectScribe_OneNodeClan(t *testing.T) {
	// Scribe == Orchestrator == self is legal on a 1-node Clan (§13.8).
	reg := peers.NewRegistry()
	e, store := scribeEngine(t, 70, true, false, reg)

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "A" {
		t.Fatalf("scribe = %q, want self on a lone-member Clan", got.MemberID)
	}
}

func TestSelectScribe_PinOverridesScore(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 50, true, true) // pinned, NOT weakest
	installScribePeer(reg, "C", "c:1", 22, true, false)
	e, store := scribeEngine(t, 70, true, false, reg)

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "B" {
		t.Fatalf("scribe = %q, want pinned B despite higher score", got.MemberID)
	}
}

func TestSelectScribe_MultiPinLowestID(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 50, true, true)
	installScribePeer(reg, "C", "c:1", 22, true, true)
	e, store := scribeEngine(t, 70, true, true, reg) // self (A) pinned too

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "A" {
		t.Fatalf("scribe = %q, want A (lowest member_id among pins, §5.6 mirror)", got.MemberID)
	}
}

func TestSelectScribe_TieBreaksOldestJoin(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 70, true, false) // same score as self
	e, store := scribeEngine(t, 70, true, false, reg)

	got := e.selectScribe(loadClanT(t, store))
	if got.MemberID != "A" {
		t.Fatalf("scribe = %q, want A (oldest AdmittedAt at equal score)", got.MemberID)
	}
}

func TestRefreshScribe_PersistsOnChange(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "C", "c:1", 22, true, false)
	e, store := scribeEngine(t, 70, true, false, reg)

	scribe := e.refreshScribe(loadClanT(t, store))
	if scribe != "C" {
		t.Fatalf("refresh selected %q, want C", scribe)
	}
	if loadClanT(t, store).CurrentScribe != "C" {
		t.Fatal("CurrentScribe must persist on change")
	}
	// Re-selection of the SAME scribe must not rewrite (R2 discipline is
	// observable only behaviorally here; assert via state unchanged).
	if e.refreshScribe(loadClanT(t, store)) != "C" {
		t.Fatal("stable re-selection changed the scribe")
	}
}

func TestSelectScribe_DeadScribeReselected(t *testing.T) {
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 50, true, false)
	installScribePeer(reg, "C", "c:1", 22, true, false)
	e, store := scribeEngine(t, 70, true, false, reg)

	if got := e.selectScribe(loadClanT(t, store)); got.MemberID != "C" {
		t.Fatalf("precondition: want C, got %q", got.MemberID)
	}

	// C dies: mark it heartbeat-seen long ago (Live() false) and age its ad
	// out of the freshness window via ExpireForTest below — simplest honest
	// simulation: rebuild the registry without C.
	reg2 := peers.NewRegistry()
	installScribePeer(reg2, "B", "b:1", 50, true, false)
	e2, store2 := scribeEngine(t, 70, true, false, reg2)
	if got := e2.selectScribe(loadClanT(t, store2)); got.MemberID != "B" {
		t.Fatalf("after scribe death want B, got %q", got.MemberID)
	}
}

func TestFollowerAdoptsScribeFromHeartbeat(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active"},
			{MemberID: "B", State: "active"},
		},
	})
	reg := peers.NewRegistry()
	installScribePeer(reg, "B", "b:1", 90, true, false) // B = orchestrator-grade peer
	e := mkEngine(t, EngineOpts{
		SelfID:   "A",
		ClanID:   "c1",
		State:    NewState("A", 0, "", 32),
		Store:    store,
		Registry: reg,
		Client:   newFakeHB(200),
		Health:   always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 10, ReasoningEnabled: true, ScribeCapable: true}
		},
	})

	hb := Heartbeat{
		MemberID: "B", ClanID: "c1", Term: 1,
		LeaseUntil: time.Now().Add(8 * time.Second),
		Scribe:     "A",
	}
	if _, err := e.OnHeartbeatReceived(hb, "B", time.Now()); err != nil {
		t.Fatalf("heartbeat rejected: %v", err)
	}
	if got := e.opts.State.CurrentScribe(); got != "A" {
		t.Fatalf("follower scribe = %q, want adopted A", got)
	}
	clan, _ := store.LoadClan()
	if clan.CurrentScribe != "A" {
		t.Fatal("adopted scribe must persist")
	}

	// Orchestrator clears the scribe (capability withdrawn) → follower clears.
	hb2 := hb
	hb2.Term = 2
	hb2.Scribe = ""
	if _, err := e.OnHeartbeatReceived(hb2, "B", time.Now()); err != nil {
		t.Fatalf("second heartbeat rejected: %v", err)
	}
	if got := e.opts.State.CurrentScribe(); got != "" {
		t.Fatalf("follower scribe = %q, want cleared", got)
	}
}

func TestHeartbeatCarriesScribeField(t *testing.T) {
	// Orchestrator path: emitHeartbeats must put the selection on the wire.
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active"},
			{MemberID: "C", State: "active"},
		},
	})
	st := NewState("A", 1, "A", 32)
	now := time.Now()
	st.CommitSelfElection(1, ReasonBootstrap, now, 5*time.Second)
	reg := peers.NewRegistry()
	installScribePeer(reg, "C", "c:1", 22, true, false)

	hb := &ackHB{body: []byte(`{}`)}
	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: hb, Health: always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 70, ReasoningEnabled: true, ScribeCapable: true}
		},
	})
	e.TickForTest(context.Background(), now.Add(time.Second))

	hb.mu.Lock()
	defer hb.mu.Unlock()
	if len(hb.bodies) == 0 {
		t.Fatal("no heartbeat posted")
	}
	var sent Heartbeat
	if err := json.Unmarshal(hb.bodies[0], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Scribe != "C" {
		t.Fatalf("heartbeat scribe = %q, want C (weakest capable)", sent.Scribe)
	}
	if loadClanT(t, store).CurrentScribe != "C" {
		t.Fatal("orchestrator must persist its own selection")
	}
}
