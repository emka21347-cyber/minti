#!/usr/bin/env bash
# Build the minti-cland macOS distribution tarball(s).
#
# Cross-compiles the cland binary for darwin/amd64 + darwin/arm64,
# then bundles each with the plist + install/uninstall scripts +
# README into dist/minti-cland-darwin-{amd64,arm64}-v$VERSION.tar.gz.
#
# Runs on macOS, Linux, or Windows (via Git Bash). The Go toolchain
# is the only required dependency; we use `tar` if available, fall
# back to `python3 -m tarfile` otherwise.

set -euo pipefail

VERSION="${VERSION:-0.2.0-M5}"
GO="${GO:-go}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAND_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$CLAND_DIR/.." && pwd)"
DIST_DIR="$REPO_ROOT/dist"
STAGE_AMD64="$DIST_DIR/minti-cland-darwin-amd64-v$VERSION"
STAGE_ARM64="$DIST_DIR/minti-cland-darwin-arm64-v$VERSION"

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }

mkdir -p "$DIST_DIR" "$CLAND_DIR/dist"

# ---------- cross-compile ----------

LDFLAGS="-X main.version=$VERSION -s -w"

info "Cross-compiling cland for darwin/amd64 + darwin/arm64 (version=$VERSION)"
(
    cd "$CLAND_DIR"
    GOOS=darwin GOARCH=amd64 "$GO" build -trimpath -ldflags "$LDFLAGS" \
        -o "dist/minti-cland-darwin-amd64" ./cmd/minti-cland
    GOOS=darwin GOARCH=arm64 "$GO" build -trimpath -ldflags "$LDFLAGS" \
        -o "dist/minti-cland-darwin-arm64" ./cmd/minti-cland
)
ok "minti-cland-darwin-amd64"
ok "minti-cland-darwin-arm64"

# ---------- stage trees ----------

stage_one() {
    local arch="$1"
    local stage_dir="$2"
    local bin="$CLAND_DIR/dist/minti-cland-darwin-$arch"

    info "Staging $arch tree"
    rm -rf "$stage_dir"
    mkdir -p "$stage_dir/bin" "$stage_dir/configs"

    install -m 0755 "$bin" "$stage_dir/bin/minti-cland"
    install -m 0644 "$SCRIPT_DIR/com.minti.cland.plist"      "$stage_dir/com.minti.cland.plist"
    install -m 0644 "$SCRIPT_DIR/cland.yaml.darwin.example"  "$stage_dir/configs/cland.yaml.darwin.example"
    install -m 0644 "$SCRIPT_DIR/README.md"                  "$stage_dir/README.md"
    install -m 0755 "$SCRIPT_DIR/install-cland.sh"           "$stage_dir/install-cland.sh"
    install -m 0755 "$SCRIPT_DIR/uninstall-cland.sh"         "$stage_dir/uninstall-cland.sh"

    # Optional: include the reasoning-scores rubric if it exists alongside
    # the cland repo (currently the canonical sample lives elsewhere on
    # Linux; ship a minimal stub if absent).
    if [[ -f "$CLAND_DIR/configs/reasoning-scores.yaml.example" ]]; then
        install -m 0644 "$CLAND_DIR/configs/reasoning-scores.yaml.example" \
            "$stage_dir/configs/reasoning-scores.yaml.example"
    fi

    ok "$stage_dir"
}

stage_one amd64 "$STAGE_AMD64"
stage_one arm64 "$STAGE_ARM64"

# ---------- tar.gz ----------

make_tarball() {
    local stage_dir="$1"
    local out="$2"
    local parent="$(dirname "$stage_dir")"
    local base="$(basename "$stage_dir")"

    if command -v tar >/dev/null 2>&1; then
        rm -f "$out"
        tar -C "$parent" -czf "$out" "$base"
    else
        # Python fallback for Windows Git Bash setups without tar.
        rm -f "$out"
        python3 -c "
import tarfile, os, sys
src = sys.argv[1]
dst = sys.argv[2]
base = os.path.basename(src)
with tarfile.open(dst, 'w:gz') as t:
    t.add(src, arcname=base)
" "$stage_dir" "$out"
    fi
}

info "Compressing tarballs"
TAR_AMD64="$DIST_DIR/minti-cland-darwin-amd64-v$VERSION.tar.gz"
TAR_ARM64="$DIST_DIR/minti-cland-darwin-arm64-v$VERSION.tar.gz"
make_tarball "$STAGE_AMD64" "$TAR_AMD64"
make_tarball "$STAGE_ARM64" "$TAR_ARM64"
ok "$TAR_AMD64"
ok "$TAR_ARM64"

# ---------- summary ----------

# SHA-256 helper that works on both shasum (macOS) and sha256sum (Linux).
sha256_of() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        echo "(no shasum / sha256sum available)"
    fi
}

echo
echo "Build artefacts:"
printf '  %s\n    sha256: %s\n' "$TAR_AMD64" "$(sha256_of "$TAR_AMD64")"
printf '  %s\n    sha256: %s\n' "$TAR_ARM64" "$(sha256_of "$TAR_ARM64")"
echo
echo "Operator workflow (system install):"
echo "  tar -xzf $TAR_AMD64 -C /tmp"
echo "  cd /tmp/$(basename "$STAGE_AMD64")"
echo "  sudo ./install-cland.sh"
