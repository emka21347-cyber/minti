package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func node(id string, rev uint64, updated string, mut ...func(*Node)) Node {
	n := Node{
		ID:     id,
		Type:   "fact",
		Title:  "title-" + id,
		Status: "active",
		Provenance: Provenance{
			AuthorMemberID: "member-a",
			Source:         "manual",
			CreatedAt:      ts("2026-06-11T10:00:00Z"),
		},
		UpdatedAt: ts(updated),
		Rev:       rev,
	}
	for _, m := range mut {
		m(&n)
	}
	return n
}

func edge(from, to, rel string) Edge {
	return Edge{From: from, To: to, Relation: rel,
		CreatedAt: ts("2026-06-11T10:00:00Z"), CreatedBy: "member-a"}
}

func graph(nodes []Node, edges []Edge) *Graph {
	return &Graph{FormatVersion: FormatVersion, Nodes: nodes, Edges: edges}
}

// ---------- canonical bytes (spec §13.4 — REQUIRED conformance) ----------

// TestCanonicalNodeBytesStable pins the normative encoding byte-for-byte.
// If this test breaks, every node hash and blueprint checksum in every
// deployed Clan breaks with it — change it only with a format_version bump
// and a deliberate spec edit.
func TestCanonicalNodeBytesStable(t *testing.T) {
	n := Node{
		ID:        "9a1f0c2e-0000-4000-8000-000000000001",
		Type:      "finding",
		Title:     "TLS pin mismatch on resurrected node",
		Body:      "details *markdown*",
		Tags:      []string{"tls", "clan"},
		Status:    "active",
		SessionID: "9a1f0c2e-0000-4000-8000-00000000000s",
		Provenance: Provenance{
			AuthorMemberID: "11111111-2222-4333-8444-555555555555",
			Source:         "manual",
			CreatedAt:      ts("2026-06-11T10:00:00Z"),
		},
		UpdatedAt: ts("2026-06-11T10:00:00.000000001Z"),
		Rev:       3,
	}
	want := `{"id":"9a1f0c2e-0000-4000-8000-000000000001",` +
		`"type":"finding",` +
		`"title":"TLS pin mismatch on resurrected node",` +
		`"body":"details *markdown*",` +
		`"tags":["tls","clan"],` +
		`"status":"active",` +
		`"session_id":"9a1f0c2e-0000-4000-8000-00000000000s",` +
		`"provenance":{"author_member_id":"11111111-2222-4333-8444-555555555555","source":"manual","created_at":"2026-06-11T10:00:00Z"},` +
		`"updated_at":"2026-06-11T10:00:00.000000001Z",` +
		`"rev":3}`
	got := string(CanonicalNodeBytes(n))
	if got != want {
		t.Fatalf("canonical encoding drifted:\n got: %s\nwant: %s", got, want)
	}

	// Empty optional fields are omitted — also normative.
	bare := node("9a1f0c2e-0000-4000-8000-000000000002", 1, "2026-06-11T10:00:00Z")
	gotBare := string(CanonicalNodeBytes(bare))
	if strings.Contains(gotBare, `"body"`) || strings.Contains(gotBare, `"tags"`) || strings.Contains(gotBare, `"session_id"`) {
		t.Fatalf("empty optional fields must be omitted, got: %s", gotBare)
	}
}

// ---------- digest (spec §13.5) ----------

func TestDigestEmptyAndPermutationStable(t *testing.T) {
	empty := sha256.Sum256(nil)
	if got := Digest(nil); got != hex.EncodeToString(empty[:]) {
		t.Fatalf("nil graph digest = %s, want sha256 of empty", got)
	}
	if got := Digest(NewGraph()); got != hex.EncodeToString(empty[:]) {
		t.Fatalf("empty graph digest mismatch")
	}

	a := node("aaaaaaaa-0000-4000-8000-000000000001", 1, "2026-06-11T10:00:00Z")
	b := node("bbbbbbbb-0000-4000-8000-000000000002", 2, "2026-06-11T11:00:00Z")
	e1 := edge(a.ID, b.ID, "relates")
	e2 := edge(b.ID, a.ID, "derived_from")

	g1 := graph([]Node{a, b}, []Edge{e1, e2})
	g2 := graph([]Node{b, a}, []Edge{e2, e1}) // permuted
	if Digest(g1) != Digest(g2) {
		t.Fatal("digest must be permutation-invariant (sorted lines)")
	}
}

func TestDigestSensitivity(t *testing.T) {
	a := node("aaaaaaaa-0000-4000-8000-000000000001", 1, "2026-06-11T10:00:00Z")
	base := graph([]Node{a}, nil)
	d0 := Digest(base)

	// Rev bump moves it.
	a2 := a
	a2.Rev = 2
	if Digest(graph([]Node{a2}, nil)) == d0 {
		t.Fatal("rev bump must change digest")
	}
	// Timestamp bump moves it.
	a3 := a
	a3.UpdatedAt = a.UpdatedAt.Add(time.Nanosecond)
	if Digest(graph([]Node{a3}, nil)) == d0 {
		t.Fatal("updated_at bump must change digest")
	}
	// New edge moves it.
	if Digest(graph([]Node{a}, []Edge{edge(a.ID, a.ID, "relates")})) == d0 {
		t.Fatal("edge add must change digest")
	}
	// Archived node still counted (tombstones converge).
	a4 := a
	a4.Status = "archived"
	a4.Rev = 2
	a4.UpdatedAt = a.UpdatedAt.Add(time.Second)
	if Digest(graph([]Node{a4}, nil)) == d0 {
		t.Fatal("archived node must still move the digest")
	}
}

