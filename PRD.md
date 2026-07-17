# MINTI — Product Requirements (rework era)

Status: **active** · Started 2026-07-17 · Supersedes the pre-rework PRD v0.6
(external: `~/.claude/plans/hello-can-we-create-abundant-hopper.md`) for
direction; inherits its principles (open-source-first, lean enough to revive
old hardware).

## What MINTI is now

**The ISO is the product.** MINTI is a bootable Linux desktop (Debian base,
XFCE) with a resident AI agent. You flash it onto an old laptop, boot it, and
talk to the agent — and the user experience improves version by version
through those conversations. The Clan protocol (distributed trust group for
AI compute) remains the substrate underneath; the desktop-with-agent is the
face.

## The core loop

```
build ISO vN → boot (VM, then real laptop) → founder talks to the on-device
agent → feedback distilled → UX changes → ISO vN+1
```

Every flashable version is logged in `CHANGELOG.md`. The loop is permanent —
not a milestone.

## Variants

| Variant | DE | Target | Role |
|---|---|---|---|
| `minti-node` | none (console) | 1–2 GB e-waste | silent Clan worker, LAN-managed |
| `minti-desktop` | XFCE (Cinnamon tier later) | 4 GB+ laptops | daily driver, agent on-screen |

## Locked decisions

| # | Decision | Why |
|---|---|---|
| 1 | Debian Bookworm base stays | All build fixes (/usr perms, user hook, boot params) survive; no distro rebase |
| 2 | XFCE first; Cinnamon later as a skin tier | Lightest complete DE, no GPU dependency, proves the desktop plumbing; Cinnamon needs compositing |
| 3 | WiFi/ethernet firmware baked in, explicit package list | `--apt-recommends false` means metapackages won't pull them; firmware is the connectivity bootstrap (no chicken-and-egg) |
| 4 | Broadcom `wl` for 2013-era Macs ships as a **prebuilt .ko** | DKMS route drags a ~150 MB toolchain for one module; prebuilt is ~3 MB. Never `firmware-b43-installer` (downloads at install time) |
| 5 | ISO must boot **BIOS + UEFI hybrid** | Old MacBooks are EFI-only; current syslinux-only image cannot boot them at all |
| 6 | Builder v2: fresh Debian 12 build VM, modern live-build | Retires the lb 3.0~a57 quirk farm; native hybrid boot + working firmware handling |
| 7 | x86_64 only | Apple Silicon (2020+) is arm64/Asahi-only — out of scope |
| 8 | NetworkManager on desktop variant | WiFi UX is first-boot-critical |
| 9 | Calamares installer (R5) before "daily driver" claims | Live USB loses all state on reboot |
| 10 | No device detection / per-device ISOs | Kernel autoloads drivers if blobs are present; universal image is more reliable than a picker (~50 MB total cost) |

## Target fleet (founder's real hardware)

| Machine | Expectation |
|---|---|
| Lenovo Y-series 2017/18 | Easiest; flash first (UEFI, Intel WiFi, nouveau) |
| MacBook 2013–2015 | Prime target; needs EFI boot + `wl` driver |
| MacBook 2016–2017 | Should work; brcmfmac WiFi + applespi keyboard (in Debian 6.1 kernel) |
| MacBook 2018–2020 (T2) | Stretch only; SSD/keyboard behind T2 need out-of-tree t2linux |
| MacBook 2020+ (M1/M2) | Will not work — different CPU architecture |

## Phases

- [x] **R0** — Archive pre-rework work, scaffold new root, dummy the public site (2026-07-17)
- [ ] **R1** — Builder v2: Debian 12 build VM + modern live-build; plain XFCE ISO boots BIOS **and** EFI in VirtualBox
- [ ] **R2** — MINTI Desktop v0.1: firmware set, XFCE + lightdm autologin, NetworkManager, Firefox-ESR, minti binaries from archive, branding (tight ▚), dashboard/agent autostart
- [ ] **R3** — Flash round 1: Lenovo, then 2013 MacBook; field notes → v0.2
- [ ] **R4** — Standing agent-on-desktop UX loop (permanent cadence)
- [ ] **R5+** — Calamares installer, Cinnamon tier, T2 Macs, updates channel

## Parked / out of scope

M2 remote access (Vercel-login MVP), Android node, WAN transport, Apple
Silicon. The live Clan on the founder PC and the v0.5.1 tester lineage stay
as-is until consciously migrated.

## Prior work

Everything pre-rework lives in `archive/` (history intact). The Go stack
(cland, workspace, runtime, mcp-servers) still builds and ships onto the new
ISO as binaries; code graduates out of archive only when actually reworked.
v0.5.1 is tagged on the public repo.
