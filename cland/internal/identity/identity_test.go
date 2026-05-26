package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestLoadOrCreate_GeneratesOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !uuidRe.MatchString(id.MemberID) {
		t.Errorf("not a UUIDv4: %q", id.MemberID)
	}
	if len(id.PublicKey()) != ed25519.PublicKeySize {
		t.Errorf("pub key size wrong")
	}
	if len(id.PrivateKey()) != ed25519.PrivateKeySize {
		t.Errorf("priv key size wrong")
	}
	if id.CreatedAt.IsZero() {
		t.Errorf("created_at not set")
	}
	// File must exist.
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Errorf("identity.json not persisted: %v", err)
	}
}

func TestLoadOrCreate_IdempotentAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.MemberID != b.MemberID {
		t.Errorf("member_id changed across runs: %q vs %q", a.MemberID, b.MemberID)
	}
	if a.PubKey != b.PubKey || a.PrivKey != b.PrivKey {
		t.Errorf("keys changed across runs")
	}
}

func TestLoadOrCreate_FileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on Windows")
	}
	dir := t.TempDir()
	if _, err := LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("identity.json mode = %o, want 0600", mode)
	}
}

func TestLoadOrCreate_CorruptedFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Errorf("expected error on corrupt identity.json")
	}
}

func TestLoadOrCreate_InvalidKeyLengthErrors(t *testing.T) {
	dir := t.TempDir()
	// Write a syntactically valid JSON identity with a short pub_key.
	bad := []byte(`{"member_id":"12345678-1234-4234-8234-123456789abc",` +
		`"pub_key_b64":"YWFh",` +
		`"priv_key_b64":"YWFh",` +
		`"created_at":"2026-01-01T00:00:00Z"}`)
	if err := os.WriteFile(Path(dir), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Errorf("expected error on invalid key length")
	}
}

func TestPublicPrivateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Sign with priv, verify with pub.
	msg := make([]byte, 32)
	_, _ = rand.Read(msg)
	sig := ed25519.Sign(id.PrivateKey(), msg)
	if !ed25519.Verify(id.PublicKey(), msg, sig) {
		t.Errorf("ed25519 sign/verify round-trip failed")
	}
	// Sanity-check the base64 fields decode to the same bytes.
	pubBytes, _ := base64.StdEncoding.DecodeString(id.PubKey)
	if len(pubBytes) != ed25519.PublicKeySize {
		t.Errorf("base64 pub decoded to wrong size: %d", len(pubBytes))
	}
}

func TestMkdirFailureSurfacesError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
	// Create a file where the dir should be — MkdirAll should fail.
	dir := t.TempDir()
	conflict := filepath.Join(dir, "id-dir")
	if err := os.WriteFile(conflict, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(conflict); err == nil {
		t.Errorf("expected error when state path is a file, not a dir")
	}
}
