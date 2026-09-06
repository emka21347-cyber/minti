"""Peer-review the M4 Phase J (3-node testbed) plan with local LLMs.

Mirrors m4-phaseE-review.py — same 3-model set, same think:false for qwen,
same output convention — but tailored to Phase J's concerns (multi-node
testbed, Windows-host-as-member, VBoxManage clone workflow, 16-test gate).

Reviewers see:
  - the Phase J plan file (hello-what-do-you-valiant-ullman.md)
  - the super-plan Phase J section + the 16-test acceptance gate
  - a Windows-specific code summary so reviewers catch host-specific bugs
  - a quick recap of what already passed in Phase I
"""
import json
import os
import re
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"
MODELS = [
    ("qwen3.6:latest",  {"think": False}),    # think:false per Phase D round-2 learning
    ("deepseek-r1:32b", {}),                  # explicit reasoning model
    ("gemma4:31b",      {}),                  # strong general-purpose
]

REPO = r"C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
PLAN_PATH = r"C:\Users\aouad\.claude\plans\hello-what-do-you-valiant-ullman.md"
SUPER_PLAN_PATH = r"C:\Users\aouad\.claude\plans\velvet-drifting-codd.md"
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
        raise SystemExit(f"could not locate section: start={start_marker!r} end={end_marker!r}")
    return text[a:b]


