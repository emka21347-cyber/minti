// Package transport hosts cland's HTTPS server + client. It enforces the
// HMAC-over-canonical-request auth (per docs/clan-protocol.md §2.3),
// pins the Clan certificate on outbound connections, and protects against
// replays via a bounded nonce cache.
package transport

import (
	"container/list"
	"sync"
	"time"
)

// NonceCache rejects repeated (memberID, nonce) pairs within a TTL window.
// Memory is bounded per member via LRU eviction (D-M4.5), closing the
// "flood with unique nonces to OOM the daemon" vector the peer-review pass
// flagged.
type NonceCache struct {
	mu           sync.Mutex
	members      map[string]*memberBucket
	capPerMember int
	ttl          time.Duration
}

type memberBucket struct {
	// entries: nonce -> *list.Element holding a nonceEntry
	entries map[string]*list.Element
	// lru: oldest at front, newest at back
	lru *list.List
}

type nonceEntry struct {
	nonce string
	added time.Time
}

// NewNonceCache returns a cache with `capPerMember` entries kept per member
// (LRU-evicted past the cap), each entry alive for `ttl`.
func NewNonceCache(capPerMember int, ttl time.Duration) *NonceCache {
	if capPerMember <= 0 {
		capPerMember = 10_000
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &NonceCache{
		members:      make(map[string]*memberBucket),
		capPerMember: capPerMember,
		ttl:          ttl,
	}
}

// CheckAndStore returns true iff the (memberID, nonce) pair has NOT been seen
// in the last TTL. A return of false signals a replay (or a duplicate within
// the cap window).
func (c *NonceCache) CheckAndStore(memberID, nonce string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	bucket := c.members[memberID]
	if bucket == nil {
		bucket = &memberBucket{
			entries: make(map[string]*list.Element),
			lru:     list.New(),
		}
		c.members[memberID] = bucket
	}

	// Lazy TTL sweep: drop expired entries from the front of the LRU.
	for e := bucket.lru.Front(); e != nil; {
		ne := e.Value.(*nonceEntry)
		if now.Sub(ne.added) <= c.ttl {
			break
		}
		next := e.Next()
		delete(bucket.entries, ne.nonce)
		bucket.lru.Remove(e)
		e = next
	}

	if _, exists := bucket.entries[nonce]; exists {
		return false
	}

	e := bucket.lru.PushBack(&nonceEntry{nonce: nonce, added: now})
	bucket.entries[nonce] = e

	// LRU evict past the per-member cap.
	for bucket.lru.Len() > c.capPerMember {
		front := bucket.lru.Front()
		ne := front.Value.(*nonceEntry)
		delete(bucket.entries, ne.nonce)
		bucket.lru.Remove(front)
	}

	return true
}

// Size reports the total number of cached nonces across all members. Test +
// observability use.
func (c *NonceCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int
	for _, b := range c.members {
		n += b.lru.Len()
	}
	return n
}

// MemberSize reports the cached count for one member.
func (c *NonceCache) MemberSize(memberID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.members[memberID]
	if b == nil {
		return 0
	}
	return b.lru.Len()
}
