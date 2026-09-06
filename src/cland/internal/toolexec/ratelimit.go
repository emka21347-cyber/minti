package toolexec

import (
	"sync"
	"time"
)

// RateLimiter is a per-origin-member token bucket for /mcp/execute. Mitigates
// qwen's insider-threat finding (project peer-review 2026-05-28): a malicious
// or compromised member can forge tokens claiming any origin given the shared
// clan_key trust model. We can't prevent forgery in v1, but we can bound the
// blast radius by rate-limiting how many tool calls each origin can dispatch
// per window.
//
// Defaults: 10 requests per 60s per origin. Configurable via opts.
//
// Pragmatically per-origin (not per-target): the cost we want to bound is
// the call rate from a single (claimed) source, since that's what an
// insider-spoofing-someone-else would exhibit.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	maxBurst int
	window   time.Duration
	now      func() time.Time
}

type bucket struct {
	timestamps []time.Time
}

// NewRateLimiter returns a limiter with the given max-requests-per-window.
// Defaults: 10 / 60s.
func NewRateLimiter(maxBurst int, window time.Duration) *RateLimiter {
	if maxBurst <= 0 {
		maxBurst = 10
	}
	if window <= 0 {
		window = 60 * time.Second
	}
	return &RateLimiter{
		buckets:  make(map[string]*bucket),
		maxBurst: maxBurst,
		window:   window,
		now:      time.Now,
	}
}

// Allow returns true iff `originID` is under the rate limit. On true the
// request is recorded against the bucket (consumes one token).
func (l *RateLimiter) Allow(originID string) bool {
	if originID == "" {
		return true // bypass for missing origin; the caller should still 401
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	b, ok := l.buckets[originID]
	if !ok {
		b = &bucket{}
		l.buckets[originID] = b
	}
	// Drop timestamps older than the window.
	kept := b.timestamps[:0]
	for _, t := range b.timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.timestamps = kept
	if len(b.timestamps) >= l.maxBurst {
		return false
	}
	b.timestamps = append(b.timestamps, now)
	return true
}

// CountInWindow returns the current request count for an origin (for tests
// + diagnostics).
func (l *RateLimiter) CountInWindow(originID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[originID]
	if !ok {
		return 0
	}
	now := l.now()
	cutoff := now.Add(-l.window)
	n := 0
	for _, t := range b.timestamps {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}
