package server

// Anthropic-compatible /v1/messages — added in M3 so Claude Code (and other
// Anthropic-API clients) can drive *local* models through MINTI without code
// changes on their side.
//
// Scope for M3:
//   - String-content messages only. Multi-content-block messages and
//     tool-call content blocks are not yet translated — they are M3.5 / M4
//     work alongside cland's cross-Clan tool routing. A request that
//     contains non-string content gets a 400 with a clear reason.
//   - Top-level `system` field is honored (prefixed as a system-role message
//     in the internal request).
//   - Both non-streaming and SSE streaming.
//   - `x-api-key` and `anthropic-version` headers are accepted and ignored
//     (the request is for a *local* model — no external auth needed). This
//     keeps Claude Code happy without requiring users to invent a fake key.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/minti/runtime-adapter/internal/backend"
)

// ---------- Anthropic-shaped request / response ----------

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string OR array of content blocks
}

type anthropicMessagesRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	MaxTokens   *int               `json:"max_tokens,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicTextBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicMessagesResponse struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"` // "message"
	Role       string               `json:"role"` // "assistant"
	Content    []anthropicTextBlock `json:"content"`
	Model      string               `json:"model"`
	StopReason string               `json:"stop_reason"` // "end_turn" | "max_tokens" | "stop_sequence"
	Usage      anthropicUsage       `json:"usage"`
}

// ---------- Handler ----------

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var req anthropicMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		anthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("decode body: %v", err))
		return
	}
	if req.Model == "" {
		anthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if len(req.Messages) == 0 {
		anthropicError(w, http.StatusBadRequest, "invalid_request_error", "messages must not be empty")
		return
	}

	// Translate to internal shape. System prompt is prepended as a system-role
	// message because the internal interface keeps everything in the messages
	// list.
	internal := backend.ChatRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}
	if req.System != "" {
		internal.Messages = append(internal.Messages, backend.Message{Role: "system", Content: req.System})
	}
	for i, m := range req.Messages {
		text, err := anthropicMessageText(m)
		if err != nil {
			anthropicError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("messages[%d]: %v", i, err))
			return
		}
		internal.Messages = append(internal.Messages, backend.Message{Role: m.Role, Content: text})
	}

	if req.Stream {
		s.streamAnthropic(w, r, internal)
		return
	}

	ctx, cancel := withTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	resp, err := s.Backend.Chat(ctx, internal)
	if err != nil {
		s.handleAnthropicBackendErr(w, err)
		return
	}
	out := anthropicMessagesResponse{
		ID:         newMessageID(),
		Type:       "message",
		Role:       "assistant",
		Content:    []anthropicTextBlock{{Type: "text", Text: resp.Content}},
		Model:      resp.Model,
		StopReason: anthropicStopReason(resp.FinishReason),
		Usage: anthropicUsage{
			InputTokens:  resp.PromptTokens,
			OutputTokens: resp.CompletionTokens,
		},
	}
	jsonResponse(w, http.StatusOK, out)
}

// anthropicMessageText extracts the text body from a content field. Accepts
// the two shapes Claude Code commonly sends: a bare string or a single-element
// array of `{type:"text", text:"..."}` blocks. Mixed/tool-use content blocks
// are rejected with a clear error message (deferred to M3.5+).
func anthropicMessageText(m anthropicMessage) (string, error) {
	trimmed := strings.TrimSpace(string(m.Content))
	if len(trimmed) == 0 {
		return "", nil
	}
	// String form: "..."
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			return "", fmt.Errorf("invalid string content: %w", err)
		}
		return s, nil
	}
	// Array form: [{type:"text", text:"..."}, ...]
	if trimmed[0] == '[' {
		var blocks []map[string]any
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			return "", fmt.Errorf("invalid content blocks: %w", err)
		}
		var out strings.Builder
		for i, b := range blocks {
			t, _ := b["type"].(string)
			if t != "text" {
				return "", fmt.Errorf("content_block[%d] type %q not supported yet (M3 ships text-only; tool_use and image are M3.5+)", i, t)
			}
			text, _ := b["text"].(string)
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString(text)
		}
		return out.String(), nil
	}
	return "", fmt.Errorf("content must be a string or array of text blocks")
}

func newMessageID() string {
	return "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// anthropicStopReason maps the internal FinishReason to one of Anthropic's
// documented values. Unknown reasons fall back to "end_turn".
func anthropicStopReason(internal string) string {
	switch internal {
	case "", "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func anthropicError(w http.ResponseWriter, status int, errType, msg string) {
	jsonResponse(w, status, map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errType,
			"message": msg,
		},
	})
}

