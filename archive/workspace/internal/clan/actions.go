// Actions are the workspace's write/RPC surface onto the local minti-cland
// CLI — join, chat, invite, knock accept/deny, cookbook. Same shell-out
// contract as clan.go/memory.go: the CLI runs the HMAC client + clan_key
// access correctly as the service user; we just relay results.
package clan

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

// JoinRequest is the body of POST /api/join. Either a single connection token
// (the one-paste path) OR the three raw fields.
type JoinRequest struct {
	Connect string `json:"connect"`
	Token   string `json:"token"`
	Address string `json:"address"`
	Pin     string `json:"pin"`
}

// Join shells `minti-cland join` to join the Clan. The workspace runs as the
// user that owns the cland state (Linux minti, macOS _minti), so the CLI
// writes clan.json directly; the idle daemon notices within ~2s and restarts
// into active mode. Returns the CLI's error verbatim (bad token, already
// joined, founder unreachable, pin mismatch).
func Join(ctx context.Context, req JoinRequest) error {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return errors.New("minti-cland not installed")
	}
	var args []string
	switch {
	case req.Connect != "":
		args = []string{"join", "--connect", req.Connect}
	case req.Token != "" && req.Address != "" && req.Pin != "":
		args = []string{"join", "--token", req.Token, "--address", req.Address, "--pin", req.Pin}
	default:
		return errors.New("provide a connection token, or token + address + pin")
	}
	_, err = runCland(ctx, bin, args...)
	return err
}

// ChatStream shells `minti-cland chat` and copies its stdout to w, flushing
// after every read so the browser renders tokens as they arrive. The CLI
// streams the model reply unbuffered; we relay it byte-for-byte. ctx is the
// request context — a client disconnect cancels it and exec.CommandContext
// kills the CLI (no orphaned inference). On failure the CLI's stderr is
// returned (caller already sent 200 headers, so it surfaces inline).
// AgentChatStream relays `minti-cland agent --json` stdout (the NDJSON agent
// event stream) to w. Mirrors ChatStream: the agent loop runs in the cland
// daemon (which spawns MCP servers + has network egress — the workspace unit is
// loopback-only); this shelled CLI is a thin client to the daemon. Change tools
// surface `approval_required` events the SPA resolves via POST /api/agent/approve.
func AgentChatStream(ctx context.Context, message, model string, readOnly bool, w io.Writer, flush func()) error {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return errors.New("minti-cland not installed")
	}
	args := []string{"agent", "--json"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	if readOnly {
		args = append(args, "--read-only")
	}
	args = append(args, message) // flags-before-positional (Go flag parsing)

	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				_ = cmd.Process.Kill()
				break
			}
			flush()
		}
		if rerr != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// AgentApprove resolves a single pending agent approval by shelling
// `minti-cland agent-approve` (which POSTs to the daemon's /agent/approve).
func AgentApprove(ctx context.Context, reqID, callID string, approve bool) error {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return errors.New("minti-cland not installed")
	}
	args := []string{"agent-approve", "--req", reqID, "--call", callID}
	if !approve {
		args = append(args, "--deny")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func ChatStream(ctx context.Context, message, model string, w io.Writer, flush func()) error {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return errors.New("minti-cland not installed")
	}
	args := []string{"chat"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	args = append(args, message) // flags-before-positional (Go flag parsing)

	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	buf := make([]byte, 4096)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				_ = cmd.Process.Kill()
				break
			}
			flush()
		}
		if rerr != nil {
			break // EOF or pipe closed
		}
	}
	if err := cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// Invite mints a single-use connection token (founder/any member). Returns the
// CLI's --json payload verbatim — it carries the `connect` blob the SPA shows.
func Invite(ctx context.Context, ttlSeconds int) ([]byte, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, errors.New("minti-cland not installed")
	}
	if ttlSeconds < 60 {
		ttlSeconds = 300
	}
	return runCland(ctx, bin, "invite", "--json", "--ttl", fmt.Sprintf("%ds", ttlSeconds))
}

// Peers relays `peers --json` (live registry — members + candidates).
func Peers(ctx context.Context) ([]byte, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, errors.New("minti-cland not installed")
	}
	return runCland(ctx, bin, "peers", "--json")
}

// Knocks relays `knocks --json` (pending knock requests awaiting approval).
func Knocks(ctx context.Context) ([]byte, error) {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return nil, errors.New("minti-cland not installed")
	}
	return runCland(ctx, bin, "knocks", "--json")
}

