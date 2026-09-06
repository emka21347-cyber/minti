// Command minti-workspace serves the MINTI Clan Workspace — the human-facing
// web front door to a Clan. Single static binary, embeds its own SPA.
//
// Phase A: serves the deck + GET /api/mesh (live cland, or demo on a dev box).
// Default bind is loopback; LAN exposure waits until PIN/bearer auth lands so
// the mutating API is never opened without a gate.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/minti/workspace/internal/server"
	"github.com/minti/workspace/web"
)

var version = "0.1.0-M-workspace-A"

func main() {
	listen := flag.String("listen", "127.0.0.1:8088", "address:port to serve on (loopback until auth lands)")
	flag.Parse()

	webFS, err := fs.Sub(web.FS, ".")
	if err != nil {
		log.Fatalf("minti-workspace: embed: %v", err)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.New(webFS).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("minti-workspace %s — http://%s", version, *listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("minti-workspace: serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("minti-workspace: shutting down…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
