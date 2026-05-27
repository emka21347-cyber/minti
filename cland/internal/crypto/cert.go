// Package crypto holds Clan-level cryptographic primitives: self-signed
// X.509 cert generation, SPKI pin computation, HMAC over the canonical
// request form, and the KeyProvider interface used by the transport layer
// during a key-rotation grace window.
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"
)

// ClanCert is the per-Clan self-signed cert pinned by every member. The PEM
// bytes are persisted in state.Clan.ClanCertPEM; the pin in state.Clan.ClanCertPin.
type ClanCert struct {
	CertPEM   []byte             // X.509 in PEM form
	CertDER   []byte             // X.509 DER (re-derived from PEM on parse)
	Pin       string             // "sha256:<hex>" of SubjectPublicKeyInfo
	NotBefore time.Time
	NotAfter  time.Time
	parsed    *x509.Certificate  // cached parse
}

// GenerateClanCert produces a self-signed X.509 certificate using the
// supplied Ed25519 keypair. Valid for `validity` from now. SANs are
// localhost + 127.0.0.1 + any extra IPs/DNS names the caller passes — the
// pin is what authenticates, but SANs let standard TLS libraries succeed
// at hostname verification when the client points at one of those names.
func GenerateClanCert(priv ed25519.PrivateKey, validity time.Duration, extraIPs []net.IP, extraDNS []string) (*ClanCert, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("crypto.GenerateClanCert: invalid Ed25519 private key")
	}
	pub := priv.Public().(ed25519.PublicKey)

	// 128-bit random serial number is the convention.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("crypto: serial: %w", err)
	}

	now := time.Now().UTC()
	notAfter := now.Add(validity)

	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	ips = append(ips, extraIPs...)
	dns := append([]string{"localhost"}, extraDNS...)

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "MINTI Clan", Organization: []string{"MINTI"}},
		NotBefore:             now,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           ips,
		DNSNames:              dns,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		return nil, fmt.Errorf("crypto: CreateCertificate: %w", err)
	}
	parsed, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("crypto: ParseCertificate(self): %w", err)
	}

	pemBlock := &pem.Block{Type: "CERTIFICATE", Bytes: certDER}
	return &ClanCert{
		CertPEM:   pem.EncodeToMemory(pemBlock),
		CertDER:   certDER,
		Pin:       spkiPin(parsed),
		NotBefore: parsed.NotBefore,
		NotAfter:  parsed.NotAfter,
		parsed:    parsed,
	}, nil
}

// ParseClanCertPEM decodes a PEM-encoded clan_cert and recomputes its pin.
func ParseClanCertPEM(pemBytes []byte) (*ClanCert, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("crypto: invalid PEM (no CERTIFICATE block)")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse cert: %w", err)
	}
	return &ClanCert{
		CertPEM:   pemBytes,
		CertDER:   block.Bytes,
		Pin:       spkiPin(parsed),
		NotBefore: parsed.NotBefore,
		NotAfter:  parsed.NotAfter,
		parsed:    parsed,
	}, nil
}

// X509 returns the parsed certificate.
func (c *ClanCert) X509() *x509.Certificate { return c.parsed }

// VerifyPin returns nil if pin matches this cert's SPKI hash.
func (c *ClanCert) VerifyPin(pin string) error {
	if pin == "" {
		return errors.New("crypto: empty pin")
	}
	if pin != c.Pin {
		return fmt.Errorf("crypto: pin mismatch (have %s, want %s)", c.Pin, pin)
	}
	return nil
}

// spkiPin computes the canonical SPKI sha256 pin for an X.509 certificate.
// We hash the cert's RawSubjectPublicKeyInfo bytes (the DER-encoded
// SubjectPublicKeyInfo), prefix with "sha256:", and hex-encode. This is the
// same format used in the spec §3.2 `clan_cert_pin`.
func spkiPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// VerifyServerCertPin is the callback to plug into tls.Config.VerifyPeerCertificate.
// It walks every cert the server presented and accepts the chain iff at least
// one cert matches the pin. Hostname verification is bypassed (we pin instead).
func VerifyServerCertPin(pin string) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("crypto: no peer certs presented")
		}
		for _, raw := range rawCerts {
			parsed, err := x509.ParseCertificate(raw)
			if err != nil {
				continue
			}
			if spkiPin(parsed) == pin {
				return nil
			}
		}
		return fmt.Errorf("crypto: no peer cert matched pin %s", pin)
	}
}
