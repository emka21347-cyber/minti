# qwen3.6:latest - Distribution D0 installer-safety review

- wall_time_s: 32.4
- prompt_tokens: 10589
- eval_tokens: 3215
- raw_chars: 12878
- clean_chars: 12878
- extra_body: {'think': False}

---

Here is the safety review of the implementation plan for Milestone D.

### 1. Windows upgrade-over-live-service (D1) & Q1

**Verdict: Critical Risk in `LocalSystem` strategy; Moderate Risk in Upgrade Atomicity.**

**A. The `LocalSystem` / Least-Privilege Problem (Review Q1)**
The plan proposes running all three services as `LocalSystem`. This is **unsafe and architecturally weak**.
*   **Problem:** `LocalSystem` has unrestricted access to the host OS. If `minti-workspace` or `minti-runtime` has a memory corruption bug, a buffer overflow, or a logic error that allows arbitrary code execution (even via a malicious payload in a config file), the attacker gains `SYSTEM` privileges on the machine. This violates the principle of least privilege and contradicts the "hardening" posture claimed for Linux/macOS.
*   **The "Strict DACL" Fallacy:** The plan argues that because the state dir has strict DACLs, `LocalSystem` is needed to read it. This is a false dichotomy. You do not need `SYSTEM` to read a file; you only need `Read` permissions.
*   **Recommended Fix:** Create two dedicated virtual accounts (e.g., `MINTI-CLAND-SVC`, `MINTI-WORKSPACE-SVC`) or use the built-in `NT SERVICE\...` identities if using Windows 10/Server 2016+ with proper group policy.
    *   `Minti-Cland`: Runs as a dedicated service account with `SeServiceLogonRight`. Grant this account `Read/Write` to `%PROGRAMDATA%\MINTI\cland`.
    *   `Minti-Workspace`: Runs as a different dedicated service account. Grant this account `Read` access to `%PROGRAMDATA%\MINTI\cland` (specifically the `clan_key` and `identity.json`).
    *   **Why now?** Implementing this in D1 is trivial compared to retrofitting it later when users have already installed the stack. It eliminates the "all-or-nothing" blast radius of a workspace compromise.

**B. Upgrade Atomicity & Race Conditions (D1)**
The plan: *Stop services -> Swap binaries -> Refresh NSSM -> Start services.*
*   **Risk:** If the script crashes or is killed between `Stop-Service` and `Start-Service`, the machine has no running MINTI services but potentially mixed binary versions. While "re-run installer" is the recovery, this leaves a window of "broken state" where the user cannot interact with their Clan.
*   **Risk:** The plan relies on `nssm set` to update paths/configs. If the new binary version changes required CLI arguments or config schema, `nssm set` might not be enough; you might need `nssm edit` or a full remove/add cycle for certain parameters.
*   **Mitigation:** Ensure the installer checks for *running* processes of the *old* binaries before stopping them (to ensure clean handle release). The current "skip absent" logic is good, but verify that `Get-Service` status reflects the *new* binary version immediately after swap to prevent starting a stale service if the stop failed silently.

### 2. What gets written/started/opened per OS — completeness + honesty (Q2)

**A. Windows Registry & Environment**
*   **Missing:** The plan mentions `AppEnvironmentExtra` for PATH in NSSM. It does not explicitly state that the installer *removes* this environment variable on uninstall. If the uninstaller only removes services and binaries, the PATH entry remains in the registry (`HKLM\SYSTEM\CurrentControlSet\Services\Minti-Workspace\Environment`). This is a minor "dust" issue but should be cleaned.
*   **Event Log:** The plan mentions stdout/err logs. It does not mention writing to the Windows Event Log. For enterprise IT admins who monitor Event Logs, the absence of entries for "Install," "Uninstall," or "Service Start/Stop" makes troubleshooting hard. *Recommendation:* Add basic Event Log source registration ("MINTI") and write key lifecycle events (Install success/fail, Service Start/Stop).

