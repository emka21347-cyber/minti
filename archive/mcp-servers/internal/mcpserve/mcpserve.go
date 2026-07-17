// Package mcpserve is the thin wrapper every MINTI MCP server uses. It does
// three jobs:
//
//   - Hands off MCP wire protocol to github.com/modelcontextprotocol/go-sdk
//   - Routes every tool invocation through internal/permission.Check before
//     calling the user's handler — denied calls never reach the handler
//   - Writes a structured event to internal/audit for every call, regardless of
//     outcome
//
// Servers register tools via the top-level generic AddTool — Go does not allow
// generic methods on struct types, so the wrapper is a free function over the
// Server value.
package mcpserve

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/permission"
	"github.com/minti/mcp-servers/internal/policy"
)

type Server struct {
	name    string
	version string
	impl    *mcp.Server
	pol     *policy.Policy
	log     *audit.Logger
}

// New creates a Server. name should match the installed binary name
// (e.g. "minti-mcp-recon") because permission rules are keyed by it.
func New(name, version string, pol *policy.Policy, log *audit.Logger) *Server {
	return &Server{
		name:    name,
		version: version,
		impl:    mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, nil),
		pol:     pol,
		log:     log,
	}
}

// Run starts the stdio MCP server and blocks until ctx is cancelled or stdin
// closes (whichever first).
func (s *Server) Run(ctx context.Context) error {
	return s.impl.Run(ctx, &mcp.StdioTransport{})
}

// Name returns the registered server name.
func (s *Server) Name() string { return s.name }

// AddTool registers a tool. The handler receives typed input, returns typed
// output; everything else (consent enforcement, audit) is taken care of.
//
// The handler is NOT called if policy denies the request.
func AddTool[In, Out any](
	s *Server,
	tool *mcp.Tool,
	handler func(context.Context, In) (Out, error),
) {
	if tool.Name == "" {
		panic("mcpserve.AddTool: tool.Name is required")
	}
	name := tool.Name
	mcp.AddTool(s.impl, tool, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input In,
	) (*mcp.CallToolResult, Out, error) {
		var zero Out

		argsMap := toMap(input)
		decision, reason := permission.Check(s.pol, s.name, name, argsMap)
		evt := audit.Event{
			Server:   s.name,
			Tool:     name,
			Decision: decision.String(),
			Args:     argsMap,
			Reason:   reason,
		}
		if decision == permission.Deny {
			_ = s.log.Write(evt)
			// Return a structured tool-call error rather than transport error
			// so the host sees a useful message.
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: "policy denied: " + reason},
				},
			}, zero, nil
		}

		start := time.Now()
		out, err := handler(ctx, input)
		evt.DurationMS = time.Since(start).Milliseconds()
		if err != nil {
			evt.Error = err.Error()
		}
		_ = s.log.Write(evt)

		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				},
			}, zero, nil
		}
		return nil, out, nil
	})
}

func toMap(in any) map[string]any {
	b, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		// Non-object inputs (rare) fall back to a single-key map for audit.
		return map[string]any{"_": fmt.Sprintf("%v", in)}
	}
	return m
}
