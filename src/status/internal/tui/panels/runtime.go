package panels

import (
	"fmt"
	"strings"
	"time"

	"github.com/minti/status/internal/probes/runtime"
)

// Runtime renders the Runtime + VRAM panel (right column on wide).
func Runtime(st runtime.Status, vram []runtime.LoadedModel) string {
	const w = halfWidth
	const lbl = 10
	var b strings.Builder

	b.WriteString(titleBar("Runtime (minti-runtime :7780)", w) + "\n")

	healthIcon := styleBad.Render("●")
	healthTxt := "(not running)"
	if st.Healthy {
		healthIcon = styleGood.Render("●")
		ver := st.Version
		if ver == "" {
			ver = "?"
		}
		healthTxt = "healthy  " + ver
	}
	b.WriteString(row("Health", healthIcon+" "+healthTxt, lbl, w) + "\n")

	backend := st.Backend
	if backend == "" {
		backend = "(unknown)"
	}
	b.WriteString(row("Backend", backend, lbl, w) + "\n")

	// Resident: top up to 3 models by name (resident=true marked with ★).
	best := -1
	for i, m := range st.Models {
		if best < 0 || m.ReasoningScore > st.Models[best].ReasoningScore {
			best = i
		}
	}
	residentLines := []string{}
	for i, m := range st.Models {
		if i >= 4 {
			residentLines = append(residentLines, styleFaint.Render(fmt.Sprintf("    … +%d more", len(st.Models)-i)))
			break
		}
		star := "  "
		if i == best {
			star = styleSelf.Render("★ ")
		}
		score := ""
		if m.ReasoningScore > 0 {
			score = styleFaint.Render(fmt.Sprintf("   reasoning=%d", m.ReasoningScore))
		}
		residentLines = append(residentLines, "    "+star+styleValue.Render(m.Name)+score)
	}
	if len(residentLines) == 0 {
		residentLines = []string{"    " + styleFaint.Render("(no models)")}
	}
	b.WriteString(row("Resident", strings.TrimSpace(residentLines[0]), lbl, w) + "\n")
	for _, line := range residentLines[1:] {
		b.WriteString(padRight(line, w) + "\n")
	}

	// In VRAM (Ollama /api/ps).
	vramTxt := styleFaint.Render("(none loaded)")
	if len(vram) > 0 {
		first := vram[0]
		vramTxt = styleValue.Render(first.Name) + styleFaint.Render(fmt.Sprintf("   (%.1f GiB, ttl %s)",
			first.SizeGB, fmtTTL(first.TTLLeft)))
	}
	b.WriteString(row("In VRAM", vramTxt, lbl, w))

	return b.String()
}

func fmtTTL(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
