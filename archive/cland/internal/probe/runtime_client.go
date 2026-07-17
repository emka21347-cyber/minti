package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// RuntimeCapabilities mirrors the JSON wire shape runtime-adapter publishes
// at GET /minti/capabilities. Schema-frozen with M1; if runtime-adapter ever
// adds fields we tolerate them by leaving JSON unknown.
type RuntimeCapabilities struct {
	Kind            string         `json:"kind"`
	Healthy         bool           `json:"healthy"`
	Models          []RuntimeModel `json:"models"`
	SupportsStream  bool           `json:"supports_stream"`
	RemoteAPIVendor string         `json:"remote_api_vendor,omitempty"`
}

type RuntimeModel struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Resident  bool   `json:"resident"`
}

// ResidentModels returns the names of models marked resident=true.
func (c *RuntimeCapabilities) ResidentModels() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Models))
	for _, m := range c.Models {
		if m.Resident {
			out = append(out, m.Name)
		}
	}
	return out
}

// RemoteAPIs returns the configured remote-API vendor names (e.g.
// ["anthropic"]) for use by scores.ReasoningScore. Currently runtime-adapter
// surfaces a single vendor via RemoteAPIVendor; M4.1 adds proper
// multi-vendor support.
func (c *RuntimeCapabilities) RemoteAPIs() []string {
	if c == nil || c.RemoteAPIVendor == "" {
		return nil
	}
	return []string{c.RemoteAPIVendor}
}

// RuntimeClient hits http://127.0.0.1:7780/minti/capabilities. Loopback only,
// no auth needed. Caches the response for `ttl` to avoid hammering the
// runtime adapter every advertisement tick.
type RuntimeClient struct {
	baseURL string
	ttl     time.Duration
	client  *http.Client

	mu       sync.Mutex
	cached   *RuntimeCapabilities
	cachedAt time.Time
}

// NewRuntimeClient returns a client pointed at baseURL (typically
// "http://127.0.0.1:7780"). `ttl` is how long the cached capabilities are
// considered fresh; 30 s matches the advertisement interval.
func NewRuntimeClient(baseURL string, ttl time.Duration) *RuntimeClient {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &RuntimeClient{
		baseURL: baseURL,
		ttl:     ttl,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Get returns the most recent /minti/capabilities response. Refreshes if
// the cache is empty or older than ttl. Returns the LAST successful response
// (with an error) if the current refresh fails — degrades gracefully so a
// transiently-down runtime adapter doesn't immediately zero out our
// reasoning_score.
func (c *RuntimeClient) Get(ctx context.Context) (*RuntimeCapabilities, error) {
	c.mu.Lock()
	if c.cached != nil && time.Since(c.cachedAt) < c.ttl {
		cached := *c.cached
		c.mu.Unlock()
		return &cached, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/minti/capabilities", nil)
	if err != nil {
		return c.lastWithErr(err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return c.lastWithErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.lastWithErr(fmt.Errorf("runtime: status %d", resp.StatusCode))
	}
	var caps RuntimeCapabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return c.lastWithErr(fmt.Errorf("runtime: decode: %w", err))
	}

	c.mu.Lock()
	c.cached = &caps
	c.cachedAt = time.Now()
	c.mu.Unlock()
	return &caps, nil
}

func (c *RuntimeClient) lastWithErr(err error) (*RuntimeCapabilities, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil {
		// Return the stale cache; caller can decide whether to publish it
		// or substitute defaults.
		cp := *c.cached
		return &cp, err
	}
	return nil, errors.Join(errors.New("runtime: no cached capabilities"), err)
}
