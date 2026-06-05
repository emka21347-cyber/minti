// Package permission decides whether a tool call is allowed given the loaded
// policy. M2 scope: local-only enforcement of hard deny rules. The host
// (mcptest / opencode / Claude Code) is responsible for rendering the consent
// prompt before calling — see docs/clan-protocol.md §7.1.
//
// Cross-Clan signed permission tokens (the second half of §7.1) are stubbed
// here as VerifyCrossClanToken and always reject in M2; they are completed in
// M4 alongside cland.
package permission

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/minti/mcp-servers/internal/policy"
)

type Decision int

const (
	Allow Decision = iota
	Deny
)

func (d Decision) String() string {
	if d == Allow {
		return "allow"
	}
	return "deny"
}

// Check returns the policy decision for (server, tool, args). args is the
// flattened argument map already JSON-round-tripped by the caller.
//
// nil policy is treated as Defaults().
func Check(p *policy.Policy, server, tool string, args map[string]any) (Decision, string) {
	if p == nil {
		p = policy.Defaults()
	}
	switch server {
	case "minti-mcp-fs":
		return checkFS(&p.MCP.FS, tool, args)
	case "minti-mcp-shell":
		return checkShell(&p.MCP.Shell, tool, args)
	case "minti-mcp-recon":
		return checkRecon(&p.MCP.Recon, tool, args)
	case "minti-mcp-pkg":
		return checkPkg(&p.MCP.Pkg, tool, args)
	case "minti-mcp-http":
		return checkHTTP(&p.MCP.HTTP, tool, args)
	case "minti-mcp-wiki":
		return checkWiki(&p.MCP.Wiki, tool, args)
	}
	return Allow, ""
}

// VerifyCrossClanToken is preserved as a thin stub for backwards compatibility
// — no caller in the mcp-servers module invokes it. The real verification now
// lives in cland/internal/toolexec (Phase G): cland's POST /mcp/execute handler
// verifies the spec §7.1 token (HMAC under clan_key + target == self.member_id
// + exp + approved_at + replay) BEFORE spawning the MCP server subprocess. The
// MCP server itself never sees the token; cland already authorised the call by
// the time the subprocess is launched.
//
// Tool-side policy (the deny_tools per-namespace kill switch) is still
// enforced inside each MCP server binary via Check() above — so a cross-Clan
// call that passes token verification can still be refused by the executor's
// local policy.
func VerifyCrossClanToken(_ string) error {
	return errors.New("permission.VerifyCrossClanToken: verification lives in cland/internal/toolexec since M4-G; this stub is here for compat only")
}

func checkFS(p *policy.FSPolicy, tool string, args map[string]any) (Decision, string) {
	if denied(p.DenyTools, tool) {
		return Deny, "tool '" + tool + "' is on fs.deny_tools"
	}
	path, _ := args["path"].(string)
	if path == "" {
		return Allow, ""
	}
	abs := expandHome(path)
	for _, d := range p.Deny {
		if hasPrefix(abs, expandHome(d)) {
			return Deny, "fs path '" + path + "' is on fs.deny"
		}
	}
	if len(p.Allow) == 0 {
		return Allow, ""
	}
	for _, a := range p.Allow {
		if hasPrefix(abs, expandHome(a)) {
			return Allow, ""
		}
	}
	return Deny, "fs path '" + path + "' not under any fs.allow prefix"
}

func checkShell(p *policy.ShellPolicy, tool string, args map[string]any) (Decision, string) {
	if denied(p.DenyTools, tool) {
		return Deny, "tool '" + tool + "' is on shell.deny_tools"
	}
	cmd, _ := args["command"].(string)
	name := firstWord(cmd)
	switch p.Mode {
	case "deny":
		return Deny, "shell.mode=deny"
	case "allowlist":
		for _, ok := range p.Allowlist {
			if name == ok {
				return Allow, ""
			}
		}
		return Deny, "shell command '" + name + "' not on shell.allowlist"
	case "prompt", "":
		return Allow, ""
	}
	return Deny, "shell.mode '" + p.Mode + "' is unknown"
}

func checkRecon(p *policy.ReconPolicy, tool string, args map[string]any) (Decision, string) {
	if denied(p.DenyTools, tool) {
		return Deny, "tool '" + tool + "' is on recon.deny_tools"
	}
	if tool == "nmap_scan" {
		raw, _ := args["raw_socket"].(bool)
		if raw && !p.AllowRawSocket {
			return Deny, "nmap raw-socket scan requires recon.allow_raw_socket=true"
		}
	}
	return Allow, ""
}

func checkPkg(p *policy.PkgPolicy, tool string, args map[string]any) (Decision, string) {
	if denied(p.DenyTools, tool) {
		return Deny, "tool '" + tool + "' is on pkg.deny_tools"
	}
	return Allow, ""
}

func checkHTTP(p *policy.HTTPPolicy, tool string, args map[string]any) (Decision, string) {
	if denied(p.DenyTools, tool) {
		return Deny, "tool '" + tool + "' is on http.deny_tools"
	}
	return Allow, ""
}

func checkWiki(p *policy.WikiPolicy, tool string, args map[string]any) (Decision, string) {
	if denied(p.DenyTools, tool) {
		return Deny, "tool '" + tool + "' is on wiki.deny_tools"
	}
	return Allow, ""
}

func denied(list []string, tool string) bool {
	for _, t := range list {
		if t == tool {
			return true
		}
	}
	return false
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func hasPrefix(path, prefix string) bool {
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
