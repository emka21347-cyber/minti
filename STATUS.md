# MINTI — Project Status

> **Last updated:** 2026-05-26
> **Purpose:** Read this *first* when opening a new chat or onboarding to the project. It's the single document that tells you where MINTI is right now and how to pick up work without re-reading history.

---

## TL;DR

MINTI is a minimal, AI-agent-first Linux software stack plus a cross-OS **Clan protocol** for distributed local AI compute. **M0–M2 are done and validated** in the `minti-dev` Linux Mint VM: install path works, runtime daemon serves OpenAI/Ollama-compatible chat, 5 MCP tool servers (`fs`, `shell`, `recon`, `http`, `pkg`) speak stdio MCP through a policy-gated framework, and `minti-pack-recon` debian metapackage installs the nmap/masscan/whois/dig toolchain. Acceptance smoke: `mcptest mcp-recon nmap_scan target=127.0.0.1` runs nmap with audit-logged consent; the deny path refuses with a policy reason. **M3 is next**: bundle `opencode` and document the Claude Code preset against the same local endpoint.

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

### Next 🟡
- **M3 — Agent client integration** (PRD §8, ~1 week part-time)
  - Bundle `opencode` (sst, MIT) and pre-configure it to talk to `minti-runtime` at `127.0.0.1:7780`.
  - Document Claude Code config preset pointing at the same endpoint (per P1: optional, never bundled).
  - Validate end-to-end: agent client → minti-runtime → ollama; agent client invokes `minti-mcp-recon.nmap_scan` and renders the consent prompt itself (rather than via `mcptest`).

### Future 🔲
- M4: Clan Layer v1 — `cland` daemon (the iceberg, 4 weeks)
- M5: Cross-platform Clan Agent (Windows + macOS)
- M6: Security hardening + remaining packs (webapp, wireless, forensics) + Pack Manager
- M7: Old-hardware smoke test
- M8: v1.0 release

---

## How to resume work

### Single prompt to start the new chat

```
Continuing MINTI build. Read memory at
C:\Users\aouad\.claude\projects\C--Users-aouad-Documents-CCode-MINT-MINT-wip\memory\MEMORY.md
then read PRD at C:\Users\aouad\.claude\plans\hello-can-we-create-abundant-hopper.md
(v0.6) and STATUS.md in the repo root. M0+M1+M2 are done and validated.
We start M3 — bundle opencode + document Claude Code preset. Pre-flight
check the system first (per my "check before you start" rule), then
propose a task breakdown for M3 with explicit T2-direct vs T3-delegable
tagging before writing code.
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
