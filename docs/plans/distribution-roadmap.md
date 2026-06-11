# MINTI Distribution & OS Roadmap (multi-track)

> Disentangling "download from Vercel → total-wipe → lean MINTI OS" into the
> separate deliverables it actually is. Each ships on its own timeline.
> User intent (2026-06-07): the **download + install-the-OS path is FIRST**;
> the workspace is the in-OS experience; the Android native node is parked.

## The tracks

| # | Track | What it is | Status | Depends on |
|---|---|---|---|---|
| **1** | **Lean OS ISO** | The live-build image (Mint-lean, Debian bookworm base) | **PoC proven** — boots, autologin, minti tools run | — |
| **2** | **Installer ("total wipe")** | Make the ISO *install to disk* persistently, with a wipe option | **NOT built** — current ISO is live-only (RAM, no persistence). Installer was deliberately disabled for the PoC | Track 1 |
| **3** | **Vercel download site** | Static landing page hosting the `.iso` (+ later `.apk`); optional guided USB flasher | **NOT built** | Tracks 1+2 (something worth downloading) |
| **4** | **Clan Workspace UI** | The web dashboard that ships *inside* the OS (the mock we locked) | Mock locked; Phase A pending | independent of 1-3 |
| **5** | **Android native node + WAN** | Phone joins Clan as a contributing node; WAN transport | **Parked** (user: "later") | WAN/NAT milestone |

These parallelize: 2 is live-build config, 3 is a static site, 4 is Go. Different surfaces, can run concurrently.

## Reality checks (so there are no surprises)

1. **Live ISO ≠ installer.** Today the image boots to RAM and forgets everything on reboot. "Total wipe + set up the OS" needs an **installer** added to the ISO. Recommended: **Calamares** — the modern, distro-agnostic graphical installer (used by EndeavourOS, etc.), themeable to the MINTI brand, handles wipe/partition/bootloader. Alternatives: debian-installer (clunky UX, the one we disabled) or a custom script (lean but reinvents partitioning/GRUB/LUKS — risky). → **Calamares.**

2. **You can't wipe a machine by just downloading a file.** The honest flow is: download `.iso` → write to USB → boot from USB → installer offers "try live" or "wipe & install". You can't wipe the disk you're currently booted from. Two ways to smooth it:
   - **Minimal:** Vercel page = download `.iso` + clear instructions (use balenaEtcher/Rufus to flash USB).
   - **Slick (later):** a small per-OS **guided flasher** download (à la Raspberry Pi Imager) that writes the USB and walks the user through it. Its own mini-deliverable.

3. **Total wipe is destructive** — the installer must have an explicit, unmissable confirm + a clear "install alongside" option for people who don't want to nuke their disk.

## Recommended sequence

1. **Track 1 hardening** — make the ISO reproducible + clean up the PoC debug scaffolding (already a pending item).
2. **Track 2 — Calamares installer** → the ISO can now total-wipe-install the lean OS to disk. This is the core of "first".
3. **Track 3 — Vercel site** → land, download the `.iso`, instructions to flash + boot. (Guided flasher = optional follow-on.)
4. **Track 4 — Workspace Phase A** runs in parallel (independent code), lands inside the OS.
5. **Track 5 — Android node** later, on the WAN milestone.

## Decisions (user, 2026-06-07)
- **Installer = Calamares.** User said "whatever Mint uses — they reach most hardware with ease; it's about reviving old hardware." Mint uses Ubiquity (Ubuntu-based); our ISO is Debian, where Calamares is the equivalent friendly broad-hardware installer (and what Debian Live itself ships). Honors the intent.
- **MUST re-enable firmware** (we disabled `--firmware-chroot/binary` in commit aebdba2 to dodge a download bug). "Reach most hardware / revive old laptops" is impossible without WiFi/network firmware. This is now a hard requirement on the OS track, not optional.
- **Two distribution products** (Vercel offers both doors):
  - **A · Revive a machine** — full OS `.iso` → flash USB → boot → Calamares total-wipe install.
  - **B · Add MINTI to your machine** — install just the MINTI stack (cland + runtime + workspace) on an existing Win/Mac/Linux OS, no wipe. Reuses M5 cross-platform service work (NSSM/launchd/systemd) + the workspace as its GUI. For people with good hardware who just want to run Clans.
- **Download UX:** ISO + flash instructions for product A; a simple per-OS installer/download for product B. Guided USB flasher = later polish.
- **Sequencing: Workspace FIRST.** User: "workspace first after all." It's both the in-OS dashboard AND the headline of product B, so it unblocks the most. Installer + site + firmware come after. Android node still parked.

## Updated build order
1. **Workspace** — freeze mock → Phase A (`minti-workspace` live mesh) → B/C/D. (NOW)
2. OS track: re-enable firmware + add Calamares installer + harden/reproducible ISO.
3. Vercel site: two doors (revive vs add-MINTI), host `.iso` + product-B installers.
4. Android native node + WAN (parked).

## Re-confirmed (user, 2026-06-11) + browser reality check

User re-confirmed products **A + B as the deliverables** after asking whether a
Vercel page could "take control of the system" directly from the browser.
Recorded so it isn't re-litigated:

