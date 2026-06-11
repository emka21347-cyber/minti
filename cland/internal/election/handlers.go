package election

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/minti/cland/internal/state"
	"github.com/minti/cland/internal/transport"
)

// RevocationsSyncer is the Phase H-2 hook called from /clan/heartbeat — on
// digest mismatch with the sender, fetch + merge the sender's revocations
// list. Optional dep; nil = sync disabled (e.g. for tests).
type RevocationsSyncer interface {
	MaybeSync(ctx context.Context, senderID, theirDigest string) bool
}

// RosterSyncer is the Phase H-3 counterpart for roster state transitions
// (admitted→active per spec §3.1, etc.). Same shape as RevocationsSyncer.
type RosterSyncer interface {
	MaybeSync(ctx context.Context, senderID, theirDigest string) bool
}

// MemorySyncer is the Memory M2 third passenger (spec §13.5): on memory
// digest mismatch, fetch GET /clan/memory from the sender and merge. Same
// shape as the other two; optional-nil and independent of them.
type MemorySyncer interface {
	MaybeSync(ctx context.Context, senderID, theirDigest string) bool
}

// Handlers bundles the four Phase E endpoints:
//
//   POST /clan/heartbeat        — Orchestrator → peers (anti-spoof + term gate
//                                 + Phase H-2 revocations digest sync)
//   GET  /clan/orchestrator     — diagnostic (current term + winner + lease)
//   GET  /clan/election/history — diagnostic ring buffer
//   POST /clan/pin              — local-loop only; flips PinnedOrchestrator
//
// All routes are HMAC-authenticated by transport.Server.Handle; the pin
// endpoint is functionally local but goes through the same auth path so we
// don't need a separate insecure listener.
type Handlers struct {
	Engine *Engine
	Store  *state.Store
	Bump   func() // optional; called after /clan/pin succeeds to propagate the new pin
	RevocationsSync RevocationsSyncer // optional; Phase H-2
	RosterSync      RosterSyncer      // optional; Phase H-3
	MemorySync      MemorySyncer      // optional; Memory M2 (spec §13.5)

	// MemoryDigest returns the local cached graph digest for the §13.5
	// response leg: heartbeats only flow Orchestrator→peers, so the RESPONSE
	// carries the receiver's digest back — otherwise follower edits would
	// never reach the Orchestrator. Optional; nil = field omitted.
	MemoryDigest func() string
}

// HeartbeatAck is the body of a 200 response to POST /clan/heartbeat.
// Exported so the engine can decode it for the §13.5 response leg.
type HeartbeatAck struct {
	Accepted     bool   `json:"accepted"`
	TermChanged  bool   `json:"term_changed"`
	OrchChanged  bool   `json:"orch_changed"`
	MemoryDigest string `json:"memory_digest,omitempty"`
}

func (h *Handlers) Register(srv *transport.Server) {
	srv.Handle("POST /clan/heartbeat", h.handleHeartbeat)
	srv.Handle("GET /clan/orchestrator", h.handleOrchestrator)
	srv.Handle("GET /clan/election/history", h.handleHistory)
	srv.Handle("POST /clan/pin", h.handlePin)
}

// ---------- POST /clan/heartbeat ----------

type heartbeatErrorBody struct {
	Error                string `json:"error"`
	ExpectedOrchestrator string `json:"expected_orchestrator,omitempty"`
}

