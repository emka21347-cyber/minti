// Package backend defines the uniform interface that minti-runtime presents
// to higher layers (agent clients, cland). Concrete backends (Ollama,
// llama.cpp-server, LocalAI, remote APIs) implement this interface so the
// rest of the system never depends on a specific runtime.
package backend

import (
	"context"
	"encoding/json"
	"io"
)

// Kind identifies which concrete backend is in use.
type Kind string

const (
	KindOllama    Kind = "ollama"
	KindLlamaCpp  Kind = "llamacpp-server"
	KindLocalAI   Kind = "localai"
	KindRemoteAPI Kind = "remote-api"
)

// Tool describes a function the model may call.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"` // JSON Schema object
}

// ToolCall is a single tool invocation emitted by the model.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"` // raw JSON object
}

// Message is a single chat turn. Mirrors OpenAI/Ollama shape so we don't
// re-translate between layers.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // for role:"tool" result messages
}

// ChatRequest is the normalised request the runtime accepts internally.
// Callers pass this in; backends translate to their wire format.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

// ChatResponse is the normalised non-streaming response.
type ChatResponse struct {
	Model            string     `json:"model"`
	Content          string     `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	PromptTokens     int        `json:"prompt_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
	FinishReason     string     `json:"finish_reason"`
	DurationSeconds  float64    `json:"duration_seconds"`
}

// StreamChunk is one event in a streaming response. The final chunk has
// Done=true and may carry usage stats; intermediate chunks carry Delta only.
// ToolCallDelta carries incremental tool-call JSON when the model is streaming
// a tool invocation (rare in Ollama, but handled for completeness).
type StreamChunk struct {
	Delta            string     `json:"delta"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"` // set on final chunk if model called tools
	Done             bool       `json:"done"`
	FinishReason     string     `json:"finish_reason,omitempty"`
	PromptTokens     int        `json:"prompt_tokens,omitempty"`
	CompletionTokens int        `json:"completion_tokens,omitempty"`
	DurationSeconds  float64    `json:"duration_seconds,omitempty"`
}

// ModelInfo describes a model resident or available on a backend.
type ModelInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Resident  bool   `json:"resident"`
}

// Capabilities is what a backend advertises about itself. The runtime
// aggregates these for the /minti/capabilities endpoint.
type Capabilities struct {
	Kind            Kind        `json:"kind"`
	Healthy         bool        `json:"healthy"`
	Models          []ModelInfo `json:"models"`
	SupportsStream  bool        `json:"supports_stream"`
	RemoteAPIVendor string      `json:"remote_api_vendor,omitempty"`
}

// Backend is the interface every concrete runtime must satisfy.
//
// Implementations must be safe for concurrent use by multiple goroutines.
// Chat blocks until the response is complete; ChatStream writes one
// StreamChunk per event and closes w when done.
type Backend interface {
	Kind() Kind
	Health(ctx context.Context) error
	Capabilities(ctx context.Context) (Capabilities, error)
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest, w StreamWriter) error
}

// StreamWriter is the sink the backend writes streaming chunks to.
// Implementations of Backend should call Write per chunk and Close once.
type StreamWriter interface {
	io.Closer
	WriteChunk(c StreamChunk) error
}
