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

// ollamaChatRequest mirrors Ollama's /api/chat wire format.
type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []Message              `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Model           string  `json:"model"`
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	DoneReason      string  `json:"done_reason"`
	TotalDuration   int64   `json:"total_duration"`   // nanoseconds
	PromptEvalCount int     `json:"prompt_eval_count"`
	EvalCount       int     `json:"eval_count"`
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
		Messages: req.Messages,
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
	return ChatResponse{
		Model:            oresp.Model,
		Content:          oresp.Message.Content,
		PromptTokens:     oresp.PromptEvalCount,
		CompletionTokens: oresp.EvalCount,
		FinishReason:     oresp.DoneReason,
		DurationSeconds:  float64(oresp.TotalDuration) / 1e9,
	}, nil
}

func (o *Ollama) ChatStream(ctx context.Context, req ChatRequest, w StreamWriter) error {
	body, err := json.Marshal(ollamaChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
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
