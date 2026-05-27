package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/crypto"
)

const (
	// TimestampSkewMax matches docs/clan-protocol.md §2.3: receivers accept
	// timestamps within ±60s of local clock.
	TimestampSkewMax = 60 * time.Second
	// NonceTTL matches the spec's 5-minute replay-protection window.
	NonceTTL = 5 * time.Minute
)

// ServerOpts is the dependency bundle handed to NewServer.
type ServerOpts struct {
	ListenAddr  string
	Cert        *crypto.ClanCert
	PrivateKey  ed25519.PrivateKey
	KeyProvider crypto.KeyProvider
	NonceCache  *NonceCache
	Audit       auditlog.Logger
	Log         *slog.Logger
}

// Server is cland's HTTPS endpoint. It exposes a mux for downstream phases
// (membership, election, routing, toolexec) to register handlers on; every
// handler runs behind the HMAC auth middleware.
type Server struct {
	opts ServerOpts
	mux  *http.ServeMux
	srv  *http.Server
}

// NewServer wires the TLS config + mux. Routes are added via Handle().
func NewServer(opts ServerOpts) (*Server, error) {
	if opts.Cert == nil {
		return nil, errors.New("transport: ServerOpts.Cert required")
	}
	if len(opts.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("transport: ServerOpts.PrivateKey must be Ed25519 (64 bytes)")
	}
	if opts.KeyProvider == nil {
		return nil, errors.New("transport: ServerOpts.KeyProvider required")
	}
	if opts.NonceCache == nil {
		opts.NonceCache = NewNonceCache(0, 0)
	}
	if opts.Audit == nil {
		return nil, errors.New("transport: ServerOpts.Audit required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{opts.Cert.CertDER},
		PrivateKey:  opts.PrivateKey,
		Leaf:        opts.Cert.X509(),
	}

	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              opts.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	return &Server{opts: opts, mux: mux, srv: srv}, nil
}

// Handle registers an authenticated handler. The mux pattern uses Go 1.22+
// method-prefixed routes (e.g., "POST /clan/heartbeat"). Handlers run only
// after auth middleware accepts the request; on rejection the handler is
// NOT called.
func (s *Server) Handle(pattern string, handler http.HandlerFunc) {
	s.mux.Handle(pattern, s.authMiddleware(handler))
}

// HandleAnonymous registers a handler that bypasses HMAC auth. Reserved for
// the public join handshake (added in Phase C) and similar pre-key flows.
func (s *Server) HandleAnonymous(pattern string, handler http.HandlerFunc) {
	s.mux.Handle(pattern, handler)
}

// Start begins serving TLS. Blocks until Shutdown or fatal error.
func (s *Server) Start() error {
	s.opts.Log.Info("cland transport: listening", "addr", s.opts.ListenAddr, "pin", s.opts.Cert.Pin)
	err := s.srv.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the server gracefully with a context-bounded deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// authMiddleware enforces docs/clan-protocol.md §2.3 — HMAC over the
// canonical request with replay protection. The body is buffered in memory
// because we need it both for HMAC computation and for the downstream
// handler.
func (s *Server) authMiddleware(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hard size cap so a malicious peer can't OOM us during HMAC verify.
		// 8 MiB matches the largest realistic Clan-internal payload (an
		// advertisement plus a tool execution result).
		const maxBody = 8 << 20
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			s.reject(w, r, "body_read", "request body exceeded limit or read error: "+err.Error())
			return
		}
		_ = r.Body.Close()
		// Restore body for the handler.
		r.Body = io.NopCloser(bytes.NewReader(body))

		memberID := r.Header.Get(crypto.HeaderMember)
		tsStr := r.Header.Get(crypto.HeaderTimestamp)
		nonce := r.Header.Get(crypto.HeaderNonce)
		mac := r.Header.Get(crypto.HeaderHMAC)

		if memberID == "" || tsStr == "" || nonce == "" || mac == "" {
			s.reject(w, r, "missing_headers", fmt.Sprintf(
				"missing one of %s/%s/%s/%s",
				crypto.HeaderMember, crypto.HeaderTimestamp,
				crypto.HeaderNonce, crypto.HeaderHMAC,
			))
			return
		}

		tsMillis, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			s.reject(w, r, "bad_timestamp", "X-Minti-Timestamp not an int64")
			return
		}
		ts := time.UnixMilli(tsMillis)
		if skew := time.Since(ts); skew > TimestampSkewMax || skew < -TimestampSkewMax {
			s.reject(w, r, "timestamp_skew", fmt.Sprintf("ts skew %v exceeds ±%v", skew, TimestampSkewMax))
			return
		}

		// Try current key first; fall back to grace key during rotation.
		current := s.opts.KeyProvider.Current()
		ok := crypto.VerifyMAC(current, r.Method, r.URL.Path, body, tsMillis, nonce, mac)
		if !ok {
			if grace, has := s.opts.KeyProvider.Grace(); has {
				ok = crypto.VerifyMAC(grace, r.Method, r.URL.Path, body, tsMillis, nonce, mac)
			}
		}
		if !ok {
			s.reject(w, r, "hmac_mismatch", "HMAC does not match (tried current + grace)")
			return
		}

		// Replay protection.
		if !s.opts.NonceCache.CheckAndStore(memberID, nonce) {
			s.reject(w, r, "replay", "(member_id, nonce) seen recently")
			return
		}

		// Set origin member in context for handlers that care.
		ctx := context.WithValue(r.Context(), originMemberKey{}, memberID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type originMemberKey struct{}

// OriginMember returns the caller's authenticated member_id, set by
// authMiddleware before the handler fires.
func OriginMember(ctx context.Context) string {
	v, _ := ctx.Value(originMemberKey{}).(string)
	return v
}

// reject writes a bare 401, audit-logs, and returns. Per spec §2.3 we
// deliberately do not leak failure reason to the wire — the local audit log
// has it.
func (s *Server) reject(w http.ResponseWriter, r *http.Request, reason, detail string) {
	w.WriteHeader(http.StatusUnauthorized)
	s.opts.Log.Warn("cland auth rejected", "reason", reason, "detail", detail, "path", r.URL.Path, "remote", r.RemoteAddr)
	_ = s.opts.Audit.Write(auditlog.Event{
		Server:   "minti-cland",
		Tool:     "transport.auth",
		Decision: "deny",
		Reason:   reason,
		Args:     map[string]any{"path": r.URL.Path, "remote": r.RemoteAddr},
		Error:    detail,
	})
}
