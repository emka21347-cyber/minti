"""Peer-review the M8 Phase 0 (knock-by-clan-id) protocol spec with local LLMs.

Mirrors m4-phaseE-review.py exactly in shape, narrowed to the crypto- and
distributed-systems-focused review M8 needs. Sends each model:

  - Membership context (§3.1, §3.2, §3.3) so reviewers see what M8 reuses.
  - The NEW §3.4 spec being reviewed (knock flow + crypto + threat model).
  - Existing cland code shapes M8 reuses (invite.go, transport HMAC, etc.).
  - The M8 implementation plan from ~/.claude/plans/we-need-to-plan-async-candle.md.

Per memory/reference_local_llms.md: qwen3.6 gets think:false to avoid the
trace-overflow failure mode observed in earlier rounds.
"""
import json
import os
import re
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"
MODELS = [
    ("qwen3.6:latest",  {"think": False}),
    ("deepseek-r1:32b", {}),
    ("gemma4:31b",      {}),
]

REPO = r"C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
PLAN_PATH = r"C:\Users\aouad\.claude\plans\we-need-to-plan-async-candle.md"
SPEC_PATH = os.path.join(REPO, "docs", "clan-protocol.md")
OUT_DIR = os.path.join(REPO, "scripts", "m4-reviews")
os.makedirs(OUT_DIR, exist_ok=True)


def strip_thinking(s: str) -> str:
    return re.sub(r"<think>.*?</think>\s*", "", s, flags=re.DOTALL)


def read(p: str) -> str:
    with open(p, encoding="utf-8") as f:
        return f.read()


def extract_section(text: str, start_marker: str, end_marker: str) -> str:
    a = text.find(start_marker)
    b = text.find(end_marker, a + 1) if a >= 0 else -1
    if a < 0 or b < 0:
        raise SystemExit(f"could not locate section: start={start_marker!r} end={end_marker!r}")
    return text[a:b]


