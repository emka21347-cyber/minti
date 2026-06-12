#!/usr/bin/env bash
# MINTI installer — prepares a Debian-family host to run MINTI.
# Idempotent: safe to re-run. Installs runtime + 5 MCP servers + cland +
# workspace (Dist D2). Does NOT install addon packs (user picks those).
#
# Works from two layouts:
#   - a source checkout  (<repo>/install/install.sh, binaries in module dirs)
#   - the door-B tarball (<root>/install/install.sh, binaries in <root>/bin,
#     configs in <root>/configs, units in <root>/systemd)
# Every artifact lookup tries the tarball path first, then the checkout
# paths — same candidate-loop idiom throughout.
#
# Env knobs:
#   MINTI_CHROOT=1     live-build chroot mode: skip systemctl + auto-open;
#                      an Ollama install failure stays FATAL (an ISO built
#                      without Ollama is worse than a failed build).
#   MINTI_NO_OLLAMA=1  skip the Ollama install entirely (D0 review F6).

set -euo pipefail

minti_version="0.4.0-D1"

# Resolve repo root: install.sh lives at <repo>/install/install.sh, so
# the staged artifacts (runtime-adapter binary, systemd unit, configs)
# are at <repo>/runtime-adapter/... when running from a source checkout.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

# first_existing PATH... — print the first path that exists, fail if none.
# Used to resolve every artifact across the two supported layouts
# (tarball candidates first, then source-checkout candidates).
first_existing() {
    local p
    for p in "$@"; do
        if [[ -e "${p}" ]]; then printf '%s' "${p}"; return 0; fi
    done
    return 1
}

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

# Set MINTI_CHROOT=1 when running inside a live-build chroot to skip
# service management (systemd is not running; unit files are still installed).
MINTI_CHROOT="${MINTI_CHROOT:-0}"
_svc() {
    if [[ "$MINTI_CHROOT" == "1" ]]; then
        info "chroot mode: skip systemctl $*"
        return 0
    fi
    systemctl "$@"
}

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
# D0 review F6: a flaky ollama.com download must not abort the whole
# door-B install (the runtime tolerates a missing backend by design and
# picks it up when it appears). In the ISO chroot the failure STAYS
# fatal — a silently Ollama-less image is worse than a failed build.
ollama_status="not installed"
if [[ "${MINTI_NO_OLLAMA:-0}" == "1" ]]; then
    warn "MINTI_NO_OLLAMA=1 — skipping Ollama install (get it later: https://ollama.com/download/linux)"
    ollama_status="skipped (MINTI_NO_OLLAMA=1)"
elif command -v ollama >/dev/null 2>&1; then
    ollama_version="$(ollama --version 2>&1 | head -1 || echo 'unknown')"
    ok "Ollama already installed (${ollama_version})."
    ollama_status="already installed (${ollama_version})"
else
    info "Installing Ollama via official install script..."
    # Official Ollama installer is the supported path; we don't shadow it.
    if curl -fsSL https://ollama.com/install.sh | sh; then
        ollama_status="installed ($(ollama --version 2>&1 | head -1 || echo 'version-unknown'))"
        ok "Ollama ${ollama_status}."
    else
        if [[ "$MINTI_CHROOT" == "1" ]]; then
            err "Ollama install failed inside the image build chroot — aborting (the ISO must not ship without it)."
            exit 1
        fi
        warn "Ollama install FAILED. MINTI still works — the runtime reports the"
        warn "missing backend honestly and picks Ollama up when it appears."
        warn "Install it later from: https://ollama.com/download/linux"
        ollama_status="FAILED — install manually"
    fi
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

# ---------- branding: minti-fetch (logo + system/Clan info) ----------
fetch_bin_src="${repo_root}/branding/minti-fetch"
if [[ -f "${fetch_bin_src}" ]]; then
    install -m 0755 "${fetch_bin_src}" /usr/local/bin/minti-fetch
    install -d -m 0755 /etc/minti/branding
    [[ -f "${repo_root}/branding/logo.txt" ]] && install -m 0644 "${repo_root}/branding/logo.txt" /etc/minti/branding/logo.txt
    ok "Installed minti-fetch (run anytime to see Clan/system status)."
fi

