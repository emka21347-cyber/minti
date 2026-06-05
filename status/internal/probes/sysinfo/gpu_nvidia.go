//go:build linux || windows

package sysinfo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// readGPU shells out to nvidia-smi for a single-line summary. Returns
// empty string when nvidia-smi is missing or no GPU found. Linux + Windows
// only; macOS skips this entirely (no nvidia-smi worth the trouble).
//
// Slow probe (~50ms cold, ~10ms warm). Only called on the slow tick.
func readGPU(ctx context.Context) string {
	bin, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, bin,
		"--query-gpu=name,memory.total,memory.used",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if line == "" {
		return ""
	}
	// "NVIDIA GeForce RTX 4070, 12282, 8612"
	parts := strings.Split(line, ", ")
	if len(parts) < 3 {
		return line
	}
	name := strings.TrimSpace(parts[0])
	name = strings.TrimPrefix(name, "NVIDIA ")
	totalMiB, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	usedMiB, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	return formatGPU(name, totalMiB, usedMiB)
}

func formatGPU(name string, totalMiB, usedMiB float64) string {
	totalGiB := totalMiB / 1024
	usedGiB := usedMiB / 1024
	return name + "  " +
		strconv.FormatFloat(totalGiB, 'f', 1, 64) + " GiB" +
		" (" + strconv.FormatFloat(usedGiB, 'f', 1, 64) + " used)"
}
