package panels

import (
	"testing"
	"time"

	"github.com/minti/status/internal/probes/addons"
	"github.com/minti/status/internal/probes/harness"
	"github.com/minti/status/internal/probes/sysinfo"
)

func TestSystem_Linux(t *testing.T) {
	got := System(sysinfo.Info{
		Hostname:    "node-3",
		User:        "alice",
		OSPretty:    "Debian GNU/Linux 13 (trixie)",
		Kernel:      "6.1.0-21-amd64",
		Arch:        "x86_64",
		Load1:       0.62,
		RAMUsedGB:   12.3,
		RAMTotalGB:  32.0,
		SwapUsedGB:  0.0,
		SwapTotalGB: 8.0,
		Uptime:      4*time.Hour + 12*time.Minute,
		CPUModel:    "AMD Ryzen 7 5700X",
		CPUCores:    16,
		GPU:         "GeForce RTX 4070  12.0 GiB (8.4 used)",
	})
	assertGolden(t, "system_linux", got)
}

func TestSystem_Windows(t *testing.T) {
	got := System(sysinfo.Info{
		Hostname:   "ami",
		User:       "aouad",
		OSPretty:   "Windows 11 (build 26200)",
		Kernel:     "",
		Arch:       "amd64",
		Uptime:     1*time.Hour + 54*time.Minute,
		CPUModel:   "AMD Ryzen 9 9950X3D 16-Core Processor",
		CPUCores:   32,
		GPU:        "GeForce RTX 5090  31.8 GiB (1.2 used)",
		RAMUsedGB:  23.8,
		RAMTotalGB: 61.7,
	})
	assertGolden(t, "system_windows", got)
}

func TestSystem_Darwin(t *testing.T) {
	got := System(sysinfo.Info{
		Hostname:   "mbp",
		User:       "alice",
		OSPretty:   "macOS 14.5",
		Arch:       "arm64",
		Uptime:     3*24*time.Hour + 7*time.Hour,
		CPUModel:   "Apple M2 Pro",
		CPUCores:   12,
		RAMTotalGB: 32.0,
	})
	assertGolden(t, "system_darwin", got)
}

func TestSystem_NoUptime(t *testing.T) {
	got := System(sysinfo.Info{
		Hostname:   "node-x",
		OSPretty:   "Linux Mint 22.3",
		Arch:       "x86_64",
		CPUModel:   "Intel Core i5",
		CPUCores:   4,
		RAMUsedGB:  2.1,
		RAMTotalGB: 8.0,
		// no Uptime set — render "(n/a)"
	})
	assertGolden(t, "system_no_uptime", got)
}

func TestAddons_Empty(t *testing.T) {
	got := Addons(nil)
	assertGolden(t, "addons_empty", got)
}

func TestAddons_Populated(t *testing.T) {
	got := Addons([]addons.Pack{
		{Name: "hermes3", Kind: "ollama-model", At: refTime.Add(-1 * time.Hour)},
		{Name: "mistral", Kind: "ollama-model", At: refTime.Add(-2 * time.Hour)},
		{Name: "wiki-simple", Kind: "kiwix-zim", At: refTime.Add(-3 * time.Hour)},
	})
	assertGolden(t, "addons_populated", got)
}

func TestHarness_None(t *testing.T) {
	got := Harness(harness.OpencodeConfig{}, harness.ClaudeConfig{})
	assertGolden(t, "harness_none", got)
}

func TestHarness_OpencodeFull(t *testing.T) {
	oc := harness.OpencodeConfig{
		Configured:   true,
		Provider:     "minti-runtime",
		DefaultModel: "hermes3:8b",
		MCPNames:     []string{"fs", "http", "pkg", "recon", "shell", "wiki"},
	}
	cc := harness.ClaudeConfig{Configured: true, Path: "/home/alice/.claude/settings.json"}
	got := Harness(oc, cc)
	assertGolden(t, "harness_opencode_full", got)
}

func TestHeader_Basic(t *testing.T) {
	got := Header(HeaderData{
		Hostname:   "node-3",
		User:       "alice",
		Now:        refTime,
		MintiVer:   "0.1.0-M3",
		StatusVer:  "0.3.0-M7.1",
		RefreshDur: 2 * time.Second,
		LastTick:   refTime,
		Width:      100,
	})
	assertGolden(t, "header_basic", got)
}

func TestFooter_NoErr(t *testing.T) {
	got := Footer(FooterData{Width: 100})
	assertGolden(t, "footer_no_err", got)
}

func TestFooter_WithErr(t *testing.T) {
	got := Footer(FooterData{
		Width:   100,
		LastErr: "clan: permission denied (sudo for clan details)",
	})
	assertGolden(t, "footer_with_err", got)
}
