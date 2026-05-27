//go:build linux

package probe

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readRAMGB() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		// Format: "MemTotal:       65945248 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return float64(kb) / 1024.0 / 1024.0 // kB → MB → GB
	}
	return 0
}

func readUptime24h() float64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	// First whitespace-separated token: uptime in seconds.
	tok := strings.Fields(strings.TrimSpace(string(data)))
	if len(tok) == 0 {
		return 0
	}
	sec, err := strconv.ParseFloat(tok[0], 64)
	if err != nil {
		return 0
	}
	frac := sec / (24 * 3600)
	if frac > 1 {
		return 1
	}
	if frac < 0 {
		return 0
	}
	return frac
}

func readOnBattery() bool {
	// Walk /sys/class/power_supply/BAT*; if any battery is "Discharging" we
	// consider the host on battery. Hosts without a battery have no BAT* dir
	// and return false naturally.
	matches, err := filepath.Glob("/sys/class/power_supply/BAT*/status")
	if err != nil || len(matches) == 0 {
		return false
	}
	for _, p := range matches {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "Discharging" {
			return true
		}
	}
	return false
}
