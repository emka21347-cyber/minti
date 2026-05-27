package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/transport"
)

// ExecutorIface is the surface the handler uses. Decoupled from the concrete
// Executor so tests can inject a fake without spawning real subprocesses.
type ExecutorIface interface {
	Execute(ctx context.Context, wireTool string, args map[string]any) (*ExecResult, string, string, error)
}

// HandlerOpts is the dep bundle for NewHandler.
type HandlerOpts struct {
	SelfID       string
	KeyProvider  crypto.KeyProvider // for HMAC verify (current + grace)
	Executor     ExecutorIface
	Replay       *ReplayCache
	Audit        auditlog.Logger
	Log          *slog.Logger
	MaxLifetime  time.Duration // max (exp - approved_at); zero → DefaultMaxTokenLifetime
}

// Handler bundles the /mcp/execute endpoint.
type Handler struct {
	opts HandlerOpts
}

// NewHandler validates opts.
func NewHandler(opts HandlerOpts) (*Handler, error) {
	if opts.SelfID == "" {
		return nil, errors.New("toolexec: SelfID required")
	}
	if opts.KeyProvider == nil {
		return nil, errors.New("toolexec: KeyProvider required")
	}
	if opts.Executor == nil {
		return nil, errors.New("toolexec: Executor required")
	}
	if opts.Replay == nil {
		opts.Replay = NewReplayCache(0, 0)
	}
	if opts.Audit == nil {
		return nil, errors.New("toolexec: Audit required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.MaxLifetime <= 0 {
		opts.MaxLifetime = DefaultMaxTokenLifetime
	}
	return &Handler{opts: opts}, nil
}

// Register wires /mcp/execute onto a transport.Server.
func (h *Handler) Register(srv *transport.Server) {
	srv.Handle("POST /mcp/execute", h.handleExecute)
}

// ExecuteRequest is the wire body.
type ExecuteRequest struct {
	Token Token           `json:"token"`
	Args  json.RawMessage `json:"args"` // preserved as bytes; hashed verbatim
}

// ExecuteResponse is what cland returns on success.
type ExecuteResponse struct {
	Server string      `json:"server"`
	Tool   string      `json:"tool"`
	Result *ExecResult `json:"result"`
}

func (h *Handler) handleExecute(w http.ResponseWriter, r *http.Request) {
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.deny(w, http.StatusBadRequest, "decode", &req.Token, err)
		return
	}

	// 1. args bytes must match the hash the origin signed. Catches anyone
	//    tampering with args after token signing.
	gotHash := HashArgs(req.Args)
	if gotHash != req.Token.ArgsHash {
		h.deny(w, http.StatusUnauthorized, "args_hash_mismatch", &req.Token,
			ErrArgsHashMismatch)
		return
	}

	// 2. HMAC sig must verify under current or grace key (rotation tolerant).
	keys := []byte{}
	current := h.opts.KeyProvider.Current()
	graceKey, hasGrace := h.opts.KeyProvider.Grace()
	verifyErr := req.Token.VerifyHMAC(current, graceKey)
	_ = keys
	_ = hasGrace
	if verifyErr != nil {
		h.deny(w, http.StatusUnauthorized, "hmac_mismatch", &req.Token, verifyErr)
		return
	}

	// 3. temporal + target claims.
	now := time.Now()
	if err := req.Token.VerifyClaims(h.opts.SelfID, now, h.opts.MaxLifetime); err != nil {
		var status int
		var reason string
		switch {
		case errors.Is(err, ErrWrongTarget):
			status, reason = http.StatusForbidden, "wrong_target"
		case errors.Is(err, ErrExpired):
			status, reason = http.StatusUnauthorized, "expired"
		case errors.Is(err, ErrApprovedInFuture):
			status, reason = http.StatusUnauthorized, "approved_in_future"
		default:
			status, reason = http.StatusUnauthorized, "malformed_token"
		}
		h.deny(w, status, reason, &req.Token, err)
		return
	}

	// 4. Replay protection — must be AFTER all other checks pass so we
	//    don't pollute the cache with invalid tokens (which would let an
	//    attacker exhaust the cache cheaply).
	if !h.opts.Replay.CheckAndStore(req.Token.RequestID, now) {
		h.deny(w, http.StatusUnauthorized, "replay", &req.Token, ErrReplay)
		return
	}

	// 5. All token checks passed. Parse args (raw → map) for the executor.
	var argsMap map[string]any
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &argsMap); err != nil {
			h.deny(w, http.StatusBadRequest, "args_not_object", &req.Token, err)
			return
		}
	}

	result, server, tool, err := h.opts.Executor.Execute(r.Context(), req.Token.Tool, argsMap)
	if err != nil {
		// Spawn / list / call errors: 502 (we accepted the token but the
		// downstream subprocess broke). Distinct from policy-deny (which is
		// signalled by result.IsError=true on the MCP layer).
		h.opts.Log.Warn("toolexec: executor failed",
			"tool", req.Token.Tool, "err", err)
		h.audit(&req.Token, server, tool, "execute_failed", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":  "executor failed",
			"detail": err.Error(),
		})
		return
	}

	// 6. Success path. Audit + return.
	h.audit(&req.Token, server, tool, "ok", "")
	writeJSON(w, http.StatusOK, ExecuteResponse{
		Server: server,
		Tool:   tool,
		Result: result,
	})
}

// deny is the unified rejection path: HTTP error + structured audit entry.
// Status carries the 4xx the spec mandates per failure mode.
func (h *Handler) deny(w http.ResponseWriter, status int, reason string, t *Token, err error) {
	tool := ""
	requestID := ""
	origin := ""
	if t != nil {
		tool = t.Tool
		requestID = t.RequestID
		origin = t.OriginMember
	}
	h.opts.Log.Warn("toolexec: deny", "reason", reason, "tool", tool, "request_id", requestID, "err", err)
	_ = h.opts.Audit.Write(auditlog.Event{
		MemberID: h.opts.SelfID,
		Server:   "minti-cland",
		Tool:     "toolexec.execute",
		Decision: "deny",
		Reason:   reason,
		Args: map[string]any{
			"tool":          tool,
			"request_id":    requestID,
			"origin_member": origin,
		},
		Error: errString(err),
	})
	body := map[string]string{"error": reason}
	if err != nil {
		body["detail"] = err.Error()
	}
	writeJSON(w, status, body)
}

// audit records a successful execution (or executor-side failure).
func (h *Handler) audit(t *Token, server, tool, outcome, errMsg string) {
	decision := "allow"
	if outcome != "ok" {
		decision = "deny"
	}
	_ = h.opts.Audit.Write(auditlog.Event{
		MemberID: h.opts.SelfID,
		Server:   server,
		Tool:     tool,
		Decision: decision,
		Reason:   outcome,
		Args: map[string]any{
			"request_id":    t.RequestID,
			"origin_member": t.OriginMember,
		},
		Error: errMsg,
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
