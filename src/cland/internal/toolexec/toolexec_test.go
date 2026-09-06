package toolexec

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
	"sync"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/crypto"
)

// ---------- shared fakes ----------

type noopAudit struct{}

func (noopAudit) Write(auditlog.Event) error { return nil }

// recordingAudit captures decisions so tests can assert deny/allow + reason.
type recordingAudit struct {
	mu     sync.Mutex
	events []auditlog.Event
}

func (a *recordingAudit) Write(e auditlog.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}
func (a *recordingAudit) reasons() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.events))
	for i, e := range a.events {
		out[i] = e.Reason
	}
	return out
}

// fakeExecutor records calls and returns a programmable result/error.
type fakeExecutor struct {
	mu       sync.Mutex
	calls    int
	lastTool string
	lastArgs map[string]any
	result   *ExecResult
	err      error
}

func (f *fakeExecutor) Execute(_ context.Context, wireTool string, args map[string]any) (*ExecResult, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastTool = wireTool
	f.lastArgs = args
	if f.err != nil {
		return nil, "minti-fake", "fake_tool", f.err
	}
	if f.result == nil {
		f.result = &ExecResult{Content: []ResultContent{{Type: "text", Text: "ok"}}}
	}
	return f.result, "minti-fake", "fake_tool", nil
}

func mkKey(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed
	}
	return k
}

// mkToken builds + signs a token using `key`. now/exp helpers keep call sites
// readable.
func mkToken(t *testing.T, key []byte, target, tool string, argsJSON []byte, approvedAt, exp time.Time) Token {
	t.Helper()
	tok := Token{
		RequestID:    "rid-" + t.Name(),
		OriginMember: "origin-A",
		TargetMember: target,
		Tool:         tool,
		ArgsHash:     HashArgs(argsJSON),
		ApprovedAt:   approvedAt.UnixMilli(),
		Exp:          exp.UnixMilli(),
	}
	tok.Sign(key)
	return tok
}

func mkRequest(t *testing.T, tok Token, argsJSON []byte) *http.Request {
	t.Helper()
	body, err := json.Marshal(ExecuteRequest{Token: tok, Args: argsJSON})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return httptest.NewRequest("POST", "/mcp/execute", bytes.NewReader(body))
}

func mkHandler(t *testing.T, opts HandlerOpts) *Handler {
	t.Helper()
	if opts.Audit == nil {
		opts.Audit = noopAudit{}
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h, err := NewHandler(opts)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

// ---------- 1. token sign/verify roundtrip ----------

func TestToken_SignVerify_Roundtrip(t *testing.T) {
	key := mkKey(1)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", []byte(`{"path":"/x"}`),
		time.Now(), time.Now().Add(5*time.Minute))
	if err := tok.VerifyHMAC(key); err != nil {
		t.Errorf("verify: %v", err)
	}
}

// ---------- 2. token tamper detection ----------

func TestToken_Tampered_FailsVerify(t *testing.T) {
	key := mkKey(1)
	args := []byte(`{"path":"/x"}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))

	// Flip a field — Tool changes but sig was for the original.
	tampered := tok
	tampered.Tool = "mcp-shell.execute" // attacker changes the requested tool
	if err := tampered.VerifyHMAC(key); !errors.Is(err, ErrSigMismatch) {
		t.Errorf("tampered Tool should ErrSigMismatch; got %v", err)
	}

	// Flip request_id — same sig won't cover it.
	tampered2 := tok
	tampered2.RequestID = "rid-stolen"
	if err := tampered2.VerifyHMAC(key); !errors.Is(err, ErrSigMismatch) {
		t.Errorf("tampered RequestID should ErrSigMismatch; got %v", err)
	}
}

// ---------- 3. wrong target reject ----------

func TestHandler_WrongTarget_403(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)

	args := []byte(`{}`)
	tok := mkToken(t, key, "X" /* wrong target */, "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))
	audit := &recordingAudit{}
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{}, Audit: audit,
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusForbidden {
		t.Errorf("status: got %d want 403; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.Join(audit.reasons(), ","), "wrong_target") {
		t.Errorf("audit didn't record wrong_target: %v", audit.reasons())
	}
}

// ---------- 4. expired reject ----------

func TestHandler_Expired_401(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	args := []byte(`{}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", args,
		time.Now().Add(-10*time.Minute), time.Now().Add(-1*time.Minute))
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{},
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired token: got %d want 401", w.Code)
	}
}

// ---------- 5. future approved_at reject ----------

