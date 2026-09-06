// Package harness reads opencode + (optionally) Claude Code config files
// to report which agent harness is wired up on this host.
package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// OpencodeConfig is the subset of ~/.config/opencode/opencode.json we
// surface in the Harness panel.
type OpencodeConfig struct {
	Configured   bool
	Path         string
	Provider     string // first provider name
	DefaultModel string
	MCPNames     []string // sorted, e.g. ["fs","http","pkg","recon","shell","wiki"]
}

// ClaudeConfig is the subset of ~/.claude/settings.json we surface.
type ClaudeConfig struct {
	Configured bool
	Path       string
	MCPCount   int
}

// ProbeOpencode tries the per-user config first, falls back to the
// system-wide example. Never errors — returns Configured=false when
// nothing is on disk.
func ProbeOpencode(ctx context.Context) OpencodeConfig {
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), ".config", "opencode", "opencode.json"),
		"/etc/minti/opencode.config.example.json",
	}
	for _, p := range candidates {
		if cfg, ok := readOpencode(p); ok {
			return cfg
		}
	}
	return OpencodeConfig{}
}

// ProbeClaude looks for ~/.claude/settings.json (Claude Code preset).
func ProbeClaude(ctx context.Context) ClaudeConfig {
	p := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return ClaudeConfig{}
	}
	var raw struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(b, &raw)
	return ClaudeConfig{
		Configured: true,
		Path:       p,
		MCPCount:   len(raw.MCPServers),
	}
}

func readOpencode(p string) (OpencodeConfig, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return OpencodeConfig{}, false
	}
	var raw struct {
		Provider map[string]struct {
			Models map[string]any `json:"models"`
		} `json:"provider"`
		Mcp map[string]struct {
			Type    string `json:"type"`
			Enabled bool   `json:"enabled"`
		} `json:"mcp"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return OpencodeConfig{}, false
	}

	cfg := OpencodeConfig{Configured: true, Path: p, DefaultModel: raw.Model}
	for k := range raw.Provider {
		cfg.Provider = k
		break
	}
	for name, m := range raw.Mcp {
		if m.Enabled {
			short := name
			const prefix = "minti-mcp-"
			if len(short) > len(prefix) && short[:len(prefix)] == prefix {
				short = short[len(prefix):]
			}
			cfg.MCPNames = append(cfg.MCPNames, short)
		}
	}
	sort.Strings(cfg.MCPNames)
	return cfg, true
}
