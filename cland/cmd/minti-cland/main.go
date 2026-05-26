// minti-cland — the MINTI Clan daemon.
//
// Phase A (this file): loads config, loads-or-creates identity, opens state
// + audit log, idles waiting for SIGINT/SIGTERM. Reports a one-line readiness
// message. No HTTPS listener, mDNS, or election yet — those land in Phases
// B–E.
//
// The plan is at C:\Users\aouad\.claude\plans\velvet-drifting-codd.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/config"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/state"
)

// version is overridden via -ldflags at build time.
var version = "0.1.0-M4-A"

func main() {
	cfgPath := flag.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := flag.String("state", "", "state directory (overrides config)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*cfgPath, *stateDirFlag, log); err != nil {
		log.Error("cland fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath, stateDirOverride string, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if stateDirOverride != "" {
		cfg.State.Dir = stateDirOverride
	}
	log.Info("config loaded",
		"path", cfgPath,
		"listen", fmt.Sprintf("%s:%d", cfg.Listen.Address, cfg.Listen.Port),
		"state_dir", cfg.State.Dir,
		"mdns_enabled", cfg.Discovery.MDNSEnabled,
	)

	id, err := identity.LoadOrCreate(cfg.State.Dir)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	log.Info("identity ready",
		"member_id", id.MemberID,
		"pub_key_first8", id.PubKey[:8],
	)

	store, err := state.NewStore(cfg.State.Dir)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	clan, err := store.LoadClan()
	if err != nil {
		return fmt.Errorf("state.LoadClan: %w", err)
	}
	if clan.IsActive() {
		log.Info("clan state loaded",
			"clan_id", clan.ClanID,
			"role", clan.Role,
			"roster_size", len(clan.Roster),
		)
	} else {
		log.Info("clan state: unaffiliated", "hint", "run `minti-cland create` or `minti-cland join <token>`")
	}

	auditPath, err := auditlog.DefaultPath()
	if err != nil {
		return fmt.Errorf("auditlog: %w", err)
	}
	audit, err := auditlog.NewFileLogger(auditPath)
	if err != nil {
		return fmt.Errorf("auditlog: %w", err)
	}
	log.Info("audit log ready", "path", auditPath)
	_ = audit // wired into transport/election/toolexec in Phase B+

	log.Info("minti-cland started — Phase A skeleton", "version", version)
	log.Warn("Phase A is identity+state only — HTTPS, mDNS, election, routing arrive in B–F")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("shutdown signal received; exiting cleanly")
	return nil
}
