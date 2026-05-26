# MINTI — Documentation

> **What this is:** A chronicle of what got built in MINTI, in what order, why each decision was made, and how. Read [STATUS.md](STATUS.md) first if you just need to know where we are right now. Read the **PRD** at `~/.claude/plans/hello-can-we-create-abundant-hopper.md` (v0.6) for the authoritative spec.

---

## 1. Project overview

MINTI is two things in one:

1. **A minimal Linux software layer** that turns any Debian-family host (Debian 12+, Linux Mint 21+, Ubuntu 22.04+) into a MINTI node. v1 ships as an installer script + signed apt repo; v1.5 will add a bootable ISO.
2. **The Clan protocol** — a cross-OS trust group that lets MINTI nodes plus Windows / macOS Clan Agents share AI inference, agent work, and tool execution. Membership is consensual. The Orchestrator is the strongest *reasoner* (not the strongest hardware), elected dynamically by a leader-lease + heartbeat protocol. A Cardputer with a Claude Opus API key can outrank a 4090 running Mistral 7B — because reasoning quality drives the choice, not raw silicon.

The motivating story: any device that can speak the Clan protocol becomes useful — even a 1-2 GB-RAM machine from 2010 contributes as a `tools` node or `inference` worker for tiny models.

### Three inviolate principles (PRD §2a)

- **P1. Open-source-first.** Every default is open source. Proprietary services are optional integrations users can bring with their own credentials — never bundled, never required.
- **P2. Resurrection of dead hardware.** The stack remains installable and useful on 1–2 GB RAM machines with decade-old CPUs.
- **P3. PRD wins over clever code.** Update the PRD first, then the code.

---

## 2. Architecture summary

(See PRD §5 for the authoritative version; this is a quick read.)

```
┌─────────────────────────────────────────────────────────────┐
│                  MINTI Node (any Debian-family host)         │
│                                                             │
│  Agent Layer:    opencode (bundled), OpenHands (optional),  │
│                   Claude Code (documented preset)            │
│  Clan Agent:     cland — discovery + membership + election   │
│                   + routing (M4 deliverable; not yet built)  │
│  AI Runtime:     minti-runtime → Ollama (or llama.cpp /      │
│                   LocalAI / remote-api backends)             │
│  Tool Packs:     opt-in apt metapackages — recon, webapp,    │
│                   wireless, forensics                        │
│  Base OS:        Debian / Mint / Ubuntu (unmodified) + the   │
│                   MINTI software layer on top                │
└─────────────────────────────────────────────────────────────┘

   LAN (mDNS discovery + HTTPS w/ pinned cert + HMAC auth)
   ┌──────────┬──────────┬──────────┬──────────┐
   ▼          ▼          ▼          ▼          ▼
 [GPU box] [old Mac]  [Windows]  [Cardputer] [...]   = a Clan
```

The runtime exposes the **OpenAI-compatible** (`/v1/chat/completions`) and **Ollama-compatible** (`/api/chat`) HTTP APIs on `localhost:7780` — both open standards. Per P1, Anthropic-API-compatibility is supported as an *optional* alternate shape, not a centerpiece.

---

## 3. Build history

### 3.1 PRD evolution (v0.1 → v0.6)

The PRD was iterated through six versions over multiple working sessions:

| Version | Date | What changed |
|---|---|---|
| v0.1 | 2026-05-21 | Initial scope: minimal Mint-like distro with Kali tools and Ollama agents on a single node. |
| v0.2 | 2026-05-21 | Introduced the **Clan model** + cross-platform Clan Agent. Orchestrator role is dynamic, not fixed to one machine. |
| v0.3 | 2026-05-21 | **Orchestrator = best reasoner, not biggest machine.** Cardputer-with-Opus can win over 4090-with-Mistral. Remote-API backends made first-class. `opencode` + Claude Code committed as bundled clients. Threat model added. |
| v0.4 | 2026-05-21 | After DeepSeek-R1 peer review: security hardening bundle promoted to v1 must-have (key rotation, signed packs, audit log). Distributed inference for models bigger than any single member (Hermes 3 405B) added as v2 headline. |
| v0.5 | 2026-05-21 | After Gemma4 + Qwen3.6 peer review + user clarifications: **install path pivoted from ISO to script + apt repo** (ISO becomes v1.5). Consensus upgraded to leader-lease + 2s heartbeat. Two-score model (`reasoning_score` vs `system_score`). Build workflow §13 added (three-tier delegation). |
| v0.6 | 2026-05-22 | **Open-source-first explicitly enshrined as P1.** OpenHands added as first-class optional client. Claude Code demoted to documented-optional. Reasoning-score table reordered to lead with open-weight local models; remote APIs are a separate optional sub-table. Anthropic-compat endpoint demoted from headline to "one of several shapes the `remote-api` backend can serve." |

