// Package server wires the Clan Workspace HTTP surface: the embedded SPA plus
// the JSON API the frontend reads. Phase A exposes a health check and a /api/mesh
// snapshot; Memory M5 adds the spec §13 memory surface. SSE (/api/events) and
// PIN/bearer auth land in the next increments.
//
// SECURITY (spec §13.6 workspace caveat): every MUTATING route below ships
// loopback-only (main.go binds 127.0.0.1) and MUST be enumerated in the
// PIN/bearer middleware when it lands:
//
//	POST /api/join             POST /api/chat
//	POST /api/invite           POST /api/knock/accept   POST /api/knock/deny
//	POST /api/cookbook/install
//	POST /api/memory/node      POST /api/memory/edge
//	POST /api/memory/archive   POST /api/memory/import
//
// /api/join changes Clan membership and /api/invite mints join tokens — both
// are especially sensitive. GET /api/memory/blueprint is read-only but EXPORTS
// distilled chat content; gate it with the same auth when LAN exposure arrives.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/minti/workspace/internal/clan"
)

// maxImportBytes bounds a posted blueprint (cland's own fetch guard is 4 MiB).
const maxImportBytes = 4<<20 + 1

// Server holds the composed HTTP handler.
type Server struct {
	handler http.Handler
}

// New builds the workspace handler over the embedded web filesystem.
func New(webFS fs.FS) *Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /api/mesh", func(w http.ResponseWriter, r *http.Request) {
		// Probe shells show + orchestrator + peers (3 local CLI calls); 3s budget.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		writeJSON(w, clan.Probe(ctx))
	})

	// POST /api/join — paste a connection token (or token+address+pin) to
	// join the Clan. Shells `minti-cland join`; the idle daemon then restarts
	// itself into active mode and the SPA polls /api/mesh until live.
	mux.HandleFunc("POST /api/join", func(w http.ResponseWriter, r *http.Request) {
		var req clan.JoinRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := clan.Join(ctx, req); err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// POST /api/chat — STREAMING. Relays `minti-cland chat` stdout to the
	// browser token-by-token. No response timeout (chat can run minutes); the
	// request context cancels the shelled CLI on client disconnect.
	mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Message string `json:"message"`
			Model   string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
			httpError(w, http.StatusBadRequest, errors.New("message required"))
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			httpError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		if err := clan.ChatStream(r.Context(), req.Message, req.Model, w, flusher.Flush); err != nil {
			// 200 + headers already sent — surface the error inside the stream
			// so the SPA can show it in the chat bubble.
			_, _ = io.WriteString(w, "\n[chat error: "+err.Error()+"]")
			flusher.Flush()
		}
	})

	// GET /api/peers — live registry (members + candidates), relayed verbatim.
	mux.HandleFunc("GET /api/peers", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		raw, err := clan.Peers(ctx)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	// POST /api/invite — mint a single-use connection token (founder hands it
	// to a tester). Body: {ttl_seconds}. Returns the CLI's --json incl. connect.
	mux.HandleFunc("POST /api/invite", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TTLSeconds int `json:"ttl_seconds"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		raw, err := clan.Invite(ctx, req.TTLSeconds)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	// GET /api/knocks — pending knock requests awaiting approval.
	mux.HandleFunc("GET /api/knocks", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		raw, err := clan.Knocks(ctx)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	mux.HandleFunc("POST /api/knock/accept", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if err := clan.KnockAccept(ctx, req.ID); err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/knock/deny", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID, Reason string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if err := clan.KnockDeny(ctx, req.ID, req.Reason); err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	// GET /api/cookbook — static model-pack manifest (v0.5: read-only).
	mux.HandleFunc("GET /api/cookbook", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"packs": clan.CookbookPacks()})
	})

	// POST /api/cookbook/install — STREAMING. Pulls the model onto this node via
	// Ollama and relays progress ("pulling … 45%") token-by-token to the browser.
	mux.HandleFunc("POST /api/cookbook/install", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			httpError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		if err := clan.CookbookInstallStream(r.Context(), req.Name, w, flusher.Flush); err != nil {
			_, _ = io.WriteString(w, "\n[install error: "+err.Error()+"]")
			flusher.Flush()
		}
	})

	// ----- Memory M5: the spec §13 graph surface -----

	mux.HandleFunc("GET /api/memory", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		writeJSON(w, clan.ProbeMemory(ctx))
	})

	mux.HandleFunc("GET /api/memory/digest", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		writeJSON(w, clan.ProbeMemoryDigest(ctx))
	})

	mux.HandleFunc("GET /api/scribe", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		writeJSON(w, clan.ProbeScribe(ctx))
	})

	mux.HandleFunc("POST /api/memory/node", func(w http.ResponseWriter, r *http.Request) {
		var n clan.MemoryNode
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		raw, err := clan.MemoryAddNode(ctx, n)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	mux.HandleFunc("POST /api/memory/edge", func(w http.ResponseWriter, r *http.Request) {
		var e struct {
			From, To, Relation string
		}
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		raw, err := clan.MemoryLink(ctx, e.From, e.To, e.Relation)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	mux.HandleFunc("POST /api/memory/archive", func(w http.ResponseWriter, r *http.Request) {
		var a struct{ ID string }
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil || a.ID == "" {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		raw, err := clan.MemoryArchive(ctx, a.ID)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	mux.HandleFunc("GET /api/memory/blueprint", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		data, err := clan.MemoryExportBlueprint(ctx,
			r.URL.Query().Get("session"),
			r.URL.Query().Get("strip") == "1")
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition",
			`attachment; filename="minti-blueprint-`+time.Now().Format("2006-01-02")+`.json"`)
		_, _ = w.Write(data)
	})

	mux.HandleFunc("POST /api/memory/import", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxImportBytes))
		if err != nil || len(body) == 0 || len(body) >= maxImportBytes {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		raw, err := clan.MemoryImportBlueprint(ctx, body)
		if err != nil {
			httpError(w, http.StatusBadGateway, err)
			return
		}
		writeRaw(w, raw)
	})

	// Everything else: the embedded SPA. http.FileServer serves index.html at /.
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	return &Server{handler: mux}
}

// Handler returns the composed http.Handler.
func (s *Server) Handler() http.Handler { return s.handler }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeRaw relays a CLI --json payload verbatim.
func writeRaw(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func httpError(w http.ResponseWriter, status int, err error) {
	msg := "bad request"
	if err != nil {
		msg = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
