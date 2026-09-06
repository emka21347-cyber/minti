//go:build !linux

package main

import "errors"

// freeDiskBytes stub: native development builds on Windows/macOS don't have a
// good cross-platform answer (Windows GetDiskFreeSpaceEx works but pulls in
// golang.org/x/sys/windows for no win — we don't run there in production).
// Returns an error so the caller treats the precheck as "unknown" rather
// than zero/false-negative.
func freeDiskBytes(p string) (int64, error) {
	return 0, errors.New("freeDiskBytes not implemented on non-linux (build for linux)")
}