Three local LLMs (DeepSeek-R1 32B, Gemma 4 31B, Qwen 3.6) acted as peer reviewers between versions and caught real issues (insufficient key rotation, missing signed-packs requirement, gossip-only election vulnerability, scope inflation, Wayland-vs-X11 detail).

### 3.2 M0 — Repo + Clan Protocol Spec + install.sh v0

**Commits:** `920313f`, `dcab15f`

**Created:**

- Repository skeleton at `C:\Users\aouad\Documents\CCode\MINT\MINT_wip`. Top-level dirs per PRD §11: `cland/`, `runtime-adapter/`, `mcp-servers/{fs,shell,recon,http,pkg}/`, `console/`, `pack-manager/`, `packs/{recon,webapp,wireless,forensics}/`, `branding/`, `docs/`, `install/`, `ansible/`, `scripts/`, `tests/`.
- `docs/clan-protocol.md` — wire-level spec for the Clan protocol. 12 sections covering identity, membership state machine, mDNS discovery, capability advertisement, leader-lease election (terms, hysteresis, user-pin override), routing, MCP permission flow across nodes, key rotation, member revocation, audit log format, and the full HTTPS endpoint reference.
- `install/install.sh` v0 — idempotent bash installer. Preflight: root check, distro detection (debian/ubuntu/linuxmint/pop), arch check (x86_64), network reachability. Installs base packages, detects GPU informationally, installs Ollama via the official upstream script, creates `/etc/minti/` + `/var/lib/minti/`, prints a "next steps" banner.
- `README.md`, `LICENSE` (MIT), `.gitignore` (secrets-aware), `.gitattributes` (LF enforcement for shell/yaml/go), `Makefile` (stub targets).

**Why:** The Clan protocol spec needed to exist before the daemon. install.sh needed to exist before we could test on real Linux. The repo skeleton avoids "where does this file go" decisions during implementation.

### 3.3 M1 — Per-node AI Runtime Adapter

**Commits:** `ce02dfd`, `e148d35`

**Created:** `runtime-adapter/` — a Go daemon that presents a uniform AI runtime interface to the rest of the system.

**Layout:**

```
runtime-adapter/
  go.mod                                       go 1.22 + gopkg.in/yaml.v3
  cmd/minti-runtime/main.go                    entry, flag parsing, graceful shutdown
  internal/backend/backend.go                  Backend interface + shared types
  internal/backend/ollama.go                   Ollama HTTP proxy + streaming translation
  internal/backend/stubs.go                    llamacpp/localai/remote-api stubs (501)
  internal/config/config.go                    YAML loader + Default() + NewBackend()
  internal/server/server.go                    router, /minti/health, /minti/capabilities,
                                                /v1/models, /api/tags
  internal/server/chat.go                      /v1/chat/completions (OpenAI + SSE),
                                                /api/chat (Ollama + NDJSON)
  configs/runtime.yaml.example                 documented defaults
  systemd/minti-runtime.service                hardened unit
  README.md
```

**Key design decisions:**