# ---------- minti-runtime (optional in M1; ships when binary is built) ----------
# Look for the binary in both the native-build location and the cross-compile
# dist/ location. The first one found wins. Linux native build → runtime-adapter/minti-runtime.
# Windows cross-compile for Linux → runtime-adapter/dist/minti-runtime-linux-amd64.
runtime_bin=""
for candidate in \
    "${repo_root}/bin/minti-runtime" \
    "${repo_root}/runtime-adapter/minti-runtime" \
    "${repo_root}/runtime-adapter/dist/minti-runtime-linux-amd64" \
    "${repo_root}/runtime-adapter/dist/minti-runtime"; do
    if [[ -x "${candidate}" ]]; then
        runtime_bin="${candidate}"
        break
    fi
done
runtime_unit="$(first_existing \
    "${repo_root}/systemd/minti-runtime.service" \
    "${repo_root}/runtime-adapter/systemd/minti-runtime.service")" || runtime_unit=""
runtime_cfg_example="$(first_existing \
    "${repo_root}/configs/runtime.yaml.example" \
    "${repo_root}/runtime-adapter/configs/runtime.yaml.example")" || runtime_cfg_example=""

runtime_status="skipped (binary not built)"
if [[ -n "${runtime_bin}" ]]; then
    info "Installing minti-runtime (source: ${runtime_bin#${repo_root}/})..."

    install -m 0755 "${runtime_bin}" /usr/local/bin/minti-runtime
    runtime_new_hash="$(sha256sum /usr/local/bin/minti-runtime | awk '{print $1}')"

    install -m 0644 "${runtime_unit}" /etc/systemd/system/minti-runtime.service
    if [[ ! -f /etc/minti/runtime.yaml ]]; then
        install -m 0644 "${runtime_cfg_example}" /etc/minti/runtime.yaml
        ok "Wrote default /etc/minti/runtime.yaml from example."
    else
        ok "Preserving existing /etc/minti/runtime.yaml."
    fi
    _svc daemon-reload

    # Decide whether to restart by comparing the new on-disk binary to what
    # the running process is actually executing. This catches the case where
    # an earlier install.sh run replaced the binary on disk but never
    # restarted the service — `systemctl is-active && enable --now` is a
    # no-op in that state, leaving stale code in memory.
    should_restart=false
    if systemctl is-active --quiet minti-runtime.service; then
        runtime_pid="$(systemctl show -p MainPID --value minti-runtime.service)"
        if [[ -n "${runtime_pid}" && "${runtime_pid}" != "0" ]]; then
            running_hash="$(sha256sum "/proc/${runtime_pid}/exe" 2>/dev/null | awk '{print $1}')"
            if [[ -z "${running_hash}" || "${running_hash}" != "${runtime_new_hash}" ]]; then
                should_restart=true
            fi
        fi
    fi
    if [[ "${should_restart}" == "true" ]]; then
        info "minti-runtime running stale binary — restarting service..."
        _svc restart minti-runtime.service
        runtime_status="restarted with new binary"
    else
        _svc enable --now minti-runtime.service
        runtime_status="installed and started"
    fi
    ok "minti-runtime running on 127.0.0.1:7780"
else
    warn "minti-runtime binary not found at ${runtime_bin}; skipping service install."
    warn "Build it with: cd runtime-adapter && go build -o minti-runtime ./cmd/minti-runtime"
fi

# ---------- MCP servers (M2) ----------
# MCP servers are NOT daemons — they're spawned over stdio by agent clients
# (opencode in M3, mcptest for local validation now). Just stage the binaries
# under /opt/minti/mcp/ and let agents pick them up by path.
mcp_dir="/opt/minti/mcp"
install -d -m 0755 /opt/minti
install -d -m 0755 "${mcp_dir}"

mcp_status="skipped (binaries not built)"
mcp_count=0
mcp_total=5
declare -a mcp_servers=(mcp-fs mcp-shell mcp-recon mcp-pkg mcp-http)

