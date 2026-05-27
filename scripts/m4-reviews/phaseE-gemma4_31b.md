# gemma4:31b - Phase E plan review

- wall_time_s: 74.3
- prompt_tokens: 6781
- eval_tokens: 3604
- raw_chars: 4737
- clean_chars: 4737
- extra_body: {}

---

This review focuses on the distributed-systems correctness of the Phase E implementation plan.

### 1. Distributed-Systems Correctness & Edge Cases

**Critical: The "Zombie Leader" Problem (Priority 1 & 6)**
The plan describes the heartbeat goroutine in `engine.go` as a simple ticker. However, the Orchestrator is elected specifically because it is "reasoning-capable" (spec §5.4). If the `runtime-adapter` (the process providing the reasoning capability) crashes or deadlocks, but the `cland` daemon remains healthy, the `election.Engine` will continue to emit heartbeats.
*   **Problem:** The cluster will remain locked to an Orchestrator that is functionally dead, preventing failover because the lease is still being renewed.
*   **Requirement:** The heartbeat emission must be gated by a health check of the local reasoning capability. If the adapter is unresponsive, the Orchestrator must stop heartbeating to trigger a failover.

**Clock Skew Vulnerability (Priority 6)**
Spec §5.3 defines `lease_until` as an absolute timestamp (`now + LEASE_DURATION`). 
*   **Problem:** In a distributed system, relying on absolute timestamps for lease expiry is dangerous. If the Orchestrator's clock is significantly ahead of a peer's clock, the peer will perceive a lease that lasts much longer than 8s. If the Orchestrator's clock is behind, the peer may trigger a failover prematurely.
*   **Mitigation:** While the `FAILOVER_GRACE` (6s) provides a buffer, the plan should explicitly state that `lease_expires` is tracked locally as `time.Now().Add(LEASE_DURATION)` upon receipt of a valid heartbeat, rather than trusting the absolute timestamp in the JSON payload for the local expiry timer.

**Heartbeat I/O Amplification (Priority 4)**
The plan states: "Written on every heartbeat-applied... via the existing `Store.SaveClan()` atomic-rename path."
*   **Problem:** If a Clan has 10 members, each member receives 1 heartbeat every 2 seconds. Performing an atomic-rename disk write every 2 seconds per node is excessive and will cause significant I/O wait/wear, especially on SD cards or slow cloud volumes.
*   **Requirement:** `currentTerm` and `currentOrchestrator` must be persisted, but `leaseExpires` (a volatile timer) should not be persisted on every heartbeat. Persist the term/winner only on change.

### 2. Spec Drift

**Missing Heartbeat Payload Fields (Priority 2)**
Spec §5.3 explicitly requires the heartbeat to carry the `active_roster`.
*   **Problem:** The plan's description of `state.go` and `engine.go` mentions using the roster for quorum computation, but it does not mention including the `active_roster` in the outgoing heartbeat JSON.
*   **Requirement:** Ensure the heartbeat payload matches spec §5.3 exactly.

### 3. Sequencing & Reuse

**Candidate Selection Logic (Priority 3)**
The plan introduces a logic branch: `Live(now)` OR `AdFresh(now)` if no prior heartbeat exists.
*   **Observation:** This is a correct and necessary addition to handle the "cold start" bootstrap where no leader has existed yet to provide a heartbeat. This is a valid divergence from the strict reading of §5.4 to ensure the system actually starts.

### 4. Pin / Advertisement Propagation

**Pin-Flip Race (Priority 5)**
The plan relies on `advSvc.Bump()` to propagate a pin.
*   **Analysis:** There is a window where a node is `PinnedOrchestrator = true` locally, but peers haven't received the advertisement. If an election triggers now, the local node will claim victory (per §5.4), but peers will reject it (409 Conflict) because their view of the candidates doesn't show the pin.
*   **Verdict:** This is acceptable. The "loser" will back off and retry. Once the advertisement propagates, the next election cycle will succeed. No change needed, but the test matrix should explicitly verify this "transient failure" period.

### 5. Overconfidence

**Random Backoff Convergence (Priority 7)**
The plan claims "10 runs, $\ge 9$ should converge within 3 rounds."
*   **Critique:** This is a reasonable statistical target for a unit test, but the plan lacks a "worst-case" test (e.g., 5 nodes all attempting to lead simultaneously). I suggest adding a high-contention test case to the unit matrix.

---

### Summary of Findings

| ID | Priority | Issue | Impact |
|---|---|---|---|
| **E1** | High | Zombie Leader | Orchestrator can hold lease while reasoning is dead. |
| **E2** | Med | I/O Pressure | Disk thrashing due to `SaveClan` on every heartbeat. |
| **E3** | Med | Spec Drift | `active_roster` missing from heartbeat payload. |
| **E4** | Low | Clock Skew | Absolute timestamps in spec §5.3 are risky. |

