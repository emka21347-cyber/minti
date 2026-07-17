package main

import "testing"

func TestConnectBlob_RoundTrip(t *testing.T) {
	// Synthetic fixtures (not a real node): token/addr/pin only exercise the
	// encode↔decode round-trip, so any well-formed values work. Address uses the
	// RFC 5737 documentation range.
	const (
		token = "dGVzdC1pbnZpdGUtdG9rZW4tZml4dHVyZS1ub3QtcmVhbA"
		addr  = "203.0.113.10:7777"
		pin   = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
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
