# MINTI Clan Workspace — v1 Plan (DRAFT, pre-review)

> The human-facing front door to a MINTI Clan. Becomes the **flagship** MINTI
> interface (opencode demoted to optional power-tool pack). Self-hosted AI
> workspace in the spirit of Odysseus, but Clan-native: it doesn't just chat —
> it shows and operates the distributed mesh.

## Locked decisions (user, 2026-06-07)
- **Surface:** Full AI workspace — web UI served by each node (not just a TUI).
- **Scope (all four):** Clan visibility · knock approval + invites · chat against the Clan · addon/model management.
- **Reach:** LAN — each node serves its own UI on its LAN address; auth required.
- **Phase order:** **Visibility first**, then knocks, then chat, then addons.
- **Chat history:** Ephemeral, but persists *as long as the Clan exists* — including a 1-node Clan that's the last survivor of a bigger one. So: tie session lifetime to Clan identity, not to the browser and not to a permanent DB.
- **Mobile:** Responsive + PWA manifest (installable, LAN-reachable from phone). Offline SW deferred.
- **Positioning:** Workspace becomes flagship; opencode stays bundled but optional.

## Architecture
- **New Go service `minti-workspace`** (own module, mirrors `cland`/`status` layout). Single static binary, cross-compiles linux/windows/darwin. Serves:
  - Embedded static SPA (`embed.FS`) — no node/npm at runtime, no build step on the box.
  - JSON API + SSE under `/api/*`.
  - Reuses the `status` repo's **probes** (clan, runtime, sysinfo, addons) verbatim — they already shell `minti-cland --json` + read Ollama. Promote `status/internal/probes` → a shared `internal/probes` package both binaries import (avoids fork).
- **Frontend:** vanilla TS + Vite → built to static assets, committed/embedded. No SPA framework for v1 (keeps it tiny, themeable, dependency-light). Brand: cyan nodes, mint single-focus accent, deep-black bg — straight from `docs/brand.md`. 9-node mesh as the live network view, not just a logo.
- **Auth:** token gate derived from the Clan key. A node already holds `clan_key`; `/api/auth` issues a short-lived bearer after the user proves possession of a **workspace PIN** printed by `minti-workspace` on first run (and surfaced via `minti-cland`/fetch). LAN-exposed → must not be open. Localhost stays unauthenticated only for the loopback `/healthz`.
- **Chat lifetime = Clan lifetime (REVISED post-review):** sessions stored **on disk** at `/var/lib/minti/workspace/sessions/<clan_id>/*.jsonl` (not tmpfs). Keyed by `clan_id`. On start, workspace reads local `clan.json`; sessions whose `clan_id` matches are reattached, sessions for any *other* clan_id are pruned. Result: history **survives reboot while the Clan lives** (incl. a 1-node last-survivor), and is GC'd the moment the node leaves/destroys the Clan or joins a different one. No vector store in v1. (Reviewer + user both pushed off tmpfs — tmpfs would lose history on reboot, contradicting "survives as long as the Clan exists".)
- **Orchestrator failover mid-use (review gap):** the leader holding the lease can change at any moment. Workspace chat proxies to "current Orchestrator"; on a 502/disconnect it re-resolves the Orchestrator (it already probes election state) and **reconnects the SSE stream to the new leader** rather than wedging. UI shows a brief "Orchestrator changed → reconnecting" toast. Visibility view already reflects the new leader via the probe push.

