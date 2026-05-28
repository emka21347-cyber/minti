#!/usr/bin/env bash
# minti-cland installer for macOS (10.13 High Sierra and newer).
#
# System install (run as root): registers as a launchd LaunchDaemon under a
# dedicated _minti service account, state at /Library/Application Support/MINTI/cland.
#
# Per-user install (run as non-root): user-scoped LaunchAgent at
# ~/Library/LaunchAgents/com.minti.cland.plist, state at
# ~/Library/Application Support/MINTI/cland.
#
# M5-C plan -- the dscl + launchctl recipes are the riskiest part of M5
# (no live Mac available during M5; relies on plutil-lint + shellcheck +
# a second LLM review pass for cross-version confidence).
#
# Tested encoding: this file is UTF-8 (LF). bash 3.2 (the macOS system
# bash, frozen for licensing reasons) is the assumed runtime; no
# bash-4-only features.

set -euo pipefail

# ---------- arg parsing ----------

PURGE_NOT_USED=0   # placeholder; install has no --purge
FORCE_BOOTSTRAP=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            cat <<'EOF'
Usage: install-cland.sh [--force-bootstrap]

Installs minti-cland as a launchd-managed service.

Detection:
  root  ($EUID==0)   -- system install (LaunchDaemon)
  user                -- per-user install (LaunchAgent)

Options:
  --force-bootstrap   Bootout + bootstrap even if a previous version is
                      already loaded. Default: bootstraps only if not
                      already loaded.
EOF
            exit 0
            ;;
        --force-bootstrap)
            FORCE_BOOTSTRAP=1; shift ;;
        *)
            echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

# ---------- log helpers ----------

