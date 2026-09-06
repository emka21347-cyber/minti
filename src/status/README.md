# minti-status

Live terminal-UI dashboard for a MINTI node. Surfaces — in one screen,
on a 2-second refresh — who is in the Clan, who's Orchestrator, what
LLM is running, what's loaded in VRAM right now, which addon packs are
installed, and which agent harness is wired up.

Think `minti-fetch --watch`, but as a proper Go TUI instead of bash
re-running every few seconds.

## What you'll see

This is real `--once` output from a running MINTI node (the Phase J
test Clan in our `minti-dev` VM, self-orchestrating at term 3368):

```text
┌─ minti-status 0.1.0-M7.4 · runtime 0.1.0-M3 ────── root@minti-VirtualBox  06 Jun 02:16:51  [r 2s] ─┐
▎ System ─────────────────────────────────────────  ▎ Runtime (minti-runtime :7780) ────────────────
  OS       Linux Mint 22.3                            Health     ● healthy  0.1.0-M3
  Kernel   6.17.0-29-generic  x86_64                  Backend    ollama
  Uptime   1d 19h 55m                                 Resident   ★ llama3.2:3b
  CPU      AMD Ryzen 9 9950X3D  4t   load 0.06        In VRAM    (none loaded)
  GPU      (no nvidia GPU)
  RAM      1.1 / 7.8 GiB   swap 0.0 / 2.0
▎ Clan ─────────────────────────────────────────────────────────────────────────────────────────────
  Clan ID          5725d958…bffe    role=founder    pin=sha256:f6db79289…
  Orchestrator     a9f3df01…30ee ★ (self)    term=3368    lease 2s
  Members (4)      member                 os        state     reason  sys     ad
                ● 192.168.56.102:7777 (self) ★ linux     founder   —       —       now
                ○ 192.168.56.1:7777      windows   active    50      66      205h ago
                ○ 192.168.56.1:7777      windows   admitted  —       61      173h ago
                ○ 192.168.56.101:7777    linux     active    35      22      173h ago
  Candidates (4)   192.168.1.195:7777 (mdns)  10.0.2.15:7777 (mdns)  …
  Last elections   term 3368 ← a9f3df01…30ee   reason=bootstrap   6s ago  (×32)
▎ Addons (/var/lib/minti/packs) ───────────────────  ▎ Harness (opencode + claude) ──────────────────
  (no addon packs installed)                          opencode   minti-runtime
                                                      MCP        fs · http · pkg · recon · shell
                                                      claude     (not configured)
└─ q quit  r refresh  ?/h help ──────────────────────────────────────────────────────────────────────┘
```

