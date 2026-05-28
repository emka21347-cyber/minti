// minti-cland — the MINTI Clan daemon + CLI.
//
// Subcommand dispatch:
//
//	minti-cland                          — daemon mode (current default)
//	minti-cland create [--address X:7777] — found a new Clan, print paste-key
//	minti-cland invite [--ttl 1h]         — mint a single-use join token
//	minti-cland join   [paste-key | --token ...]
//	                                     — join an existing Clan
//	minti-cland leave                    — wipe local Clan state (CAREFUL)
//	minti-cland revoke <member_id> [--reason "..."]
//	                                     — kick a member from the Clan
//	minti-cland members                  — print persisted roster
//	minti-cland show                     — print clan_id + pin + LAN address
//
// More subcommands (pin, peer-add) land in later M4 phases.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/minti/cland/internal/advertise"
	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/config"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/discovery"
	"github.com/minti/cland/internal/election"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/keyrotate"
	"github.com/minti/cland/internal/membership"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/probe"
	"github.com/minti/cland/internal/revocations"
	"github.com/minti/cland/internal/rostersync"
	"github.com/minti/cland/internal/router"
	"github.com/minti/cland/internal/scores"
	"github.com/minti/cland/internal/state"
	"github.com/minti/cland/internal/toolexec"
	"github.com/minti/cland/internal/transport"
)

// version is overridden via -ldflags at build time.
var version = "0.1.0-M4-C2"

