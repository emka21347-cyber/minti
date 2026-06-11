package memory

// Scribe distillation duty (spec §13.9) — the Karpathy move: activity in,
// curated memory out, a human between. Runs ONLY while this member is the
// elected Scribe (§13.8). A debounced ticker watches three sources — the
// workspace chat sessions for this Clan, this member's own audit log, and
// fresh findings in open research sessions — and prompts a SMALL local model
// (via the runtime-adapter's OpenAI surface) to propose at most five durable
// memories per pass. Everything lands status:"proposed" + source:"scribe";
// nothing becomes active without an explicit human promote.
//
// Cost discipline: smallest resident model, 120 s debounce, single-flight
// (one distillation at a time — our v1 "skip if busy" guard; the runtime
// doesn't expose load), per-pass cap of 5, and the §13.9 pending-proposal
// budget (no new proposals while >200 of our own sit un-reviewed).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minti/cland/internal/auditlog"
)

// DefaultChatDir is where the workspace persists this Clan's chat sessions
// (spec §13.9 input #1). The MINTI_CLAND_SCRIBE_CHATDIR env override is
// applied by NewScribe.
func DefaultChatDir(clanID string) string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "MINTI", "workspace", "sessions", clanID)
	}
	return filepath.Join("/var/lib/minti/workspace/sessions", clanID)
}

const (
	// DefaultScribeInterval is the §13.9 debounce.
	DefaultScribeInterval = 120 * time.Second
	// MaxProposalsPerPass caps one distillation's output.
	MaxProposalsPerPass = 5
	// PendingProposalBudget is the §13.9 budget: the scribe refuses to mint
	// NEW proposals while more than this many of its own sit un-reviewed.
	PendingProposalBudget = 200
	// maxActivityBytes bounds the prompt (small models, small contexts).
	maxActivityBytes = 6 << 10
)

// Proposal is one distilled memory candidate as the model must emit it.
type Proposal struct {
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	SessionID string   `json:"session_id"`
	Tags      []string `json:"tags"`
}

// proposalTypes the scribe may emit (a subset of NodeTypes — no sessions,
// members, events or artifacts from a language model).
var proposalTypes = map[string]bool{
	"fact": true, "finding": true, "decision": true, "skill": true,
}

// ScribeOpts is the dependency bundle.
type ScribeOpts struct {
	Service  *Service
	SelfID   string
	ClanID   string
	IsScribe func() bool // true while WE hold the scribe role (§13.8)

	RuntimeBase string // runtime-adapter base URL (OpenAI surface)
	PickModel   func(ctx context.Context) string // smallest resident model; "" = none available

	ChatDir   string // workspace chat sessions dir for this Clan ("" = source disabled)
	AuditPath string // this member's own audit log ("" = source disabled)

	Audit    auditlog.Logger
	Log      *slog.Logger
	Interval time.Duration
	HTTP     *http.Client
}

// Scribe is the distillation loop.
type Scribe struct {
	opts ScribeOpts

	// High-water marks: in-memory, seeded at startup (boot = current EOF /
	// now), so a restart distills only NEW activity — never the backlog.
	fileOffsets  map[string]int64
	findingsMark time.Time
	seeded       bool
}

// NewScribe validates opts and returns a ready Scribe.
func NewScribe(opts ScribeOpts) (*Scribe, error) {
	if opts.Service == nil || opts.SelfID == "" || opts.IsScribe == nil {
		return nil, errors.New("scribe: Service, SelfID, IsScribe required")
	}
	if opts.RuntimeBase == "" {
		return nil, errors.New("scribe: RuntimeBase required")
	}
	if opts.PickModel == nil {
		return nil, errors.New("scribe: PickModel required")
	}
	if opts.Audit == nil {
		return nil, errors.New("scribe: Audit required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultScribeInterval
	}
	// Smoke hatch: a localhost rig wants second-scale passes.
	if v := os.Getenv("MINTI_CLAND_SCRIBE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			opts.Interval = d
		}
	}
	if v := os.Getenv("MINTI_CLAND_SCRIBE_CHATDIR"); v != "" {
		opts.ChatDir = v
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 90 * time.Second}
	}
	return &Scribe{opts: opts, fileOffsets: map[string]int64{}}, nil
}

