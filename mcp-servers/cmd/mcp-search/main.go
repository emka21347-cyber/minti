// minti-mcp-search — keyless web search backed by DuckDuckGo's HTML endpoint.
//
// One tool:
//   web_search(query, limit) — query html.duckduckgo.com and return
//                              [{title, url, snippet}]. No API key, no JS; the
//                              HTML is parsed with stdlib regex and DDG's
//                              `/l/?uddg=` redirect wrappers are unwrapped to the
//                              real target URL.
//
// Loopback-only by convention: Clan members reach this MCP server via cland's
// /mcp/execute (Phase G), not raw HTTP. Policy: see policy.SearchPolicy (only
// knob is search.deny_tools). Audit log entry per call via mcpserve.
//
// P1-clean: stdlib + the MCP SDK only, no scraping libraries.
package main

import (
	"context"
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

	"github.com/minti/mcp-servers/internal/audit"
	"github.com/minti/mcp-servers/internal/mcpserve"
	"github.com/minti/mcp-servers/internal/policy"
)

var version = "0.1.0-M1"

const (
	defaultBaseURL = "https://html.duckduckgo.com/html/"
	maxSearchLimit = 25
	maxBodyBytes   = 2 << 20 // 2 MiB — the DDG HTML results page
	httpTimeout    = 20 * time.Second
)

// A browser-ish UA: the HTML endpoint returns an empty/challenge page to
// obviously-bot clients. (var, not const — interpolates the version var.)
var userAgent = "Mozilla/5.0 (compatible; minti-mcp-search/" + version + ")"

func main() {
	if err := run(); err != nil {
		log.Fatalf("minti-mcp-search: %v", err)
	}
}

func run() error {
	base := defaultBaseURL
	if v := os.Getenv("MINTI_SEARCH_BASE_URL"); v != "" {
		base = v
	}
	pol, err := policy.Load()
	if err != nil {
		return err
	}
	logger, err := audit.Default()
	if err != nil {
		return err
	}

	srv := mcpserve.New("minti-mcp-search", version, pol, logger)
	httpc := &http.Client{Timeout: httpTimeout}

	mcpserve.AddTool(srv, &mcp.Tool{
		Name: "web_search",
		Description: "Search the web via DuckDuckGo (no API key). Returns up to " +
			"`limit` results as {title, url, snippet}. Use this to find current " +
			"information online; fetch a result's url with fetch_url for the full page.",
	}, func(ctx context.Context, in WebSearchIn) (WebSearchOut, error) {
		return webSearch(ctx, httpc, base, in)
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return srv.Run(ctx)
}

// ---------- web_search ----------

type WebSearchIn struct {
	Query string `json:"query" jsonschema:"search terms (free-form text)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results to return (default 8, max 25)"`
}

type searchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type WebSearchOut struct {
	Query   string      `json:"query"`
	Results []searchHit `json:"results"`
}

func webSearch(ctx context.Context, c *http.Client, base string, in WebSearchIn) (WebSearchOut, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return WebSearchOut{}, fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	endpoint := base
	if strings.Contains(base, "?") {
		endpoint += "&q=" + url.QueryEscape(q)
	} else {
		endpoint += "?q=" + url.QueryEscape(q)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WebSearchOut{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")

	resp, err := c.Do(req)
	if err != nil {
		return WebSearchOut{}, fmt.Errorf("duckduckgo unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return WebSearchOut{}, fmt.Errorf("duckduckgo returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return WebSearchOut{}, err
	}

	results := parseDDGResults(string(body), limit)
	if len(results) == 0 {
		// Tolerant, structured: a zero parse usually means DDG rate-limited us
		// or changed its markup, not that there are truly no hits.
		return WebSearchOut{Query: q, Results: []searchHit{}},
			fmt.Errorf("no results parsed (DuckDuckGo may have rate-limited or changed its HTML format)")
	}
	return WebSearchOut{Query: q, Results: results}, nil
}

// ---------- HTML parsing (pure; fixture-tested) ----------

var (
	resultLinkRE    = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result__a[^"]*"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	resultSnippetRE = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	tagRE           = regexp.MustCompile(`<[^>]+>`)
)

// parseDDGResults extracts result links + snippets from a DuckDuckGo HTML
// results page. Titles and snippets appear in matching order, so they are
// zipped by index; missing snippets are tolerated. The href is unwrapped from
// DDG's /l/?uddg= redirect to the real target.
func parseDDGResults(html string, limit int) []searchHit {
	links := resultLinkRE.FindAllStringSubmatch(html, -1)
	snips := resultSnippetRE.FindAllStringSubmatch(html, -1)

	out := make([]searchHit, 0, len(links))
	seen := map[string]bool{}
	for i, m := range links {
		target := unwrapDDGURL(m[1])
		title := cleanText(m[2])
		if target == "" || title == "" || seen[target] {
			continue
		}
		seen[target] = true
		hit := searchHit{Title: title, URL: target}
		if i < len(snips) {
			hit.Snippet = cleanText(snips[i][1])
		}
		out = append(out, hit)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// unwrapDDGURL turns a DuckDuckGo result href into the real destination URL.
// DDG wraps results as `//duckduckgo.com/l/?uddg=<urlencoded target>&rut=...`;
// some hrefs are already direct. Returns "" if nothing usable is found.
func unwrapDDGURL(href string) string {
	href = htmlUnescape(strings.TrimSpace(href))
	if href == "" {
		return ""
	}
	parseTarget := href
	if strings.HasPrefix(parseTarget, "//") {
		parseTarget = "https:" + parseTarget
	}
	if u, err := url.Parse(parseTarget); err == nil {
		if uddg := u.Query().Get("uddg"); uddg != "" {
			return uddg // url.Parse already decoded the query value
		}
	}
	// Not a redirect wrapper — accept direct http(s) URLs as-is.
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return ""
}

func cleanText(s string) string {
	s = tagRE.ReplaceAllString(s, "")
	s = htmlUnescape(s)
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func htmlUnescape(s string) string {
	return strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&#x27;", "'",
		"&apos;", "'",
		"&nbsp;", " ",
	).Replace(s)
}
