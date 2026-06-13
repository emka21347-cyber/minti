package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minti/cland/internal/agent"
	"github.com/minti/cland/internal/config"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/toolexec"
	"github.com/minti/cland/internal/transport"
)

// cmdAgent runs the native Hermes-agent loop (M1) for a single prompt. The loop
// offers the node's read-only MCP tools to the model, executes the tools it
// calls locally, feeds results back, and prints the exchange. Model turns route
// through the local daemon's /v1/messages (the tool-capable runtime endpoint);
// tools execute in-process here via toolexec.
//
// M1 S1: read-only. Change tools (fs.write_text, shell.exec, pkg.install) are
// refused until the approval gate (S3).
func cmdAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	model := fs.String("model", "hermes3:8b", "model to drive the agent")
	system := fs.String("system", defaultAgentSystem, "system prompt")
	mcpDir := fs.String("mcp-dir", "", "directory holding the minti-mcp-* binaries (default: config binaries_dir)")
	maxIters := fs.Int("max-iters", agent.DefaultMaxIters, "max tool-call rounds")
	readOnly := fs.Bool("read-only", false, "offer only read tools; change tools are refused (no approval prompt)")
	jsonOut := fs.Bool("json", false, "emit the raw NDJSON event stream instead of a rendered transcript")
	_ = fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		b, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		return errors.New("usage: minti-cland agent [--model m] [--mcp-dir d] <prompt>")
	}

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated — join a Clan first")
	}

	binDir := *mcpDir
	if binDir == "" {
		binDir = cfg.MCP.BinariesDir
	}
	if binDir == "" {
		return errors.New("no MCP binaries dir (set --mcp-dir or config mcp.binaries_dir)")
	}

	// Model transport: a long-timeout HMAC client to the local daemon, mirroring
	// cmdChat. The daemon's router proxies /v1/messages to the orchestrator's
	// runtime (self-route on a solo node).
	host := cfg.Listen.Address
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := fmt.Sprintf("https://%s:%d", host, cfg.Listen.Port)
	kp, err := crypto.NewSimpleKeyProvider(clan.ClanKey())
	if err != nil {
		return err
	}
	cli, err := transport.NewClient(transport.ClientOpts{
		MemberID:    id.MemberID,
		KeyProvider: kp,
		Pin:         clan.ClanCertPin,
		Timeout:     30 * time.Minute,
	})
	if err != nil {
		return err
	}

	executor := &toolexec.Executor{BinariesDir: binDir}

	ctx := context.Background()
	dbgLog := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// Default: offer all tools, gate change tools behind an interactive prompt.
	// --read-only: offer read tools only and wire no approver (change tools refused).
	var include func(wire string) bool
	var approver agent.Approver
	if *readOnly {
		include = func(wire string) bool { return agent.Classify(wire) == agent.ClassRead }
	} else {
		approver = &consoleApprover{in: bufio.NewReader(os.Stdin), out: os.Stderr}
	}
	catalog, err := agent.BuildCatalog(ctx, executor, include, dbgLog)
	if err != nil {
		return fmt.Errorf("build tool catalog: %w", err)
	}

	tc := catalog.Tools()
	names := make([]string, len(tc))
	for i, t := range tc {
		names[i] = t.Name
	}
	mode := "read+change (change tools require approval)"
	if *readOnly {
		mode = "read-only"
	}
	fmt.Fprintf(os.Stderr, "agent: %d tools available [%s]: %s\n", len(tc), mode, strings.Join(names, ", "))

	var emitter agent.Emitter
	if *jsonOut {
		emitter = agent.NewNDJSONEmitter(os.Stdout)
	} else {
		emitter = &consoleEmitter{w: os.Stdout}
	}

	loop := &agent.Loop{
		Caller:   &anthropicCaller{cli: cli, base: base, model: *model},
		Executor: executor,
		Catalog:  catalog,
		Emitter:  emitter,
		Approver: approver,
		System:   *system,
		MaxIters: *maxIters,
	}
	return loop.Run(ctx, prompt)
}

const defaultAgentSystem = "You are a MINTI node agent. You can call tools to inspect the local filesystem " +
	"and the web (read), and to change the system — write files, run shell commands, install packages. " +
	"Change actions require the user to approve each call, so use them only when needed and with care. " +
	"Use tools when they help; otherwise answer directly. When you have enough information, give a " +
	"concise final answer with no tool call."

// ---------- approval: interactive stdin prompt ----------

