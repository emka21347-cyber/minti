#!/bin/bash
# Build a bootable MINTI live ISO from Debian Bookworm.
# Requires: live-build, sudo, inkscape or resvg (for PNG export), grub-common (grub-mkfont)
#
# Usage (from repo root):
#   sudo bash scripts/build-iso.sh [--skip-png] [--skip-font]
#
# Output: build/iso/minti-bookworm-amd64.hybrid.iso
# Config source: lbconfig/  (source-controlled)
# Build workdir: build/iso/ (gitignored, created here)
set -euo pipefail

# Force a sane umask. If the build runs with umask 007 (some shells/sudo envs),
# `mkdir -p` for the includes.chroot staging dirs creates parents like /usr at
# 0770, and live-build applies that to the real rootfs — leaving /usr
# non-traversable by normal users (breaks all non-root exec + login). 022
# guarantees staged dirs are 0755. (The chroot hook also normalizes perms as a
# belt-and-braces backstop.)
umask 022

REPO=${REPO:-$(git rev-parse --show-toplevel)}
LBCONFIG="$REPO/lbconfig"    # source-controlled lb config tree
BUILD_DIR="$REPO/build/iso"  # lb build working dir + ISO output (gitignored)
ASSETS="$REPO/assets"

log()  { echo -e "\033[38;5;42m[build-iso]\033[0m $*"; }
warn() { echo -e "\033[38;5;220m[build-iso WARN]\033[0m $*"; }
die()  { echo -e "\033[38;5;196m[build-iso ERROR]\033[0m $*" >&2; exit 1; }

SKIP_PNG=0
SKIP_FONT=0
for arg in "$@"; do
    case "$arg" in
        --skip-png)  SKIP_PNG=1 ;;
        --skip-font) SKIP_FONT=1 ;;
    esac
done

# ── pre-flight ────────────────────────────────────────────────────────────────
[[ $EUID -eq 0 ]] || die "live-build requires root. Run: sudo bash scripts/build-iso.sh"
command -v lb >/dev/null 2>&1 || die "live-build not found. Install: apt install live-build"
[[ -f /usr/lib/ISOLINUX/isolinux.bin ]] || die "isolinux not found. Install: apt install isolinux"

# ── rasterize visual assets (requires inkscape or resvg) ─────────────────────
if [[ $SKIP_PNG -eq 0 ]]; then
    RASTERIZER=""
    if command -v resvg >/dev/null 2>&1; then
        RASTERIZER="resvg"
    elif command -v inkscape >/dev/null 2>&1; then
        RASTERIZER="inkscape"
    else
        warn "No rasterizer found (resvg or inkscape). Skipping PNG export. Use --skip-png to suppress."
    fi

    if [[ -n "$RASTERIZER" ]]; then
        log "Rasterizing wallpaper..."
        SVG="$ASSETS/wallpaper-3840x2160.svg"
        if [[ "$RASTERIZER" == "resvg" ]]; then
            resvg -w 2560 -h 1440 "$SVG" "$ASSETS/wallpaper-2560x1440.png"
            resvg -w 1920 -h 1080 "$SVG" "$ASSETS/wallpaper-1920x1080.png"
            resvg -w 1920 -h 1080 "$SVG" "$ASSETS/grub-theme/minti/background.png"
        else
            inkscape --export-type=png --export-width=2560 --export-height=1440 \
                --export-filename="$ASSETS/wallpaper-2560x1440.png" "$SVG" 2>/dev/null
            inkscape --export-type=png --export-width=1920 --export-height=1080 \
                --export-filename="$ASSETS/wallpaper-1920x1080.png" "$SVG" 2>/dev/null
            inkscape --export-type=png --export-width=1920 --export-height=1080 \
                --export-filename="$ASSETS/grub-theme/minti/background.png" "$SVG" 2>/dev/null
        fi

        log "Rasterizing Plymouth assets..."
        POUT="$ASSETS/plymouth/minti"
        if [[ "$RASTERIZER" == "resvg" ]]; then
            resvg -h 256 "$ASSETS/logo.svg" "$POUT/minti-wordmark.png"
        else
            inkscape --export-type=png --export-height=256 \
                --export-filename="$POUT/minti-wordmark.png" "$ASSETS/logo.svg" 2>/dev/null
        fi
        # node.png: 14×14 cyan circle (used in Plymouth animation)
        if command -v convert >/dev/null 2>&1; then
            convert -size 14x14 xc:none \
                -fill '#5fd7ff' -draw 'circle 7,7 7,1' \
                "$POUT/node.png" 2>/dev/null && log "node.png generated"
        else
            warn "ImageMagick not found — $POUT/node.png must be created manually"
        fi

        # GRUB selection highlight pixmaps (minimal 10×40 solid-color fill)
        GRUB_THEME_DIR="$ASSETS/grub-theme/minti"
        if command -v convert >/dev/null 2>&1; then
            for pix in select_c select_e select_w; do
                [[ -f "$GRUB_THEME_DIR/${pix}.png" ]] && continue
                convert -size 10x40 xc:'#00d787' "$GRUB_THEME_DIR/${pix}.png" 2>/dev/null
            done
        fi
    fi