for s in "${mcp_servers[@]}"; do
    bin=""
    for candidate in \
        "${repo_root}/bin/minti-${s}" \
        "${repo_root}/mcp-servers/dist/minti-${s}-linux-amd64" \
        "${repo_root}/mcp-servers/dist/minti-${s}"; do
        if [[ -x "${candidate}" ]]; then bin="${candidate}"; break; fi
    done
    if [[ -n "${bin}" ]]; then
        install -m 0755 "${bin}" "${mcp_dir}/minti-${s}"
        mcp_count=$((mcp_count + 1))
    else
        warn "MCP binary missing: minti-${s} (run 'make mcp-linux' to build)"
    fi
done

# mcptest stays useful through M3 — it's the only way to drive an MCP server
# from the shell without an agent client.
for candidate in \
    "${repo_root}/bin/mcptest" \
    "${repo_root}/mcp-servers/dist/mcptest-linux-amd64" \
    "${repo_root}/mcp-servers/dist/mcptest"; do
    if [[ -x "${candidate}" ]]; then
        install -m 0755 "${candidate}" /usr/local/bin/mcptest
        ok "Installed mcptest to /usr/local/bin/"
        break
    fi
done

if [[ "${mcp_count}" -gt 0 ]]; then
    ok "Installed ${mcp_count}/${mcp_total} MCP servers to ${mcp_dir}"
    mcp_status="${mcp_count}/${mcp_total} servers in ${mcp_dir}"
fi

# System default policy (preserved if already present — user may have edited).
policy_example="$(first_existing \
    "${repo_root}/configs/policy.yaml.example" \
    "${repo_root}/mcp-servers/configs/policy.yaml.example")" || policy_example=""
if [[ -f "${policy_example}" ]]; then
    if [[ ! -f /etc/minti/policy.yaml ]]; then
        install -m 0644 "${policy_example}" /etc/minti/policy.yaml
        ok "Wrote default /etc/minti/policy.yaml"
    else
        ok "Preserving existing /etc/minti/policy.yaml"
    fi
fi

# Per-user state directory — audit log + per-user policy override land here.
# Only when running under sudo do we know who the actual target user is.
real_user="${SUDO_USER:-}"
if [[ -n "${real_user}" && "${real_user}" != "root" ]]; then
    user_home="$(getent passwd "${real_user}" | cut -d: -f6)"
    if [[ -n "${user_home}" && -d "${user_home}" ]]; then
        user_minti="${user_home}/.minti"
        install -d -m 0755 -o "${real_user}" -g "${real_user}" "${user_minti}"
        ok "Prepared ${user_minti}/ (owner: ${real_user})"
    fi
fi

# ---------- minti-cland (M4 — Clan daemon) ----------
# Mirrors minti-runtime install: stage the binary, write the systemd unit,
# preserve any existing config, restart-on-stale-binary check (M3 B13 pattern).
# cland's state dir holds clan_key + cert priv, so mode 0700 is mandatory.
cland_bin=""
for candidate in \
    "${repo_root}/bin/minti-cland" \
    "${repo_root}/cland/minti-cland" \
    "${repo_root}/cland/dist/minti-cland-linux-amd64" \
    "${repo_root}/cland/dist/minti-cland"; do
    if [[ -x "${candidate}" ]]; then
        cland_bin="${candidate}"
        break
    fi
done
cland_unit="$(first_existing \
    "${repo_root}/systemd/minti-cland.service" \
    "${repo_root}/cland/systemd/minti-cland.service")" || cland_unit=""
cland_cfg_example="$(first_existing \
    "${repo_root}/configs/cland.yaml.example" \
    "${repo_root}/cland/configs/cland.yaml.example")" || cland_cfg_example=""
cland_rubric_example="$(first_existing \
    "${repo_root}/configs/reasoning-scores.yaml.example" \
    "${repo_root}/cland/configs/reasoning-scores.yaml.example")" || cland_rubric_example=""

