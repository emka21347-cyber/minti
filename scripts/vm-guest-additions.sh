#!/usr/bin/env bash
set -e
echo "=== check available vbox packages ==="
apt-cache search '^virtualbox-guest' 2>&1 | head

echo
echo "=== install Guest Additions userspace + X11 (modules ship with kernel) ==="
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    virtualbox-guest-utils virtualbox-guest-x11 2>&1 | tail -10

echo
echo "=== group + user ==="
getent group vboxsf || echo "(vboxsf group missing)"
sudo usermod -aG vboxsf minti
groups minti

echo
echo "=== modules / runtime ==="
lsmod | grep -i vbox || echo "(no vbox modules loaded yet)"
systemctl is-active vboxadd-service 2>&1 || true