func main() {
	if len(os.Args) > 1 && !looksLikeFlag(os.Args[1]) {
		var err error
		switch os.Args[1] {
		case "create":
			err = cmdCreate(os.Args[2:])
		case "invite":
			err = cmdInvite(os.Args[2:])
		case "join":
			err = cmdJoin(os.Args[2:])
		case "leave":
			err = cmdLeave(os.Args[2:])
		case "revoke":
			err = cmdRevoke(os.Args[2:])
		case "members":
			err = cmdMembers(os.Args[2:])
		case "peer-add":
			err = cmdPeerAdd(os.Args[2:])
		case "peers":
			err = cmdPeers(os.Args[2:])
		case "pin":
			err = cmdPin(os.Args[2:])
		case "orchestrator":
			err = cmdOrchestrator(os.Args[2:])
		case "election-history":
			err = cmdElectionHistory(os.Args[2:])
		case "rotate-key":
			err = cmdRotateKey(os.Args[2:])
		case "show":
			err = cmdShow(os.Args[2:])
		case "help", "-h", "--help":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "minti-cland: unknown subcommand %q\n\n", os.Args[1])
			printUsage()
			os.Exit(2)
		}
		if err != nil {
			exitErr(os.Args[1], err)
		}
		return
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
  minti-cland                          daemon mode (default)
  minti-cland create [flags]           found a new Clan; prints paste-key
  minti-cland invite [--ttl 1h]        mint a single-use join token
  minti-cland join [flags]             join an existing Clan
       --mnemonic "..." --address ip:port --pin sha256:...
       OR  --token <base64> --address ip:port --pin sha256:...
  minti-cland leave                    wipe local Clan state
  minti-cland revoke <member_id> [--reason "..."]
                                       kick a member
  minti-cland members                  print persisted roster
  minti-cland peer-add <ip:port>       manually register a peer (mDNS fallback)
  minti-cland peers                    print live peer registry
  minti-cland pin [--self|--clear]     set or clear the self-pin (spec §5.6)
  minti-cland orchestrator             print current Orchestrator + term + lease
  minti-cland election-history         print recent elections (ring buffer)
  minti-cland rotate-key               rotate the Clan key (Orchestrator only)
  minti-cland show                     print clan_id, pin, LAN address
  minti-cland help                     this message

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

	apath, err := auditlog.DefaultPath()
	if err != nil {
		return err
	}
	audit, err := auditlog.NewFileLogger(apath)
	if err != nil {
		return err
	}
	log.Info("audit log ready", "path", apath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !clan.IsActive() {
		log.Info("clan state: unaffiliated — daemon idle", "hint", "run `minti-cland create` or `minti-cland join`")
		log.Info("minti-cland started", "version", version)
		<-ctx.Done()
		log.Info("shutdown signal received")
		return nil
	}

	// Clan is active → bring up the transport server + membership service.
	log.Info("clan loaded", "clan_id", clan.ClanID, "role", clan.Role, "roster_size", len(clan.Roster))

	cc, err := crypto.ParseClanCertPEM([]byte(clan.ClanCertPEM))
	if err != nil {
		return fmt.Errorf("parse persisted clan_cert: %w", err)
	}
	kp, err := crypto.NewSimpleKeyProvider(clan.ClanKey())
	if err != nil {
		return fmt.Errorf("keyprovider: %w", err)
	}

	// Use the Clan-shared priv (founder's cert priv, distributed via
	// /clan/welcome + /clan/join). Falls back to local identity priv for
	// founders whose state predates the v0.2 wire fix.
	tlsPriv := clan.ClanCertPrivKey()
	if len(tlsPriv) == 0 {
		log.Warn("clan state has no shared cert priv — using local identity priv. This works for founders but joiners need the shared priv from welcome/join (re-join required if you see this on a non-founder).")
		tlsPriv = id.PrivateKey()
	}
	srv, err := transport.NewServer(transport.ServerOpts{
		ListenAddr:  fmt.Sprintf("%s:%d", cfg.Listen.Address, cfg.Listen.Port),
		Cert:        cc,
		PrivateKey:  tlsPriv,
		KeyProvider: kp,
		NonceCache:  transport.NewNonceCache(0, 0),
		Audit:       audit,
		Log:         log,
	})
	if err != nil {
		return fmt.Errorf("transport: %w", err)
	}

	lanAddr, err := resolveLANAddr(cfg)
	if err != nil {
		// Not fatal — just means the LAN address advertised to invitees
		// will be the configured one (possibly 0.0.0.0).
		log.Warn("could not resolve LAN address; falling back", "err", err)
		lanAddr = fmt.Sprintf("%s:%d", cfg.Listen.Address, cfg.Listen.Port)
	}

	membSvc := membership.NewService(store, id, lanAddr, audit, log)
	membSvc.Register(srv)

	// Zombie sweep + invite sweep ticker.
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	defer sweepCancel()
	go membSvc.StartZombieSweep(sweepCtx, 60*time.Second)

	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Start() }()

	log.Info("minti-cland transport up",
		"version", version,
		"addr", fmt.Sprintf("%s:%d", cfg.Listen.Address, cfg.Listen.Port),
		"lan_addr", lanAddr,
		"pin", cc.Pin,
	)

	// ----- Phase D: discovery + advertise + peer registry -----
	rubric, err := scores.LoadRubric(cfg.Discovery.RubricPath)
	if err != nil {
		log.Warn("reasoning-scores rubric load failed; reasoning_score will be 0",
			"path", cfg.Discovery.RubricPath, "err", err)
		rubric = &scores.Rubric{}
	}
	registry := peers.NewRegistry()
	if revs, _ := store.LoadRevocations(); revs != nil {
		registry.SetRevocations(revs)
	}
	prober := probe.New()
	runtimeClient := probe.NewRuntimeClient(cfg.Runtime.BaseURL, 30*time.Second)
	recentFailures := scores.NewRecentFailures()

	advClient, err := transport.NewClient(transport.ClientOpts{
		MemberID:    id.MemberID,
		KeyProvider: kp,
		Pin:         cc.Pin,
		Timeout:     15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("advertise transport client: %w", err)
	}
	advSvc := &advertise.Service{
		ClanID:         clan.ClanID,
		MemberID:       id.MemberID,
		LANAddress:     lanAddr,
		Registry:       registry,
		Prober:         prober,
		RuntimeClient:  runtimeClient,
		Rubric:         rubric,
		RecentFailures: recentFailures,
		Client:         advClient,
		Log:            log,
		Interval:       cfg.Advertise.Interval,
		InitialWait:    cfg.Advertise.InitialDelay,
		BumpRate:       cfg.Advertise.BumpRate,
	}
	(&peers.Handlers{
		Registry: registry,
		Bump:     advSvc.Bump,
		Promote:  membSvc.PromoteToActive, // Phase H-3: §3.1 admitted→active on first ad
	}).Register(srv)

	discSvc := &discovery.Service{
		ClanID:    clan.ClanID,
		MemberID:  id.MemberID,
		Port:      cfg.Listen.Port,
		Interface: cfg.Discovery.Interface,
		Log:       log,
	}
	if cfg.Discovery.MDNSEnabled {
		if err := discSvc.Register(); err != nil {
			log.Warn("discovery.Register failed; mDNS disabled", "err", err)
		}
	} else {
		log.Info("discovery: mdns_enabled=false; use `minti-cland peer-add` for peer registration")
	}
	defer discSvc.Shutdown()

	discoCtx, discoCancel := context.WithCancel(context.Background())
	defer discoCancel()
	if cfg.Discovery.MDNSEnabled {
		go func() {
			err := discSvc.Browse(discoCtx, func(c discovery.Candidate) {
				log.Info("discovery: peer seen via mDNS", "address", c.Address)
				if err := registry.UpsertCandidate(c.Address, peers.SourceMDNS); err != nil {
					log.Warn("registry: upsert candidate", "address", c.Address, "err", err)
					return
				}
				advSvc.Bump()
			})
			if err != nil && discoCtx.Err() == nil {
				log.Warn("discovery browse exited", "err", err)
			}
		}()
	}

	advCtx, advCancel := context.WithCancel(context.Background())
	defer advCancel()
	go advSvc.Start(advCtx)
	log.Info("advertise + discovery loops running",
		"interval", cfg.Advertise.Interval, "rubric_path", cfg.Discovery.RubricPath)

	// ----- Phase E: leader-lease election -----
	electionState := election.NewState(id.MemberID, clan.CurrentTerm, clan.CurrentOrchestrator, cfg.Election.HistorySize)
	localSelfFn := func() election.LocalCandidate {
		curClan, err := store.LoadClan()
		if err != nil || curClan == nil {
			return election.LocalCandidate{MemberID: id.MemberID}
		}
		caps, _ := runtimeClient.Get(context.Background())
		resident := caps.ResidentModels()
		remote := caps.RemoteAPIs()
		score := scores.ReasoningScore(rubric, resident, remote)
		enabled := caps != nil && caps.Healthy && len(resident)+len(remote) > 0
		// Smoke-test escape hatch: same env var as healthFn (R1 bypass).
		// Forces reasoning-capable so the daemon can be elected even without
		// a live runtime-adapter alongside. Production leaves this unset.
		if !enabled && os.Getenv("MINTI_CLAND_FORCE_HEALTHY") == "1" {
			enabled = true
			if score == 0 {
				score = 50
			}
		}
		var admittedAt time.Time
		for _, m := range curClan.Roster {
			if m.MemberID == id.MemberID {
				admittedAt = m.AdmittedAt
				break
			}
		}
		return election.LocalCandidate{
			MemberID:         id.MemberID,
			ReasoningScore:   score,
			ReasoningEnabled: enabled,
			Pinned:           curClan.PinnedOrchestrator,
			AdmittedAt:       admittedAt,
		}
	}
	healthFn := func(now time.Time) bool {
		// Smoke-test escape hatch: lets the Phase E smoke script run cland
		// pairs on 127.0.0.1 without requiring a live minti-runtime alongside.
		// Production deployments leave this unset; R1 gate is then enforced.
		if os.Getenv("MINTI_CLAND_FORCE_HEALTHY") == "1" {
			return true
		}
		caps, err := runtimeClient.Get(context.Background())
		// R1: heartbeat only if the local runtime answered successfully + reports healthy.
		// On err the runtime client may return a stale cache; we still treat as unhealthy
		// to avoid the zombie-leader scenario (gemma E1).
		return err == nil && caps != nil && caps.Healthy
	}
	electionEng, err := election.NewEngine(election.EngineOpts{
		SelfID:            id.MemberID,
		ClanID:            clan.ClanID,
		State:             electionState,
		Store:             store,
		Registry:          registry,
		Client:            advClient, // reuse the same HMAC-stamping transport.Client
		Health:            healthFn,
		LocalSelf:         localSelfFn,
		Audit:             audit,
		Log:               log,
		OnSelfElected: func() {
			// Phase H-3: when self wins election, promote our own roster
			// entry to "active" (the advertise-receive path doesn't fire
			// for us since we only emit heartbeats, not receive them).
			_ = membSvc.PromoteToActive(id.MemberID)
		},
		HeartbeatInterval: cfg.Election.HeartbeatInterval,
		LeaseDuration:     cfg.Election.LeaseDuration,
		FailoverGrace:     cfg.Election.FailoverGrace,
		ElectionTimeout:   cfg.Election.ElectionTimeout,
	})
	if err != nil {
		return fmt.Errorf("election: %w", err)
	}
	// Phase H-2: revocations sync (heartbeat-driven gossip + GET endpoint).
	revSyncer, err := revocations.NewSyncer(revocations.SyncerOpts{
		SelfID:   id.MemberID,
		Store:    store,
		Registry: registry,
		Fetcher:  advClient, // HMAC-stamping transport.Client
		LookupAddr: func(memberID string) string {
			_, members := registry.Snapshot()
			for _, m := range members {
				if m.MemberID == memberID {
					return m.Address
				}
			}
			return ""
		},
		Audit: audit,
		Log:   log,
	})
	if err != nil {
		return fmt.Errorf("revocations syncer: %w", err)
	}
	(&revocations.Handler{Store: store, Log: log}).Register(srv)

	// Phase H-3: roster gossip (same pattern as H-2 revocations).
	rosterSyncer, err := rostersync.NewSyncer(rostersync.SyncerOpts{
		SelfID:   id.MemberID,
		Store:    store,
		Registry: registry,
		Fetcher:  advClient,
		LookupAddr: func(memberID string) string {
			_, members := registry.Snapshot()
			for _, m := range members {
				if m.MemberID == memberID {
					return m.Address
				}
			}
			return ""
		},
		Audit: audit,
		Log:   log,
	})
	if err != nil {
		return fmt.Errorf("rostersync: %w", err)
	}
	(&rostersync.Handler{Store: store, Log: log}).Register(srv)

	(&election.Handlers{
		Engine:          electionEng,
		Store:           store,
		Bump:            advSvc.Bump,
		RevocationsSync: revSyncer,
		RosterSync:      rosterSyncer,
	}).Register(srv)
	electionCtx, electionCancel := context.WithCancel(context.Background())
	defer electionCancel()
	go electionEng.Run(electionCtx)
	log.Info("election engine running",
		"heartbeat", cfg.Election.HeartbeatInterval,
		"lease", cfg.Election.LeaseDuration,
		"failover_grace", cfg.Election.FailoverGrace,
		"history_size", cfg.Election.HistorySize,
	)

	// ----- Phase F: routing layer -----
	routerSvc, err := router.NewRouter(router.Opts{
		SelfID:         id.MemberID,
		ClanID:         clan.ClanID,
		ElectionState:  electionState,
		Registry:       registry,
		RuntimeBaseURL: cfg.Runtime.BaseURL,
		PeerClient:     advClient, // reuse the HMAC-stamping transport.Client
		Audit:          audit,
		Log:            log,
	})
	if err != nil {
		return fmt.Errorf("router: %w", err)
	}
	routerSvc.Register(srv)
	log.Info("router enabled",
		"runtime_base", cfg.Runtime.BaseURL,
		"endpoints", "/v1/chat/completions /v1/messages /api/chat")

	// ----- Phase G: cross-Clan tool execution -----
	toolExecutor := &toolexec.Executor{
		BinariesDir: cfg.MCP.BinariesDir,
		ExecTimeout: cfg.MCP.ExecTimeout,
	}
	toolHandler, err := toolexec.NewHandler(toolexec.HandlerOpts{
		SelfID:      id.MemberID,
		KeyProvider: kp,
		Executor:    toolExecutor,
		Replay:      toolexec.NewReplayCache(0, 0),
		RateLimiter: toolexec.NewRateLimiter(0, 0), // defaults: 10 req per 60s per origin
		Audit:       audit,
		Log:         log,
		MaxLifetime: cfg.MCP.MaxTokenLifetime,
	})
	if err != nil {
		return fmt.Errorf("toolexec: %w", err)
	}
	toolHandler.Register(srv)
	log.Info("toolexec enabled",
		"binaries_dir", cfg.MCP.BinariesDir,
		"max_token_lifetime", cfg.MCP.MaxTokenLifetime,
		"endpoints", "/mcp/execute")

	// ----- Phase H-1: key rotation 2PC -----
	proposeStore := keyrotate.NewProposeStore(nil)
	(&keyrotate.MemberHandler{
		SelfID: id.MemberID, Store: proposeStore, Rotater: kp,
		Audit: audit, Log: log,
	}).Register(srv)
	// Sweep expired propose entries every PROPOSE_TIMEOUT — bounded background work.
	sweepDoneCtx, sweepDoneCancel := context.WithCancel(context.Background())
	defer sweepDoneCancel()
	go func() {
		t := time.NewTicker(keyrotate.ProposeTimeout)
		defer t.Stop()
		for {
			select {
			case <-sweepDoneCtx.Done():
				return
			case <-t.C:
				proposeStore.SweepExpired()
			}
		}
	}()

	rotateCoord, err := keyrotate.NewCoordinator(keyrotate.CoordinatorOpts{
		SelfID:  id.MemberID,
		Rotater: kp,
		Client:  advClient, // HMAC-stamping transport.Client
		PeerSource: func() []keyrotate.Peer {
			// Rotate against the persisted active roster: anyone currently
			// "active" + with a known address in peers.Registry.
			curClan, _ := store.LoadClan()
			if curClan == nil {
				return nil
			}
			activeIDs := map[string]bool{}
			for _, m := range curClan.Roster {
				if m.State == "active" && m.MemberID != id.MemberID {
					activeIDs[m.MemberID] = true
				}
			}
			_, members := registry.Snapshot()
			out := make([]keyrotate.Peer, 0, len(activeIDs))
			for _, m := range members {
				if activeIDs[m.MemberID] && m.Address != "" {
					out = append(out, keyrotate.Peer{MemberID: m.MemberID, Address: m.Address})
				}
			}
			return out
		},
		Audit: audit,
		Log:   log,
	})
	if err != nil {
		return fmt.Errorf("keyrotate coordinator: %w", err)
	}
	(&keyrotate.TriggerHandler{
		Coordinator: rotateCoord,
		IsOrchestrator: func() bool {
			return electionState.IAmOrchestrator(time.Now())
		},
	}).Register(srv)
	log.Info("keyrotate enabled",
		"propose_timeout", keyrotate.ProposeTimeout,
		"grace_duration", keyrotate.DefaultGraceDuration,
		"endpoints", "/clan/rotate-key /clan/rotate-key/{propose,commit,abort}")

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-srvErr:
		if err != nil {
			log.Error("transport error", "err", err)
			return err
		}
	}

	shCtx, shCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shCancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Warn("graceful shutdown failed", "err", err)
	}
	return nil
}

