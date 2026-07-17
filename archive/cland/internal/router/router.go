// Package router implements the spec §6 Orchestrator routing layer.
//
// v1 scope (Phase F):
//   - Reasoning workloads (/v1/chat/completions, /v1/messages, /api/chat):
//     always served by the Orchestrator's own minti-runtime per spec §6.1.
//     Non-Orchestrator daemons transparently proxy to the current Orchestrator
//     over HMAC HTTPS. Orchestrators self-route via the loopback fast path
//     (D-M4.10) — straight to http://127.0.0.1:7780, bypassing TLS round-trip.
//   - Streaming pass-through for SSE (/v1/chat/completions, /v1/messages) and
//     NDJSON (/api/chat) is preserved end-to-end.
//
// Worker routing (system_score * (1 - load)) is wired in as a stub: the
// candidate pool + score computation are exposed, but the only inbound
// endpoint that uses it is /v1/embeddings, which runtime-adapter doesn't
// expose yet — once it does, this package will route to the picked worker
// the same way Orchestrator-proxy routes today.
//
// Authentication: all router endpoints register through transport.Server.
// Handle, so every inbound request was HMAC-verified by Phase B middleware
// before reaching this package. The local-agent → cland edge (plain HTTP on
// 127.0.0.1) is out of scope for v1; agents talk to runtime-adapter directly
// today, or attach the HMAC headers themselves.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/election"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/transport"
)

// PeerPoster is the subset of transport.Client the router needs. Decoupled
// for tests — the real client lives in cland/internal/transport.
type PeerPoster interface {
	Do(req *http.Request) (*http.Response, error)
}

// LocalDoer is the local-loopback HTTP client (plain http, no TLS) used for
// the self-routing fast path. Defaults to a *http.Client with timeouts;
// tests inject a fake.
type LocalDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Opts is the dep bundle for NewRouter.
type Opts struct {
	SelfID         string
	ClanID         string
	ElectionState  *election.State    // for IAmOrchestrator / Snapshot
	Registry       *peers.Registry    // for orchestrator address + worker pool
	RuntimeBaseURL string             // typically "http://127.0.0.1:7780"
	LocalClient    LocalDoer          // optional; defaults to *http.Client with sane timeouts
	PeerClient     PeerPoster         // transport.Client when proxying to a peer
	Audit          auditlog.Logger
	Log            *slog.Logger

	// PeerProxyTimeout caps total time a non-Orchestrator daemon spends
	// waiting on the Orchestrator's response before giving up. Independent
	// of the underlying http.Client timeouts. 30 min matches runtime-adapter's
	// own non-streaming ceiling.
	PeerProxyTimeout time.Duration
}

// Router carries the deps + a handful of atomic counters for test
// observability.
type Router struct {
	opts Opts

	requests          atomic.Uint64
	selfRoutes        atomic.Uint64
	peerProxies       atomic.Uint64
	rejectsNonOrch    atomic.Uint64
	rejectsForeignCID atomic.Uint64
}