cland_status="skipped (binary not built)"
if [[ -n "${cland_bin}" ]]; then
    info "Installing minti-cland (source: ${cland_bin#${repo_root}/})..."

    install -m 0755 "${cland_bin}" /usr/local/bin/minti-cland
    cland_new_hash="$(sha256sum /usr/local/bin/minti-cland | awk '{print $1}')"

    # State dir owns clan_key + cert priv — strict 0700, owner minti.
    install -d -m 0700 -o minti -g minti /var/lib/minti/cland

    install -m 0644 "${cland_unit}" /etc/systemd/system/minti-cland.service
    if [[ ! -f /etc/minti/cland.yaml ]]; then
        install -m 0644 "${cland_cfg_example}" /etc/minti/cland.yaml
        ok "Wrote default /etc/minti/cland.yaml from example."
    else
        ok "Preserving existing /etc/minti/cland.yaml."
    fi
    if [[ ! -f /etc/minti/reasoning-scores.yaml ]]; then
        install -m 0644 "${cland_rubric_example}" /etc/minti/reasoning-scores.yaml
        ok "Wrote default /etc/minti/reasoning-scores.yaml from example."
    else
        ok "Preserving existing /etc/minti/reasoning-scores.yaml."
    fi
    _svc daemon-reload

    # Same restart-on-stale-binary pattern as minti-runtime above.
    cland_should_restart=false
    if systemctl is-active --quiet minti-cland.service; then
        cland_pid="$(systemctl show -p MainPID --value minti-cland.service)"
        if [[ -n "${cland_pid}" && "${cland_pid}" != "0" ]]; then
            cland_running_hash="$(sha256sum "/proc/${cland_pid}/exe" 2>/dev/null | awk '{print $1}')"
            if [[ -z "${cland_running_hash}" || "${cland_running_hash}" != "${cland_new_hash}" ]]; then
                cland_should_restart=true
            fi
        fi
    fi
    if [[ "${cland_should_restart}" == "true" ]]; then
        info "minti-cland running stale binary — restarting service..."
        _svc restart minti-cland.service
        cland_status="restarted with new binary"
    else
        _svc enable --now minti-cland.service
        cland_status="installed and started"
    fi
    ok "minti-cland running on 0.0.0.0:7777 (Clan-facing HTTPS)"
else
    warn "minti-cland binary not found at any of:"
    warn "  ${repo_root}/cland/minti-cland (native build)"
    warn "  ${repo_root}/cland/dist/minti-cland-linux-amd64 (cross-compile)"
    warn "Build with: make cland (native) or make cland-linux (for this host)"
fi

# ---------- minti-workspace (Dist D2 — Clan Workspace web UI) ----------
# Same deploy pattern as cland: stage binary, install hardened unit,
# B13 restart-on-stale-binary. No config file (single -listen flag, baked
# into the unit). Loopback-only until PIN/bearer auth lands — the unit
# enforces it at the cgroup level (IPAddressAllow loopback + deny any).
workspace_bin=""
for candidate in \
    "${repo_root}/bin/minti-workspace" \
    "${repo_root}/workspace/minti-workspace" \
    "${repo_root}/workspace/dist/minti-workspace-linux-amd64" \
    "${repo_root}/workspace/dist/minti-workspace"; do
    if [[ -x "${candidate}" ]]; then
        workspace_bin="${candidate}"
        break
    fi
done
workspace_unit="$(first_existing \
    "${repo_root}/systemd/minti-workspace.service" \
    "${repo_root}/workspace/systemd/minti-workspace.service")" || workspace_unit=""

workspace_status="skipped (binary not built)"
if [[ -n "${workspace_bin}" && -n "${workspace_unit}" ]]; then
    info "Installing minti-workspace (source: ${workspace_bin#${repo_root}/})..."

    install -m 0755 "${workspace_bin}" /usr/local/bin/minti-workspace
    workspace_new_hash="$(sha256sum /usr/local/bin/minti-workspace | awk '{print $1}')"

    install -m 0644 "${workspace_unit}" /etc/systemd/system/minti-workspace.service
    _svc daemon-reload

    # Same restart-on-stale-binary pattern as minti-runtime/cland (B13).
    workspace_should_restart=false
    if systemctl is-active --quiet minti-workspace.service; then
        workspace_pid="$(systemctl show -p MainPID --value minti-workspace.service)"
        if [[ -n "${workspace_pid}" && "${workspace_pid}" != "0" ]]; then
            workspace_running_hash="$(sha256sum "/proc/${workspace_pid}/exe" 2>/dev/null | awk '{print $1}')"
            if [[ -z "${workspace_running_hash}" || "${workspace_running_hash}" != "${workspace_new_hash}" ]]; then
                workspace_should_restart=true
            fi
        fi
    fi
    if [[ "${workspace_should_restart}" == "true" ]]; then
        info "minti-workspace running stale binary — restarting service..."
        _svc restart minti-workspace.service
        workspace_status="restarted with new binary"
    else
        _svc enable --now minti-workspace.service
        workspace_status="installed and started"
    fi
    ok "minti-workspace running on 127.0.0.1:8088 (loopback only)"
