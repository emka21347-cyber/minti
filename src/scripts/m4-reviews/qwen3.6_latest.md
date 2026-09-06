# qwen3.6:latest — M4 plan review

- wall_time_s: 79.2
- prompt_tokens: 12040
- eval_tokens: 7465
- raw_chars: 7624
- clean_chars: 7624

---

Here is a direct, priority-ordered review of the M4 implementation plan against the Clan Protocol Spec v0.1.

### 🔴 Security & Distributed Systems Risks
1. **Paste-key entropy mismatch (Critical)**  
   - **Spec §2.2** requires `clan_key` as `32 random bytes` (256 bits).  
   - **Plan Phase C** implements a `6-word BIP39-derived phrase`. 6 BIP39 words yield ~64 bits of entropy. This is a **4x cryptographic downgrade** that breaks the spec's threat model.  
   - **Fix:** Either use a custom 12-word wordlist, apply PBKDF2/Argon2 with high iteration counts, or explicitly note the spec's `BIP39-ish` wording allows a lower-entropy derivation with compensating controls.

2. **Nonce cache DoS vector**  
   - **Spec §2.3** requires tracking `(member_id, nonce)` for 5 minutes.  
   - **Plan Phase B** says `5-min cache` with no size limit or eviction policy. Under sustained probing, memory grows linearly per member.  
   - **Fix:** Cap cache at `N_members × 5000 entries` with LRU eviction, or use a sliding-window Bloom filter with explicit false-positive bounds.

3. **Split-brain quorum during partition**  
   - **Spec §5.4** uses `≥ ⌈N/2⌉` where `N = current active roster size`.  
   - **Plan Phase E** implements this mathematically. During a network partition, `N` is locally inconsistent. Both sides can compute `N` from stale advertisements and both reach quorum → split-brain. The spec claims leader-lease avoids this, but the math does not guarantee it without a persistent quorum witness or lease persistence.  
   - **Fix:** Compute `N` from the last *persisted* roster, not the live advertisement cache, or add a persistent quorum file that survives partitions.

4. **Key rotation crash recovery**  
   - **Spec §8.1** says rotation aborts if any ACK is missed.  
   - **Plan Phase H** says `Atomic rollback if any ACK is missed`. This ignores the case where a member ACKs, crashes before persisting the new key, and restarts with the old key. The grace window breaks, causing permanent desync.  
   - **Fix:** Implement a two-phase commit: `PROPOSE_NEW_KEY` → `ACK_NEW_KEY` → `PERSIST_NEW_KEY` → `COMMIT`. Rollback only on explicit NACK or timeout.

### 🟡 Spec-Implementation Drift
1. **`/v1/messages` endpoint**  
   - **Plan Phase F** adds `/v1/messages` to the routing layer.  
   - **Spec §10** endpoint table only lists `/v1/chat/completions`, `/api/chat`, `/v1/embeddings`.  
   - **Fix:** Remove from Phase F or flag as a spec edit.

2. **`?target=A` query param on `/mcp/execute`**  
   - **Plan Test 8** uses `https://localhost:7777/mcp/execute?target=A`.  
   - **Spec §7.1** defines `POST /mcp/execute` with token + args in the request body. No query parameters are specified.  
   - **Fix:** Move `target` to the request body or use a header (`X-Minti-Target`).

3. **`recently_failed` tracking**  
   - **Spec §6.4** includes `- 0.10 * recently_failed` in the system score formula.  
   - **Plan Phase D** says it uses the formula but never defines how `recently_failed` is tracked, reset, or persisted.  
   - **Fix:** Define a sliding window (e.g., last 100 requests) or a persistent counter with decay.

4. **`reasoning_score` calculation**  
   - **Spec §6.3** says it's the *maximum* across enabled, available backends.  
   - **Plan Phase D** says `reasoning_score from /etc/minti/reasoning-scores.yaml × resident-models intersection`. "Intersection" implies filtering, not max-picking.  
   - **Fix:** Explicitly compute `max(score)` over the intersection.

