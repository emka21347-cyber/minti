// Command minti-runtime is the per-node AI runtime adapter daemon.
// It listens on a localhost HTTP port and exposes OpenAI- and Ollama-
// compatible chat endpoints that route through a configured backend
// (Ollama by default; llamacpp-server, LocalAI, and remote-API backends
// land in later milestones).
//
// Usage:
//   minti-runtime [--config /etc/minti/runtime.yaml] [--addr 127.0.0.1] [--port 7780]
//
// Flags override the config file values when supplied.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/minti/runtime-adapter/internal/config"
	"github.com/minti/runtime-adapter/internal/server"
)

func main() {
	configPath := flag.String("config", "/etc/minti/runtime.yaml", "path to runtime.yaml")
	addrFlag := flag.String("addr", "", "bind address override (e.g. 127.0.0.1)")
	portFlag := flag.Int("port", 0, "bind port override (e.g. 7780)")
	logLevel := flag.String("log-level", "", "log level override (debug|info|warn|error)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(2)
	}
	if *addrFlag != "" {
		cfg.Listen.Address = *addrFlag
	}
	if *portFlag != 0 {
		cfg.Listen.Port = *portFlag
	}
	if *logLevel != "" {
		cfg.Telemetry.LogLevel = *logLevel
	}

	log := newLogger(cfg.Telemetry.LogLevel)

	b, err := config.NewBackend(cfg.Backend)
	if err != nil {
		log.Error("construct backend", "err", err)
		os.Exit(2)
	}
	log.Info("backend constructed", "kind", b.Kind(), "base_url", cfg.Backend.BaseURL)

	// Probe backend health at startup but don't refuse to run if it's down —
	// Ollama may come up after us, and our /minti/health endpoint will
	// surface the situation honestly.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := b.Health(probeCtx); err != nil {
		log.Warn("backend probe failed at startup (continuing anyway)", "err", err)
	} else {
		log.Info("backend healthy")
	}
	probeCancel()

	srv := server.New(b, log)
	listenAddr := net.JoinHostPort(cfg.Listen.Address, strconv.Itoa(cfg.Listen.Port))
	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("minti-runtime listening", "addr", listenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			log.Error("http server error", "err", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("graceful shutdown failed", "err", err)
	}
	log.Info("stopped")
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	case "", "info":
		l = slog.LevelInfo
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
