# Clan Memory Phase 0 — peer-review triage (2026-06-11)

Run via `scripts/memory-peer-review.py` against the new Clan Memory spec
(`docs/clan-protocol.md` §13 in v0.4) + the approved Memory implementation
plan (`~/.claude/plans/we-can-still-do-transient-glade.md`).

Three local reviewers (all on the daily-driver Windows host's Ollama
RTX-5090; 58 KB prompt ≈ 14.5K tokens each):
- `qwen3.6:latest` (think:false; 33 s; 12.1 KB clean)
- `deepseek-r1:32b` (default; 54 s; 5.2 KB clean)
- `gemma4:31b` (default; 87 s; 5.2 KB clean)

## Verdicts as returned

| Reviewer | Verdict |
|---|---|
| qwen3.6 | ship after addressing items 1..5 (canonical bytes, clock clamp, cap overshoot, proposed-count, blueprint struct order) |
| deepseek-r1 | ship after addressing items 2 (clock skew), 3 (digest recomputation), 5 (scribe edge testing) |
| gemma4 | ship after addressing items 6 (Scribe budget) and 9 (import replace restriction) |

## Folded into spec v0.4

### F1 — Clock-skew honesty + origin-monotone timestamps (all three)
All three reviewers attacked the far-future-clock scenario: a member
whose clock is years ahead writes nodes whose `updated_at` out-ranks
every honest edit until wall-clocks catch up. Two of the three proposed
fixes were themselves wrong, and the disagreement produced the right
answer:

- qwen3.6 proposed clamping `updated_at` at the receiver
  (`min(provided, now+60s)`).
