package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/minti/cland/internal/toolexec"
)

// knownNamespaces are the MCP servers the agent surfaces tools from. The catalog
// tolerates a namespace whose binary is absent (it logs and skips), so a server
// can be listed here before it ships — e.g. mcp-search lands in M1 S2.
var knownNamespaces = []string{
	"mcp-fs",
	"mcp-http",
	"mcp-pkg",
	"mcp-recon",
	"mcp-search",
	"mcp-shell",
	"mcp-wiki",
}

// schemaLister is the toolexec capability the catalog needs (satisfied by
// *toolexec.Executor). Decoupled so tests can inject a fake without spawning
// real MCP subprocesses.
type schemaLister interface {
	ListTools(ctx context.Context, namespace string) ([]toolexec.ToolSchema, error)
}

// ToolDef is a single model-facing tool definition. Name is the BARE tool name
// the model sees and emits (e.g. "read_text") — many model APIs disallow the
// dot in our wire form "mcp-fs.read_text". The catalog maps bare → wire.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Catalog is the immutable set of tools offered to the model for one agent
// session, plus the bare-name → wire-name map the loop uses to classify and
// execute each call.
type Catalog struct {
	tools      []ToolDef
	wireByName map[string]string // bare tool name → "namespace.tool"
}

// BuildCatalog lists every known namespace via the lister and assembles the
// catalog. `include` filters by WIRE name (e.g. read-only for M1 S1:
// func(w string) bool { return Classify(w) == ClassRead }); nil includes all.
//
// Because bare tool names are what the model calls, a name appearing in two
// servers is a hard error — the loop could not unambiguously route it. (None
// collide today; the guard catches a future addition that would.)
func BuildCatalog(ctx context.Context, lister schemaLister, include func(wire string) bool, log *slog.Logger) (*Catalog, error) {
	cat := &Catalog{wireByName: make(map[string]string)}
	for _, ns := range knownNamespaces {
		schemas, err := lister.ListTools(ctx, ns)
		if err != nil {
			if log != nil {
				log.Warn("agent: skipping MCP namespace (list failed)", "namespace", ns, "err", err)
			}
			continue
		}
		for _, s := range schemas {
			wire := ns + "." + s.Name
			if include != nil && !include(wire) {
				continue
			}
			if prev, dup := cat.wireByName[s.Name]; dup {
				return nil, fmt.Errorf("agent: tool name collision %q (from %q and %q) — qualify the catalog before exposing both", s.Name, prev, wire)
			}
			cat.wireByName[s.Name] = wire
			cat.tools = append(cat.tools, ToolDef{
				Name:        s.Name,
				Description: s.Description,
				InputSchema: s.InputSchema,
			})
		}
	}
	return cat, nil
}

// Tools returns the model-facing tool definitions.
func (c *Catalog) Tools() []ToolDef { return c.tools }

// WireName maps a bare tool name (as the model emitted it) to its wire form
// "namespace.tool". ok is false if the model named a tool not in the catalog.
func (c *Catalog) WireName(bare string) (string, bool) {
	w, ok := c.wireByName[bare]
	return w, ok
}