**B. Firewall Posture**
*   **Windows:** `Private,Domain` is correct for LAN. However, if the user connects to a "Public" network (e.g., coffee shop), the port :7777 closes. This is safe but might confuse users expecting "Clan" to work everywhere. The plan should explicitly state: *"Port 7777 is only open on Private/Domain networks for security."*
*   **macOS:** "Nothing for loopback" is correct. However, the plan must ensure that `minti-workspace` (loopback) and `minti-cland` (LAN) are *not* blocked by macOS Application Firewall if the user has it enabled. Unsigned binaries often trigger a one-time prompt. The installer should handle this or document it clearly.

**C. Linux PATH & User**
*   **Honesty:** The plan states workspace finds `minti-cland` on PATH. It sets `/usr/local/bin` in the systemd unit. This is correct. However, ensure that the `minti` user's shell profile (if any) doesn't interfere with the service environment.

### 3. Uninstall semantics (Q6)

**A. Windows Firewall Rule**
*   **Problem:** The uninstaller removes the firewall rule. This is good. But does it handle the case where the rule was manually modified by an admin? `netsh advfirewall` delete might fail if the rule name doesn't match exactly. *Recommendation:* Use the rule's GUID or a more robust lookup than just name matching.

**B. Linux `minti` User**
*   **Problem:** The plan says default uninstall preserves `/var/lib/minti` and the `minti` user. This is correct for state preservation. However, if the user runs `--purge`, it removes the `minti` user. If other services on the system use UID 300-499 (as per M5-C comment), this could be risky. *Recommendation:* Ensure the uninstaller checks if the UID is used by any other service before deleting the user account.

**C. macOS LaunchDaemons**
*   **Problem:** The plan says "remove services + binaries." It does not explicitly mention removing the `com.minti.*.plist` files from `/Library/LaunchDaemons/`. If these remain, they will attempt to start on reboot and fail (binary missing), cluttering Console logs. *Recommendation:* Explicitly delete plist files in the uninstaller.

### 4. Ollama strategy (Q3 & Q8)

**A. Asymmetry Defensibility**
*   **Verdict:** The asymmetry (Linux auto-installs, Win/mac guide) is **defensible but risky**.
*   **Risk:** On Linux, `curl | sh` is a standard pattern, but it runs as root. If the official script changes or is compromised, the user's machine is owned. The plan adds `MINTI_NO_OLLAMA=1`, which is good.
*   **Win/mac Risk:** Probing `127.0.0.1:11434` and trusting whatever answers is a **security risk**. A malicious process could bind to 11434 and mimic Ollama's API version response to trick the installer into thinking Ollama is present when it isn't, or worse, intercept the handshake. *Recommendation:* Add a check for the specific Ollama user-agent or a known endpoint (e.g., `/api/tags`) that returns valid JSON structure, not just a port open.

**B. Linux Auto-Install Safety**
*   **Problem:** The plan says "auto-run the OFFICIAL ollama.com install script." This is a black box. If the script fails midway, MINTI might be installed but Ollama half-installed, leading to a broken state. *Recommendation:* Wrap the Ollama install in a try/catch block and verify Ollama is running post-install. If it fails, warn the user prominently that MINTI is installed but Ollama is missing.

### 5. Auto-open flow (Q4)

**A. Browser Opening from Elevated Context**
*   **Problem:** On Windows, `Start-Process -Verb RunAs` runs in an elevated session. Opening a browser from an elevated session can sometimes cause issues with user profile loading or security zones. *Recommendation:* Ensure the auto-open step drops elevation (runs as `$SUDO_USER` on Linux/macOS, or uses a non-elevated process on Windows) before opening the browser. The plan mentions this for Linux (`sudo -u $SUDO_USER`) but not explicitly for Windows. **Fix:** On Windows, after services start, spawn a *new* non-elevated PowerShell process to run `Start-Process http://127.0.0.1:8088`.

**B. Timeout Logic**
*   **Problem:** "Exit non-zero on timeout" is correct for the installer script. But if the browser opens (because it succeeded) and then the user closes it, does the installer wait? No, it exits. This is fine. However, if the workspace takes 20s to start (e.g., loading models), the auto-open will fail. *Recommendation:* Increase timeout to 30s or add a "Click here to open" button in the installer UI (if any) as a fallback.

### 6. Linux/macOS service wiring (Q7)

