# Clan Protocol Specification — v0.1

> Status: **Draft**, v1 implementation target.
> Source of truth for the on-wire behavior of `cland` (Linux) and the cross-platform Clan Agent (Windows / macOS).
> This document defines the protocol; the PRD ([../../../../.claude/plans/hello-can-we-create-abundant-hopper.md](../../../../.claude/plans/hello-can-we-create-abundant-hopper.md)) defines the product. Where they conflict, the PRD wins on intent and this document wins on wire details.

---

## 1. Glossary

| Term | Meaning |
|---|---|
| **Clan** | A mutually-consenting trust group of machines that share AI inference, agent work, and tool execution. Identified by `clan_id` (UUIDv4) and authenticated by `clan_key` (32 random bytes). |
| **Member** | A machine running `cland` (Linux) or the Clan Agent (Win/Mac) that has been admitted to a Clan. Identified by `member_id` (UUIDv4, generated at first run). |
| **Orchestrator** | The member currently holding the lease (§5). Drives routing decisions. Exactly one at a time per Clan, plus brief overlap during failover. |
| **Worker** | A member without the lease that contributes one or more non-`reasoning` capabilities. |
| **Term** | A monotonically-increasing integer that identifies one continuous period of Orchestrator leadership. Increments on every successful election. |
| **`reasoning_score`** | 0–100 integer per backend; comes from the model rubric (§6.3). Drives Orchestrator election only. |
| **`system_score`** | 0–100 integer per node; derived from hardware probe (§6.4). Drives worker routing. |

---

## 2. Identity & Trust

### 2.1 Member identity

On first run, a member generates:

