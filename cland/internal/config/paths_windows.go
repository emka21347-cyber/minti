//go:build windows

// Platform-canonical paths for Windows.
//
// Service-context detection: when cland runs under NSSM as the LocalSystem
// account (SID S-1-5-18), %LOCALAPPDATA% resolves to
//
//	C:\Windows\System32\config\systemprofile\AppData\Local
//
// — a valid but wrong place for shared service state. M5 peer-review item 5
// (qwen + deepseek) explicitly flagged the "LOCALAPPDATA empty" heuristic
// from the original plan as broken: that env var is never empty under
// LocalSystem.
//
// The right check is "is this process running as LocalSystem?". We open the
// current process token, read the user SID, and compare against the well-
// known SID for LocalSystem. If LocalSystem → use %PROGRAMDATA%. Otherwise
// (interactive admin, normal user) → use %LOCALAPPDATA%.
//
// Belt-and-braces: the M5-B installer writes an explicit `state.dir:` into
// the default cland.yaml, so these defaults only fire as a last-ditch
// fallback if the operator launches cland without --config.

package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// isLocalSystem reports whether the current process token's user is the
// well-known LocalSystem SID (S-1-5-18).
//
// On any error opening the token, parsing the SID, or constructing the
// reference SID we return false — i.e. fall back to per-user paths. That's
// the safe default (LocalSystem mis-detection would put state in a place
// only SYSTEM can read; per-user mis-detection just puts state in the
// invoker's profile).
func isLocalSystem() bool {
	var token windows.Token
	proc := windows.CurrentProcess()
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil || user == nil {
		return false
	}

	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	return windows.EqualSid(user.User.Sid, systemSID)
}

// programData returns %PROGRAMDATA% with a sane fallback when the env var is
// unset (which shouldn't happen on a real Windows host but covers tests).
func programData() string {
	if v := os.Getenv("PROGRAMDATA"); v != "" {
		return v
	}
	return `C:\ProgramData`
}

// programFiles returns %PROGRAMFILES%.
func programFiles() string {
	if v := os.Getenv("PROGRAMFILES"); v != "" {
		return v
	}
	return `C:\Program Files`
}

// localAppData returns %LOCALAPPDATA% with a fallback to the user-profile-
// based path.
func localAppData() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "AppData", "Local")
	}
	return programData() // last resort
}

// DefaultStateDir returns the canonical state dir for the current
// invocation context. See package doc for service-context detection.
func DefaultStateDir() string {
	if isLocalSystem() {
		return filepath.Join(programData(), "MINTI", "cland")
	}
	return filepath.Join(localAppData(), "MINTI", "cland")
}

// DefaultAuditLogPath returns <state_dir>\audit.jsonl. See paths_unix.go
// for why the path was unified onto the state dir.
func DefaultAuditLogPath() string {
	return filepath.Join(DefaultStateDir(), "audit.jsonl")
}

// DefaultConfigPath returns the canonical cland.yaml path.
func DefaultConfigPath() string {
	if isLocalSystem() {
		return filepath.Join(programData(), "MINTI", "cland", "cland.yaml")
	}
	return filepath.Join(localAppData(), "MINTI", "cland", "cland.yaml")
}

// DefaultRubricPath returns the canonical reasoning-scores.yaml path.
func DefaultRubricPath() string {
	if isLocalSystem() {
		return filepath.Join(programData(), "MINTI", "reasoning-scores.yaml")
	}
	return filepath.Join(localAppData(), "MINTI", "reasoning-scores.yaml")
}

// DefaultMCPBinariesDir returns where the installer stages MCP server
// binaries on Windows.
func DefaultMCPBinariesDir() string {
	return filepath.Join(programFiles(), "MINTI", "mcp")
}
