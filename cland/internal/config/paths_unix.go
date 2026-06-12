//go:build linux || darwin

// Platform-canonical paths for Linux + macOS. Branches on runtime.GOOS so the
// same file serves both; keeps the helper surface small.
//
// Resolution rules:
//   - "system" install (euid == 0): writes to /var/lib, /etc, /opt etc on Linux;
//     /Library/Application Support/MINTI on macOS.
//   - "user" install (euid != 0): writes under $HOME — ~/.minti on Linux,
//     ~/Library/Application Support/MINTI on macOS.
//
// macOS uid 0 detection: when launchd runs cland as UserName=_minti, the
// process is NOT euid 0 — the installer sets up the canonical state dir +
// passes --config explicitly, so the daemon never reaches these defaults in
// service-context. These helpers are the *fallback* when no config is given.

package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultStateDir returns the canonical state dir for the current platform +
// invocation context. Holds clan.json, identity.json, audit.jsonl by default.
func DefaultStateDir() string {
	if runtime.GOOS == "darwin" {
		if os.Geteuid() == 0 {
			return "/Library/Application Support/MINTI/cland"
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "MINTI", "cland")
		}
		return "/Library/Application Support/MINTI/cland"
	}
	// linux
	if os.Geteuid() == 0 {
		return "/var/lib/minti/cland"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".minti", "cland")
	}
	return "/var/lib/minti/cland"
}

// DefaultAuditLogPath returns <state_dir>/audit.jsonl. Per M5-A plan we drop
// the previous ~/.minti/audit.jsonl convention so the audit log lives next to
// the rest of cland's state — eliminates the `Environment=HOME=...` shim the
// Linux systemd unit needed (M4 Phase I) and works under macOS launchd where
// the service user (_minti) has HOME=/var/empty.
func DefaultAuditLogPath() string {
	return filepath.Join(DefaultStateDir(), "audit.jsonl")
}

// DefaultConfigPath returns the canonical config file path.
//
// MINTI_CONFIG overrides everything: a service whose account/HOME doesn't map
// to the canonical path (e.g. the workspace unit shelling the cland CLI as a
// non-root service user) sets it once in its unit so every shell-out resolves
// the right config without threading --config through each call.
func DefaultConfigPath() string {
	if v := os.Getenv("MINTI_CONFIG"); v != "" {
		return v
	}
	if runtime.GOOS == "darwin" {
		if os.Geteuid() == 0 {
			return "/Library/Application Support/MINTI/cland.yaml"
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "MINTI", "cland.yaml")
		}
		return "/Library/Application Support/MINTI/cland.yaml"
	}
	// linux
	if os.Geteuid() == 0 {
		return "/etc/minti/cland.yaml"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "minti", "cland.yaml")
	}
	return "/etc/minti/cland.yaml"
}

// DefaultRubricPath returns the canonical reasoning-scores rubric YAML path.
func DefaultRubricPath() string {
	if runtime.GOOS == "darwin" {
		if os.Geteuid() == 0 {
			return "/Library/Application Support/MINTI/reasoning-scores.yaml"
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "MINTI", "reasoning-scores.yaml")
		}
		return "/Library/Application Support/MINTI/reasoning-scores.yaml"
	}
	// linux
	if os.Geteuid() == 0 {
		return "/etc/minti/reasoning-scores.yaml"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "minti", "reasoning-scores.yaml")
	}
	return "/etc/minti/reasoning-scores.yaml"
}

// DefaultMCPBinariesDir returns where install.sh stages the MCP server
// binaries (`minti-mcp-recon`, etc.). cland resolves wire tool spec
// "mcp-recon.nmap_scan" to "<dir>/minti-mcp-recon".
func DefaultMCPBinariesDir() string {
	if runtime.GOOS == "darwin" {
		// Apple convention prefers /usr/local/libexec for headless service helpers.
		return "/usr/local/libexec/minti/mcp"
	}
	return "/opt/minti/mcp"
}
