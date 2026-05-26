// minti-mcp-shell — execute a shell command via /bin/sh -c.
//
// Highest-risk MCP surface. Policy gating layers, in order:
//  1. mcp.shell.deny_tools — global per-tool kill switch
//  2. mcp.shell.mode = deny  — refuse all
//  3. mcp.shell.mode = allowlist — first word of command must be on allowlist
//  4. mcp.shell.mode = prompt (default) — server allows; the host renders the
//     consent prompt before calling. This is the expected interactive shape.
package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
	"github.com/minti/mcp-servers/internal/proc"
)

var version = "0.1.0-M2"

const (
	defaultTimeoutSec = 30
	maxTimeoutSec     = 600
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-shell: %v", err)
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

	srv := mcpserve.New("minti-mcp-shell", version, pol, logger)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name: "exec",
		Description: "Run a shell command via /bin/sh -c with a timeout. " +
			"Returns stdout, stderr, exit code. Subject to mcp.shell.mode policy.",
	}, execShell)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

type ExecIn struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}
type ExecOut struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

func execShell(parent context.Context, in ExecIn) (ExecOut, error) {
	if in.Command == "" {
		return ExecOut{}, fmt.Errorf("command is required")
	}
	timeout := in.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultTimeoutSec
	}
	if timeout > maxTimeoutSec {
		timeout = maxTimeoutSec
	}

	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	shell, flag := shellAndFlag()
	cmd := exec.CommandContext(ctx, shell, flag, in.Command)
	stdout, stderr, exitCode, err := proc.Run(cmd)

	out := ExecOut{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}
	if ctx.Err() == context.DeadlineExceeded {
		out.TimedOut = true
		return out, nil
	}
	if err != nil && exitCode == 0 {
		// Non-exit-status error (e.g. command not found).
		return out, err
	}
	return out, nil
}

func shellAndFlag() (string, string) {
	if runtime.GOOS == "windows" {
		// Allows dev/test of the binary on Win; production target is Linux.
		return "cmd", "/C"
	}
	return "/bin/sh", "-c"
}
