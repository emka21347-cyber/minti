package membership

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/state"
)

func newDiscardAudit() auditlog.Logger {
	p, _ := auditlog.NewFileLogger("nul-test-audit") // unused
	return p
}

// noopAudit drops events on the floor — used by tests that don't care
// about audit contents.
type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newService(t *testing.T) (*Service, *state.Store, *identity.Identity) {
	t.Helper()
	store, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, id, "127.0.0.1:7777", noopAudit{}, discardLog())
	return svc, store, id
}

// foundCreate is a small helper: founder + Create => clan is active.
func foundCreate(t *testing.T, svc *Service, store *state.Store, id *identity.Identity) *PasteKey {
	t.Helper()
	pk, err := Create(store, id, "127.0.0.1:7777")
	if err != nil {
		t.Fatal(err)
	}
	// Re-init service so it picks up the new clan state via store reads.
	_ = svc // svc reads store on every op, so no re-init needed
	return pk
}

// ---------- InviteStore ----------

func TestInviteStore_IssueRedeem(t *testing.T) {
	s := NewInviteStore()
	tok, err := s.Issue("clan-1", "issuer-x", 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token == "" || tok.ClanID != "clan-1" || tok.IssuedBy != "issuer-x" {
		t.Errorf("token fields wrong: %+v", tok)
	}
	got, err := s.Redeem(tok.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != tok.Token {
		t.Errorf("redeem returned wrong token")
	}
	// Second redemption must fail (single-use).
	if _, err := s.Redeem(tok.Token); !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("second redeem error = %v, want ErrInviteUnknown", err)
	}
}

func TestInviteStore_Expiry(t *testing.T) {
	s := NewInviteStore()
	tok, err := s.Issue("c", "i", InviteTTLMin)
	if err != nil {
		t.Fatal(err)
	}
	// Force-expire by mutating the in-store entry.
	s.mu.Lock()
	s.tokens[tok.Token].ExpiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if _, err := s.Redeem(tok.Token); !errors.Is(err, ErrInviteExpired) {
		t.Errorf("expired token error = %v, want ErrInviteExpired", err)
	}
	// Even expired-then-redeemed tokens must not be redeemable again.
	if _, err := s.Redeem(tok.Token); !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("post-expired redeem error = %v, want ErrInviteUnknown", err)
	}
}

func TestInviteStore_TTLBounds(t *testing.T) {
	s := NewInviteStore()
	if _, err := s.Issue("c", "i", 0); !errors.Is(err, ErrInviteTTL) {
		t.Errorf("0 ttl error = %v", err)
	}
	if _, err := s.Issue("c", "i", 48*time.Hour); !errors.Is(err, ErrInviteTTL) {
		t.Errorf("48h ttl error = %v", err)
	}
}

func TestInviteStore_Sweep(t *testing.T) {
	s := NewInviteStore()
	a, _ := s.Issue("c", "i", InviteTTLMin)
	b, _ := s.Issue("c", "i", InviteTTLMin)
	// Expire `a` only.
	s.mu.Lock()
	s.tokens[a.Token].ExpiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if got := s.Sweep(); got != 1 {
		t.Errorf("sweep purged %d, want 1", got)
	}
	if s.Size() != 1 {
		t.Errorf("post-sweep size %d, want 1", s.Size())
	}
	// `b` should still redeem.
	if _, err := s.Redeem(b.Token); err != nil {
		t.Errorf("unrelated token should still redeem: %v", err)
	}
}

// ---------- Service operations ----------

func TestService_IssueInvite_RequiresActiveClan(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.IssueInvite("issuer", time.Hour); err == nil {
		t.Errorf("invite on unaffiliated should error")
	}
}

