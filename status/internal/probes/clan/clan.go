// Package clan probes minti-cland by shelling out to the cland CLI
// subcommands that already expose --json output. Avoids re-implementing
// HMAC client + clan_key access here; cland already does it correctly
// when run as root or the `minti` user.
//
// Graceful degradation: if minti-cland is not on PATH OR if clan_key
// can't be read (EACCES, exit code 1 with permission denied), Probe()
// returns a clan.State with Configured=false and an error that
// IsPermissionDenied recognises. The TUI then shows
// "(sudo for clan details)" — same UX as minti-fetch.
package clan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// State is the full Clan snapshot. Empty when no Clan is configured;
// partially filled when only orchestrator info has been refreshed.
type State struct {
	Configured       bool
	ClanID           string
	Role             string // "founder" | "member" | "admitted" | ...
	Pin              string // sha256:hex...
	SelfMemberID     string

	Orchestrator     string // member display name or member_id prefix
	IsSelfOrch       bool
	Term             int
	LeaseLeft        time.Duration

	Members          []Member
	RecentElections  []ElectionEntry
}

type Member struct {
	MemberID       string
	DisplayName    string // address or short member_id
	State          string // "active" | "admitted" | "revoked"
	ReasoningScore int
	SystemScore    int
	LastAd         time.Duration
	IsOrchestrator bool
}

type ElectionEntry struct {
	Term   int
	Winner string
	Reason string
	At     time.Time
}

// ErrPermissionDenied indicates the CLI ran but couldn't read clan_key.
// Wraps the original error so callers can still log details.
type ErrPermissionDenied struct{ Wrapped error }

func (e *ErrPermissionDenied) Error() string {
	return "permission denied (sudo for clan details)"
}
func (e *ErrPermissionDenied) Unwrap() error { return e.Wrapped }

// IsPermissionDenied returns true if err originates from EACCES on
// clan.json or similar. Used by the TUI to switch to degraded rendering.
func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	var pd *ErrPermissionDenied
	return errors.As(err, &pd)
}

// Probe runs the full set of CLI subcommands (show + orchestrator +
// peers + election-history). Use ProbeOrchestratorOnly on the fast
// tick to refresh just the term/lease countdown.
func Probe(ctx context.Context) (State, error) {
	st, err := ProbeOrchestratorOnly(ctx)
	if err != nil {
		return st, err
	}
	if !st.Configured {
		return st, nil
	}

	if members, err := readPeers(ctx); err == nil {
		st.Members = members
		// Tag the orchestrator inside the member list.
		for i := range st.Members {
			if st.Members[i].MemberID == st.SelfMemberID && st.IsSelfOrch {
				st.Members[i].IsOrchestrator = true
			} else if !st.IsSelfOrch && st.Members[i].DisplayName == st.Orchestrator {
				st.Members[i].IsOrchestrator = true
			}
		}
	}

	if hist, err := readHistory(ctx); err == nil {
		st.RecentElections = hist
	}

	return st, nil
}

// ProbeOrchestratorOnly: cheap fast-tick refresh. Runs `minti-cland show`
// + `minti-cland orchestrator --json`. ~30-50 ms each.
func ProbeOrchestratorOnly(ctx context.Context) (State, error) {
	var st State

	showOut, err := runCland(ctx, "show")
	if err != nil {
		if isNotConfigured(showOut, err) {
			return st, nil
		}
		if isPermDenied(showOut, err) {
			return st, &ErrPermissionDenied{Wrapped: err}
		}
		return st, err
	}
	st.Configured = true
	parseShow(showOut, &st)

	orchOut, err := runCland(ctx, "orchestrator", "--json")
	if err != nil {
		return st, nil // orchestrator unknown isn't fatal — Clan exists, no leader yet
	}
	var orch struct {
		CurrentOrchestrator string `json:"current_orchestrator"`
		CurrentTerm         int    `json:"current_term"`
		LeaseExpires        string `json:"lease_expires"`
		Self                string `json:"self"`
		IsSelf              bool   `json:"is_self"`
	}
	if json.Unmarshal(orchOut, &orch) == nil {
		st.Orchestrator = shorten(orch.CurrentOrchestrator)
		st.IsSelfOrch = orch.IsSelf
		st.Term = orch.CurrentTerm
		st.SelfMemberID = orch.Self
		if t, err := time.Parse(time.RFC3339, orch.LeaseExpires); err == nil {
			if d := time.Until(t); d > 0 {
				st.LeaseLeft = d.Round(time.Second)
			}
		}
	}
	return st, nil
}

