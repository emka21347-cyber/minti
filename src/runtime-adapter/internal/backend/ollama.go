package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Ollama is the local-Ollama backend. It talks to ollama serve over its
// HTTP API (/api/chat, /api/tags). Default base URL is the Ollama default.
type Ollama struct {
	BaseURL string // e.g. "http://127.0.0.1:11434"
	HTTP    *http.Client
}

// NewOllama returns an Ollama backend with sensible defaults.
func NewOllama(baseURL string) *Ollama {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	return &Ollama{
		BaseURL: baseURL,
		HTTP: &http.Client{
			Timeout: 30 * time.Minute, // long-running completions
		},
	}
}

func (o *Ollama) Kind() Kind { return KindOllama }

func (o *Ollama) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	// Short timeout for health.
	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", o.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama health returned %d", resp.StatusCode)
	}
	return nil
}

func (o *Ollama) Capabilities(ctx context.Context) (Capabilities, error) {
	caps := Capabilities{
		Kind:           KindOllama,
		SupportsStream: true,
	}
	if err := o.Health(ctx); err != nil {
		caps.Healthy = false
		return caps, nil // surface unhealthy state, don't error
	}
	caps.Healthy = true

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return caps, err
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return caps, err
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return caps, err
	}
	for _, m := range tags.Models {
		caps.Models = append(caps.Models, ModelInfo{
			Name:      m.Name,
			SizeBytes: m.Size,
			Resident:  true,
		})
	}
	return caps, nil
}

// ollamaMessage mirrors Ollama's message shape, including tool_calls.
type ollamaMessage struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolCalls []ollamaToolCall  `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ollamaTool mirrors Ollama's tool definition shape.
type ollamaTool struct {
	Type     string          `json:"type"` // always "function"
	Function ollamaToolFunc  `json:"function"`
}

type ollamaToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

// ollamaChatRequest mirrors Ollama's /api/chat wire format.
type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Tools    []ollamaTool           `json:"tools,omitempty"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Model           string        `json:"model"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason"`
	TotalDuration   int64         `json:"total_duration"`   // nanoseconds
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

// toOllamaMessages converts internal Messages to Ollama wire format.
// role:"tool" messages (tool results) are emitted as role:"tool" with content only.
func toOllamaMessages(msgs []Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(msgs))
	for _, m := range msgs {
		om := ollamaMessage{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, ollamaToolCall{
				Function: ollamaToolCallFunction{Name: tc.Name, Arguments: tc.Input},
			})
		}
		out = append(out, om)
	}
	return out
}

// toOllamaTools converts internal Tool slice to Ollama wire format.
func toOllamaTools(tools []Tool) []ollamaTool {
	out := make([]ollamaTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, ollamaTool{
			Type: "function",
			Function: ollamaToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// fromOllamaToolCalls converts Ollama tool_calls to internal ToolCall slice.
// IDs are synthesised as "<name>_<index>" since Ollama doesn't emit them.
func fromOllamaToolCalls(calls []ollamaToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for i, tc := range calls {
		id := fmt.Sprintf("%s_%d", tc.Function.Name, i)
		out = append(out, ToolCall{
			ID:    id,
			Name:  tc.Function.Name,
			Input: tc.Function.Arguments,
		})
	}
	return out
}

func buildOptions(req ChatRequest) map[string]interface{} {
	opts := map[string]interface{}{}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		opts["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		opts["num_predict"] = *req.MaxTokens
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

func (o *Ollama) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: toOllamaMessages(req.Messages),
		Tools:    toOllamaTools(req.Tools),
		Stream:   false,
		Options:  buildOptions(req),
	})
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("ollama chat call failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(b))
	}
	var oresp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&oresp); err != nil {
		return ChatResponse{}, err
	}
	cr := ChatResponse{
		Model:            oresp.Model,
		Content:          oresp.Message.Content,
		PromptTokens:     oresp.PromptEvalCount,
		CompletionTokens: oresp.EvalCount,
		FinishReason:     oresp.DoneReason,
		DurationSeconds:  float64(oresp.TotalDuration) / 1e9,
	}
	if len(oresp.Message.ToolCalls) > 0 {
		cr.ToolCalls = fromOllamaToolCalls(oresp.Message.ToolCalls)
		cr.FinishReason = "tool_use"
	}
	return cr, nil
}

func (o *Ollama) ChatStream(ctx context.Context, req ChatRequest, w StreamWriter) error {
	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: toOllamaMessages(req.Messages),
		Tools:    toOllamaTools(req.Tools),
		Stream:   true,
		Options:  buildOptions(req),
	})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama stream call failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Ollama can emit large JSON lines; bump scanner buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var oresp ollamaChatResponse
		if err := json.Unmarshal(line, &oresp); err != nil {
			return fmt.Errorf("decode ollama stream chunk: %w", err)
		}
		chunk := StreamChunk{
			Delta: oresp.Message.Content,
			Done:  oresp.Done,
		}
		if oresp.Done {
			chunk.FinishReason = oresp.DoneReason
			chunk.PromptTokens = oresp.PromptEvalCount
			chunk.CompletionTokens = oresp.EvalCount
			chunk.DurationSeconds = float64(oresp.TotalDuration) / 1e9
			if len(oresp.Message.ToolCalls) > 0 {
				chunk.ToolCalls = fromOllamaToolCalls(oresp.Message.ToolCalls)
				chunk.FinishReason = "tool_use"
			}
		}
		if err := w.WriteChunk(chunk); err != nil {
			return err
		}
		if oresp.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return w.Close()
}