def build_prompt() -> str:
    spec_full = read(SPEC_PATH)
    # Existing membership context — give reviewers §3.1..§3.3 so they
    # understand what M8 inherits from / how it composes.
    spec_membership = extract_section(spec_full, "## 3. Membership", "### 3.4 Knock flow")
    # The new section being reviewed.
    spec_knock = extract_section(
        spec_full,
        "### 3.4 Knock flow (live in-person / Signal-call onboarding)",
        "### 3.5 Revocation",
    )

    # Extract the M8 plan section from the approved plan file.
    plan_full = read(PLAN_PATH)
    plan_m8 = extract_section(
        plan_full,
        "# M8: Knock-by-Clan-ID Clan-joining flow",
        "**Total: ~2.5 days focused.**",
    )
    # Include the closing line of the plan that the section delimiter cut off.
    plan_m8 += "**Total: ~2.5 days focused.** Commits land as `M8 Phase 0/1/2/3` mirroring M4's phasing. Final commit message starts `M8: knock-by-clan-id Clan-joining flow (Phases 0-3)`.\n"

    code_summary = """Existing cland code shapes the M8 plan builds on (verbatim from the codebase):

cland/internal/membership/invite.go (M8 reuses this exact pattern for KnockStore):
  type InviteStore struct {
    mu     sync.Mutex
    tokens map[string]*InviteToken
  }
  func (s *InviteStore) Put(token *InviteToken) error
  func (s *InviteStore) Redeem(tokenStr string) (*InviteToken, error)  // CAS + delete-under-lock
  func (s *InviteStore) Sweep()                                         // TTL eviction
  TTL bounds: 60s (InviteTTLMin) .. 24h (InviteTTLMax), default 1h.
  In-memory only; lost on daemon restart.

cland/internal/membership/handlers.go registers all membership endpoints:
  srv.Handle("POST /clan/invite", s.handleInvite)        // authenticated (HMAC)
  srv.HandleAnonymous("POST /clan/join", s.handleJoin)   // anonymous (token-based)
  srv.Handle("POST /clan/welcome", s.handleWelcome)      // authenticated (HMAC from derived clan_key)
  srv.Handle("GET /clan/members", s.handleMembers)       // authenticated
  srv.Handle("POST /clan/leave", s.handleLeave)
  srv.Handle("POST /clan/revoke", s.handleRevoke)
M8 adds: knock (anon, joiner→receiver), knock-list/accept/deny (HMAC, operator),
knock-deliver (anon-but-AES-GCM-auth, receiver→joiner).

cland/internal/membership/service.go contains the WelcomeResponse shape M8
seals into the encrypted blob:
  type WelcomeResponse struct {
    ClanID              string         `json:"clan_id"`
    ClanCertPEM         string         `json:"clan_cert_pem"`
    ClanCertPrivKeyB64  string         `json:"clan_cert_priv_key_b64"`
    ClanKeyB64          string         `json:"clan_key_b64"`  // NOT in §3.3's WelcomeResponse — added for knock
    Roster              []RosterMember `json:"roster"`
  }
The §3.3 WelcomeResponse omits ClanKeyB64 because the paste-key joiner already
derived it from the mnemonic. The §3.4 knock joiner has NO clan_key yet, so it
must be in the sealed payload.

cland/internal/transport/server.go: Handle(pattern, h) registers behind HMAC
middleware (auth.go); HandleAnonymous(pattern, h) skips auth. Both go through
the same TLS server with the founder-cert.

cland/internal/transport/client.go: HTTPS client with SPKI pin enforcement
(VerifyPeerCertificate + InsecureSkipVerify=true). Used by the joiner-side to
talk to the receiver. The joiner has NO clan cert pin at knock time and must
TOFU the cert it receives — this is one of the open design points to validate.

cland/internal/crypto/: KeyProvider returns Current()/Grace() HMAC keys; used
for HMAC-stamped requests. NOT used by knock (no shared HMAC key at knock time).

cland/internal/bip39/bip39.go and membership/membership.go:
  DeriveClanKey(seed) = HKDF-SHA256(IKM=seed, salt="minti-clan-key", info="v1") → 32B
M8 uses the same HKDF-SHA256 primitive but with a DIFFERENT salt
("minti-knock-v1") and a CONCATENATED info: clan_id ‖ knock_id ‖ joiner_x25519_pub
‖ receiver_x25519_pub.

cland/internal/peers/peers.go:245-265 (checkRateLimitLocked): sliding-window
rate limiter (10 events / 60 s default), per-origin-keyed. M8 reuses this exact
shape for the knock store's per-joiner_addr + per-clan_id buckets.

go.mod confirms golang.org/x/crypto v0.52.0 is vendored:
  - HKDF (golang.org/x/crypto/hkdf) — already used in membership.go:20
  - curve25519 (golang.org/x/crypto/curve25519) — NEW for M8
  - AES-GCM (crypto/aes + crypto/cipher) — stdlib, already available
No new module deps required.

status/internal/probes/clan/invite.go: M7.6's invite probe (the immediate
predecessor pattern for the new knocks probe). Shells `minti-cland invite
--ttl X --json`, parses InviteResponse, pre-renders the join command for
copy-paste display in the TUI.

status/internal/tui/panels/invite.go: M7.6's invite panel (the immediate
predecessor pattern for the new knocks panel). Render-or-skip on nil/expired;
self-clears via tea.Tick scheduled for time.Until(expires).
"""

    instructions = """You are reviewing the protocol spec + implementation plan for MINTI M8 — a
NEW Clan-joining flow ("knock by clan-id"). MINTI's Clan layer currently has
two joining flows (§3.2 invite-token, §3.3 BIP39 paste-key), both of which
require the joiner to receive out-of-band auth material BEFORE joining. M8
adds a third path where the joiner only needs the Clan ID + a reachable LAN
address; the existing-Clan operator visually verifies a 6-digit SAS code with
the joiner over a side channel (phone, in-person, Signal) before accepting.

The crypto core: ephemeral X25519 ECDH between joiner and receiver, with the
shared secret fed through HKDF-SHA256 binding BOTH pubkeys + clan_id +
knock_id into the info. The output is split into (32-byte AES-256 key,
4 bytes of SAS material, 12-byte AES-GCM nonce). Receiver seals a
WelcomeResponse with AES-256-GCM keyed by the HKDF output and aad=knock_id;
joiner opens with the same key/nonce/aad. The GCM tag is the receiver-
authenticity proof.

Your job is to find concrete problems, not to be polite. Be willing to say
"looks good" rather than padding criticism. Be willing to disagree with the
plan author. Cite spec sections (e.g. "§3.4 confirm-code semantics") or
plan sections (e.g. "Rate limits") by name so the author can find them.

Look for, in this priority order:

1. **Cryptographic correctness.** Is the HKDF construction sound (salt
   namespacing, info binding, output split)? Is reusing a deterministic
   nonce safe given that each (joiner_pub, receiver_pub) pair derives a
   distinct key? Does the SAS derivation leak information about the key?
   Is the GCM aad=knock_id binding sufficient, or should the aad also
   include clan_id / pubkeys?

2. **Authentication of /clan/knock-deliver.** This is the load-bearing
   weakness. The joiner exposes an anonymous endpoint waiting for a sealed
   blob. The blob's GCM tag is the only auth. Can an attacker who scraped
   /clan/knock off the wire forge a tag? They'd need the X25519 ECDH
   shared secret — which they don't have unless they MITM'd the original
   /clan/knock leg (in which case the SAS verification step would catch
   them). Is this reasoning correct, or is there a hole?

3. **Active MITM on /clan/knock.** TLS-with-SPKI-pin protects §3.2/§3.3
   because the joiner is given the pin out of band. The §3.4 joiner has
   no pin. The plan says the joiner TOFUs the cert. Is the SAS sufficient
   defence-in-depth? Could an attacker proxy the entire knock leg,
   substitute pubkeys, and learn the AES key just by participating?

4. **Confirm-code entropy.** 20 bits ≈ 1M space. With 5-min TTL + human-
   eyeball verification, an attacker needs ≈500k MITM attempts per knock
   window to land a collision. Is 20 bits sufficient for v1's trusted-LAN
   threat model, or should it be 24/30 bits like the original plan-agent
   review hinted toward?

5. **Joiner-side rate limits.** The /clan/knock-deliver endpoint accepts
   any (knock_id, encrypted_blob) — anyone can spam it. Per-knock_id CAS
   limits accepts to 1. Per-source-IP limit is 5/60s for unknown
   knock_ids. Sufficient against a determined flooder, or do we need an
   IP-allowlist (only accept knock-deliver from addresses the joiner
   knock-d?

6. **Race conditions.** Two operators on two different Clan members both
   press `a` on the same knock — both POST /clan/knock-deliver to the
   joiner. The joiner's CAS makes the first-write win; the second gets
   409. But the second operator's receiver still recorded the knock as
   "accepted" — is there a way to leave the receiver-side state
   inconsistent (e.g., one receiver thinks it succeeded; the joiner only
   has the other's payload)?

7. **State persistence after accept.** The joiner persists clan_key +
   cert_pem + cert_priv_key + roster from the sealed blob. The cert_priv
   shared via §3.3 / §3.4 means *every* member of the Clan can decrypt
   inbound TLS to the founder's cert. Is this acceptable for v1's
   unitary-trust model? (Per spec §10 / R1, yes — but flag if the M8
   plan implies tighter semantics than it delivers.)

8. **Replay / cross-Clan attacks.** A captured /clan/knock-deliver
   payload from Clan A — can it be replayed against Clan B's joiner
   that happens to pick the same knock_id? clan_id is in HKDF info →
   different key → GCM tag fails. But the joiner sees a 409-or-tag-
   mismatch error; is that visible enough to detect a cross-Clan attack
   vs. an honest "wrong knock_id" failure?

9. **TUI integration concerns.** The accept/deny operations happen via
   minti-status pressing `a`/`d` on highlighted knocks. The plan says
   the TUI polls /clan/knock-list every 5s. Is 5s an acceptable
   detection latency for a knock — or should it be the fast 2s tick?
   What about audit-log writes — does the plan match the existing
   service.go:121-126 idiom?

10. **What the plan got right and shouldn't change.** Be willing to
    say so. Padding critique with non-issues wastes the author's time.

Be specific. Quote the spec/plan paragraph you're objecting to. If a
finding is out-of-scope for M8 v1 (e.g. mDNS auto-discovery), say so
and move on.

End your review with one short verdict line:
  VERDICT: ship Phase 0 as-is | ship after addressing items N..M | block on item K

Below are FOUR documents.
"""

    parts = [
        instructions,
        "==================================================================\n"
        "DOCUMENT 1: Existing membership context — spec §3.1, §3.2, §3.3\n"
        "==================================================================\n\n",
        spec_membership,
        "\n\n==================================================================\n"
        "DOCUMENT 2: NEW spec §3.4 (Knock flow) — the section being reviewed\n"
        "==================================================================\n\n",
        spec_knock,
        "\n\n==================================================================\n"
        "DOCUMENT 3: Existing cland code shapes the M8 plan reuses\n"
        "==================================================================\n\n",
        code_summary,
        "\n\n==================================================================\n"
        "DOCUMENT 4: M8 implementation plan (we-need-to-plan-async-candle.md)\n"
        "==================================================================\n\n",
        plan_m8,
    ]
    return "".join(parts)


