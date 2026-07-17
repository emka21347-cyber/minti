# iso/ — the MINTI Desktop image

Populated in R1/R2 (see `docs/plans/rework.md`). This will hold the modern
live-build tree for `minti-desktop` (and later `minti-node`).

## Carry-forward lessons from the old build (archive/lbconfig)

Learned the hard way on lb 3.0~a57; most quirks die with builder v2, these
do not:

1. **umask 022 during builds.** A umask-007 build once created `/usr` as
   0770 in the rootfs — no non-root user could exec anything. Days lost.
2. **Explicit firmware packages.** With `--apt-recommends false`,
   metapackages will NOT pull firmware-iwlwifi/realtek/atheros/brcm80211/
   misc-nonfree — list each one.
3. **Prebuilt broadcom-sta `wl.ko`** for BCM4360 (2013–2015 MacBooks):
   compile once at build time, ship the ~3 MB module. No dkms + headers +
   gcc on the image (~150 MB), and never firmware-b43-installer (downloads
   from the internet at install time — chicken-and-egg).
4. **BIOS+UEFI hybrid is mandatory.** Old MacBooks are EFI-only; a
   syslinux-only ISO silently can't boot them.
5. **Deterministic live user.** Don't trust live-config to create the user;
   create it in a hook (the old hook: user/live, NOPASSWD sudo, unlocked,
   no expiry).
6. **NetworkManager on the desktop image** — first-boot WiFi UX is the
   product's front door.
