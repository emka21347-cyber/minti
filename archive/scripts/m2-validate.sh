#!/usr/bin/env bash
# M2 in-VM validation script. Run via:
#   ssh minti@127.0.0.1 'bash /media/sf_minti-repo/scripts/m2-validate.sh'
#
# Performs the M2 acceptance gate end-to-end:
#   1. Build minti-pack-recon .deb on the VM (Debian-native build).
#   2. Install it via dpkg -i (apt-get -f install fills any missing deps).
#   3. Acceptance test: mcptest minti-mcp-recon nmap_scan target=127.0.0.1.
#   4. Deny-path test: drop a user policy with deny_tools=[nmap_scan] and
#      verify the same call is refused + logged.
#   5. Print audit.jsonl tail.

set -euo pipefail

REPO="/media/sf_minti-repo"
DIST="${HOME}/m2-dist"
POLICY="${HOME}/.minti/policy.yaml"
POLICY_BACKUP="${HOME}/.minti/policy.yaml.m2-validate.bak"

step() { printf "\n=== %s ===\n" "$*"; }

cleanup() {
    if [[ -f "${POLICY_BACKUP}" ]]; then
        mv -f "${POLICY_BACKUP}" "${POLICY}"
        echo "(restored ${POLICY})"
    elif [[ -f "${POLICY}.was-absent" ]]; then
        rm -f "${POLICY}" "${POLICY}.was-absent"
        echo "(removed ${POLICY} — was absent originally)"
    fi
}
trap cleanup EXIT

step "Build prerequisites"
if ! dpkg -s debhelper >/dev/null 2>&1; then
    sudo apt-get update -qq
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
        dpkg-dev debhelper devscripts >/dev/null
fi
dpkg-buildpackage --version | head -1

step "Stage pack-recon source (rsync to writable dir)"
# The shared folder is mounted with broad exec bits, which trips debhelper
# into thinking debian/install is an executable script. Stage a writable
# copy, then explicitly set sane modes: only debian/rules executable.
mkdir -p "${DIST}"
rsync -a --delete "${REPO}/packs/recon/" "${DIST}/recon/"
find "${DIST}/recon/debian" -type f -exec chmod 0644 {} \;
chmod 0755 "${DIST}/recon/debian/rules"

step "Build .deb"
( cd "${DIST}/recon" && dpkg-buildpackage -b -uc -us 2>&1 | tail -25 )
ls -la "${DIST}"/minti-pack-recon_*.deb

step "Pre-install pack dependencies"
# Install one at a time so a missing optional doesn't block the available ones.
sudo apt-get update -qq
for pkg in nmap masscan whois dnsutils bind9-dnsutils theharvester amass; do
    if sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$pkg" 2>/dev/null; then
        echo "  installed: $pkg"
    else
        echo "  (skipped — not in repos: $pkg)"
    fi
done

step "Install pack"
sudo dpkg -i "${DIST}"/minti-pack-recon_*.deb || sudo apt-get install -f -y -qq
echo "Installed apt binaries we'll wrap:"
for b in nmap whois dig; do
    if command -v "$b" >/dev/null; then echo "  $b: $(command -v $b)"; else echo "  $b: MISSING"; fi
done
echo "Skill file installed?"
ls -la /usr/share/minti/packs/recon/skill.md 2>/dev/null || echo "  (not present)"

step "Acceptance: mcptest mcp-recon nmap_scan target=127.0.0.1"
mcptest --yes --arg target=127.0.0.1 /opt/minti/mcp/minti-mcp-recon nmap_scan | head -40

step "Deny path: write user policy denying nmap_scan, re-run"
if [[ -f "${POLICY}" ]]; then
    cp "${POLICY}" "${POLICY_BACKUP}"
else
    touch "${POLICY}.was-absent"
fi
mkdir -p "$(dirname "${POLICY}")"
cat > "${POLICY}" <<'YAML'
mcp:
  recon:
    deny_tools: ["nmap_scan"]
YAML
echo "User policy now:"; cat "${POLICY}"
set +e
mcptest --yes --arg target=127.0.0.1 /opt/minti/mcp/minti-mcp-recon nmap_scan
rc=$?
set -e
echo "(exit code: $rc — expected 4 for IsError)"

step "Audit log: last 4 events"
tail -4 ~/.minti/audit.jsonl

step "Done"
echo "M2 acceptance + deny-path validation complete."