// Run blocks until ctx cancels. Single goroutine; ticks are synchronous, so
// a slow model naturally single-flights the loop.
func (s *Scribe) Run(ctx context.Context) {
	t := time.NewTicker(s.opts.Interval)
	defer t.Stop()
	s.opts.Log.Info("scribe loop started",
		"interval", s.opts.Interval, "chat_dir", s.opts.ChatDir)
	for {
		select {
		case <-ctx.Done():
			s.opts.Log.Info("scribe loop stopped")
			return
		case <-t.C:
			s.tick(ctx, time.Now())
		}
	}
}

// tick is one distillation attempt. Exported for tests via TickForTest.
func (s *Scribe) tick(ctx context.Context, now time.Time) {
	if !s.seeded {
		// First tick after boot: record EOFs/now WITHOUT distilling, so we
		// only ever process activity that happened while we were watching.
		s.gather(now)
		s.seeded = true
		return
	}
	if !s.opts.IsScribe() {
		// Not (or no longer) the scribe — keep marks fresh so a later
		// election doesn't dump the interim backlog on the model.
		s.gather(now)
		return
	}
	if n := s.pendingProposals(); n > PendingProposalBudget {
		s.opts.Log.Warn("scribe: pending-proposal budget reached; skipping pass",
			"pending", n, "budget", PendingProposalBudget)
		s.gather(now) // marks still advance
		return
	}

	activity, fresh := s.gather(now)
	if !fresh {
		return
	}
	model := s.opts.PickModel(ctx)
	if model == "" {
		s.opts.Log.Debug("scribe: no resident model; skipping pass")
		return
	}

	proposals, err := s.distill(ctx, model, activity)
	if err != nil {
		s.opts.Log.Warn("scribe: distillation failed", "model", model, "err", err)
		return
	}
	wrote := s.writeProposals(proposals, now)
	if wrote > 0 {
		s.opts.Log.Info("scribe: proposals written", "count", wrote, "model", model)
		_ = s.opts.Audit.Write(auditlog.Event{
			MemberID: s.opts.SelfID,
			Server:   "minti-cland",
			Tool:     "memory.scribe",
			Decision: "allow",
			Reason:   "distilled",
			Args:     map[string]any{"proposals": wrote, "model": model},
		})
	}
}

// TickForTest drives one synchronous tick (tests own the clock).
func (s *Scribe) TickForTest(ctx context.Context, now time.Time) { s.tick(ctx, now) }

// pendingProposals counts OUR un-reviewed proposals (§13.9 budget).
func (s *Scribe) pendingProposals() int {
	n := 0
	for _, node := range s.opts.Service.Snapshot().Nodes {
		if node.Status == "proposed" &&
			node.Provenance.Source == "scribe" &&
			node.Provenance.AuthorMemberID == s.opts.SelfID {
			n++
		}
	}
	return n
}

