# Dist D0 — 3-LLM review triage (2026-06-11)

Reviews: `dist-d0-qwen3.6_latest.md` (3215 tok, deep + specific),
`dist-d0-deepseek-r1_32b.md` (1111 tok — skipped its thinking phase this
round, generic corroboration only, no unique finds), `dist-d0-gemma4_31b.md`
(3235 tok, the dissenting voice on least-privilege and the unsigned-binary
blocker). Verdicts: qwen "build after 1A/5A/6A", deepseek "build after 1-9"
(blanket), gemma "build after 2+8".

## Folded (changes made to the D-milestone plan)

| # | Finding | Who | Fold |
|---|---|---|---|
| F1 | LocalSystem for all three services is excessive; the two loopback daemons don't need it | qwen (critical) + deepseek; gemma dissents ("pragmatic, loopback-bound") | **Split along the trust boundary.** `Minti-Runtime` + `Minti-Workspace` run as `NT SERVICE\<name>` virtual accounts (no password mgmt; `sc.exe config obj=` after NSSM registration — NSSM's ObjectName set wants a password arg, sc doesn't). icacls grants: runtime = Modify on its own state dir; workspace = Modify on its own state dir + **Read** on the cland state dir (the shelled CLI reads identity/clan_key/cland.yaml; it writes nothing — if D1 verification proves otherwise the fallback is a Modify grant on `audit.jsonl` only, not LocalSystem). `Minti-Cland` STAYS LocalSystem: it's the live M5-B service being upgraded in place, its mDNS/firewall/Defender behavior is proven under that account, and an ObjectName flip mid-upgrade on the daily driver is exactly the class of surprise D1 must not have. Blast-radius honesty: workspace compromise under the virtual account yields clan_key read (Clan-scoped, recoverable via key rotation) — not SYSTEM. gemma's failure-surface concern is acknowledged: the grant dance is 3 commands, and the daily-driver verification covers it. |
| F2 | Unsigned binaries: SmartScreen/Gatekeeper WILL warn; pretending one-click while the OS shows red destroys trust; macOS quarantine must be cleared on ALL bundled binaries | gemma (blocker) + qwen 8A + deepseek 8 — 3/3 consensus | Site IA gains a **"Known warnings" section**: unsigned status stated plainly, the exact SmartScreen ("More info → Run anyway") and Gatekeeper ("Open Anyway") paths, verify-SHA-256-first framing, and minimum hardware expectations (RAM per model so users don't read swap-death as "MINTI broke my machine"). macOS installer runs `xattr -d com.apple.quarantine` on **all three** binaries (M5-C did it for one). Windows README mirrors the SmartScreen steps. |
| F3 | Browser auto-open from the elevated session inherits elevation | qwen 5A + deepseek 5 | Windows opens the URL via `explorer.exe "http://127.0.0.1:8088"` — explorer marshals it to the desktop user's medium-IL context. Linux/mac were already de-elevated (`sudo -u $SUDO_USER xdg-open` / `open` as console user). |
| F4 | `IPAddressAllow=localhost` vs IPv6 `::1` ambiguity | qwen 6A | systemd's `localhost` special value covers `127.0.0.0/8` + `::1/128` (systemd.resource-control(5)) — but the unit now says it explicitly: `IPAddressAllow=127.0.0.0/8 ::1/128`. Zero cost, no doubt. |
| F5 | Port-probe 11434 trusts whatever answers | qwen 4A | Detection parses `/api/version` for a JSON `version` field instead of trusting an open port. Stays advisory-only (nothing is ever piped into a detected Ollama; worst case is wrong guidance text). |
| F6 | Linux Ollama step is fatal under `set -e` — a flaky ollama.com download aborts the whole install | qwen 4B (real find — current install.sh behavior) | D2 wraps the Ollama block: **non-fatal on door-B installs** (warn + continue + summary line "Ollama: FAILED — install manually"; runtime tolerates absence by design) but **still fatal under `MINTI_CHROOT=1`** (an ISO silently built without Ollama is worse than a failed build). |
| F7 | Auto-open poll 15 s is tight | qwen 5B | 30 s. Trivial insurance; on timeout behavior unchanged (print URL + status + logs, exit non-zero). |
| F8 | Firewall-rule removal by DisplayName misses an admin-renamed rule | qwen 3A | Uninstaller removes by DisplayName **and** sweeps inbound rules whose Program == our installed exe path. |
| F9 | macOS uninstall must explicitly remove all three plists (orphan plists respawn-fail at boot) | qwen 3C | Plan text now explicit: bootout + delete `com.minti.{cland,runtime,workspace}.plist` in both system + per-user branches (M5-C already did this for one service; now stated for three). |
| F10 | Mid-install crash window (binaries swapped, NSSM config not yet refreshed) deserves explicit documentation | qwen 7A/1B (partial) | Plan documents the window: services are stopped during it, `AppParameters` are version-stable across this upgrade, and re-running the installer is the designed repair. No transactional machinery (gemma 7 concurs it would add more bugs than it solves). |

## Overruled (with reasons)

| # | Claim | Who | Why overruled |
|---|---|---|---|
| O1 | Uninstaller must scrub the NSSM `AppEnvironmentExtra` PATH entry from the registry | qwen 2A | `nssm remove` / `sc delete` deletes the entire `HKLM\SYSTEM\CurrentControlSet\Services\<name>` key — AppEnvironmentExtra lives inside it. Nothing survives. |
| O2 | Register a Windows Event Log source + write install/lifecycle events | qwen 2A | NSSM already writes service start/stop/crash events to the Application log. A MINTI event source is enterprise polish, not v1 safety — deferred to the code-signing milestone. |
| O3 | Workspace may stick in demo mode if it boots before cland | qwen 6B | Verified against code: `clan.Probe()` runs per-request (`exec.LookPath` + exec on every `/api/mesh` call — workspace/internal/clan/clan.go:54). It self-heals the moment cland answers. gemma 6 read it correctly. |
| O4 | `nssm set` may not suffice; might need `nssm edit` or remove/re-add | qwen 1B | `nssm edit` is the GUI dialog. Every scriptable parameter is settable via `nssm set`, and the installer re-sets every parameter on every run — that IS the full refresh. Args are version-stable here; remove/re-add is exactly what the plan avoids (service identity + SCM churn). |
| O5 | Linux `--purge` userdel risks colliding with other services' UIDs (300–499) | qwen 3B | Conflates the macOS dscl UID-scan recipe with Linux. Linux removes by NAME (`userdel minti`), a system user our installer created; no UID-range logic exists on that path. |
| O6 | Stop order "reverse" could leave inconsistent state | deepseek 1 | The order IS dependency-correct: consumers stop first (workspace → runtime → cland), providers start first (cland → runtime → workspace). No concrete failure scenario offered. |
| O7 | Harmonize the Ollama strategy across all three OSes | deepseek 4 | Harmonizing means either silently running third-party installers on Win/mac (violates the safety bar) or regressing the validated Linux M0 path + ISO chroot hook. gemma 4 endorses the asymmetry as defensible *because the site states it per-OS* — which F2's disclosure work now guarantees. |
| O8 | Installer should detect partial installs and offer a "repair" mode | qwen 7B | Idempotent re-run IS repair — every step is check-then-fix. A named repair mode adds UI surface with zero new capability. |

## Resolved questions (asked in the plan, answered by the panel)

- **Q1 NSSM copies**: per-service copies endorsed (gemma: isolation; deepseek: "redundant but safer"). Kept.
- **Q1 service accounts**: split per F1.
- **Q2 elevation shim**: ship it — qwen "acceptable and necessary; a double-clicked .cmd → UAC is the standard Windows pattern", gemma "use the shim". The .ps1 one-liner stays the documented canonical path; the shim gets a header comment explaining what it does.
- **Q3 Ollama postures**: defensible as-is (gemma), with F2/F5/F6 refinements.
- **Q4 auto-open**: sound with F3/F7; gemma confirms exit-nonzero-on-timeout is right.
- **Q7 unit hardening**: child CLI survives (gemma + qwen agree given F4); `MemoryDenyWriteExecute` is fine for pure-Go binaries (gemma).
- **Q9 site trust**: strong baseline; F2's Known-warnings section was the missing piece (gemma).
- **Q10 do-not-change list** (3/3): loopback-only workspace/runtime, state-preserving uninstall default, SHA-256 verification of bundled binaries, idempotent re-run, the "what this installs" disclosure.

## Reviewer-quality note

deepseek-r1:32b emitted no `<think>` trace and returned generic
corroboration (~2.5 KB) — unusual for it; treated as a weak third vote, not
an independent reviewer this round. qwen3.6 (think:false) and gemma4 carried
the review. If a future load-bearing review needs three strong voices,
re-prompt deepseek or swap in a re-roll.
