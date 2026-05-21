#!/usr/bin/env bash
# MINTI installer v0 (M0) — prepares a Debian-family host to run MINTI later.
# Idempotent: safe to re-run. Does NOT install cland or MCP servers (later milestones).

set -euo pipefail

minti_version="0.1.0-M0"

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
info "Ensuring base packages (curl, ca-certificates, gnupg)..."
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    curl ca-certificates gnupg lsb-release >/dev/null
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

# ---------- MINTI state directories ----------
info "Creating MINTI state directories..."
install -d -m 0755 /etc/minti
install -d -m 0755 /var/lib/minti
printf '%s\n' "${minti_version}" > /etc/minti/version
ok "Wrote /etc/minti/version=${minti_version}"

# ---------- Done ----------
printf "\n"
printf "%s%s═══════════════════════════════════════════════════════%s\n" "${bold}" "${green}" "${reset}"
printf "%s%s  MINTI M0 install complete (version %s)%s\n" "${bold}" "${green}" "${minti_version}" "${reset}"
printf "%s%s═══════════════════════════════════════════════════════%s\n" "${bold}" "${green}" "${reset}"
printf "  Host:    %s\n" "${PRETTY_NAME:-${ID} ${VERSION_ID}}"
printf "  Arch:    %s\n" "${arch}"
printf "  %s\n" "${gpu_summary}"
printf "  Ollama:  %s\n" "$(ollama --version 2>&1 | head -1 || echo 'present')"
printf "\n"
printf "%sNext steps:%s\n" "${bold}" "${reset}"
printf "  1. Pull a starter model (pick one matching your hardware):\n"
printf "       ollama pull llama3.2:3b      # ~2 GB, CPU-friendly\n"
printf "       ollama pull qwen2.5:7b       # ~5 GB, modest GPU\n"
printf "       ollama pull deepseek-r1:32b  # ~19 GB, reasoner\n"
printf "  2. The MINTI Clan daemon (cland) is not installed in M0.\n"
printf "     It lands in M1-M4 — see docs/clan-protocol.md for the spec.\n"
printf "  3. Re-run this script after pulling MINTI updates; it's idempotent.\n"
printf "\n"

exit 0
