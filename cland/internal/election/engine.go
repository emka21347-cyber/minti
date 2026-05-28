package election

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/peers"
	"github.com/minti/cland/internal/state"
)

// Heartbeater POSTs heartbeat JSON to a peer. Decoupled from transport.Client
// so tests can swap in an in-process fake. Implementations must attach HMAC
// auth — the real one is transport.Client.Post.
type Heartbeater interface {
	Post(url, contentType string, body []byte) (*http.Response, error)
}

// RuntimeHealthCheck reports whether the local minti-runtime has answered
// /minti/capabilities successfully within RuntimeProbeMaxAge. Used to gate
// heartbeat emission per R1 (zombie-leader prevention). The caller wires this
// to a closure over probe.RuntimeClient.
type RuntimeHealthCheck func(now time.Time) bool

// LocalCandidate is what the local daemon would publish about itself for
// election purposes. The caller computes it (typically from probe + scores
// + state.Clan.PinnedOrchestrator) and hands the closure in.
type LocalCandidate struct {
	MemberID         string
	ReasoningScore   int
	ReasoningEnabled bool
	Pinned           bool
	AdmittedAt       time.Time
}

// EngineOpts is the dependency bundle for NewEngine.
type EngineOpts struct {
	SelfID       string
	ClanID       string
	State        *State
	Store        *state.Store
	Registry     *peers.Registry
	Client       Heartbeater
	Health       RuntimeHealthCheck
	LocalSelf    func() LocalCandidate
	Audit        auditlog.Logger
	Log          *slog.Logger

	// Cadence — typically cfg.Election. Defaults applied via DefaultIfZero.
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	FailoverGrace     time.Duration
	ElectionTimeout   time.Duration
}

// Engine drives heartbeat emission (when Orchestrator) and election
// triggering (when not). Single goroutine; no per-tick allocations on the
// hot path beyond the heartbeat JSON encode.
type Engine struct {
	opts EngineOpts

	// Random source for split-brain backoff (R7). Seeded per-engine so
	// concurrent test runs don't deadlock on a shared default source.
	rng    *rand.Rand
	rngMu  sync.Mutex

	// Atomic counters for test observability.
	heartbeatsSent  atomic.Uint64
	electionsRun    atomic.Uint64
}

// NewEngine validates opts and returns a ready-to-Run engine. Sane defaults
// are applied for any cadence field left zero (defense in depth — main.go
// passes cfg.Election which has its own defaults).
func NewEngine(opts EngineOpts) (*Engine, error) {
	if opts.SelfID == "" {
		return nil, fmt.Errorf("election: SelfID required")
	}
	if opts.ClanID == "" {
		return nil, fmt.Errorf("election: ClanID required")
	}
	if opts.State == nil || opts.Store == nil || opts.Registry == nil {
		return nil, fmt.Errorf("election: State + Store + Registry required")
	}
	if opts.Client == nil {
		return nil, fmt.Errorf("election: Client required")
	}
	if opts.Health == nil {
		return nil, fmt.Errorf("election: Health required (R1 zombie-leader gate)")
	}
	if opts.LocalSelf == nil {
		return nil, fmt.Errorf("election: LocalSelf required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 2 * time.Second
	}
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = 8 * time.Second
	}
	if opts.FailoverGrace <= 0 {
		opts.FailoverGrace = 6 * time.Second
	}
	if opts.ElectionTimeout <= 0 {
		opts.ElectionTimeout = 1 * time.Second
	}
	return &Engine{
		opts: opts,
		rng:  rand.New(rand.NewSource(time.Now().UnixNano() ^ int64(len(opts.SelfID)))),
	}, nil
}

// Run blocks until ctx is cancelled. Single goroutine; the tick handles both
// heartbeat emission (if Orchestrator) and election triggering (if not).
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(e.opts.HeartbeatInterval)
	defer t.Stop()
	e.opts.Log.Info("election engine started",
		"self", e.opts.SelfID,
		"heartbeat", e.opts.HeartbeatInterval,
		"lease", e.opts.LeaseDuration,
		"failover_grace", e.opts.FailoverGrace,
	)
	for {
		select {
		case <-ctx.Done():
			e.opts.Log.Info("election engine stopped")
			return
		case <-t.C:
			e.tick(ctx, time.Now())
		}
	}
}

