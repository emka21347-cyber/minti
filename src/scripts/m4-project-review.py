"""Whole-project peer-review pass with local LLMs.

Different from m4-phase{E,J}-review.py: those reviewed individual implementation
plans. THIS one zooms out and asks: how does the project look as a whole?
What's the design coherent / fragile / risky? What's overconfident? What
would a senior reviewer cut, prioritize, redo?

Input: README + PRD principles + STATUS.md current state + clan-protocol.md
spec + a package-shape summary + recent commit log + the deferred items.

Output: scripts/m4-reviews/project-{model}.md + project-input.txt.
"""
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"
MODELS = [
    ("qwen3.6:latest",  {"think": False}),
    ("deepseek-r1:32b", {}),
    ("gemma4:31b",      {}),
]

REPO = r"C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
PRD_PATH = r"C:\Users\aouad\.claude\plans\hello-can-we-create-abundant-hopper.md"
OUT_DIR = os.path.join(REPO, "scripts", "m4-reviews")
os.makedirs(OUT_DIR, exist_ok=True)


def strip_thinking(s: str) -> str:
    return re.sub(r"<think>.*?</think>\s*", "", s, flags=re.DOTALL)


def read(p: str) -> str:
    with open(p, encoding="utf-8") as f:
        return f.read()


def extract_section(text: str, start_marker: str, end_marker: str) -> str:
    a = text.find(start_marker)
    b = text.find(end_marker, a + 1) if a >= 0 else -1
    if a < 0 or b < 0:
        raise SystemExit(f"could not locate: start={start_marker!r} end={end_marker!r}")
    return text[a:b]


def head(path: str, n: int) -> str:
    with open(path, encoding="utf-8") as f:
        return "".join(f.readlines()[:n])


def git_log(n: int) -> str:
    return subprocess.run(
        ["git", "-C", REPO, "log", f"-{n}", "--oneline"],
        capture_output=True, text=True, check=False,
    ).stdout


def package_summary() -> str:
    return """Go modules + package layout (17,474 LoC total):

  cland/internal/                                  -- the Clan daemon (Phase A-J)
    identity/    Ed25519 keypair + member_id persistence on disk (0600)
    state/       Persisted Clan state (clan_key, cert, roster, revocations)
    config/      YAML loader, defaults for all phases
    auditlog/    Cross-component audit log writer (~/.minti/audit.jsonl)
    crypto/      HMAC + SPKI-pin cert + KeyProvider (current+grace)
    transport/   HTTPS server + client with HMAC middleware + nonce cache
    bip39/       12-word mnemonic encode/decode (vendored wordlist)
    membership/  /clan/{create,invite,join,welcome,leave,revoke,members}
    peers/       In-memory Registry (candidates + members), peer-add guardrails
    probe/       Cross-platform hardware probe (Linux + Windows)
    discovery/   grandcat/zeroconf mDNS browse + register
    advertise/   30s capability advertisement loop
    scores/      reasoning_score + system_score formulas
    election/    Spec §5 leader-lease election engine + handlers
    router/      Spec §6 chat-completion routing + self-route fast path
    toolexec/    Spec §7 cross-Clan tool execution via signed tokens
    keyrotate/   Spec §8 2PC key rotation (orchestrator-driven)
    revocations/ Heartbeat-digest gossip + GET /clan/revocations

  cland/cmd/minti-cland/main.go                    -- CLI + daemon entry (~1000 LoC)
  cland/configs/cland.yaml.example                 -- default config template
  cland/systemd/minti-cland.service                -- hardened systemd unit (Phase I)

  runtime-adapter/                                 -- minti-runtime (M1)
    cmd/minti-runtime/                             -- HTTP server
    internal/backend/                              -- Ollama backend + stub
    internal/server/                               -- /v1/chat/completions (OpenAI),
                                                      /v1/messages (Anthropic),
                                                      /api/chat (Ollama),
                                                      /minti/{health,version,capabilities}

  mcp-servers/                                     -- 5 stdio MCP servers (M2)
    cmd/{mcp-fs,mcp-shell,mcp-recon,mcp-http,mcp-pkg}/  -- one binary each
    cmd/mcptest/                                   -- stdio test harness
    internal/{audit,permission,policy,mcpserve,proc}/  -- shared framework

  install/install.sh                               -- one-shot installer (idempotent)
  branding/minti-fetch                             -- neofetch-style status (bash)
  scripts/                                         -- dev/build/peer-review/smoke scripts

Test coverage: cland has unit tests for all 18 packages (~3000 LoC of tests).
  Phase E end-to-end smoke (2-node Clan formation, election, failover) runs
  cleanly on Windows host. Phase I full-install smoke validated on real Linux
  Mint VM. Phase J 3-node (2 VMs + Windows) demonstrated election + failover
  with cross-OS members; full 16-test acceptance gate deferred."""