**A. systemd `IPAddressDeny=any` & Subprocesses**
*   **Problem:** The plan states: *"workspace shells the minti-cland CLI... which makes a loopback HMAC call."* It claims this survives `IPAddressDeny=any`. This is **correct** because `localhost` (127.0.0.1) is explicitly allowed by `IPAddressAllow=localhost`. However, ensure that the Go runtime's DNS resolution for `localhost` resolves to `127.0.0.1` and not an IPv6 address (`::1`) which might be blocked if not explicitly allowed. *Recommendation:* Explicitly set `IPAddressAllow=127.0.0.1,::1` or ensure the Go client forces IPv4 for localhost.

**B. macOS LaunchDaemon Ordering**
*   **Problem:** "Workspace up before cland -> demo data on first open." The plan says workspace shells `minti-cland`. If `minti-cland` is not running, the shell command fails, and workspace degrades to demo data. This is acceptable behavior (as per M1 contract). However, ensure that the workspace *retries* or *polls* for cland availability rather than failing once at startup. If it fails once and stays in "demo mode" until restart, the user experience is poor. *Recommendation:* Workspace should poll for `minti-cland` health every few seconds if it starts before cland.

### 7. Failure + rollback (Q5)

**A. Mid-Install Crash Windows**
*   **Problem:** "Binaries swapped but service config not refreshed." If the installer crashes after copying new binaries but before updating NSSM, the service will start the *new* binary with *old* config/args. This could cause a crash if the new binary requires new args. *Recommendation:* Use a two-phase commit: 1. Update NSSM config to point to new binary (with old args). 2. Copy binaries. 3. Restart service. Or, better: Stop service -> Copy binaries -> Update NSSM -> Start service. The current plan is "Stop -> Swap -> Refresh -> Start." This is safe *if* the new binary is compatible with the old config. If not, you need a migration step.

**B. Re-run Installer as Recovery**
*   **Problem:** The plan relies on "re-run installer" for recovery. This is good. But ensure that the installer detects a *partial* install (e.g., services exist but are in a bad state) and offers to repair, not just upgrade.

### 8. Site IA + trust (Q9)

**A. Trust Signals**
*   **Missing:** The plan mentions SHA-256 inline. This is good. But it does not mention **code signing**. Unsigned installers trigger SmartScreen/Gatekeeper warnings. *Recommendation:* Add a prominent note: *"Installers are currently unsigned to allow rapid iteration. You may see security warnings; please verify the SHA-256 hash before proceeding."* This manages expectations and reduces support tickets.

**B. Two-Door Split**
*   **Clarity:** The split is clear. Door A (ISO) for "Revive," Door B (Installer) for "Add." The badge on Door A ("live preview — disk installer coming") is honest. *Recommendation:* Ensure the link to Door A is disabled or clearly marked as "Coming Soon" until D4, so users don't click a broken link.

### 9. The elevation shim (Q2)

**A. Shim Acceptability**
*   **Verdict:** The `Install-MINTI.cmd` shim is **acceptable and necessary**. M5-B's refusal to auto-elevate was due to SmartScreen UX confusion. A double-clicked `.cmd` file that immediately prompts for UAC is a standard, expected pattern on Windows. It is *not* "auto-elevation" in the malicious sense; it's explicit user consent via UAC.
*   **Recommendation:** Ensure the `.cmd` file has a clear comment at the top explaining what it does, and that the UAC prompt clearly says "MINTI Installer" (if possible) or at least doesn't look suspicious.

### 10. What the plan got right and should NOT change

*   **Ollama Detect-or-Guide on Win/mac:** This is the correct safety posture. Never auto-install third-party dependencies silently.
*   **State Preservation by Default:** Crucial for user trust. Uninstallers that wipe state without warning are unacceptable.
*   **Loopback-Only Workspace:** Keeping the workspace loopback-only until auth is landed is a critical security decision. Do not change this.
*   **SHA-256 Verification of Bundled Binaries:** Essential for supply chain security. Keep this.
*   **Idempotent Re-run:** The installer should be able to run multiple times without breaking the system. This is well-planned.

---

### Final Verdict

The plan is largely sound but has one critical architectural flaw (`LocalSystem` privilege) and a few minor safety gaps (browser elevation, DNS resolution for localhost). These must be addressed before code is written.