// tick is called every HeartbeatInterval. Exported on (e *Engine) but
// lowercase to keep the API surface small; tests synthesize ticks via
// TickForTest below.
func (e *Engine) tick(ctx context.Context, now time.Time) {
	snap := e.opts.State.Snapshot()

	// R6 startup grace: don't trigger an election in the first FAILOVER_GRACE
	// seconds, regardless of state. Lets in-flight heartbeats from the existing
	// Orchestrator land first after a restart.
	withinGrace := now.Sub(snap.StartedAt) < e.opts.FailoverGrace

	// Path A: I'm Orchestrator (and lease still mine) — maybe emit heartbeats.
	if e.opts.State.IAmOrchestrator(now) {
		// Step-down: if our local candidate selection now prefers someone
		// else (e.g., a peer just got pinned via /clan/advertise), vacate.
		// We stop emitting; peers' leases expire; the preferred candidate
		// runs an election and wins. This is the spec §5.6 mechanism by
		// which pin propagation actually triggers a re-election.
		if clan, lerr := e.opts.Store.LoadClan(); lerr == nil && clan != nil {
			if cand, _ := e.selectCandidate(clan); cand.MemberID != "" && cand.MemberID != e.opts.SelfID {
				e.opts.Log.Info("election: stepping down — preferred candidate elsewhere",
					"preferred", cand.MemberID)
				return
			}
		}
		if e.opts.Health(now) {
			e.emitHeartbeats(ctx, now, snap.CurrentTerm)
		} else {
			e.opts.Log.Warn("election: skipping heartbeat — runtime unhealthy (R1)",
				"term", snap.CurrentTerm)
			_ = e.opts.Audit.Write(auditlog.Event{
				MemberID: e.opts.SelfID,
				Server:   "minti-cland",
				Tool:     "election.heartbeat",
				Decision: "deny",
				Reason:   "runtime_unhealthy",
			})
		}
		return
	}

	// Path B: not Orchestrator. Election only if lease is silent AND we're
	// out of startup grace.
	if withinGrace {
		return
	}
	if !e.opts.State.LeaseSilent(now, e.opts.FailoverGrace) {
		return
	}
	e.runElection(ctx, now, snap.CurrentTerm)
}

// TickForTest is a synchronous tick fired from tests — production drives via
// time.Ticker.
func (e *Engine) TickForTest(ctx context.Context, now time.Time) {
	e.tick(ctx, now)
}

// HeartbeatsSent / ElectionsRun expose hot-path counters for tests.
func (e *Engine) HeartbeatsSent() uint64 { return e.heartbeatsSent.Load() }
func (e *Engine) ElectionsRun() uint64   { return e.electionsRun.Load() }

// emitHeartbeats POSTs a spec §5.3 heartbeat to every member in the live
// registry. Failures don't abort emission — peers handle their own failover
// timing. The body includes active_roster + reasoning_score per R3, even
// though Phase E receivers don't yet act on those.
func (e *Engine) emitHeartbeats(ctx context.Context, now time.Time, term uint64) {
	self := e.opts.LocalSelf()
	clan, err := e.opts.Store.LoadClan()
	if err != nil || clan == nil {
		e.opts.Log.Error("election: load clan for heartbeat", "err", err)
		return
	}
	// Phase H-2: include local revocations digest so receivers can detect
	// drift and sync. Cheap (small read + sha256 over a few member_ids).
	revs, _ := e.opts.Store.LoadRevocations()
	revDigest := ""
	if revs != nil {
		revDigest = revs.Digest()
	}
	hb := Heartbeat{
		MemberID:          e.opts.SelfID,
		ClanID:            e.opts.ClanID,
		Term:              term,
		LeaseUntil:        now.Add(e.opts.LeaseDuration),
		ReasoningScore:    self.ReasoningScore,
		ActiveRoster:      activeRoster(clan),
		RevocationsDigest: revDigest,
	}
	body, err := json.Marshal(hb)
	if err != nil {
		e.opts.Log.Error("election: marshal heartbeat", "err", err)
		return
	}

	_, members := e.opts.Registry.Snapshot()
	if len(members) == 0 {
		// Lone-member Clan — no peers to send to. Still useful to advance
		// our own lease since we're the only voter.
		e.opts.State.CommitSelfElection(term, ReasonBootstrap, now, e.opts.LeaseDuration)
		return
	}
	for _, m := range members {
		if m.MemberID == e.opts.SelfID {
			continue
		}
		url := "https://" + m.Address + "/clan/heartbeat"
		resp, err := e.opts.Client.Post(url, "application/json", body)
		if err != nil {
			e.opts.Log.Debug("election: heartbeat POST failed", "peer", m.MemberID, "err", err)
			continue
		}
		_ = resp.Body.Close()
		e.heartbeatsSent.Add(1)
	}
	// Renew our own local lease (we're alive and emitting) — gives consumers
	// of IAmOrchestrator a fresh window.
	e.opts.State.CommitSelfElection(term, ReasonBootstrap /*self-renew, not a new election*/, now, e.opts.LeaseDuration)
}

