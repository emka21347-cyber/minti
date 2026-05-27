// Package peers is cland's runtime view of the Clan. It tracks who's
// currently reachable, where (which `ip:port`), and what each member last
// said about itself (capability advertisement payload + scores).
//
// Two stores (per Phase D peer-review fix qwen3.6 1A):
//
//   - **Candidate addresses**: things we know are *somewhere* on the network,
//     from mDNS or `/clan/peer-add`. No identity binding. The mDNS TXT
//     `member_id` is unauthenticated and must not be trusted, so it doesn't
//     even land here.
//   - **Member-keyed entries**: populated only when a candidate's first
//     authenticated `/clan/advertise` succeeds. The body's HMAC-signed
//     `member_id` is the source of truth for identity.
//
// Two freshness predicates (Phase D timing model — see plan):
//
//   - `AdFresh(now)` = last advertisement < 90 s (3× ad interval). Phase D
//     uses this for diagnostics and the worker-routing freshness gate.
//   - `Live(now)`    = `LastSeenAt` < 4 s  (2× election heartbeat). Phase E
//     will plumb heartbeat receipts into `LastSeenAt`; until then,
//     `LastSeenAt` is updated only by /clan/advertise so Live() rarely
//     returns true (expected pre-Phase E).
//
// Stale entries are NOT removed (per qwen3.6 1D) — they remain visible in
// /clan/peers diagnostics so operators can see "we know about X but haven't
// heard from them recently".
package peers

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/minti/cland/internal/state"
)

// Limits per the Phase D plan + gemma4:31b peer-review.
const (
	DefaultMaxEntries  = 100
	AdFreshWindow      = 90 * time.Second
	LiveWindow         = 4 * time.Second
	PeerAddRateLimit   = 10
	PeerAddRateWindow  = 60 * time.Second
	TCPDialTimeout     = 3 * time.Second
)

// DiscoveredVia records how an address came to our attention.
type DiscoveredVia string

const (
	SourceMDNS   DiscoveredVia = "mdns"
	SourceManual DiscoveredVia = "manual"
	SourceJoin   DiscoveredVia = "join"
)

// Errors callers may check with errors.Is.
var (
	ErrRevoked      = errors.New("peers: member is revoked")
	ErrRegistryFull = errors.New("peers: registry full")
	ErrRateLimited  = errors.New("peers: peer-add rate limit exceeded")
)

// Candidate is an address-only entry — known to be reachable somewhere on
// the network, identity unconfirmed.
type Candidate struct {
	Address       string        `json:"address"`
	DiscoveredVia DiscoveredVia `json:"discovered_via"`
	FirstSeen     time.Time     `json:"first_seen"`
}

// Member is the member-keyed entry, populated only after a candidate's first
// authenticated `/clan/advertise` succeeds. Identity (`MemberID`) is the
// HMAC-signed value from that advertisement's body.
type Member struct {
	MemberID      string         `json:"member_id"`
	Address       string         `json:"address"`
	DiscoveredVia DiscoveredVia  `json:"discovered_via"`
	LastAd        time.Time      `json:"last_ad"`
	LastSeenAt    time.Time      `json:"last_seen_at"`
	LatestAd      *Advertisement `json:"latest_ad,omitempty"`
	AdGeneration  uint64         `json:"ad_generation"`
}

// Advertisement is the parsed §4.2 payload. Loose typing on `Hardware` /
// `Capabilities` so this package doesn't need to know every field cland's
// scores package will eventually consume.
type Advertisement struct {
	MemberID           string         `json:"member_id"`
	ClanID             string         `json:"clan_id"`
	Term               uint64         `json:"term"`
	Generation         uint64         `json:"generation"`
	OS                 string         `json:"os"`
	Hardware           map[string]any `json:"hardware"`
	ReasoningScore     int            `json:"reasoning_score"`
	SystemScore        int            `json:"system_score"`
	Capabilities       map[string]any `json:"capabilities"`
	Load               float64        `json:"load"`
	PinnedOrchestrator bool           `json:"pinned_orchestrator"`
}

