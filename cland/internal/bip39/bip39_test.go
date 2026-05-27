package bip39

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// Canonical sha256 of the BIP39 English wordlist (bitcoin/bips
// bip-0039/english.txt). If you replace wordlist.txt with anything else,
// this test forces you to acknowledge it.
const canonicalSHA256 = "2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda"

func TestWordlistIntegrity(t *testing.T) {
	if got := len(Wordlist()); got != 2048 {
		t.Fatalf("wordlist length = %d, want 2048", got)
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(rawWordlist) + "\n"))
	if hex.EncodeToString(h[:]) != canonicalSHA256 {
		t.Errorf("wordlist sha256 differs from canonical bip-0039 english.txt:\n  got:  %x\n  want: %s",
			h[:], canonicalSHA256)
	}
}

// Standard BIP39 12-word test vectors from
// https://github.com/trezor/python-mnemonic/blob/master/vectors.json
// (English, 128-bit entropy). entropy hex → mnemonic.
var standardVectors = []struct {
	hex      string
	mnemonic string
}{
	{
		"00000000000000000000000000000000",
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
	},
	{
		"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f",
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
	},
	{
		"80808080808080808080808080808080",
		"letter advice cage absurd amount doctor acoustic avoid letter advice cage above",
	},
	{
		"ffffffffffffffffffffffffffffffff",
		"zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong",
	},
	{
		"0c1e24e5917779d297e14d45f14e1a1a",
		"army van defense carry jealous true garbage claim echo media make crunch",
	},
}

func TestStandardVectors_EncodeDecode(t *testing.T) {
	for i, v := range standardVectors {
		seed, _ := hex.DecodeString(v.hex)
		got, err := MnemonicFromSeed(seed)
		if err != nil {
			t.Fatalf("vector %d encode: %v", i, err)
		}
		if got != v.mnemonic {
			t.Errorf("vector %d encode:\n  got:  %s\n  want: %s", i, got, v.mnemonic)
		}
		back, err := SeedFromMnemonic(v.mnemonic)
		if err != nil {
			t.Fatalf("vector %d decode: %v", i, err)
		}
		if !bytes.Equal(back, seed) {
			t.Errorf("vector %d decode: got %x, want %x", i, back, seed)
		}
	}
}

func TestRandomRoundTrip(t *testing.T) {
	for i := 0; i < 32; i++ {
		seed := make([]byte, SeedBytes)
		_, _ = rand.Read(seed)
		m, err := MnemonicFromSeed(seed)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(strings.Fields(m)); got != MnemonicWords {
			t.Errorf("mnemonic should be %d words, got %d", MnemonicWords, got)
		}
		back, err := SeedFromMnemonic(m)
		if err != nil {
			t.Fatalf("decode: %v\nseed=%x mnemonic=%s", err, seed, m)
		}
		if !bytes.Equal(back, seed) {
			t.Errorf("round-trip mismatch:\n  in:  %x\n  out: %x", seed, back)
		}
	}
}

func TestMnemonicFromSeed_WrongLengthErrors(t *testing.T) {
	for _, badLen := range []int{0, 8, 15, 17, 32} {
		if _, err := MnemonicFromSeed(make([]byte, badLen)); err == nil {
			t.Errorf("seed of length %d should error", badLen)
		}
	}
}

func TestSeedFromMnemonic_BadInputs(t *testing.T) {
	cases := []struct {
		name, mnemonic string
	}{
		{"too few words", "abandon abandon abandon"},
		{"too many words", strings.Repeat("abandon ", 24) + "about"},
		{"unknown word", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon foobar"},
		{"empty string", ""},
		{"bad checksum (good words, last word swapped)",
			"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := SeedFromMnemonic(c.mnemonic); err == nil {
				t.Errorf("expected error for %q", c.name)
			}
		})
	}
}

func TestSeedFromMnemonic_NormalisesCaseAndWhitespace(t *testing.T) {
	m := "  Army Van Defense  carry jealous true garbage claim echo media make crunch  "
	want, _ := hex.DecodeString("0c1e24e5917779d297e14d45f14e1a1a")
	got, err := SeedFromMnemonic(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}