// runElection implements spec §5.4 from the candidate's perspective.
// Simplified to the case where *we are the candidate*: the candidate
// announces a new term to all peers, counts accepts, and commits on quorum.
// If we are NOT the candidate, we simply wait — the actual candidate's
// heartbeat will land in our /clan/heartbeat handler.
func (e *Engine) runElection(ctx context.Context, now time.Time, currentTerm uint64) {
	e.electionsRun.Add(1)

	clan, err := e.opts.Store.LoadClan()
	if err != nil || clan == nil {
		e.opts.Log.Warn("election: cannot load clan", "err", err)
		return
	}

	candidate, reason := e.selectCandidate(clan)
	if candidate.MemberID == "" {
		// Nobody is reasoning-capable yet (e.g., bootstrap pre-first-probe).
		e.opts.Log.Debug("election: no candidate selected; skipping")
		return
	}
	if candidate.MemberID != e.opts.SelfID {
		// Wait for the chosen candidate's heartbeat. If they're broken we'll
		// retry next tick.
		e.opts.Log.Debug("election: waiting for peer candidate",
			"candidate", candidate.MemberID, "reason", reason)
		return
	}

	newTerm := currentTerm + 1
	quorum := e.quorum(clan)
	accepts := 1 // self vote

	revs, _ := e.opts.Store.LoadRevocations()
	revDigest := ""
	if revs != nil {
		revDigest = revs.Digest()
	}
	hb := Heartbeat{
		MemberID:          e.opts.SelfID,
		ClanID:            e.opts.ClanID,
		Term:              newTerm,
		LeaseUntil:        now.Add(e.opts.LeaseDuration),
		ReasoningScore:    candidate.ReasoningScore,
		ActiveRoster:      activeRoster(clan),
		RevocationsDigest: revDigest,
	}
	body, _ := json.Marshal(hb)
	_, members := e.opts.Registry.Snapshot()
	for _, m := range members {
		if m.MemberID == e.opts.SelfID {
			continue
		}
		url := "https://" + m.Address + "/clan/heartbeat"
		resp, err := e.opts.Client.Post(url, "application/json", body)
		if err != nil {
			e.opts.Log.Debug("election: announce POST failed", "peer", m.MemberID, "err", err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			accepts++
		}
		_ = resp.Body.Close()
	}

	if accepts >= quorum {
		res := e.opts.State.CommitSelfElection(newTerm, reason, now, e.opts.LeaseDuration)
		if res.TermChanged || res.OrchChanged {
			if err := e.persistTermAndOrch(newTerm, e.opts.SelfID); err != nil {
				e.opts.Log.Error("election: persist after self-elect", "err", err)
			}
		}
		e.opts.Log.Info("election won",
			"term", newTerm, "accepts", accepts, "quorum", quorum, "reason", reason)
		_ = e.opts.Audit.Write(auditlog.Event{
			MemberID: e.opts.SelfID,
			Server:   "minti-cland",
			Tool:     "election.commit",
			Decision: "allow",
			Reason:   reason,
			Args:     map[string]any{"term": newTerm, "accepts": accepts, "quorum": quorum},
		})
		return
	}

	// Quorum not reached — abort, record, sleep a R7 random backoff before
	// the next tick can retry. The natural 2s tick takes us into the next
	// attempt unless backoff says wait longer.
	backoff := e.backoff()
	e.opts.Log.Warn("election aborted (quorum not reached)",
		"term", newTerm, "accepts", accepts, "quorum", quorum, "backoff", backoff)
	e.opts.State.AppendHistory(HistoryEntry{
		Term:   newTerm,
		Winner: "",
		Reason: ReasonRetry,
		At:     now,
	})
	if backoff > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
	}
}

