#!/usr/bin/env bash
# M3 in-VM validation. Run via:
#   ssh minti@127.0.0.1 'bash /media/sf_minti-repo/scripts/m3-validate.sh'
#
# Verifies the M3 acceptance criteria end-to-end:
#   1. minti-runtime serves the new /v1/messages (Anthropic) endpoint
#      non-streaming + streaming against a real local model.
#   2. opencode is on PATH after install.sh.
#   3. /etc/minti/opencode.config.example.json + per-user opencode.json land
#      with the MINTI provider + 5 MCP servers wired.
#   4. mcp-recon still works through mcptest (regression check after
#      install.sh ran the M3-extended path).

set -euo pipefail

REPO="/media/sf_minti-repo"
step() { printf "\n=== %s ===\n" "$*"; }

step "Re-run install.sh (idempotent — M3 path: opencode install + config)"
sudo bash "${REPO}/install/install.sh" 2>&1 | tail -25

step "Runtime version reports M3"
curl -sf http://127.0.0.1:7780/minti/version

step "Ensure a small model is resident (llama3.2:3b)"
if ollama list 2>/dev/null | grep -q "llama3.2:3b"; then
    echo "  llama3.2:3b already present."
else
    echo "  pulling llama3.2:3b (this is ~2 GB)..."
    ollama pull llama3.2:3b
fi

step "/v1/messages — non-streaming"
curl -sf -X POST http://127.0.0.1:7780/v1/messages \
    -H "Content-Type: application/json" \
    -H "x-api-key: minti-local-no-auth" \
    -H "anthropic-version: 2023-06-01" \
    -d '{
        "model": "llama3.2:3b",
        "max_tokens": 50,
        "system": "Be very brief. One word answers.",
        "messages": [{"role":"user","content":"Reply with exactly the word PONG."}]
    }' | python3 -c 'import json,sys; r=json.load(sys.stdin); print("type:",r["type"],"role:",r["role"],"stop:",r["stop_reason"]); print("content:",r["content"][0]["text"][:120]); print("usage:",r["usage"])'

step "/v1/messages — streaming SSE (count events)"
curl -sNf -X POST http://127.0.0.1:7780/v1/messages \
    -H "Content-Type: application/json" \
    -H "x-api-key: anything" \
    -d '{
        "model": "llama3.2:3b",
        "stream": true,
        "max_tokens": 30,
        "messages": [{"role":"user","content":"Count from 1 to 3."}]
    }' > /tmp/m3-sse.txt
echo "  event counts:"
for ev in message_start content_block_start content_block_delta content_block_stop message_delta message_stop; do
    n=$(grep -c "^event: ${ev}$" /tmp/m3-sse.txt || true)
    printf "    %-22s %d\n" "${ev}" "${n}"
done
echo "  final lines:"
tail -4 /tmp/m3-sse.txt | sed 's/^/    /'
rm -f /tmp/m3-sse.txt

step "opencode binary"
if command -v opencode >/dev/null 2>&1; then
    opencode --version 2>&1 | head -3
    echo "  resolved: $(command -v opencode) -> $(readlink -f "$(command -v opencode)")"
else
    echo "  opencode NOT on PATH (install.sh reported its status above)"
fi

step "opencode config: system template + per-user default"
ls -la /etc/minti/opencode.config.example.json 2>&1 || true
echo ""
ls -la ~/.config/opencode/opencode.json 2>&1 || true
echo ""
echo "  user config registers these MCP servers:"
python3 -c '
import json, sys, os
p = os.path.expanduser("~/.config/opencode/opencode.json")
if not os.path.exists(p):
    print("    (no user config)"); sys.exit(0)
cfg = json.load(open(p))
for name, server in cfg.get("mcp", {}).items():
    cmd = server.get("command", ["?"])[0]
    enabled = server.get("enabled", False)
    print(f"    {name:<14} -> {cmd} (enabled={enabled})")
print("  provider baseURL:", list(cfg.get("provider", {}).values())[0].get("options", {}).get("baseURL"))
'

step "Regression: mcp-recon via mcptest still works"
mcptest --yes --arg target=127.0.0.1 /opt/minti/mcp/minti-mcp-recon nmap_scan 2>&1 | head -15

step "Audit log: last 3 events"
tail -3 ~/.minti/audit.jsonl

step "Done"
echo "M3 validation complete."