- **Backend abstraction**: `Backend` interface with `Kind()`, `Health()`, `Capabilities()`, `Chat()`, `ChatStream()`. Concrete backends implement it. The rest of the system never depends on a specific runtime.
- **Two HTTP surfaces**: OpenAI-compatible (`/v1/chat/completions`, `/v1/models`) and Ollama-compatible (`/api/chat`, `/api/tags`). Both translate to the same internal types. Per P1, both are open standards — no Anthropic shape in v1.
- **Streaming**: SSE for OpenAI, NDJSON for Ollama-native. Internal `StreamChunk` type unifies them; backend writes chunks to a `StreamWriter`.
- **systemd unit hardening**: `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `IPAddressDeny=any` + `IPAddressAllow=localhost`, `NoNewPrivileges=true`, `MemoryDenyWriteExecute=true`. Per the principle that v1 binds to localhost only.

**Smoke-test results (against deepseek-r1:32b on host):**

| Endpoint | Result |
|---|---|
| `/minti/health` | 200 `{"status":"ok"}` |
| `/minti/version` | 200 `{"runtime":"minti-runtime","version":"0.1.0-M1"}` |
| `/minti/capabilities` | 200 — correctly enumerated 3 resident models |
| `/v1/models` | 200 — OpenAI-shaped, valid |
| `/v1/chat/completions` non-streaming | 200 — valid envelope, usage stats |
| `/v1/chat/completions` SSE | 200 — 330 chunks, 4.6 s TTFB, `[DONE]` terminator |
| `/api/chat` NDJSON | 200 — 5 chunks (warm), 1.4 s, reason=stop |

### 3.4 Validation pass — real Debian 13 in WSL2

**Commits:** `f3ea578` (cross-compile binary discovery), `f59a3f7` (zstd dependency)

The runtime + install script worked on Windows. Real validation needed a real Linux. We brought up WSL2 with Debian 13 ("trixie") and ran `install.sh` end-to-end. **Two bugs caught:**

1. **Cross-compiled Linux binary wasn't found by `install.sh`.** Native Go build dropped `runtime-adapter/minti-runtime`; cross-compile dropped `runtime-adapter/dist/minti-runtime-linux-amd64`. install.sh only checked the native path → silent skip on WSL. **Fix:** probe both locations + report which was picked. (`f3ea578`)

2. **Missing `zstd` dependency.** Ollama 0.24's release tarball is `.tar.zst` and aborts on machines without zstd. Debian 13 doesn't ship zstd by default. **Fix:** add `zstd` to base packages in install.sh. (`f59a3f7`)

After fixes, the full install + chat path verified end-to-end on Debian 13:

```
[MINTI] Detected: Debian GNU/Linux 13 (trixie) (x86_64)
[MINTI] GPU: NVIDIA GeForce RTX 5090, 32607 MiB   ← WSL2 dxgkrnl passthrough exposed the 5090 to the Linux side
[MINTI] Ollama installed: ollama version 0.24.0
[MINTI] minti-runtime running on 127.0.0.1:7780
$ ollama pull llama3.2:3b
$ curl … /v1/chat/completions → "PONG" (proxy works end-to-end on real systemd, real Linux, real model)
```

**WSL desktop preview attempt.** Tried to set up Xfce over xrdp in WSL so the user could *see* the MINTI desktop look (PRD §6.1). Resolved five real issues in sequence (security_layer, Xwrapper, X11 socket RO mount, Wayland→X11 backend, light-locker session-killer) before hitting an irreducible xrdp↔Xorg transport instability on WSL. **Conclusion: not a MINTI bug; xrdp-on-WSL is famously flaky. For desktop testing, use a real VM.** The setup is captured in `scripts/wsl-desktop-preview.sh` for anyone who wants to retry. Commit `fe89bbb`.

### 3.5 Dev environment — Linux Mint Xfce VM in VirtualBox

**Commits:** `e1e8dcf` (in-VM setup script), `46adb9d` (helper scripts)

**Why a VM:** The WSL desktop attempt hit known WSL limitations. A real VM with a real Linux ISO is the canonical desktop test bed and doubles as the v1.5 ISO test target.

**What was built:**

- VirtualBox VM `minti-dev`: 8 GB RAM, 4 vCPUs, 40 GB dynamic disk, NAT with port-forwards `127.0.0.1:2222 → :22` (SSH) and `127.0.0.1:17780 → :7780` (minti-runtime), shared folder pointing at the Windows-side MINTI repo (auto-mounts at `/media/sf_minti-repo` after Guest Additions install).
- Linux Mint 22.3 Xfce ("Zena") installed inside.
- VirtualBox Guest Additions installed (kernel modules auto-loaded, vboxsf group, user added, shared folder auto-mounts at boot).
- Passwordless SSH key-based auth set up: `~\.ssh\minti_vm` on the host has Full-Control-by-current-user-only ACL; corresponding public key in the VM's `~/.ssh/authorized_keys`. Passwordless sudo enabled for the `minti` user via `/etc/sudoers.d/minti-nopasswd`.
- `scripts/setup-vm.sh` — one-shot installer that runs inside the VM and: installs base packages, runs `install.sh`, installs Claude Code via Anthropic's official apt repo (documented per P1 as optional/proprietary, not bundled), pulls `llama3.2:3b`, sets a default git identity.

**Verified after setup:** minti-runtime.service active, ollama.service active, llama3.2:3b chats end-to-end ("PONG" via curl in 8 s on CPU).

### 3.6 Branding — ASCII logo + minti-fetch

**Commit:** `54bb46b`

A small mesh-of-nodes ASCII logo (capturing the *Clan* visual metaphor, not a borrowed-from-Mint leaf) plus a neofetch-style status command:

```
       ●───●───●          minti@minti-VirtualBox
      ╱ │   │   ╲         ────────────────────────────
     ●──┼───●───┼──●      OS:        MINTI 0.1.0-M1 (Linux Mint 22.3)
     │  │   ║   │  │      Kernel:    6.17.0-29-generic x86_64
     ●──┼───●───┼──●      Uptime:    13 hours, 24 minutes
      ╲ │   │   ╱         CPU:       AMD Ryzen 9 9950X3D
       ●───●───●          GPU:       none / CPU
                          RAM:       1.3G / 8.3G
       M I N T I          Runtime:   active (0.1.0-M1) (ollama)
       ─────────          Models:    llama3.2:3b
       Open agents.       Clan:      not joined  role=standalone
       Open weights.      Orch:      -
       Clan-aware.
