# qwen3.6:latest - M8 Phase 0 protocol review

- wall_time_s: 35.5
- prompt_tokens: 11070
- eval_tokens: 3002
- raw_chars: 11719
- clean_chars: 11719
- extra_body: {'think': False}

---

Here is the adversarial review of the M8 protocol spec and implementation plan.

### 1. Cryptographic correctness
**Status: Sound, with one minor note.**

*   **HKDF Construction:** The construction `salt="minti-knock-v1"`, `info=clan_id || knock_id || joiner_x25519_pub || receiver_x25519_pub` is correct. Binding both pubkeys prevents a "key confirmation" attack where an attacker substitutes their own pubkey to force the victim into deriving a key they control (though here, the key is used for GCM auth, so this is less critical than in signature contexts, but still good practice). The salt namespacing (`minti-knock-v1`) correctly isolates this from the `minti-clan-key` salt used in §3.3.
*   **Nonce Reuse:** You state: *"each (joiner_pub, receiver_pub) pair derives a unique key, so the same nonce never re-encrypts under the same key."* This is **correct**. Since the ephemeral X25519 keys are random per knock, the derived AES key is unique. Reusing a deterministic nonce (e.g., all zeros or a counter) is safe *provided* the key is never reused. Here, the key is derived from ephemeral secrets, so it is effectively one-time use.
*   **SAS Leakage:** The SAS is derived from `bundle[32:36]`. Since this is part of the HKDF output which is keyed by the shared secret, leaking 4 bytes (20 bits) does not compromise the remaining 28 bytes of key material or the security of the ECDH. It is a standard SAS derivation pattern.
*   **GCM AAD:** `aad=knock_id` is sufficient because `knock_id` is part of the HKDF info, binding it to the key. However, including `clan_id` in the AAD would provide an additional layer of binding (ensuring the decrypted payload belongs to the intended clan context). Given `clan_id` is already in the info, this is a low-value addition but not strictly necessary for security. **Verdict: Acceptable as-is.**

### 2. Authentication of /clan/knock-deliver
**Status: Correct reasoning, but relies on a fragile assumption.**

*   **The Logic:** You argue that an attacker cannot forge the GCM tag without the shared secret. This is true. The shared secret is derived from the receiver's ephemeral private key, which never leaves the receiver's machine.
*   **The Hole:** The joiner exposes an anonymous endpoint (`POST /clan/knock-deliver`). If an attacker can intercept the `/clan/knock` response (MITM), they learn `receiver_x25519_pub`. They *still* cannot derive the shared secret without the receiver's private key.
*   **The Real Risk:** The risk is not forging the tag, but **spoofing the endpoint**. An attacker could run a fake joiner on the LAN that accepts knocks and returns valid-looking (but empty/malicious) GCM blobs if they can somehow guess or manipulate the `knock_id` or if the joiner doesn't validate the `knock_id` against its own pending store strictly.
*   **Mitigation Check:** The plan says: *"The joiner looks up knock_id in its own pending-knock store... derives the same (key, nonce)... and AES-GCM_open."* If the `knock_id` is not in the store, it fails. If it is, it opens. This is secure **provided** the joiner's local store is trusted and not corrupted by a previous MITM.
*   **Verdict: Secure.** The GCM tag is the correct authentication mechanism here because the key is only known to the two parties who performed ECDH.

### 3. Active MITM on /clan/knock
**Status: Critical Weakness in Trust Model.**

