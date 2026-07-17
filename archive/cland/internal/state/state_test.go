package state

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLoadClan_MissingReturnsNilNoError(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.LoadClan()
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Errorf("missing clan.json should return nil, got %+v", c)
	}
}

func TestClan_IsActive(t *testing.T) {
	var zero *Clan
	if zero.IsActive() {
		t.Errorf("nil clan must not be active")
	}
	empty := &Clan{}
	if empty.IsActive() {
		t.Errorf("empty clan must not be active")
	}
	full := &Clan{ClanID: "x"}
	if !full.IsActive() {
		t.Errorf("non-empty clan_id should be active")
	}
}

func TestSaveLoadClan_RoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	original := &Clan{
		ClanID:      "f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
		ClanCertPEM: "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		ClanCertPin: "sha256:abcd1234",
		Role:        "founder",
		JoinedAt:    time.Now().UTC().Truncate(time.Second),
		Roster: []RosterMember{
			{MemberID: "m1", State: "active", AdmittedAt: time.Now().UTC().Truncate(time.Second)},
		},
	}
	original.SetClanKey(key)

	if err := s.SaveClan(original); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadClan()
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("loaded clan is nil after save")
	}
	if loaded.ClanID != original.ClanID || loaded.Role != original.Role {
		t.Errorf("scalar fields mismatched: %+v vs %+v", loaded, original)
	}
	if !bytes.Equal(loaded.ClanKey(), key) {
		t.Errorf("clan_key did not round-trip")
	}
	if len(loaded.Roster) != 1 || loaded.Roster[0].MemberID != "m1" {
		t.Errorf("roster lost: %+v", loaded.Roster)
	}
}

func TestSaveClan_AtomicNoStaleTmp(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveClan(&Clan{ClanID: "x"}); err != nil {
		t.Fatal(err)
	}
	// .tmp must not linger after a successful save.
	if _, err := os.Stat(filepath.Join(dir, ClanFile+".tmp")); !os.IsNotExist(err) {
		t.Errorf("stale .tmp left behind: err=%v", err)
	}
}

func TestRevocations_RoundTripAndDefault(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.LoadRevocations()
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || len(r.Entries) != 0 {
		t.Fatalf("missing file should return empty revocations, got %+v", r)
	}

	r.Entries = append(r.Entries, Revocation{
		MemberID:   "evil",
		PubKeyHash: "sha256:deadbeef",
		RevokedAt:  time.Now().UTC().Truncate(time.Second),
		RevokedBy:  "self",
		Reason:     "test",
	})
	if err := s.SaveRevocations(r); err != nil {
		t.Fatal(err)
	}
	again, err := s.LoadRevocations()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Entries) != 1 || again.Entries[0].MemberID != "evil" {
		t.Errorf("revocations not persisted: %+v", again)
	}
}

func TestSaveClan_ConcurrentWritesSerialise(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &Clan{ClanID: "c", Role: "founder"}
			if err := s.SaveClan(c); err != nil {
				t.Errorf("concurrent save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	loaded, err := s.LoadClan()
	if err != nil || loaded == nil {
		t.Fatalf("post-concurrent load: clan=%v err=%v", loaded, err)
	}
}

// TestElectionFields_RoundTrip covers the Phase E additions: CurrentTerm,
// CurrentOrchestrator, PinnedOrchestrator must round-trip via SaveClan /
// LoadClan. LeaseExpires is INTENTIONALLY NOT in the struct (R2 — volatile
// state, reconstructed from the next heartbeat after restart).
func TestElectionFields_RoundTrip(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	original := &Clan{
		ClanID:              "f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
		Role:                "founder",
		CurrentOrchestrator: "orch-uuid",
		CurrentTerm:         42,
		PinnedOrchestrator:  true,
	}
	if err := s.SaveClan(original); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadClan()
	if err != nil || loaded == nil {
		t.Fatalf("load: %v %v", loaded, err)
	}
	if loaded.CurrentTerm != 42 {
		t.Errorf("CurrentTerm: got %d want 42", loaded.CurrentTerm)
	}
	if loaded.CurrentOrchestrator != "orch-uuid" {
		t.Errorf("CurrentOrchestrator: got %q want %q", loaded.CurrentOrchestrator, "orch-uuid")
	}
	if !loaded.PinnedOrchestrator {
		t.Errorf("PinnedOrchestrator: got false want true")
	}
}

// TestElectionFields_DefaultZero verifies the omitempty tags keep older
// state files (Phases A-D) loadable: a Clan with no election fields set
// must load with all three at the zero value.
func TestElectionFields_DefaultZero(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.SaveClan(&Clan{ClanID: "x"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadClan()
	if err != nil || loaded == nil {
		t.Fatalf("load: %v %v", loaded, err)
	}
	if loaded.CurrentTerm != 0 || loaded.CurrentOrchestrator != "" || loaded.PinnedOrchestrator {
		t.Errorf("expected zero-value election fields, got term=%d orch=%q pinned=%v",
			loaded.CurrentTerm, loaded.CurrentOrchestrator, loaded.PinnedOrchestrator)
	}
}

func TestSaveClan_RejectsNil(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.SaveClan(nil); err == nil {
		t.Errorf("SaveClan(nil) must error")
	}
	if err := s.SaveRevocations(nil); err == nil {
		t.Errorf("SaveRevocations(nil) must error")
	}
}
