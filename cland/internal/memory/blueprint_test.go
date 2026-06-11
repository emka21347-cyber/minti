package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBlueprintRoundtrip(t *testing.T) {
	exportNow := ts("2026-06-11T12:00:00Z")
	g := graph(
		[]Node{node("n1", 1, "2026-06-11T10:00:00Z"), node("n2", 2, "2026-06-11T10:00:00Z")},
		[]Edge{edge("n1", "n2", "relates")},
	)

	bp, err := ExportBlueprint(g, "clan-xyz", "", false, exportNow)
	if err != nil {
		t.Fatalf("ExportBlueprint error: %v", err)
	}

	if bp.Kind != BlueprintKind {
		t.Errorf("Kind = %q, want %q", bp.Kind, BlueprintKind)
	}
	if bp.FormatVersion != 1 {
		t.Errorf("FormatVersion = %d, want 1", bp.FormatVersion)
	}
	if !strings.HasPrefix(bp.SourceClan, "sha256:") || len(bp.SourceClan) != 7+64 {
		t.Errorf("SourceClan = %q, expected sha256: + 64 hex chars", bp.SourceClan)
	}
	if bp.Signature != "" {
		t.Errorf("Signature = %q, want empty", bp.Signature)
	}
	if bp.Stats.Nodes != 2 || bp.Stats.Edges != 1 || bp.Stats.Proposed != 0 || bp.Stats.Archived != 0 {
		t.Errorf("Stats = %+v, want Nodes:2 Edges:1 Proposed:0 Archived:0", bp.Stats)
	}

	if err := ValidateBlueprint(bp); err != nil {
		t.Fatalf("ValidateBlueprint error: %v", err)
	}

	data, err := json.Marshal(bp)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var bp2 Blueprint
	if err := json.Unmarshal(data, &bp2); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if err := ValidateBlueprint(&bp2); err != nil {
		t.Fatalf("ValidateBlueprint on roundtrip error: %v", err)
	}
}

func TestBlueprintChecksumTamperRejected(t *testing.T) {
	exportNow := ts("2026-06-11T12:00:00Z")
	g := graph(
		[]Node{node("n1", 1, "2026-06-11T10:00:00Z")},
		nil,
	)

	bp, err := ExportBlueprint(g, "clan-xyz", "", false, exportNow)
	if err != nil {
		t.Fatalf("ExportBlueprint error: %v", err)
	}

	bp.Graph.Nodes[0].Title += "x"
	err = ValidateBlueprint(bp)
	if err == nil {
		t.Fatal("expected error for tampered node title, got nil")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %v, want containing 'checksum'", err)
	}

	bp.Graph.Nodes[0].Title = strings.TrimSuffix(bp.Graph.Nodes[0].Title, "x")

	checksum := bp.ChecksumSHA256
	if len(checksum) < 1 {
		t.Fatal("checksum too short")
	}
	flipChar := checksum[0]
	newChar := 'f'
	if flipChar == '0' {
		newChar = 'f'
	} else {
		newChar = '0'
	}
	bp.ChecksumSHA256 = string(newChar) + checksum[1:]

	err = ValidateBlueprint(bp)
	if err == nil {
		t.Fatal("expected error for tampered checksum, got nil")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %v, want containing 'checksum'", err)
	}
}

func TestBlueprintVersionGate(t *testing.T) {
	exportNow := ts("2026-06-11T12:00:00Z")
	g := graph(
		[]Node{node("n1", 1, "2026-06-11T10:00:00Z")},
		nil,
	)

	bp, err := ExportBlueprint(g, "clan-xyz", "", false, exportNow)
	if err != nil {
		t.Fatalf("ExportBlueprint error: %v", err)
	}

	bp.FormatVersion = BlueprintFormatVersion + 1
	bp.ChecksumSHA256 = BlueprintChecksum(bp)
	err = ValidateBlueprint(bp)
	if err == nil {
		t.Fatal("expected error for new version, got nil")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Errorf("error = %v, want containing 'newer than supported'", err)
	}

	bp.FormatVersion = 0
	bp.ChecksumSHA256 = BlueprintChecksum(bp)
	err = ValidateBlueprint(bp)
	if err == nil {
		t.Fatal("expected error for version 0, got nil")
	}
	if !strings.Contains(err.Error(), "invalid format_version") {
		t.Errorf("error = %v, want containing 'invalid format_version'", err)
	}
}

