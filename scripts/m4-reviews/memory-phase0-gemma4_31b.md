# gemma4:31b - Clan Memory Phase 0 protocol review

- wall_time_s: 87.2
- prompt_tokens: 15547
- eval_tokens: 3188
- raw_chars: 5185
- clean_chars: 5185
- extra_body: {}

---

This review focuses on technical convergence and failure modes. I have ignored "politeness" to prioritize the stability of the gossip layer.

### 1. Merge Convergence (§13.4)
The LWW rule `(updated_at, rev, sha256(canonical_node_bytes))` is a total ordering. It is commutative, associative, and idempotent. **Convergence is guaranteed.**

*   **Tombstone Resurrection:** Because archiving is an ordinary field edit, a stale edit with a greater timestamp *will* resurrect an archived node. In a research-notes context, this is acceptable (data preservation > strict deletion), but the author should be aware that "archived" is not a terminal state.
*   **Edges:** Set-union on `(from, to, relation)` is sound and cannot diverge.

### 2. LWW + Clock Skew (§13.4)
The spec acknowledges clock skew but underestimates the "poisoning" effect.
*   **The `rev` Fallacy:** The spec mentions `rev` as part of the tiebreak. However, because `updated_at` is the primary key in the tuple, **`rev` provides zero protection against clock skew.** A node with a clock set to 2035 will win every conflict regardless of its `rev` count until the rest of the Clan's wall-clocks catch up.
*   **Mitigation:** Clamping `updated_at` at the receiver is impossible because it breaks convergence (different receivers would assign different timestamps to the same edit, creating divergent graphs). 
*   **Verdict:** For v1 "trusted Clan" model on old hardware, this poisoning risk is acceptable, but the claim that `rev` helps with clock skew is technically incorrect. It only helps with *identical* timestamps.

### 3. Digest Correctness & Heartbeat Cost (§13.5)
*   **Canonicalization:** The construction `"n|id|rev|RFC3339Nano(updated_at)"` is sound. Since `id` (UUID), `rev` (int), and `updated_at` (fixed RFC format) contain no pipe or newline characters, separator injection is impossible.
*   **CPU/Memory:** The requirement that the engine reads a **cached** digest via closure is critical and correctly specified. This prevents the $O(N \log N)$ cost of sorting and hashing the graph every 2 seconds.
*   **Payload:** Three SHA256 hex strings in a heartbeat are negligible (~192 bytes). No concern here.

### 4. Gossip Sync Behavior (§13.5)
*   **The Wedge Scenario:** The "Fetch Guard (4 MiB) vs Write Cap (2 MiB)" logic is sound. It provides the necessary headroom for merge unions to sync even if they transiently exceed the write cap, preventing a permanent digest mismatch loop.
*   **Permissive Merge:** Correct call. Dropping oversized nodes during a merge would freeze digests in a state of permanent divergence.

### 5. Scribe Selection (§13.8)
*   **Edge Cases:** (a) No capable node $\to$ `""` is fine. (b) 1-node Clan $\to$ self-scribe is fine. (c) Authoritative selection via Orchestrator avoids split-brain. (d) Flapping on a flaky weak node is non-fatal as memory is fully replicated. **No issues here.**

### 6. Scribe Distillation Safety (§13.9)
*   **Budget Exhaustion:** There is a concrete risk here. The Scribe can propose up to 5 nodes every 120s. A runaway scribe (or a very chatty Clan) could fill the **2,000-node total cap** with `status:"proposed"` garbage, effectively locking out human researchers from adding new findings.
*   **Recommendation:** Implement a separate budget for proposed nodes (e.g., max 500 `proposed` nodes) or allow the Scribe to overwrite its own oldest proposals if the global cap is hit.

### 7. Blueprint Integrity & Privacy (§13.10)
*   **Checksums:** The "empty field" trick is standard and works in Go provided a stable serializer (like a sorted map or fixed-struct JSON) is used.
*   **Privacy Leak:** `--strip-authors` maps IDs to `member-1..N`. This does not leak identity, but it does leak the *relative cardinality* of contributors. Acceptable for v1.
*   **Import Flow:** The flow (Verify $\to$ Flip Source $\to$ Seed) is idempotent and sound.

### 8. Write Authority & Abuse (§13.6 / §13.11)
*   **Insider Threat:** A single member can archive the entire graph or flood it with garbage. Given the "trusted Clan" model, this is bounded by the 2 MiB cap and is acceptable for v1.

### 9. Endpoint Surface (§13.6)
*   **Import Authority:** `POST /clan/memory/import` is HMAC-authenticated. This means any member can push a blueprint to any other member. While `merge` is safe, the `--replace` mode is destructive. If this endpoint is exposed to the network, one compromised member could potentially trigger a "replace" on others if they can trick the client into calling it.
*   **Recommendation:** The implementation plan (M4) should explicitly restrict `POST /clan/memory/import` with `mode: "replace"` to **local loopback only**.

### 10. What the spec got right
*   **Scribe = Weakest Node:** This is an elegant architectural choice that balances load and gives low-spec hardware a purpose.
*   **Permissive Merge / Strict Write:** The distinction between these two is the only way to prevent "sync-wedging" in a gossip network with caps.
*   **Deterministic IDs (§13.3):** Essential for avoiding duplicate system events across N members.

