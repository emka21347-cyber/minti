# MINTI STATUS — rework log

Append-only, newest first. Every working session ends with an entry here.
Pre-rework chronicle: `archive/STATUS.md`.

---

## 2026-07-17 — R0: the pivot, archive, scaffold, public dummies

**Done**
- Decided the rework (plan: `docs/plans/rework.md`): the ISO is the product —
  XFCE desktop + resident agent, versions flashed onto real laptops.
- Archived everything pre-rework into `archive/` (399 files, `git mv`,
  history intact). Root rebuilt: PRD.md, STATUS.md, CLAUDE.md, CHANGELOG.md,
  README.md, iso/, docs/plans/.
- Replaced the public site (minti-pi.vercel.app, deploys from `site/`) with a
  brand-consistent "rebuilding" placeholder; new public README likewise.
- VirtualBox cleanup: MINTI-LiveTest powered off, all media in
  `archive/build/` detached + deregistered (safe to move/delete).
- Earlier same session: verified old ISO boots in VM (BIOS, 8 GB MacBook
  profile), diagnosed MacBook WiFi gap (no firmware baked + BCM4360 needs
  `wl`), diagnosed EFI gap (syslinux-only ISO can't boot EFI-only Macs).

**Next**
- R1: stand up builder v2 — fresh Debian 12 VirtualBox VM with modern
  live-build; exit test = plain XFCE live ISO boots in BIOS VM **and** EFI VM.

**Blockers / notes**
- Push of these commits = prod deploy (public repo + Vercel auto-deploy).
  User consented 2026-07-17 ("make them dummies").
- Old build VM (minti-dev-2) still holds the lb 3.0~a57 setup; keep until R1
  proves out.
- The old ISOs + test VDI live in `archive/build/` (gitignored, on disk).
