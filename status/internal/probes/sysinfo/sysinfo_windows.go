//go:build windows

package sysinfo

import (
	"context"
	"os"
	"os/user"
	"runtime"
	"syscall"
	"unsafe"
)

// Day-1 Windows stub for the cross-platform requirement. Hostname, user,
// arch, RAM (via direct syscall to kernel32!GlobalMemoryStatusEx to avoid
// the golang.org/x/sys/windows binding-version dance). Fleshes out in a
// follow-on commit (OS version via RtlGetVersion, per-CPU load).
func ProbeCheap(ctx context.Context) (Info, error) {
	var i Info
	hn, _ := os.Hostname()
	i.Hostname = hn
	if u, err := user.Current(); err == nil {
		i.User = u.Username
	}
	i.OSPretty = "Windows"
	i.Arch = runtime.GOARCH

	total, avail := globalMemoryStatusEx()
	if total > 0 {
		i.RAMTotalGB = bytesToGB(total)
		i.RAMUsedGB = bytesToGB(total - avail)
	}
	return i, nil
}

func readCPU() (string, int) {
	return runtime.GOARCH + " (Windows)", runtime.NumCPU()
}

// MEMORYSTATUSEX as documented in winnt.h (64 bytes on 64-bit Windows).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func globalMemoryStatusEx() (total, avail uint64) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return 0, 0
	}
	return m.TotalPhys, m.AvailPhys
}

func bytesToGB(b uint64) float64 {
	return float64(b) / 1024 / 1024 / 1024
}
