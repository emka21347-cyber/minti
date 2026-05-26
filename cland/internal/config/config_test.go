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
	if c.State.Dir != "/var/lib/minti/cland" {
		t.Errorf("default state dir = %q", c.State.Dir)
	}
	if !c.Discovery.MDNSEnabled {
		t.Errorf("default mdns_enabled should be true")
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
	// Untouched default should still apply.
	if c.State.Dir != "/var/lib/minti/cland" {
		t.Errorf("state.dir default clobbered: %q", c.State.Dir)
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
