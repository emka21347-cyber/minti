package main

// Memory CLI subcommands (Clan Memory graph, spec §13). Split from main.go
// for size; same package, same loadCommon/localDaemonClient plumbing.

import (
	"bytes"
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
	"github.com/minti/cland/internal/election"
	"github.com/minti/cland/internal/memory"
	"github.com/minti/cland/internal/transport"
)

// memoryDaemon bundles the boilerplate every memory subcommand shares:
// load config + identity + state, require an active Clan, build the
// loopback HMAC client.
func memoryDaemon(cfgPath, stateDirFlag string) (*transport.Client, string, error) {
	cfg, id, store, err := loadCommon(cfgPath, stateDirFlag)
	if err != nil {
		return nil, "", err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return nil, "", err
	}
	if !clan.IsActive() {
		return nil, "", errors.New("unaffiliated")
	}
	return localDaemonClient(cfg, clan, id)
}

// fetchMemoryGraph GETs the full graph from the local daemon.
func fetchMemoryGraph(cli *transport.Client, base string) (*memory.Graph, error) {
	req, _ := http.NewRequest(http.MethodGet, base+"/clan/memory", nil)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("memory fetch (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var g memory.Graph
	if err := json.NewDecoder(resp.Body).Decode(&g); err != nil {
		return nil, err
	}
	return &g, nil
}

// postMemoryNode POSTs a node write and decodes the stored result.
func postMemoryNode(cli *transport.Client, base string, n memory.Node) (memory.Node, error) {
	body, _ := json.Marshal(n)
	resp, err := cli.Post(base+"/clan/memory/node", "application/json", body)
	if err != nil {
		return memory.Node{}, fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return memory.Node{}, fmt.Errorf("memory node write (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var stored memory.Node
	if err := json.Unmarshal(raw, &stored); err != nil {
		return memory.Node{}, err
	}
	return stored, nil
}

// splitPositionals returns up to max leading non-flag arguments and re-parses
// any remaining arguments (trailing flags) on fs. Go's flag package stops at
// the first positional, which silently swallows trailing flags — and, worse,
// makes a swallowed --config/--state fall back to the PRODUCTION daemon
// defaults (caught live by the M1 smoke: `research start "title" --state X`
// posted at the real :7777 service). max < 0 means unlimited.
func splitPositionals(fs *flag.FlagSet, max int) ([]string, error) {
	rest := fs.Args()
	var pos []string
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && (max < 0 || len(pos) < max) {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		pos = append(pos, fs.Args()...)
	}
	return pos, nil
}

// resolveNodeID resolves a user-supplied id against the graph: exact match
// first, then unique prefix. Returns the input verbatim when nothing matches
// (dangling references are legal per spec §13.4).
func resolveNodeID(g *memory.Graph, in string) (string, error) {
	if in == "" {
		return "", errors.New("empty node id")
	}
	var prefix []string
	for _, n := range g.Nodes {
		if n.ID == in {
			return in, nil
		}
		if strings.HasPrefix(n.ID, in) {
			prefix = append(prefix, n.ID)
		}
	}
	switch len(prefix) {
	case 0:
		return in, nil
	case 1:
		return prefix[0], nil
	default:
		return "", fmt.Errorf("id prefix %q is ambiguous (%d matches)", in, len(prefix))
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func cmdMemory(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: minti-cland memory <list|show|add|link|archive|digest|research> [flags]")
	}
	switch args[0] {
	case "list":
		return cmdMemoryList(args[1:])
	case "show":
		return cmdMemoryShow(args[1:])
	case "add":
		return cmdMemoryAdd(args[1:])
	case "link":
		return cmdMemoryLink(args[1:])
	case "archive":
		return cmdMemoryArchive(args[1:])
	case "digest":
		return cmdMemoryDigest(args[1:])
	case "research":
		return cmdMemoryResearch(args[1:])
	case "export":
		return cmdMemoryExport(args[1:])
	case "import":
		return cmdMemoryImport(args[1:])
	default:
		return fmt.Errorf("unknown memory subcommand %q", args[0])
	}
}

// ---------- export / import (Clan Blueprint, spec §13.10) ----------

func cmdMemoryExport(args []string) error {
	fs := flag.NewFlagSet("memory export", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	outF := fs.String("out", "", "output file (default minti-blueprint-<date>.json)")
	sessionF := fs.String("session", "", "export only one research session (id or prefix)")
	stripF := fs.Bool("strip-authors", false, "pseudonymize member identities (member-1..N)")
	_ = fs.Parse(args)

	cfg, id, store, err := loadCommon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	clan, err := store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}
	cli, base, err := localDaemonClient(cfg, clan, id)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}

	sessionID := *sessionF
	if sessionID != "" {
		if sessionID, err = resolveNodeID(g, sessionID); err != nil {
			return err
		}
	}

	bp, err := memory.ExportBlueprint(g, clan.ClanID, sessionID, *stripF, time.Now())
	if err != nil {
		return err
	}

	out := *outF
	if out == "" {
		out = "minti-blueprint-" + time.Now().Format("2006-01-02") + ".json"
	}
	data, err := json.MarshalIndent(bp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		return err
	}

	// Spec §13.11: no silent export — distillates may carry chat content.
	fmt.Println("WARNING: the blueprint may contain distilled chat content and member")
	fmt.Println("activity. Treat the file as sensitive; share deliberately.")
	if !*stripF {
		fmt.Println("(member ids are included verbatim — use --strip-authors to pseudonymize)")
	}
	fmt.Println()
	fmt.Printf("Blueprint written: %s\n", out)
	fmt.Printf("  nodes=%d edges=%d proposed=%d archived=%d\n",
		bp.Stats.Nodes, bp.Stats.Edges, bp.Stats.Proposed, bp.Stats.Archived)
	fmt.Printf("  checksum=%s\n", bp.ChecksumSHA256[:16]+"…")
	fmt.Println()
	fmt.Println("A fresh Clan can inherit it at creation:")
	fmt.Printf("  minti-cland create --from-blueprint %s\n", out)
	return nil
}

func cmdMemoryImport(args []string) error {
	fs := flag.NewFlagSet("memory import", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	replaceF := fs.Bool("replace", false, "DESTRUCTIVE: discard the local graph before importing")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)
	pos, err := splitPositionals(fs, 1)
	if err != nil || len(pos) == 0 {
		return errors.New("usage: minti-cland memory import <blueprint.json> [--replace]")
	}

	bp, err := readBlueprintFile(pos[0])
	if err != nil {
		return err
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	mode := "merge"
	if *replaceF {
		mode = "replace"
	}
	body, _ := json.Marshal(memory.ImportRequest{Blueprint: bp, Mode: mode})
	resp, err := cli.Post(base+"/clan/memory/import", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("memory import (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		fmt.Println(strings.TrimSpace(string(raw)))
		return nil
	}
	var ir memory.ImportResponse
	_ = json.Unmarshal(raw, &ir)
	if ir.Changed {
		fmt.Printf("Imported (%s): graph digest now %s…\n", mode, ir.Digest[:16])
		if mode == "merge" {
			fmt.Println("The merged graph gossips to the whole Clan automatically.")
		}
	} else {
		fmt.Println("Import was a no-op — the graph already contained everything.")
	}
	return nil
}

// readBlueprintFile loads + client-side-validates a blueprint so the user
// gets a clear error before anything touches the daemon.
func readBlueprintFile(path string) (*memory.Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bp memory.Blueprint
	if err := json.Unmarshal(data, &bp); err != nil {
		return nil, fmt.Errorf("not a blueprint JSON file: %w", err)
	}
	if err := memory.ValidateBlueprint(&bp); err != nil {
		return nil, err
	}
	return &bp, nil
}

func cmdMemoryList(args []string) error {
	fs := flag.NewFlagSet("memory list", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	sessionF := fs.String("session", "", "filter: nodes of one research session (id or prefix)")
	typeF := fs.String("type", "", "filter: node type")
	statusF := fs.String("status", "", "filter: node status")
	_ = fs.Parse(args)

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}

	sessionID := *sessionF
	if sessionID != "" {
		if sessionID, err = resolveNodeID(g, sessionID); err != nil {
			return err
		}
	}
	filtered := make([]memory.Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if sessionID != "" && n.SessionID != sessionID && n.ID != sessionID {
			continue
		}
		if *typeF != "" && n.Type != *typeF {
			continue
		}
		if *statusF != "" && n.Status != *statusF {
			continue
		}
		filtered = append(filtered, n)
	}

	if *jsonOut {
		out := memory.Graph{FormatVersion: g.FormatVersion, Nodes: filtered, Edges: g.Edges}
		return json.NewEncoder(os.Stdout).Encode(&out)
	}
	if len(filtered) == 0 {
		fmt.Println("No memory nodes (matching the filters).")
		return nil
	}
	fmt.Printf("Memory nodes (%d of %d, %d edges):\n", len(filtered), len(g.Nodes), len(g.Edges))
	for _, n := range filtered {
		session := ""
		if n.SessionID != "" {
			session = " session=" + shortID(n.SessionID)
		}
		fmt.Printf("  %s  %-16s %-10s %s%s\n",
			shortID(n.ID), n.Type, n.Status, n.Title, session)
	}
	return nil
}

func cmdMemoryShow(args []string) error {
	fs := flag.NewFlagSet("memory show", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)
	pos, err := splitPositionals(fs, 1)
	if err != nil || len(pos) == 0 {
		return errors.New("usage: minti-cland memory show <id>")
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}
	nodeID, err := resolveNodeID(g, pos[0])
	if err != nil {
		return err
	}
	var found *memory.Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == nodeID {
			found = &g.Nodes[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("no node %q", pos[0])
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(found)
	}
	fmt.Printf("ID:        %s\n", found.ID)
	fmt.Printf("Type:      %s\n", found.Type)
	fmt.Printf("Status:    %s\n", found.Status)
	fmt.Printf("Title:     %s\n", found.Title)
	if found.SessionID != "" {
		fmt.Printf("Session:   %s\n", found.SessionID)
	}
	if len(found.Tags) > 0 {
		fmt.Printf("Tags:      %s\n", strings.Join(found.Tags, ", "))
	}
	fmt.Printf("Author:    %s (source=%s)\n", found.Provenance.AuthorMemberID, found.Provenance.Source)
	fmt.Printf("Created:   %s\n", found.Provenance.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:   %s (rev %d)\n", found.UpdatedAt.Format(time.RFC3339), found.Rev)
	if found.Body != "" {
		fmt.Printf("\n%s\n", found.Body)
	}
	first := true
	for _, e := range g.Edges {
		if e.From != found.ID && e.To != found.ID {
			continue
		}
		if first {
			fmt.Println("\nEdges:")
			first = false
		}
		fmt.Printf("  %s --%s--> %s\n", shortID(e.From), e.Relation, shortID(e.To))
	}
	return nil
}

func cmdMemoryAdd(args []string) error {
	fs := flag.NewFlagSet("memory add", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	typeF := fs.String("type", "", "node type (finding|decision|fact|skill|event|artifact|member|research_session)")
	titleF := fs.String("title", "", "node title (required, <=200 chars)")
	bodyF := fs.String("body", "", "markdown body (<=8 KiB)")
	tagsF := fs.String("tags", "", "comma-separated tags (<=16)")
	sessionF := fs.String("session", "", "research session this contributes to (id or prefix)")
	statusF := fs.String("status", "", "node status (default active)")
	idF := fs.String("id", "", "explicit node id (default: minted UUIDv4)")
	_ = fs.Parse(args)

	if *typeF == "" || *titleF == "" {
		return errors.New("usage: minti-cland memory add --type T --title \"...\" [--body ...] [--tags a,b] [--session id]")
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}

	n := memory.Node{
		ID:     *idF,
		Type:   *typeF,
		Title:  *titleF,
		Body:   *bodyF,
		Status: *statusF,
	}
	if *tagsF != "" {
		for _, t := range strings.Split(*tagsF, ",") {
			if t = strings.TrimSpace(t); t != "" {
				n.Tags = append(n.Tags, t)
			}
		}
	}
	if *sessionF != "" {
		g, err := fetchMemoryGraph(cli, base)
		if err != nil {
			return err
		}
		sessionID, err := resolveNodeID(g, *sessionF)
		if err != nil {
			return err
		}
		n.SessionID = sessionID
	}

	stored, err := postMemoryNode(cli, base, n)
	if err != nil {
		return err
	}

	// Contribution edge per spec §13.7: node --contributes_to--> session.
	if stored.SessionID != "" {
		edgeBody, _ := json.Marshal(memory.Edge{From: stored.ID, To: stored.SessionID, Relation: "contributes_to"})
		resp, err := cli.Post(base+"/clan/memory/edge", "application/json", edgeBody)
		if err != nil {
			return fmt.Errorf("node stored but contributes_to edge failed: %w", err)
		}
		_ = resp.Body.Close()
	}

	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(stored)
	}
	fmt.Printf("Memory node added: %s (%s, rev %d)\n", stored.ID, stored.Type, stored.Rev)
	return nil
}

func cmdMemoryLink(args []string) error {
	fs := flag.NewFlagSet("memory link", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	relF := fs.String("relation", "relates", "edge relation (relates|supersedes|derived_from|contributes_to|about_member|caused_by)")
	_ = fs.Parse(args)
	pos, err := splitPositionals(fs, 2)
	if err != nil || len(pos) < 2 {
		return errors.New("usage: minti-cland memory link <from> <to> [--relation relates]")
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}
	from, err := resolveNodeID(g, pos[0])
	if err != nil {
		return err
	}
	to, err := resolveNodeID(g, pos[1])
	if err != nil {
		return err
	}

	body, _ := json.Marshal(memory.Edge{From: from, To: to, Relation: *relF})
	resp, err := cli.Post(base+"/clan/memory/edge", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("memory link (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		fmt.Println(strings.TrimSpace(string(raw)))
		return nil
	}
	var er memory.EdgeResponse
	_ = json.Unmarshal(raw, &er)
	if er.Added {
		fmt.Printf("Linked %s --%s--> %s\n", shortID(from), *relF, shortID(to))
	} else {
		fmt.Println("Edge already exists (no-op).")
	}
	return nil
}

func cmdMemoryArchive(args []string) error {
	fs := flag.NewFlagSet("memory archive", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)
	pos, err := splitPositionals(fs, 1)
	if err != nil || len(pos) == 0 {
		return errors.New("usage: minti-cland memory archive <id>")
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}
	nodeID, err := resolveNodeID(g, pos[0])
	if err != nil {
		return err
	}
	var target *memory.Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == nodeID {
			target = &g.Nodes[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no node %q", pos[0])
	}
	if target.Status == "archived" {
		fmt.Printf("Node %s is already archived.\n", shortID(nodeID))
		return nil
	}
	update := *target
	update.Status = "archived"
	stored, err := postMemoryNode(cli, base, update)
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(stored)
	}
	fmt.Printf("Archived %s (%s) — tombstone gossips to the Clan.\n", shortID(stored.ID), stored.Title)
	return nil
}

func cmdMemoryDigest(args []string) error {
	fs := flag.NewFlagSet("memory digest", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/clan/memory/digest", nil)
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("memory digest (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		_, _ = io.Copy(os.Stdout, resp.Body)
		return nil
	}
	var dr memory.DigestResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return err
	}
	fmt.Println(dr.Digest)
	return nil
}

func cmdMemoryResearch(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: minti-cland memory research <start|close|list> [args]")
	}
	switch args[0] {
	case "start":
		return cmdResearchStart(args[1:])
	case "close":
		return cmdResearchClose(args[1:])
	case "list":
		return cmdResearchList(args[1:])
	default:
		return fmt.Errorf("unknown research subcommand %q", args[0])
	}
}

func cmdResearchStart(args []string) error {
	fs := flag.NewFlagSet("memory research start", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)
	pos, err := splitPositionals(fs, -1)
	if err != nil || len(pos) == 0 || strings.TrimSpace(pos[0]) == "" {
		return errors.New("usage: minti-cland memory research start \"<title>\"")
	}
	title := strings.Join(pos, " ")

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	stored, err := postMemoryNode(cli, base, memory.Node{
		Type:   "research_session",
		Title:  title,
		Status: "active",
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(stored)
	}
	fmt.Printf("Research session started: %s\n", stored.ID)
	fmt.Printf("Contribute with:\n  minti-cland memory add --type finding --session %s --title \"...\"\n", shortID(stored.ID))
	return nil
}

func cmdResearchClose(args []string) error {
	fs := flag.NewFlagSet("memory research close", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)
	pos, err := splitPositionals(fs, 1)
	if err != nil || len(pos) == 0 {
		return errors.New("usage: minti-cland memory research close <id>")
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}
	sessionID, err := resolveNodeID(g, pos[0])
	if err != nil {
		return err
	}
	var target *memory.Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == sessionID {
			target = &g.Nodes[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no node %q", pos[0])
	}
	if target.Type != "research_session" {
		return fmt.Errorf("node %s is a %s, not a research_session", shortID(sessionID), target.Type)
	}
	if target.Status == "archived" {
		fmt.Printf("Session %s is already closed.\n", shortID(sessionID))
		return nil
	}
	update := *target
	update.Status = "archived"
	stored, err := postMemoryNode(cli, base, update)
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(stored)
	}
	fmt.Printf("Research session closed: %s (%s)\n", shortID(stored.ID), stored.Title)
	return nil
}

func cmdResearchList(args []string) error {
	fs := flag.NewFlagSet("memory research list", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	g, err := fetchMemoryGraph(cli, base)
	if err != nil {
		return err
	}

	contributions := map[string]int{}
	for _, n := range g.Nodes {
		if n.SessionID != "" {
			contributions[n.SessionID]++
		}
	}
	var sessions []memory.Node
	for _, n := range g.Nodes {
		if n.Type == "research_session" {
			sessions = append(sessions, n)
		}
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(sessions)
	}
	if len(sessions) == 0 {
		fmt.Println("No research sessions. Start one:\n  minti-cland memory research start \"<title>\"")
		return nil
	}
	fmt.Printf("Research sessions (%d):\n", len(sessions))
	for _, s := range sessions {
		state := "open"
		if s.Status == "archived" {
			state = "closed"
		}
		fmt.Printf("  %s  %-6s %3d contributions  %s\n",
			shortID(s.ID), state, contributions[s.ID], s.Title)
	}
	return nil
}

// ---------- scribe (Memory M3, spec 13.8) ----------

func cmdScribe(args []string) error {
	fs := flag.NewFlagSet("scribe", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	jsonOut := fs.Bool("json", false, "raw JSON output")
	_ = fs.Parse(args)

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/clan/scribe", nil)
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("scribe (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if *jsonOut {
		_, _ = io.Copy(os.Stdout, resp.Body)
		return nil
	}
	var sr election.ScribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return err
	}
	fmt.Printf("Self:    %s\n", sr.Self)
	if sr.CurrentScribe == "" {
		fmt.Println("Scribe:  (none - no scribe-capable active member)")
	} else {
		tag := ""
		if sr.IsSelf {
			tag = " (self)"
		}
		fmt.Printf("Scribe:  %s%s\n", sr.CurrentScribe, tag)
	}
	if sr.PinnedScribe {
		fmt.Println("Pin:     this member is scribe-pinned")
	}
	return nil
}

func cmdPinScribe(args []string) error {
	fs := flag.NewFlagSet("pin-scribe", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultConfigPath(), "")
	stateDirFlag := fs.String("state", "", "")
	selfFlag := fs.Bool("self", false, "pin this member as the Scribe")
	clearFlag := fs.Bool("clear", false, "clear this member's scribe pin")
	_ = fs.Parse(args)

	if *selfFlag == *clearFlag {
		return errors.New("usage: minti-cland pin-scribe --self | --clear")
	}

	cli, base, err := memoryDaemon(*cfgPath, *stateDirFlag)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(election.PinScribeRequest{Value: *selfFlag})
	resp, err := cli.Post(base+"/clan/pin-scribe", "application/json", body)
	if err != nil {
		return fmt.Errorf("call local daemon: %w (is minti-cland running?)", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pin-scribe (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var pr election.PinScribeResponse
	if err := json.Unmarshal(bytes.TrimSpace(raw), &pr); err != nil {
		return err
	}
	if pr.PinnedScribe {
		fmt.Println("Scribe pin set - takes effect on the Orchestrator's next selection tick.")
	} else {
		fmt.Println("Scribe pin cleared.")
	}
	return nil
}