// runCland exec's minti-cland with the given args, returns stdout (raw).
func runCland(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, errors.New("minti-cland not on PATH")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Append stderr to the error message for diagnosis.
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return stdout.Bytes(), errors.New(msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func isNotConfigured(_ []byte, err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no clan") ||
		strings.Contains(s, "not affiliated") ||
		strings.Contains(s, "unaffiliated") ||
		strings.Contains(s, "no such file")
}

func isPermDenied(_ []byte, err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "permission denied") ||
		strings.Contains(s, "eacces") ||
		strings.Contains(s, "operation not permitted")
}

// parseShow extracts clan_id + role + pin from `minti-cland show`'s
// human-readable output. (No --json on this subcommand at time of
// writing; cheap regex-grade parse works.)
func parseShow(out []byte, st *State) {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "clan_id:") || strings.HasPrefix(line, "Clan ID"):
			st.ClanID = afterColon(line)
		case strings.HasPrefix(line, "role:"):
			st.Role = afterColon(line)
		case strings.HasPrefix(line, "pin:") || strings.HasPrefix(line, "Pin:"):
			st.Pin = afterColon(line)
		case strings.HasPrefix(line, "member_id:") || strings.HasPrefix(line, "Member:"):
			if st.SelfMemberID == "" {
				st.SelfMemberID = afterColon(line)
			}
		}
	}
}

func afterColon(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+1:])
}

// readPeers calls `minti-cland peers --json` and translates the response
// into our flat Member slice. Cland's PeersListResponse has both
// candidates + members; we surface members + admitted-state candidates.
func readPeers(ctx context.Context) ([]Member, error) {
	out, err := runCland(ctx, "peers", "--json")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Members []struct {
			MemberID       string  `json:"member_id"`
			Address        string  `json:"address"`
			State          string  `json:"state"`
			ReasoningScore int     `json:"reasoning_score"`
			SystemScore    int     `json:"system_score"`
			LastAdAgo      float64 `json:"last_ad_ago_sec"`
		} `json:"members"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	now := time.Now()
	_ = now
	members := make([]Member, 0, len(resp.Members))
	for _, m := range resp.Members {
		members = append(members, Member{
			MemberID:       m.MemberID,
			DisplayName:    m.Address,
			State:          m.State,
			ReasoningScore: m.ReasoningScore,
			SystemScore:    m.SystemScore,
			LastAd:         time.Duration(m.LastAdAgo * float64(time.Second)),
		})
	}
	return members, nil
}

// readHistory calls `minti-cland election-history --json` and keeps the
// most recent 3 entries.
func readHistory(ctx context.Context) ([]ElectionEntry, error) {
	out, err := runCland(ctx, "election-history", "--json")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Entries []struct {
			Term   int    `json:"term"`
			Winner string `json:"winner"`
			Reason string `json:"reason"`
			At     string `json:"at"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	if len(resp.Entries) > 3 {
		resp.Entries = resp.Entries[len(resp.Entries)-3:]
	}
	out2 := make([]ElectionEntry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		t, _ := time.Parse(time.RFC3339, e.At)
		out2 = append(out2, ElectionEntry{
			Term:   e.Term,
			Winner: shorten(e.Winner),
			Reason: e.Reason,
			At:     t,
		})
	}
	return out2, nil
}

// shorten a UUID/long member_id to its first 8 chars + "…".
func shorten(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:8] + "…"
}
