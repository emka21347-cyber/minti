package panels

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/minti/status/internal/probes/clan"
)

// selfOS is the OS string used for the synthesised self row. Defaults
// to runtime.GOOS; tests override to keep goldens deterministic across
// build platforms.
var selfOS = runtime.GOOS

// Clan renders the Clan panel — the headline of the dashboard.
// `now` is the reference time used to compute "time since" for
// election-history rows; pass time.Now() in production, a fixed value
// from goldens in tests.
func Clan(st clan.State, probeErr error, now time.Time) string {
	const w = fullWidth
	const lbl = 16

	var b strings.Builder
	b.WriteString(titleBar("Clan", w) + "\n")

	if clan.IsPermissionDenied(probeErr) {
		msg := styleWarn.Render("(sudo for clan details)")
		b.WriteString(row("Clan ID", msg, lbl, w))
		return b.String()
	}

	if !st.Configured {
		b.WriteString(row("Clan ID", styleFaint.Render("(unaffiliated — run `minti-cland create`)"), lbl, w))
		return b.String()
	}

	// Row 1: Clan ID + role + pin
	header := styleValue.Render(shortID(st.ClanID))
	if st.Role != "" {
		header += "    " + styleFaint.Render("role=") + styleValue.Render(st.Role)
	}
	if st.Pin != "" {
		header += "    " + styleFaint.Render("pin=") + styleValue.Render(shortenPin(st.Pin))
	}
	b.WriteString(row("Clan ID", header, lbl, w) + "\n")

	// Row 2: Orchestrator + term + lease
	orchTxt := shortID(st.Orchestrator)
	if orchTxt == "" {
		orchTxt = "(no orchestrator yet)"
	}
	orchLine := styleValue.Render(orchTxt)
	if st.IsSelfOrch {
		orchLine = styleSelf.Render(orchTxt + " ★ (self)")
	}
	if st.Term > 0 {
		orchLine += "    " + styleFaint.Render("term=") + styleValue.Render(fmt.Sprintf("%d", st.Term))
	}
	if st.LeaseLeft > 0 {
		orchLine += "    " + styleFaint.Render("lease ") + styleValue.Render(fmtTTL(st.LeaseLeft))
	}
	b.WriteString(row("Orchestrator", orchLine, lbl, w) + "\n")

	// Members table — synthesise a self row at the top.
	rows := make([]memberDisplay, 0, len(st.Members)+1)
	rows = append(rows, selfDisplay(st))
	for _, m := range st.Members {
		rows = append(rows, memberDisplayFrom(m))
	}

	memberCount := len(rows)
	b.WriteString(padRight("  "+styleFaint.Render(fmt.Sprintf("Members (%d)", memberCount)), lbl+2) +
		memberHeaderRow() + "\n")
	for _, r := range rows {
		b.WriteString(padRight("  "+strings.Repeat(" ", lbl)+r.render(), w) + "\n")
	}

	// Candidates (mDNS / manual peer-add but no successful capability ad yet).
	if len(st.Candidates) > 0 {
		caps := make([]string, 0, len(st.Candidates))
		for _, c := range st.Candidates {
			caps = append(caps, c.Address+styleFaint.Render(" ("+c.DiscoveredVia+")"))
		}
		b.WriteString(row(fmt.Sprintf("Candidates (%d)", len(st.Candidates)),
			styleFaint.Render(strings.Join(caps, "  ")), lbl, w) + "\n")
	}

	// Elections — deduped + count of repeats.
	if len(st.RecentElections) > 0 {
		b.WriteString(row("Last elections", "", lbl, w) + "\n")
		for _, e := range st.RecentElections {
			ago := now.Sub(e.At)
			repeat := ""
			if e.Repeated > 1 {
				repeat = fmt.Sprintf("  (×%d)", e.Repeated)
			}
			line := fmt.Sprintf("term %d  ← %s   reason=%s   %s ago%s",
				e.Term, shortID(e.Winner), e.Reason, fmtAgo(ago), repeat)
			b.WriteString(padRight("  "+strings.Repeat(" ", lbl)+styleFaint.Render(line), w) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// memberDisplay is the panel's view of one row. Built from either the
// synthetic self entry or a real clan.Member.
type memberDisplay struct {
	Bullet   string // styled
	Name     string // address or short member_id
	OS       string
	State    string
	Reason   int
	Sys      int
	Ad       string // formatted "Xs ago" / "Xm ago" / "—"
	IsSelf   bool
	IsOrch   bool
}

func memberDisplayFrom(m clan.Member) memberDisplay {
	bullet := styleFaint.Render("○")
	if m.State == "active" && m.LastAd > 0 && m.LastAd <= 90*time.Second {
		bullet = styleGood.Render("●")
	}
	name := m.Address
	if name == "" {
		name = shortID(m.MemberID)
	}
	return memberDisplay{
		Bullet: bullet,
		Name:   name,
		OS:     m.OS,
		State:  m.State,
		Reason: m.ReasoningScore,
		Sys:    m.SystemScore,
		Ad:     dashDur(m.LastAd),
		IsOrch: m.IsOrchestrator,
	}
}

// selfDisplay synthesises a row for the local node. cland's peers --json
// excludes self; we surface it from State + the local runtime data.
func selfDisplay(st clan.State) memberDisplay {
	name := st.SelfAddress
	if name == "" {
		name = shortID(st.SelfMemberID)
	}
	return memberDisplay{
		Bullet: styleGood.Render("●"),
		Name:   name,
		OS:     selfOS, // overridable via the selfOS var for tests
		State:  st.Role,
		Reason: 0, // not surfaced for self in v1
		Sys:    0,
		Ad:     "now",
		IsSelf: true,
		IsOrch: st.IsSelfOrch,
	}
}

func memberHeaderRow() string {
	return styleFaint.Render(fmt.Sprintf(" %-22s %-9s %-9s %-7s %-7s %s",
		"member", "os", "state", "reason", "sys", "ad"))
}

func (m memberDisplay) render() string {
	name := m.Name
	if m.IsSelf {
		name += " " + styleSelf.Render("(self)")
	}
	if m.IsOrch {
		name += " " + styleSelf.Render("★")
	}
	// padding accounts for ansi escape codes — use lipgloss.Width-aware pad
	namePad := padRight(name, 22)

	osTxt := m.OS
	if osTxt == "" {
		osTxt = "—"
	}
	// Header order is: member · os · state · reason · sys · ad
	return m.Bullet + " " +
		namePad + " " +
		padRight(osTxt, 9) + " " +
		padRight(m.State, 9) + " " +
		padRight(dashInt(m.Reason), 7) + " " +
		padRight(dashInt(m.Sys), 7) + " " +
		m.Ad
}

func shortID(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:8] + "…" + s[len(s)-4:]
}

func shortenPin(p string) string {
	const cut = 16
	if len(p) <= cut+5 {
		return p
	}
	return p[:cut] + "…"
}

func dashInt(n int) string {
	if n <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d", n)
}

func dashDur(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

func fmtAgo(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
