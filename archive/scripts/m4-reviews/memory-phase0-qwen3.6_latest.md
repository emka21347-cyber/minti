# qwen3.6:latest - Clan Memory Phase 0 protocol review

- wall_time_s: 33.1
- prompt_tokens: 15145
- eval_tokens: 3044
- raw_chars: 12056
- clean_chars: 12056
- extra_body: {'think': False}

---

Here is the concrete review of the MINTI "Clan Memory" spec and implementation plan.

### 1. Merge Convergence (CRDT-lite)
**Verdict: Sound, with one critical definition gap.**

*   **LWW Tuple:** The tuple `(updated_at, rev, sha256(canonical_node_bytes))` is correctly ordered for commutativity/associativity/idempotency. The hash term breaks ties deterministically *provided* `canonical_node_bytes` is strictly defined.
    *   **Gap:** §13.4 says "canonical_node_bytes = the node encoded as compact JSON with fields in the §13.1 declaration order." Go's `json.Marshal` does **not** guarantee field order if struct tags are missing or if using `mapstructure`. You must enforce `json:"id"` (or similar) on *every* field in `Node` and `Edge` structs in `graph.go` to ensure the byte representation is stable across implementations/versions. Without explicit tags, `json.Marshal` sorts alphabetically by default in Go, but if you add fields later without tags, order might shift or become unstable if using reflection-based marshaling elsewhere. **Action:** Explicitly mandate `json:"<field>"` tags on all struct fields in the spec and code review.