func TestBlueprintKindGate(t *testing.T) {
	exportNow := ts("2026-06-11T12:00:00Z")
	g := graph(
		[]Node{node("n1", 1, "2026-06-11T10:00:00Z")},
		nil,
	)

	bp, err := ExportBlueprint(g, "clan-xyz", "", false, exportNow)
	if err != nil {
		t.Fatalf("ExportBlueprint error: %v", err)
	}

	bp.Kind = "something-else"
	bp.ChecksumSHA256 = BlueprintChecksum(bp)
	err = ValidateBlueprint(bp)
	if err == nil {
		t.Fatal("expected error for wrong kind, got nil")
	}
	if !strings.Contains(err.Error(), "not a clan blueprint") {
		t.Errorf("error = %v, want containing 'not a clan blueprint'", err)
	}
}

func TestStripAuthorsPseudonymizes(t *testing.T) {
	exportNow := ts("2026-06-11T12:00:00Z")
	n1 := node("n1", 1, "2026-06-11T10:00:00Z")
	n1.Provenance.AuthorMemberID = "zzz-author"

	n2 := node("n2", 2, "2026-06-11T10:00:00Z")
	n2.Provenance.AuthorMemberID = "aaa-author"

	mmmNode := node("mmm-member", 3, "2026-06-11T10:00:00Z")
	mmmNode.Type = "member"
	mmmNode.Provenance.AuthorMemberID = "aaa-author"
	// Titles are free text — strip-authors pseudonymizes IDENTIFIERS, not
	// prose (spec §13.11). The node() helper embeds the id in the title;
	// give this one a clean title so the no-leak assertion tests the
	// identifier paths, not the fixture.
	mmmNode.Title = "the thinkpad in the corner"

	g := graph(
		[]Node{n1, n2, mmmNode},
		[]Edge{edge("n1", "mmm-member", "about_member")},
	)

	bp, err := ExportBlueprint(g, "clan-xyz", "", true, exportNow)
	if err != nil {
		t.Fatalf("ExportBlueprint error: %v", err)
	}

	// Distinct identity set sorted: [aaa-author, member-a, mmm-member,
	// zzz-author] -> member-1..member-4. Only the member-TYPE node's ID is
	// renamed; ordinary node ids (n1, n2) stay.
	nodes := bp.Graph.Nodes
	if len(nodes) != 3 {
		t.Fatalf("node count = %d, want 3", len(nodes))
	}
	n1Found, n2Found, memberFound := false, false, false
	for _, n := range nodes {
		switch {
		case n.ID == "n1":
			n1Found = true
			if n.Provenance.AuthorMemberID != "member-4" {
				t.Errorf("n1 author = %q, want member-4 (zzz-author sorts last)", n.Provenance.AuthorMemberID)
			}
		case n.ID == "n2":
			n2Found = true
			if n.Provenance.AuthorMemberID != "member-1" {
				t.Errorf("n2 author = %q, want member-1 (aaa-author sorts first)", n.Provenance.AuthorMemberID)
			}
		case n.Type == "member":
			memberFound = true
			if n.ID != "member-3" {
				t.Errorf("member node ID = %q, want member-3 (mmm-member sorts third)", n.ID)
			}
		}
	}
	if !n1Found || !n2Found || !memberFound {
		t.Fatalf("missing nodes in output: n1=%v n2=%v member=%v", n1Found, n2Found, memberFound)
	}

	var edgeToMember *Edge
	for i := range bp.Graph.Edges {
		e := &bp.Graph.Edges[i]
		if e.From == "n1" && e.To == "member-3" {
			edgeToMember = e
			break
		}
	}
	if edgeToMember == nil {
		t.Fatal("edge to member not found")
	}
	if edgeToMember.CreatedBy != "member-2" {
		t.Errorf("edge CreatedBy = %q, want member-2", edgeToMember.CreatedBy)
	}

	data, err := json.Marshal(bp)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	strData := string(data)
	if strings.Contains(strData, "zzz-author") {
		t.Error("serialized data contains 'zzz-author'")
	}
	if strings.Contains(strData, "mmm-member") {
		t.Error("serialized data contains 'mmm-member'")
	}

	if err := ValidateBlueprint(bp); err != nil {
		t.Fatalf("ValidateBlueprint error: %v", err)
	}
}

