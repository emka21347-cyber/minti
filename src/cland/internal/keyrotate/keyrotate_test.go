package keyrotate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
)

// ---------- fakes ----------

type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

// fakeRotater records the last Rotate call.
type fakeRotater struct {
	mu     sync.Mutex
	calls  int
	lastKey []byte
	lastGrace time.Duration
	err    error
}

func (f *fakeRotater) Rotate(newKey []byte, grace time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls++
	f.lastKey = append(f.lastKey[:0], newKey...)
	f.lastGrace = grace
	return nil
}

// fakePoster records POST calls and returns programmable statuses keyed by URL path.
type fakePoster struct {
	mu       sync.Mutex
	calls    atomic.Int32
	urls     []string
	bodies   [][]byte
	statusFn func(url string) int // optional; defaults to 200
	errFn    func(url string) error
}

func (f *fakePoster) Post(url, ct string, body []byte) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Add(1)
	f.urls = append(f.urls, url)
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	if f.errFn != nil {
		if err := f.errFn(url); err != nil {
			return nil, err
		}
	}
	status := http.StatusOK
	if f.statusFn != nil {
		status = f.statusFn(url)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func (f *fakePoster) UrlsByPath(suffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, u := range f.urls {
		if strings.HasSuffix(u, suffix) {
			n++
		}
	}
	return n
}

func mkKey(seed byte) []byte {
	k := make([]byte, KeyLen)
	for i := range k {
		k[i] = seed
	}
	return k
}

// ---------- store tests ----------

func TestStore_PutAccepted(t *testing.T) {
	s := NewProposeStore(time.Now)
	ok, err := s.Put(ProposeRequest{
		ProposeID: "p1",
		NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(1)),
	})
	if err != nil || !ok {
		t.Errorf("first put: ok=%v err=%v", ok, err)
	}
	if s.Pending() != "p1" {
		t.Errorf("pending: got %q", s.Pending())
	}
}

func TestStore_PutDifferentIDWhilePending_409(t *testing.T) {
	s := NewProposeStore(time.Now)
	_, _ = s.Put(ProposeRequest{ProposeID: "p1", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(1))})
	ok, err := s.Put(ProposeRequest{ProposeID: "p2", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(2))})
	if ok || !errors.Is(err, ErrAlreadyPending) {
		t.Errorf("expected ErrAlreadyPending; got ok=%v err=%v", ok, err)
	}
	if s.Pending() != "p1" {
		t.Errorf("first propose should still be pending; got %q", s.Pending())
	}
}

func TestStore_PutSameIDIdempotent(t *testing.T) {
	s := NewProposeStore(time.Now)
	body := ProposeRequest{ProposeID: "p1", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(1))}
	_, _ = s.Put(body)
	ok, err := s.Put(body)
	if err != nil || !ok {
		t.Errorf("idempotent re-put: ok=%v err=%v", ok, err)
	}
}

func TestStore_SweepExpired(t *testing.T) {
	now := time.Now()
	fakeNow := now
	s := NewProposeStore(func() time.Time { return fakeNow })
	_, _ = s.Put(ProposeRequest{ProposeID: "p1", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(1))})

	fakeNow = now.Add(MemberRevertAfter / 2)
	if s.SweepExpired() {
		t.Errorf("sweep before revert window should be no-op")
	}

	fakeNow = now.Add(MemberRevertAfter + 1*time.Second)
	if !s.SweepExpired() {
		t.Errorf("sweep after revert window should drop pending")
	}
	if s.Pending() != "" {
		t.Errorf("after sweep, pending should be empty; got %q", s.Pending())
	}
}

func TestStore_TakeMatchesID(t *testing.T) {
	s := NewProposeStore(time.Now)
	_, _ = s.Put(ProposeRequest{ProposeID: "p1", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(7))})
	k, ok := s.Take("p1")
	if !ok || !bytes.Equal(k, mkKey(7)) {
		t.Errorf("take: ok=%v key=%v", ok, k)
	}
	if s.Pending() != "" {
		t.Errorf("take should clear pending")
	}
	// Second take returns false.
	if _, ok := s.Take("p1"); ok {
		t.Errorf("second take should return false")
	}
}

