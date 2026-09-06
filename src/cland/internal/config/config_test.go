package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Listen.Port != 7777 {
		t.Errorf("default port = %d, want 7777", c.Listen.Port)
	}
	// State.Dir is platform-canonical now (M5-A). Assert it matches the
	// helper rather than a hardcoded path so the test stays valid on
	// linux + windows + darwin.
	if c.State.Dir != DefaultStateDir() {
		t.Errorf("default state dir = %q, want %q", c.State.Dir, DefaultStateDir())
	}
	if c.State.Dir == "" {
		t.Errorf("default state dir must not be empty")
	}
	if !c.Discovery.MDNSEnabled {
		t.Errorf("default mdns_enabled should be true")
	}
}

func TestDefaultConfigPath_MintiConfigOverride(t *testing.T) {
	const want = "/some/explicit/cland.yaml"
	t.Setenv("MINTI_CONFIG", want)
	if got := DefaultConfigPath(); got != want {
		t.Errorf("DefaultConfigPath with MINTI_CONFIG set = %q, want %q", got, want)
	}
	// Empty MINTI_CONFIG must fall through to the canonical path, not "".
	t.Setenv("MINTI_CONFIG", "")
	if got := DefaultConfigPath(); got == "" || got == want {
		t.Errorf("DefaultConfigPath with empty MINTI_CONFIG = %q, want canonical path", got)
	}
}

func TestLoad_MissingFileReturnsDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Port != 7777 {
		t.Errorf("missing file should yield default port; got %d", c.Listen.Port)
	}
}

func TestLoad_OverlaysDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cland.yaml")
	must(t, os.WriteFile(path, []byte(`
listen:
  port: 9999
discovery:
  mdns_enabled: false
`), 0o600))
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Port != 9999 {
		t.Errorf("port overlay lost: %d", c.Listen.Port)
	}
	if c.Discovery.MDNSEnabled {
		t.Errorf("mdns_enabled overlay lost")
	}
	// Untouched default should still apply (now platform-canonical via
	// DefaultStateDir, not a hardcoded /var/lib path).
	if c.State.Dir != DefaultStateDir() {
		t.Errorf("state.dir default clobbered: got %q, want %q", c.State.Dir, DefaultStateDir())
	}
}

func TestLoad_GarbageYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cland.yaml")
	must(t, os.WriteFile(path, []byte(":\n  - this is not valid"), 0o600))
	_, err := Load(path)
	if err == nil {
		t.Errorf("expected parse error, got nil")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
