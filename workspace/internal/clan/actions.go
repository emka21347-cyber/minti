// Actions are the workspace's write/RPC surface onto the local minti-cland
// CLI — join, chat, invite, knock accept/deny, cookbook. Same shell-out
// contract as clan.go/memory.go: the CLI runs the HMAC client + clan_key
// access correctly as the service user; we just relay results.
package clan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	Name    string `json:"name"`
	Desc    string `json:"desc"`
	Size    string `json:"size"`
	Tag     string `json:"tag"`     // the `ollama pull` target
	NeedRAM string `json:"need_ram"`
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

// CookbookInstallCmd returns the copy-paste command to install a pack.
func CookbookInstallCmd(name string) (string, error) {
	for _, p := range CookbookPacks() {
		if p.Name == name || p.Tag == name {
			return "ollama pull " + p.Tag, nil
		}
	}
	return "", errors.New("unknown pack: " + name)
}
