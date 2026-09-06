// Package web carries the embedded Clan Workspace frontend. The SPA is built
// into the binary so a node serves its own UI with no node/npm runtime — it
// just works on an offline, lean box.
package web

import "embed"

// FS holds the static frontend assets (index.html and, later, css/js/icons).
//
//go:embed index.html
var FS embed.FS
