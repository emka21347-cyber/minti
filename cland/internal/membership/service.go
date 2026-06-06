package membership

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/state"
)

// Wire-shape requests/responses shared by the HTTP handlers and the CLI client.

// WelcomeRequest — sent by a paste-key joiner who already has clan_key.
type WelcomeRequest struct {
	MemberID     string `json:"member_id"`
	MemberPubKey string `json:"member_pubkey_b64"`
}

// WelcomeResponse — server tells the joiner about the Clan + roster.
// clan_key is NOT included — the joiner already derived it from the mnemonic.
// ClanCertPrivKeyB64 IS included so the joiner can serve TLS with the same
// cert. Per spec §10a residual R1 — sharing the priv key fits v1's unitary
// trust model (any active member has clan_key, which is the same trust level).
type WelcomeResponse struct {
	ClanID             string               `json:"clan_id"`
	ClanCertPEM        string               `json:"clan_cert_pem"`
	ClanCertPrivKeyB64 string               `json:"clan_cert_priv_key_b64"`
	Roster             []state.RosterMember `json:"roster"`
}

// JoinRequest — sent by an invite-token joiner who does NOT yet have clan_key.
type JoinRequest struct {
	Token        string `json:"token"`
	MemberID     string `json:"member_id"`
	MemberPubKey string `json:"member_pubkey_b64"`
}

// JoinResponse — invite-token path also delivers the clan_key + cert priv key
// (see WelcomeResponse for the priv-key rationale).
type JoinResponse struct {
	ClanID             string               `json:"clan_id"`
	ClanKeyB64         string               `json:"clan_key_b64"`
	ClanCertPEM        string               `json:"clan_cert_pem"`
	ClanCertPrivKeyB64 string               `json:"clan_cert_priv_key_b64"`
	Roster             []state.RosterMember `json:"roster"`
}

// RevokeRequest — body of POST /clan/revoke.
type RevokeRequest struct {
	MemberID string `json:"member_id"`
	Reason   string `json:"reason,omitempty"`
}

// InviteRequest — body of POST /clan/invite.
type InviteRequest struct {
	TTLSeconds int `json:"ttl_seconds"`
}

// InviteResponse — returned by POST /clan/invite. Adds LAN address + pin so
// the joiner can connect (the InviteStore itself doesn't know these).
type InviteResponse struct {
	Token       string `json:"token"`
	ClanID      string `json:"clan_id"`
	ClanCertPin string `json:"clan_cert_pin"`
	LANAddress  string `json:"lan_address"`
	ExpiresAt   string `json:"expires_at"`
}

// Service is the high-level membership operations API. Handlers + CLI alike
// call into this. The Service owns the in-memory invite store and exposes
// the operations the spec §3 + §10 endpoint table call for.
type Service struct {
	store      *state.Store
	id         *identity.Identity
	listenAddr string
	invites    *InviteStore
	knocks     *KnockStore
	log        *slog.Logger
	audit      auditlog.Logger

	mu sync.Mutex // serialises Clan-state mutations
}

// ZombieMaxAge is the §3.1 24h timeout for stuck `admitted` members.
const ZombieMaxAge = 24 * time.Hour

// NewService wires the operations API. listenAddr is what we'll advertise
// as the LAN reach-address in invite responses — auto-derive at the CLI
// level if not known.
func NewService(store *state.Store, id *identity.Identity, listenAddr string, audit auditlog.Logger, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:      store,
		id:         id,
		listenAddr: listenAddr,
		invites:    NewInviteStore(),
		knocks:     NewKnockStore(),
		log:        log,
		audit:      audit,
	}
}

// IssueInvite — current member mints a single-use token for a candidate.
func (s *Service) IssueInvite(issuerMemberID string, ttl time.Duration) (*InviteResponse, error) {
	clan, err := s.store.LoadClan()
	if err != nil {
		return nil, err
	}
	if !clan.IsActive() {
		return nil, errors.New("not in a Clan; nothing to invite into")
	}
	tok, err := s.invites.Issue(clan.ClanID, issuerMemberID, ttl)
	if err != nil {
		return nil, err
	}
	_ = s.audit.Write(auditlog.Event{
		Server:   "minti-cland",
		Tool:     "membership.invite",
		Decision: "allow",
		Args:     map[string]any{"issuer": issuerMemberID, "ttl_s": int(ttl.Seconds())},
	})
	return &InviteResponse{
		Token:       tok.Token,
		ClanID:      tok.ClanID,
		ClanCertPin: clan.ClanCertPin,
		LANAddress:  s.listenAddr,
		ExpiresAt:   tok.ExpiresAt.Format(time.RFC3339),
	}, nil
}

