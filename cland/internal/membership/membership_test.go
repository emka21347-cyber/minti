package membership

import (
	"bytes"
	"strings"
	"testing"

	"github.com/minti/cland/internal/bip39"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/identity"
	"github.com/minti/cland/internal/state"
)

func newStore(t *testing.T) *state.Store {
	t.Helper()
	s, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeriveClanKey_DeterministicAndCorrectLength(t *testing.T) {
	seed := []byte("1234567890ABCDEF") // 16 bytes
	k1, err := DeriveClanKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Errorf("clan_key length = %d, want 32", len(k1))
	}
	k2, _ := DeriveClanKey(seed)
	if !bytes.Equal(k1, k2) {
		t.Errorf("DeriveClanKey is non-deterministic")
	}
}

func TestDeriveClanKey_DifferentSeedsDifferentKeys(t *testing.T) {
	k1, _ := DeriveClanKey([]byte("AAAAAAAAAAAAAAAA"))
	k2, _ := DeriveClanKey([]byte("BBBBBBBBBBBBBBBB"))
	if bytes.Equal(k1, k2) {
		t.Errorf("distinct seeds produced identical clan_keys")
	}
}

func TestDeriveClanKey_BadSeedLength(t *testing.T) {
	if _, err := DeriveClanKey([]byte("short")); err == nil {
		t.Errorf("short seed should error")
	}
}

func TestCreate_PopulatesClanState(t *testing.T) {
	store := newStore(t)
	id := newIdentity(t)

	pk, err := Create(store, id, "192.168.1.10:7777")
	if err != nil {
		t.Fatal(err)
	}
	if pk.ClanID == "" {
		t.Errorf("PasteKey.ClanID empty")
	}
	if got := len(strings.Fields(pk.Mnemonic)); got != bip39.MnemonicWords {
		t.Errorf("mnemonic should be %d words, got %d", bip39.MnemonicWords, got)
	}
	if pk.Address != "192.168.1.10:7777" {
		t.Errorf("address echoed wrong: %q", pk.Address)
	}
	if !strings.HasPrefix(pk.Pin, "sha256:") {
		t.Errorf("pin format wrong: %q", pk.Pin)
	}

	// Persisted state matches.
	clan, err := store.LoadClan()
	if err != nil {
		t.Fatal(err)
	}
	if clan == nil || clan.ClanID != pk.ClanID {
		t.Fatalf("clan not persisted or clan_id mismatch")
	}
	if clan.Role != "founder" {
		t.Errorf("Role = %q, want founder", clan.Role)
	}
	if clan.ClanCertPin != pk.Pin {
		t.Errorf("pin not persisted")
	}
	if len(clan.ClanKey()) != 32 {
		t.Errorf("clan_key length wrong: %d", len(clan.ClanKey()))
	}
	if len(clan.Roster) != 1 || clan.Roster[0].MemberID != id.MemberID {
		t.Errorf("roster missing founder: %+v", clan.Roster)
	}
	if clan.Roster[0].State != "active" {
		t.Errorf("founder should be active, got %q", clan.Roster[0].State)
	}
}

func TestCreate_CertParseable(t *testing.T) {
	store := newStore(t)
	id := newIdentity(t)
	pk, err := Create(store, id, "127.0.0.1:7777")
	if err != nil {
		t.Fatal(err)
	}
	clan, _ := store.LoadClan()
	parsed, err := crypto.ParseClanCertPEM([]byte(clan.ClanCertPEM))
	if err != nil {
		t.Fatalf("persisted cert PEM did not round-trip: %v", err)
	}
	if parsed.Pin != pk.Pin {
		t.Errorf("recomputed pin mismatch: cert=%s pastekey=%s", parsed.Pin, pk.Pin)
	}
}

func TestCreate_ValidatesArgs(t *testing.T) {
	store := newStore(t)
	id := newIdentity(t)
	if _, err := Create(nil, id, "x"); err == nil {
		t.Errorf("nil store should error")
	}
	if _, err := Create(store, nil, "x"); err == nil {
		t.Errorf("nil identity should error")
	}
	if _, err := Create(store, id, ""); err == nil {
		t.Errorf("empty listen addr should error")
	}
}

// TestFounderAndJoiner_DeriveSameClanKey is the central paste-key test:
// the founder generates a mnemonic; the joiner enters the SAME mnemonic on
// a different machine; both end up with byte-identical clan_keys.
func TestFounderAndJoiner_DeriveSameClanKey(t *testing.T) {
	founderStore := newStore(t)
	founderID := newIdentity(t)
	pk, err := Create(founderStore, founderID, "127.0.0.1:7777")
	if err != nil {
		t.Fatal(err)
	}
	founderClan, _ := founderStore.LoadClan()
	founderKey := founderClan.ClanKey()

	// Joiner side — fresh machine, only has the mnemonic.
	joinerKey, _, err := PreJoinViaMnemonic(pk.Mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(founderKey, joinerKey) {
		t.Errorf("paste-key convergence broken:\n  founder: %x\n  joiner:  %x", founderKey, joinerKey)
	}
}

func TestPreJoinViaMnemonic_NormalisesUserInput(t *testing.T) {
	pk, err := Create(newStore(t), newIdentity(t), "127.0.0.1:7777")
	if err != nil {
		t.Fatal(err)
	}
	// User types in mixed case and extra whitespace.
	dirty := "  " + strings.ToUpper(pk.Mnemonic) + "  "
	k1, _, err := PreJoinViaMnemonic(pk.Mnemonic)
	if err != nil {
		t.Fatal(err)
	}
	k2, _, err := PreJoinViaMnemonic(dirty)
	if err != nil {
		t.Fatalf("mixed-case + whitespace should still decode: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Errorf("normalisation broken")
	}
}

func TestPreJoinViaMnemonic_BadMnemonicErrors(t *testing.T) {
	if _, _, err := PreJoinViaMnemonic("not a real mnemonic at all just words"); err == nil {
		t.Errorf("garbage mnemonic should error")
	}
}
