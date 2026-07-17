// Package runtime probes minti-runtime + Ollama via loopback HTTP.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const (
	defaultBase = "http://127.0.0.1:7780"
	httpTimeout = 1500 * time.Millisecond
)

// Status is the runtime adapter snapshot rendered by the Runtime panel.
type Status struct {
	Healthy  bool
	Version  string
	Backend  string // "ollama" etc.
	Endpoint string
	Models   []Model
}

// Model is one entry in the adapter's /minti/capabilities models list.
type Model struct {
	Name           string
	SizeBytes      int64
	Resident       bool
	ReasoningScore int
}

// LoadedModel is one entry from Ollama's /api/ps response: a model that
// is currently RESIDENT IN VRAM (vs just downloaded to disk).
type LoadedModel struct {
	Name    string
	SizeGB  float64
	TTLLeft time.Duration
}

// Probe hits minti-runtime's three info endpoints and combines the
// result. Loopback only; no auth. ~1-2 round trips, <50 ms healthy.
func Probe(ctx context.Context) (Status, error) {
	base := defaultBase
	client := &http.Client{Timeout: httpTimeout}

	st := Status{Endpoint: base}

	// /minti/health → just status:ok / fail.
	if ok, err := getOK(ctx, client, base+"/minti/health"); err != nil {
		return st, err
	} else {
		st.Healthy = ok
	}

	// /minti/version → {runtime, version}.
	var ver struct {
		Runtime string `json:"runtime"`
		Version string `json:"version"`
	}
	if err := getJSON(ctx, client, base+"/minti/version", &ver); err == nil {
		st.Version = ver.Version
	}

	// /minti/capabilities → {kind, healthy, models[]}.
	var caps struct {
		Kind   string `json:"kind"`
		Models []struct {
			Name           string `json:"name"`
			SizeBytes      int64  `json:"size_bytes"`
			Resident       bool   `json:"resident"`
			ReasoningScore int    `json:"reasoning_score"`
		} `json:"models"`
	}
	if err := getJSON(ctx, client, base+"/minti/capabilities", &caps); err == nil {
		st.Backend = caps.Kind
		for _, m := range caps.Models {
			st.Models = append(st.Models, Model{
				Name:           m.Name,
				SizeBytes:      m.SizeBytes,
				Resident:       m.Resident,
				ReasoningScore: m.ReasoningScore,
			})
		}
	}

	return st, nil
}

// getOK returns true iff GET <url> returns 200 with {"status":"ok"} body.
func getOK(ctx context.Context, c *http.Client, url string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, nil // no error type — just "not running"
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
