package panels

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// HeaderData is what the tui package collects from its Model to feed
// the header.
type HeaderData struct {
	Hostname   string
	User       string
	Now        time.Time
	MintiVer   string // runtime adapter version
	StatusVer  string // minti-status own version (-ldflags)
	RefreshDur time.Duration
	LastTick   time.Time
	Width      int
}

// Header renders the top status bar:
//
//	┌─ minti-status v0.3.0-M7 ───────── alice@node-3  06 Jun 14:32:01  [r 2.0s] ─┐
func Header(d HeaderData) string {
	if d.Width <= 0 {
		d.Width = fullWidth
	}
	left := fmt.Sprintf(" minti-status %s ", d.StatusVer)
	if d.MintiVer != "" {
		left = fmt.Sprintf(" minti-status %s · runtime %s ", d.StatusVer, d.MintiVer)
	}

	right := fmt.Sprintf(" %s@%s  %s  [r %s] ",
		ifEmpty(d.User, "?"),
		ifEmpty(d.Hostname, "?"),
		d.Now.Format("02 Jan 15:04:05"),
		d.RefreshDur,
	)

	gap := d.Width - lipgloss.Width(left) - lipgloss.Width(right) - 2 // 2 for the ┌ ┐
	if gap < 1 {
		gap = 1
	}
	dashes := strings.Repeat("─", gap)

	style := lipgloss.NewStyle().Foreground(colorGrey)
	titled := lipgloss.NewStyle().Foreground(colorMint).Bold(true).Render(strings.TrimSpace(left))
	return style.Render("┌─") +
		" " + titled + " " +
		style.Render(dashes) +
		styleValue.Render(right) +
		style.Render("─┐")
}

func ifEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// FooterData feeds the footer (keybinds + last-error line).
type FooterData struct {
	LastErr string
	Width   int
	Help    bool
}

// Footer renders the bottom status bar:
//
//	└─ q quit  r refresh  ?/h help                last-err: clan: permission denied ─┘
func Footer(d FooterData) string {
	if d.Width <= 0 {
		d.Width = fullWidth
	}
	left := " q quit  r refresh  ?/h help "
	if d.Help {
		left = " q quit · r refresh · ? hide help · Esc/Ctrl+C exit "
	}
	right := ""
	if d.LastErr != "" {
		right = " last-err: " + d.LastErr + " "
	}
	gap := d.Width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	dashes := strings.Repeat("─", gap)
	style := lipgloss.NewStyle().Foreground(colorGrey)
	leftRender := styleLabel.Render(left)
	rightRender := ""
	if right != "" {
		rightRender = styleWarn.Render(right)
	}
	return style.Render("└─") + leftRender + style.Render(dashes) + rightRender + style.Render("─┘")
}
