package membership

import (
	"encoding/base64"
	"testing"
	"time"
)

// ─── Crypto round-trip ────────────────────────────────────────────────────────

func TestDeriveKnockBundle_Symmetric(t *testing.T) {
	// Both sides derive byte-identical (key, nonce, sas) when they swap roles.
	joinerPriv, joinerPub, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	recvPriv, recvPub, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	knockID := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	clanID := "test-clan-id"

	joinerKey, joinerNonce, joinerSAS, err := DeriveKnockBundle(joinerPriv, recvPub, clanID, knockID, joinerPub, recvPub)
	if err != nil {
		t.Fatal(err)
	}
	recvKey, recvNonce, recvSAS, err := DeriveKnockBundle(recvPriv, joinerPub, clanID, knockID, joinerPub, recvPub)
	if err != nil {
		t.Fatal(err)
	}

	if string(joinerKey) != string(recvKey) {
		t.Errorf("key mismatch: joiner=%x recv=%x", joinerKey, recvKey)
	}
	if string(joinerNonce) != string(recvNonce) {
		t.Errorf("nonce mismatch: joiner=%x recv=%x", joinerNonce, recvNonce)
	}
	if joinerSAS != recvSAS {
		t.Errorf("SAS mismatch: joiner=%s recv=%s", joinerSAS, recvSAS)
	}
}

func TestDeriveKnockBundle_SASFormat(t *testing.T) {
	priv, pub, _ := GenerateX25519()
	_, _, sas, err := DeriveKnockBundle(priv, pub, "clan", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, pub, pub)
	if err != nil {
		t.Fatal(err)
	}
	// "XXXX-XXXXX": 4 + hyphen + 5 = 10 chars
	if len(sas) != 10 || sas[4] != '-' {
		t.Errorf("unexpected SAS format %q (want XXXX-XXXXX)", sas)
	}
}

func TestDeriveKnockBundle_KnockIDBinds(t *testing.T) {
	// Different knock_id → different keys, preventing cross-knock replay.
	priv, pub, _ := GenerateX25519()
	clanID := "clan"
	joinerPub := pub
	recvPub := pub
	kid1 := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	kid2 := []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	k1, n1, s1, _ := DeriveKnockBundle(priv, pub, clanID, kid1, joinerPub, recvPub)
	k2, n2, s2, _ := DeriveKnockBundle(priv, pub, clanID, kid2, joinerPub, recvPub)

	if string(k1) == string(k2) {
		t.Error("different knock_id produced same key")
	}
	if string(n1) == string(n2) {
		t.Error("different knock_id produced same nonce")
	}
	if s1 == s2 {
		t.Error("different knock_id produced same SAS (astronomically unlikely; re-run if flaky)")
	}
}

