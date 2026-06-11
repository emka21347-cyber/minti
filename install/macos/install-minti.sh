#!/usr/bin/env bash
# MINTI full-stack installer for macOS (10.13+) — door B, Dist D2.
#
# Extends the peer-reviewed M5-C cland recipe (cland/darwin/) to the
# three-service stack:
#   com.minti.cland      LAN :7777 (TLS+HMAC) — the only listening LAN port
#   com.minti.runtime    127.0.0.1:7780 (loopback)
#   com.minti.workspace  127.0.0.1:8088 (loopback; web UI)
#
# System install (run with sudo): LaunchDaemons under the dedicated
# _minti service account (M5-C dscl recipe verbatim), state under
# /Library/Application Support/MINTI/.
#
# Per-user install (run without sudo): LaunchAgents, state under
# ~/Library/Application Support/MINTI/.
#
# D0-review folds in force: quarantine xattr stripped from ALL THREE
# binaries (F2); Ollama detect-or-guide, never auto-installed (F5);
# 30 s workspace health poll (F7); browser open as the console user,
# never root (F3). bash 3.2 compatible (macOS system bash).
#
# HONESTY: written + lint-verified without a live Mac (same posture as
# M5-C). The download page labels the macOS build's verification status.

set -euo pipefail

UNATTENDED=0
NO_AUTO_OPEN=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            cat <<'EOF'
Usage: install-minti.sh [--unattended] [--no-auto-open]

sudo  => system install (LaunchDaemons, _minti service account)
plain => per-user install (LaunchAgents under your account)

--unattended    skip interactive prompts (the Ollama download-page offer)
--no-auto-open  don't open the workspace URL in a browser at the end
EOF
            exit 0 ;;
        --unattended)   UNATTENDED=1; shift ;;
        --no-auto-open) NO_AUTO_OPEN=1; shift ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }
warn() { printf '  \033[33mwarn:\033[0m %s\n' "$*"; }
err()  { printf '  \033[31mERROR:\033[0m %s\n' "$*" >&2; }

if [[ "$(uname -s)" != "Darwin" ]]; then
    err "this installer is for macOS; current OS=$(uname -s)"
    exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Bundled artefacts (tarball layout).
BIN_CLAND="$SCRIPT_DIR/bin/minti-cland"
BIN_RUNTIME="$SCRIPT_DIR/bin/minti-runtime"
BIN_WORKSPACE="$SCRIPT_DIR/bin/minti-workspace"
PLIST_CLAND_SRC="$SCRIPT_DIR/com.minti.cland.plist"
PLIST_RUNTIME_SRC="$SCRIPT_DIR/com.minti.runtime.plist"
PLIST_WORKSPACE_SRC="$SCRIPT_DIR/com.minti.workspace.plist"
YAML_CLAND="$SCRIPT_DIR/configs/cland.yaml.darwin.example"
YAML_RUNTIME="$SCRIPT_DIR/configs/runtime.yaml.darwin.example"
YAML_RUBRIC="$SCRIPT_DIR/configs/reasoning-scores.yaml.example"

for f in "$BIN_CLAND" "$BIN_RUNTIME" "$BIN_WORKSPACE" \
         "$PLIST_CLAND_SRC" "$PLIST_RUNTIME_SRC" "$PLIST_WORKSPACE_SRC" \
         "$YAML_CLAND" "$YAML_RUNTIME"; do
    [[ -f "$f" ]] || { err "missing required bundle file: $f"; exit 3; }
done

# ---------- shared helpers ----------

# bootstrap_service <domain> <label> <plist-path>
# M5-C pattern: always bootout a loaded instance first (upgraded binaries
# must not stay mapped), bootstrap with legacy load -w fallback, soft
# confirmation only.
bootstrap_service() {
    local domain="$1" label="$2" plist="$3"
    if launchctl print "$domain/$label" >/dev/null 2>&1; then
        launchctl bootout "$domain/$label" 2>/dev/null || true
        ok "$label: previous instance booted out"
    fi
    if ! launchctl bootstrap "$domain" "$plist" 2>/dev/null; then
        warn "$label: launchctl bootstrap failed; falling back to legacy load -w"
        launchctl load -w "$plist"
    fi
    sleep 1
    if launchctl print "$domain/$label" >/dev/null 2>&1; then
        ok "$label loaded under launchd"
    else
        warn "$label not visible in launchctl print; check its err.log"
    fi
}

