// minti-pack-fetch — content fetcher for MINTI addon packs.
//
// Reads /usr/share/minti/packs/<name>/manifest.json and dispatches by `kind`:
//
//	"ollama-model" → exec `ollama pull <tag>`. Idempotent (Ollama returns
//	                 immediately if the tag is already pulled).
//	"kiwix-zim"    → HTTP download to <dest>/<basename(url)>.partial with
//	                 Range-resume + SHA-256 verify, then atomic rename.
//
// Records install state at /var/lib/minti/packs/<name>.installed (single-line
// JSON: timestamp, kind, source/tag, sha256, dest) so minti-fetch can list
// installed addons.
//
// Designed to be called from Debian postinst scripts. Honours the env-var
// MINTI_PACK_NO_FETCH=1: writes the .installed marker but skips the actual
// download (the operator runs `minti-pack-fetch <name>` later when bandwidth
// and disk allow). This lets `apt install minti-pack-wiki-simple` complete
// fast on a slow link.
//
// Stdlib only — no third-party Go deps to maintain.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var version = "0.1.0-M6"

const (
	defaultManifestRoot = "/usr/share/minti/packs"
	defaultStateRoot    = "/var/lib/minti/packs"
	envNoFetch          = "MINTI_PACK_NO_FETCH"
	// diskHeadroom: free disk must exceed this multiple of manifest.size_bytes
	// before kiwix-zim downloads start. 1.25× covers the .partial file plus
	// transient filesystem journaling/working space without being excessive.
	diskHeadroom = 1.25
)

