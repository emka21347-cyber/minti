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

## Kickstart prompt for the D-milestone chat (paste verbatim)

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
