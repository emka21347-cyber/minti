#!/usr/bin/env bash
# MINTI uninstaller (Dist D2) — removes what install.sh placed on this host.
#
# STATE IS PRESERVED BY DEFAULT: /etc/minti + /var/lib/minti hold the
# member identity, clan_key, configs, and pack state — deleting them
# silently would drop this host out of any Clan it has joined. Pass
# --purge to remove them (and the `minti` system user) after the printed
# warning; a re-install then re-joins from scratch.
#
# Third-party software is NEVER touched: Ollama and opencode have their
# own uninstall paths (pointers printed at the end). Addon packs are
# dpkg-managed (`apt remove 'minti-pack-*'`).

set -euo pipefail

if [[ "${MINTI_CHROOT:-0}" == "1" ]]; then
    echo "ERROR: refusing to run the uninstaller inside an image-build chroot." >&2
    exit 1
fi

if [[ "$(id -u)" -ne 0 ]]; then
    echo "ERROR: must run as root. Re-run with: sudo bash $0" >&2
    exit 1
fi

PURGE=0
for arg in "$@"; do
    case "${arg}" in
        --purge) PURGE=1 ;;
        -h|--help)
            cat <<'EOF'
Usage: sudo bash uninstall.sh [--purge]

Removes MINTI services + binaries. State (/etc/minti, /var/lib/minti,
the minti user) is preserved by default so a re-install reuses the same
member identity. --purge removes state too (identity + clan_key
destroyed; the host must re-join its Clan from scratch).
EOF
            exit 0
            ;;
        *) echo "unknown option: ${arg}" >&2; exit 2 ;;
    esac
done

if [[ -t 2 ]] && command -v tput >/dev/null 2>&1; then
    cyan=$(tput setaf 6); green=$(tput setaf 2); yellow=$(tput setaf 3)
    red=$(tput setaf 1); reset=$(tput sgr0)
else
    cyan=""; green=""; yellow=""; red=""; reset=""
fi
info() { printf "%s[MINTI]%s %s\n" "${cyan}" "${reset}" "$*" >&2; }
ok()   { printf "%s[MINTI]%s %s\n" "${green}" "${reset}" "$*" >&2; }
warn() { printf "%s[MINTI]%s %s\n" "${yellow}" "${reset}" "WARN: $*" >&2; }

info "MINTI uninstaller (purge=${PURGE})"

# ---------- 1. services (consumers first: workspace -> cland -> runtime) ----------
for unit in minti-workspace minti-cland minti-runtime; do
    if [[ -f "/etc/systemd/system/${unit}.service" ]]; then
        info "Removing service ${unit}..."
        systemctl stop "${unit}.service" 2>/dev/null || true
        systemctl disable "${unit}.service" 2>/dev/null || true
        rm -f "/etc/systemd/system/${unit}.service"
        ok "${unit} stopped + unit removed"
    else
        warn "${unit}.service not installed; skipping"
    fi
done
systemctl daemon-reload 2>/dev/null || true

# ---------- 2. binaries ----------
for bin in minti-workspace minti-cland minti-runtime minti-pack-fetch mcptest minti-fetch; do
    if [[ -e "/usr/local/bin/${bin}" ]]; then
        rm -f "/usr/local/bin/${bin}"
        ok "removed /usr/local/bin/${bin}"
    fi
done
# The opencode symlink is ours (install.sh created it); the per-user
# ~/.opencode installation it points at is not.
if [[ -L /usr/local/bin/opencode ]]; then
    rm -f /usr/local/bin/opencode
    ok "removed /usr/local/bin/opencode symlink (per-user ~/.opencode remains)"
fi
if [[ -d /opt/minti ]]; then
    rm -rf /opt/minti
    ok "removed /opt/minti (MCP server binaries)"
fi

# ---------- 3. state — preserved by default ----------
if [[ "${PURGE}" == "1" ]]; then
    warn "PURGE: removing /etc/minti + /var/lib/minti + the minti user."
    warn "This deletes the member identity + clan_key — the host leaves its"
    warn "Clan and a re-install must re-join from scratch."
    rm -rf /etc/minti /var/lib/minti
    ok "state removed"
    if id -u minti >/dev/null 2>&1; then
        userdel minti 2>/dev/null || warn "could not remove user 'minti' (processes still running?)"
        ok "system user 'minti' removed"
    fi
else
    if [[ -d /var/lib/minti || -d /etc/minti ]]; then
        printf "\n"
        info "State preserved at /etc/minti + /var/lib/minti (and the 'minti' user)."
        info "  identity.json + clan.json + configs kept — re-installing reuses the"
        info "  same member_id and Clan membership. Pass --purge to remove."
    fi
fi

printf "\n"
info "Not touched (third-party):"
info "  Ollama    — see https://github.com/ollama/ollama/blob/main/docs/linux.md#uninstall"
info "  opencode  — per-user install at ~/.opencode (delete that dir to remove)"
info "  addon packs — apt remove 'minti-pack-*'"
ok "Uninstall complete."
exit 0
