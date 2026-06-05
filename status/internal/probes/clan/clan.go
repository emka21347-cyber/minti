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
	"sort"
	"strings"
	"time"
)

// State is the full Clan snapshot. Empty when no Clan is configured;
// partially filled when only orchestrator info has been refreshed.
type State struct {
	Configured   bool
	ClanID       string
	Role         string // "founder" | "member" | "admitted" | ...
	Pin          string // sha256:hex...
	SelfMemberID string
	SelfAddress  string // from `minti-cland show` LAN addr line

	Orchestrator string // member display name or member_id prefix
	IsSelfOrch   bool
	Term         int
	LeaseLeft    time.Duration

	Members         []Member
	Candidates      []Candidate
	RecentElections []ElectionEntry // deduped by (term, winner)
}

// Member is one peer in the live registry — i.e. a node that has been
// admitted to the Clan AND has advertised capabilities at least once.
// Self is synthesised separately by the panel; cland's peers --json
// omits self.
type Member struct {
	MemberID       string
	Address        string
	DiscoveredVia  string // "mdns" | "manual"
	OS             string // "linux" | "windows" | "darwin"
	GPU            string // e.g. "NVIDIA GeForce RTX 5090" or ""
	VRAMGB         float64
	RAMGB          float64
	State          string // "active" | "admitted" | "revoked"
	ReasoningScore int
	SystemScore    int
	LastAd         time.Duration // time.Since(last_ad)
	HeartbeatSeen  bool
	Generation     int
	IsOrchestrator bool
	IsSelf         bool // synthesised by Probe() before returning
}

// Candidate is a peer discovered via mDNS / manual peer-add but not yet
// member-added (no successful capability advertisement received).
type Candidate struct {
	Address       string
	DiscoveredVia string
	FirstSeen     time.Time
}

// ElectionEntry is one row in the deduped election history.
type ElectionEntry struct {
	Term     int
	Winner   string
	Reason   string
	At       time.Time // most recent firing of this (term, winner)
	Repeated int       // 1 means seen once; >1 = how many times this exact event fired
}

// ErrPermissionDenied indicates the CLI ran but couldn't read clan_key.
type ErrPermissionDenied struct{ Wrapped error }

func (e *ErrPermissionDenied) Error() string {
	return "permission denied (sudo for clan details)"
}
func (e *ErrPermissionDenied) Unwrap() error { return e.Wrapped }

func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	var pd *ErrPermissionDenied
	return errors.As(err, &pd)
}

// Probe runs the full set of CLI subcommands. Use ProbeOrchestratorOnly
// on the fast tick to refresh just the term/lease countdown.
func Probe(ctx context.Context) (State, error) {
	st, err := ProbeOrchestratorOnly(ctx)
	if err != nil {
		return st, err
	}
	if !st.Configured {
		return st, nil
	}

	if members, candidates, err := readPeers(ctx); err == nil {
		st.Members = members
		st.Candidates = candidates
		// Tag the orchestrator inside the member list.
		for i := range st.Members {
			if st.Members[i].MemberID == st.SelfMemberID {
				st.Members[i].IsSelf = true
			}
			if st.IsSelfOrch && st.Members[i].MemberID == st.SelfMemberID {
				st.Members[i].IsOrchestrator = true
			} else if !st.IsSelfOrch && st.Members[i].MemberID == st.Orchestrator {
				st.Members[i].IsOrchestrator = true
			}
		}
	}

	if hist, err := readHistory(ctx); err == nil {
		st.RecentElections = hist
	}

	return st, nil
}

// ProbeOrchestratorOnly: cheap fast-tick refresh.
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
		return st, nil // orchestrator unknown isn't fatal
	}
	var orch struct {
		CurrentOrchestrator string `json:"current_orchestrator"`
		CurrentTerm         int    `json:"current_term"`
		LeaseExpires        string `json:"lease_expires"`
		Self                string `json:"self"`
		IsSelf              bool   `json:"is_self"`
	}
	if json.Unmarshal(orchOut, &orch) == nil {
		st.Orchestrator = orch.CurrentOrchestrator
		st.IsSelfOrch = orch.IsSelf
		st.Term = orch.CurrentTerm
		// Prefer orchestrator's self (full UUID) over parseShow's value.
		if orch.Self != "" {
			st.SelfMemberID = orch.Self
		}
		if t, err := time.Parse(time.RFC3339, orch.LeaseExpires); err == nil {
			if d := time.Until(t); d > 0 {
				st.LeaseLeft = d.Round(time.Second)
			}
		}
	}
	return st, nil
}

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

