//go:build linux

package main

import "syscall"

// freeDiskBytes returns the bytes available to unprivileged users on the
// filesystem containing p. Linux is the only target this binary really runs
// on (it lives in a .deb postinst); the disk_other.go stub keeps native
// development builds compiling on Windows/macOS.
func freeDiskBytes(p string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
