# MINTI — Project Status

> **Last updated:** 2026-05-28
> **Purpose:** Read this *first* when opening a new chat or onboarding to the project. It's the single document that tells you where MINTI is right now and how to pick up work without re-reading history.

---

## TL;DR

MINTI is a minimal, AI-agent-first Linux software stack plus a cross-OS **Clan protocol** for distributed local AI compute. **M0–M3 are done and validated** in the `minti-dev` Linux Mint VM. Install path works; `minti-runtime` serves OpenAI + Ollama + **Anthropic** (`/v1/messages`, M3) shapes; 5 stdio MCP tool servers route through a policy-gated framework; `minti-pack-recon` installs the nmap/whois/dig toolchain; **opencode** (sst, MIT) is bundled with a system-wide config that registers minti-runtime as a custom provider and all 5 MCP servers as stdio commands; **Claude Code preset** is documented in `docs/claude-code-preset.md` for users who already have it. **M4 in flight**: cland Phases 0 + A + B + C + D + E + F + G + H-1 + H-2 + I done — full security + protocol stack PLUS Debian-family install path. **End-to-end-validated on the real Linux Mint 22.3 `minti-dev` VM**: install.sh stages everything, hardened systemd unit + cland service comes up active, lone-member Clan forms + self-elects (term=1), minti-fetch surfaces full Clan-aware status, idempotent re-install preserves configs. Phase J (two-node testbed validation) wraps M4.

---

## Source of truth

The PRD is the authoritative spec. **Read it before any implementation work:**

- **Path:** `C:\Users\aouad\.claude\plans\hello-can-we-create-abundant-hopper.md`
- **Version:** **v0.6** (current)
- **Core principle (P1):** Open-source-first. MINTI never *depends* on a closed-source service. Default agent clients (`opencode`, OpenHands), default models (Hermes 3, Llama, Qwen, DeepSeek), default runtime (Ollama / llama.cpp), and default transport are all open. Proprietary services (Claude Code, Anthropic API, OpenAI) are optional integrations users can choose to bring — never bundled, never required.
- **Other principles:** P2 (resurrection of dead hardware — installable on 1–2 GB old machines), P3 (PRD wins over clever-code).

---

## Current milestone

### Done ✅
- **M0** — Repo skeleton, Clan Protocol Spec v0.1 (`docs/clan-protocol.md`), `install.sh` v0
- **M1** — `runtime-adapter/` Go daemon. Backend interface + Ollama implementation + OpenAI-compatible + Ollama-compatible HTTP API, with systemd unit, config schema, install integration. Builds clean on Windows + cross-compiles to Linux amd64. All endpoints smoke-tested end-to-end.
- **Validation pass** — Full install + chat smoke test on real Debian 13 in WSL2. Two install bugs caught + fixed.
- **Dev environment** — Linux Mint 22.3 Xfce VM in VirtualBox with the entire MINTI stack installed.

- **M2 — MCP servers + first tool pack** (2026-05-26 ✓ validated end-to-end in the `minti-dev` VM)
  - 5 MCP servers under `mcp-servers/cmd/mcp-{fs,shell,recon,http,pkg}/`, each speaking stdio MCP via the official `github.com/modelcontextprotocol/go-sdk` v1.6.1.
  - Shared framework in `mcp-servers/internal/{policy,audit,permission,mcpserve,proc}/`. Every tool call routes through policy → audit transparently.
  - `mcptest` stdio test harness in `cmd/mcptest/` — renders consent prompt on TTY.
  - `minti-pack-recon` debian metapackage in `packs/recon/debian/` — Depends: nmap, masscan, whois, dnsutils|bind9-dnsutils. Recommends: theharvester, amass. Suggests: rustscan, golang-go.
  - `install.sh` extended: stages MCP binaries to `/opt/minti/mcp/`, installs `mcptest` to `/usr/local/bin/`, writes default `/etc/minti/policy.yaml`, prepares `~/.minti/` for the invoking user.
  - `docs/clan-protocol.md` §7.2 extended with `deny_tools` per-namespace kill switch + `allow_raw_socket` recon gate + system+user policy overlay.
  - **Acceptance:** `mcptest --yes --arg target=127.0.0.1 /opt/minti/mcp/minti-mcp-recon nmap_scan` ran a real `nmap -sV -T3 --top-ports 1000 127.0.0.1` (returned 22/ssh + 631/cups in 6.2s) with the call audited.
  - **Deny path:** same call with `mcp.recon.deny_tools: [nmap_scan]` in `~/.minti/policy.yaml` → refused before nmap spawn; audit log shows `decision: deny, reason: tool 'nmap_scan' is on recon.deny_tools`.