// AdFresh reports whether the last advertisement is within the Phase D
// freshness window (90 s, 3× ad interval).
func (m *Member) AdFresh(now time.Time) bool {
	return !m.LastAd.IsZero() && now.Sub(m.LastAd) < AdFreshWindow
}

// Live reports whether LastSeenAt is within the Phase E heartbeat window
// (4 s, 2× HEARTBEAT_INTERVAL). Until Phase E plumbs heartbeat updates,
// this will rarely be true.
func (m *Member) Live(now time.Time) bool {
	return !m.LastSeenAt.IsZero() && now.Sub(m.LastSeenAt) < LiveWindow
}

// rateBucket tracks per-origin peer-add timestamps for the rolling-window
// rate limit.
type rateBucket struct {
	timestamps []time.Time
}

// Registry is goroutine-safe.
type Registry struct {
	mu            sync.Mutex
	candidates    map[string]*Candidate     // key = address
	members       map[string]*Member        // key = member_id
	revocations   map[string]bool           // key = member_id (snapshot)
	perOriginRate map[string]*rateBucket    // key = origin member_id (peer-add)
	maxEntries    int

	// dialFunc is pluggable for tests — defaults to net.DialTimeout (TCP).
	dialFunc func(network, address string, timeout time.Duration) error
}

// NewRegistry returns a fresh registry with default limits.
func NewRegistry() *Registry {
	return &Registry{
		candidates:    make(map[string]*Candidate),
		members:       make(map[string]*Member),
		revocations:   make(map[string]bool),
		perOriginRate: make(map[string]*rateBucket),
		maxEntries:    DefaultMaxEntries,
		dialFunc:      defaultDial,
	}
}

func defaultDial(network, address string, timeout time.Duration) error {
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// SetMaxEntries overrides DefaultMaxEntries — used by tests only.
func (r *Registry) SetMaxEntries(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxEntries = n
}

// SetDialFunc replaces the TCP pre-dial probe (test seam).
func (r *Registry) SetDialFunc(f func(network, address string, timeout time.Duration) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dialFunc = f
}

// SetRevocations seeds the in-memory revocation snapshot from disk. Call at
// startup and whenever revocations.json changes (Phase H will plumb gossip).
func (r *Registry) SetRevocations(rev *state.Revocations) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revocations = make(map[string]bool, len(rev.Entries))
	for _, e := range rev.Entries {
		r.revocations[e.MemberID] = true
	}
}

