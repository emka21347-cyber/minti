# gemma4:31b — M4 plan review

- wall_time_s: 77.5
- prompt_tokens: 12529
- eval_tokens: 3091
- raw_chars: 4635
- clean_chars: 4635

---

This is a high-quality plan. The architectural decisions (D-M4.1 through D-M4.10) show a strong grasp of the existing codebase and the spec. The sequencing is logical, and the decision to defer the Remote-API backend to M4.1 is a correct move for scope discipline.

However, there are concrete risks regarding the distributed state and the security of the key rotation/revocation flows.

### 1. Security & Distributed Systems Risks

**A. Key Rotation Atomic Failure (Spec §8.1 / Phase H)**
The spec states that if any member fails to ACK, the rotation is aborted. However, the plan does not account for the **Orchestrator crashing during the ACK collection phase**. 
*   **The Risk:** If the Orchestrator sends the `rotate-key` message to 3/5 members, and then crashes, those 3 members are now in a "pending" state with a new key, while the others are on the old key. 
*   **Requirement:** Phase H must define a timeout for the "pending" key state where members revert to the old key if the Orchestrator doesn't send a "Commit" signal.

**B. Revocation Consistency (Spec §3.4 / Phase H)**
The spec calls for revocation to be "gossiped" within 2s. The plan describes this as "gossips to roster." 
*   **The Risk:** If the Orchestrator (or revoker) sends a revocation broadcast and one node is momentarily partitioned, that node will continue to trust the revoked member. Since the revoked member is now "purged" from the Orchestrator's view, the Orchestrator may never try to send that revocation to the partitioned node again.
*   **Requirement:** Revocation records must be persisted and checked against during the heartbeat/advertisement exchange (e.g., the heartbeat `active_roster` should be used to cross-reference the revocation list).

**C. Nonce Cache Memory Exhaustion (D-M4.5 / Phase B)**
The plan implements a 5-minute nonce cache. 
*   **The Risk:** A malicious actor could flood the `cland` endpoint with unique nonces and valid timestamps, bloating the memory of the nonce cache until the daemon OOMs.
*   **Requirement:** The nonce cache must be capped in size (LRU) or tied to the `member_id` with a strict per-member rate limit.

### 2. Spec-Implementation Drift

**A. Tool Execution Token Scope (Spec §7.1 / Phase G)**
Spec §7.1 requires the execution token to be signed with the **Clan Key HMAC**. 
*   **The Risk:** Because the Clan Key is shared among all members, any member can forge an execution token for any other member. The "permission prompt on origin" is a UI-level control, but not a cryptographic one.
*   **Observation:** This is an intentional v1 design (as per OQ-4 in the spec), but the plan should explicitly acknowledge that `VerifyCrossClanToken` only proves the token came from *a* Clan member, not specifically the *origin* member.

### 3. Sequencing Problems

**A. Transport Middleware vs. Key Rotation (Phase B $\rightarrow$ Phase H)**
Phase B implements the HMAC auth middleware. Phase H implements Key Rotation.
*   **The Problem:** Key rotation introduces a "grace window" where two keys are valid. If the middleware in Phase B is written as a simple `if hmac(key, msg) == provided`, Phase H will require a destructive rewrite of the transport layer.
*   **Requirement:** Phase B must implement the middleware to support a `KeyProvider` interface that can return both a `current` and `grace` key.

### 4. Edge Cases & Verification Gaps

**A. The "Zombie" Member (Spec §3.1 / Phase C)**
The spec mentions a 24h timeout for `admitted` members who never become `active`. 
*   **The Gap:** This is not mentioned in the Phase C implementation tasks or the Verification gate. A member that joins but never advertises capabilities should not occupy a slot in the roster indefinitely.

