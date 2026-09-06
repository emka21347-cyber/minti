package election

// Memory M2 response-leg tests (spec §13.5): the Orchestrator's heartbeat
// carries its memory digest OUT (request leg), and each peer's ack carries
// the peer's digest BACK so follower edits can flow upstream.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
)

// ackHB returns a programmable JSON body on every POST (fakeHB returns an
// empty body, which the ack decoder treats as no-ack).
type ackHB struct {
	mu     sync.Mutex
	body   []byte
	bodies [][]byte
}

func (f *ackHB) Post(url, contentType string, body []byte) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(f.body))}, nil
}

func TestEmitHeartbeats_MemoryDigestBothLegs(t *testing.T) {
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{
			{MemberID: "A", State: "active"},
			{MemberID: "B", State: "active"},
		},
	})
	st := NewState("A", 1, "A", 32)
	now := time.Now()
	st.CommitSelfElection(1, ReasonBootstrap, now, 5*time.Second)

	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:9", 10, false)

	ackBody, _ := json.Marshal(HeartbeatAck{Accepted: true, MemoryDigest: "their-digest"})
	hb := &ackHB{body: ackBody}

	got := make(chan [2]string, 4)
	e := mkEngine(t, EngineOpts{
		SelfID:   "A",
		ClanID:   "c1",
		State:    st,
		Store:    store,
		Registry: reg,
		Client:   hb,
		Health:   always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 50, ReasoningEnabled: true}
		},
		MemoryDigest: func() string { return "our-digest" },
		OnHeartbeatAck: func(peerID string, ack HeartbeatAck) {
			got <- [2]string{peerID, ack.MemoryDigest}
		},
	})

	e.TickForTest(context.Background(), now.Add(time.Second))

	// Response leg: the ack callback fires (own goroutine) with B's digest.
	select {
	case pair := <-got:
		if pair[0] != "B" || pair[1] != "their-digest" {
			t.Fatalf("ack callback got %v, want [B their-digest]", pair)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnHeartbeatAck never fired")
	}

	// Request leg: the emitted heartbeat body carries OUR digest.
	hb.mu.Lock()
	defer hb.mu.Unlock()
	if len(hb.bodies) == 0 {
		t.Fatal("no heartbeat was posted")
	}
	if !strings.Contains(string(hb.bodies[0]), `"memory_digest":"our-digest"`) {
		t.Fatalf("heartbeat body missing memory_digest passenger: %s", hb.bodies[0])
	}
}

func TestEmitHeartbeats_NoAckCallbackNoDecode(t *testing.T) {
	// With OnHeartbeatAck nil the engine must keep the old behavior (close
	// the body, never decode) — guarded here by simply not panicking on a
	// garbage body.
	store := mkStore(t)
	saveClan(t, store, &state.Clan{
		ClanID: "c1",
		Roster: []state.RosterMember{{MemberID: "A", State: "active"}, {MemberID: "B", State: "active"}},
	})
	st := NewState("A", 1, "A", 32)
	now := time.Now()
	st.CommitSelfElection(1, ReasonBootstrap, now, 5*time.Second)
	reg := peers.NewRegistry()
	installPeer(reg, "B", "127.0.0.1:9", 10, false)

	hb := &ackHB{body: []byte("not json at all")}
	e := mkEngine(t, EngineOpts{
		SelfID: "A", ClanID: "c1", State: st, Store: store, Registry: reg,
		Client: hb, Health: always(true),
		LocalSelf: func() LocalCandidate {
			return LocalCandidate{MemberID: "A", ReasoningScore: 50, ReasoningEnabled: true}
		},
	})
	e.TickForTest(context.Background(), now.Add(time.Second)) // must not panic
}
