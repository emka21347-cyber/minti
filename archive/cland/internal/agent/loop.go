package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/minti/cland/internal/toolexec"
)

// DefaultMaxIters caps tool-call rounds so a confused or adversarial model
// can't loop forever burning the GPU. Each iteration = one model call + the
// execution of any tools it requested.
const DefaultMaxIters = 8

// maxResultChars bounds a single tool result fed back to the model, so a large
// file read or HTTP body can't blow the context window. The model is told the
// result was truncated.
const maxResultChars = 16 * 1024

// ToolCall is a tool invocation the model emitted. Name is the BARE tool name
// (the catalog maps it to a wire name); ID correlates the eventual result.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is what we feed back for one ToolCall.
type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// Turn is one entry in the transcript handed to the model each round. A user
// turn carries either Text (the prompt) or ToolResults (outputs of the prior
// round); an assistant turn carries Text and/or ToolCalls.
type Turn struct {
	Role        string // "user" | "assistant"
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// ModelReply is one model response: optional preamble Text plus any ToolCalls.
// No ToolCalls means Text is the final answer.
type ModelReply struct {
	Text      string
	ToolCalls []ToolCall
}

// Decision is the outcome of an approval prompt for a change-class tool.
type Decision int

const (
	DecisionDeny    Decision = iota // default / fail-closed
	DecisionApprove
)

// ApprovalRequest describes a pending change-tool call awaiting Approve/Deny.
type ApprovalRequest struct {
	CallID string
	Tool   string // wire name "namespace.tool"
	Class  ToolClass
	Input  json.RawMessage
}

// Approver decides whether a change-class tool call may run. Await BLOCKS until
// the user decides or ctx is cancelled. A returned error (e.g. timeout) is
// treated as a deny — fail-closed. Implementations: a CLI stdin prompt, and (in
// M1 S4) an in-process channel resolved by the daemon's POST /agent/approve.
type Approver interface {
	Await(ctx context.Context, req ApprovalRequest) (Decision, error)
}

// ModelCaller performs one (non-streaming) model turn. The cland daemon / CLI
// implements this by POSTing the transcript + tool schemas to the runtime's
// Anthropic /v1/messages endpoint (the only runtime surface that plumbs tools
// both ways) through the local router. Tool-decision turns are non-streaming
// because Hermes emits tool_calls only on the terminal chunk.
type ModelCaller interface {
	Call(ctx context.Context, system string, transcript []Turn, tools []ToolDef) (ModelReply, error)
}

// Loop is the native Hermes-agent harness (M1). It offers the catalog's tools to
// the model, executes read tools locally via the Executor, feeds results back,
// and repeats until the model answers or the iteration cap is hit.
//
// Read tools auto-run. Change-class tools (fs.write_text, shell.exec,
// pkg.install) require Approve/Deny via the Approver before they execute; if no
// Approver is wired they are refused outright (fail-closed).
type Loop struct {
	Caller   ModelCaller
	Executor toolexec.ExecutorIface
	Catalog  *Catalog
	Emitter  Emitter
	Approver Approver // nil → change tools are refused
	System   string   // optional system prompt
	MaxIters int      // <=0 → DefaultMaxIters
	Log      *slog.Logger
}

// Run drives the loop for a single user prompt, emitting events as it goes. It
// returns an error only for terminal failures (model unreachable, iteration cap);
// per-tool failures are surfaced as error results to the model, not returned.
func (l *Loop) Run(ctx context.Context, prompt string) error {
	maxIters := l.MaxIters
	if maxIters <= 0 {
		maxIters = DefaultMaxIters
	}
	tools := l.Catalog.Tools()
	transcript := []Turn{{Role: "user", Text: prompt}}

	for iter := 0; iter < maxIters; iter++ {
		reply, err := l.Caller.Call(ctx, l.System, transcript, tools)
		if err != nil {
			l.emit(Event{Type: EventError, Text: fmt.Sprintf("model call failed: %v", err)})
			return fmt.Errorf("agent: model call failed: %w", err)
		}

		if len(reply.ToolCalls) == 0 {
			// No tools requested → this is the final answer.
			l.emit(Event{Type: EventFinal, Text: reply.Text})
			return nil
		}

		// The model wants tools. Surface any preamble text, record the assistant
		// turn, then run each call and collect results for the next round.
		if reply.Text != "" {
			l.emit(Event{Type: EventText, Text: reply.Text})
		}
		transcript = append(transcript, Turn{Role: "assistant", Text: reply.Text, ToolCalls: reply.ToolCalls})

		results := make([]ToolResult, 0, len(reply.ToolCalls))
		for _, tc := range reply.ToolCalls {
			results = append(results, l.runToolCall(ctx, tc))
		}
		transcript = append(transcript, Turn{Role: "user", ToolResults: results})
	}

	l.emit(Event{Type: EventError, Text: fmt.Sprintf("reached iteration cap (%d rounds) without a final answer", maxIters)})
	return fmt.Errorf("agent: iteration cap %d reached", maxIters)
}

// runToolCall classifies, (for reads) executes, and emits events for one call,
// returning the result to feed back to the model.
func (l *Loop) runToolCall(ctx context.Context, tc ToolCall) ToolResult {
	wire, ok := l.Catalog.WireName(tc.Name)
	if !ok {
		msg := fmt.Sprintf("unknown tool %q", tc.Name)
		l.emit(Event{Type: EventToolResult, CallID: tc.ID, Tool: tc.Name, IsError: true, Result: msg})
		return ToolResult{ToolUseID: tc.ID, Content: "ERROR: " + msg, IsError: true}
	}

	class := Classify(wire)
	l.emit(Event{Type: EventToolCall, CallID: tc.ID, Tool: wire, Class: class.String(), Input: tc.Input})

	if class == ClassChange {
		if l.Approver == nil {
			// Fail-closed: no approver wired (e.g. a read-only session) → refuse.
			msg := "tool requires approval but this session has no approver (read-only mode)"
			l.emit(Event{Type: EventToolResult, CallID: tc.ID, Tool: wire, IsError: true, Result: msg})
			return ToolResult{ToolUseID: tc.ID, Content: "ERROR: " + msg, IsError: true}
		}
		l.emit(Event{Type: EventApprovalRequired, CallID: tc.ID, Tool: wire, Class: class.String(), Input: tc.Input})
		dec, err := l.Approver.Await(ctx, ApprovalRequest{CallID: tc.ID, Tool: wire, Class: class, Input: tc.Input})
		if err != nil || dec != DecisionApprove {
			reason := "denied by user"
			if err != nil {
				reason = fmt.Sprintf("approval failed: %v", err) // timeout / ctx cancel → fail-closed
			}
			l.emit(Event{Type: EventToolResult, CallID: tc.ID, Tool: wire, IsError: true, Result: reason})
			return ToolResult{ToolUseID: tc.ID, Content: "DENIED: " + reason, IsError: true}
		}
		// approved → fall through and execute like any other tool
	}

	l.emit(Event{Type: EventToolRunning, CallID: tc.ID, Tool: wire})

	var args map[string]any
	if len(tc.Input) > 0 {
		if err := json.Unmarshal(tc.Input, &args); err != nil {
			msg := fmt.Sprintf("invalid tool arguments: %v", err)
			l.emit(Event{Type: EventToolResult, CallID: tc.ID, Tool: wire, IsError: true, Result: msg})
			return ToolResult{ToolUseID: tc.ID, Content: "ERROR: " + msg, IsError: true}
		}
	}

	res, _, _, err := l.Executor.Execute(ctx, wire, args)
	if err != nil {
		msg := fmt.Sprintf("tool execution failed: %v", err)
		l.emit(Event{Type: EventToolResult, CallID: tc.ID, Tool: wire, IsError: true, Result: msg})
		return ToolResult{ToolUseID: tc.ID, Content: "ERROR: " + msg, IsError: true}
	}

	text, truncated := flattenResult(res)
	l.emit(Event{Type: EventToolResult, CallID: tc.ID, Tool: wire, IsError: res.IsError, Result: text})
	if truncated {
		text += "\n[...result truncated...]"
	}
	return ToolResult{ToolUseID: tc.ID, Content: text, IsError: res.IsError}
}

// flattenResult renders an ExecResult's content blocks to a single string for
// the model, capped at maxResultChars. Returns (text, wasTruncated).
func flattenResult(res *toolexec.ExecResult) (string, bool) {
	var b strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		} else if len(c.JSON) > 0 {
			b.Write(c.JSON)
		}
		b.WriteByte('\n')
	}
	s := strings.TrimRight(b.String(), "\n")
	if len(s) > maxResultChars {
		return s[:maxResultChars], true
	}
	return s, false
}

func (l *Loop) emit(ev Event) {
	if l.Emitter == nil {
		return
	}
	if err := l.Emitter.Emit(ev); err != nil && l.Log != nil {
		l.Log.Warn("agent: emit failed", "type", ev.Type, "err", err)
	}
}
