// Package keyrotate implements spec §8 — orchestrator-initiated key rotation
// with a 2PC + timeout-revert. Phase H-1 of M4.
//
// Wire surface (HMAC-authenticated by transport, signed under the CURRENT key
// at the time of each request — so propose+commit are both signed under the
// old key; only post-rotation requests use the new key, with the old key
// accepted via KeyProvider.Grace() during the grace window):
//
//   POST /clan/rotate-key/propose   — Orchestrator → member
//   POST /clan/rotate-key/commit    — Orchestrator → member
//   POST /clan/rotate-key/abort     — Orchestrator → member
//
// And a local-only trigger the CLI uses:
//
//   POST /clan/rotate-key           — local agent / CLI → self.Orchestrator
//
// State machine on member side:
//
//   IDLE → PROPOSED (on propose accept)
//        → ROTATED (on commit; KeyProvider.Rotate applied with 5 min grace)
//        → IDLE    (on abort, or on propose-timeout w/o commit)
//
// Coordinator (Orchestrator-side):
//
//   1. Pick new key (32 random bytes).
//   2. PROPOSE to all active peers in parallel; collect ACKs.
//   3. If every peer ACKed within PROPOSE_TIMEOUT: COMMIT broadcast +
//      self KeyProvider.Rotate. Otherwise ABORT broadcast to everyone
//      who ACKed; no self-rotation.
//
// Members that PROPOSE'd but never receive a COMMIT/ABORT within
// ProposeTimeout × 1.5 auto-revert to IDLE (defends against an
// Orchestrator that crashes mid-rotation).
package keyrotate

import (
	"encoding/base64"
	"errors"
	"time"
)

const (
	// ProposeTimeout caps how long the Orchestrator waits for all ACKs.
	// 60 s gives a 2-3 hop LAN ample time even with high latency.
	ProposeTimeout = 60 * time.Second

	// MemberRevertAfter is how long a member holds a PROPOSED state without
	// a COMMIT/ABORT before reverting to IDLE. 1.5× ProposeTimeout = 90 s.
	MemberRevertAfter = 90 * time.Second

	// DefaultGraceDuration is the post-commit grace window during which the
	// old key is still accepted (via KeyProvider.Grace). Matches spec §8.1.
	DefaultGraceDuration = 5 * time.Minute

	// KeyLen is the clan_key length in bytes.
	KeyLen = 32
)

// Errors callers check.
var (
	ErrUnknownPropose   = errors.New("keyrotate: no pending propose for that id")
	ErrAlreadyPending   = errors.New("keyrotate: a different propose is already pending")
	ErrBadKey           = errors.New("keyrotate: new key wrong length")
	ErrNotOrchestrator  = errors.New("keyrotate: only the current Orchestrator may initiate")
)

// ProposeRequest is the wire body for POST /clan/rotate-key/propose.
type ProposeRequest struct {
	ProposeID string    `json:"propose_id"` // UUID
	NewKeyB64 string    `json:"new_key_b64"`
	ProposeTS time.Time `json:"propose_ts"`
}

// NewKey decodes + length-checks the new key.
func (r *ProposeRequest) NewKey() ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(r.NewKeyB64)
	if err != nil {
		return nil, err
	}
	if len(b) != KeyLen {
		return nil, ErrBadKey
	}
	return b, nil
}

// CommitRequest is the wire body for POST /clan/rotate-key/commit.
type CommitRequest struct {
	ProposeID     string        `json:"propose_id"`
	CommitTS      time.Time     `json:"commit_ts"`
	GraceDuration time.Duration `json:"grace_duration"`
}

// AbortRequest is the wire body for POST /clan/rotate-key/abort.
type AbortRequest struct {
	ProposeID string `json:"propose_id"`
	Reason    string `json:"reason,omitempty"`
}

// AckResponse is the standard reply from member to Orchestrator for any of
// the three lifecycle endpoints.
type AckResponse struct {
	ProposeID string `json:"propose_id"`
	Accepted  bool   `json:"accepted"`
	Reason    string `json:"reason,omitempty"`
}

// RotateResult is what the coordinator returns to its caller (CLI).
type RotateResult struct {
	ProposeID  string   `json:"propose_id"`
	Committed  bool     `json:"committed"`
	AckedBy    []string `json:"acked_by"`     // member_ids that ACKed propose
	FailedBy   []string `json:"failed_by"`    // member_ids that failed (timeout or error)
	AbortedAt  string   `json:"aborted_at,omitempty"`
	AbortReason string  `json:"abort_reason,omitempty"`
}