// ---------- merge properties (spec §13.4) ----------

// randomGraph builds a deterministic pseudo-random graph sharing node ids
// with its siblings so merges actually conflict.
func randomGraph(seed int64) *Graph {
	rng := rand.New(rand.NewSource(seed))
	ids := []string{
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003",
		"00000000-0000-4000-8000-000000000004",
	}
	var nodes []Node
	for _, id := range ids {
		if rng.Intn(3) == 0 {
			continue // not every graph has every node
		}
		updated := time.Unix(1765000000+int64(rng.Intn(3)), int64(rng.Intn(2))).UTC()
		n := node(id, uint64(rng.Intn(3)+1), updated.Format(time.RFC3339Nano), func(n *Node) {
			n.Title = fmt.Sprintf("title-%d", rng.Intn(4))
			if rng.Intn(4) == 0 {
				n.Status = "archived"
			}
		})
		nodes = append(nodes, n)
	}
	var edges []Edge
	for i := 0; i < rng.Intn(4); i++ {
		edges = append(edges, edge(ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))], "relates"))
	}
	return graph(nodes, edges)
}

func TestMergeCommutativeAssociativeIdempotent(t *testing.T) {
	for seed := int64(0); seed < 25; seed++ {
		a := randomGraph(seed)
		b := randomGraph(seed + 100)
		c := randomGraph(seed + 200)

		ab := Merge(a, b)
		ba := Merge(b, a)
		if Digest(ab) != Digest(ba) {
			t.Fatalf("seed %d: merge not commutative on digest", seed)
		}
		// Note: commutativity of CONTENT requires the hash tiebreak to be
		// total. Compare full structures, not just digests (digest ignores
		// body/title).
		if !reflect.DeepEqual(ab.Nodes, ba.Nodes) || !reflect.DeepEqual(ab.Edges, ba.Edges) {
			// Edge metadata may differ when the same key exists on both
			// sides with different metadata ("local wins" is direction-
			// dependent by design). Strip metadata before comparing edges.
			if !edgeKeysEqual(ab.Edges, ba.Edges) || !reflect.DeepEqual(ab.Nodes, ba.Nodes) {
				t.Fatalf("seed %d: merge not commutative on content", seed)
			}
		}

		// Associativity (same caveat on edge metadata).
		abc1 := Merge(Merge(a, b), c)
		abc2 := Merge(a, Merge(b, c))
		if Digest(abc1) != Digest(abc2) || !reflect.DeepEqual(abc1.Nodes, abc2.Nodes) || !edgeKeysEqual(abc1.Edges, abc2.Edges) {
			t.Fatalf("seed %d: merge not associative", seed)
		}

		// Idempotency.
		aa := Merge(a, a)
		if Digest(aa) != Digest(Merge(aa, a)) {
			t.Fatalf("seed %d: merge not idempotent", seed)
		}
	}
}

func edgeKeysEqual(a, b []Edge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if edgeKey(a[i]) != edgeKey(b[i]) {
			return false
		}
	}
	return true
}

func TestLWWWinnerRules(t *testing.T) {
	id := "00000000-0000-4000-8000-00000000000a"

	// Newer timestamp wins regardless of rev.
	older := node(id, 9, "2026-06-11T10:00:00Z")
	newer := node(id, 1, "2026-06-11T10:00:01Z", func(n *Node) { n.Title = "newer" })
	m := Merge(graph([]Node{older}, nil), graph([]Node{newer}, nil))
	if m.Nodes[0].Title != "newer" {
		t.Fatal("newer timestamp must win")
	}

	// Equal timestamp → higher rev wins.
	r1 := node(id, 1, "2026-06-11T10:00:00Z")
	r2 := node(id, 2, "2026-06-11T10:00:00Z", func(n *Node) { n.Title = "rev2" })
	m = Merge(graph([]Node{r1}, nil), graph([]Node{r2}, nil))
	if m.Nodes[0].Title != "rev2" {
		t.Fatal("higher rev must win at equal timestamp")
	}

	// Equal timestamp + rev → hash tiebreak, and BOTH directions agree.
	h1 := node(id, 1, "2026-06-11T10:00:00Z", func(n *Node) { n.Title = "alpha" })
	h2 := node(id, 1, "2026-06-11T10:00:00Z", func(n *Node) { n.Title = "beta" })
	m12 := Merge(graph([]Node{h1}, nil), graph([]Node{h2}, nil))
	m21 := Merge(graph([]Node{h2}, nil), graph([]Node{h1}, nil))
	if m12.Nodes[0].Title != m21.Nodes[0].Title {
		t.Fatal("hash tiebreak must converge both directions")
	}
}

