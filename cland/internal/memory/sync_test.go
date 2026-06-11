package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFetcher serves a canned response (or error) and counts calls.
type fakeFetcher struct {
	calls  atomic.Int64
	status int
	body   []byte
	err    error
	delay  time.Duration
}

func (f *fakeFetcher) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(f.body)),
	}, nil
}

func newSyncerForTest(t *testing.T, svc *Service, f Fetcher, addr string) *Syncer {
	t.Helper()
	s, err := NewSyncer(SyncerOpts{
		Service: svc,
		Fetcher: f,
		LookupAddr: func(string) string { return addr },
		FetchTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMaybeSyncEmptyDigestSkips(t *testing.T) {
	svc, _ := newTestService(t)
	f := &fakeFetcher{}
	s := newSyncerForTest(t, svc, f, "127.0.0.1:1")
	if s.MaybeSync(context.Background(), "peer", "") {
		t.Fatal("empty digest must not trigger a fetch (pre-§13 compatibility)")
	}
	if f.calls.Load() != 0 {
		t.Fatal("fetcher must not be called")
	}
}

func TestMaybeSyncMatchingDigestNoOp(t *testing.T) {
	svc, _ := newTestService(t)
	f := &fakeFetcher{}
	s := newSyncerForTest(t, svc, f, "127.0.0.1:1")
	if s.MaybeSync(context.Background(), "peer", svc.Digest()) {
		t.Fatal("matching digest must be a no-op")
	}
	if f.calls.Load() != 0 {
		t.Fatal("fetcher must not be called on match")
	}
}

func TestMaybeSyncMismatchFetchesAndMerges(t *testing.T) {
	svc, _ := newTestService(t)
	remote := graph([]Node{node("00000000-0000-4000-8000-0000000000aa", 1, "2026-06-11T10:00:00Z")}, nil)
	body, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFetcher{body: body}
	s := newSyncerForTest(t, svc, f, "127.0.0.1:1")

	if !s.MaybeSync(context.Background(), "peer", Digest(remote)) {
		t.Fatal("mismatch must trigger a fetch")
	}
	if f.calls.Load() != 1 {
		t.Fatalf("expected 1 fetch, got %d", f.calls.Load())
	}
	if svc.Digest() != Digest(remote) {
		t.Fatal("local graph must converge to the fetched one")
	}

	// Idempotent on repeat: digests now match → no fetch.
	if s.MaybeSync(context.Background(), "peer", Digest(remote)) {
		t.Fatal("post-merge repeat must be a no-op")
	}
	if f.calls.Load() != 1 {
		t.Fatal("no extra fetch after convergence")
	}
}

func TestMaybeSyncFetchErrorPreservesLocal(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.AddOrUpdateNode("o", Node{Type: "fact", Title: "keep me"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before := svc.Digest()

	f := &fakeFetcher{err: errors.New("connection refused")}
	s := newSyncerForTest(t, svc, f, "127.0.0.1:1")
	if !s.MaybeSync(context.Background(), "peer", "different-digest") {
		t.Fatal("mismatch must attempt the fetch")
	}
	if svc.Digest() != before {
		t.Fatal("fetch error must preserve local state untouched")
	}
}

func TestMaybeSyncUnknownAddrSkips(t *testing.T) {
	svc, _ := newTestService(t)
	f := &fakeFetcher{}
	s, err := NewSyncer(SyncerOpts{
		Service:    svc,
		Fetcher:    f,
		LookupAddr: func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.MaybeSync(context.Background(), "peer", "different") {
		t.Fatal("unknown addr must defer, not fetch")
	}
	if f.calls.Load() != 0 {
		t.Fatal("fetcher must not be called")
	}
}

func TestMaybeSyncOversizedResponseDropped(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.AddOrUpdateNode("o", Node{Type: "fact", Title: "local"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before := svc.Digest()

	huge := `{"format_version":1,"nodes":[],"edges":[],"pad":"` +
		strings.Repeat("x", MaxFetchBytes) + `"}`
	f := &fakeFetcher{body: []byte(huge)}
	s := newSyncerForTest(t, svc, f, "127.0.0.1:1")
	if !s.MaybeSync(context.Background(), "peer", "different") {
		t.Fatal("mismatch must attempt the fetch")
	}
	if svc.Digest() != before {
		t.Fatal("over-guard response must be dropped, local preserved (spec §13.5)")
	}
}

func TestMaybeSyncInflightDedup(t *testing.T) {
	svc, _ := newTestService(t)
	remote := graph([]Node{node("00000000-0000-4000-8000-0000000000ab", 1, "2026-06-11T10:00:00Z")}, nil)
	body, _ := json.Marshal(remote)
	f := &fakeFetcher{body: body, delay: 150 * time.Millisecond}
	s := newSyncerForTest(t, svc, f, "127.0.0.1:1")

	done := make(chan bool, 2)
	go func() { done <- s.MaybeSync(context.Background(), "peer", Digest(remote)) }()
	time.Sleep(30 * time.Millisecond) // first fetch in flight
	go func() { done <- s.MaybeSync(context.Background(), "peer", Digest(remote)) }()
	r1, r2 := <-done, <-done
	if r1 == r2 {
		t.Fatalf("exactly one call should fetch (got %v, %v)", r1, r2)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("in-flight dedup must hold fetches to 1, got %d", f.calls.Load())
	}
}