VERDICT: build after addressing items 1A, 5A, 6A

---

## Raw (with thinking trace if any)

Here is the safety review of the implementation plan for Milestone D.

### 1. Windows upgrade-over-live-service (D1) & Q1

**Verdict: Critical Risk in `LocalSystem` strategy; Moderate Risk in Upgrade Atomicity.**

**A. The `LocalSystem` / Least-Privilege Problem (Review Q1)**
The plan proposes running all three services as `LocalSystem`. This is **unsafe and architecturally weak**.
*   **Problem:** `LocalSystem` has unrestricted access to the host OS. If `minti-workspace` or `minti-runtime` has a memory corruption bug, a buffer overflow, or a logic error that allows arbitrary code execution (even via a malicious payload in a config file), the attacker gains `SYSTEM` privileges on the machine. This violates the principle of least privilege and contradicts the "hardening" posture claimed for Linux/macOS.
*   **The "Strict DACL" Fallacy:** The plan argues that because the state dir has strict DACLs, `LocalSystem` is needed to read it. This is a false dichotomy. You do not need `SYSTEM` to read a file; you only need `Read` permissions.
*   **Recommended Fix:** Create two dedicated virtual accounts (e.g., `MINTI-CLAND-SVC`, `MINTI-WORKSPACE-SVC`) or use the built-in `NT SERVICE\...` identities if using Windows 10/Server 2016+ with proper group policy.
    *   `Minti-Cland`: Runs as a dedicated service account with `SeServiceLogonRight`. Grant this account `Read/Write` to `%PROGRAMDATA%\MINTI\cland`.
    *   `Minti-Workspace`: Runs as a different dedicated service account. Grant this account `Read` access to `%PROGRAMDATA%\MINTI\cland` (specifically the `clan_key` and `identity.json`).
    *   **Why now?** Implementing this in D1 is trivial compared to retrofitting it later when users have already installed the stack. It eliminates the "all-or-nothing" blast radius of a workspace compromise.

**B. Upgrade Atomicity & Race Conditions (D1)**
The plan: *Stop services -> Swap binaries -> Refresh NSSM -> Start services.*
*   **Risk:** If the script crashes or is killed between `Stop-Service` and `Start-Service`, the machine has no running MINTI services but potentially mixed binary versions. While "re-run installer" is the recovery, this leaves a window of "broken state" where the user cannot interact with their Clan.
*   **Risk:** The plan relies on `nssm set` to update paths/configs. If the new binary version changes required CLI arguments or config schema, `nssm set` might not be enough; you might need `nssm edit` or a full remove/add cycle for certain parameters.
*   **Mitigation:** Ensure the installer checks for *running* processes of the *old* binaries before stopping them (to ensure clean handle release). The current "skip absent" logic is good, but verify that `Get-Service` status reflects the *new* binary version immediately after swap to prevent starting a stale service if the stop failed silently.

### 2. What gets written/started/opened per OS — completeness + honesty (Q2)

**A. Windows Registry & Environment**
*   **Missing:** The plan mentions `AppEnvironmentExtra` for PATH in NSSM. It does not explicitly state that the installer *removes* this environment variable on uninstall. If the uninstaller only removes services and binaries, the PATH entry remains in the registry (`HKLM\SYSTEM\CurrentControlSet\Services\Minti-Workspace\Environment`). This is a minor "dust" issue but should be cleaned.
*   **Event Log:** The plan mentions stdout/err logs. It does not mention writing to the Windows Event Log. For enterprise IT admins who monitor Event Logs, the absence of entries for "Install," "Uninstall," or "Service Start/Stop" makes troubleshooting hard. *Recommendation:* Add basic Event Log source registration ("MINTI") and write key lifecycle events (Install success/fail, Service Start/Stop).

**B. Firewall Posture**
*   **Windows:** `Private,Domain` is correct for LAN. However, if the user connects to a "Public" network (e.g., coffee shop), the port :7777 closes. This is safe but might confuse users expecting "Clan" to work everywhere. The plan should explicitly state: *"Port 7777 is only open on Private/Domain networks for security."*
*   **macOS:** "Nothing for loopback" is correct. However, the plan must ensure that `minti-workspace` (loopback) and `minti-cland` (LAN) are *not* blocked by macOS Application Firewall if the user has it enabled. Unsigned binaries often trigger a one-time prompt. The installer should handle this or document it clearly.

