// Package agent implements the native Hermes-agent harness that lives inside the
// cland daemon: it passes MCP tool schemas to the model, executes the model's
// tool-calls locally on this node, loops, and streams the whole exchange back to
// the dashboard.
//
// registry.go is the safety gate's foundation (M1 stage S0): a static, fail-closed
// classification of every MCP tool into read-only (auto-run) vs system-changing
// (per-call in-chat Approve/Deny). The agent loop consults Classify before every
// tool invocation.
package agent

// ToolClass is the safety classification of an MCP tool.
type ToolClass int

const (
	// ClassRead tools only observe — they read the filesystem, query the network,
	// or look things up. They auto-run inside the agent loop with no user prompt.
	ClassRead ToolClass = iota
	// ClassChange tools mutate the user's system: filesystem writes, shell
	// execution, package installs. Every ClassChange call requires an explicit
	// in-chat Approve/Deny before it runs.
	ClassChange
)

func (c ToolClass) String() string {
	switch c {
	case ClassRead:
		return "read"
	case ClassChange:
		return "change"
	default:
		return "unknown"
	}
}

// Wire tool names are "<namespace>.<tool>" as resolved by
// cland/internal/toolexec (e.g. "mcp-fs.read_text" → server "minti-mcp-fs",
// tool "read_text"). The two sets below are keyed by that wire form.
//
// readTools is the explicit allowlist of observational tools that auto-run.
// Classify is fail-closed against this set: a tool NOT listed here is treated as
// ClassChange and therefore requires approval, even if it is also absent from
// changeTools. That makes "forgot to classify a new tool" fail safe (gated) rather
// than fail open (silently auto-run).
var readTools = map[string]struct{}{
	// mcp-fs — read side
	"mcp-fs.read_text": {},
	"mcp-fs.list_dir":  {},
	"mcp-fs.glob":      {},
	// mcp-http — both verbs only fetch
	"mcp-http.fetch_url": {},
	"mcp-http.head_url":  {},
	// mcp-recon — network observation only; never mutates the local system
	"mcp-recon.dig_lookup": {},
	"mcp-recon.http_probe": {},
	"mcp-recon.whois":      {},
	"mcp-recon.nmap_scan":  {},
	// mcp-wiki — lookups
	"mcp-wiki.wiki_get":    {},
	"mcp-wiki.wiki_search": {},
	// mcp-pkg — search is read-only; install is the change verb (below)
	"mcp-pkg.search": {},
	// mcp-search — the keyless DuckDuckGo server added in M1 S2; classified now so
	// the registry is complete before the server exists.
	"mcp-search.web_search": {},
}

// changeTools is the explicit set of system-CHANGING tools. It is maintained
// alongside readTools purely for documentation and for Known(): Classify does not
// consult it (the fail-closed read allowlist already gates everything else as a
// change). A test asserts the two sets are disjoint.
var changeTools = map[string]struct{}{
	"mcp-fs.write_text": {},
	"mcp-shell.exec":    {},
	"mcp-pkg.install":   {},
}

// Classify returns the safety class of a wire tool name. Fail-closed: only tools
// in the static read allowlist are ClassRead; everything else — known change tools
// AND any unrecognised tool — is ClassChange, so it requires approval.
func Classify(wireTool string) ToolClass {
	if _, ok := readTools[wireTool]; ok {
		return ClassRead
	}
	return ClassChange
}

// RequiresApproval reports whether a tool call must be gated behind an in-chat
// Approve/Deny. It is the loop's decision hook and is exactly Classify == ClassChange.
func RequiresApproval(wireTool string) bool {
	return Classify(wireTool) == ClassChange
}

// Known reports whether a wire tool appears in either static set. The loop uses it
// to log/telemeter calls to unrecognised tools — which Classify still gates as
// ClassChange — so an unclassified tool is visible rather than silently approved-away.
func Known(wireTool string) bool {
	if _, ok := readTools[wireTool]; ok {
		return true
	}
	_, ok := changeTools[wireTool]
	return ok
}
