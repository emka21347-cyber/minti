package panels

import (
	"fmt"
	"strings"
	"time"

	"github.com/minti/status/internal/probes/sysinfo"
)

// System renders the System panel (left column on wide).
func System(s sysinfo.Info) string {
	const w = halfWidth
	const lbl = 8
	var b strings.Builder
	b.WriteString(titleBar("System", w) + "\n")
	b.WriteString(row("OS", ifEmpty(s.OSPretty, "(unknown)"), lbl, w) + "\n")
	kernel := s.Kernel
	if s.Arch != "" {
		kernel = strings.TrimSpace(kernel + "  " + s.Arch)
	}
	b.WriteString(row("Kernel", ifEmpty(kernel, "(n/a)"), lbl, w) + "\n")
	b.WriteString(row("Uptime", fmtUptime(s.Uptime), lbl, w) + "\n")
	cpu := s.CPUModel
	if cpu == "" {
		cpu = "(n/a)"
	}
	if s.CPUCores > 0 {
		cpu = fmt.Sprintf("%s  %dt", cpu, s.CPUCores)
	}
	if s.Load1 > 0 {
		cpu = fmt.Sprintf("%s   load %.2f", cpu, s.Load1)
	}
	b.WriteString(row("CPU", cpu, lbl, w) + "\n")
	b.WriteString(row("GPU", ifEmpty(s.GPU, "(no nvidia GPU)"), lbl, w) + "\n")
	ram := fmt.Sprintf("%.1f / %.1f GiB", s.RAMUsedGB, s.RAMTotalGB)
	if s.SwapTotalGB > 0 {
		ram += fmt.Sprintf("   swap %.1f / %.1f", s.SwapUsedGB, s.SwapTotalGB)
	}
	b.WriteString(row("RAM", ram, lbl, w))
	return b.String()
}

// fmtUptime renders a duration as "Xd Yh Zm" (matches `uptime -p` style
// but tighter — no commas or "up" prefix). Sub-minute durations are
// shown as seconds so the post-boot transition is observable.
func fmtUptime(d time.Duration) string {
	if d <= 0 {
		return "(n/a)"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
