// minti-mcp-pkg — apt-get wrapper.
//
// `install` requires either real root or NOPASSWD sudo for apt-get; otherwise
// it returns the underlying sudo error to the agent. Package names are
// validated with a strict regex to keep exec.Command's argv path injection-free.
package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
	"github.com/minti/mcp-servers/internal/proc"
)

var version = "0.1.0-M2"

// Debian package names: per Debian Policy 5.6.7, [a-z0-9][a-z0-9+\-.]+.
// Be slightly more permissive on uppercase for search queries.
var pkgNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.\-]*$`)

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-pkg: %v", err)
	}
}

func run() error {
	pol, err := policy.Load()
	if err != nil {
		return err
	}
	logger, err := audit.Default()
	if err != nil {
		return err
	}

	srv := mcpserve.New("minti-mcp-pkg", version, pol, logger)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "search",
		Description: "Search apt-cache for packages matching a query. Returns name + summary per match.",
	}, search)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "install",
		Description: "Install a Debian package via `sudo apt-get install -y <pkg>`. Requires NOPASSWD sudo for apt-get.",
	}, install)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

type SearchIn struct {
	Query string `json:"query"`
}
type SearchMatch struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}
type SearchOut struct {
	Matches []SearchMatch `json:"matches"`
}

func search(ctx context.Context, in SearchIn) (SearchOut, error) {
	if !pkgNameRe.MatchString(in.Query) {
		return SearchOut{}, fmt.Errorf("invalid query: %q", in.Query)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "apt-cache", "search", in.Query)
	stdout, stderr, exitCode, err := proc.Run(cmd)
	if err != nil && exitCode == 0 {
		return SearchOut{}, fmt.Errorf("apt-cache: %w; stderr=%s", err, strings.TrimSpace(stderr))
	}
	if exitCode != 0 {
		return SearchOut{}, fmt.Errorf("apt-cache exited %d: %s", exitCode, strings.TrimSpace(stderr))
	}
	matches := parseSearch(stdout)
	return SearchOut{Matches: matches}, nil
}

type InstallIn struct {
	Package string `json:"package"`
}
type InstallOut struct {
	Installed bool   `json:"installed"`
	Output    string `json:"output"`
}

func install(ctx context.Context, in InstallIn) (InstallOut, error) {
	if !pkgNameRe.MatchString(in.Package) {
		return InstallOut{}, fmt.Errorf("invalid package: %q", in.Package)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sudo", "-n", "apt-get", "install", "-y", in.Package)
	cmd.Env = append(cmd.Environ(), "DEBIAN_FRONTEND=noninteractive")
	stdout, stderr, exitCode, err := proc.Run(cmd)
	out := strings.TrimSpace(stdout + "\n" + stderr)
	if err != nil && exitCode == 0 {
		return InstallOut{Output: out}, fmt.Errorf("sudo apt-get: %w", err)
	}
	if exitCode != 0 {
		return InstallOut{Output: out}, fmt.Errorf("apt-get install exited %d", exitCode)
	}
	return InstallOut{Installed: true, Output: out}, nil
}

func parseSearch(raw string) []SearchMatch {
	var out []SearchMatch
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "<name> - <summary>"
		idx := strings.Index(line, " - ")
		if idx < 0 {
			out = append(out, SearchMatch{Name: line})
			continue
		}
		out = append(out, SearchMatch{
			Name:    line[:idx],
			Summary: line[idx+3:],
		})
	}
	return out
}
