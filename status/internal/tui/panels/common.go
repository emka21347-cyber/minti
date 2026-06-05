// Package panels renders individual dashboard panels from probe structs.
//
// Each panel is a function returning a styled multi-line string. layout.go
// then composes them via lipgloss.JoinHorizontal/JoinVertical. Panels own
// their internal widths (no responsive logic here — that lives in layout).
package panels

import (
	"github.com/charmbracelet/lipgloss"
)

// Palette — kept in sync with internal/tui/styles.go (the tui package
// is the consumer, this is the library; we duplicate the 4-5 colors we
// need so panels stays decoupled from tui).
var (
	colorMint   = lipgloss.Color("42")
	colorCyan   = lipgloss.Color("81")
	colorGrey   = lipgloss.Color("245")
	colorWhite  = lipgloss.Color("255")
	colorRed    = lipgloss.Color("196")
	colorYellow = lipgloss.Color("214")

	styleTitle  = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	styleLabel  = lipgloss.NewStyle().Foreground(colorGrey)
	styleValue  = lipgloss.NewStyle().Foreground(colorWhite)
	styleFaint  = lipgloss.NewStyle().Foreground(colorGrey)
	styleGood   = lipgloss.NewStyle().Foreground(colorMint)
	styleWarn   = lipgloss.NewStyle().Foreground(colorYellow)
	styleBad    = lipgloss.NewStyle().Foreground(colorRed)
	styleSelf   = lipgloss.NewStyle().Foreground(colorMint).Bold(true)
)

// Default per-panel widths (in cells). Layout passes the actual width
// when relevant; these are defaults for tests + when the terminal is
// not yet sized.
const (
	halfWidth = 50  // two of these = 100 cols → matches the layout floor
	fullWidth = 100
)

// titleBar returns a colored title rendered at the top of a panel:
//
//	▎ Title          ────────────────────
func titleBar(name string, width int) string {
	bar := "▎ " + styleTitle.Render(name) + " "
	rest := width - lipgloss.Width(bar)
	if rest < 0 {
		rest = 0
	}
	pad := lipgloss.NewStyle().Foreground(colorGrey).Render(repeat("─", rest))
	return bar + pad
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, n*len(s))
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

// row builds one "  label  value" line padded to width.
func row(label, value string, labelW, width int) string {
	l := styleLabel.Render(padRight(label, labelW))
	v := styleValue.Render(value)
	line := "  " + l + " " + v
	return padRight(line, width)
}

// padRight pads s with spaces to exactly `w` visible cells (lipgloss-aware).
func padRight(s string, w int) string {
	cur := lipgloss.Width(s)
	if cur >= w {
		return s
	}
	return s + repeat(" ", w-cur)
}
