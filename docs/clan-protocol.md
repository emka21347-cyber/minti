# Clan Protocol Specification — v0.4

> Status: **Draft**, v1 implementation target.
> Source of truth for the on-wire behavior of `cland` (Linux) and the cross-platform Clan Agent (Windows / macOS).
> This document defines the protocol; the PRD ([../../../../.claude/plans/hello-can-we-create-abundant-hopper.md](../../../../.claude/plans/hello-can-we-create-abundant-hopper.md)) defines the product. Where they conflict, the PRD wins on intent and this document wins on wire details.
>
> **v0.4 (2026-06-11, Memory M0):** Adds §13 *Clan Memory* — a gossiped, Clan-owned, curated memory **graph** for distributed research: LWW/CRDT-lite merge, content-versioned digest riding the election heartbeat (third passenger after revocations §3.5 + roster), an elected **Scribe** role on the *weakest* capable node (inverse of Orchestrator selection) with a continuous small-LLM distillation duty behind a human-review gate, explicit research sessions, and a portable single-file **Clan Blueprint** importable at `create`. §5.3 gains a heartbeat-passengers note (retroactively documenting the H-2/H-3 digest fields). §10 gains the four memory endpoints (+ the H-2 `GET /clan/revocations` row that was missing). §12 gains OQ-8 (delta sync), OQ-9 (edge tombstones / hard-delete), OQ-10 (blueprint signing).
>
> **v0.3 (2026-06-06, M8 Phase 0):** Adds §3.4 *Knock flow (no out-of-band secret)* — a third Clan-joining path alongside invite-token (§3.2) and paste-key (§3.3). Joiner sends an ephemeral X25519 pubkey + Ed25519 identity to a known Clan address; receiver and joiner derive a 20-bit confirm code from a HKDF binding both pubkeys + `clan_id` + `knock_id`; existing Clan operator visually verifies the digits with the joiner over a side channel before pressing accept; the welcome payload is AES-256-GCM-sealed with the ECDH-derived key + `aad=knock_id`. Renumbers existing §3.4 Revocation → §3.5 and §3.5 Self-leave → §3.6.
>
> **v0.2 (2026-05-27, M4 Phase 0):** Six additive edits surfaced by a local-LLM peer-review pass on the M4 implementation plan — §3.3 paste-key entropy clarified to 12-word BIP39 + HKDF; §6.4 `recently_failed` defined; §7.1 `target_member` location pinned to token claims + v1 honesty note added; §10 endpoint table gains `/v1/messages`; §12 OQ-2 (manual peer-add fallback) moved from v1.1 into v1 scope. Wire format unchanged where it was already concrete.

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

1. An existing member exposes the Clan Key via one of:
   - **QR code** encoding the full 256-bit `clan_key` directly, OR
   - **12-word BIP39 mnemonic** encoding a 128-bit derivation seed.
2. Candidate enters the phrase (or scans the QR), plus the LAN address of any active member.
3. Candidate connects, advertises identity, and is admitted on the active member's policy.

**Entropy and key derivation.** When the mnemonic path is used, the candidate's `cland` expands the 128-bit BIP39 seed to the full 256-bit `clan_key` via `HKDF-SHA256(seed, salt="minti-clan-key", info="v1")`. The 128-bit seed is sufficient for v1's trusted-LAN threat model — brute-forcing 128 bits is infeasible with current hardware. The QR path uses the full 256 bits directly. v2 mTLS replaces the shared `clan_key` entirely and closes the 128-bit gap.

**Trust caveat:** any sniffer in the QR/voice channel sees the Clan Key. Use only over trusted channels (sitting at the same desk, secure messenger).

### 3.4 Knock flow (live in-person / Signal-call onboarding)

For onboarding a new node when the joiner has *no out-of-band secret* — only knowledge of the Clan ID and a reachable LAN address of an active member. Trust is established by an **ephemeral X25519 ECDH** between joiner and receiver plus a **20-bit Short Authentication String (SAS)** — a 6-digit numeric confirm-code — that the existing-Clan operator visually verifies with the joiner over a side channel (phone call, in-person, Signal). Modelled on the Signal pairing flow.

This flow coexists with §3.2 (invite-token) and §3.3 (paste-key). Choose by handoff style:

| Flow | Joiner needs… | Best for |
|---|---|---|
| §3.2 Invite-token | Token + LAN address + cert pin (single line) | Asynchronous remote ("paste this in the SSH session you opened on the new box") |
| §3.3 Paste-key | 12-word BIP39 mnemonic | Trusted-channel one-shot ("here's the phrase, save it somewhere safe") |
| §3.4 Knock | Clan ID + any active LAN address | Live in-person or Signal-call onboarding ("you're sitting next to me — let's join your laptop now") |

**Wire endpoints.** Two on the **receiver** side (any active Clan member can act as receiver), one on the **joiner** side:

