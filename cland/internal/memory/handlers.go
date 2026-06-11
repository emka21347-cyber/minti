package memory

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/minti/cland/internal/transport"
)

// Handler exposes the spec §13.6 read/write surface behind the existing HMAC
// middleware:
//
//	GET  /clan/memory         — full graph
//	GET  /clan/memory/digest  — cached digest (cheap poll for UIs; gossip
//	                            itself rides the heartbeat, not this)
//	POST /clan/memory/node    — create/update one node (author daemon-set)
//	POST /clan/memory/edge    — add one edge (set-union)
//
// POST /clan/memory/import arrives with the blueprint work (M4).
type Handler struct {
	Svc *Service
	Log *slog.Logger

	// Origin extracts the authenticated member id from the request context.
	// nil = transport.OriginMember (production); tests inject their own.
	Origin func(ctx context.Context) string
}

func (h *Handler) Register(srv *transport.Server) {
	srv.Handle("GET /clan/memory", h.handleGet)
	srv.Handle("GET /clan/memory/digest", h.handleDigest)
	srv.Handle("POST /clan/memory/node", h.handleNode)
	srv.Handle("POST /clan/memory/edge", h.handleEdge)
	srv.Handle("POST /clan/memory/import", h.handleImport)
}

func (h *Handler) origin(r *http.Request) string {
	if h.Origin != nil {
		return h.Origin(r.Context())
	}
	return transport.OriginMember(r.Context())
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	b, err := h.Svc.GraphJSON()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// DigestResponse is the body of GET /clan/memory/digest.
type DigestResponse struct {
	Digest string `json:"digest"`
}

func (h *Handler) handleDigest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, DigestResponse{Digest: h.Svc.Digest()})
}

func (h *Handler) handleNode(w http.ResponseWriter, r *http.Request) {
	var n Node
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	origin := h.origin(r)
	if origin == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no authenticated sender"})
		return
	}
	stored, err := h.Svc.AddOrUpdateNode(origin, n, time.Now())
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// EdgeResponse is the body of POST /clan/memory/edge.
type EdgeResponse struct {
	Added bool `json:"added"`
}

func (h *Handler) handleEdge(w http.ResponseWriter, r *http.Request) {
	var e Edge
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	origin := h.origin(r)
	if origin == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no authenticated sender"})
		return
	}
	added, err := h.Svc.AddEdge(origin, e, time.Now())
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, EdgeResponse{Added: added})
}

// ImportRequest is the body of POST /clan/memory/import (spec §13.10).
type ImportRequest struct {
	Blueprint *Blueprint `json:"blueprint"`
	Mode      string     `json:"mode"` // "merge" (default) | "replace"
}

// ImportResponse is the body of a successful import.
type ImportResponse struct {
	Changed bool   `json:"changed"`
	Digest  string `json:"digest"`
}

func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	var req ImportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxFetchBytes+1)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	origin := h.origin(r)
	if origin == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no authenticated sender"})
		return
	}
	if err := ValidateBlueprint(req.Blueprint); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Spec §13.6 / §13.10 (M0 review F4): "replace" is a destructive
	// primitive and v1 HMAC under a SHARED key cannot prove which member
	// sent a request — so replace is refused unless the TCP peer is this
	// machine itself (loopback or one of our own interface addresses; the
	// CLI dials the configured listen address, which may be a LAN IP).
	if req.Mode == "replace" && !requestFromSelf(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "mode \"replace\" is loopback-only (spec §13.6); remote members may merge",
		})
		return
	}
	changed, err := h.Svc.ImportGraph(MarkImported(req.Blueprint.Graph), req.Mode, origin)
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ImportResponse{Changed: changed, Digest: h.Svc.Digest()})
}

// requestFromSelf reports whether the request's TCP source address belongs
// to this machine (loopback or any local interface IP).
func requestFromSelf(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// statusFor maps service errors to HTTP: caps → 409 (spec §13.6), anything
// else from validation → 400.
func statusFor(err error) int {
	if errors.Is(err, ErrCap) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
