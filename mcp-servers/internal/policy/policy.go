// Package policy loads and merges MINTI MCP policy files.
//
// Two files are consulted, in order:
//   - /etc/minti/policy.yaml          (system defaults, installed by install.sh)
//   - ~/.minti/policy.yaml            (per-user overrides)
//
// Fields present in the user file fully replace the corresponding system fields.
// Missing files are treated as empty (defaults preserved). The most-restrictive
// defaults apply when no file is present.
//
// Schema follows docs/clan-protocol.md §7.2.
package policy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Policy struct {
	MCP MCPPolicy `yaml:"mcp"`
}

type MCPPolicy struct {
	FS    FSPolicy    `yaml:"fs"`
	Shell ShellPolicy `yaml:"shell"`
	Recon ReconPolicy `yaml:"recon"`
	Pkg   PkgPolicy   `yaml:"pkg"`
	HTTP  HTTPPolicy  `yaml:"http"`
	Wiki  WikiPolicy  `yaml:"wiki"`
}

type FSPolicy struct {
	Allow     []string `yaml:"allow"`
	Deny      []string `yaml:"deny"`
	DenyTools []string `yaml:"deny_tools"`
}

type ShellPolicy struct {
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
	DenyTools []string `yaml:"deny_tools"`
}

type ReconPolicy struct {
	AllowRemoteOrigin       bool     `yaml:"allow_remote_origin"`
	RequireLocalUserPresent bool     `yaml:"require_local_user_present"`
	AllowRawSocket          bool     `yaml:"allow_raw_socket"`
	DenyTools               []string `yaml:"deny_tools"`
}

type PkgPolicy struct {
	RequireSudo bool     `yaml:"require_sudo"`
	DenyTools   []string `yaml:"deny_tools"`
}

type HTTPPolicy struct {
	MaxBodyBytes int64    `yaml:"max_body_bytes"`
	DenyTools    []string `yaml:"deny_tools"`
}

// WikiPolicy gates the offline-Wikipedia MCP server. Knowledge access is
// inherently low-risk; the only knob is DenyTools (e.g. operator wants to
// allow wiki_search but block wiki_get to avoid huge article payloads).
type WikiPolicy struct {
	DenyTools []string `yaml:"deny_tools"`
}

// Defaults returns the most-restrictive starting policy. Used when no files are
// present.
func Defaults() *Policy {
	return &Policy{
		MCP: MCPPolicy{
			FS: FSPolicy{
				Allow: []string{"~"},
				Deny:  []string{"~/.ssh", "~/.aws", "~/.minti/audit.jsonl"},
			},
			Shell: ShellPolicy{Mode: "prompt"},
			Recon: ReconPolicy{
				AllowRemoteOrigin:       false,
				RequireLocalUserPresent: true,
				AllowRawSocket:          false,
			},
			Pkg:  PkgPolicy{RequireSudo: true},
			HTTP: HTTPPolicy{MaxBodyBytes: 1 << 20},
		},
	}
}

// SystemPath is the conventional path for the system-wide policy file.
const SystemPath = "/etc/minti/policy.yaml"

// UserPath returns ~/.minti/policy.yaml for the invoking user.
func UserPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".minti", "policy.yaml"), nil
}

// Load reads the system policy then overlays the user policy.
func Load() (*Policy, error) {
	user, err := UserPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(SystemPath, user)
}

// LoadFrom is Load with explicit paths (used in tests).
func LoadFrom(systemPath, userPath string) (*Policy, error) {
	p := Defaults()
	if err := overlay(p, systemPath); err != nil {
		return nil, fmt.Errorf("policy: load system %s: %w", systemPath, err)
	}
	if err := overlay(p, userPath); err != nil {
		return nil, fmt.Errorf("policy: load user %s: %w", userPath, err)
	}
	return p, nil
}

func overlay(p *Policy, path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(p); err != nil {
		return err
	}
	return nil
}
