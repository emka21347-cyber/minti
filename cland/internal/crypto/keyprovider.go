package crypto

import (
	"errors"
	"sync"
	"time"
)

// KeyProvider serves the current Clan Key (and, during a key-rotation grace
// window, the previous one) to HMAC verifiers and signers.
//
// Designed-in from Phase B per D-M4.11 so that Phase H's two-key grace
// window doesn't require a destructive rewrite of the transport layer.
type KeyProvider interface {
	// Current returns the active key. Always present once the Clan is formed.
	Current() []byte
	// Grace returns the previous key + true while the rotation grace window
	// is open; (nil, false) otherwise.
	Grace() ([]byte, bool)
}

// SimpleKeyProvider is the goroutine-safe in-memory KeyProvider used by the
// daemon. State is held entirely in memory; the canonical clan_key lives in
// state.Clan and is reloaded from disk only on daemon startup.
type SimpleKeyProvider struct {
	mu          sync.RWMutex
	current     []byte
	grace       []byte
	graceExpiry time.Time
}

// NewSimpleKeyProvider wraps a single 32-byte key.
func NewSimpleKeyProvider(key []byte) (*SimpleKeyProvider, error) {
	if len(key) == 0 {
		return nil, errors.New("crypto.NewSimpleKeyProvider: empty key")
	}
	return &SimpleKeyProvider{current: cloneBytes(key)}, nil
}

func (p *SimpleKeyProvider) Current() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneBytes(p.current)
}

func (p *SimpleKeyProvider) Grace() ([]byte, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.grace == nil || time.Now().After(p.graceExpiry) {
		return nil, false
	}
	return cloneBytes(p.grace), true
}

// Rotate replaces the current key with newKey and keeps the old key valid
// for `graceDur` as the grace key. Phase H's /clan/rotate-key commits land
// here.
func (p *SimpleKeyProvider) Rotate(newKey []byte, graceDur time.Duration) error {
	if len(newKey) == 0 {
		return errors.New("crypto.Rotate: empty new key")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grace = p.current
	p.graceExpiry = time.Now().Add(graceDur)
	p.current = cloneBytes(newKey)
	return nil
}

// DropGrace clears any grace key — used to abort a rotation cleanly when
// the propose-commit timeout elapses without commit.
func (p *SimpleKeyProvider) DropGrace() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.grace = nil
	p.graceExpiry = time.Time{}
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
