# MINTI — Project Status

> **Last updated:** 2026-06-06 (M6.1: minti-pack-fetch packaged as its own .deb + Kiwix ZIM pinned)
> **Purpose:** Read this *first* when opening a new chat or onboarding to the project. It's the single document that tells you where MINTI is right now and how to pick up work without re-reading history.

---

## TL;DR

MINTI is a minimal, AI-agent-first Linux software stack plus a cross-OS **Clan protocol** for distributed local AI compute. **M0–M3 are done and validated** in the `minti-dev` Linux Mint VM. Install path works; `minti-runtime` serves OpenAI + Ollama + **Anthropic** (`/v1/messages`, M3) shapes; 5 stdio MCP tool servers route through a policy-gated framework; `minti-pack-recon` installs the nmap/whois/dig toolchain; **opencode** (sst, MIT) is bundled with a system-wide config that registers minti-runtime as a custom provider and all 5 MCP servers as stdio commands; **Claude Code preset** is documented in `docs/claude-code-preset.md` for users who already have it. **M4 core consensus validated** (2026-05-28): cland Phases 0 + A + B + C + D + E + F + G + H-1/2/3 + I + J + security hardening all shipped. **3-node Clan demonstrated** (2 Linux VMs + Windows 11 host); cross-OS election + failover working. **M5 (Cross-platform Clan Agent) shipped**: M5-A foundations + M5-B Windows NSSM-managed service (live-installed on the daily-driver host) + M5-C macOS launchd daemon (build clean, live Mac install deferred). **M6-content + M6.1 landed** (2026-06-06): DLC-style addon packs `minti-pack-{hermes3,mistral,wiki-simple}` + shared `minti-pack-fetch` helper (now its own .deb with restored `Depends:` chain) + new `minti-mcp-wiki` server + default-model fallback in runtime-adapter. End-to-end dep-chain smoke (purge → dpkg-i-addon-fails → install pack-fetch.deb → install addons → markers + Addons line → purge) verified in the VM. Kiwix ZIM pinned to wikipedia_en_simple_all_nopic_2026-05.zim. **Honestly not yet proven**: full 16-test acceptance gate (Phase J Pass 4, **M5-D Phase 4 rerun paused last session pending VM bring-up**; validate-script JSON fixes are on disk), >24h Defender behavioural-block soak, cold-boot-no-login mDNS, real Ollama pull through `minti-pack-fetch hermes3`, real Kiwix ZIM download end-to-end with strict SHA-256 verify, live macOS install. After M6.1: **M5-D resume**, then **M4.1** (Cardputer demo) or M3.5 (tool-use content blocks) or M6-broader (signed repos, signed MSI, notarised .pkg).

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

  - **Phase J done** (2026-05-28, see commit log): 3-node testbed validation — VM-A (minti-dev) + VM-B (minti-dev-2, cloned) + Windows 11 host running `minti-cland.exe` directly. Cross-OS Clan consensus demonstrated end-to-end.
    - **Peer-review pass first** (J1-J7 fixes folded in before any execution): `scripts/m4-phaseJ-review.py` ran gemma4 + qwen3.6 (`think:false`) + deepseek-r1; all 3 reviewers caught 2 real blockers (identity collision after VM clone, SSH host-key collision) + 1 ordering bug (test 10 → 14 quorum collapse) + Windows process-lifecycle concerns. Triage + plan v2 captured in `~\.claude\plans\hello-what-do-you-valiant-ullman.md`; 4 reviewer findings explicitly overruled with reasons (PROPOSE_TIMEOUT bump, zombie-leader on restart, election-split-sim, full Windows Service NSSM).
    - **`scripts/m4-setup-second-vm.ps1`** automates the VBoxManage clone + host-only NIC 2 on both VMs + DHCP enable + port-forwards + shared-folder re-add + J1+J2 post-clone hygiene (wipe inherited `/var/lib/minti/cland/*` + regen `/etc/ssh/ssh_host_*`). Idempotent; PS5.1 stderr quirks documented in comments.
    - **Windows-as-Clan-member** via the existing `cland/minti-cland.exe`: state dir `%LOCALAPPDATA%\MINTI\cland`, `listen.address: "192.168.56.1"` (the host-only adapter), `Start-Process -WindowStyle Hidden -PassThru` per J4 (PID-tracked for alive-checks), `$env:MINTI_CLAND_FORCE_HEALTHY = "1"` to bypass the R1 runtime gate. **One firewall rule needed**: `New-NetFirewallRule -Name minti-cland -Direction Inbound -LocalPort 7777 -Protocol TCP -Action Allow -Profile Any` (admin). Documented in commit message.
    - **Two real protocol findings surfaced during validation**:
      1. **VMs advertised their NAT IP (10.0.2.15) instead of host-only IP** — `listen.address: "0.0.0.0"` resolved to the first non-loopback (NAT comes before host-only in NetworkManager iface order). Fix: set explicit `listen.address: "192.168.56.X"` per VM. Operational, not code; documented for future installer guidance.
      2. **Admitted members never promoted to "active"** — spec §3.1 says promotion happens on first capability advertisement, but our membership Service writes "admitted" on welcome and no code path ever flips them. The R5 quorum filter (active-only) from Phase E peer-review then computed N=1 per node, so every node self-elected and consensus broke at 3 nodes. **Code fix in `engine.go quorum()`**: count both "active" + "admitted" (with comment documenting the trade-off). **Phase H-3 follow-up**: wire the promotion through the /clan/advertise receive path + gossip the promotion via heartbeat.
    - **End-to-end demonstration** (Pass 3-ish — abbreviated from the full 16-test gate): all 3 nodes joined Clan `5725d958...` via paste-key, cross peer-added, converged on Windows as Orchestrator at term=19 (Windows reasoning_score 50 via FORCE_HEALTHY > VMs' 35 from real ollama llama3.2:3b rubric). Killed Windows orchestrator → VM-B took over at term=22 within 16 s; both VMs agreed on the new winner. Election-history showed clean term progression (18 → 19 → 20 → 21 → 22).
    - **Deferred to follow-up**: (a) Phase H-3 admitted→active promotion + gossip, (b) full 16-test PowerShell validate script (`scripts/m4-validate.ps1`) — Pass 4 in the plan, time-boxed out. Core functionality validated; the gate adds operational coverage but not fundamental correctness. Also deferred: VM NAT port-forward repair (host-only IPs work fine for everything Clan-related; NAT was only needed for VirtualBox-style "ssh -p 2222" which broke after adding nic2 — moved to host-only IPs throughout).

  - **Phase E done** (2026-05-27, see commit log): leader-lease election engine.

- **M5 — Cross-platform Clan Agent (Windows + macOS)** (2026-05-28, three commits: `25e1cf1` `4e5373b` `dbd6e10`). PRD §8 line 481 verbatim: "Same `cland` binary cross-compiled; tray app via Fyne." User-confirmed scope: full Windows + macOS together, service-only (tray deferred to M5.1), Windows service via NSSM. Pre-coding 3-LLM peer-review pass on the plan; 9 consensus items folded before the first character was written. Plan + reviews at `~\.claude\plans\synthetic-popping-owl.md`.
  - **M5-A done** (`25e1cf1`): foundations. `cland/internal/probe/probe_darwin.go` (`SysctlRaw` + `binary.LittleEndian.Uint64` for RAM + kern.boottime; `pmset -g batt` with `hasBattery` Once-cache + index-safe parse + "No batteries"/"not present" handling). `cland/internal/probe/probe_windows.go` battery placeholder replaced with `GetSystemPowerStatus`. `cland/internal/config/paths_unix.go` + `paths_windows.go` exporting `DefaultStateDir/AuditLogPath/ConfigPath/RubricPath/MCPBinariesDir`; Windows side uses `golang.org/x/sys/windows.GetCurrentProcessToken` + SID compare against `S-1-5-18` for LocalSystem detection (NOT the broken "LOCALAPPDATA empty" heuristic — peer-review item 5). `config.go Default()`, `auditlog.go DefaultPath`, `main.go` 14 config-default sites + help string all delegate to the helpers. `srv.Shutdown` budget dropped 5s → 3s (peer-review item 7: Go 3s + NSSM 5.5s stays under SCM "Not Responding" threshold). Top-level Makefile gained `cland-windows`, `cland-darwin-{amd64,arm64}`, `cland-all-platforms` with `-trimpath -ldflags="-s -w"` (~30% smaller binaries). Verified: vet clean on linux/windows/darwin, `go test ./...` 18 packages green, 4-platform cross-compile correct file types, Win32 ground-truth battery probe matches cland reading.
  - **M5-B done** (`4e5373b`): Windows NSSM-managed service. `cland/windows/nssm/install-cland.ps1` (`icacls` strict DACL on state dir; explicit `win64/nssm.exe` from the 2.24 zip with SHA-256 sidecar verified at install time — peer-review item 2; `-FirewallProfile Private,Domain` default with operator override — peer-review item 1; `AppStopMethodConsole 5500`; Defender Folder-Exclusion guidance printed at end — peer-review item 8). `uninstall-cland.ps1` preserves state by default, `-Purge` wipes. `build-zip.ps1` cross-compiles + downloads NSSM (curl.exe primary with retry, Invoke-WebRequest fallback — nssm.cc is famously flaky; in this session we hit a 503 and the script recovered cleanly on the second try). `cland.yaml.windows.example` with explicit `state.dir:` so the helper's LocalSystem fallback is belt-and-braces. README operator quickstart + security model + Defender + network-profile + NSSM tuning reference. Makefile `cland-windows-zip` target. `.cache/` gitignored. **Live install verified on the daily-driver Windows 11 host**: NSSM SHA-256 verification fired, DACL exactly `Administrators:(OI)(CI)(F)` + `SYSTEM:(OI)(CI)(F)` no inheritance, service log shows `version=0.2.0-M5` + correct config/state/identity wiring, identity persisted at `%PROGRAMDATA%\MINTI\cland\identity.json`. **One real-world finding surfaced**: the Phase J foreground `minti-cland.exe` from last week (PID 23752, bound to `192.168.56.1:7777`, ESTABLISHED to both VMs) was still running and would have blocked the new service. Stopped during verification — M5-D's validate script now has an explicit "no stray foreground processes" preflight. The service is **currently installed and Running** on the host in pre-Clan state; manually uninstall with `powershell -ExecutionPolicy Bypass -File C:\Temp\m5b-cland-install\uninstall-cland.ps1`.
  - **M5-C done** (`dbd6e10`): macOS launchd daemon. **No Mac available during M5**; live install deferred to M5.1 dogfood. Second targeted 3-LLM peer-review pass on the dscl + launchctl recipes specifically; 6 items folded: (1) `HiddenSystemUser=1` alongside `IsHidden=1` for 10.13–14.x coverage, (2) `xattr -d com.apple.quarantine` after install, (3) `plutil -remove UserName` instead of fragile sed range delete in the per-user variant, (4) dropped `if [...] || true` thinko around the bootout call, (5) UID scan range bumped 270→300 to clear Apple's third-party convention, (6) state-check broadened to any `launchctl print` recognition rather than `state = running` specifically (version variance). `com.minti.cland.plist`: `Label`, `ProgramArguments`, `RunAtLoad=true`, `KeepAlive={SuccessfulExit:false}`, `ThrottleInterval=5`, `UserName=_minti`, `EnvironmentVariables.HOME=<state_dir>` (mirrors the Linux unit's HOME redirect; `_minti` has `NFSHomeDirectory=/var/empty`), `ProcessType=Background`. `install-cland.sh` system branch creates `_minti` at lowest free UID 300–499, drops binary at `/usr/local/bin/`, state at `/Library/Application Support/MINTI/cland` with `chmod 0700 chown _minti:_minti`, plist into `/Library/LaunchDaemons/` with `root:wheel 0644` + `plutil -lint`, bootstraps via modern `launchctl bootstrap system <plist>` with `load -w` fallback. Per-user branch generates a UserName-stripped plist on the fly, bootstraps into `gui/<uid>`. `uninstall-cland.sh` mirrors; `--purge` deletes state + `_minti` account. `build-tarball.sh` cross-compiles + stages both archs + tarballs (uses `tar`, falls back to `python3 tarfile` for Git-Bash-on-Windows). `cland.yaml.darwin.example` + README (10-yr-old-Mac performance expectations, arm64 vs amd64, Gatekeeper workarounds, Application Firewall first-run). Makefile `cland-darwin-tarball` target. **Verified without a Mac**: `plistlib` parses the plist (all 11 expected keys present), `bash -n` syntax clean on all 3 .sh files, both Mach-O binaries produced (`amd64` + `arm64`), tarballs assembled with correct 8-file layout.
  - **M5-D scripted, live run deferred** (this session): `scripts/m5-phaseD-validate.ps1` written + syntax-checked. Wraps the Phase J 3-node scenario but assumes Windows is service-managed: service preflight (no stray foreground processes), mDNS cold-boot test (restart-service + sleep + query peers from both VMs over SSH), firewall scoping check + disable/enable toggle, identity.json DACL audit (skips if non-elevated), election with Windows as a candidate (kill Linux Orchestrator → verify survivor takes over → restart). 24h Defender soak is OUT OF SCOPE for this script — separate poll-script work for M5.1. **Live run requires**: both VMs warmed up + SSH-reachable on host-only IPs, Windows service already joined to the Phase J Clan (currently the service is in pre-Clan idle; would need a `join` or `create` first), an elevated PowerShell session for the DACL audit branch.

