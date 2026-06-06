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

REPO=$(git rev-parse --show-toplevel)
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

# ── build ─────────────────────────────────────────────────────────────────────
cd "$BUILD_DIR"
log "lb clean..."
lb clean --all 2>&1 | tail -3

log "lb config..."
bash auto/config

log "lb build (may take 10–30 min)..."
lb build 2>&1 | tee "$BUILD_DIR/build.log" | grep -E '^\[|^P|[Ee]rror|[Ww]arn' || true

ISO=$(ls "$BUILD_DIR"/minti-bookworm-amd64.hybrid.iso 2>/dev/null \
    || ls "$BUILD_DIR"/live-image-amd64.hybrid.iso 2>/dev/null || echo "")
if [[ -n "$ISO" && -f "$ISO" ]]; then
    SIZE=$(du -h "$ISO" | cut -f1)
    log "Done. $ISO ($SIZE)"
    log "Flash: dd if=$ISO of=/dev/sdX bs=4M status=progress oflag=sync"
else
    die "Build complete but ISO not found. Check: $BUILD_DIR/build.log"
fi
