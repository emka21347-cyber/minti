// minti-mcp-fs — filesystem MCP server.
//
// Tools: read_text, write_text, list_dir, glob.
//
// Defense in depth: every path is resolved (Abs + EvalSymlinks where the file
// exists) and re-checked against $HOME before any IO. Policy on top of that
// can restrict further via mcp.fs.allow / mcp.fs.deny / mcp.fs.deny_tools.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
)

var version = "0.1.0-M2"

const defaultMaxBytes int64 = 1 << 20 // 1 MiB

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-fs: %v", err)
	}
}

func run() error {
	pol, err := policy.Load()
	if err != nil {
		return err
	}
	logger, err := audit.Default()
	if err != nil {
		return err
	}

	srv := mcpserve.New("minti-mcp-fs", version, pol, logger)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "read_text",
		Description: "Read a UTF-8 text file. Returns content (truncated at max_bytes), size, and a truncation flag.",
	}, readText)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "write_text",
		Description: "Write UTF-8 text to a file. Creates parent directories when create_dirs=true. Refuses to write outside $HOME.",
	}, writeText)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "list_dir",
		Description: "List entries in a directory. Returns name, is_dir, size, and modification time per entry.",
	}, listDir)

	mcpserve.AddTool(srv, &mcp.Tool{
		Name:        "glob",
		Description: "Match files against a glob pattern (Go filepath.Glob semantics). max_results caps the response.",
	}, glob)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

type ReadIn struct {
	Path     string `json:"path" jsonschema:"absolute or ~-prefixed path of the file to read"`
	MaxBytes int64  `json:"max_bytes,omitempty" jsonschema:"cap on bytes returned (default 1048576)"`
}
type ReadOut struct {
	Content   string `json:"content"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

func readText(_ context.Context, in ReadIn) (ReadOut, error) {
	resolved, err := safePath(in.Path)
	if err != nil {
		return ReadOut{}, err
	}
	limit := in.MaxBytes
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	f, err := os.Open(resolved)
	if err != nil {
		return ReadOut{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ReadOut{}, err
	}
	if st.IsDir() {
		return ReadOut{}, fmt.Errorf("path is a directory: %s", in.Path)
	}
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, errors.New("EOF")) && n == 0 {
		return ReadOut{}, err
	}
	return ReadOut{
		Content:   string(buf[:n]),
		Bytes:     int64(n),
		Truncated: st.Size() > int64(n),
	}, nil
}

type WriteIn struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs bool   `json:"create_dirs,omitempty"`
}
type WriteOut struct {
	Bytes int `json:"bytes"`
}

func writeText(_ context.Context, in WriteIn) (WriteOut, error) {
	resolved, err := safePathForWrite(in.Path)
	if err != nil {
		return WriteOut{}, err
	}
	if in.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return WriteOut{}, err
		}
	}
	if err := os.WriteFile(resolved, []byte(in.Content), 0o600); err != nil {
		return WriteOut{}, err
	}
	return WriteOut{Bytes: len(in.Content)}, nil
}

type ListIn struct {
	Path string `json:"path"`
}
type Entry struct {
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}
type ListOut struct {
	Entries []Entry `json:"entries"`
}

func listDir(_ context.Context, in ListIn) (ListOut, error) {
	resolved, err := safePath(in.Path)
	if err != nil {
		return ListOut{}, err
	}
	dirEntries, err := os.ReadDir(resolved)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{Entries: make([]Entry, 0, len(dirEntries))}
	for _, e := range dirEntries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, Entry{
			Name:     e.Name(),
			IsDir:    e.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	return out, nil
}

type GlobIn struct {
	Pattern    string `json:"pattern"`
	MaxResults int    `json:"max_results,omitempty"`
}
type GlobOut struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

func glob(_ context.Context, in GlobIn) (GlobOut, error) {
	pattern := expandHome(in.Pattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return GlobOut{}, err
	}
	max := in.MaxResults
	if max <= 0 || max > 1000 {
		max = 1000
	}
	out := GlobOut{}
	for _, m := range matches {
		if _, err := safePath(m); err != nil {
			continue // silently drop matches that escape the jail
		}
		out.Paths = append(out.Paths, m)
		if len(out.Paths) >= max {
			out.Truncated = len(matches) > len(out.Paths)
			break
		}
	}
	return out, nil
}

// safePath resolves the input path to an absolute path with symlinks expanded,
// verifies it falls under $HOME, and returns the resolved path. Used for read
// + list operations where the target must already exist.
func safePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expandHome(p))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !underHome(resolved, home) {
		return "", fmt.Errorf("path %s escapes home jail", p)
	}
	return resolved, nil
}

// safePathForWrite is safePath that tolerates non-existent leaves (so write
// can create new files), but requires the parent dir to be under $HOME.
func safePathForWrite(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expandHome(p))
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(abs)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		// Parent may also not exist yet — walk up until something resolves.
		resolvedParent, err = resolveExistingAncestor(parent)
		if err != nil {
			return "", err
		}
	}
	if !underHome(resolvedParent, home) {
		return "", fmt.Errorf("write target %s escapes home jail", p)
	}
	return abs, nil
}

func resolveExistingAncestor(p string) (string, error) {
	cur := p
	for cur != "" && cur != filepath.Dir(cur) {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return r, nil
		}
		cur = filepath.Dir(cur)
	}
	return "", fmt.Errorf("no existing ancestor for %s", p)
}

func underHome(path, home string) bool {
	path = filepath.Clean(path)
	home = filepath.Clean(home)
	if path == home {
		return true
	}
	return strings.HasPrefix(path, home+string(filepath.Separator))
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
