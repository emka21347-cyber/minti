// Package rostersync implements cross-Clan gossip for the roster state
// machine (spec §3.1). Phase H-3 of M4.
//
// Wire surface:
//
//   GET /clan/roster  — returns the full local roster (HMAC-auth)
//
// Plus the heartbeat-driven sync: every /clan/heartbeat now carries the
// sender's roster_digest (sha256 of sorted "(member_id):(state)" pairs);
// on mismatch with the receiver's local digest, the receiver fetches the
// sender's full roster via the GET endpoint and merges using the §3.1
// state hierarchy (revoked > active > admitted > unaffiliated).
//
// This package mirrors revocations/sync.go almost line-for-line — same
// in-flight dedup, same Fetcher + AddressLookup interfaces, same 5s timeout.
// The merge logic is the only semantic difference: revocations is a union
// of an append-only list; roster is per-member state-progression.
//
// Together with revocations gossip (H-2), this covers the two membership
// mutations that need to propagate without a dedicated handshake.
package rostersync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
	"github.com/minti/cland/internal/transport"
)

// Fetcher abstracts the HMAC HTTP client used to GET /clan/roster from a
// peer. Lets tests inject a fake without spinning up TLS + HMAC.
type Fetcher interface {
	Do(req *http.Request) (*http.Response, error)
}

// AddressLookup returns a peer's "ip:port" — empty if unknown. Wired to
// peers.Registry.Snapshot in production.
type AddressLookup func(memberID string) string

// SyncerOpts is the dependency bundle.
type SyncerOpts struct {
	SelfID       string
	Store        *state.Store
	Registry     *peers.Registry
	Fetcher      Fetcher
	LookupAddr   AddressLookup
	Audit        auditlog.Logger
	Log          *slog.Logger
	FetchTimeout time.Duration // default 5s
}

// Syncer owns the digest-compare + fetch-on-mismatch logic for roster
// state transitions.
type Syncer struct {
	opts SyncerOpts

	mu       sync.Mutex
	inflight map[string]struct{} // key = peer member_id
}

func NewSyncer(opts SyncerOpts) (*Syncer, error) {
	if opts.SelfID == "" {
		return nil, errors.New("rostersync: SelfID required")
	}
	if opts.Store == nil || opts.Registry == nil {
		return nil, errors.New("rostersync: Store + Registry required")
	}
	if opts.Fetcher == nil {
		return nil, errors.New("rostersync: Fetcher required")
	}
	if opts.LookupAddr == nil {
		return nil, errors.New("rostersync: LookupAddr required")
	}
	if opts.Audit == nil {
		return nil, errors.New("rostersync: Audit required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = 5 * time.Second
	}
	return &Syncer{opts: opts, inflight: make(map[string]struct{})}, nil
}

// MaybeSync compares the inbound digest against local; if mismatched +
// non-empty, fires a fetch + merge. Synchronous in the caller's goroutine
// (typically the election handler's request goroutine, which is short).
// Per-peer in-flight dedup prevents concurrent fetches.
func (s *Syncer) MaybeSync(ctx context.Context, senderID, theirDigest string) bool {
	if theirDigest == "" {
		return false
	}
	local, err := s.opts.Store.LoadClan()
	if err != nil || local == nil {
		return false
	}
	if local.RosterDigest() == theirDigest {
		return false
	}

	s.mu.Lock()
	if _, busy := s.inflight[senderID]; busy {
		s.mu.Unlock()
		return false
	}
	s.inflight[senderID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, senderID)
		s.mu.Unlock()
	}()

	addr := s.opts.LookupAddr(senderID)
	if addr == "" {
		s.opts.Log.Debug("rostersync: peer addr unknown; deferring", "peer", senderID)
		return false
	}

	fctx, cancel := context.WithTimeout(ctx, s.opts.FetchTimeout)
	defer cancel()
	fetched, err := s.fetch(fctx, addr)
	if err != nil {
		s.opts.Log.Warn("rostersync: fetch failed",
			"peer", senderID, "addr", addr, "err", err)
		return true
	}

	merged := state.MergeRosterStates(local.Roster, fetched.Roster)
	// Persist only if something changed.
	if rosterEqual(merged, local.Roster) {
		return true
	}
	local.Roster = merged
	if err := s.opts.Store.SaveClan(local); err != nil {
		s.opts.Log.Error("rostersync: save failed", "err", err)
		return true
	}
	s.opts.Log.Info("rostersync: applied",
		"peer", senderID, "new_digest", local.RosterDigest(), "entries", len(merged))
	_ = s.opts.Audit.Write(auditlog.Event{
		MemberID: s.opts.SelfID,
		Server:   "minti-cland",
		Tool:     "rostersync",
		Decision: "allow",
		Reason:   "merged",
		Args:     map[string]any{"from_peer": senderID, "entries": len(merged)},
	})
	return true
}

// rosterResp is the GET /clan/roster response wire shape.
type rosterResp struct {
	Roster []state.RosterMember `json:"roster"`
}

func (s *Syncer) fetch(ctx context.Context, addr string) (*rosterResp, error) {
	url := "https://" + addr + "/clan/roster"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.opts.Fetcher.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var out rosterResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// rosterEqual reports whether two roster slices are byte-identical in
// (member_id, state) projection.
func rosterEqual(a, b []state.RosterMember) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].MemberID != b[i].MemberID || a[i].State != b[i].State {
			return false
		}
	}
	return true
}

// ---------- GET handler ----------

// Handler exposes GET /clan/roster behind the existing HMAC middleware.
type Handler struct {
	Store *state.Store
	Log   *slog.Logger
}

func (h *Handler) Register(srv *transport.Server) {
	srv.Handle("GET /clan/roster", h.handleList)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	clan, err := h.Store.LoadClan()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"load failed: %v"}`, err), http.StatusInternalServerError)
		return
	}
	out := rosterResp{}
	if clan != nil {
		out.Roster = clan.Roster
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}
