# gemma4:31b - M8 Phase 0 protocol review

- wall_time_s: 80.9
- prompt_tokens: 11549
- eval_tokens: 3167
- raw_chars: 5426
- clean_chars: 5426
- extra_body: {}

---

This review focuses on the M8 "Knock" flow. I have evaluated the protocol against the priority list provided.

### 1. Cryptographic Correctness
**Verdict: Sound.**
*   **HKDF Construction:** The binding of `clan_id`, `knock_id`, and both pubkeys into the `info` field is correct. It prevents precomputation attacks and ensures that a compromise of one session key does not leak others. 
*   **Nonce Safety:** Reusing a deterministic nonce is safe here because the AES key is derived from an ephemeral X25519 exchange. Since $\text{Key} = f(\text{priv}_j, \text{pub}_r)$, and at least one party generates a fresh ephemeral key per knock, the $(\text{key}, \text{nonce})$ pair is unique for every session.
*   **SAS Leakage:** The SAS is derived from 4 bytes of the HKDF output. Since HKDF-SHA256 is a PRF, these bytes are computationally independent of the AES key (the first 32 bytes). No leakage occurs.
*   **GCM AAD:** Using `aad=knock_id` is sufficient to prevent moving a valid ciphertext from one knock session to another. While adding `clan_id` would be "belt-and-suspenders," it is not required for correctness as the key itself is already bound to the `clan_id`.

### 2. Authentication of `/clan/knock-deliver`
**Verdict: Correct.**
The reasoning holds. An attacker who scrapes `/clan/knock` only sees public keys. To forge a valid GCM tag for the delivery blob, they would need the shared secret $S = \text{X25519}(\text{priv}_j, \text{pub}_r)$. They cannot obtain this without either compromising a private key or successfully MITM-ing the initial knock leg. If they MITM'd the knock leg, they would have had to substitute pubkeys, which leads to the SAS mismatch discussed in Item 3.

### 3. Active MITM on `/clan/knock`
**Verdict: Correct (Defense-in-Depth).**
The "TOFU" of the certificate is a weakness, but the SAS provides the necessary security bound. In an active MITM scenario where the attacker proxies and substitutes pubkeys ($\text{Joiner} \leftrightarrow \text{Attacker} \leftrightarrow \text{Receiver}$), the Joiner derives $SAS_A$ and the Receiver derives $SAS_B$. The visual verification over the side channel is the primary authentication mechanism.

### 4. Confirm-code Entropy
**Verdict: Weak / Borderline.**
The plan claims $\approx 500\text{k}$ MITM attempts are needed to land a collision. However, this assumes the attacker is guessing the SAS. In reality, an active MITM attacker who has intercepted the `knock_id` can perform a **targeted pubkey brute-force**.

An attacker can generate millions of ephemeral X25519 keys and compute $\text{HKDF}(\dots)$ until they find a public key that produces the *exact* 6-digit SAS currently displayed on the operator's TUI. On modern hardware, $2^{20}$ HKDF iterations take milliseconds.
*   **Risk:** An attacker could potentially substitute their pubkey for one that results in the same SAS before the operator presses `a`.
*   **Mitigation:** While this is difficult to do "live" without knowing exactly when the operator is looking, 20 bits is too low for a cryptographic guarantee. I recommend increasing this to **30 bits (approx. 1 billion)** or using an 8-digit code. This pushes the brute-force requirement into a range that is significantly harder to execute in the $\approx 300\text{s}$ window on standard hardware.

### 5. Joiner-side Rate Limits
**Verdict: Insufficient.**
The current plan relies on source-IP limits for unknown `knock_id`s. However, an attacker who has scraped a valid `/clan/knock` request knows the `knock_id`. They can then flood `/clan/knock-deliver` with fake blobs to DoS the joiner or attempt to exhaust resources before the legitimate receiver responds.
*   **Recommendation:** The Joiner should implement an **IP-allowlist for delivery**. Since the Joiner initiated the knock to a specific `receiver_addr`, it should only accept `/clan/knock-deliver` requests originating from that specific IP address.

### 6. Race Conditions
**Verdict: Acceptable.**
The CAS (Compare-And-Swap) on the joiner's `KnockStore` correctly ensures "first-write wins." The fact that a second operator might see a "success" in their TUI while the joiner rejected the second blob is a minor UX inconsistency, not a protocol failure.

### 7. State Persistence / Trust Model
**Verdict: Correct.**
Sharing the `clan_cert_priv_key` means any member can decrypt inbound TLS to the founder's cert. As noted in §10/R1, this matches MINTI's v1 unitary-trust model.

### 8. Replay / Cross-Clan Attacks
**Verdict: Correct.**
The inclusion of `clan_id` in the HKDF info ensures that a payload from Clan A cannot be decrypted by a joiner targeting Clan B, as the keys will differ. The error (GCM tag mismatch) is distinct and detectable.

### 9. TUI Integration
**Verdict: Looks good.**
*   5s polling is acceptable for a 300s TTL.
*   The audit-log idiom (`service.go:121-126`) is correctly identified.

### 10. What the plan got right
*   **HKDF Binding:** The decision to bind `clan_id` and `knock_id` into the info field is excellent and prevents offline precomputation.
*   **GCM as Auth:** Using the GCM tag as the authenticity proof for delivery is a clean, efficient design that avoids unnecessary signature overhead.
*   **Phasing:** The 3-LLM peer review gate before implementation is a disciplined approach to crypto changes.

***

