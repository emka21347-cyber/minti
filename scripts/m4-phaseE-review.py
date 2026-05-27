"""Peer-review the M4 Phase E (leader-lease election) plan with local LLMs.

Mirrors m4-peer-review.py (which reviewed the full M4 plan in Phase 0) but is
scoped to Phase E: sends each model a focused prompt containing
  - the Phase E plan file (hello-what-do-you-valiant-ullman.md)
  - the Phase E section from the approved super-plan (velvet-drifting-codd.md)
  - spec section 5 (Leader-Lease Election) verbatim
  - a Phase D code summary so reviewers can spot drift / reuse misses

Per memory/reference_local_llms.md: qwen3.6 gets think:false to avoid the
trace-overflow failure mode observed in the Phase D Round 1 review.
"""
import json
import os
import re
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"
MODELS = [
    ("qwen3.6:latest",  {"think": False}),       # think:false per round-2 Phase D learning
    ("deepseek-r1:32b", {}),                     # explicit reasoning model
    ("gemma4:31b",      {}),                     # strong general-purpose
]

REPO = r"C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
PLAN_PATH = r"C:\Users\aouad\.claude\plans\hello-what-do-you-valiant-ullman.md"
SUPER_PLAN_PATH = r"C:\Users\aouad\.claude\plans\velvet-drifting-codd.md"
SPEC_PATH = os.path.join(REPO, "docs", "clan-protocol.md")
OUT_DIR = os.path.join(REPO, "scripts", "m4-reviews")
os.makedirs(OUT_DIR, exist_ok=True)


def strip_thinking(s: str) -> str:
    return re.sub(r"<think>.*?</think>\s*", "", s, flags=re.DOTALL)


def read(p: str) -> str:
    with open(p, encoding="utf-8") as f:
        return f.read()


def extract_section(text: str, start_marker: str, end_marker: str) -> str:
    """Slice between (and including) start_marker and the line containing end_marker."""
    a = text.find(start_marker)
    b = text.find(end_marker, a + 1) if a >= 0 else -1
    if a < 0 or b < 0:
        raise SystemExit(f"could not locate section: start={start_marker!r} end={end_marker!r}")
    return text[a:b]


