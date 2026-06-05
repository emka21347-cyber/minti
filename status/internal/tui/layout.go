package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/minti/status/internal/tui/panels"
)

// layout assembles the body of the dashboard. Wide (≥100 cols): two
// 2-column rows + a wide Clan row between them. Narrow (<100): all
// panels stacked vertically.
//
// Each panel function takes the probe struct(s) it cares about. Panels
// own their box-drawing; layout.go just glues them together.
func layout(m Model) string {
	wide := m.width >= 100

	sys := panels.System(m.sys)
	rt := panels.Runtime(m.rt, m.vram)
	cn := panels.Clan(m.clan, m.clanErr)
	ad := panels.Addons(m.addons)
	hn := panels.Harness(m.opencode, m.claudecfg)

	if wide {
		topRow := lipgloss.JoinHorizontal(lipgloss.Top, sys, rt)
		bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, ad, hn)
		return strings.Join([]string{topRow, cn, bottomRow}, "\n")
	}
	return strings.Join([]string{sys, rt, cn, ad, hn}, "\n")
}
