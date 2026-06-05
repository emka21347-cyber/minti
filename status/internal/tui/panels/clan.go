package panels

import (
	"fmt"
	"strings"
	"time"

	"github.com/minti/status/internal/probes/clan"
)

// Clan renders the Clan panel — the headline of the dashboard. Wide row,
// full width.
func Clan(st clan.State, probeErr error) string {
	const w = fullWidth
	const lbl = 16

	var b strings.Builder
	b.WriteString(titleBar("Clan", w) + "\n")

	// Permission-denied / EACCES → graceful degradation, matches minti-fetch.
	if clan.IsPermissionDenied(probeErr) {
		msg := styleWarn.Render("(sudo for clan details)")
		b.WriteString(row("Clan ID", msg, lbl, w))
		return b.String()
	}

	if !st.Configured {
		b.WriteString(row("Clan ID", styleFaint.Render("(unaffiliated — run `minti-cland create`)"), lbl, w))
		return b.String()
	}

	id := shortID(st.ClanID)
	header := styleValue.Render(id)
	if st.Role != "" {
		header += "    " + styleFaint.Render("role=") + styleValue.Render(st.Role)
	}
	if st.Pin != "" {
		header += "    " + styleFaint.Render("pin=") + styleValue.Render(shortenPin(st.Pin))
	}
	b.WriteString(row("Clan ID", header, lbl, w) + "\n")

	orchLine := styleValue.Render(ifEmpty(st.Orchestrator, "(no orchestrator yet)"))
	if st.IsSelfOrch {
		orchLine = styleSelf.Render(ifEmpty(st.Orchestrator, "(self)") + " ★")
	}
	if st.Term > 0 {
		orchLine += "    " + styleFaint.Render("term=") + styleValue.Render(fmt.Sprintf("%d", st.Term))
	}
	if st.LeaseLeft > 0 {
		orchLine += "    " + styleFaint.Render("lease ") + styleValue.Render(fmtTTL(st.LeaseLeft))
	}
	b.WriteString(row("Orchestrator", orchLine, lbl, w) + "\n")

	// Members table — minimal columns for v1.
	header2 := fmt.Sprintf("%-1s %-30s %-9s %-9s %-9s %s",
		" ", "member", "state", "reason", "sys", "ad")
	b.WriteString(padRight("  "+styleFaint.Render(fmt.Sprintf("Members (%d)", len(st.Members))), lbl+2) +
		styleFaint.Render(header2) + "\n")
	for _, m := range st.Members {
		bullet := styleFaint.Render("○") // dim
		if m.LastAd > 0 && m.LastAd <= 30*time.Second && m.State == "active" {
			bullet = styleGood.Render("●")
		}
		name := m.DisplayName
		if name == "" {
			name = shortID(m.MemberID)
		}
		if m.IsOrchestrator {
			name = styleSelf.Render(name + " ★")
		}
		reason := dash(m.ReasoningScore)
		sys := dash(m.SystemScore)
		adTxt := dashDur(m.LastAd)

		line := fmt.Sprintf("%s %-30s %-9s %-9s %-9s %s",
			bullet,
			padRight(name, 30),
			padRight(m.State, 9),
			padRight(reason, 9),
			padRight(sys, 9),
			adTxt,
		)
		b.WriteString(padRight("  "+strings.Repeat(" ", lbl)+line, w) + "\n")
	}

	if len(st.RecentElections) > 0 {
		b.WriteString(row("Last elections", "", lbl, w) + "\n")
		for _, e := range st.RecentElections {
			ago := time.Since(e.At)
			line := fmt.Sprintf("term %d  ← %s   reason=%s   %s ago",
				e.Term, e.Winner, e.Reason, fmtAgo(ago))
			b.WriteString(padRight("  "+strings.Repeat(" ", lbl)+styleFaint.Render(line), w) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
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

func dash(n int) string {
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
	return fmt.Sprintf("%dm ago", int(d.Minutes()))
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
