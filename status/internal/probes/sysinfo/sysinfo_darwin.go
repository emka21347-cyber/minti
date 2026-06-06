//go:build darwin

package sysinfo

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ProbeCheap on macOS: hostname, user, RAM, uptime, OS pretty (10.x vs
// 11+ semantic version via sw_vers — kept light, fork-execs once per
// medium tick). CPU model is fetched on the slow tick via readCPU().
func ProbeCheap(ctx context.Context) (Info, error) {
	var i Info
	hn, _ := os.Hostname()
	i.Hostname = hn
	if u, err := user.Current(); err == nil {
		i.User = u.Username
	}
	i.OSPretty = readOSPretty(ctx)
	i.Arch = runtime.GOARCH

	if v, err := unix.SysctlUint64("hw.memsize"); err == nil {
		i.RAMTotalGB = bytesToGB(v)
	}
	// macOS doesn't expose "available" RAM via sysctl in a useful way
	// (it has paging, memory compression, file cache); we leave RAMUsedGB
	// at zero rather than computing a misleading number. A vm_stat fork
	// would land it; deferred to M7.5+ when the panel cares.

	i.Uptime = readUptime()

	return i, nil
}

// readCPU returns the marketing brand string + logical core count.
// machdep.cpu.brand_string is available on both Intel + Apple Silicon
// (on AS it returns "Apple M2 Pro" etc.).
func readCPU() (string, int) {
	cores := runtime.NumCPU()
	model, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil || model == "" {
		return fmt.Sprintf("macOS %s", runtime.GOARCH), cores
	}
	return strings.TrimSpace(model), cores
}

// readGPU stub — nvidia-smi essentially never exists on macOS.
func readGPU(ctx context.Context) string { return "" }

// readOSPretty: prefer the marketing version from sw_vers (e.g.
// "macOS 14.5"); fall back to "macOS <kern.osrelease>" if sw_vers
// isn't available (rare).
func readOSPretty(ctx context.Context) string {
	if out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output(); err == nil {
		return "macOS " + strings.TrimSpace(string(out))
	}
	if rel, err := unix.Sysctl("kern.osrelease"); err == nil {
		return "macOS Darwin " + rel
	}
	return "macOS"
}

// readUptime decodes kern.boottime, a 16-byte struct timeval (uint64
// seconds + uint64 microseconds on 64-bit Darwin), and subtracts from
// now. Matches the value `uptime` shells out to print.
func readUptime() time.Duration {
	raw, err := unix.SysctlRaw("kern.boottime")
	if err != nil || len(raw) < 8 {
		return 0
	}
	// struct timeval { time_t tv_sec; suseconds_t tv_usec; } —
	// on Darwin (both Intel + arm64), 16 bytes laid out little-endian.
	boot := binary.LittleEndian.Uint64(raw[:8])
	if boot == 0 {
		return 0
	}
	now := uint64(unixNow())
	if now <= boot {
		return 0
	}
	return time.Duration(now-boot) * time.Second
}

// unixNow returns time.Now().Unix() but as a function so the deterministic
// test layer (if we ever add one for macOS) can stub it.
var unixNow = func() int64 {
	return time.Now().Unix()
}

func bytesToGB(b uint64) float64 {
	return float64(b) / 1024 / 1024 / 1024
}
