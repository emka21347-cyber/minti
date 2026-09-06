// Memory probes: the workspace's window onto the Clan Memory graph
// (spec §13). Same contract as clan.go — shell the local minti-cland CLI
// (which already speaks HMAC to the daemon), degrade to a believable demo
// graph on a dev box where cland isn't installed. The workspace never
// parses memory.json or speaks the Clan protocol itself.
package clan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MemoryNode mirrors cland's spec §13.1 node shape (wire-compatible subset —
// the workspace passes nodes through, it doesn't reinterpret them).
type MemoryNode struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	Body       string    `json:"body,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Status     string    `json:"status"`
	SessionID  string    `json:"session_id,omitempty"`
	Provenance struct {
		AuthorMemberID string    `json:"author_member_id"`
		Source         string    `json:"source"`
		CreatedAt      time.Time `json:"created_at"`
	} `json:"provenance"`
	UpdatedAt time.Time `json:"updated_at"`
	Rev       uint64    `json:"rev"`
}

// MemoryEdge mirrors spec §13.1.
type MemoryEdge struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Relation  string    `json:"relation"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// MemorySnapshot is the JSON payload served at /api/memory.
type MemorySnapshot struct {
	Source        string       `json:"source"` // "live" | "demo" | "unconfigured"
	FormatVersion int          `json:"format_version"`
	Digest        string       `json:"digest"`
	Scribe        string       `json:"scribe"`          // current scribe member_id ("" = none)
	SelfMemberID  string       `json:"self_member_id"`  // for "(you)" labels
	Nodes         []MemoryNode `json:"nodes"`
	Edges         []MemoryEdge `json:"edges"`
}

// MemoryDigestOnly is the cheap change-poll payload at /api/memory/digest.
type MemoryDigestOnly struct {
	Source string `json:"source"`
	Digest string `json:"digest"`
}