// RedeemInvite — token-based join. Validates token, adds joiner to roster
// as `admitted`, returns clan_key + cert. Server-side endpoint is anonymous
// (joiner has no clan_key yet); the token IS the auth.
func (s *Service) RedeemInvite(req JoinRequest) (*JoinResponse, error) {
	if req.Token == "" || req.MemberID == "" || req.MemberPubKey == "" {
		return nil, errors.New("token, member_id, member_pubkey_b64 all required")
	}
	tok, err := s.invites.Redeem(req.Token)
	if err != nil {
		_ = s.audit.Write(auditlog.Event{
			Server: "minti-cland", Tool: "membership.join_token", Decision: "deny",
			Args: map[string]any{"member_id": req.MemberID}, Reason: err.Error(),
		})
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return nil, err
	}
	if !clan.IsActive() || clan.ClanID != tok.ClanID {
		return nil, errors.New("issuer's Clan state changed since token was minted")
	}

	now := time.Now().UTC()
	updated := upsertRoster(clan.Roster, state.RosterMember{
		MemberID:   req.MemberID,
		PubKeyB64:  req.MemberPubKey,
		State:      "admitted",
		AdmittedAt: now,
		LastSeenAt: now,
	})
	clan.Roster = updated
	if err := s.store.SaveClan(clan); err != nil {
		return nil, err
	}
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.join_token", Decision: "allow",
		Args: map[string]any{"member_id": req.MemberID, "issued_by": tok.IssuedBy},
	})
	return &JoinResponse{
		ClanID:             clan.ClanID,
		ClanKeyB64:         clan.ClanKeyB64,
		ClanCertPEM:        clan.ClanCertPEM,
		ClanCertPrivKeyB64: clan.ClanCertPrivKeyB64,
		Roster:             updated,
	}, nil
}

// Welcome — paste-key join. The joiner has already proved they hold the
// clan_key (the HTTP layer's HMAC middleware verified that for us). We
// trust them, add them to the roster, and hand back the cert.
func (s *Service) Welcome(req WelcomeRequest) (*WelcomeResponse, error) {
	if req.MemberID == "" || req.MemberPubKey == "" {
		return nil, errors.New("member_id, member_pubkey_b64 required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return nil, err
	}
	if !clan.IsActive() {
		return nil, errors.New("not in a Clan")
	}
	now := time.Now().UTC()
	updated := upsertRoster(clan.Roster, state.RosterMember{
		MemberID:   req.MemberID,
		PubKeyB64:  req.MemberPubKey,
		State:      "admitted",
		AdmittedAt: now,
		LastSeenAt: now,
	})
	clan.Roster = updated
	if err := s.store.SaveClan(clan); err != nil {
		return nil, err
	}
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.welcome", Decision: "allow",
		Args: map[string]any{"member_id": req.MemberID},
	})
	return &WelcomeResponse{
		ClanID:             clan.ClanID,
		ClanCertPEM:        clan.ClanCertPEM,
		ClanCertPrivKeyB64: clan.ClanCertPrivKeyB64,
		Roster:             updated,
	}, nil
}

// Members returns the persisted roster.
func (s *Service) Members() ([]state.RosterMember, error) {
	clan, err := s.store.LoadClan()
	if err != nil {
		return nil, err
	}
	if !clan.IsActive() {
		return nil, errors.New("unaffiliated")
	}
	return clan.Roster, nil
}

// Leave clears local Clan state — this member becomes unaffiliated.
// Cross-Clan gossip of the leave intent lands in Phase H + D.
func (s *Service) Leave() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("already unaffiliated")
	}
	prevID := clan.ClanID
	if err := s.store.SaveClan(&state.Clan{}); err != nil {
		return err
	}
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.leave", Decision: "allow",
		Args: map[string]any{"clan_id": prevID},
	})
	return nil
}

