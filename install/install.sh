#!/usr/bin/env bash
# MINTI installer v0 (M0) — prepares a Debian-family host to run MINTI later.
# Idempotent: safe to re-run. Does NOT install cland or MCP servers (later milestones).

set -euo pipefail

minti_version="0.1.0-M1"

# Resolve repo root: install.sh lives at <repo>/install/install.sh, so
# the staged artifacts (runtime-adapter binary, systemd unit, configs)
# are at <repo>/runtime-adapter/... when running from a source checkout.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

# ---------- Output helpers ----------
if [[ -t 2 ]] && command -v tput >/dev/null 2>&1; then
    cyan=$(tput setaf 6); green=$(tput setaf 2); yellow=$(tput setaf 3)
    red=$(tput setaf 1); bold=$(tput bold); reset=$(tput sgr0)
else
    cyan=""; green=""; yellow=""; red=""; bold=""; reset=""
fi

info() { printf "%s[MINTI]%s %s\n" "${cyan}" "${reset}" "$*" >&2; }
ok()   { printf "%s[MINTI]%s %s\n" "${green}" "${reset}" "$*" >&2; }
warn() { printf "%s[MINTI]%s %s\n" "${yellow}" "${reset}" "WARN: $*" >&2; }
err()  { printf "%s[MINTI]%s %s\n" "${red}" "${reset}" "ERROR: $*" >&2; }

trap 'err "Failed at line ${LINENO} (last command: ${BASH_COMMAND})"' ERR

# ---------- Preflight ----------
if [[ "$(id -u)" -ne 0 ]]; then
    err "Must run as root. Re-run with: sudo bash $0"
    exit 1
fi

if [[ ! -f /etc/os-release ]]; then
    err "/etc/os-release not found; cannot detect distro."
    exit 1
fi
# shellcheck disable=SC1091
source /etc/os-release

case "${ID:-unknown}" in
    debian|ubuntu|linuxmint|pop) ;;
    *)
        err "MINTI v1 supports Debian-family hosts only; got '${ID:-unknown}'."
        exit 1
        ;;
esac

# Version sanity (warn-only; don't block early adopters)
version_ok="true"
case "${ID}" in
    debian)    [[ "${VERSION_ID%%.*}" -ge 12 ]] || version_ok="false" ;;
    ubuntu)    [[ "$(printf '%s\n22.04\n' "${VERSION_ID}" | sort -V | head -1)" == "22.04" ]] || version_ok="false" ;;
    linuxmint) [[ "${VERSION_ID%%.*}" -ge 21 ]] || version_ok="false" ;;
    pop)       [[ "$(printf '%s\n22.04\n' "${VERSION_ID}" | sort -V | head -1)" == "22.04" ]] || version_ok="false" ;;
esac
if [[ "${version_ok}" != "true" ]]; then
    warn "Distro version ${ID} ${VERSION_ID} is below the recommended baseline; proceeding anyway."
fi

arch="$(uname -m)"
if [[ "${arch}" != "x86_64" ]]; then
    err "MINTI v1 is amd64 only; detected '${arch}'."
    exit 1
fi

info "Detected: ${PRETTY_NAME:-${ID} ${VERSION_ID}} (${arch})"

# Internet reachability (warn-only)
if command -v curl >/dev/null 2>&1; then
    if curl -sf --max-time 5 https://api.github.com/zen >/dev/null 2>&1; then
        ok "Network reachable."
    else
        warn "Network check failed; Ollama install may fail if it tries to download."
    fi
else
    warn "curl not installed; will install during base packages step."
fi

# ---------- Base packages ----------
# zstd is required by the current Ollama install script for extracting its
# release tarball; without it the Ollama installer aborts. lsb-release is
# kept for any later distro-version code that wants a normalized lookup.
info "Ensuring base packages (curl, ca-certificates, gnupg, zstd)..."
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    curl ca-certificates gnupg lsb-release zstd >/dev/null
ok "Base packages present."

# ---------- GPU detection (informational) ----------
gpu_summary="No NVIDIA GPU detected (CPU-only mode)"
if command -v nvidia-smi >/dev/null 2>&1; then
    gpu_line="$(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader 2>/dev/null | head -1 || true)"
    if [[ -n "${gpu_line}" ]]; then
        gpu_summary="GPU: ${gpu_line}"
        ok "${gpu_summary}"
    fi
else
    info "${gpu_summary}"
fi

# ---------- Ollama install (idempotent) ----------
if command -v ollama >/dev/null 2>&1; then
    ollama_version="$(ollama --version 2>&1 | head -1 || echo 'unknown')"
    ok "Ollama already installed (${ollama_version})."