info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mok:\033[0m %s\n' "$*"; }
warn() { printf '  \033[33mwarn:\033[0m %s\n' "$*"; }
err()  { printf '  \033[31mERROR:\033[0m %s\n' "$*" >&2; }

# ---------- preflight ----------

if [[ "$(uname -s)" != "Darwin" ]]; then
    err "this installer is for macOS; current OS=$(uname -s)"
    exit 2
fi

# Resolve script dir (where the tarball was extracted).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Bundled artefacts.
BUNDLED_BIN="$SCRIPT_DIR/bin/minti-cland"
BUNDLED_PLIST="$SCRIPT_DIR/com.minti.cland.plist"
BUNDLED_YAML="$SCRIPT_DIR/configs/cland.yaml.darwin.example"
BUNDLED_RUBRIC="$SCRIPT_DIR/configs/reasoning-scores.yaml.example"

for f in "$BUNDLED_BIN" "$BUNDLED_PLIST" "$BUNDLED_YAML"; do
    [[ -f "$f" ]] || { err "missing required bundle file: $f"; exit 3; }
done

# ---------- branch on euid ----------

if [[ $EUID -eq 0 ]]; then
    INSTALL_MODE=system
else
    INSTALL_MODE=user
fi

info "MINTI cland macOS installer (mode=$INSTALL_MODE)"

# Common knobs (overridden by user branch below).
SVC_LABEL="com.minti.cland"

# =====================================================================
#                          SYSTEM INSTALL
# =====================================================================
if [[ "$INSTALL_MODE" == "system" ]]; then

    INSTALL_BIN="/usr/local/bin/minti-cland"
    STATE_DIR="/Library/Application Support/MINTI/cland"
    SYS_RUBRIC_DIR="/Library/Application Support/MINTI"
    LOG_DIR="/usr/local/var/log/minti"
    PLIST_DEST="/Library/LaunchDaemons/com.minti.cland.plist"
    BOOTSTRAP_DOMAIN="system"

    # ---------- 1. ensure _minti service account ----------
    #
    # Apple convention: system service accounts have a leading underscore
    # and live in the <500 UID range. Default macOS uses 200-400 heavily;
    # we scan dynamically for the first free UID starting at 270 -- DO
    # NOT hardcode (M5 peer-review item 3 from qwen + deepseek + gemma).
    #
    # If _minti already exists, skip creation entirely (idempotent
    # re-install).

    info "Ensuring _minti service account"

    if dscl . -read /Users/_minti >/dev/null 2>&1; then
        existing_uid="$(dscl . -read /Users/_minti UniqueID 2>/dev/null | awk '{print $2}')"
        ok "_minti exists (uid=$existing_uid); leaving as-is"
    else
        # Find lowest free UID >= 300 and < 500.
        #
        # M5-C peer-review item 5 (qwen): Apple populates 200-299 densely
        # (_www=70, _postgres=216, _mysql=74, _screensaver=*, etc.). Starting
        # the scan at 300 puts us cleanly in the third-party system-daemon
        # convention range.
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
            err "your Mac has an unusual concentration of system users"
            exit 4
        fi
        ok "chosen uid/gid: $chosen_uid"

        # Create the group first so PrimaryGroupID resolves cleanly.
        dscl . -create  /Groups/_minti
        dscl . -create  /Groups/_minti PrimaryGroupID "$chosen_uid"
        dscl . -create  /Groups/_minti RealName       "MINTI cland service"

        dscl . -create  /Users/_minti
        dscl . -create  /Users/_minti UniqueID         "$chosen_uid"
        dscl . -create  /Users/_minti PrimaryGroupID   "$chosen_uid"
        dscl . -create  /Users/_minti UserShell        /usr/bin/false
        dscl . -create  /Users/_minti RealName         "MINTI cland service"
        dscl . -create  /Users/_minti NFSHomeDirectory /var/empty
        # IsHidden hides from login window on 10.13-10.15; HiddenSystemUser is
        # the canonical key on 11+ (Big Sur and newer). Set both -- belt and
        # braces, both are harmless on the version that doesn't recognise
        # them (M5-C peer-review item 1: qwen + deepseek).
        dscl . -create  /Users/_minti IsHidden         1
        dscl . -create  /Users/_minti HiddenSystemUser 1

        ok "_minti created (uid=gid=$chosen_uid; hidden from login window)"
    fi

    # ---------- 2. install binary ----------
    info "Installing minti-cland binary"
    install -m 0755 "$BUNDLED_BIN" "$INSTALL_BIN"
    # Strip the Gatekeeper quarantine xattr if present. Binaries downloaded
    # via a browser get com.apple.quarantine attached; launchd may refuse
    # to exec them or pop a "cannot be opened" dialog. The xattr is harmless
    # on binaries extracted from a tarball where it was never attached --
    # the -d call is idempotent (M5-C peer-review item 2: gemma).
    xattr -d com.apple.quarantine "$INSTALL_BIN" 2>/dev/null || true
    ok "$INSTALL_BIN"

    # ---------- 3. state dir (load-bearing for security) ----------
    info "Preparing state dir"
    mkdir -p "$STATE_DIR"
    chown -R _minti:_minti "$STATE_DIR"
    chmod 0700 "$STATE_DIR"
    ok "$STATE_DIR (owner=_minti, mode=0700)"

    # ---------- 4. drop default configs (preserve on re-install) ----------
    info "Staging configs"
    if [[ ! -f "$STATE_DIR/cland.yaml" ]]; then
        install -m 0640 -o _minti -g _minti "$BUNDLED_YAML" "$STATE_DIR/cland.yaml"
        ok "cland.yaml installed"
    else
        ok "cland.yaml preserved"
    fi
    if [[ -f "$BUNDLED_RUBRIC" && ! -f "$STATE_DIR/reasoning-scores.yaml" ]]; then
        install -m 0640 -o _minti -g _minti "$BUNDLED_RUBRIC" "$STATE_DIR/reasoning-scores.yaml"
        ok "reasoning-scores.yaml installed"
    fi
    # System-wide rubric mirror (matches paths_unix.go DefaultRubricPath
    # for darwin root). Allows the daemon to find the rubric even if the
    # cland.yaml override is removed.
    mkdir -p "$SYS_RUBRIC_DIR"
    if [[ -f "$STATE_DIR/reasoning-scores.yaml" && ! -f "$SYS_RUBRIC_DIR/reasoning-scores.yaml" ]]; then
        cp "$STATE_DIR/reasoning-scores.yaml" "$SYS_RUBRIC_DIR/reasoning-scores.yaml"
        chmod 0644 "$SYS_RUBRIC_DIR/reasoning-scores.yaml"
        ok "system rubric mirror at $SYS_RUBRIC_DIR/reasoning-scores.yaml"
    fi

    # ---------- 5. log dir ----------
    info "Preparing log dir"
    mkdir -p "$LOG_DIR"
    chown _minti:_minti "$LOG_DIR"
    chmod 0750 "$LOG_DIR"
    ok "$LOG_DIR"

    # ---------- 6. plist ----------
    info "Installing LaunchDaemon plist"
    install -m 0644 -o root -g wheel "$BUNDLED_PLIST" "$PLIST_DEST"
    if ! plutil -lint "$PLIST_DEST" >/dev/null; then
        err "plist failed plutil -lint after install"
        exit 5
    fi
    ok "$PLIST_DEST"

    # ---------- 7. bootstrap ----------
    #
    # `launchctl bootstrap system <plist>` is the canonical modern API
    # (macOS 10.10+, when launchctl was rewritten around domains). The
    # historical `launchctl load -w` is still accepted but the man page
    # has marked it as legacy since 10.10. We try bootstrap first; if it
    # fails with "Bootstrap failed: 5: Input/output error" (which usually
    # means "already loaded"), fall back to bootout+bootstrap.
    #
    # On 10.13 High Sierra both forms work the same; on 10.15+ bootstrap
    # is the only well-supported form. Documented per
    # https://www.launchd.info/

    info "Loading service into launchd"

    # If already loaded, always boot out so the bootstrap below starts
    # cleanly. The conditional --force-bootstrap was a thinko -- on a
    # re-install of an upgraded binary we MUST bootout first, otherwise
    # the old binary stays mapped (M5-C peer-review item 4: gemma).
    # The 2>/dev/null swallows the "not loaded" error on first install.
    if launchctl print "$BOOTSTRAP_DOMAIN/$SVC_LABEL" >/dev/null 2>&1; then
        launchctl bootout "$BOOTSTRAP_DOMAIN/$SVC_LABEL" 2>/dev/null || true
        ok "previous instance booted out"
    fi
    # --force-bootstrap is now informational only; bootout always runs.
    [[ "$FORCE_BOOTSTRAP" -eq 1 ]] && ok "(--force-bootstrap requested; behaviour is already default)"

    if ! launchctl bootstrap "$BOOTSTRAP_DOMAIN" "$PLIST_DEST" 2>/dev/null; then
        # Bootstrap can also fail on old macOS where the kernel rejects
        # the bootstrap call. Fall back to legacy `load -w`.
        warn "launchctl bootstrap failed; falling back to legacy load -w"
        launchctl load -w "$PLIST_DEST"
    fi

    # Confirm the daemon is loaded. The exact `state = ...` line varies
    # by macOS version (running / waiting / idle), so we just check that
    # launchctl print recognises the service -- a soft confirmation;
    # never blocks the installer (M5-C peer-review item 6: qwen + deepseek).
    sleep 1
    if launchctl print "$BOOTSTRAP_DOMAIN/$SVC_LABEL" >/dev/null 2>&1; then
        ok "service is loaded under launchd"
    else
        warn "launchctl print does not show $SVC_LABEL; check $LOG_DIR/cland.err.log"
    fi

    # ---------- 8. firewall hint ----------
    info "macOS Application Firewall"
    warn "On first inbound connection, macOS may prompt: 'Allow incoming"
    warn "network connections for minti-cland?' -- click Allow."
    warn "Cannot pre-approve from the command line without a GUI dialog;"
    warn "see README.md > Firewall for the manual pre-approval flow."

    # ---------- summary ----------
    info "Install complete"
    echo
    echo "Label:    $SVC_LABEL"
    echo "Binary:   $INSTALL_BIN"
    echo "State:    $STATE_DIR"
    echo "Logs:     $LOG_DIR/cland.{out,err}.log"
    echo "Config:   $STATE_DIR/cland.yaml"
    echo
    echo "Verify:"
    echo "  sudo launchctl print system/$SVC_LABEL | head -40"
    echo "  $INSTALL_BIN show --config '$STATE_DIR/cland.yaml'"
    echo "  nc -zv 127.0.0.1 7777    # (after you join a Clan)"
    echo
    exit 0
fi

# =====================================================================
#                          PER-USER INSTALL
# =====================================================================

# User-mode paths.
INSTALL_BIN="$HOME/.local/bin/minti-cland"
STATE_DIR="$HOME/Library/Application Support/MINTI/cland"
LOG_DIR="$HOME/Library/Logs/minti-cland"
PLIST_DEST="$HOME/Library/LaunchAgents/com.minti.cland.plist"
USER_UID="$(id -u)"
BOOTSTRAP_DOMAIN="gui/$USER_UID"

info "Per-user install (no root)"

mkdir -p "$(dirname "$INSTALL_BIN")" "$STATE_DIR" "$LOG_DIR" "$(dirname "$PLIST_DEST")"

install -m 0755 "$BUNDLED_BIN" "$INSTALL_BIN"
# Same Gatekeeper xattr-strip as the system branch (M5-C peer-review
# item 2: gemma).
xattr -d com.apple.quarantine "$INSTALL_BIN" 2>/dev/null || true
ok "$INSTALL_BIN"

if [[ ! -f "$STATE_DIR/cland.yaml" ]]; then
    install -m 0600 "$BUNDLED_YAML" "$STATE_DIR/cland.yaml"
    # Per-user config needs different paths than the system plist
    # contains -- rewrite to point at $HOME-rooted locations.
    sed -i.bak \
        -e "s#/Library/Application Support/MINTI/cland#$STATE_DIR#g" \
        -e "s#/usr/local/var/log/minti#$LOG_DIR#g" \
        -e "s#/usr/local/bin/minti-cland#$INSTALL_BIN#g" \
        "$STATE_DIR/cland.yaml"
    rm -f "$STATE_DIR/cland.yaml.bak"
    ok "cland.yaml staged (paths rewritten to \$HOME)"
else
    ok "cland.yaml preserved"
fi
chmod 0700 "$STATE_DIR"

# Build a user-scoped plist on the fly. Path rewrites via sed are safe
# (literal string replace); the UserName removal goes through `plutil
# -remove` instead of a fragile sed-range deletion (M5-C peer-review
# item 3: qwen + gemma). plutil ships on every macOS since 10.7 so this
# is universally available.
USER_PLIST_TMP="$(mktemp)"
sed \
    -e "s#/usr/local/bin/minti-cland#$INSTALL_BIN#g" \
    -e "s#/Library/Application Support/MINTI/cland#$STATE_DIR#g" \
    -e "s#/usr/local/var/log/minti#$LOG_DIR#g" \
    "$BUNDLED_PLIST" > "$USER_PLIST_TMP"
# Remove the UserName key via plutil -- it does real XML editing,
# preserves formatting, and fails loudly on a malformed plist.
plutil -remove UserName "$USER_PLIST_TMP"

install -m 0644 "$USER_PLIST_TMP" "$PLIST_DEST"
rm -f "$USER_PLIST_TMP"

if ! plutil -lint "$PLIST_DEST" >/dev/null; then
    err "generated user plist failed plutil -lint"
    exit 5
fi
ok "$PLIST_DEST"

# Bootstrap into the GUI session domain.
if launchctl print "$BOOTSTRAP_DOMAIN/$SVC_LABEL" >/dev/null 2>&1; then
    launchctl bootout "$BOOTSTRAP_DOMAIN/$SVC_LABEL" 2>/dev/null || true
fi
launchctl bootstrap "$BOOTSTRAP_DOMAIN" "$PLIST_DEST"
sleep 1
if launchctl print "$BOOTSTRAP_DOMAIN/$SVC_LABEL" 2>/dev/null | grep -q 'state = running'; then
    ok "service is running (user agent)"
else
    warn "launchctl print does not show 'state = running'; check $LOG_DIR/cland.err.log"
fi

info "Per-user install complete"
echo
echo "Label:    $SVC_LABEL"
echo "Binary:   $INSTALL_BIN"
echo "State:    $STATE_DIR"
echo "Logs:     $LOG_DIR/cland.{out,err}.log"
echo "Config:   $STATE_DIR/cland.yaml"
echo
echo "Make sure $HOME/.local/bin is on your PATH for the CLI to be"
echo "discoverable; the LaunchAgent uses the absolute path so it runs"
echo "either way."