# detect_ollama — sets OLLAMA_STATUS; advisory only (D0 review F5: the
# liveness probe checks for a JSON "version" field, not just an open port).
detect_ollama() {
    OLLAMA_STATUS="not found"
    if command -v ollama >/dev/null 2>&1; then
        OLLAMA_STATUS="found on PATH ($(command -v ollama))"
    elif [[ -d /Applications/Ollama.app ]]; then
        OLLAMA_STATUS="found at /Applications/Ollama.app"
    fi
    if curl -sf --max-time 3 http://127.0.0.1:11434/api/version 2>/dev/null | grep -q '"version"'; then
        OLLAMA_STATUS="serving on 127.0.0.1:11434"
    fi
    if [[ "$OLLAMA_STATUS" == "not found" ]]; then
        warn "Ollama not detected. MINTI installs fine without it, but there are"
        warn "no local models until Ollama is present: https://ollama.com/download/mac"
        if [[ "$UNATTENDED" -eq 0 ]]; then
            printf '  Open the Ollama download page in your browser? [y/N] '
            read -r answer || answer=""
            case "$answer" in
                y|Y) open_as_console_user "https://ollama.com/download/mac" ;;
            esac
        fi
    else
        ok "Ollama: $OLLAMA_STATUS"
    fi
}

# open_as_console_user <url> — D0 review F3: never open a browser as root.
open_as_console_user() {
    local url="$1"
    if [[ $EUID -eq 0 && -n "${SUDO_USER:-}" && "$SUDO_USER" != "root" ]]; then
        sudo -u "$SUDO_USER" open "$url" 2>/dev/null || true
    else
        open "$url" 2>/dev/null || true
    fi
}

# poll_and_open_workspace — F7: up to 30 s; URL printed either way.
poll_and_open_workspace() {
    local url="http://127.0.0.1:8088"
    local up=0 i
    info "Waiting for the workspace on $url (up to 30 s)"
    for i in $(seq 1 30); do
        if curl -sf --max-time 2 "$url/" >/dev/null 2>&1; then up=1; break; fi
        sleep 1
    done
    if [[ "$up" -eq 1 ]]; then
        ok "workspace answering at $url"
        if [[ "$NO_AUTO_OPEN" -eq 0 ]]; then
            open_as_console_user "$url"
            ok "opened the workspace in your browser (as the console user)"
        fi
    else
        warn "workspace not answering after 30 s; check the workspace err.log,"
        warn "then browse to $url manually."
        return 1
    fi
}

