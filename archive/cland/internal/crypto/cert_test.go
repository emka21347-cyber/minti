package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"testing"
	"time"
)

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestGenerateClanCert_ProducesValidCert(t *testing.T) {
	priv := mustKey(t)
	cc, err := GenerateClanCert(priv, 365*24*time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cc.Pin == "" || !strings.HasPrefix(cc.Pin, "sha256:") {
		t.Errorf("bad pin format: %q", cc.Pin)
	}
	if cc.X509().IsCA != true {
		t.Errorf("self-signed cert must have IsCA=true")
	}
	if !cc.NotAfter.After(time.Now().Add(364 * 24 * time.Hour)) {
		t.Errorf("validity too short: %v", cc.NotAfter)
	}
	// SANs must include localhost + 127.0.0.1
	hasLocalhost := false
	for _, n := range cc.X509().DNSNames {
		if n == "localhost" {
			hasLocalhost = true
		}
	}
	if !hasLocalhost {
		t.Errorf("missing localhost SAN: %v", cc.X509().DNSNames)
	}
	hasLoopback := false
	for _, ip := range cc.X509().IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			hasLoopback = true
		}
	}
	if !hasLoopback {
		t.Errorf("missing 127.0.0.1 SAN: %v", cc.X509().IPAddresses)
	}
}

func TestParseClanCertPEM_RoundTrip(t *testing.T) {
	priv := mustKey(t)
	orig, err := GenerateClanCert(priv, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseClanCertPEM(orig.CertPEM)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Pin != orig.Pin {
		t.Errorf("pin mismatch after PEM round-trip: orig=%s parsed=%s", orig.Pin, parsed.Pin)
	}
}

func TestParseClanCertPEM_RejectsGarbage(t *testing.T) {
	if _, err := ParseClanCertPEM([]byte("not a pem")); err == nil {
		t.Errorf("expected error on garbage PEM")
	}
}

func TestVerifyPin(t *testing.T) {
	cc, err := GenerateClanCert(mustKey(t), time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cc.VerifyPin(cc.Pin); err != nil {
		t.Errorf("self-pin should verify: %v", err)
	}
	if err := cc.VerifyPin("sha256:0000"); err == nil {
		t.Errorf("wrong pin should fail")
	}
	if err := cc.VerifyPin(""); err == nil {
		t.Errorf("empty pin should fail")
	}
}

func TestVerifyServerCertPin_AcceptsMatch(t *testing.T) {
	priv := mustKey(t)
	cc, err := GenerateClanCert(priv, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := VerifyServerCertPin(cc.Pin)
	if err := verifier([][]byte{cc.CertDER}, nil); err != nil {
		t.Errorf("matching pin should accept: %v", err)
	}
}

func TestVerifyServerCertPin_RejectsMismatch(t *testing.T) {
	cc, err := GenerateClanCert(mustKey(t), time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Generate a *different* cert; its pin won't match the one we expect.
	other, err := GenerateClanCert(mustKey(t), time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier := VerifyServerCertPin(cc.Pin)
	if err := verifier([][]byte{other.CertDER}, nil); err == nil {
		t.Errorf("mismatched pin should fail")
	}
	if err := verifier(nil, nil); err == nil {
		t.Errorf("empty cert list should fail")
	}
}

// TestCertIsUsableAsTLSServerCert sanity-checks that the cert we generate can
// actually back a tls.Config; this catches issues like key-cert pairing or
// missing fields before the transport layer hits them.
func TestCertIsUsableAsTLSServerCert(t *testing.T) {
	priv := mustKey(t)
	cc, err := GenerateClanCert(priv, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tlsCert := tls.Certificate{
		Certificate: [][]byte{cc.CertDER},
		PrivateKey:  priv,
		Leaf:        cc.X509(),
	}
	// The verifier should accept the pair via x509.Certificate.CheckSignatureFrom(self).
	if err := tlsCert.Leaf.CheckSignatureFrom(tlsCert.Leaf); err != nil {
		t.Errorf("self-signed cert failed self-signature check: %v", err)
	}
	// Also confirm the parsed leaf matches what we'd parse back from DER.
	parsed, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Errorf("parse: %v", err)
	}
	if parsed.Subject.CommonName != "MINTI Clan" {
		t.Errorf("unexpected CN: %q", parsed.Subject.CommonName)
	}
}
