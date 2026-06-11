#!/usr/bin/env bash
# Build the full-stack MINTI macOS distribution tarballs (door B, Dist D2).
# Produces dist/minti-macos-{arm64,amd64}-v<VERSION>.tar.gz.
#
# Pure-Go cross-compiles; runs anywhere bash + go exist (Linux, Git Bash
# on Windows, macOS). Tarball layout (install-minti.sh expects exactly
# this, all paths script-relative):
#   install-minti.sh  uninstall-minti.sh  README.md
#   com.minti.{cland,runtime,workspace}.plist
#   bin/{minti-cland,minti-runtime,minti-workspace}
#   configs/{cland.yaml.darwin.example,runtime.yaml.darwin.example,
#            reasoning-scores.yaml.example}

set -euo pipefail

VERSION="${VERSION:-0.4.0-D1}"
GO="${GO:-go}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
dist_dir="${repo_root}/dist"
mkdir -p "${dist_dir}"

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }

# make_tarball <dir> <out.tar.gz> — python tarfile with an explicit mode
# filter: the ONLY deterministic way to get correct modes when building
# on Git Bash/NTFS (chmod is a no-op there; MSYS fakes POSIX modes from a
# content heuristic, so darwin Mach-O binaries read back 0644). root:root;
# dirs 0755; bin/* + *.sh -> 0755; everything else 0644.
make_tarball() {
    local dir="$1" out="$2" py
    rm -f "${out}"
    py="$(command -v python3 || command -v python)" || {
        printf '  \033[31mERROR:\033[0m python3/python required to build the tarball\n' >&2
        exit 1
    }
    "${py}" - "${dir}" "${out}" <<'EOF'
import os, sys, tarfile
src, out = sys.argv[1], sys.argv[2]
def fix(ti):
    ti.uid = ti.gid = 0
    ti.uname = ti.gname = "root"
    if ti.isdir():
        ti.mode = 0o755
    elif "/bin/" in ti.name or ti.name.endswith(".sh"):
        ti.mode = 0o755
    else:
        ti.mode = 0o644
    return ti
with tarfile.open(out, "w:gz") as t:
    t.add(src, arcname=os.path.basename(src), filter=fix)
EOF
}

for arch in arm64 amd64; do
    stage="${dist_dir}/minti-macos-${arch}-v${VERSION}"
    rm -rf "${stage}"
    mkdir -p "${stage}/bin" "${stage}/configs"

    info "cross-compiling for darwin-${arch}"
    ( cd "${repo_root}/cland" && GOOS=darwin GOARCH="${arch}" "${GO}" build -trimpath \
        -ldflags "-X main.version=${VERSION} -s -w" -o "${stage}/bin/minti-cland" ./cmd/minti-cland )
    ( cd "${repo_root}/runtime-adapter" && GOOS=darwin GOARCH="${arch}" "${GO}" build -trimpath \
        -ldflags "-s -w" -o "${stage}/bin/minti-runtime" ./cmd/minti-runtime )
    ( cd "${repo_root}/workspace" && GOOS=darwin GOARCH="${arch}" "${GO}" build -trimpath \
        -ldflags "-X main.version=${VERSION} -s -w" -o "${stage}/bin/minti-workspace" ./cmd/minti-workspace )
    ok "3 binaries (darwin-${arch})"

    cp "${script_dir}/install-minti.sh"   "${stage}/install-minti.sh"
    cp "${script_dir}/uninstall-minti.sh" "${stage}/uninstall-minti.sh"
    cp "${script_dir}/README.md"          "${stage}/README.md"
    cp "${repo_root}/cland/darwin/com.minti.cland.plist" "${stage}/com.minti.cland.plist"
    cp "${script_dir}/com.minti.runtime.plist"   "${stage}/com.minti.runtime.plist"
    cp "${script_dir}/com.minti.workspace.plist" "${stage}/com.minti.workspace.plist"
    cp "${repo_root}/cland/darwin/cland.yaml.darwin.example" "${stage}/configs/cland.yaml.darwin.example" 2>/dev/null || \
        cp "${repo_root}/cland/configs/cland.yaml.example" "${stage}/configs/cland.yaml.darwin.example"
    cp "${script_dir}/configs/runtime.yaml.darwin.example" "${stage}/configs/runtime.yaml.darwin.example"
    cp "${repo_root}/cland/configs/reasoning-scores.yaml.example" "${stage}/configs/reasoning-scores.yaml.example"
    # Force +x on binaries too — Go-on-Windows (Git Bash) writes non-.exe
    # outputs without the executable bit (same fix as the Linux builder).
    chmod 0755 "${stage}/bin/"* "${stage}/install-minti.sh" "${stage}/uninstall-minti.sh"
    ok "bundle staged (+x forced on bin/* and scripts)"

    out="${dist_dir}/minti-macos-${arch}-v${VERSION}.tar.gz"
    info "creating ${out}"
    make_tarball "${stage}" "${out}"
    ok "${out}"
done

echo
echo 'checksums.txt lines (site D3):'
for arch in arm64 amd64; do
    f="${dist_dir}/minti-macos-${arch}-v${VERSION}.tar.gz"
    if command -v sha256sum >/dev/null 2>&1; then
        sum="$(sha256sum "${f}" | awk '{print $1}')"
    else
        sum="$(shasum -a 256 "${f}" | awk '{print $1}')"
    fi
    printf '  %s  %s\n' "${sum}" "$(basename "${f}")"
done
echo
echo 'Operator workflow (Apple Silicon = arm64, Intel = amd64):'
printf '  tar xzf minti-macos-arm64-v%s.tar.gz && cd minti-macos-arm64-v%s\n' "${VERSION}" "${VERSION}"
echo '  sudo bash install-minti.sh        # system install (recommended)'
echo '  bash install-minti.sh             # or per-user, no sudo'