# =====================================================================
#                          SYSTEM INSTALL
# =====================================================================
if [[ $EUID -eq 0 ]]; then

    INSTALL_DIR="/usr/local/bin"
    APP_SUPPORT="/Library/Application Support/MINTI"
    LOG_DIR="/usr/local/var/log/minti"
    DAEMON_DIR="/Library/LaunchDaemons"
    DOMAIN="system"

    info "MINTI full-stack macOS installer (mode=system)"

    # ---------- 1. _minti service account (M5-C recipe verbatim) ----------
    info "Ensuring _minti service account"
    if dscl . -read /Users/_minti >/dev/null 2>&1; then
        existing_uid="$(dscl . -read /Users/_minti UniqueID 2>/dev/null | awk '{print $2}')"
        ok "_minti exists (uid=$existing_uid); leaving as-is"
    else
        existing_uids="$(dscl . -list /Users UniqueID 2>/dev/null | awk '{print $2}' | sort -n | uniq)"
        chosen_uid=""
        for candidate in $(seq 300 499); do
            if ! printf '%s\n' "$existing_uids" | grep -qx "$candidate"; then
                chosen_uid="$candidate"
                break
            fi
        done
        if [[ -z "$chosen_uid" ]]; then
            err "no free UID found in 300..499 range; aborting"
            exit 4
        fi
        dscl . -create  /Groups/_minti
        dscl . -create  /Groups/_minti PrimaryGroupID "$chosen_uid"
        dscl . -create  /Groups/_minti RealName       "MINTI service"
        dscl . -create  /Users/_minti
        dscl . -create  /Users/_minti UniqueID         "$chosen_uid"
        dscl . -create  /Users/_minti PrimaryGroupID   "$chosen_uid"
        dscl . -create  /Users/_minti UserShell        /usr/bin/false
        dscl . -create  /Users/_minti RealName         "MINTI service"
        dscl . -create  /Users/_minti NFSHomeDirectory /var/empty
        dscl . -create  /Users/_minti IsHidden         1
        dscl . -create  /Users/_minti HiddenSystemUser 1
        ok "_minti created (uid=gid=$chosen_uid; hidden from login window)"
    fi

    # ---------- 2. binaries (quarantine stripped from ALL — D0 F2) ----------
    info "Installing binaries"
    for pair in "minti-cland:$BIN_CLAND" "minti-runtime:$BIN_RUNTIME" "minti-workspace:$BIN_WORKSPACE"; do
        name="${pair%%:*}"; src="${pair#*:}"
        install -m 0755 "$src" "$INSTALL_DIR/$name"
        xattr -d com.apple.quarantine "$INSTALL_DIR/$name" 2>/dev/null || true
        ok "$INSTALL_DIR/$name (quarantine xattr stripped)"
    done

    # ---------- 3. state dirs ----------
    info "Preparing state dirs"
    for sub in cland runtime workspace; do
        mkdir -p "$APP_SUPPORT/$sub"
        chown -R _minti:_minti "$APP_SUPPORT/$sub"
        chmod 0700 "$APP_SUPPORT/$sub"
        ok "$APP_SUPPORT/$sub (owner=_minti, mode=0700)"
    done
    mkdir -p "$LOG_DIR"
    chown _minti:_minti "$LOG_DIR"
    chmod 0750 "$LOG_DIR"
    ok "$LOG_DIR"

    # ---------- 4. configs (preserve on re-install) ----------
    info "Staging configs"
    if [[ ! -f "$APP_SUPPORT/cland/cland.yaml" ]]; then
        install -m 0640 -o _minti -g _minti "$YAML_CLAND" "$APP_SUPPORT/cland/cland.yaml"
        ok "cland.yaml installed"
    else
        ok "cland.yaml preserved"
    fi
    if [[ ! -f "$APP_SUPPORT/runtime/runtime.yaml" ]]; then
        install -m 0640 -o _minti -g _minti "$YAML_RUNTIME" "$APP_SUPPORT/runtime/runtime.yaml"
        ok "runtime.yaml installed"
    else
        ok "runtime.yaml preserved"
    fi
    if [[ -f "$YAML_RUBRIC" && ! -f "$APP_SUPPORT/cland/reasoning-scores.yaml" ]]; then
        install -m 0640 -o _minti -g _minti "$YAML_RUBRIC" "$APP_SUPPORT/cland/reasoning-scores.yaml"
        ok "reasoning-scores.yaml installed"
    fi
    if [[ -f "$APP_SUPPORT/cland/reasoning-scores.yaml" && ! -f "$APP_SUPPORT/reasoning-scores.yaml" ]]; then
        cp "$APP_SUPPORT/cland/reasoning-scores.yaml" "$APP_SUPPORT/reasoning-scores.yaml"
        chmod 0644 "$APP_SUPPORT/reasoning-scores.yaml"
        ok "system rubric mirror at $APP_SUPPORT/reasoning-scores.yaml"
    fi
    # LOAD-BEARING symlink for the workspace's shelled CLI: with HOME=/
    # (workspace plist) the CLI's non-root default config path resolves to
    # /Library/Application Support/MINTI/cland.yaml — point it at the real
    # config. _minti can traverse + read; same trust domain as the daemon.
    if [[ ! -e "$APP_SUPPORT/cland.yaml" ]]; then
        ln -s "$APP_SUPPORT/cland/cland.yaml" "$APP_SUPPORT/cland.yaml"
        ok "cland.yaml symlink for the workspace CLI ($APP_SUPPORT/cland.yaml)"
    fi

    # ---------- 5. plists + bootstrap (cland -> runtime -> workspace) ----------
    info "Installing LaunchDaemon plists"
    for pair in "com.minti.cland:$PLIST_CLAND_SRC" "com.minti.runtime:$PLIST_RUNTIME_SRC" "com.minti.workspace:$PLIST_WORKSPACE_SRC"; do
        label="${pair%%:*}"; src="${pair#*:}"
        install -m 0644 -o root -g wheel "$src" "$DAEMON_DIR/$label.plist"
        if ! plutil -lint "$DAEMON_DIR/$label.plist" >/dev/null; then
            err "$label.plist failed plutil -lint after install"
            exit 5
        fi
        ok "$DAEMON_DIR/$label.plist"
    done

    detect_ollama

    info "Loading services into launchd"
    bootstrap_service "$DOMAIN" com.minti.cland     "$DAEMON_DIR/com.minti.cland.plist"
    bootstrap_service "$DOMAIN" com.minti.runtime   "$DAEMON_DIR/com.minti.runtime.plist"
    bootstrap_service "$DOMAIN" com.minti.workspace "$DAEMON_DIR/com.minti.workspace.plist"

    # ---------- 6. firewall hint (cland only — the loopback two are silent) ----------
    info "macOS Application Firewall"
    warn "On first inbound LAN connection, macOS may prompt: 'Allow incoming"
    warn "network connections for minti-cland?' -- click Allow. The runtime"
    warn "and workspace are loopback-only and trigger no prompt."

    poll_and_open_workspace || true

    info "Install complete"
    echo
    echo "Services:  com.minti.{cland,runtime,workspace}  (sudo launchctl print system/<label>)"
    echo "Binaries:  $INSTALL_DIR/minti-{cland,runtime,workspace}"
    echo "State:     $APP_SUPPORT/{cland,runtime,workspace}"
    echo "Logs:      $LOG_DIR/{cland,runtime,workspace}.{out,err}.log"
    echo "Workspace: http://127.0.0.1:8088"
    echo "Ollama:    $OLLAMA_STATUS"
    echo
    echo "Uninstall (state preserved unless --purge):"
    echo "  sudo bash $SCRIPT_DIR/uninstall-minti.sh"
    exit 0