func TestHandler_FutureApprovedAt_401(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	args := []byte(`{}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", args,
		time.Now().Add(2*time.Hour), time.Now().Add(3*time.Hour))
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{},
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("future approved_at: got %d want 401", w.Code)
	}
}

// ---------- 6. replay reject ----------

func TestHandler_Replay_SecondCall401(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	args := []byte(`{}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))
	audit := &recordingAudit{}
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{}, Audit: audit,
	})

	// First call should succeed.
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusOK {
		t.Fatalf("first call: got %d want 200; body=%s", w.Code, w.Body.String())
	}

	// Second call with SAME token — should hit replay cache.
	w2 := httptest.NewRecorder()
	h.handleExecute(w2, mkRequest(t, tok, args))
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("replay: got %d want 401; body=%s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(strings.Join(audit.reasons(), ","), "replay") {
		t.Errorf("audit didn't record replay: %v", audit.reasons())
	}
}

// ---------- 7. foreign clan_key reject ----------

func TestHandler_ForeignKey_401(t *testing.T) {
	myKey := mkKey(1)
	foreignKey := mkKey(2)
	kp, _ := crypto.NewSimpleKeyProvider(myKey)
	args := []byte(`{}`)
	tok := mkToken(t, foreignKey, "B", "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{},
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("foreign key: got %d want 401; body=%s", w.Code, w.Body.String())
	}
}

// ---------- 8. args tamper after sign ----------

func TestHandler_ArgsTamperedAfterSign_401(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	originalArgs := []byte(`{"path":"/safe"}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", originalArgs,
		time.Now(), time.Now().Add(5*time.Minute))

	// Attacker swaps args — same token sig, different args bytes.
	tamperedArgs := []byte(`{"path":"/etc/passwd"}`)
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{},
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, tamperedArgs))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("args tamper: got %d want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "args_hash_mismatch") {
		t.Errorf("body should mention args_hash_mismatch: %s", w.Body.String())
	}
}

// ---------- 9. grace key acceptance during rotation ----------

func TestHandler_GraceKey_AcceptedDuringRotation(t *testing.T) {
	oldKey := mkKey(1)
	newKey := mkKey(2)
	kp, _ := crypto.NewSimpleKeyProvider(oldKey)
	// Rotation: old becomes grace, new becomes current.
	if err := kp.Rotate(newKey, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	args := []byte(`{}`)
	// Origin still has the OLD key (rotation in progress).
	tok := mkToken(t, oldKey, "B", "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: &fakeExecutor{},
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusOK {
		t.Errorf("grace-key signed token should be accepted; got %d", w.Code)
	}
}

// ---------- 10. happy path ----------

func TestHandler_HappyPath_Success(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	args := []byte(`{"path":"/x"}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))

	fake := &fakeExecutor{result: &ExecResult{Content: []ResultContent{
		{Type: "text", Text: "hello from fake tool"},
	}}}
	audit := &recordingAudit{}
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: fake, Audit: audit,
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if fake.calls != 1 {
		t.Errorf("executor calls: got %d want 1", fake.calls)
	}
	if fake.lastTool != "mcp-fs.read_file" {
		t.Errorf("tool passed to executor: %q", fake.lastTool)
	}
	var resp ExecuteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Text != "hello from fake tool" {
		t.Errorf("unexpected result content: %+v", resp.Result.Content)
	}
	if got := strings.Join(audit.reasons(), ","); !strings.Contains(got, "ok") {
		t.Errorf("audit didn't record ok: %v", audit.reasons())
	}
}

// ---------- 11. executor failure → 502 (distinct from token deny) ----------

func TestHandler_ExecutorFails_502(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	args := []byte(`{}`)
	tok := mkToken(t, key, "B", "mcp-fs.read_file", args,
		time.Now(), time.Now().Add(5*time.Minute))

	fake := &fakeExecutor{err: errors.New("subprocess crashed")}
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: fake,
	})
	w := httptest.NewRecorder()
	h.handleExecute(w, mkRequest(t, tok, args))
	if w.Code != http.StatusBadGateway {
		t.Errorf("got %d want 502", w.Code)
	}
}

// ---------- 12. executor resolveBinary parses tool spec ----------

func TestExecutor_ResolveBinary_ParsesNamespace(t *testing.T) {
	e := &Executor{BinariesDir: t.TempDir()}
	server, tool, path, err := e.resolveBinary("mcp-recon.nmap_scan")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if server != "minti-mcp-recon" {
		t.Errorf("server: got %q want minti-mcp-recon", server)
	}
	if tool != "nmap_scan" {
		t.Errorf("tool: got %q want nmap_scan", tool)
	}
	if !strings.Contains(path, "minti-mcp-recon") {
		t.Errorf("path missing binary name: %q", path)
	}
}

// ---------- 12b. rate limiter — allow/deny + per-origin isolation ----------

func TestRateLimiter_AllowUnderLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.Allow("B") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
}

func TestRateLimiter_DenyOverLimit(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	rl.Allow("B")
	rl.Allow("B")
	if rl.Allow("B") {
		t.Errorf("3rd request in window should be denied")
	}
}