func TestService_IssueAndRedeemInvite_AddsJoinerAsAdmitted(t *testing.T) {
	svc, store, id := newService(t)
	pk := foundCreate(t, svc, store, id)

	tok, err := svc.IssueInvite(id.MemberID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ClanCertPin == "" {
		t.Errorf("invite response missing pin")
	}
	if tok.ClanID != pk.ClanID {
		t.Errorf("invite clan_id mismatch")
	}
	if tok.LANAddress != "127.0.0.1:7777" {
		t.Errorf("invite address echo wrong: %q", tok.LANAddress)
	}

	resp, err := svc.RedeemInvite(JoinRequest{
		Token:        tok.Token,
		MemberID:     "joiner-1",
		MemberPubKey: "joiner-pub-b64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ClanID != pk.ClanID {
		t.Errorf("redeem clan_id mismatch")
	}
	if resp.ClanKeyB64 == "" || resp.ClanCertPEM == "" {
		t.Errorf("redeem must return clan_key + cert")
	}
	if len(resp.Roster) != 2 {
		t.Fatalf("roster size = %d, want 2 (founder + joiner)", len(resp.Roster))
	}
	// Verify joiner is in roster as admitted.
	clan, _ := store.LoadClan()
	var found bool
	for _, m := range clan.Roster {
		if m.MemberID == "joiner-1" {
			found = true
			if m.State != "admitted" {
				t.Errorf("joiner state = %q, want admitted", m.State)
			}
		}
	}
	if !found {
		t.Errorf("joiner not in persisted roster")
	}

	// Token must not be reusable.
	if _, err := svc.RedeemInvite(JoinRequest{Token: tok.Token, MemberID: "joiner-2", MemberPubKey: "x"}); err == nil {
		t.Errorf("token must be single-use")
	}
}

func TestService_RedeemInvite_RejectsBadInputs(t *testing.T) {
	svc, store, id := newService(t)
	foundCreate(t, svc, store, id)

	cases := []JoinRequest{
		{Token: "", MemberID: "j", MemberPubKey: "p"},
		{Token: "garbage", MemberID: "j", MemberPubKey: "p"},
		{Token: "x", MemberID: "", MemberPubKey: "p"},
		{Token: "x", MemberID: "j", MemberPubKey: ""},
	}
	for i, c := range cases {
		if _, err := svc.RedeemInvite(c); err == nil {
			t.Errorf("case %d should error: %+v", i, c)
		}
	}
}

func TestService_Welcome(t *testing.T) {
	svc, store, id := newService(t)
	pk := foundCreate(t, svc, store, id)

	resp, err := svc.Welcome(WelcomeRequest{
		MemberID:     "paste-joiner",
		MemberPubKey: "joiner-pub-b64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ClanID != pk.ClanID || resp.ClanCertPEM == "" {
		t.Errorf("welcome response wrong: %+v", resp)
	}
	if len(resp.Roster) != 2 {
		t.Fatalf("roster size after welcome = %d, want 2", len(resp.Roster))
	}
	// Idempotency: re-welcoming the same member doesn't double-list.
	resp2, _ := svc.Welcome(WelcomeRequest{MemberID: "paste-joiner", MemberPubKey: "x"})
	if len(resp2.Roster) != 2 {
		t.Errorf("repeated welcome inflated roster to %d", len(resp2.Roster))
	}
	// Bad input.
	if _, err := svc.Welcome(WelcomeRequest{}); err == nil {
		t.Errorf("empty welcome should error")
	}
}

func TestService_Members_RequiresActive(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Members(); err == nil {
		t.Errorf("members on unaffiliated should error")
	}
}

func TestService_Leave(t *testing.T) {
	svc, store, id := newService(t)
	foundCreate(t, svc, store, id)

	if err := svc.Leave(); err != nil {
		t.Fatal(err)
	}
	clan, _ := store.LoadClan()
	if clan.IsActive() {
		t.Errorf("after Leave the clan must be inactive")
	}
	if err := svc.Leave(); err == nil {
		t.Errorf("double Leave should error")
	}
}

func TestService_Revoke(t *testing.T) {
	svc, store, id := newService(t)
	foundCreate(t, svc, store, id)

	// Add a second member via Welcome so we have someone to revoke.
	if _, err := svc.Welcome(WelcomeRequest{MemberID: "evil", MemberPubKey: "bad-pub-b64"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke("evil", "test", id.MemberID); err != nil {
		t.Fatal(err)
	}
	clan, _ := store.LoadClan()
	for _, m := range clan.Roster {
		if m.MemberID == "evil" && m.State != "revoked" {
			t.Errorf("evil state = %q, want revoked", m.State)
		}
	}
	revs, _ := store.LoadRevocations()
	if len(revs.Entries) != 1 || revs.Entries[0].MemberID != "evil" {
		t.Errorf("revocation not persisted: %+v", revs.Entries)
	}
	// Revoke missing member.
	if err := svc.Revoke("nobody", "x", id.MemberID); err == nil {
		t.Errorf("revoking unknown member should error")
	}
}

func TestService_PromoteToActive(t *testing.T) {
	svc, store, id := newService(t)
	foundCreate(t, svc, store, id)
	// Add a second member via Welcome (lands as "admitted" per spec §3.1).
	if _, err := svc.Welcome(WelcomeRequest{MemberID: "joiner", MemberPubKey: "pub-b64"}); err != nil {
		t.Fatal(err)
	}
	// Initial state should be admitted.
	clan, _ := store.LoadClan()
	for _, m := range clan.Roster {
		if m.MemberID == "joiner" && m.State != "admitted" {
			t.Fatalf("freshly-welcomed member: state=%q want admitted", m.State)
		}
	}

	// Promote.
	if err := svc.PromoteToActive("joiner"); err != nil {
		t.Fatal(err)
	}
	clan, _ = store.LoadClan()
	var promoted bool
	for _, m := range clan.Roster {
		if m.MemberID == "joiner" {
			promoted = m.State == "active"
		}
	}
	if !promoted {
		t.Errorf("PromoteToActive didn't flip state to active")
	}

	// Idempotent: re-promoting is a no-op (no error).
	if err := svc.PromoteToActive("joiner"); err != nil {
		t.Errorf("idempotent promote should not error: %v", err)
	}

	// Unknown member: no-op (don't error — race with revoke / leave).
	if err := svc.PromoteToActive("nobody"); err != nil {
		t.Errorf("promoting unknown should be no-op, got %v", err)
	}

	// Empty ID: no-op.
	if err := svc.PromoteToActive(""); err != nil {
		t.Errorf("empty member_id should be no-op")
	}
}

func TestService_SweepZombies(t *testing.T) {
	svc, store, id := newService(t)
	foundCreate(t, svc, store, id)

	// Add a zombie (admitted, very old).
	clan, _ := store.LoadClan()
	clan.Roster = append(clan.Roster, state.RosterMember{
		MemberID:   "zombie",
		PubKeyB64:  "z",
		State:      "admitted",
		AdmittedAt: time.Now().Add(-(ZombieMaxAge + time.Hour)).UTC(),
	})
	// Add a fresh admitted (should NOT be purged).
	clan.Roster = append(clan.Roster, state.RosterMember{
		MemberID:   "fresh",
		PubKeyB64:  "f",
		State:      "admitted",
		AdmittedAt: time.Now().UTC(),
	})
	if err := store.SaveClan(clan); err != nil {
		t.Fatal(err)
	}

	n, err := svc.SweepZombies()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purged %d, want 1", n)
	}
	after, _ := store.LoadClan()
	found := map[string]bool{}
	for _, m := range after.Roster {
		found[m.MemberID] = true
	}
	if found["zombie"] {
		t.Errorf("zombie should be purged")
	}
	if !found["fresh"] {
		t.Errorf("fresh admitted should remain")
	}

	// Idempotent.
	n2, _ := svc.SweepZombies()
	if n2 != 0 {
		t.Errorf("second sweep purged %d, want 0", n2)
	}
}

func TestService_StartZombieSweep_Cancels(t *testing.T) {
	svc, _, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.StartZombieSweep(ctx, 10*time.Millisecond)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Errorf("StartZombieSweep did not stop after context cancel")
	}
}

func TestUpsertRoster_ReplaceVsAppend(t *testing.T) {
	roster := []state.RosterMember{{MemberID: "a", State: "active"}}
	updated := upsertRoster(roster, state.RosterMember{MemberID: "b", State: "admitted"})
	if len(updated) != 2 {
		t.Errorf("append: want 2, got %d", len(updated))
	}
	updated = upsertRoster(updated, state.RosterMember{MemberID: "a", State: "revoked"})
	if len(updated) != 2 {
		t.Errorf("replace: want 2, got %d", len(updated))
	}
	for _, m := range updated {
		if m.MemberID == "a" && m.State != "revoked" {
			t.Errorf("upsert did not replace state; got %q", m.State)
		}
	}
}
