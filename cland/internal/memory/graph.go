// Package memory implements spec §13 Clan Memory: a gossiped, Clan-owned,
// curated knowledge graph for distributed research.
//
// Unlike the audit log (§9.1, never gossiped), the memory graph IS gossiped:
// every active member holds the full graph and converges on the union of all
// members' contributions. This file is the pure layer — types, caps,
// canonical bytes, deterministic ids, Digest, Merge — with no I/O, no locks,
// no clock reads, so the CRDT properties are testable in isolation.
//
// FIELD ORDER IS NORMATIVE (spec §13.4). The Go structs below declare fields
// in the exact §13.1 wire order with explicit json tags; canonical node bytes
// (the LWW hash tiebreak) and the blueprint checksum both depend on this
// order. graph_test.go pins the encoding byte-for-byte — if you add or move
// a field, that conformance test MUST be updated deliberately, never casually.
package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const FormatVersion = 1

// Caps per spec §13.1 — enforced at write endpoints, deliberately NOT at
// merge (§13.4 permissive-merge rule: dropping nodes locally would freeze
// digests in permanent mismatch).
const (
	MaxNodes           = 2000
	MaxEdges           = 8000
	MaxSerializedBytes = 2 << 20 // 2 MiB
	MaxTitleChars      = 200
	MaxBodyBytes       = 8 << 10 // 8 KiB
	MaxTags            = 16
)

// Node types per spec §13.1.
var NodeTypes = map[string]bool{
	"research_session": true,
	"finding":          true,
	"decision":         true,
	"fact":             true,
	"skill":            true,
	"event":            true,
	"member":           true,
	"artifact":         true,
}

// Node statuses per spec §13.1.
var NodeStatuses = map[string]bool{
	"proposed":   true,
	"active":     true,
	"superseded": true,
	"archived":   true,
}

// Provenance sources per spec §13.1.
var ProvenanceSources = map[string]bool{
	"manual":  true,
	"system":  true,
	"scribe":  true,
	"distill": true,
	"import":  true,
}

// Edge relations per spec §13.1.
var EdgeRelations = map[string]bool{
	"relates":        true,
	"supersedes":     true,
	"derived_from":   true,
	"contributes_to": true,
	"about_member":   true,
	"caused_by":      true,
}

// Provenance is set by the daemon on create and immutable afterwards.
// AuthorMemberID always comes from the HMAC-authenticated origin, never from
// the client (spec §13.6).
type Provenance struct {
	AuthorMemberID string    `json:"author_member_id"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
}

// Node is one memory in the graph. Field order is normative — see the
// package comment.
type Node struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	Status     string     `json:"status"`
	SessionID  string     `json:"session_id,omitempty"`
	Provenance Provenance `json:"provenance"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Rev        uint64     `json:"rev"`
}

// Edge links two nodes. Edges are add-only in v1 (no tombstones — OQ-9);
// deduped by (From, To, Relation) with local metadata winning.
type Edge struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Relation  string    `json:"relation"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// Graph is the unit that is stored, fetched, merged, and exported.
type Graph struct {
	FormatVersion int    `json:"format_version"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

// NewGraph returns an empty graph at the current format version.
func NewGraph() *Graph {
	return &Graph{FormatVersion: FormatVersion}
}

// ---------- canonical bytes + LWW ----------

// CanonicalNodeBytes returns the normative compact-JSON encoding of a node
// (spec §13.4). Go's encoding/json emits struct fields in declaration order,
// which the conformance test pins byte-for-byte.
func CanonicalNodeBytes(n Node) []byte {
	b, err := json.Marshal(n)
	if err != nil {
		// A Node contains only strings, times, and ints — Marshal cannot
		// fail on it. Keep the impossible branch loud rather than silent.
		panic(fmt.Sprintf("memory: canonical marshal: %v", err))
	}
	return b
}

// nodeWins reports whether candidate `a` beats incumbent `b` under the spec
// §13.4 LWW rule: greater (UpdatedAt, Rev, sha256(canonical)) wins. Both
// sides of any merge compare identical tuples, so the winner is the same
// everywhere regardless of local clocks.
func nodeWins(a, b Node) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	if a.Rev != b.Rev {
		return a.Rev > b.Rev
	}
	ha := sha256.Sum256(CanonicalNodeBytes(a))
	hb := sha256.Sum256(CanonicalNodeBytes(b))
	// Equal hashes mean identical canonical bytes (same node) — a does not
	// strictly win, so idempotent merges keep the incumbent.
	return hex.EncodeToString(ha[:]) > hex.EncodeToString(hb[:])
}

// edgeKey is the dedup identity for edges.
func edgeKey(e Edge) string { return e.From + "|" + e.To + "|" + e.Relation }

// ---------- Merge (CRDT-lite, spec §13.4) ----------