// selectCandidate implements spec §5.4 step 1. Pin path wins over score; ties
// (with or without pins) broken by oldest AdmittedAt (which is the spec's
// "joined_at" — same moment, different name in our struct).
func (e *Engine) selectCandidate(clan *state.Clan) (LocalCandidate, string) {
	self := e.opts.LocalSelf()
	candidates := []LocalCandidate{}
	if self.ReasoningEnabled {
		candidates = append(candidates, self)
	}

	// Pull peer candidates from live registry. Once we've ever received a
	// heartbeat from a peer (HeartbeatSeen=true), require Live(now) — that
	// way a stale-but-recently-active peer that's just died gets dropped on
	// the next election cycle. For peers we've NEVER heartbeat (bootstrap),
	// fall back to AdFresh so we can elect across the cluster on cold start.
	now := time.Now()
	_, members := e.opts.Registry.Snapshot()
	for _, m := range members {
		if m.LatestAd == nil {
			continue
		}
		if !reasoningEnabled(m.LatestAd.Capabilities) {
			continue
		}
		if m.HeartbeatSeen {
			if !m.Live(now) {
				continue
			}
		} else if !m.AdFresh(now) {
			continue
		}
		admittedAt := admittedAtFromRoster(clan, m.MemberID)
		candidates = append(candidates, LocalCandidate{
			MemberID:         m.MemberID,
			ReasoningScore:   m.LatestAd.ReasoningScore,
			ReasoningEnabled: true,
			Pinned:           m.LatestAd.PinnedOrchestrator,
			AdmittedAt:       admittedAt,
		})
	}
	if len(candidates) == 0 {
		return LocalCandidate{}, ""
	}

	// Pin path: any pinned candidate restricts the set to pinned-only;
	// multi-pin → lowest member_id wins per spec §5.6.
	pinned := []LocalCandidate{}
	for _, c := range candidates {
		if c.Pinned {
			pinned = append(pinned, c)
		}
	}
	if len(pinned) > 0 {
		sort.Slice(pinned, func(i, j int) bool { return pinned[i].MemberID < pinned[j].MemberID })
		if len(pinned) > 1 {
			e.opts.Log.Warn("election: multiple pins; lowest member_id wins (spec §5.6)",
				"winner", pinned[0].MemberID, "all", memberIDs(pinned))
		}
		return pinned[0], ReasonPinOverride
	}

	// Score path: highest reasoning_score; tie → oldest AdmittedAt (the
	// spec's "joined_at"); secondary tie → lowest member_id.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ReasoningScore != candidates[j].ReasoningScore {
			return candidates[i].ReasoningScore > candidates[j].ReasoningScore
		}
		if !candidates[i].AdmittedAt.Equal(candidates[j].AdmittedAt) {
			return candidates[i].AdmittedAt.Before(candidates[j].AdmittedAt)
		}
		return candidates[i].MemberID < candidates[j].MemberID
	})
	return candidates[0], ReasonLeaseExpire
}

// quorum is ⌈N/2⌉ where N is the persisted roster size (per spec §5.5,
// computed from state.Clan NOT the live registry — that's the partition-
// tolerance guarantee). Counts both "active" AND "admitted" members.
//
// Phase J finding: the admitted→active promotion (spec §3.1) isn't wired
// through the advertise loop in v1, so members stay "admitted" forever.
// Filtering to "active" only (the original R5 fix from Phase E peer-review)
// makes quorum=1 per node, which breaks 3-node consensus entirely. Counting
// "admitted" too is the pragmatic interim until Phase H-3 wires the
// promotion + gossips it. The downside (admitted members can vote in
// elections before fully validated) is documented in the M4 STATUS as a
// known limitation.
func (e *Engine) quorum(clan *state.Clan) int {
	n := 0
	for _, m := range clan.Roster {
		if m.State == "active" || m.State == "admitted" {
			n++
		}
	}
	// Lone-member edge case: the founder has only one entry in the roster
	// (themselves). If the founder hasn't been promoted to "active" yet —
	// which happens on first successful advertisement — there are zero
	// counted members and quorum would be 0. Floor at 1 so we still need
	// at least our own vote.
	if n == 0 {
		n = 1
	}
	return (n + 1) / 2
}