fi

# ── generate GRUB font ────────────────────────────────────────────────────────
if [[ $SKIP_FONT -eq 0 ]]; then
    FONT_SRC=""
    for path in \
        /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf \
        /usr/share/fonts/dejavu/DejaVuSans-Bold.ttf \
        /usr/share/fonts/truetype/DejaVuSans-Bold.ttf; do
        [[ -f "$path" ]] && FONT_SRC="$path" && break
    done
    if [[ -n "$FONT_SRC" ]]; then
        log "Generating GRUB fonts from $FONT_SRC..."
        grub-mkfont -s 18 -o "$ASSETS/grub-theme/minti/minti.pf2"   "$FONT_SRC"
        grub-mkfont -s 12 -o "$ASSETS/grub-theme/minti/minti12.pf2" "$FONT_SRC"
    else
        warn "DejaVu Sans Bold not found; GRUB font not generated. Install fonts-dejavu-core."
    fi
fi

# ── sync lbconfig → build workdir ─────────────────────────────────────────────
log "Syncing lbconfig/ → build/iso/..."
mkdir -p "$BUILD_DIR"
rsync -a --delete "$LBCONFIG/" "$BUILD_DIR/"

# This live-build (3.0~a57) globs hooks at config/hooks/*.chroot, NOT the modern
# config/hooks/normal/*.hook.chroot. Flatten our normal hooks so they run.
if compgen -G "$BUILD_DIR/config/hooks/normal/*.chroot" >/dev/null; then
    log "Flattening config/hooks/normal/ → config/hooks/ (lb 3.0 convention)..."
    cp "$BUILD_DIR"/config/hooks/normal/*.chroot "$BUILD_DIR/config/hooks/"
fi

# ── stage visual assets into chroot includes ──────────────────────────────────
log "Staging visual assets..."

CHROOT_PLY="$BUILD_DIR/config/includes.chroot/usr/share/plymouth/themes/minti"
CHROOT_GRUB="$BUILD_DIR/config/includes.chroot/usr/share/grub/themes/minti"
mkdir -p "$CHROOT_PLY" "$CHROOT_GRUB"
cp -r "$ASSETS/plymouth/minti/."   "$CHROOT_PLY/"
cp -r "$ASSETS/grub-theme/minti/." "$CHROOT_GRUB/"

# GRUB theme in bootloader overlay so live-ISO menu is also themed
mkdir -p "$BUILD_DIR/config/bootloaders/grub-pc/themes/minti"
cp -r "$ASSETS/grub-theme/minti/." "$BUILD_DIR/config/bootloaders/grub-pc/themes/minti/"

# ── stage install.sh into chroot and binary ───────────────────────────────────
log "Staging install.sh..."
mkdir -p "$BUILD_DIR/config/includes.chroot/tmp"
cp "$REPO/install/install.sh" "$BUILD_DIR/config/includes.chroot/tmp/install.sh"
mkdir -p "$BUILD_DIR/config/includes.binary/minti-install"
cp "$REPO/install/install.sh" "$BUILD_DIR/config/includes.binary/minti-install/install.sh"

# ── stage pre-built MINTI binaries into chroot ────────────────────────────────
# Binaries must already exist (cross-compiled on host or native build).
# These are copied into /usr/local/bin and /opt/minti/mcp inside the live chroot
# so minti-fetch, minti-cland, etc. work immediately after live boot.
log "Staging MINTI binaries..."
CHROOT_USR="$BUILD_DIR/config/includes.chroot/usr/local/bin"
CHROOT_MCP="$BUILD_DIR/config/includes.chroot/opt/minti/mcp"
CHROOT_SVC="$BUILD_DIR/config/includes.chroot/etc/systemd/system"
mkdir -p "$CHROOT_USR" "$CHROOT_MCP" "$CHROOT_SVC"

# Helper: find the best available binary (native > cross-compiled > skip)
stage_bin() {
    local name="$1" dest="$2" candidates=("${@:3}")
    for src in "${candidates[@]}"; do
        if [[ -f "$src" ]]; then
            install -m 0755 "$src" "$dest/$name"
            log "  $name → $dest/$name"
            return 0
        fi
    done
    warn "  $name not found (skipping); run 'make ${name/minti-/}-linux' first"
}

stage_bin minti-runtime     "$CHROOT_USR" \
    "$REPO/runtime-adapter/minti-runtime" \
    "$REPO/runtime-adapter/dist/minti-runtime-linux-amd64"

stage_bin minti-cland       "$CHROOT_USR" \
    "$REPO/cland/minti-cland" \
    "$REPO/cland/dist/minti-cland-linux-amd64"

stage_bin minti-pack-fetch  "$CHROOT_USR" \
    "$REPO/pack-manager/minti-pack-fetch" \
    "$REPO/pack-manager/dist/minti-pack-fetch-linux-amd64"

for mcp in minti-mcp-fs minti-mcp-shell minti-mcp-recon minti-mcp-http minti-mcp-pkg minti-mcp-wiki; do
    stage_bin "$mcp" "$CHROOT_MCP" \
        "$REPO/mcp-servers/$mcp" \
        "$REPO/mcp-servers/dist/${mcp}-linux-amd64"
done

# Stage systemd units (install.sh in MINTI_CHROOT mode skips service start but
# the .service files must be present so `systemctl enable` works on first boot)
for unit in \
    "$REPO/runtime-adapter/systemd/minti-runtime.service" \
    "$REPO/cland/systemd/minti-cland.service"; do
    [[ -f "$unit" ]] && install -m 0644 "$unit" "$CHROOT_SVC/$(basename $unit)" || \
        warn "  $(basename $unit) not found"
done

# Stage minti-fetch bash script
CHROOT_BRANDING="$BUILD_DIR/config/includes.chroot/usr/local/bin"
if [[ -f "$REPO/branding/minti-fetch" ]]; then
    install -m 0755 "$REPO/branding/minti-fetch" "$CHROOT_BRANDING/minti-fetch"
    log "  minti-fetch → $CHROOT_BRANDING/minti-fetch"
fi

# ── fix syslinux/isolinux bootloader template ────────────────────────────────
# The system-installed live-build template has 2012-era symlinks pointing at
# /usr/lib/syslinux/{isolinux.bin,vesamenu.c32} — bookworm moved these to
# /usr/lib/ISOLINUX/ and /usr/lib/syslinux/modules/bios/. Create a local
# bootloader override with actual files from the host.
log "Creating isolinux bootloader override..."
ISOLINUX_DIR="$BUILD_DIR/config/bootloaders/isolinux"
mkdir -p "$ISOLINUX_DIR"

# Binaries from host (the system lb template has broken 2012-era symlinks)
cp /usr/lib/ISOLINUX/isolinux.bin                   "$ISOLINUX_DIR/"
cp /usr/lib/syslinux/modules/bios/vesamenu.c32      "$ISOLINUX_DIR/"
cp /usr/lib/syslinux/modules/bios/ldlinux.c32       "$ISOLINUX_DIR/"
cp /usr/lib/syslinux/modules/bios/libcom32.c32      "$ISOLINUX_DIR/"
cp /usr/lib/syslinux/modules/bios/libutil.c32       "$ISOLINUX_DIR/"

# Config files — written directly (no gfxboot/bootlogo/splash.svg.in,
# which crash on bookworm because the system template needs `rsvg`)
cat > "$ISOLINUX_DIR/isolinux.cfg" <<'ISOCFG'
include menu.cfg
default vesamenu.c32
prompt 0
timeout 50
ISOCFG

cat > "$ISOLINUX_DIR/menu.cfg" <<'MENUCFG'
menu hshift 0
menu width 82
menu title MINTI Live
include stdmenu.cfg
include live.cfg
MENUCFG

cat > "$ISOLINUX_DIR/stdmenu.cfg" <<'STDCFG'
menu background #1e1e2e
menu color title        1;36;44    #ff5fd7ff #00000000 std
menu color sel          7;37;40    #ff00d787 #ff1e1e2e all
menu color unsel        37;44      #ffcdd6f4 #ff1e1e2e std
menu color hotsel       1;7;37;40  #ff00d787 #ff1e1e2e all
menu color hotkey       1;36;44    #ff5fd7ff #00000000 std
menu color timeout_msg  37;40      #ffcdd6f4 #ff1e1e2e std
menu color timeout      1;37;40    #ff00d787 #ff1e1e2e std
menu color tabmsg       37;40      #ff6c7086 #ff1e1e2e std
STDCFG

cat > "$ISOLINUX_DIR/live.cfg.in" <<'LIVECFG'
label live-@FLAVOUR@
  menu label ^MINTI Live (@FLAVOUR@)
  menu default
  linux @KERNEL@
  initrd @INITRD@
  append @LB_BOOTAPPEND_LIVE@

label live-@FLAVOUR@-failsafe
  menu label MINTI Live (@FLAVOUR@ failsafe)
  linux @KERNEL@
  initrd @INITRD@
  append @LB_BOOTAPPEND_FAILSAFE@
LIVECFG

# Valid empty cpio archive — prevents lb's gfxboot hack from crashing
cpio --quiet -o < /dev/null > "$ISOLINUX_DIR/bootlogo"

# ── build ─────────────────────────────────────────────────────────────────────
cd "$BUILD_DIR"
log "lb clean (preserving chroot cache)..."
lb clean 2>&1 | tail -3

log "lb config..."
bash auto/config

log "lb build (may take 10–30 min)..."
lb build 2>&1 | tee "$BUILD_DIR/build.log" | grep -E '^\[|^P|[Ee]rror|[Ww]arn' || true

ISO=$(ls "$BUILD_DIR"/minti-bookworm-amd64.hybrid.iso 2>/dev/null \
    || ls "$BUILD_DIR"/live-image-amd64.hybrid.iso 2>/dev/null \
    || ls "$BUILD_DIR"/binary.hybrid.iso 2>/dev/null || echo "")
if [[ -n "$ISO" && -f "$ISO" ]]; then
    SIZE=$(du -h "$ISO" | cut -f1)
    log "Done. $ISO ($SIZE)"
    log "Flash: dd if=$ISO of=/dev/sdX bs=4M status=progress oflag=sync"
else
    die "Build complete but ISO not found. Check: $BUILD_DIR/build.log"
fi
