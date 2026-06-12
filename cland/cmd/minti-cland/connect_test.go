package main

import "testing"

func TestConnectBlob_RoundTrip(t *testing.T) {
	const (
		token = "If5ZmCmPa03dDedXHeE_GgdJITd15aP_aaRSXsoTfqU"
		addr  = "192.168.1.195:7777"
		pin   = "sha256:0124e9fd663d5e94c39a73efe125e7af226f86ad2ee654d68000b5fb1e4d6597"
	)
	blob := encodeConnectBlob(token, addr, pin)
	gotT, gotA, gotP, err := decodeConnectBlob(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotT != token || gotA != addr || gotP != pin {
		t.Errorf("round-trip mismatch: %q/%q/%q", gotT, gotA, gotP)
	}
	// Trailing whitespace (pasted blobs) must still decode.
	if _, _, _, err := decodeConnectBlob(blob + "\n"); err != nil {
		t.Errorf("trailing newline should decode: %v", err)
	}
}

func TestDecodeConnectBlob_Rejects(t *testing.T) {
	cases := []string{
		"",
		"hello",
		"MINTI1-not-base64!!",
		"MINTI2-" + "Zm9v", // wrong version prefix
	}
	for _, s := range cases {
		if _, _, _, err := decodeConnectBlob(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}