**B. Rotation Failure Path (Verification #9)**
Verification step 9 only tests the "happy path."
*   **The Gap:** There is no test for the **Abort** scenario. 
*   **Requirement:** Add a test case: `Node B is stopped $\rightarrow$ Node A initiates rotate-key $\rightarrow$ Verify rotation aborts and Node A remains on the old key`.

### 5. Overconfidence

**A. mDNS Reliability (D-M4.3 / Phase D)**
The plan relies on `zeroconf` for discovery. While standard, mDNS is notoriously brittle in VM environments (especially VirtualBox) depending on the network adapter mode (NAT vs. Bridged vs. Host-Only).
*   **Warning:** Phase J (Two-node testbed) is the most likely place for the plan to stall. The plan should have a "Plan B" for manual IP entry if mDNS fails in the VM environment, even if it's just for the sake of unblocking the M4 validation.

***

**VERDICT: ship after addressing items 1A, 1B, 3A, and 4B**

---

## Raw (with thinking trace)

This is a high-quality plan. The architectural decisions (D-M4.1 through D-M4.10) show a strong grasp of the existing codebase and the spec. The sequencing is logical, and the decision to defer the Remote-API backend to M4.1 is a correct move for scope discipline.

However, there are concrete risks regarding the distributed state and the security of the key rotation/revocation flows.

### 1. Security & Distributed Systems Risks

**A. Key Rotation Atomic Failure (Spec §8.1 / Phase H)**
The spec states that if any member fails to ACK, the rotation is aborted. However, the plan does not account for the **Orchestrator crashing during the ACK collection phase**. 
*   **The Risk:** If the Orchestrator sends the `rotate-key` message to 3/5 members, and then crashes, those 3 members are now in a "pending" state with a new key, while the others are on the old key. 
*   **Requirement:** Phase H must define a timeout for the "pending" key state where members revert to the old key if the Orchestrator doesn't send a "Commit" signal.

**B. Revocation Consistency (Spec §3.4 / Phase H)**
The spec calls for revocation to be "gossiped" within 2s. The plan describes this as "gossips to roster." 
*   **The Risk:** If the Orchestrator (or revoker) sends a revocation broadcast and one node is momentarily partitioned, that node will continue to trust the revoked member. Since the revoked member is now "purged" from the Orchestrator's view, the Orchestrator may never try to send that revocation to the partitioned node again.
*   **Requirement:** Revocation records must be persisted and checked against during the heartbeat/advertisement exchange (e.g., the heartbeat `active_roster` should be used to cross-reference the revocation list).

**C. Nonce Cache Memory Exhaustion (D-M4.5 / Phase B)**
The plan implements a 5-minute nonce cache. 
*   **The Risk:** A malicious actor could flood the `cland` endpoint with unique nonces and valid timestamps, bloating the memory of the nonce cache until the daemon OOMs.
*   **Requirement:** The nonce cache must be capped in size (LRU) or tied to the `member_id` with a strict per-member rate limit.

### 2. Spec-Implementation Drift

**A. Tool Execution Token Scope (Spec §7.1 / Phase G)**
Spec §7.1 requires the execution token to be signed with the **Clan Key HMAC**. 
*   **The Risk:** Because the Clan Key is shared among all members, any member can forge an execution token for any other member. The "permission prompt on origin" is a UI-level control, but not a cryptographic one.
*   **Observation:** This is an intentional v1 design (as per OQ-4 in the spec), but the plan should explicitly acknowledge that `VerifyCrossClanToken` only proves the token came from *a* Clan member, not specifically the *origin* member.

### 3. Sequencing Problems

**A. Transport Middleware vs. Key Rotation (Phase B $\rightarrow$ Phase H)**
Phase B implements the HMAC auth middleware. Phase H implements Key Rotation.
*   **The Problem:** Key rotation introduces a "grace window" where two keys are valid. If the middleware in Phase B is written as a simple `if hmac(key, msg) == provided`, Phase H will require a destructive rewrite of the transport layer.
*   **Requirement:** Phase B must implement the middleware to support a `KeyProvider` interface that can return both a `current` and `grace` key.

### 4. Edge Cases & Verification Gaps

**A. The "Zombie" Member (Spec §3.1 / Phase C)**
The spec mentions a 24h timeout for `admitted` members who never become `active`. 
*   **The Gap:** This is not mentioned in the Phase C implementation tasks or the Verification gate. A member that joins but never advertises capabilities should not occupy a slot in the roster indefinitely.

**B. Rotation Failure Path (Verification #9)**
Verification step 9 only tests the "happy path."
*   **The Gap:** There is no test for the **Abort** scenario. 
*   **Requirement:** Add a test case: `Node B is stopped $\rightarrow$ Node A initiates rotate-key $\rightarrow$ Verify rotation aborts and Node A remains on the old key`.

### 5. Overconfidence

**A. mDNS Reliability (D-M4.3 / Phase D)**
The plan relies on `zeroconf` for discovery. While standard, mDNS is notoriously brittle in VM environments (especially VirtualBox) depending on the network adapter mode (NAT vs. Bridged vs. Host-Only).
*   **Warning:** Phase J (Two-node testbed) is the most likely place for the plan to stall. The plan should have a "Plan B" for manual IP entry if mDNS fails in the VM environment, even if it's just for the sake of unblocking the M4 validation.

***

**VERDICT: ship after addressing items 1A, 1B, 3A, and 4B**