func (s *Server) handleAnthropicBackendErr(w http.ResponseWriter, err error) {
	if code := contextErrCode(err); code != 0 {
		anthropicError(w, code, "api_error", err.Error())
		return
	}
	s.Log.Error("anthropic backend error", "err", err)
	anthropicError(w, http.StatusBadGateway, "api_error", err.Error())
}

// ---------- Streaming (Anthropic SSE) ----------
//
// Wire format (per https://docs.anthropic.com/en/api/messages-streaming):
//
//   event: message_start
//   data:  {"type":"message_start","message":{...with empty content}}
//
//   event: content_block_start
//   data:  {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
//
//   event: content_block_delta
//   data:  {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"H"}}
//
//   ...repeats...
//
//   event: content_block_stop
//   data:  {"type":"content_block_stop","index":0}
//
//   event: message_delta
//   data:  {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":N}}
//
//   event: message_stop
//   data:  {"type":"message_stop"}
//
// Both event: and data: lines are needed for clients that key off event names.

type anthropicStreamWriter struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	msgID    string
	model    string
	started  bool // sent message_start + content_block_start
	finished bool // sent the closing events
	outToks  int
}

func (sw *anthropicStreamWriter) sendEvent(eventName string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(sw.w, "event: %s\ndata: %s\n\n", eventName, buf); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}

func (sw *anthropicStreamWriter) ensureStarted() error {
	if sw.started {
		return nil
	}
	sw.started = true
	if err := sw.sendEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":           sw.msgID,
			"type":         "message",
			"role":         "assistant",
			"content":      []any{},
			"model":        sw.model,
			"stop_reason":  nil,
			"usage":        map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		return err
	}
	return sw.sendEvent("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})
}

func (sw *anthropicStreamWriter) WriteChunk(c backend.StreamChunk) error {
	if err := sw.ensureStarted(); err != nil {
		return err
	}
	if c.Delta != "" {
		if err := sw.sendEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]string{"type": "text_delta", "text": c.Delta},
		}); err != nil {
			return err
		}
		sw.outToks += approxTokens(c.Delta)
	}
	if c.Done {
		if err := sw.closeStream(c); err != nil {
			return err
		}
	}
	return nil
}

func (sw *anthropicStreamWriter) closeStream(c backend.StreamChunk) error {
	if sw.finished {
		return nil
	}
	sw.finished = true
	if err := sw.sendEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	}); err != nil {
		return err
	}
	out := c.CompletionTokens
	if out == 0 {
		out = sw.outToks
	}
	if err := sw.sendEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]string{"stop_reason": anthropicStopReason(c.FinishReason)},
		"usage": map[string]int{"output_tokens": out},
	}); err != nil {
		return err
	}
	return sw.sendEvent("message_stop", map[string]any{"type": "message_stop"})
}

// Close is called by the framework when the backend finishes without a
// terminal Done chunk (rare; mostly a safety net). It synthesizes the closing
// events with a generic end_turn.
func (sw *anthropicStreamWriter) Close() error {
	if sw.finished {
		return nil
	}
	if !sw.started {
		// Backend produced no chunks at all — still need a valid response
		// envelope so the client doesn't hang.
		if err := sw.ensureStarted(); err != nil {
			return err
		}
	}
	return sw.closeStream(backend.StreamChunk{Done: true, FinishReason: "stop"})
}

// approxTokens is a cheap stand-in when the backend doesn't report
// CompletionTokens until the final chunk. ~4 chars per token rule of thumb.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n == 0 {
		return 1
	}
	return n
}

func (s *Server) streamAnthropic(w http.ResponseWriter, r *http.Request, req backend.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		anthropicError(w, http.StatusInternalServerError, "api_error", "streaming not supported by HTTP server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sw := &anthropicStreamWriter{
		w:       w,
		flusher: flusher,
		msgID:   newMessageID(),
		model:   req.Model,
	}
	if err := s.Backend.ChatStream(r.Context(), req, sw); err != nil {
		s.Log.Warn("anthropic stream backend error", "err", err)
		// Best-effort terminator so the client sees a clean shutdown.
		_ = sw.sendEvent("error", map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "api_error", "message": err.Error()},
		})
	}
	if !sw.finished {
		_ = sw.Close()
	}
}
