# qwen3.6:latest - Phase E plan review

- wall_time_s: 31.5
- prompt_tokens: 6558
- eval_tokens: 2742
- raw_chars: 10271
- clean_chars: 10271
- extra_body: {'think': False}

---

This is a solid plan that correctly identifies the critical path and reuses Phase D infrastructure effectively. However, there are three concrete correctness issues regarding quorum computation, state persistence races, and pin propagation latency that must be addressed before shipping.

### 1. Quorum Computation Divergence (Spec §5.4 vs Plan)

**Finding:** The plan computes quorum using `state.Clan.Roster` filtered by `active` or `admitted` states. The spec §5.4 explicitly states: *"N = current active roster size"*.

In `cland/internal/state/state.go`, `RosterMember` has a `State` field. The plan includes `admitted` members in the quorum count (`N`).
*   **Problem:** A member in `admitted` state has not yet completed the handshake/verification process defined in Phase C/D. They are not fully trusted peers yet. Including them in `N` allows a minority partition to reach quorum if the split is, for example, 2 `active` + 1 `admitted` vs 2 `active`. The `admitted` peer is not yet a valid participant in the consensus protocol.
*   **Spec Alignment:** Spec §5.4 implies "active" in the context of a functioning clan. More importantly, `peers.Registry.Live()` is the source of truth for *who is currently participating*. The plan correctly notes using `peers.Registry.Snapshot()` for candidate selection but then switches to `state.Clan` for quorum. This is inconsistent.
*   **Fix:** Compute `N` from `peers.Registry.Snapshot().Members` where `Live(now)` is true. Or, strictly filter `state.Clan.Roster` to only `State == "active"`. Do not include `admitted` members in the quorum denominator. The plan's comment *"N is computed from the persisted active roster... so a partitioned 2/5 minority can't reach quorum"* is only true if `admitted` members are excluded. If `admitted` members are included, a 3-member partition (2 active, 1 admitted) could elect a leader against a 2-member partition (2 active), creating a split-brain where both sides think they have quorum (3/5 vs 2/5 is fine, but if the roster is 3/3, 2 vs 1 is bad). Actually, the bigger issue is that `admitted` members might not have the full state to validate heartbeats correctly.
*   **Recommendation:** Change quorum calculation to: `N = count(peers.Registry.Snapshot().Members where Live(now))`. This aligns with the "live" nature of the lease protocol and ensures only verified, reachable peers count.

### 2. State Persistence Race on Crash (Spec §5.4 + §5.5)

**Finding:** The plan persists `currentTerm`, `currentOrchestrator`, and `leaseExpires` on *every* heartbeat and election commit. It relies on `state.Store.SaveClan()` being atomic-rename.