func TestSessionFilteredExport(t *testing.T) {
	exportNow := ts("2026-06-11T12:00:00Z")
	sessNode := node("sess-1", 1, "2026-06-11T10:00:00Z")
	sessNode.Type = "research_session"

	f1 := node("f1", 2, "2026-06-11T10:00:00Z")
	f1.SessionID = "sess-1"

	f2 := node("f2", 3, "2026-06-11T10:00:00Z")

	f3 := node("f3", 4, "2026-06-11T10:00:00Z")

	g := graph(
		[]Node{sessNode, f1, f2, f3},
		[]Edge{
			edge("f1", "sess-1", "contributes_to"),
			edge("f3", "sess-1", "contributes_to"),
			edge("f2", "f3", "relates"),
		},
	)

	bp, err := ExportBlueprint(g, "c", "sess-1", false, exportNow)
	if err != nil {
		t.Fatalf("ExportBlueprint error: %v", err)
	}

	nodeIDs := make(map[string]bool)
	for _, n := range bp.Graph.Nodes {
		nodeIDs[n.ID] = true
	}
	expectedNodes := map[string]bool{"sess-1": true, "f1": true, "f3": true}
	if !reflect.DeepEqual(nodeIDs, expectedNodes) {
		t.Errorf("kept nodes = %v, want %v", nodeIDs, expectedNodes)
	}

	edgeCount := 0
	for _, e := range bp.Graph.Edges {
		if e.Relation == "contributes_to" {
			edgeCount++
		}
	}
	if edgeCount != 2 {
		t.Errorf("kept edges = %d, want 2", edgeCount)
	}

	for _, e := range bp.Graph.Edges {
		if e.Relation == "relates" {
			t.Error("edge 'relates' should have been dropped")
		}
	}

	_, err = ExportBlueprint(g, "c", "nope", false, exportNow)
	if err == nil {
		t.Fatal("expected error for non-existent session, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want containing 'not found'", err)
	}
}

func TestMarkImportedFlipsSourceOnly(t *testing.T) {
	n := node("n1", 3, "2026-06-11T10:00:00Z")
	g := graph([]Node{n}, nil)

	m := MarkImported(g)

	if m.Nodes[0].Provenance.Source != "import" {
		t.Errorf("source = %q, want import", m.Nodes[0].Provenance.Source)
	}
	if m.Nodes[0].Rev != 3 {
		t.Errorf("rev = %d, want 3", m.Nodes[0].Rev)
	}
	if !m.Nodes[0].UpdatedAt.Equal(ts("2026-06-11T10:00:00Z")) {
		t.Error("updatedAt changed")
	}

	if g.Nodes[0].Provenance.Source != "manual" {
		t.Errorf("original source = %q, want manual (deep copy violated)", g.Nodes[0].Provenance.Source)
	}

	m2 := MarkImported(m)
	if !reflect.DeepEqual(m.Nodes, m2.Nodes) {
		t.Error("MarkImported is not idempotent on nodes")
	}
}

func TestBlueprintChecksumByteStability(t *testing.T) {
	bp := &Blueprint{
		Kind:            BlueprintKind,
		FormatVersion:   1,
		ExportedAt:      ts("2026-06-11T12:00:00Z"),
		SourceClan:      "sha256:ab",
		SessionFilter:   "",
		Stats:           BlueprintStats{},
		Graph:           graph(nil, nil),
		Signature:       "",
		ChecksumSHA256:  "",
	}

	c1 := BlueprintChecksum(bp)
	c2 := BlueprintChecksum(bp)
	if c1 != c2 {
		t.Errorf("checksums differ: %q vs %q", c1, c2)
	}

	bp.ChecksumSHA256 = ""
	data, err := json.Marshal(*bp)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	h := sha256.Sum256(data)
	c3 := hex.EncodeToString(h[:])
	if c1 != c3 {
		t.Errorf("checksum from marshal = %q, want %q", c3, c1)
	}
}