func TestTombstonePropagationAndResurrection(t *testing.T) {
	id := "00000000-0000-4000-8000-00000000000b"
	live := node(id, 1, "2026-06-11T10:00:00Z")
	tomb := node(id, 2, "2026-06-11T10:00:05Z", func(n *Node) { n.Status = "archived" })

	// Tombstone (newer) wins through merge in both directions.
	if got := Merge(graph([]Node{live}, nil), graph([]Node{tomb}, nil)).Nodes[0].Status; got != "archived" {
		t.Fatalf("tombstone must propagate, got %s", got)
	}
	if got := Merge(graph([]Node{tomb}, nil), graph([]Node{live}, nil)).Nodes[0].Status; got != "archived" {
		t.Fatalf("tombstone must survive reverse merge, got %s", got)
	}

	// Documented v1 behavior: an even newer active edit resurrects.
	resurrect := node(id, 3, "2026-06-11T10:00:10Z")
	if got := Merge(graph([]Node{tomb}, nil), graph([]Node{resurrect}, nil)).Nodes[0].Status; got != "active" {
		t.Fatalf("newer edit must beat tombstone (spec §13.4), got %s", got)
	}
}

func TestEdgeUnionDedupLocalWins(t *testing.T) {
	a, b := "00000000-0000-4000-8000-00000000000a", "00000000-0000-4000-8000-00000000000b"
	localEdge := edge(a, b, "relates")
	remoteEdge := edge(a, b, "relates")
	remoteEdge.CreatedBy = "member-z" // different metadata, same key

	m := Merge(graph(nil, []Edge{localEdge}), graph(nil, []Edge{remoteEdge}))
	if len(m.Edges) != 1 {
		t.Fatalf("dup edge must dedup, got %d", len(m.Edges))
	}
	if m.Edges[0].CreatedBy != "member-a" {
		t.Fatal("local edge metadata must win")
	}

	// Different relation = different edge.
	m = Merge(graph(nil, []Edge{localEdge}), graph(nil, []Edge{edge(a, b, "supersedes")}))
	if len(m.Edges) != 2 {
		t.Fatalf("distinct relations must both survive, got %d", len(m.Edges))
	}
}

// ---------- deterministic ids (spec §13.3) ----------

func TestDeterministicEventID(t *testing.T) {
	id1 := DeterministicEventID("clan-1", "member_joined", "member-x", "")
	id2 := DeterministicEventID("clan-1", "member_joined", "member-x", "")
	if id1 != id2 {
		t.Fatal("same inputs must mint same id")
	}
	if DeterministicEventID("clan-2", "member_joined", "member-x", "") == id1 {
		t.Fatal("different clan must mint different id")
	}
	if DeterministicEventID("clan-1", "member_joined", "member-x", "term-7") == id1 {
		t.Fatal("qualifier must change id")
	}
	// UUID shape: 36 chars, version nibble 4, variant in [89ab].
	if len(id1) != 36 || id1[14] != '4' || !strings.ContainsRune("89ab", rune(id1[19])) {
		t.Fatalf("not UUID-shaped: %s", id1)
	}
}

func TestNewUUIDv4Shape(t *testing.T) {
	id, err := NewUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 || id[14] != '4' {
		t.Fatalf("not v4: %s", id)
	}
	id2, _ := NewUUIDv4()
	if id == id2 {
		t.Fatal("two UUIDs collided — rand broken")
	}
}

// ---------- validation ----------

func TestValidateNode(t *testing.T) {
	good := node("00000000-0000-4000-8000-00000000000c", 1, "2026-06-11T10:00:00Z")
	if err := ValidateNode(good); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}
	bad := good
	bad.Type = "nonsense"
	if ValidateNode(bad) == nil {
		t.Fatal("bad type accepted")
	}
	bad = good
	bad.Title = strings.Repeat("x", MaxTitleChars+1)
	if ValidateNode(bad) == nil {
		t.Fatal("oversized title accepted")
	}
	bad = good
	bad.Body = strings.Repeat("x", MaxBodyBytes+1)
	if ValidateNode(bad) == nil {
		t.Fatal("oversized body accepted")
	}
	bad = good
	bad.Tags = make([]string, MaxTags+1)
	if ValidateNode(bad) == nil {
		t.Fatal("too many tags accepted")
	}
	bad = good
	bad.Status = "zombie"
	if ValidateNode(bad) == nil {
		t.Fatal("bad status accepted")
	}
}

func TestValidateEdge(t *testing.T) {
	if err := ValidateEdge(edge("a", "b", "relates")); err != nil {
		t.Fatalf("valid edge rejected: %v", err)
	}
	if ValidateEdge(edge("", "b", "relates")) == nil {
		t.Fatal("empty from accepted")
	}
	if ValidateEdge(edge("a", "b", "friends_with")) == nil {
		t.Fatal("bad relation accepted")
	}
}