**C. Linux PATH & User**
*   **Honesty:** The plan states workspace finds `minti-cland` on PATH. It sets `/usr/local/bin` in the systemd unit. This is correct. However, ensure that the `minti` user's shell profile (if any) doesn't interfere with the service environment.

### 3. Uninstall semantics (Q6)

**A. Windows Firewall Rule**
*   **Problem:** The uninstaller removes the firewall rule. This is good. But does it handle the case where the rule was manually modified by an admin? `netsh advfirewall` delete might fail if the rule name doesn't match exactly. *Recommendation:* Use the rule's GUID or a more robust lookup than just name matching.

**B. Linux `minti` User**
*   **Problem:** The plan says default uninstall preserves `/var/lib/minti` and the `minti` user. This is correct for state preservation. However, if the user runs `--purge`, it removes the `minti` user. If other services on the system use UID 300-499 (as per M5-C comment), this could be risky. *Recommendation:* Ensure the uninstaller checks if the UID is used by any other service before deleting the user account.

**C. macOS LaunchDaemons**
*   **Problem:** The plan says "remove services + binaries." It does not explicitly mention removing the `com.minti.*.plist` files from `/Library/LaunchDaemons/`. If these remain, they will attempt to start on reboot and fail (binary missing), cluttering Console logs. *Recommendation:* Explicitly delete plist files in the uninstaller.

### 4. Ollama strategy (Q3 & Q8)

**A. Asymmetry Defensibility**
*   **Verdict:** The asymmetry (Linux auto-installs, Win/mac guide) is **defensible but risky**.
*   **Risk:** On Linux, `curl | sh` is a standard pattern, but it runs as root. If the official script changes or is compromised, the user's machine is owned. The plan adds `MINTI_NO_OLLAMA=1`, which is good.
*   **Win/mac Risk:** Probing `127.0.0.1:11434` and trusting whatever answers is a **security risk**. A malicious process could bind to 11434 and mimic Ollama's API version response to trick the installer into thinking Ollama is present when it isn't, or worse, intercept the handshake. *Recommendation:* Add a check for the specific Ollama user-agent or a known endpoint (e.g., `/api/tags`) that returns valid JSON structure, not just a port open.

**B. Linux Auto-Install Safety**
*   **Problem:** The plan says "auto-run the OFFICIAL ollama.com install script." This is a black box. If the script fails midway, MINTI might be installed but Ollama half-installed, leading to a broken state. *Recommendation:* Wrap the Ollama install in a try/catch block and verify Ollama is running post-install. If it fails, warn the user prominently that MINTI is installed but Ollama is missing.

### 5. Auto-open flow (Q4)

**A. Browser Opening from Elevated Context**
*   **Problem:** On Windows, `Start-Process -Verb RunAs` runs in an elevated session. Opening a browser from an elevated session can sometimes cause issues with user profile loading or security zones. *Recommendation:* Ensure the auto-open step drops elevation (runs as `$SUDO_USER` on Linux/macOS, or uses a non-elevated process on Windows) before opening the browser. The plan mentions this for Linux (`sudo -u $SUDO_USER`) but not explicitly for Windows. **Fix:** On Windows, after services start, spawn a *new* non-elevated PowerShell process to run `Start-Process http://127.0.0.1:8088`.

**B. Timeout Logic**
*   **Problem:** "Exit non-zero on timeout" is correct for the installer script. But if the browser opens (because it succeeded) and then the user closes it, does the installer wait? No, it exits. This is fine. However, if the workspace takes 20s to start (e.g., loading models), the auto-open will fail. *Recommendation:* Increase timeout to 30s or add a "Click here to open" button in the installer UI (if any) as a fallback.

### 6. Linux/macOS service wiring (Q7)