*   **Problem:** Consider the sequence:
    1.  Orchestrator (A) sends heartbeat with `term=10`, `lease_until=T+8s`.
    2.  Peer (B) receives heartbeat, updates local state to `term=10`, `lease_until=T+8s`.
    3.  A crashes *before* persisting the new term/lease to disk.
    4.  B's lease expires. B starts election, increments term to `11`, wins election, commits `term=11`.
    5.  A restarts. A loads `term=9` from disk (old state).
    6.  A receives B's heartbeat with `term=11`. A accepts it (11 > 9).
    7.  **Critical Failure:** A's local `leaseExpires` is still `T+8s` (from the old term 10 state, or worse, if it never persisted term 10, it might have stale lease data). If A's lease expires again, A might start an election with `term=10` (if it increments from 9) or `term=11` (if it somehow caught up). If A increments to `10`, it conflicts with B's `11`. If A increments to `11`, it creates a term collision.
    8.  More dangerously: If A restarts and sees `term=10` in its *own* persisted state (from a previous successful commit), but B has `term=11`, A will accept B's heartbeat. But if A's `leaseExpires` is *in the past* (because it crashed before updating it), A might immediately trigger an election with `term=11` (if it increments from 10) or `term=10` (if it thinks it's still 10).
    9.  **The Real Race:** The plan says "Persisted roster never disagrees with peers.Registry". This is false. If A crashes, B elects, A restarts, A's `peers.Registry` is empty (or stale). A's `state.Clan.Roster` is persisted. A computes quorum from persisted roster. B computes quorum from live registry. They may disagree on `N`.
    10. **The Term Monotonicity Violation:** If A crashes and restarts, it loads `currentTerm` from disk. If it was in the middle of an election (term incremented but not committed), it might load the old term. If B has already committed `term+1`, A will see a lower term. This is handled by accepting B's heartbeat. But if A's `leaseExpires` is *also* stale, A might think it's still the leader until the lease expires, then try to re-elect with a lower term.
    11. **Specific Fix:** The plan must ensure that upon restart, if `currentTerm` is loaded from disk, the daemon *must* check if any peer has a higher term. If so, it must *immediately* adopt the higher term and *reset* its lease to `now + LEASE_DURATION` (or wait for the next heartbeat). The plan does not explicitly state this "catch-up" logic. It assumes "term continuity" is preserved by persistence, but persistence is not atomic with the network state.
    12. **Recommendation:** Add a "Term Sync" step on startup: Load `currentTerm` from disk. Query `peers.Registry` for the highest term seen in any advertisement. If `registry_term > disk_term`, set `currentTerm = registry_term + 1` (to avoid collision) and `leaseExpires = now + LEASE_DURATION`. This prevents A from re-entering the election with an old term.

### 3. Pin Propagation Latency and Race (Spec §5.6)

**Finding:** The plan flips `state.Clan.PinnedOrchestrator` and calls `advSvc.Bump()` to propagate the pin. It relies on the "next advertise tick" for propagation.

*   **Problem:** The advertise tick is every 30s (implied by Phase D, typical for ads). The plan says "pin propagates via the Advertisement.PinnedOrchestrator field". If the pin is set, and the next ad is 30s away, the pin is not visible to peers for 30s.
*   **Scenario:**
    1.  Orchestrator A is elected.
    2.  User pins B (a peer) via CLI.
    3.  B's state is updated locally. B's advertise loop will send the pin in the next ad (up to 30s).
    4.  Meanwhile, A's lease expires (8s). A starts election.
    5.  B has not yet advertised the pin. A's election logic (scoring) does not see B as pinned. A might win the election again (if A's score is high enough).
    6.  B receives A's heartbeat (term 2). B accepts.
    7.  30s later, B advertises the pin. B's local engine sees the pin. But B is not the Orchestrator. B must now trigger a *new* election to override A.
    8.  This creates a 30s window where the pin is ineffective. The spec §5.6 says "Pinned member wins all elections". It does not say "Pinned member wins elections *after* the pin is advertised". But practically, if the pin is not advertised, other members don't know to prioritize B.
    9.  **Recommendation:** The pin must be propagated *immediately*, not on the next tick. The plan should send a targeted "pin update" message or force an immediate advertisement. Or, the pin should be part of the heartbeat payload? No, heartbeats are for lease. The pin is a capability. It must be advertised. **Fix:** `advSvc.Bump()` should trigger an *immediate* advertisement, not wait for the next tick. The plan says "bumps the advertise loop... so peers see the new pin on the next ad". This is ambiguous. If "bump" means "send immediately", it's fine. If it means "schedule for next tick", it's a bug. Clarify that `Bump()` triggers an immediate ad.

### 4. Minor: Anti-Spoof Check Logic

**Finding:** The plan says: *"A peer ignores a heartbeat if term < currentTerm, or if the sender is not the highest-reasoning_score member according to its current advertisements."*