- `member_id`: UUIDv4, persisted in `~/.minti/identity.json` (Linux/macOS) or `%APPDATA%\MINTI\identity.json` (Windows).
- `member_keypair`: Ed25519. Public key advertised; private key never leaves the member. *(Used for v2 mTLS migration; in v1 we still rely on the shared Clan Key for authentication, but per-member keys are generated up-front so v2 doesn't require re-keying.)*

### 2.2 Clan identity

A Clan is described by:

- `clan_id`: UUIDv4.
- `clan_key`: 32 random bytes (HMAC-SHA256 key for v1 message authentication).
- `clan_cert`: self-signed X.509 certificate held by the founding member; trusted via pinning on every subsequent join.

The Clan's first member generates all three during the `clan create` flow. Subsequent members receive them through an **invite token** (§3.2) or **paste-key** (§3.3).

### 2.3 Transport authentication

All inter-member messages travel over **HTTPS** to the destination's Clan endpoint (default port `7777`). The TLS server presents `clan_cert`; the client pins it via the public-key hash captured at join. **No public CA is involved.**

Every message also carries an HMAC over `(method, path, body, timestamp, nonce)`:

```
X-Minti-Member: <member_id>
X-Minti-Timestamp: <unix-millis>
X-Minti-Nonce: <16 random hex bytes>
X-Minti-HMAC: <hex hmac-sha256 using clan_key>
```

Receivers verify:

1. Timestamp is within ±60s of local clock.
2. `(member_id, nonce)` has not been seen in the last 5 minutes (replay protection).
3. HMAC matches.

Failed verification → 401, no body. The audit log records the attempt.

---

## 3. Membership

### 3.1 States

```
unaffiliated --(create)--> active (founder)
unaffiliated --(invite + admit)--> admitted --(handshake)--> active
admitted --(timeout 24h)--> unaffiliated
active --(revoke)--> revoked --(48h grace)--> purged
active --(self-leave)--> unaffiliated
```

### 3.2 Invite flow (preferred)

1. An `active` member with sufficient authority (v1: any active member) calls `POST /clan/invite` with `{"ttl_seconds": <60..86400>, "as_role": "auto"}`.
2. Server returns:
   ```json
   {
     "token": "<base64url 32 bytes>",
     "clan_id": "...", "clan_cert_pin": "sha256:...",
     "lan_address": "192.168.1.42:7777",
     "expires_at": "2026-05-21T18:30:00Z"
   }
   ```
3. The candidate (running `cland --join <token>`) calls `POST /clan/join` on the issuer with `{"token": "...", "member_id": "...", "member_pubkey": "..."}`.
4. Issuer verifies token, then returns `clan_key` (and `clan_cert` for pinning). Token is consumed (single use).
5. Candidate persists `clan_id`, `clan_key`, pinned `clan_cert`, and transitions to `admitted`, then `active` on first successful capability advertisement.

### 3.3 Paste-key flow (fallback)

For low-friction joining without out-of-band token exchange:

1. An existing member exposes the Clan Key as a QR code or 6-word BIP39-ish phrase in the Console.
2. Candidate enters the phrase, plus the LAN address of any active member.
3. Candidate connects, advertises identity, and is admitted on the active member's policy.

**Trust caveat:** any sniffer in the QR/voice channel sees the Clan Key. Use only over trusted channels (sitting at the same desk, secure messenger).

### 3.4 Revocation

`POST /clan/revoke` `{"member_id": "...", "reason": "..."}`:

- The revoker (any active member in v1) signs a revocation record with the Clan Key.
- The record is gossiped to all active members within one heartbeat window (≤2s).
- All members add the revoked member's pubkey hash to a **pin-revocation list** persisted on disk.
- In-flight tool calls from/to the revoked member: cancelled with status `revoked`.
- In-flight inference requests routed to the revoked member: retried once on another `inference`-capable member.
- The revoked member is `purged` from rosters after 48h grace (so brief mistakes can be undone manually).

### 3.5 Self-leave

A member can call `POST /clan/leave` on itself. It signs the leave intent, broadcasts to peers, deletes local Clan secrets, and returns to `unaffiliated`.

---

## 4. Discovery & Capability Advertisement

### 4.1 Discovery (mDNS, LAN only in v1)

Every active member registers an mDNS service:

- Service type: `_minti-clan._tcp.local`
- Port: `7777`
- TXT records:
  - `clan_id=<uuid>`
  - `member_id=<uuid>`
  - `proto=1` (protocol version)

mDNS is used **only for finding peers**. All authentication and capability data move over the authenticated HTTPS endpoint, not over mDNS.

**Known limitations:** mDNS is blocked by some enterprise APs, doesn't cross VLANs, and may be filtered on captive-portal networks. v1 documents this; v2 adds manual unicast address-paste as a fallback.

### 4.2 Capability advertisement

Every 30s (and on capability change), each active member POSTs `/clan/advertise` to each known peer with:

```json
{
  "member_id": "...",
  "clan_id": "...",
  "term": 17,
  "os": "linux",
  "hardware": {
    "cpu_score": 1234,
    "ram_gb": 64,
    "vram_gb": 32,
    "gpu": "NVIDIA RTX 5090",
    "on_battery": false,
    "uptime_24h": 0.99
  },
  "reasoning_score": 72,
  "system_score": 92,
  "capabilities": {
    "reasoning":  {
      "enabled": true,
      "backends": [
        { "kind": "local-ollama", "model": "deepseek-r1:32b",
          "reasoning_score": 72, "available": true }
      ]
    },
    "inference":  { "enabled": true, "models_resident": ["llama3.2:3b","qwen2.5:7b"] },
    "vision-gen": { "enabled": false },
    "embeddings": { "enabled": false },
    "tools":      { "enabled": true,  "packs": ["recon"] },
    "storage":    { "enabled": true,  "cache_gb_free": 420 }
  },
  "load": 0.15,
  "pinned_orchestrator": false
}
```

Receivers retain the most-recent advertisement per `member_id` for the duration of that member's `active` state. An advertisement older than 2× the heartbeat interval (4s, by default) is treated as stale and the member's contribution is excluded from routing decisions.

---

## 5. Leader-Lease Election

### 5.1 Why leader-lease, not pure gossip

Per peer-review (PRD v0.5), gossip-only election creates split-brain on partitions. The leader-lease model is "Raft-lite": real terms, real leases, real failover — without log replication (we have no state to replicate, only routing decisions).

### 5.2 Lease parameters

| Parameter | Value | Notes |
|---|---|---|
| `HEARTBEAT_INTERVAL` | 2s | Orchestrator emits a heartbeat this often. |
| `LEASE_DURATION` | 8s | Heartbeat carries `lease_until = now + LEASE_DURATION`. |
| `FAILOVER_GRACE` | 6s | If no heartbeat received for this long, peers begin election. |
| `ELECTION_TIMEOUT` | 1s | Time to wait for a new heartbeat before declaring election complete. |
| `MIN_TERM_INCREMENT` | 1 | Term must strictly increase on election. |

### 5.3 Heartbeat message

POST `/clan/heartbeat` to every peer every `HEARTBEAT_INTERVAL`:

```json
{
  "member_id": "<orchestrator>",
  "clan_id": "...",
  "term": 17,
  "lease_until": "2026-05-21T18:00:08Z",
  "reasoning_score": 72,
  "active_roster": ["uuid1", "uuid2", "uuid3"]
}
```

Peers update local state: `current_orchestrator = member_id`, `current_term = term`, `lease_expires = lease_until`.

A peer ignores a heartbeat if `term < current_term`, or if the sender is not the highest-`reasoning_score` member according to its current advertisements (anti-spoofing).

### 5.4 Election flow

When a member observes `lease_expires < now - FAILOVER_GRACE`:

1. **Candidate selection**: scan current advertisements for members where `capabilities.reasoning.enabled == true`. The candidate with the highest `reasoning_score` is the next Orchestrator. Ties broken by oldest `joined_at`. If a member has `pinned_orchestrator: true` and is reasoning-capable, it wins regardless.
2. **Term increment**: `new_term = current_term + 1`.
3. **Announce**: the candidate (or, if the candidate is *self*, this member) POSTs an "election announcement" heartbeat with `term = new_term` to all peers.
4. **Acceptance**: each peer accepts the first heartbeat for `new_term` from a candidate that matches its own scoring conclusion. If a peer's view disagrees (it scored a different member highest), it rejects with `409 Conflict, expected_orchestrator=<id>`.
5. **Settlement**: after `ELECTION_TIMEOUT`, if the candidate has received ≥ ⌈N/2⌉ accepts (where N = current active roster size), it commits the lease and begins normal heartbeat operation. Otherwise the election times out and is retried after a small random backoff (50–150 ms).

### 5.5 Split-brain handling

If two candidates simultaneously claim the same `new_term`:

- Peers see both heartbeats. Each peer accepts only the first received and rejects the second.
- The "loser" (whose accept count falls below quorum) yields, increments its `term` counter to match, and listens.
- If no candidate reaches quorum within `ELECTION_TIMEOUT`, both candidates abort, do a random backoff, and retry from step 1. The protocol terminates because backoffs eventually diverge.

### 5.6 User-pin override

A user may set `pinned_orchestrator: true` on their own member via Console or `cland pin`. While any reasoning-capable member is pinned:

- Pinned member wins all elections regardless of `reasoning_score`.
- If the pinned member becomes unreachable, the pin is auto-suspended until it returns (then resumed).
- Multiple simultaneous pins is a misconfiguration; v1 logs a warning and the lowest `member_id` wins.

---

## 6. Routing & Scoring

### 6.1 Reasoning vs worker routing

Two independent dimensions, two scores:

| Workload type | Routed to | Score used | Picked by |
|---|---|---|---|
| `reasoning` (chat completion, agent loop step) | Orchestrator (always) | `reasoning_score` | Election (§5) |
| `inference` (other chat completion) | Any `inference`-capable | `system_score`, `load` | Orchestrator's router (§6.2) |
| `vision-gen` | Any `vision-gen`-capable | `system_score`, `load` | Orchestrator's router |
| `embeddings` | Any `embeddings`-capable | `system_score`, `load` | Orchestrator's router |
| `tools` (MCP call) | Member that owns the tool or closest by network | per-tool policy | Orchestrator's router |

### 6.2 Worker router (v1)

For each request:

1. Filter members to those advertising the relevant capability.
2. Compute `score = system_score * (1 - load)`.
3. Pick the highest-scoring eligible member. Break ties by lowest network latency (measured during last advertisement round).
4. On member failure mid-request, retry once on the next-best candidate.

### 6.3 Reasoning-score rubric (table-driven, v1)

Loaded from `/etc/minti/reasoning-scores.yaml`; ships with this default table. Users edit freely:

| Backend example | reasoning_score |
|---|---|
| `remote-api:anthropic:claude-opus-4-7` | 95 |
| `remote-api:openai:gpt-4-class` | 90 |
| `remote-api:anthropic:claude-sonnet-4-6` | 88 |
| `local:llama3.1:70b-q4` | 72 |
| `local:qwen2.5:32b` | 68 |
| `local:hermes3:8b` | 58 |
| `local:llama3.2:8b` | 55 |
| `local:mistral:7b` | 40 |
| `local:llama3.2:3b` | 35 |
| `local:phi-3.5:mini` | 33 |

A member's published `reasoning_score` is the **maximum** across its enabled, *available* backends.

### 6.4 System-score formula (v1)

```
system_score = clamp_0_100(
    0.40 * normalize(vram_gb, 0..48)
  + 0.20 * normalize(ram_gb, 0..128)
  + 0.20 * normalize(cpu_score, 0..2000)
  + 0.10 * normalize(nvme_throughput_gbps, 0..10)
  + 0.10 * uptime_24h
  - 0.10 * on_battery
  - 0.10 * recently_failed
)
```

Where `normalize(x, lo..hi) = max(0, min(1, (x - lo) / (hi - lo)))`. Tunable; ships in `/etc/minti/system-score.yaml`.

---

## 7. Tool-Call Routing & Permissions

### 7.1 Permission prompts always render on origin

A core peer-review concern: if member A's agent decides to run `nmap` and routing sends the call to member B, where does the permission prompt appear?

**Resolution: permission prompts always render on the agent's *origin* member**, never on the executor.

Flow:

1. Member A's agent decides to invoke `mcp-recon.nmap_scan({target: ...})`.
2. Member A's local MCP layer (NOT the executor) renders the permission prompt to A's user.
3. On user approval, A generates a signed **execution token**:
   ```
   { request_id, origin_member, target_member, tool, args, approved_at, exp }
   ```
   signed with Clan Key HMAC.
4. A sends `POST /mcp/execute` to member B with the token + args.
5. B verifies the token, checks its own local policy (some tools may be locally denied even on remote request), and executes if allowed.
6. B streams output back to A; A relays to the agent.

Member B never prompts its user during cross-Clan execution. If B's policy forbids a tool, B rejects with `403 policy_deny` and A's agent treats it as a tool failure.

### 7.2 Per-tool policy

Each member has a `~/.minti/policy.yaml`:

```yaml
mcp:
  fs:
    allow: ["~/Documents", "~/Projects"]
    deny: ["~/.ssh", "~/.aws"]
  shell:
    mode: prompt   # prompt | allowlist | deny
    allowlist: ["ls", "cat", "grep", "find"]
  recon:
    allow_remote_origin: true     # accept tool calls from other members
    require_local_user_present: false
  pkg:
    require_sudo: true
```

A remote tool call must pass *both* the origin member's prompt AND the executor's policy.

---

## 8. Key Rotation

### 8.1 Rotation flow (Orchestrator-initiated)

1. Orchestrator generates `clan_key_new` (32 random bytes).
2. Orchestrator broadcasts `POST /clan/rotate-key` to all active members with `{new_key, effective_at, grace_until}`, signed by the OLD key.
3. Each member acknowledges with a signed ACK (using the new key) within 30s.
4. If all active members ACK: the rotation is **committed**. `effective_at` = `now`, `grace_until` = `now + 5 minutes`. Both keys verify HMACs during the grace window; only the new key after.
5. If any active member fails to ACK: the rotation is **aborted**. Old key remains; no state change. Operator is notified.

In-flight requests during the grace window complete on whichever key they started with.

### 8.2 Rotation triggers

- Manual: user invokes `cland rotate-key`.
- Automatic: scheduled rotation (default disabled in v1; configurable cadence in v1.1).
- Forced: after a member revocation, the operator is *strongly* encouraged to rotate (the revoked member still knew the old key).

### 8.3 Cert rotation

The `clan_cert` is rotated independently and less frequently. v1: manual command, members re-pin on receipt of a signed cert-update notice. v2: automatic per per-member-keypair migration.

---

## 9. Audit Log

### 9.1 Local-only, per-member

Each member writes `~/.minti/audit.jsonl` (one JSON record per line). The log is **never** gossiped or aggregated. It is visible only to that member's local user. Other Clan members do not see it.

### 9.2 Record format

```json
{
  "ts": "2026-05-21T18:00:00.123Z",
  "event": "api_key_read | request_received | request_sent | election_observed | membership_change | tool_executed | tool_rejected | key_rotated | policy_change",
  "actor": "self | <member_id>",
  "details": { ... event-specific ... }
}
```

### 9.3 Required events (v1)

| Event | When | Details (minimum) |
|---|---|---|
| `api_key_read` | Any read of a stored remote-API key | `{key_id, by_component, granted: bool}` |
| `request_received` | Any cross-Clan request handled | `{from_member, kind, model, prompt_tokens, completion_tokens}` (NOT content) |
| `request_sent` | Any cross-Clan request issued | `{to_member, kind, model, prompt_tokens}` |
| `election_observed` | Any Orchestrator change observed | `{prior, new, term, reason}` |
| `membership_change` | Member admit/revoke/purge | `{event, member_id}` |
| `tool_executed` | MCP tool ran (local OR cross-Clan) | `{tool, args_hash, origin_member, allowed: bool, exit_code}` |
| `key_rotated` | Clan Key rotated | `{old_fingerprint, new_fingerprint, initiated_by}` |
| `policy_change` | Local policy.yaml edited | `{path, old_hash, new_hash}` |

### 9.4 Retention

v1: no automatic rotation. v1.1 will add `logrotate` integration with a default of 30 days.

---

## 10. Endpoint Reference (HTTPS, port 7777)

| Path | Method | Caller | Purpose |
|---|---|---|---|
| `/v1/chat/completions` | POST | local agent | OpenAI-compatible chat (routed by Orchestrator) |
| `/api/chat` | POST | local agent | Ollama-compatible chat (same routing) |
| `/v1/embeddings` | POST | local agent | OpenAI-compatible embeddings |
| `/clan/advertise` | POST | active member | Push capability advertisement |
| `/clan/heartbeat` | POST | Orchestrator | Periodic lease renewal |
| `/clan/invite` | POST | active member | Generate single-use admission token |
| `/clan/join` | POST | candidate | Redeem token, receive secrets |
| `/clan/leave` | POST | self | Voluntary self-removal |
| `/clan/revoke` | POST | active member | Kick another member |
| `/clan/rotate-key` | POST | Orchestrator | Begin key rotation |
| `/clan/members` | GET | local UI | Current roster |
| `/clan/orchestrator` | GET | local UI | Who is the current Orchestrator |
| `/clan/election/history` | GET | local UI | Recent elections (term, winner, reason) |
| `/mcp/execute` | POST | origin member | Cross-Clan tool call with execution token |

---

## 11. Versioning

This is **protocol version 1** (`proto=1` in mDNS TXT). Future versions:

- Backwards-incompatible changes bump to `proto=2` and refuse to interoperate with `proto=1` members.
- Members on different `proto` versions in the same Clan: the lower-version members are treated as `revoked` until upgraded.

---

## 12. Open items (deferred but tracked)

| ID | Item | Target |
|---|---|---|
| OQ-1 | Dynamic capability probes (replace static `reasoning_score` table with measured latency/throughput) | v1.1 |
| OQ-2 | Manual unicast address-paste fallback for mDNS-hostile networks | v1.1 |
| OQ-3 | Tensor-parallel inference for models bigger than any single member | v2 (exo or llama.cpp RPC) |
| OQ-4 | Per-member mTLS replacing shared Clan Key (full insider-abuse fix) | v2 |
| OQ-5 | WireGuard mesh for off-LAN members | v2 |
| OQ-6 | Raft-strict consensus (if leader-lease proves insufficient) | v2 if needed |
| OQ-7 | Micro Clan Agent for ESP32/Cardputer-class devices (subset proto) | v2 stretch |

---

*End of Clan Protocol Spec v0.1. Wire-level conformance tests will live in `tests/protocol/`.*