func TestRateLimiter_PerOriginIsolated(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	if !rl.Allow("B") {
		t.Errorf("first req from B should be allowed")
	}
	if !rl.Allow("C") {
		t.Errorf("first req from C should be allowed (different bucket)")
	}
	if rl.Allow("B") {
		t.Errorf("second req from B should be denied")
	}
}

func TestRateLimiter_EmptyOriginBypasses(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	for i := 0; i < 5; i++ {
		if !rl.Allow("") {
			t.Errorf("empty origin should always bypass; got deny at %d", i)
		}
	}
}

func TestRateLimiter_WindowSlides(t *testing.T) {
	rl := NewRateLimiter(1, 100*time.Millisecond)
	// Inject a deterministic clock.
	now := time.Now()
	rl.now = func() time.Time { return now }
	if !rl.Allow("B") {
		t.Fatalf("first allow")
	}
	if rl.Allow("B") {
		t.Fatalf("second should deny")
	}
	now = now.Add(150 * time.Millisecond)
	if !rl.Allow("B") {
		t.Errorf("after window slides, should allow again")
	}
}

func TestHandler_RateLimited_Returns429(t *testing.T) {
	key := mkKey(1)
	kp, _ := crypto.NewSimpleKeyProvider(key)
	rl := NewRateLimiter(1, time.Minute) // strict: 1 per minute
	fake := &fakeExecutor{}
	audit := &recordingAudit{}
	h := mkHandler(t, HandlerOpts{
		SelfID: "B", KeyProvider: kp,
		Executor: fake, RateLimiter: rl, Audit: audit,
	})
	// 1st request — passes rate limit + succeeds.
	args := []byte(`{}`)
	tok1 := mkToken(t, key, "B", "mcp-fs.read_file", args, time.Now(), time.Now().Add(5*time.Minute))
	tok1.RequestID = "rid-1"
	tok1.Sign(key)
	w1 := httptest.NewRecorder()
	h.handleExecute(w1, mkRequest(t, tok1, args))
	if w1.Code != http.StatusOK {
		t.Fatalf("first req: %d", w1.Code)
	}
	// 2nd request from same origin (different request_id) — rate-limited.
	tok2 := mkToken(t, key, "B", "mcp-fs.read_file", args, time.Now(), time.Now().Add(5*time.Minute))
	tok2.RequestID = "rid-2"
	tok2.Sign(key)
	w2 := httptest.NewRecorder()
	h.handleExecute(w2, mkRequest(t, tok2, args))
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("second req should be 429, got %d", w2.Code)
	}
	reasons := strings.Join(audit.reasons(), ",")
	if !strings.Contains(reasons, "rate_limited") {
		t.Errorf("audit should record rate_limited; got %v", audit.reasons())
	}
}

// ---------- 13. replay cache TTL eviction ----------

func TestReplayCache_TTLEviction(t *testing.T) {
	c := NewReplayCache(100, 50*time.Millisecond)
	now := time.Now()
	if !c.CheckAndStore("rid-1", now) {
		t.Fatal("first insert should succeed")
	}
	if c.CheckAndStore("rid-1", now.Add(10*time.Millisecond)) {
		t.Fatal("within-TTL replay should fail")
	}
	if !c.CheckAndStore("rid-1", now.Add(100*time.Millisecond)) {
		t.Fatal("after-TTL re-presentation should be allowed (entry expired)")
	}
}

// ---------- 13b. replay cache overflow rejects rather than evicts (H-3) ----------

func TestReplayCache_OverflowRejectsRatherThanEvict(t *testing.T) {
	// qwen project-review hardening: when cap is hit + no expirable entries,
	// the cache must REJECT the new request (return false) rather than
	// evict a still-valid entry. Otherwise an attacker could provoke
	// eviction by flooding distinct request_ids, then replay an evicted one.
	c := NewReplayCache(3, time.Hour)
	now := time.Now()
	for i, rid := range []string{"a", "b", "c"} {
		if !c.CheckAndStore(rid, now) {
			t.Fatalf("insert %d (%q) should succeed", i, rid)
		}
	}
	// Cap reached. New request must be rejected (overflow), NOT accepted-via-eviction.
	if c.CheckAndStore("d", now) {
		t.Fatalf("overflow request must be rejected; got accepted (eviction bug)")
	}
	if c.OverflowCount() != 1 {
		t.Errorf("OverflowCount: got %d want 1", c.OverflowCount())
	}
	// Existing entries must still be in the cache (not evicted).
	for _, rid := range []string{"a", "b", "c"} {
		if c.CheckAndStore(rid, now) {
			t.Errorf("entry %q should still be in cache (replay should detect)", rid)
		}
	}
}
