package panels

import (
	"errors"
	"testing"
	"time"

	"github.com/minti/status/internal/probes/clan"
)

// refTime is the fixed wall-clock used by every golden test in this
// package. Picking a stable RFC3339 anchor keeps the "X minutes ago"
// strings deterministic.
var refTime = time.Date(2026, 6, 6, 14, 32, 1, 0, time.UTC)

func init() {
	// Pin selfOS so darwin / windows runs of `go test` produce the same
	// goldens as the linux dev box.
	selfOS = "linux"
}

func TestClan_Unaffiliated(t *testing.T) {
	got := Clan(clan.State{Configured: false}, nil, refTime)
	assertGolden(t, "clan_unaffiliated", got)
}

func TestClan_PermissionDenied(t *testing.T) {
	wrapped := errors.New("permission denied: clan.json")
	got := Clan(clan.State{}, &clan.ErrPermissionDenied{Wrapped: wrapped}, refTime)
	assertGolden(t, "clan_eacces", got)
}

// TestClan_SelfOrchestrator: 3-node Clan, self is the Orchestrator.
// Self row gets ★ + (self); peers render with state/scores/ages.
func TestClan_SelfOrchestrator(t *testing.T) {
	st := clan.State{
		Configured:   true,
		ClanID:       "5725d958-2dda-401a-8eed-2e513c2ebffe",
		Role:         "founder",
		Pin:          "sha256:f6db79289845fe1204d6dc7ae6b62ca8",
		SelfMemberID: "a9f3df01-e600-4ca3-80a8-c910a00430ee",
		SelfAddress:  "192.168.56.102:7777",
		Orchestrator: "a9f3df01-e600-4ca3-80a8-c910a00430ee",
		IsSelfOrch:   true,
		Term:         12,
		LeaseLeft:    47 * time.Second,
		Members: []clan.Member{
			{
				MemberID:       "57056a7f-fe7c-443d-a92c-388c2777af8b",
				Address:        "192.168.56.101:7777",
				OS:             "linux",
				State:          "active",
				ReasoningScore: 35,
				SystemScore:    22,
				LastAd:         3 * time.Second,
				HeartbeatSeen:  true,
			},
			{
				MemberID:       "e6c07875-f4bd-4c28-a178-c9b76d01b668",
				Address:        "192.168.56.1:7777",
				OS:             "windows",
				State:          "active",
				ReasoningScore: 50,
				SystemScore:    66,
				LastAd:         1 * time.Second,
				HeartbeatSeen:  true,
			},
		},
		Candidates: []clan.Candidate{
			{Address: "10.0.2.15:7777", DiscoveredVia: "mdns"},
		},
		RecentElections: []clan.ElectionEntry{
			{
				Term:     12,
				Winner:   "a9f3df01-e600-4ca3-80a8-c910a00430ee",
				Reason:   "score",
				At:       refTime.Add(-2 * time.Minute),
				Repeated: 1,
			},
			{
				Term:     11,
				Winner:   "57056a7f-fe7c-443d-a92c-388c2777af8b",
				Reason:   "lease-expired",
				At:       refTime.Add(-18 * time.Minute),
				Repeated: 1,
			},
		},
	}
	got := Clan(st, nil, refTime)
	assertGolden(t, "clan_self_orchestrator", got)
}

// TestClan_PeerOrchestrator: same Clan, but the leader is the peer at
// 192.168.56.101 — self is "member" not "founder", ★ moves to the peer.
func TestClan_PeerOrchestrator(t *testing.T) {
	peerID := "57056a7f-fe7c-443d-a92c-388c2777af8b"
	st := clan.State{
		Configured:   true,
		ClanID:       "5725d958-2dda-401a-8eed-2e513c2ebffe",
		Role:         "member",
		Pin:          "sha256:f6db79289845fe1204d6dc7ae6b62ca8",
		SelfMemberID: "a9f3df01-e600-4ca3-80a8-c910a00430ee",
		SelfAddress:  "192.168.56.102:7777",
		Orchestrator: peerID,
		IsSelfOrch:   false,
		Term:         13,
		LeaseLeft:    20 * time.Second,
		Members: []clan.Member{
			{
				MemberID:       peerID,
				Address:        "192.168.56.101:7777",
				OS:             "linux",
				State:          "active",
				ReasoningScore: 78,
				SystemScore:    68,
				LastAd:         1 * time.Second,
				HeartbeatSeen:  true,
				IsOrchestrator: true,
			},
			{
				MemberID:       "e6c07875-f4bd-4c28-a178-c9b76d01b668",
				Address:        "192.168.56.1:7777",
				OS:             "windows",
				State:          "active",
				ReasoningScore: 50,
				SystemScore:    66,
				LastAd:         2 * time.Second,
				HeartbeatSeen:  true,
			},
		},
		RecentElections: []clan.ElectionEntry{
			{
				Term:     13,
				Winner:   peerID,
				Reason:   "lease-expired",
				At:       refTime.Add(-30 * time.Second),
				Repeated: 1,
			},
		},
	}
	got := Clan(st, nil, refTime)
	assertGolden(t, "clan_peer_orchestrator", got)
}

// TestClan_DedupedHistory: same election event recorded 32× (cland
// emits one per heartbeat tick when the Clan is stable). Panel should
// collapse to a single line with (×32).
func TestClan_DedupedHistory(t *testing.T) {
	st := clan.State{
		Configured:   true,
		ClanID:       "5725d958-2dda-401a-8eed-2e513c2ebffe",
		Role:         "founder",
		SelfMemberID: "a9f3df01-e600-4ca3-80a8-c910a00430ee",
		SelfAddress:  "192.168.56.102:7777",
		Orchestrator: "a9f3df01-e600-4ca3-80a8-c910a00430ee",
		IsSelfOrch:   true,
		Term:         3368,
		LeaseLeft:    5 * time.Second,
		RecentElections: []clan.ElectionEntry{
			{
				Term:     3368,
				Winner:   "a9f3df01-e600-4ca3-80a8-c910a00430ee",
				Reason:   "bootstrap",
				At:       refTime.Add(-4 * time.Second),
				Repeated: 32,
			},
		},
	}
	got := Clan(st, nil, refTime)
	assertGolden(t, "clan_deduped_history", got)
}
