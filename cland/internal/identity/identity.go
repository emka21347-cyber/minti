// Package identity manages this member's persistent identity: a UUIDv4
// member_id + Ed25519 keypair generated once on first run and persisted to
// disk. The private key never leaves this machine.
//
// File format: JSON at `<state_dir>/identity.json`, mode 0600.
// Schema documented in docs/clan-protocol.md §2.1.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Identity struct {
	MemberID  string    `json:"member_id"`
	PubKey    string    `json:"pub_key_b64"`  // base64-std encoded
	PrivKey   string    `json:"priv_key_b64"` // base64-std encoded; sensitive
	CreatedAt time.Time `json:"created_at"`
}

// Path returns the canonical identity file path for the given state dir.
func Path(stateDir string) string {
	return filepath.Join(stateDir, "identity.json")
}

// LoadOrCreate loads the identity from `<stateDir>/identity.json`. If the
// file does not exist, generates a fresh identity, persists it (0600), and
// returns it. Subsequent runs receive the same identity.
func LoadOrCreate(stateDir string) (*Identity, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("identity: mkdir %s: %w", stateDir, err)
	}
	path := Path(stateDir)
	data, err := os.ReadFile(path)
	if err == nil {
		var id Identity
		if err := json.Unmarshal(data, &id); err != nil {
			return nil, fmt.Errorf("identity: parse %s: %w", path, err)
		}
		if err := id.validate(); err != nil {
			return nil, fmt.Errorf("identity: %s: %w", path, err)
		}
		return &id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("identity: read %s: %w", path, err)
	}
	return createAndSave(path)
}

func createAndSave(path string) (*Identity, error) {
	memberID, err := newUUIDv4()
	if err != nil {
		return nil, fmt.Errorf("identity: uuid: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: ed25519: %w", err)
	}
	id := &Identity{
		MemberID:  memberID,
		PubKey:    base64.StdEncoding.EncodeToString(pub),
		PrivKey:   base64.StdEncoding.EncodeToString(priv),
		CreatedAt: time.Now().UTC(),
	}
	if err := id.saveAtomic(path); err != nil {
		return nil, err
	}
	return id, nil
}

func (id *Identity) saveAtomic(path string) error {
	buf, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("identity: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return fmt.Errorf("identity: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("identity: rename: %w", err)
	}
	return nil
}

func (id *Identity) validate() error {
	if id.MemberID == "" {
		return errors.New("missing member_id")
	}
	pub, err := base64.StdEncoding.DecodeString(id.PubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid pub_key_b64")
	}
	priv, err := base64.StdEncoding.DecodeString(id.PrivKey)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid priv_key_b64")
	}
	return nil
}

// PublicKey returns the parsed Ed25519 public key. Callers should treat this
// as advertised material — it goes into capability advertisements.
func (id *Identity) PublicKey() ed25519.PublicKey {
	pub, _ := base64.StdEncoding.DecodeString(id.PubKey)
	return pub
}

// PrivateKey returns the parsed Ed25519 private key. Hold onto the reference
// briefly; do not log it.
func (id *Identity) PrivateKey() ed25519.PrivateKey {
	priv, _ := base64.StdEncoding.DecodeString(id.PrivKey)
	return priv
}

// newUUIDv4 generates a random UUID v4 from crypto/rand. Avoids pulling in a
// third-party dep for one function.
func newUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // variant 10xx
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16],
	), nil
}
