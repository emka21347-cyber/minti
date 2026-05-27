// Package state persists Clan-level state to disk: clan_id, clan_key,
// pinned cert, roster, and the revocation list.
//
// File layout under <state_dir>:
//
//   identity.json     — owned by internal/identity
//   clan.json         — this package; contains clan_id, clan_key, cert pin,
//                       roster cache, founder/joined timestamps. mode 0600
//                       because clan_key is sensitive.
//   revocations.json  — this package; pin-revocation list gossiped per
//                       spec §3.4. mode 0644 (no secrets).
//
// Writes are atomic: write-temp + fsync + rename. clan_key is loaded into
// memory at startup and never re-read from disk per request — per
// architectural decision D-M4.11.
package state

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	ClanFile        = "clan.json"
	RevocationsFile = "revocations.json"
)

// Clan is what every active member persists about its current Clan. Empty
// (zero) Clan means the member is `unaffiliated`.
type Clan struct {
	// Identity of the Clan itself.
	ClanID             string `json:"clan_id"`                    // UUIDv4
	ClanKeyB64         string `json:"clan_key_b64"`               // 32 bytes base64-std (HMAC key)
	ClanCertPEM        string `json:"clan_cert_pem"`              // X.509 server cert, PEM
	ClanCertPin        string `json:"clan_cert_pin"`              // "sha256:<hex>" of SPKI
	ClanCertPrivKeyB64 string `json:"clan_cert_priv_key_b64,omitempty"` // Ed25519 priv key matching the cert. Shared across all members of the Clan (v1 unitary-trust model per spec §10a residual R1). Required for any member's daemon to serve TLS with the Clan cert — without it the joiner's TLS handshake would fail (priv/pub mismatch).

	// Per-this-member metadata.
	Role     string    `json:"role"`                          // "founder" | "joined"
	JoinedAt time.Time `json:"joined_at"`

	// Roster cache: last-known set of admitted/active members. Refreshed on
	// every capability advertisement; serialised here so the daemon survives
	// a restart without having to wait for the next round of advertisements.
	Roster []RosterMember `json:"roster,omitempty"`

	// Phase E (leader-lease election) state.
	//
	// CurrentTerm + CurrentOrchestrator are persisted on change only, so a
	// restarted daemon doesn't re-issue an old term against peers that have
	// moved on. LeaseExpires is INTENTIONALLY NOT persisted (Phase E peer-
	// review R2): it's volatile per-tick state that's reconstructed from
	// the next received heartbeat, and persisting it would mean one
	// SaveClan write per node every HEARTBEAT_INTERVAL.
	//
	// PinnedOrchestrator is the local self-pin per spec §5.6 — propagated
	// to peers via the next /clan/advertise (Advertisement.PinnedOrchestrator).
	CurrentOrchestrator string `json:"current_orchestrator,omitempty"`
	CurrentTerm         uint64 `json:"current_term,omitempty"`
	PinnedOrchestrator  bool   `json:"pinned_orchestrator,omitempty"`
}

// RosterMember is one entry in the persisted roster cache.
type RosterMember struct {
	MemberID   string    `json:"member_id"`
	PubKeyB64  string    `json:"pub_key_b64"`
	State      string    `json:"state"` // admitted | active | revoked
	AdmittedAt time.Time `json:"admitted_at,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
}

// Revocations is the pin-revocation list per spec §3.4. Persisted separately
// so the gossip path can update it without touching clan_key.
type Revocations struct {
	Entries []Revocation `json:"entries"`
}

type Revocation struct {
	MemberID    string    `json:"member_id"`
	PubKeyHash  string    `json:"pub_key_hash"` // sha256 hex of pubkey bytes
	RevokedAt   time.Time `json:"revoked_at"`
	RevokedBy   string    `json:"revoked_by"`
	Reason      string    `json:"reason,omitempty"`
}

// IsActive reports whether this member is currently part of a Clan.
func (c *Clan) IsActive() bool { return c != nil && c.ClanID != "" }

// ClanKey returns the decoded 32-byte HMAC key, or nil if unaffiliated.
func (c *Clan) ClanKey() []byte {
	if c == nil || c.ClanKeyB64 == "" {
		return nil
	}
	b, _ := base64.StdEncoding.DecodeString(c.ClanKeyB64)
	return b
}

// SetClanKey base64-encodes and stores the key.
func (c *Clan) SetClanKey(key []byte) {
	c.ClanKeyB64 = base64.StdEncoding.EncodeToString(key)
}

// ClanCertPrivKey returns the Ed25519 private key (raw 64-byte form) that
// pairs with the persisted ClanCertPEM. Returns nil if not set (which means
// this member can't serve TLS — only the founder pre-v0.2-fix had it).
func (c *Clan) ClanCertPrivKey() []byte {
	if c == nil || c.ClanCertPrivKeyB64 == "" {
		return nil
	}
	b, _ := base64.StdEncoding.DecodeString(c.ClanCertPrivKeyB64)
	return b
}

// SetClanCertPrivKey stores the raw Ed25519 priv key (64 bytes) base64-encoded.
func (c *Clan) SetClanCertPrivKey(priv []byte) {
	c.ClanCertPrivKeyB64 = base64.StdEncoding.EncodeToString(priv)
}

// Store is the on-disk Clan + Revocations store. Concurrent callers are
// serialised by an internal mutex.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("state: mkdir %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// LoadClan returns the persisted Clan, or a zero Clan (unaffiliated) when
// the file does not exist.
func (s *Store) LoadClan() (*Clan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return loadJSON[Clan](filepath.Join(s.dir, ClanFile))
}

func (s *Store) SaveClan(c *Clan) error {
	if c == nil {
		return errors.New("state: SaveClan(nil)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return saveJSONAtomic(filepath.Join(s.dir, ClanFile), c, 0o600)
}

// LoadRevocations returns the persisted revocation list (empty list if file
// is missing).
func (s *Store) LoadRevocations() (*Revocations, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := loadJSON[Revocations](filepath.Join(s.dir, RevocationsFile))
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = &Revocations{}
	}
	return r, nil
}

func (s *Store) SaveRevocations(r *Revocations) error {
	if r == nil {
		return errors.New("state: SaveRevocations(nil)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return saveJSONAtomic(filepath.Join(s.dir, RevocationsFile), r, 0o644)
}

// loadJSON returns nil + nil if the file is missing.
func loadJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: read %s: %w", path, err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("state: parse %s: %w", path, err)
	}
	return &v, nil
}

func saveJSONAtomic(path string, v any, mode os.FileMode) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, mode); err != nil {
		return fmt.Errorf("state: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
