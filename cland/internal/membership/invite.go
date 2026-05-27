package membership

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// InviteToken is what an existing member hands to a candidate over a
// trusted channel. Single-use: redemption consumes it.
//
// Wire shape matches docs/clan-protocol.md §3.2 response (LAN address +
// cert pin are added at the handler layer, since the InviteStore itself
// doesn't know either).
type InviteToken struct {
	Token     string    `json:"token"`      // base64url(32 random bytes)
	ClanID    string    `json:"clan_id"`
	IssuedBy  string    `json:"issued_by"`  // member_id of the issuer
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Errors callers compare with errors.Is.
var (
	ErrInviteUnknown = errors.New("invite: unknown or already-consumed token")
	ErrInviteExpired = errors.New("invite: token expired")
	ErrInviteTTL     = errors.New("invite: ttl must be between 60s and 24h")
)

const (
	InviteTTLMin = 60 * time.Second
	InviteTTLMax = 24 * time.Hour
)

// InviteStore is an in-memory single-use token store. cland processes
// hold one for the duration of the daemon's lifetime; pending tokens are
// LOST on restart by design (operators can always re-issue, and a token
// surviving a crash would weaken the threat model).
type InviteStore struct {
	mu     sync.Mutex
	tokens map[string]*InviteToken
}

func NewInviteStore() *InviteStore {
	return &InviteStore{tokens: make(map[string]*InviteToken)}
}

// Issue mints a fresh token valid for ttl. Validates ttl is in the
// spec §3.2 range.
func (s *InviteStore) Issue(clanID, issuedBy string, ttl time.Duration) (*InviteToken, error) {
	if ttl < InviteTTLMin || ttl > InviteTTLMax {
		return nil, fmt.Errorf("%w: got %v (min %v, max %v)", ErrInviteTTL, ttl, InviteTTLMin, InviteTTLMax)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("invite: rand: %w", err)
	}
	now := time.Now().UTC()
	tok := &InviteToken{
		Token:     base64.RawURLEncoding.EncodeToString(b),
		ClanID:    clanID,
		IssuedBy:  issuedBy,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	s.mu.Lock()
	s.tokens[tok.Token] = tok
	s.mu.Unlock()
	return tok, nil
}

// Redeem consumes a token. On success the token is removed from the store
// (single-use). Returns ErrInviteUnknown for unknown tokens, ErrInviteExpired
// for past-TTL tokens.
func (s *InviteStore) Redeem(token string) (*InviteToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.tokens[token]
	if !ok {
		return nil, ErrInviteUnknown
	}
	if time.Now().UTC().After(tok.ExpiresAt) {
		delete(s.tokens, token) // garbage-collect expired entry
		return nil, ErrInviteExpired
	}
	delete(s.tokens, token) // single-use consume
	return tok, nil
}

// Sweep removes expired tokens. Called periodically by the daemon to keep
// the in-memory map bounded; failure to sweep just leaves dead entries
// taking memory until daemon restart.
func (s *InviteStore) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	purged := 0
	for k, t := range s.tokens {
		if now.After(t.ExpiresAt) {
			delete(s.tokens, k)
			purged++
		}
	}
	return purged
}

// Size — diagnostic.
func (s *InviteStore) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tokens)
}
