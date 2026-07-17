package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnderHome(t *testing.T) {
	home := must(os.UserHomeDir())
	t.Logf("home=%s", home)

	cases := []struct {
		path string
		want bool
	}{
		{home, true},
		{filepath.Join(home, "Documents"), true},
		{filepath.Join(home, "Documents", "deep", "file.txt"), true},
		{filepath.Dir(home), false},
		{rootishPath(), false},
	}
	for _, c := range cases {
		got := underHome(c.path, home)
		if got != c.want {
			t.Errorf("underHome(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestSafePath_RejectsOutsideHome(t *testing.T) {
	_, err := safePath(rootishPath())
	if err == nil {
		t.Errorf("expected safePath to reject %s", rootishPath())
	}
}

func TestSafePath_AcceptsHomeFile(t *testing.T) {
	home := must(os.UserHomeDir())
	// Use a known-existing path under home — the home dir itself.
	resolved, err := safePath(home)
	if err != nil {
		t.Fatalf("safePath(home): %v", err)
	}
	if !underHome(resolved, home) {
		t.Errorf("resolved %s not under home %s", resolved, home)
	}
}

func TestSafePath_HomeShortcut(t *testing.T) {
	if _, err := safePath("~"); err != nil {
		t.Errorf("safePath(~) errored: %v", err)
	}
}

func rootishPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows`
	}
	return "/etc"
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
