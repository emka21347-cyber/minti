package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/minti/cland/internal/toolexec"
)

// ---------- fakes ----------

// fakeLister returns canned schemas per namespace, mimicking toolexec.ListTools
// without spawning subprocesses.
type fakeLister struct {
	byNS map[string][]toolexec.ToolSchema
	err  map[string]error
}

func (f fakeLister) ListTools(_ context.Context, ns string) ([]toolexec.ToolSchema, error) {
	if f.err != nil {
		if e, ok := f.err[ns]; ok {
			return nil, e
		}
	}
	return f.byNS[ns], nil
}

// fakeCaller replays scripted replies turn by turn and records the transcript it
// last saw (so a test can assert tool results were fed back).
type fakeCaller struct {
	replies   []ModelReply
	calls     int
	lastTrans []Turn
}

func (f *fakeCaller) Call(_ context.Context, _ string, transcript []Turn, _ []ToolDef) (ModelReply, error) {
	f.lastTrans = transcript
	if f.calls >= len(f.replies) {
		// Default to a final answer so a runaway test still terminates.
		f.calls++
		return ModelReply{Text: "done"}, nil
	}
	r := f.replies[f.calls]
	f.calls++
	return r, nil
}

// fakeExecutor records executed wire tools and returns a canned result.
type fakeExecutor struct {
	executed []string
	result   *toolexec.ExecResult
	err      error
}

func (f *fakeExecutor) Execute(_ context.Context, wire string, _ map[string]any) (*toolexec.ExecResult, string, string, error) {
	f.executed = append(f.executed, wire)
	if f.err != nil {
		return nil, "", "", f.err
	}
	return f.result, "", "", nil
}

type recordingEmitter struct{ events []Event }

func (e *recordingEmitter) Emit(ev Event) error { e.events = append(e.events, ev); return nil }

func (e *recordingEmitter) types() []EventType {
	out := make([]EventType, len(e.events))
	for i, ev := range e.events {
		out[i] = ev.Type
	}
	return out
}

func (e *recordingEmitter) last() Event { return e.events[len(e.events)-1] }