*   **Problem:** The spec §5.3 says: *"Peers ignore a heartbeat if... the sender is not the highest-reasoning_score member according to its current advertisements."*
*   **Nuance:** The plan uses `peers.Registry.Snapshot()` to determine the highest score. This is correct. However, the plan must ensure that the "current advertisements" are fresh. If the highest-score member's ad is stale (e.g., 89s old), should we trust it? The spec doesn't explicitly say, but `Live(now)` is 4s. `AdFresh(now)` is 90s. The plan uses `AdFresh` for bootstrap. For anti-spoof, it should probably require the ad to be `AdFresh`. If the ad is stale, the score is unreliable. The plan should add a check: `if !AdFresh(now) { ignore }` for anti-spoof purposes. Otherwise, a stale ad from a high-score member could be spoofed by a lower-score member who is currently live.
*   **Recommendation:** Add `AdFresh(now)` check to the anti-spoof logic. If the sender's ad is not fresh, ignore the heartbeat for anti-spoof purposes (or treat it as if the sender is not the highest score).

### 5. Overconfidence: "Random backoff converges"

**Finding:** The plan states: *"Statistical: 10 runs, ≥9 should converge within 3 rounds."*

*   **Problem:** This is a probabilistic claim. In a distributed system, you cannot guarantee convergence in a fixed number of rounds. The test should be: *"Run 100 times, measure max rounds to convergence. Assert max rounds < 10."* Or better, assert that the backoff distribution is uniform and that the probability of collision decreases exponentially. The current test is too weak.
*   **Recommendation:** Change the test to: *"Run 100 simulations. Assert that 100% converge within 10 rounds. Assert that the average rounds to convergence is < 3."* This validates the probabilistic claim more robustly.

---

### Summary of Required Changes

1.  **Quorum:** Compute `N` from `peers.Registry.Snapshot().Members` where `Live(now)` is true. Exclude `admitted` members.
2.  **Startup Term Sync:** On startup, if `disk_term < max_registry_term`, set `currentTerm = max_registry_term + 1` and reset lease.
3.  **Pin Propagation:** Ensure `advSvc.Bump()` triggers an *immediate* advertisement, not a scheduled one.
4.  **Anti-Spoof:** Add `AdFresh(now)` check to the anti-spoof logic.
5.  **Test Strengthening:** Strengthen the split-brain backoff test to 100 runs and assert max rounds.

---

VERDICT: ship after addressing items 1, 2, 3

---

## Raw (with thinking trace if any)

This is a solid plan that correctly identifies the critical path and reuses Phase D infrastructure effectively. However, there are three concrete correctness issues regarding quorum computation, state persistence races, and pin propagation latency that must be addressed before shipping.

### 1. Quorum Computation Divergence (Spec §5.4 vs Plan)

**Finding:** The plan computes quorum using `state.Clan.Roster` filtered by `active` or `admitted` states. The spec §5.4 explicitly states: *"N = current active roster size"*.

In `cland/internal/state/state.go`, `RosterMember` has a `State` field. The plan includes `admitted` members in the quorum count (`N`).
*   **Problem:** A member in `admitted` state has not yet completed the handshake/verification process defined in Phase C/D. They are not fully trusted peers yet. Including them in `N` allows a minority partition to reach quorum if the split is, for example, 2 `active` + 1 `admitted` vs 2 `active`. The `admitted` peer is not yet a valid participant in the consensus protocol.
*   **Spec Alignment:** Spec §5.4 implies "active" in the context of a functioning clan. More importantly, `peers.Registry.Live()` is the source of truth for *who is currently participating*. The plan correctly notes using `peers.Registry.Snapshot()` for candidate selection but then switches to `state.Clan` for quorum. This is inconsistent.
*   **Fix:** Compute `N` from `peers.Registry.Snapshot().Members` where `Live(now)` is true. Or, strictly filter `state.Clan.Roster` to only `State == "active"`. Do not include `admitted` members in the quorum denominator. The plan's comment *"N is computed from the persisted active roster... so a partitioned 2/5 minority can't reach quorum"* is only true if `admitted` members are excluded. If `admitted` members are included, a 3-member partition (2 active, 1 admitted) could elect a leader against a 2-member partition (2 active), creating a split-brain where both sides think they have quorum (3/5 vs 2/5 is fine, but if the roster is 3/3, 2 vs 1 is bad). Actually, the bigger issue is that `admitted` members might not have the full state to validate heartbeats correctly.
*   **Recommendation:** Change quorum calculation to: `N = count(peers.Registry.Snapshot().Members where Live(now))`. This aligns with the "live" nature of the lease protocol and ensures only verified, reachable peers count.