def deferred_items() -> str:
    return """Known limitations + explicitly deferred items (captured for honesty):

  - Phase H-3 (admitted→active promotion + gossip): members stay "admitted"
    forever in the roster because no code path promotes them on first
    advertisement. Phase J workaround: quorum counts both states. Real fix
    is ~50-100 LoC in peers/handlers + heartbeat digest. Surfaced during
    real-world 3-node validation in Phase J.

  - Full 16-test PowerShell validation gate (scripts/m4-validate.ps1):
    written but not yet run end-to-end. Phase J's Pass 3 demonstrated core
    consensus + cross-OS membership + failover; the full gate adds
    operational coverage (chat-completion routing, cross-Clan tool exec,
    key rotation, revocation) but not fundamental correctness.

  - Phase E peer-review found and the project intentionally did NOT do:
    election-conflict simulation, PROPOSE_TIMEOUT bump for Windows-slow,
    zombie-leader-on-restart hardening, NSSM-based Windows Service for
    cland.exe. All deferred with reasons.

  - v2 items per PRD: WireGuard mesh for off-LAN Clans, per-member
    keypairs (replaces shared clan_key — closes the "any member can forge
    a permission token claiming someone else as origin" gap in §7.1),
    tensor-parallel distributed inference (Hermes 3 405B canonical demo),
    Pack Manager GUI, ISO installer.

  - Test 14 of the 16-gate (mDNS goodbye on hard-kill) — daemon doesn't
    send mDNS goodbye on kill -9. Documented as known; falls back to
    Live=false within FAILOVER_GRACE.

  - One operational rough edge: VBox NAT port-forwarding gets sketchy
    after host-only NIC topology changes; Phase J switched all Clan
    traffic to host-only IPs directly."""


def review_questions() -> str:
    return """Review questions (priority order):

1. **Architectural coherence.** The project blends a Linux distro (MINTI),
   a Go daemon (cland), an HTTP runtime adapter, MCP servers, a tool-pack
   ecosystem, and a cross-OS Clan protocol. Is this scope sensible for
   a one-person v1, or is the surface area too large to ship coherently?
   Where would you cut or sequence differently?

2. **Spec-vs-implementation drift.** The protocol spec at
   docs/clan-protocol.md is the authoritative wire format. Phase J found
   one drift (admitted→active promotion missing). Where else is the code
   likely to diverge from the spec in ways that would only show up at
   real-world scale (e.g. 5+ nodes, longer uptimes, real network
   partitions)?

3. **Security posture.** v1 uses a shared `clan_key` HMAC for all
   member-to-member messages. The PRD explicitly calls this out as a
   v1-limitation that v2 mTLS + per-member keypairs would close. Given
   the current code:
     - What are the real attack vectors a peer-reviewer should worry
       about at v1 ship?
     - Are there security smells in the code organization (e.g. wrong
       defaults, missing rate limits, log injection, audit-log
       trustability)?
     - The toolexec /mcp/execute path lets one Clan member execute
       a tool on another. How robust is the token verification + replay
       cache + policy gate?

4. **Operational readiness.** Phase I validated install + systemd on a
   real Linux Mint VM. Phase J validated 3-node + failover. What
   operational gaps would stop a first non-author user from running
   MINTI successfully?

5. **The peer-review workflow itself.** Each milestone phase has been
   reviewed by 3 local LLMs (qwen, deepseek, gemma) before code lands.
   Findings have been adopted (~70%) or overruled with reasons (~30%).
   Is this process generating real value, or is it theater? What's
   missing from how it's being run?

6. **What's overconfident.** STATUS.md says "M4 done" after a Phase J
   that only ran an abbreviated validation (Pass 3, not full 16-gate).
   Is that overclaim? Where else in the project is success language
   running ahead of what's actually been demonstrated?

7. **Anything else.** Free-form. The reviewer's gut take on what's
   most at risk + what to prioritize for M4.1 vs M5 vs the deferred
   Phase H-3.

End with a one-line verdict:
  VERDICT: looks healthy | proceed with caution on items N..M | block on item K
"""


