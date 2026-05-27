// Package revocations implements spec §3.4 cross-Clan gossip for the
// revocation list. Phase H-2 of M4.
//
// Wire surface:
//
//   GET /clan/revocations   — returns the full revocations list (HMAC-auth)
//
// Plus the heartbeat-driven sync: every /clan/heartbeat carries the sender's
// revocations digest (sha256 of sorted member_ids); on mismatch with the
// receiver's local digest, the receiver fetches the sender's full list via
// the GET endpoint and merges (union, dedup by member_id) into local state.
//
// Eventual consistency: even if peers were partitioned during a revocation,
// the digest gossip ensures all members converge on the union of all known
// revocations once the partition heals (within one heartbeat round, ~2 s).
package revocations

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

// Fetcher abstracts the HMAC HTTP client used to GET /clan/revocations
// from a peer. Lets tests inject a fake without spinning up TLS + HMAC.
type Fetcher interface {
	Do(req *http.Request) (*http.Response, error)
}

// AddressLookup returns a peer's "ip:port" — nil/empty if unknown. Wired
// to peers.Registry.Snapshot in production.
type AddressLookup func(memberID string) string

// SyncerOpts is the dependency bundle.
type SyncerOpts struct {
	SelfID      string
	Store       *state.Store
	Registry    *peers.Registry
	Fetcher     Fetcher
	LookupAddr  AddressLookup
	Audit       auditlog.Logger
	Log         *slog.Logger
	FetchTimeout time.Duration // default 5s
}

// Syncer owns the digest-compare + fetch-on-mismatch logic.
type Syncer struct {
	opts SyncerOpts

	// In-flight de-dup: don't fire concurrent fetches for the same peer
	// when many heartbeats arrive close together.
	mu       sync.Mutex
	inflight map[string]struct{} // key = peer member_id
}

func NewSyncer(opts SyncerOpts) (*Syncer, error) {
	if opts.SelfID == "" {
		return nil, errors.New("revocations: SelfID required")
	}
	if opts.Store == nil || opts.Registry == nil {
		return nil, errors.New("revocations: Store + Registry required")
	}
	if opts.Fetcher == nil {
		return nil, errors.New("revocations: Fetcher required")
	}
	if opts.LookupAddr == nil {
		return nil, errors.New("revocations: LookupAddr required")
	}
	if opts.Audit == nil {
		return nil, errors.New("revocations: Audit required")
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
// non-empty, fires a fetch + merge. Fetches run synchronously (in the
// caller's goroutine — the election handler's request goroutine, which is
// short-lived). Returns true iff a fetch was actually triggered.
//
// Per-peer in-flight dedup: a second MaybeSync for the same peer while one
// is in progress returns false immediately.
func (s *Syncer) MaybeSync(ctx context.Context, senderID, theirDigest string) bool {
	if theirDigest == "" {
		return false
	}
	local, err := s.opts.Store.LoadRevocations()
	if err != nil {
		s.opts.Log.Warn("revocations sync: load local failed", "err", err)
		return false
	}
	localDigest := local.Digest()
	if localDigest == theirDigest {
		return false
	}

	// Dedup concurrent fetches for this peer.
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
		s.opts.Log.Debug("revocations sync: peer addr unknown; deferring",
			"peer", senderID)
		return false
	}

	fctx, cancel := context.WithTimeout(ctx, s.opts.FetchTimeout)
	defer cancel()
	fetched, err := s.fetch(fctx, addr)
	if err != nil {
		s.opts.Log.Warn("revocations sync: fetch failed",
			"peer", senderID, "addr", addr, "err", err)
		return true
	}

	merged := local.Merge(fetched)
	// Only persist when something actually changed (avoid pointless writes).
	if len(merged.Entries) > len(local.Entries) {
		if err := s.opts.Store.SaveRevocations(merged); err != nil {
			s.opts.Log.Error("revocations sync: save failed", "err", err)
			return true
		}
		s.opts.Registry.SetRevocations(merged)
		s.opts.Log.Info("revocations sync: applied",
			"peer", senderID, "new_count", len(merged.Entries),
			"prev_count", len(local.Entries))
		_ = s.opts.Audit.Write(auditlog.Event{
			MemberID: s.opts.SelfID,
			Server:   "minti-cland",
			Tool:     "revocations.sync",
			Decision: "allow",
			Reason:   "merged",
			Args: map[string]any{
				"from_peer":   senderID,
				"prev_count":  len(local.Entries),
				"new_count":   len(merged.Entries),
				"new_digest":  merged.Digest(),
			},
		})
	}
	return true
}

// fetch GETs /clan/revocations from peer at `addr` and decodes the response.
func (s *Syncer) fetch(ctx context.Context, addr string) (*state.Revocations, error) {
	url := "https://" + addr + "/clan/revocations"
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
	var out state.Revocations
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// ---------- GET handler ----------

// Handler exposes GET /clan/revocations. Registered behind the existing
// HMAC middleware so only Clan members can read the list.
type Handler struct {
	Store *state.Store
	Log   *slog.Logger
}

func (h *Handler) Register(srv *transport.Server) {
	srv.Handle("GET /clan/revocations", h.handleList)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	revs, err := h.Store.LoadRevocations()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"load failed: %v"}`, err), http.StatusInternalServerError)
		return
	}
	if revs == nil {
		revs = &state.Revocations{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(revs)
}
