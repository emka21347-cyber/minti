package memory

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/state"
)

type nopAudit struct{}

func (nopAudit) Write(auditlog.Event) error { return nil }

func newTestService(t *testing.T) (*Service, *state.Store) {
	t.Helper()
	store, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(ServiceOpts{
		Store: store, SelfID: "self-member", ClanID: "clan-test", Audit: nopAudit{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, store
}

func TestAddNodeSetsProvenanceFromOrigin(t *testing.T) {
	svc, _ := newTestService(t)
	forged := Node{
		Type: "fact", Title: "water is wet",
		Provenance: Provenance{AuthorMemberID: "i-am-lying", Source: "manual"},
	}
	stored, err := svc.AddOrUpdateNode("real-origin", forged, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Provenance.AuthorMemberID != "real-origin" {
		t.Fatalf("author must be daemon-set from origin, got %q", stored.Provenance.AuthorMemberID)
	}
	if stored.ID == "" || stored.Rev != 1 || stored.Status != "active" {
		t.Fatalf("create defaults wrong: %+v", stored)
	}
	if svc.Digest() == Digest(NewGraph()) {
		t.Fatal("digest must move after a write")
	}
}

func TestUpdatePreservesProvenanceBumpsRev(t *testing.T) {
	svc, _ := newTestService(t)
	created, err := svc.AddOrUpdateNode("author-1", Node{Type: "fact", Title: "v1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	edit := created
	edit.Title = "v2"
	edit.Provenance = Provenance{AuthorMemberID: "editor-2", Source: "scribe"} // must be ignored
	edit.Rev = 99                                                              // must be ignored — rev comes from stored
	updated, err := svc.AddOrUpdateNode("editor-2", edit, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Provenance.AuthorMemberID != "author-1" {
		t.Fatal("provenance must be immutable after create")
	}
	if updated.Rev != 2 {
		t.Fatalf("rev must bump from stored, got %d", updated.Rev)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatal("updated_at must advance")
	}
}

// TestOriginMonotoneStamp is the M0 review's REQUIRED test: a poisoned
// far-future timestamp in the graph must not lock members out — the next
// local edit stamps past it and wins LWW (spec §13.4 HLC-lite).
func TestOriginMonotoneStamp(t *testing.T) {
	svc, _ := newTestService(t)

	poisonTime := time.Now().Add(10 * 365 * 24 * time.Hour).UTC() // ~2036
	poisoned := node("00000000-0000-4000-8000-0000000000ff", 5, poisonTime.Format(time.RFC3339Nano))
	if changed, err := svc.ApplyRemote(graph([]Node{poisoned}, nil), "peer-evil"); err != nil || !changed {
		t.Fatalf("poison merge: changed=%v err=%v", changed, err)
	}

	// An honest edit with today's clock must still WIN over the poisoned node.
	edit := poisoned
	edit.Title = "honest correction"
	stored, err := svc.AddOrUpdateNode("honest-member", edit, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !stored.UpdatedAt.After(poisonTime) {
		t.Fatalf("stamp must exceed poisoned max: got %s vs poison %s", stored.UpdatedAt, poisonTime)
	}

	// And it must survive a re-merge with the original poisoned copy.
	final, err := svc.ApplyRemote(graph([]Node{poisoned}, nil), "peer-evil")
	if err != nil {
		t.Fatal(err)
	}
	_ = final
	snap := svc.Snapshot()
	for _, n := range snap.Nodes {
		if n.ID == poisoned.ID && n.Title != "honest correction" {
			t.Fatal("honest edit lost to poisoned timestamp after re-merge")
		}
	}
}

func TestNodeCapAndEdgeCap(t *testing.T) {
	svc, _ := newTestService(t)
	// Fill to the node cap by injecting via merge (cap-exempt by spec §13.4),
	// then verify the WRITE path rejects.
	nodes := make([]Node, 0, MaxNodes)
	for i := 0; i < MaxNodes; i++ {
		nodes = append(nodes, node(DeterministicEventID("c", "fill", "n", strconv.Itoa(i)), 1, "2026-06-11T10:00:00Z"))
	}
	if changed, err := svc.ApplyRemote(graph(nodes, nil), "filler"); err != nil || !changed {
		t.Fatalf("fill merge failed: %v", err)
	}

	_, err := svc.AddOrUpdateNode("origin", Node{Type: "fact", Title: "one too many"}, time.Now())
	if !errors.Is(err, ErrCap) {
		t.Fatalf("node cap must reject with ErrCap, got %v", err)
	}
	// Updates to EXISTING nodes still work at the cap.
	existing := nodes[0]
	existing.Title = "edited at cap"
	if _, err := svc.AddOrUpdateNode("origin", existing, time.Now()); err != nil {
		t.Fatalf("update at cap must still work: %v", err)
	}

	// Edge cap.
	edges := make([]Edge, 0, MaxEdges)
	for i := 0; i < MaxEdges; i++ {
		edges = append(edges, edge("n"+strconv.Itoa(i), "n"+strconv.Itoa(i+1), "relates"))
	}
	if _, err := svc.ApplyRemote(graph(nil, edges), "filler"); err != nil {
		t.Fatal(err)
	}
	_, err = svc.AddEdge("origin", edge("x", "y", "relates"), time.Now())
	if !errors.Is(err, ErrCap) {
		t.Fatalf("edge cap must reject with ErrCap, got %v", err)
	}
}

func TestEdgeDedupNoOp(t *testing.T) {
	svc, _ := newTestService(t)
	e := edge("from-node", "to-node", "relates")
	added, err := svc.AddEdge("o1", e, time.Now())
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	d1 := svc.Digest()
	added, err = svc.AddEdge("o2", e, time.Now())
	if err != nil || added {
		t.Fatalf("dup add must be no-op: added=%v err=%v", added, err)
	}
	if svc.Digest() != d1 {
		t.Fatal("no-op dup must not move the digest")
	}
}

func TestPersistenceRoundtrip(t *testing.T) {
	store, err := state.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc1, err := NewService(ServiceOpts{Store: store, SelfID: "s", ClanID: "c", Audit: nopAudit{}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc1.AddOrUpdateNode("author", Node{Type: "decision", Title: "persist me"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc1.AddEdge("author", edge(n.ID, n.ID, "relates"), time.Now()); err != nil {
		t.Fatal(err)
	}
	d1 := svc1.Digest()

	// Fresh service over the same dir = daemon restart.
	svc2, err := NewService(ServiceOpts{Store: store, SelfID: "s", ClanID: "c", Audit: nopAudit{}})
	if err != nil {
		t.Fatal(err)
	}
	if svc2.Digest() != d1 {
		t.Fatalf("digest must survive restart: %s vs %s", svc2.Digest(), d1)
	}
	snap := svc2.Snapshot()
	if len(snap.Nodes) != 1 || len(snap.Edges) != 1 {
		t.Fatalf("graph must survive restart: %d nodes %d edges", len(snap.Nodes), len(snap.Edges))
	}
	if snap.Nodes[0].Provenance.AuthorMemberID != "author" {
		t.Fatal("provenance must survive restart")
	}
}

func TestApplyRemoteIdempotentAndConvergent(t *testing.T) {
	svcA, _ := newTestService(t)
	svcB, _ := newTestService(t)

	nA, err := svcA.AddOrUpdateNode("member-a", Node{Type: "finding", Title: "from A"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	nB, err := svcB.AddOrUpdateNode("member-b", Node{Type: "finding", Title: "from B"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = nA
	_ = nB

	// Cross-sync both ways → digests converge.
	if _, err := svcA.ApplyRemote(svcB.Snapshot(), "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := svcB.ApplyRemote(svcA.Snapshot(), "a"); err != nil {
		t.Fatal(err)
	}
	if svcA.Digest() != svcB.Digest() {
		t.Fatalf("digests must converge: %s vs %s", svcA.Digest(), svcB.Digest())
	}

	// Re-applying the same remote is a no-op.
	changed, err := svcA.ApplyRemote(svcB.Snapshot(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("idempotent re-apply must report no change")
	}
}

func TestReplaceSwapsGraph(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.AddOrUpdateNode("o", Node{Type: "fact", Title: "old world"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	fresh := graph([]Node{node("00000000-0000-4000-8000-0000000000aa", 1, "2026-06-11T10:00:00Z")}, nil)
	if err := svc.Replace(fresh, "local-cli"); err != nil {
		t.Fatal(err)
	}
	snap := svc.Snapshot()
	if len(snap.Nodes) != 1 || snap.Nodes[0].ID != "00000000-0000-4000-8000-0000000000aa" {
		t.Fatalf("replace must discard prior graph, got %+v", snap.Nodes)
	}
}
