# M8 Phase 0 — peer-review triage (2026-06-06)

Run via `scripts/m8-peer-review.py` against the M8 protocol spec
(`docs/clan-protocol.md` §3.4 in v0.3) + the approved M8 implementation
plan (`~/.claude/plans/we-need-to-plan-async-candle.md`).

Three local reviewers (all on the daily-driver Windows host's Ollama
RTX-5090):
- `qwen3.6:latest` (think:false; 35 s; 11.7 KB clean)
- `deepseek-r1:32b` (default; 45 s; 2.9 KB clean)
- `gemma4:31b` (default; 81 s; 5.4 KB clean)

## Verdicts as returned

| Reviewer | Verdict |
|---|---|
| qwen3.6 | ship after addressing items 3 and 7 |
| deepseek-r1 | ship after addressing items 4 (entropy), 5 (rate limits), and 6 (state consistency) |
| gemma4 | ship after addressing items 4 (increase SAS entropy) and 5 (add IP-allowlist for delivery) |

## Folded into spec v0.3

### F1 — Bump SAS entropy 20 → 30 bits (deepseek + gemma4)
gemma4 surfaced the **pubkey-grinding attack** the original 20-bit design
missed: an active MITM who intercepts `knock_id` can generate ephemeral
X25519 keypairs offline and run HKDF until they find a pubkey whose
derived SAS matches the operator's TUI. 20 bits = milliseconds on modern
hardware; 30 bits = minutes-to-hours, exceeds 5-min TTL. Render the SAS
as 9 decimal digits formatted "XXXX-XXXXX".

deepseek phrased it as "marginal security" without naming the grinding
attack; gemma4 wrote the canonical attack derivation. Both verdicted on
the same fix.

**Spec change**: `docs/clan-protocol.md` §3.4 cryptographic-core block —
`code_bytes` still 4 bytes but the rendered code is now 9 digits via
`mod 10⁹`; threat-model table entry updated to "≈10⁹ HKDF iterations".

### F2 — IP-allowlist on joiner-side `/clan/knock-deliver` (deepseek + gemma4)
gemma4 made the concrete proposal: "Joiner should implement an IP-
allowlist for delivery. Since the Joiner initiated the knock to a
specific receiver_addr, it should only accept /clan/knock-deliver
requests originating from that specific IP address." deepseek phrased
it more loosely as "an allowlist could enhance resilience". Same intent.

**Spec change**: §3.4 new "Delivery allowlist (joiner side)" subsection;
rate-limit table updated.

### F3 — Joiner-side mutual SAS confirmation (qwen3.6 only — CRITICAL)
Single-reviewer finding but the most fundamental of all flagged issues.
qwen3.6 noted that the spec said "operator visually verifies before
accepting" but never said the **joiner** also verifies. Without joiner-
side confirmation, a MITM who substitutes pubkeys can deliver an
attacker-controlled blob to the joiner directly — joiner derives an
attacker-controlled key, opens the blob successfully (because GCM tag
verifies with that key), and joins the attacker's "Clan". The receiver-
operator's SAS check is irrelevant because no traffic reaches the
receiver at all.

This isn't single-reviewer as in "low confidence" — gemma4 and deepseek
both *implicitly* assumed mutual verification when they wrote item 3
"SAS provides necessary security bound". The spec didn't state it
explicitly, so the implementation could omit it. Treated as a blocker.

**Spec change**: §3.4 new "Mutual SAS confirmation (CRITICAL)" block;
joiner-side state machine extended with explicit `y/n` confirmation
step BEFORE arming `/clan/knock-deliver` acceptor; threat-model table
gains a new row for "MITM substitutes pubkeys, joiner accepts blindly".

### F4 — Document `clan_cert_priv` distribution (qwen3.6 only — DOC)
qwen3.6 flagged that the sealed WelcomeResponse includes the Clan's TLS
private key, which means any joiner admitted via §3.4 can impersonate
the Clan's TLS identity. This is true and matches §10/R1 (v1 unitary-
trust model) but wasn't explicit in §3.4.

**Spec change**: §3.4 new "Trust transferred on accept" paragraph.

## Overruled with reason

### O1 — deepseek item 6 ("ensure state consistency in race conditions")
deepseek argued the receiver-side state might be inconsistent if two
operators concurrently accept the same knock: "Implement mechanisms to
synchronize receiver states post-acceptance, preventing inconsistent
outcomes when multiple operators act simultaneously."

gemma4 explicitly considered the same scenario and overruled it:
"The fact that a second operator might see a 'success' in their TUI
while the joiner rejected the second blob is a minor UX inconsistency,
not a protocol failure."

**Reason for overrule**: the joiner's CAS guarantees only one accept
succeeds. Both receivers' audit logs faithfully record what they did
(POST sent + status received); the truth-of-record is the joiner's
final state, which is single-valued. The "two TUIs both show success
briefly" UX is acceptable for v1 — the audit log clearly distinguishes
which acceptor's blob actually landed.

## Non-fixes — items reviewers agreed were fine

All three explicitly approved:
- HKDF construction (salt namespacing, `info` binding with `clan_id` +
  `knock_id` + both pubkeys).
- Deterministic nonce (safe because each `(joiner_pub, receiver_pub)`
  pair derives a unique key).
- AES-GCM tag as receiver-authenticity proof (no separate signature).
- Anonymous `/clan/knock` endpoint (mirrors §3.2 `/clan/join` precedent).
- Cross-Clan replay defence (`clan_id` in HKDF info).
- Per-knock_id CAS on joiner (single-accept guarantee).
- TUI poll cadence (5 s for a 300 s TTL is acceptable).

## Net protocol delta

`docs/clan-protocol.md` v0.2 → v0.3:
- +1 new subsection (§3.4 Knock flow)
- §3.4 Revocation renumbered → §3.5
- §3.5 Self-leave renumbered → §3.6
- §3.4 contains: wire shapes, crypto core (X25519 + HKDF + AES-GCM),
  30-bit SAS derivation, mutual SAS confirmation (CRITICAL), delivery
  allowlist, state machine, acceptance authority, rate limits, threat
  model, TTL bounds, identity persistence, trust transfer.

Net Phase 0 doc growth: ~150 lines.

## Gate for Phase 1

Phase 1 (Go implementation) can proceed: all three reviewers verdicted
"ship after fixing" (none blocked outright); all consensus and high-
severity findings are folded into the spec; the overrule has gemma4's
explicit support and the protocol-level reasoning is documented above.

The Phase 1 implementation MUST match the v0.3 spec exactly — in
particular the mutual SAS confirmation step (F3) is load-bearing for
security and is the easiest place for an implementation shortcut to
silently break the protocol.
