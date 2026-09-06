package crypto

import (
	"crypto/rand"
	"strings"
	"testing"
)

func randKey(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestComputeMAC_Deterministic(t *testing.T) {
	key := randKey(t, 32)
	a := ComputeMAC(key, "POST", "/clan/heartbeat", []byte(`{"x":1}`), 1234567890, "nonce-abc")
	b := ComputeMAC(key, "POST", "/clan/heartbeat", []byte(`{"x":1}`), 1234567890, "nonce-abc")
	if a != b {
		t.Errorf("MAC not deterministic: %s vs %s", a, b)
	}
	if len(a) != 64 { // sha256 hex = 64 chars
		t.Errorf("expected 64-char hex sha256, got %d: %s", len(a), a)
	}
}

func TestComputeMAC_MethodCanonicalised(t *testing.T) {
	key := randKey(t, 32)
	lower := ComputeMAC(key, "post", "/x", nil, 1, "n")
	upper := ComputeMAC(key, "POST", "/x", nil, 1, "n")
	if lower != upper {
		t.Errorf("method case should be canonicalised; got %s != %s", lower, upper)
	}
}

func TestVerifyMAC_RoundTrip(t *testing.T) {
	key := randKey(t, 32)
	mac := ComputeMAC(key, "POST", "/clan/advertise", []byte("body"), 9999, "nce")
	if !VerifyMAC(key, "POST", "/clan/advertise", []byte("body"), 9999, "nce", mac) {
		t.Errorf("verify should accept matching mac")
	}
	if !VerifyMAC(key, "POST", "/clan/advertise", []byte("body"), 9999, "nce", strings.ToUpper(mac)) {
		t.Errorf("verify should ignore case in provided hex")
	}
}

func TestVerifyMAC_RejectsTamper(t *testing.T) {
	key := randKey(t, 32)
	mac := ComputeMAC(key, "POST", "/x", []byte("a"), 1, "n")

	cases := []struct {
		name                    string
		method, path, body, nce string
		ts                      int64
	}{
		{"different body", "POST", "/x", "b", "n", 1},
		{"different path", "POST", "/y", "a", "n", 1},
		{"different method", "GET", "/x", "a", "n", 1},
		{"different ts", "POST", "/x", "a", "n", 2},
		{"different nonce", "POST", "/x", "a", "m", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if VerifyMAC(key, c.method, c.path, []byte(c.body), c.ts, c.nce, mac) {
				t.Errorf("tamper accepted")
			}
		})
	}
}

func TestVerifyMAC_RejectsWrongKey(t *testing.T) {
	macA := ComputeMAC(randKey(t, 32), "POST", "/x", nil, 1, "n")
	if VerifyMAC(randKey(t, 32), "POST", "/x", nil, 1, "n", macA) {
		t.Errorf("wrong key accepted")
	}
}

func TestVerifyMAC_RejectsGarbageHex(t *testing.T) {
	if VerifyMAC(randKey(t, 32), "POST", "/x", nil, 1, "n", "zzz") {
		t.Errorf("garbage hex accepted")
	}
}

func TestNewNonce(t *testing.T) {
	n1, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	n2, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	if n1 == n2 {
		t.Errorf("two nonces collided (chance ~2^-128)")
	}
	if len(n1) != 32 {
		t.Errorf("expected 16-byte hex (32 chars), got %d: %s", len(n1), n1)
	}
}
