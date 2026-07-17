#!/usr/bin/env bash
# minti-cland uninstaller for macOS.
#
# Reverse of install-cland.sh. State directory (clan_key + identity.json)
# is preserved by default; pass --purge to remove it + the _minti
# service account.
#
# bash 3.2 compatible (macOS system bash).

set -euo pipefail

PURGE=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            cat <<'EOF'
Usage: uninstall-cland.sh [--purge]

Removes the launchd service + binary + plist + (system) log dir.

State directory (identity.json + clan.json + audit.jsonl) is preserved
by default so a future re-install reuses the same member_id.

  --purge    Also delete:
               - the state directory (member_id + clan_key destroyed)
               - the system rubric mirror
               - the _minti service account + group (system install only)
EOF
            exit 0
            ;;
        --purge) PURGE=1; shift ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }
warn() { printf '  \033[33mwarn:\033[0m %s\n' "$*"; }
err()  { printf '  \033[31mERROR:\033[0m %s\n' "$*" >&2; }

if [[ "$(uname -s)" != "Darwin" ]]; then
    err "this uninstaller is for macOS; current OS=$(uname -s)"
    exit 2
fi

SVC_LABEL="com.minti.cland"

if [[ $EUID -eq 0 ]]; then
    MODE=system
    INSTALL_BIN="/usr/local/bin/minti-cland"
    STATE_DIR="/Library/Application Support/MINTI/cland"
    SYS_RUBRIC="/Library/Application Support/MINTI/reasoning-scores.yaml"
    LOG_DIR="/usr/local/var/log/minti"
    PLIST_DEST="/Library/LaunchDaemons/com.minti.cland.plist"
    BOOTSTRAP_DOMAIN="system"
else
    MODE=user
    INSTALL_BIN="$HOME/.local/bin/minti-cland"
    STATE_DIR="$HOME/Library/Application Support/MINTI/cland"
    SYS_RUBRIC=""
    LOG_DIR="$HOME/Library/Logs/minti-cland"
    PLIST_DEST="$HOME/Library/LaunchAgents/com.minti.cland.plist"
    USER_UID="$(id -u)"
    BOOTSTRAP_DOMAIN="gui/$USER_UID"
fi

info "MINTI cland macOS uninstaller (mode=$MODE, purge=$PURGE)"

# ---------- 1. bootout the launchd job ----------

if launchctl print "$BOOTSTRAP_DOMAIN/$SVC_LABEL" >/dev/null 2>&1; then
    info "Booting out $SVC_LABEL from $BOOTSTRAP_DOMAIN"
    launchctl bootout "$BOOTSTRAP_DOMAIN/$SVC_LABEL" 2>/dev/null || \
        launchctl unload -w "$PLIST_DEST" 2>/dev/null || true
    ok "service stopped"
else
    warn "service not loaded; skipping bootout"
fi

# ---------- 2. plist + binary ----------

if [[ -f "$PLIST_DEST" ]]; then
    rm -f "$PLIST_DEST"
    ok "$PLIST_DEST removed"
fi

if [[ -f "$INSTALL_BIN" ]]; then
    rm -f "$INSTALL_BIN"
    ok "$INSTALL_BIN removed"
fi

# ---------- 3. log dir (always remove; not user data) ----------

if [[ -d "$LOG_DIR" ]]; then
    rm -rf "$LOG_DIR"
    ok "$LOG_DIR removed"
fi

# ---------- 4. state dir -- preserve unless --purge ----------

if [[ $PURGE -eq 1 ]]; then
    if [[ -d "$STATE_DIR" ]]; then
        rm -rf "$STATE_DIR"
        ok "$STATE_DIR removed (member_id + clan_key gone)"
    fi
    if [[ -n "$SYS_RUBRIC" && -f "$SYS_RUBRIC" ]]; then
        rm -f "$SYS_RUBRIC"
        ok "$SYS_RUBRIC removed"
    fi
    # System service account removal (root only).
    if [[ "$MODE" == "system" ]]; then
        if dscl . -read /Users/_minti >/dev/null 2>&1; then
            dscl . -delete /Users/_minti  || warn "dscl delete /Users/_minti failed"
            dscl . -delete /Groups/_minti || warn "dscl delete /Groups/_minti failed"
            ok "_minti user + group removed"
        fi
    fi
else
    if [[ -d "$STATE_DIR" ]]; then
        echo
        echo "State preserved at: $STATE_DIR"
        echo "  identity.json + clan.json kept. Re-installing reuses the same member_id."
        echo "  Pass --purge to remove (member_id + clan_key destroyed)."
    fi
fi

info "Uninstall complete"