def build_prompt() -> str:
    readme = read(os.path.join(REPO, "README.md"))
    status = read(os.path.join(REPO, "STATUS.md"))
    spec = read(os.path.join(REPO, "docs", "clan-protocol.md"))
    prd_full = read(PRD_PATH)
    # PRD vision + principles only (lines 1-90 cover §1, §2, §2a)
    prd_principles = "\n".join(prd_full.splitlines()[:90])
    log = git_log(15)

    intro = """You are reviewing the entire MINTI project at the v1 ship boundary.
M4 (the Clan layer) just landed; M0-M3 (install path, runtime, MCP
servers, agent client integration) are committed. Two follow-up items
are explicitly deferred (Phase H-3 admitted-active promotion + the full
16-test PowerShell validation gate).

You are NOT reviewing a single phase or a single file. You are reviewing
the project's design, sequencing, security posture, operational readiness,
and the process by which it has been built.

Be direct. Disagree with the author where you have evidence. Cite specific
spec sections, files, or phase commits when relevant. Length: aim for 800-
1200 words of focused critique, not a comprehensive line-by-line audit
(the codebase is 17K LoC; that's a different exercise).
"""

    parts = [
        intro,
        "\n\n===== DOCUMENT 1: README =====\n\n", readme,
        "\n\n===== DOCUMENT 2: PRD principles (top 90 lines — vision + design rules) =====\n\n", prd_principles,
        "\n\n===== DOCUMENT 3: STATUS.md (current state + phase chronicle) =====\n\n", status,
        "\n\n===== DOCUMENT 4: docs/clan-protocol.md (wire spec) =====\n\n", spec,
        "\n\n===== DOCUMENT 5: Code layout summary =====\n\n", package_summary(),
        "\n\n===== DOCUMENT 6: Recent commit log =====\n\n", log,
        "\n\n===== DOCUMENT 7: Known limitations + deferred items =====\n\n", deferred_items(),
        "\n\n===== REVIEW QUESTIONS =====\n\n", review_questions(),
    ]
    return "".join(parts)


def chat(model: str, prompt: str, extra_body: dict) -> dict:
    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "options": {
            "temperature": 0.3,
            "num_ctx": 24576,
            "num_predict": 16000,
        },
    }
    body.update(extra_body)
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        OLLAMA, data=data, headers={"Content-Type": "application/json"}, method="POST"
    )
    with urllib.request.urlopen(req, timeout=2400) as r:
        return json.loads(r.read().decode("utf-8"))


def main() -> int:
    prompt = build_prompt()
    print(f"input chars={len(prompt)} approx tokens={len(prompt)//4}", flush=True)
    with open(os.path.join(OUT_DIR, "project-input.txt"), "w", encoding="utf-8") as f:
        f.write(prompt)

    summary = []
    for model, extra in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"project-{safe}.md")
        print(f"\n--- {model} ---", flush=True)
        t0 = time.time()
        try:
            resp = chat(model, prompt, extra)
        except Exception as exc:
            print(f"  ERROR: {exc}", flush=True)
            summary.append((model, "ERROR", 0, str(exc)))
            continue
        dur = time.time() - t0
        raw = resp.get("message", {}).get("content", "")
        clean = strip_thinking(raw).strip()
        eval_count = resp.get("eval_count", 0)
        prompt_eval = resp.get("prompt_eval_count", 0)

        with open(out_path, "w", encoding="utf-8") as o:
            o.write(f"# {model} - whole-project review\n\n")
            o.write(f"- wall_time_s: {dur:.1f}\n- prompt_tokens: {prompt_eval}\n")
            o.write(f"- eval_tokens: {eval_count}\n- raw_chars: {len(raw)}\n")
            o.write(f"- clean_chars: {len(clean)}\n- extra_body: {extra}\n\n---\n\n")
            o.write(clean)
            o.write("\n\n---\n\n## Raw (with thinking trace if any)\n\n")
            o.write(raw)

        print(f"  done in {dur:.1f}s; eval={eval_count}; clean={len(clean)}; saved {out_path}", flush=True)
        summary.append((model, "OK", dur, out_path))

    print("\n--- summary ---", flush=True)
    for m, status, dur, info in summary:
        print(f"  {m:<25} {status:<6} {dur:>5.0f}s  {info}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
