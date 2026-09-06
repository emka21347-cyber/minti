// Package clan probes the local minti-cland daemon for a JSON-friendly
// snapshot of the Clan. It shells cland's CLI subcommands (which already run
// the HMAC client + clan_key access correctly when invoked as root or the
// minti user), mirroring the approach minti-status uses — deliberately
// decoupled from that module for now; a shared probes package can be extracted
// once both binaries' API shapes settle.
//
// When minti-cland is not installed (e.g. a dev box), Probe returns a demo
// snapshot so the UI still has something to render.
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

// Member is one node in the Clan as the workspace UI consumes it.
type Member struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Address        string `json:"address"`
	OS             string `json:"os"`
	State          string `json:"state"` // "active" | "candidate" | "self"
	IsSelf         bool   `json:"is_self"`
	IsOrchestrator bool   `json:"is_orchestrator"`
	Busy           bool   `json:"busy"`
	ReasoningScore int    `json:"reasoning_score"`
	SystemScore    int    `json:"system_score"`
}

// Snapshot is the JSON payload served at /api/mesh.
type Snapshot struct {
	Source       string   `json:"source"` // "live" | "demo" | "unconfigured"
	Configured   bool     `json:"configured"`
	ClanID       string   `json:"clan_id"`
	Role         string   `json:"role"`
	SelfMemberID string   `json:"self_member_id"`
	SelfAddress  string   `json:"self_address"`
	Orchestrator string   `json:"orchestrator"`
	IsSelfOrch   bool     `json:"is_self_orchestrator"`
	Term         int      `json:"term"`
	LeaseLeftSec float64  `json:"lease_left_sec"`
	Members      []Member `json:"members"`
}

// Probe returns the current Clan snapshot. It never returns an error: failure
// modes (cland missing, unaffiliated, permission denied) degrade to a snapshot
// with an explanatory Source so the UI can render gracefully.
func Probe(ctx context.Context) Snapshot {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return demo()
	}

	showOut, err := runCland(ctx, bin, "show")
	if err != nil {
		if isUnaffiliated(err) {
			return Snapshot{Source: "unconfigured"}
		}
		// permission denied or other: report unconfigured rather than crash.
		return Snapshot{Source: "unconfigured"}
	}

	st := Snapshot{Source: "live", Configured: true}
	parseShow(showOut, &st)

	if orchOut, err := runCland(ctx, bin, "orchestrator", "--json"); err == nil {
		var o struct {
			CurrentOrchestrator string `json:"current_orchestrator"`
			CurrentTerm         int    `json:"current_term"`
			LeaseExpires        string `json:"lease_expires"`
			Self                string `json:"self"`
			IsSelf              bool   `json:"is_self"`
		}
		if json.Unmarshal(orchOut, &o) == nil {
			st.Orchestrator = o.CurrentOrchestrator
			st.IsSelfOrch = o.IsSelf
			st.Term = o.CurrentTerm
			if o.Self != "" {
				st.SelfMemberID = o.Self
			}
			if t, err := time.Parse(time.RFC3339, o.LeaseExpires); err == nil {
				if d := time.Until(t); d > 0 {
					st.LeaseLeftSec = d.Round(time.Second).Seconds()
				}
			}
		}
	}

	// Self is always a member so a 1-node Clan still draws.
	members := []Member{{
		ID: st.SelfMemberID, Name: shortName(st.SelfMemberID), Address: st.SelfAddress,
		State: "self", IsSelf: true, IsOrchestrator: st.IsSelfOrch,
	}}

	// Real peers + candidates from `minti-cland peers --json`. Best-effort: a
	// probe failure leaves the self-only view rather than erroring the page.
	// OS + scores live under latest_ad, which is nil until a peer's first
	// advertise (~5s after it joins) — render the node without them till then.
	if peersOut, err := runCland(ctx, bin, "peers", "--json"); err == nil {
		var pl struct {
			Members []struct {
				MemberID string `json:"member_id"`
				Address  string `json:"address"`
				LatestAd *struct {
					OS             string `json:"os"`
					ReasoningScore int    `json:"reasoning_score"`
					SystemScore    int    `json:"system_score"`
				} `json:"latest_ad"`
			} `json:"members"`
			Candidates []struct {
				Address string `json:"address"`
			} `json:"candidates"`
		}
		if json.Unmarshal(peersOut, &pl) == nil {
			// Track addresses already shown so a peer that is BOTH an active
			// member and a manually-added candidate doesn't draw as two nodes.
			seenAddr := map[string]bool{st.SelfAddress: true}
			for _, m := range pl.Members {
				if m.MemberID == st.SelfMemberID {
					continue // self already surfaced above
				}
				mem := Member{
					ID:             m.MemberID,
					Name:           shortName(m.MemberID),
					Address:        m.Address,
					State:          "active",
					IsOrchestrator: m.MemberID == st.Orchestrator,
				}
				if m.LatestAd != nil {
					mem.OS = m.LatestAd.OS
					mem.ReasoningScore = m.LatestAd.ReasoningScore
					mem.SystemScore = m.LatestAd.SystemScore
				}
				members = append(members, mem)
				seenAddr[m.Address] = true
			}
			for _, c := range pl.Candidates {
				if seenAddr[c.Address] {
					continue // already drawn as an active member
				}
				seenAddr[c.Address] = true
				members = append(members, Member{
					Name: "candidate", Address: c.Address, State: "candidate",
				})
			}
		}
	}
	st.Members = members

	return st
}

