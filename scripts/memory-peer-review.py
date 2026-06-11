"""Peer-review the Clan Memory Phase 0 (spec §13) with local LLMs.

Mirrors m8-peer-review.py exactly in shape, narrowed to the distributed-
systems- and privacy-focused review Clan Memory needs. Sends each model:

  - Existing gossip/election/audit context (§3.5, §5, §9.1) so reviewers see
    the patterns §13 reuses — and the audit-log §9.1 it deliberately inverts.
  - The NEW §13 spec being reviewed (data model, CRDT-lite merge, digest
    gossip, scribe role, distillation, blueprint).
  - Existing cland code shapes the memory plan reuses (Digest/Merge,
    revocations Syncer, heartbeat passengers, selectCandidate, atomic saves).
  - The Memory implementation plan (M0..M6).

Per memory/reference_local_llms.md: qwen3.6 gets think:false to avoid the
trace-overflow failure mode observed in earlier rounds.
"""
import json
import os
import re
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
PLAN_PATH = r"C:\Users\aouad\.claude\plans\we-can-still-do-transient-glade.md"
SPEC_PATH = os.path.join(REPO, "docs", "clan-protocol.md")
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
    spec_full = read(SPEC_PATH)
    # Context §13 builds on: revocation gossip (the template), election +
    # heartbeat (where the new passengers + scribe selection hook in), and
    # the audit log §13 deliberately inverts.
    spec_revocation = extract_section(spec_full, "### 3.5 Revocation", "## 4. Discovery")
    spec_election = extract_section(spec_full, "## 5. Leader-Lease Election", "## 6. Routing & Scoring")
    spec_audit = extract_section(spec_full, "## 9. Audit Log", "### 9.2 Record format")
    # The new section being reviewed.
    spec_memory = extract_section(
        spec_full,
        "## 13. Clan Memory",
        "*End of Clan Protocol Spec",
    )

    # The implementation plan (everything except the kickstart prompt).
    plan_full = read(PLAN_PATH)
    plan_memory = extract_section(
        plan_full,
        "# Clan Memory — Implementation Plan",
        "## Kickstart prompt",
    )

    code_summary = """Existing cland code shapes the Memory plan builds on (verbatim from the codebase):

cland/internal/state/state.go — the persistence + gossip-digest patterns §13 copies:
  func (r *Revocations) Digest() string
      // sha256-hex of SORTED member_ids, LF-joined. SET-ONLY digest: ignores
      // per-entry timestamps/reasons. Empty list = sha256 of empty input.
      // §13's digest is deliberately DIFFERENT (content-versioned: includes
      // rev + updated_at per node) because LWW edits must change the digest.
  func (r *Revocations) Merge(other *Revocations) *Revocations
      // union, dedup by MemberID, local metadata wins. §13 edges copy this;
      // §13 nodes add the LWW (updated_at, rev, sha256(canonical)) winner rule.
  func saveJSONAtomic(path string, v any, mode os.FileMode) error
      // write-temp + rename. memory.json reuses this with mode 0600.
  loadJSON returns nil+nil when the file is missing (load-missing = empty).

cland/internal/revocations/sync.go — the Syncer that memory/sync.go will copy
near-verbatim:
  func (s *Syncer) MaybeSync(ctx, senderID, theirDigest string) bool
      // empty digest -> skip; load local + compare; per-peer in-flight dedup
      // (map under mutex); LookupAddr from registry, unknown addr -> skip;
      // HMAC GET https://<addr>/clan/revocations with 5s timeout; Merge;
      // persist ONLY if something changed; audit-log the application;
      // fetch errors preserve local state.

cland/internal/election/state.go — the heartbeat §13 adds two passengers to:
  type Heartbeat struct {
      MemberID, ClanID string; Term uint64; LeaseUntil time.Time
      ReasoningScore int; ActiveRoster []string
      RevocationsDigest string `json:"revocations_digest,omitempty"` // H-2
      RosterDigest      string `json:"roster_digest,omitempty"`      // H-3
      // M-Memory adds: MemoryDigest + Scribe, both omitempty strings.
  }

cland/internal/election/engine.go:
  - emitHeartbeats() and runElection() are the TWO sites that build a
    Heartbeat. Both currently do store.LoadRevocations() + Digest() per call
    (cheap for the small revocation list). The plan REFUSES that pattern for
    memory: the engine gets EngineOpts.MemoryDigest func() string returning a
    digest CACHED by the memory service, recomputed only on mutation/merge —
    never a reload/re-hash of memory.json on the 2 s heartbeat path.
  - selectCandidate(): pin-set restriction first (multi-pin -> lowest
    member_id), else HIGHEST reasoning_score, tie -> oldest AdmittedAt, tie ->
    lowest member_id. Candidates = self + registry members with fresh ads +
    reasoning enabled (Live() required once HeartbeatSeen).
    §13's selectScribe INVERTS the score sort (LOWEST reasoning_score wins)
    over scribe_capable active members, same tiebreaks, pinned_scribe override.
  - quorum() counts "active" roster entries, floors at 1.

cland/internal/election/handlers.go — handleHeartbeat:
  decode -> OriginMember auth check -> Engine.OnHeartbeatReceived (anti-spoof:
  sender must equal OUR selectCandidate choice; term gate; foreign clan_id
  reject) -> THEN the optional passengers fire:
      if h.RevocationsSync != nil && hb.RevocationsDigest != "" { MaybeSync }
      if h.RosterSync != nil && hb.RosterDigest != "" { MaybeSync }
  §13 adds a third: if h.MemorySync != nil && hb.MemoryDigest != "" { MaybeSync }
  All passengers optional-nil and independent; a passenger can't fail the
  heartbeat (errors are logged, response already committed).

cland/internal/identity/identity.go newUUIDv4(): 16 bytes crypto/rand, set
version/variant nibbles, format 8-4-4-4-12. §13's deterministic ids reuse the
fold (sha256(clan_id|kind|subject|qualifier)[0:16] -> same nibble surgery).

cland/cmd/minti-cland/main.go: services Register(srv) on the HMAC transport
around line 457 (revocations.Handler, rostersync.Handler, election.Handlers
with the syncer deps). CLI subcommands talk to the local daemon through a
loopback HMAC client (localDaemonClient pattern); transport.OriginMember
gives handlers the authenticated member id — this is what sets
provenance.author_member_id server-side.

workspace/internal/clan/clan.go: the workspace NEVER speaks HMAC itself — it
shells `minti-cland <sub> --json` (exec.LookPath + CommandContext), degrades
to a demo snapshot when cland is missing. memory.go will copy this contract.

Constraints in force: P1 = no new external Go deps (hand-rolled UUIDs, stdlib
sha256/json only). P2 = lean for 1-2 GB boxes (caps, cached digest, frozen
force-layout). P3 = this spec §13 wins over any clever implementation idea.
"""

    instructions = """You are reviewing the protocol spec + implementation plan for MINTI "Clan
Memory" — a NEW gossiped, Clan-owned, curated memory GRAPH for distributed
research. Every member contributes findings into one shared graph; an elected
"Scribe" role on the WEAKEST capable node runs a small local LLM that
continuously distills Clan activity into proposed memories (a human must
promote them); explicit research sessions group contributions; the graph
renders live in the Clan Workspace; and the whole thing (or one session)
exports as a single-file "Clan Blueprint" a fresh Clan imports at create.

Architecture in one breath: memory is the OPPOSITE of the audit log (§9.1
audit is never gossiped; memory IS — every member holds the full graph). It
reuses the proven revocations-gossip pattern: a digest rides the election
heartbeat as a third passenger; on mismatch a peer fetches the full graph
over HMAC and merges. Merge is CRDT-lite: nodes keyed by id with LWW winner
(updated_at, rev, sha256(canonical_bytes)) — hash term breaks equal-timestamp
ties deterministically; deletes are status:"archived" tombstones (ordinary
LWW field edits); edges are a set-union deduped by (from,to,relation),
add-only in v1. Scribe selection INVERTS orchestrator selection: lowest
reasoning_score among scribe_capable active members, announced
authoritatively by the Orchestrator in the heartbeat `scribe` field. No
lease for the scribe — memory loss is non-fatal, re-select on miss.

Your job is to find concrete problems, not to be polite. Be willing to say
"looks good" rather than padding criticism. Be willing to disagree with the
spec author. Cite spec subsections (e.g. "§13.4 merge semantics") or plan
sections (e.g. "Phases M2") by name so the author can find them.

Look for, in this priority order:

1. **Merge convergence.** Is the LWW rule actually commutative/associative/
   idempotent as claimed? Both sides compare (updated_at, rev,
   sha256(canonical_node_bytes)) and pick the greater — can two members ever
   converge to DIFFERENT winners? Tombstones: archived is just an LWW field
   edit; can an archive be resurrected by a concurrent stale edit with a
   greater tuple, and is that acceptable? Edges add-only set-union with
   dangling-edge tolerance — any divergence trap?

2. **LWW + clock skew.** These are resurrected 10-year-old boxes, often
   without NTP. A member with a clock years in the FUTURE writes nodes whose
   updated_at always wins every subsequent LWW conflict (its edits are
   unbeatable until real time catches up). The spec accepts "LWW may drop a
   concurrent edit" — but is the far-future-clock poisoning scenario
   acceptable for v1, does the rev counter help at all, and is there a cheap
   mitigation (e.g. clamp updated_at to receiver's now on WRITE endpoints)
   that doesn't reintroduce vector clocks?

3. **Digest correctness + heartbeat cost.** Digest = sha256 over sorted
   "n|id|rev|RFC3339Nano(updated_at)" lines + sorted "e|from|to|relation"
   lines, LF-joined, archived included. Is this canonicalization sound
   (RFC3339Nano stability, separator injection via crafted ids?), does it
   change exactly when it must (every LWW-visible edit), and does it stay
   equal across peers whose graphs are equal? The digest is CACHED and the
   engine reads it via a closure — the heartbeat path must never re-hash
   memory.json every 2 s. Three digest passengers now ride every heartbeat
   (revocations + roster + memory): payload/size/CPU concerns on 1-2 GB
   boxes at 2 s cadence?

4. **Gossip sync behavior.** Full-graph fetch on mismatch (no deltas in v1,
   OQ-8). Write cap 2 MiB but fetch guard 4 MiB so a transiently-over-cap
   merge union still syncs — is the 2x headroom argument sound, or is there
   still a wedge scenario where digests mismatch forever while fetches get
   refused? Edit storms: per-peer in-flight dedup + persist-only-on-change —
   sufficient against fetch storms when many members edit concurrently?
   Merge is PERMISSIVE about per-field caps (caps gate only the write
   endpoints) so peers never drop nodes and freeze digests — correct call?

5. **Scribe selection edge cases.** (a) NO scribe_capable node — spec says
   current_scribe="" and that's fine; agree? (b) 1-node Clan: scribe ==
   orchestrator == self, distillation debounced/lowest-priority; agree?
   (c) The Orchestrator's selection is AUTHORITATIVE via the heartbeat
   `scribe` field (peers adopt, never derive) — does this avoid split-brain
   scribes, and what happens during orchestrator failover (scribe field gap
   until the new leader's first heartbeat)? (d) Re-selection on miss with NO
   lease machinery — any flapping risk when the weakest node is also flaky,
   and does it matter (memory loss is non-fatal, graph fully replicated)?

6. **Scribe distillation safety.** Small models emit garbage JSON; the
   parser extracts the first [...] block, validates entries, drops the rest.
   Everything lands status:"proposed" + source:"scribe"; ONLY a human promote
   makes it active. Rate caps: smallest model, 120 s debounce, <=5 proposals
   per pass, skip-if-busy. Can a runaway scribe still flood the graph to the
   2,000-node cap with proposals, and should proposed nodes have a separate
   budget? Is watching chat sessions + own audit + open research findings
   the right v1 input set?

7. **Blueprint integrity + privacy.** Checksum = sha256 over the document
   with checksum field set to "" (canonical compact JSON, fixed field
   order); signature reserved-empty (OQ-10). source_clan is sha256(clan_id),
   never raw. --strip-authors maps sorted distinct member ids to
   member-1..N. Import-at-create flips provenance.source to "import"
   PRESERVING updated_at/rev so re-import merges idempotently. Holes?
   (e.g. does the checksum-with-empty-field trick have JSON canonicalization
   pitfalls in Go? does strip-authors leak via the SORTED order? does the
   import flow let a tampered file in any path skip checksum verify?)
   Distillates can carry verbatim chat content — export warns + workspace
   confirm modal + 0600 store; sufficient for v1?

8. **Write authority + abuse.** Peer-equal writes (any active member),
   provenance.author_member_id ALWAYS daemon-set from the HMAC-authenticated
   origin, client value ignored. v1 honesty: shared clan_key means HMAC
   proves "some member", not "which member" (§7.1 note applies). Caps return
   409 at the write endpoints. Combined with permissive merge — what's the
   worst a single malicious/buggy member can do, and is it bounded enough
   for v1's trusted-Clan model?

9. **Endpoint surface + workspace.** GET /clan/memory, POST
   /clan/memory/{node,edge}, POST /clan/memory/import — all HMAC. Import is
   listed caller="local CLI": should remote members be able to POST import,
   or is local-only right? The workspace mutates via shelling minti-cland
   (loopback) and its /api/memory/* surface must be enumerated in the
   PIN/bearer gate when that lands — is the spec note strong enough?

10. **What the spec got right and shouldn't change.** Be willing to say so.
    Padding critique with non-issues wastes the author's time.

If a finding is out-of-scope for v1 (delta sync, vector clocks, per-member
keys, edge tombstones), check whether §12's OQ table already tracks it; if
yes, say "tracked as OQ-N" and move on.

End your review with one short verdict line:
  VERDICT: ship Phase 0 as-is | ship after addressing items N..M | block on item K

Below are FOUR documents.
"""

    parts = [
        instructions,
        "==================================================================\n"
        "DOCUMENT 1: Existing context — spec §3.5 (revocation gossip),\n"
        "§5 (election + heartbeat), §9.1 (audit log — the thing §13 inverts)\n"
        "==================================================================\n\n",
        spec_revocation,
        "\n\n",
        spec_election,
        "\n\n",
        spec_audit,
        "\n\n==================================================================\n"
        "DOCUMENT 2: NEW spec §13 (Clan Memory) — the section being reviewed\n"
        "==================================================================\n\n",
        spec_memory,
        "\n\n==================================================================\n"
        "DOCUMENT 3: Existing cland code shapes the Memory plan reuses\n"
        "==================================================================\n\n",
        code_summary,
        "\n\n==================================================================\n"
        "DOCUMENT 4: Memory implementation plan (M0..M6)\n"
        "==================================================================\n\n",
        plan_memory,
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

    with open(os.path.join(OUT_DIR, "memory-phase0-input.txt"), "w", encoding="utf-8") as f:
        f.write(prompt)

    summary = []
    for model, extra in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"memory-phase0-{safe}.md")
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
            o.write(f"# {model} - Clan Memory Phase 0 protocol review\n\n")
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