### 2. State Persistence Race on Crash (Spec §5.4 + §5.5)

**Finding:** The plan persists `currentTerm`, `currentOrchestrator`, and `leaseExpires` on *every* heartbeat and election commit. It relies on `state.Store.SaveClan()` being atomic-rename.

*   **Problem:** Consider the sequence:
    1.  Orchestrator (A) sends heartbeat with `term=10`, `lease_until=T+8s`.
    2.  Peer (B) receives heartbeat, updates local state to `term=10`, `lease_until=T+8s`.
    3.  A crashes *before* persisting the new term/lease to disk.
    4.  B's lease expires. B starts election, increments term to `11`, wins election, commits `term=11`.
    5.  A restarts. A loads `term=9` from disk (old state).
    6.  A receives B's heartbeat with `term=11`. A accepts it (11 > 9).
    7.  **Critical Failure:** A's local `leaseExpires` is still `T+8s` (from the old term 10 state, or worse, if it never persisted term 10, it might have stale lease data). If A's lease expires again, A might start an election with `term=10` (if it increments from 9) or `term=11` (if it somehow caught up). If A increments to `10`, it conflicts with B's `11`. If A increments to `11`, it creates a term collision.
    8.  More dangerously: If A restarts and sees `term=10` in its *own* persisted state (from a previous successful commit), but B has `term=11`, A will accept B's heartbeat. But if A's `leaseExpires` is *in the past* (because it crashed before updating it), A might immediately trigger an election with `term=11` (if it increments from 10) or `term=10` (if it thinks it's still 10).
    9.  **The Real Race:** The plan says "Persisted roster never disagrees with peers.Registry". This is false. If A crashes, B elects, A restarts, A's `peers.Registry` is empty (or stale). A's `state.Clan.Roster` is persisted. A computes quorum from persisted roster. B computes quorum from live registry. They may disagree on `N`.
    10. **The Term Monotonicity Violation:** If A crashes and restarts, it loads `currentTerm` from disk. If it was in the middle of an election (term incremented but not committed), it might load the old term. If B has already committed `term+1`, A will see a lower term. This is handled by accepting B's heartbeat. But if A's `leaseExpires` is *also* stale, A might think it's still the leader until the lease expires, then try to re-elect with a lower term.
    11. **Specific Fix:** The plan must ensure that upon restart, if `currentTerm` is loaded from disk, the daemon *must* check if any peer has a higher term. If so, it must *immediately* adopt the higher term and *reset* its lease to `now + LEASE_DURATION` (or wait for the next heartbeat). The plan does not explicitly state this "catch-up" logic. It assumes "term continuity" is preserved by persistence, but persistence is not atomic with the network state.
    12. **Recommendation:** Add a "Term Sync" step on startup: Load `currentTerm` from disk. Query `peers.Registry` for the highest term seen in any advertisement. If `registry_term > disk_term`, set `currentTerm = registry_term + 1` (to avoid collision) and `leaseExpires = now + LEASE_DURATION`. This prevents A from re-entering the election with an old term.

### 3. Pin Propagation Latency and Race (Spec §5.6)

**Finding:** The plan flips `state.Clan.PinnedOrchestrator` and calls `advSvc.Bump()` to propagate the pin. It relies on the "next advertise tick" for propagation.

*   **Problem:** The advertise tick is every 30s (implied by Phase D, typical for ads). The plan says "pin propagates via the Advertisement.PinnedOrchestrator field". If the pin is set, and the next ad is 30s away, the pin is not visible to peers for 30s.
*   **Scenario:**
    1.  Orchestrator A is elected.
    2.  User pins B (a peer) via CLI.
    3.  B's state is updated locally. B's advertise loop will send the pin in the next ad (up to 30s).
    4.  Meanwhile, A's lease expires (8s). A starts election.
    5.  B has not yet advertised the pin. A's election logic (scoring) does not see B as pinned. A might win the election again (if A's score is high enough).
    6.  B receives A's heartbeat (term 2). B accepts.
    7.  30s later, B advertises the pin. B's local engine sees the pin. But B is not the Orchestrator. B must now trigger a *new* election to override A.
    8.  This creates a 30s window where the pin is ineffective. The spec §5.6 says "Pinned member wins all elections". It does not say "Pinned member wins elections *after* the pin is advertised". But practically, if the pin is not advertised, other members don't know to prioritize B.
    9.  **Recommendation:** The pin must be propagated *immediately*, not on the next tick. The plan should send a targeted "pin update" message or force an immediate advertisement. Or, the pin should be part of the heartbeat payload? No, heartbeats are for lease. The pin is a capability. It must be advertised. **Fix:** `advSvc.Bump()` should trigger an *immediate* advertisement, not wait for the next tick. The plan says "bumps the advertise loop... so peers see the new pin on the next ad". This is ambiguous. If "bump" means "send immediately", it's fine. If it means "schedule for next tick", it's a bug. Clarify that `Bump()` triggers an immediate ad.