// Merge returns the union of local and remote. Commutative, associative,
// idempotent: nodes keyed by ID with the LWW winner rule; edges set-union
// deduped by (From,To,Relation) with local metadata winning. The output is
// sorted (nodes by ID, edges by key) so equal merged sets are also equal
// byte-wise. Caps are deliberately NOT enforced here (permissive merge).
// Inputs are not mutated.
func Merge(local, remote *Graph) *Graph {
	out := NewGraph()
	if local != nil && local.FormatVersion > out.FormatVersion {
		out.FormatVersion = local.FormatVersion
	}
	if remote != nil && remote.FormatVersion > out.FormatVersion {
		out.FormatVersion = remote.FormatVersion
	}

	nodes := map[string]Node{}
	if local != nil {
		for _, n := range local.Nodes {
			nodes[n.ID] = n
		}
	}
	if remote != nil {
		for _, n := range remote.Nodes {
			if cur, ok := nodes[n.ID]; !ok || nodeWins(n, cur) {
				nodes[n.ID] = n
			}
		}
	}
	out.Nodes = make([]Node, 0, len(nodes))
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].ID < out.Nodes[j].ID })

	edges := map[string]Edge{}
	if local != nil {
		for _, e := range local.Edges {
			if _, ok := edges[edgeKey(e)]; !ok {
				edges[edgeKey(e)] = e // local wins incl. on local-internal dupes
			}
		}
	}
	if remote != nil {
		for _, e := range remote.Edges {
			if _, ok := edges[edgeKey(e)]; !ok {
				edges[edgeKey(e)] = e
			}
		}
	}
	out.Edges = make([]Edge, 0, len(edges))
	for _, e := range edges {
		out.Edges = append(out.Edges, e)
	}
	sort.Slice(out.Edges, func(i, j int) bool { return edgeKey(out.Edges[i]) < edgeKey(out.Edges[j]) })

	return out
}

// ---------- Digest (content-versioned, spec §13.5) ----------

// Digest returns the sha256-hex over sorted "n|id|rev|RFC3339Nano(updated_at)"
// node lines followed by sorted "e|from|to|relation" edge lines, LF-joined.
// Unlike the revocations set-only digest (§3.5), this moves on every LWW edit
// — rev and updated_at are in the line. Archived nodes are included
// (tombstones must converge). The empty graph digests sha256 of empty input,
// matching the §3.5 convention.
func Digest(g *Graph) string {
	if g == nil || (len(g.Nodes) == 0 && len(g.Edges) == 0) {
		s := sha256.Sum256(nil)
		return hex.EncodeToString(s[:])
	}
	lines := make([]string, 0, len(g.Nodes)+len(g.Edges))
	nodeLines := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeLines = append(nodeLines, fmt.Sprintf("n|%s|%d|%s", n.ID, n.Rev, n.UpdatedAt.Format(time.RFC3339Nano)))
	}
	sort.Strings(nodeLines)
	edgeLines := make([]string, 0, len(g.Edges))
	for _, e := range g.Edges {
		edgeLines = append(edgeLines, "e|"+e.From+"|"+e.To+"|"+e.Relation)
	}
	sort.Strings(edgeLines)
	lines = append(lines, nodeLines...)
	lines = append(lines, edgeLines...)
	s := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(s[:])
}

// ---------- identifiers (spec §13.3) ----------

// NewUUIDv4 generates a random UUID v4 from crypto/rand — hand-rolled like
// identity.go's, per P1 (no external deps).
func NewUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return foldToUUID(b), nil
}

// DeterministicEventID mints the spec §13.3 deterministic id: sha256 over
// "clan_id|kind|subject|qualifier", first 16 bytes folded to UUID shape.
// Every member observing the same event mints the identical id, so the
// merge union dedups system nodes instead of multiplying them.
func DeterministicEventID(clanID, kind, subject, qualifier string) string {
	sum := sha256.Sum256([]byte(clanID + "|" + kind + "|" + subject + "|" + qualifier))
	return foldToUUID(sum[:16])
}

// foldToUUID applies UUIDv4 version/variant nibble surgery to 16 bytes and
// formats 8-4-4-4-12.
func foldToUUID(b []byte) string {
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // variant 10xx
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---------- write-path validation (spec §13.1 / §13.6) ----------

// ValidateNode checks the per-field caps + enums for a node arriving at a
// write endpoint. Merge never calls this (permissive-merge rule).
func ValidateNode(n Node) error {
	if n.Type == "" || !NodeTypes[n.Type] {
		return fmt.Errorf("memory: invalid node type %q", n.Type)
	}
	if n.Status != "" && !NodeStatuses[n.Status] {
		return fmt.Errorf("memory: invalid node status %q", n.Status)
	}
	if strings.TrimSpace(n.Title) == "" {
		return fmt.Errorf("memory: title required")
	}
	if len([]rune(n.Title)) > MaxTitleChars {
		return fmt.Errorf("memory: title exceeds %d chars", MaxTitleChars)
	}
	if len(n.Body) > MaxBodyBytes {
		return fmt.Errorf("memory: body exceeds %d bytes", MaxBodyBytes)
	}
	if len(n.Tags) > MaxTags {
		return fmt.Errorf("memory: more than %d tags", MaxTags)
	}
	if n.Provenance.Source != "" && !ProvenanceSources[n.Provenance.Source] {
		return fmt.Errorf("memory: invalid provenance source %q", n.Provenance.Source)
	}
	return nil
}

// ValidateEdge checks an edge arriving at a write endpoint. Dangling
// endpoints are allowed by spec (§13.4) — existence is not checked here.
func ValidateEdge(e Edge) error {
	if e.From == "" || e.To == "" {
		return fmt.Errorf("memory: edge requires from + to")
	}
	if !EdgeRelations[e.Relation] {
		return fmt.Errorf("memory: invalid edge relation %q", e.Relation)
	}
	return nil
}
