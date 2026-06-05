// Package sysinfo reads host system information for the dashboard.
//
// Cheap fields (load, free RAM) refresh on the medium tick; expensive
// fields (CPU model, nvidia-smi GPU query) refresh on the slow tick. Both
// land in the same Info struct so View() doesn't care which tick filled
// which field.
package sysinfo

import (
	"context"
)

// Info is the full system snapshot rendered by the System panel.
type Info struct {
	// Always populated (cheap).
	Hostname    string
	User        string
	OSPretty    string // e.g. "Debian GNU/Linux 13 (trixie)"
	Kernel      string // e.g. "6.1.0-21-amd64"
	Arch        string // GOARCH normalized (amd64, arm64)
	Load1       float64
	RAMUsedGB   float64
	RAMTotalGB  float64
	SwapUsedGB  float64
	SwapTotalGB float64

	// Refreshed on slow tick.
	CPUModel string
	CPUCores int
	GPU      string // "RTX 4070  12 GiB" or "(no nvidia GPU)" or ""

	// MINTI version from /etc/minti/version if present.
	MintiVersion string
}

// Probe runs both cheap + expensive sources. Used for --once and at
// startup. Use ProbeCheap on the medium tick to avoid re-running
// nvidia-smi every 5s.
func Probe(ctx context.Context) (Info, error) {
	info, err := ProbeCheap(ctx)
	if err != nil {
		return info, err
	}
	info.CPUModel, info.CPUCores = readCPU()
	info.GPU = readGPU(ctx)
	return info, nil
}
