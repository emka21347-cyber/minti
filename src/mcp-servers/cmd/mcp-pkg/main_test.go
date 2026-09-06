package main

import "testing"

func TestPkgNameRegex(t *testing.T) {
	good := []string{"nmap", "libssl-dev", "python3.11", "g++", "lib32stdc++6"}
	bad := []string{
		"nmap; rm -rf /",
		"$(whoami)",
		"`id`",
		"nmap && evil",
		"nmap | sh",
		"-evil",      // leading dash
		"+plus",      // leading plus
		".dotleader", // leading dot
		"",
	}
	for _, s := range good {
		if !pkgNameRe.MatchString(s) {
			t.Errorf("expected pkg accepted: %q", s)
		}
	}
	for _, s := range bad {
		if pkgNameRe.MatchString(s) {
			t.Errorf("expected pkg REJECTED: %q", s)
		}
	}
}

func TestParseSearch(t *testing.T) {
	raw := "nmap - Network exploration tool and security/port scanner\n" +
		"masscan - TCP port scanner\n" +
		"  \n" +
		"weirdpkg\n"
	matches := parseSearch(raw)
	if len(matches) != 3 {
		t.Fatalf("got %d matches, want 3 (incl. summary-less)", len(matches))
	}
	if matches[0].Name != "nmap" || matches[0].Summary == "" {
		t.Errorf("first match malformed: %+v", matches[0])
	}
	if matches[2].Name != "weirdpkg" || matches[2].Summary != "" {
		t.Errorf("summary-less match malformed: %+v", matches[2])
	}
}
