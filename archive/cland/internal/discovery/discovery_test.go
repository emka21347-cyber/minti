package discovery

import (
	"sync"
	"testing"
	"time"
)

func TestParseTXT(t *testing.T) {
	clan, member := parseTXT([]string{"clan_id=ABC", "member_id=XYZ", "proto=1"})
	if clan != "ABC" || member != "XYZ" {
		t.Errorf("clan=%q member=%q", clan, member)
	}
	if c, m := parseTXT([]string{"random=garbage"}); c != "" || m != "" {
		t.Errorf("unknown keys should yield empty")
	}
}

func TestShouldEmit_DebounceWindow(t *testing.T) {
	s := &Service{}
	addr := "10.0.0.1:7777"
	// First emit allowed.
	if !s.shouldEmit(addr) {
		t.Errorf("first emit for new address should be true")
	}
	// Second within window suppressed.
	if s.shouldEmit(addr) {
		t.Errorf("second emit within %v should be suppressed", debounce)
	}
	// Different address not affected.
	if !s.shouldEmit("10.0.0.2:7777") {
		t.Errorf("different address should pass")
	}
}

func TestShouldEmit_ReopenAfterWindow(t *testing.T) {
	s := &Service{}
	addr := "10.0.0.1:7777"
	if !s.shouldEmit(addr) {
		t.Fatal("first emit blocked")
	}
	// Backdate so the window has elapsed.
	s.dmu.Lock()
	s.lastEmit[addr] = time.Now().Add(-2 * debounce)
	s.dmu.Unlock()
	if !s.shouldEmit(addr) {
		t.Errorf("post-window emit should pass")
	}
}

func TestShouldEmit_ConcurrentSafe(t *testing.T) {
	s := &Service{}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.shouldEmit("10.0.0.1:7777")
		}(i)
	}
	wg.Wait()
	// At most one emit in the window across 64 callers.
	s.dmu.Lock()
	defer s.dmu.Unlock()
	if len(s.lastEmit) != 1 {
		t.Errorf("expected exactly 1 emitted address, got %d", len(s.lastEmit))
	}
}

func TestRegister_RejectsEmptyArgs(t *testing.T) {
	cases := []*Service{
		{MemberID: "m", Port: 7777},
		{ClanID: "c", Port: 7777},
		{ClanID: "c", MemberID: "m"},
	}
	for i, s := range cases {
		if err := s.Register(); err == nil {
			t.Errorf("case %d: expected error for incomplete Service", i)
		}
	}
}