- **M3 — Agent client integration** (2026-05-26 ✓ validated end-to-end in the `minti-dev` VM)
  - **Anthropic `/v1/messages` surface added to `runtime-adapter`** (non-streaming + SSE). Test bench: real `llama3.2:3b` returned a clean Anthropic-shaped envelope with `type: message`, proper `stop_reason`, usage stats; SSE emitted all 6 event types in spec order (message_start, content_block_start, 12 content_block_delta, content_block_stop, message_delta, message_stop). Tool-use content blocks in messages are rejected with a clear 400 — deferred to M3.5.
  - **opencode v1.15.10 (sst, MIT) bundled** via the official installer, symlinked from `~/.opencode/bin/opencode` to `/usr/local/bin/opencode` so all users find it on PATH. System config template at `/etc/minti/opencode.config.example.json` + per-user default dropped at `~/.config/opencode/opencode.json` (preserved if already present). Config registers `minti-runtime` as a custom `@ai-sdk/openai-compatible` provider at `http://127.0.0.1:7780/v1` and all 5 stdio MCP servers by path.
  - **Claude Code preset** documented at `docs/claude-code-preset.md` — env-var (`ANTHROPIC_BASE_URL=http://127.0.0.1:7780`) + `claude mcp add` recipe. Tool execution flows directly via stdio to `/opt/minti/mcp/*`; chat reasoning flows through `/v1/messages` against the local model.
  - **install.sh idempotency hardening**: now compares the new on-disk binary's hash to the running process's `/proc/PID/exe` hash. Catches the failure mode where a prior install replaced the binary on disk but never restarted the service (B13 in DOCUMENTATION.md).

### In flight 🟡