// Revoke adds memberID to the persisted revocation list AND marks them
// revoked in the roster. Cross-Clan gossip on heartbeat lands in Phase H.
func (s *Service) Revoke(memberID, reason, revoker string) error {
	if memberID == "" {
		return errors.New("member_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}

	// Find the target and capture pubkey hash for the revocation list.
	var pubHash string
	for i, m := range clan.Roster {
		if m.MemberID == memberID {
			clan.Roster[i].State = "revoked"
			pubHash = "sha256:" + memberPubHash(m.PubKeyB64)
			break
		}
	}
	if pubHash == "" {
		return fmt.Errorf("member %q not in roster", memberID)
	}
	if err := s.store.SaveClan(clan); err != nil {
		return err
	}

	revs, err := s.store.LoadRevocations()
	if err != nil {
		return err
	}
	revs.Entries = append(revs.Entries, state.Revocation{
		MemberID:   memberID,
		PubKeyHash: pubHash,
		RevokedAt:  time.Now().UTC(),
		RevokedBy:  revoker,
		Reason:     reason,
	})
	if err := s.store.SaveRevocations(revs); err != nil {
		return err
	}
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.revoke", Decision: "allow",
		Args: map[string]any{"member_id": memberID, "revoked_by": revoker, "reason": reason},
	})
	return nil
}

// PromoteToActive flips a roster entry from "admitted" to "active" per
// spec §3.1 — "first successful capability advertisement". Idempotent;
// returns nil + no-op if the member is already active or not in roster.
// Phase H-3 of M4 closes the correctness gap project-review flagged:
// without this, members stayed "admitted" forever and the active-only
// quorum filter (R5) computed N=1 per node → 3-node consensus broken.
//
// Wired from peers/handlers.handleAdvertise after BindMember succeeds.
// Safe to call from any goroutine — the inner mutex serializes writes.
func (s *Service) PromoteToActive(memberID string) error {
	if memberID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return nil // unaffiliated; nothing to promote
	}
	changed := false
	for i, m := range clan.Roster {
		if m.MemberID == memberID && m.State == "admitted" {
			clan.Roster[i].State = "active"
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	if err := s.store.SaveClan(clan); err != nil {
		return err
	}
	_ = s.audit.Write(auditlog.Event{
		Server:   "minti-cland",
		Tool:     "membership.promote",
		Decision: "allow",
		Reason:   "first_advertisement",
		Args:     map[string]any{"member_id": memberID},
	})
	return nil
}

// SweepZombies removes members stuck in `admitted` state past ZombieMaxAge
// per spec §3.1. Returns the number purged. Idempotent — safe to call
// repeatedly on a ticker.
func (s *Service) SweepZombies() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return 0, err
	}
	if !clan.IsActive() {
		return 0, nil
	}
	now := time.Now().UTC()
	kept := make([]state.RosterMember, 0, len(clan.Roster))
	purged := 0
	for _, m := range clan.Roster {
		if m.State == "admitted" && now.Sub(m.AdmittedAt) > ZombieMaxAge {
			purged++
			continue
		}
		kept = append(kept, m)
	}
	if purged == 0 {
		return 0, nil
	}
	clan.Roster = kept
	if err := s.store.SaveClan(clan); err != nil {
		return 0, err
	}
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.zombie_sweep", Decision: "allow",
		Args: map[string]any{"purged": purged},
	})
	return purged, nil
}

// StartZombieSweep runs SweepZombies + invites.Sweep on a ticker until ctx
// is cancelled. Suitable for `go s.StartZombieSweep(ctx, interval)`.
func (s *Service) StartZombieSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if n, err := s.SweepZombies(); err != nil {
				s.log.Warn("zombie sweep failed", "err", err)
			} else if n > 0 {
				s.log.Info("zombie sweep", "purged_members", n)
			}
			if n := s.invites.Sweep(); n > 0 {
				s.log.Info("invite sweep", "purged_tokens", n)
			}
			if n := s.knocks.Sweep(); n > 0 {
				s.log.Info("knock sweep", "purged_knocks", n)
			}
		}
	}
}

// upsertRoster returns a new roster slice with the given member's entry
// replaced if present, appended otherwise.
func upsertRoster(roster []state.RosterMember, m state.RosterMember) []state.RosterMember {
	out := make([]state.RosterMember, len(roster))
	copy(out, roster)
	for i, existing := range out {
		if existing.MemberID == m.MemberID {
			out[i] = m
			return out
		}
	}
	return append(out, m)
}

// memberPubHash returns the sha256 hex of the base64-decoded pubkey bytes.
// Used to build the pin-revocation list entries — hash so even a leaked
// revocation file doesn't expose pubkeys directly.
func memberPubHash(pubB64 string) string {
	return shortHashHex(pubB64)
}
