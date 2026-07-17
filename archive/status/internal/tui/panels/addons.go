package panels

import (
	"strings"

	"github.com/minti/status/internal/probes/addons"
)

// Addons renders the Addons panel (bottom-left on wide).
func Addons(packs []addons.Pack) string {
	const w = halfWidth
	var b strings.Builder
	b.WriteString(titleBar("Addons (/var/lib/minti/packs)", w) + "\n")

	if len(packs) == 0 {
		b.WriteString(padRight("  "+styleFaint.Render("(no addon packs installed)"), w))
		return b.String()
	}

	for _, p := range packs {
		check := styleGood.Render("✓")
		name := styleValue.Render(p.Name)
		kind := ""
		if p.Kind != "" {
			kind = "   " + styleFaint.Render("("+p.Kind+")")
		}
		line := "  " + check + " " + name + kind
		b.WriteString(padRight(line, w) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
