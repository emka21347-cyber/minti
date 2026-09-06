//go:build linux

package sysinfo

import (
	"bufio"
	"context"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProbeCheap reads only the fast sources (no fork/exec, no >5ms paths).
// Safe to run on the medium tick (every 5s).
func ProbeCheap(ctx context.Context) (Info, error) {
	var i Info

	hn, _ := os.Hostname()
	i.Hostname = hn

	if u, err := user.Current(); err == nil {
		i.User = u.Username
	}

	i.OSPretty = readOSPretty()
	i.MintiVersion = readMintiVersion()

	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err == nil {
		i.Kernel = byteArrayToString(uts.Release[:])
		i.Arch = byteArrayToString(uts.Machine[:])
	}

	if la, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(la))
		if len(fields) > 0 {
			i.Load1, _ = strconv.ParseFloat(fields[0], 64)
		}
	}

	// /proc/uptime: first field is seconds since boot (float, with
	// hundredths-of-second precision). Round to seconds for display.
	if ub, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(ub))
		if len(fields) > 0 {
			if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				i.Uptime = time.Duration(secs * float64(time.Second)).Round(time.Second)
			}
		}
	}

	used, total, swapUsed, swapTotal := readMeminfo()
	i.RAMUsedGB = bytesToGB(used)
	i.RAMTotalGB = bytesToGB(total)
	i.SwapUsedGB = bytesToGB(swapUsed)
	i.SwapTotalGB = bytesToGB(swapTotal)

	return i, nil
}

// readCPU returns model name + logical core count from /proc/cpuinfo.
func readCPU() (string, int) {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", 0
	}
	defer f.Close()

	model := ""
	cores := 0
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if model == "" && strings.HasPrefix(line, "model name") {
			if idx := strings.Index(line, ":"); idx > 0 {
				model = strings.TrimSpace(line[idx+1:])
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}
	return model, cores
}

// readOSPretty reads PRETTY_NAME from /etc/os-release.
func readOSPretty() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			v := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func readMintiVersion() string {
	b, err := os.ReadFile("/etc/minti/version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readMeminfo: returns (memUsedBytes, memTotalBytes, swapUsedBytes, swapTotalBytes).
// Lines look like "MemTotal:        8123456 kB" → bytes.
func readMeminfo() (used, total, swapUsed, swapTotal uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var memTotal, memAvailable, memFree, swapTotalKB, swapFreeKB uint64
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := line[:colon]
		valStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[colon+1:]), " kB"))
		val, _ := strconv.ParseUint(valStr, 10, 64)
		switch key {
		case "MemTotal":
			memTotal = val
		case "MemAvailable":
			memAvailable = val
		case "MemFree":
			memFree = val
		case "SwapTotal":
			swapTotalKB = val
		case "SwapFree":
			swapFreeKB = val
		}
	}

	// Prefer MemAvailable (kernel ≥ 3.14) over MemFree.
	avail := memAvailable
	if avail == 0 {
		avail = memFree
	}
	total = memTotal * 1024
	used = (memTotal - avail) * 1024
	swapTotal = swapTotalKB * 1024
	swapUsed = (swapTotalKB - swapFreeKB) * 1024
	return
}

func bytesToGB(b uint64) float64 {
	return float64(b) / 1024 / 1024 / 1024
}

// byteArrayToString converts a kernel-style int8 char array (NUL-padded)
// to a Go string. syscall.Utsname fields are [65]int8 on Linux.
func byteArrayToString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