// consoleApprover implements agent.Approver by prompting the local user on
// stderr and reading y/N from stdin. Used by the CLI; the daemon's HTTP path
// (M1 S4) uses a channel-based approver instead. Fail-closed: anything other
// than an explicit yes is a deny, and a read error denies.
type consoleApprover struct {
	in  *bufio.Reader
	out io.Writer
}

func (a *consoleApprover) Await(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	fmt.Fprintf(a.out, "\n⚠ APPROVAL REQUIRED — %s [%s]\n  args: %s\n  approve? [y/N]: ",
		req.Tool, req.Class, truncate(string(req.Input), 300))
	line, err := a.in.ReadString('\n')
	if err != nil {
		return agent.DecisionDeny, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return agent.DecisionApprove, nil
	default:
		return agent.DecisionDeny, nil
	}
}

// ---------- model caller: Anthropic /v1/messages, non-streaming ----------

type anthropicCaller struct {
	cli   *transport.Client
	base  string
	model string
}

// wire types (subset of the runtime's Anthropic surface).
type aTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}
type aMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type aToolUse struct {
	Type  string          `json:"type"` // "tool_use"
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}
type aText struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}
type aToolResult struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func (c *anthropicCaller) Call(_ context.Context, system string, transcript []agent.Turn, tools []agent.ToolDef) (agent.ModelReply, error) {
	msgs := make([]aMessage, 0, len(transcript))
	for _, t := range transcript {
		switch {
		case t.Role == "assistant":
			var blocks []any
			if t.Text != "" {
				blocks = append(blocks, aText{Type: "text", Text: t.Text})
			}
			for _, tc := range t.ToolCalls {
				input := tc.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, aToolUse{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			raw, _ := json.Marshal(blocks)
			msgs = append(msgs, aMessage{Role: "assistant", Content: raw})
		case len(t.ToolResults) > 0:
			blocks := make([]any, 0, len(t.ToolResults))
			for _, tr := range t.ToolResults {
				blocks = append(blocks, aToolResult{Type: "tool_result", ToolUseID: tr.ToolUseID, Content: tr.Content})
			}
			raw, _ := json.Marshal(blocks)
			msgs = append(msgs, aMessage{Role: "user", Content: raw})
		default: // user text
			raw, _ := json.Marshal(t.Text)
			msgs = append(msgs, aMessage{Role: "user", Content: raw})
		}
	}

	atools := make([]aTool, 0, len(tools))
	for _, t := range tools {
		atools = append(atools, aTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}

	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"system":     system,
		"messages":   msgs,
		"tools":      atools,
		"max_tokens": 4096,
		"stream":     false,
	})

	resp, err := c.cli.Post(c.base+"/v1/messages", "application/json", body)
	if err != nil {
		return agent.ModelReply{}, fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return agent.ModelReply{}, fmt.Errorf("model call failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Content []json.RawMessage `json:"content"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return agent.ModelReply{}, fmt.Errorf("decode model response: %w", err)
	}
	if out.Error != nil {
		return agent.ModelReply{}, fmt.Errorf("%s", out.Error.Message)
	}

	var reply agent.ModelReply
	for _, block := range out.Content {
		var typed struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(block, &typed)
		switch typed.Type {
		case "text":
			var b aText
			if json.Unmarshal(block, &b) == nil {
				reply.Text += b.Text
			}
		case "tool_use":
			var b aToolUse
			if json.Unmarshal(block, &b) == nil {
				reply.ToolCalls = append(reply.ToolCalls, agent.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
			}
		}
	}
	return reply, nil
}

// ---------- console renderer ----------

type consoleEmitter struct{ w io.Writer }

func (e *consoleEmitter) Emit(ev agent.Event) error {
	switch ev.Type {
	case agent.EventText:
		fmt.Fprintf(e.w, "\n%s\n", ev.Text)
	case agent.EventToolCall:
		fmt.Fprintf(e.w, "  → %s [%s] %s\n", ev.Tool, ev.Class, truncate(string(ev.Input), 160))
	case agent.EventToolRunning:
		// quiet — tool_result follows
	case agent.EventToolResult:
		marker := "←"
		if ev.IsError {
			marker = "✗"
		}
		fmt.Fprintf(e.w, "  %s %s\n", marker, truncate(ev.Result, 200))
	case agent.EventFinal:
		fmt.Fprintf(e.w, "\n%s\n", ev.Text)
	case agent.EventError:
		fmt.Fprintf(os.Stderr, "error: %s\n", ev.Text)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