**VERDICT: block on item E1** (The "Zombie Leader" scenario breaks the primary purpose of the failover mechanism).

---

## Raw (with thinking trace if any)

This review focuses on the distributed-systems correctness of the Phase E implementation plan.

### 1. Distributed-Systems Correctness & Edge Cases

**Critical: The "Zombie Leader" Problem (Priority 1 & 6)**
The plan describes the heartbeat goroutine in `engine.go` as a simple ticker. However, the Orchestrator is elected specifically because it is "reasoning-capable" (spec §5.4). If the `runtime-adapter` (the process providing the reasoning capability) crashes or deadlocks, but the `cland` daemon remains healthy, the `election.Engine` will continue to emit heartbeats.
*   **Problem:** The cluster will remain locked to an Orchestrator that is functionally dead, preventing failover because the lease is still being renewed.
*   **Requirement:** The heartbeat emission must be gated by a health check of the local reasoning capability. If the adapter is unresponsive, the Orchestrator must stop heartbeating to trigger a failover.

**Clock Skew Vulnerability (Priority 6)**
Spec §5.3 defines `lease_until` as an absolute timestamp (`now + LEASE_DURATION`). 
*   **Problem:** In a distributed system, relying on absolute timestamps for lease expiry is dangerous. If the Orchestrator's clock is significantly ahead of a peer's clock, the peer will perceive a lease that lasts much longer than 8s. If the Orchestrator's clock is behind, the peer may trigger a failover prematurely.
*   **Mitigation:** While the `FAILOVER_GRACE` (6s) provides a buffer, the plan should explicitly state that `lease_expires` is tracked locally as `time.Now().Add(LEASE_DURATION)` upon receipt of a valid heartbeat, rather than trusting the absolute timestamp in the JSON payload for the local expiry timer.

**Heartbeat I/O Amplification (Priority 4)**
The plan states: "Written on every heartbeat-applied... via the existing `Store.SaveClan()` atomic-rename path."
*   **Problem:** If a Clan has 10 members, each member receives 1 heartbeat every 2 seconds. Performing an atomic-rename disk write every 2 seconds per node is excessive and will cause significant I/O wait/wear, especially on SD cards or slow cloud volumes.
*   **Requirement:** `currentTerm` and `currentOrchestrator` must be persisted, but `leaseExpires` (a volatile timer) should not be persisted on every heartbeat. Persist the term/winner only on change.

### 2. Spec Drift

**Missing Heartbeat Payload Fields (Priority 2)**
Spec §5.3 explicitly requires the heartbeat to carry the `active_roster`.
*   **Problem:** The plan's description of `state.go` and `engine.go` mentions using the roster for quorum computation, but it does not mention including the `active_roster` in the outgoing heartbeat JSON.
*   **Requirement:** Ensure the heartbeat payload matches spec §5.3 exactly.

### 3. Sequencing & Reuse

**Candidate Selection Logic (Priority 3)**
The plan introduces a logic branch: `Live(now)` OR `AdFresh(now)` if no prior heartbeat exists.
*   **Observation:** This is a correct and necessary addition to handle the "cold start" bootstrap where no leader has existed yet to provide a heartbeat. This is a valid divergence from the strict reading of §5.4 to ensure the system actually starts.

### 4. Pin / Advertisement Propagation

**Pin-Flip Race (Priority 5)**
The plan relies on `advSvc.Bump()` to propagate a pin.
*   **Analysis:** There is a window where a node is `PinnedOrchestrator = true` locally, but peers haven't received the advertisement. If an election triggers now, the local node will claim victory (per §5.4), but peers will reject it (409 Conflict) because their view of the candidates doesn't show the pin.
*   **Verdict:** This is acceptable. The "loser" will back off and retry. Once the advertisement propagates, the next election cycle will succeed. No change needed, but the test matrix should explicitly verify this "transient failure" period.

### 5. Overconfidence

**Random Backoff Convergence (Priority 7)**
The plan claims "10 runs, $\ge 9$ should converge within 3 rounds."
*   **Critique:** This is a reasonable statistical target for a unit test, but the plan lacks a "worst-case" test (e.g., 5 nodes all attempting to lead simultaneously). I suggest adding a high-contention test case to the unit matrix.

---

### Summary of Findings

| ID | Priority | Issue | Impact |
|---|---|---|---|
| **E1** | High | Zombie Leader | Orchestrator can hold lease while reasoning is dead. |
| **E2** | Med | I/O Pressure | Disk thrashing due to `SaveClan` on every heartbeat. |
| **E3** | Med | Spec Drift | `active_roster` missing from heartbeat payload. |
| **E4** | Low | Clock Skew | Absolute timestamps in spec §5.3 are risky. |

**VERDICT: block on item E1** (The "Zombie Leader" scenario breaks the primary purpose of the failover mechanism).