package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minti/cland/internal/state"
)

// ---------- ParseProposals (the §13.9 tolerant parser) ----------

func TestParseProposalsCleanAndWrapped(t *testing.T) {
	clean := `[{"type":"fact","title":"water is wet","body":"","session_id":"","tags":["x"]}]`
	if got := ParseProposals(clean); len(got) != 1 || got[0].Title != "water is wet" {
		t.Fatalf("clean parse failed: %+v", got)
	}
	wrapped := "Sure! Here are the durable memories I found:\n```json\n" + clean + "\n```\nHope this helps!"
	if got := ParseProposals(wrapped); len(got) != 1 {
		t.Fatalf("prose-wrapped parse failed: %+v", got)
	}
	thinking := "<think>let me reason about [brackets] for a while...</think>\n" + clean
	if got := ParseProposals(thinking); len(got) != 1 {
		t.Fatalf("thinking-trace parse failed: %+v", got)
	}
	// A misleading bracket BEFORE the real array.
	decoy := "as noted [1] in the logs:\n" + clean
	if got := ParseProposals(decoy); len(got) != 1 {
		t.Fatalf("decoy-bracket parse failed: %+v", got)
	}
}

func TestParseProposalsDropsInvalidAndCaps(t *testing.T) {
	raw := `[
		{"type":"fact","title":"keep me"},
		{"type":"event","title":"models may not mint events"},
		{"type":"fact","title":""},
		{"type":"FINDING","title":"case-normalized"},
		{"type":"fact","title":"2"},{"type":"fact","title":"3"},
		{"type":"fact","title":"4"},{"type":"fact","title":"5"},
		{"type":"fact","title":"6 — over the cap"}
	]`
	got := ParseProposals(raw)
	if len(got) != MaxProposalsPerPass {
		t.Fatalf("got %d proposals, want cap %d", len(got), MaxProposalsPerPass)
	}
	if got[0].Title != "keep me" || got[1].Type != "finding" {
		t.Fatalf("validation/normalization wrong: %+v", got[:2])
	}
}

func TestParseProposalsGarbage(t *testing.T) {
	for _, raw := range []string{"", "no json here", "[not, valid, json", "{\"an\":\"object\"}"} {
		if got := ParseProposals(raw); got != nil {
			t.Fatalf("garbage %q yielded %+v, want nil", raw, got)
		}
	}
	if got := ParseProposals("[]"); len(got) != 0 {
		t.Fatalf("empty array should yield 0 proposals, got %+v", got)
	}
}

// ---------- model pick ----------

func TestSmallestResidentModel(t *testing.T) {
	models := []string{"hermes3:8b", "llama3.2:1b", "qwen2.5:0.5b", "mystery-model"}
	if got := SmallestResidentModel(models); got != "qwen2.5:0.5b" {
		t.Fatalf("smallest = %q, want qwen2.5:0.5b", got)
	}
	if got := SmallestResidentModel([]string{"mystery-a", "mystery-b"}); got != "mystery-a" {
		t.Fatalf("no-hint fallback = %q, want first", got)
	}
	if got := SmallestResidentModel(nil); got != "" {
		t.Fatalf("empty list = %q, want empty", got)
	}
	os.Setenv("MINTI_CLAND_SCRIBE_MODEL", "forced:1b")
	defer os.Unsetenv("MINTI_CLAND_SCRIBE_MODEL")
	if got := SmallestResidentModel(models); got != "forced:1b" {
		t.Fatalf("env override ignored, got %q", got)
	}
}

// ---------- the loop ----------

