// minti-cland — the MINTI Clan daemon + CLI.
//
// Subcommand dispatch:
//
//	minti-cland                          — daemon mode (current default)
//	minti-cland create [--address X:7777] — found a new Clan, print paste-key
//	minti-cland members                  — print persisted roster
//	minti-cland show                     — print clan_id + pin + address
//
// More subcommands (invite/join/leave/revoke/pin) land in Phase C
// continuation + Phase E. The plan is at
// C:\Users\aouad\.claude\plans\velvet-drifting-codd.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/config"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/membership"
	"github.com/minti/cland/internal/state"
)

// version is overridden via -ldflags at build time.
var version = "0.1.0-M4-C"

func main() {
	if len(os.Args) > 1 && !looksLikeFlag(os.Args[1]) {
		switch os.Args[1] {
		case "create":
			if err := cmdCreate(os.Args[2:]); err != nil {
				exitErr("create", err)
			}
			return
		case "members":
			if err := cmdMembers(os.Args[2:]); err != nil {
				exitErr("members", err)
			}
			return
		case "show":
			if err := cmdShow(os.Args[2:]); err != nil {
				exitErr("show", err)
			}
			return
		case "help", "-h", "--help":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "minti-cland: unknown subcommand %q\n\n", os.Args[1])
			printUsage()
			os.Exit(2)
		}
	}
	if err := runDaemon(os.Args[1:]); err != nil {
		exitErr("daemon", err)
	}
}

func looksLikeFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func exitErr(cmd string, err error) {
	fmt.Fprintf(os.Stderr, "minti-cland %s: %v\n", cmd, err)
	os.Exit(1)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `minti-cland %s

Usage:
  minti-cland                    daemon mode (default)
  minti-cland create [flags]     found a new Clan; prints paste-key
  minti-cland members            print persisted Clan roster
  minti-cland show               print clan_id, cert pin, LAN address
  minti-cland help               this message

Daemon flags:
  --config PATH    config file (default /etc/minti/cland.yaml)
  --state DIR      state directory (overrides config)

`, version)
}

// ---------- daemon ----------

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	_ = fs.Parse(args)

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *stateDirFlag != "" {
		cfg.State.Dir = *stateDirFlag
	}
	log.Info("config loaded",
		"path", *cfgPath,
		"listen", fmt.Sprintf("%s:%d", cfg.Listen.Address, cfg.Listen.Port),
		"state_dir", cfg.State.Dir,
	)

	id, err := identity.LoadOrCreate(cfg.State.Dir)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	log.Info("identity ready", "member_id", id.MemberID)

	store, err := state.NewStore(cfg.State.Dir)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if clan.IsActive() {
		log.Info("clan loaded", "clan_id", clan.ClanID, "role", clan.Role, "roster_size", len(clan.Roster))
	} else {
		log.Info("clan state: unaffiliated", "hint", "run `minti-cland create` or join an existing Clan")
	}

	apath, err := auditlog.DefaultPath()
	if err != nil {
		return err
	}
	if _, err := auditlog.NewFileLogger(apath); err != nil {
		return err
	}
	log.Info("audit log ready", "path", apath)
	log.Info("minti-cland started — Phase A+B+C skeleton", "version", version)
	log.Warn("Phase D–F (discovery, election, routing) not yet wired — daemon idles")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	log.Info("shutdown signal received")
	return nil
}

// ---------- create ----------

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	addressFlag := fs.String("address", "", "LAN address joiners should reach us at (default: auto-detected from listen + first non-loopback IPv4)")
	jsonOut := fs.Bool("json", false, "output the paste-key as a single JSON line")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *stateDirFlag != "" {
		cfg.State.Dir = *stateDirFlag
	}
	id, err := identity.LoadOrCreate(cfg.State.Dir)
	if err != nil {
		return err
	}
	store, err := state.NewStore(cfg.State.Dir)
	if err != nil {
		return err
	}
	existing, err := store.LoadClan()
	if err != nil {
		return err
	}
	if existing.IsActive() {
		return fmt.Errorf("this member is already in Clan %s (role=%s) — use `leave` first if you want to start over",
			existing.ClanID, existing.Role)
	}

	addr := *addressFlag
	if addr == "" {
		resolved, err := resolveLANAddr(cfg)
		if err != nil {
			return err
		}
		addr = resolved
	}

	pk, err := membership.Create(store, id, addr)
	if err != nil {
		return err
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(pk)
	}

	fmt.Println("Clan founded.")
	fmt.Println()
	fmt.Println("Share this paste-key with new members over a trusted channel")
	fmt.Println("(QR code, voice call, secure messenger — anyone who sees it can join):")
	fmt.Println()
	fmt.Printf("  Clan ID:  %s\n", pk.ClanID)
	fmt.Printf("  Address:  %s\n", pk.Address)
	fmt.Printf("  Pin:      %s\n", pk.Pin)
	fmt.Println()
	fmt.Println("  Mnemonic (12 BIP39 words):")
	fmt.Printf("    %s\n", pk.Mnemonic)
	fmt.Println()
	fmt.Println("Joiners run:")
	fmt.Printf("  minti-cland join --mnemonic %q --address %s --pin %s\n", pk.Mnemonic, pk.Address, pk.Pin)
	return nil
}

// ---------- members ----------

func cmdMembers(args []string) error {
	fs := flag.NewFlagSet("members", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *stateDirFlag != "" {
		cfg.State.Dir = *stateDirFlag
	}
	store, err := state.NewStore(cfg.State.Dir)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated — no Clan to show. Run `create` or join an existing one")
	}
	fmt.Printf("Clan %s (%d members)\n", clan.ClanID, len(clan.Roster))
	for _, m := range clan.Roster {
		fmt.Printf("  %s  %-9s  admitted=%s  last_seen=%s\n",
			m.MemberID, m.State, m.AdmittedAt.Format("2006-01-02T15:04:05Z"), m.LastSeenAt.Format("2006-01-02T15:04:05Z"))
	}
	return nil
}

// ---------- show ----------

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *stateDirFlag != "" {
		cfg.State.Dir = *stateDirFlag
	}
	id, err := identity.LoadOrCreate(cfg.State.Dir)
	if err != nil {
		return err
	}
	store, err := state.NewStore(cfg.State.Dir)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	fmt.Printf("Member ID:  %s\n", id.MemberID)
	if !clan.IsActive() {
		fmt.Println("Clan:       (unaffiliated)")
		return nil
	}
	fmt.Printf("Clan ID:    %s\n", clan.ClanID)
	fmt.Printf("Role:       %s\n", clan.Role)
	fmt.Printf("Cert pin:   %s\n", clan.ClanCertPin)
	addr, err := resolveLANAddr(cfg)
	if err == nil {
		fmt.Printf("LAN addr:   %s\n", addr)
	}
	return nil
}

// resolveLANAddr returns "ip:port" — if cfg.Listen.Address is the wildcard
// (0.0.0.0 / ::) we probe the first non-loopback IPv4. Otherwise we use the
// configured address verbatim. Fails when there's no routable IPv4.
func resolveLANAddr(cfg config.Config) (string, error) {
	addr := cfg.Listen.Address
	if addr == "0.0.0.0" || addr == "" || addr == "::" {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", fmt.Errorf("resolve LAN addr: %w", err)
		}
		found := ""
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
				continue
			}
			found = ipnet.IP.String()
			break
		}
		if found == "" {
			return "", errors.New("no non-loopback IPv4 interface found; pass --address explicitly")
		}
		addr = found
	}
	return fmt.Sprintf("%s:%d", addr, cfg.Listen.Port), nil
}