- **M4 — Clan Layer v1** (PRD §8, ~4 weeks part-time — "the iceberg"). Started 2026-05-27.
  - **Phase 0 done** (`dccd7fb` + `577daed`): `docs/clan-protocol.md` → v0.2 with 6 peer-review-surfaced edits (12-word BIP39 paste-key + HKDF, `recently_failed` definition, `target_member` in signed token claims + v1 honesty note, `/v1/messages` + `/clan/peer-add` rows, OQ-2 pulled into v1). Local-LLM peer-review driver + raw reviews at `scripts/m4-peer-review.py` + `scripts/m4-reviews/`.
  - **Phase A done** (`8b1c2fd`): cland module skeleton — `cmd/minti-cland/main.go` + `internal/{config,identity,state,auditlog}/`. Ed25519 keypair + UUIDv4 persist to `identity.json` (mode 0600); Clan state at `clan.json` (also 0600, holds `clan_key`) with atomic-rename writes; auditlog schema duplicated verbatim from `mcp-servers/internal/audit` (D-M4.6).
  - **Phase B done** (`990fbc2`): crypto + transport. `crypto/` package — self-signed X.509 from Ed25519 with SPKI `sha256:<hex>` pin; HMAC-SHA256 over canonical request `(METHOD\nPATH\nsha256(body)hex\nts\nnonce)` per spec §2.3; `KeyProvider` interface (Current + optional Grace) wired from day one per D-M4.11. `transport/` package — HTTPS server with auth middleware (±60 s window, replay-rejection via LRU-bounded nonce cache, 401 empty body + audit-log on every reject, grace-key acceptance during rotation, `HandleAnonymous` escape hatch for the future `/clan/join`); HTTPS client with `VerifyPeerCertificate` SPKI pin enforcement.
  - **Phase C-core done** (`0210239`): BIP39 paste-key + membership crypto core + CLI subcommands. `internal/bip39/` vendors the canonical bitcoin/bips english.txt (sha256 verified) + 12-word mnemonic encode/decode w/ checksum + case/whitespace normalisation. `internal/membership/Create()` founds a Clan; `PreJoinViaMnemonic()` does the joiner-side derivation. CLI gains `create`/`members`/`show`. **Founder + joiner derive byte-identical clan_keys** given the same mnemonic.
  - **Phase C-rest done** (`ef328f2`): HTTP endpoints (`/clan/{invite,join,welcome,members,leave,revoke}`) on top of the Phase B transport; `invite` uses HandleAnonymous (joiner has no key yet), the other 5 are HMAC-gated. `InviteStore` (single-use, TTL 60s..24h), `Service.{Welcome,Leave,Revoke,SweepZombies}` + 60 s zombie sweep ticker per spec §3.1. CLI `invite`/`join` (paste-key + token paths)/`leave`/`revoke`. End-to-end smoke proved founder + joiner pair formation.
  - **Phase D done** (`73ecb2a` foundations + `d3f26a3` wiring + 2 prereq fixes): three foundation packages — `peers/` (Registry with candidate vs member-keyed stores, AdFresh/Live predicates, peer-add DoS guardrails 100-cap + 10/60s per-origin + TCP pre-dial), `scores/` (rubric loader + ReasoningScore + SystemScore + sliding-5-min RecentFailures), `probe/` (Linux + Windows hardware probe with EMA-smoothed SHA-256 CPU benchmark, nvidia-smi VRAM, runtime-adapter capabilities client). Then `discovery/` (grandcat/zeroconf with 1s debounce + clan_id filter + foreign-clan WARN + IsActive gating + Candidate-only emission per qwen3.6 1A) and `advertise/` (30s tick + 5s initial-delay + Bump rate-limited + per-peer failure record). Three new HTTP endpoints — `/clan/{advertise,peer-add,peers}` — registered behind the existing HMAC middleware. Two new CLI subcommands (`peer-add`, `peers`).
    - **2 prereq fixes** surfaced + landed in this commit: (1) the daemon's TLS server now uses the **shared** `clan_cert` priv key (founder's Ed25519) — Phase C latently used each member's identity priv, which only worked one-way; (2) **Advertisement.LANAddress** added so receivers register the sender's listen port, not the incoming TCP socket's ephemeral source port.
    - **End-to-end smoke** (Windows, two state dirs on 127.0.0.1:17980 + :17981): founder creates → daemons run on both sides → `peer-add` from founder → within 6s both `peers` commands show each other as `ad_fresh=true` with `system_score=66`.

  - **Phase E done** (2026-05-27, see commit log): leader-lease election engine. New `cland/internal/election/` package — `state.go` (in-memory term/orch/lease + history ring), `engine.go` (single-goroutine ticker driving heartbeat emit + election trigger + candidate selection + quorum + ⌈N/2⌉ accept counting), `handlers.go` (`POST /clan/heartbeat`, `GET /clan/orchestrator`, `GET /clan/election/history`, `POST /clan/pin`). Spec §5.2 cadence (HEARTBEAT_INTERVAL=2s / LEASE_DURATION=8s / FAILOVER_GRACE=6s / ELECTION_TIMEOUT=1s) configurable via `cfg.Election`. State extensions: `Clan.CurrentTerm` + `Clan.CurrentOrchestrator` + `Clan.PinnedOrchestrator` persisted via the existing atomic-rename path. New CLI: `pin --self|--clear`, `orchestrator`, `election-history`.
    - **7 peer-review-driven design fixes** baked in: R1 zombie-leader gate (heartbeat skipped if runtime probe unhealthy), R2 persist-on-change only (LeaseExpires in-memory — eliminates 1-write-per-2s I/O storm), R3 full spec §5.3 payload schema (`active_roster` + `reasoning_score`), R4 local lease tracking (ignore wire `lease_until`), R5 active-only quorum (drop `admitted`), R6 startup grace (no election in first FAILOVER_GRACE — handles post-restart heartbeat-catch-up), R7 50-150 ms random backoff. Plus a step-down behavior (Orchestrator vacates when `selectCandidate` now prefers another, enabling pin propagation), a higher-term-bypasses-anti-spoof rule (Raft convention; without it a restarted daemon split-brains against a survivor that ran a successful failover), and a `HeartbeatSeen` flag in `peers.Member` (so a once-heartbeating-now-dead peer drops out of candidate selection instead of being preserved by the AdFresh fallback).
    - **3 peer-review artifacts** at `scripts/m4-reviews/phaseE-{qwen3.6_latest,deepseek-r1_32b,gemma4_31b}.md`. gemma4 returned the only blocker (E1 zombie leader → R1), qwen3.6 caught the active-only quorum point (R5), deepseek corroborated. Driver at `scripts/m4-phaseE-review.py`; raw input snapshot at `scripts/m4-reviews/phaseE-input.txt`.
    - **16 unit tests green** in `internal/election/` covering lone-member election, AdmittedAt tiebreaker, pin override, multi-pin tiebreaker, anti-spoof (same-term reject) + higher-term bypass, term replay, lease expiry trigger, active-only quorum, R7 backoff bounds, persistence-omits-LeaseExpires, zombie-leader gate, startup grace, foreign clan_id reject, and step-down on preferred-elsewhere.
    - **End-to-end smoke** (`scripts/m4-phaseE-smoke.ps1`): two daemons on `127.0.0.1:17980` + `:17981`, paste-key join, advertise exchange, election commits with both sides agreeing on Orchestrator at term=1, kill the Orchestrator, survivor takes over at term=2 within 14 s, restart the killed daemon → it loads persisted term=1 + then accepts the survivor's higher-term heartbeat (term=2). Election-history surfaces the failover reason. `MINTI_CLAND_FORCE_HEALTHY=1` env-var hatch documented in main.go + advertise.go lets the smoke run without minti-runtime alongside; production deployments leave it unset and the R1 gate enforces.

  - **Phase F done** (2026-05-27, see commit log): routing layer. New `cland/internal/router/` package implements spec §6.1: every cland process exposes `POST /v1/chat/completions` + `POST /v1/messages` + `POST /api/chat` on its HMAC transport. The Orchestrator self-routes via the loopback fast path (D-M4.10) — straight to `http://127.0.0.1:7780`, no TLS, no HMAC. Non-Orchestrators proxy transparently to the current Orchestrator over HMAC HTTPS using the same `transport.Client` the election engine already uses. Streaming pass-through preserves SSE (OpenAI/Anthropic) + NDJSON (Ollama) chunk boundaries via explicit `http.Flusher` writes. Inbound HMAC headers are stripped on the upstream hop (the Orchestrator re-signs when forwarding to a peer; runtime-adapter doesn't need them). Hop-by-hop headers (RFC 7230 §6.1) also stripped.
    - **No orchestrator-yet → 503**; **peer-addr-unknown → 503** with `X-Minti-Expected-Orchestrator` header pointing at the persisted current; **local-runtime-down → 502**. Decorated with `auditlog` entries for every accept and deny.
    - **8 unit tests** in `internal/router/`: no-orchestrator reject, self-orchestrator self-routes, local-runtime-down → 502, not-orchestrator peer-proxies, peer-addr-unknown → 503 + redirect header, SSE pass-through, HMAC-quad stripped on forward, real-HTTP roundtrip against `httptest.NewServer` fake runtime.
    - **Phase E regression smoke** re-run with router wired in: election, failover, term bump, restart-accept all pass — router didn't disturb anything.
    - **Deferred**: a real chat-completion end-to-end smoke needs runtime-adapter + Ollama wired into the daemon test rig. The router proxy logic is fully covered by the unit suite + the Phase E regression; the smoke deferral is about test-harness scope, not correctness gaps. Worker routing (`system_score × (1 - load)`) is plumbed in the package but unused until `/v1/embeddings` / `/v1/vision` endpoints land in runtime-adapter.

  - **Phase G done** (2026-05-28, see commit log): cross-Clan tool execution. New `cland/internal/toolexec/` package serves spec §7.1's `POST /mcp/execute`:
    - **Token** (`token.go`) — request_id / origin_member / target_member / tool / args_hash / approved_at / exp / sig. Canonical = LF-joined fields; HMAC-SHA256 over canonical with the shared `clan_key` via `crypto.KeyProvider` (current + grace, so rotation doesn't break in-flight tokens). `args_hash = sha256(raw_args_bytes)` — origin and target see byte-identical args; we never re-marshal, so JSON key-ordering can't break verification.
    - **Executor** (`executor.go`) — wire spec `"mcp-recon.nmap_scan"` parses to server `minti-mcp-recon` (binary at `cfg.MCP.BinariesDir`) + tool `nmap_scan`. Spawns the subprocess via the official `github.com/modelcontextprotocol/go-sdk` v1.6.1 (same SDK mcptest uses), lists tools to validate the name, calls the tool, normalises `[]Content` blocks into a JSON-safe `ExecResult`. `exec.CommandContext` + `DefaultExecTimeout=5 min` cap runaways.
    - **Replay** (`replay.go`) — bounded in-memory `request_id` cache (LRU-style sweep when full, per-entry TTL). Default 10 000 entries × 15 min — covers the worst-case `DefaultMaxTokenLifetime=10 min` token window.
    - **Handler** (`handlers.go`) — strict order: (1) args-hash equality (catches arg tampering after sign), (2) HMAC verify under current OR grace key, (3) target/exp/approved_at claims, (4) replay (only AFTER all prior checks pass — so attackers can't pollute the cache cheaply), (5) spawn. Each failure mode emits a distinct 401/403/502 + a structured `auditlog` entry with a stable reason string.
    - **mcp-servers stub updated** — `permission.VerifyCrossClanToken` retains its signature but now documents that real verification lives in `cland/internal/toolexec`; nothing in mcp-servers calls the function, the executor's local `policy.deny_tools` is enforced inside each MCP server binary just as it was in M2.
    - **v1 honesty note** baked into the package doc-comment: the shared `clan_key` HMAC proves the token was issued by *some* current Clan member, not specifically by the claimed `origin_member`. A malicious insider could forge a token claiming a different origin. The "permission prompt on origin" guarantee is UI control, not cryptographic. v2 per-member keypairs close this gap.
    - **13 unit tests** in `internal/toolexec/`: sign/verify roundtrip, tamper-detection (Tool + RequestID bit-flip), wrong target → 403, expired → 401, future approved_at → 401, replay → 401, foreign clan_key → 401, args-tamper-after-sign → 401, grace-key acceptance during rotation, happy-path with audit, executor failure → 502, namespace parsing, replay cache TTL eviction.
    - **Phase E smoke regression-clean** with toolexec wired in.
    - **Deferred**: a real end-to-end smoke (origin daemon mints token → POSTs to target daemon → real MCP server spawns → returns tool output) needs a built minti-mcp-* binary on the smoke rig. The 13 unit tests + the M2 mcptest pattern (which has run real recon scans end-to-end) cover the executor path; the deferral is about smoke-test harness scope, not correctness. Local cland-side policy intercept (the spec's `403 policy_deny` before spawn) is deferred — the MCP server's own `deny_tools` enforcement still applies, but as an `IsError` result rather than a clean 403.
  - **Phase H-1 done** (2026-05-28, see commit log): key rotation 2PC. New `cland/internal/keyrotate/` package:
    - **types.go**: ProposeRequest / CommitRequest / AbortRequest / AckResponse / RotateResult wire shapes. PROPOSE_TIMEOUT=60s, MemberRevertAfter=90s (1.5×), DefaultGraceDuration=5min per spec §8.1.
    - **store.go**: in-memory `ProposeStore` — at most one pending propose; `ErrAlreadyPending` on collision; idempotent re-put for same id; `SweepExpired` clears pending after MemberRevertAfter (defends against an Orchestrator crashing mid-rotation).
    - **member.go**: `MemberHandler` exposes `POST /clan/rotate-key/{propose,commit,abort}` behind the existing HMAC middleware. Commit pops the pending key and invokes `KeyProvider.Rotate(newKey, graceDur)` — `crypto.KeyProvider`'s existing rotation mechanics (grace = old key, current = new) handle the 5-min in-flight tolerance natively.
    - **coordinator.go**: Orchestrator-side 2PC. Generates 32-byte new key + propose_id, broadcasts PROPOSE to all active roster members in parallel, collects ACKs within PROPOSE_TIMEOUT. If any peer 4xx/5xx/timeout → broadcast ABORT to everyone who ACKed (so they don't have to wait MemberRevertAfter to revert) + skip self-rotate. If all ACK → broadcast COMMIT + self-rotate. Lone-orchestrator special case (no peers): self-rotates trivially.
    - **trigger.go**: `POST /clan/rotate-key` (the local-CLI trigger) checks Orchestrator-status gate first, returns 503 + "not orchestrator" if not. Routes to coordinator.Rotate().
    - **CLI**: `minti-cland rotate-key` — POSTs to local daemon, prints commit/abort result.
    - **Daemon wiring** in main.go: also starts a background sweep goroutine that calls `proposeStore.SweepExpired()` every PROPOSE_TIMEOUT (60s) — covers the orchestrator-crash scenario.
    - **10 unit tests** in `internal/keyrotate/`: store put-accepted / put-different-id-409 / put-same-id-idempotent / sweep-expired / take-matches-id; member propose happy / commit-without-propose-409 / commit-after-propose-rotates / abort-clears; coordinator happy-all-ack / one-peer-fails-aborts (no self-rotate) / lone-orch / network-error-treated-as-failure.
    - **Phase E smoke regression-clean** with keyrotate wired.
    - **Deferred to Phase H-2**: revocation gossip. `/clan/revoke` already exists from Phase C (writes `revocations.json`, flips roster entry to "revoked", `peers.Registry.SetRevocations` consults the list on candidate binding). What's missing is *cross-daemon propagation* — Phase H-2 will piggyback revocations digest on heartbeats so a partitioned-then-healed node syncs.

  - **Phase H-2 done** (2026-05-28, see commit log): revocation gossip via heartbeat digest. New `cland/internal/revocations/` package:
    - **state.Revocations.Digest()** — sha256-hex of sorted member_ids, LF-joined. Stable across permutations; empty list yields a fixed value (sha256 of empty input); per-entry timestamps + reasons are not in the digest (only the SET of revoked members matters for membership filtering).
    - **state.Revocations.Merge(other)** — union, dedup by MemberID, local metadata wins on conflict.
    - **election.Heartbeat** gains `revocations_digest` (emitted by `Engine.emitHeartbeats` + `runElection` announce; non-blocking, ignored by Phase E receivers that haven't been recompiled).
    - **revocations.Syncer.MaybeSync(senderID, theirDigest)** — called from `election.Handlers` after the heartbeat passes lease/term/anti-spoof. Compares local digest to theirs; on mismatch + unknown peer addr → log + skip; on mismatch + known addr → GET `https://<addr>/clan/revocations` via the HMAC-stamping `transport.Client`, merge into local store, refresh `peers.Registry.SetRevocations` so the next `BindMember` consults the updated set. Per-peer in-flight dedup prevents concurrent fetches when many heartbeats arrive close together.
    - **revocations.Handler** registers `GET /clan/revocations` behind the existing HMAC middleware.
    - **8 unit tests** in `internal/revocations/`: digest permutation-invariant, empty digest has stable value, empty-digest MaybeSync skips, matching digest no-op, mismatched digest fetches + merges + refreshes registry, unknown-peer-addr doesn't panic, fetch error preserves local, idempotent on repeat, GET returns correct shape, GET on empty store returns empty list.
    - **Phase E smoke regression-clean** with revocations wired.
  - **Phase I done** (2026-05-28, see commit log): install + systemd + Makefile + minti-fetch. End-to-end smoke-validated on the real `minti-dev` Linux Mint 22.3 VM:
    - **Makefile**: VERSION bumped to `0.1.0-M4`; new `cland` (native) + `cland-linux` (cross-compile) targets parallel to runtime; `cland` joined `all:`; `clean` extended.
    - **`cland/systemd/minti-cland.service`**: hardened from the runtime unit — `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `NoNewPrivileges=true`, `LockPersonality=true`, `MemoryDenyWriteExecute=true`, `RestrictRealtime=true`, `User=minti`, `Group=minti`. `ReadWritePaths=/var/lib/minti/cland /var/lib/minti`. **`IPAddressDeny=any` deliberately omitted** (cland needs LAN peer traffic on :7777; documented). `Restart=on-failure` + `RestartSec=3`.
    - **`install/install.sh`**: cland deployment block inserted between MCP and opencode sections. Reuses M3 B13 binary-hash-vs-`/proc/PID/exe` restart pattern verbatim. Reuses config-without-clobbering pattern for both `cland.yaml` and `reasoning-scores.yaml`. State dir mode 0700 owner minti:minti (holds clan_key). Final summary line adds `Cland:`; next-steps surfaces `minti-cland create`.
    - **`branding/minti-fetch`**: legacy static-file Clan section (which nothing ever wrote) replaced with live `minti-cland show` + `minti-cland orchestrator --json` calls. Surfaces clan_id + role + member count + Orchestrator + `(self)` tag + term. Falls back to `(sudo for details)` on permission error (system daemon's clan_key is unreadable by regular users) and `(not affiliated)` pre-Clan. 1 s timeouts on all daemon calls so a hung daemon never blocks fetch.
    - **1 operational fix surfaced + landed**: the daemon under `ProtectHome=true` couldn't reach `/home/minti/.minti/audit.jsonl` (the default auditlog path) — process crash-looped on startup. Fix: `Environment=HOME=/var/lib/minti/cland` in the systemd unit redirects the per-user audit log into the already-writable state dir. Zero code change; works because the `minti` system user is `--no-create-home` (the real /home/minti was never going to exist anyway).
    - **Verified end-to-end** in the VM: install completes cleanly (5/5 MCP + runtime + cland + opencode), service goes `active`, `minti-fetch` correctly shows `(not affiliated)` pre-Clan and `Clan: <id> role=founder members=1 Orch: <self> (self) term=1` post-`create`. HTTPS listener on :7777 binds, returns 401 to unauthed curls (HMAC auth wired). Re-running install.sh is idempotent: configs preserved, binary hash matches → no restart. Journald shows clean election cycle ("election engine started" → "election won term=1 accepts=1 quorum=1").
  - **Next:** **Phase J** — clone `minti-dev → minti-dev-2` (host-only NIC 2 for mDNS), run the 16-test acceptance gate. M4 done after J.

  - **Phase E done** (2026-05-27, see commit log): leader-lease election engine.

### Next after M4

- **M4 — Clan Layer v1** (PRD §8, ~4 weeks part-time — "the iceberg"):
  - `cland` daemon in Go (cross-compiles to Linux/Windows/macOS via PRD D11).
  - mDNS discovery service `_minti-clan._tcp.local`.
  - Membership state machine (`unaffiliated → invited → admitted → active → demoted/revoked`).
  - Leader-lease + 2 s heartbeat / 8 s expiry election (PRD §5.0 D10 / docs/clan-protocol.md §5).
  - HTTPS endpoint on `:7777` with self-signed Clan cert pinned at join + HMAC auth with shared Clan Key.
  - Whole-request routing: chat completions and MCP tool calls — the latter using the signed permission-token flow from clan-protocol.md §7.1 (`permission.VerifyCrossClanToken` shifts from M2 stub to real implementation).
  - Key rotation `/clan/rotate-key` + member revocation `/clan/revoke` (PRD §6.4 / G9).
  - This is the security/distributed-systems core — **stays Tier 2 entirely** per PRD §13.

### Future 🔲
- M5: Cross-platform Clan Agent (Windows + macOS)
- M6: Security hardening + remaining packs (webapp, wireless, forensics) + Pack Manager
- M7: Old-hardware smoke test
- M8: v1.0 release
- M3.5 (squeeze-in candidate): tool-use content blocks in `/v1/messages` (Claude Code's native tool-call shape over the API rather than via separate MCP stdio). Currently rejected with 400.

---

## How to resume work

### Single prompt to start the new chat

```
Continuing MINTI build. Read memory at
C:\Users\aouad\.claude\projects\C--Users-aouad-Documents-CCode-MINT-MINT-wip\memory\MEMORY.md
then read STATUS.md and the super-plan at
C:\Users\aouad\.claude\plans\velvet-drifting-codd.md. M0–M3 done;
M4 Phases 0 + A + B + C + D + E + F + G + H-1 + H-2 + I all committed
+ end-to-end-validated on the real minti-dev Linux Mint VM (install
clean, systemd active, lone-member Clan self-elects, minti-fetch
shows Clan surface, idempotent re-install). Pre-flight, then execute
Phase J per the super-plan — write scripts/m4-setup-second-vm.ps1
(VBoxManage clone minti-dev → minti-dev-2 with host-only NIC 2 for
mDNS multicast + ssh + cland port-forwards) and scripts/m4-validate.sh
(bash, runs in Node A's VM — the 16-test acceptance gate from the
super-plan). User powers both VMs on, runs install on -dev-2 (cland
should be idempotent there too), then runs validate. Expected
fail-mode hot spots flagged in the plan file: test 14 (mDNS goodbye
on hard-kill — we don't send one yet), test 10's third-node
fast-forward (JOIN handshake doesn't re-fetch revocations yet).
M4 done after J.
```

### Repo location

- Path: `C:\Users\aouad\Documents\CCode\MINT\MINT_wip`
- Branch: `main`
- Remote: **none** (local-only by user choice; add later if publishing)

### Test environments (both currently powered off)

| Env | How to start | Notes |
|---|---|---|
| **VirtualBox VM `minti-dev`** | `& "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe" startvm minti-dev --type gui` (or `--type headless` for SSH-only) | Linux Mint 22.3 Xfce, 8 GB RAM, 4 vCPUs, 40 GB disk. Full MINTI stack installed. SSH to it via `ssh -i ~\.ssh\minti_vm -p 2222 minti@127.0.0.1` (passwordless). Passwordless `sudo` for `minti` user. Shared folder at `/media/sf_minti-repo` = the Windows-side repo (instant edit sync). NAT port-forwards: `127.0.0.1:2222→VM:22`, `127.0.0.1:17780→VM:7780`. |
| **WSL2 Debian 13** | `wsl -d Debian -u root` | Same MINTI stack installed. xrdp desktop attempted but flaky on WSL (known WSL artifact, not a MINTI bug — use the VM for desktop testing). |
| **Go toolchain (host)** | `$env:Path = "C:\Program Files\Go\bin;$env:Path"; go version` | Installed system-wide via winget. 1.26.3. |

### Local LLMs available for delegation (host Ollama on RTX 5090)

Start Ollama on host with `& "C:\Users\aouad\AppData\Local\Programs\Ollama\ollama.exe" serve` (background). Models:

- `deepseek-r1:32b` (18.5 GB) — reasoning, has `<think>` traces
- `gemma4:31b` (18.5 GB) — strong general, also has thinking
- `qwen3.6:latest` (22.3 GB) — strong code, thinking-heavy

**Gotcha:** all three emit thinking traces. For code-gen delegation, set `num_predict ≥ 8000` and/or strip thinking. See `memory/reference_local_llms.md`.

### Build the runtime + run install.sh end-to-end

```powershell
# native build
cd C:\Users\aouad\Documents\CCode\MINT\MINT_wip
$env:Path = "C:\Program Files\Go\bin;$env:Path"
cd runtime-adapter
go build -o minti-runtime.exe .\cmd\minti-runtime

# Linux cross-compile (for the VM)
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -o dist\minti-runtime-linux-amd64 .\cmd\minti-runtime
$env:GOOS = ""; $env:GOARCH = ""

# Inside the VM (via SSH):
ssh -i ~\.ssh\minti_vm -p 2222 minti@127.0.0.1 "sudo bash /media/sf_minti-repo/install/install.sh"
ssh -i ~\.ssh\minti_vm -p 2222 minti@127.0.0.1 "curl -s http://127.0.0.1:7780/minti/health"
```

---

## Recent commits

```
54bb46b  Add MINTI ASCII logo + minti-fetch (neofetch-style status)
46adb9d  Add VM helper scripts (status, guest-additions, chat-test)
e1e8dcf  Add in-VM setup script: MINTI + Claude Code in one shot
fe89bbb  Add WSL Debian desktop-preview script (Xfce-over-xrdp)
f59a3f7  fix(install.sh): add zstd to base packages
f3ea578  fix(install.sh): locate cross-compiled Linux binary in dist/
e148d35  M1 verified: build clean, smoke tests pass
ce02dfd  M1 scaffold: minti-runtime Go source + systemd + install integration
dcab15f  Add .gitattributes to enforce LF endings on shell/yaml/go files
920313f  M0: repo skeleton + Clan Protocol Spec v0.1 + install.sh v0
```

---

## What works right now (verified)

- `install.sh` runs end-to-end on a fresh Debian-family host: detects distro/arch/GPU/network, installs base packages + Ollama, creates `minti` system user, installs `minti-runtime` binary, writes default `/etc/minti/runtime.yaml`, enables + starts the systemd unit. Idempotent.
- `minti-runtime` daemon on `127.0.0.1:7780`:
  - `GET /minti/health` → `{"status":"ok"}`
  - `GET /minti/version`, `GET /minti/capabilities` → static + live capability discovery
  - `POST /v1/chat/completions` (OpenAI shape, streaming + non-streaming) — translates to Ollama, proxies stream chunks back
  - `POST /api/chat` (Ollama-native passthrough)
  - `GET /v1/models`, `GET /api/tags` — model enumeration
- `minti-fetch` (bash) prints a colored ASCII logo + live system + runtime + Clan status (à la neofetch)
- Build is reproducible: `make runtime` (native), `make runtime-linux` (Linux amd64 cross-compile)

---

## Open questions / known issues

| | |
|---|---|
| MCP permission flow across Clan members | Spec'd in `docs/clan-protocol.md` §7; needs implementation in M2 |
| Reasoning-score rubric maintenance | YAML at `/etc/minti/reasoning-scores.yaml`, community-curated update channel in M6 |
| Tensor-parallel inference (model bigger than one node) | v2 — adopt exo or llama.cpp RPC |
| Cross-OS Clan Agent maintenance burden | Single Go binary, CI cross-builds — addressed in M5 |
| xrdp desktop in WSL2 | Known-flaky, not a MINTI issue. Use the VirtualBox VM for desktop dev. |

---

## Files to read for context

| File | What it tells you |
|---|---|
| `STATUS.md` (this file) | Where we are right now |
| `DOCUMENTATION.md` | What got built, in what order, why |
| `README.md` | Public-facing project description |
| `docs/clan-protocol.md` | Wire-level spec for the Clan protocol |
| `runtime-adapter/README.md` | How the runtime daemon works internally |
| `scripts/setup-vm.sh` | The one-shot installer that produced the current VM |
| `~/.claude/plans/hello-can-we-create-abundant-hopper.md` | **The PRD** — the authoritative source of truth |
| `~/.claude/projects/.../memory/*.md` | User profile, build workflow, dev env gotchas |