// readCatalog builds a catalog with one read tool (read_text) and one change
// tool (write_text), filtered to read-only — matching the M1 S1 surface.
func readCatalog(t *testing.T) *Catalog {
	t.Helper()
	lister := fakeLister{byNS: map[string][]toolexec.ToolSchema{
		"mcp-fs": {
			{Name: "read_text", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "write_text", Description: "write a file", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		"mcp-wiki": {
			{Name: "wiki_search", Description: "search", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}}
	cat, err := BuildCatalog(context.Background(), lister, func(w string) bool { return Classify(w) == ClassRead }, nil)
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	return cat
}

func textResult(s string) *toolexec.ExecResult {
	return &toolexec.ExecResult{Content: []toolexec.ResultContent{{Type: "text", Text: s}}}
}

// ---------- catalog tests ----------

func TestBuildCatalogReadFilter(t *testing.T) {
	cat := readCatalog(t)
	if len(cat.Tools()) != 2 {
		t.Fatalf("expected 2 read tools, got %d: %+v", len(cat.Tools()), cat.Tools())
	}
	if w, ok := cat.WireName("read_text"); !ok || w != "mcp-fs.read_text" {
		t.Errorf("read_text → %q,%v", w, ok)
	}
	if _, ok := cat.WireName("write_text"); ok {
		t.Errorf("write_text (change) should be filtered out of the read catalog")
	}
}

func TestBuildCatalogCollision(t *testing.T) {
	lister := fakeLister{byNS: map[string][]toolexec.ToolSchema{
		"mcp-fs":   {{Name: "search"}},
		"mcp-pkg":  {{Name: "search"}},
	}}
	if _, err := BuildCatalog(context.Background(), lister, nil, nil); err == nil {
		t.Fatal("expected collision error for duplicate bare name 'search'")
	}
}

func TestBuildCatalogSkipsMissingNamespace(t *testing.T) {
	lister := fakeLister{
		byNS: map[string][]toolexec.ToolSchema{"mcp-wiki": {{Name: "wiki_get"}}},
		err:  map[string]error{"mcp-fs": errors.New("spawn failed")},
	}
	cat, err := BuildCatalog(context.Background(), lister, nil, nil)
	if err != nil {
		t.Fatalf("a failing namespace should be skipped, not fatal: %v", err)
	}
	if _, ok := cat.WireName("wiki_get"); !ok {
		t.Error("wiki_get from the healthy namespace should be present")
	}
}

// ---------- loop tests ----------

func TestLoopReadThenFinal(t *testing.T) {
	caller := &fakeCaller{replies: []ModelReply{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "read_text", Input: json.RawMessage(`{"path":"x"}`)}}},
		{Text: "the file says hello"},
	}}
	exec := &fakeExecutor{result: textResult("hello")}
	em := &recordingEmitter{}
	loop := &Loop{Caller: caller, Executor: exec, Catalog: readCatalog(t), Emitter: em}

	if err := loop.Run(context.Background(), "read x"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(exec.executed) != 1 || exec.executed[0] != "mcp-fs.read_text" {
		t.Errorf("executed = %v, want [mcp-fs.read_text]", exec.executed)
	}
	want := []EventType{EventToolCall, EventToolRunning, EventToolResult, EventFinal}
	if got := em.types(); !equalTypes(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if em.last().Text != "the file says hello" {
		t.Errorf("final text = %q", em.last().Text)
	}
	// The tool result must have been fed back into the transcript.
	if n := len(caller.lastTrans); n < 3 {
		t.Errorf("transcript too short (%d) — tool result not fed back", n)
	}
}

func TestLoopRefusesChangeTool(t *testing.T) {
	// The model's bare name "write_text" isn't in the read catalog, so it routes
	// through the unknown-tool path; either way it must NOT execute. To exercise
	// the explicit change-refusal branch we use a catalog that includes changes.
	lister := fakeLister{byNS: map[string][]toolexec.ToolSchema{
		"mcp-fs": {{Name: "write_text", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}}
	cat, _ := BuildCatalog(context.Background(), lister, nil, nil) // nil filter → includes change tools
	caller := &fakeCaller{replies: []ModelReply{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "write_text", Input: json.RawMessage(`{"path":"x","content":"y"}`)}}},
		{Text: "ok, I won't write then"},
	}}
	exec := &fakeExecutor{result: textResult("should not run")}
	em := &recordingEmitter{}
	loop := &Loop{Caller: caller, Executor: exec, Catalog: cat, Emitter: em}

	if err := loop.Run(context.Background(), "write x"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(exec.executed) != 0 {
		t.Errorf("change tool must NOT execute in read-only mode; executed=%v", exec.executed)
	}
	var sawRefusal bool
	for _, ev := range em.events {
		if ev.Type == EventToolResult && ev.IsError && ev.Tool == "mcp-fs.write_text" {
			sawRefusal = true
		}
	}
	if !sawRefusal {
		t.Error("expected an error tool_result refusing the change tool")
	}
}

func TestLoopUnknownTool(t *testing.T) {
	caller := &fakeCaller{replies: []ModelReply{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "nonexistent_tool"}}},
		{Text: "fine"},
	}}
	exec := &fakeExecutor{result: textResult("")}
	em := &recordingEmitter{}
	loop := &Loop{Caller: caller, Executor: exec, Catalog: readCatalog(t), Emitter: em}

	if err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(exec.executed) != 0 {
		t.Errorf("unknown tool must not execute; executed=%v", exec.executed)
	}
}

func TestLoopIterationCap(t *testing.T) {
	// A model that always calls a tool must hit the cap and error out.
	caller := &fakeCaller{replies: []ModelReply{}} // default reply is final "done"...
	// ...so override with an always-tool caller:
	always := &alwaysToolCaller{}
	exec := &fakeExecutor{result: textResult("loop")}
	em := &recordingEmitter{}
	loop := &Loop{Caller: always, Executor: exec, Catalog: readCatalog(t), Emitter: em, MaxIters: 3}

	err := loop.Run(context.Background(), "spin")
	if err == nil {
		t.Fatal("expected iteration-cap error")
	}
	if always.calls != 3 {
		t.Errorf("model called %d times, want MaxIters=3", always.calls)
	}
	if em.last().Type != EventError {
		t.Errorf("last event = %v, want error", em.last().Type)
	}
	_ = caller
}

type alwaysToolCaller struct{ calls int }

func (a *alwaysToolCaller) Call(_ context.Context, _ string, _ []Turn, _ []ToolDef) (ModelReply, error) {
	a.calls++
	return ModelReply{ToolCalls: []ToolCall{{ID: "c", Name: "read_text", Input: json.RawMessage(`{}`)}}}, nil
}

func TestLoopModelError(t *testing.T) {
	em := &recordingEmitter{}
	loop := &Loop{Caller: errCaller{}, Executor: &fakeExecutor{}, Catalog: readCatalog(t), Emitter: em}
	if err := loop.Run(context.Background(), "x"); err == nil {
		t.Fatal("expected error when the model call fails")
	}
	if em.last().Type != EventError {
		t.Errorf("last event = %v, want error", em.last().Type)
	}
}

type errCaller struct{}

func (errCaller) Call(_ context.Context, _ string, _ []Turn, _ []ToolDef) (ModelReply, error) {
	return ModelReply{}, errors.New("runtime unreachable")
}

func equalTypes(a, b []EventType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