def build_prompt() -> str:
    spec_full = read(SPEC_PATH)
    spec_s5 = extract_section(spec_full, "## 5. Leader-Lease Election", "## 6. Routing & Scoring")

    super_full = read(SUPER_PLAN_PATH)
    super_phaseE = extract_section(super_full, "### Phase E — Leader-lease election", "### Phase F — Routing layer")

    plan_full = read(PLAN_PATH)

    code_summary = """Phase D code shapes the plan builds on (verbatim from the codebase):

cland/internal/state/state.go — `Clan` struct currently has:
  ClanID, ClanKeyB64, ClanCertPEM, ClanCertPin, ClanCertPrivKeyB64,
  Role ("founder" | "joined"), JoinedAt time.Time,
  Roster []RosterMember.
`RosterMember` has: MemberID, PubKeyB64, State ("admitted" | "active" | "revoked"),
  AdmittedAt time.Time, LastSeenAt time.Time.
Note: there is NO JoinedAt on RosterMember — only AdmittedAt. The plan reuses
AdmittedAt as the spec's "oldest joined_at" tiebreaker.

cland/internal/peers/peers.go — `Registry` exposes:
  UpsertCandidate(addr, via), AddPeer(originMemberID, addr) (with TCP pre-dial
  + per-origin rate-limit + 100-entry cap), BindMember(ad, remoteAddr),
  TouchLive(memberID), Snapshot() (Candidate[], Member[]),
  SetRevocations(rev).
`Member` has: MemberID, Address, DiscoveredVia, LastAd, LastSeenAt,
  LatestAd *Advertisement, AdGeneration.
Two predicates: AdFresh(now) = LastAd < 90s; Live(now) = LastSeenAt < 4s.
Currently NOTHING calls TouchLive — Phase E's heartbeat handler is the first
caller, which is the explicit Phase D -> Phase E handoff.

cland/internal/peers/peers.go — `Advertisement` has:
  MemberID, ClanID, Term uint64, Generation uint64, OS, LANAddress,
  Hardware, ReasoningScore int, SystemScore int, Capabilities, Load,
  PinnedOrchestrator bool.

cland/internal/transport — `Server` exposes Handle(pattern, handler) registered
behind HMAC middleware. `Client` is constructed with MemberID + KeyProvider +
Pin + Timeout, auto-attaches the HMAC headers on every request.

cland/internal/crypto — `KeyProvider` interface returns Current() and Grace()
keys, wired through transport from Phase B (D-M4.11).

cland/internal/membership — already registered routes via Service.Register(srv);
`peers.Handlers` follows the same pattern.

cland/cmd/minti-cland/main.go — daemon mode wires the transport server,
membership, advertise loop, and discovery on the listed ports. The Phase E
plan inserts the election engine right after the advertise loop starts.

config: cland/internal/config/config.go currently has Listen/State/Discovery/
Advertise/Runtime/Telemetry blocks (NO Election block yet — Phase E adds it).
"""

    instructions = """You are reviewing the implementation plan for MINTI M4 Phase E — leader-lease
election in the `cland` daemon. The Clan protocol's election engine is the
critical-path distributed-systems component. M0-M3 are done; Phase D (capability
advertisement + peer registry) just landed and was end-to-end-smoked between
two daemons on 127.0.0.1.

Your job is to find concrete problems, not to be polite. Be willing to say
"looks good" rather than padding criticism. Be willing to disagree with the
plan author. Cite spec sections (e.g. "spec §5.4") or plan sections (e.g.
"State persistence") by their identifier so the author can find them.

Look for, in this priority order:

1. **Distributed-systems correctness.** Split-brain at partition boundaries,
   term-monotonicity violations, race conditions between heartbeat tick and
   election tick, lease-expiry off-by-one, quorum computation from the wrong
   roster (live vs persisted), heartbeat anti-spoof bypass, election-history
   ring corruption under concurrent writes.

2. **Spec drift.** Places where the plan diverges from clan-protocol.md §5
   without flagging the divergence. The cadence constants are locked
   (HEARTBEAT=2s / LEASE=8s / FAILOVER=6s / TIMEOUT=1s / MIN_TERM_INCREMENT=1)
   — does the plan honor them? Is the multi-pin tiebreaker exactly per §5.6?
   Is the anti-spoof check exactly per §5.3?

3. **Sequencing / reuse drift.** The plan reuses Phase D helpers
   (peers.Registry.TouchLive, peers.Member.Live, transport.Client,
   state.Store.SaveClan, crypto.KeyProvider). Is the reuse correct? Are there
   helpers the plan re-introduces unnecessarily? Are there Phase D guarantees
   the plan relies on but the Phase D code doesn't actually provide?

4. **State persistence races.** The plan persists currentTerm + leaseExpires
   on every heartbeat-applied + election commit. The existing SaveClan is
   atomic-rename but serialised on a single mutex. Is there a path where a
   crashed write could leave the daemon thinking it's still Orchestrator when
   it isn't (or vice versa)?

5. **Pin / advertisement propagation.** The plan flips state.Clan.
   PinnedOrchestrator via a CLI and relies on the next advertise tick to
   propagate. Is the latency acceptable? Is there a race where election can
   run between pin-flip and ad-propagation, producing a wrong-winner outcome?

6. **Edge cases not in the 11-test unit matrix + 10-step smoke.** Failure
   modes that should be tested but aren't. Examples to consider: clock skew
   between Orchestrator and peer, Orchestrator's runtime-adapter going down
   (does it still emit heartbeats?), heartbeat broadcast partial-success
   (3/5 succeed, 2/5 timeout), restart during election commit.

7. **Overconfidence.** "Random backoff converges" is a probabilistic claim;
   does the test plan validate it? "Persisted roster never disagrees with
   peers.Registry" — is that actually true under any sequence of joins +
   revocations?

Be specific. Quote the line of the plan you're objecting to. If a finding is
out of Phase E scope (e.g. belongs to Phase F routing), say so and move on.

End your review with one short verdict line:
  VERDICT: ship the plan as-is | ship after addressing items N..M | block on item K

Below are FOUR documents.
"""

    parts = [
        instructions,
        "==================================================================\n"
        "DOCUMENT 1: Spec §5 (Leader-Lease Election) verbatim\n"
        "==================================================================\n\n",
        spec_s5,
        "\n\n==================================================================\n"
        "DOCUMENT 2: Approved super-plan, Phase E section (velvet-drifting-codd.md)\n"
        "==================================================================\n\n",
        super_phaseE,
        "\n\n==================================================================\n"
        "DOCUMENT 3: Phase D code shapes the plan builds on\n"
        "==================================================================\n\n",
        code_summary,
        "\n\n==================================================================\n"
        "DOCUMENT 4: The Phase E plan being reviewed (hello-what-do-you-valiant-ullman.md)\n"
        "==================================================================\n\n",
        plan_full,
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

    # Persist the assembled prompt so we can diff between runs and so reviews
    # are reproducible from disk.
    with open(os.path.join(OUT_DIR, "phaseE-input.txt"), "w", encoding="utf-8") as f:
        f.write(prompt)

    summary = []
    for model, extra in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"phaseE-{safe}.md")
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
            o.write(f"# {model} - Phase E plan review\n\n")
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