// UpsertCandidate adds an address-only entry. Idempotent: subsequent calls
// for the same address preserve the original DiscoveredVia + FirstSeen.
// Caller from discovery package; mDNS-derived `member_id` MUST NOT be passed
// here (per qwen3.6 1A — strip the TXT member_id at the discovery layer).
func (r *Registry) UpsertCandidate(address string, via DiscoveredVia) error {
	if address == "" {
		return errors.New("peers: address required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.candidates[address]; exists {
		return nil
	}
	if r.totalLocked() >= r.maxEntries {
		return fmt.Errorf("%w (cap=%d)", ErrRegistryFull, r.maxEntries)
	}
	r.candidates[address] = &Candidate{
		Address:       address,
		DiscoveredVia: via,
		FirstSeen:     time.Now().UTC(),
	}
	return nil
}

// AddPeer is the peer-add path: enforces per-origin rate limit, does a TCP
// pre-dial (guard against malicious member flooding the registry with dead
// IPs — gemma4:31b peer-review), then UpsertCandidate. Returns the matching
// sentinel error for each failure mode so handlers can pick the right HTTP
// status.
func (r *Registry) AddPeer(originMemberID, address string) error {
	if originMemberID == "" {
		return errors.New("peers: origin member_id required")
	}
	if address == "" {
		return errors.New("peers: address required")
	}

	r.mu.Lock()
	if !r.checkRateLimitLocked(originMemberID) {
		r.mu.Unlock()
		return fmt.Errorf("%w: max %d adds per %v", ErrRateLimited, PeerAddRateLimit, PeerAddRateWindow)
	}
	if r.totalLocked() >= r.maxEntries {
		r.mu.Unlock()
		return fmt.Errorf("%w (cap=%d)", ErrRegistryFull, r.maxEntries)
	}
	dialer := r.dialFunc
	r.mu.Unlock()

	// Pre-dial outside the lock — slow operation, must not block other peers.
	if err := dialer("tcp", address, TCPDialTimeout); err != nil {
		return fmt.Errorf("peers: pre-dial %s: %w", address, err)
	}
	return r.UpsertCandidate(address, SourceManual)
}

func (r *Registry) checkRateLimitLocked(originID string) bool {
	bucket, ok := r.perOriginRate[originID]
	if !ok {
		bucket = &rateBucket{}
		r.perOriginRate[originID] = bucket
	}
	now := time.Now()
	cutoff := now.Add(-PeerAddRateWindow)
	kept := bucket.timestamps[:0]
	for _, t := range bucket.timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	bucket.timestamps = kept
	if len(kept) >= PeerAddRateLimit {
		return false
	}
	bucket.timestamps = append(bucket.timestamps, now)
	return true
}

// BindMember is called after the transport layer verifies a /clan/advertise
// payload's HMAC. Promotes / updates the member-keyed entry. Returns
// ErrRevoked if the asserted member_id is on the revocation list.
//
// The `remoteAddr` (HTTP request's RemoteAddr) is used as the binding
// address; if the same member arrives from a new IP, the address is updated.
// Server-side dedup on `generation` per spec §4.2 v0.3.
func (r *Registry) BindMember(ad *Advertisement, remoteAddr string) error {
	if ad == nil || ad.MemberID == "" {
		return errors.New("peers: advertisement requires member_id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.revocations[ad.MemberID] {
		return ErrRevoked
	}

	now := time.Now().UTC()
	existing := r.members[ad.MemberID]
	if existing != nil {
		// Dedup older generation (sender may resend on bump).
		if ad.Generation > 0 && ad.Generation < existing.AdGeneration {
			existing.LastSeenAt = now
			return nil
		}
		existing.LastAd = now
		existing.LastSeenAt = now
		existing.LatestAd = ad
		existing.AdGeneration = ad.Generation
		if remoteAddr != "" {
			existing.Address = remoteAddr
		}
		return nil
	}

	if r.totalLocked() >= r.maxEntries {
		return fmt.Errorf("%w (cap=%d)", ErrRegistryFull, r.maxEntries)
	}

	via := SourceMDNS // discovery callback path is the common case
	if cand := r.candidates[remoteAddr]; cand != nil {
		via = cand.DiscoveredVia
	}
	r.members[ad.MemberID] = &Member{
		MemberID:      ad.MemberID,
		Address:       remoteAddr,
		DiscoveredVia: via,
		LastAd:        now,
		LastSeenAt:    now,
		LatestAd:      ad,
		AdGeneration:  ad.Generation,
	}
	return nil
}

// TouchLive bumps LastSeenAt for a member — used by Phase E to record
// heartbeat receipts (calling site lands when Phase E ships).
func (r *Registry) TouchLive(memberID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m := r.members[memberID]; m != nil {
		m.LastSeenAt = time.Now().UTC()
	}
}

// Snapshot returns immutable copies of all candidates + members. The advertise
// loop iterates this every tick; /clan/peers handler dumps it for diagnostics.
func (r *Registry) Snapshot() ([]Candidate, []Member) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cs := make([]Candidate, 0, len(r.candidates))
	for _, c := range r.candidates {
		cs = append(cs, *c)
	}
	ms := make([]Member, 0, len(r.members))
	for _, m := range r.members {
		cp := *m
		if m.LatestAd != nil {
			adCp := *m.LatestAd
			cp.LatestAd = &adCp
		}
		ms = append(ms, cp)
	}
	return cs, ms
}

// totalLocked counts unique addresses + member-keyed entries against the cap.
// A candidate plus a member binding for the same address counts as 2 — the
// cap is conservative on purpose; legitimate Clans won't approach 100.
func (r *Registry) totalLocked() int {
	return len(r.candidates) + len(r.members)
}
