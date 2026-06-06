package panels

import (
	"fmt"
	"strings"
	"time"

	"github.com/minti/status/internal/probes/clan"
)

// Invite renders the copy-paste join command when the user has minted
// an invite via the `i` keybind. Returns "" when no invite is active
// (so layout can naturally skip the section without leaving a gap).
//
// The `now` parameter keeps the rendered "expires in" countdown
// deterministic for goldens.
func Invite(inv *clan.Invite, now time.Time) string {
	if inv == nil {
		return ""
	}
	const w = fullWidth
	const lbl = 16

	remaining := inv.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return ""
	}

	title := fmt.Sprintf("Invite (5m TTL · expires in %s)", fmtTTL(remaining.Round(time.Second)))
	var b strings.Builder
	b.WriteString(titleBar(title, w) + "\n")

	b.WriteString(row("Copy + run on", styleFaint.Render("the joining node:"), lbl, w) + "\n")

	// Render the join command on its own line. No surrounding chrome on
	// the data line so triple-click in modern terminals selects the
	// whole command cleanly.
	cmdLine := "  " + styleValue.Render(inv.JoinCommand)
	b.WriteString(padRight(cmdLine, w))

	return b.String()
}
