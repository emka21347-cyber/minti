package peers

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/minti/cland/internal/state"
)

// ---------- UpsertCandidate ----------

func TestUpsertCandidate_Idempotent(t *testing.T) {
	r := NewRegistry()
	if err := r.UpsertCandidate("10.0.0.1:7777", SourceMDNS); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertCandidate("10.0.0.1:7777", SourceManual); err != nil {
		t.Fatal(err)
	}
	cs, _ := r.Snapshot()
	if len(cs) != 1 {
		t.Fatalf("expected 1 candidate after dup upsert, got %d", len(cs))
	}
	// Original DiscoveredVia preserved.
	if cs[0].DiscoveredVia != SourceMDNS {
		t.Errorf("DiscoveredVia changed on dup: got %q", cs[0].DiscoveredVia)
	}
}

func TestUpsertCandidate_EmptyRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.UpsertCandidate("", SourceMDNS); err == nil {
		t.Errorf("empty address should error")
	}
}

func TestUpsertCandidate_CapEnforced(t *testing.T) {
	r := NewRegistry()
	r.SetMaxEntries(3)
	for i := 0; i < 3; i++ {
		if err := r.UpsertCandidate(fmt.Sprintf("10.0.0.%d:7777", i), SourceMDNS); err != nil {
			t.Fatal(err)
		}
	}
	err := r.UpsertCandidate("10.0.0.99:7777", SourceMDNS)
	if !errors.Is(err, ErrRegistryFull) {
		t.Errorf("4th upsert err = %v, want ErrRegistryFull", err)
	}
}

// ---------- AddPeer (rate-limit + pre-dial) ----------

func TestAddPeer_PreDialSuccess(t *testing.T) {
	r := NewRegistry()
	r.SetDialFunc(func(string, string, time.Duration) error { return nil })
	if err := r.AddPeer("origin-1", "192.168.1.42:7777"); err != nil {
		t.Fatal(err)
	}
	cs, _ := r.Snapshot()
	if len(cs) != 1 || cs[0].DiscoveredVia != SourceManual {
		t.Errorf("expected 1 manual candidate, got %+v", cs)
	}
}

func TestAddPeer_PreDialFails(t *testing.T) {
	r := NewRegistry()
	r.SetDialFunc(func(string, string, time.Duration) error { return errors.New("connection refused") })
	err := r.AddPeer("origin-1", "192.168.1.42:7777")
	if err == nil {
		t.Fatalf("pre-dial fail should error")
	}
	// Must NOT have persisted.
	cs, _ := r.Snapshot()
	if len(cs) != 0 {
		t.Errorf("failed pre-dial should not persist, got %d candidates", len(cs))
	}
}

func TestAddPeer_RateLimit(t *testing.T) {
	r := NewRegistry()
	r.SetMaxEntries(1000)
	r.SetDialFunc(func(string, string, time.Duration) error { return nil })
	for i := 0; i < PeerAddRateLimit; i++ {
		if err := r.AddPeer("origin-x", fmt.Sprintf("10.0.0.%d:7777", i)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	err := r.AddPeer("origin-x", "10.0.0.99:7777")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("11th call err = %v, want ErrRateLimited", err)
	}
	// Different origin should NOT be limited.
	if err := r.AddPeer("origin-y", "10.0.1.0:7777"); err != nil {
		t.Errorf("different origin should not be limited: %v", err)
	}
}

func TestAddPeer_RejectsEmptyInputs(t *testing.T) {
	r := NewRegistry()
	r.SetDialFunc(func(string, string, time.Duration) error { return nil })
	if err := r.AddPeer("", "10.0.0.1:1"); err == nil {
		t.Errorf("empty origin should error")
	}
	if err := r.AddPeer("o", ""); err == nil {
		t.Errorf("empty address should error")
	}
}

// ---------- BindMember ----------

func ad(memberID string, gen uint64) *Advertisement {
	return &Advertisement{MemberID: memberID, ClanID: "c", Generation: gen, ReasoningScore: 50, SystemScore: 60}
}

func TestBindMember_NewEntry(t *testing.T) {
	r := NewRegistry()
	if err := r.BindMember(ad("m1", 1), "10.0.0.1:7777"); err != nil {
		t.Fatal(err)
	}
	_, ms := r.Snapshot()
	if len(ms) != 1 || ms[0].MemberID != "m1" {
		t.Errorf("expected 1 member m1, got %+v", ms)
	}
	if !ms[0].AdFresh(time.Now()) {
		t.Errorf("just-bound member should be AdFresh")
	}
}

func TestBindMember_RevokedRejected(t *testing.T) {
	r := NewRegistry()
	r.SetRevocations(&state.Revocations{Entries: []state.Revocation{{MemberID: "evil"}}})
	err := r.BindMember(ad("evil", 1), "10.0.0.1:7777")
	if !errors.Is(err, ErrRevoked) {
		t.Errorf("revoked bind err = %v, want ErrRevoked", err)
	}
	_, ms := r.Snapshot()
	if len(ms) != 0 {
		t.Errorf("revoked member must not be in registry")
	}
}

func TestBindMember_UpdateExistingNewerGen(t *testing.T) {
	r := NewRegistry()
	_ = r.BindMember(ad("m1", 1), "10.0.0.1:7777")
	if err := r.BindMember(ad("m1", 2), "10.0.0.1:7777"); err != nil {
		t.Fatal(err)
	}
	_, ms := r.Snapshot()
	if ms[0].AdGeneration != 2 {
		t.Errorf("expected gen=2 after update, got %d", ms[0].AdGeneration)
	}
}

func TestBindMember_DedupOldGeneration(t *testing.T) {
	r := NewRegistry()
	_ = r.BindMember(ad("m1", 5), "10.0.0.1:7777")
	// Older generation arriving: bumps LastSeenAt but doesn't replace LatestAd.
	if err := r.BindMember(ad("m1", 3), "10.0.0.1:7777"); err != nil {
		t.Fatal(err)
	}
	_, ms := r.Snapshot()
	if ms[0].AdGeneration != 5 {
		t.Errorf("older generation should not replace, got %d", ms[0].AdGeneration)
	}
}

func TestBindMember_PromotesCandidateDiscoveredVia(t *testing.T) {
	r := NewRegistry()
	_ = r.UpsertCandidate("10.0.0.1:7777", SourceManual)
	_ = r.BindMember(ad("m1", 1), "10.0.0.1:7777")
	_, ms := r.Snapshot()
	if ms[0].DiscoveredVia != SourceManual {
		t.Errorf("DiscoveredVia should carry from candidate, got %q", ms[0].DiscoveredVia)
	}
}

func TestBindMember_AddressRebind(t *testing.T) {
	r := NewRegistry()
	_ = r.BindMember(ad("m1", 1), "10.0.0.1:7777")
	_ = r.BindMember(ad("m1", 2), "10.0.0.7:7777")
	_, ms := r.Snapshot()
	if ms[0].Address != "10.0.0.7:7777" {
		t.Errorf("address should update on newer ad from new IP, got %q", ms[0].Address)
	}
}

func TestBindMember_NilOrEmptyRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.BindMember(nil, "x"); err == nil {
		t.Errorf("nil ad should error")
	}
	if err := r.BindMember(&Advertisement{}, "x"); err == nil {
		t.Errorf("empty member_id should error")
	}
}

