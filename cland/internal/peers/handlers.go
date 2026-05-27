package peers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/minti/cland/internal/transport"
)

// PeerAddRequest is the body of POST /clan/peer-add.
type PeerAddRequest struct {
	Address string `json:"address"`
}

// PeersListResponse is the body of GET /clan/peers (diagnostic).
type PeersListResponse struct {
	Candidates []Candidate `json:"candidates"`
	Members    []Member    `json:"members"`
}

// Handlers bundles the three Phase D endpoints. Pass a `bump` callback to
// trigger an immediate advertisement broadcast on peer-add.
type Handlers struct {
	Registry *Registry
	Bump     func() // optional; called after /clan/peer-add succeeds
}

// Register wires the three Phase D endpoints onto a transport.Server.
func (h *Handlers) Register(srv *transport.Server) {
	srv.Handle("POST /clan/advertise", h.handleAdvertise)
	srv.Handle("POST /clan/peer-add", h.handlePeerAdd)
	srv.Handle("GET /clan/peers", h.handleListPeers)
}

func (h *Handlers) handleAdvertise(w http.ResponseWriter, r *http.Request) {
	var ad Advertisement
	if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := h.Registry.BindMember(&ad, r.RemoteAddr); err != nil {
		switch {
		case errors.Is(err, ErrRevoked):
			writeErr(w, http.StatusForbidden, err)
		case errors.Is(err, ErrRegistryFull):
			writeErr(w, http.StatusServiceUnavailable, err)
		default:
			writeErr(w, http.StatusBadRequest, err)
		}
		return
	}
	// 200 OK with empty body per spec §4.2 v0.3.
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) handlePeerAdd(w http.ResponseWriter, r *http.Request) {
	var req PeerAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	origin := transport.OriginMember(r.Context())
	if origin == "" {
		// Test fallback (production middleware always sets the transport key).
		origin = originFromTestCtx(r.Context())
	}
	if err := h.Registry.AddPeer(origin, req.Address); err != nil {
		switch {
		case errors.Is(err, ErrRateLimited):
			writeErr(w, http.StatusTooManyRequests, err)
		case errors.Is(err, ErrRegistryFull):
			writeErr(w, http.StatusServiceUnavailable, err)
		default:
			// Includes pre-dial failure (peer unreachable).
			writeErr(w, http.StatusServiceUnavailable, err)
		}
		return
	}
	if h.Bump != nil {
		h.Bump()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "added", "address": req.Address})
}

func (h *Handlers) handleListPeers(w http.ResponseWriter, r *http.Request) {
	cs, ms := h.Registry.Snapshot()
	writeJSON(w, http.StatusOK, PeersListResponse{Candidates: cs, Members: ms})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