// ---------- member handler tests ----------

func mkMemberHandler(t *testing.T, rotater Rotater) (*MemberHandler, *ProposeStore) {
	t.Helper()
	store := NewProposeStore(time.Now)
	h := &MemberHandler{
		SelfID:  "B",
		Store:   store,
		Rotater: rotater,
		Audit:   noopAudit{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return h, store
}

func TestMember_Propose_HappyPath(t *testing.T) {
	r := &fakeRotater{}
	h, store := mkMemberHandler(t, r)

	body, _ := json.Marshal(ProposeRequest{
		ProposeID: "p1",
		NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(1)),
	})
	req := httptest.NewRequest("POST", "/clan/rotate-key/propose", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handlePropose(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("propose: got %d want 200", w.Code)
	}
	if store.Pending() != "p1" {
		t.Errorf("pending should be p1; got %q", store.Pending())
	}
}

func TestMember_CommitWithoutPropose_409(t *testing.T) {
	h, _ := mkMemberHandler(t, &fakeRotater{})
	body, _ := json.Marshal(CommitRequest{ProposeID: "p-unknown"})
	req := httptest.NewRequest("POST", "/clan/rotate-key/commit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleCommit(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("commit no-pending: got %d want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestMember_CommitAfterPropose_RotatesKey(t *testing.T) {
	r := &fakeRotater{}
	h, _ := mkMemberHandler(t, r)

	// 1. propose
	pBody, _ := json.Marshal(ProposeRequest{ProposeID: "p1", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(9))})
	w1 := httptest.NewRecorder()
	h.handlePropose(w1, httptest.NewRequest("POST", "/", bytes.NewReader(pBody)))
	if w1.Code != http.StatusOK {
		t.Fatalf("propose failed: %d", w1.Code)
	}

	// 2. commit
	cBody, _ := json.Marshal(CommitRequest{ProposeID: "p1", GraceDuration: 30 * time.Second})
	w2 := httptest.NewRecorder()
	h.handleCommit(w2, httptest.NewRequest("POST", "/", bytes.NewReader(cBody)))
	if w2.Code != http.StatusOK {
		t.Fatalf("commit failed: %d body=%s", w2.Code, w2.Body.String())
	}
	if r.calls != 1 {
		t.Errorf("rotater calls: got %d want 1", r.calls)
	}
	if !bytes.Equal(r.lastKey, mkKey(9)) {
		t.Errorf("rotater got wrong key")
	}
	if r.lastGrace != 30*time.Second {
		t.Errorf("grace duration: got %v want 30s", r.lastGrace)
	}
}

func TestMember_AbortClearsPending(t *testing.T) {
	h, store := mkMemberHandler(t, &fakeRotater{})
	pBody, _ := json.Marshal(ProposeRequest{ProposeID: "p1", NewKeyB64: base64.StdEncoding.EncodeToString(mkKey(1))})
	w := httptest.NewRecorder()
	h.handlePropose(w, httptest.NewRequest("POST", "/", bytes.NewReader(pBody)))

	aBody, _ := json.Marshal(AbortRequest{ProposeID: "p1", Reason: "test"})
	w2 := httptest.NewRecorder()
	h.handleAbort(w2, httptest.NewRequest("POST", "/", bytes.NewReader(aBody)))
	if w2.Code != http.StatusOK {
		t.Errorf("abort: got %d want 200", w2.Code)
	}
	if store.Pending() != "" {
		t.Errorf("abort should clear pending; got %q", store.Pending())
	}
}

// ---------- coordinator tests ----------

func mkCoord(t *testing.T, opts CoordinatorOpts) *Coordinator {
	t.Helper()
	if opts.SelfID == "" {
		opts.SelfID = "A"
	}
	if opts.Audit == nil {
		opts.Audit = noopAudit{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c, err := NewCoordinator(opts)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return c
}

func TestCoordinator_HappyPath_AllAck(t *testing.T) {
	rot := &fakeRotater{}
	poster := &fakePoster{} // default 200
	peers := []Peer{{MemberID: "B", Address: "127.0.0.1:17981"}, {MemberID: "C", Address: "127.0.0.1:17982"}}
	c := mkCoord(t, CoordinatorOpts{
		Rotater: rot, Client: poster,
		PeerSource: func() []Peer { return peers },
		ProposeTimeout: 5 * time.Second,
	})
	res, err := c.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !res.Committed {
		t.Errorf("expected Committed=true; got %+v", res)
	}
	if len(res.AckedBy) != 2 {
		t.Errorf("acked: %v", res.AckedBy)
	}
	if got := poster.UrlsByPath("/clan/rotate-key/propose"); got != 2 {
		t.Errorf("propose broadcasts: got %d want 2", got)
	}
	if got := poster.UrlsByPath("/clan/rotate-key/commit"); got != 2 {
		t.Errorf("commit broadcasts: got %d want 2", got)
	}
	if rot.calls != 1 {
		t.Errorf("self rotater calls: got %d want 1", rot.calls)
	}
}

func TestCoordinator_OnePeerFailsPropose_Aborts(t *testing.T) {
	rot := &fakeRotater{}
	poster := &fakePoster{
		statusFn: func(url string) int {
			// peer C rejects propose
			if strings.Contains(url, "17982") && strings.HasSuffix(url, "/propose") {
				return http.StatusConflict
			}
			return http.StatusOK
		},
	}
	peers := []Peer{{MemberID: "B", Address: "127.0.0.1:17981"}, {MemberID: "C", Address: "127.0.0.1:17982"}}
	c := mkCoord(t, CoordinatorOpts{
		Rotater: rot, Client: poster,
		PeerSource: func() []Peer { return peers },
		ProposeTimeout: 5 * time.Second,
	})
	res, err := c.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if res.Committed {
		t.Errorf("expected NOT committed on partial failure; res=%+v", res)
	}
	if got := poster.UrlsByPath("/clan/rotate-key/commit"); got != 0 {
		t.Errorf("commit must not broadcast; got %d", got)
	}
	if got := poster.UrlsByPath("/clan/rotate-key/abort"); got != 1 {
		// Only B (which ACKed) gets the abort; C never made it to ACKed.
		t.Errorf("abort broadcasts: got %d want 1", got)
	}
	if rot.calls != 0 {
		t.Errorf("self must NOT rotate on abort; got %d calls", rot.calls)
	}
}

func TestCoordinator_LoneOrchestrator_NoPeers_SelfRotates(t *testing.T) {
	rot := &fakeRotater{}
	c := mkCoord(t, CoordinatorOpts{
		Rotater: rot, Client: &fakePoster{},
		PeerSource: func() []Peer { return nil },
	})
	res, err := c.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !res.Committed {
		t.Errorf("lone-orch should commit trivially; got %+v", res)
	}
	if rot.calls != 1 {
		t.Errorf("self rotater should fire once; got %d", rot.calls)
	}
}

func TestCoordinator_PeerNetworkError_TreatedAsFailure(t *testing.T) {
	rot := &fakeRotater{}
	poster := &fakePoster{
		errFn: func(url string) error {
			if strings.Contains(url, "17981") {
				return errors.New("connection refused")
			}
			return nil
		},
	}
	peers := []Peer{{MemberID: "B", Address: "127.0.0.1:17981"}, {MemberID: "C", Address: "127.0.0.1:17982"}}
	c := mkCoord(t, CoordinatorOpts{
		Rotater: rot, Client: poster,
		PeerSource: func() []Peer { return peers },
		ProposeTimeout: 1 * time.Second,
	})
	res, err := c.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if res.Committed {
		t.Errorf("network error should abort; got %+v", res)
	}
}