def chat(model: str, prompt: str, extra_body: dict) -> dict:
    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "options": {
            "temperature": 0.3,
            "num_ctx": 24576,
            "num_predict": 16000,
        },
    }
    body.update(extra_body)
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        OLLAMA, data=data, headers={"Content-Type": "application/json"}, method="POST"
    )
    with urllib.request.urlopen(req, timeout=2400) as r:
        return json.loads(r.read().decode("utf-8"))


def main() -> int:
    prompt = build_prompt()
    print(f"input chars={len(prompt)}, approx tokens={len(prompt)//4}", flush=True)

    with open(os.path.join(OUT_DIR, "m8-phase0-input.txt"), "w", encoding="utf-8") as f:
        f.write(prompt)

    summary = []
    for model, extra in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"m8-phase0-{safe}.md")
        print(f"\n--- {model} ---", flush=True)
        t0 = time.time()
        try:
            resp = chat(model, prompt, extra)
        except Exception as exc:
            print(f"  ERROR: {exc}", flush=True)
            summary.append((model, "ERROR", 0, str(exc)))
            continue
        dur = time.time() - t0
        raw = resp.get("message", {}).get("content", "")
        clean = strip_thinking(raw).strip()
        eval_count = resp.get("eval_count", 0)
        prompt_eval = resp.get("prompt_eval_count", 0)

        with open(out_path, "w", encoding="utf-8") as o:
            o.write(f"# {model} - M8 Phase 0 protocol review\n\n")
            o.write(f"- wall_time_s: {dur:.1f}\n- prompt_tokens: {prompt_eval}\n")
            o.write(f"- eval_tokens: {eval_count}\n- raw_chars: {len(raw)}\n")
            o.write(f"- clean_chars: {len(clean)}\n- extra_body: {extra}\n\n---\n\n")
            o.write(clean)
            o.write("\n\n---\n\n## Raw (with thinking trace if any)\n\n")
            o.write(raw)

        print(
            f"  done in {dur:.1f}s; eval_tokens={eval_count}; "
            f"clean_chars={len(clean)}; saved {out_path}",
            flush=True,
        )
        summary.append((model, "OK", dur, out_path))

    print("\n--- summary ---", flush=True)
    for m, status, dur, info in summary:
        print(f"  {m:<25} {status:<6} {dur:>5.0f}s  {info}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