// ProbeMemory returns the full graph snapshot. Never errors — degrades to
// the demo graph (cland missing) or an unconfigured marker (no Clan).
func ProbeMemory(ctx context.Context) MemorySnapshot {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return demoMemory()
	}
	raw, err := runCland(ctx, bin, "memory", "list", "--json")
	if err != nil {
		if isUnaffiliated(err) {
			return MemorySnapshot{Source: "unconfigured"}
		}
		return MemorySnapshot{Source: "unconfigured"}
	}
	var g struct {
		FormatVersion int          `json:"format_version"`
		Nodes         []MemoryNode `json:"nodes"`
		Edges         []MemoryEdge `json:"edges"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return MemorySnapshot{Source: "unconfigured"}
	}
	snap := MemorySnapshot{
		Source:        "live",
		FormatVersion: g.FormatVersion,
		Nodes:         g.Nodes,
		Edges:         g.Edges,
	}
	snap.Digest = ProbeMemoryDigest(ctx).Digest
	if sraw, err := runCland(ctx, bin, "scribe", "--json"); err == nil {
		var sr struct {
			CurrentScribe string `json:"current_scribe"`
			Self          string `json:"self"`
		}
		if json.Unmarshal(sraw, &sr) == nil {
			snap.Scribe = sr.CurrentScribe
			snap.SelfMemberID = sr.Self
		}
	}
	return snap
}

// ProbeMemoryDigest is the cheap 5 s poll: one CLI call, no graph transfer.
func ProbeMemoryDigest(ctx context.Context) MemoryDigestOnly {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return MemoryDigestOnly{Source: "demo", Digest: demoDigest}
	}
	raw, err := runCland(ctx, bin, "memory", "digest", "--json")
	if err != nil {
		return MemoryDigestOnly{Source: "unconfigured"}
	}
	var dr struct {
		Digest string `json:"digest"`
	}
	if json.Unmarshal(raw, &dr) != nil {
		return MemoryDigestOnly{Source: "unconfigured"}
	}
	return MemoryDigestOnly{Source: "live", Digest: dr.Digest}
}

// ---------- mutations (shell-out; loopback-only surface, see server.go) ----------

// MemoryAddNode shells `memory add`. For updates (promote/dismiss/edit) the
// caller passes the existing id — cland's AddOrUpdateNode treats a known id
// as an update and bumps rev server-side.
func MemoryAddNode(ctx context.Context, n MemoryNode) (json.RawMessage, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, fmt.Errorf("demo mode: minti-cland not installed")
	}
	args := []string{"memory", "add", "--json",
		"--type", n.Type, "--title", n.Title}
	if n.ID != "" {
		args = append(args, "--id", n.ID)
	}
	if n.Body != "" {
		args = append(args, "--body", n.Body)
	}
	if len(n.Tags) > 0 {
		args = append(args, "--tags", strings.Join(n.Tags, ","))
	}
	if n.SessionID != "" {
		args = append(args, "--session", n.SessionID)
	}
	if n.Status != "" {
		args = append(args, "--status", n.Status)
	}
	return runCland(ctx, bin, args...)
}

// MemoryLink shells `memory link`.
func MemoryLink(ctx context.Context, from, to, relation string) (json.RawMessage, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, fmt.Errorf("demo mode: minti-cland not installed")
	}
	if relation == "" {
		relation = "relates"
	}
	return runCland(ctx, bin, "memory", "link", from, to, "--relation", relation, "--json")
}

// MemoryArchive shells `memory archive`.
func MemoryArchive(ctx context.Context, id string) (json.RawMessage, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, fmt.Errorf("demo mode: minti-cland not installed")
	}
	return runCland(ctx, bin, "memory", "archive", id, "--json")
}

// MemoryExportBlueprint shells `memory export` into a temp file and returns
// the file's bytes (the CLI owns checksum + privacy mechanics).
func MemoryExportBlueprint(ctx context.Context, sessionID string, stripAuthors bool) ([]byte, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, fmt.Errorf("demo mode: minti-cland not installed")
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("minti-bp-%d.json", time.Now().UnixNano()))
	defer os.Remove(tmp)
	args := []string{"memory", "export", "--out", tmp}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	if stripAuthors {
		args = append(args, "--strip-authors")
	}
	if _, err := runCland(ctx, bin, args...); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp)
}

// MemoryImportBlueprint writes the posted blueprint to a temp file and shells
// `memory import` (merge only — the destructive replace stays CLI-only).
func MemoryImportBlueprint(ctx context.Context, blueprint []byte) (json.RawMessage, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, fmt.Errorf("demo mode: minti-cland not installed")
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("minti-bp-import-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, blueprint, 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	return runCland(ctx, bin, "memory", "import", tmp, "--json")
}

// MemoryWipe clears the Clan memory graph (shells `memory wipe`, which replaces
// the graph with an empty one via the loopback-only import-replace path).
func MemoryWipe(ctx context.Context) (json.RawMessage, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, fmt.Errorf("demo mode: minti-cland not installed")
	}
	return runCland(ctx, bin, "memory", "wipe", "--json")
}

// ScribeInfo is the JSON payload at /api/scribe.
type ScribeInfo struct {
	Source        string `json:"source"`
	CurrentScribe string `json:"current_scribe"`
	Self          string `json:"self"`
	IsSelf        bool   `json:"is_self"`
}

// ProbeScribe shells `scribe --json`.
func ProbeScribe(ctx context.Context) ScribeInfo {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return ScribeInfo{Source: "demo", CurrentScribe: "thinkpad-x230", Self: "minti-a"}
	}
	raw, err := runCland(ctx, bin, "scribe", "--json")
	if err != nil {
		return ScribeInfo{Source: "unconfigured"}
	}
	var sr struct {
		CurrentScribe string `json:"current_scribe"`
		Self          string `json:"self"`
		IsSelf        bool   `json:"is_self"`
	}
	if json.Unmarshal(raw, &sr) != nil {
		return ScribeInfo{Source: "unconfigured"}
	}
	return ScribeInfo{Source: "live", CurrentScribe: sr.CurrentScribe, Self: sr.Self, IsSelf: sr.IsSelf}
}

// ---------- demo graph (dev-box fallback, mirrors clan.go demo()) ----------

const demoDigest = "demo-digest-0001"

func demoMemory() MemorySnapshot {
	t := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	mk := func(id, typ, title, status, session, author, source string, updated string) MemoryNode {
		var n MemoryNode
		n.ID, n.Type, n.Title, n.Status, n.SessionID = id, typ, title, status, session
		n.Provenance.AuthorMemberID = author
		n.Provenance.Source = source
		n.Provenance.CreatedAt = t(updated)
		n.UpdatedAt = t(updated)
		n.Rev = 1
		return n
	}
	e := func(from, to, rel string) MemoryEdge {
		return MemoryEdge{From: from, To: to, Relation: rel, CreatedAt: t("2026-06-10T12:00:00Z"), CreatedBy: "minti-a"}
	}
	nodes := []MemoryNode{
		mk("sess-tls", "research_session", "Old hardware TLS quirks", "active", "", "minti-a", "manual", "2026-06-10T09:00:00Z"),
		mk("f-ntp", "finding", "x230 fails pin check before NTP sync", "active", "sess-tls", "thinkpad-x230", "manual", "2026-06-10T09:20:00Z"),
		mk("f-cert", "finding", "cert clock-skew window is ±60s", "active", "sess-tls", "minti-b", "manual", "2026-06-10T10:05:00Z"),
		mk("d-clamp", "decision", "defer pin clamp to v1.1", "active", "sess-tls", "minti-a", "manual", "2026-06-10T11:00:00Z"),
		mk("fact-mesh", "fact", "mDNS dies on enterprise APs", "active", "", "minti-c", "manual", "2026-06-09T16:00:00Z"),
		mk("skill-iso", "skill", "lb build needs umask 022", "active", "", "minti-a", "manual", "2026-06-08T13:00:00Z"),
		mk("ev-join", "event", "member_joined: thinkpad", "active", "", "minti-a", "system", "2026-06-08T12:00:00Z"),
		mk("m-think", "member", "thinkpad-x230 (the scribe)", "active", "", "minti-a", "system", "2026-06-08T12:00:00Z"),
		mk("p-sum", "finding", "proposed: session summary — TLS quirks trace to clock skew", "proposed", "sess-tls", "thinkpad-x230", "scribe", "2026-06-10T11:30:00Z"),
		mk("p-fact", "fact", "proposed: workspace chat mostly asks about model fit", "proposed", "", "thinkpad-x230", "scribe", "2026-06-10T11:40:00Z"),
		mk("old-note", "fact", "superseded packaging note", "archived", "", "minti-b", "manual", "2026-06-07T10:00:00Z"),
	}
	edges := []MemoryEdge{
		e("f-ntp", "sess-tls", "contributes_to"),
		e("f-cert", "sess-tls", "contributes_to"),
		e("d-clamp", "sess-tls", "contributes_to"),
		e("p-sum", "sess-tls", "contributes_to"),
		e("f-ntp", "m-think", "about_member"),
		e("d-clamp", "f-cert", "derived_from"),
		e("fact-mesh", "f-ntp", "relates"),
	}
	return MemorySnapshot{
		Source:        "demo",
		FormatVersion: 1,
		Digest:        demoDigest,
		Scribe:        "thinkpad-x230",
		SelfMemberID:  "minti-a",
		Nodes:         nodes,
		Edges:         edges,
	}
}
