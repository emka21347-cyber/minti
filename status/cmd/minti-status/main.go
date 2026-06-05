// minti-status — live terminal-UI dashboard for MINTI nodes.
//
// Read-only v1 (M7). Surfaces local Clan membership, runtime + loaded LLM,
// installed addon packs, configured agent harnesses. Refreshes on three
// independent tickers (2s / 5s / 30s) so no panel ever blocks the UI.
//
// Usage:
//
//	minti-status                 # interactive TUI
//	minti-status --once          # one-shot render to stdout (for scripting / SSH non-TTY)
//	minti-status --no-color      # disable ANSI escapes (also honours NO_COLOR env)
//	minti-status --refresh=5s    # base refresh interval (default 2s)
//	minti-status --version
//
// See plan: ~/.claude/plans/we-need-to-plan-async-candle.md
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/minti/status/internal/tui"
	"github.com/minti/status/internal/version"
)

func main() {
	var (
		once       = flag.Bool("once", false, "render one snapshot to stdout and exit")
		noColor    = flag.Bool("no-color", false, "disable ANSI colors (also via NO_COLOR env)")
		refresh    = flag.Duration("refresh", 2*time.Second, "base refresh interval (fast tick)")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("minti-status", version.Version)
		return
	}

	if os.Getenv("NO_COLOR") != "" {
		*noColor = true
	}

	tui.SetBuildVersion(version.Version)

	opts := tui.Options{
		Once:    *once,
		NoColor: *noColor,
		Refresh: *refresh,
	}

	if err := tui.Run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "minti-status:", err)
		os.Exit(1)
	}
}
