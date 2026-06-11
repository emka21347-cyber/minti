#!/usr/bin/env bash
# Build the full-stack MINTI Linux distribution tarball (door B, Dist D2).
# Produces dist/minti-linux-amd64-v<VERSION>.tar.gz.
#
# Runs anywhere bash + go + tar exist (Linux, Git Bash on Windows). All
# binaries are pure-Go cross-compiles (GOOS=linux GOARCH=amd64).
#
# Tarball layout — install/install.sh resolves every artifact from these
# paths first (tarball mode), falling back to source-checkout paths:
#   install/{install.sh,uninstall.sh,opencode.config.example.json}
#   bin/{minti-cland,minti-runtime,minti-workspace,minti-pack-fetch,
#        mcptest,minti-mcp-{fs,shell,recon,http,pkg}}
#   configs/{runtime.yaml.example,cland.yaml.example,
#            reasoning-scores.yaml.example,policy.yaml.example}
#   systemd/{minti-runtime,minti-cland,minti-workspace}.service
#   branding/{minti-fetch,logo.txt}
#   README.md

set -euo pipefail

VERSION="${VERSION:-0.4.0-D1}"
GO="${GO:-go}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
dist_dir="${repo_root}/dist"
stage="${dist_dir}/minti-linux-amd64-v${VERSION}"

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }
err()  { printf '  \033[31mERROR:\033[0m %s\n' "$*" >&2; }

# ---------- 1. cross-compile (GOOS=linux amd64, release flags) ----------

build() { # build <module-dir> <package> <out-name>
    local dir="$1" pkg="$2" out="$3"
    info "build ${out} (linux-amd64)"
    ( cd "${repo_root}/${dir}" && \
      GOOS=linux GOARCH=amd64 "${GO}" build -trimpath \
        -ldflags "-X main.version=${VERSION} -s -w" \
        -o "${stage}/bin/${out}" "${pkg}" )
    ok "bin/${out}"
}

rm -rf "${stage}"
mkdir -p "${stage}/bin" "${stage}/install" "${stage}/configs" "${stage}/systemd" "${stage}/branding"

build cland           ./cmd/minti-cland      minti-cland
build runtime-adapter ./cmd/minti-runtime    minti-runtime
build workspace       ./cmd/minti-workspace  minti-workspace
build pack-manager    ./cmd/minti-pack-fetch minti-pack-fetch
for s in fs shell recon http pkg; do
    build mcp-servers "./cmd/mcp-${s}" "minti-mcp-${s}"
done
build mcp-servers ./cmd/mcptest mcptest

# NOTE on modes: chmod is unreliable on MSYS/NTFS (Git Bash fakes POSIX
# modes from a content heuristic — ELF binaries read back 0644 no matter
# what), so executable bits are normalized INSIDE the archive by the
# python tarfile step below, never trusted from the staging tree.

# ---------- 2. scripts, configs, units, branding ----------

info "staging scripts + configs + units"
cp "${repo_root}/install/install.sh"   "${stage}/install/install.sh"
cp "${repo_root}/install/uninstall.sh" "${stage}/install/uninstall.sh"
cp "${repo_root}/install/opencode.config.example.json" "${stage}/install/opencode.config.example.json"

cp "${repo_root}/runtime-adapter/configs/runtime.yaml.example"      "${stage}/configs/runtime.yaml.example"
cp "${repo_root}/cland/configs/cland.yaml.example"                  "${stage}/configs/cland.yaml.example"
cp "${repo_root}/cland/configs/reasoning-scores.yaml.example"       "${stage}/configs/reasoning-scores.yaml.example"
cp "${repo_root}/mcp-servers/configs/policy.yaml.example"           "${stage}/configs/policy.yaml.example"

cp "${repo_root}/runtime-adapter/systemd/minti-runtime.service"     "${stage}/systemd/minti-runtime.service"
cp "${repo_root}/cland/systemd/minti-cland.service"                 "${stage}/systemd/minti-cland.service"
cp "${repo_root}/workspace/systemd/minti-workspace.service"         "${stage}/systemd/minti-workspace.service"

cp "${repo_root}/branding/minti-fetch" "${stage}/branding/minti-fetch"
[[ -f "${repo_root}/branding/logo.txt" ]] && cp "${repo_root}/branding/logo.txt" "${stage}/branding/logo.txt"

cat > "${stage}/README.md" <<'EOF'
# MINTI for Linux — full stack ("Add MINTI to this machine")

Debian-family (Debian 12+, Ubuntu 22.04+, Mint 21+, Pop!_OS 22.04+), amd64.

Install (from this directory):

    sudo bash install/install.sh

What it does: installs Ollama (official script; set MINTI_NO_OLLAMA=1 to
skip), the MINTI runtime (loopback :7780), 5 MCP tool servers, the cland
Clan daemon (LAN :7777 — the only listening LAN port), the Clan Workspace
web UI (loopback :8088), and the opencode terminal agent. Services run as
the unprivileged `minti` system user under hardened systemd units. When
it finishes it opens http://127.0.0.1:8088 in your browser (or prints the
URL on headless boxes — tunnel with `ssh -L 8088:127.0.0.1:8088 ...`).

Uninstall (state preserved by default; --purge wipes identity + clan_key):

    sudo bash install/uninstall.sh [--purge]

The installer is idempotent — re-run it to upgrade or repair. Verify the
tarball's SHA-256 against the one published on the download page before
installing.
EOF
ok "README.md"

# ---------- 3. tar ----------

tarball="${dist_dir}/minti-linux-amd64-v${VERSION}.tar.gz"
info "creating ${tarball}"
rm -f "${tarball}"
# Python tarfile with an explicit mode filter — the ONLY deterministic
# way to get correct modes when building on Git Bash/NTFS (chmod is a
# no-op there and install.sh's candidate loops test -x on the target).
# root:root ownership; dirs 0755; bin/*, *.sh, branding/* -> 0755;
# everything else 0644.
PY="$(command -v python3 || command -v python)" || { err "python3/python required to build the tarball"; exit 1; }
"${PY}" - "${stage}" "${tarball}" <<'EOF'
import os, sys, tarfile
src, out = sys.argv[1], sys.argv[2]
def fix(ti):
    ti.uid = ti.gid = 0
    ti.uname = ti.gname = "root"
    if ti.isdir():
        ti.mode = 0o755
    elif "/bin/" in ti.name or ti.name.endswith(".sh") or "/branding/" in ti.name:
        ti.mode = 0o755
    else:
        ti.mode = 0o644
    return ti
with tarfile.open(out, "w:gz") as t:
    t.add(src, arcname=os.path.basename(src), filter=fix)
EOF
ok "${tarball}"

if command -v sha256sum >/dev/null 2>&1; then
    sum="$(sha256sum "${tarball}" | awk '{print $1}')"
else
    sum="$(shasum -a 256 "${tarball}" | awk '{print $1}')"
fi
printf '\nchecksums.txt line (site D3):\n  %s  minti-linux-amd64-v%s.tar.gz\n' "${sum}" "${VERSION}"
printf '\nOperator workflow:\n'
printf '  tar xzf minti-linux-amd64-v%s.tar.gz\n' "${VERSION}"
printf '  cd minti-linux-amd64-v%s\n' "${VERSION}"
printf '  sudo bash install/install.sh\n'
