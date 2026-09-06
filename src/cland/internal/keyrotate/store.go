package keyrotate

import (
	"sync"
	"time"
)

// Rotater is the subset of crypto.KeyProvider the member handler needs.
// Decoupled so member tests can use a fake rotater.
type Rotater interface {
	Rotate(newKey []byte, graceDur time.Duration) error
}

// ProposeStore holds in-memory state about a pending propose. At most ONE
// propose may be pending at a time per member — receiving a second propose
// with a different id while one is pending returns ErrAlreadyPending.
//
// Auto-revert: a propose older than MemberRevertAfter is treated as expired
// and overwritten on the next acceptable propose; the Sweep goroutine (if
// started) actively clears expired entries so /clan/rotate-key/abort isn't
// needed in the orchestrator-crash case.
type ProposeStore struct {
	mu      sync.Mutex
	pending *pending
	now     func() time.Time // overridable for tests
}

type pending struct {
	ProposeID string
	NewKey    []byte
	ReceivedAt time.Time
}

// NewProposeStore returns an empty store. now defaults to time.Now if nil.
func NewProposeStore(now func() time.Time) *ProposeStore {
	if now == nil {
		now = time.Now
	}
	return &ProposeStore{now: now}
}

// Put accepts a propose. Returns:
//   - nil + true   if accepted (no pending, or overwrites an expired one)
//   - ErrAlreadyPending + false if a DIFFERENT propose is pending and fresh
//   - nil + true   if the SAME propose_id arrived twice (idempotent ACK)
func (s *ProposeStore) Put(p ProposeRequest) (bool, error) {
	newKey, err := p.NewKey()
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.pending != nil {
		if s.pending.ProposeID == p.ProposeID {
			// Idempotent — duplicate propose, same id.
			return true, nil
		}
		if now.Sub(s.pending.ReceivedAt) < MemberRevertAfter {
			return false, ErrAlreadyPending
		}
		// Existing pending is expired; safe to overwrite.
	}
	s.pending = &pending{ProposeID: p.ProposeID, NewKey: newKey, ReceivedAt: now}
	return true, nil
}

// Take pops the pending state if its id matches. Returns the new key bytes
// and true on success, nil and false otherwise.
func (s *ProposeStore) Take(proposeID string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.ProposeID != proposeID {
		return nil, false
	}
	key := s.pending.NewKey
	s.pending = nil
	return key, true
}

// Clear removes any pending state (used by abort).
func (s *ProposeStore) Clear(proposeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil || s.pending.ProposeID != proposeID {
		return false
	}
	s.pending = nil
	return true
}

// Pending returns the currently-pending propose_id ("" if none) — for
// tests and diagnostics.
func (s *ProposeStore) Pending() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return ""
	}
	return s.pending.ProposeID
}

// SweepExpired drops any pending propose older than MemberRevertAfter.
// Returns true if a sweep happened. Caller would typically invoke this on
// a ticker; for tests, call directly.
func (s *ProposeStore) SweepExpired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return false
	}
	if s.now().Sub(s.pending.ReceivedAt) < MemberRevertAfter {
		return false
	}
	s.pending = nil
	return true
}