// fakeRuntime serves a canned /v1/chat/completions content and counts calls.
func fakeRuntime(t *testing.T, content string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(content) + `}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func newScribeRig(t *testing.T, runtimeURL, chatDir string, isScribe func() bool) (*Scribe, *Service) {
	t.Helper()
	store, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(ServiceOpts{Store: store, SelfID: "scribe-self", ClanID: "clan-s", Audit: nopAudit{}})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := NewScribe(ScribeOpts{
		Service:     svc,
		SelfID:      "scribe-self",
		ClanID:      "clan-s",
		IsScribe:    isScribe,
		RuntimeBase: runtimeURL,
		PickModel:   func(context.Context) string { return "tiny:1b" },
		ChatDir:     chatDir,
		Audit:       nopAudit{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sc, svc
}

func TestScribeTickDistillsNewChatOnly(t *testing.T) {
	content := `[{"type":"fact","title":"the clan prefers lowercase","body":"observed in chat","session_id":"","tags":["style"]}]`
	rt, calls := fakeRuntime(t, content)
	chatDir := t.TempDir()
	chatFile := filepath.Join(chatDir, "session-1.jsonl")
	if err := os.WriteFile(chatFile, []byte("{\"role\":\"user\",\"text\":\"pre-boot history\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sc, svc := newScribeRig(t, rt.URL, chatDir, func() bool { return true })
	now := time.Now()

	// Tick 1: seeding — marks set to EOF, NO distillation of the backlog.
	sc.TickForTest(context.Background(), now)
	if calls.Load() != 0 {
		t.Fatal("seeding tick must not call the model")
	}
	// Tick 2: nothing new — no call.
	sc.TickForTest(context.Background(), now.Add(time.Second))
	if calls.Load() != 0 {
		t.Fatal("no-activity tick must not call the model")
	}
	// New chat lands; tick 3 distills it.
	f, _ := os.OpenFile(chatFile, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("{\"role\":\"user\",\"text\":\"we decided to always pin certs\"}\n")
	_ = f.Close()
	sc.TickForTest(context.Background(), now.Add(2*time.Second))
	if calls.Load() != 1 {
		t.Fatalf("fresh chat must trigger exactly one distillation, got %d", calls.Load())
	}

	snap := svc.Snapshot()
	if len(snap.Nodes) != 1 {
		t.Fatalf("expected 1 proposal node, got %d", len(snap.Nodes))
	}
	n := snap.Nodes[0]
	if n.Status != "proposed" || n.Provenance.Source != "scribe" || n.Provenance.AuthorMemberID != "scribe-self" {
		t.Fatalf("proposal gating wrong: %+v", n)
	}
}

func TestScribeNotScribeNoDistill(t *testing.T) {
	rt, calls := fakeRuntime(t, `[{"type":"fact","title":"x"}]`)
	chatDir := t.TempDir()
	chatFile := filepath.Join(chatDir, "s.jsonl")
	_ = os.WriteFile(chatFile, []byte("a\n"), 0o600)

	sc, svc := newScribeRig(t, rt.URL, chatDir, func() bool { return false })
	now := time.Now()
	sc.TickForTest(context.Background(), now) // seed
	_ = os.WriteFile(chatFile, []byte("a\nb\n"), 0o600)
	sc.TickForTest(context.Background(), now.Add(time.Second))
	if calls.Load() != 0 {
		t.Fatal("non-scribe must never call the model")
	}
	if len(svc.Snapshot().Nodes) != 0 {
		t.Fatal("non-scribe must never write")
	}
}

func TestScribeGarbageCompletionWritesNothing(t *testing.T) {
	rt, calls := fakeRuntime(t, "I am a small model and I will now ramble without any JSON at all.")
	chatDir := t.TempDir()
	chatFile := filepath.Join(chatDir, "s.jsonl")
	_ = os.WriteFile(chatFile, []byte("a\n"), 0o600)

	sc, svc := newScribeRig(t, rt.URL, chatDir, func() bool { return true })
	now := time.Now()
	sc.TickForTest(context.Background(), now) // seed
	f, _ := os.OpenFile(chatFile, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("new activity\n")
	_ = f.Close()
	sc.TickForTest(context.Background(), now.Add(time.Second))
	if calls.Load() != 1 {
		t.Fatalf("expected the model to be consulted once, got %d", calls.Load())
	}
	if len(svc.Snapshot().Nodes) != 0 {
		t.Fatal("garbage completion must write NOTHING to the graph")
	}
}

func TestScribeSessionSummaryDeterministicID(t *testing.T) {
	sessID := "00000000-0000-4000-8000-00000000sess"
	content := `[{"type":"finding","title":"session summary: TLS quirks trace to clock skew","body":"...","session_id":"` + sessID + `","tags":[]}]`
	rt, _ := fakeRuntime(t, content)
	chatDir := t.TempDir()
	chatFile := filepath.Join(chatDir, "s.jsonl")
	_ = os.WriteFile(chatFile, []byte("a\n"), 0o600)

	sc, svc := newScribeRig(t, rt.URL, chatDir, func() bool { return true })
	// An OPEN research session the summary may bind to.
	if _, err := svc.AddOrUpdateNode("founder", Node{ID: sessID, Type: "research_session", Title: "TLS quirks", Status: "active"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	sc.TickForTest(context.Background(), now) // seed
	append1 := func(s string) {
		f, _ := os.OpenFile(chatFile, os.O_APPEND|os.O_WRONLY, 0)
		_, _ = f.WriteString(s)
		_ = f.Close()
	}
	append1("activity 1\n")
	sc.TickForTest(context.Background(), now.Add(time.Second))
	append1("activity 2\n")
	sc.TickForTest(context.Background(), now.Add(2*time.Second))

	// Two passes proposed the same summary — the deterministic id must have
	// UPDATED one node, not spawned a twin.
	var summaries []Node
	for _, n := range svc.Snapshot().Nodes {
		if strings.HasPrefix(strings.ToLower(n.Title), "session summary") {
			summaries = append(summaries, n)
		}
	}
	if len(summaries) != 1 {
		t.Fatalf("expected exactly 1 summary node, got %d", len(summaries))
	}
	if summaries[0].ID != DeterministicEventID("clan-s", "session_summary", sessID, "") {
		t.Fatal("summary did not use the deterministic §13.3 id")
	}
	if summaries[0].Rev < 2 {
		t.Fatalf("second pass should have UPDATED the summary (rev %d)", summaries[0].Rev)
	}
	// And the contributes_to edge exists exactly once.
	edges := 0
	for _, e := range svc.Snapshot().Edges {
		if e.To == sessID && e.Relation == "contributes_to" {
			edges++
		}
	}
	if edges != 1 {
		t.Fatalf("expected 1 contributes_to edge, got %d", edges)
	}
}

func TestScribePendingBudgetSkips(t *testing.T) {
	rt, calls := fakeRuntime(t, `[{"type":"fact","title":"one more"}]`)
	chatDir := t.TempDir()
	chatFile := filepath.Join(chatDir, "s.jsonl")
	_ = os.WriteFile(chatFile, []byte("a\n"), 0o600)

	sc, svc := newScribeRig(t, rt.URL, chatDir, func() bool { return true })
	// Pre-fill PendingProposalBudget+1 of our own proposals via merge
	// (cap-exempt and fast).
	var nodes []Node
	for i := 0; i <= PendingProposalBudget; i++ {
		n := node(DeterministicEventID("clan-s", "fill", "p", string(rune('a'+i%26))+"-"+strconvI(i)), 1, "2026-06-11T10:00:00Z")
		n.Status = "proposed"
		n.Provenance.Source = "scribe"
		n.Provenance.AuthorMemberID = "scribe-self"
		nodes = append(nodes, n)
	}
	if _, err := svc.ApplyRemote(graph(nodes, nil), "fill"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	sc.TickForTest(context.Background(), now) // seed
	f, _ := os.OpenFile(chatFile, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("new activity\n")
	_ = f.Close()
	sc.TickForTest(context.Background(), now.Add(time.Second))
	if calls.Load() != 0 {
		t.Fatal("over-budget scribe must not consult the model (§13.9 budget)")
	}
}

func strconvI(i int) string {
	return string([]byte{byte('0' + (i/100)%10), byte('0' + (i/10)%10), byte('0' + i%10)})
}
