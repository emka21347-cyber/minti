// Package bip39 implements the BIP39 mnemonic phrase encoding/decoding for
// 12-word (128-bit entropy) seeds used by MINTI's paste-key Clan join flow.
//
// The English wordlist is vendored at wordlist.txt and loaded via go:embed
// — sha256 should equal 2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda
// matching the canonical bitcoin/bips bip-0039/english.txt.
//
// Spec: https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki
package bip39

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"strings"
)

//go:embed wordlist.txt
var rawWordlist string

// SeedBits is the only entropy size we support — 128 bits ≡ 12 mnemonic words.
// Spec §3.3 (clan-protocol.md v0.2) ratifies this for MINTI's threat model.
const SeedBits = 128

// SeedBytes is SeedBits / 8 = 16.
const SeedBytes = SeedBits / 8

// MnemonicWords is the canonical word count for a 128-bit seed.
const MnemonicWords = 12

// checksumBits per BIP39: ENT / 32 = 4 for 128-bit entropy.
const checksumBits = SeedBits / 32

// totalBits = SeedBits + checksumBits = 132, split into 12 × 11-bit indices.
const totalBits = SeedBits + checksumBits

var (
	wordlist    []string         // index -> word
	wordIndex   map[string]int   // word -> index
	initErrSink error
)

func init() {
	lines := strings.Split(strings.TrimSpace(rawWordlist), "\n")
	if len(lines) != 2048 {
		initErrSink = fmt.Errorf("bip39: wordlist must contain 2048 words, got %d", len(lines))
		panic(initErrSink)
	}
	wordlist = make([]string, 2048)
	wordIndex = make(map[string]int, 2048)
	for i, w := range lines {
		w = strings.TrimSpace(w)
		wordlist[i] = w
		wordIndex[w] = i
	}
}

// MnemonicFromSeed encodes a 16-byte seed as a 12-word BIP39 mnemonic.
func MnemonicFromSeed(seed []byte) (string, error) {
	if len(seed) != SeedBytes {
		return "", fmt.Errorf("bip39: seed must be %d bytes, got %d", SeedBytes, len(seed))
	}

	// Build bit string = entropy ‖ checksum.
	hash := sha256.Sum256(seed)
	bits := make([]byte, totalBits)
	for i := 0; i < SeedBits; i++ {
		bits[i] = bitAt(seed, i)
	}
	for i := 0; i < checksumBits; i++ {
		bits[SeedBits+i] = bitAt(hash[:], i)
	}

	// Split into 12 × 11-bit chunks; each chunk is a word index.
	words := make([]string, MnemonicWords)
	for w := 0; w < MnemonicWords; w++ {
		var idx int
		for b := 0; b < 11; b++ {
			idx = (idx << 1) | int(bits[w*11+b])
		}
		words[w] = wordlist[idx]
	}
	return strings.Join(words, " "), nil
}

// SeedFromMnemonic decodes a 12-word BIP39 mnemonic back to its 16-byte seed,
// validating the BIP39 checksum. Unknown words or a bad checksum are errors.
func SeedFromMnemonic(mnemonic string) ([]byte, error) {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(mnemonic)))
	if len(words) != MnemonicWords {
		return nil, fmt.Errorf("bip39: expected %d words, got %d", MnemonicWords, len(words))
	}

	bits := make([]byte, totalBits)
	for w, word := range words {
		idx, ok := wordIndex[word]
		if !ok {
			return nil, fmt.Errorf("bip39: unknown word %q at position %d", word, w+1)
		}
		// Splat 11 bits, MSB first.
		for b := 0; b < 11; b++ {
			bits[w*11+b] = byte((idx >> (10 - b)) & 1)
		}
	}

	seed := make([]byte, SeedBytes)
	for i := 0; i < SeedBits; i++ {
		if bits[i] == 1 {
			seed[i/8] |= 1 << (7 - (i % 8))
		}
	}
	expectedHash := sha256.Sum256(seed)
	for i := 0; i < checksumBits; i++ {
		want := bitAt(expectedHash[:], i)
		got := bits[SeedBits+i]
		if want != got {
			return nil, errors.New("bip39: checksum mismatch")
		}
	}
	return seed, nil
}

// bitAt returns the i-th bit of b, MSB-first.
func bitAt(b []byte, i int) byte {
	return (b[i/8] >> (7 - (i % 8))) & 1
}

// Wordlist returns a defensive copy of the loaded 2048-word list (testing
// + diagnostics use).
func Wordlist() []string {
	out := make([]string, len(wordlist))
	copy(out, wordlist)
	return out
}