fi

# =====================================================================
#                          PER-USER INSTALL
# =====================================================================

INSTALL_DIR="$HOME/.local/bin"
APP_SUPPORT="$HOME/Library/Application Support/MINTI"
LOG_DIR="$HOME/Library/Logs/minti"
AGENT_DIR="$HOME/Library/LaunchAgents"
DOMAIN="gui/$(id -u)"

info "MINTI full-stack macOS installer (mode=per-user)"

mkdir -p "$INSTALL_DIR" "$APP_SUPPORT/cland" "$APP_SUPPORT/runtime" "$APP_SUPPORT/workspace" "$LOG_DIR" "$AGENT_DIR"

info "Installing binaries"
for pair in "minti-cland:$BIN_CLAND" "minti-runtime:$BIN_RUNTIME" "minti-workspace:$BIN_WORKSPACE"; do
    name="${pair%%:*}"; src="${pair#*:}"
    install -m 0755 "$src" "$INSTALL_DIR/$name"
    xattr -d com.apple.quarantine "$INSTALL_DIR/$name" 2>/dev/null || true
    ok "$INSTALL_DIR/$name (quarantine xattr stripped)"
done

info "Staging configs"
if [[ ! -f "$APP_SUPPORT/cland/cland.yaml" ]]; then
    install -m 0600 "$YAML_CLAND" "$APP_SUPPORT/cland/cland.yaml"
    sed -i.bak \
        -e "s#/Library/Application Support/MINTI/cland#$APP_SUPPORT/cland#g" \
        -e "s#/usr/local/var/log/minti#$LOG_DIR#g" \
        -e "s#/usr/local/bin/minti-cland#$INSTALL_DIR/minti-cland#g" \
        "$APP_SUPPORT/cland/cland.yaml"
    rm -f "$APP_SUPPORT/cland/cland.yaml.bak"
    ok "cland.yaml staged (paths rewritten to \$HOME)"
