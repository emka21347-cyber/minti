package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/state"
)

// ErrCap is wrapped by every cap-rejection so handlers can map it to 409.
var ErrCap = errors.New("memory: cap exceeded")

// Service owns the in-memory working copy of the graph, the cached digest,
// and persistence. All mutations go through it; the digest is recomputed
// under the same mutex that applies the mutation (M0 review note), so the
// election heartbeat's MemoryDigest closure never observes a stale value and
// never touches memory.json (spec §13.5 cost discipline).
type Service struct {
	mu     sync.Mutex
	store  *state.Store
	selfID string
	clanID string
	audit  auditlog.Logger
	log    *slog.Logger

	g      *Graph
	digest string
	// maxUpdated backs the §13.4 origin-monotone (HLC-lite) stamp: the write
	// path stamps max(now, maxUpdated+1ns), so a poisoned far-future
	// timestamp in the graph can never lock members out of editing.
	maxUpdated time.Time
}

// ServiceOpts is the dependency bundle for NewService.
type ServiceOpts struct {
	Store  *state.Store
	SelfID string
	ClanID string
	Audit  auditlog.Logger
	Log    *slog.Logger
}

// NewService loads memory.json (missing = empty graph), computes the initial
// digest, and returns a ready Service.
func NewService(opts ServiceOpts) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("memory: Store required")
	}
	if opts.SelfID == "" {
		return nil, errors.New("memory: SelfID required")
	}
	if opts.Audit == nil {
		return nil, errors.New("memory: Audit required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	g := NewGraph()
	found, err := opts.Store.LoadMemory(g)
	if err != nil {
		return nil, fmt.Errorf("memory: load: %w", err)
	}
	if !found {
		g = NewGraph()
	}
	s := &Service{
		store:  opts.Store,
		selfID: opts.SelfID,
		clanID: opts.ClanID,
		audit:  opts.Audit,
		log:    opts.Log,
		g:      g,
	}
	s.digest = Digest(g)
	for _, n := range g.Nodes {
		if n.UpdatedAt.After(s.maxUpdated) {
			s.maxUpdated = n.UpdatedAt
		}
	}
	return s, nil
}

// Digest returns the cached content-versioned digest. Safe for the 2 s
// heartbeat path — no I/O, no hashing.
func (s *Service) Digest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.digest
}

// GraphJSON returns the current graph serialized, for GET /clan/memory and
// the CLI. Serializing under the lock gives a consistent snapshot without
// deep-copying slices.
func (s *Service) GraphJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal(s.g)
}

// Snapshot returns a deep copy of the graph for callers that need to walk it
// (blueprint export, scribe consolidation).
func (s *Service) Snapshot() *Graph {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Merge(s.g, nil) // Merge copies + sorts; nil remote = identity
}

// stampLocked returns the §13.4 origin-monotone timestamp and advances the
// high-water mark. Caller holds s.mu.
func (s *Service) stampLocked(now time.Time) time.Time {
	ts := now
	if !ts.After(s.maxUpdated) {
		ts = s.maxUpdated.Add(time.Nanosecond)
	}
	s.maxUpdated = ts
	return ts
}