else
    warn "minti-workspace binary or unit not found; skipping. Build with:"
    warn "  make workspace-linux  (binary)  — unit at workspace/systemd/"
fi

# ---------- minti-pack-fetch (M6-content — addon-pack content fetcher) ----------
# Not a daemon; just a helper binary called by addon-pack postinst scripts.
# The minti-pack-fetch.deb (built via `make pack-fetch-deb`) installs the
# same binary to the same path — this bare-binary deploy lets `apt install
# ./minti-pack-<addon>_*.deb` resolve the Depends without the user having
# to install pack-fetch.deb first.
pack_fetch_bin=""
for candidate in \
    "${repo_root}/bin/minti-pack-fetch" \
    "${repo_root}/pack-manager/minti-pack-fetch" \
    "${repo_root}/pack-manager/dist/minti-pack-fetch-linux-amd64" \
    "${repo_root}/pack-manager/dist/minti-pack-fetch"; do
    if [[ -x "${candidate}" ]]; then
        pack_fetch_bin="${candidate}"
        break
    fi
done

pack_fetch_status="skipped (binary not built)"
if [[ -n "${pack_fetch_bin}" ]]; then
    info "Installing minti-pack-fetch (source: ${pack_fetch_bin#${repo_root}/})..."
    install -m 0755 "${pack_fetch_bin}" /usr/local/bin/minti-pack-fetch
    install -d -m 0755 /var/lib/minti/packs
    pack_fetch_status="installed at /usr/local/bin/minti-pack-fetch"
    ok "minti-pack-fetch ready (addon packs can call it)."
else
    warn "minti-pack-fetch binary not found at any of:"
    warn "  ${repo_root}/pack-manager/minti-pack-fetch (native build)"
    warn "  ${repo_root}/pack-manager/dist/minti-pack-fetch-linux-amd64 (cross-compile)"
    warn "Build with: make pack-fetch (native) or make pack-fetch-linux (for this host)"
    warn "Without it, addon packs (minti-pack-hermes3, etc) will fail at postinst."
fi

# ---------- opencode (M3 — bundled agent client) ----------
# Per PRD §6.3 + P1, opencode (sst, MIT) is the bundled default terminal agent.
# We use upstream's official install script (a single shell-piped command) and
# symlink the resulting binary into /usr/local/bin so all users find it on PATH.
opencode_status="skipped"
if command -v opencode >/dev/null 2>&1; then
    opencode_status="already installed ($(opencode --version 2>&1 | head -1 || echo 'version-unknown'))"
    ok "opencode already installed."
else
    info "Installing opencode via the official install script..."
    # The official installer is non-interactive when piped. It writes to
    # ~/.opencode/bin (HOME of the executing user); run it as $SUDO_USER if
    # available so the binary lands in the human's home rather than /root.
    real_user="${SUDO_USER:-root}"
    if [[ "${real_user}" != "root" ]]; then
        real_home="$(getent passwd "${real_user}" | cut -d: -f6)"
        if su - "${real_user}" -c 'curl -fsSL https://opencode.ai/install | bash' >/dev/null 2>&1; then
            ok "opencode install script completed (as ${real_user})."
        else
            warn "opencode install script failed under ${real_user}; trying as root..."
            real_user="root"; real_home="/root"
            curl -fsSL https://opencode.ai/install | bash >/dev/null 2>&1 || true
        fi
    else
        real_home="/root"
        curl -fsSL https://opencode.ai/install | bash >/dev/null 2>&1 || true
    fi

    # Symlink wherever the binary actually landed so PATH always finds it.
    oc_bin=""
    for candidate in \
        "${real_home}/.opencode/bin/opencode" \
        "${real_home}/.local/bin/opencode" \
        "/root/.opencode/bin/opencode" \
        "/usr/local/bin/opencode"; do
        if [[ -x "${candidate}" ]]; then
            oc_bin="${candidate}"
            break
        fi
    done
    if [[ -n "${oc_bin}" ]]; then
        if [[ "${oc_bin}" != "/usr/local/bin/opencode" ]]; then
            ln -sf "${oc_bin}" /usr/local/bin/opencode
        fi
        opencode_status="installed at ${oc_bin} (symlinked to /usr/local/bin)"
        ok "${opencode_status}"
    else
        opencode_status="install failed — try manually: curl -fsSL https://opencode.ai/install | bash"
        warn "opencode install: binary not found after install. ${opencode_status}"
    fi