// ---------- create ----------

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	addressFlag := fs.String("address", "", "LAN address joiners should reach us at (default: auto-detected)")
	jsonOut := fs.Bool("json", false, "output the paste-key as a single JSON line")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	existing, err := store.LoadClan()
	if err != nil {
		return err
	}
	if existing.IsActive() {
		return fmt.Errorf("this member is already in Clan %s (role=%s) — use `leave` first", existing.ClanID, existing.Role)
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
		return json.NewEncoder(os.Stdout).Encode(pk)
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
	fmt.Printf("  minti-cland join --mnemonic %q --address %s --pin %s\n",
		pk.Mnemonic, pk.Address, pk.Pin)
	return nil
}

// ---------- invite ----------

func cmdInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	ttl := fs.Duration("ttl", time.Hour, "token validity (60s..24h)")
	jsonOut := fs.Bool("json", false, "output the invite response as JSON")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated — found a Clan first with `create`")
	}

	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(membership.InviteRequest{TTLSeconds: int(ttl.Seconds())})
	resp, err := cli.Post(base+"/clan/invite", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is `minti-cland` running and listening on %s?)", err, base)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("invite failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ir membership.InviteResponse
	if err := json.NewDecoder(resp.Body).Decode(&ir); err != nil {
		return err
	}

	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(ir)
	}
	fmt.Println("Invite token minted (single-use; share over a trusted channel).")
	fmt.Println()
	fmt.Printf("  Clan ID:    %s\n", ir.ClanID)
	fmt.Printf("  Address:    %s\n", ir.LANAddress)
	fmt.Printf("  Pin:        %s\n", ir.ClanCertPin)
	fmt.Printf("  Token:      %s\n", ir.Token)
	fmt.Printf("  Expires at: %s\n", ir.ExpiresAt)
	fmt.Println()
	fmt.Println("Joiner runs:")
	fmt.Printf("  minti-cland join --token %s --address %s --pin %s\n", ir.Token, ir.LANAddress, ir.ClanCertPin)
	return nil
}

