# MINTI Rework — "the ISO is the product"

Decided 2026-07-17. This is the working plan; PRD.md holds the durable
decisions.

## Vision

Boot a laptop into a MINTI XFCE desktop. Talk to the agent that lives there.
The UX improves version by version through those conversations — and each
version gets flashed onto real hardware. Everything else serves that loop.

## R0 — Archive + fresh scaffold — DONE 2026-07-17

- All pre-rework work → `archive/` (git mv, history intact).
- New root: PRD.md, STATUS.md, CLAUDE.md (handover protocol), CHANGELOG.md,
  README.md, iso/, docs/plans/.
- Public surfaces (minti-pi.vercel.app via `site/`, GitHub README) replaced
  with brand-consistent "rebuilding" placeholders.
- VirtualBox test VM powered down; old media deregistered.

## R1 — Builder v2

Retire the lb 3.0~a57 build VM. Stand up a fresh **Debian 12 VM** with
modern live-build. One move dissolves the old quirk list (isolinux template
hacks, hook-location trap, isohybrid, the firmware-download bug that forced
`--firmware-* false`) and gives **native BIOS+UEFI hybrid ISOs** — required
because the MacBooks are EFI-only.

Keep the hard-won lessons: umask 022 during builds (the /usr 0770 saga),
deterministic user-creation hook, boot=live append.

**Exit test:** a plain Debian XFCE live ISO built on the new VM boots in a
BIOS VirtualBox VM *and* an EFI VirtualBox VM.

## R2 — MINTI Desktop v0.1

Debian Bookworm +:
- **Firmware, explicit list** (~50 MB): firmware-iwlwifi, firmware-realtek,
  firmware-atheros, firmware-brcm80211, firmware-misc-nonfree,
  intel-microcode, amd64-microcode. `--apt-recommends false` means every one
  must be listed explicitly.
- **Prebuilt broadcom-sta `wl.ko`** (compiled once at build time, ~3 MB) for
  BCM4360 Macs (2013–2015). No dkms/toolchain on the image. Never
  firmware-b43-installer (needs internet at install time).
- **XFCE** + lightdm autologin, **NetworkManager** + applet, Firefox-ESR.
- minti binaries from archive (cland, workspace, runtime, mcp servers,
  minti-fetch with the **tightened ▚** — halves must touch, current mark has
  a 6-space gap).
- Branding pass on Greybird-dark; dashboard/agent autostarts in the browser.

**Exit test:** boots BIOS + EFI in VM (8 GB MacBook profile), WiFi firmware
present (`ls /lib/firmware`), desktop up, agent reachable on-screen.

## R3 — Flash round 1 (real hardware)

| Order | Machine | Why |
|---|---|---|
| 1 | Lenovo Y (2017/18) | standard UEFI PC, Intel WiFi, nouveau — easiest |
| 2 | MacBook 2013–2015 | proves EFI boot + wl driver, the whole point |
| 3 | MacBook 2016–2017 | brcmfmac + applespi, likely fine |
| — | T2 Macs (2018–2020) | stretch, needs t2linux out-of-tree |
| — | Apple Silicon (2020+) | impossible on amd64 ISO, parked |

Field notes feed v0.2.

## R4 — The standing loop

Founder on the laptop, talking to the agent → feedback → UX change → new ISO
version. Permanent cadence, not a milestone.

## R5+ — Deferred, in order

1. **Calamares installer** — live USB forgets everything; daily-driver use
   needs a real install.
2. Cinnamon skin tier for capable machines (user likes the sleeker look;
   XFCE stays default for weak hardware).
3. T2 Mac support.
4. Updates channel (apt repo or image-based).

## Untouched by the rework

Live Clan on the founder PC (192.168.1.195:7777), v0.5.1 lineage, public
repo history. Migrate consciously, later.
