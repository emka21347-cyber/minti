#!/usr/bin/env bash
echo "--- apt/dpkg activity ---"
pgrep -al 'apt|dpkg|mintupdate|aptk|unattended' || echo "(no apt processes)"
echo
echo "--- apt lock status ---"
sudo -n fuser /var/lib/dpkg/lock-frontend 2>/dev/null && echo "(lock held)" || echo "(lock free)"
echo
echo "--- last apt action ---"
sudo -n tail -3 /var/log/apt/history.log 2>/dev/null || echo "(no history)"
echo
echo "--- reboot required? ---"
[[ -f /var/run/reboot-required ]] && echo "YES — reboot needed" || echo "no"