fi

# Install the MINTI opencode config template + drop into the invoking user's
# home if they don't already have one. Preserves user-modified configs.
oc_template_src="${repo_root}/install/opencode.config.example.json"
oc_template_dst="/etc/minti/opencode.config.example.json"
if [[ -f "${oc_template_src}" ]]; then
    install -m 0644 "${oc_template_src}" "${oc_template_dst}"
    ok "Wrote opencode config template to ${oc_template_dst}"

    if [[ -n "${SUDO_USER:-}" && "${SUDO_USER}" != "root" ]]; then
        user_home="$(getent passwd "${SUDO_USER}" | cut -d: -f6)"
        if [[ -n "${user_home}" && -d "${user_home}" ]]; then
            user_oc_dir="${user_home}/.config/opencode"
            user_oc="${user_oc_dir}/opencode.json"
            if [[ ! -f "${user_oc}" ]]; then
                install -d -m 0755 -o "${SUDO_USER}" -g "${SUDO_USER}" "${user_oc_dir}"
                install -m 0644 -o "${SUDO_USER}" -g "${SUDO_USER}" "${oc_template_dst}" "${user_oc}"
                ok "Installed default opencode config for ${SUDO_USER} at ${user_oc}"
            else
                ok "Preserving existing ${user_oc} for ${SUDO_USER}"
            fi
        fi
    fi
fi