```

`branding/minti-fetch` is pure bash, no external deps beyond curl/sed/awk/wc. Reads live state from `/proc`, `/etc/os-release`, `/etc/minti/version`, and the local minti-runtime endpoints. Honors `NO_COLOR`. Properly UTF-8 aligned (`wc -m` for character count, not byte count — multi-byte box-drawing glyphs render correctly).

`install.sh` was updated to copy the script to `/usr/local/bin/minti-fetch` and the logo to `/etc/minti/branding/`.

### 3.7 M2 — MCP servers + `minti-pack-recon`

**Created:** `mcp-servers/` Go module (`github.com/minti/mcp-servers`, Go 1.25 — the official MCP SDK pulled in transitive deps that bumped past the runtime-adapter's 1.22 baseline), plus the first debian tool pack.

**Layout:**

```
mcp-servers/
  go.mod / go.sum                      # imports github.com/modelcontextprotocol/go-sdk
  cmd/
    mcp-fs/main.go                     # read_text, write_text, list_dir, glob (home-jailed)
    mcp-shell/main.go                  # exec via /bin/sh -c with timeout + policy modes
    mcp-recon/main.go                  # nmap_scan, whois, dig_lookup, http_probe
    mcp-pkg/main.go                    # apt-cache search, apt-get install (sudo-gated)
    mcp-http/main.go                   # fetch_url, head_url (size-capped)
    mcptest/main.go                    # stdio test client w/ TTY consent prompt
  internal/
    policy/       loads /etc/minti/policy.yaml + ~/.minti/policy.yaml overlay
    audit/        appends one JSON event per line to ~/.minti/audit.jsonl
    permission/   Check(server, tool, args) → Allow|Deny with reason
    mcpserve/     wraps the MCP SDK so every AddTool routes through policy + audit
    proc/         shared subprocess-exec helper (stdout/stderr/exit-code capture)
  configs/policy.yaml.example          # documented defaults

packs/recon/
  debian/control                       # Depends: nmap masscan whois dnsutils|bind9-dnsutils
                                       # Recommends: theharvester, amass
                                       # Suggests: rustscan, golang-go
  debian/changelog                     # 0.1.0-M2
  debian/copyright                     # DEP-5 / MIT
  debian/rules                         # dh sequence, no binaries built
  debian/install                       # skill.md → /usr/share/minti/packs/recon/
  debian/source/format                 # 3.0 (native)
  skill.md                             # agent-facing tool catalog + safety rules
  README.md
