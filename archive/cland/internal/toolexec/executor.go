package toolexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultExecTimeout caps any single MCP CallTool invocation. The remote
// origin's HTTP timeout will likely cut us off first, but this is a safety
// net against runaway subprocesses (e.g. a tool that genuinely hangs).
const DefaultExecTimeout = 5 * time.Minute

// Executor spawns MCP server subprocesses and invokes a named tool. Stateless
// — each call is its own subprocess + stdio handshake + result; nothing
// persists between invocations. This is intentional for v1: spawn cost is
// negligible vs the work most tools do, and a fresh subprocess gives clean
// isolation (no cross-call state leaks).
type Executor struct {
	// BinariesDir resolves tool namespace → executable path.
	// On Linux production: "/opt/minti/mcp" (install.sh writes binaries here).
	// In dev/test: any directory containing minti-mcp-* binaries.
	BinariesDir string

	// ExecTimeout caps each CallTool invocation. Zero = DefaultExecTimeout.
	ExecTimeout time.Duration
}

// ExecResult is the structured response a /mcp/execute handler returns to
// the origin. Content blocks are simplified to a uniform shape so JSON
// encoding is stable across mcp-go SDK upgrades.
type ExecResult struct {
	IsError bool                   `json:"is_error"`
	Content []ResultContent        `json:"content"`
	Meta    map[string]any         `json:"meta,omitempty"`
}

// ResultContent normalises mcp.Content variants (TextContent, ImageContent,
// etc.) for over-the-wire transmission.
type ResultContent struct {
	Type string `json:"type"`           // "text" | "image" | "resource" | ...
	Text string `json:"text,omitempty"` // populated for type=text
	JSON []byte `json:"json,omitempty"` // raw JSON for non-text variants (resource refs etc.)
}

// ToolSchema is a tool's name + description + JSON-Schema input as advertised by
// an MCP server. The agent harness (cland/internal/agent) calls ListTools to
// build the model's tool catalog.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Errors callers check.
var (
	ErrUnknownNamespace = errors.New("toolexec: unknown MCP namespace")
	ErrToolNotFound     = errors.New("toolexec: tool not found on server")
	ErrSpawnFailed      = errors.New("toolexec: subprocess spawn failed")
)

// resolveServerBinary maps an MCP namespace like "mcp-recon" to:
//   - server: "minti-mcp-recon" (the MCP server name used by permission.Check)
//   - path:   <BinariesDir>/minti-mcp-recon[.exe on Windows]
func (e *Executor) resolveServerBinary(namespace string) (server, path string, err error) {
	if namespace == "" {
		return "", "", fmt.Errorf("%w: empty namespace", ErrUnknownNamespace)
	}
	server = "minti-" + namespace
	binaryName := server
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	if e.BinariesDir == "" {
		return "", "", errors.New("toolexec: Executor.BinariesDir is empty")
	}
	return server, filepath.Join(e.BinariesDir, binaryName), nil
}

// resolveBinary maps a wire tool string like "mcp-recon.nmap_scan" to:
//   - server: "minti-mcp-recon" (the MCP server name used by permission.Check)
//   - tool:   "nmap_scan"
//   - path:   <BinariesDir>/minti-mcp-recon[.exe on Windows]
func (e *Executor) resolveBinary(wireTool string) (server, tool, path string, err error) {
	dot := strings.IndexByte(wireTool, '.')
	if dot < 0 {
		return "", "", "", fmt.Errorf("%w: tool %q must be of the form 'namespace.tool'", ErrUnknownNamespace, wireTool)
	}
	namespace := wireTool[:dot] // e.g. "mcp-recon"
	tool = wireTool[dot+1:]
	if namespace == "" || tool == "" {
		return "", "", "", fmt.Errorf("%w: empty namespace or tool", ErrUnknownNamespace)
	}
	server, path, err = e.resolveServerBinary(namespace)
	return server, tool, path, err
}

// ListTools spawns the MCP server for `namespace` (e.g. "mcp-fs"), lists its
// tools, and returns their schemas. Like Execute it is stateless — one fresh
// subprocess per call. The agent Catalog calls this once per namespace and
// caches the result, so the spawn cost is paid only at catalog-build time.
func (e *Executor) ListTools(ctx context.Context, namespace string) ([]ToolSchema, error) {
	_, binPath, err := e.resolveServerBinary(namespace)
	if err != nil {
		return nil, err
	}

	timeout := e.ExecTimeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath)
	client := mcp.NewClient(&mcp.Implementation{Name: "minti-cland-toolexec", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSpawnFailed, err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	out := make([]ToolSchema, 0, len(tools.Tools))
	for _, t := range tools.Tools {
		// t.InputSchema is the server's JSON Schema (client-side: a JSON value);
		// marshal it back to raw bytes for the model's tool definition.
		schema, _ := json.Marshal(t.InputSchema)
		out = append(out, ToolSchema{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

// Execute spawns the MCP server, invokes the named tool with `args`, and
// returns the structured result. Context controls cancellation + timeout.
func (e *Executor) Execute(ctx context.Context, wireTool string, args map[string]any) (*ExecResult, string, string, error) {
	server, tool, binPath, err := e.resolveBinary(wireTool)
	if err != nil {
		return nil, "", "", err
	}

	timeout := e.ExecTimeout
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath)
	// Inherit stderr so MCP server crash logs land in cland's stderr (which
	// the systemd unit captures to journald).
	cmd.Stderr = nil // discarded; future: route to audit log

	client := mcp.NewClient(&mcp.Implementation{Name: "minti-cland-toolexec", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, server, tool, fmt.Errorf("%w: %v", ErrSpawnFailed, err)
	}
	defer session.Close()

	// Verify the tool exists (cheap; one extra round-trip; surfaces bad tool
	// names with a clean error instead of an obscure MCP framing failure).
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, server, tool, fmt.Errorf("list tools: %w", err)
	}
	found := false
	for _, t := range tools.Tools {
		if t.Name == tool {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(tools.Tools))
		for _, t := range tools.Tools {
			names = append(names, t.Name)
		}
		return nil, server, tool, fmt.Errorf("%w: %q not in [%s]", ErrToolNotFound, tool, strings.Join(names, ", "))
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		return nil, server, tool, fmt.Errorf("call tool: %w", err)
	}

	out := &ExecResult{IsError: result.IsError, Meta: result.Meta}
	for _, c := range result.Content {
		switch tc := c.(type) {
		case *mcp.TextContent:
			out.Content = append(out.Content, ResultContent{Type: "text", Text: tc.Text})
		default:
			b, _ := json.Marshal(c)
			out.Content = append(out.Content, ResultContent{Type: "other", JSON: b})
		}
	}
	return out, server, tool, nil
}