func (h *Handlers) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb Heartbeat
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sender := transport.OriginMember(r.Context())
	if sender == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no authenticated sender"})
		return
	}
	res, err := h.Engine.OnHeartbeatReceived(hb, sender, time.Now())
	if err != nil {
		// Spec §5.4 step 4: peer disagrees → 409 Conflict with expected_orchestrator.
		// Term-stale + anti-spoof both fall here; clients differentiate by the
		// error string + presence of expected_orchestrator.
		body := heartbeatErrorBody{Error: err.Error()}
		switch {
		case errors.Is(err, ErrAntiSpoof):
			// Tell sender who WE think should be Orchestrator.
			if clan, lerr := h.Store.LoadClan(); lerr == nil && clan != nil {
				if cand, _ := h.Engine.selectCandidate(clan); cand.MemberID != "" {
					body.ExpectedOrchestrator = cand.MemberID
				}
			}
			writeJSON(w, http.StatusConflict, body)
		case errors.Is(err, ErrTermStale):
			writeJSON(w, http.StatusConflict, body)
		default:
			writeJSON(w, http.StatusBadRequest, body)
		}
		return
	}
	// Phase H-2 + H-3: opportunistically sync revocations + roster state
	// transitions after the heartbeat passes lease/term/anti-spoof. Cheap
	// when digests match; per-peer in-flight dedup in each Syncer when
	// they don't.
	if h.RevocationsSync != nil && hb.RevocationsDigest != "" {
		_ = h.RevocationsSync.MaybeSync(context.Background(), sender, hb.RevocationsDigest)
	}
	if h.RosterSync != nil && hb.RosterDigest != "" {
		_ = h.RosterSync.MaybeSync(context.Background(), sender, hb.RosterDigest)
	}
	if h.MemorySync != nil && hb.MemoryDigest != "" {
		_ = h.MemorySync.MaybeSync(context.Background(), sender, hb.MemoryDigest)
	}
	ack := HeartbeatAck{
		Accepted:    res.Accepted,
		TermChanged: res.TermChanged,
		OrchChanged: res.OrchChanged,
	}
	if h.MemoryDigest != nil {
		// §13.5 response leg: tell the Orchestrator what OUR graph looks
		// like so follower edits flow upstream. Cached read — no I/O.
		ack.MemoryDigest = h.MemoryDigest()
	}
	writeJSON(w, http.StatusOK, ack)
}

// ---------- GET /clan/orchestrator ----------

// OrchestratorResponse is the body of GET /clan/orchestrator. Exported so
// CLI subcommands can decode it.
type OrchestratorResponse struct {
	CurrentOrchestrator string    `json:"current_orchestrator"`
	CurrentTerm         uint64    `json:"current_term"`
	LeaseExpires        time.Time `json:"lease_expires"`
	Self                string    `json:"self"`
	IsSelf              bool      `json:"is_self"`
}

func (h *Handlers) handleOrchestrator(w http.ResponseWriter, r *http.Request) {
	snap := h.Engine.opts.State.Snapshot()
	writeJSON(w, http.StatusOK, OrchestratorResponse{
		CurrentOrchestrator: snap.CurrentOrchestrator,
		CurrentTerm:         snap.CurrentTerm,
		LeaseExpires:        snap.LeaseExpires,
		Self:                snap.SelfID,
		IsSelf:              snap.CurrentOrchestrator == snap.SelfID,
	})
}

// ---------- GET /clan/election/history ----------

// HistoryResponse is the body of GET /clan/election/history.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

func (h *Handlers) handleHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HistoryResponse{Entries: h.Engine.opts.State.History()})
}

// ---------- POST /clan/pin ----------

// PinRequest is the body of POST /clan/pin.
type PinRequest struct {
	Value bool `json:"value"` // true = pin self, false = clear self-pin
}

// PinResponse is the body of POST /clan/pin.
type PinResponse struct {
	PinnedOrchestrator bool `json:"pinned_orchestrator"`
}

func (h *Handlers) handlePin(w http.ResponseWriter, r *http.Request) {
	var req PinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	clan, err := h.Store.LoadClan()
	if err != nil || clan == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load clan failed"})
		return
	}
	if clan.PinnedOrchestrator == req.Value {
		writeJSON(w, http.StatusOK, PinResponse{PinnedOrchestrator: clan.PinnedOrchestrator})
		return
	}
	clan.PinnedOrchestrator = req.Value
	if err := h.Store.SaveClan(clan); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Bump != nil {
		h.Bump() // propagate pin via the next advertisement immediately
	}
	writeJSON(w, http.StatusOK, PinResponse{PinnedOrchestrator: req.Value})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
