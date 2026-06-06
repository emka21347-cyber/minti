package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/minti/status/internal/probes/addons"
	"github.com/minti/status/internal/probes/clan"
	"github.com/minti/status/internal/probes/harness"
	"github.com/minti/status/internal/probes/runtime"
	"github.com/minti/status/internal/probes/sysinfo"
)

// buildVersion is set in main via -ldflags + read at startup. Held in
// the tui package so the View() can stamp it in the header without
// taking a dep on cmd/.
var buildVersion = "dev"

// SetBuildVersion lets main pass the embedded version string in.
func SetBuildVersion(v string) { buildVersion = v }

// Tick messages — distinct types so Update can switch on them cleanly.
type tickFastMsg time.Time
type tickMedMsg  time.Time
type tickSlowMsg time.Time

func tickFast(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickFastMsg(t) })
}
func tickMed(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMedMsg(t) })
}
func tickSlow(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickSlowMsg(t) })
}

// Probe-result messages. Each carries an error so the UI can surface it
// in the footer without trying to render a half-populated probe struct.
type sysInfoMsg struct {
	Info sysinfo.Info
	Err  error
}

type runtimeMsg struct {
	Status runtime.Status
	Err    error
}

type ollamaPSMsg struct {
	Loaded []runtime.LoadedModel
	Err    error
}

type clanMsg struct {
	State    clan.State
	Err      error
	Degraded bool // true on EACCES — UI renders "(sudo for clan details)"
}

type addonsMsg struct {
	Packs []addons.Pack
	Err   error
}

type harnessMsg struct {
	OC harness.OpencodeConfig
	CC harness.ClaudeConfig
}

type inviteMsg struct {
	Invite *clan.Invite
	Err    error
}

// inviteExpiredMsg fires from a tea.Tick when the active invite's
// expiry passes — Update clears m.invite so the panel auto-dismisses.
type inviteExpiredMsg struct{}

// cmdMintInvite shells minti-cland invite --ttl X --json off the UI
// goroutine and returns the parsed result as a tea.Msg.
func cmdMintInvite(ttl time.Duration) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		inv, err := clan.MintInvite(ctx, ttl)
		return inviteMsg{Invite: inv, Err: err}
	}
}

// cmdInviteExpiryWatch returns a tea.Cmd that fires once `d` has
// elapsed — the Update handler then clears the invite from the panel.
func cmdInviteExpiryWatch(d time.Duration) tea.Cmd {
	if d <= 0 {
		return func() tea.Msg { return inviteExpiredMsg{} }
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return inviteExpiredMsg{} })
}

// probeAll fires every probe once — used at startup + on `r` keypress.
func probeAll() tea.Cmd {
	return tea.Batch(
		cmdProbeSysinfoCheap(),
		cmdProbeSysinfoExpensive(),
		cmdProbeRuntime(),
		cmdProbeOllamaPS(),
		cmdProbeClanFull(),
		cmdProbeAddons(),
		cmdProbeHarness(),
	)
}

// Per-probe tea.Cmd factories. Each spawns a goroutine via tea.Cmd
// idiom and returns its result as a Msg.
func cmdProbeSysinfoCheap() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		info, err := sysinfo.ProbeCheap(ctx)
		return sysInfoMsg{Info: info, Err: err}
	}
}

func cmdProbeSysinfoExpensive() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		info, err := sysinfo.Probe(ctx) // includes nvidia-smi etc.
		return sysInfoMsg{Info: info, Err: err}
	}
}

func cmdProbeRuntime() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		st, err := runtime.Probe(ctx)
		return runtimeMsg{Status: st, Err: err}
	}
}

func cmdProbeOllamaPS() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		loaded, err := runtime.ProbeOllamaPS(ctx)
		return ollamaPSMsg{Loaded: loaded, Err: err}
	}
}

// cmdProbeClanFast: just `minti-cland orchestrator --json` — cheap,
// fires on fast tick so the term/lease countdown stays current.
func cmdProbeClanFast() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		st, err := clan.ProbeOrchestratorOnly(ctx)
		degraded := clan.IsPermissionDenied(err)
		return clanMsg{State: st, Err: err, Degraded: degraded}
	}
}

// cmdProbeClanFull: every clan subcommand (members, peers, history).
// Fires on medium tick.
func cmdProbeClanFull() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		st, err := clan.Probe(ctx)
		degraded := clan.IsPermissionDenied(err)
		return clanMsg{State: st, Err: err, Degraded: degraded}
	}
}

func cmdProbeAddons() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		packs, err := addons.Probe(ctx)
		return addonsMsg{Packs: packs, Err: err}
	}
}

func cmdProbeHarness() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		oc := harness.ProbeOpencode(ctx)
		cc := harness.ProbeClaude(ctx)
		return harnessMsg{OC: oc, CC: cc}
	}
}
