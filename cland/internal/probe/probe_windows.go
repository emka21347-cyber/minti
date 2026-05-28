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

// Win32 SYSTEM_POWER_STATUS — see Microsoft docs for GetSystemPowerStatus.
// 4 bytes of byte fields (no padding before BatteryLifeTime; the struct is
// 12 bytes total on Windows x64).
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte // Reserved1 on older SDKs
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
	procGetSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")
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

// readOnBattery uses Win32 GetSystemPowerStatus (no cgo, no WMI). Implemented
// in M5-A — the M4 placeholder always-false return is gone.
//
// ACLineStatus semantics (per Microsoft docs):
//
//	0  Offline (running on battery)
//	1  Online  (running on AC)
//	255 Unknown
//
// We return true iff ACLineStatus == 0. Unknown is treated as AC (false) —
// the system_score formula prefers false-positive "on AC" to false-positive
// "on battery", because the latter penalises hosts that should be eligible
// for orchestrator. The qwen3.6 peer-review suggestion to additionally check
// BatteryFlag == 0xFF was rejected as gold-plating; ACLineStatus is the
// authoritative AC/battery bit.
func readOnBattery() bool {
	var sps systemPowerStatus
	r1, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&sps)))
	if r1 == 0 {
		return false
	}
	return sps.ACLineStatus == 0
}