// AddOrUpdateNode applies one node write (spec §13.6). origin is the
// HMAC-authenticated member id — it ALWAYS becomes the author on create;
// client-supplied provenance is ignored on update (immutable after create).
// Returns the stored node.
func (s *Service) AddOrUpdateNode(origin string, n Node, now time.Time) (Node, error) {
	if origin == "" {
		return Node{}, errors.New("memory: no authenticated origin")
	}
	if n.Status == "" {
		n.Status = "active"
	}
	if err := ValidateNode(n); err != nil {
		return Node{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.g.Nodes {
		if s.g.Nodes[i].ID == n.ID {
			idx = i
			break
		}
	}

	if idx < 0 {
		// Create.
		if len(s.g.Nodes) >= MaxNodes {
			return Node{}, fmt.Errorf("%w: %d nodes (max %d)", ErrCap, len(s.g.Nodes), MaxNodes)
		}
		if n.ID == "" {
			id, err := NewUUIDv4()
			if err != nil {
				return Node{}, fmt.Errorf("memory: mint id: %w", err)
			}
			n.ID = id
		}
		src := n.Provenance.Source
		if src == "" {
			src = "manual"
		}
		n.Provenance = Provenance{AuthorMemberID: origin, Source: src, CreatedAt: now.UTC()}
		n.Rev = 1
		n.UpdatedAt = s.stampLocked(now.UTC())
		candidate := append(append([]Node{}, s.g.Nodes...), n)
		if err := s.checkSizeLocked(candidate, s.g.Edges); err != nil {
			return Node{}, err
		}
		s.g.Nodes = candidate
	} else {
		// Update: provenance + created identity are immutable; rev bumps from
		// the STORED node so a stale client snapshot can't rewind history.
		old := s.g.Nodes[idx]
		n.ID = old.ID
		n.Provenance = old.Provenance
		n.Rev = old.Rev + 1
		n.UpdatedAt = s.stampLocked(now.UTC())
		candidate := append([]Node{}, s.g.Nodes...)
		candidate[idx] = n
		if err := s.checkSizeLocked(candidate, s.g.Edges); err != nil {
			return Node{}, err
		}
		s.g.Nodes = candidate
	}

	if err := s.persistLocked(); err != nil {
		return Node{}, err
	}
	s.auditEvent("memory.node", origin, map[string]any{
		"id": n.ID, "type": n.Type, "status": n.Status, "rev": n.Rev,
	})
	return n, nil
}

// AddEdge applies one edge write (spec §13.6). Duplicate (from,to,relation)
// is a no-op returning added=false. CreatedBy/CreatedAt are daemon-set.
func (s *Service) AddEdge(origin string, e Edge, now time.Time) (added bool, err error) {
	if origin == "" {
		return false, errors.New("memory: no authenticated origin")
	}
	if err := ValidateEdge(e); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ex := range s.g.Edges {
		if edgeKey(ex) == edgeKey(e) {
			return false, nil
		}
	}
	if len(s.g.Edges) >= MaxEdges {
		return false, fmt.Errorf("%w: %d edges (max %d)", ErrCap, len(s.g.Edges), MaxEdges)
	}
	e.CreatedAt = now.UTC()
	e.CreatedBy = origin
	candidate := append(append([]Edge{}, s.g.Edges...), e)
	if err := s.checkSizeLocked(s.g.Nodes, candidate); err != nil {
		return false, err
	}
	s.g.Edges = candidate

	if err := s.persistLocked(); err != nil {
		return false, err
	}
	s.auditEvent("memory.edge", origin, map[string]any{
		"from": e.From, "to": e.To, "relation": e.Relation,
	})
	return true, nil
}

// ApplyRemote merges a remote graph into the local one (gossip sync §13.5,
// merge-import §13.10). Permissive about caps per §13.4. Returns whether the
// local graph changed; persists + recomputes the digest only when it did.
func (s *Service) ApplyRemote(remote *Graph, from string) (changed bool, err error) {
	if remote == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.digest
	merged := Merge(s.g, remote)
	mergedDigest := Digest(merged)
	if mergedDigest == prev {
		return false, nil
	}
	s.g = merged
	for _, n := range merged.Nodes {
		if n.UpdatedAt.After(s.maxUpdated) {
			s.maxUpdated = n.UpdatedAt
		}
	}
	if err := s.persistLocked(); err != nil {
		return true, err
	}
	s.auditEvent("memory.sync", from, map[string]any{
		"prev_digest": prev, "new_digest": s.digest,
		"nodes": len(merged.Nodes), "edges": len(merged.Edges),
	})
	return true, nil
}

// Replace swaps the entire graph (loopback-only import --replace, §13.10).
// Destructive by contract; the caller enforces the loopback restriction.
func (s *Service) Replace(g *Graph, from string) error {
	if g == nil {
		return errors.New("memory: Replace(nil)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.digest
	s.g = Merge(g, nil) // copy + canonical sort
	s.maxUpdated = time.Time{}
	for _, n := range s.g.Nodes {
		if n.UpdatedAt.After(s.maxUpdated) {
			s.maxUpdated = n.UpdatedAt
		}
	}
	if err := s.persistLocked(); err != nil {
		return err
	}
	s.auditEvent("memory.import", from, map[string]any{
		"mode": "replace", "prev_digest": prev, "new_digest": s.digest,
		"nodes": len(s.g.Nodes), "edges": len(s.g.Edges),
	})
	return nil
}

// RecordSystemEvent writes (or LWW-refreshes) a spec §13.7.1 system node:
// deterministic id so every observer mints the same node and the gossip
// union dedups; source "system"; type "event". Best-effort — callers log
// and continue on error (a full graph must never break membership flows).
func (s *Service) RecordSystemEvent(kind, subject, qualifier, title string, now time.Time) error {
	n := Node{
		ID:         DeterministicEventID(s.clanID, kind, subject, qualifier),
		Type:       "event",
		Title:      title,
		Status:     "active",
		Provenance: Provenance{Source: "system"},
	}
	_, err := s.AddOrUpdateNode(s.selfID, n, now)
	return err
}

// checkSizeLocked enforces the 2 MiB serialized cap on a candidate write.
// Caller holds s.mu.
func (s *Service) checkSizeLocked(nodes []Node, edges []Edge) error {
	candidate := Graph{FormatVersion: s.g.FormatVersion, Nodes: nodes, Edges: edges}
	b, err := json.Marshal(&candidate)
	if err != nil {
		return fmt.Errorf("memory: size check: %w", err)
	}
	if len(b) > MaxSerializedBytes {
		return fmt.Errorf("%w: %d bytes serialized (max %d)", ErrCap, len(b), MaxSerializedBytes)
	}
	return nil
}

// persistLocked saves the graph and refreshes the cached digest. Caller
// holds s.mu. Mutation + digest swap under one lock — the heartbeat closure
// can never see a digest that disagrees with the persisted graph.
func (s *Service) persistLocked() error {
	if err := s.store.SaveMemory(s.g); err != nil {
		return fmt.Errorf("memory: persist: %w", err)
	}
	s.digest = Digest(s.g)
	return nil
}

func (s *Service) auditEvent(tool, actor string, args map[string]any) {
	_ = s.audit.Write(auditlog.Event{
		MemberID: s.selfID,
		Server:   "minti-cland",
		Tool:     tool,
		Decision: "allow",
		Reason:   actor,
		Args:     args,
	})
}
