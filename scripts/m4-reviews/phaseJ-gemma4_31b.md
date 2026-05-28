# gemma4:31b - Phase J plan review

- wall_time_s: 57.2
- prompt_tokens: 7040
- eval_tokens: 2590
- raw_chars: 4652
- clean_chars: 4652
- extra_body: {}

---

This review focuses on the transition from $N=1$ (Phase I) to $N=3$ (Phase J). While the plan is structurally sound, there are several "silent killers" related to VM cloning and the sequential nature of the 16-test gate.

### 1. VBoxManage Clone Gotchas (High Priority)
The plan assumes `install.sh` handles the transition from a cloned VM to a unique member. It does not.

*   **Identity Collision:** Per Document 4, `minti-dev` already has a functional Clan identity (member_id, keys) from Phase I. Cloning the VM clones `/var/lib/minti/cland/clan.json` (or equivalent). If `minti-dev-2` boots with the same `member_id` as `minti-dev`, the Clan will experience "member flapping" or immediate rejection. The plan must explicitly include a step to wipe the identity directory on `minti-dev-2` before running `join`.
*   **SSH Host Key Collision:** Cloning the VM clones `/etc/ssh/ssh_host_*_key`. When the Windows host (the test runner) attempts to SSH into VM-A (port 2222) and then VM-B (port 2223), it will encounter the same host key. While this doesn't break the protocol, it will trigger `REMOTE HOST IDENTIFICATION HAS CHANGED` warnings in the PowerShell session, potentially breaking the `m4-validate.ps1` automation.
*   **Netplan/Network Persistence:** If `minti-dev` has any static IP configurations in `/etc/netplan/` or `/etc/network/interfaces`, the clone will inherit them, causing an IP conflict on the host-only network.

### 2. Test Ordering & State Contamination (High Priority)
The 16-test gate is treated as a linear sequence, but the state mutations are cumulative.

*   **The Quorum Collapse (Test 10 $\rightarrow$ Test 14):** 
    *   **Test 10** revokes Member C (Windows). Member C is now effectively dead/unauthorized.
    *   **Test 14** hard-kills Member A (VM-A).
    *   **The Result:** Only Member B (VM-B) remains active. In a 3-node cluster, quorum is $\lceil 3/2 \rceil = 2$. With A killed and C revoked, the cluster loses quorum. Test 14 will fail not because the "hard-kill" logic is broken, but because the cluster is dead.
    *   **Requirement:** The test suite needs a "Cluster Reset" or "Member Restore" step between Test 10 and Test 14 to ensure a healthy 3-node baseline before testing failover.

### 3. 3-Node Correctness Gaps (Medium Priority)
*   **Key Rotation Unanimity:** Document 3 notes that rotation requires unanimous ACK (3/3). This is the most fragile part of the 3-node setup. If the Windows foreground PowerShell process is throttled or the network blips, the rotation will abort (Test 15). The plan should explicitly verify the `PROPOSE_TIMEOUT` value to ensure it's generous enough for a Windows-to-Linux round trip.
*   **Election Split-Vote:** With 3 nodes starting simultaneously, there is a theoretical risk of a split vote (each node votes for itself). The plan mentions "Wait 15s," but does not specify what to do if `minti-cland orchestrator` shows no leader after 15s.

### 4. Windows-as-Member Traps (Medium Priority)
*   **Process Lifecycle:** Running `minti-cland.exe` as a foreground PowerShell job is risky for a 45-minute test suite. If the user accidentally clicks into the window and hits a key, or if the shell enters a suspended state, the node drops. 
*   **Recommendation:** Use `Start-Process -WindowStyle Hidden` or a simple wrapper to ensure the daemon doesn't die due to terminal interaction.

### 5. Pre-flagged Failure Mode Adequacy (Low Priority)
*   **Test 14 (mDNS Goodbye):** The plan's decision to soften this to "marked Live=false" is correct. Expecting a `kill -9` to trigger a graceful mDNS packet is unrealistic.
*   **Test 10 (Propagation):** The plan's shift to "all 3 up, revoke C, B detects" is the correct way to validate the gossip protocol.

### 6. Time Estimates & Sequencing (Low Priority)
*   **Resume Capability:** The plan lacks a "resume" mechanism. If Test 15 fails, the user must either manually intervene or restart the entire 2-hour process. Given the state mutations, a "Reset Cluster" script is needed to make Pass 4 iterative rather than a "one-shot" gamble.

***

### VERDICT: block on item 1 & 2

**Required changes before shipping:**
1.  **Add Identity Wipe:** Add a command to `scripts/m4-setup-second-vm.ps1` or the boot sequence to delete `/var/lib/minti/cland/*` on `minti-dev-2` to prevent `member_id` collisions.
2.  **Fix Test Sequence:** Insert a "Cluster Recovery" step after Test 10 (Revocation) to restore Member C before proceeding to Test 14 (Hard-kill), otherwise quorum will be lost.
3.  **SSH Key Rotation:** Add a step to regenerate SSH host keys on `minti-dev-2` to avoid `known_hosts` conflicts on the Windows host.

