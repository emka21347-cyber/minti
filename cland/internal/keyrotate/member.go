package keyrotate

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/transport"
)

// MemberHandler bundles the three Orchestrator-driven lifecycle endpoints
// from the member's perspective.
type MemberHandler struct {
	SelfID  string
	Store   *ProposeStore
	Rotater Rotater // typically *crypto.SimpleKeyProvider
	Audit   auditlog.Logger
	Log     *slog.Logger
}

// Register wires the three /clan/rotate-key/* endpoints.
func (h *MemberHandler) Register(srv *transport.Server) {
	srv.Handle("POST /clan/rotate-key/propose", h.handlePropose)
	srv.Handle("POST /clan/rotate-key/commit", h.handleCommit)
	srv.Handle("POST /clan/rotate-key/abort", h.handleAbort)
}

func (h *MemberHandler) handlePropose(w http.ResponseWriter, r *http.Request) {
	var req ProposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("decode propose", err))
		return
	}
	accepted, err := h.Store.Put(req)
	if err != nil {
		// ErrAlreadyPending or bad key both go 409; coordinator interprets.
		status := http.StatusConflict
		if errors.Is(err, ErrBadKey) {
			status = http.StatusBadRequest
		}
		h.auditDeny(req.ProposeID, "propose_rejected", err)
		writeJSON(w, status, AckResponse{ProposeID: req.ProposeID, Accepted: false, Reason: err.Error()})
		return
	}
	if !accepted {
		// Currently no path returns (false, nil) — but defensive.
		writeJSON(w, http.StatusConflict, AckResponse{ProposeID: req.ProposeID, Accepted: false})
		return
	}
	h.auditAllow(req.ProposeID, "propose_accepted")
	writeJSON(w, http.StatusOK, AckResponse{ProposeID: req.ProposeID, Accepted: true})
}

func (h *MemberHandler) handleCommit(w http.ResponseWriter, r *http.Request) {
	var req CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("decode commit", err))
		return
	}
	newKey, ok := h.Store.Take(req.ProposeID)
	if !ok {
		h.auditDeny(req.ProposeID, "commit_no_pending", ErrUnknownPropose)
		writeJSON(w, http.StatusConflict, AckResponse{ProposeID: req.ProposeID, Accepted: false, Reason: ErrUnknownPropose.Error()})
		return
	}
	grace := req.GraceDuration
	if grace <= 0 {
		grace = DefaultGraceDuration
	}
	if err := h.Rotater.Rotate(newKey, grace); err != nil {
		// Rotation failed in-place — propose was consumed; that's lossy but
		// safe: we never half-applied. Old key remains current.
		h.auditDeny(req.ProposeID, "commit_rotate_failed", err)
		writeJSON(w, http.StatusInternalServerError, AckResponse{ProposeID: req.ProposeID, Accepted: false, Reason: err.Error()})
		return
	}
	h.auditAllow(req.ProposeID, "commit_applied")
	writeJSON(w, http.StatusOK, AckResponse{ProposeID: req.ProposeID, Accepted: true})
}

func (h *MemberHandler) handleAbort(w http.ResponseWriter, r *http.Request) {
	var req AbortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("decode abort", err))
		return
	}
	cleared := h.Store.Clear(req.ProposeID)
	h.auditAllow(req.ProposeID, "abort_received")
	writeJSON(w, http.StatusOK, AckResponse{ProposeID: req.ProposeID, Accepted: cleared})
}

// ---------- helpers ----------

func (h *MemberHandler) auditAllow(proposeID, reason string) {
	_ = h.Audit.Write(auditlog.Event{
		MemberID: h.SelfID,
		Server:   "minti-cland",
		Tool:     "keyrotate.member",
		Decision: "allow",
		Reason:   reason,
		Args:     map[string]any{"propose_id": proposeID},
	})
}

func (h *MemberHandler) auditDeny(proposeID, reason string, err error) {
	_ = h.Audit.Write(auditlog.Event{
		MemberID: h.SelfID,
		Server:   "minti-cland",
		Tool:     "keyrotate.member",
		Decision: "deny",
		Reason:   reason,
		Args:     map[string]any{"propose_id": proposeID},
		Error:    errString(err),
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errBody(stage string, err error) map[string]string {
	return map[string]string{"error": stage, "detail": err.Error()}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
