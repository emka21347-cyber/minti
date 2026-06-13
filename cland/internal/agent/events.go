package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// EventType is the discriminator for an agent stream event. Plain chat (no
// tools) produces only text + final; the others appear when the model uses
// tools. This is the NDJSON protocol the workspace relay + SPA render in M1 S4.
type EventType string

const (
	EventText        EventType = "text"              // model preamble / assistant text
	EventToolCall    EventType = "tool_call"         // model requested a tool (classified)
	EventToolRunning EventType = "tool_running"      // a read tool started executing
	EventToolResult  EventType = "tool_result"       // a tool returned (or was refused)
	EventFinal       EventType = "final"             // the model's final answer
	EventError       EventType = "error"             // the loop failed
)

// Event is one item in the agent stream. Fields are populated per Type; unused
// fields are omitted from the wire form.
type Event struct {
	Type    EventType       `json:"type"`
	Text    string          `json:"text,omitempty"`     // text / final / error message
	CallID  string          `json:"call_id,omitempty"`  // correlates tool_call→running→result
	Tool    string          `json:"tool,omitempty"`     // wire name "namespace.tool"
	Class   string          `json:"class,omitempty"`    // "read" | "change"
	Input   json.RawMessage `json:"input,omitempty"`    // tool arguments
	Result  string          `json:"result,omitempty"`   // tool output (possibly truncated)
	IsError bool            `json:"is_error,omitempty"` // tool/result error flag
}

// Emitter receives loop events. Implementations: a console renderer (the CLI),
// and NDJSONEmitter (the HTTP relay path in M1 S4). Emit must be safe to call
// from the single loop goroutine; it need not be concurrent-safe.
type Emitter interface {
	Emit(Event) error
}

// NDJSONEmitter writes one JSON object per line and flushes after each, so a
// streaming HTTP client sees events as they happen. Used by the agent HTTP
// handler (M1 S4) and by tests.
type NDJSONEmitter struct {
	mu      sync.Mutex
	w       io.Writer
	flusher http.Flusher // optional; flushed after each event when present
}

// NewNDJSONEmitter wraps w. If w is also an http.Flusher it is flushed per event.
func NewNDJSONEmitter(w io.Writer) *NDJSONEmitter {
	e := &NDJSONEmitter{w: w}
	if f, ok := w.(http.Flusher); ok {
		e.flusher = f
	}
	return e
}

func (e *NDJSONEmitter) Emit(ev Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := e.w.Write(append(b, '\n')); err != nil {
		return err
	}
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return nil
}
