// Package version holds the embedded build version, set via -ldflags.
package version

// Version is overridden at build time via:
//
//	go build -ldflags "-X github.com/minti/status/internal/version.Version=0.1.0-M7"
var Version = "dev"