### 🔵 Sequencing Problems
1. **Phase G key sourcing**  
   - **Plan Phase G** says `passing the key in from the M3 audit-log infrastructure path or reading from /var/lib/minti/cland/clan.json`.  
   - The audit logger does not hold the key. Reading from disk on every request creates a TOCTOU race and I/O bottleneck.  
   - **Fix:** Load `clan_key` into memory at startup via a thread-safe config/state accessor.

2. **Phase F self-proxy loop**  
   - **Plan Phase F** says `cland proxies to 127.0.0.1:7780 after auth`. If the Orchestrator is also a worker for inference, routing to itself creates a loop.  
   - **Fix:** Add a self-routing bypass: if `target_member == self_member_id`, route directly to the local runtime without HTTPS round-trip.

3. **Audit package promotion**  
   - **Plan Phase A** promotes `mcp-servers/internal/audit` to `pkg/audit`. This breaks M2/M3's internal boundary and forces a cross-module dependency early.  
   - **Fix:** Define a small `cland/internal/auditlog` interface and adapt the existing logger, or copy the minimal types needed for M4.

### 🟣 Scope Cuts / Creep
1. **Unacknowledged endpoint addition** (`/v1/messages`)  
   - Added in Phase F without spec edit or explicit scope flag. Treat as creep.

2. **Unacknowledged query param** (`?target=A`)  
   - Added in Test 8 without spec alignment. Treat as creep.

3. **Reasoning routing ambiguity**  
   - **Plan Phase F** says reasoning can be proxied to another reasoning-capable member. **Spec §6.1** states reasoning is *always* routed to the Orchestrator. If the Orchestrator proxies reasoning cross-Clan, it violates §6.1. Clarify whether reasoning is ever proxied or strictly local-to-orchestrator.

### ⚪ Verification Gate Gaps
1. **Clock skew > 60s**  
   - **Spec §2.3** requires ±60s window. No test covers 61s skew. Should verify rejection path and clock-sync behavior.

2. **Single-member clan**  
   - **Spec §3.1** allows it. Plan Phase 0 flags it, but verification lacks a single-node test. Add to gate.

3. **Executor-side policy denial**  
   - **Test 8** only covers happy path. **Spec §7.1 step 5** requires `403` on executor-side deny. Add a test with `deny_tools: ["nmap_scan"]` in the target's policy.

4. **mDNS cleanup on crash**  
   - **Test 6** kills systemd abruptly. mDNS registration persists. Node B will try to connect to a dead IP. No test covers TTL/cleanup or fallback behavior.

### 🟠 Overconfidence
1. **Timeline sizing**  
   - 11 phases × ~1 session = 11 sessions. Part-time (20h/wk) = ~2.2 weeks. PRD allocates 4 weeks. Distributed crypto, election, and rotation debugging routinely take 2–3× longer. Overconfident on execution speed.

2. **`grandcat/zeroconf` library choice**  
   - Known to have multicast routing issues on enterprise VLANs and concurrency bugs with concurrent registration/browser. Plan acknowledges spec §4.1 limitations but doesn't address the library's specific failure modes. Add explicit error handling for `EADDRINUSE` and multicast socket reuse.

3. **"Atomic rollback" for key rotation**  
   - Distributed state machines aren't atomic. Plan overconfidently simplifies the ACK/commit protocol. Needs an explicit state machine diagram for rotation phases and crash recovery.

---

## VERDICT
**Conditional Pass (Requires 4 fixes before Phase A begins)**

The plan is structurally sound and correctly defers out-of-scope items. However, it contains **one critical security downgrade** (paste-key entropy), **one distributed systems split-brain risk** (quorum during partition), and **two unacknowledged spec drifts** (`/v1/messages`, `?target=A`). These must be resolved before implementation begins.

**Required pre-flight actions:**
1. Fix paste-key entropy to match 256-bit `clan_key` or explicitly adopt a lower-entropy derivation with compensating controls.
2. Define how `N` is computed during partitions to prevent split-brain quorum.
3. Remove `/v1/messages` and `?target=A` or file spec edits.
4. Add clock skew, single-member, and executor-policy-denial tests to the verification gate.

