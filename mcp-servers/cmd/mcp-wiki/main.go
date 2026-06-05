// minti-mcp-wiki — offline-Wikipedia tools backed by a local kiwix-serve.
//
// Two tools:
//   wiki_search(query, limit)  — OPDS search across all ZIMs in the kiwix
//                                library, returns [{title, url, snippet}]
//   wiki_get(path)             — fetch the article HTML at <path> (the `url`
//                                from a prior search result), strip tags, return
//                                plain text
//
// Reads /etc/minti/wiki.yaml for the kiwix-serve endpoint; falls back to
// http://127.0.0.1:8888 (the minti-pack-wiki-simple default). Loopback-only
// by convention — Clan members reach this MCP server via cland's
// /mcp/execute (Phase G); raw HTTP access is not part of the contract.
//
// Policy: see policy.WikiPolicy. Only knob is `wiki.deny_tools` to drop a
// specific tool. Audit log entry per call via the shared mcpserve framework.
package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
)

var version = "0.1.0-M6"

const (
	defaultBaseURL  = "http://127.0.0.1:8888"
	defaultConfPath = "/etc/minti/wiki.yaml"
	maxSearchLimit  = 50
	maxArticleBytes = 1 << 20 // 1 MiB — covers any reasonable article
	httpTimeout     = 15 * time.Second
)

type wikiConfig struct {
	Kiwix struct {
		BaseURL string `yaml:"base_url"`
	} `yaml:"kiwix"`
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-wiki: %v", err)
	}
}

func run() error {
	base, err := loadBaseURL()
	if err != nil {
		return err
	}
	pol, err := policy.Load()
	if err != nil {
		return err
	}
	logger, err := audit.Default()
	if err != nil {
		return err
	}

	srv := mcpserve.New("minti-mcp-wiki", version, pol, logger)
	httpc := &http.Client{Timeout: httpTimeout}

	mcpserve.AddTool(srv, &mcp.Tool{
		Name: "wiki_search",
		Description: "Full-text search the local offline Wikipedia (kiwix-serve). " +
			"Returns up to `limit` hits as {title, url, snippet}. " +
			"Pass the returned `url` to wiki_get to fetch an article.",
	}, func(ctx context.Context, in WikiSearchIn) (WikiSearchOut, error) {
		return wikiSearch(ctx, httpc, base, in)
	})

	mcpserve.AddTool(srv, &mcp.Tool{
		Name: "wiki_get",
		Description: "Fetch an article from offline Wikipedia (kiwix-serve) at " +
			"`path` (the `url` returned by wiki_search). Returns plain text " +
			"with HTML stripped. Capped at 1 MiB.",
	}, func(ctx context.Context, in WikiGetIn) (WikiGetOut, error) {
		return wikiGet(ctx, httpc, base, in)
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

// loadBaseURL reads /etc/minti/wiki.yaml if present, otherwise returns the
// default. Allows operator override via $MINTI_WIKI_BASE_URL for tests.
func loadBaseURL() (string, error) {
	if v := os.Getenv("MINTI_WIKI_BASE_URL"); v != "" {
		return v, nil
	}
	b, err := os.ReadFile(defaultConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultBaseURL, nil
		}
		return "", fmt.Errorf("read %s: %w", defaultConfPath, err)
	}
	var c wikiConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		return "", fmt.Errorf("parse %s: %w", defaultConfPath, err)
	}
	if c.Kiwix.BaseURL == "" {
		return defaultBaseURL, nil
	}
	return strings.TrimRight(c.Kiwix.BaseURL, "/"), nil
}

// ---------- wiki_search ----------

type WikiSearchIn struct {
	Query string `json:"query" jsonschema:"search terms (free-form text)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max hits to return (default 10, max 50)"`
}

type wikiHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type WikiSearchOut struct {
	Query string    `json:"query"`
	Hits  []wikiHit `json:"hits"`
}

// OPDS feed shape — minimal subset of what we need from kiwix-serve's
// `?format=xml` response. The kiwix-serve responses follow Atom + OPDS
// extensions; we only read <entry><title> + <link href> + <summary>.
type opdsFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []opdsEntry `xml:"entry"`
}

type opdsEntry struct {
	Title   string     `xml:"title"`
	Summary string     `xml:"summary"`
	Links   []opdsLink `xml:"link"`
}

type opdsLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