// NewRouter validates opts.
func NewRouter(opts Opts) (*Router, error) {
	if opts.SelfID == "" || opts.ClanID == "" {
		return nil, errors.New("router: SelfID + ClanID required")
	}
	if opts.ElectionState == nil {
		return nil, errors.New("router: ElectionState required")
	}
	if opts.Registry == nil {
		return nil, errors.New("router: Registry required")
	}
	if opts.RuntimeBaseURL == "" {
		opts.RuntimeBaseURL = "http://127.0.0.1:7780"
	}
	if opts.LocalClient == nil {
		opts.LocalClient = &http.Client{Timeout: 30 * time.Minute}
	}
	if opts.PeerClient == nil {
		return nil, errors.New("router: PeerClient required for cross-Clan proxying")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.PeerProxyTimeout <= 0 {
		opts.PeerProxyTimeout = 30 * time.Minute
	}
	return &Router{opts: opts}, nil
}

// Register wires the v1 reasoning endpoints onto a transport.Server. All
// three flow through the same routeReasoning() handler — only the upstream
// path varies.
func (r *Router) Register(srv *transport.Server) {
	srv.Handle("POST /v1/chat/completions", r.handleReasoning)
	srv.Handle("POST /v1/messages", r.handleReasoning)
	srv.Handle("POST /api/chat", r.handleReasoning)
}

// Counters expose hot-path counters for tests + observability.
func (r *Router) Counters() Counters {
	return Counters{
		Requests:          r.requests.Load(),
		SelfRoutes:        r.selfRoutes.Load(),
		PeerProxies:       r.peerProxies.Load(),
		RejectsNonOrch:    r.rejectsNonOrch.Load(),
		RejectsForeignCID: r.rejectsForeignCID.Load(),
	}
}

type Counters struct {
	Requests          uint64
	SelfRoutes        uint64
	PeerProxies       uint64
	RejectsNonOrch    uint64
	RejectsForeignCID uint64
}

// ---------- handlers ----------

func (r *Router) handleReasoning(w http.ResponseWriter, req *http.Request) {
	r.requests.Add(1)

	snap := r.opts.ElectionState.Snapshot()
	if snap.CurrentOrchestrator == "" {
		http.Error(w, `{"error":"no orchestrator elected yet"}`, http.StatusServiceUnavailable)
		return
	}

	if snap.CurrentOrchestrator == r.opts.SelfID {
		// Self-route — fast path, no TLS, no HMAC.
		r.selfRoutes.Add(1)
		r.proxyLocal(w, req)
		return
	}

	// Need to find the Orchestrator's address in our peer registry.
	addr := r.findPeerAddr(snap.CurrentOrchestrator)
	if addr == "" {
		r.opts.Log.Warn("router: orchestrator address unknown",
			"orchestrator", snap.CurrentOrchestrator)
		w.Header().Set("X-Minti-Expected-Orchestrator", snap.CurrentOrchestrator)
		http.Error(w, `{"error":"orchestrator address not in peer registry"}`, http.StatusServiceUnavailable)
		r.rejectsNonOrch.Add(1)
		return
	}
	r.peerProxies.Add(1)
	r.proxyPeer(w, req, addr)
}

// findPeerAddr looks up a member's reachable address. Returns "" if unknown.
func (r *Router) findPeerAddr(memberID string) string {
	_, members := r.opts.Registry.Snapshot()
	for _, m := range members {
		if m.MemberID == memberID {
			return m.Address
		}
	}
	return ""
}

// ---------- self-route (loopback fast path) ----------

func (r *Router) proxyLocal(w http.ResponseWriter, req *http.Request) {
	upstream := r.opts.RuntimeBaseURL + req.URL.Path
	upReq, err := http.NewRequestWithContext(req.Context(), req.Method, upstream, req.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"build upstream: %v"}`, err), http.StatusInternalServerError)
		return
	}
	copyForwardableHeaders(upReq.Header, req.Header)

	resp, err := r.opts.LocalClient.Do(upReq)
	if err != nil {
		r.opts.Log.Warn("router: local runtime call failed", "path", req.URL.Path, "err", err)
		http.Error(w, fmt.Sprintf(`{"error":"local runtime: %v"}`, err), http.StatusBadGateway)
		_ = r.opts.Audit.Write(auditlog.Event{
			MemberID: r.opts.SelfID, Server: "minti-cland", Tool: "router.self_route",
			Decision: "deny", Reason: "local_runtime_unreachable", Error: err.Error(),
			Args: map[string]any{"path": req.URL.Path},
		})
		return
	}
	defer resp.Body.Close()
	streamResponse(w, resp)
	_ = r.opts.Audit.Write(auditlog.Event{
		MemberID: r.opts.SelfID, Server: "minti-cland", Tool: "router.self_route",
		Decision: "allow", Args: map[string]any{"path": req.URL.Path, "status": resp.StatusCode},
	})
}

// ---------- peer-proxy path (HMAC HTTPS to Orchestrator) ----------

func (r *Router) proxyPeer(w http.ResponseWriter, req *http.Request, peerAddr string) {
	ctx, cancel := context.WithTimeout(req.Context(), r.opts.PeerProxyTimeout)
	defer cancel()

	upstream := "https://" + peerAddr + req.URL.Path
	upReq, err := http.NewRequestWithContext(ctx, req.Method, upstream, req.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"build upstream: %v"}`, err), http.StatusInternalServerError)
		return
	}
	copyForwardableHeaders(upReq.Header, req.Header)

	resp, err := r.opts.PeerClient.Do(upReq)
	if err != nil {
		r.opts.Log.Warn("router: peer-proxy call failed", "peer_addr", peerAddr, "path", req.URL.Path, "err", err)
		http.Error(w, fmt.Sprintf(`{"error":"peer proxy: %v"}`, err), http.StatusBadGateway)
		_ = r.opts.Audit.Write(auditlog.Event{
			MemberID: r.opts.SelfID, Server: "minti-cland", Tool: "router.peer_proxy",
			Decision: "deny", Reason: "peer_unreachable", Error: err.Error(),
			Args: map[string]any{"path": req.URL.Path, "peer_addr": peerAddr},
		})
		return
	}
	defer resp.Body.Close()
	streamResponse(w, resp)
	_ = r.opts.Audit.Write(auditlog.Event{
		MemberID: r.opts.SelfID, Server: "minti-cland", Tool: "router.peer_proxy",
		Decision: "allow", Args: map[string]any{"path": req.URL.Path, "peer_addr": peerAddr, "status": resp.StatusCode},
	})
}

// streamResponse copies headers + status + body from upstream to client.
// For SSE / NDJSON it preserves chunk boundaries by flushing on every Write
// (io.Copy does small writes by default; the flusher catches them).
func streamResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		// Hop-by-hop headers are dropped per RFC 7230 §6.1.
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	if flusher == nil {
		_, _ = io.Copy(w, resp.Body)
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			flusher.Flush()
		}
		if rerr != nil {
			return
		}
	}
}

// copyForwardableHeaders copies request headers from `src` to `dst`, skipping
// hop-by-hop headers and the HMAC auth quad (the Orchestrator will need to
// re-sign on its own proxy hop; the inbound HMAC was for cland-to-cland auth,
// not for the upstream runtime-adapter or peer).
func copyForwardableHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHopByHop(k) {
			continue
		}
		if isMintiAuth(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isHopByHop(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Host":
		return true
	}
	return false
}

func isMintiAuth(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "X-Minti-Member", "X-Minti-Timestamp", "X-Minti-Nonce", "X-Minti-Hmac":
		return true
	}
	return false
}
