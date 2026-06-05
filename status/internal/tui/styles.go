package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// styles is a singleton holding pre-built lipgloss.Style values. Built
// once at startup so we don't repeat the same allocations every tick.
var styles = struct {
	// Palette — matches minti-fetch (mint 42, cyan 81, grey 245).
	Mint   lipgloss.Style
	Cyan   lipgloss.Style
	Grey   lipgloss.Style
	White  lipgloss.Style
	Red    lipgloss.Style
	Yellow lipgloss.Style

	// Semantic shortcuts.
	Label    func(s string) string
	Value    func(s string) string
	Faint    func(s string) string
	Good     func(s string) string
	Warn     func(s string) string
	Bad      func(s string) string
	Title    func(s string) string
	Self     func(s string) string // ★ next to local member / role=founder
}{}

func init() {
	styles.Mint = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styles.Cyan = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	styles.Grey = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styles.White = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	styles.Red = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styles.Yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	title := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	self := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)

	// Wrap Render (variadic in lipgloss) into single-string helpers.
	styles.Label = func(s string) string { return styles.Grey.Render(s) }
	styles.Value = func(s string) string { return styles.White.Render(s) }
	styles.Faint = func(s string) string { return styles.Grey.Render(s) }
	styles.Good = func(s string) string { return styles.Mint.Render(s) }
	styles.Warn = func(s string) string { return styles.Yellow.Render(s) }
	styles.Bad = func(s string) string { return styles.Red.Render(s) }
	styles.Title = func(s string) string { return title.Render(s) }
	styles.Self = func(s string) string { return self.Render(s) }
}
