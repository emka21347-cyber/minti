// Package membership implements MINTI's Clan membership flows: founding a
// new Clan (`create`), joining an existing one via the 12-word paste-key
// mnemonic (the §3.3 fallback flow), invite-token redemption (§3.2), and
// the §3.4 revocation path.
//
// Phase C (this iteration): pure crypto + state persistence for the founder
// + the joiner's mnemonic-to-clan_key derivation. HTTP endpoints
// (/clan/invite, /clan/join, /clan/welcome, /clan/members, /clan/leave,
// /clan/revoke) land in Phase C continuation alongside the zombie sweep
// and invite-token storage.
package membership

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/hkdf"

	"github.com/minti/cland/internal/bip39"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/state"
)

// HKDF parameters per docs/clan-protocol.md §3.3 (v0.2).
const (
	hkdfSalt     = "minti-clan-key"
	hkdfInfo     = "v1"
	certValidity = 365 * 24 * time.Hour
)

// PasteKey is the shareable bundle a founder hands a joiner. All three
// fields are needed: mnemonic encodes the clan_key seed, address tells the
// joiner where to connect, pin tells them which cert to trust on first
// connect (TOFU-style but against a user-supplied pin, not blind).
type PasteKey struct {
	ClanID   string `json:"clan_id"`
	Mnemonic string `json:"mnemonic"` // 12 BIP39 words
	Address  string `json:"address"`  // ip:port of the founder's cland
	Pin      string `json:"pin"`      // sha256:<hex>
}

// Create founds a new Clan on this member. Steps:
//  1. Generate 16-byte random seed.
//  2. Encode it as a 12-word BIP39 mnemonic.
//  3. HKDF-expand seed → 32-byte clan_key.
//  4. Generate clan_id (UUIDv4).
//  5. Generate self-signed clan_cert using this member's Ed25519 key.
//  6. Persist the lot to the state store, with this member marked as the
//     founder and added to the roster as active.
//
// Returns the PasteKey for the founder to share with joiners.
func Create(store *state.Store, id *identity.Identity, listenAddr string) (*PasteKey, error) {
	if store == nil || id == nil {
		return nil, fmt.Errorf("membership.Create: store and identity required")
	}
	if listenAddr == "" {
		return nil, fmt.Errorf("membership.Create: listenAddr required")
	}

	seed := make([]byte, bip39.SeedBytes)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("membership: seed: %w", err)
	}
	mnemonic, err := bip39.MnemonicFromSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("membership: mnemonic: %w", err)
	}
	clanKey, err := DeriveClanKey(seed)
	if err != nil {
		return nil, err
	}
	clanID, err := newUUIDv4()
	if err != nil {
		return nil, fmt.Errorf("membership: clan_id: %w", err)
	}
	cc, err := crypto.GenerateClanCert(id.PrivateKey(), certValidity, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("membership: cert: %w", err)
	}

	now := time.Now().UTC()
	clan := &state.Clan{
		ClanID:      clanID,
		ClanCertPEM: string(cc.CertPEM),
		ClanCertPin: cc.Pin,
		Role:        "founder",
		JoinedAt:    now,
		Roster: []state.RosterMember{{
			MemberID:   id.MemberID,
			PubKeyB64:  id.PubKey,
			State:      "active",
			AdmittedAt: now,
			LastSeenAt: now,
		}},
	}
	clan.SetClanKey(clanKey)
	// Persist the cert's matching Ed25519 priv so subsequent joiners can
	// also serve TLS with this same cert. v1 unitary-trust model per
	// spec §10a residual R1 — every Clan member who has clan_key already
	// has the priv key's effective trust level.
	clan.SetClanCertPrivKey(id.PrivateKey())
	if err := store.SaveClan(clan); err != nil {
		return nil, err
	}
	return &PasteKey{
		ClanID:   clanID,
		Mnemonic: mnemonic,
		Address:  listenAddr,
		Pin:      cc.Pin,
	}, nil
}

// DeriveClanKey expands a 16-byte BIP39 seed to the 32-byte clan_key used
// for HMAC. HKDF-SHA256(seed, salt="minti-clan-key", info="v1") per
// docs/clan-protocol.md §3.3 (v0.2).
//
// This is the entire bridge between the 128-bit human-paste mnemonic and
// the 256-bit clan_key the protocol's HMAC layer consumes. Both founder
// and joiner must derive the same key from the same seed.
func DeriveClanKey(seed []byte) ([]byte, error) {
	if len(seed) != bip39.SeedBytes {
		return nil, fmt.Errorf("membership.DeriveClanKey: seed must be %d bytes, got %d", bip39.SeedBytes, len(seed))
	}
	r := hkdf.New(sha256.New, seed, []byte(hkdfSalt), []byte(hkdfInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("membership: hkdf: %w", err)
	}
	return key, nil
}

// PreJoinViaMnemonic does the joiner's local crypto work: decode the
// founder-supplied mnemonic, derive the matching clan_key. The returned
// key + seed feed the HTTPS handshake in Phase C-continuation
// (POST /clan/welcome) that fetches the clan_cert_pem and persists state.
//
// On its own this function does NOT persist anything — it just proves the
// joiner has the right mnemonic. Tests assert both sides converge on the
// same clan_key.
func PreJoinViaMnemonic(mnemonic string) (clanKey []byte, seed []byte, err error) {
	seed, err = bip39.SeedFromMnemonic(mnemonic)
	if err != nil {
		return nil, nil, fmt.Errorf("membership.PreJoinViaMnemonic: %w", err)
	}
	clanKey, err = DeriveClanKey(seed)
	if err != nil {
		return nil, nil, err
	}
	return clanKey, seed, nil
}

// newUUIDv4 — see identity.newUUIDv4. Duplicated here to avoid a circular
// import; consolidate into a shared util in a follow-up phase.
func newUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	), nil
}
