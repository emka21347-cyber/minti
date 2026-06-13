package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/minti/cland/internal/agent"
	"github.com/minti/cland/internal/toolexec"
	"github.com/minti/cland/internal/transport"
)

// agentService hosts the native agent loop inside the cland daemon. The loop
// runs HERE (not in the workspace) because it spawns MCP server subprocesses
// for tool execution, and only the cland unit has network egress — the
// workspace unit is loopback-only (IPAddressDeny=any). Model turns route through
// the daemon's own /v1/messages over loopback (reusing the router); change tools
// are gated by a per-request ChannelApprover resolved by POST /agent/approve.
type agentService struct {
	executor    *toolexec.Executor
	cli         *transport.Client // loopback HMAC client for model calls
	base        string            // https://127.0.0.1:<port>
	defaultModel string
	log         *slog.Logger

	catalogOnce sync.Once
	catalog     *agent.Catalog
	catalogErr  error

	mu      sync.Mutex
	pending map[string]*agent.ChannelApprover // reqID → approver
	reqSeq  atomic.Uint64
}

func newAgentService(executor *toolexec.Executor, cli *transport.Client, base, defaultModel string, log *slog.Logger) *agentService {
	return &agentService{
		executor:     executor,
		cli:          cli,
		base:         base,
		defaultModel: defaultModel,
		log:          log,
		pending:      make(map[string]*agent.ChannelApprover),
	}
}

func (s *agentService) register(srv *transport.Server) {
	srv.Handle("POST /agent/chat", s.handleChat)
	srv.Handle("POST /agent/approve", s.handleApprove)
}

// getCatalog builds the read+change tool catalog once (spawning each MCP server
// to list its tools) and caches it. Built lazily so a momentarily-missing MCP
// binary doesn't fail daemon startup.
func (s *agentService) getCatalog(ctx context.Context) (*agent.Catalog, error) {
	s.catalogOnce.Do(func() {
		// nil filter → offer all tools; change tools are gated by the approver.
		s.catalog, s.catalogErr = agent.BuildCatalog(ctx, s.executor, nil, s.log)
	})
	return s.catalog, s.catalogErr
}

type agentChatRequest struct {
	Message  string `json:"message"`
	Model    string `json:"model,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

func (s *agentService) handleChat(w http.ResponseWriter, r *http.Request) {
	var req agentChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		http.Error(w, `{"error":"message required"}`, http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unsupported"}`, http.StatusInternalServerError)
		return
	}

	catalog, err := s.getCatalog(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"tool catalog: %v"}`, err), http.StatusBadGateway)
		return
	}

	model := req.Model
	if model == "" {
		model = s.defaultModel
	}
	reqID := fmt.Sprintf("a%d", s.reqSeq.Add(1))

	// A read-only session gets no approver (change tools refused); otherwise a
	// per-request channel approver the browser resolves via POST /agent/approve.
	var approver agent.Approver
	if !req.ReadOnly {
		ca := agent.NewChannelApprover(0)
		s.mu.Lock()
		s.pending[reqID] = ca
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.pending, reqID)
			s.mu.Unlock()
		}()
		approver = ca
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	emitter := &reqIDEmitter{reqID: reqID, inner: agent.NewNDJSONEmitter(w)}
	loop := &agent.Loop{
		Caller:   &anthropicCaller{cli: s.cli, base: s.base, model: model},
		Executor: s.executor,
		Catalog:  catalog,
		Emitter:  emitter,
		Approver: approver,
		System:   defaultAgentSystem,
		Log:      s.log,
	}
	if err := loop.Run(r.Context(), req.Message); err != nil {
		s.log.Warn("agent: loop ended with error", "req_id", reqID, "err", err)
		// The loop already emitted an `error` event; nothing more to send.
	}
}

type agentApproveRequest struct {
	ReqID   string `json:"req_id"`
	CallID  string `json:"call_id"`
	Approve bool   `json:"approve"`
}

func (s *agentService) handleApprove(w http.ResponseWriter, r *http.Request) {
	var req agentApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReqID == "" || req.CallID == "" {
		http.Error(w, `{"error":"req_id and call_id required"}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	ca := s.pending[req.ReqID]
	s.mu.Unlock()
	if ca == nil {
		http.Error(w, `{"error":"no such agent request (already finished?)"}`, http.StatusNotFound)
		return
	}
	dec := agent.DecisionDeny
	if req.Approve {
		dec = agent.DecisionApprove
	}
	if err := ca.Resolve(req.CallID, dec); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// reqIDEmitter stamps every event with the agent request id before delegating,
// so the browser can correlate approval_required → POST /agent/approve.
type reqIDEmitter struct {
	reqID string
	inner agent.Emitter
}

func (e *reqIDEmitter) Emit(ev agent.Event) error {
	ev.ReqID = e.reqID
	return e.inner.Emit(ev)
}
