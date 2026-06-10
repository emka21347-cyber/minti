// Package server wires the Clan Workspace HTTP surface: the embedded SPA plus
// the JSON API the frontend reads. Phase A exposes a health check and a /api/mesh
// snapshot; SSE (/api/events) and PIN/bearer auth land in the next increments.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/minti/workspace/internal/clan"
)

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
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		writeJSON(w, clan.Probe(ctx))
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