func runCland(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), errors.New(msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func isUnaffiliated(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no clan") ||
		strings.Contains(s, "not affiliated") ||
		strings.Contains(s, "unaffiliated")
}

// parseShow reads cland's `show` text output (case-insensitive labels).
func parseShow(out []byte, st *Snapshot) {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "clan id"), strings.HasPrefix(lower, "clan_id"):
			st.ClanID = afterColon(line)
		case strings.HasPrefix(lower, "member id"), strings.HasPrefix(lower, "member_id"):
			if st.SelfMemberID == "" {
				st.SelfMemberID = afterColon(line)
			}
		case strings.HasPrefix(lower, "role"):
			st.Role = afterColon(line)
		case strings.HasPrefix(lower, "lan addr"), strings.HasPrefix(lower, "lan_addr"), strings.HasPrefix(lower, "address"):
			st.SelfAddress = afterColon(line)
		}
	}
}

func afterColon(line string) string {
	if i := strings.IndexByte(line, ':'); i >= 0 {
		return strings.TrimSpace(line[i+1:])
	}
	return ""
}

func shortName(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	if id == "" {
		return "self"
	}
	return id
}

// demo is the dev-box snapshot used when minti-cland isn't installed — it
// mirrors the locked mock so the UI renders identically without a real Clan.
func demo() Snapshot {
	return Snapshot{
		Source: "demo", Configured: true,
		ClanID: "5725d958", Role: "founder",
		SelfMemberID: "minti-a", SelfAddress: "192.168.56.101:7777",
		Orchestrator: "minti-b", IsSelfOrch: false, Term: 19, LeaseLeftSec: 6.2,
		Members: []Member{
			{ID: "minti-a", Name: "minti-a", Address: "192.168.56.101:7777", OS: "linux", State: "self", IsSelf: true, ReasoningScore: 35, SystemScore: 60},
			{ID: "minti-b", Name: "minti-b", Address: "192.168.56.102:7777", OS: "linux", State: "active", IsOrchestrator: true, ReasoningScore: 50, SystemScore: 66},
			{ID: "minti-c", Name: "minti-c", Address: "192.168.56.103:7777", OS: "windows", State: "active", Busy: true, ReasoningScore: 44, SystemScore: 58},
			{ID: "minti-d", Name: "minti-d", Address: "192.168.56.104:7777", OS: "darwin", State: "active", ReasoningScore: 41, SystemScore: 55},
			{ID: "thinkpad-x230", Name: "thinkpad-x230", Address: "192.168.56.150:7777", OS: "linux", State: "active", ReasoningScore: 22, SystemScore: 40},
			{ID: "cand-1", Name: "candidate", Address: "192.168.56.180:7777", State: "candidate"},
			{ID: "cand-2", Name: "candidate", Address: "192.168.56.181:7777", State: "candidate"},
		},
	}
}
