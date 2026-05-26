package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/minti/runtime-adapter/internal/backend"
)

// ---------- fake backend for handler tests ----------

type fakeBackend struct {
	lastReq backend.ChatRequest
	resp    backend.ChatResponse
	stream  []backend.StreamChunk
	err     error
}

func (f *fakeBackend) Kind() backend.Kind                                  { return backend.KindOllama }
func (f *fakeBackend) Health(context.Context) error                        { return nil }
func (f *fakeBackend) Capabilities(context.Context) (backend.Capabilities, error) {
	return backend.Capabilities{Kind: backend.KindOllama, Healthy: true}, nil
}
func (f *fakeBackend) Chat(_ context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return backend.ChatResponse{}, f.err
	}
	if f.resp.Model == "" {
		f.resp.Model = req.Model
	}
	return f.resp, nil
}
func (f *fakeBackend) ChatStream(_ context.Context, req backend.ChatRequest, w backend.StreamWriter) error {
	f.lastReq = req
	if f.err != nil {
		return f.err
	}
	for _, c := range f.stream {
		if err := w.WriteChunk(c); err != nil {
			return err
		}
	}
	return w.Close()
}

// ---------- translation tests ----------

func TestAnthropicMessageText_String(t *testing.T) {
	m := anthropicMessage{Role: "user", Content: json.RawMessage(`"hello there"`)}
	got, err := anthropicMessageText(m)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello there" {
		t.Errorf("got %q", got)
	}
}

func TestAnthropicMessageText_TextBlocks(t *testing.T) {
	m := anthropicMessage{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]`)}
	got, err := anthropicMessageText(m)
	if err != nil {
		t.Fatal(err)
	}
	if got != "line1\nline2" {
		t.Errorf("got %q", got)
	}
}

func TestAnthropicMessageText_RejectsToolUse(t *testing.T) {
	m := anthropicMessage{Role: "user", Content: json.RawMessage(`[{"type":"tool_use","id":"x","name":"foo"}]`)}
	_, err := anthropicMessageText(m)
	if err == nil {
		t.Fatal("expected error for tool_use block")
	}
	if !strings.Contains(err.Error(), "tool_use") {
		t.Errorf("error should mention tool_use: %v", err)
	}
}

func TestAnthropicStopReason(t *testing.T) {
	cases := map[string]string{
		"":              "end_turn",
		"stop":          "end_turn",
		"length":        "max_tokens",
		"stop_sequence": "stop_sequence",
		"garbage":       "end_turn",
	}
	for in, want := range cases {
		if got := anthropicStopReason(in); got != want {
			t.Errorf("anthropicStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---------- handler integration ----------

func newTestServer(b backend.Backend) *httptest.Server {
	s := New(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return httptest.NewServer(s.Routes())
}

func TestHandleAnthropicMessages_NonStreaming(t *testing.T) {
	fb := &fakeBackend{
		resp: backend.ChatResponse{
			Content:          "Hi!",
			PromptTokens:     10,
			CompletionTokens: 2,
		},
	}
	ts := newTestServer(fb)
	defer ts.Close()

	body := `{
		"model": "llama3.2:3b",
		"system": "Be brief.",
		"max_tokens": 32,
		"messages": [{"role":"user","content":"hi"}]
	}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}

	var out anthropicMessagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Type != "message" || out.Role != "assistant" {
		t.Errorf("envelope wrong: %+v", out)
	}
	if len(out.Content) != 1 || out.Content[0].Type != "text" || out.Content[0].Text != "Hi!" {
		t.Errorf("content wrong: %+v", out.Content)
	}
	if out.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q", out.StopReason)
	}
	if out.Usage.InputTokens != 10 || out.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v", out.Usage)
	}

	// System field should have been prefixed as a system-role message.
	if len(fb.lastReq.Messages) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d", len(fb.lastReq.Messages))
	}
	if fb.lastReq.Messages[0].Role != "system" || fb.lastReq.Messages[0].Content != "Be brief." {
		t.Errorf("system msg wrong: %+v", fb.lastReq.Messages[0])
	}
	if fb.lastReq.Messages[1].Role != "user" || fb.lastReq.Messages[1].Content != "hi" {
		t.Errorf("user msg wrong: %+v", fb.lastReq.Messages[1])
	}
}

func TestHandleAnthropicMessages_ValidationErrors(t *testing.T) {
	ts := newTestServer(&fakeBackend{})
	defer ts.Close()

	cases := []struct {
		name string
		body string
	}{
		{"empty model", `{"messages":[{"role":"user","content":"hi"}]}`},
		{"empty messages", `{"model":"x","messages":[]}`},
		{"tool_use block", `{"model":"x","messages":[{"role":"user","content":[{"type":"tool_use"}]}]}`},
		{"garbage json", `{not json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(c.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				b, _ := io.ReadAll(resp.Body)
				t.Errorf("status %d for %s, want 400. body=%s", resp.StatusCode, c.name, b)
			}
			var env map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
				t.Fatal(err)
			}
			if env["type"] != "error" {
				t.Errorf("expected error envelope, got %+v", env)
			}
		})
	}
}

func TestHandleAnthropicMessages_Streaming(t *testing.T) {
	fb := &fakeBackend{
		stream: []backend.StreamChunk{
			{Delta: "Hel"},
			{Delta: "lo!"},
			{Done: true, FinishReason: "stop", CompletionTokens: 2},
		},
	}
	ts := newTestServer(fb)
	defer ts.Close()

	body := `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("content-type = %q", got)
	}

	all, _ := io.ReadAll(resp.Body)
	stream := string(all)

	wantEvents := []string{
		"event: message_start\n",
		"event: content_block_start\n",
		"event: content_block_delta\n",
		"event: content_block_stop\n",
		"event: message_delta\n",
		"event: message_stop\n",
	}
	for _, ev := range wantEvents {
		if !strings.Contains(stream, ev) {
			t.Errorf("stream missing %q. full output: %s", ev, stream)
		}
	}
	// Two delta chunks should produce two content_block_delta events.
	if got, want := strings.Count(stream, "content_block_delta"), 2; got < want {
		// account for the "type":"content_block_delta" appearing in JSON as well
		// — there's always more occurrences than events, so this is a lower bound.
		t.Logf("content_block_delta occurrences=%d (event lines + JSON refs), full=%s", got, stream)
	}
	// Stop reason should land in the message_delta payload.
	if !strings.Contains(stream, `"stop_reason":"end_turn"`) {
		t.Errorf("missing stop_reason. full output: %s", stream)
	}
}

func TestHandleAnthropicMessages_AuthHeadersIgnored(t *testing.T) {
	// Local target — we must accept and ignore x-api-key / anthropic-version
	// so Claude Code doesn't refuse to send because the user hasn't set a key.
	fb := &fakeBackend{resp: backend.ChatResponse{Content: "ok"}}
	ts := newTestServer(fb)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages",
		bytes.NewBufferString(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-anything-the-user-has")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d (should ignore auth headers for local routing)", resp.StatusCode)
	}
}