// ---------- join ----------

func cmdJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	mnemonic := fs.String("mnemonic", "", "12-word BIP39 paste-key (omit if using --token)")
	token := fs.String("token", "", "invite token (omit if using --mnemonic)")
	address := fs.String("address", "", "LAN address of an existing member: ip:port (required)")
	pin := fs.String("pin", "", "sha256:<hex> cert pin (required)")
	_ = fs.Parse(args)

	if *address == "" || *pin == "" {
		return errors.New("--address and --pin are required")
	}
	if (*mnemonic == "" && *token == "") || (*mnemonic != "" && *token != "") {
		return errors.New("provide exactly one of --mnemonic or --token")
	}

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	existing, err := store.LoadClan()
	if err != nil {
		return err
	}
	if existing.IsActive() {
		return fmt.Errorf("already in Clan %s — `leave` first", existing.ClanID)
	}
	_ = cfg // not directly used; could expose --listen override here

	if *mnemonic != "" {
		return joinPasteKey(*mnemonic, *address, *pin, id, store)
	}
	return joinToken(*token, *address, *pin, id, store)
}

func joinPasteKey(mnemonic, address, pin string, id *identity.Identity, store *state.Store) error {
	clanKey, _, err := membership.PreJoinViaMnemonic(mnemonic)
	if err != nil {
		return fmt.Errorf("derive clan_key from mnemonic: %w", err)
	}
	kp, err := crypto.NewSimpleKeyProvider(clanKey)
	if err != nil {
		return err
	}
	cli, err := transport.NewClient(transport.ClientOpts{
		MemberID:    id.MemberID,
		KeyProvider: kp,
		Pin:         pin,
		Timeout:     30 * time.Second,
	})
	if err != nil {
		return err
	}
	reqBody, _ := json.Marshal(membership.WelcomeRequest{
		MemberID:     id.MemberID,
		MemberPubKey: id.PubKey,
	})
	resp, err := cli.Post("https://"+address+"/clan/welcome", "application/json", reqBody)
	if err != nil {
		return fmt.Errorf("call %s: %w", address, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("welcome rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var wr membership.WelcomeResponse
	if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
		return err
	}

	clan := &state.Clan{
		ClanID:             wr.ClanID,
		ClanCertPEM:        wr.ClanCertPEM,
		ClanCertPrivKeyB64: wr.ClanCertPrivKeyB64,
		Role:               "joined",
		JoinedAt:           time.Now().UTC(),
		Roster:             wr.Roster,
	}
	clan.SetClanKey(clanKey)
	// Recompute pin from the cert PEM we just received and double-check it
	// matches what the user gave us — catches the (rare) case where the
	// founder's cert PEM-encoding round-trips slightly differently.
	cc, err := crypto.ParseClanCertPEM([]byte(wr.ClanCertPEM))
	if err != nil {
		return fmt.Errorf("parse returned clan_cert: %w", err)
	}
	if cc.Pin != pin {
		return fmt.Errorf("pin mismatch: server cert hashes to %s, you gave %s", cc.Pin, pin)
	}
	clan.ClanCertPin = cc.Pin
	if err := store.SaveClan(clan); err != nil {
		return err
	}
	fmt.Printf("Joined Clan %s as %s (via paste-key). %d members in roster.\n", clan.ClanID, id.MemberID, len(clan.Roster))
	return nil
}

func joinToken(token, address, pin string, id *identity.Identity, store *state.Store) error {
	httpCli := transport.NewPinnedHTTPClient(pin, 30*time.Second)
	reqBody, _ := json.Marshal(membership.JoinRequest{
		Token:        token,
		MemberID:     id.MemberID,
		MemberPubKey: id.PubKey,
	})
	resp, err := httpCli.Post("https://"+address+"/clan/join", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("call %s: %w", address, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("join rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var jr membership.JoinResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return err
	}
	cc, err := crypto.ParseClanCertPEM([]byte(jr.ClanCertPEM))
	if err != nil {
		return fmt.Errorf("parse returned clan_cert: %w", err)
	}
	if cc.Pin != pin {
		return fmt.Errorf("pin mismatch: server cert hashes to %s, you gave %s", cc.Pin, pin)
	}
	clan := &state.Clan{
		ClanID:             jr.ClanID,
		ClanKeyB64:         jr.ClanKeyB64,
		ClanCertPEM:        jr.ClanCertPEM,
		ClanCertPrivKeyB64: jr.ClanCertPrivKeyB64,
		ClanCertPin:        cc.Pin,
		Role:               "joined",
		JoinedAt:           time.Now().UTC(),
		Roster:             jr.Roster,
	}
	if err := store.SaveClan(clan); err != nil {
		return err
	}
	fmt.Printf("Joined Clan %s as %s (via invite token). %d members in roster.\n", clan.ClanID, id.MemberID, len(clan.Roster))
	return nil
}

// ---------- leave ----------

func cmdLeave(args []string) error {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	confirm := fs.Bool("yes", false, "skip the destructive-op confirmation")
	_ = fs.Parse(args)

	_, _, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("already unaffiliated")
	}
	if !*confirm {
		return fmt.Errorf("this will wipe local Clan state (clan_id %s, role %s) — pass --yes to confirm",
			clan.ClanID, clan.Role)
	}
	// Local wipe — daemon may still be running but will pick up the change on its next state read.
	if err := store.SaveClan(&state.Clan{}); err != nil {
		return err
	}
	fmt.Printf("Left Clan %s. State wiped.\n", clan.ClanID)
	return nil
}

// ---------- revoke ----------

func cmdRevoke(args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	reason := fs.String("reason", "", "human-readable reason recorded in the revocation entry")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		return errors.New("usage: minti-cland revoke <member_id> [--reason \"...\"]")
	}
	targetID := fs.Arg(0)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}

	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(membership.RevokeRequest{MemberID: targetID, Reason: *reason})
	resp, err := cli.Post(base+"/clan/revoke", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is `minti-cland` running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	fmt.Printf("Revoked %s from Clan %s.\n", targetID, clan.ClanID)
	return nil
}

// ---------- peer-add ----------

func cmdPeerAdd(args []string) error {
	fs := flag.NewFlagSet("peer-add", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		return errors.New("usage: minti-cland peer-add <ip:port>")
	}
	target := fs.Arg(0)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(peers.PeerAddRequest{Address: target})
	resp, err := cli.Post(base+"/clan/peer-add", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peer-add failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	fmt.Printf("Registered peer %s in Clan %s.\n", target, clan.ClanID)
	return nil
}

// ---------- peers ----------

func cmdPeers(args []string) error {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/clan/peers", nil)
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peers failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		_, _ = io.Copy(os.Stdout, resp.Body)
		return nil
	}
	var pl peers.PeersListResponse
	if err := json.NewDecoder(resp.Body).Decode(&pl); err != nil {
		return err
	}
	fmt.Printf("Clan %s\n", clan.ClanID)
	fmt.Printf("Candidates (%d):\n", len(pl.Candidates))
	for _, c := range pl.Candidates {
		fmt.Printf("  %s  via=%s  first_seen=%s\n",
			c.Address, c.DiscoveredVia, c.FirstSeen.Format("2006-01-02T15:04:05Z"))
	}
	now := time.Now()
	fmt.Printf("Members (%d):\n", len(pl.Members))
	for _, m := range pl.Members {
		rs, ss := 0, 0
		if m.LatestAd != nil {
			rs, ss = m.LatestAd.ReasoningScore, m.LatestAd.SystemScore
		}
		fmt.Printf("  %s @ %-22s  via=%-8s  reason=%3d  sys=%3d  ad_fresh=%t  last_ad=%s\n",
			m.MemberID, m.Address, m.DiscoveredVia, rs, ss, m.AdFresh(now),
			m.LastAd.Format("2006-01-02T15:04:05Z"))
	}
	return nil
}

// ---------- pin ----------

func cmdPin(args []string) error {
	fs := flag.NewFlagSet("pin", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	self := fs.Bool("self", false, "pin self as Orchestrator (overrides election score)")
	clear := fs.Bool("clear", false, "clear the self-pin")
	_ = fs.Parse(args)
	if *self == *clear {
		return errors.New("pass exactly one of --self or --clear")
	}

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(election.PinRequest{Value: *self})
	resp, err := cli.Post(base+"/clan/pin", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pin failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var pr election.PinResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return err
	}
	if pr.PinnedOrchestrator {
		fmt.Println("self-pinned (will win the next election regardless of score).")
	} else {
		fmt.Println("self-pin cleared (election reverts to score-based selection).")
	}
	return nil
}

// ---------- orchestrator ----------

func cmdOrchestrator(args []string) error {
	fs := flag.NewFlagSet("orchestrator", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/clan/orchestrator", nil)
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("orchestrator failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		_, _ = io.Copy(os.Stdout, resp.Body)
		return nil
	}
	var or election.OrchestratorResponse
	if err := json.NewDecoder(resp.Body).Decode(&or); err != nil {
		return err
	}
	fmt.Printf("Self:        %s\n", or.Self)
	if or.CurrentOrchestrator == "" {
		fmt.Println("Orchestrator: (none — election not yet committed)")
		return nil
	}
	tag := ""
	if or.IsSelf {
		tag = " (self)"
	}
	fmt.Printf("Orchestrator: %s%s\n", or.CurrentOrchestrator, tag)
	fmt.Printf("Term:         %d\n", or.CurrentTerm)
	if !or.LeaseExpires.IsZero() {
		fmt.Printf("Lease until:  %s\n", or.LeaseExpires.Format(time.RFC3339))
	}
	return nil
}

// ---------- election-history ----------

func cmdElectionHistory(args []string) error {
	fs := flag.NewFlagSet("election-history", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/clan/election/history", nil)
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("election-history failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		_, _ = io.Copy(os.Stdout, resp.Body)
		return nil
	}
	var hr election.HistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return err
	}
	if len(hr.Entries) == 0 {
		fmt.Println("No elections recorded yet.")
		return nil
	}
	fmt.Printf("Recent elections (%d):\n", len(hr.Entries))
	for _, e := range hr.Entries {
		fmt.Printf("  term=%-4d winner=%-36s reason=%-20s at=%s\n",
			e.Term, e.Winner, e.Reason, e.At.Format(time.RFC3339))
	}
	return nil
}

// ---------- rotate-key ----------

func cmdRotateKey(args []string) error {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	resp, err := cli.Post(base+"/clan/rotate-key", "application/json", []byte("{}"))
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if *jsonOut {
		fmt.Println(string(raw))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("rotate-key returned %d", resp.StatusCode)
		}
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rotate-key failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var res keyrotate.RotateResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	fmt.Printf("Rotation committed (propose_id=%s)\n", res.ProposeID)
	fmt.Printf("  ACKed by %d peers: %v\n", len(res.AckedBy), res.AckedBy)
	if len(res.FailedBy) > 0 {
		fmt.Printf("  Failed: %v\n", res.FailedBy)
	}
	fmt.Printf("  Grace window: %v (old key still valid)\n", keyrotate.DefaultGraceDuration)
	return nil
}

// ---------- members ----------

func cmdMembers(args []string) error {
	fs := flag.NewFlagSet("members", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	_ = fs.Parse(args)

	_, _, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated — no Clan to show")
	}
	fmt.Printf("Clan %s (%d members)\n", clan.ClanID, len(clan.Roster))
	for _, m := range clan.Roster {
		fmt.Printf("  %s  %-9s  admitted=%s  last_seen=%s\n",
			m.MemberID, m.State,
			m.AdmittedAt.Format("2006-01-02T15:04:05Z"),
			m.LastSeenAt.Format("2006-01-02T15:04:05Z"))
	}
	return nil
}

// ---------- show ----------

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/minti/cland.yaml", "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
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
	if addr, err := resolveLANAddr(cfg); err == nil {
		fmt.Printf("LAN addr:   %s\n", addr)
	}
	return nil
}

// ---------- helpers ----------

// loadCommon parses config, identity, and state — used by every subcommand
// that needs all three.
func loadCommon(cfgPath, stateDirOverride string) (config.Config, *identity.Identity, *state.Store, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return cfg, nil, nil, err
	}
	if stateDirOverride != "" {
		cfg.State.Dir = stateDirOverride
	}
	id, err := identity.LoadOrCreate(cfg.State.Dir)
	if err != nil {
		return cfg, nil, nil, err
	}
	store, err := state.NewStore(cfg.State.Dir)
	if err != nil {
		return cfg, id, nil, err
	}
	return cfg, id, store, nil
}

// localDaemonClient builds a transport.Client pointed at the running local
// daemon. Uses 127.0.0.1 when the daemon binds 0.0.0.0/empty/::, so the
// CLI loop never accidentally goes off-host.
func localDaemonClient(cfg config.Config, clan *state.Clan, id *identity.Identity) (*transport.Client, string, error) {
	host := cfg.Listen.Address
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := fmt.Sprintf("https://%s:%d", host, cfg.Listen.Port)

	kp, err := crypto.NewSimpleKeyProvider(clan.ClanKey())
	if err != nil {
		return nil, "", err
	}
	cli, err := transport.NewClient(transport.ClientOpts{
		MemberID:    id.MemberID,
		KeyProvider: kp,
		Pin:         clan.ClanCertPin,
		Timeout:     15 * time.Second,
	})
	if err != nil {
		return nil, "", err
	}
	return cli, base, nil
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
