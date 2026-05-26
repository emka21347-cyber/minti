package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.MCP.Shell.Mode != "prompt" {
		t.Errorf("default shell.mode = %q, want %q", d.MCP.Shell.Mode, "prompt")
	}
	if d.MCP.Pkg.RequireSudo != true {
		t.Errorf("default pkg.require_sudo = %v, want true", d.MCP.Pkg.RequireSudo)
	}
	if d.MCP.Recon.AllowRawSocket {
		t.Errorf("default recon.allow_raw_socket should be false")
	}
	if d.MCP.HTTP.MaxBodyBytes != 1<<20 {
		t.Errorf("default http.max_body_bytes = %d, want %d", d.MCP.HTTP.MaxBodyBytes, 1<<20)
	}
}

func TestLoadFrom_MissingFilesPreservesDefaults(t *testing.T) {
	p, err := LoadFrom(filepath.Join(t.TempDir(), "nope1.yaml"), filepath.Join(t.TempDir(), "nope2.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom missing files: %v", err)
	}
	if p.MCP.Shell.Mode != "prompt" {
		t.Errorf("missing files dropped defaults: got %q", p.MCP.Shell.Mode)
	}
}

func TestLoadFrom_UserOverridesSystem(t *testing.T) {
	dir := t.TempDir()
	system := filepath.Join(dir, "system.yaml")
	user := filepath.Join(dir, "user.yaml")
	mustWrite(t, system, `mcp:
  shell:
    mode: allowlist
    allowlist: ["ls"]
  recon:
    allow_raw_socket: true
`)
	mustWrite(t, user, `mcp:
  shell:
    mode: deny
`)
	p, err := LoadFrom(system, user)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if p.MCP.Shell.Mode != "deny" {
		t.Errorf("shell.mode = %q, user file should win, want %q", p.MCP.Shell.Mode, "deny")
	}
	if !p.MCP.Recon.AllowRawSocket {
		t.Errorf("recon.allow_raw_socket from system file lost; got %v", p.MCP.Recon.AllowRawSocket)
	}
	if len(p.MCP.Shell.Allowlist) != 1 || p.MCP.Shell.Allowlist[0] != "ls" {
		t.Errorf("system shell.allowlist lost: %v", p.MCP.Shell.Allowlist)
	}
}

func TestLoadFrom_UnknownFieldsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	mustWrite(t, path, `mcp:
  shell:
    mode: prompt
    typo_field: oops
`)
	_, err := LoadFrom(path, "")
	if err == nil {
		t.Fatal("expected error from unknown field, got nil")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
