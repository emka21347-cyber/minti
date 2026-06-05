//go:build darwin

package sysinfo

import (
	"context"
	"os"
	"os/user"
	"runtime"

	"golang.org/x/sys/unix"
)

// ProbeCheap on macOS: hostname + user + arch + RAM via sysctl. Day-1
// stub; expands in a follow-on (uptime, CPU model from machdep.cpu.brand_string).
func ProbeCheap(ctx context.Context) (Info, error) {
	var i Info
	hn, _ := os.Hostname()
	i.Hostname = hn
	if u, err := user.Current(); err == nil {
		i.User = u.Username
	}
	i.OSPretty = "macOS"
	i.Arch = runtime.GOARCH

	if v, err := unix.SysctlUint64("hw.memsize"); err == nil {
		i.RAMTotalGB = bytesToGB(v)
	}
	return i, nil
}

func readCPU() (string, int) {
	model, _ := unix.Sysctl("machdep.cpu.brand_string")
	return model, runtime.NumCPU()
}

// readGPU stub for darwin — nvidia-smi essentially never exists on macOS
// in practice; we leave the slot empty.
func readGPU(ctx context.Context) string { return "" }

func bytesToGB(b uint64) float64 {
	return float64(b) / 1024 / 1024 / 1024
}
