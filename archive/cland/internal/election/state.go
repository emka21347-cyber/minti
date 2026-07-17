// Package election implements the spec §5 leader-lease election:
//
//   - One Orchestrator at a time per Clan, identified by (term, member_id).
//   - Orchestrator emits a HEARTBEAT_INTERVAL (2 s) pulse carrying lease_until.
//   - Receivers track lease locally (R4) — wire lease_until is informational.
//   - If lease silence exceeds FAILOVER_GRACE (6 s), peers run an election.
//   - Election commits when the candidate has accepts ≥ ⌈N/2⌉ of the
//     PERSISTED ACTIVE roster (R5) — never the live registry — so a minority
//     partition can't fake quorum.
//
// State that needs to survive restarts is in state.Clan (R2: term +
// orchestrator only). leaseExpires is in-memory, reconstructed from the next
// heartbeat. The history ring is also in-memory; surviving via re-election.
package election

import (
	"errors"
	"sync"
	"time"
)

// Reasons recorded in election-history entries.
const (
	ReasonBootstrap   = "bootstrap"
	ReasonLeaseExpire = "lease_expired"
	ReasonPinOverride = "pin_override"
	ReasonScoreChange = "score_change"
	ReasonRetry       = "split_brain_retry"
)

// Errors returned by State.ApplyHeartbeat. Handler maps these to HTTP codes.
var (
	ErrTermStale       = errors.New("election: heartbeat term older than current")
	ErrAntiSpoof       = errors.New("election: sender is not the highest-scoring candidate in our view")
	ErrUnaffiliated    = errors.New("election: daemon is unaffiliated")
)

// Heartbeat is the spec §5.3 wire payload, extended in Phase H-2 with
// `revocations_digest` so peers can detect when their local revocations list
// has drifted from the sender's. On mismatch the receiver fetches the full
// list via GET /clan/revocations from the sender's address.
type Heartbeat struct {
	MemberID           string    `json:"member_id"`
	ClanID             string    `json:"clan_id"`
	Term               uint64    `json:"term"`
	LeaseUntil         time.Time `json:"lease_until"`
	ReasoningScore     int       `json:"reasoning_score"`
	ActiveRoster       []string  `json:"active_roster"`
	RevocationsDigest  string    `json:"revocations_digest,omitempty"` // Phase H-2: sha256 of sorted revoked member_ids
	RosterDigest       string    `json:"roster_digest,omitempty"`      // Phase H-3: sha256 of sorted (member_id, state) tuples
	MemoryDigest       string    `json:"memory_digest,omitempty"`      // Memory M2: spec §13.5 content-versioned graph digest
	Scribe             string    `json:"scribe,omitempty"`             // Memory M3: spec §13.8 Orchestrator-authoritative Scribe selection
}

// HistoryEntry is one row in the in-memory ring at /clan/election/history.
type HistoryEntry struct {
	Term    uint64    `json:"term"`
	Winner  string    `json:"winner"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"at"`
}

// State is the daemon's local view of the lease + election state. Persistence
// of (CurrentTerm, CurrentOrchestrator) is the caller's responsibility — the
// engine calls state.Store.SaveClan() *only when those fields change* (R2).
// LeaseExpires is in-memory only; reconstructed from the next heartbeat.
type State struct {
	mu sync.Mutex

	selfID    string
	startedAt time.Time

	currentTerm         uint64
	currentOrchestrator string
	leaseExpires        time.Time

	// currentScribe is the Memory M3 (spec §13.8) role: selected by the
	// Orchestrator, adopted by followers from the heartbeat `scribe` field.
	// No lease — re-selected by the Orchestrator whenever the holder stops
	// being eligible. Persistence (Clan.CurrentScribe) is the engine's
	// responsibility, on change only.
	currentScribe string

	// In-memory ring of recent elections, size = cfg.HistorySize.
	history    []HistoryEntry
	historyCap int
}

// NewState constructs a State seeded with what's persisted in state.Clan.
// startedAt is captured here so the engine can enforce the R6 startup grace.
func NewState(selfID string, persistedTerm uint64, persistedOrchestrator string, historyCap int) *State {
	if historyCap <= 0 {
		historyCap = 32
	}
	return &State{
		selfID:              selfID,
		startedAt:           time.Now(),
		currentTerm:         persistedTerm,
		currentOrchestrator: persistedOrchestrator,
		historyCap:          historyCap,
	}
}

// CurrentScribe returns the in-memory scribe selection ("" = none).
func (s *State) CurrentScribe() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentScribe
}

// SetScribe records a new scribe selection; reports whether it changed.
func (s *State) SetScribe(memberID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentScribe == memberID {
		return false
	}
	s.currentScribe = memberID
	return true
}

// SeedScribe sets the initial scribe from persisted state without the
// "changed" semantics — called once at startup.
func (s *State) SeedScribe(memberID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentScribe = memberID
}