## Security (LAN-exposed, mutating API — review-driven)
- **Bind:** default to the Clan/LAN interface address only (not `0.0.0.0`), configurable; never bind public.
- **Auth:** workspace prints a **PIN** on first run (also surfaced by `minti-fetch`); `/api/auth` exchanges PIN → short-lived **bearer (TTL ~12h)**, refreshable. Bearer sent in `Authorization` header.
- **CSRF:** bearer-in-header (not cookie) + strict same-origin / `Origin`+`Host` check on every state-changing request. No ambient cookie auth → CSRF surface stays closed.
- **Knock/SAS:** accept-knock requires re-confirming the SAS in-UI (load-bearing, mirrors M8 threat model). No "accept all".
- **Resource guard (1–2GB boxes):** cap concurrent SSE clients (e.g. 8), single shared probe ticker fanned out to all clients (don't probe per-connection), backpressure-drop stale frames.

## Phases
| Phase | Deliverable | Notes |
|---|---|---|
| **A — Visibility** | `minti-workspace` binary + embedded SPA shell + auth + **live Clan mesh view**: members, Orchestrator, term/lease, scores, election history, runtime/VRAM, addons. SSE-pushed. | Fastest to real pixels — reuses status probes. The 9-node mesh animates with real peers. |
| **B — Knocks + invites** | Operator surface: pending knocks with SAS render, accept/deny; mint invite token w/ copy-paste join cmd + QR. First mutations. | Mirrors M8 `knock`/`knock-accept` + M7.6 invite. SAS confirm is load-bearing — explicit confirm step. |
| **C — Chat** | Chat panel → `minti-runtime` `/v1/messages` SSE, routed to Orchestrator's model. Model picker (from runtime `/v1/models`). Ephemeral-per-clan history. | Makes the Clan *usable*. Streams tokens. |
| **D — Addons** | Cookbook view: installed packs, VRAM-fit hints, trigger `minti-pack-fetch` (hermes3/mistral/wiki) with live download progress over SSE. | Wraps existing pack-fetch; progress via stderr parse. |

## Integration / packaging
- New `make workspace*` targets; `minti-workspace.service` (hardened like cland, but needs LAN bind + the chosen port). Add to `install.sh` + the live-ISO chroot staging. Surface the workspace URL+PIN in `minti-fetch`.
- Positioning: README + install epilogue lead with the workspace; opencode noted as optional. **Don't rip opencode out in v1** — just reposition.

## Out of scope for v1 (explicit)
Vector/RAG memory · email/calendar/tasks · multi-user accounts · WAN/tunnel exposure · offline service worker · document editor · image editing.

## Open questions — resolved after deepseek-r1:32b review (2026-06-07)
1. **Auth** → PIN→short-lived bearer in header + same-origin/CSRF check + LAN-iface bind. RESOLVED (keep PIN; reviewer's "too complex" rejected — a gateless mutating LAN API is the real risk).
2. **Probe sharing** → extract to a shared `internal/probes` module behind a stable interface; both binaries import. RESOLVED.
3. **Chat lifetime** → on-disk under `/var/lib/minti/workspace/sessions/<clan_id>/`, pruned by clan_id mismatch. Survives reboot while Clan lives. RESOLVED.
4. **Frontend** → vanilla TS for v1; reassess a small framework only if Phase C/D friction shows up. RESOLVED (defer).

## Round-2 user decisions (2026-06-07)
- **Chat reboot-survival confirmed:** last-survivor node keeps history across reboot → the on-disk `/var/lib/minti/workspace/sessions/<clan_id>/` design stands.
- **opencode:** reposition-only in v1 (nothing removed). Deeper direction the user flagged: **Hermes-as-harness** — local Hermes 3 as the executing agent, *optionally orchestrated by a BYO Claude model* (P1-compliant: proprietary optional, never required). Mirrors the project's own three-tier delegation. Scoped as a post-v1 phase (Phase E candidate: "Agent mode"), NOT v1 chat.
- **Mock-first workflow:** lock the UI as a clickable HTML mock (fake data, real interactions) reviewed in preview BEFORE writing the Go/TS implementation. Mock lives at `workspace/mock/index.html` and becomes the visual/interaction reference for the real frontend.
- **WAN Clans** ("one machine somewhere else, not same WLAN"): explicitly **out of v1 scope** — it's cland protocol work (NAT traversal / WireGuard-class tunnel or relay for remote members), its own milestone. The workspace UI must not assume same-subnet anywhere (use addresses from cland verbatim — it already does).
- **Download/landing site** (click → download ISO): parked follow-on; reuses the mock's design language.