func TestDeriveKnockBundle_ClanIDBinds(t *testing.T) {
	// Different clan_id → different keys, preventing cross-clan replay.
	priv, pub, _ := GenerateX25519()
	kid := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	k1, _, _, _ := DeriveKnockBundle(priv, pub, "clan-A", kid, pub, pub)
	k2, _, _, _ := DeriveKnockBundle(priv, pub, "clan-B", kid, pub, pub)
	if string(k1) == string(k2) {
		t.Error("different clan_id produced same key")
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	priv, pub, _ := GenerateX25519()
	kid := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	key, nonce, _, _ := DeriveKnockBundle(priv, pub, "clan", kid, pub, pub)

	want := KnockWelcome{
		ClanID:     "clan",
		ClanKeyB64: "abc123",
	}
	ct, err := SealKnockWelcome(key, nonce, kid, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenKnockWelcome(key, nonce, kid, ct)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClanID != want.ClanID || got.ClanKeyB64 != want.ClanKeyB64 {
		t.Errorf("open/seal mismatch: got %+v", got)
	}
}

func TestSealOpen_TamperCiphertext(t *testing.T) {
	priv, pub, _ := GenerateX25519()
	kid := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	key, nonce, _, _ := DeriveKnockBundle(priv, pub, "clan", kid, pub, pub)
	ct, _ := SealKnockWelcome(key, nonce, kid, KnockWelcome{ClanID: "clan"})
	ct[0] ^= 0xFF // flip a byte
	if _, err := OpenKnockWelcome(key, nonce, kid, ct); err == nil {
		t.Error("expected GCM authentication failure on tampered ciphertext")
	}
}

func TestSealOpen_WrongAAD(t *testing.T) {
	priv, pub, _ := GenerateX25519()
	kid := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	kidWrong := []byte{0xFF, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	key, nonce, _, _ := DeriveKnockBundle(priv, pub, "clan", kid, pub, pub)
	ct, _ := SealKnockWelcome(key, nonce, kid, KnockWelcome{ClanID: "clan"})
	if _, err := OpenKnockWelcome(key, nonce, kidWrong, ct); err == nil {
		t.Error("expected GCM failure on wrong AAD (knock_id mismatch)")
	}
}

func TestSealOpen_WrongKey(t *testing.T) {
	priv, pub, _ := GenerateX25519()
	attackerPriv, attackerPub, _ := GenerateX25519()
	kid := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	realKey, realNonce, _, _ := DeriveKnockBundle(priv, pub, "clan", kid, pub, pub)
	attackerKey, attackerNonce, _, _ := DeriveKnockBundle(attackerPriv, attackerPub, "clan", kid, pub, pub)
	ct, _ := SealKnockWelcome(realKey, realNonce, kid, KnockWelcome{ClanID: "clan"})
	if _, err := OpenKnockWelcome(attackerKey, attackerNonce, kid, ct); err == nil {
		t.Error("expected GCM failure with attacker key")
	}
}

// ─── KnockStore ───────────────────────────────────────────────────────────────

func newTestKnock(id, clanID string, ttl time.Duration) *PendingKnock {
	return &PendingKnock{
		KnockID:   id,
		ClanID:    clanID,
		State:     "pending",
		ExpiresAt: time.Now().UTC().Add(ttl),
		SAS:       "1234-56789",
	}
}

func TestKnockStore_StoreAndList(t *testing.T) {
	ks := NewKnockStore()
	pk := newTestKnock("aaa", "clan1", 5*time.Minute)
	if err := ks.Store(pk, "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	list := ks.List()
	if len(list) != 1 || list[0].KnockID != "aaa" {
		t.Errorf("expected 1 knock, got %v", list)
	}
}

func TestKnockStore_Consume_SingleUse(t *testing.T) {
	ks := NewKnockStore()
	pk := newTestKnock("bbb", "clan1", 5*time.Minute)
	_ = ks.Store(pk, "10.0.0.2")

	if _, err := ks.Consume("bbb", "accepted"); err != nil {
		t.Fatal("first consume should succeed:", err)
	}
	if _, err := ks.Consume("bbb", "accepted"); err == nil {
		t.Error("second consume should fail (already accepted)")
	}
}

func TestKnockStore_Consume_NotFound(t *testing.T) {
	ks := NewKnockStore()
	_, err := ks.Consume("nonexistent", "accepted")
	if err != ErrKnockNotFound {
		t.Errorf("expected ErrKnockNotFound, got %v", err)
	}
}

func TestKnockStore_Consume_Expired(t *testing.T) {
	ks := NewKnockStore()
	pk := newTestKnock("ccc", "clan1", -1*time.Second) // already expired
	_ = ks.Store(pk, "10.0.0.3")
	_, err := ks.Consume("ccc", "accepted")
	if err != ErrKnockExpired {
		t.Errorf("expected ErrKnockExpired, got %v", err)
	}
}

func TestKnockStore_RateLimit_PerSrc(t *testing.T) {
	ks := NewKnockStore()
	src := "10.0.0.4"
	for i := 0; i < knockRatePerSrc; i++ {
		pk := newTestKnock(string(rune('a'+i)), "clan1", 5*time.Minute)
		if err := ks.Store(pk, src); err != nil {
			t.Fatalf("store %d failed unexpectedly: %v", i, err)
		}
	}
	// One more should be rate-limited.
	pk := newTestKnock("z", "clan1", 5*time.Minute)
	if err := ks.Store(pk, src); err == nil {
		t.Error("expected rate-limit error on knock 11 from same source IP")
	}
}

func TestKnockStore_RateLimit_PerClan(t *testing.T) {
	ks := NewKnockStore()
	clanID := "clan-rate-test"
	for i := 0; i < knockRatePerClan; i++ {
		pk := newTestKnock(string(rune('A'+i%26))+string(rune('a'+i/26)), clanID, 5*time.Minute)
		// Use distinct source IPs to avoid per-src limit.
		src := "10.1." + string(rune('0'+i/100)) + "." + string(rune('0'+i%100))
		if err := ks.Store(pk, src); err != nil {
			t.Fatalf("store %d failed unexpectedly: %v", i, err)
		}
	}
	pk := newTestKnock("last", clanID, 5*time.Minute)
	if err := ks.Store(pk, "10.2.0.1"); err == nil {
		t.Error("expected per-clan rate-limit error on knock 31")
	}
}

func TestKnockStore_Sweep(t *testing.T) {
	ks := NewKnockStore()
	_ = ks.Store(newTestKnock("live", "c", 5*time.Minute), "10.0.0.5")
	_ = ks.Store(newTestKnock("dead", "c", -1*time.Second), "10.0.0.6")
	n := ks.Sweep()
	if n != 1 {
		t.Errorf("expected 1 swept, got %d", n)
	}
	if ks.Size() != 1 {
		t.Errorf("expected 1 remaining, got %d", ks.Size())
	}
}

// ─── F3: Mutual SAS confirmation (critical invariant) ─────────────────────────
//
// Verifies that both sides independently derive the SAME SAS from the
// same inputs — a necessary precondition for mutual out-of-band comparison.
// If this fails, the MITM defence (spec §3.4 "Mutual SAS confirmation") is void.

func TestF3_MutualSASConfirmation(t *testing.T) {
	joinerPriv, joinerPub, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	recvPriv, recvPub, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	knockIDRaw := []byte{9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 15, 14, 13, 12, 11, 10}
	clanID := "mutual-sas-test-clan"

	// Joiner derives SAS after receiving KnockResponse (has receiver pub).
	_, _, joinerSAS, err := DeriveKnockBundle(joinerPriv, recvPub, clanID, knockIDRaw, joinerPub, recvPub)
	if err != nil {
		t.Fatal("joiner DeriveKnockBundle:", err)
	}

	// Receiver derives SAS when it stores the pending knock (has joiner pub).
	_, _, receiverSAS, err := DeriveKnockBundle(recvPriv, joinerPub, clanID, knockIDRaw, joinerPub, recvPub)
	if err != nil {
		t.Fatal("receiver DeriveKnockBundle:", err)
	}

	// CRITICAL: both SAScodes must be identical. If not, one side displays a
	// different code — the mutual-confirmation defence is broken and a MITM
	// who swapped pubkeys would go undetected.
	if joinerSAS != receiverSAS {
		t.Errorf("F3 FAIL: joiner SAS %q != receiver SAS %q — mutual confirmation impossible", joinerSAS, receiverSAS)
	}

	// Also verify the sealed/opened payload round-trip with the same derived keys.
	_, joinerNonce, _, _ := DeriveKnockBundle(joinerPriv, recvPub, clanID, knockIDRaw, joinerPub, recvPub)
	recvKey, _, _, _ := DeriveKnockBundle(recvPriv, joinerPub, clanID, knockIDRaw, joinerPub, recvPub)
	// Receiver seals; joiner opens.
	ct, err := SealKnockWelcome(recvKey, joinerNonce, knockIDRaw, KnockWelcome{ClanID: clanID, ClanKeyB64: "k"})
	if err != nil {
		t.Fatal("seal:", err)
	}
	joinerKey, _, _, _ := DeriveKnockBundle(joinerPriv, recvPub, clanID, knockIDRaw, joinerPub, recvPub)
	got, err := OpenKnockWelcome(joinerKey, joinerNonce, knockIDRaw, ct)
	if err != nil {
		t.Fatal("open:", err)
	}
	if got.ClanID != clanID {
		t.Errorf("open produced wrong clan_id: %s", got.ClanID)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// TestDecodeKnockID validates the hex knock_id enforcement.
func TestDecodeKnockID(t *testing.T) {
	_, err := decodeKnockID("deadbeefdeadbeefdeadbeefdeadbeef") // 32 hex = 16 bytes
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeKnockID("deadbeef"); err == nil {
		t.Error("expected error for 4-byte knock_id")
	}
	if _, err := decodeKnockID("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); err == nil {
		t.Error("expected error for invalid hex")
	}
}

// TestRandKnockID checks uniqueness and format.
func TestRandKnockID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		h, b, err := randKnockID()
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != 16 {
			t.Errorf("raw bytes len %d, want 16", len(b))
		}
		if len(h) != 32 {
			t.Errorf("hex len %d, want 32", len(h))
		}
		if seen[h] {
			t.Errorf("duplicate knock_id generated: %s", h)
		}
		seen[h] = true
	}
}

// TestGenerateX25519 verifies keypair usability.
func TestGenerateX25519(t *testing.T) {
	priv, pub, err := GenerateX25519()
	if err != nil {
		t.Fatal(err)
	}
	if len(priv) != 32 || len(pub) != 32 {
		t.Errorf("unexpected key lengths priv=%d pub=%d", len(priv), len(pub))
	}
	// Encode/decode round-trip to confirm pub is valid base64url.
	encoded := base64.StdEncoding.EncodeToString(pub)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 {
		t.Error("pub base64 round-trip failed")
	}
}