| Endpoint | Side | Auth | Purpose |
|---|---|---|---|
| `POST /clan/knock` | Receiver | Anonymous (joiner has no `clan_key` yet) | Initiate the knock; receiver stores it pending operator decision |
| `GET /clan/knock-list` | Receiver | HMAC (Clan member operator) | TUI / CLI reads pending knocks for display |
| `POST /clan/knock-accept` | Receiver | HMAC | Operator accept; triggers encrypted delivery to joiner |
| `POST /clan/knock-deny` | Receiver | HMAC | Operator deny; receiver pings joiner with reason |
| `POST /clan/knock-deliver` | Joiner | **AES-GCM tag is the auth** (no HMAC; joiner doesn't have `clan_key` yet) | Receiver delivers the sealed welcome blob; joiner opens, persists, becomes `active` |

**Request / response shapes.**

```json
// → POST /clan/knock  (anonymous)
{
  "clan_id":                  "<UUIDv4>",
  "joiner_member_id":         "<UUIDv4>",
  "joiner_identity_pub_b64":  "<base64 Ed25519 pubkey>",
  "joiner_x25519_pub_b64":    "<base64 ephemeral X25519 pubkey>",
  "joiner_display":           "alice-macbook",
  "joiner_addr":              "192.168.56.103:7777"
}

// ← KnockResponse
{
  "knock_id":                 "<hex 16 bytes>",
  "receiver_x25519_pub_b64":  "<base64 receiver's ephemeral X25519 pubkey>",
  "confirm_code":             "541-829",
  "ttl_seconds":              300
}
```

**Cryptographic core.** Both sides derive the *same* `(key, nonce, confirm_code)` from the same inputs. The receiver computes them when it stores the pending knock; the joiner computes them once it has the `KnockResponse`.

```
shared    = X25519(my_x25519_priv, peer_x25519_pub)            // 32 bytes
salt      = "minti-knock-v1"
info      = clan_id || knock_id || joiner_x25519_pub || receiver_x25519_pub
kdf       = HKDF-SHA256(IKM=shared, salt=salt, info=info)
read 48 bytes from kdf into bundle:
  key           = bundle[0:32]                                  // AES-256 key
  code_bytes    = bundle[32:36]                                 // 32-bit SAS source
  nonce         = bundle[36:48]                                 // 12-byte GCM nonce
sas30           = BE_uint32(code_bytes) mod 1_000_000_000        // 30 bits of entropy → 9 decimal digits
confirm_code    = sprintf("%04d-%05d",
                          sas30 / 100_000,
                          sas30 mod 100_000)                     // displayed "XXXX-XXXXX"
```

The static salt is namespaced (`"minti-knock-v1"`) so future protocol versions can rotate it. **`clan_id` and `knock_id` are in `info`** — without them, an attacker could precompute `(joiner_pub, receiver_pub) → confirm_code` tables offline; with them, every knock window requires a fresh derivation. The nonce is deterministic but safe: each `(joiner_pub, receiver_pub)` pair derives a unique `key`, so the same nonce never re-encrypts under the same key.

**Sealed welcome.** When the operator accepts, the receiver builds the same `WelcomeResponse` payload it would for §3.3 (clan_id, clan_cert_pem, clan_cert_priv_key_b64, clan_key_b64, roster) and seals it:

```
plaintext  = json.encode(WelcomeResponse)
ciphertext = AES-256-GCM_seal(key, nonce, plaintext, aad=knock_id)
```

```json
// → POST /clan/knock-deliver  (on the joiner's listener)
{
  "knock_id":            "<hex 16 bytes>",
  "encrypted_blob_b64":  "<base64 ciphertext + GCM tag>"
}
```

The joiner looks up `knock_id` in its own pending-knock store (which remembers its ephemeral X25519 private key), derives the same `(key, nonce)`, and `AES-256-GCM_open(…, aad=knock_id)`. **The GCM tag is the receiver-authenticity proof** — only a party that knows `shared` (i.e. holds `receiver_x25519_priv`) can forge a valid tag. No separate signature is required.

**Confirm-code semantics.** 30 bits of SAS (≈10⁹ space, 9-digit code rendered "XXXX-XXXXX") was chosen after the M8 Phase 0 peer-review surfaced a **pubkey-grinding attack** on a smaller 20-bit space: an active MITM who intercepts `knock_id` can generate ephemeral X25519 keypairs offline and run HKDF until they find a pubkey whose derived SAS matches the value the operator's TUI is displaying. With 20 bits, modern hardware finds a collision in milliseconds — well under the 5-minute knock TTL — so 20 bits offers no real protection beyond the human-attention threshold. 30 bits raises the offline grind to ≈10⁹ HKDF iterations (minutes-to-hours of single-thread compute), which is observable in the LAN and won't complete within the TTL. The defence is therefore: (a) `clan_id` + `knock_id` in HKDF info prevents pre-computation across knocks; (b) 30 bits requires per-knock targeted grind that exceeds the TTL; (c) the operator and joiner each independently derive the SAS and verify match over a side channel — **both** sides must visually confirm before any state is applied (see "Mutual SAS confirmation" below).

**Mutual SAS confirmation (CRITICAL).** The protocol's MITM defence collapses if only ONE side checks the SAS. The joiner-side CLI/TUI MUST display the locally derived SAS and BLOCK on explicit user confirmation (`y`/`n`) BEFORE processing any incoming `/clan/knock-deliver`. The flow is:

1. Joiner derives SAS from `KnockResponse`. Prints to terminal: `Show this code to the Clan operator: XXXX-XXXXX. Does it match what they see? [y/n]`.
2. Joiner blocks on stdin (or TUI keypress) waiting for `y`. On `n` or 5-minute timeout, joiner discards its ephemeral X25519 private key + the pending knock entry, exits.
3. In parallel, the receiver's operator sees the same SAS in their TUI. They read it aloud / over Signal to the joiner. The joiner-side human compares and presses `y` (or `n`).
4. After joiner confirms `y`, the joiner enters a state where it will accept exactly one matching `/clan/knock-deliver` from the original `receiver_addr` (see "Delivery allowlist" below).
5. Operator presses `a` in their TUI. Receiver POSTs `/clan/knock-deliver`.
6. Joiner verifies source IP, opens GCM blob, persists state, transitions to admitted.

Without step 1-3 on the joiner side, a MITM can substitute pubkeys, derive an attacker-controlled key, deliver an attacker-controlled blob, and the joiner blindly joins the attacker's "Clan". The receiver-operator's SAS check alone is insufficient because the attacker may not need the receiver to accept at all.

**Delivery allowlist (joiner side).** When the joiner POSTs `/clan/knock` to `receiver_addr`, it records that address. The joiner-side `/clan/knock-deliver` handler then refuses any POST whose source IP doesn't match `receiver_addr`'s IP (port may differ — ephemeral source ports on outbound TLS won't match the listener port). This prevents an attacker who scraped a valid `knock_id` from the wire from flooding the joiner with fake delivery attempts, AND prevents an attacker on the same LAN from racing the legitimate receiver to deliver first. Rejected deliveries return 403; the joiner audit-logs them so an operator can spot intrusion attempts after the fact.

**State machine.**

```
joiner-side                                 receiver-side
-----------                                 -------------
(holds clan_id only)
generate ephemeral X25519
record receiver_addr for allowlist
POST /clan/knock  ----------------------->  validate clan_id matches
                                            generate receiver X25519
                                            knock_id = rand(16 bytes)
                                            store PendingKnock{TTL=300s}
                <-----------------------    return KnockResponse
derive (key, nonce, code)                   derive (key, nonce, code)
display "Show this code: 5123-67890"
              ↕
[joiner human reads code aloud]
              ↕
                                            [operator opens TUI, sees code]
[joiner human confirms y/n] ←──── side channel (phone/in-person/Signal) ───→ [operator confirms]
joiner BLOCKS for y/n input
on `y`: arm /clan/knock-deliver acceptor    [operator presses `a` accept]
on `n` or timeout: discard ephemeral,       seal welcome with (key,nonce)
exit
                <─────────────────────────  POST /clan/knock-deliver
verify source IP matches receiver_addr
  (else 403 + audit log)
open + verify GCM tag (aad=knock_id)
write clan.json, identity.json
transition to admitted
                                            promote roster entry to active
                                            on first /clan/advertise (§4.2)
```

**Acceptance authority.** *Any* member whose roster `state` is `active` (§3.1) may accept or deny incoming knocks. The Clan is peer-equal post-Phase H-1; centralisation through the elected Orchestrator is not required for membership decisions. First-write-wins: the joiner's KnockStore deletes the entry under lock on first successful `knock-deliver`; concurrent accepts from multiple members race for the CAS and the losers receive 409.

**Rate limits.** All values v1; tune per operational data later.

| Side | Bucket | Limit |
|---|---|---|
| Receiver | per `joiner_addr` (the IP:port in `/clan/knock`) | 10 / 60 s |
| Receiver | per `clan_id` (all knocks targeting one Clan) | 30 / 60 s |
| Joiner   | source IP allowlist on `/clan/knock-deliver` | only `receiver_addr` IP accepted; all others 403 |
| Joiner   | per `knock_id` on `/clan/knock-deliver` | 1 accept; further attempts 409 |
| Joiner   | per allowed source IP for invalid blobs | 5 / 60 s (defence against a compromised receiver flooding garbage) |

The per-`clan_id` bucket prevents a swarm DoS against the receiver; the joiner's source-IP allowlist (the receiver_addr the joiner *originally targeted* in `/clan/knock`) shrinks the joiner-side attack surface to a single trusted source, eliminating the M8 peer-review concern about flooding `/clan/knock-deliver` with scraped `knock_id`s from third-party IPs.

**Threat model.**

| Attacker capability | Defence | Limit |
|---|---|---|
| Passive eavesdrop | TLS + SPKI pin on the knock leg (receiver advertises its existing Clan cert); ECDH ephemeral keys → forward secrecy | Standard |
| Active MITM swapping pubkeys | 30-bit SAS binds *both* pubkeys + `clan_id` + `knock_id`; **both** operator AND joiner independently verify digits (mutual confirmation) | ≈10⁹ HKDF iterations needed for collision per knock — minutes-to-hours wall-clock, exceeds 5-min TTL |
| MITM substitutes pubkeys, joiner accepts blindly | Joiner CLI blocks for `y/n` SAS confirmation before processing any `/clan/knock-deliver` (see "Mutual SAS confirmation") | Hard barrier — joiner refuses to apply blob without explicit human confirmation |
| Attacker scrapes `knock_id`, floods `/clan/knock-deliver` from elsewhere | Joiner allowlists source IP to `receiver_addr` only (see "Delivery allowlist") | Hard barrier — non-receiver IPs get 403 |
| Replay old `knock-deliver` | `knock_id` is single-use via CAS on joiner | After consume, 409 |
| Cross-Clan replay | `clan_id` in HKDF info → different keys → GCM tag fails | Cryptographic barrier |
| Spam knocks (DoS) | Per-`joiner_addr` + per-`clan_id` rate limits | See table |
| Compromised existing member | Out of scope for §3.4; addressed by revocation (§3.5) | v1 honesty |

**TTL bounds.** Default knock TTL is 5 min (300 s). Min: 60 s. Max: 15 min (900 s). The joiner-side ephemeral private key is held only while the knock is pending; on expiry the joiner discards it. The receiver-side KnockStore sweeper runs every 60 s (same cadence as the InviteStore sweep) and evicts expired knocks.

**Identity persistence.** The joiner uses their pre-existing Ed25519 identity (from `identity.json`, the same one used for §3.2/§3.3 joining). A joiner with a previously revoked identity will appear to the operator with that identity's fingerprint — `JoinerIdentityFingerprint` is the first 8 hex chars of `sha256(joiner_identity_pub)` and is shown in the receiver's TUI. The operator decides whether to accept; the protocol does not auto-block previously revoked identities (operator may have explicit reason to re-admit).

**Trust transferred on accept.** The sealed `WelcomeResponse` includes `clan_cert_priv_key_b64` — the Ed25519 private key that signs the Clan's TLS certificate. Per §2.2 and the v1 unitary-trust model (§10/R1), every Clan member holds this key so any member can serve TLS on the Clan's pinned cert. **A new joiner admitted via §3.4 therefore receives full authority to impersonate the Clan's TLS identity**, identical to admission via §3.2 or §3.3. Operators must treat the SAS confirmation step as the authorisation gate for that authority. v2 plans to replace the unitary-trust model with per-member certificates signed by a rotating CA; until then, every accepted knock grants the joiner the same trust level as any other Clan member.

### 3.5 Revocation

`POST /clan/revoke` `{"member_id": "...", "reason": "..."}`:

- The revoker (any active member in v1) signs a revocation record with the Clan Key.
- The record is gossiped to all active members within one heartbeat window (≤2s).
- All members add the revoked member's pubkey hash to a **pin-revocation list** persisted on disk.
- In-flight tool calls from/to the revoked member: cancelled with status `revoked`.
- In-flight inference requests routed to the revoked member: retried once on another `inference`-capable member.
- The revoked member is `purged` from rosters after 48h grace (so brief mistakes can be undone manually).

### 3.6 Self-leave

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

**Heartbeat passengers (additive, `omitempty`).** Later phases piggyback cheap state digests on the heartbeat so peers can detect drift without extra polling. All are optional string fields; receivers that predate a passenger ignore it (JSON unknown-field tolerance), so passengers never break mixed-version Clans:

| Field | Added | Meaning | On mismatch |
|---|---|---|---|
| `revocations_digest` | Phase H-2 | sha256 of sorted revoked member_ids | fetch `GET /clan/revocations`, merge (§3.5) |
| `roster_digest` | Phase H-3 | sha256 of sorted `(member_id, state)` tuples | fetch roster, merge state transitions |
| `memory_digest` | §13 | content-versioned graph digest (§13.5); also returned in the heartbeat **response** so follower edits flow back (§13.5 response leg) | fetch `GET /clan/memory`, merge (§13.4) |
| `scribe` | §13 | Orchestrator's current Scribe selection (member_id) | peers adopt it (§13.8) |

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

Where:

- `normalize(x, lo..hi) = max(0, min(1, (x - lo) / (hi - lo)))`.
- `on_battery` is `1.0` when the member is currently on battery power, `0.0` otherwise.
- `recently_failed` is the count of cross-Clan requests that failed (timeout, 5xx, or HMAC-reject) in a sliding **5-minute** window, normalised by `/ 20` and clamped to `[0, 1]`. So 20+ failed requests in 5 min saturate the penalty; isolated failures barely register. Counter is in-memory only — process restart clears it.

Tunable; ships in `/etc/minti/system-score.yaml`.

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
   signed with `HMAC-SHA256(clan_key, canonical_serialize(token))`. `target_member` and all other fields are part of the signed claims — they are **not** passed as query-string parameters or top-level body fields, so the wire request can't carry an unauthenticated target hint.
4. A sends `POST /mcp/execute` to member B with the token (in the body) + the unsigned `args` blob.
5. B verifies the token (HMAC valid, `target_member == self.member_id`, `exp` not past, `approved_at` not in the future, `request_id` not previously seen). On any failure, B returns `401` and audit-logs the attempt. B then checks its own local policy against `tool` and `args` (some tools may be locally denied even on remote request) and executes if allowed; on policy deny, returns `403 policy_deny`.
6. B streams output back to A; A relays to the agent.

Member B never prompts its user during cross-Clan execution.

**v1 honesty note (clan_key origin binding).** Because v1 uses a single shared `clan_key`, the HMAC over the execution token proves only that the token was issued by *some* current Clan member — not specifically by `origin_member`. A malicious insider could forge a token claiming someone else as origin. The "permission prompt on origin" guarantee is a UI-level control (the user actually clicked yes on their machine), not a cryptographic one. **v2 per-member keypairs close this gap** — tokens will then be signed by the origin's private key and verifiable only against that origin's pubkey.

### 7.2 Per-tool policy

Two files are consulted, in order:

- `/etc/minti/policy.yaml` — system defaults installed by `install.sh`.
- `~/.minti/policy.yaml` — per-user overrides for the invoking user.

The user file fully **replaces** corresponding system fields (it does not deep-merge lists). Missing files are treated as empty; the most-restrictive defaults apply when no file is present.

```yaml
mcp:
  fs:
    allow: ["~/Documents", "~/Projects"]
    deny: ["~/.ssh", "~/.aws"]
    deny_tools: []                # explicit per-tool kill switch
  shell:
    mode: prompt                  # prompt | allowlist | deny
    allowlist: ["ls", "cat", "grep", "find"]
    deny_tools: []
  recon:
    allow_remote_origin: true     # accept tool calls from other members
    require_local_user_present: false
    allow_raw_socket: false       # gate nmap -sS
    deny_tools: []
  pkg:
    require_sudo: true
    deny_tools: []
  http:
    max_body_bytes: 1048576       # cap on fetch_url response body
    deny_tools: []
```

`deny_tools` is a per-namespace allow-everything-except list, checked before any other rule for that namespace. `allow_raw_socket` gates nmap's SYN scan (`-sS`), which needs `CAP_NET_RAW`.

A remote tool call (when cland routes it cross-Clan in M4) must pass *both* the origin member's prompt AND the executor's policy.

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
| `/v1/messages` | POST | local agent | Anthropic-compatible chat (routed by Orchestrator). cland exposes every shape `runtime-adapter` does. |
| `/api/chat` | POST | local agent | Ollama-compatible chat (same routing) |
| `/v1/embeddings` | POST | local agent | OpenAI-compatible embeddings |
| `/clan/advertise` | POST | active member | Push capability advertisement |
| `/clan/peer-add` | POST | local user/CLI | Manually register a peer by `ip:port` when mDNS discovery is unavailable (v1; pulled forward from OQ-2) |
| `/clan/heartbeat` | POST | Orchestrator | Periodic lease renewal |
| `/clan/invite` | POST | active member | Generate single-use admission token |
| `/clan/join` | POST | candidate | Redeem token, receive secrets |
| `/clan/leave` | POST | self | Voluntary self-removal |
| `/clan/revoke` | POST | active member | Kick another member |
| `/clan/rotate-key` | POST | Orchestrator | Begin key rotation |
| `/clan/members` | GET | local UI | Current roster |
| `/clan/orchestrator` | GET | local UI | Who is the current Orchestrator |
| `/clan/election/history` | GET | local UI | Recent elections (term, winner, reason) |
| `/clan/revocations` | GET | active member | Full revocation list, fetched on heartbeat digest mismatch (Phase H-2; row added retroactively in v0.4) |
| `/clan/memory` | GET | active member / local UI | Full memory graph (§13); fetched on `memory_digest` mismatch |
| `/clan/memory/digest` | GET | local UI | Cached graph digest (§13.5) — cheap change-poll for the workspace; gossip itself rides the heartbeat, never this |
| `/clan/memory/node` | POST | active member / local CLI | Create or update one memory node (§13.6); author set by the daemon |
| `/clan/memory/edge` | POST | active member / local CLI | Add one memory edge (§13.6); set-union semantics |
| `/clan/memory/import` | POST | local CLI | Import a Clan Blueprint into the running Clan (§13.10); merge by default |
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
| ~~OQ-2~~ | ~~Manual unicast address-paste fallback for mDNS-hostile networks~~ — **resolved in v0.2; in v1/M4 scope as `/clan/peer-add` (§10) and the `minti-cland peer-add` CLI subcommand.** | v1/M4 |
| OQ-3 | Tensor-parallel inference for models bigger than any single member | v2 (exo or llama.cpp RPC) |
| OQ-4 | Per-member mTLS replacing shared Clan Key (full insider-abuse fix) | v2 |
| OQ-5 | WireGuard mesh for off-LAN members | v2 |
| OQ-6 | Raft-strict consensus (if leader-lease proves insufficient) | v2 if needed |
| OQ-7 | Micro Clan Agent for ESP32/Cardputer-class devices (subset proto) | v2 stretch |
| OQ-8 | Memory delta-sync (full-graph fetch on digest mismatch is O(graph); fine ≤2 MiB on LAN, wasteful beyond) | v1.1 |
| OQ-9 | Memory edge tombstones + hard-delete/compaction (v1 archives nodes but never shrinks the graph; edges are add-only) | v1.1 |
| OQ-10 | Blueprint signing (v1 ships an integrity checksum + reserved `signature` field; no authenticity proof) | v2 |

---

## 13. Clan Memory

A Clan-owned, curated, **gossiped knowledge graph** built for distributed research: every member contributes findings into one shared graph; an elected **Scribe** (§13.8) continuously distills Clan activity into it; the whole graph (or one research session) exports as a portable **Clan Blueprint** (§13.10) that a fresh Clan can import at `create`. The model is curated memory in the Karpathy sense — distilled, evolving knowledge — not raw logs.

**Unlike the audit log (§9.1), which is *never* gossiped, Clan Memory IS gossiped**: every active member holds the full graph and converges on the union of all members' contributions. The two stores are opposites by design — audit answers "what did *my* node do" (private, per-member); memory answers "what does the *Clan* know" (shared, replicated).

**Survival rule.** Memory is keyed to `clan_id` and lives exactly as long as the Clan: it survives daemon restarts and reboots, including the 1-node last-survivor case. On self-leave (§3.6) the leaving member deletes its local copy along with the other Clan secrets; joining a different Clan prunes any graph whose `clan_id` doesn't match. (Same lifetime rule as workspace chat sessions.)

**Write authority is peer-equal.** Any `active` member may create, edit, or archive nodes and add edges — the same authority model as membership decisions (§3.4 "Acceptance authority"). There is no memory-owner role; the Scribe is a *contributor* with a duty, not a gatekeeper.

### 13.1 Data model

A single graph per Clan. Wire shapes (JSON):

```json
// Node
{
  "id":          "<UUIDv4, or deterministic id (§13.3)>",
  "type":        "research_session | finding | decision | fact | skill | event | member | artifact",
  "title":       "<= 200 chars",
  "body":        "markdown, <= 8 KiB",
  "tags":        ["<= 16 strings"],
  "status":      "proposed | active | superseded | archived",
  "session_id":  "<research_session node id, or \"\" for global memory>",
  "provenance": {
    "author_member_id": "<member_id — set by the DAEMON, never client-supplied>",
    "source":           "manual | system | scribe | distill | import",
    "created_at":       "RFC3339"
  },
  "updated_at":  "RFC3339Nano — the LWW key",
  "rev":         1
}

// Edge
{
  "from": "<node id>", "to": "<node id>",
  "relation": "relates | supersedes | derived_from | contributes_to | about_member | caused_by",
  "created_at": "RFC3339", "created_by": "<member_id>"
}

// Graph (the unit that is stored, fetched, merged, and exported)
{ "format_version": 1, "nodes": [ ... ], "edges": [ ... ] }
```

**Status lifecycle.** `proposed` is the Scribe/distillation entry state (§13.9) — visible in the review lane, excluded from "what the Clan knows" summaries until a human **promotes** it to `active`. `superseded` marks a node replaced by a newer one (set alongside a `supersedes` edge; manual in v1). `archived` is the **tombstone**: archiving is an ordinary LWW field update, so deletion propagates exactly like any edit. Tombstones are never removed in v1 (hard-delete is OQ-9) and keep their `title`/`body` (an archived research session is *closed*, not destroyed). Any status may transition to any other via a node update; UIs only offer the sensible arrows (propose→promote/dismiss, active→archive).

**Caps (lean, P2).** Enforced at the write endpoints (§13.6) with clear errors: **2,000 nodes**, **8,000 edges**, **2 MiB serialized graph**, plus the per-field caps above (title 200 chars, body 8 KiB, tags 16). Caps exist to keep the full-graph gossip fetch, the workspace render, and the 1–2 GB boxes comfortable. **Proposed nodes count toward the node cap** like any other (see §13.9 for the Scribe's own budget). **Over-cap honesty:** merge is deliberately exempt (§13.4), so a partition-heal union can land past a cap; from there the graph is **read-only** (new writes 409) until it shrinks — and v1 has no compaction (archiving keeps title/body; hard-delete is OQ-9). Practical recovery: export the valuable sessions as a Blueprint and found (or locally `--replace`) from it. Documented rather than hidden.

### 13.2 Storage

`memory.json` in the cland state dir, mode **0600** (distillates may carry chat content — treat like `clan.json`, not like `revocations.json`). Written via the same atomic write-temp + rename path as all Clan state; a missing file loads as the empty graph. The graph is held in memory by the daemon's memory service and persisted on change only — never re-parsed per heartbeat (§13.5).

### 13.3 Identifiers

- **Manual + scribe + import nodes:** UUIDv4 (hand-rolled from crypto/rand, as `identity.go` does — no external dep, P1).
- **System / structural nodes** (auto-events §13.7.1, per-session scribe summaries): a **deterministic id** so every observer mints the *same* node and the merge union dedups instead of multiplying. Construction:

```
seed  = sha256( clan_id || "|" || kind || "|" || subject || "|" || qualifier )
id    = seed[0:16] folded to UUID shape (version nibble = 4, variant bits = 10)
```

e.g. `kind="member_joined", subject=<member_id>, qualifier=""` — every member that observes the join writes the identical node id; the LWW merge collapses them. The qualifier disambiguates repeatable events (e.g. an election term number).

### 13.4 Merge semantics (CRDT-lite)

`Merge(local, remote) → merged` must be **commutative, associative, and idempotent** so that pairwise gossip converges regardless of topology or ordering:

- **Nodes** are keyed by `id`. On conflict the winner is the greater tuple of
  `(updated_at, rev, sha256(canonical_node_bytes))` — compared in that order. Both sides compare the identical tuple, so the winner is deterministic regardless of either receiver's local clock: convergence is guaranteed; clock skew affects *fairness* (whose edit survives), never *agreement*. The `rev` and hash terms break ties **only at identical timestamps** — they provide **zero** protection against clock skew (M0 peer review, gemma4).
- **Canonical node bytes (normative).** `canonical_node_bytes` = the node encoded as compact stdlib JSON (no indentation) with fields in the exact §13.1 declaration order. Implementations MUST declare the Go structs in that order with explicit `json:` tags and MUST ship a conformance test asserting the canonical encoding of a fixed node is byte-stable — Go marshals in struct declaration order, so a careless field insertion silently changes every hash (M0 peer review, qwen3.6).
- **Origin-monotone timestamps (HLC-lite).** The write path (§13.6) stamps `updated_at = max(now, max_updated_at_in_local_graph + 1ns)`. Stamped once at the origin daemon and gossiped verbatim, so convergence is untouched (receiver-side clamping is forbidden — different receivers would stamp different values and diverge). Effect: a member with a far-future clock can write nodes that temporarily out-rank honest wall-clocks, but any member's *next* edit stamps past the poisoned value — nobody is ever locked out of editing. The cost is Lamport-style: after poisoning, timestamps run ahead of wall-clock (visible in the UI as author + updated_at) until real time catches up. Three lines of code; vector clocks stay out (P2).
- **Tombstones propagate as ordinary LWW wins** — `status:"archived"` is just a field edit with a newer `(updated_at, rev)`. Corollary: **archived is not a terminal state** — a concurrent edit carrying a greater tuple resurrects the node. Acceptable for v1's research-notes workload (data preservation beats strict deletion); flagged so nobody mistakes archive for delete.
- **Edges** are a **set-union** deduped by `(from, to, relation)`; on duplicate, local metadata (`created_at`, `created_by`) wins — mirrors `Revocations.Merge` (§3.5). Edges are add-only in v1 (no edge tombstones — OQ-9); an edge whose endpoint node is archived is simply not rendered.
- **Dangling edges are permitted.** Gossip and direct POSTs can deliver an edge before its endpoint nodes. Receivers keep the edge; UIs hide it until both endpoints exist. Refusing it would break the union property.
- **Merge is permissive about per-field caps.** Caps are enforced at write endpoints (§13.6); merge accepts structurally valid nodes even if oversized, because dropping them locally would freeze the digests in permanent mismatch (a fetch storm). Total ingest is bounded by the sync fetch guard (§13.5). v1 honesty: a malicious member can stuff the graph up to the guard — the same insider trust level that already lets any member revoke anyone (§3.5).

**v1 honesty — LWW can drop a concurrent edit.** Two members editing the *same node* within one gossip round: the lesser `(updated_at, rev, hash)` tuple loses silently. Acceptable for v1's research-notes workload (the UI shows `author + updated_at` so the loss is visible and recoverable from the loser's screen); vector clocks are deliberately out of scope (P2 lean). The origin-monotone stamp above bounds the *clock-poisoning* variant of this; it does not change the basic LWW trade.

### 13.5 Digest & gossip

Memory rides the election heartbeat as the **third digest passenger** (§5.3 table), reusing the proven §3.5 revocations machinery: compare digests → on mismatch, fetch the full graph over HMAC → merge → persist if changed.

**Digest construction (content-versioned).** Unlike the revocations digest — which hashes only the *set* of member_ids, because only membership matters — the memory digest must change on every **edit**, or LWW updates would never propagate. One line per node and per edge:

```
node line:  "n|" + id + "|" + rev + "|" + RFC3339Nano(updated_at)
edge line:  "e|" + from + "|" + to + "|" + relation

digest = sha256_hex( join("\n", sort(node_lines) ++ sort(edge_lines)) )
```

Node lines sorted, then edge lines sorted, concatenated in that order, LF-joined. **Archived nodes are included** — tombstones must converge too. The empty graph digests to sha256 of empty input (same convention as §3.5).

**Digest cost discipline.** The digest is **cached** in the daemon's memory service and recomputed only on mutation or merge. The election engine reads it through an injected `MemoryDigest func() string` and MUST NOT reload or re-hash `memory.json` on the 2 s heartbeat path. Emitted from both heartbeat sites (steady-state heartbeats and election announcements), `omitempty`. Wire cost of all three digest passengers together is ~192 bytes per heartbeat (three sha256 hex strings) — negligible even on 1–2 GB boxes.

**Sync flow** (mirrors §3.5 / Phase H-2, plus the response leg):

1. Heartbeat arrives carrying `memory_digest` ≠ local digest.
2. Per-peer in-flight dedup (concurrent heartbeats from the same sender trigger at most one fetch).
3. `GET /clan/memory` from the sender over HMAC + pinned TLS.
4. **Fetch guard: 4 MiB.** Responses larger than that are dropped with a warning. The guard is deliberately 2× the write cap so a graph transiently past 2 MiB (e.g. a merge union of two near-cap graphs) still syncs instead of wedging the Clan in permanent divergence; sustained growth is stopped by the write-path cap on every member.
5. `Merge` (§13.4) into the local graph; persist + recompute cached digest **only if the digest changed**; audit-log the application.
6. Fetch errors preserve local state untouched (next heartbeat retries).

**The response leg (follower → Orchestrator).** Heartbeats flow one way — Orchestrator → peers — so a digest only in the *request* would let follower edits reach nobody (the §3.5 revocations passenger quietly has this asymmetry; tolerable for revocations, fatal for a research graph where *every* member contributes). Therefore the **heartbeat response** carries the receiver's own `memory_digest` alongside the §5.4 accept fields; the Orchestrator compares each ack's digest against its own and runs the same fetch-and-merge (steps 2–6) against that follower on mismatch. Follower edits reach the Orchestrator within one heartbeat round; they reach every other member on the following round's request leg.

Eventual consistency bound: an Orchestrator edit reaches every connected member within one heartbeat round (~2 s) plus one fetch; a follower edit within two rounds (~4 s) plus two fetches; partitioned members converge on the union when the partition heals — same guarantee as revocations.

### 13.6 Write endpoints & authority

All three write paths are HMAC-authenticated (§2.3); the local CLI reaches them through the same loopback HMAC client every other subcommand uses.

| Endpoint | Body | Semantics |
|---|---|---|
| `GET /clan/memory` | — | Full graph JSON (the §13.1 `Graph` shape). |
| `GET /clan/memory/digest` | — | `{ "digest": "<hex>" }` — the cached §13.5 digest. Lets local UIs poll for change without transferring the graph. |
| `POST /clan/memory/node` | one `Node` | Create if `id` unknown; **update** if known (daemon bumps `rev = old.rev + 1`, stamps `updated_at` per the §13.4 origin-monotone rule). Caps enforced here (§13.1) → `409` with a clear error past them. |
| `POST /clan/memory/edge` | one `Edge` | Set-union add; duplicate `(from,to,relation)` is a no-op `200`. Caps enforced. |
| `POST /clan/memory/import` | `{ blueprint, mode }` | §13.10. `mode: "merge"` (default) or `"replace"`. **`replace` is loopback-CLI-only**: the daemon rejects it (`403`) unless the request originates from the local CLI on the loopback interface. A destructive remote primitive behind a *shared* key is exactly the §7.1 insider-forgery surface — remote members get `merge` only (M0 peer review, gemma4). |

**Provenance is daemon-set.** `provenance.author_member_id` is **always** overwritten with the authenticated member (`transport.OriginMember`) on create; client-supplied values are ignored. On update, the original `provenance` block is immutable — v1 does not track last-editor (lean); `updated_at`/`rev` show *that* it changed, the audit log on the accepting daemon shows *who* changed it. `created_by` on edges follows the same rule.

**Every memory mutation is audit-logged** locally by the daemon that accepts it (`tool: "memory.node" / "memory.edge" / "memory.import" / "memory.sync"`, §9.2 format). The audit trail of *who wrote what* stays per-member and local; only the resulting *content* gossips.

**Workspace caveat.** The Clan Workspace mutates memory by shelling the local CLI (loopback). Its HTTP surface (`/api/memory/*`) ships loopback-only and MUST be enumerated in the workspace PIN/bearer middleware when that lands — flagged here so the gate isn't forgotten.

### 13.7 Research sessions

A **research session** is an ordinary node (`type: "research_session"`) — no separate store. It groups a research effort into a unit that can be consolidated, analyzed, filtered, and exported.

- **Start:** any active member mints one (`minti-cland memory research start "<title>"` → UUIDv4, `status:"active"`, `session_id:""`). The starter is recorded in provenance; sessions are peer-equal like all writes.
- **Contribute:** a contribution is a node carrying `session_id: <session node id>` plus a `contributes_to` edge to the session node. `memory add --type finding --session <id> …` does both.
- **Close:** `memory research close <id>` flips the session node to `status:"archived"` (closed, not destroyed — title/body/edges survive). Contributions keep their own statuses.
- **Consolidate:** the Scribe's duty (§13.9) includes proposing a per-session summary node (deterministic id: `kind="session_summary", subject=<session id>` — so repeated consolidation passes *update* one node instead of spawning duplicates) and flagging near-duplicate findings.
- **Analyze / share:** the workspace memory tab filters by session (one cluster); `memory export --session <id>` exports just that effort's subgraph as a Blueprint (§13.10).

#### 13.7.1 System auto-events

The daemon itself writes a small set of system nodes (`source:"system"`, deterministic ids §13.3, `session_id:""`) so the graph keeps an ambient Clan chronicle without any human action: member joined / left / revoked (from membership transitions), and **failover** election milestones (reason ≠ bootstrap — steady-state re-elections are noise, failovers are history). Every member observes these independently and mints the identical node id; the union dedups.

### 13.8 Scribe role

The Scribe is the Clan's "court typewriter": symmetric to the Orchestrator but **inverted** — the strongest reasoning node *thinks*, the weakest capable node *remembers*. It gives 1–2 GB resurrected boxes a real job: run a tiny LLM ambiently and keep the minutes, so the busy Orchestrator never stops inferring to take notes.

- **Capability.** A member advertises `scribe_capable: true` in its capability advertisement (§4.2) when its local runtime is healthy and reports at least one available model it could distill with (any model qualifies in v1; the Scribe loop itself prefers the smallest resident model — llama3.2:1b / qwen2.5:0.5b class; a future `minti-pack-scribe-tiny` ships one). A `pinned_scribe: false|true` field rides the same advertisement.
- **Selection (inverse of §5.4 step 1).** Among `scribe_capable` **active** members, pick the **lowest** `reasoning_score`; ties broken by oldest `joined_at`, then lowest `member_id`. Any `pinned_scribe` member (that is scribe-capable) wins regardless; multiple pins → lowest `member_id` (mirror of §5.6).
- **Authority + propagation.** The **Orchestrator's** selection is authoritative: it computes the Scribe locally and emits the winner in the heartbeat `scribe` passenger (§5.3); peers adopt it on heartbeat accept. This avoids N members re-deriving conflicting scribes from divergent advertisement views — the same single-leader authority that already settles routing.
- **No lease.** Memory loss is non-fatal (the graph is fully replicated; only *new distillation* pauses), so the Scribe gets none of the lease machinery. The Orchestrator re-selects on its next heartbeat tick whenever the current Scribe stops being eligible: dropped from the active roster, advertisement stale, heartbeat-miss, or capability withdrawn.
- **Persistence + surfaces.** `current_scribe` (and the local `pinned_scribe` flag) persist in `clan.json` on change only. CLI: `minti-cland scribe [--json]`, `minti-cland pin-scribe --self|--clear`. The workspace marks the Scribe node with a quill in the mesh and memory tab.

**Edge cases (normative).**

| Case | Behavior |
|---|---|
| No `scribe_capable` active member | `current_scribe = ""` — distillation is simply off; manual + system memory still work. Not an error. |
| 1-node Clan | Scribe == Orchestrator == self. Fine: distillation is debounced + lowest-priority (§13.9), so the lone node thinks first and scribbles in the gaps. |
| Scribe == Orchestrator in an N-node Clan | Legal but never *chosen*: it only happens when exactly one member is scribe-capable and it also wins the orchestrator election. The inverse selection otherwise guarantees they differ. |
| Scribe dies | Next Orchestrator heartbeat tick re-selects among the survivors. In-flight proposals are lost (best-effort by design); accepted memories are already replicated. |

### 13.9 Scribe distillation duty

The elected Scribe runs a continuous, low-cost loop on its **small** model — the Karpathy move: activity in, curated memory out, a human between.

- **Inputs watched:** workspace chat session files for this Clan (`/var/lib/minti/workspace/sessions/<clan_id>/`), the Scribe's own local audit events, and fresh findings in **open** research sessions.
- **Loop:** debounced ticker (default 120 s; skips a beat when the local runtime is mid-inference). New activity since the last high-water mark is prompted into the smallest resident model via the local runtime-adapter (`127.0.0.1:7780/v1/chat/completions`) with a strict-JSON "archivist" prompt requesting **≤ 5 durable memories** per pass.
- **Tolerant parsing:** small models leak prose around JSON. The parser extracts the first `[ … ]` block, drops entries that fail validation, and never lets a garbage completion corrupt the graph — worst case, the pass yields nothing and logs why.
- **Everything lands gated:** `status:"proposed"`, `source:"scribe"`, plus `contributes_to` edges when the input came from a research session. **Nothing the Scribe writes becomes `active` without an explicit human promote** (workspace review lane, or a node update via CLI). Auto-promotion and BYO-Claude orchestration ride the Hermes-as-harness direction later — out of v1.
- **Cost caps:** smallest model, debounce, per-pass proposal cap, skip-if-busy. The Scribe must never contend with the Orchestrator for inference — it is the lowest-priority consumer of the weakest box.
- **Pending-proposal budget.** Proposed nodes count toward the global 2,000-node cap (§13.1), so an unattended Scribe could otherwise grind the Clan's write budget away 5 nodes per pass. The Scribe therefore refuses to mint *new* proposals while more than **200** of its own un-reviewed (`status:"proposed"`, `source:"scribe"`) nodes exist — updating its existing proposals (e.g. the deterministic per-session summaries) stays allowed. Tunable; the floor matters, not the number (M0 peer review, gemma4 + qwen3.6 consensus).

### 13.10 Clan Blueprint (export / import)

A single portable JSON file — the Clan's distilled knowledge, snapshot at a moment — that a **fresh Clan imports at `create` to pick up where a previous effort left off**, or an existing Clan merges in.

```json
{
  "kind":            "minti-clan-blueprint",
  "format_version":  1,
  "exported_at":     "RFC3339",
  "source_clan":     "sha256:<hex of clan_id>",
  "session_filter":  "<research_session id, or \"\" for the full graph>",
  "stats":           { "nodes": 0, "edges": 0, "proposed": 0, "archived": 0 },
  "graph":           { "format_version": 1, "nodes": [...], "edges": [...] },
  "signature":       "",
  "checksum_sha256": "<hex>"
}
```

- **Checksum:** sha256-hex over the document serialized as compact JSON in the field order above with `checksum_sha256` set to `""` (the toolexec canonical-bytes idiom; the §13.4 normative canonical-bytes rule — declaration-ordered structs, explicit tags, conformance test — applies to this document too). Verified on **every** import; any mismatch rejects the file outright. `signature` is reserved-empty in v1 (OQ-10) — the checksum proves *integrity*, not *authorship*; v1-honesty posture, same as toolexec's.
- **`source_clan` is pre-hashed** — a Blueprint never carries the raw `clan_id` (it is quasi-public but there's no reason to leak it in a file built for sharing).
- **Export:** `minti-cland memory export [--out f] [--session <id>] [--strip-authors]` (CLI fetches `GET /clan/memory`, builds the file client-side); the workspace offers a download behind a **privacy confirm modal**. `--session` exports the session node + everything carrying its `session_id` or reachable by its `contributes_to` edges. `--strip-authors` pseudonymizes member ids (`provenance.author_member_id`, `created_by`, and `member`-type node ids): distinct ids are sorted, then mapped to `member-1 … member-N` — stable within the file, meaningless outside it.
- **Import at create:** `minti-cland create --from-blueprint f.json` — after the normal `create` flow, verify checksum + `format_version`, flip every node's `provenance.source` to `"import"` (deterministically, preserving `updated_at`/`rev`, so re-importing the same file stays idempotent under merge), seed `memory.json`. The new Clan starts with the inherited graph already in place; it gossips to joiners like any other state.
- **Import into an existing Clan:** `minti-cland memory import f.json [--replace]` → `POST /clan/memory/import`. Default mode is **merge** (the §13.4 union — safe, idempotent, and the result gossips Clan-wide for free). `--replace` discards the local graph first; it is destructive, demands the explicit flag, is audit-logged loudly, and is **loopback-only** at the daemon (§13.6). Import is a *local* operation by design — a member imports into its own daemon and gossip distributes the result; there is no remote-import use case that merge-gossip doesn't already cover with less attack surface. Note the v1 semantics of `--replace` under gossip: it clears *this member's* copy, after which the next digest mismatch merges peers' graphs back in — Clan-wide forgetting requires every member to replace (or a fresh Clan from a Blueprint); true coordinated compaction is OQ-9.

### 13.11 Privacy & v1 honesty

- **No secrets in nodes.** Keys, tokens, and credentials do not belong in memory; the graph is Clan-replicated by design and exportable by intent. (Policy enforcement is human + prompt discipline in v1 — there is no scanner.)
- **Distillates carry chat content.** The Scribe reads workspace chats; its proposals can quote them. That is why `memory.json` is mode 0600, why the export path *always* warns ("may contain distilled chat content"), and why `--strip-authors` exists. No silent export: the CLI prints the warning before writing, the workspace requires the confirm modal.
- **Author attribution is honest-but-insider-forgeable.** `author_member_id` is set by the accepting daemon from the HMAC-authenticated origin — but v1's shared `clan_key` means HMAC proves *some member* sent it, not *which* (§7.1 v1 honesty note applies verbatim). v2 per-member keys close this for memory exactly as for tool tokens.
- **The graph is only as trustworthy as the Clan.** Any active member can edit or archive any node (peer-equal writes); provenance + audit logs + the review lane are the recourse, not cryptography. This matches the Clan's overall v1 trust model and is stated here so nobody mistakes the memory graph for an integrity-protected ledger.

---

*End of Clan Protocol Spec v0.1. Wire-level conformance tests will live in `tests/protocol/`.*