// gather collects new activity since the high-water marks (advancing them)
// and reports whether anything fresh appeared.
func (s *Scribe) gather(now time.Time) (string, bool) {
	var b strings.Builder
	fresh := false

	// 1. Workspace chat sessions (*.jsonl under ChatDir).
	if s.opts.ChatDir != "" {
		if entries, err := os.ReadDir(s.opts.ChatDir); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, name := range names {
				path := filepath.Join(s.opts.ChatDir, name)
				if chunk := s.tailFile(path); chunk != "" {
					fresh = true
					fmt.Fprintf(&b, "--- chat %s ---\n%s\n", name, chunk)
				}
			}
		}
	}

	// 2. Our own audit log (the scribe distills what IT can see — the audit
	// log itself stays local-only per §9.1; only distilled CONTENT gossips).
	if s.opts.AuditPath != "" {
		if chunk := s.tailFile(s.opts.AuditPath); chunk != "" {
			fresh = true
			fmt.Fprintf(&b, "--- clan activity (audit) ---\n%s\n", chunk)
		}
	}

	// 3. Fresh findings in OPEN research sessions.
	snap := s.opts.Service.Snapshot()
	openSessions := map[string]string{}
	for _, n := range snap.Nodes {
		if n.Type == "research_session" && n.Status == "active" {
			openSessions[n.ID] = n.Title
		}
	}
	var freshFindings []Node
	for _, n := range snap.Nodes {
		if n.SessionID == "" || n.Status != "active" {
			continue
		}
		if _, open := openSessions[n.SessionID]; !open {
			continue
		}
		if n.Provenance.Source == "scribe" {
			continue // never re-distill our own output
		}
		if n.UpdatedAt.After(s.findingsMark) {
			freshFindings = append(freshFindings, n)
		}
	}
	if len(freshFindings) > 0 {
		fresh = true
		for _, n := range freshFindings {
			fmt.Fprintf(&b, "--- research [session %s %q] ---\n%s: %s\n%s\n",
				shortID8(n.SessionID), openSessions[n.SessionID], n.Type, n.Title, n.Body)
		}
	}
	s.findingsMark = now

	out := b.String()
	if len(out) > maxActivityBytes {
		out = out[len(out)-maxActivityBytes:] // newest tail wins
	}
	return out, fresh
}