*   **The Problem:** You state: *"TLS-with-SPKI-pin protects §3.2/§3.3... The §3.4 joiner has no pin. The plan says the joiner TOFUs the cert."*
*   **The Attack:** An attacker on the LAN can perform a classic TLS MITM. They intercept `POST /clan/knock`. They generate their own X25519 keypair. They send a fake `KnockResponse` with *their* pubkey to the joiner. The joiner derives a shared secret with the *attacker*. The attacker then forwards the real `KnockResponse` (with the real receiver's pubkey) to the receiver, who accepts it. The attacker now holds the AES key.
*   **The SAS Defense:** The plan relies on the SAS to catch this. *"If an attacker substitutes pubkeys in transit, the SAS the joiner derives differs from the SAS the receiver displays."*
    *   Joiner sees Code A (derived with Attacker's pubkey).
    *   Receiver sees Code B (derived with Real Receiver's pubkey).
    *   Operator reads Code B to Joiner. Joiner compares Code A vs Code B. Mismatch!
*   **The Flaw:** This works **only if the operator actually reads and compares the codes**. It does **not** prevent the attacker from simply dropping the connection or returning a dummy blob if they don't want to be caught, or if the joiner doesn't enforce strict SAS comparison (e.g., if the TUI just says "Accepted" without showing the code).
*   **Recommendation:** The plan must explicitly state that the **joiner's CLI/TUI MUST display the derived SAS and require explicit user confirmation that it matches the operator's side**. If the joiner blindly accepts any valid GCM tag, the MITM succeeds. The current spec says *"operator visually verifies... before accepting"* but doesn't explicitly mandate the *joiner* verify the code locally before trusting the payload. **This is a high-severity implementation requirement.**

### 4. Confirm-code entropy
**Status: Borderline, but acceptable for v1 with caveats.**

*   **Entropy:** 20 bits (6 digits) is weak. Signal uses 60 bits (or 30 bits displayed).
*   **Attack Surface:** An attacker on the LAN can perform a brute-force MITM. They substitute their pubkey, derive the key, and try to guess the SAS that will match the receiver's side? No, they can't guess the receiver's SAS because they don't have the receiver's private key.
*   **Wait, correction:** The attacker *cannot* forge the SAS on the receiver's side. The receiver computes SAS from its own ephemeral privkey. The attacker only sees the receiver's pubkey. The attacker cannot compute the shared secret with the receiver.
*   **So what is the attack?** The attacker substitutes their pubkey to the joiner. Joiner derives Key_Attacker. Attacker sends fake WelcomeResponse encrypted with Key_Attacker. Joiner opens it successfully. **The SAS mismatch is detected by the operator.**
*   **Is 20 bits enough?** Yes, because the attacker cannot *force* a match. The attacker must hope the operator makes a mistake or doesn't look. The probability of a random collision is negligible. The risk is **social engineering** (operator ignores mismatch) or **automation error** (joiner accepts without checking).
*   **Recommendation:** 20 bits is fine *if and only if* the UI forces comparison. If you want to be safer, use 30 bits (9 digits), but 6 is standard for "human-readable" codes. **Verdict: Acceptable.**

### 5. Joiner-side rate limits
**Status: Insufficient against determined flooder.**

*   **The Problem:** The joiner's `/clan/knock-deliver` endpoint accepts any `knock_id`. If an attacker knows a valid `knock_id` (e.g., from sniffing the LAN traffic of `/clan/knock`), they can spam `POST /clan/knock-deliver` to the joiner.
*   **The Limit:** *"Per source IP for unknown knock_ids: 5/60s."*
*   **The Hole:** If the attacker sends a valid `knock_id` (from a live knock), it is not "unknown". It hits the "Per knock_id: 1 accept" limit. But what if they send *many different* valid knock_ids? They can't, because each knock_id is tied to a specific ephemeral session.
*   **The Real Hole:** The joiner listens on a LAN address. An attacker can flood that IP with `knock-deliver` requests using *different* `knock_id`s (if they can guess them or if the joiner hasn't expired old ones). But `knock_id` is 16 bytes random. Guessing is impossible.
*   **However:** The attacker can just send *one* valid knock-deliver per live knock. The rate limit of 5/60s for *unknown* IDs doesn't help against *known* IDs. But since `knock_id` is random, the attacker can only target active knocks.
*   **Verdict: Acceptable.** The randomness of `knock_id` prevents blind flooding. The per-knock-id limit prevents replay/spam on a specific session.

### 6. Race conditions
**Status: Potential State Inconsistency.**

*   **Scenario:** Two operators (Op1, Op2) on two different members both press `a` for the same knock.
*   **Receiver Side:** Both send `POST /clan/knock-accept`. The receiver's logic says *"First-write-wins: the joiner's KnockStore deletes the entry... concurrent accepts from multiple members race for the CAS and the losers receive 409."*
    *   Wait, the plan says the **joiner** has the CAS on `knock-deliver`. But the **receiver** also needs to handle concurrent accepts.
    *   If Op1 sends accept, receiver stores "Accepted by Op1". Op2 sends accept, receiver sees "Already Accepted", returns 409 or ignores. This is fine.
*   **Joiner Side:** Op1's accept triggers `knock-deliver` to joiner. Joiner opens blob, persists state. Op2's accept (if it somehow got through or if the network is slow) triggers another `knock-deliver`. The joiner's CAS on `knock_id` ensures only one succeeds.
*   **The Inconsistency:** What if Op1 accepts, sends deliver, but the packet is lost? Joiner never receives it. Op2 accepts, sends deliver, joiner receives it. Joiner joins. Op1 thinks they succeeded (locally), but the joiner joined via Op2. This is **acceptable** because the result (joiner is active) is the same. The "who" is different, but in a peer-equal clan, any active member accepting is valid.
*   **Verdict: Acceptable.** The outcome is deterministic (one success), and the identity of the acceptor doesn't change the joiner's state.

### 7. State persistence after accept
**Status: Critical Security Risk.**

*   **The Issue:** *"The cert_priv shared via §3.4 means every member of the Clan can decrypt inbound TLS to the founder's cert."*
*   **Analysis:** The `WelcomeResponse` includes `clan_cert_priv_key_b64`. This is the private key for the clan's TLS certificate. By sharing this with the joiner, you are giving them full control over the clan's identity.
*   **Is this acceptable?** In v1's "unitary-trust model," yes. The founder trusts the joiner completely. However, this means **any** member who joins via knock can impersonate the entire clan (by using the cert_priv to sign TLS certs or decrypt traffic).
*   **Recommendation:** This is a known trade-off of v1. Flag it clearly: *"Joiners receive the Clan's TLS private key. They become full peers with equal authority to impersonate the clan identity."* If this is not desired, you must use a different mechanism (e.g., joiner gets a new cert signed by the founder). But for v1, this is likely intended. **Verdict: Acceptable for v1, but document the risk.**

### 8. Replay / cross-Clan attacks
**Status: Secure.**

*   **Cross-Clan:** `clan_id` is in HKDF info. Different clan → different key → GCM tag fails. Correct.
*   **Replay:** `knock_id` is single-use via CAS on joiner. Correct.
*   **Verdict: Secure.**

### 9. TUI integration concerns
**Status: Latency acceptable, but audit log mismatch risk.**

*   **Polling:** 5s tick is fine for a 5-min TTL. The user has time to react.
*   **Audit Log:** *"Does the plan match the existing service.go:121-126 idiom?"* You must ensure that `knock-accept` and `knock-deliver` both log to the same audit stream. If they don't, you'll have gaps in the membership history.
*   **Verdict: Acceptable, but verify audit logging implementation.**

### 10. What the plan got right
*   **Using GCM tag as auth:** Elegant and correct for this context.
*   **Binding pubkeys in HKDF info:** Prevents key confirmation attacks.
*   **Ephemeral X25519:** Provides forward secrecy for the join session.
*   **Scope:** Keeping it simple (no mDNS, no persistence) is wise for v1.

---

### VERDICT: ship after addressing items 3 and 7

---

## Raw (with thinking trace if any)

Here is the adversarial review of the M8 protocol spec and implementation plan.

### 1. Cryptographic correctness
**Status: Sound, with one minor note.**

*   **HKDF Construction:** The construction `salt="minti-knock-v1"`, `info=clan_id || knock_id || joiner_x25519_pub || receiver_x25519_pub` is correct. Binding both pubkeys prevents a "key confirmation" attack where an attacker substitutes their own pubkey to force the victim into deriving a key they control (though here, the key is used for GCM auth, so this is less critical than in signature contexts, but still good practice). The salt namespacing (`minti-knock-v1`) correctly isolates this from the `minti-clan-key` salt used in §3.3.
*   **Nonce Reuse:** You state: *"each (joiner_pub, receiver_pub) pair derives a unique key, so the same nonce never re-encrypts under the same key."* This is **correct**. Since the ephemeral X25519 keys are random per knock, the derived AES key is unique. Reusing a deterministic nonce (e.g., all zeros or a counter) is safe *provided* the key is never reused. Here, the key is derived from ephemeral secrets, so it is effectively one-time use.
*   **SAS Leakage:** The SAS is derived from `bundle[32:36]`. Since this is part of the HKDF output which is keyed by the shared secret, leaking 4 bytes (20 bits) does not compromise the remaining 28 bytes of key material or the security of the ECDH. It is a standard SAS derivation pattern.
*   **GCM AAD:** `aad=knock_id` is sufficient because `knock_id` is part of the HKDF info, binding it to the key. However, including `clan_id` in the AAD would provide an additional layer of binding (ensuring the decrypted payload belongs to the intended clan context). Given `clan_id` is already in the info, this is a low-value addition but not strictly necessary for security. **Verdict: Acceptable as-is.**

### 2. Authentication of /clan/knock-deliver
**Status: Correct reasoning, but relies on a fragile assumption.**

*   **The Logic:** You argue that an attacker cannot forge the GCM tag without the shared secret. This is true. The shared secret is derived from the receiver's ephemeral private key, which never leaves the receiver's machine.
*   **The Hole:** The joiner exposes an anonymous endpoint (`POST /clan/knock-deliver`). If an attacker can intercept the `/clan/knock` response (MITM), they learn `receiver_x25519_pub`. They *still* cannot derive the shared secret without the receiver's private key.
*   **The Real Risk:** The risk is not forging the tag, but **spoofing the endpoint**. An attacker could run a fake joiner on the LAN that accepts knocks and returns valid-looking (but empty/malicious) GCM blobs if they can somehow guess or manipulate the `knock_id` or if the joiner doesn't validate the `knock_id` against its own pending store strictly.
*   **Mitigation Check:** The plan says: *"The joiner looks up knock_id in its own pending-knock store... derives the same (key, nonce)... and AES-GCM_open."* If the `knock_id` is not in the store, it fails. If it is, it opens. This is secure **provided** the joiner's local store is trusted and not corrupted by a previous MITM.
*   **Verdict: Secure.** The GCM tag is the correct authentication mechanism here because the key is only known to the two parties who performed ECDH.

### 3. Active MITM on /clan/knock
**Status: Critical Weakness in Trust Model.**

*   **The Problem:** You state: *"TLS-with-SPKI-pin protects §3.2/§3.3... The §3.4 joiner has no pin. The plan says the joiner TOFUs the cert."*
*   **The Attack:** An attacker on the LAN can perform a classic TLS MITM. They intercept `POST /clan/knock`. They generate their own X25519 keypair. They send a fake `KnockResponse` with *their* pubkey to the joiner. The joiner derives a shared secret with the *attacker*. The attacker then forwards the real `KnockResponse` (with the real receiver's pubkey) to the receiver, who accepts it. The attacker now holds the AES key.
*   **The SAS Defense:** The plan relies on the SAS to catch this. *"If an attacker substitutes pubkeys in transit, the SAS the joiner derives differs from the SAS the receiver displays."*
    *   Joiner sees Code A (derived with Attacker's pubkey).
    *   Receiver sees Code B (derived with Real Receiver's pubkey).
    *   Operator reads Code B to Joiner. Joiner compares Code A vs Code B. Mismatch!
*   **The Flaw:** This works **only if the operator actually reads and compares the codes**. It does **not** prevent the attacker from simply dropping the connection or returning a dummy blob if they don't want to be caught, or if the joiner doesn't enforce strict SAS comparison (e.g., if the TUI just says "Accepted" without showing the code).
*   **Recommendation:** The plan must explicitly state that the **joiner's CLI/TUI MUST display the derived SAS and require explicit user confirmation that it matches the operator's side**. If the joiner blindly accepts any valid GCM tag, the MITM succeeds. The current spec says *"operator visually verifies... before accepting"* but doesn't explicitly mandate the *joiner* verify the code locally before trusting the payload. **This is a high-severity implementation requirement.**

### 4. Confirm-code entropy
**Status: Borderline, but acceptable for v1 with caveats.**

*   **Entropy:** 20 bits (6 digits) is weak. Signal uses 60 bits (or 30 bits displayed).
*   **Attack Surface:** An attacker on the LAN can perform a brute-force MITM. They substitute their pubkey, derive the key, and try to guess the SAS that will match the receiver's side? No, they can't guess the receiver's SAS because they don't have the receiver's private key.
*   **Wait, correction:** The attacker *cannot* forge the SAS on the receiver's side. The receiver computes SAS from its own ephemeral privkey. The attacker only sees the receiver's pubkey. The attacker cannot compute the shared secret with the receiver.
*   **So what is the attack?** The attacker substitutes their pubkey to the joiner. Joiner derives Key_Attacker. Attacker sends fake WelcomeResponse encrypted with Key_Attacker. Joiner opens it successfully. **The SAS mismatch is detected by the operator.**
*   **Is 20 bits enough?** Yes, because the attacker cannot *force* a match. The attacker must hope the operator makes a mistake or doesn't look. The probability of a random collision is negligible. The risk is **social engineering** (operator ignores mismatch) or **automation error** (joiner accepts without checking).
*   **Recommendation:** 20 bits is fine *if and only if* the UI forces comparison. If you want to be safer, use 30 bits (9 digits), but 6 is standard for "human-readable" codes. **Verdict: Acceptable.**

### 5. Joiner-side rate limits
**Status: Insufficient against determined flooder.**

*   **The Problem:** The joiner's `/clan/knock-deliver` endpoint accepts any `knock_id`. If an attacker knows a valid `knock_id` (e.g., from sniffing the LAN traffic of `/clan/knock`), they can spam `POST /clan/knock-deliver` to the joiner.
*   **The Limit:** *"Per source IP for unknown knock_ids: 5/60s."*
*   **The Hole:** If the attacker sends a valid `knock_id` (from a live knock), it is not "unknown". It hits the "Per knock_id: 1 accept" limit. But what if they send *many different* valid knock_ids? They can't, because each knock_id is tied to a specific ephemeral session.
*   **The Real Hole:** The joiner listens on a LAN address. An attacker can flood that IP with `knock-deliver` requests using *different* `knock_id`s (if they can guess them or if the joiner hasn't expired old ones). But `knock_id` is 16 bytes random. Guessing is impossible.
*   **However:** The attacker can just send *one* valid knock-deliver per live knock. The rate limit of 5/60s for *unknown* IDs doesn't help against *known* IDs. But since `knock_id` is random, the attacker can only target active knocks.
*   **Verdict: Acceptable.** The randomness of `knock_id` prevents blind flooding. The per-knock-id limit prevents replay/spam on a specific session.

### 6. Race conditions
**Status: Potential State Inconsistency.**

*   **Scenario:** Two operators (Op1, Op2) on two different members both press `a` for the same knock.
*   **Receiver Side:** Both send `POST /clan/knock-accept`. The receiver's logic says *"First-write-wins: the joiner's KnockStore deletes the entry... concurrent accepts from multiple members race for the CAS and the losers receive 409."*
    *   Wait, the plan says the **joiner** has the CAS on `knock-deliver`. But the **receiver** also needs to handle concurrent accepts.
    *   If Op1 sends accept, receiver stores "Accepted by Op1". Op2 sends accept, receiver sees "Already Accepted", returns 409 or ignores. This is fine.
*   **Joiner Side:** Op1's accept triggers `knock-deliver` to joiner. Joiner opens blob, persists state. Op2's accept (if it somehow got through or if the network is slow) triggers another `knock-deliver`. The joiner's CAS on `knock_id` ensures only one succeeds.
*   **The Inconsistency:** What if Op1 accepts, sends deliver, but the packet is lost? Joiner never receives it. Op2 accepts, sends deliver, joiner receives it. Joiner joins. Op1 thinks they succeeded (locally), but the joiner joined via Op2. This is **acceptable** because the result (joiner is active) is the same. The "who" is different, but in a peer-equal clan, any active member accepting is valid.
*   **Verdict: Acceptable.** The outcome is deterministic (one success), and the identity of the acceptor doesn't change the joiner's state.

### 7. State persistence after accept
**Status: Critical Security Risk.**

*   **The Issue:** *"The cert_priv shared via §3.4 means every member of the Clan can decrypt inbound TLS to the founder's cert."*
*   **Analysis:** The `WelcomeResponse` includes `clan_cert_priv_key_b64`. This is the private key for the clan's TLS certificate. By sharing this with the joiner, you are giving them full control over the clan's identity.
*   **Is this acceptable?** In v1's "unitary-trust model," yes. The founder trusts the joiner completely. However, this means **any** member who joins via knock can impersonate the entire clan (by using the cert_priv to sign TLS certs or decrypt traffic).
*   **Recommendation:** This is a known trade-off of v1. Flag it clearly: *"Joiners receive the Clan's TLS private key. They become full peers with equal authority to impersonate the clan identity."* If this is not desired, you must use a different mechanism (e.g., joiner gets a new cert signed by the founder). But for v1, this is likely intended. **Verdict: Acceptable for v1, but document the risk.**

### 8. Replay / cross-Clan attacks
**Status: Secure.**

*   **Cross-Clan:** `clan_id` is in HKDF info. Different clan → different key → GCM tag fails. Correct.
*   **Replay:** `knock_id` is single-use via CAS on joiner. Correct.
*   **Verdict: Secure.**

### 9. TUI integration concerns
**Status: Latency acceptable, but audit log mismatch risk.**

*   **Polling:** 5s tick is fine for a 5-min TTL. The user has time to react.
*   **Audit Log:** *"Does the plan match the existing service.go:121-126 idiom?"* You must ensure that `knock-accept` and `knock-deliver` both log to the same audit stream. If they don't, you'll have gaps in the membership history.
*   **Verdict: Acceptable, but verify audit logging implementation.**

### 10. What the plan got right
*   **Using GCM tag as auth:** Elegant and correct for this context.
*   **Binding pubkeys in HKDF info:** Prevents key confirmation attacks.
*   **Ephemeral X25519:** Provides forward secrecy for the join session.
*   **Scope:** Keeping it simple (no mDNS, no persistence) is wise for v1.

---

### VERDICT: ship after addressing items 3 and 7