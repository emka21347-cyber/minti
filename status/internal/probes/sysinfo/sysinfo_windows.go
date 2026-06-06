//go:build windows

package sysinfo

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// ProbeCheap reads hostname, user, RAM, uptime, OS version, all via
// native Windows APIs (no PowerShell shell-outs — every call is direct
// syscall into kernel32.dll / ntdll.dll). CPU model is read from the
// registry. Safe to run every medium tick (<10 ms total).
func ProbeCheap(ctx context.Context) (Info, error) {
	var i Info
	hn, _ := os.Hostname()
	i.Hostname = hn
	if u, err := user.Current(); err == nil {
		// Windows reports DOMAIN\username; trim the domain for compact display.
		name := u.Username
		if idx := lastIndex(name, "\\"); idx >= 0 {
			name = name[idx+1:]
		}
		i.User = name
	}
	i.OSPretty = readOSVersion()
	i.Arch = runtime.GOARCH

	total, avail := globalMemoryStatusEx()
	if total > 0 {
		i.RAMTotalGB = bytesToGB(total)
		i.RAMUsedGB = bytesToGB(total - avail)
	}

	if d := getTickCount64(); d > 0 {
		i.Uptime = d.Round(time.Second)
	}

	return i, nil
}

// readCPU pulls the processor brand string from the Windows registry —
// the same field Task Manager surfaces under "Performance > CPU".
//
//	HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0\ProcessorNameString
//
// On failure, falls back to a GOARCH placeholder so the panel doesn't
// render an empty cell.
func readCPU() (string, int) {
	cores := runtime.NumCPU()
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return runtime.GOARCH + " (Windows)", cores
	}
	defer key.Close()
	name, _, err := key.GetStringValue("ProcessorNameString")
	if err != nil || name == "" {
		return runtime.GOARCH + " (Windows)", cores
	}
	// Trim trailing NULs / whitespace (registry values often have them).
	return trimNulSpace(name), cores
}

// readOSVersion calls ntdll.RtlGetVersion — the only modern way to get
// a real Windows version (GetVersionEx lies for compatibility post-8.1
// unless the binary embeds a manifest).
func readOSVersion() string {
	type rtlOsVersionInfoEx struct {
		OSVersionInfoSize uint32
		MajorVersion      uint32
		MinorVersion      uint32
		BuildNumber       uint32
		PlatformID        uint32
		CSDVersion        [128]uint16
	}
	ntdll := syscall.NewLazyDLL("ntdll.dll")
	proc := ntdll.NewProc("RtlGetVersion")
	var info rtlOsVersionInfoEx
	info.OSVersionInfoSize = uint32(unsafe.Sizeof(info))
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&info)))
	if r != 0 {
		return "Windows"
	}
	// Marketing name: Win10 + build ≥ 22000 = "Windows 11".
	name := "Windows"
	switch {
	case info.MajorVersion == 10 && info.BuildNumber >= 22000:
		name = "Windows 11"
	case info.MajorVersion == 10:
		name = "Windows 10"
	case info.MajorVersion == 6 && info.MinorVersion == 3:
		name = "Windows 8.1"
	case info.MajorVersion == 6 && info.MinorVersion == 2:
		name = "Windows 8"
	case info.MajorVersion == 6 && info.MinorVersion == 1:
		name = "Windows 7"
	}
	return fmt.Sprintf("%s (build %d)", name, info.BuildNumber)
}

// getTickCount64 returns the time the system has been running since
// boot. kernel32!GetTickCount64 (Vista+).
func getTickCount64() time.Duration {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	proc := k32.NewProc("GetTickCount64")
	r, _, _ := proc.Call()
	if r == 0 {
		return 0
	}
	return time.Duration(uint64(r)) * time.Millisecond
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

// trimNulSpace strips trailing NUL + whitespace bytes. Registry strings
// from kernel APIs sometimes contain a literal NUL beyond the string
// data — golang.org/x/sys/windows/registry handles most cases, this is
// belt-and-braces.
func trimNulSpace(s string) string {
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == 0 || c == ' ' || c == '\t' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

func lastIndex(s, sep string) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