// tailFile returns the bytes appended since the last mark (seeding to EOF on
// first sight) and advances the mark. Truncated/rotated files re-seed.
func (s *Scribe) tailFile(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	size := fi.Size()
	mark, seen := s.fileOffsets[path]
	if !seen || size < mark {
		s.fileOffsets[path] = size
		return ""
	}
	if size == mark {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(mark, io.SeekStart); err != nil {
		return ""
	}
	chunk, err := io.ReadAll(io.LimitReader(f, maxActivityBytes))
	if err != nil {
		return ""
	}
	s.fileOffsets[path] = mark + int64(len(chunk))
	return string(chunk)
}

// distill prompts the small model and tolerant-parses its output.
func (s *Scribe) distill(ctx context.Context, model, activity string) ([]Proposal, error) {
	prompt := "You are the Clan Scribe — the archivist of a small research collective of machines. " +
		"Below is recent Clan activity. Propose AT MOST " + strconv.Itoa(MaxProposalsPerPass) +
		" durable memories worth keeping forever. Prefer hard-won facts, decisions, findings, and skills; " +
		"skip chit-chat, status noise, and anything ephemeral.\n\n" +
		"STRICT OUTPUT FORMAT — a single JSON array, no prose before or after:\n" +
		`[{"type":"fact|finding|decision|skill","title":"<=120 chars","body":"<=500 chars","session_id":"<research session id this belongs to, or empty>","tags":["lowercase","short"]}]` +
		"\nIf nothing is durable, output [].\n\n=== ACTIVITY ===\n" + activity
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.2,
		"max_tokens":  1200,
		"stream":      false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.opts.RuntimeBase, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.opts.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("decode completion: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, errors.New("no choices in completion")
	}
	return ParseProposals(cr.Choices[0].Message.Content), nil
}

// ParseProposals is the §13.9 tolerant parser: small models leak prose and
// thinking traces around JSON. Strategy: strip <think> blocks, then try a
// json.Decoder at every '[' until one yields a JSON array; validate each
// entry, drop the invalid, cap at MaxProposalsPerPass. A garbage completion
// yields nil — never an error, never a graph write.
func ParseProposals(raw string) []Proposal {
	clean := raw
	if i := strings.Index(clean, "</think>"); i >= 0 {
		clean = clean[i+len("</think>"):]
	}
	for at := 0; ; {
		idx := strings.Index(clean[at:], "[")
		if idx < 0 {
			return nil
		}
		at += idx
		dec := json.NewDecoder(strings.NewReader(clean[at:]))
		var cand []Proposal
		if err := dec.Decode(&cand); err == nil {
			out := make([]Proposal, 0, len(cand))
			for _, p := range cand {
				if !validProposal(&p) {
					continue
				}
				out = append(out, p)
				if len(out) == MaxProposalsPerPass {
					break
				}
			}
			return out
		}
		at++ // not an array here; keep scanning
		if at >= len(clean) {
			return nil
		}
	}
}

// validProposal normalizes + gates one entry (mutates in place).
func validProposal(p *Proposal) bool {
	p.Type = strings.TrimSpace(strings.ToLower(p.Type))
	p.Title = strings.TrimSpace(p.Title)
	if !proposalTypes[p.Type] || p.Title == "" {
		return false
	}
	if len([]rune(p.Title)) > MaxTitleChars {
		p.Title = string([]rune(p.Title)[:MaxTitleChars])
	}
	if len(p.Body) > 2048 {
		p.Body = p.Body[:2048]
	}
	if len(p.Tags) > MaxTags {
		p.Tags = p.Tags[:MaxTags]
	}
	return true
}

// writeProposals lands validated proposals as proposed/scribe nodes (+ the
// §13.7 contributes_to edge when session-bound). Session summaries get the
// deterministic §13.3 id so repeated passes UPDATE one node instead of
// spawning duplicates. Returns how many landed.
func (s *Scribe) writeProposals(proposals []Proposal, now time.Time) int {
	if len(proposals) == 0 {
		return 0
	}
	// Validate session references against OPEN sessions; clear bad ones.
	open := map[string]bool{}
	for _, n := range s.opts.Service.Snapshot().Nodes {
		if n.Type == "research_session" && n.Status == "active" {
			open[n.ID] = true
		}
	}
	wrote := 0
	for _, p := range proposals {
		if p.SessionID != "" && !open[p.SessionID] {
			p.SessionID = ""
		}
		n := Node{
			Type:      p.Type,
			Title:     p.Title,
			Body:      p.Body,
			Tags:      p.Tags,
			Status:    "proposed",
			SessionID: p.SessionID,
			Provenance: Provenance{Source: "scribe"},
		}
		if p.SessionID != "" && strings.HasPrefix(strings.ToLower(p.Title), "session summary") {
			n.ID = DeterministicEventID(s.opts.ClanID, "session_summary", p.SessionID, "")
		}
		stored, err := s.opts.Service.AddOrUpdateNode(s.opts.SelfID, n, now)
		if err != nil {
			s.opts.Log.Warn("scribe: proposal write rejected", "title", truncate(p.Title, 40), "err", err)
			continue
		}
		if stored.SessionID != "" {
			if _, err := s.opts.Service.AddEdge(s.opts.SelfID, Edge{
				From: stored.ID, To: stored.SessionID, Relation: "contributes_to",
			}, now); err != nil {
				s.opts.Log.Warn("scribe: contributes_to edge rejected", "err", err)
			}
		}
		wrote++
	}
	return wrote
}

// SmallestResidentModel picks the smallest model from a resident list by the
// parameter-count hint in the tag ("llama3.2:1b" < "hermes3:8b"); tags
// without a hint sort last; empty list yields "".
func SmallestResidentModel(models []string) string {
	if v := os.Getenv("MINTI_CLAND_SCRIBE_MODEL"); v != "" {
		return v
	}
	best, bestSize := "", -1.0
	for _, m := range models {
		size := paramHint(m)
		if best == "" || (size >= 0 && (bestSize < 0 || size < bestSize)) {
			if best == "" || size >= 0 {
				best, bestSize = m, size
			}
		}
	}
	return best
}

// paramHint extracts "0.5" from "qwen2.5:0.5b" etc.; -1 when absent.
func paramHint(tag string) float64 {
	lower := strings.ToLower(tag)
	for i := 0; i < len(lower); i++ {
		if lower[i] != 'b' {
			continue
		}
		j := i - 1
		for j >= 0 && (lower[j] == '.' || (lower[j] >= '0' && lower[j] <= '9')) {
			j--
		}
		if j == i-1 {
			continue // no digits before this 'b'
		}
		if v, err := strconv.ParseFloat(lower[j+1:i], 64); err == nil {
			return v
		}
	}
	return -1
}

func shortID8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