// parseShow handles cland's actual output labels (case-insensitive):
//
//	Member ID:  a9f3df01-...
//	Clan ID:    5725d958-...
//	Role:       founder
//	Cert pin:   sha256:f6db79...
//	LAN addr:   192.168.56.102:7777
func parseShow(out []byte, st *State) {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "clan id"), strings.HasPrefix(lower, "clan_id"):
			st.ClanID = afterColon(line)
		case strings.HasPrefix(lower, "member id"), strings.HasPrefix(lower, "member_id"), strings.HasPrefix(lower, "member:"):
			if st.SelfMemberID == "" {
				st.SelfMemberID = afterColon(line)
			}
		case strings.HasPrefix(lower, "role"):
			st.Role = afterColon(line)
		case strings.HasPrefix(lower, "cert pin"), strings.HasPrefix(lower, "pin"):
			st.Pin = afterColon(line)
		case strings.HasPrefix(lower, "lan addr"), strings.HasPrefix(lower, "address"):
			st.SelfAddress = afterColon(line)
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

// peersResp matches the real shape of `minti-cland peers --json`. See
// cland/internal/peers/peers.go for the source of truth; we only pull
// the fields we render.
type peersResp struct {
	Candidates []struct {
		Address       string `json:"address"`
		DiscoveredVia string `json:"discovered_via"`
		FirstSeen     string `json:"first_seen"`
	} `json:"candidates"`
	Members []struct {
		MemberID       string `json:"member_id"`
		Address        string `json:"address"`
		DiscoveredVia  string `json:"discovered_via"`
		LastAd         string `json:"last_ad"`
		LastSeenAt     string `json:"last_seen_at"`
		AdGeneration   int    `json:"ad_generation"`
		HeartbeatSeen  bool   `json:"heartbeat_seen"`
		LatestAd       struct {
			OS             string `json:"os"`
			ReasoningScore int    `json:"reasoning_score"`
			SystemScore    int    `json:"system_score"`
			Hardware       struct {
				GPU    string  `json:"gpu"`
				RAMGB  float64 `json:"ram_gb"`
				VRAMGB float64 `json:"vram_gb"`
			} `json:"hardware"`
		} `json:"latest_ad"`
	} `json:"members"`
}

func readPeers(ctx context.Context) ([]Member, []Candidate, error) {
	out, err := runCland(ctx, "peers", "--json")
	if err != nil {
		return nil, nil, err
	}
	var resp peersResp
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, nil, err
	}

	members := make([]Member, 0, len(resp.Members))
	for _, m := range resp.Members {
		mm := Member{
			MemberID:       m.MemberID,
			Address:        m.Address,
			DiscoveredVia:  m.DiscoveredVia,
			OS:             m.LatestAd.OS,
			GPU:            m.LatestAd.Hardware.GPU,
			VRAMGB:         m.LatestAd.Hardware.VRAMGB,
			RAMGB:          m.LatestAd.Hardware.RAMGB,
			ReasoningScore: m.LatestAd.ReasoningScore,
			SystemScore:    m.LatestAd.SystemScore,
			HeartbeatSeen:  m.HeartbeatSeen,
			Generation:     m.AdGeneration,
		}
		// State: cland's wire state isn't in peers --json (it's in
		// members --json, the roster). Approximate from freshness:
		// heartbeat_seen + recent ad → "active", else "admitted".
		if mm.HeartbeatSeen {
			mm.State = "active"
		} else {
			mm.State = "admitted"
		}
		if t, err := time.Parse(time.RFC3339Nano, m.LastAd); err == nil {
			mm.LastAd = time.Since(t).Round(time.Second)
		}
		members = append(members, mm)
	}

	candidates := make([]Candidate, 0, len(resp.Candidates))
	for _, c := range resp.Candidates {
		fc := Candidate{
			Address:       c.Address,
			DiscoveredVia: c.DiscoveredVia,
		}
		if t, err := time.Parse(time.RFC3339Nano, c.FirstSeen); err == nil {
			fc.FirstSeen = t
		}
		candidates = append(candidates, fc)
	}

	return members, candidates, nil
}

// readHistory returns the most recent UNIQUE elections (deduped by
// (term, winner)). Cland fires an election event on every successful
// heartbeat round, so a stable Clan may have hundreds of entries
// describing the same actual leadership event. We collapse those.
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

	// Dedupe by (term, winner). Keep the latest At + total repetitions.
	type key struct {
		term   int
		winner string
	}
	byKey := map[key]*ElectionEntry{}
	order := []key{}
	for _, e := range resp.Entries {
		k := key{e.Term, e.Winner}
		t, _ := time.Parse(time.RFC3339, e.At)
		if entry, ok := byKey[k]; ok {
			entry.Repeated++
			if t.After(entry.At) {
				entry.At = t
				entry.Reason = e.Reason
			}
		} else {
			byKey[k] = &ElectionEntry{
				Term:     e.Term,
				Winner:   e.Winner,
				Reason:   e.Reason,
				At:       t,
				Repeated: 1,
			}
			order = append(order, k)
		}
	}

	// Sort by most recent At descending, take top 3.
	sort.Slice(order, func(i, j int) bool {
		return byKey[order[i]].At.After(byKey[order[j]].At)
	})
	if len(order) > 3 {
		order = order[:3]
	}
	out2 := make([]ElectionEntry, 0, len(order))
	for _, k := range order {
		out2 = append(out2, *byKey[k])
	}
	return out2, nil
}
