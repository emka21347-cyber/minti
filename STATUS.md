# MINTI — Project Status

> **Last updated:** 2026-05-26
> **Purpose:** Read this *first* when opening a new chat or onboarding to the project. It's the single document that tells you where MINTI is right now and how to pick up work without re-reading history.

---

## TL;DR

MINTI is a minimal, AI-agent-first Linux software stack plus a cross-OS **Clan protocol** for distributed local AI compute. **M0–M3 are done and validated** in the `minti-dev` Linux Mint VM. Install path works; `minti-runtime` serves OpenAI + Ollama + **Anthropic** (`/v1/messages`, M3) shapes; 5 stdio MCP tool servers route through a policy-gated framework; `minti-pack-recon` installs the nmap/whois/dig toolchain; **opencode** (sst, MIT) is bundled with a system-wide config that registers minti-runtime as a custom provider and all 5 MCP servers as stdio commands; **Claude Code preset** is documented in `docs/claude-code-preset.md` for users who already have it. **M4 is next**: the Clan daemon `cland` (the iceberg — leader-lease election + HMAC + pinned cert + key rotation + cross-Clan tool routing).

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
  - **Phase 0 done** (commits `dccd7fb` + `577daed`): `docs/clan-protocol.md` bumped to v0.2 with 6 small additive edits surfaced by a local-LLM peer-review pass (qwen3.6 + deepseek-r1:32b + gemma4:31b). Edits: §3.3 12-word BIP39 mnemonic (128-bit, HKDF-expanded to clan_key); §6.4 `recently_failed` definition; §7.1 `target_member` in signed token claims + v1 honesty note; §10 `/v1/messages` + `/clan/peer-add` rows; §12 OQ-2 pulled into v1. Peer-review driver + raw reviews at `scripts/m4-peer-review.py` + `scripts/m4-reviews/`.
  - **Phase A done** (commit `8b1c2fd`): `cland/` module bootstrap — `cmd/minti-cland/main.go` + `internal/{config,identity,state,auditlog}/`. Ed25519 keypair + UUIDv4 member_id persist to `<state_dir>/identity.json` mode 0600; Clan state at `clan.json` (also 0600 — holds `clan_key`) with atomic-rename writes; auditlog schema duplicated verbatim from `mcp-servers/internal/audit` (D-M4.6). All unit tests pass. Binary smoke-tested: first run generates identity, second run loads the same one.
  - **Next:** Phase B — crypto + transport (HTTPS w/ self-signed cert + SPKI pinning; HMAC over `method|path|body|ts|nonce`; LRU-bounded nonce cache `10_000 × N_active_members`; `KeyProvider` interface from day one so Phase H's two-key grace window doesn't force a transport rewrite).
  - Plan: `C:\Users\aouad\.claude\plans\velvet-drifting-codd.md`.

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
then read STATUS.md and the M4 plan at
C:\Users\aouad\.claude\plans\velvet-drifting-codd.md (already approved
2026-05-27 after a local-LLM peer-review pass). M0–M3 are done; M4 is in
flight: Phase 0 (spec edits → clan-protocol v0.2, commits dccd7fb +
577daed) and Phase A (cland module skeleton + identity + state, commit
8b1c2fd) are committed and tested. Pre-flight check the system, then
execute Phase B per the plan — crypto + transport (self-signed clan_cert
+ SPKI pinning + HMAC + LRU-bounded nonce cache + KeyProvider interface).
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