A full-quality `.gif` rendering of the live interactive view (with
colour and ticker updates visible) is generated from
[`docs/minti-status.tape`](docs/minti-status.tape) via `make status-gif`
— see [Rendering the .gif](#rendering-the-gif) below.

## Quickstart

```bash
# Install via apt (after `make status-deb` builds the .deb):
sudo apt install ./dist/minti-status_*.deb

# Or run the cross-compiled binary directly:
make status-linux
sudo ./status/dist/minti-status-linux-amd64

# Interactive (default):
minti-status

# One-shot snapshot to stdout — useful in scripts / SSH pipes:
minti-status --once

# Disable ANSI colour:
NO_COLOR=1 minti-status
# (or)
minti-status --no-color

# Faster refresh:
minti-status --refresh 1s
```

**Run as root** (or as the `minti` system user) for the Clan panel to
read `/var/lib/minti/cland/clan.json` and shell `minti-cland`
subcommands. When unprivileged, the Clan panel degrades to
`(sudo for clan details)` — identical to `minti-fetch`'s behaviour.

### Keybinds

| Key | Action |
|---|---|
| `q` / `Ctrl+C` / `Esc` | Quit |
| `r` | Force-refresh every probe immediately |
| `?` / `h` | Toggle help footer |

## Panels

| Panel | Probe | Refresh | Data |
|---|---|---|---|
| **Header** | local | every tick | hostname, user, MINTI version, time, refresh badge |
| **System** | `/proc`, `uname`, `nvidia-smi` | 5s / 30s | OS, kernel, CPU, GPU, RAM, swap |
| **Runtime** | `GET 127.0.0.1:7780/minti/{health,version,capabilities}` | 2s | adapter health + version + backend + resident-model list with `★` on the best `reasoning_score` |
| **Runtime — In VRAM** | `GET 127.0.0.1:11434/api/ps` (Ollama) | 2s | Models actually loaded in VRAM **right now**, with TTL countdown — distinct from the disk-resident list above |
| **Clan** | `minti-cland show / orchestrator --json / peers --json / election-history --json` | 2s + 5s | Clan ID, role, cert pin, Orchestrator (with `★` + `(self)` markers), term, lease countdown, member table (incl. synthesised self row), candidates discovered via mDNS, deduped recent elections |
| **Addons** | `/var/lib/minti/packs/*.installed` | 5s | M6+ marker files dropped by `minti-pack-fetch` |
| **Harness** | `~/.config/opencode/opencode.json`, `~/.claude/settings.json` | 30s | configured agent client + MCP server list + Claude Code preset presence |
| **Footer** | local | every tick | keybinds + last-error line |

## Refresh model

Three independent tickers run goroutine-style commands off the UI loop:

| Ticker | Cadence | Probes fired |
|---|---|---|
| `tickFast` | 2 s | runtime + Ollama `/api/ps` + `minti-cland orchestrator --json` |
| `tickMed` | 5 s | sysinfo cheap reads + addons dir scan + `minti-cland peers --json` + `members` + `election-history --json` |
| `tickSlow` | 30 s | `nvidia-smi`, CPU model from `/proc/cpuinfo`, harness config files |

A probe failure never crashes the UI — it lands in the footer's
`last-err:` line and the affected panel degrades gracefully. EACCES on
`clan.json` is the most common one and triggers the `(sudo for clan
details)` rendering.

## Building

This module's binary cross-compiles to all four MINTI target platforms.
Drive it from the top-level Makefile:

```bash
make status                 # native
make status-linux           # GOOS=linux  GOARCH=amd64
make status-windows         # GOOS=windows GOARCH=amd64
make status-darwin-amd64    # GOOS=darwin GOARCH=amd64
make status-darwin-arm64    # GOOS=darwin GOARCH=arm64
make status-all-platforms   # all four
make status-deb             # → dist/minti-status_<v>_amd64.deb
```

The `.deb` ships a pre-built binary at `/usr/bin/minti-status`,
`Architecture: amd64`, `Build-Depends: debhelper-compat (= 13)` only.
`Recommends: minti-cland` (since the Clan panel is the headline).

## Tests

Plain Go golden tests under `internal/tui/panels/testdata/golden/`. 15
scenarios cover each panel × the meaningful states it can be in (Clan
unaffiliated / self-orch / peer-orch / EACCES / deduped-history;
Runtime healthy / down; addons + harness empty + populated; header +
footer with-err / no-err).

```bash
# Run tests:
cd status && go test ./...

# Regenerate goldens after a deliberate rendering change:
cd status && go test ./internal/tui/panels/ -update
```

ANSI escapes are stripped before diffing — colours are styling, not
structure. `\r` is also stripped defensively against CRLF on Windows.

## Rendering the .gif

The README .gif is generated from
[`docs/minti-status.tape`](docs/minti-status.tape) via
[charmbracelet/vhs](https://github.com/charmbracelet/vhs).

```bash
# Install vhs (one-time)
#   macOS:     brew install vhs
#   Linux:     download release from GitHub — not in apt yet
#   Windows:   download release from GitHub (or `scoop install vhs`)

make status-gif             # → status/docs/minti-status.gif
```

vhs needs `ttyd` and `ffmpeg` on PATH at render time. The `.gif` itself
is a build artefact (gitignored); the `.tape` source is the
source-of-truth and lives next to the README.

## Scope

This is **v1 — read-only**. The UI surfaces state; it doesn't change
it. Mutations (pin self / clear pin, rotate Clan key, peer-add, leave,
revoke) are deliberately deferred to v2.

Also out of scope for v1:

- Live log streaming (journalctl tail, audit-log tail) — `journalctl
  -u minti-cland.service -f` in another pane works fine for now.
- MCP tool invocation from the UI — `mcptest` already does that.
- Model switching / `ollama pull` — use Ollama's own CLI.
- Multi-Clan view — single Clan only (matches cland's data model).
- Mouse support — keyboard-only, fits SSH + tmux floor.
- Config file / theme customisation — palette is hardcoded to match
  `minti-fetch`; `NO_COLOR` is respected.

## Source layout

```
status/
├── cmd/minti-status/main.go     # flag parsing → tui.Run
├── internal/
│   ├── tui/                     # bubbletea Model + layout + panels
│   │   ├── app.go               # Init / Update / View
│   │   ├── msgs.go              # tick + probe-result msg types + tea.Cmd factories
│   │   ├── styles.go            # lipgloss palette (mint 42, cyan 81, grey 245)
│   │   ├── layout.go            # responsive grid (≥100 cols 2-col, <100 stacked)
│   │   └── panels/              # one .go file per panel (pure functions)
│   │       └── testdata/golden/ # 15 panel rendering snapshots
│   ├── probes/                  # one package per data source
│   │   ├── sysinfo/             # linux + windows + darwin build-tagged
│   │   ├── runtime/             # minti-runtime + Ollama /api/ps
│   │   ├── clan/                # shells minti-cland --json subcommands
│   │   ├── addons/              # /var/lib/minti/packs/*.installed
│   │   └── harness/             # opencode.json + .claude/settings.json
│   └── version/version.go       # embedded build version (-ldflags)
├── debian/                      # M6.1-pattern .deb scaffold
└── docs/
    ├── minti-status.tape        # vhs source (committed; .gif is gitignored)
    └── example.txt              # static --once snapshot (committed)
```