type Manifest struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`       // "ollama-model" | "kiwix-zim"
	Tag       string `json:"tag"`        // ollama-model: e.g. "hermes3:8b"
	URL       string `json:"url"`        // kiwix-zim: HTTPS source
	SHA256    string `json:"sha256"`     // kiwix-zim: hex digest (lowercase)
	SizeBytes int64  `json:"size_bytes"` // best-effort, for the disk precheck
	Dest      string `json:"dest"`       // kiwix-zim: destination directory
}

type Installed struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`             // tag (ollama) or url (kiwix)
	SHA256    string    `json:"sha256,omitempty"`   // kiwix only
	DestPath  string    `json:"dest_path,omitempty"`// kiwix only
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"fetcher_version"`
}

func main() {
	var (
		manifestRoot = flag.String("manifest-root", defaultManifestRoot, "root dir holding <name>/manifest.json")
		manifestPath = flag.String("manifest", "", "explicit manifest path (overrides -manifest-root + name)")
		stateRoot    = flag.String("state-root", defaultStateRoot, "where to record .installed markers")
		force        = flag.Bool("force", false, "re-fetch even if already installed")
		noFetch      = flag.Bool("no-fetch", false, "write .installed marker but skip actual download")
		list         = flag.Bool("list", false, "list known packs + install state and exit")
		showVersion  = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "minti-pack-fetch %s\n", version)
		fmt.Fprintln(os.Stderr, "usage: minti-pack-fetch [flags] <pack-name>")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *list {
		if err := listInstalled(*manifestRoot, *stateRoot); err != nil {
			fail(err)
		}
		return
	}

	if os.Getenv(envNoFetch) == "1" {
		*noFetch = true
	}

	name := strings.TrimSpace(flag.Arg(0))
	if name == "" && *manifestPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	mPath := *manifestPath
	if mPath == "" {
		mPath = filepath.Join(*manifestRoot, name, "manifest.json")
	}
	m, err := readManifest(mPath)
	if err != nil {
		fail(fmt.Errorf("read manifest %s: %w", mPath, err))
	}
	if name == "" {
		name = m.Name
	}
	if m.Name != "" && m.Name != name {
		fail(fmt.Errorf("manifest name %q does not match requested %q", m.Name, name))
	}

	if err := os.MkdirAll(*stateRoot, 0o755); err != nil {
		fail(fmt.Errorf("mkdir state-root: %w", err))
	}
	markerPath := filepath.Join(*stateRoot, name+".installed")

	if !*force && fileExists(markerPath) {
		fmt.Printf("minti-pack-fetch: %s already installed (use -force to refetch)\n", name)
		return
	}

	inst := Installed{
		Name:      name,
		Kind:      m.Kind,
		Timestamp: time.Now().UTC(),
		Version:   version,
	}

	switch m.Kind {
	case "ollama-model":
		inst.Source = m.Tag
		if *noFetch {
			fmt.Printf("minti-pack-fetch: MINTI_PACK_NO_FETCH=1 → skipping `ollama pull %s`\n", m.Tag)
		} else if err := fetchOllama(m); err != nil {
			fail(err)
		}
	case "kiwix-zim":
		inst.Source = m.URL
		inst.SHA256 = m.SHA256
		dest, err := fetchKiwix(m, *noFetch)
		if err != nil {
			fail(err)
		}
		inst.DestPath = dest
	default:
		fail(fmt.Errorf("unknown manifest kind %q (expected ollama-model | kiwix-zim)", m.Kind))
	}

	if err := writeInstalled(markerPath, &inst); err != nil {
		fail(fmt.Errorf("write %s: %w", markerPath, err))
	}
	fmt.Printf("minti-pack-fetch: %s done (kind=%s)\n", name, m.Kind)
}

// ---------- manifest + state I/O ----------

func readManifest(p string) (*Manifest, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m.Kind == "" {
		return nil, errors.New("manifest.kind is required")
	}
	return &m, nil
}

func writeInstalled(path string, i *Installed) error {
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---------- ollama-model ----------

func fetchOllama(m *Manifest) error {
	if m.Tag == "" {
		return errors.New("ollama-model manifest missing `tag`")
	}
	if _, err := exec.LookPath("ollama"); err != nil {
		return fmt.Errorf("ollama not on PATH — install it first: %w", err)
	}
	fmt.Printf("minti-pack-fetch: ollama pull %s\n", m.Tag)
	cmd := exec.Command("ollama", "pull", m.Tag)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		hint := ""
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			hint = "\n  hint: is the ollama daemon running? Try `systemctl start ollama` or `ollama serve` in another terminal."
		}
		return fmt.Errorf("ollama pull failed: %w%s", err, hint)
	}
	return nil
}

// ---------- kiwix-zim ----------

func fetchKiwix(m *Manifest, noFetch bool) (string, error) {
	if m.URL == "" {
		return "", errors.New("kiwix-zim manifest missing `url`")
	}
	if m.Dest == "" {
		return "", errors.New("kiwix-zim manifest missing `dest`")
	}
	if err := os.MkdirAll(m.Dest, 0o755); err != nil {
		return "", fmt.Errorf("mkdir dest %s: %w", m.Dest, err)
	}
	finalPath := filepath.Join(m.Dest, path.Base(m.URL))
	if noFetch {
		fmt.Printf("minti-pack-fetch: MINTI_PACK_NO_FETCH=1 → skipping download of %s\n", m.URL)
		return finalPath, nil
	}

	if fileExists(finalPath) {
		fmt.Printf("minti-pack-fetch: %s already present\n", finalPath)
		if m.SHA256 != "" {
			fmt.Println("minti-pack-fetch: verifying existing file SHA-256...")
			sum, err := sha256OfFile(finalPath)
			if err != nil {
				return "", err
			}
			if !strings.EqualFold(sum, m.SHA256) {
				return "", fmt.Errorf("existing file SHA-256 mismatch (got %s, want %s); rerun with -force after removing the file", sum, m.SHA256)
			}
			fmt.Println("minti-pack-fetch: SHA-256 OK")
		}
		return finalPath, nil
	}

	if m.SizeBytes > 0 {
		free, err := freeDiskBytes(m.Dest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "minti-pack-fetch: warn: couldn't probe free disk: %v\n", err)
		} else {
			needed := int64(float64(m.SizeBytes) * diskHeadroom)
			if free < needed {
				return "", fmt.Errorf("not enough free disk in %s: have %s, need %s (%.2f× of %s payload)",
					m.Dest, humanBytes(free), humanBytes(needed), diskHeadroom, humanBytes(m.SizeBytes))
			}
		}
	}

	partial := finalPath + ".partial"
	if err := downloadResumable(m.URL, partial); err != nil {
		return "", err
	}

	if m.SHA256 != "" {
		fmt.Println("minti-pack-fetch: verifying SHA-256...")
		sum, err := sha256OfFile(partial)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(sum, m.SHA256) {
			return "", fmt.Errorf("SHA-256 mismatch (got %s, want %s) — leaving %s on disk for inspection", sum, m.SHA256, partial)
		}
		fmt.Println("minti-pack-fetch: SHA-256 OK")
	}

	if err := os.Rename(partial, finalPath); err != nil {
		return "", fmt.Errorf("rename %s -> %s: %w", partial, finalPath, err)
	}
	return finalPath, nil
}

// downloadResumable streams URL into dst, resuming from dst's current size
// via an HTTP Range header. Reports progress to stderr roughly once per second.
func downloadResumable(url, dst string) error {
	var startAt int64
	if st, err := os.Stat(dst); err == nil {
		startAt = st.Size()
		fmt.Printf("minti-pack-fetch: resuming %s from %s\n", dst, humanBytes(startAt))
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if startAt > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startAt))
	}
	req.Header.Set("User-Agent", "minti-pack-fetch/"+version)

	client := &http.Client{Timeout: 0} // unlimited; large blobs
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored Range (or this is a fresh download). If we have
		// data already and got a 200, the server doesn't support Range —
		// restart from scratch.
		if startAt > 0 {
			fmt.Fprintln(os.Stderr, "minti-pack-fetch: server returned 200 (no Range support); restarting download")
			if err := os.Truncate(dst, 0); err != nil {
				return err
			}
			startAt = 0
		}
	case http.StatusPartialContent:
		// good — server honoured Range
	default:
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	total := startAt
	if resp.ContentLength > 0 {
		total += resp.ContentLength
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	pr := &progressReader{r: resp.Body, startAt: startAt, total: total, lastTick: time.Now()}
	if _, err := io.Copy(f, pr); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	pr.Flush()
	return nil
}

type progressReader struct {
	r        io.Reader
	startAt  int64
	got      int64
	total    int64 // 0 if unknown
	lastTick time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.got += int64(n)
	if time.Since(p.lastTick) > time.Second {
		p.report()
		p.lastTick = time.Now()
	}
	return n, err
}

func (p *progressReader) Flush() { p.report() }

func (p *progressReader) report() {
	have := p.startAt + p.got
	if p.total > 0 {
		pct := float64(have) / float64(p.total) * 100
		fmt.Fprintf(os.Stderr, "  %s / %s  (%5.1f%%)\n", humanBytes(have), humanBytes(p.total), pct)
	} else {
		fmt.Fprintf(os.Stderr, "  %s\n", humanBytes(have))
	}
}

// ---------- helpers ----------

func sha256OfFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// freeDiskBytes is platform-specific. See disk_linux.go / disk_other.go.

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	pre := []string{"K", "M", "G", "T", "P"}[exp]
	return fmt.Sprintf("%.2f %sB", float64(n)/float64(div), pre)
}

// ---------- -list ----------

func listInstalled(manifestRoot, stateRoot string) error {
	entries, err := os.ReadDir(manifestRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Printf("%-20s %-13s %s\n", "PACK", "KIND", "STATE")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		mpath := filepath.Join(manifestRoot, name, "manifest.json")
		m, err := readManifest(mpath)
		if err != nil {
			continue
		}
		state := "not installed"
		marker := filepath.Join(stateRoot, name+".installed")
		if b, err := os.ReadFile(marker); err == nil {
			var inst Installed
			if json.Unmarshal(b, &inst) == nil {
				state = "installed " + inst.Timestamp.Format("2006-01-02")
			} else {
				state = "installed (marker unparseable)"
			}
		}
		fmt.Printf("%-20s %-13s %s\n", name, m.Kind, state)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "minti-pack-fetch: error: %v\n", err)
	os.Exit(1)
}
