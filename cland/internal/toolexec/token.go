// Package toolexec implements cross-Clan tool execution per spec §7.1.
//
// Flow: an origin Clan member's agent decides to invoke a tool. The origin
// renders the permission prompt to its user, then mints a signed Execution
// Token containing claims (request_id, origin, target, tool, args_hash,
// approved_at, exp) under HMAC-SHA256(clan_key, canonical). The origin POSTs
// the token + args to the target member's `/mcp/execute`. The target verifies:
//
//   1. body.args bytes hash matches token.args_hash (catches arg tampering)
//   2. HMAC sig matches under current OR grace clan_key (rotation tolerant)
//   3. target_member == self.member_id (anti-misroute / spoofing)
//   4. exp not past
//   5. approved_at not in the future (small clock-skew tolerance allowed)
//   6. request_id not previously seen (replay protection)
//
// On any failure: 401 + audit log, no execution. On success: policy check
// against the local mcp-servers policy (deny_tools enforced), then spawn the
// named MCP server stdio subprocess via the mcp-go SDK and stream the result
// back to the origin.
//
// v1 honesty note (spec §7.1): the shared clan_key model means the HMAC
// proves the token was issued by *some* current member — not specifically
// by the claimed origin. A malicious insider could mint a token claiming
// someone else as origin. The "permission prompt rendered on origin"
// guarantee is a UI control, not a cryptographic one. v2 per-member
// keypairs + signed-by-origin tokens close this gap.
package toolexec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Errors callers check with errors.Is.
var (
	ErrSigMismatch       = errors.New("toolexec: token HMAC mismatch")
	ErrArgsHashMismatch  = errors.New("toolexec: args bytes do not match token.args_hash")
	ErrWrongTarget       = errors.New("toolexec: target_member is not self")
	ErrExpired           = errors.New("toolexec: token exp is past")
	ErrApprovedInFuture  = errors.New("toolexec: token approved_at is in the future")
	ErrReplay            = errors.New("toolexec: request_id already seen")
	ErrMalformedToken    = errors.New("toolexec: malformed token")
)

// ClockSkewTolerance bounds how far approved_at may be in the future before
// we reject as malformed. Matches transport.TimestampSkewMax (±60s).
const ClockSkewTolerance = 60 * time.Second

// DefaultMaxTokenLifetime caps how long a token may be valid for — even if
// the origin sets a longer exp, we treat anything > this as malformed.
// Keeps replay window bounded.
const DefaultMaxTokenLifetime = 10 * time.Minute

// Token is the wire shape per spec §7.1. ArgsHash is sha256(raw args bytes).
// Sig is the lowercase-hex HMAC-SHA256 over canonical().
type Token struct {
	RequestID    string `json:"request_id"`
	OriginMember string `json:"origin_member"`
	TargetMember string `json:"target_member"`
	Tool         string `json:"tool"`
	ArgsHash     string `json:"args_hash"`
	ApprovedAt   int64  `json:"approved_at"` // unix millis
	Exp          int64  `json:"exp"`         // unix millis
	Sig          string `json:"sig"`         // hex hmac
}

// canonical returns the LF-joined signed-fields string. Sig is NOT included
// (signature can't sign itself). Format is stable + lexicographically
// orderless — fields appear in a fixed order both sides know.
func (t *Token) canonical() string {
	return strings.Join([]string{
		t.RequestID,
		t.OriginMember,
		t.TargetMember,
		t.Tool,
		t.ArgsHash,
		strconv.FormatInt(t.ApprovedAt, 10),
		strconv.FormatInt(t.Exp, 10),
	}, "\n")
}

// Sign computes HMAC-SHA256 over canonical() and sets t.Sig. Used by
// origin members (in production) and by tests minting fresh tokens.
func (t *Token) Sign(key []byte) {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(t.canonical()))
	t.Sig = hex.EncodeToString(m.Sum(nil))
}

// VerifyHMAC returns nil iff t.Sig matches the expected HMAC under any of
// the provided keys (current + optional grace). Constant-time compare.
// Does NOT check temporal claims or target — caller does those separately
// via VerifyClaims so each check has a distinct error for the audit log.
func (t *Token) VerifyHMAC(keys ...[]byte) error {
	if t.Sig == "" {
		return ErrSigMismatch
	}
	provided, err := hex.DecodeString(strings.ToLower(t.Sig))
	if err != nil {
		return fmt.Errorf("%w: sig not hex", ErrMalformedToken)
	}
	can := []byte(t.canonical())
	for _, k := range keys {
		if len(k) == 0 {
			continue
		}
		m := hmac.New(sha256.New, k)
		m.Write(can)
		if hmac.Equal(m.Sum(nil), provided) {
			return nil
		}
	}
	return ErrSigMismatch
}

// VerifyClaims checks the non-cryptographic claims:
//   - target_member == selfMemberID
//   - exp not past
//   - approved_at not more than ClockSkewTolerance in the future
//   - exp - approved_at <= maxLifetime
//
// Replay-cache check is the handler's responsibility (it owns the cache).
func (t *Token) VerifyClaims(selfMemberID string, now time.Time, maxLifetime time.Duration) error {
	if t.TargetMember != selfMemberID {
		return fmt.Errorf("%w (token target=%q, self=%q)", ErrWrongTarget, t.TargetMember, selfMemberID)
	}
	exp := time.UnixMilli(t.Exp)
	if !exp.After(now) {
		return fmt.Errorf("%w (exp=%s now=%s)", ErrExpired, exp.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	approvedAt := time.UnixMilli(t.ApprovedAt)
	if approvedAt.Sub(now) > ClockSkewTolerance {
		return fmt.Errorf("%w (approved_at=%s, now=%s)",
			ErrApprovedInFuture, approvedAt.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if maxLifetime > 0 && exp.Sub(approvedAt) > maxLifetime {
		return fmt.Errorf("%w (lifetime=%s, max=%s)",
			ErrMalformedToken, exp.Sub(approvedAt), maxLifetime)
	}
	return nil
}

// HashArgs returns the sha256-hex of the raw args body the way the origin
// must compute it before signing. Operates on bytes — order-of-keys in any
// embedded JSON is preserved (we never re-marshal), so origin + target see
// byte-identical input.
func HashArgs(rawArgs []byte) string {
	h := sha256.Sum256(rawArgs)
	return hex.EncodeToString(h[:])
}