***

**VERDICT: ship after addressing items 6 (Scribe budget) and 9 (Import replace restriction)**

---

## Raw (with thinking trace if any)

This review focuses on technical convergence and failure modes. I have ignored "politeness" to prioritize the stability of the gossip layer.

### 1. Merge Convergence (§13.4)
The LWW rule `(updated_at, rev, sha256(canonical_node_bytes))` is a total ordering. It is commutative, associative, and idempotent. **Convergence is guaranteed.**

*   **Tombstone Resurrection:** Because archiving is an ordinary field edit, a stale edit with a greater timestamp *will* resurrect an archived node. In a research-notes context, this is acceptable (data preservation > strict deletion), but the author should be aware that "archived" is not a terminal state.
*   **Edges:** Set-union on `(from, to, relation)` is sound and cannot diverge.

### 2. LWW + Clock Skew (§13.4)
The spec acknowledges clock skew but underestimates the "poisoning" effect.
*   **The `rev` Fallacy:** The spec mentions `rev` as part of the tiebreak. However, because `updated_at` is the primary key in the tuple, **`rev` provides zero protection against clock skew.** A node with a clock set to 2035 will win every conflict regardless of its `rev` count until the rest of the Clan's wall-clocks catch up.
*   **Mitigation:** Clamping `updated_at` at the receiver is impossible because it breaks convergence (different receivers would assign different timestamps to the same edit, creating divergent graphs). 
*   **Verdict:** For v1 "trusted Clan" model on old hardware, this poisoning risk is acceptable, but the claim that `rev` helps with clock skew is technically incorrect. It only helps with *identical* timestamps.

### 3. Digest Correctness & Heartbeat Cost (§13.5)
*   **Canonicalization:** The construction `"n|id|rev|RFC3339Nano(updated_at)"` is sound. Since `id` (UUID), `rev` (int), and `updated_at` (fixed RFC format) contain no pipe or newline characters, separator injection is impossible.
*   **CPU/Memory:** The requirement that the engine reads a **cached** digest via closure is critical and correctly specified. This prevents the $O(N \log N)$ cost of sorting and hashing the graph every 2 seconds.
*   **Payload:** Three SHA256 hex strings in a heartbeat are negligible (~192 bytes). No concern here.

### 4. Gossip Sync Behavior (§13.5)
*   **The Wedge Scenario:** The "Fetch Guard (4 MiB) vs Write Cap (2 MiB)" logic is sound. It provides the necessary headroom for merge unions to sync even if they transiently exceed the write cap, preventing a permanent digest mismatch loop.
*   **Permissive Merge:** Correct call. Dropping oversized nodes during a merge would freeze digests in a state of permanent divergence.

### 5. Scribe Selection (§13.8)
*   **Edge Cases:** (a) No capable node $\to$ `""` is fine. (b) 1-node Clan $\to$ self-scribe is fine. (c) Authoritative selection via Orchestrator avoids split-brain. (d) Flapping on a flaky weak node is non-fatal as memory is fully replicated. **No issues here.**

### 6. Scribe Distillation Safety (§13.9)
*   **Budget Exhaustion:** There is a concrete risk here. The Scribe can propose up to 5 nodes every 120s. A runaway scribe (or a very chatty Clan) could fill the **2,000-node total cap** with `status:"proposed"` garbage, effectively locking out human researchers from adding new findings.
*   **Recommendation:** Implement a separate budget for proposed nodes (e.g., max 500 `proposed` nodes) or allow the Scribe to overwrite its own oldest proposals if the global cap is hit.

### 7. Blueprint Integrity & Privacy (§13.10)
*   **Checksums:** The "empty field" trick is standard and works in Go provided a stable serializer (like a sorted map or fixed-struct JSON) is used.
*   **Privacy Leak:** `--strip-authors` maps IDs to `member-1..N`. This does not leak identity, but it does leak the *relative cardinality* of contributors. Acceptable for v1.
*   **Import Flow:** The flow (Verify $\to$ Flip Source $\to$ Seed) is idempotent and sound.

### 8. Write Authority & Abuse (§13.6 / §13.11)
*   **Insider Threat:** A single member can archive the entire graph or flood it with garbage. Given the "trusted Clan" model, this is bounded by the 2 MiB cap and is acceptable for v1.

### 9. Endpoint Surface (§13.6)
*   **Import Authority:** `POST /clan/memory/import` is HMAC-authenticated. This means any member can push a blueprint to any other member. While `merge` is safe, the `--replace` mode is destructive. If this endpoint is exposed to the network, one compromised member could potentially trigger a "replace" on others if they can trick the client into calling it.
*   **Recommendation:** The implementation plan (M4) should explicitly restrict `POST /clan/memory/import` with `mode: "replace"` to **local loopback only**.

### 10. What the spec got right
*   **Scribe = Weakest Node:** This is an elegant architectural choice that balances load and gives low-spec hardware a purpose.
*   **Permissive Merge / Strict Write:** The distinction between these two is the only way to prevent "sync-wedging" in a gossip network with caps.
*   **Deterministic IDs (§13.3):** Essential for avoiding duplicate system events across N members.

***

**VERDICT: ship after addressing items 6 (Scribe budget) and 9 (Import replace restriction)**