package election

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/minti/cland/internal/state"
	"github.com/minti/cland/internal/transport"
)

// Handlers bundles the four Phase E endpoints:
//
//   POST /clan/heartbeat        — Orchestrator → peers (anti-spoof + term gate)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted":     res.Accepted,
		"term_changed": res.TermChanged,
		"orch_changed": res.OrchChanged,
	})
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
