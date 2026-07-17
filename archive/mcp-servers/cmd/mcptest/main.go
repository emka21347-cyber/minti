// mcptest — stdio MCP test client used for M2 validation.
//
// Spawns an MCP server binary, lists its tools, renders a consent prompt for
// the requested tool + args, and (on approval) invokes it. Doubles as a
// debugging tool through M3 once opencode is bundled.
//
// Usage:
//
//	mcptest [--arg k=v ...] [--yes] <server-binary> <tool-name>
//
// Note: Go's flag parser stops at the first positional, so all --arg / --yes
// flags must precede the server binary path.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	var (
		yes  = flag.Bool("yes", false, "skip the interactive consent prompt")
		args arrayFlag
	)
	flag.Var(&args, "arg", "tool argument as key=value (repeatable; bool/int auto-parsed)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: mcptest [--arg k=v]... [--yes] <server-binary> <tool-name>")
		fmt.Fprintln(os.Stderr, "  All flags must precede the positional <server-binary> argument.")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(2)
	}
	serverBin := flag.Arg(0)
	toolName := flag.Arg(1)

	parsed, err := parseArgs(args)
	if err != nil {
		die(err)
	}

	ctx := context.Background()

	cmd := exec.CommandContext(ctx, serverBin)
	cmd.Stderr = os.Stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "mcptest", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		die(fmt.Errorf("connect to %s: %w", serverBin, err))
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		die(fmt.Errorf("list tools: %w", err))
	}

	var target *mcp.Tool
	for _, t := range tools.Tools {
		if t.Name == toolName {
			target = t
			break
		}
	}
	if target == nil {
		names := make([]string, 0, len(tools.Tools))
		for _, t := range tools.Tools {
			names = append(names, t.Name)
		}
		die(fmt.Errorf("tool %q not found in %s. Available: %s",
			toolName, serverBin, strings.Join(names, ", ")))
	}

	// Consent prompt (rendered to stderr so stdout stays usable for piped results).
	fmt.Fprintf(os.Stderr, "\n[mcptest] tool: %s\n", target.Name)
	if target.Description != "" {
		fmt.Fprintf(os.Stderr, "[mcptest] description: %s\n", target.Description)
	}
	fmt.Fprintf(os.Stderr, "[mcptest] args: %s\n", prettyArgs(parsed))

	if !*yes {
		fmt.Fprint(os.Stderr, "[mcptest] approve this tool call? [y/N] ")
		var resp string
		_, _ = fmt.Fscanln(os.Stdin, &resp)
		switch strings.ToLower(strings.TrimSpace(resp)) {
		case "y", "yes":
		default:
			fmt.Fprintln(os.Stderr, "[mcptest] aborted by user")
			os.Exit(3)
		}
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: parsed,
	})
	if err != nil {
		die(fmt.Errorf("call tool: %w", err))
	}

	fmt.Fprintln(os.Stderr, "[mcptest] --- result ---")
	for _, c := range result.Content {
		switch tc := c.(type) {
		case *mcp.TextContent:
			fmt.Println(tc.Text)
		default:
			b, _ := json.MarshalIndent(c, "", "  ")
			fmt.Println(string(b))
		}
	}
	if result.IsError {
		os.Exit(4)
	}
}

type arrayFlag []string

func (a *arrayFlag) String() string     { return strings.Join(*a, ",") }
func (a *arrayFlag) Set(v string) error { *a = append(*a, v); return nil }

func parseArgs(raw []string) (map[string]any, error) {
	m := make(map[string]any, len(raw))
	for _, p := range raw {
		i := strings.IndexByte(p, '=')
		if i < 0 {
			return nil, fmt.Errorf("arg %q: expected key=value", p)
		}
		k, v := p[:i], p[i+1:]
		m[k] = parseValue(v)
	}
	return m, nil
}

func parseValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func prettyArgs(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "[mcptest] ERROR:", err)
	os.Exit(1)
}
