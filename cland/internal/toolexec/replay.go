package toolexec

import (
	"sync"
	"time"
)

// ReplayCache tracks recently-seen request_ids to block token replay.
//
// Bounded by max-size (LRU-by-insert-time eviction when full) and per-entry
// TTL (matches token max-lifetime so we don't need to track exp per entry —
// any entry older than TTL can't possibly accept a re-presented valid token).
//
// In-memory only. A daemon restart loses the cache; an attacker with a
// captured-but-still-valid token could replay across the restart window.
// Mitigated by DefaultMaxTokenLifetime (10 min) bounding the window.
// Persisting the cache (Phase H or beyond) would close that gap but adds
// disk I/O on the hot path; deferred.
type ReplayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // request_id → expiry
	maxSize int
	ttl     time.Duration
}

// NewReplayCache returns a ReplayCache. Pass zero/negative for defaults
// (max 10_000 entries, TTL 15 min).
func NewReplayCache(maxSize int, ttl time.Duration) *ReplayCache {
	if maxSize <= 0 {
		maxSize = 10_000
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &ReplayCache{
		entries: make(map[string]time.Time, 64),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// CheckAndStore returns true iff `requestID` was NOT previously seen. On
// first sight it records the request and returns true. On replay it returns
// false. Expired entries are GC'd opportunistically on each call.
func (c *ReplayCache) CheckAndStore(requestID string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Drop expired before anything else (cheap; bounded by maxSize).
	if exp, ok := c.entries[requestID]; ok {
		if now.Before(exp) {
			return false // replay
		}
		// Expired — overwrite below.
	}

	// Periodic GC: when full, sweep expired entries first.
	if len(c.entries) >= c.maxSize {
		for k, exp := range c.entries {
			if !now.Before(exp) {
				delete(c.entries, k)
			}
		}
		// If still full after sweep, evict an arbitrary entry (range iteration
		// is randomised in Go, so this is "random eviction" — not strictly LRU
		// but bounded-memory which is what matters).
		if len(c.entries) >= c.maxSize {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}

	c.entries[requestID] = now.Add(c.ttl)
	return true
}

// Size returns the current entry count — for tests + diagnostics.
func (c *ReplayCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
