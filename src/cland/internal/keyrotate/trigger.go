package keyrotate

import (
	"encoding/json"
	"net/http"

	"github.com/minti/cland/internal/transport"
)

// OrchestratorGate is a small predicate the trigger handler calls to confirm
// this daemon is currently the Orchestrator. Decoupled so it can be wired to
// election.State.IAmOrchestrator without importing election from here.
type OrchestratorGate func() bool

// TriggerHandler exposes a single POST /clan/rotate-key endpoint that the
// local CLI (or any HMAC-authenticated caller) hits to kick off a rotation.
// Only the current Orchestrator may proceed; everyone else returns 503 with
// a helpful body indicating where to retry.
type TriggerHandler struct {
	Coordinator *Coordinator
	IsOrchestrator OrchestratorGate
}

// Register wires POST /clan/rotate-key.
func (h *TriggerHandler) Register(srv *transport.Server) {
	srv.Handle("POST /clan/rotate-key", h.handle)
}

func (h *TriggerHandler) handle(w http.ResponseWriter, r *http.Request) {
	if !h.IsOrchestrator() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "not orchestrator",
			"detail": "only the current Orchestrator may initiate a key rotation",
		})
		return
	}
	res, err := h.Coordinator.Rotate(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "rotate failed",
			"detail": err.Error(),
		})
		return
	}
	status := http.StatusOK
	if !res.Committed {
		status = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}
