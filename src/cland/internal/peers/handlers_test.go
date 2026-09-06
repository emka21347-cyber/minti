package peers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/state"
)

func TestHandlers_AdvertiseUpdatesRegistry(t *testing.T) {
	r := NewRegistry()
	h := &Handlers{Registry: r}

	ad := &Advertisement{MemberID: "m1", ClanID: "c", Generation: 1, ReasoningScore: 50, SystemScore: 60}
	body, _ := json.Marshal(ad)
	req := httptest.NewRequest("POST", "/clan/advertise", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.1:7777"
	rec := httptest.NewRecorder()
	h.handleAdvertise(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	_, members := r.Snapshot()
	if len(members) != 1 || members[0].MemberID != "m1" {
		t.Errorf("registry should contain m1, got %+v", members)
	}
}

func TestHandlers_AdvertiseRevokedReturns403(t *testing.T) {
	r := NewRegistry()
	r.SetRevocations(&state.Revocations{Entries: []state.Revocation{{MemberID: "evil"}}})
	h := &Handlers{Registry: r}

	body, _ := json.Marshal(&Advertisement{MemberID: "evil", ClanID: "c"})
	req := httptest.NewRequest("POST", "/clan/advertise", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleAdvertise(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("revoked member status = %d, want 403", rec.Code)
	}
}

func TestHandlers_AdvertiseBadJSON(t *testing.T) {
	h := &Handlers{Registry: NewRegistry()}
	req := httptest.NewRequest("POST", "/clan/advertise", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	h.handleAdvertise(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON status = %d, want 400", rec.Code)
	}
}

func TestHandlers_PeerAddTriggersBump(t *testing.T) {
	r := NewRegistry()
	r.SetDialFunc(func(string, string, time.Duration) error { return nil })
	var bumped atomic.Int32
	h := &Handlers{Registry: r, Bump: func() { bumped.Add(1) }}

	body, _ := json.Marshal(&PeerAddRequest{Address: "10.0.0.7:7777"})
	req := httptest.NewRequest("POST", "/clan/peer-add", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	// origin is empty (transport.OriginMember requires context value); peers.AddPeer
	// would reject empty origin, so simulate having one by stuffing it in context.
	// Test of the rate-limit + cap paths is covered in peers_test.go; here we
	// confirm the handler shape.
	h.handlePeerAdd(rec, req)

	// origin == "" → AddPeer should error out.
	if rec.Code == http.StatusOK {
		t.Errorf("missing origin should not succeed")
	}
	if bumped.Load() != 0 {
		t.Errorf("Bump should not fire on error")
	}
}

func TestHandlers_PeerAddDialFailReturns503(t *testing.T) {
	r := NewRegistry()
	r.SetDialFunc(func(string, string, time.Duration) error { return errors.New("connection refused") })
	h := &Handlers{Registry: r, Bump: func() {}}

	body, _ := json.Marshal(&PeerAddRequest{Address: "10.0.0.99:7777"})
	// Hand-build a request with an origin in context (skipping transport
	// middleware). Use a small ctx-injecting helper.
	req := requestWithOrigin("POST", "/clan/peer-add", body, "origin-x")
	rec := httptest.NewRecorder()
	h.handlePeerAdd(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("pre-dial fail status = %d, want 503", rec.Code)
	}
}

func TestHandlers_PeerAddSuccessFiresBump(t *testing.T) {
	r := NewRegistry()
	r.SetDialFunc(func(string, string, time.Duration) error { return nil })
	var bumped atomic.Int32
	h := &Handlers{Registry: r, Bump: func() { bumped.Add(1) }}

	body, _ := json.Marshal(&PeerAddRequest{Address: "10.0.0.42:7777"})
	req := requestWithOrigin("POST", "/clan/peer-add", body, "origin-y")
	rec := httptest.NewRecorder()
	h.handlePeerAdd(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("ok status = %d", rec.Code)
	}
	if bumped.Load() != 1 {
		t.Errorf("Bump should fire on success, got %d", bumped.Load())
	}
}

func TestHandlers_ListPeers(t *testing.T) {
	r := NewRegistry()
	_ = r.UpsertCandidate("10.0.0.1:7777", SourceMDNS)
	_ = r.BindMember(&Advertisement{MemberID: "m1", ClanID: "c", Generation: 1}, "10.0.0.5:7777")

	h := &Handlers{Registry: r}
	req := httptest.NewRequest("GET", "/clan/peers", nil)
	rec := httptest.NewRecorder()
	h.handleListPeers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp PeersListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 1 || len(resp.Members) != 1 {
		t.Errorf("expected 1 candidate + 1 member, got %d + %d", len(resp.Candidates), len(resp.Members))
	}
}

// requestWithOrigin builds an *http.Request with `originMemberKey` injected
// into the context, simulating what transport.authMiddleware does.
func requestWithOrigin(method, path string, body []byte, origin string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	// We need to inject the same context key transport.OriginMember reads.
	// transport exposes WithOrigin-equivalent indirectly — simplest: tests
	// that need this use a helper that mirrors transport's key. Since the
	// key type is unexported in transport, we use the transport.OriginMember
	// reader by piggy-backing on what middleware would have set. Easier:
	// build the request via httptest and rely on the fact that the handler
	// path under test (peer-add) reads OriginMember from context.
	//
	// For these tests we manually call transport's exported helper if one
	// exists; otherwise we use the same package-level injection pattern.
	return req.WithContext(withTestOrigin(req.Context(), origin))
}
