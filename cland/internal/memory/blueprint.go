package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// BlueprintKind is the JSON "kind" discriminator for clan blueprints.
const BlueprintKind = "minti-clan-blueprint"

// BlueprintFormatVersion is the current version of the blueprint schema.
const BlueprintFormatVersion = 1

// BlueprintStats holds aggregate counts derived from a graph snapshot.
type BlueprintStats struct {
	Nodes    int `json:"nodes"`
	Edges    int `json:"edges"`
	Proposed int `json:"proposed"` // count of nodes with Status "proposed"
	Archived int `json:"archived"` // count of nodes with Status "archived"
}

// Blueprint represents a serialized, signed snapshot of a clan's knowledge graph.
// Defined per spec §13.10 — Clan Blueprint.
type Blueprint struct {
	Kind           string         `json:"kind"`
	FormatVersion  int            `json:"format_version"`
	ExportedAt     time.Time      `json:"exported_at"`
	SourceClan     string         `json:"source_clan"`
	SessionFilter  string         `json:"session_filter"`
	Stats          BlueprintStats `json:"stats"`
	Graph          *Graph         `json:"graph"`
	Signature      string         `json:"signature"`
	ChecksumSHA256 string         `json:"checksum_sha256"`
}

// ExportBlueprint creates a deep, sorted copy of the graph g, optionally filtering
// by session and stripping author PII. Returns an error if g is nil or the filter
// cannot be resolved.
func ExportBlueprint(g *Graph, clanID, sessionFilter string, stripAuthors bool, now time.Time) (*Blueprint, error) {
	if g == nil {
		return nil, fmt.Errorf("memory: export of nil graph")
	}

	gCopy := Merge(g, nil)

	if sessionFilter != "" {
		var foundNode *Node
		for i := range gCopy.Nodes {
			if gCopy.Nodes[i].ID == sessionFilter {
				foundNode = &gCopy.Nodes[i]
				break
			}
		}
		if foundNode == nil {
			return nil, fmt.Errorf("memory: session %q not found", sessionFilter)
		}

		keep := make(map[string]bool)
		for i := range gCopy.Nodes {
			n := &gCopy.Nodes[i]
			if n.ID == sessionFilter || n.SessionID == sessionFilter {
				keep[n.ID] = true
			}
		}
		// Contributions point node --contributes_to--> session (spec §13.7),
		// so the contributing node is the edge's FROM side.
		for i := range gCopy.Edges {
			e := &gCopy.Edges[i]
			if e.To == sessionFilter && e.Relation == "contributes_to" {
				keep[e.From] = true
			}
		}

		var newNodes []Node
		for i := range gCopy.Nodes {
			n := gCopy.Nodes[i]
			if keep[n.ID] {
				newNodes = append(newNodes, n)
			}
		}
		gCopy.Nodes = newNodes

		var newEdges []Edge
		for i := range gCopy.Edges {
			e := gCopy.Edges[i]
			if keep[e.From] && keep[e.To] {
				newEdges = append(newEdges, e)
			}
		}
		gCopy.Edges = newEdges
	}

	if stripAuthors {
		memberIDs := make(map[string]bool)
		for i := range gCopy.Nodes {
			if gCopy.Nodes[i].Type == "member" {
				memberIDs[gCopy.Nodes[i].ID] = true
			}
		}

		// The distinct identity set spans authors, edge creators, AND the ids
		// of member-type nodes — all three leak member identity (spec §13.10).
		// Member-node ids MUST be in the set even when that member authored
		// nothing, or the rename below would erase their id to "".
		allIdentities := make(map[string]bool)
		for i := range gCopy.Nodes {
			if gCopy.Nodes[i].Provenance.AuthorMemberID != "" {
				allIdentities[gCopy.Nodes[i].Provenance.AuthorMemberID] = true
			}
		}
		for i := range gCopy.Edges {
			if gCopy.Edges[i].CreatedBy != "" {
				allIdentities[gCopy.Edges[i].CreatedBy] = true
			}
		}
		for id := range memberIDs {
			allIdentities[id] = true
		}

		var sortedKeys []string
		for k := range allIdentities {
			sortedKeys = append(sortedKeys, k)
		}
		sort.Strings(sortedKeys)

		mapping := make(map[string]string)
		for i, k := range sortedKeys {
			mapping[k] = fmt.Sprintf("member-%d", i+1)
		}

		for i := range gCopy.Nodes {
			n := &gCopy.Nodes[i]
			if pseudo, ok := mapping[n.Provenance.AuthorMemberID]; ok {
				n.Provenance.AuthorMemberID = pseudo
			}
			if memberIDs[n.ID] {
				n.ID = mapping[n.ID]
			}
		}

		for i := range gCopy.Edges {
			e := &gCopy.Edges[i]
			if pseudo, ok := mapping[e.CreatedBy]; ok {
				e.CreatedBy = pseudo
			}
			if memberIDs[e.From] {
				e.From = mapping[e.From]
			}
			if memberIDs[e.To] {
				e.To = mapping[e.To]
			}
		}
	}

	stats := computeStats(gCopy)

	clanHash := sha256.Sum256([]byte(clanID))

	bp := &Blueprint{
		Kind:          BlueprintKind,
		FormatVersion: BlueprintFormatVersion,
		ExportedAt:    now.UTC(),
		SourceClan:    "sha256:" + hex.EncodeToString(clanHash[:]),
		SessionFilter: sessionFilter,
		Stats:         stats,
		Graph:         gCopy,
		Signature:     "",
	}
	bp.ChecksumSHA256 = BlueprintChecksum(bp)

	return bp, nil
}

// computeStats calculates the aggregate statistics for a graph.
func computeStats(g *Graph) BlueprintStats {
	stats := BlueprintStats{
		Nodes: len(g.Nodes),
		Edges: len(g.Edges),
	}
	for i := range g.Nodes {
		if g.Nodes[i].Status == "proposed" {
			stats.Proposed++
		}
		if g.Nodes[i].Status == "archived" {
			stats.Archived++
		}
	}
	return stats
}

// BlueprintChecksum computes the SHA-256 checksum of the blueprint's JSON representation.
func BlueprintChecksum(b *Blueprint) string {
	bCopy := *b
	bCopy.ChecksumSHA256 = ""
	data, err := json.Marshal(bCopy)
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// ValidateBlueprint checks the structural integrity and authenticity of a blueprint.
func ValidateBlueprint(b *Blueprint) error {
	if b == nil {
		return fmt.Errorf("memory: nil blueprint")
	}
	if b.Kind != BlueprintKind {
		return fmt.Errorf("memory: not a clan blueprint (kind %q)", b.Kind)
	}
	if b.FormatVersion > BlueprintFormatVersion {
		return fmt.Errorf("memory: blueprint format_version %d is newer than supported %d", b.FormatVersion, BlueprintFormatVersion)
	}
	if b.FormatVersion < 1 {
		return fmt.Errorf("memory: invalid format_version %d", b.FormatVersion)
	}
	if b.Graph == nil {
		return fmt.Errorf("memory: blueprint has no graph")
	}
	expected := BlueprintChecksum(b)
	if expected != b.ChecksumSHA256 {
		return fmt.Errorf("memory: checksum mismatch — file corrupted or tampered")
	}
	return nil
}

// MarkImported returns a deep copy of the graph where every node's provenance source
// is marked as "import". This operation is idempotent regarding provenance updates.
func MarkImported(g *Graph) *Graph {
	gCopy := Merge(g, nil)
	for i := range gCopy.Nodes {
		gCopy.Nodes[i].Provenance.Source = "import"
	}
	return gCopy
}
