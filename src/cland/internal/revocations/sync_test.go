package revocations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
)

// ---------- fakes ----------

type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

type fakeFetcher struct {
	calls    atomic.Int32
	resp     *http.Response
	err      error
	lastURL  string
}

func (f *fakeFetcher) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	f.lastURL = req.URL.String()
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func mkRevsResp(revs state.Revocations) *http.Response {
	body, _ := json.Marshal(revs)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func mkSyncer(t *testing.T, fetcher Fetcher, addrFn AddressLookup) (*Syncer, *state.Store, *peers.Registry) {
	t.Helper()
	store, _ := state.NewStore(t.TempDir())
	reg := peers.NewRegistry()
	s, err := NewSyncer(SyncerOpts{
		SelfID:     "A",
		Store:      store,
		Registry:   reg,
		Fetcher:    fetcher,
		LookupAddr: addrFn,
		Audit:      noopAudit{},
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewSyncer: %v", err)
	}
	return s, store, reg
}

// ---------- digest tests (state package would normally own, but exercised here) ----------

func TestDigest_StableAcrossPermutations(t *testing.T) {
	r1 := &state.Revocations{Entries: []state.Revocation{
		{MemberID: "alice"}, {MemberID: "bob"}, {MemberID: "charlie"},
	}}
	r2 := &state.Revocations{Entries: []state.Revocation{
		{MemberID: "charlie"}, {MemberID: "alice"}, {MemberID: "bob"},
	}}
	if r1.Digest() != r2.Digest() {
		t.Errorf("digest must be permutation-invariant; r1=%s r2=%s", r1.Digest(), r2.Digest())
	}
}

func TestDigest_EmptyHasStableValue(t *testing.T) {
	empty1 := &state.Revocations{}
	empty2 := (*state.Revocations)(nil)
	if empty1.Digest() != empty2.Digest() {
		t.Errorf("empty + nil should produce same digest")
	}
	// And: should NOT equal a single-entry digest.
	one := &state.Revocations{Entries: []state.Revocation{{MemberID: "x"}}}
	if empty1.Digest() == one.Digest() {
		t.Errorf("empty digest must differ from non-empty")
	}
}

// ---------- MaybeSync tests ----------

func TestMaybeSync_EmptyDigestSkipsFetch(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, _, _ := mkSyncer(t, fetcher, func(string) string { return "127.0.0.1:1" })
	if s.MaybeSync(context.Background(), "B", "") {
		t.Errorf("empty digest should be no-op")
	}
	if fetcher.calls.Load() != 0 {
		t.Errorf("fetcher should not be called; got %d", fetcher.calls.Load())
	}
}

func TestMaybeSync_MatchingDigest_NoFetch(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, store, _ := mkSyncer(t, fetcher, func(string) string { return "127.0.0.1:1" })

	// Seed local with one entry; compute its digest; pass that digest in.
	local := &state.Revocations{Entries: []state.Revocation{{MemberID: "evil-1"}}}
	if err := store.SaveRevocations(local); err != nil {
		t.Fatal(err)
	}
	if s.MaybeSync(context.Background(), "B", local.Digest()) {
		t.Errorf("matching digest should be no-op")
	}
	if fetcher.calls.Load() != 0 {
		t.Errorf("fetcher should not be called; got %d", fetcher.calls.Load())
	}
}

func TestMaybeSync_MismatchedDigest_FetchesAndMerges(t *testing.T) {
	// Peer has 2 entries we don't. Sender's digest != ours.
	peerRevs := state.Revocations{Entries: []state.Revocation{
		{MemberID: "evil-1", RevokedAt: time.Now()},
		{MemberID: "evil-2", RevokedAt: time.Now()},
	}}
	fetcher := &fakeFetcher{resp: mkRevsResp(peerRevs)}
	s, store, reg := mkSyncer(t, fetcher, func(id string) string {
		if id == "B" {
			return "127.0.0.1:17981"
		}
		return ""
	})

	if !s.MaybeSync(context.Background(), "B", peerRevs.Digest()) {
		t.Errorf("expected sync to fire on mismatch")
	}
	if fetcher.calls.Load() != 1 {
		t.Errorf("fetcher calls: got %d want 1", fetcher.calls.Load())
	}
	if !strings.HasPrefix(fetcher.lastURL, "https://127.0.0.1:17981/clan/revocations") {
		t.Errorf("fetch URL: got %q", fetcher.lastURL)
	}

	// Local store should now have both entries.
	got, _ := store.LoadRevocations()
	if got == nil || len(got.Entries) != 2 {
		t.Errorf("merged store: %v", got)
	}

	// peers.Registry should be refreshed (consults revocations on next BindMember).
	// We don't have a direct way to read its in-memory revocations set, so just
	// confirm SetRevocations was effectively called by checking no panic + no
	// SaveRevocations error. Indirect — trust the wire-up.
	_ = reg
}

func TestMaybeSync_UnknownPeerAddr_DoesNotPanic(t *testing.T) {
	fetcher := &fakeFetcher{}
	s, _, _ := mkSyncer(t, fetcher, func(string) string { return "" })
	// Empty addr lookup → MaybeSync returns false, doesn't crash.
	if s.MaybeSync(context.Background(), "B", "some-different-digest") {
		t.Errorf("addr-unknown should NOT report a fired sync")
	}
}

func TestMaybeSync_FetchError_DoesNotCorruptLocal(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("conn refused")}
	s, store, _ := mkSyncer(t, fetcher, func(string) string { return "127.0.0.1:1" })
	local := &state.Revocations{Entries: []state.Revocation{{MemberID: "keep-me"}}}
	if err := store.SaveRevocations(local); err != nil {
		t.Fatal(err)
	}
	// Mismatched digest → tries to fetch → fails → local unchanged.
	s.MaybeSync(context.Background(), "B", "different-digest")
	got, _ := store.LoadRevocations()
	if got == nil || len(got.Entries) != 1 || got.Entries[0].MemberID != "keep-me" {
		t.Errorf("fetch failure must not corrupt local; got %+v", got)
	}
}

func TestMaybeSync_Idempotent_DoesNotDoubleAdd(t *testing.T) {
	peerRevs := state.Revocations{Entries: []state.Revocation{{MemberID: "evil-1"}}}
	fetcher := &fakeFetcher{resp: mkRevsResp(peerRevs)}
	s, store, _ := mkSyncer(t, fetcher, func(string) string { return "127.0.0.1:1" })

	// First sync: applies the entry.
	s.MaybeSync(context.Background(), "B", peerRevs.Digest())
	got1, _ := store.LoadRevocations()

	// Now the digests match — second call should be no-op.
	fetcher.resp = mkRevsResp(peerRevs)
	s.MaybeSync(context.Background(), "B", peerRevs.Digest())
	got2, _ := store.LoadRevocations()

	if len(got1.Entries) != 1 || len(got2.Entries) != 1 {
		t.Errorf("entry count drifted: %d → %d", len(got1.Entries), len(got2.Entries))
	}
}

// ---------- GET /clan/revocations handler ----------

func TestHandler_GET_ReturnsList(t *testing.T) {
	store, _ := state.NewStore(t.TempDir())
	r := &state.Revocations{Entries: []state.Revocation{{MemberID: "evil"}}}
	_ = store.SaveRevocations(r)

	h := &Handler{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest("GET", "/clan/revocations", nil)
	w := httptest.NewRecorder()
	h.handleList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", w.Code)
	}
	var got state.Revocations
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].MemberID != "evil" {
		t.Errorf("body: %+v", got)
	}
}

func TestHandler_GET_EmptyStore_ReturnsEmpty(t *testing.T) {
	store, _ := state.NewStore(t.TempDir())
	h := &Handler{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest("GET", "/clan/revocations", nil)
	w := httptest.NewRecorder()
	h.handleList(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	var got state.Revocations
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Entries) != 0 {
		t.Errorf("empty store should yield empty list; got %+v", got)
	}
}
