package keyrotate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/minti/cland/internal/auditlog"
)

// Poster is the subset of transport.Client the coordinator needs. Decoupled
// for tests.
type Poster interface {
	Post(url, contentType string, body []byte) (*http.Response, error)
}

// Peer is one ACK target during rotation.
type Peer struct {
	MemberID string
	Address  string
}

// CoordinatorOpts is the dep bundle.
type CoordinatorOpts struct {
	SelfID  string
	Rotater Rotater
	Client  Poster
	// PeerSource returns the list of active peers we'll PROPOSE+COMMIT to.
	// Excludes self. Called fresh on each Rotate() so a member that joined
	// mid-rotation gets picked up — but cleanly: PROPOSE first, ABORT if
	// any peer rejects, so the snapshot is consistent within one call.
	PeerSource func() []Peer
	Audit      auditlog.Logger
	Log        *slog.Logger
	// Defaults: ProposeTimeout, DefaultGraceDuration.
	ProposeTimeout time.Duration
	GraceDuration  time.Duration
}

// Coordinator runs the orchestrator-side 2PC.
type Coordinator struct {
	opts CoordinatorOpts
}

func NewCoordinator(opts CoordinatorOpts) (*Coordinator, error) {
	if opts.SelfID == "" {
		return nil, fmt.Errorf("keyrotate: SelfID required")
	}
	if opts.Rotater == nil {
		return nil, fmt.Errorf("keyrotate: Rotater required")
	}
	if opts.Client == nil {
		return nil, fmt.Errorf("keyrotate: Client required")
	}
	if opts.PeerSource == nil {
		return nil, fmt.Errorf("keyrotate: PeerSource required")
	}
	if opts.Audit == nil {
		return nil, fmt.Errorf("keyrotate: Audit required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.ProposeTimeout <= 0 {
		opts.ProposeTimeout = ProposeTimeout
	}
	if opts.GraceDuration <= 0 {
		opts.GraceDuration = DefaultGraceDuration
	}
	return &Coordinator{opts: opts}, nil
}

// Rotate runs the full 2PC. Blocks until commit, abort, or all peer attempts
// have settled (success/timeout). Self-rotates after all COMMITs are sent —
// during the brief window where peers are mid-rotation, in-flight HMACs may
// 401 (mitigated by KeyProvider grace + the natural 2 s retry cadence of
// heartbeats/advertisements).
func (c *Coordinator) Rotate(ctx context.Context) (RotateResult, error) {
	res := RotateResult{}
	newKey := make([]byte, KeyLen)
	if _, err := rand.Read(newKey); err != nil {
		return res, fmt.Errorf("gen new key: %w", err)
	}
	propID, err := newProposeID()
	if err != nil {
		return res, fmt.Errorf("gen propose_id: %w", err)
	}
	res.ProposeID = propID

	peers := c.opts.PeerSource()
	// Lone-orchestrator case: no peers → just self-rotate, no consensus
	// needed. Edge case but real for single-member Clans.
	if len(peers) == 0 {
		if err := c.opts.Rotater.Rotate(newKey, c.opts.GraceDuration); err != nil {
			return res, fmt.Errorf("self rotate (lone): %w", err)
		}
		res.Committed = true
		c.audit("allow", "rotate_lone", propID, nil)
		return res, nil
	}

	// Phase 1 — PROPOSE.
	propBody, _ := json.Marshal(ProposeRequest{
		ProposeID: propID,
		NewKeyB64: base64.StdEncoding.EncodeToString(newKey),
		ProposeTS: time.Now().UTC(),
	})
	pCtx, pCancel := context.WithTimeout(ctx, c.opts.ProposeTimeout)
	defer pCancel()
	propResults := c.broadcast(pCtx, peers, "/clan/rotate-key/propose", propBody)

	failed := []string{}
	acked := []string{}
	for _, r := range propResults {
		if r.ok {
			acked = append(acked, r.memberID)
		} else {
			failed = append(failed, r.memberID)
		}
	}
	res.AckedBy = acked
	res.FailedBy = failed

	if len(failed) > 0 {
		// Abort path: notify everyone who ACKed so they revert immediately
		// (don't make them wait MemberRevertAfter).
		abortBody, _ := json.Marshal(AbortRequest{
			ProposeID: propID,
			Reason:    fmt.Sprintf("%d peers failed propose", len(failed)),
		})
		ackedPeers := peersByID(peers, acked)
		c.broadcast(ctx, ackedPeers, "/clan/rotate-key/abort", abortBody)
		res.AbortedAt = time.Now().UTC().Format(time.RFC3339)
		res.AbortReason = fmt.Sprintf("propose failed on: %v", failed)
		c.audit("deny", "rotate_aborted", propID, fmt.Errorf("failed: %v", failed))
		return res, nil
	}

	// Phase 2 — COMMIT.
	commitBody, _ := json.Marshal(CommitRequest{
		ProposeID:     propID,
		CommitTS:      time.Now().UTC(),
		GraceDuration: c.opts.GraceDuration,
	})
	c.broadcast(ctx, peers, "/clan/rotate-key/commit", commitBody)
	// Even if some commits fail, all members that ACKed propose either get
	// the commit or revert via their own MemberRevertAfter timer.

	// Self-rotate.
	if err := c.opts.Rotater.Rotate(newKey, c.opts.GraceDuration); err != nil {
		return res, fmt.Errorf("self rotate: %w", err)
	}
	res.Committed = true
	c.audit("allow", "rotate_committed", propID, nil)
	return res, nil
}

// ---------- internal ----------

type peerResult struct {
	memberID string
	ok       bool
	status   int
	err      error
}

func (c *Coordinator) broadcast(ctx context.Context, peers []Peer, path string, body []byte) []peerResult {
	results := make([]peerResult, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Add(1)
		go func(i int, p Peer) {
			defer wg.Done()
			results[i] = c.sendOne(ctx, p, path, body)
		}(i, p)
	}
	wg.Wait()
	return results
}

func (c *Coordinator) sendOne(ctx context.Context, p Peer, path string, body []byte) peerResult {
	res := peerResult{memberID: p.MemberID}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://"+p.Address+path, bytes.NewReader(body))
	if err != nil {
		res.err = err
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	// Coordinator's Poster is a transport.Client (HMAC-stamping) but the
	// minimal Poster interface here just exposes Post(url, contentType, body).
	// We use the Post shape because that's what transport.Client provides;
	// callers in tests fake the same surface.
	resp, err := c.opts.Client.Post("https://"+p.Address+path, "application/json", body)
	if err != nil {
		res.err = err
		return res
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	res.status = resp.StatusCode
	res.ok = resp.StatusCode == http.StatusOK
	return res
}

func (c *Coordinator) audit(decision, reason, proposeID string, err error) {
	args := map[string]any{"propose_id": proposeID}
	_ = c.opts.Audit.Write(auditlog.Event{
		MemberID: c.opts.SelfID,
		Server:   "minti-cland",
		Tool:     "keyrotate.coordinator",
		Decision: decision,
		Reason:   reason,
		Args:     args,
		Error:    errString(err),
	})
}

func newProposeID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// UUIDv4-ish (just hex; we don't need RFC 4122 strict format here).
	return hex.EncodeToString(b), nil
}

func peersByID(all []Peer, ids []string) []Peer {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	out := make([]Peer, 0, len(ids))
	for _, p := range all {
		if set[p.MemberID] {
			out = append(out, p)
		}
	}
	return out
}
