package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minti/cland/internal/config"
	"github.com/minti/cland/internal/crypto"
	"github.com/minti/cland/internal/transport"
)

// cmdChat sends a chat message through the local daemon's router and streams
// the reply to stdout. The router self-routes (orchestrator == us) or
// peer-proxies to the orchestrator, which talks to its minti-runtime → Ollama;
// either way the NDJSON reply streams straight back.
func cmdChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "path to cland config")
	stateDirFlag := fs.String("state", "", "state directory (overrides config)")
	model := fs.String("model", "hermes3:8b", "model to route the request to")
	system := fs.String("system", "", "optional system prompt")
	jsonOut := fs.Bool("json", false, "emit the whole reply as one JSON object instead of streaming text")
	_ = fs.Parse(args)

	msg := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if msg == "" { // fall back to stdin so callers can pipe a prompt
		b, _ := io.ReadAll(os.Stdin)
		msg = strings.TrimSpace(string(b))
	}
	if msg == "" {
		return errors.New("usage: minti-cland chat [--model <m>] <message>")
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

	// Dedicated long-timeout client. localDaemonClient caps at 15s, and the
	// http.Client timeout bounds the WHOLE response read — so a reply that
	// streams longer than 15s would be truncated mid-sentence. Chat can run
	// minutes on a CPU node; give it 30 (matches the router's proxy timeout).
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

	// Ollama-shaped /api/chat body — forwarded verbatim router→runtime→Ollama.
	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]chatMsg, 0, 2)
	if *system != "" {
		msgs = append(msgs, chatMsg{Role: "system", Content: *system})
	}
	msgs = append(msgs, chatMsg{Role: "user", Content: msg})
	body, _ := json.Marshal(map[string]any{
		"model":    *model,
		"messages": msgs,
		"stream":   true,
	})

	resp, err := cli.Post(base+"/api/chat", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chat failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	// Stream NDJSON: each line is {message:{content}, done}. Write content to
	// stdout as it arrives — os.Stdout is unbuffered here ON PURPOSE; wrapping
	// it in a bufio.Writer would defeat the token-by-token streaming the
	// workspace relays to the browser.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var full strings.Builder
	var gotContent bool
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Error string `json:"error"`
			Done  bool   `json:"done"`
		}
		if json.Unmarshal([]byte(line), &chunk) != nil {
			continue // tolerate keepalives / non-JSON framing
		}
		// Ollama reports a missing model (etc.) as an {"error":...} line — surface
		// it instead of streaming nothing and looking hung.
		if chunk.Error != "" {
			return fmt.Errorf("%s", chunk.Error)
		}
		if chunk.Message.Content != "" {
			gotContent = true
		}
		if *jsonOut {
			full.WriteString(chunk.Message.Content)
		} else {
			_, _ = io.WriteString(os.Stdout, chunk.Message.Content)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("stream read: %w", err)
	}
	// An empty 200 stream is how the runtime currently signals a model that
	// isn't pulled (rather than a clean error) — don't look hung, say so.
	if !gotContent {
		return fmt.Errorf("no response from %q — is the model pulled? install it from the Cookbook or pick another", *model)
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"reply": full.String()})
	}
	fmt.Println()
	return nil
}