def build_prompt() -> str:
    plan_full = read(PLAN_PATH)

    super_full = read(SUPER_PLAN_PATH)
    # Phase J in the super-plan
    try:
        super_phaseJ = extract_section(super_full, "### Phase J", "### Phase K")
    except SystemExit:
        # Some plans use "## Verification" right after J — try that
        super_phaseJ = extract_section(super_full, "### Phase J", "## Verification")

    # The 16-test acceptance gate (super-plan has it under a "Verification" or
    # "M4 acceptance gate" heading)
    try:
        accept_gate = extract_section(super_full, "## Verification — the M4 acceptance gate", "## Explicitly out of M4 scope")
    except SystemExit:
        # Fall back to a regex grab of any "16 ... NEW" lines
        accept_gate = "(could not extract 16-test gate verbatim; review the super-plan directly)"

    windows_summary = """Windows-host-as-member quirks the reviewer should know:

1. `cland/internal/probe/probe_windows.go` deliberately stubs `readOnBattery()`
   to always return false (battery detection is M5 territory). VRAM + uptime
   work via Win32 APIs (GlobalMemoryStatusEx, GetTickCount64). system_score
   on Windows won't get the on-battery penalty (acceptable for fixed testbed).

2. mDNS discovery via grandcat/zeroconf v1.0.0 works on Windows but does
   NOT reliably cross VirtualBox host-only ↔ Windows host boundaries.
   The Phase D plan documented this as the reason peer-add manual fallback
   exists (OQ-2 pulled forward into v1). Phase J uses manual peer-add for
   3-node setup.

3. The `MINTI_CLAND_FORCE_HEALTHY=1` env-var (set via PowerShell:
   `$env:MINTI_CLAND_FORCE_HEALTHY = "1"`) bypasses both:
   - the R1 zombie-leader heartbeat gate (no minti-runtime needed)
   - the localSelf reasoning_enabled gate (so the node IS a candidate)
   Used in Phase E smoke + planned for Phase J's Windows node (no runtime there).

4. Audit log on Windows: `os.UserHomeDir()` returns %USERPROFILE%, so the
   log lands at `C:\\Users\\aouad\\.minti\\audit.jsonl`. mkdir works fine;
   POSIX permission modes (0600/0700) are ignored on Windows.

5. Shared cert priv key for joiners: the founder's clan_cert priv key is
   delivered via /clan/welcome response and persisted into the joiner's
   clan.json (ClanCertPrivKeyB64 field). Verified during exploration — works
   on Windows too. Required because the TLS server presents the shared cert.

6. Cross-Clan tool exec (/mcp/execute) on Windows: not exercised in Phase J.
   The Windows node participates in election + heartbeat + revocation gossip;
   tool execution still happens on Linux VMs (where the MCP server binaries
   live in /opt/minti/mcp/).

7. Quorum at N=3: ⌈3/2⌉ = 2. Failover requires 2 of 3 active. Key rotation
   requires unanimous ACK per H-1 (3 of 3). Revocation gossip needs only one
   heartbeat round to converge.
"""

    phase_i_recap = """Phase I (just shipped, commit 912eafb) end-to-end-validated on real
minti-dev VM:
  - install.sh installs cland clean (5/5 MCP + runtime + cland + opencode)
  - systemd unit comes up active (one operational fix: Environment=HOME=
    /var/lib/minti/cland so audit log clears ProtectHome=true)
  - Lone-member Clan self-elects at term=1
  - minti-fetch shows full Clan-aware surface (clan_id + role + members
    + Orch + (self) tag + term)
  - HTTPS listener returns 401 on unauthed curl (HMAC wired)
  - Re-running install.sh is idempotent (configs preserved, binary hash
    match → no restart)
  - Journald shows "election engine started" → "election won term=1 accepts=1
    quorum=1" cycle clean

So Phase J starts from a known-good single-node baseline. New surface area:
multi-node, multi-OS, mDNS-or-peer-add discovery, real LAN, 16-test gate."""

    instructions = """You are reviewing the implementation plan for MINTI M4 Phase J — a
3-node testbed comprising two cloned Linux VMs (minti-dev + minti-dev-2)
PLUS the Windows 11 host running cland directly. After Phase J, M4 is done.

Phases 0/A/B/C/D/E/F/G/H-1/H-2/I are all committed + validated. The full
cland test suite (16 packages) is green. The single-node install +
systemd + minti-fetch path was end-to-end-validated on the real Linux VM
in the last session.

Your job is to find concrete problems. Be willing to say "looks good"
rather than padding criticism. Be willing to disagree with the plan
author. Cite spec sections, plan passes, or specific files by their
identifier.

Look for, in this priority order:

1. **3-node-specific correctness gaps.** Things that are fine at N=1 or
   N=2 but break at N=3: quorum off-by-one, election conflict (3 candidates
   simultaneously elect), revocation gossip taking longer than expected
   because of the digest-fetch round-trip, key rotation needing unanimous
   ACK exposing partial-failure cases. Phase E + H-1 + H-2 tests were
   primarily 1-2 node; Phase J is the first real consensus test.

2. **Windows-as-member traps.** The plan calls Windows a "full member"
   running cland.exe directly. Will mDNS actually fail-and-fall-back-cleanly
   to manual peer-add on the VirtualBox host-only ↔ Windows boundary? Are
   there Windows-specific file path / permission / process-lifecycle
   surprises that aren't accounted for? Does running cland.exe as a
   foreground PowerShell job survive long enough for the 16-test gate
   (~30-45 min)?

3. **VBoxManage clone gotchas.** The clone script adds a host-only NIC 2 to
   BOTH VMs, regenerates the MAC on -dev-2, re-adds the shared folder.
   What's missing? (Examples: SSH host keys conflict because they were
   cloned verbatim; static MAC in netplan; persistent network rules on
   eth0/eth1 baked into udev; previous /etc/minti/cland.yaml from the
   parent VM if cland was already installed; the existing identity.json
   would clone too — same member_id on both VMs is a real bug source.)

4. **Test ordering + state contamination.** The 16 tests run sequentially
   and mutate state (revoke, pin, kill, restart). Are there tests that
   depend on cleanup the prior test didn't perform? E.g., test 6 stops
   minti-cland on A; test 7 restarts A + pins — but does the systemd
   restart re-fetch state cleanly, or does the in-memory election state
   need warming-up time the test doesn't provide?

5. **Pre-flagged-failure-mode adequacy.** The plan flags test 14 (mDNS
   goodbye) and test 10 (third-node post-revoke) as expected non-blockers.
   Are these calls correct, or is the plan hiding real bugs as "expected
   fail"?

6. **Time estimates + sequencing risk.** 2 hours total estimated; Pass 4
   alone is 30-45 min. If something fails midway through Pass 4, can the
   script resume from where it left off, or does it have to restart from
   scratch (re-creating the Clan etc.)?

7. **Anything the plan is overconfident about.** "No protocol or daemon-
   code changes anticipated" — is this realistic given 3-node consensus is
   genuinely new territory?

End your review with a short verdict:
  VERDICT: ship plan v1 as-is | ship after addressing items N..M | block on item K

Below are FOUR documents.
"""

    parts = [
        instructions,
        "==================================================================\n"
        "DOCUMENT 1: The Phase J plan being reviewed\n"
        "==================================================================\n\n",
        plan_full,
        "\n\n==================================================================\n"
        "DOCUMENT 2: Super-plan Phase J section + the 16-test acceptance gate\n"
        "==================================================================\n\n",
        super_phaseJ,
        "\n\n--- M4 acceptance gate ---\n\n",
        accept_gate,
        "\n\n==================================================================\n"
        "DOCUMENT 3: Windows-host-as-member code summary\n"
        "==================================================================\n\n",
        windows_summary,
        "\n\n==================================================================\n"
        "DOCUMENT 4: Phase I end-state recap (the starting point for Phase J)\n"
        "==================================================================\n\n",
        phase_i_recap,
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
    print(f"input chars={len(prompt)}, approx tokens={len(prompt)//4}", flush=True)

    with open(os.path.join(OUT_DIR, "phaseJ-input.txt"), "w", encoding="utf-8") as f:
        f.write(prompt)

    summary = []
    for model, extra in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"phaseJ-{safe}.md")
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
            o.write(f"# {model} - Phase J plan review\n\n")
            o.write(f"- wall_time_s: {dur:.1f}\n- prompt_tokens: {prompt_eval}\n")
            o.write(f"- eval_tokens: {eval_count}\n- raw_chars: {len(raw)}\n")
            o.write(f"- clean_chars: {len(clean)}\n- extra_body: {extra}\n\n---\n\n")
            o.write(clean)
            o.write("\n\n---\n\n## Raw (with thinking trace if any)\n\n")
            o.write(raw)

        print(
            f"  done in {dur:.1f}s; eval_tokens={eval_count}; "
            f"clean_chars={len(clean)}; saved {out_path}",
            flush=True,
        )
        summary.append((model, "OK", dur, out_path))

    print("\n--- summary ---", flush=True)
    for m, status, dur, info in summary:
        print(f"  {m:<25} {status:<6} {dur:>5.0f}s  {info}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
