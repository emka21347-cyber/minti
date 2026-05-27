package transport

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNonceCache_AcceptsFirstThenRejectsReplay(t *testing.T) {
	c := NewNonceCache(100, time.Minute)
	if !c.CheckAndStore("m1", "n1") {
		t.Errorf("first should be accepted")
	}
	if c.CheckAndStore("m1", "n1") {
		t.Errorf("replay should be rejected")
	}
	// Same nonce, different member is fine — independent keyspaces.
	if !c.CheckAndStore("m2", "n1") {
		t.Errorf("same nonce different member should be accepted")
	}
}

func TestNonceCache_LRUEvictPastCap(t *testing.T) {
	c := NewNonceCache(3, time.Minute)
	for i := 0; i < 5; i++ {
		if !c.CheckAndStore("m", fmt.Sprintf("n%d", i)) {
			t.Errorf("unique nonce %d should be accepted", i)
		}
	}
	if got := c.MemberSize("m"); got != 3 {
		t.Errorf("expected per-member size 3 after eviction, got %d", got)
	}
	// The oldest (n0, n1) were evicted; n2-n4 still cached so they replay.
	if c.CheckAndStore("m", "n4") {
		t.Errorf("n4 should be cached and rejected as replay")
	}
	// n0 was evicted, so re-submitting it now is treated as a fresh nonce.
	if !c.CheckAndStore("m", "n0") {
		t.Errorf("evicted nonce should be accepted again (no longer cached)")
	}
}

func TestNonceCache_TTLExpiry(t *testing.T) {
	c := NewNonceCache(100, 20*time.Millisecond)
	if !c.CheckAndStore("m", "n1") {
		t.Fatal("first accept")
	}
	if c.CheckAndStore("m", "n1") {
		t.Fatal("immediate replay")
	}
	time.Sleep(40 * time.Millisecond)
	// After TTL the lazy sweep on next CheckAndStore should drop n1.
	if !c.CheckAndStore("m", "n1") {
		t.Errorf("after TTL the nonce should be acceptable again")
	}
}

func TestNonceCache_DefaultsApplied(t *testing.T) {
	c := NewNonceCache(0, 0) // → defaults: 10_000 cap, 5-min TTL
	if !c.CheckAndStore("m", "n") {
		t.Fatal("first accept under defaults")
	}
	if c.CheckAndStore("m", "n") {
		t.Errorf("replay under defaults")
	}
}

func TestNonceCache_ConcurrentSafety(t *testing.T) {
	c := NewNonceCache(100_000, time.Minute)
	var wg sync.WaitGroup
	const writers = 8
	const perWriter = 500
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_ = c.CheckAndStore(fmt.Sprintf("m%d", id), fmt.Sprintf("n%d", i))
			}
		}(w)
	}
	wg.Wait()

	for w := 0; w < writers; w++ {
		want := perWriter
		got := c.MemberSize(fmt.Sprintf("m%d", w))
		if got != want {
			t.Errorf("member m%d: got %d, want %d", w, got, want)
		}
	}
}

func TestNonceCache_Size(t *testing.T) {
	c := NewNonceCache(100, time.Minute)
	c.CheckAndStore("a", "1")
	c.CheckAndStore("a", "2")
	c.CheckAndStore("b", "1")
	if c.Size() != 3 {
		t.Errorf("total size = %d, want 3", c.Size())
	}
}