Once these are addressed, the plan is ready to proceed to Phase 0.

---

## Raw (with thinking trace)

Here is a direct, priority-ordered review of the M4 implementation plan against the Clan Protocol Spec v0.1.

### 🔴 Security & Distributed Systems Risks
1. **Paste-key entropy mismatch (Critical)**  
   - **Spec §2.2** requires `clan_key` as `32 random bytes` (256 bits).  
   - **Plan Phase C** implements a `6-word BIP39-derived phrase`. 6 BIP39 words yield ~64 bits of entropy. This is a **4x cryptographic downgrade** that breaks the spec's threat model.  
   - **Fix:** Either use a custom 12-word wordlist, apply PBKDF2/Argon2 with high iteration counts, or explicitly note the spec's `BIP39-ish` wording allows a lower-entropy derivation with compensating controls.

2. **Nonce cache DoS vector**  
   - **Spec §2.3** requires tracking `(member_id, nonce)` for 5 minutes.  
   - **Plan Phase B** says `5-min cache` with no size limit or eviction policy. Under sustained probing, memory grows linearly per member.  
   - **Fix:** Cap cache at `N_members × 5000 entries` with LRU eviction, or use a sliding-window Bloom filter with explicit false-positive bounds.

3. **Split-brain quorum during partition**  
   - **Spec §5.4** uses `≥ ⌈N/2⌉` where `N = current active roster size`.  
   - **Plan Phase E** implements this mathematically. During a network partition, `N` is locally inconsistent. Both sides can compute `N` from stale advertisements and both reach quorum → split-brain. The spec claims leader-lease avoids this, but the math does not guarantee it without a persistent quorum witness or lease persistence.  
   - **Fix:** Compute `N` from the last *persisted* roster, not the live advertisement cache, or add a persistent quorum file that survives partitions.

4. **Key rotation crash recovery**  
   - **Spec §8.1** says rotation aborts if any ACK is missed.  
   - **Plan Phase H** says `Atomic rollback if any ACK is missed`. This ignores the case where a member ACKs, crashes before persisting the new key, and restarts with the old key. The grace window breaks, causing permanent desync.  
   - **Fix:** Implement a two-phase commit: `PROPOSE_NEW_KEY` → `ACK_NEW_KEY` → `PERSIST_NEW_KEY` → `COMMIT`. Rollback only on explicit NACK or timeout.

### 🟡 Spec-Implementation Drift
1. **`/v1/messages` endpoint**  
   - **Plan Phase F** adds `/v1/messages` to the routing layer.  
   - **Spec §10** endpoint table only lists `/v1/chat/completions`, `/api/chat`, `/v1/embeddings`.  
   - **Fix:** Remove from Phase F or flag as a spec edit.

2. **`?target=A` query param on `/mcp/execute`**  
   - **Plan Test 8** uses `https://localhost:7777/mcp/execute?target=A`.  
   - **Spec §7.1** defines `POST /mcp/execute` with token + args in the request body. No query parameters are specified.  
   - **Fix:** Move `target` to the request body or use a header (`X-Minti-Target`).

3. **`recently_failed` tracking**  
   - **Spec §6.4** includes `- 0.10 * recently_failed` in the system score formula.  
   - **Plan Phase D** says it uses the formula but never defines how `recently_failed` is tracked, reset, or persisted.  
   - **Fix:** Define a sliding window (e.g., last 100 requests) or a persistent counter with decay.

4. **`reasoning_score` calculation**  
   - **Spec §6.3** says it's the *maximum* across enabled, available backends.  
   - **Plan Phase D** says `reasoning_score from /etc/minti/reasoning-scores.yaml × resident-models intersection`. "Intersection" implies filtering, not max-picking.  
   - **Fix:** Explicitly compute `max(score)` over the intersection.

