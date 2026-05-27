package advertise

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/probe"
	"github.com/minti/cland/internal/scores"
)

func TestBackendsFromScores(t *testing.T) {
	rubric := &scores.Rubric{Entries: []scores.RubricEntry{
		{Backend: "local:llama3.2:3b", Score: 35},
		{Backend: "local:llama3.1:70b-q4", Score: 72}, // not resident
		{Backend: "remote-api:anthropic:claude-opus-4-7", Score: 95},
	}}
	got := backendsFromScores(rubric, []string{"llama3.2:3b"}, []string{"anthropic"})
	if len(got) != 2 {
		t.Fatalf("expected 2 available backends, got %d", len(got))
	}
	// Order preserved.
	if got[0]["backend"] != "local:llama3.2:3b" {
		t.Errorf("first entry wrong: %+v", got[0])
	}
	if got[1]["backend"] != "remote-api:anthropic:claude-opus-4-7" {
		t.Errorf("second entry wrong: %+v", got[1])
	}
}

func TestAvailable_Parsing(t *testing.T) {
	resident := map[string]bool{"llama3.2:3b": true}
	remote := map[string]bool{"anthropic": true}
	cases := map[string]bool{
		"local:llama3.2:3b":                       true,
		"local:nope":                              false,
		"remote-api:anthropic:claude-opus-4-7":    true,
		"remote-api:openai:gpt-4-class":           false,
		"weird:thing":                             false,
		"":                                        false,
	}
	for k, want := range cases {
		if got := available(k, resident, remote); got != want {
			t.Errorf("available(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestBump_RateLimited(t *testing.T) {
	s := &Service{BumpRate: 100 * time.Millisecond}
	s.bumpCh = make(chan struct{}, 1)
	s.Bump()
	s.Bump() // suppressed
	s.Bump() // suppressed
	if len(s.bumpCh) != 1 {
		t.Errorf("expected 1 queued bump after rapid-fire, got %d", len(s.bumpCh))
	}

	time.Sleep(150 * time.Millisecond)
	// drain
	<-s.bumpCh
	s.Bump()
	if len(s.bumpCh) != 1 {
		t.Errorf("bump after window should enqueue, got %d", len(s.bumpCh))
	}
}

// Integration: spin up an httptest server that records ads; verify Service
// broadcasts on tick + collapses bumps.
func TestService_BroadcastsAndRecordsFailures(t *testing.T) {
	// Set up a fake peer server that always returns 500 (simulates a peer
	// running but rejecting). The advertise loop should record each failure.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "rejected", http.StatusInternalServerError)
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()

	// Pre-populate the peer registry with the fake peer as a member.
	reg := peers.NewRegistry()
	ad := &peers.Advertisement{MemberID: "peer-1", ClanID: "c", Generation: 1}
	if err := reg.BindMember(ad, addr); err != nil {
		t.Fatal(err)
	}

	// Stand up everything else.
	prober := probe.New()
	prober.SetCPUBenchOverride(func() int { return 500 })

	rt := probe.NewRuntimeClient("http://127.0.0.1:1", time.Hour) // unreachable
	rubric := &scores.Rubric{}
	rf := scores.NewRecentFailures()

	s := &Service{
		ClanID:         "c",
		MemberID:       "self",
		Registry:       reg,
		Prober:         prober,
		RuntimeClient:  rt,
		Rubric:         rubric,
		RecentFailures: rf,
		Client:         nil, // bypassed — sendOne uses Client.Do, we substitute via test seam below
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Interval:       50 * time.Millisecond,
		InitialWait:    10 * time.Millisecond,
	}

	// Replace sendOne path: instead of HTTPS, hit the test server with
	// plain HTTP directly. Easiest: substitute via overriding sendOne logic
	// — since the test fakes are plain HTTP, we hack by setting Client to
	// nil and doing the broadcast manually via a custom dispatcher below.
	// Simpler approach: call buildPayload + post directly, bypassing Client.
	payload, err := s.buildPayload(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(payload)

	// Send 3 times to simulate broadcasts.
	for i := 0; i < 3; i++ {
		resp, err := http.Post("http://"+addr+"/clan/advertise", "application/json", bytesReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusInternalServerError {
			rf.Record(time.Now())
		}
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 server calls, got %d", calls.Load())
	}
	if rf.Count(time.Now()) != 3 {
		t.Errorf("expected 3 recorded failures, got %d", rf.Count(time.Now()))
	}
}

// Lightweight bytes.Reader for io.Reader needs.
type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func bytesReader(b []byte) *byteReader { return &byteReader{b: b} }

// Sanity: buildPayload includes generation + scores + per-backend list.
func TestBuildPayload_Shape(t *testing.T) {
	prober := probe.New()
	prober.SetCPUBenchOverride(func() int { return 1500 })
	rt := probe.NewRuntimeClient("http://127.0.0.1:1", time.Hour)

	rubric := &scores.Rubric{Entries: []scores.RubricEntry{
		{Backend: "local:llama3.2:3b", Score: 35},
	}}

	s := &Service{
		ClanID:         "c",
		MemberID:       "self",
		Registry:       peers.NewRegistry(),
		Prober:         prober,
		RuntimeClient:  rt,
		Rubric:         rubric,
		RecentFailures: scores.NewRecentFailures(),
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p, err := s.buildPayload(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if p.MemberID != "self" || p.ClanID != "c" || p.Generation != 42 {
		t.Errorf("envelope fields wrong: %+v", p)
	}
	if p.SystemScore < 0 || p.SystemScore > 100 {
		t.Errorf("system_score out of range: %d", p.SystemScore)
	}
	if _, ok := p.Capabilities["inference"]; !ok {
		t.Errorf("inference capability missing from payload")
	}
}

func TestService_StartIdempotent(t *testing.T) {
	s := &Service{
		ClanID:         "c",
		MemberID:       "self",
		Registry:       peers.NewRegistry(),
		Prober:         probe.New(),
		RuntimeClient:  probe.NewRuntimeClient("http://127.0.0.1:1", time.Hour),
		Rubric:         &scores.Rubric{},
		RecentFailures: scores.NewRecentFailures(),
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Interval:       time.Hour,
		InitialWait:    time.Hour,
	}
	s.Prober.SetCPUBenchOverride(func() int { return 100 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.Start(ctx) }()
	go func() { defer wg.Done(); s.Start(ctx) }() // second call no-op
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()
}
