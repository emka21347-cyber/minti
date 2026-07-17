package agent

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		tool string
		want ToolClass
	}{
		// read-only — auto-run
		{"mcp-fs.read_text", ClassRead},
		{"mcp-fs.list_dir", ClassRead},
		{"mcp-fs.glob", ClassRead},
		{"mcp-http.fetch_url", ClassRead},
		{"mcp-http.head_url", ClassRead},
		{"mcp-recon.dig_lookup", ClassRead},
		{"mcp-recon.http_probe", ClassRead},
		{"mcp-recon.whois", ClassRead},
		{"mcp-recon.nmap_scan", ClassRead},
		{"mcp-wiki.wiki_get", ClassRead},
		{"mcp-wiki.wiki_search", ClassRead},
		{"mcp-pkg.search", ClassRead},
		{"mcp-search.web_search", ClassRead},
		// system-changing — needs approval
		{"mcp-fs.write_text", ClassChange},
		{"mcp-shell.exec", ClassChange},
		{"mcp-pkg.install", ClassChange},
	}
	for _, c := range cases {
		if got := Classify(c.tool); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.tool, got, c.want)
		}
		if gotApproval := RequiresApproval(c.tool); gotApproval != (c.want == ClassChange) {
			t.Errorf("RequiresApproval(%q) = %v, want %v", c.tool, gotApproval, c.want == ClassChange)
		}
	}
}

// TestClassifyFailClosed: anything not in the read allowlist — bogus names, a
// brand-new unclassified tool, malformed input — must be gated as ClassChange so
// it cannot silently auto-run.
func TestClassifyFailClosed(t *testing.T) {
	unknown := []string{
		"mcp-fs.delete_tree",      // plausible future change verb, not yet classified
		"mcp-evil.rm_rf",          // hostile namespace
		"mcp-fs.read_text.extra",  // malformed
		"read_text",               // missing namespace
		"",                        // empty
		"mcp-pkg.remove",          // looks read-ish but unknown → must gate
	}
	for _, tool := range unknown {
		if got := Classify(tool); got != ClassChange {
			t.Errorf("Classify(%q) = %v, want ClassChange (fail-closed)", tool, got)
		}
		if !RequiresApproval(tool) {
			t.Errorf("RequiresApproval(%q) = false, want true (fail-closed)", tool)
		}
		if Known(tool) {
			t.Errorf("Known(%q) = true, want false", tool)
		}
	}
}

// TestSetsDisjoint: a tool must never be both a read and a change tool — that
// would make the classification ambiguous (and Classify, which only reads the read
// set, would silently win, masking the contradiction).
func TestSetsDisjoint(t *testing.T) {
	for tool := range readTools {
		if _, also := changeTools[tool]; also {
			t.Errorf("tool %q is in both readTools and changeTools", tool)
		}
	}
}

// TestShippedToolsAllClassified is a tripwire: every tool the MCP servers ship
// today must be Known. If someone adds a tool to mcp-servers/cmd/* without
// classifying it here, this test fails — forcing a deliberate read/change decision
// rather than letting it fall through to the fail-closed default unnoticed.
//
// Keep this list in sync with the AddTool registrations in mcp-servers/cmd/*.
func TestShippedToolsAllClassified(t *testing.T) {
	shipped := []string{
		"mcp-fs.read_text", "mcp-fs.list_dir", "mcp-fs.glob", "mcp-fs.write_text",
		"mcp-http.fetch_url", "mcp-http.head_url",
		"mcp-pkg.search", "mcp-pkg.install",
		"mcp-recon.dig_lookup", "mcp-recon.http_probe", "mcp-recon.whois", "mcp-recon.nmap_scan",
		"mcp-shell.exec",
		"mcp-wiki.wiki_get", "mcp-wiki.wiki_search",
	}
	for _, tool := range shipped {
		if !Known(tool) {
			t.Errorf("shipped MCP tool %q is not classified in the registry", tool)
		}
	}
}

func TestToolClassString(t *testing.T) {
	if ClassRead.String() != "read" {
		t.Errorf("ClassRead.String() = %q, want %q", ClassRead.String(), "read")
	}
	if ClassChange.String() != "change" {
		t.Errorf("ClassChange.String() = %q, want %q", ClassChange.String(), "change")
	}
	if ToolClass(99).String() != "unknown" {
		t.Errorf("ToolClass(99).String() = %q, want %q", ToolClass(99).String(), "unknown")
	}
}
