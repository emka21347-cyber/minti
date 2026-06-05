package panels

import (
	"fmt"
	"strings"

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
