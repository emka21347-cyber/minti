#!/usr/bin/env bash
# Set up the MINTI Xfce desktop inside WSL Debian for visual preview via
# Windows Remote Desktop. This is a DEV PREVIEW helper, not part of the
# MINTI product — it exists so the desktop look can be inspected without
# building a full ISO + VM.
#
# Run as root inside WSL Debian:
#   sudo bash /mnt/c/Users/aouad/Documents/CCode/MINT/MINT_wip/scripts/wsl-desktop-preview.sh
#
# Then from Windows: mstsc /v:<WSL_IP>:3390  (get IP via `wsl hostname -I`)
# Login: aouad / minti  (change with `passwd` after first login)
#
# Known state (2026-05-21): with all fixes below, the Xfce desktop RENDERS
# correctly (panel, desktop, apps load). The remaining issue is xrdp↔Xorg
# transport instability on WSL specifically — the session drops ~3s after
# real graphics start flowing. That's a deep xrdp-WSL quirk, not a MINTI
# problem. For a stable desktop, use a proper VM (planned for v1.5 ISO
# testing) rather than WSL.
#
# What the fixes here address (in order discovered during debugging):
#   1. Missing zstd dep for Ollama install            (install.sh fix)
#   2. xrdp 0x204 / xrdp_mcs_incoming failed          (security_layer=rdp)
#   3. Xorg can't start from xrdp on fresh Debian     (Xwrapper + xorg-legacy)
#   4. /tmp/.X11-unix RO-mounted by WSLg              (remount via systemd)
#   5. Xfce 4.20 auto-uses Wayland, breaks            (GDK_BACKEND=x11 etc.)
#   6. light-locker aborts without LightDM            (purge)
#   7. Session log path: /tmp is unreliable in WSLg   (log to ~aouad)
set -euo pipefail

login_user="aouad"
login_pass="minti"
rdp_port="3390"

echo "[preview] === step 1: login user ==="
if ! id "${login_user}" >/dev/null 2>&1; then
    useradd -m -s /bin/bash "${login_user}"
    printf '%s:%s\n' "${login_user}" "${login_pass}" | chpasswd
    usermod -aG sudo "${login_user}"
    echo "[preview] created user ${login_user} (CHANGE password with 'passwd' after login)"
else
    echo "[preview] user ${login_user} already exists"
fi

echo "[preview] === step 2: install packages ==="
DEBIAN_FRONTEND=noninteractive apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    xfce4 xfce4-goodies dbus-x11 xorgxrdp xrdp \
    xserver-xorg-core xserver-xorg-legacy \
    >/tmp/xfce-install.log 2>&1
DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq \
    light-locker xfce4-screensaver \
    >/tmp/rmlock.log 2>&1 || true

echo "[preview] === step 3: Xwrapper.config (let xrdp start Xorg) ==="
cat > /etc/X11/Xwrapper.config <<EOF
allowed_users=anybody
needs_root_rights=yes
EOF

echo "[preview] === step 4: xrdp.ini (port + classic RDP security) ==="
sed -i "s/^port=3389/port=${rdp_port}/" /etc/xrdp/xrdp.ini
sed -i 's/^security_layer=.*/security_layer=rdp/' /etc/xrdp/xrdp.ini
sed -i 's/^crypt_level=.*/crypt_level=high/' /etc/xrdp/xrdp.ini

echo "[preview] === step 5: X11 socket dir RW oneshot (WSLg mounts it RO) ==="
# WSLg bind-mounts /tmp/.X11-unix read-only so xrdp's Xorg can't add its
# display socket. Remount RW at every WSL boot via a oneshot unit.
cat > /etc/systemd/system/minti-wsl-x11rw.service <<'EOF'
[Unit]
Description=Remount /tmp/.X11-unix rw so xrdp's Xorg can add its socket
DefaultDependencies=no
After=local-fs.target
Before=xrdp.service
[Service]
Type=oneshot
ExecStart=/bin/mount -o remount,rw /tmp/.X11-unix
ExecStart=/bin/chmod 1777 /tmp/.X11-unix
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now minti-wsl-x11rw.service

echo "[preview] === step 6: xrdp session launcher (force X11 backend) ==="
# Critical: Debian 13's Xfce 4.20 auto-detects WSLg's Wayland socket and
# tries the wlr layer-shell protocol, which xrdp's X11 server can't provide.
# Force GTK/Qt/SDL to X11 explicitly.
cat > /etc/xrdp/startwm.sh <<WM
#!/bin/sh
# Log to /home/${login_user} — /tmp is unreliable under WSLg in this context.
exec >/home/${login_user}/startwm.log 2>&1
echo "=== startwm \$(date) DISPLAY=\$DISPLAY ==="
export XDG_RUNTIME_DIR=/run/user/\$(id -u)
mkdir -p "\$XDG_RUNTIME_DIR" 2>/dev/null
chmod 700 "\$XDG_RUNTIME_DIR" 2>/dev/null
unset WAYLAND_DISPLAY
export GDK_BACKEND=x11
export QT_QPA_PLATFORM=xcb
export CLUTTER_BACKEND=x11
export XDG_SESSION_TYPE=x11
export SDL_VIDEODRIVER=x11
export LIBGL_ALWAYS_SOFTWARE=1
export XDG_CURRENT_DESKTOP=XFCE
rm -f "\$XDG_RUNTIME_DIR"/wayland-0 "\$XDG_RUNTIME_DIR"/wayland-0.lock 2>/dev/null || true
dbus-run-session -- startxfce4
echo "=== xfce exited code \$? \$(date) ==="
WM
chmod +x /etc/xrdp/startwm.sh

echo "[preview] === step 7: enable + start xrdp ==="
systemctl enable xrdp >/dev/null 2>&1 || true
systemctl restart xrdp xrdp-sesman
sleep 1
systemctl is-active xrdp xrdp-sesman

echo ""
echo "[preview] DONE."
echo "[preview] WSL IP: $(hostname -I | awk '{print $1}')"
echo "[preview] From Windows:"
echo "[preview]   mstsc /v:\$(wsl hostname -I | awk '{print \$1}'):${rdp_port}"
echo "[preview]   Login: ${login_user} / ${login_pass}"
echo ""
echo "[preview] Session log (after a connect): /home/${login_user}/startwm.log"
echo "[preview] Known limitation: desktop RENDERS but xrdp transport drops ~3s in."
echo "[preview] For a stable desktop, use a real VM instead of WSL+xrdp."
