//go:build windows

package probe

import (
	"syscall"
	"unsafe"
)

// Win32 GlobalMemoryStatusEx structure (per Microsoft docs).
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

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
)

func readRAMGB() float64 {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r1, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return 0
	}
	return float64(ms.TotalPhys) / 1024.0 / 1024.0 / 1024.0 // bytes → GB
}

func readUptime24h() float64 {
	// GetTickCount64 returns milliseconds since system start.
	r1, _, _ := procGetTickCount64.Call()
	ms := uint64(r1)
	sec := float64(ms) / 1000.0
	frac := sec / (24 * 3600)
	if frac > 1 {
		return 1
	}
	if frac < 0 {
		return 0
	}
	return frac
}

// readOnBattery — documented limitation per Phase D plan + PRD §3a non-goals.
// Windows is a Clan-Agent target in v1, not a full-member target; proper
// WMI battery detection is M5 work. We always return false here so the
// system_score formula doesn't randomly penalise Windows hosts.
func readOnBattery() bool {
	return false
}