*   **Tombstones:** "Archived is just an LWW field edit." This is correct. If Node A is archived at T1, and a stale node B (from before T1) arrives with `updated_at < T1`, it loses. If a *new* edit to Node A arrives at T2 > T1, it wins. The tombstone persists until overwritten by an even newer edit or explicitly un-archived. This is acceptable for v1; "resurrection" of an archived node requires a new edit with a higher timestamp, which is the desired behavior (you can't accidentally resurrect old data).
*   **Edges:** Set-union is CRDT-safe. "Local metadata wins" on duplicate edges needs clarification. If Peer A adds edge `(X,Y,rel)` at T1 and Peer B adds `(X,Y,rel)` at T2, who's `created_at`/`created_by` survives? The spec says "local metadata wins," which implies the *receiver* keeps its own version if it already has it, or the one with the higher LWW tuple? Since edges are add-only and deduped by `(from,to,relation)`, there is no conflict resolution needed for the edge itself—only for the metadata. **Clarification Needed:** If an edge exists locally, does a remote update to the *same* edge (e.g., changing `created_by` via some weird mechanism?) overwrite it? The spec says "add-only," so duplicates are no-ops. This is safe.

### 2. LWW + Clock Skew
**Verdict: Acceptable for v1, but mitigation is cheap.**

*   **Far-Future Clock:** A node with a clock 10 years in the future will win every LWW conflict until real time catches up. This is a known LWW flaw. The spec acknowledges this ("LWW may drop a concurrent edit").
*   **Mitigation:** The spec mentions "clamp updated_at to receiver's now on WRITE endpoints" as a potential mitigation but dismisses it. **I disagree.** For a distributed graph where *every* member holds the full state, allowing a single node to poison the timeline with future timestamps is dangerous. It can effectively "freeze" edits from other nodes for years.
    *   **Recommendation:** Add a simple clamp: `updated_at = min(provided_updated_at, now + skew_tolerance)`. A tolerance of 60 seconds is sufficient to handle NTP drift without reintroducing vector clocks. This prevents the "poisoning" scenario while allowing legitimate future timestamps (e.g., from scheduled tasks) within reason.
*   **Rev Counter:** The `rev` counter helps only if it increments monotonically *after* `updated_at`. If two nodes edit simultaneously with the same `updated_at`, the rev counter breaks ties. This is good, but only if `rev` is also monotonic and never resets. Ensure `rev` is persisted and incremented on *every* write, including sync merges (if a merge results in a change to a node's metadata, does `rev` increment? No, `rev` should only increment on *local* writes or when the node content changes). **Clarification:** Does `Merge` update `rev`? If so, it must be `max(local.rev, remote.rev) + 1` if the merge results in a change, to prevent stale merges from winning later.

### 3. Digest Correctness + Heartbeat Cost
**Verdict: Sound, but canonicalization is fragile.**

*   **Canonicalization:** The digest uses `"n|id|rev|RFC3339Nano(updated_at)"`. RFC3339Nano includes fractional seconds. If two edits happen in the same nanosecond (possible on fast local machines), the digest will be identical, which is correct for idempotency but means no convergence trigger. This is acceptable.
*   **Separator Injection:** `id` fields are UUIDs (safe). `from`/`to` are node IDs (UUIDs). `relation` is a fixed enum. No injection risk.
*   **Archived Nodes:** Including archived nodes in the digest is **critical** and correct. If you exclude them, an archive event won't change the digest, and peers won't fetch the tombstone, leading to permanent divergence.
*   **Cost:** Caching the digest is essential. The spec correctly mandates a `MemoryDigest func() string` injected into the heartbeat engine. This avoids re-hashing 2 MiB of JSON every 2 seconds. **Action:** Ensure the cache is invalidated *before* persisting in `Merge` if the merge resulted in changes, and *after* persisting if it didn't (to avoid unnecessary writes).

### 4. Gossip Sync Behavior
**Verdict: Sound, but fetch guard needs a hard cap.**

*   **Fetch Guard:** 4 MiB is 2x the write cap (2 MiB). This handles transient over-cap during merge. However, if a malicious node sends a 100 MiB graph in response to `GET /clan/memory`, the receiver will download it before checking the guard? No, the guard should be on the *response size* check before processing. The spec says "Responses larger than that are dropped." This is correct.
*   **Wedge Scenario:** If Peer A has a digest mismatch because Peer B has a 3 MiB graph (over cap), and Peer B refuses to send it because it's over cap, they wedge. But the write cap prevents any node from *writing* >2 MiB. The only way to get >2 MiB is via merge. If two nodes each have 1.5 MiB, their union might be 3 MiB. The 4 MiB guard allows this sync. Once synced, both are at 3 MiB? No, the write cap prevents further writes. So they are stuck at 3 MiB? **Clarification:** Does the write cap apply to the *total graph size* or *per-write*? §13.1 says "2 MiB serialized graph." This implies a hard cap on the total graph size. If so, the union of two valid graphs could exceed 2 MiB, causing a merge failure? **Critical Gap:** The spec must define what happens when a merge results in a graph >2 MiB. Does it reject the remote nodes? That would cause permanent divergence. **Recommendation:** Allow the merge to succeed up to the fetch guard (4 MiB), but then enforce the write cap by rejecting *new* writes until the graph shrinks below 2 MiB (e.g., via archiving). Or, relax the write cap to 4 MiB for v1? Given "lean for 1-2 GB boxes," 2 MiB is tight. **Suggestion:** Increase write cap to 4 MiB to match the fetch guard, or explicitly handle merge-over-cap by dropping the *oldest* nodes (not LWW) to fit? No, that breaks LWW. **Best Fix:** Allow the graph to grow up to 4 MiB via sync, but reject new writes if total size > 4 MiB. This prevents unbounded growth while allowing legitimate merge expansion.

### 5. Scribe Selection Edge Cases
**Verdict: Sound.**

*   **(a) No scribe_capable:** `current_scribe=""` is correct. Distillation stops, which is fine.
*   **(b) 1-node Clan:** Scribe == Orchestrator == self. The spec says "distillation is debounced + lowest-priority." This is fine; it won't contend with itself.
*   **(c) Orchestrator Failover:** The new Orchestrator will emit a heartbeat with the `scribe` field. Peers adopt it. If there's a gap (new leader hasn't sent first heartbeat yet), peers retain the old scribe. This is acceptable; memory loss is non-fatal.
*   **(d) Flapping:** No lease means if the weakest node is flaky, it might be selected, then drop out, then re-selected. This causes distillation pauses. The spec says "memory loss is non-fatal." This is acceptable for v1.

### 6. Scribe Distillation Safety
**Verdict: Sound.**

*   **Garbage JSON:** Tolerant parser + human review gate is the correct approach.
*   **Rate Caps:** 120s debounce, <=5 proposals per pass is good.
*   **Proposed Nodes Budget:** Should proposed nodes have a separate budget? The spec says "2,000 nodes" cap. If proposed nodes count towards this, a runaway scribe could fill the graph with proposed nodes, blocking active contributions. **Recommendation:** Explicitly state that `proposed` nodes count towards the 2,000 node cap. This prevents a scribe from spamming proposals to block the graph.

### 7. Blueprint Integrity + Privacy
**Verdict: Sound, but checksum verification is critical.**

*   **Checksum:** sha256 over compact JSON with `checksum_sha256` set to `""` is a standard trick. Ensure the Go code uses `json.MarshalIndent` with no indent (compact) and sorts keys? No, the spec says "fixed field order." You must use struct tags to enforce order in Go's `json.Marshal`. If you rely on alphabetical sorting, it works, but if you add fields later, you must ensure they are added in the correct position in the struct or via tags. **Action:** Verify that `json.Marshal` in Go sorts keys alphabetically by default *only* if no tags are present? No, Go's `json.Marshal` does **not** sort keys; it uses the order of fields in the struct. Therefore, you **must** define the struct fields in the exact order specified in §13.10, or use a custom marshaler. This is a common pitfall.
*   **Strip-Authorers:** Sorting distinct member IDs and mapping to `member-1..N` is stable within the file. It doesn't leak identity across files, which is correct for privacy.
*   **Import Flow:** Checksum verification on import is mandatory. The spec says "Verified on every import." This is correct.

### 8. Write Authority + Abuse
**Verdict: Sound.**

*   **Malicious Member:** Any active member can write anything. The worst case is filling the graph with garbage up to the cap (2 MiB). This is bounded and acceptable for a trusted Clan model.
*   **HMAC Provenance:** `author_member_id` is set by the daemon from HMAC auth. Since the key is shared, it proves "some member" sent it. This is honest v1 posture.

### 9. Endpoint Surface + Workspace
**Verdict: Sound.**

*   **Import Endpoint:** `POST /clan/memory/import` should be available to remote members? The spec says "caller='local CLI'." If remote members can import, they could inject malicious blueprints. However, the checksum verification prevents tampering. The risk is injecting *valid* but unwanted data. Since merge is default, this is safe. **Recommendation:** Allow remote import if HMAC-authenticated, as it's a standard sync operation.
*   **Workspace Gate:** The spec notes that `/api/memory/*` must be enumerated in the PIN/bearer gate. This is critical and correctly flagged.

### 10. What the Spec Got Right
*   **Scribe on Weakest Node:** Brilliant symmetry with Orchestrator. Gives purpose to weak nodes without burdening strong ones.
*   **Digest Caching:** Essential for performance.
*   **Tombstones as LWW Edits:** Simple and correct.
*   **Blueprint Checksum Trick:** Standard and effective.
*   **Human Review Gate:** Critical for AI-generated content quality.

---

**Summary of Required Changes:**
1.  **§13.4 Merge Semantics:** Clarify that `canonical_node_bytes` requires explicit `json:"<field>"` tags on all struct fields to ensure stable serialization order in Go.
2.  **§13.5 Digest & Gossip:** Add a clock skew clamp: `updated_at = min(provided_updated_at, now + 60s)` on write endpoints to prevent far-future poisoning.
3.  **§13.1 Caps:** Clarify behavior when merge results in graph size >2 MiB but <4 MiB. Allow it, but reject new writes until size <2 MiB. Or increase cap to 4 MiB for v1.
4.  **§13.9 Scribe Distillation:** Explicitly state that `proposed` nodes count towards the 2,000 node cap.
5.  **§13.10 Blueprint:** Verify Go struct field order matches JSON spec exactly, or use a custom marshaler.

VERDICT: ship after addressing items 1..5

---

## Raw (with thinking trace if any)

Here is the concrete review of the MINTI "Clan Memory" spec and implementation plan.

### 1. Merge Convergence (CRDT-lite)
**Verdict: Sound, with one critical definition gap.**

*   **LWW Tuple:** The tuple `(updated_at, rev, sha256(canonical_node_bytes))` is correctly ordered for commutativity/associativity/idempotency. The hash term breaks ties deterministically *provided* `canonical_node_bytes` is strictly defined.
    *   **Gap:** §13.4 says "canonical_node_bytes = the node encoded as compact JSON with fields in the §13.1 declaration order." Go's `json.Marshal` does **not** guarantee field order if struct tags are missing or if using `mapstructure`. You must enforce `json:"id"` (or similar) on *every* field in `Node` and `Edge` structs in `graph.go` to ensure the byte representation is stable across implementations/versions. Without explicit tags, `json.Marshal` sorts alphabetically by default in Go, but if you add fields later without tags, order might shift or become unstable if using reflection-based marshaling elsewhere. **Action:** Explicitly mandate `json:"<field>"` tags on all struct fields in the spec and code review.
*   **Tombstones:** "Archived is just an LWW field edit." This is correct. If Node A is archived at T1, and a stale node B (from before T1) arrives with `updated_at < T1`, it loses. If a *new* edit to Node A arrives at T2 > T1, it wins. The tombstone persists until overwritten by an even newer edit or explicitly un-archived. This is acceptable for v1; "resurrection" of an archived node requires a new edit with a higher timestamp, which is the desired behavior (you can't accidentally resurrect old data).
*   **Edges:** Set-union is CRDT-safe. "Local metadata wins" on duplicate edges needs clarification. If Peer A adds edge `(X,Y,rel)` at T1 and Peer B adds `(X,Y,rel)` at T2, who's `created_at`/`created_by` survives? The spec says "local metadata wins," which implies the *receiver* keeps its own version if it already has it, or the one with the higher LWW tuple? Since edges are add-only and deduped by `(from,to,relation)`, there is no conflict resolution needed for the edge itself—only for the metadata. **Clarification Needed:** If an edge exists locally, does a remote update to the *same* edge (e.g., changing `created_by` via some weird mechanism?) overwrite it? The spec says "add-only," so duplicates are no-ops. This is safe.

### 2. LWW + Clock Skew
**Verdict: Acceptable for v1, but mitigation is cheap.**

*   **Far-Future Clock:** A node with a clock 10 years in the future will win every LWW conflict until real time catches up. This is a known LWW flaw. The spec acknowledges this ("LWW may drop a concurrent edit").
*   **Mitigation:** The spec mentions "clamp updated_at to receiver's now on WRITE endpoints" as a potential mitigation but dismisses it. **I disagree.** For a distributed graph where *every* member holds the full state, allowing a single node to poison the timeline with future timestamps is dangerous. It can effectively "freeze" edits from other nodes for years.
    *   **Recommendation:** Add a simple clamp: `updated_at = min(provided_updated_at, now + skew_tolerance)`. A tolerance of 60 seconds is sufficient to handle NTP drift without reintroducing vector clocks. This prevents the "poisoning" scenario while allowing legitimate future timestamps (e.g., from scheduled tasks) within reason.
*   **Rev Counter:** The `rev` counter helps only if it increments monotonically *after* `updated_at`. If two nodes edit simultaneously with the same `updated_at`, the rev counter breaks ties. This is good, but only if `rev` is also monotonic and never resets. Ensure `rev` is persisted and incremented on *every* write, including sync merges (if a merge results in a change to a node's metadata, does `rev` increment? No, `rev` should only increment on *local* writes or when the node content changes). **Clarification:** Does `Merge` update `rev`? If so, it must be `max(local.rev, remote.rev) + 1` if the merge results in a change, to prevent stale merges from winning later.

### 3. Digest Correctness + Heartbeat Cost
**Verdict: Sound, but canonicalization is fragile.**

*   **Canonicalization:** The digest uses `"n|id|rev|RFC3339Nano(updated_at)"`. RFC3339Nano includes fractional seconds. If two edits happen in the same nanosecond (possible on fast local machines), the digest will be identical, which is correct for idempotency but means no convergence trigger. This is acceptable.
*   **Separator Injection:** `id` fields are UUIDs (safe). `from`/`to` are node IDs (UUIDs). `relation` is a fixed enum. No injection risk.
*   **Archived Nodes:** Including archived nodes in the digest is **critical** and correct. If you exclude them, an archive event won't change the digest, and peers won't fetch the tombstone, leading to permanent divergence.
*   **Cost:** Caching the digest is essential. The spec correctly mandates a `MemoryDigest func() string` injected into the heartbeat engine. This avoids re-hashing 2 MiB of JSON every 2 seconds. **Action:** Ensure the cache is invalidated *before* persisting in `Merge` if the merge resulted in changes, and *after* persisting if it didn't (to avoid unnecessary writes).

### 4. Gossip Sync Behavior
**Verdict: Sound, but fetch guard needs a hard cap.**

*   **Fetch Guard:** 4 MiB is 2x the write cap (2 MiB). This handles transient over-cap during merge. However, if a malicious node sends a 100 MiB graph in response to `GET /clan/memory`, the receiver will download it before checking the guard? No, the guard should be on the *response size* check before processing. The spec says "Responses larger than that are dropped." This is correct.
*   **Wedge Scenario:** If Peer A has a digest mismatch because Peer B has a 3 MiB graph (over cap), and Peer B refuses to send it because it's over cap, they wedge. But the write cap prevents any node from *writing* >2 MiB. The only way to get >2 MiB is via merge. If two nodes each have 1.5 MiB, their union might be 3 MiB. The 4 MiB guard allows this sync. Once synced, both are at 3 MiB? No, the write cap prevents further writes. So they are stuck at 3 MiB? **Clarification:** Does the write cap apply to the *total graph size* or *per-write*? §13.1 says "2 MiB serialized graph." This implies a hard cap on the total graph size. If so, the union of two valid graphs could exceed 2 MiB, causing a merge failure? **Critical Gap:** The spec must define what happens when a merge results in a graph >2 MiB. Does it reject the remote nodes? That would cause permanent divergence. **Recommendation:** Allow the merge to succeed up to the fetch guard (4 MiB), but then enforce the write cap by rejecting *new* writes until the graph shrinks below 2 MiB (e.g., via archiving). Or, relax the write cap to 4 MiB for v1? Given "lean for 1-2 GB boxes," 2 MiB is tight. **Suggestion:** Increase write cap to 4 MiB to match the fetch guard, or explicitly handle merge-over-cap by dropping the *oldest* nodes (not LWW) to fit? No, that breaks LWW. **Best Fix:** Allow the graph to grow up to 4 MiB via sync, but reject new writes if total size > 4 MiB. This prevents unbounded growth while allowing legitimate merge expansion.

### 5. Scribe Selection Edge Cases
**Verdict: Sound.**

*   **(a) No scribe_capable:** `current_scribe=""` is correct. Distillation stops, which is fine.
*   **(b) 1-node Clan:** Scribe == Orchestrator == self. The spec says "distillation is debounced + lowest-priority." This is fine; it won't contend with itself.
*   **(c) Orchestrator Failover:** The new Orchestrator will emit a heartbeat with the `scribe` field. Peers adopt it. If there's a gap (new leader hasn't sent first heartbeat yet), peers retain the old scribe. This is acceptable; memory loss is non-fatal.
*   **(d) Flapping:** No lease means if the weakest node is flaky, it might be selected, then drop out, then re-selected. This causes distillation pauses. The spec says "memory loss is non-fatal." This is acceptable for v1.

### 6. Scribe Distillation Safety
**Verdict: Sound.**

*   **Garbage JSON:** Tolerant parser + human review gate is the correct approach.
*   **Rate Caps:** 120s debounce, <=5 proposals per pass is good.
*   **Proposed Nodes Budget:** Should proposed nodes have a separate budget? The spec says "2,000 nodes" cap. If proposed nodes count towards this, a runaway scribe could fill the graph with proposed nodes, blocking active contributions. **Recommendation:** Explicitly state that `proposed` nodes count towards the 2,000 node cap. This prevents a scribe from spamming proposals to block the graph.

### 7. Blueprint Integrity + Privacy
**Verdict: Sound, but checksum verification is critical.**

*   **Checksum:** sha256 over compact JSON with `checksum_sha256` set to `""` is a standard trick. Ensure the Go code uses `json.MarshalIndent` with no indent (compact) and sorts keys? No, the spec says "fixed field order." You must use struct tags to enforce order in Go's `json.Marshal`. If you rely on alphabetical sorting, it works, but if you add fields later, you must ensure they are added in the correct position in the struct or via tags. **Action:** Verify that `json.Marshal` in Go sorts keys alphabetically by default *only* if no tags are present? No, Go's `json.Marshal` does **not** sort keys; it uses the order of fields in the struct. Therefore, you **must** define the struct fields in the exact order specified in §13.10, or use a custom marshaler. This is a common pitfall.
*   **Strip-Authorers:** Sorting distinct member IDs and mapping to `member-1..N` is stable within the file. It doesn't leak identity across files, which is correct for privacy.
*   **Import Flow:** Checksum verification on import is mandatory. The spec says "Verified on every import." This is correct.

### 8. Write Authority + Abuse
**Verdict: Sound.**

*   **Malicious Member:** Any active member can write anything. The worst case is filling the graph with garbage up to the cap (2 MiB). This is bounded and acceptable for a trusted Clan model.
*   **HMAC Provenance:** `author_member_id` is set by the daemon from HMAC auth. Since the key is shared, it proves "some member" sent it. This is honest v1 posture.

### 9. Endpoint Surface + Workspace
**Verdict: Sound.**

*   **Import Endpoint:** `POST /clan/memory/import` should be available to remote members? The spec says "caller='local CLI'." If remote members can import, they could inject malicious blueprints. However, the checksum verification prevents tampering. The risk is injecting *valid* but unwanted data. Since merge is default, this is safe. **Recommendation:** Allow remote import if HMAC-authenticated, as it's a standard sync operation.
*   **Workspace Gate:** The spec notes that `/api/memory/*` must be enumerated in the PIN/bearer gate. This is critical and correctly flagged.

### 10. What the Spec Got Right
*   **Scribe on Weakest Node:** Brilliant symmetry with Orchestrator. Gives purpose to weak nodes without burdening strong ones.
*   **Digest Caching:** Essential for performance.
*   **Tombstones as LWW Edits:** Simple and correct.
*   **Blueprint Checksum Trick:** Standard and effective.
*   **Human Review Gate:** Critical for AI-generated content quality.

---

**Summary of Required Changes:**
1.  **§13.4 Merge Semantics:** Clarify that `canonical_node_bytes` requires explicit `json:"<field>"` tags on all struct fields to ensure stable serialization order in Go.
2.  **§13.5 Digest & Gossip:** Add a clock skew clamp: `updated_at = min(provided_updated_at, now + 60s)` on write endpoints to prevent far-future poisoning.
3.  **§13.1 Caps:** Clarify behavior when merge results in graph size >2 MiB but <4 MiB. Allow it, but reject new writes until size <2 MiB. Or increase cap to 4 MiB for v1.
4.  **§13.9 Scribe Distillation:** Explicitly state that `proposed` nodes count towards the 2,000 node cap.
5.  **§13.10 Blueprint:** Verify Go struct field order matches JSON spec exactly, or use a custom marshaler.

VERDICT: ship after addressing items 1..5