else
    ok "cland.yaml preserved"
fi
if [[ ! -f "$APP_SUPPORT/runtime/runtime.yaml" ]]; then
    install -m 0600 "$YAML_RUNTIME" "$APP_SUPPORT/runtime/runtime.yaml"
    ok "runtime.yaml staged"
else
    ok "runtime.yaml preserved"
fi
chmod 0700 "$APP_SUPPORT/cland" "$APP_SUPPORT/runtime" "$APP_SUPPORT/workspace"
# The CLI's per-user default config path is $HOME/Library/Application
# Support/MINTI/cland.yaml — same symlink trick as the system branch so
# the workspace's shelled CLI (and the user's bare `minti-cland show`)
# resolve without --config.
if [[ ! -e "$APP_SUPPORT/cland.yaml" ]]; then
    ln -s "$APP_SUPPORT/cland/cland.yaml" "$APP_SUPPORT/cland.yaml"
    ok "cland.yaml symlink ($APP_SUPPORT/cland.yaml)"
fi

# generate_user_plist <label> <src> — sed path rewrites + plutil edits
# (M5-C item 3: structural edits via plutil, never sed-range deletes).
generate_user_plist() {
    local label="$1" src="$2" tmp
    tmp="$(mktemp)"
    sed \
        -e "s#/usr/local/bin/#$INSTALL_DIR/#g" \
        -e "s#/Library/Application Support/MINTI#$APP_SUPPORT#g" \
        -e "s#/usr/local/var/log/minti#$LOG_DIR#g" \
        "$src" > "$tmp"
    plutil -remove UserName "$tmp"
    if [[ "$label" == "com.minti.workspace" ]]; then
        # The user agent runs as the real user: their HOME already
        # resolves the CLI default config (via the symlink above), and
        # PATH needs the user-local bin dir.
        plutil -replace EnvironmentVariables.HOME -string "$HOME" "$tmp"
        plutil -replace EnvironmentVariables.PATH -string "$INSTALL_DIR:/usr/local/bin:/usr/bin:/bin" "$tmp"
    fi
    install -m 0644 "$tmp" "$AGENT_DIR/$label.plist"
    rm -f "$tmp"
    if ! plutil -lint "$AGENT_DIR/$label.plist" >/dev/null; then
        err "generated $label.plist failed plutil -lint"
        exit 5
    fi
    ok "$AGENT_DIR/$label.plist"
}

info "Generating LaunchAgent plists"
generate_user_plist com.minti.cland     "$PLIST_CLAND_SRC"
generate_user_plist com.minti.runtime   "$PLIST_RUNTIME_SRC"
generate_user_plist com.minti.workspace "$PLIST_WORKSPACE_SRC"

detect_ollama

info "Loading services into launchd"
bootstrap_service "$DOMAIN" com.minti.cland     "$AGENT_DIR/com.minti.cland.plist"
bootstrap_service "$DOMAIN" com.minti.runtime   "$AGENT_DIR/com.minti.runtime.plist"
bootstrap_service "$DOMAIN" com.minti.workspace "$AGENT_DIR/com.minti.workspace.plist"

poll_and_open_workspace || true

info "Per-user install complete"
echo
echo "Services:  com.minti.{cland,runtime,workspace}  (launchctl print $DOMAIN/<label>)"
echo "Binaries:  $INSTALL_DIR/minti-{cland,runtime,workspace}"
echo "State:     $APP_SUPPORT/{cland,runtime,workspace}"
echo "Logs:      $LOG_DIR/{cland,runtime,workspace}.{out,err}.log"
echo "Workspace: http://127.0.0.1:8088"
echo "Ollama:    $OLLAMA_STATUS"
echo
echo "Make sure $INSTALL_DIR is on your PATH for the CLI."
echo "Uninstall: bash $SCRIPT_DIR/uninstall-minti.sh  [--purge]"
exit 0
