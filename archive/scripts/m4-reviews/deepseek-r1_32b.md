# deepseek-r1:32b — M4 plan review

- wall_time_s: 36.2
- prompt_tokens: 11651
- eval_tokens: 1011
- raw_chars: 2830
- clean_chars: 2830

---

### Review of M4 Implementation Plan for Clan Protocol

#### 1. Security and Distributed-Systems Risks:
- **Shared Clan Key**: The use of a shared `clan_key` for HMAC authentication (§2.3) introduces a significant insider-abuse risk. If any member is compromised, it can impersonate others. This violates the principle of least privilege and creates a single point of failure. The spec acknowledges this as "residual R1" but defers mitigation to v2 (§8.4). However, the plan does not propose any interim mitigations beyond audit logging.
- **Election Spoofing**: The election flow (§5.4) relies on members trusting their own cached advertisements for anti-spoofing. This could be exploited if a member's advertisement is stale or manipulated during network partitions.

#### 2. Spec-Implementation Drift:
- The plan does not address the `pinned_orchestrator` tiebreaker (spec §5.6) where multiple members have `pinned_orchestrator: true`. The spec states that the lowest `member_id` wins, but there is no mention of implementing this logic.

#### 3. Sequencing Problems:
- **Phase F Dependency**: The routing layer in Phase F depends on `runtime-adapter` having a remote-api backend (for Cardputer demos), but this is deferred to M4.1. This creates a dependency where the routing layer cannot be fully tested without the remote-api backend, which may not exist yet.
- **Verification Gate Prerequisites**: The verification script's step 5 assumes `opencode` can route through cland to minti-runtime, but this requires the runtime-adapter's remote-api backend to be implemented first.

#### 4. Scope Cuts:
- The plan explicitly cuts the Cardputer demo (PRD §9 #8) by deferring it to M4.1. However, it does not acknowledge that this omission may prevent full verification of the routing layer in M4.

#### 5. Edge Cases Not Covered:
- **Multiple Pinned Members**: The spec allows multiple `pinned_orchestrator: true` settings but only logs a warning (§5.6). The verification gate does not test this scenario.
- **Key Rotation During Election**: The plan does not test key rotation while an election is in progress, which could leave the system in an inconsistent state.

#### 6. Overconfidence:
- **Go's Stdlib for Crypto**: While using Go's stdlib for HMAC and TLS is reasonable, the plan assumes it will handle all edge cases without additional auditing or fuzzing.
- **mTLS Assumptions**: The plan assumes that per-member mTLS (v2) will fully mitigate insider risks but does not propose interim mitigations for v1.

---

### Verdict:
The plan is mostly solid but needs to address the shared clan key risk and implement the `pinned_orchestrator` tiebreaker. It also needs to adjust its verification gate to cover more edge cases and acknowledge dependencies on external components.

VERDICT: ship after addressing items 1, 2, and 5

---

## Raw (with thinking trace)

### Review of M4 Implementation Plan for Clan Protocol

#### 1. Security and Distributed-Systems Risks:
- **Shared Clan Key**: The use of a shared `clan_key` for HMAC authentication (§2.3) introduces a significant insider-abuse risk. If any member is compromised, it can impersonate others. This violates the principle of least privilege and creates a single point of failure. The spec acknowledges this as "residual R1" but defers mitigation to v2 (§8.4). However, the plan does not propose any interim mitigations beyond audit logging.
- **Election Spoofing**: The election flow (§5.4) relies on members trusting their own cached advertisements for anti-spoofing. This could be exploited if a member's advertisement is stale or manipulated during network partitions.

#### 2. Spec-Implementation Drift:
- The plan does not address the `pinned_orchestrator` tiebreaker (spec §5.6) where multiple members have `pinned_orchestrator: true`. The spec states that the lowest `member_id` wins, but there is no mention of implementing this logic.

#### 3. Sequencing Problems:
- **Phase F Dependency**: The routing layer in Phase F depends on `runtime-adapter` having a remote-api backend (for Cardputer demos), but this is deferred to M4.1. This creates a dependency where the routing layer cannot be fully tested without the remote-api backend, which may not exist yet.
- **Verification Gate Prerequisites**: The verification script's step 5 assumes `opencode` can route through cland to minti-runtime, but this requires the runtime-adapter's remote-api backend to be implemented first.

#### 4. Scope Cuts:
- The plan explicitly cuts the Cardputer demo (PRD §9 #8) by deferring it to M4.1. However, it does not acknowledge that this omission may prevent full verification of the routing layer in M4.

#### 5. Edge Cases Not Covered:
- **Multiple Pinned Members**: The spec allows multiple `pinned_orchestrator: true` settings but only logs a warning (§5.6). The verification gate does not test this scenario.
- **Key Rotation During Election**: The plan does not test key rotation while an election is in progress, which could leave the system in an inconsistent state.

#### 6. Overconfidence:
- **Go's Stdlib for Crypto**: While using Go's stdlib for HMAC and TLS is reasonable, the plan assumes it will handle all edge cases without additional auditing or fuzzing.
- **mTLS Assumptions**: The plan assumes that per-member mTLS (v2) will fully mitigate insider risks but does not propose interim mitigations for v1.

---

### Verdict:
The plan is mostly solid but needs to address the shared clan key risk and implement the `pinned_orchestrator` tiebreaker. It also needs to adjust its verification gate to cover more edge cases and acknowledge dependencies on external components.

VERDICT: ship after addressing items 1, 2, and 5