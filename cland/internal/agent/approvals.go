package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultApprovalTimeout bounds how long a change-tool call waits for a decision
// before failing closed (deny). Matches the M1 design's 120s.
const DefaultApprovalTimeout = 120 * time.Second

var (
	// ErrApprovalTimeout is returned by ChannelApprover.Await when no decision
	// arrives in time. The loop treats any Await error as a deny (fail-closed).
	ErrApprovalTimeout = errors.New("agent: approval timed out")
	// ErrNoPendingApproval is returned by Resolve when the call_id isn't waiting
	// (already resolved, timed out, or never existed).
	ErrNoPendingApproval = errors.New("agent: no pending approval for that call")
)

// ChannelApprover is an Approver whose decisions arrive out-of-band via Resolve
// — used by the cland daemon, where the loop runs in one goroutine (blocked in
// Await) and the browser's decision lands on a separate HTTP request
// (POST /agent/approve) that calls Resolve. One instance serves one agent
// request; calls within it are keyed by their call_id (unique per request).
type ChannelApprover struct {
	mu      sync.Mutex
	pending map[string]chan Decision
	timeout time.Duration
}

// NewChannelApprover returns an approver with the given Await timeout
// (<=0 → DefaultApprovalTimeout).
func NewChannelApprover(timeout time.Duration) *ChannelApprover {
	if timeout <= 0 {
		timeout = DefaultApprovalTimeout
	}
	return &ChannelApprover{pending: make(map[string]chan Decision), timeout: timeout}
}

// Await registers the call as pending and blocks until Resolve delivers a
// decision, the timeout elapses, or ctx is cancelled. Timeout/cancel → deny.
func (a *ChannelApprover) Await(ctx context.Context, req ApprovalRequest) (Decision, error) {
	ch := make(chan Decision, 1)
	a.mu.Lock()
	a.pending[req.CallID] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, req.CallID)
		a.mu.Unlock()
	}()

	timer := time.NewTimer(a.timeout)
	defer timer.Stop()
	select {
	case d := <-ch:
		return d, nil
	case <-timer.C:
		return DecisionDeny, ErrApprovalTimeout
	case <-ctx.Done():
		return DecisionDeny, ctx.Err()
	}
}

// Resolve delivers a decision for a pending call_id. Safe to call from another
// goroutine. Returns ErrNoPendingApproval if nothing is waiting on that id.
func (a *ChannelApprover) Resolve(callID string, d Decision) error {
	a.mu.Lock()
	ch, ok := a.pending[callID]
	a.mu.Unlock()
	if !ok {
		return ErrNoPendingApproval
	}
	select {
	case ch <- d: // buffered (cap 1) → never blocks; first writer wins
	default:
	}
	return nil
}

// Pending lists call_ids currently awaiting a decision (observability/tests).
func (a *ChannelApprover) Pending() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.pending))
	for id := range a.pending {
		out = append(out, id)
	}
	return out
}