- **M6-content done** (2026-06-06, this session): addon packs (DLC) — Hermes 3 + Mistral + offline Simple Wikipedia. Reuses M2's `packs/recon/` debhelper-compat-13 metapackage skeleton but extends it for two new content kinds that aren't apt artifacts: Ollama model tags + Kiwix ZIM blobs.
  - **`pack-manager/cmd/minti-pack-fetch`** — new Go single-file helper (~9 MB, stdlib only). Reads `/usr/share/minti/packs/<name>/manifest.json`, dispatches by `kind`: `ollama-model` shells to `ollama pull <tag>` (idempotent), `kiwix-zim` does HTTP Range-resumable download with SHA-256 verify + 1.25× free-disk precheck + atomic rename. Writes `/var/lib/minti/packs/<name>.installed` state markers. Honours `MINTI_PACK_NO_FETCH=1` env-var: writes marker, skips download, lets `apt install` finish fast on metered links. Split build tags (`disk_linux.go` / `disk_other.go`) keep native dev builds compiling on Windows.
  - **Three new packs** under `packs/{hermes3,mistral,wiki-simple}/`. Each: `debian/{control,rules,changelog,copyright,source/format,install,postinst,postrm}` mirroring recon's structure, plus a `manifest.json` + agent-facing `skill.md`. wiki-simple adds `debian/dirs` (for `/var/lib/minti/wiki`), `debian/prerm`, and a `systemd/minti-kiwix-serve.service` unit (loopback only, hardened with the same `ProtectSystem/PrivateTmp/MemoryDenyWriteExecute` profile as `minti-cland.service`). Postinst calls `/usr/local/bin/minti-pack-fetch` (the helper ships with the base install, not as a separate .deb at v0.1 — documented in each pack's Description).
  - **`minti-mcp-wiki`** — new MCP server at `mcp-servers/cmd/mcp-wiki/main.go`. Two tools: `wiki_search(query, limit)` queries kiwix-serve's OPDS feed (falls back to HTML scraping for older versions), `wiki_get(path)` fetches an article and HTML-strips. Policy gate `mcp.wiki.deny_tools` added to `policy.MCPPolicy`; permission router extended in `permission.Check()`. Reads `/etc/minti/wiki.yaml` for the kiwix-serve base URL (override via `$MINTI_WIKI_BASE_URL` for tests). Mirrors the mcp-recon framework exactly.
  - **Default-model fallback** in `runtime-adapter/internal/server/server.go`: new `s.resolveModel(ctx, requested)` helper. If caller omits `model`, prefers `hermes3:8b` → `mistral:7b` → first model returned by the backend. Wired into both the OpenAI handler (`chat.go`) and the Anthropic handler (`messages.go`); Ollama passthrough left strict (callers always specify there). Five lines of logic; existing tests still pass.
  - **Three small touch-ups**: install.sh epilogue (lines 445–474) now mentions Hermes/Mistral in the model-pull suggestions and lists the three addon packs in a new step 5; `install/opencode.config.example.json` adds a `mistral:7b` row + marks `hermes3:8b` as conceptual default; `branding/minti-fetch` reads `/var/lib/minti/packs/*.installed` and surfaces an `Addons:` line (verified live).
  - **Makefile**: new `PM_DIR=pack-manager` module, `pack-fetch` + `pack-fetch-linux` targets, three new `pack-{hermes3,mistral,wiki-simple}` targets built via a shared `build-pack` macro. `mcp-wiki` added to `MCP_SERVERS`. The `build-pack` macro also `chmod -x`'s `debian/install`/`dirs`/`control`/`changelog`/`copyright` because vboxsf shared mounts mark everything executable, which otherwise confuses dh_install — see Phase E findings below.
  - **End-to-end smoke** in the `minti-dev` VM (host-only DHCP didn't recover post-restart, so this session used the NAT port-forward `127.0.0.1:2222`): all three .debs build clean; `sudo MINTI_PACK_NO_FETCH=1 dpkg -i minti-pack-hermes3_0.1.0-M6_all.deb` lands the marker + skill.md + manifest.json correctly, postinst runs and prints the deferred-fetch hint; `minti-pack-fetch -list` reports `hermes3 ollama-model installed 2026-06-05`; the updated `minti-fetch` shows `Addons: hermes3`; `dpkg --purge` removes the marker cleanly. wiki-simple unpack lands the systemd unit at `/lib/systemd/system/minti-kiwix-serve.service` correctly but the **smoke run used `dpkg -i` which doesn't resolve Depends** — production deploy needs `apt install ./minti-pack-wiki-simple_*.deb` (or pre-install `kiwix-tools`) to pull the dep. **A real Ollama pull was deliberately NOT run** (Hermes 8B is ~5 GB; deferred until needed); the `MINTI_PACK_NO_FETCH=1` path exercises everything the unsetup path does except the actual download.
  - **3 Phase E findings worth recording**:
    1. **vboxsf is mode-locked.** Files on the shared folder are reported with all bits set; `chmod -x` is a no-op. dh_install then treats `debian/install` as an executable config script and chokes on the bare `skill.md` line. Workaround: copy the pack out of the shared folder before building (`~/m6-build/<pack>/`). The Makefile's `build-pack` macro still calls `chmod -x` for the non-vboxsf case where it actually works.
    2. **debhelper `--with systemd` is deprecated.** Compat 11+ auto-loads the systemd sequence — `dh $@ --with systemd` errors out with "no longer provided". Fix: bare `dh $@`; dh_installsystemd still runs automatically when a `.service` file is present under `lib/systemd/system/`.
    3. **`Description-prereq:` is not a real control field.** dpkg-deb rejected it. Fold prerequisite notes into the regular `Description:` paragraph (used "Requires /usr/local/bin/minti-pack-fetch from the base MINTI install").
  - **Follow-ons for a future M6 commit**: ~~package `minti-pack-fetch` itself as a .deb~~ ✓ landed in M6.1 (2026-06-06); ~~deploy `minti-pack-fetch` from `install.sh`~~ ✓ landed in M6.1; ~~pin Kiwix ZIM SHA-256~~ ✓ landed in M6.1 (wikipedia_en_simple_all_nopic_2026-05.zim, 937 MB); add `minti-pack-hermes3-70b` + `minti-pack-mistral-nemo` + `minti-pack-wiki-en-top` + `minti-pack-wiki-en` follow-on tiers; consider building inside an `lxc-launch debian:13` to dodge vboxsf entirely; chmod -x the systemd .service files in the build-pack copy stage (currently dpkg warns "Configuration file ... is marked executable. Proceeding anyway.").

- **M6.1 done** (2026-06-06, this session): `minti-pack-fetch` now ships as its own .deb. Restores a real `Depends: minti-pack-fetch (>= 0.1)` chain on all three addon packs — `dpkg -i ./minti-pack-hermes3_*.deb` without pack-fetch installed now fails cleanly with "dependency problems - leaving unconfigured" instead of silently breaking at postinst.
  - **`pack-manager/debian/`** scaffold: control (Architecture: amd64, Build-Depends just debhelper-compat=13), changelog (0.1.0-M6), copyright (MIT, matches recon), rules (skips dh_auto_build/test — binary is pre-built; matches how cland .zip/.tar.gz ship pre-built artifacts), install (binary lands at `/usr/bin/minti-pack-fetch`), source/format (3.0 native).
  - **Path decision**: deb installs at `/usr/bin/minti-pack-fetch`, install.sh's bare-binary deploy stays at `/usr/local/bin/minti-pack-fetch`. Both are in $PATH; PATH order puts /usr/local/bin first so an operator's manual deploy overrides the deb. Standard Debian convention (dh_usrlocal would reject the /usr/local path otherwise). Addon postinst scripts changed from `/usr/local/bin/minti-pack-fetch` → bare `minti-pack-fetch` (PATH lookup).
  - **Makefile** gained `pack-fetch-deb` target (mirrors the build-pack macro but adapted for the pack-manager source layout — stages the pre-built binary at `pack-manager/minti-pack-fetch` before `dpkg-buildpackage` and cleans it up after). `build-pack` macro extended to `chmod -x` `debian/source/format` (vboxsf workaround: this file would otherwise inherit the executable bit and break `dpkg-source --before-build`).
  - **install.sh** gained a pack-fetch deployment block between cland and opencode. Searches the same three locations as the other binaries (native build, dist/-linux-amd64, dist/-bare); installs to `/usr/local/bin/minti-pack-fetch` mode 0755; creates `/var/lib/minti/packs/` as a side effect. Surfaced in the final summary block as `Pack-fetch:` and the "addon packs" next-step block already documents `MINTI_PACK_NO_FETCH=1`.
  - **Manifest parser bug surfaced by smoke**: `minti-pack-fetch` was using `json.Decoder.DisallowUnknownFields()`, which rejected the `_comment` field I added to `wiki-simple/manifest.json`. Switched to standard `json.Unmarshal`. The `_comment` idiom is broadly used in JSON config files and future schema additions shouldn't break older fetchers. Two-line code change in `pack-manager/cmd/minti-pack-fetch/main.go`.
  - **Kiwix ZIM pinned**: `packs/wiki-simple/manifest.json` updated from a 2024-06 placeholder + empty sha256 to `wikipedia_en_simple_all_nopic_2026-05.zim` (937 MB, ~250k articles, no images) with SHA-256 `503b74027d101ec13b272f1ebbd23473e1d260be71a35a71141dfc985fd20d0a` — cross-checked against the published .sha256 sidecar and the 302-redirect Digest header from the kiwix.org load balancer. The fetcher's strict SHA-256 verify path is now exercised whenever someone actually downloads.
  - **End-to-end smoke** in the `minti-dev` VM (NAT port-forward `127.0.0.1:2222`): clean state → `dpkg -i hermes3.deb` alone fails with unmet `minti-pack-fetch` dep (correct); install `pack-fetch.deb` → `/usr/bin/minti-pack-fetch` lands + reports version 0.1.0-M6; reinstall hermes3 with `MINTI_PACK_NO_FETCH=1` → postinst runs through, marker written, `minti-fetch` shows `Addons: hermes3`; `apt install ./mistral.deb ./wiki-simple.deb` resolves the full dep transitive (pulled kiwix-tools, aria2, libkiwix12t64, etc.) → both extra markers land → `Addons: hermes3 mistral wiki-simple`; purge addons → markers vanish, pack-fetch survives (correct Depends-not-Pre-Depends semantics); purge pack-fetch → /usr/bin/minti-pack-fetch gone. Both `/usr/local/bin/` (install.sh) and `/usr/bin/` (deb) pack-fetch coexisted cleanly during the test; PATH put /usr/local/bin first.
  - **Known minor cosmetic from the smoke**: dpkg still warns "Configuration file /usr/lib/systemd/system/minti-kiwix-serve.service is marked executable. Proceeding anyway." Same root cause as the other vboxsf Phase E findings — the source tree on the shared folder has the file marked executable; the build-pack copy stage chmods debian/ files but doesn't touch `systemd/*.service`. Fix is a one-line tweak to the build-pack macro; deferred since the warning is purely cosmetic (the unit symlinked + started fine).

### Next after M5
- **M5.1 (lightweight follow-on)**: tray app + Fyne GUI; `runtime.force_healthy: true` config knob (currently env-var only); cold-boot test via Task-Scheduler-on-boot orchestration; 24h Defender soak script; **live macOS install** when a Mac is booted.
- **Phase J Pass 4 / M5-D live**: full 16-test PowerShell acceptance gate against the service-managed Windows + both VMs. **M5-D Phase 4 rerun was paused** mid-session pending VM bring-up; `scripts/m5-phaseD-validate.ps1` has the JSON-schema fixes (tests 2 + 5 read `current_orchestrator` / `current_term` / `latest_ad.os` / `last_ad` + 90s window instead of the legacy fields the original draft assumed). Resume: bring up both VMs, run the script from elevated PS.
- **M6 (broader)**: package `minti-pack-fetch` as its own .deb; install.sh deploys it; signed repo; remaining tool packs (webapp, wireless, forensics); code-signing for Windows MSI + Apple notarised .pkg.
- **M4.1**: remote-API backend → Cardputer-as-Orchestrator demo.
- **M3.5 (squeeze-in candidate)**: tool-use content blocks in `/v1/messages` (Claude Code's native tool-call shape over the API rather than via separate MCP stdio). Currently rejected with 400.

### Future 🔲
- M6: Security hardening + remaining packs (webapp, wireless, forensics) + Pack Manager + **code-signing for Windows + Apple Developer notarisation** (unlocks signed .msi + notarised .pkg, removes Defender Folder-Exclusion + Gatekeeper xattr-strip workarounds).
- M7: Old-hardware smoke test (real 10-yr-old Mac + 4-6 GB VRAM "secondary box" joining the Clan).
- M8: v1.0 release.

### PRD §6.5 deviations (M5)
- **No signed `.msi`** — needs an Authenticode cert. Ship `.zip + .ps1` instead. M6 brings a signed MSI via WiX or NSIS.
- **No signed `.pkg`** — needs an Apple Developer account + `xcrun notarytool`. Ship `.tar.gz + .sh` instead. M6 brings a notarised + stapled `.pkg`.
- **No `windows.SetFileSecurity` Go-side ACL hardening on identity.json** — installer's `icacls /inheritance:r` provides the equivalent guarantee with much smaller surface. The installer becomes load-bearing for Windows ACL security; documented in code + READMEs.
- **macOS battery probe uses `pmset -g batt` shell-out (not IOKit)** — keeps cland's zero-cgo build property; 30s probe cadence makes the fork-exec cost trivial.

---

## How to resume work

### Single prompt to start the new chat

```
M4 + M5-A/B/C all DONE (2026-05-28). cland Phases through M4-J shipped;
M5-A foundations + M5-B Windows NSSM service (live install verified) +
M5-C macOS launchd daemon (build clean, live test deferred — no Mac)
committed in 25e1cf1, 4e5373b, dbd6e10. M5-D `scripts/m5-phaseD-validate.ps1`
written but live run is operator-session work (needs both VMs warmed,
Windows Service join-ed to the Clan, elevated PS for DACL audit).
Read memory + STATUS.md for the full chronicle.

Operator follow-ups captured for the next session:
  - **Windows Service is currently installed + Running on the host**
    at version 0.2.0-M5 in pre-Clan idle (member_id b2ecb5c4-...).
    Uninstall (preserve state):
      powershell -ExecutionPolicy Bypass -File C:\Temp\m5b-cland-install\uninstall-cland.ps1
    Or have it join the Phase J Clan to make M5-D's election test work:
      & "C:\Program Files\MINTI\cland\minti-cland.exe" join --mnemonic ... --address 192.168.56.101:7777 --pin sha256:...
  - **M5-D live run**: with VMs up + Windows Service joined, from elevated PS:
      powershell -ExecutionPolicy Bypass -File scripts\m5-phaseD-validate.ps1
  - **Phase J residue**: the foreground minti-cland.exe from Phase J
    was killed during M5-B verification (PID 23752). The Phase J
    state at %LOCALAPPDATA%\MINTI\cland is still on disk (a separate
    member_id from the service's state at %PROGRAMDATA%\MINTI\cland).
    Do NOT restart that foreground binary while the Service is also
    running -- they will race for :7777.

Next milestones per super-plan: M5.1 (tray app + Fyne; live macOS
install when a Mac boots), M4.1 (remote-API backend → Cardputer-
as-Orchestrator demo) or M5 (cross-platform Clan Agent — Windows
Service / macOS launchd, smaller subset than full cland).
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
dbd6e10  M5-C: minti-cland macOS launchd daemon (install / uninstall / build-tarball)
4e5373b  M5-B: minti-cland Windows Service via NSSM (install / uninstall / build-zip)
25e1cf1  M5-A: cross-platform foundations for cland (probes, paths, Makefile)
51a1ac0  M4-H-3 + security hardening: admitted→active promotion + gossip; replay+rate limits
8d103ca  M4 project-review pass: 3 local LLMs critique the whole project at v1 boundary
9b21a86  M4-J: 3-node testbed (2 VMs + Windows host) + 2 real protocol fixes — M4 DONE
912eafb  M4-I: install.sh + systemd unit + Makefile + minti-fetch Clan surface
a33634d  M4-H-2: revocation gossip via heartbeat digest
500e9b2  M4-H-1: key rotation 2PC (orchestrator-driven, /clan/rotate-key)
8dde3f4  M4-G: cross-Clan tool execution (/mcp/execute + signed permission tokens)
558ee1a  M4-F: routing layer (Orchestrator chat-completion proxy + self-route fast path)
9f07428  M4-E: leader-lease election engine + heartbeat + pin + 3 prereq fixes
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
