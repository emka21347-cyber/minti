package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minti/cland/internal/agent"
	"github.com/minti/cland/internal/config"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/transport"
)

// cmdAgent is a thin client to the daemon's native agent loop. It POSTs the
// prompt to /agent/chat, streams the NDJSON event stream, and renders it. The
// loop itself runs in the daemon (which spawns MCP servers + has network egress);
// this command holds no tools. In interactive (non-JSON) mode it prompts the
// terminal for Approve/Deny on each change tool and POSTs /agent/approve; in
// --json mode it just streams (approvals come via `agent-approve`, e.g. from the
// workspace dashboard).
func cmdAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	model := fs.String("model", "hermes3:8b", "model to drive the agent")
	readOnly := fs.Bool("read-only", false, "offer only read tools; change tools are refused")
	jsonOut := fs.Bool("json", false, "stream the raw NDJSON events (no interactive approval prompts)")
	_ = fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		b, _ := io.ReadAll(os.Stdin)
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		return errors.New("usage: minti-cland agent [--model m] [--read-only] [--json] <prompt>")
	}

	cli, base, err := agentClient(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{"message": prompt, "model": *model, "read_only": *readOnly})
	resp, err := cli.Post(base+"/agent/chat", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	render := &consoleEmitter{w: os.Stdout}
	stdin := bufio.NewReader(os.Stdin)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if *jsonOut {
			fmt.Printf("%s\n", line)
		}
		var ev agent.Event
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if !*jsonOut {
			_ = render.Emit(ev)
		}
		// Interactive approval: only when attended (non-JSON) and not read-only.
		if ev.Type == agent.EventApprovalRequired && !*jsonOut && !*readOnly {
			approve := promptYesNo(stdin)
			if err := postApprove(cli, base, ev.ReqID, ev.CallID, approve); err != nil {
				fmt.Fprintf(os.Stderr, "approve failed: %v\n", err)
			}
		}
	}
	return sc.Err()
}

// cmdAgentApprove resolves a single pending approval out-of-band — used by the
// workspace dashboard relay (and available for scripting). It POSTs to the
// daemon's /agent/approve.
func cmdAgentApprove(args []string) error {
	fs := flag.NewFlagSet("agent-approve", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	reqID := fs.String("req", "", "agent request id (from the approval_required event)")
	callID := fs.String("call", "", "tool call id (from the approval_required event)")
	deny := fs.Bool("deny", false, "deny instead of approve")
	_ = fs.Parse(args)
	if *reqID == "" || *callID == "" {
		return errors.New("usage: minti-cland agent-approve --req <id> --call <id> [--deny]")
	}
	cli, base, err := agentClient(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	return postApprove(cli, base, *reqID, *callID, !*deny)
}

// agentClient builds the loopback HMAC client + base URL the agent commands use
// to reach the local daemon. Mirrors cmdChat's transport setup.
func agentClient(cfgPath, stateDir string) (*transport.Client, string, error) {
	cfg, id, store, err := loadCommon(cfgPath, stateDir)
	if err != nil {
		return nil, "", err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return nil, "", err
	}
	if !clan.IsActive() {
		return nil, "", errors.New("unaffiliated — join a Clan first")
	}
	host := cfg.Listen.Address
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	kp, err := crypto.NewSimpleKeyProvider(clan.ClanKey())
	if err != nil {
		return nil, "", err
	}
	cli, err := transport.NewClient(transport.ClientOpts{
		MemberID:    id.MemberID,
		KeyProvider: kp,
		Pin:         clan.ClanCertPin,
		Timeout:     30 * time.Minute,
	})
	if err != nil {
		return nil, "", err
	}
	return cli, fmt.Sprintf("https://%s:%d", host, cfg.Listen.Port), nil
}

func postApprove(cli *transport.Client, base, reqID, callID string, approve bool) error {
	body, _ := json.Marshal(map[string]any{"req_id": reqID, "call_id": callID, "approve": approve})
	resp, err := cli.Post(base+"/agent/approve", "application/json", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("approve failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func promptYesNo(in *bufio.Reader) bool {
	fmt.Fprint(os.Stderr, "  approve? [y/N]: ")
	line, err := in.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// defaultAgentSystem is the system prompt the daemon's agent loop uses.
const defaultAgentSystem = "You are a MINTI node agent. You can call tools to inspect the local filesystem " +
	"and the web (read), and to change the system — write files, run shell commands, install packages. " +
	"Change actions require the user to approve each call, so use them only when needed and with care. " +
	"Use tools when they help; otherwise answer directly. When you have enough information, give a " +
	"concise final answer with no tool call."

// ---------- model caller: Anthropic /v1/messages, non-streaming (used by the daemon loop) ----------

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
		return agent.ModelReply{}, fmt.Errorf("call runtime: %w", err)
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
	case agent.EventApprovalRequired:
		fmt.Fprintf(e.w, "  ⚠ APPROVAL REQUIRED — %s [%s] %s\n", ev.Tool, ev.Class, truncate(string(ev.Input), 200))
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
