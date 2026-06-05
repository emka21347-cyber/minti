package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const ollamaBase = "http://127.0.0.1:11434"

// ProbeOllamaPS queries Ollama's /api/ps for models RESIDENT IN VRAM
// right now (as opposed to just downloaded). Distinct signal from
// minti-runtime's models list: a model can be downloaded but unloaded.
//
// Returns empty slice if Ollama isn't reachable — that's a valid state
// (no model currently loaded), not an error.
func ProbeOllamaPS(ctx context.Context) ([]LoadedModel, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaBase+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil // Ollama not running → empty result, not error
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var raw struct {
		Models []struct {
			Name      string `json:"name"`
			Size      int64  `json:"size"`
			ExpiresAt string `json:"expires_at"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	out := make([]LoadedModel, 0, len(raw.Models))
	now := time.Now()
	for _, m := range raw.Models {
		lm := LoadedModel{
			Name:   m.Name,
			SizeGB: float64(m.Size) / 1024 / 1024 / 1024,
		}
		if t, err := time.Parse(time.RFC3339, m.ExpiresAt); err == nil {
			if d := t.Sub(now); d > 0 {
				lm.TTLLeft = d.Round(time.Second)
			}
		}
		out = append(out, lm)
	}
	return out, nil
}