### 4. Minor: Anti-Spoof Check Logic

**Finding:** The plan says: *"A peer ignores a heartbeat if term < currentTerm, or if the sender is not the highest-reasoning_score member according to its current advertisements."*

*   **Problem:** The spec §5.3 says: *"Peers ignore a heartbeat if... the sender is not the highest-reasoning_score member according to its current advertisements."*
*   **Nuance:** The plan uses `peers.Registry.Snapshot()` to determine the highest score. This is correct. However, the plan must ensure that the "current advertisements" are fresh. If the highest-score member's ad is stale (e.g., 89s old), should we trust it? The spec doesn't explicitly say, but `Live(now)` is 4s. `AdFresh(now)` is 90s. The plan uses `AdFresh` for bootstrap. For anti-spoof, it should probably require the ad to be `AdFresh`. If the ad is stale, the score is unreliable. The plan should add a check: `if !AdFresh(now) { ignore }` for anti-spoof purposes. Otherwise, a stale ad from a high-score member could be spoofed by a lower-score member who is currently live.
*   **Recommendation:** Add `AdFresh(now)` check to the anti-spoof logic. If the sender's ad is not fresh, ignore the heartbeat for anti-spoof purposes (or treat it as if the sender is not the highest score).

### 5. Overconfidence: "Random backoff converges"

**Finding:** The plan states: *"Statistical: 10 runs, ≥9 should converge within 3 rounds."*

*   **Problem:** This is a probabilistic claim. In a distributed system, you cannot guarantee convergence in a fixed number of rounds. The test should be: *"Run 100 times, measure max rounds to convergence. Assert max rounds < 10."* Or better, assert that the backoff distribution is uniform and that the probability of collision decreases exponentially. The current test is too weak.
*   **Recommendation:** Change the test to: *"Run 100 simulations. Assert that 100% converge within 10 rounds. Assert that the average rounds to convergence is < 3."* This validates the probabilistic claim more robustly.

---

### Summary of Required Changes

1.  **Quorum:** Compute `N` from `peers.Registry.Snapshot().Members` where `Live(now)` is true. Exclude `admitted` members.
2.  **Startup Term Sync:** On startup, if `disk_term < max_registry_term`, set `currentTerm = max_registry_term + 1` and reset lease.
3.  **Pin Propagation:** Ensure `advSvc.Bump()` triggers an *immediate* advertisement, not a scheduled one.
4.  **Anti-Spoof:** Add `AdFresh(now)` check to the anti-spoof logic.
5.  **Test Strengthening:** Strengthen the split-brain backoff test to 100 runs and assert max rounds.

---

VERDICT: ship after addressing items 1, 2, 3