// ---------- Freshness predicates ----------

func TestAdFresh_Window(t *testing.T) {
	now := time.Now()
	m := &Member{LastAd: now.Add(-89 * time.Second)}
	if !m.AdFresh(now) {
		t.Errorf("89s old should be AdFresh (window=90s)")
	}
	m.LastAd = now.Add(-91 * time.Second)
	if m.AdFresh(now) {
		t.Errorf("91s old should NOT be AdFresh")
	}
	m.LastAd = time.Time{}
	if m.AdFresh(now) {
		t.Errorf("zero LastAd should NOT be AdFresh")
	}
}

func TestLive_Window(t *testing.T) {
	now := time.Now()
	m := &Member{LastSeenAt: now.Add(-3 * time.Second)}
	if !m.Live(now) {
		t.Errorf("3s old should be Live (window=4s)")
	}
	m.LastSeenAt = now.Add(-5 * time.Second)
	if m.Live(now) {
		t.Errorf("5s old should NOT be Live")
	}
}

func TestTouchLive_UpdatesLastSeen(t *testing.T) {
	r := NewRegistry()
	_ = r.BindMember(ad("m1", 1), "10.0.0.1:7777")
	// Backdate LastSeenAt 10s.
	r.mu.Lock()
	r.members["m1"].LastSeenAt = time.Now().Add(-10 * time.Second)
	r.mu.Unlock()

	r.TouchLive("m1")
	_, ms := r.Snapshot()
	if time.Since(ms[0].LastSeenAt) > time.Second {
		t.Errorf("TouchLive did not update LastSeenAt: %v ago", time.Since(ms[0].LastSeenAt))
	}
}

// ---------- Concurrency ----------

func TestRegistry_ConcurrentUpsertsAreSafe(t *testing.T) {
	r := NewRegistry()
	r.SetMaxEntries(10_000)
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.UpsertCandidate(fmt.Sprintf("10.0.0.%d:%d", i%256, i), SourceMDNS)
			_ = r.BindMember(ad(fmt.Sprintf("m%d", i), uint64(i)), fmt.Sprintf("10.0.0.%d:%d", i%256, i))
		}(i)
	}
	wg.Wait()
	cs, ms := r.Snapshot()
	// Some address collisions on N//256 wrap-around are expected; just ensure
	// no crash + the registry is internally consistent (no nils).
	for _, c := range cs {
		if c.Address == "" {
			t.Errorf("candidate with empty address")
		}
	}
	for _, m := range ms {
		if m.MemberID == "" {
			t.Errorf("member with empty id")
		}
	}
}

// ---------- Snapshot returns copies ----------

func TestSnapshot_ReturnsDeepCopy(t *testing.T) {
	r := NewRegistry()
	_ = r.BindMember(ad("m1", 1), "10.0.0.1:7777")

	_, ms := r.Snapshot()
	ms[0].Address = "MUTATED"
	ms[0].LatestAd.ClanID = "MUTATED"

	_, ms2 := r.Snapshot()
	if ms2[0].Address == "MUTATED" {
		t.Errorf("Snapshot leaked mutable reference (Address)")
	}
	if ms2[0].LatestAd.ClanID == "MUTATED" {
		t.Errorf("Snapshot leaked mutable reference (LatestAd)")
	}
}
