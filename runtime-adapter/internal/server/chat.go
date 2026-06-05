package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/minti/runtime-adapter/internal/backend"
)

// ---------- OpenAI-shaped request/response ----------

type openAIChatRequest struct {
	Model       string             `json:"model"`
	Messages    []backend.Message  `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	MaxTokens   *int               `json:"max_tokens,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type openAIChoice struct {
	Index        int             `json:"index"`
	Message      backend.Message `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

// SSE delta envelope used in streaming mode.
type openAIStreamChoice struct {
	Index        int                 `json:"index"`
	Delta        openAIStreamDelta   `json:"delta"`
	FinishReason *string             `json:"finish_reason"`
}

type openAIStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIStreamEvent struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
}

func newCompletionID() string {
	return "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// ---------- OpenAI handler ----------

func (s *Server) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	var req openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("decode body: %v", err))
		return
	}
	if len(req.Messages) == 0 {
		httpError(w, http.StatusBadRequest, "messages must not be empty")
		return
	}
	// Default-model fallback: empty `model` resolves to hermes3:8b → mistral:7b →
	// first available. See resolveModel for the preference list.
	resolved, err := s.resolveModel(r.Context(), req.Model)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	if resolved == "" {
		httpError(w, http.StatusBadRequest, "model is required (no models pulled — run `ollama pull hermes3:8b` or install minti-pack-hermes3)")
		return
	}
	req.Model = resolved

	internal := backend.ChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}

	if req.Stream {
		s.streamOpenAI(w, r, internal)
		return
	}

	ctx, cancel := withTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	resp, err := s.Backend.Chat(ctx, internal)
	if err != nil {
		s.handleBackendErr(w, err)
		return
	}
	out := openAIChatResponse{
		ID:      newCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: []openAIChoice{{
			Index:        0,
			Message:      backend.Message{Role: "assistant", Content: resp.Content},
			FinishReason: defaultIfEmpty(resp.FinishReason, "stop"),
		}},
		Usage: openAIUsage{
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
			TotalTokens:      resp.PromptTokens + resp.CompletionTokens,
		},
	}
	jsonResponse(w, http.StatusOK, out)
}

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// openAIStreamWriter translates internal StreamChunks into SSE events.
type openAIStreamWriter struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	id       string
	model    string
	role     string // sent once on first chunk
	finished bool
}

func (sw *openAIStreamWriter) WriteChunk(c backend.StreamChunk) error {
	ev := openAIStreamEvent{
		ID:      sw.id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   sw.model,
		Choices: []openAIStreamChoice{{Index: 0}},
	}
	if sw.role == "" {
		ev.Choices[0].Delta.Role = "assistant"
		sw.role = "assistant"
	}
	if c.Delta != "" {
		ev.Choices[0].Delta.Content = c.Delta
	}
	if c.Done {
		reason := defaultIfEmpty(c.FinishReason, "stop")
		ev.Choices[0].FinishReason = &reason
		sw.finished = true
	}
	buf, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(sw.w, "data: %s\n\n", buf); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}

func (sw *openAIStreamWriter) Close() error {
	if _, err := fmt.Fprint(sw.w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}

func (s *Server) streamOpenAI(w http.ResponseWriter, r *http.Request, req backend.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported by HTTP server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sw := &openAIStreamWriter{w: w, flusher: flusher, id: newCompletionID(), model: req.Model}
	ctx := r.Context()
	if err := s.Backend.ChatStream(ctx, req, sw); err != nil {
		// Best-effort: write a final SSE event carrying the error before closing.
		s.Log.Warn("stream backend error", "err", err)
		errEnv := map[string]interface{}{"error": map[string]string{"message": err.Error()}}
		buf, _ := json.Marshal(errEnv)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}
	if !sw.finished {
		_ = sw.Close()
	}
}

// ---------- Ollama-shaped pass-through ----------

type ollamaChatPassthrough struct {
	Model    string             `json:"model"`
	Messages []backend.Message  `json:"messages"`
	Stream   *bool              `json:"stream,omitempty"`
	Options  map[string]any     `json:"options,omitempty"`
}

func (s *Server) handleOllamaChat(w http.ResponseWriter, r *http.Request) {
	var req ollamaChatPassthrough
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Model == "" || len(req.Messages) == 0 {
		httpError(w, http.StatusBadRequest, "model and messages are required")
		return
	}
	internal := backend.ChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
	}
	if t, ok := req.Options["temperature"].(float64); ok {
		internal.Temperature = &t
	}
	if t, ok := req.Options["top_p"].(float64); ok {
		internal.TopP = &t
	}
	if n, ok := req.Options["num_predict"].(float64); ok {
		nn := int(n)
		internal.MaxTokens = &nn
	}
	// Ollama defaults to stream=true if unset.
	streaming := true
	if req.Stream != nil {
		streaming = *req.Stream
	}
	internal.Stream = streaming

	if streaming {
		s.streamOllama(w, r, internal)
		return
	}

	ctx, cancel := withTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	resp, err := s.Backend.Chat(ctx, internal)
	if err != nil {
		s.handleBackendErr(w, err)
		return
	}
	out := map[string]interface{}{
		"model":             resp.Model,
		"message":           map[string]string{"role": "assistant", "content": resp.Content},
		"done":              true,
		"done_reason":       defaultIfEmpty(resp.FinishReason, "stop"),
		"prompt_eval_count": resp.PromptTokens,
		"eval_count":        resp.CompletionTokens,
		"total_duration":    int64(resp.DurationSeconds * 1e9),
	}
	jsonResponse(w, http.StatusOK, out)
}

// ollamaStreamWriter emits NDJSON matching Ollama's native stream shape so
// clients that expect /api/chat streaming see it unchanged.
type ollamaStreamWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	model   string
}

func (sw *ollamaStreamWriter) WriteChunk(c backend.StreamChunk) error {
	out := map[string]interface{}{
		"model":   sw.model,
		"message": map[string]string{"role": "assistant", "content": c.Delta},
		"done":    c.Done,
	}
	if c.Done {
		out["done_reason"] = defaultIfEmpty(c.FinishReason, "stop")
		out["prompt_eval_count"] = c.PromptTokens
		out["eval_count"] = c.CompletionTokens
		out["total_duration"] = int64(c.DurationSeconds * 1e9)
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if _, err := sw.w.Write(append(buf, '\n')); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}

func (sw *ollamaStreamWriter) Close() error { return nil }

func (s *Server) streamOllama(w http.ResponseWriter, r *http.Request, req backend.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	sw := &ollamaStreamWriter{w: w, flusher: flusher, model: req.Model}
	if err := s.Backend.ChatStream(r.Context(), req, sw); err != nil {
		s.Log.Warn("ollama stream error", "err", err)
	}
}

// ---------- error mapping ----------

func (s *Server) handleBackendErr(w http.ResponseWriter, err error) {
	if errors.Is(err, backend.ErrNotImplemented) {
		httpError(w, http.StatusNotImplemented, err.Error())
		return
	}
	if ctxErr := contextErrCode(err); ctxErr != 0 {
		httpError(w, ctxErr, err.Error())
		return
	}
	s.Log.Error("backend error", "err", err)
	httpError(w, http.StatusBadGateway, err.Error())
}

func contextErrCode(err error) int {
	switch {
	case errors.Is(err, context.Canceled):
		return 499 // client closed request
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	}
	return 0
}
