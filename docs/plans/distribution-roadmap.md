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
