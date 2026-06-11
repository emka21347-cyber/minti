#!/usr/bin/env bash
# MINTI full-stack uninstaller for macOS — door B, Dist D2.
#
# sudo  => removes the system install (LaunchDaemons + /usr/local binaries)
# plain => removes the per-user install (LaunchAgents + ~/.local binaries)
#
# STATE PRESERVED BY DEFAULT: /Library/Application Support/MINTI (system)
# or ~/Library/Application Support/MINTI (per-user) holds the member
# identity + clan_key. --purge removes it (and, system mode, the _minti
# account) after a printed warning. All three plists are explicitly
# booted out AND deleted (D0 review F9 — an orphan plist respawn-fails at
# every boot). Ollama is never touched.

set -euo pipefail

PURGE=0
for arg in "$@"; do
    case "$arg" in
        --purge) PURGE=1 ;;
        -h|--help)
            echo "Usage: uninstall-minti.sh [--purge]   (sudo => system mode)"
            exit 0 ;;
        *) echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }
warn() { printf '  \033[33mwarn:\033[0m %s\n' "$*"; }

LABELS="com.minti.workspace com.minti.runtime com.minti.cland"

if [[ $EUID -eq 0 ]]; then
    MODE=system
    DOMAIN="system"
    PLIST_DIR="/Library/LaunchDaemons"
    BIN_DIR="/usr/local/bin"
    APP_SUPPORT="/Library/Application Support/MINTI"
    LOG_DIR="/usr/local/var/log/minti"
else
    MODE=per-user
    DOMAIN="gui/$(id -u)"
    PLIST_DIR="$HOME/Library/LaunchAgents"
    BIN_DIR="$HOME/.local/bin"
    APP_SUPPORT="$HOME/Library/Application Support/MINTI"
    LOG_DIR="$HOME/Library/Logs/minti"
fi

info "MINTI macOS uninstaller (mode=$MODE, purge=$PURGE)"

# ---------- 1. services: bootout + DELETE every plist (F9) ----------
for label in $LABELS; do
    if launchctl print "$DOMAIN/$label" >/dev/null 2>&1; then
        launchctl bootout "$DOMAIN/$label" 2>/dev/null || true
        ok "$label booted out"
    fi
    if [[ -f "$PLIST_DIR/$label.plist" ]]; then
        rm -f "$PLIST_DIR/$label.plist"
        ok "$PLIST_DIR/$label.plist removed"
    else
        warn "$label.plist not present; skipping"
    fi
done

# ---------- 2. binaries ----------
for bin in minti-workspace minti-runtime minti-cland; do
    if [[ -e "$BIN_DIR/$bin" ]]; then
        rm -f "$BIN_DIR/$bin"
        ok "$BIN_DIR/$bin removed"
    fi
done

# ---------- 3. state — preserved by default ----------
if [[ "$PURGE" -eq 1 ]]; then
    warn "PURGE: removing $APP_SUPPORT + $LOG_DIR — the member identity +"
    warn "clan_key are destroyed; a re-install must re-join its Clan."
    rm -rf "$APP_SUPPORT" "$LOG_DIR"
    ok "state + logs removed"
    if [[ "$MODE" == "system" ]] && dscl . -read /Users/_minti >/dev/null 2>&1; then
        dscl . -delete /Users/_minti
        dscl . -delete /Groups/_minti 2>/dev/null || true
        ok "_minti account removed"
    fi
else
    if [[ -d "$APP_SUPPORT" ]]; then
        echo
        info "State preserved at: $APP_SUPPORT"
        info "  identity.json + clan.json + configs kept — re-installing reuses"
        info "  the same member_id. Pass --purge to remove."
        if [[ "$MODE" == "system" ]]; then
            info "  (the _minti account is also kept; --purge removes it)"
        fi
    fi
fi

echo
info "Not touched (third-party): Ollama — drag Ollama.app to Trash to remove."
ok "Uninstall complete."
exit 0