---

## Raw (with thinking trace if any)

This review focuses on the transition from $N=1$ (Phase I) to $N=3$ (Phase J). While the plan is structurally sound, there are several "silent killers" related to VM cloning and the sequential nature of the 16-test gate.

### 1. VBoxManage Clone Gotchas (High Priority)
The plan assumes `install.sh` handles the transition from a cloned VM to a unique member. It does not.

*   **Identity Collision:** Per Document 4, `minti-dev` already has a functional Clan identity (member_id, keys) from Phase I. Cloning the VM clones `/var/lib/minti/cland/clan.json` (or equivalent). If `minti-dev-2` boots with the same `member_id` as `minti-dev`, the Clan will experience "member flapping" or immediate rejection. The plan must explicitly include a step to wipe the identity directory on `minti-dev-2` before running `join`.
*   **SSH Host Key Collision:** Cloning the VM clones `/etc/ssh/ssh_host_*_key`. When the Windows host (the test runner) attempts to SSH into VM-A (port 2222) and then VM-B (port 2223), it will encounter the same host key. While this doesn't break the protocol, it will trigger `REMOTE HOST IDENTIFICATION HAS CHANGED` warnings in the PowerShell session, potentially breaking the `m4-validate.ps1` automation.
*   **Netplan/Network Persistence:** If `minti-dev` has any static IP configurations in `/etc/netplan/` or `/etc/network/interfaces`, the clone will inherit them, causing an IP conflict on the host-only network.

### 2. Test Ordering & State Contamination (High Priority)
The 16-test gate is treated as a linear sequence, but the state mutations are cumulative.

*   **The Quorum Collapse (Test 10 $\rightarrow$ Test 14):** 
    *   **Test 10** revokes Member C (Windows). Member C is now effectively dead/unauthorized.
    *   **Test 14** hard-kills Member A (VM-A).
    *   **The Result:** Only Member B (VM-B) remains active. In a 3-node cluster, quorum is $\lceil 3/2 \rceil = 2$. With A killed and C revoked, the cluster loses quorum. Test 14 will fail not because the "hard-kill" logic is broken, but because the cluster is dead.
    *   **Requirement:** The test suite needs a "Cluster Reset" or "Member Restore" step between Test 10 and Test 14 to ensure a healthy 3-node baseline before testing failover.

### 3. 3-Node Correctness Gaps (Medium Priority)
*   **Key Rotation Unanimity:** Document 3 notes that rotation requires unanimous ACK (3/3). This is the most fragile part of the 3-node setup. If the Windows foreground PowerShell process is throttled or the network blips, the rotation will abort (Test 15). The plan should explicitly verify the `PROPOSE_TIMEOUT` value to ensure it's generous enough for a Windows-to-Linux round trip.
*   **Election Split-Vote:** With 3 nodes starting simultaneously, there is a theoretical risk of a split vote (each node votes for itself). The plan mentions "Wait 15s," but does not specify what to do if `minti-cland orchestrator` shows no leader after 15s.

### 4. Windows-as-Member Traps (Medium Priority)
*   **Process Lifecycle:** Running `minti-cland.exe` as a foreground PowerShell job is risky for a 45-minute test suite. If the user accidentally clicks into the window and hits a key, or if the shell enters a suspended state, the node drops. 
*   **Recommendation:** Use `Start-Process -WindowStyle Hidden` or a simple wrapper to ensure the daemon doesn't die due to terminal interaction.

### 5. Pre-flagged Failure Mode Adequacy (Low Priority)
*   **Test 14 (mDNS Goodbye):** The plan's decision to soften this to "marked Live=false" is correct. Expecting a `kill -9` to trigger a graceful mDNS packet is unrealistic.
*   **Test 10 (Propagation):** The plan's shift to "all 3 up, revoke C, B detects" is the correct way to validate the gossip protocol.

### 6. Time Estimates & Sequencing (Low Priority)
*   **Resume Capability:** The plan lacks a "resume" mechanism. If Test 15 fails, the user must either manually intervene or restart the entire 2-hour process. Given the state mutations, a "Reset Cluster" script is needed to make Pass 4 iterative rather than a "one-shot" gamble.

***

### VERDICT: block on item 1 & 2

**Required changes before shipping:**
1.  **Add Identity Wipe:** Add a command to `scripts/m4-setup-second-vm.ps1` or the boot sequence to delete `/var/lib/minti/cland/*` on `minti-dev-2` to prevent `member_id` collisions.
2.  **Fix Test Sequence:** Insert a "Cluster Recovery" step after Test 10 (Revocation) to restore Member C before proceeding to Test 14 (Hard-kill), otherwise quorum will be lost.
3.  **SSH Key Rotation:** Add a step to regenerate SSH host keys on `minti-dev-2` to avoid `known_hosts` conflicts on the Windows host.