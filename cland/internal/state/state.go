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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ClanFile        = "clan.json"
	RevocationsFile = "revocations.json"
	MemoryFile      = "memory.json"
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

// Digest returns the sha256-hex over the sorted, LF-joined member_ids in this
// revocation list. Used by Phase H-2 heartbeat-driven gossip: two daemons
// compare digests, and on mismatch the receiver fetches the full list to
// reconcile. Stable across goroutine call order; ignores per-entry timestamps
// + reasons (only the SET of revoked members matters for membership filtering).
func (r *Revocations) Digest() string {
	if r == nil || len(r.Entries) == 0 {
		// Empty list has a well-defined digest (sha256 of empty input).
		// Both sides compute the same one — no special "absent" case.
		s := sha256.Sum256(nil)
		return hex.EncodeToString(s[:])
	}
	ids := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		ids = append(ids, e.MemberID)
	}
	sort.Strings(ids)
	joined := strings.Join(ids, "\n")
	s := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(s[:])
}

// Merge takes the union of in + other Revocations, deduping by MemberID.
// Returns the merged result. Per-entry metadata (timestamps, reason) from
// the existing entry wins on conflict — the local view of "when did we
// learn about this" is more authoritative than what a peer tells us.
func (r *Revocations) Merge(other *Revocations) *Revocations {
	out := &Revocations{}
	seen := map[string]bool{}
	if r != nil {
		for _, e := range r.Entries {
			if !seen[e.MemberID] {
				out.Entries = append(out.Entries, e)
				seen[e.MemberID] = true
			}
		}
	}
	if other != nil {
		for _, e := range other.Entries {
			if !seen[e.MemberID] {
				out.Entries = append(out.Entries, e)
				seen[e.MemberID] = true
			}
		}
	}
	return out
}

// IsActive reports whether this member is currently part of a Clan.
func (c *Clan) IsActive() bool { return c != nil && c.ClanID != "" }

// RosterDigest returns sha256-hex over sorted "(member_id):(state)" tuples.
// Used by Phase H-3 heartbeat-driven gossip — two daemons compare digests,
// and on mismatch the receiver fetches the full roster to reconcile state
// transitions (admitted → active being the dominant case). Stable across
// goroutine call order; ignores PubKeyB64 + AdmittedAt + LastSeenAt (only
// the (id, state) projection matters for quorum + filtering).
func (c *Clan) RosterDigest() string {
	if c == nil || len(c.Roster) == 0 {
		s := sha256.Sum256(nil)
		return hex.EncodeToString(s[:])
	}
	pairs := make([]string, 0, len(c.Roster))
	for _, m := range c.Roster {
		pairs = append(pairs, m.MemberID+":"+m.State)
	}
	sort.Strings(pairs)
	joined := strings.Join(pairs, "\n")
	s := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(s[:])
}

// MergeRoster takes the union with `other`, preferring the "more progressed"
// state per spec §3.1 hierarchy (revoked > active > admitted > unaffiliated).
// New members in `other` are appended verbatim. Local entries not in `other`
// are preserved. Returns the merged roster; caller persists.
func MergeRosterStates(local, other []RosterMember) []RosterMember {
	rank := map[string]int{"unaffiliated": 0, "admitted": 1, "active": 2, "revoked": 3}
	byID := make(map[string]RosterMember, len(local))
	for _, m := range local {
		byID[m.MemberID] = m
	}
	for _, m := range other {
		existing, present := byID[m.MemberID]
		if !present {
			byID[m.MemberID] = m
			continue
		}
		if rank[m.State] > rank[existing.State] {
			existing.State = m.State
			byID[m.MemberID] = existing
		}
	}
	out := make([]RosterMember, 0, len(byID))
	// Iterate `local` first to preserve order, then any additions from other.
	seen := map[string]bool{}
	for _, m := range local {
		out = append(out, byID[m.MemberID])
		seen[m.MemberID] = true
	}
	for _, m := range other {
		if !seen[m.MemberID] {
			out = append(out, byID[m.MemberID])
			seen[m.MemberID] = true
		}
	}
	return out
}

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

// LoadMemory unmarshals memory.json into v (the memory.Graph — typed as any
// here so state doesn't import the memory package, which imports state).
// Returns found=false with v untouched when the file doesn't exist yet.
func (s *Store) LoadMemory(v any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, MemoryFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("state: parse %s: %w", path, err)
	}
	return true, nil
}

// SaveMemory persists the memory graph atomically at mode 0600 — distillates
// may carry chat content, so it gets clan.json treatment, not the 0644 the
// revocations list gets (spec §13.2).
func (s *Store) SaveMemory(v any) error {
	if v == nil {
		return errors.New("state: SaveMemory(nil)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return saveJSONAtomic(filepath.Join(s.dir, MemoryFile), v, 0o600)
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