- **A web page cannot install software, download models, open ports, or run
  daemons — browsers are sandboxed by design.** Any "browser does everything"
  experience is an illusion built on ONE manual step: the user runs a small
  installer once; from then on the browser genuinely is the whole interface,
  because the workspace runs on the machine and serves its UI locally. That
  one step is a security feature, not a gap.
- **Browsers never connect to each other.** Each machine runs the stack
  (product B); each browser opens its OWN machine's workspace; the cland
  daemons form the Clan underneath (knock flow = the onboarding). Same-LAN
  works with what exists; cross-network is the parked WAN track (5).
- **Browser-only nodes (zero install) rejected:** WASM in-browser models are
  toy-scale, die with the tab, can't drive Ollama/GPU, and can't revive old
  hardware — against the project's purpose (P2).
- **Door-B polish adopted:** the product-B installer auto-opens
  `http://127.0.0.1:8088` (the workspace) when it finishes, so the
  page→install→Clan flow feels seamless. PWA manifest (already planned)
  completes the app feel.
- **Hosting cost posture:** Vercel free tier serves the page; the ~360 MB
  `.iso` lives on a free artifact host (GitHub Releases / Cloudflare R2) that
  the page links to — Vercel never hosts the big binaries and never runs a
  node (unchanged locked decision).

## RESEQUENCED (user, 2026-06-11): Distribution milestone "D" is NEXT

After Clan Memory (M0–M6) shipped, the user pulled the **distribution track
ahead of the remaining workspace phases** (knocks/chat/cookbook wiring move
behind it). The next milestone is everything needed to put the two doors on
the internet, phased so the site never points at vapor:

| Phase | Deliverable | Notes |
|---|---|---|
| **D0** | Plan peer-review (3 local LLMs) | Installer SAFETY is the focus: what door B writes/starts/uninstalls per OS, Ollama handling, the auto-open flow, site information architecture. Installers touch people's machines — same review-before-code bar as the protocol work. |
| **D1** | Door B — Windows one-click | Wrap the proven M5-B NSSM zip (cland) + ADD runtime-adapter + workspace as services + Ollama detect-or-guide + auto-open `http://127.0.0.1:8088` on finish. The headline product. |
| **D2** | Door B — Linux + macOS | Extend `install/install.sh` to deploy + unit the workspace (it doesn't yet); reuse the M5-C launchd tarball for mac. Same auto-open. |
| **D3** | The site itself | Static two-door page, MINTI brand (reuse `workspace/mock/index.html` design language + `docs/brand.md`), lives in `site/` in this repo, deployable to Vercel. Door A links the live-ISO with HONEST "live preview — disk installer coming" copy until D4; door B links the D1/D2 artifacts. Built + previewable locally without any Vercel account. |
| **D4** | Door A — Calamares installer + firmware re-enable on the ISO | The big OS-track item. Needs the build VM (minti-dev-2) powered on — operator-gated. Can trail D3's launch; the site copy already covers that honestly. |

**Operator (user) decisions/actions this milestone needs — none block D0–D2:**
1. ~~Vercel account~~ **VERIFIED 2026-06-11**: Vercel CLI authed as
   `emka21347-2077` on the daily-driver. D3 deploy is unblocked.
2. ~~Artifact host~~ **VERIFIED 2026-06-11**: GitHub `emka21347-cyber` authed
   via gh CLI (scopes repo+workflow) → **GitHub Releases is the default
   artifact host.** One real decision REMAINS: the MINTI source repo is
   local-only with no remote (user's choice). Releases must attach to SOME
   public GitHub repo — either publish the source repo, or create a small
   public `minti-releases` repo that holds only artifacts + checksums while
   the source stays private. Ask the user at D3, not before.
3. Booting the build VM for D4 when that phase starts.

Commits: `Dist D<N>: <what>`, one per phase, same verification + honest
STATUS.md discipline as the Memory milestone. The Clan Memory 3-node VM gate
(see STATUS.md) is unrelated and runs whenever the VMs are up anyway.

## D-milestone detail (D0, 2026-06-11) — reviewed by 3 local LLMs before any code

> Scope: everything door B writes/starts/uninstalls per OS, the Ollama
> strategy, the auto-open flow, upgrade/coexistence semantics, and the site
> IA. This section is the review input for D0 and the implementation
> contract for D1–D3. D4 stays a pointer (gated on the build VM).

### Door B — the common contract (all three OSes)

Door B installs **three services** and touches **nothing else**:

| Service | Binary | Listens | Exposure |
|---|---|---|---|
| Clan daemon | `minti-cland` | `:7777` TLS+HMAC | LAN (the only open port) |
| Runtime adapter | `minti-runtime` | `127.0.0.1:7780` | loopback only |
| Workspace UI | `minti-workspace` | `127.0.0.1:8088` | loopback only — **MUST stay loopback until PIN/bearer auth lands** (main.go contract) |

Invariants (the safety bar, from the M5-B precedent):
1. **Exactly one inbound firewall change** — cland :7777, scoped
   `Private,Domain` on Windows (M5-B rule), nothing for the two loopback
   daemons (loopback traffic is not firewalled on any of the three OSes).
2. **Every bundled third-party binary checksum-verified at install time**
   (today that's exactly one: NSSM on Windows, SHA-256 sidecar verified by
   the installer before use — existing M5-B mechanism). The artifacts
   themselves get published SHA-256s on the site + `checksums.txt`.
3. **Explicit uninstaller per OS, state-preserving by default.** Uninstall
   removes services + binaries + the firewall rule; `/var/lib/minti` +
   `/etc/minti` (Linux), `%PROGRAMDATA%\MINTI` (Windows),
   `/Library/Application Support/MINTI` (macOS) survive unless
   `--purge`/`-Purge`. Purge wipes identity/clan_key → host leaves its Clan;
   the uninstaller says so before doing it.
4. **Never touches third-party software on uninstall.** Ollama and opencode
   are left installed; the uninstaller prints how to remove them upstream.
5. **No silent destructive steps anywhere.** Upgrades preserve configs +
   state; installers refuse rather than kill foreign processes squatting on
   our ports.
6. **Idempotent re-run is the upgrade path AND the failure recovery path**
   (M3 B13 stale-binary-restart pattern everywhere).
7. Workspace finds `minti-cland` on PATH (`exec.LookPath`) and degrades to
   demo data when absent — installers must make the cland binary resolvable
   from the workspace service's environment (see per-OS notes).

**Ollama strategy — detect-or-guide, never silent-install (Win/mac):**
- **Detect**: (a) `ollama` on PATH → report version; (b) OS default install
  location (`%LOCALAPPDATA%\Programs\Ollama\ollama.exe`,
  `/Applications/Ollama.app`); (c) HTTP probe `127.0.0.1:11434/api/version`
  — parsed for a JSON `version` field, not just an open port (D0 review F5;
  detection stays advisory-only, nothing is ever piped into it).
- **Found + serving** → report and move on. **Found + not running** → tell
  the user to launch the Ollama app (it autostarts at login thereafter).
- **Missing** → print guidance + offer to open the official download page
  (`ollama.com/download/<os>`) — interactive y/N, default N, skipped in
  unattended mode. We never download or execute a third-party installer.
- **Linux exception (existing, validated M0 behavior):** `install.sh`
  already runs the official `ollama.com/install.sh` when Ollama is missing —
  keep it (changing it would regress the validated install path AND the ISO
  chroot hook that reuses install.sh). Add `MINTI_NO_OLLAMA=1` opt-out env.
  The difference in posture vs Win/mac is stated honestly on the site.
  **D0 review F6:** the Ollama block becomes non-fatal on door-B installs
  (today `set -e` aborts the whole install on a flaky ollama.com download;
  instead: warn + continue + an honest `Ollama: FAILED` summary line —
  runtime tolerates absence by design) but stays **fatal under
  `MINTI_CHROOT=1`** (an ISO silently built without Ollama is worse than a
  failed build).
- **Install order never matters**: minti-runtime starts fine with Ollama
  down (startup probe warns; `/minti/health` surfaces it honestly — M1).

**Auto-open flow (all OSes):** after the services report running, poll
`http://127.0.0.1:8088/` for HTTP 200 (≤30 s — D0 review F7). On success,
open the URL in the user's default browser **de-elevated**: Windows via
`explorer.exe "http://127.0.0.1:8088"` (explorer marshals the URL to the
desktop user's medium-IL context instead of the elevated installer session
— D0 review F3), Linux `xdg-open` as `$SUDO_USER`, macOS `open` as the
console user. On timeout, do NOT open — print the URL, the service status
command, and the log paths, and exit non-zero. The printed URL appears in
both cases (the browser open is best-effort polish, never load-bearing).

### D1 — Windows (the headline)

**Artifact:** `dist/minti-windows-amd64-v<VER>.zip` built by
`install/windows/build-zip.ps1` — a sibling of the proven
`cland/windows/nssm/build-zip.ps1`, reusing its steps verbatim (cross-
compile, NSSM download+cache+SHA-256 sidecar, staging tree, zip). The M5-B
cland-only zip remains for the cland-only use case; door B ships the full
stack. Makefile gains `minti-windows-zip` (and `workspace-windows` /
`runtime-windows` cross-compile targets — the workspace module has no
Makefile presence yet).

**Zip layout:**
```
install-minti.ps1          uninstall-minti.ps1        README.md
Install-MINTI.cmd          (elevation shim — see review Q2)
bin\minti-cland.exe        bin\minti-runtime.exe      bin\minti-workspace.exe
bin\nssm.exe               bin\nssm.sha256
configs\cland.yaml.windows.example
configs\runtime.yaml.windows.example
configs\reasoning-scores.yaml.example
```

**What install-minti.ps1 writes/starts (exhaustive):**
- Dirs: `%PROGRAMFILES%\MINTI\{cland,runtime,workspace}` (binaries; each
  service dir gets its own `nssm.exe` copy — uniform with M5-B's layout, no
  shared-file coupling between services, ~330 KB × 3; cland's stays exactly
  where M5-B put it so the upgrade path is the already-proven re-run).
- State: `%PROGRAMDATA%\MINTI\{cland,runtime,workspace}` — strict DACL
  (`SYSTEM` + `Administrators` only, inheritance off; identical icacls call
  to M5-B) on all three. cland's holds clan_key/identity; runtime's holds
  `runtime.yaml` + logs; workspace's holds logs only.
- Services (NSSM 2.24, SHA-256-verified before first use): `Minti-Cland`
  (existing M5-B name — **upgrade in place, never a second service**),
  `Minti-Runtime`, `Minti-Workspace`. All `SERVICE_AUTO_START`,
  stdout/err logs at `<state>\logs\` with 10 MiB rotation,
  `AppStopMethodConsole 5500` (matches Go-side 3 s shutdown under the SCM
  ~10 s budget — M5 item 7).
  - **Service accounts (D0 review F1 — split along the trust boundary):**
    `Minti-Cland` stays **LocalSystem** (the live M5-B service is upgraded
    in place; its mDNS/firewall/Defender behavior is proven under that
    account — an ObjectName flip mid-upgrade is the class of surprise D1
    must not have). `Minti-Runtime` + `Minti-Workspace` run as
    **`NT SERVICE\<name>` virtual accounts** (no passwords; registered via
    NSSM then `sc.exe config <name> obj= "NT SERVICE\<name>"` — sc takes no
    password arg, NSSM's ObjectName setter wants one). icacls grants after
    registration: runtime = Modify on its own state dir; workspace = Modify
    on its own state dir + **Read** on the cland state dir (the shelled CLI
    reads identity/clan_key/cland.yaml and writes nothing; if D1
    verification proves a write is needed, the fallback is a Modify grant
    on `audit.jsonl` only — never LocalSystem). Blast-radius honesty: a
    workspace compromise yields clan_key read (Clan-scoped, recoverable by
    key rotation), not SYSTEM.
  - Workspace service gets `AppEnvironmentExtra
    PATH=%PROGRAMFILES%\MINTI\cland;...` so `exec.LookPath("minti-cland")`
    resolves — scoped to the service, no machine-wide PATH mutation.
  - Runtime service args: `--config %PROGRAMDATA%\MINTI\runtime\runtime.yaml`
    (its compiled-in default is the Unix path; missing file = sane defaults,
    config staged anyway, preserved on re-run).
- Firewall: the ONE rule, `MINTI cland (Clan TLS)` :7777, default
  `Private,Domain` with operator override — removed+recreated per M5-B
  (handles renamed install dirs). Nothing for 7780/8088.
- Preflight (refuse + remediate, never kill): admin check; stray
  *foreground* `minti-cland.exe` detection (the M5-D finding — a Phase-J-
  style console process holding :7777 blocks the service silently);
  foreign-process port checks on 7780/8088 (our own services are fine —
  they get stopped for upgrade).
- Ollama detect-or-guide as above; then start order cland → runtime →
  workspace; health-poll; auto-open; summary + the M5-B Defender
  folder-exclusion guidance verbatim.

**Upgrade / M5-B coexistence (the daily-driver case):** re-run = upgrade.
Stop in dependency order workspace → runtime → cland (skip absent ones —
an M5-B box has only `Minti-Cland`), swap binaries, preserve every config +
the whole state dir + DACL, refresh NSSM settings idempotently, start
cland → runtime → workspace. PID is discovered live via `Get-Service`,
never assumed. The existing service is never removed/re-added (no
`nssm remove` in the install path) — settings are `nssm set`-refreshed.
Crash-window honesty (D0 review F10): between binary swap and NSSM
refresh the services are stopped, `AppParameters` are version-stable
across this upgrade, and re-running the installer is the designed repair —
no transactional machinery (it would add more failure modes than it
removes).

**Uninstall (uninstall-minti.ps1):** stop+remove the three services (each
skipped gracefully if absent — works on an M5-B-only box too), remove the
firewall rule (by DisplayName **and** a sweep of inbound rules whose
Program is our installed exe — catches admin-renamed rules, D0 review F8),
remove `%PROGRAMFILES%\MINTI`. State preserved by default
with the exact M5-B messaging; `-Purge` wipes `%PROGRAMDATA%\MINTI` +
logs after printing what that means (member identity destroyed). Ollama
untouched (pointer to Windows "Add or remove programs").

**"One-click" honesty:** the real flow is download zip → extract →
double-click `Install-MINTI.cmd` → UAC consent. The shim runs
`powershell -ExecutionPolicy Bypass -File install-minti.ps1` elevated via
`Start-Process -Verb RunAs`. M5-B deliberately refused auto-elevation
*from inside an already-running script* (SmartScreen UX); a double-clicked
shim is a different entry path but still MOTW-flagged — **review Q2 asks
whether the shim is acceptable or whether the README one-liner (open
elevated PS, run the script) stays the only documented path.** Either way
the .ps1 path remains canonical and the site shows the 3 honest steps.

**D1 verification (daily driver, only after the D0 review blesses the
upgrade path):** build zip → install over the live M5-B service → all
three services Running; `:7777` listening, `127.0.0.1:7780/minti/health`
+ `127.0.0.1:8088/` return 200; cland state/identity unchanged
(member_id before == after); auto-open fired; re-run idempotent (configs
preserved, no restart when hashes match); `go vet`/`go test` green in
cland + runtime-adapter + workspace. Uninstall on the daily driver: the
no-purge path (stop/remove services + reinstall right after) only if the
review blesses it; **-Purge is never exercised on the daily driver** —
it gets a VM pass when the VMs are next up (honest deferral if not).

### D2 — Linux + macOS

**Linux artifact:** `dist/minti-linux-amd64-v<VER>.tar.gz` — `bin/` (cland,
runtime, workspace, pack-fetch, 5 MCP servers, mcptest, minti-status),
`configs/`, `systemd/`, `branding/minti-fetch`, `install.sh`,
`uninstall.sh`, README. All pure-Go cross-compiles from this box (existing
Makefile targets + new workspace ones). `install.sh` keeps working from a
git checkout too — its existing per-binary candidate-path loops just gain
the tarball layout as another candidate.

**install.sh additions (D2):**
- **Workspace section** mirroring the cland block verbatim: binary →
  `/usr/local/bin/minti-workspace`; new unit
  `workspace/systemd/minti-workspace.service` — hardening copied from
  `minti-cland.service` (`ProtectSystem=strict`, `ProtectHome=true`,
  `PrivateTmp`, `NoNewPrivileges`, `LockPersonality`,
  `MemoryDenyWriteExecute`, `RestrictRealtime`, `User=minti`,
  `Restart=on-failure`) **plus** `IPAddressAllow=127.0.0.0/8 ::1/128` +
  `IPAddressDeny=any` (explicit v4+v6 loopback rather than the `localhost`
  alias — D0 review F4) — the workspace is loopback-only and its only
  outbound calls are the shelled `minti-cland` CLI's loopback HMAC hits on
  :7777, so localhost-only is airtight (cland's own unit can't have this —
  documented there). `ExecStart=/usr/local/bin/minti-workspace -listen
  127.0.0.1:8088`. B13 stale-binary restart. Workspace runs as `minti`, the
  same user that owns `/var/lib/minti/cland` (0700) — the shelled CLI can
  read the HMAC key. PATH: `/usr/local/bin` is on the unit's default path.
- **Auto-open**: if `$SUDO_USER` set and a display is up
  (`DISPLAY`/`WAYLAND_DISPLAY` in the user's session) →
  `sudo -u $SUDO_USER xdg-open http://127.0.0.1:8088` best-effort; else
  print the URL + an `ssh -L 8088:127.0.0.1:8088` hint for headless boxes.
  Skipped under `MINTI_CHROOT=1` (ISO build path stays non-interactive).
- `MINTI_NO_OLLAMA=1` opt-out around the existing Ollama block.

**New `install/uninstall.sh`:** stops + disables + removes the three units
+ binaries (+ MCP binaries + mcptest + minti-fetch + pack-fetch), removes
unit files, `daemon-reload`. Preserves `/etc/minti` + `/var/lib/minti` (+
the `minti` user) by default; `--purge` removes both + the user after an
explicit printed warning (clan_key/identity destroyed). Ollama + opencode
untouched (prints their own uninstall pointers). Refuses to run inside
`MINTI_CHROOT=1`.

**macOS artifact:** `dist/minti-macos-{arm64,amd64}-v<VER>.tar.gz`
extending the M5-C tarball: adds `bin/minti-runtime` + `bin/minti-workspace`
+ `com.minti.runtime.plist` + `com.minti.workspace.plist` (clones of the
peer-reviewed `com.minti.cland.plist`: `RunAtLoad`, `KeepAlive
{SuccessfulExit:false}`, `ThrottleInterval 5`, `ProcessType Background`,
system mode `UserName _minti`; runtime's `ProgramArguments` passes
`--config "/Library/Application Support/MINTI/runtime/runtime.yaml"`,
workspace's passes `-listen 127.0.0.1:8088`). `install-minti.sh` /
`uninstall-minti.sh` extend the M5-C scripts' system/per-user dual branches
to the three services (same `_minti` dscl recipe, same state-dir modes,
same bootstrap-with-fallback). `xattr -d com.apple.quarantine` runs on
**all three** bundled binaries (M5-C cleared one — D0 review F2);
`uninstall-minti.sh` bootouts + deletes **all three**
`com.minti.{cland,runtime,workspace}.plist` files in both system and
per-user branches (orphan plists respawn-fail at every boot — D0 review
F9). Ollama: detect-or-guide (open `ollama.com/download/mac`); note that
Ollama.app is a per-user login item — fine, runtime talks to
`127.0.0.1:11434` regardless of who started it. Auto-open via `open` as
the console user.

**D2 verification:** Linux — `bash -n` + shellcheck (if present) on both
scripts; tarball assembled with the right layout; full install/uninstall/
re-install cycle in the `minti-dev` VM **when the VMs are next powered on**
(same honest-deferral discipline as M5-D; D2 can commit with the VM pass
pending, explicitly logged in STATUS.md). macOS — **no Mac available**:
build-clean cross-compile, `plutil -lint`-equivalent plist parse
(python plistlib, the M5-C recipe), `bash -n`, and the targeted LLM review;
the site labels the macOS download with its verification status honestly.
`go vet`/`go test` green in workspace (+ any touched module).

### D3 — the site

**Stack:** pure static in `site/` — `index.html` + `site/assets/`
(logo.svg reused, one woff2 if we self-host the mono font, favicon,
`vercel.json`). No JS framework, no external CDNs (the mock's CDN pulls
were mock-only; the locked rule is the real frontend self-hosts), no
analytics/telemetry, total page weight target < 200 KB. Works fully from
`python -m http.server` (that's the local preview verification). Vercel
free tier serves it; **artifacts NEVER on Vercel; Vercel never runs a
node** (locked).

**IA (one page, top to bottom):**
1. **Top bar** — mock idiom: `▚ MINTI` wordmark + tagline; GitHub link.
2. **Hero** — one sentence (own-your-hardware AI mesh; open agents, open
   weights, Clan-aware) + the 9-node mesh SVG, brand palette rules enforced
   (mint = single-focus elements only, cyan = the enumerable nodes).
3. **The two doors** side-by-side cards; door B visually primary (it's the
   headline product), door A carries an honest `live preview — disk
   installer coming` badge until D4 ships, then flips.
   - **B · Add MINTI to this machine** — OS picker (Windows .zip / Linux
     .tar.gz / macOS .tar.gz arm64+amd64), per-OS: requirements, the 3
     honest install steps, SHA-256 inline + `checksums.txt` link, and a
     **"what this installs" disclosure** (3 services, the one open port,
     exact paths, uninstall one-liner) — the trust signal an installer page
     owes people.
   - **A · Revive a machine** — ISO download + SHA-256, flash instructions
     (Rufus / balenaEtcher / dd), boot-from-USB note, requirements
     (x86-64, 1–2 GB RAM), the honest live-vs-installed copy.
4. **Known warnings** (D0 review F2 — gemma's blocker, 3/3 consensus):
   the binaries are not yet code-signed, stated plainly; the exact
   SmartScreen ("More info → Run anyway") and Gatekeeper ("Open Anyway")
   paths; verify-the-SHA-256-first framing; minimum hardware expectations
   (RAM per model tier, so swap-death on a 4 GB box isn't read as "MINTI
   broke my machine"). Pretending one-click while the OS shows red
   destroys trust — the warning is part of the product until signing
   lands.
5. **How it works strip** — three glyphs: install → machines knock into a
   Clan → the browser is the whole interface (workspace screenshot or
   stylized SVG, no heavy images).
6. **Footer** — GitHub, license, "no telemetry, no accounts, everything
   stays on your LAN" statement.
- Downloads link to **GitHub Releases** (`emka21347-cyber`); the one open
  operator decision (publish the source repo vs a public `minti-releases`
  artifacts-only repo) is asked **at D3, not before** (locked roadmap item).

**Delegation:** site first-pass HTML/CSS = T3 via `scripts/t3-codegen.py`
with a tight spec (palette hexes, IA section list, semantic-HTML +
no-framework constraints); T2 line-by-line review before it lands (M4
precedent: T3 ships silent logic bugs; here the blast radius is copy +
links + hashes, all reviewable). Installer scripts (D1/D2) are T2-direct —
they write to people's machines; per the delegation rule that's never T3
work.

**D3 verification:** local static preview renders in a desktop + a
~360 px viewport; every download link resolves (or is a clearly-marked
placeholder until the release exists); checksums file matches built
artifacts; honest door-A copy in place; Vercel deploy only after the
operator answers the repo question.

### D4 — door A (pointer only; gated on the build VM)

Calamares (`calamares` + `calamares-settings-debian` in the package list,
settings sequence + MINTI branding from `docs/brand.md`) + the **hard
requirement** firmware re-enable (revert aebdba2: `--firmware-chroot
true`, `--firmware-binary true`, archive-areas `main contrib non-free
non-free-firmware`) + keep the live-boot path working alongside the
installer. The detailed plan is authored when D4 starts, after re-reading
`memory/reference_live_build.md` (build VM = the CLONE minti-dev-2,
identified by MAC, lb 3.0~a57 quirks). Not blocked-on by D1–D3; the site
copy already covers its absence honestly.

### Versioning + naming

`VERSION` bumps to `0.4.0-D1` when D1 lands. Artifacts:
`minti-windows-amd64-v<VER>.zip`, `minti-linux-amd64-v<VER>.tar.gz`,
`minti-macos-{arm64,amd64}-v<VER>.tar.gz`, later
`minti-os-amd64-v<VER>.iso`; one `checksums.txt` (SHA-256) covers all.

### Questions put to the D0 reviewers

1. Windows: per-service NSSM copies + LocalSystem for all three services
   (workspace *needs* SYSTEM to read the strict-DACL cland state via the
   shelled CLI) — sound, or is there a least-privilege split worth the
   complexity now?
2. The `Install-MINTI.cmd` elevation shim vs M5-B's documented
   no-auto-elevate stance — acceptable entry path, or README-only?
3. The three Ollama postures (Win/mac detect-or-guide + open-page;
   Linux auto-run official script + `MINTI_NO_OLLAMA=1`) — defensible?
4. Auto-open flow — any failure mode where opening the browser misleads
   the user about install success?
5. Upgrade-over-M5-B ordering + the stray-foreground-cland preflight —
   gaps that could break the daily driver's live service?
6. Uninstall semantics per OS — anything destructive hiding in the
   default path? Anything the default path should remove but preserves?
7. The workspace systemd unit's `IPAddressAllow=localhost` +
   `IPAddressDeny=any` — does the shelled `minti-cland` CLI (child of the
   unit, loopback HMAC to :7777) survive it? Any other unit-hardening
   knob that breaks subprocess or loopback behavior?
8. Port-conflict preflights (7777 stray-cland, 7780/8088 foreign
   process) — sufficient? What about 11434 squatters mimicking Ollama?
9. Site IA — does the page earn "this installer won't hurt my machine"
   trust? What's missing? Is the two-door split self-explanatory to
   someone who has never heard of MINTI?
10. What's right and should NOT change (don't pad the critique).

### D0 review outcome (2026-06-11)

Run via `scripts/dist-d0-review.py` (mirrors memory-peer-review.py; input
snapshot + raw reviews at `scripts/m4-reviews/dist-d0-*`). Verdicts:
qwen3.6 "build after 1A/5A/6A", deepseek-r1 blanket "address 1–9" (weak
round — no thinking trace, generic corroboration), gemma4 "build after
2+8". **Ten findings folded (F1–F10, marked inline above), eight
overruled with reasons** — full table in
`scripts/m4-reviews/dist-d0-triage.md`. Headline folds: the
runtime/workspace services drop LocalSystem for `NT SERVICE\` virtual
accounts while cland keeps the proven M5-B account (F1, the panel's only
split decision, resolved along the trust boundary); the site + macOS
installer stop pretending unsigned binaries are seamless (F2, 3/3
consensus); the Windows browser auto-open de-elevates via explorer.exe
(F3). The panel unanimously endorsed: loopback-only workspace/runtime,
state-preserving uninstall default, SHA-256 verification, idempotent
re-run as the repair story, the elevation shim, per-service NSSM copies,
and the "what this installs" disclosure. D1 may start.

## Sequencing after D3 (user, 2026-06-11) + handover

D0–D3 shipped 2026-06-11 (commits `1f419e1` → `1da3597` → `9f6cd50` →
`603fdba`). The two doors are live: **https://minti-pi.vercel.app** serving
downloads from **github.com/emka21347-cyber/minti-releases** (v0.4.0-D1).
User-decided order for what remains:

1. **Site layout polish FIRST** — iterate on minti-pi.vercel.app's layout
   with the user in the loop until it's "set". Layout only; the D0-review
   trust content is non-negotiable (see kickstart below).
2. **Then D4** — Calamares disk installer + firmware re-enable on the ISO
   (build-VM-gated as before). The site's door-A copy flips when it ships.
3. **macOS deliberately LAST, "really further down the line"** — once
   everything else is set, the user switches to a real Mac and the D2
   macOS recipes get their live validation there, closing the milestone.
   Do not spend session time on macOS before the user brings the Mac.

Operational facts the next session needs:
- **Vercel**: `site/` is linked to project `minti` (scope
  `emka21347-2077s-projects`; `.vercel/` is gitignored). Deploy =
  `vercel deploy --cwd site --prod`. **Production deploys are held for
  explicit user consent by the permission classifier** — ask once at
  session start whether iterative layout redeploys are pre-authorized,
  or preview locally and deploy once at the end.
- **Local preview**: `.claude/launch.json` has a `site` config
  (`python -m http.server 8930 --directory site`). Gotcha from D3:
  `preview_screenshot` hung repeatedly (tool-side renderer; the page
  itself served fine) — layout verification ran HTTP/structural-level
  only. For LAYOUT work, visual verification is the point: retry the
  preview tools fresh, and lean on the Launch preview panel + the user's
  eyes as the arbiter.
- **Downloads**: all four artifact links + checksums.txt on the page are
  live (verified 200). Don't touch URLs/hashes during layout work.

## Kickstart prompt for the NEXT chat — site layout (paste verbatim)

```
Project: MINTI — repo at C:\Users\aouad\Documents\CCode\MINT\MINT_wip.
Task: polish the LAYOUT of the live two-door site (site/index.html,
deployed at https://minti-pi.vercel.app). Iterative design session — the
user reviews and redirects; work T2-direct (no T3 for iteration).

Read FIRST, in order:
1. docs/plans/distribution-roadmap.md — "Sequencing after D3 + handover"
   (operational facts: Vercel project/consent, launch.json preview,
   screenshot-tool gotcha) and the D-milestone detail D3 section (the
   locked IA + review-mandated content).
2. docs/brand.md + workspace/mock/index.html — the locked visual
   language; the mock is the layout north star (top-bar idiom, surface
   colors, spacing, node-mesh motifs).
3. site/index.html — the current page (15.5 KB, single file).
4. STATUS.md "Dist D3 done" entry — how it was built + verified.

Invariants that survive ANY layout change (D0 3-LLM review, 3/3
consensus — not yours or the user's to drop casually; if the user asks,
flag the review provenance first): zero JavaScript, zero external
resources (no CDN fonts/icons), palette rules (mint = single-focus only,
cyan = links/enumerables, no gradients), the what-gets-installed
disclosure table, inline SHA-256s + checksums.txt link, the Known
warnings section (unsigned-binary honesty incl. SmartScreen/Gatekeeper
steps), honest door-A live-preview copy until D4 ships, readable at
360 px, total page < 40 KB. Door B stays visually primary.

Workflow: edit site/index.html → verify in the local preview (launch
config "site"; try preview_screenshot fresh — it hung tool-side on
2026-06-11) → user reviews in the Launch panel → iterate. Deploy to
production only with explicit user approval (the classifier enforces
this). One commit when the user calls it set: "Dist D3.1: site layout".

When the user says the site is set, hand over to D4 (Calamares +
firmware): BEFORE touching anything ISO, read
memory/reference_live_build.md (build VM is the CLONE minti-dev-2,
identify by MAC not IP; lb 3.0~a57 quirks) + the roadmap's D4 section +
locked decisions (Calamares; firmware re-enable is a HARD requirement —
revert aebdba2: archive-areas "main contrib non-free non-free-firmware",
--firmware-chroot true, --firmware-binary true). lbconfig/ +
scripts/build-iso.{sh,ps1} are the build surface. Operator gate: the
user boots the VM. The built ISO uploads to
github.com/emka21347-cyber/minti-releases and door A's status box flips.
macOS is deliberately parked until the user brings a real Mac — do not
schedule or start macOS work.
```

## Kickstart prompt for the D-milestone chat (historical — used 2026-06-11; superseded by the section above)

```
Project: MINTI — repo at C:\Users\aouad\Documents\CCode\MINT\MINT_wip (Go monorepo:
cland, runtime-adapter, workspace, status, mcp-servers, pack-manager).

Task: the Distribution milestone "D" — put MINTI's two doors on the internet.
Door B ("Add MINTI to this machine"): one-click installers for Windows/Linux/macOS
that set up cland + runtime-adapter + workspace as services, handle Ollama
(detect-or-guide), and auto-open http://127.0.0.1:8088 when done. Door A
("Revive a machine"): the lean-OS ISO — D4 adds the Calamares disk installer +
re-enables firmware. D3 is the site itself: a static two-door landing page in
site/ (MINTI brand), built + previewed locally, deployable to Vercel free tier;
big artifacts live on a free file host the page links to, NEVER on Vercel, and
Vercel NEVER runs a node.

Read FIRST, in order:
1. docs/plans/distribution-roadmap.md — the contract: tracks, locked decisions
   (Calamares; firmware re-enable is a HARD requirement; two products A+B),
   the 2026-06-11 resequencing + browser reality check, the D0..D4 phase
   table, and the operator list (Vercel `emka21347-2077` + GitHub
   `emka21347-cyber` both VERIFIED authed on the daily-driver; the one open
   decision — publish the source repo vs a separate public minti-releases
   artifacts repo — is asked at D3; build-VM boot gates D4 only).
2. STATUS.md — TL;DR + the M5-B/M5-C entries (the Windows NSSM installer and
   macOS launchd work you are wrapping) + the M9/ISO entries (door A state).
3. docs/brand.md + workspace/mock/index.html — the locked visual language the
   site MUST reuse.
4. cland/windows/nssm/{install-cland.ps1,build-zip.ps1,README} — the proven,
   peer-reviewed Windows install pattern (SHA-256-verified NSSM, strict DACL,
   firewall scoping, Defender guidance). Extend, don't reinvent.
5. install/install.sh — the Linux path; note it does NOT yet deploy the
   workspace (D2 adds that + a systemd unit mirroring minti-cland.service's
   hardening). cland/macos/ for the M5-C launchd recipes.
6. memory/MEMORY.md + memory/reference_dev_environment.md (pre-flight rule,
   PS 5.1 quirks, smoke recipe) + memory/reference_local_llms.md (T3 calling
   pattern + the load-bearing-review caveat) + memory/reference_live_build.md
   BEFORE touching anything ISO (D4: build VM is the CLONE minti-dev-2,
   identify by MAC).

Norms (non-negotiable): P1 no new external Go deps; P2 lean for 1-2 GB boxes;
P3 spec/plan wins. PEER-REVIEW BEFORE CODE — installers touch people's
machines: D0 runs the 3-LLM review (qwen3.6 think:false, deepseek-r1:32b,
gemma4:31b; copy scripts/memory-peer-review.py, outputs to scripts/m4-reviews/,
triage with folded-vs-overruled reasons) focused on installer SAFETY
(what is written/started/uninstalled per OS, Ollama handling, failure +
rollback paths, the auto-open flow) and the site's two-door IA. One commit per
phase, message "Dist D<N>: <what>". Each phase ends with go vet/go test green
in touched modules (where Go is touched), the phase's verification run, and an
honest STATUS.md update. Delegation: T2 = installer logic, service wiring,
anything that writes to user machines; T3 via scripts/t3-codegen.py = the
static site's first-pass HTML/CSS (tight spec; review line-by-line — M4's
delegation shipped 2 silent logic bugs that only review caught).

Door-B safety bar (from the M5-B precedent): explicit uninstall that preserves
state by default, no silent destructive steps, every downloaded binary
checksum-verified, services hardened like the existing units. The Windows
installer must coexist with an already-installed M5-B service (upgrade, don't
double-install — the daily-driver HAS one running, pre-Clan, PID varies).

Do NOW: (1) read the items above; (2) execute D0 — write the D-milestone plan
detail (per-OS install/uninstall/upgrade flows, Ollama strategy, site IA) into
docs/plans/distribution-roadmap.md, run the 3-LLM review on it, write the
triage, fold consensus findings, commit "Dist D0: plan + 3-LLM peer review";
(3) then D1. Do not start a phase until the prior phase's verification passes
and it is committed. Verify door-B installers on THIS machine (the daily
driver) only after the D0 review blesses the upgrade-over-M5-B path; never
test destructive paths on the daily driver.
```
