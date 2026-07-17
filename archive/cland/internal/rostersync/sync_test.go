package rostersync

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

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
)

// ---------- fakes ----------

type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

type fakeFetcher struct {
	calls   atomic.Int32
	resp    *http.Response
	err     error
	lastURL string
}

func (f *fakeFetcher) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	f.lastURL = req.URL.String()
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func mkRosterResp(rs []state.RosterMember) *http.Response {
	body, _ := json.Marshal(rosterResp{Roster: rs})
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func mkSyncer(t *testing.T, fetcher Fetcher, addrFn AddressLookup) (*Syncer, *state.Store) {
	t.Helper()
	store, _ := state.NewStore(t.TempDir())
	s, err := NewSyncer(SyncerOpts{
		SelfID:     "A",
		Store:      store,
		Registry:   peers.NewRegistry(),
		Fetcher:    fetcher,
		LookupAddr: addrFn,
		Audit:      noopAudit{},
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewSyncer: %v", err)
	}
	return s, store
}

// ---------- state.Clan.RosterDigest + MergeRosterStates ----------

func TestRosterDigest_StableAcrossPermutations(t *testing.T) {
	a := &state.Clan{ClanID: "c1", Roster: []state.RosterMember{
		{MemberID: "alice", State: "active"},
		{MemberID: "bob", State: "admitted"},
		{MemberID: "carol", State: "revoked"},
	}}
	b := &state.Clan{ClanID: "c1", Roster: []state.RosterMember{
		{MemberID: "carol", State: "revoked"},
		{MemberID: "alice", State: "active"},
		{MemberID: "bob", State: "admitted"},
	}}
	if a.RosterDigest() != b.RosterDigest() {
		t.Errorf("digest must be permutation-invariant; %s != %s", a.RosterDigest(), b.RosterDigest())
	}
}

func TestRosterDigest_StateChangeChangesDigest(t *testing.T) {
	a := &state.Clan{Roster: []state.RosterMember{{MemberID: "x", State: "admitted"}}}
	b := &state.Clan{Roster: []state.RosterMember{{MemberID: "x", State: "active"}}}
	if a.RosterDigest() == b.RosterDigest() {
		t.Errorf("digest must change when state changes")
	}
}

func TestMergeRosterStates_TakesMoreProgressedState(t *testing.T) {
	local := []state.RosterMember{
		{MemberID: "alice", State: "admitted"},
		{MemberID: "bob", State: "active"},
	}
	other := []state.RosterMember{
		{MemberID: "alice", State: "active"},   // progresses
		{MemberID: "bob", State: "admitted"},   // does NOT regress
		{MemberID: "carol", State: "admitted"}, // new
	}
	merged := state.MergeRosterStates(local, other)
	got := map[string]string{}
	for _, m := range merged {
		got[m.MemberID] = m.State
	}
	if got["alice"] != "active" {
		t.Errorf("alice should progress admitted→active; got %q", got["alice"])
	}
	if got["bob"] != "active" {
		t.Errorf("bob should NOT regress active→admitted; got %q", got["bob"])
	}
	if got["carol"] != "admitted" {
		t.Errorf("carol should be added with admitted; got %q", got["carol"])
	}
}

func TestMergeRosterStates_RevokedAlwaysWins(t *testing.T) {
	local := []state.RosterMember{{MemberID: "evil", State: "active"}}
	other := []state.RosterMember{{MemberID: "evil", State: "revoked"}}
	merged := state.MergeRosterStates(local, other)
	if merged[0].State != "revoked" {
		t.Errorf("revoked must always trump active; got %q", merged[0].State)
	}
}

// ---------- MaybeSync ----------

func TestMaybeSync_EmptyDigestSkips(t *testing.T) {
	f := &fakeFetcher{}
	s, _ := mkSyncer(t, f, func(string) string { return "127.0.0.1:1" })
	if s.MaybeSync(context.Background(), "B", "") {
		t.Errorf("empty digest should be no-op")
	}
	if f.calls.Load() != 0 {
		t.Errorf("fetcher must not be called; got %d", f.calls.Load())
	}
}

func TestMaybeSync_MatchingDigest_NoFetch(t *testing.T) {
	clan := &state.Clan{ClanID: "c1", Roster: []state.RosterMember{
		{MemberID: "alice", State: "active"},
	}}
	f := &fakeFetcher{}
	s, store := mkSyncer(t, f, func(string) string { return "127.0.0.1:1" })
	_ = store.SaveClan(clan)

	if s.MaybeSync(context.Background(), "B", clan.RosterDigest()) {
		t.Errorf("matching digest should be no-op")
	}
	if f.calls.Load() != 0 {
		t.Errorf("fetcher must not be called; got %d", f.calls.Load())
	}
}

func TestMaybeSync_MismatchedDigest_FetchesAndMerges(t *testing.T) {
	// Local has admitted; peer has active.
	local := &state.Clan{ClanID: "c1", Roster: []state.RosterMember{
		{MemberID: "joiner", State: "admitted"},
		{MemberID: "self", State: "active"},
	}}
	peerRoster := []state.RosterMember{
		{MemberID: "joiner", State: "active"}, // progresses
		{MemberID: "self", State: "active"},
	}
	f := &fakeFetcher{resp: mkRosterResp(peerRoster)}
	s, store := mkSyncer(t, f, func(id string) string {
		if id == "B" {
			return "127.0.0.1:17981"
		}
		return ""
	})
	_ = store.SaveClan(local)

	// theirDigest is whatever the peer would have computed
	theirClan := &state.Clan{Roster: peerRoster}
	if !s.MaybeSync(context.Background(), "B", theirClan.RosterDigest()) {
		t.Errorf("expected sync to fire on mismatch")
	}
	if f.calls.Load() != 1 {
		t.Errorf("fetcher calls: got %d want 1", f.calls.Load())
	}
	if !strings.HasPrefix(f.lastURL, "https://127.0.0.1:17981/clan/roster") {
		t.Errorf("URL: got %q", f.lastURL)
	}
	got, _ := store.LoadClan()
	for _, m := range got.Roster {
		if m.MemberID == "joiner" && m.State != "active" {
			t.Errorf("joiner not promoted: state=%q want active", m.State)
		}
	}
}

func TestMaybeSync_UnknownPeerAddr_NoPanic(t *testing.T) {
	f := &fakeFetcher{}
	s, store := mkSyncer(t, f, func(string) string { return "" })
	_ = store.SaveClan(&state.Clan{ClanID: "c1", Roster: []state.RosterMember{{MemberID: "x", State: "admitted"}}})
	if s.MaybeSync(context.Background(), "B", "some-different-digest") {
		t.Errorf("addr-unknown should NOT report a fired sync")
	}
}

func TestMaybeSync_FetchError_PreservesLocal(t *testing.T) {
	f := &fakeFetcher{err: errors.New("conn refused")}
	s, store := mkSyncer(t, f, func(string) string { return "127.0.0.1:1" })
	local := &state.Clan{ClanID: "c1", Roster: []state.RosterMember{{MemberID: "keep", State: "admitted"}}}
	_ = store.SaveClan(local)
	s.MaybeSync(context.Background(), "B", "different-digest")
	got, _ := store.LoadClan()
	if len(got.Roster) != 1 || got.Roster[0].State != "admitted" {
		t.Errorf("fetch failure must not corrupt local; got %+v", got.Roster)
	}
}

// ---------- GET handler ----------

func TestHandler_GET_ReturnsRoster(t *testing.T) {
	store, _ := state.NewStore(t.TempDir())
	clan := &state.Clan{ClanID: "c1", Roster: []state.RosterMember{
		{MemberID: "alice", State: "active"},
	}}
	_ = store.SaveClan(clan)

	h := &Handler{Store: store, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest("GET", "/clan/roster", nil)
	w := httptest.NewRecorder()
	h.handleList(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status %d", w.Code)
	}
	var got rosterResp
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Roster) != 1 || got.Roster[0].State != "active" {
		t.Errorf("body: %+v", got)
	}
}