# ---------- minti-pack-recon (M2; optional — only if built locally) ----------
# Build path: from a Debian host, `make pack-recon` produces a .deb in dist/.
# Install path: dpkg -i dist/minti-pack-recon_*.deb (not done by this script —
# we don't auto-install packs; the user picks them).
pack_status="skipped (build with: make pack-recon)"
shopt -s nullglob
pack_files=( "${repo_root}"/dist/minti-pack-recon_*.deb )
shopt -u nullglob
if [[ ${#pack_files[@]} -gt 0 ]]; then
    pack_status="built; install with: sudo dpkg -i ${pack_files[0]##*/}"
fi

# ---------- Done ----------
printf "\n"
printf "%s%s═══════════════════════════════════════════════════════%s\n" "${bold}" "${green}" "${reset}"
printf "%s%s  MINTI install complete (version %s)%s\n" "${bold}" "${green}" "${minti_version}" "${reset}"
printf "%s%s═══════════════════════════════════════════════════════%s\n" "${bold}" "${green}" "${reset}"
printf "  Host:    %s\n" "${PRETTY_NAME:-${ID} ${VERSION_ID}}"
printf "  Arch:    %s\n" "${arch}"
printf "  %s\n" "${gpu_summary}"
printf "  Ollama:  %s\n" "${ollama_status}"
printf "  Runtime: %s\n" "${runtime_status}"
printf "  MCP:     %s\n" "${mcp_status}"
printf "  Cland:   %s\n" "${cland_status}"
printf "  Workspace: %s\n" "${workspace_status}"
printf "  Pack-fetch: %s\n" "${pack_fetch_status}"
printf "  opencode: %s\n" "${opencode_status}"
printf "  Pack:    %s\n" "${pack_status}"
printf "\n"
printf "%sNext steps:%s\n" "${bold}" "${reset}"
printf "  1. Pull a starter model — install %sminti-pack-hermes3%s for the recommended default,\n" "${bold}" "${reset}"
printf "     or pull a raw Ollama tag yourself:\n"
printf "       ollama pull hermes3:8b       # ~5 GB, agent-tuned (Hermes 3, recommended)\n"
printf "       ollama pull mistral:7b       # ~4 GB, alternative\n"
printf "       ollama pull llama3.2:3b      # ~2 GB, CPU-friendly\n"
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
printf "  3. Smoke-test an MCP tool:\n"
printf "       mcptest --yes --arg path=\$HOME %s/minti-mcp-fs list_dir\n" "${mcp_dir}"
printf "  4. Launch the bundled agent (configure with /connect on first run):\n"
printf "       opencode\n"
printf "  5. Install optional %saddon packs%s (Debian/Ubuntu metapackages; auto-fetch\n" "${bold}" "${reset}"
printf "     content via minti-pack-fetch — set MINTI_PACK_NO_FETCH=1 to defer):\n"
printf "       sudo apt install ./dist/minti-pack-hermes3_*.deb       # ~5 GB, agent-tuned chat\n"
printf "       sudo apt install ./dist/minti-pack-mistral_*.deb       # ~4 GB, alt chat\n"
printf "       sudo apt install ./dist/minti-pack-wiki-simple_*.deb   # ~1.5 GB, offline Wikipedia\n"
printf "  6. Build & install the recon tool pack (Debian/Ubuntu only):\n"
printf "       make pack-recon && sudo dpkg -i dist/minti-pack-recon_*.deb\n"
if [[ "${cland_status}" == *"installed"* || "${cland_status}" == *"restarted"* ]]; then
    printf "  7. Join a Clan: open the workspace (step 10) and paste the connection\n"
    printf "     token a member shared with you — the easiest path.\n"
    printf "     Or from the CLI (run as the %sminti%s service user, flags BEFORE the\n" "${bold}" "${reset}"
    printf "     message/positional — plain 'sudo' writes root-owned state the daemon\n"
    printf "     can't read):\n"
    printf "       sudo -u minti minti-cland join --config /etc/minti/cland.yaml --connect MINTI1-...\n"
    printf "     To found your own Clan instead:\n"
    printf "       sudo -u minti minti-cland create --config /etc/minti/cland.yaml --address <this-host-LAN-ip>:7777\n"
else
    printf "  7. Build minti-cland: make cland-linux  (then re-run this installer)\n"
fi
printf "  8. This script is idempotent — safe to re-run after MINTI updates.\n"
printf "  9. Run %sminti-fetch%s anytime to see your system + Clan + addons status.\n" "${bold}" "${reset}"
if [[ "${workspace_status}" == *"installed"* || "${workspace_status}" == *"restarted"* ]]; then
    printf " 10. The %sClan Workspace%s (web UI) is at http://127.0.0.1:8088 — loopback only.\n" "${bold}" "${reset}"
    printf "     Headless box? Tunnel it:  ssh -L 8088:127.0.0.1:8088 <user>@<this-host>\n"
fi
printf "\n"

# ---------- auto-open the workspace (door-B polish; D0 review F3/F7) ----------
# Best-effort, never load-bearing: poll up to 30 s, open the browser as the
# invoking (non-root) user when a display is around, print the URL always.
# Skipped in the image-build chroot.
if [[ "$MINTI_CHROOT" != "1" ]] && \
   [[ "${workspace_status}" == *"installed"* || "${workspace_status}" == *"restarted"* ]]; then
    workspace_url="http://127.0.0.1:8088"
    workspace_up=false
    for _ in $(seq 1 30); do
        if curl -sf --max-time 2 "${workspace_url}/" >/dev/null 2>&1; then
            workspace_up=true
            break
        fi
        sleep 1
    done
    if [[ "${workspace_up}" == "true" ]]; then
        opened=false
        if [[ -n "${SUDO_USER:-}" && "${SUDO_USER}" != "root" && -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]] \
           && command -v xdg-open >/dev/null 2>&1; then
            # De-elevated open (D0 review F3): xdg-open as the human user,
            # with just the display env it needs.
            if sudo -u "${SUDO_USER}" --preserve-env=DISPLAY,WAYLAND_DISPLAY,XDG_RUNTIME_DIR \
                   xdg-open "${workspace_url}" >/dev/null 2>&1; then
                opened=true
                ok "Opened the Clan Workspace in your browser: ${workspace_url}"
            fi
        fi
        if [[ "${opened}" != "true" ]]; then
            ok "Clan Workspace is up: ${workspace_url}"
        fi
    else
        warn "Workspace service installed but not answering after 30 s."
        warn "Check: systemctl status minti-workspace ; journalctl -u minti-workspace -n 30"
    fi
fi

exit 0