// AcceptableSender returns true iff `senderID` is the highest-scoring
// candidate in our local view. Used by the /clan/heartbeat handler for the
// anti-spoof check (spec §5.3).
func (e *Engine) AcceptableSender(senderID string) bool {
	clan, err := e.opts.Store.LoadClan()
	if err != nil || clan == nil {
		return false
	}
	candidate, _ := e.selectCandidate(clan)
	return candidate.MemberID == senderID
}

// PersistTermAndOrch wraps Store.SaveClan, writing only when the on-disk
// values differ from the new ones (R2). Returns nil on no-op or success;
// the engine logs and continues on save errors (transient disk faults
// shouldn't crash the daemon).
func (e *Engine) persistTermAndOrch(term uint64, orch string) error {
	clan, err := e.opts.Store.LoadClan()
	if err != nil {
		return err
	}
	if clan == nil {
		return fmt.Errorf("election: persist into nil clan")
	}
	if clan.CurrentTerm == term && clan.CurrentOrchestrator == orch {
		return nil
	}
	clan.CurrentTerm = term
	clan.CurrentOrchestrator = orch
	return e.opts.Store.SaveClan(clan)
}

// OnHeartbeatReceived is called by the /clan/heartbeat handler after the
// transport's HMAC middleware accepted the request. The body is the parsed
// Heartbeat; senderID is what the middleware authenticated.
//
// Anti-spoof: senderID must match the candidate WE would pick.
// On accept, persists term/orchestrator if they changed (R2), and bumps the
// peer registry's Live() timestamp.
func (e *Engine) OnHeartbeatReceived(hb Heartbeat, senderID string, now time.Time) (ApplyResult, error) {
	if hb.MemberID != senderID {
		// HMAC-authenticated member must match the heartbeat's claimed sender.
		// Otherwise a member could spoof a heartbeat as someone else.
		return ApplyResult{}, fmt.Errorf("election: hb.member_id %q != authenticated sender %q",
			hb.MemberID, senderID)
	}
	if hb.ClanID != e.opts.ClanID {
		return ApplyResult{}, fmt.Errorf("election: foreign clan_id %q (ours: %q)", hb.ClanID, e.opts.ClanID)
	}

	acceptable := e.AcceptableSender(senderID)
	res, err := e.opts.State.ApplyHeartbeat(hb, now, e.opts.LeaseDuration, acceptable)
	if err != nil {
		return res, err
	}
	e.opts.Registry.TouchLive(senderID)
	if res.TermChanged || res.OrchChanged {
		if perr := e.persistTermAndOrch(res.NewTerm, res.NewOrch); perr != nil {
			e.opts.Log.Error("election: persist after accept", "err", perr)
		}
	}
	return res, nil
}

// backoff is the R7 split-brain backoff: 50-150 ms uniform random.
func (e *Engine) backoff() time.Duration {
	e.rngMu.Lock()
	defer e.rngMu.Unlock()
	return time.Duration(50+e.rng.Intn(101)) * time.Millisecond
}

// ---------- helpers (file-private) ----------

func activeRoster(c *state.Clan) []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Roster))
	for _, m := range c.Roster {
		if m.State == "active" {
			out = append(out, m.MemberID)
		}
	}
	return out
}

func admittedAtFromRoster(c *state.Clan, memberID string) time.Time {
	if c == nil {
		return time.Time{}
	}
	for _, m := range c.Roster {
		if m.MemberID == memberID {
			return m.AdmittedAt
		}
	}
	return time.Time{}
}

func reasoningEnabled(caps map[string]any) bool {
	if caps == nil {
		return false
	}
	r, ok := caps["reasoning"].(map[string]any)
	if !ok {
		return false
	}
	en, _ := r["enabled"].(bool)
	return en
}

func memberIDs(cs []LocalCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.MemberID
	}
	return out
}

// readBody is a small util that handlers use — kept here to avoid an extra
// dependency on bytes/io in the handlers file. (Returned for testing).
func readBody(r *http.Response) []byte {
	if r == nil || r.Body == nil {
		return nil
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r.Body)
	_ = r.Body.Close()
	return buf.Bytes()
}
