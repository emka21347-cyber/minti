// Package tui implements the bubbletea-based MINTI status dashboard.
//
// Architecture: a single bubbletea.Model holds the latest result of each
// probe. Three independent tickers (fast/medium/slow) emit tick messages;
// Update fires probe tea.Cmds that run off the UI goroutine and return
// result messages. The View renders whatever the model currently holds —
// it never blocks on a probe. A probe error never crashes the UI; it
// surfaces in the footer's last-err line + degrades the affected panel
// gracefully (e.g. "(sudo for clan details)" on EACCES).
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/minti/status/internal/probes/addons"
	"github.com/minti/status/internal/probes/clan"
	"github.com/minti/status/internal/probes/harness"
	"github.com/minti/status/internal/probes/runtime"
	"github.com/minti/status/internal/probes/sysinfo"
	"github.com/minti/status/internal/tui/panels"
)

// Options configures a TUI run. Held outside the Model so --once can take
// a different rendering path without re-instantiating the whole stack.
type Options struct {
	Once    bool
	NoColor bool
	Refresh time.Duration // fast tick; medium = 2.5×, slow = 15× of this
}

// Run blocks until the user quits or an unrecoverable error occurs.
func Run(opts Options) error {
	if opts.Refresh <= 0 {
		opts.Refresh = 2 * time.Second
	}

	if opts.Once {
		return runOnce(opts)
	}

	m := newModel(opts)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// runOnce: render a single snapshot to stdout. Useful when stderr is
// non-TTY (CI, SSH pipe, `minti-status --once | tee`). Hits all probes
// once synchronously then prints the same View() output minus the
// keybinds footer.
func runOnce(opts Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	m := newModel(opts)
	m.sys, _ = sysinfo.Probe(ctx)
	m.rt, _ = runtime.Probe(ctx)
	m.vram, _ = runtime.ProbeOllamaPS(ctx)
	m.clan, m.clanErr = clan.Probe(ctx)
	m.addons, _ = addons.Probe(ctx)
	m.opencode = harness.ProbeOpencode(ctx)
	m.claudecfg = harness.ProbeClaude(ctx)
	m.ready = true
	fmt.Print(m.View())
	if !strings.HasSuffix(m.View(), "\n") {
		fmt.Println()
	}
	return nil
}

// Model is the single bubbletea state container.
type Model struct {
	opts Options

	// terminal dimensions
	width, height int

	// probe results — Update merges results from messages into here;
	// View reads exclusively from here.
	sys       sysinfo.Info
	rt        runtime.Status
	vram      []runtime.LoadedModel
	clan      clan.State
	clanErr   error
	addons    []addons.Pack
	opencode  harness.OpencodeConfig
	claudecfg harness.ClaudeConfig

	lastErr     string    // shows in footer
	lastTick    time.Time // for the header's "[r 2.0s]" badge
	showHelp    bool
	ready       bool // true once the first round of probes has landed
}

func newModel(opts Options) Model {
	return Model{
		opts:     opts,
		width:    100,
		height:   30,
		lastTick: time.Now(),
	}
}

// Init kicks off the three tickers + an initial probe of every source so
// the UI populates within the first second instead of waiting for the
// first tick.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickFast(m.opts.Refresh),
		tickMed(m.opts.Refresh*5/2),
		tickSlow(m.opts.Refresh*15),
		probeAll(),
	)
}

// Update handles input + tick + probe-result messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			m.lastTick = time.Now()
			return m, probeAll()
		case "?", "h":
			m.showHelp = !m.showHelp
			return m, nil
		}

	case tickFastMsg:
		m.lastTick = time.Time(msg)
		return m, tea.Batch(
			tickFast(m.opts.Refresh),
			cmdProbeRuntime(),
			cmdProbeOllamaPS(),
			cmdProbeClanFast(),
		)

	case tickMedMsg:
		return m, tea.Batch(
			tickMed(m.opts.Refresh*5/2),
			cmdProbeSysinfoCheap(),
			cmdProbeAddons(),
			cmdProbeClanFull(),
		)

	case tickSlowMsg:
		return m, tea.Batch(
			tickSlow(m.opts.Refresh*15),
			cmdProbeSysinfoExpensive(),
			cmdProbeHarness(),
		)

	case sysInfoMsg:
		if msg.Err == nil {
			// Merge slow-tick fields: the medium-tick ProbeCheap returns
			// an Info without CPUModel/CPUCores/GPU (they're only set by
			// the slow-tick Probe). Without this merge, every 5s the
			// fast/medium probe wipes the slow fields and the panel
			// blinks back to "(n/a)" until the next 30s slow tick.
			if msg.Info.CPUModel == "" {
				msg.Info.CPUModel = m.sys.CPUModel
				msg.Info.CPUCores = m.sys.CPUCores
			}
			if msg.Info.GPU == "" {
				msg.Info.GPU = m.sys.GPU
			}
			m.sys = msg.Info
		} else {
			m.lastErr = "sysinfo: " + msg.Err.Error()
		}
		m.ready = true
		return m, nil

	case runtimeMsg:
		if msg.Err == nil {
			m.rt = msg.Status
		}
		// runtime down isn't an "error" — panel shows (not running)
		return m, nil

	case ollamaPSMsg:
		if msg.Err == nil {
			m.vram = msg.Loaded
		}
		return m, nil

	case clanMsg:
		// Merge: the fast-tick ProbeOrchestratorOnly returns a State with
		// only clan/orchestrator/term/lease populated — Members,
		// Candidates, and RecentElections are empty. Without preserving
		// the previous full-tick values, the panel blinks between
		// "Members (1)" (just synthetic self) and "Members (3)" (full
		// roster) every 2s. We preserve them if the new State doesn't
		// carry them.
		if msg.Err == nil {
			if len(msg.State.Members) == 0 && len(m.clan.Members) > 0 {
				msg.State.Members = m.clan.Members
			}
			if len(msg.State.Candidates) == 0 && len(m.clan.Candidates) > 0 {
				msg.State.Candidates = m.clan.Candidates
			}
			if len(msg.State.RecentElections) == 0 && len(m.clan.RecentElections) > 0 {
				msg.State.RecentElections = m.clan.RecentElections
			}
		}
		m.clan, m.clanErr = msg.State, msg.Err
		if msg.Err != nil && !msg.Degraded {
			m.lastErr = "clan: " + msg.Err.Error()
		}
		return m, nil

	case addonsMsg:
		if msg.Err == nil {
			m.addons = msg.Packs
		}
		return m, nil

	case harnessMsg:
		m.opencode = msg.OC
		m.claudecfg = msg.CC
		return m, nil
	}

	return m, nil
}

// View renders the dashboard. Panels assemble themselves; layout.go
// decides whether to stack them 1- or 2-column based on width.
func (m Model) View() string {
	if !m.ready {
		return styles.Faint("starting probes…\n")
	}

	header := panels.Header(panels.HeaderData{
		Hostname:   m.sys.Hostname,
		User:       m.sys.User,
		Now:        time.Now(),
		MintiVer:   m.rt.Version,
		StatusVer:  buildVersion,
		RefreshDur: m.opts.Refresh,
		LastTick:   m.lastTick,
		Width:      m.width,
	})

	body := layout(m)

	footer := panels.Footer(panels.FooterData{
		LastErr: m.lastErr,
		Width:   m.width,
		Help:    m.showHelp,
	})

	return strings.Join([]string{header, body, footer}, "\n")
}