func wikiSearch(ctx context.Context, c *http.Client, base string, in WikiSearchIn) (WikiSearchOut, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return WikiSearchOut{}, fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	endpoint := fmt.Sprintf("%s/search?pattern=%s&pageLength=%d",
		base, url.QueryEscape(q), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WikiSearchOut{}, err
	}
	// kiwix-serve serves OPDS XML when this header is set; the alternative
	// `&format=xml` query param works on some versions but isn't universal.
	req.Header.Set("Accept", "application/atom+xml")
	req.Header.Set("User-Agent", "minti-mcp-wiki/"+version)

	resp, err := c.Do(req)
	if err != nil {
		return WikiSearchOut{}, fmt.Errorf("kiwix-serve unreachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return WikiSearchOut{}, fmt.Errorf("kiwix-serve returned %s for %s", resp.Status, endpoint)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return WikiSearchOut{}, err
	}

	out := WikiSearchOut{Query: q, Hits: []wikiHit{}}
	ct := resp.Header.Get("Content-Type")

	if strings.Contains(ct, "xml") || strings.Contains(ct, "atom") {
		var feed opdsFeed
		if err := xml.Unmarshal(body, &feed); err != nil {
			return out, fmt.Errorf("parse OPDS: %w", err)
		}
		for _, e := range feed.Entries {
			h := wikiHit{Title: strings.TrimSpace(e.Title), Snippet: strings.TrimSpace(stripTags(e.Summary))}
			// Prefer the rel="" / type="text/html" link (the article); fall
			// back to the first link.
			for _, l := range e.Links {
				if l.Type == "text/html" || l.Rel == "alternate" {
					h.URL = l.Href
					break
				}
			}
			if h.URL == "" && len(e.Links) > 0 {
				h.URL = e.Links[0].Href
			}
			if h.Title != "" {
				out.Hits = append(out.Hits, h)
			}
		}
	} else {
		// Fallback: scrape titles + relative-href anchors from the HTML
		// search result page. Older / minimal kiwix-serve installs don't
		// honour the Accept header.
		out.Hits = scrapeHTMLResults(string(body), limit)
	}

	return out, nil
}

// scrapeHTMLResults pulls (href, title) pairs out of kiwix-serve's HTML
// search response. Pattern is: <a class="zim-link" href="/viewer#book/A/Foo">Foo</a>
// — but the class name varies, so we match more loosely. Best-effort.
var htmlResultRE = regexp.MustCompile(`<a[^>]+href="([^"]+)"[^>]*>([^<]{1,200})</a>`)

func scrapeHTMLResults(body string, limit int) []wikiHit {
	hits := []wikiHit{}
	matches := htmlResultRE.FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		href := strings.TrimSpace(m[1])
		title := strings.TrimSpace(stripTags(m[2]))
		// Filter out non-article links (assets, search itself, etc.).
		if title == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "?") {
			continue
		}
		if strings.Contains(href, "/skin/") || strings.Contains(href, "/search") {
			continue
		}
		if seen[href] {
			continue
		}
		seen[href] = true
		hits = append(hits, wikiHit{Title: title, URL: href})
		if len(hits) >= limit {
			break
		}
	}
	return hits
}

// ---------- wiki_get ----------

type WikiGetIn struct {
	Path string `json:"path" jsonschema:"article path from a wiki_search hit's url field, e.g. /viewer#wikipedia_en_simple_all_nopic/A/Paris"`
}

type WikiGetOut struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
}

func wikiGet(ctx context.Context, c *http.Client, base string, in WikiGetIn) (WikiGetOut, error) {
	p := strings.TrimSpace(in.Path)
	if p == "" {
		return WikiGetOut{}, fmt.Errorf("path is required")
	}
	// Kiwix viewer paths use a "#" anchor (`/viewer#book/A/Article`). The
	// real article HTML lives at `/raw/<book>/A/<Article>` or
	// `/content/<book>/A/<Article>` depending on version. Translate the
	// viewer form to /content so we can fetch raw HTML.
	if strings.HasPrefix(p, "/viewer#") {
		p = "/content/" + strings.TrimPrefix(p, "/viewer#")
	}
	// Allow callers to pass either a path-only string or a full URL —
	// reject only schemes other than http/https.
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		// pass through
	} else if !strings.HasPrefix(p, "/") {
		return WikiGetOut{}, fmt.Errorf("path must begin with '/' (got %q)", p)
	}

	full := p
	if strings.HasPrefix(p, "/") {
		full = base + p
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return WikiGetOut{}, err
	}
	req.Header.Set("User-Agent", "minti-mcp-wiki/"+version)

	resp, err := c.Do(req)
	if err != nil {
		return WikiGetOut{}, fmt.Errorf("kiwix-serve unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return WikiGetOut{}, fmt.Errorf("kiwix-serve returned %s for %s", resp.Status, full)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArticleBytes))
	if err != nil {
		return WikiGetOut{}, err
	}

	title := extractTitle(string(body))
	text := stripTags(string(body))
	text = collapseWhitespace(text)

	return WikiGetOut{URL: full, Title: title, Text: text}, nil
}

// ---------- helpers ----------

var (
	tagRE        = regexp.MustCompile(`<[^>]+>`)
	scriptRE     = regexp.MustCompile(`(?s)<(script|style)\b[^>]*>.*?</(script|style)>`)
	titleRE      = regexp.MustCompile(`(?is)<title[^>]*>(.+?)</title>`)
	whitespaceRE = regexp.MustCompile(`[ \t]+`)
	blanklineRE  = regexp.MustCompile(`\n{3,}`)
)

func stripTags(s string) string {
	s = scriptRE.ReplaceAllString(s, "")
	s = tagRE.ReplaceAllString(s, "")
	// Decode the most common HTML entities. We don't pull in golang.org/x/net
	// just for this; the kiwix corpus is well-behaved English.
	s = strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
	).Replace(s)
	return s
}

func extractTitle(html string) string {
	m := titleRE.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(stripTags(m[1]))
}

func collapseWhitespace(s string) string {
	s = whitespaceRE.ReplaceAllString(s, " ")
	s = blanklineRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