// KnockAccept approves a pending knock by id.
func KnockAccept(ctx context.Context, id string) error {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return errors.New("minti-cland not installed")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("knock id required")
	}
	_, err = runCland(ctx, bin, "knock-accept", id)
	return err
}

// KnockDeny rejects a pending knock. --reason goes BEFORE the id positional
// (Go's flag parser stops at the first positional).
func KnockDeny(ctx context.Context, id, reason string) error {
	bin, err := exec.LookPath("minti-cland")
	if err != nil {
		return errors.New("minti-cland not installed")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("knock id required")
	}
	args := []string{"knock-deny"}
	if strings.TrimSpace(reason) != "" {
		args = append(args, "--reason", reason)
	}
	args = append(args, id)
	_, err = runCland(ctx, bin, args...)
	return err
}

// Pack is one entry in the model cookbook.
type Pack struct {
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Size      string `json:"size"`
	Tag       string `json:"tag"` // the `ollama pull` target
	NeedRAM   string `json:"need_ram"`
	Installed bool   `json:"installed"`
}

// CookbookPacks is the v0.5 static manifest. Real one-click install + a live
// RAM "fits" probe are fast-follows; for now the UI shows the packs and the
// exact `ollama pull` command (CookbookInstallCmd).
func CookbookPacks() []Pack {
	return []Pack{
		{Name: "Hermes 3", Desc: "Agent-tuned chat (Nous Research)", Size: "~4.7 GB", Tag: "hermes3:8b", NeedRAM: "~8 GB free"},
		{Name: "Mistral", Desc: "Fast general chat", Size: "~4.1 GB", Tag: "mistral:7b", NeedRAM: "~6 GB free"},
		{Name: "Llama 3.2 3B", Desc: "Small + CPU-friendly", Size: "~2.0 GB", Tag: "llama3.2:3b", NeedRAM: "~4 GB free"},
	}
}

// ollamaBase is where Ollama listens on every node (loopback only). The
// workspace unit's IPAddressAllow=127.0.0.0/8 permits this.
const ollamaBase = "http://127.0.0.1:11434"

// ollamaInstalled returns the set of model tags currently pulled on this node
// (best-effort; empty if Ollama is unreachable).
func ollamaInstalled(ctx context.Context) map[string]bool {
	set := map[string]bool{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaBase+"/api/tags", nil)
	if err != nil {
		return set
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return set
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) == nil {
		for _, m := range tags.Models {
			set[m.Name] = true
		}
	}
	return set
}

// CookbookList returns the manifest with each pack's Installed flag set from
// the node's live Ollama model list.
func CookbookList(ctx context.Context) []Pack {
	have := ollamaInstalled(ctx)
	packs := CookbookPacks()
	for i := range packs {
		packs[i].Installed = have[packs[i].Tag]
	}
	return packs
}

// CookbookUninstall removes a pack's model from this node via Ollama's
// DELETE /api/delete.
func CookbookUninstall(ctx context.Context, name string) error {
	tag := ""
	for _, p := range CookbookPacks() {
		if p.Name == name || p.Tag == name {
			tag = p.Tag
			break
		}
	}
	if tag == "" {
		return errors.New("unknown pack: " + name)
	}
	body, _ := json.Marshal(map[string]any{"name": tag})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, ollamaBase+"/api/delete", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("Ollama not reachable on 127.0.0.1:11434")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("uninstall failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// CookbookInstallStream pulls a pack's model onto this node via Ollama's
// streaming /api/pull, relaying human-readable progress ("pulling … 45%") to
// w. ctx (the request) cancels the pull if the client disconnects.
func CookbookInstallStream(ctx context.Context, name string, w io.Writer, flush func()) error {
	tag := ""
	for _, p := range CookbookPacks() {
		if p.Name == name || p.Tag == name {
			tag = p.Tag
			break
		}
	}
	if tag == "" {
		return errors.New("unknown pack: " + name)
	}
	body, _ := json.Marshal(map[string]any{"name": tag, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaBase+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("Ollama not reachable on 127.0.0.1:11434 — is it installed and running?")
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lastPct := -1
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Status    string `json:"status"`
			Error     string `json:"error"`
			Completed int64  `json:"completed"`
			Total     int64  `json:"total"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return errors.New(ev.Error)
		}
		msg := ev.Status
		if ev.Total > 0 { // downloading a layer — emit only on percent change to limit noise
			pct := int(ev.Completed * 100 / ev.Total)
			if pct == lastPct {
				continue
			}
			lastPct = pct
			msg = ev.Status + " " + strconv.Itoa(pct) + "%"
		}
		_, _ = io.WriteString(w, msg+"\n")
		flush()
	}
	return sc.Err()
}