- gemma4 explicitly demolished that: receiver-side clamping **breaks
  convergence** — different receivers stamp different values for the
  same node, the graphs diverge, digests never match again. gemma4 also
  called out the "**rev fallacy**": `rev` and the hash term break ties
  only at *identical* timestamps and provide **zero** skew protection
  (the plan's risk table implied otherwise).
- deepseek corroborated the severity ("critical for v1 on resurrected
  boxes without NTP") without a workable fix.

**Spec change (§13.4)**: a third path neither reviewer quite reached but
gemma4's constraint forces: **origin-monotone stamping (HLC-lite)**. The
*origin* daemon (the only place a timestamp is ever minted — `updated_at`
is daemon-set, never client-supplied) stamps
`updated_at = max(now, max_updated_at_in_local_graph + 1ns)`. Stamped
once, gossiped verbatim → convergence untouched. Effect: poisoned
future timestamps can win temporarily, but any member's *next* edit
stamps past them — nobody is ever locked out of editing; timestamps
drift Lamport-style ahead of wall-clock until reality catches up
(visible in the UI). Three lines of code, no vector clocks (P2). Also
corrected the merge prose: convergence was never at risk from skew
(identical tuples compare identically everywhere — gemma4 confirmed);
*fairness* was. §13.4 now says exactly that, and the rev-fallacy is
spelled out.

### F2 — Scribe pending-proposal budget (gemma4 + qwen3.6 + deepseek consensus)
gemma4 made it concrete: 5 proposals / 120 s = a runaway Scribe (or just
a chatty Clan) grinds the **2,000-node global cap** full of
`status:"proposed"` noise and locks human researchers out of the write
budget. qwen3.6 and deepseek both independently asked for a separate
proposed-node budget.

**Spec change (§13.1 + §13.9)**: proposed nodes explicitly count toward
the global cap; the Scribe refuses to mint *new* proposals while more
than **200** of its own un-reviewed proposals exist (updating existing
ones — e.g. the deterministic per-session summaries — stays allowed).
Number tunable; the floor is what matters. (gemma4 suggested 500; with
a 2,000-node cap, 200 = 10% is the leaner pick.)

### F3 — Canonical-bytes discipline made normative (qwen3.6 + deepseek)
qwen3.6 (twice — items 1 and 5) and deepseek (item 7) flagged that
"compact JSON in declaration order" is fragile in Go: `json.Marshal`
emits struct-declaration order (NOT alphabetical — qwen's own text
wobbled on this but the conclusion stands), so a careless field
insertion silently changes every node hash and blueprint checksum.

**Spec change (§13.4 + §13.10)**: normative paragraph — Go structs MUST
declare fields in the §13.1 wire order with explicit `json:` tags, and
implementations MUST ship a conformance test asserting the canonical
encoding of a fixed node is byte-stable. The blueprint checksum section
cross-references the same rule.

### F4 — `mode:"replace"` import restricted to loopback (gemma4, high-severity)
gemma4 item 9: `POST /clan/memory/import` is HMAC-authenticated, and v1
HMAC proves "some member", not "which member" (§7.1). A destructive
remote primitive (`replace`) behind a shared key is an insider-abuse
surface for free.

**Spec change (§13.6 + §13.10)**: the daemon rejects `mode:"replace"`
(403) unless the request originates from the local CLI on loopback;
remote members get `merge` only. Bonus honesty surfaced while folding:
v1 `--replace` semantics under gossip are now documented — replacing on
one member doesn't make the Clan forget (the next digest mismatch merges
peers' graphs back); Clan-wide forgetting needs every member to replace
or a fresh Clan from a Blueprint. True coordinated compaction is OQ-9.

### F5 — Over-cap wedge clarification (qwen3.6)
qwen3.6 item 3 chased the real corner: merge is cap-exempt, so a
partition-heal union can land past the 2 MiB write cap; archiving keeps
title/body, so v1 has no shrink lever; the graph is then read-only
forever. gemma4 independently endorsed the 2 MiB-write / 4 MiB-fetch
headroom logic as the correct anti-wedge design, so the caps stand —
but the stuck state needed honest documentation.

**Spec change (§13.1)**: "Over-cap honesty" — past a cap the graph is
read-only (writes 409) until it shrinks, v1 has no compaction (OQ-9),
practical recovery = export valuable sessions as a Blueprint and found
(or locally `--replace`) from it. (qwen's alternative — raise the write
cap to 4 MiB — rejected: it just moves the cliff and doubles the worst
case for 1–2 GB boxes.)

### F6 — Heartbeat passenger cost quantified (deepseek)
deepseek worried three digest passengers stress 1–2 GB boxes at 2 s
cadence. gemma4 did the arithmetic: ~192 bytes total (three sha256 hex
strings) — negligible. **Spec change (§13.5)**: the number is now in
the spec so the concern stays closed.

## Overruled with reason

### O1 — deepseek item 3: "digest recomputed only on change may miss node order swaps → false positives"
Backwards. The digest sorts node lines and edge lines before hashing
precisely so that ordering is irrelevant: two graphs equal *as sets*
MUST digest equal — that's the design, not a flaw. A "node order swap"
is not a graph change. No spec change. (deepseek's same item also asked
for cache-invalidation care in `Merge`; that's an implementation note
carried to M1: recompute the cached digest under the service mutex
after a merge mutates, before releasing.)

### O2 — deepseek item 1: "LWW not idempotent in all cases / clock skew can converge different winners"
The comparison tuple `(updated_at, rev, sha256(canonical))` is data
carried *with the node*, identical on every receiver — both sides of
any merge compare the same tuples and pick the same winner regardless
of local clocks. gemma4 stated it flat: "Convergence is guaranteed."
Skew affects fairness (handled by F1), never agreement. merge(A,A)=A
holds trivially. No spec change beyond the F1 prose tightening.

### O3 — qwen3.6 item 9: "allow remote HMAC import"
Rejected — the opposite was folded (F4). Merge-gossip already delivers
the only legitimate remote-import outcome (a member imports locally and
the union propagates), with strictly less attack surface. deepseek
agreed local-only is correct.

### O4 — deepseek item 7: "sorted strip-authors mapping could leak via order"
The sort is over random UUIDv4s, so the order carries no semantic
information whatsoever; mapping by first-appearance (the alternative)
WOULD leak structure (who wrote the first node). gemma4's adjacent
observation — pseudonymization leaks relative *cardinality* of
contributors (member-1 wrote N nodes) — is true and accepted for v1:
that's inherent to any per-author pseudonym scheme, and collapsing all
authors to one value would destroy the provenance the import exists to
preserve.

## Non-fixes — items reviewers agreed were fine

- LWW tuple total ordering → convergence (gemma4: "guaranteed"; qwen: "correctly ordered").
- Tombstones as ordinary LWW edits; resurrection-by-newer-edit accepted as v1 behavior (all three; now documented in §13.4).
- Edge set-union with dangling-edge tolerance (gemma4: "cannot diverge").
- Permissive merge / strict write split (gemma4: "the only way to prevent sync-wedging"; qwen concurred).
- 2 MiB write cap / 4 MiB fetch guard headroom (gemma4 explicitly endorsed; F5 documents the residual corner).
- Digest construction + separator-injection safety (gemma4: UUIDs/int/RFC3339 can't carry `|` or LF).
- Cached digest via injected closure on the heartbeat path (all three: essential, correctly specified).
- All four scribe edge cases as specced: no-capable → `""`, 1-node Clan self-scribe, orchestrator-authoritative selection avoids split-brain, leaseless flapping is non-fatal (gemma4: "No issues here"; qwen + deepseek concur, deepseek asks for thorough tests — M3's test list covers exactly these).
- Distillation safety posture: tolerant parser + human promote gate + rate caps (all three).
- Blueprint checksum empty-field trick, strip-authors stability, import verify→flip→seed idempotency (gemma4: "sound"; qwen: "standard and effective").
- Daemon-set provenance + §7.1 shared-key honesty restated for memory (all three).
- Workspace `/api/memory/*` PIN/bearer enumeration note (qwen: "critical and correctly flagged").

## Net protocol delta

`docs/clan-protocol.md` v0.3 → v0.4:
- +1 new top-level section (§13 Clan Memory, ~200 lines): purpose +
  audit-log inversion, data model + caps, storage, deterministic ids,
  CRDT-lite merge (LWW + origin-monotone stamps + tombstones + edge
  union), content-versioned digest + heartbeat gossip, write endpoints
  + authority, research sessions + system auto-events, Scribe role
  (inverse selection) + distillation duty, Clan Blueprint, privacy &
  v1 honesty.
- §5.3 gains the heartbeat-passengers table (retroactively documents
  H-2 `revocations_digest` + H-3 `roster_digest`; adds `memory_digest`
  + `scribe`).
- §10 gains 4 memory endpoint rows + the previously-missing H-2
  `GET /clan/revocations` row.
- §12 gains OQ-8 (delta sync), OQ-9 (edge tombstones / hard-delete /
  compaction), OQ-10 (blueprint signing).

## Gate for M1

M1 (Go implementation) can proceed: all three reviewers verdicted "ship
after fixing" (none blocked outright); every consensus and high-severity
finding is folded; each overrule is documented above with the reasoning
and, where applicable, the dissenting reviewer's own counter-argument.

Carried into M1 as implementation notes (not spec changes):
- Digest cache recompute under the service mutex on merge (deepseek O1 tail).
- The §13.4 canonical-bytes conformance test is a REQUIRED M1 test.
- The §13.6 origin-monotone stamp gets a dedicated unit test (poisoned
  future timestamp → next local edit still wins).
- M3's test list already covers the scribe edge cases deepseek wanted
  exercised.
