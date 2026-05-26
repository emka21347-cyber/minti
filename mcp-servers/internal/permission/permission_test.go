package permission

import (
	"testing"

	"github.com/minti/mcp-servers/internal/policy"
)

func TestCheck_NilPolicyUsesDefaults(t *testing.T) {
	d, _ := Check(nil, "minti-mcp-shell", "exec", map[string]any{"command": "ls"})
	if d != Allow {
		t.Errorf("nil policy default-prompt should allow at server side; got %v", d)
	}
}

func TestCheck_DenyTools(t *testing.T) {
	p := policy.Defaults()
	p.MCP.Recon.DenyTools = []string{"nmap_scan"}
	d, reason := Check(p, "minti-mcp-recon", "nmap_scan", map[string]any{"target": "127.0.0.1"})
	if d != Deny {
		t.Errorf("expected Deny, got %v (reason=%q)", d, reason)
	}
}

func TestCheck_ShellAllowlist(t *testing.T) {
	p := policy.Defaults()
	p.MCP.Shell.Mode = "allowlist"
	p.MCP.Shell.Allowlist = []string{"ls", "cat"}

	if d, _ := Check(p, "minti-mcp-shell", "exec", map[string]any{"command": "ls -la"}); d != Allow {
		t.Error("allowlisted 'ls' should be Allow")
	}
	if d, _ := Check(p, "minti-mcp-shell", "exec", map[string]any{"command": "rm -rf /"}); d != Deny {
		t.Error("non-allowlisted 'rm' should be Deny")
	}
}

func TestCheck_ShellDenyMode(t *testing.T) {
	p := policy.Defaults()
	p.MCP.Shell.Mode = "deny"
	if d, _ := Check(p, "minti-mcp-shell", "exec", map[string]any{"command": "ls"}); d != Deny {
		t.Error("mode=deny must deny everything")
	}
}

func TestCheck_ReconRawSocket(t *testing.T) {
	p := policy.Defaults() // allow_raw_socket: false
	d, _ := Check(p, "minti-mcp-recon", "nmap_scan", map[string]any{
		"target":     "127.0.0.1",
		"raw_socket": true,
	})
	if d != Deny {
		t.Error("raw-socket nmap without policy must Deny")
	}

	p.MCP.Recon.AllowRawSocket = true
	d, _ = Check(p, "minti-mcp-recon", "nmap_scan", map[string]any{
		"target":     "127.0.0.1",
		"raw_socket": true,
	})
	if d != Allow {
		t.Error("raw-socket nmap with policy must Allow")
	}
}

func TestCheck_FSDenyOverridesAllow(t *testing.T) {
	p := policy.Defaults()
	p.MCP.FS.Allow = []string{"/tmp"}
	p.MCP.FS.Deny = []string{"/tmp/secret"}

	if d, _ := Check(p, "minti-mcp-fs", "read_text", map[string]any{"path": "/tmp/ok.txt"}); d != Allow {
		t.Error("/tmp/ok.txt under allow prefix should be Allow")
	}
	if d, _ := Check(p, "minti-mcp-fs", "read_text", map[string]any{"path": "/tmp/secret/key"}); d != Deny {
		t.Error("/tmp/secret/key on deny list should be Deny")
	}
	if d, _ := Check(p, "minti-mcp-fs", "read_text", map[string]any{"path": "/etc/passwd"}); d != Deny {
		t.Error("/etc/passwd outside allow should be Deny")
	}
}

func TestVerifyCrossClanToken_AlwaysFailsInM2(t *testing.T) {
	if err := VerifyCrossClanToken("any-token"); err == nil {
		t.Error("cross-Clan token verification must fail in M2")
	}
}
