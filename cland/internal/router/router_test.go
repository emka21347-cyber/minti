package router

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/election"
	"github.com/minti/cland/internal/peers"
)

// ---------- fakes ----------

type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

// fakePoster records the last request and returns a programmable response.
type fakePoster struct {
	resp         *http.Response
	err          error
	calls        atomic.Int32
	lastURL      string
	lastHeaders  http.Header
	lastBody     []byte
}

func (f *fakePoster) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	f.lastURL = req.URL.String()
	f.lastHeaders = req.Header.Clone()
	if req.Body != nil {
		f.lastBody, _ = io.ReadAll(req.Body)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func mkOK(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func mkSSE(events []string) *http.Response {
	body := strings.Join(events, "")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":  {"text/event-stream"},
			"Cache-Control": {"no-cache"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func mkRouter(t *testing.T, selfID, orchID string, opts ...func(*Opts)) *Router {
	t.Helper()
	st := election.NewState(selfID, 1, orchID, 32)
	reg := peers.NewRegistry()
	o := Opts{
		SelfID:         selfID,
		ClanID:         "c1",
		ElectionState:  st,
		Registry:       reg,
		RuntimeBaseURL: "http://127.0.0.1:7780",
		LocalClient:    &fakePoster{resp: mkOK(`{"ok":true}`)},
		PeerClient:     &fakePoster{resp: mkOK(`{"ok":true}`)},
		Audit:          noopAudit{},
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, fn := range opts {
		fn(&o)
	}
	r, err := NewRouter(o)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

// installPeer mirrors the engine_test helper but tailored to what the router
// reads (Address only, no LatestAd needed).
func installPeerWithAddr(reg *peers.Registry, memberID, addr string) {
	ad := &peers.Advertisement{
		MemberID:     memberID,
		LANAddress:   addr,
		Capabilities: map[string]any{"reasoning": map[string]any{"enabled": true}},
	}
	_ = reg.BindMember(ad, addr)
}

// ---------- 1. no orchestrator yet → 503 ----------

func TestRouter_NoOrchestrator_503(t *testing.T) {
	r := mkRouter(t, "A", "" /* no orch */)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d want 503; body=%s", w.Code, w.Body.String())
	}
}

// ---------- 2. self is Orchestrator → self-route fast path ----------

func TestRouter_SelfOrchestrator_SelfRoutes(t *testing.T) {
	local := &fakePoster{resp: mkOK(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)}
	r := mkRouter(t, "A", "A", func(o *Opts) { o.LocalClient = local })
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x","messages":[]}`))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if local.calls.Load() != 1 {
		t.Errorf("local upstream not called; calls=%d", local.calls.Load())
	}
	if !strings.HasPrefix(local.lastURL, "http://127.0.0.1:7780/v1/chat/completions") {
		t.Errorf("upstream URL: got %q", local.lastURL)
	}
	if r.Counters().SelfRoutes != 1 {
		t.Errorf("SelfRoutes counter: got %d", r.Counters().SelfRoutes)
	}
}

// ---------- 3. local runtime down → 502 ----------

func TestRouter_LocalRuntimeDown_502(t *testing.T) {
	local := &fakePoster{err: fmt.Errorf("conn refused")}
	r := mkRouter(t, "A", "A", func(o *Opts) { o.LocalClient = local })
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("got %d want 502", w.Code)
	}
}

// ---------- 4. self is NOT orchestrator, peer known → peer-proxy ----------

func TestRouter_NotOrchestrator_ProxiesToPeer(t *testing.T) {
	peer := &fakePoster{resp: mkOK(`{"choices":[{"message":{"role":"assistant","content":"via-peer"}}]}`)}
	r := mkRouter(t, "B", "A", func(o *Opts) { o.PeerClient = peer })
	installPeerWithAddr(r.opts.Registry, "A", "127.0.0.1:17980")

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"model":"x","messages":[]}`))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if peer.calls.Load() != 1 {
		t.Errorf("peer poster not called: %d", peer.calls.Load())
	}
	if !strings.HasPrefix(peer.lastURL, "https://127.0.0.1:17980/api/chat") {
		t.Errorf("peer URL: got %q", peer.lastURL)
	}
	if r.Counters().PeerProxies != 1 {
		t.Errorf("PeerProxies counter: got %d", r.Counters().PeerProxies)
	}
}

// ---------- 5. peer address unknown → 503 + X-Minti-Expected-Orchestrator ----------

func TestRouter_PeerAddrUnknown_503WithHeader(t *testing.T) {
	r := mkRouter(t, "B", "A") // A is Orchestrator but not in registry
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d want 503", w.Code)
	}
	if got := w.Header().Get("X-Minti-Expected-Orchestrator"); got != "A" {
		t.Errorf("X-Minti-Expected-Orchestrator: got %q want A", got)
	}
	if r.Counters().RejectsNonOrch != 1 {
		t.Errorf("RejectsNonOrch: got %d", r.Counters().RejectsNonOrch)
	}
}

// ---------- 6. streaming pass-through (SSE) ----------

func TestRouter_StreamingPassThrough_SSE(t *testing.T) {
	events := []string{
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	local := &fakePoster{resp: mkSSE(events)}
	r := mkRouter(t, "A", "A", func(o *Opts) { o.LocalClient = local })
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type: got %q want text/event-stream", ct)
	}
	got := w.Body.String()
	for _, ev := range events {
		if !strings.Contains(got, ev) {
			t.Errorf("missing event %q in body %q", ev, got)
		}
	}
}

// ---------- 7. forwardable headers strip HMAC quad ----------

func TestRouter_StripsHMACHeadersOnForward(t *testing.T) {
	local := &fakePoster{resp: mkOK("{}")}
	r := mkRouter(t, "A", "A", func(o *Opts) { o.LocalClient = local })

	req := httptest.NewRequest("POST", "/api/chat", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Minti-Member", "B")
	req.Header.Set("X-Minti-Timestamp", "12345")
	req.Header.Set("X-Minti-Nonce", "deadbeef")
	req.Header.Set("X-Minti-HMAC", "abc")
	req.Header.Set("X-Custom-App-Header", "preserve-me")
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)

	if v := local.lastHeaders.Get("X-Minti-Member"); v != "" {
		t.Errorf("X-Minti-Member should be stripped, got %q", v)
	}
	if v := local.lastHeaders.Get("X-Minti-HMAC"); v != "" {
		t.Errorf("X-Minti-HMAC should be stripped, got %q", v)
	}
	if v := local.lastHeaders.Get("X-Custom-App-Header"); v != "preserve-me" {
		t.Errorf("custom header should be preserved, got %q", v)
	}
	if v := local.lastHeaders.Get("Content-Type"); v != "application/json" {
		t.Errorf("Content-Type should be preserved, got %q", v)
	}
}

// ---------- 8. real-HTTP self-route (httptest.NewServer) ----------

func TestRouter_SelfRoute_AgainstFakeRuntime(t *testing.T) {
	// Stand up a real loopback HTTP server pretending to be runtime-adapter.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream got unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "wrong path", 400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"echo":"%s"}`, body)))
	}))
	defer upstream.Close()

	r := mkRouter(t, "A", "A", func(o *Opts) {
		o.RuntimeBaseURL = upstream.URL
		o.LocalClient = &http.Client{Timeout: 5 * time.Second}
	})

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"ping":1}`)))
	w := httptest.NewRecorder()
	r.handleReasoning(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"ping":1`) {
		t.Errorf("body didn't echo request payload: %s", w.Body.String())
	}
}
