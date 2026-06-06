package tui

import (
	"strings"
	"time"

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

	now := time.Now()
	sys := panels.System(m.sys)
	rt := panels.Runtime(m.rt, m.vram)
	cn := panels.Clan(m.clan, m.clanErr, now)
	inv := panels.Invite(m.invite, now)
	ad := panels.Addons(m.addons)
	hn := panels.Harness(m.opencode, m.claudecfg)

	parts := []string{}
	if wide {
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, sys, rt))
		parts = append(parts, cn)
		if inv != "" {
			parts = append(parts, inv)
		}
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, ad, hn))
	} else {
		parts = append(parts, sys, rt, cn)
		if inv != "" {
			parts = append(parts, inv)
		}
		parts = append(parts, ad, hn)
	}
	return strings.Join(parts, "\n")
}
