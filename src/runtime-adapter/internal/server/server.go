// Package server hosts minti-runtime's HTTP surface. Two API shapes are
// supported simultaneously:
//   - OpenAI-compatible: /v1/chat/completions, /v1/models
//   - Ollama-compatible: /api/chat, /api/tags  (thin pass-through)
// Plus our own introspection: /minti/capabilities, /minti/health.
//
// Agent clients pick whichever shape they prefer; both route through the
// same backend.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/minti/runtime-adapter/internal/backend"
)

// Server holds shared state for HTTP handlers.
type Server struct {
	Backend backend.Backend
	Log     *slog.Logger
}

// New constructs a Server with the given backend.
func New(b backend.Backend, log *slog.Logger) *Server {
	return &Server{Backend: b, Log: log}
}

// defaultModelPreference is the ordered list of models we prefer when the
// caller omits the `model` field. Hermes 3 8B is the agent-tuned default
// (lands via minti-pack-hermes3); Mistral 7B is the fallback (minti-pack-
// mistral). If neither is pulled we fall through to the first model the
// backend reports.
var defaultModelPreference = []string{
	"hermes3:8b",
	"mistral:7b",
}

// resolveModel returns the model the caller asked for, or — if they didn't —
// the first preferred default that the backend has resident. If no preferred
// default matches, returns the first available model. Returns ("", nil) only
// if the backend has zero models; handlers must error in that case.
//
// We deliberately probe Capabilities every call. The cost is one Ollama
// /api/tags fetch per inbound request with an empty model — Ollama serves
// that in <5 ms. A cache would couple us to "did the user pull a model
// since the runtime started" — staleness here would silently route to a
// pulled-but-removed model. Better to ask each time.
func (s *Server) resolveModel(ctx context.Context, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	caps, err := s.Backend.Capabilities(ctx)
	if err != nil {
		return "", fmt.Errorf("model not specified and could not list backend models: %w", err)
	}
	if len(caps.Models) == 0 {
		return "", nil
	}
	available := make(map[string]bool, len(caps.Models))
	for _, m := range caps.Models {
		available[m.Name] = true
	}
	for _, pref := range defaultModelPreference {
		if available[pref] {
			return pref, nil
		}
	}
	return caps.Models[0].Name, nil
}

// Routes returns a mux with all endpoints wired.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// OpenAI compatibility
	mux.HandleFunc("POST /v1/chat/completions", s.handleOpenAIChat)
	mux.HandleFunc("GET /v1/models", s.handleOpenAIModels)

	// Anthropic compatibility (M3 — for Claude Code and other Anthropic-shaped
	// clients to drive local models). Tool-use content blocks are M3.5+;
	// text-only chat is supported now.
	mux.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)

	// Ollama compatibility (pass-through translation through our backend)
	mux.HandleFunc("POST /api/chat", s.handleOllamaChat)
	mux.HandleFunc("GET /api/tags", s.handleOllamaTags)

	// MINTI native
	mux.HandleFunc("GET /minti/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /minti/health", s.handleHealth)
	mux.HandleFunc("GET /minti/version", s.handleVersion)

	return mux
}

// jsonResponse writes status + body as JSON.
func jsonResponse(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

// httpError writes an OpenAI-shaped error envelope.
func httpError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    http.StatusText(status),
		},
	})
}

// withTimeout returns a derived ctx that's still bounded by a hard ceiling
// even if upstream timeouts misbehave. 30 min matches Ollama's longest sane
// non-streaming call; streaming uses the request's own deadline.
func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d)
}

// handleHealth — quick liveness probe; also confirms the underlying backend
// can be reached. Returns 503 if backend is down so monitoring tools see it.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Backend.Health(ctx); err != nil {
		s.Log.Warn("backend unhealthy", "err", err)
		httpError(w, http.StatusServiceUnavailable, fmt.Sprintf("backend unhealthy: %v", err))
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVersion — static build-info endpoint. Filled in at build time via -ldflags
// in a later iteration; for now returns a stable placeholder.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]string{
		"runtime": "minti-runtime",
		"version": "0.1.0-M3",
	})
}

// handleCapabilities — describes which backend is wired and what models it knows.
// cland will eventually call this to compose its capability advertisement.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context(), 5*time.Second)
	defer cancel()
	caps, err := s.Backend.Capabilities(ctx)
	if err != nil {
		s.Log.Warn("capabilities probe failed", "err", err)
		httpError(w, http.StatusServiceUnavailable, fmt.Sprintf("capabilities probe failed: %v", err))
		return
	}
	jsonResponse(w, http.StatusOK, caps)
}

// handleOllamaTags — proxies an Ollama-style /api/tags listing using
// whatever models the backend says are resident.
func (s *Server) handleOllamaTags(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context(), 5*time.Second)
	defer cancel()
	caps, err := s.Backend.Capabilities(ctx)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	type tag struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	out := struct {
		Models []tag `json:"models"`
	}{}
	for _, m := range caps.Models {
		out.Models = append(out.Models, tag{Name: m.Name, Size: m.SizeBytes})
	}
	jsonResponse(w, http.StatusOK, out)
}

// handleOpenAIModels — OpenAI-shaped /v1/models. Same source as ollama tags.
func (s *Server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context(), 5*time.Second)
	defer cancel()
	caps, err := s.Backend.Capabilities(ctx)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	type entry struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	out := struct {
		Object string  `json:"object"`
		Data   []entry `json:"data"`
	}{Object: "list"}
	now := time.Now().Unix()
	for _, m := range caps.Models {
		out.Data = append(out.Data, entry{ID: m.Name, Object: "model", Created: now, OwnedBy: string(caps.Kind)})
	}
	jsonResponse(w, http.StatusOK, out)
}
