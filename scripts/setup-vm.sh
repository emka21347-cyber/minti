#!/usr/bin/env bash
# Run inside a freshly-installed Linux Mint Xfce VM to set up MINTI + Claude Code
# in one shot. Idempotent.
#
# Pre-conditions:
#   - Booted into the installed (NOT live) Mint Xfce system.
#   - VirtualBox Guest Additions installed and the share auto-mounted to
#     /media/sf_minti-repo. (If not, see "Manual share mount" at the bottom.)
#
# Run as root from the shared folder:
#   sudo bash /media/sf_minti-repo/scripts/setup-vm.sh
set -euo pipefail

repo=/media/sf_minti-repo
if [[ ! -d "${repo}" ]]; then
    echo "ERROR: shared folder not mounted at ${repo}"
    echo "       Install VirtualBox Guest Additions, then add yourself to the 'vboxsf' group:"
    echo "         sudo apt-get install -y virtualbox-guest-utils"
    echo "         sudo usermod -aG vboxsf \$USER  (then log out + back in)"
    exit 1
fi

echo "[setup-vm] === step 1: base packages ==="
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    git curl ca-certificates gnupg lsb-release zstd build-essential >/dev/null

echo "[setup-vm] === step 2: run MINTI install.sh ==="
bash "${repo}/install/install.sh"

echo "[setup-vm] === step 3: Claude Code via official apt repo ==="
if ! command -v claude >/dev/null 2>&1; then
    install -d -m 0755 /etc/apt/keyrings
    curl -fsSL https://downloads.claude.ai/keys/claude-code.asc \
        -o /etc/apt/keyrings/claude-code.asc
    echo "deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/stable stable main" \
        > /etc/apt/sources.list.d/claude-code.list
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq claude-code
else
    echo "[setup-vm] claude already installed: $(claude --version 2>&1 | head -1 || echo present)"
fi

echo "[setup-vm] === step 4: pull a starter model so the runtime can chat ==="
if ollama list 2>/dev/null | grep -q llama3.2:3b; then
    echo "[setup-vm] llama3.2:3b already pulled"
else
    ollama pull llama3.2:3b
fi

echo "[setup-vm] === step 5: configure git (so commits work inside the VM) ==="
if [[ -z "$(git config --global user.email 2>/dev/null || true)" ]]; then
    # Default identity; user can change with `git config --global` later.
    git config --global user.email "aouad@minti.local"
    git config --global user.name "aouad"
fi

cat <<BANNER

  ===============================================================
   MINTI dev VM ready.
  ===============================================================

  What's installed:
    - MINTI runtime (systemd: minti-runtime.service)  on :7780
    - Ollama (systemd: ollama.service)                on :11434
    - llama3.2:3b model
    - Claude Code (Anthropic) — run 'claude' to log in

  Quick checks:
    curl http://127.0.0.1:7780/minti/health
    curl http://127.0.0.1:7780/minti/capabilities

  Start a Claude Code session against this repo:
    cd /media/sf_minti-repo
    claude
    # → it opens Firefox for OAuth; log in to your Anthropic account
    # → after that, you're driving MINTI dev FROM INSIDE the VM

  From the Windows host, you can also reach the runtime via the
  VM's NAT port-forward at:
    http://127.0.0.1:17780/minti/health

  ===============================================================
BANNER

# ---- Manual share mount (only if guest additions didn't auto-mount) -------
# sudo mkdir -p /media/sf_minti-repo
# sudo mount -t vboxsf -o uid=$(id -u),gid=$(id -g),rw minti-repo /media/sf_minti-repo
