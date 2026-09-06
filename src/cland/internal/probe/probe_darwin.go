//go:build darwin

// macOS hardware probe. Mirrors probe_linux.go's three readers.
//
// RAM + uptime via syscall.SysctlRaw with explicit binary.LittleEndian
// decode (per M5 peer-review item 6 — macOS is little-endian on amd64 +
// arm64, but explicit LE beats relying on unsafe-pointer struct alignment).
//
// VRAM stays 0 here: the M5 macOS target is the old-Mac-resurrection story
// (PRD P2), 10-yr-old hardware with integrated graphics. The cross-platform
// readNvidiaSMI() in probe.go is already a no-op on macOS (no nvidia-smi
// binary), so nothing else to do.

package probe

import (
	"context"
	"encoding/binary"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

func readRAMGB() float64 {
	buf, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(buf) < 8 {
		return 0
	}
	bytesTotal := binary.LittleEndian.Uint64([]byte(buf)[:8])
	return float64(bytesTotal) / 1024.0 / 1024.0 / 1024.0
}

func readUptime24h() float64 {
	// kern.boottime is a `struct timeval { time_t tv_sec; suseconds_t tv_usec }`.
	// On all current macOS arches (amd64 + arm64) both fields are 8 bytes
	// little-endian, so the raw buffer is 16 bytes total.
	buf, err := syscall.Sysctl("kern.boottime")
	if err != nil || len(buf) < 16 {
		return 0
	}
	b := []byte(buf)
	sec := int64(binary.LittleEndian.Uint64(b[:8]))
	usec := int64(binary.LittleEndian.Uint64(b[8:16]))
	boot := time.Unix(sec, usec*1000)
	frac := time.Since(boot).Hours() / 24.0
	if frac > 1 {
		return 1
	}
	if frac < 0 {
		return 0
	}
	return frac
}

// hasBatteryCache caches whether the system has a battery so we don't
// fork-exec pmset every 30s on desktop Macs (Mac mini, iMac, Studio, Pro).
// Determined on first call; never re-evaluated.
var (
	hasBatteryOnce sync.Once
	hasBattery     bool
)

func readOnBattery() bool {
	hasBatteryOnce.Do(func() {
		hasBattery = detectHasBattery()
	})
	if !hasBattery {
		return false
	}

	out, err := pmsetBatt()
	if err != nil {
		// timeout / binary missing — don't cache a "no battery" verdict from
		// a transient failure; just return false this cycle.
		return false
	}
	return strings.Contains(out, "discharging;")
}

// detectHasBattery runs `pmset -g batt` once and looks for sentinel strings
// that indicate the host has no battery. Anything else → assume battery
// present (and let the per-call discharging check do the real work).
func detectHasBattery() bool {
	out, err := pmsetBatt()
	if err != nil {
		// pmset missing or permission-denied. Safe default: assume no
		// battery (so future calls skip the fork-exec).
		return false
	}
	if strings.Contains(out, "No batteries") ||
		strings.Contains(out, "no batteries") ||
		strings.Contains(out, "not present") {
		return false
	}
	// If pmset ran but output is unparseable / empty, conservatively
	// disable to avoid burning fork-execs every 30s on a system we can't
	// read anyway.
	if strings.TrimSpace(out) == "" {
		return false
	}
	return true
}

// pmsetBatt runs `/usr/bin/pmset -g batt` with a tight timeout and returns
// stdout. Used for both initial hasBattery detection and per-cycle
// discharging check.
func pmsetBatt() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/pmset", "-g", "batt")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