// Snapshot returns an atomic copy of the public fields. Used by handlers
// (GET /clan/orchestrator) and by Engine.tick() for race-free reads.
type Snapshot struct {
	SelfID              string
	StartedAt           time.Time
	CurrentTerm         uint64
	CurrentOrchestrator string
	LeaseExpires        time.Time
}

func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		SelfID:              s.selfID,
		StartedAt:           s.startedAt,
		CurrentTerm:         s.currentTerm,
		CurrentOrchestrator: s.currentOrchestrator,
		LeaseExpires:        s.leaseExpires,
	}
}

// ApplyHeartbeat is called from the /clan/heartbeat handler. The anti-spoof
// check (sender == highest-scoring candidate in our registry) is the
// caller's responsibility — pass `acceptable=false` to force ErrAntiSpoof.
//
// Returns:
//   - (true, nil)  — accepted, state updated, persistence is needed if
//                    `termChanged` or `orchChanged` are true.
//   - (false, ErrTermStale)  — term < currentTerm; emit 409.
//   - (false, ErrAntiSpoof)  — sender not the highest-scoring candidate; 409.
//
// On accept, R4: leaseExpires = now + leaseDuration (NOT hb.LeaseUntil).
type ApplyResult struct {
	Accepted     bool
	TermChanged  bool
	OrchChanged  bool
	NewTerm      uint64
	NewOrch      string
}

func (s *State) ApplyHeartbeat(hb Heartbeat, now time.Time, leaseDuration time.Duration, acceptable bool) (ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hb.Term < s.currentTerm {
		return ApplyResult{}, ErrTermStale
	}
	// Anti-spoof only applies same-term. A higher term implies a successful
	// election elsewhere; refusing it would split-brain a recovering daemon
	// whose registry hasn't yet caught up with the cluster's current view of
	// who should be Orchestrator. Raft / standard leader-lease convention.
	if hb.Term == s.currentTerm && !acceptable {
		return ApplyResult{}, ErrAntiSpoof
	}
	termChanged := hb.Term != s.currentTerm
	orchChanged := hb.MemberID != s.currentOrchestrator
	s.currentTerm = hb.Term
	s.currentOrchestrator = hb.MemberID
	s.leaseExpires = now.Add(leaseDuration) // R4 — local tracking, ignore hb.LeaseUntil
	if termChanged || orchChanged {
		s.pushHistoryLocked(HistoryEntry{
			Term:   hb.Term,
			Winner: hb.MemberID,
			Reason: ReasonLeaseExpire,
			At:     now,
		})
	}
	return ApplyResult{
		Accepted:    true,
		TermChanged: termChanged,
		OrchChanged: orchChanged,
		NewTerm:     hb.Term,
		NewOrch:     hb.MemberID,
	}, nil
}

// CommitSelfElection is called by the engine when *we* have just won an
// election. Sets self as Orchestrator, increments term, sets lease, records
// history.
func (s *State) CommitSelfElection(newTerm uint64, reason string, now time.Time, leaseDuration time.Duration) ApplyResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	termChanged := newTerm != s.currentTerm
	orchChanged := s.selfID != s.currentOrchestrator
	s.currentTerm = newTerm
	s.currentOrchestrator = s.selfID
	s.leaseExpires = now.Add(leaseDuration)
	s.pushHistoryLocked(HistoryEntry{
		Term:   newTerm,
		Winner: s.selfID,
		Reason: reason,
		At:     now,
	})
	return ApplyResult{
		Accepted:    true,
		TermChanged: termChanged,
		OrchChanged: orchChanged,
		NewTerm:     newTerm,
		NewOrch:     s.selfID,
	}
}

// LeaseSilent reports true iff lease has expired and the FAILOVER_GRACE
// silence window has also passed. Also true on cold start (lease never set).
// Callers gate on R6 startup grace separately.
func (s *State) LeaseSilent(now time.Time, failoverGrace time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaseExpires.IsZero() {
		return true
	}
	return now.After(s.leaseExpires.Add(failoverGrace))
}

// IAmOrchestrator returns true iff this daemon currently holds the lease
// and its lease has not yet expired.
func (s *State) IAmOrchestrator(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentOrchestrator == s.selfID && !s.leaseExpires.IsZero() && now.Before(s.leaseExpires)
}

// History returns a copy of the in-memory ring, newest last.
func (s *State) History() []HistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HistoryEntry, len(s.history))
	copy(out, s.history)
	return out
}

// AppendHistory adds an entry directly — used when an election aborts (for
// observability) without touching term/lease.
func (s *State) AppendHistory(e HistoryEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushHistoryLocked(e)
}

func (s *State) pushHistoryLocked(e HistoryEntry) {
	s.history = append(s.history, e)
	if len(s.history) > s.historyCap {
		s.history = s.history[len(s.history)-s.historyCap:]
	}
}