**A. systemd `IPAddressDeny=any` & Subprocesses**
*   **Problem:** The plan states: *"workspace shells the minti-cland CLI... which makes a loopback HMAC call."* It claims this survives `IPAddressDeny=any`. This is **correct** because `localhost` (127.0.0.1) is explicitly allowed by `IPAddressAllow=localhost`. However, ensure that the Go runtime's DNS resolution for `localhost` resolves to `127.0.0.1` and not an IPv6 address (`::1`) which might be blocked if not explicitly allowed. *Recommendation:* Explicitly set `IPAddressAllow=127.0.0.1,::1` or ensure the Go client forces IPv4 for localhost.

**B. macOS LaunchDaemon Ordering**
*   **Problem:** "Workspace up before cland -> demo data on first open." The plan says workspace shells `minti-cland`. If `minti-cland` is not running, the shell command fails, and workspace degrades to demo data. This is acceptable behavior (as per M1 contract). However, ensure that the workspace *retries* or *polls* for cland availability rather than failing once at startup. If it fails once and stays in "demo mode" until restart, the user experience is poor. *Recommendation:* Workspace should poll for `minti-cland` health every few seconds if it starts before cland.

### 7. Failure + rollback (Q5)

**A. Mid-Install Crash Windows**
*   **Problem:** "Binaries swapped but service config not refreshed." If the installer crashes after copying new binaries but before updating NSSM, the service will start the *new* binary with *old* config/args. This could cause a crash if the new binary requires new args. *Recommendation:* Use a two-phase commit: 1. Update NSSM config to point to new binary (with old args). 2. Copy binaries. 3. Restart service. Or, better: Stop service -> Copy binaries -> Update NSSM -> Start service. The current plan is "Stop -> Swap -> Refresh -> Start." This is safe *if* the new binary is compatible with the old config. If not, you need a migration step.

**B. Re-run Installer as Recovery**
*   **Problem:** The plan relies on "re-run installer" for recovery. This is good. But ensure that the installer detects a *partial* install (e.g., services exist but are in a bad state) and offers to repair, not just upgrade.

### 8. Site IA + trust (Q9)

**A. Trust Signals**
*   **Missing:** The plan mentions SHA-256 inline. This is good. But it does not mention **code signing**. Unsigned installers trigger SmartScreen/Gatekeeper warnings. *Recommendation:* Add a prominent note: *"Installers are currently unsigned to allow rapid iteration. You may see security warnings; please verify the SHA-256 hash before proceeding."* This manages expectations and reduces support tickets.

**B. Two-Door Split**
*   **Clarity:** The split is clear. Door A (ISO) for "Revive," Door B (Installer) for "Add." The badge on Door A ("live preview — disk installer coming") is honest. *Recommendation:* Ensure the link to Door A is disabled or clearly marked as "Coming Soon" until D4, so users don't click a broken link.

### 9. The elevation shim (Q2)

**A. Shim Acceptability**
*   **Verdict:** The `Install-MINTI.cmd` shim is **acceptable and necessary**. M5-B's refusal to auto-elevate was due to SmartScreen UX confusion. A double-clicked `.cmd` file that immediately prompts for UAC is a standard, expected pattern on Windows. It is *not* "auto-elevation" in the malicious sense; it's explicit user consent via UAC.
*   **Recommendation:** Ensure the `.cmd` file has a clear comment at the top explaining what it does, and that the UAC prompt clearly says "MINTI Installer" (if possible) or at least doesn't look suspicious.

### 10. What the plan got right and should NOT change

*   **Ollama Detect-or-Guide on Win/mac:** This is the correct safety posture. Never auto-install third-party dependencies silently.
*   **State Preservation by Default:** Crucial for user trust. Uninstallers that wipe state without warning are unacceptable.
*   **Loopback-Only Workspace:** Keeping the workspace loopback-only until auth is landed is a critical security decision. Do not change this.
*   **SHA-256 Verification of Bundled Binaries:** Essential for supply chain security. Keep this.
*   **Idempotent Re-run:** The installer should be able to run multiple times without breaking the system. This is well-planned.

---

### Final Verdict

The plan is largely sound but has one critical architectural flaw (`LocalSystem` privilege) and a few minor safety gaps (browser elevation, DNS resolution for localhost). These must be addressed before code is written.

VERDICT: build after addressing items 1A, 5A, 6A