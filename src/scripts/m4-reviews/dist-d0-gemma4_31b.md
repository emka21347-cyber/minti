# gemma4:31b - Distribution D0 installer-safety review

- wall_time_s: 78.0
- prompt_tokens: 11097
- eval_tokens: 3235
- raw_chars: 5377
- clean_chars: 5377
- extra_body: {}

---

This review focuses on concrete safety and reliability risks in the Distribution milestone "D" plan.

### 1. Windows Upgrade-over-Live-Service (D1)
**Risk: Low.** The plan follows the M5-B precedent which is already live on your machine. 
*   **NSSM Copies (Q1):** Per-service copies are the correct call. At ~330KB, the overhead is negligible, and it prevents a "dependency hell" where updating one service's NSSM config or binary version accidentally breaks another. It ensures total isolation between the three services.
*   **LocalSystem (Q1):** While not "least-privilege," it is the only pragmatic choice here. Since the Workspace must shell the `minti-cland` CLI to read a strict-DACL state directory, any other account would require complex SID management in the installer, increasing the failure surface. Because the Workspace and Runtime are bound strictly to loopback, the attack surface for LocalSystem is minimized.
*   **Race Conditions:** The "Stop $\to$ Swap $\to$ Start" flow is sound. Since you are not using `nssm remove`, there is no risk of losing service identity or triggering SCM delays associated with deleting/re-creating services.

### 2. Completeness + Honesty (Writes/Starts/Opens)
**Problem: macOS Gatekeeper / Windows SmartScreen.**
The plan mentions "honesty" on the site, but it misses a critical UX reality: **unsigned binaries will be blocked by default.**
*   On macOS, `xattr -d com.apple.quarantine` is mentioned in M5-C for some paths, but the installer must ensure this is applied to all three bundled binaries, or the user will be trapped in "App cannot be opened" loops.
*   **Site IA:** The site MUST explicitly tell users: *"You will see a security warning because these binaries are not yet signed; click 'Run anyway' / 'Open Anyway'."* Pretending it is a seamless "one-click" when the OS throws a red warning destroys trust.

### 3. Uninstall Semantics
**Verdict: Looks good.**
The preserve-state default is the correct safety posture for Clan identity. The explicit `-Purge` flag with a printed warning handles the destructive case honestly. No hidden destructiveness found.

### 4. Ollama Strategy (Review Q3, Q8)
**Verdict: Defensible.**
*   **Asymmetry:** The Linux auto-install is defensible because it preserves the validated M0 path and ISO chroot hook. As long as the site explicitly lists "Auto-installs Ollama" for Linux and "Guides you to install Ollama" for Win/Mac, it is honest.
*   **Probing (Q8):** Probing `127.0.0.1:11434` is low risk. It is used only as a discovery mechanism to avoid redundant installation prompts. The actual communication with Ollama is handled by the runtime-adapter, which has its own health checks.

### 5. Auto-open Flow (Review Q4)
**Risk: Low.**
*   **Context:** Using `Start-Process` / `xdg-open as $SUDO_USER` correctly avoids the "browser opened as root" problem.
*   **Failure Mode:** If the browser opens but the stack is partially broken (e.g., Workspace is up, but Runtime is down), the user sees a "demo mode" or an error page in the UI. This is preferable to a silent failure or a hanging installer. 
*   **Exit Code:** Exit non-zero on timeout is correct; it signals that the "polish" failed and directs the user to the logs.

### 6. Linux/macOS Service Wiring (Review Q7)
**Risk: Low.**
*   **Hardening:** `IPAddressDeny=any` + `IPAddressAllow=localhost` will NOT break the shelled `minti-cland` CLI because loopback is explicitly permitted. 
*   **Go Runtime:** Standard Go binaries do not require W+X memory; `MemoryDenyWriteExecute` should not interfere with the runtime or the workspace.
*   **macOS Ordering:** If Workspace starts before Cland, it will simply show demo data until a refresh occurs. This is acceptable for a background daemon boot sequence.

### 7. Failure + Rollback
**Verdict: Looks good.**
The idempotency of the installer (re-run to fix) is sufficient here. Because you are swapping files and refreshing configs rather than performing complex database migrations, there is no need for transactional "rollback" logic that would likely introduce more bugs than it solves.

### 8. Site IA + Trust (Review Q9)
**Problem: Missing "Reality Check" section.**
The trust signals (SHA-256, disclosure of paths/ports) are excellent. However, to truly earn trust, the site needs a **"Known Warnings"** section:
1.  Explicitly mention the lack of code-signing and how to bypass the OS warnings.
2.  Mention minimum hardware requirements (RAM) for the LLM side, so users don't think MINTI "broke" their machine when it swaps to disk.

### 9. The Elevation Shim (Review Q2)
**Verdict: Use the shim.**
A `.cmd` shim is significantly more user-friendly than a README instruction for an elevated shell. While it remains MOTW-flagged, it reduces the friction of "Door B" from a technical chore to a standard installation flow.

### 10. What the plan got right (Do NOT change)
*   **Loopback restriction:** Keeping Workspace/Runtime on `127.0.0.1` until auth lands is a critical safety win.
*   **State preservation:** Defaulting to preserve identity during uninstall prevents accidental "Clan exile."
*   **Disclosure:** The "what this installs" section on the site is exactly what power users need to trust an installer.