### 🔵 Sequencing Problems
1. **Phase G key sourcing**  
   - **Plan Phase G** says `passing the key in from the M3 audit-log infrastructure path or reading from /var/lib/minti/cland/clan.json`.  
   - The audit logger does not hold the key. Reading from disk on every request creates a TOCTOU race and I/O bottleneck.  
   - **Fix:** Load `clan_key` into memory at startup via a thread-safe config/state accessor.

2. **Phase F self-proxy loop**  
   - **Plan Phase F** says `cland proxies to 127.0.0.1:7780 after auth`. If the Orchestrator is also a worker for inference, routing to itself creates a loop.  
   - **Fix:** Add a self-routing bypass: if `target_member == self_member_id`, route directly to the local runtime without HTTPS round-trip.

3. **Audit package promotion**  
   - **Plan Phase A** promotes `mcp-servers/internal/audit` to `pkg/audit`. This breaks M2/M3's internal boundary and forces a cross-module dependency early.  
   - **Fix:** Define a small `cland/internal/auditlog` interface and adapt the existing logger, or copy the minimal types needed for M4.

### 🟣 Scope Cuts / Creep
1. **Unacknowledged endpoint addition** (`/v1/messages`)  
   - Added in Phase F without spec edit or explicit scope flag. Treat as creep.

2. **Unacknowledged query param** (`?target=A`)  
   - Added in Test 8 without spec alignment. Treat as creep.

3. **Reasoning routing ambiguity**  
   - **Plan Phase F** says reasoning can be proxied to another reasoning-capable member. **Spec §6.1** states reasoning is *always* routed to the Orchestrator. If the Orchestrator proxies reasoning cross-Clan, it violates §6.1. Clarify whether reasoning is ever proxied or strictly local-to-orchestrator.

### ⚪ Verification Gate Gaps
1. **Clock skew > 60s**  
   - **Spec §2.3** requires ±60s window. No test covers 61s skew. Should verify rejection path and clock-sync behavior.

2. **Single-member clan**  
   - **Spec §3.1** allows it. Plan Phase 0 flags it, but verification lacks a single-node test. Add to gate.

3. **Executor-side policy denial**  
   - **Test 8** only covers happy path. **Spec §7.1 step 5** requires `403` on executor-side deny. Add a test with `deny_tools: ["nmap_scan"]` in the target's policy.

4. **mDNS cleanup on crash**  
   - **Test 6** kills systemd abruptly. mDNS registration persists. Node B will try to connect to a dead IP. No test covers TTL/cleanup or fallback behavior.

### 🟠 Overconfidence
1. **Timeline sizing**  
   - 11 phases × ~1 session = 11 sessions. Part-time (20h/wk) = ~2.2 weeks. PRD allocates 4 weeks. Distributed crypto, election, and rotation debugging routinely take 2–3× longer. Overconfident on execution speed.

2. **`grandcat/zeroconf` library choice**  
   - Known to have multicast routing issues on enterprise VLANs and concurrency bugs with concurrent registration/browser. Plan acknowledges spec §4.1 limitations but doesn't address the library's specific failure modes. Add explicit error handling for `EADDRINUSE` and multicast socket reuse.

3. **"Atomic rollback" for key rotation**  
   - Distributed state machines aren't atomic. Plan overconfidently simplifies the ACK/commit protocol. Needs an explicit state machine diagram for rotation phases and crash recovery.

---

## VERDICT
**Conditional Pass (Requires 4 fixes before Phase A begins)**

The plan is structurally sound and correctly defers out-of-scope items. However, it contains **one critical security downgrade** (paste-key entropy), **one distributed systems split-brain risk** (quorum during partition), and **two unacknowledged spec drifts** (`/v1/messages`, `?target=A`). These must be resolved before implementation begins.

**Required pre-flight actions:**
1. Fix paste-key entropy to match 256-bit `clan_key` or explicitly adopt a lower-entropy derivation with compensating controls.
2. Define how `N` is computed during partitions to prevent split-brain quorum.
3. Remove `/v1/messages` and `?target=A` or file spec edits.
4. Add clock skew, single-member, and executor-policy-denial tests to the verification gate.

Once these are addressed, the plan is ready to proceed to Phase 0.