else
    info "Installing Ollama via official install script..."
    # Official Ollama installer is the supported path; we don't shadow it.
    curl -fsSL https://ollama.com/install.sh | sh
    ok "Ollama installed: $(ollama --version 2>&1 | head -1 || echo 'version-unknown')."
fi

# ---------- MINTI state directories + system user ----------
info "Creating MINTI state directories..."
install -d -m 0755 /etc/minti
install -d -m 0755 /var/lib/minti
printf '%s\n' "${minti_version}" > /etc/minti/version
ok "Wrote /etc/minti/version=${minti_version}"

# Idempotent system user — owns /var/lib/minti, runs the daemon.
if ! id -u minti >/dev/null 2>&1; then
    info "Creating system user 'minti'..."
    useradd --system --no-create-home --shell /usr/sbin/nologin minti
    ok "Created system user 'minti'."
else
    ok "System user 'minti' already exists."
fi
chown -R minti:minti /var/lib/minti

# ---------- minti-runtime (optional in M1; ships when binary is built) ----------
# Look for the binary in both the native-build location and the cross-compile
# dist/ location. The first one found wins. Linux native build → runtime-adapter/minti-runtime.
# Windows cross-compile for Linux → runtime-adapter/dist/minti-runtime-linux-amd64.
runtime_bin=""
for candidate in \
    "${repo_root}/runtime-adapter/minti-runtime" \
    "${repo_root}/runtime-adapter/dist/minti-runtime-linux-amd64" \
    "${repo_root}/runtime-adapter/dist/minti-runtime"; do
    if [[ -x "${candidate}" ]]; then
        runtime_bin="${candidate}"
        break
    fi
done
runtime_unit="${repo_root}/runtime-adapter/systemd/minti-runtime.service"
runtime_cfg_example="${repo_root}/runtime-adapter/configs/runtime.yaml.example"

runtime_status="skipped (binary not built)"
if [[ -n "${runtime_bin}" ]]; then
    info "Installing minti-runtime (source: ${runtime_bin#${repo_root}/})..."
    install -m 0755 "${runtime_bin}" /usr/local/bin/minti-runtime
    install -m 0644 "${runtime_unit}" /etc/systemd/system/minti-runtime.service
    # Don't overwrite an existing runtime.yaml that the user has edited.
    if [[ ! -f /etc/minti/runtime.yaml ]]; then
        install -m 0644 "${runtime_cfg_example}" /etc/minti/runtime.yaml
        ok "Wrote default /etc/minti/runtime.yaml from example."
    else
        ok "Preserving existing /etc/minti/runtime.yaml."
    fi
    systemctl daemon-reload
    systemctl enable --now minti-runtime.service
    runtime_status="installed and started"
    ok "minti-runtime running on 127.0.0.1:7780"
else
    warn "minti-runtime binary not found at ${runtime_bin}; skipping service install."
    warn "Build it with: cd runtime-adapter && go build -o minti-runtime ./cmd/minti-runtime"
fi

# ---------- Done ----------
printf "\n"
printf "%s%s═══════════════════════════════════════════════════════%s\n" "${bold}" "${green}" "${reset}"
printf "%s%s  MINTI M0 install complete (version %s)%s\n" "${bold}" "${green}" "${minti_version}" "${reset}"
printf "%s%s═══════════════════════════════════════════════════════%s\n" "${bold}" "${green}" "${reset}"
printf "  Host:    %s\n" "${PRETTY_NAME:-${ID} ${VERSION_ID}}"
printf "  Arch:    %s\n" "${arch}"
printf "  %s\n" "${gpu_summary}"
printf "  Ollama:  %s\n" "$(ollama --version 2>&1 | head -1 || echo 'present')"
printf "  Runtime: %s\n" "${runtime_status}"
printf "\n"
printf "%sNext steps:%s\n" "${bold}" "${reset}"
printf "  1. Pull a starter model (pick one matching your hardware):\n"
printf "       ollama pull llama3.2:3b      # ~2 GB, CPU-friendly\n"
printf "       ollama pull qwen2.5:7b       # ~5 GB, modest GPU\n"
printf "       ollama pull deepseek-r1:32b  # ~19 GB, reasoner\n"
if [[ "${runtime_status}" == "installed and started" ]]; then
    printf "  2. Test the runtime:\n"
    printf "       curl -s http://127.0.0.1:7780/minti/health\n"
    printf "       curl -s http://127.0.0.1:7780/v1/models | head\n"
else
    printf "  2. Build minti-runtime:\n"
    printf "       cd %s/runtime-adapter && go build -o minti-runtime ./cmd/minti-runtime\n" "${repo_root}"
    printf "       then re-run this installer.\n"
fi
printf "  3. The MINTI Clan daemon (cland) lands in M4; see docs/clan-protocol.md.\n"
printf "  4. This script is idempotent — safe to re-run after MINTI updates.\n"
printf "\n"

exit 0