**VERDICT: ship after addressing items 4 (increase SAS entropy) and 5 (add IP-allowlist for delivery)**

---

## Raw (with thinking trace if any)

This review focuses on the M8 "Knock" flow. I have evaluated the protocol against the priority list provided.

### 1. Cryptographic Correctness
**Verdict: Sound.**
*   **HKDF Construction:** The binding of `clan_id`, `knock_id`, and both pubkeys into the `info` field is correct. It prevents precomputation attacks and ensures that a compromise of one session key does not leak others. 
*   **Nonce Safety:** Reusing a deterministic nonce is safe here because the AES key is derived from an ephemeral X25519 exchange. Since $\text{Key} = f(\text{priv}_j, \text{pub}_r)$, and at least one party generates a fresh ephemeral key per knock, the $(\text{key}, \text{nonce})$ pair is unique for every session.
*   **SAS Leakage:** The SAS is derived from 4 bytes of the HKDF output. Since HKDF-SHA256 is a PRF, these bytes are computationally independent of the AES key (the first 32 bytes). No leakage occurs.
*   **GCM AAD:** Using `aad=knock_id` is sufficient to prevent moving a valid ciphertext from one knock session to another. While adding `clan_id` would be "belt-and-suspenders," it is not required for correctness as the key itself is already bound to the `clan_id`.

### 2. Authentication of `/clan/knock-deliver`
**Verdict: Correct.**
The reasoning holds. An attacker who scrapes `/clan/knock` only sees public keys. To forge a valid GCM tag for the delivery blob, they would need the shared secret $S = \text{X25519}(\text{priv}_j, \text{pub}_r)$. They cannot obtain this without either compromising a private key or successfully MITM-ing the initial knock leg. If they MITM'd the knock leg, they would have had to substitute pubkeys, which leads to the SAS mismatch discussed in Item 3.

### 3. Active MITM on `/clan/knock`
**Verdict: Correct (Defense-in-Depth).**
The "TOFU" of the certificate is a weakness, but the SAS provides the necessary security bound. In an active MITM scenario where the attacker proxies and substitutes pubkeys ($\text{Joiner} \leftrightarrow \text{Attacker} \leftrightarrow \text{Receiver}$), the Joiner derives $SAS_A$ and the Receiver derives $SAS_B$. The visual verification over the side channel is the primary authentication mechanism.

### 4. Confirm-code Entropy
**Verdict: Weak / Borderline.**
The plan claims $\approx 500\text{k}$ MITM attempts are needed to land a collision. However, this assumes the attacker is guessing the SAS. In reality, an active MITM attacker who has intercepted the `knock_id` can perform a **targeted pubkey brute-force**.

An attacker can generate millions of ephemeral X25519 keys and compute $\text{HKDF}(\dots)$ until they find a public key that produces the *exact* 6-digit SAS currently displayed on the operator's TUI. On modern hardware, $2^{20}$ HKDF iterations take milliseconds.
*   **Risk:** An attacker could potentially substitute their pubkey for one that results in the same SAS before the operator presses `a`.
*   **Mitigation:** While this is difficult to do "live" without knowing exactly when the operator is looking, 20 bits is too low for a cryptographic guarantee. I recommend increasing this to **30 bits (approx. 1 billion)** or using an 8-digit code. This pushes the brute-force requirement into a range that is significantly harder to execute in the $\approx 300\text{s}$ window on standard hardware.

### 5. Joiner-side Rate Limits
**Verdict: Insufficient.**
The current plan relies on source-IP limits for unknown `knock_id`s. However, an attacker who has scraped a valid `/clan/knock` request knows the `knock_id`. They can then flood `/clan/knock-deliver` with fake blobs to DoS the joiner or attempt to exhaust resources before the legitimate receiver responds.
*   **Recommendation:** The Joiner should implement an **IP-allowlist for delivery**. Since the Joiner initiated the knock to a specific `receiver_addr`, it should only accept `/clan/knock-deliver` requests originating from that specific IP address.

### 6. Race Conditions
**Verdict: Acceptable.**
The CAS (Compare-And-Swap) on the joiner's `KnockStore` correctly ensures "first-write wins." The fact that a second operator might see a "success" in their TUI while the joiner rejected the second blob is a minor UX inconsistency, not a protocol failure.

### 7. State Persistence / Trust Model
**Verdict: Correct.**
Sharing the `clan_cert_priv_key` means any member can decrypt inbound TLS to the founder's cert. As noted in §10/R1, this matches MINTI's v1 unitary-trust model.

### 8. Replay / Cross-Clan Attacks
**Verdict: Correct.**
The inclusion of `clan_id` in the HKDF info ensures that a payload from Clan A cannot be decrypted by a joiner targeting Clan B, as the keys will differ. The error (GCM tag mismatch) is distinct and detectable.

### 9. TUI Integration
**Verdict: Looks good.**
*   5s polling is acceptable for a 300s TTL.
*   The audit-log idiom (`service.go:121-126`) is correctly identified.

### 10. What the plan got right
*   **HKDF Binding:** The decision to bind `clan_id` and `knock_id` into the info field is excellent and prevents offline precomputation.
*   **GCM as Auth:** Using the GCM tag as the authenticity proof for delivery is a clean, efficient design that avoids unnecessary signature overhead.
*   **Phasing:** The 3-LLM peer review gate before implementation is a disciplined approach to crypto changes.

***

**VERDICT: ship after addressing items 4 (increase SAS entropy) and 5 (add IP-allowlist for delivery)**