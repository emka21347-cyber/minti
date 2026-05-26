// Package proc provides shared subprocess-exec helpers for MCP servers.
package proc

import (
	"bytes"
	"errors"
	"os/exec"
)

// Run executes cmd, capturing stdout and stderr separately.
//
//   - exitCode is the process exit code; 0 on success.
//   - err is non-nil only when the binary fails to start (e.g. PATH miss).
//     A non-zero exit status from a successfully-started process is reported
//     via exitCode + (err == nil).
func Run(cmd *exec.Cmd) (stdout, stderr string, exitCode int, err error) {
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	stdout = so.String()
	stderr = se.String()
	if err == nil {
		return stdout, stderr, 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return stdout, stderr, ee.ExitCode(), nil
	}
	return stdout, stderr, 0, err
}
