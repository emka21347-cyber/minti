# CLAUDE.md — how we work on MINTI (rework era)

## Orientation

- **Product truth:** `PRD.md` (root). Don't relitigate its locked decisions
  without strong cause.
- **Session log:** `STATUS.md` (append-only, newest first).
- **Current plan:** `docs/plans/rework.md`.
- **Everything pre-2026-07-17:** `archive/` — a parts bin, not a graveyard.
  The Go stack in there still builds; its binaries ship onto the new ISO.
  Old chronicle: `archive/STATUS.md`, old docs: `archive/DOCUMENTATION.md`.

## The core loop (what this project is)

ISO version → boot it (VM first, then real laptop) → the founder talks to the
on-device agent → distill feedback → change the UX → next version. Flashable
versions are logged in `CHANGELOG.md`.

## Session rituals

**Start:**
1. Read `PRD.md`, then the newest `STATUS.md` entry.
2. Pre-flight the machine before heavy work (user rule: "always check if
   something is going on so no problems and no spilling"):
   `nvidia-smi`, running daemons (ollama / minti-runtime / VirtualBox),
   free RAM. Skip only for trivial follow-up edits.

**End (the handover):**
1. Append a `STATUS.md` entry: Done / Next / Blockers-notes.
2. Update `PRD.md` if scope or a locked decision changed.
3. Update agent memory (cross-session facts that aren't derivable from the
   repo).
4. Leave the tree committed. Do not push without deploy consent (below).

## Model split (user rule)

Plan in **Fable 5**, execute in **Opus 4.8**. The agent cannot switch models —
remind the user to `/model` at the plan→execute boundary.

## Delegation tiers (user rule)

| Tier | Who | What |
|---|---|---|
| T1 | user | architecture, scope, milestone-boundary reviews |
| T2 | Claude | task breakdown, hard code (distributed systems, security, multi-file), review of T3 output |
| T3 | local LLMs via Ollama (user's box) | boilerplate, single-file code with a tight spec |

T3-delegable only if: self-contained (<30-line spec), quick to review, low
blast radius, not on the security or distributed-systems critical path.
T3 output gets hostile review — a past round shipped 4 silent HTML bugs.

## Deploy safety

- The repo is **public**: origin `github.com/emka21347-cyber/minti`.
- Vercel project `minti` auto-deploys `site/` → https://minti-pi.vercel.app.
- **Pushing `main` is a prod deploy.** Needs explicit user consent each time.

## Build environment carry-forwards

- ISO lessons (umask 022, explicit firmware list, EFI requirement, prebuilt
  `wl.ko`, no dkms toolchain on the image): `iso/README.md`, and the war
  stories in `archive/lbconfig/` + agent memory `reference-live-build`.
- Windows host quirks (PowerShell 5.1 traps, Go path, curl-for-SSE):
  agent memory `reference-dev-environment`.
- Old build VM is VirtualBox `minti-dev-2` — identify by MAC, not IP.