```

**Key design decisions:**

- **Official SDK over hand-rolling.** Use `github.com/modelcontextprotocol/go-sdk` v1.6.1 (MIT). Wrapped behind `internal/mcpserve` so an SDK swap stays a single-file change. Spec compliance + future revisions come for free.
- **Subprocess, not daemon.** Per MCP convention each `minti-mcp-*` is a short-lived stdio child of the agent client (`opencode` in M3, `mcptest` now). No systemd units — just binaries staged at `/opt/minti/mcp/`.
- **Server-side enforcement + host-side consent.** Policy denies are enforced in the server (defense in depth). Approval prompts are rendered by the host (`mcptest --yes` skips them in automation). Cross-Clan signed permission tokens (`docs/clan-protocol.md` §7.1) are stubbed (`permission.VerifyCrossClanToken` always rejects until M4).
- **Two-file policy overlay.** `/etc/minti/policy.yaml` (system defaults from `install.sh`) + `~/.minti/policy.yaml` (per-user override). User fields fully replace system fields — slice merging is a future-work refinement.
- **Defense-in-depth at fs handlers.** `safePath()` resolves the input, expands symlinks, and re-verifies the result is under `$HOME` before any IO — independent of the policy layer.
- **Input validation via strict regex before exec.** mcp-recon and mcp-pkg validate target/domain/package names with conservative regexes; commands always go through `exec.Command(bin, args...)` (no shell), so even a malformed input that bypasses the regex can't introduce shell metas.
- **PRD §13 delegation: T2 for the security path, T3 reserved for genuinely declarative work.** All security-critical code (policy, audit, permission, mcpserve, recon/shell/pkg handlers) is Tier 2 (Claude direct). T3 candidates from the plan (mcp-http handler, debian metadata files) were re-evaluated against the local-LLM gotcha in `memory/reference_local_llms.md` ("simple boilerplate: just write it as Tier 2 — the round-trip cost exceeds direct authorship") and authored T2 instead. Cost-report at milestone close documents this honestly.

**Validation (in the `minti-dev` VM):**

```
=== mcptest mcp-recon nmap_scan target=127.0.0.1 ===
{"command":"nmap -sV -T3 --top-ports 1000 127.0.0.1",
 "output":"Starting Nmap 7.94SVN ...
  PORT    STATE SERVICE VERSION
  22/tcp  open  ssh     OpenSSH 9.6p1 Ubuntu 3ubuntu13.16
  631/tcp open  ipp     CUPS 2.4
  ..."}

=== Deny path (~/.minti/policy.yaml sets mcp.recon.deny_tools: [nmap_scan]) ===
policy denied: tool 'nmap_scan' is on recon.deny_tools
(exit 4 — IsError)

=== ~/.minti/audit.jsonl ===
{"ts":"...","server":"minti-mcp-recon","tool":"nmap_scan","decision":"allow","duration_ms":6190}
{"ts":"...","server":"minti-mcp-recon","tool":"nmap_scan","decision":"deny","reason":"tool 'nmap_scan' is on recon.deny_tools"}
```

**Other M2 work:**

- `install.sh` extended to stage MCP binaries to `/opt/minti/mcp/`, install `mcptest` to `/usr/local/bin/`, install `/etc/minti/policy.yaml` (preserve existing), and prepare `~/.minti/` for the invoking user (via `SUDO_USER`).
- `Makefile` gained `mcp`, `mcp-linux`, `pack-recon`, `sign-recon` targets. `make all` now builds the runtime AND the 5 MCP servers + mcptest.
- `docs/clan-protocol.md` §7.2 extended with the `deny_tools` per-namespace kill switch, `allow_raw_socket` recon capability gate, `http.max_body_bytes`, and the system+user policy overlay convention.
- `scripts/m2-validate.sh` — reproducible in-VM acceptance script (stages source out of the shared folder to escape vboxsf's exec-bit defaults, installs deps individually so a single missing optional doesn't block the rest, runs both the allow and deny paths, restores any user-policy override on exit).

---

## 4. Decision log

Major architectural decisions, in chronological order, with the reasoning at the time.

| # | Decision | Why |
|---|---|---|
| D1 | Base distro: **Debian 12+ minimal** (not Mint, despite the MINTI name) | Closest to Kali's lineage for clean tool fusion; lighter; well-trodden toolchain. MINTI adopts the *spirit* of Mint (ease + GUI installer) but is its own brand. |
| D2 | DE: **Xfce default, LXQt alt** | Both under 300 MB idle; Xfce familiar, LXQt lighter for resurrected hardware. |
| D3 | Installer: **Calamares (v1.5 ISO only)** | v1 uses script + apt repo; ISO is deferred to v1.5. Avoids 4 GB ISO toolchain debt during M0-M4. |
| D4 | Per-node AI runtime: **Ollama default, llama.cpp / LocalAI / remote-api as alt backends, all behind a uniform `minti-runtime` adapter** | Ollama has the best cross-platform installer story (matters for Win/Mac Clan Agents). Adapter isolates the choice. |
| D5 | Bundled agent clients: **`opencode` default + OpenHands optional** | Both MIT, both speak OpenAI-compat. Claude Code is a documented optional preset, not bundled (P1). |
| D6 | Hardware (ISO): **x86_64 only in v1**, NVIDIA opt-in | Matches user's hardware + most contributors. ARM/Apple Silicon are v2+. |
| D7 | Tool delivery: **Modular apt metapackages** (`minti-pack-recon`, `-webapp`, `-wireless`, `-forensics`) | Mirrors Kali's `kali-linux-*` pattern but lighter. Each pack ships an MCP "skill" file teaching the agent how to use it safely. |
| D8 | Clan transport (v1): **mDNS for discovery + HTTPS w/ pinned cert + HMAC for traffic, LAN-only** | Simple, secure enough for trusted LAN, no external CA needed. WireGuard mesh deferred to v2. |
| D9 | Clan scheduling (v1): **Capability-typed whole-request routing by the Orchestrator** | Tensor-parallel split (running a model bigger than one member) is v2's exo-class work. |
| D10 | Election (v1): **Leader-lease + 2 s heartbeat / 8 s expiry, with user-pin absolute override** | Raft-lite. Real distributed-systems primitive, sounder than the gossip-only design we initially had (peer-review caught this). |
| D11 | Cross-platform Clan Agent: **Single Go binary**, cross-compiled to Linux/Win/Mac | One source. Cardputer/ESP32 micro-agent is stretch. |
| D12 | Reproducibility: **`live-build` + Ansible + git** for ISO (v1.5); same repo for cross-platform Agent | Single source of truth. |
| D13 | Remote-API bridge: **First-class capability**, opt-in per node | Enables the Cardputer-with-Opus-key Orchestrator scenario. API keys encrypted at rest with the Clan Key. |
| D14 | Two-score model: **`reasoning_score` (per backend) + `system_score` (per node)** | Reasoning routing and worker routing never get confused. Cardputer-with-Opus has reasoning_score 95 / system_score 2; the 5090 has reasoning_score 72 / system_score 92. |
| D15 | Install path: **Script + apt repo in v1; ISO in v1.5** | Removes ISO toolchain from the critical path. "Install on dead Mac" becomes "install Linux Mint first (5 min), then run our script (2 min)." |

---

## 5. Bug log

Issues caught + fixed during M0–M1 + validation.

| # | Found in | Issue | Fix | Commit |
|---|---|---|---|---|
| B1 | Path C (Git Bash dry-run of install.sh) | `install.sh` only probed `runtime-adapter/minti-runtime` for the runtime binary, missing the Windows cross-compile output at `dist/minti-runtime-linux-amd64`. Would silently skip the runtime install on WSL/VM. | Probe both locations; pick the first found; print which path was used. | `f3ea578` |
| B2 | Path A (real Debian 13 install) | Ollama 0.24's installer aborts on missing `zstd` (it's the tarball compression). Default Debian 13 lacks zstd. | Add `zstd` to the base-packages apt install list. | `f59a3f7` |
| B3 | PowerShell ↔ Ollama API testing | PowerShell 5.1's `ConvertTo-Json` mangles bodies containing embedded backslashes or quoted JSON (the PRD-content prompts to local LLMs). Ollama rejected the malformed JSON. | Switched to Python's `json.dumps` over a temp file. Recorded in `memory/reference_local_llms.md`. | (memory only) |
| B4 | Local-LLM delegation of install.sh | All three installed local LLMs (DeepSeek-R1, Gemma 4, Qwen 3.6) emit thinking traces that consume the `num_predict` budget. Output got truncated. | Bumped `num_predict` to 8000+ for code-gen; documented thinking-token gotcha. Reserve delegation for *small, tightly-spec'd* tasks. | (memory only) |
| B5 | WSL xrdp Xfce desktop | Five distinct WSL-specific blockers in series: security_layer mismatch (xrdp 0x204), missing xserver-xorg-legacy + Xwrapper.config, `/tmp/.X11-unix` mounted read-only by WSLg, Xfce 4.20 auto-Wayland detection breaking layer-shell, `light-locker` aborting without LightDM. | Each fixed individually. Final irreducible issue (xrdp↔Xorg transport drop ~3s in) is a known WSL-only artifact. Documented in `scripts/wsl-desktop-preview.sh` and PRD memory. | `fe89bbb` |
| B6 | SSH from host to VM | Windows OpenSSH refused the private key because Administrators + SYSTEM had access (inherited ACL). | `icacls /inheritance:r` + grant only current user. | (operational) |
| B7 | SSH key paste to VM | Pasting `echo 'ssh-ed25519 ...' >> ~/.ssh/authorized_keys` into the VM terminal stripped the spaces between `ssh-ed25519`, the base64 key, and the comment. Auth failed because SSH parses public keys as three space-separated fields. | Re-wrote the key via a base64-encoded blob: `echo '<base64>' | base64 -d > ~/.ssh/authorized_keys`. Paste-proof against space-stripping. | (operational) |
| B8 | minti-fetch alignment | `visible_len` used `awk length()` which counts bytes. Box-drawing glyphs (●, ─, │, ╲, ╱) are 3 bytes in UTF-8 but render as 1 column. Right column drifted across rows. | Switched to `wc -m` (character count under UTF-8 locale). Right column now aligns perfectly. | `54bb46b` |
| B9 | Ollama 0.24 installer (Debian 13) | Required zstd which is not in default Debian 13. | See B2. | `f59a3f7` |
| B10 | M2 pack-recon build | `debian/compat` file + `Build-Depends: debhelper-compat (= 13)` together — debhelper refuses ("compat level specified twice"). | Removed `debian/compat`; rely on the build-dep alone (modern convention). | (M2 commit) |
| B11 | M2 pack-recon build inside the VM | rsync from the vboxsf shared folder propagated 0755 mode to every `debian/*` file. debhelper then tried to execute `debian/install` as a script ("returned exit code 127: skill.md: not found"). | `m2-validate.sh` now does `find debian -type f -exec chmod 0644 \;` after rsync, then re-chmod 0755 just `debian/rules`. | `scripts/m2-validate.sh` |
| B12 | M2 pack-recon deps | `theharvester` is in Debian 12 main but not in Linux Mint 22 / Ubuntu noble default repos; including it in `Depends:` made the .deb un-installable on stock Mint. `dnsutils` was being renamed to `bind9-dnsutils` on some derivatives. | Moved `theharvester` (and `amass`) from `Depends:` to `Recommends:`; changed dnsutils dep to `dnsutils | bind9-dnsutils` alternation. | (M2 commit) |

---

## 6. File layout

```
.
├── README.md                       Public-facing overview
├── STATUS.md                       Where we are right now (resume from here)
├── DOCUMENTATION.md                This file — what was built, in what order
├── LICENSE                          MIT
├── .gitattributes                   LF enforcement for shell/yaml/go
├── .gitignore                       Secrets-aware
├── Makefile                         make runtime, make runtime-linux, etc.
│
├── cland/                          Clan daemon (M4 target — not yet built)
├── runtime-adapter/                M1 — Go daemon: Ollama proxy + uniform interface
│   ├── go.mod / go.sum
│   ├── cmd/minti-runtime/main.go
│   ├── internal/backend/           Backend interface + ollama + stubs
│   ├── internal/config/            YAML loader
│   ├── internal/server/            HTTP router + chat handlers
│   ├── configs/runtime.yaml.example
│   ├── systemd/minti-runtime.service
│   └── README.md
│
├── mcp-servers/                    M2 — Go module, 5 MCP servers + mcptest + shared framework
│   ├── go.mod / go.sum             official MCP SDK (modelcontextprotocol/go-sdk)
│   ├── cmd/mcp-{fs,shell,recon,pkg,http}/main.go
│   ├── cmd/mcptest/main.go         stdio test client (M2 acceptance harness)
│   ├── internal/{policy,audit,permission,mcpserve,proc}/
│   ├── configs/policy.yaml.example
│   └── README.md
├── console/                        GTK tray app (M6)
├── pack-manager/                   GTK pack manager (M6)
├── packs/                          Debian metapackages
│   ├── recon/                      M2 — nmap, masscan, whois, dnsutils + skill.md
│   │   ├── debian/                 control / changelog / copyright / rules / install / source/format
│   │   ├── skill.md                Agent-facing tool catalog + safety rules
│   │   └── README.md
│   ├── webapp/                     M6
│   ├── wireless/                   M6
│   └── forensics/                  M6
│
├── branding/                       Logos, themes
│   ├── logo.txt                    Plain ASCII logo
│   └── minti-fetch                 Neofetch-style status command
│
├── docs/
│   └── clan-protocol.md            Wire-level Clan protocol spec (v0.1)
│
├── install/
│   └── install.sh                  v1 installer (script path) — idempotent
│
├── ansible/                        v1.5 — alternative install via Ansible
├── scripts/                        Dev/build/test helpers
│   ├── setup-vm.sh                 Inside-VM one-shot installer
│   ├── vm-chat-test.sh             Smoke test the runtime end-to-end
│   ├── vm-guest-additions.sh       Install vbox guest additions
│   ├── vm-status.sh                Quick apt/dpkg/lock snapshot
│   └── wsl-desktop-preview.sh      Xfce-over-xrdp in WSL (partial; finicky)
│
└── tests/                          Reserved for protocol conformance tests
```

---

## 7. How to install and develop

### On a fresh Debian-family Linux

```bash
# clone (currently local-only; substitute path)
git clone <repo>  ~/minti
cd ~/minti
sudo bash install/install.sh

# verify
curl http://127.0.0.1:7780/minti/health
minti-fetch
```

### In the existing VirtualBox VM (development)

```powershell
# start the VM
& "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe" startvm minti-dev --type gui

# wait for boot, then SSH in
ssh -i ~\.ssh\minti_vm -p 2222 minti@127.0.0.1

# inside the VM, the repo is at /media/sf_minti-repo (Windows-side, live sync)
ls /media/sf_minti-repo
```

### Build the runtime

```powershell
# add Go to PATH
$env:Path = "C:\Program Files\Go\bin;$env:Path"

# native build
cd C:\Users\aouad\Documents\CCode\MINT\MINT_wip\runtime-adapter
go build -o minti-runtime.exe .\cmd\minti-runtime

# Linux cross-compile
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -o dist\minti-runtime-linux-amd64 .\cmd\minti-runtime
$env:GOOS = ""; $env:GOARCH = ""
```

Or via Make from inside the repo:

```bash
make runtime          # native
make runtime-linux    # cross-compile to amd64 Linux
make fmt vet test     # Go hygiene
```

---

## 8. References

- **PRD (the source of truth):** `C:\Users\aouad\.claude\plans\hello-can-we-create-abundant-hopper.md` — currently v0.6
- **Clan protocol wire spec:** `docs/clan-protocol.md` — currently v0.1
- **Runtime adapter internals:** `runtime-adapter/README.md`
- **User profile + dev environment notes:** `C:\Users\aouad\.claude\projects\C--Users-aouad-Documents-CCode-MINT-MINT-wip\memory\` — five memory files describing the user, the project, the delegation workflow, the local LLMs available, and dev-environment gotchas (PowerShell↔bash, SSH-to-VM patterns).
- **Open-source ecosystem MINTI builds on:** Debian, Linux Mint, Xfce, Ollama (MIT), llama.cpp (MIT), opencode (MIT), OpenHands (MIT), Hermes 3 (Nous Research, open weights), Llama 3.x (Meta, open weights), Qwen 2.5 (Apache 2.0), DeepSeek-R1 (MIT), exo (MIT — v2 distributed-inference target).
