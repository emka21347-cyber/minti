package panels

import (
	"strings"

	"github.com/minti/status/internal/probes/harness"
)

// Harness renders the Harness panel (bottom-right on wide).
func Harness(oc harness.OpencodeConfig, cc harness.ClaudeConfig) string {
	const w = halfWidth
	const lbl = 10
	var b strings.Builder
	b.WriteString(titleBar("Harness (opencode + claude)", w) + "\n")

	if oc.Configured {
		val := styleValue.Render(ifEmpty(oc.Provider, "(unknown)"))
		if oc.DefaultModel != "" {
			val += "   " + styleFaint.Render("model=") + styleValue.Render(oc.DefaultModel)
		}
		b.WriteString(row("opencode", val, lbl, w) + "\n")
		if len(oc.MCPNames) > 0 {
			b.WriteString(row("MCP", strings.Join(oc.MCPNames, " · "), lbl, w) + "\n")
		}
	} else {
		b.WriteString(row("opencode", styleFaint.Render("(not configured)"), lbl, w) + "\n")
	}

	if cc.Configured {
		b.WriteString(row("claude", styleValue.Render("preset present"), lbl, w))
	} else {
		b.WriteString(row("claude", styleFaint.Render("(not configured)"), lbl, w))
	}

	return strings.TrimRight(b.String(), "\n")
}