***

**VERDICT: build after addressing items 2 and 8 (explicitly document unsigned binary warnings on the site and in the macOS install script).**

---

## Raw (with thinking trace if any)

This review focuses on concrete safety and reliability risks in the Distribution milestone "D" plan.

### 1. Windows Upgrade-over-Live-Service (D1)
**Risk: Low.** The plan follows the M5-B precedent which is already live on your machine. 
*   **NSSM Copies (Q1):** Per-service copies are the correct call. At ~330KB, the overhead is negligible, and it prevents a "dependency hell" where updating one service's NSSM config or binary version accidentally breaks another. It ensures total isolation between the three services.
*   **LocalSystem (Q1):** While not "least-privilege," it is the only pragmatic choice here. Since the Workspace must shell the `minti-cland` CLI to read a strict-DACL state directory, any other account would require complex SID management in the installer, increasing the failure surface. Because the Workspace and Runtime are bound strictly to loopback, the attack surface for LocalSystem is minimized.
*   **Race Conditions:** The "Stop $\to$ Swap $\to$ Start" flow is sound. Since you are not using `nssm remove`, there is no risk of losing service identity or triggering SCM delays associated with deleting/re-creating services.

### 2. Completeness + Honesty (Writes/Starts/Opens)
**Problem: macOS Gatekeeper / Windows SmartScreen.**
The plan mentions "honesty" on the site, but it misses a critical UX reality: **unsigned binaries will be blocked by default.**
*   On macOS, `xattr -d com.apple.quarantine` is mentioned in M5-C for some paths, but the installer must ensure this is applied to all three bundled binaries, or the user will be trapped in "App cannot be opened" loops.
*   **Site IA:** The site MUST explicitly tell users: *"You will see a security warning because these binaries are not yet signed; click 'Run anyway' / 'Open Anyway'."* Pretending it is a seamless "one-click" when the OS throws a red warning destroys trust.

### 3. Uninstall Semantics
**Verdict: Looks good.**
The preserve-state default is the correct safety posture for Clan identity. The explicit `-Purge` flag with a printed warning handles the destructive case honestly. No hidden destructiveness found.

### 4. Ollama Strategy (Review Q3, Q8)
**Verdict: Defensible.**
*   **Asymmetry:** The Linux auto-install is defensible because it preserves the validated M0 path and ISO chroot hook. As long as the site explicitly lists "Auto-installs Ollama" for Linux and "Guides you to install Ollama" for Win/Mac, it is honest.
*   **Probing (Q8):** Probing `127.0.0.1:11434` is low risk. It is used only as a discovery mechanism to avoid redundant installation prompts. The actual communication with Ollama is handled by the runtime-adapter, which has its own health checks.

### 5. Auto-open Flow (Review Q4)
**Risk: Low.**
*   **Context:** Using `Start-Process` / `xdg-open as $SUDO_USER` correctly avoids the "browser opened as root" problem.
*   **Failure Mode:** If the browser opens but the stack is partially broken (e.g., Workspace is up, but Runtime is down), the user sees a "demo mode" or an error page in the UI. This is preferable to a silent failure or a hanging installer. 
*   **Exit Code:** Exit non-zero on timeout is correct; it signals that the "polish" failed and directs the user to the logs.

### 6. Linux/macOS Service Wiring (Review Q7)
**Risk: Low.**
*   **Hardening:** `IPAddressDeny=any` + `IPAddressAllow=localhost` will NOT break the shelled `minti-cland` CLI because loopback is explicitly permitted. 
*   **Go Runtime:** Standard Go binaries do not require W+X memory; `MemoryDenyWriteExecute` should not interfere with the runtime or the workspace.
*   **macOS Ordering:** If Workspace starts before Cland, it will simply show demo data until a refresh occurs. This is acceptable for a background daemon boot sequence.

### 7. Failure + Rollback
**Verdict: Looks good.**
The idempotency of the installer (re-run to fix) is sufficient here. Because you are swapping files and refreshing configs rather than performing complex database migrations, there is no need for transactional "rollback" logic that would likely introduce more bugs than it solves.

### 8. Site IA + Trust (Review Q9)
**Problem: Missing "Reality Check" section.**
The trust signals (SHA-256, disclosure of paths/ports) are excellent. However, to truly earn trust, the site needs a **"Known Warnings"** section:
1.  Explicitly mention the lack of code-signing and how to bypass the OS warnings.
2.  Mention minimum hardware requirements (RAM) for the LLM side, so users don't think MINTI "broke" their machine when it swaps to disk.

### 9. The Elevation Shim (Review Q2)
**Verdict: Use the shim.**
A `.cmd` shim is significantly more user-friendly than a README instruction for an elevated shell. While it remains MOTW-flagged, it reduces the friction of "Door B" from a technical chore to a standard installation flow.

### 10. What the plan got right (Do NOT change)
*   **Loopback restriction:** Keeping Workspace/Runtime on `127.0.0.1` until auth lands is a critical safety win.
*   **State preservation:** Defaulting to preserve identity during uninstall prevents accidental "Clan exile."
*   **Disclosure:** The "what this installs" section on the site is exactly what power users need to trust an installer.

***

**VERDICT: build after addressing items 2 and 8 (explicitly document unsigned binary warnings on the site and in the macOS install script).**