package toolexec

import (
	"sync"
	"time"
)

// ReplayCache tracks recently-seen request_ids to block token replay.
//
// Phase H-3 hardening (project peer-review qwen finding 2026-05-28):
// **strict TTL-only eviction**. The original implementation evicted an
// arbitrary entry (random Go map iteration) when full + no expired entries
// were available — qwen's correct flag: an attacker could provoke this
// eviction by sending many distinct valid request_ids in the TTL window,
// then replay one of the evicted IDs. Fix: when full + no expirable
// entries → REJECT the new request (return false). The caller treats this
// as a security-relevant overflow rather than silently making room.
//
// Cap is generous (100K entries default) so reaching it in normal traffic
// is implausible. Coupled with the per-origin RateLimiter (10 req/60s
// default), one origin can land at most ~100 entries per token lifetime
// (10 min default), so an unprivileged DoS on the cache would need ~1000
// distinct origins.
//
// In-memory only. A daemon restart loses the cache; an attacker with a
// captured-but-still-valid token could replay across the restart window.
// Mitigated by DefaultMaxTokenLifetime (10 min) bounding the window.
// Persisting the cache would close the gap but adds disk I/O; deferred.
type ReplayCache struct {
	mu       sync.Mutex
	entries  map[string]time.Time // request_id → expiry
	maxSize  int
	ttl      time.Duration
	overflow int // count of full-cap rejections (read for metrics/audit)
}

// NewReplayCache returns a ReplayCache. Pass zero/negative for defaults
// (max 100_000 entries, TTL 15 min).
func NewReplayCache(maxSize int, ttl time.Duration) *ReplayCache {
	if maxSize <= 0 {
		maxSize = 100_000
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

// CheckAndStore returns true iff `requestID` is being recorded fresh (i.e.,
// caller should ACCEPT the request). Returns false in two cases:
//   1. Replay — same request_id seen within TTL.
//   2. Cache overflow — at max-size + no expired entries to evict. The
//      caller MUST treat this as suspicious + reject the request.
// Both cases the audit-log entry should reflect the distinction (handler
// receives ErrReplay; overflow gets its own metric via OverflowCount).
func (c *ReplayCache) CheckAndStore(requestID string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Replay check.
	if exp, ok := c.entries[requestID]; ok {
		if now.Before(exp) {
			return false // genuine replay
		}
		// Expired — fall through to overwrite below.
	}

	// Make room if needed: sweep expired entries first.
	if len(c.entries) >= c.maxSize {
		for k, exp := range c.entries {
			if !now.Before(exp) {
				delete(c.entries, k)
			}
		}
		// If still full after sweep, REJECT — do NOT evict a valid entry.
		// qwen project-review fix: arbitrary-eviction allowed replay of an
		// evicted-but-still-valid token. Better to drop the new request +
		// log an overflow than risk replay.
		if len(c.entries) >= c.maxSize {
			c.overflow++
			return false
		}
	}

	c.entries[requestID] = now.Add(c.ttl)
	return true
}

// OverflowCount returns how many CheckAndStore calls were rejected because
// the cache was full of non-expired entries. Non-zero → either legitimate
// traffic genuinely exceeds expected scale (raise the cap), or someone is
// attempting to provoke replay-cache pressure (raise the alarm).
func (c *ReplayCache) OverflowCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.overflow
}

// Size returns the current entry count — for tests + diagnostics.
func (c *ReplayCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
